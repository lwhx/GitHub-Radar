package backups

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Kind string

const (
	KindManual Kind = "manual"
	KindDaily  Kind = "daily"
	KindWeekly Kind = "weekly"

	dailyRetention  = 7
	weeklyRetention = 4
	backupDirMode   = 0o750
	backupFileMode  = 0o640
)

var (
	backupNamePattern = regexp.MustCompile(`^radar-(manual|daily|weekly)-(\d{8}T\d{6}\.\d{9}Z)\.db$`)
	ErrInvalidName    = errors.New("invalid backup name")
	ErrNotFound       = errors.New("backup not found")
)

type Backup struct {
	Name        string    `json:"name"`
	Kind        Kind      `json:"kind"`
	CreatedAt   time.Time `json:"created_at"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	Verified    bool      `json:"verified"`
	DownloadURL string    `json:"download_url,omitempty"`
}

// Service creates online-consistent SQLite backups with VACUUM INTO.
type Service struct {
	db  *sql.DB
	dir string
	now func() time.Time
	mu  sync.Mutex
}

func NewService(database *sql.DB, directory string, now func() time.Time) (*Service, error) {
	if database == nil {
		return nil, errors.New("backup database is required")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve backup directory: %w", err)
	}
	if err := os.MkdirAll(absolute, backupDirMode); err != nil {
		return nil, fmt.Errorf("create backup directory %q: %w", absolute, err)
	}
	if now == nil {
		now = time.Now
	}
	return &Service{db: database, dir: absolute, now: now}, nil
}

func (service *Service) Create(ctx context.Context, kind Kind) (Backup, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.createLocked(ctx, kind)
}

func (service *Service) createLocked(ctx context.Context, kind Kind) (Backup, error) {
	if kind != KindManual && kind != KindDaily && kind != KindWeekly {
		return Backup{}, fmt.Errorf("unsupported backup kind %q", kind)
	}
	createdAt := service.now().UTC()
	name := fmt.Sprintf("radar-%s-%s.db", kind, createdAt.Format("20060102T150405.000000000Z"))
	path := filepath.Join(service.dir, name)
	if _, err := os.Stat(path); err == nil {
		return Backup{}, fmt.Errorf("backup %q already exists", name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Backup{}, fmt.Errorf("inspect backup destination: %w", err)
	}

	quoted := strings.ReplaceAll(filepath.ToSlash(path), "'", "''")
	if _, err := service.db.ExecContext(ctx, `VACUUM INTO '`+quoted+`'`); err != nil {
		_ = os.Remove(path)
		return Backup{}, fmt.Errorf("create SQLite backup: %w", err)
	}
	if err := os.Chmod(path, backupFileMode); err != nil {
		_ = os.Remove(path)
		return Backup{}, fmt.Errorf("set backup permissions: %w", err)
	}
	verified, err := verifySQLite(ctx, path)
	if err != nil || !verified {
		_ = os.Remove(path)
		return Backup{}, fmt.Errorf("verify created backup: %w", err)
	}
	backup, err := metadata(path)
	if err != nil {
		_ = os.Remove(path)
		return Backup{}, err
	}
	backup.Verified = true
	if err := service.pruneLocked(); err != nil {
		return Backup{}, err
	}
	return backup, nil
}

func (service *Service) List(ctx context.Context) ([]Backup, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return service.listUnlocked()
}

func (service *Service) listUnlocked() ([]Backup, error) {
	entries, err := os.ReadDir(service.dir)
	if err != nil {
		return nil, fmt.Errorf("list backup directory: %w", err)
	}
	items := make([]Backup, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !backupNamePattern.MatchString(entry.Name()) {
			continue
		}
		item, err := metadata(filepath.Join(service.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].Name > items[j].Name
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (service *Service) Open(name string) (string, error) {
	if !backupNamePattern.MatchString(name) || filepath.Base(name) != name {
		return "", ErrInvalidName
	}
	path := filepath.Join(service.dir, name)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("inspect backup: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", ErrInvalidName
	}
	return path, nil
}

func (service *Service) Verify(ctx context.Context, name string) (bool, error) {
	path, err := service.Open(name)
	if err != nil {
		return false, err
	}
	return verifySQLite(ctx, path)
}

// EnsureScheduled creates a daily backup when none exists in 24h and a weekly
// backup when none exists in 7d. It is idempotent within those windows.
func (service *Service) EnsureScheduled(ctx context.Context) ([]Backup, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	items, err := service.listUnlocked()
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	latest := map[Kind]time.Time{}
	for _, item := range items {
		if item.CreatedAt.After(latest[item.Kind]) {
			latest[item.Kind] = item.CreatedAt
		}
	}
	created := []Backup{}
	for _, due := range []struct {
		kind Kind
		age  time.Duration
	}{{KindDaily, 24 * time.Hour}, {KindWeekly, 7 * 24 * time.Hour}} {
		if timestamp, exists := latest[due.kind]; exists && now.Sub(timestamp) < due.age {
			continue
		}
		item, err := service.createLocked(ctx, due.kind)
		if err != nil {
			return created, err
		}
		created = append(created, item)
	}
	return created, nil
}

func (service *Service) pruneLocked() error {
	items, err := service.listUnlocked()
	if err != nil {
		return err
	}
	limits := map[Kind]int{KindDaily: dailyRetention, KindWeekly: weeklyRetention}
	counts := map[Kind]int{}
	for _, item := range items {
		limit, managed := limits[item.Kind]
		if !managed {
			continue
		}
		counts[item.Kind]++
		if counts[item.Kind] <= limit {
			continue
		}
		if err := os.Remove(filepath.Join(service.dir, item.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("prune backup %q: %w", item.Name, err)
		}
	}
	return nil
}

func metadata(path string) (Backup, error) {
	name := filepath.Base(path)
	matches := backupNamePattern.FindStringSubmatch(name)
	if matches == nil {
		return Backup{}, ErrInvalidName
	}
	createdAt, err := time.Parse("20060102T150405.000000000Z", matches[2])
	if err != nil {
		return Backup{}, fmt.Errorf("parse backup timestamp: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Backup{}, fmt.Errorf("stat backup %q: %w", name, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return Backup{}, fmt.Errorf("open backup for checksum: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return Backup{}, fmt.Errorf("hash backup: %w", copyErr)
	}
	if closeErr != nil {
		return Backup{}, fmt.Errorf("close backup: %w", closeErr)
	}
	return Backup{
		Name: name, Kind: Kind(matches[1]), CreatedAt: createdAt,
		SizeBytes: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func verifySQLite(ctx context.Context, path string) (bool, error) {
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return false, fmt.Errorf("open backup for verification: %w", err)
	}
	defer database.Close()
	var result string
	if err := database.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return false, fmt.Errorf("run backup quick_check: %w", err)
	}
	if result != "ok" {
		return false, fmt.Errorf("backup quick_check returned %q", result)
	}
	return true, nil
}
