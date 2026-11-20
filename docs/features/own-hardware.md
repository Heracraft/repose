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
is written and proven on x86 and in the api's tests (§7, I-1374, I-1376,
I-1380): the aarch64 outputs, the Mac appliance's modules and image
recipe, the launcher, and the bootstrap order that lets a machine with
no route to the api register over the hub. Not yet done at this line:
the aarch64 image built and booted on a Mac, the image published where
the installer fetches it, and the `host_install_command` recorded
(`repose-admin hosts install-command 'repose host add --join %s'`), so
`repose host add` on production still prints the argument and the docs
page rather than the line. Owned hosts do not
update themselves; an operator switches them by hand, and the timer that
would pull a published host system is not built and may ship after
launch.

**The two figures the public page quotes**, measured by 19f on the lab
Mac and not on anyone's desk: an appliance given 12 GiB, which is what a
16 GB Mac's share is sized at, reports 11.6 GiB and 8 vCPU in its Hello
and leaves 9.6 GiB placeable after the eighth-with-a-2-GiB-floor reserve
(I-724). On disk, from the **production** image: the install step runs in 29 s
with the image local, the sparse unpack leaves 4.58 GB of real blocks
(37 GiB apparent, then extended into the volume's free space less 20
GB), the peak is about 6.6 GB with the 2.08 GB download still beside
it, and **at rest after first boot it is 5.65 GB** (the pool's metadata
grown to 1 GiB, plus the caches volume's ext4).

The caveats the page does not carry. The appliance was 12 GiB on a 24
GB M4, not a 16 GB Mac on a desk. The 5.65 GB is from an image built
**before** 19f's boot-time trim, which on the lab image gave back 3 GB
of the build's stale blocks, so the shipping image's at-rest figure
will be a little lower and nobody has measured it; the page says "under
6 GB", which is true before the trim and after it.

What a machine costs, from one image minutes apart with nothing else
changed: 9.71 GB with a `large` machine's 40 GiB volume created and the
machine stopped, 9.76 GB with it running, and the thin volume 0.66
percent of 40 GiB allocated, which is 0.28 GB. So a machine costs about
0.3 GB when it is created, the volume's own filesystem, and from there
what it writes. The 0.05 GB between stopped and running is the guest's
first minutes of writes.

The public page does not carry the 0.3 GB. It says a machine costs what
it writes up to the size of its disk, which is the thin volume's
behaviour and answers the question a reader is actually asking, which is
whether three machines need three times 40 GB. 0.3 GB is close enough to
nothing that quoting it would be one more number to carry and no
decision changed.

**Why two earlier figures appeared to disagree**, recorded because the
next person will meet the same trap: 19f's lab reading of 9.71 GB was
taken after a boot-time trim and the production reading of 5.65 GB
before one, since that image predates the trim. Across that boundary the
closure difference is not additive and a subtraction gives a figure
below the at-rest floor. On the same side of a trim it reconciles:
observed 9.71 less 5.65 is 4.06 GB, predicted 7.09 for the closure less
the trim's 3.0 is 4.09 GB, within 0.03 GB. Two real-block readings are
comparable only when both are on the same side of a trim.

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
repose host add --join eyJ2IjoxLCJ0b2tlbiI6InJqdF9WYjNuUTgu...
mac-mini: run this on the machine you are adding. It works once, before Thu 20 Nov 14:05.
```

```
$ repose host ls
HOST      STATE   MEMORY  MACHINES  RUNNING  LAST SEEN
mac-mini  online  24 GB   3         1        just now
```

```
$ repose ls
PROJECT     SIZE   STATE    UP     AGENTS           HOST
todo-app *  large  running  2h14m  claude: working  mac-mini
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
`host_install_command` setting with `%s` replaced by `install.argument`,
so the install path can change without a CLI release and the user-facing
step and `repose-admin hosts install-command` cannot drift. While no
setting is recorded the api sends `command: ""`, and a client then
prints the argument and `install.url`, which is the public docs page. A
client never composes a line of its own: a line a user pastes has to be
true.

The argument is the join bundle (I-1380): the token and the machine's
place on the hub, a WireGuard key pair's private half with the machine's
address and the edge's peer, minted by `POST /hosts` so that the edge
peers the machine before it boots. A machine of the user's has no route
to the api's VNet address, so without this it could not register at all
(I-92, I-1382). Register replaces the key, so the bundle is dead once the
machine has joined, and an unused one dies with its token; the user who
loses one runs `host rm` and `host add` again. `SECURITY.md` "A host you
do not own" says what the bundle is worth to someone who reads it.

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
cannot take the machine, is refused at the request with `capacity` and a
message that names the host and its state, rather than quietly using the
fleet and billing for it, and nothing is written (I-1390). Until I-1390
the row went in first and the op refused it afterwards, so a customer
whose Mac had not finished installing stranded one project in `error`
per attempt and could make one nowhere: the CLI had no flag for the
other answer. `repose run --host NAME|repose` is that flag; the api's
refusal carries `detail.hosted_allowed` so the CLI names `--host repose`
only when the plan would honour it.

## 4. How long a machine takes to start

A machine on an owned Mac takes about **four minutes** to start, the first
time and every time after. The hosted fleet starts the same machine in
about 12 seconds (`/docs`, "Come back and stop"), so this is the one
number on this feature where a user's experience is twenty times worse
than the thing they already know, and the page says it in those words
rather than "a few minutes".

Measured by 19f on the real Mac, the same guest stopped and started:
cold 3 min 36 s to multi-user, warm 3 min 59 s; Ready at 70 s cold and
90 s warm; `hostd stop` 18 s. Load average inside the guest was 7 on 2
vCPUs during boot, so everything in the boot path is two to three times
slower than on x86.

It is every start and not the first, which is the part that surprises.
The guest skips its own registration the second time (`store paths
already registered`, I-225), but only once hostd's `RegisterPaths`
arrives, and that waits on hostd computing `nix-store -qR` and
`--dump-db` of the closure on the appliance, per start. So it is neither
once per guest nor once per closure: a second machine on the same
closure pays it again.

What is **not** slow: running. Gate 0 measured no nesting tax worth the
name (a ratio of 1.07, inside noise), so once a machine is up the
agents, builds and tests go at the Mac's own speed. A reader who takes
four minutes to mean "everything is four times slower" has been misled
by a true sentence, which is why the public page says both.

Known and not built: hostd caching the closure's `dump-db` per closure
rather than recomputing it per start, which 19f expects would halve the
figure. It is recorded as the next thing and is not on the public page,
because the page says what happens today.

## 5. When the host is offline

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

## 6. Snapshots stay on the host's own disk

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

## 7. Removing a host

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

## 7. The Mac appliance

One aarch64 NixOS VM on Apple's Virtualization framework runs hostd and
the user's machines as Cloud Hypervisor microVMs under its KVM
(`nix/hosts/mac.nix`, `nixosConfigurations.mac`, I-1376). The launcher
is the CLI itself: `repose host add` on a Mac fetches vfkit and the
appliance image and starts the VM from a launchd agent that runs
`repose host run` at every login, holding the Mac awake with
`caffeinate` while it runs; `repose host rm` on that Mac takes the
appliance away with the host (I-1380; `internal/cli/appliance.go`). One disk, the image grown to the size the user
chose, with the machines' volumes and snapshots in it; three quarters of
the Mac's memory to the VM and the fleet's host reserve inside it. The
kernel is a product constraint: 6.18.57 is the one version known both to
boot nested guests on Apple silicon and to pass the CVE-2026-99999
assertion (research doc §7). A machine's boot on a Mac is minutes, not
seconds (3 min 36 s cold and 3 min 59 s warm to multi-user on an M4 at 2
vCPU, research doc §9; hostd's per-start dump-db of the closure is half
of it), so hostd's ready window is five minutes there
(`guestReadyTimeout`) where the Azure hosts keep 60 s. The park is on
(I-1386, I-1394): a restart, a shutdown or a logout the user asks for
parks every running machine to an encrypted image on the appliance's
disk (6.5 s a guest on the M4, snapshot 203 ms, an 824 MB image) and the
next login unparks it (3.8 s), against about 289 s to start a guest cold
and lose its work. The image's key is wrapped not by Key Vault, which a
Mac has no identity for, but by the launcher, asked over a vsock vfkit
forwards to `park-key.sock` (`internal/parkkey`); the launcher keeps the
one wrapping key as a generic-password item in the user's login keychain
(`internal/cli/keychain.go`, through `security`, since the darwin build
has no cgo) and the VM never sees it. Not hardware-backed, and the docs
say so. A power cut, a panic or a forced power-off parks nothing: the api
starts the machines that were running (I-645), cold. The image's six
hours (I-653) count against the user here, because hostd exists only once
someone logs in: a restart with no login for six hours discards the image
unread, and the docs say so beside the power-cut case. A stuck snapshot
holds the shutdown at most 240 s (the deadline plus the park's own stop
grace), the unit's wall is 270, the launcher's plug 330 and launchd's kill
360; what macOS itself allows an agent at a restart is only the Mac's to
show. `repose host add`,
`repose host run` and `repose host rm` are the user's side on the Mac.

The two downloads a first install makes, vfkit and the 2.08 GB image,
run on a client of their own with no whole-request deadline (I-1414).
The CLI's api client carries a 30 s `Timeout` and `http.Client.Timeout`
covers reading the body, so while the downloads took that client whole a
2 GB fetch had 30 seconds to finish: the first `repose host add` on the
lab M4 failed at 31 s with `context deadline exceeded ... while reading
body` and the second succeeded in 37 s, on a connection that moves the
asset at 226 MB/s (ENG-117). What ends a dead connection instead is a
stall window: no bytes for 60 s, the response headers included, and the
attempt is given up. A failed attempt leaves what it got in a `.part`
file beside its destination and the next attempt continues from it with
a `Range` request, three attempts in all; a server that ignores the
range starts the file over, and a mismatched checksum after a resume
costs one fresh attempt, because the part may be a prefix of an older
image. A download that does not finish is the one failure in the install
that names the way out: `repose host add` minted the host before the
install, so a plain second `repose host add` would be refused by the
one-host limit, and the sentence carries `repose host add --join
<argument>` instead.

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
- A create no host can take is refused before the row is written, twice
  in a row with nothing stranded, and `placement: "hosted"` is the way
  out: `TestCreateRefusedBeforeTheRowIsWritten` (internal/api/http);
  `repose run --host` sends it and names it in the refusal:
  `TestCreateRefusedWhileOwnHostWaitsNamesTheWayOut`,
  `TestRunHostChoosesWhereTheMachineIsMade` (internal/cli). I-1390.

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

The readout (I-1392):

- A machine on a host the account owns names it where the account looks:
  a HOST column in `repose ls` and a `host` row in `repose status`, with
  `repose` for a machine on repose's servers and `-` for one not placed
  yet. An account that owns no hardware gets neither, and a host of
  repose's keeps I-616's ruling and stays under `-v` with the machine's
  address: `TestHostReadoutNamesTheHostYouOwn` (internal/cli).
- The name is the project's own `host_name`, so the readout costs no
  `/hosts` read and the column follows the machines rather than the
  hardware: `TestHostReadoutComesFromTheProjectRecord` (internal/cli),
  `TestCreateRefusedBeforeTheRowIsWritten` (internal/api/http) for the
  field itself.

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

The host itself (I-1372, I-1368, I-1376, I-1380):

- An owned host takes its join token from `/run/repose/join-token` with
  no cloud-init, consumes it once and refuses a second: the
  `host-services` VM check, whose host is `provider = "none"`.
- `POST /hosts` peers the machine before it boots and Register keeps the
  addresses and replaces the key: `TestHostAddPeersTheHubBeforeRegistration`
  (internal/api/http), `TestHubBootstrapPeersTheHostBeforeItRegisters`
  (internal/api/hostmgr); an unregistered host whose token expired or
  that was removed is not a peer (the first test).
- The appliance splits the bundle, brings wg0 up on the bootstrap key,
  registers over it at the hub address, swaps to the registration's key
  and ignores a second bundle; the one-disk layout grows to the disk:
  the `host-mac` VM check. The launcher's sizing, its sparse unpack, the
  bundle, the config, the agent and vfkit's arguments:
  `TestAppliance*` (internal/cli); the Mac refusals:
  `TestApplianceRefusesTheWrongMac`.
- An owned host writes snapshots to its own disk and carries no bucket
  credentials; the module refuses to build otherwise: the
  `owned-host-keeps-snapshots-local` flake check.
- A host cannot reach another host or the control plane beyond its own
  ports, and the gateway routes to a host only its owner's projects
  (I-1368, `SECURITY.md` "A host you do not own").

The park on the Mac (I-1386, I-1394):

- hostd asks the launcher to wrap and unwrap image keys, what crosses is
  never the key, and the record's key id names the launcher's key:
  `TestWrapUnwrapThroughTheLauncher` (internal/parkkey). A record from a
  replaced keychain item is refused by name, and a tampered one does not
  open: `TestUnwrapRefusesAnotherKey`. A locked keychain is a plain
  error, so the park skips its guests with `no_key` rather than writing
  images nobody can read: `TestALockedKeychainIsAPlainError`; no launcher
  at the socket fails within its timeout: `TestNoLauncher`.
- The launcher makes one keychain item the first time, reads it back
  rather than trusting the add, never logs the value, tries no add
  against a locked keychain and refuses a value that is not a key:
  `TestKeychainKeeper*` (internal/cli). The agent's `ExitTimeOut` and the
  vsock device are in the plist and vfkit's arguments:
  `TestApplianceConfigAndPlist`; the port is the same on both sides of
  the VM boundary: `TestMacNixAgreesOnThePort`.
- On the Mac itself: a park at a stop, a real restart, and an unpark
  with the `guest_restored` phase timings. Run by whoever holds the Mac;
  not run at this line.

## Depends on

Workstreams 19a (ownership, placement, the routes, the dashboard's Hosts
page), 19b (the hub and gateway rules), 19c (`repose host`, the install
path, the offline wording, the snapshot store), 19d (the plan), 19e (the
aarch64 outputs), 19f (the Mac appliance, its launcher and the bootstrap
order), 19g (unattended updates). Underneath them: 05 (`api`, `db-schema`,
scheduler), 07 (CLI), 08 (dashboard), 01 and 03 (the host module and
hostd).

## Deferred

The x86 `metal` path beyond what the hosted half already allows: the
one-disk disko layout is the Mac's, and an x86 box still needs two disks
and a way to place the join bundle (`/run/repose/launcher/join` by hand
would do; nothing ships it).
Snapshots to a bucket of the user's own (I-1372, revisit when a user
asks). More than one host per account, which is a number in `plans.go`
and nothing else. Unattended updates, which may ship after launch.
Moving a running machine between hosts, which restore already does and
nothing else will.
