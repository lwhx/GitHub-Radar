package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github-radar/github-radar/internal/db"
)

func seedPublicRepo(t *testing.T, store *db.DB, githubID int64, owner, name string, stars int) {
	t.Helper()
	_, err := store.SQL().Exec(`INSERT INTO repositories
		(github_id, owner, name, full_name, html_url, topics_json, stars_count, forks_count, open_issues_count,
		 github_created_at, github_updated_at, first_seen_at, sync_status)
		VALUES (?, ?, ?, ?, ?, '[]', ?, 0, 0, '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-03T00:00:00Z', 'synced')`,
		githubID, owner, name, owner+"/"+name, "https://github.com/"+owner+"/"+name, stars)
	if err != nil {
		t.Fatalf("seed repository %s/%s: %v", owner, name, err)
	}
}

func setRepoDescription(t *testing.T, store *db.DB, githubID int64, description string) {
	t.Helper()
	if _, err := store.SQL().Exec(`UPDATE repositories SET description = ? WHERE github_id = ?`, description, githubID); err != nil {
		t.Fatal(err)
	}
}

type reposListMeta struct {
	Total         int     `json:"total"`
	Page          int     `json:"page"`
	Limit         int     `json:"limit"`
	DataUpdatedAt *string `json:"data_updated_at"`
}

func decodeReposList(t *testing.T, recorder *httptest.ResponseRecorder) ([]publicRepo, reposListMeta) {
	t.Helper()
	var payload struct {
		Data struct {
			Items []publicRepo   `json:"items"`
			Meta  map[string]any `json:"meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v; body = %s", err, recorder.Body.String())
	}
	metaRaw, err := json.Marshal(payload.Data.Meta)
	if err != nil {
		t.Fatal(err)
	}
	var meta reposListMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	return payload.Data.Items, meta
}

// 锁定可见性守卫：隐藏仓库永不出现在公开列表。
func TestListReposExcludesHiddenRepositories(t *testing.T) {
	store := newTestStore(t)
	seedPublicRepo(t, store, 201, "visible", "one", 10)
	seedPublicRepo(t, store, 202, "secret", "two", 999)
	if _, err := store.SQL().Exec(`UPDATE repositories SET is_hidden = 1 WHERE github_id = 202`); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/repos")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	items, meta := decodeReposList(t, recorder)
	if meta.Total != 1 || len(items) != 1 || items[0].FullName != "visible/one" {
		t.Fatalf("items = %+v meta = %+v, want only visible/one", items, meta)
	}
}

func TestListReposFiltersByQueryAcrossNameAndDescription(t *testing.T) {
	store := newTestStore(t)
	seedPublicRepo(t, store, 301, "acme", "agent-framework", 50)
	seedPublicRepo(t, store, 302, "other", "widget", 40)
	seedPublicRepo(t, store, 303, "zig", "lang", 30)
	setRepoDescription(t, store, 303, "agent tooling for humans")
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/repos?q=agent")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	items, meta := decodeReposList(t, recorder)
	if meta.Total != 2 {
		t.Fatalf("meta.total = %d, want 2; body = %s", meta.Total, recorder.Body.String())
	}
	got := []string{items[0].FullName, items[1].FullName}
	if got[0] != "acme/agent-framework" || got[1] != "zig/lang" {
		t.Fatalf("items = %v, want acme/agent-framework + zig/lang", got)
	}
}

func TestListReposSupportsSortEnumsAndRejectsUnknown(t *testing.T) {
	store := newTestStore(t)
	seedPublicRepo(t, store, 401, "old", "repo", 5)
	store.SQL().Exec(`UPDATE repositories SET github_created_at = '2025-01-01T00:00:00Z' WHERE github_id = 401`)
	seedPublicRepo(t, store, 402, "new", "repo", 3)
	store.SQL().Exec(`UPDATE repositories SET github_created_at = '2026-06-01T00:00:00Z' WHERE github_id = 402`)
	handler := newTestHandler(t, store)

	byCreated := request(t, handler, http.MethodGet, "/api/v1/repos?sort=created_desc")
	if byCreated.Code != http.StatusOK {
		t.Fatalf("created_desc status = %d", byCreated.Code)
	}
	items, _ := decodeReposList(t, byCreated)
	if len(items) != 2 || items[0].FullName != "new/repo" || items[1].FullName != "old/repo" {
		t.Fatalf("created_desc order = [%s, %s]", items[0].FullName, items[1].FullName)
	}

	unknown := request(t, handler, http.MethodGet, "/api/v1/repos?sort=bogus")
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("bogus sort status = %d, want 400", unknown.Code)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(unknown.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if payload.Error.Code != "unsupported_sort" {
		t.Fatalf("error.code = %q, want unsupported_sort", payload.Error.Code)
	}
}

func TestListReposPaginatesAndCapsLimit(t *testing.T) {
	store := newTestStore(t)
	seedPublicRepo(t, store, 501, "low", "stars", 1)
	seedPublicRepo(t, store, 502, "mid", "stars", 2)
	seedPublicRepo(t, store, 503, "high", "stars", 3)
	handler := newTestHandler(t, store)

	second := request(t, handler, http.MethodGet, "/api/v1/repos?page=2&limit=1")
	items, meta := decodeReposList(t, second)
	if second.Code != http.StatusOK || len(items) != 1 || items[0].FullName != "mid/stars" {
		t.Fatalf("page 2 items = %+v, want mid/stars", items)
	}
	if meta.Page != 2 || meta.Limit != 1 || meta.Total != 3 {
		t.Fatalf("meta = %+v, want page=2 limit=1 total=3", meta)
	}

	capped := request(t, handler, http.MethodGet, "/api/v1/repos?limit=500")
	_, meta = decodeReposList(t, capped)
	if capped.Code != http.StatusBadRequest {
		t.Fatalf("limit=500 status = %d, want 400", capped.Code)
	}
}

func TestGetRepoReturnsDetailForOwnerAndName(t *testing.T) {
	store := newTestStore(t)
	seedPublicRepo(t, store, 601, "acme", "rocket", 77)
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/repos/acme/rocket")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data publicRepo `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	repo := payload.Data
	if repo.FullName != "acme/rocket" || repo.StarsCount != 77 || repo.HtmlURL == "" || repo.Owner != "acme" || repo.Name != "rocket" {
		t.Fatalf("detail = %+v", repo)
	}
}

func TestGetRepoIsCaseInsensitive(t *testing.T) {
	store := newTestStore(t)
	seedPublicRepo(t, store, 602, "acme", "rocket", 10)
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/repos/ACME/Rocket")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestGetRepoReturnsRepoNotFoundForUnknown(t *testing.T) {
	store := newTestStore(t)
	seedPublicRepo(t, store, 603, "acme", "rocket", 10)
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/repos/acme/ghost")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Error.Code != "repo_not_found" {
		t.Fatalf("error.code = %q, want repo_not_found", payload.Error.Code)
	}
}

func TestGetRepoHidesHiddenRepositories(t *testing.T) {
	store := newTestStore(t)
	seedPublicRepo(t, store, 604, "secret", "thing", 999)
	if _, err := store.SQL().Exec(`UPDATE repositories SET is_hidden = 1 WHERE github_id = 604`); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/repos/secret/thing")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for hidden repository", recorder.Code)
	}
}

func TestListReposOrdersByStarsAndReportsMeta(t *testing.T) {
	store := newTestStore(t)
	seedPublicRepo(t, store, 101, "alpha", "one", 5)
	seedPublicRepo(t, store, 102, "beta", "two", 100)
	seedPublicRepo(t, store, 103, "gamma", "three", 42)
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/repos")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Items []struct {
				FullName   string   `json:"full_name"`
				StarsCount int      `json:"stars_count"`
				HtmlURL    string   `json:"html_url"`
				Topics     []string `json:"topics"`
			} `json:"items"`
			Meta struct {
				Total         int     `json:"total"`
				Page          int     `json:"page"`
				Limit         int     `json:"limit"`
				DataUpdatedAt *string `json:"data_updated_at"`
			} `json:"meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v; body = %s", err, recorder.Body.String())
	}
	if len(payload.Data.Items) != 3 {
		t.Fatalf("items = %d, want 3; body = %s", len(payload.Data.Items), recorder.Body.String())
	}
	wantOrder := []string{"beta/two", "gamma/three", "alpha/one"}
	for index, want := range wantOrder {
		if payload.Data.Items[index].FullName != want {
			t.Fatalf("items[%d].full_name = %s, want %s", index, payload.Data.Items[index].FullName, want)
		}
	}
	if payload.Data.Items[0].StarsCount != 100 || payload.Data.Items[0].HtmlURL == "" {
		t.Fatalf("first item = %+v", payload.Data.Items[0])
	}
	if payload.Data.Items[0].Topics == nil {
		t.Fatal("topics must serialize as an array, got null")
	}
	meta := payload.Data.Meta
	if meta.Total != 3 || meta.Page != 1 || meta.Limit != 20 {
		t.Fatalf("meta = %+v, want total=3 page=1 limit=20", meta)
	}
}
