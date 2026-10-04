// Package store holds the row types and typed queries the api's packages
// share. Column names follow docs/interfaces/db-schema.md; rows are
// scanned by name so a query lists exactly the columns of the struct.
package store

import (
	"context"
	"errors"
	"net/netip"
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
	// PaddleCustomerID is the Paddle customer, created at checkout (0008,
	// I-289). BillingAnchor is unused since I-289 (the period is the
	// subscription's); PastDueSince is when the account first failed a
	// payment, which the 3-day stop reads. BillingStatus is a projection
	// of the subscription: none|trial|active|past_due|suspended|exempt.
	PaddleCustomerID *string    `db:"paddle_customer_id"`
	BillingAnchor    *time.Time `db:"billing_anchor"`
	PastDueSince     *time.Time `db:"past_due_since"`
	BillingStatus    string     `db:"billing_status"`
	HasCard          bool       `db:"has_card"`
	TrialCreditCents int64      `db:"trial_credit_cents"`
	ProjectLimit     int        `db:"project_limit"`
	XLLimit          int        `db:"xl_limit"`
	SuspendedAt      *time.Time `db:"suspended_at"`
	SuspendedReason  *string    `db:"suspended_reason"`
	CancelledAt      *time.Time `db:"cancelled_at"`
	DeletedAt        *time.Time `db:"deleted_at"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
}

const userCols = `id, logto_sub, handle, email, github_login, tz, notify_email, ntfy_url, paddle_customer_id, billing_anchor, past_due_since, billing_status, has_card, trial_credit_cents, project_limit, xl_limit, suspended_at, suspended_reason, cancelled_at, deleted_at, created_at, updated_at`

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
}

const projectCols = `id, user_id, name, slug, remote_url, class, state, host_id, guest_id, guest_ip, vsock_cid, agent_default, hold_base_updates, base_version, config_revision_id, volume_bytes, tz, host_unreachable, last_error, started_at, stopped_at, destroyed_at, created_at, updated_at, expires_at`

// Host is a hosts row.
type Host struct {
	ID                 uuid.UUID     `db:"id"`
	Name               string        `db:"name"`
	Hostname           *string       `db:"hostname"`
	SKU                *string       `db:"sku"`
	Provider           *string       `db:"provider"`
	Region             *string       `db:"region"`
	MemBytes           int64         `db:"mem_bytes"`
	VCPUs              int           `db:"vcpus"`
	PoolBytes          int64         `db:"pool_bytes"`
	GuestCIDR          *netip.Prefix `db:"guest_cidr"`
	WGPubkey           *string       `db:"wg_pubkey"`
	WGIP               *netip.Addr   `db:"wg_ip"`
	State              string        `db:"state"`
	Draining           bool          `db:"draining"`
	FreeMemBytes       int64         `db:"free_mem_bytes"`
	PoolFreeBytes      int64         `db:"pool_free_bytes"`
	Load1              float64       `db:"load1"`
	RunningGuests      int           `db:"running_guests"`
	LastHeartbeatAt    *time.Time    `db:"last_heartbeat_at"`
	CertSerial         *string       `db:"cert_serial"`
	CertExpiresAt      *time.Time    `db:"cert_expires_at"`
	NixosSystem        *string       `db:"nixos_system"`
	CHVersion          *string       `db:"ch_version"`
	JoinTokenHash      *string       `db:"join_token_hash"`
	JoinTokenExpiresAt *time.Time    `db:"join_token_expires_at"`
	RegisteredAt       *time.Time    `db:"registered_at"`
	CreatedAt          time.Time     `db:"created_at"`
	UpdatedAt          time.Time     `db:"updated_at"`
}

const hostCols = `id, name, hostname, sku, provider, region, mem_bytes, vcpus, pool_bytes, guest_cidr, wg_pubkey, wg_ip, state, draining, free_mem_bytes, pool_free_bytes, load1, running_guests, last_heartbeat_at, cert_serial, cert_expires_at, nixos_system, ch_version, join_token_hash, join_token_expires_at, registered_at, created_at, updated_at`

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
}

const revisionCols = `id, project_id, fragment, menu, base_version, status, system_closure, closure_bytes, kernel_changed, reboot_required, error, fragment_line, built_at, applied_at, created_at, updated_at`

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
}

const snapshotCols = `id, project_id, host_id, blob_path, bytes, reason, taken_at, expires_at, deleted_at, restoring_op_id, created_at`

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
}

const eventCols = `id, project_id, ts, ts_second, kind, agent, tmux_window, summary, source, skew_seconds, host_event_id, delivered, created_at`

// BaseVersion is a base_versions row.
type BaseVersion struct {
	Version    string    `db:"version"`
	NixRev     string    `db:"nix_rev"`
	Changelog  string    `db:"changelog"`
	ReleasedAt time.Time `db:"released_at"`
	Security   bool      `db:"security"`
	CreatedAt  time.Time `db:"created_at"`
}

const baseVersionCols = `version, nix_rev, changelog, released_at, security, created_at`

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
	_, err := q.Exec(ctx, `update projects set state = $2,
		started_at = case when $2 = 'running' and state <> 'running' then now() else started_at end,
		stopped_at = case when $2 = 'stopped' then now() else stopped_at end,
		destroyed_at = case when $2 = 'destroyed' then coalesce(destroyed_at, now()) else destroyed_at end
		where id = $1`, id, state)
	return err
}

// --- hosts ------------------------------------------------------------

// GetHost fetches a host by id.
func GetHost(ctx context.Context, q Querier, id uuid.UUID) (*Host, error) {
	return one[Host](ctx, q, "select "+hostCols+" from hosts where id = $1", id)
}

// GetHostByName fetches a host by name.
func GetHostByName(ctx context.Context, q Querier, name string) (*Host, error) {
	return one[Host](ctx, q, "select "+hostCols+" from hosts where name = $1", name)
}

// ListHosts lists hosts in name order.
func ListHosts(ctx context.Context, q Querier) ([]Host, error) {
	return many[Host](ctx, q, "select "+hostCols+" from hosts order by name")
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

// ListEvents lists a project's events after since, newest first.
func ListEvents(ctx context.Context, q Querier, projectID uuid.UUID, since time.Time, limit int) ([]Event, error) {
	return many[Event](ctx, q, "select "+eventCols+" from events where project_id = $1 and ts > $2 order by ts desc, id desc limit $3", projectID, since, limit)
}

// ListEventsBefore lists a project's events older than the event before,
// newest first: the next page after a list that ended at before (I-414).
// An id that is not one of the project's events gives no rows.
func ListEventsBefore(ctx context.Context, q Querier, projectID, before uuid.UUID, limit int) ([]Event, error) {
	return many[Event](ctx, q, "select "+eventCols+" from events where project_id = $1 and (ts, id) < (select ts, id from events where id = $2 and project_id = $1) order by ts desc, id desc limit $3", projectID, before, limit)
}

// --- base versions ----------------------------------------------------

// LatestBase returns the newest base version, or ErrNotFound.
func LatestBase(ctx context.Context, q Querier) (*BaseVersion, error) {
	return one[BaseVersion](ctx, q, "select "+baseVersionCols+" from base_versions order by released_at desc limit 1")
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
}

// Holding reports whether the entry holds a seat: invited, not
// converted, and the hold has not run out at now.
func (e *WaitlistEntry) Holding(now time.Time) bool {
	return e.InvitedAt != nil && e.ConvertedAt == nil && e.HoldUntil != nil && e.HoldUntil.After(now)
}

// waitlistSQL numbers the waiting users of live accounts oldest first;
// everyone else gets position 0.
const waitlistSQL = `select w.user_id, u.handle, w.joined_at, w.invited_at, w.invited_by, w.hold_until, w.converted_at, w.expired_invites,
	coalesce(q.position, 0)::integer as position
	from waitlist w join users u on u.id = w.user_id
	left join (select w2.user_id, row_number() over (order by w2.joined_at, w2.user_id) as position
	             from waitlist w2 join users u2 on u2.id = w2.user_id
	            where w2.invited_at is null and u2.suspended_at is null and u2.cancelled_at is null and u2.deleted_at is null) q
	  on q.user_id = w.user_id`

// GetWaitlistEntry returns the user's row, or db.ErrNotFound.
func GetWaitlistEntry(ctx context.Context, q Querier, userID uuid.UUID) (*WaitlistEntry, error) {
	return one[WaitlistEntry](ctx, q, waitlistSQL+" where w.user_id = $1", userID)
}

// ListWaiting returns the users holding a place, next first.
func ListWaiting(ctx context.Context, q Querier) ([]WaitlistEntry, error) {
	return many[WaitlistEntry](ctx, q, waitlistSQL+" where q.position is not null order by q.position")
}

// ListWaitlist returns every row: the waiting in order, then the rest,
// newest invitation first.
func ListWaitlist(ctx context.Context, q Querier) ([]WaitlistEntry, error) {
	return many[WaitlistEntry](ctx, q, waitlistSQL+" order by q.position nulls last, w.invited_at desc nulls last, w.joined_at")
}
