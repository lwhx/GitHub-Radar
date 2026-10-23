package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appdb "github.com/github-radar/github-radar/internal/db"
	"github.com/github-radar/github-radar/internal/github"
)

type fakeGitHubClient struct {
	user   github.AuthenticatedUser
	limits github.RateLimitStatus
}

func (client fakeGitHubClient) GetAuthenticatedUser(context.Context) (github.AuthenticatedUser, github.ResponseMeta, error) {
	return client.user, github.ResponseMeta{}, nil
}

func (client fakeGitHubClient) GetRateLimitStatus(context.Context) (github.RateLimitStatus, github.ResponseMeta, error) {
	return client.limits, github.ResponseMeta{}, nil
}

func TestServiceCreateVerifiesAndEncryptsCredential(t *testing.T) {
	t.Parallel()

	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	defer store.Close()
	cipher, err := NewCipher(bytes.Repeat([]byte{0x42}, 32), bytes.NewReader(bytes.Repeat([]byte{0x01}, 12)))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	now := time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)
	fake := fakeGitHubClient{
		user: github.AuthenticatedUser{ID: 1, Login: "octocat", Type: "User"},
		limits: github.RateLimitStatus{Resources: github.RateLimitResources{
			Core:   github.RateLimitResource{Limit: 5000, Used: 3, Remaining: 4997, Reset: now.Add(time.Hour).Unix()},
			Search: github.RateLimitResource{Limit: 30, Used: 1, Remaining: 29, Reset: now.Add(time.Minute).Unix()},
		}},
	}
	service := newService(store.SQL(), cipher, func(string) (githubClient, error) { return fake, nil }, func() time.Time { return now })
	const token = "github_pat_live_secret_1234"
	credential, err := service.Create(context.Background(), CreateInput{Label: "Primary", Token: token, Priority: 100})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if credential.ID == 0 || credential.Label != "Primary" || credential.AuthIdentity != "1" || credential.AuthLogin != "octocat" || credential.TokenHint != "…1234" || credential.Status != "active" {
		t.Errorf("credential = %+v", credential)
	}

	var ciphertext string
	if err := store.SQL().QueryRow("SELECT token_ciphertext FROM github_credentials WHERE id = ?", credential.ID).Scan(&ciphertext); err != nil {
		t.Fatalf("query ciphertext: %v", err)
	}
	if ciphertext == token || strings.Contains(ciphertext, token) {
		t.Fatal("database contains plaintext token")
	}
	plaintext, err := cipher.Decrypt(ciphertext)
	if err != nil || plaintext != token {
		t.Fatalf("decrypt stored token = %q, %v", plaintext, err)
	}

	rows, err := store.SQL().Query("SELECT resource, limit_value, used, remaining FROM api_rate_limits WHERE credential_id = ? ORDER BY resource", credential.ID)
	if err != nil {
		t.Fatalf("query rate limits: %v", err)
	}
	defer rows.Close()
	got := make(map[string][3]int)
	for rows.Next() {
		var resource string
		var limit, used, remaining int
		if err := rows.Scan(&resource, &limit, &used, &remaining); err != nil {
			t.Fatalf("scan rate limit: %v", err)
		}
		got[resource] = [3]int{limit, used, remaining}
	}
	if got["core"] != [3]int{5000, 3, 4997} || got["search"] != [3]int{30, 1, 29} {
		t.Errorf("rate limits = %+v", got)
	}

	listed, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != credential.ID {
		t.Fatalf("listed credentials = %+v", listed)
	}
	if strings.Contains(string(encoded), token) || strings.Contains(string(encoded), ciphertext) || strings.Contains(string(encoded), "token_ciphertext") {
		t.Fatalf("credential list leaked secret: %s", encoded)
	}
}
