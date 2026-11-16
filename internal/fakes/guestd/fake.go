package guestd

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/vsockrpc"
)

// Call is one recorded request, by its family name.
type Call struct {
	Kind string
	Req  *guestdv1.Request
	At   time.Time
}

// Fake is an in-process guestd.
type Fake struct {
	mu sync.Mutex

	calls []Call
	conns []*vsockrpc.Conn

	// Version is what Ping reports.
	Version string
	// BootID is what Ping and Ready report.
	BootID string
	// Uptime is what Ping reports.
	Uptime uint64
	// Signals and Procs are what Sample returns.
	Signals *hostdv1.GuestSignals
	Procs   []*hostdv1.ProcSample
	// Partial is the Sample partial flag.
	Partial bool
	// SwitchResult is what Switch returns when it is not made to fail.
	SwitchResult *guestdv1.SwitchResult
	// GrowBytes is what GrowFs reports.
	GrowBytes uint64
	// ExecResult is what Exec returns.
	ExecResult *guestdv1.ExecResult
	// Fail maps a request kind ("freeze", "switch", ...) to the error it
	// should answer with.
	Fail map[string]*guestdv1.Error
	// Frozen tracks Freeze and Thaw.
	Frozen bool
}

// New builds a fake with harmless defaults.
func New() *Fake {
	return &Fake{
		Version:      "1",
		BootID:       "00000000-0000-0000-0000-000000000000",
		Uptime:       42,
		Signals:      &hostdv1.GuestSignals{GuestdOk: true},
		SwitchResult: &guestdv1.SwitchResult{},
		GrowBytes:    20 << 30,
		ExecResult:   &guestdv1.ExecResult{},
		Fail:         map[string]*guestdv1.Error{},
	}
}

// Serve handles one connection until it ends.
func (f *Fake) Serve(ctx context.Context, c net.Conn) {
	conn := vsockrpc.NewConn(c)
	f.mu.Lock()
	f.conns = append(f.conns, conn)
	f.mu.Unlock()

	for {
		if ctx.Err() != nil {
			return
		}
		env, err := conn.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				return
			}
			return
		}
		req := env.GetRequest()
		if req == nil {
			continue
		}
		resp := f.answer(req)
		if err := conn.Send(&guestdv1.Envelope{
			RequestId: env.GetRequestId(),
			Body:      &guestdv1.Envelope_Response{Response: resp},
		}); err != nil {
			return
		}
	}
}

// Listen starts a unix listener and serves every connection on it, so a test
// can point hostd's client at a path.
func (f *Fake) Listen(ctx context.Context, path string) (net.Listener, error) {
	l, err := vsockrpc.ListenUnix(path)
	if err != nil {
		return nil, err
	}
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go f.Serve(ctx, c)
		}
	}()
	return l, nil
}

// Notify pushes an unsolicited notification to every live connection.
func (f *Fake) Notify(n *guestdv1.Notify) {
	f.mu.Lock()
	conns := make([]*vsockrpc.Conn, len(f.conns))
	copy(conns, f.conns)
	f.mu.Unlock()
	env := &guestdv1.Envelope{Body: &guestdv1.Envelope_Notify{Notify: n}}
	for _, c := range conns {
		_ = c.Send(env)
	}
}

// Ready is the convenience for the notification hostd waits for at boot.
func (f *Fake) Ready() {
	f.Notify(&guestdv1.Notify{N: &guestdv1.Notify_Ready{Ready: &guestdv1.Ready{BootId: f.BootID}}})
}

// Calls returns every recorded request.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Call, len(f.calls))
	copy(out, f.calls)
	return out
}

// Kinds returns the recorded request kinds in order, which is what most
// assertions want.
func (f *Fake) Kinds() []string {
	calls := f.Calls()
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Kind)
	}
	return out
}

// Reset drops the recorded calls.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func (f *Fake) record(kind string, req *guestdv1.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, Call{Kind: kind, Req: req, At: time.Now()})
}

func (f *Fake) failure(kind string) *guestdv1.Response {
	f.mu.Lock()
	e := f.Fail[kind]
	f.mu.Unlock()
	if e == nil {
		return nil
	}
	return &guestdv1.Response{Ok: false, Error: e}
}

func (f *Fake) answer(req *guestdv1.Request) *guestdv1.Response {
	kind := Kind(req)
	f.record(kind, req)
	if resp := f.failure(kind); resp != nil {
		return resp
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	switch req.GetReq().(type) {
	case *guestdv1.Request_Ping:
		return &guestdv1.Response{Ok: true, Result: &guestdv1.Response_Ping{Ping: &guestdv1.PingResult{
			Version: f.Version, UptimeS: f.Uptime, BootId: f.BootID,
		}}}
	case *guestdv1.Request_Freeze:
		f.Frozen = true
		return &guestdv1.Response{Ok: true}
	case *guestdv1.Request_Thaw:
		f.Frozen = false
		return &guestdv1.Response{Ok: true}
	case *guestdv1.Request_Switch:
		return &guestdv1.Response{Ok: true, Result: &guestdv1.Response_Switch{Switch: f.SwitchResult}}
	case *guestdv1.Request_RegisterPaths:
		return &guestdv1.Response{Ok: true}
	case *guestdv1.Request_GrowFs:
		return &guestdv1.Response{Ok: true, Result: &guestdv1.Response_GrowFs{
			GrowFs: &guestdv1.GrowFsResult{NewBytes: f.GrowBytes},
		}}
	case *guestdv1.Request_Sample:
		return &guestdv1.Response{Ok: true, Result: &guestdv1.Response_Sample{Sample: &guestdv1.SampleResult{
			Signals: f.Signals, Procs: f.Procs, Partial: f.Partial,
		}}}
	case *guestdv1.Request_Exec:
		return &guestdv1.Response{Ok: true, Result: &guestdv1.Response_Exec{Exec: f.ExecResult}}
	default:
		return &guestdv1.Response{Ok: true}
	}
}

// Kind names a request's family, the key used by Fail and Kinds.
func Kind(req *guestdv1.Request) string {
	switch req.GetReq().(type) {
	case *guestdv1.Request_Ping:
		return "ping"
	case *guestdv1.Request_Freeze:
		return "freeze"
	case *guestdv1.Request_Thaw:
		return "thaw"
	case *guestdv1.Request_Switch:
		return "switch"
	case *guestdv1.Request_RegisterPaths:
		return "register_paths"
	case *guestdv1.Request_AnswerQuestion:
		return "answer_question"
	case *guestdv1.Request_SayToAgent:
		return "say_to_agent"
	case *guestdv1.Request_GrowFs:
		return "grow_fs"
	case *guestdv1.Request_WriteSecrets:
		return "write_secrets"
	case *guestdv1.Request_SetPrincipals:
		return "set_principals"
	case *guestdv1.Request_SetupProject:
		return "setup_project"
	case *guestdv1.Request_Sample:
		return "sample"
	case *guestdv1.Request_Exec:
		return "exec"
	case *guestdv1.Request_Shutdown:
		return "shutdown"
	default:
		return "unknown"
	}
}
