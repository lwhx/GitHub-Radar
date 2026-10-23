package db_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	database "github.com/github-radar/github-radar/internal/db"
)

func openTestDB(t *testing.T) *database.DB {
	t.Helper()
	store, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func tableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info(%s): %v", table, err)
	}
	return columns
}

func repositoryInsertSQL() string {
	return `INSERT INTO repositories (
		github_id, owner, name, full_name, html_url, topics_json,
		github_created_at, github_updated_at, first_seen_at, sync_status,
		description
	) VALUES (?, 'owner', 'repo', ?, 'https://example.test/repo', '[]',
		'2026-10-23T00:00:00Z', '2026-10-23T00:00:00Z', '2026-10-23T00:00:00Z', 'ok', ?)`
}

func insertRepository(t *testing.T, db *sql.DB, githubID int64, fullName, description string) {
	t.Helper()
	if _, err := db.Exec(repositoryInsertSQL(), githubID, fullName, description); err != nil {
		t.Fatalf("insert repository: %v", err)
	}
}

func ftsMatchCount(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM repositories_fts WHERE repositories_fts MATCH ?", query).Scan(&count); err != nil {
		t.Fatalf("search FTS for %q: %v", query, err)
	}
	return count
}

func TestRepositoriesFTSSynchronizesInsertUpdateAndDelete(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	insertRepository(t, store.SQL(), 1, "owner/one", "quasaroldterm")
	if got := ftsMatchCount(t, store.SQL(), "quasaroldterm"); got != 1 {
		t.Fatalf("FTS insert match count = %d, want 1", got)
	}

	if _, err := store.SQL().Exec(
		"UPDATE repositories SET description = ?, description_zh = ?, topics_json = ? WHERE github_id = 1",
		"nebula-new-term", "星云中文词", `["agentic-topic"]`,
	); err != nil {
		t.Fatalf("update repository: %v", err)
	}
	if got := ftsMatchCount(t, store.SQL(), "quasaroldterm"); got != 0 {
		t.Errorf("FTS old term match count after update = %d, want 0", got)
	}
	for _, query := range []string{"nebula", "星云中文词", "agentic"} {
		if got := ftsMatchCount(t, store.SQL(), query); got != 1 {
			t.Errorf("FTS updated term %q match count = %d, want 1", query, got)
		}
	}

	if _, err := store.SQL().Exec("DELETE FROM repositories WHERE github_id = 1"); err != nil {
		t.Fatalf("delete repository: %v", err)
	}
	if got := ftsMatchCount(t, store.SQL(), "nebula"); got != 0 {
		t.Errorf("FTS match count after delete = %d, want 0", got)
	}
}

func TestRepositoriesFTSReplaceRemovesOldTerms(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	insertRepository(t, store.SQL(), 1, "owner/one", "replaceoldterm")
	if _, err := store.SQL().Exec(strings.Replace(repositoryInsertSQL(), "INSERT INTO", "INSERT OR REPLACE INTO", 1), 1, "owner/one", "replacenewterm"); err != nil {
		t.Fatalf("replace repository: %v", err)
	}
	if got := ftsMatchCount(t, store.SQL(), "replaceoldterm"); got != 0 {
		t.Errorf("FTS old term match count after replace = %d, want 0", got)
	}
	if got := ftsMatchCount(t, store.SQL(), "replacenewterm"); got != 1 {
		t.Errorf("FTS new term match count after replace = %d, want 1", got)
	}
}

func TestSchemaContractsAndIndexes(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)

	wantColumns := map[string][]string{
		"repositories":       {"id", "github_id", "owner", "name", "full_name", "html_url", "homepage_url", "description", "description_zh", "language", "license_spdx", "default_branch", "topics_json", "stars_count", "forks_count", "open_issues_count", "is_fork", "is_archived", "is_disabled", "is_hidden", "github_created_at", "github_updated_at", "pushed_at", "first_seen_at", "last_synced_at", "last_successful_fetch_at", "sync_status"},
		"star_snapshots":     {"id", "repo_id", "star_count", "captured_at", "source", "sample_status"},
		"categories":         {"id", "slug", "title", "description", "sort_order", "enabled"},
		"category_rules":     {"id", "category_id", "match_type", "pattern", "priority", "enabled"},
		"repo_categories":    {"repo_id", "category_id", "is_primary", "source"},
		"collection_windows": {"id", "source", "query_hash", "time_from", "time_to", "page", "cursor_json", "coverage_status", "last_error"},
		"collector_jobs":     {"id", "job_type", "enabled", "interval_seconds", "next_run_at", "priority", "config_json"},
		"job_runs":           {"id", "job_id", "started_at", "finished_at", "status", "request_count", "created_count", "updated_count", "error_summary"},
		"github_credentials": {"id", "label", "auth_identity", "token_ciphertext", "token_hint", "enabled", "status", "last_checked_at"},
		"api_rate_limits":    {"credential_id", "resource", "limit_value", "remaining", "reset_at", "cooldown_until", "updated_at"},
		"admin_users":        {"id", "username", "password_hash", "created_at", "last_login_at"},
		"admin_sessions":     {"id", "admin_id", "session_hash", "csrf_hash", "expires_at", "created_at", "revoked_at"},
		"site_settings":      {"key", "json_value", "updated_at"},
		"audit_logs":         {"id", "admin_id", "action", "target", "result", "created_at", "request_id"},
	}
	for table, columns := range wantColumns {
		got := tableColumns(t, store.SQL(), table)
		for _, column := range columns {
			if !got[column] {
				t.Errorf("table %q missing column %q", table, column)
			}
		}
	}
	if got := tableColumns(t, store.SQL(), "github_credentials"); got["token"] || got["token_plaintext"] {
		t.Errorf("github_credentials contains a plaintext token column: %v", got)
	}

	wantIndexes := []string{
		"idx_repositories_github_created_at", "idx_repositories_stars_count",
		"idx_repositories_last_synced_at", "idx_repositories_language",
		"idx_repositories_is_hidden", "idx_star_snapshots_repo_captured",
	}
	for _, index := range wantIndexes {
		var count int
		if err := store.SQL().QueryRow("SELECT COUNT(*) FROM sqlite_schema WHERE type = 'index' AND name = ?", index).Scan(&count); err != nil {
			t.Fatalf("query index %q: %v", index, err)
		}
		if count != 1 {
			t.Errorf("index %q count = %d, want 1", index, count)
		}
	}

	insertRepository(t, store.SQL(), 1, "owner/one", "alpha")
	if _, err := store.SQL().Exec(repositoryInsertSQL(), 1, "owner/two", "beta"); err == nil {
		t.Error("duplicate github_id insert succeeded, want UNIQUE error")
	}
	if _, err := store.SQL().Exec(repositoryInsertSQL(), 2, "owner/one", "beta"); err == nil {
		t.Error("duplicate full_name insert succeeded, want UNIQUE error")
	}
	if _, err := store.SQL().Exec("UPDATE repositories SET is_hidden = 2 WHERE id = 1"); err == nil {
		t.Error("invalid boolean update succeeded, want CHECK error")
	}
}

func TestSiteSettingsKeyRejectsNull(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	for _, test := range []struct {
		name string
		sql  string
	}{
		{
			name: "omitted key",
			sql:  "INSERT INTO site_settings(json_value, updated_at) VALUES ('{}', '2026-10-23T00:00:00Z')",
		},
		{
			name: "explicit null key",
			sql:  "INSERT INTO site_settings(key, json_value, updated_at) VALUES (NULL, '{}', '2026-10-23T00:00:00Z')",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := store.SQL().Exec(test.sql); err == nil {
				t.Error("site setting insert with NULL key succeeded, want NOT NULL error")
			}
		})
	}
}

func TestIdentityAndJunctionUniquenessConstraints(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	if _, err := store.SQL().Exec("INSERT INTO categories(id, slug, title) VALUES (1, 'ai', 'AI')"); err != nil {
		t.Fatalf("insert category: %v", err)
	}
	if _, err := store.SQL().Exec("INSERT INTO categories(id, slug, title) VALUES (2, 'ai', 'Duplicate')"); err == nil {
		t.Error("duplicate category slug insert succeeded, want UNIQUE error")
	}

	insertRepository(t, store.SQL(), 1, "owner/one", "alpha")
	if _, err := store.SQL().Exec("INSERT INTO repo_categories(repo_id, category_id, source) VALUES (1, 1, 'auto')"); err != nil {
		t.Fatalf("insert repo category: %v", err)
	}
	if _, err := store.SQL().Exec("INSERT INTO repo_categories(repo_id, category_id, source) VALUES (1, 1, 'manual')"); err == nil {
		t.Error("duplicate repo category insert succeeded, want UNIQUE error")
	}

	if _, err := store.SQL().Exec("INSERT INTO admin_users(id, username, password_hash, created_at) VALUES (1, 'admin', 'hash', '2026-10-23T00:00:00Z')"); err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	if _, err := store.SQL().Exec("INSERT INTO admin_users(id, username, password_hash, created_at) VALUES (2, 'admin', 'hash', '2026-10-23T00:00:00Z')"); err == nil {
		t.Error("duplicate username insert succeeded, want UNIQUE error")
	}
	if _, err := store.SQL().Exec("INSERT INTO admin_sessions(admin_id, session_hash, expires_at, created_at) VALUES (1, 'same-hash', '2026-10-24T00:00:00Z', '2026-10-23T00:00:00Z')"); err != nil {
		t.Fatalf("insert admin session: %v", err)
	}
	if _, err := store.SQL().Exec("INSERT INTO admin_sessions(admin_id, session_hash, expires_at, created_at) VALUES (1, 'same-hash', '2026-10-24T00:00:00Z', '2026-10-23T00:00:00Z')"); err == nil {
		t.Error("duplicate session hash insert succeeded, want UNIQUE error")
	}
}

func TestTimestampColumnsEnforceUTCRFC3339(t *testing.T) {
	t.Parallel()

	for _, timestamp := range []string{
		"not-a-timestamp",
		"2026-99-99T00:00:00Z",
		"2026-02-30T00:00:00Z",
		"2026-10-23T00:00:00.Z",
	} {
		t.Run(timestamp, func(t *testing.T) {
			store := openTestDB(t)
			_, err := store.SQL().Exec(`INSERT INTO repositories (
				github_id, owner, name, full_name, html_url, topics_json,
				github_created_at, github_updated_at, first_seen_at, sync_status
			) VALUES (1, 'owner', 'repo', 'owner/repo', 'https://example.test/repo', '[]',
				?, '2026-10-23T00:00:00Z', '2026-10-23T00:00:00Z', 'ok')`, timestamp)
			if err == nil {
				t.Errorf("invalid UTC RFC3339 timestamp %q insert succeeded, want CHECK error", timestamp)
			}
		})
	}
}

func TestTimestampColumnsRejectFractionalAndHour24Values(t *testing.T) {
	t.Parallel()

	for _, timestamp := range []string{
		"2026-10-23T00:00:00.1Z",
		"2026-10-23T00:00:00.123456Z",
		"2026-10-23T24:00:00Z",
	} {
		t.Run(timestamp, func(t *testing.T) {
			store := openTestDB(t)
			_, err := store.SQL().Exec(`INSERT INTO repositories (
				github_id, owner, name, full_name, html_url, topics_json,
				github_created_at, github_updated_at, first_seen_at, sync_status
			) VALUES (1, 'owner', 'repo', 'owner/repo', 'https://example.test/repo', '[]',
				?, '2026-10-23T00:00:00Z', '2026-10-23T00:00:00Z', 'ok')`, timestamp)
			if err == nil {
				t.Errorf("non-canonical timestamp %q insert succeeded, want CHECK error", timestamp)
			}
		})
	}
}

func TestTimestampTextOrderingMatchesChronologicalOrdering(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	insertRepository(t, store.SQL(), 1, "owner/one", "alpha")
	for _, timestamp := range []string{
		"2027-01-01T00:00:00Z",
		"2026-02-01T00:00:00Z",
		"2026-10-23T12:34:56Z",
	} {
		if _, err := store.SQL().Exec(
			"INSERT INTO star_snapshots(repo_id, star_count, captured_at, source, sample_status) VALUES (1, 1, ?, 'github', 'ok')",
			timestamp,
		); err != nil {
			t.Fatalf("insert timestamp %q: %v", timestamp, err)
		}
	}

	rows, err := store.SQL().Query("SELECT captured_at FROM star_snapshots ORDER BY captured_at")
	if err != nil {
		t.Fatalf("query timestamp order: %v", err)
	}
	defer rows.Close()
	want := []string{"2026-02-01T00:00:00Z", "2026-10-23T12:34:56Z", "2027-01-01T00:00:00Z"}
	for index := 0; rows.Next(); index++ {
		var got string
		if err := rows.Scan(&got); err != nil {
			t.Fatalf("scan timestamp: %v", err)
		}
		if index >= len(want) || got != want[index] {
			t.Fatalf("timestamp at index %d = %q, want %q", index, got, want[index])
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate timestamps: %v", err)
	}
}

func TestAllTimestampColumnsRejectNonRFC3339Values(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	insertRepository(t, store.SQL(), 1, "owner/one", "alpha")
	if _, err := store.SQL().Exec("INSERT INTO collector_jobs(id, job_type, enabled, interval_seconds, priority, config_json) VALUES (1, 'sync', 1, 60, 0, '{}')"); err != nil {
		t.Fatalf("insert collector job: %v", err)
	}
	if _, err := store.SQL().Exec("INSERT INTO github_credentials(id, label, token_ciphertext, token_hint) VALUES (1, 'primary', 'ciphertext', '...1234')"); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
	if _, err := store.SQL().Exec("INSERT INTO admin_users(id, username, password_hash, created_at) VALUES (1, 'admin', 'hash', '2026-10-23T00:00:00Z')"); err != nil {
		t.Fatalf("insert admin user: %v", err)
	}

	tests := []struct {
		name string
		sql  string
	}{
		{"snapshot captured_at", "INSERT INTO star_snapshots(repo_id, star_count, captured_at, source, sample_status) VALUES (1, 1, 'invalid', 'github', 'ok')"},
		{"window time_from", "INSERT INTO collection_windows(source, query_hash, time_from, time_to, coverage_status) VALUES ('github', 'hash', 'invalid', '2026-10-23T00:00:00Z', 'complete')"},
		{"job next_run_at", "UPDATE collector_jobs SET next_run_at = 'invalid' WHERE id = 1"},
		{"run started_at", "INSERT INTO job_runs(job_id, started_at, status) VALUES (1, 'invalid', 'running')"},
		{"credential last_checked_at", "UPDATE github_credentials SET last_checked_at = 'invalid' WHERE id = 1"},
		{"rate limit reset_at", "INSERT INTO api_rate_limits(credential_id, resource, limit_value, remaining, reset_at, updated_at) VALUES (1, 'core', 5000, 4999, 'invalid', '2026-10-23T00:00:00Z')"},
		{"admin last_login_at", "UPDATE admin_users SET last_login_at = 'invalid' WHERE id = 1"},
		{"session expires_at", "INSERT INTO admin_sessions(admin_id, session_hash, expires_at, created_at) VALUES (1, 'session', 'invalid', '2026-10-23T00:00:00Z')"},
		{"setting updated_at", "INSERT INTO site_settings(key, json_value, updated_at) VALUES ('site', '{}', 'invalid')"},
		{"audit created_at", "INSERT INTO audit_logs(admin_id, action, target, result, created_at) VALUES (1, 'test', 'db', 'ok', 'invalid')"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := store.SQL().Exec(test.sql); err == nil {
				t.Errorf("statement with invalid timestamp succeeded: %s", test.sql)
			}
		})
	}
}

func TestForeignKeysAndSnapshotDeduplication(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	const capturedAt = "2026-10-23T01:02:03Z"
	if _, err := store.SQL().Exec(
		"INSERT INTO star_snapshots(repo_id, star_count, captured_at, source, sample_status) VALUES (999, 1, ?, 'github', 'ok')",
		capturedAt,
	); err == nil {
		t.Fatal("orphan snapshot insert succeeded, want foreign key error")
	}

	insertRepository(t, store.SQL(), 1, "owner/one", "alpha")
	if _, err := store.SQL().Exec(
		"INSERT INTO star_snapshots(repo_id, star_count, captured_at, source, sample_status) VALUES (1, 1, ?, 'github', 'ok')",
		capturedAt,
	); err != nil {
		t.Fatalf("insert first snapshot: %v", err)
	}
	if _, err := store.SQL().Exec(
		"INSERT INTO star_snapshots(repo_id, star_count, captured_at, source, sample_status) VALUES (1, 2, ?, 'github', 'ok')",
		capturedAt,
	); err == nil {
		t.Fatal("duplicate snapshot insert succeeded, want UNIQUE error")
	}
}

func TestMigrationIsIdempotentAcrossOpen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "radar.db")
	first, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	var count int
	if err := second.SQL().QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 1").Scan(&count); err != nil {
		t.Fatalf("query migration: %v", err)
	}
	if count != 1 {
		t.Errorf("version 1 migration count = %d, want 1", count)
	}
}

func TestConcurrentOpenSerializesInitializationAndMigration(t *testing.T) {
	for round := 0; round < 3; round++ {
		path := filepath.Join(t.TempDir(), "radar.db")
		const workers = 12
		stores := make(chan *database.DB, workers)
		errs := make(chan error, workers)
		start := make(chan struct{})
		var group sync.WaitGroup

		for worker := 0; worker < workers; worker++ {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				store, err := database.Open(context.Background(), path)
				if err != nil {
					errs <- err
					return
				}
				stores <- store
			}()
		}
		close(start)
		group.Wait()
		close(errs)
		close(stores)

		for err := range errs {
			t.Errorf("round %d concurrent Open() error = %v", round, err)
		}
		var first *database.DB
		for store := range stores {
			if first == nil {
				first = store
			}
			if err := store.Close(); err != nil {
				t.Errorf("round %d Close() error = %v", round, err)
			}
		}
		if first == nil {
			t.Fatalf("round %d had no successful Open()", round)
		}

		verify, err := database.Open(context.Background(), path)
		if err != nil {
			t.Fatalf("round %d verification Open() error = %v", round, err)
		}
		var count int
		if err := verify.SQL().QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 1").Scan(&count); err != nil {
			_ = verify.Close()
			t.Fatalf("round %d query migration: %v", round, err)
		}
		if err := verify.Close(); err != nil {
			t.Errorf("round %d verification Close() error = %v", round, err)
		}
		if count != 1 {
			t.Errorf("round %d migration count = %d, want 1", round, count)
		}
	}
}

func TestOpenRespectsContextDeadlineDuringWriteLockContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radar.db")
	store, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("initial Open() error = %v", err)
	}
	lockConn, err := store.SQL().Conn(context.Background())
	if err != nil {
		_ = store.Close()
		t.Fatalf("acquire lock connection: %v", err)
	}
	if _, err := lockConn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		_ = lockConn.Close()
		_ = store.Close()
		t.Fatalf("begin immediate lock transaction: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	started := time.Now()
	contended, err := database.Open(ctx, path)
	elapsed := time.Since(started)
	cancel()
	t.Logf("contended Open() elapsed = %v", elapsed)
	if contended != nil {
		_ = contended.Close()
		t.Error("contended Open() returned non-nil database")
	}
	if err == nil {
		t.Error("contended Open() error = nil, want context deadline error")
	} else if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Errorf("contended Open() error = %v, want context deadline exceeded", err)
	}
	if elapsed >= 750*time.Millisecond {
		t.Errorf("contended Open() elapsed = %v, want less than 750ms", elapsed)
	}

	if _, rollbackErr := lockConn.ExecContext(context.Background(), "ROLLBACK"); rollbackErr != nil {
		t.Fatalf("rollback lock transaction: %v", rollbackErr)
	}
	if err := lockConn.Close(); err != nil {
		t.Fatalf("close lock connection: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial database: %v", err)
	}

	reopened, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() after releasing lock error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened database: %v", err)
	}
}

func TestInitialMigrationCreatesAllRequiredTables(t *testing.T) {
	t.Parallel()

	store, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	wantTables := []string{
		"schema_migrations",
		"repositories",
		"star_snapshots",
		"categories",
		"category_rules",
		"repo_categories",
		"collection_windows",
		"collector_jobs",
		"job_runs",
		"github_credentials",
		"api_rate_limits",
		"admin_users",
		"admin_sessions",
		"site_settings",
		"audit_logs",
	}
	for _, table := range wantTables {
		var count int
		if err := store.SQL().QueryRow(
			"SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = ?", table,
		).Scan(&count); err != nil {
			t.Fatalf("query table %q: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %q count = %d, want 1", table, count)
		}
	}
}

func TestReadySucceedsUntilDatabaseIsClosed(t *testing.T) {
	t.Parallel()

	store, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() before Close() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := store.Ready(context.Background()); err == nil {
		t.Fatal("Ready() after Close() error = nil, want error")
	}
}

func TestReadyRespectsContextDeadlineDuringWriteLockContention(t *testing.T) {
	store := openTestDB(t)
	lockConn, err := store.SQL().Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire lock connection: %v", err)
	}
	if _, err := lockConn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		_ = lockConn.Close()
		t.Fatalf("begin immediate lock transaction: %v", err)
	}
	lockHeld := true
	lockConnClosed := false
	defer func() {
		if lockHeld {
			_, _ = lockConn.ExecContext(context.Background(), "ROLLBACK")
		}
		if !lockConnClosed {
			_ = lockConn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = store.Ready(ctx)
	elapsed := time.Since(started)
	t.Logf("Ready() elapsed = %v", elapsed)
	if err == nil {
		t.Fatal("Ready() error = nil, want context deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Errorf("Ready() error = %v, want context deadline exceeded", err)
	}
	if elapsed >= 750*time.Millisecond {
		t.Errorf("Ready() elapsed = %v, want less than 750ms", elapsed)
	}

	if _, err := lockConn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatalf("rollback lock transaction: %v", err)
	}
	lockHeld = false
	if err := lockConn.Close(); err != nil {
		t.Fatalf("close lock connection: %v", err)
	}
	lockConnClosed = true
	store.SQL().SetMaxOpenConns(1)
	store.SQL().SetMaxIdleConns(1)
	if err := store.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() after releasing lock error = %v", err)
	}
	var busyTimeout int
	if err := store.SQL().QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout after Ready(): %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("busy_timeout after Ready() = %d, want 5000", busyTimeout)
	}
	var readinessRows int
	if err := store.SQL().QueryRow("SELECT COUNT(*) FROM site_settings WHERE key = '__radar_readiness__'").Scan(&readinessRows); err != nil {
		t.Fatalf("query readiness residue: %v", err)
	}
	if readinessRows != 0 {
		t.Errorf("readiness residue count = %d, want 0", readinessRows)
	}
}

func TestReadyFailsWhenConnectionIsQueryOnly(t *testing.T) {
	store := openTestDB(t)
	store.SQL().SetMaxOpenConns(1)
	store.SQL().SetMaxIdleConns(1)
	if _, err := store.SQL().Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatalf("enable query_only: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.SQL().Exec("PRAGMA query_only=OFF")
	})

	if err := store.Ready(context.Background()); err == nil {
		t.Fatal("Ready() with query_only=ON error = nil, want write readiness error")
	}
	if _, err := store.SQL().Exec("PRAGMA query_only=OFF"); err != nil {
		t.Fatalf("disable query_only: %v", err)
	}
	if err := store.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() after query_only=OFF error = %v", err)
	}
	var readinessRows int
	if err := store.SQL().QueryRow("SELECT COUNT(*) FROM site_settings WHERE key = '__radar_readiness__'").Scan(&readinessRows); err != nil {
		t.Fatalf("query readiness residue: %v", err)
	}
	if readinessRows != 0 {
		t.Errorf("readiness residue count = %d, want 0", readinessRows)
	}
}

func TestOpenReportsParentPathThatIsAFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parentFile := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("occupied"), 0o600); err != nil {
		t.Fatalf("create parent file: %v", err)
	}

	_, err := database.Open(context.Background(), filepath.Join(parentFile, "radar.db"))
	if err == nil {
		t.Fatal("Open() error = nil, want parent path error")
	}
	if !strings.Contains(err.Error(), "create database parent directory") || !strings.Contains(err.Error(), parentFile) {
		t.Errorf("Open() error = %q, want directory context and path", err)
	}
}

func TestOpenSupportsReservedCharactersInDatabasePath(t *testing.T) {
	names := []string{"radar#copy.db", "radar%copy.db", "radar copy.db"}
	if runtime.GOOS != "windows" {
		names = append(names, "radar?copy.db")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			store, err := database.Open(context.Background(), path)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("database file at exact requested path: %v", err)
			}
			if info.IsDir() {
				t.Fatalf("database path %q is a directory", path)
			}
		})
	}
}

func TestOpenCreatesParentConfiguresSQLiteAndMigrates(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "radar.db")
	store, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	var journalMode string
	if err := store.SQL().QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var foreignKeys int
	if err := store.SQL().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}

	var busyTimeout int
	if err := store.SQL().QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busyTimeout)
	}

	var recursiveTriggers int
	if err := store.SQL().QueryRow("PRAGMA recursive_triggers").Scan(&recursiveTriggers); err != nil {
		t.Fatalf("query recursive_triggers: %v", err)
	}
	if recursiveTriggers != 1 {
		t.Errorf("recursive_triggers = %d, want 1", recursiveTriggers)
	}

	var migrationCount int
	if err := store.SQL().QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 1").Scan(&migrationCount); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if migrationCount != 1 {
		t.Errorf("version 1 migration count = %d, want 1", migrationCount)
	}
}
