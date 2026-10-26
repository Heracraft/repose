# Workstream 11: infrastructure (OpenTofu)

> **Host size is a variable, and a host's shape is per host (DECISIONS I-14
> and I-39, amended by I-786).** `host_size` defaults to `Standard_D16s_v7`
> and `host_data_disk_gb` to 512. Those are **fleet** defaults: raising them
> to reach a launch shape resizes every host that already exists, which
> deallocates it and stops every guest on it. The launch host's
> `Standard_D64s_v7` and 2048 GB go in its own `host_shapes` entry instead,
> and a plan that changes an existing host is the signal that this was done
> the other way (I-39: this subscription can only create v7 sizes).
> Validation rejects any size whose name is not
> `Standard_D[0-9]+(l|d|ld)?s_v[567]` so an AMD or ARM size cannot be applied
> by mistake. The Coolify VM is `Standard_D4s_v7`, the edge `Standard_D2s_v7`.
> Every production host is its own `nixosConfigurations.host-<name>`
> attribute selected by `host_flake_attrs` (DECISIONS I-40).

> **State backend (created 2026-09-19):** resource group `repose-prod`,
> storage account `reposetfstate3912`, container `tfstate`, key
> `azure.tfstate`, `use_azuread_auth = true`. Subscription
> `5f27aace-dd8c-4dc0-95bf-b59ee8de7d70`, tenant
> `3d6e97f4-af27-4cea-8bf3-f42718e67189`, region `eastus`.

## 1. Goal

Every cloud resource the platform needs is declared in `infra/` and created
by `tofu apply`, so a new host, a replacement edge, or a rebuilt control-plane
VM is a reviewed diff rather than a remembered sequence of portal clicks. The
Azure-specific surface is kept to compute and storage so that a second
provider module for hosts is an addition, not a rewrite.

## 2. Scope: builds

- `infra/azure/`: one root module per environment (`prod/`, `staging/`)
  composing these modules:
  - `network`: VNet `10.200.0.0/16` in the existing resource group (the
    group is created by `infra/bootstrap`, not here, DECISIONS I-19), subnet
    `hosts` (`10.200.1.0/24`) with a NAT gateway and an NSG carrying **no
    security rules at all** (Azure's built-in `AllowVnetInBound` lets the
    edge's jump connection through, so an explicit rule here is always a
    mistake), subnet `edge` (`10.200.2.0/24`) allowing inbound 22,
    443, 51820/udp and the operator SSH port, subnet `control`
    (`10.200.3.0/24`) allowing inbound 80, 443 and 22 from configured
    address lists.
  - `host`: one VM per instance at `host_size`, `security_type =
    "Standard"` (Trusted Launch disables nested virtualization), Ubuntu
    24.04 marketplace image as the nixos-anywhere target, no public IP, one
    Premium SSD v2 data disk at `host_data_disk_gb` (16k IOPS, 600 MB/s),
    cloud-init that opens root SSH for the provisioner and carries nothing
    secret, a provisioner that runs `nixos-anywhere --flake
    .#<host flake attr> root@<private ip>` through the edge as a jump host,
    under a coreutils `timeout` of `install_deadline` and always with
    `--kexec`, pointing at the image this flake builds,
    a post-install check that `/dev/kvm` exists and `kvm_intel` nested is
    `Y`, and a final provisioner that writes the join token to
    `/run/repose/join-token` as file content and restarts `hostd`
    (DECISIONS I-20: cloud-init cannot deliver it, because nixos-anywhere
    replaces the system that ran cloud-init). Tag `repose:role=host`.
  - `edge`: one VM at `edge_size` (`Standard_D2s_v7`; DECISIONS I-23 chose
    the non-burstable shape, I-39 the v7 family), static public IP with
    `prevent_destroy`, DNS A record
    `ssh.repose.herakraft.co`, Ubuntu image, nixos-anywhere provisioner
    with `.#edge`. Its subnet NSG opens 22 (user gateway), 443 (preview
    stub) and 51820/udp (WireGuard hub) to the internet, and the operator
    sshd port 2222 to the operator address list and the VNet, which is the
    port list workstream 06 §5.1 owns. Operator SSH after install is by
    certificate only, on 2222, because 22 belongs to the gateway and every
    host provisioner jumps through it.
  - `coolify`: `coolify_count` of them; the module default is **0** so a new
    environment costs nothing, and production runs **1** from 2026-09-20
    (DECISIONS I-24, I-71). One `Standard_D4s_v7`, Ubuntu 24.04 LTS, static
    public IP, DNS A records `repose.herakraft.co` and
    `api.repose.herakraft.co` (Logto is the owner's `accounts.herakraft.co`,
    DECISIONS I-84), 256 GB Premium SSD
    OS disk. It is a server of the owner's existing Coolify instance, not a
    Coolify install of its own (DECISIONS I-83): cloud-init installs Docker
    from Docker's apt repository and Tailscale (not joined; the instance
    reaches the VM over the tailnet, I-86), puts the instance's public key
    (`coolify_public_key`) on root next to the operator keys, and
    installs a WireGuard peer config so Prometheus and the api can reach
    the edge network. Its NSG allows 80 and 443 from `control_web_cidrs`,
    and 22 from `operator_cidrs` plus `coolify_manager_cidrs` (the
    instance's address); a `postcondition` on the control NSG fails the
    plan if a rule for 8000, 6001, 6002 or `*` is ever added, since no
    Coolify dashboard exists on this VM. `terraform_data.ready` holds the
    apply open until root login by the instance's key, Docker with the
    compose plugin, `rclone` and `pg_restore` are all in place. The click
    path past the VM, starting with adding the server, is
    `docs/ops/coolify.md`.
  - `storage`: storage account (LRS, hot), container `repose-snapshots`
    with a lifecycle rule moving blobs older than 7 days to cool and
    deleting blobs older than 45 days (the 30-day post-destroy window plus
    slack; the api deletes on schedule and this is the backstop). A
    user-assigned managed identity for hosts with `Storage Blob Data
    Contributor` scoped to that container, and the same role on the same
    container for the api's service principal once
    `api_identity_object_id` is supplied, since the api's expiry job
    deletes blobs (DECISIONS I-131).
  - `keyvault`: Key Vault with purge protection, one RSA-3072 key
    `repose-dek-wrap` with rotation policy 12 months, and an access policy
    for the api's identity (get, wrap and unwrap; get on a key is the
    public half only, DECISIONS I-91) created when
    `api_identity_object_id` is supplied. The api on the control VM
    authenticates with a client certificate stored in Coolify's secret
    store, since the control VM is not an Azure identity target for
    containers; the app registration and that certificate are a human step
    (DECISIONS I-21).
- `infra/dns/`: Cloudflare zone records for `herakraft.co` subdomains used
  above, so DNS is in the same apply as the addresses it points at.
  `manage_dns` defaults to **false**, because the records need a
  `CLOUDFLARE_API_TOKEN` and a zone id that do not exist yet (DECISIONS
  I-72). While it is false the names are not merely absent: `herakraft.co`
  answers every name under it from a proxied wildcard, so
  `ssh.repose.herakraft.co` resolves to Cloudflare's proxy, which carries
  neither SSH nor WireGuard. A plan-time `check` warns, and `infra/README.md`
  carries the four records to create by hand until the token exists.
- State backend: the Azure Storage container `tfstate` in `repose-prod`,
  created by hand on 2026-09-19 and read but never managed by the
  environment roots (DECISIONS I-19). `infra/bootstrap/` is the tiny root
  module, applied with local state, that declares the same shape for a new
  environment; `make bootstrap ENV=staging` creates `repose-staging` and
  keeps its state in the same account under a different key. Locking via
  blob leases.
- `infra/README.md`: how to bootstrap, how to add a host, how to rotate the
  join token, how to destroy a host safely (drain first).
- A `Makefile` target per root: `make plan ENV=prod`, `make apply ENV=prod`.

## 3. Scope: does not build

- The NixOS configurations themselves (`nix/hosts`, `nix/edge`): workstream
  01 and 06. This workstream only invokes them.
- Coolify application definitions (api, dashboard, Logto): workstream 05 and
  08 document theirs; Coolify holds them in its own database, and a
  `docs/ops/coolify.md` note records the click path until Coolify's API is
  used.
- Hetzner module, or a host in a second region: not built, and not only
  unbuilt. §5 "The portability wall" is what has to be decided first; a
  host outside this VNet cannot reach the api to register at all.
- Automatic capacity (api-driven `tofu apply`): later, per R3-5.

## 4. Interfaces

Owns: variable names and outputs of each module, the join-token handoff
(`/run/repose/join-token`), the tag scheme, DNS names.

Consumes: `interfaces/host-conventions.md` (what the host expects at boot),
`interfaces/grpc-hostd.md` (Register uses the join token). For M1 the token
is minted by `hostdev init`, the one-host dev driver in workstream 03
(DECISIONS I-17), not by `repose-admin`; workstream 05 is not on the path
between this workstream and a registered host.

## 5. Design detail

### Adding a host

```
hostdev init --host host-03            # M1: mints the join token and prints it
# once the api exists: repose-admin hosts add --name host-03 --provider azure
# nothing to do about the installer image: the apply builds it, ahead of any VM
# write nix/hosts/host-03.nix (it imports nix/hosts/prod.nix and adds only
#   what host-03 measured for itself) and nixosConfigurations.host-03
# add "host-03" to hosts and host-03 = "host-03" to host_flake_attrs,
#   in azure/prod/prod.tfvars, in a reviewed commit
# add host-03's shape to host_shapes there when it is not the fleet's size
# add host-03 = "<token>" to join_tokens in prod.local.tfvars
make -C infra plan ENV=prod && make -C infra apply ENV=prod
```

The nix file and its flake attribute must be **committed in the checkout
`flake_path` names**, which is not the worktree the apply is launched from.
The installer passes `--flake <flake_path>#<attr>`, and `flake_path` is a
bare path inside a git repository, which nix resolves as a git flakeref: it
reads what is committed there and nothing else. So a file written but not
committed is invisible, and a branch nobody merged is invisible. The two ways
that surfaces are not alike: a missing installer stops `tofu plan` on the
preflight's `flake_path` precondition, which names the path and the likely
cause, while a missing host attribute is caught only by a warning and so
fails during the apply, inside nix, as `does not provide attribute ... Did
you mean host-01?`.

Without a `host_flake_attrs` entry the host installs the generic `host`
attribute, whose `apiAddr` is not absent but wrong and plausible: it defaults
to `api.repose.herakraft.co:443`, where nothing gRPC answers, because
api-grpc's mTLS listeners are 8443 and 8444 and `manage_dns = false` makes
that name Cloudflare's proxied wildcard. The apply succeeds and the VM is
healthy; it simply never registers (`docs/ops/RUNBOOK.md`
"HostUnregistered"), which is why `every_host_has_a_flake_attr` exists at
all.

It warns rather than fails, and that is deliberate rather than an oversight:
`host_flake_attr_default` is a supported value, and a precondition that
aborted the plan would take the live host down with it over a stray key
(DECISIONS I-786). It is the same blast-radius argument as `install_deadline`
pointed the other way -- a guard that can abort a plan blocks every emergency
apply there is -- so a stray key must not be able to do what the 16h56m lock
did. The consequence to know: once the installer is on `main` the preflight's
precondition passes permanently, and from then on this warning is the only
guard on a missing host attribute. Nothing stops that apply, by design.

**Sizes are per host.** `host_shapes` is a map keyed by host name whose
attributes (`size`, `class`, `zone`, `data_disk_gb`, `data_disk_iops`,
`data_disk_mbps`, `os_disk_gb`) each override the fleet-wide variable of that
name for that host; what an entry leaves out, and every host with no entry,
takes the fleet value. That is what lets one apply add a host of another size
while the plan shows no change for the hosts already carrying guests, which
is how host-02 (`Standard_D64s_v7`, 2048 GB) joined host-01 (`D16s_v7`, 512
GB) with no downtime (DECISIONS I-786, amending I-14 and I-39;
`docs/ops/LAUNCH.md` §3). A fleet-wide value changed instead plans a change
on every host, which is the resize I-786 was written about.

The token can also be passed inline as `-var 'join_tokens={"host-03":"..."}'`,
but then the next apply proposes to remove the delivery step. Leaving the
spent token in the local tfvars file is inert (single use, 24-hour expiry)
and keeps later plans quiet; you rotate a token by replacing its value.

The apply creates the VM, attaches the disk, runs nixos-anywhere with the
disko layout from `nix/hosts/disko.nix` (root on the OS disk, `vg-guests`
on the data disk), reboots into NixOS, and hostd registers on first boot.
The token is single-use and expires in 24 hours. `repose-admin hosts list`
shows the host as `ready` within two minutes of the reboot or the runbook
entry "Host never registered" applies.

### The installer image

Every machine here is installed from a kexec image this repository builds
(`packages.x86_64-linux.kexec-installer`, from
`nixosConfigurations.installer`), always passed to nixos-anywhere as
`--kexec`. Before that, nixos-anywhere passed no `--kexec` and the **target**
downloaded a fixed upstream asset: nixos-images release `nixos-25.05`,
`nixos-kexec-installer-noninteractive-x86_64-linux.tar.gz`, Linux 6.14.10.
The same bytes installed host-01 (2026-09-20), host-stg-01 (2026-10-11) and
host-02 (2026-10-26); neither this flake nor its pinned 6.18.57 kernel took
any part in those installs.

The defect that forced our own image, still present in upstream HEAD: the
image's `restore-network.service` writes one `00-<ifname>.network` per
non-loopback interface, matching by `MACAddress=` with `DHCP=yes`. On Azure
accelerated networking the synthetic NIC (`hv_netvsc`) and the SR-IOV VF
(`mana`) **share a MAC**, so every generated unit matches both links and the
installer configures and DHCPs the VF as though it were a second NIC.
Microsoft's rule is that addresses belong to the synthetic interface alone.
Missing modules were not the cause: `mana`, `hv_netvsc` and `pci-hyperv` are
all in that image's stage-2 module tree.

Ours is the installed hosts' own answer, moved earlier. `nix/hosts/network.nix`
matches the uplink by **name** and sets `networking.useDHCP = false` so
nixpkgs' catch-all `99-ethernet-default-dhcp` is never generated, which is
why the VF matches no unit on a running host and networkd leaves it alone.
The image does the same without knowing an interface name: `restore-network`
off, a `05-vf-unmanaged` unit matching `Driver = "mana mlx5_core mlx4_core"`
with `Unmanaged = true`, a `10-uplink` matching `Driver = "hv_netvsc"` with
DHCP, the host's own 6.18.57 kernel, and `boot.consoleLogLevel = 7`. The last
of those is the one an operator notices: the upstream image boots at
`loglevel=4`, so no netvsc, mana or DHCP message reaches the serial console,
and host-02's failed install and a successful one emitted an identical set of
134 systemd messages. There was no console evidence to read.

**Why unset means ours.** `kexec_image` defaults to null and null selects
the image this flake builds; the upstream asset has no value that reaches it,
and somebody who wants it passes its URL, which shows up in a diff. The
alternative was considered and rejected: null meaning the upstream asset
would have made prod's default the defect, because `kexec_image` has no value
in `prod.tfvars`, so the next apply that installed a machine would have
kexeced into the image that hung host-02 with nothing in the way but a
plan-time warning, and `TF_VAR_kexec_image=` typed on an apply line is in no
file, no diff and no review. The asymmetry decides it: null meaning ours with
a broken flake fails loudly, at build time, naming the flake, with nothing
created, while null meaning upstream and one forgotten variable is a silent
install into a known-broken image, up to `install_deadline` of hang, and a VM
to clean up. Loud and rare beats silent and likely.

The loud failure lands at apply time, not plan time, and that cost was taken
knowingly: nothing OpenTofu can do at plan time runs `nix`, so `tofu plan`
cannot tell you the image builds. The apply can, and does so before creating
anything.

How it reaches a machine, in two steps that build the same thing. The
environment module builds the attribute as a preflight before any VM exists
(`terraform_data.kexec_image`, triggered by `flake.nix`, `flake.lock`,
`nix/installer/**` and `kexec_flake_attr`, with a precondition that
`flake_path` holds both `flake.nix` and `installer/default.nix` -- a
filesystem check, which passes for a file that exists but is untracked,
where the build a moment later reads only what git tracks), and
**both** `module.host` and
`module.edge` `depends_on` it, so a flake that does not build costs no VM at
all. Each install then builds the same attribute itself and passes the
`.tar.gz` inside the result. The preflight's job is the early failure, not
the delivery: a wiped `.terraform` costs a rebuild and nothing more, because
no install trusts the preflight's record of a path. It keeps a link rather
than using `--no-link` because its triggers mean it does not re-run on an
apply that changed nothing, and an unrooted path collected in between would
leave it reporting success for an image that is gone. `kexec_flake_attr`
(default `kexec-installer`) is plumbed prod -> environment -> host and edge ->
nixos_anywhere from one variable, so the preflight and the installs cannot
name different attributes and build different images.

Both builds are bounded, each by `install_deadline` and each with its own
message. That closed the last unbounded path on this resource: a warm store
makes the build seconds, but a substituter that accepts a connection and then
stops answering has no deadline of its own, and it would have held the state
lock exactly the way the install did. The build's message says that nothing
was installed and no machine was touched, so it reads differently from the
install hitting the same deadline. Each install links its build with
`--out-link` into a temp directory a trap removes, rather than `--no-link`:
the image is half a gigabyte with no GC root of its own, and a concurrent
`nix-collect-garbage` between the build and the upload would take it.

The image is a local path, so nixos-anywhere **uploads it through the edge's
ProxyJump**: 526 MB per install (526,497,334 bytes), where the old flow had
the target fetch 366 MB from GitHub itself (365,921,544 bytes) and the edge
carry none of it. Both are measured and both are decimal MB, so they compare
directly. That is a real regression on the most fragile link in the path.
`kexec_image` also takes a URL, and a URL is handed to the target to fetch
directly, so publishing the image to the snapshots account is a tfvars value
rather than a code change. It is not built: the container, its read access
and the upload step are the follow-up that removes the upload. Whoever does
it meets the plan-time validation that the value end in `.tar.gz`,
`.tar.xz`, `.tar.zst` or `.tar`,
which exists because nixos-anywhere picks its decompression by matching the
end of the value: a URL with a SAS token appended matches nothing and fails
on the target after the whole image has crossed the wire.

`kexec_image` is in no machine's replace triggers, on the hosts or on the
edge, so changing the image never reinstalls a machine that exists. The edge
is wired for it on the same terms -- same VM shape, same shared-MAC VF, so
the same latent defect -- and has simply not been reinstalled since that
started mattering. It inherits the fix the day it is next reinstalled, not
before.

### The install deadline

`install_deadline`, default `45m`, wraps three things in coreutils `timeout
--kill-after=1m`, each with its own message: the preflight image build, the
install's own build of the same attribute, and the nixos-anywhere run. All
three exit 124, or 137 when the follow-up KILL was needed. The install's
message re-emits the deadline and the two diagnostics in `infra/README.md`
"Watching an install"; a build's says that nothing was installed and no
machine was touched, because the two send an operator to different places. A
deadline reached with the installer alive is a stuck install to go and look
at, and only an install that was genuinely still working wants a longer
deadline; a build that ran out of time is a substituter that stopped
answering or a cold store with no cache, and no machine is involved.

It is a wrapper because nothing else can bound that step. OpenTofu rejects a
`timeout` argument on `local-exec` (`tofu 1.13.1 validate`: `An argument named
"timeout" is not expected here`), and `connect_timeout` is wired only into
the two `connection{}` blocks, so it bounds the SSH handshakes on the steps
either side of the install and not the install. Inside, nixos-anywhere waits
with `until runSsh -o ConnectTimeout=10 -- exit 0; do sleep 5; done`: an
infinite loop with no flag that stops it.

What that cost once: host-02's first install waited 16 hours 56 minutes, and
held prod's single state lock for all of it, so no emergency apply against
the edge, the control plane, DNS or the vaults could have run in that window.
The deadline is there for the lock as much as for the install.

### How the installer reaches a host

Through the edge. `nixos-anywhere`, the post-install `/dev/kvm` and nested
checks, and the join-token delivery all connect to the host's private address
with the edge as an SSH jump host, and the module graph makes a host depend on
the edge being installed first. Hosts never get a public IP, not even a
temporary one during the install: that would need an inbound rule on the hosts
subnet, which is the one thing the tfsec policy forbids and §4 and §7 of
`DESIGN.md` promise never exists, and the window is about fifteen minutes of a
stock Ubuntu image accepting root SSH. DECISIONS I-25 has the full reasoning.

The edge's operator sshd must be listening on `edge_operator_ssh_port` for
that to work. It defaults to 2222 because 22 belongs to the user gateway;
the wave-one edge served sshd on 22 and the first apply set
`edge_operator_ssh_port = 22`, and the M2 edge deploy moved it to 2222
(DECISIONS I-92).

### Why Ubuntu as the install target

Azure has no NixOS marketplace image and building one adds an image
pipeline. nixos-anywhere replaces the running Ubuntu with NixOS over SSH
using kexec, which takes about four minutes once the installer image is on
the target and needs nothing but a root login. The Ubuntu image is a
delivery vehicle; nothing from it survives.

### Provider portability

The `host` module has a provider-neutral variable surface: `name`,
`join_token`, `class` (`azure-d16s-v7` on host-01 and `azure-d64s-v7` on
host-02, each from that host's `host_shapes` entry), `data_disk_gb`. Its
outputs are `private_ip` and `ssh_jump`. A `hetzner_host` module with the same
variables and outputs, using the Hetzner Robot provider for an AX162-R and
the same nixos-anywhere provisioner, is one change to the root module's
`for_each`. The NixOS host config already takes its guest CIDR
and WireGuard keys from `Register`, so it does not care which provider it
runs on. Blob as the snapshot target is behind `hostd`'s
`SnapshotStore` interface (Azure Blob and S3-compatible implementations),
so a Hetzner host uses Hetzner Object Storage or R2.

That is the easy half, and it is the half that is finished. The hard half is
below, and until it is answered a host outside this VNet cannot register at
all, so the module would install a machine that never joins the fleet.

### The portability wall

**A host that is not in the home VNet cannot register.** `repose.host.apiAddr`
is `10.200.3.4:8443`, the control VM's private address inside
`10.200.0.0/16`, and `Register` is the first thing hostd does: the WireGuard
tunnel that would otherwise carry it does not exist until `Register` returns
the host's keys and the edge's endpoint (DECISIONS I-92,
`interfaces/grpc-hostd.md`). A host in another region, or at another
provider, has no route to that address and nothing to build one with. The
bootstrap is circular, and no amount of OpenTofu fixes it.

This is the line to read R3-20 against. R3-20 says the host module is written
so that Hetzner is a second module, and that is true of the module; it was
never a claim that a Hetzner host can join this fleet. DECISIONS I-92 records
the ordering that makes it false, and I-642 has already walked the shortcut
and refused it for the staging host: `nsg-control` opens 80, 443 and 22 and
no 8443, and a rule opening the api to an outside address is not the end of
it, because `Register`'s reply makes that machine a WireGuard peer on this
edge with a route for its guest CIDR. Reachability is the first problem, not
the only one.

So a second provider is not a module away. The module is the small part.
What has to be answered first, beginning with the wall itself:

1. **The registration path.** The options are an api gRPC listener reachable
   from the internet (its own name, its own certificate from the platform CA,
   and a join token that is now an internet-facing credential with the rate
   limiting and abuse surface that implies), a tunnel established before
   `Register` from material delivered with the join token, or a registration
   broker on the edge, which already has a public address and already
   terminates WireGuard for every host. Nothing here is built and nothing is
   decided.
2. **The install path.** nixos-anywhere, the `/dev/kvm` and nested checks and
   the join-token delivery all reach a host at its private address with the
   edge as a jump host, and hosts deliberately have no public IP (DECISIONS
   I-25). A machine in Hetzner's network is not behind the edge, so the
   provisioner chain needs its own answer for how the operator reaches it and
   how the token gets there.
3. **The installer image.** `nix/installer` is Azure-shaped on purpose: it
   tells the synthetic NIC and the SR-IOV VF apart by the Hyper-V and MANA
   drivers, and with `restore-network` off a target whose address is static
   rather than from DHCP comes up unreachable. Bare metal needs its own
   image, which is the same work again.
4. **The host's Azure identities.** The guest freeze unwraps each image key
   with a user-assigned managed identity against Key Vault, and snapshots
   authenticate to Blob with the same identity (DECISIONS I-653, I-131).
   Neither exists off Azure. Both become a credential delivered the way the
   join token is, or another key store and another snapshot target.
5. **The install diagnostics.** The two things that tell an operator whether
   an install is alive, the serial console and the per-minute byte counters
   (`infra/README.md` "Watching an install"), are Azure VM APIs. Both work on
   a machine with no usable address, which is exactly when they are needed, and
   a host elsewhere has neither until something equivalent is found. On bare
   metal that is an IPMI serial console and a switch port counter, which is
   not nothing, but it is not `az` either.

`proposals/2026-09-26-subscription-paddle-hetzner.md` priced Hetzner hosts
and the plans are set with them in mind. Prices are not a plan: nobody should
schedule a Hetzner host, or a second region, on the strength of the paragraph
above this one.

### Cost table (the 2026-09-19 estimate, East US, on-demand)

| Resource | Monthly |
|---|---|
| Host `D64s_v5` | ~$2,240 (1-year reserved ~$1,380) |
| Host data disk, Premium SSD v2, 2 TB, 16k IOPS | ~$200 |
| NAT gateway plus egress | ~$35 plus $0.045 per GB egress beyond the first 100 GB |
| Edge `D2s_v5` plus static IP | ~$75 |
| Coolify VM `D4s_v5` plus 256 GB disk plus IP | ~$180 |
| Blob, 1 TB snapshots cool tier | ~$15 |
| Key Vault | under $5 |
| R2, 50 GB | under $1 |
| Total with one host | ~$2,700 |

`infra/README.md` "Cost" carries the figures, re-derived from the Azure
Retail Prices API for what `prod.tfvars` actually creates: about **$1,332** a
month for one host and the control plane, about **$4,751** for both hosts and
the control plane, and about **$3,812** once host-01 is retired. The $10k
credit funds roughly two months of two hosts, and reservations generally
cannot be bought with sponsorship credits.
Most of the difference from the estimate above is Premium SSD v2 provisioned
IOPS and throughput, which are variables and can be raised in place later.

## 6. Failure modes

| Failure | Outcome |
|---|---|
| Install never finishes | the target booted the installer and never got a usable address; `install_deadline` (45m) stops the step at `timeout` exit 124 rather than holding the environment's state lock indefinitely, and the step prints the two read-only checks (`infra/README.md` "Watching an install"). Nothing bounded this before 2026-10-26, when one install held prod's lock for 16h56m. |
| Installer image will not build | the preflight `terraform_data.kexec_image` fails before any VM is created, naming the flake; both the edge and the hosts are ordered behind it, so a broken image costs no machine. |
| Installer image build hangs | a substituter that accepts a connection and then stops answering, or a cold store with no cache; the preflight's build and each install's build are separately wrapped in `install_deadline`, and that message says no machine was touched, which is what distinguishes it from the install hitting the same deadline. |
| nixos-anywhere fails mid-install | the VM is left on Ubuntu or half-kexec'd; `tofu apply` reports the provisioner error; re-running the apply re-taints the host and retries from the image (the data disk is preserved because it is a separate resource and `prevent_destroy = true`). |
| Join token expired before first boot | hostd logs `register_fail reason=token_expired` every 30 s; runbook "Host never registered": mint a new token, write it to `/run/repose/join-token` over the edge jump, restart hostd. |
| `security_type` left at the default | the VM boots but `/dev/kvm` is absent; hostd refuses to start with `kvm_missing`. The variable has no default so the plan fails without an explicit value. |
| Data disk detached or lost | hostd fails `pool_missing` at start and marks every guest on the host `error`; recovery is restore from Blob onto another host (runbook "Host loss"). `prevent_destroy` and a `lifecycle.ignore_changes` on the disk attachment guard the common mistakes. |
| State lock stuck | `tofu force-unlock` after confirming no apply is running; documented in `infra/README.md`. |
| DNS record points at a replaced IP | static IPs are separate resources with `prevent_destroy`, so a VM replacement keeps its address. |

## 7. Testing

- `tofu validate` and `tofu plan` in CI against a `staging` root with
  `hosts=[]` (no cost) on every change to `infra/`.
- A monthly exercise on staging: create one host, let it register, destroy
  it. Time and any manual steps recorded in `infra/README.md`.
- `tfsec` in CI for the NSG rules: `infra/policy/tfsec/repose_tfchecks.yaml`
  (tfsec only loads custom checks from files whose names end
  `_tfchecks.yaml`). REPOSE-NET-001 fails the build on any security rule on
  `nsg-hosts`; REPOSE-VM-001 and REPOSE-VM-002 fail it on any VM with Secure
  Boot or a vTPM, which is how a "hardened" host with no `/dev/kvm` gets
  caught. `make -C infra check` runs fmt, validate and these.
- Manual once: confirm `/dev/kvm` exists and `cat /sys/module/kvm_intel/parameters/nested` is `Y` on a freshly applied host.

## 8. Rollback

`tofu apply` with the previous variable values. Hosts are never destroyed
by a rollback unless the host list shrinks, and shrinking requires the host
to be `retired` in the api first (a `precondition` reads
`repose-admin hosts list --json`). State is versioned in the storage
container with soft delete enabled, so a corrupted state file is restored
from the previous version.

## 9. Checklist

Evidence dated 2026-09-20, from the applied production environment unless a
row says otherwise. Commands were run from the dev box, which is in
`operator_cidrs`.

- [x] `infra/bootstrap` applied once; state container exists with soft
      delete and versioning. Evidence: `az storage container show --name
      tfstate --account-name reposetfstate3912` returns `publicAccess: null`;
      `az storage account blob-service-properties show` returns
      `versioning: true`, blob soft delete `enabled: true, days: 30`,
      container soft delete `enabled: true, days: 30`.
- [x] `tofu plan` on `prod` with the current host list is empty. Evidence:
      `make plan ENV=prod` at commit 95349b5 (this branch's base), against
      the live backend: *"No changes. Your infrastructure matches the
      configuration."* after refreshing all 38 resources. With
      `coolify_count = 1` the same plan, re-run at this branch's head against
      the live backend, is **4 to add, 0 to change, 0 to destroy** (the
      control-plane public IP, NIC, VM and its readiness resource, and
      nothing else) with no errors and one warning, the
      `dns_is_managed_or_manual` check firing as designed because
      `manage_dns` is false.
- [x] A host created by `tofu apply` registers without manual steps.
      Evidence: the registrar for M1 is `hostdev` on the edge (DECISIONS
      I-17), which runs from its store path as the transient unit
      `hostdev.service` on 443, so it is not on root's `PATH` and
      `hostdev status` says `command not found`, which is not the same as
      absent. `systemctl is-active hostdev` on the edge returns `active`, and
      its journal carries
      `{"msg":"host registered","component":"hostdev","event":"register","host_id":"host-01a0bcd4"}`
      at 2026-09-20T03:20:55Z, minutes after the host's install, with no
      manual step between. On `host-01`, `/var/lib/repose/hostd` holds
      `cert.pem`, `host.json` and `key.pem` (the registration output), and
      hostd's journal shows repeated `stream_connect`. The token delivered by
      the apply's provisioner bought those credentials. The
      `repose-admin hosts list` form of this evidence waits on the control
      plane being deployed.
- [x] `/dev/kvm` present and nested enabled on a fresh host. Evidence, on
      `host-01` through the edge jump:
      `crw-rw---- 1 root kvm 10, 232 Sep 20 03:47 /dev/kvm`,
      `cat /sys/module/kvm_intel/parameters/nested` → `Y`,
      `uname -sr` → `Linux 6.18.52`, `nproc` → 16, 62 GB of RAM. The
      `Standard_D16s_v7` runs nested KVM as I-39 said it would.
- [x] NSG on the hosts subnet has no inbound rules; the `tfsec` rule
      REPOSE-NET-001 enforces it. Evidence: `az network nsg show -g
      repose-prod -n nsg-hosts` returns `"rules": []` with tag
      `repose:inbound=none`; `make check` is clean; the rule was fired
      against a fixture that adds one on 2026-09-19. A second guard was added
      on 2026-09-20 for the control subnet: a `postcondition` that fails the
      plan on any inbound rule for 8000, 6001, 6002 or `*` (DECISIONS I-71).
      Fired deliberately by adding a `TEMP-coolify-dashboard` rule for 8000
      to the module and planning against a scratch local-state root; it
      failed with *"The control subnet NSG must not open 8000, 6001, 6002 or
      every port: Coolify's dashboard is unauthenticated until its admin
      account exists, and the way in is `ssh -L 8000:127.0.0.1:8000`"*, and
      passed again once the rule was removed. The three new variable
      validations were fired the same way and produce their documented
      messages: `coolify_size = Standard_D4s_v5`, `coolify_version = latest`,
      and `manage_dns` on with a null zone id.
- [ ] IMDS is reachable from the host and blocked from guests. **Not
      re-checked this session:** the sandbox on the machine running these
      commands refuses shell pipelines that fetch instance metadata, so the
      `curl` from the host was not run. Nothing about the NSG changed since
      the host installed successfully, and the guest-side half is workstream
      01's nftables rule. One command on the host closes it. Open: the
      host half, a `curl -H Metadata:true http://169.254.169.254/...` on
      host-01 answering, is not pasted; the guest half is closed by
      `TestGuestCannotReachIMDS` (14 §9, `isolation-go-20260921T003759Z.txt`)
- [ ] Data disk has `prevent_destroy`; a plan that would replace it fails.
      **Not closed:** the deliberate attempt needs a plan against real state
      with a changed disk size, which was not run. The
      `prevent_destroy` and `ignore_changes` blocks are in
      `azure/modules/host/main.tf` and the equivalent attempt was made
      against the plan on 2026-09-19. One `host_data_disk_gb` change, planned
      and read, closes it. Open: a plan against prod state with a changed
      `host_data_disk_gb`, failing on `prevent_destroy`, is not pasted
- [x] Blob lifecycle rule exists. Evidence: `az storage account
      management-policy show --account-name reposesnapshots3912` returns one
      enabled rule `snapshots-tier-and-expire`, `blobTypes: [blockBlob]`,
      `prefixMatch: [repose-snapshots/]`, `tierToCool.daysAfterCreationGreaterThan: 7`,
      `delete.daysAfterCreationGreaterThan: 45`. The account itself has
      `allowSharedKeyAccess: false`, `allowBlobPublicAccess: false`.
      **Open:** a test blob aged past 45 days and observed deleted. Azure
      keys the rule on real creation time, so this waits 45 days or a
      backdated fixture in staging.
- [x] Key Vault key exists, the api identity can wrap and unwrap and cannot
      `get`. **Half closed.** The vault and key exist:
      `repose-kv-3912`, purge protection on, 90-day soft delete, key
      `repose-dek-wrap` at
      `https://repose-kv-3912.vault.azure.net/keys/repose-dek-wrap/abfe06e3...`.
      The api half cannot be checked: `api_identity_object_id` is null, the
      environment output reports `api_policy_configured: false`, and the app
      registration is a human step (DECISIONS I-21). Supply the object id and
      the wrap/unwrap-only policy appears; the denied `get` is then one
      `az keyvault key show` as that identity. Closed: the policy was
      applied 2026-09-20 and the production CA init then wrapped through
      the key (DECISIONS I-91); secrets' DEKs are wrapped by it in
      production (STATUS 2026-09-20 14-security review 05 line). The
      "cannot `get`" half is superseded by I-91 (Get is granted: it returns
      only the public half, the private key is non-exportable)
- [x] Postgres backups. **Withdrawn, not closed.** This row used to ask
      for an R2 bucket, its token and lifecycle rule, a successful Coolify
      backup job and a rehearsed restore. Backups are Coolify's, against a
      destination in the owner's own Coolify, and nothing on this side
      takes part (DECISIONS I-112): `infra/r2` is deleted, the on-VM
      check and the rehearsal script are gone, and there is nothing here
      left to verify.
- [ ] Edge and Coolify DNS names resolve to their static IPs. **Not closed,
      and worse than absent.** On 2026-09-20 `dig +short
      ssh.repose.herakraft.co` returns `172.67.175.123` and `104.21.31.82`,
      Cloudflare's proxy, not the edge's `20.102.98.254`: `herakraft.co`
      answers every name under it from a proxied wildcard, and the proxy
      carries neither SSH nor WireGuard (DECISIONS I-72). The plan now warns,
      `infra/README.md` has the four records to create by hand, and
      `ops/RUNBOOK.md` has the symptom entry. Closes with a Cloudflare token
      and `manage_dns = true`, or with the records made by hand. Open:
      no `dig +short` for the edge and control-plane names answering their
      static IPs is pasted since this note (the M2 gate reached the gateway
      as `ssh.repose.herakraft.co`, "Real-edge evidence" in 06 §9, so the
      edge record appears to exist)
- [ ] `infra/README.md` covers bootstrap, add host, rotate token, drain and
      destroy host, force-unlock, and someone followed it end to end.
      **Partly:** the control-plane and DNS sections were written by
      following them on 2026-09-20 and the gaps found are now in them (the
      dashboard is not in the NSG; the wildcard; the `.env` that is half the
      backup). The end-to-end pass by somebody who did not write it is still
      owed. Waits on: owner (someone who did not write `infra/README.md`
      follows it end to end)
- [x] Cost table re-checked against the Azure price API on the date of
      the first apply. Evidence: queried 2026-09-20,
      `armRegionName eq 'eastus' and priceType eq 'Consumption'`:
      `Standard_D16s_v7` $1.058/h, `Standard_D2s_v7` $0.132/h,
      `Standard_D4s_v7` **$0.265/h**, `P15 LRS Disk` $38.012142 + $1.825
      mount per month. The re-query corrected the control plane from $140.16
      to $193.45 a month, so the environment is about **$1,094**, not
      $1,040, and over the $1,000 budget alert. Table in `infra/README.md`.

## 10. What is left for the owner

The apply has happened: the network, NAT gateway, snapshot account, Key Vault,
edge and `host-01` are live in `repose-prod`, and `make plan ENV=prod` at this
branch's base was clean against them. What this branch adds is the
control plane, and it is **not applied**; the owner applies from `main`.

One thing to expect, because it happened twice on 2026-09-20: a plan from this
dev box can lose its connection to Azure Resource Manager mid-refresh
(`context deadline exceeded` on a single resource read) and then fail to
release the state lease, leaving the next plan or apply to refuse with
`Error acquiring the state lock`. The recovery is `infra/README.md`,
"Recovering from a stuck state lease": confirm no apply is running anywhere,
then `make -C infra force-unlock ENV=prod LOCK_ID=<the id from the message>`.
The lock blob's own metadata says which operation left it and when, so you
can confirm "no apply was running" instead of assuming it:

```bash
az storage blob metadata show --account-name reposetfstate3912 \
  --container-name tfstate --name azure.tfstate --auth-mode login \
  --query terraformlockid -o tsv | base64 -d
# {"ID":"...","Operation":"OperationTypePlan","Who":"azureuser@woker-1",...}
```

`make plan ENV=prod` shows 4 to add (the control-plane public IP, NIC,
VM and readiness check), and `make apply ENV=prod` creates them. The apply
does not return until the VM is what Coolify's server validation expects
(DECISIONS I-83). `docs/ops/coolify.md` is everything after that, in order,
and its first two steps matter most: put the owner's Coolify address in
`coolify_manager_cidrs`, then add the VM as a server in that instance.

Two things still wait on a human rather than on an apply:

- **A Cloudflare API token**, for `manage_dns = true` and nothing else
  now that `infra/r2` is gone (DECISIONS I-112). Until it exists, the four
  DNS records in `infra/README.md` should be created by hand, because the
  wildcard means the names resolve wrongly rather than not at all.
- **The api's Entra app registration** and its client certificate. Pass the
  app's object id as `api_identity_object_id` and the Key Vault wrap/unwrap
  policy appears (DECISIONS I-21).
- **The budget alert.** `ops/AZURE-SETUP.md` step 7 sets $1,000 a month; the
  environment with the control plane on is about $1,094.

`edge_operator_ssh_port` was 22 until the M2 edge deploy (2026-09-20) gave
the edge its gateway; it is 2222 since, the variable's default.
