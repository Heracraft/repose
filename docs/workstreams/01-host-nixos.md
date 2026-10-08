# 01: Host NixOS configuration

Milestone: M1. Owns `interfaces/host-conventions.md`. Consumed by 03-hostd
(runs on it), 11-infra-opentofu (provisions it), 10-observability (scrapes
it).

## 1. Goal

Produce the NixOS configuration for a repose host: a machine that boots
from nixos-anywhere with nothing on it but the plumbing hostd needs to run
tenants' guests, and nothing else. Every path, device and rule in
`interfaces/host-conventions.md` is created by this configuration.

## 2. Scope: builds

- `nix/hosts/default.nix`: the host module, parameterised by `hostName` and
  `provider` (`azure` now, `hetzner` later), imported by
  `nix/flake.nix` as `nixosConfigurations.host-<name>`.
- `nix/hosts/disko.nix`: disk layout for nixos-anywhere. OS disk: GPT, 1 GB
  ESP, rest ext4 for `/`. Data disk (`/dev/disk/azure/scsi1/lun0` on Azure,
  passed in as `dataDevice`): one PV, VG `vg-guests`, thin pool `thin` using
  all but 5 percent of the VG (pool metadata sized at 1 percent, minimum
  1 GB).
- `nix/hosts/kernel.nix`: `boot.kernelModules = [ "kvm-intel" "vhost_vsock"
  "vhost_net" "tun" "nf_tables" "dm_thin_pool" "overlay" ]`,
  `boot.kernelParams = [ "transparent_hugepage=madvise" ]`, sysctl
  `net.ipv4.ip_forward=1`, `fs.inotify.max_user_instances=8192`,
  `vm.overcommit_memory=1` (CH mmaps guest RAM and the host must not refuse
  it), `kernel.unprivileged_userns_clone=1`.
- `nix/hosts/network.nix`: `br-guests` bridge with the host address `.1` of
  the host's `/22` (read at boot from `/var/lib/repose/hostd/host.json`
  by a `repose-host-net` oneshot that runs before hostd, because the CIDR
  is assigned at registration, not at build time); no DHCP on the bridge;
  `wg0` via `networking.wireguard.interfaces.wg0` with keys and peer from the
  same `host.json`; systemd-networkd, not scripted networking.
- `nix/hosts/nftables.nix`: the `repose` table exactly as
  `host-conventions.md` describes, with the guest chain rules templated by
  hostd at runtime (hostd adds and removes per-guest counter rules and `tc`
  classes; the base table is declarative and hostd's rules live in a
  separate chain `guest_dyn` it owns).
- `nix/hosts/storage.nix`: `services.lvm.enable`, `boot.initrd.services.lvm
  .enable`, thin provisioning tools, `lvm.conf` with
  `thin_pool_autoextend_threshold = 80` and
  `thin_pool_autoextend_percent = 10` so the pool grows into the VG headroom
  rather than filling silently; a `repose-pool-monitor.timer` every 5
  minutes that exports pool usage to a textfile for node_exporter.
- `nix/hosts/virt.nix`: `cloud-hypervisor`, `virtiofsd`, `ch-remote` in
  `environment.systemPackages`; a `virtiofsd` user and group; udev rule
  giving `kvm` group access to `/dev/kvm` and group `hostd` to the
  `g-<id>` volumes of `vg-guests`; `hostd` runs as root (it needs LVM,
  nftables, tap creation), starts every `guest@<id>` as the `hostd` user
  (in `kvm`; DECISIONS I-51) and drops to `virtiofsd:virtiofsd` when
  spawning virtiofsd with `--sandbox namespace --shared-dir /run/repose/store-view
  --cache auto --xattr --socket-group hostd`. virtiofsd sees only the
  guest's own closure: `/run/repose/store-view` is an empty tmpfs in the
  unit's private mount namespace into which hostd binds each path of the
  closure (DECISIONS I-463). The earlier whole-store export
  (`/run/repose/store-export`, `.links` masked) stays one release for
  guests started before it.
- `nix/hosts/hostd.nix`: `systemd.services.hostd` with `Restart=always`,
  `RestartSec=2`, `After=network-online.target repose-host-net.service`,
  `ExecStart=${hostd}/bin/hostd --state /var/lib/repose/hostd`,
  `LimitNOFILE=1048576`, `StateDirectory=repose/hostd`, `KillMode=process`
  (guests are transient units, not children; a hostd restart must not stop
  guests), plus `repose-snapshot.timer` at 03:00 local calling
  `hostd snapshot --all --reason scheduled`.
- `nix/hosts/gc.nix`: `nix.gc.automatic = true` weekly with `--delete-older-
  than 14d`; the GC roots directory `/nix/var/nix/gcroots/repose/` is
  created and owned by root; `nix.settings.min-free` 50 GB and `max-free`
  100 GB so a build never fills the store completely; `nix.settings.sandbox
  = true`; `nix.settings.trusted-users = [ "root" ]` only;
  `nix.settings.substituters` = cache.nixos.org plus the platform overlay
  cache URL (an attribute of the module, default empty until 12 publishes
  one); `nix.settings.max-jobs = 2`, `cores = 8` per the build caps.
- `nix/hosts/observability.nix`: `services.prometheus.exporters.node`
  bound to the `wg0` address only, textfile collector at
  `/var/lib/node_exporter/textfile`; Fluent Bit reading journald and
  `/var/lib/repose/guests/*/console.log` (tail input with the guest id as a
  label from the path) shipping to the Loki address in `host.json`, over
  `wg0`. Nothing listens on the Azure NIC.
- `nix/hosts/registration.nix`: `repose-register.service`, oneshot before
  hostd, reads `/run/repose/join-token` (written by cloud-init from the
  OpenTofu output), calls `hostd register`, which writes `host.json`,
  `cert.pem`, `key.pem`, deletes the token, and exits. Idempotent: if
  `host.json` exists the service does nothing.
- `nix/hosts/ssh.nix`: sshd on `wg0` only, `PermitRootLogin
  prohibit-password`, `TrustedUserCAKeys` = the Host CA public key (from
  `host.json`), no password auth, `AllowUsers root`. A PAM `session` hook
  that runs `hostd audit-login` so every interactive login lands in
  `audit_log`. Until the edge exists, a `bootstrap` option allows the
  operator's plain public key on the Azure NIC, removed by 11-infra when
  the edge is up.
- `nix/hosts/hardening.nix`: no `sudo` for anyone but root; `security.
  lockKernelModules = false` (hostd loads tun devices, and CH needs kvm
  after boot; the module list is fixed instead); `boot.tmp.useTmpfs = true`;
  journald `SystemMaxUse=4G`; `services.fstrim` weekly; automatic reboots
  off (an unattended reboot kills every tenant; kernel updates are applied
  by draining a host, 03 owns `Drain`).
- `nix/hosts/azure.nix`: `waagent` (Azure Linux agent) enabled so the
  platform can report VM health, but its extension handling disabled; the
  IMDS address is allowed from the host itself (waagent needs it) and
  blocked from guests by the nftables table; `services.cloud-init` with
  only the `write_files` module so the join token arrives.

## 3. Scope: does not build

- hostd itself (03). This workstream ships the unit file and the state
  directory; the binary comes from the flake's `packages.hostd` output that
  03 defines. Until 03 exists, the unit points at a stub that logs and
  sleeps, so the host boots and can be inspected.
- Guest configurations, the runner package, the base module (02).
- OpenTofu for the VM, disk, NAT gateway, cloud-init payload (11). This
  workstream provides the nixos-anywhere command and the disko layout; 11
  calls them.
- Prometheus, Loki, Grafana on the personal server (10). This workstream
  ships the exporters and the shipper.
- The edge and the WireGuard hub (06). This workstream ships the client.

## 4. Interfaces owned / consumed

Owned: `interfaces/host-conventions.md`. Every path and name there is
created here; a change to either happens in the same commit.

Consumed: the `host.json` shape written by `hostd register` (03), which this
workstream reads at boot for the guest CIDR, wg keys, Loki address. Shape:

```json
{ "host_id": "...", "guest_cidr": "10.64.4.0/22", "wg": { "private_key": "...",
  "address": "10.255.0.7/16", "edge_pubkey": "...", "edge_endpoint": "edge.repose.herakraft.co:51820" },
  "host_ca_pub": "ssh-ed25519 ...", "loki_url": "http://10.255.0.1:3100" }
```

## 5. Design detail

### Why nixos-anywhere and not an image

Azure has no NixOS image and building one means an image pipeline (Packer or
nixos-generators, an Azure gallery, versioning). nixos-anywhere takes the
stock Ubuntu image, kexecs into a NixOS installer, partitions with disko, and
installs. It is one command from the operator's machine or from OpenTofu's
`local-exec`. Boot to a usable host is about 8 minutes. When that becomes the
bottleneck, 11 can add an image build, and this configuration is unchanged.

### Bridge addressing is runtime, not build time

The `/22` for a host is allocated by the api at registration. Baking it into
the NixOS config would mean one configuration per host and a rebuild to add
a host. Instead the network unit reads `host.json` and configures `br-guests`
and `wg0` at boot. The first boot (before registration) has no bridge, which
is fine because there are no guests yet; `repose-register.service` triggers
a restart of `repose-host-net.service` after writing `host.json`.

### The store a guest sees

A guest sees its own closure and nothing else of the host store (DECISIONS
I-463). The first design shared the whole store with `.links` (the
hard-link farm of `nix-store --optimise`) masked, on the reasoning that a
path is only readable by its hash. The store directory itself stayed
listable, though, so every project's closure and fragment source and the
host's own system closure were readable from any guest. Each
`virtiofsd@<id>` now shares an empty tmpfs in its own mount namespace, and
hostd binds the guest's closure into it at boot and adds a new closure's
paths before an in-place switch.

### Thin pool sizing and the 80 percent rule

The pool starts at 95 percent of the data disk and autoextends into the
remaining 5 percent when 80 percent full. hostd's `host_warning{kind:
"pool_high"}` fires earlier, at 70 percent, where the api stops placing new
projects on the host and an operator grows the disk (DECISIONS I-586). A full thin pool makes every guest's writes fail
at once; the two mechanisms exist so that never happens silently.

### Memory reservation

`systemd.slices.guests` with `MemoryMax = total - 16 GB` and each guest's
transient unit inside it with `MemoryMax = class RAM + 512 MB` and
`MemoryHigh` 128 MB below it. The 512 MB covers CH's own overhead (page
tables, io_uring, slab, the kernel and initrd it read); virtiofsd is its own
unit with `MemoryMax=1G`, and the disk is opened O_DIRECT so guest disk
writes never become host page cache in the unit (DECISIONS I-230). The slice cap is the hard
line that keeps hostd, virtiofsd and builds alive if the reservation
accounting in the api is ever wrong.

### Builds and guests share cores

`nix.settings.cores = 8`, `max-jobs = 2`, so builds use at most 16 of 64
vCPU. Builds run in `system.slice`, guests in `guests.slice`, and
`CPUWeight` is 100 for guests and 50 for builds. A build never starves a
running agent.

### Draining

`hostd drain` (03) stops placements, snapshots every guest, and reports.
The host config supports it with nothing special; kernel updates are applied
by drain, `nixos-rebuild boot`, reboot, undrain. `system.autoUpgrade` is off.

### Hardening list

- Only `root` exists as a human-usable account, and only via the Host CA.
- The Azure NIC accepts nothing inbound (NSG in 11, and nftables `input`
  chain drops everything not on `wg0` or `lo` except waagent's needs).
- `/dev/kvm` is group `kvm`, mode 0660; the `hostd` user that runs the
  `guest@<id>` units is in `kvm` (I-51), virtiofsd does not need it, hostd
  itself is root.
- Guests cannot reach the host: nftables `guest_in` drops all traffic from
  `br-guests` to the host's addresses, including the bridge `.1`, except
  ICMP echo for debugging (rate-limited) and packets in the reply direction
  of a flow the host itself opened (`ct direction reply`, so a guest's own
  first packet never matches it), both deliberate exceptions recorded here
  and in DECISIONS I-18 and I-74.
- Every `Exec` into a guest and every host login is audited.

## 6. Failure modes

| Failure | Operator-visible outcome |
|---|---|
| Data disk missing at install | disko fails with `device /dev/disk/azure/scsi1/lun0 not found`; nixos-anywhere aborts before touching the OS disk. Attach the disk, rerun. |
| `host.json` absent at boot (registration never ran) | `repose-host-net.service` logs `no host.json; bridge not configured` and exits 0; hostd starts, sees no registration, retries `Register` every 30 s using the join token, logs `waiting for join token` if that is missing too. Alert `host_unregistered` after 10 minutes (10-observability). |
| Join token rejected | hostd logs `register: join token invalid or used`; same alert. Operator mints a new one with `repose-admin hosts add --reissue <host>` and writes it to `/run/repose/join-token`. |
| Thin pool at 70 percent, data or metadata | `host_warning{pool_high}` event, `PoolHigh` alert, no new placements; autoextend consumes the headroom at 80; at 85 percent hostd refuses `CreateGuest`, `Restore` and `ResizeVolume`, and at 95 `StartGuest`, with `insufficient_capacity`; existing guests keep running (DECISIONS I-586). |
| Store at 80 percent | `host_warning{store_80}`; `nix.settings.min-free` triggers GC of unrooted paths; builds fail with `closure_too_large` before touching the last 50 GB. |
| WireGuard handshake fails | `wg show wg0` shows no handshake; hostd's gRPC still works (it goes over the Azure NIC), so the api sees the host but the gateway cannot reach guests; alert `host_wg_down` from the edge side. |
| hostd crashes | Restarts in 2 s; guests are untouched (transient units, `KillMode=process`); hostd reconciles from `state.db` and `systemctl list-units 'guest@*'`. |
| Kernel panic or host reboot | Every guest is down; `Hello` after boot reports all guests `stopped`; the api restarts those that were `running` (03) and notifies users. |

## 7. Testing

- `nix flake check` builds the host configuration and runs `nixos-test`
  VM tests in `nix/hosts/tests/`: (a) the nftables table loads and a
  simulated guest namespace cannot reach `169.254.169.254` or the host,
  (b) the thin pool is created by disko and a thin volume can be created,
  (c) `repose-host-net` configures the bridge from a fixture `host.json`,
  (d) the hostd unit starts the stub and survives `systemctl restart`
  without stopping a transient `guest@test` unit.
- On a real Azure host (required, the VM test cannot do KVM inside KVM in
  CI): nixos-anywhere from scratch, then the checklist commands below.
  Paste outputs in the PR.

## 8. Rollback

Hosts are cattle. A bad host configuration is rolled back with
`nixos-rebuild switch --rollback` on the host (previous generation kept), or
by re-provisioning the host with the previous flake revision and restoring
guests from Blob. `disko` changes are never applied to a host with tenants;
they require a new host.

## 9. Checklist

- [ ] `nix build .#nixosConfigurations.host-bench.config.system.build.toplevel`
      succeeds. Evidence: CI. — open: CI only evaluates (`nix flake check
      --no-build`, .github/workflows/ci.yml) and builds no host toplevel; the
      build is recorded only locally (STATUS 2026-09-19 01-host-nixos
      done-local line). Needs a CI build step or a pasted build log
- [x] `nixos-anywhere` onto a fresh D64s_v5 completes and the host reboots
      into NixOS. Evidence: `nixos-version` output and the wall time,
      pasted. — closed: RESEARCH §11 (host-01 `Standard_D16s_v7` per DECISIONS
      I-39 in place of D64s_v5, NixOS 26.11 `b1b8759`, nixos-anywhere through
      the edge about ten minutes)
- [x] `lsblk` shows `vg-guests-thin` and `lvs` shows the pool at the
      expected size with autoextend set (`lvs -o+thin_autoextend` via
      `lvm.conf` check). Evidence: pasted. — closed: host-storage VM test
      subtests "disko creates the PV, vg-guests and the thin pool" and
      "autoextend is configured and monitored" (nix/hosts/tests/default.nix,
      EXIT=0 per STATUS 2026-09-20 01/03-host-followup done line); on host-01
      thin pool/lsblk per STATUS 2026-09-20 m1-integration done line
- [x] After writing a fixture `host.json`, `ip addr show br-guests` shows the
      `.1` address and `wg show` shows the interface. Evidence: pasted. —
      closed: host-network VM test subtest "repose-host-net configures
      br-guests and wg0 from host.json" prints both (EXIT=0, STATUS 2026-09-20
      01/03-host-followup done line)
- [x] `nft list table inet repose` shows `guest_fwd`, `guest_in`, `nat`,
      `guest_dyn` with the IMDS drop and the `10.64.0.0/12` drop. Evidence:
      pasted. — closed: host-network VM test asserts the five chains and both
      drops (nix/hosts/tests/default.nix); on host-01
      security/review-2026-09-21.md "Verified as deployed" (guest_fwd drop
      counters, guest_in, masquerade)
- [x] From a network namespace attached to the bridge (or a real guest once
      02 exists): `curl -m2 http://169.254.169.254` fails, `ping 10.64.x.1`
      is rate limited, `ping 10.64.other` fails, `curl https://github.com`
      succeeds. Evidence: the four commands and outputs pasted. — closed:
      host-network VM test subtest "a guest reaches the internet through NAT
      and nothing else" (namespace on the bridge); on a real guest STATUS
      2026-09-20 m1-integration done line (IMDS blocked, github reachable,
      guest-to-guest blocked) and security/review-2026-09-21.md "Verified as
      deployed" (echo limit 5/s, IMDS drop counter)
- [x] `ls /run/repose/store-export/.links` is empty. Evidence: pasted. —
      closed: security/review-2026-09-21.md "Guest cannot write the store"
      (`.links` a 4k ro tmpfs, host-01); STATUS 2026-09-20 m1-integration done
      line (.links masked); DECISIONS I-61
- [x] `systemctl status hostd` is active with the stub; `systemctl restart
      hostd` leaves a transient `guest@test` unit (created with `systemd-run
      --unit guest@test sleep infinity`) running. Evidence: pasted. — closed:
      host-services VM test (`systemd-run --unit guest@test ... sleep
      infinity`, `systemctl restart hostd`, nix/hosts/tests/default.nix),
      EXIT=0 per STATUS 2026-09-20 m1-integration follow-up (a) line
- [x] `ss -tlnp` shows nothing listening on the Azure NIC address except
      sshd during bootstrap. Evidence: pasted. — closed: host-network VM test
      subtest "sshd and node_exporter listen on wg0 only; nothing on the
      provider NIC"; on host-01 security/review-2026-09-21.md (listeners on
      10.255.0.2 only, sshd `ListenAddress 10.255.0.2:22`, M5-6)
- [x] node_exporter answers on the `wg0` address and not on the Azure NIC.
      Evidence: two curl outputs. — closed: host-network VM test (curl to
      10.255.0.7:9100 succeeds, to the provider address fails,
      nix/hosts/tests/default.nix); host-01 binding per
      security/review-2026-09-21.md "Guest cannot reach host"
- [ ] Fluent Bit ships a test line from journald to Loki and it is visible
      in Grafana with the `host` label. Evidence: screenshot or LogQL result.
      — waits on: owner (the Loki URL and the monitoring peer,
      10-observability §9); the VM test ships to a local Loki via logcli, the
      Grafana view on a real host is not done
- [x] `repose-register.service` with a real join token (a `hostdev` in a
      second test node, DECISIONS I-17) writes `host.json`, `cert.pem` and
      `key.pem` and deletes the token; running it again does nothing.
      Evidence: pasted journal and `hostdev status`. — closed: host-services
      subtest "registration consumes the join token once and is idempotent"
      against a real hostdev node, STATUS 2026-09-20 m1-integration follow-up
      (a) done line (18:11Z, token consumed once, second refused, `hostd
      status`); DECISIONS I-71, I-75
- [ ] `nix/hosts/tests/` VM tests pass in CI. Evidence: CI. — open: no CI job
      runs the host VM tests (ci.yml evaluates only); they pass on the dev
      box's KVM (STATUS 2026-09-20 01/03-host-followup and m1-integration
      follow-up (a) lines). Needs a KVM-capable CI runner or a recorded
      decision that they run locally
- [ ] `interfaces/host-conventions.md` matches every path and unit name in
      the configuration. Evidence: a grep list in the PR. — open: no grep list
      of paths and unit names against nix/hosts was recorded in any commit or
      STATUS line; needs the grep run and pasted
- [x] `ops/RUNBOOK.md` has entries for: host unregistered, pool 80 percent,
      store 80 percent, wg down, host reboot. Evidence: the entries. — closed:
      ops/RUNBOOK.md "HostUnregistered", "PoolHigh (thin pool at 80 percent)",
      "StoreHigh (store at 80 percent)", "HostWgDown", "Host rebooted"
- [x] No `TODO` in `nix/hosts/`. Evidence: `rg TODO nix/hosts` empty. —
      closed: `rg TODO nix/hosts` empty (exit 1) at d3b72d3, run by the upkeep
      audit 2026-09-23
