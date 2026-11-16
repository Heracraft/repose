package gateway

import (
	"context"
	"net/http"
	"net/url"

	"github.com/heracraft/repose/internal/gateway/webhook"
)

// The webhook side of the internal api (DECISIONS I-1110). *Client
// satisfies webhook.API, so the handler depends on this package's client
// without this package depending on the handler.

// WebhookRoutes fetches the route table. since is the version the edge
// holds; an unchanged table comes back with Unchanged set and no rows.
func (c *Client) WebhookRoutes(ctx context.Context, since string) (*webhook.RouteTable, error) {
	path := "/webhook-routes"
	if since != "" {
		path += "?since=" + url.QueryEscape(since)
	}
	var out webhook.RouteTable
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProjectState is GET /internal/route?project_id=: the same answer the
// login form gives, for a delivery that knows the project and no login.
func (c *Client) ProjectState(ctx context.Context, projectID string) (*webhook.ProjectState, error) {
	var out webhook.ProjectState
	if err := c.do(ctx, http.MethodGet, "/route?project_id="+url.QueryEscape(projectID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WebhookWake resumes a frozen machine for a delivery. The reason word
// is `webhook`, so a host's log line says which kind of traffic woke it
// (amends I-818).
func (c *Client) WebhookWake(ctx context.Context, projectID string) error {
	wctx, cancel := context.WithTimeout(ctx, webhook.WakeBudget)
	defer cancel()
	_, err := c.Wake(wctx, projectID, "webhook")
	return err
}

// WebhookStart asks the api to start a stopped machine for a delivery on
// a route with --start. started false with no error is the api's refusal
// or its once-per-10-minutes rule, not a failure.
func (c *Client) WebhookStart(ctx context.Context, projectID, routeID string) (bool, string, error) {
	var out struct {
		Started bool   `json:"started"`
		Reason  string `json:"reason"`
	}
	body := map[string]string{"project_id": projectID, "route_id": routeID}
	if err := c.do(ctx, http.MethodPost, "/webhook-start", body, &out); err != nil {
		return false, "", err
	}
	return out.Started, out.Reason, nil
}

// ReportWebhookDeliveries sends a batch of delivery records.
func (c *Client) ReportWebhookDeliveries(ctx context.Context, batch []webhook.Delivery) error {
	return c.do(ctx, http.MethodPost, "/webhook-deliveries", map[string]any{"deliveries": batch}, nil)
}
