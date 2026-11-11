package cli

import (
	"strings"
	"testing"
	"time"
)

// The close path after Ctrl-C returns promptly when nobody is attached
// (I-313): one ssh that switches the endpoint back and shows the tmux
// line only to a client that is there. It used to poll list-clients for
// 8 s after "Bridge closed" was printed.
func TestBridgeCloseReturnsPromptly(t *testing.T) {
	target, log := bridgeGuest(t)
	start := time.Now()
	bridgeStop(target, "todo-app", bridgeTmuxLine(false))
	took := time.Since(start)
	t.Logf("cancel-to-return (close path): %s", took.Round(time.Millisecond))
	if took > 3*time.Second {
		t.Errorf("the close path took %s", took)
	}
	if l := readLog(t, log); !strings.Contains(l, "browser bridge stop") {
		t.Errorf("calls: %q", l)
	}
}

func TestTmuxIfAttached(t *testing.T) {
	got := tmuxIfAttached("todo-app", "50% #1 done")
	// I-1067: the tmux branch shows the message on every terminal attached
	// to the session or a session grouped with it, by client name.
	want := `if systemctl --user -q is-active repose-herdr-server.service 2>/dev/null; then herdr notification show repose --body '50% #1 done' >/dev/null 2>&1 || true; else for repose_c in $(tmux list-clients -F '#{client_name} #{session_name} #{session_group}' 2>/dev/null | awk -v s='todo-app' '$2 == s || $3 == s { print $1 }'); do tmux display-message -c "$repose_c" -d 4000 '50% ##1 done'; done; fi`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
