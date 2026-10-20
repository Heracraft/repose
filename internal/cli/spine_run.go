package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/machine"
	"github.com/heracraft/repose/internal/undo"
)

// A run on a machine the user owns (DECISIONS I-812): no api, no guest
// and no gateway. It reuses the three pieces of the hosted run that take
// an sshTarget and nothing else — the sync of the dirty checkout
// (syncGuest), the agent window (startAgentWindow) and the attach
// (attachTmux) — and replaces everything that needs a control plane:
// placement, the certificate, the credential carry and the muxer probe.
//
// A checkout reaches this path once `--machine` has named a machine for
// it; after that the project map remembers, and `repose run` there means
// that machine (spine.go placeFor).

// SpineRunOptions is what `repose run --machine NAME` carries.
type SpineRunOptions struct {
	MachineName string
	ProjectArg  string
	Prompt      string
	Agent       string
	NoSync      bool
	NoAttach    bool
	// Now is the clock the ✓ lines are timed by; nil means time.Now.
	Now func() time.Time
}

// agentProject is `repose-agent project init`'s answer.
type agentProject struct {
	Path    string `json:"path"`
	Created bool   `json:"created"`
}

// agentTake is `repose-agent point take`'s answer.
type agentTake struct {
	Point agentPoint `json:"point"`
}

// SpineRunCmd runs an agent on a machine the user owns.
func SpineRunCmd(ctx context.Context, e *Env, opts SpineRunOptions) error {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	m, place, err := spineRunPlace(e, opts)
	if err != nil {
		return err
	}
	cfg, err := machinesConfigPath()
	if err != nil {
		return exitf(ExitGeneric, "%v", err)
	}
	t := machineTarget(cfg, m)
	agentName := opts.Agent
	if agentName == "" {
		agentName = "claude"
	}

	step := now()
	tick := func(text string) {
		at := now()
		_, _ = fmt.Fprintf(e.ErrOut, "✓ %s  %s\n", text, fmtElapsed(at.Sub(step)))
		step = at
	}

	// The project's live area inside the undo store, and the symlink from
	// the login user's home to it.
	var pr agentProject
	if err := agentRun(ctx, t, &pr, "project", "init", "--project", place.Slug); err != nil {
		return spineRunFailed("prepare "+place.Slug, err)
	}
	if pr.Created {
		tick("Prepared " + place.Slug + " on " + m.Name)
	}

	// The laptop's work, uncommitted work included. This is the half of
	// the product the wrapper shape keeps whole (proposals/2026-10-19).
	if !opts.NoSync {
		remote, _ := checkoutKeys(e, resolveDeps{})
		root := gitRepoRoot(e.Cwd)
		if root == "" {
			root = e.Cwd
		}
		sum, err := syncGuest(ctx, t, root, place.Slug, SyncOptions{
			RemoteURL: remote,
			NoRemote:  remote == "",
		})
		if err != nil {
			return spineRunFailed("sync "+place.Slug, err)
		}
		tick(syncWords(sum))
	}

	// The point the agent starts from, so a rewind has somewhere to go
	// back to. Taking one costs milliseconds (research/wrapper-undo.md),
	// which is why it is every run and not a nightly job.
	var take agentTake
	if err := agentRun(ctx, t, &take, "point", "take",
		"--project", place.Slug, "--name", undo.NewPointName(), "--reason", "run"); err != nil {
		return spineRunFailed("take a rewind point", err)
	}
	tick("Took a rewind point")

	// Only now is the checkout bound to this machine: a run that failed
	// before this leaves nothing behind to resolve to.
	if err := rememberPlace(e, resolveDeps{}, place); err != nil {
		return exitf(ExitGeneric, "%v", err)
	}

	dir := pr.Path
	if dir == "" {
		dir = filepath.Join("~", place.Slug)
	}
	if err := startAgentWindow(ctx, t, place.Slug, agentName, dir, agentName, opts.Prompt, false, nil, mcpApprovals{}); err != nil {
		return spineRunFailed("start "+agentName, err)
	}
	// `tmux new-window` exits 0 whatever happens to the command it
	// started, so a missing agent reads as a started one. On a machine
	// the user owns the agents are installed by hand, so this is the
	// likely failure and it has to be said, not swallowed.
	out, err := runSSH(ctx, t, "tmux list-windows -t "+shQuote(place.Slug)+" -F '#{window_name}'", nil)
	if err != nil {
		return spineRunFailed("read "+place.Slug+"'s windows", err)
	}
	if !slices.Contains(strings.Fields(string(out)), agentName) {
		return exitf(ExitGeneric,
			"%s started and stopped at once on %s. `repose machine add` installs the agents; this one is not there or will not run.",
			agentName, m.Name)
	}
	if opts.NoAttach {
		_, _ = fmt.Fprintf(e.Out, "%s is running in %s on %s.\n", agentName, place.Slug, m.Name)
		return nil
	}
	return attachTmux(t, place.Slug, agentName, laptopTZ(), dir, nil, nil)
}

// spineRunPlace decides which machine and slug this run means: --machine
// names one, and without it the project map answers for the checkout.
func spineRunPlace(e *Env, opts SpineRunOptions) (machine.Machine, machine.Project, error) {
	if opts.MachineName == "" {
		return placeFor(e, opts.ProjectArg, resolveDeps{})
	}
	reg := registryFor(e)
	m, err := reg.Get(opts.MachineName)
	if err != nil {
		if isNoSuchMachine(err) {
			return machine.Machine{}, machine.Project{}, exitf(ExitUsage,
				"%q is not a machine you added; `repose machine ls` lists them.", opts.MachineName)
		}
		return machine.Machine{}, machine.Project{}, exitf(ExitGeneric, "%v", err)
	}
	slug := opts.ProjectArg
	if slug == "" {
		slug = machine.Slug(filepath.Base(e.Cwd))
	}
	if slug == "" {
		return machine.Machine{}, machine.Project{}, exitf(ExitUsage,
			"this folder's name cannot name a project; pass one: repose run NAME --machine %s", m.Name)
	}
	return m, machine.Project{Machine: m.Name, Slug: slug}, nil
}

// syncWords is the one line a sync of an owned machine prints.
func syncWords(s *SyncSummary) string {
	if s == nil {
		return "Synced"
	}
	switch {
	case s.Modified == 0 && s.Untracked == 0 && s.Commits == 0:
		return "Nothing to sync"
	case s.Modified+s.Untracked == 0:
		return fmt.Sprintf("Synced %d %s", s.Commits, plural(s.Commits, "commit", "commits"))
	case s.Commits == 0:
		n := s.Modified + s.Untracked
		return fmt.Sprintf("Synced %d %s", n, plural(n, "file", "files"))
	default:
		n := s.Modified + s.Untracked
		return fmt.Sprintf("Synced %d %s and %d %s",
			n, plural(n, "file", "files"), s.Commits, plural(s.Commits, "commit", "commits"))
	}
}

// spineRunFailed names the step that failed, the way rewindFailed does.
func spineRunFailed(step string, err error) error {
	var ae *agentError
	if errors.As(err, &ae) {
		return exitf(ExitGeneric, "Could not %s: %s", step, ae.Message)
	}
	return exitf(ExitGeneric, "Could not %s: %v", step, err)
}

// spineRunFor answers the options for a run on a machine the user owns,
// or nil when this checkout is a hosted project's. --machine names one;
// without it the project map decides, so a checkout stays on the machine
// its first run named.
func spineRunFor(e *Env, opts RunOptions) (*SpineRunOptions, error) {
	if opts.Machine == "" {
		ps, err := machine.LoadProjects(e.Dir)
		if err != nil {
			return nil, exitf(ExitGeneric, "%v", err)
		}
		remote, dir := checkoutKeys(e, resolveDeps{})
		if _, ok := ps.Resolve(remote, dir); !ok {
			return nil, nil
		}
	}
	// What the hosted run can do and this one cannot. Refusing is the
	// point: a silently ignored --size reads as a machine that resized.
	for _, f := range []struct {
		set  bool
		name string
	}{
		{opts.Size != "", "--size"},
		{opts.Temp != 0, "--temp"},
		{opts.Worktree, "--worktree"},
		{opts.Bridge || len(opts.BridgeAllow) > 0, "--bridge"},
		{opts.On != "", "--on"},
		{opts.Multiplexer != "", "--multiplexer"},
		{opts.NoPersonal, "--no-personal"},
	} {
		if f.set {
			return nil, cobraUsageError{fmt.Errorf("%s is for a machine repose runs; this project is on a machine you own", f.name)}
		}
	}
	return &SpineRunOptions{
		MachineName: opts.Machine,
		ProjectArg:  opts.ProjectArg,
		Prompt:      opts.Prompt,
		Agent:       opts.Agent,
		NoSync:      opts.NoSync,
		NoAttach:    opts.NoAttach,
	}, nil
}
