package obs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// LevelNotice sits between Info and Warn: the operator-visible events that
// are not problems (a guest started, an operator logged in). hostd used
// slog.Level(2) for this before obs existed; the name is here so the JSON
// says NOTICE instead of INFO+2.
const LevelNotice = slog.Level(2)

// Redacted replaces the value of a field whose name is on the never-log
// list. It is deliberately not the empty string: a reviewer reading a log
// line must be able to tell "we refused to log this" from "this was empty".
const Redacted = "[redacted]"

// forbidden are substrings of a log field name whose value is never logged.
// Matching is on a substring rather than the whole name so that `user_email`,
// `access_token` and `wrapped_key` are caught without being listed: the
// never-log list of docs/ops/OBSERVABILITY.md is about the value, and a
// value's name is whatever the call site felt like. The reviewer is the real
// check; this is the floor.
//
// The list is the union of what workstreams 10 and 05 each arrived at
// independently (DECISIONS I-59).
var forbidden = []string{
	"token", "secret", "password", "authorization", "cert", "key",
	"email", "handle", "remote_url", "argv", "args", "environ", "env",
	"prompt", "summary", "fragment", "ntfy_url", "gchat_url", "webhook", "github_login",
	"public_key", "ciphertext", "cmdline", "command_line", "user_agent",
	"transcript",
}

// allowed are exact field names that contain a forbidden substring and carry
// no tenant data: bounded enums, identifiers and counts. Each one is here
// because it is in use and defensible, not because it was convenient.
var allowed = map[string]bool{
	// A serial, a fingerprint or a version identifies without carrying.
	"cert_serial": true, "cert_fingerprint": true, "cert_kind": true,
	"key_id": true, "key_version": true, "kind": true,
	// Counts, lengths and presence, never values. argv_len and summary_bytes
	// are the shapes that replaced an argv and a summary on the two lines
	// that used to carry them (DECISIONS I-53).
	"certs": true, "keys": true, "secrets": true, "secret_count": true,
	"secret_names_count": true, "secrets_count": true, "token_used": true,
	"has_token": true, "cert_bytes": true, "key_bytes": true,
	"argv_len": true, "summary_bytes": true, "env_count": true,
	"secrets_bytes": true, "prompt_len": true,
	// A store path or a unit path on the host is the platform's own
	// business; a path inside a guest is not, and cannot be told apart by
	// name, so `store_path` is allowed and `path` is refused by obslint.
	"store_path": true, "unit_path": true,
}

// IsForbidden reports whether a field name must be redacted.
func IsForbidden(key string) bool {
	k := strings.ToLower(key)
	if allowed[k] {
		return false
	}
	for _, f := range forbidden {
		if strings.Contains(k, f) {
			return true
		}
	}
	return false
}

// RedactedFields lists the forbidden substrings, for tests and for the
// obslint rules that refuse them at build time.
func RedactedFields() []string {
	out := make([]string, len(forbidden))
	copy(out, forbidden)
	return out
}

// LogOptions configures NewLogger. Only Component is required.
type LogOptions struct {
	// Component is the binary's name in the log and must be valid; an
	// invalid one panics, because a log line with the wrong component is
	// invisible to every query in docs/ops/OBSERVABILITY.md.
	Component Component
	// Level defaults to slog.LevelInfo. Use ParseLevel for a flag value.
	Level slog.Level
	// Writer defaults to os.Stdout. guestd passes os.Stderr, which is the
	// serial console hostd captures, so that logging keeps working while the
	// guest's root filesystem is frozen for a snapshot.
	Writer io.Writer
	// OnViolation, when set, is called with a description of every line that
	// breaks the §5 rules (no event field, or a field name that must never
	// be logged). NewTestLogger points it at t.Errorf; production leaves it
	// nil and relies on redaction plus obslint.
	OnViolation func(string)
}

// NewLogger builds the logger of docs/workstreams/10-observability.md §5:
// one JSON object per line with ts, level, component and msg always
// present, and the never-log fields redacted.
func NewLogger(o LogOptions) *slog.Logger {
	if err := o.Component.check(); err != nil {
		// As in NewMetrics: the component is a constant, and a log line with
		// the wrong component is invisible to every query in
		// docs/ops/OBSERVABILITY.md, which is worse than not starting.
		panic(err)
	}
	w := o.Writer
	if w == nil {
		w = os.Stdout
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       o.Level,
		ReplaceAttr: replaceAttr,
	})
	return slog.New(&handler{
		Handler:   h,
		component: o.Component,
		violation: o.OnViolation,
	})
}

// NewTestLogger writes to w (use io.Discard for a quiet test) and fails the
// test on any line that breaks the §5 rules, so a component's own unit
// tests are the first place an unnamed event shows up.
func NewTestLogger(t interface{ Errorf(string, ...any) }, c Component, w io.Writer) *slog.Logger {
	if w == nil {
		w = io.Discard
	}
	return NewLogger(LogOptions{
		Component:   c,
		Level:       slog.LevelDebug,
		Writer:      w,
		OnViolation: func(s string) { t.Errorf("log line breaks docs/workstreams/10-observability.md §5: %s", s) },
	})
}

// Nop is the logger a constructor falls back to when its caller passed
// none: it writes nowhere but keeps the type and the component, so a
// library never has to check for nil and never reaches slog's default
// handler, which no repose binary configures.
func Nop(c Component) *slog.Logger {
	return NewLogger(LogOptions{Component: c, Level: slog.LevelError + 1, Writer: io.Discard})
}

// ParseLevel maps a --log-level flag value to a slog level. The names are
// the JSON `level` values lowercased.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "notice":
		return LevelNotice, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q; use debug, info, notice, warn or error", s)
	}
}

// replaceAttr renames slog's built-in keys to the §5 names and formats the
// two that need it: ts as RFC 3339 UTC, level with NOTICE named.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 {
		switch a.Key {
		case slog.TimeKey:
			return slog.String("ts", a.Value.Time().UTC().Format(time.RFC3339))
		case slog.MessageKey:
			return slog.String("msg", a.Value.String())
		case slog.LevelKey:
			if lv, ok := a.Value.Any().(slog.Level); ok && lv == LevelNotice {
				return slog.String(slog.LevelKey, "NOTICE")
			}
			return a
		}
	}
	if IsForbidden(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	return a
}

// handler adds `component` to every line, redacts the never-log fields that
// arrive inside groups or LogValuers (where ReplaceAttr does not see them
// the same way), and reports §5 violations to OnViolation.
type handler struct {
	slog.Handler
	component Component
	violation func(string)
	// preset is true once component has been added by WithAttrs, so a
	// derived logger does not add it twice.
	preset bool
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	var hasEvent bool
	var bad []string
	r.Attrs(func(a slog.Attr) bool {
		switch {
		case a.Key == "event":
			hasEvent = true
		case IsForbidden(a.Key):
			bad = append(bad, a.Key)
		case a.Key == "component":
			// A call site that sets component itself is harmless but
			// redundant; the handler's value wins on the wire because
			// WithAttrs runs first, and slog keeps both. Drop the caller's.
		}
		return true
	})
	if h.violation != nil {
		if !hasEvent && r.Level >= slog.LevelInfo {
			h.violation(fmt.Sprintf("no event field: %q", r.Message))
		}
		for _, k := range bad {
			h.violation(fmt.Sprintf("field %q is on the never-log list (%q)", k, r.Message))
		}
	}
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	if !h.preset {
		out.AddAttrs(slog.String("component", string(h.component)))
	}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != "component" {
			out.AddAttrs(a)
		}
		return true
	})
	return h.Handler.Handle(ctx, out)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	keep := make([]slog.Attr, 0, len(attrs)+1)
	if !h.preset {
		keep = append(keep, slog.String("component", string(h.component)))
	}
	for _, a := range attrs {
		if a.Key == "component" {
			continue
		}
		keep = append(keep, a)
	}
	return &handler{Handler: h.Handler.WithAttrs(keep), component: h.component, violation: h.violation, preset: true}
}

func (h *handler) WithGroup(name string) slog.Handler {
	// component belongs at the top level, so it is added before the group is
	// opened; otherwise the record attrs Handle adds would land inside it.
	base := h.Handler
	if !h.preset {
		base = base.WithAttrs([]slog.Attr{slog.String("component", string(h.component))})
	}
	return &handler{Handler: base.WithGroup(name), component: h.component, violation: h.violation, preset: true}
}
