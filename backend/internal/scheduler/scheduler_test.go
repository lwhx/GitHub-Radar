package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/credentials"
	appdb "github.com/github-radar/github-radar/internal/db"
	"github.com/github-radar/github-radar/internal/github"
)

type fixedSearcher struct {
	result github.SearchResult
	err    error
}

func (search fixedSearcher) Search(context.Context, github.SearchOptions) (github.SearchResult, github.ResponseMeta, error) {
	return search.result, github.ResponseMeta{}, search.err
}

func (search fixedSearcher) SearchNext(context.Context, string) (github.SearchResult, github.ResponseMeta, error) {
	return search.result, github.ResponseMeta{}, search.err
}

type fixedRepositoryGetter struct {
	repository github.Repository
	meta       github.ResponseMeta
	err        error
	calls      int
}

func (getter *fixedRepositoryGetter) GetRepository(context.Context, string, string, string) (github.Repository, github.ResponseMeta, error) {
	getter.calls++
	return getter.repository, getter.meta, getter.err
}

type controlledClock struct {
	unix atomic.Int64
}

func newControlledClock(now time.Time) *controlledClock {
	clock := &controlledClock{}
	clock.Set(now)
	return clock
}

func (clock *controlledClock) Now() time.Time {
	return time.Unix(clock.unix.Load(), 0).UTC()
}

func (clock *controlledClock) Set(now time.Time) {
	clock.unix.Store(now.Unix())
}

type gatedSearcher struct {
	entered chan struct{}
	release chan struct{}
}

func (search *gatedSearcher) Search(ctx context.Context, _ github.SearchOptions) (github.SearchResult, github.ResponseMeta, error) {
	select {
	case search.entered <- struct{}{}:
	default:
	}
	select {
	case <-search.release:
		return github.SearchResult{}, github.ResponseMeta{}, nil
	case <-ctx.Done():
		return github.SearchResult{}, github.ResponseMeta{}, ctx.Err()
	}
}

func (search *gatedSearcher) SearchNext(ctx context.Context, _ string) (github.SearchResult, github.ResponseMeta, error) {
	return search.Search(ctx, github.SearchOptions{})
}

type failingGatedSearcher struct {
	entered chan struct{}
	release chan struct{}
	err     error
}

func (search *failingGatedSearcher) Search(ctx context.Context, _ github.SearchOptions) (github.SearchResult, github.ResponseMeta, error) {
	select {
	case search.entered <- struct{}{}:
	default:
	}
	select {
	case <-search.release:
		return github.SearchResult{}, github.ResponseMeta{}, search.err
	case <-ctx.Done():
		return github.SearchResult{}, github.ResponseMeta{}, ctx.Err()
	}
}

func (search *failingGatedSearcher) SearchNext(ctx context.Context, _ string) (github.SearchResult, github.ResponseMeta, error) {
	return search.Search(ctx, github.SearchOptions{})
}

func newTestService(t *testing.T, search fixedSearcher) (*Service, *appdb.DB) {
	t.Helper()
	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	service := newService(store.SQL(), func(context.Context) (Searcher, error) { return search, nil }, func() time.Time { return now }, "test-owner")
	return service, store
}

func TestRunOnceClaimsLeaseRecoversAndCompletes(t *testing.T) {
	t.Parallel()

	search := fixedSearcher{result: github.SearchResult{TotalCount: 1, Items: []github.Repository{{
		ID: 5, FullName: "owner/name", Name: "name", Owner: github.Owner{Login: "owner"},
		HTMLURL: "https://github.com/owner/name", StargazersCount: 3,
		CreatedAt: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC), Visibility: "public",
	}}}}
	service, store := newTestService(t, search)
	ctx := context.Background()

	// The first ever run looks back seven days.
	if err := service.EnsureJob(ctx); err != nil {
		t.Fatalf("EnsureJob() error = %v", err)
	}
	run, err := service.RunOnce(ctx, "manual")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if run.ID == 0 || run.Status != "completed" || run.CreatedCount != 1 || run.Trigger != "manual" {
		t.Fatalf("run = %+v", run)
	}
	var windows int
	if err := store.SQL().QueryRow("SELECT COUNT(*) FROM collection_windows WHERE job_run_id = ?", run.ID).Scan(&windows); err != nil {
		t.Fatal(err)
	}
	if windows == 0 {
		t.Error("run produced no collection windows")
	}
	var leaseOwner *string
	if err := store.SQL().QueryRow("SELECT lease_owner FROM collector_jobs").Scan(&leaseOwner); err != nil {
		t.Fatal(err)
	}
	if leaseOwner != nil {
		t.Errorf("lease not released: %v", *leaseOwner)
	}
	var nextRunAt *string
	if err := store.SQL().QueryRow("SELECT next_run_at FROM collector_jobs").Scan(&nextRunAt); err != nil {
		t.Fatal(err)
	}
	if nextRunAt == nil || *nextRunAt != "2026-10-23T12:30:00Z" {
		t.Errorf("next_run_at = %v", nextRunAt)
	}
}

func TestEnsureJobMarksStaleRunningRunsFailed(t *testing.T) {
	t.Parallel()

	service, store := newTestService(t, fixedSearcher{})
	ctx := context.Background()
	if err := service.EnsureJob(ctx); err != nil {
		t.Fatalf("initial EnsureJob() error = %v", err)
	}
	var jobID int64
	if err := store.SQL().QueryRow(`SELECT id FROM collector_jobs WHERE job_type = 'github_collection'`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	for _, run := range []struct {
		startedAt   string
		heartbeatAt string
	}{
		{startedAt: "2026-10-23T10:00:00Z", heartbeatAt: "2026-10-23T10:05:00Z"},
		{startedAt: "2026-10-23T11:55:00Z", heartbeatAt: "2026-10-23T11:59:00Z"},
	} {
		if _, err := store.SQL().Exec(`INSERT INTO job_runs
			(job_id, started_at, status, trigger, heartbeat_at) VALUES (?, ?, 'running', 'schedule', ?)`,
			jobID, run.startedAt, run.heartbeatAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.EnsureJob(ctx); err != nil {
		t.Fatalf("recovery EnsureJob() error = %v", err)
	}
	rows, err := store.SQL().Query(`SELECT status, finished_at, last_error_code, error_summary FROM job_runs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []struct {
		status, finishedAt, errorCode, summary sql.NullString
	}
	for rows.Next() {
		var item struct {
			status, finishedAt, errorCode, summary sql.NullString
		}
		if err := rows.Scan(&item.status, &item.finishedAt, &item.errorCode, &item.summary); err != nil {
			t.Fatal(err)
		}
		got = append(got, item)
	}
	if len(got) != 2 {
		t.Fatalf("run count = %d, want 2", len(got))
	}
	if got[0].status.String != "failed" || got[0].finishedAt.String != "2026-10-23T12:00:00Z" ||
		got[0].errorCode.String != "process_interrupted" || !strings.Contains(got[0].summary.String, "interrupted") {
		t.Fatalf("stale run = %+v", got[0])
	}
	if got[1].status.String != "running" || got[1].finishedAt.Valid || got[1].errorCode.Valid || got[1].summary.Valid {
		t.Fatalf("fresh run changed = %+v", got[1])
	}
}

func TestRunOnceRenewsLeaseBeforeOriginalExpiry(t *testing.T) {
	t.Parallel()

	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	clock := newControlledClock(base)
	search := &gatedSearcher{entered: make(chan struct{}, 1), release: make(chan struct{})}
	service := newService(store.SQL(), func(context.Context) (Searcher, error) { return search, nil }, clock.Now, "first-owner")
	ticks := make(chan time.Time, 1)
	service.newLeaseTicker = func(time.Duration) leaseTicker {
		return leaseTicker{ticks: ticks, stop: func() {}}
	}
	if err := service.EnsureJob(context.Background()); err != nil {
		t.Fatal(err)
	}

	type runResult struct {
		run Run
		err error
	}
	finished := make(chan runResult, 1)
	go func() {
		run, runErr := service.RunOnce(context.Background(), "manual")
		finished <- runResult{run: run, err: runErr}
	}()
	select {
	case <-search.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("collection did not start")
	}

	clock.Set(base.Add(8 * time.Minute))
	ticks <- clock.Now()
	wantExpiry := formatTime(base.Add(18 * time.Minute))
	deadline := time.Now().Add(2 * time.Second)
	for {
		var expiry, heartbeat string
		err := store.SQL().QueryRow(`SELECT collector_jobs.lease_expires_at, job_runs.heartbeat_at
			FROM collector_jobs JOIN job_runs ON job_runs.job_id = collector_jobs.id`).Scan(&expiry, &heartbeat)
		if err == nil && expiry == wantExpiry && heartbeat == formatTime(clock.Now()) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lease was not renewed: expiry %q heartbeat %q error %v", expiry, heartbeat, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	clock.Set(base.Add(11 * time.Minute))
	contender := newService(store.SQL(), func(context.Context) (Searcher, error) { return fixedSearcher{}, nil }, clock.Now, "second-owner")
	if _, err := contender.RunOnce(context.Background(), "schedule"); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("contending RunOnce() error = %v, want ErrLeaseHeld", err)
	}
	close(search.release)
	select {
	case result := <-finished:
		if result.err != nil || result.run.Status != "completed" {
			t.Fatalf("original RunOnce() = %+v, %v", result.run, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("original collection did not finish")
	}
	var runs int
	if err := store.SQL().QueryRow(`SELECT COUNT(*) FROM job_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("job run count = %d, want 1", runs)
	}
}

func TestRunOnceStopsSafelyWhenLeaseIsLost(t *testing.T) {
	t.Parallel()

	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	clock := newControlledClock(base)
	search := &gatedSearcher{entered: make(chan struct{}, 1), release: make(chan struct{})}
	service := newService(store.SQL(), func(context.Context) (Searcher, error) { return search, nil }, clock.Now, "first-owner")
	ticks := make(chan time.Time, 1)
	service.newLeaseTicker = func(time.Duration) leaseTicker {
		return leaseTicker{ticks: ticks, stop: func() {}}
	}
	if err := service.EnsureJob(context.Background()); err != nil {
		t.Fatal(err)
	}

	type runResult struct {
		run Run
		err error
	}
	finished := make(chan runResult, 1)
	go func() {
		run, runErr := service.RunOnce(context.Background(), "manual")
		finished <- runResult{run: run, err: runErr}
	}()
	select {
	case <-search.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("collection did not start")
	}
	if _, err := store.SQL().Exec(`UPDATE collector_jobs
		SET lease_owner = 'second-owner', lease_expires_at = '2026-10-23T12:20:00Z'`); err != nil {
		t.Fatal(err)
	}
	clock.Set(base.Add(4 * time.Minute))
	ticks <- clock.Now()

	select {
	case result := <-finished:
		if !errors.Is(result.err, ErrLeaseHeld) {
			t.Fatalf("RunOnce() error = %v, want ErrLeaseHeld", result.err)
		}
		if result.run.Status != "failed" || result.run.LastErrorCode != "lease_lost" || result.run.FinishedAt == nil {
			t.Fatalf("lost-lease run = %+v", result.run)
		}
		var owner, status, errorCode string
		if err := store.SQL().QueryRow(`SELECT collector_jobs.lease_owner, job_runs.status, job_runs.last_error_code
			FROM collector_jobs JOIN job_runs ON job_runs.job_id = collector_jobs.id
			WHERE job_runs.id = ?`, result.run.ID).Scan(&owner, &status, &errorCode); err != nil {
			t.Fatal(err)
		}
		if owner != "second-owner" || status != "failed" || errorCode != "lease_lost" {
			t.Fatalf("persisted lost-lease state = owner %q status %q code %q", owner, status, errorCode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lost-lease collection did not stop")
	}
}

func TestExpiredLeaseCannotBeRenewedByPreviousRunnerWithSameOwner(t *testing.T) {
	t.Parallel()

	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	clock := newControlledClock(base)
	firstSearch := &gatedSearcher{entered: make(chan struct{}, 1), release: make(chan struct{})}
	first := newService(store.SQL(), func(context.Context) (Searcher, error) { return firstSearch, nil }, clock.Now, "shared-owner")
	firstTicks := make(chan time.Time, 1)
	first.newLeaseTicker = func(time.Duration) leaseTicker {
		return leaseTicker{ticks: firstTicks, stop: func() {}}
	}
	if err := first.EnsureJob(context.Background()); err != nil {
		t.Fatal(err)
	}

	type runResult struct {
		run Run
		err error
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstFinished := make(chan runResult, 1)
	go func() {
		run, runErr := first.RunOnce(firstCtx, "manual")
		firstFinished <- runResult{run: run, err: runErr}
	}()
	select {
	case <-firstSearch.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first collection did not start")
	}

	clock.Set(base.Add(11 * time.Minute))
	secondSearch := &gatedSearcher{entered: make(chan struct{}, 1), release: make(chan struct{})}
	second := newService(store.SQL(), func(context.Context) (Searcher, error) { return secondSearch, nil }, clock.Now, "shared-owner")
	second.newLeaseTicker = func(time.Duration) leaseTicker {
		return leaseTicker{ticks: make(chan time.Time), stop: func() {}}
	}
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	secondFinished := make(chan runResult, 1)
	go func() {
		run, runErr := second.RunOnce(secondCtx, "schedule")
		secondFinished <- runResult{run: run, err: runErr}
	}()
	select {
	case <-secondSearch.entered:
	case result := <-secondFinished:
		t.Fatalf("second collection ended before acquiring reclaimed lease: %+v, %v", result.run, result.err)
	case <-time.After(2 * time.Second):
		t.Fatal("second collection did not reclaim expired lease")
	}

	firstTicks <- clock.Now()
	select {
	case result := <-firstFinished:
		if !errors.Is(result.err, ErrLeaseHeld) || result.run.LastErrorCode != "lease_lost" {
			t.Fatalf("first RunOnce() after reclaim = %+v, %v", result.run, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("previous runner renewed a reclaimed lease with the same owner")
	}
	close(secondSearch.release)
	select {
	case result := <-secondFinished:
		if result.err != nil || result.run.Status != "completed" {
			t.Fatalf("second RunOnce() = %+v, %v", result.run, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second collection did not finish")
	}
}

func TestLeaseLossOutranksRateLimitErrorInRunStatus(t *testing.T) {
	t.Parallel()

	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	clock := newControlledClock(base)
	search := &failingGatedSearcher{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		err: &github.APIError{
			Kind: github.ErrorPrimaryRateLimit, StatusCode: 403,
			RetryAt: base.Add(30 * time.Minute), Message: "rate limited",
		},
	}
	service := newService(store.SQL(), func(context.Context) (Searcher, error) { return search, nil }, clock.Now, "first-owner")
	ticks := make(chan time.Time, 1)
	service.newLeaseTicker = func(time.Duration) leaseTicker {
		return leaseTicker{ticks: ticks, stop: func() {}}
	}
	if err := service.EnsureJob(context.Background()); err != nil {
		t.Fatal(err)
	}

	type runResult struct {
		run Run
		err error
	}
	finished := make(chan runResult, 1)
	go func() {
		run, runErr := service.RunOnce(context.Background(), "schedule")
		finished <- runResult{run: run, err: runErr}
	}()
	select {
	case <-search.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("collection did not start")
	}
	if _, err := store.SQL().Exec(`UPDATE collector_jobs
		SET lease_owner = 'second-owner', lease_expires_at = '2026-10-23T12:20:00Z'`); err != nil {
		t.Fatal(err)
	}
	clock.Set(base.Add(4 * time.Minute))
	ticks <- clock.Now()
	close(search.release)

	select {
	case result := <-finished:
		if !errors.Is(result.err, ErrLeaseHeld) {
			t.Fatalf("RunOnce() error = %v, want ErrLeaseHeld", result.err)
		}
		if result.run.Status != "failed" || result.run.LastErrorCode != "lease_lost" {
			t.Fatalf("run = %+v, want failed/lease_lost even with a concurrent rate limit", result.run)
		}
		var nextRun string
		if err := store.SQL().QueryRow(`SELECT next_run_at FROM collector_jobs`).Scan(&nextRun); err != nil {
			t.Fatal(err)
		}
		if nextRun != formatTime(base) {
			t.Fatalf("next_run_at = %q, want the reclaimed runner's schedule untouched", nextRun)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("collection did not stop")
	}
}

func TestRunOnceReturnsLeaseHeldWithoutStartingAnotherRun(t *testing.T) {
	t.Parallel()

	service, store := newTestService(t, fixedSearcher{})
	ctx := context.Background()
	if err := service.EnsureJob(ctx); err != nil {
		t.Fatalf("EnsureJob() error = %v", err)
	}
	if _, err := store.SQL().Exec(`UPDATE collector_jobs SET lease_owner = 'other', lease_expires_at = '2026-10-23T13:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	_, err := service.RunOnce(ctx, "manual")
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("RunOnce() error = %v, want ErrLeaseHeld", err)
	}
	var runs int
	if err := store.SQL().QueryRow("SELECT COUNT(*) FROM job_runs").Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Errorf("job runs = %d", runs)
	}
}

func TestRunOnceRecordsFailureAndKeepsWindowForRetry(t *testing.T) {
	t.Parallel()

	service, store := newTestService(t, fixedSearcher{err: errors.New("GitHub returned HTTP 503")})
	ctx := context.Background()
	if err := service.EnsureJob(ctx); err != nil {
		t.Fatalf("EnsureJob() error = %v", err)
	}
	run, err := service.RunOnce(ctx, "schedule")
	if err == nil {
		t.Fatal("RunOnce() error = nil, want collection failure")
	}
	if run.Status != "failed" || run.LastErrorCode == "" {
		t.Fatalf("run = %+v", run)
	}
	var pending int
	if err := store.SQL().QueryRow("SELECT COUNT(*) FROM collection_windows WHERE coverage_status = 'pending' AND attempt_count > 0").Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending == 0 {
		t.Error("failed window was not kept pending for retry")
	}
	var events int
	if err := store.SQL().QueryRow("SELECT COUNT(*) FROM job_run_events WHERE run_id = ?", run.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events == 0 {
		t.Error("failure produced no job run events")
	}
}

func TestRunOnceRejectsConcurrentRunFromSameOwner(t *testing.T) {
	t.Parallel()

	service, store := newTestService(t, fixedSearcher{})
	ctx := context.Background()
	if err := service.EnsureJob(ctx); err != nil {
		t.Fatalf("EnsureJob() error = %v", err)
	}
	if _, err := store.SQL().Exec(`UPDATE collector_jobs SET lease_owner = 'test-owner', lease_expires_at = '2026-10-23T12:05:00Z'`); err != nil {
		t.Fatal(err)
	}
	_, err := service.RunOnce(ctx, "manual")
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("RunOnce() error = %v, want ErrLeaseHeld", err)
	}
}

func TestRunOnceSkipsWhenNoCredentialIsConfigured(t *testing.T) {
	t.Parallel()

	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	service := newService(store.SQL(), func(context.Context) (Searcher, error) {
		return nil, credentials.ErrNoCredential
	}, func() time.Time { return now }, "test-owner")
	ctx := context.Background()
	if err := service.EnsureJob(ctx); err != nil {
		t.Fatalf("EnsureJob() error = %v", err)
	}

	if err := service.Precheck(ctx); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("Precheck() error = %v, want ErrNoCredential", err)
	}

	_, err = service.RunOnce(ctx, "schedule")
	if !errors.Is(err, ErrNoCredential) {
		t.Fatalf("RunOnce() error = %v, want ErrNoCredential", err)
	}
	var runs int
	if err := store.SQL().QueryRow("SELECT COUNT(*) FROM job_runs").Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Errorf("job runs = %d, want 0: missing credentials must not create a failed run", runs)
	}
	var leaseOwner *string
	if err := store.SQL().QueryRow("SELECT lease_owner FROM collector_jobs").Scan(&leaseOwner); err != nil {
		t.Fatal(err)
	}
	if leaseOwner != nil {
		t.Errorf("lease not released: %v", *leaseOwner)
	}
	var nextRunAt *string
	if err := store.SQL().QueryRow("SELECT next_run_at FROM collector_jobs").Scan(&nextRunAt); err != nil {
		t.Fatal(err)
	}
	if nextRunAt == nil || *nextRunAt != "2026-10-23T12:30:00Z" {
		t.Errorf("next_run_at = %v, want a retry after the normal interval", nextRunAt)
	}
}

func TestPrecheckPassesWhenACredentialIsUsable(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t, fixedSearcher{})
	if err := service.Precheck(context.Background()); err != nil {
		t.Fatalf("Precheck() error = %v, want nil", err)
	}
}

func TestRecoveryRunUsesIndependentDailyJobAndFixed48HourWindow(t *testing.T) {
	t.Parallel()

	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	source := func(context.Context) (Searcher, error) { return fixedSearcher{}, nil }
	collectionService := newService(store.SQL(), source, func() time.Time { return now }, "collection-owner")
	recoveryService := newRecoveryService(store.SQL(), source, func() time.Time { return now }, "recovery-owner")
	if err := collectionService.EnsureJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := recoveryService.EnsureJob(context.Background()); err != nil {
		t.Fatal(err)
	}

	run, err := recoveryService.RunOnce(context.Background(), "recovery")
	if err != nil {
		t.Fatalf("recovery RunOnce() error = %v", err)
	}
	if run.Status != "completed" || run.Trigger != "recovery" {
		t.Fatalf("recovery run = %+v", run)
	}
	rows, err := store.SQL().Query(`SELECT job_type, interval_seconds FROM collector_jobs ORDER BY job_type`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	intervals := map[string]int{}
	for rows.Next() {
		var jobType string
		var interval int
		if err := rows.Scan(&jobType, &interval); err != nil {
			t.Fatal(err)
		}
		intervals[jobType] = interval
	}
	if intervals["github_collection"] != 1800 || intervals["github_recovery"] != 86400 {
		t.Fatalf("job intervals = %+v", intervals)
	}
	var sourceName, queryHash, from, to string
	if err := store.SQL().QueryRow(`SELECT source, query_hash, time_from, time_to
		FROM collection_windows WHERE job_run_id = ?`, run.ID).Scan(&sourceName, &queryHash, &from, &to); err != nil {
		t.Fatal(err)
	}
	if sourceName != "github_recovery" || queryHash != "created-public-recovery-v1" ||
		from != "2026-10-21T12:00:00Z" || to != "2026-10-23T12:00:00Z" {
		t.Fatalf("recovery window = source %q hash %q %s..%s", sourceName, queryHash, from, to)
	}
}

func TestRunOnceDefersRateLimitedDetailSyncWithoutFailingRepository(t *testing.T) {
	t.Parallel()

	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	retryAt := now.Add(20 * time.Minute)
	if _, err := store.SQL().Exec(`INSERT INTO repositories
		(github_id, owner, name, full_name, html_url, topics_json, stars_count, github_created_at,
		 github_updated_at, first_seen_at, sync_status, visibility, next_sync_at)
		VALUES (88, 'limited', 'repo', 'limited/repo', 'https://github.com/limited/repo', '[]', 4,
		 '2026-10-20T00:00:00Z', '2026-10-20T01:00:00Z', '2026-10-20T01:00:00Z',
		 'pending', 'public', '2026-10-23T11:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	getter := &fixedRepositoryGetter{err: &github.APIError{
		Kind: github.ErrorPrimaryRateLimit, StatusCode: 403, RetryAt: retryAt, Message: "rate limited",
	}}
	service := newService(store.SQL(), func(context.Context) (Searcher, error) {
		return fixedSearcher{}, nil
	}, func() time.Time { return now }, "test-owner", func(context.Context) (RepositoryGetter, error) {
		return getter, nil
	})
	if err := service.EnsureJob(context.Background()); err != nil {
		t.Fatal(err)
	}

	run, err := service.RunOnce(context.Background(), "schedule")
	if err != nil {
		t.Fatalf("RunOnce() error = %v, want deferred run without operational error", err)
	}
	if run.Status != "deferred" || run.RateLimitedCount != 1 || run.FailedCount != 0 || run.LastErrorCode != "rate_limited" {
		t.Fatalf("run = %+v", run)
	}
	var nextRun string
	var leaseOwner *string
	if err := store.SQL().QueryRow(`SELECT next_run_at, lease_owner FROM collector_jobs`).Scan(&nextRun, &leaseOwner); err != nil {
		t.Fatal(err)
	}
	if nextRun != formatTime(retryAt) || leaseOwner != nil {
		t.Fatalf("job after deferral = next %q lease %v", nextRun, leaseOwner)
	}
	var status string
	var failures int
	if err := store.SQL().QueryRow(`SELECT sync_status, consecutive_failures FROM repositories WHERE github_id = 88`).Scan(&status, &failures); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || failures != 0 {
		t.Fatalf("rate-limited repository = status %q failures %d", status, failures)
	}
}

func TestRunOnceSynchronizesDueRepositoryDetails(t *testing.T) {
	t.Parallel()

	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	if _, err := store.SQL().Exec(`INSERT INTO repositories
		(github_id, owner, name, full_name, html_url, topics_json, stars_count, github_created_at,
		 github_updated_at, first_seen_at, sync_status, visibility, next_sync_at)
		VALUES (77, 'due', 'repo', 'due/repo', 'https://github.com/due/repo', '[]', 4,
		 '2026-10-20T00:00:00Z', '2026-10-20T01:00:00Z', '2026-10-20T01:00:00Z',
		 'pending', 'public', '2026-10-23T11:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	getter := &fixedRepositoryGetter{
		repository: github.Repository{
			ID: 77, Owner: github.Owner{Login: "due"}, Name: "repo", FullName: "due/repo",
			HTMLURL: "https://github.com/due/repo", StargazersCount: 8,
			CreatedAt: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 10, 23, 11, 0, 0, 0, time.UTC), Visibility: "public",
		},
		meta: github.ResponseMeta{StatusCode: 200, ETag: `"detail-v1"`},
	}
	service := newService(store.SQL(), func(context.Context) (Searcher, error) {
		return fixedSearcher{}, nil
	}, func() time.Time { return now }, "test-owner", func(context.Context) (RepositoryGetter, error) {
		return getter, nil
	})
	if err := service.EnsureJob(context.Background()); err != nil {
		t.Fatal(err)
	}

	run, err := service.RunOnce(context.Background(), "manual")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if getter.calls != 1 || run.RequestCount != 2 || run.UpdatedCount != 1 || run.SnapshotCount != 1 {
		t.Fatalf("detail run = %+v, getter calls = %d", run, getter.calls)
	}
	var stars, snapshots int
	if err := store.SQL().QueryRow(`SELECT stars_count FROM repositories WHERE github_id = 77`).Scan(&stars); err != nil {
		t.Fatal(err)
	}
	if err := store.SQL().QueryRow(`SELECT COUNT(*) FROM star_snapshots WHERE repo_id = (SELECT id FROM repositories WHERE github_id = 77)`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if stars != 8 || snapshots != 1 {
		t.Fatalf("detail persistence = stars %d snapshots %d", stars, snapshots)
	}
	var storedSnapshots, storedNotModified int
	if err := store.SQL().QueryRow(`SELECT snapshot_count, not_modified_count FROM job_runs WHERE id = ?`, run.ID).
		Scan(&storedSnapshots, &storedNotModified); err != nil {
		t.Fatal(err)
	}
	if storedSnapshots != 1 || storedNotModified != 0 {
		t.Fatalf("stored run detail counters = snapshots %d not-modified %d", storedSnapshots, storedNotModified)
	}
	history, err := service.JobRunHistory(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].SnapshotCount != 1 || history[0].NotModifiedCount != 0 {
		t.Fatalf("job run history = %+v", history)
	}
}
