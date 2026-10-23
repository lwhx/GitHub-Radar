package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL       = "https://api.github.com"
	defaultMaxConcurrent = 2
	defaultMaxRetries    = 2
	maxResponseBody      = 16 << 20
)

type ClientConfig struct {
	BaseURL       string
	Token         string
	HTTPClient    *http.Client
	MaxConcurrent int
	Now           func() time.Time
	Sleep         func(context.Context, time.Duration) error
	RetryBackoff  func(int) time.Duration
}

type Client struct {
	baseURL      *url.URL
	token        string
	httpClient   *http.Client
	now          func() time.Time
	sleep        func(context.Context, time.Duration) error
	retryBackoff func(int) time.Duration
	maxRetries   int
	semaphore    chan struct{}
}

func NewClient(config ClientConfig) (*Client, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("GitHub base URL must be an absolute URL without user info")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	configuredHTTPClient := config.HTTPClient
	if configuredHTTPClient == nil {
		configuredHTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	client := secureHTTPClient(configuredHTTPClient, parsed)
	now := config.Now
	if now == nil {
		now = time.Now
	}
	sleep := config.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	retryBackoff := config.RetryBackoff
	if retryBackoff == nil {
		retryBackoff = defaultRetryBackoff
	}
	maxConcurrent := config.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrent
	}
	return &Client{
		baseURL: parsed, token: strings.TrimSpace(config.Token), httpClient: client, now: now,
		sleep: sleep, retryBackoff: retryBackoff, maxRetries: defaultMaxRetries,
		semaphore: make(chan struct{}, maxConcurrent),
	}, nil
}

func secureHTTPClient(source *http.Client, baseURL *url.URL) *http.Client {
	clone := *source
	previousPolicy := source.CheckRedirect
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != baseURL.Scheme || !strings.EqualFold(request.URL.Host, baseURL.Host) {
			return http.ErrUseLastResponse
		}
		if len(via) >= 3 {
			return errors.New("too many GitHub redirects")
		}
		if previousPolicy != nil {
			return previousPolicy(request, via)
		}
		return nil
	}
	return &clone
}

func (client *Client) getJSON(ctx context.Context, path string, query url.Values, destination any) (ResponseMeta, error) {
	endpoint := *client.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	return client.getJSONURL(ctx, &endpoint, destination)
}

type requestOptions struct {
	etag             string
	allowNotModified bool
}

func (client *Client) getJSONURL(ctx context.Context, endpoint *url.URL, destination any) (ResponseMeta, error) {
	return client.getJSONURLWithOptions(ctx, endpoint, destination, requestOptions{})
}

func (client *Client) getJSONURLWithOptions(ctx context.Context, endpoint *url.URL, destination any, options requestOptions) (ResponseMeta, error) {
	for attempt := 0; ; attempt++ {
		response, err := client.doGET(ctx, endpoint, options)
		if err != nil {
			if ctx.Err() != nil || attempt >= client.maxRetries {
				return ResponseMeta{}, err
			}
			if err := client.sleep(ctx, client.retryBackoff(attempt+1)); err != nil {
				return ResponseMeta{}, err
			}
			continue
		}
		if response.StatusCode >= http.StatusInternalServerError && attempt < client.maxRetries {
			response.Body.Close()
			if err := client.sleep(ctx, client.retryBackoff(attempt+1)); err != nil {
				return ResponseMeta{}, err
			}
			continue
		}
		return client.decodeResponse(response, destination, options)
	}
}

func (client *Client) doGET(ctx context.Context, endpoint *url.URL, options requestOptions) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create GitHub request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", APIVersion)
	request.Header.Set("User-Agent", defaultUserAgent)
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	if options.etag != "" {
		request.Header.Set("If-None-Match", options.etag)
	}
	if err := client.acquire(ctx); err != nil {
		return nil, err
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		client.release()
		return nil, fmt.Errorf("send GitHub request: %w", err)
	}
	response.Body = &releaseReadCloser{ReadCloser: response.Body, release: client.release}
	return response, nil
}

func (client *Client) acquire(ctx context.Context) error {
	select {
	case client.semaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (client *Client) release() {
	<-client.semaphore
}

type releaseReadCloser struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (closer *releaseReadCloser) Close() error {
	err := closer.ReadCloser.Close()
	closer.once.Do(closer.release)
	return err
}

func (client *Client) decodeResponse(response *http.Response, destination any, options requestOptions) (ResponseMeta, error) {
	defer response.Body.Close()
	meta := responseMeta(response.Header)
	meta.StatusCode = response.StatusCode
	if response.StatusCode == http.StatusNotModified && options.allowNotModified {
		meta.NotModified = true
		return meta, nil
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBody))
		return meta, newAPIError(response.StatusCode, response.Header, body, client.now().UTC())
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBody))
	if err := decoder.Decode(destination); err != nil {
		return meta, fmt.Errorf("decode GitHub response: %w", err)
	}
	return meta, nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func defaultRetryBackoff(attempt int) time.Duration {
	base := time.Second << max(attempt-1, 0)
	jitter := time.Duration(rand.IntN(251)) * time.Millisecond
	return base + jitter
}

func responseMeta(headers http.Header) ResponseMeta {
	return ResponseMeta{
		ETag:    headers.Get("ETag"),
		NextURL: parseNextURL(headers.Get("Link")),
		RateLimit: RateLimit{
			Resource:  headers.Get("X-RateLimit-Resource"),
			Limit:     parseHeaderInt(headers, "X-RateLimit-Limit"),
			Used:      parseHeaderInt(headers, "X-RateLimit-Used"),
			Remaining: parseHeaderInt(headers, "X-RateLimit-Remaining"),
			ResetAt:   parseHeaderTime(headers, "X-RateLimit-Reset"),
		},
	}
}

func parseNextURL(linkHeader string) string {
	for _, part := range strings.Split(linkHeader, ",") {
		segments := strings.Split(part, ";")
		if len(segments) < 2 {
			continue
		}
		hasNextRelation := false
		for _, parameter := range segments[1:] {
			if strings.TrimSpace(parameter) == `rel="next"` {
				hasNextRelation = true
				break
			}
		}
		if !hasNextRelation {
			continue
		}
		return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(segments[0]), "<"), ">")
	}
	return ""
}

func parseHeaderInt(headers http.Header, name string) *int {
	value, err := strconv.Atoi(headers.Get(name))
	if err != nil {
		return nil
	}
	return &value
}

func parseHeaderTime(headers http.Header, name string) *time.Time {
	value := parseResetTime(headers.Get(name))
	if value.IsZero() {
		return nil
	}
	return &value
}

func parseResetTime(value string) time.Time {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}
