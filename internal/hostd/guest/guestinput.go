package guest

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// Everything a guest sends over vsock is written by whoever is root in that
// guest, not necessarily by guestd. Before any of it reaches the api, a log
// line or a metric label, hostd bounds it here: kinds come from fixed sets,
// names are short tokens, free text is valid UTF-8 without control
// characters and capped, and each guest's notifications pass a token bucket
// (DECISIONS I-445).

// Caps on guest-sent fields. The summary and question text caps are the
// ones docs/interfaces/grpc-hostd.md gives; the rest are generous for what
// guestd sends (agent names like "opencode", tmux window names).
const (
	capSummary    = 1024
	capText       = 1024
	capAgent      = 32
	capWindow     = 64
	capOption     = 64
	capOptions    = 3
	capComm       = 16
	capProcs      = 128
	capAgentProcs = 32
)

// Notify rate: a burst of notifyBurst, refilled one every notifyEvery. An
// agent turn produces one or two notifications and guestd sends each
// warning kind at most once per 10 minutes, so a guest within its rights
// never meets this limit.
const (
	notifyBurst = 30
	notifyEvery = 2 * time.Second
)

// guestAgentKinds are the AgentEvent kinds a guest may send
// (docs/interfaces/grpc-hostd.md). Platform kinds such as billing_stopped
// are the api's to raise, never a guest's.
var guestAgentKinds = map[string]bool{"completed": true, "needs_input": true, "error": true, "agent_message": true}

// guestWarningKinds are the Warning kinds guestd sends
// (docs/interfaces/vsock-guestd.md). Any other becomes guestOtherWarning.
var guestWarningKinds = map[string]bool{
	"disk_high": true, "inotify_exhausted": true, "docker_down": true, "freeze_timeout": true,
	"store_path_missing": true, "oom": true, "tmux_down": true,
}

const guestOtherWarning = "guest_other"

// questionStates are the states a guest-side close may carry; empty is an
// open question.
var questionStates = map[string]bool{"": true, "cancelled": true, "expired": true}

// agentStates are the agent states guestd reports in a sample.
var agentStates = map[string]bool{"working": true, "idle": true, "needs_input": true, "unknown": true}

// cleanText makes s valid UTF-8 with no control characters other than
// newline and tab, and at most n bytes, cut on a rune boundary.
func cleanText(s string, n int) string {
	s = strings.ToValidUTF8(s, "?")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	return cutRunes(s, n)
}

// cleanLine is cleanText with newlines and tabs dropped too: a window name
// or a question option is one line.
func cleanLine(s string, n int) string {
	return cleanText(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, s), n)
}

func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// cleanToken keeps an agent name: lower-case letters, digits, '-' and '_',
// at most n bytes. Anything else is "unknown"; empty stays empty.
func cleanToken(s string, n int) string {
	if s == "" {
		return ""
	}
	if len(s) > n {
		return "unknown"
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return "unknown"
		}
	}
	return s
}

// cleanComm keeps a process name as the kernel shows it, at most 16 bytes of
// printable text; a name with nothing printable left is "?".
func cleanComm(s string) string {
	s = cleanLine(s, capComm)
	if strings.TrimSpace(s) == "" {
		return "?"
	}
	return s
}

// cleanAgentEvent bounds a guest's AgentEvent; ok is false for a kind a
// guest may not send.
func cleanAgentEvent(guestID, agent, kind, summary, window string) (*hostdv1.AgentEvent, bool) {
	if !guestAgentKinds[kind] {
		return nil, false
	}
	return &hostdv1.AgentEvent{
		GuestId: guestID, Agent: cleanToken(agent, capAgent), Kind: kind,
		Summary: cleanText(summary, capSummary), TmuxWindow: cleanLine(window, capWindow),
	}, true
}

// cleanQuestion bounds a guest's Question; ok is false for an id that is not
// a uuid or a state a guest may not send.
func cleanQuestion(guestID string, id, agent, window, text string, options []string, timeoutS uint32, st string) (*hostdv1.AgentQuestion, bool) {
	if _, err := uuid.Parse(id); err != nil || !questionStates[st] {
		return nil, false
	}
	var opts []string
	for _, o := range options {
		if len(opts) == capOptions {
			break
		}
		if o = cleanLine(o, capOption); o != "" {
			opts = append(opts, o)
		}
	}
	return &hostdv1.AgentQuestion{
		GuestId: guestID, QuestionId: id, Agent: cleanToken(agent, capAgent), TmuxWindow: cleanLine(window, capWindow),
		Text: cleanText(text, capText), Options: opts, TimeoutS: timeoutS, State: st,
	}, true
}

// cleanWarning turns a guest's Warning into the HostWarning hostd forwards.
// The detail is rebuilt by hostd from the numbers and the process name the
// known kinds carry, so no guest-written sentence reaches the api's log.
func cleanWarning(guestID, kind, detail string) *hostdv1.HostWarning {
	out := "guest " + guestID
	if !guestWarningKinds[kind] {
		return &hostdv1.HostWarning{Kind: guestOtherWarning, Detail: out}
	}
	var a, b int
	switch kind {
	case "disk_high":
		if _, err := fmt.Sscanf(detail, "root filesystem is %d percent full", &a); err == nil {
			out += fmt.Sprintf(": root filesystem is %d percent full", a)
		}
	case "inotify_exhausted":
		if _, err := fmt.Sscanf(detail, "inotify instances %d of %d", &a, &b); err == nil {
			out += fmt.Sprintf(": inotify instances %d of %d", a, b)
		} else if _, err := fmt.Sscanf(detail, "inotify watches %d of %d", &a, &b); err == nil {
			out += fmt.Sprintf(": inotify watches %d of %d", a, b)
		}
	case "oom":
		// A process name is one of the fields a log line may carry
		// (docs/ops/OBSERVABILITY.md).
		out += ": killed " + cleanComm(detail)
	}
	return &hostdv1.HostWarning{Kind: kind, Detail: out}
}

// cleanSignals bounds the guest-reported part of a sample. The counts are
// numbers and pass as they are; the agent list is capped and its strings
// cleaned.
func cleanSignals(in *hostdv1.GuestSignals) *hostdv1.GuestSignals {
	out := &hostdv1.GuestSignals{}
	if in == nil {
		return out
	}
	out.SshSessions, out.TmuxClients, out.DockerContainers = in.SshSessions, in.TmuxClients, in.DockerContainers
	for _, a := range in.Agents {
		if len(out.Agents) == capAgentProcs {
			break
		}
		if a == nil {
			continue
		}
		st := a.State
		if !agentStates[st] {
			st = "unknown"
		}
		out.Agents = append(out.Agents, &hostdv1.AgentProc{Agent: cleanToken(a.Agent, capAgent), TmuxWindow: cleanLine(a.TmuxWindow, capWindow), State: st})
	}
	return out
}

// cleanProcs caps the process list and cleans each name. guestd sends its
// top 50 by CPU plus the watched names below them.
func cleanProcs(in []*hostdv1.ProcSample) []*hostdv1.ProcSample {
	out := make([]*hostdv1.ProcSample, 0, min(len(in), capProcs))
	for _, p := range in {
		if len(out) == capProcs {
			break
		}
		if p == nil {
			continue
		}
		out = append(out, &hostdv1.ProcSample{Comm: cleanComm(p.Comm), CpuNsDelta: p.CpuNsDelta, RssBytes: p.RssBytes})
	}
	return out
}

// bucket is a token bucket on the guest's notifications, read and written
// only by the guest's monitor goroutine.
type bucket struct {
	tokens  float64
	last    time.Time
	dropped int
}

// take reports whether one notification may pass at now.
func (b *bucket) take(now time.Time) bool {
	if b.last.IsZero() {
		b.tokens, b.last = notifyBurst, now
	}
	if el := now.Sub(b.last); el > 0 {
		b.tokens += float64(el) / float64(notifyEvery)
		if b.tokens > notifyBurst {
			b.tokens = notifyBurst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
