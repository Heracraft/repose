package cli

import (
	"encoding/json"
	"time"
)

// These mirror docs/interfaces/api.md exactly (field names and JSON tags
// match internal/fakes/api's Project et al. so the CLI decodes the fake
// and the real api identically).

type Me struct {
	ID          string    `json:"id"`
	Handle      string    `json:"handle"`
	Email       string    `json:"email"`
	GitHubLogin string    `json:"github_login"`
	TZ          string    `json:"tz"`
	CreatedAt   time.Time `json:"created_at"`
	Billing     struct {
		Status           string     `json:"status"`
		Plan             *string    `json:"plan"`
		Seats            int        `json:"seats"`
		PeriodEnd        *time.Time `json:"period_end"`
		TrialEnd         *time.Time `json:"trial_end"`
		CancelAt         *time.Time `json:"cancel_at"`
		TrialCreditCents int64      `json:"trial_credit_cents"`
		HasCard          bool       `json:"has_card"`
	} `json:"billing"`
	Limits struct {
		Projects int `json:"projects"`
		XL       int `json:"xl"`
		MemoryGB int `json:"memory_gb"`
		DiskGB   int `json:"disk_gb"`
		EgressGB int `json:"egress_gb"`
	} `json:"limits"`
	Notify struct {
		Email   bool    `json:"email"`
		NtfyURL *string `json:"ntfy_url"`
	} `json:"notify"`
}

type Project struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Slug             string     `json:"slug"`
	RemoteURL        string     `json:"remote_url"`
	Class            string     `json:"class"`
	State            string     `json:"state"`
	OpID             string     `json:"op_id,omitempty"` // the op a create or start left in flight
	HostID           string     `json:"host_id,omitempty"`
	GuestIP          string     `json:"guest_ip,omitempty"`
	AgentDefault     string     `json:"agent_default"`
	HoldBaseUpdates  bool       `json:"hold_base_updates"`
	BaseVersion      string     `json:"base_version"`
	ConfigRevisionID string     `json:"config_revision_id"`
	VolumeBytes      int64      `json:"volume_bytes"`
	DiskUsedBytes    int64      `json:"disk_used_bytes,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	Signals          *Signals   `json:"signals,omitempty"`
	// CostTodayCents and CostMonthCents are 0 since I-289 and go after one
	// release; RunningSecondsToday and RunningSecondsMonth are what
	// `repose status` shows (absent from an older api, so 0).
	CostTodayCents      int64      `json:"cost_today_cents"`
	CostMonthCents      int64      `json:"cost_month_cents"`
	RunningSecondsToday int64      `json:"running_seconds_today,omitempty"`
	RunningSecondsMonth int64      `json:"running_seconds_month,omitempty"`
	LastSnapshotAt      *time.Time `json:"last_snapshot_at,omitempty"`
	// LastError is the api's "code: message" for the op that last failed
	// (the ops engine writes it; cleared by a successful start), and
	// HostUnreachable its flag for a host that stopped answering. Both are
	// what the CLI reads to say why a project is in `error` (I-153);
	// absent from older api builds, which the CLI treats as "no reason".
	LastError       *string `json:"last_error,omitempty"`
	HostUnreachable bool    `json:"host_unreachable,omitempty"`
	// TZ is the zone the guest gets at its next start; the CLI moves it
	// to the laptop's when they differ (I-198). Absent from older apis.
	TZ *string `json:"tz,omitempty"`
	// Idle is set while the project has run for a day with no SSH
	// session and no agent working (DECISIONS I-262). Absent from older
	// apis, which the CLI treats as "not idle".
	Idle *ProjectIdle `json:"idle,omitempty"`
	// PersonalOptOut is set when the project keeps the account's
	// machine.nix off (DECISIONS I-490). Absent from older apis.
	PersonalOptOut bool `json:"personal_opt_out,omitempty"`
	// ExpiresAt is set while the project is temporary (DECISIONS I-347):
	// the api destroys it, with no snapshot, once this has passed. Absent
	// from older apis and on every normal project.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// Multiplexer is what runs the machine's terminals from its next
	// start, tmux or herdr (DECISIONS I-502). Absent from older apis,
	// which multiplexer.Normalize reads as tmux. What a running machine
	// runs now is the guest's answer (muxFor), not this.
	Multiplexer string `json:"multiplexer,omitempty"`
}

// ProjectIdle is Project.idle.
type ProjectIdle struct {
	Since       time.Time `json:"since"`
	HourlyCents int64     `json:"hourly_cents"`
}

type Signals struct {
	SSHSessions int           `json:"ssh_sessions"`
	TmuxClients int           `json:"tmux_clients"`
	Docker      int           `json:"docker"`
	Agents      []AgentSignal `json:"agents"`
	// DockerContainers is the name the api actually sends (Docker's
	// "docker" never arrived, so status printed "docker 0" for everyone);
	// both are kept so --json output does not lose a key.
	DockerContainers int `json:"docker_containers,omitempty"`
	// GuestdOK is the newest sample's word on guestd; nil when the api
	// did not say. A running project with false is one `repose start`
	// restarts (I-157).
	GuestdOK *bool `json:"guestd_ok,omitempty"`
	// SampledAt is when the host took the sample the signals are from.
	SampledAt *time.Time `json:"sampled_at,omitempty"`
}

type AgentSignal struct {
	Agent  string `json:"agent"`
	Window string `json:"window"`
	State  string `json:"state"`
}

type Op struct {
	State  string  `json:"state"`
	Error  OpError `json:"error,omitempty"`
	LogURL string  `json:"log_url,omitempty"`
	// Version, Phase and ProjectState come from an api with the op
	// long-poll (I-236); an older api leaves them empty. Version is
	// opaque: it changes when the op's state or phase or the project's
	// state does, and goes back as ?seen=.
	Version      string `json:"version,omitempty"`
	Phase        string `json:"phase,omitempty"`
	ProjectState string `json:"project_state,omitempty"`
	// RebootRequired is set on a config op whose revision changes the
	// kernel of a running machine: built, not switched to.
	RebootRequired bool `json:"reboot_required,omitempty"`
	// Result is a finished op's result; a snapshot's is {snapshot_id}.
	Result map[string]any `json:"result,omitempty"`
}

// OpError is an op's error as the api stores it: `{code, message}` (the
// hostd Result's error, or the api's own `invalid`), which an older
// reading as a bare string rendered as nothing (DECISIONS I-114). A bare
// string is still accepted.
type OpError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	// Detail is the host's own wording, for operators (I-159); the CLI
	// shows it only under -v.
	Detail any `json:"detail,omitempty"`
}

func (e *OpError) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*e = OpError{}
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*e = OpError{Message: s}
		return nil
	}
	type raw OpError
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*e = OpError(r)
	return nil
}

// String is the message alone; the code chooses a prefix only where the
// build contract names one (RenderBuildError).
func (e OpError) String() string { return e.Message }

type CertResponse struct {
	Certificate string    `json:"certificate"`
	ExpiresAt   time.Time `json:"expires_at"`
	Gateway     struct {
		Host      string `json:"host"`
		Port      int    `json:"port"`
		HostCAPub string `json:"host_ca_pub"`
	} `json:"gateway"`
}

type SecretMeta struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Revision struct {
	ID             string    `json:"revision_id"`
	CreatedAt      time.Time `json:"created_at"`
	Status         string    `json:"status"`
	Error          string    `json:"error,omitempty"`
	FragmentLine   *int      `json:"fragment_line,omitempty"`
	PersonalLine   *int      `json:"personal_line,omitempty"`
	Personal       bool      `json:"personal,omitempty"`
	KernelChanged  bool      `json:"kernel_changed,omitempty"`
	RebootRequired bool      `json:"reboot_required,omitempty"`
}

type ConfigResponse struct {
	RevisionID string `json:"revision_id"`
	Fragment   string `json:"fragment"`
	// Personal is the machine.nix text the active revision carries,
	// "" for none or from an older api (DECISIONS I-490).
	Personal    string     `json:"personal,omitempty"`
	Menu        any        `json:"menu,omitempty"`
	BaseVersion string     `json:"base_version"`
	AppliedAt   *time.Time `json:"applied_at,omitempty"`
}

type Snapshot struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	Bytes     int64      `json:"bytes"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type Event struct {
	ID      string    `json:"id"`
	TS      time.Time `json:"ts"`
	Kind    string    `json:"kind"`
	Agent   string    `json:"agent,omitempty"`
	Summary string    `json:"summary"`
}

type CatalogItem struct {
	ID          string          `json:"id"`
	Label       string          `json:"label"`
	Group       string          `json:"group"`
	Kind        string          `json:"kind"`
	Description string          `json:"description"`
	Options     []CatalogOption `json:"options,omitempty"`
}

type CatalogOption struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Values  []string `json:"values"`
	Default string   `json:"default"`
}

type Route struct {
	HostID string `json:"host_id"`
	// HostName is the host's name ("host-01"); the api has returned it
	// since M2 and status shows it instead of the id (I-192).
	HostName string `json:"host_name,omitempty"`
	GuestIP  string `json:"guest_ip"`
	State    string `json:"state"`
}

type NotifyTestResult struct {
	Email string `json:"email"`
	Ntfy  string `json:"ntfy"`
}
