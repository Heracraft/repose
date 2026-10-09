package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"time"
)

// SessionOption is the tmux pane option a hook sets to the agent's own
// conversation id (DECISIONS I-636, guest-conventions.md "tmux"). At a
// stop, repose-tmux-save records it with the window, and the next start
// reopens the window on that conversation (`claude --resume ID`,
// `codex resume ID`), so two agents in one folder each get their own.
const SessionOption = "@repose-session"

// sessionIDShape is what an agent's conversation id looks like (a UUID,
// or Codex's thread id); anything else is not set.
var sessionIDShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// sessionIDOf is the conversation id in an agent's hook payload: Claude
// Code's session_id, Codex's thread-id. "" when the payload has none.
func sessionIDOf(agent string, payload []byte) string {
	var m map[string]any
	if json.Unmarshal(payload, &m) != nil {
		return ""
	}
	var id string
	switch agent {
	case "claude":
		id, _ = m["session_id"].(string)
	case "codex":
		id, _ = m["thread-id"].(string)
	}
	if !sessionIDShape.MatchString(id) {
		return ""
	}
	return id
}

// setSessionOption is the tmux call; tests replace it.
var setSessionOption = func(pane, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "tmux", "set-option", "-p", "-t", pane, SessionOption, id).Run()
}

// recordSession sets SessionOption on the hook's tmux pane. Best effort,
// and nothing outside tmux: herdr resumes its own agents.
func recordSession(agent string, payload []byte) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return
	}
	if id := sessionIDOf(agent, payload); id != "" {
		setSessionOption(pane, id)
	}
}
