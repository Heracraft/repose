package cli

import (
	"context"
	"net/url"
	"time"
)

// The webhook routes of docs/interfaces/api.md (DECISIONS I-1110).

// WebhookDelivery is one record of what a provider's call did.
type WebhookDelivery struct {
	At         time.Time `json:"at"`
	Status     int       `json:"status"`
	Outcome    string    `json:"outcome"`
	DurationMs int       `json:"duration_ms"`
	BytesIn    int64     `json:"bytes_in"`
	BytesOut   int64     `json:"bytes_out"`
}

// WebhookRoute is one route of a project, with its public URL.
type WebhookRoute struct {
	RouteID    string            `json:"route_id"`
	Name       string            `json:"name"`
	Port       int               `json:"port"`
	Path       string            `json:"path"`
	AllowStart bool              `json:"allow_start"`
	URL        string            `json:"url"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Last       *WebhookDelivery  `json:"last_delivery,omitempty"`
	Deliveries []WebhookDelivery `json:"deliveries,omitempty"`
}

func (c *Client) ListWebhooks(ctx context.Context, id string) ([]WebhookRoute, error) {
	out := []WebhookRoute{}
	if err := c.get(ctx, "/projects/"+url.PathEscape(id)+"/webhooks", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) GetWebhook(ctx context.Context, id, name string) (*WebhookRoute, error) {
	var out WebhookRoute
	if err := c.get(ctx, "/projects/"+url.PathEscape(id)+"/webhooks/"+url.PathEscape(name), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutWebhook adds a route or changes an existing one's target. start nil
// leaves --start as it is.
func (c *Client) PutWebhook(ctx context.Context, id, name, target string, start *bool) (*WebhookRoute, error) {
	body := map[string]any{"name": name, "target": target}
	if start != nil {
		body["allow_start"] = *start
	}
	var out WebhookRoute
	if err := c.post(ctx, "/projects/"+url.PathEscape(id)+"/webhooks", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RotateWebhook(ctx context.Context, id, name string) (*WebhookRoute, error) {
	var out WebhookRoute
	if err := c.post(ctx, "/projects/"+url.PathEscape(id)+"/webhooks/"+url.PathEscape(name)+"/rotate", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteWebhook(ctx context.Context, id, name string) error {
	return c.delete(ctx, "/projects/"+url.PathEscape(id)+"/webhooks/"+url.PathEscape(name), nil)
}
