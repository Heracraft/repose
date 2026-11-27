package ops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/buildlog"
	"github.com/heracraft/repose/internal/api/scheduler"
	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/multiplexer"
)

// Phase names. An op's phases are fixed at enqueue by the Plan functions.
const (
	PhaseBuild         = "build"
	PhaseCreateGuest   = "create_guest"
	PhaseStartGuest    = "start_guest"
	PhaseStopGuest     = "stop_guest"
	PhaseSnapshot      = "snapshot"
	PhaseDestroyGuest  = "destroy_guest"
	PhaseResize        = "resize"
	PhaseRestore       = "restore"
	PhaseApplyConfig   = "apply_config"
	PhaseUpdateSecrets = "update_secrets"
	PhaseExec          = "exec"
	PhaseDrain         = "drain"
	// PhaseWake resumes a machine that rests, before the first phase of
	// the plan that reaches its guest (DECISIONS I-818). It is a phase,
	// not an op: the engine allows one open op per project and the wake
	// is needed precisely when that op is open. It is skipped when there
	// is nothing to wake -- the machine is not running, has no guest, or
	// sits on a host that does not pause -- so a plan carrying it costs
	// nothing on a host without the feature.
	PhaseWake = "wake"
)

// Op kinds.
const (
	KindCreate        = "create"
	KindStart         = "start"
	KindStop          = "stop"
	KindDestroy       = "destroy"
	KindResize        = "resize"
	KindSnapshot      = "snapshot"
	KindRestore       = "restore"
	KindBuild         = "build"
	KindApply         = "apply"
	KindUpdateSecrets = "update_secrets"
	KindExec          = "exec"
	KindDrain         = "drain"
	// KindWake is a wake on its own (DECISIONS I-1642): a user route in
	// the http process, which holds no host's stream, asks for one the way
	// `repose start` asks for a start, and the process that holds the
	// stream sends it.
	KindWake = "wake"
)

// PlanCreate is build then create, for a Nix project.
func PlanCreate() []string { return PlanCreateFor(store.RuntimeNix) }

// PlanCreateFor is the create plan for a guest runtime (DECISIONS
// I-1464): an erofs project boots a published image and has nothing to
// build, so its create is the one phase.
func PlanCreateFor(runtime string) []string {
	if store.NormalizeRuntime(runtime) == store.RuntimeErofs {
		return []string{PhaseCreateGuest}
	}
	return []string{PhaseBuild, PhaseCreateGuest}
}

// isErofs reports whether a project boots the erofs base (I-1462).
func isErofs(p *store.Project) bool {
	return p != nil && store.NormalizeRuntime(p.GuestRuntime) == store.RuntimeErofs
}

// PlanStart is build (skipped when the revision is already built), start,
// then apply a built-but-unapplied revision.
func PlanStart(pendingRevision bool) []string {
	if pendingRevision {
		return []string{PhaseBuild, PhaseStartGuest, PhaseApplyConfig}
	}
	return []string{PhaseStartGuest}
}

// PlanStop is one stop. A machine that rests is woken first: hostd's
// stop asks guestd to shut the system down, and a paused guest answers
// nothing (DECISIONS I-818). The wake is an optimisation, not a
// requirement: a stop whose wake fails goes on without it (I-918,
// endsTheGuest), because hostd escalates past a guest that cannot
// answer.
func PlanStop() []string { return []string{PhaseWake, PhaseStopGuest} }

// endsTheGuest reports whether this op's whole purpose is to take the
// guest down, which is what makes a failed PhaseWake survivable: every
// phase after it works on a guest that need not answer (I-918).
func endsTheGuest(kind string) bool { return kind == KindStop || kind == KindDestroy }

// PlanDestroy stops the guest (without a snapshot), takes the final
// snapshot of the stopped volume and destroys (I-165); a project that
// never got a guest has nothing to send. A stopped guest skips the stop.
// Stopping first ends the guest's hours and sessions at once and gives a
// clean snapshot without guestd's freeze, which also makes a dead guestd
// (I-156) the ordinary path rather than a recovery.
//
// A temporary project (expires_at set, DECISIONS I-347) keeps nothing:
// DestroyGuest stops a running guest itself, and no snapshot is taken,
// whether the reaper or `repose rm` asked. Its plan names destroy_guest
// even before the project has a guest: the reaper queues behind an open
// op (allowQueue), which may be the create that places it, and the phase
// reads the guest when it is sent and skips when there is none (I-350).
func PlanDestroy(p *store.Project) []string {
	if p.ExpiresAt != nil {
		// No wake: a temporary project keeps nothing, and DestroyGuest
		// takes the machine down whether its vCPUs run or not (I-818).
		// Every other phase below reaches the guest, and the stop asks
		// guestd to shut the system down, so a running machine -- which
		// may be resting -- is woken first.
		return []string{PhaseDestroyGuest}
	}
	if p.GuestID == nil || p.HostID == nil {
		return []string{}
	}
	if p.State == "stopped" {
		return []string{PhaseSnapshot, PhaseDestroyGuest}
	}
	return []string{PhaseWake, PhaseStopGuest, PhaseSnapshot, PhaseDestroyGuest}
}

// PlanResize is one resize. The volume grows on the host, then guestd
// grows the filesystem inside, so the machine is woken first (I-818).
func PlanResize() []string { return []string{PhaseWake, PhaseResize} }

// PlanSnapshot is one snapshot. hostd freezes the guest's filesystem
// over vsock before it reads the volume, so the machine is woken first
// (I-818). The nightly snapshot is hostd's own timer and needs nothing
// here.
func PlanSnapshot() []string { return []string{PhaseWake, PhaseSnapshot} }

// PlanRestore destroys the stopped project's guest (keeping nothing),
// builds when the project has no closure yet, restores, and starts.
func PlanRestore(p *store.Project, hasClosure, start bool) []string {
	var ph []string
	if p.GuestID != nil && p.HostID != nil {
		ph = append(ph, PhaseDestroyGuest)
	}
	// An erofs project never builds (I-1464): the restore names a base
	// image and the start boots it.
	if !hasClosure && !isErofs(p) {
		ph = append(ph, PhaseBuild)
	}
	ph = append(ph, PhaseRestore)
	if start {
		ph = append(ph, PhaseStartGuest)
	}
	return ph
}

// UnfinishedRestore reports whether the project's newest create or
// restore is a restore that failed in its build or restore phase, which
// leaves the project with no usable volume (DECISIONS I-461): the old
// guest was destroyed first, or there was none (a restore as a new
// project). A restore that failed in destroy_guest left the old guest and
// its volume as they were, one that failed in start_guest restored the
// volume, and a later create or restore supersedes it.
func UnfinishedRestore(ctx context.Context, q store.Querier, projectID uuid.UUID) (bool, error) {
	u, err := unfinishedRestore(ctx, q, projectID)
	return u.unfinished, err
}

// restoreFailure is what a project's newest failed restore says.
type restoreFailure struct {
	unfinished bool
	// destroyedOld: the op's destroy_guest phase finished before the
	// failure, so the guest the project named then is gone and the host
	// has released its address.
	destroyedOld bool
}

func unfinishedRestore(ctx context.Context, q store.Querier, projectID uuid.UUID) (restoreFailure, error) {
	var kind, state string
	var step int
	var params map[string]any
	err := q.QueryRow(ctx, `select kind, state, step, params from ops where project_id = $1 and kind in ('create', 'restore') order by created_at desc limit 1`, projectID).Scan(&kind, &state, &step, &params)
	if db.IsNoRows(err) {
		return restoreFailure{}, nil
	}
	if err != nil {
		return restoreFailure{}, err
	}
	if kind != KindRestore || state != "error" {
		return restoreFailure{}, nil
	}
	op := &store.Op{Step: step, Params: params}
	ph := currentPhase(op)
	if ph != PhaseBuild && ph != PhaseRestore {
		return restoreFailure{}, nil
	}
	return restoreFailure{unfinished: true, destroyedOld: slices.Contains(phases(op)[:step], PhaseDestroyGuest)}, nil
}

// PlanBuild builds and, when the guest runs, applies. The wake sits
// between them, not at the head: the build runs on the host and may take
// forty minutes, and the machine has to be awake for the apply, which is
// the first phase that reaches the guest (I-818).
func PlanBuild(running bool) []string {
	if running {
		return []string{PhaseBuild, PhaseWake, PhaseApplyConfig}
	}
	return []string{PhaseBuild}
}

// PlanBuildFor is a configuration change's plan for the project's
// runtime (DECISIONS I-1464). A Nix project's is PlanBuild. An erofs
// project's menu is applied inside the running guest, so it is wake then
// apply whatever the state: both phases skip on a machine that is not
// running, and the revision then waits as built for the next start,
// which sends its menu.
func PlanBuildFor(p *store.Project, running bool) []string {
	if isErofs(p) {
		return []string{PhaseWake, PhaseApplyConfig}
	}
	return PlanBuild(running)
}

// PlanStartFor is PlanStart for the project's runtime: an erofs start
// never builds, and its StartGuest carries the newest menu itself.
func PlanStartFor(p *store.Project, pendingRevision bool) []string {
	if isErofs(p) {
		return []string{PhaseStartGuest}
	}
	return PlanStart(pendingRevision)
}

// ApplyLogLine is the line a build op's log gets when its apply phase is
// sent (DECISIONS I-320); the CLI reads it as the start of "Switching the
// machine". Its wording is part of the build log's contract
// (docs/interfaces/api.md "Build log lines").
const ApplyLogLine = "switching the machine"

// PlanApply re-applies a built revision. Woken first (I-818): the switch
// runs inside the guest, and a paused guest's vsock call times out as
// guest_unresponsive, which the recovery turns into a reboot.
func PlanApply() []string { return []string{PhaseWake, PhaseApplyConfig} }

// PlanUpdateSecrets pushes the whole set to a running guest, woken first:
// the agent guide promises a new value reaches the next command within
// seconds (I-818).
func PlanUpdateSecrets() []string { return []string{PhaseWake, PhaseUpdateSecrets} }

// PlanExec runs one audited command, woken first (I-818): an operator's
// exec that answers guest_unresponsive on a resting machine is a false
// incident.
func PlanExec() []string { return []string{PhaseWake, PhaseExec} }

// PlanWake is the wake alone (I-1642).
func PlanWake() []string { return []string{PhaseWake} }

// PlanDrain drains a host.
func PlanDrain() []string { return []string{PhaseDrain} }

// delivery is what CreateGuest, StartGuest and Restore carry (I-26).
type delivery struct {
	secrets    []*hostdv1.Secret
	hostKey    []byte
	hostCert   []byte
	env        map[string]string
	principals []string
	project    []byte
	// triggers is the project's whole trigger set (DECISIONS I-1248). It
	// rides every create, start and restore for the reason the secrets
	// do: hostd keeps none of it, so a guest that lost it — a stop and
	// start, a reboot, a move to another host — comes back with it.
	triggers []*hostdv1.Trigger
	// values is every user secret's plaintext, for build-log redaction and
	// the fragment scan; it never leaves this package.
	values []string
}

func (e *Engine) projectAndUser(ctx context.Context, op *store.Op) (*store.Project, *store.User, error) {
	if op.ProjectID == nil {
		return nil, nil, errors.New("op has no project")
	}
	p, err := store.GetProject(ctx, e.pool, *op.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	u, err := store.GetUser(ctx, e.pool, p.UserID)
	if err != nil {
		return nil, nil, err
	}
	return p, u, nil
}

// delivery decrypts the project's secrets for the wire. The reserved
// names hold the guest's sshd material; a missing host key is generated
// and stored under them so every later start delivers the same key.
func (e *Engine) delivery(ctx context.Context, p *store.Project, u *store.User) (*delivery, error) {
	vals, err := e.sec.DecryptForGuest(ctx, p.ID.String())
	if err != nil {
		return nil, err
	}
	d := &delivery{env: map[string]string{"LANG": e.cfg.Lang}, principals: []string{p.ID.String()}}
	for _, v := range vals {
		switch v.Name {
		case "ssh_host_ed25519_key":
			d.hostKey = v.Value
		case "ssh_host_ed25519_key-cert.pub":
			d.hostCert = v.Value
		case "user_ca.pub":
		default:
			d.secrets = append(d.secrets, &hostdv1.Secret{Name: v.Name, Value: v.Value})
			d.values = append(d.values, string(v.Value))
		}
	}
	if d.hostKey == nil || d.hostCert == nil {
		principals := []string{p.Slug + "." + u.Handle}
		if p.GuestIP != nil {
			principals = append([]string{p.GuestIP.String()}, principals...)
		}
		hk, err := e.ca.NewGuestHostKey(ctx, principals)
		if err != nil {
			return nil, err
		}
		if err := e.sec.PutReserved(ctx, p.UserID.String(), p.ID.String(), "ssh_host_ed25519_key", hk.PrivateKeyPEM); err != nil {
			return nil, err
		}
		if err := e.sec.PutReserved(ctx, p.UserID.String(), p.ID.String(), "ssh_host_ed25519_key-cert.pub", []byte(hk.Certificate)); err != nil {
			return nil, err
		}
		d.hostKey, d.hostCert = hk.PrivateKeyPEM, []byte(hk.Certificate)
	}
	tz := "UTC"
	if p.TZ != nil && *p.TZ != "" {
		tz = *p.TZ
	} else if u.TZ != nil && *u.TZ != "" {
		tz = *u.TZ
	}
	d.env["TZ"] = tz
	remote := ""
	if p.RemoteURL != nil {
		remote = *p.RemoteURL
	}
	pj, err := json.Marshal(map[string]any{"project_id": p.ID.String(), "slug": p.Slug, "name": p.Name, "remote_url": remote, "user_handle": u.Handle, "class": p.Class, "tz": tz,
		// What the guest's start runs (I-502, I-503); an older guestd
		// ignores the key and starts tmux.
		"multiplexer": multiplexer.Normalize(p.Multiplexer)})
	if err != nil {
		return nil, err
	}
	d.project = pj
	if e.triggers != nil {
		set, err := e.triggers.ForGuest(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		d.triggers = set
	}
	return d, nil
}

// resignHostCert re-signs the guest's host certificate once its address
// is known, so the next start delivers a certificate with both
// principals.
func (e *Engine) resignHostCert(ctx context.Context, p *store.Project, u *store.User, ip string) error {
	vals, err := e.sec.DecryptForGuest(ctx, p.ID.String())
	if err != nil {
		return err
	}
	for _, v := range vals {
		if v.Name == "ssh_host_ed25519_key" {
			signer, err := parseSSHKey(v.Value)
			if err != nil {
				return err
			}
			line, err := e.ca.SignHostCert(ctx, signer, []string{ip, p.Slug + "." + u.Handle}, 10*365*24*time.Hour)
			if err != nil {
				return err
			}
			return e.sec.PutReserved(ctx, p.UserID.String(), p.ID.String(), "ssh_host_ed25519_key-cert.pub", []byte(line))
		}
	}
	return nil
}

// baseRef picks the base a build runs against: the revision's own
// base_version first (a base bump's revision names the new base while the
// project still records the old one; until I-134 the project's base won
// and every bump was built against the base it was leaving), then the
// project's, then the newest published, then the dev checkout.
func (e *Engine) baseRef(ctx context.Context, p *store.Project, rev *store.Revision) (version, ref string, err error) {
	if rev != nil && rev.BaseVersion != nil {
		b, err := store.GetBase(ctx, e.pool, *rev.BaseVersion)
		if err == nil {
			return b.Version, b.NixRev, nil
		}
		if !errors.Is(err, db.ErrNotFound) {
			return "", "", err
		}
	}
	if p.BaseVersion != nil {
		b, err := store.GetBase(ctx, e.pool, *p.BaseVersion)
		if err == nil {
			return b.Version, b.NixRev, nil
		}
		if !errors.Is(err, db.ErrNotFound) {
			return "", "", err
		}
	}
	b, err := store.LatestBase(ctx, e.pool)
	if err == nil {
		return b.Version, b.NixRev, nil
	}
	if errors.Is(err, db.ErrNotFound) {
		if e.cfg.BaseRef == "" {
			return "", "", &opError{code: "internal", msg: "no base version published; run `repose-admin base publish`"}
		}
		return "dev", e.cfg.BaseRef, nil
	}
	return "", "", err
}

func newCommandID() string { return store.NewID().String() }

// buildCommand produces the phase's command (with a fresh command_id the
// caller may overwrite) and the host it goes to. skip means the phase has
// nothing to send.
func (e *Engine) buildCommand(ctx context.Context, op *store.Op, phase string) (cmd *hostdv1.Command, hostID uuid.UUID, skip bool, err error) {
	if phase == PhaseDrain {
		if op.HostID == nil {
			return nil, uuid.Nil, false, errors.New("drain op has no host")
		}
		return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_Drain{Drain: &hostdv1.Drain{}}}, *op.HostID, false, nil
	}
	p, u, err := e.projectAndUser(ctx, op)
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	switch phase {
	case PhaseWake:
		// Skipped when there is nothing to wake, so a plan carrying the
		// phase costs nothing on a host without the feature and nothing
		// for a machine that is not running (I-818). Sent whether or not
		// the row says the machine rests: the command is idempotent and
		// answers in milliseconds, which is cheaper than the race
		// between a pause and this read.
		if p.GuestID == nil || p.HostID == nil || p.State != "running" {
			return nil, uuid.Nil, true, nil
		}
		if gone, err := e.hostGone(ctx, *p.HostID); err != nil {
			return nil, uuid.Nil, false, err
		} else if gone {
			return nil, uuid.Nil, true, nil // nobody expects that host back (I-924)
		}
		pauses, err := e.hostPauses(ctx, *p.HostID)
		if err != nil {
			return nil, uuid.Nil, false, err
		}
		if !pauses {
			return nil, uuid.Nil, true, nil
		}
		// A host that is not connected right now: the wake is skipped
		// rather than queued for its next Hello. The wake's timeout is
		// 30 seconds, and queueing one would fail an op in half a minute
		// that used to wait out a host's reconnect on its real phase
		// (which allows 10 to 30 minutes). hostd wakes the guest itself
		// before anything that needs guestd, and a guest_unresponsive
		// from a project the api believes resting is retried as a wake
		// (recover.go), so skipping here loses no safety.
		if !e.send.Connected(*p.HostID) {
			return nil, uuid.Nil, true, nil
		}
		// A wake op carries its caller's reason (`view`, `keep_awake`),
		// so hostd's line names what woke the machine (I-1642).
		reason := "op"
		if r, ok := op.Params["reason"].(string); ok && op.Kind == KindWake && r != "" {
			reason = r
		}
		return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_WakeGuest{WakeGuest: &hostdv1.WakeGuest{GuestId: p.GuestID.String(), Reason: reason}}}, *p.HostID, false, nil
	case PhaseBuild:
		return e.buildBuild(ctx, op, p)
	case PhaseCreateGuest:
		return e.buildCreate(ctx, op, p, u)
	case PhaseStartGuest:
		return e.buildStart(ctx, op, p, u)
	case PhaseStopGuest:
		if p.GuestID == nil || p.HostID == nil {
			return nil, uuid.Nil, true, nil
		}
		if gone, err := e.hostGone(ctx, *p.HostID); err != nil {
			return nil, uuid.Nil, false, err
		} else if gone {
			return nil, uuid.Nil, true, nil // nobody expects that host back (I-924)
		}
		// A destroy's stop does not snapshot when a snapshot phase follows
		// it (I-165); a destroy enqueued before I-165 with the old plan
		// (stop, destroy) still takes its snapshot in the stop. Every other
		// stop snapshots unless told not to.
		snap := op.Kind != KindDestroy || !slices.Contains(phases(op), PhaseSnapshot)
		if v, ok := op.Params["snapshot"].(bool); ok {
			snap = v
		}
		state := "stopping"
		if op.Kind == KindDestroy {
			state = "destroying" // the user asked for a destroy; that is what every list shows
		}
		if err := e.setState(ctx, p, state); err != nil {
			return nil, uuid.Nil, false, err
		}
		return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_StopGuest{StopGuest: &hostdv1.StopGuest{GuestId: p.GuestID.String(), SnapshotFirst: snap, TimeoutS: 60}}}, *p.HostID, false, nil
	case PhaseSnapshot:
		if p.GuestID == nil || p.HostID == nil {
			return nil, uuid.Nil, true, nil
		}
		if gone, err := e.hostGone(ctx, *p.HostID); err != nil {
			return nil, uuid.Nil, false, err
		} else if gone {
			return nil, uuid.Nil, true, nil // nobody expects that host back (I-924)
		}
		reason := snapshotReason(op)
		if op.Kind == KindDestroy {
			if err := e.setState(ctx, p, "destroying"); err != nil {
				return nil, uuid.Nil, false, err
			}
		}
		// hostd takes scheduled, stop or manual; a fork's snapshot (I-703)
		// is a manual one there, where the reason only labels its log line.
		if reason == "fork" {
			reason = "manual"
		}
		return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_Snapshot{Snapshot: &hostdv1.Snapshot{GuestId: p.GuestID.String(), Reason: reason}}}, *p.HostID, false, nil
	case PhaseDestroyGuest:
		if p.GuestID == nil || p.HostID == nil {
			return nil, uuid.Nil, true, nil
		}
		if gone, err := e.hostGone(ctx, *p.HostID); err != nil {
			return nil, uuid.Nil, false, err
		} else if gone {
			// Nothing to destroy on a host nobody expects back, and
			// addressing it waits out the phase timeout and then tells
			// the owner to try again -- which can never work, leaves the
			// project counting against their plan, and blocks the host's
			// retirement for good (I-924).
			if op.Kind == KindDestroy {
				if err := e.setState(ctx, p, "destroying"); err != nil {
					return nil, uuid.Nil, false, err
				}
			}
			return nil, uuid.Nil, true, nil
		}
		if op.Kind == KindDestroy {
			if err := e.setState(ctx, p, "destroying"); err != nil {
				return nil, uuid.Nil, false, err
			}
		}
		return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_DestroyGuest{DestroyGuest: &hostdv1.DestroyGuest{GuestId: p.GuestID.String(), KeepVolume: false}}}, *p.HostID, false, nil
	case PhaseResize:
		if p.GuestID == nil || p.HostID == nil {
			return nil, uuid.Nil, false, &opError{code: "invalid", msg: "project has no guest yet"}
		}
		nb, _ := op.Params["volume_bytes"].(float64)
		return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_ResizeVolume{ResizeVolume: &hostdv1.ResizeVolume{GuestId: p.GuestID.String(), NewBytes: uint64(nb)}}}, *p.HostID, false, nil
	case PhaseRestore:
		return e.buildRestore(ctx, op, p, u)
	case PhaseApplyConfig:
		return e.buildApply(ctx, op, p)
	case PhaseUpdateSecrets:
		if p.GuestID == nil || p.HostID == nil || p.State != "running" {
			return nil, uuid.Nil, true, nil
		}
		d, err := e.delivery(ctx, p, u)
		if err != nil {
			return nil, uuid.Nil, false, err
		}
		return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_UpdateSecrets{UpdateSecrets: &hostdv1.UpdateSecrets{GuestId: p.GuestID.String(), Secrets: d.secrets}}}, *p.HostID, false, nil
	case PhaseExec:
		if p.GuestID == nil || p.HostID == nil {
			return nil, uuid.Nil, false, &opError{code: "invalid", msg: "project has no guest"}
		}
		if op.AuditID == nil {
			return nil, uuid.Nil, false, &opError{code: "invalid", msg: "exec requires an audit id"}
		}
		var argv []string
		if raw, ok := op.Params["argv"].([]any); ok {
			for _, a := range raw {
				if s, ok := a.(string); ok {
					argv = append(argv, s)
				}
			}
		}
		timeout, _ := op.Params["timeout_s"].(float64)
		if timeout == 0 {
			timeout = 60
		}
		return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_Exec{Exec: &hostdv1.Exec{GuestId: p.GuestID.String(), Argv: argv, TimeoutS: uint32(timeout), AuditId: op.AuditID.String()}}}, *p.HostID, false, nil
	}
	return nil, uuid.Nil, false, fmt.Errorf("unknown phase %q", phase)
}

// snapshotReason is the reason a snapshot phase records: the op's, else
// `stop` for a destroy's final snapshot (taken of the stopped volume,
// I-165) and `manual` for anything else.
func snapshotReason(op *store.Op) string {
	if r, ok := op.Params["reason"].(string); ok && r != "" {
		return r
	}
	if op.Kind == KindDestroy {
		return "stop"
	}
	return "manual"
}

func (e *Engine) setState(ctx context.Context, p *store.Project, state string) error {
	if p.State == state {
		return nil
	}
	if err := store.SetProjectState(ctx, e.pool, p.ID, state); err != nil {
		return err
	}
	e.log.Info("project state", "event", "guest_state", "project_id", p.ID.String(), "state", state)
	p.State = state
	return nil
}

func (e *Engine) revisionFor(ctx context.Context, op *store.Op, p *store.Project) (*store.Revision, error) {
	id := op.RevisionID
	if id == nil {
		id = p.ConfigRevisionID
	}
	if id == nil {
		return nil, &opError{code: "invalid", msg: "project has no config revision"}
	}
	return store.GetRevision(ctx, e.pool, *id)
}

func (e *Engine) buildBuild(ctx context.Context, op *store.Op, p *store.Project) (*hostdv1.Command, uuid.UUID, bool, error) {
	if isErofs(p) {
		// Nothing to build (I-1464): an op enqueued with a Nix plan for
		// an erofs project skips its build rather than sending one.
		return nil, uuid.Nil, true, nil
	}
	if op.Kind == KindStart {
		rev, needBuild, err := e.resolveStartRevision(ctx, op, p)
		if err != nil {
			return nil, uuid.Nil, false, err
		}
		if !needBuild || rev == nil {
			return nil, uuid.Nil, true, nil
		}
	}
	rev, err := e.revisionFor(ctx, op, p)
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	// A fragment carrying a current secret value is refused before
	// evaluation (docs/features/secrets.md).
	vals, err := e.sec.DecryptForGuest(ctx, p.ID.String())
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	var redact []string
	for _, v := range vals {
		if secrets.IsReserved(v.Name) {
			continue
		}
		redact = append(redact, string(v.Value))
		// The same strings the log redaction looks for, so a key pasted
		// one line at a time, or base64-encoded, is refused too.
		for _, n := range buildlog.Needles([]string{string(v.Value)}) {
			if strings.Contains(rev.Fragment, n) {
				return nil, uuid.Nil, false, &opError{code: "invalid", msg: "fragment contains the value of secret " + v.Name}
			}
			// The personal layer is under the same rule (I-490): it is
			// the account's, the secret is this project's, and the
			// refusal names the file the user edits.
			if rev.Personal != "" && strings.Contains(rev.Personal, n) {
				return nil, uuid.Nil, false, &opError{code: "invalid", msg: PersonalName + " contains the value of secret " + v.Name}
			}
		}
	}
	e.logs.SetRedactions(op.ID, redact)
	version, ref, err := e.baseRef(ctx, p, rev)
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	hostID, err := e.placeOp(ctx, op, p)
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	if op.Kind == KindCreate || op.Kind == KindBuild || op.Kind == KindStart {
		reused, err := e.reuseBuild(ctx, p, rev, version, *hostID, op.Kind == KindCreate)
		if err != nil {
			return nil, uuid.Nil, false, err
		}
		if reused {
			return nil, uuid.Nil, true, nil
		}
	}
	if op.Kind == KindCreate {
		if rev.Personal != "" {
			// Never block (DECISIONS I-490): the combined configuration
			// is not on this host, so the machine is created on the
			// project layer alone and the combined revision is built and
			// applied in place once it runs.
			if rev, err = e.deferPersonal(ctx, op, p, rev, version); err != nil {
				return nil, uuid.Nil, false, err
			}
			reused, err := e.reuseBuild(ctx, p, rev, version, *hostID, true)
			if err != nil {
				return nil, uuid.Nil, false, err
			}
			if reused {
				return nil, uuid.Nil, true, nil
			}
		}
	}
	if op.CommandID == nil && e.cfg.MaxHostBuilds > 0 {
		var inflight int
		if err := e.pool.QueryRow(ctx, hostBuildInFlightSQL, *hostID, op.ID).Scan(&inflight); err != nil {
			return nil, uuid.Nil, false, err
		}
		if inflight >= e.cfg.MaxHostBuilds {
			return nil, uuid.Nil, false, errBuildWait
		}
	}
	if op.Kind == KindCreate {
		if err := e.setState(ctx, p, "building"); err != nil {
			return nil, uuid.Nil, false, err
		}
	}
	if _, err := e.pool.Exec(ctx, "update config_revisions set status = 'building', base_version = coalesce(base_version, $2) where id = $1", rev.ID, version); err != nil {
		return nil, uuid.Nil, false, err
	}
	if rev.BaseVersion == nil {
		if _, err := e.pool.Exec(ctx, "update projects set base_version = coalesce(base_version, $2) where id = $1", p.ID, version); err != nil {
			return nil, uuid.Nil, false, err
		}
	}
	l := e.cfg.Limits
	return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_Build{Build: &hostdv1.Build{
		ProjectId: p.ID.String(), RevisionId: rev.ID.String(), Fragment: []byte(rev.Fragment), BaseRef: ref, BaseVersion: version,
		Personal: []byte(rev.Personal),
		Limits:   &hostdv1.Limits{EvalS: l.EvalS, BuildS: l.BuildS, Cores: l.Cores, ClosureBytes: l.ClosureBytes},
	}}}, *hostID, false, nil
}

// PersonalName is the personal layer's name in what users read: the file
// is ~/.config/repose/machine.nix on the laptop (DECISIONS I-490).
const PersonalName = "machine.nix"

// deferPersonal turns a create whose combined configuration is not on the
// host into a create on the project layer alone plus a build of the
// combined revision once the guest runs (DECISIONS I-490). It records a
// copy of rev without the personal layer, points the op and the project
// at it, and remembers rev in the op's params; onResult enqueues rev's
// build when create_guest succeeds. A re-sent build phase finds the op on
// the copy, whose personal text is empty, and does not come back here.
func (e *Engine) deferPersonal(ctx context.Context, op *store.Op, p *store.Project, rev *store.Revision, version string) (*store.Revision, error) {
	baseID := store.NewID()
	err := db.InTx(ctx, e.pool, func(tx db.Tx) error {
		if _, err := tx.Exec(ctx, `insert into config_revisions (id, project_id, fragment, menu, base_version, status, created_at)
			values ($1, $2, $3, $4, coalesce($5, $6), 'building', (select created_at - interval '1 millisecond' from config_revisions where id = $7))`,
			baseID, p.ID, rev.Fragment, rev.Menu, rev.BaseVersion, version, rev.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update config_revisions set base_version = coalesce(base_version, $2) where id = $1", rev.ID, version); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update ops set revision_id = $2, params = params || jsonb_build_object('personal_revision', $3::text) where id = $1`, op.ID, baseID, rev.ID.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "update projects set config_revision_id = $2 where id = $1 and config_revision_id = $3", p.ID, baseID, rev.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	op.RevisionID = &baseID
	op.Params["personal_revision"] = rev.ID.String()
	if p.ConfigRevisionID != nil && *p.ConfigRevisionID == rev.ID {
		p.ConfigRevisionID = &baseID
	}
	e.log.Info("personal layer deferred past create", "event", "personal_deferred", "project_id", p.ID.String(), "revision_id", rev.ID.String())
	return store.GetRevision(ctx, e.pool, baseID)
}

// enqueuePersonalBuild queues the build and apply of a create's combined
// revision behind the create (DECISIONS I-490), once: a result handled
// twice finds the op already there.
func (e *Engine) enqueuePersonalBuild(ctx context.Context, p *store.Project, rid uuid.UUID) error {
	var n int
	if err := e.pool.QueryRow(ctx, "select count(*) from ops where revision_id = $1", rid).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	// A personal change queued while the machine was being created read
	// the account's text later than this revision did, so it wins.
	if err := e.pool.QueryRow(ctx, "select count(*) from ops where project_id = $1 and kind = 'build' and params ? 'personal' and state in ('pending', 'running')", p.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		_, err := e.pool.Exec(ctx, "update config_revisions set status = 'failed', error = 'superseded: a newer machine.nix change replaced it' where id = $1 and status = 'building'", rid)
		return err
	}
	pid := p.ID
	if _, err := e.Enqueue(ctx, e.pool, NewOp{Kind: KindBuild, ProjectID: &pid, RevisionID: &rid, Params: map[string]any{"personal": "create"}, Phases: PlanBuild(true)}, true); err != nil {
		return err
	}
	e.Kick()
	return nil
}

// hostBuildInFlightSQL counts running ops on host $1 whose current phase
// is build and whose command has already been assigned (command_id is
// not null), excluding op $2 itself (DECISIONS I-1454).
const hostBuildInFlightSQL = `select count(*) from ops
	where host_id = $1 and state = 'running' and command_id is not null
	  and params->'phases'->>step = 'build' and id <> $2`

// reusableClosureSQL finds a closure already built on host $1 from the
// same fragment ($2) and personal layer ($5) on the same base version
// ($3): a built or applied revision of another live project there whose
// guest exists. Those are the only inputs of the system closure (the
// project id, name, class, address, tz and user arrive at run time,
// I-34, I-43), and that guest's GC root holds the path on the host, so
// nothing has to be built or copied (DECISIONS I-160, I-1454). A
// create's revision recorded no base_version before I-160, so the base is
// the project's for those rows when the revision is the active one.
const reusableClosureSQL = `select r.system_closure, coalesce(r.closure_bytes, 0), r.kernel_changed
	from projects q join config_revisions r on r.project_id = q.id
	where q.host_id = $1 and q.id <> $4 and q.destroyed_at is null and q.guest_id is not null
	  and q.state not in ('destroying', 'error')
	  and r.status in ('built', 'applied') and r.system_closure is not null and r.system_closure <> ''
	  and r.fragment = $2 and coalesce(r.base_version, case when r.id = q.config_revision_id then q.base_version end) = $3
	  and r.personal = $5
	order by r.applied_at desc nulls last, r.built_at desc nulls last limit 1`

// reuseBuild completes a build phase without a Build command when the
// host already holds the closure it would produce (DECISIONS I-160,
// I-1454). The revision becomes `built` with that closure; for a new
// guest (isCreate), kernel_changed is false because it has booted nothing
// yet.
func (e *Engine) reuseBuild(ctx context.Context, p *store.Project, rev *store.Revision, version string, hostID uuid.UUID, isCreate bool) (bool, error) {
	// "dev" is the api's --base-ref checkout, a name that can move; only a
	// published base pins one git revision.
	if version == "dev" || version == "" {
		return false, nil
	}
	var closure string
	var bytes int64
	var kernelChanged bool
	err := e.pool.QueryRow(ctx, reusableClosureSQL, hostID, rev.Fragment, version, p.ID, rev.Personal).Scan(&closure, &bytes, &kernelChanged)
	if db.IsNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if isCreate {
		kernelChanged = false
	}
	if _, err := e.pool.Exec(ctx, `update config_revisions set status = 'built', base_version = coalesce(base_version, $2),
		system_closure = $3, closure_bytes = $4, kernel_changed = $5, built_at = now(), error = null, fragment_line = null, personal_line = null
		where id = $1`, rev.ID, version, closure, bytes, kernelChanged); err != nil {
		return false, err
	}
	if rev.BaseVersion == nil {
		if _, err := e.pool.Exec(ctx, "update projects set base_version = coalesce(base_version, $2) where id = $1", p.ID, version); err != nil {
			return false, err
		}
	}
	e.log.Info("build reused", "event", "build_reused", "project_id", p.ID.String(), "revision_id", rev.ID.String(), "host_id", hostID.String())
	return true, nil
}

func (e *Engine) buildCreate(ctx context.Context, op *store.Op, p *store.Project, u *store.User) (*hostdv1.Command, uuid.UUID, bool, error) {
	if p.HostID == nil && isErofs(p) {
		// No build before it to place the project (I-1464).
		if _, err := e.placeOp(ctx, op, p); err != nil {
			return nil, uuid.Nil, false, err
		}
	}
	if p.HostID == nil {
		return nil, uuid.Nil, false, errors.New("create_guest before placement")
	}
	rev, err := e.revisionFor(ctx, op, p)
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	var bi *hostdv1.BaseImage
	if isErofs(p) {
		if bi, err = e.erofsBase(ctx, op, p, *p.HostID); err != nil {
			return nil, uuid.Nil, false, err
		}
		op.Params["menu_revision_id"] = rev.ID.String()
	} else if rev.SystemClosure == nil {
		return nil, uuid.Nil, false, &opError{code: "internal", msg: "revision has no closure"}
	}
	if p.GuestID == nil {
		gid := store.NewID()
		if _, err := e.pool.Exec(ctx, "update projects set guest_id = $2 where id = $1", p.ID, gid); err != nil {
			return nil, uuid.Nil, false, err
		}
		p.GuestID = &gid
	}
	d, err := e.delivery(ctx, p, u)
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	if err := e.setState(ctx, p, "starting"); err != nil {
		return nil, uuid.Nil, false, err
	}
	remote := ""
	if p.RemoteURL != nil {
		remote = *p.RemoteURL
	}
	cg := &hostdv1.CreateGuest{
		ProjectId: p.ID.String(), GuestId: p.GuestID.String(), Class: p.Class, VolumeBytes: uint64(p.VolumeBytes),
		Secrets: d.secrets, Env: d.env, SshCaPub: e.ca.UserCAPub(), Principals: d.principals, HostKey: d.hostKey, HostCert: d.hostCert,
		UserId: p.UserID.String(), ProjectSlug: p.Slug, RemoteUrl: remote, ProjectJson: d.project, Triggers: d.triggers,
	}
	if bi != nil {
		cg.BaseImage, cg.Menu = bi, MenuSpecOf(rev)
	} else {
		cg.SystemClosure = *rev.SystemClosure
	}
	return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_CreateGuest{CreateGuest: cg}}, *p.HostID, false, nil
}

func (e *Engine) buildStart(ctx context.Context, op *store.Op, p *store.Project, u *store.User) (*hostdv1.Command, uuid.UUID, bool, error) {
	if p.GuestID == nil || p.HostID == nil {
		return nil, uuid.Nil, false, &opError{code: "invalid", msg: "project has no guest; create it first"}
	}
	d, err := e.delivery(ctx, p, u)
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	sg := &hostdv1.StartGuest{
		GuestId: p.GuestID.String(), Secrets: d.secrets, Env: d.env, SshCaPub: e.ca.UserCAPub(), Principals: d.principals, HostKey: d.hostKey, HostCert: d.hostCert, ProjectJson: d.project,
		Class:    p.Class, // I-260: a class changed while stopped applies at this start
		Triggers: d.triggers,
	}
	if isErofs(p) {
		// The base the project should boot (its pending one, I-1464) and
		// its newest menu, which a stopped machine's menu change waits
		// for.
		bi, err := e.erofsBase(ctx, op, p, *p.HostID)
		if err != nil {
			return nil, uuid.Nil, false, err
		}
		rev, err := e.erofsStartRevision(ctx, p)
		if err != nil {
			return nil, uuid.Nil, false, err
		}
		sg.BaseImage, sg.Menu = bi, MenuSpecOf(rev)
		if rev != nil {
			op.Params["menu_revision_id"] = rev.ID.String()
		}
	}
	if err := e.setState(ctx, p, "starting"); err != nil {
		return nil, uuid.Nil, false, err
	}
	return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_StartGuest{StartGuest: sg}}, *p.HostID, false, nil
}

func (e *Engine) buildRestore(ctx context.Context, op *store.Op, p *store.Project, u *store.User) (*hostdv1.Command, uuid.UUID, bool, error) {
	if op.SnapshotID == nil {
		return nil, uuid.Nil, false, &opError{code: "invalid", msg: "restore needs a snapshot"}
	}
	snap, err := store.GetSnapshot(ctx, e.pool, *op.SnapshotID)
	if err != nil {
		return nil, uuid.Nil, false, &opError{code: "not_found", msg: "snapshot not found"}
	}
	// Mark the snapshot as in use by this op inside the same lock the
	// expiry job takes, so it is never deleted under a running restore.
	err = db.InTx(ctx, e.pool, func(tx db.Tx) error {
		var deleted *time.Time
		if err := tx.QueryRow(ctx, "select deleted_at from snapshots where id = $1 for update", snap.ID).Scan(&deleted); err != nil {
			return err
		}
		if deleted != nil {
			return &opError{code: "not_found", msg: "snapshot was deleted"}
		}
		_, err := tx.Exec(ctx, "update snapshots set restoring_op_id = $2 where id = $1", snap.ID, op.ID)
		return err
	})
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	// The new guest id is fixed on first build so a re-send is identical.
	gidStr, _ := op.Params["new_guest_id"].(string)
	if gidStr == "" {
		gidStr = store.NewID().String()
		op.Params["new_guest_id"] = gidStr
	}
	// Host: the project's if it can take placements, else pick one.
	hostID := uuid.Nil
	if p.HostID != nil {
		h, err := store.GetHost(ctx, e.pool, *p.HostID)
		if err == nil && h.State == "ready" && !h.Draining {
			hostID = h.ID
		}
	}
	if to, ok := op.Params["to_host_id"].(string); ok && to != "" {
		hostID = uuid.MustParse(to)
	}
	if hostID == uuid.Nil {
		err := db.InTx(ctx, e.pool, func(tx db.Tx) error {
			t, err := e.target(ctx, tx, p)
			if err != nil {
				return err
			}
			pk, err := scheduler.PickHost(ctx, tx, t, p.Class, p.HeldBytes(), p.VolumeBytes, e.now())
			if err != nil {
				return err
			}
			hostID = pk.HostID
			return nil
		})
		if errors.Is(err, scheduler.ErrNoCapacity) && e.placementWaits(ctx, op, p) {
			return nil, uuid.Nil, false, errPlacementWait
		}
		if errors.Is(err, scheduler.ErrNoOwnHost) {
			e.m.ScheduleTotal.WithLabelValues("no_own_host").Inc()
			return nil, uuid.Nil, false, e.ownHostRefusal(ctx, p)
		}
		if errors.Is(err, scheduler.ErrNoCapacity) {
			return nil, uuid.Nil, false, errCapacity()
		}
		if err != nil {
			return nil, uuid.Nil, false, err
		}
	}
	// Whether this restore leaves the project's host, fixed on the first
	// build so a re-send says the same (I-142): the host_moved event is
	// for a project that came up somewhere else, not for a restore over
	// the same host's volume.
	// A new project (a restore as a new project) had no host, so it moved
	// from nowhere: not a move either.
	if _, ok := op.Params["host_moved"]; !ok {
		op.Params["host_moved"] = p.HostID != nil && *p.HostID != hostID
	}
	if _, err := e.pool.Exec(ctx, "update projects set host_id = $2, guest_id = $3, guest_ip = null, vsock_cid = null where id = $1", p.ID, hostID, gidStr); err != nil {
		return nil, uuid.Nil, false, err
	}
	p.HostID = &hostID
	gid := uuid.MustParse(gidStr)
	p.GuestID = &gid
	if err := e.setState(ctx, p, "restoring"); err != nil {
		return nil, uuid.Nil, false, err
	}
	d, err := e.delivery(ctx, p, u)
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	closure := ""
	if p.ConfigRevisionID != nil {
		if rev, err := store.GetRevision(ctx, e.pool, *p.ConfigRevisionID); err == nil && rev.SystemClosure != nil {
			closure = *rev.SystemClosure
		}
	}
	if op.RevisionID != nil {
		if rev, err := store.GetRevision(ctx, e.pool, *op.RevisionID); err == nil && rev.SystemClosure != nil {
			closure = *rev.SystemClosure
		}
	}
	remote := ""
	if p.RemoteURL != nil {
		remote = *p.RemoteURL
	}
	r := &hostdv1.Restore{
		ProjectId: p.ID.String(), GuestId: gidStr, Class: p.Class, VolumeBytes: uint64(p.VolumeBytes), SystemClosure: closure,
		Secrets: d.secrets, Env: d.env, SshCaPub: e.ca.UserCAPub(), Principals: d.principals, HostKey: d.hostKey, HostCert: d.hostCert,
		UserId: p.UserID.String(), ProjectSlug: p.Slug, RemoteUrl: remote, ProjectJson: d.project, Triggers: d.triggers,
	}
	r.BlobPath, r.Sha256 = snap.BlobPath, sha256Of(snap)
	if isErofs(p) {
		// A base image, never a closure (I-1458): both set is refused.
		bi, err := e.erofsBase(ctx, op, p, hostID)
		if err != nil {
			return nil, uuid.Nil, false, err
		}
		r.SystemClosure, r.BaseImage = "", bi
	}
	return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_Restore{Restore: r}}, hostID, false, nil
}

// sha256Of is the digest a Restore checks the blob against (I-462): ""
// for a snapshot taken before digests were recorded, restored unchecked.
func sha256Of(s *store.Snapshot) string {
	if s.Sha256 == nil {
		return ""
	}
	return *s.Sha256
}

// PendingRevisionSQL selects the revision a start applies: the newest
// built one with a closure, newer than the project's current revision
// ($2, nullable). The http start route asks the same question (I-147).
const pendingRevisionSQL = `select id from config_revisions
	where project_id = $1 and status = 'built' and system_closure is not null
	  and created_at > coalesce((select created_at from config_revisions where id = $2), 'epoch'::timestamptz)
	order by created_at desc limit 1`

// ensureStartBaseRevision prepares a config_revision on the newest
// published base version when a stopped, unheld project is behind that
// base and has no revision for it yet (DECISIONS I-1454). It reports
// whether a revision for the latest base is now pending (built or
// building) for the start op.
func ensureStartBaseRevision(ctx context.Context, q store.Querier, p *store.Project) (bool, error) {
	if p.State != "stopped" || p.HoldBaseUpdates || p.ConfigRevisionID == nil || isErofs(p) {
		// An erofs project's base is pending_base_version, sent by the
		// start itself, not a revision to build (I-1464).
		return false, nil
	}
	latest, err := store.LatestBase(ctx, q)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) || db.IsNoRows(err) {
			return false, nil
		}
		return false, err
	}
	if p.BaseVersion != nil && *p.BaseVersion == latest.Version {
		return false, nil
	}
	revs, err := store.ListRevisions(ctx, q, p.ID)
	if err != nil {
		return false, err
	}
	if len(revs) > 0 && revs[0].BaseVersion != nil && *revs[0].BaseVersion == latest.Version {
		switch revs[0].Status {
		case "built", "building", "pending":
			return true, nil
		case "failed":
			return false, nil
		}
	}
	cur, err := store.GetRevision(ctx, q, *p.ConfigRevisionID)
	if err != nil {
		return false, err
	}
	for i := range revs {
		if revs[i].CreatedAt.After(cur.CreatedAt) && revs[i].Status == "built" {
			cur = &revs[i]
			break
		}
	}
	rid := store.NewID()
	if _, err := q.Exec(ctx, `insert into config_revisions (id, project_id, fragment, menu, base_version, status, personal, personal_revision_id, personal_opt_out)
		values ($1, $2, $3, $4, $5, 'building', $6, $7, $8)`,
		rid, p.ID, cur.Fragment, cur.Menu, latest.Version, cur.Personal, cur.PersonalRevisionID, cur.PersonalOptOut); err != nil {
		return false, err
	}
	return true, nil
}

// resolveStartRevision locates (or prepares) the pending revision for a
// start op's PhaseBuild and reports whether PhaseBuild needs to run. When
// the revision is already built, PhaseBuild is skipped and PhaseApplyConfig
// applies it after StartGuest (DECISIONS I-1454).
func (e *Engine) resolveStartRevision(ctx context.Context, op *store.Op, p *store.Project) (*store.Revision, bool, error) {
	if op.RevisionID == nil {
		if _, err := ensureStartBaseRevision(ctx, e.pool, p); err != nil {
			return nil, false, err
		}
		var rid uuid.UUID
		err := e.pool.QueryRow(ctx, `select id from config_revisions
			where project_id = $1 and status in ('building', 'built')
			  and created_at > coalesce((select created_at from config_revisions where id = $2), 'epoch'::timestamptz)
			order by created_at desc limit 1`, p.ID, p.ConfigRevisionID).Scan(&rid)
		if err != nil {
			if db.IsNoRows(err) {
				return nil, false, nil
			}
			return nil, false, err
		}
		if _, err := e.pool.Exec(ctx, "update ops set revision_id = $2 where id = $1", op.ID, rid); err != nil {
			return nil, false, err
		}
		op.RevisionID = &rid
	}
	rev, err := store.GetRevision(ctx, e.pool, *op.RevisionID)
	if err != nil {
		return nil, false, err
	}
	if rev.Status == "built" && rev.SystemClosure != nil && *rev.SystemClosure != "" {
		return rev, false, nil
	}
	return rev, true, nil
}

// PendingRevision reports whether a start of p would apply a revision,
// preparing a base-update revision first when p is stopped, unheld, and
// behind the newest published base (DECISIONS I-1454).
func PendingRevision(ctx context.Context, q store.Querier, p *store.Project) (bool, error) {
	if isErofs(p) {
		return false, nil // the start carries the newest menu itself (I-1464)
	}
	ok, err := ensureStartBaseRevision(ctx, q, p)
	if err != nil || ok {
		return ok, err
	}
	var rid uuid.UUID
	err = q.QueryRow(ctx, pendingRevisionSQL, p.ID, p.ConfigRevisionID).Scan(&rid)
	if err != nil {
		if db.IsNoRows(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (e *Engine) buildApply(ctx context.Context, op *store.Op, p *store.Project) (*hostdv1.Command, uuid.UUID, bool, error) {
	if isErofs(p) {
		return e.buildApplyMenu(ctx, op, p)
	}
	// A restart applies to the stopped guest between its stop and its boot:
	// hostd moves a stopped guest's root without guestd, and the boot comes
	// up on the new closure (I-157).
	restarting := op.Kind == KindStart && isRestart(op) && p.State == "stopped"
	if p.GuestID == nil || p.HostID == nil || (p.State != "running" && !restarting) {
		return nil, uuid.Nil, true, nil // applies at the next start
	}
	// A start applies the newest built-but-unapplied revision that is newer
	// than the one the guest runs, fixed on the op so the result handler
	// sees the same one. A `built` row older than the applied one is
	// superseded, not pending (I-147: m3-held's start on 2026-09-21
	// re-applied its 2026.09.21.3 row over the applied .4 and ran that
	// older base's activation).
	if op.Kind == KindStart && op.RevisionID == nil {
		var rid uuid.UUID
		err := e.pool.QueryRow(ctx, pendingRevisionSQL, p.ID, p.ConfigRevisionID).Scan(&rid)
		if err != nil {
			if db.IsNoRows(err) {
				return nil, uuid.Nil, true, nil
			}
			return nil, uuid.Nil, false, err
		}
		if _, err := e.pool.Exec(ctx, "update ops set revision_id = $2 where id = $1", op.ID, rid); err != nil {
			return nil, uuid.Nil, false, err
		}
		op.RevisionID = &rid
	}
	rev, err := e.revisionFor(ctx, op, p)
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	if rev.SystemClosure == nil {
		return nil, uuid.Nil, false, &opError{code: "invalid", msg: "revision is not built"}
	}
	if p.ConfigRevisionID != nil && *p.ConfigRevisionID == rev.ID && rev.Status == "applied" && op.Kind != KindApply {
		return nil, uuid.Nil, true, nil
	}
	force, _ := op.Params["reboot"].(bool)
	if op.Kind == KindStart && rev.KernelChanged {
		force = true
	}
	return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_ApplyConfig{ApplyConfig: &hostdv1.ApplyConfig{GuestId: p.GuestID.String(), SystemClosure: *rev.SystemClosure, ForceReboot: force}}}, *p.HostID, false, nil
}

// onResult applies a successful phase result.
func (e *Engine) onResult(ctx context.Context, op *store.Op, phase string, res *hostdv1.Result) error {
	if phase == PhaseDrain {
		_, err := e.pool.Exec(ctx, "update hosts set draining = true, state = case when state = 'ready' then 'draining' else state end where id = $1", *op.HostID)
		return err
	}
	p, u, err := e.projectAndUser(ctx, op)
	if err != nil {
		return err
	}
	switch phase {
	case PhaseBuild:
		b := res.GetBuild()
		if b == nil {
			return errors.New("build result without payload")
		}
		rev, err := e.revisionFor(ctx, op, p)
		if err != nil {
			return err
		}
		_, err = e.pool.Exec(ctx, "update config_revisions set status = 'built', system_closure = $2, closure_bytes = $3, kernel_changed = $4, built_at = now(), error = null, fragment_line = null, personal_line = null where id = $1",
			rev.ID, b.SystemClosure, int64(b.ClosureBytes), b.KernelChanged)
		return err
	case PhaseCreateGuest, PhaseRestore:
		c := res.GetCreate()
		if c == nil {
			return errors.New("create result without payload")
		}
		if _, err := e.pool.Exec(ctx, "update projects set guest_ip = $2, vsock_cid = $3, host_unreachable = false, last_error = null where id = $1", p.ID, c.GuestIp, int32(c.VsockCid)); err != nil {
			return err
		}
		if phase == PhaseCreateGuest {
			if err := setVMSize(ctx, e.pool, p.ID, c.GetVm()); err != nil {
				return err
			}
		}
		if err := e.resignHostCert(ctx, p, u, c.GuestIp); err != nil {
			return err
		}
		if phase == PhaseCreateGuest && isErofs(p) {
			if err := e.onErofsBooted(ctx, op, p, "", c.GetMenuError()); err != nil {
				return err
			}
		}
		if phase == PhaseCreateGuest {
			rev, err := e.revisionFor(ctx, op, p)
			if err != nil {
				return err
			}
			if _, err := e.pool.Exec(ctx, "update config_revisions set status = 'applied', applied_at = now() where id = $1", rev.ID); err != nil {
				return err
			}
			if _, err := e.pool.Exec(ctx, "update projects set config_revision_id = $2 where id = $1", p.ID, rev.ID); err != nil {
				return err
			}
			if err := e.setState(ctx, p, "running"); err != nil {
				return err
			}
			if s, ok := op.Params["personal_revision"].(string); ok && s != "" {
				if rid, err := uuid.Parse(s); err == nil {
					if err := e.enqueuePersonalBuild(ctx, p, rid); err != nil {
						return err
					}
				}
			}
		} else {
			if _, err := e.pool.Exec(ctx, "update snapshots set restoring_op_id = null where restoring_op_id = $1", op.ID); err != nil {
				return err
			}
			if err := e.setState(ctx, p, "stopped"); err != nil {
				return err
			}
			if moved, _ := op.Params["host_moved"].(bool); moved {
				// The operator's record of the migration, for every move.
				// The user hears about it from start_guest, and only if
				// there is one (DECISIONS I-964).
				e.log.Info("project restored onto another host", "event", "host_moved", "project_id", p.ID.String(), "host_id", p.HostID.String())
			} else {
				e.log.Info("project restored", "event", "restored", "project_id", p.ID.String(), "host_id", p.HostID.String())
			}
		}
		fields := map[string]any{"guest_ip": c.GuestIp}
		if b := bootTimings(c.Boot); b != nil {
			fields["boot"] = b
		}
		return e.setResult(ctx, op, fields)
	case PhaseStartGuest:
		if b := bootTimings(res.GetStart().GetBoot()); b != nil {
			if err := e.setResult(ctx, op, map[string]any{"boot": b}); err != nil {
				return err
			}
		}
		if st := res.GetStart(); st != nil && st.FailedClosure != "" {
			return e.onBootFallback(ctx, op, p, st)
		}
		if isErofs(p) {
			if err := e.onErofsBooted(ctx, op, p, res.GetStart().GetBootedBaseVersion(), res.GetStart().GetMenuError()); err != nil {
				return err
			}
		}
		// A warning an earlier phase of this start left (its apply
		// failed, I-590) stays the project's last word; otherwise the
		// start clears the last error.
		var lastErr any
		if w, ok := op.Params["warning"].(map[string]any); ok {
			c, _ := w["code"].(string)
			m, _ := w["message"].(string)
			lastErr = c + ": " + m
		}
		if _, err := e.pool.Exec(ctx, "update projects set host_unreachable = false, last_error = $2 where id = $1", p.ID, lastErr); err != nil {
			return err
		}
		if err := setVMSize(ctx, e.pool, p.ID, res.GetStart().GetVm()); err != nil {
			return err
		}
		// StartResult is the only result of a start, and until I-720 it
		// carried no address: a start after a create that failed past the
		// guest's own boot left the project running and unroutable, which
		// no later message corrected (prod host-01 sent two Hellos in the
		// 72 h to 2026-10-13, so the Hello reconcile alone would have
		// taken days).
		if st := res.GetStart(); st != nil {
			// This start's own op is still running; the address is its
			// result, so it is the one op the write does not refuse for
			// (I-1444).
			e.recordGuestAddress(ctx, p, st.GetGuestIp(), st.GetVsockCid(), "start", &op.ID)
		}
		// A restore onto another host tells the user here and not in the
		// restore phase, because only a project that is being started
		// has a start_guest phase at all. A move of a *stopped* project
		// gives it back stopped, on a host the user never named and
		// cannot see, so the mail -- "<slug> was restored onto a new host
		// from its latest snapshot" -- announces an event with no
		// user-visible consequence, and on a machine they deliberately
		// switched off it reads as data loss. Gating on the plan rather
		// than on a `start` param holds for every caller: the admin move
		// sets that param, a restore enqueued straight from the ops API
		// does not (DECISIONS I-964).
		if moved, _ := op.Params["host_moved"].(bool); moved {
			e.notifyPlatform(ctx, p.ID, "host_moved", p.Slug+" was restored onto a new host from its latest snapshot")
		}
		return e.setState(ctx, p, "running")
	case PhaseStopGuest:
		if s := res.GetStop(); s != nil && s.BlobPath != "" {
			if err := e.recordSnapshot(ctx, op, p, s.BlobPath, int64(s.Bytes), s.Sha256, s.BaseVersion, "stop"); err != nil {
				return err
			}
		}
		if s := res.GetStop(); s != nil && s.SnapshotError != "" {
			// The machine stopped and its volume is intact; only the
			// snapshot is missing (a full store, DECISIONS I-1636). The
			// same as a stop whose snapshot phase fails after the guest is
			// down (I-158): stopped, the reason on the project, and the
			// user told. The earlier snapshots are kept.
			short := s.SnapshotError
			if len(short) > 200 {
				short = short[:200]
			}
			// A store with no room says what frees it: the retention, which
			// deletes on the host too (I-1636). The user has nothing to run.
			if strings.Contains(s.SnapshotError, "no space left on device") {
				short = "the host's snapshot space is full. Repose deletes snapshots older than seven days, and all but a project's newest seven, once a day"
			}
			_, _ = e.pool.Exec(ctx, "update projects set last_error = $2 where id = $1", p.ID, "snapshot_failed: "+short) // best effort; the event below carries it too
			e.notifyPlatform(ctx, p.ID, "snapshot_failed", p.Slug+" stopped, but its snapshot was not saved: "+short+".")
		}
		if op.Kind == KindDestroy {
			return nil
		}
		return e.setState(ctx, p, "stopped")
	case PhaseSnapshot:
		s := res.GetSnapshot()
		if s == nil || s.BlobPath == "" {
			return errors.New("snapshot result without a blob path")
		}
		return e.recordSnapshot(ctx, op, p, s.BlobPath, int64(s.Bytes), s.Sha256, s.BaseVersion, snapshotReason(op))
	case PhaseDestroyGuest:
		if op.Kind != KindDestroy {
			// Restore: the old guest is gone and hostd has released its
			// address, so the project lets go of it now, not when the
			// restore phase is sent; a restore that fails in between
			// would otherwise leave the project holding an address the
			// host gives the next guest (DECISIONS I-461).
			_, err := e.pool.Exec(ctx, "update projects set guest_id = null, guest_ip = null, vsock_cid = null where id = $1 and guest_id = $2", p.ID, p.GuestID)
			return err
		}
		return e.markDestroyed(ctx, op, p.ID)
	case PhaseResize:
		nb, _ := op.Params["volume_bytes"].(float64)
		_, err := e.pool.Exec(ctx, "update projects set volume_bytes = $2 where id = $1", p.ID, int64(nb))
		return err
	case PhaseApplyConfig:
		a := res.GetApply()
		rev, err := e.revisionFor(ctx, op, p)
		if err != nil {
			return err
		}
		if a != nil && a.RebootRequired {
			op.RebootRequired = true
			if _, err := e.pool.Exec(ctx, "update ops set reboot_required = true where id = $1", op.ID); err != nil {
				return err
			}
			_, err := e.pool.Exec(ctx, "update config_revisions set reboot_required = true where id = $1", rev.ID)
			return err
		}
		if _, err := e.pool.Exec(ctx, "update config_revisions set status = 'applied', applied_at = now(), reboot_required = false where id = $1", rev.ID); err != nil {
			return err
		}
		if _, err := e.pool.Exec(ctx, "update config_revisions set status = 'built' where project_id = $1 and id <> $2 and status = 'applied'", p.ID, rev.ID); err != nil {
			return err
		}
		_, err = e.pool.Exec(ctx, "update projects set config_revision_id = $2, base_version = coalesce($3, base_version) where id = $1", p.ID, rev.ID, rev.BaseVersion)
		if err != nil {
			return err
		}
		e.log.Info("configuration applied", "event", "switch_done", "project_id", p.ID.String(), "rebooted", a != nil && a.Rebooted)
		return nil
	case PhaseUpdateSecrets:
		return nil
	case PhaseWake:
		// The machine is awake, whether this phase woke it or found it
		// so: the marker goes now rather than when hostd's event lands,
		// so the phases after it never read a stale row (I-818).
		if w := res.GetWake(); w == nil || w.WasResting {
			if _, err := e.pool.Exec(ctx, "update projects set resting_since = null where id = $1 and resting_since is not null", p.ID); err != nil {
				return err
			}
		}
		if op.Kind == KindWake {
			// The caller reads whether the host answered; a phase skipped
			// for a host that is away leaves no result (I-1642).
			return e.setResult(ctx, op, map[string]any{"was_resting": res.GetWake().GetWasResting(), "wake_ms": res.GetWake().GetWakeMs()})
		}
		return nil
	case PhaseExec:
		x := res.GetExec()
		if x == nil {
			return errors.New("exec result without payload")
		}
		return e.setResult(ctx, op, map[string]any{"exit_code": x.ExitCode, "stdout": base64.StdEncoding.EncodeToString(x.Stdout), "stderr": base64.StdEncoding.EncodeToString(x.Stderr)})
	}
	return nil
}

// ForkSnapshotTTL is how long a snapshot a fork took for itself is kept
// (DECISIONS I-703).
const ForkSnapshotTTL = 24 * time.Hour

// recordSnapshot writes the snapshot row. sha256 is the digest hostd
// computed while uploading, "" from a hostd older than I-462.
func (e *Engine) recordSnapshot(ctx context.Context, op *store.Op, p *store.Project, blobPath string, bytes int64, sha256, baseVersion, reason string) error {
	id := store.NewID()
	var expires *time.Time
	if op.Kind == KindDestroy {
		t := e.now().Add(30 * 24 * time.Hour)
		expires = &t
	}
	if reason == "fork" {
		// Long enough to fork from again after a copy failed, and no
		// longer: it is not one of the project's snapshots (I-703).
		t := e.now().Add(ForkSnapshotTTL)
		expires = &t
	}
	var got uuid.UUID
	err := e.pool.QueryRow(ctx, `insert into snapshots (id, project_id, host_id, blob_path, bytes, reason, taken_at, expires_at, sha256, base_version) values ($1, $2, $3, $4, $5, $6, now(), $7, nullif($8, ''), coalesce(nullif($9, ''), (select base_version from projects where id = $2)))
		on conflict (blob_path) do update set reason = excluded.reason, bytes = greatest(snapshots.bytes, excluded.bytes), expires_at = coalesce(excluded.expires_at, snapshots.expires_at), sha256 = coalesce(snapshots.sha256, excluded.sha256)
		where snapshots.project_id = excluded.project_id
		returning id`, id, p.ID, p.HostID, blobPath, bytes, reason, expires, sha256, baseVersion).Scan(&got)
	if err != nil {
		return err
	}
	if _, err := e.pool.Exec(ctx, "update ops set snapshot_id = $2 where id = $1", op.ID, got); err != nil {
		return err
	}
	e.log.Info("snapshot recorded", "event", "snapshot_done", "project_id", p.ID.String(), "reason", reason, "bytes", bytes)
	return e.setResult(ctx, op, map[string]any{"snapshot_id": got.String()})
}

// onFail applies the kind's failure policy to the project.
func (e *Engine) onFail(ctx context.Context, op *store.Op, code, msg string, line, personalLine int) {
	if op.ProjectID == nil {
		return
	}
	p, err := store.GetProject(ctx, e.pool, *op.ProjectID)
	if err != nil {
		return
	}
	phase := currentPhase(op)
	if phase == PhaseBuild {
		if op.RevisionID != nil || p.ConfigRevisionID != nil {
			rev, err := e.revisionFor(ctx, op, p)
			if err == nil {
				var l, pl *int
				if line > 0 {
					l = &line
				}
				if personalLine > 0 {
					pl = &personalLine
				}
				_, _ = e.pool.Exec(ctx, "update config_revisions set status = 'failed', error = $2, fragment_line = $3, personal_line = $4 where id = $1", rev.ID, code+": "+msg, l, pl) // best effort; the op carries the error
			}
		}
		if op.StartedAt != nil {
			e.m.BuildDuration.WithLabelValues(code).Observe(e.now().Sub(*op.StartedAt).Seconds())
		}
	}
	if phase == PhaseRestore {
		_, _ = e.pool.Exec(ctx, "update snapshots set restoring_op_id = null where restoring_op_id = $1", op.ID) // release the expiry guard
	}
	if phase == PhaseApplyConfig && isErofs(p) && op.RevisionID != nil {
		// An erofs menu that did not apply (I-1458): the machine keeps
		// running with what it had, and the revision is not sent again by
		// the next start until the user changes the menu.
		_, _ = e.pool.Exec(ctx, "update config_revisions set status = 'failed', error = $2 where id = $1 and status = 'built'", *op.RevisionID, code+": "+msg) // best effort; the op carries the error
	}
	if phase == PhaseApplyConfig && code == codeBootFailed && op.RevisionID != nil {
		// A forced reboot onto the revision never reached Ready and the
		// machine runs its previous system (I-590): the revision is not
		// applied again by a start or the base sweep.
		_, _ = e.pool.Exec(ctx, "update config_revisions set status = 'failed', error = $2 where id = $1 and status in ('built', 'applied')", *op.RevisionID, code+": "+msg) // best effort; the op carries the error
		e.notifyPlatform(ctx, p.ID, EventBootFailed, bootFailedSummary(p.Slug, msg))
	}
	if _, ok := op.Params["personal"].(string); ok && op.Kind == KindBuild && (phase == PhaseBuild || personalLine > 0) {
		// A build the personal layer started (I-490): the machine keeps
		// running what it has, and the user hears which file to fix.
		first := msg
		if i := strings.IndexByte(first, '\n'); i > 0 {
			first = first[:i]
		}
		e.notifyPlatform(ctx, p.ID, "personal_failed", PersonalName+" did not apply to "+p.Slug+", which keeps its current configuration: "+first)
	}
	short := msg
	if i := strings.IndexByte(short, '\n'); i > 0 {
		short = short[:i]
	}
	if len(short) > 200 {
		short = short[:200]
	}
	switch {
	case op.Kind == KindStop && phase == PhaseSnapshot:
		// The guest is already down (a stop whose guestd was dead snapshots
		// after the stop, I-158): it stays stopped, and the missing
		// snapshot is reported like any other.
		_, _ = e.pool.Exec(ctx, "update projects set last_error = $2 where id = $1", p.ID, code+": "+short) // best effort; the op carries the error
		e.notifyPlatform(ctx, p.ID, "snapshot_failed", "the snapshot after stopping failed: "+short)
	case op.Kind == KindDestroy:
		// The CLI no longer waits for a destroy (I-166), so the failure
		// has to reach the user some other way: the project's state and
		// reason in every list, and a notification.
		// The reason names the retry, so `repose ls` and the
		// dashboard say "destroy again", not the "start" every other error
		// suggests.
		reason := "destroying " + p.Slug + " failed: " + strings.TrimSuffix(short, ".") + ". `repose rm " + p.Slug + "` tries again"
		_, _ = e.pool.Exec(ctx, "update projects set state = 'error', last_error = $2 where id = $1 and state <> 'destroyed'", p.ID, code+": "+reason) // best effort; the op carries the error
		e.notifyPlatform(ctx, p.ID, "destroy_failed", reason+".")
	case op.Kind == KindStop:
		// `repose stop` returns once the stop is under way (I-710), so
		// the failure reaches the user the way a destroy's does (I-165):
		// the project's state and reason in every list, and a
		// notification. The guest may still be on (a freeze that failed
		// stops nothing); a second stop takes it down without a snapshot
		// (I-158).
		reason := "stopping " + p.Slug + " failed: " + strings.TrimSuffix(short, ".")
		if !strings.Contains(short, "`repose ") {
			reason += ". `repose stop " + p.Slug + "` tries again"
		}
		_, _ = e.pool.Exec(ctx, "update projects set state = 'error', last_error = $2 where id = $1 and state <> 'destroyed'", p.ID, code+": "+reason) // best effort; the op carries the error
		e.notifyPlatform(ctx, p.ID, EventStopFailed, reason+".")
	case op.Kind == KindStart && code == codeVolumeUnavailable:
		// The host's pool is down (I-644): nothing booted and the disk is
		// intact, so the project is stopped, not error, and the next
		// start is a start, not a restart. A create or restore keeps
		// error with its host: freeing the placement would orphan hostd's
		// record, and the scheduler already places nothing there.
		_, _ = e.pool.Exec(ctx, "update projects set state = 'stopped', last_error = $2 where id = $1 and state <> 'destroyed'", p.ID, code+": "+short) // best effort; the op carries the error
	case op.Kind == KindStart && phase == PhaseStartGuest && code == "insufficient_capacity":
		// The host refused the start for room (its memory commit, free
		// memory, cores or pool), every one of those checks answering
		// before the boot, so the machine is as it was: stopped, its disk
		// and host kept (ENG-265, DECISIONS I-1680). Left in error, `repose
		// status` read "error: the host has no room ..." for a machine
		// nothing had happened to. The op keeps the code and the sentence.
		_, _ = e.pool.Exec(ctx, "update projects set state = 'stopped', last_error = $2 where id = $1 and state <> 'destroyed'", p.ID, code+": "+short) // best effort; the op carries the error
	case op.Kind == KindCreate && phase == PhaseCreateGuest && code == "insufficient_capacity":
		// The host refused the create for room (memory, CPU, its pool,
		// its store) before it wrote a record or a volume, so there is
		// nothing to keep or clean: the project goes, as one refused at
		// placement never exists (DECISIONS I-1648). Left in error it
		// read "`repose start X` restarts it", and the host no longer had
		// the machine (the lab's F2). The op keeps the refusal, which the
		// CLI and the dashboard show; no event, nothing billed.
		_, _ = e.pool.Exec(ctx, "update projects set state = 'destroyed', destroyed_at = now(), host_id = null, last_error = $2 where id = $1 and state <> 'destroyed'", p.ID, CreateRefusedPrefix+short) // best effort; the op carries the error
	case op.Kind == KindCreate || op.Kind == KindStart || op.Kind == KindRestore:
		if code == "capacity" {
			_, _ = e.pool.Exec(ctx, "update projects set host_id = null where id = $1", p.ID) // free the placement
		}
		_, _ = e.pool.Exec(ctx, "update projects set state = 'error', last_error = $2 where id = $1 and state <> 'destroyed'", p.ID, code+": "+short)
	case op.Kind == KindWake:
		// The route that asked is still waiting and tells the user; the
		// machine is as it was, so there is nothing to record on it
		// (I-1642).
	case op.Kind == KindSnapshot:
		_, _ = e.pool.Exec(ctx, "update projects set last_error = $2 where id = $1", p.ID, code+": "+short)
		e.notifyPlatform(ctx, p.ID, "snapshot_failed", "snapshot failed: "+short)
	default:
		_, _ = e.pool.Exec(ctx, "update projects set last_error = $2 where id = $1", p.ID, code+": "+short)
	}
}

// setVMSize records what a booted machine's VM got (DECISIONS I-1680):
// the size from a host that sizes each start, null from every other host,
// so a machine moved or restarted where it is its class shows no size.
func setVMSize(ctx context.Context, q store.Querier, id uuid.UUID, v *hostdv1.VmSize) error {
	_, err := q.Exec(ctx, "update projects set vm_size = $2 where id = $1", id, vmSizeOf(v))
	return err
}

// vmSizeOf is hostd's VmSize as the column stores it; nil for none.
func vmSizeOf(v *hostdv1.VmSize) *store.VMSize {
	if v == nil || v.Vcpus == 0 || v.MemMib == 0 {
		return nil
	}
	return &store.VMSize{VCPUs: v.Vcpus, MemMiB: v.MemMib, FullVCPUs: max(v.FullVcpus, v.Vcpus), FullMemMiB: max(v.FullMemMib, v.MemMib)}
}

// CreateRefusedPrefix begins the last_error of a project whose create the
// host refused for room (DECISIONS I-1648), followed by the sentence the
// op's error gave: GET /projects/:id reads it to say the project was not
// created, and a destroy never writes it.
const CreateRefusedPrefix = "create_refused: "

// notifyPlatform records a platform-originated event (13-notifications.md
// §2), best effort: a failure to notify must never fail the op it reports.
func (e *Engine) notifyPlatform(ctx context.Context, projectID uuid.UUID, kind, summary string) {
	if e.events == nil {
		return
	}
	if err := e.events.Platform(ctx, projectID, kind, summary); err != nil {
		e.log.Warn("platform event", "event", "notify_fail", "kind", kind, "project_id", projectID.String(), "err", err.Error())
	}
}

// markDestroyed is the end of a destroy: the row keeps its history with
// destroyed_at set and the last snapshot expires in 30 days (R4-11). The
// newest live snapshot by taken_at is the one kept; the older ones expire
// at once, so the next sweep deletes them. It used to give all of them
// the 30 days, which is up to seven times the bytes R4-11 promises
// (I-808). It
// runs from the destroy_guest phase, and from finish for a destroy whose
// plan was empty because the project never had a guest (a create that
// failed before CreateGuest), which used to leave the project in `error`
// for ever (I-124).
//
// A temporary project (expires_at set) keeps no snapshot: every one it
// has, the nightly one included, expires at once, and the snapshot
// expiry deletes it on its next run (DECISIONS I-347). A destroy the
// reaper started (params.expired) records temp_destroyed, once: only
// the call that marks the row does.
func (e *Engine) markDestroyed(ctx context.Context, op *store.Op, projectID uuid.UUID) error {
	// A machine whose host is lost or retired is destroyed without it
	// (DECISIONS I-1548): every phase skipped (I-924), and a snapshot
	// that host kept on its own disk, the account's own host's, goes
	// with it, marked as a host removal marks it (I-1542). A snapshot in
	// repose's store (a host repose runs) stays as the final snapshot.
	hostGone := false
	var hostID *uuid.UUID
	if err := e.pool.QueryRow(ctx, "select host_id from projects where id = $1", projectID).Scan(&hostID); err != nil && !db.IsNoRows(err) {
		return err
	}
	if hostID != nil {
		gone, err := e.hostGone(ctx, *hostID)
		if err != nil {
			return err
		}
		hostGone = gone
		if gone {
			if _, err := e.pool.Exec(ctx, `update snapshots s set gone_with_host = h.name
				  from hosts h
				 where s.project_id = $1 and s.host_id = h.id and h.owner_account_id is not null
				   and s.gone_with_host is null and s.deleted_at is null`, projectID); err != nil {
				return err
			}
		}
	}
	tag, err := e.pool.Exec(ctx, `update projects set state = 'destroyed', destroyed_at = now(), host_id = null, guest_id = null, guest_ip = null, vsock_cid = null where id = $1 and destroyed_at is null`, projectID)
	if err != nil {
		return err
	}
	e.log.Info("project destroyed", "event", "guest_destroy", "project_id", projectID.String(), "host_gone", hostGone)
	_, err = e.pool.Exec(ctx, `update snapshots s set expires_at = case
			when p.expires_at is not null then least(coalesce(s.expires_at, now()), now())
			when s.id = (select k.id from snapshots k where k.project_id = s.project_id and k.deleted_at is null
				order by k.taken_at desc, k.id desc limit 1)
				then coalesce(s.expires_at, now() + interval '30 days')
			else least(coalesce(s.expires_at, now()), now()) end
		from projects p where p.id = s.project_id and s.project_id = $1 and s.deleted_at is null`, projectID)
	if err != nil {
		return err
	}
	if expired, _ := op.Params["expired"].(bool); expired && tag.RowsAffected() > 0 {
		var slug string
		if err := e.pool.QueryRow(ctx, "select slug from projects where id = $1", projectID).Scan(&slug); err == nil {
			e.notifyPlatform(ctx, projectID, "temp_destroyed", TempDestroyedSummary(slug))
		}
	}
	return nil
}

// TempDestroyedSummary is the temp_destroyed notification's body.
func TempDestroyedSummary(slug string) string {
	return fmt.Sprintf("%s was temporary and its time ran out, so it was destroyed. No snapshot was kept.", slug)
}

// bootTimings is hostd's BootTimings as ops.result's "boot" (DECISIONS
// I-707), or nil from a hostd that sends none. It is for operators: the
// op read users get leaves it out.
func bootTimings(b *hostdv1.BootTimings) map[string]any {
	if b == nil {
		return nil
	}
	return map[string]any{"volume_ms": b.VolumeMs, "prepare_ms": b.PrepareMs, "ready_ms": b.ReadyMs,
		"paths_ms": b.PathsMs, "setup_ms": b.SetupMs, "total_ms": b.TotalMs}
}

// ownHostRefusal is the error a placement that only the account's own
// hardware could take failed with (DECISIONS I-1366). A user whose Mac
// is asleep must read about their Mac: "no host with capacity" is a
// sentence about repose's fleet and tells them nothing they can act on.
// The code is still `capacity`, so the api answers 503 and the op can be
// retried once the machine is back, exactly as a fleet placement can.
func (e *Engine) ownHostRefusal(ctx context.Context, p *store.Project) error {
	msg, _ := e.ownHostRefusalDetail(ctx, p, uuid.Nil)
	return &opError{code: "capacity", msg: msg}
}

// ownHostRefusalDetail is ownHostRefusal's sentence with the detail a
// request-time refusal carries (PlaceNew, I-1390): `reason`
// `no_own_host`, and the host's name and state when there is one, so a
// client can name the way out without parsing the sentence.
//
// An account with several hosts (an exempt one, I-1582) is told about
// every one of them: naming the first alone said "your host a has not
// answered" while b, online and full, was the host the machine needed
// room on. `host` and `host_state` stay the first host's, the oldest.
//
// pin is the host the project named (I-1584), or uuid.Nil: a named host
// is the only one the sentence is about, because it is the only one the
// placement was allowed to use.
func (e *Engine) ownHostRefusalDetail(ctx context.Context, p *store.Project, pin uuid.UUID) (string, map[string]any) {
	detail := map[string]any{"reason": "no_own_host"}
	hosts, err := store.ListAccountHosts(ctx, e.pool, p.UserID)
	if err != nil {
		// The sentence is the only thing lost; the refusal stands.
		e.log.Warn("own host lookup", "event", "schedule_fail", "project_id", p.ID.String(), "err", err.Error())
	}
	var live []store.Host
	for _, h := range hosts {
		if h.State == "retired" || h.State == "lost" {
			// Not a host the account has any more: the placement filter
			// does not count these either, so they explain nothing.
			continue
		}
		if pin != uuid.Nil && h.ID != pin {
			continue
		}
		live = append(live, h)
	}
	if len(live) == 0 {
		return "you own no host yet; `repose host add` prints the step that adds one", detail
	}
	// The figures a ready host refused with (I-1586). Without them the
	// sentence is I-1366's "has no room", still true.
	rooms, err := scheduler.OwnedRooms(ctx, e.pool, p.UserID, p.HeldBytes(), p.VolumeBytes)
	if err != nil {
		e.log.Warn("own host room lookup", "event", "schedule_fail", "project_id", p.ID.String(), "err", err.Error())
	}
	detail["host"], detail["host_state"] = live[0].Name, live[0].State
	if len(live) == 1 {
		h := live[0]
		return "your host " + h.Name + " " + e.ownHostWhy(h, p.Class, false, rooms), detail
	}
	why := make([]string, len(live))
	for i, h := range live {
		why[i] = h.Name + " " + e.ownHostWhy(h, p.Class, true, rooms)
	}
	for _, h := range live {
		if r, ok := rooms[h.ID]; ok && !r.Shares {
			// A host whose hostd does not share yet places by class
			// (I-1630), so the sentence names the class it asked for.
			return "none of your hosts can take " + aClass(p.Class) + " machine (" + gbText(scheduler.ClassRAM(p.Class)) + "): " + strings.Join(why, "; "), detail
		}
	}
	return "none of your hosts can start a machine: " + strings.Join(why, "; "), detail
}

// ownHostWhy is why one host of the account's own took no placement, as
// the words after its name. short is the form in a list of several,
// where the class and its memory are already said once and the
// consequence of a removal is plain. rooms is scheduler.OwnedRooms: a
// ready host is refused by its memory or its disk, and the sentence
// names which, with what it has and what the machine needs (I-1586).
func (e *Engine) ownHostWhy(h store.Host, class string, short bool, rooms map[uuid.UUID]scheduler.Room) string {
	switch h.State {
	case "registering":
		return "has not registered yet"
	case "unreachable":
		// How long it has been quiet, not just that it is: a user
		// reading "14m" knows their Mac went to sleep, and one
		// reading "3d" knows they never turned it back on. One
		// wording across `repose ls`, `repose status`, `run`'s wait
		// and the dashboard (19c); a host of repose's reads "its
		// server is not answering" instead, because that one is not
		// the user's to fix.
		return "has not answered for " + quietFor(h.LastHeartbeatAt, e.now())
	case "draining":
		if short {
			return "is being removed"
		}
		return "is being removed, so nothing can be placed on it"
	}
	if r, ok := rooms[h.ID]; ok {
		if why := roomWhy(r, class, short); why != "" {
			return why
		}
	}
	if short {
		return "has no room"
	}
	if r, ok := rooms[h.ID]; ok && !r.Shares {
		return "has no room for " + aClass(class) + " machine"
	}
	return "has no room for a machine"
}

// roomWhy is the figure that keeps a machine of class off a ready host
// of the user's own with room r, as the words after the host's name, or
// "" when neither memory nor disk is short (a host that missed a
// heartbeat, or one at its guest slots), which "has no room" still
// covers. Memory first: it is what the user frees by stopping a machine.
// On a host that shares (I-1630) a machine has no size, so the memory is
// what the host reports free against scheduler.OwnedStartFreeBytes and no
// class is named; on an owned host whose hostd does not share yet it is
// placement's class figure, as before (I-1586).
func roomWhy(r scheduler.Room, class string, short bool) string {
	need := scheduler.OwnedStartFreeBytes
	if !r.Shares {
		need = scheduler.ClassRAM(class)
	}
	switch {
	case r.FreeBytes < need:
		if short {
			return "has " + gbText(r.FreeBytes) + " free"
		}
		if !r.Shares {
			return "has " + gbText(r.FreeBytes) + " of memory free for machines, and " + aClass(class) + " machine needs " + gbText(need) +
				". A machine holds its memory while it runs, so stopping one frees room for it"
		}
		return ownMemoryWhy(r.FreeBytes)
	case r.PoolUnavailable:
		return "cannot use its disk for machines"
	case r.PoolFreeBytes < r.PoolNeedBytes:
		if short {
			return "has " + gbText(r.PoolFreeBytes) + " of disk free, " + gbText(r.PoolNeedBytes) + " needed"
		}
		return "has " + gbText(r.PoolFreeBytes) + " of disk free, and a new machine needs " + gbText(r.PoolNeedBytes) + " free"
	}
	return ""
}

// aClass is a size class with its article: "a small", "an xl".
func aClass(class string) string {
	if class == "xl" {
		return "an xl"
	}
	return "a " + class
}

// ownMemoryWhy is the words after a host's name when a host the user
// owns has too little memory free to start a machine (I-1630): what it
// reports free and the figure a start needs. Shared by placement's
// refusal and by a start hostd refused for memory, so both read alike.
func ownMemoryWhy(free int64) string {
	return "has " + gbText(max(free, 0)) + " of memory free, and a machine starts there when it has at least " +
		gbText(scheduler.OwnedStartFreeBytes) + " free. Stopping a machine frees the memory it uses"
}

// gbText is a size in the GB the docs and `repose host ls` use (GiB),
// whole when it is whole and to one decimal otherwise: "8 GB", "5.5 GB".
func gbText(b int64) string {
	t := strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64)
	return strings.TrimSuffix(t, ".0") + " GB"
}

// quietFor is how long a host has been silent, in the shortest true
// unit: "14m", "3h", "2d". An unreachable host has heartbeated at least
// once by definition, but a row written before any heartbeat answers
// "90s", the window that makes a host unreachable at all.
func quietFor(last *time.Time, now time.Time) string {
	if last == nil {
		return "90s"
	}
	d := now.Sub(*last)
	switch {
	case d < time.Hour:
		return strconv.Itoa(max(1, int(d.Minutes()))) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	}
}

// placeOp places a project that has no host yet, as the op's first phase
// that needs one: a create's build, or an erofs create's create_guest,
// which has no build before it (DECISIONS I-1464). A placed project
// answers its host.
func (e *Engine) placeOp(ctx context.Context, op *store.Op, p *store.Project) (*uuid.UUID, error) {
	if p.HostID != nil {
		return p.HostID, nil
	}
	var picked uuid.UUID
	err := db.InTx(ctx, e.pool, func(tx db.Tx) error {
		// An operator may pin the placement (repose-admin hosts smoke).
		if want, ok := op.Params["host_id"].(string); ok && want != "" {
			h, err := store.GetHost(ctx, tx, uuid.MustParse(want))
			if err != nil {
				return err
			}
			if h.State != "ready" && h.State != "draining" {
				return &opError{code: "capacity", msg: "host " + h.Name + " is " + h.State}
			}
			picked = h.ID
		} else {
			t, err := e.target(ctx, tx, p)
			if err != nil {
				return err
			}
			pk, err := scheduler.PickHost(ctx, tx, t, p.Class, p.HeldBytes(), p.VolumeBytes, e.now())
			if err != nil {
				return err
			}
			picked = pk.HostID
		}
		_, err := tx.Exec(ctx, "update projects set host_id = $2 where id = $1 and host_id is null", p.ID, picked)
		return err
	})
	if errors.Is(err, scheduler.ErrNoCapacity) && e.placementWaits(ctx, op, p) {
		return nil, errPlacementWait
	}
	if errors.Is(err, scheduler.ErrNoOwnHost) {
		e.m.ScheduleTotal.WithLabelValues("no_own_host").Inc()
		e.log.Warn("no host of the account's own has capacity", "event", "schedule_fail",
			"project_id", p.ID.String(), "class", p.Class, "reason", "no_own_host")
		return nil, e.ownHostRefusal(ctx, p)
	}
	if errors.Is(err, scheduler.ErrNoCapacity) {
		// docs/workstreams/10-observability.md §5 requires schedule_fail
		// and counts placements by result; without these two lines a
		// fleet that has run out of memory is invisible until a user
		// complains. The reason separates the two capacity walls: a
		// fleet out of memory is bought differently from a fleet out
		// of cores (DECISIONS I-736).
		reason := "no_capacity"
		if errors.Is(err, scheduler.ErrNoCPUCapacity) {
			reason = "no_cpu"
		}
		e.m.ScheduleTotal.WithLabelValues(reason).Inc()
		e.log.Warn("no host has capacity", "event", "schedule_fail",
			"project_id", p.ID.String(), "class", p.Class, "reason", reason)
		return nil, errCapacity()
	}
	if err != nil {
		e.m.ScheduleTotal.WithLabelValues("error").Inc()
		e.log.Error("placement failed", "event", "schedule_fail",
			"project_id", p.ID.String(), "class", p.Class, "reason", "error", "err", err.Error())
		return nil, err
	}
	e.m.ScheduleTotal.WithLabelValues("ok").Inc()
	e.log.Info("placed project", "event", "schedule", "project_id", p.ID.String(), "host_id", picked.String(), "class", p.Class)
	p.HostID = &picked
	return &picked, nil
}
