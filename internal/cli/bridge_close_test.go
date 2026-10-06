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
	want := `if tmux list-sessions >/dev/null 2>&1; then if tmux list-clients -t '=todo-app' -F x 2>/dev/null | grep -q .; then tmux display-message -d 4000 -t '=todo-app:' '50% ##1 done'; fi; elif [ -S /home/dev/.config/herdr/herdr.sock ]; then herdr notification show repose --body '50% #1 done' >/dev/null 2>&1 || true; fi`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
