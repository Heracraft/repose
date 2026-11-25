# Three ways to run sandboxes on a Mac, measured on the M4 (2026-11-25)

Spike mac-arch, sharing program 2026-11-25 (replacement session; the first
was stopped before it wrote anything). Owner's question: get the most out
of the hardware, with real isolation, without losing what makes repose
repose. Three designs were run on the program's rented M4 Mac mini
(`e2e-mac`: AWS mac-m4.metal, macOS 15.8.3, Apple M4 with 4 performance
and 6 efficiency cores, 24 GiB). The lab owned the Mac; its runs had
priority, and this spike used one window it opened (20:49Z-21:27Z) and
a second for gVisor (22:24Z-23:00Z).

- **D1, today:** each sandbox is a Cloud Hypervisor guest nested inside
  repose's Linux appliance, which vfkit runs under Apple's
  Virtualization.framework (VZ) (DECISIONS I-1376, I-1494). Numbers from
  the lab (`docs/reports/2026-11-25-sharing-lab.md`, `~/briefs/sharing-program.log`).
- **D2:** each sandbox is its own VZ virtual machine on macOS, no
  appliance. Prototype: the unchanged repose guest bundle 2026.11.24.1
  (kernel, initramfs, `rootfs.erofs`) and a copy of a real guest's
  volume, booted by vfkit 0.6.4 (the binary repose already ships) and by
  `vzx`, a 100-line VZ launcher written for this spike to reach what
  vfkit does not expose (save/restore, balloon target, pause).
- **D3:** each sandbox is a gVisor (`runsc` release-20261123.0, arm64)
  sandbox inside the appliance, no nested VM, on the same root and volume.

## Headline

- **The nesting is the cost, not Apple's hypervisor.** The same build
  in a VZ VM straight on macOS (D2) runs as fast as directly in the
  appliance: H 42-55 s at 2-8 vCPUs, against 62-119 s in today's nested
  guest. Two at once: D2 48 / 49 s, today 157 s on the first pair after
  a boot and 572-582 s on later ones. Three D2 VMs at once: 59-61 s each.
- **Memory: D2 gives back on stop, nothing gives back while running.**
  The appliance holds 12.3 GB of the Mac from boot to shutdown. A D2 VM
  holds what it has touched (0.6 GB after boot, 5.6-6.6 GB after a
  build) and returns all of it when it stops. VZ has no free page
  reporting, and a balloon inflate did not lower a VM's footprint.
- **D2 loses the park today.** VZ's save of a running guest killed the
  guest 3 of 3 times on macOS 15.8.3, though VZ said the configuration
  could be saved. D2 also loses one hostd for Mac and x86, and the
  egress rules need rebuilding in user space. 27-40 days.
- **D3 (gVisor) is not repose.** It is quick to start and has no
  pair collapse (two builds 1.4-1.6x), but it is not a real kernel per
  project, and the guest's own systemd and Docker did not run in it.
- **Recommendation:** keep D1 for now. Spend 2-3 days on the two proofs
  that decide D2, in a shape that keeps one hostd: VZ save/restore of
  the guest (the park), and a guest VM's NIC wired into the appliance
  (the egress rules). Then build D2 in that shape if both pass.

## Numbers

### Workloads

The lab's own harness, unchanged (`/mnt/nixstore/sharing/lab/harness/guest/`):

- **H**: clean builds of three real repositories (recruiting with turbo,
  unwrap with next, pocketmail server and web), package caches warm.
- **P**: `go build -a -p $(nproc) std cmd` with a fresh `GOCACHE`, no network.

D2 used a crash-consistent copy of `e2e-citebook`'s own volume (thin
snapshot, `e2image -ra`, 9.4 GB of used blocks, taken 20:50Z), so D1's
L2 runs and D2 build the same checkouts with the same caches on the same
base. Each single-VM line is two runs on one boot; run 1 is the first
after boot, as in the lab's sweep. Each D2 pair or triple line is the
measured run after one warm-up run on the same boots (the warm-up is in
the data file too).

### One sandbox alone: wall / user / sys, seconds

| vCPUs | D1 nested guest (L2), lab 19:35Z, n=3 | Appliance itself, no sandbox (L1), lab l1.csv, n=3 | D2 VZ VM, n=2 | D3 gVisor, systrap, n=2 |
|---|---|---|---|---|
| H, 2 | 90.5, 61.9, 65.4 / 59-71 / 19-23 | 52.9, 47.5, 49.5 / 28.8-29.7 / 33.3-34.7 | 55.1, 45.6 / 53.2-53.5 / 6.4-6.6 | 67.2, 72.5 / 40.5-50.4 / 48.7-55.4 |
| H, 4 | 96.3, 84.7, 87.1 / 81-84 / 44-48 | 41.4, 44.6, 45.0 / 30.9-31.9 / 40.6-41.4 | 51.2, 42.5 / 57.5-58.2 / 8.8-9.2 | 53.3, 52.0 / 58.1-58.6 / 54.3-54.8 |
| H, 8 | 98.2, 74.5, 119.4 / 81-124 / 51-84 | 45.0, 48.2, 45.6 / 36.3-36.9 / 42.8-43.4 | 43.3, 44.9 / 64.3-64.8 / 10.4-10.6 | 51.9, 65.8 / 74.0-76.3 / 68.5-68.6 |
| P, 2 | 49.5, 50.6, 49.1 / 84 / 9 | 55.5, 46.3, 45.5 / 76.8-77.7 / 12.5-12.9 | 55.4, 48.2 (and 49.1) / 78.4-78.8 / 6.6-6.9 | 81.3, 77.9 / (3.0-78.6) / 25.5-62.9 |
| P, 4 | 60.9, 43.3, 44.1 / 121-167 / 33-64 | 27.2, 26.1, 26.2 / 88.0-89.0 / 13.8-14.1 | 36.7, 26.8 / 90.0-91.3 / 8.3-8.4 | 44.3, 39.6 / 91.6-94.1 / 55.8-55.9 |
| P, 8 | 150.6, 42.4, 35.3 / 172-567 / 59-406 | 23.5, 19.6, 19.6 / 118.4-119.2 / 20.8-21.3 | 24.8, 23.4 / 123.6-124.3 / 13.5-13.6 | 33.4, 34.5 / 118.1-121.1 / 93.2-97.2 |

Reading: with nothing else running, D2 builds H in 42-55 s at any vCPU
count, against D1's 62-119 s, and spends a third or less of D1's system
time. P scales in D2 as a build should (48-55 s at 2 vCPUs, 23-25 s at
8); in D1 it is fastest at 6 and erratic at 8. D2 lands where the
appliance itself does (L1: the same build in a chroot in the appliance,
one VZ layer, no nesting): the cost in D1 is the second, nested layer,
not Virtualization.framework. gVisor (systrap platform, the only one
usable here: its KVM platform under nesting took more than 375 s for one
H, cut) is within 15-30 percent of L1 for H at 4 and 8 CPUs and 40-70
percent slower for P, with system time 50-97 s, the cost of trapping
every syscall into its Sentry. The user column of D3 is the sandbox's
own rusage as runsc reports it and is not reliable (P at 2 CPUs: 3.0
and 78.6 s for the same build).

The L1 numbers are the lab's file `/mnt/nixstore/sharing/lab/data/l1.csv`
as of 22:23Z (runs 22:07-22:23Z), read before the lab's own report on
them; its H rows ran pinned with taskset in a chroot, so their user/sys
split is not comparable with a VM's, only their wall times are.

### Two and three sandboxes busy at once: wall / user / sys, seconds

| Run | D1 nested (lab 20:49Z) | D2 VZ VMs (this spike) | D3 gVisor (and L1) |
|---|---|---|---|
| 2 x H, 2 vCPU each | first pair after boot 115.3 / 115.2; later 296.7 / 284.2 | not run | not run |
| 2 x H, 4 vCPU each | first 157.3 / 158.6; later 572.5 / 577.2 and 578.5 / 582.3 (user 669-818, sys 405-442) | 48.0 / 49.1 (user 74.8-75.0, sys 12.9-13.0) | 74.2 / 74.3 (L1 pair: 67.0 / 68.5 and 58.7 / 67.2) |
| 2 x P, 4 vCPU each | > 820, cut | 37.5 / 38.2 (user 124.6-125.0, sys 12.5-13.3) | 65.1 / 65.0 (L1 pair: 40.6 / 40.8 and 36.3 / 39.5) |
| 2 x H, 8 vCPU each | first 154.2 / 150.4 | 47.0 / 49.6 (user 78.0-78.5, sys 13.4-13.9) | - |
| 2 x P, 8 vCPU each | 600 s timeout, both (rc 124) | 33.1 / 36.5 (user 192.1-192.7, sys 29.9-34.6) | - |
| 3 x H, 4 vCPU each | not run | 59.1 / 59.8 / 60.9 (user 88.4-89.2, sys 16.6-17.5) | - |

Reading: in D2 a second busy sandbox costs 0-15 percent and a third
about 30 percent (12 guest vCPUs on 10 cores); in D1 the same pair costs
1.5-1.9x on its first run and 3.7x to more than 15x after that. Two
builds at once directly in the appliance (L1) or in two gVisor sandboxes
there cost 1.3-1.6x, so the collapse belongs to the nested guests, not
to the appliance, its 8 vCPUs or the Mac's cores. The lab
has not settled why D1's later pairs are so slow (its 20:49Z line lists
re-faulting after reclaim and THP compaction as candidates; the
conductor's 20:49Z line adds hostd's pressure reclaim inside busy
guests). Whatever its cause, D2 does not have it: two VMs of 8 vCPUs
each (16 vCPUs on 10 cores) still built H in under 50 s.

### Memory the Mac gives up (macOS `footprint`, phys_footprint, MB)

| | D1 | D2 | D3 |
|---|---|---|---|
| Idle, no sandbox running | 12 335-12 345 for a 12 GiB appliance, all the time (this spike 19:47Z and 21:27Z; lab 19:35Z: 12 GB "and never lower since") | 0 (nothing runs) | as D1; inside the appliance MemAvailable stayed 6.85-10.9 GiB of 11.7 through every gVisor run |
| One sandbox, just booted | as above | 647 (4 GiB VM, `check`) | as D1 |
| One sandbox, after H and P (8 GiB VM) | as above | 5 699 (2 vCPU), 6 281 (4), 6 561 (8) | as D1 |
| Same, 30 s after `drop_caches` + `compact_memory` in the guest | as above (inside the appliance the guest's memory does come back to the appliance: lab 17:51Z, 21:11Z) | unchanged: 5 699, 6 281, 6 561 | as D1 |
| Two busy, 8 GiB VMs | as above | 5 670 + 5 897 (H); 4 451 + 4 293 (P) | as D1 |
| Three busy, 6 GiB VMs | not run | 5 624 + 5 611 + 5 629 | as D1 |
| Sandbox stopped | as above | returns to 0 at once (the VM's process exits) | as D1 |

- **VZ gives nothing back while a VM runs.** VZ's only balloon is the
  traditional one: the guest's virtio-balloon features were bits
  0, 2, 29, 32, 34 (MUST_TELL_HOST, DEFLATE_ON_OOM, EVENT_IDX, VERSION_1,
  RING_PACKED), with no FREE_PAGE_HINT (3) and no REPORTING (5), so free
  page reporting, which hostd relies on (I-652, internal/hostd/ch/park.go:66),
  does not exist under VZ. Inflating the balloon by 3 GiB from macOS with
  `vzx` (target 1024 MiB on a 4 GiB VM) made the guest give up the
  memory (its `free` 49 MB) but the VM's footprint rose from 2 604 to
  4 090 MB within 20 s instead of falling. So on this macOS neither D1's
  appliance nor a D2 VM returns memory to macOS while it runs; a D2 VM
  returns all of it when it stops.
- **macOS compresses and swaps a VM's memory instead.** During the D2
  triple, macOS took the idle appliance's 12 GB out of RAM (its RSS fell
  to 254 MB, footprint unchanged) into 4.8 GB of encrypted swap. Under
  pressure macOS does reclaim; it is not memory lost forever, but the
  appliance pays for it in page faults when it is touched again.
- D1's appliance holds its whole share from boot to shutdown; D2 holds
  per sandbox what that sandbox has touched since it booted, and nothing
  for a stopped one.

### Sandbox start

| | D1 | D2 | D3 |
|---|---|---|---|
| Start to sshd ready | 9.8-11 s (`repose start`, lab 18:42Z, 19:02Z) | 1.0-2.5 s from launching vfkit or vzx, every boot (about 20) | 0.02-0.03 s for runsc to run `true` (systrap), 0.08-0.09 s (kvm); a full guest with sshd was not built |
| New volume | thin LV create | 17-27 ms (APFS clone of a 40 GB sparse file) | thin snapshot |

The D2 figure is launch to port 22 open; repose's own start adds the
api round trip, hostd's work and guestd's ready report.

### Save and restore of a running VM (the park)

VZ's `validateSaveRestoreSupport()` said yes for the guest's
configuration, and `pause` took 2-4 ms, but `saveMachineStateTo` killed
the VM every time ("The virtual machine stopped unexpectedly", VZ error 1):
three tries, one after a balloon cycle, one without, one with no balloon
and no vsock device. Cause not found; the remaining
suspects are the virtio console serial port, the read-only block
device and the NAT attachment. Not tried: macOS 26.

## What repose promises, per design

D1 = today: nested Cloud Hypervisor guests inside the Linux appliance (I-1376, I-1494).
D2 = one Virtualization.framework VM per sandbox on macOS, no appliance.
D3 = gVisor (runsc) sandboxes inside the appliance, no nested VM.

| Promise | D1 nested CH (today) | D2 VZ VM per sandbox | D3 gVisor in the appliance |
|---|---|---|---|
| A real kernel per project, against a full-permission agent | Works. "Isolation between users is the hypervisor boundary ... never a container boundary" (docs/DESIGN.md:58-59). | Works, one layer fewer: the guest's own 6.8 kernel under Apple's hypervisor, each VM its own VZ XPC process (measured: booted the unchanged guest bundle). | **Lost.** One appliance kernel for every sandbox; the agent talks to gVisor's Sentry, a user-space kernel. Strong, but a container-class boundary, which DESIGN.md:58-59 rules out. Measured: the agent sees `Linux 4.19.0-gvisor`, all 8 appliance CPUs and the appliance's 12 GB as MemTotal. |
| A persistent per-project machine | Works: a thin LV per guest on vg-guests (DESIGN.md:294; internal/hostd/lvm/lvm.go:201), vdb `direct=on` (internal/hostd/ch/ch.go:192-198). | Works with change: the volume is a sparse raw file on APFS; the lvm package is replaced by a file backend. Measured: the guest's own init laid it out unchanged (images/guest/initramfs/init). | Works: the same thin LV, mounted in the appliance and bound at /home/dev (measured with gv.sh). |
| Snapshots | Works: guestd fsfreeze + `lvcreate -s`, streamed with zstd (internal/hostd/guest/snapshot.go:86-152; I-1372 for the owned store). | Works with change: an APFS clone (`cp -c`, CLONE) after guestd's fsfreeze over vsock (VZ has virtio-vsock); the stream reads a file, not an LV. | Works: unchanged LVM snapshot; the freeze is done from the appliance on its own mount, not through guestd. |
| Undo (`repose restore`, `snapshots restore --as`) and fork clones (I-701; the command `fork` itself was removed, I-1082) | Works (internal/hostd/guest/clone.go:14-20; docs/features/snapshots.md). | Works with change: a restore writes a raw file; a clone is an APFS clone. | Works unchanged (LVM). |
| The park: memory kept across a host restart (I-652, I-653, I-1386, I-1394) | Works: CH `vm.snapshot` into a dm-crypt thin volume, key wrapped by the Mac's Keychain over vsock 1386; 6.5 s a guest to park, 3.8 s to restore (I-1386). | **Lost today**, measured: `saveMachineStateTo` killed the guest in 3 of 3 tries on macOS 15.8.3 although `validateSaveRestoreSupport()` passed. If a later macOS saves this guest, the park becomes a VZ state file encrypted with the Keychain key directly (internal/parkkey reused, no vsock hop). Until then a Mac restart, or a crash of the process that owns a VM, ends every machine's work (VZ VMs die with their process). | Works with change, untested here: `runsc checkpoint`/`restore` into the same dm-crypt volume; hostd's park package rewritten around it. |
| Resting: an idle machine's vCPUs paused, memory kept (I-818) | Works in code (CH `vm.pause`, internal/hostd/guest/pause.go), but the Mac's hostd.env does not pass `--pause-idle` (images/appliance-arm64/mac/etc/repose/hostd.env); only the fleet turns it on. | Works with change, measured: VZ `pause` in 2-4 ms, footprint unchanged while paused (4 090 MB before and after). hostd's pause.go moves from CH `vm.pause` to VZ, and nothing returns memory while it rests. | Works with change: `runsc pause` (cgroup freezer) instead of `vm.pause`. |
| Claude login share (I-278, I-464) | Works: per-guest unprivileged `virtiofsd-auth` with `--translate-uid` on the user's `auth-<user>` LV (internal/hostd/guest/claudeauth.go; internal/hostd/virtiofs/virtiofs.go:72-97). | Works with change: a VZ shared directory per user (vfkit already shares one into the appliance, internal/cli/appliance.go:410); uid translation and the file bind need re-proving. | Works, simpler: a bind mount of the share into the sandbox; no virtiofsd. |
| Dirty-checkout sync and copied tool logins (I-367, I-422) | Works: laptop to guest over SSH only (docs/features/sync-at-launch.md:232-274). | Works, as long as SSH reaches the guest (next row). | Works, as long as SSH reaches the sandbox. |
| SSH through the edge gateway (I-1380, I-1382, I-1368) | Works: wg0 in the appliance, `br-guests`, the guest's sshd with the platform CA (DESIGN.md:312-337). | Works with change: no Linux to run wg0. hostd on macOS needs userspace WireGuard (wireguard-go on a userspace netstack) forwarding to the VMs' NAT addresses, or a Network Extension (signing, user approval). | Works with change: the sandbox gets a veth on `br-guests` in a netns instead of a tap; sshd runs inside it. |
| Egress rules and networking (I-18, I-217, I-238; internal/hostd/net/net.go) | Works: per-guest tap, nftables bridge admission, block chains, tc policer. | Works with change, the largest: VZ NAT (vmnet shared) puts every VM on one subnet with the Mac's LAN reachable and no per-VM filter. Parity needs a file-handle network attachment to a userspace switch in hostd that re-implements the rules in Go. | Works with change, small, untested: runsc's own netstack on a veth; the nftables rules key on the veth instead of the tap. |
| virtiofs | Only the login share on erofs guests (ch.go:201-214). | See the login share. | Not needed (gofer / bind mounts). |
| The browser (Xvnc, I-246, I-292) | Works, inside the guest (docs/features/browser.md:73-98). | Works unchanged (same guest image; inside the VM). | Works with change, unverified: Xvnc and Chromium under gVisor; Chromium's own sandbox needs namespaces gVisor emulates partly. |
| Docker inside the machine (/var/lib/docker on the volume, images/guest/initramfs/init) | Works. | Works unchanged. | **Lost as shipped**, measured: dockerd in the sandbox stopped with `failed to create NAT chain DOCKER: iptables not found` (with capabilities given; without them, `chown /var/run/docker.sock: operation not permitted`). gVisor documents a Docker-in-gVisor setup with its own flags; not tried. |
| The guest's own systemd (tmux sessions, browser, npm registry, zram units in images/guest/rootfs/etc/systemd) | Works. | Works unchanged. | **Lost as shipped**, measured: the guest's own `/lib/systemd/systemd` as PID 1 printed nothing in 25 s and was cut. The 33 units in images/guest/rootfs need another supervisor. |
| Memory sharing on owned hosts (I-1626, I-1628, I-652) | Works inside the appliance (free page reporting, zram, OOM order), but macOS gives up the appliance's whole share as soon as it is touched and never gets it back: no balloon on the appliance (lab 19:35Z). | Different, measured: macOS gives up only what running sandboxes have touched (647 MB for a freshly booted 4 GiB VM, 5.6-6.6 GB after a build in an 8 GiB VM) and all of it the moment one stops. Nothing comes back while a VM runs: no free page reporting under VZ (balloon features 0, 2, 29, 32, 34), and a balloon inflate raised the footprint. Sharing between sandboxes is macOS's compressor and swap, not hostd's zram and OOM order (I-1626): ruling items 3-4 become "macOS decides". | Works best inside the appliance (the Sentry allocates on demand); macOS still gives up the appliance's whole share, as in D1. |
| Host updates without reinstalling (I-1496; I-1638 on branch host-files) | Works: signed hostd from the release, systemd restarts it, guests keep running. | Works with change: hostd becomes a macOS binary that must carry the virtualization entitlement (codesigned); the updater moves from systemd to launchd. A VM's process is launched by hostd, so a hostd restart must not kill VMs (VZ VMs die with the process that owns them). | Works: plus runsc shipped as a host file. |
| One hostd for Mac and x86 (I-1376, I-1494, I-1498) | Works: one Linux hostd, the same host files. | **Lost**: a second hostd backend (VZ, APFS, userspace network, launchd) kept in step with the Linux one. | Works with change: one Linux hostd with a second runtime (runsc beside CH), or x86 also moves to runsc. |

## Cost to build D2 or D3

Estimates are working days for one builder with review, from reading the
code named; none of it was built. hostd today is Linux only: systemd
units, LVM, nftables, Cloud Hypervisor (internal/hostd/, 17 132 lines in
internal/hostd/guest alone).

### D2: a VZ VM per sandbox, hostd on macOS

| Piece | What changes | Files | Days |
|---|---|---|---|
| VM runtime | a per-VM launcher process (like `vzx` or vfkit) started through launchd, so a hostd restart does not kill VMs (a VZ VM dies with the process that owns it); hostd talks to it instead of CH's API socket | internal/hostd/ch (754 lines) replaced by a vz package; guest/create.go, unit.go, shutdown.go, stop.go, reconcile.go (today `systemctl list-units 'guest@*'`) | 4-6 |
| Volumes | sparse raw files on APFS: create, grow, delete; snapshot = APFS clone after guestd's fsfreeze; restore writes a file; fork = clone | internal/hostd/lvm (863) replaced; guest/snapshot.go, clone.go, pool.go; snapshot/stream reads a file | 3-5 |
| Network and edge | VZ NAT has no per-VM filter and reaches the user's LAN. Parity needs a file-handle network attachment per VM to a userspace switch in hostd, the egress rules (smtp, stratum, flow blocks, policer) re-done in Go, and WireGuard to the edge in userspace (wireguard-go on a userspace netstack) or a signed Network Extension | internal/hostd/net (900) replaced; images/appliance-arm64/host/etc/repose/*.nft and the wg-quick unit retired | 8-12, the largest risk |
| Park | blocked: VZ save kills this guest on macOS 15.8.3. If it works later: VZ state file encrypted with the Keychain key | internal/hostd/park (833), guest/park.go, internal/parkkey | 2-3 once VZ saves |
| Resting | VZ pause/resume | guest/pause.go, control/pause.go | 1 |
| Login share | a VZ shared directory per user; prove uid mapping and the file bind | guest/claudeauth.go, virtiofs | 1-2 |
| Samples, memory, owned mode | no cgroups: per-VM CPU and footprint from macOS (task_info), owned admission on macOS free memory, OOM order left to macOS | metrics, hostinfo, guest/samples.go, guest/owned.go | 2-3 |
| Install and updates | hostd becomes a codesigned macOS binary with the virtualization entitlement; the updater moves from systemd to launchd; `repose host add` installs hostd and its launchd jobs instead of the appliance | internal/cli/appliance.go (1 495), hostscmd.go, images/appliance-arm64 (Mac part retired), I-1496 updater | 3-4 |
| Tests, fakes, docs | a fake VZ runtime that refuses what VZ refuses; own-hardware.md rewritten | internal/hostd/fakeguestd, apps/web/src/content/docs/own-hardware.md | 3-4 |
| **Total** | | | **27-40**, and two hostd backends kept in step afterwards |

A cheaper shape, not tried today (call it D2b): keep a small appliance for
hostd, WireGuard and nftables, and run each guest as a VZ VM beside it,
launched by the macOS-side `repose host run` process that already exists
(internal/cli/hostscmd.go:82). Each guest's NIC would be a file-handle
attachment whose other end is a NIC of the appliance, so the bridge and
the nftables rules stay as they are. That keeps one hostd and the egress
rules; it still needs APFS volumes, the launcher protocol, and a fixed
number of appliance NICs (VZ adds devices only at boot). Unverified:
whether VZ allows that many network devices on the appliance, and the
throughput of a file-handle NIC. Rough size 12-20 days.

### D3: gVisor in the appliance

| Piece | What changes | Files | Days |
|---|---|---|---|
| Runtime | `runsc` OCI bundle per guest (root = erofs + overlay on the volume, binds for home, docker, services) in the guest@ unit instead of cloud-hypervisor | guest/create.go, unit.go, erofs.go, ch replaced by a runsc package | 4-6 |
| guestd transport | vsock does not exist in a sandbox: a unix socket bound into it | internal/hostd/vsockclient, vsockrpc, cmd/guestd | 2-3 |
| Network | runsc's netstack on a veth in a netns, attached to br-guests; rules keyed on the veth | internal/hostd/net | 2 |
| Park, rest, snapshots | `runsc checkpoint/restore` into the dm-crypt volume; `runsc pause`; fsfreeze from the appliance | park, guest/park.go, pause.go, snapshot.go | 3-4 |
| Guest image | systemd did not come up as PID 1 and Docker did not start: the 33 units in images/guest/rootfs (tmux sessions, browser, npm registry, docker start, zram) move to another supervisor, and Docker needs gVisor's own setup or goes | images/guest (Dockerfile, rootfs/etc/systemd), cmd/guestd | 5-8 |
| Security review | the boundary changes from a hypervisor to gVisor's Sentry; DESIGN.md:58-59 and every promise built on it re-argued | docs/DESIGN.md, docs/features/secrets.md | 2 |
| **Total** | | | **18-25**, and an owner decision that a container-class boundary is acceptable |

## Recommendation

Keep D1 on the Mac for now, and do not build D3.

D2 is where the hardware is: one fewer virtualization layer makes every
build 1.2-2x faster alone and up to 12x faster when two run at once,
lets a stopped machine give its memory back to macOS, and starts a
machine in 1-2.5 s. But D2 as measured loses two things repose promises
in writing and keeps a third only at high cost: the park (VZ's save
failed), the egress rules, and one hostd. The evidence supports building
D2 only once those are answered, so the next step is two short proofs
(2-3 days together), both on the Mac:

1. **The park on VZ.** Find why `saveMachineStateTo` kills this guest:
   remove devices one at a time from `vzx` (console port first, then the
   read-only disk, then NAT), and try macOS 26. If no configuration
   saves, D2 has no park, and a Mac restart costs every machine its
   work, which I-1386 was written to prevent.
2. **D2b: VMs beside a small appliance.** Keep hostd, WireGuard and
   nftables in a small appliance; run each guest as its own VZ VM,
   launched by the macOS process `repose host run` that already exists
   (internal/cli/hostscmd.go:82), with the guest's NIC a file-handle
   attachment whose other end is a NIC of the appliance. Measure the
   NIC's throughput and check that the bridge and nftables rules apply
   unchanged. If it works, one hostd stays and the 8-12 days of
   userspace networking go away; D2b is then about 12-20 days.

Meanwhile on D1: the pair collapse is specific to nested guests (L1 and
gVisor pairs in the same appliance do not collapse), so the owned vCPU
rule cannot fix it; the lab's runs say N=2 suffers least.

What would change this:
- the lab finds and fixes the cause of the nested pair collapse, so
  that two D1 guests cost 1.5x or less: D1's remaining gap is then
  1.5-2x for one build, and D2's cost waits for a customer who feels it;
- VZ save works in some configuration: D2b goes ahead;
- VZ save cannot be made to work: D2 trades the park for speed, which
  is the owner's call, not this report's;
- the owner accepts a container-class boundary on hardware the user
  owns (the only tenant there is the owner): D3 becomes a candidate, at
  18-25 days plus the guest image's systemd and Docker.

## What was not run

- D1 numbers are the lab's, not re-run. The lab's L1 rows were read
  from its data file before its own report on them.
- D2 and D3 ran n=2, not n=3 (the conductor cut the window, 20:00Z).
  D2 singles had no warm-up beyond run 1; its pairs and the triple each
  had one warm-up run on the same boots.
- D2 pairs at 2 vCPU, D3 pairs at 8 and a D3 triple: not run.
- D2 ran while the appliance (12 GB) was still running idle; macOS swapped
  it out (4.8 GB of swap) during the triple. That swap was still there
  at 21:39Z and confounded the lab's next pair (lab 21:39Z line).
- VZ save/restore: three attempts, all failed; causes not isolated;
  macOS 26 not tried. Restore was never reached.
- Balloon: one inflate, 20 s observed. Whether macOS lowers the
  footprint later or under pressure was not measured.
- gVisor: systrap only (KVM platform cut at 375 s). Its own network
  stack, checkpoint/restore, pause, a full guest with sshd, the browser
  and the Docker-in-gVisor setup: not tried. Memory per sandbox: only
  the appliance's MemAvailable was sampled (the appliance has no
  `ps`/`pgrep`, so per-process RSS was not read).
- The macOS scheduling class of the D2 VM processes was read only on an
  idle VM after the runs (PRI 31, NI 0, the same as the appliance's VZ
  process at that moment).
- The appliance's disk.raw held 58.9 GB on the Mac at 22:57Z. Some of it
  is this spike's (thin snapshots written by the gVisor builds, removed
  afterwards, but a removed LV does not shrink the file); how much was
  not measured.
- Nothing about an x86 host: every number is the M4.

## Provenance

- Data on woker-1: `/mnt/nixstore/sharing/mac-arch/d2.csv` (every D2
  build), `fp.csv` (D2 footprint every 5 s), `win-*.log` (the window's
  console output), `gv.csv` (D3); the lab's `/mnt/nixstore/sharing/lab/data/`
  (`sweep.log`, `runs.jsonl`, `pair*.log`).
- Scripts: `/mnt/nixstore/sharing/mac-arch/s/` (`window.sh`, `d2.sh`,
  `vz2.sh`, `vzx.swift`, `cp.sh`, `gv.sh`).
- Nothing was installed on macOS. Files lived under `~/mac-arch` and the
  launcher share's `mac-arch/` folder and were deleted at the end; the
  gVisor binary lived under `/run/macarch-gv` in the appliance and was
  removed with its thin snapshots.
