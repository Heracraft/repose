# The release queue and the process around it: what to change (2026-10-15)

A proposal, for the owner to read before anything in `ops/dev/release-queue`
changes. The queue works; the process around it leaks, and one night cost
six double-taken decision ids, two agents reporting a released branch as
unqueued, and a queue line telling the conductor that a comment in a test
script needed two host switches. It can also lose a branch between `add`
and the cut while reporting success, which nobody had noticed (problem 10).

Every number below was measured on woker-1 today, from the integrated main
`293a8dba`. Ten problems: the brief's nine, and one more the cpu-weight
lane found while queueing. Five of the field reports — in problems 2, 3, 4,
6 and 10 — came from the cpu-weight and docs lanes as this was written and
are marked where they are used; each was re-measured here before it went
in, and one of them corrected a claim in problem 4 that was too strong.
Each problem says what it does today, what it should do, what the change
costs, and what was rejected.

The tool had no tests, which is why each of these was argued about rather
than run. The first thing built was the fixture: `ops/dev/release-queue-test`
("The fixture" below). Twelve of its tests fail today — one for each of the
brief's problems 1 to 7, four for problem 10 and one for the test-file half
of problem 2 — and each is listed in the script's `KNOWN_BAD` with the
problem it belongs to. That list is the acceptance criterion for this work:
a fix lands when its line comes out of `KNOWN_BAD` and the suite still
exits 0.

## Summary

Ordered by what fixing it saves, so the numbers here are not the brief's;
the brief's number is in the second column, and the fixture's `KNOWN_BAD`
uses the brief's. Problem 10 is not in the brief: it was found by the
cpu-weight lane while queueing, after this document was first written.

| # | brief | Problem | Fix | Owner's call |
|---|---|---|---|---|
| 1 | 5 | `needs` and the commit count are read against `refs/heads/main` | compute against the integration tip; refuse a stale `cut` | no, a bug |
| 2 | 4 | path-derived `needs` charges a deploy for a gate | gates install nothing; move `checks` out of `flake.nix` | no, a bug |
| 3 | 3 | a released branch has no answer, and a finished worktree reads as work | `where`, `worktrees` | no, new read-only commands |
| 4 | 1 | ids collide across the two machines | a shared id ledger on a git ref, pushed to `origin` | **yes**: a new ref on a public remote |
| 5 | 6 | a stale copy of the script is silent | the script checks its own age against `origin/integrated` | no |
| 6 | 2 | queue state is per machine and nothing says so | `where` answers from refs; entries record their machine | no |
| 7 | 7 | live checks and the release record are written by hand | `live`, and `record` prints the STATUS line | no |
| 8 | 8 | reaching the other lanes | `say` and `inbox` per branch | no |
| 9 | 9 | the gateway bans the conductor | renew the operator certificate before use; commit the vsock route | partly: the gateway is yours |
| 10 | — | the entry store has no schema: free text, first-match reads, sibling temp files, a lossy filename | refuse a newline, read the last match, move temp files out, encode the filename | no, three bugs |

Problems 1, 2, 3, 5 are bug fixes and new read-only commands; they can land
without a decision from you beyond "yes, go". Problem 4 asks for a new ref
on the public GitHub repository and is the one real question. Problems 7 and
8 add state the tool keeps; they change the conductor's procedure, so
`RELEASE.md` moves with them.

Two of them are not waiting for a release night to bite. Problem 1 misreads
the targets of **every** `add` run on this box, including a branch with
nothing in it; three of the five branches queued here now carry a note
correcting their own `ships as` field, which is a field that has stopped
being one. And as of this afternoon **I-747, I-748 and I-749 are
reserved on woker-1 and visible to nothing on kanali**: not a collision yet
— one lane holds all three — but a three-number-wide window in which a lane
on kanali that runs `id` is handed one of them (§4). The offline band in
problem 4 closes that without the new ref, and can land on its own.

---

## 1. `needs` and the commit count are read against `refs/heads/main` (brief 5)

**What happens.** `cmd_add` computes everything from the local `main` ref:
`needs_of main...HEAD`, `git rev-list --count main..HEAD`, and the conflict
check `git merge-tree main HEAD`. On woker-1 `refs/heads/main` is `a8865ad8`,
**132 commits behind `origin/main` and 139 behind `origin/integrated`**. The
main checkout is the conductor's and nobody has fast-forwarded it since the
work moved to kanali.

Reproduced on this branch, which has no commits of its own:

```
$ git rev-list --count main..refs/remotes/origin/main
132
$ needs_of main...HEAD                      # what add would print
api,base,cli,edge,host,web
$ git diff --name-only main...HEAD | wc -l
371
$ needs_of refs/remotes/origin/integrated...HEAD    # the truth
none
```

A branch with nothing in it would have been queued as shipping the whole
product: a push, a CLI tag, a base publish, a switch of every host and a
switch of the edge.

The docs lane then reproduced it on real work, which is the better evidence:
`docs-prose` changes thirteen files — eight docs pages, three under `docs/`,
the agent guide, and one `_test.go` — and `add` recorded
`needs=api,base,cli,edge,host,web` over a 371-file diff. Verified here:

```
$ git diff --name-only $(git merge-base docs-prose origin/main)..docs-prose | wc -l
13
$ git diff --name-only main...docs-prose | wc -l
371
$ needs_of main...docs-prose
api,base,cli,edge,host,web
$ needs_of <merge-base with origin/main>...docs-prose
base,cli,web
```

A conductor reading that entry plans a host switch and an edge switch to
ship four documentation pages.

**The dots are not the bug.** The lane's reading was that `...` takes a
symmetric difference and so attributes other lanes' files to this branch.
It does not: in `git diff`, `A...B` is exactly `git diff $(git merge-base A
B) B`, and the two spellings are byte-identical here —

```
$ git diff --name-only main...docs-prose          | md5sum   a75e10b8ca34
$ git diff --name-only $(git merge-base main docs-prose) docs-prose | md5sum   a75e10b8ca34
$ git rev-parse --short $(git merge-base main docs-prose)
a8865ad8                                            # local main itself
```

The inflation is that the merge-base **is** the stale local main, because
local main is an ancestor of every branch cut from anything newer. So
changing `...` to `..` would change nothing, and a fix written against the
symmetric-difference reading would fix nothing. The base is the bug, which
is what the integration tip below addresses.

**Lanes have started routing around the field in prose.** `staging-host`,
queued on 2026-10-11, carries the workaround in its own note: *"The computed
'needs' is wrong because this worktree's local main is 109 behind
origin/main."* `cpu-weight`'s note says the same of its merge base, and
`docs-prose`'s now spells out the whole mechanism and its true target set
beside the wrong line. **Three of the five branches queued on this box
carry a note correcting their own `ships as` field.**

That is the argument for fixing this first, and it does not need the reader
to follow merge-base semantics to feel it: a field that lanes annotate
rather than trust has stopped being a field. The conductor's own procedure
reads it — `RELEASE.md` step 4 keys the verification list to what the batch
ships as, and step 3 keys the owner's release summary to it — so the prose
in the note is load-bearing and the structured field is decoration. (The
framing is the docs lane's.)

It is worse than noise in both directions. A branch cut from
`origin/integrated` is charged for everything integrated since the stale
`main`; and once the conductor stops believing the line, a branch that
really does ship `host` reads like more of the same.

**What it should do.** The base is the newest integrated commit the branch
was cut from, not whatever `refs/heads/main` points at. Take the merge-base
of `HEAD` with each of `refs/heads/main`, `refs/remotes/origin/integrated`
and `refs/remotes/origin/main`, and keep the newest — the one the others are
ancestors of. On kanali that is the local `main`, which is integrated main;
on woker-1 it is `origin/integrated`. Measured here:

```
integration tip: 293a8dba (Merge dev-tool-guards (r20261015-4))
files in the diff: 0        needs: none        commits ahead: 1
```

Three consequences:

- `needs` and the commit count read `<tip>...HEAD`.
- The conflict check runs against the integration **tip itself**, not the
  merge-base: that is the tree `cut` will merge into.
- `cut` refuses when `refs/heads/main` is behind the tip, naming the
  fast-forward. A cut from a 132-commit-stale main would re-merge four weeks
  of main through `--no-ff` merges, which is how a release loses a change
  silently. Nothing stops that today.
- `ls`'s last line (`git rev-list --count refs/remotes/origin/main..main`)
  prints `0 commits` on woker-1 while 132 are unreleased. It reads the tip
  as well.

**One stale ref, three wrong answers.** The docs lane's framing, and it is
the reason this problem is first: `id` reads local main's branches, the
worktree's script copy comes from local main, and `needs` is computed
against local main. On the box where main is stale all three are wrong at
once — and they fail differently. The id collision is loud when it bites
(two entries with one number, and `decisions-index.py --check` now catches
it). The stale script is silent but harmless until it hands out a used
number. The target set is silent and lands in the conductor's plan, where
the next thing that reads it is a host switch.

**Cost.** About twenty lines, no new state, no network beyond the `git fetch`
agents already run before `id`. Two fixture tests
(`needs_against_stale_local_main`, plus a new one for the cut refusal).

**Rejected.** Making `add` refuse until the local `main` is fast-forwarded:
it would stop every lane on woker-1 on a ref only the conductor may move,
and the branch's base is knowable without it. Reading `main` from the
conductor's machine over the network: `add` must work while the ssh gateway
has banned the box (problem 9).

---

## 2. Path-derived `needs` charges a deploy for a gate (brief 4)

**What happens.** `needs_of` maps paths to targets, so every file under
`nix/guest/` means a base publish, every file under `nix/hosts/` a switch of
every host, and any change to `nix/flake.nix` means `base`, `host` and
`edge` together. The flake's VM tests and `checks` live under exactly those
paths and are installed on nothing: they are evaluated by a gate and thrown
away.

`dev-tool-guards` (`6950361a`) is the case from the brief. Its only
non-documentation change is a comment and an `awk` script inside
`checks.guest-closure-size`:

```
$ git show --name-only --format= 6950361a
docs/DECISIONS-INDEX.md  docs/DECISIONS.md  docs/ops/RELEASE.md
nix/flake.nix  ops/dev/decisions-index.py  ops/dev/release-queue
$ needs_of 6950361a~1...6950361a
base,edge,host
```

The scale of it, over every commit since 2026-09-15:

- **352 commits** touch `nix/`. For **74** of them the rule below gives a
  narrower answer than today's.
- **37 commits install nothing at all and were charged a deploy target.**
  Five of those were charged `base,edge,host`: `33a74449` and `bf85a295`
  (both "flake check guest-keystroke-path: …"), `557d1b45` and `2561429c`
  (guest VM tests), and `6950361a` itself.
- Of the 53 commits touching `nix/flake.nix`, **23 changed only lines inside
  the `checks.${system}` block**, and every one of them was charged
  `base,edge,host`.

**What it should do.** Two changes, and the first makes the second honest.

*Move the checks out of `flake.nix`.* The inline `checks.${system}` block is
lines 198 to 679 of a 707-line file: **482 of 707 lines of `nix/flake.nix`
are checks**. Moved to `nix/checks.nix`, `flake.nix` becomes the outputs a
deploy installs, and the path tells the truth with no parsing. The
alternative is deciding per hunk whether a changed line fell inside the
block, by line ranges, which is exactly the kind of guess that produced this
problem.

*A package changed only in its tests reaches no binary.* `needs_of` maps
changed **files** to packages and then asks which binaries import the
package; it never asks whether the change can reach a binary at all. Go
does not compile `_test.go` into one, so a package whose changed files are
all test files contributes nothing. The discriminator is one condition.

Found by the cpu-weight lane on its own branch, and verified here. That
branch changes seventeen Go packages; exactly one of them is in
`cmd/gateway`'s dependencies, and it is changed only in a test file:

```
$ git diff <merge-base>...cpu-weight --name-only | grep '^internal/obs'
internal/obs/metrics/families_host_test.go
$ go list -deps ./cmd/gateway | grep 'obs/metrics$'
github.com/heracraft/repose/internal/obs/metrics
(cmd/gateway and nix/edge: untouched)
```

So `edge` on that entry is a false positive, and `edge` is **a switch of
the edge, which is the owner's action**, on a gateway that is byte-identical.
Over history, 38 of the 542 non-merge commits touching Go since 2026-09-15
changed only `_test.go` files and nothing else that installs anywhere;
several are a `gofmt` of a test file. Each was charged every target the
changed packages' importers deploy to.

And it is live on a second branch. `docs-prose`'s only Go path is
`internal/cli/agent_guide_test.go`, which is why its correct target set is
`base,cli,web` rather than `base,web`: with the right base the `cli` is
still there, and only this rule removes it. The two halves of this problem
are independent and both are needed — the docs lane read its own set as
"web, base, and nothing else", which is the answer after both fixes, not
after one.

*Name the paths that install nothing.* `nix/checks.nix`, `nix/guest/tests/`,
`nix/hosts/tests/` and `nix/guest/checks.nix` are gates. A branch that
touches only those ships as `none`, and the gate still runs: the conductor's
verification list in `RELEASE.md` step 4 is keyed to what the batch *ships*
as, so that list grows a row — **a batch that changes a VM test or a flake
check runs it, whatever the batch ships as**. Without that row this change
would trade a false deploy for a skipped test, which is a worse trade.

**Cost.** A mechanical move of 482 lines, `nix flake check` as its evidence,
and about eight lines in `needs_of`. Two fixture tests
(`needs_flake_checks_only`, `needs_ignores_a_test_only_change`), and three
more for the test directories.

**Rejected.** *Resolving the real targets through the flake's own graph* —
evaluating `nixosConfigurations.host-01.config.system.build.toplevel.drvPath`
and the guest system on both sides of the diff and comparing. That is the
only answer that is right by construction, and it is the right answer for
the conductor at cut time, not for an agent's `add`: it needs the flake
inputs, a warm evaluation cache and minutes, and it cannot run inside a
repose guest at all for the host configurations. Worth adding later as
`release-queue needs --exact` for the conductor to run once per batch; not
worth putting in an agent's path to the queue. *Keeping `checks` in
`flake.nix` and classifying by line range*: a refactor inside the block
moves every line number, and the classifier would be wrong the first time
someone reformats the file.

---

## 3. A released branch has no answer, and a finished worktree reads as work (brief 3)

**What happens.** `ls` drops a `released` entry, so the question "did this
ship" is answerable only from the state file in the common git dir — on the
machine that holds it — or by hand with `git merge-base --is-ancestor`.
Reproduced:

```
$ ops/dev/release-queue ls --all
queued     staging-host    d9329127f  ships as api,base,cli,edge,host,infra,web
queued     cpu-capacity    383d699e0  ships as none
queued     cpu-weight      5dfd958f3  ships as api,base,edge,host
$ grep -rl boot-real-addr .git/release-queue/
(nothing)
$ git merge-base --is-ancestor boot-real-addr origin/main && echo released
released
```

The census here moved while this was being written, and the direction is
worth recording: **52 worktrees with 33 on branches wholly on `origin/main`
at 17:5x, 29 with 8 at 20:3x**, and the eight are the main checkout plus
seven `.claude/worktrees/agent-*` leftovers. Nothing updated a worktree;
they were deleted, after this finding was relayed. That is the argument for
the `worktrees` command rather than against it — the list was actionable the
moment someone could see it — and it is also why the numbers in this section
carry a time.

`boot-real-addr` shipped in r20261014-1 with CLI v0.1.35 and its migration
0026 is applied on prod. Two agents concluded it was never queued. What
misled both is still on disk: `~/kanali-boot-real-addr`, a worktree on a
branch that is wholly on `origin/main`.

The cpu-weight lane reported the same reading again while this document was
being written, independently and for the same two reasons: *"I told my user
that boot-real-addr was a loose end, because its worktree was still there
and `release-queue ls` does not list released entries."* That is the shape
of it — **the two signals a reader reaches for, is there a worktree and is
it in the listing, both point the wrong way, and the truth is only in the
state file and the merge commit.** Neither signal is ambiguous; both are
confidently wrong. **33 of the 52 worktrees on this box
hold branches fully on `origin/main`** (32 plus the main checkout), among
them `~/kanali-host-capacity`, `~/kanali-store-whiteouts` and six
`.claude/worktrees/agent-*`. The brief counts about 120 on kanali.

**What it should do.** Two read-only commands, both answering from git refs
rather than from the state file, so they give the same answer on either
machine:

```
$ release-queue where boot-real-addr
boot-real-addr  released, merged in r20261014-1 (merge a8ef3576, tip d26ab70f)
                leftover worktree ~/kanali-boot-real-addr
$ release-queue worktrees
33 of 52 worktrees are on a branch wholly on origin/main:
  ~/kanali-boot-real-addr      boot-real-addr      released in r20261014-1
  ...
  check the pane before removing one; a session whose worktree is gone
  cannot commit (ORCHESTRATION.md)
```

The mechanism is the merge commit's own subject. `merge_queue` writes
`Merge <branch> (<release>)`, so the batch a branch landed in is on public
main and needs no state at all. Verified from woker-1 for `test-hygiene`, a
branch this machine has never had a ref for — the one the agent reported as
invisible and unqueued:

```
$ git log origin/integrated --grep='Merge test-hygiene (' --format='%h %s' | head -1
deb39daa Merge test-hygiene (r20261015-4)
$ git merge-base --is-ancestor deb39daa origin/main && echo released
(no output: integrated, not yet released)

so:  test-hygiene  merged in r20261015-4, on main, not released
```

`where` therefore answers in this order: the local entry if there is one,
else the merge subject on `origin/integrated`, else the branch's own ref
against `origin/main` and `origin/integrated`, else "nothing here knows this
name". That covers a branch queued on the other machine, a branch released
before anyone asked, and a branch nobody ever queued — with no shared state
and no reachable kanali.

`worktrees` only lists and explains; it deletes nothing. A worktree whose
pane still has a session in it must not be removed under the session
(`ORCHESTRATION.md`, "Delete merged worktrees and branches"), and this tool
cannot see panes.

**Cost.** Two commands, about sixty lines, no new state. Two fixture tests
(`where_is_released_branch`, `where_is_branch_on_the_other_box`).

**Rejected.** Deleting the finished worktrees automatically: a session whose
worktree is removed keeps a dead working directory and cannot commit, and
that has already cost a session's work once. Keeping released entries in
`ls` by default: `ls` is read before every cut and would grow by one line
per branch ever shipped; `--all` already shows them, and `where` answers the
question that was actually being asked.

---

## 4. Ids collide across the two machines (brief 1)

**What happens.** I-745 closed most of this: `id` now reads `refs/remotes`
as well as `refs/heads`, reads both ends of a range header, and refuses to
print a number that is already an entry id. Three things are still open.

*The reservations file is per machine.* `$GIT_COMMON_DIR/release-queue/ids`
never leaves the box. An id reserved on kanali is invisible on woker-1 until
it reaches a shared ref, which is after the lane commits it and the
conductor integrates. The window is the whole life of a branch. This is the
mechanism behind all six double-takes (I-718, I-719, I-732, I-740, and
I-642, I-647 before them); the fixture test
`id_sees_other_machine_reservation` is it in nine lines.

It is not only history. Measured on woker-1 while writing this, by asking
each source of the scan for the highest id it can see on its own:

```
shared refs only (refs/heads + refs/remotes)             746
+ this machine's worktrees (uncommitted DECISIONS.md)    746
+ this machine's reservations file  (the full scan)      749
```

Everything that can cross to the other machine — every ref this box has,
local and remote-tracking, and every worktree's uncommitted file — tops out
at **746**. The reservations file holds **I-747, I-748 and I-749**. The gap
between the two ceilings is exactly the one input to the scan that cannot be
shared, and it is three numbers wide: a lane on kanali that fetches
everything there is to fetch and runs `id` is handed I-747.

Those three are one lane's, reserved from the main checkout at 20:18:52 for
entries it has not written yet, and the docs lane corrected an earlier draft
of this section that called them the seventh, eighth and ninth double-take.
They are not. A double-take is two lanes holding one number; this is one
lane holding three, and the right reading of it is the **window**, not a
collision: the reservations are correct, invisible, and will stay invisible
until the entries are written and integrated. The window is as wide as the
reservations not yet in anyone's `DECISIONS.md`, and it is open on both
machines at once.

*And when the scan is right it can be right by accident.* The cpu-weight
lane's `id` printed I-746, which was correct, but the ceiling it was
computed from came from contributors that are incidental to this machine:
a branch and worktree that lane is about to delete, and two release
worktrees (`kanali-r20261015-4`, `kanali-r20261015-3`) that hold a recent
`docs/DECISIONS.md` in their working copy while being on no branch at all —
`done` deletes both the worktree and its branch, so they are a few minutes
from vanishing. I-745's own measurement is the same shape from the other
side: on 2026-10-15 the old scan topped out at I-740 and would have handed
out I-741, which stop-probe already held. A ceiling that moves when a
worktree is tidied up is not a reservation system.

*The voided reservations work by formatting convention.* Two lines in the
file are dead reservations kept as comments rather than deleted (I-697,
renumbered to I-733; I-740, renumbered to I-746), each with the reason. The
scan does not read them only because `ids_in_use` greps `^I-[0-9]+` and the
comment lines begin with `#`. That is the right outcome by the right
reasoning, and it is held up by a leading character; the ledger's format
should make a voided entry a field rather than a comment.

*A branch cut before I-745 runs the old script.* Problem 5.

*The scan reads refs it can no longer refresh.* Four remotes exist as
remote-tracking refs on this box and none of them is configured:

```
$ git for-each-ref refs/remotes/kanali | wc -l
157
$ git fetch kanali
fatal: 'kanali' does not appear to be a git repository
```

So the 157 `refs/remotes/kanali/*` refs the scan reads are a snapshot from
whenever they were last fetched, and `RELEASE.md` step 3's advice to "git
fetch before reserving" cannot be followed for them. Same for
`refs/remotes/guest/*`, `b3`, `b4`.

**What it should do.** One shared ledger, on a ref on `origin`:
`refs/repose/ids`. `id` fetches it, computes the ceiling over the ledger and
the local scan together, appends its reservations, commits a one-blob tree
and pushes with a compare-and-swap (`--force-with-lease`); a rejected push
means another machine reserved first, and it retries from the new ledger.
Two machines cannot take one number, because the push that would do it
fails.

The ledger holds the number, the date and the machine, and **not the branch
name**: a branch name can say what an unreleased security fix is about, and
this ref is on a public repository. The local `ids` file keeps the branch
name, as now.

When `origin` cannot be reached, `--offline` reserves from this machine's
band — woker-1 takes even numbers, kanali odd — which can collide with
nothing, and marks the reservation unsynced so the next online `id` pushes
it. The id sequence already has gaps (I-740 is voided in the reservations
file), so a gap costs nothing.

Alongside it, the dead remote-tracking refs: either configure `kanali` as a
real remote reachable over the vsock route (problem 9), or delete the four
stale remote namespaces so the scan reads only refs a `git fetch` can
refresh. Keeping 157 refs nothing can update is how "fetch before reserving"
became a line nobody can act on.

**Cost.** A fetch and a push per `id` call, about forty lines, and **a new
ref on the public GitHub repository**. This is the one item that needs your
decision: `refs/repose/ids` would hold every reserved decision number and
the date it was reserved. Every one of those numbers is already public in
`docs/DECISIONS.md` within days, which is why the branch name is left out —
the number and its date say nothing the repository does not already say.
No workflow triggers on it (`ci.yml` and `infra.yml` run on `main` and pull
requests, `release.yml` on `v*` tags), and it is not a branch, so it does
not appear in GitHub's branch list or in Coolify.

**Rejected.** *Reserving ids by a per-machine band alone*, with no ledger: it
works and needs no network, but it halves the usable sequence and a third
machine needs a new modulus and a renumbering of the rule. Kept as the
offline path only. *A private repository for the ledger*: one more thing for
you to create and for every lane to have credentials for, to hold numbers
that are public anyway. *Committing reservations to a file on `main`*: every
`id` call becomes a commit on a branch, racing the conductor's merges, which
is what I-416 rejected when the queue was designed. *Moving the whole queue
state into the ref*: entry notes and live-check text carry host addresses
and operational detail (`cpu-weight`'s live text names `host-01`,
`SEATS_TOTAL` and `10.255.0.7`), and a queued branch's name and note would
be published before its release, which is exactly what I-651's security
carve-out exists to prevent.

---

## 5. A stale copy of the script is silent (brief 6)

**What happens.** Every worktree has its own `ops/dev/release-queue`, from
whatever main it was cut from, and they all write the same state in the
common git dir.

**The stale copy is what `main` gives you.** This is the part that does not
decay, so it is the claim rather than the count:

```
$ for r in origin/main origin/integrated refs/heads/main; do
    printf '%-20s %s\n' "$r" "$(git rev-parse $r:ops/dev/release-queue)"; done
origin/main          e07d81cdcd3a619508226e550d6df2bba762ba2c
origin/integrated    ba144becbdb091449be0869f1bed54cacd842460
refs/heads/main      e07d81cdcd3a619508226e550d6df2bba762ba2c
$ git show origin/main:ops/dev/release-queue | grep -c 'ids_in_use()'
0
```

`e07d81cd` is the dominant stale generation in every worktree census below
**and** it is byte-for-byte what `origin/main` and this box's `refs/heads/main`
hold. I-745's whole rewrite — `ids_in_use`, the `refs/remotes` scan, the
range-header pattern, the refusal to print a number that is already an entry
id — is on `origin/integrated` and on no `main`. CLAUDE.md tells every agent
to start with `git worktree add ../<checkout>-<slug> -b <slug> main`, so on
this box a worktree created exactly as documented gets the pre-I-745 script
every time. The stale copies are a steady state, not a backlog that drains:
**a guard against handing out a used number cannot live only on the branch
nobody branches from.** Found by the docs lane after its own reservation.

One qualifier the lane's version does not have, and it ties this to problem
1: there are three different mains in play. On kanali, `refs/heads/main`
*is* integrated main, so the documented command there does carry the fix. On
woker-1 it does not, because `refs/heads/main` is the stale ref of problem 1.
The same root defect produces both symptoms, and it means the lane that most
needs the guard — one on the box whose main is stale — is the one that
cannot get it.

The census, which does decay:

```
17:5x  52 worktrees, 47 with the script: 44 older, in two generations
20:3x  29 worktrees, 23 with the script: 18 older
```

The drop is worktrees being removed between the two measurements, not copies
being updated. The two older generations date from 2026-10-04 (`aca8ec4c`)
and 2026-10-11 (`e07d81cd`, which is `main`'s).

A lane in any of them runs a version of `id`, `needs_of` and `add` that was
superseded, and nothing says so. The old `id` is the one that handed out a
used number six times.

It happened again while this was being written, and the lane could not have
known. The docs lane, explaining its I-747 reservation, cited `cmd_id`'s
`git branch --show-current` as "line 192". In `origin/integrated`'s copy
that line is 220:

```
origin/integrated   cmd_id at line 218, show-current at 220
main checkout       cmd_id at line 190, show-current at 192
```

So the reservation was made with the main checkout's copy, which is the
**pre-I-745 script**: it scans `refs/heads` only, has no range-header
support, and does not refuse a number that is already an entry id. It
happened to be harmless, because nothing above I-746 lived only on
`refs/remotes` at that moment (`refs/remotes` tops out at I-745). Had an id
been reserved and committed on kanali and pushed to `origin/integrated`,
that run would have handed out a used number, which is I-745's measured
failure mode exactly. A careful lane ran the superseded script, reasoned
correctly from it, and cited its line numbers with confidence. That is the
whole problem in one event.

**What it should do.** On every run, compare the running file against
`origin/integrated:ops/dev/release-queue`. When they differ, print one line
on stderr naming the fix, and carry on — a hard refusal would strand a lane
whose fetch is stale for an unrelated reason:

```
release-queue: this worktree's copy is not origin/integrated's; git fetch
  origin integrated and run it from there, or merge main (DECISIONS I-745)
```

Silent in two cases: when the branch itself changes `ops/dev/release-queue`
(the author of a change to the tool is not running a stale copy), and when
`origin/integrated` is not fetched at all, where the line would be noise
about a missing ref rather than about the script.

**And the guard has to reach `main`.** The warning above tells a lane its
copy is old; it does not stop the copy being old, because the documented
worktree command reproduces it. That half is a process change, not a code
one, and it is the owner's: a fix to `ops/dev/release-queue` or
`ops/dev/decisions-index.py` is tooling every lane runs before it writes
anything, so it should reach `main` in the next release rather than wait for
the queue to fill (`RELEASE.md`, "For the conductor: releasing"). I-745 has
been integrated and unreleased since 2026-10-15; until main moves, every
lane on this box reserving ids runs the script it replaced.

**Cost.** Ten lines, one `git rev-parse`. Two fixture tests
(`stale_script_warns`, `author_script_no_warning` — the second guards
against warning the author, which is the way this kind of check turns into
noise).

**Rejected.** Re-executing `origin/integrated`'s copy instead of warning: it
would silently run code the lane has not read, and it would break this
branch, whose whole purpose is to change the script. Comparing a version
string in the file: it has none, and a version string is one more thing to
forget to bump; the blob hash is the file.

---

## 6. Queue state is per machine, and nothing says so (brief 2)

**What happens.** kanali's queue is the authoritative one. A branch queued
on woker-1 is queued nowhere the conductor looks, and `ls` on either box
reports only its own. An agent on kanali told the conductor its branch was
invisible from woker-1 and should be fetched for the next batch; the branch
had been merged an hour earlier (`deb39daa Merge test-hygiene
(r20261015-4)`). Nothing in the tool's output says which machine's queue is
being read: woker-1's `ls` shows three entries, kanali's shows a different
set, and both print the same heading.

**What it should do.** Most of this is problem 3's `where`, and the reason is
worth stating: **git already carries the answer across machines for every
state but one.** A branch that is merged is on `origin/integrated`; a branch
that is released is on `origin/main`; the release it shipped in is in the
merge commit's subject. Only "queued, not yet cut" lives nowhere but one
machine's state file. So `where` answers correctly from either box for every
question that was actually asked that night, with no shared state at all.

`where`'s second lookup is the one that matters here: it reads
`origin/integrated`, so it answers for a branch name this machine has no ref
for at all (the verified `test-hygiene` case above). That is the whole of
what was asked that night.

And the remaining state cannot be derived, which is now measured rather
than assumed. The cpu-weight lane counted 129 entries against 196 distinct
branch names on kanali: entries outlive their branches, because `done`
deletes the branch it merged. So a ledger — problem 4's or any other —
cannot reconstruct the queue from git alone; `where`'s refs-only answer
covers merged and released, and queued-but-not-cut is irreducibly local
state.

For the remaining state, three small things:

- Each entry records the machine that queued it (`machine=`), and `ls`
  prints which machine's queue it is reading in its heading. An agent
  reading `ls` then knows what it is not seeing.
- `add` names the queue it just wrote to, and says so when that is not the
  conductor's. The cpu-weight lane put it exactly: *"I queued cpu-weight in
  woker-1's queue file and it sat there unread; no conductor reads that
  file, and nothing told me. It surfaced only because the conductor happened
  to look."* A branch queued on the wrong machine is not wrong, it is
  **invisible**, and `add` prints a success line over it. That is the one
  place in this problem where a line of output would have cost nothing and
  saved the branch: of the three ways the queue was read wrong that night,
  this is the only one where the tool was in a position to know.
- `ls` names a queued branch whose commit is not on this machine, so the
  conductor learns it needs the branch before the cut rather than during it.
- `RELEASE.md` says, in words, that the queue state is the conductor's
  machine's and that `where` is the cross-machine question. Today it does
  not say it anywhere.

**Cost.** One field, two lines of output, a paragraph in `RELEASE.md`.

**Rejected.** Replicating entries through a ref on `origin`: problem 4's
rejected list, for the same reason — a queued branch's name and note before
its release. Having `add` push the branch to `origin` as
`refs/repose/queued/<branch>` so the conductor can cut from either box
without a bundle: genuinely useful, and it would remove the git-bundle dance
of problem 9, but it publishes an unreleased branch on a public repository,
which is a bigger decision than this document should bundle. Worth asking
separately if the bundle dance costs another night.

---

## 7. Live checks and the release record are written by hand (brief 7)

**What happens.** `add --live "<text>"` stores the text. Nothing stores
whether it ran, when, or what it printed. Three releases went out with the
gap written into their STATUS line:

- r20261014-1: `NOT DONE: the --live checks, which this line will not
  pretend happened`
- r20261014-2: `NOT DONE: the --live checks for either release`
- r20261015-1: a partial, deferred to r20261015-2

The record itself is hand-assembled prose in `docs/workstreams/STATUS.md`,
one line per release. Those lines are now **3,989, 3,216, 2,599, 2,588 and
2,142 characters** long, in a file whose own header says *"One line per
entry, at most 600 characters"* and *"A 5,000-character line is read by
every session that opens this file and is the reason it had to be split."*
The release record broke the rule the file states, five times.

**What it should do.** The queue already holds every fact in those lines
except the live evidence: the branches, their commits, their targets, the
release id and the base commit. Two commands close the gap:

```
$ release-queue live cpu-weight --ran "seats prints total 1 ... (pasted output)"
$ release-queue live secrets-empty-push --not-done "--global writes into every
    running guest, outside the throwaway-only rule"
$ release-queue record r20261015-2
```

`live` stores the result against the entry with a timestamp, and `--not-done`
takes a reason, so "not done" is a recorded state with a cause rather than a
sentence someone remembered to write. `done` prints which released branches
have a `--live` check and no result yet, as the list the conductor works
through.

`record <release>` prints the STATUS line from the stored facts: branches
with their decision ids and targets, the main commit, the shipped versions,
the live evidence, and what is not done with why. The conductor pastes it.
What is in the queue stops being retyped, and the line stops being the only
copy of the evidence.

Whether the printed record should be the 600-character line the file asks for
with the evidence in a per-release file under `docs/releases/`, or the long
line as today, is a question for you. The stored facts support either.

**Cost.** One field per entry, two commands, about eighty lines, and a
rewrite of `RELEASE.md` steps 4 and 5. One fixture test
(`live_check_is_recorded`).

**Rejected.** Running the live checks from the tool: a live check needs a
throwaway `e2e-*` project, a CLI built from the released commit and a
judgement about whether the output is right; it is the conductor's work, and
a tool that ran it would have to decide pass or fail from free text.
Generating the STATUS line into the file directly: `STATUS.md` is
union-merged, and a tool writing into it would race every branch that adds a
line.

---

## 8. Reaching the other lanes (brief 8)

**What happens.** Local sessions have `ListAgents` and `SendMessage`. A
session inside a repose guest has neither: it is reached with
`repose-admin exec <guest id> -- bash -lc '...'` through the control VM,
with a tmux paste, and its replies come back through a relay file or a pane
capture. `/tmp/claude-1000/kx` is the wrapper that makes it work, base64 so
the quoting survives two shells; it is not in the repository, and the relay
file it pairs with (`~/kanali-relay.txt`) is already gone from this box.
Messages get lost that way.

**What it should do.** The queue is a channel both machines already write to,
for the traffic that is about a branch:

```
$ release-queue say cpu-weight "cut without cpu-capacity; requeue after it lands"
$ release-queue inbox          # in a worktree: what was said about this branch
```

`say` appends a dated line to the entry; `inbox` prints the lines for the
current branch and marks them read; `ls` marks an entry that has unread
lines. A lane that reads the queue before it queues — which it does — reads
its messages with no channel at all.

This does not replace the channel: it carries "your branch was dropped from
the cut", "queue again after X lands", "the conflict is yours", which is most
of what was being relayed that night, and it is durable where a tmux paste
is not. It is per machine, like the rest of the entry, so it works when the
conductor and the lane are on the same box, which is the common case.

**Cost.** Two commands, about forty lines. No test is listed in `KNOWN_BAD`
for it; one comes with the implementation.

**Rejected.** A message queue of its own: a second thing to run and keep
alive, for traffic that is already keyed by branch. Carrying arbitrary
session-to-session messages: the queue knows about branches, and a message
that is not about a branch belongs in the channel.

---

## 9. The gateway bans the conductor (brief 9)

**What happens.** The gateway refuses a source `/24` for 10 minutes after 20
failed handshakes in 10 minutes (`internal/gateway/limits.go`: `banFailures
= 20`, `banWindow = 10 * time.Minute`, `banDuration = 10 * time.Minute`).
woker-1 is one address for several agents, and it was cut off from kanali
three times in one night, mid-release.

I-599 already made the easy case harmless: a refusal of the user's *own
current* certificate (a stopped project, a principal from before a restore)
ends `refused` and does not count. What still counts is the list I-599
keeps: *"no certificate, another CA, **expired**, revoked, another user's
handle, a bad login, a broken handshake."* And the operator certificate on
this box expires every 8 hours by design (`ops/dev/operator-cert.sh`:
`--ttl 8h`). An expired certificate plus a retry loop is 20 counted
failures in well under 10 minutes, every time.

So the ban is not misfiring: it is doing its job to a box whose certificate
went stale and that kept trying. Note for the record that the brief cites
I-640 for the ban; I-640 is the tmux agent windows. The ban is I-435, and
I-599 is the refusal that does not count.

**What it should do.** Two cheap things on this side, and nothing in the
gateway:

- `ops/dev/operator-cert.sh --if-stale` renews only when
  `ssh-keygen -L -f <key>-cert.pub` shows under an hour left, so it can be
  the first line of anything that reaches a host or a guest, and the
  `RELEASE.md` and `RUNBOOK` steps that ssh get it. The failures stop at the
  source: a fresh certificate does not fail a handshake.
- The vsock route becomes a committed tool rather than a file in `/tmp`:
  `ops/dev/guest-exec <guest-id> '<command>'`, the base64 wrapping and the
  `docker exec` on the control VM that `/tmp/claude-1000/kx` does today,
  with the control address and the api container prefix read the way
  `operator-cert.sh` reads them. It is the route that works when the gateway
  has banned the box, and today it exists only as an untracked file that the
  next session has to be told about.

**Your call, not this lane's:** whether an expired certificate from a source
that has authenticated before should count towards the ban at all, and
whether the conductor's box should be exempt. Both are gateway changes and
both weaken the ban; neither is needed if the certificate is renewed before
it is used.

**Rejected.** Raising `banFailures` or shortening `banDuration`: the ban is
tenant protection and the failures were real. A long-lived operator
certificate: the 8-hour TTL is what makes the key on this box low-value.

---

## 10. The entry store has no schema, and three ways in lose a branch (not in the brief)

Three defects with one root: an entry is a flat `key=value` file with no
escaping, no schema and no injective name, and every reader trusts it.
Each of the three ends the same way — **a branch leaves the queue while
`add` prints a success line** — and each was found by a different route.

**What happens.** An entry is a flat `key=value` file, written as a block of
`echo "key=$value"` lines in a fixed order: `branch sha worktree needs live
note queued seq state`. Nothing escapes the value. So a `--note` or `--live`
value containing a newline — which the useful ones do; `cpu-weight`'s stored
`live` text is 1,900 characters of numbered checks — becomes one `key=value`
line plus orphan lines that parse as nothing, and `add` prints its success
line over it. Found by the cpu-weight lane doing it: a four-paragraph note
gave 12 lines, 9 of them `key=value` and 3 orphans, and `field note` returns
only the first paragraph. The paragraph that was lost asked the conductor to
drop another lane's branch.

**It is worse than losing the paragraphs.** `field()` is `sed -n
"s/^$2=//p" | head -n1` — the **first** line matching a key — and `note=` is
written *before* `state=`, `sha=` and `needs=`. So a line inside a note that
looks like a key silently overrides the real one. Reproduced end to end in
the fixture, with a note whose second paragraph is the single word
`state=released`:

```
$ release-queue add --note "$note"
queued forged at 05c69683 (1 commits; ships as: api,host)

entries/forged:
  note=why this branch exists
  state=released              <- the note's second line
  and the rest of the reason
  ...
  state=queued                <- the real one, never read

$ release-queue ls
the queue is empty
```

`add` reported success and the branch left the queue, because `ls` drops a
released entry. The same path reaches `sha=`, where `merge_queue` would take
the forged value and **merge a different commit**, and `needs=`, where the
cut would record the wrong targets. Nobody here is hostile; a note that
quotes a command, a diff or an error message is all it takes.

**The second half: a stray temp file is a second queue entry.** `cmd_add`
writes `"$f.new"` and moves it into place; `setfield` writes `"$f.XXXX"`;
every reader globs `entries/*`. So an `add` killed between its write and its
`mv` leaves a complete entry behind, and the branch is queued twice.
Reproduced in the fixture — a successful `add`, then a second `add`
interrupted after the branch moved:

```
$ release-queue ls
queued  interrupted  37f5013ae  ships as api,host  (branch moved since queued: 7cb230d; queue again)
queued  interrupted  7cb230d4f  ships as api,host
```

Two commits of one branch, both queued; `cut` merges both and the release
record gets two `merged=` lines for it. (The cpu-weight lane expected the
stray to read as a branch named `cpu-weight.aBcD` with no sha; it is worse
than that — the stray carries the full entry including the branch name, so
it duplicates rather than inventing anything. A later `add` on the same
branch cleans it up as a side effect of its own write, so the exposure is a
cut that comes first.)

**The third way in: the filename is lossy.** `key()` is `printf '%s' "$1"
| tr '/' '%'`, which is not injective — `pair/one` and `pair%one` encode to
the same filename, so the second to queue silently overwrites the first's
entry. Verified:

```
$ release-queue add            # on pair/one
queued pair/one at 1b0d5ef9 (1 commits; ships as: api,host)
$ release-queue add            # on pair%one
queued pair%one at 7c9e9daa (1 commits; ships as: cli)
$ ls .git/release-queue/entries/
pair%one
$ release-queue ls
queued  pair%one  7c9e9daad  ships as cli
```

`pair/one` is gone from the queue, with a success line behind it.

The live exposure, swept by the cpu-weight lane on both boxes and
re-measured here for woker-1: no two branches collide today, no branch name
anywhere contains a literal `%`, and one entry on kanali is exposed —
`repose%fider`, whose decoded branch is `repose/fider` and whose colliding
partner does not exist. So, as with the other two, the defect is real and
has never fired.

The risk is not today's names, it is the naming convention, and this is the
lane's point: `/` is the documented shape (`ws/01-host-nixos`,
`release/r20261015-4`, `bump/agents-20260924`), so the queue's own habit is
what produces the slashes.

```
$ git for-each-ref refs/heads refs/remotes | <strip the remote> | sort -u | wc -l
212 distinct branch names on woker-1
  with a literal %:  0
  with a slash:     43
  key() collisions:  none
```

**43 of the 212 are one keystroke from a silent erase**, and a branch
created by a tab-completion slip is likelier than a deliberate `%`. Encode
the filename properly, or name the entry by a hash of the branch and keep
the name in the file.

**None of the three has ever fired.** The cpu-weight lane swept all 129
live entries on kanali the way `field()` reads them: 0 with orphan lines, 0
where the first `state=` differs from the last, 0 strays, and the one
filename that does not match its `branch=` is `repose%fider`, which is
`key()` working as designed. So no entry was ever written through a
multi-line note, no interrupted `add` left one behind, and `ls` has never
silently hidden a queued branch. This is pre-emptive, not a cleanup — it is
first in the order of work because it is a handful of lines and needs no
decision, not because it is burning.

The lane corrected that sweep itself, and the correction is worth recording
because the proposal rests on it: the first pass grepped `entries/` for
`\.[A-Za-z0-9]{4}$`, which matches `setfield`'s `mktemp "$f.XXXX"` and
cannot match `cmd_add`'s `"$f.new"` — three characters. The re-sweep, for
`*.new`, for names containing a dot, tilde or `tmp`, and for files whose
`branch=` differs from the filename, is what the result above is.

**What it should do.** Three lines of change, and the first alone closes the
forging half:

- `add` refuses a `--note` or `--live` value containing a newline, or
  flattens it to one line and says it did. Flatten is the better default:
  the lane's four-paragraph note was worth keeping, and it worked around
  this by hand with `tr '\n' ' '` once it knew.
- `field()` reads the **last** match rather than the first, so the fields
  the tool writes after the free text win over anything inside it. Belt and
  braces, and it costs `tail -n1` instead of `head -n1`.
- Problem 4's ledger inherits the same guard. Anything that stores a reason
  string will eventually be handed a paragraph — the voided-id field is
  exactly that.
- The writers stop leaving siblings next to the entry: `mktemp` into a
  `tmp/` subdirectory, and `$f.new` with it. **Not** a name filter in the
  readers. The two temp names sit at lines 37 and 152 of the same script, on
  every box, and look nothing alike:

  ```
  origin/integrated    mktemp "$f.XXXX" at 37   "$f.new" at 152,153
  origin/main          mktemp "$f.XXXX" at 37   "$f.new" at 152,153
  ```

  A reader taught to skip one still trips on the other, and would rot
  silently when a third is added. A directory that holds only entries needs
  to know none of them. (The lane's point, and it is the right call.)

**Cost.** Three lines for the note, and one directory for the temp files.
Three fixture tests (`add_rejects_a_multiline_note`,
`add_note_cannot_forge_a_field`, `stray_temp_entry_is_not_a_branch`).

**Rejected.** Making the entry a different format (JSON, a directory of
one-value files): it would be more correct and it is not worth rewriting
every reader for, when refusing a newline closes the hole. Quoting the value
and unquoting on read: two more places to get wrong than refusing the input.

---

## The fixture

`ops/dev/release-queue-test`, built before any of the above, because the
tool had no tests and "a change to `cmd_id` or to `needs` is verified by
running it" is how these nine problems were found one release night at a
time.

It builds a scratch world per test and throws it away: a bare
`origin.git` with `main` and `integrated`, and two clones of it, `woker`
and `kanali`, so a test can say what one machine sees of the other's
reservations. The repository inside is synthetic and has the shape
`release-queue` reads — the seven `cmd/` binaries with internal packages
between them (so `go list -deps` resolves), `internal/cli` with
stand-ins for the trust-the-reader guards, a non-Go file under a package
directory, `apps/web`, `nix/` split the way the real one is with a
`flake.nix` that has both an outputs region and a checks region,
`infra/`, `docs/DECISIONS.md` with a plain header and a range header, and
the `ops/dev/release-queue` under test copied in.

```
$ TMPDIR=/tmp/rt ops/dev/release-queue-test
release-queue tests (ea7fe85d)
  ok      abandon_requeues
  known   add_note_cannot_forge_a_field  (P10 a note line that looks like a key wins over the real one)
  ok      add_records_live_and_note
  ok      add_refuses_dirty_tree
  ok      add_refuses_main
  ok      add_refuses_no_commits
  known   add_rejects_a_multiline_note  (P10 a newline in --note is written raw)
  ok      add_runs_cli_guards
  ok      author_script_no_warning
  ok      cut_merges_in_queue_order
  ok      cut_regenerates_decisions_index
  ok      done_requires_main_to_hold_it
  ok      id_hands_out_several
  ok      id_refuses_used_number
  ok      id_sees_local_branch
  known   id_sees_other_machine_reservation  (P1 the reservations file never leaves the machine)
  ok      id_sees_range_header
  ok      id_sees_remote_tracking
  ok      id_sees_uncommitted_worktree
  known   live_check_is_recorded  (P7 nothing stores whether the check ran)
  known   needs_against_stale_local_main  (P5 needs and the commit count read refs/heads/main)
  ok      needs_cli_only
  ok      needs_docs_only
  ok      needs_embedded_file
  known   needs_flake_checks_only  (P4 a checks-only flake change ships as base,host,edge)
  ok      needs_go_shared_package
  ok      needs_guest_nix
  known   needs_ignores_a_test_only_change  (P4 a _test.go change reaches no binary)
  ok      needs_shared_nix
  ok      needs_web
  known   stale_script_warns  (P6 a worktree's old copy says nothing)
  known   stray_temp_entry_is_not_a_branch  (P10 an interrupted add leaves entries/<b>.new, which ls and cut read)
  known   two_branches_keep_two_entries  (P10 key() maps / to %, so a%b and a/b share one entry file)
  known   where_is_branch_on_the_other_box  (P2 the queue state is per machine)
  known   where_is_released_branch  (P3 no command answers 'did this branch ship')

23 passed, 0 failed, 12 known bad, 0 unexpectedly passing
exit 0, 11 s
```

Every test asserts the behaviour its name asks for. `KNOWN_BAD` at the top
of the script names the twelve that fail until their fix lands, with the
problem each belongs to; the suite exits 0 when exactly those fail, and 1
when any other fails **or when a known-bad one starts passing**, which means
its line is stale and should come out. The numbering in `KNOWN_BAD` is the
brief's, which is why it runs P1 to P7 rather than this document's order;
P10 is this document's problem 10, which the brief does not have.

It has already earned its keep. `needs_ignores_a_test_only_change` and the
four entry-store tests were written from field reports rather than from
reading the script, and all five were failing within minutes of being
written — including the three that prove a branch can leave the queue while
`add` reports success.

**Why a fixture and not a sweep of the live queue.** Both were used here and
they answer different questions: a sweep of the 129 live entries says a
shape has never occurred, and only a fixture says what happens when it does.
Neither is sufficient — the sweep called problem 10 pre-emptive, which
reranked it, and the fixture is what showed that the pre-emptive case loses
a branch. (The framing is the cpu-weight lane's.)

Three measurements in this document were wrong and were caught, which is
the other half of the argument. One of the entry-store tests passed on its
first run because it modelled an interrupted `add` followed by another
`add` — the self-cleaning order — and had to be rewritten to put the cut
first. The lane's first stray sweep used a four-character pattern that
could not match a three-character suffix. And two worktree censuses decayed
between measurements, one because the finding had been acted on. Every one
surfaced because someone re-ran it rather than citing it.

It needs `go` and `python3` and nothing else: no network, no nix, no
Postgres, no host. `ops/check.sh` is where it belongs once this work lands,
so CI runs it.

## What this does not touch

- The tool stays bash, with its state in the common git dir under one flock,
  on no branch (the brief's rule). Problem 4 adds one ref on `origin`
  alongside that state; nothing moves out of it.
- `cut`, `resume`, `done` and `abandon` keep their shape. Problem 1 adds one
  refusal to `cut`.
- Nothing here changes what a release *does*: the push, the tag, the base
  publish and the switches stay where `RELEASE.md` has them, and the host
  and edge switches stay yours.

## Order of work

1. Problems 1, 2, 3, 5 — bug fixes and read-only commands, each with the
   fixture test that was failing. These can go in one branch.
2. Problem 6 — one field and a paragraph, once 3 is in.
3. Problem 4 — after your decision on the ref. The offline band can land
   first on its own; it is strictly better than today with no new ref.
4. Problems 7 and 8 — they change the conductor's procedure, so
   `RELEASE.md` and `ORCHESTRATION.md` move with them in the same commit.
5. Problem 9 — `--if-stale` and `ops/dev/guest-exec` are small and
   independent; the gateway question is yours whenever you want it.

Problem 10 goes first. It is three lines, it needs no decision, and it is
the only one on this list that can lose a branch between `add` and the cut —
though the sweep says it never has, so it is cheap insurance rather than a
fire. The same pass should move the entry writers' temp files out of
`entries/`.

Each step ships with its `RELEASE.md` and `ORCHESTRATION.md` update and a
`DECISIONS.md` entry under "Made during implementation", amending I-416 and
I-424 where it changes what they describe. (The brief cites I-411 as the
entry that records the queue's design; I-411 is `repose exec` taking its
command with or without `--`. The queue is I-416, amended by I-424, I-651
and I-745.)
