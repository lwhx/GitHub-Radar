package db

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

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
