package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdminCreatePromptsForPasswordAndPrintsOnlyUsername(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_PATH", t.TempDir()+"/radar.db")
	passwords := [][]byte{[]byte("correct horse battery staple"), []byte("correct horse battery staple")}
	passwordCalls := 0
	readPassword := func(int) ([]byte, error) {
		value := passwords[passwordCalls]
		passwordCalls++
		return value, nil
	}
	var output bytes.Buffer

	err := run(context.Background(), []string{"admin", "create"}, strings.NewReader("Radar.Admin\n"), &output, readPassword)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if passwordCalls != 2 {
		t.Errorf("password reader calls = %d, want 2", passwordCalls)
	}
	got := output.String()
	if !strings.Contains(got, "radar.admin") || strings.Contains(got, "correct horse") || strings.Contains(got, "$argon2id$") {
		t.Fatalf("output = %q", got)
	}
}

func TestAdminCreateRejectsExtraArgumentsWithoutReadingPassword(t *testing.T) {
	called := false
	err := run(context.Background(), []string{"admin", "create", "extra"}, strings.NewReader("radar\n"), io.Discard, func(int) ([]byte, error) {
		called = true
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("run() error = %v, want usage error", err)
	}
	if called {
		t.Fatal("password reader was called for invalid arguments")
	}
}

func TestAdminCreateRejectsPasswordMismatchAndDuplicate(t *testing.T) {
	for _, test := range []struct {
		name      string
		passwords [][]byte
		prepare   bool
		want      string
	}{
		{name: "mismatch", passwords: [][]byte{[]byte("correct horse battery staple"), []byte("different secure password")}, want: "do not match"},
		{name: "duplicate", passwords: [][]byte{[]byte("correct horse battery staple"), []byte("correct horse battery staple")}, prepare: true, want: "already exists"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("APP_ENV", "development")
			t.Setenv("DATABASE_PATH", t.TempDir()+"/radar.db")
			if test.prepare {
				values := [][]byte{[]byte("correct horse battery staple"), []byte("correct horse battery staple")}
				i := 0
				if err := run(context.Background(), []string{"admin", "create"}, strings.NewReader("radar\n"), io.Discard, func(int) ([]byte, error) {
					value := values[i]
					i++
					return value, nil
				}); err != nil {
					t.Fatalf("prepare run() error = %v", err)
				}
			}
			i := 0
			err := run(context.Background(), []string{"admin", "create"}, strings.NewReader("radar\n"), io.Discard, func(int) ([]byte, error) {
				value := test.passwords[i]
				i++
				return value, nil
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestRunDispatchesCommands(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "serve", args: []string{"serve"}, want: "serve"},
		{name: "healthcheck", args: []string{"healthcheck"}, want: "healthcheck"},
		{name: "admin create", args: []string{"admin", "create"}, want: "admin"},
		{name: "backup", args: []string{"backup"}, want: "backup"},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := ""
			deps := commandDeps{
				serve:       func(context.Context, io.Writer) error { called = "serve"; return nil },
				healthcheck: func(context.Context) error { called = "healthcheck"; return nil },
				adminCreate: func(context.Context, io.Reader, io.Writer, passwordReader) error { called = "admin"; return nil },
				backup:      func(context.Context, io.Writer) error { called = "backup"; return nil },
			}
			if err := runWithDeps(context.Background(), test.args, strings.NewReader(""), io.Discard, nil, deps); err != nil {
				t.Fatalf("runWithDeps() error = %v", err)
			}
			if called != test.want {
				t.Fatalf("called = %q, want %q", called, test.want)
			}
		})
	}
}

func TestRunRejectsExtraArgumentsAndUsageListsCommands(t *testing.T) {
	for _, args := range [][]string{{"serve", "extra"}, {"healthcheck", "extra"}, {"unknown"}, nil} {
		err := runWithDeps(context.Background(), args, strings.NewReader(""), io.Discard, nil, commandDeps{})
		if err == nil {
			t.Fatalf("args %v unexpectedly succeeded", args)
		}
		for _, command := range []string{"serve", "healthcheck", "admin create", "backup"} {
			if !strings.Contains(err.Error(), command) {
				t.Errorf("usage %q missing %q", err, command)
			}
		}
	}
}

func TestRunBackupCreatesVerifiedFile(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "radar.db")
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_PATH", databasePath)
	var output bytes.Buffer
	if err := runBackup(context.Background(), &output); err != nil {
		t.Fatalf("runBackup() error = %v", err)
	}
	fields := strings.Fields(output.String())
	if len(fields) != 4 || !strings.HasPrefix(fields[0], "radar-manual-") || !strings.HasPrefix(fields[3], "sha256:") {
		t.Fatalf("output = %q", output.String())
	}
	info, err := os.Stat(filepath.Join(root, "backups", fields[0]))
	if err != nil || info.Size() == 0 {
		t.Fatalf("backup stat = %+v, %v", info, err)
	}
}

func TestHealthcheckSuccessNon200AndTimeout(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		timeout time.Duration
		wantErr bool
	}{
		{name: "success", handler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"status":"ready"}}`))
		}, timeout: time.Second},
		{name: "non-200", handler: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
		}, timeout: time.Second, wantErr: true},
		{name: "invalid response", handler: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not json"))
		}, timeout: time.Second, wantErr: true},
		{name: "timeout", handler: func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}, timeout: 20 * time.Millisecond, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			client := &http.Client{Timeout: test.timeout}
			err := checkReady(context.Background(), client, server.URL)
			if (err != nil) != test.wantErr {
				t.Fatalf("checkReady() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestServeStopsWhenContextIsCancelled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_ENV", "development")
	t.Setenv("APP_PORT", fmt.Sprintf("%d", port))
	databasePath := t.TempDir() + "/radar.db"
	t.Setenv("DATABASE_PATH", databasePath)
	t.Setenv("STATIC_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, io.Discard) }()
	address := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(2 * time.Second)
	for {
		connection, dialErr := net.DialTimeout("tcp", address, 25*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("server did not start: %v", dialErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer database.Close()
	var recoveryInterval int
	if err := database.QueryRow(`SELECT interval_seconds FROM collector_jobs WHERE job_type = 'github_recovery'`).Scan(&recoveryInterval); err != nil {
		cancel()
		t.Fatalf("recovery job was not seeded: %v", err)
	}
	if recoveryInterval != 86400 {
		cancel()
		t.Fatalf("recovery interval = %d, want 86400", recoveryInterval)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServe() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runServe did not stop after context cancellation")
	}
}
