package billing_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// recordSender records the SetBurst commands the allowance sends.
type recordSender struct {
	mu   sync.Mutex
	sent []*hostdv1.SetBurst
	fail bool
}

func (r *recordSender) Send(_ context.Context, _ uuid.UUID, cmd *hostdv1.Command) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return context.DeadlineExceeded
	}
	if c := cmd.GetSetBurst(); c != nil {
		r.sent = append(r.sent, c)
	}
	return nil
}

func (r *recordSender) take() []*hostdv1.SetBurst {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.sent
	r.sent = nil
	return out
}

// burstHost puts the account's project on a host and says whether that
// host names the `burst` feature.
func burstHost(t *testing.T, pool *db.Pool, a account, bursts bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	hid := store.NewID()
	features := "{}"
	if bursts {
		features = "{burst}"
	}
	if _, err := pool.Exec(ctx, "insert into hosts (id, name, state, features) values ($1, $2, 'ready', $3)", hid, "h"+hid.String()[24:], features); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update projects set host_id = $2 where id = $1", a.ProjectID, hid); err != nil {
		t.Fatal(err)
	}
	return hid
}

// addBurst writes one more hour of burst into usage_hours for the
// project, the way the rollup would.
var burstHour = map[uuid.UUID]int{}

func addBurst(t *testing.T, pool *db.Pool, a account, coreHours float64) {
	t.Helper()
	ns := int64(coreHours * float64(billing.NsPerCoreHour))
	burstHour[a.ProjectID]++
	hour := a.Period.Start.Add(time.Duration(burstHour[a.ProjectID]) * time.Hour)
	if _, err := pool.Exec(context.Background(), `insert into usage_hours (project_id, hour, class, running_seconds, gb_alloc, egress_bytes, burst_cpu_ns, period_start, period_end, price_version)
		values ($1, $2, 'large', 3600, 40, 0, $3, $4, $5, 'plan-v1')`,
		a.ProjectID, hour, ns, a.Period.Start, a.Period.End); err != nil {
		t.Fatal(err)
	}
}

// Under the allowance nothing is withdrawn; past it every guest of the
// account is told to stop bursting, once; and at the next period the
// cores come back (DECISIONS I-1128).
func TestBurstAllowanceStopsAtThePlansCoreHours(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	burstHost(t, pool, a, true)
	send := &recordSender{}
	al := billing.NewAllowance(pool, send, quiet())
	al.Now = func() time.Time { return a.Period.Start.Add(10 * 24 * time.Hour) }

	// Solo includes 20 core-hours. Fourteen used: nothing is withdrawn,
	// but the guest is told it may burst, because the api has to say so
	// once.
	addBurst(t, pool, a, 14)
	if _, err := al.Run(ctx); err != nil {
		t.Fatal(err)
	}
	got := send.take()
	if len(got) != 1 || !got[0].Allowed {
		t.Fatalf("under the allowance the api sent %v; want one allowed=true", got)
	}
	// A second tick with nothing changed sends nothing.
	if _, err := al.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := send.take(); len(got) != 0 {
		t.Fatalf("an unchanged tick re-sent %v", got)
	}

	// Another 8 core-hours takes the period past 20.
	addBurst(t, pool, a, 8)
	if _, err := al.Run(ctx); err != nil {
		t.Fatal(err)
	}
	got = send.take()
	if len(got) != 1 || got[0].Allowed {
		t.Fatalf("past the allowance the api sent %v; want one allowed=false", got)
	}
	if _, err := al.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := send.take(); len(got) != 0 {
		t.Fatalf("the withdrawal was repeated: %v", got)
	}

	// Nothing was stopped and nothing was charged: burst past the
	// allowance costs the user their extra cores and nothing else.
	var running int
	if err := pool.QueryRow(ctx, "select count(*) from projects where user_id = $1 and state = 'running'", a.UserID).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if running != 1 {
		t.Fatalf("machines running after the allowance ran out: %d; want 1", running)
	}
	var charges int
	if err := pool.QueryRow(ctx, "select count(*) from overage_charges").Scan(&charges); err != nil {
		t.Fatal(err)
	}
	if charges != 0 {
		t.Fatalf("burst wrote %d overage charges; overage is not built", charges)
	}

	// The next period starts clean, so the cores come back. Polar's
	// webhook rolls the subscription's period forward; nothing else
	// changes.
	next := billing.Period{Start: a.Period.End, End: a.Period.End.AddDate(0, 1, 0)}
	if _, err := pool.Exec(ctx, "update subscriptions set period_start = $2, period_end = $3 where id = $1", a.SubID, next.Start, next.End); err != nil {
		t.Fatal(err)
	}
	al.Now = func() time.Time { return next.Start.Add(24 * time.Hour) }
	if _, err := al.Run(ctx); err != nil {
		t.Fatal(err)
	}
	got = send.take()
	if len(got) != 1 || !got[0].Allowed {
		t.Fatalf("at the next period the api sent %v; want one allowed=true", got)
	}
}

// A host that does not name the `burst` feature is never sent a
// SetBurst: it bursts nothing, and an older hostd answers the command
// invalid_argument.
func TestBurstAllowanceSkipsAHostThatDoesNotBurst(t *testing.T) {
	pool := testdb.Open(t)
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	burstHost(t, pool, a, false)
	addBurst(t, pool, a, 40)
	send := &recordSender{}
	al := billing.NewAllowance(pool, send, quiet())
	al.Now = func() time.Time { return a.Period.Start.Add(10 * 24 * time.Hour) }
	if _, err := al.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := send.take(); len(got) != 0 {
		t.Fatalf("a host without the feature was sent %v", got)
	}
}

// A send that fails is retried at the next tick: nothing is remembered
// as sent that the host never got.
func TestBurstAllowanceRetriesAFailedSend(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	burstHost(t, pool, a, true)
	addBurst(t, pool, a, 40)
	send := &recordSender{fail: true}
	al := billing.NewAllowance(pool, send, quiet())
	al.Now = func() time.Time { return a.Period.Start.Add(10 * 24 * time.Hour) }
	if _, err := al.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := send.take(); len(got) != 0 {
		t.Fatalf("a failed send was recorded: %v", got)
	}
	send.mu.Lock()
	send.fail = false
	send.mu.Unlock()
	if _, err := al.Run(ctx); err != nil {
		t.Fatal(err)
	}
	got := send.take()
	if len(got) != 1 || got[0].Allowed {
		t.Fatalf("the retry sent %v; want one allowed=false", got)
	}
}

// The three plans' allowances, and the arithmetic between nanoseconds
// and core-hours that every figure a person reads goes through.
func TestBurstAllowancesAndCoreHours(t *testing.T) {
	for _, tc := range []struct {
		plan  string
		hours int
	}{{"solo", 20}, {"plus", 50}, {"pro", 120}} {
		p, ok := billing.PlanByID(tc.plan)
		if !ok {
			t.Fatalf("no plan %q", tc.plan)
		}
		if p.BurstCoreHours != tc.hours {
			t.Errorf("%s includes %d burst core-hours; want %d", tc.plan, p.BurstCoreHours, tc.hours)
		}
		if p.BurstAllowanceNs() != int64(tc.hours)*billing.NsPerCoreHour {
			t.Errorf("%s allowance in ns = %d", tc.plan, p.BurstAllowanceNs())
		}
	}
	for _, tc := range []struct {
		ns   int64
		want float64
	}{{0, 0}, {-5, 0}, {billing.NsPerCoreHour, 1}, {billing.NsPerCoreHour * 3 / 2, 1.5}, {billing.NsPerCoreHour / 100, 0}} {
		if got := billing.BurstCoreHours(tc.ns); got != tc.want {
			t.Errorf("BurstCoreHours(%d) = %v; want %v", tc.ns, got, tc.want)
		}
	}
}
