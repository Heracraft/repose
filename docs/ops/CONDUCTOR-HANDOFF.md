# Conductor handoff

Read this first, then `docs/ops/RELEASE.md` and `docs/ops/ORCHESTRATION.md`.
Written by the conductor of 2026-11-11 to 2026-11-18 (session `conductor`
on woker-1), and revised at each handover since; "Where things stand"
is dated and is the newest conductor's. It says what is true now, what
the owner has ruled, what the commands are that worked, and what is
owed. Keep it current: the next conductor rewrites "Where things stand"
and appends to "Lessons".

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

## Where things stand (2026-11-20, 00:25 UTC)

- Live and unchanged since the last conductor's shift: CLI v0.1.51 is the
  `Latest` release (tagged 21:27 UTC on 2026-11-19), `origin/main` is
  a3ecdc45, the base is 2026.11.19. `appliance-2026.11.19` exists and is
  correctly **not** `Latest`, so I-1398's second failure has stayed fixed.
- host-02 is healthy, read on the host at 00:14 UTC: up 3 h 43 m (the
  20:29 restart of the outage), `/run/current-system` switched 20:33 to
  `mz9z1xh2...-nixos-system-host-02-26.11.20260916.b1b8759`, `hostd.service`
  active and running, three `guest@*` units running, one of them restored
  from a park snapshot. The edge is up 5 h 45 m (its 18:27 restart).
  **Burst is on in production**: hostd's ExecStart carries `--burst
  --burst-gate 0.750000`, which is the owner's 2026-11-11 task, done.
  Stages 4 and 5 of the burst proposal (the overage switch, the status
  row, the docs) are still unbuilt, so burst is silent and unbilled.
- Two names cost me a false alarm each, and both are in Lessons: the host
  is **10.255.0.4**, not .2 (that is retired host-01, and it answers "No
  route to host"), and the unit is **`hostd.service`**, not
  `repose-hostd` (every other unit on the host does carry the prefix).
- Local `main` is be8edb8e and `integrated` is the same commit. It is **25
  commits ahead of `origin/main`** and every one of them ships as nothing
  to deploy: tonight's review method, the conducting record, three
  proposals, five decision entries and `ops/dev/release-assets` with its
  tests. They are waiting on the owner's release word, and nothing else.
- The queue holds one entry, mine: PR #86 `ci-buf-comment` (I-1400, ships
  none), which stops the buf job posting a comment the `contents: read`
  token cannot post. **Pull request CI had been red on every run in the
  repository for a week on that and nothing else**, so a red run carried
  no information; on #86's own run the job is green again. Do not cut a
  batch of it alone unless you want the gate proven; it costs nothing to
  carry.
- The mac-mini program runs itself. `kanali-mac-mini-da` is its
  mini-conductor in `~/kanali-mac-mini` and is alive, with builders and
  two fixers as windows of the `mac-mini` tmux session. Its branch is 56
  commits ahead of main and **held behind its own review gate**; it will
  hand it over as one unit and not before. Two things about it are mine
  when it comes: the **full `nix flake check` has never passed on that
  branch** (abandoned after two OOM kills; its checks were built one at a
  time under the lock), which I have agreed to run on the batch worktree
  and to say out loud in the batch record and the release notes, and
  commit **7db572b5, which puts the Own hardware plan on public sale, is
  held deliberately** and must not reach main without the owner's word for
  that commit. The landing session holds a separate piece for the same
  reason. "The plan stays on sale in principle" is not that word.
- The Mac is in a bad state, which is the program's to fix but the
  owner's to pay for: the reviewer's tally on the published build is
  **two successes in nine guest starts and nothing at all since 23:13
  UTC**, with the event log isolating `starting` while stop, destroy and
  restore each complete in about three seconds. The staged park test
  cannot be taken while nothing starts.
- `ops/dev/macmini` and the aws-mcp server both fail with "Your session
  has expired. Please reauthenticate using `aws login`". Nobody can
  allocate, inspect or release the AWS Mac until the owner re-authenticates,
  and the host bills at $1.353 an hour meanwhile. The owner has approved
  a third Mac day; it is the credentials that are missing, not the money.
- Secrets: the four files under `~/.config/repose-ops/` are as the last
  conductor left them. **The repository has no GitHub Actions secrets at
  all** (`gh secret list` is empty), which is why ENG-99 cannot be built
  from here; see Owed.
- Worktrees under `~`: the program's, the owner's, and two of mine
  (`kanali-ci-buf-comment`, `kanali-conductor-handoff-5`).


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
- **An always-red check is as useless as an always-green one, and costs
  more**, because it also teaches everyone to ignore the place real
  failures appear. Every pull request run in the repository was red for a
  week; the one red job passed its three buf commands and then exited 1
  posting a comment the token cannot post. Nobody asked why the red was
  permanent. Read a run by job, not by conclusion, and when a job is red
  on every branch, that is the bug.
- **Read a check's exit code, and read what it actually selected.** Two
  sibling traps, both met in one hour: `ssh root@10.255.0.2` answers "No
  route to host" because .2 is retired host-01 and host-02 is .4, and
  `systemctl is-active repose-hostd` answers `inactive` because the unit
  is `hostd.service` while every other unit on the host is `repose-*`.
  Either answer, believed, reads as a production outage. List what
  exists before concluding something is absent.
- A new worktree needs its own `pnpm install --frozen-lockfile` before
  `just done-check`, or the web half fails with "Command vitest not
  found" and nothing wrong with the branch.
- The full `go test -race ./...` with one Postgres for every package
  gives four reds on a quiet-ish 8-core box, and they are not the
  batch's: `TestHelloLeavesAProjectWithAnOpenOpAlone`,
  `TestAFailedWakeIsNotSentAgainStraightAway` and
  `TestResumeSkipsStopAndDestroyWhileUnreachable` each wait 30 s in
  `apitest.WaitOp` for an op the starved engine does not finish, and all
  three pass alone on the same commit. The fourth,
  `TestPickTable/xl_fits_above_reserve`, was wrong by construction and is
  fixed as I-1412. Rerun the three alone before reading anything into
  them, and do not file them with the fourth.
- `ops/dev/release-queue add` validates the body with `ops/dev/pr-record`
  and refuses a Markdown heading where the template has none: the
  what-changes part is everything before `**Why.**`, which must be that
  exact bold marker. Run `ops/dev/pr-record check <file>` before `add`.

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
- The post-merge full-suite reruns that worker briefs carry because
  pull request CI could not be trusted. The gate itself is fixed (#86,
  I-1400); the briefs still say to rerun by hand, and whether the gate
  is trusted again is a judgement to make after a batch has been green.
- **Four things only the owner can unblock**, each with the consequence
  of leaving it:
  1. The **release word**. 25 integrated commits, all `ships: none`, are
     not live, and ENG-99's card cannot go green at all until the push,
     because what proves it is the first scheduled `release-assets.yml`
     run against a pushed main.
  2. **`aws login`**. Nothing can touch the AWS Mac until it is done,
     the program needs that hardware for three things no other machine
     can answer, and it bills at $1.353 an hour either way.
  3. **ENG-99's channel.** A red scheduled `release-assets.yml` run
     reaches only whoever GitHub notifies. Routing it to the owner's
     chat needs a webhook URL as a GitHub Actions secret, and this
     repository has no Actions secrets at all, so nothing can be built
     from here either way. The alternative that needs no secret in
     GitHub is an Uptime Kuma monitor on the workflow's latest run
     conclusion through the public API, which is a change on the owner's
     Kuma rather than in this repository. The owner picks which.
  4. Whether **7db572b5** (the Own hardware plan on public sale) and the
     landing session's held piece go, and when.
- From the outage research (PR #77): the api pages nothing itself on
  `host_unreachable`; an outside probe of the VM agent heartbeat; the
  edge keeping wgsync's peers across a reboot; `repose_gateway_wgsync_errors_total`
  never exported; nothing watches `repose-wg-watchdog`. The owner picks.
- The xl park deadline: an xl that cannot snapshot in time loses its
  running state on every graceful host restart (txhm, 2026-11-19).
- kanali's and verista's stop ops at 20:30 on 2026-11-19: read out in
  `docs/proposals/2026-11-19-azure-host-events.md` §4a. Both xl machines,
  stopped twenty seconds apart; verista's stop completed with its
  snapshot at 20:31, kanali's failed at step 1 of 2 at 20:33 with the
  thin pool `context canceled`, which is the hostd switch of that
  minute, so **kanali has no snapshot from that stop** and its newest is
  03:10. Who asked cannot be recovered: the op rows carry no actor, the
  audit log records no project stop or start at all, and the api's logs
  went with the redeploy. Two things for the owner: did you stop those
  two before the switch, and is an actor on the op worth building.
