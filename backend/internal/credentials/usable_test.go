package credentials

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/github-radar/github-radar/internal/db"
	"github.com/github-radar/github-radar/internal/github"
)

func newTestService(t *testing.T, user github.AuthenticatedUser) (*Service, *appdb.DB) {
	t.Helper()
	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cipher, err := NewCipher(bytes.Repeat([]byte{0x42}, 32), nil)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	now := time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)
	fake := fakeGitHubClient{
		user: user,
		limits: github.RateLimitStatus{Resources: github.RateLimitResources{
			Core:   github.RateLimitResource{Limit: 5000, Remaining: 5000, Reset: now.Add(time.Hour).Unix()},
			Search: github.RateLimitResource{Limit: 30, Remaining: 30, Reset: now.Add(time.Minute).Unix()},
		}},
	}
	service := newService(store.SQL(), cipher, func(string) (githubClient, error) { return fake, nil }, func() time.Time { return now })
	return service, store
}

func TestUsableCredentialDoesNotRotateTokensToBypassQuota(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t, github.AuthenticatedUser{ID: 7, Login: "octocat", Type: "User"})
	ctx := context.Background()
	first, err := service.Create(ctx, CreateInput{Label: "primary", Token: "github_pat_first_token", Priority: 100})
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	second, err := service.Create(ctx, CreateInput{Label: "secondary", Token: "github_pat_second_token", Priority: 50})
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}

	usable, err := service.Usable(ctx, "search")
	if err != nil {
		t.Fatalf("Usable() error = %v", err)
	}
	if usable.ID != first.ID {
		t.Errorf("usable credential = %d, want highest priority %d", usable.ID, first.ID)
	}

	cooldownUntil := service.now().Add(time.Hour)
	if err := service.RecordCooldown(ctx, first.ID, "search", cooldownUntil, "primary rate limit"); err != nil {
		t.Fatalf("RecordCooldown() error = %v", err)
	}

	_, err = service.Usable(ctx, "search")
	var rateLimited *RateLimitedError
	if !errors.As(err, &rateLimited) {
		t.Fatalf("Usable() after cooldown error = %T %v, want *RateLimitedError", err, err)
	}
	if !rateLimited.RetryAt.Equal(cooldownUntil) {
		t.Errorf("retry at = %v, want %v", rateLimited.RetryAt, cooldownUntil)
	}
	if rateLimited.CredentialID != first.ID && rateLimited.CredentialID != second.ID {
		t.Errorf("rate limited credential id = %d", rateLimited.CredentialID)
	}

	// The core bucket is tracked independently from the search bucket.
	usable, err = service.Usable(ctx, "core")
	if err != nil {
		t.Fatalf("Usable(core) error = %v", err)
	}
	if usable.ID != first.ID {
		t.Errorf("usable core credential = %d, want %d", usable.ID, first.ID)
	}
}

func TestUsableCredentialSkipsDisabledCredentials(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t, github.AuthenticatedUser{ID: 8, Login: "hubot", Type: "User"})
	ctx := context.Background()
	first, err := service.Create(ctx, CreateInput{Label: "primary", Token: "github_pat_disabled_token", Priority: 100})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	second, err := service.Create(ctx, CreateInput{Label: "backup", Token: "github_pat_backup_token", Priority: 10})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := service.SetEnabled(ctx, first.ID, false); err != nil {
		t.Fatalf("SetEnabled() error = %v", err)
	}
	usable, err := service.Usable(ctx, "search")
	if err != nil {
		t.Fatalf("Usable() error = %v", err)
	}
	if usable.ID != second.ID {
		t.Errorf("usable credential = %d, want %d", usable.ID, second.ID)
	}
	if usable.Token != "github_pat_backup_token" {
		t.Errorf("usable token = %q", usable.Token)
	}
}

func TestUsableCredentialReportsMissingCredential(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t, github.AuthenticatedUser{ID: 9, Login: "empty", Type: "User"})
	_, err := service.Usable(context.Background(), "search")
	if !errors.Is(err, ErrNoCredential) {
		t.Fatalf("Usable() error = %v, want ErrNoCredential", err)
	}
}
