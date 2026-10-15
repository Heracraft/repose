package guest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus/testutil"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// waitEvents polls the recorder until want events pass or the deadline.
func (h *harness) waitEvents(want func([]*hostdv1.Event) bool) []*hostdv1.Event {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.rec.mu.Lock()
		evs := append([]*hostdv1.Event(nil), h.rec.events...)
		h.rec.mu.Unlock()
		if want(evs) || time.Now().After(deadline) {
			return evs
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestGuestWarningKindAndDetailAreHostWritten: a guest's Warning reaches the
// api with a kind from the fixed set and a detail hostd wrote, so neither a
// label value nor a log line carries a sentence the guest chose.
func TestGuestWarningKindAndDetailAreHostWritten(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	gd := h.guestd(gid1)
	send := func(kind, detail string) {
		if err := gd.Notify(&guestdv1.Notify{N: &guestdv1.Notify_Warning{Warning: &guestdv1.Warning{Kind: kind, Detail: detail}}}); err != nil {
			t.Fatal(err)
		}
	}
	send(strings.Repeat("k", 100000), "secret tenant text")
	send("disk_high", "root filesystem is 93 percent full")
	send("oom", "xmrig\x00\x1b[31m-very-long-process-name")
	send("store_path_missing", "/nix/store/abc-tenant-thing")
	// herdr_down (I-507) keeps its kind; its detail is hostd's own.
	send("herdr_down", "/home/dev/.config/herdr/herdr.sock: connection refused")
	warnings := func(evs []*hostdv1.Event) []*hostdv1.HostWarning {
		var out []*hostdv1.HostWarning
		for _, e := range evs {
			if w := e.GetHostWarning(); w != nil {
				out = append(out, w)
			}
		}
		return out
	}
	got := warnings(h.waitEvents(func(e []*hostdv1.Event) bool { return len(warnings(e)) >= 5 }))
	if len(got) != 5 {
		t.Fatalf("warnings %v", got)
	}
	want := []struct{ Kind, Detail string }{
		{Kind: "guest_other", Detail: "guest " + gid1},
		{Kind: "disk_high", Detail: "guest " + gid1 + ": root filesystem is 93 percent full"},
		{Kind: "oom", Detail: "guest " + gid1 + ": killed xmrig[31m-very-l"},
		{Kind: "store_path_missing", Detail: "guest " + gid1},
		{Kind: "herdr_down", Detail: "guest " + gid1},
	}
	for i, w := range want {
		if got[i].Kind != w.Kind || got[i].Detail != w.Detail {
			t.Errorf("warning %d: got %q %q, want %q %q", i, got[i].Kind, got[i].Detail, w.Kind, w.Detail)
		}
	}
}

// TestGuestAgentEventsAreBounded: kinds outside the guest set are dropped
// and counted; agent, window and summary are cut to their caps as valid
// UTF-8.
func TestGuestAgentEventsAreBounded(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	gd := h.guestd(gid1)
	send := func(ae *guestdv1.AgentEvent) {
		if err := gd.Notify(&guestdv1.Notify{N: &guestdv1.Notify_AgentEvent{AgentEvent: ae}}); err != nil {
			t.Fatal(err)
		}
	}
	send(&guestdv1.AgentEvent{Agent: "claude", Kind: "billing_stopped", Summary: "spoof"})
	send(&guestdv1.AgentEvent{Agent: strings.Repeat("a", 400000), Kind: "agent_message", TmuxWindow: strings.Repeat("é", 400000), Summary: strings.Repeat("é", 4000) + "\x00"})
	h.waitEvents(func(e []*hostdv1.Event) bool {
		for _, ev := range e {
			if ev.GetAgentEvent() != nil {
				return true
			}
		}
		return false
	})
	time.Sleep(50 * time.Millisecond) // anything dropped would have arrived by now
	evs := h.waitEvents(func(e []*hostdv1.Event) bool { return true })
	var got []*hostdv1.AgentEvent
	for _, e := range evs {
		if a := e.GetAgentEvent(); a != nil {
			got = append(got, a)
		}
	}
	if len(got) != 1 {
		t.Fatalf("agent events %v", got)
	}
	a := got[0]
	if a.Kind != "agent_message" || a.Agent != "unknown" || len(a.TmuxWindow) > capWindow || len(a.Summary) > capSummary ||
		!utf8.ValidString(a.TmuxWindow) || !utf8.ValidString(a.Summary) || strings.ContainsRune(a.Summary, 0) {
		t.Fatalf("not bounded: kind %q agent %q window %d bytes summary %d bytes", a.Kind, a.Agent, len(a.TmuxWindow), len(a.Summary))
	}
	if n := testutil.ToFloat64(h.metrics.GuestNotifyDropped.WithLabelValues("invalid")); n != 1 {
		t.Fatalf("invalid drops %v, want 1", n)
	}
}

// TestGuestQuestionIsBounded: a question id that is not a uuid, or a state a
// guest may not send, is dropped; options are capped.
func TestGuestQuestionIsBounded(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	gd := h.guestd(gid1)
	send := func(q *guestdv1.Question) {
		if err := gd.Notify(&guestdv1.Notify{N: &guestdv1.Notify_Question{Question: q}}); err != nil {
			t.Fatal(err)
		}
	}
	send(&guestdv1.Question{QuestionId: "not-a-uuid", Text: "x"})
	send(&guestdv1.Question{QuestionId: "0199aaaa-0000-7000-8000-00000000000c", Text: "x", State: "answered"})
	send(&guestdv1.Question{QuestionId: "0199aaaa-0000-7000-8000-00000000000d", Text: "x", Options: []string{"a", strings.Repeat("b", 1000), "c", "d", "e"}})
	h.waitEvents(func(e []*hostdv1.Event) bool {
		for _, ev := range e {
			if ev.GetAgentQuestion() != nil {
				return true
			}
		}
		return false
	})
	time.Sleep(50 * time.Millisecond) // anything dropped would have arrived by now
	evs := h.waitEvents(func(e []*hostdv1.Event) bool { return true })
	var got []*hostdv1.AgentQuestion
	for _, e := range evs {
		if q := e.GetAgentQuestion(); q != nil {
			got = append(got, q)
		}
	}
	if len(got) != 1 || got[0].QuestionId != "0199aaaa-0000-7000-8000-00000000000d" || len(got[0].Options) != capOptions || len(got[0].Options[1]) != capOption {
		t.Fatalf("questions %v", got)
	}
}

// TestGuestNotifyFloodIsRateLimited: a guest sending as fast as it can gets
// its burst through and no more, and the drops are counted.
func TestGuestNotifyFloodIsRateLimited(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	gd := h.guestd(gid1)
	const sent = 200
	for i := 0; i < sent; i++ {
		if err := gd.Notify(&guestdv1.Notify{N: &guestdv1.Notify_AgentEvent{AgentEvent: &guestdv1.AgentEvent{Agent: "shell", Kind: "agent_message", Summary: "m"}}}); err != nil {
			t.Fatal(err)
		}
	}
	dropped := func() float64 {
		return testutil.ToFloat64(h.metrics.GuestNotifyDropped.WithLabelValues("rate_limited"))
	}
	forwarded := func(evs []*hostdv1.Event) int {
		n := 0
		for _, e := range evs {
			if e.GetAgentEvent() != nil {
				n++
			}
		}
		return n
	}
	n := forwarded(h.waitEvents(func(e []*hostdv1.Event) bool { return float64(forwarded(e))+dropped() >= sent }))
	// The flood takes well under one refill interval here; allow a couple
	// of refills on a slow runner.
	if n < notifyBurst || n > notifyBurst+3 {
		t.Fatalf("forwarded %d of %d, want about the burst of %d", n, sent, notifyBurst)
	}
	if float64(n)+dropped() != sent {
		t.Fatalf("forwarded %d + dropped %v != sent %d", n, dropped(), sent)
	}
}

// TestBucketRefills: the bucket refills at one per notifyEvery up to the
// burst.
func TestBucketRefills(t *testing.T) {
	var b bucket
	t0 := time.Unix(1000, 0)
	for i := 0; i < notifyBurst; i++ {
		if !b.take(t0) {
			t.Fatalf("burst refused at %d", i)
		}
	}
	if b.take(t0) {
		t.Fatal("past the burst")
	}
	if !b.take(t0.Add(notifyEvery)) || b.take(t0.Add(notifyEvery)) {
		t.Fatal("one refill should give exactly one")
	}
	t1 := t0.Add(time.Hour)
	for i := 0; i < notifyBurst; i++ {
		if !b.take(t1) {
			t.Fatalf("refill capped below the burst at %d", i)
		}
	}
	if b.take(t1) {
		t.Fatal("refill past the burst")
	}
}

// TestSampleGuestFieldsAreCleaned: a guestd answering Sample with names
// Postgres refuses (a NUL; protobuf already refuses invalid UTF-8) or with
// endless lists has them
// cleaned and capped before they join the host's Samples.
func TestSampleGuestFieldsAreCleaned(t *testing.T) {
	h := newHarness(t, nil)
	var procs []*hostdv1.ProcSample
	for i := 0; i < 1000; i++ {
		procs = append(procs, &hostdv1.ProcSample{Comm: "bad\x00\x1b" + strings.Repeat("x", 100), CpuNsDelta: 1})
	}
	var agents []*hostdv1.AgentProc
	for i := 0; i < 1000; i++ {
		agents = append(agents, &hostdv1.AgentProc{Agent: "cl\x00aude", TmuxWindow: "w\x00\x07", State: "pwned"})
	}
	h.gopts.Sample = &guestdv1.SampleResult{Signals: &hostdv1.GuestSignals{SshSessions: 2, Agents: agents}, Procs: procs}
	h.create(gid1)
	s := h.m.CollectSamples(context.Background())
	gs := s.Guests[0]
	if !gs.Signals.GuestdOk || gs.Signals.SshSessions != 2 || len(gs.Procs) != capProcs || len(gs.Signals.Agents) != capAgentProcs {
		t.Fatalf("signals %d agents, %d procs, ok %v", len(gs.Signals.Agents), len(gs.Procs), gs.Signals.GuestdOk)
	}
	for _, p := range gs.Procs {
		if len(p.Comm) > capComm || !utf8.ValidString(p.Comm) || strings.ContainsRune(p.Comm, 0) {
			t.Fatalf("comm %q", p.Comm)
		}
	}
	for _, a := range gs.Signals.Agents {
		if a.Agent != "unknown" || a.State != "unknown" || !utf8.ValidString(a.TmuxWindow) || strings.ContainsRune(a.TmuxWindow, 0) {
			t.Fatalf("agent %+v", a)
		}
	}
}

func TestCleanText(t *testing.T) {
	for in, want := range map[string]string{
		"ok\nline\ttab": "ok\nline\ttab",
		"nul\x00bell\a": "nulbell",
		"bad\xffutf":    "bad?utf",
		"\x1b[2Jclear":  "[2Jclear",
	} {
		if got := cleanText(in, 100); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
	if got := cleanText(strings.Repeat("é", 10), 5); got != "éé" {
		t.Errorf("cut mid-rune: %q", got)
	}
	if got := cleanToken("Claude", 32); got != "unknown" {
		t.Errorf("token %q", got)
	}
	if got := cleanToken("open-code_2", 32); got != "open-code_2" {
		t.Errorf("token %q", got)
	}
}

// TestGuestCloseCarriesItsReasonAndPassesAFullBucket (I-690): a close
// carries a reason from the fixed set (anything else becomes ""), and the
// close of a question hostd passed as open gets through an empty bucket,
// once. A close of an id hostd never saw open, or a second close of one,
// takes a token like any notification, so a guest cannot flood the host's
// stream with made-up closes.
func TestGuestCloseCarriesItsReasonAndPassesAFullBucket(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	gd := h.guestd(gid1)
	const a1, a2, a3 = "0199aaaa-0000-7000-8000-0000000000a1", "0199aaaa-0000-7000-8000-0000000000a2", "0199aaaa-0000-7000-8000-0000000000a3"
	for _, id := range []string{a1, a2} {
		if err := gd.Notify(&guestdv1.Notify{N: &guestdv1.Notify_Question{Question: &guestdv1.Question{QuestionId: id, Agent: "claude", Text: "q?"}}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 200; i++ {
		if err := gd.Notify(&guestdv1.Notify{N: &guestdv1.Notify_AgentEvent{AgentEvent: &guestdv1.AgentEvent{Agent: "shell", Kind: "agent_message", Summary: "m"}}}); err != nil {
			t.Fatal(err)
		}
	}
	closes := []*guestdv1.Question{
		{QuestionId: a1, State: "cancelled", Reason: "terminal"},
		{QuestionId: a2, State: "cancelled", Reason: "made-up"},
		{QuestionId: a3, State: "expired"},                        // never seen open
		{QuestionId: a1, State: "cancelled", Reason: "withdrawn"}, // closed already
	}
	for _, q := range closes {
		if err := gd.Notify(&guestdv1.Notify{N: &guestdv1.Notify_Question{Question: q}}); err != nil {
			t.Fatal(err)
		}
	}
	closed := func(e []*hostdv1.Event) map[string][]string {
		got := map[string][]string{}
		for _, ev := range e {
			if q := ev.GetAgentQuestion(); q != nil && q.State != "" {
				got[q.QuestionId] = append(got[q.QuestionId], q.State+"/"+q.Reason)
			}
		}
		return got
	}
	h.waitEvents(func(e []*hostdv1.Event) bool { return len(closed(e)) >= 2 })
	time.Sleep(50 * time.Millisecond) // anything else would have arrived by now
	got := closed(h.waitEvents(func([]*hostdv1.Event) bool { return true }))
	want := map[string][]string{a1: {"cancelled/terminal"}, a2: {"cancelled/"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("closes %v, want %v", got, want)
	}
}

// TestAgentDoneAndRunningAreAccepted: agent_done (I-688) is a guest kind
// and running (I-694) a guest agent state.
func TestAgentDoneAndRunningAreAccepted(t *testing.T) {
	if _, ok := cleanAgentEvent(gid1, "claude", "agent_done", "PR is up", "claude"); !ok {
		t.Fatal("agent_done refused")
	}
	out := cleanSignals(&hostdv1.GuestSignals{Agents: []*hostdv1.AgentProc{{Agent: "pi", TmuxWindow: "pi", State: "running"}}})
	if out.Agents[0].State != "running" {
		t.Fatalf("running became %q", out.Agents[0].State)
	}
}
