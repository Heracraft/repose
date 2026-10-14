# Boot speed on host-01: the free page reporting A/B (2026-10-13)

The 55 s create of nuru-playground (2026-10-12 22:26Z) came 13 minutes
after host-01 started giving guests a balloon with free page reporting
(I-686). This is the A/B that tested that suspect before anything was
changed, and what it found instead. Decisions: I-704 to I-707. Raw rows:
[boot-speed-data/ab-host-01.txt](boot-speed-data/ab-host-01.txt).

## How it ran

- `ops/dev/freeze-bench.sh hup ID CLASS VOL` on host-01, as root, with
  `CLOSURE` set to base 2026.10.12 (`iswjhprp`), the closure nuru booted,
  and `VOL` a 20 GiB `bench-bf1` thin volume made and removed by the
  driver. It boots the way `internal/hostd/guest/create.go` does: mkfs.ext4
  on the volume, cloud-hypervisor 53.0 in a transient unit in
  guests.slice with the class's `MemoryMax`, `MemoryHigh` and `CPUQuota`,
  hostd's `--disk` limits, virtiofsd 1.14.0 with hostd's flags, the
  closure's kernel, initrd and kernel-params, then RegisterPaths and
  SetupProject through `ops/dev/freezebench`. Class large (4 vCPU, 8 GiB).
- `BALLOON=1` adds `--balloon size=0,free_page_reporting=on`. `CMDLINE`
  adds to the kernel line: `modprobe.blacklist=virtio_balloon` with
  `LATE=1` (modprobe after set up), `page_reporting.page_reporting_order=10`,
  `systemd.mask=repose-pin-profile.service`.
- `DIAG=1` prints the guest's pressure counters, `systemd-analyze blame`,
  `/proc/stat`, the top interrupt sources and each unit's `CPUUsageNSec`,
  plus the hypervisor's cgroup `cpu.stat`.
- One guest at a time, interleaved arms. Host load moved from 5 to 18
  during the run as tenants and other sessions created machines.
- The first runs shared the whole host store over virtio-fs. hostd shares
  a view of the closure (I-463), and the script does that now (a tmpfs in
  virtiofsd's mount namespace with each closure path bound in), but the
  rows below are from before that change. The arms are comparable with
  each other; the absolute times are heavier than a tenant's, mostly in
  RegisterPaths (12 to 19 s here, 3 to 8 s in production).

## Results

Start of the hypervisor to SetupProject done, in seconds:

| arm | load 8 to 10 | load 12 to 16 |
|---|---|---|
| balloon off | 42.4, 39.2, 35.9 | 58.8 |
| balloon on | 34.6, 35.9, 33.6 | 50.5 |
| balloon on, loaded after set up | | 53.8 |
| balloon on, order 10 | | 45.0 |

- The balloon costs nothing measurable at boot. Minor faults to set up
  were about 200,000 in every arm, and 6,000 to 9,000 in the 10 s after.
  The boot slows with the host's load instead, by up to 24 s.
- The hypervisor's cgroup was never throttled (`nr_throttled 0`). The
  guest saw the slowdown as steal and kernel time: one 46 s boot spent 19 s
  of vCPU time in user, 63 s in system, 43 s in irq, 22 s in softirq and
  31 s in steal. The top interrupt sources were function-call IPIs
  (270,000), the local timer and the virtio-fs request queue (170,000).
- Per-unit CPU in that boot: PID 1 16.2 s, repose-pin-profile 14.4 s,
  guestd 13.4 s, docker 9.4 s, user@1000 7.1 s. repose-pin-profile is not
  on the way to Ready but runs beside home-manager's first activation.
  Masked, the boot reached set up in 34.4 s against 46.0 s with it, at
  load 9.6 and 11.5 (I-706).
- Four bench boots failed: RegisterPaths and SetupProject ran past
  guestd's 30 s bound. Production creates on host-01 failed the same way
  while the bench ran (I-705).

## The branch's closure against the base, with the closure view

Run 5 shared a view of the closure, as hostd does, with the balloon on.
`base` is 2026.10.12; `new` is the boot-fast guest (I-705, I-706) built
from 47b42a9 and copied to host-01. One boot each, back to back:

| | base | new |
|---|---|---|
| host load at start | 4.3 | 9.2 |
| guestd answers | 15.6 s | 14.0 s |
| RegisterPaths | 9.6 s | 4.2 s |
| SetupProject | 14.8 s | 8.2 s |
| start to set up | 40.0 s | 26.4 s |
| home-manager-dev | 11.2 s | 5.4 s |
| docker.service | 22.1 s | 9.9 s |
| repose-pin-profile main process | 24.2 to 63.5 s of uptime | 26.0 to 56.5 s |
| `/run/repose/project-set-up` | absent | written at set up |

On the base, the copy-up started as soon as repose-paths finished and ran
beside home-manager and the session; on the branch it started once the
project was set up. home-manager, SetupProject and docker, which ran
beside it on the base, took about half as long, on a host that was
busier. RegisterPaths finishes before the copy-up can start, so its
4.2 s against 9.6 s is this pair's noise, and one pair is one pair.

## What changed

- Free page reporting stays on (I-704).
- RegisterPaths and SetupProject get 3 minutes in guestd and hostd
  (I-705).
- repose-pin-profile waited for `/run/repose/project-set-up` (I-706);
  the copy it was waiting out is gone instead (I-718, below).
- hostd logs `"boot timings"` and the api keeps them in `ops.result.boot`;
  `BootSlow` warns when most of a host's boots in 30 minutes take over
  2 minutes (I-707).

## Then the copy itself went (I-718, 2026-10-13)

Deferring the copy-up left its cost where the user works. It was removed
instead. What it was, measured on prod and on host-stg-01 the same day:

| | |
|---|---|
| the work | 26 store paths, 1554 inodes, 75.5 MB, per fresh guest |
| on a fresh small prod guest (`e2e-pin3`, base 2026.10.12) | 86.5 s wall, 18.2 s CPU, 74.1 MB written |
| the same unit, earlier runs | 31.2 s (small), 55.9 s (large, idle host), 96.7 s (teksafari-org) |
| of that, overlayfs copy-up | about 47 s, 14.8 ms per inode |
| why | first write to a thin-pool region 18.2 ms, already-allocated 3.3 to 4.4 ms |
| the guest's whole writable store afterwards | 76 MB, all of it the copy |
| what a real host GC can take of the eleven | 2: libpq-18.6, mariadb-connector-c-3.3.5 |
| fetching all eleven closures from cache.nixos.org, in a guest | 3 s and 6 s |
| fetching one back with `nix-store --repair-path`, as root | 315 ms cold, 230 ms warm, with the bytes really deleted by a host GC (1.70 s in the first probe, where a whiteout only hid the path) |
| the same with egress blackholed | fails in 8.3 s, no hang |
| 11 `nix-store -qR` calls against one | 8.50 s against 1.78 s |
| the same 1554-inode pass with the overlay mounted `volatile` | 0.725 s against 5.885 s |

Thin-pool allocation is the only serial resource in the storage stack:
on host-stg-01, 4 KB `O_DSYNC` writes to already-allocated blocks scale
linearly to 8 writers (7170 IOPS, flat latency), while writes that
allocate ceiling at about 900/s and double in latency by 8 writers, and 7
competing allocating writers inflate the identical copy-up pass 1.58x.
The per-class IOPS caps (small 2000, large 3000, xl 4000) are never
approached: 1554 serial fsyncs over 31 to 97 s is 16 to 50 a second.
