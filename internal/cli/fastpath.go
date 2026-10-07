package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/multiplexer"
)

// The connect fast path (DECISIONS I-223). connect's slow path reads the
// account and every project from the api, reuses or re-issues the
// certificate, rewrites ~/.ssh/repose and proves the connection with
// `ssh <slug>.repose true`: two api round trips and one ssh round trip
// before the command's first real ssh, on every run and attach, although
// on most of them nothing changed since the last. When the files on disk
// already cover the project, none of the api calls can change anything,
// and when the multiplexed connection of an earlier command is still up
// (ControlPersist) and runs a command, it is the proof: the gateway ends a
// client connection as soon as its guest connection ends, so a master
// that answers means a guest that answered on it.

// noFastPath turns the fast path off (REPOSE_NO_FASTPATH=1), for
// measuring it against the slow one and as a way out if it misjudges.
func noFastPath() bool { return os.Getenv(envNoFastPath) == "1" }

// sshFilesCover reports whether ~/.ssh/repose (sd) already lets the CLI
// reach p: a certificate for the CLI's key with p's id among its
// principals and certReuseMargin of validity left, the known_hosts file,
// ~/.ssh/repose/config as this binary writes it, and a Host block for p's
// slug in ~/.ssh/repose/hosts. handle is the
// account handle the block names.
func sshFilesCover(sd string, p *Project, now time.Time) (handle string, ok bool) {
	if p == nil || p.ID == "" || p.Slug == "" {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(sd, "known_hosts")); err != nil {
		return "", false
	}
	pub, err := ensureReposeKey()
	if err != nil {
		return "", false
	}
	cert := parseCertFile(filepath.Join(sd, reposeKeyName+"-cert.pub"))
	if !certIsFor(cert, pub) || !certUsableFor(cert, []string{p.ID}, now, certReuseMargin) {
		return "", false
	}
	if !sshEntryCurrent(sd) {
		// ~/.ssh/repose/config from before I-281 (the Host blocks
		// themselves), or naming a repose binary that has moved: the slow
		// path writes both files.
		return "", false
	}
	cfg, err := os.ReadFile(filepath.Join(sd, sshHostsName))
	if err != nil {
		return "", false
	}
	if strings.Contains(string(cfg), "ForwardAgent yes") {
		// Written by a CLI before I-247: the slow path rewrites the whole
		// file, so the upgrade drops agent forwarding on its first command.
		return "", false
	}
	return hostBlockHandle(string(cfg), p.Slug)
}

// hostBlockHandle finds `Host <slug>.repose` in a config renderSSHConfig
// wrote and returns the handle from its `User <slug>.<handle>` line.
func hostBlockHandle(cfg, slug string) (string, bool) {
	in := false
	for _, l := range strings.Split(cfg, "\n") {
		t := strings.TrimSpace(l)
		if k, v, ok := strings.Cut(t, " "); ok && strings.EqualFold(k, "Host") {
			in = v == slug+".repose"
			continue
		}
		if !in {
			continue
		}
		if k, v, ok := strings.Cut(t, " "); ok && strings.EqualFold(k, "User") {
			if h, ok := strings.CutPrefix(v, slug+"."); ok && h != "" {
				return h, true
			}
			return "", false
		}
	}
	return "", false
}

// masterProbeTimeout bounds masterAlive's round trip over a master. A
// healthy one answers in two round trips; one whose connection died while
// the laptop slept or changed networks never does.
const masterProbeTimeout = 2 * time.Second

// masterAlive reports whether a ControlPersist master for t is up and
// still reaches its guest. `ssh -O check` asks only the master's local
// socket, and a master whose TCP connection died while the laptop slept
// or changed networks still answers it: a session opened on it hung until
// ssh's keepalives gave up, up to 90 seconds, after "Connected" was
// printed (I-491). So a master that answers the check also runs `true`
// within masterProbeTimeout, and one that does not is told to stop and
// reported as down, for the caller's cold path. Stop, not exit: sessions
// already on the master keep running, so a slow but healthy master does
// not cut another terminal's attach.
func masterAlive(ctx context.Context, t sshTarget) bool {
	if goos() == "windows" {
		return false
	}
	if !sshControl(ctx, t, "check") {
		return false
	}
	pctx, cancel := context.WithTimeout(ctx, masterProbeTimeout)
	defer cancel()
	started := time.Now()
	cmd := exec.CommandContext(pctx, "ssh", append(append([]string{}, t.Args...), "true")...)
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err == nil {
		timingf("connect: ssh master answered in %dms", time.Since(started).Milliseconds())
		return true
	}
	timingf("connect: ssh master did not answer in %dms; stopped it", time.Since(started).Milliseconds())
	sshControl(ctx, t, "stop")
	return false
}

// sshControl sends a control command (`ssh -O <op>`) to t's master,
// which answers locally, and reports whether it succeeded.
func sshControl(ctx context.Context, t sshTarget, op string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	args := append([]string{"-O", op}, t.Args...)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.WaitDelay = time.Second
	return cmd.Run() == nil
}

// connectFast is connect when ~/.ssh/repose already covers the project:
// no api call, and no ssh at all when a master is up. ok is false when
// the slow path must run (the files do not cover the project, the alias
// does not resolve and the slow path's warning is due, tests, Windows).
func connectFast(ctx context.Context, e *Env, project *Project) (target sshTarget, ok bool, err error) {
	if e.TargetFor != nil || noFastPath() {
		return sshTarget{}, false, nil
	}
	sd, err := sshDir()
	if err != nil {
		return sshTarget{}, false, nil
	}
	handle, covered := sshFilesCover(sd, project, time.Now())
	if !covered {
		return sshTarget{}, false, nil
	}
	if resolves, _ := aliasResolves(project.Slug, handle); !resolves {
		return sshTarget{}, false, nil
	}
	target = e.target(project.Slug)
	if masterAlive(ctx, target) {
		timingf("connect: files cover the project, ssh master up")
		return target, true, nil
	}
	timingf("connect: files cover the project, no ssh master")
	err = waitForSSH(ctx, target, certRefusalHandler(func() error {
		// A refusal of the certificate on disk (revoked, a rotated CA):
		// the slow path's forced re-issue, which needs the account.
		me, err := e.Client.GetMe(ctx)
		if err != nil {
			return err
		}
		projects, err := e.Client.ListProjects(ctx)
		if err != nil {
			return err
		}
		_, err = ensureCert(ctx, e.Client, certParams{Handle: me.Handle, Projects: projects, Force: true, CheckAlias: true}, nil)
		return err
	}))
	if err != nil {
		return sshTarget{}, true, err
	}
	return target, true, nil
}

// cachedGuess is the project this command will most likely resolve to,
// from the projects cache alone (no api call): the explicit PROJECT when
// the cache knows its id, else the cached project for this checkout's
// remote, unless the directory is pinned to another one. nil when the
// cache cannot say.
func cachedGuess(e *Env, explicit string, deps resolveDeps) *Project {
	// Another checkout of a machine (I-480) takes the slow path: the
	// guess would be the machine's own checkout, and an early probe there
	// would read and make the wrong tree.
	if strings.Contains(explicit, ":") || (explicit == "" && e.extraCheckout() != nil) {
		return nil
	}
	if explicit != "" {
		for remote, c := range e.Cache.ByRemote {
			if c.Slug == explicit || c.ProjectID == explicit {
				return &Project{ID: c.ProjectID, Slug: c.Slug, RemoteURL: remote}
			}
		}
		return nil
	}
	remote := deps.RemoteFor(e.Cwd)
	if remote == "" {
		return nil
	}
	c, ok := e.Cache.ByRemote[remote]
	if !ok || c.ProjectID == "" || c.Slug == "" {
		return nil
	}
	for _, k := range uniqueStrings(dirKey(e.Cwd, deps), e.Cwd) {
		if id, ok := e.Cache.ByDir[k]; ok && id != c.ProjectID {
			return nil
		}
	}
	return &Project{ID: c.ProjectID, Slug: c.Slug, RemoteURL: remote}
}

// warmTarget returns the ssh target for guess when the files on disk
// cover it (covered), and whether a master for it is up (master): the
// guest answers on a connection that is still open.
func warmTarget(ctx context.Context, e *Env, guess *Project) (t sshTarget, covered, master bool) {
	if guess == nil || e.TargetFor != nil || noFastPath() {
		return sshTarget{}, false, false
	}
	sd, err := sshDir()
	if err != nil {
		return sshTarget{}, false, false
	}
	handle, covered := sshFilesCover(sd, guess, time.Now())
	if !covered {
		return sshTarget{}, false, false
	}
	if resolves, _ := aliasResolves(guess.Slug, handle); !resolves {
		return sshTarget{}, false, false
	}
	t = e.target(guess.Slug)
	return t, true, masterAlive(ctx, t)
}

// attachFast is `repose attach` with no api call (I-223): the cache names
// the project and a master that answers proves its guest is running, which is
// everything the attach needs. done is false when the full path must
// run; it then has done nothing.
func attachFast(ctx context.Context, e *Env, explicit string, bridge bool) (done bool, err error) {
	deps := defaultResolveDeps()
	guess := cachedGuess(e, explicit, deps)
	target, _, master := warmTarget(ctx, e, guess)
	if !master {
		return false, nil
	}
	timingf("attach: cached project, ssh master up; no api call")
	_, _ = fmt.Fprintf(e.Out, "Connected to %s\n", guess.Slug)
	tz := laptopTZ()
	helper := fastAttachHelper(e, guess, target, tz, explicit, bridge)
	if helper.RepoDir != "" {
		e.addReposeRemote(ctx, guess, target, nil) // I-272
	}
	// What runs now, from the guest: no api call here either (I-509),
	// and the sidebar reconcile takes this project alone (herdrSyncFor).
	mux, err := muxFor(ctx, target, guess)
	if err != nil {
		return true, err
	}
	helper.Multiplexer = mux.Name()
	release := e.herdrSyncFor(ctx, guess, mux.Name() == multiplexer.Herdr)
	defer release()
	defer e.keepTokenFresh()()
	// The cache keeps no expiry: a temporary machine is never cached
	// (I-351), so the guess is never one.
	return true, mux.Attach(e, attachReq{Target: target, Project: guess, TZ: tz, RepoDir: helper.RepoDir, Renew: renewFor(e, guess), Release: release, Helper: helper})
}

// fastAttachHelper is the session helper's options for attachFast: the
// same as the full path's, --bridge included (I-305).
func fastAttachHelper(e *Env, guess *Project, target sshTarget, tz, explicit string, bridge bool) sessionOptions {
	helper := sessionOptions{Slug: guess.Slug, Target: target.Args, TZ: tz, HomeDir: e.HomeDir, Forward: os.Getenv(forwardEnvOff) != "1", Carry: true, Bridge: bridge, Checkout: target.Checkout}
	if skip, _, _ := e.Cfg.loginSkip(guess.Slug); skip[mcpLogin] {
		helper.MCPOff = true // I-556
	}
	helper.MCP = e.Cfg.mcpForward(guess.Slug) // I-557
	if root := gitRepoRoot(e.Cwd); root != "" && explicit == "" {
		// Guessed from this checkout's remote: the checkout is the
		// project's own, whose git config the carry takes.
		helper.RepoDir = root
	}
	return helper
}

// earlyProbe is the sync's probe, started before the api has answered
// (I-223): when the cache names the project and ~/.ssh/repose covers it,
// the probe (a read of the guest's checkout, and the creation of an
// empty one, which the sync would do anyway) runs beside resolve's read
// of the project instead of after it. With no master up (cold), the
// probe's own connection becomes the master, so the ssh handshake also
// overlaps the api call. The result is used only when the project
// resolves to the guessed one and was running all along; settle decides.
type earlyProbe struct {
	id, slug string
	target   sshTarget
	cold     bool // no master was up: the probe made the connection
	done     chan struct{}
	out      []byte
	err      error
	started  time.Time // when the ssh began
	usable   bool      // set by settle
	// boot is a probe started when the guest's start finished (I-237):
	// it is this guest's, whatever the project was before the command.
	boot bool
	// noProbe is a boot connection made with `true` (run --no-sync): it
	// proves the connection but has no checkout to report.
	noProbe bool
}

func startEarlyProbe(ctx context.Context, e *Env, opts RunOptions) *earlyProbe {
	// Outside a repository nothing syncs, and the probe would make a
	// checkout the machine should not have (I-358, I-368).
	root := gitRepoRoot(e.Cwd)
	if opts.NoSync || root == "" {
		return nil
	}
	guess := cachedGuess(e, e.resolveArg(opts.ProjectArg), defaultResolveDeps())
	target, covered, master := warmTarget(ctx, e, guess)
	if !covered {
		return nil
	}
	if master {
		timingf("run: cached project, ssh master up; probe started beside the api")
	} else {
		timingf("run: cached project, no ssh master; probe (and master) started beside the api")
	}
	ep := &earlyProbe{id: guess.ID, slug: guess.Slug, target: target, cold: !master, done: make(chan struct{})}
	ep.started = time.Now()
	go func() {
		defer close(ep.done)
		ep.out, ep.err = runSSH(ctx, target, probeScript(guess.Slug, checkoutName(root), ""), nil)
	}()
	return ep
}

// gatewayRouteTTL is how long the gateway keeps a project's route answer
// (internal/gateway RouteTTL): a connection it refused because the guest
// was stopped is refused again, from its cache, for that long.
const gatewayRouteTTL = 5 * time.Second

// routeTTLMargin is added to gatewayRouteTTL for the difference between
// two connections' handshakes (the first one also resolved the name).
const routeTTLMargin = 300 * time.Millisecond

// startBootProbe makes the command's first connection to p's guest the
// moment the op that started it has finished (I-237), beside the
// project read that follows, instead of after it and the certificate
// check as `ssh true`: with the sync on, the connection runs the sync's
// probe, so the handshake and the probe are one ssh. The gateway refuses
// a connection to a guest that is not running yet and caches the refusal
// for gatewayRouteTTL, so nothing is dialled before the op says the guest
// is up, and after an early probe the gateway refused, not before the
// refusal it cached has expired: the refusal was cached when that probe
// reached authentication, a fixed number of round trips after it began,
// and the new connection reaches authentication after as many, so it
// starts gatewayRouteTTL (and a margin) after the refused one started. Whatever was there before (an early probe refused because the
// guest was stopped, a master of a guest that restarted) is closed
// first, so the new connection is never a stale master's session.
func startBootProbe(ctx context.Context, e *Env, p *Project, withProbe bool) {
	var target sshTarget
	if e.TargetFor != nil {
		target = e.TargetFor(p.Slug)
	} else {
		if noFastPath() {
			return
		}
		sd, err := sshDir()
		if err != nil {
			return
		}
		handle, covered := sshFilesCover(sd, p, time.Now())
		if !covered {
			return // connect's slow path issues the certificate first
		}
		if resolves, _ := aliasResolves(p.Slug, handle); !resolves {
			return
		}
		target = e.target(p.Slug)
	}
	var notBefore time.Time
	if old := e.early; old != nil {
		select {
		case <-old.done:
		case <-time.After(3 * time.Second):
			return // still dialling: settle closes it, and connect dials as before
		}
		if old.err != nil && old.slug == p.Slug {
			notBefore = old.started.Add(gatewayRouteTTL + routeTTLMargin)
		}
		old.usable = false
	}
	closeMaster(ctx, e, p.Slug)
	ep := &earlyProbe{id: p.ID, slug: p.Slug, target: target, cold: true, boot: true, usable: true, noProbe: !withProbe, done: make(chan struct{})}
	script := "true"
	if withProbe {
		script = probeScript(p.Slug, checkoutName(gitRepoRoot(e.Cwd)), "")
	}
	timingf("run: guest up; first connection started beside the project read")
	go func() {
		defer close(ep.done)
		if d := time.Until(notBefore); d > 0 {
			timingf("run: the gateway refused this project's early probe; waiting %dms out its route cache", d.Milliseconds())
			if sleepOrDone(ctx, d) != nil {
				ep.err = ctx.Err()
				return
			}
		}
		ep.out, ep.err = runSSH(ctx, target, script, nil)
	}()
	e.early = ep
}

// settle decides, once the api has answered, whether the probe stands in
// for the sync's own: the project is the guessed one and its guest was
// running before this command (a start makes a new guest). A cold probe
// that will not be used is waited for and its master closed: against a
// stopped guest, the gateway's refusal would otherwise keep a master
// that leads nowhere for the next ssh of this command to ride.
func (ep *earlyProbe) settle(ctx context.Context, e *Env, p *Project, wasRunning bool) {
	if ep == nil {
		return
	}
	ep.usable = p != nil && p.ID == ep.id && (wasRunning || ep.boot)
	if !ep.usable && ep.cold {
		<-ep.done
		closeMaster(ctx, e, ep.slug)
	}
}

// forProject is the probe's result as SyncOptions.Probe, or nil when
// settle found it cannot stand in for the sync's own.
func (ep *earlyProbe) forProject() func() ([]byte, error) {
	if ep == nil || !ep.usable || ep.noProbe {
		return nil
	}
	return func() ([]byte, error) {
		<-ep.done
		return ep.out, ep.err
	}
}

// connected is connectFast's use of a cold probe for project: once it
// has answered, its connection is the command's master and no `ssh true`
// is needed. ok is false when there was no such probe or it failed; a
// failed one's master is closed, and connect proves the connection the
// usual way.
func (ep *earlyProbe) connected(ctx context.Context, e *Env, project *Project) (sshTarget, bool) {
	if ep == nil || !ep.usable || !ep.cold || project == nil || project.ID != ep.id {
		return sshTarget{}, false
	}
	<-ep.done
	if ep.err != nil {
		closeMaster(ctx, e, ep.slug)
		return sshTarget{}, false
	}
	if ep.boot {
		timingf("connect: the connection made when the guest came up is the master")
	} else {
		timingf("connect: the early probe's connection is the master")
	}
	return ep.target, true
}
