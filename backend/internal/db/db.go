package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/github-radar/github-radar/migrations"
	_ "modernc.org/sqlite"
)

const directoryPermissions = 0o750

// DB owns the application's SQLite connection pool.
type DB struct {
	sql *sql.DB
}

// Open creates the database directory, configures SQLite, and applies migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, directoryPermissions); err != nil {
		return nil, fmt.Errorf("create database parent directory %q: %w", parent, err)
	}

	query := url.Values{}
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "busy_timeout(5000)")

	sqlDB, err := sql.Open("sqlite", path+"?"+query.Encode())
	if err != nil {
		return nil, fmt.Errorf("open SQLite database %q: %w", path, err)
	}

	db := &DB{sql: sqlDB}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("connect to SQLite database %q: %w", path, err)
	}
	if err := migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate SQLite database %q: %w", path, err)
	}
	return db, nil
}

// SQL exposes the connection pool to repository and service packages.
func (db *DB) SQL() *sql.DB { return db.sql }

// Ready verifies that SQLite accepts a lightweight query.
func (db *DB) Ready(ctx context.Context) error {
	if err := db.sql.PingContext(ctx); err != nil {
		return fmt.Errorf("check SQLite readiness: %w", err)
	}
	return nil
}

// Close closes the SQLite connection pool.
func (db *DB) Close() error { return db.sql.Close() }

type migration struct {
	version int
	name    string
	sql     string
}

func migrate(ctx context.Context, db *sql.DB) error {
	return migrateFS(ctx, db, migrations.Files)
}

func migrateFS(ctx context.Context, db *sql.DB, migrationFiles fs.FS) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationFiles, ".")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	list := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return fmt.Errorf("parse migration version in %q: %w", entry.Name(), err)
		}
		contents, err := fs.ReadFile(migrationFiles, entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		list = append(list, migration{version: version, name: entry.Name(), sql: string(contents)})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].version < list[j].version })

	for _, item := range list {
		var applied int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", item.version).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %d: %w", item.version, err)
		}
		if applied != 0 {
			continue
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx, item.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("execute migration %d (%s): %w", item.version, item.name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, name) VALUES (?, ?)", item.version, item.name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", item.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", item.version, err)
		}
	}
	return nil
}
