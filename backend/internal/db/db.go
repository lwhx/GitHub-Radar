package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/github-radar/github-radar/migrations"
	_ "modernc.org/sqlite"
)

const directoryPermissions = 0o750

const (
	busyTimeoutMS = 5000
	maxOpenConns  = 8
)

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

	dsn, err := sqliteDSN(path)
	if err != nil {
		return nil, fmt.Errorf("build SQLite DSN for %q: %w", path, err)
	}
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database %q: %w", path, err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxOpenConns)

	db := &DB{sql: sqlDB}
	if err := initializeWAL(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("initialize SQLite database %q: %w", path, err)
	}
	if err := migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate SQLite database %q: %w", path, err)
	}
	return db, nil
}

func sqliteDSN(path string) (string, error) {
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	query.Add("_pragma", "recursive_triggers(ON)")
	return sqliteDSNWithQuery(path, query)
}

func sqliteDSNWithQuery(path string, query url.Values) (string, error) {
	slashPath := strings.ReplaceAll(path, `\`, "/")
	if !isWindowsDrivePath(slashPath) && !strings.HasPrefix(slashPath, "//") && !strings.HasPrefix(slashPath, "/") {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		slashPath = filepath.ToSlash(absolute)
	}
	uri := url.URL{Scheme: "file", RawQuery: query.Encode()}
	if strings.HasPrefix(slashPath, "//") {
		hostAndPath := strings.TrimPrefix(slashPath, "//")
		host, rest, found := strings.Cut(hostAndPath, "/")
		if !found || host == "" || rest == "" {
			return "", fmt.Errorf("invalid UNC database path")
		}
		uri.Host = host
		uri.Path = "/" + rest
	} else {
		if isWindowsDrivePath(slashPath) {
			slashPath = "/" + slashPath
		}
		uri.Path = slashPath
	}
	return uri.String(), nil
}

func isWindowsDrivePath(path string) bool {
	return len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && path[2] == '/'
}

// SQL exposes the connection pool to repository and service packages.
func (db *DB) SQL() *sql.DB { return db.sql }

// Ready verifies that SQLite can read and start a reversible write.
func (db *DB) Ready(ctx context.Context) error {
	conn, err := db.sql.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire SQLite readiness connection: %w", err)
	}
	defer conn.Close()

	var one int
	if err := conn.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("check SQLite read readiness: %w", err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite write readiness transaction: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO site_settings(key, json_value, updated_at)
		VALUES ('__radar_readiness__', '{}', '2000-01-01T00:00:00Z')`); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("check SQLite write readiness: %w", err)
	}
	if err := tx.Rollback(); err != nil {
		return fmt.Errorf("rollback SQLite write readiness transaction: %w", err)
	}
	return nil
}

// Close closes the SQLite connection pool.
func (db *DB) Close() error { return db.sql.Close() }

type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

func migrate(ctx context.Context, db *sql.DB) error {
	return migrateFS(ctx, db, migrations.Files)
}

func migrateFS(ctx context.Context, db *sql.DB, migrationFiles fs.FS) error {
	list, err := loadMigrations(migrationFiles)
	if err != nil {
		return err
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	if err := retryBusy(ctx, func() error {
		_, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE")
		return err
	}); err != nil {
		return fmt.Errorf("begin immediate migration transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY CHECK (version > 0),
		name TEXT NOT NULL,
		checksum TEXT NOT NULL CHECK (length(checksum) = 64 AND checksum NOT GLOB '*[^0-9a-f]*'),
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			CHECK (length(applied_at) = 20 AND applied_at GLOB '????-??-??T??:??:??Z'
				AND applied_at NOT GLOB '*[^0-9TZ:-]*' AND substr(applied_at, 12, 2) BETWEEN '00' AND '23'
				AND strftime('%Y-%m-%dT%H:%M:%SZ', applied_at) IS NOT NULL
				AND strftime('%Y-%m-%dT%H:%M:%SZ', applied_at) = applied_at)
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit schema_migrations initialization: %w", err)
	}
	committed = true

	if err := retryBusy(ctx, func() error {
		_, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE")
		return err
	}); err != nil {
		return fmt.Errorf("begin immediate migration transaction: %w", err)
	}
	committed = false

	for _, item := range list {
		var appliedName, appliedChecksum string
		err := conn.QueryRowContext(ctx, "SELECT name, checksum FROM schema_migrations WHERE version = ?", item.version).Scan(&appliedName, &appliedChecksum)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("check migration %d: %w", item.version, err)
		}
		if err == nil {
			if appliedName != item.name {
				return fmt.Errorf("migration %d name changed from %q to %q", item.version, appliedName, item.name)
			}
			if appliedChecksum != item.checksum {
				return fmt.Errorf("migration %d (%s) checksum changed", item.version, item.name)
			}
			continue
		}

		if _, err := conn.ExecContext(ctx, item.sql); err != nil {
			return fmt.Errorf("execute migration %d (%s): %w", item.version, item.name, err)
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migrations(version, name, checksum) VALUES (?, ?, ?)", item.version, item.name, item.checksum); err != nil {
			return fmt.Errorf("record migration %d: %w", item.version, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	committed = true
	return nil
}

func loadMigrations(migrationFiles fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	list := make([]migration, 0, len(entries))
	versions := make(map[int]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("parse migration version in %q: %w", entry.Name(), err)
		}
		if version <= 0 {
			return nil, fmt.Errorf("migration version in %q must be positive", entry.Name())
		}
		if previous, exists := versions[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d in %q and %q", version, previous, entry.Name())
		}
		versions[version] = entry.Name()
		contents, err := fs.ReadFile(migrationFiles, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(contents)
		list = append(list, migration{
			version:  version,
			name:     entry.Name(),
			sql:      string(contents),
			checksum: hex.EncodeToString(digest[:]),
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].version < list[j].version })
	return list, nil
}

func initializeWAL(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	return retryBusy(ctx, func() error {
		var mode string
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
			return err
		}
		if strings.EqualFold(mode, "wal") {
			return nil
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
			return err
		}
		if !strings.EqualFold(mode, "wal") {
			return fmt.Errorf("journal_mode = %q, want WAL", mode)
		}
		return nil
	})
}

func retryBusy(ctx context.Context, operation func() error) error {
	deadline := time.Now().Add(time.Duration(busyTimeoutMS) * time.Millisecond)
	for delay := 10 * time.Millisecond; ; delay *= 2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		if err == nil || !isBusyError(err) || time.Now().After(deadline) {
			return err
		}
		if delay > 200*time.Millisecond {
			delay = 200 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func isBusyError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked")
}
