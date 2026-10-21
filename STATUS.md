# repose on a machine you own: where this stands

Last written 2026-10-21. This branch is `spine-status`; the work it
describes is on `wrapper-spine`, which is parked and deliberately **not in
the release queue**. `main` has never been touched by any of it.

If you are picking this up cold, read this page and then
[`docs/proposals/2026-10-19-wrapper-only.md`](docs/proposals/2026-10-19-wrapper-only.md)
for why the shape exists, and
[`docs/workstreams/SPINE.md`](docs/workstreams/SPINE.md) for the evidence
behind every claim below.

## What it is

repose runs a project on a plain Linux box **you** own, over ordinary ssh.
No microVM, no SSH gateway, no account, no server of ours in the path. The
project's files live in a btrfs subvolume on that box, so one command puts
them back after an agent wrecks them.

It exists because every hosted seat loses $5 to $10 a month even on Hetzner
(`DECISIONS` I-642), and because KVM was never a feature: it was the price
of putting several tenants on one machine we rent. One box, one tenant, and
that price is not owed (I-812).

## Does it deliver the promise?

The promise is one sentence: *one command turns your laptop's working state
into a dev machine an agent can wreck safely, and it comes back.* On a real
Debian 12 box, `ops/dev/spine-e2e.sh run` exits 0 with all of this:

| | |
|---|---|
| the dirty checkout arrives | 5 files and 1 commit, untracked files and the gitignored `.env` included |
| the logins arrive | `Logins copied: gh, git` — the agent commits and pushes (`ad9e7fe the agent commits`) |
| the project's runtime arrives | `✓ Installed nodejs_22`, then on the box `node --version` → **v22.23.2**, and the project runs |
| a point costs nothing | **8 ms** to take, 32 KiB at rest, then only the blocks that change |
| a rewind works | **158 ms** for 30,000 files; the code, the untracked files and the dev database all come back |
| a rewind is reversible | the state it replaced becomes a point of its own, named in the result line |
| you can get back to it | `attach`, `sync`, `ps`, `ls`, `status`, `config`, `rewind` all reach the box |

Three passes got it there. The first built the shape. The second came out of
an audit that asked whether the shape delivered its own pitch and found 27
ways it did not — on a box you could not commit, could not push, had no
`.env`, and could not return to the agent you left. The third built the
toolchain, which is what made the sentence true rather than nearly true.

## What is decided

| Id | Decision |
|---|---|
| I-812 | A project may live on a plain box you own, reached by ordinary ssh, with no guest, gateway or control plane |
| I-814 | Undo is a btrfs subvolume; a rewind stops the project's processes first and is itself undoable |
| I-816 | Single-digit hosted price on hardware you bring, plus an identical self-hostable version, self-host first |
| I-830 | **Apache-2.0**, the whole repository |
| I-832 | The toolchain is Nix single-user: store shared at `/nix`, the project's profile inside its subvolume so a rewind undoes `config add` |
| I-834 | The hosted option's reachability is a byte-splice relay on the edge — designed, not built |
| I-836 | Free is everything self-hosted; a hosted account buys reachability, one registry, notification delivery and the update feed |

I-830 mattered more than it looks: the repository was already public with
**no licence file at all**, which means all rights reserved. There was no
free option in the only sense that counts.

## What is not built

- **Reachability.** A box behind NAT is yours to reach, with Tailscale or
  your own forwarding. This is also the only thing a paid account would
  sell, so there is no paid product until I-834 exists.
- **The price.** It waits on one measurement: `relay_bytes_total` has been
  counted by the gateway since it shipped and nobody has read it. At
  Azure's $0.087/GB, $5 less fees buys about 49 GB a month. Read the
  counter, then put the relay on a box with an unlimited port.
- **The project's own dependency install.** No `npm ci`, no `uv sync`:
  that runs the project's lockfile scripts on a box where the agent already
  has root. A decision, not a default.
- **A database.** Yours to run, and outside what a rewind undoes, along
  with anything installed outside the project's subvolume.
- **`repose config remove`** on an owned box. The profile only grows; the
  rewind is the way back, and the command says so.
- **No released `repose-agent`.** `machine add` needs `--agent-url` with a
  hash until a tag publishes the asset. `.goreleaser.yaml` builds it.
- **`repose attach` is held by tests, not by a session**, because attaching
  needs a terminal. `run -d`, `sync`, `ps`, `config` and `rewind` were
  driven against a real box.
- **macOS is not a box**: no btrfs, no systemd. The install refuses it.
- **One tenant, one trust boundary.** There is no hypervisor between the
  agent and the box: an agent with full permissions reaches everything that
  login reaches, the rewind points included. `docs/SECURITY.md` says so.

## How to pick it up

```
git checkout wrapper-spine
ops/dev/spine-e2e.sh up      # a throwaway Debian box in docker, repose installed on it
ops/dev/spine-e2e.sh run     # a dirty checkout, a run, a wreck, a rewind, and the checks
ops/dev/spine-e2e.sh down    # remove it
```

Against a box you already have, which needs no docker and no disk here:

```
SPINE_E2E_SSH=me@192.168.1.40 SPINE_E2E_KEY=~/.ssh/id_ed25519 ops/dev/spine-e2e.sh up
```

It refuses rather than filling the disk (it filled this machine's root disk
once), and `down` never removes a box you named yourself.

The unit tests need a real btrfs store for the measured half:

```
sudo ops/dev/undo-testfs.sh --up
REPOSE_UNDO_TESTFS=/mnt/nixstore/repose-undo-testfs go test ./internal/undo/
sudo ops/dev/undo-testfs.sh --down
```

## What I would do next, in order

1. **Read `relay_bytes_total` off the live edge.** No code, and it gates the
   price and therefore the whole paid half.
2. **Decide the licence is really out.** It is committed on this branch and
   not on `main`. Until `main` carries it, the public repository is still
   all rights reserved.
3. **Publish `repose-agent` in a release**, so `machine add` works with no
   flags.
4. **Build the relay** (I-834) if the measurement says a single-digit price
   survives it. Direct-first, never on the WireGuard hub.
5. **Then decide whether the hosted microVM shape stays at all.** Nothing
   on this branch forces that choice, which was the point of parking it.

## Open questions only the owner can answer

- Does this become its own product, or a second shape of repose? The branch
  is built so either is possible; the docs currently present it as a second
  shape.
- Is the free half's control plane self-hostable too, or is "bring your own
  reachability" the honest free story for good? I-816's no-feature-gates
  rule arguably demands the former.
- What install count and conversion rate make a single-digit price a
  business? I-816 says that number goes on paper before the paid half is
  built, and it has not.

## Code this branch adds

```
internal/undo/          the undo backend: btrfs points, rewind, fork, gc, the quiesce check
internal/machine/       the on-laptop registry of machines you own, and their ssh blocks
cmd/repose-agent/       the command on the box, JSON over ssh: points, env, tools, quiesce
internal/cli/spine*.go  machine, rewind, config and the run/attach/sync/ps paths
ops/install/            machine-install.sh, what `repose machine add` runs over ssh
ops/dev/spine-e2e.sh    the end-to-end harness
LICENSE                 Apache-2.0
```

`hostd`, `guestd`, the gateway, the api and billing are untouched: the
hosted product on `main` works exactly as it did.
