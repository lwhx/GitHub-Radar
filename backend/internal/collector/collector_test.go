package collector

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	appdb "github.com/github-radar/github-radar/internal/db"
	"github.com/github-radar/github-radar/internal/github"
)

type page struct {
	result github.SearchResult
	next   string
	err    error
}

type fakeSearcher struct {
	mu      sync.Mutex
	windows map[string][]page
	next    map[string]page
	calls   int
}

func newFakeSearcher() *fakeSearcher {
	return &fakeSearcher{windows: map[string][]page{}, next: map[string]page{}}
}

func (fake *fakeSearcher) Search(_ context.Context, options github.SearchOptions) (github.SearchResult, github.ResponseMeta, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls++
	key := options.From.UTC().Format(time.RFC3339) + "/" + options.To.UTC().Format(time.RFC3339)
	pages := fake.windows[key]
	index := options.Page - 1
	if index < 0 || index >= len(pages) {
		return github.SearchResult{}, github.ResponseMeta{}, errors.New("unexpected search page " + key)
	}
	item := pages[index]
	if item.err != nil {
		return github.SearchResult{}, github.ResponseMeta{}, item.err
	}
	return item.result, github.ResponseMeta{NextURL: item.next}, nil
}

func (fake *fakeSearcher) SearchNext(_ context.Context, nextURL string) (github.SearchResult, github.ResponseMeta, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls++
	item, found := fake.next[nextURL]
	if !found {
		return github.SearchResult{}, github.ResponseMeta{}, errors.New("unexpected next URL " + nextURL)
	}
	if item.err != nil {
		return github.SearchResult{}, github.ResponseMeta{}, item.err
	}
	return item.result, github.ResponseMeta{NextURL: item.next}, nil
}

func repo(id int64, fullName string, stars int) github.Repository {
	owner, name, _ := cut(fullName)
	created := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	return github.Repository{
		ID: id, Owner: github.Owner{Login: owner}, Name: name, FullName: fullName,
		HTMLURL: "https://github.com/" + fullName, StargazersCount: stars,
		CreatedAt: created, UpdatedAt: created.Add(time.Hour), Visibility: "public",
		Topics: []string{"go"},
	}
}

func cut(fullName string) (string, string, bool) {
	for index, r := range fullName {
		if r == '/' {
			return fullName[:index], fullName[index+1:], true
		}
	}
	return fullName, "", false
}

func newTestCollector(t *testing.T, search Searcher) (*Collector, *sql.DB) {
	t.Helper()
	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	return newCollector(store.SQL(), search, func() time.Time { return now }, defaultMaxResults, defaultMaxSplitDepth), store.SQL()
}

func windowKey(from, to time.Time) string {
	return from.UTC().Format(time.RFC3339) + "/" + to.UTC().Format(time.RFC3339)
}

func TestEnsureWindowReusesLogicalWindowAfterPageAdvance(t *testing.T) {
	t.Parallel()

	worker, database := newTestCollector(t, newFakeSearcher())
	from := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	firstID, err := worker.EnsureWindow(context.Background(), from, to, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE collection_windows SET page = 2 WHERE id = ?`, firstID); err != nil {
		t.Fatal(err)
	}

	secondID, err := worker.EnsureWindow(context.Background(), from, to, 0)
	if err != nil {
		t.Fatalf("second EnsureWindow() error = %v", err)
	}
	if secondID != firstID {
		t.Fatalf("second window id = %d, want existing id %d", secondID, firstID)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM collection_windows
		WHERE source = 'github_search' AND query_hash = 'created-public-v1'
			AND time_from = ? AND time_to = ?`, formatTime(from), formatTime(to)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("logical window count = %d, want 1", count)
	}
}

func TestWindowFailureSchedulesRetryAndTerminatesAfterThirdAttempt(t *testing.T) {
	t.Parallel()

	searcher := newFakeSearcher()
	worker, database := newTestCollector(t, searcher)
	from := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	windowID, err := worker.EnsureWindow(context.Background(), from, from.Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("temporary search failure")
	if err := worker.markAttemptFailed(context.Background(), windowID, cause); err != nil {
		t.Fatal(err)
	}
	var status, nextAttempt string
	var attempts int
	if err := database.QueryRow(`SELECT coverage_status, attempt_count, next_attempt_at
		FROM collection_windows WHERE id = ?`, windowID).Scan(&status, &attempts, &nextAttempt); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || attempts != 1 || nextAttempt != "2026-10-23T12:05:00Z" {
		t.Fatalf("first failure = status %q attempts %d next %q", status, attempts, nextAttempt)
	}
	if err := worker.markAttemptFailed(context.Background(), windowID, cause); err != nil {
		t.Fatal(err)
	}
	if err := worker.markAttemptFailed(context.Background(), windowID, cause); err != nil {
		t.Fatal(err)
	}
	var nullableNext sql.NullString
	if err := database.QueryRow(`SELECT coverage_status, attempt_count, next_attempt_at
		FROM collection_windows WHERE id = ?`, windowID).Scan(&status, &attempts, &nullableNext); err != nil {
		t.Fatal(err)
	}
	if status != "coverage_incomplete" || attempts != 3 || nullableNext.Valid {
		t.Fatalf("terminal failure = status %q attempts %d next %+v", status, attempts, nullableNext)
	}
}

func TestProcessPendingAdoptsFailedWindowFromPreviousRunBySource(t *testing.T) {
	t.Parallel()

	searcher := newFakeSearcher()
	from := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	searcher.windows[windowKey(from, to)] = []page{{err: errors.New("temporary search failure")}}
	worker, database := newTestCollector(t, searcher)
	result, err := database.Exec(`INSERT INTO collector_jobs
		(job_type, enabled, interval_seconds, next_run_at, priority, config_json)
		VALUES ('test', 1, 60, '2026-10-23T12:00:00Z', 0, '{}')`)
	if err != nil {
		t.Fatal(err)
	}
	jobID, _ := result.LastInsertId()
	insertRun := func() int64 {
		t.Helper()
		result, err := database.Exec(`INSERT INTO job_runs
			(job_id, started_at, status, trigger) VALUES (?, '2026-10-23T12:00:00Z', 'running', 'schedule')`, jobID)
		if err != nil {
			t.Fatal(err)
		}
		runID, _ := result.LastInsertId()
		return runID
	}
	oldRunID := insertRun()
	newRunID := insertRun()
	windowID, err := worker.EnsureWindow(context.Background(), from, to, oldRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ProcessPending(context.Background(), oldRunID); err == nil {
		t.Fatal("first ProcessPending() error = nil")
	}
	if _, err := database.Exec(`UPDATE collection_windows SET next_attempt_at = '2026-10-23T11:59:00Z' WHERE id = ?`, windowID); err != nil {
		t.Fatal(err)
	}

	searcher.windows[windowKey(from, to)] = []page{{
		result: github.SearchResult{TotalCount: 1, Items: []github.Repository{repo(90, "recovered/repo", 5)}},
	}}
	summary, err := worker.ProcessPending(context.Background(), newRunID)
	if err != nil {
		t.Fatalf("recovery ProcessPending() error = %v", err)
	}
	if summary.Created != 1 {
		t.Fatalf("recovery summary = %+v", summary)
	}
	var assignedRun int64
	var status string
	if err := database.QueryRow(`SELECT job_run_id, coverage_status FROM collection_windows WHERE id = ?`, windowID).
		Scan(&assignedRun, &status); err != nil {
		t.Fatal(err)
	}
	if assignedRun != newRunID || status != "completed" {
		t.Fatalf("adopted window = run %d status %q", assignedRun, status)
	}
}

func TestProcessPendingStoresRepositoriesAndInitialSnapshotsOnce(t *testing.T) {
	t.Parallel()

	searcher := newFakeSearcher()
	from := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 22, 6, 0, 0, 0, time.UTC)
	searcher.windows[windowKey(from, to)] = []page{{
		result: github.SearchResult{TotalCount: 3, Items: []github.Repository{
			repo(1, "alpha/one", 10), repo(2, "beta/two", 20),
		}},
		next: "https://api.github.test/search/repositories?page=2",
	}}
	searcher.next["https://api.github.test/search/repositories?page=2"] = page{
		result: github.SearchResult{TotalCount: 3, Items: []github.Repository{repo(3, "gamma/three", 30)}},
	}

	collector, database := newTestCollector(t, searcher)
	windowID, err := collector.EnsureWindow(context.Background(), from, to, 0)
	if err != nil {
		t.Fatalf("EnsureWindow() error = %v", err)
	}
	summary, err := collector.ProcessPending(context.Background(), 0)
	if err != nil {
		t.Fatalf("ProcessPending() error = %v", err)
	}
	if summary.Created != 3 || summary.Pages != 2 || summary.IncompleteWindows != 0 {
		t.Fatalf("summary = %+v", summary)
	}

	var repositories, snapshots int
	if err := database.QueryRow("SELECT COUNT(*) FROM repositories").Scan(&repositories); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM star_snapshots").Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if repositories != 3 || snapshots != 3 {
		t.Fatalf("repositories = %d, snapshots = %d", repositories, snapshots)
	}
	var scheduled int
	if err := database.QueryRow(`SELECT COUNT(*) FROM repositories WHERE next_sync_at = '2026-10-23T12:30:00Z'`).Scan(&scheduled); err != nil {
		t.Fatal(err)
	}
	if scheduled != 3 {
		t.Fatalf("repositories scheduled for detail sync = %d, want 3", scheduled)
	}
	var status string
	var nextURL sql.NullString
	if err := database.QueryRow("SELECT coverage_status, next_url FROM collection_windows WHERE id = ?", windowID).Scan(&status, &nextURL); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || nextURL.Valid {
		t.Errorf("window status = %q, next_url = %+v", status, nextURL)
	}

	// Re-processing a completed window must not create a second initial snapshot.
	if _, err := collector.ProcessPending(context.Background(), 0); err != nil {
		t.Fatalf("second ProcessPending() error = %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM star_snapshots").Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if snapshots != 3 {
		t.Errorf("snapshots after re-run = %d", snapshots)
	}
}

func TestSplitWindowRollsBackParentWhenChildCreationFails(t *testing.T) {
	t.Parallel()

	searcher := newFakeSearcher()
	from := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)
	searcher.windows[windowKey(from, to)] = []page{{result: github.SearchResult{TotalCount: 1000}}}
	worker, database := newTestCollector(t, searcher)
	windowID, err := worker.EnsureWindow(context.Background(), from, to, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TRIGGER reject_split_children
		BEFORE INSERT ON collection_windows WHEN NEW.parent_id IS NOT NULL
		BEGIN SELECT RAISE(ABORT, 'reject child'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ProcessWindow(context.Background(), windowID); err == nil {
		t.Fatal("ProcessWindow() error = nil, want child insertion failure")
	}
	var status string
	var children int
	if err := database.QueryRow(`SELECT coverage_status FROM collection_windows WHERE id = ?`, windowID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM collection_windows WHERE parent_id = ?`, windowID).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || children != 0 {
		t.Fatalf("failed split left parent status %q with %d children", status, children)
	}
}

func TestDeepWindowStillSplitsWhenWiderThanMinimumWidth(t *testing.T) {
	t.Parallel()

	// Real GitHub volume: a 7-day window bisected six times (2h37m30s) still
	// holds ~24k results, far above the 900 cap. The width floor must govern,
	// not the depth counter.
	searcher := newFakeSearcher()
	from := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	to := from.Add(9450 * time.Second)
	searcher.windows[windowKey(from, to)] = []page{{
		result: github.SearchResult{TotalCount: 24000},
	}}
	mid := from.Add(4725 * time.Second)
	for index, half := range [][2]time.Time{{from, mid}, {mid, to}} {
		name := []string{"deep/first", "deep/second"}[index]
		searcher.windows[windowKey(half[0], half[1])] = []page{{
			result: github.SearchResult{TotalCount: 500, Items: []github.Repository{repo(int64(index)+1, name, 1)}},
		}}
	}

	worker, database := newTestCollector(t, searcher)
	windowID, err := worker.EnsureWindow(context.Background(), from, to, 0)
	if err != nil {
		t.Fatalf("EnsureWindow() error = %v", err)
	}
	if _, err := database.Exec(`UPDATE collection_windows SET split_depth = 6 WHERE id = ?`, windowID); err != nil {
		t.Fatal(err)
	}

	summary, err := worker.ProcessPending(context.Background(), 0)
	if err != nil {
		t.Fatalf("ProcessPending() error = %v", err)
	}
	if summary.SplitWindows != 1 || summary.IncompleteWindows != 0 || summary.Created != 2 {
		t.Fatalf("summary = %+v, want one split, no coverage_incomplete, two created repos", summary)
	}
	var status string
	if err := database.QueryRow(`SELECT coverage_status FROM collection_windows WHERE id = ?`, windowID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "split" {
		t.Fatalf("parent status = %q, want split", status)
	}
}

func TestProcessPendingSplitsWindowWhenSearchExceedsResultCap(t *testing.T) {
	t.Parallel()

	searcher := newFakeSearcher()
	from := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	searcher.windows[windowKey(from, to)] = []page{{
		result: github.SearchResult{TotalCount: 1200},
	}}
	mid := from.Add(12 * time.Hour)
	for index, half := range [][2]time.Time{{from, mid}, {mid, to}} {
		name := []string{"split/first", "split/second"}[index]
		searcher.windows[windowKey(half[0], half[1])] = []page{{
			result: github.SearchResult{TotalCount: 1, Items: []github.Repository{repo(int64(index)+1, name, 1)}},
		}}
	}

	collector, database := newTestCollector(t, searcher)
	if _, err := collector.EnsureWindow(context.Background(), from, to, 0); err != nil {
		t.Fatalf("EnsureWindow() error = %v", err)
	}
	summary, err := collector.ProcessPending(context.Background(), 0)
	if err != nil {
		t.Fatalf("ProcessPending() error = %v", err)
	}
	if summary.SplitWindows != 1 || summary.Created != 2 || summary.Windows != 3 {
		t.Fatalf("summary = %+v", summary)
	}
	var children int
	if err := database.QueryRow("SELECT COUNT(*) FROM collection_windows WHERE parent_id IS NOT NULL AND split_depth = 1").Scan(&children); err != nil {
		t.Fatal(err)
	}
	if children != 2 {
		t.Errorf("child windows = %d", children)
	}
	var splitStatus string
	if err := database.QueryRow("SELECT coverage_status FROM collection_windows WHERE parent_id IS NULL").Scan(&splitStatus); err != nil {
		t.Fatal(err)
	}
	if splitStatus != "split" {
		t.Errorf("parent status = %q", splitStatus)
	}
}

func TestProcessPendingSkipsNonPublicRepositories(t *testing.T) {
	t.Parallel()

	searcher := newFakeSearcher()
	from := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC)
	private := repo(11, "secret/private", 5)
	private.Private = true
	private.Visibility = "private"
	searcher.windows[windowKey(from, to)] = []page{{
		result: github.SearchResult{TotalCount: 2, Items: []github.Repository{repo(12, "public/visible", 7), private}},
	}}

	collector, database := newTestCollector(t, searcher)
	if _, err := collector.EnsureWindow(context.Background(), from, to, 0); err != nil {
		t.Fatalf("EnsureWindow() error = %v", err)
	}
	summary, err := collector.ProcessPending(context.Background(), 0)
	if err != nil {
		t.Fatalf("ProcessPending() error = %v", err)
	}
	if summary.Created != 1 || summary.Skipped != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	var hidden int
	if err := database.QueryRow("SELECT COUNT(*) FROM repositories WHERE full_name = 'secret/private'").Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if hidden != 0 {
		t.Errorf("private repository rows = %d", hidden)
	}
}

func TestSearchRefreshDoesNotChangeStarsWithoutSnapshot(t *testing.T) {
	t.Parallel()

	searcher := newFakeSearcher()
	firstFrom := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	firstTo := firstFrom.Add(time.Hour)
	searcher.windows[windowKey(firstFrom, firstTo)] = []page{{
		result: github.SearchResult{TotalCount: 1, Items: []github.Repository{repo(31, "stable/stars", 10)}},
	}}
	collector, database := newTestCollector(t, searcher)
	if _, err := collector.EnsureWindow(context.Background(), firstFrom, firstTo, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := collector.ProcessPending(context.Background(), 0); err != nil {
		t.Fatal(err)
	}

	secondFrom := firstTo
	secondTo := secondFrom.Add(time.Hour)
	searcher.windows[windowKey(secondFrom, secondTo)] = []page{{
		result: github.SearchResult{TotalCount: 1, Items: []github.Repository{repo(31, "stable/stars", 99)}},
	}}
	if _, err := collector.EnsureWindow(context.Background(), secondFrom, secondTo, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := collector.ProcessPending(context.Background(), 0); err != nil {
		t.Fatal(err)
	}

	var stars, snapshots int
	var nextSync string
	if err := database.QueryRow(`SELECT stars_count, next_sync_at FROM repositories WHERE github_id = 31`).Scan(&stars, &nextSync); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM star_snapshots`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if stars != 10 || snapshots != 1 {
		t.Fatalf("search refresh changed unsnapshotted stars: stars=%d snapshots=%d", stars, snapshots)
	}
	if nextSync != "2026-10-23T12:00:00Z" {
		t.Fatalf("next_sync_at = %q, want immediate detail refresh", nextSync)
	}
}

func TestProcessPendingResumesFromStoredNextURL(t *testing.T) {
	t.Parallel()

	searcher := newFakeSearcher()
	from := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 22, 6, 0, 0, 0, time.UTC)
	second := "https://api.github.test/search/repositories?page=2"
	searcher.windows[windowKey(from, to)] = []page{{
		result: github.SearchResult{TotalCount: 2, Items: []github.Repository{repo(21, "resume/first", 1)}},
		next:   second,
	}}
	searcher.next[second] = page{err: errors.New("connection reset")}

	collector, database := newTestCollector(t, searcher)
	windowID, err := collector.EnsureWindow(context.Background(), from, to, 0)
	if err != nil {
		t.Fatalf("EnsureWindow() error = %v", err)
	}
	if _, err := collector.ProcessWindow(context.Background(), windowID); err == nil {
		t.Fatal("ProcessWindow() error = nil, want page 2 failure")
	}
	var status string
	var nextURL sql.NullString
	var attempts int
	if err := database.QueryRow("SELECT coverage_status, next_url, attempt_count FROM collection_windows WHERE id = ?", windowID).
		Scan(&status, &nextURL, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || !nextURL.Valid || nextURL.String != second || attempts != 1 {
		t.Fatalf("window after failure = status %q next_url %+v attempts %d", status, nextURL, attempts)
	}
	if _, err := database.Exec(`UPDATE collection_windows SET next_attempt_at = '2026-10-23T11:59:00Z' WHERE id = ?`, windowID); err != nil {
		t.Fatal(err)
	}

	searcher.mu.Lock()
	searcher.next[second] = page{result: github.SearchResult{TotalCount: 2, Items: []github.Repository{repo(22, "resume/second", 2)}}}
	searcher.calls = 0
	searcher.mu.Unlock()

	if _, err := collector.ProcessPending(context.Background(), 0); err != nil {
		t.Fatalf("ProcessPending() resume error = %v", err)
	}
	searcher.mu.Lock()
	calls := searcher.calls
	searcher.mu.Unlock()
	if calls != 1 {
		t.Errorf("resume requests = %d, want 1 (page 2 only)", calls)
	}
	var snapshots int
	if err := database.QueryRow("SELECT COUNT(*) FROM star_snapshots").Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if snapshots != 2 {
		t.Errorf("snapshots = %d, want 2", snapshots)
	}
	if err := database.QueryRow("SELECT coverage_status FROM collection_windows WHERE id = ?", windowID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Errorf("resumed window status = %q", status)
	}
}
