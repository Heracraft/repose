package stream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// apiStub is an in-process api: it records what the host sent, sends
// commands, acks events, and can drop the stream.
type apiStub struct {
	hostdv1.UnimplementedHostServiceServer
	mu       sync.Mutex
	hellos   int
	results  []*hostdv1.Result
	samples  []*hostdv1.Samples
	events   []*hostdv1.Event
	logs     []*hostdv1.BuildLog
	hbs      int
	order    []string // "hb" and "result", as received
	toSend   chan *hostdv1.ApiMessage
	dropNext chan struct{}
	sessions chan struct{}
}

func newStub() *apiStub {
	return &apiStub{toSend: make(chan *hostdv1.ApiMessage, 16), dropNext: make(chan struct{}, 1), sessions: make(chan struct{}, 16)}
}

func (a *apiStub) Session(srv hostdv1.HostService_SessionServer) error {
	a.sessions <- struct{}{}
	ctx, cancel := context.WithCancel(srv.Context())
	defer cancel()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-a.toSend:
				_ = srv.Send(m) // a failed send ends the session through Recv below
			case <-a.dropNext:
				cancel()
				return
			}
		}
	}()
	for {
		msg, err := srv.Recv()
		if err != nil {
			return err
		}
		a.mu.Lock()
		switch m := msg.Msg.(type) {
		case *hostdv1.HostMessage_Hello:
			a.hellos++
		case *hostdv1.HostMessage_Result:
			a.results = append(a.results, m.Result)
			a.order = append(a.order, "result")
		case *hostdv1.HostMessage_Samples:
			a.samples = append(a.samples, m.Samples)
		case *hostdv1.HostMessage_Event:
			a.events = append(a.events, m.Event)
			a.toSend <- &hostdv1.ApiMessage{Msg: &hostdv1.ApiMessage_Ack{Ack: &hostdv1.Ack{EventId: m.Event.EventId}}}
		case *hostdv1.HostMessage_Log:
			a.logs = append(a.logs, m.Log)
		case *hostdv1.HostMessage_Heartbeat:
			a.hbs++
			a.order = append(a.order, "hb")
		}
		a.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

type hostStub struct {
	mu   sync.Mutex
	cmds []*hostdv1.Command
	s    *Stream
}

func (h *hostStub) Hello() *hostdv1.Hello         { return &hostdv1.Hello{HostId: "h1"} }
func (h *hostStub) Heartbeat() *hostdv1.Heartbeat { return &hostdv1.Heartbeat{Draining: false} }
func (h *hostStub) Dispatch(cmd *hostdv1.Command) {
	h.mu.Lock()
	h.cmds = append(h.cmds, cmd)
	h.mu.Unlock()
	h.s.Result(&hostdv1.Result{CommandId: cmd.CommandId, Ok: true})
}

type bufDialer struct{ ln *bufconn.Listener }

func (b bufDialer) Dial(ctx context.Context) (hostdv1.HostService_SessionClient, func(), error) {
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return b.ln.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	s, err := hostdv1.NewHostServiceClient(conn).Session(ctx)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return s, func() { _ = conn.Close() }, nil
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSessionCommandsResultsEventsAndReconnect(t *testing.T) {
	ln := bufconn.Listen(1 << 20)
	stub := newStub()
	gs := grpc.NewServer()
	hostdv1.RegisterHostServiceServer(gs, stub)
	go func() { _ = gs.Serve(ln) }()
	defer gs.Stop()

	host := &hostStub{}
	s := New(Config{HeartbeatInterval: 50 * time.Millisecond, SampleBuffer: 3, BackoffBase: 20 * time.Millisecond, MaxBackoff: 50 * time.Millisecond}, bufDialer{ln}, host, nil, nil)
	host.s = s
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	<-stub.sessions
	waitFor(t, "hello", func() bool { stub.mu.Lock(); defer stub.mu.Unlock(); return stub.hellos == 1 })
	stub.toSend <- &hostdv1.ApiMessage{Msg: &hostdv1.ApiMessage_Command{Command: &hostdv1.Command{CommandId: "c1", Cmd: &hostdv1.Command_Drain{Drain: &hostdv1.Drain{}}}}}
	waitFor(t, "result", func() bool {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		return len(stub.results) == 1 && stub.results[0].CommandId == "c1"
	})
	waitFor(t, "heartbeat", func() bool { stub.mu.Lock(); defer stub.mu.Unlock(); return stub.hbs >= 1 })

	s.Event(&hostdv1.Event{EventId: "e1", Ev: &hostdv1.Event_HostWarning{HostWarning: &hostdv1.HostWarning{Kind: "pool_high"}}})
	waitFor(t, "event acked", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.events) == 0 })

	// Outage: drop the stream, buffer samples and an event, then reconnect.
	stub.dropNext <- struct{}{}
	waitFor(t, "disconnect", func() bool { return !s.Connected() })
	for i := 0; i < 5; i++ {
		s.Samples(&hostdv1.Samples{Ts: int64(i)})
	}
	if s.Dropped() != 2 {
		t.Fatalf("expected 2 dropped samples past the buffer, got %d", s.Dropped())
	}
	s.Event(&hostdv1.Event{EventId: "e2", Ev: &hostdv1.Event_HostWarning{HostWarning: &hostdv1.HostWarning{Kind: "store_high"}}})
	<-stub.sessions
	waitFor(t, "second hello", func() bool { stub.mu.Lock(); defer stub.mu.Unlock(); return stub.hellos == 2 })
	waitFor(t, "buffered samples", func() bool {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		return len(stub.samples) == 3 && stub.samples[0].Ts == 2 && stub.samples[2].Ts == 4
	})
	waitFor(t, "event after reconnect", func() bool {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		for _, e := range stub.events {
			if e.EventId == "e2" {
				return true
			}
		}
		return false
	})
	waitFor(t, "e2 acked", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.events) == 0 })
	// An event must not be delivered twice after being acked on a prior session.
	stub.mu.Lock()
	e1 := 0
	for _, e := range stub.events {
		if e.EventId == "e1" {
			e1++
		}
	}
	stub.mu.Unlock()
	if e1 != 1 {
		t.Fatalf("e1 delivered %d times", e1)
	}
	// Reconnect on request (certificate rotation).
	s.Reconnect()
	<-stub.sessions
	waitFor(t, "third hello", func() bool { stub.mu.Lock(); defer stub.mu.Unlock(); return stub.hellos == 3 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestBackoffSchedule(t *testing.T) {
	s := New(Config{}, nil, nil, nil, nil)
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30}
	for i, w := range want {
		d := s.backoff(i)
		if d < w*time.Second || d > w*time.Second+w*time.Second/4+time.Millisecond {
			t.Fatalf("attempt %d: %v not within %vs plus jitter", i, d, w)
		}
	}
	if !errors.Is(context.Canceled, context.Canceled) {
		t.Fatal("unreachable")
	}
}

// TestHeartbeatAheadOfEveryResult: each result is preceded by a fresh
// heartbeat, so the api reads the memory a stop or destroy freed before it
// places the next guest, not up to HeartbeatInterval later (I-409).
func TestHeartbeatAheadOfEveryResult(t *testing.T) {
	ln := bufconn.Listen(1 << 20)
	stub := newStub()
	gs := grpc.NewServer()
	hostdv1.RegisterHostServiceServer(gs, stub)
	go func() { _ = gs.Serve(ln) }()
	defer gs.Stop()
	host := &hostStub{}
	s := New(Config{HeartbeatInterval: time.Hour}, bufDialer{ln}, host, nil, nil)
	host.s = s
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	<-stub.sessions
	waitFor(t, "hello", func() bool { stub.mu.Lock(); defer stub.mu.Unlock(); return stub.hellos == 1 })
	for _, id := range []string{"c1", "c2"} {
		stub.toSend <- &hostdv1.ApiMessage{Msg: &hostdv1.ApiMessage_Command{Command: &hostdv1.Command{CommandId: id, Cmd: &hostdv1.Command_Drain{Drain: &hostdv1.Drain{}}}}}
	}
	waitFor(t, "two results", func() bool { stub.mu.Lock(); defer stub.mu.Unlock(); return len(stub.results) == 2 })
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if got := strings.Join(stub.order, ","); got != "hb,result,hb,result" {
		t.Fatalf("order %s, want a heartbeat before each result", got)
	}
}

// TestGuestEventsCannotEvictOrBlockHostEvents: guest-raised events wait in
// their own capped list. A flood while the api is away never blocks the
// caller, drops only older guest events, and leaves the host's own events
// in place; all of what is kept is delivered after the reconnect.
func TestGuestEventsCannotEvictOrBlockHostEvents(t *testing.T) {
	ln := bufconn.Listen(1 << 20)
	stub := newStub()
	stub.toSend = make(chan *hostdv1.ApiMessage, 4096)
	gs := grpc.NewServer()
	hostdv1.RegisterHostServiceServer(gs, stub)
	defer gs.Stop()

	host := &hostStub{}
	s := New(Config{HeartbeatInterval: time.Hour, BackoffBase: 20 * time.Millisecond, MaxBackoff: 50 * time.Millisecond}, bufDialer{ln}, host, nil, nil)
	host.s = s

	s.Event(&hostdv1.Event{EventId: "host-1", Ev: &hostdv1.Event_GuestStateChanged{GuestStateChanged: &hostdv1.GuestStateChanged{State: "stopped"}}})
	const flood = MaxGuestEvents + 3000
	done := make(chan int)
	go func() {
		evicted := 0
		for i := 0; i < flood; i++ {
			if !s.GuestEvent(&hostdv1.Event{EventId: fmt.Sprintf("guest-%d", i), Ev: &hostdv1.Event_AgentEvent{AgentEvent: &hostdv1.AgentEvent{Kind: "agent_message"}}}) {
				evicted++
			}
		}
		done <- evicted
	}()
	var evicted int
	select {
	case evicted = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("GuestEvent blocked with no session")
	}
	if evicted != flood-MaxGuestEvents {
		t.Fatalf("evicted %d, want %d", evicted, flood-MaxGuestEvents)
	}
	s.mu.Lock()
	_, hostKept := s.events["host-1"]
	pending := len(s.events)
	s.mu.Unlock()
	if !hostKept || pending != MaxGuestEvents+1 {
		t.Fatalf("host event kept %v, pending %d", hostKept, pending)
	}

	go func() { _ = gs.Serve(ln) }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	waitFor(t, "every kept event acked", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.events) == 0 })
	stub.mu.Lock()
	defer stub.mu.Unlock()
	seen := map[string]int{}
	for _, e := range stub.events {
		seen[e.EventId]++
	}
	if seen["host-1"] != 1 || seen["guest-0"] != 0 || seen[fmt.Sprintf("guest-%d", flood-1)] != 1 || len(seen) != MaxGuestEvents+1 {
		t.Fatalf("delivered %d distinct events (host-1 %d, guest-0 %d)", len(seen), seen["host-1"], seen["guest-0"])
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("%s delivered %d times", id, n)
		}
	}

	// On a live session a guest event goes out without a reconnect.
	s.GuestEvent(&hostdv1.Event{EventId: "guest-live", Ev: &hostdv1.Event_AgentEvent{AgentEvent: &hostdv1.AgentEvent{Kind: "completed"}}})
	stub.mu.Unlock()
	waitFor(t, "live guest event", func() bool { stub.mu.Lock(); defer stub.mu.Unlock(); return len(stub.events) == MaxGuestEvents+2 })
	stub.mu.Lock()
}
