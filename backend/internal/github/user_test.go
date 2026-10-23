package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAuthenticatedUserReturnsStableIdentity(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"octocat","id":1,"type":"User","site_admin":false}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "test-token"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	user, _, err := client.GetAuthenticatedUser(context.Background())
	if err != nil {
		t.Fatalf("GetAuthenticatedUser() error = %v", err)
	}
	if user.ID != 1 || user.Login != "octocat" || user.Type != "User" {
		t.Errorf("user = %+v", user)
	}
}
