package api

import (
	"testing"

	"github.com/heracraft/repose/internal/billing"
)

// The fake's plan_limit sentence is the gate's, word for word: the CLI
// rewrites the gate's words (namePlanFix), and a fake that said it
// another way kept every test against it from seeing that (I-633).
func TestPlanLimitMessageIsTheGates(t *testing.T) {
	const url = "https://repose.herakraft.co/billing"
	for _, c := range []struct {
		plan  string
		class string
		slugs []string
	}{
		{"solo", "large", nil},
		{"solo", "large", []string{"todo-app"}},
		{"solo", "small", []string{"api", "web"}},
		{"plus", "large", []string{"a", "b", "c"}},
		{"solo", "xl", nil},
		{"plus", "xl", []string{"a"}},
	} {
		var fp PlanDef
		for _, p := range Plans {
			if p.ID == c.plan {
				fp = p
			}
		}
		bp, ok := billing.PlanByID(c.plan)
		if !ok {
			t.Fatalf("billing has no plan %s", c.plan)
		}
		got := planLimitMessage(fp, c.class, c.slugs)
		want := billing.PlanLimitMessage(bp, c.class, c.slugs, url)
		if got != want {
			t.Errorf("%s %s %v:\n fake %q\n gate %q", c.plan, c.class, c.slugs, got, want)
		}
	}
}
