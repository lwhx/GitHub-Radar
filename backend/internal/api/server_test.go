package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/auth"
	"github.com/github-radar/github-radar/internal/db"
)

func newTestStore(t *testing.T) *db.DB {
	t.Helper()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newTestHandler(t *testing.T, store *db.DB, options ...func(*Config)) http.Handler {
	t.Helper()
	cfg := Config{
		DB:            store,
		Auth:          auth.NewService(store.SQL()),
		PublicBaseURL: "http://radar.test",
		StaticDir:     t.TempDir(),
		Now:           func() time.Time { return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC) },
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, option := range options {
		option(&cfg)
	}
	return New(cfg)
}

func request(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeData(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v; body=%q", err, recorder.Body.String())
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		t.Fatalf("decode data: %v; body=%q", err, recorder.Body.String())
	}
}

func TestHealthAndReadyEndpoints(t *testing.T) {
	store := newTestStore(t)
	handler := newTestHandler(t, store)

	for _, path := range []string{"/healthz", "/readyz"} {
		recorder := request(t, handler, http.MethodGet, path)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body=%q", path, recorder.Code, recorder.Body.String())
		}
		if got := recorder.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("GET %s Content-Type = %q", path, got)
		}
	}
}

func TestReadyReturnsGenericServiceUnavailableWhenDatabaseIsClosed(t *testing.T) {
	store := newTestStore(t)
	handler := newTestHandler(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	recorder := request(t, handler, http.MethodGet, "/readyz")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() == "" || containsAny(recorder.Body.String(), "database is closed", filepath.ToSlash(t.TempDir()), "SQL") {
		t.Fatalf("response leaks internal error: %q", recorder.Body.String())
	}
}

func TestStatsReturnsRealDatabaseCountsAndTimes(t *testing.T) {
	store := newTestStore(t)
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/stats")
	if recorder.Code != http.StatusOK {
		t.Fatalf("empty stats status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	var empty struct {
		Repositories int     `json:"repositories"`
		Snapshots    int     `json:"star_snapshots"`
		Categories   int     `json:"categories"`
		LatestSync   *string `json:"latest_sync_at"`
		DataUpdated  *string `json:"data_updated_at"`
	}
	decodeData(t, recorder, &empty)
	if empty.Repositories != 0 || empty.Snapshots != 0 || empty.Categories != 0 || empty.LatestSync != nil || empty.DataUpdated != nil {
		t.Fatalf("empty stats = %+v", empty)
	}

	_, err := store.SQL().Exec(`INSERT INTO repositories
		(github_id, owner, name, full_name, html_url, github_created_at, github_updated_at, first_seen_at, last_successful_fetch_at)
		VALUES (1, 'o', 'r', 'o/r', 'https://example.test/o/r', '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z', '2026-10-20T00:00:00Z', '2026-10-22T00:00:00Z');
		INSERT INTO star_snapshots(repo_id, star_count, captured_at, source, sample_status)
		VALUES (1, 7, '2026-10-22T01:00:00Z', 'test', 'ok');
		INSERT INTO categories(slug, title, enabled) VALUES ('enabled', 'Enabled', 1), ('disabled', 'Disabled', 0);`)
	if err != nil {
		t.Fatal(err)
	}
	recorder = request(t, handler, http.MethodGet, "/api/v1/stats")
	if recorder.Code != http.StatusOK {
		t.Fatalf("stats status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	var populated struct {
		Repositories int     `json:"repositories"`
		Snapshots    int     `json:"star_snapshots"`
		Categories   int     `json:"categories"`
		LatestSync   *string `json:"latest_sync_at"`
		DataUpdated  *string `json:"data_updated_at"`
	}
	decodeData(t, recorder, &populated)
	if populated.Repositories != 1 || populated.Snapshots != 1 || populated.Categories != 1 {
		t.Fatalf("counts = %+v", populated)
	}
	if populated.LatestSync == nil || *populated.LatestSync != "2026-10-22T00:00:00Z" || populated.DataUpdated == nil || *populated.DataUpdated != "2026-10-22T00:00:00Z" {
		t.Fatalf("times = %+v", populated)
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
