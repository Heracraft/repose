package mcpreg

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// opencodeConfig is the reader's opencode file sync writes, under home.
func opencodeConfig(home string) string {
	return home + "/.config/opencode/opencode.json"
}

// restartOpencode restarts opencode 2's background service when one is
// running, after sync changed ~/.config/opencode/opencode.json (DECISIONS
// I-1050). The service is one per machine and holds the config it started
// with, so without this a server repose rendered after it started -- a
// forwarded one, or one the laptop added -- reaches no opencode session,
// new or old, until the user finds `opencode service restart`. Sessions
// live in opencode.db and the TUI reconnects, so a restart costs a running
// agent the turn in flight and nothing else. A machine without opencode on
// PATH, or with its service stopped, is left as it is.
var restartOpencode = func(warn func(string)) {
	bin, err := exec.LookPath("opencode")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "service", "status").Output()
	if err != nil || strings.TrimSpace(string(out)) == "stopped" {
		return
	}
	if err := exec.CommandContext(ctx, bin, "service", "restart").Run(); err != nil {
		warn("opencode service restart failed; a running opencode sees the new MCP servers after `opencode service restart`: " + err.Error())
	}
}
