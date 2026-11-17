// Package triggerspec parses a trigger's schedule and says when it fires
// next (DECISIONS I-1252). The CLI parses a spec before it sends one, so a
// typo exits 2 with the grammar and no round trip, and the api parses it
// again as the clock that fires it, so both agree on one grammar.
//
// The grammar is small on purpose. systemd's calendar language is the
// other choice and was refused: the api would have to carry a parser for
// a language the user does not see anywhere else in repose, and
// `OnCalendar=Mon..Fri 07:00` with no zone fires at 07:00 UTC, which
// looks wrong to everyone who writes it.
//
//	every 15m           every 15 minutes from when it was added (15m..24h)
//	daily 03:00         every day
//	weekdays 07:00      Monday to Friday
//	weekends 10:00      Saturday and Sunday
//	mon,thu 18:30       those days
//	monthly 1 09:00     that day of the month (1..28)
//
// A zone name may end any spec (`daily 03:00 UTC`,
// `weekdays 07:00 Europe/Berlin`). Without one the caller's zone is
// stored, which the CLI already sends (I-104), so 07:00 is the user's
// 07:00.
package triggerspec

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	// The zone database is in the binary. The CLI runs on a laptop and
	// the api in a scratch container, and a zone the host does not have
	// would otherwise turn `weekdays 07:00 Europe/Berlin` into UTC.
	_ "time/tzdata"
)

// Kinds of spec.
const (
	KindEvery    = "every"
	KindDaily    = "daily"
	KindWeekdays = "weekdays"
	KindWeekends = "weekends"
	KindDays     = "days"
	KindMonthly  = "monthly"
)

// MinEvery and MaxEvery bound `every D`. Four runs an hour against the
// hour's cap of four (features/triggers.md §10) is what it is, and an
// agent run that starts every five minutes does not finish before the
// next one starts.
const (
	MinEvery = 15 * time.Minute
	MaxEvery = 24 * time.Hour
)

// MaxSpec bounds a spec, so no error echoes a page of someone's input
// and no row holds one.
const MaxSpec = 64

// Spec is a parsed schedule. The zero value is not a spec; use Parse.
type Spec struct {
	Kind  string         // one of the Kind constants
	Text  string         // the spec, canonical, without the zone
	Zone  string         // an IANA name; never empty after Parse
	At    time.Duration  // time of day, for every kind but `every`
	Every time.Duration  // the interval, for KindEvery
	Days  []time.Weekday // the days, for KindDays
	Day   int            // the day of the month, for KindMonthly
	loc   *time.Location
}

// ErrSpec is every parse failure. The CLI prints it and exits 2.
var ErrSpec = errors.New("not a schedule")

var weekdayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday,
	"wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday,
	"sat": time.Saturday,
}

// Parse reads a spec. zone is the caller's zone, used when the spec names
// none; an empty zone means UTC.
func Parse(spec, zone string) (Spec, error) {
	if len(spec) > MaxSpec {
		return Spec{}, fmt.Errorf("%w: a schedule is at most %d characters", ErrSpec, MaxSpec)
	}
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(spec)))
	if len(fields) == 0 {
		return Spec{}, fmt.Errorf("%w: it is empty", ErrSpec)
	}
	// The zone is a field past the ones its kind takes, and nothing is
	// guessed: `monthly` takes three words and every other kind two, so a
	// fourth or a third is the zone. Guessing by shape read the `22m30s`
	// of `every 22m30s` as a zone and answered the wrong error.
	want := 2
	if fields[0] == KindMonthly {
		want = 3
	}
	orig := strings.Fields(strings.TrimSpace(spec))
	switch {
	case len(fields) == want+1:
		last := orig[len(orig)-1]
		if !zoneRe.MatchString(last) {
			return Spec{}, fmt.Errorf("%w: %s is not a time zone; write UTC or a name like Europe/Berlin", ErrSpec, cut(last))
		}
		loc, err := time.LoadLocation(last)
		if err != nil {
			return Spec{}, fmt.Errorf("%w: %s is not a time zone; write UTC or a name like Europe/Berlin", ErrSpec, cut(last))
		}
		zone = loc.String()
		fields = fields[:len(fields)-1]
	case len(fields) > want+1:
		return Spec{}, fmt.Errorf("%w: %s has more words than a schedule takes", ErrSpec, cut(strings.Join(fields, " ")))
	}
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return Spec{}, fmt.Errorf("%w: %s is not a time zone", ErrSpec, zone)
	}
	s := Spec{Zone: loc.String(), loc: loc}

	head := fields[0]
	switch head {
	case KindEvery:
		if len(fields) != 2 {
			return Spec{}, fmt.Errorf("%w: write `every 15m`", ErrSpec)
		}
		d, err := time.ParseDuration(fields[1])
		if err != nil {
			return Spec{}, fmt.Errorf("%w: %s is not a duration; write `every 15m`", ErrSpec, fields[1])
		}
		if d < MinEvery || d > MaxEvery {
			return Spec{}, fmt.Errorf("%w: `every` takes 15m to 24h", ErrSpec)
		}
		if d%time.Minute != 0 {
			return Spec{}, fmt.Errorf("%w: `every` takes whole minutes", ErrSpec)
		}
		s.Kind, s.Every = KindEvery, d
		s.Text = s.canonical()
		return s, nil

	case KindMonthly:
		if len(fields) != 3 {
			return Spec{}, fmt.Errorf("%w: write `monthly 1 09:00`", ErrSpec)
		}
		day, err := strconv.Atoi(fields[1])
		if err != nil || strings.ContainsAny(fields[1], "+-") || day < 1 || day > 28 {
			return Spec{}, fmt.Errorf("%w: monthly takes a day from 1 to 28, so that every month has it", ErrSpec)
		}
		at, err := parseAt(fields[2])
		if err != nil {
			return Spec{}, err
		}
		s.Kind, s.Day, s.At = KindMonthly, day, at
		s.Text = s.canonical()
		return s, nil

	case KindDaily, KindWeekdays, KindWeekends:
		if len(fields) != 2 {
			return Spec{}, fmt.Errorf("%w: write `%s 07:00`", ErrSpec, head)
		}
		at, err := parseAt(fields[1])
		if err != nil {
			return Spec{}, err
		}
		s.Kind, s.At = head, at
		s.Text = s.canonical()
		return s, nil

	default:
		if len(fields) != 2 {
			return Spec{}, fmt.Errorf("%w: %s is not a schedule; `repose help triggers` has the six shapes", ErrSpec, strings.Join(fields, " "))
		}
		days, err := parseDays(fields[0])
		if err != nil {
			return Spec{}, err
		}
		at, err := parseAt(fields[1])
		if err != nil {
			return Spec{}, err
		}
		s.Kind, s.Days, s.At = KindDays, days, at
		s.Text = s.canonical()
		return s, nil
	}
}

// zoneRe is the zone a spec may name: `UTC`, or an IANA `Region/City`
// (`Europe/Berlin`, `America/Argentina/Salta`). An abbreviation such as
// `EST` is refused even where the host has it, because it is a fixed
// offset that ignores the summer the user lives in: `daily 07:00 EST`
// fires at 08:00 their time for half the year.
var zoneRe = regexp.MustCompile(`^(UTC|[A-Za-z][A-Za-z_-]+(/[A-Za-z0-9+_-]+){1,2})$`)

// cut keeps a bad field out of a long error line.
func cut(f string) string {
	if len(f) > 32 {
		return f[:32] + "…"
	}
	return f
}

func parseAt(f string) (time.Duration, error) {
	h, m, ok := strings.Cut(f, ":")
	if !ok {
		return 0, fmt.Errorf("%w: %s is not a time of day; write 07:00", ErrSpec, f)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || len(m) != 2 || strings.ContainsAny(f, "+-") ||
		hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("%w: %s is not a time of day; write 07:00", ErrSpec, f)
	}
	return time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute, nil
}

func parseDays(f string) ([]time.Weekday, error) {
	var days []time.Weekday
	seen := map[time.Weekday]bool{}
	for _, name := range strings.Split(f, ",") {
		d, ok := weekdayNames[name]
		if !ok {
			return nil, fmt.Errorf("%w: %s is not a day; write mon, tue, wed, thu, fri, sat or sun", ErrSpec, name)
		}
		if !seen[d] {
			seen[d], days = true, append(days, d)
		}
	}
	// Monday first, so `sat,sun` is not stored as `sun,sat`.
	slices.SortFunc(days, func(a, b time.Weekday) int { return isoDay(a) - isoDay(b) })
	return days, nil
}

// everyText writes an interval of whole minutes as `45m`, `1h` or
// `1h30m`, which is how the user typed it and how `time.Duration` does
// not (`1h0m0s`).
func everyText(d time.Duration) string {
	h, m := int(d/time.Hour), int(d%time.Hour/time.Minute)
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dh%dm", h, m)
	}
}

// isoDay numbers a weekday from Monday, which is how a week is written.
func isoDay(d time.Weekday) int {
	if d == time.Sunday {
		return 7
	}
	return int(d)
}

// canonical is the one spelling of a spec. `7:00`, `07:00` and `THU,MON`
// are all accepted and all stored one way, so a user who reads the row
// back, a table and a `--json` field never show two spellings of one
// schedule.
func (s Spec) canonical() string {
	hhmm := fmt.Sprintf("%02d:%02d", int(s.At/time.Hour), int(s.At%time.Hour/time.Minute))
	switch s.Kind {
	case KindEvery:
		return "every " + everyText(s.Every)
	case KindMonthly:
		return fmt.Sprintf("monthly %d %s", s.Day, hhmm)
	case KindDays:
		names := make([]string, 0, len(s.Days))
		for _, d := range s.Days {
			names = append(names, strings.ToLower(d.String()[:3]))
		}
		return strings.Join(names, ",") + " " + hhmm
	default:
		return s.Kind + " " + hhmm
	}
}

// Next is the first fire after t, in the spec's zone. anchor is when the
// trigger was added, which only `every D` reads: without an anchor an
// `every 15m` added at 07:07 would drift by a tick every time the row is
// written.
//
// Two clock oddities, both the zone's and neither worth a flag:
// a time the clock skips at a spring-forward fires when the clock passes
// it (02:30 in a zone that jumps to 03:00 fires at 03:30), and a time the
// clock repeats at an autumn-back fires once, on the first pass.
func (s Spec) Next(t, anchor time.Time) time.Time {
	loc := s.loc
	if loc == nil {
		loc = time.UTC
	}
	t = t.In(loc).Truncate(time.Second)
	switch s.Kind {
	case KindEvery:
		if anchor.IsZero() {
			anchor = t
		}
		a := anchor.In(loc).Truncate(time.Second)
		steps := t.Sub(a) / s.Every
		next := a.Add((steps + 1) * s.Every)
		for !next.After(t) {
			next = next.Add(s.Every)
		}
		return next
	}
	// Every other kind fires at a time of day; walk days from today. The
	// fire is built as a wall clock in the zone and never as midnight
	// plus a duration: a day that is 23 or 25 hours long would move it
	// (measured: `daily 07:00 Europe/Berlin` fired at 08:00 on
	// 2026-03-29 and 06:00 on 2026-10-25). time.Date normalising a time
	// the clock skips is what gives the documented 03:30 for a 02:30.
	hh, mm := int(s.At/time.Hour), int(s.At%time.Hour/time.Minute)
	for i := range 400 {
		day := time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, i)
		if !s.fires(day) {
			continue
		}
		at := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc)
		if at.After(t) {
			return at
		}
	}
	return time.Time{}
}

// fires says whether a day is one of the spec's.
func (s Spec) fires(day time.Time) bool {
	switch s.Kind {
	case KindDaily:
		return true
	case KindWeekdays:
		return day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
	case KindWeekends:
		return day.Weekday() == time.Saturday || day.Weekday() == time.Sunday
	case KindDays:
		for _, d := range s.Days {
			if d == day.Weekday() {
				return true
			}
		}
		return false
	case KindMonthly:
		return day.Day() == s.Day
	}
	return false
}

// String is the spec and its zone, as `repose triggers add` prints them
// and as the row stores them.
func (s Spec) String() string {
	if s.Zone == "" || s.Zone == "UTC" {
		return s.Text + " UTC"
	}
	return s.Text + " " + s.Zone
}
