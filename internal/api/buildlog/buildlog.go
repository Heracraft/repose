// Package buildlog stores BuildLog lines per op in batches of 50 lines or
// 200 ms and publishes them on an in-process broadcast the SSE handler
// subscribes to (05-control-plane-api.md §5.4). Lines are scanned for
// the project's current secret values before storage and matching
// substrings replaced with [redacted] (docs/features/secrets.md).
package buildlog

import (
	"context"
	"encoding/base64"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/db"
)

// Line is one stored line. TS is when it reached the api (DECISIONS
// I-322); a line stored before 0008 carries the migration's time.
type Line struct {
	Seq  int64     `json:"seq"`
	Line string    `json:"line"`
	TS   time.Time `json:"ts"`
}

// Store batches, persists and broadcasts.
type Store struct {
	pool *db.Pool
	log  *slog.Logger

	mu sync.Mutex
	// flushMu is held for the whole of Flush, so a Read that flushes first
	// waits for a flush already in flight: Flush takes the batch out of
	// pending under mu and only then inserts it, and a reader that saw
	// "nothing pending" in that window read the table without the batch
	// (an SSE stream ending with 1 of 3 lines on CI, DECISIONS I-117).
	flushMu  sync.Mutex
	pending  map[uuid.UUID][]Line
	redact   map[uuid.UUID][]string
	subs     map[uuid.UUID]map[chan Line]struct{}
	commands map[string]uuid.UUID // command_id -> op_id
	flushCh  chan struct{}
	batch    int
	interval time.Duration
}

// New makes a store.
func New(pool *db.Pool, log *slog.Logger) *Store {
	return &Store{pool: pool, log: log, pending: map[uuid.UUID][]Line{}, redact: map[uuid.UUID][]string{}, subs: map[uuid.UUID]map[chan Line]struct{}{},
		commands: map[string]uuid.UUID{}, flushCh: make(chan struct{}, 1), batch: 50, interval: 200 * time.Millisecond}
}

// Bind maps a command id to its op so incoming lines find their op.
func (s *Store) Bind(commandID string, opID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands[commandID] = opID
}

// Unbind forgets a command (op finished).
func (s *Store) Unbind(commandID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.commands, commandID)
}

// OpFor resolves a command id to its op; ok=false when unknown.
func (s *Store) OpFor(commandID string) (uuid.UUID, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.commands[commandID]
	return id, ok
}

// MinRedact is the shortest value matched. Shorter values are left alone:
// a three-byte secret would turn every word containing it into [redacted],
// and docs/features/secrets.md says so.
const MinRedact = 4

// Needles returns the strings to look for so that none of values is stored:
// each value whole, its standard and URL-safe base64, and, for a value of
// several lines (a PEM key, a JSON credential), each line on its own,
// because logs and fragments are matched one line at a time and a
// multi-line value never appears whole on one. PEM armour lines
// ("-----BEGIN ...") are not secret and are skipped. Longest first, so a
// whole value is replaced before any line of it.
func Needles(values []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if len(v) >= MinRedact && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, v := range values {
		if len(v) < MinRedact {
			continue
		}
		add(v)
		add(base64.StdEncoding.EncodeToString([]byte(v)))
		add(base64.URLEncoding.EncodeToString([]byte(v)))
		if strings.ContainsAny(v, "\r\n") {
			for _, l := range strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == '\r' }) {
				l = strings.TrimSpace(l)
				if !strings.HasPrefix(l, "-----") {
					add(l)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// SetRedactions registers the values that must never be stored for an op.
func (s *Store) SetRedactions(opID uuid.UUID, values []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redact[opID] = Needles(values)
}

// Redact replaces an op's registered values in text, for what the api stores
// besides log lines (the error a failed build carries).
func (s *Store) Redact(opID uuid.UUID, text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return redact(s.redact[opID], text)
}

func redact(needles []string, text string) string {
	for _, v := range needles {
		text = strings.ReplaceAll(text, v, "[redacted]")
	}
	return text
}

// ClearRedactions forgets an op's redaction set.
func (s *Store) ClearRedactions(opID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.redact, opID)
}

// Append queues a line; it is persisted by the next flush.
func (s *Store) Append(opID uuid.UUID, seq int64, line string) {
	s.mu.Lock()
	line = redact(s.redact[opID], line)
	s.pending[opID] = append(s.pending[opID], Line{Seq: seq, Line: line, TS: time.Now().UTC()})
	full := len(s.pending[opID]) >= s.batch
	s.mu.Unlock()
	if full {
		select {
		case s.flushCh <- struct{}{}:
		default:
		}
	}
}

// Note appends a line of the api's own to an op's log after every line
// already there: the phase lines hostd cannot send, such as "switching
// the machine" when a build's apply starts (DECISIONS I-320). hostd's
// lines for the op must all have arrived; they have once its result has.
func (s *Store) Note(ctx context.Context, opID uuid.UUID, line string) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	var last int64
	if err := s.pool.QueryRow(ctx, "select coalesce(max(seq), 0) from build_logs where op_id = $1", opID).Scan(&last); err != nil {
		return err
	}
	s.mu.Lock()
	for _, l := range s.pending[opID] {
		if l.Seq > last {
			last = l.Seq
		}
	}
	s.mu.Unlock()
	s.Append(opID, last+1, line)
	return nil
}

// Run flushes on the interval or when a batch fills, until ctx ends.
func (s *Store) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Flush(context.Background())
			return
		case <-t.C:
			s.Flush(ctx)
		case <-s.flushCh:
			s.Flush(ctx)
		}
	}
}

// Flush persists every pending line and publishes it. Flushes are
// serialised; a caller with nothing to flush still waits for one in flight.
func (s *Store) Flush(ctx context.Context) {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	batch := s.pending
	s.pending = map[uuid.UUID][]Line{}
	s.mu.Unlock()
	for opID, lines := range batch {
		rows := make([][]any, 0, len(lines))
		for _, l := range lines {
			rows = append(rows, []any{opID, l.Seq, l.Line, l.TS})
		}
		if err := db.InTx(ctx, s.pool, func(tx db.Tx) error {
			for _, r := range rows {
				if _, err := tx.Exec(ctx, "insert into build_logs (op_id, seq, line, ts) values ($1, $2, $3, $4) on conflict do nothing", r...); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			s.log.Error("build log flush failed", "event", "buildlog_flush_fail", "op_id", opID, "lines", len(lines), "err", err.Error())
			s.mu.Lock()
			s.pending[opID] = append(lines, s.pending[opID]...)
			s.mu.Unlock()
			continue
		}
		s.mu.Lock()
		subs := make([]chan Line, 0, len(s.subs[opID]))
		for ch := range s.subs[opID] {
			subs = append(subs, ch)
		}
		s.mu.Unlock()
		for _, ch := range subs {
			for _, l := range lines {
				select {
				case ch <- l:
				default: // a slow subscriber catches up from the table
				}
			}
		}
	}
}

// Subscribe returns a channel of lines for an op and a cancel function.
func (s *Store) Subscribe(opID uuid.UUID) (<-chan Line, func()) {
	ch := make(chan Line, 1024)
	s.mu.Lock()
	if s.subs[opID] == nil {
		s.subs[opID] = map[chan Line]struct{}{}
	}
	s.subs[opID][ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs[opID], ch)
		if len(s.subs[opID]) == 0 {
			delete(s.subs, opID)
		}
		s.mu.Unlock()
	}
}

// Read returns stored lines with seq > since, in order; lines still in
// the batch are flushed first so a reader never lags the writer by a
// flush interval.
func (s *Store) Read(ctx context.Context, opID uuid.UUID, since int64, limit int) ([]Line, error) {
	if limit <= 0 {
		limit = 10000
	}
	// Unconditional: a flush in flight has already emptied pending, and the
	// reader must not query the table until that batch is inserted.
	s.Flush(ctx)
	rows, err := s.pool.Query(ctx, "select seq, line, ts from build_logs where op_id = $1 and seq > $2 order by seq limit $3", opID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Line{}
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.Seq, &l.Line, &l.TS); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Trim keeps the logs of the newest keep ops per project (docs/ops/
// OBSERVABILITY.md retention) and returns rows deleted.
func (s *Store) Trim(ctx context.Context, keep int) (int64, error) {
	tag, err := s.pool.Exec(ctx, `delete from build_logs where op_id in (
		select id from (select id, row_number() over (partition by project_id order by created_at desc) as rn from ops where kind in ('build','create')) o where rn > $1)`, keep)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
