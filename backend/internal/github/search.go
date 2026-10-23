package github

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type SearchOptions struct {
	From time.Time
	To   time.Time
	Page int
}

func (client *Client) SearchRepositories(ctx context.Context, options SearchOptions) (SearchResult, ResponseMeta, error) {
	if options.From.IsZero() || options.To.IsZero() || !options.From.Before(options.To) {
		return SearchResult{}, ResponseMeta{}, errors.New("GitHub search window must have from before to")
	}
	page := options.Page
	if page == 0 {
		page = 1
	}
	if page < 1 || page > 10 {
		return SearchResult{}, ResponseMeta{}, errors.New("GitHub search page must be between 1 and 10")
	}
	query := url.Values{
		"q":        {"created:" + formatSearchTime(options.From) + ".." + formatSearchTime(options.To) + " is:public"},
		"sort":     {"created"},
		"order":    {"asc"},
		"page":     {strconv.Itoa(page)},
		"per_page": {"100"},
	}
	var result SearchResult
	meta, err := client.getJSON(ctx, "/search/repositories", query, &result)
	return result, meta, err
}

func (client *Client) SearchRepositoriesNext(ctx context.Context, nextURL string) (SearchResult, ResponseMeta, error) {
	endpoint, err := url.Parse(nextURL)
	if err != nil || endpoint.Scheme != client.baseURL.Scheme || !strings.EqualFold(endpoint.Host, client.baseURL.Host) || endpoint.User != nil {
		return SearchResult{}, ResponseMeta{}, errors.New("GitHub next page URL must use the configured API origin")
	}
	wantPath := strings.TrimRight(client.baseURL.Path, "/") + "/search/repositories"
	if endpoint.Path != wantPath {
		return SearchResult{}, ResponseMeta{}, errors.New("GitHub next page URL has an unexpected path")
	}
	var result SearchResult
	meta, err := client.getJSONURL(ctx, endpoint, &result)
	return result, meta, err
}

func formatSearchTime(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(time.RFC3339)
}
