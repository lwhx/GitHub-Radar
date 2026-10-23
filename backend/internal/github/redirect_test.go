package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientRejectsCrossOriginRedirectBeforeSendingRequest(t *testing.T) {
	t.Parallel()

	evilRequests := 0
	evil := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		evilRequests++
	}))
	defer evil.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", evil.URL+"/stolen")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer origin.Close()

	client, err := NewClient(ClientConfig{BaseURL: origin.URL, Token: "must-not-leak"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, _, err = client.GetRepository(context.Background(), "octocat", "Hello-World", "")
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("error = %T %v", err, err)
	}
	if evilRequests != 0 {
		t.Fatalf("cross-origin redirect requests = %d", evilRequests)
	}
}
