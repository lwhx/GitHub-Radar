package github

import "context"

type AuthenticatedUser struct {
	ID        int64   `json:"id"`
	Login     string  `json:"login"`
	Name      *string `json:"name"`
	Type      string  `json:"type"`
	SiteAdmin bool    `json:"site_admin"`
}

func (client *Client) GetAuthenticatedUser(ctx context.Context) (AuthenticatedUser, ResponseMeta, error) {
	var user AuthenticatedUser
	meta, err := client.getJSON(ctx, "/user", nil, &user)
	return user, meta, err
}
