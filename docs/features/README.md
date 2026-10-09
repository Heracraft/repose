# Features

User-facing behaviour, one feature per file. Each doc says what the user sees,
the behaviour and edge cases that must hold (written so they can be turned
into tests), which workstreams it depends on, and what is deferred. When a
feature doc and a workstream doc disagree on user-visible behaviour, the
feature doc wins; on internals, the workstream doc wins.

| Feature | One line | Status |
|---|---|---|
| [projects.md](projects.md) | How a directory becomes a project, `repose run NAME`, per-account limits | built |
| [run-and-attach.md](run-and-attach.md) | `repose run`, `repose attach`, tmux sessions and windows, the agent picker | built |
| [sync-at-launch.md](sync-at-launch.md) | Git plus the one-shot diff of uncommitted work, refuse-on-dirty | built |
| [agents.md](agents.md) | The five agents, wrappers, hooks, Claude login, MCP support | built; `mcp forward` not built |
| [browser.md](browser.md) | The shared headed Chromium, Playwright MCP, chrome-devtools-mcp, `repose browser` (the viewer), `browser bridge` | built |
| [secrets.md](secrets.md) | Synced tool logins, `.env` files and named secrets | built |
| [config.md](config.md) | The menu, the Nix fragment, apply, base bumps, hold | built |
| [snapshots.md](snapshots.md) | Nightly and on-stop snapshots, list, restore, fork | built |
| [notifications.md](notifications.md) | Agent events by email and ntfy, `repose-notify`, `repose-ask` | built; Telegram, Discord not built |
| [ports-and-previews.md](ports-and-previews.md) | Auto-forward while attached, `repose open <port>`, preview URLs | partly built; preview URLs not built, not approved (2026-09-25) |
| [stop-start-destroy.md](stop-start-destroy.md) | Lifecycle states, what each one costs, retention | built, temporary machines included (I-347..I-355); idle auto-stop not built |
| [status-and-logs.md](status-and-logs.md) | `repose status`, `repose ls`, `repose logs`, the dashboard view | partly built; git state in status not built |
| [pricing.md](pricing.md) | Card before compute, the first day of compute, what is charged, the invoice, a failed payment | built |

Features with a written design that is not built live in the doc for the
nearest built feature (preview URLs in ports-and-previews.md, `mcp
forward` in agents.md). Teams have no doc
because there is no design yet; see `../DECISIONS.md` R5-6.
