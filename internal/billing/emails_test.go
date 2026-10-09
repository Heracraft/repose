package billing_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
)

// accountEmail reads the account's newest event of kind, checks its summary
// is the JSON object the notify templates read (I-294 (1)), renders it
// through notify.Render and checks every want appears in both the HTML
// and the text. The payload is returned for field-level checks.
func accountEmail(t *testing.T, pool *db.Pool, a account, kind string, wants ...string) map[string]any {
	t.Helper()
	var summary string
	if err := pool.QueryRow(context.Background(), "select summary from events where user_id = $1 and kind = $2 order by ts desc, id desc limit 1", a.UserID, kind).Scan(&summary); err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(summary), &payload); err != nil {
		t.Fatalf("%s summary is not a JSON object: %q (%v)", kind, summary, err)
	}
	r := render(t, kind, summary, wants...)
	if strings.Contains(r.HTML, "{") && strings.Contains(r.HTML, "&#34;plan&#34;") {
		t.Fatalf("%s email shows the payload as JSON:\n%s", kind, r.HTML)
	}
	return payload
}

// render renders one account event and checks the wants.
func render(t *testing.T, kind, summary string, wants ...string) notify.Rendered {
	t.Helper()
	r, err := notify.Render(notify.Message{Kind: kind, Summary: summary, Dashboard: "https://repose.herakraft.co"})
	if err != nil {
		t.Fatalf("render %s: %v", kind, err)
	}
	for _, w := range wants {
		if !strings.Contains(r.HTML, w) {
			t.Errorf("%s HTML lacks %q:\n%s", kind, w, r.HTML)
		}
		if !strings.Contains(r.Text, w) {
			t.Errorf("%s text lacks %q:\n%s", kind, w, r.Text)
		}
	}
	return r
}

// Every payload the billing producers write renders through the notify
// templates with the plan's name, the amount and the date in both the HTML
// and the text (I-291; the templates' fields are the table in
// docs/features/notifications.md, "Account emails").
func TestAccountPayloadsRender(t *testing.T) {
	at := time.Date(2026, 10, 8, 14, 0, 0, 0, time.UTC)
	cases := []struct {
		kind    string
		payload any
		wants   []string
	}{
		{billing.KindTrialEnding, billing.TrialEndingPayload{Plan: "plus", AmountCents: 5900, ChargeAt: at}, []string{"Plus", "$59.00", "8 October 2026 at 14:00 UTC"}},
		{billing.KindPaymentFailed, billing.PaymentFailedPayload{Plan: "solo", AmountCents: 2900}, []string{"Solo", "$29.00", "https://repose.herakraft.co/billing"}},
		{billing.KindPaymentFailed, billing.PaymentFailedPayload{Plan: "solo", AmountCents: 2900, PortalURL: "https://polar.sh/repose/portal"}, []string{"Solo", "$29.00", "https://polar.sh/repose/portal"}},
		{billing.KindSubscriptionCancelled, billing.SubscriptionCancelledPayload{Plan: "plus", EndsAt: at}, []string{"Plus", "8 October 2026 at 14:00 UTC"}},
		{billing.KindSubscriptionEnded, billing.SubscriptionEndedPayload{Plan: "solo", EndedAt: at, RetentionUntil: at.Add(30 * 24 * time.Hour)}, []string{"Solo", "8 October 2026 at 14:00 UTC", "7 November 2026 at 14:00 UTC"}},
		{billing.KindPlanChanged, billing.PlanChangedPayload{FromPlan: "solo", ToPlan: "plus", EffectiveAt: at}, []string{"from Solo to Plus", "8 October 2026 at 14:00 UTC"}},
		{billing.KindEgressStopped, billing.EgressStoppedPayload{Plan: "solo", EgressGB: 1000.25, LimitGB: 250, Until: at}, []string{"Solo", "1000.3 GB", "250 GB", "8 October 2026 at 14:00 UTC"}},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.payload)
		if err != nil {
			t.Fatal(err)
		}
		r := render(t, c.kind, string(b), c.wants...)
		if strings.Contains(r.Text, `"plan"`) || strings.Contains(r.HTML, "&#34;plan&#34;") {
			t.Errorf("%s renders the payload as JSON:\n%s", c.kind, r.Text)
		}
		if r.Subject == "" || strings.Contains(r.Subject, "{") {
			t.Errorf("%s subject %q", c.kind, r.Subject)
		}
	}
}
