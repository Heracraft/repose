// repose machine guide (DECISIONS I-243), MCP servers (I-554) and turn
// hooks (I-690, I-694).
// Managed by the platform: repose-agent-setup links it here; do not edit.
const fs = require("node:fs");
const { spawn } = require("node:child_process");
// report posts to guestd's hook socket through repose-hook, in the
// socket's own shape; a failure never reaches pi.
const report = (kind, summary) => {
  try {
    const body = JSON.stringify({ agent: "pi", kind, summary: String(summary || "").slice(0, 1000) });
    const child = spawn("repose-hook", ["--agent", "pi", body], { stdio: "ignore" });
    child.on("error", () => {});
    child.unref();
  } catch {}
};
const lastText = (messages) => {
  for (const m of [...(messages || [])].reverse()) {
    if (!m || m.role !== "assistant" || !Array.isArray(m.content)) continue;
    const text = m.content.filter((c) => c && c.type === "text").map((c) => c.text).join(" ").trim();
    if (text) return text;
  }
  return "";
};
module.exports = function (pi) {
  // The prompt's text is not passed on: guestd needs only the fact.
  pi.on("input", (event) => { if (!event || event.source !== "extension") report("prompt", ""); });
  pi.on("agent_end", (event) => { report("completed", lastText(event && event.messages) || "pi finished"); });
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
