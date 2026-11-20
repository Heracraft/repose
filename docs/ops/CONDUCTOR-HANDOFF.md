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

## Where things stand (2026-11-20, 10:40 UTC)

- **Three releases went out today.** `v0.1.52` from `dfbdaf7a` with base
  **2026.11.20.1** and **`appliance-2026.11.20`**, then `v0.1.53` from
  `f6aab4f2`, which pins that appliance in the CLI; `origin/main` is
  f6aab4f2 and the queue is empty. A twelfth batch, `r20261120-12`, is
  merged into **local** main at `0f8aa2ca` and deliberately not pushed: it
  is this document and one STATUS line, so it ships nothing, and a push
  cycles production for nothing. It goes out with whatever is released
  next. `releases/latest` resolves to the CLI tag
  and not the appliance, checked after publishing, which is last night's
  second failure not repeated. No host switch and no edge switch: the
  branch established from the code that the api and hostd are independent
  in this batch.
- **The mac-mini program's repair batch is released.** 197 commits as
  `r20261120-10`, verified here at 105 Go packages with zero data races,
  422 Playwright tests, vitest 53 files, the guest system and its closure
  at 6,533,221,680 of 7,516,192,768 bytes, and the host configuration
  evaluating. One red was the batch's own and is fixed in it: a council
  report quoted the old domain, allow-listed rather than edited because
  the name has not moved and the line is a record of what a reviewer ran.
- **What a release claim is about is the artefact, not the tree.** v0.1.52's
  notes said a customer's Mac worked properly; the shipped CLI still
  pinned `appliance-2026.11.19`, so none of the hostd, guestd or hook
  repairs reached a Mac. The spark caught it on the published tag. The
  notes are corrected and ENG-143 carries the mechanism. **Check which
  artefact a claim lives in before publishing it.**
- **The live check passed on hardware**, run against a locally built
  appliance and not the published artefact, with the published v0.1.52
  CLI: a refusal at 55 GB free leaving no `disk.raw` and minting no host,
  an install resuming with `--join-file` to a guest running in 453 s, and
  `repose-ask --timeout 3m` answered with exit 0. ENG-125 closed on an
  absence. That evidence is what made publishing the appliance defensible
  rather than hopeful, and the sequence was: prove on hardware, then
  publish.
- **The weekly model limit was at 90 % at 08:20 and resets Saturday
  2026-11-22 at 07:00 UTC.** The mac-mini conductor stopped before
  launching the review's second pass and put it to the owner as a
  spending decision (ENG-14, `Needs you`). The agreed order if capacity
  must be chosen: the batch verification, then the appliance and its live
  check, then ENG-138's unknowns, then the second pass.
- **ENG-138 is narrowed on hardware and the narrowing is the result.** A
  logout gives the appliance **18 seconds** and the park is attempted; a
  restart gives **one**. So macOS honours the shutdown window on a session
  teardown and not on a system restart, and the remaining work is the
  restart hook rather than `ExitTimeOut`. One keychain failure in that run
  is recorded as **not evidence**: the reviewer had changed the account
  password with `dscl . -passwd`, which does not change the login
  keychain's own password.
- **The Mac's queue is empty**, about $38 spent at $1.353 an hour since
  06:02 UTC on 2026-11-19. Its shutdown and the `aws login` that any
  release of the host needs are the owner's, raised with them directly.
- Open and unowned, in the order they would hurt: **ENG-111**, the Hello
  reconcile writing a guest address while an op is open, confirmed on CI
  and reproduced here under full-suite load, seven read-then-act call
  sites, five weeks live, blocking no release. **ENG-141**, `repose host
  rm --local` stranding a machine with no warning and working while the
  CLI is not logged in, which wedged the owner's account twice this
  morning. **ENG-119** with its siblings ENG-120, ENG-121 and ENG-122.
  **ENG-139**, a golden whose order depends on `TMPDIR`. **ENG-113**, the
  suite at `-p 2`.
- **CI's `nix flake check (evaluate)` is the job that will red on you
  next, and it is not your commit.** On 2026-11-20 run `44710016416` on
  `f6aab4f2` took 46 minutes against its usual 11 and lost its runner;
  every step after the Nix install carried an empty conclusion, the log
  answered `BlobNotFound`, and GitHub's annotation on check-run
  `136199039366` names starving the runner for CPU or memory first. The
  parent commit was green on all twelve jobs and the diff touches nothing
  under `nix/`. A re-run on a fresh runner passed, and then the same job
  lost its runner again 25 minutes later on a branch of documents, this
  time saying `The runner has received a shutdown signal` 2.5 minutes
  into the evaluation. **ENG-144** holds the finding with the measurement:
  that command wants more than 8.47 GiB in one process, measured here
  inside a cap, on a 16 GB runner. Re-run it before you suspect the
  commit, read the annotation rather than the empty log, and know that a
  red there currently carries no information about your change.
- **`7db572b5` is still held** and nothing today touched it. ENG-129 is in
  `Needs you` with the fact that decided the argument: `POLAR_PRODUCT_OWN`
  is set on production, the plan is purchasable at $10 with a seven-day
  trial, and the public copy is the owner's separate gate.


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
- **A release claim is about an artefact, not about the tree.** v0.1.52's
  notes said a customer's Mac worked properly. True of `main`; false of
  the release, because the shipped CLI still pinned the previous
  appliance, so every repair in hostd, guestd and the hook reached nobody.
  Before publishing a claim, ask which artefact it lives in and whether
  that artefact is the one the release ships (ENG-143).
- **A test whose input is the repository is touched by every change.**
  `test/domain` failed on a line in a merged council report, and neither
  of the branch's runs covered it because both chose packages from the
  diff. Choose by what a test reads, not by what the diff names.
- **The exit code belongs to the thing that looked, not the thing looked
  at.** Three in one day: `golangci-lint` absent from `PATH` reporting a
  lint failure in zero seconds, `go test -race` without `CGO_ENABLED=1`
  erroring instead of running, and a watcher of mine reporting failure
  because its last command was `grep -c` on a pattern that correctly
  matched nothing. Read the tool's own output and its duration.
- **Prove on hardware, then publish.** The appliance went out only after
  the three-part live check passed on a Mac against that exact image,
  because publishing an unproven one would have replaced a build that
  starts two times in nine with one nobody had started at all. Publish
  with `--latest=false` and run `ops/dev/release-assets` **after**, which
  is the only order that catches both of 2026-11-19's failures.
- **`/tmp` is on the 29 GB root disk, not a tmpfs, and `/tmp/rt` grows
  without bound.** It held 7.9 GB at 01:04 on 2026-11-20 with the root
  filesystem at 86 %, mostly 372 `repose-sync-*.tar` from CLI tests plus
  `go-build*` and test temp directories; the box-prep line about `df -h /`
  under 85 % is about this as much as about the Nix store. Delete entries
  older than three hours and leave `nix-develop-*` and `nix-shell.*`
  alone, because a live dev shell holds one. Expect `df` not to move at
  once: a third of what I removed was still held open by running
  processes, and the space comes back when they exit.
- **A platform's own annotation beats both sides' inference.** A red
  whose log would not load had two of us reasoning from shape: I was
  ready to accept the infrastructure's word, and the mac-mini conductor
  had been wrong about load on shape four hours earlier, which is the
  only reason they went looking for the link instead of agreeing. The
  answer was one annotation on the check-run and one grep of `nix/`.
  **Inference was the expensive thing all day; looking was the cheap
  one.**
- **Measure inside the cap rather than ban and guess.** This box forbids
  a whole-flake evaluation because it takes 8.5 to 12 GB, and that range
  came from runs with their builds, so the evaluation's own cost was
  attributed by reasoning. One capped run settled it: the cgroup killed
  `nix` at `anon-rss:8886640kB` with `--no-build` and nothing built, and
  it had not finished. The ban is on running it uncapped; a cgroup kill
  costs one evaluation and yields a number, where the uncapped run costs
  a session and yields the same number. `DEV-BOX.md` has the invocation.
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
