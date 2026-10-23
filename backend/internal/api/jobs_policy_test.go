package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/github-radar/github-radar/internal/scheduler"
)

func TestAdminUpdateJobTogglesEnabled(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	schedulerService := &fakeScheduler{}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Scheduler = schedulerService })
	cookies := adminCookiesForLifecycle(t, handler, store)

	recorder := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/jobs/1", `{"enabled":false}`, cookies, true)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"enabled":false`) {
		t.Fatalf("body = %q, want disabled job", recorder.Body.String())
	}
	if len(schedulerService.policyCalls) != 1 || schedulerService.policyCalls[0].ID != 1 {
		t.Fatalf("policy calls = %+v", schedulerService.policyCalls)
	}
	call := schedulerService.policyCalls[0]
	if call.Enabled == nil || *call.Enabled {
		t.Fatalf("enabled = %v, want false", call.Enabled)
	}
}

func TestAdminUpdateJobAcceptsInterval(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	schedulerService := &fakeScheduler{}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Scheduler = schedulerService })
	cookies := adminCookiesForLifecycle(t, handler, store)

	recorder := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/jobs/1", `{"interval_seconds":7200}`, cookies, true)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"interval_seconds":7200`) {
		t.Fatalf("body = %q, want updated interval", recorder.Body.String())
	}
}

func TestAdminUpdateJobRejectsBadPayload(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	schedulerService := &fakeScheduler{}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Scheduler = schedulerService })
	cookies := adminCookiesForLifecycle(t, handler, store)

	empty := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/jobs/1", `{}`, cookies, true)
	if empty.Code != http.StatusBadRequest || !strings.Contains(empty.Body.String(), "INVALID_REQUEST") {
		t.Fatalf("empty status = %d; body=%q", empty.Code, empty.Body.String())
	}
	tiny := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/jobs/1", `{"interval_seconds":30}`, cookies, true)
	if tiny.Code != http.StatusBadRequest || !strings.Contains(tiny.Body.String(), "INVALID_INTERVAL") {
		t.Fatalf("tiny interval status = %d; body=%q", tiny.Code, tiny.Body.String())
	}
	if len(schedulerService.policyCalls) != 0 {
		t.Fatal("invalid payloads must not reach the scheduler")
	}
}

func TestAdminUpdateJobReturnsNotFoundAndRequiresCSRF(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	schedulerService := &fakeScheduler{policyErr: scheduler.ErrJobMissing}
	handler := newTestHandler(t, store, func(cfg *Config) { cfg.Scheduler = schedulerService })
	cookies := adminCookiesForLifecycle(t, handler, store)

	noCSRF := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/jobs/1", `{"enabled":true}`, cookies, false)
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("no CSRF status = %d", noCSRF.Code)
	}
	missing := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/jobs/99", `{"enabled":true}`, cookies, true)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "NOT_FOUND") {
		t.Fatalf("missing status = %d; body=%q", missing.Code, missing.Body.String())
	}
}
