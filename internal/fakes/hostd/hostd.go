// Package hostd is the in-process fake host the api's tests use: it
// implements the client side of docs/interfaces/grpc-hostd.md, "creates"
// guests as structs, emits Samples on a ticker, completes Build after a
// configurable delay with a fake closure path, and can be told to fail
// any command with any error code.
package hostd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// Guest is a fake guest.
type Guest struct {
	GuestID   string
	ProjectID string
	Class     string
	State     string
	IP        string
	CID       uint32
	Closure   string
	Secrets   map[string][]byte
	// GuestdDead models a guest whose unit runs but whose guestd does not
	// answer (I-143's strand): every command that needs guestd on a
	// running guest fails with guest_unresponsive, as the real hostd does,
	// until the guest is booted again.
	GuestdDead bool
}

// Options tune the fake.
type Options struct {
	HostID         string
	SampleInterval time.Duration
	Heartbeat      time.Duration
	BuildDelay     time.Duration
	FakeClosure    string
	// Fail maps a command kind ("CreateGuest", "Build", ...) to the error
	// code every command of that kind returns.
	Fail map[string]string
	// KernelChanged is what Build reports.
	KernelChanged bool
}

// Fake is one fake host.
type Fake struct {
	opts Options

	mu       sync.Mutex
	guests   map[string]*Guest
	results  map[string]*hostdv1.Result
	failMsg  map[string]string
	draining bool
	nextIP   int
	send     func(*hostdv1.HostMessage) error
	commands []*hostdv1.Command
	// asks are the questions the fake's guests "know" (DECISIONS I-244):
	// question_id -> the AnswerQuestion that closed it, nil while open.
	asks map[string]*hostdv1.AnswerQuestion
}

// New returns a fake with defaults.
func New(opts Options) *Fake {
	if opts.HostID == "" {
		opts.HostID = "fake-host"
	}
	if opts.SampleInterval == 0 {
		opts.SampleInterval = 60 * time.Second
	}
	if opts.Heartbeat == 0 {
		opts.Heartbeat = 15 * time.Second
	}
	if opts.FakeClosure == "" {
		opts.FakeClosure = "/nix/store/00000000000000000000000000000000-nixos-system-fake"
	}
	if opts.Fail == nil {
		opts.Fail = map[string]string{}
	}
	return &Fake{opts: opts, guests: map[string]*Guest{}, results: map[string]*hostdv1.Result{}, asks: map[string]*hostdv1.AnswerQuestion{}}
}

// Ask emits an AgentQuestion as a guest's repose-ask would, and makes the
// fake's guest know the question so AnswerQuestion succeeds.
func (f *Fake) Ask(q *hostdv1.AgentQuestion) {
	f.mu.Lock()
	if _, ok := f.asks[q.QuestionId]; !ok {
		f.asks[q.QuestionId] = nil
	}
	f.mu.Unlock()
	f.event(&hostdv1.Event{Ev: &hostdv1.Event_AgentQuestion{AgentQuestion: q}})
}

// Message emits an agent_message AgentEvent as repose-notify would.
func (f *Fake) Message(guestID, agent, text string) {
	f.event(&hostdv1.Event{Ev: &hostdv1.Event_AgentEvent{AgentEvent: &hostdv1.AgentEvent{GuestId: guestID, Agent: agent, Kind: "agent_message", Summary: text, TmuxWindow: agent}}})
}

// Answer is what closed a question, nil while it is open or unknown.
func (f *Fake) Answer(questionID string) *hostdv1.AnswerQuestion {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asks[questionID]
}

// ForgetQuestion makes the fake's guest forget a question, as a guest
// reboot does: the next AnswerQuestion for it is not_found.
func (f *Fake) ForgetQuestion(questionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.asks, questionID)
}

// Guests returns the fake's guests.
func (f *Fake) Guests() []*Guest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Guest
	for _, g := range f.guests {
		cp := *g
		// The struct copy shared the Secrets map with the live guest, which
		// UpdateSecrets writes under f.mu while a test reads the copy
		// without it (race detector, CI 2026-09-20).
		cp.Secrets = make(map[string][]byte, len(g.Secrets))
		for k, v := range g.Secrets {
			cp.Secrets[k] = v
		}
		out = append(out, &cp)
	}
	return out
}

// Commands returns every command received.
func (f *Fake) Commands() []*hostdv1.Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*hostdv1.Command(nil), f.commands...)
}

// SetKernelChanged changes what Build reports at runtime.
func (f *Fake) SetKernelChanged(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opts.KernelChanged = v
}

// SetFail changes the failure map at runtime.
func (f *Fake) SetFail(kind, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if code == "" {
		delete(f.opts.Fail, kind)
	} else {
		f.opts.Fail[kind] = code
	}
}

// SetFailMessage sets the message a failing command of kind returns, as
// hostd's build_failed carries the builder's log tail; empty restores the
// default.
func (f *Fake) SetFailMessage(kind, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failMsg == nil {
		f.failMsg = map[string]string{}
	}
	f.failMsg[kind] = msg
}

// SetGuestdDead marks a guest's guestd dead or alive.
func (f *Fake) SetGuestdDead(guestID string, dead bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g, ok := f.guests[guestID]; ok {
		g.GuestdDead = dead
	}
}

// guestdGone reports whether a command needing guestd on g must fail the
// way hostd's session() does.
func (f *Fake) guestdGone(g *Guest) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return g.GuestdDead && g.State == "running"
}

func (f *Fake) stateOf(g *Guest) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return g.State
}

func unresponsive(id string, g *Guest) *hostdv1.Result {
	return errResult(id, "guest_unresponsive", "guestd unreachable for guest "+g.GuestID)
}

// Hello is the reconciliation message.
func (f *Fake) Hello() *hostdv1.Hello {
	f.mu.Lock()
	defer f.mu.Unlock()
	h := &hostdv1.Hello{HostId: f.opts.HostID, FreeMemBytes: 200 << 30, PoolFreeBytes: 1 << 40}
	for _, g := range f.guests {
		h.Guests = append(h.Guests, &hostdv1.GuestStatus{GuestId: g.GuestID, State: g.State, Ip: g.IP, VsockCid: g.CID, SystemClosure: g.Closure})
	}
	return h
}

// Run holds a Session on conn until ctx ends or the stream fails.
func (f *Fake) Run(ctx context.Context, conn grpc.ClientConnInterface) error {
	sess, err := hostdv1.NewHostServiceClient(conn).Session(ctx)
	if err != nil {
		return err
	}
	var sendMu sync.Mutex
	send := func(m *hostdv1.HostMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return sess.Send(m)
	}
	f.mu.Lock()
	f.send = send
	f.mu.Unlock()
	if err := send(&hostdv1.HostMessage{Msg: &hostdv1.HostMessage_Hello{Hello: f.Hello()}}); err != nil {
		return err
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		hb := time.NewTicker(f.opts.Heartbeat)
		sm := time.NewTicker(f.opts.SampleInterval)
		defer hb.Stop()
		defer sm.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case <-hb.C:
				f.mu.Lock()
				running := uint32(0)
				for _, g := range f.guests {
					if g.State == "running" {
						running++
					}
				}
				dr := f.draining
				f.mu.Unlock()
				_ = send(&hostdv1.HostMessage{Msg: &hostdv1.HostMessage_Heartbeat{Heartbeat: &hostdv1.Heartbeat{FreeMemBytes: 200 << 30, PoolFreeBytes: 1 << 40, Load1: 0.1, RunningGuests: running, Draining: dr}}}) // a dead stream ends Recv
			case <-sm.C:
				_ = send(&hostdv1.HostMessage{Msg: &hostdv1.HostMessage_Samples{Samples: f.Samples()}}) // same
			}
		}
	}()
	for {
		msg, err := sess.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if c, ok := msg.Msg.(*hostdv1.ApiMessage_Command); ok {
			go func(cmd *hostdv1.Command) {
				res := f.Execute(cmd)
				_ = send(&hostdv1.HostMessage{Msg: &hostdv1.HostMessage_Result{Result: res}}) // same
			}(c.Command)
		}
	}
}

// Samples builds one Samples message with a busy fake guest.
func (f *Fake) Samples() *hostdv1.Samples {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &hostdv1.Samples{Ts: time.Now().Unix(), Host: &hostdv1.HostSample{MemFree: 200 << 30, PoolFree: 1 << 40, Load1: 0.1}}
	for _, g := range f.guests {
		gs := &hostdv1.GuestSample{GuestId: g.GuestID, State: g.State, Class: g.Class, DiskAllocBytes: 40 << 30, DiskUsedBytes: 5 << 30, Signals: &hostdv1.GuestSignals{}}
		if g.State == "running" {
			gs.CpuNsDelta, gs.MemRssBytes, gs.NetTxBytesDelta, gs.NetRxBytesDelta = 30e9, 2<<30, 1<<20, 4<<20
			gs.Signals = &hostdv1.GuestSignals{SshSessions: 1, TmuxClients: 1, GuestdOk: !g.GuestdDead, Agents: []*hostdv1.AgentProc{{Agent: "claude", TmuxWindow: "claude", State: "working"}}}
			gs.Procs = []*hostdv1.ProcSample{{Comm: "claude", CpuNsDelta: 25e9, RssBytes: 1 << 30}, {Comm: "node", CpuNsDelta: 5e9, RssBytes: 300 << 20}}
		}
		s.Guests = append(s.Guests, gs)
	}
	return s
}

func (f *Fake) event(ev *hostdv1.Event) {
	f.mu.Lock()
	send := f.send
	f.mu.Unlock()
	if send == nil {
		return
	}
	ev.EventId = uuid.Must(uuid.NewV7()).String()
	ev.Ts = time.Now().Unix()
	_ = send(&hostdv1.HostMessage{Msg: &hostdv1.HostMessage_Event{Event: ev}}) // a dead stream ends Recv
}

func (f *Fake) setState(g *Guest, st, reason string) {
	// Under f.mu: the heartbeat goroutine reads every guest's State under
	// the same lock, and callers of setState hold no lock (CI race,
	// 2026-09-20).
	f.mu.Lock()
	g.State = st
	f.mu.Unlock()
	f.event(&hostdv1.Event{Ev: &hostdv1.Event_GuestStateChanged{GuestStateChanged: &hostdv1.GuestStateChanged{GuestId: g.GuestID, State: st, Reason: reason}}})
}

func errResult(id, code, msg string) *hostdv1.Result {
	return &hostdv1.Result{CommandId: id, Ok: false, Error: &hostdv1.Error{Code: code, Message: msg}}
}

func kind(cmd *hostdv1.Command) string {
	switch cmd.Cmd.(type) {
	case *hostdv1.Command_CreateGuest:
		return "CreateGuest"
	case *hostdv1.Command_StartGuest:
		return "StartGuest"
	case *hostdv1.Command_StopGuest:
		return "StopGuest"
	case *hostdv1.Command_DestroyGuest:
		return "DestroyGuest"
	case *hostdv1.Command_ResizeVolume:
		return "ResizeVolume"
	case *hostdv1.Command_Build:
		return "Build"
	case *hostdv1.Command_ApplyConfig:
		return "ApplyConfig"
	case *hostdv1.Command_Snapshot:
		return "Snapshot"
	case *hostdv1.Command_Restore:
		return "Restore"
	case *hostdv1.Command_UpdateSecrets:
		return "UpdateSecrets"
	case *hostdv1.Command_SetPrincipals:
		return "SetPrincipals"
	case *hostdv1.Command_Exec:
		return "Exec"
	case *hostdv1.Command_Drain:
		return "Drain"
	case *hostdv1.Command_AnswerQuestion:
		return "AnswerQuestion"
	}
	return ""
}

// Execute runs a command synchronously, with the documented idempotency.
func (f *Fake) Execute(cmd *hostdv1.Command) *hostdv1.Result {
	f.mu.Lock()
	f.commands = append(f.commands, cmd)
	if r, ok := f.results[cmd.CommandId]; ok {
		f.mu.Unlock()
		return r
	}
	if code, ok := f.opts.Fail[kind(cmd)]; ok {
		msg := "fake host was told to fail " + kind(cmd)
		if m := f.failMsg[kind(cmd)]; m != "" {
			msg = m
		}
		r := errResult(cmd.CommandId, code, msg)
		f.results[cmd.CommandId] = r
		f.mu.Unlock()
		return r
	}
	f.mu.Unlock()
	res := f.execute(cmd)
	f.mu.Lock()
	f.results[cmd.CommandId] = res
	f.mu.Unlock()
	return res
}

func (f *Fake) execute(cmd *hostdv1.Command) *hostdv1.Result {
	id := cmd.CommandId
	ok := func(payload func(*hostdv1.Result)) *hostdv1.Result {
		r := &hostdv1.Result{CommandId: id, Ok: true}
		if payload != nil {
			payload(r)
		}
		return r
	}
	get := func(gid string) (*Guest, *hostdv1.Result) {
		g, found := f.guests[gid]
		if !found {
			return nil, errResult(id, "not_found", "guest "+gid+" not on this host")
		}
		return g, nil
	}
	switch c := cmd.Cmd.(type) {
	case *hostdv1.Command_CreateGuest:
		f.mu.Lock()
		if f.draining {
			f.mu.Unlock()
			return errResult(id, "insufficient_capacity", "host draining")
		}
		if _, exists := f.guests[c.CreateGuest.GuestId]; exists {
			f.mu.Unlock()
			return errResult(id, "already_exists", "guest exists")
		}
		f.nextIP++
		g := &Guest{GuestID: c.CreateGuest.GuestId, ProjectID: c.CreateGuest.ProjectId, Class: c.CreateGuest.Class, State: "creating",
			IP: fmt.Sprintf("10.64.4.%d", 1+f.nextIP), CID: uint32(1000 + f.nextIP), Closure: c.CreateGuest.SystemClosure, Secrets: map[string][]byte{}}
		for _, s := range c.CreateGuest.Secrets {
			g.Secrets[s.Name] = s.Value
		}
		f.guests[g.GuestID] = g
		f.mu.Unlock()
		f.setState(g, "creating", "")
		f.setState(g, "starting", "")
		f.setState(g, "running", "")
		return ok(func(r *hostdv1.Result) {
			r.Payload = &hostdv1.Result_Create{Create: &hostdv1.CreateResult{GuestIp: g.IP, VsockCid: g.CID}}
		})
	case *hostdv1.Command_StartGuest:
		f.mu.Lock()
		g, e := get(c.StartGuest.GuestId)
		f.mu.Unlock()
		if e != nil {
			return e
		}
		if g.State != "running" {
			f.mu.Lock()
			g.GuestdDead = false // a boot brings a fresh guestd
			if c.StartGuest.Class != "" {
				g.Class = c.StartGuest.Class // I-260, as hostd
			}
			f.mu.Unlock()
			f.setState(g, "starting", "")
			f.setState(g, "running", "")
		}
		return ok(nil)
	case *hostdv1.Command_StopGuest:
		f.mu.Lock()
		g, e := get(c.StopGuest.GuestId)
		f.mu.Unlock()
		if e != nil {
			return e
		}
		var blob string
		if c.StopGuest.SnapshotFirst && f.guestdGone(g) {
			return unresponsive(id, g)
		}
		if c.StopGuest.SnapshotFirst && f.stateOf(g) == "running" {
			blob = fmt.Sprintf("%s/%s/%d.img.zst", "user", g.ProjectID, time.Now().UnixNano())
			f.event(&hostdv1.Event{Ev: &hostdv1.Event_SnapshotDone{SnapshotDone: &hostdv1.SnapshotDone{GuestId: g.GuestID, BlobPath: blob, Bytes: 1 << 30}}})
		}
		f.setState(g, "stopping", "")
		f.setState(g, "stopped", "")
		return ok(func(r *hostdv1.Result) {
			r.Payload = &hostdv1.Result_Stop{Stop: &hostdv1.StopResult{BlobPath: blob, Bytes: 1 << 30}}
		})
	case *hostdv1.Command_DestroyGuest:
		f.mu.Lock()
		g, e := get(c.DestroyGuest.GuestId)
		if e != nil {
			f.mu.Unlock()
			return e
		}
		delete(f.guests, g.GuestID)
		f.mu.Unlock()
		f.setState(g, "destroying", "")
		f.setState(g, "destroyed", "")
		return ok(nil)
	case *hostdv1.Command_ResizeVolume:
		f.mu.Lock()
		g, e := get(c.ResizeVolume.GuestId)
		f.mu.Unlock()
		if e != nil {
			return e
		}
		if f.guestdGone(g) {
			return unresponsive(id, g) // hostd extended the volume; GrowFs needs guestd
		}
		return ok(nil)
	case *hostdv1.Command_Build:
		f.mu.Lock()
		send := f.send
		f.mu.Unlock()
		for i, line := range []string{"evaluating configuration", "building fake system", "built"} {
			if send != nil {
				_ = send(&hostdv1.HostMessage{Msg: &hostdv1.HostMessage_Log{Log: &hostdv1.BuildLog{CommandId: id, Seq: uint64(i + 1), Line: line}}}) // dead stream ends Recv
			}
			if f.opts.BuildDelay > 0 {
				time.Sleep(f.opts.BuildDelay / 3)
			}
		}
		closure := f.opts.FakeClosure
		if c.Build.RevisionId != "" {
			closure = f.opts.FakeClosure + "-" + c.Build.RevisionId
		}
		return ok(func(r *hostdv1.Result) {
			r.Payload = &hostdv1.Result_Build{Build: &hostdv1.BuildResult{SystemClosure: closure, ClosureBytes: 3 << 30, KernelChanged: f.opts.KernelChanged}}
		})
	case *hostdv1.Command_ApplyConfig:
		// A kernel-changing closure applies nothing unless force_reboot
		// (DECISIONS I-5); the fake reports kernel changes per KernelChanged.
		f.mu.Lock()
		g, e := get(c.ApplyConfig.GuestId)
		if e == nil && g.GuestdDead && g.State == "running" {
			f.mu.Unlock()
			return unresponsive(id, g)
		}
		needsReboot := e == nil && f.opts.KernelChanged && g.Closure != c.ApplyConfig.SystemClosure && !c.ApplyConfig.ForceReboot
		if e == nil && !needsReboot {
			g.Closure = c.ApplyConfig.SystemClosure
		}
		f.mu.Unlock()
		if e != nil {
			return e
		}
		return ok(func(r *hostdv1.Result) {
			r.Payload = &hostdv1.Result_Apply{Apply: &hostdv1.ApplyResult{Rebooted: c.ApplyConfig.ForceReboot, RebootRequired: needsReboot}}
		})
	case *hostdv1.Command_Snapshot:
		f.mu.Lock()
		g, e := get(c.Snapshot.GuestId)
		f.mu.Unlock()
		if e != nil {
			return e
		}
		if f.guestdGone(g) {
			return unresponsive(id, g) // no Freeze without guestd
		}
		blob := fmt.Sprintf("%s/%s/%d.img.zst", "user", g.ProjectID, time.Now().UnixNano())
		f.event(&hostdv1.Event{Ev: &hostdv1.Event_SnapshotDone{SnapshotDone: &hostdv1.SnapshotDone{GuestId: g.GuestID, BlobPath: blob, Bytes: 1 << 30}}})
		return ok(func(r *hostdv1.Result) {
			r.Payload = &hostdv1.Result_Snapshot{Snapshot: &hostdv1.SnapshotResult{BlobPath: blob, Bytes: 1 << 30}}
		})
	case *hostdv1.Command_Restore:
		f.mu.Lock()
		if f.draining {
			f.mu.Unlock()
			return errResult(id, "insufficient_capacity", "host draining")
		}
		f.nextIP++
		g := &Guest{GuestID: c.Restore.GuestId, ProjectID: c.Restore.ProjectId, Class: c.Restore.Class, State: "restoring", IP: fmt.Sprintf("10.64.4.%d", 1+f.nextIP), CID: uint32(1000 + f.nextIP), Closure: c.Restore.SystemClosure}
		f.guests[g.GuestID] = g
		f.mu.Unlock()
		f.setState(g, "restoring", "")
		f.setState(g, "stopped", "")
		return ok(func(r *hostdv1.Result) {
			r.Payload = &hostdv1.Result_Create{Create: &hostdv1.CreateResult{GuestIp: g.IP, VsockCid: g.CID}}
		})
	case *hostdv1.Command_UpdateSecrets:
		f.mu.Lock()
		g, e := get(c.UpdateSecrets.GuestId)
		if e == nil {
			for _, s := range c.UpdateSecrets.Secrets {
				g.Secrets[s.Name] = s.Value
			}
		}
		f.mu.Unlock()
		if e != nil {
			return e
		}
		return ok(nil)
	case *hostdv1.Command_SetPrincipals:
		f.mu.Lock()
		_, e := get(c.SetPrincipals.GuestId)
		f.mu.Unlock()
		if e != nil {
			return e
		}
		return ok(nil)
	case *hostdv1.Command_Exec:
		if c.Exec.AuditId == "" {
			return errResult(id, "invalid_argument", "audit_id required")
		}
		f.mu.Lock()
		_, e := get(c.Exec.GuestId)
		f.mu.Unlock()
		if e != nil {
			return e
		}
		return ok(func(r *hostdv1.Result) {
			r.Payload = &hostdv1.Result_Exec{Exec: &hostdv1.ExecResult{Stdout: []byte("fake\n")}}
		})
	case *hostdv1.Command_Drain:
		f.mu.Lock()
		f.draining = true
		f.mu.Unlock()
		return ok(nil)
	case *hostdv1.Command_AnswerQuestion:
		a := c.AnswerQuestion
		f.mu.Lock()
		g, e := get(a.GuestId)
		if e == nil && g.State != "running" {
			e = errResult(id, "not_found", "guest is "+g.State)
		}
		if e == nil {
			if _, known := f.asks[a.QuestionId]; !known {
				e = errResult(id, "not_found", "no question "+a.QuestionId+" in this guest")
			} else if f.asks[a.QuestionId] == nil {
				f.asks[a.QuestionId] = a
			}
		}
		f.mu.Unlock()
		if e != nil {
			return e
		}
		return ok(nil)
	}
	return errResult(id, "invalid_argument", "unknown command")
}
