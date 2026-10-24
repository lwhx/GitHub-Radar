package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/backups"
)

type fakeBackups struct {
	items       []backups.Backup
	filePath    string
	createErr   error
	verifyErr   error
	createCalls int
	verifyCalls int
}

func (fake *fakeBackups) Create(context.Context, backups.Kind) (backups.Backup, error) {
	fake.createCalls++
	if fake.createErr != nil {
		return backups.Backup{}, fake.createErr
	}
	return fake.items[0], nil
}
func (fake *fakeBackups) List(context.Context) ([]backups.Backup, error) { return fake.items, nil }
func (fake *fakeBackups) Open(name string) (string, error) {
	if strings.Contains(name, "..") || !strings.HasPrefix(name, "radar-") {
		return "", backups.ErrInvalidName
	}
	if fake.filePath == "" {
		return "", backups.ErrNotFound
	}
	return fake.filePath, nil
}
func (fake *fakeBackups) Verify(context.Context, string) (bool, error) {
	fake.verifyCalls++
	if fake.verifyErr != nil {
		return false, fake.verifyErr
	}
	return true, nil
}

func backupFixture(t *testing.T) (*fakeBackups, backups.Backup) {
	t.Helper()
	name := "radar-manual-20261024T120000.000000000Z.db"
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("sqlite-backup"), 0o640); err != nil {
		t.Fatal(err)
	}
	item := backups.Backup{Name: name, Kind: backups.KindManual, CreatedAt: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), SizeBytes: 13, SHA256: strings.Repeat("a", 64), Verified: true}
	return &fakeBackups{items: []backups.Backup{item}, filePath: path}, item
}

func TestAdminBackupsRequireAuthenticationAndListMetadata(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	fake, item := backupFixture(t)
	handler := newTestHandler(t, store, func(config *Config) { config.Backups = fake })
	anonymous := request(t, handler, http.MethodGet, "/api/v1/admin/backups")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", anonymous.Code)
	}
	cookies := adminCookiesForLifecycle(t, handler, store)
	response := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/backups", "", cookies, false)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Data struct {
			Items     []backups.Backup `json:"items"`
			Retention map[string]int   `json:"retention"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Items) != 1 || payload.Data.Items[0].Name != item.Name || payload.Data.Items[0].DownloadURL == "" {
		t.Fatalf("payload = %+v", payload.Data)
	}
	if payload.Data.Retention["daily"] != 7 || payload.Data.Retention["weekly"] != 4 {
		t.Fatalf("retention = %+v", payload.Data.Retention)
	}
}

func TestAdminBackupCreateRequiresCSRFAndAudits(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	fake, _ := backupFixture(t)
	handler := newTestHandler(t, store, func(config *Config) { config.Backups = fake })
	cookies := adminCookiesForLifecycle(t, handler, store)

	noCSRF := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/backups", "", cookies, false)
	if noCSRF.Code != http.StatusForbidden || fake.createCalls != 0 {
		t.Fatalf("no CSRF status=%d calls=%d", noCSRF.Code, fake.createCalls)
	}
	created := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/backups", "", cookies, true)
	if created.Code != http.StatusCreated || fake.createCalls != 1 {
		t.Fatalf("create status=%d calls=%d body=%s", created.Code, fake.createCalls, created.Body.String())
	}
	var audits int
	if err := store.SQL().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'admin.backup.create' AND result = 'success'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("audit count = %d", audits)
	}

	fake.createErr = errors.New("disk full")
	failed := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/backups", "", cookies, true)
	if failed.Code != http.StatusInternalServerError || !strings.Contains(failed.Body.String(), "BACKUP_FAILED") {
		t.Fatalf("failed status=%d body=%s", failed.Code, failed.Body.String())
	}
}

func TestAdminBackupDownloadAndVerify(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	fake, item := backupFixture(t)
	handler := newTestHandler(t, store, func(config *Config) { config.Backups = fake })
	cookies := adminCookiesForLifecycle(t, handler, store)

	download := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/backups/"+item.Name, "", cookies, false)
	if download.Code != http.StatusOK || download.Body.String() != "sqlite-backup" {
		t.Fatalf("download status=%d body=%q", download.Code, download.Body.String())
	}
	if !strings.Contains(download.Header().Get("Content-Disposition"), item.Name) || download.Header().Get("Content-Type") != "application/vnd.sqlite3" {
		t.Fatalf("download headers = %+v", download.Header())
	}

	verified := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/backups/"+item.Name+"/verify", "", cookies, true)
	if verified.Code != http.StatusOK || fake.verifyCalls != 1 || !strings.Contains(verified.Body.String(), `"verified":true`) {
		t.Fatalf("verify status=%d calls=%d body=%s", verified.Code, fake.verifyCalls, verified.Body.String())
	}

	invalid := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/backups/not-a-backup", "", cookies, false)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}
