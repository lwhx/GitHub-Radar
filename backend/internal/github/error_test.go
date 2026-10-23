package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAPIErrorClassification(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 23, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		status      int
		headers     map[string]string
		body        string
		wantKind    ErrorKind
		wantRetryAt time.Time
	}{
		{name: "invalid credential", status: http.StatusUnauthorized, body: `{"message":"Bad credentials"}`, wantKind: ErrorAuthentication},
		{name: "permission denied", status: http.StatusForbidden, body: `{"message":"Resource not accessible"}`, wantKind: ErrorForbidden},
		{name: "primary limit", status: http.StatusForbidden, headers: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1792728060"}, body: `{"message":"API rate limit exceeded"}`, wantKind: ErrorPrimaryRateLimit, wantRetryAt: time.Unix(1792728060, 0).UTC()},
		{name: "secondary limit with retry after", status: http.StatusForbidden, headers: map[string]string{"Retry-After": "75", "X-RateLimit-Remaining": "12"}, body: `{"message":"You have exceeded a secondary rate limit"}`, wantKind: ErrorSecondaryRateLimit, wantRetryAt: now.Add(75 * time.Second)},
		{name: "secondary limit default delay", status: http.StatusTooManyRequests, body: `{"message":"slow down"}`, wantKind: ErrorSecondaryRateLimit, wantRetryAt: now.Add(time.Minute)},
		{name: "not found", status: http.StatusNotFound, body: `{"message":"Not Found"}`, wantKind: ErrorNotFound},
		{name: "gone", status: http.StatusGone, body: `{"message":"Gone"}`, wantKind: ErrorGone},
		{name: "service failure", status: http.StatusServiceUnavailable, body: `{"message":"Service Unavailable"}`, wantKind: ErrorService},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for name, value := range test.headers {
					w.Header().Set(name, value)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client, err := NewClient(ClientConfig{
				BaseURL: server.URL,
				Now:     func() time.Time { return now },
				Sleep:   func(context.Context, time.Duration) error { return nil },
			})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			_, _, err = client.SearchRepositories(context.Background(), SearchOptions{From: now.Add(-time.Hour), To: now})
			var apiError *APIError
			if !errors.As(err, &apiError) {
				t.Fatalf("error = %T %v, want *APIError", err, err)
			}
			if apiError.Kind != test.wantKind {
				t.Errorf("kind = %q, want %q", apiError.Kind, test.wantKind)
			}
			if !apiError.RetryAt.Equal(test.wantRetryAt) {
				t.Errorf("retry_at = %v, want %v", apiError.RetryAt, test.wantRetryAt)
			}
		})
	}
}

func TestSearchRepositoriesReturnsTypedValidationError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-GitHub-Request-Id", "request-422")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{
			"message": "Validation Failed",
			"documentation_url": "https://docs.github.com/rest/search/search#search-repositories",
			"errors": [{"resource":"Search","field":"q","code":"invalid"}]
		}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "do-not-leak-token"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, _, err = client.SearchRepositories(context.Background(), SearchOptions{
		From: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC),
	})

	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("error = %T %v, want *APIError", err, err)
	}
	if apiError.Kind != ErrorValidation || apiError.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("API error = %+v", apiError)
	}
	if apiError.RequestID != "request-422" || apiError.DocumentationURL == "" {
		t.Errorf("API error metadata = %+v", apiError)
	}
	if len(apiError.Errors) != 1 || apiError.Errors[0].Field != "q" {
		t.Errorf("validation errors = %+v", apiError.Errors)
	}
	if strings.Contains(err.Error(), "do-not-leak-token") {
		t.Fatal("error leaked token")
	}
}
