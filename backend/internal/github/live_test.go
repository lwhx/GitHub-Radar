package github

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveSearchContract calls the real GitHub API and only runs when GITHUB_LIVE=1.
// It uses no credential: unauthenticated search is rate limited but legal.
func TestLiveSearchContract(t *testing.T) {
	if os.Getenv("GITHUB_LIVE") != "1" {
		t.Skip("set GITHUB_LIVE=1 to call the real GitHub API")
	}
	client, err := NewClient(ClientConfig{})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	to := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	from := to.Add(-30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, meta, err := client.SearchRepositories(ctx, SearchOptions{From: from, To: to})
	if err != nil {
		t.Fatalf("SearchRepositories() error = %v", err)
	}
	t.Logf("total=%d items=%d status=%d etag=%q remaining=%v next=%q incomplete=%v",
		result.TotalCount, len(result.Items), meta.StatusCode, meta.ETag,
		meta.RateLimit.Remaining, meta.NextURL, result.IncompleteResults)
	if meta.StatusCode != 200 {
		t.Fatalf("status = %d", meta.StatusCode)
	}
	if meta.RateLimit.Limit == nil || meta.RateLimit.Remaining == nil {
		t.Error("live response did not expose rate limit headers")
	}
	for _, item := range result.Items {
		if item.Private || (item.Visibility != "" && item.Visibility != "public") {
			t.Fatalf("unauthenticated search returned non-public repository %s", item.FullName)
		}
	}
	if len(result.Items) == 0 {
		t.Log("no repositories were created in the sampled 30 minute window")
	}
}
