package multiplexer

import "testing"

func TestNormalizeAndUnit(t *testing.T) {
	cases := []struct{ in, norm, unit string }{
		{"", Tmux, TmuxUnit},
		{"tmux", Tmux, TmuxUnit},
		{"herdr", Herdr, HerdrUnit},
		{"screen", Tmux, TmuxUnit},
		{"HERDR", Tmux, TmuxUnit},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.norm {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.norm)
		}
		if got := Unit(c.in); got != c.unit {
			t.Errorf("Unit(%q) = %q, want %q", c.in, got, c.unit)
		}
	}
	for _, n := range Names {
		if !Valid(n) {
			t.Errorf("Valid(%q) = false", n)
		}
	}
	if Valid("") || Valid("screen") {
		t.Error("Valid accepted a value outside Names")
	}
}
