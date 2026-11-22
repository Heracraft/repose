package guestd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/vsockrpc"
)

// erofsGuest makes the harness's root an erofs guest: the mountinfo the
// initramfs leaves, and the volume's mount point.
func erofsGuest(h *harness) {
	if err := sysdep.WriteMountinfo(h.paths, sysdep.FixtureMountinfoErofs); err != nil {
		panic(err)
	}
	ep := h.paths
	ep.Erofs = true
	mustMkdirNoT(ep.RWMount())
}

func mustMkdirNoT(path string) {
	if err := mkdirAll(path); err != nil {
		panic(err)
	}
}

func nixGuest(h *harness) {
	if err := sysdep.WriteMountinfo(h.paths, sysdep.FixtureMountinfoNix); err != nil {
		panic(err)
	}
}

func TestTheRuntimeComesFromMountinfo(t *testing.T) {
	if rt := newHarness(t, erofsGuest).srv.Runtime(); rt != sysdep.RuntimeErofs {
		t.Fatalf("erofs mountinfo: runtime %s", rt)
	}
	if rt := newHarness(t, nixGuest).srv.Runtime(); rt != sysdep.RuntimeNix {
		t.Fatalf("nix mountinfo: runtime %s", rt)
	}
	// A guest with no mountinfo at all is the Nix guest it always was.
	if rt := newHarness(t).srv.Runtime(); rt != sysdep.RuntimeNix {
		t.Fatalf("no mountinfo: runtime %s", rt)
	}
}

func TestFreezeTargetsTheVolumeOnAnErofsGuest(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(*harness)
		want  func(root string) string
	}{
		{"erofs", erofsGuest, func(root string) string { return filepath.Join(root, "run", "repose", "rw") }},
		{"nix", nixGuest, func(root string) string { return root }},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.setup)
			for _, req := range []*guestdv1.Request{
				{Req: &guestdv1.Request_Freeze{Freeze: &guestdv1.Freeze{}}},
				{Req: &guestdv1.Request_Thaw{Thaw: &guestdv1.Thaw{}}},
			} {
				if resp := h.do(t, req); !resp.GetOk() {
					t.Fatalf("%+v", resp.GetError())
				}
			}
			want := []string{"freeze " + c.want(h.paths.Root), "thaw " + c.want(h.paths.Root)}
			got := h.freezer.Paths()
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("freezer calls %q, want %q", got, want)
			}
			t.Logf("%s: %q", c.name, got)
		})
	}
}

func TestGrowFsResizesTheVolumeDeviceOnAnErofsGuest(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(*harness)
		want  string
	}{
		{"erofs", erofsGuest, "resize2fs /dev/vdb"},
		{"nix", nixGuest, "resize2fs /dev/vda"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.setup)
			resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_GrowFs{GrowFs: &guestdv1.GrowFs{}}})
			if !resp.GetOk() {
				t.Fatalf("%+v", resp.GetError())
			}
			call, ok := h.runner.Ran("resize2fs")
			if !ok || strings.Join(call.Argv, " ") != c.want {
				t.Fatalf("argv %v, want %s", call.Argv, c.want)
			}
			t.Logf("%s: %v", c.name, call.Argv)
		})
	}
}

// Sample's root figures are the volume's on an erofs guest: with no
// /run/repose/rw they are zero (statfs fails), with it they are not.
func TestSampleStatsTheVolumeOnAnErofsGuest(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		if err := sysdep.WriteMountinfo(h.paths, sysdep.FixtureMountinfoErofs); err != nil {
			t.Fatal(err)
		}
	})
	sample := func() *guestdv1.SampleResult {
		resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_Sample{Sample: &guestdv1.Sample{}}})
		if !resp.GetOk() {
			t.Fatalf("%+v", resp.GetError())
		}
		return resp.GetSample()
	}
	if s := sample(); s.GetRootSizeBytes() != 0 {
		t.Fatalf("root_size_bytes %d with no /run/repose/rw: statfs read another path", s.GetRootSizeBytes())
	}
	mustMkdir(t, filepath.Join(h.paths.Root, "run", "repose", "rw"))
	s := sample()
	if s.GetRootSizeBytes() == 0 || s.GetRootUsedBytes() == 0 {
		t.Fatalf("root figures %d/%d with /run/repose/rw present", s.GetRootUsedBytes(), s.GetRootSizeBytes())
	}
	t.Logf("statfs of /run/repose/rw: used %d of %d bytes", s.GetRootUsedBytes(), s.GetRootSizeBytes())
}

func TestSetupProjectOnAnErofsGuestWaitsOnNoNixUnit(t *testing.T) {
	h := newHarness(t, erofsGuest)
	start := time.Now()
	resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_SetupProject{SetupProject: &guestdv1.SetupProject{
		ProjectSlug: "shop", RemoteUrl: "github.com/acme/shop", Tz: "UTC",
		ProjectJson: []byte(`{"project_id":"p_1","slug":"shop","multiplexer":"tmux"}`),
	}}})
	took := time.Since(start)
	if !resp.GetOk() {
		t.Fatalf("%+v", resp.GetError())
	}
	for _, c := range h.runner.Calls() {
		j := strings.Join(c.Argv, " ")
		if strings.Contains(j, "repose-paths") || strings.Contains(j, "home-manager") || strings.Contains(j, "nix-store") {
			t.Fatalf("SetupProject touched a Nix unit: %s", j)
		}
	}
	if _, ok := h.runner.Ran("systemctl"); !ok {
		t.Fatal("the session unit was not started")
	}
	if d := h.srv.deadlineFor(&guestdv1.Request{Req: &guestdv1.Request_SetupProject{}}); d != DefaultDeadline {
		t.Fatalf("erofs SetupProject deadline %s, want %s", d, DefaultDeadline)
	}
	if took > time.Second {
		t.Fatalf("SetupProject took %s on an idle erofs guest", took)
	}
	t.Logf("SetupProject on an erofs guest: %s, %d commands", took, len(h.runner.Calls()))
}

func TestNixOnlyRequestsAreUnimplementedOnAnErofsGuest(t *testing.T) {
	h := newHarness(t, erofsGuest)
	for name, req := range map[string]*guestdv1.Request{
		"Switch":        {Req: &guestdv1.Request_Switch{Switch: &guestdv1.Switch{SystemClosure: "/nix/store/aaaa-nixos-system"}}},
		"RegisterPaths": {Req: &guestdv1.Request_RegisterPaths{RegisterPaths: &guestdv1.RegisterPaths{Registration: []byte("x")}}},
	} {
		start := time.Now()
		resp := h.do(t, req)
		if resp.GetOk() || resp.GetError().GetCode() != sysdep.CodeUnimplemented {
			t.Fatalf("%s: %+v", name, resp)
		}
		if took := time.Since(start); took > time.Second {
			t.Fatalf("%s waited %s", name, took)
		}
	}
	if n := len(h.runner.Calls()); n != 0 {
		t.Fatalf("ran %d commands: %v", n, h.runner.Calls())
	}

	nix := newHarness(t, nixGuest)
	resp := nix.do(t, &guestdv1.Request{Req: &guestdv1.Request_ApplyMenu{ApplyMenu: &guestdv1.ApplyMenu{}}})
	if resp.GetOk() || resp.GetError().GetCode() != sysdep.CodeUnimplemented || resp.GetApplyMenu().GetErrorCode() != sysdep.CodeUnimplemented || !resp.GetApplyMenu().GetDone() {
		t.Fatalf("ApplyMenu on a Nix guest: %+v", resp)
	}
}

// Over the wire: every progress line is a Notify carrying the
// ApplyMenu's request_id, and every one is ahead of the Response.
func TestApplyMenuStreamsProgressKeyedByRequestID(t *testing.T) {
	h := newHarness(t, erofsGuest, func(h *harness) {
		// bun is not installed yet.
		h.runner.Match["mise where"] = sysdep.RunResult{ExitCode: 1}
	})
	conn, err := vsockrpc.DialUnix(h.srv.cfg.DevSocket)
	if err != nil {
		t.Fatal(err)
	}
	c := vsockrpc.NewConn(conn)
	defer c.Close() //nolint:errcheck // test cleanup
	if err := c.Send(&guestdv1.Envelope{RequestId: "r-menu-1", Body: &guestdv1.Envelope_Request{Request: &guestdv1.Request{
		Req: &guestdv1.Request_ApplyMenu{ApplyMenu: &guestdv1.ApplyMenu{Menu: &hostdv1.MenuSpec{Items: []*hostdv1.MenuItem{{Id: "bun"}, {Package: "jq"}}}}},
	}}}); err != nil {
		t.Fatal(err)
	}
	var lines []string
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		env, err := c.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if n := env.GetNotify(); n != nil {
			if p := n.GetApplyMenuProgress(); p != nil {
				if env.GetRequestId() != "r-menu-1" {
					t.Fatalf("progress line with request_id %q", env.GetRequestId())
				}
				lines = append(lines, p.GetLine())
			}
			continue
		}
		resp := env.GetResponse()
		if resp == nil || env.GetRequestId() != "r-menu-1" {
			continue
		}
		if !resp.GetOk() || !resp.GetApplyMenu().GetDone() {
			t.Fatalf("response %+v", resp)
		}
		break
	}
	want := []string{"installing bun", "bun: installing bun@1 with mise", "bun ready", "installing jq", "jq: updating package lists", "jq installed", "menu applied"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines before the response %q, want %q", lines, want)
	}
}

func mkdirAll(path string) error { return os.MkdirAll(path, 0o755) }

// Every command guestd runs on an erofs guest gets Ubuntu's whole PATH,
// sbin included (resize2fs, fsfreeze, runuser live there); a Nix guest
// keeps the PATH it had.
func TestCommandsGetTheRuntimesPATH(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(*harness)
		want  string
	}{
		{"erofs", erofsGuest, "PATH=" + sysdep.ErofsPATH},
		{"nix", nixGuest, "PATH=" + sysdep.GuestPATH},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.setup)
			for _, req := range []*guestdv1.Request{
				{Req: &guestdv1.Request_Exec{Exec: &guestdv1.Exec{Argv: []string{"fsfreeze", "--help"}}}},
				{Req: &guestdv1.Request_Exec{Exec: &guestdv1.Exec{Argv: []string{"id"}, AsUser: "dev"}}},
				{Req: &guestdv1.Request_GrowFs{GrowFs: &guestdv1.GrowFs{}}},
			} {
				if resp := h.do(t, req); !resp.GetOk() {
					t.Fatalf("%+v", resp.GetError())
				}
			}
			calls := h.runner.Calls()
			if len(calls) < 3 {
				t.Fatalf("%d calls", len(calls))
			}
			for _, call := range calls {
				found := false
				for _, kv := range call.Env {
					if kv == c.want {
						found = true
					}
				}
				if !found {
					t.Fatalf("%v ran with env %v, want %s", call.Argv, call.Env, c.want)
				}
			}
		})
	}
}
