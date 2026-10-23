package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchRepositoriesNextRejectsDifferentOrigin(t *testing.T) {
	t.Parallel()

	requestCount := 0
	otherServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requestCount++
	}))
	defer otherServer.Close()
	baseServer := httptest.NewServer(http.NotFoundHandler())
	defer baseServer.Close()

	client, err := NewClient(ClientConfig{BaseURL: baseServer.URL, Token: "must-not-leak"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, _, err = client.SearchRepositoriesNext(context.Background(), otherServer.URL+"/search/repositories?page=2")
	if err == nil {
		t.Fatal("SearchRepositoriesNext() error = nil")
	}
	if requestCount != 0 {
		t.Fatalf("off-origin request count = %d", requestCount)
	}
}

func TestSearchRepositoriesNextFollowsLinkURL(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") != "opaque-value" {
			t.Errorf("cursor = %q", r.URL.Query().Get("cursor"))
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total_count":1,"incomplete_results":false,"items":[]}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "test-token"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, _, err := client.SearchRepositoriesNext(context.Background(), server.URL+"/search/repositories?cursor=opaque-value")
	if err != nil {
		t.Fatalf("SearchRepositoriesNext() error = %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("total count = %d", result.TotalCount)
	}
}
