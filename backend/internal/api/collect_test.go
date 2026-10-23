package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/credentials"
	"github.com/github-radar/github-radar/internal/scheduler"
)

type fakeCredentials struct {
	created    []credentials.CreateInput
	enabled    map[int64]bool
	tested     []int64
	deleted    []int64
	missingIDs map[int64]bool
	failWith   error
}

func (fake *fakeCredentials) List(context.Context) ([]credentials.CredentialDetail, error) {
	return []credentials.CredentialDetail{{Credential: credentials.Credential{ID: 1, Label: "primary", AuthLogin: "octocat", TokenHint: "…1234", Enabled: true}}}, nil
}

func (fake *fakeCredentials) Create(_ context.Context, input credentials.CreateInput) (credentials.Credential, error) {
	if fake.failWith != nil {
		return credentials.Credential{}, fake.failWith
	}
	fake.created = append(fake.created, input)
	return credentials.Credential{ID: 2, Label: input.Label, AuthLogin: "octocat", TokenHint: "…9999", Enabled: true}, nil
}

func (fake *fakeCredentials) SetEnabled(_ context.Context, id int64, enabled bool) error {
	if fake.enabled == nil {
		fake.enabled = map[int64]bool{}
	}
	fake.enabled[id] = enabled
	return nil
}

func (fake *fakeCredentials) Usable(context.Context, string) (credentials.UsableCredential, error) {
	return credentials.UsableCredential{}, credentials.ErrNoCredential
}

func (fake *fakeCredentials) Test(_ context.Context, id int64) (credentials.Credential, error) {
	if fake.missingIDs[id] {
		return credentials.Credential{}, credentials.ErrNotFound
	}
	fake.tested = append(fake.tested, id)
	return credentials.Credential{ID: id, Label: "primary", AuthIdentity: "8", AuthLogin: "hubot", TokenHint: "…1234", Enabled: true, Status: "active"}, nil
}

func (fake *fakeCredentials) Delete(_ context.Context, id int64) error {
	if fake.missingIDs[id] {
		return credentials.ErrNotFound
	}
	fake.deleted = append(fake.deleted, id)
	return nil
}

type fakeScheduler struct {
	runs        int
	precheckErr error
}

func (fake *fakeScheduler) EnsureJob(context.Context) error { return nil }
func (fake *fakeScheduler) Precheck(context.Context) error  { return fake.precheckErr }
func (fake *fakeScheduler) Jobs(context.Context) ([]scheduler.Job, error) {
	return []scheduler.Job{{ID: 1, JobType: "github_collection", Enabled: true}}, nil
}
func (fake *fakeScheduler) RunOnce(context.Context, string) (scheduler.Run, error) {
	fake.runs++
	return scheduler.Run{ID: 9, Status: "completed"}, nil
}
func (fake *fakeScheduler) JobRunHistory(context.Context, int) ([]scheduler.JobRun, error) {
	return []scheduler.JobRun{{ID: 9, Status: "completed", Trigger: "manual", StartedAt: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}}, nil
}
func (fake *fakeScheduler) RunEvents(context.Context, int64, int) ([]scheduler.RunEvent, error) {
	return []scheduler.RunEvent{{ID: 1, RunID: 9, Level: "error", EventCode: "collection_failed", Message: "boom"}}, nil
}
func (fake *fakeScheduler) Run(context.Context, int64) (scheduler.JobRun, error) {
	return scheduler.JobRun{ID: 9, Status: "completed"}, nil
}

func adminRequest(t *testing.T, handler http.Handler, method, target, body string, cookies map[string]*http.Cookie, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://radar.test")
	for name, cookie := range cookies {
		if name == "radar_admin_session" || name == "radar_csrf" {
			req.AddCookie(cookie)
		}
	}
	if csrf {
		if cookie, found := cookies["radar_csrf"]; found {
			req.Header.Set("X-CSRF-Token", cookie.Value)
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestAdminCollectionEndpointsRequireSessionAndCSRF(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	credentialsService := &fakeCredentials{}
	schedulerService := &fakeScheduler{}
	handler := newTestHandler(t, store, func(cfg *Config) {
		cfg.Credentials = credentialsService
		cfg.Scheduler = schedulerService
	})

	recorder := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/credentials", "", nil, false)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", recorder.Code)
	}

	login := loginRequest(handler, "http://radar.test", "192.0.2.30:1000", `{"username":"radar","password":"`+testPassword+`"}`, "application/json")
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d; body=%q", login.Code, login.Body.String())
	}
	cookies := responseCookies(login)

	missingCSRF := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/credentials",
		`{"label":"primary","token":"github_pat_token_value"}`, cookies, false)
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d; body=%q", missingCSRF.Code, missingCSRF.Body.String())
	}
	if len(credentialsService.created) != 0 {
		t.Fatal("credential created without CSRF")
	}

	list := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/credentials", "", cookies, false)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"token_hint":"…1234"`) {
		t.Fatalf("list status = %d; body=%q", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), "token_ciphertext") {
		t.Fatal("credential list leaked ciphertext")
	}
}

func TestAdminCreateCredentialReturnsCreatedWithoutSecret(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	credentialsService := &fakeCredentials{}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Credentials = credentialsService })
	login := loginRequest(handler, "http://radar.test", "192.0.2.31:1000", `{"username":"radar","password":"`+testPassword+`"}`, "application/json")
	cookies := responseCookies(login)

	const token = "github_pat_super_secret_value"
	recorder := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/credentials",
		`{"label":"primary","token":"`+token+`","priority":100}`, cookies, true)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), token) {
		t.Fatal("create response leaked the token")
	}
	if len(credentialsService.created) != 1 || credentialsService.created[0].Priority != 100 {
		t.Fatalf("created inputs = %+v", credentialsService.created)
	}
	var payload struct {
		Data struct {
			Label string `json:"label"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Label != "primary" {
		t.Errorf("payload = %+v", payload)
	}

	invalid := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/credentials",
		`{"label":"primary","token":"x"}`, cookies, true)
	if invalid.Code != http.StatusBadRequest {
		t.Errorf("invalid token status = %d", invalid.Code)
	}
}

func TestAdminJobRunAcceptsAndHistoryIsReadable(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	schedulerService := &fakeScheduler{}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Scheduler = schedulerService })
	login := loginRequest(handler, "http://radar.test", "192.0.2.32:1000", `{"username":"radar","password":"`+testPassword+`"}`, "application/json")
	cookies := responseCookies(login)

	run := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/jobs/run", "", cookies, true)
	if run.Code != http.StatusAccepted {
		t.Fatalf("run status = %d; body=%q", run.Code, run.Body.String())
	}
	runs := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/job-runs", "", cookies, false)
	if runs.Code != http.StatusOK || !strings.Contains(runs.Body.String(), `"status":"completed"`) {
		t.Fatalf("runs status = %d; body=%q", runs.Code, runs.Body.String())
	}
	detail := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/job-runs/9", "", cookies, false)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "collection_failed") {
		t.Fatalf("detail status = %d; body=%q", detail.Code, detail.Body.String())
	}
	badID := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/job-runs/abc", "", cookies, false)
	if badID.Code != http.StatusBadRequest {
		t.Fatalf("bad id status = %d", badID.Code)
	}
}

func TestAdminJobRunReportsMissingCredential(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	schedulerService := &fakeScheduler{precheckErr: scheduler.ErrNoCredential}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Scheduler = schedulerService })
	login := loginRequest(handler, "http://radar.test", "192.0.2.33:1000", `{"username":"radar","password":"`+testPassword+`"}`, "application/json")
	cookies := responseCookies(login)

	recorder := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/jobs/run", "", cookies, true)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "CREDENTIAL_REQUIRED") {
		t.Errorf("body = %q", recorder.Body.String())
	}
	if schedulerService.runs != 0 {
		t.Errorf("scheduler started %d runs despite a missing credential", schedulerService.runs)
	}
}
