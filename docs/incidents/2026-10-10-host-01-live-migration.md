# 2026-10-10: host-01 live-migrated, its network died, Azure rebooted it, and the guest pool stayed down

host-01 lost its network for about 7 minutes after an Azure live migration,
then Azure rebooted it gracefully. Repose then kept every guest down for
about 9 hours more: the guest pool did not come back after the boot,
nothing tried again, nothing restarted machines, and nobody was paged. No
outside user was affected: the stopped projects were the owner's own and
the M3 isolation test account's.

Plans: `host-restart-final-plan.md` and `host-resilience-plan.md` (the
conductor's workspace). Decisions: I-643, I-644, I-645, I-646, I-648,
I-649, I-650, I-651, I-654.

## Timeline (UTC)

| Time | What |
|---|---|
| 05:39:05 | systemd-resolved on host-01: "Clock change detected" (the migrated VM resuming). |
| 05:39:06 | Azure live-migrates host-01 to new hardware. Its MANA VF stops passing traffic; nothing on the host notices. |
| 05:39:32 | The last node_exporter scrape of host-01 (Prometheus scrapes over WireGuard through the edge). |
| 05:40 to 05:45 | Azure's `VmAvailabilityMetric` stays at 1.0. No `host_unreachable` in the api: the heartbeat path is the VNet, and nothing pages. ntfy, the paging endpoint, answers 503 on every path. |
| 05:45:32 | Azure's heal begins: a graceful shutdown. The api logs `stream_disconnect`. Every guest@ unit is stopped at once with SIGTERM; virtiofsd logs "Client disconnected" at 05:45:32, hostd stops by 05:45:33. Cloud Hypervisor exits on SIGTERM without telling the guest (I-650 measured this later on woker-1: the guest's last 5 to 6 s of writes are lost). |
| 05:46:12 | host-01 is up (boot bbbec1ed). The initrd's LVM starts a thin_check of `vg-guests/thin`; the switch to the real root kills it. Its device-mapper leftovers block activation, and nothing retries (I-643). |
| 05:46:25 | `docker-registry` is skipped on its mount condition; it stays down until 2026-10-11 17:24. |
| 05:46:29 | hostd's reconcile finds 4 guests recorded running with no unit (01a0c154, 01a11778 kanali, 01a12261, 01a12312) and marks them stopped; Hello reaches the api. hostd reports the inactive pool as an empty one (0 percent used), so the scheduler would place new machines there (I-644). |
| 14:56:37 | `repose start kanali` times out after 60 s on the dead pool and ends in `error`. |
| later | The pool is brought back by hand; kanali runs again. 01a0c154 and 01a12261 stay stopped (the owner's own project and the M3 isolation test account). 01a12312 was destroyed at 19:32. |
| 2026-10-11 17:24 | `docker-registry` started by hand; `lvchange --monitor y` turns dmeventd monitoring on, which had been off since the boot: `lvm2-monitor` ran while the pool was inactive, and the `lvm` on PATH was built without dmeventd (I-643). |

## Why it took 9 hours

1. The initrd activated LVM and its teardown killed thin_check halfway
   (fixed: no LVM in the initrd, one pool unit after boot, I-643).
2. Nothing retried the activation, and the caches were skipped instead of
   waiting for the pool (fixed: the pool unit retries and clears
   leftovers; the caches follow and are wanted by it, I-643).
3. hostd read the inactive pool as empty and a missing VG as a missing
   volume (fixed: `pool_unavailable`, `volume_unavailable`, I-644).
4. Nothing restarted the machines that were running (resume after a
   reboot, I-645, L6).
5. Nobody was paged: ntfy was down, and HostUnreachable cannot see a host
   whose network died while the api's side stays quiet (I-648, L1 and L3).
6. The shutdown killed every guest without a clean stop (fixed: the ACPI
   power button through a guest@ drop-in, and hostd records nothing as
   failed during a shutdown, I-650).

## Still to record at the planned window (L8)

- The pool unit's real duration on the 476 GiB pool, with `event=` lines.
- The initrd grep (0), `io_timeout` 240000, `seg_monitor` monitored after
  the reboot, and each guest's `guest_powered_off` line.
- Per-guest boot times, and the scrape page reaching the phone.
