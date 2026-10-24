package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/github-radar/github-radar/internal/adminrepos"
	"github.com/github-radar/github-radar/internal/api"
	"github.com/github-radar/github-radar/internal/auth"
	"github.com/github-radar/github-radar/internal/backups"
	"github.com/github-radar/github-radar/internal/classify"
	"github.com/github-radar/github-radar/internal/config"
	"github.com/github-radar/github-radar/internal/credentials"
	"github.com/github-radar/github-radar/internal/db"
	"github.com/github-radar/github-radar/internal/readmes"
	"github.com/github-radar/github-radar/internal/scheduler"
	"github.com/github-radar/github-radar/internal/settings"
	"golang.org/x/term"
)

type passwordReader func(fd int) ([]byte, error)

type commandDeps struct {
	serve            func(context.Context, io.Writer) error
	healthcheck      func(context.Context) error
	adminCreate      func(context.Context, io.Reader, io.Writer, passwordReader) error
	backup           func(context.Context, io.Writer) error
	classifyBackfill func(context.Context, io.Writer) error
}

const usage = "usage: radar serve | radar healthcheck | radar admin create | radar backup | radar classify backfill"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, term.ReadPassword); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, input io.Reader, output io.Writer, readPassword passwordReader) error {
	return runWithDeps(ctx, args, input, output, readPassword, commandDeps{
		serve: runServe, healthcheck: runHealthcheck, adminCreate: runAdminCreate, backup: runBackup,
		classifyBackfill: runClassifyBackfill,
	})
}

func runWithDeps(ctx context.Context, args []string, input io.Reader, output io.Writer, readPassword passwordReader, deps commandDeps) error {
	switch {
	case len(args) == 1 && args[0] == "serve":
		if deps.serve == nil {
			return errors.New("serve command is unavailable")
		}
		return deps.serve(ctx, output)
	case len(args) == 1 && args[0] == "healthcheck":
		if deps.healthcheck == nil {
			return errors.New("healthcheck command is unavailable")
		}
		return deps.healthcheck(ctx)
	case len(args) == 2 && args[0] == "admin" && args[1] == "create":
		if deps.adminCreate == nil {
			return errors.New("admin create command is unavailable")
		}
		return deps.adminCreate(ctx, input, output, readPassword)
	case len(args) == 1 && args[0] == "backup":
		if deps.backup == nil {
			return errors.New("backup command is unavailable")
		}
		return deps.backup(ctx, output)
	case len(args) == 2 && args[0] == "classify" && args[1] == "backfill":
		if deps.classifyBackfill == nil {
			return errors.New("classify backfill command is unavailable")
		}
		return deps.classifyBackfill(ctx, output)
	default:
		return errors.New(usage)
	}
}

func runServe(ctx context.Context, output io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(output, nil))
	for _, warning := range cfg.Warnings {
		logger.Warn("configuration warning", "warning", warning)
	}
	store, err := db.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer store.Close()

	credentialService, err := credentials.NewService(store.SQL(), cfg.EncryptionKey, credentials.WithGitHubBaseURL(cfg.GitHubAPIBaseURL))
	if err != nil {
		return fmt.Errorf("initialize GitHub credential store: %w", err)
	}
	searcherSource, err := scheduler.CredentialSearcherSource(credentialService, cfg.GitHubAPIBaseURL)
	if err != nil {
		return fmt.Errorf("initialize GitHub searcher: %w", err)
	}
	repositorySource, err := scheduler.CredentialRepositoryGetterSource(credentialService, cfg.GitHubAPIBaseURL)
	if err != nil {
		return fmt.Errorf("initialize GitHub repository detail client: %w", err)
	}
	repositoryAdminService := adminrepos.New(store.SQL(), func(ctx context.Context) (adminrepos.RepositoryGetter, error) {
		return repositorySource(ctx)
	})
	jobScheduler := scheduler.New(store.SQL(), searcherSource, repositorySource)
	readmeService := readmes.NewService(store.SQL(), readmes.NewGitHubProvider(credentialService, cfg.GitHubAPIBaseURL), time.Now, logger)
	settingsCipher, err := credentials.NewCipher(cfg.EncryptionKey, nil)
	if err != nil {
		return fmt.Errorf("initialize settings cipher: %w", err)
	}
	siteSettings := settings.NewService(store.SQL(), settingsCipher, time.Now)
	backupService, err := backups.NewService(store.SQL(), backupDirectory(cfg.DatabasePath), time.Now)
	if err != nil {
		return fmt.Errorf("initialize backup service: %w", err)
	}
	if err := jobScheduler.EnsureJob(ctx); err != nil {
		return err
	}
	recoveryScheduler := scheduler.NewRecovery(store.SQL(), searcherSource)
	if err := recoveryScheduler.EnsureJob(ctx); err != nil {
		return err
	}
	collectorCtx, stopCollector := context.WithCancel(context.Background())
	defer stopCollector()
	go runCollectionLoop(collectorCtx, store.SQL(), jobScheduler, recoveryScheduler, logger)
	go runBackupLoop(collectorCtx, backupService, logger)

	server := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           api.New(api.Config{DB: store, Auth: auth.NewService(store.SQL()), Credentials: credentialService, Scheduler: jobScheduler, Readmes: readmeService, Settings: siteSettings, Backups: backupService, Repositories: repositoryAdminService, PublicBaseURL: cfg.PublicBaseURL, CookieSecure: cfg.CookieSecure, StaticDir: cfg.StaticDir, AppEnv: cfg.AppEnv, Logger: logger}),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20,
	}
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-signalCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("http server shutdown failed", "error", err)
		}
	}()
	logger.Info("http server starting", "address", server.Addr)
	err = server.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		stop()
		return fmt.Errorf("serve HTTP: %w", err)
	}
	<-shutdownDone
	logger.Info("http server stopped")
	return nil
}

// runCollectionLoop performs due scheduled collections and recovers unfinished ones.
func runCollectionLoop(ctx context.Context, database *sql.DB, collectionScheduler, recoveryScheduler *scheduler.Service, logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	runners := []struct {
		jobType string
		trigger string
		service *scheduler.Service
	}{
		{jobType: "github_collection", trigger: "schedule", service: collectionScheduler},
		{jobType: "github_recovery", trigger: "recovery", service: recoveryScheduler},
	}
	for {
		for _, runner := range runners {
			due, err := collectionDue(ctx, database, runner.jobType)
			if err != nil {
				logger.Error("check collection schedule", "job_type", runner.jobType, "error", err)
				continue
			}
			if !due {
				continue
			}
			run, err := runner.service.RunOnce(ctx, runner.trigger)
			switch {
			case errors.Is(err, scheduler.ErrLeaseHeld), errors.Is(err, scheduler.ErrJobMissing):
			case errors.Is(err, scheduler.ErrNoCredential):
				logger.Info("collection skipped: no usable GitHub credential", "job_type", runner.jobType)
			case err != nil:
				logger.Error("scheduled collection failed", "job_type", runner.jobType, "run_id", run.ID, "error", err)
			default:
				logger.Info("scheduled collection finished", "job_type", runner.jobType, "run_id", run.ID, "created", run.CreatedCount, "pages", run.RequestCount)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runBackupLoop ensures the daily and weekly backup windows are continuously met.
func runBackupLoop(ctx context.Context, service *backups.Service, logger *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		created, err := service.EnsureScheduled(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("scheduled backup failed", "error", err)
		}
		for _, item := range created {
			logger.Info("scheduled backup created", "name", item.Name, "kind", item.Kind, "size_bytes", item.SizeBytes)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func collectionDue(ctx context.Context, database *sql.DB, jobType string) (bool, error) {
	var due int
	err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM collector_jobs
		WHERE enabled = 1 AND job_type = ?
		AND (next_run_at IS NULL OR next_run_at <= strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))`, jobType).Scan(&due)
	if err != nil {
		return false, fmt.Errorf("query collection schedule: %w", err)
	}
	return due > 0, nil
}

func runHealthcheck(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return checkReady(ctx, &http.Client{Timeout: 3 * time.Second}, "http://127.0.0.1:"+strconv.Itoa(cfg.Port))
}

func checkReady(ctx context.Context, client *http.Client, baseURL string) error {
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/readyz", nil)
	if err != nil {
		return fmt.Errorf("create healthcheck request: %w", err)
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("readiness request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&payload); err != nil || payload.Data.Status != "ready" {
		return errors.New("readiness returned an invalid response")
	}
	return nil
}

func runBackup(ctx context.Context, output io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	store, err := db.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer store.Close()
	service, err := backups.NewService(store.SQL(), backupDirectory(cfg.DatabasePath), time.Now)
	if err != nil {
		return fmt.Errorf("initialize backup service: %w", err)
	}
	item, err := service.Create(ctx, backups.KindManual)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "%s\t%d bytes\tsha256:%s\n", item.Name, item.SizeBytes, item.SHA256)
	return nil
}

func backupDirectory(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), "backups")
}

// runClassifyBackfill 对整个库执行一次自动归类，并输出各分类的仓库数。
func runClassifyBackfill(ctx context.Context, output io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	store, err := db.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer store.Close()

	engine, err := classify.LoadEngine(ctx, store.SQL())
	if err != nil {
		return err
	}
	result, err := classify.Backfill(ctx, store.SQL(), engine, 0)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "scanned %d repositories: categorized %d, unclassified %d, new assignments %d\n",
		result.Scanned, result.Categorized, result.Unclassified, result.Assignments)

	counts, err := classify.CategoryCounts(ctx, store.SQL())
	if err != nil {
		return err
	}
	for _, count := range counts {
		fmt.Fprintf(output, "%s	%s	%d\n", count.Slug, count.Title, count.Repos)
	}
	return nil
}

func runAdminCreate(ctx context.Context, input io.Reader, output io.Writer, readPassword passwordReader) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	store, err := db.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer store.Close()

	reader := bufio.NewReader(input)
	fmt.Fprint(output, "Username: ")
	username, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read username: %w", err)
	}
	fmt.Fprint(output, "Password: ")
	password, err := readPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(output)
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	fmt.Fprint(output, "Confirm password: ")
	confirmation, err := readPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(output)
	if err != nil {
		return fmt.Errorf("read password confirmation: %w", err)
	}
	if string(password) != string(confirmation) {
		return fmt.Errorf("passwords do not match")
	}
	admin, err := auth.NewService(store.SQL()).CreateAdmin(ctx, strings.TrimSpace(username), string(password))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrAdminExists):
			return fmt.Errorf("administrator already exists")
		case errors.Is(err, auth.ErrInvalidPassword):
			return fmt.Errorf("password must be 12 to 1024 characters")
		case errors.Is(err, auth.ErrInvalidUsername):
			return fmt.Errorf("username must be 3 to 64 ASCII letters, digits, dots, underscores or hyphens")
		}
		return err
	}
	fmt.Fprintf(output, "Administrator %s created.\n", admin.Username)
	return nil
}
