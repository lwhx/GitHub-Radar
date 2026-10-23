package api

import (
	"net/http"
	"strings"
	"testing"
)

func adminCookiesForLifecycle(t *testing.T, handler http.Handler, store interface{}) map[string]*http.Cookie {
	t.Helper()
	login := loginRequest(handler, "http://radar.test", "192.0.2.61:1000", `{"username":"radar","password":"`+testPassword+`"}`, "application/json")
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d; body=%q", login.Code, login.Body.String())
	}
	return responseCookies(login)
}

func TestAdminTestCredentialRefreshesIdentity(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	credentialsService := &fakeCredentials{}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Credentials = credentialsService })
	cookies := adminCookiesForLifecycle(t, handler, store)

	recorder := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/credentials/7/test", "", cookies, true)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"auth_login":"hubot"`) {
		t.Fatalf("body = %q, want refreshed identity", recorder.Body.String())
	}
	if len(credentialsService.tested) != 1 || credentialsService.tested[0] != 7 {
		t.Fatalf("tested = %v, want [7]", credentialsService.tested)
	}
	if strings.Contains(recorder.Body.String(), "token_ciphertext") {
		t.Fatal("test response leaked ciphertext")
	}
}

func TestAdminTestCredentialRequiresCSRF(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	credentialsService := &fakeCredentials{}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Credentials = credentialsService })
	cookies := adminCookiesForLifecycle(t, handler, store)

	recorder := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/credentials/7/test", "", cookies, false)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	if len(credentialsService.tested) != 0 {
		t.Fatal("credential re-checked without CSRF")
	}
}

func TestAdminDeleteCredentialRemovesIt(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	credentialsService := &fakeCredentials{}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Credentials = credentialsService })
	cookies := adminCookiesForLifecycle(t, handler, store)

	recorder := adminRequest(t, handler, http.MethodDelete, "http://radar.test/api/v1/admin/credentials/7", "", cookies, true)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	if len(credentialsService.deleted) != 1 || credentialsService.deleted[0] != 7 {
		t.Fatalf("deleted = %v, want [7]", credentialsService.deleted)
	}
	if !strings.Contains(recorder.Body.String(), `"deleted":true`) {
		t.Fatalf("body = %q, want deleted confirmation", recorder.Body.String())
	}
}

func TestAdminCredentialLifecycleReturnsNotFound(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	credentialsService := &fakeCredentials{missingIDs: map[int64]bool{7: true}}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Credentials = credentialsService })
	cookies := adminCookiesForLifecycle(t, handler, store)

	testRecorder := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/credentials/7/test", "", cookies, true)
	if testRecorder.Code != http.StatusNotFound || !strings.Contains(testRecorder.Body.String(), "NOT_FOUND") {
		t.Fatalf("test status = %d; body=%q", testRecorder.Code, testRecorder.Body.String())
	}
	deleteRecorder := adminRequest(t, handler, http.MethodDelete, "http://radar.test/api/v1/admin/credentials/7", "", cookies, true)
	if deleteRecorder.Code != http.StatusNotFound || !strings.Contains(deleteRecorder.Body.String(), "NOT_FOUND") {
		t.Fatalf("delete status = %d; body=%q", deleteRecorder.Code, deleteRecorder.Body.String())
	}
}
