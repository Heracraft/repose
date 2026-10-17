# The release queue: the plan (2026-10-16)

What to build, in what order, with the research that settled each choice.
[queue-revamp.md](queue-revamp.md) is the problem statement — ten problems
with their evidence. This is the answer to "come up with a plan", and it
exists separately because four of the proposal's design choices changed once
they were measured rather than assumed.

Nothing here is built yet. `ops/dev/closure-equiv.py` is in the tree because
§1's evidence is not checkable without it.

## What the research changed

| The proposal said | Measured | Now |
|---|---|---|
| exact target resolution "needs minutes" | **3 s** a side, at any revision | it goes in, as the answer behind a path-based gate (§1) |
| moving `checks` out of `flake.nix` is the fix for over-reporting | evaluation sees through a checks-only change whatever the paths say | the move is optional cleanliness, not correctness (§1) |
| the `_test.go` rule and exact evaluation overlap | the Go source filter includes test files, so nix reports a change the binary would not have | both are needed, for different reasons (§1) |
| a custom ref on GitHub should work | dry run accepted: `* [new reference] … -> refs/repose/probe` | the ledger is viable; still the owner's call (§4) |

And one measurement that is the best single argument in either document.
`dev-tool-guards` (I-745, `6950361a`) was queued as shipping
**`base,edge,host`** — a base publish, a switch of the edge and a switch of
every host, two of them the owner's own action. All three systems are
byte-identical:

```
$ ops/dev/closure-equiv.py nixosConfigurations.host-01.config.system.build.toplevel 6950361a 6950361a~1
VERDICT: the same system; only the version stamp differs
$ ops/dev/closure-equiv.py nixosConfigurations.edge.config.system.build.toplevel 6950361a 6950361a~1
VERDICT: the same system; only the version stamp differs
$ ops/dev/closure-equiv.py packages.x86_64-linux.guest-system 6950361a 6950361a~1
VERDICT: the same system; only the version stamp differs
```

---

## 1. Exact targets: what the measurements were, and the design that follows

### Why the obvious thing does not work

`self.shortRev` is stamped into hostd's and guestd's version and into the
guest's `baseVersion`, so **every commit changes the drvPath of every
system**. A plain comparison always answers "changed":

```
                        rev 04e66056   rev 293a8dba (its merge-base)
host-01 toplevel        66a18mh985bv   5cankvpinpn2     differ
guest-system            1dvllrhvynwy   cdqycg0dph1f     differ
repose (the CLI)        wyfjjf9d7xks   8yj8bzsi9c14     differ
```

The branch between those two revisions adds one research document and one
test script. The filtered Go source is identical across them
(`g23qpl0fqnc9sivsim2m6yk5p2f306q0-repose-go-src` both sides — the filter in
`nix/packages.nix` is correct and tight); only `version` differs, `04e6605`
against `293a8db`.

### What does work

Compare the derivation **graphs** modulo the stamp. For host-01 across those
two revisions: 4,844 derivations a side, 4,826 byte-identical, and all 18
that differ are the stamp and what is downstream of it — 17 share a name and
0 have a different name.

`ops/dev/closure-equiv.py` does this properly: a structural hash per
derivation over the builder, args, system, output names, sources and its
formal inputs' hashes, with the stamp replaced and every store path that
something in the closure produces replaced by that producer's name.

Validated on four cases, and it took four bugs to get there, each found by
diffing rather than by reasoning:

| Case | Expected | Result |
|---|---|---|
| docs-only branch vs its merge-base | same | same |
| `dev-tool-guards`, flake `checks` only | same | same, on all three targets |
| a real hostd change (`864fd2f8`) | differs | differs: **17 of 4,399**, minimal cause `repose-proto-go.drv` |
| a closure against itself | same | same |

The bugs are recorded in the script's comments because each is a trap for
the next person: the stamp was replaced before the store-path lookup, so no
producer ever matched; the store-path regex swallowed the path *inside* the
package, so a unit file naming `…/hostd-@STAMP@/bin/hostd` matched nothing;
fixed-output derivations carry no `path` under `outputs`, only `hash`, so
their store path (in `env`) was invisible and every vendored Go module set
diverged; and resolving store paths found in text by the producer's *hash*
made the answer depend on which node the walk started from — the same pair
reported 18 changed bottom-up and 3,975 from the root. Paths in text are now
resolved by producer *name*, and discrimination comes from the formal input
graph, which is acyclic.

### The design

**Paths decide whether to ask; evaluation answers.** The path rules stay as
the first gate, because they are instant and most branches touch no nix at
all. When they say `base`, `host` or `edge`, `add` evaluates that target on
both sides and keeps it only if the system really changes.

```
needs_of:
  paths  -> candidate targets                     (instant, as today)
  go list -deps, minus *_test.go-only packages    (instant)
  for each of base, host, edge in the candidates:
      closure-equiv <target> <merge-base> <HEAD>  (~6 s each)
      drop the target when the systems match
```

Cost: nothing for a branch that touches no nix; about 6 s per nix target
otherwise, and at most ~20 s for a branch that touches all three. A branch
that reaches a host switch can afford six seconds to find out whether it
does.

Two rules that do **not** overlap and are both needed:

- *`_test.go` only.* Nix is conservative about inputs: the Go source filter
  includes test files, so a test-only commit changes `repose-go-src`
  (`gyflb8fxm3v7…` against `mbd27888ivq8…` across `d8298941`) and every
  derivation that reads it, even though `go build` ignores the file. Only
  `go list -deps` plus "a package whose changed files are all `_test.go`
  contributes nothing" gets this right.
- *Evaluation.* Only this gets a flake `checks` change, a VM test, or a
  `nix/guest` edit that happens not to alter the built system.

**`--exact` / `--fast`.** `add --fast` skips the evaluation and says in the
entry that it did, for a lane working offline or on a box without the nix
store. `release-queue needs --exact <target>` runs the comparison alone, so
the conductor can ask the question before a switch — which is the check
r20261015-2 made by hand when it skipped a base publish because "nothing in
cmd/guestd … changed but two comments".

**What moves out of scope.** Extracting `checks` from `flake.nix` (482 of its
707 lines) is now a readability change, not a correctness one. Worth doing;
not worth doing first, and not worth the merge risk inside this work. It is
dropped from the plan and noted in the proposal.

---

## 2. The plan, in order

Five batches. Each is a branch, each closes the `KNOWN_BAD` lines it names,
and each ships with its `RELEASE.md` / `ORCHESTRATION.md` update and a
`DECISIONS.md` entry under "Made during implementation" amending I-416 and
I-424 where it changes what they describe. Decision ids are **not reserved
yet**: a reservation that is never written is the dead-entry problem this
work is about, so each batch reserves its own when it starts, from a
worktree that has fetched `origin/integrated`.

### Batch A — the entry store (proposal §10)

The only item that can silently lose a branch. Three defects, one root.

- `add` refuses a `--note`/`--live` value containing a newline, or flattens
  it and says so. Flatten is the default.
- `field()` reads the **last** match, not the first, so the fields the tool
  writes after the free text win over anything inside it.
- `mktemp` and `$f.new` move to `entries/tmp/`; every reader keeps globbing
  `entries/*` and no reader learns a name filter. (Both temp names sit at
  lines 37 and 152 of the same script and look nothing alike; a filter would
  need to know both and would rot when a third is added.)
- `key()` becomes a reversible encoding: `%` → `%25`, `/` → `%2F`. On
  startup, under the lock, an entry whose filename does not match its own
  `branch=` is renamed; idempotent, and it is one file on kanali
  (`repose%fider`) and none on woker-1.

Closes `add_rejects_a_multiline_note`, `add_note_cannot_forge_a_field`,
`stray_temp_entry_is_not_a_branch`, `two_branches_keep_two_entries`.
Files: `ops/dev/release-queue` only. No owner decision.

### Batch B — the base, and what a branch really ships as (§1, §2)

- `integration_tip`: the newest of `merge-base(HEAD, ref)` over
  `refs/heads/main`, `refs/remotes/origin/integrated`,
  `refs/remotes/origin/main`. `needs`, the commit count and the conflict
  check all read it; the conflict check uses the tip itself, not the
  merge-base, because that is the tree `cut` merges into.
- `cut` refuses when `refs/heads/main` is behind the tip, naming the
  fast-forward.
- `ls`'s unreleased count reads the tip.
- `needs_of` skips packages whose changed files are all `_test.go`, and the
  gate paths (`nix/guest/tests/`, `nix/hosts/tests/`, `nix/guest/checks.nix`)
  ship as nothing.
- `add` runs `closure-equiv` for each of `base`, `host`, `edge` it is about
  to claim, and drops the ones whose system does not change. `--fast` opts
  out and records that it did.
- `ops/dev/closure-equiv.py` gains its own test against two fixture
  revisions, so the four bugs above stay fixed.

Edge cases to pin as tests, all reachable today: a branch cut from local
main (kanali), from `origin/integrated` (woker-1), from `origin/main`
(`docs-prose`); a branch that has merged main; **local main ahead of
`origin/integrated`**, which is the security-fix case where
`REPOSE_INTEGRATED_REMOTE=none` holds the push back and local main is the
newest ref — the tip rule must prefer it; and no merge-base at all, which
must refuse loudly rather than compute against nothing.

Closes `needs_against_stale_local_main`, `needs_flake_checks_only`,
`needs_ignores_a_test_only_change`. No owner decision.

### Batch C — answering where a branch is (§3, §6)

- `where <branch>`: the local entry, else the merge subject on
  `origin/integrated` (`Merge <branch> (<release>)`), else the branch's own
  ref against `origin/main` and `origin/integrated`, else "nothing here
  knows this name".
- `worktrees`: lists worktrees whose branch is wholly on `origin/main`,
  explains them, deletes nothing.
- entries record `machine=`; `ls` names whose queue it is reading, and marks
  a queued branch whose commit this machine does not have.
- `RELEASE.md` says in words that the queue state is one machine's and that
  `where` is the cross-machine question.

Closes `where_is_released_branch`, `where_is_branch_on_the_other_box`.
No owner decision.

### Batch D — the script's own age (§5)

One line on stderr when the running file differs from
`origin/integrated:ops/dev/release-queue`; silent when the branch itself
changes the tool, and when `origin/integrated` is not fetched.

Closes `stale_script_warns`, keeps `author_script_no_warning` passing.
**The other half is yours and is not code:** I-745's guard is on
`origin/integrated` and on no `main`, and `origin/main` has no `ids_in_use`
at all, so a worktree cut the documented way on this box gets the superseded
script every time. The warning tells a lane; only main moving fixes it.

### Batch E — the ledger, the record, the channel, the certificate

Each item here either needs a decision or is independent of the rest.

- **The id ledger (§4).** The offline band — woker-1 even, kanali odd — lands
  first and needs no decision; it makes a collision impossible with no ref
  and no network. `refs/repose/ids` with a compare-and-swap push lands only
  if you say yes. GitHub accepts the namespace (dry run above), no workflow
  triggers on it, and the ledger carries the number, the date and the
  machine and **not** the branch name. The counter-argument is I-651's, in
  its own words — *"A branch, not a custom ref: it can be fetched by name
  and seen on GitHub"* — and it applies less here, because the ledger is
  machine state a human never reads and a branch would mean a commit per
  reservation.
- **The record (§7).** `live <branch> --ran/--not-done`, and
  `record <release>` printing the line from stored facts: branches with
  their ids and targets, the main commit, shipped versions, live evidence,
  and what is not done with why. `done` lists released branches whose
  `--live` check has no result yet. **Your call:** the 600-character STATUS
  line with the evidence in `docs/releases/<id>.md`, or the long line as
  today. The stored facts support either; I would do the first, because the
  five release lines at 2,142 to 3,989 characters break the rule that file's
  own header states.
- **The channel (§8).** `say <branch>` and `inbox`, per machine.
- **The certificate (§9).** `operator-cert.sh --if-stale`, and
  `/tmp/claude-1000/kx` committed as `ops/dev/guest-exec`. The gateway
  question — whether an expired certificate from a source that has
  authenticated before should count towards the ban — stays yours, and
  nothing here waits on it.

---

## 3. What this does not do

- It does not move the queue's state out of the common git dir. Batch E adds
  one ref beside it if you approve; nothing moves.
- It does not change what a release *does*. The push, the tag, the base
  publish and the switches stay where `RELEASE.md` has them, and host and
  edge switches stay yours.
- It does not touch the five branches queued on this box, or the queue state
  on either machine, beyond Batch A's rename of an entry whose filename
  disagrees with its own contents.
- It does not extract `checks` from `flake.nix`, for the reason in §1.

## 4. What is left open

1. `refs/repose/ids` on the public repository: yes, or band-only.
2. The release record's shape: short line plus a per-release file, or the
   long line.
3. The gateway's ban on an expired certificate: leave it, or soften it.
4. Whether a fix to `ops/dev/release-queue` or `ops/dev/decisions-index.py`
   should reach `main` in the next release rather than wait for the queue to
   fill. Everything in Batch D is cosmetic until this is answered.

Batches A to D need none of these.
