package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/auth"
)

func TestOverviewRequiresAuthenticationAndReturnsRealCounts(t *testing.T) {
	store := newTestStore(t)
	handler := newTestHandler(t, store)

	unauthorized := request(t, handler, http.MethodGet, "/api/v1/admin/overview")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d; body=%q", unauthorized.Code, unauthorized.Body.String())
	}

	createAdmin(t, store, "radar")
	session, err := auth.NewService(store.SQL()).Login(context.Background(), "radar", testPassword, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	clearSeededCategories(t, store)
	if _, err := store.SQL().Exec(`INSERT INTO categories(slug, title, enabled) VALUES ('one', 'One', 1), ('two', 'Two', 0)`); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://radar.test/api/v1/admin/overview", nil)
	req.AddCookie(&http.Cookie{Name: "radar_admin_session", Value: session.Token})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
	var data struct {
		RepositoryCount    int  `json:"repository_count"`
		SnapshotCount      int  `json:"snapshot_count"`
		CategoryCount      int  `json:"category_count"`
		AdminCount         int  `json:"admin_count"`
		ActiveSessionCount int  `json:"active_session_count"`
		DatabaseReady      bool `json:"database_ready"`
	}
	decodeData(t, recorder, &data)
	if data.RepositoryCount != 0 || data.SnapshotCount != 0 || data.CategoryCount != 2 || data.AdminCount != 1 || data.ActiveSessionCount != 1 || !data.DatabaseReady {
		t.Fatalf("overview = %+v", data)
	}
}

func TestSecurityHeadersAndServerGeneratedRequestID(t *testing.T) {
	store := newTestStore(t)
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.AppEnv = "production" })
	req := httptest.NewRequest(http.MethodGet, "http://radar.test/api/v1/admin/overview", nil)
	req.Header.Set("X-Request-ID", "attacker-controlled")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	requestID := recorder.Header().Get("X-Request-ID")
	if requestID == "" || requestID == "attacker-controlled" {
		t.Fatalf("X-Request-ID = %q", requestID)
	}
	for name, want := range map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Permissions-Policy":        "camera=(), microphone=(), geolocation=()",
		"X-Robots-Tag":              "noindex,nofollow",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	} {
		if got := recorder.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	csp := recorder.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "img-src 'self' data: https:") {
		t.Errorf("CSP = %q", csp)
	}
	if !strings.Contains(recorder.Body.String(), requestID) {
		t.Fatalf("error body request_id does not match header: %q", recorder.Body.String())
	}
}

func TestAPIUnknownRouteIsJSONAndNeverFallsBackToSPA(t *testing.T) {
	store := newTestStore(t)
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("SPA INDEX"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.StaticDir = staticDir })
	recorder := request(t, handler, http.MethodGet, "/api/v1/missing")
	if recorder.Code != http.StatusNotFound || recorder.Header().Get("Content-Type") != "application/json" || strings.Contains(recorder.Body.String(), "SPA INDEX") {
		t.Fatalf("response = %d %q %q", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}
}

func TestAPIMethodNotAllowedIsJSON(t *testing.T) {
	store := newTestStore(t)
	handler := newTestHandler(t, store)
	recorder := request(t, handler, http.MethodPost, "/api/v1/stats")
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = %d %q %q", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}
}

func TestStaticFilesAndSPAFallbackWithoutDirectoryListing(t *testing.T) {
	store := newTestStore(t)
	staticDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(staticDir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("SPA INDEX"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "assets", "app.js"), []byte("console.log('ok')"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.StaticDir = staticDir })

	asset := request(t, handler, http.MethodGet, "/assets/app.js")
	if asset.Code != http.StatusOK || !strings.Contains(asset.Header().Get("Content-Type"), "javascript") || !strings.Contains(asset.Body.String(), "console.log") {
		t.Fatalf("asset = %d %q %q", asset.Code, asset.Header().Get("Content-Type"), asset.Body.String())
	}
	fallback := request(t, handler, http.MethodGet, "/admin/settings")
	if fallback.Code != http.StatusOK || fallback.Body.String() != "SPA INDEX" {
		t.Fatalf("fallback = %d %q", fallback.Code, fallback.Body.String())
	}
	directory := request(t, handler, http.MethodGet, "/assets/")
	if directory.Code != http.StatusOK || directory.Body.String() != "SPA INDEX" || strings.Contains(directory.Body.String(), "app.js") {
		t.Fatalf("directory = %d %q", directory.Code, directory.Body.String())
	}
}

func TestStaticTraversalIsRejectedAndMissingDistIsUnderstandable(t *testing.T) {
	store := newTestStore(t)
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("SPA INDEX"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.StaticDir = staticDir })
	req := httptest.NewRequest(http.MethodGet, "http://radar.test/", nil)
	req.URL.Path = "/../outside.txt"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest || strings.Contains(recorder.Body.String(), "SPA INDEX") {
		t.Fatalf("traversal = %d %q", recorder.Code, recorder.Body.String())
	}

	missing := newTestHandler(t, store, func(cfg *Config) { cfg.StaticDir = filepath.Join(t.TempDir(), "missing") })
	missingRecorder := request(t, missing, http.MethodGet, "/anything")
	if missingRecorder.Code != http.StatusServiceUnavailable || !strings.Contains(missingRecorder.Body.String(), "web application is unavailable") {
		t.Fatalf("missing dist = %d %q", missingRecorder.Code, missingRecorder.Body.String())
	}
}

func TestStaticSymlinkCannotEscapeRoot(t *testing.T) {
	store := newTestStore(t)
	parent := t.TempDir()
	staticDir := filepath.Join(parent, "dist")
	if err := os.Mkdir(staticDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("SPA INDEX"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(outside, []byte("OUTSIDE SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(staticDir, "escape.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.StaticDir = staticDir })
	recorder := request(t, handler, http.MethodGet, "/escape.txt")
	if strings.Contains(recorder.Body.String(), "OUTSIDE SECRET") {
		t.Fatalf("static server followed symlink outside root: %d %q", recorder.Code, recorder.Body.String())
	}
}
