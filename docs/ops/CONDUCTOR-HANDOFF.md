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

## Where things stand (2026-11-27, 01:45 UTC)

Written by the release conductor of 2026-11-25 to 27 (woker-1). Program books: `~/briefs/design2-progress.md` (running log), `~/briefs/design2-program-2026-11-26.md` and `~/briefs/design2-skeleton-2026-11-26.md` (briefs), `~/briefs/design2-program.log` (coordination). The owner's one-page read is `docs/reports/2026-11-27-design2-summary.md`; the measurements are `docs/reports/2026-11-26-design2-ux.md`; the shape and build plan are `docs/proposals/2026-11-26-design2.md` (I-1662 and amendments I-1664 to I-1672).

- **Design 2 (Mac without the appliance) is proven as a skeleton, not built.** Code is on branch `design2` (head 66ee08f2), never on main except the api's inert parked state (I-1672). Two owner decisions wait: ENG-270 (build it, 32-39 builder days) and ENG-266 (rest by pause vs by save). Open fixes: ENG-263, 264, 265, 268, 269; ENG-267 parked.
- **host-02 is Standard_D32s_v7 until launch** (owner, 2026-11-27, runway; ENG-262, r20261127-1, applied ~01:05Z, re-registered, 125.8 GB, 11 seats). Before launch: the same change reversed plus `hosts add --name host-02 --reissue` and `hostd undrain` on the host (RUNBOOK, re-register section).
- **The owner's machines on host-02 are all stopped.** Four (kanali, verista, shule-id, job) were stopped at 00:11Z by a builder that used an owner session outside its e2e-only brief; disks and snapshots kept. The owner starts them. Rule since: every brief that hands out an owner session forbids stop, rm and restart on any non-`e2e-*` machine and requires each machine by name.
- **The AWS Mac** (mac-m4 host h-02930697783cd33c2, eu-west-1b) was being terminated and released from 01:40Z (`tmux -L relcheck` session `macdestroy2`, log `~/briefs/macmini-destroy2.log`, outcome in `~/briefs/aws-mac-billing.log`). If the log does not say RELEASED, run `aws login --remote --profile repose-lab` with the owner, then `ops/dev/macmini destroy` until `ops/dev/macmini status` shows no host (ENG-249). Lab host `e2e-d2n` is already off the owner's account.
- **Queue:** batch r20261127-2 (PR #235, landing Mac figures refreshed, `MAC_DESIGN2_LIVE` still false) was cut and under web checks in `~/projects/factory-r20261127-2`; finish it with the usual read-then-merge (`done`), or `drop` it.
- **Owner, 2026-11-27: the board is filling up; the next conductor runs big sweeps.** Take open cards in batches by area (dashboard, CLI, owned hosts, docs), fix them with a few builders at a time, and close each with evidence, rather than one card per session. Next up by the owner's word: ENG-190 (remove `repose config add` and the add-a-service UI), ENG-131 (a create on an unregistered own host waits: "Waiting for your Mac (mac-mini) to finish installing. This project starts there when it's ready."), ENG-226 with ENG-250 (dashboard create starts the project's default agent, asks which when none is known; start an agent with Remote Control), ENG-171 (hosted plans may bring their own hardware; price leaning included).
- **ENG-255** (Open in Claude agents missing): stage 1 on main; live needs an erofs guest base release, a hostd release and a host-02 switch, the switch on the owner's word.
- **Lessons:** a builder given an owner session will act on the owner's machines unless the brief names the machines it may touch; shred every minted session when its holder stops, and check `/mnt/nixstore/sharing/*/cfg` for leftovers; never clean up disk by removing worktrees a running builder uses; the root disk above 90 percent fails guestd's `TestHerdrProjectWarnsHerdrDown` (ENG-248).

## Where things stand (2026-11-26, 10:30 UTC)

The sharing program (machines on hardware you own share it) is built, reviewed twice (pass 2: no P0) and shipped: hostd 2026.11.26.2, appliance 2026.11.26.1, CLI v0.1.66; main 20871b13. Reports: docs/reports/2026-11-25-sharing-lab.md, 2026-11-25-mac-architectures.md, 2026-11-26-sharing-council.md, -pass2.md. Decisions I-1624 to I-1660.

- **Owed:** the rented AWS Mac (eu-west-1b, host h-02930697783cd33c2) is not released: the AWS session expired at teardown (ENG-249, Needs you). `aws login --remote --profile repose-lab`, then `ops/dev/macmini destroy`.
- **Lessons:** run `go test ./...` for the whole repo and the nix Go build on every batch (a package list missed obslint, test/domain and the nix source filter); never hand-edit DECISIONS-INDEX.md; unix socket tests fail under a long TMPDIR; woker-1's root disk near 90 percent trips guestd's disk_high test (ENG-248); `release-queue cut` takes everything queued (use `drop` to ship one alone); the appliance image CI job fails, build it on woker-1 with /mnt/nixstore/tmp/nonix-appliance/rebuild-main.sh; on a nested Mac, swapping guest RAM freezes the guest (I-1656).

## Where things stand (2026-11-25, 00:30 UTC)

Written by the release conductor of 2026-11-23 to 25 (woker-1). The two
program books are `/home/azureuser/briefs/x86-multihost-2026-11-23.md`
and `/home/azureuser/briefs/frontend-users-2026-11-24.md`; each ends in a
dated log.

- **main is `becc81f4`, pushed and live; the queue is empty.** Batches
  `r20261123-4` to `r20261124-9` are merged. Released last: nix base
  `2026.11.24`, erofs (Ubuntu) base `2026.11.24.1`, CLI `v0.1.63`
  (signed, `release-assets` and CI green at `becc81f4`). host-02 was not
  switched this shift; nothing in these batches needed it.
- **Release records are owed for `r20261124-3` to `-9`** (`docs/releases/`
  has up to `r20261124-2`). `ops/dev/release-queue record`, on a
  `records-N` branch, PR body shaped like the earlier records PR (it needs
  the ```record block or `pr-record check` refuses it).
- **Program 1, many own hosts per account (x86), is shipped and closed.**
  The Own plan takes any number of hosts (I-1594), addresses are reused
  and an account may have 10 waiting (I-1598), placement re-reads room on
  refusal (ENG-214), the x86 installer checks the kernel after apt
  (I-1600). Customer gate passed on Latitude hardware (pass 2b, report
  `docs/reports/2026-11-24-x86-multihost-2.md`). All Latitude servers are
  destroyed; spend under $10 of $100 (`/home/azureuser/briefs/latitude-billing.log`).
- **Program 2, front-end users (Desktop, web, phone), is shipped and
  live-checked.** Research `docs/research/frontend-users-2026-11-24.md`;
  review `docs/reports/2026-11-24-frontend-preview-review.md` (pass 2 has
  nothing at P0 or P1). Shipped: the one-field New project form (I-1602,
  I-1612), Messages instead of the say field (I-1604), Open in Claude and
  `repose code --editor claude` (I-1606), the hook detecting the agent from
  the payload (I-1608), `projects.checkout` (migration 0053, I-1610). Live
  checks on 2026-11-24 23:30-23:50: href exact, Messages reply in 5 s,
  hook outside the wrapper "finished ok" on erofs `2026.11.24.1`.
- **Research: agent-first machine** (`docs/research/agent-first-machine-2026-11-24.md`).
  One survivor: language servers on PATH plus each harness's own switch
  (ENG-212), measured in a real guest first (ENG-213). The owner's
  gimmick tests apply to anything proposed from it (rules below).
- **Owner decisions this shift:** unlimited hosts on the $10 Own plan;
  ENG-229 (setup with no terminal) is parked to 2027-01-15, the one-time
  CLI install stays; 50 merged, clean, idle worktrees were removed with
  the owner's word (audit `/home/azureuser/briefs/worktree-audit-2026-11-24.txt`;
  15 kept, listed there).
- **Waiting on the owner:** ENG-226 (a dashboard-made machine has no
  code and no agent), ENG-215 (the 1000-host deployment-wide address
  ceiling; POST /hosts answers 500 when full), ENG-171, ENG-190, ENG-131,
  ENG-5; the owner's Mac script `~/previews/open-in-claude/owner-mac-script.md`
  (the only test of the real Desktop, T3 Code and Codex apps); and a gh
  token rotation advised after pass 2b copied the owner's gh and git
  logins into e2e guests on a Latitude box (since wiped).
- **Open, ready for builders:** ENG-232 (a Messages send makes Claude
  exit while it sits at its trust prompt; agents repose starts are not
  affected), ENG-234 (the erofs publish message says "at their next
  start"; it is the 04:00 sweep), ENG-233 (review P3s), ENG-212, ENG-213,
  flakes ENG-220, ENG-230, ENG-231; the research follow-ups (a dashboard
  Remote Control switch, `sshHostCommands`).
- **Box:** `/` at 85 % on 2026-11-25 00:20; clear `/tmp/rt` and the Go
  cache before the next batch.

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

- Gimmicks (2026-11-24): forks, receipts and acceptance passes, phone
  reach, `config add` were rejected. Before proposing a machine-side
  feature ask: can the agent already do it; does it rest on user intent
  or self-grading; does a harness vendor ship it; is it harness-side.
- Run `repose run` and `create` from an empty `mktemp -d`, never a repo
  checkout: the CLI copies the folder, env files and gh and git logins
  into the machine (2026-11-24).

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

- `TaskStop` on the harness shell that launched a detached `setsid` job
  killed the job too (an emulated 40-minute arm64 build, 2026-11-22):
  leave finished launcher shells alone, or launch long jobs from a
  `Monitor`/`run_in_background` command of their own.
- `release-queue resume` and `cut` merge the commit a PR was queued at,
  not the branch's newest push; a fix pushed to a cut branch is not in the
  batch until the builder re-runs `release-queue add` (2026-11-22, twice).
- A release image is built from main's head, in a detached checkout, never
  from a branch: the first Mac appliance of the No-Nix program carried a
  hostd from before the split-parts fetch and no Mac could create a
  machine; `images/appliance-arm64/build.sh` now refuses anything else
  (I-1540). The Mac program learned the same on 2026-11-19.
- Boot the image, do not trust the tree: the guest image shipped `/etc`
  group-writable twice (the checkout's umask through `COPY`, then a later
  `COPY` undoing the fix); sshd refused every login. `build.sh` now reads
  the modes from the produced erofs inodes (`dump.erofs` masks special
  bits; read `i_mode` raw, `erofs-mode.py` in `docs/research/nonix-staging-e2e/`).
- Never commit to a build script while a 40-minute emulated build is still
  reading it: bash reads the file as it goes and the arm64 build died on
  "line 126: t: command not found" (2026-11-22).
- Sub-agents that go quiet with a command "that never ran" are sitting on
  a permission prompt the owner is not there to answer, not hung: three
  builders lost one to two hours each on 2026-11-22 until stopped and
  resumed by message (which keeps their context). The owner's terminal
  shows the prompts; check `/permissions` for `ask` rules that match
  `ssh`, `docker` or the staging CLI before blaming the harness.
- A tool that lists sizes or modes is not the artefact: one `dump.erofs`
  reading produced a false sticky-bit finding that nearly went into a
  build gate (2026-11-22).
- `.gitignore`'s `bin/` rule swallowed nine appliance scripts for a whole
  program; `embed.sh --check` on main is the grep that catches it.
- An owned host that runs only erofs guests cannot create anything while
  the account's default runtime is nix, and the CLI called it "no room":
  flip `default_guest_runtime` before telling customers to add a host
  (D-24 in the program summary; I-1538 for the sentence).
- Two sparks on one exempt account share its single own-host slot
  (`ExemptHosts = 1`): sequence hardware checks, or use two accounts.
- A guest image bundle is 2.86 GB; the appliance root was 2 GiB and held
  the image cache: `repose-images-volume` (I-1494 amended) is the fix, and
  "where does the cache live" is a question for every new host shape.
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
- **A stacked pull request cannot be merged by GitHub, only closed.**
  `gh pr create --base BRANCH` bases a PR on another queued branch.
  When the batch lands, `done` pushes `integrated` and GitHub marks the
  PR whose base *was* `integrated` as merged, while the stacked ones stay
  open: their base branch still exists, and `gh pr edit --base integrated`
  then refuses with "There are no new commits between base branch
  'integrated' and head branch", which is the refusal telling you the
  work is already in. Confirm with `git merge-base --is-ancestor <head>
  origin/integrated`, close them with a comment naming the batch, and
  delete the merged branches. Two were left open on 2026-11-20 until I
  looked.
- **A verification that is not installed, and a suite talking to the
  wrong server, both report what looks like a product failure.**
  `golangci-lint` outside the dev shell answers "No such file or
  directory" in zero seconds, and a pipe calls that success. A Playwright
  suite whose predecessor still holds the test port fails every test in a
  file at the identical timeout, which reads as sign-in and billing being
  broken. The tell in both cases is the shape of the result rather than
  its content: zero seconds, or one duration repeated. `ss -ltnp` names
  the process holding a port in one line.
- **Measure inside the cap rather than ban and guess, and then check your
  own proposal.** A capped run settled what the flake evaluation costs
  with nothing built. Sharding it then looked obvious, so I measured that
  too: splitting by top-level group fails because `checks` alone is the
  whole peak, and sharding `checks` by four drops the worst process to
  6.06 GiB and runs faster. Then the proposal turned out not to be a
  one-line change, because `nix flake check` validates output shapes a
  sharded `nix eval` would silently stop checking. **The measurement was
  cheap and it is the thing that stopped me landing a weaker gate.**
- **A test asserting the conductor's own advice.** `DEV-BOX.md` tells a
  heavy Nix run to point `TMPDIR` at the big disk, a verification script
  took that for Go tests too, and a golden flipped because `unique` sorts
  the real path. Fixed in the test and recorded as I-1442; the boundary is
  in DEV-BOX.md. Advice travels further than the case it was written for.
- **A composed page is measured in the browser, not in the file that
  composes it.** I grepped `+page.svelte` for `data-motion-group`, got
  zero, and cleared a landing change as having no looping pictures. The
  attribute lives in the picture components; five of them loop, and the
  change had removed the page's only motion control, which is WCAG
  2.2.2. I had the page open in a browser at the time and nine
  screenshots of it. One `locator('.pic-frame').count()` would have said
  five. What caught it was a sixth spec file nobody had updated, doing
  its job.
- **An instrument that answers instantly and confidently is the one to
  check.** Three did it in one day. `kaneo ls` returns 50 of ~144 cards
  with column counts that are internally consistent. `gh pr checks <n>`
  aggregates superseded attempts, so a job re-run green still prints
  `fail`; read the newest run instead, `gh run list --branch <b> --limit
  1` then `gh run view <id> --json status,conclusion,jobs`. And the grep
  above. All three were wrong about the question asked, and none of them
  looked wrong.
- **`pgrep -f "<pattern>"` matches your own waiter.** An
  `until ! pgrep -f "playwright test"` loop contains that string in its
  own command line, so it never exits and every check says the job is
  still running. It cost me a suite I thought was running and was not.
  Match on something the waiter does not contain, or check the log.
- **A bound nobody can re-derive is one the next person widens.**
  `TestParallelRangesErrors` allowed `after + parallel + 1` fetches when
  the dispatcher is bounded by the buffer pool, `parallel + 1`, plus one
  per part the consumer hands back. One short since it was written, and
  only `-race` was slow enough to reach it. Before widening it I removed
  the `cancel()` and watched the test fail at every block in the
  fixture, because widening a bound is how a guard quietly stops
  guarding.

- **Two bases, two publishes.** New machines boot the erofs (Ubuntu)
  base, not the nix one. Build it with `gh workflow run guest-image.yml
  --ref main -f version=YYYY.MM.DD.N -f publish=true`, then
  `repose-admin base publish --format erofs` on the control VM.
  `docs/research/nonix-staging-e2e/publish-erofs.sh --dry-run` prints the
  command, but rootfs is split into parts: pass the whole-file SHA256s
  from each manifest's `.files["rootfs.erofs"].sha256` (2026-11-24).
- **A restart does not pick up a new erofs base.** `erofsBase`
  (`internal/api/ops/erofs.go`) takes the op's param, then
  `pending_base_version` (set by the 04:00 sweep), then `base_version`.
  Check a new base on a new project; ten restarts proved nothing
  (2026-11-24, ENG-234).
- **The CLI sign job needs 400 bytes of notes before it runs.** Set them
  with `gh release edit vX --notes-file` as soon as goreleaser's draft
  exists; otherwise `gh run rerun <id> --failed` (v0.1.62).
- **`cut` takes every queued branch; there is no hold.** Cut r20261124-9
  took held branches meant to wait; `release-queue abandon <rel>`
  requeues them, before any merge.
- **Playwright here needs** `PLAYWRIGHT_CHROMIUM_PATH=/nix/store/8kf2nnmqqg7i5h8h8yfw5w29biwwccas-repose-chromium/bin/chromium`
  and a unique `REPOSE_WEB_TEST_PORT`; strip ANSI before grepping the
  summary, a coloured "1 passed" hid 406.
- **An owner CLI session for live checks:** adapt
  `~/briefs/mint-owner-cli-session.sh` to a private `XDG_CONFIG_HOME`
  (the CLI honours it), run from an empty directory, shred after the hour.
  The script's final `rm` deleted the credentials before they moved, once.
- **A Claude started by hand waits at its trust prompt** and exits on a
  Messages send (ENG-232). Accept the prompt before a live Messages check.

## Owed

- Release records for `r20261124-3` to `-9`.
- Mac-only checks of Open in Claude in the real apps: the owner's script.

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
