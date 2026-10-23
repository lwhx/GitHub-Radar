package readmes

import (
	"context"
	"time"

	"github.com/github-radar/github-radar/internal/credentials"
	"github.com/github-radar/github-radar/internal/github"
)

// Quota 提供可用凭据并回报额度消耗，与采集器共用同一套凭据分配。
type Quota interface {
	Usable(ctx context.Context, resource string) (credentials.UsableCredential, error)
	RecordRateLimit(ctx context.Context, credentialID int64, resource string, limit, remaining int, resetAt time.Time) error
}

// GitHubProvider 用真实 GitHub API 拉取 README。
type GitHubProvider struct {
	quota   Quota
	baseURL string
}

func NewGitHubProvider(quota Quota, baseURL string) *GitHubProvider {
	return &GitHubProvider{quota: quota, baseURL: baseURL}
}

func (provider *GitHubProvider) Readme(ctx context.Context, owner string, name string) (github.Readme, error) {
	usable, err := provider.quota.Usable(ctx, "core")
	if err != nil {
		return github.Readme{}, err
	}
	client, err := github.NewClient(github.ClientConfig{BaseURL: provider.baseURL, Token: usable.Token})
	if err != nil {
		return github.Readme{}, err
	}
	readme, meta, err := client.GetReadme(ctx, owner, name)
	limit, remaining, resetAt, ok := rateLimitSnapshot(meta)
	if ok {
		_ = provider.quota.RecordRateLimit(ctx, usable.ID, "core", limit, remaining, resetAt)
	}
	return readme, err
}

func rateLimitSnapshot(meta github.ResponseMeta) (int, int, time.Time, bool) {
	if meta.RateLimit.Limit == nil || meta.RateLimit.Remaining == nil {
		return 0, 0, time.Time{}, false
	}
	resetAt := time.Time{}
	if meta.RateLimit.ResetAt != nil {
		resetAt = *meta.RateLimit.ResetAt
	}
	return *meta.RateLimit.Limit, *meta.RateLimit.Remaining, resetAt, true
}
