# Bring your own host (proposal, 2026-10-10)

**Status: shape A chosen (DECISIONS I-647); the rest is analysis.** The owner asked for a
sketch of how users could run repose on their own hardware, the way
Coolify does, after I-642 showed every hosted seat loses money on Azure
and on Hetzner. Any of it becomes real only through `DECISIONS.md`
entries.

## Summary

| Question | Answer |
|---|---|
| Is it possible? | Yes. Hosts already dial out for everything, register with a single-use token, and hold a certificate scoped to their own host id |
| Which shape first? | A: the hosted control plane (api, dashboard, gateway) with hosts the user owns. This is Coolify Cloud's model |
| What does a user need? | An x86_64 box with `/dev/kvm` that NixOS may take over, and a spare disk or partition for guest volumes |
| What does it cost you per user? | The edge relaying their SSH traffic, plus a share of the control plane. Their guests' internet traffic leaves through their own network |
| Biggest work items | Host ownership in the scheduler, isolating a stranger's host on the WireGuard hub, snapshots without Azure, updating hosts nobody on the team can log in to |
| Biggest product risk | NixOS takes over the disk, a much bigger ask than Coolify's "any Linux with Docker" |

## Two shapes

**A. Hosted control plane, the user's hosts.** The user signs up as
today, adds a host from the dashboard or the CLI, and runs one install
command against their box. The box registers, joins the WireGuard hub
and takes that user's projects. `repose run`, `attach`, the dashboard,
notifications and the browser view work unchanged, because they already
go through the api and the gateway. The user's existing account, plan
page and `repose ls` stay the single place they look.

**B. Everything self-hosted.** The user runs the api, dashboard,
Postgres, gateway and hostd on their own machines, with no account at
repose.herakraft.co. This is what earns GitHub stars and lets companies
run it internally, and it needs everything A needs plus the list under
"What B adds".

A first. It is a subset of B's work, it keeps users in the system you
can count, and the hosted seats stay as the plan for trying repose
without hardware.

## What a host needs

- **CPU.** x86_64 with hardware virtualization and `/dev/kvm`. Bare metal
  works with no nested penalty, Intel or AMD. A VM works only with nested
  virtualization turned on: on Proxmox or libvirt that is one setting,
  and RESEARCH.md §3 measured about 10 percent on Intel against 50 to 90
  on AMD under Azure. Most cheap cloud VPSes expose no `/dev/kvm`
  (Hetzner Cloud among them, *unverified*), so a VPS is usually out.
  ARM is out: the guest base is x86_64.
- **OS.** The box becomes NixOS. `nixos-anywhere` installs over SSH and
  wipes the disks it is given. A homelab user with Proxmox can give
  repose a VM instead of a whole machine.
- **Disk.** One device for `vg-guests` (`repose.host.dataDevice`), either
  a second disk or a partition. The disko layout today expects two
  devices (`nix/hosts/disko-layout.nix`); a one-disk layout is new.
- **Memory.** 16 GB and up. The host reserve is 8 GB on host-01, so
  a 32 GB box holds three `large` guests.
- **Network.** Outbound UDP 51820 to the edge and TCP 443 to the api. No
  inbound port and no public IP, which suits a home network behind NAT.

## What already exists

- `repose.host.provider` (`nix/hosts/default.nix:37`), with Azure's
  specifics behind it in `nix/hosts/azure.nix`: waagent, cloud-init, the
  Hyper-V initrd modules. A second provider was planned from the start
  (R3-20).
- Registration: `hosts add` in the admin CLI mints a single-use join
  token, and `hostd register` (`nix/hosts/registration.nix`) trades it
  for `host.json` and an mTLS certificate. hostd is authorised for its
  own host id only (ARCHITECTURE.md, trust boundary 3).
- Hosts dial out to the WireGuard hub and the api; nothing connects in.
- `snapshot.Blob` (`internal/hostd/snapshot/blob.go`) and
  `secrets.KeyVault` (`internal/api/secrets/secrets.go`) are interfaces,
  with a file backend and an in-memory fake beside the Azure ones.
- `hostinfo` reads the Azure SKU from IMDS and returns empty when IMDS is
  absent, so a bare-metal host already reports cleanly.
- The overlay binary cache is Cachix (`repose.cachix.org`, I-46), so a
  host anywhere can pull the guest base without touching Azure.

## Work for shape A, in order

1. **A generic `metal` provider.** `nix/hosts/kernel.nix` loads
   `kvm-intel` only; load `kvm-amd` too and let the kernel take the one
   that fits. Without cloud-init, the join token arrives with the
   installer over SSH, which the `bootstrap` option (I-92) already allows
   for operator keys. Add a one-disk disko layout with a partition for
   `vg-guests`.
2. **An install command.** One command from the laptop that mints the
   token and runs `nixos-anywhere` against `root@BOX` with a flake output
   that takes the token, the disks and the edge address. Where it goes
   in the CLI is a placement question for the owner: a `host` noun next to `project` nouns, or
   under `config`. The dashboard shows the token for users who install
   by hand.
3. **Host ownership.** A host gets an owning account. The scheduler
   (`internal/api/scheduler`) places an owner's projects on their own
   hosts and never places anyone else's project there. Getting this
   wrong puts one user's code on another user's machine, so it needs its
   own tests and a line in `docs/SECURITY.md`. Owned hosts are left out
   of seats and the waitlist, and their memory does not count against
   the plan.
4. **A stranger's host on the hub.** Hosts today are the operator's own
   appliances. An owned host is root-controlled by someone you have
   never met, sitting on your WireGuard network. The hub's nftables must
   drop host-to-host traffic and everything from a host except the ports
   it needs. The gateway may route a connection to a host only for that
   host's owner's projects. Every hostd RPC that names a project needs an
   audit that the project lives on the calling host. Security review
   before any public host joins.
5. **Snapshots without Azure.** hostd reaches Blob with the host VM's
   managed identity, which an owned host does not have. Add an
   S3-compatible `Blob` (R2, Backblaze B2, Hetzner Object Storage, MinIO)
   with the user's bucket, and the existing `FileBlob` on the host as the
   fallback (a dead disk then loses the snapshots too). Storing the
   bucket's keys on the host is a new kind of credential, and CLAUDE.md
   allows three homes for secrets, so it needs a decision first.
6. **Updates nobody runs by hand.** Today the conductor switches host-01
   when the owner asks. Owned hosts need to pull: a timer that fetches a
   published host system from Cachix and switches to it, the way guests
   take a new base. hostd already reconciles from systemd, LVM and bbolt
   at start, which is what makes an unattended switch safe. The api then
   has to accept the previous hostd release for a window.
7. **The plan.** Either free with an account, or a small price per host
   (Coolify Cloud charges for each server it manages). At $5 a month,
   Polar keeps 75 cents of it.
8. **Docs and decisions.** A public page on adding a host, with the
   requirements above; entries for the provider, ownership, the hub rules,
   the snapshot store and the plan.

## What it costs you per user

- **The edge.** Every `repose` SSH connection, sync and attach goes
  laptop to edge to host and back, and Azure bills edge egress at about
  $0.087 a GB. Nobody has measured the gateway's bytes per user yet;
  measure that before setting a price. A later option is a direct path
  when the host has a public address.
- **The control plane.** Fixed, shared, and already paid.
- **Cachix.** Hosts pull the base from it; check its plan's limits
  against the number of hosts.
- **Guest traffic and abuse.** Both leave from the user's own network,
  so the $0.05 a GB egress overage and the miner rules matter less there.
  An abusive guest gets the user's IP blocked, not yours.

## What B adds

- **Login.** The api requires Logto (`LOGTO_ISSUER`,
  `internal/api/app/config.go:145`). A self-hosted install needs Logto in
  the box, or a GitHub OAuth app straight to the api.
- **Key wrapping.** `KEYVAULT_URL` is required outside dev mode
  (`config.go:142`). Add a `KeyVault` that wraps keys with a KEK file on
  the control-plane box.
- **Billing off.** Polar, plans and the waitlist turn off as a unit.
- **The CLI's address.** The CLI needs a configured api URL and gateway
  host instead of repose.herakraft.co.
- **One-box install.** A NixOS module with Postgres, the api, the
  dashboard, the gateway, the WireGuard hub and hostd on one machine,
  plus DNS and TLS for it.
- **Support.** Strangers running your control plane find every
  assumption the hosted one hides.

## Risks

- NixOS taking over the disk narrows the audience to people with a spare
  box, a homelab or rented metal. Proxmox and libvirt VMs widen it again.
- When a user's host is offline, their projects are unavailable. The
  dashboard and `repose ls` need to say so plainly.
- A malicious host can lie about its guests and attack the hub. Items 3
  and 4 above are the defence, and they come before any public host.
- Every hostd release has to work on hardware nobody tested: other NICs,
  other NVMe controllers, AMD.

## Suggested order

1. Turn host-01's successor into a `metal` host on rented Hetzner metal or
   an auction box. That proves items 1 and 2 on your own hardware, with no
   stranger involved.
2. Ownership and the hub rules (items 3 and 4), then one trusted user's
   box.
3. The S3 snapshot store and pulled updates (items 5 and 6).
4. A public page and the plan (items 7 and 8).
5. Shape B, once A has users.

## Decisions for the owner

1. A first, B later, or the other way round.
2. Free, or a price per host.
3. Where the install command lives in the CLI.
4. Whether the user's bucket keys on the host are an allowed home for a
   secret.
