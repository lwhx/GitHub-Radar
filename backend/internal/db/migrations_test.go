package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"
)

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
