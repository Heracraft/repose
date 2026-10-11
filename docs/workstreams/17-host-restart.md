# 17 · host restart (guests come back after a host reboot)

## 1. Goal

A host reboot, planned or Azure's, costs running machines their memory
and nothing else: the guest pool comes back by itself, a pool that cannot
is reported and paged, a graceful shutdown lets every guest write its
disk, machines that were running start again, and the owner is paged.
The incident that set it: `../incidents/2026-10-10-host-01-live-migration.md`.
The plan with its owner decisions D1 to D9 is the conductor's
`host-restart-final-plan.md`; where it and the docs differ, the docs win.

## 2. Items

| Item | What | Decisions | Branch |
|---|---|---|---|
| L1, L3 | Paging and alert coverage | I-648 | `host-alerts` |
| L2 | Operator access when host-01 is down; integrated main off host-01 | I-651 | conductor |
| L4 | Guest disks come back after a reboot: the pool unit, `volume_unavailable`, host hardening, console password code | I-643, I-644, I-649, I-654 | `host-pool` |
| L5 | A host shutdown leaves guests resumable: the clean stop, the shutdown flag | I-650 | `host-pool` |
| L6 | Running machines start again after a reboot | I-645 | its builder, after L4 |
| L7 | Agent windows saved every minute | I-646 | its builder |
| L8 | Release, then one planned host-01 window | | conductor and owner |

Shared numbers: `Hello.pool_unavailable = 6`, `Heartbeat.pool_unavailable
= 7` (L4); `Hello.host_boot_id = 7`, `host_boot_at = 8`,
`GuestStatus.interrupted_boot_id = 6`, `interrupted_at = 7` (L6).
Migrations `0021_host_pool_unavailable` (L4) and `0022_resume` (L6).

## 3. L4 and L5 (`host-pool`)

- `nix/hosts/storage.nix`: no LVM in the initrd, no autoactivation,
  `repose-guest-pool.service`, the pool monitor's new metrics, the
  dmeventd-capable lvm everywhere. `caches.nix`, `hostd.nix`: ordering.
- `nix/hosts/vm-hardening.nix` (hosts and the edge): `mana_ib`, NVMe
  `io_timeout`, ethtool, 10 s boot menu, `root-console.hash`.
- `nix/hosts/hostd.nix`: the `guest@.service.d` drop-in with the clean
  stop.
- hostd: `lvm.PoolStatus`, `ErrPoolUnavailable`, the lvm-layer guard,
  `VolumeExists`, start, reconcile, Hello and Heartbeat, metric
  `repose_host_pool_available`, the shutting-down flag.
- api: migration 0021, `hosts.pool_unavailable`, the scheduler filter, a
  failed start left `stopped`, the user sentence. CLI: exit 1.
- Alerts `PoolUnavailable` (page), `PoolUnmonitored` (warn), the
  inhibition in `repose-route.yaml`.
- VM test `host-reboot`; host-services' graceful reboot subtest.

What only a host can prove waits for L8: the pool unit's duration on a
476 GiB pool, the first boot of the new initrd, the NVMe value under a
real migration, and a real Cloud Hypervisor guest powered off by the
drop-in at a real shutdown (a VM test cannot run a third level of
virtualisation; RESEARCH §16 has the dev box's numbers).

## 4. Checklist

- [x] L4 Go tests fail without the change and pass with it. Evidence: the
      mutation run in the `host-pool` commit message.
- [x] `host-reboot` subtests 1 to 8 pass, subtest 4 fails on the config
      before I-643 (scratch test `host-reboot-oldconfig`), subtest 6 never
      repairs, subtest 8's initrd grep is 0. Evidence: the runs pasted in
      the commit message.
- [x] `host-storage`, `host-caches`, `host-services` pass. Evidence: the
      same.
- [x] L5: `TestUnexpectedExitDuringShutdownKeepsRunning`,
      `TestBootDuringShutdownNeverFallsBack`, host-services' graceful
      reboot subtest. Evidence: the same.
- [ ] Live after the switch and the planned reboot: RUNBOOK "Switch a host
      to main" step 7 and "Host rebooted" step 3, recorded in the incident
      file (L8).
