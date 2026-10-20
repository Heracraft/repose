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
// and no gateway. It reuses the four pieces of the hosted run that take
// an sshTarget and nothing else — the sync of the dirty checkout
// (syncGuest), the carry that makes the box workable (the tool logins,
// the git identity and config, the .env files, the time zone), the agent
// window (startAgentWindow) and the attach (attachTmux) — and replaces
// everything that needs a control plane: placement, the certificate, the
// stored secrets and the muxer probe.
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
	// StashRemote and DiscardRemote move the box's own uncommitted
	// changes out of the way first, as they do on a hosted machine.
	StashRemote   bool
	DiscardRemote bool
	// SyncOnly stops after the project, the laptop's work and the rewind
	// point: `repose sync` on a machine the user owns, which starts no
	// agent and attaches to nothing. It is also the full sync; a run
	// leaves a checkout that is already there alone (I-367).
	SyncOnly bool
	// Deps is how the working directory resolves to a project and to the
	// remote the box's git is pointed at. The zero value is the
	// directory alone, as RewindOptions.Deps is; spineRunFor passes the
	// real one.
	Deps resolveDeps
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

// agentCodeExists is `project init`'s refusal for a ~/<slug> that is the
// box owner's own file or link (cmd/repose-agent linkHome). spine.go
// holds the two codes every command acts on; this one is this path's.
const agentCodeExists = "exists"

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
		return spineInitFailed(place.Slug, m.Name, err)
	}
	if pr.Created {
		tick("Prepared " + place.Slug + " on " + m.Name)
	}

	// The laptop's work, uncommitted work included. This is the half of
	// the product the wrapper shape keeps whole (proposals/2026-10-19),
	// and the carry that rides its ssh is what makes the box workable:
	// without it an agent there cannot commit or push and has no .env.
	var appr mcpApprovals
	root := gitRepoRoot(e.Cwd)
	switch {
	case root == "":
		// Nothing to sync, and nothing to carry either: the carry is
		// part of the sync's own ssh (I-224).
		_, _ = fmt.Fprintln(e.Out, "Not a git repository, so nothing was synced.")
	case !opts.NoSync:
		appr, err = e.spineSync(ctx, t, place, root, opts, tick)
		if err != nil {
			return err
		}
	}

	// The environment the agent will work in, before the point that is
	// its way back: the project's repose.nix applied on the box, and
	// then whatever the project needs that the box has not got
	// (I-832). A sync sends work and takes a point; applying an
	// environment and installing a toolchain is a run's.
	if !opts.SyncOnly {
		e.spineApplyEnv(ctx, t, m, place, root, tick)
		e.spineInstallTools(ctx, t, m, place, tick)
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
	if err := rememberPlace(e, opts.Deps, place); err != nil {
		return exitf(ExitGeneric, "%v", err)
	}
	// The work's way home that is not GitHub: `git fetch repose` brings
	// the box's commits to this checkout (I-272).
	e.spineReposeRemote(root, m.Name, place.Slug)

	if opts.SyncOnly {
		return nil
	}

	dir := pr.Path
	if dir == "" {
		dir = filepath.Join("~", place.Slug)
	}
	// Claude Code's login is the user's own, in the machine, and repose
	// never copies it (DECISIONS I-278): with none there the window opens
	// on the login and the prompt is typed after it (I-607). With no
	// prompt there is nothing to type either way.
	needsLogin := false
	if agentName == "claude" {
		needsLogin, err = needsClaudeLogin(ctx, t, false)
		if err != nil {
			return spineRunFailed("read Claude Code's login on "+m.Name, err)
		}
	}
	attachOnly := needsLogin || opts.Prompt == ""
	prompt := opts.Prompt
	if needsLogin && opts.NoAttach {
		// Nobody logs in now: the prompt is refused below, not left to
		// type itself later (I-607).
		prompt = ""
	}
	if err := startAgentWindow(ctx, t, place.Slug, agentName, dir, agentName, prompt, attachOnly, nil, appr); err != nil {
		return spineRunFailed("start "+agentName, err)
	}
	// `tmux new-window` exits 0 whatever happens to the command it
	// started, so a missing agent reads as a started one. On a machine
	// the user owns the agents are installed by hand, so this is the
	// likely failure and it has to be said, not swallowed.
	out, err := runOnMachine(ctx, t, "tmux list-windows -t "+shQuote(place.Slug)+" -F '#{window_name}'", nil)
	if err != nil {
		return spineRunFailed("read "+place.Slug+"'s windows", err)
	}
	if !slices.Contains(strings.Fields(string(out)), agentName) {
		return exitf(ExitGeneric,
			"%s started and stopped at once on %s. `repose machine add` installs the agents; this one is not there or will not run.",
			agentName, m.Name)
	}
	if needsLogin && opts.Prompt != "" {
		if opts.NoAttach {
			return spineLoginRefusal(place.Slug, agentName)
		}
		_, _ = fmt.Fprintln(e.Out, spineLoginNotice(place.Slug))
	}
	if opts.NoAttach {
		_, _ = fmt.Fprintf(e.Out, "%s is running in %s on %s.\n", agentName, place.Slug, m.Name)
		return nil
	}
	return attachTmux(t, place.Slug, agentName, laptopTZ(), dir, nil, nil)
}

// spineLoginRefusal and spineLoginNotice are the hosted run's two
// sentences for a Claude Code that is not logged in (run.go, I-607),
// said again here: with --no-attach nobody logs in now, so the prompt is
// refused and the window named; attaching, the prompt is typed after the
// login. The window is the agent's name, which `repose attach -w` takes.
func spineLoginRefusal(slug, window string) error {
	return exitf(ExitGeneric, "Claude Code is not logged in on %s, so your prompt was not typed. Log in with `%s`, then run your prompt again.",
		slug, "repose attach "+slug+" -w "+window)
}

func spineLoginNotice(slug string) string {
	return "Claude Code is not logged in on " + slug + ". Log in in the window that opens; your prompt is typed after the login."
}

// spineSync sends the laptop's checkout and, in the same ssh, everything
// the box needs to do the work with it: the tool logins and the git
// identity (I-150, I-195), the Claude Code config (I-196), the
// gitignored .env files (I-197), the time zone (I-198) and the laptop's
// tools (I-221). It is run.go's own carry with the two parts a control
// plane answers for left out — the api's stored secrets and the probe
// `run` starts before the api replies — and it prints what it did: the ✓
// line, the sync's warnings and the carry's news. The MCP approvals it
// returns are the laptop's answers for this checkout, which the agent
// window's trust write copies (I-556).
func (e *Env) spineSync(ctx context.Context, t sshTarget, place machine.Project, root string, opts SpineRunOptions, tick func(string)) (mcpApprovals, error) {
	var appr mcpApprovals
	remote, _ := checkoutKeys(e, opts.Deps)
	skip, chosen := e.loginSkip(place.Slug)
	// Carry lines print only when they change (DECISIONS I-618). The key
	// is the machine's name and the project's: an owned machine has no
	// project id, and two machines may hold the same slug.
	noted := newCarryNoter(e.Dir, place.Machine+"/"+place.Slug)

	// The laptop's side of the carry (the .env walk, git config, the
	// Claude files) is read while the probe's ssh is in flight.
	type builtEnv struct {
		envs []envFile
		err  error
	}
	envDone := make(chan builtEnv, 1)
	go func() {
		envs, err := buildEnvCarry(root)
		envDone <- builtEnv{envs, err}
	}()
	type builtCarry struct {
		gc      *gitCarry
		gcErr   error
		cc      *claudeCarry
		tc      *toolsCarry
		mc      *mcpCarry
		mcNotes []string
	}
	carryDone := make(chan builtCarry, 1)
	go func() {
		var b builtCarry
		b.gc, b.gcErr = buildGitCarry(root, e.HomeDir)
		b.cc, _ = buildClaudeCarry(e.HomeDir)
		b.tc = buildToolsCarry(e.HomeDir, root, precedenceFor(e.personalOn, root))
		b.mc, b.mcNotes = buildMCPCarry(e.HomeDir, gitRepoRoot(root), place.Slug, "", toolBinsOf(b.tc))
		carryDone <- b
	}()

	sum, err := syncGuest(ctx, t, root, place.Slug, SyncOptions{
		StashRemote: opts.StashRemote, DiscardRemote: opts.DiscardRemote,
		// A run leaves a checkout that is already there alone, whatever
		// the laptop has; `repose sync` is what sends work over it
		// (I-367), and the line below says when there is some.
		FirstOnly: !opts.SyncOnly,
		Exclude:   e.Cfg.SyncExclude,
		NoRemote:  remote == "",
		RemoteURL: remote,
		EnvLater: func() []envFile {
			b := <-envDone
			if b.err != nil {
				e.warn("Could not list your .env files (%s); none were sent.", oneLine(b.err.Error()))
			}
			return b.envs
		},
		EnvOff: skip[envLogin],
		Carry: func(markers map[string]string) (*credCarry, error) {
			b := <-carryDone
			if b.mc != nil && !skip[mcpLogin] {
				appr = b.mc.Approvals
			}
			if b.gcErr != nil {
				e.warn("Could not read your git config (%s); the machine keeps its own.", oneLine(b.gcErr.Error()))
			}
			if b.gc != nil {
				for _, n := range b.gc.Notes {
					noted.warn(e, n)
				}
			}
			if b.cc != nil {
				for _, n := range b.cc.Notes {
					noted.warn(e, n)
				}
			}
			for _, n := range b.mcNotes {
				noted.warn(e, n)
			}
			// The MCP servers themselves are not carried: rendering them
			// into an agent's config is repose-mcp's, and a box the user
			// owns has not got it (ops/install/machine-install.sh
			// write_wrapper). The project's own .mcp.json travels in the
			// checkout, and its approvals above go with the window.
			return buildCredentialsAndCarry(e.HomeDir, root, credSyncOptions{
				RemoteURL: remote,
				Skip:      skip,
				Kept: func(label string) {
					noted.warn(e, keptLoginLine(label))
				},
			}, carryOptions{TZ: laptopTZ(), Git: b.gc, Claude: b.cc, Tools: b.tc, Markers: markers})
		},
	})
	if err != nil {
		return appr, spineRunFailed("sync "+place.Slug, err)
	}
	// A project named on the command line is named in the line's command
	// too: a bare `repose sync` here may mean another (I-631).
	named := ""
	if opts.ProjectArg != "" {
		named = place.Slug
	}
	step, line := spineSyncReport(sum, named)
	if step != "" {
		tick(step)
	}
	if line != "" {
		_, _ = fmt.Fprintln(e.Out, line)
	}
	for _, w := range sum.Warnings() {
		_, _ = fmt.Fprintln(e.ErrOut, w)
	}
	noted.print(e, sum.Copied, skip, chosen, sum.Carried)
	return appr, nil
}

// spineSyncReport is what one sync of an owned machine says: step is the
// ✓ line's text, line a plain line on stdout, either "" for none. A
// checkout the run left alone says nothing unless the laptop has work it
// never took, which `repose sync` is for (I-367).
func spineSyncReport(s *SyncSummary, named string) (step, line string) {
	switch {
	case s == nil:
		return "Synced", ""
	case s.Skipped && s.LaptopAhead:
		return "", laptopAheadLine(s, named)
	case s.Skipped:
		return "", ""
	}
	return syncWords(s), ""
}

// spineReposeRemote points the checkout's `repose` remote at the
// project's folder on the machine, over the machine's own ssh alias
// (`<machine>.repose`, machine.Alias), so an agent's commits come back
// with plain git. The hosted run's addReposeRemote decides this from the
// api's project and the directory cache; here the machine is named and
// the folder is ~/<slug>, so the shared half (ensureReposeRemote, which
// leaves a remote named repose that is the user's own alone) is called
// directly. A failure is a warning: the remote is a convenience beside
// the run.
func (e *Env) spineReposeRemote(root, machineName, slug string) {
	if root == "" || e.inHome() {
		return // a dotfiles repository in ~ is no machine's checkout (I-601)
	}
	note, err := ensureReposeRemote(root, machineName, slug)
	if err != nil {
		e.warn("Could not add the git remote repose (%s).", oneLine(err.Error()))
		return
	}
	if note != "" {
		_, _ = fmt.Fprintln(e.ErrOut, strings.TrimSpace(note))
	}
}

// spineRunPlace decides which machine and slug this run means: --machine
// names one, and without it the project map answers for the checkout.
func spineRunPlace(e *Env, opts SpineRunOptions) (machine.Machine, machine.Project, error) {
	if opts.MachineName == "" {
		return placeFor(e, opts.ProjectArg, opts.Deps)
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

// spineInitFailed is `project init`'s refusals. `exists` is a ~/<slug>
// the box owner made themselves: repose will not replace their file or
// link, so the way past it is another name for the project.
func spineInitFailed(slug, machineName string, err error) error {
	var ae *agentError
	if errors.As(err, &ae) && ae.Code == agentCodeExists {
		return exitf(ExitUsage, "~/%s on %s is not repose's. `repose run OTHERNAME --machine %s` prepares another folder.",
			slug, machineName, machineName)
	}
	return spineRunFailed("prepare "+slug, err)
}

// spineRunFailed names the step that failed, the way rewindFailed does.
// An error that is already a sentence of its own (the sync's stepFailed,
// a refusal with its own exit code) passes through untouched.
func spineRunFailed(step string, err error) error {
	var ae *agentError
	if errors.As(err, &ae) {
		return exitf(ExitGeneric, "Could not %s: %s", step, ae.Message)
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return err
	}
	return exitf(ExitGeneric, "Could not %s: %v", step, err)
}

// spineRunFor answers the options for a run on a machine the user owns,
// or nil when this checkout is a hosted project's. --machine names one;
// without it the project map decides, so a checkout stays on the machine
// its first run named.
func spineRunFor(e *Env, opts RunOptions) (*SpineRunOptions, error) {
	deps := defaultResolveDeps()
	if opts.Machine == "" {
		ps, err := machine.LoadProjects(e.Dir)
		if err != nil {
			return nil, exitf(ExitGeneric, "%v", err)
		}
		remote, dir := checkoutKeys(e, deps)
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
		MachineName:   opts.Machine,
		ProjectArg:    opts.ProjectArg,
		Prompt:        opts.Prompt,
		Agent:         opts.Agent,
		NoSync:        opts.NoSync,
		NoAttach:      opts.NoAttach,
		StashRemote:   opts.StashRemote,
		DiscardRemote: opts.DiscardRemote,
		// `repose sync` is the only command that sets it (cli.go
		// newSyncCmd): the whole sync, and no agent.
		SyncOnly: opts.Sync,
		Deps:     deps,
	}, nil
}
