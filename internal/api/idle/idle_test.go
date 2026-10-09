package idle

import (
	"strings"
	"testing"
	"time"
)

func TestSince(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }
	fresh := at(time.Minute)
	cases := []struct {
		name        string
		state       string
		started     *time.Time
		unreachable bool
		s           Signals
		want        *time.Time
	}{
		{"never used, up 25h", "running", at(25 * time.Hour), false, Signals{Newest: fresh}, at(25 * time.Hour)},
		{"up 23h", "running", at(23 * time.Hour), false, Signals{Newest: fresh}, nil},
		{"used 23h ago", "running", at(40 * time.Hour), false, Signals{Newest: fresh, LastUsed: at(23 * time.Hour)}, nil},
		{"used 30h ago", "running", at(40 * time.Hour), false, Signals{Newest: fresh, LastUsed: at(30 * time.Hour)}, at(30 * time.Hour)},
		{"use before this start", "running", at(25 * time.Hour), false, Signals{Newest: fresh, LastUsed: at(50 * time.Hour)}, at(25 * time.Hour)},
		{"stopped", "stopped", at(40 * time.Hour), false, Signals{Newest: fresh}, nil},
		{"no start time", "running", nil, false, Signals{Newest: fresh}, nil},
		{"host unreachable", "running", at(40 * time.Hour), true, Signals{Newest: fresh}, nil},
		{"no samples", "running", at(40 * time.Hour), false, Signals{}, nil},
		{"stale samples", "running", at(40 * time.Hour), false, Signals{Newest: at(time.Hour)}, nil},
	}
	for _, c := range cases {
		got, ok := Since(now, c.state, c.started, c.unreachable, c.s)
		if (c.want == nil) != !ok || (ok && !got.Equal(*c.want)) {
			t.Errorf("%s: got %v %v, want %v", c.name, got, ok, c.want)
		}
	}
}

func TestSummaryCarriesNoGuestContent(t *testing.T) {
	s := Summary("todo-app", "xl", 26*time.Hour+40*time.Minute)
	for _, want := range []string{"todo-app", "26h", "holds 16 GB of your plan's memory (xl) until you stop it", "`repose stop todo-app`."} {
		if !strings.Contains(s, want) {
			t.Errorf("summary %q lacks %q", s, want)
		}
	}
}
