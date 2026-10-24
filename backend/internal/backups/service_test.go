package backups

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/github-radar/github-radar/internal/db"
)

func newServiceForTest(t *testing.T, now time.Time) (*Service, *appdb.DB, *time.Time) {
	t.Helper()
	root := t.TempDir()
	store, err := appdb.Open(context.Background(), filepath.Join(root, "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	clock := now
	service, err := NewService(store.SQL(), filepath.Join(root, "backups"), func() time.Time { return clock })
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, store, &clock
}

func TestCreateProducesConsistentVerifiedBackup(t *testing.T) {
	service, store, _ := newServiceForTest(t, time.Date(2026, 10, 24, 12, 30, 0, 123, time.UTC))
	// 迁移 0006 已 seed 默认分类（含 slug 'ai'），本测试使用自己的夹具，先清空。
	if _, err := store.SQL().Exec(`DELETE FROM categories`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQL().Exec(`INSERT INTO categories (slug, title) VALUES ('ai', 'AI')`); err != nil {
		t.Fatal(err)
	}

	backup, err := service.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if backup.Kind != KindManual || backup.Name == "" || backup.SizeBytes <= 0 || backup.SHA256 == "" {
		t.Fatalf("backup = %+v", backup)
	}
	if !backup.Verified {
		t.Fatal("created backup must pass quick_check")
	}
	path, err := service.Open(backup.Name)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}

	backupDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer backupDB.Close()
	var count int
	if err := backupDB.QueryRow(`SELECT COUNT(*) FROM categories WHERE slug = 'ai'`).Scan(&count); err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if count != 1 {
		t.Fatalf("backup category count = %d", count)
	}
}

func TestListSortsNewestAndOpenRejectsTraversal(t *testing.T) {
	service, _, clock := newServiceForTest(t, time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC))
	first, err := service.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(time.Hour)
	second, err := service.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}

	items, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 2 || items[0].Name != second.Name || items[1].Name != first.Name {
		t.Fatalf("items = %+v", items)
	}
	for _, name := range []string{"../radar.db", "not-a-backup.db", "radar-manual-../../x.db"} {
		if _, err := service.Open(name); err == nil {
			t.Fatalf("Open(%q) should fail", name)
		}
	}
}

func TestOpenAndListRejectSymbolicLinks(t *testing.T) {
	service, _, _ := newServiceForTest(t, time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC))
	target := filepath.Join(t.TempDir(), "outside.db")
	if err := os.WriteFile(target, []byte("outside"), 0o640); err != nil {
		t.Fatal(err)
	}
	name := "radar-manual-20261024T080000.000000000Z.db"
	if err := os.Symlink(target, filepath.Join(service.dir, name)); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, err := service.Open(name); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("Open(symlink) error = %v, want ErrInvalidName", err)
	}
	items, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("List() included symlink: %+v", items)
	}
}

func TestVerifyDetectsCorruptBackup(t *testing.T) {
	service, _, _ := newServiceForTest(t, time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC))
	backup, err := service.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	path, err := service.Open(backup.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not sqlite"), 0o640); err != nil {
		t.Fatal(err)
	}

	verified, err := service.Verify(context.Background(), backup.Name)
	if err == nil || verified {
		t.Fatalf("Verify() = %v, %v; want false + error", verified, err)
	}
}

func TestRetentionKeepsSevenDailyAndFourWeekly(t *testing.T) {
	service, _, clock := newServiceForTest(t, time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC))
	ctx := context.Background()
	for day := 0; day < 10; day++ {
		*clock = time.Date(2026, 9, 1+day, 1, 0, 0, day, time.UTC)
		if _, err := service.Create(ctx, KindDaily); err != nil {
			t.Fatal(err)
		}
	}
	for week := 0; week < 6; week++ {
		*clock = time.Date(2026, 7, 1+week*7, 2, 0, 0, week, time.UTC)
		if _, err := service.Create(ctx, KindWeekly); err != nil {
			t.Fatal(err)
		}
	}
	*clock = time.Date(2026, 10, 24, 3, 0, 0, 0, time.UTC)
	if _, err := service.Create(ctx, KindManual); err != nil {
		t.Fatal(err)
	}

	items, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[Kind]int{}
	for _, item := range items {
		counts[item.Kind]++
	}
	if counts[KindDaily] != 7 || counts[KindWeekly] != 4 || counts[KindManual] != 1 {
		t.Fatalf("retained counts = %+v", counts)
	}
}

func TestEnsureScheduledCreatesOnlyDueBackups(t *testing.T) {
	service, _, clock := newServiceForTest(t, time.Date(2026, 10, 24, 4, 0, 0, 0, time.UTC))
	ctx := context.Background()
	created, err := service.EnsureScheduled(ctx)
	if err != nil {
		t.Fatalf("EnsureScheduled() error = %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("first ensure created %d, want daily+weekly", len(created))
	}
	created, err = service.EnsureScheduled(ctx)
	if err != nil || len(created) != 0 {
		t.Fatalf("second ensure = %d, %v; want none", len(created), err)
	}
	*clock = clock.Add(25 * time.Hour)
	created, err = service.EnsureScheduled(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0].Kind != KindDaily {
		t.Fatalf("after 25h created = %+v, want daily only", created)
	}
}
