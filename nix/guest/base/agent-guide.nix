# The machine guide (DECISIONS I-243): one source, ./agent-guide.md, put
# where each agent reads global instructions, never in a file the user owns.
#
#   Claude Code  /etc/claude-code/CLAUDE.md, its managed memory file on Linux
#   Codex        /etc/codex/config.toml `developer_instructions`, the system
#                config layer (a user-level `developer_instructions` replaces it)
#   opencode     /etc/opencode/opencode.json `instructions`, the managed config
#                dir; `instructions` arrays are unioned across config layers
#   Gemini CLI   an extension whose context file is the guide, linked into
#                ~/.gemini/extensions/ by repose-agent-setup (no system layer
#                for context; imports from outside the workspace are refused)
#   pi           an extension that adds the guide as a system prompt section,
#                linked into ~/.pi/agent/extensions/ by repose-agent-setup
#
# /etc/codex/config.toml also starts Codex the way Claude Code starts
# (DECISIONS I-546): approval_policy "never", sandbox_mode
# "danger-full-access" and no update check at startup, top-level keys
# before developer_instructions. It is the lowest config layer, so a key in
# ~/.codex/config.toml wins key by key.
#
# The render drops HTML comments and every line marked `needs: CMD` whose
# CMD the guest does not have, so an agent is never told to run a command
# that is not there (checked against the guest's own system path), and
# puts the Playwright release whose Chromium the guest seeds (compat.nix)
# in place of @playwrightVersion@ (I-548).
{ config, lib, pkgs, ... }:
let
  rendered = pkgs.runCommand "repose-agent-guide" {
    src = ./agent-guide.md;
    sw = config.system.path;
    playwrightVersion = pkgs.playwright-driver.version;
    nativeBuildInputs = [ pkgs.jq ];
  } ''
    mkdir -p $out/gemini-extension
    in_comment=0
    while IFS= read -r line || [ -n "$line" ]; do
      if [ $in_comment = 1 ]; then
        case "$line" in *"-->"*) in_comment=0 ;; esac
        continue
      fi
      case "$line" in
        "<!--"*) case "$line" in *"-->"*) ;; *) in_comment=1; continue ;; esac ;;
      esac
      if [[ "$line" =~ \<!--\ needs:\ ([A-Za-z0-9._-]+)\ --\> ]]; then
        [ -x "$sw/bin/''${BASH_REMATCH[1]}" ] || continue
      fi
      printf '%s\n' "$line"
    done < $src | sed -E 's/[[:space:]]*<!--([^-]|-[^-])*-->//g; s/[[:space:]]+$//' \
      | sed '/./,$!d' | sed "s/@playwrightVersion@/$playwrightVersion/g" > $out/agent-guide.md

    # Top-level keys only: in TOML every key after a [table] header belongs
    # to that table. A JSON string is a valid TOML basic string.
    {
      printf 'approval_policy = "never"\nsandbox_mode = "danger-full-access"\ncheck_for_update_on_startup = false\n'
      printf 'developer_instructions = '; jq -Rs . $out/agent-guide.md
    } > $out/codex-config.toml
    jq -n '{ "$schema": "https://opencode.ai/config.json", instructions: [ "/etc/repose/agent-guide.md" ] }' > $out/opencode.json
    jq -n '{ name: "repose-machine-guide", version: "1.0.0", contextFileName: "GEMINI.md" }' > $out/gemini-extension/gemini-extension.json
    cp $out/agent-guide.md $out/gemini-extension/GEMINI.md
  '';

  # pi has no system-level instructions file; its extensions can add a
  # section to the system prompt. Read at each run, so it follows the guide
  # through base updates without being re-linked.
  piExtension = pkgs.writeText "repose-pi-extension.js" ''
    // repose machine guide (DECISIONS I-243). Managed by the platform:
    // repose-agent-setup links it here; do not edit.
    const fs = require("node:fs");
    module.exports = function (pi) {
      pi.on("before_agent_start", (event) => {
        let text;
        try { text = fs.readFileSync("/etc/repose/agent-guide.md", "utf8"); } catch { return; }
        const sections = event.systemPromptOptions && event.systemPromptOptions.sections;
        if (sections) { sections.repose_machine = text; return; }
        return { systemPrompt: event.systemPrompt + "\n\n" + text };
      });
    };
  '';
in
{
  environment.etc = {
    "repose/agent-guide.md".source = "${rendered}/agent-guide.md";
    "claude-code/CLAUDE.md".source = "${rendered}/agent-guide.md";
    "codex/config.toml".source = "${rendered}/codex-config.toml";
    "opencode/opencode.json".source = "${rendered}/opencode.json";
    "repose/gemini-extension".source = "${rendered}/gemini-extension";
    "repose/pi-extension.js".source = piExtension;
  };
}
