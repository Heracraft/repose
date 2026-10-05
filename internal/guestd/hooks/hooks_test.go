package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func quietLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

type sinkRecorder struct {
	mu   sync.Mutex
	rows []string
}

func (s *sinkRecorder) sink(agent, window, kind, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, strings.Join([]string{agent, window, kind, summary}, "|"))
}

func (s *sinkRecorder) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.rows...)
}

func newHookServer(t *testing.T, resolve WindowResolver) (*Server, *sinkRecorder, *http.Client) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hooks.sock")
	rec := &sinkRecorder{}
	// The test process is not root, so it cannot chown the socket to the dev
	// group; the mode and ownership are asserted in the NixOS VM test.
	s := NewServer(path, -1, rec.sink, resolve, quietLog())
	if err := s.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = s.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Close(ctx)
	})

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		},
	}
	return s, rec, client
}

func post(t *testing.T, c *http.Client, body string) *http.Response {
	t.Helper()
	resp, err := c.Post("http://guestd/", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestHookAccepted(t *testing.T) {
	_, rec, c := newHookServer(t, nil)
	resp := post(t, c, `{"agent":"claude","kind":"completed","summary":"done","window":"claude"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if got := rec.all(); len(got) != 1 || got[0] != "claude|claude|completed|done" {
		t.Fatalf("relayed = %v", got)
	}
}

func TestHookSocketModeIs0660(t *testing.T) {
	s, _, _ := newHookServer(t, nil)
	fi, err := os.Stat(s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o660 {
		t.Fatalf("mode = %o, want 660 so only the dev group may post", fi.Mode().Perm())
	}
}

func TestHookRejections(t *testing.T) {
	_, rec, c := newHookServer(t, nil)
	cases := map[string]string{
		"malformed json": `{"agent":`,
		"unknown agent":  `{"agent":"aider","kind":"completed"}`,
		"unknown kind":   `{"agent":"claude","kind":"thinking"}`,
	}
	for name, body := range cases {
		resp := post(t, c, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, resp.StatusCode)
		}
	}
	if got := rec.all(); len(got) != 0 {
		t.Fatalf("a rejected hook was relayed: %v", got)
	}
}

func TestHookMethodNotAllowed(t *testing.T) {
	_, _, c := newHookServer(t, nil)
	resp, err := c.Get("http://guestd/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestSummaryIsTruncatedAtTheCap(t *testing.T) {
	_, rec, c := newHookServer(t, nil)
	body, err := json.Marshal(Payload{
		Agent: "codex", Kind: "completed", Window: "codex",
		Summary: strings.Repeat("x", SummaryCap+500),
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := post(t, c, string(body))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	parts := strings.SplitN(rec.all()[0], "|", 4)
	if len(parts[3]) != SummaryCap {
		t.Fatalf("summary length = %d, want %d", len(parts[3]), SummaryCap)
	}
}

func TestWindowIsEmptyOutsideTmux(t *testing.T) {
	// No window in the payload, and the resolver cannot find one (a herdr
	// pane, a script): the event still reaches the sink, because a
	// notification the user does not get is worse than one with a less
	// precise window, and the window stays empty so the sink does not take
	// the agent's name for a tmux window.
	_, rec, c := newHookServer(t, func(context.Context, string) (string, error) {
		return "", os.ErrNotExist
	})
	post(t, c, `{"agent":"pi","kind":"needs_input","summary":"?"}`)
	got := rec.all()
	if len(got) != 1 || !strings.HasPrefix(got[0], "pi||needs_input|") {
		t.Fatalf("relayed = %v", got)
	}
}

// TestHelperPost is not a test. It is the child process
// TestWindowIsResolvedFromTheCallersPane runs so that the peer of the socket
// is a process with a TMUX_PANE this test chose, which is the only way to
// exercise the SO_PEERCRED path deterministically.
func TestHelperPost(t *testing.T) {
	if os.Getenv("REPOSE_HOOK_HELPER") != "1" {
		t.Skip("helper process")
	}
	socket := os.Getenv("REPOSE_HOOK_SOCKET")
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}
	resp, err := client.Post("http://guestd/", "application/json",
		strings.NewReader(`{"agent":"claude","kind":"completed","summary":"ok"}`))
	if err != nil {
		t.Fatalf("helper post: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // helper
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("helper status = %d", resp.StatusCode)
	}
}

func TestWindowIsResolvedFromTheCallersPane(t *testing.T) {
	var (
		mu      sync.Mutex
		gotPane string
	)
	s, rec, _ := newHookServer(t, func(_ context.Context, pane string) (string, error) {
		mu.Lock()
		gotPane = pane
		mu.Unlock()
		return "claude-2", nil
	})

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperPost$", "-test.v")
	cmd.Env = append(os.Environ(),
		"REPOSE_HOOK_HELPER=1",
		"REPOSE_HOOK_SOCKET="+s.Addr(),
		"TMUX_PANE=%7",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper process: %v\n%s", err, out)
	}

	mu.Lock()
	pane := gotPane
	mu.Unlock()
	if pane != "%7" {
		t.Fatalf("pane = %q, want %%7 read from the caller's own environment through SO_PEERCRED", pane)
	}
	got := rec.all()
	if len(got) != 1 || !strings.HasPrefix(got[0], "claude|claude-2|") {
		t.Fatalf("relayed = %v, want the resolved window", got)
	}
}

func TestOversizedBodyIsRejected(t *testing.T) {
	_, rec, c := newHookServer(t, nil)
	var b bytes.Buffer
	b.WriteString(`{"agent":"claude","kind":"completed","summary":"`)
	b.WriteString(strings.Repeat("x", 128<<10))
	b.WriteString(`"}`)
	resp, err := c.Post("http://guestd/", "application/json", &b)
	if err == nil {
		defer resp.Body.Close() //nolint:errcheck // test
		if resp.StatusCode == http.StatusNoContent {
			t.Fatal("a body larger than the reader's limit was accepted")
		}
	}
	if got := rec.all(); len(got) != 0 {
		t.Fatalf("an oversized hook was relayed: %v", got)
	}
}

func TestCloseRemovesTheSocket(t *testing.T) {
	s, _, _ := newHookServer(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(s.Addr()); !os.IsNotExist(err) {
		t.Fatal("the socket was left behind, so a restart would fail to bind")
	}
}
