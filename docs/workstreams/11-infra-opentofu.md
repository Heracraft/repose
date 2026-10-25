# Workstream 11: infrastructure (OpenTofu)

> **Host size is a variable (DECISIONS I-14).** `host_size` defaults to
> `Standard_D16s_v7` and `host_data_disk_gb` to 512 for the pre-launch month;
> launch values are `Standard_D64s_v7` and 2048 (I-39: this subscription can
> only create v7 sizes). Validation rejects any size whose name is not
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
- Hetzner module: designed for in §5, not built.
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
# write nix/hosts/host-03.nix and nixosConfigurations.host-03 in nix/flake.nix
# add "host-03" to hosts and host-03 = "host-03" to host_flake_attrs,
#   in azure/prod/prod.tfvars, in a reviewed commit
# add host-03's shape to host_shapes there when it is not the fleet's size
# add host-03 = "<token>" to join_tokens in prod.local.tfvars (git-ignored)
make -C infra plan ENV=prod && make -C infra apply ENV=prod
```

The nix file and its flake attribute must be in the checkout the apply runs
from, not only in git: the installer passes `--flake <flake_path>#<attr>`, a
path on the machine running `tofu`. Without a `host_flake_attrs` entry the
host installs the generic `host` attribute, which knows no api address, so it
comes up and never registers (`docs/ops/RUNBOOK.md` "HostUnregistered"). The
`every_host_has_a_flake_attr` check prints a warning naming the host on both
the plan and the apply, and a warning stops neither.

**Sizes are per host.** `host_shapes` is a map keyed by host name whose
attributes (`size`, `class`, `zone`, `data_disk_gb`, `data_disk_iops`,
`data_disk_mbps`, `os_disk_gb`) each override the fleet-wide variable of that
name for that host; what an entry leaves out, and every host with no entry,
takes the fleet value. That is what lets one apply add a host of another size
while the plan shows no change for the hosts already carrying guests, which
is how host-02 (`Standard_D64s_v7`, 2048 GB) joined host-01 (`D16s_v7`, 512
GB) with no downtime (DECISIONS I-14; `docs/ops/LAUNCH.md` §3). A fleet-wide
value changed instead plans a change on every host.

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

### How the installer reaches a host

Through the edge. `nixos-anywhere`, the post-install `/dev/kvm` and nested
checks, and the join-token delivery all connect to the host's private address
with the edge as an SSH jump host, and the module graph makes a host depend on
the edge being installed first. Hosts never get a public IP, not even a
temporary one during the install: that would need an inbound rule on the hosts
subnet, which is the one thing the tfsec policy forbids and §4 and §7 of
`DESIGN.md` promise never exists, and the window is about ten minutes of a
stock Ubuntu image accepting root SSH. DECISIONS I-25 has the full reasoning.

The edge's operator sshd must be listening on `edge_operator_ssh_port` for
that to work. It defaults to 2222 because 22 belongs to the user gateway;
the wave-one edge served sshd on 22 and the first apply set
`edge_operator_ssh_port = 22`, and the M2 edge deploy moved it to 2222
(DECISIONS I-92).

### Why Ubuntu as the install target

Azure has no NixOS marketplace image and building one adds an image
pipeline. nixos-anywhere replaces the running Ubuntu with NixOS over SSH
using kexec, which takes about four minutes and needs nothing but a root
login. The Ubuntu image is a delivery vehicle; nothing from it survives.

### Provider portability

The `host` module has a provider-neutral variable surface: `name`,
`join_token`, `class` (`azure-d16s-v7` on host-01 and `azure-d64s-v7` on
host-02, each from that host's `host_shapes` entry), `data_disk_gb`. Its
outputs are `private_ip` and `ssh_jump`. A `hetzner_host` module with the same
variables and outputs, using the Hetzner Robot provider for an AX162-R and
the same nixos-anywhere provisioner, slots in with one change to the root
module's `for_each`. The NixOS host config already takes its guest CIDR
and WireGuard keys from `Register`, so it does not care which provider it
runs on. Blob as the snapshot target is behind `hostd`'s
`SnapshotStore` interface (Azure Blob and S3-compatible implementations),
so a Hetzner host uses Hetzner Object Storage or R2.

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
