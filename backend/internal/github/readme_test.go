package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetReadmeRequestsRawMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/demo/readme" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if accept := r.Header.Get("Accept"); accept != "application/vnd.github.raw" {
			t.Errorf("Accept = %q, want raw markdown media type", accept)
		}
		w.Header().Set("X-GitHub-Media-Type", "markdown")
		_, _ = w.Write([]byte("# Demo\n正文"))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "token"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	readme, meta, err := client.GetReadme(context.Background(), "acme", "demo")
	if err != nil {
		t.Fatalf("GetReadme() error = %v", err)
	}
	if !strings.Contains(readme.Content, "# Demo") {
		t.Fatalf("content = %q", readme.Content)
	}
	if readme.MediaType != "markdown" || meta.StatusCode != http.StatusOK {
		t.Fatalf("media type = %q, status = %d", readme.MediaType, meta.StatusCode)
	}
}

func TestGetReadmeMapsMissingToNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, _, err = client.GetReadme(context.Background(), "acme", "missing")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != ErrorNotFound {
		t.Fatalf("error = %v, want ErrorNotFound", err)
	}
}
