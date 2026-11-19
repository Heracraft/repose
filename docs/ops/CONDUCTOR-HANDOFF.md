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

## Where things stand (2026-11-19, 21:35 UTC)

- Live: CLI v0.1.51 (v0.1.50 at 21:00, v0.1.51 at 21:29), main a3ecdc45
  pushed, base 2026.11.19 (at 28272062), host-02 at hostd 2827206 and the
  edge at 28272062, both switched 20:33 on the owner's word. The Own
  hardware work (mac-mini, I-1364..I-1382) is on production; the plan is
  on sale (`POLAR_PRODUCT_OWN` set by the owner); the edge hub material
  has been in `settings` since 2026-09-20 (no edge init needed).
- Local main equals origin/main and integrated. The queue is empty.
- The mac-mini program (sessions `kanali-mac-mini-da` the mini-conductor,
  `factory-3f` the spark) runs a customer review on production; its
  findings land as ENG cards on the owner's board; it queues branches
  itself and stays out of main. Known after v0.1.51: the dashboard's
  add-host dialog shows `join_token` where the CLI takes
  `install.argument` (ENG-29); `repose host add --join` puts a WireGuard
  private key on the command line (ENG-42); a create on an unregistered
  own host refuses and pages OpsFailing (ENG-46).
- The outage of 2026-11-19 (host-02 16:21-20:29, the edge 18:21-18:27,
  the control VM 19:10-19:37 UTC): Azure host-level events ("Live
  Migration Failed" on host-02) left two VMs running with dead NICs;
  `az vm restart` fixed each in seconds; the park unit saved four of five
  guests at the restart (the xl txhm skipped: "snapshot would outlast the
  deadline"). Grafana paged Chat within nine minutes each time; nobody
  acted for three hours. `docs/proposals/2026-11-19-azure-host-events.md`
  and RUNBOOK "Host or control VM dead on the network" hold the rest.
- Worktrees under `~`: the owner's and the mac-mini program's (`kanali-doors`,
  `kanali-form-shifts*`, `kanali-mac-mini*`, `kanali-council-*`, `kanali-pm`, `kanali-review-docs`); nothing of the conductor's.
- Secrets on this box: `~/.config/repose-ops/{grafana-token,
  logto-m2m-secret, polar-live-token, polar-sandbox-token}`; no Coolify
  token (the owner sets Coolify envs in the UI).
- Machines that are not yours: everything in `repose ls` except `e2e-*`
  you made. The box's CLI login (`~/.config/repose/account.json`) is the
  owner's; a CLI built from main is at `/tmp/rt/repose-main`.

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
- `gh release create vX --draft` makes an "untagged" draft and no tag;
  release.yml never runs. Push the annotated tag, wait about 90 s for the
  workflow's draft, then `gh release edit vX --title vX --notes-file`.
- A push deploys the docs pages at once; the CLI ships at the tag. A
  release that adds a command documents it before anyone can run it (30
  minutes on 2026-11-19, with the plan on sale). Tag right after the push.
- After a host restart the api reconciles from Hello and issues ops.
  Switching hostd two minutes later killed a stop-snapshot mid-op. Read
  `repose-admin ops list --state running` before a switch.
- `repose-admin hosts add` never calls PrepareHub and prints a bare
  token; `POST /v1/hosts` builds the WireGuard bundle. Read the
  `settings` rows before believing the api lacks edge material.
- Heavy checks go under `flock /mnt/nixstore/repose-ws/nix-build.lock`;
  two programs' VM checks at once put the load at 100 and OOM-killed nix
  evaluations. The harness kills background Bash waiters on "low memory"
  while the 15-minute load is high; a Monitor survives.
- A VM "running" with the VM agent Not Ready, a login prompt on its
  console and nothing on the network is a dead NIC from the host side;
  Resource Health may still say Available. Restart it (RUNBOOK "Host or
  control VM dead on the network").

## Owed

- Dashboard-only live checks: dash-acts' say field, browser-view's frames
  and clipboard on a live machine, notify-hold's typing hold, the
  trigger made from the Triggers tab (started on e2e-dash on
  2026-11-18, result in the conductor's scratchpad).
- Revoke certificates has no safe live check: it cuts the owner's own
  SSH.
- 143 worktrees under `~`; removing them needs the owner's word.
- `gemini-cli.nix` hardcodes `bundle/gemini.js` the way chrome-devtools-mcp
  did (not fixed). The daily bump's next run proves the wrapper fix.
- Live checks owed: say-agent-pick on a machine on base 2026.11.19 (one
  agent in a trigger run's window, `repose say` with no `--agent`); the
  mac-disk-floor refusal sentence on a Mac with under 57 GB free (the
  mac-mini program has the Mac); bump (`chrome-devtools-mcp --version`
  on the new base).
- The buf job's comment permission in `ci.yml` (above), and the
  post-merge full-suite reruns in worker briefs that it causes.
- From the outage research (PR #77): the api pages nothing itself on
  `host_unreachable`; an outside probe of the VM agent heartbeat; the
  edge keeping wgsync's peers across a reboot; `repose_gateway_wgsync_errors_total`
  never exported; nothing watches `repose-wg-watchdog`. The owner picks.
- The xl park deadline: an xl that cannot snapshot in time loses its
  running state on every graceful host restart (txhm, 2026-11-19).
- kanali's and verista's stop ops at 20:30 on 2026-11-19: trigger unknown
  (the api container's logs went with the redeploy); ask the owner.
