# 00: Benchmark gate

> Deferred (DECISIONS I-12); nothing here was built. The paths in §2 and below (`packages/core/flake.nix`, `nix/hosts/bench.nix`, `nix/guest/bench.nix`, `scripts/bench/`) do not exist: the bench files were never written, and `packages/core` was retired in 4b1ec14. The first host records timings instead (`RESEARCH.md` §11 to §13).

Milestone: M0. Owns no interfaces. Blocks nothing except the final host SKU
decision; every other workstream starts in parallel with this one.

## 1. Goal

Measure how much a Cloud Hypervisor guest nested inside an Azure Intel VM
loses against a plain Azure VM of the same size, on CPU, disk and network,
and record the numbers so the host SKU decision rests on evidence. If the
loss is too large, hosts move to Hetzner metal and nothing else in the design
changes.

## 2. Scope: builds

- A throwaway host built by hand: one `Standard_D64s_v5` in East US, created
  with `--security-type Standard`, NixOS installed with `nixos-anywhere` from a
  minimal host config kept at `nix/hosts/bench.nix` (not the production host
  config, which workstream 01 owns).
- One microvm.nix guest configuration at `nix/guest/bench.nix`: Cloud
  Hypervisor, 4 vCPU, 8 GB, host `/nix/store` shared read-only over virtio-fs,
  a 40 GB thin volume for the writable overlay, Docker enabled, the tools list
  from `packages/core/flake.nix`.
- A control VM: one `Standard_D4s_v5` (4 vCPU, 16 GB, but memory is capped in
  the guest comparison by only running the same workloads) with the same
  NixOS system as the guest, installed with nixos-anywhere. The control is
  NixOS, not Ubuntu, so the only variable is the nesting.
- A benchmark script `scripts/bench/run.sh` that executes every workload
  below three times, discards the first run (cache warm-up), and writes a
  JSON result to `scripts/bench/results/<hostname>-<date>.json`.
- A results section in `docs/RESEARCH.md` under "Benchmark 2026-09" with the
  table and the verdict.

## 3. Scope: does not build

- The production host configuration (01-host-nixos). The bench host config
  may be copied from it later, but 01 does not wait for this workstream.
- hostd or guestd (03, 04). The guest is started by hand with the microvm.nix
  runner script.
- Any OpenTofu (11). `az` CLI commands are acceptable here and only here,
  because the machines are thrown away.

## 4. Interfaces owned / consumed

Owned: none. Consumed: none. Output is a section in `RESEARCH.md` and a
`DECISIONS.md` entry (`I-0`) recording the verdict.

## 5. Design detail

### Provisioning the host

```
az group create -n repose-bench -l eastus
az vm create -g repose-bench -n bench-host \
  --image Canonical:ubuntu-24_04-lts:server:latest \
  --size Standard_D64s_v5 --security-type Standard \
  --os-disk-size-gb 128 --admin-username azureuser \
  --ssh-key-values ~/.ssh/id_ed25519.pub --public-ip-sku Standard
az vm disk attach -g repose-bench --vm-name bench-host \
  --name bench-data --new --size-gb 512 --sku PremiumV2_LRS
```

Confirm nested virtualization before installing anything:

```
ssh azureuser@<ip> 'grep -c -E "vmx|svm" /proc/cpuinfo; ls -l /dev/kvm'
```

Both must succeed. If `/dev/kvm` is absent the VM was created with Trusted
Launch; delete it and recreate with `--security-type Standard`. Do not try to
fix it in place; Azure does not allow changing the security type.

Install NixOS:

```
nix run github:nix-community/nixos-anywhere -- \
  --flake .#bench-host --target-host azureuser@<ip> --build-on-remote
```

`nix/hosts/bench.nix` declares: kernel modules `kvm-intel`, `vhost_vsock`,
`tun`; packages `cloud-hypervisor`, `virtiofsd`, `lvm2`, `fio`, `iperf3`,
`git`, `docker`; LVM thin pool `vg-guests/thin` on `/dev/sdc` (the attached
data disk); bridge `br-guests` with `10.64.0.1/22` and masquerade; a
`bench` user with passwordless sudo.

### Starting the guest

microvm.nix with `microvm.hypervisor = "cloud-hypervisor"`, `microvm.vcpu =
4`, `microvm.mem = 8192`, `microvm.shares = [{ source = "/nix/store"; mountPoint
= "/nix/.ro-store"; tag = "ro-store"; proto = "virtiofs"; }]`,
`microvm.volumes = [{ image = "/dev/vg-guests/g-bench"; mountPoint = "/"; }]`,
`microvm.interfaces = [{ type = "tap"; id = "tap-bench"; mac = "02:00:00:00:00:01"; }]`,
`microvm.writableStoreOverlay = "/nix/.rw-store"`. Build the runner:

```
nix build .#nixosConfigurations.bench-guest.config.microvm.declaredRunner
lvcreate -V 40G -T vg-guests/thin -n g-bench
ip tuntap add tap-bench mode tap && ip link set tap-bench master br-guests up
./result/bin/microvm-run
```

Record `cloud-hypervisor --version`, the guest kernel version (`uname -r`
inside), and the host kernel version. They go in the results table.

### Workloads

Every workload runs identically in the guest and on the control VM, from the
same NixOS closure, three times, first run discarded, median of the remaining
two reported.

| Axis | Workload | Measured |
|---|---|---|
| CPU | `nix build nixpkgs#hello --rebuild` after `nix build nixpkgs#{ripgrep,jq,go}` is cached: measures a small source build. Then `go test ./...` in a checkout of `github.com/gohugoio/hugo` at a pinned tag. | wall seconds |
| CPU | `sysbench cpu --threads=4 --time=30 run` | events per second |
| Disk | `docker pull docker.io/library/postgres:16` (image pre-pulled to a local registry mirror on the host so network is not measured), then `docker rmi` and pull again. | wall seconds for extract |
| Disk | `fio --name=rw --rw=randrw --bs=4k --size=2G --numjobs=4 --time_based --runtime=30 --direct=1 --ioengine=io_uring` | read IOPS, write IOPS, p99 latency |
| Network | `git clone https://github.com/torvalds/linux --depth=1` | wall seconds, MB/s |
| Network | `iperf3 -c <control vm private ip> -t 30 -P 4` | Gbit/s |
| Memory | `sysbench memory --memory-block-size=1M --memory-total-size=32G run` | MB/s |
| Boot | time from `microvm-run` to sshd accepting a connection | seconds (guest only, for the record) |

Docker inside the guest uses the default overlay2 driver on ext4 on the thin
volume. If `docker info` shows `vfs` the guest kernel is missing overlayfs;
fix the guest config before measuring; the number would be meaningless.

### Gate

For each axis, penalty = (guest - control) / control for wall-time metrics,
or (control - guest) / control for throughput metrics. The gate passes if
every axis is under 20 percent. Boot time has no gate.

If any axis is over 20 percent:

1. Repeat the whole procedure once on `Standard_D64s_v6` (Emerald Rapids).
   If it passes, `DECISIONS.md` records the SKU change as I-0 and 11-infra
   uses v6.
2. If v6 also fails, write `DECISIONS.md` entry I-0: "Hosts move to Hetzner
   AX162-R (or Latitude c3.large.x86). Azure remains for control plane,
   Blob, Key Vault, Coolify VM. 11-infra adds a `hetzner` host module using
   the Robot API. The host conventions, hostd, and everything else are
   unchanged because hosts dial out for every connection." Then continue
   with M1 on the bench host anyway, since the software is identical.

### Recording

Append to `docs/RESEARCH.md` under a heading `## Benchmark 2026-09`:

- The SKUs, image, host kernel, guest kernel, CH version, microvm.nix rev,
  nixpkgs rev.
- One table with control, guest, penalty per axis.
- Verdict and the `DECISIONS.md` id.
- The raw JSON committed at `scripts/bench/results/`.

### Teardown

`az group delete -n repose-bench --yes`. Keep the bench host only if M1 has
already started using it (it may be re-imaged with the production host
config by 01).

## 6. Failure modes

| Failure | Outcome |
|---|---|
| `/dev/kvm` missing after install | The VM was Trusted Launch. Recreate. Documented above; the script checks and exits 2 with `no /dev/kvm: recreate the VM with --security-type Standard`. |
| microvm-run exits with `Error booting VM: ... KVM` | Nested virt unsupported on this size (AMD or ARM). Script prints the SKU and exits 3. |
| virtiofsd share not mounted in guest | `nix` inside the guest reports missing paths. Check `journalctl -u virtiofsd` on host; usually the socket path or a missing `vhost_user_fs` module. Not a benchmark result; fix and rerun. |
| Docker in guest uses vfs | Guest kernel lacks overlayfs. Fix `boot.kernelModules`, rerun. |
| Numbers inconsistent between runs (>15 percent spread) | Noisy neighbour on the Azure host. Deallocate and start again to land on another physical host, rerun. Record the spread. |

## 7. Testing

The workstream is itself a test. Verification that it was done right: the
results JSON exists for both machines, the RESEARCH.md table has every axis,
and someone other than the author can rerun `scripts/bench/run.sh` on the
same machines and land within 10 percent.

## 8. Rollback

None needed. Everything is in a throwaway resource group.

## 9. Checklist

- [ ] `nix/hosts/bench.nix` and `nix/guest/bench.nix` exist and `nix flake
      check` passes. Evidence: CI output. Superseded: DECISIONS I-12
      (benchmark deferred; no bench.nix was written,
      `nixosConfigurations.host-bench` exists in nix/flake.nix)
- [ ] The bench host was created with `--security-type Standard` and
      `/dev/kvm` exists. Evidence: the `az vm show` output showing
      `securityProfile: null` and the `ls -l /dev/kvm` line, pasted in
      RESEARCH.md. Superseded: DECISIONS I-12 (no bench host; host-01 has
      /dev/kvm and nested virt per STATUS 2026-09-20 m1-integration done line,
      RESEARCH §11)
- [ ] The guest booted with the virtio-fs shared store, confirmed by `mount |
      grep ro-store` inside the guest. Evidence: pasted output. Superseded:
      DECISIONS I-12 (no bench guest; the shared store was verified on host-01
      guests, STATUS 2026-09-20 m1-integration done line)
- [ ] Docker inside the guest reports `overlay2`. Evidence: `docker info`
      excerpt pasted. Superseded: DECISIONS I-12 (no bench guest; overlay2
      verified on host-01, STATUS 2026-09-20 m1-integration done line,
      RESEARCH §11 docker pull row)
- [ ] Every workload in the table ran three times on both machines.
      Evidence: the two results JSON files with three entries per workload.
      Superseded: DECISIONS I-12 (no comparison run; RESEARCH §11 has host-01
      absolute numbers instead)
- [ ] RESEARCH.md has the table, versions, verdict. Evidence: the section.
      Superseded: DECISIONS I-12 (RESEARCH §10 table stays empty; §11 records
      host-01 timings and versions)
- [ ] `DECISIONS.md` has entry I-0 with the verdict, even if the gate
      passed on D64s_v5 (the entry then says "no change"). Evidence: the
      entry. Superseded: DECISIONS I-12 (I-12 records the deferral in place
      of I-0)
- [ ] The resource group was deleted or handed to 01 with a note in
      `workstreams/STATUS.md`. Evidence: the STATUS line. Superseded:
      DECISIONS I-12 (no resource group was created; STATUS 2026-09-17
      00-benchmark deferred line)
