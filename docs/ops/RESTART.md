# Start here after a stop

Written 2026-11-20 by the mac-mini mini-conductor, at the end of a shift
that ended on a weekly usage limit. It is for the sessions that restart
after the limit resets (**2026-11-22, 07:00 UTC**) and for any session
that comes to this repository cold. It says what is true, what is done,
what is not, and what to do first.

Read this, then `docs/ops/CONDUCTOR-HANDOFF.md` for the queue and the
live plane, then `CLAUDE.md` and `docs/README.md`. Do not read the whole
board before starting: §5 carries the inventory, and the board's own
listing is unreliable (§8).

## 1. The state in one screen

| | |
|---|---|
| Published CLI | **v0.1.53**, `releases/latest` resolves to it |
| Published base | **2026.11.20.1** |
| Published appliance | **appliance-2026.11.20** (`--latest=false`), pinned by v0.1.53 |
| `origin/main` | `f6aab4f2` |
| local `main` | **ahead of origin** at `0089a8fd` (r20261120-12, -13 integrated, unpushed) |
| `mac-mini` | fully merged into `main`; the branch is done |
| Unmerged branches | `landing-overhaul` (4 commits, the web work), `tmpdir-split` (1 commit) |
| Open pull request | **#99**, the conductor handoff, queued and uncut |
| Host switch / edge switch | **none**, and none needed by anything released |
| Production enforcement | billing enforced, `settings.billing_enforce` true |
| The Own hardware plan | `available: true`, $10, **7-day trial**, purchasable today |

**What shipped today.** A 197-commit repair batch (`r20261120-10`) for
bring your own hardware, then two releases: **v0.1.52**, which pinned
the *previous* appliance and therefore carried only the install-side
fixes, and **v0.1.53**, which pins `appliance-2026.11.20` and is the
first release where the guest-side repairs reach a customer. If you read
one lesson from this shift, read §6's first line.

## 2. The owner's standing instructions

Given 2026-11-20 after the release, in their words where the words
matter:

- **Work on the website, now, and do not wait for a launch.** *"We're
  essentially just gonna work on the website part of things, even if
  nothing has launched yet. Just work on it. It's okay."*
- **Publish what works.** *"We also want to publish everything we've
  already created right now that works."*
- **Do not invent blockers.** *"It does not matter. It's not stable. No
  user is buying. Literally, don't make that a block. Don't invent
  blockers."* This **retires the customer review as a gate**. It is not
  a licence to ship something false; it is a refusal of ceremony.
- **The homepage states why somebody would do this, and the rest is
  cut.** Real features over an hour of polish; the value proposition
  first.
- **Hand over properly.** This document exists because they asked for
  it: a session after the reset should *"start perfectly where you
  ended and not scrambling"*.

Earlier instructions that still hold: never run `nix flake check` with
its builds on this box (§6); the owner is the only one who spends money
on the AWS Mac; `git` identity is never overridden; work in a worktree;
`main` moves only through the release queue.

## 3. What to do first

**If you are the release conductor** (`~/projects/factory`, `main`,
rename to `conductor`):

1. **Push `main` and release what is integrated.** Two batches are
   local-only, including the `claude-golden` fix. The owner has approved
   publishing what works; `docs/ops/RELEASE.md` is the procedure.
2. **Cut PR #99** (its own handoff) once `main`'s run is green. The
   guard that refuses while a run is unknown has been right twice; do
   not override it.
3. **Queue `tmpdir-split`.** One commit, documentation only.
4. Then take branches from the mini-conductor as they arrive.

**If you are the mac-mini mini-conductor** (`~/kanali-mac-mini`, branch
`mac-mini`, rename to something stable):

1. **Read §5 and pick from the web group.** That is the owner's current
   priority and `landing-overhaul` is where it starts.
2. **Do not relaunch the customer review.** It is no longer a gate
   (§2). `~/kanali-review2-cli` and `~/kanali-review2-dash` exist with
   briefs at `~/briefs/review2-*.md` if a review is ever wanted again.
3. **Three cards are live defects on a published CLI** and are the
   strongest candidates outside the web work: **ENG-102**, **ENG-138**,
   **ENG-70**. See §5.

## 4. What the mac-mini program finished

All of this is released and needs no further work:

- **Guest starts on a Mac** (I-1408, I-1416): hostd owns the two phase
  deadlines, the appliance allows ten minutes each, the guest's paths
  wait outlasts them. Verified five of five on hardware.
- **The install** (I-1414, I-1422, I-1424): the 2 GB download is out
  from under a 30-second api timeout and resumes; the disk guard runs
  before any fetch **and** before the host is minted, so a refusal
  leaves no artefact and no host name; the join argument comes in off
  the command line (`--join -`, `--join-file`).
- **The ask** (I-1404, I-1406): `--timeout` bounds the open, the
  question is idempotent, exit 7 exists. Verified on hardware.
- **Removing a host that has stopped answering** (I-1418, I-1420), with
  three sentences that named impossible recoveries rewritten.
- **The park's key path** (I-1386, I-1394): the vsock dial fix; verified
  parking and unparking across a clean stop on real hardware.
- **The restart promise withdrawn** (I-1434): every page that said a Mac
  restart keeps your work now says machines start fresh.
- **Triggers** (I-1436): `triggers add --route` on a route that already
  answers asks first.
- **Gates**: `done-check`'s `internal/cli` filter widened from two tests
  to eight; CHECKLIST rows for a deadline's value, for a narrowing
  selector and a silent skip; `decisions-index.py` hardened against two
  heading shapes it could not see.

## 5. What is not done, grouped

**73 cards are open.** They are not all worth doing; they are all worth
reading once in a group. Counts are of open cards in each theme.

**Web and dashboard (18) — the owner's current priority.** The home
page overhaul (**ENG-82** hero copy and cut sections, **ENG-83** a real
capture in the hero, **ENG-86** a review pass on the proposal) plus the
price being unfindable anywhere public (**ENG-47**), and the dashboard
and docs findings a customer hit: a sign-in page that is white in dark
mode (ENG-53), a question unreadable on a phone (ENG-54), a Mac listed
with a line of gibberish and a Remove button a phone cannot reach
(ENG-88), a destroy confirmation that flashes past in four seconds
(ENG-52), a webhook you can set but not watch (ENG-56), a project page
that says less than the list (ENG-65), no way to tell which machines run
on your own Mac (ENG-68, ENG-116), a refusal rendered behind the dialog
that raised it (ENG-136), where a signed-out visitor should land
(ENG-137), a troubleshooting page with nothing about a Mac install
(ENG-57), the one escape from a stuck host documented nowhere (ENG-64),
a command on the dashboard you cannot read (ENG-63), and one clause of
mine that talks about us (ENG-140).

**Live defects on the published product (3, and read these first).**

- **ENG-102, high, verified on `main` today**: `repose host add --json`
  marshals `install.argument` and `join_token`, so the join bundle — which
  carries a **WireGuard private key** — lands in the transcript of any
  agent that reaches for `--json`, and `cli.md` documents that it does.
  The failure-line leak of the same key was fixed today as I-1424 on
  exactly the reasoning that the argument must stay off the command
  line; this path was missed. The fix is a design call about what
  `--json` emits, which is why it is not already done.
- **ENG-138, urgent**: a restart of the Mac loses what the machines were
  doing, because macOS gives the launcher about one second against the
  360 it asks for. Measured. The docs now say so, so this is a product
  gap and not a false promise. Three unknowns are named on the card and
  only the Mac can answer them.
- **ENG-70, urgent**: a guest takes about 58 s to start on a Mac and
  should take seconds. ENG-103 (seventeen minutes, not the four the page
  promises) and ENG-100 (told it did not answer within a minute after
  six and a half) are the customer-facing shapes of the same thing.

**Owned-host sizing (10, a coherent workstream).** ENG-90 to ENG-98 plus
ENG-84: what a size means on hardware the user owns — a memory figure
rather than a class — through the api, scheduler, hostd wire, CLI,
dashboard and docs, with the figures coming from one source. Research is
done and recorded; nothing is built. **ENG-91** is the entry point.

**The Mac path's remaining defects (14).** A host that is online while a
unit failed (ENG-79), an architecture field carrying a nix store path
(ENG-89), burst on by default with no gate (ENG-73), adding a Mac
quietly taking over every new project (ENG-107), an install that refused
after the customer freed what the docs asked for (ENG-87), a dead
install that says `waiting` for ever (ENG-60), a broken project whose
suggested fix cannot work (ENG-59), a published appliance built from a
dirty tree burning 15 percent of a core (ENG-110), the operator's
`mark-lost` not resolving an owned host by name (ENG-121) and what it
tells the user (ENG-122), an unstarted guest image (ENG-125), guestd's
own CPU unmeasured (ENG-126), a 15-minute image rebuild per hostd change
(ENG-128), and a whole-store GC exhausting its own share (ENG-135).

**Platform and process (18).** CI running an 8.5 to 12 GB evaluation on
a 16 GB runner and losing it (**ENG-144**), `internal/cli` at 485 s
against a 600 s default (ENG-115), the api tests' flat 30-second wait
(ENG-113), checks that cost more than they catch (ENG-108), a red
release-assets run reaching nobody (ENG-99), a repository-input test
nothing accounts for (ENG-142), a withdrawn promise left in code
comments (ENG-143), and the rest.

**Held, blocked or the owner's (5).** `7db572b5` (ENG-16), the plan's
public copy — **see §7**; releasing the AWS Mac when it is no longer
needed (ENG-21); the park on owned hosts, blocked on ENG-138 (ENG-2);
the single-host account refused with no way out (ENG-131); 28 commits on
`main` carrying an unverified git identity (ENG-5).

## 6. The traps, with the commands

**Four things reported failure in zero seconds this shift and none of
them was a defect in the code.** Check the instrument before the
subject.

- **`golangci-lint` is not on `PATH` outside the dev shell.** A
  verification run reports `FAIL lint` instantly.
  `/nix/store/ilazks9wgdw6vbc6q8gb2i4by1r1cr73-golangci-lint-2.13.2/bin`
- **`go test -race` needs `CGO_ENABLED=1`** or it errors without
  running. `gcc` is present at `/usr/bin/gcc`.
- **The api and db tests report `ok` in milliseconds while running
  nothing** when there is no `DATABASE_URL` and no `initdb` on `PATH`
  (`internal/db/testdb/testdb.go:71`). `internal/api/http` is 0.020 s
  skipping and 69 s running. Postgres:
  `/nix/store/2hjw5mxi2q3z3cjcb5h7gh8yrmprkgxl-postgresql-and-plugins-16.15/bin`
- **A watcher's own exit code is not the subject's.** `grep -c` returns
  1 when it finds nothing, so a watcher looking for failures reported
  failure *because there were none*.

**Never run `nix flake check` with its builds here.** It wants 8.5 to 12
GB in one process on a 16 GB box and has cost three sessions over two
nights. One check at a time, capped, under the lock:

```
flock /mnt/nixstore/repose-ws/nix-build.lock \
  systemd-run --user --scope -p MemoryMax=6G --quiet -- \
  env TMPDIR=/mnt/nixstore/tmp choom -n 1000 -- \
  nix build --no-link -L ./nix#checks.x86_64-linux.<name>
```

- **`TMPDIR=/mnt/nixstore/tmp` is for nix and VM tests only.** The root
  disk has about 4.5 GB free and VM tests write their images into
  `TMPDIR`. **Do not export it for `go test`**: one test sorts
  unnormalised temporary paths, so `/mnt` reds it and `/tmp` greens it,
  deterministically (fixed in r20261120-13; the shape remains).
- **`nix build --rebuild` errors** when there is no valid prior output
  rather than forcing a build. Plain `nix build --no-link -L`.
- **A VM check needs the box quiet, not just the lock.** `guest-base`'s
  I-264 probe writes nothing under load: 7.45 s and empty against 2.25 s
  and correct, same tree.
- **`guest-store-whiteouts` fails here and on `main` identically** and
  is ENG-135, not your branch.
- **The six `aarch64-linux` checks need the Mac's builder** (`macmini-l1`
  at 192.168.64.2, in a window the spark grants) and have never run on
  this box.

## 7. What is held, and by whom

**`7db572b5` — the Own hardware plan's public copy** (the landing's
pricing cell, `/docs/billing`'s section, `lib/plans.ts`'s
`OWN_HARDWARE`). The owner fenced it on the customer review passing;
that gate is retired (§2). **It should not be cherry-picked as it
stands**, because the homepage it edits is being rewritten: the plan's
price and what it buys belong in the new homepage, stated clearly, which
is what the owner has asked for. Whoever does the web work closes
ENG-16, ENG-47 and ENG-129 by doing it properly rather than by merging
this commit.

`landing-overhaul` (4 commits) is the web work's starting point and was
held for the same reason. It is now wanted.

## 8. The board, and its one defect

The board is Kaneo, project `ENG`, through the `project-board` skill.
Write it at two moments only — when you claim and when you stop.
`Holder` is a role, never a session name. Never assign anything to the
owner; move it to `Needs you` with the question in the note.

**`kaneo ls` under-reports and will mislead you.** It printed
"Open (9)" on a column holding **73** cards. Get the real inventory
through the Kaneo API (`list_tasks`, `projectId wwwsmens0qqpm8jz5lfig816`,
`status to-do`, `limit 100`) and read §5 rather than the listing.

## 9. Questions only the owner can answer

In `Needs you`, each with its evidence on the card. None of them blocks
work:

- **ENG-102's shape**: what `repose host add --json` should emit, given
  that a script needs the argument and an agent's transcript must not
  carry a key.
- **ENG-48**: the frost under a frozen row reads as a half-loaded image
  at 1x in dark mode. Its colours were measured from photographs for a
  decision with an id, so changing it is taste on a settled thing.
- **ENG-131**: on a plan that sells no memory on repose's servers, a
  customer whose own host is still installing is refused with no way
  out. The three ways out each change what the plan sells.
- **ENG-5**: 28 commits on `main` carry an unverified git identity; the
  owner may want them rewritten.
