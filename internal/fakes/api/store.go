package api

import (
	"encoding/json"
	"time"
)

// User is one account the fake knows. Options.Users maps bearer tokens to
// these; the zero Options has a single canned user.
type User struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	Email       string `json:"email"`
	GitHubLogin string `json:"github_login"`
}

// Project is the documented Project object.
type Project struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Slug             string     `json:"slug"`
	RemoteURL        string     `json:"remote_url"`
	Class            string     `json:"class"`
	State            string     `json:"state"`
	OpID             string     `json:"op_id,omitempty"` // the create op, while one is in flight
	HostID           string     `json:"host_id,omitempty"`
	GuestIP          string     `json:"guest_ip,omitempty"`
	AgentDefault     string     `json:"agent_default"`
	HoldBaseUpdates  bool       `json:"hold_base_updates"`
	BaseVersion      string     `json:"base_version"`
	ConfigRevisionID string     `json:"config_revision_id"`
	VolumeBytes      int64      `json:"volume_bytes"`
	DiskUsedBytes    int64      `json:"disk_used_bytes,omitempty"`
	RootUsedBytes    int64      `json:"root_used_bytes,omitempty"` // the guest's root filesystem (I-567)
	RootSizeBytes    int64      `json:"root_size_bytes,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	Signals          *Signals   `json:"signals,omitempty"`
	CostTodayCents   int64      `json:"cost_today_cents"`
	CostMonthCents   int64      `json:"cost_month_cents"`
	LastSnapshotAt   *time.Time `json:"last_snapshot_at,omitempty"`
	LastError        *string    `json:"last_error"`
	HostUnreachable  bool       `json:"host_unreachable"`
	TZ               *string    `json:"tz"`
	// Idle is set by a test to stand for a running project nobody has used
	// for a day (DECISIONS I-262).
	Idle *Idle `json:"idle,omitempty"`
	// ExpiresAt is set on a temporary project (DECISIONS I-347).
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// PersonalOptOut keeps the account's machine.nix off (I-490).
	PersonalOptOut bool `json:"personal_opt_out"`
	// Multiplexer is what the next start runs, tmux or herdr (I-502).
	Multiplexer string `json:"multiplexer"`
}

// Idle is Project.idle.
type Idle struct {
	Since       time.Time `json:"since"`
	HourlyCents int64     `json:"hourly_cents"`
}

// DestroyedProject is one row of GET /projects/destroyed (I-167).
type DestroyedProject struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Slug            string     `json:"slug"`
	Class           string     `json:"class"`
	RemoteURL       string     `json:"remote_url,omitempty"`
	VolumeBytes     int64      `json:"volume_bytes"`
	DestroyedAt     time.Time  `json:"destroyed_at"`
	NameFree        bool       `json:"name_free"`
	RestorableUntil *time.Time `json:"restorable_until"`
	Snapshot        Snapshot   `json:"snapshot"`
}

// Signals is Project.signals.
type Signals struct {
	SSHSessions int           `json:"ssh_sessions"`
	TmuxClients int           `json:"tmux_clients"`
	Agents      []AgentSignal `json:"agents"`
	GuestdOK    bool          `json:"guestd_ok"`
}

// AgentSignal is one entry of Signals.Agents.
type AgentSignal struct {
	Agent  string `json:"agent"`
	Window string `json:"window"`
	State  string `json:"state"`
}

// Snapshot is one row of GET /projects/:id/snapshots.
type Snapshot struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	Bytes     int64      `json:"bytes"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Event is one row of GET /projects/:id/events.
type Event struct {
	ID      string    `json:"id"`
	TS      time.Time `json:"ts"`
	Kind    string    `json:"kind"`
	Agent   string    `json:"agent,omitempty"`
	Summary string    `json:"summary"`
}

// SecretMeta is one row of GET /projects/:id/secrets. Values are never
// kept, let alone returned.
type SecretMeta struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Revision is one config revision.
type Revision struct {
	ID          string          `json:"revision_id"`
	CreatedAt   time.Time       `json:"created_at"`
	Status      string          `json:"status"`
	Error       string          `json:"error,omitempty"`
	Fragment    string          `json:"-"`
	Menu        json.RawMessage `json:"-"`
	BaseVersion string          `json:"-"`
	AppliedAt   *time.Time      `json:"-"`
	// Personal says the revision carries machine.nix (I-490).
	Personal     bool   `json:"personal"`
	personalText string // what GET /config returns as personal
}

// CatalogItem is one row of GET /catalog. Kind and Options were added to
// api.md by DECISIONS I-44 (internal/menu); this fake predated that entry
// and is brought in line with it here rather than in a new decision, since
// the interface doc was already right.
type CatalogItem struct {
	ID          string          `json:"id"`
	Label       string          `json:"label"`
	Group       string          `json:"group"`
	Kind        string          `json:"kind"`
	Description string          `json:"description"`
	Options     []CatalogOption `json:"options,omitempty"`
}

// CatalogOption is one entry of CatalogItem.Options.
type CatalogOption struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Values  []string `json:"values"`
	Default string   `json:"default"`
}

// Op is GET /projects/:id/ops/:op_id.
type Op struct {
	State  string `json:"state"`
	Error  string `json:"error,omitempty"`
	LogURL string `json:"log_url,omitempty"`
	// Version, Phase and ProjectState are the long-poll's fields (I-236),
	// filled per read; Options.NoLongPoll leaves them out.
	Version      string `json:"version,omitempty"`
	Phase        string `json:"phase,omitempty"`
	ProjectState string `json:"project_state,omitempty"`
	// Result is a finished op's result: `{snapshot_id}` for a snapshot.
	Result map[string]any `json:"result,omitempty"`
}

// forkRec is one project a fork request made, kept to answer its resend.
type forkRec struct {
	projectID, name, class, opID string
}

// Host is one row of GET /internal/hosts.
type Host struct {
	HostID    string `json:"host_id"`
	WGPubkey  string `json:"wg_pubkey"`
	WGIP      string `json:"wg_ip"`
	GuestCIDR string `json:"guest_cidr"`
	State     string `json:"state"`
}

// UsageRow is one row of GET /usage.
type UsageRow struct {
	ProjectID  string           `json:"project_id"`
	Day        string           `json:"day"`
	GuestHours map[string]int64 `json:"guest_hours"`
	GBMonths   float64          `json:"gb_months"`
	EgressGB   float64          `json:"egress_gb"`
	CostCents  int64            `json:"cost_cents"`
}

type userRec struct {
	User
	TZ          string
	CreatedAt   time.Time
	NotifyEmail bool
	NtfyURL     string
	Cancelling  bool
	// personal is the account's machine.nix saves, oldest first (I-490).
	personal []*personalRev
}

type project struct {
	Project
	owner       string
	destroyed   bool
	destroyedAt time.Time
	retained    time.Time // destroyed projects keep their last snapshot until then
	secrets     map[string]*SecretMeta
	revisions   []*Revision
	snapshots   []*Snapshot
	events      []*Event
	eventKeys   map[string]bool // (agent, kind, ts second) dedupe for /internal/events
}

type op struct {
	Op
	id        string
	projectID string
	kind      string
	phase     string // while running
	created   time.Time
}

type cert struct {
	serial     uint64
	owner      string
	projectIDs []string
	expiresAt  time.Time
	revoked    bool
}

type revocation struct {
	serial uint64
	at     time.Time
}
