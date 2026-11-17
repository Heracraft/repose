package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/fakes/api"
)

// TestParseHold (I-1282): `--hold` takes off or a duration, and refuses
// anything past an hour, with the words the usage error prints.
func TestParseHold(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
	}{{"off", 0}, {"0", 0}, {"30s", 30}, {"2m", 120}, {"1h", 3600}} {
		got, err := ParseHold(c.in)
		if err != nil || got != c.want {
			t.Fatalf("ParseHold(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"", "on", "2", "two minutes", "2h", "-1m"} {
		if _, err := ParseHold(in); err == nil {
			t.Fatalf("ParseHold(%q) took it", in)
		}
	}
}

// TestNotifySetHold (I-1282): `--hold` round-trips through the api and
// `repose notify` prints it; `off` reads as off, not as 0s.
func TestNotifySetHold(t *testing.T) {
	fake := api.New(api.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out = out

	five, err := ParseHold("5m")
	if err != nil {
		t.Fatal(err)
	}
	if err := NotifySetCmd(ctx, e, nil, nil, nil, &five); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.HasSuffix(got, "hold: 5m0s\n") {
		t.Fatalf("after --hold 5m: %q", got)
	}
	out.buf.Reset()
	zero, err := ParseHold("off")
	if err != nil {
		t.Fatal(err)
	}
	if err := NotifySetCmd(ctx, e, nil, nil, nil, &zero); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.HasSuffix(got, "hold: off\n") {
		t.Fatalf("after --hold off: %q", got)
	}
}

// TestNotSent (I-1280): `repose events` says why a notification went
// nowhere, and says nothing for every other row.
func TestNotSent(t *testing.T) {
	for _, c := range []struct {
		name string
		in   map[string]string
		want string
	}{
		{"no delivery queued", nil, ""},
		{"empty", map[string]string{}, ""},
		{"sent", map[string]string{"gchat": "2026-11-17T13:40:22Z"}, ""},
		{"failed on its own", map[string]string{"ntfy": "failed: the server answered 403"}, ""},
		{"sent on one channel", map[string]string{"gchat": "2026-11-17T13:40:22Z", "email": "held"}, ""},
		{"held", map[string]string{"gchat": "held"}, "not sent yet: you are at the machine"},
		{"dropped", map[string]string{"gchat": "dropped: resolved"}, "not sent: it was over by then"},
		{"held and dropped", map[string]string{"gchat": "held", "email": "dropped: resolved"}, "not sent yet: you are at the machine"},
	} {
		if got := notSent(c.in); got != c.want {
			t.Fatalf("%s: notSent = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestEventLineSaysWhyNothingWasSent (I-1280): the phrase lands on the
// row, after the agent's link.
func TestEventLineSaysWhyNothingWasSent(t *testing.T) {
	ev := Event{Kind: "agent_done", Agent: "claude", Summary: "deployed",
		Link:      &EventLink{URL: "https://example.com/app"},
		Delivered: map[string]string{"gchat": "held"}}
	line := eventLine(ev, eventsWidths{})
	if !strings.Contains(line, "https://example.com/app  (not sent yet: you are at the machine)") {
		t.Fatalf("event line: %q", line)
	}
	ev.Delivered = map[string]string{"gchat": "2026-11-17T13:40:22Z"}
	if line := eventLine(ev, eventsWidths{}); strings.Contains(line, "not sent") {
		t.Fatalf("a sent row says so: %q", line)
	}
}
