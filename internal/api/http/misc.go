package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
)

func sinceParam(r *http.Request) time.Time {
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProjectAny(r)
	if err != nil {
		return err
	}
	// limit and before page back through the list (I-414); with neither,
	// the newest 50 as before.
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return errf("invalid", "limit must be 1 to 200")
		}
		limit = n
	}
	var evs []store.Event
	if v := r.URL.Query().Get("before"); v != "" {
		before, perr := uuid.Parse(v)
		if perr != nil {
			return errf("invalid", "before must be an event id")
		}
		evs, err = store.ListEventsBefore(r.Context(), s.d.Pool, p.ID, before, limit)
	} else {
		evs, err = store.ListEvents(r.Context(), s.d.Pool, p.ID, sinceParam(r), limit)
	}
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(evs))
	for _, e := range evs {
		out = append(out, map[string]any{"id": e.ID, "ts": e.TS, "kind": e.Kind, "agent": e.Agent, "window": e.TmuxWindow, "summary": e.Summary, "source": e.Source, "delivered": e.Delivered})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// projectLogs serves JSON lines: build (the newest build's log), ops (one
// line per operation), console (the console hostd captured from boots
// that never reached Ready, which it attaches to the op's result (I-592);
// the full console goes to Loki, docs/ops/OBSERVABILITY.md).
func (s *Server) projectLogs(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProjectAny(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "console"
	}
	since := sinceParam(r)
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	switch kind {
	case "build":
		var opID *store.Op
		opsList, err := store.ListProjectOps(ctx, s.d.Pool, p.ID, 50)
		if err != nil {
			return err
		}
		for i := range opsList {
			if opsList[i].Kind == "build" || opsList[i].Kind == "create" {
				opID = &opsList[i]
				break
			}
		}
		if opID == nil {
			return nil
		}
		lines, err := s.d.Logs.Read(ctx, opID.ID, 0, 10000)
		if err != nil {
			return err
		}
		// ts and kind since DECISIONS I-322; since= is then a cursor, as
		// for ops: only lines that arrived after it.
		for _, l := range lines {
			if !since.IsZero() && !l.TS.After(since) {
				continue
			}
			if err := enc.Encode(map[string]any{"ts": l.TS, "kind": "build", "op_id": opID.ID, "seq": l.Seq, "line": l.Line}); err != nil {
				return nil
			}
		}
	case "ops":
		opsList, err := store.ListProjectOps(ctx, s.d.Pool, p.ID, 10000)
		if err != nil {
			return err
		}
		for i := len(opsList) - 1; i >= 0; i-- {
			op := opsList[i]
			if op.CreatedAt.Before(since) {
				continue
			}
			line := map[string]any{"ts": op.CreatedAt, "op_id": op.ID, "kind": op.Kind, "state": op.State, "finished_at": op.FinishedAt}
			if op.FinishedAt != nil {
				line["duration_ms"] = op.FinishedAt.Sub(op.CreatedAt).Milliseconds()
			}
			if op.Error != nil {
				line["error"] = op.Error
			}
			if err := enc.Encode(line); err != nil {
				return nil
			}
		}
	case "console":
		// Each boot hostd captured: its console's lines, each with the
		// time its op finished (one boot, one time), oldest boot first.
		// An op from before I-592 carries no console; its error's
		// sentence stands in, as it did.
		opsList, err := store.ListProjectOps(ctx, s.d.Pool, p.ID, 20)
		if err != nil {
			return err
		}
		for i := len(opsList) - 1; i >= 0; i-- {
			op := opsList[i]
			ts := op.FinishedAt
			if ts == nil {
				ts = &op.UpdatedAt
			}
			if !since.IsZero() && !ts.After(since) {
				continue
			}
			if tail, _ := op.Result["console"].(string); tail != "" {
				for _, l := range strings.Split(tail, "\n") {
					if err := enc.Encode(map[string]any{"ts": ts, "op_id": op.ID, "kind": "console", "line": l}); err != nil {
						return nil
					}
				}
				continue
			}
			if op.Error == nil {
				continue
			}
			msg, _ := op.Error["message"].(string)
			if msg == "" {
				continue
			}
			if err := enc.Encode(map[string]any{"ts": ts, "op_id": op.ID, "kind": op.Kind, "line": msg}); err != nil {
				return nil
			}
		}
	default:
		return errf("invalid", "kind must be console, build or ops")
	}
	return nil
}

func (s *Server) usage(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	from, to := sinceParamNamed(r, "from"), sinceParamNamed(r, "to")
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if from.IsZero() {
		from = to.AddDate(0, -1, 0)
	}
	rows, err := s.d.Pool.Query(r.Context(), `select u.project_id, p.slug, date_trunc('day', u.hour) as day, u.class, sum(u.running_seconds), sum(u.gb_alloc), sum(u.egress_bytes), sum(u.cost_cents), sum(u.credit_cents)
		from usage_hours u join projects p on p.id = u.project_id where p.user_id = $1 and u.hour >= $2 and u.hour < $3 group by u.project_id, p.slug, day, u.class order by day, p.slug`, u.ID, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var pid, slug, class string
		var day time.Time
		var running, gb, egress, cost, credit int64
		if err := rows.Scan(&pid, &slug, &day, &class, &running, &gb, &egress, &cost, &credit); err != nil {
			return err
		}
		hours := map[string]float64{"small": 0, "large": 0, "xl": 0}
		hours[class] = float64(running) / 3600
		// cost_cents is the egress overage share and credit_cents 0 from
		// price_version plan-v1; both stay one release (api.md).
		out = append(out, map[string]any{"project_id": pid, "slug": slug, "day": day.Format("2006-01-02"), "guest_hours": hours,
			"gb_months": float64(gb) / 720, "egress_gb": float64(egress) / (1 << 30), "cost_cents": cost, "credit_cents": credit})
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "days": out})
	return rows.Err()
}

func sinceParamNamed(r *http.Request, name string) time.Time {
	v := r.URL.Query().Get(name)
	if v == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t
	}
	return time.Time{}
}
