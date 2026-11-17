package triggerspec_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/triggerspec"
)

// mustLoad fails rather than skips: the zone database is in the binary
// (time/tzdata), so a zone that will not load is a bug and not a thin
// container. A t.Skipf here is how the five zone promises would pass
// without ever running.
func mustLoad(t *testing.T, zone string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatalf("zone %s will not load, so time/tzdata is not linked in: %v", zone, err)
	}
	return loc
}

func TestEveryShapeParses(t *testing.T) {
	for _, c := range []struct {
		spec string
		kind string
	}{
		{"every 15m", triggerspec.KindEvery},
		{"every 24h", triggerspec.KindEvery},
		{"daily 03:00", triggerspec.KindDaily},
		{"weekdays 07:00", triggerspec.KindWeekdays},
		{"weekends 10:00", triggerspec.KindWeekends},
		{"mon,thu 18:30", triggerspec.KindDays},
		{"MON,THU 18:30", triggerspec.KindDays},
		{"monthly 1 09:00", triggerspec.KindMonthly},
		{"monthly 28 23:59", triggerspec.KindMonthly},
	} {
		s, err := triggerspec.Parse(c.spec, "UTC")
		if err != nil {
			t.Errorf("%q: %v", c.spec, err)
			continue
		}
		if s.Kind != c.kind {
			t.Errorf("%q: kind %q, want %q", c.spec, s.Kind, c.kind)
		}
	}
}

func TestABadSpecSaysWhatToWrite(t *testing.T) {
	for _, c := range []struct{ spec, want string }{
		{"", "it is empty"},
		{"hourly", "is not a schedule"},
		{"every 5m", "15m to 24h"},
		{"every", "write `every 15m`"},
		{"every 1m", "15m to 24h"},
		{"every 48h", "15m to 24h"},
		{"every 90s", "15m to 24h"},
		{"every 22m30s", "whole minutes"},
		{"every banana", "is not a duration"},
		{"daily", "write `daily 07:00`"},
		{"daily 7:0", "is not a time of day"},
		{"daily 25:00", "is not a time of day"},
		{"daily 07:60", "is not a time of day"},
		{"monthly 29 09:00", "a day from 1 to 28"},
		{"monthly 0 09:00", "a day from 1 to 28"},
		{"monthly 09:00", "write `monthly 1 09:00`"},
		{"funday 09:00", "is not a day"},
		{"tomorrow", "is not a schedule"},
		{"daily 03:00 Mars/Olympus", "is not a time zone"},
		{"daily 03:00 EST", "is not a time zone"},
		{"daily 03:00 MST7MDT", "is not a time zone"},
		{"daily 03:00 europe/berlin", "is not a time zone"},
		{"daily 07:00 " + strings.Repeat("x", 80), "at most 64 characters"},
		{"monthly +1 09:00", "a day from 1 to 28"},
		{"daily +7:00", "is not a time of day"},
	} {
		_, err := triggerspec.Parse(c.spec, "UTC")
		if err == nil {
			t.Errorf("%q parsed", c.spec)
			continue
		}
		if !errors.Is(err, triggerspec.ErrSpec) {
			t.Errorf("%q: %v is not ErrSpec", c.spec, err)
		}
		if got := err.Error(); !contains(got, c.want) {
			t.Errorf("%q: %q does not say %q", c.spec, got, c.want)
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// A spec is accepted as the user types it and stored one way.
func TestASpecIsStoredInOneSpelling(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"daily 7:00", "daily 07:00"},
		{"DAILY 07:00", "daily 07:00"},
		{"  weekdays   7:05  ", "weekdays 07:05"},
		{"THU,MON 18:30", "mon,thu 18:30"},
		{"sat,sun 10:00", "sat,sun 10:00"},
		{"sun,sat 10:00", "sat,sun 10:00"},
		{"every 60m", "every 1h"},
		{"every 90m", "every 1h30m"},
		{"monthly 01 9:00", "monthly 1 09:00"},
	} {
		s, err := triggerspec.Parse(c.in, "UTC")
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if s.Text != c.want {
			t.Errorf("%q stored as %q, want %q", c.in, s.Text, c.want)
		}
	}
}

func TestTheZoneIsTheCallersUnlessTheSpecNamesOne(t *testing.T) {
	s, err := triggerspec.Parse("weekdays 07:00", "Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	if s.Zone != "Europe/Berlin" {
		t.Errorf("zone %q, want Europe/Berlin", s.Zone)
	}
	if got := s.String(); got != "weekdays 07:00 Europe/Berlin" {
		t.Errorf("String %q", got)
	}
	s, err = triggerspec.Parse("daily 03:00 UTC", "Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	if s.Zone != "UTC" {
		t.Errorf("zone %q, want UTC", s.Zone)
	}
	if s.Text != "daily 03:00" {
		t.Errorf("Text %q keeps the zone", s.Text)
	}
	// No zone anywhere is UTC, not the test machine's zone.
	s, err = triggerspec.Parse("daily 03:00", "")
	if err != nil || s.Zone != "UTC" {
		t.Errorf("zone %q err %v, want UTC", s.Zone, err)
	}
	// A zone that holds a digit or a region still loads.
	if _, err := triggerspec.Parse("daily 03:00 America/Argentina/Salta", "UTC"); err != nil {
		t.Errorf("region zone: %v", err)
	}
}

func TestNextWalksTheRightDays(t *testing.T) {
	berlin := mustLoad(t, "Europe/Berlin")
	// Friday 2026-11-20, 09:00 Berlin.
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, berlin)
	for _, c := range []struct {
		spec string
		want time.Time
	}{
		{"daily 03:00", time.Date(2026, 11, 21, 3, 0, 0, 0, berlin)},
		{"daily 09:00", time.Date(2026, 11, 21, 9, 0, 0, 0, berlin)}, // today's is gone
		{"daily 18:30", time.Date(2026, 11, 20, 18, 30, 0, 0, berlin)},
		{"weekdays 07:00", time.Date(2026, 11, 23, 7, 0, 0, 0, berlin)}, // Monday
		{"weekends 10:00", time.Date(2026, 11, 21, 10, 0, 0, 0, berlin)},
		{"mon,thu 18:30", time.Date(2026, 11, 23, 18, 30, 0, 0, berlin)},
		{"monthly 1 09:00", time.Date(2026, 12, 1, 9, 0, 0, 0, berlin)},
		{"monthly 28 09:00", time.Date(2026, 11, 28, 9, 0, 0, 0, berlin)},
	} {
		s, err := triggerspec.Parse(c.spec, "Europe/Berlin")
		if err != nil {
			t.Errorf("%q: %v", c.spec, err)
			continue
		}
		got := s.Next(now, time.Time{})
		if !got.Equal(c.want) {
			t.Errorf("%q: next %s, want %s", c.spec, got.Format(time.RFC3339), c.want.Format(time.RFC3339))
		}
	}
}

func TestEveryIsAnchoredAndDoesNotDrift(t *testing.T) {
	s, err := triggerspec.Parse("every 15m", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Date(2026, 11, 20, 7, 7, 0, 0, time.UTC)
	want := []time.Time{
		time.Date(2026, 11, 20, 7, 22, 0, 0, time.UTC),
		time.Date(2026, 11, 20, 7, 37, 0, 0, time.UTC),
		time.Date(2026, 11, 20, 7, 52, 0, 0, time.UTC),
	}
	// Each fire computed from the anchor, and each computed again from a
	// clock that is late: the series must not move.
	at := anchor
	for i, w := range want {
		got := s.Next(at, anchor)
		if !got.Equal(w) {
			t.Fatalf("fire %d: %s, want %s", i, got.Format(time.RFC3339), w.Format(time.RFC3339))
		}
		late := got.Add(43 * time.Second)
		if again := s.Next(late, anchor); !again.Equal(w.Add(15 * time.Minute)) {
			t.Errorf("fire %d from a late clock: %s, want %s", i, again.Format(time.RFC3339), w.Add(15*time.Minute).Format(time.RFC3339))
		}
		at = got
	}
	// A clock before the anchor still gives the first fire.
	if got := s.Next(anchor.Add(-time.Hour), anchor); !got.After(anchor.Add(-time.Hour)) {
		t.Errorf("before the anchor: %s", got)
	}
}

// The fire keeps its wall-clock time on a day the clock moves. This is
// the test the first version of Next did not have: building a fire as
// midnight plus a duration gave 08:00 on the 23-hour day and 06:00 on
// the 25-hour one, and a monotonicity walk over those days saw nothing
// wrong.
func TestTheWallClockHoldsOnBothTransitionDays(t *testing.T) {
	berlin := mustLoad(t, "Europe/Berlin")
	s, err := triggerspec.Parse("daily 07:00", "Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		from  time.Time
		day   int
		month time.Month
	}{
		{"spring forward (23 hours)", time.Date(2026, 3, 28, 12, 0, 0, 0, berlin), 29, time.March},
		{"autumn back (25 hours)", time.Date(2026, 10, 24, 12, 0, 0, 0, berlin), 25, time.October},
	} {
		got := s.Next(c.from, time.Time{})
		if got.Day() != c.day || got.Month() != c.month {
			t.Errorf("%s: fired on %s", c.name, got.Format(time.RFC3339))
			continue
		}
		if h, m := got.Hour(), got.Minute(); h != 7 || m != 0 {
			t.Errorf("%s: fired at %02d:%02d (%s), want 07:00 on the wall clock",
				c.name, h, m, got.Format(time.RFC3339))
		}
	}
	// And the whole year of a weekday spec keeps 07:00, which is what
	// "07:00 is their 07:00" means.
	w, err := triggerspec.Parse("weekdays 07:00", "Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, berlin)
	for range 260 {
		at = w.Next(at, time.Time{})
		if at.Hour() != 7 || at.Minute() != 0 {
			t.Fatalf("a fire at %s is not 07:00 on the wall clock", at.Format(time.RFC3339))
		}
	}
}

// Spring forward: Europe/Berlin jumps 02:00 to 03:00 on 2026-03-29, so
// 02:30 does not exist that day. The fire happens when the clock passes
// it, not never.
func TestATimeTheClockSkipsStillFires(t *testing.T) {
	berlin := mustLoad(t, "Europe/Berlin")
	s, err := triggerspec.Parse("daily 02:30", "Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 28, 12, 0, 0, 0, berlin)
	got := s.Next(now, time.Time{})
	want := time.Date(2026, 3, 29, 3, 30, 0, 0, berlin) // 02:30 normalised
	if !got.Equal(want) {
		t.Errorf("next %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if h := got.In(berlin).Hour(); h != 3 {
		t.Errorf("hour %d, want 3", h)
	}
}

// Autumn back: 2026-10-25 has 02:30 twice in Berlin. It fires once.
func TestATimeTheClockRepeatsFiresOnce(t *testing.T) {
	berlin := mustLoad(t, "Europe/Berlin")
	s, err := triggerspec.Parse("daily 02:30", "Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	first := s.Next(time.Date(2026, 10, 24, 12, 0, 0, 0, berlin), time.Time{})
	if first.Day() != 25 || first.Hour() != 2 || first.Minute() != 30 {
		t.Fatalf("first %s", first.Format(time.RFC3339))
	}
	next := s.Next(first, time.Time{})
	if next.Day() != 26 {
		t.Errorf("the repeated half hour fired twice: %s", next.Format(time.RFC3339))
	}
}

// The clock is the zone's, so a machine in UTC fires a Berlin 07:00 at
// 06:00 UTC in winter and 05:00 UTC in summer.
func TestTheStoredZoneHoldsAcrossTheYear(t *testing.T) {
	s, err := triggerspec.Parse("weekdays 07:00", "Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	winter := s.Next(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), time.Time{})
	summer := s.Next(time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC), time.Time{})
	if h := winter.UTC().Hour(); h != 6 {
		t.Errorf("winter fires at %02dZ, want 06Z", h)
	}
	if h := summer.UTC().Hour(); h != 5 {
		t.Errorf("summer fires at %02dZ, want 05Z", h)
	}
}

func TestNextIsAlwaysAfterNow(t *testing.T) {
	berlin := mustLoad(t, "Europe/Berlin")
	specs := []string{"every 15m", "daily 00:00", "weekdays 23:59", "weekends 00:00", "sun 12:00", "monthly 28 00:00"}
	start := time.Date(2026, 2, 27, 23, 59, 30, 0, berlin)
	for _, spec := range specs {
		s, err := triggerspec.Parse(spec, "Europe/Berlin")
		if err != nil {
			t.Fatal(err)
		}
		at := start
		for i := range 500 {
			next := s.Next(at, start)
			if next.IsZero() {
				t.Fatalf("%q: no next fire after %s", spec, at)
			}
			if !next.After(at) {
				t.Fatalf("%q: next %s is not after %s (step %d)", spec, next, at, i)
			}
			at = next
		}
	}
}
