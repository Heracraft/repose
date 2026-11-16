# Removing `repose fork` (proposal, 2026-11-10)

**Status: the owner decided on 2026-11-10 to remove `repose fork`. This
proposal plans the removal.** It lists every part of fork in the
repository, the order in which the parts go, what an old CLI and a user
who types `repose fork` see during the release of overlap, what the docs
point to instead, and the evidence that closes the work. The decision
needs a DECISIONS entry before any code changes (section 8). No id is
reserved here.

The council voted fork a gimmick, 4 of 4, score 3.9
(`reports/2026-11-04-product-value-council.md`, "Gimmick"): three 8 GB
copies fill a Solo plan right after the demo, the owner does not use it,
and worktrees give parallel attempts inside one machine's memory.

## Summary

| Question | Proposal |
|---|---|
| CLI during the overlap | `repose fork` stays as a hidden command for one release. It prints where to go and exits 2. It is deleted in the next release, named in the entry. |
| api during the overlap | `POST /projects/:id/fork` stays as a route and answers `400 invalid`, `detail.reason: "fork_removed"`, with a message an old CLI prints well. Its code behind the route goes. |
| hostd | The api stops sending `Restore.clone` at once. hostd accepts it for one more release, then drops it; proto field 19 becomes `reserved`. |
| What replaces it in docs | `repose run --worktree -p` for parallel attempts on one machine; `repose run NAME` for a second machine with the laptop's checkout; `snapshots restore ID --as NAME` to copy a machine's disk |
| `snapshots restore --as` | Stays. It is the one remaining way to copy a machine's disk, and the council kept CLI restore as a support path. |
| Data migration | None for projects. Fork copies are ordinary projects. A later migration narrows `snapshots.reason` once no `fork` snapshot is live. `hosts.features` stays, because `pause` uses it (I-818). |
| Evidence | The greps in section 7, `TestDocs`, the docs anchor test, and the live refusal pasted |

## 1. The repo today: every part of fork

**CLI (`internal/cli`).**

| What | Where |
|---|---|
| Command, flags, help text | `cli.go:223` (registration), `cli.go:1751-1800` (`newForkCmd`, `SuggestFor: clone, copy`) |
| Root help group | `help.go:28` ("projects" group lists `fork`) |
| The command | `fork.go` (514 lines): plan and cap checks, `forkWithRetry`, fork snapshot fallback (`needsForkSnapshot`, `fork.go:326-334`), per-copy waits, summary |
| api client | `fork.go:84` (`Client.Fork`), `api_routes.go:379-390` (`CreateForkSnapshot`) |
| Git remotes for copies (I-622) | `git_remote.go:192-219` (`addForkRemotes`); `git_remote.go:266-274` (`forgetForkRemote`), called by `rm` at `lifecycle.go:697`. The same remove also serves the second-machine remote (`addSecondMachineRemote`, `git_remote.go:246-263`), so it stays under a new name. |
| Other fork words | `status.go:246`, `:280` ("creating, restoring, forking and growing a disk"), `cli.go:1643` (`rm` example with `todo-app-fork-1`), `run.go:92-93`, `payment.go:35-36`, `resize_class.go:139`, `project.go:29` (comments) |
| Tests | `fork_test.go` (4 tests), `grammar_test.go:439` `TestForkRemotes`, `git_remote_test.go:328` `TestRmOfSeveralForksRemovesEachRemote`, `plan_check_test.go:90`, `confirm_test.go:590`, `stop_background_test.go:303` |

**api (`internal/api`).**

| What | Where |
|---|---|
| Route | `http/routes.go:57` `POST /v1/projects/{id}/fork` |
| Handler | `http/fork.go` (273 lines): `forkProject`, `forkNames`, `forkedByRequest`; copies named secrets with `Secrets.CopyNamed` (`fork.go:191`; `secrets/secrets.go:639`, used by nothing else) |
| Fork snapshot (I-703) | `http/snapshots.go:106-119` (`reason: "fork"`), `snapshots/expiry.go:53`, `:109-115`, `ops/phases.go:492-494`, `:1344` |
| Clone on the source's host (I-701) | `ops/phases.go:158-205` (`UnfinishedClone`, `fork_of`), `:909-1022` (`Restore.clone`); `scheduler/scheduler.go:210-242` (`FitsOn`, `local_clone`), `:341-348`; `ops/engine.go:345` |
| Messages a user reads | `lifecycle/lifecycle.go:157-160` ("... and fork %s again") |
| Shared code that stays | `http/restore.go:291` `insertRestored` (its `params` argument is fork's only); `http/multiplexer.go:99-102` `copiedMultiplexer` (also restore as new); `http/misc.go:320` (`bootsGuest` lists `fork`) |
| Fakes | `internal/fakes/api/handlers.go` (21 lines name fork), `fake.go:72-76`, `:142`, `store.go:187`; `internal/fakes/hostd/hostd.go:78`, `:350` (`local_clone`) |
| Tests | `http/fork_test.go` (3 tests), `http/multiplexer_test.go:220`, `snapshots/expiry_test.go:164`, `:245`, `secrets/secrets_test.go:469`, fork cases in `abuse_test.go` and `billing_test.go` |

**hostd (`internal/hostd`) and the contract.**

| What | Where |
|---|---|
| Fork point and thin clones | `guest/clone.go` (168 lines), `FeatureLocalClone` at `:24` |
| Restore with `clone` | `guest/snapshot.go:223-228`, `:250-260`, `:303-326`, `:386-387` |
| Locks and sweep | `guest/manager.go:417-418`, `:516`; `guest/reconcile.go:245` (`fp-*` sweep at start) |
| LVM | `lvm/lvm.go:286` `Real.Clone`, `:589` `Fake.Clone`: only `clone.go:100` calls them |
| Proto | `proto/repose/hostd/v1/hostd.proto:130-131` (`local_clone` in Hello), `:249-261` (`RestoreClone clone = 19`) |
| Tests | `guest/clone_test.go` (9 tests), `guest/clone_host_test.go`, `lvm/lvm_test.go:61`, `:84` |

**Database.** No fork column on `projects` (fork has no lineage,
`docs/features/snapshots.md:166`). Copies are `restore` ops with
`fork_of`, `fork_request_id` and clone fields in `ops.params`
(`http/fork.go:153`, `:255`). Migration `0025_fork_clone` added
`hosts.features` and the `fork` value of `snapshots_reason_check`
(`internal/db/migrations/0025_fork_clone.up.sql`).

**Dashboard (`apps/web`).** No fork action exists
(`docs/features/snapshots.md:166`, "no dashboard action yet"). Fork words
appear in the landing FAQ, "Can three agents try three approaches at
once?" (`apps/web/src/routes/+page.svelte:322-331`), and the billing page
(`routes/billing/+page.svelte:582`). The `disk_over_plan` email says
"Creating, restoring and forking projects"
(`internal/api/notify/templates/kinds/disk_over_plan.tmpl:2`, golden
files in `internal/api/notify/testdata/disk_over_plan.{txt,html}`).

**Public docs (`apps/web/src/content/docs`).** `lifecycle.md:271-300`
("Fork a project"), `:155` (a failed copy), `:321` (`restoring` state
row); `cli.md:337-354` (`repose fork [PROJECT]`), `:17` (suggested
words); `limits.md:16`, `:18`, `:22`; `tutorial-git-workflow.md:113`;
`tutorial-conductor.md:68`. The guest agent guide
(`nix/guest/base/agent-guide.md`) never names fork.

**Internal docs.** `docs/features/snapshots.md:81`, `:120`, `:122-167`
("Forking"); `docs/features/projects.md:67`, `:115-120`;
`docs/features/README.md:18`; `docs/interfaces/api.md:22-24`, `:176`,
`:188`, `:252-255`, `:364`, `:371`; `docs/interfaces/grpc-hostd.md:200-212`;
`docs/interfaces/db-schema.md:64`, `:108`;
`docs/interfaces/guest-conventions.md:192`; `docs/PRICING.md:79-81`;
`docs/DESIGN.md:136`; `docs/SECURITY.md:401`; `docs/LANDING.md:128`;
`docs/workstreams/07-cli.md:122`. Guest VM test:
`nix/guest/tests/default.nix:1316` (a fork's copy reopens no windows; the
same holds for `restore --as`).

**DECISIONS that built fork.** I-254 (the command), I-255 (the copy's
checkout), I-701 (clones on the source's host), I-702 (timed lines),
I-703 (the fork snapshot), I-610 (plan memory before a fork,
`--no-start`), I-622 and I-631 (fork remotes, `rm` removes them), I-633
(interrupt lines), I-730 (waiting out a background stop), I-736 (fork
term in placement), I-433 and I-474 (secrets copied to a fork), I-549
(herdr gate on a fork or restore as new), I-569 and I-585 (cap and disk
for N copies), I-460 (a held project cannot be forked), I-630 (`clone`
and `copy` suggest `fork`), I-646 (a copy reopens no windows), I-293 and
I-300 (fork in the limit checks).

## 2. What an old client sees, and the "one release" rule

CLAUDE.md: "A change to any file in `docs/interfaces/` ships in the same
commit as the code that changes the contract, and the old shape stays
accepted for one release." A removed endpoint has no new shape to move
to, so this proposal reads the rule as: for one release, the old request
still reaches a route that answers it with a documented, deliberate
refusal.

**The api refusal.** `POST /projects/:id/fork` answers:

```
400 {"error": {"code": "invalid",
  "message": "repose fork was removed; `repose run --worktree -p PROMPT` runs another agent on its own branch on the same machine",
  "detail": {"reason": "fork_removed"}}}
```

Three facts about released CLIs (v0.1.43, v0.1.44) shape this:

- An old CLI prints `invalid` as `Could not fork todo-app: <message>.
  Nothing was created.` and exits 1 (`internal/cli/fork.go:229-230`). The
  message must read as a clause after the colon and end without a full
  stop.
- An old CLI falls back to taking a fork snapshot on `409 conflict` with
  `reason: snapshot_needed`, or on an `invalid` whose message contains
  "snapshot_id is required" (`fork.go:326-334`). The refusal must match
  neither, or each old `repose fork` would take a snapshot for nothing.
- An old CLI checks the project cap and plan memory before it posts
  (`fork.go:121-170`). A user with a full plan gets the memory refusal
  first, which is harmless.

A new error code (`gone`, 410) was considered. `api.md` keeps one list
of codes, and an old CLI prints an unknown code through its generic path
with no message of its own, so `invalid` with a reason reads better.

**The CLI stub.** For one release `repose fork` is a hidden command with
`DisableFlagParsing`, so any old flags reach it, and it prints:

```
$ repose fork todo-app -n 3
repose fork was removed in v0.1.NN. To try another approach on the same
machine, run `repose run --worktree -p "..."`. For a second machine with
this checkout, run `repose run todo-app-2`.
```

It exits 2 (wrong usage). `docs_test.go` lists it in `hiddenCommands`
with the reason "removed in v0.1.NN; prints where to go"
(`internal/cli/docs_test.go:34-46`), so `cli.md` does not have to name it.
The release that deletes the stub is named in the DECISIONS entry and
added to the release checklist. I-888 records why: I-603's "for one
release" form was still there eight releases later.

**How long the api refusal stays.** No `repose update` exists, so old
CLIs stay on laptops for months. The refusal route is about 15 lines.
Keeping it until the version-skew warning ships (council fix item) costs
little and keeps old clients from a bare 404. This is decision 3.

## 3. What the docs say instead

| Fork's use | Replacement | Where it is documented |
|---|---|---|
| N agents try N approaches from one state | `repose run --worktree -p "..."` per approach: each agent gets its own tree and `worktree-N` branch on the same machine, in the plan memory already held | `run-and-attach.md` "Several agents, separate trees" |
| A second machine on the same repository | `repose run NAME`: a new machine that syncs the laptop's checkout, history and uncommitted work, with a git remote named after it | `lifecycle.md` "A second machine for the same repository" |
| A throwaway second machine | `repose run --temp` | `lifecycle.md` "Temporary machines" |
| A copy of the machine's own disk (installed dependencies, Docker images, the machine's uncommitted work) | `repose snapshots restore SNAPSHOT_ID --as NAME`, from the stop snapshot or the nightly one | `lifecycle.md` "Snapshots" |

`repose run --on PROJECT` is no replacement: it adds another folder to
an existing machine, beside its checkout
(`run-and-attach.md` "Several repositories on one machine").

The last row is slower than a fork's clone: a restore goes through Blob,
and the measured fork of kanali before I-701 took 3m57s. If the council's
advice to remove manual `snapshots create` is also taken, a copy of the
disk as it is now needs `repose stop` first, so that the stop snapshot
holds the current state. This is decision 4.

Pages to change: `lifecycle.md` (remove "Fork a project"; the
`restoring` row says "A snapshot is being written to the disk"; drop the
fork sentence at `:155`), `cli.md` (remove the section; drop `clone` and
`copy` from the suggested words at `:17` unless they point elsewhere),
`limits.md` (three lines), `tutorial-git-workflow.md:113` (risky
experiments: a `--worktree` agent, or a snapshot first),
`tutorial-conductor.md:68` (a second front: `repose run NAME`), the
landing FAQ (remove the question, or answer it with worktrees), the
billing page and the `disk_over_plan` email (drop "forking").

## 4. Order of removal

Each step is its own branch through the release queue.

**Step 1: refuse and document (release R).**

- DECISIONS entry (section 8).
- api: the route's handler becomes the refusal. `lifecycle.go:157-160`
  stops telling a failed clone copy to "fork %s again" and says to
  remove it, since such a copy has no snapshot of its own.
- CLI: `fork.go` and `newForkCmd` replaced by the stub; `help.go:28`
  drops `fork`; `SuggestFor` `clone` and `copy` removed or pointed at
  another command; the `rm` example at `cli.go:1643` uses other names;
  `status.go:246`, `:280` drop "forking"; `forgetForkRemote` becomes
  `forgetMachineRemote`, since `rm` still removes the remote of a second
  machine and of any fork copy a user still has; `addForkRemotes` and its
  test go.
- Docs, landing, billing page and email template in the same commit
  (I-242), and the interface docs: `api.md` marks the route removed with
  its refusal; `grpc-hostd.md` marks `clone` as no longer sent and still
  accepted until release R+1.

**Step 2: remove the api's fork code (release R, a deploy after step 1).**

Before it ships, a read-only query shows no fork restore in flight:
`select count(*) from ops where kind = 'restore' and state in
('pending','running') and params ? 'fork_of'` returns 0. Then delete
`forkNames`, `forkedByRequest`, `MaxForks`, `Secrets.CopyNamed`, the clone
term in `scheduler.FitsOn` and its I-736 vCPU check for forks, the clone
paths in `ops/phases.go`, `UnfinishedClone`, the `params` argument of
`insertRestored`, `fork` in `bootsGuest`, the fork parts of both fakes,
and their tests.

**Step 3: hostd (release R+1).**

Delete `clone.go`, the `clone` branches in `snapshot.go`, the fork
locks in `manager.go`, `lvm.Clone`, and `local_clone` from `Features`.
Mark `RestoreClone` and field 19 `reserved` in `hostd.proto`, regenerate
`internal/gen`. The `fp-*` sweep in `reconcile.go:245` stays for this
release, so a host that held a point when it upgraded still removes it at
start; it goes in R+2. The api stopped reading `local_clone` in step 2, so
a host that still reports it does no harm.

**Step 4: data and the stub (release R+1).**

- `fork` snapshots expire a day after they are taken (I-703), so a day
  after step 1 no live one exists. A new migration sets any remaining
  `reason = 'fork'` row to `manual`, as `0025`'s down file does, and
  narrows `snapshots_reason_check` to `scheduled, stop, manual`. The
  `fork` branches in `snapshots/expiry.go`, `ops/phases.go:492-494` and
  `:1344`, and the `{reason: "fork"}` body of `POST
  /projects/:id/snapshots` go with it; that body is refused as `invalid`
  after it, which only an old CLI's fork fallback ever sent.
- `hosts.features` stays: the idle pause reads `pause` from it (I-818).
- The CLI stub is deleted, and `hiddenCommands` loses its line.
- The api refusal route stays or goes per decision 3.

Fork copies that users already have are ordinary projects named
`X-fork-N`. They keep working, and `repose rm` removes each one and its
remote. Nothing migrates them.

## 5. Things that stay

- `repose snapshots restore ID --as NAME`, `repose restore --as`, and
  the dashboard's **Restore as new**, with I-549 (herdr gate on a restore
  as new), I-646 (a copy reopens no windows) and I-255's checkout rule
  for a restore under another name.
- The second-machine remote and its removal by `rm` (I-633).
- `hosts.features` and the Hello feature list (I-818).
- The nix test at `nix/guest/tests/default.nix:1316`, renamed to say
  "a restore under another name".

## 6. Risks

- **A user who relied on fork for a disk copy** loses a fast path. The
  `restore --as` path works and is slower. The release note names it.
- **Links into `/docs/lifecycle#fork-a-project`** from outside (posts,
  issues) land on the page top. Inside the docs, the anchor test catches
  them.
- **A stub kept too long.** The entry names the release that deletes it.

## 7. Evidence that closes it

Paste each in the commit or the queue entry:

1. `go test ./internal/cli -run TestDocs` passes (CHECKLIST, "A
   user-visible change"): `cli.md` and the CLI agree, and the stub is in
   `hiddenCommands`.
2. `pnpm --filter web exec vitest run src/lib/docs.test.ts` passes: no
   docs page links to `#fork-a-project`.
3. `git show --stat` of step 1 includes `cli.md`, `lifecycle.md`,
   `limits.md`, both tutorials, `+page.svelte`, the billing page, the
   email template and its golden files.
4. After step 2, this prints nothing outside `docs/DECISIONS.md`,
   `docs/proposals/`, `docs/releases/`, `docs/workstreams/STATUS.md` and
   the stub with its test:

   ```
   rg -n -i 'repose fork|forkProject|ForkCmd|ForkRequest|ForkedProject|fork_of|fork_request_id|CopyNamed|addForkRemotes|forgetForkRemote|fork-a-project|MaxForks|-fork-[0-9]' \
     internal cmd apps/web/src docs nix
   ```
5. After step 3, this prints nothing outside those same paths:

   ```
   rg -n 'local_clone|RestoreClone|FeatureLocalClone|ForkPoint|forkLock|releaseForkPoint|cloneVolume' internal proto docs/interfaces
   ```
6. After step 4: `rg -n "'fork'|\"fork\"" internal/api internal/db
   docs/interfaces` prints only the new migration and its down file.
7. CHECKLIST: `rg 'TODO|FIXME|XXX|not implemented'` over the touched
   paths is empty or each hit cites a DECISIONS entry.
8. Live, after the deploy of step 1: `repose fork` from the released CLI
   prints the stub and exits 2; the previous release's CLI prints `Could
   not fork X: repose fork was removed; ...` and takes no snapshot
   (`repose snapshots list X` shows none new); `repose --help` has no
   `fork`.
9. After step 3, each host's last Hello has no `local_clone`:
   `select name, features from hosts` (read-only).

## 8. The DECISIONS entry

One entry under "Made during implementation", reserved with
`ops/dev/release-queue id` by the session that builds step 1. It:

- **Supersedes** I-254, I-701, I-702 and I-703.
- **Amends** I-255 (the fork half; restore under another name keeps
  it), I-610 (drops the fork half and `fork --no-start`), I-622 and
  I-631 (fork remotes; `rm` keeps removing a machine's remote), I-633
  (fork interrupt lines), I-730 (the fork wait), I-736 (the fork term in
  placement), I-433 and I-474 (`CopyNamed`), I-549 and I-646 (restore as
  new only), I-569 and I-585 (no N-copy check), I-460 (a held project
  cannot be restored as new), I-630 (`clone` and `copy` suggestions), and
  I-293 and I-300 where they name fork.
- **Records** the reading of "one release" for a removed endpoint and a
  removed command (section 2), the release that deletes the stub, and the
  `fork_removed` reason in `api.md`.
- **Cites** the council report and the owner's decision of 2026-11-10.

## Decisions for the owner

1. **The stub's text and exit code.** Exit 2, with the worktree and
   second-machine lines, as drafted.
2. **The api refusal's shape.** `400 invalid` with `reason:
   fork_removed`, as drafted, or a new `gone` code.
3. **How long the api refusal stays.** One release, as the rule says,
   or until the version-skew warning ships.
4. **Manual `snapshots create`.** If the council's removal of it goes
   ahead too, `stop` then `restore --as` is the only disk copy. Keep CLI
   `snapshots create` while fork goes, or accept that.
5. **The landing FAQ question.** Remove it, or answer it with
   `--worktree`.
6. **`clone` and `copy` as typed commands.** Suggest nothing, or point
   `copy` at `cp`.
