package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// `repose browser [PROJECT]` (DECISIONS I-292): one command that starts the
// machine's desktop viewer if needed, forwards its port to the laptop in
// the background, and opens the viewer page with the password in the URL
// fragment, so the user types nothing. The forward is an `ssh -N` child in
// its own session that outlives the CLI; its pid and port are kept under
// the config directory so a second `repose browser` reuses it and
// `--stop` can end it. `repose open --desktop` is the old name and does
// the same.

// BrowserOptions are `repose browser`'s flags.
type BrowserOptions struct {
	Stop   bool // stop the forward and the machine's viewer
	NoOpen bool // print the URL instead of opening a browser
}

// desktopIdleMinutes is the guest's viewer idle stop (nix/guest/base/
// desktop.nix), named in the one line the command prints.
const desktopIdleMinutes = 30

// desktopGuestPort is where the forward goes in the guest: the viewer's
// socket-activated entry point. A variable so a test can stand a local
// server in for it.
var desktopGuestPort = desktopPort

// browserForwardWait bounds how long the CLI waits for a new forward to
// answer the viewer's health path.
var browserForwardWait = 20 * time.Second

// BrowserCmd implements `repose browser [PROJECT] [--stop] [--no-open]`.
func BrowserCmd(ctx context.Context, e *Env, projectArg string, opts BrowserOptions) error {
	if opts.Stop {
		return stopBrowserCmd(ctx, e, projectArg)
	}
	project, err := requireRunningProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	// The certificate and config first: a forward from a laptop whose
	// certificate expired overnight must refresh it like run does.
	target, err := connect(ctx, e, project)
	if err != nil {
		return err
	}
	// The guest's own helper starts the socket-activated chain (Xvnc,
	// websockify, the agents' browser) and prints the password, which is
	// the boot's: the same at every start until the machine reboots, so a
	// link kept in a tab keeps working (I-292).
	out, err := runSSH(ctx, target, "repose-guest-profile desktop start", nil)
	if err != nil {
		return stepFailed("start the desktop on the machine", err, "")
	}
	pw := desktopPassword(out)
	if pw == "" {
		return stepFailed("read the desktop's password", errors.New("repose-guest-profile desktop start printed none"), "")
	}
	port, err := ensureBrowserForward(ctx, e, project, target)
	if err != nil {
		return err
	}
	u := browserURL(port, pw)
	_, _ = fmt.Fprintf(e.Out, "Watching %s's browser at %s (the view sleeps after %d idle minutes).\n", project.Slug, u, desktopIdleMinutes)
	if !opts.NoOpen {
		_ = openBrowser(u)
	}
	return nil
}

// stopBrowserCmd is `repose browser --stop`: the guest's viewer stops (the
// agents' browser keeps running for the agent) and the laptop's forward
// ends. A project that is not running has no viewer to stop; the forward
// is ended all the same.
func stopBrowserCmd(ctx context.Context, e *Env, projectArg string) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	if project.State == "running" {
		target, err := connect(ctx, e, project)
		if err != nil {
			return err
		}
		if _, err := runSSH(ctx, target, "repose-guest-profile desktop stop", nil); err != nil {
			return stepFailed("stop the desktop on the machine", err, "")
		}
	}
	if err := stopBrowserForward(e, project.Slug); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.Out, "Stopped watching %s's browser.\n", project.Slug)
	return nil
}

// browserURL is the viewer page's address on the laptop. The password
// rides in the fragment: a browser never sends the fragment with a
// request, so the forward, websockify and any log see only the path, and
// the page reads it to connect at once (I-292).
func browserURL(localPort int, password string) string {
	return fmt.Sprintf("http://localhost:%d/#p=%s", localPort, url.QueryEscape(password))
}

// browserForward is one laptop forward to a project's viewer, kept as a
// file so later invocations find it.
type browserForward struct {
	Port int `json:"port"`
	PID  int `json:"pid"`
}

func browserForwardPath(e *Env, slug string) string {
	return filepath.Join(e.Dir, "browser-forwards", slug+".json")
}

func readBrowserForward(e *Env, slug string) (browserForward, bool) {
	b, err := os.ReadFile(browserForwardPath(e, slug))
	if err != nil {
		return browserForward{}, false
	}
	var f browserForward
	if err := json.Unmarshal(b, &f); err != nil || f.Port == 0 {
		return browserForward{}, false
	}
	return f, true
}

func writeBrowserForward(e *Env, slug string, f browserForward) error {
	p := browserForwardPath(e, slug)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(f)
	return os.WriteFile(p, b, 0o600)
}

// viewerHealthy reports whether the laptop port answers with repose's
// viewer: its healthz file, which only the shipped page's web root has.
// A stale forward, another project's viewer or an unrelated server on the
// port all fail this. Connecting also starts the guest's chain through
// its socket activation, which is what the caller wants anyway.
func viewerHealthy(port int) bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return resp.StatusCode == http.StatusOK && strings.HasPrefix(string(body), "repose desktop viewer")
}

// viewerName is the project the viewer on a laptop port belongs to, from
// the project.json the guest writes into the viewer's web root at every
// start (nix/guest/base/desktop.nix, I-198); "" when the page does not
// say. Every guest's viewer answers /healthz with the same words, so the
// health check alone cannot tell one machine's viewer from another's:
// the owner's `repose browser job` reused a port that recruiting's
// forward had meanwhile taken, and the page refused job's password
// (DECISIONS I-1238).
func viewerName(port int) string {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/project.json", port))
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var v struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&v); err != nil {
		return ""
	}
	return v.Name
}

// viewerIsFor reports whether the viewer on the port is this project's:
// its page names the project, or does not say (a guest from before
// project.json). Another project's name means another machine's forward
// holds the port.
func viewerIsFor(port int, p *Project) bool {
	name := viewerName(port)
	return name == "" || name == p.Slug || name == p.Name
}

// ensureBrowserForward returns the laptop port that reaches the project's
// viewer: the live forward from an earlier invocation when it still
// answers for this project, else a new one on 6080 or the next free port
// (I-261). A recorded port where another project's viewer answers is
// that project's forward, left running; this project's record is
// forgotten and it gets a port of its own (I-1238).
func ensureBrowserForward(ctx context.Context, e *Env, p *Project, target sshTarget) (int, error) {
	if f, ok := readBrowserForward(e, p.Slug); ok {
		switch {
		case viewerHealthy(f.Port) && viewerIsFor(f.Port, p):
			return f.Port, nil
		case viewerHealthy(f.Port):
			if err := os.Remove(browserForwardPath(e, p.Slug)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return 0, err
			}
		default:
			// Dead or hijacked: end whatever is left and start over.
			_ = stopBrowserForward(e, p.Slug)
		}
	}
	port, err := pickLocalPort(e, desktopPort, laptopPortFree, freePort)
	if err != nil {
		return 0, err
	}
	pid, err := startBrowserForward(ctx, target, port, desktopGuestPort)
	if err != nil {
		return 0, err
	}
	if err := writeBrowserForward(e, p.Slug, browserForward{Port: port, PID: pid}); err != nil {
		return 0, err
	}
	return port, nil
}

// browserForwardArgs is the ssh invocation of the background forward: its
// own connection, never the shared ControlMaster (ownConnection, I-149),
// no remote command, exit when the local port cannot be bound, and
// keepalives so a forward whose laptop slept dies within about 45 seconds
// instead of lingering half-open.
func browserForwardArgs(target sshTarget, localPort, guestPort int) []string {
	args := append(ownConnection(),
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-L", openForwardSpec(localPort, guestListener{Port: guestPort, Host: "127.0.0.1"}),
	)
	return append(args, target.Args...)
}

// startBrowserForward starts the forward as a detached child and returns
// its pid once the viewer answers through it. It fails when ssh exits
// first (the port could not be bound, the connection was refused) or when
// nothing answers within browserForwardWait.
func startBrowserForward(ctx context.Context, target sshTarget, localPort, guestPort int) (int, error) {
	cmd := exec.Command("ssh", browserForwardArgs(target, localPort, guestPort)...)
	stderr, err := startDetached(cmd)
	if err != nil {
		return 0, stepFailed("start the forward to the desktop", err, "")
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.Now().Add(browserForwardWait)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return 0, ctx.Err()
		case err := <-exited:
			detail := sshStderrDetail(stderr.String())
			if err == nil {
				err = errors.New("ssh exited")
			}
			return 0, stepFailed("start the forward to the desktop", fmt.Errorf("%w%s", err, optionalDetail(detail)), "")
		case <-tick.C:
			if viewerHealthy(localPort) {
				return cmd.Process.Pid, nil
			}
			if time.Now().After(deadline) {
				_ = cmd.Process.Kill()
				return 0, stepFailed("reach the desktop through the forward", fmt.Errorf("nothing answered on the laptop's port %d within %s", localPort, browserForwardWait), "")
			}
		}
	}
}

func optionalDetail(detail string) string {
	if detail == "" {
		return ""
	}
	return " (" + detail + ")"
}

// stopBrowserForward ends the recorded forward, if any, and forgets it.
// The pid is only killed while the port still answers as our viewer, so a
// recycled pid after a reboot is left alone.
func stopBrowserForward(e *Env, slug string) error {
	f, ok := readBrowserForward(e, slug)
	if !ok {
		return nil
	}
	if f.PID > 0 && viewerHealthy(f.Port) {
		if p, err := os.FindProcess(f.PID); err == nil {
			_ = p.Kill()
		}
	}
	if err := os.Remove(browserForwardPath(e, slug)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
