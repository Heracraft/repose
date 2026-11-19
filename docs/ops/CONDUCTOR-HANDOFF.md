# Conductor handoff

Read this first, then `docs/ops/RELEASE.md` and `docs/ops/ORCHESTRATION.md`.
Written by the conductor of 2026-11-11 to 2026-11-18 (session `conductor`
on woker-1). It says what is true now, what the owner has ruled, what the
commands are that worked, and what is owed. Keep it current: the next
conductor rewrites "Where things stand" and appends to "Lessons".

## Who you are

One long-lived Claude session in the main checkout (`~/projects/factory`,
branch `main`), named `conductor` (`/rename conductor` on start; other
sessions message that name). You never build a workstream yourself. You
cut the queue, verify each batch, move `main` on this box, and ship when
the owner says so. Workers are Agent-tool subagents on Opus in their own
worktrees, or Claude sessions in tmux windows briefed by a file.

The owner's word is the only gate (bypass mode, 2026-11-11): a release,
a host switch and an edge switch each happen when the owner says so,
in words, for that one. "Integrate" needs no word. "Switch whatever" on
2026-11-18 covered both switches of that release and no later one.

## Where things stand (2026-11-19, 05:00 UTC)

- Live: CLI v0.1.49, main e57d63da pushed (two pushes on 2026-11-19, no
  switch and no tag, owner's word), base 2026.11.18.1, host-02 and the
  edge at 159231b. CI on origin/main is green (44444722781).
- Local main is 12 commits ahead (r20261119-2: say-agent-pick I-1356,
  dash-create I-1354, doors I-1360, the conductor-machine proposal,
  rel-record-8), ships as base,web. `origin/integrated` holds it. The
  pushed r20261119-1 carries bump-devtools-path, which reaches base,
  host-02 and the edge and is published and switched nowhere yet. So
  the next release needs: a push (api and web), a base publish (say,
  bump) and the owner's word for the host-02 and edge switches.
- The queue is empty. Every PR from the night is closed as merged; the
  25 stale agent-bump and dependabot PRs were closed on the owner's word.
- Worktrees: 152 removed on the owner's word on 2026-11-19 (branches
  kept). What is left under `~` is this conductor's: `kanali-rel-record-8`,
  `kanali-domain-dash-session`, `kanali-conductor-machine`,
  `kanali-conductor-handoff-3`, and the owner's `kanali-doors`.
- Live sessions on this box (`ListAgents`): the owner's own `factory-f0`
  (made doors) and `factory-3f` (a bring-your-own-hardware assessment,
  nothing queued). The ten sessions of the previous week were killed on
  the owner's word after each reported nothing in flight.
- Machines that are not yours: everything in `repose ls` except `e2e-*`
  you made. `tmp-wyyd` is the owner's.
- The dashboard parity order (I-1266): project create shipped as
  dash-create; next are resize and logs, then the change-pass gaps the
  decision names (`agent_default`, `multiplexer`).

## The owner's rules this week

- A feature is not done until the dashboard can create, change, run and
  remove it, not only show it (2026-11-17, now a CHECKLIST item and
  I-1266). The audit of what the dashboard still lacks is in the
  conductor's scratchpad; the next items in the owner's order are
  project create from the dashboard, then resize and logs.
- `repose say`'s mechanism and its UI are ugly (2026-11-18); redesign it
  as a conversation, do not polish the field.
- Release titles are the bare tag, `v0.1.N` (2026-11-17).
- Temporary and tiny machines install nothing in the background
  (2026-11-18, shipped as carry-lite).
- Secrets come as a 0600 file under `~/.config/repose-ops/` written with
  `read -rs`, never pasted in chat.

## The commands that worked

- Box prep before a batch: `df -h /` under 85 %, `go clean -cache`,
  `docker image prune -af`, `docker volume rm $(docker volume ls -qf
  dangling=true)` (the throwaway Postgres leaves one each time; use
  `docker rm -f -v`).
- A batch: `ops/dev/release-queue cut`; in the batch worktree, the Go set
  in a private tmux (`tmux -L relcheck new-session -d "..."`), with
  `CGO_ENABLED=1`, `DATABASE_URL` on a `--network host` Postgres, and
  `TMPDIR=/tmp/rt` set inside `nix develop`; the web set to one log file
  per check with its own exit line (never `| tail` before reading the
  status); `nix build` of guest-system, closure-size and the packages
  the batch reaches; the VM checks the batch touches, one at a time,
  after the test runs; host and edge toplevel evals, then a build on the
  target and `dry-activate`. Read every result before
  `git merge --ff-only release/<batch>` and `release-queue done <batch>`.
- A release: switch host-02 first when the batch adds things hostd must
  know (`RUNBOOK` "Switch a host to main": build the derivation on the
  host, `nix-env --set`, `systemd-run --unit=repose-switch`), then push
  `main`, watch CI, publish the base (`docker exec <api container>
  repose-admin base publish --rev <sha>` on the control VM), switch the
  edge (`nixos-rebuild switch --flake ./nix#edge --target-host
  root@20.102.98.254` with `NIX_SSHOPTS="-p 2222"`), tag `v0.1.N` on the
  pushed commit, `gh release edit v0.1.N --title "v0.1.N" --notes-file`
  before the sign job, then the live checks, `release-queue live
  <branch> --ran|--not-done`, and a `rel-record-N` branch from
  `release-queue record`.
- A signed-in dashboard for live checks: `ops/dev/dashboard-session`
  (as the owner, an hour, see `DEV-BOX.md`). Then playwright scripts
  with `storageState: 'tests-live/.auth/state.json'`.
- A worker: Agent tool, `model: "opus"`, `isolation: "worktree"`, a brief
  that names the files, the evidence, the checks to run and how to run
  them here, and "report to conductor". For work that must outlive your
  context, a tmux window with a brief file (`ORCHESTRATION.md`).

## Lessons

- A both-sides merge of code is not resolved until it builds and lints;
  a helper left unclosed hid behind 67 svelte-check errors (2026-11-17).
- A check's exit piped through `tail` is tail's exit; two reds shipped to
  a cut that way (2026-11-15).
- The queue's host comparison looked at retired host-01 only and dropped
  a host-02-only deploy silently (fixed, I-1336); the path analysis and
  the closure comparison still disagree about what reaches hostd and the
  gateway, open.
- Red VM checks that are not the batch's: `guest-base`'s I-264 probe
  under load, `guest-keystroke-path` on single-user nix (CI's gate),
  `guest-tools-carry`'s I-417 subtest (fixed 2026-11-18). Reproduce on
  main before calling a red a flake; rerun alone before calling it load.
- A host system built with `--no-link` hours before a switch is
  garbage-collected by the time you switch; build right before.
- CI runs a branch's tests the worker never ran under `-race`; two data
  races and a wake retry gap reached main that way and were fixed as
  their own branches.
- Burst's cap ratchets up while a guest is hungry and comes down only at
  a shut gate; `cpu.max` at the ceiling on a quiet guest is the design.
- A batch that "ships none" still gets the Go set: r20261118-10 was cut
  without it and the domain test was red on main for a night
  (2026-11-19, fixed as domain-dash-session).
- A batch worktree needs its own `pnpm install --frozen-lockfile` (under
  two seconds from the store). Symlinking the main checkout's
  `node_modules` gave three reds that were the checkout's stale modules,
  not the batch's (2026-11-19).
- `closure-equiv` says host-02 and the edge "really change" for any Go
  change: hostd's derivation takes the whole Go tree as its source, so
  `hostd-<rev>-go-modules.drv` re-hashes though hostd imports none of it.
  Read the "what changes" list: if it is only the hostd and gateway
  derivations and their units, and `go list -deps` of those binaries has
  no hit for the changed packages, nothing on the host changed. Worth a
  worker: filter the derivation's source to what the binary imports.
- Every PR run on GitHub was red for a week and nobody read one: the
  buf job passes its checks and then fails posting a comment with a token
  that lacks the permission. Workers rerun whole suites locally because of
  it. Fix the permission before trusting PR CI as a gate.

## Owed

- Dashboard-only live checks: dash-acts' say field, browser-view's frames
  and clipboard on a live machine, notify-hold's typing hold, the
  trigger made from the Triggers tab (started on e2e-dash on
  2026-11-18, result in the conductor's scratchpad).
- Revoke certificates has no safe live check: it cuts the owner's own
  SSH.
- 143 worktrees under `~`; removing them needs the owner's word.
- bump-devtools-path is on origin/main but the base is not published and
  host-02 and the edge are not switched; the daily bump's next run opens
  its PR from the fixed wrapper, and `gemini-cli.nix` hardcodes
  `bundle/gemini.js` the same way (not fixed).
- Live checks owed to the next release: say-agent-pick (one agent in a
  trigger run's window, `repose say` with no `--agent`), dash-create (New
  project from `/projects`), doors (the Triggers tab's doors), bump
  (`chrome-devtools-mcp --version` on the new base).
- The buf job's comment permission in `ci.yml` (above), and the
  post-merge full-suite reruns in worker briefs that it causes.
