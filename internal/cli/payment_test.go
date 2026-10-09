package cli

import (
	"bytes"
	"strings"
	"testing"
)

// payment_required prints the api's sentence verbatim and exits 7; an
// older api's fragment (or a card-era reason) gets the plan sentence
// (DECISIONS I-289).
func TestPaymentRequiredMessage(t *testing.T) {
	const fallback = "Choose a plan at https://repose.herakraft.co/billing first."
	for _, c := range []struct {
		name string
		err  *APIError
		want string
	}{
		{"plan_limit verbatim", &APIError{Code: "payment_required", Message: "Your Solo plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at https://repose.herakraft.co/billing.", Detail: map[string]any{"reason": "plan_limit"}},
			"Your Solo plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at https://repose.herakraft.co/billing."},
		{"plan_limit names the stop", &APIError{Code: "payment_required", Message: "Your Solo plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at https://repose.herakraft.co/billing.", Detail: map[string]any{"reason": "plan_limit", "projects": []any{"todo-app"}}},
			"Your Solo plan runs 8 GB at once and todo-app is using it. `repose stop todo-app` frees it, or upgrade at https://repose.herakraft.co/billing."},
		{"plan_limit with two machines names both", &APIError{Code: "payment_required", Message: "Your Plus plan runs 16 GB at once and a and b are using it. Stop one, or upgrade at https://repose.herakraft.co/billing.", Detail: map[string]any{"reason": "plan_limit", "projects": []any{"a", "b"}}},
			"Your Plus plan runs 16 GB at once and a and b are using it. `repose stop a b` frees it, or upgrade at https://repose.herakraft.co/billing."},
		{"plan_limit from another api verbatim", &APIError{Code: "payment_required", Message: "A large (8 GB) would pass the 8 GB of memory Solo gives running machines; todo-app is using it. Stop one or upgrade.", Detail: map[string]any{"reason": "plan_limit", "projects": []any{"todo-app"}}},
			"A large (8 GB) would pass the 8 GB of memory Solo gives running machines; todo-app is using it. Stop one or upgrade."},
		{"egress_limit verbatim", &APIError{Code: "payment_required", Message: "Your machines are stopped until 1 November: this period's egress passed 1000 GB.", Detail: map[string]any{"reason": "egress_limit"}},
			"Your machines are stopped until 1 November: this period's egress passed 1000 GB."},
		{"subscription_required verbatim", &APIError{Code: "payment_required", Message: fallback, Detail: map[string]any{"reason": "subscription_required", "waitlist": nil}}, fallback},
		{"old api card_required", &APIError{Code: "payment_required", Message: "add a card before starting a guest", Detail: map[string]any{"reason": "card_required"}}, fallback},
		{"old api no detail", &APIError{Code: "payment_required", Message: "your trial credit is used up"}, fallback},
		{"new reason, empty message", &APIError{Code: "payment_required", Detail: map[string]any{"reason": "past_due"}}, fallback},
	} {
		if got := paymentRequiredMessage(c.err); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// The generic handler prints it and exits 7.
	var stderr strings.Builder
	code := exitCodeFor(&APIError{Code: "payment_required", Message: "Your last payment failed. Update your card at https://repose.herakraft.co/billing to start machines again.", Detail: map[string]any{"reason": "past_due"}}, &stderr)
	if code != ExitPaymentRequired || !strings.Contains(stderr.String(), "Your last payment failed.") {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}

// The project cap refusal reads the same from every command (I-569):
// run, restore and the rest print the CLI's sentence for the api's 400,
// not "invalid: ..."; an api from before I-569 (no detail.reason) is
// recognised by its numbers; another invalid is the api's sentence with
// its code after it. The cap exits 7, as the other plan limits (I-623).
func TestProjectLimitMessage(t *testing.T) {
	for _, c := range []struct {
		name string
		err  *APIError
		want string
		code int
	}{
		{"one", &APIError{Code: "invalid", Message: "you have 100 of the 100 projects an account can have, running or stopped; destroy one first",
			Detail: map[string]any{"reason": "project_limit", "limit": float64(100), "projects": float64(100)}},
			"You have 100 of the 100 projects an account can have, running or stopped. Destroy one first with `repose rm PROJECT`.\n", ExitPaymentRequired},
		{"several", &APIError{Code: "invalid", Message: "…",
			Detail: map[string]any{"reason": "project_limit", "limit": float64(100), "projects": float64(98), "requested": float64(5)}},
			"You have 98 of the 100 projects an account can have, running or stopped, and 5 more would make 103. Destroy some first with `repose rm PROJECT`.\n", ExitPaymentRequired},
		{"older api", &APIError{Code: "invalid", Message: "you have 6 of 6 projects; destroy one, or upgrade your plan at https://repose.herakraft.co/billing",
			Detail: map[string]any{"limit": float64(6), "projects": float64(6)}},
			"You have 6 of the 6 projects an account can have, running or stopped. Destroy one first with `repose rm PROJECT`.\n", ExitPaymentRequired},
		{"other invalid", &APIError{Code: "invalid", Message: "name must match [A-Za-z0-9._-]{1,64}"},
			"Name must match [A-Za-z0-9._-]{1,64} (invalid).\n", ExitGeneric},
		{"other reason", &APIError{Code: "invalid", Message: "something else", Detail: map[string]any{"reason": "other", "limit": float64(1), "projects": float64(1)}},
			"Something else (invalid).\n", ExitGeneric},
	} {
		var buf bytes.Buffer
		if code := exitCodeFor(c.err, &buf); code != c.code || buf.String() != c.want {
			t.Errorf("%s: exit %d, printed %q, want %q", c.name, code, buf.String(), c.want)
		}
	}
}
