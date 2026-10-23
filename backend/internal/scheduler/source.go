package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/github-radar/github-radar/internal/credentials"
	"github.com/github-radar/github-radar/internal/github"
)

// CredentialQuota is the credential surface the GitHub adapters need.
type CredentialQuota interface {
	Usable(ctx context.Context, resource string) (credentials.UsableCredential, error)
	RecordCooldown(ctx context.Context, credentialID int64, resource string, until time.Time, reason string) error
	RecordRateLimit(ctx context.Context, credentialID int64, resource string, limit, remaining int, resetAt time.Time) error
}

type credentialObserver struct {
	quota        CredentialQuota
	credentialID int64
	resource     string
	now          func() time.Time
}

type credentialSearcher struct {
	client   *github.Client
	observer credentialObserver
}

type credentialRepositoryGetter struct {
	client   *github.Client
	observer credentialObserver
}

// CredentialSearcherSource resolves the highest-priority usable search credential.
func CredentialSearcherSource(quota CredentialQuota, baseURL string) (func(context.Context) (Searcher, error), error) {
	if quota == nil {
		return nil, errors.New("credential quota service is required")
	}
	return func(ctx context.Context) (Searcher, error) {
		client, observer, err := credentialClient(ctx, quota, baseURL, "search")
		if err != nil {
			return nil, err
		}
		return credentialSearcher{client: client, observer: observer}, nil
	}, nil
}

// CredentialRepositoryGetterSource resolves a core-quota credential for conditional repository requests.
func CredentialRepositoryGetterSource(quota CredentialQuota, baseURL string) (func(context.Context) (RepositoryGetter, error), error) {
	if quota == nil {
		return nil, errors.New("credential quota service is required")
	}
	return func(ctx context.Context) (RepositoryGetter, error) {
		client, observer, err := credentialClient(ctx, quota, baseURL, "core")
		if err != nil {
			return nil, err
		}
		return credentialRepositoryGetter{client: client, observer: observer}, nil
	}, nil
}

func credentialClient(ctx context.Context, quota CredentialQuota, baseURL, resource string) (*github.Client, credentialObserver, error) {
	usable, err := quota.Usable(ctx, resource)
	if err != nil {
		return nil, credentialObserver{}, err
	}
	client, err := github.NewClient(github.ClientConfig{BaseURL: baseURL, Token: usable.Token})
	if err != nil {
		return nil, credentialObserver{}, err
	}
	observer := credentialObserver{quota: quota, credentialID: usable.ID, resource: resource, now: time.Now}
	return client, observer, nil
}

func (search credentialSearcher) Search(ctx context.Context, options github.SearchOptions) (github.SearchResult, github.ResponseMeta, error) {
	result, meta, err := search.client.SearchRepositories(ctx, options)
	return result, meta, search.observer.observe(ctx, meta, err)
}

func (search credentialSearcher) SearchNext(ctx context.Context, nextURL string) (github.SearchResult, github.ResponseMeta, error) {
	result, meta, err := search.client.SearchRepositoriesNext(ctx, nextURL)
	return result, meta, search.observer.observe(ctx, meta, err)
}

func (getter credentialRepositoryGetter) GetRepository(ctx context.Context, owner, name, etag string) (github.Repository, github.ResponseMeta, error) {
	repository, meta, err := getter.client.GetRepository(ctx, owner, name, etag)
	return repository, meta, getter.observer.observe(ctx, meta, err)
}

func (observer credentialObserver) observe(ctx context.Context, meta github.ResponseMeta, requestErr error) error {
	var observationErr error
	if meta.RateLimit.Remaining != nil && meta.RateLimit.ResetAt != nil && meta.RateLimit.Limit != nil {
		observationErr = observer.quota.RecordRateLimit(context.WithoutCancel(ctx), observer.credentialID, observer.resource,
			*meta.RateLimit.Limit, *meta.RateLimit.Remaining, *meta.RateLimit.ResetAt)
		if observationErr != nil {
			observationErr = fmt.Errorf("record GitHub %s quota: %w", observer.resource, observationErr)
		}
	}
	var apiError *github.APIError
	if !errors.As(requestErr, &apiError) || (apiError.Kind != github.ErrorPrimaryRateLimit && apiError.Kind != github.ErrorSecondaryRateLimit) {
		return errors.Join(requestErr, observationErr)
	}
	until := apiError.RetryAt
	if until.IsZero() {
		until = observer.now().UTC().Add(time.Minute)
	}
	cooldownErr := observer.quota.RecordCooldown(context.WithoutCancel(ctx), observer.credentialID, observer.resource, until, string(apiError.Kind))
	if cooldownErr != nil {
		cooldownErr = fmt.Errorf("record GitHub %s cooldown: %w", observer.resource, cooldownErr)
	}
	return errors.Join(requestErr, observationErr, cooldownErr)
}
