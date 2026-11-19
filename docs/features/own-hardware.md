# Your own hardware

A user puts a machine of their own into their account and their projects
run on it instead of on repose's servers. Everything else stays repose's:
the control plane, the gateway their SSH goes through, the notifications,
the triggers. The hardware is a Mac mini on a desk or an x86 box in a
cupboard, and it takes that account's projects and nobody else's, ever.

The business shape is `docs/proposals/2026-10-10-bring-your-own-host.md`
and DECISIONS I-647 (shape A: the hosted control plane, hosts the user
owns). The program that builds it is `docs/workstreams/19-mac-mini.md`;
the decisions behind what is built are I-1364 (the plan), I-1366
(ownership, placement and the three routes), I-1368 (the hub treats a
host as hostile), I-1370 (`repose host` and what an offline host says)
and I-1372 (snapshots on the host's own disk). The order of the hardware,
the aarch64 outputs, the Mac appliance and unattended updates have ids
reserved in the program doc and no entries yet; this doc describes their
absence in words rather than pointing a reader at something they cannot
read (I-994). User-facing documentation is
`apps/web/src/content/docs/own-hardware.md`.

**Status.** The hosted half is built: the Own hardware plan, ownership,
placement, the three routes, `repose host`, the dashboard's Hosts page,
the hub rules, the offline wording, the snapshot store. The machine half
is not: there is no aarch64 guest base, no Mac appliance image and no
launcher, so nothing has joined an account yet and no
`host_install_command` has been recorded, which means every `repose host
add` today prints the token and the docs page rather than a line to run
(§3). Owned hosts do not update themselves; an operator switches them by
hand, and the timer that would pull a published host system is not built
and may ship after launch.

**The two figures the public page quotes**, measured by 19f on the lab
Mac and not on anyone's desk: an appliance given 12 GiB, which is what a
16 GB Mac's share is sized at, reports 11.6 GiB and 8 vCPU in its Hello
and leaves 9.6 GiB placeable after the eighth-with-a-2-GiB-floor reserve
(I-724). The image unpacks sparse in 21 s to 4.4 GB of real blocks (37
GiB apparent), peaking at about 6.5 GB while the 2.0 GB download is
still on disk, and the lab disk held 21 GB with the guest closure built
and one `large` volume created, before that machine had written
anything. The caveats the page does not carry: the appliance was 12 GiB
on a 24 GB Mac, and no guest had booted, so the cost of a machine that
is actually working is not measured yet.

## What the user sees

```
$ repose host add
rjt_Vb3nQ8... (the token, until an install line is recorded)
mac-mini: this is its join token. It works once, before Thu 20 Nov 14:05. What to do with it: https://repose.run/docs/own-hardware
```

Once an install line is recorded, stdout is that line with the token
already in it, and nothing else, so it pipes to the machine:

```
$ repose host add mac-mini
curl -fsSL https://repose.run/mac | sh -s -- rjt_Vb3nQ8...
mac-mini: run this on the machine you are adding. It works once, before Thu 20 Nov 14:05.
```

```
$ repose host ls
HOST      STATE   MEMORY  MACHINES  RUNNING  LAST SEEN
mac-mini  online  24 GB   3         1        just now
```

```
$ repose ls
PROJECT     SIZE   STATE    UP     AGENTS
todo-app *  large  running  2h14m  claude: working
todo-app: your host mac-mini has not answered for 14m.
```

The dashboard has the same three actions on a Hosts page and the same
sentence under a machine whose host is quiet.

## 1. The plan

The Own hardware plan sells one host and no memory, disk or egress on
repose's own servers; the hardware's own memory is the only cap on what
runs at once and its disk the only cap on what is kept. It sells no
seats. The price is in `docs/PRICING.md` and
`apps/web/src/content/docs/billing.md`, and is named nowhere else, so
there is one place to change it.

Two questions the rest of the api asks the plan, and nothing else
(`internal/billing/ownhardware.go`):

- **How many hosts may this account own**, which `POST /hosts` refuses
  past with `host_limit` and `detail.limit`. The figure is
  `billing.Plan.Hosts`.
- **May this account's machines run on hardware repose bought**, which
  is `billing.Plan.MemoryGB` and which placement reads rather than
  refusing on: an Own hardware account has a host of its own to be placed
  on, so a filter is the right shape and a refusal is not.

A billing-exempt account owns one host without a plan
(`billing.ExemptHosts`), because an exempt account is refused nothing
(I-1090) and the owner adding their own Mac must not have to sell
themselves a plan first.

## 2. Adding a host

`repose host add [NAME]` mints a single-use join token valid 24 hours and
prints the one step to run on the machine being added. The dashboard's
Hosts page does the same and shows the same step.

The step is the api's, never the CLI's: `install.command` is the
`host_install_command` setting with `%s` replaced by the token, so the
install path can change without a CLI release and the user-facing step
and `repose-admin hosts install-command` cannot drift. While no setting
is recorded the api sends `command: ""`, and a client then prints the
token and `install.url`, which is the public docs page. A client never
composes a line of its own: a line a user pastes has to be true.

Without NAME the api names the host `host-1`, `host-2`, … within the
account. Names are unique **per account**, not globally, so two users may
both call their machine `mac-mini` and neither is ever told about the
other's.

On the machine, the token reaches `hostd register` through
`/run/repose/join-token` and nothing else: `repose-register.service`
consumes it, writes `host.json`, `cert.pem` and `key.pem`, and deletes
it. Nothing about that path is cloud-init's, which is why it already
works on a machine that has none (`host-conventions.md`, "A host the user
owns").

## 3. Placement

A project carries `placement`, `own` or `hosted`, default `own`
(`api.md`, `db-schema.md`). `own` means the account's own hardware when
it owns any and the hosted fleet when it owns none. A project is pinned
to its host for life (DESIGN.md §3), so the field is read at the create
and at a re-placement and never after, and `PATCH /projects/:id` does not
take it, because nothing would honour it.

The two directions that must both hold: an owner's project goes to their
own hardware, and a stranger's project is never placed there. Owned hosts
are outside the seat count, the waitlist and the pool gauges, because
repose did not buy them.

A create for `own` while the account's host has not registered yet, or
cannot take the machine, fails the create op with `capacity` and a
message that names the host and its state, rather than quietly using the
fleet and billing for it.

## 4. When the host is offline

A Mac that is asleep, rebooting, switched off or off the network stops
answering. After 90 seconds of no heartbeat its state is `unreachable`,
and every surface says the same sentence, which is the api's own wording
for a placement it refused (`ops.ownHostRefusal`):

> your host mac-mini has not answered for 14m

A host of repose's says `its server is not answering` instead, because
that one is not the user's to fix. `repose ls`, `repose status`, `repose
run`'s wait and both dashboard surfaces use the one wording; the CLI's
copy is `hostUnreachableLine` and the dashboard's is
`$lib/offlinehost.ts`, and they say the same words.

The host states in the words a user reads, which the CLI and the
dashboard share (`hostStateWord`): `registering` is `waiting`, `ready` is
`online`, `unreachable` is `offline`, `draining` is `removing`, `retired`
and `lost` are `removed`.

A machine on a host that only lost the network keeps running and is
reachable again as soon as the host answers, with whatever the agents did
meanwhile. A host that was switched off or rebooted stops its machines,
and the api starts the ones that were running again once it is back
(I-645).

## 5. Snapshots stay on the host's own disk

Snapshots of machines on repose's servers go to Azure Blob. Snapshots of
machines on hardware the user owns are written to that machine's own disk
(`FileBlob`, hostd's `--snapshot-dir`) and repose keeps no copy, so a
dead disk loses every snapshot on it and the machines with it. That is
the one thing on this feature a user cannot undo and cannot discover any
other way, so the docs page says it in a section of its own and `repose
host rm` says it in its question.

The alternative was an S3-compatible backend with the user's own bucket,
which would put that bucket's keys on a machine in someone's house: a
fourth home for a secret where CLAUDE.md and `secrets.md` allow three.
`repose.host.owned = true` makes the host module assert that
`snapshots.blobUrl` and `snapshots.identityClientId` are empty, so a
future provider module cannot hand an owned host a bucket by accident
(I-1372).

## 6. Removing a host

`repose host rm NAME` asks one question, then drains and retires the
host and clears its certificate serials so it cannot come back on them.
The machine itself is untouched: nothing of the user's on it is deleted
and it keeps running until they turn it off.

A host that still holds live machines is not removed. A machine moves
host only by restore, so the api answers `host_has_projects` with their
slugs and the user destroys or restores them first. A second `rm` of a
host already retired is not an error on the wire (`204`), and the CLI
says `You have no host named X` because a retired host is not one the
user has.

## Behaviour that must hold

Ownership and placement (I-1366):

- An owner's project is placed on their own host, and a stranger's
  project is refused there. Both directions, red when the ownership term
  is removed: `TestOwnershipPlacesBothWays` (internal/api/scheduler).
- An owned host is outside the seat count, the waitlist and the pool
  gauges.
- `placement` is read at the create and at a re-placement and never
  after; `PATCH /projects/:id` does not take it: `TestPlacementField`
  (internal/api/http).
- An account whose plan sells no memory on repose's hosts never lands on
  the fleet, whatever the field says.

The routes (I-1366):

- `GET /hosts` lists this account's hosts and never another account's or
  one repose runs: `TestHostIsNotAnotherAccounts` (internal/api/http).
- `POST /hosts` mints a single-use token valid 24 hours, names the host
  when the caller does not, and refuses a name the account already has.
  Names are unique per account: `TestHostNames`, `TestHostAddListRemove`.
- `POST /hosts` past the plan's figure is `host_limit` with
  `detail.limit`; from a plan that sells none it is
  `own_hardware_plan_required`: `TestHostNeedsAPlanThatSellsOne`.
- `DELETE /hosts/{id}` on a host with live machines is `host_has_projects`
  naming them; on an already retired host it is `204`:
  `TestHostWithMachinesIsNotRemoved`.

The CLI (I-1370):

- `repose host add` puts the install step, or the token when no line is
  recorded, on stdout alone so it pipes, and what to do with it on
  stderr: `TestHostAddListRm` (internal/cli) covers both.
- `repose host ls` is one table, leaves out a host the user removed, and
  shows memory in whole gigabytes, because a 24 GB Mac reports about
  23.7 GiB and "23.7 GB" reads as the wrong machine.
- `repose host rm` asks one question naming the snapshot loss, and `-y`
  skips it; a name the account does not have exits 1.
- `repose host ls` works under an API token with `read`; `host add` and
  `host rm` take a login, because minting a join token hands out a
  machine and removing one retires it: `TestHostsUnderAnAPIToken`.
- The state words are the same in the CLI and the dashboard:
  `TestHostStateWords` and `offlinehost.test.ts`.

An offline host (I-1370):

- A quiet host of the account's is named with how long it has been quiet
  in `repose ls`, `repose status`, `repose run`'s wait and both dashboard
  surfaces; a host of repose's is not named:
  `TestOfflineOwnHostIsNamedEverywhereItShows` (internal/cli),
  `own-hardware.spec.ts` (apps/web).
- `GET /hosts` is read only when a machine says its host is quiet, so the
  common listing keeps its one round trip, and the page asks once rather
  than in a loop when the account owns none:
  `TestLsReadsHostsOnlyWhenOneIsQuiet`, and the read count in
  `own-hardware.spec.ts`.

The host itself (I-1372, I-1368):

- An owned host takes its join token from `/run/repose/join-token` with
  no cloud-init, consumes it once and refuses a second: the
  `host-services` VM check, whose host is `provider = "none"`.
- An owned host writes snapshots to its own disk and carries no bucket
  credentials; the module refuses to build otherwise: the
  `owned-host-keeps-snapshots-local` flake check.
- A host cannot reach another host or the control plane beyond its own
  ports, and the gateway routes to a host only its owner's projects
  (I-1368, `SECURITY.md` "A host you do not own").

## Depends on

Workstreams 19a (ownership, placement, the routes, the dashboard's Hosts
page), 19b (the hub and gateway rules), 19c (`repose host`, the install
path, the offline wording, the snapshot store), 19d (the plan), 19e (the
aarch64 outputs), 19f (the Mac appliance and its launcher), 19g
(unattended updates). Underneath them: 05 (`api`, `db-schema`,
scheduler), 07 (CLI), 08 (dashboard), 01 and 03 (the host module and
hostd).

## Deferred

The x86 `metal` path beyond what the hosted half already allows: the
one-disk disko layout is the Mac's, and an x86 box still needs two disks.
Snapshots to a bucket of the user's own (I-1372, revisit when a user
asks). More than one host per account, which is a number in `plans.go`
and nothing else. Unattended updates, which may ship after launch.
Moving a running machine between hosts, which restore already does and
nothing else will.
