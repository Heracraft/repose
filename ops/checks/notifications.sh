#!/usr/bin/env bash
# M3 step 4 (docs/workstreams/PROMPTS.md "M3 bring-up / m3"), the two open
# rows of docs/workstreams/13-notifications.md §9: each of the five agents
# produces a `completed` event in a real guest, `repose status` shows it,
# and the ntfy delivery arrives.
#
# What "produces" means per agent, honestly:
#   claude, codex, opencode: if the agent is logged in inside the guest
#     (its credential file exists), a real prompt is run through `repose
#     run --agent` and the agent's own hook fires. Otherwise the agent's
#     native hook payload is replayed through `repose-hook` (the agent in
#     REPOSE_HOOK_AGENT, as the wrappers set it) from a
#     tmux window named after the agent, which exercises the wrapper's hook
#     command, the socket, guestd's mapping and window resolution, hostd,
#     the api's ingest, dedupe and outbox, and the delivery: everything
#     but the agent's own decision to fire. The report says which it was.
#   gemini, pi: the shipped mechanism is the pane-idle heuristic (DECISIONS
#     I-49, I-50): the real binary is started in its window and left idle;
#     after 90 s guestd emits `completed "<agent> went idle"`.
#
#   ops/checks/notifications.sh          # PROJECT running; NTFY_URL set
#   AGENTS="gemini" ops/checks/notifications.sh   # one agent only
set -euo pipefail
check=notifications
# shellcheck source-path=SCRIPTDIR
. "$(dirname "$0")/lib.sh"
: "${NTFY_URL:?set NTFY_URL (the owner ntfy topic URL) in ops/checks/m3.env}"

pid=$(project_id)
[ "$(project_state)" = running ] || fail "$PROJECT is $(project_state), not running"
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
containers
log "project $pid; events since $since"

# Channel settings and the test route (13 §5.6; the settings page's button
# calls the same route).
"$REPOSE" notify set --ntfy "$NTFY_URL" 2>&1 | evidence "repose notify set --ntfy <url>"
"$REPOSE" notify test 2>&1 | evidence "repose notify test (per-channel result)"

# wait_event <agent> <since>: the first completed event for the agent after
# `since`; prints its id.
wait_event() {
	local agent=$1 from=$2 s id
	for ((s = 0; s < 240; s += 5)); do
		id=$(psql_q "select id from events where project_id='$pid' and agent='$agent' and kind='completed' and ts >= '$from' order by ts limit 1")
		[ -n "$id" ] && { echo "$id"; return 0; }
		sleep 5
	done
	return 1
}
# wait_delivery <event id>: until delivered.ntfy is a timestamp.
wait_delivery() {
	local id=$1 s d
	for ((s = 0; s < 120; s += 5)); do
		d=$(psql_q "select delivered->>'ntfy' from events where id='$id'")
		case $d in
		20*) echo "$d"; return 0 ;;
		esac
		sleep 5
	done
	echo "${d:-none}"
	return 1
}
# in_window <agent> <shell command>: run the command in a new tmux window
# named after the agent, in the guest, detached.
in_window() {
	guest_sh "$PROJECT" "tmux new-window -d -t '$PROJECT' -n '$1' $(printf '%q' "$2")"
}

status_line() {
	local agent=$1 mech=$2 id=$3 deliv=$4 ts0=$5
	printf '%s | %s | %s | event %s | ntfy %s | %ss after trigger\n' "$agent" "$mech" "$PROJECT" "$id" "$deliv" "$(( $(date +%s) - ts0 ))"
}

# AGENTS="gemini pi" limits the run to those agents (default: all five).
: "${AGENTS:=claude codex opencode gemini pi}"
wants() { case " $AGENTS " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }
results=()
run_agent() {
	wants "$1" || return 0
	local agent=$1 cred=$2 real_prompt=$3 replay_cmd=$4 mech from ts0 id deliv
	from=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	ts0=$(date +%s)
	if [ -n "$cred" ] && guest_sh "$PROJECT" "test -s $cred"; then
		mech="real agent run"
		"$REPOSE" run "$PROJECT" -p "$real_prompt" --agent "$agent" --no-attach --no-sync 2>&1 | tail -3 | evidence "$agent: repose run with a prompt"
	else
		mech="hook payload replayed through repose-hook in a tmux window named $agent"
		in_window "$agent" "$replay_cmd; sleep 120"
	fi
	if id=$(wait_event "$agent" "$from"); then
		deliv=$(wait_delivery "$id" || true)
		psql_q "select ts, kind, agent, tmux_window, summary, source, delivered::text from events where id='$id'" | evidence "$agent: the completed event"
		results+=("$(status_line "$agent" "$mech" "$id" "$deliv" "$ts0")")
	else
		results+=("$agent | $mech | $PROJECT | NO completed event within 240 s")
		log "no completed event for $agent"
	fi
	guest_sh "$PROJECT" "tmux kill-window -t '$PROJECT:$agent' 2>/dev/null || true"
}

# The credential paths are expanded by the guest's shell, not this one.
# shellcheck disable=SC2088
run_agent claude '~/.claude/.credentials.json' \
	"Reply with exactly the words m3 notification check and nothing else." \
	"printf '%s' '{\"hook_event_name\":\"Stop\",\"session_id\":\"m3\",\"transcript_path\":\"/nonexistent\"}' | REPOSE_HOOK_AGENT=claude repose-hook"
# shellcheck disable=SC2088
run_agent codex '~/.codex/auth.json' \
	"Reply with exactly the words m3 notification check and nothing else." \
	"REPOSE_HOOK_AGENT=codex repose-hook '{\"type\":\"agent-turn-complete\",\"last-assistant-message\":\"m3 codex check\"}'"
# shellcheck disable=SC2088
run_agent opencode '~/.local/share/opencode/auth.json' \
	"Reply with exactly the words m3 notification check and nothing else." \
	"printf '%s' '{\"agent\":\"opencode\",\"kind\":\"completed\",\"summary\":\"m3 opencode check\"}' | REPOSE_HOOK_AGENT=opencode repose-hook"

# The two heuristic agents: the real binary, idle in its window.
heuristic_agent() {
	local agent=$1 from ts0 id deliv
	wants "$agent" || return 0
	from=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	ts0=$(date +%s)
	in_window "$agent" "$agent"
	sleep 20
	guest_sh "$PROJECT" "tmux list-windows -t '$PROJECT' -F '#{window_name} #{pane_current_command}'" | evidence "$agent: window and its foreground command"
	if id=$(wait_event "$agent" "$from"); then
		deliv=$(wait_delivery "$id" || true)
		psql_q "select ts, kind, agent, tmux_window, summary, source, delivered::text from events where id='$id'" | evidence "$agent: the heuristic completed event"
		results+=("$(status_line "$agent" "pane-idle heuristic (I-49), real binary idle" "$id" "$deliv" "$ts0")")
	else
		results+=("$agent | pane-idle heuristic | $PROJECT | NO completed event within 240 s")
		log "no heuristic completion for $agent"
	fi
	guest_sh "$PROJECT" "tmux kill-window -t '$PROJECT:$agent' 2>/dev/null || true"
}
heuristic_agent gemini
heuristic_agent pi

# What the CLI shows (13 §5.7); the dashboard's events card is 08's row.
"$REPOSE" status --project "$PROJECT" 2>&1 | evidence "repose status (last event line)"
"$REPOSE" events --since 1h --project "$PROJECT" 2>&1 | tail -12 | evidence "repose events --since 1h"

# The ntfy URL is stored as is and never logged (13 §5.6).
assert_zero "ntfy URL in api logs" "$(docker_logs_since "$API_CTR" "$since" | grep -c -F -- "$NTFY_URL" || true)"
assert_zero "ntfy URL in api-grpc logs" "$(docker_logs_since "$API_GRPC_CTR" "$since" | grep -c -F -- "$NTFY_URL" || true)"

printf '%s\n' "${results[@]}" | evidence "STATUS.md lines per agent (13 §9 real-guest row)"
log "notifications check finished; evidence in $report. A row with NO event is a finding, not a pass."
