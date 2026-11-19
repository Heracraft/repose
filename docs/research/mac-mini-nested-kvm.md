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
  `ttyAMA0`, not `ttyS0`; our runner hard-codes `console=ttyS0` in
  `nix/guest/microvm.nix`), and has snapshot and restore on aarch64, which
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
   The flake fixes `system = "x86_64-linux"` (`nix/flake.nix:63`), so
   this is where the first code change shows itself; the closure's size
   against the 7 GiB limit is the number to carry back.

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
  no `binfmt` to emulate);
- a host image for the framework (`raw-efi`, one disk, a partition for
  `vg-guests`, a host reserve scaled for 16 GB);
- a launcher for macOS on vfkit or our own framework code, started at
  login, holding the join token for the first registration, with
  `pmset` keeping the Mac awake;
- the runner's `console=ttyS0` made per-architecture;
- freeze and restore gated on step 5's result, x86-only if it fails.

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
| 4 nesting tax | pending | |
| 5 freeze and restore | not run | |
| 6 virtiofs | not run | |
| 7 vfkit | PASS (boot) | vfkit 0.6.4 `--nested` booted the Tart disk to a login prompt on the first run; the address lookup was the harness's fault |

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
