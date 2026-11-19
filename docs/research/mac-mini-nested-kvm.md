# A repose host inside a Mac mini: nested KVM on a rented M4 (research, 2026-11-19)

**Status: research and an untested automation. No decision here is made;
the numbers this doc asks for do not exist yet.** The owner wants bring
your own hardware for launch (I-647 chose shape A), and the hardware
users own is a Mac mini, which has no x86 and no `/dev/kvm`. The path
that keeps macOS on the user's machine is a sealed aarch64 NixOS VM on
Apple's Virtualization framework with hostd inside it, and repose guests
as Cloud Hypervisor microVMs under that VM's KVM. That needs nested
virtualization, which Apple added in macOS 15 for M3 and later chips, for
Linux guests only. Nothing about it can be measured on woker-1 (an Azure
AMD VM), so the first step is a rented M4 Mac mini for one day.

Outside facts were read on 2026-11-19 from the sources at the end.
Facts marked *unverified* come from one secondary page or from a tool's
source code and should be checked on the machine.

## 1. What to rent

| Provider | M4 16 GB | Billing | Fit |
|---|---|---|---|
| Scaleway `M4-S` (fr-par-1) | €0.22 an hour, about €149 a month | hourly, 24 h minimum | the lab day: one API call, full macOS, SSH from the start |
| Scaleway `M4-M` (32 GB, 1 TB) | €0.29 an hour, about €199 a month | hourly, 24 h minimum | the staging Mac later: room for the VM plus several guests |
| AWS EC2 `mac-m4.metal` (24 GB) | $1.23 an hour, 24 h minimum | hourly | five times Scaleway's price for the same day |
| OakHost, HostMyApple, MacStadium | €115 to $149 a month | monthly | a second month-long box if Scaleway's zone runs dry |

Scaleway is the one with an API, a CLI in nixpkgs (`scaleway-cli`
2.62.0) and a Terraform provider, so it is the one the automation
targets. **On 2026-11-19 every macOS Mac mini there was out of stock**
(M1-M, M2-M, M2-L, M4-S, M4-M, M4-XL), and a new account must pass
identity verification before it has any Apple silicon quota at all. The
server-type API carries `stock` (`no_stock`, `low_stock`, `high_stock`),
so `ops/dev/macmini watch` polls it and rents the first M4 that appears.
The one machine in stock was `M2-L-ASAHI` (M2 Pro, Asahi Linux, €0.21 an
hour, no minimum lease). The M2 and M2 Pro machines are useless here: nested
virtualization wants an M3 or later. Asahi Linux on the
M2 Pro is native Linux with native KVM: it runs steps 3, 5, 6 and 8 of
the protocol (a Cloud Hypervisor guest, freeze and restore, virtiofs,
the aarch64 base build) today, as an aarch64 box without the nesting.
It cannot answer steps 2, 4 and 7, which are the Mac path's go or
no-go, because the chip predates nested virtualization and there is no
macOS on it. The day on an M4 still has to happen; AWS `mac-m4.metal`
at about $30 for its 24 h minimum is the fallback if Scaleway's M4
stock stays empty.

## 2. Scaleway facts the automation relies on

- **API.** `https://api.scaleway.com/apple-silicon/v1alpha1/zones/{zone}/servers`,
  header `X-Auth-Token: $SCW_SECRET_KEY`. Create takes `name`, `type`,
  `project_id`, optional `os_id`, `commitment_type` (`none`,
  `duration_24h`, `renewed_monthly`), `enable_vpc`, `public_bandwidth_bps`,
  `enable_kext`. A server reports `status` (`starting`, `ready`, `error`,
  `rebooting`, `updating`, `locking`, `locked`, `unlocking`,
  `reinstalling`, `busy`), `ip`, `vnc_url`, `vnc_port`, `ssh_username`,
  `sudo_password`, `delivered`, `deletable_at`, `deletion_scheduled`, `os`.
  `DELETE` on the server is the only delete; whether a delete before
  `deletable_at` is refused or scheduled is *unverified* (the console has
  an "auto-delete after 24 h" switch; the SDK exposes no such field).
- **CLI.** `scw apple-silicon server create type=M4-S commitment-type=none
  zone=fr-par-1 -o json`, `server wait`, `server get`, `server delete`,
  `server ssh`, `os list server-type=M4-S`. Auth from `SCW_ACCESS_KEY`,
  `SCW_SECRET_KEY`, `SCW_DEFAULT_PROJECT_ID`, `SCW_DEFAULT_ZONE`, or
  `scw init`. An IAM API key is made at console.scaleway.com/iam/api-keys;
  give it a policy with `AppleSiliconFullAccess` and the IAM right to
  create SSH keys (`IAMManager`), scoped to one project made for this.
- **Login.** macOS user `m1`, `sudo_password` from the API, project SSH
  keys synced onto the box by Scaleway's `scw-agent`. `fail2ban` guards
  SSH and VNC; a lockout is cleared only by a reboot from the console.
  Screen Sharing (VNC) is on by default. No SIP changes, no FileVault,
  no recovery mode: it is MDM-managed and nobody has physical access.
- **OS.** macOS 15 (Sequoia), 26 and 27 are installable on every Mac
  mini (changelogs of 2024-10-09, 2025-09-22, 2026-09-21). Any of them
  is new enough. Choosing a non-default version adds about an hour to
  delivery; the default is the fast path. `os list` names them.
- **Clock.** 24 h minimum lease under Apple's licence, so the day costs
  €5.28 whatever happens. Delete takes about 30 minutes to complete.
  Reinstall wipes the disk and resets credentials.
- **Preinstalled.** Xcode, MacPorts, `fail2ban`, `scw-agent`. No
  Homebrew; the lab installs nothing system-wide and needs no `sudo`
  except `pmset` later.

## 3. The stack on the Mac, and why these tools

```
Mac mini (macOS 15+, M4)                          L0
└─ one Linux VM on Virtualization.framework       L1  "the appliance": NixOS aarch64, hostd, KVM
   └─ Cloud Hypervisor microVMs                   L2  repose guests, one per project
```

- **L1 launcher, lab day: Tart** (`tart run --nested --no-graphics`).
  Ready Ubuntu arm64 images (`ghcr.io/cirruslabs/ubuntu`, user `admin`,
  password `admin`), `tart ip`, `--dir` shares, `--disk` attachments,
  `--serial`. Linux VMs boot by EFI from `~/.tart/vms/<name>/disk.img`
  (raw) with `nvram.bin` beside it, so a NixOS `raw-efi` image dropped in
  place of `disk.img` is the next step. Tart runs headless over SSH; CI
  farms run it that way.
- **L1 launcher, product: not Tart.** Tart is now `openai/tart` under
  the Functional Source License (FSL-1.1-ALv2): free on personal
  computers, a paid licence for organisations past a count of
  installations, and a competing-use clause. Shipping it inside a repose
  launcher to every user is the case the licence is written for. vfkit
  (crc-org, Apache-2.0, v0.6.4) drives the same framework from one Go
  binary and takes `--nested` on macOS 15 with M3 or later. The lab day
  boots the same disk with vfkit too, so the measurement is on the
  product's path and not only on Tart's. The alternative is our own
  Swift launcher on the framework, which is small but is a signed,
  notarised macOS program to keep building.
- **L2: Cloud Hypervisor**, as on x86. It supports aarch64 with KVM,
  boots an uncompressed arm64 `Image` directly (the serial console is
  `ttyAMA0`, not `ttyS0`; the runner and hostd pick the console by
  architecture since I-1374), and has snapshot and restore on aarch64, which
  is less travelled than on x86 and is what I-652's freeze depends on.
- **The Lima issue that is not ours.** lima-vm/lima#4498 reports QEMU
  failing with "host kernel KVM does not support providing Virtualization
  extensions to the guest CPU" inside a nested VM on an M4. That is a
  request for a third level (EL2 exposed to the L2 guest). We need two:
  KVM in L1 running plain guests in L2. `kvm-ok` passing in L1 is the
  thing that report confirms works.

## 4. The measurement protocol (one day, in order)

Each step has a pass line. The day stops at the first failure, which is
the finding.

1. **The chip and the OS.** `sw_vers -productVersion` is 15 or later;
   `sysctl -n machdep.cpu.brand_string` says M4; `sysctl -n
   kern.hv_support` is 1.
2. **Nested is offered.** Tart starts the Ubuntu VM with `--nested` and
   does not warn; inside it `/dev/kvm` exists and `kvm-ok` passes. This
   is the go or no-go for the whole Mac path. Record `nproc` and
   `free -g` in L1 at the sizes given (8 vCPU, 12 GiB of the Mac's 16).
3. **A guest boots in L2.** Cloud Hypervisor (`cloud-hypervisor-static-aarch64`
   from its release) boots an Ubuntu arm64 cloud image with the L1
   kernel's `Image`, 2 vCPU, 2 GiB, serial on `ttyAMA0`. Record the time
   from exec to the `login:` prompt, five runs. x86 reference: the boot
   figures in `docs/research/boot-speed.md`.
4. **The nesting tax.** `openssl speed -multi N sha256` and
   `openssl speed -multi N rsa2048` in L1 and in L2 at equal thread
   counts; the ratio is the L2 penalty. The same in L1 against the
   macOS host's `openssl speed` gives L1's own cost. RESEARCH.md §3
   measured about 10 percent for Intel nested on Azure and 50 to 90 for
   AMD; the product needs the Mac number to be nearer the first.
5. **Freeze and restore in L2.** `ch-remote pause`, `snapshot
   file:///...`, kill, `cloud-hypervisor --api-socket` and `ch-remote
   restore`. Pass is a guest that answers after restore. Record the
   snapshot's size and the restore time against I-685's figures.
6. **virtiofs in L2.** `virtiofsd` (aarch64 build) serving a directory
   into the guest, mounted and read. The store share and the login share
   both ride on it.
7. **vfkit on the same disk.** Boot the Tart VM's `disk.img` with vfkit
   `--nested` and repeat step 2. Pass means the product launcher can be
   Apache-licensed.
8. **Build the aarch64 base, if time allows.** Install Nix in L1, clone
   the repository, and attempt the guest closure for `aarch64-linux`.
   The flake used to fix `system = "x86_64-linux"`; since I-1374 it has
   `aarch64-linux` outputs (§8), so the step is `ops/dev/aarch64-base
   check` and the closure's size against the 7 GiB limit is the number
   to carry back.

What the day cannot answer: how a user's own Mac mini behaves with the
Mac asleep, on Wi-Fi, behind a home NAT, or on a 16 GB machine that is
also someone's desktop. Those come from the first trusted user's box.

## 5. Automation

**2026-11-19, later the same day: the lab moved to AWS.** Scaleway's M4
shelf was empty and two other Mac hosts were too, so the day runs on
EC2 `mac-m4.metal` in us-east-1 (2024 Mac mini, M4, 24 GB, macOS on the
metal, about $1.23 an hour with the same 24 h minimum, billed per
Dedicated Host from the allocation call). The driver's default provider
is now `aws`; `MACMINI_PROVIDER=scaleway` keeps the Scaleway path. New
accounts have a quota of 0 for "Running Dedicated mac-m4 Hosts"
(L-2CBA8B92), and the console's quota page lists the x86 "m4" family
first, which is how the first request on this account went to the wrong
quota. `ops/dev/macmini watch` polls the right one and allocates when it
reaches 1. The macOS AMI comes from the SSM parameter
`/aws/service/ec2-macos/<name>/arm64_mac/latest/image_id` (`sequoia`,
`tahoe`, `golden_gate` for 15, 26, 27); the login is `ec2-user` with
passwordless sudo; a stop or terminate puts the host through a scrubbing
workflow before it can be released. AWS's EC2 Mac page says nothing
about nested virtualization either way, so step 2 is still the go or
no-go. Budget: `repose-lab-monthly`, $50 a month, emails at 50, 80 and a
forecast of 100 percent; Budgets evaluate once a day, so it is an alarm,
not a kill switch.

`ops/dev/macmini` (this branch) drives the provider from woker-1,
`ops/dev/macmini-bootstrap.sh` runs on the Mac, and
`ops/dev/macmini-measure-linux.sh` is the Linux half (Cloud Hypervisor
boots and the benchmark), which runs inside L1 on a Mac or natively on
an Asahi box: `bootstrap` looks at `uname` and picks. Both are **untested**
until the owner's API key exists; what they do is exactly section 4,
steps 1 to 4 and 7, with the results in `~/repose-lab/results/` on the
Mac and copied back by `ops/dev/macmini results`. Steps 5, 6 and 8 are
hand steps on the first day and go into the script once seen to work.

```
nix develop ./nix#infra
export SCW_ACCESS_KEY=... SCW_SECRET_KEY=... SCW_DEFAULT_PROJECT_ID=...
ops/dev/macmini watch             # polls M4-S then M4-M stock every 5 min and creates on the first hit
ops/dev/macmini create            # M4-S, fr-par-1, no commitment; uploads your SSH key
ops/dev/macmini wait              # until ready and sshd answers
ops/dev/macmini bootstrap         # Tart, vfkit, the Ubuntu VM with --nested, steps 1-4 and 7
ops/dev/macmini ssh               # m1@<ip>
ops/dev/macmini results           # copies ~/repose-lab/results/ to docs/research/mac-mini-nested-kvm-data/
ops/dev/macmini destroy           # refuses before deletable_at and prints when
```

Why a script and not `infra/`: the lab box lives a day and holds no
state anyone else needs. The staging Mac that follows a go is a tofu
root (`scaleway_apple_silicon_server`, provider `scaleway/scaleway`)
beside `infra/azure`, which `infra/README.md` already anticipates for a
second provider. The API key is an operator secret in the shell on
woker-1, the way `CLOUDFLARE_API_TOKEN` is; it never enters this
repository or an agent's output.

## 6. What a go turns into

The work items in `proposals/2026-10-10-bring-your-own-host.md` stay,
with the `metal` provider replaced by:

- an `aarch64-linux` guest base and host system as second flake outputs,
  built on the Mac's L1 as a Nix remote builder (woker-1 is x86 and has
  no `binfmt` to emulate): done as I-1374, §8;
- a host image for the framework (`raw-efi`, one disk, a partition for
  `vg-guests`, a host reserve scaled for 16 GB): done as I-1376,
  `nix/hosts/mac.nix` and `packages.aarch64-linux.mac-appliance-image`;
- a launcher for macOS on vfkit or our own framework code, started at
  login, holding the join token for the first registration, with
  `pmset` keeping the Mac awake: done as I-1380, `apps/web/static/mac`
  (a plist over vfkit; `caffeinate`, not `pmset`, so no setting of the
  Mac's changes), and the join carries the host's place on the hub too,
  which is how a Mac registers with no route to the api's VNet;
- the runner's `console=ttyS0` made per-architecture: done as I-1374;
- freeze and restore gated on step 5's result, x86-only if it fails:
  step 5 was not run on 2026-11-19 (§7), so the park is off in
  `mac.nix` and asserted off until it is.

## 8. The aarch64 build (I-1374, 2026-11-19)

**Nothing aarch64 has been built. Every number a build would give is
below as a placeholder, and every aarch64 output in the flake has only
ever been evaluated, never realised.** A flake output nobody has built
is the same class of thing as a migration written and never registered;
this section exists so that the reader does not take the evaluation
evidence for a build.

What exists, from `nix/flake.nix`: `packages.aarch64-linux` (the guest
runner and system, the Go binaries, the agents), `guestSystems.aarch64-linux`
(what hostd on an aarch64 host evaluates), `nixosConfigurations.host-aarch64`
and `installer-aarch64`, and `checks.aarch64-linux` (the closure limit,
the runner, the host and the installer build; no VM tests, they need KVM
of the builder's architecture). The runner boots the kernel's `Image`
with `console=ttyAMA0`; hostd renders the same console on arm64. Every
agent's arm64 release is pinned beside the x86 one in `versions.json`.
The CI job `nix-aarch64-overlay` and `ops/dev/aarch64-base` are written
and have never run.

Measured on woker-1 (x86, evaluation only, no aarch64 builder; every
number below is from `nix eval` under the shared build lock):

- The aarch64 guest system, host system, installer and closure check all
  evaluate to derivations; the x86 derivations are unchanged by the
  restructuring (the guest system's, runner's, host-02's, edge's and
  installer's `.drv` paths are identical to `mac-mini`'s).
- The host kernel pin resolves to 6.18.57 on aarch64 too (the same
  nixpkgs revision), so `nix/hosts/kernel.nix`'s CVE-2026-99999
  assertion holds.
- `nix build --dry-run ./nix#packages.aarch64-linux.guest-system` on
  2026-11-19: 979 paths fetched from cache.nixos.org (1.9 GiB download,
  5.7 GiB unpacked) and 421 derivations built. The built ones are the
  guest's own configuration (unit files, etc files, the initrd and the
  shrunk module set), the Go binaries, the agents (prebuilt binaries
  patched on the builder) and the playwright set with its chromium
  override; the kernel, chromium and node itself are fetched. One
  `nodejs-24.20.0` derivation is in the build list beside the fetched
  one, so the day may include one node compile: budget an hour for it
  on 8 vCPU before reading the closure number.

Owed by the first aarch64 build day (the Mac's appliance VM, or any
aarch64 Linux box with nix), each line replaced by its number:

- aarch64 guest closure size: **NOT BUILT**, from
  `ops/dev/aarch64-base check` (the x86 closure is about 6.0 GiB
  against the 7 GiB limit).
- guest system build time, wall clock on the builder: **NOT BUILT**.
- host system build time (`host-aarch64`): **NOT BUILT**.
- paths substituted against built, and whether the node compile
  happened: **NOT BUILT**.
- builder memory: **NOT BUILT** (asked for 16 GiB, 24 comfortable, with
  about 15 GB free in `/nix`, before the first attempt).
- the aarch64 overlay on Cachix: **NOT PUSHED** (the CI job runs on the
  first push of this to `main`; the recipe's `push` has never run).

`ops/dev/aarch64-base` is the recipe for the day (`check`, `build`,
`push`), and `docs/ops/DEV-BOX.md` "aarch64 builds" says where it runs.

## 7. What the Mac said (AWS mac-m4.metal, eu-west-1a, 2026-11-19)

Host: EC2 Dedicated Host h-0b0c0ac94c35a8e93, instance i-03d94447e3932adfe,
macOS 15.8.2 (24H232), Apple M4, 10 cores, 24 GB, `kern.hv_support` 1.
Allocated 06:02 UTC after Virginia reported no capacity in any zone;
Ireland had capacity on the first try once its quota applied.

| Step | Result | Note |
|---|---|---|
| 1 chip and OS | PASS | macOS 15.8.2 on Apple M4 |
| 2 nested KVM | PASS | `/dev/kvm` in the appliance VM (Tart `--nested`, Ubuntu 24.04 arm64, 8 vCPU, 12 GiB), `kvm-ok` passes; KVM reports "Hyp mode initialized", GICv3 system-register interface, no GICv2 |
| 3 guest boots | PASS on 6.18.57 and 6.8; FAIL on 7.0 | a stock Ubuntu cloud image: `Run /init` 1.5 s, systemd 4.0 s, login prompt 134 to 139 s (the image waits 120 s for a network the guest has no NIC for; the kernel-to-systemd time is the figure) |
| 4 nesting tax | PASS, none measurable | one-thread `openssl speed sha256`, 16 KiB blocks: guest 3.12 GB/s, appliance 2.92 GB/s (ratio 1.07, inside core-to-core noise on a chip with P and E cores); the appliance at 8 threads 18.7 GB/s, 6.4x one thread; the Mac itself under LibreSSL 3.23 GB/s at 8 KiB, not like for like |
| 5 freeze and restore | not run | |
| 6 virtiofs | not run | |
| 7 vfkit | PASS | vfkit 0.6.4 `--nested` booted the Tart disk to a login prompt on the first run; the address lookup was the harness's fault |

Raw files: `mac-mini-nested-kvm-data/` (host facts, kvm-ok, the guest
console logs under 6.8 and 6.18.57, the hang logs under 7.0, the openssl
outputs at all three levels). The appliance VM was then resized to 18
GiB and given nix 2.35.6 as the aarch64 builder for workstream 19e.

**The kernel condition (the finding of the day).** With Ubuntu's
`7.0.0-43-generic` kernel in the appliance VM, every guest hangs about
two seconds into boot, right after the initramfs hands over: the guest
kernel prints `rcu_preempt kthread timer wakeup didn't happen ...
Possible timer handling issue on cpu=0`, and never reaches a login
prompt. It is the same under Cloud Hypervisor v54.1 and under QEMU
8.2.2 with KVM (`-M virt -accel kvm -cpu host`), with one vCPU or two,
with `idle=poll`, and with `nohz=off highres=off`. With
`6.8.0-147-generic` in the same VM, on the same macOS and the same
host, the same guest boots to a login prompt, and so does it with
**`6.18.57`** (Ubuntu's mainline build of the exact version
`nix/hosts/kernel.nix` asserts: `versionAtLeast 6.18.57`, the first
stable kernel with the fix for CVE-2026-99999, a guest-to-host escape
through a tap's `vnet_hdr`; DECISIONS I-778). So the appliance ships
6.18.57 or a later 6.18 point release, **never 6.8**, and the pin is a
product requirement rather than a detail: the `mac` provider's kernel
(I-1374, I-1376) must be proven by `ops/dev/macmini-l2-try.sh` on
Apple silicon before any bump, and the bisect window for whoever bumps
next is 6.18.57 to 7.0.0-43.
Whether this is a KVM regression between 6.8 and 7.0 in nVHE mode
under Apple's hypervisor, or an Apple-side bug that newer kernels
tickle, is *unverified*; the logs are in `mac-mini-nested-kvm-data/`
(`l2-try-1.log`, `l2-try-2.log`, `l2-qemu.log` for the hang,
`l2-boot-*.log` for the boots under 6.8). macOS 15 is enough: the
requirement does not move to 26.

**Harness lessons** (each cost a run): `nohup` under `ssh -t` on macOS
does not detach and the VM dies with the session, so the driver runs
the bootstrap without a tty; `/dev/kvm` is root:kvm 0660 in the Ubuntu
image; Cloud Hypervisor v54 wants `image_type=raw` on every disk;
Ubuntu's arm64 `vmlinuz` is an EFI zboot PE with the Image compressed
inside (zstd here), which the measure script now unwraps; a login typed
at the first prompt of a cloud image fails because cloud-init has not
set the password yet; `pkill -f` on a name that appears in your own
command line kills your own shell.

## 9. The appliance day (19f, 2026-11-19)

The production image (`packages.aarch64-linux.mac-appliance-image`,
DECISIONS I-1376) was built on the appliance VM L1 as the builder:
`nixosConfigurations.mac` in 3 min 50 s from 19e's cache, closure 1.78
GB; the image in 12 min 45 s (disko's image builder in a KVM VM, 37 GiB
sparse raw inside), 2,081,798,905 bytes as `repose-appliance.raw.gz`.
It boots on the Mac under vfkit 0.6.4 (`--nested`, EFI, virtio-blk,
NAT, virtio-fs, the console to a file) to the login prompt on the
6.18.57 pin, with the join, net, pool, grow and register units running.

**A repose guest on the appliance** (a lab image with hostdev through a
relay on the Mac, the aarch64 guest closure baked in; the VM at 12 GiB,
8 vCPU): `hostdev create --class large` then `start`; inside the guest
`findmnt -t virtiofs` lists `/nix/.ro-store ro-store` and
`/run/repose/claude-auth claude-auth`, `/nix/store` is the overlay over
the share, `ls /nix/store` of 1225 paths takes 0.18 s, Docker 29.8.0
runs on overlay2, the tmux session unit is up, ssh with a certificate
from hostdev's CA logs in. Gate 0 step 6 (virtiofs) therefore passes.
Timing of the first boot (`systemd-analyze`): kernel 13.4 s, userspace
3 min 23 s; guestd listening at 56 s, "guest is ready" at 70 s; the
critical chain is `repose-paths` waiting 1 min 41 s for hostd's
RegisterPaths (I-232's dump-db of the closure, slow on the appliance),
then home-manager 26 s. hostd's 60 s ready window closed on it twice,
reproducibly: `--ready-timeout` and `guestReadyTimeout = "5m"` in
mac.nix are the fix (I-1376). A stop takes 18 s. Failing units in the
aarch64 guest, not fixed here: `repose-zram-swap` (`zramctl: /dev/zram0:
failed to wait for initialized: No such file or directory`) and Docker's
first attempt (`timeout waiting for containerd to start`, then it
starts). Once in three starts SetupProject failed at `start the tmux
session unit: systemctl exited 1`; unverified.

Hello from the 12 GiB VM: `mem_bytes` 12,492,406,784, `vcpus` 8, `cores`
8; `free_mem_bytes` 10,344,923,136 before a guest and 1,218,117,632 with
the large guest placed; `pool_bytes` 11,790,188,544 on a 46 GiB disk
file (the image's 4 GiB PV grown at first boot).

Disk on the Mac (the sparse file's real blocks): the production image
unpacks in 21 s to 4.4 GB (apparent 37 GiB); a lab image with the 6.6
GiB guest closure held 12.7 GB after boot and 9.7 GB after `fstrim -av`
inside the appliance, which matches root 7.97 GB + /boot 0.1 + the thin
pool's 1.37 GB allocated (the caches volume's ext4 1.08, the guest
volume 0.28, metadata 1 GiB), so the 3 GB gap was stale blocks from the
image build, given back by discard through virtio-blk to APFS. With the
large guest running: 9.76 GB. vfkit's own RSS: 24 MiB.

Lessons, each of which cost a run:

- **Apple's NAT isolates VMs from each other at layer 2.** Two VMs on
  vfkit's or Tart's NAT (bridge100, 192.168.64.0/24) each reach the Mac
  and the internet, and the Mac reaches both, but ARP between them
  fails. A two-VM lab (a hostdev in one, the appliance in the other)
  needs a relay on the Mac: `ssh -N -L 192.168.64.1:8443:127.0.0.1:8443
  admin@<builder>` and an image that dials the Mac's address. The
  product is not affected; a real appliance dials the api over the
  internet.
- **BSD `dd ... count=0 seek=N` does not extend a file.** The installer
  uses perl's `truncate`, which macOS ships.
- **`gunzip` writes the zeros.** A 37 GiB image whose data is a few GiB
  costs 37 GiB on the volume unless the unpack seeks over zero blocks;
  the installer does, in perl.
- **launchd takes no agent from an ssh session with no login.**
  `launchctl bootstrap user/<uid>` answers "Bad request" for a
  LaunchAgent; the GUI domain does not exist. The installer starts the
  VM detached from a subshell with every descriptor off the terminal
  (`nohup` under a tty says "can't detach from console" and the VM dies
  with the session). On a Mac with a login session the agent is what a
  user gets (I-1380).
- **A reboot of the Ubuntu builder VM resets `/dev/kvm` to 0660**, and
  nix's build users are not in `kvm`, so an image build after the
  reboot fell back to TCG without saying so loudly (`qemu-system-aarch64:
  Could not access KVM kernel module: Permission denied`, then `falling
  back to tcg`). `chmod 666 /dev/kvm` before any build there.
- **`pkill -f` with the pattern spelled in the same ssh command kills
  the ssh session.** `pkill -f 'lab-image[2]'` matches the process and
  not the shell that carries the pattern.

## Sources

- Scaleway Apple silicon docs (docs-content repository, `pages/apple-silicon/*`:
  create, connect by SSH, FAQ, delete, reinstall, datasheet, concepts;
  changelogs 2024-10-09, 2025-09-22, 2026-02-23, 2026-09-21).
- Scaleway CLI `docs/commands/apple-silicon.md`; Go SDK
  `api/applesilicon/v1alpha1/applesilicon_sdk.go`; Terraform provider
  `docs/resources/apple_silicon_server.md`.
- Scaleway pricing page for Apple silicon (M4-S €0.22/h, M4-M €0.29/h, 2026-11-19).
- Tart `Sources/tart/Commands/Run.swift`, `Create.swift`, `VMDirectory.swift`,
  `LICENSE` (FSL-1.1-ALv2, copyright OpenAI); Scaleway tutorial
  "Run and manage Linux VMs on Apple silicon with Tart".
- vfkit (crc-org) release v0.6.4, Apache-2.0; kubernetes/minikube#22805
  on `--nested` in vfkit and krunkit on macOS 15 with M3, M4, M5.
- Parallels forum and UTM issue #6700 on nested virtualization in
  macOS 15 for M3 and later, Linux guests only; lima-vm/lima#4498.
- macyou.co, macrent.cloud and Vantage for the other providers' prices (2026-11).
