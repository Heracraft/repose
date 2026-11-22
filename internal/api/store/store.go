// Package store holds the row types and typed queries the api's packages
// share. Column names follow docs/interfaces/db-schema.md; rows are
// scanned by name so a query lists exactly the columns of the struct.
package store

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/heracraft/repose/internal/db"
)

// Querier is satisfied by *pgxpool.Pool and pgx.Tx.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// User is a users row.
type User struct {
	ID          uuid.UUID `db:"id"`
	LogtoSub    *string   `db:"logto_sub"`
	Handle      string    `db:"handle"`
	Email       *string   `db:"email"`
	GithubLogin *string   `db:"github_login"`
	TZ          *string   `db:"tz"`
	NotifyEmail bool      `db:"notify_email"`
	NtfyURL     *string   `db:"ntfy_url"`
	// GChatURL is the Google Chat webhook, a credential the api never
	// returns; GChatBrokenAt is when Google first refused it (0023, I-692).
	GChatURL      *string    `db:"gchat_url"`
	GChatBrokenAt *time.Time `db:"gchat_broken_at"`
	// NotifyHoldSeconds is how long a report waits while the owner is at
	// the machine (0045, I-1282); 0 turns the hold off.
	NotifyHoldSeconds int `db:"notify_hold_seconds"`
	// GhLoginCopy is whether a laptop may copy this account's GitHub CLI
	// login into its machines (0047, I-1340). A refusal only: a laptop
	// that has turned the copy off keeps it off whatever this says.
	GhLoginCopy bool `db:"gh_login_copy"`
	// GhTokenExpiresAt is what GitHub said about the account's GH_TOKEN
	// when it was stored (0047, I-1342), or nil when it said nothing.
	GhTokenExpiresAt *time.Time `db:"gh_token_expires_at"`
	// BillingCustomerID is the Polar customer, which Polar creates when
	// the first checkout completes (0008, 0020, I-289, I-604). BillingAnchor is unused since I-289 (the period is the
	// subscription's); PastDueSince is when the account first failed a
	// payment, which the 3-day stop reads. BillingStatus is a projection
	// of the subscription: none|trial|active|past_due|suspended|exempt.
	BillingCustomerID *string    `db:"billing_customer_id"`
	BillingAnchor     *time.Time `db:"billing_anchor"`
	PastDueSince      *time.Time `db:"past_due_since"`
	BillingStatus     string     `db:"billing_status"`
	HasCard           bool       `db:"has_card"`
	TrialCreditCents  int64      `db:"trial_credit_cents"`
	ProjectLimit      int        `db:"project_limit"`
	XLLimit           int        `db:"xl_limit"`
	SuspendedAt       *time.Time `db:"suspended_at"`
	SuspendedReason   *string    `db:"suspended_reason"`
	CancelledAt       *time.Time `db:"cancelled_at"`
	DeletedAt         *time.Time `db:"deleted_at"`
	CreatedAt         time.Time  `db:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at"`
}

const userCols = `id, logto_sub, handle, email, github_login, tz, notify_email, ntfy_url, gchat_url, gchat_broken_at, notify_hold_seconds, gh_login_copy, gh_token_expires_at, billing_customer_id, billing_anchor, past_due_since, billing_status, has_card, trial_credit_cents, project_limit, xl_limit, suspended_at, suspended_reason, cancelled_at, deleted_at, created_at, updated_at`

// Project is a projects row.
type Project struct {
	ID               uuid.UUID   `db:"id"`
	UserID           uuid.UUID   `db:"user_id"`
	Name             string      `db:"name"`
	Slug             string      `db:"slug"`
	RemoteURL        *string     `db:"remote_url"`
	Class            string      `db:"class"`
	State            string      `db:"state"`
	HostID           *uuid.UUID  `db:"host_id"`
	GuestID          *uuid.UUID  `db:"guest_id"`
	GuestIP          *netip.Addr `db:"guest_ip"`
	VsockCID         *int32      `db:"vsock_cid"`
	AgentDefault     string      `db:"agent_default"`
	HoldBaseUpdates  bool        `db:"hold_base_updates"`
	BaseVersion      *string     `db:"base_version"`
	ConfigRevisionID *uuid.UUID  `db:"config_revision_id"`
	VolumeBytes      int64       `db:"volume_bytes"`
	TZ               *string     `db:"tz"`
	HostUnreachable  bool        `db:"host_unreachable"`
	LastError        *string     `db:"last_error"`
	StartedAt        *time.Time  `db:"started_at"`
	StoppedAt        *time.Time  `db:"stopped_at"`
	DestroyedAt      *time.Time  `db:"destroyed_at"`
	CreatedAt        time.Time   `db:"created_at"`
	UpdatedAt        time.Time   `db:"updated_at"`
	// ExpiresAt is set on a temporary project (0010, DECISIONS I-347):
	// the reaper destroys it, with no snapshot, once this has passed.
	ExpiresAt *time.Time `db:"expires_at"`
	// PersonalOptOut keeps the account's personal layer off this
	// project's machine (0015, DECISIONS I-490).
	PersonalOptOut bool `db:"personal_opt_out"`
	// Multiplexer is what the guest's next start runs, tmux or herdr
	// (0017, DECISIONS I-502); project_json carries it to the guest.
	Multiplexer string `db:"multiplexer"`
	// Placement is where the machine goes when it is placed, `own` or
	// `hosted` (0048, DECISIONS I-1366): `own` is a host this account
	// owns when it owns any and the hosted fleet when it owns none.
	// Read at a create and at a re-placement, never after, because a
	// project is pinned to its host for life.
	Placement string `db:"placement"`
	// GuestRuntime is what the machine boots, nix or erofs (0051,
	// DECISIONS I-1462), fixed at create. PendingBaseVersion is the erofs
	// base its next start boots, set by the base bump; nil when none.
	GuestRuntime       string  `db:"guest_runtime"`
	PendingBaseVersion *string `db:"pending_base_version"`
	// DiskHeldBytes is the bytes the project's volume holds, the figure
	// the plan's disk counts (0019, DECISIONS I-585): the thin volume's
	// allocated blocks from the newest sample, or the estimate a new
	// project starts with while DiskHeldAt is nil. Nil on a row with
	// neither, which counts at VolumeBytes (HeldBytes).
	DiskHeldBytes *int64     `db:"disk_held_bytes"`
	DiskHeldAt    *time.Time `db:"disk_held_at"`
	// RestingSince is when the machine's current rest began, nil while it
	// is awake (0030, DECISIONS I-818): its vCPUs are stopped and its
	// memory is resident, so every process survives and a wake is a
	// resume. The state stays `running`; resting is a marker, never a
	// state. Written from hostd's GuestResting event and reconciled from
	// Hello, and cleared by SetProjectState for any state but running.
	RestingSince *time.Time `db:"resting_since"`
	// KeepAwake keeps this machine out of the rest rule (DECISIONS
	// I-820). The api decides what rests, so it takes effect at once.
	KeepAwake bool `db:"keep_awake"`
}

// HeldBytes is the bytes the plan's disk counts for p: the measured or
// estimated figure, else the volume's size (DECISIONS I-585).
func (p *Project) HeldBytes() int64 {
	if p.DiskHeldBytes != nil && *p.DiskHeldBytes >= 0 {
		return *p.DiskHeldBytes
	}
	return p.VolumeBytes
}

const projectCols = `id, user_id, name, slug, remote_url, class, state, host_id, guest_id, guest_ip, vsock_cid, agent_default, hold_base_updates, base_version, config_revision_id, volume_bytes, tz, host_unreachable, last_error, started_at, stopped_at, destroyed_at, created_at, updated_at, expires_at, personal_opt_out, multiplexer, placement, disk_held_bytes, disk_held_at, resting_since, keep_awake, guest_runtime, pending_base_version`

// Host is a hosts row. Cores is the host's physical cores, which
// placement counts committed guest vCPUs against (0027, DECISIONS I-735,
// I-736); VCPUs is the SMT thread count, twice that on every shape repose
// runs. Cores is 0 when it is unknown: a host that registered before the
// field and has not rotated since.
type Host struct {
	ID            uuid.UUID     `db:"id"`
	Name          string        `db:"name"`
	Hostname      *string       `db:"hostname"`
	SKU           *string       `db:"sku"`
	Provider      *string       `db:"provider"`
	Region        *string       `db:"region"`
	MemBytes      int64         `db:"mem_bytes"`
	VCPUs         int           `db:"vcpus"`
	Cores         int           `db:"cores"`
	PoolBytes     int64         `db:"pool_bytes"`
	GuestCIDR     *netip.Prefix `db:"guest_cidr"`
	WGPubkey      *string       `db:"wg_pubkey"`
	WGIP          *netip.Addr   `db:"wg_ip"`
	State         string        `db:"state"`
	Draining      bool          `db:"draining"`
	FreeMemBytes  int64         `db:"free_mem_bytes"`
	PoolFreeBytes int64         `db:"pool_free_bytes"`
	// PoolUnavailable: hostd says the thin pool cannot serve volumes
	// (DECISIONS I-644); nothing is placed on the host while it is set.
	PoolUnavailable    bool       `db:"pool_unavailable"`
	Load1              float64    `db:"load1"`
	RunningGuests      int        `db:"running_guests"`
	LastHeartbeatAt    *time.Time `db:"last_heartbeat_at"`
	CertSerial         *string    `db:"cert_serial"`
	PrevCertSerial     *string    `db:"prev_cert_serial"`
	CertExpiresAt      *time.Time `db:"cert_expires_at"`
	NixosSystem        *string    `db:"nixos_system"`
	Arch               *string    `db:"arch"`
	CHVersion          *string    `db:"ch_version"`
	JoinTokenHash      *string    `db:"join_token_hash"`
	JoinTokenExpiresAt *time.Time `db:"join_token_expires_at"`
	RegisteredAt       *time.Time `db:"registered_at"`
	// OwnerAccountID is the account that owns the machine (DECISIONS
	// I-1366); nil is a host repose runs. Placement puts only that
	// account's projects on an owned host, and the seat count, the
	// waitlist and the pool gauges leave it out.
	OwnerAccountID *uuid.UUID `db:"owner_account_id"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
}

const hostCols = `id, name, hostname, sku, provider, region, mem_bytes, vcpus, cores, pool_bytes, guest_cidr, wg_pubkey, wg_ip, state, draining, free_mem_bytes, pool_free_bytes, pool_unavailable, load1, running_guests, last_heartbeat_at, cert_serial, prev_cert_serial, cert_expires_at, nixos_system, arch, ch_version, join_token_hash, join_token_expires_at, registered_at, owner_account_id, created_at, updated_at`

// Op is an ops row.
type Op struct {
	ID             uuid.UUID      `db:"id"`
	ProjectID      *uuid.UUID     `db:"project_id"`
	Kind           string         `db:"kind"`
	State          string         `db:"state"`
	Step           int            `db:"step"`
	CommandID      *uuid.UUID     `db:"command_id"`
	HostID         *uuid.UUID     `db:"host_id"`
	Params         map[string]any `db:"params"`
	CommandResult  map[string]any `db:"command_result"`
	Result         map[string]any `db:"result"`
	Error          map[string]any `db:"error"`
	RevisionID     *uuid.UUID     `db:"revision_id"`
	SnapshotID     *uuid.UUID     `db:"snapshot_id"`
	AuditID        *uuid.UUID     `db:"audit_id"`
	RebootRequired bool           `db:"reboot_required"`
	SentAt         *time.Time     `db:"sent_at"`
	StartedAt      *time.Time     `db:"started_at"`
	FinishedAt     *time.Time     `db:"finished_at"`
	CreatedAt      time.Time      `db:"created_at"`
	UpdatedAt      time.Time      `db:"updated_at"`
}

const opCols = `id, project_id, kind, state, step, command_id, host_id, params, command_result, result, error, revision_id, snapshot_id, audit_id, reboot_required, sent_at, started_at, finished_at, created_at, updated_at`

// Revision is a config_revisions row.
type Revision struct {
	ID             uuid.UUID      `db:"id"`
	ProjectID      uuid.UUID      `db:"project_id"`
	Fragment       string         `db:"fragment"`
	Menu           map[string]any `db:"menu"`
	BaseVersion    *string        `db:"base_version"`
	Status         string         `db:"status"`
	SystemClosure  *string        `db:"system_closure"`
	ClosureBytes   *int64         `db:"closure_bytes"`
	KernelChanged  bool           `db:"kernel_changed"`
	RebootRequired bool           `db:"reboot_required"`
	Error          *string        `db:"error"`
	FragmentLine   *int           `db:"fragment_line"`
	BuiltAt        *time.Time     `db:"built_at"`
	AppliedAt      *time.Time     `db:"applied_at"`
	CreatedAt      time.Time      `db:"created_at"`
	UpdatedAt      time.Time      `db:"updated_at"`
	// The personal layer this revision is built with (0015, DECISIONS
	// I-490): its text ('' for none), the account revision it came from,
	// whether the project had opted out, and an error's line in it.
	Personal           string     `db:"personal"`
	PersonalRevisionID *uuid.UUID `db:"personal_revision_id"`
	PersonalOptOut     bool       `db:"personal_opt_out"`
	PersonalLine       *int       `db:"personal_line"`
}

const revisionCols = `id, project_id, fragment, menu, base_version, status, system_closure, closure_bytes, kernel_changed, reboot_required, error, fragment_line, built_at, applied_at, created_at, updated_at, personal, personal_revision_id, personal_opt_out, personal_line`

// PersonalRevision is a personal_revisions row: one save of an account's
// machine.nix (DECISIONS I-490).
type PersonalRevision struct {
	ID        uuid.UUID `db:"id"`
	UserID    uuid.UUID `db:"user_id"`
	Fragment  string    `db:"fragment"`
	Source    string    `db:"source"`
	CreatedAt time.Time `db:"created_at"`
}

const personalCols = `id, user_id, fragment, source, created_at`

// CurrentPersonal is the account's newest personal revision, or
// db.ErrNotFound when it has never saved one.
func CurrentPersonal(ctx context.Context, q Querier, userID uuid.UUID) (*PersonalRevision, error) {
	return one[PersonalRevision](ctx, q, "select "+personalCols+" from personal_revisions where user_id = $1 order by created_at desc, id desc limit 1", userID)
}

// ListPersonal lists an account's personal revisions, newest first.
func ListPersonal(ctx context.Context, q Querier, userID uuid.UUID, limit int) ([]PersonalRevision, error) {
	return many[PersonalRevision](ctx, q, "select "+personalCols+" from personal_revisions where user_id = $1 order by created_at desc, id desc limit $2", userID, limit)
}

// PersonalLayer is what a new project revision records about the
// personal layer.
type PersonalLayer struct {
	Text       string
	RevisionID *uuid.UUID
	OptOut     bool
}

// PersonalFor is the personal layer a new revision of p gets now: the
// account's current text, or none when p has opted out or the account
// has none.
func PersonalFor(ctx context.Context, q Querier, p *Project) (PersonalLayer, error) {
	if p.PersonalOptOut {
		return PersonalLayer{OptOut: true}, nil
	}
	cur, err := CurrentPersonal(ctx, q, p.UserID)
	if errors.Is(err, db.ErrNotFound) {
		return PersonalLayer{}, nil
	}
	if err != nil {
		return PersonalLayer{}, err
	}
	id := cur.ID
	return PersonalLayer{Text: cur.Fragment, RevisionID: &id}, nil
}

// Snapshot is a snapshots row.
type Snapshot struct {
	ID            uuid.UUID  `db:"id"`
	ProjectID     uuid.UUID  `db:"project_id"`
	HostID        *uuid.UUID `db:"host_id"`
	BlobPath      string     `db:"blob_path"`
	Bytes         int64      `db:"bytes"`
	Reason        string     `db:"reason"`
	TakenAt       time.Time  `db:"taken_at"`
	ExpiresAt     *time.Time `db:"expires_at"`
	DeletedAt     *time.Time `db:"deleted_at"`
	RestoringOpID *uuid.UUID `db:"restoring_op_id"`
	CreatedAt     time.Time  `db:"created_at"`
	// Sha256 is the hex SHA-256 of the stored blob, which a restore
	// checks; nil for a snapshot taken before I-462.
	Sha256 *string `db:"sha256"`
	// BaseVersion is the base the guest ran when it was taken (0051,
	// DECISIONS I-1462); nil before 0051.
	BaseVersion *string `db:"base_version"`
	// GoneWithHost names the account's own host the snapshot was kept on
	// when that host was removed, which deleted it (0052, I-1542).
	GoneWithHost *string `db:"gone_with_host"`
}

const snapshotCols = `id, project_id, host_id, blob_path, bytes, reason, taken_at, expires_at, deleted_at, restoring_op_id, created_at, sha256, base_version, gone_with_host`

// Event is an events row.
type Event struct {
	ID          uuid.UUID      `db:"id"`
	ProjectID   uuid.UUID      `db:"project_id"`
	TS          time.Time      `db:"ts"`
	TSSecond    int64          `db:"ts_second"`
	Kind        string         `db:"kind"`
	Agent       *string        `db:"agent"`
	TmuxWindow  *string        `db:"tmux_window"`
	Summary     string         `db:"summary"`
	Source      string         `db:"source"`
	SkewSeconds *int           `db:"skew_seconds"`
	HostEventID *string        `db:"host_event_id"`
	Delivered   map[string]any `db:"delivered"`
	CreatedAt   time.Time      `db:"created_at"`
	// LinkURL and LinkLabel are an agent's `--link` (DECISIONS I-931).
	LinkURL   *string `db:"link_url"`
	LinkLabel *string `db:"link_label"`
}

const eventCols = `id, project_id, ts, ts_second, kind, agent, tmux_window, summary, source, skew_seconds, host_event_id, delivered, created_at, link_url, link_label`

// EventLink is an event's link as the routes show it: {url, label}, nil
// when it has none (DECISIONS I-931).
func EventLink(url, label *string) map[string]string {
	if url == nil || *url == "" {
		return nil
	}
	l := map[string]string{"url": *url}
	if label != nil && *label != "" {
		l["label"] = *label
	}
	return l
}

// BaseVersion is a base_versions row.
type BaseVersion struct {
	Version    string    `db:"version"`
	NixRev     string    `db:"nix_rev"`
	Changelog  string    `db:"changelog"`
	ReleasedAt time.Time `db:"released_at"`
	Security   bool      `db:"security"`
	CreatedAt  time.Time `db:"created_at"`
	// Format is nix or erofs (0051, DECISIONS I-1462). An erofs row names
	// three files per architecture, each with its SHA-256; nix_rev is ''.
	Format               string  `db:"format"`
	ImageAmd64URL        *string `db:"image_amd64_url"`
	ImageAmd64SHA256     *string `db:"image_amd64_sha256"`
	KernelAmd64URL       *string `db:"kernel_amd64_url"`
	KernelAmd64SHA256    *string `db:"kernel_amd64_sha256"`
	InitramfsAmd64URL    *string `db:"initramfs_amd64_url"`
	InitramfsAmd64SHA256 *string `db:"initramfs_amd64_sha256"`
	ImageArm64URL        *string `db:"image_arm64_url"`
	ImageArm64SHA256     *string `db:"image_arm64_sha256"`
	KernelArm64URL       *string `db:"kernel_arm64_url"`
	KernelArm64SHA256    *string `db:"kernel_arm64_sha256"`
	InitramfsArm64URL    *string `db:"initramfs_arm64_url"`
	InitramfsArm64SHA256 *string `db:"initramfs_arm64_sha256"`
	KernelVersion        *string `db:"kernel_version"`
	ManifestURL          *string `db:"manifest_url"`
}

const baseVersionCols = `version, nix_rev, changelog, released_at, security, created_at, format, image_amd64_url, image_amd64_sha256, kernel_amd64_url, kernel_amd64_sha256, initramfs_amd64_url, initramfs_amd64_sha256, image_arm64_url, image_arm64_sha256, kernel_arm64_url, kernel_arm64_sha256, initramfs_arm64_url, initramfs_arm64_sha256, kernel_version, manifest_url`

// Guest runtimes (DECISIONS I-1462).
const (
	RuntimeNix   = "nix"
	RuntimeErofs = "erofs"
	// SettingDefaultGuestRuntime is the settings key a project create
	// reads when the request names no runtime; absent means nix.
	SettingDefaultGuestRuntime = "default_guest_runtime"
)

// NormalizeRuntime is the runtime a row carries, nix for anything but
// erofs.
func NormalizeRuntime(r string) string {
	if r == RuntimeErofs {
		return RuntimeErofs
	}
	return RuntimeNix
}

// ImageArch maps a host's arch (x86_64, aarch64) to the base row's
// image column suffix (amd64, arm64); "" for anything else.
func ImageArch(hostArch string) string {
	switch hostArch {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return ""
}

// BaseFiles is one architecture's three files of an erofs base.
type BaseFiles struct {
	RootfsURL, RootfsSHA256       string
	KernelURL, KernelSHA256       string
	InitramfsURL, InitramfsSHA256 string
}

// Files returns the base's files for an image arch (amd64, arm64), and
// false when the row has none for it.
func (b *BaseVersion) Files(arch string) (BaseFiles, bool) {
	s := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	var f BaseFiles
	switch arch {
	case "amd64":
		f = BaseFiles{s(b.ImageAmd64URL), s(b.ImageAmd64SHA256), s(b.KernelAmd64URL), s(b.KernelAmd64SHA256), s(b.InitramfsAmd64URL), s(b.InitramfsAmd64SHA256)}
	case "arm64":
		f = BaseFiles{s(b.ImageArm64URL), s(b.ImageArm64SHA256), s(b.KernelArm64URL), s(b.KernelArm64SHA256), s(b.InitramfsArm64URL), s(b.InitramfsArm64SHA256)}
	default:
		return BaseFiles{}, false
	}
	return f, f.RootfsURL != "" && f.KernelURL != "" && f.InitramfsURL != ""
}

// Arches lists the image arches (amd64, arm64) an erofs row has files for.
func (b *BaseVersion) Arches() []string {
	var out []string
	for _, a := range []string{"amd64", "arm64"} {
		if _, ok := b.Files(a); ok {
			out = append(out, a)
		}
	}
	return out
}

// Certificate is a certificates row.
type Certificate struct {
	Serial      int64       `db:"serial"`
	UserID      uuid.UUID   `db:"user_id"`
	ProjectIDs  []uuid.UUID `db:"project_ids"`
	PublicKeyFP string      `db:"public_key_fp"`
	KeyID       string      `db:"key_id"`
	Kind        string      `db:"kind"`
	IssuedAt    time.Time   `db:"issued_at"`
	ExpiresAt   time.Time   `db:"expires_at"`
	RevokedAt   *time.Time  `db:"revoked_at"`
	CreatedAt   time.Time   `db:"created_at"`
}

const certificateCols = `serial, user_id, project_ids, public_key_fp, key_id, kind, issued_at, expires_at, revoked_at, created_at`

func one[T any](ctx context.Context, q Querier, sql string, args ...any) (*T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[T])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, db.ErrNotFound
	}
	return v, err
}

func many[T any](ctx context.Context, q Querier, sql string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if out == nil {
		out = []T{}
	}
	return out, err
}

// NewID makes a UUIDv7.
func NewID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// --- users ------------------------------------------------------------

// GetUser fetches a user by id.
func GetUser(ctx context.Context, q Querier, id uuid.UUID) (*User, error) {
	return one[User](ctx, q, "select "+userCols+" from users where id = $1", id)
}

// GetUserBySub fetches a user by Logto subject.
func GetUserBySub(ctx context.Context, q Querier, sub string) (*User, error) {
	return one[User](ctx, q, "select "+userCols+" from users where logto_sub = $1", sub)
}

// GetUserByHandle fetches a user by handle.
func GetUserByHandle(ctx context.Context, q Querier, handle string) (*User, error) {
	return one[User](ctx, q, "select "+userCols+" from users where handle = $1", handle)
}

// ListUsers lists every user except the platform pseudo-user.
func ListUsers(ctx context.Context, q Querier) ([]User, error) {
	return many[User](ctx, q, "select "+userCols+" from users where handle <> 'repose-platform' order by created_at")
}

// --- projects ---------------------------------------------------------

// GetProject fetches any project by id.
func GetProject(ctx context.Context, q Querier, id uuid.UUID) (*Project, error) {
	return one[Project](ctx, q, "select "+projectCols+" from projects where id = $1", id)
}

// GetProjectForUpdate locks the row.
func GetProjectForUpdate(ctx context.Context, q Querier, id uuid.UUID) (*Project, error) {
	return one[Project](ctx, q, "select "+projectCols+" from projects where id = $1 for update", id)
}

// GetUserProject fetches a live project owned by the user; cross-user
// lookups are not found (SECURITY.md boundary 5).
func GetUserProject(ctx context.Context, q Querier, userID, id uuid.UUID) (*Project, error) {
	return one[Project](ctx, q, "select "+projectCols+" from projects where id = $1 and user_id = $2 and destroyed_at is null", id, userID)
}

// GetUserProjectAny is GetUserProject including destroyed projects.
func GetUserProjectAny(ctx context.Context, q Querier, userID, id uuid.UUID) (*Project, error) {
	return one[Project](ctx, q, "select "+projectCols+" from projects where id = $1 and user_id = $2", id, userID)
}

// ListUserProjects lists a user's live projects.
func ListUserProjects(ctx context.Context, q Querier, userID uuid.UUID) ([]Project, error) {
	return many[Project](ctx, q, "select "+projectCols+" from projects where user_id = $1 and destroyed_at is null order by created_at", userID)
}

// GetProjectByGuest finds the project a guest belongs to.
func GetProjectByGuest(ctx context.Context, q Querier, guestID uuid.UUID) (*Project, error) {
	return one[Project](ctx, q, "select "+projectCols+" from projects where guest_id = $1", guestID)
}

// ErrOtherHost is GetProjectOnHost's answer for a guest that belongs to a
// project placed on another host, or on none.
var ErrOtherHost = errors.New("guest is not on this host")

// GetProjectOnHost resolves a guest a host reported, and refuses it with
// ErrOtherHost unless the project is placed on that host: what a host says
// about a guest counts only for its own guests (DECISIONS I-447).
func GetProjectOnHost(ctx context.Context, q Querier, guestID, hostID uuid.UUID) (*Project, error) {
	p, err := GetProjectByGuest(ctx, q, guestID)
	if err != nil {
		return nil, err
	}
	if p.HostID == nil || *p.HostID != hostID {
		return nil, ErrOtherHost
	}
	return p, nil
}

// GetProjectByLogin resolves <slug>.<handle>.
func GetProjectByLogin(ctx context.Context, q Querier, slug, handle string) (*Project, error) {
	return one[Project](ctx, q, "select "+projectCols+" from projects p where p.slug = $1 and p.destroyed_at is null and p.user_id = (select id from users where handle = $2)", slug, handle)
}

// GetProjectByGuestIP resolves a guest address (the edge's hook path).
func GetProjectByGuestIP(ctx context.Context, q Querier, ip netip.Addr) (*Project, error) {
	return one[Project](ctx, q, "select "+projectCols+" from projects where guest_ip = $1 and destroyed_at is null", ip)
}

// ListProjectsOnHost lists the live projects placed on a host.
func ListProjectsOnHost(ctx context.Context, q Querier, hostID uuid.UUID) ([]Project, error) {
	return many[Project](ctx, q, "select "+projectCols+" from projects where host_id = $1 and destroyed_at is null order by created_at", hostID)
}

// ListAllProjects lists every non-destroyed project (admin).
func ListAllProjects(ctx context.Context, q Querier) ([]Project, error) {
	return many[Project](ctx, q, "select "+projectCols+" from projects where destroyed_at is null and user_id <> '00000000-0000-7000-8000-000000000000' order by created_at")
}

// SetProjectState updates state and the matching timestamp.
func SetProjectState(ctx context.Context, q Querier, id uuid.UUID, state string) error {
	// resting_since is cleared by any state but running: a machine that
	// stops, errors or is destroyed is not resting, and a marker left on
	// it would show a frozen machine that no longer exists (I-818). This
	// is the one place a project's state is written, so it is the one
	// place the marker has to be cleared.
	_, err := q.Exec(ctx, `update projects set state = $2,
		started_at = case when $2 = 'running' and state <> 'running' then now() else started_at end,
		stopped_at = case when $2 = 'stopped' then now() else stopped_at end,
		destroyed_at = case when $2 = 'destroyed' then coalesce(destroyed_at, now()) else destroyed_at end,
		resting_since = case when $2 = 'running' then resting_since else null end
		where id = $1`, id, state)
	return err
}

// --- hosts ------------------------------------------------------------

// Where a project's machine goes when it is placed (0048, DECISIONS
// I-1366). `own` is a host the account owns when it owns any and the
// hosted fleet when it owns none; `hosted` is the fleet whatever it owns.
const (
	PlacementOwn    = "own"
	PlacementHosted = "hosted"
)

// NormalizePlacement is the value a row carries, with the default for a
// row written before 0048 or by a client that sent none.
func NormalizePlacement(p string) string {
	if p == PlacementHosted {
		return PlacementHosted
	}
	return PlacementOwn
}

// GetHost fetches a host by id.
func GetHost(ctx context.Context, q Querier, id uuid.UUID) (*Host, error) {
	return one[Host](ctx, q, "select "+hostCols+" from hosts where id = $1", id)
}

// GetHostByName fetches one of the operator's own hosts by name. Names
// are unique per owner since 0048 (DECISIONS I-1366), so a name alone
// names a host only among the hosts repose runs; an owned host is
// reached by id, or by name within its account with
// GetAccountHostByName. That is also the safe default for the operator
// CLI: `hosts drain mac-mini` must not find a user's Mac.
func GetHostByName(ctx context.Context, q Querier, name string) (*Host, error) {
	return one[Host](ctx, q, "select "+hostCols+" from hosts where name = $1 and owner_account_id is null", name)
}

// GetAccountHostByName fetches one host the account owns, by the name
// that account gave it.
func GetAccountHostByName(ctx context.Context, q Querier, owner uuid.UUID, name string) (*Host, error) {
	return one[Host](ctx, q, "select "+hostCols+" from hosts where owner_account_id = $1 and name = $2", owner, name)
}

// ListHosts lists hosts in name order.
func ListHosts(ctx context.Context, q Querier) ([]Host, error) {
	return many[Host](ctx, q, "select "+hostCols+" from hosts order by name")
}

// ListAccountHosts lists the hosts one account owns, oldest first, so a
// user reads them in the order they added them.
func ListAccountHosts(ctx context.Context, q Querier, owner uuid.UUID) ([]Host, error) {
	return many[Host](ctx, q, "select "+hostCols+" from hosts where owner_account_id = $1 order by created_at, name", owner)
}

// --- ops --------------------------------------------------------------

// GetOp fetches an op.
func GetOp(ctx context.Context, q Querier, id uuid.UUID) (*Op, error) {
	return one[Op](ctx, q, "select "+opCols+" from ops where id = $1", id)
}

// OpMark is what a long-poll of an op watches (GET /projects/:id/ops/:op_id
// ?wait, I-236): the op's state and step and its project's state.
type OpMark struct {
	State        string
	Step         int
	ProjectState string
}

// GetOpMark reads an op's OpMark in one small query; the long-poll calls
// it every 100 ms and holds no connection in between.
func GetOpMark(ctx context.Context, q Querier, id uuid.UUID) (OpMark, error) {
	var m OpMark
	var ps *string
	err := q.QueryRow(ctx, "select o.state, o.step, p.state from ops o left join projects p on p.id = o.project_id where o.id = $1", id).Scan(&m.State, &m.Step, &ps)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, db.ErrNotFound
	}
	if ps != nil {
		m.ProjectState = *ps
	}
	return m, err
}

// GetOpByCommand finds the op that sent a command.
func GetOpByCommand(ctx context.Context, q Querier, commandID uuid.UUID) (*Op, error) {
	return one[Op](ctx, q, "select "+opCols+" from ops where command_id = $1", commandID)
}

// ListProjectOps lists a project's ops, newest first.
func ListProjectOps(ctx context.Context, q Querier, projectID uuid.UUID, limit int) ([]Op, error) {
	return many[Op](ctx, q, "select "+opCols+" from ops where project_id = $1 order by created_at desc limit $2", projectID, limit)
}

// OpenOpsForProject returns pending or running ops of a project.
func OpenOpsForProject(ctx context.Context, q Querier, projectID uuid.UUID) ([]Op, error) {
	return many[Op](ctx, q, "select "+opCols+" from ops where project_id = $1 and state in ('pending','running') order by created_at", projectID)
}

// --- revisions --------------------------------------------------------

// GetRevision fetches a revision.
func GetRevision(ctx context.Context, q Querier, id uuid.UUID) (*Revision, error) {
	return one[Revision](ctx, q, "select "+revisionCols+" from config_revisions where id = $1", id)
}

// ListRevisions lists a project's revisions, newest first.
func ListRevisions(ctx context.Context, q Querier, projectID uuid.UUID) ([]Revision, error) {
	return many[Revision](ctx, q, "select "+revisionCols+" from config_revisions where project_id = $1 order by created_at desc", projectID)
}

// --- snapshots --------------------------------------------------------

// GetSnapshot fetches a snapshot.
func GetSnapshot(ctx context.Context, q Querier, id uuid.UUID) (*Snapshot, error) {
	return one[Snapshot](ctx, q, "select "+snapshotCols+" from snapshots where id = $1", id)
}

// ListUserProjectsBySlug lists every project of the user with that slug,
// live first, then destroyed ones newest destroy first (I-167).
func ListUserProjectsBySlug(ctx context.Context, q Querier, userID uuid.UUID, slug string) ([]Project, error) {
	return many[Project](ctx, q, "select "+projectCols+" from projects where user_id = $1 and slug = $2 order by destroyed_at desc nulls first, created_at desc", userID, slug)
}

// RestorableSnapshotWhere is a snapshot (aliased s) that can still be
// restored: not deleted by the retention job and not past its expiry.
const RestorableSnapshotWhere = `s.deleted_at is null and (s.expires_at is null or s.expires_at > now())`

// NewestRestorableSnapshot is a project's newest snapshot that can still
// be restored, or db.ErrNotFound.
func NewestRestorableSnapshot(ctx context.Context, q Querier, projectID uuid.UUID) (*Snapshot, error) {
	return one[Snapshot](ctx, q, "select "+snapshotCols+" from snapshots s where s.project_id = $1 and "+RestorableSnapshotWhere+" order by s.taken_at desc, s.created_at desc limit 1", projectID)
}

// ListSnapshots lists a project's live snapshots, newest first.
func ListSnapshots(ctx context.Context, q Querier, projectID uuid.UUID) ([]Snapshot, error) {
	return many[Snapshot](ctx, q, "select "+snapshotCols+" from snapshots where project_id = $1 and deleted_at is null order by taken_at desc, created_at desc", projectID)
}

// --- events -----------------------------------------------------------

// ListEvents lists a project's events after since, newest first. kinds,
// when not empty, keeps only those kinds (I-662).
func ListEvents(ctx context.Context, q Querier, projectID uuid.UUID, since time.Time, limit int, kinds ...string) ([]Event, error) {
	if len(kinds) > 0 {
		return many[Event](ctx, q, "select "+eventCols+" from events where project_id = $1 and ts > $2 and kind = any($4) order by ts desc, id desc limit $3", projectID, since, limit, kinds)
	}
	return many[Event](ctx, q, "select "+eventCols+" from events where project_id = $1 and ts > $2 order by ts desc, id desc limit $3", projectID, since, limit)
}

// ListEventsBefore lists a project's events older than the event before,
// newest first: the next page after a list that ended at before (I-414).
// An id that is not one of the project's events gives no rows.
func ListEventsBefore(ctx context.Context, q Querier, projectID, before uuid.UUID, limit int, kinds ...string) ([]Event, error) {
	if len(kinds) > 0 {
		return many[Event](ctx, q, "select "+eventCols+" from events where project_id = $1 and (ts, id) < (select ts, id from events where id = $2 and project_id = $1) and kind = any($4) order by ts desc, id desc limit $3", projectID, before, limit, kinds)
	}
	return many[Event](ctx, q, "select "+eventCols+" from events where project_id = $1 and (ts, id) < (select ts, id from events where id = $2 and project_id = $1) order by ts desc, id desc limit $3", projectID, before, limit)
}

// ListEventsAfter lists the limit events newer than the event after and
// nearest to it, newest first: the page before a page that started at
// after (I-662). An id that is not one of the project's events gives no
// rows.
func ListEventsAfter(ctx context.Context, q Querier, projectID, after uuid.UUID, limit int, kinds ...string) ([]Event, error) {
	var evs []Event
	var err error
	if len(kinds) > 0 {
		evs, err = many[Event](ctx, q, "select "+eventCols+" from events where project_id = $1 and (ts, id) > (select ts, id from events where id = $2 and project_id = $1) and kind = any($4) order by ts asc, id asc limit $3", projectID, after, limit, kinds)
	} else {
		evs, err = many[Event](ctx, q, "select "+eventCols+" from events where project_id = $1 and (ts, id) > (select ts, id from events where id = $2 and project_id = $1) order by ts asc, id asc limit $3", projectID, after, limit)
	}
	if err != nil {
		return nil, err
	}
	slices.Reverse(evs)
	return evs, nil
}

// CountEvents counts a project's events, only those of kinds when kinds
// is not empty: the Repose-Event-Count header (I-662).
func CountEvents(ctx context.Context, q Querier, projectID uuid.UUID, kinds ...string) (int, error) {
	var n int
	var err error
	if len(kinds) > 0 {
		err = q.QueryRow(ctx, "select count(*) from events where project_id = $1 and kind = any($2)", projectID, kinds).Scan(&n)
	} else {
		err = q.QueryRow(ctx, "select count(*) from events where project_id = $1", projectID).Scan(&n)
	}
	return n, err
}

// --- base versions ----------------------------------------------------

// LatestBase returns the newest nix base version, or ErrNotFound. An
// erofs row is never a Nix project's base (DECISIONS I-1462).
func LatestBase(ctx context.Context, q Querier) (*BaseVersion, error) {
	return one[BaseVersion](ctx, q, "select "+baseVersionCols+" from base_versions where format = 'nix' order by released_at desc limit 1")
}

// LatestErofsBase returns the newest erofs base that has files for the
// image arch (amd64, arm64), or ErrNotFound; arch "" means any.
func LatestErofsBase(ctx context.Context, q Querier, arch string) (*BaseVersion, error) {
	switch arch {
	case "amd64":
		return one[BaseVersion](ctx, q, "select "+baseVersionCols+" from base_versions where format = 'erofs' and image_amd64_url is not null order by released_at desc limit 1")
	case "arm64":
		return one[BaseVersion](ctx, q, "select "+baseVersionCols+" from base_versions where format = 'erofs' and image_arm64_url is not null order by released_at desc limit 1")
	case "":
		return one[BaseVersion](ctx, q, "select "+baseVersionCols+" from base_versions where format = 'erofs' order by released_at desc limit 1")
	}
	return nil, db.ErrNotFound
}

// DefaultGuestRuntime is settings.default_guest_runtime, nix when absent
// or anything else (DECISIONS I-1462).
func DefaultGuestRuntime(ctx context.Context, q Querier) (string, error) {
	v, err := Setting(ctx, q, SettingDefaultGuestRuntime)
	if err != nil {
		return RuntimeNix, err
	}
	return NormalizeRuntime(v), nil
}

// GetBase fetches one base version.
func GetBase(ctx context.Context, q Querier, version string) (*BaseVersion, error) {
	return one[BaseVersion](ctx, q, "select "+baseVersionCols+" from base_versions where version = $1", version)
}

// ListBases lists base versions, newest first.
func ListBases(ctx context.Context, q Querier) ([]BaseVersion, error) {
	return many[BaseVersion](ctx, q, "select "+baseVersionCols+" from base_versions order by released_at desc")
}

// --- certificates -----------------------------------------------------

// GetCertificate fetches a certificate by serial.
func GetCertificate(ctx context.Context, q Querier, serial int64) (*Certificate, error) {
	return one[Certificate](ctx, q, "select "+certificateCols+" from certificates where serial = $1", serial)
}

// --- audit ------------------------------------------------------------

// Audit inserts an audit_log row and returns its id. detail must never
// carry a secret value.
func Audit(ctx context.Context, q Querier, actor, action, target string, detail map[string]any) (uuid.UUID, error) {
	id := NewID()
	if detail == nil {
		detail = map[string]any{}
	}
	_, err := q.Exec(ctx, "insert into audit_log (id, actor, action, target, detail) values ($1, $2, $3, $4, $5)", id, actor, action, target, detail)
	return id, err
}

// Setting reads a settings value; "" when absent.
func Setting(ctx context.Context, q Querier, key string) (string, error) {
	var v string
	err := q.QueryRow(ctx, "select value from settings where key = $1", key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting upserts a settings value.
func SetSetting(ctx context.Context, q Querier, key, value string) error {
	_, err := q.Exec(ctx, "insert into settings (key, value) values ($1, $2) on conflict (key) do update set value = excluded.value", key, value)
	return err
}

// --- waitlist (DECISIONS I-269, I-290) --------------------------------

// WaitlistEntry is a waitlist row with the user's handle and, while the
// user waits, their place in the queue (1 is next).
type WaitlistEntry struct {
	UserID    uuid.UUID  `db:"user_id"`
	Handle    string     `db:"handle"`
	JoinedAt  time.Time  `db:"joined_at"`
	InvitedAt *time.Time `db:"invited_at"`
	InvitedBy *string    `db:"invited_by"`
	// HoldUntil is when the invitation's seat hold runs out; set with
	// InvitedAt.
	HoldUntil *time.Time `db:"hold_until"`
	// ConvertedAt is when the invited user's subscription arrived.
	ConvertedAt *time.Time `db:"converted_at"`
	// ExpiredInvites counts holds that ran out; each moved the user to
	// the back.
	ExpiredInvites int `db:"expired_invites"`
	// Position is 0 once invited, and for a waiting user whose account is
	// suspended, cancelled or deleted: such a user holds no place.
	Position int `db:"position"`
	// LapsedAt is when a hold ran out unconverted (I-1033). A lapsed
	// user holds no place (Position 0), is not counted as waiting and is
	// never invited by the tick, until they ask again.
	LapsedAt *time.Time `db:"lapsed_at"`
	// CooldownUntil is I-902's cooldown: a row cooling keeps its
	// Position but ListWaiting leaves it out until it passes. Since
	// I-1033 it matters only for rows that were cooling at migration
	// 0034, and for an api on the older code.
	CooldownUntil *time.Time `db:"cooldown_until"`
}

// Holding reports whether the entry holds a seat: invited, not
// converted, and the hold has not run out at now.
func (e *WaitlistEntry) Holding(now time.Time) bool {
	return e.InvitedAt != nil && e.ConvertedAt == nil && e.HoldUntil != nil && e.HoldUntil.After(now)
}

// waitlistSQL numbers the waiting users of live accounts oldest first;
// everyone else, the lapsed and the billing-exempt included, gets
// position 0 (I-1090).
const waitlistSQL = `select w.user_id, u.handle, w.joined_at, w.invited_at, w.invited_by, w.hold_until, w.converted_at, w.expired_invites, w.lapsed_at, w.cooldown_until,
	coalesce(q.position, 0)::integer as position
	from waitlist w join users u on u.id = w.user_id
	left join (select w2.user_id, row_number() over (order by w2.joined_at, w2.user_id) as position
	             from waitlist w2 join users u2 on u2.id = w2.user_id
	            where w2.invited_at is null and w2.lapsed_at is null and u2.suspended_at is null and u2.cancelled_at is null and u2.deleted_at is null
	              and u2.billing_status <> 'exempt') q
	  on q.user_id = w.user_id`

// GetWaitlistEntry returns the user's row, or db.ErrNotFound.
func GetWaitlistEntry(ctx context.Context, q Querier, userID uuid.UUID) (*WaitlistEntry, error) {
	return one[WaitlistEntry](ctx, q, waitlistSQL+" where w.user_id = $1", userID)
}

// ListWaiting returns the candidates for a free seat, next first: the
// users holding a place whose cooldown, if any, has passed at now. A
// lapsed user holds none (I-1033), which is what stops a hold that ran
// out from being offered again by itself, and nor does a billing-exempt
// one (I-1090), which has no plan to convert the seat with.
func ListWaiting(ctx context.Context, q Querier, now time.Time) ([]WaitlistEntry, error) {
	return many[WaitlistEntry](ctx, q, waitlistSQL+" where q.position is not null and (w.cooldown_until is null or w.cooldown_until <= $1) order by q.position", now)
}

// ListWaitlist returns every row: the waiting in order, then the rest,
// newest invitation first.
func ListWaitlist(ctx context.Context, q Querier) ([]WaitlistEntry, error) {
	return many[WaitlistEntry](ctx, q, waitlistSQL+" order by q.position nulls last, w.invited_at desc nulls last, w.joined_at")
}
