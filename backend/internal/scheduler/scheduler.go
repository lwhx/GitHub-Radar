package scheduler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/github-radar/github-radar/internal/collector"
	"github.com/github-radar/github-radar/internal/credentials"
	"github.com/github-radar/github-radar/internal/github"
)

const (
	collectionJobType  = "github_collection"
	recoveryJobType    = "github_recovery"
	defaultInterval    = 30 * time.Minute
	recoveryInterval   = 24 * time.Hour
	recoveryLookback   = 48 * time.Hour
	initialLookback    = 7 * 24 * time.Hour
	incrementalOverlap = 10 * time.Minute
	leaseDuration      = 10 * time.Minute
	leaseRenewInterval = leaseDuration / 3
	leaseOwner         = "radar"
	timestampLayout    = "2006-01-02T15:04:05Z"
)

var (
	// ErrLeaseHeld reports that another runner owns the collection job.
	ErrLeaseHeld = errors.New("collection job lease is held by another runner")
	// ErrJobMissing reports that the collection job row has not been seeded.
	ErrJobMissing = errors.New("collection job does not exist")
	// ErrNoCredential reports that collection cannot start without a usable token.
	// It is a setup state rather than a failed run, so it records no run row.
	ErrNoCredential = errors.New("no usable GitHub credential is configured")
)

// Searcher is the GitHub search surface the scheduler hands to the collector.
type Searcher = collector.Searcher

// RepositoryGetter is the conditional detail surface handed to the detail synchronizer.
type RepositoryGetter = collector.RepositoryGetter

type searcherSource func(context.Context) (Searcher, error)
type repositoryGetterSource func(context.Context) (RepositoryGetter, error)

type leaseTicker struct {
	ticks <-chan time.Time
	stop  func()
}

func newLeaseTicker(interval time.Duration) leaseTicker {
	ticker := time.NewTicker(interval)
	return leaseTicker{ticks: ticker.C, stop: ticker.Stop}
}

type Service struct {
	db             *sql.DB
	source         searcherSource
	detailSource   repositoryGetterSource
	now            func() time.Time
	owner          string
	jobType        string
	interval       time.Duration
	recovery       bool
	newLeaseTicker func(time.Duration) leaseTicker
}

type Run struct {
	ID                      int64
	JobID                   int64
	Status                  string
	Trigger                 string
	StartedAt               time.Time
	FinishedAt              *time.Time
	RequestCount            int
	CreatedCount            int
	UpdatedCount            int
	SnapshotCount           int
	NotModifiedCount        int
	FailedCount             int
	RateLimitedCount        int
	CoverageIncompleteCount int
	LastErrorCode           string
}

func New(database *sql.DB, source func(context.Context) (Searcher, error), detailSources ...func(context.Context) (RepositoryGetter, error)) *Service {
	return newService(database, source, time.Now, leaseOwner, detailSources...)
}

// NewRecovery builds the independent daily coverage-recovery scheduler.
func NewRecovery(database *sql.DB, source func(context.Context) (Searcher, error)) *Service {
	return newRecoveryService(database, source, time.Now, leaseOwner+"-recovery")
}

func newService(database *sql.DB, source func(context.Context) (Searcher, error), now func() time.Time, owner string, detailSources ...func(context.Context) (RepositoryGetter, error)) *Service {
	service := &Service{
		db: database, source: source, now: now, owner: owner,
		jobType: collectionJobType, interval: defaultInterval, newLeaseTicker: newLeaseTicker,
	}
	if len(detailSources) > 0 {
		service.detailSource = detailSources[0]
	}
	return service
}

func newRecoveryService(database *sql.DB, source func(context.Context) (Searcher, error), now func() time.Time, owner string) *Service {
	return &Service{
		db: database, source: source, now: now, owner: owner,
		jobType: recoveryJobType, interval: recoveryInterval, recovery: true, newLeaseTicker: newLeaseTicker,
	}
}

// EnsureJob seeds the single collection job row; it is idempotent.
func (service *Service) EnsureJob(ctx context.Context) error {
	now := formatTime(service.now())
	_, err := service.db.ExecContext(ctx, `INSERT INTO collector_jobs
		(job_type, enabled, interval_seconds, next_run_at, priority, config_json)
		VALUES (?, 1, ?, ?, 0, '{}')
		ON CONFLICT(job_type) DO NOTHING`, service.jobType, int(service.interval.Seconds()), now)
	if err != nil {
		return fmt.Errorf("seed collection job: %w", err)
	}
	return nil
}

func (service *Service) RunOnce(ctx context.Context, trigger string) (Run, error) {
	jobID, leaseToken, err := service.claimLease(ctx)
	if err != nil {
		return Run{}, err
	}
	released := false
	defer func() {
		if !released {
			_ = service.releaseLease(context.WithoutCancel(ctx), jobID, leaseToken)
		}
	}()

	searcher, sourceErr := service.source(ctx)
	if errors.Is(sourceErr, credentials.ErrNoCredential) {
		return Run{}, service.skipRun(ctx, jobID, leaseToken, &released)
	}

	run, err := service.startRun(ctx, jobID, trigger)
	if err != nil {
		return Run{}, err
	}
	runCtx, stopHeartbeat := service.startLeaseHeartbeat(ctx, jobID, run.ID, leaseToken)
	var searchSummary collector.Summary
	var detailSummary collector.DetailSummary
	collectErr := sourceErr
	if collectErr == nil {
		searchSummary, collectErr = service.collect(runCtx, run, searcher)
	}
	if collectErr == nil && service.detailSource != nil {
		var getter RepositoryGetter
		getter, collectErr = service.detailSource(runCtx)
		if collectErr == nil {
			detailSummary, collectErr = collector.NewDetailSynchronizer(service.db, getter, service.now).SyncDue(runCtx, run.ID)
		}
	}
	if heartbeatErr := stopHeartbeat(); heartbeatErr != nil {
		if errors.Is(heartbeatErr, ErrLeaseHeld) {
			collectErr = ErrLeaseHeld
		} else {
			collectErr = errors.Join(collectErr, heartbeatErr)
		}
	}
	run.RequestCount = searchSummary.Pages + detailSummary.Requests
	run.CreatedCount = searchSummary.Created
	run.UpdatedCount = searchSummary.Updated + detailSummary.Updated
	run.SnapshotCount = searchSummary.Snapshots + detailSummary.Snapshots
	run.NotModifiedCount = detailSummary.NotModified
	run.FailedCount = searchSummary.FailedWindows + detailSummary.Failed
	run.RateLimitedCount = detailSummary.RateLimited
	run.CoverageIncompleteCount = searchSummary.IncompleteWindows
	nextRunAt := service.now().Add(service.interval)
	isDeferred := false
	if retryAt, limited := rateLimitRetryAt(collectErr); limited {
		isDeferred = true
		run.Status = "deferred"
		run.FailedCount = 0
		if run.RateLimitedCount == 0 {
			run.RateLimitedCount = 1
		}
		run.LastErrorCode = "rate_limited"
		if retryAt.After(service.now()) {
			nextRunAt = retryAt
		} else {
			nextRunAt = service.now().Add(time.Minute)
		}
	} else if collectErr != nil {
		run.Status = "failed"
		if run.FailedCount == 0 {
			run.FailedCount = 1
		}
		run.LastErrorCode = errorCode(collectErr)
	} else {
		run.Status = "completed"
	}
	if err := service.finishRun(ctx, &run, collectErr); err != nil {
		return run, errors.Join(collectErr, err)
	}
	if err := service.completeJob(ctx, jobID, leaseToken, nextRunAt); err != nil {
		return run, errors.Join(collectErr, err)
	}
	released = true
	if isDeferred {
		return run, nil
	}
	return run, collectErr
}

// Precheck reports whether a run could start right now without claiming the job.
func (service *Service) Precheck(ctx context.Context) error {
	_, err := service.source(ctx)
	if errors.Is(err, credentials.ErrNoCredential) {
		return ErrNoCredential
	}
	return err
}

// skipRun hands the job back without recording a run: a deployment that has not
// added a token yet should not accumulate failed runs every interval.
func (service *Service) skipRun(ctx context.Context, jobID int64, leaseToken string, released *bool) error {
	if err := service.completeJob(ctx, jobID, leaseToken, service.now().Add(service.interval)); err != nil {
		return errors.Join(ErrNoCredential, err)
	}
	*released = true
	return ErrNoCredential
}

func (service *Service) collect(ctx context.Context, run Run, searcher collector.Searcher) (collector.Summary, error) {
	from, to, err := service.windowBounds(ctx)
	if err != nil {
		return collector.Summary{}, err
	}
	worker := collector.New(service.db, searcher)
	if service.recovery {
		worker = collector.NewRecovery(service.db, searcher)
	}
	if _, err := worker.EnsureWindow(ctx, from, to, run.ID); err != nil {
		return collector.Summary{}, err
	}
	return worker.ProcessPending(ctx, run.ID)
}

func (service *Service) windowBounds(ctx context.Context) (time.Time, time.Time, error) {
	to := service.now().UTC().Truncate(time.Second)
	if service.recovery {
		return to.Add(-recoveryLookback), to, nil
	}
	var previous sql.NullString
	err := service.db.QueryRowContext(ctx, `SELECT MAX(time_to) FROM collection_windows
		WHERE source = 'github_search' AND coverage_status IN ('completed', 'split', 'coverage_incomplete')`).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, time.Time{}, fmt.Errorf("read last collection window: %w", err)
	}
	if !previous.Valid {
		return to.Add(-initialLookback), to, nil
	}
	last, err := time.Parse(timestampLayout, previous.String)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse last collection window: %w", err)
	}
	from := last.Add(-incrementalOverlap).Truncate(time.Second)
	if !from.Before(to) {
		from = to.Add(-incrementalOverlap)
	}
	return from, to, nil
}

func (service *Service) claimLease(ctx context.Context) (int64, string, error) {
	leaseToken, err := makeLeaseToken(service.owner)
	if err != nil {
		return 0, "", err
	}
	now := service.now().UTC()
	var jobID int64
	err = service.db.QueryRowContext(ctx, `UPDATE collector_jobs
		SET lease_owner = ?, lease_expires_at = ?
		WHERE job_type = ? AND enabled = 1 AND (lease_expires_at IS NULL OR lease_expires_at <= ?)
		RETURNING id`, leaseToken, formatTime(now.Add(leaseDuration)), service.jobType, formatTime(now)).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		var exists int
		if countErr := service.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collector_jobs WHERE job_type = ?`, service.jobType).Scan(&exists); countErr != nil {
			return 0, "", fmt.Errorf("check collection job: %w", countErr)
		}
		if exists == 0 {
			return 0, "", ErrJobMissing
		}
		return 0, "", ErrLeaseHeld
	}
	if err != nil {
		return 0, "", fmt.Errorf("claim collection job lease: %w", err)
	}
	return jobID, leaseToken, nil
}

func makeLeaseToken(owner string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate collection lease token: %w", err)
	}
	return owner + ":" + hex.EncodeToString(random[:]), nil
}

func (service *Service) releaseLease(ctx context.Context, jobID int64, leaseToken string) error {
	_, err := service.db.ExecContext(ctx, `UPDATE collector_jobs
		SET lease_owner = NULL, lease_expires_at = NULL WHERE id = ? AND lease_owner = ?`, jobID, leaseToken)
	if err != nil {
		return fmt.Errorf("release collection job lease: %w", err)
	}
	return nil
}

func (service *Service) startLeaseHeartbeat(ctx context.Context, jobID, runID int64, leaseToken string) (context.Context, func() error) {
	runCtx, cancel := context.WithCancelCause(ctx)
	ticker := service.newLeaseTicker(leaseRenewInterval)
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		defer ticker.stop()
		for {
			select {
			case <-runCtx.Done():
				done <- nil
				return
			case <-stop:
				done <- nil
				return
			case <-ticker.ticks:
				if err := service.renewLease(runCtx, jobID, runID, leaseToken); err != nil {
					cancel(err)
					done <- err
					return
				}
			}
		}
	}()
	return runCtx, func() error {
		close(stop)
		err := <-done
		cancel(context.Canceled)
		return err
	}
}

func (service *Service) renewLease(ctx context.Context, jobID, runID int64, leaseToken string) error {
	now := service.now().UTC()
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin collection lease renewal: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE collector_jobs
		SET lease_expires_at = ? WHERE id = ? AND lease_owner = ?`,
		formatTime(now.Add(leaseDuration)), jobID, leaseToken)
	if err != nil {
		return fmt.Errorf("renew collection job lease: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check collection job lease renewal: %w", err)
	}
	if updated != 1 {
		return ErrLeaseHeld
	}
	result, err = tx.ExecContext(ctx, `UPDATE job_runs SET heartbeat_at = ? WHERE id = ? AND status = 'running'`, formatTime(now), runID)
	if err != nil {
		return fmt.Errorf("update collection run heartbeat: %w", err)
	}
	updated, err = result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check collection run heartbeat: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("collection run %d is no longer running", runID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit collection lease renewal: %w", err)
	}
	return nil
}

func (service *Service) startRun(ctx context.Context, jobID int64, trigger string) (Run, error) {
	now := formatTime(service.now())
	result, err := service.db.ExecContext(ctx, `INSERT INTO job_runs(job_id, started_at, status, trigger, heartbeat_at)
		VALUES (?, ?, 'running', ?, ?)`, jobID, now, normalizeTrigger(trigger), now)
	if err != nil {
		return Run{}, fmt.Errorf("start collection run: %w", err)
	}
	runID, err := result.LastInsertId()
	if err != nil {
		return Run{}, fmt.Errorf("read collection run id: %w", err)
	}
	return Run{ID: runID, JobID: jobID, Status: "running", Trigger: normalizeTrigger(trigger), StartedAt: service.now().UTC()}, nil
}

func (service *Service) finishRun(ctx context.Context, run *Run, collectErr error) error {
	now := formatTime(service.now())
	status := run.Status
	if status == "" || status == "running" {
		status = "completed"
	}
	var summary any
	if collectErr != nil {
		summary = truncate(collectErr.Error(), 500)
	}
	finishedAt := service.now().UTC()
	_, err := service.db.ExecContext(ctx, `UPDATE job_runs
		SET finished_at = ?, status = ?, request_count = ?, created_count = ?, updated_count = ?, snapshot_count = ?,
			not_modified_count = ?, retry_count = ?, rate_limited_count = ?, failed_count = ?,
			coverage_incomplete_count = ?, last_error_code = ?, error_summary = ?
		WHERE id = ?`,
		now, status, run.RequestCount, run.CreatedCount, run.UpdatedCount, run.SnapshotCount,
		run.NotModifiedCount, 0, run.RateLimitedCount, run.FailedCount,
		run.CoverageIncompleteCount, nullText(run.LastErrorCode), summary, run.ID)
	if err != nil {
		return fmt.Errorf("finish collection run %d: %w", run.ID, err)
	}
	run.Status = status
	run.FinishedAt = &finishedAt
	if collectErr != nil {
		level := "error"
		if status == "deferred" {
			level = "warning"
		}
		if _, err := service.db.ExecContext(ctx, `INSERT INTO job_run_events
			(run_id, occurred_at, level, event_code, message, details_json)
			VALUES (?, ?, ?, ?, ?, '{}')`,
			run.ID, now, level, run.LastErrorCode, truncate(collectErr.Error(), 2000)); err != nil {
			return fmt.Errorf("record collection run event: %w", err)
		}
	}
	return nil
}

func (service *Service) completeJob(ctx context.Context, jobID int64, leaseToken string, nextRunAt time.Time) error {
	result, err := service.db.ExecContext(ctx, `UPDATE collector_jobs
		SET next_run_at = ?, lease_owner = NULL, lease_expires_at = NULL
		WHERE id = ? AND lease_owner = ?`, formatTime(nextRunAt), jobID, leaseToken)
	if err != nil {
		return fmt.Errorf("schedule next collection run and release lease: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check collection job completion: %w", err)
	}
	if updated != 1 {
		return ErrLeaseHeld
	}
	return nil
}

func rateLimitRetryAt(err error) (time.Time, bool) {
	var apiError *github.APIError
	if !errors.As(err, &apiError) {
		return time.Time{}, false
	}
	if apiError.Kind != github.ErrorPrimaryRateLimit && apiError.Kind != github.ErrorSecondaryRateLimit {
		return time.Time{}, false
	}
	return apiError.RetryAt, true
}

func normalizeTrigger(trigger string) string {
	switch trigger {
	case "manual", "recovery", "schedule":
		return trigger
	default:
		return "schedule"
	}
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrLeaseHeld) {
		return "lease_lost"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	return "collection_failed"
}

func nullText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func formatTime(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(timestampLayout)
}
