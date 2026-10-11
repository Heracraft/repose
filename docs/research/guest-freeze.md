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

Two guests at once (as hostd freezes them, two at a time): a large guest
holding 4.7 GB after the build runs below and a small idle one froze
together in 10.9 s (5.5 GB of images, about 500 MB/s to local NVMe through
dm-crypt) and restored together in 6.2 s. After a cache drop and 5 s, the
large guest's image was still 3.9 GB: free page reporting hands back only
free blocks of 2 MiB and more, and a guest whose memory is fragmented by
builds keeps much of it. The image sizes in the table are the better
case; the deadline must be set from a host's real guests.

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

A real build: `go build -a -v std cmd` from an empty cache in a large
guest, frozen and restored three times while it ran, finished with exit 0
in 143 s of guest wall time against 113 s for the same build with no
freeze; the 30 s difference is the time the guest spent frozen (the clock
is set forward at each restore).

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
  documented; `skipShutdown` exists to skip it. Measured on host-stg-01
  (below): about 600 s, then the VM is powered off. The freeze's deadline
  stays 120 s, well inside it.

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

## Staging: host-stg-01, 2026-10-11 19:52Z to 20:55Z

host-stg-01 is Intel (Standard_D16s_v7, 64 GiB) with a Premium SSD v2 data
disk at 3000 IOPS and **125 MB/s** (host-01: 16000 and 600). It ran this
branch merged onto `staging-host` (a test branch, `gf-stg-test`, never
merged) with `repose.host.freeze.enable` and an RSA key file on `/run`, the
real hostd, registered with `hostdev` on the staging edge. Two e2e guests
through `hostdev`: e2e-frz-large (large, a Python process holding 3 GiB
and touching one byte per MiB every second, 4.1 GB resident) and
e2e-frz-small (small, idle, 0.77 GB resident), each with the probes
(writer, store reader, a TCP connection to an echo server on the host's
bridge address, a tmux window with a counter). Raw output:
[guest-freeze-data/staging-host-stg-01.txt](guest-freeze-data/staging-host-stg-01.txt).

**Azure's graceful-shutdown wait, measured.** With the plain staging build
and a unit whose ExecStop loops forever (TimeoutStopSec 1200), `az vm
restart`: the guest OS got `hv_utils: Shutdown request received` 27 s
after the call; the loop wrote 596 one-second ticks (each with a sync);
the next boot's kernel started 612 s after the request; `az` returned
after 647 s. **Azure waits about 10 minutes for a graceful shutdown, then
powers the VM off.** A freeze has that long on a restart Azure asks for.

| test | result |
|---|---|
| 1. `hostd freeze`, `hostd thaw`, first build | small: frozen 22.4 s (snapshot 0.25 s; 15 s was the stop path's wait for a paused hypervisor to exit), restored 6.1 s, every probe intact, same TCP connection. large: `vm.snapshot` outlived `ch.HTTP`'s 10 s client timeout; the resume was refused while the hypervisor was still writing and **the guest was left paused**; after a manual resume its vsock never answered again and it was cold-started. At the next thaw the half-written image was discarded ("image incomplete"), nothing left. Fixed (commit 68361da4): no client timeout on the snapshot (10 min of its own), resume retried until `vm.info` says Running, a paused hypervisor killed after 3 s. |
| 2. `hostd freeze`, `hostd thaw`, fixed build | both frozen: small 0.76 GB, large 4.10 GB (snapshot 35.7 s), all done in 41.8 s; restored: small 11.0 s, large 36.0 s. Writer no gap, store reader equal every pass, the same TCP connection (no reconnect), the tmux window's process the same pid with its counter carrying on. |
| 3. graceful `systemctl reboot` (through `systemd-run`) | "freezing guests" 0.43 s after the reboot request, both frozen (reason `shutdown`) in 43.3 s, next kernel 51.9 s after the request; hostd restored small in 12.9 s and large in 38.9 s after it started (the large waited for the test key, re-delivered by hand at boot, see below); Hello went out after the restores, so hostdev saw both `running` throughout. Writer no gap, store reader intact, tmux processes alive with their counters continuing, TCP reset and reconnected (its peer on the host died with the reboot). |
| 4. `az vm restart` (Azure's shutdown request) | `hv_utils` shutdown request, freeze started 0.42 s later, 2/2 frozen in 42.3 s, next kernel 54.5 s after the request, `az` returned after 103 s; both restored (12.8 s, 39.1 s), tmux pids unchanged (1213, 1116), counters continuing, writer no gap, store intact, TCP reconnected. |
| 5. 10 `hostd freeze`/`thaw` cycles back to back | 20 of 20 guest-cycles frozen and restored; per cycle the large guest froze in 36.6 to 37.3 s and restored in 37.8 to 38.7 s, the small in 12.3 to 13.4 s (restore); guest clock within 1 s of the host's after every restore (19 at 0 s, 1 at 1 s); no image volume or dm mapping left after any cycle; host available memory flat (58.3 GB); the TCP connection never dropped; store reader never wrong; writer 9734 and 16950 lines with no gap. |

- **The disk is the time.** Freeze and restore ran at 110 to 135 MB/s,
  the staging disk's 125 MB/s, both directions, shared between the
  guests. host-01's disk is 600 MB/s: the same two guests would take
  about 9 s to freeze and 8 s to restore. Snapshot time alone (the
  guest paused, before the hypervisor exits) is what the large guest
  felt: 36 s here, about 7 s on host-01.
- **A key file on `/run` does not survive the reboot it is meant for.**
  The first reboot discarded both images ("image key unavailable",
  guests started stopped and cold) because the test key lived on a tmpfs
  and was not back within the 90 s unwrap window; the next two reboots
  had it pushed at boot and restored. This is why production uses Key
  Vault (I-653); `--freeze-key-file` is for tests that deliver the key at
  boot.
- **hostd answers nothing until the thaw is done.** The control socket
  and the stream start after Reconcile, so `hostd guests` and Hello wait
  for the slowest restore (39 s here). That is the intended order (the api
  must not see a frozen guest as stopped); it also means a stuck restore
  holds hostd for up to the 90 s key wait plus the 180 s restore bound.
- Not tested on staging: Scheduled Events (turning the listener on turns
  the events on for the VM for 24 h, during which every staging restart
  waits for approval; left for a slot where that is wanted), Key Vault
  (no vault for staging yet), more guests than two.

## hostd's own code against a real hypervisor (woker-1)

`freeze-bench.sh hfreeze` and `hthaw` drive a bench guest through the
packages hostd uses: `freeze.Image` on a thin volume of a loop-backed VG
(`vg-gftest`), dm-crypt plain, ext4, `FileKey` RSA wrapping, the record
store, `Fingerprint`, `ch.Freezer` and `ch.RestoreArgs`. 25 cycles of a
large guest with a balloon and the probes
([guest-freeze-data/hostd-path-25.txt](guest-freeze-data/hostd-path-25.txt)):
freeze 3.2 to 3.4 s (volume made in about 0.2 s, snapshot 0.4 s, 1.0 GB
image), restore 1.15 to 1.5 s; the writer's 953 lines with no gap; the
store reader never wrong; one TCP connection throughout; after each cycle
no image volume, no dm mapping and no record left, and the pool back to
0.00 percent (the thin volume's blocks discarded on removal).
