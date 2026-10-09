# 16 · multiplexer (herdr as the second multiplexer)

## 1. Goal

A project can run its terminals in herdr instead of tmux, chosen per
project, with agents, notifications, OOM protection, `repose ps`,
`paste`, temporary machines and the laptop's herdr sidebar working as
they do on tmux. Decided by the owner on 2026-10-05: DECISIONS
I-501..I-511, which supersede I-482. The design and the live test are in
`../proposals/2026-10-05-session-backends.md`; where it and the docs
below differ, the docs win.

Read before starting, in this order: the DECISIONS entries I-501 to
I-511 (and I-499, I-500, which `herdr-fixes` shipped), this file, then
the contract docs your branch owns or consumes (section 4).

## 2. Four branches

Every branch starts from `multiplexer-spec`, which carries the contracts,
the shared package `internal/multiplexer` and the queued `herdr-fixes`:

```
git worktree add ~/kanali-<branch> -b <branch> multiplexer-spec
```

| Branch | Stage | Ships as | Builds |
|---|---|---|---|
| `mux-api` | S1 | api, host (hostd) | migration, api fields, the base gate, hostd and api allowlists, the fakes |
| `mux-base` | S2 | base | the herdr package, units, seeded config, wrappers, integrations, workspace step, flake check |
| `mux-guestd` | S3 | base (guestd, repose-hook) | the herdr agent source, peer check, OOM and renice, `herdr_down`, `herdr:` hook windows, `ensureSession` |
| `mux-cli` | S4, S5 | cli, web | the multiplexer seam and its herdr side, config and flag, the laptop sidebar, the dashboard label, every public doc |

Release order, which the conductor follows: `mux-api` first (hostd and
the api must know `herdr_down` and the field before any base sends or
reads them); then `mux-base` and `mux-guestd` together in one base
publish (the units without guestd's `ensureSession`, or the reverse,
leave a herdr project with no session); then `mux-cli`, in the release
that sets `herdrMinBase` (section 3.1) to that base's version. Until that
release the api refuses every request for herdr, so nothing a user can
reach changes before the CLI and docs ship.

Parallel work: all four can start at once. `mux-cli` needs `mux-api`'s
fake api for its tests: it starts with the seam (no api change needed)
and runs `git merge mux-api` once `mux-api` has committed
`internal/fakes/api`. No other branch merges another.

## 3. Boundaries

### 3.1 mux-api (S1)

Builds:

- `internal/db/migrations/0017_project_multiplexer.{up,down}.sql`:
  `projects.multiplexer text not null default 'tmux' check (multiplexer
  in ('tmux','herdr'))`. 0017 because the queued `solo-at-20` holds
  0016; the number stays 0017 whatever order they land in.
  `ops/dev/schema-fixture.sql` if it lists project columns.
- `internal/api/store`: the column on the project row, read and written.
- `internal/api/http/projects.go`: `multiplexer` in the Project JSON, on
  POST (default `tmux`) and on PATCH; `400 invalid` outside
  `multiplexer.Names`; the gate. Fork and restore as new copy the
  source's value, falling back to `tmux` when the gate refuses.
- `internal/api/http/multiplexer.go` (new): `herdrMinBase` (empty in this
  branch) and the gate function `api.md` "The base gate" specifies,
  comparing `base_versions.released_at`.
- `internal/api/ops/phases.go`: `"multiplexer"` in the `project_json`
  map, normalized.
- `internal/hostd/guest/guestinput.go`: `herdr_down` in
  `guestWarningKinds`. `internal/api/events/events.go`: `herdr_down` in
  the warning kinds.
- `proto/repose/guestd/v1/guestd.proto`, `proto/repose/hostd/v1/hostd.proto`:
  comments on `tmux_window` and `tmux_clients` only ("a tmux window name
  or a herdr agent key, at most 64 bytes"; "tmux clients only"); no field
  is added, renamed or renumbered; `internal/gen` regenerated.
- `internal/fakes/api` (field, PATCH, the 409 with a settable min base)
  and `internal/fakes/hostd` (passes `project_json` through) for the
  other branches' tests.

Does not build: anything in `internal/cli`, `apps/web`, `nix/`,
`internal/guestd`, `cmd/repose-hook`. Does not set `herdrMinBase`.

Checklist (evidence in the commit message):

- [x] Migration 0017 applied, down and up, in `db_test.go` (output).
- [x] `curl` (or the api test) PATCH `{"multiplexer":"herdr"}` with the
      min base set, then GET shows it; `"screen"` answers 400; a project
      on an older or null base answers `409` with `detail.reason =
      base_update_needed`; with `herdrMinBase` empty every herdr request
      answers 409 with `needs: ""`; `tmux` is never refused (test names
      and output).
- [x] `project_json` sent on StartGuest carries `multiplexer` (test).
- [x] `herdr_down` passes hostd's `cleanWarning` unchanged and the api
      stores it under its own kind (unit tests).
- [x] A POST without `multiplexer` creates a tmux project; a Project
      decoded by a client struct without the field still decodes (test).
- [x] `go test` for `./internal/api/...`, `./internal/hostd/...`,
      `./internal/db/...`, `./internal/fakes/...`, per package, on
      Postgres.

### 3.2 mux-base (S2)

Builds, all under `nix/` and `scripts/`:

- `nix/overlay/agents/herdr.nix` (new), its entry in
  `nix/overlay/agents/default.nix` and `versions.json` (`herdr.version`,
  `herdr.x86_64-linux.url` and `.hash`, I-551; 0.9.3 or the newest
  release that passes the check),
  `scripts/bump-agents.sh` moving it. The install check:
  `herdr status client --json` with `endpoint_protocol_generation == 1`
  and `protocol >= 22`, else the build fails naming both values.
- `nix/guest/base/herdr.nix` (new), imported in
  `nix/guest/base/default.nix`: the package in `systemPackages`,
  `repose-herdr-server.service`, the scripts `repose-multiplexer-is` and
  `repose-herdr-workspace` (both on PATH), the seeded
  `~/.config/herdr/config.toml`. Exactly as `guest-conventions.md`
  "Session units" and "herdr" (Package, Server, Workspace, Seeded config).
- `nix/guest/base/tmux.nix`: `ExecCondition=repose-multiplexer-is tmux`
  on `repose-tmux-session.service`; the path unit removed.
- `nix/overlay/agents/wrap.nix`: `HERDR_AGENT=<binary>`.
  `nix/overlay/agents/agent-setup.nix`: the integration install.
- `nix/guest/base/env.nix`: nothing pushes into herdr; check the
  post-switch push leaves the herdr unit alone (it is a user unit, so the
  `systemctl --user set-environment` half reaches it at its next start).
- `nix/flake.nix`: `guest-session-survives-switch` also asserts
  `X-RestartIfChanged=false` on the herdr unit, and asserts no
  `repose-tmux-session.path` exists.
- `nix/guest/base/agent-guide.md` and `agent-guide.commands`: one line
  that a herdr project's terminals are herdr tabs, if the guide names
  tmux.

Does not build: guestd (`ensureSession` is `mux-guestd`'s; this branch
only provides the units it starts), `cmd/repose-hook`, any Go.

Contract it provides to the others: the unit names in
`internal/multiplexer`; `repose-multiplexer-is` and
`repose-herdr-workspace` with the behaviour in guest-conventions;
`herdr` on PATH in every guest; `HERDR_AGENT` in wrapped agents.

Checklist:

- [x] Build log of `herdr.nix` with the install check passing, and the
      same check failing against a fake binary that reports generation 2
      (both pasted).
- [x] `nix flake check` output with the extended
      `guest-session-survives-switch`, and the check failing with the
      herdr line removed. (On kanali: `nix build` of the check alone;
      never a full guest system build.)
- [x] `nix eval` of the guest config shows both units with their
      `ExecCondition`, no path unit, `restartIfChanged = false` on both.
- [x] `repose-multiplexer-is` against four `project.json` files (no key,
      `tmux`, `herdr`, `screen`) and no file: exit codes pasted.
- [ ] On a temporary machine (after the conductor deploys the branch's
      base, or with `nix copy` of the closure): `project.json`
      hand-edited to herdr and the machine restarted gives
      `systemctl --user is-active repose-herdr-server` active, `pgrep -c
      tmux` 0, the workspace in the checkout, a wrapped agent with
      `REPOSE_PROJECT`, a secret, the login PATH and `HERDR_AGENT`; a stop
      and start resumes claude with no client (process tree pasted). Needs
      `mux-guestd`'s `ensureSession` in the same base: test them together.
- [ ] A tmux project on the same base: `repose-tmux-session` active,
      the existing VM subtests unchanged and I-551's restart subtest
      passing (or, where VM tests cannot run
      here, say so in STATUS).

### 3.3 mux-guestd (S3)

Builds:

- `internal/guestd/sample/source.go` (new): the `Pane` and `Source`
  types of proposal 3.2; `tmuxSource` from today's `tmuxClient`
  (stat the socket first); `herdrSource` per guest-conventions "herdr",
  "Socket use by guestd" and "Agent keys"; the union in the watcher
  (`refreshTmux` becomes `refreshPanes`); `Reported` state used when
  set; the `state_change_seq` completion for gemini and pi; EOF grace of
  two refreshes; `herdr_down` and `tmux_down` gated on `project.json`.
- `internal/guestd/sample/oom.go`: the herdr server and its agents at
  -800, nice -5 on the server's threads, nice 0 for its tree below 0
  (I-505).
- `internal/guestd/project/project.go`: `Info.Multiplexer`
  (`json:"multiplexer,omitempty"`); `ensureTmux` becomes `ensureSession`,
  starting `multiplexer.Unit(info.Multiplexer)`.
- `internal/guestd/secrets`: the tmux push only when the tmux socket
  exists.
- `internal/guestd/warn`: the `herdr_down` kind.
- `internal/guestd/guestd.go` (`onHook`), `internal/guestd/hooks`,
  `internal/guestd/questions` as needed: `herdr:` windows resolved
  through the source, unresolved ones relayed with no state change.
- `cmd/repose-hook`: the `herdr:<$HERDR_PANE_ID>` window for hooks,
  `repose-notify` and `repose-ask` (I-506).
- `internal/fakes/guestd` if it models `SetupProject`.

Does not build: units or packages (`mux-base`), anything on the CLI,
`events.subscribe` (stage 6, later). Never calls `session.snapshot`,
`pane.process_info`, `pane.read` or any herdr method that changes state.
Logs carry ids, states, counts and error codes only: never a pane's
cwd, title, argv, a workspace label, an agent session id or anything
read from herdr beyond the six fields.

Checklist:

- [x] Tests against a fake herdr socket (a unix listener answering one
      line per connection) replaying the live test: working then done
      gives AgentState working then idle and one `completed` for gemini;
      a sequence that moves twice between two polls gives one
      `completed`; a peer with the wrong `SO_PEERCRED` uid is refused
      without a write; an EOF shorter than two refreshes emits no
      `unknown`; protocol 21 gives `herdr_down` and no agents; a reply
      over 1 MiB is dropped (test names and output).
- [x] The decoder struct has exactly the six fields (pasted), and a test
      feeds an `agent.list` reply with `title`, `terminal_title`, cwd-like
      tokens and `agent_session` and asserts none reach `AgentProc` or a
      log line.
- [x] Key rules: named agent, unnamed agent, other-checkout workspace,
      a collision with a tmux window (`claude (herdr)`), a 70-byte key
      cut to 64 (tests).
- [x] `ensureSession` starts the herdr unit for `herdr`, the tmux unit
      for no key, `tmux` and `screen` (test with the runner fake).
- [x] A hook with `window: "herdr:w2:p1"` sets the resolved agent's
      state; `herdr:nope` and `herdr:` plus 60 bytes change nothing and
      relay with an empty window (tests). Built as a window-less hook
      relays: the sink gets "", and `onHook` sends the agent's name as
      `tmux_window` (I-499, I-506; vsock-guestd.md now says so).
- [x] `TestStraceNeverOpensCmdlineOrEnviron` still green (output).
- [x] `go test` per package: `./internal/guestd/...`, `./cmd/repose-hook/...`.
- [ ] Live, with `mux-base` in the same base: `repose ls` AGENTS shows
      `claude working` during a prompt and `idle` after;
      `/proc/<claude>/oom_score_adj` is -800; every herdr server thread
      has nice -5 and a pane's shell nice 0; a herdr pane's `needs_input`
      leaves a tmux window named `claude` idle; `journalctl -u guestd |
      grep -c cwd` is 0; the I-494 load test (keystroke echo with
      `stress-ng` on every core) before and after the renice.

### 3.4 mux-cli (S4, S5)

Builds:

- `internal/cli/mux.go` (new): the `multiplexer` interface of proposal
  3.3. `mux_tmux.go`: today's `tmux.go` and the attach, ps, paste and
  temp code behind it, behaviour unchanged. `mux_herdr.go`: the herdr
  side exactly as `features/run-and-attach.md` "herdr projects".
- The backend probe (`systemctl --user -q is-active
  repose-herdr-server`), in the slow path and `fastpath.go`.
- Call sites: `run.go`, `fastpath.go`, `ps.go`, `paste.go`, `temp.go`,
  `session.go`, `bridge.go`, `bridgecmd.go`, `forward.go`,
  `carry_claude.go`, `inputproxy.go`, `reattach.go`, `carry.go` (TZ),
  `sync.go` (`repose-herdr-workspace` after the first sync, skipped on a
  base without it), `status.go`, `lifecycle.go` (`rm` removes the
  sidebar entry), `carry_tools.go` (`baseCommands` gains `herdr`).
- `config.go`: `default_multiplexer`. `run.go` and `sync.go`:
  `--multiplexer`, the pick order, the PATCH on an existing project, the
  fallback on `base_update_needed` for the `HERDR_ENV` pick only.
  `api_types.go`, `api_routes.go`: the field.
- `herdr_catalog.go` (new): the reconcile of I-510, called from `cert.go`
  where the project list is in hand, from `run`, `attach` and `rm`.
- `apps/web/src/lib/api/types.ts` and
  `apps/web/src/routes/projects/[id]/+page.svelte`: `multiplexer`; the
  `herdr` label beside the size; the tmux clients row hidden on herdr.
- Public docs in `apps/web/src/content/docs/`: `cli.md` (`--multiplexer`
  on `run` and `sync`, `default_multiplexer`), `run-and-attach.md`,
  `config.md`, `lifecycle.md` (temporary machines, stop), `secrets.md`
  (I-508), `notifications.md`, `agents.md`, `troubleshooting.md`
  (`herdr update` in a guest, old CLI on a herdr machine), and
  `tutorial-herdr.md` rewritten around `--multiplexer herdr` and the
  sidebar, dropping `machine add`, the install answer, `integration
  install`, the `cd` and the prefix rebind. Release notes say which CLI
  version herdr needs.

Does not build: anything outside `internal/cli`, `cmd/repose`,
`apps/web`. Does not edit `internal/fakes/api` (merge `mux-api`).

Checklist:

- [x] Unit tests for the pick order (flag, config, `HERDR_ENV`, temp,
      gate fallback), the PATCH on switch and the switch line, the three
      attach paths chosen by environment and laptop herdr version, the
      reconcile (add for a running herdr project; remove only
      `*.repose` entries of gone slugs; a hand-made entry for another
      host survives; a disabled one stays disabled; a temp machine never
      added; herdr older than 0.9.0 or a failing `machine list` does
      nothing).
- [x] The herdr ssh scripts tested against the 0.9.3 binary in a local
      herdr server (agent start, prompt, list, pane send-text,
      workspace create, worktree open) with outputs pasted.
- [x] `go test ./internal/cli -run Docs` passes with `cli.md`;
      `docs/CHECKLIST.md` greps run; `go test ./internal/cli/...` green
      but TestCarryGitConfig and TestToolsCarryOldBase.
- [ ] (open: needs the release that ships mux-api, mux-base, mux-guestd
      and sets herdrMinBase) Live, after the base and api ship: from a fresh laptop config with
      `default_multiplexer = "herdr"`, `repose run -p "say done"` shows the
      reply; `repose ps` lists it; `--worktree` groups under the
      repository in herdr; attach with and without a laptop herdr and
      from inside a laptop herdr pane; a temporary herdr machine is
      destroyed after its last pane closes; `herdr machine list --json`
      before and after `run` and `rm`.
- [ ] (open: needs the same release) S5 measurements (I-510, I-511): ssh-prepare api calls over 30
      minutes with a laptop herdr open on a stopped machine; bridges
      open to unselected machines after herdr's idle cleanup.
- [x] Dashboard captures of a herdr project at 1440 and 390, light and
      dark, at 1x.

## 4. Who owns which shared file

One owner per file; another branch that needs a change there asks the
owner (or the conductor) instead of editing it.

| File or set | Owner | Note |
|---|---|---|
| `internal/multiplexer/*` | `multiplexer-spec` (frozen) | names, units, socket, prefix, caps. Every branch imports it, none edits it; a needed change goes to `mux-api` and the others merge it |
| `proto/repose/*/v1/*.proto`, `internal/gen/**` | `mux-api` | comment-only; no field changes in this workstream |
| `internal/fakes/api/**`, `internal/fakes/hostd/**` | `mux-api` | `mux-cli` merges `mux-api` |
| `internal/fakes/guestd/**` | `mux-guestd` | |
| `internal/api/**`, `internal/db/**`, `internal/hostd/**`, `ops/dev/schema-fixture.sql` | `mux-api` | |
| `internal/guestd/**`, `cmd/repose-hook/**`, `cmd/guestd/**` | `mux-guestd` | |
| `nix/**`, `scripts/bump-agents.sh`, `scripts/bump-agents-pr.sh` | `mux-base` | |
| `internal/cli/**`, `cmd/repose/**`, `apps/web/**` | `mux-cli` | dashboard and public docs included |
| `docs/interfaces/api.md`, `grpc-hostd.md`, `db-schema.md` | `mux-api` | |
| `docs/interfaces/vsock-guestd.md` | `mux-guestd` | |
| `docs/interfaces/guest-conventions.md` | split by section | `mux-base`: the sections "Session units", "Agent wrappers" (up to the `repose-hook` mapping), "Environment", and in "herdr" the Package, Server, Workspace, Seeded config and Agent wrappers bullets. `mux-guestd`: the `repose-hook` mapping and "Messages and questions", "Memory pressure", "CPU weights", and in "herdr" the Socket use, Agent keys, Hooks, Environment and Memory and CPU bullets. `mux-cli`: "The checkout", "tmux", "Laptop config the CLI carries", and the Messages bullet in "herdr". Edit only your sections; git merges disjoint hunks |
| `docs/interfaces/cli-config.md` | `mux-cli` | |
| `docs/features/run-and-attach.md`, `status-and-logs.md`, `stop-start-destroy.md`, `projects.md` | `mux-cli` | |
| `docs/workstreams/README.md` | `multiplexer-spec` | the row for this file is in |
| `docs/features/agents.md` | `mux-base` | |
| `docs/features/notifications.md`, `secrets.md`, `docs/SECURITY.md` | `mux-guestd` | |
| `docs/DECISIONS.md` | by entry | each branch appends its evidence sentence ("Built: ...", test names) only to its own entries: I-501, I-508 `mux-base`; I-503, I-504, I-505, I-506 `mux-guestd`; I-502, I-507 `mux-api`; I-509, I-510, I-511 `mux-cli`. A changed decision is a new entry with an id from `ops/dev/release-queue id` |
| `docs/DECISIONS-INDEX.md` | every branch | regenerate with `python3 ops/dev/decisions-index.py`; on a merge conflict, regenerate |
| `docs/workstreams/STATUS.md` | every branch | append your own lines; never edit another's |
| `docs/workstreams/16-multiplexer.md` | every branch | tick only your own checklist |
| `docs/proposals/2026-10-05-session-backends.md` | nobody | frozen record |

## 5. Contracts between the branches

- **project.json** (`mux-api` writes it through the api, `mux-guestd`
  reads it, `mux-base`'s `repose-multiplexer-is` reads it): key
  `multiplexer`, `tmux` or `herdr`; absent or other means tmux
  (`multiplexer.Normalize`).
- **Session units** (`mux-base` defines, `mux-guestd` starts, `mux-cli`
  probes): `multiplexer.TmuxUnit`, `multiplexer.HerdrUnit`;
  guest-conventions "Session units".
- **Workspace step** (`mux-base` provides, `mux-cli` calls after sync):
  `repose-herdr-workspace`, no arguments, exit 0 when herdr is not
  running; guest-conventions "herdr", Workspace.
- **Agent names and workspace labels** (`mux-cli` makes them,
  `mux-guestd` turns them into keys): agents named `<agent>` and
  `<agent>-N`, the lowest free N from 2; workspaces labelled `checkout`
  for the machine's own checkout (I-597; its directory name before),
  another checkout's name, or for a worktree whatever `herdr worktree
  open` gives (it groups under the repository).
- **Hook windows** (`mux-guestd` on both ends): `herdr:<pane_id>`.
- **Warnings** (`mux-guestd` sends, `mux-api` accepts): `herdr_down`.
- **The gate** (`mux-api` answers, `mux-cli` handles): `409 conflict`,
  `detail.reason = base_update_needed`, `detail.needs`.

## 6. Limits on kanali

About 4 GB free disk and 7.8 GB RAM shared with other sessions. Run
`df -h /` before anything large and stop under 1.5 GB free. Go tests per
package with `env -u REPOSE_PROJECT -u REPOSE -u REPOSE_HOOK_AGENT go
test ./internal/<pkg>/...`, never `go test -race ./...` over the repo.
No full nix build of the guest system: `nix eval` and single small
derivations only. VM tests cannot boot here. herdr 0.9.3 is at
`/tmp/claude-1000/-home-dev-kanali/a2a4b627-1528-4a87-9846-536a60ceb6e3/scratchpad/bin/herdr`
and its source at `.../scratchpad/herdr`; copy what you need into your
own scratchpad, since that directory belongs to another session.

## 7. Not in this workstream

`events.subscribe` and herdr-side notices (proposal S6); a phone
notification for herdr's `blocked` on gemini and pi (open question 1);
a dashboard control to switch the multiplexer; renaming `tmux_window`
or `tmux_clients` on the wire; a CLI version header for the api.
