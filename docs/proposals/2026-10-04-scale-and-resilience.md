# Scaling and resilience after launch (proposal, 2026-10-04)

**Status: research only. Nothing here is decided and no code is written.**
The owner asked for directions on scaling and resilience after launch,
with resilience first. This file records the state of production today,
what comparable services do, and an order of work. Each step becomes real
only through a `DECISIONS.md` entry. The edge work on branch
`edge-zero-downtime` (I-469..I-473) is taken as done and not repeated
here.

Peer facts come from public engineering posts read on 2026-10-04 and are
listed under "Sources". Where a number could not be checked it says so.
Re-check every price before it is used.

## Summary

| Question | Answer |
|---|---|
| Biggest risk today | host-01 is the only host. Losing it loses every guest, and the restore runbook needs a second host that does not exist |
| Worst-case data loss | About 36 hours of a tenant's work: the nightly snapshot does not catch up after a missed night, and `SnapshotStale` waits 36 h |
| Cheapest big win | Reattach host-01's data disk to a new VM. The guest volumes live on a managed Premium SSD v2 disk, which survives the VM |
| Second cheapest | Postgres off the control VM's OS disk, with backups something checks |
| What unlocks moving guests | Incremental snapshots from `thin_delta`, which the snapshot-speed proposal already wants |
| What unlocks scale | A second host, a central builder (R4-4), and placement that knows about disk bandwidth |
| Long bet | Disks backed by object storage with the host's disk as a cache (Fly Sprites, Replit, LSVD) |

---

## 1. Where production stands

One region (Azure eastus), one availability zone (zone 1).

| Component | Size | Runs |
|---|---|---|
| host-01 | `Standard_D16s_v7`, 64 GB, 512 GB Premium SSD v2 data disk at 600 MB/s | Every guest (7 large or 14 small, 6 seats), kanali |
| edge-01 | `Standard_D2s_v7` | Gateway on 22, WireGuard hub, wgsync, 443 stub |
| Control VM | `Standard_D4s_v7`, Coolify | api, api-grpc, web, Postgres on the OS disk (I-87) |
| Owner's server | | Logto, Coolify manager, Loki, Prometheus, Grafana, Alertmanager |
| Blob | `reposesnapshots3912`, Standard LRS, no soft delete | Snapshots |

The launch plan resizes host-01 in place to a `Standard_D64s_v7` with a
2 TB disk (I-39, LAUNCH.md §3): 30 seats on one machine. That raises the
blast radius of a host failure from 6 seats to 30.

## 2. Single points of failure

| Fails | Effect | Recovery today | Data lost |
|---|---|---|---|
| host-01 VM | Every guest stops; seats drop to 0 | RUNBOOK "Host loss" restores each project onto another host, and there is none. Never drilled (M5 open item) | Up to the last snapshot |
| host-01 data disk or thin pool | Writes fail in every guest | Grow the disk; nothing for corruption | Up to the last snapshot |
| edge-01 | No new SSH, no scrapes through the hub; guests keep running | Reboot. Planned switches are now harmless (I-471); unplanned ones are not (I-473) | None |
| Control VM | No new SSH routes after 5 s, no ops, no logins, no billing | Coolify redeploy | Postgres since its last dump. The dump goes to R2 on a Coolify schedule nothing in this repo checks (I-112) |
| Owner's server | No logins (certificates stop after 24 h), no deploys, no alerts | Owner's routine | Logs and metrics |
| Blob account | No snapshots, restores or moves | Azure | LRS keeps three copies in one datacenter. Without soft delete, a deletion cannot be undone |

Three gaps stand out. The host-loss path has never run. Snapshots can be
36 hours old before anyone hears about it. And the alerting runs on the
same server as Logto, so if that server dies, nobody gets paged about it.

## 3. What comparable services do

**Pinned local disks are the norm, with snapshots as the safety net.**
Fly volumes live on one host's NVMe and are not replicated; daily
snapshots are kept 5 days by default, and Fly's docs say snapshots are not
a primary backup. Render publishes the same terms: one instance per disk,
snapshots every 24 hours. repose's R2-3 matches them. What repose lacks
is the second host to restore onto.

**Moving a stateful VM is offline, then lazy.** Fly's 2024 machine
migrations stop the source, then boot the target on a `dm-clone` device
whose origin is the old volume served over iSCSI. The guest runs at once
while kcopyd copies the rest in the background. They dropped NBD because
its kernel threads hung on network blips, and they hit differing LUKS2
header sizes and guest addresses that encoded the host. Railway moves
volumes offline: back up, copy, check, remount.

**The newest designs make the host disk a cache.** Fly Sprites (January
2026) keep data as chunks in object storage and metadata in SQLite
replicated with Litestream; local NVMe only caches. A checkpoint takes
about 300 ms and moving hosts is a metadata handoff. Replit's Margarine
serves btrfs disks from GCS in 16 MiB blocks over NBD and snapshots at
each btrfs commit. e2b stores only changed blocks per pause and restores
memory lazily through userfaultfd (pause about 4 s per GiB, resume about
1 s). LSVD (EuroSys '22) is the paper version of this.

**Idle machines get suspended.** Codespaces stops after 30 minutes idle
by default. e2b and CodeSandbox suspend and resume in seconds or less.
Daytona adds an "archived" state that moves the filesystem to object
storage. Gitpod/Ona overcommits memory with swap and reports it "works
well in practice". repose chose differently: I-262 announces an idle
machine and never stops it.

**Replicated block storage is heavy at two hosts.** DRBD/LINSTOR is the
smallest step that survives a host loss with no data lost, at the cost
of a network round trip on every write. Ceph needs three nodes at
minimum, keeps a third of raw space usable, and small teams report months
of tuning. Longhorn assumes Kubernetes.

**Control planes fail over in a minute or two.** Azure Database for
PostgreSQL Flexible Server with zone-redundant HA fails over in 60 to
120 s with no committed data lost (under 10 s on Premium SSD v2, in
preview). Patroni's defaults leave 30 to 45 s without a leader.

**Config pushes take down fleets.** In September 2024 one Corrosion
update deadlocked every Fly proxy at once. AWS and Slack split into cells
so one failure reaches a fraction of customers; Slack drains a zone in
under 5 minutes, 1 percent at a time.

## 4. Directions, in order

Each step names what it closes, what it costs, and the trigger that says
it is time. Steps in the same tier are independent.

### Tier 0: before host-01 becomes a D64 with 30 seats

**0.1 A second host, and the host-loss drill.** Without it, the RUNBOOK's
recovery path is untested and impossible. Fix the 10.255.0.3 collision
first (the monitoring server holds host-02's WireGuard address, I-359).
Then run the two open M5 items: restore a project onto a different host,
and lose a host on purpose. A second D16s_v7 costs about $770 a month.
Running two D32s instead of one D64 costs about the same and halves what
one host failure takes down. Check that price before deciding.

**0.2 A data disk survives its VM.** host-01's guest volumes sit on a
managed Premium SSD v2 disk (`infra/azure/modules/host/main.tf`), and
Azure keeps a managed disk when the VM under it dies. A runbook entry
that creates a fresh host VM in zone 1, attaches the old disk, and lets
hostd reconcile from LVM (`hostd reconcile --rebuild`, since state.db is
on the OS disk under `/var/lib/repose/hostd`) recovers every guest with
nothing lost, in the time a VM takes to boot. Fly's docs warn about the
opposite case: local NVMe on Azure's L-series is erased when Azure moves
a VM after a hardware fault. Drill it once on a scratch host. Note that
this path ends if hosts move to Hetzner dedicated servers, where the
disk is the server's own.

**0.3 Snapshots that cannot silently go stale.** Three small changes:
`Persistent = true` on the nightly timer (`nix/hosts/hostd.nix:141`) so
a missed night catches up at boot; `SnapshotStale` at 26 hours instead
of 36; and a per-project "last good snapshot" age on the dashboard's
admin view. That takes the worst case from about 36 hours down to 24.

**0.4 Postgres with a tested restore.** Today it is a compose service on
the control VM's OS disk, and its only backup is a Coolify dump to R2
that nothing checks. Two options:

| Option | RPO / RTO | Cost | Work |
|---|---|---|---|
| WAL archiving to Blob (wal-g or pgBackRest) on the existing container, plus a nightly restore test in CI | Seconds / tens of minutes | Blob storage only | A sidecar, a check, a RUNBOOK entry |
| Azure Database for PostgreSQL Flexible Server, zone-redundant HA | 0 / 60 to 120 s | Check price; roughly two D2-class servers | Move `DATABASE_URL`, Key Vault access, migrations |

The first fits the current Coolify setup and keeps the move to another
cloud (R3-20) cheap. Either way, an alert fires when the newest backup is
older than a day.

**0.5 Alerting that does not share a fate with what it watches.** A
dead-man's switch: Alertmanager sends a heartbeat every few minutes to
an outside service, which pages the owner by another route when the
heartbeats stop. Add a blackbox probe of port 22 and `/healthz` from
outside Azure. The edge has no alert of its own today; its failure shows
only as every target going down.

**0.6 Snapshot storage that survives a mistake.** LRS keeps three copies
in one datacenter. ZRS spreads them across zones for a small price
difference. Soft delete was left off on purpose, because a destroy must
delete. Microsoft's blob soft delete keeps deleted data for a retention
period you set, from 1 to 365 days, so a short one (7 days) would let
an operator mistake or a stolen key be undone. The privacy policy would
then need to say deletion completes within that window. The owner decides
this one.

### Tier 1: once there are two hosts

**1.1 Incremental snapshots from `thin_delta`.** A full snapshot reads
every used block at the disk's 600 MB/s. On a D64 with 55 guests
averaging 10 GB used, the nightly run keeps the shared disk saturated
for about 15 minutes, while tenants are still using it. `thin_delta
--metadata-snap` lists the blocks changed between two thin snapshots, so
hostd can upload only those. The snapshot-speed proposal (§3) already
wants this, with Put Block From URL composing full blobs on the server.
Once a snapshot costs seconds, take one every hour. That moves the
worst-case loss on a host failure from 24 hours to 1, and makes 1.2
possible.

**1.2 Moving a guest with seconds of downtime.** Today `projects move` is
stop, full snapshot, restore, start, and the user waits for the whole
disk twice. With incremental snapshots: copy a base while the guest
runs, copy the delta again, stop, copy the last delta, start on the
target. The downtime becomes the last delta plus a boot. This is what
lets the owner drain a host for a kernel update without a maintenance
window. R2-3's trigger ("host maintenance moves become frequent") is met
the day there are two hosts and a nixpkgs kernel bump.

**1.3 Two edges.** Written down in I-473. With two hosts and 30-plus
seats, an unplanned edge reboot takes everyone's terminal at once. Add
the Corrosion lesson: switch one edge, watch it, then the other, so one
bad build never reaches both.

**1.4 A central builder and cache (R4-4).** Every host builds its own
closures today. A new host starts cold, and a base bump rebuilds every
guest on every host at 04:00 UTC. One builder that pushes to a cache the
hosts read from makes a new host useful within minutes and lets a moved
guest start without a rebuild. R4-4 sets "more than one host" as the
trigger, which 0.1 meets.

**1.5 A second api replica that drives ops.** I-42 names the missing
piece: a forwarding hop so a replica can reach hosts connected to the
other. Until then a second replica only serves HTTP. This matters less
than the items above, because a dead api leaves guests running.

### Tier 2: scale past a handful of hosts

**2.1 Placement that knows about disk and zones.** `scheduler.PickHost`
picks the host with the most free memory. It does not know about the
600 MB/s disk every guest shares, the 1.5x volume budget (it learns from
hostd's refusal, I-449), or zones. Add pool use and recent disk
throughput to the score, and spread one account's projects across hosts
so one failure does not take all of a user's work.

**2.2 Density without breaking what plans promise.** Plans sell "memory
that may run at once" (I-289), and hosts reserve it in full (R3-2). An
idle dev VM touches a fraction of that. cloud-hypervisor supports free
page reporting through the balloon (`free_page_reporting=on`); the guest
hands back free pages about every 2 seconds. That frees host memory
without changing what the user can use. Selling the freed memory means
overcommit, which needs swap or zram on the host and a policy for the
night every guest wakes at once; that is a pricing decision as much as a
technical one. Leave KSM off across tenants: it opens a timing side
channel between them.

**2.3 Stopped projects leave the host.** A stopped project keeps its
thin volume on the host it last ran on. Daytona's "archived" state moves
the filesystem to object storage after a while. For repose, a project
stopped for N days could drop its local volume once a verified snapshot
exists, and restore on next start onto whichever host has room. This
frees pool space on full hosts, and the user does not see it except as a
slower first start. The restore-speed work (I-403..I-405) sets how slow.

**2.4 Automatic capacity.** R3-1 and R3-5 keep capacity manual: the owner
adds a host when the 80 percent memory alert fires. Past five hosts, a
tofu module run from the api (or from the conductor) when seats fall
below a threshold saves the owner a page. The waitlist (I-269) covers
the gap in the meantime.

**2.5 Cells.** Once there are around ten hosts, group them: each cell is
a set of hosts plus its own edge pair, keyed by project. A bad host or
edge build goes to one cell first. The same split is the natural shape
for a second region (EU, or Hetzner, R3-20): a cell in Falkenstein with
its own edge, with the control plane staying where it is.

### Tier 3: the long bet

**3.1 Disks backed by object storage.** Fly Sprites, Replit and e2b
converge on the same design: the volume's truth is chunks in object
storage, and the host's disk is a cache. A host then holds nothing that
is not elsewhere. Losing one loses at most the last few seconds of
writes, a move is a metadata handoff, and a stopped project costs blob
storage only. The cost is a block layer repose would own (an NBD or
ublk server, or dm-clone with an object-storage origin), and write
latency that depends on the network. It would replace snapshots, moves
and archived projects with one mechanism. Not before Tier 1 is done and
measured.

**3.2 Live migration.** cloud-hypervisor migrates memory between hosts
over TCP with mTLS (pre-copy with a 300 ms downtime target, or
post-copy), but does not move disks. Paired with 1.2 or 3.1 for the
disk, it gives a drain with no visible downtime. virtiofsd state and the
secrets tmpfs make this harder (the same reasons I-233 lists for memory
snapshots). Worth it only if 1.2's few seconds turn out to matter to
users.

## 5. Recommended order

1. 0.2 and 0.3 this week: a runbook entry, a drill, a timer flag, an
   alert threshold. No new spend.
2. 0.1 before host-01 grows to 30 seats. Consider two D32s in place of
   one D64.
3. 0.4 and 0.5 together, before the first paying user's data matters.
4. 0.6 as an owner decision on the privacy wording.
5. 1.1 then 1.2, which turn host maintenance into an ordinary day.
6. 1.3 and 1.4 as the second host fills.
7. Tier 2 by metric: 2.1 when a host's disk saturates, 2.2 when memory
   is the binding limit and sampled use sits well below reserved, 2.3
   when stopped volumes take more than a third of a pool, 2.5 at around
   ten hosts or a second region.

## 6. Open questions for the owner

- Two D32s or one D64 for launch? Same memory, half the blast radius.
- Is a short soft-delete window on snapshots acceptable against the
  privacy promise that a destroy deletes?
- Managed Postgres or WAL archiving on the current container?
- Does Hetzner (R3-20) stay on the table? It removes the disk-reattach
  path (0.2) and puts more weight on 1.1.
- Is suspending idle machines ever acceptable, or does I-262 hold for
  good? It decides how far 2.2 can go.

## Not verified

- Azure prices for D32s_v7, ZRS, and Flexible Server HA in eastus.
- That hostd reconciles cleanly from LVM on a VM it has never run on
  (the drill in 0.2 answers this).
- CodeSandbox numbers (their blog returned 403); KSM savings figures.
- Azure Load Balancer's maximum idle timeout (I-473 says 100 minutes;
  one source says 30).
- No peer publishes VMs-per-host figures.

## Sources

- Fly machine migrations: https://fly.io/blog/machine-migrations/
- Fly volumes: https://fly.io/docs/volumes/overview
- Fly Sprites design: https://fly.io/blog/design-and-implementation/
- Fly Corrosion: https://fly.io/blog/corrosion/
- Render disks: https://render.com/docs/disks
- Railway Metal migration: https://docs.railway.com/reference/migrate-to-railway-metal
- Replit storage: https://replit.com/blog/replit-storage-the-next-generation
- e2b architecture: https://raw.githubusercontent.com/e2b-dev/infra/main/docs/ARCHITECTURE.md
- Daytona sandboxes: https://www.daytona.io/docs/en/sandboxes
- Ona leaving Kubernetes: https://www.ona.com/stories/we-are-leaving-kubernetes
- Codespaces timeout: https://docs.github.com/en/codespaces/setting-your-user-preferences/setting-your-timeout-period-for-github-codespaces
- LSVD: https://doi.org/10.1145/3492321.3524271
- Ubicloud block storage: https://www.ubicloud.com/blog/building-block-storage-for-cloud-with-spdk-non-replicated
- thin_delta: https://dyn.manpages.debian.org/trixie/thin-provisioning-tools/thin_delta.8
- cloud-hypervisor live migration: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/live_migration.md
- cloud-hypervisor balloon: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/balloon.md
- KSM: https://docs.kernel.org/admin-guide/mm/ksm.html
- LINSTOR hardware: https://kb.linbit.com/linstor-and-drbd-hardware-considerations
- Azure disk types: https://learn.microsoft.com/azure/virtual-machines/disks-types
- Azure Lsv3: https://learn.microsoft.com/en-us/azure/virtual-machines/lsv3-series
- Azure Postgres Flexible HA: https://learn.microsoft.com/azure/reliability/reliability-postgresql-flexible-server
- Azure LB probes: https://learn.microsoft.com/azure/load-balancer/load-balancer-custom-probe-overview
- Patroni: https://patroni.readthedocs.io/en/latest/dynamic_configuration.html
- AWS cells: https://docs.aws.amazon.com/wellarchitected/latest/reducing-scope-of-impact-with-cell-based-architecture/what-is-a-cell-based-architecture.html
- Slack cells: https://slack.engineering/slacks-migration-to-a-cellular-architecture/
- Tailscale DERP: https://tailscale.com/kb/1232/derp-servers
