# Guest freeze across host restarts: experiments (2026-10-11)

What Cloud Hypervisor's snapshot and restore do to a repose guest when the
host restarts under it, measured before DECISIONS I-652 (the design) and
I-653 (the images) were written. Everything here ran on woker-1, the Azure
dev box (Standard D-series, AMD, 8 vCPU, 15 GiB, nested KVM, local NVMe at
`/mnt/nixstore`). The hosts are Intel (DESIGN.md §4), and host-01's data
disk is Premium SSD v2 at 600 MB/s, so the times below are the shape, not
the host's numbers; the staging host gives those (I-652, "Proof still
owed").

## How it ran

- The guest is the real runner (`nix build ./nix#guest-runner`, base at
  kanali/main 1b0bfbe5, kernel 6.18.52), Cloud Hypervisor 53.0 and
  virtiofsd 1.14.0 from the same closure, booted the way hostd boots it:
  ext4 volume (a raw file, `direct=on`), the store over virtio-fs, a tap,
  vsock to guestd. `ops/dev/freeze-bench.sh` does the boot, freeze and
  restore; `ops/dev/freezebench` is the stand-in for hostd's side of
  vsock (wait, RegisterPaths, SetupProject, Exec, the clock).
- A freeze is: `vm.pause`, `vm.snapshot` to a directory, `vmm.shutdown`,
  virtiofsd and the tap removed (what a host reboot does to them). A
  restore is: tap, a new virtiofsd, `cloud-hypervisor --restore
  source_url=file://...`, `vm.resume`, guestd's first answer, the clock
  set through guestd's Exec.
- With `ENC=1` the image goes to an ext4 on dm-crypt (plain,
  aes-xts-plain64, a 512-bit random key) over a loop device, as hostd puts
  it on a thin volume (I-653).
- Probes in the guest (`/tmp/gf/probes`, added to the store): a writer
  appending consecutive integers to `~/seq.log` every 100 ms with an fsync
  each; a reader holding a store file (the initrd, 11 MB) open over
  virtio-fs and hashing it 64 KiB every 50 ms, comparing every pass to the
  first; one TCP connection from the guest to an echo server on the host,
  a numbered line a second, every echo checked; a tmux window running a
  process that prints its pid and a counter every second (an agent stand-in).
- Loads: `idle`; `mem` (stress-ng `--vm 2 --vm-bytes 35% --vm-keep`);
  `build` (stress-ng `--cpu 0 --cpu-load 90 --hdd 1 --hdd-bytes 512M`);
  `cache` (2.5 GB written and read back, so it sits in the guest's page
  cache).

## Timings and image sizes

Encrypted image, warm host page cache, one guest at a time. `freeze` is
pause to image closed (it includes making and mounting the encrypted
volume, about 40 ms, and the flush at unmount); `snap` is `vm.snapshot`
alone; `image` is what the image holds on disk; `restore` is the
hypervisor reading the image; `back` is restore start to guestd answering.
`drop` means the guest wrote back and dropped its page cache 10 s before
the freeze. `balloon` means the guest had a virtio-balloon with free page
reporting.

| class | balloon | load | drop | snap s | freeze s | image GB | restore s | back s |
|---|---|---|---|---|---|---|---|---|
| small | yes | idle | no | 0.35 | 2.6 | 0.81 | 0.67 | 0.73 |
| small | yes | mem | no | 1.25 | 4.3 | 2.06 | 1.40 | 1.46 |
| small | yes | build | no | 0.41 | 3.6 | 1.31 | 0.91 | 0.96 |
| small | yes | cache | no | 2.63 | 6.9 | 3.56 | 2.64 | 2.69 |
| small | yes | cache | yes | 0.51 | 4.0 | 1.61 | 1.12 | 1.17 |
| large | yes | idle | no | 0.41 | 2.9 | 1.01 | 0.76 | 0.81 |
| large | yes | mem | no | 4.98 | 9.2 | 3.49 | 2.36 | 2.41 |
| large | yes | build | no | 0.48 | 4.2 | 1.47 | 1.04 | 1.09 |
| large | yes | cache | no | 3.03 | 7.1 | 3.65 | 2.40 | 2.45 |
| large | yes | cache | yes | 0.45 | 3.8 | 1.51 | 1.04 | 1.08 |
| xl | yes | idle | no | 0.52 | 3.4 | 1.21 | 0.99 | 1.04 |
| xl | yes | mem | no | 12.97 | 20.3 | 6.62 | 5.53 | 7.19 |
| xl | yes | build | no | 0.60 | 4.3 | 1.70 | 1.31 | 1.52 |
| xl | yes | cache | no | 5.26 | 7.7 | 3.88 | 2.74 | 2.79 |
| xl | yes | cache | yes | 0.41 | 3.4 | 1.24 | 0.90 | 0.95 |
| large | no | build (after mem) | no | 3.30 | 7.2 | 3.52 | 2.31 | 2.36 |
| large | no | cache | yes | 3.70 | 7.0 | 3.66 | 2.37 | 2.41 |
| xl | no | build (after mem) | no | 9.89 | 13.2 | 6.66 | 5.80 | 5.86 |
| xl | no | cache | yes | 14.64 | 17.8 | 6.66 | 6.21 | 6.30 |

Raw rows: [guest-freeze-data/matrix.txt](guest-freeze-data/matrix.txt)
(the first block is an earlier run without the balloon and with an
in-guest drop that waited no time).

- **The image is what the guest's memory holds, not the class.** The memory
  file is as long as the guest's RAM and sparse: Cloud Hypervisor writes only
  the ranges the guest touched. An idle guest is 0.8 to 1.2 GB whatever its
  class.
- **Without free page reporting the image only grows.** Pages a guest frees
  (an exited build, a dropped cache) stay populated in the host's memfd, so
  the next image still holds them: an xl guest that once ran a 5.6 GB
  stress-ng wrote 6.6 GB images from then on, idle or not, and a dropped
  cache saved nothing (rows 16 to 19). With a balloon of size 0 and
  `free_page_reporting=on`, the guest hands freed pages back (the
  hypervisor punches them out of the memfd) and the image follows what the
  guest uses: the same cache case went from 3.6 GB to 1.5 GB, and the
  guest's resident memory on the host from 3.4 GB to 0.75 GB 40 s after the
  drop. Reporting is not immediate: about 1.6 GB of 2.5 GB came back in the
  first 8 s.
- **Dropping the cache costs the guest a cold cache after the restore**, and
  is worth it only with reporting. hostd drops it (`sync; echo 3 >
  /proc/sys/vm/drop_caches`) for every guest at once, then waits 5 s, then
  snapshots (I-652).
- **Copy restore, not on-demand.** `memory_restore_mode=ondemand` restored
  in 0.11 s against 0.75 s (userfaultfd works with the shared memfd), but
  the next freeze of that guest wrote the whole 8 GiB: on-demand restore
  left every page of the memfd populated (resident 8.4 GB, the class size,
  for the rest of the guest's life), and the image file must stay readable
  while the guest runs. A copy restore keeps the memfd sparse (resident
  864 MB before, 875 MB and 884 MB after two cycles).
- **Encryption costs about nothing here.** Freeze 2.2 s plain against
  2.7 s encrypted for the same 0.9 GB idle large guest (the difference is
  making the volume and the flush through dm-crypt); restore 0.73 s
  against 0.73 s warm. A cold restore (host page cache dropped) read 0.96
  GB in 3.0 s, 320 MB/s through dm-crypt from local NVMe.
- **Throughput.** Snapshot writes go to the page cache first; the time that
  counts at shutdown is until the image is on disk, which is bounded by the
  disk: host-01's data disk is 600 MB/s, so about 1.7 s per GB of image
  before encryption overhead, shared by every guest freezing at once.
  host-01 on 2026-10-11 18:18 UTC had 2 guests running, resident 11.3 GB
  and 6.5 GB (`systemctl show -p MemoryCurrent`): about 30 s to write both
  as they are, less after the drop with reporting. A 64 GiB host with every
  guest's memory dirty would need under 2 minutes.

## What survives a freeze

Every probe ran through every cycle of every row above and through the 60
cycle loop below.

- **tmux and the agent stand-in**: same tmux server, same session, same pid,
  counter carrying on from where it stopped. Processes do not notice beyond
  the clock jump.
- **Files written**: the writer's sequence has no gap and no repeat across
  every cycle (checked line by line at the end of the loop). The disk and
  the memory agree because nothing writes the volume between the pause and
  the restore; hostd checks that with a fingerprint of the volume's first 4
  KiB (I-652).
- **Open files on the store share, with a new virtiofsd**: they survive.
  I-233 left this open. virtiofsd 1.14 implements the vhost-user device
  state transfer: at `vm.snapshot` Cloud Hypervisor asks it for its state
  (its log: "Front-end did not announce migration to begin ... If you are
  doing a snapshot, that is OK"), the state goes into the snapshot
  (`backend_state` in `state.json`), and at restore the new virtiofsd gets
  it and finds each inode again by path (its default `--migration-mode
  find-paths`). After a restore and a guest `echo 3 > drop_caches` (so the
  guest kernel had to ask virtiofsd again), the reader's held file still
  hashed the same and `ls` and `cat` of store paths worked. The paths must
  still exist on the host: a store path the guest uses has to survive until
  the restore, which the GC roots of a guest's closure already ensure.
- **vsock to guestd**: the host side is new after a restore (hostd dials
  again); guestd answered 5 to 25 ms after `vm.resume` in every cycle.
- **The tap and TCP**: the guest's connection to the host's echo server
  survived every freeze while the server stayed up (20 s, 30 s, and the
  loop's cycles): the guest resumes in the middle of the connection and the
  peer had only seen a pause. When the host itself restarts, the peer is
  gone and the guest's next send gets a reset; the client reconnects (see
  "Host-side peers" below). Users see this as their SSH session dropping:
  every connection that goes through the host (the gateway's SSH, `repose
  open` forwards) ends, and the agent's own HTTPS connections time out and
  are retried by the agent.
- **The clock**: the guest resumes at the time of the snapshot. Measured
  behind by exactly the time it was frozen (31.7 s after a 30 s freeze plus
  the restore). guestd has no clock correction today (I-233 assumed it
  would); hostd sets it right after the resume with guestd's Exec (`date -u
  -s @<host time>`), so no guest change is needed. The monotonic clock does
  not jump, so timers and timeouts in the guest see no gap.

## Encryption of the image

I-233 and I-235 left the image out because it holds the guest's RAM: named
secrets in `/run/repose/secrets`, tool logins, whatever agents hold. I-653
puts it on dm-crypt with a fresh 512-bit key per image, wrapped with an RSA
key in Key Vault: hostd wraps with the public half it fetched ahead of time
(no network at shutdown), and only the host's managed identity can unwrap,
at Key Vault, when it restores. What is on the host's disk is ciphertext and
a wrapped key. Cost measured above: about 0.5 s per freeze, nothing at
restore. Not measured here: Key Vault's unwrap latency from a host (one call
per guest, tens of milliseconds expected).

## What happens when something changed

- **Another Cloud Hypervisor version.** A CH 53.0 snapshot restored with
  CH 45.0 (nixos-25.05) was refused before anything ran: "Detected
  incompatible CPUID entry: leaf=0x40000001 ... VM Restore failed:
  CpuidCheckCompatibility" (45.0 also has no `memory_restore_mode`). The
  same image restored afterwards with 53.0 and the guest carried on, so a
  refused restore leaves the image intact. Snapshots carry no compatibility
  promise across versions, and the same CPUID check refuses a restore on a
  host with other CPU features (an Azure Redeploy to other hardware). hostd
  records the version and does not try another one.
- **The store share without the guest's paths.** Restoring with virtiofsd
  sharing an empty directory: virtiofsd refused the saved state ("Migration
  failed: Failed to load state: Inode 2: Opening .../nixos-system-...: No
  such file or directory"), Cloud Hypervisor stopped with "Cannot create
  virtio-fs device", nothing ran. So a store path the guest held open must
  be back in the guest's store view before the restore; hostd rebuilds the
  view from the guest's closures as at a boot, and a guest that held a path
  outside them restores nothing and boots.
- **The closure.** The restored guest keeps running the closure it had in
  memory; what changed is what the store share serves. A path it uses that
  is gone from the host fails its reads. hostd restores only when the guest's
  recorded closure is the one in the record, the class, the volume size,
  the volume's fingerprint and the hypervisor version are unchanged, and the
  image is under 6 hours old; anything else is discarded and the guest boots
  (I-652).
- **The volume written since.** Restoring memory onto a disk that moved on
  corrupts the filesystem. The fingerprint (first 4 KiB of the volume, read
  with O_DIRECT, which holds ext4's superblock: mount count, mount and write
  times, state) changes with any mount or fsck; any change discards.

## Repeated cycles

60 encrypted freeze and restore cycles of one large guest with a balloon,
under a constant load (stress-ng: 2 CPU workers at 50 percent, a disk
writer, 1 GiB of churned memory) and all the probes, 3 s of running between
cycles ([guest-freeze-data/loop-60.txt](guest-freeze-data/loop-60.txt)):

- every cycle restored; the writer's whole file at the end: 1091 lines,
  0 to 1090, no gap, no repeat; the store reader 27 passes, every one equal
  to the first, no read error; the TCP connection the same one throughout
  (one `connected`, no error); the tmux window's process the same pid;
- freeze (pause to image closed) 4.5 s median 5.4 s, max 10.2 s; image
  1.9 to 3.1 GB; restore median 1.7 s, max 3.7 s;
- no leak on the host: after each cycle no dm-crypt mapping or loop device
  left, one hypervisor process, one virtiofsd, available memory flat
  (7.1 to 11.1 GB, ending higher than it started); the guest's resident
  memory on the host followed its use (1.9 to 3.0 GB), never the class's
  8 GiB.

Host-side peers: with the echo server killed while the guest was frozen
(as a host reboot kills every host-side end), the guest's first send after
the resume got the connection closed and the client reconnected as soon as
the server was back ("closed", "Connection refused" for 12 s, "connected
2"). That is what a user's SSH session and an agent's open HTTPS
connections see.

## Mount propagation into the hypervisor's sandbox

guest@ units see only their own guest directory (`TemporaryFileSystem` on
the guests directory, `BindPaths` of their own). A mount made on the host
under `<guest dir>/freeze` after the unit started is visible inside it
(checked with a transient unit with the same properties: `findmnt` in its
namespace showed the new mount as `shared,slave`), so hostd mounts the
encrypted image there and the hypervisor writes into it without a new
property, which a transient unit could not get while running.

## Ordering the freeze before systemd stops the guests

Transient units cannot be given dependencies after they start, and at
shutdown systemd stops guest@ units at the same moment as everything else
(host-01 2026-10-10 05:45:32). A drop-in on the template name
(`/etc/systemd/system/guest@.service.d/*.conf`) applies to transient units
of that name, including those already running once systemd reloads
(checked on systemd 255: `DropInPaths` and `Before=` showed on a running
transient `gftest@a.service` after `daemon-reload`). With
`Before=repose-guest-freeze.service` on guest@, virtiofsd@ and
virtiofsd-auth@, a stop of the freeze unit and a guest in one transaction
stopped the freeze unit first and the guest only after its 3 s ExecStop
had finished (journal, 18:17:40.339 to 18:17:43.342).

## Azure's time budget

- Scheduled Events give at least 15 minutes for Reboot, 10 for Redeploy,
  30 seconds for Preempt (Spot only), 5 to 15 for Terminate; Freeze (a
  pause of seconds for memory-preserving maintenance or live migration)
  15 minutes. Events go ahead at NotBefore or when approved; a hardware
  failure skips the notice and starts a Reboot at once.
  https://learn.microsoft.com/en-us/azure/virtual-machines/linux/scheduled-events
- How long Azure waits for a guest OS to finish a graceful shutdown
  (`az vm restart`, the hv_utils shutdown request of 2026-10-10) is not
  documented. Azure offers `skipShutdown` to skip it, which says the wait
  exists. host-01's shutdown on 2026-10-10 was graceful and ended within
  seconds because nothing held it. The staging host measures it (a unit
  whose ExecStop sleeps, then `az vm restart`); until then the freeze's
  deadline is 120 s.

## Staging plan (what proves it end to end)

Needs a staging host that is Intel (D-series v6 or v7, like host-01), with
a Premium SSD v2 data disk at host-01's 600 MB/s (staging.tfvars has 125
MB/s, which would make every freeze about five times slower than prod),
enough RAM for one large and one small guest (D8s_v7, 32 GiB, is enough;
the D2s_v7 in staging.tfvars cannot run a large guest), joined to the
production api and drained, `freeze_keyvault_name` set for staging and
applied, and the host built from this branch with
`repose.host.freeze.enable = true` and `keyVaultUrl` from `tofu output
freeze_keyvault`. Then, with an `e2e-` project of each class running the
probes from `ops/dev/freeze-bench.sh` (`start-probes`), in order:

1. `hostd freeze`, `hostd thaw` on the host: both restored, probes intact;
   `freeze` and `thaw` times per guest.
2. Azure's graceful-shutdown wait: a unit whose ExecStop sleeps 600 s, then
   `az vm restart`; the time from the shutdown's first journal line to the
   last line of the previous boot is how long Azure waits. Sets the
   deadline.
3. `systemctl reboot` from woker-1 (`systemd-run`, RUNBOOK "Switch a host
   to main"): both guests restored before the stream's Hello, the api never
   showing them stopped, the probes intact, `repose ssh` working again.
4. `az vm restart`: the same through Azure's shutdown request.
5. `repose.host.freeze.scheduledEvents = true`, then `az vm restart`: the
   Reboot event is logged, the guests freeze, the event is approved and the
   restart happens within a minute rather than 15.
6. A failed restore: change one guest's closure record by hand between
   freeze and start (or stop Key Vault access for the host identity): that
   guest starts cold (L6), the other restores.
7. A Key Vault outage at start: deny the host's network to the vault;
   after 90 s each guest starts cold; nothing is left in `lvs | grep frz-`.
