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
	if len(r.NotParkedReasons) == len(names) && len(names) > 0 {
		fields["not_parked_reasons"] = r.NotParkedReasons
	}
	if err := e.setResult(ctx, op, fields); err != nil {
		return err
	}
	if r.Restarting {
		return nil
	}
	if r.Parked == 0 && allLocked(r.NotParkedReasons, len(names)) {
		// Nothing was saved because the Mac is locked, so the way on is
		// to unlock it, in the CLI's words (I-1682).
		return &resultRefusal{code: CodeNotParked, msg: "This Mac's screen is locked, and a locked Mac cannot save a machine's memory. Nothing restarted; unlock it and restart again."}
	}
	return &resultRefusal{code: CodeNotParked, msg: notParkedSentence(withReasons(names, r.NotParkedReasons))}
}

// ReasonLocked is hostd's park reason on a Mac whose screen is locked
// (guest.SkipLocked, I-1682); the api does not import hostd.
const ReasonLocked = "this Mac's screen is locked"

func allLocked(reasons []string, n int) bool {
	if n == 0 || len(reasons) != n {
		return false
	}
	for _, r := range reasons {
		if r != ReasonLocked {
			return false
		}
	}
	return true
}

// withReasons is each name with hostd's reason after it ("api: the screen
// is locked"), where hostd gave one for every name; a hostd's reasons are
// fixed phrases, never tenant text.
func withReasons(names, reasons []string) []string {
	if len(reasons) != len(names) {
		return names
	}
	out := make([]string, len(names))
	for i := range names {
		out[i] = names[i] + ": " + reasons[i]
	}
	return out
}

// notParkedSentence is the refusal the user reads, in the words of
// `repose host restart` on the Mac.
func notParkedSentence(names []string) string {
	what := fmt.Sprintf("%d machines could not be parked and would start fresh", len(names))
	if len(names) == 1 {
		what = "1 machine could not be parked and would start fresh"
	}
	return fmt.Sprintf("%s (%s). Nothing restarted, and the parked machines run again.", what, strings.Join(names, "; "))
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
