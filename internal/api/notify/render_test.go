package notify_test

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/notify"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/<kind>.html and .txt from the fixtures")

const (
	dash    = "https://repose.herakraft.co"
	unsubTo = "https://api.repose.herakraft.co/v1/notify/unsubscribe?token=abc.def"
)

var (
	fixtureProject = uuid.MustParse("01a0c14a-f7e3-7926-be1c-75e7728878a1")
	fixtureQ       = uuid.MustParse("01a0c14a-fdb3-78af-9c30-5abd8fce0a84")
	fixtureExpires = time.Date(2026, 9, 28, 18, 30, 0, 0, time.UTC)
)

// fixtures is one message per kind: every account kind of I-291 with the
// payload its producer writes (docs/features/notifications.md), and every
// agent and platform kind with a summary an agent might send.
func fixtures() map[string]notify.Message {
	agent := func(kind, summary string) notify.Message {
		return notify.Message{Kind: kind, Agent: "claude", Project: "todo-app", Summary: summary, Email: "u@example.com", Dashboard: dash, ProjectID: fixtureProject, Unsubscribe: unsubTo}
	}
	platform := func(kind, summary string) notify.Message {
		return notify.Message{Kind: kind, Project: "todo-app", Summary: summary, Email: "u@example.com", Dashboard: dash, ProjectID: fixtureProject, Unsubscribe: unsubTo}
	}
	account := func(kind string, payload any) notify.Message {
		s := ""
		if payload != nil {
			b, _ := json.Marshal(payload)
			s = string(b)
		}
		return notify.Message{Kind: kind, Summary: s, Email: "u@example.com", Dashboard: dash}
	}
	q := agent("agent_question", "Drop the legacy sessions table? It has 3 rows, all from 2024.")
	q.Question = &notify.QuestionLinks{ID: fixtureQ, Options: []string{"yes", "no"}, Replies: []string{
		"https://api.repose.herakraft.co/v1/questions/reply?token=aaa.bbb&via=email",
		"https://api.repose.herakraft.co/v1/questions/reply?token=ccc.ddd&via=email",
	}, Expires: fixtureExpires}
	return map[string]notify.Message{
		"completed":              agent("completed", "Added the auth flow, 14 tests green, committed 3f9e2a1."),
		"needs_input":            agent("needs_input", "Allow Bash(rm -rf node_modules)?"),
		"error":                  agent("error", "npm test exited 1: 3 failures in auth.test.ts"),
		"agent_message":          agent("agent_message", "Deploy to staging is green."),
		"agent_question":         q,
		"idle_running":           agent("idle_running", "todo-app (large) has run 24h with no session, no tmux client and no agent working. Stop it with `repose stop todo-app` if you are done."),
		"base_updated":           platform("base_updated", "Platform base 2026.09.27.1 is running on this machine."),
		"base_update_failed":     platform("base_update_failed", "Platform base 2026.09.27.1 did not build with your configuration; the machine stays on 2026.09.20.3. See the build log on the dashboard."),
		"snapshot_failed":        platform("snapshot_failed", "The nightly snapshot failed: the volume was busy. The next one runs tonight."),
		"host_moved":             platform("host_moved", "todo-app was restored onto another server from its snapshot of 2026-09-27 02:00 UTC."),
		"destroy_failed":         platform("destroy_failed", "The destroy did not finish: the host did not answer. It is retried."),
		"notifications_paused":   platform("notifications_paused", "30+ events in the last hour; notifications for this project are paused until the top of the hour. See the dashboard."),
		"billing_stopped":        platform("billing_stopped", "Your payment has been failing for three days, so todo-app was snapshotted and stopped."),
		"abuse_stopped":          platform("abuse_stopped", "xmrig was running at full CPU on todo-app; the machine was stopped."),
		"welcome":                account("welcome", nil),
		"waitlist_joined":        account("waitlist_joined", map[string]any{"position": 7}),
		"waitlist_invited":       account("waitlist_invited", map[string]any{"hold_until": "2026-09-30T14:00:00Z"}),
		"waitlist_expired":       account("waitlist_expired", map[string]any{"position": 3}),
		"trial_ending":           account("trial_ending", map[string]any{"plan": "solo", "amount_cents": 2900, "charge_at": "2026-10-04T14:00:00Z"}),
		"payment_failed":         account("payment_failed", map[string]any{"plan": "pro", "amount_cents": 5900, "portal_url": "https://customer-portal.paddle.com/cpl_abc"}),
		"subscription_cancelled": account("subscription_cancelled", map[string]any{"plan": "solo", "ends_at": "2026-10-27T14:00:00Z"}),
		"subscription_ended":     account("subscription_ended", map[string]any{"plan": "solo", "ended_at": "2026-10-27T14:00:00Z", "retention_until": "2026-11-26T14:00:00Z"}),
		"plan_changed":           account("plan_changed", map[string]any{"from_plan": "solo", "to_plan": "pro", "effective_at": "2026-09-27T15:04:00Z"}),
		"egress_stopped":         account("egress_stopped", map[string]any{"plan": "solo", "egress_gb": 1002.4, "limit_gb": 250, "until": "2026-10-27T14:00:00Z"}),
		"disk_over_plan":         account("disk_over_plan", map[string]any{"plan": "solo", "held_gb": 112.4, "limit_gb": 100}),
	}
}

// TestGoldenEmails pins the HTML and text of every kind (DECISIONS I-291).
// `go test ./internal/api/notify -run TestGoldenEmails -update` rewrites
// the files after a template change; read the diff before committing it.
func TestGoldenEmails(t *testing.T) {
	fx := fixtures()
	for k := range events.AccountKinds {
		if _, ok := fx[k]; !ok {
			t.Errorf("account kind %s has no fixture", k)
		}
	}
	names := make([]string, 0, len(fx))
	for k := range fx {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, kind := range names {
		m := fx[kind]
		r, err := notify.Render(m)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if strings.ContainsAny(r.Text, "!—–") || strings.ContainsAny(strings.TrimPrefix(r.HTML, "<!doctype html>"), "!—–") {
			t.Errorf("%s: an exclamation mark or a dash in the copy", kind)
		}
		for ext, got := range map[string]string{".html": r.HTML, ".txt": r.Text} {
			path := filepath.Join("testdata", kind+ext)
			if *updateGolden {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: %v (run with -update)", kind, err)
			}
			if string(want) != got {
				t.Errorf("%s%s differs from the golden file (run with -update and read the diff)\n--- got ---\n%s", kind, ext, got)
			}
		}
		transactional := events.AccountKinds[kind]
		if strings.Contains(r.Text, "Stop these emails") != !transactional || strings.Contains(r.HTML, "Stop these emails") != !transactional {
			t.Errorf("%s: unsubscribe line present=%v, transactional=%v", kind, !transactional, transactional)
		}
		if !strings.Contains(r.HTML, ">repose</td>") || !strings.Contains(r.HTML, `<a href="`+dash+`"`) || !strings.Contains(r.Text, dash) {
			t.Errorf("%s: header word or dashboard link missing", kind)
		}
		if strings.Contains(r.HTML, "<img") {
			t.Errorf("%s: an image", kind)
		}
	}
}

// Tenant text is data: a summary with markup is escaped in the HTML and
// left alone in the text.
func TestRenderEscapesTenantText(t *testing.T) {
	m := notify.Message{Kind: "completed", Agent: "claude", Project: "<b>todo</b>", Summary: `<script>alert("x")</script> & done`, Email: "u@example.com", Dashboard: dash}
	r, err := notify.Render(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.HTML, "<script>") || strings.Contains(r.HTML, "<b>todo</b>") || !strings.Contains(r.HTML, "&lt;script&gt;") || !strings.Contains(r.HTML, "&amp; done") {
		t.Fatalf("html not escaped:\n%s", r.HTML)
	}
	if !strings.Contains(r.Text, `<script>alert("x")</script> & done`) {
		t.Fatalf("text altered:\n%s", r.Text)
	}
	// A payload field with markup is escaped too, and a missing field
	// renders without it.
	m = notify.Message{Kind: "trial_ending", Summary: `{"plan":"<i>solo</i>"}`, Email: "u@example.com", Dashboard: dash}
	if r, err = notify.Render(m); err != nil || strings.Contains(r.HTML, "<i>") || !strings.Contains(r.Text, "At the end of your free week") {
		t.Fatalf("payload: %v\n%s", err, r.HTML)
	}
	// A summary that is not JSON at all still renders.
	m = notify.Message{Kind: "payment_failed", Summary: "not json", Email: "u@example.com", Dashboard: dash}
	if r, err = notify.Render(m); err != nil || !strings.Contains(r.Text, "your plan") {
		t.Fatalf("non-JSON payload: %v\n%s", err, r.Text)
	}
}

// One Resend call carries html and text of the same content, and the
// subject keeps its [repose] prefix.
func TestEmailSendsHTMLAndText(t *testing.T) {
	var got struct {
		Subject string `json:"subject"`
		HTML    string `json:"html"`
		Text    string `json:"text"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := &notify.Email{APIKey: "re_test", URL: srv.URL}
	if err := e.Send(context.Background(), fixtures()["trial_ending"]); err != nil {
		t.Fatal(err)
	}
	if got.Subject != "[repose] Your free week ends soon" {
		t.Fatalf("subject %q", got.Subject)
	}
	if !strings.HasPrefix(got.HTML, "<!doctype html>") || !strings.Contains(got.HTML, "$29.00") || !strings.Contains(got.Text, "$29.00") || !strings.Contains(got.Text, "4 October 2026 at 14:00 UTC") {
		t.Fatalf("html:\n%s\ntext:\n%s", got.HTML, got.Text)
	}
}
