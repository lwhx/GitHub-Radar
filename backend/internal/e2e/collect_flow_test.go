package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/api"
	"github.com/github-radar/github-radar/internal/auth"
	"github.com/github-radar/github-radar/internal/credentials"
	"github.com/github-radar/github-radar/internal/db"
	"github.com/github-radar/github-radar/internal/scheduler"
)

const (
	adminUsername = "radar"
	adminPassword = "integration-password"
	githubToken   = "dummy-integration-token-3456"
)

type githubStub struct {
	server    *httptest.Server
	searches  atomic.Int64
	details   atomic.Int64
	anonymous atomic.Int64
}

func newGitHubStub(t *testing.T) *githubStub {
	t.Helper()
	stub := &githubStub{}
	resetAt := time.Now().UTC().Add(time.Hour).Unix()
	items := []map[string]any{
		{
			"id": 501, "name": "octo-lamp", "full_name": "octo/octo-lamp",
			"owner": map[string]any{"login": "octo"}, "html_url": "https://github.com/octo/octo-lamp",
			"description": "第一个测试仓库", "language": "Go", "topics": []string{"cli"},
			"stargazers_count": 12, "forks_count": 1, "open_issues_count": 0,
			"created_at":     "2026-10-22T10:00:00Z",
			"updated_at":     "2026-10-22T10:05:00Z",
			"pushed_at":      "2026-10-22T10:05:00Z",
			"visibility":     "public",
			"default_branch": "main",
		},
		{
			"id": 502, "name": "octo-bell", "full_name": "octo/octo-bell",
			"owner": map[string]any{"login": "octo"}, "html_url": "https://github.com/octo/octo-bell",
			"description": nil, "language": nil, "topics": []string{},
			"stargazers_count": 3, "forks_count": 0, "open_issues_count": 1,
			"created_at":     "2026-10-22T11:00:00Z",
			"updated_at":     "2026-10-22T11:05:00Z",
			"pushed_at":      nil,
			"visibility":     "public",
			"default_branch": "main",
		},
	}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"message":"Requires authentication"}`)
				return
			}
			fmt.Fprint(w, `{"id":583231,"login":"octocat"}`)
		case "/rate_limit":
			fmt.Fprintf(w, `{"resources":{"core":{"limit":5000,"used":1,"remaining":4999,"reset":%d},`+
				`"search":{"limit":30,"used":1,"remaining":29,"reset":%d}}}`, resetAt, resetAt)
		case "/search/repositories":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				stub.anonymous.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"message":"Requires authentication"}`)
				return
			}
			stub.searches.Add(1)
			w.Header().Set("X-RateLimit-Limit", "30")
			w.Header().Set("X-RateLimit-Remaining", "28")
			w.Header().Set("X-RateLimit-Used", "2")
			w.Header().Set("X-RateLimit-Resource", "search")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(resetAt))
			payload, err := json.Marshal(map[string]any{
				"total_count": len(items), "incomplete_results": false, "items": items,
			})
			if err != nil {
				t.Errorf("marshal stub payload: %v", err)
				return
			}
			_, _ = w.Write(payload)
		case "/repos/octo/octo-lamp", "/repos/octo/octo-bell":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				stub.anonymous.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"message":"Requires authentication"}`)
				return
			}
			stub.details.Add(1)
			w.Header().Set("ETag", `"detail-v1"`)
			w.Header().Set("X-RateLimit-Limit", "5000")
			w.Header().Set("X-RateLimit-Remaining", "4997")
			w.Header().Set("X-RateLimit-Used", "3")
			w.Header().Set("X-RateLimit-Resource", "core")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(resetAt))
			index := 0
			if strings.HasSuffix(r.URL.Path, "octo-bell") {
				index = 1
			}
			detail := make(map[string]any, len(items[index]))
			for key, value := range items[index] {
				detail[key] = value
			}
			detail["stargazers_count"] = items[index]["stargazers_count"].(int) + 2
			payload, err := json.Marshal(detail)
			if err != nil {
				t.Errorf("marshal detail payload: %v", err)
				return
			}
			_, _ = w.Write(payload)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
		}
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

type harness struct {
	server *httptest.Server
	client *http.Client
	store  *db.DB
}

func newHarness(t *testing.T, stub *githubStub) *harness {
	t.Helper()
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := auth.NewService(store.SQL()).CreateAdmin(ctx, adminUsername, adminPassword); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	credentialService, err := credentials.NewService(store.SQL(), bytes.Repeat([]byte{7}, 32), credentials.WithGitHubBaseURL(stub.server.URL))
	if err != nil {
		t.Fatalf("credentials.NewService() error = %v", err)
	}
	source, err := scheduler.CredentialSearcherSource(credentialService, stub.server.URL)
	if err != nil {
		t.Fatalf("CredentialSearcherSource() error = %v", err)
	}
	detailSource, err := scheduler.CredentialRepositoryGetterSource(credentialService, stub.server.URL)
	if err != nil {
		t.Fatalf("CredentialRepositoryGetterSource() error = %v", err)
	}
	jobScheduler := scheduler.New(store.SQL(), source, detailSource)
	if err := jobScheduler.EnsureJob(ctx); err != nil {
		t.Fatalf("EnsureJob() error = %v", err)
	}

	var handler http.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	handler = api.New(api.Config{
		DB: store, Auth: auth.NewService(store.SQL()), Credentials: credentialService,
		Scheduler: jobScheduler, PublicBaseURL: server.URL, AppEnv: "development",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	return &harness{server: server, client: &http.Client{Jar: jar, Timeout: 20 * time.Second}, store: store}
}

func (harness *harness) do(t *testing.T, method, path, body, csrf string) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, harness.server.URL+path, reader)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	request.Header.Set("Origin", harness.server.URL)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := harness.client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	var payload map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode %s %s response %q: %v", method, path, raw, err)
		}
	}
	return response.StatusCode, payload
}

func dataObject(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("response has no data object: %+v", payload)
	}
	return data
}

func TestAdminCredentialAndCollectionFlow(t *testing.T) {
	stub := newGitHubStub(t)
	harness := newHarness(t, stub)

	status, payload := harness.do(t, http.MethodPost, "/api/v1/admin/auth/login",
		fmt.Sprintf(`{"username":%q,"password":%q}`, adminUsername, adminPassword), "")
	if status != http.StatusOK {
		t.Fatalf("login status = %d body=%+v", status, payload)
	}
	csrf, _ := dataObject(t, payload)["csrf_token"].(string)
	if csrf == "" {
		t.Fatal("login did not return a CSRF token")
	}

	status, payload = harness.do(t, http.MethodPost, "/api/v1/admin/credentials",
		fmt.Sprintf(`{"label":"主账号","token":%q,"priority":100}`, githubToken), csrf)
	if status != http.StatusCreated {
		t.Fatalf("create credential status = %d body=%+v", status, payload)
	}
	raw, _ := json.Marshal(payload)
	if strings.Contains(string(raw), githubToken) {
		t.Fatal("credential response echoed the token")
	}
	created := dataObject(t, payload)
	if created["token_hint"] != "…3456" {
		t.Fatalf("token_hint = %v", created["token_hint"])
	}
	if created["auth_login"] != "octocat" {
		t.Fatalf("auth_login = %v", created["auth_login"])
	}

	status, payload = harness.do(t, http.MethodPost, "/api/v1/admin/jobs/run", "", csrf)
	if status != http.StatusAccepted {
		t.Fatalf("run status = %d body=%+v", status, payload)
	}

	firstRun := waitForRun(t, harness, csrf, 1)
	if firstRun["status"] != "completed" {
		t.Fatalf("first run = %+v", firstRun)
	}
	if firstRun["created_count"].(float64) != 2 {
		t.Fatalf("created_count = %v, want 2", firstRun["created_count"])
	}
	if firstRun["request_count"].(float64) < 1 {
		t.Fatalf("request_count = %v", firstRun["request_count"])
	}

	status, payload = harness.do(t, http.MethodGet, "/api/v1/stats", "", "")
	if status != http.StatusOK {
		t.Fatalf("stats status = %d", status)
	}
	stats := dataObject(t, payload)
	if stats["repositories"].(float64) != 2 || stats["star_snapshots"].(float64) != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats["latest_sync_at"] == nil {
		t.Errorf("stats latest_sync_at = nil after a completed run")
	}

	// The search responses advertise remaining=28; the credential quota must follow it.
	status, payload = harness.do(t, http.MethodGet, "/api/v1/admin/credentials", "", "")
	if status != http.StatusOK {
		t.Fatalf("list credentials status = %d", status)
	}
	listed := dataObject(t, payload)["credentials"].([]any)[0].(map[string]any)
	var searchRemaining float64
	for _, entry := range listed["rate_limits"].([]any) {
		snapshot := entry.(map[string]any)
		if snapshot["resource"] == "search" {
			searchRemaining = snapshot["remaining"].(float64)
		}
	}
	if searchRemaining != 28 {
		t.Errorf("search remaining = %v, want 28 (rate limit headers not recorded)", searchRemaining)
	}

	// Make the freshly discovered repositories due and prove the second run uses
	// the separately quota-tracked core client for recurring detail snapshots.
	if _, err := harness.store.SQL().Exec(`UPDATE repositories SET next_sync_at = '2026-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	status, payload = harness.do(t, http.MethodPost, "/api/v1/admin/jobs/run", "", csrf)
	if status != http.StatusAccepted {
		t.Fatalf("second run status = %d body=%+v", status, payload)
	}
	secondRun := waitForRun(t, harness, csrf, 2)
	if secondRun["status"] != "completed" || secondRun["created_count"].(float64) != 0 || secondRun["updated_count"].(float64) != 4 {
		t.Fatalf("second run = %+v", secondRun)
	}
	if secondRun["snapshot_count"].(float64) != 2 || secondRun["request_count"].(float64) < 3 {
		t.Fatalf("second run detail counters = %+v", secondRun)
	}
	_, payload = harness.do(t, http.MethodGet, "/api/v1/stats", "", "")
	stats = dataObject(t, payload)
	if stats["repositories"].(float64) != 2 || stats["star_snapshots"].(float64) != 4 {
		t.Fatalf("stats after second run = %+v", stats)
	}

	if stub.anonymous.Load() != 0 {
		t.Errorf("stub saw %d unauthenticated GitHub requests", stub.anonymous.Load())
	}
	if stub.searches.Load() < 2 {
		t.Errorf("stub searches = %d, want at least 2 (one per run)", stub.searches.Load())
	}
	if stub.details.Load() != 2 {
		t.Errorf("stub detail requests = %d, want 2", stub.details.Load())
	}
	status, payload = harness.do(t, http.MethodGet, "/api/v1/admin/credentials", "", "")
	if status != http.StatusOK {
		t.Fatalf("list credentials after detail sync status = %d", status)
	}
	listed = dataObject(t, payload)["credentials"].([]any)[0].(map[string]any)
	var coreRemaining float64
	for _, entry := range listed["rate_limits"].([]any) {
		snapshot := entry.(map[string]any)
		if snapshot["resource"] == "core" {
			coreRemaining = snapshot["remaining"].(float64)
		}
	}
	if coreRemaining != 4997 {
		t.Errorf("core remaining = %v, want 4997 (detail rate limit headers not recorded)", coreRemaining)
	}
	var completedWindows int
	if err := harness.store.SQL().QueryRow(`SELECT COUNT(*) FROM collection_windows WHERE coverage_status = 'completed'`).Scan(&completedWindows); err != nil {
		t.Fatal(err)
	}
	if completedWindows == 0 {
		t.Error("no collection window reached completed")
	}
	waitForSchedulerIdle(t, harness)
}

func waitForSchedulerIdle(t *testing.T, harness *harness) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var activeLeases int
		err := harness.store.SQL().QueryRow(`SELECT COUNT(*) FROM collector_jobs WHERE lease_owner IS NOT NULL`).Scan(&activeLeases)
		if err != nil {
			t.Fatalf("query active scheduler leases: %v", err)
		}
		if activeLeases == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scheduler still held a database lease after run completion")
}

func waitForRun(t *testing.T, harness *harness, csrf string, want int) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		status, payload := harness.do(t, http.MethodGet, "/api/v1/admin/job-runs?limit=20", "", "")
		if status != http.StatusOK {
			t.Fatalf("list runs status = %d", status)
		}
		runs, _ := dataObject(t, payload)["runs"].([]any)
		if len(runs) >= want {
			finished := 0
			var newest map[string]any
			for _, entry := range runs {
				run := entry.(map[string]any)
				if run["status"] == "running" {
					continue
				}
				finished++
				if newest == nil {
					newest = run
				}
			}
			if finished >= want {
				return newest
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d finished run(s)", want)
	return nil
}
