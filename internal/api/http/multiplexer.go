package httpapi

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/multiplexer"
)

// herdrMinBase is the first base version that carries herdr, its units
// and the guestd that starts them (DECISIONS I-501..I-503). While it is
// empty, or names no row of base_versions, the api refuses every request
// for herdr (docs/interfaces/api.md, "The base gate"). The conductor sets
// it in the release that ships the CLI side (workstream 16, section 2).
const herdrMinBase = ""

// minBase is herdrMinBase, replaceable by tests (export_test.go).
var minBase atomic.Pointer[string]

func init() {
	v := herdrMinBase
	minBase.Store(&v)
}

func currentMinBase() string { return *minBase.Load() }

// checkMultiplexer answers 400 invalid for a value outside
// multiplexer.Names.
func checkMultiplexer(v string) error {
	if !multiplexer.Valid(v) {
		return errf("invalid", "multiplexer must be tmux or herdr")
	}
	return nil
}

// herdrGate refuses herdr on a machine whose base predates it (I-502):
// 409 conflict, detail.reason = base_update_needed. base is the version
// the machine would run; nil means the newest published base when
// nilIsNewest (a create, fork or restore as new, whose build takes the
// newest), and older than any base otherwise (a PATCH on a project that
// has never been built). tmux is never gated; the caller checks the
// value first.
func herdrGate(ctx context.Context, q store.Querier, slug string, base *string, nilIsNewest bool) error {
	needs := currentMinBase()
	if needs == "" {
		return gateError(slug, base, "")
	}
	min, err := store.GetBase(ctx, q, needs)
	if errors.Is(err, db.ErrNotFound) {
		return gateError(slug, base, "")
	}
	if err != nil {
		return err
	}
	var have *store.BaseVersion
	switch {
	case base != nil:
		have, err = store.GetBase(ctx, q, *base)
	case nilIsNewest:
		have, err = store.LatestBase(ctx, q)
		if err == nil {
			base = &have.Version
		}
	default:
		err = db.ErrNotFound
	}
	if errors.Is(err, db.ErrNotFound) {
		// A version with no row, or no base at all, counts as older.
		return gateError(slug, base, needs)
	}
	if err != nil {
		return err
	}
	if have.ReleasedAt.Before(min.ReleasedAt) {
		return gateError(slug, base, needs)
	}
	return nil
}

func gateError(slug string, base *string, needs string) error {
	var bv any
	if base != nil {
		bv = *base
	}
	detail := map[string]any{"reason": "base_update_needed", "base_version": bv, "needs": needs}
	switch {
	case needs == "":
		return withDetail(errf("conflict", "herdr is not available yet."), detail)
	case base == nil:
		return withDetail(errf("conflict", "%s has no base yet; herdr needs %s or newer.", slug, needs), detail)
	default:
		return withDetail(errf("conflict", "%s runs base %s; herdr needs %s or newer.", slug, *base, needs), detail)
	}
}

// copiedMultiplexer is the value a fork or a restore as a new project
// takes from its source: the source's, or tmux when herdr would be
// refused for the copy's base (api.md, "The base gate").
func copiedMultiplexer(ctx context.Context, q store.Querier, src *store.Project, slug string, base *string) (string, error) {
	m := multiplexer.Normalize(src.Multiplexer)
	if m != multiplexer.Herdr {
		return m, nil
	}
	err := herdrGate(ctx, q, slug, base, true)
	var e *Error
	if errors.As(err, &e) && e.Code == "conflict" {
		return multiplexer.Tmux, nil
	}
	if err != nil {
		return "", err
	}
	return m, nil
}
