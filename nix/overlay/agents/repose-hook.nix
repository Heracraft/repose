# repose-hook (shell implementation of docs/interfaces/guest-conventions.md
# "Agent wrappers" and docs/workstreams/04-guestd.md "Hooks"): read the
# agent's hook payload (stdin, or argv[1] for Codex's notify), map it to
# {agent, kind, summary, window}, POST it to /run/repose/hooks.sock, and
# exit 0 no matter what. Workstream 04 ships the Go binary with the same
# contract; nix/flake.nix prefers that one when cmd/repose-hook exists.
{ lib, writeShellApplication, jq, curl, coreutils, tmux, gnugrep }:
writeShellApplication {
  name = "repose-hook";
  runtimeInputs = [ jq curl coreutils tmux gnugrep ];
  text = ''
    # Nothing here may fail the caller.
    set +e
    sock="''${REPOSE_HOOKS_SOCKET:-/run/repose/hooks.sock}"
    agent="''${REPOSE_HOOK_AGENT:-claude}"
    # --agent NAME, as the Go binary takes it (the opencode plugin passes it).
    if [ "''${1:-}" = "--agent" ] && [ "$#" -ge 2 ]; then
      agent="$2"
      shift 2
    fi

    if [ "$#" -ge 1 ] && [ -n "$1" ]; then
      payload="$1"
    elif [ ! -t 0 ]; then
      payload=$(head -c 65536)
    else
      payload=""
    fi
    [ -n "$payload" ] || exit 0
    echo "$payload" | jq -e . >/dev/null 2>&1 || exit 0

    kind=""; summary=""
    # Already in wire shape (the opencode plugin sends it mapped).
    if echo "$payload" | jq -e '.kind and .agent' >/dev/null 2>&1; then
      agent=$(echo "$payload" | jq -r .agent)
      kind=$(echo "$payload" | jq -r .kind)
      summary=$(echo "$payload" | jq -r '.summary // ""')
    else
      case "$agent" in
        claude)
          ev=$(echo "$payload" | jq -r '.hook_event_name // ""')
          case "$ev" in
            Stop)
              kind=completed
              transcript=$(echo "$payload" | jq -r '.transcript_path // ""')
              if [ -n "$transcript" ] && [ -r "$transcript" ]; then
                summary=$(tail -c 4096 "$transcript" | grep '"type":"assistant"' | tail -n 1 \
                  | jq -r '[.message.content[]? | select(.type == "text") | .text] | join(" ")' 2>/dev/null \
                  | head -c 200)
              fi
              [ -n "$summary" ] || summary="claude finished"
              ;;
            Notification)
              nt=$(echo "$payload" | jq -r '.notification_type // ""')
              case "$nt" in
                # idle_prompt is Claude Code's minute-later nudge after every
                # finished turn, not a question (I-418).
                permission_prompt|agent_needs_input)
                  kind=needs_input
                  summary=$(echo "$payload" | jq -r '.message // "claude needs input"' | head -c 1000)
                  ;;
              esac
              ;;
            StopFailure)
              kind=error
              summary=$(echo "$payload" | jq -r '.error // .message // "claude stopped with an error"' | head -c 1000)
              ;;
          esac
          ;;
        codex)
          ev=$(echo "$payload" | jq -r '.type // ""')
          case "$ev" in
            agent-turn-complete)
              kind=completed
              summary=$(echo "$payload" | jq -r '."last-assistant-message" // "codex finished"' | head -c 1000)
              ;;
          esac
          ;;
      esac
    fi
    [ -n "$kind" ] || exit 0

    window=""
    if [ -n "''${TMUX_PANE:-}" ]; then
      window=$(tmux display-message -p -t "$TMUX_PANE" '#{window_name}' 2>/dev/null || true)
    fi

    body=$(jq -cn --arg a "$agent" --arg k "$kind" --arg s "$summary" --arg w "$window" \
      '{agent: $a, kind: $k, summary: $s} + (if $w != "" then {window: $w} else {} end)')
    curl -s -m 5 --unix-socket "$sock" -H 'Content-Type: application/json' \
      -d "$body" http://repose/ >/dev/null 2>&1
    exit 0
  '';
}
