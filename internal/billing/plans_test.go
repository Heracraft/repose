package billing_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/billing"
)

// TestPlansMatchPricingDoc parses the plan table in docs/PRICING.md and
// fails when plans.go disagrees with it (PRICING.md "Changing prices":
// the numbers live in two places and nowhere else).
func TestPlansMatchPricingDoc(t *testing.T) {
	b, err := os.ReadFile("../../docs/PRICING.md")
	if err != nil {
		t.Fatal(err)
	}
	// | Solo | $29 a month | 8 GB: ... | 100 GB | 250 GB | 1 |
	row := regexp.MustCompile(`(?m)^\| (Solo|Plus|Pro) \| \$(\d+) a month \| (\d+) GB[^|]*\| (\d+) GB \| (\d+) GB \| (\d+) \|`)
	found := map[string]bool{}
	for _, m := range row.FindAllStringSubmatch(string(b), -1) {
		plan, ok := billing.PlanByID(strings.ToLower(m[1]))
		if !ok {
			t.Fatalf("PRICING.md names plan %q, plans.go has no such plan", m[1])
		}
		found[plan.ID] = true
		n := func(s string) int { v, _ := strconv.Atoi(s); return v }
		if plan.PriceCents != int64(n(m[2]))*100 || plan.MemoryGB != n(m[3]) || plan.DiskGB != n(m[4]) || plan.EgressGB != n(m[5]) || plan.Seats != n(m[6]) {
			t.Errorf("%s: PRICING.md says $%s, %s GB, %s GB disk, %s GB egress, %s seats; plans.go has %+v", plan.Name, m[2], m[3], m[4], m[5], m[6], plan)
		}
	}
	for _, p := range billing.Plans {
		if !found[p.ID] {
			t.Errorf("PRICING.md's table has no row for %s", p.Name)
		}
	}
	doc := string(b)
	for _, want := range []string{"$0.05 a GB", "10 on Solo, 25 on Plus, 50 on Pro", "four times the", "Seven days free"} {
		if !strings.Contains(doc, want) {
			t.Errorf("PRICING.md no longer says %q", want)
		}
	}
	if billing.OveragePerGBCents != 5 || billing.EgressHardStopMultiplier != 4 || billing.Solo.ProjectLimit != 10 || billing.Plus.ProjectLimit != 25 || billing.Pro.ProjectLimit != 50 || billing.Solo.TrialDays != 7 {
		t.Errorf("plans.go constants drifted from PRICING.md")
	}
	// The introductory price is a sentence under the table (DECISIONS I-497).
	intro := regexp.MustCompile(`Solo costs \$(\d+) a month for its first (\d+) months`).FindStringSubmatch(doc)
	if intro == nil {
		t.Errorf("PRICING.md no longer states Solo's introductory price")
	} else if n, _ := strconv.Atoi(intro[1]); int64(n)*100 != billing.Solo.IntroCents {
		t.Errorf("PRICING.md's introductory price $%s; plans.go has %d cents", intro[1], billing.Solo.IntroCents)
	} else if m, _ := strconv.Atoi(intro[2]); m != billing.Solo.IntroMonths {
		t.Errorf("PRICING.md's introductory months %s; plans.go has %d", intro[2], billing.Solo.IntroMonths)
	}
	if billing.Plus.HasIntro() || billing.Pro.HasIntro() {
		t.Errorf("only Solo has an introductory price")
	}
	if billing.Solo.IntroDiscountCents() != 900 {
		t.Errorf("Solo's discount %d cents, want 900", billing.Solo.IntroDiscountCents())
	}
	if billing.PriceVersion != "plan-v1" {
		t.Errorf("PriceVersion %q", billing.PriceVersion)
	}
}

func TestOverageAndClassMemory(t *testing.T) {
	gb := int64(1) << 30
	for _, c := range []struct {
		plan  billing.Plan
		bytes int64
		gb    int64
		cents int64
	}{
		{billing.Solo, 20 * gb, 0, 0},
		{billing.Solo, 250 * gb, 0, 0},
		{billing.Solo, 250*gb + 1, 1, 5},
		{billing.Solo, 300 * gb, 50, 250},
		{billing.Plus, 500 * gb, 0, 0},
		{billing.Plus, 600*gb + gb/2, 101, 505},
		{billing.Pro, 1000 * gb, 0, 0},
		{billing.Pro, 1200 * gb, 200, 1000},
	} {
		g, cents := billing.OverageCents(c.plan, c.bytes)
		if g != c.gb || cents != c.cents {
			t.Errorf("%s %d bytes: %d GB %d cents, want %d GB %d cents", c.plan.ID, c.bytes, g, cents, c.gb, c.cents)
		}
	}
	if billing.ClassMemoryGB("small") != 4 || billing.ClassMemoryGB("large") != 8 || billing.ClassMemoryGB("xl") != 16 {
		t.Error("class memory drifted from docs/interfaces/README.md")
	}
	if !billing.Pro.AllowsXL() || !billing.Plus.AllowsXL() || billing.Solo.AllowsXL() {
		t.Error("xl needs 16 GB: Plus and Pro")
	}
	if billing.SmallestFor("xl") != billing.Plus || billing.SmallestFor("large") != billing.Solo {
		t.Error("SmallestFor: xl names Plus, large Solo")
	}
	if billing.Solo.EgressHardStopBytes() != 1000*gb || billing.Plus.EgressHardStopBytes() != 2000*gb || billing.Pro.EgressHardStopBytes() != 4000*gb {
		t.Error("the hard stop is 1 TB on Solo, 2 TB on Plus, 4 TB on Pro")
	}
	r := billing.Price(billing.Inputs{Class: "large", RunningSeconds: 7200, GBAlloc: 40, EgressBytes: -5})
	if r.RunningSeconds != 3600 || r.EgressBytes != 0 || r.CostCents != 0 || r.EgressCents != 0 || r.GBAlloc != 40 {
		t.Errorf("Price normalises and prices nothing: %+v", r)
	}
}
