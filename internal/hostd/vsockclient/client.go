// Package vsockclient is hostd's side of docs/interfaces/vsock-guestd.md.
// A Dialer opens the per-guest connection: through Cloud Hypervisor's vsock
// unix socket on a host, or a plain unix socket in dev mode.
package vsockclient

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/hostd/vsockrpc"
)

// Port is guestd's vsock port.
const Port = 5000

// Timeouts per request family, from workstream 04's handler deadlines.
const (
	DefaultTimeout = 30 * time.Second
	SwitchTimeout  = 10 * time.Minute
	// SetupTimeout is SetupProject's: guestd gives it 3 minutes, since it
	// waits for home-manager's first activation (DECISIONS I-705), and
	// hostd waits a little longer so guestd's own error is what arrives.
	SetupTimeout = 3*time.Minute + 15*time.Second
)

// Target names a guest to connect to.
type Target struct {
	GuestID string
	Dir     string // /var/lib/repose/guests/<id>
	CID     uint32
}

// Dialer opens a Session to a guest.
type Dialer interface {
	Dial(ctx context.Context, t Target) (Session, error)
}

// Session is one live connection to a guest's guestd.
type Session interface {
	Ping(ctx context.Context) (*guestdv1.PingResult, error)
	Freeze(ctx context.Context) error
	Thaw(ctx context.Context) error
	Switch(ctx context.Context, closure string, forceReboot bool, registration []byte) (*guestdv1.SwitchResult, error)
	// RegisterPaths loads a `nix-store --dump-db` listing into the guest's
	// database (DECISIONS I-67).
	RegisterPaths(ctx context.Context, registration []byte) error
	GrowFs(ctx context.Context) (uint64, error)
	WriteSecrets(ctx context.Context, secrets []*guestdv1.Secret) error
	SetPrincipals(ctx context.Context, principals []string) error
	SetupProject(ctx context.Context, p *guestdv1.SetupProject) error
	Sample(ctx context.Context) (*guestdv1.SampleResult, error)
	Exec(ctx context.Context, argv []string, timeoutS uint32, asUser string) (*guestdv1.ExecResult, error)
	Shutdown(ctx context.Context, timeoutS uint32) error
	// AnswerQuestion closes a question a repose-ask is waiting on
	// (DECISIONS I-244).
	AnswerQuestion(ctx context.Context, a *guestdv1.AnswerQuestion) error
	Notifications() <-chan *guestdv1.Notify
	Done() <-chan struct{}
	Close() error
}

// CHDialer connects through Cloud Hypervisor's host-side vsock socket
// (`--vsock cid=N,socket=<dir>/vsock.sock`): connect, send `CONNECT 5000`,
// expect `OK <port>`.
type CHDialer struct{}

// Dial implements Dialer.
func (CHDialer) Dial(ctx context.Context, t Target) (Session, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", filepath.Join(t.Dir, "vsock.sock"))
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl) // handshake bounded by ctx; cleared below
	}
	if _, err := fmt.Fprintf(c, "CONNECT %d\n", Port); err != nil {
		_ = c.Close() // handshake failed; the error is returned
		return nil, fmt.Errorf("vsock connect: %w", err)
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		_ = c.Close() // handshake failed; the error is returned
		return nil, fmt.Errorf("vsock handshake: %w", err)
	}
	if !strings.HasPrefix(line, "OK ") {
		_ = c.Close() // handshake refused; the error is returned
		return nil, fmt.Errorf("vsock handshake refused: %s", strings.TrimSpace(line))
	}
	_ = c.SetDeadline(time.Time{}) // clearing a deadline cannot fail on a live socket
	return newSession(c), nil
}

// UnixDialer connects to a plain unix socket, for guests without vsock
// (dev machines) and the fake guestd in tests. Path maps a target to its
// socket; the default is `<dir>/guestd.sock`.
type UnixDialer struct {
	Path func(t Target) string
}

// Dial implements Dialer.
func (u UnixDialer) Dial(ctx context.Context, t Target) (Session, error) {
	p := filepath.Join(t.Dir, "guestd.sock")
	if u.Path != nil {
		p = u.Path(t)
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", p)
	if err != nil {
		return nil, err
	}
	return newSession(c), nil
}

// timeouts are a session's per-family bounds; production sessions use the
// constants above, and a test scales them down to watch a call outlive one.
type timeouts struct {
	def, sw, setup time.Duration
}

var defaultTimeouts = timeouts{def: DefaultTimeout, sw: SwitchTimeout, setup: SetupTimeout}

type session struct {
	cl *vsockrpc.Client
	t  timeouts
}

func newSession(c net.Conn) *session {
	return &session{cl: vsockrpc.NewClient(c, 256), t: defaultTimeouts}
}

func (s *session) call(ctx context.Context, timeout time.Duration, req *guestdv1.Request) (*guestdv1.Response, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return s.cl.Call(ctx, req)
}

func (s *session) Ping(ctx context.Context) (*guestdv1.PingResult, error) {
	r, err := s.call(ctx, s.t.def, &guestdv1.Request{Req: &guestdv1.Request_Ping{Ping: &guestdv1.Ping{}}})
	if err != nil {
		return nil, err
	}
	return r.GetPing(), nil
}

func (s *session) Freeze(ctx context.Context) error {
	_, err := s.call(ctx, s.t.def, &guestdv1.Request{Req: &guestdv1.Request_Freeze{Freeze: &guestdv1.Freeze{}}})
	return err
}

func (s *session) Thaw(ctx context.Context) error {
	_, err := s.call(ctx, s.t.def, &guestdv1.Request{Req: &guestdv1.Request_Thaw{Thaw: &guestdv1.Thaw{}}})
	return err
}

func (s *session) RegisterPaths(ctx context.Context, registration []byte) error {
	_, err := s.call(ctx, s.t.sw, &guestdv1.Request{Req: &guestdv1.Request_RegisterPaths{RegisterPaths: &guestdv1.RegisterPaths{Registration: registration}}})
	return err
}

func (s *session) Switch(ctx context.Context, closure string, force bool, registration []byte) (*guestdv1.SwitchResult, error) {
	r, err := s.call(ctx, s.t.sw, &guestdv1.Request{Req: &guestdv1.Request_Switch{Switch: &guestdv1.Switch{SystemClosure: closure, ForceReboot: force, Registration: registration}}})
	if err != nil {
		if r != nil && r.GetSwitch() != nil {
			return r.GetSwitch(), err
		}
		return nil, err
	}
	return r.GetSwitch(), nil
}

func (s *session) GrowFs(ctx context.Context) (uint64, error) {
	r, err := s.call(ctx, s.t.def, &guestdv1.Request{Req: &guestdv1.Request_GrowFs{GrowFs: &guestdv1.GrowFs{}}})
	if err != nil {
		return 0, err
	}
	return r.GetGrowFs().GetNewBytes(), nil
}

func (s *session) WriteSecrets(ctx context.Context, secrets []*guestdv1.Secret) error {
	_, err := s.call(ctx, s.t.def, &guestdv1.Request{Req: &guestdv1.Request_WriteSecrets{WriteSecrets: &guestdv1.WriteSecrets{Secrets: secrets}}})
	return err
}

func (s *session) SetPrincipals(ctx context.Context, principals []string) error {
	_, err := s.call(ctx, s.t.def, &guestdv1.Request{Req: &guestdv1.Request_SetPrincipals{SetPrincipals: &guestdv1.SetPrincipals{Principals: principals}}})
	return err
}

func (s *session) SetupProject(ctx context.Context, p *guestdv1.SetupProject) error {
	_, err := s.call(ctx, s.t.setup, &guestdv1.Request{Req: &guestdv1.Request_SetupProject{SetupProject: p}})
	return err
}

func (s *session) Sample(ctx context.Context) (*guestdv1.SampleResult, error) {
	r, err := s.call(ctx, s.t.def, &guestdv1.Request{Req: &guestdv1.Request_Sample{Sample: &guestdv1.Sample{}}})
	if err != nil {
		return nil, err
	}
	return r.GetSample(), nil
}

func (s *session) Exec(ctx context.Context, argv []string, timeoutS uint32, asUser string) (*guestdv1.ExecResult, error) {
	if timeoutS == 0 {
		timeoutS = 60
	}
	r, err := s.call(ctx, time.Duration(timeoutS)*time.Second+5*time.Second, &guestdv1.Request{Req: &guestdv1.Request_Exec{Exec: &guestdv1.Exec{Argv: argv, TimeoutS: timeoutS, AsUser: asUser}}})
	if err != nil {
		return nil, err
	}
	return r.GetExec(), nil
}

func (s *session) Shutdown(ctx context.Context, timeoutS uint32) error {
	_, err := s.call(ctx, s.t.def, &guestdv1.Request{Req: &guestdv1.Request_Shutdown{Shutdown: &guestdv1.Shutdown{TimeoutS: timeoutS}}})
	return err
}

func (s *session) AnswerQuestion(ctx context.Context, a *guestdv1.AnswerQuestion) error {
	_, err := s.call(ctx, s.t.def, &guestdv1.Request{Req: &guestdv1.Request_AnswerQuestion{AnswerQuestion: a}})
	return err
}

func (s *session) Notifications() <-chan *guestdv1.Notify { return s.cl.Notifications() }
func (s *session) Done() <-chan struct{}                  { return s.cl.Done() }
func (s *session) Close() error                           { return s.cl.Close() }

// IsRemote reports whether err came from guestd rather than the transport.
func IsRemote(err error) (*vsockrpc.RemoteError, bool) {
	var re *vsockrpc.RemoteError
	if errors.As(err, &re) {
		return re, true
	}
	return nil, false
}
