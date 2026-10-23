package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// readmeMaxBytes 限制单次 README 下载体积，避免异常仓库拖垮进程。
const readmeMaxBytes = 1 << 20

// Readme 是 GitHub 返回的 README 原文（Markdown），尚未净化。
type Readme struct {
	Content   string
	MediaType string
}

// GetReadme 以原始媒体类型拉取 README；仓库不存在或没有 README 时返回 404 APIError。
func (client *Client) GetReadme(ctx context.Context, owner string, name string) (Readme, ResponseMeta, error) {
	if !repositoryPathSegment.MatchString(owner) || !repositoryPathSegment.MatchString(name) {
		return Readme{}, ResponseMeta{}, errors.New("GitHub repository owner and name must be valid path segments")
	}
	endpoint := *client.baseURL
	endpoint.Path += "/repos/" + owner + "/" + name + "/readme"
	response, err := client.doGET(ctx, &endpoint, requestOptions{accept: "application/vnd.github.raw"})
	if err != nil {
		return Readme{}, ResponseMeta{}, err
	}
	defer response.Body.Close()
	meta := responseMeta(response.Header)
	meta.StatusCode = response.StatusCode
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBody))
		return Readme{}, meta, newAPIError(response.StatusCode, response.Header, body, client.now())
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, readmeMaxBytes))
	if err != nil {
		return Readme{}, meta, fmt.Errorf("read GitHub README: %w", err)
	}
	return Readme{Content: string(content), MediaType: response.Header.Get("X-GitHub-Media-Type")}, meta, nil
}

func acceptHeader(options requestOptions) string {
	if options.accept != "" {
		return options.accept
	}
	return "application/vnd.github+json"
}
