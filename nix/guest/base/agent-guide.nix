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
# The platform MCP servers (config.repose.mcpServers, browser.nix) go in the
# same places where an agent has a layer beneath the user's file, carrying
# only command and args so a user's field-level override stays clean
# (DECISIONS I-553):
#
#   Claude Code  ~/.claude.json mcpServers, merged from /etc/repose/mcp.json
#                by repose-agent-setup (managed-mcp.json would take
#                exclusive control and refuse `claude mcp add`)
#   Codex        ~/.codex/config.toml [mcp_servers.NAME], written by
#                `repose-mcp sync codex` (I-555), or appended by
#                repose-agent-setup on a base without it, when the name is
#                absent: a same-name
#                user table with `url` beside a system-layer `command`
#                stops Codex from loading its config
#   opencode     /etc/opencode/opencode.json `mcp`, type local, no `enabled`
#   Gemini CLI   /etc/gemini-cli/system-defaults.json `mcpServers` (browser.nix)
#   pi           pi.registerMcpServer in the extension below (pi 0.99 and
#                later); a ~/.pi/agent/mcp.json entry of the same name wins
#
# The render drops HTML comments and every line marked `needs: CMD` whose
# CMD the guest does not have, so an agent is never told to run a command
# that is not there (checked against the guest's own system path).
{ config, lib, pkgs, ... }:
let
  mcpServers = pkgs.writeText "repose-mcp-servers.json" (builtins.toJSON config.repose.mcpServers);

  rendered = pkgs.runCommand "repose-agent-guide" {
    src = ./agent-guide.md;
    sw = config.system.path;
    mcp = mcpServers;
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
      | sed '/./,$!d' > $out/agent-guide.md

    { printf 'developer_instructions = '; jq -Rs . $out/agent-guide.md; } > $out/codex-config.toml
    jq '{ "$schema": "https://opencode.ai/config.json", instructions: [ "/etc/repose/agent-guide.md" ],
          mcp: with_entries(.value = { type: "local", command: ([ .value.command ] + .value.args) }) }' $mcp > $out/opencode.json
    jq -n '{ name: "repose-machine-guide", version: "1.0.0", contextFileName: "GEMINI.md" }' > $out/gemini-extension/gemini-extension.json
    cp $out/agent-guide.md $out/gemini-extension/GEMINI.md
  '';

  # pi has no system-level instructions file; its extensions can add a
  # section to the system prompt. Read at each run, so it follows the guide
  # through base updates without being re-linked.
  #
  # The same extension registers the platform MCP servers from
  # /etc/repose/mcp.json, and those in ~/.repose/mcp/agents/pi.json once
  # repose-mcp writes it (DECISIONS I-554). Each call is guarded on its own:
  # an older pi has no registerMcpServer, and a bad entry throws, neither of
  # which may cost the guide.
  piExtension = pkgs.writeText "repose-pi-extension.js" ''
    // repose machine guide (DECISIONS I-243) and MCP servers (I-554).
    // Managed by the platform: repose-agent-setup links it here; do not edit.
    const fs = require("node:fs");
    module.exports = function (pi) {
      pi.on("before_agent_start", (event) => {
        let text;
        try { text = fs.readFileSync("/etc/repose/agent-guide.md", "utf8"); } catch { return; }
        const sections = event.systemPromptOptions && event.systemPromptOptions.sections;
        if (sections) { sections.repose_machine = text; return; }
        return { systemPrompt: event.systemPrompt + "\n\n" + text };
      });
      if (typeof pi.registerMcpServer !== "function") return;
      // mcp.json is Claude Code's shape, so only command and args are
      // taken from it; pi.json is written in pi's own shape.
      const platform = "/etc/repose/mcp.json";
      for (const file of [platform, (process.env.HOME || "") + "/.repose/mcp/agents/pi.json"]) {
        let reg;
        try { reg = JSON.parse(fs.readFileSync(file, "utf8")).mcpServers; } catch { continue; }
        for (const [name, s] of Object.entries(reg || {})) {
          try { pi.registerMcpServer(name, file === platform ? { command: s.command, args: s.args || [] } : s); } catch {}
        }
      }
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
