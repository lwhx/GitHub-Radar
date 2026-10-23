package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/auth"
	"github.com/github-radar/github-radar/internal/db"
)

const testPassword = "correct horse battery staple"

type failingLogoutAuth struct{ authService }

func (failingLogoutAuth) Logout(context.Context, string) error {
	return errors.New("forced logout failure")
}

func createAdmin(t *testing.T, store *db.DB, username string) {
	t.Helper()
	if _, err := auth.NewService(store.SQL()).CreateAdmin(context.Background(), username, testPassword); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
}

func loginRequest(handler http.Handler, origin, remoteAddr, body, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://radar.test/api/v1/admin/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func responseCookies(recorder *httptest.ResponseRecorder) map[string]*http.Cookie {
	result := map[string]*http.Cookie{}
	for _, cookie := range recorder.Result().Cookies() {
		result[cookie.Name] = cookie
	}
	return result
}

func TestLoginValidatesOriginContentAndJSONBoundary(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	handler := newTestHandler(t, store)
	valid := `{"username":"radar","password":"` + testPassword + `"}`

	tests := []struct {
		name, origin, body, contentType string
		wantStatus                      int
	}{
		{name: "missing origin", body: valid, contentType: "application/json", wantStatus: http.StatusForbidden},
		{name: "null origin", origin: "null", body: valid, contentType: "application/json", wantStatus: http.StatusForbidden},
		{name: "cross origin", origin: "https://evil.test", body: valid, contentType: "application/json", wantStatus: http.StatusForbidden},
		{name: "malformed origin", origin: "://", body: valid, contentType: "application/json", wantStatus: http.StatusForbidden},
		{name: "wrong content type", origin: "http://radar.test", body: valid, contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType},
		{name: "unknown field", origin: "http://radar.test", body: `{"username":"radar","password":"` + testPassword + `","extra":true}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "trailing json", origin: "http://radar.test", body: valid + `{}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "long username", origin: "http://radar.test", body: `{"username":"` + strings.Repeat("a", 65) + `","password":"` + testPassword + `"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "body too large", origin: "http://radar.test", body: `{"username":"radar","password":"` + strings.Repeat("x", 17*1024) + `"}`, contentType: "application/json", wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := loginRequest(handler, test.origin, "192.0.2.1:1234", test.body, test.contentType)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%q", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestLoginSetsSecureCookiesWithoutReturningSessionToken(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.CookieSecure = true })
	recorder := loginRequest(handler, "http://radar.test", "192.0.2.2:1234", `{"username":"radar","password":"`+testPassword+`"}`, "application/json; charset=utf-8")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	cookies := responseCookies(recorder)
	sessionCookie, csrfCookie := cookies["radar_admin_session"], cookies["radar_csrf"]
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("cookies = %#v", cookies)
	}
	if !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteLaxMode || sessionCookie.Path != "/" || sessionCookie.MaxAge <= 0 || sessionCookie.Expires.IsZero() {
		t.Fatalf("session cookie flags = %#v", sessionCookie)
	}
	if csrfCookie.HttpOnly || !csrfCookie.Secure || csrfCookie.SameSite != http.SameSiteLaxMode || csrfCookie.Path != "/" || csrfCookie.MaxAge <= 0 {
		t.Fatalf("csrf cookie flags = %#v", csrfCookie)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte(sessionCookie.Value)) || bytes.Contains(recorder.Body.Bytes(), []byte("session_token")) {
		t.Fatalf("body exposes session token: %q", recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Admin struct {
				Username string `json:"username"`
			} `json:"admin"`
			CSRFToken string `json:"csrf_token"`
			ExpiresAt string `json:"expires_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Admin.Username != "radar" || payload.Data.CSRFToken != csrfCookie.Value || payload.Data.ExpiresAt == "" {
		t.Fatalf("response = %+v", payload)
	}
}

func TestRejectedLoginOriginIsAudited(t *testing.T) {
	store := newTestStore(t)
	handler := newTestHandler(t, store)
	recorder := loginRequest(handler, "https://evil.test", "192.0.2.3:1234", `{"username":"radar","password":"`+testPassword+`"}`, "application/json")
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	var action, result, requestID string
	if err := store.SQL().QueryRow("SELECT action, result, request_id FROM audit_logs").Scan(&action, &result, &requestID); err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if action != "admin.login" || result != "failure" || requestID == "" {
		t.Fatalf("audit = action %q result %q request_id %q", action, result, requestID)
	}
}

func TestConstantTimeTokenEqual(t *testing.T) {
	if !constantTimeTokenEqual("same", "same") {
		t.Fatal("equal tokens did not match")
	}
	if constantTimeTokenEqual("short", "a-different-length-token") {
		t.Fatal("different tokens matched")
	}
}

func TestLoginRateLimitUsesRemoteIPAndSuccessClearsFailures(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	handler := newTestHandler(t, store)
	bad := `{"username":"radar","password":"wrong password value"}`
	for attempt := 1; attempt <= 5; attempt++ {
		recorder := loginRequest(handler, "http://radar.test", "192.0.2.10:1000", bad, "application/json")
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d", attempt, recorder.Code)
		}
	}
	limited := loginRequest(handler, "http://radar.test", "192.0.2.10:2000", bad, "application/json")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("sixth status = %d, want 429", limited.Code)
	}
	other := loginRequest(handler, "http://radar.test", "192.0.2.11:1000", bad, "application/json")
	if other.Code != http.StatusUnauthorized {
		t.Fatalf("other IP status = %d", other.Code)
	}

	handler = newTestHandler(t, store)
	for attempt := 0; attempt < 4; attempt++ {
		_ = loginRequest(handler, "http://radar.test", "192.0.2.12:1000", bad, "application/json")
	}
	success := loginRequest(handler, "http://radar.test", "192.0.2.12:1000", `{"username":"radar","password":"`+testPassword+`"}`, "application/json")
	if success.Code != http.StatusOK {
		t.Fatalf("success status = %d; body=%q", success.Code, success.Body.String())
	}
	for attempt := 0; attempt < 5; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "http://radar.test/api/v1/admin/auth/login", strings.NewReader(bad))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://radar.test")
		req.Header.Set("X-Forwarded-For", "192.0.2.200")
		req.RemoteAddr = "192.0.2.12:2000"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("post-reset attempt %d status = %d", attempt+1, recorder.Code)
		}
	}
}

func TestLoginLimiterUsesFixedFiveMinuteWindow(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	limiter := newLoginLimiter(func() time.Time { return now })
	limiter.fail("192.0.2.50")
	for attempt := 0; attempt < 4; attempt++ {
		now = now.Add(time.Minute)
		limiter.fail("192.0.2.50")
	}
	if !limiter.limited("192.0.2.50") {
		t.Fatal("fifth failure inside the window was not limited")
	}
	now = time.Date(2026, 10, 23, 12, 5, 1, 0, time.UTC)
	if limiter.limited("192.0.2.50") {
		t.Fatal("failure window was extended by later failures")
	}
}

func TestSessionAndLogoutEnforceSessionOriginAndDoubleSubmitCSRF(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	handler := newTestHandler(t, store)
	login := loginRequest(handler, "http://radar.test", "192.0.2.20:1000", `{"username":"radar","password":"`+testPassword+`"}`, "application/json")
	cookies := responseCookies(login)

	sessionReq := httptest.NewRequest(http.MethodGet, "http://radar.test/api/v1/admin/auth/session", nil)
	sessionReq.AddCookie(cookies["radar_admin_session"])
	sessionRecorder := httptest.NewRecorder()
	handler.ServeHTTP(sessionRecorder, sessionReq)
	if sessionRecorder.Code != http.StatusOK || !strings.Contains(sessionRecorder.Body.String(), `"username":"radar"`) {
		t.Fatalf("session response = %d %q", sessionRecorder.Code, sessionRecorder.Body.String())
	}

	cases := []struct {
		name, origin, token string
		addSession, addCSRF bool
		want                int
	}{
		{name: "missing origin", addSession: true, addCSRF: true, token: cookies["radar_csrf"].Value, want: http.StatusForbidden},
		{name: "missing session", origin: "http://radar.test", addCSRF: true, token: cookies["radar_csrf"].Value, want: http.StatusUnauthorized},
		{name: "missing csrf cookie", origin: "http://radar.test", addSession: true, token: cookies["radar_csrf"].Value, want: http.StatusForbidden},
		{name: "header mismatch", origin: "http://radar.test", addSession: true, addCSRF: true, token: "mismatch", want: http.StatusForbidden},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://radar.test/api/v1/admin/auth/logout", nil)
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			if test.token != "" {
				req.Header.Set("X-CSRF-Token", test.token)
			}
			if test.addSession {
				req.AddCookie(cookies["radar_admin_session"])
			}
			if test.addCSRF {
				req.AddCookie(cookies["radar_csrf"])
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%q", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "http://radar.test/api/v1/admin/auth/logout", nil)
	logoutReq.Header.Set("Origin", "http://radar.test")
	logoutReq.Header.Set("X-CSRF-Token", cookies["radar_csrf"].Value)
	logoutReq.AddCookie(cookies["radar_admin_session"])
	logoutReq.AddCookie(cookies["radar_csrf"])
	logoutRecorder := httptest.NewRecorder()
	handler.ServeHTTP(logoutRecorder, logoutReq)
	if logoutRecorder.Code != http.StatusNoContent {
		t.Fatalf("logout = %d %q", logoutRecorder.Code, logoutRecorder.Body.String())
	}
	for _, cookie := range logoutRecorder.Result().Cookies() {
		if (cookie.Name == "radar_admin_session" || cookie.Name == "radar_csrf") && (cookie.MaxAge != -1 || cookie.Expires.Unix() > 0) {
			t.Fatalf("cookie not cleared: %#v", cookie)
		}
	}
	revokedReq := httptest.NewRequest(http.MethodGet, "http://radar.test/api/v1/admin/auth/session", nil)
	revokedReq.AddCookie(cookies["radar_admin_session"])
	revokedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(revokedRecorder, revokedReq)
	if revokedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session status = %d", revokedRecorder.Code)
	}
}

func TestSessionRejectsExpiredCookie(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	handler := newTestHandler(t, store)
	login := loginRequest(handler, "http://radar.test", "192.0.2.21:1000", `{"username":"radar","password":"`+testPassword+`"}`, "application/json")
	cookie := responseCookies(login)["radar_admin_session"]
	if cookie == nil {
		t.Fatalf("login did not set session cookie: %d %q", login.Code, login.Body.String())
	}
	if _, err := store.SQL().Exec("UPDATE admin_sessions SET expires_at = '2000-01-01T00:00:00Z'"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://radar.test/api/v1/admin/auth/session", nil)
	req.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expired session status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestAuthAuditLogsContainNoSecrets(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	handler := newTestHandler(t, store)
	badSecret := "not-the-password-secret"
	_ = loginRequest(handler, "http://radar.test", "192.0.2.30:1000", `{"username":"`+badSecret+`","password":"`+badSecret+`"}`, "application/json")
	login := loginRequest(handler, "http://radar.test", "192.0.2.30:1000", `{"username":"radar","password":"`+testPassword+`"}`, "application/json")
	cookies := responseCookies(login)
	logoutReq := httptest.NewRequest(http.MethodPost, "http://radar.test/api/v1/admin/auth/logout", nil)
	logoutReq.Header.Set("Origin", "http://radar.test")
	logoutReq.Header.Set("X-CSRF-Token", cookies["radar_csrf"].Value)
	logoutReq.AddCookie(cookies["radar_admin_session"])
	logoutReq.AddCookie(cookies["radar_csrf"])
	handler.ServeHTTP(httptest.NewRecorder(), logoutReq)

	rows, err := store.SQL().Query("SELECT action, target, result, created_at, request_id FROM audit_logs ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var joined strings.Builder
	count := 0
	for rows.Next() {
		var action, target, result, created, requestID string
		if err := rows.Scan(&action, &target, &result, &created, &requestID); err != nil {
			t.Fatal(err)
		}
		joined.WriteString(action + target + result + created + requestID)
		count++
	}
	if count != 3 {
		t.Fatalf("audit count = %d, want 3; data=%q", count, joined.String())
	}
	for _, secret := range []string{badSecret, testPassword, cookies["radar_admin_session"].Value, cookies["radar_csrf"].Value} {
		if strings.Contains(joined.String(), secret) {
			t.Fatalf("audit logs contain secret %q", secret)
		}
	}
}

func TestLoginRemainsSuccessfulWhenAuditWriteFails(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	if _, err := store.SQL().Exec("DROP TABLE audit_logs"); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	handler := newTestHandler(t, store, func(cfg *Config) {
		cfg.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	})
	recorder := loginRequest(handler, "http://radar.test", "192.0.2.40:1000", `{"username":"radar","password":"`+testPassword+`"}`, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	cookie := responseCookies(recorder)["radar_admin_session"]
	if cookie == nil {
		t.Fatal("successful login did not set a session cookie")
	}
	if _, err := auth.NewService(store.SQL()).Authenticate(context.Background(), cookie.Value); err != nil {
		t.Fatalf("session was not persisted: %v", err)
	}
	if !strings.Contains(logs.String(), "write audit log") {
		t.Fatalf("audit failure was not logged: %q", logs.String())
	}
}

func TestLogoutFailureIsAudited(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	realAuth := auth.NewService(store.SQL())
	handler := newTestHandler(t, store, func(cfg *Config) {
		cfg.Auth = failingLogoutAuth{authService: realAuth}
	})
	login := loginRequest(handler, "http://radar.test", "192.0.2.41:1000", `{"username":"radar","password":"`+testPassword+`"}`, "application/json")
	cookies := responseCookies(login)
	request := httptest.NewRequest(http.MethodPost, "http://radar.test/api/v1/admin/auth/logout", nil)
	request.Header.Set("Origin", "http://radar.test")
	request.Header.Set("X-CSRF-Token", cookies["radar_csrf"].Value)
	request.AddCookie(cookies["radar_admin_session"])
	request.AddCookie(cookies["radar_csrf"])
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	var result string
	if err := store.SQL().QueryRow("SELECT result FROM audit_logs WHERE action = 'admin.logout' ORDER BY id DESC LIMIT 1").Scan(&result); err != nil {
		t.Fatalf("read logout audit: %v", err)
	}
	if result != "failure" {
		t.Fatalf("logout audit result = %q", result)
	}
}
