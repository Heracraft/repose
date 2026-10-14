package hostdev

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/guest"
	"github.com/heracraft/repose/internal/hostd/testca"
)

// Server is the api side for one host.
type Server struct {
	hostdv1.UnimplementedHostServiceServer
	st  *Store
	ca  *testca.CA
	ssh *SSHCA // the SSH CA hostdev signs with; its public line travels as host_ca_pub (I-139)
	log *slog.Logger

	mu        sync.Mutex
	sess      hostdv1.HostService_SessionServer
	sessID    uint64
	connected bool
	waiters   map[string][]chan *hostdv1.Result
	logSubs   map[string][]chan string
	sendMu    sync.Mutex
}

// NewServer loads the CA and state from dir.
func NewServer(dir string, log *slog.Logger) (*Server, error) {
	st, err := Open(dir)
	if err != nil {
		return nil, fmt.Errorf("hostdev: %w (run `hostdev init` first)", err)
	}
	certPEM, err := os.ReadFile(filepath.Join(dir, CAFile))
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, CAKeyFile))
	if err != nil {
		return nil, err
	}
	ca, err := testca.Load(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	sshCA, err := LoadSSHCA(dir)
	if err != nil {
		return nil, err
	}
	return &Server{st: st, ca: ca, ssh: sshCA, log: log, waiters: map[string][]chan *hostdv1.Result{}, logSubs: map[string][]chan string{}}, nil
}

// Serve runs gRPC on the configured listen address and the control socket
// until ctx ends.
func (s *Server) Serve(ctx context.Context) error {
	var listen string
	s.st.View(func(st *State) { listen = st.Listen })
	srvCert, err := tls.LoadX509KeyPair(filepath.Join(s.st.Dir, ServerCert), filepath.Join(s.st.Dir, ServerKey))
	if err != nil {
		return err
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{srvCert}, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: s.ca.Pool(), MinVersion: tls.VersionTLS13}
	gs := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	hostdv1.RegisterHostServiceServer(gs, s)
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	errCh := make(chan error, 2)
	go func() { errCh <- gs.Serve(ln) }()
	go func() { errCh <- s.serveControl(ctx) }()
	s.log.Info("hostdev listening", "event", "listen", "addr", listen)
	select {
	case <-ctx.Done():
		gs.Stop()
		return nil
	case err := <-errCh:
		gs.Stop()
		return err
	}
}

func hostIDFromPeer(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(ti.State.VerifiedChains) == 0 || len(ti.State.VerifiedChains[0]) == 0 {
		return ""
	}
	return ti.State.VerifiedChains[0][0].Subject.CommonName
}

// Register implements the unary registration: one token, one host.
func (s *Server) Register(ctx context.Context, req *hostdv1.RegisterRequest) (*hostdv1.RegisterResponse, error) {
	var resp *hostdv1.RegisterResponse
	var rerr error
	err := s.st.Update(func(st *State) {
		if req.JoinToken == "" || req.JoinToken != st.JoinToken {
			rerr = status.Error(codes.PermissionDenied, "join token invalid")
			return
		}
		if st.TokenUsed {
			rerr = status.Error(codes.PermissionDenied, "join token already used")
			return
		}
		hostID := st.Host.HostID
		if hostID == "" {
			hostID = "host-" + newID()[:8]
		}
		cert, key, err := s.ca.IssueClient(hostID, 30*24*time.Hour)
		if err != nil {
			rerr = status.Error(codes.Internal, err.Error())
			return
		}
		st.TokenUsed = true
		st.Host.HostID = hostID
		st.Host.Registered = time.Now().UTC()
		st.Host.Info = rawProto(req.Info)
		resp = &hostdv1.RegisterResponse{HostId: hostID, ClientCert: cert, ClientKey: key, GuestCidr: st.GuestCIDR, HostCaPub: s.hostCAPub()}
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if rerr != nil {
		s.log.Warn("register refused", "event", "register", "code", status.Code(rerr).String())
		return nil, rerr
	}
	s.log.Info("host registered", "event", "register", "host_id", resp.HostId)
	return resp, nil
}

// Rotate reissues the certificate for the authenticated host.
func (s *Server) Rotate(ctx context.Context, _ *hostdv1.RegisterRequest) (*hostdv1.RegisterResponse, error) {
	hostID := hostIDFromPeer(ctx)
	if hostID == "" {
		return nil, status.Error(codes.Unauthenticated, "client certificate required")
	}
	var cidr string
	s.st.View(func(st *State) { cidr = st.GuestCIDR })
	cert, key, err := s.ca.IssueClient(hostID, 30*24*time.Hour)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	s.log.Info("certificate rotated", "event", "rotate", "host_id", hostID)
	return &hostdv1.RegisterResponse{HostId: hostID, ClientCert: cert, ClientKey: key, GuestCidr: cidr, HostCaPub: s.hostCAPub()}, nil
}

// hostCAPub is hostdev's one SSH CA (review L-3: it signs both kinds),
// sent as the Host CA so an operator certificate it issues opens the host
// (I-139).
func (s *Server) hostCAPub() string {
	if s.ssh == nil {
		return ""
	}
	return strings.TrimSpace(s.ssh.Pub)
}

// Session is the long-lived stream.
func (s *Server) Session(srv hostdv1.HostService_SessionServer) error {
	hostID := hostIDFromPeer(srv.Context())
	if hostID == "" {
		return status.Error(codes.Unauthenticated, "client certificate required")
	}
	var known string
	s.st.View(func(st *State) { known = st.Host.HostID })
	if known != "" && known != hostID {
		return status.Error(codes.PermissionDenied, "this hostdev serves host "+known)
	}
	s.mu.Lock()
	s.sessID++
	mine := s.sessID
	s.sess, s.connected = srv, true
	s.mu.Unlock()
	s.log.Info("host connected", "event", "stream_connect", "host_id", hostID)
	defer func() {
		s.mu.Lock()
		if s.sessID == mine {
			s.sess, s.connected = nil, false
		}
		s.mu.Unlock()
		s.log.Info("host disconnected", "event", "stream_disconnect", "host_id", hostID)
	}()
	for {
		msg, err := srv.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch m := msg.Msg.(type) {
		case *hostdv1.HostMessage_Hello:
			s.onHello(m.Hello)
		case *hostdv1.HostMessage_Heartbeat:
			_ = s.st.Update(func(st *State) { // a failed state write only loses the heartbeat copy
				st.Host.LastHeartbeat = rawProto(m.Heartbeat)
				st.Host.HeartbeatAt = time.Now().UTC()
			})
		case *hostdv1.HostMessage_Result:
			s.onResult(m.Result)
		case *hostdv1.HostMessage_Samples:
			_ = s.st.Update(func(st *State) { // same
				st.Samples = append(st.Samples, rawProto(m.Samples))
				if len(st.Samples) > 10 {
					st.Samples = st.Samples[len(st.Samples)-10:]
				}
				for _, g := range m.Samples.Guests {
					if st.Host.Guests == nil {
						st.Host.Guests = map[string]string{}
					}
					st.Host.Guests[g.GuestId] = g.State
				}
			})
		case *hostdv1.HostMessage_Event:
			s.onEvent(m.Event)
		case *hostdv1.HostMessage_Log:
			s.onLog(m.Log)
		}
	}
}

func (s *Server) onHello(h *hostdv1.Hello) {
	var unfinished []*hostdv1.Command
	_ = s.st.Update(func(st *State) { // see above
		st.Host.LastHello = rawProto(h)
		st.Host.Guests = map[string]string{}
		byGuest := map[string]*Project{}
		for _, p := range st.Projects {
			if p.GuestID != "" {
				byGuest[p.GuestID] = p
			}
		}
		for _, g := range h.Guests {
			st.Host.Guests[g.GuestId] = g.State
			// As the api's Hello reconcile since I-720: the host's word
			// on its own guests' addresses.
			if p := byGuest[g.GuestId]; p != nil && g.Ip != "" {
				p.GuestIP = g.Ip
				if g.VsockCid != 0 {
					p.VsockCID = g.VsockCid
				}
			}
		}
		for _, c := range st.Commands {
			if c.Result == nil {
				cmd := &hostdv1.Command{}
				if err := protojson.Unmarshal(c.Command, cmd); err == nil {
					unfinished = append(unfinished, cmd)
				}
			}
		}
	})
	s.log.Info("hello", "event", "hello", "guests", len(h.Guests), "resend", len(unfinished))
	// The api re-sends unfinished commands after Hello (grpc-hostd.md).
	for _, c := range unfinished {
		_ = s.send(&hostdv1.ApiMessage{Msg: &hostdv1.ApiMessage_Command{Command: c}}) // a dead stream is noticed by Recv
	}
}

func (s *Server) onResult(r *hostdv1.Result) {
	_ = s.st.Update(func(st *State) { // see above
		c, ok := st.Commands[r.CommandId]
		if !ok {
			return
		}
		c.Result = rawProto(r)
		c.DoneAt = time.Now().UTC()
		if !r.Ok || c.Project == "" {
			return
		}
		p := st.Projects[c.Project]
		if p == nil {
			return
		}
		switch pl := r.Payload.(type) {
		case *hostdv1.Result_Create:
			p.GuestIP, p.VsockCID = pl.Create.GuestIp, pl.Create.VsockCid
		case *hostdv1.Result_Start:
			// As the api since I-720: a start reports the guest's
			// address, so a start after a create that failed past the
			// guest's own boot fills in what the create never reported.
			if pl.Start.GuestIp != "" {
				p.GuestIP = pl.Start.GuestIp
			}
			if pl.Start.VsockCid != 0 {
				p.VsockCID = pl.Start.VsockCid
			}
		case *hostdv1.Result_Build:
			p.Closure = pl.Build.SystemClosure
		case *hostdv1.Result_Snapshot:
			p.LastBlob = pl.Snapshot.BlobPath
		case *hostdv1.Result_Stop:
			if pl.Stop.BlobPath != "" {
				p.LastBlob = pl.Stop.BlobPath
			}
		}
	})
	s.mu.Lock()
	ws := s.waiters[r.CommandId]
	delete(s.waiters, r.CommandId)
	s.mu.Unlock()
	for _, w := range ws {
		w <- r
	}
	s.closeLogSubs(r.CommandId)
}

func (s *Server) onEvent(e *hostdv1.Event) {
	_ = s.st.Update(func(st *State) { // see above
		st.Events = append(st.Events, rawProto(e))
		if len(st.Events) > 500 {
			st.Events = st.Events[len(st.Events)-500:]
		}
		if g := e.GetGuestStateChanged(); g != nil {
			if st.Host.Guests == nil {
				st.Host.Guests = map[string]string{}
			}
			st.Host.Guests[g.GuestId] = g.State
		}
	})
	_ = s.send(&hostdv1.ApiMessage{Msg: &hostdv1.ApiMessage_Ack{Ack: &hostdv1.Ack{EventId: e.EventId}}}) // the host re-sends unacked events
}

func (s *Server) onLog(l *hostdv1.BuildLog) {
	dir := filepath.Join(s.st.Dir, LogsDir)
	_ = os.MkdirAll(dir, 0o700) // OpenFile reports the real problem
	f, err := os.OpenFile(filepath.Join(dir, l.CommandId+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = fmt.Fprintln(f, l.Line) // a full disk loses log lines, not the build
		_ = f.Close()
	}
	s.mu.Lock()
	subs := s.logSubs[l.CommandId]
	s.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- l.Line:
		default:
		}
	}
}

func (s *Server) closeLogSubs(id string) {
	s.mu.Lock()
	subs := s.logSubs[id]
	delete(s.logSubs, id)
	s.mu.Unlock()
	for _, ch := range subs {
		close(ch)
	}
}

func (s *Server) send(m *hostdv1.ApiMessage) error {
	s.mu.Lock()
	sess := s.sess
	s.mu.Unlock()
	if sess == nil {
		return errors.New("host not connected")
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	return sess.Send(m)
}

// Submit records and sends a command; the project name ties results to
// the project record.
func (s *Server) Submit(cmd *hostdv1.Command, project string) error {
	if cmd.CommandId == "" {
		cmd.CommandId = newID()
	}
	if err := s.st.Update(func(st *State) {
		st.Commands[cmd.CommandId] = &CommandRecord{CommandID: cmd.CommandId, Kind: guest.Kind(cmd), Project: project, Command: rawProto(cmd), SentAt: time.Now().UTC()}
	}); err != nil {
		return err
	}
	return s.send(&hostdv1.ApiMessage{Msg: &hostdv1.ApiMessage_Command{Command: cmd}})
}

// Wait blocks for a command's result.
func (s *Server) Wait(ctx context.Context, id string) (*hostdv1.Result, error) {
	var stored json.RawMessage
	s.st.View(func(st *State) {
		if c := st.Commands[id]; c != nil {
			stored = c.Result
		}
	})
	if stored != nil {
		r := &hostdv1.Result{}
		return r, protojson.Unmarshal(stored, r)
	}
	ch := make(chan *hostdv1.Result, 1)
	s.mu.Lock()
	s.waiters[id] = append(s.waiters[id], ch)
	s.mu.Unlock()
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// --- control socket ---------------------------------------------------

func (s *Server) serveControl(ctx context.Context) error {
	path := filepath.Join(s.st.Dir, ControlSocket)
	_ = os.Remove(path) // stale socket
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		connected := s.connected
		s.mu.Unlock()
		var out struct {
			Connected bool   `json:"connected"`
			State     *State `json:"state"`
		}
		out.Connected = connected
		s.st.View(func(st *State) {
			cp := *st
			cp.JoinToken = "" // never on the wire twice
			out.State = &cp
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out) // client gone
	})
	mux.HandleFunc("POST /projects", func(w http.ResponseWriter, r *http.Request) {
		var p Project
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.st.Update(func(st *State) { st.Projects[p.Name] = &p }); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /projects/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.st.Update(func(st *State) { delete(st.Projects, r.PathValue("name")) }); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /command", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cmd := &hostdv1.Command{}
		if err := protojson.Unmarshal(body, cmd); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.Submit(cmd, r.URL.Query().Get("project")); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"command_id": cmd.CommandId}) // client gone
	})
	mux.HandleFunc("GET /wait/{id}", func(w http.ResponseWriter, r *http.Request) {
		res, err := s.Wait(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusGatewayTimeout)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rawProto(res)) // client gone
	})
	mux.HandleFunc("GET /logs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/plain")
		if b, err := os.ReadFile(filepath.Join(s.st.Dir, LogsDir, id+".log")); err == nil {
			_, _ = w.Write(b) // client gone
		}
		if r.URL.Query().Get("follow") != "1" {
			return
		}
		var done bool
		s.st.View(func(st *State) {
			if c := st.Commands[id]; c != nil && c.Result != nil {
				done = true
			}
		})
		if done {
			return
		}
		ch := make(chan string, 1024)
		s.mu.Lock()
		s.logSubs[id] = append(s.logSubs[id], ch)
		s.mu.Unlock()
		if flusher != nil {
			flusher.Flush()
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case line, ok := <-ch:
				if !ok {
					return
				}
				_, _ = fmt.Fprintln(w, line) // client gone
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close() // shutting down
		_ = os.Remove(path)
	}()
	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Client is the subcommand side of the control socket.
type Client struct {
	Dir  string
	http *http.Client
}

// NewClient returns a client for the state dir's socket.
func NewClient(dir string) *Client {
	path := filepath.Join(dir, ControlSocket)
	return &Client{Dir: dir, http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}}
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://hostdev"+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hostdev serve is not running in %s: %w", c.Dir, err)
	}
	defer func() { _ = resp.Body.Close() }() // body read below
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, errors.New(strings.TrimSpace(string(b)))
	}
	return b, nil
}

// Status returns the connected flag and the state.
func (c *Client) Status(ctx context.Context) (bool, *State, error) {
	b, err := c.do(ctx, http.MethodGet, "/status", nil)
	if err != nil {
		return false, nil, err
	}
	var out struct {
		Connected bool   `json:"connected"`
		State     *State `json:"state"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return false, nil, err
	}
	return out.Connected, out.State, nil
}

// PutProject upserts a project record.
func (c *Client) PutProject(ctx context.Context, p *Project) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPost, "/projects", strings.NewReader(string(b)))
	return err
}

// DeleteProject removes a project record.
func (c *Client) DeleteProject(ctx context.Context, name string) error {
	_, err := c.do(ctx, http.MethodDelete, "/projects/"+name, nil)
	return err
}

// Submit sends a command and returns its id.
func (c *Client) Submit(ctx context.Context, cmd *hostdv1.Command, project string) (string, error) {
	b, err := pj.Marshal(cmd)
	if err != nil {
		return "", err
	}
	out, err := c.do(ctx, http.MethodPost, "/command?project="+project, strings.NewReader(string(b)))
	if err != nil {
		return "", err
	}
	var r struct {
		CommandID string `json:"command_id"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", err
	}
	return r.CommandID, nil
}

// Wait blocks for a result.
func (c *Client) Wait(ctx context.Context, id string) (*hostdv1.Result, error) {
	b, err := c.do(ctx, http.MethodGet, "/wait/"+id, nil)
	if err != nil {
		return nil, err
	}
	r := &hostdv1.Result{}
	return r, protojson.Unmarshal(b, r)
}

// Logs streams build log lines to w until the command finishes.
func (c *Client) Logs(ctx context.Context, id string, follow bool, w io.Writer) error {
	q := ""
	if follow {
		q = "?follow=1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://hostdev/logs/"+id+q, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // body read below
	_, err = io.Copy(w, resp.Body)
	return err
}
