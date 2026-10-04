# Releasing: the queue and the release session

Several agents build on this repository at once. Each one works on its own
worktree branch and queues the branch when it is done; one session, the
conductor, merges the queue into main in verified batches, and ships
what main holds when the owner asks for a release. DECISIONS I-416
records why. `ORCHESTRATION.md` covers the rest of the conductor's job;
this file is the part between "a branch is done" and "it is live".

## For an agent building a change

1. Start in a worktree, never in the main checkout:
   `git worktree add ../kanali-<slug> -b <slug> main` and work there.
2. Claim the work in `docs/workstreams/STATUS.md` (CLAUDE.md).
3. Reserve decision ids before writing them:
   `ops/dev/release-queue id [N]`. It reads every local branch, every
   worktree's uncommitted `docs/DECISIONS.md` and the reservations, under
   one lock, so two agents never write the same `I-<n>`.
4. Finish the change with the evidence its checklist names. Then commit,
   `git merge main`, rerun the checks the merge touches, and queue:

   ```
   ops/dev/release-queue add --live "repose start of a stopped e2e project: under 12 s"
   ```

   `add` refuses a dirty tree, a branch with nothing main lacks, and a branch
   that conflicts with main anywhere but `docs/DECISIONS-INDEX.md`. It
   records the commit, so later commits need another `add`; that works
   while the branch is in a cut too, and the conductor takes the new
   commits with `resume` or in the next cut. It prints what the branch
   ships as (below).
5. Stop there. Don't merge into `main`, push, tag, publish a base or switch
   a host. If the conductor asks for a change, make it on the same branch
   and `add` again.

`ops/dev/release-queue ls` shows the queue. A branch that moved after it
was queued is marked; queue it again or the release takes the old commit.

## What a branch ships as

`add` reads the branch's diff against main and names the deploy targets it
reaches. Go packages count through `go list -deps`: a change to
`internal/obs` reaches every server binary but not the CLI.

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

## For the conductor: integrating into main

Main moves in two steps (owner, 2026-10-03). Integrating merges verified
branches into main **on this machine only**: agents branch from it and
the owner sees it with `git fetch repose`, but nothing reaches users.
Releasing pushes main and ships it, when the owner asks. Nobody pushes
main between releases, the conductor included: GitHub's `main` is what
Coolify deploys, so any push ships every integrated change at once.
GitHub CI runs only on that push, so the verification below is the only
gate until then.

Integrate whenever branches are queued; it needs no owner approval.

1. **See what is waiting.** `ops/dev/release-queue ls`. Ask each agent
   with a branch in flight (`ListAgents`, `SendMessage`) whether it is
   about to queue. Sessions get renamed: a send that fails with "no agent
   named" means the name changed, not that the session ended. Run
   `ListAgents` again and match the `[ref]`, and tell agents your current
   name, since their replies to an old one are lost.
2. **Cut.** `ops/dev/release-queue cut` makes `~/kanali-r<date>-<n>` on
   branch `release/r<date>-<n>` from main and merges every queued branch
   in queue order, each with `--no-ff`. A conflict that is only the
   decisions index is settled by regenerating it. Any other conflict
   stops the cut: resolve it in the batch worktree (or send it back to
   the branch's agent and `drop` the branch), commit, then
   `ops/dev/release-queue resume <batch>`. `abandon <batch>` undoes a cut
   and puts its branches back in the queue.
3. **Freeze when you verify.** Commits that arrive after the cut wait for
   the next batch unless they fix this one; each one taken in means
   verifying again.
4. **Verify the merge, not the branches.** In the batch worktree, for
   everything the batch ships as:
   - always: `python3 ops/dev/decisions-index.py --check`; `go build ./...`,
     `go vet ./...`, `golangci-lint run ./...`; `go test -race ./...`
     with a real Postgres for the api packages (on a repose guest, unset
     `REPOSE_PROJECT REPOSE REPOSE_HOOK_AGENT` first, and keep `TMPDIR`
     at `/tmp`: a longer one pushes the hostd fakes' unix sockets past the
     108-byte limit); `go test ./internal/cli
     -run TestDocs`; the `docs/CHECKLIST.md` greps;
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
5. **Move main, locally.** `git -C ~/kanali merge --ff-only
   release/<batch>` (the main checkout must be clean), then
   `ops/dev/release-queue done <batch>`, which removes the batch
   worktree. Do not push. `ls` now lists those branches as `on-main` and
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
   is the pushed main commit), `cli` tag (the next `v0.1.N`, with notes
   from every branch since the last tag, in the release-notes style), `host`
   switches (RUNBOOK "Switch a host to main") and the `edge` switch.
   A `cli` tag pushed before the owner has set up the `release`
   environment and its signing key stays an unpublished draft ("The
   release signing key" below).
4. **Check it live.** Run each released branch's `--live` check on
   throwaway `e2e-*` projects, never on the owner's projects, at most two
   alive at once. Paste the output into the release record.
5. **Record.** One line in `docs/workstreams/STATUS.md` per release: the
   branches, the targets shipped with versions, the live evidence,
   anything not done. It goes into main with the next batch, not as a
   push of its own. Tell each agent whose branch shipped; it may then
   delete its worktree, or the owner does.

## The release signing key

A `v*` tag runs `release.yml`: GoReleaser uploads a draft, and the `sign`
job signs `checksums.txt`, attests the archives and publishes the draft
(DECISIONS I-430). A tag whose `sign` job fails leaves a draft that
install.sh never sees; fix the cause and re-run the job from the Actions
page. Notes are added after the tag as before (`gh release edit`).

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
branch), `releases/` (one per release: base commit, merged commits,
targets), `ids` (reservations). Queueing never makes a commit on any
branch, so it cannot conflict. The record that outlives the machine is the
merge commits on main and the STATUS line.
