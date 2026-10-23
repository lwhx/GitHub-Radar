package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/readmes"
)

type fakeReadmes struct {
	document readmes.Document
	err      error
}

func (fake *fakeReadmes) Get(context.Context, int64, string, string) (readmes.Document, error) {
	return fake.document, fake.err
}

func TestRepoStarsReturnsWindowedRealSeries(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Now = func() time.Time { return now } })
	seedPublicRepo(t, store, 9101, "acme", "stars-demo", 120)
	seedRankSnapshot(t, store, 9101, 120, now.Add(-2*time.Hour))
	seedRankSnapshot(t, store, 9101, 110, now.Add(-3*24*time.Hour))
	seedRankSnapshot(t, store, 9101, 90, now.Add(-20*24*time.Hour))
	seedRankSnapshot(t, store, 9101, 60, now.Add(-60*24*time.Hour))

	recorder := request(t, handler, http.MethodGet, "/api/v1/repos/acme/stars-demo/stars?window=7d")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Window           string      `json:"window"`
			Points           []starPoint `json:"points"`
			SampleCount      int         `json:"sample_count"`
			HistoryAvailable bool        `json:"history_available"`
			DataUpdatedAt    *string     `json:"data_updated_at"`
			Method           string      `json:"method"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Window != "7d" || payload.Data.SampleCount != 2 {
		t.Fatalf("window/sample = %q/%d", payload.Data.Window, payload.Data.SampleCount)
	}
	if !payload.Data.HistoryAvailable || len(payload.Data.Points) != 2 {
		t.Fatalf("points = %+v", payload.Data.Points)
	}
	if payload.Data.Points[0].StarCount != 110 || payload.Data.Points[1].StarCount != 120 {
		t.Fatalf("points order = %+v", payload.Data.Points)
	}
	if payload.Data.DataUpdatedAt == nil || !strings.HasPrefix(*payload.Data.DataUpdatedAt, "2026-10-24T10:00:00") {
		t.Fatalf("data_updated_at = %v", payload.Data.DataUpdatedAt)
	}
	if !strings.Contains(payload.Data.Method, "快照") || payload.Data.Method == "" {
		t.Fatalf("method note missing: %q", payload.Data.Method)
	}
}

func TestRepoStarsReportsMissingHistoryHonestly(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Now = func() time.Time { return now } })
	seedPublicRepo(t, store, 9102, "acme", "single-point", 10)
	seedRankSnapshot(t, store, 9102, 10, now.Add(-time.Hour))

	recorder := request(t, handler, http.MethodGet, "/api/v1/repos/acme/single-point/stars")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var payload struct {
		Data struct {
			Window           string `json:"window"`
			SampleCount      int    `json:"sample_count"`
			HistoryAvailable bool   `json:"history_available"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Window != "30d" || payload.Data.SampleCount != 1 || payload.Data.HistoryAvailable {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRepoStarsRejectsUnknownWindowAndHiddenRepo(t *testing.T) {
	store := newTestStore(t)
	handler := newTestHandler(t, store)
	seedPublicRepo(t, store, 9103, "acme", "hidden-demo", 10)
	if _, err := store.SQL().Exec(`UPDATE repositories SET is_hidden = 1 WHERE github_id = 9103`); err != nil {
		t.Fatal(err)
	}

	badWindow := request(t, handler, http.MethodGet, "/api/v1/repos/acme/hidden-demo/stars?window=1y")
	if badWindow.Code != http.StatusBadRequest || !strings.Contains(badWindow.Body.String(), "INVALID_WINDOW") {
		t.Fatalf("bad window status = %d; body=%s", badWindow.Code, badWindow.Body.String())
	}
	hidden := request(t, handler, http.MethodGet, "/api/v1/repos/acme/hidden-demo/stars?window=7d")
	if hidden.Code != http.StatusNotFound {
		t.Fatalf("hidden status = %d", hidden.Code)
	}
}

func TestRepoReadmeServesCachedDocument(t *testing.T) {
	store := newTestStore(t)
	fake := &fakeReadmes{document: readmes.Document{
		Status: "ok", Content: "# Demo\n净化后的正文", MediaType: "markdown",
		FetchedAt: time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC), Cached: true,
	}}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Readmes = fake })
	seedPublicRepo(t, store, 9201, "acme", "readme-demo", 30)

	recorder := request(t, handler, http.MethodGet, "/api/v1/repos/acme/readme-demo/readme")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Status    string `json:"status"`
			Format    string `json:"format"`
			Content   string `json:"content"`
			Cached    bool   `json:"cached"`
			Stale     bool   `json:"stale"`
			FetchedAt string `json:"fetched_at"`
			Repo      string `json:"repo"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Status != "ok" || payload.Data.Format != "markdown" || !payload.Data.Cached {
		t.Fatalf("payload = %+v", payload.Data)
	}
	if !strings.Contains(payload.Data.Content, "净化后的正文") || payload.Data.Repo != "acme/readme-demo" {
		t.Fatalf("content/repo = %q/%q", payload.Data.Content, payload.Data.Repo)
	}
}

func TestRepoReadmeMissingAndUpstreamFailure(t *testing.T) {
	store := newTestStore(t)
	seedPublicRepo(t, store, 9202, "acme", "no-readme", 5)
	missing := &fakeReadmes{document: readmes.Document{Status: "missing", FetchedAt: time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)}}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Readmes = missing })
	recorder := request(t, handler, http.MethodGet, "/api/v1/repos/acme/no-readme/readme")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"missing"`) {
		t.Fatalf("missing status = %d; body=%s", recorder.Code, recorder.Body.String())
	}

	broken := newTestHandler(t, store, func(cfg *Config) { cfg.Readmes = &fakeReadmes{err: readmes.ErrUpstream} })
	failed := request(t, broken, http.MethodGet, "/api/v1/repos/acme/no-readme/readme")
	if failed.Code != http.StatusBadGateway || !strings.Contains(failed.Body.String(), "README_UNAVAILABLE") {
		t.Fatalf("upstream status = %d; body=%s", failed.Code, failed.Body.String())
	}

	unconfigured := newTestHandler(t, store)
	unavailable := request(t, unconfigured, http.MethodGet, "/api/v1/repos/acme/no-readme/readme")
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured status = %d", unavailable.Code)
	}
}
