package github

import "time"

const (
	APIVersion       = "2026-03-10"
	defaultUserAgent = "github-radar/0.2"
)

type License struct {
	SPDXID string `json:"spdx_id"`
}

type Owner struct {
	Login string `json:"login"`
}

type Repository struct {
	ID              int64      `json:"id"`
	Owner           Owner      `json:"owner"`
	Name            string     `json:"name"`
	FullName        string     `json:"full_name"`
	HTMLURL         string     `json:"html_url"`
	Homepage        *string    `json:"homepage"`
	Description     *string    `json:"description"`
	Language        *string    `json:"language"`
	License         *License   `json:"license"`
	DefaultBranch   string     `json:"default_branch"`
	Topics          []string   `json:"topics"`
	StargazersCount int        `json:"stargazers_count"`
	ForksCount      int        `json:"forks_count"`
	OpenIssuesCount int        `json:"open_issues_count"`
	Fork            bool       `json:"fork"`
	Private         bool       `json:"private"`
	Visibility      string     `json:"visibility"`
	Archived        bool       `json:"archived"`
	Disabled        bool       `json:"disabled"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	PushedAt        *time.Time `json:"pushed_at"`
}

type SearchResult struct {
	TotalCount        int          `json:"total_count"`
	IncompleteResults bool         `json:"incomplete_results"`
	Items             []Repository `json:"items"`
}

type RateLimit struct {
	Resource  string
	Limit     *int
	Used      *int
	Remaining *int
	ResetAt   *time.Time
}

type ResponseMeta struct {
	StatusCode  int
	ETag        string
	NextURL     string
	NotModified bool
	RateLimit   RateLimit
}
