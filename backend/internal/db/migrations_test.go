package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/github-radar/github-radar/migrations"
)

func TestP12CollectorMigrationAddsRequiredState(t *testing.T) {
	t.Parallel()

	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	for table, expected := range map[string][]string{
		"repositories":       {"visibility", "etag", "next_sync_at", "last_error_code", "consecutive_failures"},
		"github_credentials": {"priority", "auth_login", "created_at", "updated_at", "last_error_code"},
		"api_rate_limits":    {"used", "cooldown_reason"},
		"collection_windows": {"job_run_id", "parent_id", "split_depth", "attempt_count", "next_attempt_at", "total_count", "incomplete_results", "next_url", "created_at", "updated_at", "completed_at"},
		"collector_jobs":     {"lease_owner", "lease_expires_at"},
		"job_runs":           {"trigger", "heartbeat_at", "retry_count", "rate_limited_count", "failed_count", "coverage_incomplete_count", "last_error_code", "snapshot_count", "not_modified_count"},
	} {
		columns := schemaColumns(t, store.SQL(), table)
		for _, column := range expected {
			if !columns[column] {
				t.Errorf("%s missing column %s", table, column)
			}
		}
	}
	var eventTableCount int
	if err := store.SQL().QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = 'job_run_events'`).Scan(&eventTableCount); err != nil {
		t.Fatalf("query job_run_events: %v", err)
	}
	if eventTableCount != 1 {
		t.Errorf("job_run_events table count = %d", eventTableCount)
	}
}

func TestORMKeywordCleanupMigrationRemovesOnlyPollutedAutoAssignments(t *testing.T) {
	t.Parallel()

	sqlDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := migrateFS(context.Background(), sqlDB, migrationFilesThrough(t, "0006_")); err != nil {
		t.Fatalf("migrate through version 6: %v", err)
	}

	insertRepo := func(githubID int64, fullName, description, topicsJSON string) int64 {
		t.Helper()
		parts := strings.SplitN(fullName, "/", 2)
		result, err := sqlDB.Exec(`INSERT INTO repositories (
			github_id, owner, name, full_name, html_url, description, topics_json,
			github_created_at, github_updated_at, first_seen_at, sync_status
		) VALUES (?, ?, ?, ?, ?, ?, ?, '2026-10-23T00:00:00Z', '2026-10-23T00:00:00Z', '2026-10-23T00:00:00Z', 'ok')`,
			githubID, parts[0], parts[1], fullName, "https://example.test/"+fullName, description, topicsJSON)
		if err != nil {
			t.Fatalf("insert repository %s: %v", fullName, err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	falsePositive := insertRepo(1, "me/performance", "platform information formatter", `[]`)
	topicORM := insertRepo(2, "me/query-builder", "typed queries", `["orm"]`)
	otherRule := insertRepo(3, "me/postgres-tools", "PostgreSQL database utilities", `[]`)
	manual := insertRepo(4, "me/transform", "normal formatter", `[]`)
	fallback := insertRepo(5, "me/format-notes", "personal knowledge platform", `[]`)
	manualOverride := insertRepo(6, "me/manual-override", "PostgreSQL database platform", `[]`)
	for _, assignment := range []struct {
		repoID int64
		source string
	}{
		{falsePositive, "auto"},
		{topicORM, "auto"},
		{otherRule, "auto"},
		{manual, "manual"},
	} {
		if _, err := sqlDB.Exec(`INSERT INTO repo_categories (repo_id, category_id, is_primary, source)
			VALUES (?, (SELECT id FROM categories WHERE slug = 'data-database'), 1, ?)`, assignment.repoID, assignment.source); err != nil {
			t.Fatalf("insert %s assignment for repository %d: %v", assignment.source, assignment.repoID, err)
		}
	}
	// 删除原主分类后，剩余自动分类必须接管主分类。
	if _, err := sqlDB.Exec(`INSERT INTO repo_categories (repo_id, category_id, is_primary, source) VALUES
		(?, (SELECT id FROM categories WHERE slug = 'data-database'), 1, 'auto'),
		(?, (SELECT id FROM categories WHERE slug = 'productivity'), 0, 'auto')`, fallback, fallback); err != nil {
		t.Fatalf("insert fallback assignments: %v", err)
	}
	// 人工覆盖存在时，迁移后不得残留任何自动归类。
	if _, err := sqlDB.Exec(`INSERT INTO repo_categories (repo_id, category_id, is_primary, source) VALUES
		(?, (SELECT id FROM categories WHERE slug = 'productivity'), 1, 'manual'),
		(?, (SELECT id FROM categories WHERE slug = 'data-database'), 0, 'auto')`, manualOverride, manualOverride); err != nil {
		t.Fatalf("insert manual override assignments: %v", err)
	}

	if err := migrateFS(context.Background(), sqlDB, migrations.Files); err != nil {
		t.Fatalf("apply cleanup migration: %v", err)
	}

	var badRuleCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM category_rules
		WHERE category_id = (SELECT id FROM categories WHERE slug = 'data-database')
			AND match_type = 'keyword' AND pattern = 'orm'`).Scan(&badRuleCount); err != nil {
		t.Fatalf("count bad ORM keyword rule: %v", err)
	}
	if badRuleCount != 0 {
		t.Errorf("bad ORM keyword rule count = %d, want 0", badRuleCount)
	}

	for _, test := range []struct {
		name   string
		repoID int64
		want   int
	}{
		{name: "false substring auto assignment removed", repoID: falsePositive, want: 0},
		{name: "exact ORM topic auto assignment kept", repoID: topicORM, want: 1},
		{name: "other database rule auto assignment kept", repoID: otherRule, want: 1},
		{name: "manual assignment kept", repoID: manual, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var count int
			if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM repo_categories
				WHERE repo_id = ? AND category_id = (SELECT id FROM categories WHERE slug = 'data-database')`, test.repoID).Scan(&count); err != nil {
				t.Fatalf("count assignment: %v", err)
			}
			if count != test.want {
				t.Errorf("assignment count = %d, want %d", count, test.want)
			}
		})
	}
	var fallbackPrimary int
	if err := sqlDB.QueryRow(`SELECT is_primary FROM repo_categories
		WHERE repo_id = ? AND category_id = (SELECT id FROM categories WHERE slug = 'productivity')`, fallback).Scan(&fallbackPrimary); err != nil {
		t.Fatalf("read fallback primary: %v", err)
	}
	if fallbackPrimary != 1 {
		t.Errorf("fallback primary = %d, want 1", fallbackPrimary)
	}
	var leftoverAuto int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM repo_categories WHERE repo_id = ? AND source = 'auto'`, manualOverride).Scan(&leftoverAuto); err != nil {
		t.Fatalf("count automatic assignments beside manual override: %v", err)
	}
	if leftoverAuto != 0 {
		t.Errorf("automatic assignments beside manual override = %d, want 0", leftoverAuto)
	}
}

func TestCollectionWindowsRejectDuplicateLogicalRangeAfterPageAdvance(t *testing.T) {
	t.Parallel()

	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const insertWindow = `INSERT INTO collection_windows
		(source, query_hash, time_from, time_to, page, coverage_status)
		VALUES ('github_search', 'created-public-v1', '2026-10-22T00:00:00Z', '2026-10-23T00:00:00Z', ?, 'pending')`
	if _, err := store.SQL().Exec(insertWindow, 2); err != nil {
		t.Fatalf("insert advanced window: %v", err)
	}
	if _, err := store.SQL().Exec(insertWindow, 1); err == nil {
		t.Fatal("duplicate logical window insert succeeded, want unique constraint error")
	}
}

func TestCollectionWindowLogicalIdentityMigrationReconcilesDuplicates(t *testing.T) {
	t.Parallel()

	sqlDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := migrateFS(context.Background(), sqlDB, migrationFilesThrough(t, "0003_")); err != nil {
		t.Fatalf("migrate through version 3: %v", err)
	}

	const insertWindow = `INSERT INTO collection_windows
		(source, query_hash, time_from, time_to, page, coverage_status, created_at, updated_at)
		VALUES ('github_search', 'created-public-v1', '2026-10-22T00:00:00Z', '2026-10-23T00:00:00Z', ?, 'pending', '2026-10-23T00:00:00Z', ?)`
	advanced, err := sqlDB.Exec(insertWindow, 3, "2026-10-23T00:03:00Z")
	if err != nil {
		t.Fatalf("insert advanced duplicate: %v", err)
	}
	advancedID, _ := advanced.LastInsertId()
	stale, err := sqlDB.Exec(insertWindow, 1, "2026-10-23T00:01:00Z")
	if err != nil {
		t.Fatalf("insert stale duplicate: %v", err)
	}
	staleID, _ := stale.LastInsertId()
	if _, err := sqlDB.Exec(`INSERT INTO collector_jobs(job_type, interval_seconds) VALUES ('test', 60)`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO job_runs(job_id, started_at, status) VALUES (1, '2026-10-23T00:00:00Z', 'running')`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO job_run_events(run_id, window_id, occurred_at, level, event_code, message)
		VALUES (1, ?, '2026-10-23T00:02:00Z', 'info', 'test', 'duplicate reference')`, staleID); err != nil {
		t.Fatal(err)
	}

	if err := migrateFS(context.Background(), sqlDB, migrations.Files); err != nil {
		t.Fatalf("migrate duplicate logical windows: %v", err)
	}
	var windowID int64
	var page, count int
	if err := sqlDB.QueryRow(`SELECT id, page, COUNT(*) OVER () FROM collection_windows
		WHERE source = 'github_search' AND query_hash = 'created-public-v1'
			AND time_from = '2026-10-22T00:00:00Z' AND time_to = '2026-10-23T00:00:00Z'`).Scan(&windowID, &page, &count); err != nil {
		t.Fatal(err)
	}
	if windowID != advancedID || page != 3 || count != 1 {
		t.Fatalf("reconciled window = id %d page %d count %d, want id %d page 3 count 1", windowID, page, count, advancedID)
	}
	var eventWindowID int64
	if err := sqlDB.QueryRow(`SELECT window_id FROM job_run_events WHERE event_code = 'test'`).Scan(&eventWindowID); err != nil {
		t.Fatal(err)
	}
	if eventWindowID != advancedID {
		t.Fatalf("event window id = %d, want keeper %d", eventWindowID, advancedID)
	}
}

func TestCollectionWindowLogicalIdentityMigrationPrefersMostAdvancedWindow(t *testing.T) {
	t.Parallel()

	sqlDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migrateFS(context.Background(), sqlDB, migrationFilesThrough(t, "0003_")); err != nil {
		t.Fatalf("migrate through version 3: %v", err)
	}

	const insertWindow = `INSERT INTO collection_windows
		(source, query_hash, time_from, time_to, page, coverage_status, next_url, attempt_count, created_at, updated_at)
		VALUES ('github_search', 'created-public-v1', '2026-10-22T00:00:00Z', '2026-10-23T00:00:00Z', ?, ?, ?, ?, '2026-10-23T00:00:00Z', ?)`
	var ids []int64
	for index, row := range []struct {
		page    int
		status  string
		nextURL any
		attempt int
	}{
		{page: 9, status: "coverage_incomplete", nextURL: nil, attempt: 3},
		{page: 1, status: "completed", nextURL: nil, attempt: 0},
		{page: 5, status: "split", nextURL: nil, attempt: 0},
		{page: 2, status: "pending", nextURL: "https://api.github.com/search/repositories?page=3", attempt: 1},
	} {
		updatedAt := fmt.Sprintf("2026-10-23T00:00:%02dZ", index)
		result, err := sqlDB.Exec(insertWindow, row.page, row.status, row.nextURL, row.attempt, updatedAt)
		if err != nil {
			t.Fatalf("insert %s duplicate: %v", row.status, err)
		}
		id, _ := result.LastInsertId()
		ids = append(ids, id)
	}

	if err := migrateFS(context.Background(), sqlDB, migrations.Files); err != nil {
		t.Fatalf("migrate duplicate logical windows: %v", err)
	}
	var windowID int64
	var status string
	var count int
	if err := sqlDB.QueryRow(`SELECT id, coverage_status, COUNT(*) OVER () FROM collection_windows
		WHERE source = 'github_search' AND query_hash = 'created-public-v1'`).Scan(&windowID, &status, &count); err != nil {
		t.Fatal(err)
	}
	if windowID != ids[1] || status != "completed" || count != 1 {
		t.Fatalf("reconciled window = id %d status %q count %d, want id %d completed count 1", windowID, status, count, ids[1])
	}
}

func TestCollectionWindowLogicalIdentityMigrationRedirectsChildParents(t *testing.T) {
	t.Parallel()

	sqlDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := migrateFS(context.Background(), sqlDB, migrationFilesThrough(t, "0003_")); err != nil {
		t.Fatalf("migrate through version 3: %v", err)
	}

	const insertWindow = `INSERT INTO collection_windows
		(source, query_hash, time_from, time_to, page, coverage_status, parent_id, split_depth, created_at, updated_at)
		VALUES ('github_search', 'created-public-v1', ?, ?, ?, ?, ?, ?, '2026-10-23T00:00:00Z', '2026-10-23T00:00:00Z')`
	advanced, err := sqlDB.Exec(insertWindow, "2026-10-22T00:00:00Z", "2026-10-23T00:00:00Z", 3, "split", nil, 0)
	if err != nil {
		t.Fatalf("insert advanced parent: %v", err)
	}
	parentID, _ := advanced.LastInsertId()
	if _, err := sqlDB.Exec(insertWindow, "2026-10-22T00:00:00Z", "2026-10-23T00:00:00Z", 1, "pending", nil, 0); err != nil {
		t.Fatalf("insert stale duplicate parent: %v", err)
	}
	stale, err := sqlDB.Exec(insertWindow, "2026-10-22T12:00:00Z", "2026-10-23T00:00:00Z", 1, "pending", parentID+1, 1)
	if err != nil {
		t.Fatalf("insert child with duplicate parent: %v", err)
	}
	childID, _ := stale.LastInsertId()

	if err := migrateFS(context.Background(), sqlDB, migrations.Files); err != nil {
		t.Fatalf("migrate duplicate logical windows: %v", err)
	}
	var storedParent int64
	if err := sqlDB.QueryRow(`SELECT parent_id FROM collection_windows WHERE id = ?`, childID).Scan(&storedParent); err != nil {
		t.Fatal(err)
	}
	if storedParent != parentID {
		t.Fatalf("child parent_id = %d, want keeper %d", storedParent, parentID)
	}
}

func migrationFilesThrough(t *testing.T, lastPrefix string) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() > lastPrefix+"~" {
			continue
		}
		contents, err := fs.ReadFile(migrations.Files, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = &fstest.MapFile{Data: contents}
	}
	return files
}

func schemaColumns(t *testing.T, database *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := database.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var sequence, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&sequence, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read table_info(%s): %v", table, err)
	}
	return columns
}

func TestSQLiteDSNEscapesWindowsDriveAndUNCPaths(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		host string
		want string
	}{
		{name: "drive", path: `C:\data\radar ?#%.db`, want: `/C:/data/radar ?#%.db`},
		{name: "UNC", path: `\\server\share\radar ?#%.db`, host: "server", want: `/share/radar ?#%.db`},
	} {
		t.Run(test.name, func(t *testing.T) {
			dsn, err := sqliteDSN(test.path)
			if err != nil {
				t.Fatalf("sqliteDSN() error = %v", err)
			}
			parsed, err := url.Parse(dsn)
			if err != nil {
				t.Fatalf("parse DSN %q: %v", dsn, err)
			}
			if parsed.Scheme != "file" || parsed.Host != test.host || parsed.Path != test.want {
				t.Errorf("sqliteDSN(%q) parsed as scheme=%q host=%q path=%q, want file/%q/%q", test.path, parsed.Scheme, parsed.Host, parsed.Path, test.host, test.want)
			}
			if strings.Contains(parsed.RawPath, "?") || strings.Contains(parsed.RawPath, "#") {
				t.Errorf("sqliteDSN(%q) left URI delimiters unescaped: %q", test.path, dsn)
			}
		})
	}
}

func TestSQLiteDSNPreservesPOSIXAbsolutePath(t *testing.T) {
	dsn, err := sqliteDSN("/app/data/radar.db")
	if err != nil {
		t.Fatalf("sqliteDSN() error = %v", err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DSN %q: %v", dsn, err)
	}
	if parsed.Scheme != "file" || parsed.Host != "" || parsed.Path != "/app/data/radar.db" {
		t.Errorf("sqliteDSN() parsed as scheme=%q host=%q path=%q", parsed.Scheme, parsed.Host, parsed.Path)
	}
}

func TestFailedMigrationRollsBackSchemaAndVersion(t *testing.T) {
	t.Parallel()

	sqlDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	files := fstest.MapFS{
		"0001_broken.sql": &fstest.MapFile{Data: []byte(`
			CREATE TABLE should_roll_back (id INTEGER PRIMARY KEY);
			THIS IS NOT VALID SQL;
		`)},
	}
	if err := migrateFS(context.Background(), sqlDB, files); err == nil {
		t.Fatal("migrateFS() error = nil, want migration error")
	}

	var tableCount int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = 'should_roll_back'").Scan(&tableCount); err != nil {
		t.Fatalf("query rolled-back table: %v", err)
	}
	if tableCount != 0 {
		t.Errorf("rolled-back table count = %d, want 0", tableCount)
	}

	var versionCount int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 1").Scan(&versionCount); err != nil {
		t.Fatalf("query rolled-back version: %v", err)
	}
	if versionCount != 0 {
		t.Errorf("rolled-back migration version count = %d, want 0", versionCount)
	}
}

func TestRetryBusyReturnsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0
	err := retryBusy(ctx, func() error {
		attempts++
		return errors.New("database is locked (SQLITE_BUSY)")
	})
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("retryBusy() error = %v, want context canceled", err)
	}
	if attempts != 0 {
		t.Errorf("retryBusy() attempts = %d, want 0", attempts)
	}
}

func TestAppliedMigrationRejectsChangedNameOrContents(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(fstest.MapFS)
		want   string
	}{
		{
			name: "name",
			mutate: func(files fstest.MapFS) {
				delete(files, "0001_create.sql")
				files["0001_renamed.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE sample (id INTEGER PRIMARY KEY);")}
			},
			want: "name",
		},
		{
			name: "contents",
			mutate: func(files fstest.MapFS) {
				files["0001_create.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE sample (id INTEGER PRIMARY KEY, changed TEXT);")}
			},
			want: "checksum",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sqlDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "radar.db"))
			if err != nil {
				t.Fatalf("sql.Open() error = %v", err)
			}
			defer sqlDB.Close()
			files := fstest.MapFS{
				"0001_create.sql": &fstest.MapFile{Data: []byte("CREATE TABLE sample (id INTEGER PRIMARY KEY);")},
			}
			if err := migrateFS(context.Background(), sqlDB, files); err != nil {
				t.Fatalf("initial migrateFS() error = %v", err)
			}
			test.mutate(files)
			err = migrateFS(context.Background(), sqlDB, files)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("migrateFS() changed migration error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestSchemaMigrationsRecordsChecksumAndCanonicalTime(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer sqlDB.Close()
	files := fstest.MapFS{
		"0001_create.sql": &fstest.MapFile{Data: []byte("CREATE TABLE sample (id INTEGER PRIMARY KEY);")},
	}
	if err := migrateFS(context.Background(), sqlDB, files); err != nil {
		t.Fatalf("migrateFS() error = %v", err)
	}
	var name, checksum, appliedAt string
	if err := sqlDB.QueryRow("SELECT name, checksum, applied_at FROM schema_migrations WHERE version = 1").Scan(&name, &checksum, &appliedAt); err != nil {
		t.Fatalf("query schema_migrations metadata: %v", err)
	}
	if name != "0001_create.sql" || len(checksum) != 64 || len(appliedAt) != 20 || !strings.HasSuffix(appliedAt, "Z") {
		t.Errorf("migration metadata = name %q checksum %q applied_at %q", name, checksum, appliedAt)
	}
	if _, err := sqlDB.Exec("INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (2, '0002_bad.sql', ?, '2026-10-23T00:00:00.1Z')", strings.Repeat("0", 64)); err == nil {
		t.Error("schema_migrations accepted non-canonical applied_at, want CHECK error")
	}
}

func TestLoadMigrationsNormalizesLineEndingsForChecksum(t *testing.T) {
	loadMigration := func(t *testing.T, contents string) migration {
		t.Helper()
		list, err := loadMigrations(fstest.MapFS{
			"0001_create.sql": &fstest.MapFile{Data: []byte(contents)},
		})
		if err != nil {
			t.Fatalf("loadMigrations() error = %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("loadMigrations() returned %d migrations, want 1", len(list))
		}
		return list[0]
	}

	const lfSQL = "CREATE TABLE sample (\n	id INTEGER PRIMARY KEY\n);\n"
	lfMigration := loadMigration(t, lfSQL)
	for name, contents := range map[string]string{
		"CRLF": strings.ReplaceAll(lfSQL, "\n", "\r\n"),
		"CR":   strings.ReplaceAll(lfSQL, "\n", "\r"),
	} {
		t.Run(name, func(t *testing.T) {
			got := loadMigration(t, contents)
			if got.checksum != lfMigration.checksum {
				t.Errorf("checksum = %q, want LF checksum %q", got.checksum, lfMigration.checksum)
			}
			if got.sql != lfSQL {
				t.Errorf("SQL = %q, want normalized SQL %q", got.sql, lfSQL)
			}
		})
	}

	changed := loadMigration(t, strings.Replace(lfSQL, "id INTEGER", "id TEXT", 1))
	if changed.checksum == lfMigration.checksum {
		t.Error("checksum did not change when migration contents changed")
	}
}

func TestLoadMigrationsRejectsNonPositiveAndDuplicateVersions(t *testing.T) {
	for _, test := range []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{
			name: "zero",
			files: fstest.MapFS{
				"0000_zero.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			},
			want: "positive",
		},
		{
			name: "negative",
			files: fstest.MapFS{
				"-1_negative.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			},
			want: "positive",
		},
		{
			name: "duplicate",
			files: fstest.MapFS{
				"0001_first.sql":  &fstest.MapFile{Data: []byte("SELECT 1;")},
				"0001_second.sql": &fstest.MapFile{Data: []byte("SELECT 2;")},
			},
			want: "duplicate migration version 1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadMigrations(test.files)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("loadMigrations() error = %v, want text %q", err, test.want)
			}
		})
	}
}
