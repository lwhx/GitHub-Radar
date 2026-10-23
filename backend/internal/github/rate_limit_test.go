package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetRateLimitStatusUsesVersionedResourceBuckets(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rate_limit" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"resources": {
				"core": {"limit":5000,"used":4,"remaining":4996,"reset":1792728060},
				"search": {"limit":30,"used":1,"remaining":29,"reset":1792724460}
			}
		}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "test-token"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	status, _, err := client.GetRateLimitStatus(context.Background())
	if err != nil {
		t.Fatalf("GetRateLimitStatus() error = %v", err)
	}
	if status.Resources.Core.Limit != 5000 || status.Resources.Core.Remaining != 4996 {
		t.Errorf("core = %+v", status.Resources.Core)
	}
	if status.Resources.Search.Limit != 30 || status.Resources.Search.Remaining != 29 {
		t.Errorf("search = %+v", status.Resources.Search)
	}
	if got := status.Resources.Search.ResetAt(); !got.Equal(time.Unix(1792724460, 0).UTC()) {
		t.Errorf("search reset = %v", got)
	}
}
