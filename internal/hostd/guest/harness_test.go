package guest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/ch"
	fakeguestd "github.com/heracraft/repose/internal/hostd/fakeguestd"
	"github.com/heracraft/repose/internal/hostd/gcroot"
	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/metrics"
	"github.com/heracraft/repose/internal/hostd/mount"
	hnet "github.com/heracraft/repose/internal/hostd/net"
	"github.com/heracraft/repose/internal/hostd/nixbuild"
	"github.com/heracraft/repose/internal/hostd/snapshot"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/storeview"
	"github.com/heracraft/repose/internal/hostd/systemd"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
	"github.com/heracraft/repose/internal/obs"
)

type recorder struct {
	mu      sync.Mutex
	results []*hostdv1.Result
	events  []*hostdv1.Event
	samples []*hostdv1.Samples
	logs    []string
	logSeq  map[string][]string // command id -> "seq line"
}

func (r *recorder) Result(res *hostdv1.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, res)
}
func (r *recorder) Event(ev *hostdv1.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}
func (r *recorder) GuestEvent(ev *hostdv1.Event) bool {
	r.Event(ev)
	return true
}
func (r *recorder) Samples(s *hostdv1.Samples) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples = append(r.samples, s)
}
func (r *recorder) BuildLog(commandID string, seq uint64, line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, line)
	if r.logSeq == nil {
		r.logSeq = map[string][]string{}
	}
	r.logSeq[commandID] = append(r.logSeq[commandID], fmt.Sprintf("%d %s", seq, line))
}
func (r *recorder) states(guestID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		if s := e.GetGuestStateChanged(); s != nil && s.GuestId == guestID {
			out = append(out, s.State)
		}
	}
	return out
}
func (r *recorder) warnings() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		if w := e.GetHostWarning(); w != nil {
			out = append(out, w.Kind)
		}
	}
	return out
}

type harness struct {
	metrics *metrics.M
	t       *testing.T
	m       *Manager
	st      *state.DB
	lvm     *lvm.Fake
	mount   *mount.Fake
	view    *storeview.Fake
	net     *hnet.Fake
	sd      *systemd.Fake
	chc     *ch.Fake
	nix     *nixbuild.Fake
	blob    *snapshot.MemBlob
	rec     *recorder
	roots   gcroot.Roots
	closure string
	sockDir string
	mu      sync.Mutex
	guestds map[string]*fakeguestd.Server
	gopts   fakeguestd.Options
	cgWait  atomic.Uint64 // every guest unit's cpu.pressure some total
	noBoot  map[string]bool
	cfg     Config
}

const (
	gid1 = "0192f0a1-1111-7000-8000-000000000001"
	gid2 = "0192f0a2-2222-7000-8000-000000000002"
)

func fakeClosure(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	c := filepath.Join(dir, name)
	if err := os.MkdirAll(c, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "bzImage"), []byte("k"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "initrd.img"), []byte("i"), 0o644)
	_ = os.Symlink(filepath.Join(dir, "bzImage"), filepath.Join(c, "kernel"))
	_ = os.Symlink(filepath.Join(dir, "initrd.img"), filepath.Join(c, "initrd"))
	_ = os.WriteFile(filepath.Join(c, "init"), []byte("#!"), 0o755)
	_ = os.WriteFile(filepath.Join(c, "kernel-params"), []byte("loglevel=4\n"), 0o644)
	return c
}

func newHarness(t *testing.T, mut func(*Config)) *harness {
	t.Helper()
	h := &harness{t: t, guestds: map[string]*fakeguestd.Server{}, noBoot: map[string]bool{}}
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	h.st = st
	h.lvm = lvm.NewFake()
	h.mount = mount.NewFake()
	h.view = storeview.NewFake()
	h.net = hnet.NewFake()
	h.sd = systemd.NewFake()
	h.chc = &ch.Fake{}
	h.closure = fakeClosure(t, "nixos-system-v1")
	h.nix = &nixbuild.Fake{Closure: h.closure, ClosureBytes: 1 << 30}
	h.blob = snapshot.NewMemBlob()
	h.rec = &recorder{}
	h.roots = gcroot.Roots{Dir: filepath.Join(t.TempDir(), "gcroots")}
	h.sockDir = t.TempDir()
	h.gopts = fakeguestd.Options{FreezeWatchdog: time.Second}
	// A guest@ unit starting means the guest boots: a fake guestd appears.
	h.sd.OnRun = func(unit string, argv []string) error {
		if !strings.HasPrefix(unit, "guest@") {
			return nil
		}
		id := strings.TrimPrefix(unit, "guest@")
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.noBoot[id] {
			return nil
		}
		if old := h.guestds[id]; old != nil {
			_ = old.Close()
		}
		srv, err := fakeguestd.Listen(filepath.Join(h.sockDir, id+".sock"), h.gopts)
		if err != nil {
			return err
		}
		h.guestds[id] = srv
		srv.OnShutdown(func() { h.sd.Exit(unit, 0) })
		return nil
	}
	h.sd.OnStop = func(unit string) {
		if !strings.HasPrefix(unit, "guest@") {
			return
		}
		id := strings.TrimPrefix(unit, "guest@")
		h.mu.Lock()
		defer h.mu.Unlock()
		if old := h.guestds[id]; old != nil {
			_ = old.Close()
			delete(h.guestds, id)
		}
	}
	cfg := Config{
		HostID: "host-1", GuestsDir: filepath.Join(t.TempDir(), "guests"), UsersDir: filepath.Join(t.TempDir(), "users"), GuestCIDR: "10.64.4.0/22",
		TotalMemBytes: 64 << 30, HostReserveBytes: 8 << 30, ReadyTimeout: 3 * time.Second,
		GuestdRetry: 30 * time.Millisecond, GuestdLostAfter: 300 * time.Millisecond, UnitPoll: 30 * time.Millisecond,
		// The hostd and virtiofsd accounts do not exist on a dev box; the
		// chowns then target the test's own ids, which is allowed unprivileged.
		Lookup: func(string) (int, int, error) { return os.Getuid(), os.Getgid(), nil },
	}
	if mut != nil {
		mut(&cfg)
	}
	h.cfg = cfg
	// The strict test logger: every line the manager writes during these
	// tests must name an event and carry no never-log field
	// (docs/workstreams/10-observability.md §5).
	logger := obs.NewTestLogger(t, obs.ComponentHostd, io.Discard)
	h.metrics = metrics.New()
	m, err := New(cfg, Deps{
		State: st, LVM: h.lvm, Mount: h.mount, View: h.view, Net: h.net, Systemd: h.sd, CH: h.chc, Nix: h.nix, Roots: h.roots, Blob: h.blob,
		Stream: &snapshot.FakeStreamer{LVM: h.lvm}, Emit: h.rec, Metrics: h.metrics, Log: logger,
		Guestd:  vsockclient.UnixDialer{Path: func(tg vsockclient.Target) string { return filepath.Join(h.sockDir, tg.GuestID+".sock") }},
		MemInfo: func() (uint64, uint64, error) { return 64 << 30, 40 << 30, nil },
		Load1:   func() float64 { return 0.5 },
		CgroupCPUPressure: func(cg string) (uint64, error) {
			if cg == "" {
				return 0, errors.New("no cgroup")
			}
			return h.cgWait.Load(), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Run()
	h.m = m
	t.Cleanup(func() {
		m.Close()
		h.mu.Lock()
		for _, s := range h.guestds {
			_ = s.Close()
		}
		h.mu.Unlock()
		_ = st.Close()
	})
	return h
}

func (h *harness) guestd(id string) *fakeguestd.Server {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.guestds[id]
}

var cmdSeq int

func cmd(c any) *hostdv1.Command {
	cmdSeq++
	out := &hostdv1.Command{CommandId: fmt.Sprintf("cmd-%04d", cmdSeq)}
	switch v := c.(type) {
	case *hostdv1.CreateGuest:
		out.Cmd = &hostdv1.Command_CreateGuest{CreateGuest: v}
	case *hostdv1.StartGuest:
		out.Cmd = &hostdv1.Command_StartGuest{StartGuest: v}
	case *hostdv1.StopGuest:
		out.Cmd = &hostdv1.Command_StopGuest{StopGuest: v}
	case *hostdv1.DestroyGuest:
		out.Cmd = &hostdv1.Command_DestroyGuest{DestroyGuest: v}
	case *hostdv1.ResizeVolume:
		out.Cmd = &hostdv1.Command_ResizeVolume{ResizeVolume: v}
	case *hostdv1.Build:
		out.Cmd = &hostdv1.Command_Build{Build: v}
	case *hostdv1.ApplyConfig:
		out.Cmd = &hostdv1.Command_ApplyConfig{ApplyConfig: v}
	case *hostdv1.Snapshot:
		out.Cmd = &hostdv1.Command_Snapshot{Snapshot: v}
	case *hostdv1.Restore:
		out.Cmd = &hostdv1.Command_Restore{Restore: v}
	case *hostdv1.UpdateSecrets:
		out.Cmd = &hostdv1.Command_UpdateSecrets{UpdateSecrets: v}
	case *hostdv1.SetPrincipals:
		out.Cmd = &hostdv1.Command_SetPrincipals{SetPrincipals: v}
	case *hostdv1.Exec:
		out.Cmd = &hostdv1.Command_Exec{Exec: v}
	case *hostdv1.Drain:
		out.Cmd = &hostdv1.Command_Drain{Drain: v}
	case *hostdv1.AnswerQuestion:
		out.Cmd = &hostdv1.Command_AnswerQuestion{AnswerQuestion: v}
	}
	return out
}

func createReq(id string) *hostdv1.CreateGuest {
	return &hostdv1.CreateGuest{
		ProjectId: "proj-" + id[:8], GuestId: id, Class: "large", VolumeBytes: 40 << 30, SystemClosure: "",
		Secrets: []*hostdv1.Secret{{Name: "API_KEY", Value: []byte("s3cret")}}, Env: map[string]string{"TZ": "Europe/Paris", "LANG": "C.UTF-8"},
		SshCaPub: "ssh-ed25519 AAAA ca", Principals: []string{"proj-" + id[:8]}, HostKey: []byte("hostkey"), HostCert: []byte("hostcert"),
		UserId: "user-1", ProjectSlug: "todo-app", RemoteUrl: "git@github.com:a/b.git",
	}
}

func (h *harness) run(c *hostdv1.Command) *hostdv1.Result {
	h.t.Helper()
	res := h.m.Execute(context.Background(), c)
	if res == nil {
		h.t.Fatalf("command %s ignored", c.CommandId)
	}
	return res
}

func (h *harness) mustOK(c *hostdv1.Command) *hostdv1.Result {
	h.t.Helper()
	res := h.run(c)
	if !res.Ok {
		h.t.Fatalf("%s failed: %s: %s", Kind(c), res.Error.Code, res.Error.Message)
	}
	return res
}

func (h *harness) mustFail(c *hostdv1.Command, code string) *hostdv1.Result {
	h.t.Helper()
	res := h.run(c)
	if res.Ok {
		h.t.Fatalf("%s succeeded, expected %s", Kind(c), code)
	}
	if res.Error.Code != code {
		h.t.Fatalf("%s: code %s (%s), expected %s", Kind(c), res.Error.Code, res.Error.Message, code)
	}
	return res
}

func (h *harness) create(id string) *hostdv1.Result {
	h.t.Helper()
	req := createReq(id)
	req.SystemClosure = h.closure
	return h.mustOK(cmd(req))
}

func (h *harness) guest(id string) *state.Guest {
	h.t.Helper()
	g, err := h.st.GetGuest(id)
	if err != nil {
		h.t.Fatalf("guest %s: %v", id, err)
	}
	return g
}

func (h *harness) waitState(id, want string) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if g, err := h.st.GetGuest(id); err == nil && g.State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	g, _ := h.st.GetGuest(id)
	h.t.Fatalf("guest %s never reached %s (now %v)", id, want, g)
}

func clone(c *hostdv1.Command) *hostdv1.Command { return proto.Clone(c).(*hostdv1.Command) }
