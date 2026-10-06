package hostd

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

type apiStub struct {
	hostdv1.UnimplementedHostServiceServer
	mu      sync.Mutex
	results map[string]*hostdv1.Result
	events  []*hostdv1.Event
	samples int
	logs    int
	send    chan *hostdv1.ApiMessage
}

func (a *apiStub) Session(srv hostdv1.HostService_SessionServer) error {
	go func() {
		for m := range a.send {
			_ = srv.Send(m)
		}
	}()
	for {
		msg, err := srv.Recv()
		if err != nil {
			return err
		}
		a.mu.Lock()
		switch m := msg.Msg.(type) {
		case *hostdv1.HostMessage_Result:
			a.results[m.Result.CommandId] = m.Result
		case *hostdv1.HostMessage_Event:
			a.events = append(a.events, m.Event)
		case *hostdv1.HostMessage_Samples:
			a.samples++
		case *hostdv1.HostMessage_Log:
			a.logs++
		}
		a.mu.Unlock()
	}
}

func TestFakeHostAgainstApiStub(t *testing.T) {
	ln := bufconn.Listen(1 << 20)
	stub := &apiStub{results: map[string]*hostdv1.Result{}, send: make(chan *hostdv1.ApiMessage, 16)}
	gs := grpc.NewServer()
	hostdv1.RegisterHostServiceServer(gs, stub)
	go func() { _ = gs.Serve(ln) }()
	defer gs.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	f := New(Options{SampleInterval: 30 * time.Millisecond, Heartbeat: 30 * time.Millisecond, Fail: map[string]string{"Snapshot": "internal"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx, conn) }()
	cmd := func(id string, c any) {
		out := &hostdv1.Command{CommandId: id}
		switch v := c.(type) {
		case *hostdv1.CreateGuest:
			out.Cmd = &hostdv1.Command_CreateGuest{CreateGuest: v}
		case *hostdv1.Build:
			out.Cmd = &hostdv1.Command_Build{Build: v}
		case *hostdv1.Snapshot:
			out.Cmd = &hostdv1.Command_Snapshot{Snapshot: v}
		case *hostdv1.StopGuest:
			out.Cmd = &hostdv1.Command_StopGuest{StopGuest: v}
		}
		stub.send <- &hostdv1.ApiMessage{Msg: &hostdv1.ApiMessage_Command{Command: out}}
	}
	wait := func(id string) *hostdv1.Result {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			stub.mu.Lock()
			r := stub.results[id]
			stub.mu.Unlock()
			if r != nil {
				return r
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("no result for %s", id)
		return nil
	}
	cmd("c1", &hostdv1.CreateGuest{GuestId: "g1", ProjectId: "p1", Class: "large", SystemClosure: "/nix/store/x"})
	r := wait("c1")
	if !r.Ok || r.GetCreate().GuestIp == "" {
		t.Fatalf("create %v", r)
	}
	cmd("c1", &hostdv1.CreateGuest{GuestId: "g1", ProjectId: "p1", Class: "large"})
	time.Sleep(50 * time.Millisecond)
	if len(f.Guests()) != 1 {
		t.Fatal("repeated command_id must not create twice")
	}
	cmd("c2", &hostdv1.Build{ProjectId: "p1", RevisionId: "r1"})
	if r := wait("c2"); !r.Ok || r.GetBuild().SystemClosure == "" {
		t.Fatalf("build %v", r)
	}
	cmd("c3", &hostdv1.Snapshot{GuestId: "g1"})
	if r := wait("c3"); r.Ok || r.Error.Code != "internal" {
		t.Fatalf("configured failure not applied: %v", r)
	}
	cmd("c4", &hostdv1.StopGuest{GuestId: "g1", SnapshotFirst: true})
	if r := wait("c4"); !r.Ok || r.GetStop().BlobPath == "" {
		t.Fatalf("stop %v", r)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stub.mu.Lock()
		ok := stub.samples >= 2 && stub.logs == 3 && len(stub.events) >= 5
		stub.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("samples=%d logs=%d events=%d", stub.samples, stub.logs, len(stub.events))
}

// TestProjectJSONPassesThrough: the fake keeps the project_json each
// CreateGuest and StartGuest delivered, so api tests can read the
// `multiplexer` the guest's next start gets (I-502, I-503); a StartGuest
// without one keeps the last.
func TestProjectJSONPassesThrough(t *testing.T) {
	f := New(Options{FakeClosure: "/nix/store/fake"})
	gid := "0192aaaa-0000-7000-8000-000000000001"
	r := f.Execute(&hostdv1.Command{CommandId: "c1", Cmd: &hostdv1.Command_CreateGuest{CreateGuest: &hostdv1.CreateGuest{
		GuestId: gid, ProjectId: "p1", Class: "small", ProjectJson: []byte(`{"slug":"a","multiplexer":"tmux"}`)}}})
	if !r.Ok {
		t.Fatalf("create: %v", r.Error)
	}
	if g := f.Guests()[0]; string(g.ProjectJSON) != `{"slug":"a","multiplexer":"tmux"}` {
		t.Fatalf("after create: %s", g.ProjectJSON)
	}
	f.Execute(&hostdv1.Command{CommandId: "c2", Cmd: &hostdv1.Command_StopGuest{StopGuest: &hostdv1.StopGuest{GuestId: gid}}})
	f.Execute(&hostdv1.Command{CommandId: "c3", Cmd: &hostdv1.Command_StartGuest{StartGuest: &hostdv1.StartGuest{GuestId: gid, ProjectJson: []byte(`{"slug":"a","multiplexer":"herdr"}`)}}})
	if g := f.Guests()[0]; string(g.ProjectJSON) != `{"slug":"a","multiplexer":"herdr"}` {
		t.Fatalf("after start: %s", g.ProjectJSON)
	}
	f.Execute(&hostdv1.Command{CommandId: "c4", Cmd: &hostdv1.Command_StopGuest{StopGuest: &hostdv1.StopGuest{GuestId: gid}}})
	f.Execute(&hostdv1.Command{CommandId: "c5", Cmd: &hostdv1.Command_StartGuest{StartGuest: &hostdv1.StartGuest{GuestId: gid}}})
	if g := f.Guests()[0]; string(g.ProjectJSON) != `{"slug":"a","multiplexer":"herdr"}` {
		t.Fatalf("a bare start lost it: %s", g.ProjectJSON)
	}
}
