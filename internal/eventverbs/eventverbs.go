// Package eventverbs is the one word each event kind is said with: the
// notification's title (`todo-app: claude finished`), `repose events`
// and status's last event line (DECISIONS I-634). Before it the two
// tables differed (`finished` on the phone, `done` in the CLI), so a
// search for the word you were sent missed the row.
package eventverbs

import "strings"

// Verbs are the notification titles' words, which the CLI took: they
// are what reaches the phone and the email subject first, and what a
// mail filter matches.
var Verbs = map[string]string{
	"completed":            "finished",
	"needs_input":          "needs input",
	"error":                "hit an error",
	"agent_message":        "says",
	"agent_question":       "asks",
	"idle_running":         "unused for 24h, holding plan memory",
	"temp_expiring":        "destroyed in an hour",
	"temp_destroyed":       "temporary machine destroyed",
	"personal_failed":      "machine.nix did not apply",
	"boot_failed":          "new system did not boot",
	"guest_state_changed":  "machine",
	"notifications_paused": "notifications paused",
}

// Verb is kind's word, or the kind with its separators as spaces.
func Verb(kind string) string {
	if v, ok := Verbs[kind]; ok {
		return v
	}
	return strings.NewReplacer("_", " ", ".", " ").Replace(kind)
}
