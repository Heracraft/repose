package cli

import (
	"context"
	"net/url"
	"time"
)

// API tokens (DECISIONS I-1314..I-1320): `GET/POST/DELETE /me/tokens`,
// the one place a token is minted, and `REPOSE_TOKEN`, the one place the
// CLI reads one.

// APIToken is a row of GET /me/tokens: everything but the secret.
type APIToken struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Prefix     string        `json:"prefix"`
	Scopes     []string      `json:"scopes"`
	Project    *TokenProject `json:"project"`
	ExpiresAt  time.Time     `json:"expires_at"`
	CreatedAt  time.Time     `json:"created_at"`
	LastUsedAt *time.Time    `json:"last_used_at"`
	RevokedAt  *time.Time    `json:"revoked_at"`
	// Token is set on the create answer only.
	Token string `json:"token,omitempty"`
}

// TokenProject names the one project a token is bound to.
type TokenProject struct {
	ID   string  `json:"id"`
	Slug *string `json:"slug"`
}

// CreateTokenRequest is POST /me/tokens.
type CreateTokenRequest struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ProjectID     string   `json:"project_id,omitempty"`
	ExpiresInDays int      `json:"expires_in_days,omitempty"`
}

func (c *Client) ListTokens(ctx context.Context) ([]APIToken, error) {
	out := []APIToken{}
	if err := c.get(ctx, "/me/tokens", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) CreateToken(ctx context.Context, req CreateTokenRequest) (*APIToken, error) {
	var out APIToken
	if err := c.post(ctx, "/me/tokens", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteToken(ctx context.Context, name string) error {
	return c.delete(ctx, "/me/tokens/"+url.PathEscape(name), nil)
}
