package billing

import (
	"context"

	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/db"
)

// The fleet's money gauges (DECISIONS I-802). The audit before launch
// found no dashboard reading a revenue number at all: seats were counted
// and what they earn was not, so the one figure that says whether a sold
// seat covers its host was in nobody's view. These two gauges are that
// figure's numerator; `repose_api_seats_total` and `_held` are its
// denominator.
//
// They are set from the minute tick on every replica and take no
// advisory lock. The seat gauges are set by the waitlist tick, which runs
// under a lock, so only the leader publishes them; a gauge that one
// replica of two publishes is a gauge that is absent half the time it is
// scraped, and a scrape that lands on the quiet replica reads nothing.
// These cost one grouped count over a table of tens of rows, so every
// replica can afford the truth.

// liveStatuses are the statuses these gauges publish. They are the three
// the seat count sums (`waitlist.count`), so MRR and seats are read off
// the same set of subscriptions and cannot disagree. `paused` is a fourth
// status the unique index treats as live; it is left out here for that
// consistency, and a paused subscription earning nothing is the safer way
// to be wrong.
var liveStatuses = []string{"trialing", "active", "past_due"}

// Fleet publishes the subscription and revenue gauges. It takes no
// logger: Refresh returns its error and the tick that calls it logs,
// which keeps one line per failure instead of two.
type Fleet struct {
	pool *db.Pool
	m    *metrics.M
}

// NewFleet builds the publisher.
func NewFleet(pool *db.Pool, m *metrics.M) *Fleet {
	return &Fleet{pool: pool, m: m}
}

// Refresh counts the live subscriptions and publishes both gauges.
//
// Every plan and status pair is set, zero included, rather than the rows
// the query returned: Reset() on a GaugeVec drops a series Prometheus has
// seen, and a panel dividing by a dropped series renders blank where it
// should render zero. The pairs are a nine-cell product of two fixed
// lists, so enumerating them costs nothing.
func (f *Fleet) Refresh(ctx context.Context) error {
	type key struct{ plan, status string }
	counts := map[key]int{}
	intro := map[key]int{}
	rows, err := f.pool.Query(ctx, `select plan, status, count(*)::integer,
			count(*) filter (where intro and (intro_until is null or intro_until > now()))::integer
		from subscriptions where status in ('trialing','active','past_due') group by 1, 2`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k key
		var n, withIntro int
		if err := rows.Scan(&k.plan, &k.status, &n, &withIntro); err != nil {
			return err
		}
		counts[k] = n
		intro[k] = withIntro
	}
	if err := rows.Err(); err != nil {
		return err
	}
	cents := map[string]int64{}
	for _, status := range liveStatuses {
		cents[status] = 0
		for _, p := range Plans {
			k := key{p.ID, status}
			n, withIntro := counts[k], intro[k]
			f.m.Subscriptions.WithLabelValues(p.ID, status).Set(float64(n))
			// A subscription on the founding price earns IntroCents until
			// its offer ends; the rest earn the list price. Both come from
			// the plan table, because no price is stored per subscription
			// (PRICING.md "Changing prices").
			if !p.HasIntro() {
				withIntro = 0
			}
			cents[status] += int64(n-withIntro)*p.PriceCents + int64(withIntro)*p.IntroCents
		}
	}
	for status, c := range cents {
		f.m.MRRCents.WithLabelValues(status).Set(float64(c))
	}
	return nil
}
