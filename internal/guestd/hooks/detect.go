package hooks

import (
	"encoding/json"
	"strings"
)

// Detect names the agent a hook payload came from when nothing else
// does: "claude", "codex", or "" when the payload does not say. The
// wrappers on the PATH export REPOSE_HOOK_AGENT, but an app that installs
// and runs its own copy of an agent over SSH bypasses them: Claude
// Desktop's Claude Code under ~/.claude/remote reads the same
// ~/.claude/settings.json and runs the same `repose-hook`, with no
// variable set (DECISIONS I-1608). A payload that could be either agent's
// is "", so it is never told as the wrong one.
func Detect(payload []byte) string {
	var h struct {
		Type           string `json:"type"`
		HookEventName  string `json:"hook_event_name"`
		TranscriptPath string `json:"transcript_path"`
	}
	if json.Unmarshal(payload, &h) != nil {
		return ""
	}
	if h.Type == "agent-turn-complete" {
		return "codex"
	}
	// Each agent keeps its transcripts in its own folder.
	switch {
	case strings.Contains(h.TranscriptPath, "/.claude/"):
		return "claude"
	case strings.Contains(h.TranscriptPath, "/.codex/"):
		return "codex"
	}
	switch h.HookEventName {
	case "Notification", "StopFailure", "SubagentStop":
		// Names only Claude Code sends; Codex has none of them.
		return "claude"
	}
	return ""
}
