# gemini: extension hooks

Gemini CLI 0.61.0 runs the hooks of an extension's `hooks/hooks.json`,
also in a folder the user has not trusted. The platform's extension
(`/etc/repose/gemini-extension`, linked to
`~/.gemini/extensions/repose-machine-guide`) declares two, both running
`repose-hook --agent gemini` (DECISIONS I-690):

- `BeforeAgent` (`before-agent.json`): the user sent a prompt. It maps to
  the socket kind `prompt`; the `prompt` field is never decoded.
- `AfterAgent` (`after-agent.json`): the turn ended. It maps to
  `completed` with `prompt_response` as the summary, also with
  `stop_hook_active` true, which marks the end of a turn a hook forced on
  (`after-agent-hook-active.json`, written by hand in the captured shape).

Both fixtures were captured from the real binary in a temp HOME against a
stand-in model API (2026-10-12). Until the first prompt hook arrives a
gemini window is `running` and the pane-idle heuristic reports its turns
(`TestHeuristicCompletionForAHooklessAgent`).

`heuristic.json` is the socket's own shape, which `Map` accepts for any
agent.
