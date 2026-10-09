package billing

import "time"

// A billing period is the subscription's current billing period as Polar
// reports it (subscriptions.period_start and period_end, I-289); PeriodFor
// is the fallback that walks whole months from an anchor when Polar has
// not set one yet, or for an account without a plan (the calendar month).
// Every usage_hours row records the period it belongs to, so the egress
// allowance is summed over the same hours the invoice covers.
//
// The anchor rule for short months: an anchor on the 31st bills on the
// 30th in November and the 28th in February, and returns to the 31st in
// March.

// Period is one billing cycle, half-open: [Start, End).
type Period struct {
	Start time.Time
	End   time.Time
}

// Hours is the period's actual length in hours, which is what the storage
// line is spread over (§5.4, "hours_in_period is the period's actual
// length in hours").
func (p Period) Hours() int {
	h := int(p.End.Sub(p.Start) / time.Hour)
	if h <= 0 {
		return DefaultPeriodHours
	}
	return h
}

// Contains reports whether an hour falls in the period.
func (p Period) Contains(t time.Time) bool {
	return !t.Before(p.Start) && t.Before(p.End)
}

// PeriodFor returns the billing period containing at, for an account whose
// cycle is anchored at anchor. A zero anchor falls back to the calendar
// month, which is what an account created before the anchor column existed
// has until the migration backfills it.
func PeriodFor(anchor, at time.Time) Period {
	at = at.UTC()
	if anchor.IsZero() {
		start := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
		return Period{Start: start, End: start.AddDate(0, 1, 0)}
	}
	anchor = anchor.UTC()
	if at.Before(anchor) {
		// Before the account existed: the period that ends at the anchor.
		return Period{Start: addMonths(anchor, -1), End: anchor}
	}
	// Walk whole months from the anchor. The number of months is bounded by
	// the account's age, and months are counted rather than added one at a
	// time so a decade-old account is still one step.
	n := int(at.Year()-anchor.Year())*12 + int(at.Month()) - int(anchor.Month())
	if n < 0 {
		n = 0
	}
	start := addMonths(anchor, n)
	for start.After(at) {
		n--
		start = addMonths(anchor, n)
	}
	for {
		next := addMonths(anchor, n+1)
		if next.After(at) {
			return Period{Start: start, End: next}
		}
		n++
		start = next
	}
}

// addMonths advances a timestamp by n calendar months, clamping the day to
// the last day of the target month rather than spilling into the next one
// (Go's AddDate turns 31 January plus a month into 3 March).
func addMonths(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	hh, mm, ss := t.Clock()
	total := int(m) - 1 + n
	y += floorDiv(total, 12)
	m = time.Month(mod(total, 12) + 1)
	if last := daysIn(y, m); d > last {
		d = last
	}
	return time.Date(y, m, d, hh, mm, ss, t.Nanosecond(), time.UTC)
}

func daysIn(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func mod(a, b int) int {
	r := a % b
	if r < 0 {
		r += b
	}
	return r
}
