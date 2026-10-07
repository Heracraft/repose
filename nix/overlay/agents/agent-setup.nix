# repose-agent-setup <agent>: make the agent report to repose-hook.
# Idempotent, never clobbers user configuration (the VM test asserts a
# pre-existing hook survives), exits 0 on every path so a wrapper can `|| true`.
#
# claude   ~/.claude/settings.json  hooks.Notification / hooks.Stop entries
#          running `repose-hook` are added unless an entry whose command
#          contains "repose-hook" already exists under that event;
#          permissions.defaultMode and skipDangerousModePermissionPrompt
#          from the platform file are added only when the user's file sets
#          no permissions.defaultMode (DECISIONS I-250); tui from the
#          platform file is added only when the user's file has no tui
#          (I-425);
#          ~/.claude.json MCP servers are `repose-mcp sync claude`'s
#          (DECISIONS I-555): the platform servers from /etc/repose/mcp.json
#          and the registry's in ~/.repose/mcp, user entries winning on
#          name clash except an entry repose wrote itself (rendered.json,
#          or mcp.json's repose_retired from an earlier base), which is
#          replaced; on a base without repose-mcp (the shell repose-hook)
#          the platform merge below does the same for the platform
#          servers. Every write to ~/.claude.json here holds Claude Code's
#          own lock, the directory ~/.claude.json.lock;
#          ~/.claude.json hasCompletedOnboarding is set to true when the
#          Claude login share is bind-mounted over .credentials.json and
#          the key is absent, since first-run onboarding asks for a login
#          method even when the shared file already holds one (I-278);
#          ~/.claude.json hasSeenAutoDefaultNudge is set to true when
#          settings.json's permissions.defaultMode is bypassPermissions and
#          the key is absent: Claude Code's one-time "Make auto mode your
#          default?" dialog otherwise takes the first prompt `repose run`
#          sends, and its Enter answers "Yes", rewriting defaultMode to auto
#          (I-283); in that mode settings.json's
#          skipDangerousModePermissionPrompt is set to true when absent,
#          whoever set the mode, since the bypass warning dialog otherwise
#          takes that prompt the same way (I-306).
# codex   ~/.codex/config.toml gains `notify = ["repose-hook"]` unless a
#          `notify` key already exists. Its MCP servers are `repose-mcp
#          sync codex`'s (DECISIONS I-555), the platform servers included;
#          on a base without repose-mcp, an [mcp_servers.NAME] table
#          (command and args) is appended for each platform server in
#          /etc/repose/mcp.json whose NAME the parsed file does not have
#          (I-553). A table the user changed, `enabled = false` included,
#          is theirs and stays; a file that does not parse is left alone.
#          A config.toml that is a symlink (home-manager, dotfiles) stays a
#          link: notify is written through it when the target is a
#          writable file, and its MCP servers are left alone.
#          Runs under flock ~/.repose/mcp/.lock, the lock repose-mcp sync
#          takes, since agents start in parallel and a duplicate table
#          stops Codex.
# every agent ends with `repose-mcp sync <agent>` where repose-mcp exists,
#          which renders ~/.repose/mcp into that agent's own config
#          (DECISIONS I-555) and exits 0 on every path.
# opencode ~/.config/opencode/plugins/repose.js is installed if absent, and
#          replaced while it is byte for byte one an earlier base installed
#          (I-481). The plugin serves opencode 1.18 and OpenCode 2; the
#          repose-agent-hooks user unit runs this at login too, so an
#          OpenCode 2 the user installed reports without the wrapper.
# gemini, pi: no hooks (guestd's pane-idle heuristic reports for them); the
#          machine guide (DECISIONS I-243) is linked in as an extension:
#          ~/.gemini/extensions/repose-machine-guide -> /etc/repose/gemini-extension,
#          ~/.pi/agent/extensions/repose-machine-guide.js -> /etc/repose/pi-extension.js.
#          A file or directory the user put at either path is left alone.
#          gemini, once per home: a Gemini CLI that updated itself into
#          npm's global prefix (bases before I-543) is removed, bin/gemini
#          and lib/node_modules/@google/gemini-cli, when that package is
#          @google/gemini-cli; it sat ahead of this wrapper on PATH. The
#          marker ~/.local/state/repose/gemini-npm-removed keeps a Gemini
#          CLI the user installs there later.
# herdr   inside a herdr pane (HERDR_ENV=1), for claude, codex, opencode
#          and pi: `herdr integration install <agent>` with the base's
#          herdr when `herdr integration status` does not list the agent's
#          integration as current, so herdr knows the agent's state and
#          resumes it after a restart. A hook older than the base's herdr
#          expects, or one needing repair, is replaced; a newer one (carried
#          from a newer laptop herdr) counts as current and is kept, since
#          herdr reports any version at or above its own as current. Best
#          effort and silent (DECISIONS I-501, I-560).
{ lib, writeShellApplication, jq, python3, coreutils, util-linux, gnugrep, reposeOpencodePlugin, herdr }:
writeShellApplication {
  name = "repose-agent-setup";
  runtimeInputs = [ jq python3 coreutils util-linux gnugrep ];
  text = ''
    agent="''${1:-}"
    platform_claude=/etc/repose/claude-settings.json
    platform_mcp=/etc/repose/mcp.json

    write_atomic() { # write_atomic <path> <mode>  (content on stdin)
      local path="$1" mode="$2" tmp
      tmp=$(mktemp -p "$(dirname "$path")")
      cat > "$tmp"
      chmod "$mode" "$tmp"
      mv -f "$tmp" "$path"
    }

    # with_claude_lock CMD...: run CMD holding Claude Code's own lock on
    # ~/.claude.json (proper-lockfile's directory ~/.claude.json.lock), so
    # a read-modify-write here never loses one Claude Code makes. A lock
    # older than 10 s was abandoned and is taken over, as Claude Code does;
    # after 5 s of waiting the write is skipped.
    with_claude_lock() {
      local lock="$HOME/.claude.json.lock" n=0 age rc=0
      until mkdir "$lock" 2>/dev/null; do
        age=$(( $(date +%s) - $(stat -c %Y "$lock" 2>/dev/null || date +%s) ))
        # A stale lock that will not go (a file, a full or foreign
        # directory) counts toward the 5 s like a live one.
        if [ "$age" -gt 10 ] && rmdir "$lock" 2>/dev/null; then
          continue
        fi
        n=$((n + 1))
        if [ "$n" -ge 100 ]; then
          echo "repose-agent-setup: ~/.claude.json stayed locked; leaving it alone" >&2
          return 0
        fi
        sleep 0.05
      done
      "$@" || rc=$?
      rmdir "$lock" 2>/dev/null || true
      return "$rc"
    }

    # mcp_sync <agent>: render the MCP registry into the agent's config
    # (DECISIONS I-555). A base with the shell repose-hook has no
    # repose-mcp; it must not print "command not found" above the agent.
    mcp_sync() {
      if command -v repose-mcp >/dev/null 2>&1; then
        repose-mcp sync "$1" || true
      fi
    }

    # The platform servers into ~/.claude.json, for a base without
    # repose-mcp; repose-mcp sync claude does this with the registry's
    # servers otherwise. Called through with_claude_lock.
    # shellcheck disable=SC2329
    merge_platform_mcp() {
      local userjson="$HOME/.claude.json"
      if [ ! -s "$userjson" ]; then
        jq '{ mcpServers: .mcpServers }' "$platform_mcp" | write_atomic "$userjson" 0600
      elif jq -e . "$userjson" >/dev/null 2>&1; then
        # A user entry that is exactly one the platform registered
        # before (repose_retired) was written here, not by the user, and
        # gives way to the current one (I-246).
        jq -s '.[0] as $u | .[1] as $p
          | (($u.mcpServers // {}) | with_entries(
              .key as $k | .value as $v
              | select(any((($p.repose_retired // {})[$k] // [])[]; . == $v) | not))) as $kept
          | $u | .mcpServers = ($p.mcpServers + $kept)' \
          "$userjson" "$platform_mcp" | write_atomic "$userjson" 0600
      else
        echo "repose-agent-setup: $userjson is not valid JSON; leaving it alone" >&2
      fi
    }

    setup_claude() {
      local settings="$HOME/.claude/settings.json"
      mkdir -p "$HOME/.claude"
      if [ -r "$platform_claude" ]; then
        if [ ! -s "$settings" ]; then
          write_atomic "$settings" 0600 < "$platform_claude"
        elif jq -e . "$settings" >/dev/null 2>&1; then
          jq -s '
            def has_repose(arr): ((arr // []) | any(.[]; ((.hooks // []) | any(.[]; ((.command // "") | contains("repose-hook"))))));
            .[0] as $user | .[1] as $platform
            | reduce ($platform.hooks | keys[]) as $ev ($user;
                if has_repose(.hooks[$ev]) then .
                else .hooks[$ev] = ((.hooks[$ev] // []) + $platform.hooks[$ev]) end)
            # The platform default mode (I-250) only where the user has
            # none: a defaultMode the user or the laptop set is theirs.
            | if ($platform.permissions.defaultMode? // null) == null
                 or ((.permissions // {}) | type) != "object"
                 or ((.permissions // {}) | has("defaultMode"))
              then .
              else .permissions.defaultMode = $platform.permissions.defaultMode
                | if has("skipDangerousModePermissionPrompt") or ($platform | has("skipDangerousModePermissionPrompt") | not) then .
                  else .skipDangerousModePermissionPrompt = $platform.skipDangerousModePermissionPrompt end
              end
            # The fullscreen renderer (I-425) only where the user chose
            # none; "default" from the user or the laptop is theirs.
            | if ($platform | has("tui")) and (has("tui") | not)
              then .tui = $platform.tui else . end
          ' "$settings" "$platform_claude" | write_atomic "$settings" 0600
        else
          echo "repose-agent-setup: $settings is not valid JSON; leaving it alone" >&2
        fi
      fi
      if command -v repose-mcp >/dev/null 2>&1; then
        mcp_sync claude
      elif [ -r "$platform_mcp" ]; then
        with_claude_lock merge_platform_mcp
      fi
      # The shared login (I-278) is signed in already; without this, the
      # first interactive start still shows the theme and login-method
      # screens. /login stays available.
      if findmnt -n --mountpoint "$HOME/.claude/.credentials.json" >/dev/null 2>&1; then
        with_claude_lock userjson_default hasCompletedOnboarding
      fi
      # The auto-mode offer (I-283) only where the mode is the bypass the
      # platform or the user chose; answering it is Shift-Tab or
      # defaultMode, not a dialog a sent prompt can hit.
      if [ "$(jq -r '.permissions.defaultMode? // empty' "$settings" 2>/dev/null)" = bypassPermissions ]; then
        with_claude_lock userjson_default hasSeenAutoDefaultNudge
        # A bypass mode the user set (their laptop's settings, carried
        # in) came without the platform's skip flag, and Claude Code's
        # bypass warning then takes the prompt `repose run` sends, and
        # declining it exits Claude Code (I-306).
        # The user's own value, false included, is kept.
        if jq -e 'has("skipDangerousModePermissionPrompt") | not' "$settings" >/dev/null 2>&1; then
          jq '.skipDangerousModePermissionPrompt = true' "$settings" | write_atomic "$settings" 0600
        fi
      fi
    }

    # userjson_default <key>: set ~/.claude.json's <key> to true unless the
    # key is there already (any value, the user's false included). Called
    # through with_claude_lock.
    # shellcheck disable=SC2329
    userjson_default() {
      local userjson="$HOME/.claude.json" key="$1"
      if [ ! -s "$userjson" ]; then
        jq -n --arg k "$key" '{($k): true}' | write_atomic "$userjson" 0600
      elif jq -e --arg k "$key" 'has($k) | not' "$userjson" >/dev/null 2>&1; then
        jq --arg k "$key" '.[$k] = true' "$userjson" | write_atomic "$userjson" 0600
      fi
    }

    setup_codex() {
      local cfg="$HOME/.codex/config.toml" lock="$HOME/.repose/mcp/.lock"
      mkdir -p "$HOME/.codex"
      mkdir -p "$HOME/.repose/mcp"
      chmod 0700 "$HOME/.repose/mcp"
      exec 9>>"$lock"
      if ! flock -w 5 9; then
        echo "repose-agent-setup: $lock is held; leaving $cfg alone" >&2
        exec 9>&-
        return 0
      fi
      # A link (home-manager, a dotfiles repo) belongs to whatever made
      # it: a rename would replace the link with a file. notify goes into
      # the link's target, by temp file and rename beside it, so a full
      # disk or a kill never leaves the user's file half written. A target
      # repose cannot write that way (home-manager's store) is named once
      # per target (~/.repose/mcp/codex-link-warned). repose-mcp sync
      # leaves the servers alone, and still records what Codex would get
      # in ~/.repose/mcp/agents/codex.json.
      if [ -L "$cfg" ]; then
        if ! grep -Eq '^[[:space:]]*notify[[:space:]]*=' "$cfg" 2>/dev/null; then
          local body target warned="$HOME/.repose/mcp/codex-link-warned"
          target=$(readlink -f "$cfg" 2>/dev/null) || target=
          if [ -n "$target" ] && [ -f "$target" ] && [ -w "$target" ] && [ -w "$(dirname "$target")" ] && body=$(cat "$target"); then
            printf 'notify = ["repose-hook"]\n%s\n' "$body" | write_atomic "$target" "$(stat -c %a "$target")"
          elif [ "$(cat "$warned" 2>/dev/null)" != "$target" ]; then
            echo "repose-agent-setup: $cfg links to a file repose cannot write; add notify = [\"repose-hook\"] to it for Codex notifications" >&2
            printf '%s\n' "$target" > "$warned"
          fi
        fi
        exec 9>&-
        mcp_sync codex
        return 0
      fi
      if [ ! -e "$cfg" ]; then
        printf 'notify = ["repose-hook"]\n' | write_atomic "$cfg" 0600
      elif ! grep -Eq '^[[:space:]]*notify[[:space:]]*=' "$cfg"; then
        { printf 'notify = ["repose-hook"]\n'; cat "$cfg"; } | write_atomic "$cfg" 0600
      fi
      # repose-mcp sync takes the same lock, so it runs after the release.
      if command -v repose-mcp >/dev/null 2>&1; then
        exec 9>&-
        mcp_sync codex
        return 0
      fi
      codex_mcp "$cfg" || true
      exec 9>&-
    }

    # toml_json: TOML on stdin to JSON on stdout through Python's tomllib,
    # which is as strict as Codex's parser (an inline table extended by a
    # later header is refused by both; yj accepts it).
    toml_json() {
      python3 -c 'import json, sys, tomllib; json.dump(tomllib.load(sys.stdin.buffer), sys.stdout, default=str)'
    }

    # codex_mcp <config.toml>: append a table for each platform server the
    # parsed file has no mcp_servers entry for. Presence is judged from the
    # parse, so an inline table or a dotted key counts; the result is
    # parsed again before it replaces the file.
    codex_mcp() {
      local cfg="$1" have add
      [ -r "$platform_mcp" ] || return 0
      # A file that does not parse is left alone without a word: Codex
      # names the error itself when it starts.
      have=$(toml_json < "$cfg" 2>/dev/null) || return 0
      if ! add=$(jq -r --argjson have "$have" '
        (($have.mcp_servers? // {}) | if type == "object" then . else {} end) as $m
        | .mcpServers | to_entries[] | select(.key as $k | $m | has($k) | not)
        | "\n[mcp_servers.\(.key | if test("^[A-Za-z0-9_-]+$") then . else tojson end)]\ncommand = \(.value.command | tojson)\nargs = \(.value.args // [] | tojson)"
      ' "$platform_mcp"); then
        echo "repose-agent-setup: $platform_mcp did not render; leaving $cfg alone" >&2
        return 0
      fi
      [ -n "$add" ] || return 0
      # A file the tables cannot be appended to (an inline mcp_servers
      # table) keeps its servers as the user wrote them, quietly, since the
      # same check fails at every start.
      { cat "$cfg"; printf '%s\n' "$add"; } | toml_json >/dev/null 2>&1 || return 0
      { cat "$cfg"; printf '%s\n' "$add"; } | write_atomic "$cfg" 0600
    }

    # link_owned <link> <target>: the link is ours by name; point it at the
    # target unless something that is not a symlink sits there.
    link_owned() {
      local link="$1" target="$2"
      [ -e "$target" ] || return 0
      if [ -L "$link" ]; then
        [ "$(readlink "$link")" = "$target" ] || ln -sfn "$target" "$link"
      elif [ -e "$link" ]; then
        echo "repose-agent-setup: $link is not a link to $target; leaving it alone" >&2
      else
        mkdir -p "$(dirname "$link")"
        ln -s "$target" "$link"
      fi
    }

    # remove_npm_gemini: once per home, the Gemini CLI an earlier base let
    # update itself into npm's global prefix, ahead of the wrapper (I-543).
    remove_npm_gemini() {
      local prefix="''${NPM_CONFIG_PREFIX:-$HOME/.npm-global}" marker="$HOME/.local/state/repose/gemini-npm-removed"
      local pkgdir="$prefix/lib/node_modules/@google/gemini-cli"
      [ -e "$marker" ] && return 0
      if [ "$(jq -r '.name? // empty' "$pkgdir/package.json" 2>/dev/null)" = "@google/gemini-cli" ]; then
        if [ -L "$prefix/bin/gemini" ] && [ "$(readlink "$prefix/bin/gemini")" = ../lib/node_modules/@google/gemini-cli/bundle/gemini.js ]; then
          rm -f "$prefix/bin/gemini"
        fi
        rm -rf "$pkgdir"
        echo "repose-agent-setup: removed the Gemini CLI that updated itself into $prefix; gemini is the machine's again" >&2
      fi
      mkdir -p "$(dirname "$marker")" && : > "$marker"
    }

    setup_gemini() {
      link_owned "$HOME/.gemini/extensions/repose-machine-guide" /etc/repose/gemini-extension
      remove_npm_gemini
      mcp_sync gemini
    }

    setup_pi() {
      link_owned "''${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}/extensions/repose-machine-guide.js" /etc/repose/pi-extension.js
      mcp_sync pi
    }

    # sha256 of every repose.js an earlier base installed. A file equal to
    # one of them is the platform's and is replaced; anything else is the
    # user's and stays (DECISIONS I-481).
    opencode_retired="8f5f76a5dc77376f3ad38127a42959c8448fcb812ff75ee71291bb762f990a49"

    setup_opencode() {
      local dir="$HOME/.config/opencode/plugins" have
      mkdir -p "$dir"
      if [ ! -e "$dir/repose.js" ]; then
        write_atomic "$dir/repose.js" 0644 < ${reposeOpencodePlugin}
      elif [ -f "$dir/repose.js" ] && [ ! -L "$dir/repose.js" ]; then
        have=$(sha256sum "$dir/repose.js" | cut -d' ' -f1)
        case " $opencode_retired " in
          *" $have "*) write_atomic "$dir/repose.js" 0644 < ${reposeOpencodePlugin} ;;
        esac
      fi
      mcp_sync opencode
    }

    # herdr's own integration for this agent, only in a herdr pane. Its
    # status line reads "<agent>: current (vN) (<path>)" when installed
    # and up to date; anything else (not installed, outdated, needs
    # repair) gets an install. Ten seconds at most, never a message.
    setup_herdr() {
      local target="$1" status
      [ "''${HERDR_ENV:-}" = 1 ] || return 0
      status=$(timeout 10 ${herdr}/bin/herdr integration status 2>/dev/null) || return 0
      if ! printf '%s\n' "$status" | grep -q "^$target: current "; then
        timeout 10 ${herdr}/bin/herdr integration install "$target" >/dev/null 2>&1 || true
      fi
    }

    case "$agent" in
      claude) setup_claude ;;
      codex) setup_codex ;;
      opencode) setup_opencode ;;
      gemini) setup_gemini ;;
      pi) setup_pi ;;
      *) echo "repose-agent-setup: unknown agent '$agent'" >&2 ;;
    esac
    case "$agent" in
      claude|codex|opencode|pi) setup_herdr "$agent" ;;
    esac
    exit 0
  '';
}
