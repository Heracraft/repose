# Architecture

A slice of [DESIGN.md](DESIGN.md) by component; where the two disagree, DESIGN.md wins.

## The one idea

A guest is a NixOS closure that already exists in the host's store before the
guest boots. Everything else follows: guests start in seconds, a config change
is a build on the host and a switch in the guest, and "exactly your config" is
a content hash, not a promise.

## Components

| Component | Runs on | Language | Talks to |
|---|---|---|---|
| `repose` (CLI) | laptop | Go | api (HTTPS), gateway (SSH), Logto (browser) |
| `api` | Coolify VM | Go | Postgres, Logto JWKS, Polar, Key Vault, hostd (gRPC server side), Resend, ntfy |
| dashboard | Coolify VM | SvelteKit | api |
| Logto | Coolify VM | (upstream) | GitHub |
| `gateway` | edge VM | Go | api (project lookup, revocation), guests over WireGuard |
| WireGuard hub | edge VM | kernel | hosts |
| `hostd` | each host | Go | api (gRPC client), Cloud Hypervisor (REST over unix socket), LVM, nftables, nix, guestd (vsock) |
| `guestd` | each guest | Go | hostd (vsock), tmux, systemd, agent hooks (unix socket) |
| Postgres | Coolify VM | | backups to R2 |
| Blob | Azure | | snapshots |
| Key Vault | Azure | | DEK wrapping |
| Loki, Grafana, Prometheus | personal server | | Fluent Bit and scrapes from hosts and Coolify VM |

## Data flows

**Create and run.**

```
CLI ─POST /projects─▶ api ─scheduler picks host─▶ hostd.Create (gRPC)
                                                   ├─ nix build guest runner (base + fragment)
                                                   ├─ lvcreate thin volume, mkfs
                                                   ├─ start virtiofsd, tap, systemd-run cloud-hypervisor
                                                   └─ guestd ready over vsock ─▶ hostd ─▶ api ─▶ CLI
CLI ─POST /certs─▶ api (SSH CA) ─▶ cert for ~/.ssh/repose/id_ed25519
CLI ─ssh todo-app.user@ssh.repose.herakraft.co─▶ gateway ─verify cert, GET /internal/route─▶ api
        gateway ─tcp over wireguard─▶ guest:22 ─▶ sshd (trusts CA, principal = project id)
CLI ─over that ssh (one multiplexed connection)─▶ credential files, git bundle of the commits + diff + untracked tar, tmux attach or send-keys
```

**Config apply.**

```
CLI ─PUT /projects/:id/config─▶ api ─validate size, store revision─▶ hostd.Apply
   hostd: nix eval (restricted, 60s) ─▶ nix build (sandbox, 30m, 8 cores) ─▶ closure in /nix/store
   hostd ─vsock Switch(system=/nix/store/...-nixos-system)─▶ guestd ─▶ switch-to-configuration switch
   build log streamed back gRPC ─▶ api ─▶ CLI (SSE)
```

**Snapshot.**

```
hostd timer or api Stop ─▶ hostd ─vsock Freeze─▶ guestd fsfreeze -f /
   hostd lvcreate -s ─▶ guestd fsfreeze -u ─▶ dd | zstd | azcopy ─▶ Blob
   hostd ─gRPC SnapshotDone(size, path)─▶ api (snapshots row)
```

**Metering and signals.**

```
hostd every 60s: per guest {running, class, cpu_secs, mem_rss, net_bytes, disk_alloc}
guestd every 60s: {ssh_sessions, tmux_clients, agent_procs[], docker_containers, proc_samples[]}
 ─▶ hostd ─gRPC Samples─▶ api ─▶ meter_samples (Postgres) ─hourly rollup─▶ usage rows ─▶ egress overage event (Polar meter)
 ─▶ hostd /metrics (Prometheus over WireGuard) ─▶ Grafana
```

**Notifications.**

```
agent hook ─unix socket─▶ guestd ─vsock Event─▶ hostd ─gRPC Event─▶ api ─▶ events row
   api ─▶ Resend (email), ntfy POST ─▶ user's phone
   CLI status / dashboard read events
```

## Network diagram

```
                     internet
                        │
        ┌───────────────┼──────────────────────┐
        │               │                      │
   Coolify VM      edge VM (NixOS)        hosts (NixOS, no public IP)
   Ubuntu, public  public IP              10.64.0.0/12 guest space
   :443 api, web   :22  gateway  ◀──wg──  host A  br-guests 10.64.0.0/22
   :443 logto      :51820 wg hub ◀──wg──  host B  br-guests 10.64.4.0/22
        ▲               │                      │
        │ grpc/mtls     │ tcp over wg          │ vsock
        └───────────────┼──── hostd ───────────┤
                        ▼                      ▼
                    guest sshd :22          guestd
                    guest NAT egress ──▶ internet (shaped 200 Mbit/s)
                    guest ✗ other guests, ✗ host, ✗ 169.254.169.254
```

## Trust boundaries

1. **Guest ↔ host**: KVM. A guest sees a virtio-fs mount of the store
   (read-only, no `.links`, no db), a block device, a tap, a vsock, a serial
   console. The virtiofsd process runs as an unprivileged user with the store
   as its only view, and Cloud Hypervisor itself runs as the unprivileged
   `hostd` user in a systemd sandbox whose device list is `/dev/kvm`,
   `/dev/net/tun` and that guest's own volume (DECISIONS I-51), so an escape
   lands next to the hypervisor rather than as root on the host.
2. **Guest ↔ guest**: no L2 or L3 path (nftables on the bridge, per-guest
   tap, no VLAN sharing). Different users' guests may share a host.
3. **Host ↔ control plane**: mTLS with a certificate issued at registration
   and rotated monthly; hostd is authorised only for its own host id.
4. **User ↔ guest**: SSH certificate with principal = project id; sshd in
   the guest is the second check after the gateway.
5. **Operator ↔ tenant data**: root on a host can read volumes. Recorded,
   not mitigated, until per-project LUKS.

## Repository layout

See `DESIGN.md` §17. The rule: one Go module, `nix/` is one flake, `infra/`
is OpenTofu, `apps/web` is the dashboard. `docs/` is the spec.

## Why these tools

- **Cloud Hypervisor, not Firecracker**: virtio-fs. Microsoft runs CH nested
  on Azure in AKS Pod Sandboxing.
- **microvm.nix**: does the runner package, share, tap and vsock wiring for
  CH; we drive its output, not its host module.
- **LVM thin, not qcow2**: per-tenant snapshots and quotas with kernel-level
  tools, no userspace image format in the data path.
- **WireGuard, not Tailscale**: hosts are appliances; the hub is one config.
- **Go**: single static binaries for five programs, first-class SSH and gRPC
  libraries, easy `syscall` work in hostd and guestd.
- **Postgres, only Postgres**: state, outbox for notifications and metering,
  no second datastore until a measurement says so.
