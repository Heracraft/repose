// Package meter ingests Samples into meter_samples and proc_samples and
// rolls them up hourly into usage_hours (05-control-plane-api.md §5.4,
// §5.10). Samples are append-only and never read on a request path; the
// rollup reads them once.
package meter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// Ingest writes samples.
type Ingest struct {
	pool *db.Pool
	m    *metrics.M
	log  *slog.Logger

	mu      sync.Mutex
	guests  map[uuid.UUID]guestEntry
	months  map[string]bool
	nowFunc func() time.Time
}

type guestEntry struct {
	project uuid.UUID
	host    uuid.UUID
	seen    time.Time
}

// New builds an ingest.
func New(pool *db.Pool, m *metrics.M, log *slog.Logger) *Ingest {
	return &Ingest{pool: pool, m: m, log: log.With("component", "api"), guests: map[uuid.UUID]guestEntry{}, months: map[string]bool{}, nowFunc: time.Now}
}

// project resolves a sampled guest to its project, only when the project
// is placed on the reporting host (DECISIONS I-447).
func (i *Ingest) project(ctx context.Context, hostID uuid.UUID, guestID string) (uuid.UUID, bool) {
	gid, err := uuid.Parse(guestID)
	if err != nil {
		return uuid.Nil, false
	}
	i.mu.Lock()
	e, ok := i.guests[gid]
	i.mu.Unlock()
	if ok && e.host == hostID && i.nowFunc().Sub(e.seen) < 10*time.Minute {
		return e.project, true
	}
	p, err := store.GetProjectOnHost(ctx, i.pool, gid, hostID)
	if err != nil {
		if errors.Is(err, store.ErrOtherHost) {
			i.m.HostReportsRefused.WithLabelValues("foreign_guest").Inc()
		}
		return uuid.Nil, false
	}
	i.mu.Lock()
	i.guests[gid] = guestEntry{project: p.ID, host: hostID, seen: i.nowFunc()}
	i.mu.Unlock()
	return p.ID, true
}

func (i *Ingest) ensureMonth(ctx context.Context, ts time.Time) error {
	key := ts.UTC().Format("2006-01")
	i.mu.Lock()
	ok := i.months[key]
	i.mu.Unlock()
	if ok {
		return nil
	}
	if err := db.EnsurePartitions(ctx, i.pool, ts); err != nil {
		return err
	}
	i.mu.Lock()
	i.months[key] = true
	i.mu.Unlock()
	return nil
}

// Caps on what a guest reported in a sample. hostd caps the same; these
// hold whatever reaches the api.
const (
	maxAgents = 32
	maxProcs  = 128
	maxComm   = 16
	maxAgent  = 32
	maxWindow = 64
)

// OnSamples stores one Samples message. Rows are inserted with ON
// CONFLICT DO NOTHING so a message hostd re-sends after a reconnect is
// harmless. Each guest's rows go in their own batch, so a row Postgres
// refuses costs that guest's sample only, and a guest whose rows are
// refused still gets its host-measured row (DECISIONS I-446).
func (i *Ingest) OnSamples(ctx context.Context, hostID uuid.UUID, s *hostdv1.Samples) {
	ts := time.Unix(s.Ts, 0).UTC()
	if s.Ts == 0 {
		ts = i.nowFunc().UTC()
	}
	if err := i.ensureMonth(ctx, ts); err != nil {
		i.log.Error("sample partition", "event", "samples_fail", "err", err.Error())
		return
	}
	for _, g := range s.Guests {
		pid, ok := i.project(ctx, hostID, g.GuestId)
		if !ok {
			continue
		}
		err := i.insertGuest(ctx, hostID, ts, pid, g, true)
		if err == nil {
			continue
		}
		i.log.Error("sample insert", "event", "samples_fail", "host_id", hostID.String(), "project_id", pid.String(), "err", err.Error())
		// The guest's own fields were refused: the host-measured ones
		// still count (running time, egress, disk).
		if err := i.insertGuest(ctx, hostID, ts, pid, g, false); err != nil {
			i.m.SamplesFailed.WithLabelValues("insert").Inc()
			i.log.Error("sample insert", "event", "samples_fail", "host_id", hostID.String(), "project_id", pid.String(), "err", err.Error())
			continue
		}
		i.m.SamplesFailed.WithLabelValues("guest_fields").Inc()
	}
}

// insertGuest stores one guest's sample. With guestFields false the row
// carries only what the host measured, and no process rows.
func (i *Ingest) insertGuest(ctx context.Context, hostID uuid.UUID, ts time.Time, pid uuid.UUID, g *hostdv1.GuestSample, guestFields bool) error {
	sig := g.Signals
	if sig == nil || !guestFields {
		sig = &hostdv1.GuestSignals{GuestdOk: sig != nil && sig.GuestdOk}
	}
	agents := []map[string]string{}
	for _, a := range sig.Agents {
		if len(agents) == maxAgents {
			break
		}
		agents = append(agents, map[string]string{"agent": store.CleanText(a.Agent, maxAgent), "window": store.CleanText(a.TmuxWindow, maxWindow), "state": store.CleanText(a.State, maxAgent)})
	}
	aj, _ := json.Marshal(agents)
	batch := &pgx.Batch{}
	batch.Queue(`insert into meter_samples (ts, project_id, host_id, state, class, cpu_ns, mem_rss, net_tx, net_rx, disk_alloc, disk_used, ssh_sessions, tmux_clients, agents, docker_containers, guestd_ok)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) on conflict do nothing`,
		ts, pid, hostID, g.State, g.Class, int64(g.CpuNsDelta), int64(g.MemRssBytes), int64(g.NetTxBytesDelta), int64(g.NetRxBytesDelta), int64(g.DiskAllocBytes), int64(g.DiskUsedBytes),
		int32(sig.SshSessions), int32(sig.TmuxClients), aj, int32(sig.DockerContainers), sig.GuestdOk)
	if guestFields {
		seen := map[string]bool{}
		for _, p := range g.Procs {
			if len(seen) == maxProcs {
				break
			}
			comm := store.CleanText(p.Comm, maxComm)
			if comm == "" || seen[comm] {
				continue
			}
			seen[comm] = true
			batch.Queue("insert into proc_samples (ts, project_id, comm, cpu_ns, rss) values ($1,$2,$3,$4,$5) on conflict do nothing", ts, pid, comm, int64(p.CpuNsDelta), int64(p.RssBytes))
		}
	}
	return i.pool.SendBatch(ctx, batch).Close()
}

// Latest is the newest sample of a project, for GET /projects/:id.
type Latest struct {
	TS               time.Time
	State            string
	SSHSessions      int
	TmuxClients      int
	Agents           []map[string]string
	DockerContainers int
	DiskUsed         int64
	GuestdOK         bool
}

// LatestSample reads the newest sample; ok=false when there is none.
func LatestSample(ctx context.Context, q store.Querier, projectID uuid.UUID) (*Latest, bool, error) {
	var l Latest
	var agents []byte
	err := q.QueryRow(ctx, "select ts, state, ssh_sessions, tmux_clients, agents, docker_containers, disk_used, guestd_ok from meter_samples where project_id = $1 order by ts desc limit 1", projectID).
		Scan(&l.TS, &l.State, &l.SSHSessions, &l.TmuxClients, &agents, &l.DockerContainers, &l.DiskUsed, &l.GuestdOK)
	if err != nil {
		if db.IsNoRows(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	_ = json.Unmarshal(agents, &l.Agents) // stored by us; malformed means empty
	return &l, true, nil
}
