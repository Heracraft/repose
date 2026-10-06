package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RunOptions is `repose run`'s flags (07-cli.md §5.1).
type RunOptions struct {
	Prompt        string
	Agent         string
	Size          string
	Name          string
	StashRemote   bool
	DiscardRemote bool
	NoSync        bool
	NoAttach      bool
	// Sync is `repose sync`: lay the laptop's work over the machine's
	// checkout. Without it, a run syncs only a machine with no checkout
	// yet (DECISIONS I-367).
	Sync        bool
	Worktree    bool     // the agent works in its own git worktree (I-253)
	Bridge      bool     // the laptop's Chrome is bridged in beside the attach (I-296)
	BridgeAllow []string // --bridge-allow: the bridge's allowlist (I-311)
	ProjectArg  string
	// On is --on: this folder joins that machine as another checkout
	// beside its own (DECISIONS I-480).
	On string
	// Temp is --temp's lifetime, 0 without it: a new temporary project
	// (DECISIONS I-347).
	Temp time.Duration
	// NoPersonal keeps the account's machine.nix off this machine, for
	// good (--no-personal, DECISIONS I-490).
	NoPersonal bool
}

// opPollInterval is how often an op (and the project, for the phase
// label) is read while waiting: two cheap GETs (I-154).
const opPollInterval = 500 * time.Millisecond
const opPollTimeout = 20 * time.Minute

// pollDelay is the pause before the next poll of a wait that began at
// started: opPollInterval for the first 30 s, where a create or start
// ends (10-15 s on host-01, so the 1 s step that began at 10 s cost half
// a second of every start, I-223), then 1 s, then 2 s after a minute, so
// a long build does not spend the account's api budget that a dashboard
// tab shares (I-187): two reads per poll is 240 of the 600 a minute.
func pollDelay(started time.Time) time.Duration {
	switch el := time.Since(started); {
	case el < 30*time.Second:
		return opPollInterval
	case el < time.Minute:
		return time.Second
	default:
		return 2 * time.Second
	}
}

const sshWaitTimeout = 60 * time.Second
const sshRetryInterval = time.Second

// runRun implements the whole `repose run` sequence, 07-cli.md §5.5.
// attachOnly runs only steps 1 (resolve, no create), 3, 4, 8 — what
// `repose attach` is (§5.5's last paragraph).
func runRun(ctx context.Context, e *Env, opts RunOptions, attachOnly bool) error {
	// A project this checkout ran before, with its ssh master still up:
	// attach needs no api call, and run's probe goes out beside the api's
	// answer instead of after it (DECISIONS I-223).
	var early *earlyProbe
	if attachOnly {
		if done, err := attachFast(ctx, e, e.resolveArg(opts.ProjectArg), opts.Bridge); done {
			return err
		}
	} else {
		if !opts.Sync && (opts.StashRemote || opts.DiscardRemote) {
			flag := "--stash-remote"
			if opts.DiscardRemote {
				flag = "--discard-remote"
			}
			return exitf(ExitUsage, "`repose run` no longer syncs a machine that already has your checkout; `repose sync %s` does.", flag)
		}
		if opts.Temp > 0 && opts.ProjectArg != "" {
			return exitf(ExitUsage, "--temp always creates a new machine; it cannot be used with --project (%s).", opts.ProjectArg)
		}
		if opts.On != "" && (opts.Temp > 0 || opts.Name != "" || opts.ProjectArg != "" || opts.Size != "") {
			return exitf(ExitUsage, "--on adds this folder to a machine you have; it cannot be used with --temp, --name, --project or --size.")
		}
		if opts.Name != "" && opts.ProjectArg != "" && opts.Name != opts.ProjectArg {
			return exitf(ExitUsage, "--name %s and --project %s name two projects; pass one.", opts.Name, opts.ProjectArg)
		}
		if opts.Temp == 0 && opts.Name == "" && opts.On == "" {
			// A guess from the cache: the checkout's project. --name and
			// --temp name another, and the probe (which creates the
			// checkout's directory) must not touch this one.
			early = startEarlyProbe(ctx, e, opts)
		}
		e.early = early
		// The boot probe reads the machine's own checkout, never another
		// one (I-480).
		ownCheckout := opts.On == "" && e.extraCheckout() == nil
		e.guestUp = func(p *Project) {
			startBootProbe(ctx, e, p, ownCheckout && !opts.NoSync && gitRepoRoot(e.Cwd) != "")
		}
	}

	// Other projects left running with nobody on them (I-262), read
	// beside the command and printed once connected.
	idleNote := startIdleNote(ctx, e)

	// The account's machine.nix, read beside the resolve; pushed before a
	// create, so the new machine's first revision has it, or once an
	// existing machine runs, so its build never holds up the start
	// (DECISIONS I-490).
	var personal *personalStep
	if !attachOnly {
		personal = e.startPersonal(ctx)
	}

	pr := e.newProgress()
	defer pr.Fail() // clears a spinner line left by an early return

	// Every refusal the sync would make of the checkout alone comes
	// before a machine is created or started for it (I-353); the git
	// reads run beside the api's.
	precheck := make(chan error, 1)
	if !attachOnly && !opts.NoSync {
		go func() { precheck <- syncPrecheck(syncRoot(e.Cwd)) }()
	} else {
		precheck <- nil
	}
	endResolve := timeSpan("phase resolve")
	res, err := resolveForRun(ctx, e, opts, attachOnly)
	if err != nil {
		return err
	}
	if !attachOnly && opts.Agent == "" && opts.Prompt != "" && !strings.ContainsAny(strings.TrimSpace(opts.Prompt), " \t\n") {
		if err := refusePromptThatIsASlug(ctx, e, opts.Prompt); err != nil {
			return err
		}
	}
	skipSync := false
	if err := <-precheck; err != nil {
		if gitRepoRoot(e.Cwd) != "" {
			return err
		}
		skipSync = true // outside a repository: an empty machine (I-358)
	}

	endResolve()
	if !attachOnly && res.Project != nil && res.Project.State == "destroying" {
		if err := startOver(ctx, e, res, &opts, pr); err != nil {
			return err
		}
	}
	project := res.Project
	if project == nil {
		if attachOnly {
			return errNoProjectFoundFor(res.Remote, e.Command)
		}
		personal.finish(ctx, e, opts.NoPersonal, nil)
		project, err = createProjectForRun(ctx, e, res.CreateRemote(), opts, pr)
		if err != nil {
			return err
		}
	}

	endEnsure := timeSpan("phase ensure-running")
	// resolveProject read the project from the api a moment ago; a
	// second read before acting on its state is a round trip for nothing
	// (DECISIONS I-223). A project just created is read again.
	fresh := res.Project != nil
	wasRunning := fresh && project.State == "running" && !guestdDead(project)
	if attachOnly {
		if !fresh {
			p, err := e.Client.GetProject(ctx, project.ID)
			if err != nil {
				return err
			}
			*project = *p
		}
		if project.State != "running" {
			return notRunningError(project)
		}
	} else if err := ensureRunningFrom(ctx, e, project, pr, fresh); err != nil {
		return err
	}
	// The probe that stands in for the sync's own: the early one, or the
	// one started when the guest came up (I-237), which replaced it.
	early = e.early
	early.settle(ctx, e, project, wasRunning)

	endEnsure()
	if !attachOnly {
		personal.finish(ctx, e, opts.NoPersonal, project)
		if opts.NoPersonal && !project.PersonalOptOut {
			e.optOutPersonal(ctx, project)
		}
	}
	tz := laptopTZ()
	tzSaved := saveProjectTZ(ctx, e, project, tz)

	pr.Phase("Connecting to "+project.Slug, "")
	endConnect := timeSpan("phase connect")
	target, err := connect(ctx, e, project)
	if err != nil {
		return err
	}
	endConnect()
	pr.End()
	_, _ = fmt.Fprintf(e.Out, "Connected to %s (%s)\n", project.Slug, project.Class)
	idleNote(project.ID)
	// Another checkout of the machine (I-480): the folder's, or the one
	// PROJECT:CHECKOUT names. --on the first time makes it.
	target.Checkout = res.Checkout
	if !attachOnly && opts.On != "" && res.Checkout == "" {
		name, err := e.addCheckout(ctx, target, project)
		if err != nil {
			return err
		}
		target.Checkout = name
	}

	helper := sessionOptions{Slug: project.Slug, Target: target.Args, TZ: tz, HomeDir: e.HomeDir, Forward: os.Getenv(forwardEnvOff) != "1", Bridge: opts.Bridge || len(opts.BridgeAllow) > 0, BridgeAllow: opts.BridgeAllow, Checkout: target.Checkout}
	if skip, _, _ := e.Cfg.loginSkip(project.Slug); skip[mcpLogin] {
		helper.MCPOff = true // I-556: attach honours the off switch too
	}
	// This folder's checkout on the machine: the machine's own (the same
	// remote), or another one the folder is (I-480), not one that
	// PROJECT:CHECKOUT named from elsewhere.
	folderIsCheckout := res.Remote != "" && res.Remote == project.RemoteURL
	if target.Checkout != "" {
		folderIsCheckout = !strings.Contains(e.resolveArg(opts.ProjectArg), ":")
	}
	if root := gitRepoRoot(e.Cwd); root != "" && folderIsCheckout {
		// The git carry needs the project's own checkout: its includeIf
		// rules and identity are what the guest should get, and a run
		// from anywhere else would carry some other repository's.
		helper.RepoDir = root
	}
	// After the attach on the input-proxy path: a temporary machine whose
	// session has ended goes at once (I-352).
	afterAttach := func() { tempSessionEnded(ctx, e, target, project) }
	if attachOnly {
		// The carry runs beside the attach, never before it (I-195).
		helper.Carry = true
		e.addReposeRemote(ctx, project, target, nil) // I-272
		startSessionHelper(e, helper)
		tzSaved()
		if l := tempLine(project, time.Now()); l != "" {
			_, _ = fmt.Fprintln(e.ErrOut, l)
		}
		defer e.keepTokenFresh()()
		return attachTmux(target, project.Slug, "", tz, helper.RepoDir, afterAttach, renewFor(e, project))
	}

	// The machine's checkout, as its sync or carry found it (I-368).
	var checkout *string
	// The laptop's .mcp.json answers for this repository, which the agent
	// window's trust write copies (I-556); read with the carry.
	var mcpAppr mcpApprovals
	if skipSync {
		_, _ = fmt.Fprintln(e.Out, "Not a git repository, so nothing was synced.")
		checkout = e.carryWithoutSync(ctx, target, project, helper.RepoDir, tz)
	} else if !opts.NoSync {
		repoRoot := gitRepoRoot(e.Cwd)
		if repoRoot == "" {
			repoRoot = e.Cwd
		}
		pr.Phase("Syncing", "")
		endSync := timeSpan("phase sync")
		skip, chosen := e.loginSkip(project.Slug)
		// Tool logins, the git identity and the carry run first in the
		// sync's apply ssh, so the checkout lands in a guest whose git
		// already knows the user and how to reach the remote (I-150), and
		// the carry costs no round trip (I-195..I-198, I-224).
		// The laptop's side of the carry (the .env walk, git config, the
		// Claude files) is read while the probe's ssh is in flight, not
		// before it (review of workstream 15, item 7).
		type builtEnv struct {
			envs []envFile
			err  error
		}
		envDone := make(chan builtEnv, 1)
		go func() {
			envs, err := buildEnvCarry(repoRoot)
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
			b.gc, b.gcErr = buildGitCarry(repoRoot, e.HomeDir)
			b.cc, _ = buildClaudeCarry(e.HomeDir)
			b.tc = buildToolsCarry(e.HomeDir, repoRoot, precedenceFor(e.personalOn, repoRoot))                                 // I-221, I-222, I-490
			b.mc, b.mcNotes = buildMCPCarry(e.HomeDir, gitRepoRoot(repoRoot), project.Slug, target.Checkout, toolBinsOf(b.tc)) // I-556
			carryDone <- b
		}()
		waitEnv := func() []envFile {
			b := <-envDone
			if b.err != nil {
				e.warn("Could not list your .env files (%s); none were sent.", oneLine(b.err.Error()))
			}
			return b.envs
		}
		// Another checkout has the folder's own remote, not the machine's
		// (I-480).
		remoteURL := project.RemoteURL
		if target.Checkout != "" {
			remoteURL = res.Remote
		}
		summary, err := syncGuest(ctx, target, repoRoot, project.Slug, SyncOptions{
			StashRemote: opts.StashRemote, DiscardRemote: opts.DiscardRemote, FirstOnly: !opts.Sync,
			Exclude: e.Cfg.SyncExclude, NoRemote: remoteURL == "", RemoteURL: remoteURL,
			EnvLater: waitEnv,
			EnvOff:   skip[envLogin],
			Probe:    early.forProject(),
			Carry: func(markers map[string]string) (*credCarry, error) {
				b := <-carryDone
				gc, cc := b.gc, b.cc
				if b.mc != nil && !skip[mcpLogin] {
					mcpAppr = b.mc.Approvals
				}
				if b.gcErr != nil {
					e.warn("Could not read your git config (%s); the guest keeps its own.", oneLine(b.gcErr.Error()))
				}
				if gc != nil {
					for _, n := range gc.Notes {
						e.warn("%s", n)
					}
				}
				if cc != nil {
					for _, n := range cc.Notes {
						e.warn("%s", n)
					}
				}
				for _, n := range b.mcNotes {
					e.warn("%s", n)
				}
				return buildCredentialsAndCarry(e.HomeDir, repoRoot, credSyncOptions{
					RemoteURL: remoteURL,
					Skip:      skip,
					Kept: func(label string) {
						e.warn("Kept the guest's %s login: it is newer than the laptop's.", label)
					},
				}, carryOptions{TZ: tz, Git: gc, Claude: cc, Tools: b.tc, MCP: b.mc, Markers: markers})
			},
		})
		if err != nil {
			return err
		}
		endSync()
		pr.End()
		checkout = &summary.Checkout
		if l := syncResultLine(summary, opts.NoAttach); l != "" {
			_, _ = fmt.Fprintln(e.Out, l)
		}
		if summary.Created {
			_, _ = fmt.Fprintf(e.Out, "Checkout: %s on the machine\n", tildePath(summary.Checkout))
		}
		for _, w := range summary.Warnings() {
			_, _ = fmt.Fprintln(e.ErrOut, w)
		}
		if len(summary.Copied) > 0 {
			_, _ = fmt.Fprintf(e.Out, "Credentials: %s\n", strings.Join(summary.Copied, ", "))
			if l := loginsLine(summary.Copied, skip, chosen); l != "" {
				_, _ = fmt.Fprintln(e.Out, l)
			}
		}
		if summary.Carried != nil {
			for _, l := range summary.Carried.Lines() {
				_, _ = fmt.Fprintln(e.ErrOut, l)
			}
		}
	} else {
		checkout = e.carryWithoutSync(ctx, target, project, helper.RepoDir, tz)
	}
	// The machine has its checkout now: point this checkout's `repose`
	// remote at it (I-272).
	e.addReposeRemote(ctx, project, target, checkout)
	// The checkout's repose.nix is the machine's configuration (I-489).
	if !skipSync && !opts.NoSync {
		e.applyRepoConfig(ctx, project, gitRepoRoot(e.Cwd), opts.Temp > 0 || project.ExpiresAt != nil)
	}

	window := ""
	if opts.Prompt != "" {
		agent := opts.Agent
		if agent == "" {
			agent = project.AgentDefault
		}
		if agent == "" {
			agent = e.Cfg.DefaultAgent
		}
		pr.Phase("Starting "+agent, "")
		name, dir := "", ""
		if checkout != nil {
			dir = tildePath(*checkout)
		}
		if opts.Worktree {
			wt, err := prepareWorktree(ctx, target, project.Slug, agent)
			if err != nil {
				return err
			}
			name, dir = wt.Window, wt.Dir
			pr.End()
			_, _ = fmt.Fprintf(e.Out, "Worktree: %s on branch %s\n", wt.Dir, wt.Branch)
			if wt.Env > 0 {
				_, _ = fmt.Fprintf(e.Out, "Copied %d .env %s from %s\n", wt.Env, plural(wt.Env, "file", "files"), tildePath(wt.Checkout))
			}
			if wt.Dirty {
				_, _ = fmt.Fprintf(e.ErrOut, "The worktree starts at the last commit; the uncommitted changes in %s are not in it.\n", tildePath(wt.Checkout))
			}
			pr.Phase("Starting "+agent, "")
		} else {
			n, othersOpen, err := windowNameFor(ctx, target, project.Slug, agent)
			if err != nil {
				return stepFailed("list the guest's tmux windows", err, "")
			}
			name = n
			if othersOpen {
				pr.Fail()
				_, _ = fmt.Fprintf(e.ErrOut, "Another %s window is open; two agents share one working tree. `repose run --worktree` gives the next one its own.\n", agent)
				pr.Phase("Starting "+agent, "")
			}
		}
		window = name

		attachInstead := false
		if agent == "claude" {
			hasSecret, err := hasOAuthSecret(ctx, e.Client, project.ID)
			if err != nil {
				return err
			}
			attachInstead, err = needsClaudeLogin(ctx, target, hasSecret)
			if err != nil {
				return err
			}
		}
		loadingDevShell := func() { pr.Phase("Loading the project's dev shell", "Dev shell loaded") }
		err := startAgentWindow(ctx, target, project.Slug, name, dir, agent, opts.Prompt, attachInstead, loadingDevShell, mcpAppr)
		var dialog *agentDialogError
		if errors.As(err, &dialog) {
			// The pre-trust did not take (I-486): the window is open
			// on the dialog, and the prompt was not typed.
			pr.Fail()
			if opts.NoAttach {
				return exitf(ExitGeneric, "%s, so your prompt was not typed. Answer it in the %s window with `repose attach %s`, then type your prompt there.", dialog.Error(), name, project.Slug)
			}
			_, _ = fmt.Fprintf(e.ErrOut, "%s, so your prompt was not typed. Answer it in the window that opens, then type your prompt there.\n", dialog.Error())
		} else if err != nil {
			return stepFailed("start "+agent+" in the guest", err, "")
		}
		pr.End()
		if attachInstead {
			_, _ = fmt.Fprintln(e.Out, "Claude Code is not logged in on this guest yet. Finish the login in the window that opens, then re-run with your prompt.")
		}
	}

	_, _ = fmt.Fprintf(e.ErrOut, "Ready in %s.\n", fmtElapsed(pr.Total()))
	if l := tempLine(project, time.Now()); l != "" {
		_, _ = fmt.Fprintln(e.ErrOut, l)
	}
	tzSaved()
	if opts.NoAttach {
		return nil
	}
	startSessionHelper(e, helper)
	defer e.keepTokenFresh()()
	return attachTmux(target, project.Slug, window, tz, helper.RepoDir, afterAttach, renewFor(e, project))
}

// syncResultLine is what a run prints about its sync. With nothing new on
// the laptop (the apply skipped, I-224, I-248) a run that attaches says
// nothing (I-303); `repose sync` and --no-attach have nothing else to
// say, so they say that.
func syncResultLine(s *SyncSummary, noAttach bool) string {
	switch {
	case s.Skipped && s.LaptopAhead:
		return laptopAheadLine(s)
	case s.Skipped:
		return ""
	case !s.Unchanged:
		return s.String()
	case noAttach:
		return "Nothing new to sync: the machine already has this checkout."
	}
	return ""
}

// laptopAheadLine is what a run that left the machine's checkout alone
// says when the laptop has work the machine never took (I-367).
func laptopAheadLine(s *SyncSummary) string {
	var parts []string
	if s.Modified > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", s.Modified))
	}
	if s.Untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", s.Untracked))
	}
	switch {
	case s.Commits == 1:
		parts = append(parts, "1 commit")
	case s.Commits > 1:
		parts = append(parts, fmt.Sprintf("%d commits", s.Commits))
	}
	return fmt.Sprintf("Not synced: your laptop has work the machine doesn't (%s). `repose sync` sends it.", strings.Join(parts, ", "))
}

// saveProjectTZ moves the project's stored zone to the laptop's when they
// differ (I-198), so the next start's SetupProject writes the zone this
// command puts in the running guest. It runs beside the connect; the
// returned wait is called before the attach and gives it at most two
// seconds, since a missed update only means the next start writes the
// old zone until the next run fixes it again.
func saveProjectTZ(ctx context.Context, e *Env, project *Project, tz string) (wait func()) {
	if tz == "" || (project.TZ != nil && *project.TZ == tz) {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		z := tz
		_, _ = e.Client.PatchProject(ctx, project.ID, PatchProjectRequest{TZ: &z})
	}()
	return func() {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}

// refusePromptThatIsASlug catches `repose run izma`: every other command
// takes the project as its argument (I-155), but `run`'s argument is the
// prompt, and a one-word prompt that is exactly a project's name is far
// more likely a slip than an instruction to an agent.
func refusePromptThatIsASlug(ctx context.Context, e *Env, prompt string) error {
	projects, err := e.Client.ListProjects(ctx)
	if err != nil {
		return nil // the check is a courtesy; the run itself reports a real api failure
	}
	for _, p := range projects {
		if p.Slug == prompt {
			return exitf(ExitUsage, "%q is one of your projects, but `repose run`'s argument is a prompt for the agent. To work on it: `repose run --project %s` (or `repose attach %s`). To send the word itself as a prompt, name the agent: `repose run --agent claude %s`.", prompt, prompt, prompt, prompt)
		}
	}
	return nil
}

// connect is steps 3 and 4: the certificate and config, then the first
// connection (which, with multiplexing, is the one every later ssh in
// this command rides). It warns when the alias does not work from a
// plain terminal and uses the generated config directly in that case.
func connect(ctx context.Context, e *Env, project *Project) (sshTarget, error) {
	if t, ok := e.early.connected(ctx, e, project); ok {
		return t, nil
	}
	if t, ok, err := connectFast(ctx, e, project); ok {
		return t, err
	}
	endAPI := timeSpan("connect api (me, projects)")
	// The two reads are independent: one round trip, not two (I-223).
	var me *Me
	var meErr error
	meDone := make(chan struct{})
	go func() {
		defer close(meDone)
		me, meErr = e.Client.GetMe(ctx)
	}()
	allProjects, err := e.Client.ListProjects(ctx)
	<-meDone
	if meErr != nil {
		return sshTarget{}, meErr
	}
	if err != nil {
		return sshTarget{}, err
	}
	endAPI()
	params := certParams{Handle: me.Handle, Projects: allProjects, CheckAlias: e.TargetFor == nil}
	endCert := timeSpan("connect cert+ssh files")
	cr, err := ensureCert(ctx, e.Client, params, nil)
	if err != nil {
		return sshTarget{}, err
	}
	endCert()
	target := e.target(project.Slug)
	if cr.AliasProblem != "" {
		e.warn("warning: %s\n(repose itself uses ~/.ssh/repose/config directly until then.)", cr.AliasProblem)
		if e.TargetFor == nil {
			if sd, err := sshDir(); err == nil {
				target = sshTarget{Args: []string{"-F", filepath.Join(sd, "config"), project.Slug + ".repose"}}
			}
		}
	}
	defer timeSpan("connect first ssh")()
	err = waitForSSH(ctx, target, certRefusalHandler(func() error {
		params.Force = true
		_, err := ensureCert(ctx, e.Client, params, nil)
		return err
	}))
	if err != nil {
		return sshTarget{}, err
	}
	return target, nil
}

// The gateway's refusals that are about the certificate itself
// (docs/interfaces/ssh-gateway.md, internal/gateway/server.go): a fresh
// certificate can fix these. The others (busy, rate limited, the control
// plane, a guest not accepting yet, a stopped or unknown project) cannot,
// and must not spend the one re-issue.
var (
	certRefusals = []string{
		"certificate revoked", "certificate expired", "certificate not yet valid",
		"certificate not signed by the repose ca", "certificate required", "certificate not valid for this project",
	}
	otherRefusals = []string{
		"gateway busy", "too many open connections", "too many authentication attempts", "cannot reach control plane",
		"not accepting connections yet", "is stopped", "no such project", "login name must be",
	}
)

// isCertRefusal reports whether ssh's failure is the gateway refusing the
// certificate: one of its certificate banners, or a bare `Permission
// denied` with none of its other banners (a gateway too old to say).
func isCertRefusal(se *sshError) bool {
	if se.ExitCode != 255 {
		return false
	}
	s := strings.ToLower(se.Stderr)
	for _, m := range certRefusals {
		if strings.Contains(s, m) {
			return true
		}
	}
	for _, m := range otherRefusals {
		if strings.Contains(s, m) {
			return false
		}
	}
	return strings.Contains(s, "permission denied")
}

// certRefusalHandler is connect's answer to a refused connection
// (DECISIONS I-175): the first certificate refusal (revoked, expired, a
// CA rotation, a project the certificate predates) gets one forced
// re-issue and an immediate retry; a certificate refusal after that ends
// the wait at once with what the gateway said, instead of 60 s of retries
// ending in "SSH did not answer". Any other refusal keeps waiting.
func certRefusalHandler(reissue func() error) func(*sshError) (bool, error) {
	reissued := false
	return func(se *sshError) (bool, error) {
		if !isCertRefusal(se) {
			return false, nil
		}
		if !reissued {
			reissued = true
			if err := reissue(); err != nil {
				return false, err
			}
			return true, nil
		}
		return false, exitf(ExitGeneric, "The gateway refused a certificate issued just now (%s). `repose login` (as the account that owns the project) and try again; if it still refuses, an operator may have revoked your certificates.", sshStderrDetail(se.Stderr))
	}
}

// warn prints one warning to stderr.
func (e *Env) warn(format string, args ...any) {
	if e.active != nil {
		_, _ = fmt.Fprintf(e.active, format+"\n", args...)
		return
	}
	_, _ = fmt.Fprintf(e.ErrOut, format+"\n", args...)
}

func hasOAuthSecret(ctx context.Context, c *Client, projectID string) (bool, error) {
	secrets, err := c.ListSecrets(ctx, projectID)
	if err != nil {
		return false, err
	}
	for _, s := range secrets {
		if s.Name == "CLAUDE_CODE_OAUTH_TOKEN" {
			return true, nil
		}
	}
	return false, nil
}

// attachTmux is step 8: ssh -t <slug>.repose tmux attach [-t
// <slug>:<window>]. On macOS and Linux ssh runs under the input proxy
// (I-280), which gets dropped files and Ctrl+V images to the session;
// repoDir is the laptop checkout that is ~/<slug> on the machine, "" when
// the attach is not from the project's own checkout. With
// REPOSE_INPUT_PROXY=0, on Windows, or without a terminal, the CLI process
// is replaced by ssh as before.
//
// tz, when known, travels as the session's TZ (sshd's AcceptEnv and the
// gateway pass it), so a base whose tmux takes TZ from the attaching
// client (update-environment) gets the laptop's zone rather than none.
//
// after, when not nil, runs once the attach has returned on the
// input-proxy path, the only one where the CLI is still there to run it
// (a temporary machine's session-end check, I-352).
//
// On that path a dropped connection attaches again once the machine
// answers (I-469); renew, when not nil, is how a certificate that ended
// the connection by expiring (I-436) is replaced.
func attachTmux(t sshTarget, slug, window, tz, repoDir string, after func(), renew func(context.Context) error) error {
	extra := []string{"-t"}
	if tz != "" {
		if err := os.Setenv("TZ", tz); err == nil {
			extra = append(extra, "-o", "SendEnv=TZ")
		}
	}
	remote := attachCommand(slug, t.Checkout, window)
	if inputProxyEnabled() {
		args := append(append(append([]string{}, extra...), t.Args...), remote)
		re := newReattacher(t, slug, renew)
		re.args = append(append(append([]string{}, extra...), t.Args...), attachCommand(slug, t.Checkout, ""))
		if handled, err := runInputProxy(args, newDropHandler(t, slug, repoDir), re); handled {
			if after != nil {
				after()
			}
			return err
		}
	}
	return execReplaceSSH(t, extra, remote)
}

// attachCommand is the guest-side command of the attach. With a window
// (the agent `repose run PROMPT` just started), an agent that exited in
// the moment between its prompt and the attach has taken its window with
// it, and `tmux attach -t <slug>:<window>` failed with "can't find window"
// (I-304). The window is checked on the guest, in the same ssh, and when
// it is gone the attach goes to the session with one line saying why: on
// the terminal (seen after a detach) and in tmux's status line.
//
// Every attach sets the session's working directory to the checkout
// (`-c`), so a window opened with Ctrl-b c starts there even when the
// session began in the home directory before the first sync (I-368).
//
// In another checkout (extra, I-480) an attach with no window opens the
// checkout's own: the one of its windows ("<checkout>" or
// "<checkout>/...") used last, else a new shell window "<checkout>"
// there. A checkout the machine does not have is refused, exit 2.
func attachCommand(slug, extra, window string) string {
	co := checkoutVar(slug, extra)
	if window == "" && extra != "" {
		missing := fmt.Sprintf("%s has no checkout %s. `repose run --on %s` in its folder adds it.", slug, extra, slug)
		return co + fmt.Sprintf(`[ -d "$repose_co" ] || { printf '%%s\n' %[3]s >&2; exit 2; }
repose_w=$(tmux list-windows -t %[1]s -F '#{window_activity} #{window_name}' 2>/dev/null | while read -r a n; do case $n in %[2]s|%[2]s/*) printf '%%s %%s\n' "$a" "$n" ;; esac; done | sort -n | tail -n 1 | cut -d' ' -f2-)
if [ -z "$repose_w" ]; then repose_w=%[2]s; tmux new-window -d -t %[1]s -n "$repose_w" -c "$repose_co"; fi
exec tmux attach -t %[1]s:"$repose_w" -c "$repose_co"`, shQuote(slug), shQuote(windowPrefix(extra)), shQuote(missing))
	}
	if window == "" {
		return co + fmt.Sprintf(`exec tmux attach -t %s -c "$repose_co"`, shQuote(slug))
	}
	target := shQuote(slug + ":" + window)
	msg := fmt.Sprintf("The %s window closed before the attach: the agent in it exited. Attached to the session instead; start the agent again there.", window)
	return co + fmt.Sprintf("if tmux has-session -t %[1]s 2>/dev/null; then exec tmux attach -t %[1]s -c \"$repose_co\"; fi; printf '%%s\\n' %[3]s >&2; exec tmux attach -t %[2]s -c \"$repose_co\" \\; display-message -d 10000 %[3]s",
		target, shQuote(slug), shQuote(msg))
}

// waitForSSH is step 4: `ssh <target> true` until it answers, for up to
// sshWaitTimeout. onRefused is offered each ssh failure and returns true
// when it changed something worth an immediate retry (a re-issued
// certificate).
func waitForSSH(ctx context.Context, t sshTarget, onRefused func(*sshError) (bool, error)) error {
	deadline := time.Now().Add(sshWaitTimeout)
	for {
		err := runSSHOK(ctx, t, "true")
		if err == nil {
			return nil
		}
		var se *sshError
		if errors.As(err, &se) && se.ExitCode == -1 {
			return exitf(ExitGeneric, "Could not run ssh: %v. repose needs the OpenSSH client (`ssh`) on your PATH.", se.Err)
		}
		if se != nil && onRefused != nil {
			retry, err := onRefused(se)
			if err != nil {
				return err
			}
			if retry {
				continue
			}
		}
		if time.Now().After(deadline) {
			detail := ""
			if se != nil {
				detail = sshStderrDetail(se.Stderr)
			}
			msg := "Guest is running but SSH did not answer in 60s. `repose logs --kind console` may show why."
			if detail != "" {
				msg += "\nLast error from ssh: " + detail
			}
			return exitf(ExitGeneric, "%s", msg)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sshRetryInterval):
		}
	}
}

// keepTokenFresh keeps the access token fresh while an attach runs
// (I-491) and returns the function that stops it. A token source that
// cannot refresh (tests, not logged in) makes it a no-op.
func (e *Env) keepTokenFresh() func() {
	if e.Client == nil {
		return func() {}
	}
	kf, ok := e.Client.Tokens.(interface{ KeepFresh(context.Context) })
	if !ok {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go kf.KeepFresh(ctx)
	return cancel
}

// renewFor is a dropped attach's certificate renewal (I-469): connect's
// slow path, which issues a certificate when the one on disk no longer
// covers the project and checks the machine answers.
func renewFor(e *Env, project *Project) func(context.Context) error {
	return func(ctx context.Context) error {
		_, err := connect(ctx, e, project)
		return err
	}
}

// ensureRunningFrom is step 2: wait out a create or start in flight,
// start a stopped (or errored) guest, and wait for the op, streaming the
// build log when the op carries one. Every phase shows on pr. fresh says
// *project was read from the api by this command a moment ago, and is
// acted on without reading it again.
func ensureRunningFrom(ctx context.Context, e *Env, project *Project, pr *progress, fresh bool) error {
	var err error
	var p *Project
	if !fresh {
		created := project.OpID
		if p, err = e.Client.GetProject(ctx, project.ID); err != nil {
			return err
		}
		*project = *p
		// A create that failed at once (no host with capacity) has
		// already left the project in error with no op in flight by the
		// time it is read back; starting it then fails with "project has
		// no guest", which hid the capacity error (dogfood, I-356). Say
		// what the create said.
		if project.State == "error" && project.OpID == "" && created != "" {
			if op, err := e.Client.GetOp(ctx, project.ID, created); err == nil && op.State == "error" {
				pr.Fail()
				return e.opFailed("create", project.Slug, op.Error, nextAfterFailedStart(project.Slug, op.Error.Code))
			}
		}
	}
	if project.State == "running" && !guestdDead(project) {
		return nil
	}
	// A project just created (or being started by someone else) has an op
	// in flight; starting it again is the conflict the first real run hit
	// ("recruiting is already starting", DECISIONS I-106). Wait on that op
	// when the api named it, otherwise on the state, then re-read.
	// "building" is the create op's first phase (05 §5.3): the project is
	// in it a moment after POST /projects answers "creating", which is what
	// the first M3 run hit (DECISIONS I-114).
	switch project.State {
	case "creating", "building", "starting", "stopping":
		if project.OpID != "" {
			op, err := waitOpPhased(ctx, e, project, project.OpID, pr, true)
			if err != nil {
				return err
			}
			if op.State == "error" {
				return failedStart(e, project, op, pr)
			}
			if e.guestUp != nil && project.State != "stopping" {
				e.guestUp(project)
			}
		} else if err := waitState(ctx, e, project, pr); err != nil {
			return err
		}
		p, err = e.Client.GetProject(ctx, project.ID)
		if err != nil {
			return err
		}
		*project = *p
		if project.State == "running" {
			return nil
		}
	case "restoring", "destroying", "destroyed":
		return notRunningError(project)
	}

	var sr *StartResult
	if project.State == "running" {
		// guestd stopped answering: the api restarts it (I-157), or, if its
		// newer sample says guestd is back, answers "already running".
		sr, err = e.Client.StartProject(ctx, project.ID)
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == "conflict" {
			return nil
		}
	} else {
		err = retryOnOpConflict(ctx, func() error {
			var err error
			sr, err = e.Client.StartProject(ctx, project.ID)
			return err
		})
	}
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == "payment_required" {
			return exitf(ExitPaymentRequired, "%s", paymentRequiredMessage(apiErr))
		}
		if errors.As(err, &apiErr) && apiErr.Code == "capacity" {
			return exitf(ExitCapacity, "No capacity right now; try again in a few minutes. (We have been alerted.)")
		}
		return err
	}
	byState := true
	switch {
	case sr.Create:
		pr.Phase("Creating "+project.Slug, "Created "+project.Slug)
	case sr.Restart:
		pr.Phase(fmt.Sprintf("Restarting %s (its agent stopped answering)", project.Slug), "Restarted "+project.Slug)
		byState = false
	default:
		pr.Phase("Starting "+project.Slug, "Started "+project.Slug)
	}
	op, err := waitOpPhased(ctx, e, project, sr.OpID, pr, byState)
	if err != nil {
		return err
	}
	if op.State == "error" {
		return failedStart(e, project, op, pr)
	}
	pr.End()
	if e.guestUp != nil {
		// The first connection goes out now, beside the read below
		// (I-237).
		e.guestUp(project)
	}
	p, err = e.Client.GetProject(ctx, project.ID)
	if err != nil {
		return err
	}
	*project = *p
	return nil
}

// failedStart reports a create or start op that ended in error: a build's
// own failure is the Nix block and exit 10 (07-cli.md §5.8); anything else
// is one sentence and the next step.
func failedStart(e *Env, project *Project, op *Op, pr *progress) error {
	pr.Fail()
	switch op.Error.Code {
	case "build_failed", "eval_failed", "closure_too_large", "build_timeout":
		RenderBuildError(e.ErrOut, op.Error.Code, op.Error.Message, "repose.nix", nil)
		_, _ = fmt.Fprintf(e.ErrOut, "Fix it with `repose config edit --project %s`.\n", project.Slug)
		return silent(ExitBuildFailed)
	}
	return e.opFailed("start", project.Slug, op.Error, nextAfterFailedStart(project.Slug, op.Error.Code))
}

// opFailed is humanize.go's opFailed plus the host's own wording under -v.
func (e *Env) opFailed(verb, slug string, oe OpError, next string) error {
	err := opFailed(verb, slug, oe, next)
	if e.Verbose && oe.Detail != nil {
		if ee, ok := err.(*exitError); ok {
			ee.msg += fmt.Sprintf("\n(detail: %v)", oe.Detail)
		}
	}
	return err
}

// waitState polls the project until it leaves a transitional state,
// showing each state as a phase.
func waitState(ctx context.Context, e *Env, project *Project, pr *progress) error {
	started := time.Now()
	deadline := started.Add(opPollTimeout)
	last := ""
	for {
		p, err := e.Client.GetProject(ctx, project.ID)
		if err != nil {
			return err
		}
		*project = *p
		switch p.State {
		case "creating", "building", "starting", "stopping":
		default:
			return nil
		}
		if p.State != last {
			last = p.State
			if label, done := phaseForState(p.Slug, p.State); label != "" {
				pr.Phase(label, done)
			}
		}
		if time.Now().After(deadline) {
			return exitf(ExitGeneric, "%s has been %s for %s; `repose status %s` shows where it is.", p.Slug, p.State, opPollTimeout, p.Slug)
		}
		if err := sleepOrDone(ctx, pollDelay(started)); err != nil {
			return err
		}
	}
}

// waitOpPhased waits on an op like waitOp, streaming any build log
// through pr, and (when byState) relabels the phase from the project's
// state on every poll: "Building the environment", then "Booting".
func waitOpPhased(ctx context.Context, e *Env, project *Project, opID string, pr *progress, byState bool) (*Op, error) {
	var w *opWatch
	if byState {
		last := ""
		w = &opWatch{
			show: func(state string) {
				if state == "" || state == last {
					return
				}
				last = state
				if label, done := phaseForState(project.Slug, state); label != "" {
					pr.Phase(label, done)
				}
			},
			read: func() string {
				p, err := e.Client.GetProject(ctx, project.ID)
				if err != nil {
					return ""
				}
				return p.State
			},
		}
	}
	var out io.Writer = pr
	if pr == nil {
		out = e.ErrOut
	}
	return waitOpWith(ctx, e.Client, project.ID, opID, out, w)
}

// waitOp polls an op to completion, streaming its build log to out if one
// appears (07-cli.md §5.5 step 2, §5.8).
func waitOp(ctx context.Context, c *Client, projectID, opID string, out io.Writer) (*Op, error) {
	return waitOpWith(ctx, c, projectID, opID, out, nil)
}

// opWatch relabels a wait's progress from the project's state: show gets
// each state learned, read fetches it (GET /projects/:id) when the op
// read does not carry it (an api without I-236).
type opWatch struct {
	show func(state string)
	read func() string
	// line, when set, gets each build log line instead of out's
	// "nix › " print, and phase each read's op phase (a config op's
	// steps, I-320).
	line  func(line string)
	phase func(phase string)
}

// opWaitHold is how long one op read asks the api to hold (I-236; the api
// caps it at 20 s, under every proxy's idle timeout).
const opWaitHold = 20 * time.Second

// opTransientBudget is how long a wait rides out the api being away (a
// Coolify rolling redeploy answers 502 or 504, or drops the connection,
// for a few seconds) before the failure reaches the user.
var opTransientBudget = 30 * time.Second

// opTransientPause is the pause between reads while the api is away.
var opTransientPause = 500 * time.Millisecond

// transientAPIError is a failure a moment later may not have: the api
// unreachable, or the proxy in front of it answering 502/503/504 with
// its own page instead of the api's envelope.
func transientAPIError(err error) bool {
	var ue *unreachableError
	if errors.As(err, &ue) {
		return true
	}
	var ae *APIError
	if errors.As(err, &ae) && ae.Code == "internal" {
		switch ae.Status {
		case 502, 503, 504:
			return true
		}
	}
	return false
}

// readOp is one read of the op in a wait. With an api that long-polls
// (the previous read carried a version), it is held until something
// changes; otherwise it is a plain read, beside a read of the project's
// state when w wants one, so an older api's two reads cost one round
// trip, not two (I-236).
func readOp(ctx context.Context, c *Client, projectID, opID string, prev *Op, w *opWatch) (op *Op, held bool, err error) {
	if prev != nil && prev.Version != "" {
		return c.GetOpWait(ctx, projectID, opID, opWaitHold, prev.Version)
	}
	if w == nil || w.read == nil {
		op, err = c.GetOp(ctx, projectID, opID)
		return op, false, err
	}
	state := make(chan string, 1)
	go func() { state <- w.read() }()
	op, err = c.GetOp(ctx, projectID, opID)
	st := <-state
	if err == nil && op.ProjectState == "" {
		op.ProjectState = st
	}
	return op, false, err
}

func waitOpWith(ctx context.Context, c *Client, projectID, opID string, out io.Writer, w *opWatch) (*Op, error) {
	seq := 0
	streamDone := false
	streamTries := 0
	started := time.Now()
	deadline := started.Add(opPollTimeout)
	var prev *Op
	var failingSince time.Time
	for {
		asked := time.Now()
		op, held, err := readOp(ctx, c, projectID, opID, prev, w)
		if err != nil {
			if transientAPIError(err) && ctx.Err() == nil {
				if failingSince.IsZero() {
					failingSince = time.Now()
				}
				if time.Since(failingSince) < opTransientBudget {
					timingf("op wait: api away, retrying")
					if err := sleepOrDone(ctx, opTransientPause); err != nil {
						return nil, err
					}
					continue
				}
			}
			return nil, err
		}
		failingSince = time.Time{}
		if w != nil && w.show != nil {
			w.show(op.ProjectState)
		}
		if w != nil && w.phase != nil && op.State != "done" && op.State != "error" {
			w.phase(op.Phase)
		}
		if op.LogURL != "" && !streamDone && streamTries < 5 && op.State != "done" && op.State != "error" {
			streamTries++
			var state string
			var lastSeq int
			if w != nil && w.line != nil {
				state, lastSeq, err = streamBuildLines(ctx, c, projectID, opID, seq, w.line)
			} else {
				state, lastSeq, err = StreamBuildLog(ctx, c, projectID, opID, out, seq)
			}
			seq = lastSeq
			if err == nil && (state == "done" || state == "error") {
				streamDone = true
				// The op read before the stream has no result yet; the
				// error (code, message, fragment line) is on the op the
				// api wrote when the stream ended, so read it again
				// rather than returning the stale one with its state
				// flipped, which rendered every build failure as
				// "error:" and nothing (I-127).
				if fresh, err := c.GetOp(ctx, projectID, opID); err == nil {
					op = fresh
				}
				if state == "error" && op.State != "error" {
					op.State = "error"
				}
			}
			// A stream cut short (a proxy's idle timeout, a network blip)
			// resumes from the last line it printed on the next poll.
		}
		if op.State == "done" || op.State == "error" {
			return op, nil
		}
		if time.Now().After(deadline) {
			return nil, exitf(ExitGeneric, "The operation is still running after %s; `repose status` shows where it is.", opPollTimeout)
		}
		// An api that held the read (or would have: the op had already
		// changed) is asked again at once; one that did not (older, or
		// its bound on held reads reached) is polled as before. A held
		// read that came back at once with nothing new is not trusted
		// to hold the next one either.
		again := op.Version != "" && (prev == nil || prev.Version == "")
		if held && (op.Version != prev.Version || time.Since(asked) >= opWaitHold/2) {
			again = true
		}
		prev = op
		if again {
			continue
		}
		// The poll interval runs from one read's start to the next, as
		// I-187's budget counted it: at a laptop's 200 ms the reads
		// themselves no longer stretch it from 0.5 s to 0.9 s.
		if err := sleepOrDone(ctx, pollDelay(started)-time.Since(asked)); err != nil {
			return nil, err
		}
	}
}

func createProjectForRun(ctx context.Context, e *Env, remote string, opts RunOptions, pr *progress) (*Project, error) {
	name := opts.Name
	if name == "" && opts.Temp > 0 {
		name = tempName()
	}
	if name == "" {
		// No remote: the directory's name (the repository root's in a
		// repository), remembered in by_dir below (I-358).
		name = basenameFromRemote(remote, syncRoot(e.Cwd))
		if remote == "" {
			name = dirProjectName(name)
			if name == "" {
				return nil, exitf(ExitUsage, "This directory's name cannot be a project name. Pass --name NAME.")
			}
		}
	}
	class := opts.Size
	if class == "" {
		class = e.Cfg.DefaultClass
	}
	req := CreateProjectRequest{Name: name, RemoteURL: remote, Class: class, TZ: localTZ(), PersonalOptOut: opts.NoPersonal}
	if opts.Temp > 0 {
		// Found by nothing and cached nowhere (I-351): its name is how
		// every later command reaches it.
		req.RemoteURL, req.ExpiresIn = "", int64(opts.Temp/time.Second)
	}
	// config.toml's default_agent becomes the new project's own default,
	// which is what `run PROMPT` without --agent reads; an existing
	// project keeps the one it was created with (I-241).
	if isAgent(e.Cfg.DefaultAgent) {
		req.AgentDefault = e.Cfg.DefaultAgent
	}

	waited := map[string]bool{}
	for attempt := 1; attempt <= 10; attempt++ {
		p, err := e.Client.CreateProject(ctx, req)
		if err == nil {
			// The phase starts once the api has answered, so it names the
			// slug every later line and command uses, not the name as
			// typed ("teksafari.org" is created as teksafari-org); the
			// POST itself takes well under a second (I-191).
			pr.Phase("Creating "+p.Slug, createdLabel(p, class))
			if opts.Temp > 0 {
				return p, nil
			}
			deps := defaultResolveDeps()
			dir := ""
			if remote == "" && deps.RemoteFor(e.Cwd) == "" {
				// A --name project with no remote has nothing else to be
				// found by; one with a remote is found by it (I-152). A
				// second project for a checkout that has a remote (I-348)
				// is reached by name: by_dir would not be believed there.
				dir = dirKey(e.Cwd, deps)
			}
			rememberProject(&e.Cache, remote, dir, *p)
			if err := e.saveCache(); err != nil {
				return nil, err
			}
			return p, nil
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == "payment_required" {
			return nil, exitf(ExitPaymentRequired, "%s", paymentRequiredMessage(apiErr))
		}
		if errors.As(err, &apiErr) && apiErr.Code == "conflict" {
			// A name held by a project still being destroyed (`repose rm`
			// a moment ago, from another checkout or with no remote, so
			// resolve did not find it) is free in seconds: wait for it,
			// as startOver does, instead of making NAME-2 (I-407).
			if held, ferr := findByName(ctx, e.Client, req.Name); ferr == nil && held != nil && held.State == "destroying" && !waited[held.ID] {
				waited[held.ID] = true
				if err := waitDestroyed(ctx, e, held, pr); err != nil {
					return nil, err
				}
				forgetProject(&e.Cache, held.ID)
				attempt--
				continue
			}
			req.Name = fmt.Sprintf("%s-%d", name, attempt+1)
			pr.Fail()
			_, _ = fmt.Fprintf(e.ErrOut, "%q is taken; trying %q\n", name, req.Name)
			continue
		}
		return nil, err
	}
	return nil, exitf(ExitGeneric, "Could not find a free project name after 10 attempts; pass --name NAME.")
}

// runDestroyWait bounds how long `repose run` waits for the destroy of
// the project it resolved to before it creates a fresh one (I-301).
var runDestroyWait = 10 * time.Minute

// startOver is `repose run` on a project being destroyed (DECISIONS I-301):
// wait for the destroy to end, forget the old project, and leave res and
// opts so the run creates a fresh project under the same name, which is
// what `repose rm` then `repose run` is for. The name and remote stay
// taken until the destroy ends, so the create cannot go first.
func startOver(ctx context.Context, e *Env, res *ResolveResult, opts *RunOptions, pr *progress) error {
	old := *res.Project
	remote := res.Remote
	if remote == "" && old.RemoteURL != "" {
		// Named as PROJECT: only its own checkout can start it over,
		// since the fresh project takes this directory's remote.
		if remote = defaultResolveDeps().RemoteFor(e.Cwd); remote != old.RemoteURL {
			return exitf(ExitUsage, "%s is being destroyed. `repose run` in its checkout waits for that and creates a fresh %s; `repose restore %s` brings the old one back once it is gone.", old.Slug, old.Slug, old.Slug)
		}
	}
	if err := waitDestroyed(ctx, e, &old, pr); err != nil {
		return err
	}
	forgetProject(&e.Cache, old.ID)
	if err := e.saveCache(); err != nil {
		return err
	}
	if opts.Name == "" {
		opts.Name = old.Name
	}
	res.Project, res.Remote = nil, remote
	// The fresh project has a remote only when the old one did: a second
	// project for a checkout (I-348) stays reached by name.
	res.NoRemote = old.RemoteURL == ""
	return nil
}

// waitDestroyed waits, as a phase on pr, for old's destroy to end, up to
// runDestroyWait.
func waitDestroyed(ctx context.Context, e *Env, old *Project, pr *progress) error {
	pr.Phase(fmt.Sprintf("Waiting for the old %s to finish destroying", old.Slug), "Destroyed the old "+old.Slug)
	started := time.Now()
	for {
		p, err := e.Client.GetProject(ctx, old.ID)
		if isNotFound(err) || (err == nil && p.State == "destroyed") {
			break
		}
		if err != nil {
			return err
		}
		if p.State != "destroying" {
			// A failed destroy (error) or a project brought back: say
			// what it is instead of creating a second one beside it.
			pr.Fail()
			return notRunningError(p)
		}
		if time.Since(started) > runDestroyWait {
			pr.Fail()
			return exitf(ExitGeneric, "%s is still being destroyed after %s. `repose status %s` shows it; `repose run` again once it is gone.", old.Slug, fmtElapsed(runDestroyWait), old.Slug)
		}
		if err := sleepOrDone(ctx, pollDelay(started)); err != nil {
			return err
		}
	}
	pr.End()
	return nil
}

// forgetProject drops every cache entry that names the project id.
func forgetProject(cache *ProjectsCache, id string) {
	for k, c := range cache.ByRemote {
		if c.ProjectID == id {
			delete(cache.ByRemote, k)
		}
	}
	for k, v := range cache.ByDir {
		if v == id {
			delete(cache.ByDir, k)
		}
	}
}

// dirProjectName makes a directory's name a valid project name
// ([A-Za-z0-9._-]{1,64}): "job search" becomes "job-search" (I-358).
func dirProjectName(base string) string {
	var b strings.Builder
	dash := false
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-.")
	if len(s) > 64 {
		s = strings.TrimRight(s[:64], "-.")
	}
	return s
}

func basenameFromRemote(remote, cwd string) string {
	if remote != "" {
		return filepath.Base(remote)
	}
	return filepath.Base(cwd)
}

// localTZ returns the laptop's IANA zone name, or "" when it cannot be
// known, in which case the request omits tz and the api applies its
// default. Go names time.Local "Local" unless TZ is set, and the previous
// fallback sent the abbreviation ("EAT"), which the api rightly refuses as
// not an IANA name (M2 gate, DECISIONS I-104).
func localTZ() string {
	if z := os.Getenv("TZ"); z != "" && z != "Local" {
		if _, err := time.LoadLocation(strings.TrimPrefix(z, ":")); err == nil {
			return strings.TrimPrefix(z, ":")
		}
	}
	if z := time.Local.String(); z != "" && z != "Local" {
		if _, err := time.LoadLocation(z); err == nil {
			return z
		}
	}
	if z := tzFromLocaltime("/etc/localtime"); z != "" {
		return z
	}
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		if z := strings.TrimSpace(string(b)); z != "" {
			if _, err := time.LoadLocation(z); err == nil {
				return z
			}
		}
	}
	return ""
}

// tzFromLocaltime resolves a /etc/localtime symlink to the zone name after
// the zoneinfo directory ("/usr/share/zoneinfo/Europe/Paris" and macOS's
// "/var/db/timezone/zoneinfo/Europe/Paris" both give Europe/Paris).
func tzFromLocaltime(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	target = filepath.Clean(target)
	const marker = "/zoneinfo/"
	i := strings.LastIndex(target, marker)
	if i < 0 {
		return ""
	}
	z := target[i+len(marker):]
	if _, err := time.LoadLocation(z); err != nil {
		return ""
	}
	return z
}

// carryWithoutSync sends what the sync's apply would have carried, the
// tool logins, the git identity, the Claude files, the tools list and the
// zone, when a run leaves the checkout alone (`--no-sync`, a directory
// that is not a repository). Two ssh commands: the guest's markers, then
// only what changed. A failure is a warning; the run goes on (DECISIONS
// I-366).
//
// It returns the machine's checkout name, which the markers' ssh reads
// on the way (I-368), or nil when that ssh failed.
func (e *Env) carryWithoutSync(ctx context.Context, t sshTarget, project *Project, repoDir, tz string) *string {
	out, err := runSSH(ctx, t, checkoutVar(project.Slug, t.Checkout)+checkoutReport+markerScript()+credsMissingScript(), nil)
	if err != nil {
		e.warn("Could not copy your tool logins to the guest (%s).", oneLine(err.Error()))
		return nil
	}
	var checkout *string
	if name, ok := parseCheckout(string(out)); ok {
		checkout = &name
	}
	markers := parseMarkers(string(out))
	if strings.Contains(string(out), "#credsmissing") {
		delete(markers, credsMarker)
	}
	co := carryOptions{TZ: tz, Markers: markers, Tools: buildToolsCarry(e.HomeDir, repoDir, precedenceFor(e.personalOn, repoDir))}
	if repoDir != "" {
		gc, err := buildGitCarry(repoDir, e.HomeDir)
		if err != nil {
			e.warn("Could not read your git config (%s); the guest keeps its own.", oneLine(err.Error()))
		}
		if gc != nil {
			for _, n := range gc.Notes {
				e.warn("%s", n)
			}
		}
		co.Git = gc
	}
	if cc, _ := buildClaudeCarry(e.HomeDir); cc != nil {
		for _, n := range cc.Notes {
			e.warn("%s", n)
		}
		co.Claude = cc
	}
	mc, notes := buildMCPCarry(e.HomeDir, repoDir, project.Slug, t.Checkout, toolBinsOf(co.Tools)) // I-556
	for _, n := range notes {
		e.warn("%s", n)
	}
	co.MCP = mc
	skip, chosen := e.loginSkip(project.Slug)
	copied, carried, err := syncCredentialsAndCarry(ctx, t, e.HomeDir, repoDir, credSyncOptions{
		RemoteURL: project.RemoteURL,
		Skip:      skip,
		Kept: func(label string) {
			e.warn("Kept the guest's %s login: it is newer than the laptop's.", label)
		},
	}, co)
	if err != nil {
		e.warn("%s", oneLine(err.Error()))
		return checkout
	}
	if len(copied) > 0 {
		_, _ = fmt.Fprintf(e.Out, "Credentials: %s\n", strings.Join(copied, ", "))
		if l := loginsLine(copied, skip, chosen); l != "" {
			_, _ = fmt.Fprintln(e.Out, l)
		}
	}
	if carried != nil {
		for _, l := range carried.Lines() {
			_, _ = fmt.Fprintln(e.ErrOut, l)
		}
	}
	return checkout
}
