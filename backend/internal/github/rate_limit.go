package github

import (
	"context"
	"time"
)

type RateLimitResource struct {
	Limit     int   `json:"limit"`
	Used      int   `json:"used"`
	Remaining int   `json:"remaining"`
	Reset     int64 `json:"reset"`
}

func (resource RateLimitResource) ResetAt() time.Time {
	if resource.Reset <= 0 {
		return time.Time{}
	}
	return time.Unix(resource.Reset, 0).UTC()
}

type RateLimitResources struct {
	Core   RateLimitResource `json:"core"`
	Search RateLimitResource `json:"search"`
}

type RateLimitStatus struct {
	Resources RateLimitResources `json:"resources"`
}

func (client *Client) GetRateLimitStatus(ctx context.Context) (RateLimitStatus, ResponseMeta, error) {
	var status RateLimitStatus
	meta, err := client.getJSON(ctx, "/rate_limit", nil, &status)
	return status, meta, err
}
