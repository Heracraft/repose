package cli

import (
	"fmt"
	"strings"
)

// This file turns what the api and hostd say (state words, `code:
// message` op errors, `last_error`) into the sentence a user reads, with
// the command that moves them forward (DECISIONS I-153). Raw codes stay
// visible in parentheses so a support thread can still grep for them, but
// never alone and never with a guest id: "guestd unreachable for guest
// 01a0…" means nothing to the person who typed `repose run`.

// reasonFor renders an op error code (and, for codes we do not know, the
// api's own message) as a clause that completes "because …" or stands
// after a colon. It never includes ids.
func reasonFor(code, message string) string {
	// Since I-159 the api's message is already the sentence to show
	// ("the environment's agent (guestd) stopped answering; `repose start`
	// restarts it"); older builds sent the host's wording, which carries a
	// guest id and is replaced by the mapping below.
	if m := strings.TrimSuffix(strings.TrimSpace(firstLine(message)), "."); m != "" && !containsUUID(m) {
		return machineWords(m)
	}
	switch code {
	case "guest_unresponsive":
		return "repose's service on the machine stopped answering"
	case "boot_failed":
		return "the machine did not boot"
	case "insufficient_capacity", "capacity":
		return "the host had no room for it right now"
	case "build_failed":
		return "its Nix configuration failed to build"
	case "eval_failed":
		return "its Nix configuration has an error"
	case "build_timeout":
		return "building its configuration took too long"
	case "closure_too_large":
		return "its configuration is larger than the machine allows"
	case "not_found":
		return "the host no longer has the machine"
	case "store_path_hidden":
		return "a nix garbage collection inside the machine hid parts of its new system"
	case "host_unreachable", "unreachable":
		return "its host is not reachable"
	case "payment_required":
		return "the account needs a plan"
	case "":
		return humaneMessage(message)
	default:
		if m := humaneMessage(message); m != "" {
			return m
		}
		return "the operation failed"
	}
}

// machineWords puts the api's older words for the machine into the
// CLI's: the api's op messages say "the environment" and "the
// environment's agent (guestd)", and the CLI says "machine" everywhere
// (review C4, DECISIONS I-630).
var machineWordsReplacer = strings.NewReplacer(
	"the environment's agent (guestd)", "repose's service on the machine",
	"The environment's agent (guestd)", "Repose's service on the machine",
	"the environment's", "the machine's",
	"the environment", "the machine",
	"The environment", "The machine",
)

func machineWords(s string) string { return machineWordsReplacer.Replace(s) }

// humaneMessage strips what is noise to a user from a raw message: guest
// and op UUIDs, trailing detail after the first line.
func humaneMessage(msg string) string {
	msg = firstLine(strings.TrimSpace(msg))
	fields := strings.Fields(msg)
	out := fields[:0]
	for _, f := range fields {
		if looksLikeUUID(strings.Trim(f, ".,;:()")) {
			continue
		}
		out = append(out, f)
	}
	s := strings.Join(out, " ")
	s = strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(s, "for guest")), ":")
	return machineWords(s)
}

func containsUUID(s string) bool {
	for _, f := range strings.Fields(s) {
		if looksLikeUUID(strings.Trim(f, ".,;:()")) {
			return true
		}
	}
	return false
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}

// splitLastError parses the api's `last_error` ("code: message", written
// by the ops engine) into its halves. A value without a recognisable code
// prefix is all message.
func splitLastError(s string) (code, message string) {
	s = strings.TrimSpace(s)
	c, m, ok := strings.Cut(s, ": ")
	if ok && c != "" && !strings.ContainsAny(c, " \t") && strings.ToLower(c) == c {
		return c, m
	}
	return "", s
}

// projectReason is the humane reason a project is in its state, from
// `last_error` and `host_unreachable`; "" when the api gave none.
func projectReason(p *Project) string {
	if p.HostUnreachable {
		return reasonFor("host_unreachable", "")
	}
	if p.LastError == nil || strings.TrimSpace(*p.LastError) == "" {
		return ""
	}
	code, msg := splitLastError(*p.LastError)
	return reasonFor(code, msg)
}

// abuseStopCode is the last_error code the api writes when it stopped a
// guest for abuse (DECISIONS I-239).
const abuseStopCode = "abuse_stopped"

// abuseStopReason is the api's sentence for a project the platform
// stopped because a miner was running ("stopped: a cryptocurrency miner
// (xmrig) was running; ..."), or "" for any other project. It is shown on
// a stopped project, where last_error is otherwise not news.
func abuseStopReason(p *Project) string {
	if p.State != "stopped" || p.LastError == nil {
		return ""
	}
	code, msg := splitLastError(*p.LastError)
	if code != abuseStopCode {
		return ""
	}
	return strings.TrimSpace(msg)
}

// notRunningError is what every command that needs a running guest says
// when the project is not running: the true state, why (when known), and
// the command that fixes it. The exit code stays 5 (cli-config.md).
func notRunningError(p *Project) error {
	return exitf(ExitGuestNotRunning, "%s", notRunningMessage(p))
}

func notRunningMessage(p *Project) string {
	s := p.Slug
	switch p.State {
	case "stopped":
		if r := abuseStopReason(p); r != "" {
			return fmt.Sprintf("%s is %s", s, strings.TrimSuffix(r, ".")+".")
		}
		return fmt.Sprintf("%s is stopped. `repose start %s` starts it.", s, s)
	case "creating", "building", "starting":
		return fmt.Sprintf("%s is still %s. `repose run` in its checkout waits for it and attaches; `repose status %s` shows progress.", s, p.State, s)
	case "stopping":
		return fmt.Sprintf("%s is stopping. Once it has stopped, `repose start %s` brings it back.", s, s)
	case "restoring":
		return fmt.Sprintf("%s is being restored from a snapshot. `repose status %s` shows when it is done.", s, s)
	case "destroying":
		return fmt.Sprintf("%s is being destroyed. `repose run` in its checkout waits for that and creates a fresh %s; `%s` brings the old one back once it is gone.", s, s, restoreHint(s))
	case "destroyed":
		return fmt.Sprintf("%s is %s; its last snapshot is kept for 30 days, and `%s` brings it back.", s, p.State, restoreHint(s))
	case "error":
		reason := projectReason(p)
		if reason == "" {
			reason = "its last operation failed"
		}
		return fmt.Sprintf("%s is in an error state: %s", s, withNext(reason, fmt.Sprintf("`repose start %s` restarts it.", s)))
	default:
		return fmt.Sprintf("%s is %s, not running. Try `repose start %s`.", s, p.State, s)
	}
}

// withNext ends reason with a full stop and adds next, unless the reason
// (a sentence from the api since I-159) already names a command.
func withNext(reason, next string) string {
	s := strings.TrimSuffix(reason, ".") + "."
	if next != "" && !strings.Contains(reason, "`repose ") {
		s += " " + next
	}
	return s
}

// opFailed renders a failed op for verb ("start", "stop", "destroy", …)
// on slug, with the next step that fits the verb. The code is kept in
// parentheses for support; ids are dropped.
func opFailed(verb, slug string, e OpError, next string) error {
	reason := reasonFor(e.Code, e.Message)
	code := ""
	if e.Code != "" {
		code = " (" + e.Code + ")"
	}
	msg := fmt.Sprintf("Could not %s %s: %s%s.", verb, slug, reason, code)
	if next != "" && !strings.Contains(reason, "`repose ") && !strings.Contains(strings.ToLower(reason), "try again") {
		msg += " " + next
	}
	return exitf(opExitCode(e.Code), "%s", msg)
}

// opExitCode is a failed op's exit code: the one the same refusal from
// the api has, so a script that retries on 8 retries a host that ran out
// of room during the boot too (DECISIONS I-623).
func opExitCode(code string) int {
	switch code {
	case "insufficient_capacity", "capacity":
		return ExitCapacity
	case "payment_required":
		return ExitPaymentRequired
	}
	return ExitGeneric
}

// nextAfterFailedStart is the advice after a start (or the start inside
// `repose run`) failed.
func nextAfterFailedStart(slug, code string) string {
	switch code {
	case "insufficient_capacity", "capacity":
		return "Try again in a few minutes."
	case "guest_unresponsive", "boot_failed":
		// hostd kept the console of this boot (DECISIONS I-592).
		return fmt.Sprintf("`repose logs %s --kind console` shows what the machine printed; `repose start %s` tries again.", slug, slug)
	default:
		return fmt.Sprintf("`repose start %s` tries again.", slug)
	}
}

// bootFallbackReason is what a running project's last_error says after
// a start or reboot whose new system did not boot, so that it runs its
// previous one (DECISIONS I-590), or after a switch refused because a nix
// garbage collection inside the machine hid the new system (I-589); ""
// for any other project.
func bootFallbackReason(p *Project) string {
	if p.State != "running" || p.LastError == nil {
		return ""
	}
	code, msg := splitLastError(*p.LastError)
	if code != "boot_failed" && code != "store_path_hidden" {
		return ""
	}
	return strings.TrimSpace(msg)
}
