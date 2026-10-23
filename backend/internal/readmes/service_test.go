package readmes

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appdb "github.com/github-radar/github-radar/internal/db"
	"github.com/github-radar/github-radar/internal/github"
)

type fakeProvider struct {
	readme github.Readme
	err    error
	calls  int
}

func (fake *fakeProvider) Readme(context.Context, string, string) (github.Readme, error) {
	fake.calls++
	return fake.readme, fake.err
}

func newServiceForTest(t *testing.T, provider Provider, now time.Time) (*Service, int64, *time.Time) {
	t.Helper()
	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	result, err := store.SQL().Exec(`INSERT INTO repositories
		(github_id, owner, name, full_name, html_url, topics_json, stars_count, forks_count, open_issues_count,
		 github_created_at, github_updated_at, first_seen_at, sync_status)
		VALUES (501, 'acme', 'demo', 'acme/demo', 'https://github.com/acme/demo', '[]', 10, 0, 0,
		 '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-03T00:00:00Z', 'synced')`)
	if err != nil {
		t.Fatalf("seed repository: %v", err)
	}
	repoID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	clock := now
	service := NewService(store.SQL(), provider, func() time.Time { return clock }, nil)
	return service, repoID, &clock
}

func TestGetSanitizesAndCachesReadme(t *testing.T) {
	provider := &fakeProvider{readme: github.Readme{Content: "# 标题\n<script>alert(1)</script>正文", MediaType: "markdown"}}
	service, repoID, _ := newServiceForTest(t, provider, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))

	document, err := service.Get(context.Background(), repoID, "acme", "demo")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if document.Status != "ok" || !strings.Contains(document.Content, "# 标题") {
		t.Fatalf("document = %+v", document)
	}
	if strings.Contains(document.Content, "script") || strings.Contains(document.Content, "alert(1)") {
		t.Fatalf("script not stripped: %q", document.Content)
	}

	cached, err := service.Get(context.Background(), repoID, "acme", "demo")
	if err != nil {
		t.Fatalf("second Get() error = %v", err)
	}
	if !cached.Cached {
		t.Fatal("second read should be served from cache")
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

func TestSanitizeBlocksUnsafeLinksAndHandlers(t *testing.T) {
	content, _ := Sanitize(`[点击](javascript:alert(1)) 和 <img src="data:image/png;base64,AAA" onerror="steal()">`)
	for _, banned := range []string{"javascript:", "data:", "onerror"} {
		if strings.Contains(strings.ToLower(content), banned) {
			t.Fatalf("sanitized content still contains %q: %q", banned, content)
		}
	}
}

func TestSanitizeTruncatesLongContent(t *testing.T) {
	content, truncated := Sanitize(strings.Repeat("字", MaxContentRunes+10))
	if !truncated {
		t.Fatal("expected truncation flag")
	}
	if len([]rune(content)) != MaxContentRunes {
		t.Fatalf("truncated length = %d, want %d", len([]rune(content)), MaxContentRunes)
	}
}

func TestGetCachesMissingReadme(t *testing.T) {
	provider := &fakeProvider{err: &github.APIError{Kind: github.ErrorNotFound, StatusCode: 404}}
	service, repoID, _ := newServiceForTest(t, provider, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))

	document, err := service.Get(context.Background(), repoID, "acme", "demo")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if document.Status != "missing" {
		t.Fatalf("status = %q, want missing", document.Status)
	}
	if _, err := service.Get(context.Background(), repoID, "acme", "demo"); err != nil {
		t.Fatalf("second Get() error = %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (missing result must be cached)", provider.calls)
	}
}

func TestGetFallsBackToStaleCopy(t *testing.T) {
	provider := &fakeProvider{readme: github.Readme{Content: "旧内容"}}
	start := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	service, repoID, clock := newServiceForTest(t, provider, start)
	if _, err := service.Get(context.Background(), repoID, "acme", "demo"); err != nil {
		t.Fatalf("warm cache error = %v", err)
	}

	*clock = start.Add(CacheTTL + time.Hour)
	provider.err = errors.New("network down")
	document, err := service.Get(context.Background(), repoID, "acme", "demo")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !document.Stale || document.Content != "旧内容" {
		t.Fatalf("document = %+v, want stale cached copy", document)
	}
}

func TestGetReturnsErrUpstreamWithoutCache(t *testing.T) {
	provider := &fakeProvider{err: errors.New("network down")}
	service, repoID, _ := newServiceForTest(t, provider, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))

	if _, err := service.Get(context.Background(), repoID, "acme", "demo"); !errors.Is(err, ErrUpstream) {
		t.Fatalf("Get() error = %v, want ErrUpstream", err)
	}
}
