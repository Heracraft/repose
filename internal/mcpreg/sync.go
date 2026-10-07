package mcpreg

import (
	"fmt"
	"io"
	"os"
	"time"
)

// syncLockWait bounds the wait for another sync (agents start in parallel,
// and hold syncs too).
var syncLockWait = 10 * time.Second

// KnownAgent reports whether sync renders for agent.
func KnownAgent(agent string) bool {
	for _, a := range Agents {
		if a == agent {
			return true
		}
	}
	return false
}

// Sync renders the registry into each agent's config, under
// ~/.repose/mcp/.lock. Problems are lines on stderr; nothing here fails the
// caller, since agent-setup runs it before every agent start.
func Sync(p Paths, agents []string, stderr io.Writer) {
	warn := func(s string) { _, _ = fmt.Fprintln(stderr, "repose-mcp:", s) }
	if err := os.MkdirAll(p.Dir(), 0o700); err != nil {
		warn("cannot make ~/.repose/mcp: " + err.Error())
		return
	}
	unlock, err := flockFile(p.Dir()+"/.lock", syncLockWait)
	if err != nil {
		warn("another repose-mcp sync held ~/.repose/mcp/.lock; MCP servers not updated")
		return
	}
	defer unlock()

	reg := Load(p)
	for _, pr := range reg.Problems {
		warn(pr)
	}
	rendered, rerr := readRendered(p)
	if rerr != "" {
		warn(rerr)
	}
	for _, agent := range agents {
		if !KnownAgent(agent) {
			warn("unknown agent " + agent)
			continue
		}
		want := reg.Render(agent)
		prev := rendered.Agents[agent]
		if prev == nil {
			prev = &agentRecord{}
		}
		var rec *agentRecord
		switch agent {
		case "claude":
			rec = applyClaude(p, want, prev, warn)
		case "codex":
			rec = applyCodex(p, want, prev, warn)
		case "gemini":
			rec = applyGemini(p, want, prev, warn)
		case "opencode":
			rec = applyOpencode(p, want, prev, warn)
		case "pi":
			rec = applyPi(p, want, prev, warn)
		}
		rendered.Agents[agent] = rec
		view := map[string]any{"mcpServers": want.User}
		if len(want.Projects) > 0 {
			view["projects"] = want.Projects
		}
		if len(want.Skipped) > 0 {
			view["skipped"] = want.Skipped
		}
		if err := writeJSONFile(p.agentFile(agent), view); err != nil {
			warn("cannot write ~/.repose/mcp/agents/" + agent + ".json: " + err.Error())
		}
	}
	if err := writeJSONFile(p.renderedFile(), rendered); err != nil {
		warn("cannot write ~/.repose/mcp/rendered.json: " + err.Error())
	}
}
