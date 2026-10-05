# Two session backends: tmux by default, herdr as the other (proposal, 2026-10-05)

**Status: proposal, not built.** Code anchors are checked against `main`
at `0bc05325`. herdr anchors are against its source at `c4653a4f` (v0.9.3
plus 42 commits) unless a release is named. Background:
`reports/Herdr integration avenues.md` and the live test on the temporary
machine `herdr-live` (section 6).

## 1. Decisions asked of the owner

Each line names the proposed answer first.

1. **Reopen I-482.** Ship herdr in the base, start it from a boot unit on
   projects that choose it, and let guestd read its socket. Both of
   I-482's premises failed the live test: any herdr from 0.9.0 accepts a
   remote of endpoint protocol generation 1, and the server resumes agents
   with no client attached. Its "revisit when" condition has been met.
2. **The name.** `multiplexer`, values `tmux` and `herdr`: the config key
   `default_multiplexer` (beside `default_agent`), the run flag
   `--multiplexer`, the api field, the column and the `project.json` key.
   The repository uses the word nowhere today, so one grep finds every
   use. "session" already means SSH sessions, the session helper and the
   tmux session.
3. **Choosing herdr for a project.** `repose run --multiplexer herdr` sets it
   and it sticks, like `--no-personal` (I-490). `default_multiplexer` in
   `config.toml` applies to new projects. Also proposed: a new project
   created from inside a herdr pane (`HERDR_ENV=1` in the CLI's
   environment) gets herdr, since tmux would land nested inside the
   user's herdr. Approve or drop the automatic part.
4. **A change takes effect at the next start.** The running multiplexer
   and its agents keep going until the machine stops. The alternative
   starts the second server at once and runs both until the stop; it
   doubles the states guestd and the CLI must handle for an hour of
   convenience.
5. **The laptop's herdr sidebar.** The CLI adds a herdr project's machine
   to the laptop's herdr catalog on `run` and `attach`, and removes it on
   `rm` and in a reconcile. repose owns only entries whose target is
   `<slug>.repose`, adopts entries the tutorial made, and never adds a
   temporary machine. The alternative puts registration behind a config
   key.
6. **`repose run` inside a laptop herdr pane.** Proposed: no nested
   client. The CLI prints `todo-app is in herdr's sidebar.`, then stays
   in the foreground as the session helper (forwards, bridge, carry)
   until Ctrl-C, the same lifetime it has beside a tmux attach. The
   alternative runs `ssh -t <slug>.repose herdr`, a client inside a
   client.
7. **CPU priority under herdr (I-494).** herdr's panes share the server's
   cgroup, so `CPUWeight=1000` on its unit would raise every build too.
   Proposed: guestd renices each thread of the herdr server to -5 beside
   its OOM write, closed by the I-494 load test. The alternatives are
   accepting the gap, or wrapping each pane shell in its own scope (which
   orphans panes when the server dies).
8. **Presence.** A laptop herdr app keeps an SSH bridge to every machine
   in its sidebar, and every SSH session counts as someone at the machine
   (`ssh_sessions > 0` in `idle.usedSQL`, `temp.Busy`,
   `busyUnattendedSQL`). Proposed: count it, as a tmux attach left open
   counts today, and measure in stage 5 whether herdr's idle bridge
   cleanup closes bridges to machines nobody has selected.

## 2. The user surface

### 2.1 Choosing herdr

```
$ repose run --help
...
      --multiplexer string   tmux|herdr: what runs this machine's terminals, from its next start (default: config.toml's default_multiplexer, else tmux)
```

```toml
# ~/.config/repose/config.toml
default_agent = "claude"
default_multiplexer = "herdr"
```

A new project, typed in a herdr pane on the laptop:

```
$ cd ~/src/todo-app && repose run
Creating todo-app (large, herdr)
Ready in 38s. todo-app is in herdr's sidebar.
```

Switching a running machine:

```
$ repose run --multiplexer herdr
todo-app uses herdr from its next start; its 2 tmux windows keep running until then.
```

The dashboard shows `herdr` beside the size on the project page,
read-only.

### 2.2 Commands on a herdr project

| Command | tmux project (unchanged) | herdr project |
|---|---|---|
| `repose run` (no prompt) | attach to tmux | In a laptop herdr pane: the sidebar line, then the helper until Ctrl-C. With `herdr` 0.9.0+ on the laptop PATH: `herdr --remote <slug>.repose --session default` as a child of the CLI. Otherwise `ssh -t <slug>.repose herdr` under the input proxy and reattacher |
| `repose run "prompt"` | new window, `send-keys` | the checkout's workspace (created when missing), a new tab, `herdr agent start claude --kind claude --pane P`, `herdr agent prompt claude "..."`, then the attach rule |
| `repose run --worktree` | worktree plus window | the same git worktree, then `herdr worktree open --path DIR` so herdr groups it under the repository, then the agent in it |
| `repose attach [P[:CHECKOUT]]` | tmux attach | the attach rule; `:CHECKOUT` focuses or creates that checkout's workspace first |
| `repose ps` | `tmux list-windows` | `herdr agent list --json` plus `workspace list --json`: WORKSPACE, AGENT, NAME, STATE. No cwd, no title |
| `repose paste` | `paste-buffer -p` | `herdr pane send-text` into the focused pane |
| `repose status` | `sessions N   tmux clients N   docker N` | `sessions N   docker N`, and `herdr` in the header |
| `repose ls` AGENTS, dashboard | tmux agents | herdr agents, with herdr's states, gone when the pane closes |
| `repose stop` | `Stopped todo-app in 6s. Resume Claude in its window with claude --resume.` when an agent was running (the hint `stop-start-destroy.md:80-82` specifies and `lifecycle.go:78-132` never prints) | `Stopped todo-app in 6s. herdr resumes claude when it starts.` |
| `repose rm` | | also removes the catalog entry repose owns |
| temporary machine | destroyed when tmux reports the session ended (I-352) | destroyed when the attach returns and herdr reports no panes; else at expiry |

Things a herdr user stops typing, all of which `tutorial-herdr.md` asks
for today: `herdr machine add`, the install-on-machine answer,
`herdr integration install` inside the machine, a `cd` into the checkout,
and a prefix rebind for tmux inside herdr.

## 3. Design by component

### 3.1 Guest (Nix)

**Package.** `nix/overlay/agents/herdr.nix` fetches upstream's static-pie
x86-64 release (the v0.9.3 binary is static-pie, so no autoPatchelf),
pinned in `versions.json` and moved by `scripts/bump-agents.sh`, the
`claude-code.nix` pattern under R3-19. The install check runs
`herdr status client --json` and fails the build unless
`endpoint_protocol_generation == 1` and `protocol >= 22`, so a bad bump
fails before any guest gets it (the I-487 lesson). The package goes in
`environment.systemPackages`, which makes
`/run/current-system/sw/bin/herdr` resolve; herdr's remote discovery
searches that path (`src/remote/attach.rs:1677-1725`). The CLI's
`baseCommands` (`internal/cli/carry_tools.go`) gains `herdr`. tmux stays
in the base on every project.

**Units.** A new `nix/guest/base/herdr.nix`, imported beside `tmux.nix`
at `nix/guest/base/default.nix:17`:

- `repose-herdr-server.service` (user unit): `Type=simple`,
  `ExecStart=bash -lc 'exec herdr server'` so the server and every pane
  inherit the login PATH (the I-227 analogue of `tmux.nix:38-46`),
  `EnvironmentFile=-/etc/repose/env` for TZ, `KillMode=control-group`,
  `Restart=on-failure` (a herdr restart resumes agents,
  `src/app/agent_resume.rs`), `restartIfChanged=false` (I-496),
  `TasksMax=infinity` (upstream #3324), no `CPUWeight`.
- Both session units gain `ExecCondition=repose-multiplexer-is <name>`,
  a script that runs `jq -e '(.multiplexer // "tmux") == $m'` on
  `~/.repose/project.json` and refuses when the other unit is active. A
  `project.json` without the key reads as tmux, so every existing guest
  behaves as today.
- The boot trigger moves to guestd. Today the path unit
  (`tmux.nix:144-151`) starts tmux as soon as `project.json` exists,
  which at boot is the previous boot's file. After a switch made while
  stopped that would start the old multiplexer before SetupProject
  writes the new file. guestd already starts the unit right after writing
  `project.json` (`internal/guestd/project/project.go:136`, `:397`), so
  the path unit is dropped and `ensureTmux` becomes `ensureSession`,
  which starts the unit `project.json` names.
- `ExecStartPost=repose-herdr-workspace`: when `herdr workspace list
  --json` has no workspace labelled with the checkout's name, it runs
  `herdr workspace create --cwd "$(repose-checkout)" --label <checkout>
  --no-focus`, the herdr counterpart of the `shell` window. It also runs
  after the first sync, in place of `sync.go`'s `respawn-pane`. On a
  resume, `session.json` already holds the workspace.
- `nix/flake.nix`'s `guest-session-survives-switch` check also asserts
  `X-RestartIfChanged=false` on the herdr unit.

**Seeded config.** `~/.config/herdr/config.toml`, written only when
absent: `[terminal] shell_mode = "login"` (herdr's Linux default is
non-login, `src/pane.rs:1882-1890`, and tmux starts login shells) and
`[update] version_check = false` (the live test's 0.9.0 logged "0.9.3
available"). `herdr update` inside a guest would install to
`~/.local/bin`, and `update --handoff` under systemd can leave the unit's
main pid (upstream #4972); the docs name both.

**Wrappers and integrations.**

- `nix/overlay/agents/wrap.nix:25` also exports `HERDR_AGENT=${bin}`.
  herdr's `parse_agent_label` accepts the five names, so detection stops
  depending on argv0 (#803).
- `repose-agent-setup` (`nix/overlay/agents/agent-setup.nix`) runs
  `herdr integration install <agent>` for claude, codex, opencode and pi,
  best effort, when `HERDR_ENV=1` and `herdr integration status` is not
  current. The guest's pinned version wins over a hook carried from the
  laptop (the live test saw v10 replaced by v9, and resume still worked).

**Environment.** herdr has no `set-environment`. The
`systemctl --user set-environment` half of the post-switch push
(`nix/guest/base/env.nix:96-128`) reaches the server at its next start.
Wrapped agents source `/etc/profile.d/repose.sh` (I-241) and every bash
command reads `BASH_ENV` (I-475), so agents and shell commands get
current secrets, TZ and PATH. A non-bash program started from an old pane
keeps the server's start environment; `secrets.md` and guest-conventions
record this.

### 3.2 guestd

**One agent-source seam.** All of discovery lives in
`Watcher.refreshTmux` (`internal/guestd/sample/watcher.go:206-326`),
which iterates `tmuxWindow` values. It becomes `refreshPanes`, fed by a
`Source` interface in a new `internal/guestd/sample/source.go`:

```go
// Pane is one agent terminal as a multiplexer reports it, cut down to
// what guestd may keep (R5-3): no cwd, no title, no argv.
type Pane struct {
	Key      string // "<window>" for tmux; "<name>" or "<agent> <pane_id>" for herdr
	Agent    string // one of the five; others are dropped by the source
	RootPID  int    // tmux pane pid; 0 when the source reports the state
	Command  string // tmux foreground comm
	Activity int64  // tmux window_activity
	Reported string // working|idle|needs_input|unknown from herdr; "" means compute from CPU
	Done     bool   // herdr saw a turn end since the last read
}

type Source interface {
	Name() string // "tmux" | "herdr"
	Panes(ctx context.Context) (panes []Pane, up bool, err error)
	Resolve(ctx context.Context, ref string) (key string, ok bool)
	Close()
}
```

- `tmuxSource` is today's `tmuxClient` (`sample/tmux.go:118-240`)
  behind the interface. It first stats `/tmp/tmux-1000/default`, so a
  herdr machine pays no fork.
- **Both sources run on every machine** and the watcher takes the union.
  Each costs nothing while its server is absent. The union covers a
  machine whose setting changed while it runs (decision 4) and anyone who
  starts the other multiplexer by hand, as tutorial users do today.
  tmux keys are window names; herdr keys never collide with them because
  herdr pane ids carry a colon (`w2:p1`).
- In `refreshPanes`, the CPU cross-check, `treeHasAnyComm` and
  `heuristicCompletion` run only when `RootPID > 0`. `computeState`
  (`watcher.go:328`) returns `Reported` when set; a `needs_input` hook
  still wins.
- `tmux_down` (`watcher.go:231`) fires only when `project.json` says
  tmux. A new kind `herdr_down` fires, through `oneShot`, when it says
  herdr and the socket has refused for two refreshes.

**herdrSource.**

- One persistent connection to `/home/dev/.config/herdr/herdr.sock`
  (`src/session.rs:171-173`). No fork, so I-31 holds. After `connect`,
  guestd reads `SO_PEERCRED` and refuses a peer whose uid is not dev's:
  guestd is root and the path is under dev's control.
- At connect, `ping`, whose answer carries `version` and `protocol`
  (`src/api/server.rs:528-535`). Below protocol 22 the source reports
  down with that reason.
- Every 5 s, one `agent.list`. Each refresh is a full reconcile, so a
  guestd restart, a herdr restart and a lost event all take the
  cold-start path. `AgentInfo` (`src/api/schema/agents.rs:187-215`)
  carries no cwd; the snapshot's `PaneInfo` does, so guestd never calls
  `session.snapshot`, and never calls `pane.process_info`, whose answer
  carries `argv`, `cmdline` and `cwd` (`src/api/schema/panes.rs:494-518`).
  The decoder struct holds only `pane_id`, `workspace_id`, `name`,
  `agent`, `agent_status` and `state_change_seq`; frames are size-capped
  and every other field is dropped by `encoding/json`.
- `state_change_seq` (present since v0.9.0) closes the polling gap: a
  pane whose sequence moved and whose status reads `done` or `idle` has
  finished a turn, even one shorter than 5 s.
- State map: `working` to working, `blocked` to needs_input, `idle` and
  `done` to idle, `unknown` to unknown. A finished turn on a hookless
  agent (gemini, pi: `HookedAgents`, `sample/tmux.go:59`) emits
  `KindCompleted "<agent> went idle"`, the text the 90 s heuristic uses
  today. Hooked agents keep their hook events, so nothing arrives twice.
- Key: the herdr agent name when it has one (repose names its agents
  `claude`, `claude-2`; herdr allows `[a-z][a-z0-9_-]{0,31}`), else
  `<agent> <pane_id>`. A workspace whose label is one of the machine's
  checkout names prefixes the key with `<checkout>/`, as I-480 does for
  windows. Keys fit hostd's 64-byte `capWindow`
  (`internal/hostd/guest/guestinput.go:29`).
- A socket EOF keeps pane state for two refreshes before the panes are
  announced `unknown` and forgotten, so a herdr restart that resumes its
  agents shows no flap.
- Stage 6 adds `events.subscribe` (`pane.created`, `pane.closed`,
  `pane.agent_detected`, and a per-pane `pane.agent_status_changed`,
  which needs a `pane_id`, `src/api/schema/events.rs:75-80`) to wake the
  watcher early. The live test showed `pane.updated` never carries
  working to done.

**OOM (I-200).** `applyOOM` protects `comm == "tmux: server"` and the
pids `agentPIDs` finds from tmux pane roots (`sample/oom.go:30-110`).
It gains the herdr server (the process in the herdr unit whose parent is
dev's `systemd --user`), and `agentPIDs` walks the herdr server's tree
for all five agents' binaries, which needs no pane pid. The live test
found claude at `oom_score_adj=200` under herdr; applyOOM leaves a
positive value alone unless the pid is on its list.

**CPU.** Beside the OOM write, guestd sets nice -5 on every thread in
`/proc/<server>/task` (nice is per thread on Linux). The SSH
`session-.scope` weight (`tmux.nix:136-142`) still covers the laptop
bridge. Subject to decision 7.

**Hook attribution, bug 2.** Today a hook without `$TMUX_PANE` falls
back to the agent name (`internal/guestd/hooks/hooks.go:186-193`) and
`RecordHook` marks any tmux window of that name (`watcher.go:383-397`).

- `cmd/repose-hook` sends `window = herdr:$HERDR_PANE_ID` when
  `REPOSE_AGENT_WINDOW` is empty and `HERDR_ENV=1`. repose-hook reads its
  own environment, so guestd reads no new variable and the single
  exception at `docs/SECURITY.md:240` stays as written.
- guestd resolves a `herdr:` window through `herdrSource.Resolve` to the
  pane's key. A window that resolves nowhere is still relayed with the
  agent name for display, and `RecordHook` is skipped for it.
  `repose-notify` and `repose-ask` take the same path.

**Other.** `secrets.go:443` `pushTmux` runs only when the tmux socket
exists. The `Info` struct in `project.go` gains `Multiplexer`; the bytes
of `project_json` already pass through unchanged (`project.go:160-175`).

### 3.3 CLI

**One seam.** A `multiplexer` interface in `internal/cli/mux.go`, with
`mux_tmux.go` (today's `tmux.go` plus the attach, ps, paste and temp
code moved behind it) and `mux_herdr.go`:

```go
type multiplexer interface {
	Name() string
	Names(ctx context.Context, t sshTarget, slug string) ([]string, error)
	StartAgent(ctx context.Context, t sshTarget, s agentStart) (key string, err error)
	Attach(t sshTarget, a attachReq) error
	List(ctx context.Context, t sshTarget, slug string) ([]PsWindow, error)
	Paste(ctx context.Context, t sshTarget, slug, target, guestPath string) error
	MessageScript(slug, text string) string
	SessionEnded(ctx context.Context, t sshTarget, slug string) (bool, error)
}
```

**Picking the backend.** The CLI asks the guest over the ssh master it already
holds: `systemctl --user -q is-active repose-herdr-server` (a few
milliseconds). The running unit decides the attach, since the setting can
differ until the next start. `Project.multiplexer` from the api decides
create and the "from its next start" line. The fast path
(`fastpath.go:261`) skips the api, and the probe covers it too.

**Call sites.**

| Today | Becomes |
|---|---|
| `run.go:383-450` prompt path (`windowNameFor`, `othersOpen`, `startAgentWindow`, `agentDialogError`) | `mux.Names`, `mux.StartAgent`; messages say "tab" on herdr |
| `attachTmux` at `run.go:256`, `run.go:467`, `fastpath.go:261` | `mux.Attach` |
| `ps.go:35` | `mux.List` |
| `paste.go` | `mux.Paste` |
| `temp.go:180` `tempSessionEnded` | `mux.SessionEnded` |
| `tmuxMessage` (`session.go:192`), `bridge.go`, `bridgecmd.go`, `forward.go`, `carry_claude.go`, `inputproxy.go` | `mux.MessageScript`; on herdr `herdr notification show`, which nobody sees when no client is attached (I-313 holds) |
| `worktreeProbeScript` (`tmux.go:323-325`) | its leading `tmux list-windows` moves into `mux.Names`, so `--worktree` works on herdr |
| `carry.go` TZ push, `sync.go` `freshShellScript` | tmux only; herdr gets TZ from `/etc/repose/env` and the workspace step after sync |
| `status.go:145` | no `tmux clients` on herdr |
| `lifecycle.go:78-132` | the stop hint on both, built from `Signals.Agents` read before the stop |

**StartAgent on herdr**, one ssh script:

1. `claudeTrustScript` (I-486).
2. Find or create the workspace for the checkout or worktree.
3. Read `herdr agent list --json` to pick `claude` or `claude-N` and to
   count same-kind agents in that workspace, which keeps the "two agents
   share one working tree" warning (`run.go:418`).
4. `herdr tab create --workspace W --cwd DIR --label NAME`.
5. `herdr agent start NAME --kind A --pane P --timeout 300000`. The
   300 s cap (`AgentStartParams`, `agents.rs:173-175`) is shorter than a
   first dev-shell build (`devShellLoadTimeout`, 30 min), so on
   `timeout` the script keeps calling `herdr agent wait NAME --until idle
   --until blocked` until that limit.
6. A pane that settles on `blocked` gets no prompt, and the CLI answers
   with I-486's dialog error naming the tab.
7. `herdr agent prompt NAME "<prompt>" --wait --until working --until
   blocked --timeout 10000`. The prompt sits in argv inside the guest,
   as it does with `tmux send-keys -l` today (`tmux.go:144`).

**Attach on herdr.** The laptop client runs as a **child** of the CLI,
never through `syscall.Exec`, so the session helper (forwards, bridge,
carry) lives as long as the client does. herdr's bridge reconnects with
backoff (`supervisor.rs:12, 211-217`), so the input proxy and
reattacher are skipped on that path and kept on the `ssh -t herdr` path.

**Laptop catalog.** `syncHerdrMachines` runs where the CLI already
holds the project list after writing the ssh files (`cert.go:271`,
`:394`), when `herdr` is on the laptop PATH and `~/.ssh/config` includes
repose's file:

1. `herdr machine list --json`.
2. For each running herdr project with no entry: `herdr machine add
   <slug>.repose --label <slug> --remote-session default </dev/null`,
   in a goroutine so it adds nothing to `Ready in` (live: exit 0 in
   1.1 s, no prompt).
3. For each `*.repose` entry whose slug is no longer a project:
   `herdr machine remove <id>`, which covers destroys from the dashboard,
   another laptop and expiry.
4. An entry the user disabled stays disabled.

`RunCmd` after `connect()` (`run.go:210`) and `DestroyCmd`
(`lifecycle.go:188`) call it too. A stopped machine's entry stays, and
herdr shows the gateway's "stopped" banner; the gateway never wakes a
stopped machine (`ssh-gateway.md`, "It does not start a stopped
machine"). Each bridge retry runs the `Match exec` ssh-prepare, so stage
5 measures its api calls at herdr's 120 s backoff.

**Messages.** A `notify(slug, text)` helper picks `herdr notification
show` or `tmux display-message`.

**Bug 1.** `internal/cli/claude_merge.jq:81` merges with `$G * $L`,
which replaces arrays, so a laptop `SessionStart` hook drops herdr's
entry and Claude stops resuming. The merge concatenates each
`hooks.<event>` array, de-duplicated by command, as it already does for
`$p`.

### 3.4 api and dashboard

- Migration 0016: `projects.multiplexer text not null default 'tmux'
  check (multiplexer in ('tmux','herdr'))`, registered, with
  `db-schema.md`.
- `internal/api/http/projects.go`: JSON (`:111`), POST (`:213`, `:287`)
  and PATCH (`:356-409`, beside `agent_default`). An unknown value is
  `400 invalid`. Fork copies the source's value; restore keeps the
  project's.
- **Base gate.** POST or PATCH to `herdr` on a project whose
  `base_version` predates the first herdr base answers `409 conflict`
  with `detail.reason = base_update_needed`. Without it, a held base
  (`hold_base_updates`) would start tmux and the user would wonder why.
- `internal/api/ops/phases.go:264` adds `"multiplexer"` to the
  `project_json` map.
- `idle.usedSQL` (`idle.go:71`), `temp.Busy` (`temp.go:81-93`) and
  `busyUnattendedSQL` (`abuse.go:255`) need no change: herdr agents
  fill `agents`, and every herdr client arrives over SSH and counts in
  `ssh_sessions`.
- Dashboard: `apps/web/src/lib/api/types.ts` gains `multiplexer`; the
  project page shows `herdr` beside the size and hides the tmux clients
  row on herdr projects. Agents stay keyed by `window`.

### 3.5 Notifications

The path stays `repose-hook` to guestd to hostd to the api; the live test
saw `claude completed` from a herdr pane reach `repose status`.
gemini and pi get completions from herdr's detection in place of the
90 s heuristic. herdr's `blocked` sets the state `needs_input` for them;
a phone notification for it is open question 1. `notifications.md`
says "no attached client" where it says "no tmux client".

## 4. Contract changes and the one-release compatibility

| Contract | Change | Old shape |
|---|---|---|
| `project.json` (guest-conventions) | adds `multiplexer` | absent means tmux in guestd and both units |
| `SetupProject` (vsock-guestd.md) | "starts the session unit `project.json` names" | tmux projects behave as today |
| api `Project` (api.md:60-70), POST/PATCH `/projects` | adds `multiplexer`; `409 base_update_needed` | absent means tmux; old CLIs ignore the key |
| `db-schema.md` | `projects.multiplexer` | default `tmux` |
| Warning kinds (`guestinput.go:52-55`, `internal/api/events/events.go:76`, vsock-guestd.md) | adds `herdr_down` | hostd and api ship first; an older hostd turns an unknown kind into `guest_other` (`guestinput.go:172-173`) |
| hooks.sock `window` (guest-conventions) | may be `herdr:<pane_id>` | tmux path unchanged; `$TMUX_PANE` read stays |
| `AgentProc`, `AgentEvent`, `Question` `tmux_window` (guestd.proto:97-108, hostd.proto:253-308) | the text widens to "tmux window or herdr agent key, at most 64 bytes"; no rename | wire unchanged; the api JSON already says `window` |
| `GuestSignals.tmux_clients` | stays tmux-only, 0 on herdr | none needed |
| `cli-config.md` | `default_multiplexer` | absent means tmux |
| guest-conventions "Agent wrappers" | `HERDR_AGENT` | additive |

No proto field is renamed and no stored protojson changes
(`internal/api/ops/engine.go:489-502` and `internal/hostdev/statefile.go:148`
marshal with field names). A neutral rename of `tmux_window` can follow
on its own, with the usual dual-accept.

**Old CLI on a herdr machine.** A CLI before stage 4 runs `exec tmux
attach` and gets tmux's "no server running". The CLI sends no version
the api could gate on (`User-Agent: repose-cli`), so the docs and the
release notes say herdr needs CLI v0.1.N.

## 5. Staging, with the evidence that closes each stage

Each stage ships alone. Evidence is pasted in the commit or PR, never
"tests pass".

**S0. The three bugs** (CLI, repose-hook, guestd; no contract change).
- jq test: a laptop `SessionStart` hook plus the guest's herdr entry
  leave both entries.
- guestd unit test: a hook with no resolvable window leaves a tmux
  window named `claude` at idle.
- `repose stop` output on a temporary machine with claude running,
  showing the hint.

**S1. api and allowlists** (api, hostd; deploys before any base).
- Migration 0016 listed in the registry, applied, down and up in
  `db_test.go`.
- `curl` PATCH `{multiplexer:"herdr"}` then GET shows it; `"screen"`
  answers 400; a held-base project answers `409 base_update_needed`.
- `herdr_down` passes hostd's `cleanWarning` unchanged (unit test).
- The guest's `project.json` shows the key after a start.

**S2. Base, dormant** (base). Package with the protocol check, seeded
config, `HERDR_AGENT`, integration install, both units with
`ExecCondition`, `ensureSession`, path unit removed, workspace step,
flake check.
- Build log with the install check passing; the same check failing
  against a fake binary reporting generation 2.
- `nix flake check` output with the extended
  `guest-session-survives-switch`; the check failing with the herdr
  line removed.
- On a temporary machine with `project.json` hand-edited to herdr and
  restarted: `systemctl --user is-active repose-herdr-server` is active,
  `pgrep -c tmux` is 0, the workspace sits in the checkout, a wrapped
  agent's environment has `REPOSE_PROJECT`, a secret, the login PATH and
  `HERDR_AGENT`.
- Stop and start resume claude headless (process tree pasted).
- A tmux project on the same base: the existing VM subtests unchanged.

**S3. guestd herdr source** (base).
- Tests against a fake herdr socket replaying the live test's sequence:
  working then done gives AgentState working then idle and one
  `completed` for gemini; a sequence jump across one poll also gives
  one `completed`; a wrong `SO_PEERCRED` uid is refused; an EOF shorter
  than 10 s emits no `unknown`.
- `TestStraceNeverOpensCmdlineOrEnviron` still green; `journalctl -u
  guestd | grep -c cwd` is 0 on the live machine.
- Live: `repose ls` AGENTS shows `claude working` during a prompt and
  `idle` after; `/proc/<claude>/oom_score_adj` is -800; every herdr
  server thread has nice -5; a herdr pane's `needs_input` leaves a tmux
  window named `claude` idle.
- I-494 load test: keystroke echo latency with `stress-ng` on every
  core, before and after the renice.

**S4. CLI and docs: the user-visible release** (CLI, web, api gate
constant set to S2's base).
- From a fresh laptop config with `default_multiplexer = "herdr"`:
  `repose run "say done"` shows the reply; `repose ps` lists it;
  `--worktree` groups under the repository in herdr.
- Attach with and without a laptop herdr; from inside a laptop herdr
  pane.
- A temporary herdr machine is destroyed after its last pane closes.
- `go test ./internal/cli -run Docs` passes with `cli.md`; the public
  docs and `tutorial-herdr.md` rewrite are in the same commit;
  `docs/CHECKLIST.md` greps run.
- Dashboard captures at 1440 and 390, light and dark.

**S5. Laptop catalog** (CLI).
- `herdr machine list --json` before and after `run` and `rm`; a
  temporary machine never added; a hand-made entry for another host
  survives `rm`; a disabled entry stays disabled.
- Count of ssh-prepare api calls over 30 minutes with a laptop herdr
  open on a stopped machine.
- The bridge count to unselected machines after herdr's idle cleanup (decision 8).

**S6. Events and herdr-side notices** (base).
- A working to done transition reported within 1 s (timestamps
  pasted).
- A `repose-ask` question and the idle notice showing as herdr
  notifications.

## 6. The live test: proved and untested

Proved on `herdr-live` (production, host-01, CLI from `0bc05325`, laptop
herdr 0.9.3 in an isolated HOME):

- A guest herdr 0.9.0 (`protocol 22`, generation 1) with a 0.9.3 laptop:
  `machine add` finished with exit 0 in 1.1 s and no install prompt;
  `machine status` reported reachable; `--remote` attached.
- After `repose stop` and `repose start`, a user unit ran `herdr server`,
  restored two workspaces and ran `claude --resume <id>` before any
  client connected. Claude remembered the previous turn.
- Agent states read from the socket. `pane.updated` alone never showed
  working to done; a per-pane `pane.agent_status_changed` subscription
  did.
- repose's own hook from a herdr pane reached guestd and `repose status`.
- Gaps: AGENTS stayed `-`, `repose ps` listed only the shell window,
  claude ran at `oom_score_adj=200`, no stop hint printed, and the
  guest's integration install replaced the laptop's v10 Claude hook with
  v9.

Untested:

- The units, the `ExecCondition` gate and `ensureSession` on a real
  guest; the VM tests cannot boot on kanali.
- `agent.list` polling with `state_change_seq` from a Go client; the
  probe used Python and events.
- `agent.start` on a pane whose login shell is still loading a flake dev
  shell, and the `agent wait` continuation past 300 s.
- The renice against I-494's load test.
- The number of bridges a laptop herdr holds open, and for how long.
- `herdr worktree open` against a worktree repose made.
- A laptop herdr older than 0.9.3 against the pinned guest release.

## 7. Rejected alternatives

- Packaging herdr from the pinned nixpkgs (0.9.0): three releases old,
  and an override costs a Rust and zig build per bump.
- Events only, no polling: a missed subscription or `events_lost` leaves
  wrong state until something else changes.
- `pane.process_info` for pane pids: its answer carries argv, cmdline and
  cwd into guestd (R5-3).
- `session.snapshot` per refresh: it carries each pane's cwd and titles.
- A guest-side binary between the CLI and both multiplexers: it
  reimplements the tmux path byte for byte for no user gain; the pin,
  the build check and the base gate cover herdr's flag churn.
- A root-owned marker file on tmpfs to pick the unit: a second source of
  truth beside `project.json`, and a boot race with the path unit.
- Renaming `tmux_window` and `tmux_clients` on the wire now: the api JSON
  already says `window`, and `ssh_sessions` already counts herdr clients.
- A `clients` signal counting herdr client processes: every herdr client
  arrives over SSH and is already in `ssh_sessions`.
- `CPUWeight=1000` on the herdr unit: its panes share the cgroup, so
  builds would get it too.
- Running both servers after a live switch: two sets of panes, two
  resume paths, for the hours until the next stop.
- A repose herdr plugin as the integration: it reaches the laptop side
  only; guestd, OOM and the agents list still need this work.

## 8. Open questions

1. Should herdr's `blocked` send a phone notification for gemini and pi,
   or only set the state? It is screen-detected, so false alarms are
   possible.
2. Integrations as user-file installs (proposed), or a Claude
   managed-settings `SessionStart` hook that no settings rewrite can
   remove?
3. Pin policy: weekly bumps through `bump-agents.sh` (proposed), with a
   CI job that runs the source's decoder and the CLI's herdr scripts
   against the new release before the pin moves.
4. Temporary machines on herdr: session end at "no panes" (proposed), or
   tmux only?
5. The dashboard: read-only `herdr` label (proposed), or a switch beside
   the size through the same PATCH?
6. Old CLIs: is a docs and release-notes line enough, or should the CLI
   start sending its version so the api can refuse herdr projects to old
   ones?

## 9. DECISIONS entries this needs

Ids reserved with `ops/dev/release-queue id` when built.

1. herdr is a supported multiplexer: in the base at a pinned upstream
   release checked for generation 1, started by a boot unit on projects
   that choose it (supersedes I-482).
2. One `multiplexer` per project, default tmux, set by `--multiplexer`
   and `default_multiplexer`, applied at the next start, refused on a
   base without herdr.
3. guestd starts the session unit after writing `project.json`; the path
   unit goes.
4. guestd reads herdr's socket on every machine by polling `agent.list`,
   checks the peer uid, and decodes only ids, agent, status and sequence
   (amends I-31, I-49).
5. herdr's server and its agents get the I-200 protection and nice -5
   (amends I-200, I-494).
6. A hook's window may be `herdr:<pane_id>`, sent by repose-hook; an
   unresolved window never changes state (amends I-244).
7. `herdr_down` joins the guest warning kinds (amends I-29).
8. On herdr, secrets, TZ and PATH reach panes through profile.d,
   `BASH_ENV` and login shells only (amends I-475, I-476, I-488).
9. Under herdr, `run "prompt"`, attach, `ps`, `paste` and the temporary
   session end go through herdr (amends R3-14 with R4-10, I-253, I-274,
   I-280, I-304, I-352, I-469, I-480).
10. The CLI owns herdr catalog entries targeting `*.repose`.
11. The settings carry concatenates hook arrays per event (bug 1); the
    stop hint is printed (bug 3).
