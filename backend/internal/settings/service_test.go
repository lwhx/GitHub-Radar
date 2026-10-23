package settings

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/credentials"
	appdb "github.com/github-radar/github-radar/internal/db"
)

func newServiceForTest(t *testing.T) (*Service, *credentials.Cipher) {
	t.Helper()
	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	cipher, err := credentials.NewCipher([]byte("0123456789abcdef0123456789abcdef"), nil)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	return NewService(store.SQL(), cipher, func() time.Time {
		return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	}), cipher
}

func TestGetReturnsDefaultsWhenEmpty(t *testing.T) {
	service, _ := newServiceForTest(t)

	snapshot, err := service.Get(context.Background())
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if snapshot.SiteName != "GitHub Radar" || snapshot.Timezone != "UTC" || snapshot.DefaultPageSize != 20 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.AIEnabled || snapshot.HasAIAPIKey {
		t.Fatalf("AI should be off by default: %+v", snapshot)
	}
}

func TestUpdatePersistsAndMasksAPIKey(t *testing.T) {
	service, cipher := newServiceForTest(t)
	ctx := context.Background()

	snapshot, err := service.Update(ctx, Update{
		SiteName:        strPtr("雷达站"),
		Timezone:        strPtr("Asia/Shanghai"),
		DefaultPageSize: intPtr(50),
		AIEnabled:       boolPtr(true),
		AIModel:         strPtr("glm-4"),
		AIAPIKey:        strPtr("sk-secret-123"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if snapshot.SiteName != "雷达站" || snapshot.Timezone != "Asia/Shanghai" || snapshot.DefaultPageSize != 50 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if !snapshot.AIEnabled || !snapshot.HasAIAPIKey {
		t.Fatalf("AI flags wrong: %+v", snapshot)
	}

	// 密文必须落库，明文绝不允许出现。
	var stored string
	if err := service.db.QueryRow(`SELECT json_value FROM site_settings WHERE key = 'ai_api_key'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "" || stored == "sk-secret-123" {
		t.Fatalf("api key must be stored encrypted, got %q", stored)
	}
	decrypted, err := cipher.Decrypt(stored)
	if err != nil || decrypted != "sk-secret-123" {
		t.Fatalf("decrypt roundtrip failed: %q %v", decrypted, err)
	}

	// 再次读取：Key 不可回传。
	reread, err := service.Get(ctx)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !reread.HasAIAPIKey {
		t.Fatal("has_ai_api_key should be true")
	}
}

func TestUpdateKeepsKeyWhenOmittedAndClearsOnEmpty(t *testing.T) {
	service, _ := newServiceForTest(t)
	ctx := context.Background()
	if _, err := service.Update(ctx, Update{AIAPIKey: strPtr("sk-secret-123")}); err != nil {
		t.Fatalf("seed key error = %v", err)
	}
	if _, err := service.Update(ctx, Update{SiteName: strPtr("改名")}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	snapshot, err := service.Get(ctx)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !snapshot.HasAIAPIKey {
		t.Fatal("omitting ai_api_key must keep the stored key")
	}

	if _, err := service.Update(ctx, Update{AIAPIKey: strPtr("")}); err != nil {
		t.Fatalf("clear key error = %v", err)
	}
	snapshot, err = service.Get(ctx)
	if err != nil {
		t.Fatalf("Get() after clear error = %v", err)
	}
	if snapshot.HasAIAPIKey {
		t.Fatal("empty ai_api_key must clear the stored key")
	}
}

func TestUpdateRejectsInvalidValues(t *testing.T) {
	service, _ := newServiceForTest(t)
	ctx := context.Background()

	if _, err := service.Update(ctx, Update{Timezone: strPtr("Mars/Olympus")}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("timezone error = %v, want ErrInvalidSettings", err)
	}
	if _, err := service.Update(ctx, Update{DefaultPageSize: intPtr(500)}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("page size error = %v, want ErrInvalidSettings", err)
	}
	if _, err := service.Update(ctx, Update{SiteName: strPtr("")}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("empty site name error = %v, want ErrInvalidSettings", err)
	}
	if _, err := service.Update(ctx, Update{AIBaseURL: strPtr("ftp://x")}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("bad base url error = %v, want ErrInvalidSettings", err)
	}
}

func strPtr(value string) *string { return &value }
func intPtr(value int) *int       { return &value }
func boolPtr(value bool) *bool    { return &value }
