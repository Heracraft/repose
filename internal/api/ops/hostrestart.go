package ops

import (
	"context"
	"fmt"
	"strings"

	"github.com/heracraft/repose/internal/api/store"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// resultRefusal is an ok result from hostd that still ends its op with a
// reason the user reads: onResult returns it, and advance fails the op
// with its code and message instead of calling it an internal error.
type resultRefusal struct{ code, msg string }

func (r *resultRefusal) Error() string { return r.code + ": " + r.msg }

// CodeNotParked is a host restart refused because a machine could not be
// parked (DECISIONS I-1690); the op's result names them.
const CodeNotParked = "not_parked"

// onRestartHost stores RestartHost's answer on the op (DECISIONS I-1690):
// how many machines are parked and, by name, the ones that could not be.
// A restart hostd did not go on with (a machine could not be parked, and
// no force) fails the op with the names; with force, the names stay in
// the result, because those machines start fresh.
func (e *Engine) onRestartHost(ctx context.Context, op *store.Op, r *hostdv1.RestartResult) error {
	if r == nil {
		return fmt.Errorf("restart result without payload")
	}
	names, err := e.slugsOfGuests(ctx, r.NotParked)
	if err != nil {
		return err
	}
	fields := map[string]any{"parked": r.Parked, "not_parked": names, "restarting": r.Restarting, "park_ms": r.ParkMs}
	if err := e.setResult(ctx, op, fields); err != nil {
		return err
	}
	if r.Restarting {
		return nil
	}
	return &resultRefusal{code: CodeNotParked, msg: notParkedSentence(names)}
}

// notParkedSentence is the refusal the user reads, in the words of
// `repose host restart` on the Mac.
func notParkedSentence(names []string) string {
	what := fmt.Sprintf("%d machines could not be parked and would start fresh", len(names))
	if len(names) == 1 {
		what = "1 machine could not be parked and would start fresh"
	}
	return fmt.Sprintf("%s (%s). Nothing restarted, and the parked machines run again.", what, strings.Join(names, ", "))
}

// slugsOfGuests names guests by their projects' slugs, in the order given;
// a guest no project holds keeps its id, so nothing is dropped.
func (e *Engine) slugsOfGuests(ctx context.Context, ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		var slug string
		err := e.pool.QueryRow(ctx, "select slug from projects where guest_id::text = $1 and destroyed_at is null", id).Scan(&slug)
		if err != nil {
			slug = id
		}
		out = append(out, slug)
	}
	return out, nil
}
