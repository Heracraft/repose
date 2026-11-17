// Package triggerrun is the one table of a trigger run's states and
// reasons (DECISIONS I-1248, I-1256). It is a leaf package because four
// places need the same words and the first draft had two of them forked:
// the api's store, hostd's bound on what a guest may send, the CLI's
// printed sentence and the dashboard's.
//
// A forked enum is not a style problem here. hostd treats a reason it
// does not know as a gate's own line of stderr, which is tenant content:
// so `not_logged_in` missing from hostd's copy turned the two most
// likely real failures into unbounded tenant text, and then logged them.
package triggerrun

// ReceiverPort is the loopback port in the guest that the edge delivers
// a trigger's events to (DECISIONS I-1250). It lives here because four
// packages need the one number — the api that aims a route at it, the
// webhooks store that refuses a user's route on it, guestd that listens,
// and the CLI that keeps it off a laptop's forwards — and none of them
// can import another.
const ReceiverPort = 9227

// States of a run. Pending and Sending are the hand-off between the
// api's two processes (I-1162) and are never shown to anyone: every
// surface draws them as Queued, as a say's are.
const (
	StatePending = "pending"
	StateSending = "sending"
	StateWorking = "working"
	StateDone    = "done"
	StateFailed  = "failed"
	StateTimeout = "timeout"
	StateSkipped = "skipped"
	// Queued is the word a client sees for the two hand-off states.
	Queued = "queued"
)

// GuestStates are the states a guest may report. The hand-off states are
// not among them: they are the api's own.
var GuestStates = map[string]bool{
	StateWorking: true, StateDone: true, StateFailed: true,
	StateTimeout: true, StateSkipped: true,
}

// States is every state a row may hold, which is the migration's check.
var States = map[string]bool{
	StatePending: true, StateSending: true, StateWorking: true,
	StateDone: true, StateFailed: true, StateTimeout: true, StateSkipped: true,
}

// Sources of a run.
const (
	SourceRoute = "route"
	SourceClock = "clock"
	SourceHand  = "hand"
)

// Sources is the source words a row may hold.
var Sources = map[string]bool{SourceRoute: true, SourceClock: true, SourceHand: true}

// Shown turns a stored state into the word every surface prints.
func Shown(state string) string {
	if state == StatePending || state == StateSending {
		return Queued
	}
	return state
}

// Terminal reports whether a run has ended, which is what `fire --wait`
// and the dashboard's poll wait for.
func Terminal(state string) bool {
	switch state {
	case StateDone, StateFailed, StateTimeout, StateSkipped:
		return true
	}
	return false
}

// The reasons. Each is a bounded enum word a log line may carry, with
// the one sentence every surface prints for it (features/triggers.md
// §4.1). ReasonGated is the single exception: its row carries a gate's
// own last line of stderr, which is tenant content and never logged.
const (
	ReasonOff              = "off"
	ReasonStopped          = "stopped"
	ReasonBusy             = "busy"
	ReasonError            = "error"
	ReasonRouteGone        = "route_gone"
	ReasonPayloadTooLarge  = "payload_too_large"
	ReasonTooManyFires     = "too_many_fires"
	ReasonDuplicate        = "duplicate"
	ReasonGated            = "gated"
	ReasonGateTimeout      = "gate_timeout"
	ReasonConcurrency      = "concurrency"
	ReasonPlanLimit        = "plan_limit"
	ReasonPromptMissing    = "prompt_missing"
	ReasonPromptTooLarge   = "prompt_too_large"
	ReasonNoAgent          = "no_agent"
	ReasonNotLoggedIn      = "not_logged_in"
	ReasonNoPermissionMode = "no_permission_mode"
	ReasonTimeout          = "timeout"
	ReasonGuestRestart     = "guest_restart"
	ReasonMachineStopped   = "machine_stopped"
	ReasonClockLate        = "clock_late"
	ReasonUnreachable      = "unreachable"
	ReasonPayloadGone      = "payload_gone"
	ReasonDryRun           = "dry_run"
)

// Sentences is every reason and the words each surface prints for it.
var Sentences = map[string]string{
	ReasonOff:              "the trigger is off",
	ReasonStopped:          "the machine was stopped",
	ReasonBusy:             "the machine was busy",
	ReasonError:            "the machine is in error",
	ReasonRouteGone:        "the route was removed",
	ReasonPayloadTooLarge:  "the event was over 1 MiB",
	ReasonTooManyFires:     "more than 60 fires this hour",
	ReasonDuplicate:        "a duplicate of an event already seen",
	ReasonGated:            "the gate refused it",
	ReasonGateTimeout:      "the gate took more than 10 seconds",
	ReasonConcurrency:      "a run is working",
	ReasonPlanLimit:        "your plan's memory is in use",
	ReasonPromptMissing:    "the prompt file is missing",
	ReasonPromptTooLarge:   "the prompt file is over 64 KB",
	ReasonNoAgent:          "the agent is not installed on this machine",
	ReasonNotLoggedIn:      "the agent is not logged in",
	ReasonNoPermissionMode: "the agent stopped at a permission prompt",
	ReasonTimeout:          "killed at the timeout",
	ReasonGuestRestart:     "the machine's agent supervisor restarted",
	ReasonMachineStopped:   "the machine stopped",
	ReasonClockLate:        "the fire was late and was not caught up",
	ReasonUnreachable:      "the machine did not take the fire",
	ReasonPayloadGone:      "that run's payload is no longer kept",
	ReasonDryRun:           "the gate ran and no agent was started",
}

// Reason reports whether word is one of the enum, which is the only
// thing a log field may carry.
func Reason(word string) bool { _, ok := Sentences[word]; return ok }

// Sentence is the words a surface prints for a reason. A `gated` reason
// may carry the gate's own last line after a colon, which is kept; a
// word this binary does not know (a newer api) is printed as it came.
func Sentence(reason string) string {
	if reason == "" {
		return ""
	}
	if s, ok := Sentences[reason]; ok {
		return s
	}
	if rest, ok := trimPrefix(reason, ReasonGated+": "); ok {
		return Sentences[ReasonGated] + ": " + rest
	}
	return reason
}

func trimPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

// GateReason is the reason a gate's refusal writes: the word, and the
// command's own last line of stderr where it wrote one. The line is
// tenant content, so it is the only free text a reason holds and it
// never reaches a log field.
func GateReason(stderrLine string) string {
	if stderrLine == "" {
		return ReasonGated
	}
	return ReasonGated + ": " + stderrLine
}

// LogReason is what a log line may carry: the enum word alone, with a
// gate's line cut off.
func LogReason(reason string) string {
	if Reason(reason) {
		return reason
	}
	if _, ok := trimPrefix(reason, ReasonGated+": "); ok {
		return ReasonGated
	}
	return ""
}
