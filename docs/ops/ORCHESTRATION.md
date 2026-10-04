# Orchestration: how the waves are run

What actually happened while building Repose with many agents at once, written
as the procedure to repeat. The design docs say what to build; this says how
the building is coordinated. It is descriptive: every step below was run at
least once between 2026-09-19 and 2026-09-20. Since 2026-09-23 the work runs
as rounds instead of waves; "Rounds" below is how, and where it differs
from the wave cycle, it wins.

## Roles

- **The conductor**: one long-lived Claude Code session in the main checkout
  (`~/projects/factory`, branch `main`). It never builds a workstream itself.
  It merges, verifies, applies infrastructure, launches and restarts worker
  sessions, and fixes anything that blocks more than one of them.
- **Workers**: one Claude Code session per workstream, each in its own git
  worktree on its own branch, launched with `/ws <nn>` (`.claude/skills/ws`).
  A worker reads the docs, claims its line in `docs/workstreams/STATUS.md`,
  builds its whole workstream, and stops with a report. It never merges,
  never pushes, never creates further branches.
- **Integration sessions** (`/ws m1`, `/ws m2`): workers whose workstream is
  "make the merged code true on real hardware". They own the real-host
  checklist items the other workers had to leave open.
- **The owner**: runs anything that spends money or that the conductor's
  permission classifier refuses, and answers questions nobody else can.

## The wave cycle

1. **Cut the wave.** Pick workstreams whose consumed interfaces are already on
   `main` (see the dependency graph in `docs/workstreams/README.md`). Wave one
   was everything M1 needs (01, 02, 03, 04, 11); wave two was the interface
   consumers (05, 12, 14, 10); wave three the user-facing layer (06, 07, 08,
   13, the rest of 11) plus follow-ups.
2. **Create worktrees from the current `main`**, one per workstream:
   `git worktree add ../repose-ws/<nn>-<name> -b ws/<nn>-<name> main`.
3. **Launch one tmux pane per worker** in a window named after the wave:
   `claude --model <model> --permission-mode auto` in the worktree, then
   `tmux send-keys '/ws <nn>' Enter` once the prompt is up. Models per
   workstream are in `docs/workstreams/PROMPTS.md`. Four to six workers at a
   time on an 8-core box; more only makes Nix evaluations queue.
4. **Subscribe to completions**: the conductor asks each worker session for
   an idle notice (`SendMessage` with `notify_when_idle`) instead of polling.
   Workers also message the conductor directly when they find something the
   whole fleet needs (a contract change, a bug in merged code).
5. **Merge in dependency order** as workers finish: interface owners before
   consumers (03 before 04, 02 before 12, 05 before 06/07/13). Every merge
   is followed by the same four checks before the next merge starts.
6. **Verify `main`** after the wave: `go build`, `go vet`, `go test -race`
   with a real Postgres, `nix flake check`, the Go packages built through the
   flake, `tofu validate`, then push and watch CI.
7. **Apply infrastructure from `main`**, never from a branch, so hosts and
   the edge install from the merged flake.
8. **Run the integration session** against the real hosts before cutting
   the next wave that depends on the result. Its findings go back to `main`
   as ordinary commits, and every other open worker is told to merge `main`.

## Merging: the rules that came out of doing it

- **Merge into a scratch worktree first** when the wave is large:
  `git worktree add ../repose-ws/_merge -b merge/waveN main`, merge each
  branch there, reconcile, run every check, then fast-forward `main` to it.
  The main checkout stays usable for applies while the merge is being
  verified.
- **Append-only docs union-merge.** `.gitattributes` marks
  `docs/workstreams/STATUS.md` and `docs/DECISIONS.md` as `merge=union`, so
  parallel additions never conflict. Everything else is resolved by hand.
- **Decision ids are assigned before the work starts.** In waves one and
  two, workers numbered from the `main` they branched off, several branches
  added the same `I-<n>`, and the conductor renumbered them by hand after
  merging (no script was ever committed). Now the conductor gives each
  worker its range of `I-<n>` in the launch prompt, so nothing is
  renumbered. After a merge, regenerate the index with
  `python3 ops/dev/decisions-index.py`.
- **`go.mod` and `go.sum`**: take one side, run `go mod tidy`, rebuild.
- **`nix/packages.nix` vendor hash**: it changes whenever any branch adds a
  Go dependency. Set it to `lib.fakeHash`, build once, paste the reported
  hash. A wave usually changes it exactly once. Do this whenever `go.sum`
  differs from the last hash update, even if a local `nix build` passes:
  the fixed-output derivation's old output can still be in the local
  store and gets reused, so only CI (no cache) sees the mismatch
  (d90b31d, 2026-09-21).
- **The flake**: several branches add outputs to `nix/flake.nix`. Rewrite it
  as one file rather than resolving hunks; a duplicated `packages.${system}`
  attribute is an evaluation error that `nix flake check` catches.
- **Duplicate implementations**: two workers wrote `internal/vsockrpc`
  against the same contract. Keep the canonical one, move the other under
  its consumer's package, record the debt as a decision (I-37), and let the
  integration session prove they interoperate on the wire.
- **After merging, fast-forward every open worktree** that has no local
  commits, and message the ones that do to merge `main` themselves.
- **Delete merged worktrees and branches**, but first check that no session
  is still running in them: a session whose worktree is removed keeps a
  dead working directory and cannot commit. Kill its pane and relaunch in a
  fresh worktree if it had more to do.
- **The flake's source filter is a merge hazard.** `nix/packages.nix` keeps
  only the paths the Go build reads. A branch that makes `cmd/` import a
  package outside that list (the dashboard's `cmd/fake-logto` importing
  `test/fake-logto`) passes `go build` and fails every `nix build` at the
  vendor step. `nix build .#guestd` is part of the merge check for that
  reason, not only for the hash.
- **Run `go test` with a short `TMPDIR`.** Inside `nix develop` the
  temporary directory is `/tmp/nix-shell.*`, and the tmux and vsock fakes
  put unix sockets under `t.TempDir()`; the socket path limit makes those
  tests fail with "File name too long" and nothing else wrong. Set
  `TMPDIR=/tmp/rt` (or similar) for the test step.
- **A test that passes here and fails on CI is usually the runner's
  PATH.** The dev box has `claude`; the runner does not, so a tmux window
  running it exits at once. Fix the fixture (remain-on-exit, a stub), not
  the assertion, and reproduce first with an exiting shim on PATH.

## Rounds (since 2026-09-23)

- **One conductor session in the main checkout** takes a batch of work
  (the owner's findings from a live session, a triage list, a proposal) and
  splits it into items that touch different files. A round is named in
  STATUS (`round 3`, `16-guest-tooling (conductor-run)`); it has no
  workstream doc.
- **Workers are Agent-tool subagents with `isolation: worktree`**, each on
  its own `worktree-agent-*` branch, all Opus 5.5. The launch prompt names
  the files the worker may touch, its pre-assigned decision numbers, and
  what to report. Workers commit on their branch, never push, never deploy
  and never touch live machines unless the prompt says so.
- **Nix builds share one lock.** Every `nix build` or `nix flake check` a
  worker or the conductor runs goes through
  `flock /mnt/nixstore/repose-ws/nix-build.lock`, so parallel workers queue
  instead of exhausting the box's memory and disk.
- **The conductor merges** each finished branch onto `main`, regenerates
  `docs/DECISIONS-INDEX.md`, and reruns the VM checks the merged change
  touches on the merge itself, not only on the branch.
- **Then it deploys and tests live**: push (Coolify redeploys api and web),
  publish a base or tag a CLI release when the round needs one, and try the
  change end to end on throwaway `e2e-*` projects on host-01, never on the
  owner's projects. At most two test projects alive at once per item.
- **Applies and switches go to the owner.** The conductor's permission
  classifier refuses `tofu apply` and `nixos-rebuild switch` to a host with
  tenant guests on it. The conductor writes the exact command into a tmux
  buffer (`apply`, `switch`) and asks the owner to run it. Announce anything
  that drops live sessions (an edge reboot or `systemctl restart gateway`
  does; an edge switch does not since I-471, except the first one onto
  it).
- **Workers and integration sessions never** force-unlock state, use the
  Coolify UI, or put anything about the owner's personal server in a file.

## The release queue (since 2026-10-03)

Every session, worker or not, now builds in its own worktree by default
and hands its branch over by queueing it (`ops/dev/release-queue add`);
the conductor merges only what is queued, as a release cut from the queue
onto a `release/<id>` worktree, and ships it. Decision ids come from
`ops/dev/release-queue id` instead of a range in the launch prompt.
`docs/ops/RELEASE.md` is the procedure; where it differs from "Merging"
and "Rounds" above, it wins.

## When a worker stalls

- Its STATUS line says `progress` and its pane is idle: message it with the
  exact next step and `notify_when_idle`.
- Its worktree is gone or its branch has diverged from what it thinks:
  kill the pane, create a fresh worktree from `main`, relaunch with the same
  `/ws` and a one-line note of what was already done.
- It reports being denied a permission: never do the action for it in the
  conductor; route it to the owner. (Applies came back to the conductor only
  after the owner said so in words.)

## Infrastructure applies

- The plan is read before every apply; the apply is run with the log
  redirected to a file (`~/.cache/repose-apply-<time>.log`) and a `Monitor`
  tailing the milestones, so the conductor keeps working while it runs.
- `-refresh=false` is used for an immediate retry after a run that was
  refreshed seconds earlier; Azure's storage data-plane refresh has timed
  out more than once.
- A stalled apply is stopped with `kill -TERM $(pgrep -x tofu)` (never a
  pattern that matches the conductor's own shell), and a stale blob lease is
  released with `tofu force-unlock <id>`.
- A VM that booted into an unbootable state is replaced by tainting only
  the VM resource; its NIC and public IP stay.
- Every failure in an install is read from the serial console
  (`az vm boot-diagnostics get-boot-log`) or the installer trace before
  anything is changed. Each one so far was a one-line fix that the docs now
  carry: NVMe-only sizes, `pci-hyperv` in the initrd, the data disk on a
  second controller, disko's globbing, LVM's size units, the register unit's
  flag name.

## Signals the conductor watches

- Idle notices from worker sessions.
- A `Monitor` on `gh run list` for CI conclusions on `main`.
- `Monitor`s on apply logs while an apply runs, stopped when it ends.
- `docs/workstreams/STATUS.md` on each branch for the claimed/progress/done
  line, read with `git show <branch>:docs/workstreams/STATUS.md`.

## What to improve next time

- A `just merge-wave` recipe that runs the merge order and checks, so the
  conductor stops re-deriving them.
- Workers should run `git merge main` at the start of every turn that
  touches shared files; most conflicts came from branches that never did.
- CI needs the same Nix version and a Postgres service from day one; both
  were discovered by a red run after the wave-two merge.
