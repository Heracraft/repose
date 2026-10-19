# repose as a wrapper only (proposal, 2026-10-19)

**Status: analysis. Nothing here is decided; any of it becomes real only
through `DECISIONS.md` entries.** The owner asked whether repose can
provide value as just the wrapper, on machines people already own, with
no SSH gateway and no KVM — a third shape beside the two in
`2026-10-10-bring-your-own-host.md`, and a smaller one than the shape A
that I-647 chose.

## Summary

| Question | Answer |
|---|---|
| Is the shape sound? | Yes, and for a better reason than cost. KVM is the price of multi-tenancy, not a feature of the product. One machine, one tenant, and the reason for it goes away |
| What does it delete? | About 39,000 lines of host and guest plumbing, host-01's bill, the $5 to $10 lost per seat (I-642), and five of the six work items shape A still carries |
| What survives? | The 42,584-line CLI nearly whole, most of the api, and nine of the thirteen feature docs — everything a user touches daily |
| What breaks? | Undo, `repose config add`, and the whole plan table. Undo is the only one that matters |
| Which undo? | A btrfs subvolume on a plain file. **Rewind point 0.13 s, fork 0 s, rewind of a wrecked 37,000-file project 2.19 s, loopback overhead 3 percent** — measured, `research/wrapper-undo.md` |
| What does undo cost to build? | 600 to 900 lines and 5 to 8 days, against the 2,238 lines of LVM and Blob code it replaces |
| Biggest risk | The moat moves from hardware nobody can copy in a month to software with free substitutes |
| Recommendation | Build it beside the hosted host, not instead of it. Prove undo first, and let twenty users on their own boxes decide whether hostd lives |

## 1. Why the shape is right

`DESIGN.md` §3 says isolation between users "is the hypervisor boundary
(Cloud Hypervisor on KVM), never a container boundary". That rule is
correct, and it exists only because strangers share host-01. A box one
user owns has one tenant on it. The rule does not bind, and neither does
the microVM: it was never a feature a user asked for, it was the cost of
putting several users on one machine we rent.

The SSH gateway is the same. It exists because a guest has no address of
its own on our host (`interfaces/ssh-gateway.md`). A box the user owns
has an address, or it is behind NAT and reachable over Tailscale, which
is their tool and not ours to build.

This is strictly cheaper than I-647's shape A. That shape keeps hostd,
the NixOS host configuration and the WireGuard hub, so its work list is
six items long: a `metal` provider, a one-disk disko layout, host
ownership in the scheduler, nftables for a stranger's host on our hub,
S3 snapshots, and unattended host switches. The wrapper-only shape skips
five of them and reduces the sixth (snapshots) to the undo question
below.

It also answers the product risk that proposal named: "NixOS takes over
the disk, a much bigger ask than Coolify's *any Linux with Docker*".
Here the ask is an SSH login and a package.

## 2. What survives and what goes

| Component | Lines (non-test) | Fate |
|---|---|---|
| `internal/cli` | 42,584 | Survives nearly whole |
| `internal/api` | 20,611 | Survives without the scheduler, seats, waitlist and host capacity |
| `internal/guestd` | 8,524 | Becomes the agent on their box, without `freeze` and vsock |
| `internal/hostd` | 18,551 | Gone |
| `nix/` host and guest configurations | 16,874 | Gone, or kept only for the hosted option |
| `internal/gateway`, `vsockrpc`, `ca` | 3,374 | Gone |

Nine of the thirteen feature docs do not care what the machine is:
projects, run-and-attach, sync-at-launch, agents, browser, secrets,
notifications, ports-and-previews, status-and-logs. Those are the ones a
user touches every day. The plumbing that would be deleted is the part
no user has ever seen.

The transport is a smaller change than it sounds. The CLI already
reaches a project as `ssh <slug>.repose` through a generated block
(`internal/cli/cert.go:342`), and the gateway appears in it as one
constant, `gatewayHost` at `cert.go:334`. Pointing a block at a box the
user owns is a `HostName`, a `User` and a key.

## 3. Undo is the one thing that must be replaced

The 2026-09-26 competitive research narrowed the product to **undo plus
the dirty checkout**. Item 2 of
`2026-09-26-feature-research-backlog.md` is the automatic rewind point,
costed there at 4 to 6 days on LVM thin snapshots, with the demand
written down: openai/codex#9203 ("Please make /undo back", 513
reactions), anthropics/claude-code#87575 (`/rewind` misses Bash edits),
HN 45505879 ("Claude has dropped my dev database about three times").

So undo is not a feature to port later. It is the feature, and it is the
only one that dies with the VM. Everything else on the list is a wrapper
concern.

**The bar to clear** is what `features/snapshots.md` gives today: a
crash-consistent copy of the whole volume — code, untracked files, the
dev database, Docker volumes and the configuration revision — restorable
onto any host, surviving the host dying because it is streamed to Blob.

### The five candidates

| | Covers | Needs of their box | Speed | Code | Days |
|---|---|---|---|---|---|
| **btrfs subvolume** | Everything in the subvolume: untracked files, the database, `node_modules`, Docker's data if it lives there. Not system packages outside it | `btrfs-progs` and the mainline module. A spare disk or partition, else a sparse file on the filesystem they have | **0.13 s a point, 0 s a fork, 2.19 s a rewind** (measured) | 600-900 | 5-8 |
| **ZFS dataset** | The same, plus `zfs diff` to answer "what did the agent touch" | An out-of-tree module, by DKMS on most distros | Unmeasured; comparable | 600-900 | 5-8, plus support |
| **Container overlay** | More: system packages too, because `apt install` lands in the overlay | Docker or Podman | A commit per point is seconds, not milliseconds; layers pile up | Large | 15-25 |
| **Shadow git** | The project directory only. Binary files re-stored whole each point | Nothing | 0.09 s when two source files changed, **2.48 s and 447 MB** when the database did | 200-400 | 2-3 |
| **Keep the microVM** | Everything, memory included (I-685 is already built) | `/dev/kvm`, and NixOS takes the disk | Today's numbers | 0 new | 0 |

### Recommendation: btrfs, on a file when there is no spare device

Three measured facts decide it, all in `research/wrapper-undo.md`:

1. **A point is free.** 0.13 s and 32 KiB, and afterwards it costs only
   the blocks that change — 100 MiB when 100 MB of a database is
   rewritten. Cheap enough to take on every `repose run "prompt"`, which
   is what the automatic rewind point needs and what the LVM version
   could not quite afford at 10 to 15 s.
2. **A rewind brings back what a file checkpoint cannot.** 2.19 s for a
   project of 37,218 files: `src` and ten deleted packages back, a 200 MB
   database back to the byte, `git status` clean. This is the difference
   between repose's undo and `/rewind`, and it is the difference the
   codex and claude-code issues are complaining about.
3. **The loopback file is not a tax.** btrfs in a sparse file on ext4
   costs 3 percent on a sequential write and nothing measurable on 5,000
   small files. That is what makes the ask "an SSH login and a package"
   instead of "give repose a disk", and it is why btrfs beats ZFS here
   even though ZFS has the nicer commands: btrfs is in the mainline
   kernel, and an out-of-tree module that breaks on a kernel upgrade of a
   machine nobody on the team can log into is a support channel, not a
   feature. ZFS is worth using when the box already has a pool; it is not
   worth requiring.

Two things come with the choice:

- **A rewind stops the project first.** With a writer running inside the
  subvolume, the swap succeeds anyway and the writer goes on writing into
  a subvolume no path reaches. Measured. So `rewind` stops the agents,
  swaps, and restarts them — and a rewind that forgets this loses writes
  silently, which is worse than refusing.
- **Where `/nix` lives becomes a product question.** Put the project's
  Nix profile inside the subvolume and a rewind undoes `repose config
  add` too, which is the behaviour the current product has. Share one
  store between projects and it does not, but the disk and the downloads
  are shared. This wants a decision, not a default.

**Rewind points are not a backup.** They live on the user's disk, and
that disk dying takes them. Today Blob covers that. The honest line is
that git is the offsite copy, with `btrfs send` to the user's own bucket
as a later option — item 5 of the shape A list, demoted from required to
optional.

## 4. The other two breakages

**`repose config add bun` is a NixOS switch** into a running guest
(`features/config.md`). On a box the user owns it becomes Nix in
single-user mode with a profile or a devenv per project, inside the
snapshotted subvolume. Honest degradation: the environment is still
declared and still reproducible, and it is no longer a whole-system
closure switched atomically.

**The plan table stops meaning anything.** Nobody stops a machine they
own to save money, so the memory, disk and egress columns of
`features/pricing.md` describe nothing, and `stop`/`start` lose half
their purpose. Flat software pricing is a different number from $49 to
$169 — this needs its own entry, and it needs checking that the business
survives it before hostd is deleted.

## 5. What the business trades

Today every seat loses $5 to $10 a month even on Hetzner (I-642), and
the funnel is capped by hardware: fifty users means a second host and a
bigger loss. In the wrapper shape the cost of serving a user is a row in
Postgres. **That is the real prize — not the saved rent, but that repose
can be handed to fifty people this week and find out whether anyone
cares.** It is the shrink option the 2026-09-26 research left undecided,
and it is a legitimate answer to it.

The price of that: the moat moves from capital to software, and the
substitutes are free. DevPod, Coder and Sculptor are in this space, and
a competent developer writes a worse version with Tailscale, tmux and a
shell script in an evening. Four things have to carry the price on their
own:

1. Undo that covers the database and the untracked files, not just the
   files git tracks.
2. The dirty checkout at launch (`features/sync-at-launch.md`), which is
   the other half of the 2026-09-26 narrowing.
3. The five agents wired to notifications and hooks, which is
   `internal/guestd/hooks` and the wrappers, and which nobody reproduces
   in an evening.
4. The browser handoff, for the login or the captcha an agent cannot pass
   (`features/browser.md`).

If those four do not justify the price, the hosted host was not saving
the product either.

## 6. The model: single digit hosted, identical self-hosted

Coolify's shape, and the owner's call on 2026-10-19: the control plane
hosted for a single-digit monthly price, on hardware the user brings,
and the identical software fully self-hostable for nothing.

It is closer than it looks. `snapshot.Blob` and `secrets.KeyVault` are
already interfaces with file backends beside the Azure ones, the control
plane already ships as a compose file on Coolify, and hosts already dial
out for everything, so nothing in the design assumes our cloud is on the
other end.

Four things the model demands:

- **No feature gates.** Every feature works with no hosted control plane.
  That rules out anything that depends on us holding something — hosted
  snapshot keys, api-side magic — and it is also what makes the
  self-hosted version trustworthy enough to be the funnel.
- **The licence is decided before the first public tag**, because it
  cannot be taken back. AGPL on the server with a permissive CLI stops
  someone reselling the control plane; Apache-2.0 is what Coolify chose
  and lives with. Either way the price is capped forever at "run it
  yourself for free".
- **Distribution becomes the job.** $9 times 30 users is $270, where the
  $49 ladder cleared about $1,400. The same money needs roughly 160
  subscribers, and most people who install it will never be one. The open
  repository is the growth engine, not a gift on the side.
- **Write the target down first.** At $9 and the few percent of
  self-hosters who convert, thousands of installs are the floor, not the
  ambition. That number decides whether this is a business or a hobby
  with users, and it should be on paper before the paid half is built.

**Order: self-host first.** No billing, no seats, no relay, no card. It
is the cheapest validation the product has ever had access to, and it
answers "will anyone install this" before anything is charged for.

What stays on our side is the whole of what the subscription buys: the
account and project registry, so `repose ls` spans every machine they
own; notification ingest and delivery; the agent wrappers and the
environment base kept current as five agents change weekly; and
**reachability** — brokering a connection to a box behind a home NAT.
That last one is the piece that must not be handed to Tailscale. Hand it
over and the hosted version has nothing left to sell.

## 7. Work in order, if it goes ahead

1. **Decide and build undo** (M, 5-8 days). The mechanism above, on one
   box, with the rewind-stops-the-project rule and a guard that deletes
   the oldest points before the filesystem fills. It goes first because
   it is the only load-bearing unknown; everything else is known work.
2. **The agent on their box** (M-L). `guestd` without `freeze` and
   without vsock, supervised by the init the box already has, with the
   NixOS assumptions in `system` and `sysdep` (1,701 lines) behind a
   second implementation.
3. **An install command** (S-M). One command from the laptop that opens
   an SSH session, installs the agent and the package, provisions the
   subvolume, and registers the machine. Where it sits in the CLI is the
   same placement question shape A had: a `machine` noun beside the
   project nouns, or under `config`.
4. **The transport** (S). `gatewayHost` becomes a per-project
   `HostName`; the certificate becomes an ordinary key, or stays a
   certificate signed by a CA on their own box.
5. **The api without a scheduler** (M). A project on a machine the user
   named, no placement, no seats, no plan memory gate.
6. **The environment story** (M). Nix single-user plus a profile per
   project, and the `/nix` placement decision from §3.
7. **Pricing** (owner). §4.
8. **Security review** (S-M). The trust boundary inverts: there is no hub
   and no shared host, so shape A's hardest item disappears, but the api
   must never let one user's command reach another user's machine. One
   line in `SECURITY.md` and its own tests.

Steps 1 and 2 are enough to use it yourself and to put it in front of
users. Steps 5 to 7 are what make it sellable.

## 8. What this does not answer

Each of these is its own `DECISIONS.md` entry when it is settled, and
none should be answered in code first:

- The licence, and whether the CLI and the server carry the same one.
- The install count and conversion rate that make the single-digit price
  a business, written down before the paid half is built.
- Whether the relay is ours or the user brings Tailscale — the one
  question that decides whether a subscription exists at all.
- The price of a hosted account, and whether the plan table is replaced
  or dropped.
- Whether the hosted host stays as the no-hardware option, or goes.
- Where the project's Nix store lives, and so whether a rewind undoes an
  environment change.
- Whether rewind points ever leave the user's box, and if so to whose
  bucket.
- macOS, which has neither btrfs nor KVM, and which some of "machines
  people own" are.
- What happens to a project when the machine it lives on is off, which is
  a state the api does not have today.
