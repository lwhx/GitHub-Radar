package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestResponseMetaPreservesMissingRateLimitValues(t *testing.T) {
	t.Parallel()

	meta := responseMeta(http.Header{"X-Ratelimit-Resource": {"core"}})
	if meta.RateLimit.Limit != nil || meta.RateLimit.Used != nil || meta.RateLimit.Remaining != nil || meta.RateLimit.ResetAt != nil {
		t.Fatalf("missing rate limit values became concrete zeros: %+v", meta.RateLimit)
	}
}

func TestSearchRepositoriesSendsContractHeadersAndDecodesResponse(t *testing.T) {
	t.Parallel()

	var requestURL *url.URL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURL = r.URL
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q", got)
		}
		if got := r.Header.Get("X-GitHub-Api-Version"); got != APIVersion {
			t.Errorf("X-GitHub-Api-Version = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != defaultUserAgent {
			t.Errorf("User-Agent = %q", got)
		}
		w.Header().Set("ETag", `"search-v1"`)
		w.Header().Set("Link", `<https://api.github.com/search/repositories?q=created&page=3>; rel="next", <https://api.github.com/search/repositories?q=created&page=10>; rel="last"`)
		w.Header().Set("X-RateLimit-Limit", "30")
		w.Header().Set("X-RateLimit-Remaining", "29")
		w.Header().Set("X-RateLimit-Reset", "1792713600")
		w.Header().Set("X-RateLimit-Resource", "search")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"total_count": 1,
			"incomplete_results": false,
			"items": [{
				"id": 1296269,
				"name": "Hello-World",
				"full_name": "octocat/Hello-World",
				"owner": {"login": "octocat"},
				"html_url": "https://github.com/octocat/Hello-World",
				"description": "example",
				"private": false,
				"visibility": "public",
				"fork": false,
				"stargazers_count": 80,
				"forks_count": 9,
				"open_issues_count": 1,
				"default_branch": "main",
				"topics": ["example"],
				"archived": false,
				"disabled": false,
				"created_at": "2026-10-23T00:00:00Z",
				"updated_at": "2026-10-23T01:00:00Z",
				"pushed_at": "2026-10-23T00:30:00Z",
				"license": {"spdx_id": "MIT"}
			}]
		}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "test-token"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	from := time.Date(2026, 10, 22, 18, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	result, meta, err := client.SearchRepositories(context.Background(), SearchOptions{From: from, To: to, Page: 2})
	if err != nil {
		t.Fatalf("SearchRepositories() error = %v", err)
	}

	if got := requestURL.Path; got != "/search/repositories" {
		t.Errorf("path = %q", got)
	}
	wantQuery := "created:2026-10-22T18:00:00Z..2026-10-23T00:00:00Z is:public"
	if got := requestURL.Query().Get("q"); got != wantQuery {
		t.Errorf("q = %q, want %q", got, wantQuery)
	}
	if got := requestURL.Query().Get("sort"); got != "created" {
		t.Errorf("sort = %q", got)
	}
	if got := requestURL.Query().Get("order"); got != "asc" {
		t.Errorf("order = %q", got)
	}
	if got := requestURL.Query().Get("page"); got != "2" {
		t.Errorf("page = %q", got)
	}
	if got := requestURL.Query().Get("per_page"); got != "100" {
		t.Errorf("per_page = %q", got)
	}
	if result.TotalCount != 1 || result.IncompleteResults || len(result.Items) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if got := result.Items[0].FullName; got != "octocat/Hello-World" {
		t.Errorf("full name = %q", got)
	}
	if result.Items[0].Private || result.Items[0].Visibility != "public" {
		t.Errorf("visibility = %q, private = %v", result.Items[0].Visibility, result.Items[0].Private)
	}
	if result.Items[0].License == nil || result.Items[0].License.SPDXID != "MIT" {
		t.Errorf("license = %+v", result.Items[0].License)
	}
	if meta.ETag != `"search-v1"` {
		t.Errorf("ETag = %q", meta.ETag)
	}
	if want := "https://api.github.com/search/repositories?q=created&page=3"; meta.NextURL != want {
		t.Errorf("next URL = %q, want %q", meta.NextURL, want)
	}
	if meta.RateLimit.Resource != "search" || meta.RateLimit.Limit == nil || *meta.RateLimit.Limit != 30 || meta.RateLimit.Remaining == nil || *meta.RateLimit.Remaining != 29 {
		t.Errorf("rate limit = %+v", meta.RateLimit)
	}
	if meta.RateLimit.ResetAt == nil || !meta.RateLimit.ResetAt.Equal(time.Unix(1792713600, 0).UTC()) {
		t.Errorf("reset_at = %v", meta.RateLimit.ResetAt)
	}
}
