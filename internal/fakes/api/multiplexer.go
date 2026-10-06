package api

import "github.com/heracraft/repose/internal/multiplexer"

// The multiplexer field and its base gate (DECISIONS I-502,
// docs/interfaces/api.md "The base gate"). Base versions here are the
// fake's date strings, so "older" compares them as strings; the real api
// compares base_versions.released_at.

// SetHerdrMinBase sets the first base version with herdr. Empty, the
// default as in the real api until the release that ships it, refuses
// every request for herdr with needs "".
func (f *Fake) SetHerdrMinBase(version string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.herdrMinBase = version
}

// SetBaseVersion sets the base a project runs, as a base bump or a hold
// would, so a test can put a project behind the gate.
func (f *Fake) SetBaseVersion(projectID, version string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[projectID]; ok {
		p.BaseVersion = version
	}
}

// herdrGate is the api's: base is the version the machine would run, ""
// meaning the newest (a create) for a POST and none for a PATCH.
func (f *Fake) herdrGate(slug, base string, emptyIsNewest bool) *apiError {
	if base == "" && emptyIsNewest {
		base = baseVersion
	}
	needs := f.herdrMinBase
	var bv any
	if base != "" {
		bv = base
	}
	detail := map[string]any{"reason": "base_update_needed", "base_version": bv, "needs": needs}
	switch {
	case needs == "":
		return errf("conflict", "herdr is not available yet.").withDetail(detail)
	case base == "":
		return errf("conflict", "%s has no base yet; herdr needs %s or newer.", slug, needs).withDetail(detail)
	case base < needs:
		return errf("conflict", "%s runs base %s; herdr needs %s or newer.", slug, base, needs).withDetail(detail)
	}
	return nil
}

func checkMultiplexer(v string) *apiError {
	if !multiplexer.Valid(v) {
		return invalid("multiplexer must be tmux or herdr")
	}
	return nil
}

// copyMultiplexer gives a fork or a restore as new the source's base, as
// the real api's insertRestored does, and the source's multiplexer, or
// tmux when the gate would refuse herdr on that base.
func (f *Fake) copyMultiplexer(src, dst *project) {
	dst.BaseVersion = src.BaseVersion
	if n := len(dst.revisions); n > 0 {
		dst.revisions[n-1].BaseVersion = src.BaseVersion
	}
	m := multiplexer.Normalize(src.Multiplexer)
	if m == multiplexer.Herdr && f.herdrGate(dst.Slug, dst.BaseVersion, true) != nil {
		m = multiplexer.Tmux
	}
	dst.Multiplexer = m
}
