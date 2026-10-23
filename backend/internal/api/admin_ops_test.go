package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/github-radar/github-radar/internal/settings"
)

type fakeSettings struct {
	snapshot  settings.Snapshot
	update    settings.Update
	updateErr error
	calls     int
}

func (fake *fakeSettings) Get(context.Context) (settings.Snapshot, error) {
	return fake.snapshot, nil
}

func (fake *fakeSettings) Update(_ context.Context, update settings.Update) (settings.Snapshot, error) {
	fake.calls++
	fake.update = update
	if fake.updateErr != nil {
		return settings.Snapshot{}, fake.updateErr
	}
	return fake.snapshot, nil
}

func TestAdminSettingsRequiresSession(t *testing.T) {
	store := newTestStore(t)
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Settings = &fakeSettings{} })

	recorder := request(t, handler, http.MethodGet, "/api/v1/admin/settings")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", recorder.Code)
	}
}

func TestAdminSettingsGetReturnsMaskedSnapshot(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	fake := &fakeSettings{snapshot: settings.Snapshot{
		SiteName: "雷达站", Timezone: "Asia/Shanghai", DefaultPageSize: 30,
		AIEnabled: true, AIModel: "glm-4", HasAIAPIKey: true,
	}}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Settings = fake })
	cookies := adminCookiesForLifecycle(t, handler, store)

	recorder := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/settings", "", cookies, false)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data["site_name"] != "雷达站" || payload.Data["has_ai_api_key"] != true {
		t.Fatalf("payload = %+v", payload.Data)
	}
	if _, exists := payload.Data["ai_api_key"]; exists {
		t.Fatal("ai_api_key must never be returned")
	}
}

func TestAdminSettingsUpdateValidatesAndAudits(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	timezone := "Asia/Shanghai"
	fake := &fakeSettings{snapshot: settings.Snapshot{SiteName: "GitHub Radar", Timezone: timezone, DefaultPageSize: 20}}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Settings = fake })
	cookies := adminCookiesForLifecycle(t, handler, store)

	recorder := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/settings",
		`{"timezone":"Asia/Shanghai","default_page_size":50}`, cookies, true)
	if recorder.Code != http.StatusOK {
		t.Fatalf("patch status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	if fake.calls != 1 || fake.update.Timezone == nil || *fake.update.Timezone != timezone {
		t.Fatalf("update = %+v calls=%d", fake.update, fake.calls)
	}
	var count int
	if err := store.SQL().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'admin.settings.update' AND result = 'success'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audit rows = %d, want 1", count)
	}

	// 无 CSRF 拒绝。
	noCSRF := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/settings", `{"site_name":"x"}`, cookies, false)
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("no CSRF status = %d", noCSRF.Code)
	}
	// 空载荷拒绝。
	empty := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/settings", `{}`, cookies, true)
	if empty.Code != http.StatusBadRequest || !strings.Contains(empty.Body.String(), "INVALID_REQUEST") {
		t.Fatalf("empty status = %d; body=%s", empty.Code, empty.Body.String())
	}
	// 非法时区 → 400，且写失败审计。
	fake.updateErr = settings.ErrInvalidSettings
	invalid := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/settings", `{"timezone":"Mars/Olympus"}`, cookies, true)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "INVALID_SETTINGS") {
		t.Fatalf("invalid status = %d; body=%s", invalid.Code, invalid.Body.String())
	}
	var failures int
	if err := store.SQL().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'admin.settings.update' AND result = 'failure'`).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 1 {
		t.Fatalf("failure audit rows = %d, want 1", failures)
	}
}

func TestAdminAuditLogsListsLatestFirstWithFilters(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	handler := newTestHandler(t, store)
	cookies := adminCookiesForLifecycle(t, handler, store)

	seedAudit := func(action, target, result, createdAt string) {
		t.Helper()
		if _, err := store.SQL().Exec(`INSERT INTO audit_logs (admin_id, action, target, result, created_at, request_id)
			SELECT id, ?, ?, ?, ?, 'req-1' FROM admin_users WHERE username = 'radar'`, action, target, result, createdAt); err != nil {
			t.Fatal(err)
		}
	}
	seedAudit("admin.category.create", "ai", "success", "2026-10-24T08:00:00Z")
	seedAudit("admin.credential.delete", "3", "success", "2026-10-24T09:00:00Z")
	seedAudit("admin.category.create", "devops", "failure", "2026-10-24T10:00:00Z")

	recorder := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/audit-logs?limit=2", "", cookies, false)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Items []struct {
				Action  string `json:"action"`
				Result  string `json:"result"`
				AdminID *int64 `json:"admin_id"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Items) != 2 {
		t.Fatalf("items = %+v", payload.Data.Items)
	}
	if payload.Data.Items[0].Action != "admin.category.create" || payload.Data.Items[0].Result != "failure" {
		t.Fatalf("first item = %+v", payload.Data.Items[0])
	}
	if payload.Data.Items[0].AdminID == nil || *payload.Data.Items[0].AdminID == 0 {
		t.Fatalf("admin_id missing: %+v", payload.Data.Items[0])
	}

	filtered := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/audit-logs?action=admin.credential.delete", "", cookies, false)
	if filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), "admin.credential.delete") {
		t.Fatalf("filtered status = %d; body=%s", filtered.Code, filtered.Body.String())
	}
	if strings.Contains(filtered.Body.String(), "admin.category.create") {
		t.Fatal("action filter must exclude other actions")
	}

	badLimit := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/audit-logs?limit=500", "", cookies, false)
	if badLimit.Code != http.StatusBadRequest || !strings.Contains(badLimit.Body.String(), "INVALID_LIMIT") {
		t.Fatalf("bad limit status = %d", badLimit.Code)
	}

	anonymous := request(t, handler, http.MethodGet, "/api/v1/admin/audit-logs")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", anonymous.Code)
	}

}
