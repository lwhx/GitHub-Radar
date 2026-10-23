package github

import (
	"context"
	"errors"
	"regexp"
)

var repositoryPathSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func (client *Client) GetRepository(ctx context.Context, owner string, name string, etag string) (Repository, ResponseMeta, error) {
	if !repositoryPathSegment.MatchString(owner) || !repositoryPathSegment.MatchString(name) {
		return Repository{}, ResponseMeta{}, errors.New("GitHub repository owner and name must be valid path segments")
	}
	var repository Repository
	path := "/repos/" + owner + "/" + name
	endpoint := *client.baseURL
	endpoint.Path += path
	meta, err := client.getJSONURLWithOptions(ctx, &endpoint, &repository, requestOptions{etag: etag, allowNotModified: true})
	return repository, meta, err
}
