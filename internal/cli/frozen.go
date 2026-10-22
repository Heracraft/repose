package cli

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
	return p.State
}
