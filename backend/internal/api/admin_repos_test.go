package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/adminrepos"
	"github.com/github-radar/github-radar/internal/github"
)

type adminRepoGetter struct {
	repository github.Repository
}

func (getter adminRepoGetter) GetRepository(context.Context, string, string, string) (github.Repository, github.ResponseMeta, error) {
	return getter.repository, github.ResponseMeta{ETag: `"admin-v1"`}, nil
}

func adminRepoFixture() github.Repository {
	description := "managed repository"
	language := "Go"
	return github.Repository{
		ID: 99001, Owner: github.Owner{Login: "acme"}, Name: "radar", FullName: "acme/radar",
		HTMLURL: "https://github.com/acme/radar", Description: &description, Language: &language,
		DefaultBranch: "main", Topics: []string{"radar"}, StargazersCount: 88,
		Visibility: "public", CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC),
	}
}

func TestAdminRepositoryLifecycle(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	service := adminrepos.New(store.SQL(), func(context.Context) (adminrepos.RepositoryGetter, error) {
		return adminRepoGetter{repository: adminRepoFixture()}, nil
	}, func() time.Time { return time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC) })
	handler := newTestHandler(t, store, func(config *Config) { config.Repositories = service })
	cookies := adminCookiesForLifecycle(t, handler, store)
	seedCategory(t, store, 71, "managed", "Managed", 1, true)

	created := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/repos/import",
		`{"url":"https://github.com/acme/radar"}`, cookies, true)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"full_name":"acme/radar"`) || !strings.Contains(created.Body.String(), `"created":true`) {
		t.Fatalf("import status=%d body=%q", created.Code, created.Body.String())
	}
	var repositoryID int64
	if err := store.SQL().QueryRow(`SELECT id FROM repositories WHERE github_id = 99001`).Scan(&repositoryID); err != nil {
		t.Fatal(err)
	}

	listed := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/repos?q=acme%2Fradar&page=1&page_size=20", "", cookies, false)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"total":1`) || !strings.Contains(listed.Body.String(), `"acme/radar"`) {
		t.Fatalf("list status=%d body=%q", listed.Code, listed.Body.String())
	}

	patched := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/repos/"+strconv.FormatInt(repositoryID, 10),
		`{"hidden":true,"category_ids":[71]}`, cookies, true)
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), `"hidden":true`) || !strings.Contains(patched.Body.String(), `"category_ids":[71]`) {
		t.Fatalf("patch status=%d body=%q", patched.Code, patched.Body.String())
	}

	publicDetail := request(t, handler, http.MethodGet, "http://radar.test/api/v1/repos/acme/radar")
	if publicDetail.Code != http.StatusNotFound {
		t.Fatalf("hidden public detail status=%d body=%q", publicDetail.Code, publicDetail.Body.String())
	}
	hiddenResync := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/repos/"+strconv.FormatInt(repositoryID, 10),
		`{"resync":true}`, cookies, true)
	if hiddenResync.Code != http.StatusBadRequest {
		t.Fatalf("hidden resync status=%d body=%q", hiddenResync.Code, hiddenResync.Body.String())
	}
	restored := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/repos/"+strconv.FormatInt(repositoryID, 10),
		`{"hidden":false,"resync":true}`, cookies, true)
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), `"hidden":false`) || !strings.Contains(restored.Body.String(), `"sync_status":"pending"`) {
		t.Fatalf("restore status=%d body=%q", restored.Code, restored.Body.String())
	}
	var audits int
	if err := store.SQL().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action IN ('admin.repository.import', 'admin.repository.update') AND result = 'success'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 3 {
		t.Fatalf("successful audit count=%d", audits)
	}
}

func TestAdminRepositoryValidationAndSecurity(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	service := adminrepos.New(store.SQL(), func(context.Context) (adminrepos.RepositoryGetter, error) {
		return adminRepoGetter{repository: adminRepoFixture()}, nil
	})
	var logs bytes.Buffer
	handler := newTestHandler(t, store, func(config *Config) {
		config.Repositories = service
		config.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	})
	cookies := adminCookiesForLifecycle(t, handler, store)

	anonymous := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/repos", "", nil, false)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list status=%d", anonymous.Code)
	}
	noCSRF := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/repos/import", `{"url":"https://github.com/acme/radar"}`, cookies, false)
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("import without CSRF status=%d", noCSRF.Code)
	}
	const canary = "userinfo-canary-9274"
	submittedURL := (&url.URL{Scheme: "https", User: url.User(canary), Host: "github.com", Path: "acme/radar"}).String()
	invalid := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/repos/import", `{"url":`+strconv.Quote(submittedURL)+`}`, cookies, true)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "INVALID_REPOSITORY_URL") {
		t.Fatalf("invalid import status=%d body=%q", invalid.Code, invalid.Body.String())
	}
	var auditTarget string
	if err := store.SQL().QueryRow(`SELECT target FROM audit_logs WHERE action = 'admin.repository.import' AND result = 'failure' ORDER BY id DESC LIMIT 1`).Scan(&auditTarget); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditTarget, canary) || strings.Contains(logs.String(), canary) {
		t.Fatalf("URL userinfo leaked: audit=%q logs=%q", auditTarget, logs.String())
	}
	emptyPatch := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/repos/1", `{}`, cookies, true)
	if emptyPatch.Code != http.StatusBadRequest || !strings.Contains(emptyPatch.Body.String(), "INVALID_REQUEST") {
		t.Fatalf("empty patch status=%d body=%q", emptyPatch.Code, emptyPatch.Body.String())
	}
}
