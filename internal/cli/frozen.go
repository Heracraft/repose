package cli

import (
	"fmt"
	"time"
)

// The idle pause (DECISIONS I-818, I-826). The api freezes a running
// machine nobody has touched for fifteen minutes: the host stops its
// vCPUs and keeps its memory, so every process, open file and socket
// stays as it was and the first keystroke wakes it. The state stays
// `running` and `started_at` does not move, so the uptime is still
// true; the marker is the whole surface (I-484, I-485), in the `ls`
// STATE cell and the `status` header, and nothing else is said about it.
//
// The field is `resting` on the wire and the word a user reads is
// "frozen", the owner's (I-683). The word is also the host-restart
// freeze's in code comments (I-652); these two are different mechanisms
// and only this one is ever shown to a user.

// frozenWord is what a user reads for a machine the api has frozen: the
// one place the word is spelled (I-683, "Names are fixed").
const frozenWord = "frozen"

// frozen reports whether the api says the machine is frozen: the one
// predicate the CLI reads the field through, as the dashboard reads
// frozenSince (apps/web/src/lib/frozen.ts). False for an api that does
// not say, and for a machine that is not running.
func frozen(p *Project) bool {
	return p != nil && p.State == "running" && p.Resting != nil
}

// stateWithFrozen is the STATE cell of `repose ls` and the state the
// `status` header names: the project's state, `running frozen` while it
// rests. "frozen" is never a state of its own (the ten states are
// fixed), so `--wait` and `--json` are untouched.
func stateWithFrozen(p *Project) string {
	if frozen(p) {
		return p.State + " " + frozenWord
	}
	if notAnswering(p) {
		return p.State + " " + notAnsweringWord
	}
	return p.State
}

// notAnsweringWord is what a user reads for a running machine whose
// guestd has not answered for about three minutes (DECISIONS I-1652).
// Like frozen it follows the state and is never one of its own.
const notAnsweringWord = "not answering"

// notAnswering reports whether the api says the machine has stopped
// answering. A frozen machine never is: its guestd is paused on purpose.
func notAnswering(p *Project) bool {
	return p != nil && p.State == "running" && p.NotAnswering != nil && !frozen(p)
}

// notAnsweringLine is the line under `ls` and `status` for such a
// machine: how long, and the one command that brings it back, with what
// it costs; "" for any other.
func notAnsweringLine(p *Project, now time.Time) string {
	if !notAnswering(p) {
		return ""
	}
	return fmt.Sprintf("not answering for %s. `repose start %s` restarts it; what was in its memory is lost.", silentFor(now.Sub(p.NotAnswering.Since)), p.Slug)
}

// silentFor is a silence in the shortest true unit, as the api's event
// says it (internal/api/answering.Duration): "4m", "2h", "3d".
func silentFor(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(int(d/time.Minute), 1))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}
