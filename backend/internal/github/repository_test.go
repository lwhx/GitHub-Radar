package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetRepositoryReturnsNotModifiedWithoutDecodingBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/octocat/Hello-World" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("If-None-Match"); got != `W/"repo-v1"` {
			t.Errorf("If-None-Match = %q", got)
		}
		w.Header().Set("ETag", `W/"repo-v1"`)
		w.Header().Set("X-RateLimit-Remaining", "4998")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "test-token"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	repository, meta, err := client.GetRepository(context.Background(), "octocat", "Hello-World", `W/"repo-v1"`)
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}
	if !meta.NotModified {
		t.Fatal("NotModified = false")
	}
	if repository.ID != 0 {
		t.Errorf("repository = %+v", repository)
	}
	if meta.RateLimit.Remaining == nil || *meta.RateLimit.Remaining != 4998 {
		t.Errorf("rate limit = %+v", meta.RateLimit)
	}
}
