package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// ErrNoEvent means the payload is a hook this agent fires that repose does not
// report. repose-hook exits 0 on it: a hook that fails must never block an
// agent, and neither must a hook that has nothing to say.
var ErrNoEvent = errors.New("hooks: payload carries no reportable event")

// TranscriptTail is how much of a Claude Code transcript is read to find the
// last assistant line (docs/workstreams/04-guestd.md §5).
const TranscriptTail = 4 << 10

// SummaryChars is how much of that line is kept.
const SummaryChars = 200

// Map turns one agent's native hook payload into the {agent, kind, summary}
// that the hook socket accepts. agent must be one of the five; the payload
// shapes are recorded in testdata beside this file and in features/agents.md.
func Map(agent string, payload []byte) (Payload, error) {
	if !agents[agent] {
		return Payload{}, fmt.Errorf("hooks: %q is not a known agent", agent)
	}
	// A wrapper may already speak the socket's own shape.
	if p, ok := asNative(agent, payload); ok {
		return p, nil
	}
	switch agent {
	case "claude":
		return mapClaude(payload)
	case "codex":
		return mapCodex(agent, payload)
	case "opencode":
		return mapOpencode(agent, payload)
	case "gemini":
		return mapGemini(payload)
	case "pi":
		// The platform's pi extension posts the socket's own shape
		// (asNative above); nothing else is an event.
		return Payload{}, ErrNoEvent
	}
	return Payload{}, ErrNoEvent
}

// asNative accepts the socket's own shape, so a wrapper that can emit it
// directly needs no mapping at all.
func asNative(agent string, payload []byte) (Payload, bool) {
	var p Payload
	if err := json.Unmarshal(payload, &p); err != nil {
		return Payload{}, false
	}
	if p.Kind == "" || !kinds[p.Kind] {
		return Payload{}, false
	}
	if p.Agent == "" {
		p.Agent = agent
	}
	p.Summary = truncate(p.Summary, SummaryCap)
	return p, true
}

// claudeHook is the part of Claude Code's hook payload repose reads.
// UserPromptSubmit's prompt is kept undecoded, and only its first bytes
// are compared with Claude Code's own task-notification marker; it is
// never stored, logged or sent.
type claudeHook struct {
	Prompt           json.RawMessage `json:"prompt"`
	HookEventName    string          `json:"hook_event_name"`
	TranscriptPath   string          `json:"transcript_path"`
	Message          string          `json:"message"`
	NotificationType string          `json:"notification_type"`
	Error            string          `json:"error"`
	// Stop, Claude Code 2.1.x (DECISIONS I-688; fixtures captured from
	// 2.1.283): the final assistant text and the background tasks still
	// running.
	LastAssistantMessage string `json:"last_assistant_message"`
	BackgroundTasks      []struct {
		Status string `json:"status"`
	} `json:"background_tasks"`
}

// paused reports whether a Stop is a pause rather than the end of the
// turn: a background task is still running or pending, after which the
// agent takes another turn by itself. A recurring cron (session_crons) is
// no pause: between its runs the agent waits at its prompt.
// stop_hook_active is no pause either: Claude Code sets it on the Stop
// that ends a turn a Stop hook forced on, and repose's hook cannot know
// whether another hook will block this Stop (DECISIONS I-688, Built).
func (h claudeHook) paused() bool {
	for _, t := range h.BackgroundTasks {
		switch t.Status {
		case "running", "pending", "":
			return true
		}
	}
	return false
}

// needsInputNotifications are the notification types that mean the agent is
// waiting for the user (docs/workstreams/04-guestd.md §5). idle_prompt is
// not one: Claude Code sends it a minute after every turn that ends, so it
// turned each "finished" into a second "needs input" notification and left
// a finished agent showing needs_input (I-418).
var needsInputNotifications = map[string]bool{
	"permission_prompt": true,
	"agent_needs_input": true,
}

func mapClaude(payload []byte) (Payload, error) {
	var h claudeHook
	if err := json.Unmarshal(payload, &h); err != nil {
		return Payload{}, fmt.Errorf("hooks: parse claude payload: %w", err)
	}
	switch h.HookEventName {
	case "UserPromptSubmit":
		if bytes.HasPrefix(h.Prompt, []byte(`"<task-notification>`)) {
			// The turn a finished background task starts (2.1.283):
			// nobody typed it, so it closes no question; the agent works
			// again.
			return Payload{Agent: "claude", Kind: KindResume}, nil
		}
		return Payload{Agent: "claude", Kind: KindPrompt}, nil
	case "Stop":
		if h.paused() {
			return Payload{}, ErrNoEvent
		}
		// The payload's last assistant message, else the last assistant
		// line from the transcript tail (older builds), else the
		// fallback guest-conventions.md names (an empty summary reached
		// the api from the first real Stop on host-01, I-121).
		summary := clip(h.LastAssistantMessage)
		if summary == "" {
			summary = lastAssistantLine(h.TranscriptPath)
		}
		if summary == "" {
			summary = "claude finished"
		}
		return Payload{Agent: "claude", Kind: "completed", Summary: summary}, nil
	case "Notification":
		// Older builds carry only `message`; classify from it when the
		// explicit type is absent, so the wrapper works on both.
		nt := h.NotificationType
		if nt == "" {
			nt = classifyClaudeMessage(h.Message)
		}
		if !needsInputNotifications[nt] {
			return Payload{}, ErrNoEvent
		}
		return Payload{Agent: "claude", Kind: "needs_input", Summary: truncate(h.Message, SummaryCap)}, nil
	case "StopFailure":
		summary := h.Error
		if summary == "" {
			summary = h.Message
		}
		return Payload{Agent: "claude", Kind: "error", Summary: truncate(summary, SummaryCap)}, nil
	default:
		return Payload{}, ErrNoEvent
	}
}

// classifyClaudeMessage recognises the two notification messages Claude Code
// sends without a type: a permission request and an idle wait.
func classifyClaudeMessage(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "permission"):
		return "permission_prompt"
	case strings.Contains(lower, "waiting for your input"), strings.Contains(lower, "is idle"):
		return "idle_prompt"
	default:
		return ""
	}
}

// codexHook is Codex CLI's notify argument, or the stdin of a hook from
// its hooks table (UserPromptSubmit, I-690), whose prompt is not decoded.
type codexHook struct {
	Type                 string `json:"type"`
	LastAssistantMessage string `json:"last-assistant-message"`
	HookEventName        string `json:"hook_event_name"`
}

func mapCodex(agent string, payload []byte) (Payload, error) {
	var h codexHook
	if err := json.Unmarshal(payload, &h); err != nil {
		return Payload{}, fmt.Errorf("hooks: parse codex payload: %w", err)
	}
	if h.HookEventName == "UserPromptSubmit" {
		return Payload{Agent: agent, Kind: KindPrompt}, nil
	}
	switch h.Type {
	case "agent-turn-complete":
		return Payload{Agent: agent, Kind: "completed", Summary: clip(h.LastAssistantMessage)}, nil
	default:
		return Payload{}, ErrNoEvent
	}
}

// geminiHook is the part of a Gemini CLI hook payload repose reads
// (0.61.0, from the platform extension's hooks/hooks.json, I-690). The
// prompt is not decoded.
type geminiHook struct {
	HookEventName  string `json:"hook_event_name"`
	PromptResponse string `json:"prompt_response"`
}

func mapGemini(payload []byte) (Payload, error) {
	var h geminiHook
	if err := json.Unmarshal(payload, &h); err != nil {
		return Payload{}, fmt.Errorf("hooks: parse gemini payload: %w", err)
	}
	switch h.HookEventName {
	case "BeforeAgent":
		return Payload{Agent: "gemini", Kind: KindPrompt}, nil
	case "AfterAgent":
		// stop_hook_active is read by nobody: as with Claude's Stop, it
		// marks the end of a turn a hook forced on.
		summary := clip(h.PromptResponse)
		if summary == "" {
			summary = "gemini finished"
		}
		return Payload{Agent: "gemini", Kind: "completed", Summary: summary}, nil
	default:
		return Payload{}, ErrNoEvent
	}
}

// opencodeHook is opencode's event payload.
type opencodeHook struct {
	Type       string `json:"type"`
	Message    string `json:"message"`
	Properties struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	} `json:"properties"`
}

func mapOpencode(agent string, payload []byte) (Payload, error) {
	var h opencodeHook
	if err := json.Unmarshal(payload, &h); err != nil {
		return Payload{}, fmt.Errorf("hooks: parse opencode payload: %w", err)
	}
	summary := h.Properties.Message
	if summary == "" {
		summary = h.Message
	}
	switch h.Type {
	case "session.idle":
		return Payload{Agent: agent, Kind: "completed", Summary: clip(summary)}, nil
	case "session.error":
		if summary == "" {
			summary = h.Properties.Error
		}
		return Payload{Agent: agent, Kind: "error", Summary: clip(summary)}, nil
	case "permission.asked", "permission.updated":
		return Payload{Agent: agent, Kind: "needs_input", Summary: clip(summary)}, nil
	default:
		return Payload{}, ErrNoEvent
	}
}

// transcriptLine is the part of a Claude Code transcript entry that holds the
// assistant's words.
type transcriptLine struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// lastAssistantLine reads the tail of a transcript and returns the most recent
// assistant text, clipped. A missing or unreadable transcript is not an error:
// the event is still worth sending, just without a summary.
func lastAssistantLine(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close() //nolint:errcheck // read-only

	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	size := fi.Size()
	offset := int64(0)
	if size > TranscriptTail {
		offset = size - TranscriptTail
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(buf), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line is a fragment of an earlier record
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !strings.Contains(line, `"type":"assistant"`) {
			continue
		}
		var tl transcriptLine
		if err := json.Unmarshal([]byte(line), &tl); err != nil || tl.Type != "assistant" {
			continue
		}
		var parts []string
		for _, c := range tl.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, strings.TrimSpace(c.Text))
			}
		}
		if len(parts) == 0 {
			continue
		}
		return clip(strings.Join(parts, " "))
	}
	return ""
}

// clip collapses whitespace and keeps the first SummaryChars characters, so a
// summary is one line the user can read in a notification.
func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= SummaryChars {
		return s
	}
	return string([]rune(s)[:SummaryChars]) + "…"
}
