package guestd

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/vsockrpc"
)

// TestFakeServesTheContract is what a consumer of the fake (hostd's tests)
// relies on: every request answered, calls recorded, failures injectable, and
// notifications pushed.
func TestFakeServesTheContract(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := New()
	f.Signals = &hostdv1.GuestSignals{SshSessions: 2, TmuxClients: 1, GuestdOk: true}
	f.Procs = []*hostdv1.ProcSample{{Comm: "claude", CpuNsDelta: 1000, RssBytes: 2048}}

	path := filepath.Join(t.TempDir(), "guestd.sock")
	if _, err := f.Listen(ctx, path); err != nil {
		t.Fatalf("listen: %v", err)
	}

	conn, err := vsockrpc.DialUnix(path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	notifies := make(chan *guestdv1.Notify, 4)
	client := vsockrpc.NewClient(conn, func(n *guestdv1.Notify) { notifies <- n })
	defer client.Close() //nolint:errcheck // test cleanup

	reqs := []*guestdv1.Request{
		{Req: &guestdv1.Request_Ping{Ping: &guestdv1.Ping{}}},
		{Req: &guestdv1.Request_Freeze{Freeze: &guestdv1.Freeze{}}},
		{Req: &guestdv1.Request_Thaw{Thaw: &guestdv1.Thaw{}}},
		{Req: &guestdv1.Request_Switch{Switch: &guestdv1.Switch{SystemClosure: "/nix/store/x"}}},
		{Req: &guestdv1.Request_GrowFs{GrowFs: &guestdv1.GrowFs{}}},
		{Req: &guestdv1.Request_WriteSecrets{WriteSecrets: &guestdv1.WriteSecrets{}}},
		{Req: &guestdv1.Request_SetPrincipals{SetPrincipals: &guestdv1.SetPrincipals{}}},
		{Req: &guestdv1.Request_SetupProject{SetupProject: &guestdv1.SetupProject{ProjectSlug: "x"}}},
		{Req: &guestdv1.Request_Sample{Sample: &guestdv1.Sample{}}},
		{Req: &guestdv1.Request_Exec{Exec: &guestdv1.Exec{Argv: []string{"true"}}}},
		{Req: &guestdv1.Request_Shutdown{Shutdown: &guestdv1.Shutdown{}}},
	}
	for _, req := range reqs {
		resp, err := client.Do(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", Kind(req), err)
		}
		if !resp.GetOk() {
			t.Fatalf("%s: %+v", Kind(req), resp.GetError())
		}
	}

	want := []string{"ping", "freeze", "thaw", "switch", "grow_fs", "write_secrets",
		"set_principals", "setup_project", "sample", "exec", "shutdown"}
	got := f.Kinds()
	if len(got) != len(want) {
		t.Fatalf("recorded %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d = %q, want %q", i, got[i], want[i])
		}
	}
	if f.Frozen {
		t.Error("the fake is still frozen after a Thaw")
	}

	f.Ready()
	select {
	case n := <-notifies:
		if n.GetReady().GetBootId() != f.BootID {
			t.Errorf("boot_id = %q", n.GetReady().GetBootId())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no Ready arrived")
	}
}

func TestFakeCanBeToldToFailAnyRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := New()
	f.Fail["switch"] = &guestdv1.Error{Code: "internal", Message: "switch-to-configuration exited 1"}

	path := filepath.Join(t.TempDir(), "guestd.sock")
	if _, err := f.Listen(ctx, path); err != nil {
		t.Fatalf("listen: %v", err)
	}
	conn, err := vsockrpc.DialUnix(path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client := vsockrpc.NewClient(conn, nil)
	defer client.Close() //nolint:errcheck // test cleanup

	resp, err := client.Do(ctx, &guestdv1.Request{Req: &guestdv1.Request_Switch{Switch: &guestdv1.Switch{}}})
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if resp.GetOk() {
		t.Fatal("the injected failure did not take")
	}
	if resp.GetError().GetCode() != "internal" {
		t.Fatalf("code = %q", resp.GetError().GetCode())
	}

	// Other requests still succeed.
	if resp, err := client.Do(ctx, &guestdv1.Request{Req: &guestdv1.Request_Ping{Ping: &guestdv1.Ping{}}}); err != nil || !resp.GetOk() {
		t.Fatalf("ping after an injected switch failure: %v %+v", err, resp.GetError())
	}
}

// An ApplyMenu streams its lines as Notify frames ahead of the Response,
// which is what hostd's relay of them as BuildLog depends on
// (DECISIONS I-1460).
func TestFakeStreamsApplyMenuProgressBeforeTheResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := New()
	f.MenuLines = []string{"installing postgresql", "postgresql 16 ready on 127.0.0.1:5432"}
	path := filepath.Join(t.TempDir(), "guestd.sock")
	if _, err := f.Listen(ctx, path); err != nil {
		t.Fatal(err)
	}
	conn, err := vsockrpc.DialUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	client := vsockrpc.NewClient(conn, func(n *guestdv1.Notify) {
		if p := n.GetApplyMenuProgress(); p != nil {
			lines = append(lines, p.GetLine())
		}
	})
	defer client.Close() //nolint:errcheck // test cleanup
	resp, err := client.Do(ctx, &guestdv1.Request{Req: &guestdv1.Request_ApplyMenu{ApplyMenu: &guestdv1.ApplyMenu{
		Menu: &hostdv1.MenuSpec{Items: []*hostdv1.MenuItem{{Id: "postgresql"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetOk() || !resp.GetApplyMenu().GetDone() {
		t.Fatalf("response %+v", resp)
	}
	// The read loop delivers in order, so both lines are in before Do
	// returned.
	if len(lines) != 2 || lines[1] != f.MenuLines[1] {
		t.Fatalf("lines before the response = %q", lines)
	}

	f.MenuErrorCode = "menu_failed"
	resp, err = client.Do(ctx, &guestdv1.Request{Req: &guestdv1.Request_ApplyMenu{ApplyMenu: &guestdv1.ApplyMenu{}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetOk() || resp.GetError().GetCode() != "menu_failed" || resp.GetApplyMenu().GetErrorCode() != "menu_failed" {
		t.Fatalf("failed apply answered %+v", resp)
	}
}
