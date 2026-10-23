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

type mutableGitHubClient struct {
	client fakeGitHubClient
}

func (holder *mutableGitHubClient) factory(string) (githubClient, error) {
	return holder.client, nil
}

func newCredentialServiceForTest(t *testing.T, now time.Time) (*Service, *mutableGitHubClient, func() time.Time) {
	t.Helper()
	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	cipher, err := NewCipher(bytes.Repeat([]byte{0x24}, 32), bytes.NewReader(bytes.Repeat([]byte{0x02}, 12)))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	holder := &mutableGitHubClient{client: fakeGitHubClient{
		user: github.AuthenticatedUser{ID: 7, Login: "octocat", Type: "User"},
		limits: github.RateLimitStatus{Resources: github.RateLimitResources{
			Core:   github.RateLimitResource{Limit: 5000, Used: 10, Remaining: 4990, Reset: now.Add(time.Hour).Unix()},
			Search: github.RateLimitResource{Limit: 30, Used: 2, Remaining: 28, Reset: now.Add(time.Minute).Unix()},
		}},
	}}
	clock := now
	service := newService(store.SQL(), cipher, holder.factory, func() time.Time { return clock })
	return service, holder, func() time.Time { return clock }
}

func TestServiceTestRefreshesIdentityAndRateLimits(t *testing.T) {
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	service, holder, _ := newCredentialServiceForTest(t, now)
	ctx := context.Background()
	created, err := service.Create(ctx, CreateInput{Label: "Primary", Token: "github_pat_live_secret_1234"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// 模拟 Token 换了身份、额度回升
	holder.client = fakeGitHubClient{
		user: github.AuthenticatedUser{ID: 8, Login: "hubot", Type: "User"},
		limits: github.RateLimitStatus{Resources: github.RateLimitResources{
			Core:   github.RateLimitResource{Limit: 5000, Used: 1, Remaining: 4999, Reset: now.Add(2 * time.Hour).Unix()},
			Search: github.RateLimitResource{Limit: 30, Used: 0, Remaining: 30, Reset: now.Add(2 * time.Minute).Unix()},
		}},
	}

	refreshed, err := service.Test(ctx, created.ID)
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if refreshed.AuthLogin != "hubot" || refreshed.AuthIdentity != "8" || refreshed.Status != "active" {
		t.Fatalf("refreshed = %+v, want hubot/8/active", refreshed)
	}
	details, err := service.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(details) != 1 || details[0].AuthLogin != "hubot" {
		t.Fatalf("details = %+v, want persisted hubot", details)
	}
	for _, limit := range details[0].RateLimits {
		if limit.Resource == "core" && limit.Remaining != 4999 {
			t.Fatalf("core remaining = %d, want refreshed 4999", limit.Remaining)
		}
	}
}

func TestServiceTestReturnsNotFoundForUnknownID(t *testing.T) {
	service, _, _ := newCredentialServiceForTest(t, time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC))
	if _, err := service.Test(context.Background(), 4242); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Test() error = %v, want ErrNotFound", err)
	}
}

func TestServiceDeleteRemovesCredentialAndCascadesRateLimits(t *testing.T) {
	service, _, _ := newCredentialServiceForTest(t, time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC))
	ctx := context.Background()
	created, err := service.Create(ctx, CreateInput{Label: "Temp", Token: "github_pat_live_secret_5678"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	details, err := service.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(details) != 0 {
		t.Fatalf("details = %d, want empty after delete", len(details))
	}
	if _, err := service.Usable(ctx, "core"); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("Usable() error = %v, want ErrNoCredential after delete", err)
	}
}

func TestServiceDeleteReturnsNotFoundForUnknownID(t *testing.T) {
	service, _, _ := newCredentialServiceForTest(t, time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC))
	if err := service.Delete(context.Background(), 4242); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() error = %v, want ErrNotFound", err)
	}
}
