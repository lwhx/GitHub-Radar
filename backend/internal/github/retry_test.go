package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestClientRetriesTransientTransportFailures(t *testing.T) {
	t.Parallel()

	attempts := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("connection reset")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"login":"octocat","id":1,"type":"User"}`)),
		}, nil
	})}
	var delays []time.Duration
	client, err := NewClient(ClientConfig{
		BaseURL:    "https://api.github.test",
		HTTPClient: httpClient,
		Sleep: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
		RetryBackoff: func(attempt int) time.Duration { return time.Duration(attempt) * time.Second },
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	user, _, err := client.GetAuthenticatedUser(context.Background())
	if err != nil {
		t.Fatalf("GetAuthenticatedUser() error = %v", err)
	}
	if user.Login != "octocat" || attempts != 3 {
		t.Errorf("user = %+v, attempts = %d", user, attempts)
	}
	if want := []time.Duration{time.Second, 2 * time.Second}; !reflect.DeepEqual(delays, want) {
		t.Errorf("delays = %v, want %v", delays, want)
	}
}

func TestClientRetriesServiceFailuresWithBoundedBackoff(t *testing.T) {
	t.Parallel()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"message":"temporarily unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"octocat","id":1,"type":"User"}`))
	}))
	defer server.Close()

	var delays []time.Duration
	client, err := NewClient(ClientConfig{
		BaseURL: server.URL,
		Token:   "test-token",
		Sleep: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
		RetryBackoff: func(attempt int) time.Duration {
			return time.Duration(attempt) * time.Second
		},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	user, _, err := client.GetAuthenticatedUser(context.Background())
	if err != nil {
		t.Fatalf("GetAuthenticatedUser() error = %v", err)
	}
	if user.Login != "octocat" {
		t.Errorf("user = %+v", user)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d", attempts)
	}
	if want := []time.Duration{time.Second, 2 * time.Second}; !reflect.DeepEqual(delays, want) {
		t.Errorf("delays = %v, want %v", delays, want)
	}
}
