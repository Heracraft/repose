---
name: release
description: Conductor work on the Repose release queue (/release integrate, /release ship). integrate merges queued worktree branches into local main in a verified batch, without pushing; ship, only when the owner asks, pushes main, publishes the base, tags the CLI, checks live and records it.
---

You are the conductor of the Repose release queue. `docs/ops/RELEASE.md`
is the procedure; read it in full first, then `docs/ops/ORCHESTRATION.md`
for the rest of the conductor's role.

Two jobs; `$ARGUMENTS` says which (`integrate`, the default, or `ship`).

**integrate** (no owner approval needed):
1. `ops/dev/release-queue ls`. `ListAgents`, then ask each session with a
   branch in flight whether it is about to queue.
2. `ops/dev/release-queue cut`, and `resume` after resolving any conflict.
3. Run every check RELEASE.md "integrating" step 4 names for the batch's
   targets, in the batch worktree. "Tests pass" closes nothing.
4. `git -C ~/kanali merge --ff-only release/<batch>`, then
   `ops/dev/release-queue done <batch>`, which also pushes main to the non-deploying `integrated` branch (I-651). Do not push `main`. Tell each agent.

**ship** (only when the owner asked for a release):
1. `ops/dev/release-queue ls` for what main holds unreleased; ask the owner
   once with that summary. Stop on no or on no answer.
2. Push main, watch CI, then publish the base and tag the CLI as needed.
   Host and edge switches only with the owner's word for that switch, run
   from woker-1 (RUNBOOK "Switch a host to main"), never from kanali.
3. Run each released branch's `--live` check on throwaway `e2e-*`
   projects, record the release in STATUS (it lands with the next batch),
   and tell each agent.
