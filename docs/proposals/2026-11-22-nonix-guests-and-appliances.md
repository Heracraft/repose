# Proposal: No-Nix Guests and Own-Hardware Appliances

**Status:** Research, architecture map, parity matrix, and workstream split (PR 1, `nonix-research`). Zero code changes, zero decision IDs reserved, zero AWS spend.  
**Date:** 2026-11-22  
**Program:** Remove Nix from microVM guests and own-hardware appliances while keeping NixOS exclusively on hosted servers (`nix/hosts/`, e.g. `host-02`) and the edge gateway (`nix/edge/`).

---

## 1. Problem Statement and Owner-Approved Direction

NixOS works well on our hosted servers (`host-02`) and edge gateway (`edge-01`), where declarative `nftables`, `systemd` sandboxing, WireGuard, and atomic `nixos-rebuild switch` rollbacks are high leverage and run on hardware we control.

On **guests** and **own-hardware appliances** (Mac mini `arm64` and generic x86 bare-metal), Nix has become the primary source of user-visible latency, reliability failures, and maintenance drag:

- **Mac mini guest boot latency (measured):** 289 s over `virtiofs` (`docs/research/mac-mini-nested-kvm.md` §10), dropping to **57.9 s** even after moving `/nix/store` to an `erofs` disk — still dominated by `RegisterPaths` (`nix-store -qR` + `--dump-db` + `--load-db`: 16.2 s to 125.5 s, spanning 43.0–172.5 s across published Mac builds, crossing the 180 s deadline and causing 7 of 9 start failures in ENG-80) and `home-manager` activation (10.8–98 s).
- **x86 hosted guest boot latency (measured):** ~11–12 s on a quiet `host-02` (`repose.run` published baseline; 26.4–40.0 s under load on `host-01`, `docs/research/boot-speed.md`), with `RegisterPaths` (4.2–19 s), `home-manager` (5.4–11.2 s), and `virtiofsd` store-view population on every boot.
- **Appliance size & iteration speed (measured):** The NixOS `arm64` Mac appliance (`repose-appliance-aarch64.raw.gz`) is **2,081,798,905 bytes (2.08 GB)** compressed (`37 GiB` apparent, `5.65–9.76 GB` on APFS), takes **12 min 45 s** to rebuild inside a nested KVM builder (`ENG-128`), and requires a 32 GiB root partition just to hold `/nix/store` and build scratch space (`nix/hosts/mac.nix:203`).
- **Evaluation memory & CI fragility (measured):** `nix flake check --no-build ./nix` consumes **8.47–12 GiB** RAM and OOM-kills 16 GiB CI runners and dev boxes (`ENG-144`, `docs/workstreams/CONDUCTING.md`).
- **Guest store fragility:** `virtiofsd` exhausts `LimitNOFILE=1048576` during in-guest Nix GC (`ENG-135`); whiteouts in `/nix/.rw-store` hide shared store paths (`I-587`); `repose-paths.service` vs `--paths-timeout` deadline races silently activate `home-manager` against an unregistered store (`ENG-124`).

### Owner-Approved Architectural Direction

1. **Keep NixOS ONLY for hosted servers (`host-02`) and edge (`nix/hosts/`, `nix/edge/`).** Preserve declarative `nftables`, `systemd` sandboxing, WireGuard, and `nixos-rebuild switch` on our managed fleet.
2. **Guests remain 100% real Cloud Hypervisor KVM microVMs** — never Docker containers, never weakened hypervisor isolation.
3. **Build-time `Dockerfile` -> `erofs` + LVM thin `overlayfs`:**
   - Standard minimal Ubuntu 24.04 LTS (or Debian 12/13) rootfs built via `docker buildx` for `linux/amd64` and `linux/arm64`, flattened into a read-only **`erofs` disk image** (`vda`, `-zlz4hc`) paired with a matching Linux kernel and minimal `initramfs`.
   - Each project keeps its writable LVM thin volume (`vdb`, `ext4`), mounted in `initramfs` as the `overlayfs` `upperdir`/`workdir` over `vda` before `switch_root` to `systemd` — with `/home/dev` and `/var/lib/docker` bind-mounted directly from `vdb` `ext4` to bypass `overlayfs` completely.
   - Preserves instant LVM thin snapshots, restore, fork (`RestoreClone`), resize (`lvextend` + `resize2fs`), idle pause/wake, and memory park/unpark.
4. **Own-hardware appliances become tiny, non-Nix Linux hosts:**
   - **Mac mini (`arm64`):** ~200–300 MB compressed immutable Debian/Ubuntu `arm64` disk image for `vfkit` (`--nested`), shipping the pinned 6.18.57+ kernel, `cloud-hypervisor`, LVM2, `nftables`, WireGuard, and a standalone `hostd` binary with atomic self-update.
   - **Generic x86 bare-metal:** A standard Linux host installer (`ops/install-host-x86.sh` / `.deb`) for Ubuntu/Debian x86 bare-metal servers (AWS, Hetzner, home boxes), enabling parallel verification on random AWS x86 bare-metal alongside the Mac mini.

---

## 2. Prior-Art Research, Failure Modes, and Counter-Evidence

Per `docs/workstreams/CONDUCTING.md` ("Research the pattern before you design one"), we studied how production microVM platforms build, boot, and persist non-Nix Linux root filesystems, prioritizing real postmortems, kernel documentation, and GitHub issue threads over vendor marketing.

### 2.1 How Production MicroVM Platforms Build and Boot Non-Nix Guests

| Platform | VMM | Rootfs Format | Writable Layer | OCI-to-Disk Pipeline |
|---|---|---|---|---|
| **Fly.io Machines / Sprites** | Firecracker | `ext4` block device (migrated to layered/CoW block volumes + persistent disks) | Direct `ext4` root + optional persistent volume | Docker/OCI image flattened into block rootfs at deploy time; custom `init` mounts volumes and execs entrypoint. |
| **E2B** | Firecracker | Read-only `squashfs` / `erofs` base template | `overlayfs` upperdir on per-sandbox writable block/tmpfs | `Dockerfile` -> `docker export` -> `mkfs.erofs` / `mksquashfs`; shared read-only lower layer across concurrent VMs. |
| **Daytona** | Linux MicroVMs (optional) / Containers | `ext4` virtual disk volumes (3 GB+ default) | Direct `ext4` | OCI image unpacked into per-workspace block volume or VM snapshot. |
| **Modal** | gVisor / MicroVMs | Proprietary content-addressed block/layer fabric | CoW scratch layer | Container layers streamed on demand into sandbox rootfs. |
| **Unikraft Cloud** | Firecracker / QEMU | Native `erofs` rootfs | RAM-backed `overlayfs` upperdir | OCI image -> `mkfs.erofs` -> direct mount with minimal init. |

**Key takeaway:** Every microVM sandbox platform that accepts `Dockerfile`-built Linux environments uses the same two-device pattern: a **read-only compressed block image (`erofs`)** shared across all microVMs on a host (`vda`) + a **per-VM writable block device (`vdb`, `ext4`)** composed via `overlayfs` in a tiny `initramfs` *before* `switch_root` to PID 1 (`systemd`).

### 2.2 EROFS Compression Trade-Offs (`lz4` vs `lz4hc` vs `zstd`)

Unlike SquashFS (128 KiB–1 MiB blocks causing high read amplification on random 4 KiB page faults), EROFS defaults to 4 KiB page-aligned clusters with in-place decompression (`kernel.org/doc/html/latest/filesystems/erofs.html`, USENIX ATC '19).

| Algorithm | Image Size | Decompression Speed | Build (`mkfs.erofs`) Time | Recommendation |
|---|---|---|---|---|
| `-zlz4` | Largest | Memory-bus-bound (~3–4 GB/s/core) | Fast | Best only if storage is free and build time is critical. |
| **`-zlz4hc,9`** | **~15–25% smaller than `lz4`** | **Identical to `lz4` at read time** | Slower (paid once in CI) | **Recommended default.** Zero runtime latency penalty; smaller download for Mac mini appliances and faster page-in on nested NVMe/APFS. |
| `-zzstd` | Smallest (~10–15% smaller than `lz4hc`) | Higher CPU cost per 4 KiB fault | Moderate | Only wins on slow I/O (<200 MB/s). On fast NVMe/APFS, zstd CPU decoding slows parallel cold-boot page faults. |

### 2.3 Real Failure Modes and How Our Design Mitigates Them

The following five failure modes are well-documented in Linux kernel docs, GitHub issues, and embedded/container postmortems. Every one of them would bite repose if ignored:

1. **Docker-in-VM on `overlayfs` (overlay-on-overlay failure)**
   - *Failure signature:* `failed to mount overlay: invalid argument`, `too many levels of symbolic links`, or `overlayfs: filesystem on '...' not supported as upperdir` (Docker/moby & LXC issue threads). Linux OverlayFS cannot reliably serve as `upperdir` for Docker's `overlay2` storage driver. Falling back to `vfs` wastes gigabytes of thin-pool space and destroys build speed.
   - *Mitigation (mandatory):* In the guest `initramfs`, mount `vdb` (`ext4`, formatted with `d_type`/filetype enabled, which is `mkfs.ext4` default) at `/mnt/rw`, create `/mnt/rw/docker`, and **bind-mount `/mnt/rw/docker` directly onto `/sysroot/var/lib/docker`** *after* mounting the root `overlayfs` and *before* `switch_root`. Docker's `overlay2` then runs directly on raw `ext4`, identical to today's NixOS guests.

2. **`systemd` inside `overlayfs` (`/etc/machine-id`, `ConditionFirstBoot`, `journald` crash loops)**
   - *Failure signature:* `systemd-machine-id-commit.service: Failed to unmount transient /etc/machine-id file: Invalid argument` (`systemd/systemd` issues); `systemd-journald` crash-looping with `Failed to get machine id: No such file or directory`; `ConditionFirstBoot=yes` misfiring on every boot when `/etc` is initially read-only or shadowed by a late overlay mount (`bootlin.com`, `freedesktop.org` docs).
   - *Mitigation (mandatory):*
     1. Mount the root `overlayfs` completely inside `initramfs` **before** `switch_root` to `/sbin/init` (`systemd`). Never mount `/etc` overlay from `/etc/fstab` or a late systemd unit.
     2. Ensure `/etc/machine-id` is empty/unpopulated in the base `erofs` image, and have the `initramfs` seed a deterministic persistent `/mnt/rw/upper/etc/machine-id` (generated once on first boot of `vdb`, e.g., from `/proc/sys/kernel/random/uuid` stripped of hyphens) *before* `switch_root`. `systemd` then sees a valid, writable, persistent `/etc/machine-id` from millisecond 0, `journald` never loops, and `ConditionFirstBoot` behaves predictably.

3. **UID/GID drift and copy-up overhead on `/home/dev`**
   - *Failure signature:* If `/home/dev` sits inside `overlayfs`, modifying any file seeded in the lower layer triggers an `overlayfs` copy-up (14.8 ms/inode on thin-pool first write, measured in `docs/research/boot-speed.md` §"Then the copy itself went"). Worse, if a base image rebuild shifts `dev`'s UID/GID, every file on `vdb` becomes owned by the wrong user.
   - *Mitigation (mandatory):*
     1. Pin `dev` to `UID=1000`, `GID=1000` (and `docker` to a fixed GID, e.g. `999`) explicitly in the guest `Dockerfile` (removing Ubuntu 24.04's default `ubuntu` user at UID 1000 first).
     2. **Bind-mount `/mnt/rw/home-dev` directly onto `/sysroot/home/dev`** in `initramfs` (seeding skeleton dotfiles from `/etc/skel` only on first boot when `/mnt/rw/home-dev` is empty). This guarantees zero `overlayfs` copy-up tax on every git checkout, `node_modules`, and agent workspace, and keeps `/home/dev` 100% isolated from base `vda` upgrades.

4. **`apt` / `dpkg` corruption and whiteout conflicts across base `vda` upgrades (THE HARDEST PROBLEM)**
   - *Failure signature:* When a user runs `sudo apt install foo` inside a guest, `dpkg` copies `/var/lib/dpkg/status` up into `vdb`'s `overlay/upper/var/lib/dpkg/status` and writes files + whiteouts (`0:0` char devices) into `vdb`'s `overlay/upper/usr` and `/etc`. When repose later upgrades the base `vda.erofs` underneath the same `vdb`:
     - The old copied-up `/var/lib/dpkg/status` in `upper` **completely shadows** the new base image's `/var/lib/dpkg/status`! `apt` thinks old versions are installed, shared libraries in `upper` clash with upgraded libraries in `lower`, and lingering whiteouts hide newly added base files (`dpkg: error: parsing file '/var/lib/dpkg/status'`, broken SONAMEs, ghost files).
   - *Mitigation (explicit architectural decision — do not hand-wave):*
     We must **never** upgrade `vda.erofs` underneath an arbitrary mutated `overlay/upper/usr` + `overlay/upper/var/lib/dpkg` without a clean boundary. Three industry-proven strategies exist:
     - **Option A (Pin `vda` per project until explicit opt-in reset, OR keep `/var/lib/dpkg` + `/usr` ephemeral across base upgrades):**
       - User-installed tools installed via **`mise` / `uv` / `npm -g` / `cargo` / `go install` / Homebrew-linux** live in `/home/dev` (direct `vdb` bind mount) -> **100% immune** to `vda` upgrades.
       - Platform services & menu packages (`repose config add postgresql redis ...`) are recorded declaratively in the API (`config_revisions.menu`) and re-applied idempotently by `guestd` on first boot after a base upgrade (with database data dirs like `/var/lib/postgresql` and `/var/lib/redis` stored on `/mnt/rw/services/` direct bind mount so data is never lost!).
       - On a **base image upgrade** (new `vda.erofs` version): `initramfs` detects `vdb`'s stamped `/mnt/rw/.base-version != /mnt/ro/etc/repose/base-version`, archives/rotates `overlay/upper` (preserving `/mnt/rw/home-dev`, `/mnt/rw/docker`, `/mnt/rw/services`, and `/mnt/rw/etc-persist` for `/etc/machine-id` and user-added `/etc` overrides), and boots cleanly on the new `vda.erofs`, after which `guestd` re-applies the project's declarative menu/packages.
       - *What if a user ran ad-hoc `sudo apt install ripgrep` directly in the shell?* It persists across **every stop/start, snapshot, restore, and fork** on the same base version. On a base version bump (which only happens when the user unholds/restarts onto a new base), an ad-hoc `apt` package outside `repose config add` is recorded in `/var/lib/repose/apt-manual.list` (via an `apt` `DPkg::Post-Invoke` hook) and can either be auto-reinstalled on base bump or surfaced clearly in `repose status`.
     - **Option B (Never rotate `upper`; pin each project to its creation `vda` forever):** Eliminates base security updates and forces hosts to cache dozens of 1.5 GB `erofs` images forever. **Rejected.**
     - **Option C (Full writable `ext4` clone of rootfs per project, like Fly.io/Daytona):** Eliminates `overlayfs` completely, but bloats every project's thin volume by 3–4 GB on day 1 (breaking our disk-held billing model I-585 and blowing up Mac mini disk budgets and snapshot upload sizes). **Rejected.**
     - **Chosen design: Option A** — direct `vdb` bind mounts for `/home/dev`, `/var/lib/docker`, and `/var/lib/repose/services` (databases), plus `overlayfs` for `/`, with clean `upper` rotation + automatic replay of `config_revisions.menu` (and optional `apt-manual.list`) on base-version transitions.

5. **Kernel / initramfs / `/lib/modules` ABI coupling & Apple M4 nested KVM constraint**
   - *Failure signature:* Booting a kernel whose version does not match `/lib/modules/<uname -r>` inside `vda.erofs` breaks `overlay`, `nf_tables`, `br_netfilter`, `zram`, and Docker networking (`iptables`/`nftables` kernel modules fail to load). Separately, on Apple M4 (`macOS 15.8.2`), host kernel `7.0.0-43` hangs nested Cloud Hypervisor L2 guests (`rcu_preempt kthread timer wakeup didn't happen`, `docs/research/mac-mini-nested-kvm.md` §7), while `6.18.57` passes both nested KVM and CVE-2026-99999 (`I-778`).
   - *Mitigation (mandatory):*
     1. Every guest base release is an **atomic bundle** per architecture: `{vmlinuz/Image, initramfs.img, rootfs.erofs, manifest.json}` built together from the same kernel package so `/lib/modules` in `rootfs.erofs` is byte-for-byte matched to `kernel` and `initramfs`.
     2. The Mac mini `arm64` appliance image pins the **exact proven `6.18.57+` LTS kernel** (verified via `ops/dev/macmini-l2-try.sh` on Apple silicon before any kernel bump).

### 2.4 Counter-Evidence Against Removing Nix (Arguing Against Ourselves)

Per `docs/workstreams/CONDUCTING.md`, we must state plainly what this architecture **gives up** compared to NixOS guests:

1. **Loss of bit-for-bit reproducible arbitrary user `fragment.nix` & `machine.nix`:** Users who already write custom Home Manager modules (`repose.nix`, `machine.nix`, `flake.nix` `devShell` auto-loading) lose declarative Nix composition on the host. While `direnv` + `mise`/`apt`/shell setup scripts cover 95% of developers better (and without 2–30 minute host builds), power users who specifically wanted NixOS-as-a-service lose that differentiator unless they install Nix *inside* their own guest volume (`vdb`) themselves.
2. **Base updates require a guest restart (no live `switch-to-configuration`):** With NixOS, non-kernel base bumps switched running guests in place without restarting `tmux` or active agents (`ApplyConfig`). With `erofs` (`vda` read-only block device), swapping `vda` requires a stop/start (or waiting until the next `repose stop && repose start`). Running agents are never interrupted unexpectedly, which is actually safer (see `I-536` / `I-496`), but security patches take effect at next restart rather than live-switching.
3. **Host disk caching of multiple `erofs` base images:** During a rolling base transition, a host with both old-base running guests and new-base guests must hold 2–3 `erofs` images (~1.2–1.8 GB each, ~3–5 GB total) until old guests restart and refcount GC reaps the old `erofs`. Still far smaller than a 15–50 GB `/nix/store`, but requires explicit refcounted GC in `hostd`.
4. **In-guest `apt` / `mise` installs consume guest network egress and thin-pool bytes:** Previously, `repose config add` fetched packages onto the host's `/nix/store` (shared across tenants, not billed to the tenant's disk or egress). Installing packages inside `vdb` uses the tenant's disk (~10–200 MB) and egress (mitigated if the host's HTTP/APT cache proxy is kept, or negligible for small CLIs).
5. **Two different host OSes to maintain (NixOS on `host-02`/`edge`, Debian/Ubuntu on own-hardware appliances):** `hostd` itself is one static Go binary, which keeps runtime logic unified, but host provisioning has two paths (`nix/hosts/` for Azure/edge vs `images/appliance-arm64/` + `ops/install-host-x86.sh` for own hardware).

---

## 3. Complete Surface Inventory (Every File, Package, Interface & Feature)

Every surface below was verified against the live codebase at `main` (`5fa82823`).

### Surface 1: Guest Image Pipeline (New `images/guest/`, Replacing `nix/guest/` & `nix/overlay/`)

- **Current Nix files replaced (kept during dual-mode transition, retired in PR 8):**
  - `nix/guest/base/*.nix` (38 modules: `boot.nix`, `docker.nix`, `browser.nix`, `desktop.nix`, `claude-auth.nix`, `ssh.nix`, `tmux.nix`, `herdr.nix`, `keystroke-path.nix`, `tool-list.nix`, `agent-guide.nix`, `trim.nix`, `sysctl.nix`, etc.)
  - `nix/guest/microvm.nix`, `nix/guest/compose.nix`, `nix/guest/contract.nix`, `nix/guest/checks.nix`
  - `nix/overlay/agents/*.nix` (repackaging binaries from `nix/overlay/agents/versions.json`)
  - `.github/workflows/bump-agents.yml` (currently installs Nix to bump `versions.json`)
- **New non-Nix files:**
  - `images/guest/Dockerfile`: Multi-arch (`linux/amd64`, `linux/arm64`) Ubuntu 24.04 LTS minimal image installing:
    - Base OS: `systemd`, `systemd-resolved`, `systemd-networkd`, `openssh-server`, `sudo`, `nftables`, `iproute2`, `ca-certificates`, `locales` (`C.UTF-8`), `util-linux` (`fstrim`, `zramctl`, `fsfreeze`), `e2fsprogs` (`resize2fs`).
    - Pinned user `dev` (`UID=1000`, `GID=1000`, passwordless `sudo`, in `docker` group `GID=999`).
    - Toolchain parity with `nix/guest/base/tool-list.nix`: `curl`, `wget`, `jq`, `ripgrep`, `fd-find`, `bat`, `fzf`, `tree`, `unzip`, `zstd`, `htop`, `git`, `gh`, `just`, `nodejs` 24 + `pnpm` + `corepack`/`yarn`, `python3.12` + `uv`, `go`, `rustup`, `tmux`, `eza`, `zoxide`, `starship`, `direnv`, `neovim`, `gcc`/`g++`/`make`/`pkg-config`/`cmake`, `file`, `lsof`, `zip`, `dnsutils`, `sqlite3`, `postgresql-client`, `openssl`, `gnupg`, `psmisc`, `cloudflared`, plus `mise` for fast per-project runtime version pins.
    - Docker Engine + `docker-compose-plugin` + `containerd` with `/etc/docker/daemon.json` matching `nix/guest/base/docker.nix` (`overlay2`, `bip: 172.20.0.1/24`, `default-address-pools: 172.20.0.0/14`, `dns: ["172.20.0.1"]`, `live-restore: true`, `ipv6: false`).
    - Headed browser stack matching `nix/guest/base/browser.nix` & `desktop.nix`: Chromium + Playwright browsers, TigerVNC `Xvnc` on `:99`, `openbox`, `websockify`/noVNC, `repose-browser.service` (CDP on `127.0.0.1:9224 -> 9225`, `repose-browser.slice` 37.5% memory cap, 30-min idle stop).
    - All 5 coding agents + MCPs + `herdr` installed directly from `nix/overlay/agents/versions.json` (moved/shared at `images/guest/versions.json`) with the exact same `repose` wrappers, `repose-hook`, `agent-setup`, and a **rewritten `/etc/claude-code/CLAUDE.md`** (`images/guest/agent-guide.md`, replacing `nix/guest/base/agent-guide.md`): updates the 7 Nix-specific instructions (Ubuntu 24.04 LTS instead of NixOS, `sudo apt-get install` / `mise` instead of `nix profile add`, removes `machine.nix`, `flake.nix`/`nix develop`, `repose-store-gc`, `nix-collect-garbage`/`nix store gc`, and updates `sudo fstrim /` to `sudo fstrim /home/dev`). Keep in sync with `internal/cli/agent_guide_test.go`.
    - Go binaries: `guestd` and `repose-hook` compiled statically for `amd64` and `arm64`.
    - Systemd units: `guestd.service`, `repose-claude-auth.service`, `repose-browser.service`, `repose-zram-swap.service` (fixing `ENG-7` by loading `zram` module cleanly on `arm64`), `repose-trim.timer`, `keystroke-path` cgroup weights (`I-200`, `I-494`, `I-576`), SSH CA trust (`TrustedUserCAKeys`, `AuthorizedPrincipalsFile`), `sysctl` (`inotify.max_user_watches=1048576`).
  - `images/guest/initramfs/init`: ~80-line static BusyBox/shell (or tiny static Go) `init` that:
    1. Mounts `proc`, `sysfs`, `devtmpfs`; loads `virtio_blk`, `virtio_net`, `virtiofs`, `erofs`, `ext4`, `overlay` (or built-in kernel).
    2. Mounts `/dev/vda` (`erofs`, `ro`) at `/mnt/ro` and `/dev/vdb` (`ext4`, `rw,noatime,discard`) at `/mnt/rw`.
    3. Checks `/mnt/rw/.base-version` vs `/mnt/ro/etc/repose/base-version`: if changed, rotates `/mnt/rw/overlay/upper` -> `/mnt/rw/overlay/upper.prev` (never touching `/mnt/rw/home-dev`, `/mnt/rw/docker`, `/mnt/rw/services`, `/mnt/rw/etc-persist`), writes `/mnt/rw/.base-upgraded` marker for `guestd`.
    4. Mounts `overlayfs` (`lowerdir=/mnt/ro,upperdir=/mnt/rw/overlay/upper,workdir=/mnt/rw/overlay/work`) at `/sysroot`.
    5. Seeds persistent `/mnt/rw/etc-persist/machine-id` into `/sysroot/etc/machine-id` if absent.
    6. Bind-mounts `/mnt/rw/home-dev` -> `/sysroot/home/dev`, `/mnt/rw/docker` -> `/sysroot/var/lib/docker`, `/mnt/rw/services` -> `/sysroot/var/lib/repose/services`.
    7. Executes `switch_root /sysroot /sbin/init`.
  - `images/guest/build.sh`: `docker buildx build` -> `docker export` -> `mkfs.erofs -zlz4hc,9` -> extracts matching `vmlinuz`/`Image` + builds `initramfs.img` -> outputs `guest-<version>-<arch>.tar.zst` (or individual `rootfs.erofs`, `kernel`, `initramfs`, `SHA256SUMS`).
  - `.github/workflows/guest-image.yml`: Native `ubuntu-24.04` (`amd64`) + `ubuntu-24.04-arm` (`arm64`) runners building and publishing GitHub Release assets (`guest-<version>`) with `--latest=false` (preserving the `ENG-58` / `CONDUCTING.md` #8 invariant!).

### Surface 2: Host Daemon (`hostd`) & Cloud Hypervisor

- **Files modified / added:**
  - **NEW `internal/hostd/baseimg/`**: Downloads, verifies SHA-256, unpacks/caches `{kernel, initramfs, rootfs.erofs}` under `/var/lib/repose/images/<version>/` (atomic temp-dir + rename, Range resume on stall per `I-1414`), maintains refcounts against active/stopped guests in `bbolt`, and garbage-collects unreferenced images older than 24 h (always keeping the latest base).
  - **`internal/hostd/ch/ch.go`**:
    - Update `Spec` to support `RootImage` (`vda`, `erofs`, `readonly=on,image_type=raw`) + `VolumeDev` (`vdb`, `ext4`, `direct=on,image_type=raw`).
    - When `StoreTag == ""` (non-Nix guest), omit `--fs tag=ro-store,...` completely (keeping `--fs tag=claude-auth,...` when `AuthTag != ""`).
    - Note `DiskID`: when `vda` is `rootfs.erofs` (`_disk0`) and `vdb` is the thin volume (`_disk1`), `vm.resize-disk` MUST target `_disk1` (or explicit `id=vdb` in `--disk path=...,id=vdb` so resize never targets the read-only `erofs`!). **Critical catch** — current `ch.go:198` hardcodes `const DiskID = "_disk0"`.
    - `Cmdline()`: omit `init=/nix/store/...` for non-Nix guests (initramfs hands off to `/sbin/init`); keep `console=ttyS0`/`ttyAMA0`, `ip=...`, `systemd.hostname=...`, `panic=-1 reboot=t`.
  - **`internal/hostd/guest/unit.go`**:
    - Add `BindReadOnlyPaths=/var/lib/repose/images/<version>` (or `ReadOnlyPaths`) to `GuestUnitProps` so the unprivileged `guest@<id>` sandbox (`ProtectSystem=strict`, `DevicePolicy=closed`) can read `rootfs.erofs`, `kernel`, and `initramfs` while staying blocked from all other guests' sockets and volumes.
  - **`internal/hostd/guest/create.go`**:
    - Dual-mode branching on `g.BaseImage != ""` (non-Nix) vs `g.SystemClosure != ""` (legacy Nix on `host-02` during migration):
      - Non-Nix path skips `stepGCRoot`, skips `m.d.Nix.PathExists`, skips `virtiofs.Start` for `ro-store`, skips `extendView`, skips `m.d.Nix.DumpDB`, and skips `sess.RegisterPaths` in `deliver()`!
      - Keeps `startAuthShare` (`virtiofsd-auth` for Claude login share, `I-278`), `WriteSecrets`, `SetPrincipals`, `SetTriggers`, and `SetupProject`.
  - **`internal/hostd/guest/park.go`**:
    - In `UnparkAll`, skip starting `virtiofsd` for `ro-store` and `storeview` when `g.BaseImage != ""` — eliminates `virtiofsd_ms` + `store_view_ms` from unpark and removes Cloud Hypervisor's virtio-fs restore surface for the store.
  - **`internal/hostd/guest/build.go` & `manager.go` & `cmd/hostd/main.go` & `internal/hostd/app/app.go`**:
    - Make `Nix`, `Roots`, `View`, `BuildUser`, `MaxBuilds`, `MaxBuildQueue` optional (disabled when `--mode=image` or when no `nix` binary is present on non-Nix appliances). Keep them active in dual-mode on `host-02` until all existing Nix projects are migrated, then remove `internal/hostd/nixbuild/`, `internal/hostd/gcroot/`, `internal/hostd/storeview/` in cleanup.
  - **`nix/hosts/hostd.nix`**:
    - Add `--images-dir /var/lib/repose/images` flag on `host-02` so hosted NixOS servers run non-Nix `erofs` guests seamlessly while still being managed by NixOS themselves.

### Surface 3: Guest Daemon (`guestd`) & Init

- **Files modified:**
  - **`internal/guestd/system/system.go`**:
    - Keep `RegisterPaths` and `Switch` working as no-ops or legacy handlers for Nix guests, and add `ApplyPackages` / `ApplyServices` (or extend `Switch` / new `ApplyConfig` RPC) for non-Nix guests.
  - **`internal/guestd/project/project.go`**:
    - Remove any assumption that `repose-paths.service` or `home-manager-dev.service` must complete before `tmux`/`herdr` starts (`SetupProject` becomes ~200 ms instead of 10–102 s!).
    - Keep `project.json`, `/run/repose/env`, checkout git init/origin, and `repose-tmux.service` / `repose-herdr.service` identical.
  - **`internal/guestd/sysdep/paths.go` & `freezer.go` & `docker.go`**:
    - Verify `GrowFs` (`resize2fs /dev/vdb`) targets `/dev/vdb` (the writable thin volume, not `/` which is `overlayfs`!). **Critical catch:** `resize2fs` on an `overlayfs` mountpoint fails; it must run against `/dev/vdb` (or `/mnt/rw`'s backing device resolved from `/proc/mounts`).
    - Verify `Freeze`/`Thaw` (`fsfreeze -f /mnt/rw` or the `vdb` mountpoint) freezes the writable `ext4` block device cleanly — `fsfreeze` on `overlayfs` `/` is unsupported (`EOPNOTSUPP`) in Linux! **Critical catch:** `internal/guestd/sysdep/freezer.go` MUST freeze the underlying `ext4` mountpoint (`/mnt/rw`, kept mounted inside the guest at `/run/repose/rw` or `/var/lib/repose/rw` via `mount --move` in `initramfs`) before LVM thin snapshot!
    - Verify `Sample` `statfs` for `root_used_bytes` / `root_size_bytes` (`I-567`) stats the `vdb` `ext4` mountpoint so disk usage reflects the thin volume, not `erofs`.
  - **NEW `internal/guestd/config/`**:
    - Applies declarative menu selections (`catalog.yaml` entries: runtimes via `mise`, services like PostgreSQL/Redis/MariaDB/Memcached/RabbitMQ/Meilisearch/NATS via pre-built systemd service units or `apt` + `/var/lib/repose/services/<svc>` data dirs, and CLIs via `apt`/upstream binaries), streaming progress lines back to `hostd` -> `api` -> `CLI`.

### Surface 4: Config & Package Management (`repose config add`, `machine.nix`, `fragment.nix`, `devShell`)

- **Current files:** `internal/menu/catalog.yaml`, `internal/menu/menu.go`, `internal/menu/lint.go`, `internal/api/config/parse.go`, `internal/api/http/config.go`, `internal/api/http/personal.go`, `internal/cli/configcmd.go`, `internal/cli/configmenu.go`.
- **What changes for users:**
  - **`repose config add <item>` & Dashboard Config Menu (STAYS, much faster):**
    - The 24 catalog entries in `internal/menu/catalog.yaml` (runtimes: `bun`, `deno`, `nodejs`, `python`, `go-tools`, `zig`, `elixir`, `ruby`, `jdk`, `dotnet`; databases: `postgresql`, `redis`, `mysql`, `memcached`, `rabbitmq`, `meilisearch`, `nats`; tools/deploy: `awscli`, `opentofu`, `kubernetes`, `shell-extras`, `wrangler`, `supabase`, `flyctl`, `vercel`, `portless`) are backed by clean non-Nix install recipes (`mise` for runtimes/CLIs, `apt` + systemd units with data on `/var/lib/repose/services/` for databases) instead of generating a Nix `fragment.nix` and running a host-side `nix build`.
    - Arbitrary package names (`repose config add ffmpeg`) install via `apt-get install -y` (or `mise use -g`) inside the guest's `vdb` in seconds instead of a 60–300 s Nix evaluation + build.
    - **Zero host-side builds, zero `restrict-eval`, zero `closure_too_large` errors.**
  - **`repose.nix` / `fragment.nix` / `machine.nix` (RETIRED completely for non-Nix guests — owner decision 2026-11-22):**
    - `repose config apply repose.nix` and `machine.nix` are retired on `erofs` projects with no replacement custom-config file format (`repose.toml`/`setup.sh` are not needed — `repose config add` + in-guest `apt`/`mise` cover package & service needs).
    - `flake.nix` `devShell` auto-loading: `direnv` stays preinstalled in the guest; projects using `.envrc` / `mise.toml` / `.nvmrc` / `.python-version` / `rust-toolchain.toml` work out of the box.
  - **Migration path for existing projects on `host-02`:**
    1. Existing NixOS projects on `host-02` continue running untouched during dual-mode rollout (`projects.guest_runtime = 'nix'`).
    2. All *new* projects created after cutover default to `guest_runtime = 'erofs'`.
    3. Existing projects with menu-only configs can opt into one-command migration (`repose migrate` or snapshot-restore of `/home/dev` onto a fresh `erofs` guest with their menu items re-applied); projects with hand-written `fragment.nix` get a clear 30-day notice before Nix guest support is retired from `host-02`.

### Surface 5: Control Plane API, DB & Base Publish

- **Files modified:**
  - **NEW `internal/db/migrations/005X_nonix_base_images.{up,down}.sql`**:
    - Extend `base_versions` with:
      - `format text not null default 'nix'` (`'nix'` | `'erofs'`)
      - `image_amd64_url text`, `image_amd64_sha256 text`
      - `image_arm64_url text`, `image_arm64_sha256 text`
      - `kernel_version text`
    - Extend `projects` with `guest_runtime text not null default 'nix'` (switched to `'erofs'` default at cutover).
    - Extend `snapshots` with `base_version text` so restores know which base image `vda` produced the snapshot.
  - **`proto/repose/hostd/v1/hostd.proto` & `docs/interfaces/grpc-hostd.md`**:
    - Add `BaseImage` message (`version`, `url`, `sha256`, `kernel_version`) as optional field on `CreateGuest`, `StartGuest`, `Restore`, and `ApplyConfig` (keeping `system_closure` accepted for 1 release per `docs/interfaces/README.md`).
    - Add `"erofs"` to `Hello.features` so the API knows which hosts support non-Nix `erofs` guests vs legacy Nix-only hosts.
    - Add `ApplyMenu` / `ConfigSpec` field on `ApplyConfig` (or `CreateGuest`/`StartGuest`) for in-guest package/service application.
  - **`proto/repose/guestd/v1/guestd.proto` & `docs/interfaces/vsock-guestd.md`**:
    - Add `ApplyMenu` request/response (or extend `Switch`) for in-guest package/service reconciliation; mark `RegisterPaths` deprecated for `erofs` guests.
  - **`internal/api/ops/phases.go`**:
    - `PlanCreate()` for `erofs` projects becomes **`[PhaseCreateGuest]`** (no `PhaseBuild`!).
    - `PlanRestore()` for `erofs` projects skips `PhaseBuild`.
    - `PlanBuild()` for `erofs` menu updates becomes `[PhaseWake, PhaseApplyConfig]` (direct vsock push to `guestd`, no host-side build).
  - **`internal/api/basebump/basebump.go`**:
    - For `erofs` projects, a base bump **never enqueues a host-side build**. Instead, it marks `projects.pending_base_version = latest.Version` (applied automatically on the next `repose stop && repose start`, or via an explicit `repose restart` prompt, with `hostd` pre-fetching the new `erofs` image in the background).
  - **`cmd/repose-admin/` (`base publish`)**:
    - Accept `--format erofs --amd64-url ... --amd64-sha256 ... --arm64-url ... --arm64-sha256 ... --kernel-version ...` and verify HTTP 200 + SHA-256 manifest reachability before inserting into `base_versions` (closing the old `05/12` invalid-rev class of bug).

### Surface 6: Snapshots, Restore, Fork, Freeze/Thaw, Park/Unpark

- **Snapshots (`internal/hostd/guest/snapshot.go`, `internal/guestd/sysdep/freezer.go`):**
  - `guestd` runs `fsfreeze -f /run/repose/rw` (the `vdb` `ext4` mountpoint, NOT `/` `overlayfs`), `hostd` runs `lvcreate -s` on the thin volume, `guestd` runs `fsfreeze -u`.
  - The thin LV contains `/home/dev`, `/var/lib/docker`, `/var/lib/repose/services`, and `overlay/upper` — so **100% of user files, git state, databases, Docker images, and installed packages** are captured in the exact same `.img.zst` blob format!
- **Restore & Cross-Base-Version Safety:**
  - Restoring a snapshot onto the same or another host creates a thin LV from `.img.zst` and attaches the host's current `vda.erofs` (or the snapshot's pinned `base_version` if cached).
  - If the host's `vda.erofs` is newer than `/mnt/rw/.base-version` inside the restored snapshot, `initramfs` performs the clean `overlay/upper` rotation (Surface 1 / §2.3 #4), preserving `/home/dev`, `/var/lib/docker`, and `/var/lib/repose/services` intact while `guestd` re-applies the project's menu items. Zero `dpkg` corruption!
- **Fork (`internal/hostd/guest/clone.go`, `RestoreClone`):**
  - `lvcreate -s` thin clone of `vdb` is unchanged and instant. `guestd` `SetupProject` links `~/<new-slug>` to the old checkout (`I-255`), which lives on `/mnt/rw/home-dev`.
- **Idle Pause/Wake (`internal/hostd/guest/pause.go`):**
  - `vm.pause` / `vm.resume` in Cloud Hypervisor is completely orthogonal to Nix; unchanged.
- **Memory Park/Unpark (`internal/hostd/guest/park.go`, `internal/hostd/ch/park.go`):**
  - Cloud Hypervisor `vm.snapshot` / `vm.restore` becomes **simpler and faster** because `virtiofsd` for `ro-store` and `storeview` no longer exist (saving ~180 ms of `virtiofsd_ms` + `store_view_ms` on unpark and eliminating the `store_view` restore failure mode). `claude-auth` `virtiofsd` stays.

### Surface 7: Own-Hardware Appliances (Mac mini `arm64` + Generic x86 Bare-Metal)

- **Mac mini `arm64` appliance (NEW `images/appliance-arm64/`, replacing `nix/hosts/mac.nix`):**
  - Built from Debian 12/13 (or Ubuntu 24.04) `arm64` minimal via `debootstrap` / `mmdebstrap` + `systemd` into a tiny raw GPT image:
    - Partition 1: ESP (64 MiB)
    - Partition 2: Immutable/minimal root `ext4` (**2 GiB**, ~200–300 MB compressed `.raw.zst` or `.raw.gz`, down from **2.08 GB** and `37 GiB` apparent!)
    - Partition 3: `vg-guests` LVM thin pool PV (grown to disk size at first boot by `repose-mac-grow`, identical logic to `nix/hosts/mac.nix:125-186`).
  - Ships: pinned **6.18.57+ `arm64` kernel** (verified with `ops/dev/macmini-l2-try.sh`), `cloud-hypervisor-static-aarch64`, `virtiofsd` (for `claude-auth` only), `lvm2`, `nftables`, `wireguard-tools`, `iproute2`, `fstrim`, `repose-launcher-join`, `repose-mac-grow`, `repose-host-net`, `repose-register`, and standalone `hostd`.
  - **Standalone `hostd` atomic self-update:** `hostd` (or a lightweight `repose-hostd-update.timer` unit) checks a signed GitHub Release manifest, downloads the new `hostd` binary (~15 MB), verifies SHA-256, atomically swaps `/usr/local/bin/hostd`, and restarts `hostd.service` (which reconciles running guests without stopping them, `KillMode=process`). Replaces 15-minute Nix appliance image rebuilds (`ENG-128`) and closes `ENG-9` (unattended updates for owned hosts) in ~5 seconds!
  - **`internal/cli/appliance.go`:**
    - Update `applianceURL`, `applianceImageApparentBytes` (from `37 << 30` down to ~`4 << 30`), `applianceDiskMinGB` (can drop from `40` to `20` GB, fixing `ENG-87`!), and download/unpack logic (supporting `.zst` or `.gz`).
- **Generic x86 bare-metal Linux host installer (NEW `ops/install-host-x86.sh`):**
  - Turns any Ubuntu 24.04 / Debian 12+ x86_64 machine with `/dev/kvm` (AWS `c6i.metal`/`m6i.metal`, Hetzner AX/EX, or home Linux box) into a repose host in <60 seconds without `nixos-anywhere` wiping the OS:
    - Verifies `/dev/kvm`, `systemd`, `nftables`, `lvm2`, `wireguard`, kernel >= 6.18.57 (or installs/warns appropriately).
    - Installs pinned `cloud-hypervisor`, `virtiofsd`, `hostd` binaries + systemd units (`hostd.service`, `guests.slice`, `repose-guest-pool.service`, `repose-host-net.service`, `nftables` guest isolation ruleset matching `nix/hosts/nftables.nix`).
    - Configures `vg-guests/thin` on a specified block device or loopback/partition, takes the `--join` bundle, and registers with the control plane.
    - Enables **Track B (`N+4`)** parallel hardware verification on a fresh AWS x86 bare-metal instance alongside the Mac mini!

### Surface 8: CLI, Dashboard, Public Docs, Observability & Security

- **CLI (`internal/cli/`):**
  - `appliance.go`: New ~250 MB image URL/checksum/apparent-size/min-disk constants.
  - `configcmd.go`, `configmenu.go`: Adapt `repose config add/remove` for non-Nix packages/menu; deprecate `repose.nix` / `machine.nix` flags on `erofs` projects with clear migration guidance.
  - `buildlog.go`, `buildprogress.go`: Simplify progress output (no Nix build wall on create/start; fixes `ENG-27` structurally).
  - `hosts.go`: Support `--join` on Linux x86 bare-metal via `ops/install-host-x86.sh` without requiring `nixos-anywhere`.
- **Dashboard (`apps/web/src/routes/`):**
  - `projects/[id]/config/+page.svelte`: For `erofs` projects, show the Config Menu + base image version + restart-to-update banner; hide the raw Nix `fragment.nix` editor and `machine.nix` toggle (kept only for legacy `nix` projects during migration).
  - `settings/machine-nix/+page.svelte` & `settings/+layout.svelte`: Mark legacy/hidden for accounts with no Nix projects.
  - `settings/hosts/+page.svelte`: Show updated Mac mini download size (~250 MB) and Linux x86 bare-metal install command.
- **Public Docs & Agent Guide (`apps/web/src/content/docs/`, `images/guest/agent-guide.md`, `internal/cli/agent_guide_test.go`):**
  - Rewrite `agent-guide.md` (`/etc/claude-code/CLAUDE.md`) to remove all 7 Nix-specific instructions (NixOS intro, "as they would on Ubuntu", `nix profile add`, `machine.nix`, `flake.nix`/`nix develop`, `repose-store-gc`/`nix-collect-garbage`, and `fstrim /` -> `fstrim /home/dev`) and keep `internal/cli/agent_guide_test.go` passing 1:1 against the docs.
  - Overhaul all affected public docs pages (`config.md`, `machine.md`, `agents.md`, `own-hardware.md`, `cli.md`, `lifecycle.md`, `troubleshooting.md`, `limits.md`) in the same commits as user-visible changes (`I-242`, enforced by `internal/cli/docs_test.go` and `agent_guide_test.go`).
- **Observability (`ops/alerts.yaml`, `ops/dashboards/`, `internal/hostd/metrics/`):**
  - Add metrics: `repose_host_base_image_fetch_errors_total`, `repose_host_base_image_bytes`, `repose_host_base_image_fetch_duration_seconds`.
  - Add alerts: `BaseImageFetchFailed`, `BaseImageDiskHigh`.
  - Scope `StoreFull` and `BuildQueueStuck` (`ops/alerts.yaml:241,272`) to Nix-mode hosts only (and retire once `host-02` finishes migrating).
  - Update `ops/dev/release-assets` and `.github/workflows/release-assets.yml` to verify both `guest-<version>` (`amd64` + `arm64`) and `appliance-<version>` URLs return HTTP 200 anonymously.
- **Security Isolation Parity (MUST NOT REGRESS):**
  - Hypervisor boundary: Cloud Hypervisor KVM with `--seccomp true`, unprivileged `hostd` user, `DevicePolicy=closed` (`/dev/kvm`, `/dev/net/tun`, own thin LV only), `TemporaryFileSystem` + `BindPaths` for guest dir, `BindReadOnlyPaths` for `vda.erofs` image dir only, `RestrictAddressFamilies=AF_UNIX AF_VSOCK`.
  - Network boundary: Host `nftables` default-deny guest-to-guest & guest-to-host, `169.254.169.254` IMDS drop, TCP 25 + mining pool port blocks, 200 flows/s rate limit, 200 Mbit/s egress shaping (`tc`).
  - Auth & secrets boundary: SSH CA with `principal = project_id`, no SSH agent forwarding (`I-247`), secrets in Postgres ciphertext + guest tmpfs (`/run/repose/secrets`, 0400 `dev`), Claude auth on per-user `virtiofsd-auth` share (`I-278`), never logged (`docs/ops/OBSERVABILITY.md`).

---

## 4. Complete Feature and Performance Parity Matrix

Every user-visible feature and operational invariant, comparing measured Nix baselines against non-Nix targets and naming the exact staging/hardware verification check.

| Feature / Invariant | Current Nix Baseline (Measured) | Target Non-Nix Behavior | Expected Gain / Loss | Staging Verification Check (`PR N+1` / `N+2`) |
|---|---|---|---|---|
| **x86 Cold Create / Start** | ~11–12 s quiet (`host-02`); 26.4–40.0 s under load (`host-01`, `boot-speed.md`) | `vda` `erofs` + `vdb` thin LV + `initramfs` -> `systemd` -> `guestd Ready` (no `PhaseBuild`, no `RegisterPaths`, no `home-manager`) | **Gain:** Target **3–6 s** end-to-end (2–4× faster); zero build-queue stalls. | 10 consecutive creates & stop/starts on `host-stg-01`; record p50/p95 `total_ms` from `boot timings`. |
| **Mac mini (`arm64`) Cold Start** | **289 s** (`virtiofs`) / **57.9 s** (`erofs` Nix store); 43–172 s in `RegisterPaths` (`mac-mini-nested-kvm.md` §10–11) | `vda` `erofs` + `vdb` thin LV, zero `RegisterPaths`, zero `home-manager` | **Gain:** Target **<10 s** (6–30× faster); eliminates `ENG-70`, `ENG-80`, `ENG-103`. | 10 consecutive creates & stop/starts on AWS `mac-m4.metal` (`PR N+3`); 10/10 pass, p95 < 15 s. |
| **Mac Appliance Download & Disk** | **2.08 GB** compressed, 37 GiB apparent, 5.65–9.76 GB on APFS, 40 GB min disk (`mac-mini-nested-kvm.md` §9) | **~200–300 MB** compressed Debian `arm64` image, ~4 GiB apparent, ~1 GB on APFS, 20 GB min disk | **Gain:** **~8× smaller download**, **~5× less APFS footprint**, halves min free-disk floor (`ENG-87`). | Measure `curl` download bytes, `du -sh disk.raw`, and `repose host add` wall time on `mac-m4.metal`. |
| **`hostd` Upgrade on Owned Hardware** | **15 min** full Nix appliance image rebuild (`ENG-128`) | ~15 MB standalone `hostd` binary atomic swap + `systemctl restart hostd` | **Gain:** **<10 s** unattended update, zero guest downtime (`ENG-9`, `ENG-128`). | Trigger `hostd` update on `mac-m4.metal` with running guest; verify uptime preserved & new version in `Hello`. |
| **Hypervisor Isolation & Sandbox** | Cloud Hypervisor KVM, `--seccomp true`, `guest@*` unprivileged unit, `DevicePolicy=closed` (`I-51`) | Identical CH + `guest@*` sandbox; `vda.erofs` mounted `BindReadOnlyPaths`; no `ro-store` `virtiofsd` | **Gain:** Smaller host attack surface (1 fewer `virtiofsd` per guest). Zero regression. | Run `ops/checks/isolation-host01.sh` (18 isolation rows: guest↔guest, guest↔host, IMDS, seccomp, cgroup devices). |
| **SSH Cert Auth, Multiplexing & Sync** | Ed25519 CA, `principal=project_id`, no agent forwarding (`I-247`), git-bundle sync (`I-150`, `I-258`) | Identical `sshd` config, `repose ssh-prepare`, `repose sync`, `repose code`, `repose cp` | **Parity** (0 change to user workflow). | Run `repose run`, `sync`, `exec`, `ssh`, `cp`, `ssh -A` refusal test against staging `erofs` guest. |
| **5 Coding Agents + Hooks + MCPs** | `claude`, `opencode`, `codex`, `gemini`, `pi` + `repose-hook` + `repose-notify`/`ask` + `/etc/claude-code/CLAUDE.md` | Identical versions from `versions.json`, identical wrappers & hooks in `erofs` | **Parity** (plus faster daily `bump-agents` without Nix). | Run each of the 5 agents + `repose-notify` + `repose-ask` + `repose reply` on staging `erofs` guest. |
| **Claude Login Share (`I-278`)** | Per-user `virtiofsd-auth` + `repose-claude-auth.service` file bind mount | Identical `virtiofsd-auth` + `repose-claude-auth.service` | **Parity**. | Login in guest A, verify guest B sees `~/.claude/.credentials.json` without re-login. |
| **Headed Browser & `repose browser`** | Chromium on `:99` (`Xvnc` + `openbox`), Playwright MCP + `chrome-devtools-mcp` CDP, noVNC viewer (`I-246`, `I-292`) | Identical systemd units & Chromium/Playwright inside `erofs` | **Parity**. | Drive Chromium via Playwright MCP, open `repose browser` viewer, verify frame capture & idle stop. |
| **Docker-in-VM (`overlay2`)** | Rootful Docker on `ext4` thin LV (`bip 172.20.0.1/24`, `live-restore: true`) | Rootful Docker on direct `vdb` `ext4` bind mount (`/var/lib/docker`) | **Parity** (avoids overlay-on-overlay pitfall). | `docker run --rm alpine sh -c 'apk add curl && curl -I https://example.com'` + `docker compose up` on staging. |
| **`repose config add` (Packages & Services)** | Generates `fragment.nix` -> host `nix eval` + `nix build` (30–180 s) -> `switch-to-configuration` | `guestd` installs via `mise`/`apt` + systemd service unit on `vdb` (~5–20 s) | **Gain:** 5–10× faster, zero host CPU/RAM build spikes. **Loss:** Uses guest disk/egress; no arbitrary Nix expressions. | `repose config add postgresql redis bun awscli` on staging; verify `psql`, `redis-cli`, `bun`, `aws` work & survive reboot. |
| **Custom `repose.nix` / `machine.nix`** | Home Manager module evaluated & built on host | Retired on `erofs` guests (kept on legacy `nix` projects during migration) | **Loss (known trade-off):** Power users wanting Nix install it inside `vdb`; standard Linux `apt`/`mise` replaces it. | Verify clear error/migration message when `repose config apply repose.nix` is run on an `erofs` project. |
| **Snapshots, Restore & Fork** | `fsfreeze /` -> `lvcreate -s` -> `zstd` -> Blob; instant local thin clone for fork (`I-701`) | `fsfreeze /run/repose/rw` (`vdb` `ext4`) -> `lvcreate -s` -> `zstd` -> Blob; instant thin clone for fork | **Parity** (verified across base-image upgrades via `upper` rotation). | Snapshot running guest with DB + Docker + uncommitted git edits -> destroy -> restore -> restore onto newer base -> fork. |
| **Online Volume Resize (`lvextend` + `GrowFs`)** | `lvextend` + CH `vm.resize-disk _disk0` + `resize2fs` (1.5 s live) | `lvextend` + CH `vm.resize-disk vdb` + `resize2fs /dev/vdb` | **Parity** (requires explicit `id=vdb` in `ch.Spec`). | `repose resize --disk 30` on running staging guest; verify `df -h /home/dev` grows live. |
| **Idle Pause/Wake (`I-818`)** | CH `vm.pause` / `vm.resume` | Identical | **Parity**. | Pause running staging guest, connect via SSH, verify wake < 1 s and `tmux` session intact. |
| **Memory Park/Unpark (`I-652`, `I-1386`)** | Park 6.5 s, Unpark 3.8 s on Mac (`mac-mini-nested-kvm.md` §10) | Identical CH `vm.snapshot`/`vm.restore`, minus `ro-store` `virtiofsd` | **Gain:** ~200 ms faster unpark, simpler restore path. | Park & unpark staging guest + Mac appliance guest; verify `/proc/uptime` & tmpfs marker survive. |
| **Base Image Upgrades (`basebump`)** | Nightly host-side `nix build` + live `switch-to-configuration` | Background `erofs` prefetch on host; applied on next stop/start with clean `upper` rotation | **Gain:** Zero host build storms (`ENG-149`). **Loss:** Needs restart to take effect. | Publish base v2 on staging, restart guest, verify new base version, intact `/home/dev` & DBs, re-applied menu items. |
| **CI Evaluation Memory** | 8.47–12 GiB RAM (`ENG-144`, OOMs 16 GiB runners) | Standard `docker buildx` + Go tests (<2 GiB RAM); Nix checks scoped to `hosts`/`edge` only | **Gain:** Eliminates CI OOMs and 45-min `aarch64` Nix builds. | Run full CI workflow on PRs `2..8`; verify peak RSS < 4 GiB. |

---

## 5. Stacked-PR and Parallel Workstream Split

Designed for immediate handoff to a mini-conductor (`docs/workstreams/CONDUCTING.md`). PRs **2 through 8** have **disjoint file ownership** so parallel builders can work simultaneously in separate worktrees with zero merge conflicts.

```
PR 1 (nonix-research, THIS PR — docs only)
 ├── PR 2 (ws-nonix-guest-image)       [parallel, no code prereq]
 ├── PR 3 (ws-nonix-contracts-api)     [parallel, defines proto/DB/API contracts first]
 │    ├── PR 4 (ws-nonix-guestd)       [consumes PR 3 proto]
 │    ├── PR 5 (ws-nonix-hostd)        [consumes PR 3 proto + PR 2 image layout]
 │    └── PR 7 (ws-nonix-config-cli-web) [consumes PR 3 API contract]
 ├── PR 6 (ws-nonix-appliances)        [parallel; consumes PR 5 hostd binary]
 └── PR 8 (ws-nonix-obs-cleanup)       [parallel; consumes PR 5 metrics & PR 2/6 asset URLs]
       │
       ▼ (all PRs 2..8 merged to program branch)
PR 9  (N+1: Staging Integration & E2E on host-stg-01)
       │
       ▼
PR 10 (N+2: Parity Audit, Benchmarks & Ruthless Customer Review)
       │
       ▼ (OWNER SPEND APPROVAL GATE — ZERO AWS ALLOCATION BEFORE THIS)
 ├── PR 11 (N+3 Track A: AWS Mac mini arm64 verification)   [parallel]
 └── PR 12 (N+4 Track B: AWS x86 bare-metal verification)   [parallel]
```

### Parallel Builder Workstreams (`PR 2` .. `PR 8`)

| PR | Branch / Workstream | Kaneo Ticket | Owned Files (Disjoint) | Consumed Interfaces | Prerequisites | Definition-of-Done Evidence |
|---|---|---|---|---|---|---|
| **PR 2** | `ws/nonix-guest-image` | `ENG-150` | `images/guest/**` (incl. rewritten `agent-guide.md`), `.github/workflows/guest-image.yml`, `scripts/bump-agents.sh` (non-Nix mode) | `docs/interfaces/guest-conventions.md` | PR 1 | `amd64` + `arm64` `rootfs.erofs` + `kernel` + `initramfs` built cleanly; local Cloud Hypervisor KVM boot test proves: `systemd` reaches `multi-user.target` in <5 s, `/home/dev` & `/var/lib/docker` are direct `vdb` `ext4` mounts (not `overlayfs`), Docker `overlay2` runs a container, `fsfreeze -f /run/repose/rw` succeeds, all 5 agents + `guestd` + Chromium `:99` + rewritten `/etc/claude-code/CLAUDE.md` present, base-upgrade `upper` rotation test preserves `/home/dev` & `/var/lib/repose/services`. |
| **PR 3** | `ws/nonix-contracts-api` | `ENG-151` | `proto/repose/**`, `internal/gen/**`, `docs/interfaces/{grpc-hostd,vsock-guestd,api,db-schema}.md`, `internal/db/migrations/005X_*`, `internal/api/{store,ops,basebump,http,hostmgr}/**`, `internal/fakes/{hostd,api}/**`, `cmd/repose-admin/**` | None (producer) | PR 1 | `buf lint` clean; `TestMigrateUpDownUp` passes on Postgres; `PlanCreate`/`PlanRestore` skip `PhaseBuild` for `erofs` projects; `basebump` marks `pending_base_version` with zero build ops; `repose-admin base publish --format erofs` validates URLs/SHA-256; dual-shape backward compatibility tests for legacy Nix `system_closure` pass; `go test -race ./internal/api/... ./internal/db/...` green. |
| **PR 4** | `ws/nonix-guestd` | `ENG-152` | `internal/guestd/**`, `cmd/guestd/**`, `internal/fakes/guestd/**` | PR 3 (`vsock-guestd.md`, `guestd.proto`), PR 2 (mount layout `/run/repose/rw`) | PR 3 | `SetupProject` runs with zero `repose-paths`/`home-manager` dependency; `Freeze`/`Thaw` targets `/run/repose/rw`; `GrowFs` targets `/dev/vdb`; `Sample` `statfs` reads `/run/repose/rw`; `ApplyMenu` installs/removes runtimes (`mise`) & services (`systemd` + `/var/lib/repose/services/`) idempotently; `go test -race ./internal/guestd/... ./cmd/guestd/...` green. |
| **PR 5** | `ws/nonix-hostd` | `ENG-153` | `internal/hostd/**`, `cmd/hostd/**`, `nix/hosts/hostd.nix`, `docs/interfaces/host-conventions.md` | PR 3 (`grpc-hostd.md`, `hostd.proto`), PR 2 (image bundle format) | PR 3 | `internal/hostd/baseimg` fetches, SHA-256 verifies, caches, and refcount-GCs `erofs` bundles; `ch.Spec` boots `vda=erofs` + `vdb=thin LV` (`id=vdb` for `vm.resize-disk`) with no `ro-store` `virtiofsd` and no `RegisterPaths`; `GuestUnitProps` adds `BindReadOnlyPaths` for image dir while preserving `DevicePolicy=closed` & `--seccomp true`; dual-mode tests prove legacy Nix guests on `host-02` still boot & switch; `go test -race ./internal/hostd/... ./cmd/hostd/...` green. |
| **PR 6** | `ws/nonix-appliances` | `ENG-154` | `images/appliance-arm64/**`, `ops/install-host-x86.sh`, `internal/cli/appliance*.go`, `.github/workflows/appliance-image.yml` | PR 5 (`hostd` standalone binary & flags) | PR 5 | `arm64` Debian appliance image builds to <=350 MB compressed (`<4 GiB` apparent) with pinned `6.18.57+` kernel, `repose-mac-grow`, `repose-launcher-join`, `hostd`, and atomic `hostd` self-updater; `ops/install-host-x86.sh` passes shellcheck + dry-run/VM test on Ubuntu 24.04 x86_64; `internal/cli` appliance unit tests (`TestAppliance*`, `darwin/arm64` build) pass. |
| **PR 7** | `ws/nonix-config-cli-web` | `ENG-155` | `internal/menu/**`, `internal/cli/{config*,build*,agent_guide_test}.go`, `apps/web/src/routes/projects/[id]/config/**`, `apps/web/src/routes/settings/machine-nix/**`, `apps/web/src/routes/settings/hosts/**`, `apps/web/src/content/docs/**` | PR 3 (`api.md`), PR 4 (`ApplyMenu` catalog recipes), PR 2 (`images/guest/agent-guide.md`) | PR 3 | `catalog.yaml` carries non-Nix recipes for all 24 entries; `repose config add/remove` works without Nix; `repose config apply repose.nix` on `erofs` gives clear retirement message; dashboard Config/Hosts views pass `svelte-check`, `vitest`, Playwright at 1440 & 390 in light/dark; `internal/cli/docs_test.go` and `agent_guide_test.go` pass against updated public docs and rewritten `agent-guide.md`. |
| **PR 8** | `ws/nonix-obs-cleanup` | `ENG-156` | `ops/alerts.yaml`, `ops/alerts_test.yaml`, `ops/dashboards/**`, `ops/dev/release-assets`, `.github/workflows/release-assets.yml` | PR 5 (new `baseimg` metrics), PR 2 & PR 6 (release asset URLs) | PR 2, PR 5, PR 6 | `promtool test rules ops/alerts_test.yaml` green with `BaseImageFetchFailed`/`BaseImageDiskHigh` and Nix-only alerts scoped away from `erofs`-only hosts; `ops/dashboards/validate.py` green; `ops/dev/release-assets` verifies guest `amd64`/`arm64` + appliance assets return HTTP 200 anonymously. |

### Integration, Parity & Hardware Verification PRs (`PR 9` .. `PR 12`)

| PR | Phase | Kaneo Ticket | Scope & Exit Gate |
|---|---|---|---|
| **PR 9 (`N+1`)** | Staging Integration & E2E | `ENG-157` (Blocked on PRs 2..8) | Deploy full stack to staging (`api` + `host-stg-01` running NixOS host with `erofs` guests). Prove every flow end-to-end: `repose run`, SSH, git sync, all 5 agents, `repose-notify`/`ask`, Claude login share, Docker `overlay2`, Chromium + `repose browser`, `repose config add postgresql redis bun`, `repose resize`, snapshot + destroy + restore, restore onto base v2 (`upper` rotation), fork, idle pause/wake, memory park/unpark, and `ops/checks/isolation-host01.sh` 18/18 pass. |
| **PR 10 (`N+2`)** | Parity Audit, Benchmarks & Review | `ENG-158` (Blocked on PR 9) | Produce side-by-side benchmark report (Nix baseline vs `erofs` on `host-stg-01`), verify zero regression on every row of §4 Parity Matrix, and execute a 3-surface ruthless customer review (`docs/REVIEW.md`: CLI, Dashboard, Guest/Config) with zero open P0s. |
| **PR 11 (`N+3` Track A)** | AWS Mac mini (`arm64`) Verification | `ENG-159` (Blocked on PR 10 + **owner spend approval**) | Allocate fresh AWS `mac-m4.metal` (~$30/24h, **ONLY after explicit owner approval**). Install ~250 MB Debian `arm64` appliance via `repose host add`, verify 10/10 guest starts (<15 s p95), Docker, browser, `config add`, snapshots/restore, park/unpark, and <10 s `hostd` self-update. Release Dedicated Host immediately upon completion. |
| **PR 12 (`N+4` Track B)** | AWS x86 Bare-Metal Verification | `ENG-160` (Blocked on PR 10 + **owner spend approval**) | Run **in parallel with Track A** on a fresh AWS x86 bare-metal instance (`c6i.metal` / `m6i.metal`, **ONLY after explicit owner approval**). Provision via `ops/install-host-x86.sh`, join to staging, verify 10/10 `erofs` guest starts, full isolation suite, snapshots, restore, fork, and `hostd` self-update. Terminate instance immediately upon completion. |

---

## 6. Kaneo Board Mapping

### 6.1 New Program Tickets Filed

| Ticket | Column | Priority | Workstream |
|---|---|---|---|
| `ENG-150` | `Open` | high | PR 2: Guest image pipeline (`Dockerfile`, `initramfs`, `erofs`, `amd64`+`arm64` CI) |
| `ENG-151` | `Open` | high | PR 3: Interface contracts, proto, DB migration & control plane API |
| `ENG-152` | `Open` | high | PR 4: Guest daemon (`guestd`) non-Nix boot, initramfs handoff & in-guest config apply |
| `ENG-153` | `Open` | high | PR 5: Host daemon (`hostd`) `erofs`+`overlayfs` runtime, base image cache & dual-mode `host-02` |
| `ENG-154` | `Open` | high | PR 6: Non-Nix own-hardware appliances (`arm64` Mac `vfkit` image + x86 bare-metal installer) |
| `ENG-155` | `Open` | medium | PR 7: Non-Nix config menu, CLI, dashboard & public docs |
| `ENG-156` | `Open` | medium | PR 8: Observability, alerts, dashboards & `release-assets` checks |
| `ENG-157` | `Blocked` | high | PR 9 (`N+1`): Staging integration & E2E verification on `host-stg-01` |
| `ENG-158` | `Blocked` | high | PR 10 (`N+2`): Parity audit, side-by-side benchmarks & ruthless customer review |
| `ENG-159` | `Blocked` | high | PR 11 (`N+3` Track A): AWS Mac mini (`arm64`) non-Nix appliance & `erofs` guest verification |
| `ENG-160` | `Blocked` | high | PR 12 (`N+4` Track B): AWS x86 bare-metal installer & `erofs` guest verification |

### 6.2 Existing Nix-Specific Kaneo Tickets Superseded by This Program (For Owner Sign-Off — NOT Closed Yet)

The following existing board tickets exist solely because guests or own-hardware appliances use Nix. None have been closed or parked yet; they are listed here for owner sign-off once PRs `9..12` prove the replacement:

| Ticket | Current Column | Summary | Why Superseded by No-Nix Program |
|---|---|---|---|
| `ENG-6` | `Open` | `hostd` caches a closure's path database per closure, not per start | `RegisterPaths` (`nix-store --dump-db`/`--load-db`) is eliminated completely. |
| `ENG-7` | `Open` | `zram-swap` and docker's first start fail on `aarch64` | Replaced by new Ubuntu/Debian `arm64` guest image + direct `vdb` `/var/lib/docker` mount (verified in PR 2/4/11). |
| `ENG-11` | `Open` | The kexec installer does not build for `aarch64` | Own-hardware Linux/Mac hosts no longer use NixOS `nixos-images` kexec installers. |
| `ENG-70` | `Open` (`urgent`) | A guest takes 58 s to start on a Mac and it must be seconds | Eliminating Nix `RegisterPaths` + `home-manager` brings Mac guest start to <10 s. |
| `ENG-87` | `Open` | Freed 60 GB docs asked for and install still refused | Shrinking Mac appliance from 37 GiB apparent / 40 GB min to ~4 GiB apparent / 20 GB min resolves this structurally. |
| `ENG-103` | `Open` | Starting first machine on Mac took 17 minutes | Eliminating 2.08 GB Nix appliance + Nix store fetch/build removes the 17-min cold path. |
| `ENG-110` | `Open` | Published appliance ships `guestd` built from dirty tree | Non-Nix `guest-image.yml` and `appliance-image.yml` stamp clean git SHAs from CI; `guestd` lives in `rootfs.erofs`, not the appliance image. |
| `ENG-125` | `Open` | No guest image with corrected `pathsWait = 1200` has been started | `repose-paths.service` and `pathsWait` disappear with Nix `RegisterPaths`. |
| `ENG-128` | `Open` | Testing a `hostd` change on the Mac costs a 15-minute image rebuild | Standalone `hostd` binary atomic self-update replaces NixOS image rebuilds (<10 s). |
| `ENG-135` | `Open` | Whole-store GC inside a guest exhausts `virtiofsd` `LimitNOFILE` | `ro-store` `virtiofsd` and in-guest `/nix/store` GC are eliminated. |
| `ENG-9` | `Open` | Unattended updates for owned hosts (workstream 19g) | Subsumed by PR 6's standalone `hostd` atomic self-updater + `erofs` base prefetch. |
| *(Already Done, context)* | `Done` | `ENG-80`, `ENG-100`, `ENG-124`, `ENG-144`, `ENG-149` | Previously patched Nix-specific symptoms (boot deadlines, `pathsWait`, 8.5 GB flake eval OOM, unexpected base-update build queue storms); this program eliminates their root cause permanently. |

---

## 7. Locked-In Owner Decisions (2026-11-22)

1. **Base Linux distro split (APPROVED):** Ubuntu 24.04 LTS for guests (`images/guest/`), minimal Debian 12/13 for the ~250 MB Mac `arm64` appliance (`images/appliance-arm64/`).
2. **`repose.nix` / `machine.nix` cutover (APPROVED):** Retire custom Nix config completely on `erofs` projects with no replacement custom-config file format (`repose config add` + in-guest `apt`/`mise` only).
3. **Base image upgrades (APPROVED — Option A):** Rotate `overlay/upper` cleanly on base-version bumps, preserve `/home/dev`, `/var/lib/docker`, `/var/lib/repose/services`, and `/etc/machine-id` unconditionally, and auto-replay `config_revisions.menu` (plus optional `apt-manual.list`).
4. **Global agent instructions & public docs (APPROVED):** Rewrite `agent-guide.md` (`/etc/claude-code/CLAUDE.md`) for Ubuntu `erofs` guests (removing all 7 Nix-specific lines and fixing `fstrim`) and update `internal/cli/agent_guide_test.go` + all affected `apps/web/src/content/docs/` pages.
5. **Future roadmap (separate parked ticket, NOT in this program):** Support Antigravity CLI (`agy`) as a first-class agent inside repose guests.
