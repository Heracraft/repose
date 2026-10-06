package sample

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/multiplexer"
	"golang.org/x/sys/unix"
)

// herdr's socket, as guestd uses it (DECISIONS I-504; guest-conventions
// "herdr", Socket use by guestd). herdr answers one request per
// connection, so every request is a dial, one line out, one line back.
const (
	// HerdrReplyCap caps one answer; a longer line is dropped unread.
	HerdrReplyCap = 1 << 20
	// herdrDialTimeout and herdrRequestTimeout bound one request, so a
	// wedged server costs a refresh at most this much.
	herdrDialTimeout    = time.Second
	herdrRequestTimeout = 2 * time.Second
	// HerdrGraceRefreshes is how many failed refreshes keep the last
	// panes before they go unknown: a herdr restart that resumes its
	// agents shows no flap.
	HerdrGraceRefreshes = 2
	// HerdrDownAfter is how many failed refreshes in a row make
	// herdr_down on a herdr project (I-507).
	HerdrDownAfter = 2
	// herdrLabelsEvery is how often workspace labels are read when no new
	// workspace appeared.
	herdrLabelsEvery = time.Minute
)

// herdr failure reasons: bounded codes, never herdr's own message.
var (
	errHerdrPeer     = errors.New("peer_uid")
	errHerdrTooLarge = errors.New("reply_too_large")
	errHerdrProtocol = errors.New("protocol_too_old")
	errHerdrReply    = errors.New("bad_reply")
)

// herdrAgent is the decoder for one agent.list entry. It holds exactly
// these seven fields; encoding/json drops every other one unread (titles,
// cwd, the agent session among them, R5-3).
type herdrAgent struct {
	PaneID         string `json:"pane_id"`
	WorkspaceID    string `json:"workspace_id"`
	Name           string `json:"name"`
	Agent          string `json:"agent"`
	AgentStatus    string `json:"agent_status"`
	StateChangeSeq uint64 `json:"state_change_seq"`
	// CompletionSeq is the sequence number of the change that finished
	// the agent's last turn, absent while the current state is no
	// finished turn. herdr leaves it unset when an agent first comes up
	// idle (DECISIONS I-561).
	CompletionSeq *uint64 `json:"completion_seq"`
}

// herdrWorkspace is the decoder for one workspace.list entry: id and label
// only.
type herdrWorkspace struct {
	ID    string `json:"workspace_id"`
	Label string `json:"label"`
}

// herdrReply is the envelope of every answer guestd reads.
type herdrReply struct {
	Result *struct {
		Type       string           `json:"type"`
		Version    string           `json:"version"`
		Protocol   int              `json:"protocol"`
		Agents     []herdrAgent     `json:"agents"`
		Workspaces []herdrWorkspace `json:"workspaces"`
	} `json:"result"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

// herdrStates maps herdr's agent_status to the AgentProc state.
var herdrStates = map[string]string{
	"working": StateWorking,
	"blocked": StateNeedsInput,
	"idle":    StateIdle,
	"done":    StateIdle,
	"unknown": StateUnknown,
}

// herdrSource reads herdr's agents from its socket. It forks nothing (I-31)
// and calls only ping, agent.list and workspace.list: never
// session.snapshot, pane.process_info, pane.read or a method that changes
// herdr's state.
type herdrSource struct {
	paths     sysdep.Paths
	socket    string
	uid       int // the peer uid the socket must belong to: dev's
	log       *slog.Logger
	now       func() time.Time
	checkouts func() map[string]bool

	// mu guards everything below. Panes holds it across its requests, so
	// Resolve, which a hook waits on, takes it with lockCtx and gives up
	// when the hook's deadline passes.
	mu ctxMutex
	n  uint64 // request counter for ids
	// pinged is true after a ping answered with a protocol guestd reads;
	// any failure clears it, so the next dial pings first.
	pinged bool
	// misses counts refreshes in a row that failed.
	misses int
	// last is the previous successful read, kept for the EOF grace.
	last []Pane
	// seq is each pane's state_change_seq at the previous read, and done
	// its completion_seq (0 when absent). herdr counts both per server
	// process from 0, so a read after a failure (a restart, maybe) only
	// records them.
	seq  map[string]uint64
	done map[string]uint64
	// labels is the workspace id -> label map, read at labelsAt.
	labels   map[string]string
	labelsAt time.Time
	// asked is the workspace ids the last workspace.list was asked about
	// (in its answer, or named by an agent then), answered or not.
	asked map[string]bool
	// keys is pane id -> key from the previous read, for Resolve.
	keys map[string]string
	// lastErr is the reason of the last failure, logged on change only.
	lastErr string
}

func newHerdrSource(p sysdep.Paths, uid int, log *slog.Logger, now func() time.Time) *herdrSource {
	h := &herdrSource{
		paths: p, socket: p.HerdrSock(), uid: uid, log: log, now: now,
		mu:  newCtxMutex(),
		seq: map[string]uint64{}, done: map[string]uint64{}, keys: map[string]string{},
	}
	h.checkouts = h.readCheckouts
	return h
}

func (h *herdrSource) Name() string { return multiplexer.Herdr }

func (h *herdrSource) Close() {}

// Misses is the number of refreshes in a row that could not read herdr.
func (h *herdrSource) Misses() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.misses
}

// Panes reads agent.list. A missing socket on a machine that never had
// herdr costs one stat and reports down with no panes. After a failure the
// previous panes stand for HerdrGraceRefreshes refreshes.
func (h *herdrSource) Panes(ctx context.Context) ([]Pane, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agents, err := h.readAgents(ctx)
	// After a failed read the server may be a new process whose sequence
	// started again at 0: this read is a baseline and marks nothing done.
	baseline := h.misses > 0
	if err != nil {
		h.misses++
		h.pinged = false
		h.noteErr(err)
		if h.last != nil && h.misses <= HerdrGraceRefreshes {
			kept := make([]Pane, len(h.last))
			for i, p := range h.last {
				p.Done = false
				kept[i] = p
			}
			return kept, false, nil
		}
		h.last, h.keys = nil, map[string]string{}
		h.seq, h.done = map[string]uint64{}, map[string]uint64{}
		return nil, false, nil
	}
	h.misses = 0
	h.noteErr(nil)
	h.maybeReadLabels(ctx, agents)
	checkouts := h.checkouts()

	panes := make([]Pane, 0, len(agents))
	seq := make(map[string]uint64, len(agents))
	doneSeq := make(map[string]uint64, len(agents))
	keys := make(map[string]string, len(agents))
	for _, a := range agents {
		if !isAgentName(a.Agent) || a.PaneID == "" {
			continue
		}
		state, ok := herdrStates[a.AgentStatus]
		if !ok {
			state = StateUnknown
		}
		prev, seen := h.seq[a.PaneID]
		completion := uint64(0)
		if a.CompletionSeq != nil {
			completion = *a.CompletionSeq
		}
		// A turn finished when completion_seq rose, even one shorter than
		// a refresh. herdr sets it only for idle reached from working or
		// blocked, so an agent coming up idle finishes nothing. Within one
		// server the sequence only grows; a lower one is a restart between
		// two refreshes.
		done := seen && !baseline && a.StateChangeSeq >= prev && completion > h.done[a.PaneID]
		seq[a.PaneID] = a.StateChangeSeq
		doneSeq[a.PaneID] = completion
		key := h.keyOf(a, checkouts)
		keys[a.PaneID] = key
		panes = append(panes, Pane{Key: key, Agent: a.Agent, Ref: a.PaneID, Reported: state, Done: done})
	}
	h.seq, h.done, h.keys = seq, doneSeq, keys
	h.last = make([]Pane, len(panes))
	copy(h.last, panes)
	return panes, true, nil
}

// Resolve maps a herdr pane id to the key its agent is reported under. A
// pane the last read did not see is looked up with one more agent.list (a
// hook can arrive before the refresh that finds a new agent); that read
// leaves the completion bookkeeping alone.
func (h *herdrSource) Resolve(ctx context.Context, ref string) (string, bool) {
	if !ValidHerdrRef(ref) {
		return "", false
	}
	if !h.mu.lockCtx(ctx) {
		// A refresh holds the lock on a slow herdr: the hook goes
		// unresolved rather than past repose-hook's timeout.
		return "", false
	}
	defer h.mu.Unlock()
	if k, ok := h.keys[ref]; ok {
		return k, true
	}
	agents, err := h.readAgents(ctx)
	if err != nil {
		return "", false
	}
	checkouts := h.checkouts()
	for _, a := range agents {
		if a.PaneID == ref && isAgentName(a.Agent) {
			return h.keyOf(a, checkouts), true
		}
	}
	return "", false
}

// keyOf is guest-conventions "herdr", Agent keys: the name when set, else
// "<agent> <pane_id>", prefixed "<checkout>/" when the workspace's label is
// one of the machine's other checkouts, at most MaxKey bytes.
func (h *herdrSource) keyOf(a herdrAgent, checkouts map[string]bool) string {
	key := a.Name
	if key == "" {
		key = a.Agent + " " + a.PaneID
	}
	if label := h.labels[a.WorkspaceID]; label != "" && checkouts[label] {
		key = label + "/" + key
	}
	return capKey(key, multiplexer.MaxKey)
}

func isAgentName(s string) bool {
	for _, a := range Agents {
		if s == a {
			return true
		}
	}
	return false
}

// readAgents pings first when the last request failed (or none was made),
// then asks agent.list.
func (h *herdrSource) readAgents(ctx context.Context) ([]herdrAgent, error) {
	if _, err := os.Stat(h.socket); err != nil {
		return nil, errHerdrNoSocket
	}
	if !h.pinged {
		r, err := h.request(ctx, "ping")
		if err != nil {
			return nil, err
		}
		if r.Result.Type != "pong" {
			return nil, errHerdrReply
		}
		if r.Result.Protocol < multiplexer.HerdrMinProtocol {
			return nil, errHerdrProtocol
		}
		h.pinged = true
	}
	r, err := h.request(ctx, "agent.list")
	if err != nil {
		return nil, err
	}
	if r.Result.Type != "agent_list" {
		return nil, errHerdrReply
	}
	return r.Result.Agents, nil
}

// errHerdrNoSocket is the reason when there is no socket: herdr is not running.
var errHerdrNoSocket = errors.New("no_socket")

// maybeReadLabels refreshes workspace labels at most once a minute, or at
// once when an agent names a workspace the last read was not asked about.
// A workspace still missing after that read, or a failed read, waits for
// the minute. A machine with no other checkouts needs no labels and makes
// no request.
func (h *herdrSource) maybeReadLabels(ctx context.Context, agents []herdrAgent) {
	if len(h.checkouts()) == 0 {
		h.labels, h.asked = nil, nil
		return
	}
	stale := h.labels == nil || h.now().Sub(h.labelsAt) >= herdrLabelsEvery
	if !stale {
		for _, a := range agents {
			if a.WorkspaceID != "" && !h.asked[a.WorkspaceID] {
				stale = true
				break
			}
		}
	}
	if !stale {
		return
	}
	r, err := h.request(ctx, "workspace.list")
	h.labelsAt = h.now()
	h.asked = make(map[string]bool, len(agents))
	for _, a := range agents {
		h.asked[a.WorkspaceID] = true
	}
	if err != nil || r.Result.Type != "workspace_list" {
		// Keys go without the prefix until the next read; the agents
		// themselves are still reported.
		if h.labels == nil {
			h.labels = map[string]string{}
		}
		return
	}
	labels := make(map[string]string, len(r.Result.Workspaces))
	for _, w := range r.Result.Workspaces {
		labels[w.ID] = w.Label
		h.asked[w.ID] = true
	}
	h.labels = labels
}

// readCheckouts reads ~/.repose/checkouts: names of directories of the
// home, one per line. A line that is not such a name is skipped.
func (h *herdrSource) readCheckouts() map[string]bool {
	b, err := os.ReadFile(h.paths.CheckoutsFile())
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\\x00") || len(name) > 255 {
			continue
		}
		out[name] = true
	}
	return out
}

// request makes one connection, checks the peer is dev, writes one request
// line and reads one answer line of at most HerdrReplyCap bytes.
func (h *herdrSource) request(ctx context.Context, method string) (*herdrReply, error) {
	ctx, cancel := context.WithTimeout(ctx, herdrRequestTimeout)
	defer cancel()
	d := net.Dialer{Timeout: herdrDialTimeout}
	c, err := d.DialContext(ctx, "unix", h.socket)
	if err != nil {
		return nil, errors.New("dial")
	}
	defer c.Close() //nolint:errcheck // one request per connection
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	uid, ok := peerUID(c)
	if !ok || uid != h.uid {
		// guestd is root and the path is dev's to replace: a socket
		// someone else listens on gets nothing written to it.
		return nil, errHerdrPeer
	}
	h.n++
	req := fmt.Sprintf(`{"id":"g%d","method":%q,"params":{}}`+"\n", h.n, method)
	if _, err := io.WriteString(c, req); err != nil {
		return nil, errors.New("write")
	}
	line, err := readLine(c, HerdrReplyCap)
	if err != nil {
		return nil, err
	}
	var r herdrReply
	if err := json.Unmarshal(line, &r); err != nil {
		return nil, errHerdrReply
	}
	if r.Error != nil || r.Result == nil {
		return nil, errHerdrReply
	}
	return &r, nil
}

// readLine reads up to the first newline, failing when none comes within
// max bytes. What was read past the cap is never decoded.
func readLine(r io.Reader, max int) ([]byte, error) {
	br := bufio.NewReaderSize(io.LimitReader(r, int64(max)+1), 64<<10)
	var buf bytes.Buffer
	for {
		chunk, err := br.ReadSlice('\n')
		buf.Write(chunk)
		if buf.Len() > max {
			return nil, errHerdrTooLarge
		}
		if err == nil {
			return buf.Bytes(), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if buf.Len() > max {
				return nil, errHerdrTooLarge
			}
			return nil, errors.New("eof")
		}
		return nil, errors.New("read")
	}
}

// peerUID reads SO_PEERCRED's uid.
func peerUID(c net.Conn) (int, bool) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, false
	}
	uid, got := 0, false
	_ = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err == nil {
			uid, got = int(cred.Uid), true
		}
	})
	return uid, got
}

// noteErr logs a change of failure reason: a bounded code, never herdr's
// message. A missing socket is the normal state of a tmux machine and is
// not logged.
func (h *herdrSource) noteErr(err error) {
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	if reason == h.lastErr {
		return
	}
	h.lastErr = reason
	switch reason {
	case "", errHerdrNoSocket.Error():
		return
	}
	h.log.Warn("could not read herdr's agents", "event", "agent_state", "reason", reason)
}

// ctxMutex is a mutex whose Lock can give up when a context ends.
type ctxMutex chan struct{}

func newCtxMutex() ctxMutex { return make(ctxMutex, 1) }

func (m ctxMutex) Lock()   { m <- struct{}{} }
func (m ctxMutex) Unlock() { <-m }

// lockCtx takes the lock, or returns false when ctx ends first.
func (m ctxMutex) lockCtx(ctx context.Context) bool {
	select {
	case m <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}
