# Releasing: the queue and the release session

Several agents build on this repository at once. Each one works on its own
worktree branch and queues the branch when it is done; one session, the
conductor, merges the queue into main in verified batches, and ships
what main holds when the owner asks for a release. DECISIONS I-416
records why. `ORCHESTRATION.md` covers the rest of the conductor's job;
this file is the part between "a branch is done" and "it is live".

## For an agent building a change

1. Start in a worktree, never in the main checkout:
   `git worktree add ../kanali-<slug> -b <slug> main` and work there. The
   `kanali-` prefix is the historical name of the checkout, not the box:
   the conductor's is woker-1 (I-763).
2. Claim the work in `docs/workstreams/STATUS.md` (CLAUDE.md).
3. Reserve decision ids before writing them:
   `ops/dev/release-queue id [N]`. It reads every local branch, every
   remote-tracking branch, every worktree's uncommitted
   `docs/DECISIONS.md` and the reservations, under one lock, and refuses
   to hand out a number that is already an entry id. `git fetch` before
   reserving (DECISIONS I-745).

   That still leaves a window: the reservations file never leaves its
   machine, so an id reserved on one box is invisible on the other until
   the branch is integrated. Give each machine a band and the window
   closes — `ops/dev/release-queue band 0/2` on woker-1,
   `band 1/2` on kanali — at the cost of gaps in the sequence. `band` with
   no argument says what this machine draws from, `band none` goes back to
   dense numbering (DECISIONS I-760).
4. Finish the change with the evidence its checklist names. Then commit,
   `git merge main`, rerun the checks the merge touches, and queue:

   ```
   ops/dev/release-queue add --live "repose start of a stopped e2e project: under 12 s"
   ```

   `add` refuses a dirty tree, a branch whose CLI output or web text
   fails the trust-the-reader guards (DECISIONS I-484, I-485; it runs
   them for a branch that ships as `cli` or `web`), a branch with nothing
   the integration tip lacks, and a branch that conflicts with the tip
   anywhere but `docs/DECISIONS-INDEX.md`. It records the commit, so later
   commits need another `add`; that works while the branch is in a cut too,
   and the conductor takes the new commits with `resume` or in the next
   cut. It prints what the branch ships as (below).

   The **integration tip** is the newest integrated commit the branch was
   cut from, not `refs/heads/main`: the merge-base of `HEAD` with each of
   `refs/heads/main`, `origin/integrated` and `origin/main`, whichever is
   newest. On kanali that is the local `main`, which is integrated main; on
   woker-1 it is `origin/integrated`, whose `main` is usually behind.
   `needs`, the commit count and the conflict check all read it, and `cut`
   refuses while `main` is behind it (DECISIONS I-759).

   A `--live` or `--note` value is folded onto one line, and says so: the
   entry file is one line per field (I-758).
5. When the release has gone out, record what the `--live` check printed:
   `ops/dev/release-queue live <branch> --ran "<output>"`, or
   `--not-done "<why>"`. Until then `done` and `record` say the result is
   owed (I-760).
6. Stop there. Don't merge into `main`, push, tag, publish a base or switch
   a host. If the conductor asks for a change, make it on the same branch
   and `add` again. `ops/dev/release-queue inbox` is where a message about
   this branch arrives; `ls` marks an entry that has one unread.

`ops/dev/release-queue ls` shows the queue. A branch that moved after it
was queued is marked; queue it again or the release takes the old commit.
A row also says which machine queued it, when that is not this one, and
whether its commit is in this clone.

**The queue state is one machine's.** It lives in that clone's common git
dir, so a branch queued on woker-1 is queued nowhere kanali looks, and
kanali's queue is the authoritative one. The cross-machine question is
`ops/dev/release-queue where <branch>`, which answers from refs instead:
the local entry, else the merge commit `cut` wrote, else the branch's own
ref. It works for a branch this machine has never had — `where
test-hygiene` from woker-1 answers "merged in r20261015-4, on main, not
released". `where` is also the answer to "did this ship", which `ls` drops
once a branch is released, and `ops/dev/release-queue worktrees` lists the
worktrees whose branch is wholly released, which is finished work that
reads like unfinished work (DECISIONS I-760).

## What a branch ships as

`add` reads the branch's diff against main and names the deploy targets it
reaches. Go packages count through `go list -deps`: a change to
`internal/obs` reaches every server binary but not the CLI. A non-Go
file under `cmd/` or `internal/` (an embedded email template, a
migration) counts as a change to the nearest package above it, so an
email edit ships as `api`.

| Target | Reached by | Shipped by | Who may run it |
|---|---|---|---|
| `api` | `cmd/api`, `cmd/repose-admin` and what they import | push to main (Coolify, about 2 min) | conductor, with the owner's go-ahead for the release |
| `web` | `apps/web/` (the public docs included) | push to main | same |
| `cli` | `cmd/repose` and what it imports | a `v*` tag (`release.yml`) | same |
| `base` | `nix/guest/`, `cmd/guestd`, `cmd/repose-hook`, shared nix files | `repose-admin base publish --rev <sha on main>` | same |
| `host` | `nix/hosts/`, `cmd/hostd`, shared nix files | a switch of each host | the owner, or the conductor when the owner says so for that release |
| `edge` | `nix/edge/`, `cmd/gateway`, shared nix files | a switch of the edge (RUNBOOK "Switch the edge") | the owner (open sessions stay since I-471; the first switch onto it drops them once) |
| `infra` | `infra/` | `tofu apply` | the owner |
| `none` | docs outside `apps/web`, ops notes, tests | nothing | |

Three things narrow that list, because it used to name deploys a branch
could not reach (DECISIONS I-759):

- a **gate** installs nothing. `nix/guest/tests`, `nix/hosts/tests` and
  `nix/guest/checks.nix` are evaluated and deployed nowhere, so a branch
  that touches only those ships as `none`. **A batch that changes a VM
  test or a flake check runs it, whatever the batch ships as** — that is
  now the only thing keeping those gates honest, so it is a row in the
  verification below;
- a package whose changed Go files are all `_test.go` reaches no binary;
- `base`, `host` and `edge` are confirmed against the built systems.
  `add` runs `ops/dev/closure-equiv.py` for each and drops the ones whose
  system does not change, which takes about three seconds each.
  `REPOSE_QUEUE_FAST=1` skips it and the entry records `exact=no`; a box
  with no nix, which is every repose guest, does the same. A comparison
  that fails keeps the target.

`ops/dev/closure-equiv.py <flake attr> <revA> <revB>` also answers the
question on its own, which is what to run before a switch or a base
publish when the diff looks like it changes nothing:

```
ops/dev/closure-equiv.py nixosConfigurations.host-01.config.system.build.toplevel main origin/main
```

A plain `drvPath` comparison cannot answer it: `self.shortRev` is stamped
into hostd, guestd and the guest's base version, so every commit changes
every system's drvPath.

## For the conductor: integrating into main

Main moves in two steps (owner, 2026-10-03). Integrating merges verified
branches into main **on this machine only**: agents branch from it and
the owner sees it with `git fetch repose`, but nothing reaches users.
Releasing pushes main and ships it, when the owner asks. Nobody pushes
main between releases, the conductor included: GitHub's `main` is what
Coolify deploys, so any push ships every integrated change at once.
GitHub CI runs only on that push, so the verification below is the only
gate until then. Integrated main is also pushed to GitHub's `integrated`
branch, which deploys nothing ("The integrated branch" below).

Integrate whenever branches are queued; it needs no owner approval.

1. **See what is waiting.** `ops/dev/release-queue ls`. Ask each agent
   with a branch in flight (`ListAgents`, `SendMessage`) whether it is
   about to queue. Sessions get renamed: a send that fails with "no agent
   named" means the name changed, not that the session ended. Run
   `ListAgents` again and match the `[ref]`, and tell agents your current
   name, since their replies to an old one are lost.
2. **Cut.** `ops/dev/release-queue cut` makes `~/kanali-r<date>-<n>` on
   branch `release/r<date>-<n>` from main and merges every queued branch
   in queue order, each with `--no-ff`. It refuses while `main` is behind
   the newest integration ref and names the fast-forward: a cut from a
   stale main re-merges everything integrated since, which is how a
   release loses a change quietly (DECISIONS I-759). A conflict that is only the
   decisions index is settled by regenerating it. Any other conflict
   stops the cut: resolve it in the batch worktree (or send it back to
   the branch's agent and `drop` the branch), commit, then
   `ops/dev/release-queue resume <batch>`. `abandon <batch>` undoes a cut
   and puts its branches back in the queue.
3. **Freeze when you verify.** Commits that arrive after the cut wait for
   the next batch unless they fix this one; each one taken in means
   verifying again.
4. **Verify the merge.** In the batch worktree, for
   everything the batch ships as:
   - always: `python3 ops/dev/decisions-index.py --check`; `go build ./...`,
     `go vet ./...`, `golangci-lint run ./...`; `go test -race -timeout 30m ./...`
     with a real Postgres for the api packages (on a repose guest, unset
     `REPOSE_PROJECT REPOSE REPOSE_HOOK_AGENT` first, and keep `TMPDIR`
     at `/tmp`: a longer one pushes the hostd fakes' unix sockets past the
     108-byte limit; internal/cli alone takes about 15 minutes with
     GOMAXPROCS=2, past go test's default 10-minute timeout); `go test ./internal/cli
     -run 'TestDocs|TestSuccessOutputNamesNoCommand|TestCLIReassures'`; the
     `docs/CHECKLIST.md` greps. **A batch that changes a VM test, a flake
     check or `nix/guest/checks.nix`: run that check, whatever the batch
     ships as** — those paths install nothing, so they no longer put
     `base`, `host` or `edge` in a branch's targets (DECISIONS I-759), and
     this is the only thing left that runs them. A batch that adds or changes CLI output:
     read its success lines and listings as a user on the hundredth run
     would, and send back any that teach a command (DECISIONS I-484);
     the test misses commands assembled at run time. A batch that adds
     or changes docs or copy: read the new paragraphs the same way and
     send back help nobody asked for (I-485);
   - `web`: `pnpm --filter web exec vitest run`, `svelte-check`, `eslint .`,
     `build`, and the playwright suites (on a repose guest, set
     `PLAYWRIGHT_CHROMIUM_PATH` to the base's
     `~/.cache/ms-playwright/chromium_headless_shell-*/chrome-headless-shell-linux64/chrome-headless-shell`,
     and run them alone: they serve on 127.0.0.1:4173, and another
     session's preview or a parallel `go test` there fails them all);
   - `base`: `nix build ./nix#guest-system` and the `guest-closure-size`
     check; the VM checks the merged change touches, on a box that boots
     them (they do not boot on a repose guest);
   - `host` / `edge`: `nix eval` of each configuration's toplevel and a
     `dry-activate` on the target before any switch.
   A failure is fixed in the batch worktree when it comes from the merge,
   or sent back to the branch's agent when it is the branch's own.
5. **Move main, locally.** `git -C <the conductor checkout> merge --ff-only
   release/<batch>` (the main checkout must be clean), then
   `ops/dev/release-queue done <batch>`, which removes the batch
   worktree and pushes main to `origin integrated` (and nothing else). Do
   not push `main`. `ls` now lists those branches as `on-main` and
   ends with what the next release ships as. Tell each agent its branch
   is on main, so it merges main before its next `add`.

## For the conductor: releasing

A release is cut when the owner asks for one, not each time a batch
lands: it costs a deploy, a base publish, a CLI tag and live checks, so
main is meant to fill. A fix for something broken in production is the
exception; say so when asking, and remember the push takes everything
else integrated with it.

1. **Ask once.** `ops/dev/release-queue ls` says what main holds unreleased
   and what it ships as. Send the owner one summary (`repose-ask --options
   yes,no`, or ask in the session): the branches with one line each, the
   targets, and what each target's ship step does to tenants. A yes covers
   the push, the tag and the base publish. A host or edge switch still
   needs the owner's word for that switch.
2. **Push main.** Watch CI on the pushed commit; a push redeploys `api` and
   `web` whether CI passes or not, so a red run is fixed at once (as its
   own small batch, integrated and pushed).
3. **Ship the rest**, in this order when present: `base` publish (the rev
   is the pushed main commit), `cli` tag (the next `v0.1.N`), `host`
   switches (RUNBOOK "Switch a host to main") and the `edge` switch.
   When the base sends a guest kind or field the running hostd refuses
   (`agent_done`, I-688), switch the hosts first and publish the base
   after them.
   A `cli` tag pushed before the owner has set up the `release`
   environment and its signing key stays an unpublished draft ("The
   release signing key" below).
4. **Check it live.** Run each released branch's `--live` check on
   throwaway `e2e-*` projects, never on the owner's projects, at most two
   alive at once. Paste the output into the release record.
5. **Record.** `ops/dev/release-queue record <release>` prints the line for
   `docs/workstreams/STATUS.md` and the detail that does not fit in it,
   from what the queue already holds — the branches with the decision ids
   they add, the targets, the base and main commits, and each `--live`
   result recorded with `release-queue live`. The line goes in `STATUS.md`
   and the detail in `docs/releases/<release>.md`, because five release
   lines written by hand reached 2,142 to 3,989 characters in a file whose
   header asks for 600 (DECISIONS I-760). Both go into main with the next
   batch, not as a push of their own.

   Tell each agent whose branch shipped — `ops/dev/release-queue say
   <branch> "shipped in <release>"` leaves it where that lane will read it,
   and `where <branch>` is what anyone can ask later. It may then delete
   its worktree, or the owner does; `ops/dev/release-queue worktrees` lists
   the ones whose branch is wholly released.

## The integrated branch

DECISIONS I-651, as I-763 leaves it. The conductor's main lives on one
box — woker-1 since 2026-10-17, the kanali guest on host-01 before it — so
without a copy elsewhere the only integrated main stops with the machine
the conductor most needs to fix. `release-queue done` therefore
pushes main to the GitHub branch `integrated` after every integrate, as a
fast-forward; a refusal is printed and means someone moved the branch by
hand.

Nothing deploys from it:

- `ci.yml` and `infra.yml` run on pushes to `main` and on pull requests;
  `release.yml` on `v*` tags; `bump-agents.yml` on its schedule. A push to
  `integrated` starts none of them.
- Coolify's `api`, `api-grpc` and `web` deploy pushes to `main` only
  (`coolify.md` fact 16).

Keep it that way: a workflow with an unfiltered `on: push`, a Coolify
application pointed at `integrated`, or a pull request opened from it
would make it something other than a copy. A release still pushes `main`;
after it `integrated` and `main` are the same commit until the next
integrate.

**`integrated` is public.** The GitHub repository is public, so every
integrated commit is readable there before it is deployed, often for days
(I-424). A fix for a vulnerability (the gateway, api auth, tenant
isolation, a host or guest escape) must not sit on `integrated` while prod
runs the vulnerable code. Queue such a branch only for the release that
ships it: when the owner has asked for that release, integrate it with
`REPOSE_INTEGRATED_REMOTE=none ops/dev/release-queue done <batch>` and ship
at once. Until main is pushed, every later `done` also runs with
`REPOSE_INTEGRATED_REMOTE=none`, and the copy off the conductor box is a
`git fetch` of woker-1's main from kanali instead (I-763; it was the other
way round while main lived on the guest).

When the ssh gateway has banned this box — 20 failed handshakes from a /24
in 10 minutes earns 10 minutes refused (DECISIONS I-435), and an expired
operator certificate counts (I-599 exempts only the user's own current
one) — run `ops/dev/operator-cert.sh --if-stale` before anything that
sshes, which stops the failures at the source, and reach a guest with
`ops/dev/guest-exec <guest-id> '<command>'`, the vsock route through the
control VM (DECISIONS I-760).

To work from it on another machine (RUNBOOK "Operating while host-01 is
down"): `git fetch origin integrated` and `git worktree add
../<checkout>-<slug> -b <slug> origin/integrated`. If the conductor box is
gone, its queue state in `.git/release-queue/` is gone with it; the
branches and their merges on `integrated` are not.

## The release signing key

A `v*` tag runs `release.yml`: GoReleaser uploads a draft, and the `sign`
job signs `checksums.txt`, attests the archives and publishes the draft
(DECISIONS I-430). A tag whose `sign` job fails leaves a draft that
install.sh never sees; fix the cause and re-run the job from the Actions
page.

**The notes are written before the tag is pushed** (DECISIONS I-798).
`release.yml` refuses to publish a release whose body is under 400 bytes,
so a tag with no notes stays a draft and `install.sh` never sees it.

Write the notes to a file before the push, then push the tag, then
`gh release edit <tag> --notes-file <file>` between the tag and the
`sign` job, and re-run the job from the Actions page.

**Do not create the draft yourself before the push** (DECISIONS I-860).
`gh release create <tag> --draft` and GoReleaser each make their own
release for the same tag, and GitHub holds both: one with the notes and
no assets, one with the four archives and an empty body. `gh release
view <tag>` then resolves to whichever it likes, `sign` fails on "no
assets to download", and the tag exists with no published release while
everything looks right. v0.1.40 shipped that way for ten minutes.

What the notes say, and how they read:

- Every change since the last tag that a user can see: a command, a flag,
  a message, a limit, a price, a default, a dashboard action, guest
  behaviour. A change nobody can see is left out.
- What a release does NOT fix, when a user would otherwise assume it does,
  with the numbers. v0.1.37's notes say the prompt race is not fixed and
  give 1 of 10, 2 of 10 and 0 of 9.
- What a change costs, when it costs something. v0.1.38's notes say a
  server shutdown now waits up to four minutes.
- Measured figures, never adjectives. "9.1 s for a 1.0 GB image", not
  "much faster".
- About 80 percent of the way to ASD-STE100, Simplified Technical English:
  the active voice, the simple present tense, one idea to a sentence,
  short sentences, no idioms and no metaphors. Product terms, command
  names and numbers stay exact. The owner asked for this on 2026-10-18.

The private key is `REPOSE_RELEASE_SIGNING_KEY`, a secret of the GitHub
environment `release`, which only `v*` tags may deploy to. The public half
is `RELEASE_PUBKEY` in `install.sh`; `sign` refuses to publish when the two
do not match. Nobody else keeps a copy of the private key.

To rotate it (a suspected leak, or a person who held it leaving):

```
openssl ecparam -name prime256v1 -genkey -noout -out key.pem
openssl ec -in key.pem -pubout                # paste into RELEASE_PUBKEY
gh secret set REPOSE_RELEASE_SIGNING_KEY --env release < key.pem
rm key.pem
```

Ship the new `install.sh` (a web deploy) before the next tag. Releases
signed with the old key then fail to verify; add their `checksums.txt`
hashes to `pinned_checksums` in `install.sh` in the same change, the way
v0.1.0 to v0.1.27 are pinned.

## Where the queue lives

In the repository's common git directory (`.git/release-queue/`), shared by
every worktree of the checkout and by no branch: `entries/` (one file per
branch, named after it percent-encoded), `releases/` (one per release: base
commit, merged commits, targets), `ids` (reservations), `band` (which ids
this machine may reserve, when it is set), and `tmp/` (writes in progress —
nothing in `entries/` is ever a temp file, because a reader took one for a
second entry of the same branch). Queueing never makes a commit on any
branch, so it cannot conflict.

An entry is `key=value` lines, one line per field, and a reader takes the
**last** line that sets a key: free text (`live=`, `note=`, `said=`) is
written before the fields the tool depends on, and a note whose own text
began `state=released` used to take the branch out of the queue
(DECISIONS I-758). `entries()` in the script is the only reader of the
directory and accepts a file when its own `branch=` encodes back to its
name, so a stray left by an older copy of the script is ignored.

This state is one machine's and does not cross to the other: `where` is
the cross-machine question, and a band keeps the two machines' id
reservations apart. The record that outlives the machine is the merge
commits on main, the STATUS line and `docs/releases/<release>.md`.
