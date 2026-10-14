package obs

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// line decodes the single JSON object a logger wrote.
func line(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	s := strings.TrimSpace(buf.String())
	if s == "" {
		t.Fatal("nothing was logged")
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		t.Fatalf("more than one line: %q", s)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not JSON: %v: %s", err, s)
	}
	return m
}

// TestRequiredFields is the §5 contract: ts in RFC 3339 UTC, level,
// component and msg on every line, without the call site naming component.
func TestRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(LogOptions{Component: ComponentHostd, Writer: &buf})
	log.Info("guest started", "event", EventGuestStart, "guest_id", "g-1")

	m := line(t, &buf)
	for _, k := range []string{"ts", "level", "component", "msg", "event"} {
		if _, ok := m[k]; !ok {
			t.Errorf("field %s missing from %v", k, m)
		}
	}
	if m["component"] != "hostd" {
		t.Errorf("component = %v, want hostd", m["component"])
	}
	if m["msg"] != "guest started" {
		t.Errorf("msg = %v", m["msg"])
	}
	ts, ok := m["ts"].(string)
	if !ok {
		t.Fatalf("ts is %T", m["ts"])
	}
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("ts %q is not RFC 3339: %v", ts, err)
	}
	if parsed.Location() != time.UTC {
		t.Errorf("ts %q is not UTC", ts)
	}
}

// TestComponentCannotBeOverridden: a call site that passes its own component
// does not end up with two, and the handler's value is the one on the wire.
func TestComponentCannotBeOverridden(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(LogOptions{Component: ComponentGuestd, Writer: &buf})
	log.Info("m", "event", EventReady, "component", "somethingelse")
	if got := line(t, &buf)["component"]; got != "guestd" {
		t.Errorf("component = %v, want guestd", got)
	}
	if strings.Count(buf.String(), `"component"`) != 1 {
		t.Errorf("component appears more than once: %s", buf.String())
	}
}

// TestWithKeepsComponentOnce: a derived logger carries component exactly
// once, whatever the caller adds.
func TestWithKeepsComponentOnce(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(LogOptions{Component: ComponentHostd, Writer: &buf}).
		With("guest_id", "g-1").With("project_id", "p-1")
	log.Info("m", "event", EventGuestState, "state", "running")
	if n := strings.Count(buf.String(), `"component"`); n != 1 {
		t.Errorf("component appears %d times: %s", n, buf.String())
	}
	m := line(t, &buf)
	if m["guest_id"] != "g-1" || m["project_id"] != "p-1" {
		t.Errorf("context fields lost: %v", m)
	}
}

// TestRedaction covers every substring on the never-log list of
// docs/ops/OBSERVABILITY.md: the value must not reach the writer.
func TestRedaction(t *testing.T) {
	const canary = "SUPER-SECRET-VALUE"
	for _, field := range RedactedFields() {
		var buf bytes.Buffer
		log := NewLogger(LogOptions{Component: ComponentAPI, Writer: &buf})
		log.Info("m", "event", EventRequest, field, canary)
		if strings.Contains(buf.String(), canary) {
			t.Errorf("field %q was logged: %s", field, buf.String())
		}
		if got := line(t, &buf)[field]; got != Redacted {
			t.Errorf("field %q = %v, want %q", field, got, Redacted)
		}
	}
}

// TestRedactionCatchesDerivedNames is the reason matching is on a substring:
// a call site does not use the bare word, it writes access_token, host_key,
// user_email. One planted value across every name the api and hostd have
// reached for, none of which may appear in the output. (From workstream 05's
// own obs package, kept at the merge: DECISIONS I-59.)
func TestRedactionCatchesDerivedNames(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(LogOptions{Component: ComponentAPI, Writer: &buf})
	const planted = "PLANTED-VALUE-9f8e"
	keys := []string{
		"token", "access_token", "join_token", "secret", "secret_value", "password",
		"authorization", "cert", "host_cert", "key", "host_key", "private_key",
		"email", "user_email", "handle", "remote_url", "argv", "environ", "prompt",
		"summary", "fragment", "ntfy_url", "gchat_url", "webhook", "github_login", "public_key", "ciphertext",
	}
	for _, k := range keys {
		log.Info("x", "event", EventRequest, k, planted)
	}
	out := buf.String()
	if strings.Contains(out, planted) {
		t.Fatalf("planted value leaked:\n%s", out)
	}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not json: %s", l)
		}
		for _, f := range []string{"ts", "level", "component", "event", "msg"} {
			if _, ok := m[f]; !ok {
				t.Fatalf("line lacks %s: %s", f, l)
			}
		}
		if m["component"] != "api" {
			t.Fatalf("component: %s", l)
		}
	}
}

// TestAllowedKeysPass pins the names that contain a forbidden substring and
// are logged anyway, because they are ids, enums or counts.
func TestAllowedKeysPass(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(LogOptions{Component: ComponentAPI, Writer: &buf})
	log.Info("x", "event", EventCertIssue, "kind", "user", "cert_serial", 42,
		"project_id", "p1", "key_version", "v2", "secrets_count", 3)
	out := buf.String()
	for _, want := range []string{
		`"kind":"user"`, `"cert_serial":42`, `"project_id":"p1"`,
		`"key_version":"v2"`, `"secrets_count":3`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %s in %s", want, out)
		}
	}
}

// TestBoundedIdsStayReadable: the names next to a forbidden one are ids and
// counts, and stay readable.
func TestBoundedIdsStayReadable(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(LogOptions{Component: ComponentAPI, Writer: &buf})
	log.Info("m", "event", EventCertIssue, "cert_serial", "42", "key_id", "kv-1", "token_used", true)
	m := line(t, &buf)
	if m["cert_serial"] != "42" || m["key_id"] != "kv-1" || m["token_used"] != true {
		t.Errorf("a bounded id was redacted: %v", m)
	}
}

// TestRedactionInsideGroup: a group is not a way around the floor.
func TestRedactionInsideGroup(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(LogOptions{Component: ComponentAPI, Writer: &buf})
	log.Info("m", "event", EventRequest, slog.Group("detail", "token", "abc123"))
	if strings.Contains(buf.String(), "abc123") {
		t.Errorf("a grouped field was logged: %s", buf.String())
	}
}

// TestViolationsReported: the test logger reports a line with no event and a
// line with a forbidden field, which is how a component's own tests catch
// them.
func TestViolationsReported(t *testing.T) {
	var got []string
	log := NewLogger(LogOptions{
		Component:   ComponentHostd,
		Writer:      io.Discard,
		OnViolation: func(s string) { got = append(got, s) },
	})
	log.Info("no event here")
	log.Info("with a secret", "event", EventGuestStart, "password", "hunter2")
	if len(got) != 2 {
		t.Fatalf("violations = %v, want 2", got)
	}
	if !strings.Contains(got[0], "no event") {
		t.Errorf("first violation = %q", got[0])
	}
	if !strings.Contains(got[1], "password") {
		t.Errorf("second violation = %q", got[1])
	}
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo, "": slog.LevelInfo,
		"notice": LevelNotice, "warn": slog.LevelWarn, "WARNING": slog.LevelWarn,
		"error": slog.LevelError,
	} {
		got, err := ParseLevel(in)
		if err != nil {
			t.Errorf("ParseLevel(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("ParseLevel(\"loud\") should fail")
	}
}

// TestNoticeLevelIsNamed: hostd logs operator-visible events at level 2, and
// "INFO+2" is not a level name anyone can filter on.
func TestNoticeLevelIsNamed(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(LogOptions{Component: ComponentHostd, Writer: &buf})
	log.Log(nil, LevelNotice, "operator login", "event", "operator_login") //nolint:staticcheck // a nil context is what slog accepts here
	if got := line(t, &buf)["level"]; got != "NOTICE" {
		t.Errorf("level = %v, want NOTICE", got)
	}
}

// TestNopWritesNothing: the fallback logger a constructor uses when it was
// given none.
func TestNopWritesNothing(t *testing.T) {
	log := Nop(ComponentGuestd)
	if log == nil {
		t.Fatal("Nop returned nil")
	}
	// Nothing to assert on the output; what matters is that a call does not
	// panic and does not reach slog's default handler.
	log.Error("m", "event", EventWarning)
}

func TestUnknownComponentPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewLogger with an unknown component should panic")
		}
	}()
	NewLogger(LogOptions{Component: Component("worker")})
}
