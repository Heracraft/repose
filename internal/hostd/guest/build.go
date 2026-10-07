package guest

import (
	"context"
	"errors"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/nixbuild"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
)

func (m *Manager) build(ctx context.Context, commandID string, c *hostdv1.Build) (*hostdv1.BuildResult, *Error) {
	if c.ProjectId == "" || c.RevisionId == "" {
		return nil, errf(CodeInvalidArgument, "project_id and revision_id required")
	}
	if len(c.Fragment) == 0 {
		return nil, errf(CodeInvalidArgument, "fragment required")
	}
	lim := c.Limits
	if lim == nil {
		lim = &hostdv1.Limits{}
	}
	if lim.EvalS == 0 {
		lim.EvalS = 60
	}
	if lim.BuildS == 0 {
		lim.BuildS = 1800
	}
	if lim.Cores == 0 {
		lim.Cores = 8
	}
	if lim.ClosureBytes == 0 {
		lim.ClosureBytes = 20 << 30
	}
	if pct, ok := m.storeUsedPct(); ok && pct >= m.cfg.StoreHighPct {
		return nil, errf(CodeInsufficientCapacity, "host store full")
	}
	log := m.d.Log.With("project_id", c.ProjectId, "command_id", commandID)
	log.Info("build start", "event", "build_start", "revision_id", c.RevisionId)
	start := m.d.Now()
	if m.d.Metrics != nil {
		m.d.Metrics.BuildsRunning.Inc()
		defer m.d.Metrics.BuildsRunning.Dec()
	}
	m.mu.Lock()
	m.buildRun++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.buildRun--
		m.mu.Unlock()
	}()
	defer func() {
		m.mu.Lock()
		delete(m.buildSeq, commandID)
		m.mu.Unlock()
	}()
	res, err := m.d.Nix.Build(ctx, nixbuild.Request{
		ProjectID: c.ProjectId, RevisionID: c.RevisionId, Fragment: c.Fragment, BaseRef: c.BaseRef, BaseVersion: c.BaseVersion, Personal: c.Personal,
		Limits: nixbuild.Limits{EvalS: lim.EvalS, BuildS: lim.BuildS, Cores: lim.Cores, ClosureBytes: lim.ClosureBytes},
	}, func(line string) {
		m.buildLog(commandID, line)
	})
	dur := m.d.Now().Sub(start)
	if err != nil {
		var ne *nixbuild.Error
		if errors.As(err, &ne) {
			log.Warn("build failed", "event", "build_fail", "code", ne.Code, "duration_ms", dur.Milliseconds())
			if m.d.Metrics != nil {
				m.d.Metrics.BuildDuration.WithLabelValues(ne.Code).Observe(dur.Seconds())
			}
			return nil, &Error{Code: ne.Code, Message: ne.Message, FragmentLine: ne.FragmentLine, PersonalLine: ne.PersonalLine}
		}
		log.Error("build failed", "event", "build_fail", "code", CodeInternal, "duration_ms", dur.Milliseconds())
		if m.d.Metrics != nil {
			m.d.Metrics.BuildDuration.WithLabelValues("internal").Observe(dur.Seconds())
		}
		return nil, errf(CodeInternal, "build: %v", err)
	}
	if m.d.Metrics != nil {
		m.d.Metrics.BuildDuration.WithLabelValues("ok").Observe(dur.Seconds())
		m.d.Metrics.BuildPhaseDuration.WithLabelValues("eval").Observe(res.EvalDuration.Seconds())
		m.d.Metrics.BuildPhaseDuration.WithLabelValues("build").Observe(res.BuildDuration.Seconds())
	}
	if res.CacheUnreachable {
		m.Warn("cache_unreachable", "substituter unreachable during build of revision "+c.RevisionId+"; built from source")
	}
	log.Info("build done", "event", "build_done", "duration_ms", dur.Milliseconds(),
		"eval_ms", res.EvalDuration.Milliseconds(), "build_ms", res.BuildDuration.Milliseconds(),
		"closure_bytes", res.ClosureBytes, "eval_cached", res.EvalCached)
	return &hostdv1.BuildResult{SystemClosure: res.SystemClosure, ClosureBytes: res.ClosureBytes, KernelChanged: m.kernelChanged(c.ProjectId, res)}, nil
}

// kernelChanged compares the built kernel and initrd with what the
// project's guest last booted or applied.
func (m *Manager) kernelChanged(projectID string, res *nixbuild.Result) bool {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return false
	}
	for _, g := range gs {
		if g.ProjectID != projectID || g.Kernel == "" {
			continue
		}
		return g.Kernel != res.Kernel || g.Initrd != res.Initrd
	}
	return false
}

func (m *Manager) apply(ctx context.Context, commandID string, c *hostdv1.ApplyConfig) (*hostdv1.ApplyResult, *Error) {
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return nil, gerr
	}
	if c.SystemClosure == "" {
		return nil, errf(CodeInvalidArgument, "system_closure required")
	}
	// A resend of a forced reboot that fell back (I-590): the guest runs,
	// or is left, on its last good closure; answer as the first run did
	// rather than adopting the closure that failed again.
	if fb := g.BootFallback; fb != nil && fb.CommandID == commandID && fb.Failed == c.SystemClosure {
		return nil, applyBootFailed(fb, "")
	}
	if ok, err := m.d.Nix.PathExists(ctx, c.SystemClosure); err != nil {
		return nil, errf(CodeInternal, "nix path-info: %v", err)
	} else if !ok {
		return nil, errf(CodeNotFound, "system closure %s is not in the host store", c.SystemClosure)
	}
	if g.State != StateRunning {
		// A record that says not running while the hypervisor runs
		// (a drift reconcile has not corrected yet) must not pass for a
		// stopped guest: moving the root alone would report success while
		// the running system never switched, and the api would record the
		// revision applied (DECISIONS I-325). Fail, so the revision stays
		// unapplied and the user sees it.
		if active, err := m.d.Systemd.IsActive(ctx, GuestUnit(g.GuestID)); err == nil && active {
			m.log(g).Warn("apply refused: record says not running but the hypervisor is", "event", "apply_state_drift", "state", g.State)
			return nil, errf(CodeInternal, "the machine is running but the host's record says %s; nothing was applied, try again in a minute", g.State)
		}
		// A stopped guest only needs the root moved; the next start boots
		// it. What that boot reads from the host must be there now: the
		// kernel, initrd and init the runner is rendered from, and every
		// path the store view will be filled with (I-591). What only the
		// guest's disk decides (its store's upper layer) shows at the
		// boot, which falls back when it never reaches Ready (I-590).
		if err := m.bootable(ctx, c.SystemClosure); err != nil {
			return nil, err
		}
		if err := m.adoptClosure(g, c.SystemClosure); err != nil {
			return nil, err
		}
		return &hostdv1.ApplyResult{}, nil
	}
	sess, serr := m.session(g.GuestID)
	if serr != nil {
		return nil, serr
	}
	// The new closure's paths must be in the guest's store before it
	// switches to them.
	if err := m.extendView(ctx, g.GuestID, []string{c.SystemClosure}); err != nil {
		return nil, errf(CodeInternal, "%v", err)
	}
	reg, derr := m.d.Nix.DumpDB(ctx, c.SystemClosure)
	if derr != nil {
		return nil, errf(CodeInternal, "nix-store --dump-db: %v", derr)
	}
	sw, err := sess.Switch(ctx, c.SystemClosure, false, reg)
	if err != nil {
		if _, ok := vsockclient.IsRemote(err); !ok {
			// The connection went, not the guest: an activation that
			// restarts guestd ends the session mid-call. Switch is
			// idempotent (profile set, activation of the same system a
			// no-op), so once guestd is back it is asked again, once
			// (I-148). Longer than that is the guestd_lost path.
			m.log(g).Warn("guestd went away during Switch; waiting for it", "event", "switch_retry", "err", err.Error())
			if again, serr := m.awaitSession(ctx, g.GuestID, m.cfg.GuestdLostAfter+30*time.Second, sess); serr == nil {
				sw, err = again.Switch(ctx, c.SystemClosure, false, reg)
			}
		}
	}
	if err != nil {
		if re, ok := vsockclient.IsRemote(err); ok {
			out := ""
			if sw != nil {
				out = string(sw.Output)
			}
			// guestd's code is one of hostd's (sysdep/errors.go): a
			// closure its store does not show is not_found, not internal
			// (I-593). The guest keeps running what it ran.
			return nil, errf(switchCode(re.Code), "switch failed: %s: %s\n\n%s", re.Code, re.Message, out)
		}
		return nil, errf(CodeGuestUnresponsive, "guestd Switch: %v", err)
	}
	if sw.NeedsReboot && !c.ForceReboot {
		m.log(g).Info("apply needs reboot", "event", "switch_done", "reboot_required", true)
		return &hostdv1.ApplyResult{RebootRequired: true}, nil
	}
	if sw.NeedsReboot {
		// force_reboot: snapshot, stop, adopt the closure, start.
		if _, err := m.snapshotGuest(ctx, g, "stop"); err != nil {
			return nil, err
		}
		if err := m.stopGuest(ctx, g, 0); err != nil {
			return nil, err
		}
		if err := m.adoptClosure(g, c.SystemClosure); err != nil {
			return nil, err
		}
		g.BootFallback = nil
		fb, tail, err := m.bootOrFallBack(ctx, g, commandID)
		if err != nil {
			return nil, err
		}
		if fb != nil {
			// The new system never reached Ready and the guest runs its
			// last good one again: the apply did not take (I-590).
			return nil, applyBootFailed(fb, tail)
		}
		m.log(g).Info("apply rebooted", "event", "switch_done", "rebooted", true)
		return &hostdv1.ApplyResult{Rebooted: true}, nil
	}
	// guestd activated it and answered: the disk runs this closure (I-590).
	m.markGood(g, c.SystemClosure)
	if err := m.adoptClosure(g, c.SystemClosure); err != nil {
		return nil, err
	}
	m.log(g).Info("apply switched", "event", "switch_done", "rebooted", false)
	return &hostdv1.ApplyResult{}, nil
}

// adoptClosure moves the guest's GC root and records the closure's kernel
// and initrd.
func (m *Manager) adoptClosure(g *state.Guest, closure string) *Error {
	if err := m.d.Roots.Set(g.GuestID, closure); err != nil {
		return errf(CodeInternal, "gcroot: %v", err)
	}
	if g.SystemClosure != "" && g.SystemClosure != closure {
		past := []string{g.SystemClosure}
		for _, p := range g.PastClosures {
			if p != closure && p != g.SystemClosure && len(past) < state.MaxPastClosures {
				past = append(past, p)
			}
		}
		g.PastClosures = past
	}
	g.SystemClosure = closure
	if info, err := nixbuild.ClosureInfo(closure); err == nil {
		g.Kernel, g.Initrd = info.Kernel, info.Initrd
	}
	if err := m.d.State.PutGuest(g); err != nil {
		return errf(CodeInternal, "state write: %v", err)
	}
	m.writeGuestJSON(g)
	return nil
}

// bootable checks what a boot of closure reads from the host: its kernel,
// initrd and init, and its requisites, which fill the store view (I-591).
func (m *Manager) bootable(ctx context.Context, closure string) *Error {
	if _, err := nixbuild.ClosureInfo(closure); err != nil {
		return errf(CodeNotFound, "system closure %s cannot boot: %v", closure, err)
	}
	if _, err := m.d.Nix.Requisites(ctx, closure); err != nil {
		return errf(CodeNotFound, "system closure %s is not whole in the host store: %v", closure, err)
	}
	return nil
}

// switchCodes are guestd's Switch error codes hostd passes to the api as
// they are; any other is internal (I-593).
var switchCodes = map[string]bool{CodeInvalidArgument: true, CodeNotFound: true}

func switchCode(code string) string {
	if switchCodes[code] {
		return code
	}
	return CodeInternal
}

// applyBootFailed is a forced reboot that fell back: boot_failed, with
// the sentence the console was classified as, the guest running its last
// good closure. Never guest_unresponsive, which the api answers with a
// reboot onto the same closure.
func applyBootFailed(fb *state.BootFallback, tail string) *Error {
	return &Error{Code: CodeBootFailed, Message: fb.Message + "; the machine runs its previous system", ConsoleTail: tail}
}

var _ = time.Second
