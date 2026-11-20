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

## Where things stand (2026-11-20, 01:30 UTC)

- **No release happened on this shift and nothing was deployed.** CLI
  v0.1.51 is still `Latest`, `origin/main` is still a3ecdc45, the base is
  2026.11.19, `appliance-2026.11.19` is correctly not `Latest`. A release
  needs the owner's word and they have not given one. Nothing was pushed,
  switched, published or tagged.
- Three batches were integrated: **r20261120-1** (ci-buf-comment,
  conductor-handoff-5), **r20261120-2** (stop-attribution, pick-clock) and
  **r20261120-3** (gemini-entry). Local `main` and `integrated` are the
  same commit. The next release now ships as **base**, which means the
  push is followed by `repose-admin base publish --rev <sha>`; before this
  shift it shipped as nothing to deploy.
- host-02 is healthy, read on the host rather than carried: up since the
  20:29 restart, system switched 20:33, `hostd.service` active, three
  `guest@*` units running and one of them restored from a park.
  **Burst is on in production** (`--burst --burst-gate 0.750000` in
  hostd's ExecStart), which is the owner's 2026-11-11 task, done; ENG-4 had
  been sitting in the board's "Needs you" asking for a go they had already
  given, and is now Done with that evidence. Stages 4 and 5 of the burst
  proposal are unbuilt, so burst is silent and unbilled. The edge is up.
- **Pull request CI is a gate again** (I-1400). For a week every run in
  the repository was red on `buf lint and breaking`, which passes its three
  buf commands and then exits 1 posting a comment the `contents: read`
  token cannot post. A red run therefore carried nothing. The first
  readable runs are green on that job.
- **What the restored gate found on its first day is ENG-111, and it is
  open.** On CI, `TestHelloLeavesAProjectWithAnOpenOpAlone` failed in
  1.41 s with "the reconcile wrote a guest address while an op was open",
  which is the behaviour I-720 exists to forbid, and it was then
  reproduced here in 10.09 s under full-suite load. The guard is
  read-then-act at seven call sites (three in `internal/api/ops/engine.go`,
  one in `internal/api/events/events.go`, two in
  `internal/api/http/projects.go`, one in `internal/admin/cmds.go`);
  `OpenOpsForProject` takes a `Querier`, so each could read inside its
  write's transaction, and the two on request paths are a separate
  decision because failing closed there is a refusal a user sees. The
  guard has stood unchanged since d26ab70f on 2026-10-14, which is I-720's
  own commit and is in the deployed main, so this is a five-week-old live
  defect rather than a regression, and **it blocks no release**. Its
  consequence for a tenant is unmeasured. The mac-mini mini-conductor and
  I agreed it does not jump the Mac's two P0s tonight.
- **The three "starvation" reds are self-inflicted, which changes the
  fix.** Per package on the same commit with the same shared Postgres:
  `internal/api/resume` 277.9 s here against 24.1 s on CI,
  `internal/api/ops` 414.7 against 53.0, `internal/api/scheduler` 254.8
  against 10.6. **Measured**: the 10x per-package difference, and that
  this box has 8 cores so `go test ./...` runs 8 packages at once against
  one server. **Inferred, not measured**: that the cause is the runner's
  lower parallelism; its core count is GitHub's documented standard and is
  nowhere in the job log, and a different Postgres fsync path would
  explain the same numbers. So try `-p 2` or an api-only pass and measure
  it before touching `apitest.WaitOp`'s flat 30 seconds (ENG-113).
  Related and not yet a problem: `internal/cli` took 461.0 s on CI and
  passed, at 77 % of Go's 600-second default that the CI job never sets,
  so a batch verification passes `-timeout` explicitly; mine used 30m and
  40m.
- The mac-mini program runs itself and **nothing of it is queued**; it will
  queue nothing until the Mac gives `fix-starts` five starts out of five.
  Two things of it are the release conductor's when it comes: **the
  `nix flake check` with its builds has never passed on that branch**, and
  the distinction matters because the job named "flake check" on CI runs
  `--no-build`. The evaluation half has passed there once, on PR #76 at
  81741b2b8 in 10m50s, for a head the branch is now 68 commits past; every
  VM test and every closure check is the difference between the two, and
  none of them has ever run on this branch. I agreed to run the real one
  on the batch worktree and to say that out loud in the batch record and
  the release notes. The second thing is commit **7db572b5**, which puts
  the Own hardware plan
  on public sale, must not reach main without the owner's word for that
  commit. The landing session holds a separate piece for the same reason.
  "The plan stays on sale in principle" is not that word.
- The Mac is in a bad state, which is the program's to fix: two successes
  in nine guest starts on the published build and nothing since 23:13 UTC,
  with the event log isolating `starting` while stop, destroy and restore
  each finish in about three seconds. The staged park test cannot be taken
  while nothing starts.
- `ops/dev/macmini` and the aws-mcp server both fail on an expired AWS
  session, so nobody can allocate, inspect or release the AWS Mac until
  the owner runs `aws login`, and it bills at $1.353 an hour meanwhile.
  The owner has approved a third Mac day: the money is approved and the
  credentials are not.
- The repository has **no GitHub Actions secrets at all** (`gh secret
  list` is empty), which is why ENG-99 cannot be built from here in any
  shape; see Owed.
- Secrets on this box are as the last conductor left them, the four files
  under `~/.config/repose-ops/`.
- Worktrees of mine under `~`: `kanali-ci-buf-comment`,
  `kanali-conductor-handoff-5`, `kanali-stop-attribution`,
  `kanali-pick-clock`, `kanali-gemini-entry`, `kanali-conductor-handoff-6`.
  The throwaway Postgres for the batches was `batch-pg-1120` on port
  55440, removed with `docker rm -f -v` at the end of the shift.


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
  in a private tmux (`tmux -L relcheck new-session -d "..."`), **at
  `-p 2`** and with an explicit `-timeout`, with `CGO_ENABLED=1`,
  `DATABASE_URL` on a `--network host` Postgres, and `TMPDIR=/tmp/rt`
  exported **inside** `nix develop` (exporting it there is what keeps the
  socket paths short enough; see the `/tmp/r` note in `DEV-BOX.md`); the web set to one log file
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
  that lacks the permission. Workers reran whole suites locally because of
  it. **Fixed on 2026-11-20 as I-1400, and not by granting the
  permission**: the comment is off (`pr_comment: false`), because the
  token stays `contents: read` and nobody reads a comment on a pull
  request the queue opened and the conductor merges. Do not re-add it. On
  the first readable runs the job is green, and the same runs found a live
  defect nobody could see while the gate was meaningless (ENG-111).
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
- Heavy nix goes in a **memory-capped scope**, not just under the lock:
  `flock /mnt/nixstore/repose-ws/nix-build.lock systemd-run --user
  --scope -p MemoryMax=6G --quiet -- choom -n 1000 -- nix build ...`, and
  never `nix flake check` with its builds (`DEV-BOX.md`, "Heavy Nix runs
  in a memory-capped scope"). The cap is what saves a session: on
  2026-11-20 `choom` worked, the kernel took nix at 12.5 GB, and the
  mini-conductor's session still died of the pressure around it.
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
- **Compare per-package times across machines before blaming a machine.**
  The three api reds of a full `-race` run read as a weak box for an hour.
  The runner is ten times faster per package (`internal/api/scheduler`
  10.6 s there against 254.8 s here), which is measured; that the cause is
  package parallelism against one Postgres is the leading explanation and
  is **not** measured, and saying so matters because the one-line version
  of it reached another session and a `CHECKLIST.md` row within the hour.
  The suite's total hid the difference, and the fix that looked right,
  widening `WaitOp`, would have hidden the real defect the same suite
  found.
- **Read a completed job's log while its run is still going.** `gh run
  view --job <id> --log` answers "run is still in progress; logs will be
  available when it is complete", even for a job that finished and failed
  minutes ago, so a red job sits unread behind a slow one in the same run.
  `gh api repos/<owner>/<repo>/actions/jobs/<id>/logs
  --allow-escape-sequences`, piped through `sed 's/\x1b\[[0-9;]*m//g'`,
  returns it.
- **Kill tmux windows by name, never by index.** The mac-mini
  mini-conductor killed three windows it meant to keep on 2026-11-20
  because tmux renumbered after its earlier kills. The worktrees were
  clean at their last commit, so only one in-progress commit was lost,
  which is luck and not design.
- `ops/dev/release-queue live <branch> --ran` takes its text as an
  argument; a bare `--ran` dies with `$2: unbound variable` and no usage
  line. `--ran TEXT`, `--not-done WHY`.
- `release-queue done` removes the batch worktree, so a test you want to
  rerun on exactly what you verified has to run elsewhere; once main has
  moved, the main checkout is the same commit.
- **Run the batch's Go set at `-p 2`.** Measured on this box on
  2026-11-20: the whole tree at `-p 2` is 101 packages ok with no
  failures, and the three api tests that are red at the default
  parallelism pass. `internal/api/ops` 20.4 s against 414.7 s,
  `internal/api/resume` 6.6 s against 277.9 s, `internal/api/scheduler`
  2.5 s against 254.8 s, each of which also beats CI's time for the same
  package. The caveat is in the number and not hidden: the slow figures
  were taken while several sessions loaded the box and the `-p 2` run
  after it went quiet, so parallelism and ambient load moved together and
  the isolating run has never been done. It is enough to invoke the suite
  this way and not enough to call `WaitOp`'s flat 30 seconds correct;
  widening that would have hidden the race of ENG-111.
- **A document both a branch and main have edited conflicts add/add, and
  "pick a side" deletes rules that look like nothing.** Merging main into
  the mac-mini branch on 2026-11-20 did this to `docs/REVIEW.md`: an
  earlier copy had reached main through PR #80 while the branch's copy
  kept growing, so each side held rules the other lacked and git offered
  only one or the other. The mini-conductor diffed them and wrote the
  union, 224 lines. Expect the same for any shared document when you cut
  a long-lived branch, and read both sides rather than resolving: git has
  no opinion when the loss is not textual.
- **`/tmp` is on the 29 GB root disk, not a tmpfs, and `/tmp/rt` grows
  without bound.** It held 7.9 GB at 01:04 on 2026-11-20 with the root
  filesystem at 86 %, mostly 372 `repose-sync-*.tar` from CLI tests plus
  `go-build*` and test temp directories; the box-prep line about `df -h /`
  under 85 % is about this as much as about the Nix store. Delete entries
  older than three hours and leave `nix-develop-*` and `nix-shell.*`
  alone, because a live dev shell holds one. Expect `df` not to move at
  once: a third of what I removed was still held open by running
  processes, and the space comes back when they exit.
- The exit-through-a-pipe trap again, in a verification script this time:
  a check whose output goes through `tail` or `grep` reports the pipe's
  exit, so `EXIT 0` printed under a `--- FAIL` line is the pipe
  succeeding. Read the `ok` and `FAIL` lines whenever either is piped.

## Owed

- Dashboard-only live checks: dash-acts' say field, browser-view's frames
  and clipboard on a live machine, notify-hold's typing hold, the
  trigger made from the Triggers tab (started on e2e-dash on
  2026-11-18, result in the conductor's scratchpad). Not attempted on the
  night of 2026-11-19 to 20 on purpose: they need a live machine, a
  signed-in dashboard and playwright on a box that was running batch
  verifications, and a hurried live check is worth less than none.
- Revoke certificates has no safe live check: it cuts the owner's own
  SSH.
- 143 worktrees under `~`; removing them needs the owner's word.
- Live checks owed: the mac-disk-floor refusal sentence on a Mac with
  under 57 GB free, which only the mac-mini program can run because it has
  the Mac. Two were closed on 2026-11-20, on a machine made and destroyed
  for them: **say-agent-pick** (I-1356), where a trigger run put its agent
  in a `t-saycheck` window, `repose say` with no `--agent` printed "Sent to
  t-saycheck" and the agent answered; and **the agent wrappers on base
  2026.11.19**, where `chrome-devtools-mcp --version` printed 1.10.1,
  `gemini --version` 0.61.0 and `playwright-mcp --version` 0.0.80 inside
  the guest.
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
- **Four cards this shift opened or corrected, none of them held by the
  conductor.** ENG-111, the Hello reconcile writing a guest address while
  an op is open: confirmed on CI and reproduced here under full-suite
  load, seven read-then-act call sites, five weeks old, live, unmeasured
  for tenants, high, and behind the Mac's two P0s. ENG-113, the three api
  tests that fail the full `-race` suite here: the per-package times make
  it our own `-p 8` against one Postgres rather than a weak runner, so
  `-p 2` or an api-only pass comes before touching `WaitOp`. ENG-114,
  `release-queue done` leaving a stacked pull request open for good
  because its base branch never advances, with the one-line fix named.
  ENG-4, burst on host-02, moved out of the board's "Needs you" to Done,
  because it was live and the card was asking the owner for a go they had
  already given.
- **ENG-108 is the owner's "remove some of these automated checks", and
  this shift's evidence argues one way.** Split the Go job so buf, vet and
  lint gate every run and the tests run where they can finish; do not
  delete the tests. The test portion is the slowest thing in the run and
  the only check of the night that found something no local run could.
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
