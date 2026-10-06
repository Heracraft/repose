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
#          ~/.claude.json mcpServers gains the platform servers from
#          /etc/repose/mcp.json, user entries winning on name clash
#          except an entry the platform registered itself in an earlier
#          base (mcp.json's repose_retired), which is replaced;
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
#          `notify` key already exists.
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
{ lib, writeShellApplication, jq, coreutils, util-linux, reposeOpencodePlugin }:
writeShellApplication {
  name = "repose-agent-setup";
  runtimeInputs = [ jq coreutils util-linux ];
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

    setup_claude() {
      local settings="$HOME/.claude/settings.json" userjson="$HOME/.claude.json"
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
      if [ -r "$platform_mcp" ]; then
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
      fi
      # The shared login (I-278) is signed in already; without this, the
      # first interactive start still shows the theme and login-method
      # screens. /login stays available.
      if findmnt -n --mountpoint "$HOME/.claude/.credentials.json" >/dev/null 2>&1; then
        userjson_default hasCompletedOnboarding
      fi
      # The auto-mode offer (I-283) only where the mode is the bypass the
      # platform or the user chose; answering it is Shift-Tab or
      # defaultMode, not a dialog a sent prompt can hit.
      if [ "$(jq -r '.permissions.defaultMode? // empty' "$settings" 2>/dev/null)" = bypassPermissions ]; then
        userjson_default hasSeenAutoDefaultNudge
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
    # key is there already (any value, the user's false included).
    userjson_default() {
      local userjson="$HOME/.claude.json" key="$1"
      if [ ! -s "$userjson" ]; then
        jq -n --arg k "$key" '{($k): true}' | write_atomic "$userjson" 0600
      elif jq -e --arg k "$key" 'has($k) | not' "$userjson" >/dev/null 2>&1; then
        jq --arg k "$key" '.[$k] = true' "$userjson" | write_atomic "$userjson" 0600
      fi
    }

    setup_codex() {
      local cfg="$HOME/.codex/config.toml"
      mkdir -p "$HOME/.codex"
      if [ ! -e "$cfg" ]; then
        printf 'notify = ["repose-hook"]\n' | write_atomic "$cfg" 0600
      elif ! grep -Eq '^[[:space:]]*notify[[:space:]]*=' "$cfg"; then
        { printf 'notify = ["repose-hook"]\n'; cat "$cfg"; } | write_atomic "$cfg" 0600
      fi
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

    setup_gemini() {
      link_owned "$HOME/.gemini/extensions/repose-machine-guide" /etc/repose/gemini-extension
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

    setup_pi() {
      link_owned "''${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}/extensions/repose-machine-guide.js" /etc/repose/pi-extension.js
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
    }

    case "$agent" in
      claude) setup_claude ;;
      codex) setup_codex ;;
      opencode) setup_opencode ;;
      gemini) setup_gemini ;;
      pi) setup_pi ;;
      *) echo "repose-agent-setup: unknown agent '$agent'" >&2 ;;
    esac
    exit 0
  '';
}
