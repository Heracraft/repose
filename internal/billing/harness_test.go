package billing_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

// quiet is a logger that keeps the test output readable; set
// BILLING_TEST_LOG to see what the jobs say.
func quiet() *slog.Logger {
	if os.Getenv("BILLING_TEST_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testConfig is a sandbox configuration pointed at the fake.
func testConfig(f *fakePolar) billing.Config {
	return billing.Config{AccessToken: "polar_oat_test", Env: billing.EnvSandbox, WebhookSecret: f.Secret(),
		ProductSolo: "prod_solo_test", ProductPlus: "prod_plus_test", ProductPro: "prod_pro_test", DiscountIntro: "dsc_intro_test",
		DashboardURL: "https://repose.herakraft.co", PortalReturnURL: "https://repose.herakraft.co/billing", BaseURL: f.URL(), Enforce: true}
}

// account is a seeded user with one project.
type account struct {
	UserID    uuid.UUID
	Handle    string
	Email     string
	ProjectID uuid.UUID
	Slug      string
	SubID     string
	Period    billing.Period
}

// period is the fixed billing period the seeded subscriptions carry.
func period() billing.Period {
	return billing.Period{Start: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
}

// seedAccount inserts a user with billing_status status and, when plan is
// not empty, a live subscription on it (status trialing|active|past_due
// from the account status), plus one project of class in state.
func seedAccount(t *testing.T, pool *db.Pool, plan, status, class, state string) account {
	t.Helper()
	ctx := context.Background()
	a := account{UserID: store.NewID(), ProjectID: store.NewID(), Period: period()}
	a.Handle = "u" + a.UserID.String()[24:]
	a.Email = a.Handle + "@example.test"
	a.Slug = "s" + a.ProjectID.String()[24:]
	if _, err := pool.Exec(ctx, `insert into users (id, handle, email, billing_status, has_card, billing_customer_id, created_at)
		values ($1, $2, $3, $4, $5, $6, $7)`, a.UserID, a.Handle, a.Email, status, plan != "", nullIf(plan == "", "ctm_"+a.Handle), a.Period.Start.Add(-24*time.Hour)); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if plan != "" {
		subStatus := map[string]string{"trial": billing.StatusTrialing, "active": billing.StatusActive, "past_due": billing.StatusPastDue}[status]
		if subStatus == "" {
			subStatus = billing.StatusActive
		}
		p, _ := billing.PlanByID(plan)
		a.SubID = "sub_" + a.Handle
		var trialEnd *time.Time
		if subStatus == billing.StatusTrialing {
			te := a.Period.Start.Add(7 * 24 * time.Hour)
			trialEnd = &te
		}
		if _, err := pool.Exec(ctx, `insert into subscriptions (id, user_id, customer_id, plan, status, seats, period_start, period_end, next_billed_at, trial_end, created_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $8, $9, $7)`, a.SubID, a.UserID, "ctm_"+a.Handle, plan, subStatus, p.Seats, a.Period.Start, a.Period.End, trialEnd); err != nil {
			t.Fatalf("seed subscription: %v", err)
		}
	}
	if class != "" {
		if _, err := pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, volume_bytes, guest_id, created_at)
			values ($1, $2, $3, $3, $4, $5, $6, $7, $8)`, a.ProjectID, a.UserID, a.Slug, class, state, int64(40)<<30, store.NewID(), a.Period.Start); err != nil {
			t.Fatalf("seed project: %v", err)
		}
	}
	return a
}

func nullIf(cond bool, v string) *string {
	if cond {
		return nil
	}
	return &v
}

// addProject adds another project to an account.
func addProject(t *testing.T, pool *db.Pool, a account, slug, class, state string, volume int64) uuid.UUID {
	t.Helper()
	id := store.NewID()
	if _, err := pool.Exec(context.Background(), `insert into projects (id, user_id, name, slug, class, state, volume_bytes, guest_id, created_at)
		values ($1, $2, $3, $3, $4, $5, $6, $7, $8)`, id, a.UserID, slug, class, state, volume, store.NewID(), a.Period.Start); err != nil {
		t.Fatalf("add project: %v", err)
	}
	return id
}

// held sets the bytes a project's volume holds, as the meter ingest does
// from a sample (DECISIONS I-585).
func held(t *testing.T, pool *db.Pool, projectID uuid.UUID, bytes int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), "update projects set disk_held_bytes = $2, disk_held_at = now() where id = $1", projectID, bytes); err != nil {
		t.Fatalf("held: %v", err)
	}
}

// usageHour writes one usage_hours row directly (the rollup's output).
func usageHour(t *testing.T, pool *db.Pool, projectID uuid.UUID, hour time.Time, class string, runningSeconds int, egress int64, p billing.Period) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `insert into usage_hours (project_id, hour, class, running_seconds, gb_alloc, egress_bytes, cost_cents, period_start, period_end, price_version)
		values ($1, $2, $3, $4, 40, $5, 0, $6, $7, 'plan-v1') on conflict (project_id, hour) do update set egress_bytes = excluded.egress_bytes, running_seconds = excluded.running_seconds`,
		projectID, hour.UTC(), class, runningSeconds, egress, p.Start, p.End); err != nil {
		t.Fatalf("usage hour: %v", err)
	}
}

// sample writes one meter_samples minute.
func sample(t *testing.T, pool *db.Pool, projectID uuid.UUID, ts time.Time, state, class string, diskAlloc, netTx int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `insert into meter_samples (ts, project_id, host_id, state, class, disk_alloc, net_tx, guestd_ok)
		values ($1, $2, $3, $4, $5, $6, $7, true) on conflict do nothing`,
		ts.UTC(), projectID, uuid.Nil, state, class, diskAlloc, netTx); err != nil {
		t.Fatalf("sample at %s: %v", ts, err)
	}
}

// ensurePartitions creates the monthly meter_samples partitions a span needs.
func ensurePartitions(t *testing.T, pool *db.Pool, from, to time.Time) {
	t.Helper()
	m := time.Date(from.UTC().Year(), from.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	for ; !m.After(to.UTC()); m = m.AddDate(0, 1, 0) {
		if err := db.EnsurePartitions(context.Background(), pool, m); err != nil {
			t.Fatalf("partitions for %s: %v", m.Format("2006-01"), err)
		}
	}
}

// stopRecorder stands in for the ops engine: it writes the same pending op
// row Enqueue would, so a test can read back the kind and parameters.
type stopRecorder struct {
	calls []ops.NewOp
	kicks int
}

func (s *stopRecorder) Enqueue(ctx context.Context, q store.Querier, n ops.NewOp, allowQueue bool) (uuid.UUID, error) {
	s.calls = append(s.calls, n)
	id := store.NewID()
	params := map[string]any{"phases": n.Phases}
	for k, v := range n.Params {
		params[k] = v
	}
	_, err := q.Exec(ctx, "insert into ops (id, project_id, kind, state, params) values ($1, $2, $3, 'pending', $4)", id, n.ProjectID, n.Kind, params)
	return id, err
}

func (s *stopRecorder) Kick() { s.kicks++ }

// userField reads one column of the account's users row as text.
func userField(t *testing.T, pool *db.Pool, a account, col string) string {
	t.Helper()
	var v *string
	if err := pool.QueryRow(context.Background(), "select "+col+"::text from users where id = $1", a.UserID).Scan(&v); err != nil {
		t.Fatalf("read users.%s: %v", col, err)
	}
	if v == nil {
		return ""
	}
	return *v
}

// eventKinds lists the account's user-only events, oldest first.
func eventKinds(t *testing.T, pool *db.Pool, a account) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), "select kind from events where user_id = $1 order by ts, id", a.UserID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		out = append(out, k)
	}
	return out
}

// outboxEmails counts email outbox rows for the account's events.
func outboxEmails(t *testing.T, pool *db.Pool, a account) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "select count(*) from events_outbox o join events e on e.id = o.event_id where e.user_id = $1 and o.channel = 'email'", a.UserID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func nop() *metrics.M { return metrics.NewNop() }

// at is a clock a job's Now can be pinned to.
func at(t time.Time) func() time.Time { return func() time.Time { return t } }

func mustUUID(t *testing.T) uuid.UUID {
	t.Helper()
	return store.NewID()
}
