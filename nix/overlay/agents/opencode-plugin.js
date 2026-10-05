// repose opencode plugin: relays session events to repose-hook, which POSTs
// them to /run/repose/hooks.sock (docs/interfaces/guest-conventions.md).
// Installed to ~/.config/opencode/plugins/repose.js by repose-agent-setup;
// replaced only while it is byte for byte a copy an earlier base installed,
// so a user may edit it (DECISIONS I-481).
//
// One default export serves both opencode lines: 1.18.29 and later call
// server() and hand it the V1 event hook; OpenCode 2 calls setup() and
// events arrive on ctx.event.subscribe(). V1 events carry `properties`,
// V2 events carry `data`.
import { spawn } from "node:child_process";

const send = (kind, summary) =>
  new Promise((resolve) => {
    const body = JSON.stringify({
      agent: "opencode",
      kind,
      summary: String(summary || "").slice(0, 1000),
    });
    try {
      // --agent: OpenCode 2's background service is not started by the
      // repose wrapper, so REPOSE_HOOK_AGENT is not in its environment.
      const child = spawn("repose-hook", ["--agent", "opencode", body], { stdio: "ignore" });
      const timer = setTimeout(() => child.kill(), 10000);
      child.on("error", () => { clearTimeout(timer); resolve(); });
      child.on("exit", () => { clearTimeout(timer); resolve(); });
    } catch (_) {
      // a hook failure never blocks the agent
      resolve();
    }
  });

const errorText = (err) => {
  if (!err) return "";
  if (typeof err === "string") return err;
  return err.message || (err.data && err.data.message) || err.name || "";
};

// V1 (1.18.x): session.idle, session.error, permission.asked (and the
// older permission.updated).
const onV1 = async (event) => {
  const type = (event && event.type) || "";
  const p = (event && event.properties) || {};
  if (type === "session.idle") {
    await send("completed", "opencode finished");
  } else if (type === "session.error") {
    await send("error", errorText(p.error) || "opencode error");
  } else if (type === "permission.updated" || type === "permission.asked") {
    await send("needs_input", p.title || p.message || p.type || "opencode needs permission");
  }
};

// V2 (checked against 2.0.22): a turn ends with session.execution.succeeded
// or session.execution.failed; session.text.ended carries the reply, kept
// per session for the summary. permission.asked is unchanged from V1.
const lastText = new Map();
const onV2 = async (event) => {
  const type = (event && event.type) || "";
  const d = (event && event.data) || {};
  const sid = d.sessionID || "";
  if (type === "session.text.ended") {
    if (d.text) lastText.set(sid, String(d.text).slice(-1000));
  } else if (type === "session.execution.succeeded") {
    const text = lastText.get(sid);
    lastText.delete(sid);
    await send("completed", text || "opencode finished");
  } else if (type === "session.execution.failed") {
    lastText.delete(sid);
    await send("error", errorText(d.error) || "opencode error");
  } else if (type === "session.execution.interrupted") {
    lastText.delete(sid);
  } else if (type === "permission.asked") {
    await send("needs_input", d.message || (d.action ? "opencode asks for " + d.action : "opencode needs permission"));
  }
};

export default {
  id: "repose",
  async server() {
    return { event: async ({ event }) => onV1(event) };
  },
  setup(ctx) {
    const controller = new AbortController();
    void (async () => {
      try {
        for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
          await onV2(event);
        }
      } catch (_) {
        // aborted at cleanup, or the stream ended
      }
    })();
    return () => controller.abort();
  },
};
