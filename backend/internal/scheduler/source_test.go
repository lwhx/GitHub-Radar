package scheduler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/credentials"
	"github.com/github-radar/github-radar/internal/github"
)

type quotaObservation struct {
	credentialID int64
	resource     string
	limit        int
	remaining    int
	resetAt      time.Time
}

type cooldownObservation struct {
	credentialID int64
	resource     string
	until        time.Time
	reason       string
}

type fakeCredentialQuota struct {
	usableResources []string
	usable          credentials.UsableCredential
	rateLimits      []quotaObservation
	cooldowns       []cooldownObservation
}

func (fake *fakeCredentialQuota) Usable(_ context.Context, resource string) (credentials.UsableCredential, error) {
	fake.usableResources = append(fake.usableResources, resource)
	return fake.usable, nil
}

func (fake *fakeCredentialQuota) RecordRateLimit(_ context.Context, credentialID int64, resource string, limit, remaining int, resetAt time.Time) error {
	fake.rateLimits = append(fake.rateLimits, quotaObservation{
		credentialID: credentialID, resource: resource, limit: limit, remaining: remaining, resetAt: resetAt,
	})
	return nil
}

func (fake *fakeCredentialQuota) RecordCooldown(_ context.Context, credentialID int64, resource string, until time.Time, reason string) error {
	fake.cooldowns = append(fake.cooldowns, cooldownObservation{
		credentialID: credentialID, resource: resource, until: until, reason: reason,
	})
	return nil
}

func TestCredentialRepositoryGetterSourceUsesCoreQuotaAndObservesResponse(t *testing.T) {
	t.Parallel()

	resetAt := time.Date(2026, 10, 23, 13, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/name" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer detail-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("If-None-Match") != `W/"old"` {
			t.Errorf("If-None-Match = %q", r.Header.Get("If-None-Match"))
		}
		w.Header().Set("ETag", `W/"new"`)
		w.Header().Set("X-RateLimit-Resource", "core")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4998")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(resetAt.Unix()))
		fmt.Fprint(w, `{"id":77,"owner":{"login":"owner"},"name":"name","full_name":"owner/name","html_url":"https://github.com/owner/name","visibility":"public","created_at":"2026-10-20T00:00:00Z","updated_at":"2026-10-23T11:00:00Z","stargazers_count":8}`)
	}))
	t.Cleanup(server.Close)
	quota := &fakeCredentialQuota{usable: credentials.UsableCredential{
		Credential: credentials.Credential{ID: 9}, Token: "detail-token",
	}}

	source, err := CredentialRepositoryGetterSource(quota, server.URL)
	if err != nil {
		t.Fatalf("CredentialRepositoryGetterSource() error = %v", err)
	}
	getter, err := source(context.Background())
	if err != nil {
		t.Fatalf("source() error = %v", err)
	}
	repository, meta, err := getter.GetRepository(context.Background(), "owner", "name", `W/"old"`)
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}
	if repository.ID != 77 || meta.ETag != `W/"new"` {
		t.Fatalf("repository = %+v meta = %+v", repository, meta)
	}
	if len(quota.usableResources) != 1 || quota.usableResources[0] != "core" {
		t.Fatalf("usable resources = %+v", quota.usableResources)
	}
	if len(quota.rateLimits) != 1 {
		t.Fatalf("rate limit observations = %+v", quota.rateLimits)
	}
	observation := quota.rateLimits[0]
	if observation.credentialID != 9 || observation.resource != "core" || observation.limit != 5000 || observation.remaining != 4998 || !observation.resetAt.Equal(resetAt) {
		t.Fatalf("rate limit observation = %+v", observation)
	}
}

func TestCredentialRepositoryGetterSourceRecordsCoreCooldownOnRateLimit(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	resetAt := now.Add(20 * time.Minute)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Resource", "core")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(resetAt.Unix()))
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
	}))
	t.Cleanup(server.Close)
	quota := &fakeCredentialQuota{usable: credentials.UsableCredential{
		Credential: credentials.Credential{ID: 9}, Token: "detail-token",
	}}
	source, err := CredentialRepositoryGetterSource(quota, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	getter, err := source(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = getter.GetRepository(context.Background(), "owner", "name", "")
	var apiError *github.APIError
	if !errors.As(err, &apiError) || apiError.Kind != github.ErrorPrimaryRateLimit {
		t.Fatalf("GetRepository() error = %v", err)
	}
	if len(quota.rateLimits) != 1 || quota.rateLimits[0].remaining != 0 {
		t.Fatalf("rate limits = %+v", quota.rateLimits)
	}
	if len(quota.cooldowns) != 1 {
		t.Fatalf("cooldowns = %+v", quota.cooldowns)
	}
	cooldown := quota.cooldowns[0]
	if cooldown.credentialID != 9 || cooldown.resource != "core" || cooldown.reason != string(github.ErrorPrimaryRateLimit) || !cooldown.until.Equal(resetAt) {
		t.Fatalf("cooldown = %+v", cooldown)
	}
}
