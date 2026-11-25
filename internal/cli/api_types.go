package cli

import (
	"encoding/json"
	"time"
)

// These mirror docs/interfaces/api.md exactly (field names and JSON tags
// match internal/fakes/api's Project et al. so the CLI decodes the fake
// and the real api identically).

type Me struct {
	// Token is set when the call was made with an API token (I-1314).
	Token       *APIToken `json:"token,omitempty"`
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
		// GChat is the Google Chat channel, never its URL (I-692).
		GChat *GChatChannel `json:"gchat"`
		// HoldSeconds is how long a report waits while you are at the
		// machine (I-1282); 0 is off.
		HoldSeconds int `json:"hold_seconds"`
	} `json:"notify"`
	// GitHub is what the account says about GitHub (I-1340, I-1342):
	// whether a laptop may copy its GitHub CLI login, and what GitHub
	// said about the GH_TOKEN it holds. A pointer because a server from
	// before I-1340 sends no `github` at all, which must read as "copy
	// as before" and not as the zero value's refusal.
	GitHub *GitHubState `json:"github"`
}

// GitHubState is GET /me's github.
type GitHubState struct {
	LoginCopy      bool       `json:"login_copy"`
	TokenExpiresAt *time.Time `json:"token_expires_at"`
}

// ghLoginCopy is whether the account lets a laptop copy its GitHub CLI
// login (I-1340). An account the CLI has not read, and a server that
// does not answer, both mean yes: this field can only ever subtract.
func (m *Me) ghLoginCopy() bool {
	return m == nil || m.GitHub == nil || m.GitHub.LoginCopy
}

type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	RemoteURL string `json:"remote_url"`
	Class     string `json:"class"`
	State     string `json:"state"`
	OpID      string `json:"op_id,omitempty"` // the op a create or start left in flight
	HostID    string `json:"host_id,omitempty"`
	// HostName is the name the account gave the host the machine is on,
	// set only for a host of the account's own (I-1392); a host repose
	// runs has host_id alone. Absent from an older api.
	HostName string `json:"host_name,omitempty"`
	// Placement is what the project asked for (I-1366): `own`, a host
	// the account owns when it owns one, or `hosted`, repose's servers.
	// Absent from an older api, which the CLI reads as own.
	Placement string `json:"placement,omitempty"`
	// Sized is false for a machine on a host of the account's own, which
	// shares that host's memory and cores and has no size (DECISIONS
	// I-1630, I-1632): Class says nothing there. Absent from an older
	// api, which means sized.
	Sized *bool `json:"sized,omitempty"`
	// MemUsedBytes is the memory the running machine uses, as it sees it,
	// from the newest sample (I-1632); 0 when the api did not say.
	MemUsedBytes     int64      `json:"mem_used_bytes,omitempty"`
	GuestIP          string     `json:"guest_ip,omitempty"`
	AgentDefault     string     `json:"agent_default"`
	HoldBaseUpdates  bool       `json:"hold_base_updates"`
	BaseVersion      string     `json:"base_version"`
	ConfigRevisionID string     `json:"config_revision_id"`
	VolumeBytes      int64      `json:"volume_bytes"`
	DiskUsedBytes    int64      `json:"disk_used_bytes,omitempty"` // the volume's allocated blocks; not shown (I-567)
	RootUsedBytes    int64      `json:"root_used_bytes,omitempty"` // the machine's root filesystem, newest sample (I-567)
	RootSizeBytes    int64      `json:"root_size_bytes,omitempty"`
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

	// GuestRuntime is what the machine boots (DECISIONS I-1462): `nix`,
	// or `erofs`, the Ubuntu base configured by the menu alone. Absent
	// from an older api, which the CLI reads as nix.
	GuestRuntime string `json:"guest_runtime,omitempty"`
	// PendingBaseVersion is the newer base an erofs machine boots at its
	// next start (I-1462); null otherwise and from an older api.
	PendingBaseVersion *string `json:"pending_base_version,omitempty"`
	// Checkout is the checkout's name under /home/dev as a CLI last told
	// the api (I-1610): "" for none, nil until one has, and from an older
	// api.
	Checkout *string `json:"checkout,omitempty"`
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
	// Resting is set while the api has frozen the machine: its vCPUs
	// are stopped after it went quiet, and every process, open file
	// and all of its memory stay as they were (DECISIONS I-818). The
	// state stays `running`; the word a user reads is "frozen"
	// (I-826). Absent from older apis, which the CLI reads as "not
	// frozen".
	Resting *ProjectResting `json:"resting,omitempty"`
	// KeepAwake is set while the project is held awake from its Config
	// page: the api never freezes it (DECISIONS I-820). Set there, as
	// hold_base_updates is, with no CLI flag.
	KeepAwake bool `json:"keep_awake"`
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

	// waitedSince is, per agent window, when its current wait for an
	// answer began, read from the project's events by the CLI (I-634);
	// never sent or printed as JSON.
	waitedSince map[string]time.Time
}

// ProjectIdle is Project.idle.
type ProjectIdle struct {
	Since       time.Time `json:"since"`
	HourlyCents int64     `json:"hourly_cents"`
}

// ProjectResting is Project.resting: when the machine was frozen.
type ProjectResting struct {
	Since time.Time `json:"since"`
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
	// CreatedAt and FinishedAt are when the op was queued and ended (nil
	// while it runs, or from an api that leaves them out).
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
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
	// Project and ProjectID are set only by GET /v1/snapshots, the
	// account-wide list (I-904); the per-project route has no need of
	// them and leaves them empty.
	ProjectID        string `json:"project_id,omitempty"`
	Project          string `json:"project,omitempty"`
	ProjectDestroyed bool   `json:"project_destroyed,omitempty"`
	// GoneWithHost names the account's own host the snapshot was kept
	// on when that host was removed, which deleted it (I-1542).
	GoneWithHost *string `json:"gone_with_host,omitempty"`
}

// EventLink is the link an agent attached to an event or a question.
type EventLink struct {
	URL   string `json:"url"`
	Label string `json:"label,omitempty"`
}

type Event struct {
	ID      string    `json:"id"`
	TS      time.Time `json:"ts"`
	Kind    string    `json:"kind"`
	Agent   string    `json:"agent,omitempty"`
	Window  string    `json:"window,omitempty"`
	Summary string    `json:"summary"`
	// Link is the agent's `--link URL --label TEXT` (DECISIONS I-931).
	Link *EventLink `json:"link,omitempty"`
	// Delivered is each channel's outcome: a send time, "held" while the
	// presence hold has it (I-689), "dropped: resolved", or a failure.
	// `repose events` says so only when nothing went out (I-1280).
	Delivered map[string]string `json:"delivered,omitempty"`
	// Project is the project's name, which the CLI adds (the api answers
	// for one project); `repose events` outside a checkout covers them
	// all (DECISIONS I-609).
	Project string `json:"project,omitempty"`
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

// NotifyTestResult is POST /me/notify-test's answer: "ok" or "error" per
// channel that is on, absent for one that is off, and why ntfy failed.
type NotifyTestResult struct {
	Email      string `json:"email"`
	Ntfy       string `json:"ntfy"`
	NtfyError  string `json:"ntfy_error"`
	GChat      string `json:"gchat"`
	GChatError string `json:"gchat_error"`
}

// GChatChannel is GET /me's notify.gchat: the webhook's space, and when
// Google refused it.
type GChatChannel struct {
	Space    string     `json:"space"`
	BrokenAt *time.Time `json:"broken_at"`
}
