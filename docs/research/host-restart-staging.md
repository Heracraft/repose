# Host restarts on staging: the stress run (2026-10-12)

The host-restart launch set (workstream 17: the pool unit, I-643;
`volume_unavailable`, I-644; resume marks, I-645; the minute record of
agent windows, I-646; the clean stop, I-650) and the guest freeze
(I-652, I-653), integrated as branch `host-restart-batch`, run on a real
Azure host and pushed until something broke. Six bugs were found; each
has a failing test and a fix on the branch, and the scenario that found
it was run again on the fixed build.

Raw output: [host-restart-staging-data/](host-restart-staging-data/).

## The setup

- **Host.** host-stg-01, Standard_D16s_v7 (Intel Xeon 6973P-C, 16 vCPU,
  62 GiB), the same image and geometry as host-01: a 512 GB Premium SSD v2
  data disk with a 476.15 GiB thin pool and 5.12 GiB of metadata. The
  disk ran at staging's free tier (3000 IOPS, 125 MB/s) for the first run
  of scenario a, then at host-01's 16000 IOPS and 600 MB/s for everything
  else (dd, one stream, direct: 340 to 390 MB/s written, 1 GB/s read).
- **Build.** `host-restart-batch` from b34fc4a9, switched with
  `systemd-run --unit=repose-switch` (RUNBOOK "Switch a host to main"),
  with `repose.host.freeze.enable` and staging's own Key Vault
  (`repose-frz-stg-3912`, key `freeze-wrap`, made by `make apply
  ENV=staging` with `freeze_keyvault_name`). After each fix the host was
  built and switched again the same way.
- **api.** `hostdev` on the staging edge, rebuilt from the branch so it
  reads the new Hello fields. It has no resume tick: where production's
  api would start an interrupted machine (I-645), the guests were started
  by hand with `hostdev start`.
- **Guests.** Four, one closure (the branch's guest base): `small`,
  `small`, `large`, `xl`. Load in each: stress-ng (`--vm 1 --vm-bytes
  40% --vm-keep --cpu 1 --cpu-load 60`), a shell appending
  `date +%s.%N` to `~/st/ts.log` every second, a busybox container
  appending `date +%s` every second, a tmux session `st` with a counter
  loop, and agent windows in the project's tmux session (`claude`,
  `codex`, `pi`: stand-in scripts whose process names match, so
  `repose-tmux-save` records them). Frozen, the four images held 16 to
  17 GB.
- **What "kept" means below.** Same guest boot id, same pids for
  stress-ng, the appender, the tmux pane and the agent windows, the same
  container pid and start time, no line in either log going backwards,
  and the guest's clock within 0.4 s of the edge's (the exec round trip).
  A gap is the longest pause in `ts.log`, in the guest's own clock.

## Results

| | Scenario | Result | Bug found |
|---|---|---|---|
| a | `systemctl reboot` | pass after fix | 1 |
| b | `az vm restart` | pass | |
| c | hard power cycle (`echo b > /proc/sysrq-trigger`); also `az vm deallocate` and `start` | pass | |
| d | `kill -9` of hostd mid-freeze | pass after fixes | 2, 3 |
| e | leftover pool components, then a reboot | pass | |
| f | damaged pool metadata, then a reboot | pass after a RUNBOOK fix | (RUNBOOK) |
| g | 24 freeze/thaw cycles under load, two reboots in a row | pass after fix | 4 |
| h | thin pool filled past 80 percent | pass after fix | 5 |
| i | Key Vault out at start and at boot | pass after fix | 6 |
| j | agent windows after a crash | pass | |

### a. Graceful reboot

The first run (b34fc4a9, 125 MB/s disk) froze 2 of 4 guests: `small2`
and the `xl` were snapshotted first, side by side (77 s, 9.9 GB), and at
the 120 s deadline the `small` (estimate 78 s) and the `large` (141 s)
were left to the clean stop. Both frozen guests came back (restore 34.5 s
and 77.8 s); the other two stopped cleanly and were marked interrupted.

**Bug 1, 42d6779f.** I-652 says smallest first; FreezeAll sorted the
guests that way, then let one goroutine per guest race for the two
snapshot slots, and Go runs the newest goroutine first, so the largest
went first. `TestFreezeStartsSmallestFirst` failed every run ("paused in
the order [xl small]"); the slots are now taken in order.

On the fixed build and host-01's disk speed (run a3):

| step | time |
|---|---|
| reboot requested to "freezing guests" | 0.5 s |
| 4 of 4 frozen (16.1 GB) | 43.7 s: small 12.1 and 12.4, large 19.1, xl 25.9 |
| reboot requested to hostd started again | 69.9 s |
| restores, all four | 27.0 s: small 6.4 and 20.0, large 11.5, xl 27.0 |
| reboot requested to Hello | 97.7 s |

Everything kept; gaps small 84 s, large 64 s, xl 79 s, small 71 s,
inside the 98 s from the reboot request to Hello. The pool unit said
`pool_activated` and `pool_monitored` 1.3 s after it started.

### b. `az vm restart`

Azure's shutdown request (`hv_utils: Shutdown request received`) came
24.2 s after the call; the freeze started 0.4 s later. 4 of 4 frozen in
44.8 s (16.3 GB), hostd up 99.8 s after the call, all four restored in
26.2 s, Hello 126.7 s after the call. Everything kept; gaps 77 to 84 s.

### c. Hard power cycle

`echo b > /proc/sysrq-trigger` (no shutdown, so no freeze): the next
kernel 14 s later, the pool unit activated and monitored the pool in
1.3 s, hostd marked all four guests interrupted (`prev: running`) and
stopped, and hostdev's last Hello carried `interrupted_boot_id` (the new
boot) and `interrupted_at` for each. Started by hand (6.9 to 9.2 s
each), as the resume tick would. The guests lost their last 8 to 14 s of
unsynced appends (NUL bytes at the end of `ts.log`: ext4 delayed
allocation), as any crashed machine does.

`az vm deallocate` then `start` is **not** a hard power cycle: Azure
sends the same graceful shutdown request (1.9 s after the call), and all
four were frozen (45.3 s) and restored (27.5 s) after the start. The gap
includes the time deallocated (126 to 147 s).

### d. hostd killed mid-freeze

`hostd freeze` of the four loaded guests, then `systemctl kill -s KILL
hostd` at 10, 22 and 38 s. Two bugs:

- **Bug 2, 094a3899.** At 38 s the `large` guest's snapshot had finished
  and hostd had stopped its hypervisor, but the record still said
  `writing`. At the restart the whole image was discarded as
  incomplete and the guest left stopped, its memory gone, with nothing
  to start it (same boot, so no interrupted mark). The image is now
  synced after the snapshot and the record marked `written`; the next
  start finishes the freeze and restores the guest.
  `TestKilledAfterTheSnapshotRestoresTheGuest` failed before ("Reason:image
  incomplete").
- **Bug 3, 5f61d8f8.** Guests whose hypervisor outlived hostd with the VM
  paused were resumed. A VM resumed after a snapshot loses its
  virtio-fs (Cloud Hypervisor 53, virtiofsd 1.14): the `large` guest kept
  running (its container went on writing) but its shell loop, which runs
  `date` and `sleep` from /nix/store, stopped for 186 s, and the `xl`
  answered no guestd Exec at all ("guest_unresponsive"). Reproduced by
  hand on a healthy guest with `vm.pause`, `vm.snapshot` and `vm.resume`
  through `ch.sock`: `/run/current-system/sw/bin/true` then hung.
  `hostd freeze` then `hostd thaw` brought such a guest back. A paused
  VM found at start is now snapshotted again into the image and restored.
  `TestKilledWhilePausedRestoresInsteadOfResuming` failed before
  ("Reason:guest already running").

After both fixes: kills at 10, 14, 22, 28, 30, 36, 40 and 42 s, and one
the instant the `large` guest's hypervisor exited. Every time, all four
ran afterwards with a working store (`timeout 5 date` from the store in
each), and no image volume, mapping, mount or record was left. hostd
answered 19 to 66 s after the kill (a second snapshot and the restore).
Not handled: a snapshot that fails inside a freeze still resumes the
guest (recorded in I-652).

### e. Leftover pool components

Guests and hostd stopped, `staging-pool-repro.sh state` and `leftover`:
the pool inactive and `vg--guests-thin_tmeta` alone mapped, the
2026-10-10 shape. `systemctl restart repose-guest-pool` logged
`pool_leftover_removed device=vg--guests-thin_tmeta`, `pool_activated`,
`pool_monitored`, and the caches came back. Device-mapper state does not
survive a reboot, so a reboot from the same shape tests the boot path
rather than the cleanup: the pool unit activated the pool 1.2 s after it
started, `seg_monitor` said `monitored`, the pool, cache, npm cache,
registry and hostd were active, no unit failed, and the test volume's
file written before the reboot read back.

### f. Damaged pool metadata

`staging-pool-repro.sh corrupt` (thin_check: status 64), then a reboot.
The pool unit logged `pool_check_failed status=64` and wrote
`/run/repose/pool-hold`; a hostd restart and `systemctl start
repose-guest-pool` were skipped (`pool_held`); nothing ran thin_repair or
`lvconvert --repair`. hostd: "thin pool unavailable", Hello and Heartbeat
`pool_unavailable: true`, `repose_host_pool_available 0` (the
PoolUnavailable expression), and a start answered `volume_unavailable:
the host's guest pool is unavailable (inactive)`.

The RUNBOOK's step 3 failed: `thin_dump` of a damaged superblock prints
"bad checksum in superblock" and leaves an empty XML, so nothing of the
metadata was kept. **7c1668e7**: keep the device with `dd ...
conv=sparse` when thin_dump cannot read it (5.1 GiB, 234 MB on disk).
Recovery as the RUNBOOK says: `rebuild`, remove the hold, start the
pool unit (`pool_already_active`, `pool_monitored`); the caches and
hostd came back with the pool `active`.

### g. Repeated cycles and two reboots

24 `hostd freeze`/`hostd thaw` cycles of the four loaded guests, a
minute and a half each: freeze 43 to 56 s, thaw 25 to 28 s, nothing left
after any cycle, hostd's RSS 36.2 to 37.5 MB and 34 file descriptors
throughout, the host's available memory flat (46.4 to 47.8 GB), the pool
at 0.61 percent before and after.

Cycle 19 failed: all four restores, "hypervisor exited during the
restore". **Bug 4, ff306643.** The daily `nix-optimise` ran at 03:59 and
replaced the guests' `systemd` binary in the store with a hard link to an
identical file. Each guest's virtiofsd then held a deleted inode it
could not name in the snapshot ("Inode deleted" at the freeze,
"Migration source has lost inode 641" at the restore). Hosts no longer
run the full optimise pass; `auto-optimise-store` still deduplicates
each path as it is added. The host-freeze VM test asserts there is no
`nix-optimise.timer`; it failed before. The 24 clean cycles are the 18
before it and 6 after the fix.

Two graceful reboots back to back on the fixed build: 4 of 4 frozen
(43.9 s, 49.0 s) and restored (26.6 s, 26.7 s) each time, everything
kept across both, gaps 69 to 86 s.

### h. Pool past 80 percent

A 440 GiB test volume filled with zeros at about 560 MB/s. At 75
percent hostd logged `pool_warning pct 75` and `host_warning pool_high`;
`repose_host_pool_free_bytes / repose_host_pool_bytes` made PoolHigh's
expression 0.83. At 80 percent dmeventd tried to extend and could not:
"Insufficient free space: 12190 extents needed, but only 6554
available", every 10 to 50 s, the pool staying 476.15 GiB at 82.95
percent.

**Bug 5, 7f5950ce.** The 10 percent step (I-586) is twice the VG's 5
percent headroom and lvm extends only by the whole step, so autoextend
could never happen, here or on host-01. The step is 5 percent; the
host-storage VM test asserts it fits the VG's free space (before: "5.88
GiB exceeds the VG's free 3.2 GiB"). After the switch, dmeventd grew the
pool from 476.15 to 499.96 GiB (79 percent) within a minute. The test
volume was removed; the pool was rebuilt in f.

### i. Key Vault out

- **At hostd's start** (TCP 443 dropped, four frozen guests, hostd
  restarted). **Bug 6, e06897b3.** hostd fetched the key's public half,
  with a 30 s bound, before its first reconcile, so Hello waited 122 s,
  not the 90 s RUNBOOK and I-652 promise. The fetch now runs beside the
  reconcile. `TestKeyFetchAtStartDoesNotBlock` failed before ("held the
  start for 30.07s"). After: hostd answered after 92 s, the four images
  were discarded ("image key unavailable"), the guests stopped, nothing
  left.
- **At boot** (the host identity's access policy removed, then a
  graceful reboot). The shutdown froze all four with the public half
  hostd already had. After the boot the vault answered 403; the unwrap
  gave up after 90.0 s, the four images were discarded, the four guests
  marked interrupted (in Hello) for a cold start, and nothing was left.
  The policy was put back and `make plan ENV=staging` said no changes;
  hostd's key loop fetched the key again within the minute.

### j. Agent windows after a crash

Before the hard power cycle (c) each guest had `claude` and `codex`
windows in its minute record, written 5 to 37 s before the crash, and a
`pi` window opened after the last record in three of them. After the
cold start each reopened "from the minute record": `claude` and `codex`
in all four (`codex resume --last`), `pi` only where the record had it.
A window opened after the last record was not reopened, which is the
60 s the record can be behind. The clean stops of a and d reopened from
the stop record instead.

## The final build

On the branch head with every fix (89a36874), four new loaded guests:
`az vm restart`, then `systemctl reboot`. 4 of 4 frozen (50.4 s, 46.0 s)
and restored (26.9 s, 27.6 s) each time; each guest kept its boot id and
every pid through both, its logs show exactly the two gaps (66 to 98 s)
and no step back. Go: `go test -race ./...` fails only
`TestSyncInterruptedDuringTheCreateRequestNamesTheProject` (known) and
guestd's `TestHerdrProjectWarnsHerdrDown`, which reads the dev box's own
disk (its root at 90 percent raises `disk_high`). VM tests host-freeze,
host-services, host-reboot, host-caches and host-storage pass.

## Also seen

- The first `docker` command in a guest just after its restore took 47
  and 52 s twice (the container itself never stopped); a third try took
  3.1 s. Not chased further.
- After a hostd restart, `repose_host_pool_bytes` and
  `repose_host_pool_free_bytes` are 0 until the first sample (a minute),
  which makes PoolHigh's expression undefined for that minute.
- `hostdev status` and `events` do not show the interrupted marks; they
  are in `state.json`'s last Hello.

## Azure

Changed in `repose-staging` for this run (AZURE-RESOURCES.md has the
costs): the Key Vault `repose-frz-stg-3912` with the `freeze-wrap` key
(kept, purge protection on); host-stg-01's data disk at 16000 IOPS and
600 MB/s (to be put back to 3000 and 125 with the size-down); the host
identity's vault policy removed and restored during i. The VM was
restarted, reset and deallocated as the scenarios say, nothing else.
