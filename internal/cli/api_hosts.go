package cli

import (
	"context"
	"net/url"
	"time"
)

// Hosts the user owns (DECISIONS I-1366, I-1370): `GET /hosts`,
// `POST /hosts` and `DELETE /hosts/{id}`. A host belongs to one account,
// runs only that account's machines, and joins with a single-use token
// the api mints per `POST /hosts`.

// Host is a row of GET /hosts.
type Host struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	// Provider is where it runs (`mac`, `metal`); Arch its machine
	// architecture (`aarch64`, `x86_64`). Both are what the host said at
	// registration, and both are empty until then.
	Provider string `json:"provider,omitempty"`
	Arch     string `json:"arch,omitempty"`
	// MemBytes, VCPUs and Cores are the hardware as it registered; 0
	// before that.
	MemBytes int64 `json:"mem_bytes"`
	VCPUs    int   `json:"vcpus"`
	Cores    int   `json:"cores"`
	// Projects is how many of the account's live machines are placed on
	// it; RunningProjects how many of those hold memory right now.
	Projects        int        `json:"projects"`
	RunningProjects int        `json:"running_projects"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at"`
	RegisteredAt    *time.Time `json:"registered_at"`
	CreatedAt       time.Time  `json:"created_at"`
	// JoinToken and Install are set on the create answer alone: the
	// token is single-use and is never shown again.
	JoinToken          string     `json:"join_token,omitempty"`
	JoinTokenExpiresAt *time.Time `json:"join_token_expires_at,omitempty"`
	Install            *Install   `json:"install,omitempty"`
}

// Install is the step the user runs on the machine itself. The api
// composes it, token included, so the install path can change without a
// CLI release. Command is empty while no install line is recorded for
// the host's kind, and then the token and URL are all there is to give.
type Install struct {
	Command string `json:"command"`
	URL     string `json:"url"`
}

// CreateHostRequest is POST /hosts. An empty Name lets the api name it.
type CreateHostRequest struct {
	Name string `json:"name,omitempty"`
}

func (c *Client) ListHosts(ctx context.Context) ([]Host, error) {
	out := []Host{}
	if err := c.get(ctx, "/hosts", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) CreateHost(ctx context.Context, req CreateHostRequest) (*Host, error) {
	var out Host
	if err := c.post(ctx, "/hosts", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteHost(ctx context.Context, id string) error {
	return c.delete(ctx, "/hosts/"+url.PathEscape(id), nil)
}
