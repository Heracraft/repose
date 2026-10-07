package guestd

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sample"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/obs"
	"github.com/heracraft/repose/internal/vsockrpc"
)

// testLog writes nowhere but fails the test on any line that breaks
// docs/workstreams/10-observability.md §5: a line with no event field, or a
// field name on the never-log list. Every guestd test runs through it, so the
// rules are checked by this workstream's own suite and not only by obslint.
func testLog(t *testing.T) *slog.Logger { return obs.NewTestLogger(t, obs.ComponentGuestd, io.Discard) }

// harness is a guestd serving a dev socket over a fake guest root, with a
// hostd-side client attached.
type harness struct {
	srv     *Server
	client  *vsockrpc.Client
	paths   sysdep.Paths
	runner  *sysdep.FakeRunner
	freezer *sysdep.FakeFreezer
	docker  *sysdep.FakeDocker
	// herdrSock, when a setup sets it, is the fake herdr's socket.
	herdrSock string

	mu       sync.Mutex
	notifies []*guestdv1.Notify
}

func (h *harness) seen() []*guestdv1.Notify {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*guestdv1.Notify(nil), h.notifies...)
}

// waitForNotify waits until pick returns true for some notification.
func (h *harness) waitForNotify(t *testing.T, what string, pick func(*guestdv1.Notify) bool) *guestdv1.Notify {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range h.seen() {
			if pick(n) {
				return n
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s notification arrived; saw %d", what, len(h.seen()))
	return nil
}

func newHarness(t *testing.T, setup ...func(*harness)) *harness {
	t.Helper()
	root := t.TempDir()
	// A minimal guest: /proc/mounts, a current system, a store closure.
	p := sysdep.Paths{Root: root}
	mustMkdir(t, p.Proc())
	mustWrite(t, p.Mounts(), "/dev/vda / ext4 rw 0 0\n")
	mustWrite(t, p.Uptime(), "1234.56 9876.54\n")
	mustMkdir(t, filepath.Join(root, "proc", "sys", "kernel", "random"))
	mustWrite(t, p.BootID(), "4f2a1c7e-0000-4000-8000-000000000001\n")

	closure := "/nix/store/aaaa-nixos-system"
	mustMkdir(t, filepath.Join(root, closure, "bin"))
	mustWrite(t, filepath.Join(root, closure, "bin", "switch-to-configuration"), "#!/bin/sh\n")
	mustMkdir(t, filepath.Join(root, "nix", "store", "kkkk-kernel"))
	mustWrite(t, filepath.Join(root, "nix", "store", "kkkk-kernel", "bzImage"), "k")
	mustWrite(t, filepath.Join(root, "nix", "store", "kkkk-kernel", "initrd"), "i")
	mustSymlink(t, filepath.Join(root, "nix", "store", "kkkk-kernel", "bzImage"), filepath.Join(root, closure, "kernel"))
	mustSymlink(t, filepath.Join(root, "nix", "store", "kkkk-kernel", "initrd"), filepath.Join(root, closure, "initrd"))
	mustMkdir(t, filepath.Join(root, "run"))
	mustSymlink(t, filepath.Join(root, closure), p.CurrentSystem())

	h := &harness{
		paths:   p,
		runner:  sysdep.NewFakeRunner(),
		freezer: &sysdep.FakeFreezer{},
		docker:  &sysdep.FakeDocker{Up: true, Containers: 1},
	}
	h.runner.Match["list-windows"] = sysdep.RunResult{}
	// dev's tmux socket: guestd forks tmux only when it exists.
	uid, _ := sysdep.DevIdentity()
	mustMkdir(t, filepath.Dir(p.TmuxSocket(uid)))
	mustWrite(t, p.TmuxSocket(uid), "")
	for _, f := range setup {
		f(h)
	}

	srv, err := New(Config{
		Root:           root,
		DevSocket:      filepath.Join(root, "run", "repose", "guestd.sock"),
		HookSocket:     filepath.Join(root, "run", "repose", "hooks.sock"),
		Log:            testLog(t),
		Runner:         h.runner,
		Freezer:        h.freezer,
		Docker:         h.docker,
		FreezeTimeout:  200 * time.Millisecond,
		SkipReadyProbe: true,
		HerdrSocket:    h.herdrSock,
		HerdrUID:       os.Getuid(),
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	h.srv = srv

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run did not return")
		}
	})

	h.client = h.dial(t)
	return h
}

func (h *harness) dial(t *testing.T) *vsockrpc.Client {
	t.Helper()
	path := h.srv.cfg.DevSocket
	var conn net.Conn
	deadline := time.Now().Add(10 * time.Second)
	for {
		var err error
		conn, err = vsockrpc.DialUnix(path)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	c := vsockrpc.NewClient(conn, func(n *guestdv1.Notify) {
		h.mu.Lock()
		h.notifies = append(h.notifies, n)
		h.mu.Unlock()
	})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func (h *harness) do(t *testing.T, req *guestdv1.Request) *guestdv1.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := h.client.Do(ctx, req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(link))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// TestEveryRequestHasAHandler walks the whole request set of
// docs/interfaces/vsock-guestd.md over a real connection. A request that is
// accepted but does nothing is the failure this table is written against.
func TestEveryRequestHasAHandler(t *testing.T) {
	h := newHarness(t)

	t.Run("Ping", func(t *testing.T) {
		resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_Ping{Ping: &guestdv1.Ping{}}})
		if !resp.GetOk() {
			t.Fatalf("error: %+v", resp.GetError())
		}
		ping := resp.GetPing()
		if ping.GetVersion() != ProtocolVersion {
			t.Errorf("version = %q, want %q", ping.GetVersion(), ProtocolVersion)
		}
		if ping.GetUptimeS() != 1234 {
			t.Errorf("uptime_s = %d, want 1234 from /proc/uptime", ping.GetUptimeS())
		}
		if ping.GetBootId() != "4f2a1c7e-0000-4000-8000-000000000001" {
			t.Errorf("boot_id = %q", ping.GetBootId())
		}
	})

	t.Run("Freeze and Thaw", func(t *testing.T) {
		if resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_Freeze{Freeze: &guestdv1.Freeze{}}}); !resp.GetOk() {
			t.Fatalf("freeze: %+v", resp.GetError())
		}
		if frozen, _, _ := h.freezer.Frozen(); !frozen {
			t.Fatal("Freeze did not freeze")
		}
		if resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_Thaw{Thaw: &guestdv1.Thaw{}}}); !resp.GetOk() {
			t.Fatalf("thaw: %+v", resp.GetError())
		}
		if frozen, _, _ := h.freezer.Frozen(); frozen {
			t.Fatal("Thaw did not thaw")
		}
	})

	t.Run("Switch", func(t *testing.T) {
		h.runner.Results["switch-to-configuration"] = sysdep.RunResult{Stdout: []byte("activating\n")}
		resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_Switch{
			Switch: &guestdv1.Switch{SystemClosure: "/nix/store/aaaa-nixos-system"},
		}})
		if !resp.GetOk() {
			t.Fatalf("error: %+v", resp.GetError())
		}
		if !strings.Contains(string(resp.GetSwitch().GetOutput()), "activating") {
			t.Fatalf("output = %q", resp.GetSwitch().GetOutput())
		}
	})

	t.Run("GrowFs", func(t *testing.T) {
		resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_GrowFs{GrowFs: &guestdv1.GrowFs{}}})
		if !resp.GetOk() {
			t.Fatalf("error: %+v", resp.GetError())
		}
		if resp.GetGrowFs().GetNewBytes() == 0 {
			t.Fatal("new_bytes was zero")
		}
	})

	t.Run("WriteSecrets", func(t *testing.T) {
		resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_WriteSecrets{
			WriteSecrets: &guestdv1.WriteSecrets{Secrets: []*guestdv1.Secret{
				{Name: "TOKEN", Value: []byte("v")},
			}},
		}})
		if !resp.GetOk() {
			t.Fatalf("error: %+v", resp.GetError())
		}
		if _, err := os.Stat(filepath.Join(h.paths.SecretsDir(), "TOKEN")); err != nil {
			t.Fatalf("the secret was not written: %v", err)
		}
	})

	t.Run("SetPrincipals", func(t *testing.T) {
		resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_SetPrincipals{
			SetPrincipals: &guestdv1.SetPrincipals{Principals: []string{"project-1"}},
		}})
		if !resp.GetOk() {
			t.Fatalf("error: %+v", resp.GetError())
		}
		b, err := os.ReadFile(h.paths.Principals())
		if err != nil || string(b) != "project-1\n" {
			t.Fatalf("principals = %q, err = %v", b, err)
		}
	})

	t.Run("SetupProject", func(t *testing.T) {
		resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_SetupProject{
			SetupProject: &guestdv1.SetupProject{ProjectSlug: "todo-app", Tz: "UTC", Lang: "C.UTF-8"},
		}})
		if !resp.GetOk() {
			t.Fatalf("error: %+v", resp.GetError())
		}
		if _, err := os.Stat(h.paths.ProjectJSON()); err != nil {
			t.Fatalf("project.json: %v", err)
		}
	})

	t.Run("Sample", func(t *testing.T) {
		resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_Sample{Sample: &guestdv1.Sample{}}})
		if !resp.GetOk() {
			t.Fatalf("error: %+v", resp.GetError())
		}
		if !resp.GetSample().GetSignals().GetGuestdOk() {
			t.Fatal("guestd_ok is false in guestd's own sample")
		}
	})

	t.Run("Exec", func(t *testing.T) {
		h.runner.Results["uptime"] = sysdep.RunResult{Stdout: []byte("up 1 day")}
		resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_Exec{
			Exec: &guestdv1.Exec{Argv: []string{"uptime"}, TimeoutS: 5},
		}})
		if !resp.GetOk() {
			t.Fatalf("error: %+v", resp.GetError())
		}
		if string(resp.GetExec().GetStdout()) != "up 1 day" {
			t.Fatalf("stdout = %q", resp.GetExec().GetStdout())
		}
	})

	t.Run("Shutdown", func(t *testing.T) {
		resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_Shutdown{
			Shutdown: &guestdv1.Shutdown{TimeoutS: 5},
		}})
		if !resp.GetOk() {
			t.Fatalf("error: %+v", resp.GetError())
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, ok := h.runner.Ran("systemctl poweroff"); ok {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("poweroff was not run; calls: %v", h.runner.Calls())
	})
}

func TestReadyIsSentOnEveryConnection(t *testing.T) {
	h := newHarness(t)
	h.waitForNotify(t, "Ready", func(n *guestdv1.Notify) bool { return n.GetReady() != nil })

	// A hostd restart: a second connection replaces the first and gets Ready
	// again without the guest rebooting.
	h.mu.Lock()
	h.notifies = nil
	h.mu.Unlock()
	h.client = h.dial(t)
	h.waitForNotify(t, "a second Ready", func(n *guestdv1.Notify) bool { return n.GetReady() != nil })
}

func TestASecondConnectionReplacesTheFirst(t *testing.T) {
	h := newHarness(t)
	first := h.client
	second := h.dial(t)

	select {
	case <-first.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the first connection was not closed when a second arrived")
	}
	resp := h.do2(t, second, &guestdv1.Request{Req: &guestdv1.Request_Ping{Ping: &guestdv1.Ping{}}})
	if !resp.GetOk() {
		t.Fatalf("the replacement connection does not work: %+v", resp.GetError())
	}
}

func (h *harness) do2(t *testing.T, c *vsockrpc.Client, req *guestdv1.Request) *guestdv1.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := c.Do(ctx, req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func TestFreezeWatchdogWarnsOverTheWire(t *testing.T) {
	h := newHarness(t)
	if resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_Freeze{Freeze: &guestdv1.Freeze{}}}); !resp.GetOk() {
		t.Fatalf("freeze: %+v", resp.GetError())
	}
	n := h.waitForNotify(t, "freeze_timeout", func(n *guestdv1.Notify) bool {
		return n.GetWarning().GetKind() == "freeze_timeout"
	})
	if n.GetWarning().GetDetail() == "" {
		t.Error("the warning carries no detail")
	}
	if frozen, _, _ := h.freezer.Frozen(); frozen {
		t.Fatal("the watchdog did not thaw")
	}
}

func TestHookRelaysAsAnAgentEvent(t *testing.T) {
	h := newHarness(t)
	postHook(t, h.srv.HookSocketPath(), `{"agent":"claude","kind":"completed","summary":"tests pass","window":"claude"}`)

	n := h.waitForNotify(t, "AgentEvent", func(n *guestdv1.Notify) bool { return n.GetAgentEvent() != nil })
	ev := n.GetAgentEvent()
	if ev.GetAgent() != "claude" || ev.GetKind() != "completed" || ev.GetSummary() != "tests pass" {
		t.Fatalf("event = %+v", ev)
	}
	if ev.GetTmuxWindow() != "claude" {
		t.Fatalf("tmux_window = %q", ev.GetTmuxWindow())
	}
}

// A hook from outside tmux (a herdr pane) is relayed under the agent's
// name and never marks the tmux window that happens to carry that name;
// the same hook from that window does.
func TestHookOutsideTmuxLeavesTmuxWindowsAlone(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		mustMkdir(t, filepath.Dir(h.paths.ProjectJSON()))
		mustWrite(t, h.paths.ProjectJSON(), `{"slug":"todo-app"}`)
		writeFakeProc(t, h.paths, 100, 1, "bash")
		writeFakeProc(t, h.paths, 101, 100, "claude")
		h.runner.Match["list-windows"] = sysdep.RunResult{Stdout: []byte("claude\t101\tclaude\t0\n")}
	})
	ctx := context.Background()
	state := func() string {
		t.Helper()
		h.srv.watcher.Refresh(ctx)
		sig, _ := h.srv.watcher.Signals()
		for _, a := range sig.GetAgents() {
			if a.GetTmuxWindow() == "claude" {
				return a.GetState()
			}
		}
		t.Fatalf("no claude window in %+v", sig.GetAgents())
		return ""
	}
	if got := state(); got == sample.StateNeedsInput {
		t.Fatalf("state before any hook = %q", got)
	}

	h.srv.onHook("claude", "", "needs_input", "Allow Bash?")
	n := h.waitForNotify(t, "AgentEvent", func(n *guestdv1.Notify) bool { return n.GetAgentEvent() != nil })
	if ev := n.GetAgentEvent(); ev.GetTmuxWindow() != "claude" || ev.GetKind() != "needs_input" {
		t.Fatalf("event = %+v, want it relayed under the agent's name", ev)
	}
	if got := state(); got == sample.StateNeedsInput {
		t.Fatalf("a hook with no tmux window set the tmux window claude to %q", got)
	}

	h.srv.onHook("claude", "claude", "needs_input", "Allow Bash?")
	if got := state(); got != sample.StateNeedsInput {
		t.Fatalf("a hook from the claude window left it %q, want needs_input", got)
	}
}

// writeFakeProc writes the /proc/<pid>/stat and status the watcher reads.
func writeFakeProc(t *testing.T, p sysdep.Paths, pid, ppid int, comm string) {
	t.Helper()
	dir := p.ProcPID(itoa(pid))
	mustMkdir(t, dir)
	fields := make([]string, 52)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = itoa(ppid)
	fields[10] = "10"
	mustWrite(t, filepath.Join(dir, "stat"), itoa(pid)+" ("+comm+") S "+strings.Join(fields, " ")+"\n")
	mustWrite(t, filepath.Join(dir, "status"), "Name:\t"+comm+"\nUid:\t1000\t1000\t1000\t1000\n")
}

func postHook(t *testing.T, socket, body string) {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial the hook socket: %v", err)
	}
	defer conn.Close() //nolint:errcheck // test
	req := "POST / HTTP/1.1\r\nHost: guestd\r\nContent-Type: application/json\r\n" +
		"Content-Length: " + itoa(len(body)) + "\r\nConnection: close\r\n\r\n" + body
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	resp, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(string(resp), "HTTP/1.1 204") {
		t.Fatalf("the hook socket answered:\n%s", resp)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestNotificationsAreDroppedAndCountedWithNoHostd(t *testing.T) {
	// No connection at all: the queue fills and the counter rises rather than
	// the guest blocking on a hostd that is not there.
	root := t.TempDir()
	srv, err := New(Config{
		Root:           root,
		DevSocket:      filepath.Join(root, "run", "repose", "guestd.sock"),
		HookSocket:     filepath.Join(root, "run", "repose", "hooks.sock"),
		Log:            testLog(t),
		Runner:         sysdep.NewFakeRunner(),
		Freezer:        &sysdep.FakeFreezer{},
		Docker:         &sysdep.FakeDocker{Up: true},
		SkipReadyProbe: true,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for i := 0; i < NotifyQueue+50; i++ {
		srv.Warn("disk_high", "test")
	}
	if got := srv.Dropped(); got != 50 {
		t.Fatalf("dropped = %d, want 50", got)
	}
}

func TestUnknownRequestIsRefusedByVersion(t *testing.T) {
	h := newHarness(t)
	// An empty oneof is what a newer hostd's unknown request looks like to
	// this build.
	resp := h.do(t, &guestdv1.Request{})
	if resp.GetOk() {
		t.Fatal("an unknown request was accepted")
	}
	if resp.GetError().GetCode() != sysdep.CodeInvalidArgument {
		t.Fatalf("code = %q, want invalid_argument", resp.GetError().GetCode())
	}
	if !strings.Contains(resp.GetError().GetMessage(), ProtocolVersion) {
		t.Fatalf("message = %q, want it to carry the protocol version so hostd can degrade per request",
			resp.GetError().GetMessage())
	}
}

func TestSwitchNeedsRebootOverTheWire(t *testing.T) {
	h := newHarness(t)
	root := h.paths.Root
	other := "/nix/store/bbbb-nixos-system"
	mustMkdir(t, filepath.Join(root, other, "bin"))
	mustWrite(t, filepath.Join(root, other, "bin", "switch-to-configuration"), "#!/bin/sh\n")
	mustMkdir(t, filepath.Join(root, "nix", "store", "jjjj-kernel"))
	mustWrite(t, filepath.Join(root, "nix", "store", "jjjj-kernel", "bzImage"), "k2")
	mustWrite(t, filepath.Join(root, "nix", "store", "jjjj-kernel", "initrd"), "i2")
	mustSymlink(t, filepath.Join(root, "nix", "store", "jjjj-kernel", "bzImage"), filepath.Join(root, other, "kernel"))
	mustSymlink(t, filepath.Join(root, "nix", "store", "jjjj-kernel", "initrd"), filepath.Join(root, other, "initrd"))

	resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_Switch{
		Switch: &guestdv1.Switch{SystemClosure: other},
	}})
	if !resp.GetOk() {
		t.Fatalf("error: %+v", resp.GetError())
	}
	if !resp.GetSwitch().GetNeedsReboot() {
		t.Fatal("needs_reboot was not set for a kernel change")
	}
}
