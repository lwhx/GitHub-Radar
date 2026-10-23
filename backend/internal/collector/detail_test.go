package collector

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/github-radar/github-radar/internal/db"
	"github.com/github-radar/github-radar/internal/github"
)

type detailResponse struct {
	repository github.Repository
	meta       github.ResponseMeta
	err        error
}

type fakeRepositoryGetter struct {
	responses map[string]detailResponse
	calls     []detailCall
}

type detailCall struct {
	owner string
	name  string
	etag  string
}

func (fake *fakeRepositoryGetter) GetRepository(_ context.Context, owner, name, etag string) (github.Repository, github.ResponseMeta, error) {
	fake.calls = append(fake.calls, detailCall{owner: owner, name: name, etag: etag})
	response := fake.responses[owner+"/"+name]
	return response.repository, response.meta, response.err
}

func newDetailTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store.SQL()
}

func insertDetailRepository(t *testing.T, database *sql.DB, now time.Time) int64 {
	t.Helper()
	result, err := database.Exec(`INSERT INTO repositories
		(github_id, owner, name, full_name, html_url, topics_json, stars_count, github_created_at,
		 github_updated_at, first_seen_at, last_synced_at, last_successful_fetch_at, sync_status,
		 visibility, etag, next_sync_at)
		VALUES (501, 'old', 'name', 'old/name', 'https://github.com/old/name', '[]', 10,
		 '2026-10-20T00:00:00Z', '2026-10-20T01:00:00Z', '2026-10-20T01:00:00Z',
		 '2026-10-23T11:00:00Z', '2026-10-23T11:00:00Z', 'pending', 'public', 'W/"old"', ?)`,
		formatTime(now.Add(-time.Minute)))
	if err != nil {
		t.Fatalf("insert repository: %v", err)
	}
	repoID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("repository id: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO star_snapshots(repo_id, star_count, captured_at, source, sample_status)
		VALUES (?, 10, '2026-10-23T11:00:00Z', 'search', 'ok')`, repoID); err != nil {
		t.Fatalf("insert initial snapshot: %v", err)
	}
	return repoID
}

func insertDetailRun(t *testing.T, database *sql.DB) int64 {
	t.Helper()
	result, err := database.Exec(`INSERT INTO collector_jobs(job_type, enabled, interval_seconds, next_run_at, priority, config_json)
		VALUES ('detail-test', 1, 1800, '2026-10-23T12:30:00Z', 0, '{}')`)
	if err != nil {
		t.Fatalf("insert detail job: %v", err)
	}
	jobID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	result, err = database.Exec(`INSERT INTO job_runs(job_id, started_at, status, trigger)
		VALUES (?, '2026-10-23T12:00:00Z', 'running', 'schedule')`, jobID)
	if err != nil {
		t.Fatalf("insert detail run: %v", err)
	}
	runID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return runID
}

func TestDetailSynchronizerStoresChangedRepositoryAndSnapshotAtomically(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	database := newDetailTestDatabase(t)
	repoID := insertDetailRepository(t, database, now)
	getter := &fakeRepositoryGetter{responses: map[string]detailResponse{
		"old/name": {
			repository: github.Repository{
				ID: 501, Owner: github.Owner{Login: "new"}, Name: "renamed", FullName: "new/renamed",
				HTMLURL: "https://github.com/new/renamed", StargazersCount: 15, ForksCount: 2,
				OpenIssuesCount: 1, DefaultBranch: "main", Topics: []string{"go", "cli"},
				CreatedAt: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, 10, 23, 11, 30, 0, 0, time.UTC), Visibility: "public",
			},
			meta: github.ResponseMeta{StatusCode: 200, ETag: `W/"new"`},
		},
	}}
	synchronizer := newDetailSynchronizer(database, getter, func() time.Time { return now }, 10)

	summary, err := synchronizer.SyncDue(context.Background(), 0)
	if err != nil {
		t.Fatalf("SyncDue() error = %v", err)
	}
	if summary.Requests != 1 || summary.Updated != 1 || summary.Snapshots != 1 || summary.NotModified != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(getter.calls) != 1 || getter.calls[0] != (detailCall{owner: "old", name: "name", etag: `W/"old"`}) {
		t.Fatalf("detail calls = %+v", getter.calls)
	}

	var fullName, etag, status, lastSynced, lastSuccessful, nextSync string
	var stars, failures int
	if err := database.QueryRow(`SELECT full_name, stars_count, etag, sync_status, last_synced_at,
		last_successful_fetch_at, next_sync_at, consecutive_failures FROM repositories WHERE id = ?`, repoID).
		Scan(&fullName, &stars, &etag, &status, &lastSynced, &lastSuccessful, &nextSync, &failures); err != nil {
		t.Fatal(err)
	}
	if fullName != "new/renamed" || stars != 15 || etag != `W/"new"` || status != "synced" || failures != 0 {
		t.Fatalf("stored repository = full_name %q stars %d etag %q status %q failures %d", fullName, stars, etag, status, failures)
	}
	if lastSynced != formatTime(now) || lastSuccessful != formatTime(now) || nextSync != formatTime(now.Add(6*time.Hour)) {
		t.Fatalf("sync times = last %q successful %q next %q", lastSynced, lastSuccessful, nextSync)
	}
	var snapshots int
	var source string
	if err := database.QueryRow(`SELECT COUNT(*) FROM star_snapshots WHERE repo_id = ?`, repoID).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT source FROM star_snapshots WHERE repo_id = ? ORDER BY captured_at DESC LIMIT 1`, repoID).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if snapshots != 2 || source != "repository_detail" {
		t.Fatalf("snapshots = %d latest source = %q", snapshots, source)
	}
}

func TestDetailSynchronizerDoesNotSnapshotNotModifiedRepository(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	database := newDetailTestDatabase(t)
	repoID := insertDetailRepository(t, database, now)
	getter := &fakeRepositoryGetter{responses: map[string]detailResponse{
		"old/name": {meta: github.ResponseMeta{StatusCode: 304, NotModified: true}},
	}}
	synchronizer := newDetailSynchronizer(database, getter, func() time.Time { return now }, 10)

	summary, err := synchronizer.SyncDue(context.Background(), 0)
	if err != nil {
		t.Fatalf("SyncDue() error = %v", err)
	}
	if summary.Requests != 1 || summary.NotModified != 1 || summary.Snapshots != 0 || summary.Updated != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	var stars, snapshots, failures int
	var etag, lastSynced, lastSuccessful, nextSync string
	if err := database.QueryRow(`SELECT stars_count, etag, last_synced_at, last_successful_fetch_at,
		next_sync_at, consecutive_failures FROM repositories WHERE id = ?`, repoID).
		Scan(&stars, &etag, &lastSynced, &lastSuccessful, &nextSync, &failures); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM star_snapshots WHERE repo_id = ?`, repoID).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if stars != 10 || snapshots != 1 || etag != `W/"old"` || failures != 0 {
		t.Fatalf("repository after 304 = stars %d snapshots %d etag %q failures %d", stars, snapshots, etag, failures)
	}
	if lastSynced != formatTime(now) || lastSuccessful != "2026-10-23T11:00:00Z" || nextSync != formatTime(now.Add(6*time.Hour)) {
		t.Fatalf("sync times after 304 = last %q successful %q next %q", lastSynced, lastSuccessful, nextSync)
	}
}

func TestDetailSynchronizerPreservesHistoryAndSchedulesNotFoundRepository(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	database := newDetailTestDatabase(t)
	repoID := insertDetailRepository(t, database, now)
	runID := insertDetailRun(t, database)
	getter := &fakeRepositoryGetter{responses: map[string]detailResponse{
		"old/name": {err: &github.APIError{Kind: github.ErrorNotFound, StatusCode: 404, Message: "Not Found"}},
	}}
	synchronizer := newDetailSynchronizer(database, getter, func() time.Time { return now }, 10)

	summary, err := synchronizer.SyncDue(context.Background(), runID)
	if err != nil {
		t.Fatalf("SyncDue() error = %v", err)
	}
	if summary.Requests != 1 || summary.Failed != 1 || summary.Snapshots != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	var fullName, status, errorCode, nextSync string
	var etag sql.NullString
	var stars, failures, snapshots int
	if err := database.QueryRow(`SELECT full_name, stars_count, sync_status, last_error_code,
		consecutive_failures, next_sync_at, etag FROM repositories WHERE id = ?`, repoID).
		Scan(&fullName, &stars, &status, &errorCode, &failures, &nextSync, &etag); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM star_snapshots WHERE repo_id = ?`, repoID).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if fullName != "old/name" || stars != 10 || snapshots != 1 || status != "inaccessible" || errorCode != "not_found" || failures != 1 {
		t.Fatalf("not-found repository = name %q stars %d snapshots %d status %q code %q failures %d",
			fullName, stars, snapshots, status, errorCode, failures)
	}
	if etag.Valid || nextSync != formatTime(now.Add(24*time.Hour)) {
		t.Fatalf("not-found scheduling = etag %+v next %q", etag, nextSync)
	}
	var events int
	if err := database.QueryRow(`SELECT COUNT(*) FROM job_run_events
		WHERE run_id = ? AND repo_id = ? AND event_code = 'repository_not_found'`, runID, repoID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("not-found events = %d", events)
	}
}

func TestDetailSynchronizerStopsOnRateLimitWithoutMutatingRepository(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	database := newDetailTestDatabase(t)
	repoID := insertDetailRepository(t, database, now)
	rateLimit := &github.APIError{
		Kind: github.ErrorPrimaryRateLimit, StatusCode: 403, Message: "API rate limit exceeded",
		RetryAt: now.Add(20 * time.Minute),
	}
	getter := &fakeRepositoryGetter{responses: map[string]detailResponse{"old/name": {err: rateLimit}}}
	synchronizer := newDetailSynchronizer(database, getter, func() time.Time { return now }, 10)

	summary, err := synchronizer.SyncDue(context.Background(), 0)
	if !errors.Is(err, rateLimit) {
		t.Fatalf("SyncDue() error = %v, want rate-limit error", err)
	}
	if summary.Requests != 1 || summary.RateLimited != 1 || summary.Failed != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	var status, etag, nextSync string
	var failures, snapshots int
	if err := database.QueryRow(`SELECT sync_status, etag, next_sync_at, consecutive_failures
		FROM repositories WHERE id = ?`, repoID).Scan(&status, &etag, &nextSync, &failures); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM star_snapshots WHERE repo_id = ?`, repoID).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || etag != `W/"old"` || nextSync != formatTime(now.Add(-time.Minute)) || failures != 0 || snapshots != 1 {
		t.Fatalf("repository changed on rate limit: status %q etag %q next %q failures %d snapshots %d",
			status, etag, nextSync, failures, snapshots)
	}
}
