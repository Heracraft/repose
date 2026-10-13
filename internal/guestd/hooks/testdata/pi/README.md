# pi: extension events

pi 1.0.4 has no hook configuration; the platform's pi extension
(`/etc/repose/pi-extension.js`) listens to pi's own events and runs
`repose-hook --agent pi` with the socket's shape (DECISIONS I-690):

- `input` (not from an extension): `{"agent":"pi","kind":"prompt"}`.
- `agent_end`: `completed`, with the last assistant text as the summary.

Both events were seen firing from the real binary in a temp HOME against a
stand-in model API (2026-10-12). `Map("pi", ...)` therefore accepts only
the socket's own shape (`heuristic.json`); anything else is no event.
Until the first hook arrives a pi window is `running` and the pane-idle
heuristic reports its turns.
