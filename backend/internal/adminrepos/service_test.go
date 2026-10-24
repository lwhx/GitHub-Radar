package adminrepos

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/github-radar/github-radar/internal/db"
	"github.com/github-radar/github-radar/internal/github"
)

type fakeGetter struct {
	repository github.Repository
	meta       github.ResponseMeta
	err        error
	calls      int
	owner      string
	name       string
}

func (getter *fakeGetter) GetRepository(_ context.Context, owner, name, etag string) (github.Repository, github.ResponseMeta, error) {
	getter.calls++
	getter.owner = owner
	getter.name = name
	if etag != "" {
		panic("manual import must not use an ETag")
	}
	return getter.repository, getter.meta, getter.err
}

func newServiceTest(t *testing.T, getter *fakeGetter) (*Service, func()) {
	t.Helper()
	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	service := New(store.SQL(), func(context.Context) (RepositoryGetter, error) { return getter, nil }, func() time.Time { return now })
	return service, func() { store.Close() }
}

func publicRepository() github.Repository {
	homepage := "https://example.com"
	description := "A real repository"
	language := "Go"
	pushed := time.Date(2026, 10, 24, 8, 30, 0, 0, time.UTC)
	return github.Repository{
		ID: 42, Owner: github.Owner{Login: "owner"}, Name: "repo", FullName: "owner/repo",
		HTMLURL: "https://github.com/owner/repo", Homepage: &homepage, Description: &description,
		Language: &language, License: &github.License{SPDXID: "MIT"}, DefaultBranch: "main",
		Topics: []string{"go", "radar"}, StargazersCount: 123, ForksCount: 4, OpenIssuesCount: 2,
		Visibility: "public", CreatedAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		UpdatedAt: time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC), PushedAt: &pushed,
	}
}

func TestImportCreatesRepositoryAndInitialSnapshot(t *testing.T) {
	getter := &fakeGetter{repository: publicRepository(), meta: github.ResponseMeta{ETag: `"v1"`}}
	service, closeDB := newServiceTest(t, getter)
	defer closeDB()

	result, err := service.Import(context.Background(), "https://github.com/owner/repo")
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if !result.Created || result.Repository.ID == 0 || result.Repository.FullName != "owner/repo" || result.Repository.StarsCount != 123 {
		t.Fatalf("Import() = %+v", result)
	}
	if getter.calls != 1 || getter.owner != "owner" || getter.name != "repo" {
		t.Fatalf("getter calls=%d owner=%q name=%q", getter.calls, getter.owner, getter.name)
	}
	var snapshotCount int
	if err := service.db.QueryRow(`SELECT COUNT(*) FROM star_snapshots WHERE repo_id = ? AND source = 'manual_import' AND star_count = 123`, result.Repository.ID).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 1 {
		t.Fatalf("snapshot count = %d", snapshotCount)
	}
}

func TestImportUpdatesExistingRepositoryWithoutOverwritingModeration(t *testing.T) {
	getter := &fakeGetter{repository: publicRepository()}
	service, closeDB := newServiceTest(t, getter)
	defer closeDB()
	first, err := service.Import(context.Background(), "https://github.com/owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(`INSERT INTO categories(id, slug, title, sort_order, enabled) VALUES (707, 'manual', 'Manual', 0, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(`UPDATE repositories SET is_hidden = 1 WHERE id = ?`, first.Repository.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(`INSERT INTO repo_categories(repo_id, category_id, is_primary, source) VALUES (?, 707, 1, 'manual')`, first.Repository.ID); err != nil {
		t.Fatal(err)
	}
	getter.repository.StargazersCount = 150
	getter.repository.Description = nil

	second, err := service.Import(context.Background(), "https://github.com/owner/repo.git")
	if err != nil {
		t.Fatalf("second Import() error = %v", err)
	}
	if second.Created || !second.Repository.Hidden || second.Repository.StarsCount != 150 {
		t.Fatalf("second Import() = %+v", second)
	}
	if len(second.Repository.CategoryIDs) != 1 || second.Repository.CategoryIDs[0] != 707 {
		t.Fatalf("categories = %v", second.Repository.CategoryIDs)
	}
}

func TestImportRejectsInvalidURLBeforeGitHubRequest(t *testing.T) {
	getter := &fakeGetter{repository: publicRepository()}
	service, closeDB := newServiceTest(t, getter)
	defer closeDB()
	for _, raw := range []string{"http://github.com/owner/repo", "https://evil.example/owner/repo", "https://github.com/owner/repo/issues", "https://user@github.com/owner/repo", "https://github.com/owner/repo?tab=readme"} {
		if _, err := service.Import(context.Background(), raw); !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("Import(%q) error = %v, want ErrInvalidURL", raw, err)
		}
	}
	if getter.calls != 0 {
		t.Fatalf("GitHub called %d times", getter.calls)
	}
}

func TestImportRejectsNonPublicRepository(t *testing.T) {
	repository := publicRepository()
	repository.Private = true
	getter := &fakeGetter{repository: repository}
	service, closeDB := newServiceTest(t, getter)
	defer closeDB()
	if _, err := service.Import(context.Background(), "https://github.com/owner/repo"); !errors.Is(err, ErrNotPublic) {
		t.Fatalf("Import() error = %v, want ErrNotPublic", err)
	}
	var count int
	if err := service.db.QueryRow(`SELECT COUNT(*) FROM repositories`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("repository count=%d error=%v", count, err)
	}
}

func TestUpdateReplacesManualCategoriesAndQueuesVisibleRepositoryResync(t *testing.T) {
	getter := &fakeGetter{repository: publicRepository()}
	service, closeDB := newServiceTest(t, getter)
	defer closeDB()
	imported, err := service.Import(context.Background(), "https://github.com/owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(`INSERT INTO categories(id, slug, title, sort_order, enabled) VALUES (707, 'one', 'One', 0, 1), (708, 'two', 'Two', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	hidden := true
	categories := []int64{708, 707, 708}
	updated, err := service.Update(context.Background(), imported.Repository.ID, Update{Hidden: &hidden, CategoryIDs: &categories})
	if err != nil {
		t.Fatalf("hide Update() error = %v", err)
	}
	if !updated.Hidden || len(updated.CategoryIDs) != 2 || updated.CategoryIDs[0] != 708 || updated.CategoryIDs[1] != 707 {
		t.Fatalf("hide Update() = %+v", updated)
	}
	if _, err := service.Update(context.Background(), imported.Repository.ID, Update{Resync: true}); !errors.Is(err, ErrInvalidUpdate) {
		t.Fatalf("hidden resync error = %v, want ErrInvalidUpdate", err)
	}

	hidden = false
	updated, err = service.Update(context.Background(), imported.Repository.ID, Update{Hidden: &hidden, Resync: true})
	if err != nil {
		t.Fatalf("restore and resync Update() error = %v", err)
	}
	if updated.Hidden || updated.SyncStatus != "pending" {
		t.Fatalf("restore and resync Update() = %+v", updated)
	}
	var status, nextSync string
	if err := service.db.QueryRow(`SELECT sync_status, next_sync_at FROM repositories WHERE id = ?`, imported.Repository.ID).Scan(&status, &nextSync); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || nextSync != "2026-10-24T09:00:00Z" {
		t.Fatalf("sync status=%q next=%q", status, nextSync)
	}
}

func TestListSearchesRepositoriesIncludingHiddenRows(t *testing.T) {
	getter := &fakeGetter{repository: publicRepository()}
	service, closeDB := newServiceTest(t, getter)
	defer closeDB()
	first, err := service.Import(context.Background(), "https://github.com/owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	hidden := true
	if _, err := service.Update(context.Background(), first.Repository.ID, Update{Hidden: &hidden}); err != nil {
		t.Fatal(err)
	}
	getter.repository.ID = 43
	getter.repository.Name = "other"
	getter.repository.FullName = "owner/other"
	getter.repository.HTMLURL = "https://github.com/owner/other"
	if _, err := service.Import(context.Background(), "https://github.com/owner/other"); err != nil {
		t.Fatal(err)
	}

	result, err := service.List(context.Background(), ListOptions{Query: "owner/repo", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if result.Total != 1 || len(result.Items) != 1 || !result.Items[0].Hidden || result.Items[0].FullName != "owner/repo" {
		t.Fatalf("List() = %+v", result)
	}
}

func TestListTreatsLikeMetacharactersLiterallyAndPaginates(t *testing.T) {
	getter := &fakeGetter{repository: publicRepository()}
	service, closeDB := newServiceTest(t, getter)
	defer closeDB()
	if _, err := service.Import(context.Background(), "https://github.com/owner/repo"); err != nil {
		t.Fatal(err)
	}
	getter.repository.ID = 43
	getter.repository.Name = "other"
	getter.repository.FullName = "owner/other"
	getter.repository.HTMLURL = "https://github.com/owner/other"
	if _, err := service.Import(context.Background(), "https://github.com/owner/other"); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"%", "_", `\`} {
		result, err := service.List(context.Background(), ListOptions{Query: query, Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("List(%q) error = %v", query, err)
		}
		if result.Total != 0 || len(result.Items) != 0 {
			t.Fatalf("List(%q) = %+v, metacharacter acted as wildcard", query, result)
		}
	}
	secondPage, err := service.List(context.Background(), ListOptions{Page: 2, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if secondPage.Total != 2 || len(secondPage.Items) != 1 || secondPage.Items[0].FullName != "owner/repo" {
		t.Fatalf("second page = %+v", secondPage)
	}
	if _, err := service.List(context.Background(), ListOptions{Page: 1, PageSize: 101}); !errors.Is(err, ErrInvalidUpdate) {
		t.Fatalf("invalid page size error = %v", err)
	}
}

func TestUpdateRejectsNonPositiveCategoryWithoutMutation(t *testing.T) {
	getter := &fakeGetter{repository: publicRepository()}
	service, closeDB := newServiceTest(t, getter)
	defer closeDB()
	imported, err := service.Import(context.Background(), "https://github.com/owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	hidden := true
	categories := []int64{0}
	if _, err := service.Update(context.Background(), imported.Repository.ID, Update{Hidden: &hidden, CategoryIDs: &categories}); !errors.Is(err, ErrInvalidUpdate) {
		t.Fatalf("Update() error = %v, want ErrInvalidUpdate", err)
	}
	var value int
	if err := service.db.QueryRow(`SELECT is_hidden FROM repositories WHERE id = ?`, imported.Repository.ID).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 0 {
		t.Fatalf("is_hidden = %d after rejected update", value)
	}
}

func TestUpdateMissingCategoryRollsBack(t *testing.T) {
	getter := &fakeGetter{repository: publicRepository()}
	service, closeDB := newServiceTest(t, getter)
	defer closeDB()
	imported, err := service.Import(context.Background(), "https://github.com/owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	hidden := true
	categories := []int64{999}
	if _, err := service.Update(context.Background(), imported.Repository.ID, Update{Hidden: &hidden, CategoryIDs: &categories}); !errors.Is(err, ErrCategoryNotFound) {
		t.Fatalf("Update() error = %v, want ErrCategoryNotFound", err)
	}
	var value int
	if err := service.db.QueryRow(`SELECT is_hidden FROM repositories WHERE id = ?`, imported.Repository.ID).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 0 {
		t.Fatalf("is_hidden = %d after rollback", value)
	}
}
