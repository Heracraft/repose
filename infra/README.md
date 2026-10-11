# infra

Every cloud resource repose needs, declared here and created by `tofu apply`,
so that a new host, a replacement edge or a rebuilt control plane is a
reviewed diff rather than a remembered sequence of portal clicks.

Azure holds compute and storage only. Everything else in the system is
provider-agnostic on purpose (`docs/DESIGN.md` §16), so a Hetzner host module
is an addition here rather than a rewrite of anything else. The workstream
document is `docs/workstreams/11-infra-opentofu.md`.

```
infra/
  Makefile            make plan ENV=prod, make apply ENV=prod, make check
  bootstrap/          resource group and state account. Local state. Applied once.
  azure/
    modules/
      environment/    composes everything below; prod/ and staging/ are thin
      network/        VNet, three subnets, NAT gateway, one NSG per subnet
      host/           one repose host: VM, data disk, NixOS, join token
      edge/           SSH gateway and WireGuard hub VM
      coolify/        control-plane VM (stays Ubuntu)
      storage/        snapshot container, lifecycle rule, host identity
      keyvault/       the key that wraps every user's DEK
      nixos_anywhere/ shared provisioner for the two NixOS machines
    prod/             state key azure.tfstate
    staging/          state key staging.tfstate
  dns/                Cloudflare records, called by the environment module
  policy/tfsec/       the two rules a reviewer should not have to remember
```

Never commit `*.tfstate` or `*.local.tfvars`. `.terraform.lock.hcl` **is**
committed, deliberately: it is what makes a plan in CI resolve the same
provider versions as a plan on a laptop.

## Before anything

`docs/ops/AZURE-SETUP.md` lists the one-time human steps: subscription and
quota, resource providers, the `repose-prod` resource group, the state storage
account, `az login`, a budget alert, an SSH key, the Cloudflare and Logto
setup. Steps 1 to 9 must be done before the first apply here.

Tools come from the dev shell. `nix develop ./nix` has `tofu`, `az` and
`nixos-anywhere`; `nix develop ./nix#infra` adds `tfsec` for `make policy`.

Two environment values are read from the environment rather than a file:

```bash
az login                                # OpenTofu's Azure credentials
export CLOUDFLARE_API_TOKEN=...         # only when manage_dns is true
```

## Values: what is committed and what is not

`prod.tfvars` holds the values that belong in review: sizes, disk sizes,
account names, the host list. `prod.local.tfvars` (git-ignored; copy
`prod.local.tfvars.example`) holds the values that are per-operator or
secret: which addresses may reach operator SSH, the operator public keys, the
path to the private key, the Cloudflare zone id and the join tokens. The
Makefile passes both when the second exists.

## Bootstrap

Production's resource group and state storage account were created by hand on
2026-09-19 (`docs/ops/AZURE-SETUP.md` steps 4 and 5) and are **not** managed
by OpenTofu: an apply that could destroy the state container could destroy the
state describing it (`docs/DECISIONS.md` I-20). `infra/bootstrap` describes
that shape anyway, so a second environment is one command:

```bash
make bootstrap ENV=staging
```

which creates `repose-staging` and keeps its state in production's account
under the key `staging.tfstate`. The `tofu import` commands for adopting
production's existing resources are at the top of `bootstrap/main.tf`; running
them is a deliberate decision, not a step in this list.

State locking is a blob lease. Versioning is on, so a corrupted state file is
restored from the previous blob version.

## Staging

`repose-staging` is a throwaway copy of this environment in the same
subscription: its own VNet, NAT gateway, snapshot account, Key Vault, edge
and host, no DNS, no tenant data, nothing shared with `repose-prod` but the
state storage account. It is where a change to a host is proven, because the
alternative is host-01, which carries paying users and the conductor's own
machine. Its resting state is `hosts = []` in `staging.tfvars`, which costs
nothing to plan, so CI plans the root on every change to `infra/`.

**It has no api and it does not borrow production's** (DECISIONS I-642).
Production's api listens on `10.200.3.4:8443` inside production's VNet,
`nsg-control` opens no 8443, and both environments take the module default
`10.200.0.0/16`, so the two VNets cannot be peered. The api side of
`docs/interfaces/grpc-hostd.md` is played by `hostdev` (I-17) on the staging
edge. That is the whole shape of what staging is good for:

| Proven here | Not proven here |
|---|---|
| Anything on a host: LVM pool activation and unit ordering, leftover device maps, a real `thin_check` failure, guest units, kernel parameters, a clean reboot or a hard power-cycle | Anything in the api: what a user is told, placement, resume after a reboot, notifications, migrations, the dashboard |
| A host install from a branch, end to end, timed | The gateway, DNS, certificates, or any `repose` CLI flow — there is no api to hold the records |
| Guests created, started, stopped and snapshotted through `hostdev` | A tenant's own path through any of it |

### Bringing it up

1. `make bootstrap ENV=staging` creates the resource group. **Run it at a
   terminal**: `tofu apply` takes its approval from a tty, and `-input=false`
   turns a missing one into `error asking for approval: EOF`, so it fails in
   a pipe or from an agent's shell without one. 22 s.
2. `infra/azure/staging/staging.local.tfvars` (git-ignored; five values have
   no default). Copy `operator_cidrs`, `control_web_cidrs`,
   `operator_authorized_keys` and `ssh_private_key_path` from
   `prod.local.tfvars` — the same dev box and laptop reach both — and set
   `flake_path` to the checkout's `nix/`. Leave `manage_dns = false`: staging
   needs no DNS record, and false is what keeps `CLOUDFLARE_API_TOKEN` out of
   the loop. The plan then warns `dns_is_managed_or_manual`, which is the
   expected state, not a problem to fix.
3. `make plan ENV=staging`, read it, `make apply ENV=staging` with no host:
   28 resources. The edge comes up `degraded`, and that is expected too — it
   is installed from `nixosConfigurations.edge`, which carries production's
   `edge/edge-01.nix`, so three `wireguard-wg0-peer-*` units fail for peers
   that are not in this environment and the gateway stays inactive. Nothing
   here needs either.
4. `hostdev` on the edge, by hand and staying by hand, because it holds a CA
   key and `docs/SECURITY.md` M-4 took it out of the edge's configuration for
   that reason:

   ```bash
   nix develop ./nix --command env CGO_ENABLED=0 go build -o /tmp/hostdev ./cmd/hostdev
   scp -P 2222 /tmp/hostdev root@<edge ip>:/var/lib/hostdev/hostdev
   ssh -p 2222 root@<edge ip>
     hostdev --state-dir /var/lib/hostdev init --listen 10.200.2.4:8444 --names 10.200.2.4
     nft insert rule inet repose-edge input ip saddr 10.200.1.0/24 tcp dport 8444 accept
     systemd-run --unit=hostdev-serve --collect --property=Restart=always \
       /var/lib/hostdev/hostdev --state-dir /var/lib/hostdev serve
   ```

   `CGO_ENABLED=0` because the edge is NixOS and a dynamically linked build
   from the dev box finds no loader there. 8444 rather than 8443 because the
   edge's gateway holds 8443 for hook ingest. The nft rule is needed because
   the edge's input chain drops by default and accepts 8443 on `wg0` alone;
   it and the unit are lost on an edge reboot, which this exercise never does.
   The CA lives in `~/repose-ws/staging-hostdev` on the dev box so a
   re-created environment reuses it and the `apiCA` in
   `nix/hosts/host-stg-01.nix` stays true; lose it and the repair is
   `hostdev init` again and a new PEM in that file.
5. The host. `hosts`, `host_flake_attrs` and `join_tokens` go in
   `staging.local.tfvars`, **not** in `staging.tfvars` as they do for
   production, so that CI keeps planning this root with no host and no plan
   ever asks for a token it cannot have:

   ```hcl
   hosts            = ["host-stg-01"]
   host_flake_attrs = { host-stg-01 = "host-stg-01" }
   join_tokens      = { host-stg-01 = "<the token hostdev init printed>" }
   ```

   Then `make apply ENV=staging` again. `nix/hosts/host-stg-01.nix` is the
   host-01 configuration pointed at hostdev, with staging's own snapshot
   account and identity.

### Using it

`hostdev --state-dir /var/lib/hostdev status`, `create`, `start`, `stop`,
`snapshot`, `events` on the edge drive the host; `hostd status`, `hostd
guests` and `hostd reconcile` on the host read the daemon over its control
socket.

The host has no public IP, so every connection jumps through the edge.
`~/repose-ws/staging-ssh-config` on the dev box holds both hops as
`stg-edge` and `stg-host`:

```bash
ssh -F ~/repose-ws/staging-ssh-config stg-host 'hostd status'
```

It keeps its own `known_hosts`, and so should anything else that connects:
the staging host is **10.200.1.4**, the address production's host-01 has in
production's identical `10.200.0.0/16`, so a shared `known_hosts` makes each
host look like an attack on the other.

`ops/dev/staging-pool-repro.sh` is the pool evidence, for before and after a
fix: `state` makes a guest-shaped thin volume with a file in it, `show`
prints the pool, the device maps, the dependent units and the boot's LVM
journal, and `leftover` leaves the pool inactive with its components active,
which is the shape the 2026-10-10 incident left behind. It refuses to run
anywhere but a `host-stg-*` machine, because `leftover` deactivates the
volume group every guest volume lives on.

### Taking it down

There is no `make destroy`, and shrinking `hosts` will not remove the host:
its data disk and the attachment carry `prevent_destroy`, which is friction
for production's tenant volumes and cannot be made conditional. For a
throwaway environment the answer is not to edit that module but to delete the
group:

```bash
az group delete --name repose-staging --yes --no-wait     # everything
az storage blob delete --account-name reposetfstate3912 \
  --container-name tfstate --name staging.tfstate --auth-mode login
rm -f infra/bootstrap/terraform.tfstate*                  # bootstrap's local state
```

To pause instead of destroy, `az vm deallocate -g repose-staging -n
host-stg-01` (and the edge) stops the compute charge and keeps the disks.

### The first run, 2026-10-11

Workstream 11 §7 asks this exercise for times and manual steps. Measured on
`woker-1`, with the OpenTofu state empty and nothing in `repose-staging`:

| Step | Time |
|---|---|
| `make bootstrap ENV=staging` (the resource group) | 22 s |
| `make apply ENV=staging`, `hosts = []` — 28 resources | 3 m 22 s |
| of which Key Vault | 2 m 24 s |
| of which the edge's nixos-anywhere install | 1 m 37 s |
| of which the storage account | 1 m 17 s |
| of which the edge VM | 49 s |
| `hostdev init` and `serve` on the edge, by hand | under a minute |
| the host: disk, NIC, VM, attachment | 1 m 11 s |
| its nixos-anywhere install, closure built on the dev box | 1 m 17 s |
| the post-install checks and the join token | 23 s |
| hostd registering with hostdev after the token landed | same second |
| a clean `systemctl reboot` to ssh answering again | 21 s |
| the pool active, measured inside that boot | 2.9 s |

**It took 20 minutes of wall clock, not 10**, because the first host install
had never been run on a size whose NVMe controllers enumerate the other way
round. That is now DECISIONS I-647, and with it the install is the 1 m 17 s
above. Two other things the run found, both fixed in the same branch:

- The post-install check tested `/dev/vg-guests/thin`, which LVM never
  creates — the volume-group symlink exists only for volumes something can
  open, and a thin pool is not one. The gate that is supposed to catch "the
  disko layout never ran" failed every install instead. It now tests
  `/dev/mapper/vg--guests-thin`.
- A test volume must not be named `auth-*`: hostd's login-share sweep
  removes an `auth-` volume that has no user directory as an orphan, by
  design and without a line on success
  (`internal/hostd/guest/claudeauth.go`, DECISIONS I-278). One vanished
  mid-exercise.

The device names are not stable across boots of one machine either: at
install the data disk was `nvme0n1` and the OS disk `nvme1n1`; after the
first reboot the pool's PV was `nvme1n1p1`. Nothing depends on the names
once the system is installed — mounts are by UUID and LVM scans — but it is
why I-647 keys on the namespace id.

### The pool evidence this host was built for, 2026-10-11

With today's main — no pool unit yet — run from
`ops/dev/staging-pool-repro.sh`, full output in `~/repose-ws/logs/stg-case*`
on the dev box:

| Case | What happened |
|---|---|
| Clean `systemctl reboot`, pool and volumes in place | Comes back. ssh answers 21 s after the reboot; `vgchange -aay` activates 4 volumes 2.9 s into the boot; the file written before the reboot reads back. |
| Azure hard restart (`az vm restart --force`) from a left-behind state | Comes back. The reset clears the device maps, so the leftovers are gone and everything activates. The call itself took 126 s. |
| Pool inactive with a component active (`vgchange -an`, then `lvchange -ay --yes vg-guests/thin_tmeta`) | `Activation of logical volume vg-guests/thin is prohibited while logical volume vg-guests/thin_tmeta is active`, every volume stays inactive, the caches and the registry stay down, and **nothing on the host retries**. A reboot clears it, because the leftover is device-mapper state. |
| Damaged pool metadata, then a clean reboot | **The host comes up without its pool and stays that way.** `Check of pool vg-guests/thin failed (status:64). Manual repair required!` at 2.3 s, `0 logical volume(s) ... now active`, `lvm-activate-vg-guests.service: Failed with result 'exit-code'`, and at 6.3 s `The caches' thin volume ... skipped, unmet condition check ConditionPathExists=/dev/vg-guests`. hostd stays up with no pool, the failed unit sits there unread, and `repose_lvm_pool_present` still reports **1**. |

The last row is the 2026-10-10 chain, verbatim error included, on a machine
nobody paid for. It is the row a fix has to change.

What staging **cannot** show is the incident's trigger: thin_check still
running when the initrd hands over. That needs a pool whose metadata takes
long enough to check, and 64 GB of thin pool checks in milliseconds. The
damaged-metadata case above produces the same end state by a different
route, and the initrd change itself is proven by the `host-reboot` VM test
and by the planned host-01 window.

### What it costs

Per day, at the rates in "Cost" below: edge `D2s_v7` $3.17, its OS disk
$0.35, NAT gateway $1.08 plus egress, two static IPs $0.24 — **about $4.90
with no host** — and a `D2s_v7` host $3.17, its 256 GB OS disk $1.31, its
64 GB Premium SSD v2 data disk at the free performance tier $0.17, so
**about $9.50 with one**.

## Order of the first apply

The pieces depend on each other in one direction only:

1. **Edge first.** Hosts have no public IP, so every provisioner reaches them
   through the edge, and the module graph enforces that.
2. **Control plane.** Independent of hosts; its WireGuard peer needs the
   edge's public key, which does not exist until the edge is installed, so
   the first apply leaves its tunnel down (below).
3. **Hosts**, one at a time, each with a join token.

```bash
make plan  ENV=prod        # read it
make apply ENV=prod
```

The plan for an environment with `hosts = []` creates the network, the NAT
gateway, the snapshot account, the Key Vault, the edge and the control plane,
and nothing else.

## How the installer reaches a host

Through the edge, always. `nixos-anywhere`, the post-install `/dev/kvm` and
nested-virtualization checks, and the join-token delivery all connect to the
host's **private** address with the edge as an SSH jump host, and the module
graph makes every host depend on the edge having been installed first.

Hosts never get a public IP, not even a temporary one for the install
(`docs/DECISIONS.md` I-25). A public IP would need an inbound rule on the
hosts subnet, which is the one thing `policy/tfsec` forbids; the install
window is about ten minutes, not seconds; and what would be sitting in it is a
stock Ubuntu image accepting root SSH.

```
you (operator_cidrs) ──ssh──▶ edge :<edge_operator_ssh_port> ──ssh──▶ host :22
```

Two things have to be true for that to work, and both are worth checking
before the first apply:

- **Your address is in `operator_cidrs`.** The edge NSG opens the operator
  SSH port to that list and to the VNet, nothing else.
- **The edge's sshd is on `edge_operator_ssh_port`.** It defaults to 2222,
  because 22 belongs to the user-facing SSH gateway. The wave-one edge served
  sshd on 22 and the first apply set `edge_operator_ssh_port = 22`; the M2
  edge deploy (2026-09-20, DECISIONS I-92) moved it to 2222, and a local
  tfvars that still says 22 makes every host provisioner fail on the
  gateway's `certificate required` banner.

The host side needs nothing configured: Azure's built-in `AllowVnetInBound`
rule is what lets the edge reach a host's sshd, which is why the hosts NSG can
have no rules of its own at all.

The provisioner shape this rests on — bastion connection, `install -d` then a
`file` provisioner then `remote-exec` — was exercised against a real sshd on
2026-09-19: the token arrived at mode 0600 and its contents appear nowhere in
the apply log, because it travels as file content and never as an argument.

## Adding a host

```bash
repose-admin hosts add --name host-02 --provider azure  # prints a token
```

Then:

1. Add `"host-02"` to `hosts` in `azure/prod/prod.tfvars`, in a commit
   somebody reads.
2. Add `host-02 = "<token>"` to `join_tokens` in `prod.local.tfvars`.
3. `make plan ENV=prod`, read it, `make apply ENV=prod`.

The apply creates the VM and its data disk, installs NixOS with
nixos-anywhere through the edge, checks that `/dev/kvm` exists and that
`kvm_intel` nested is `Y`, writes the token to `/run/repose/join-token` and
restarts `hostd`. `repose-admin hosts list` shows `ready` within about two
minutes of the reboot; if it does not, the runbook entry is
"Host never registered".

Leave the spent token in `prod.local.tfvars`. It is single-use and expires in
24 hours, so a stale entry is inert, and leaving it keeps later plans from
proposing to remove the delivery step. Replacing its value is how a token is
rotated.

The whole thing takes roughly ten minutes, most of it the kexec and the
closure copy.

## Rotating a join token

A host that never registered, or one whose token was seen by someone:

```bash
repose-admin hosts add --name host-02 --reissue       # new token
# replace the value in prod.local.tfvars
make apply ENV=prod
```

Only the token-delivery step re-runs; the VM is not touched. By hand, the same
thing is the runbook's "Host never registered": write the token to
`/run/repose/join-token` over the edge jump and `systemctl restart hostd`.

The token never travels as a command-line argument, so it is not in the apply
log, and it lands on a tmpfs, so it is not on the disk
(`docs/DECISIONS.md` I-19).

## The control plane

`coolify_count = 1` creates it: an Ubuntu `Standard_D4s_v7` with a 256 GB
Premium OS disk and a static public IP in the `control` subnet. It is a
**server** of the owner's existing Coolify instance, not a Coolify install of
its own (`docs/DECISIONS.md` I-83): that instance connects over SSH as root,
installs its proxy, and deploys the api, the dashboard, Logto and the platform
Postgres onto it. It stays Ubuntu because Coolify rejects NixOS as a managed
server (`docs/DECISIONS.md` R4-2), which is why this is the one machine here
that `nix/` knows nothing about.

cloud-init leaves the VM as Coolify's "Validate & configure" expects to find
it: root login by key, with the operator keys and the instance's own key
(`coolify_public_key`, a public key, in `prod.tfvars`) in
`/root/.ssh/authorized_keys`; Docker Engine and the compose plugin from
Docker's apt repository; Tailscale, installed but not joined, so that the
owner's Coolify reaches the VM by a tailnet address instead of a public-IP
rule that goes stale (`docs/DECISIONS.md` I-86); `rclone` and `pg_restore`,
because those are the
first two commands of `docs/ops/RUNBOOK.md` "Postgres restore" and a restore
that stops to install something is a restore nobody has rehearsed; the
WireGuard peer script; and nothing listening but sshd. The apply does not
return until `terraform_data.ready` has checked each of those over SSH.

**Two address lists must be right.** The machine running `tofu apply` must be
in `operator_cidrs`: the readiness provisioner reaches the VM over its public
IP on 22, and from anywhere else the apply looks like a fifteen-minute hang
rather than a refusal. The owner's Coolify instance reaches the VM over
Tailscale and needs no rule; a Coolify that is not on the tailnet goes in
`coolify_manager_cidrs` (`prod.local.tfvars`), which the control NSG adds to
the same port-22 rule, and without it that validation times out and looks
like a dead machine.

**There is no port 8000.** Coolify's dashboard is the owner's instance, not
this VM; `azure/modules/network/main.tf` carries a `postcondition` that fails
the plan if 8000, 6001, 6002 or `*` ever appears as an inbound rule on the
control subnet. 80 and 443 are open to `control_web_cidrs` for the proxy
Coolify installs on the server, which terminates TLS for the two names in
"DNS" below.

Everything past the VM is a click path Coolify keeps in its own database, not
in state: `docs/ops/coolify.md` is that path, including adding the server and
the one fact that ruins a restore if it is learned late — Coolify encrypts its
stored credentials with `APP_KEY` from the `.env` on the owner's Coolify host,
so a Postgres dump without that key restores a database of ciphertext.
Backups and restores are Coolify's own, against a destination the owner
configures there; nothing in this repository touches them (DECISIONS I-112).

Setting `coolify_count` back to 0 destroys the VM and its OS disk, the
platform Postgres included; Coolify's own definitions of the resources
survive on the owner's instance, pointing at a server that no longer exists.
The retention that matters is Coolify's own backup plus the owner's `.env`.

## DNS

`manage_dns` defaults to **false**, because the records need a
`CLOUDFLARE_API_TOKEN` in the environment and a `cloudflare_zone_id`, and a
plan without them fails inside the provider with an authentication error that
names neither. With a token:

```bash
export CLOUDFLARE_API_TOKEN=...
# cloudflare_zone_id = "..." and manage_dns = true in the local tfvars
make plan ENV=prod
```

The records are created in the same apply as the addresses they point at, so a
name never outlives the IP it names, and `infra/dns` refuses to proxy anything
under `ssh.`: Cloudflare's proxy carries neither SSH nor WireGuard.

### DNS while manage_dns is false

**"No record" is not "no answer" here.** `herakraft.co` serves every name
under it from a proxied wildcard, so with `manage_dns` off the repose names
resolve to Cloudflare's proxy rather than failing:

```
$ dig +short ssh.repose.herakraft.co
172.67.175.123
104.21.31.82                       # Cloudflare, not the edge (2026-09-20)
```

A user following the documented `ssh ssh.repose.herakraft.co` reaches
Cloudflare's proxy, which does not carry SSH, and every host configured with
that hostname as its WireGuard endpoint fails the same way. The environment
module raises this as a plan-time warning (`check "dns_is_managed_or_manual"`)
rather than letting it be discovered from a connection that hangs.

Until a token exists, create these by hand in the Cloudflare dashboard, as
**A records with the proxy off**, and delete them in the same change that sets
`manage_dns = true` so OpenTofu can create them itself:

| Name | Points at | Proxy | Why |
|---|---|---|---|
| `ssh.repose` | `edge_public_ip` output | **off** | SSH gateway and every host's WireGuard endpoint; the proxy carries neither |
| `repose` | `control_public_ip` output | off | dashboard; the proxy Coolify installs on the server terminates TLS |
| `api.repose` | `control_public_ip` output | off | proxying hides client addresses from the api's rate limits |

## Wiring the control plane to the edge

The control-plane VM generates its own WireGuard key at first boot and never
hands the private half to anybody, so the peering is two moves, one on each
side:

1. **The edge side** is configuration: the VM's public key
   (`ssh root@<control ip> cat /etc/wireguard/publickey`) goes into
   `nix/edge/edge-01.nix` as a `repose.edge.staticPeers` entry with
   `allowedIPs = [ "10.255.255.1/32" ]`, and the edge is rebuilt. `wgsync`
   never removes a static peer (DECISIONS I-92).
2. **The VM side** is `/etc/wireguard/wg0.conf`. cloud-init writes it from
   `edge_wireguard_public_key` only when the VM is *created*: the VM's
   `custom_data` is in `ignore_changes`, so setting the variable later
   changes nothing on a live machine. On a live VM write the peer by hand,
   exactly what `repose-wg-setup` would have written:

   ```bash
   ssh -p 2222 root@<edge ip> cat /var/lib/repose/edge/wg.pub   # the edge's key
   ssh root@<control ip> 'umask 077; cat > /etc/wireguard/wg0.conf <<CONF
   [Interface]
   Address = 10.255.255.1/16
   PostUp = wg set %i private-key /etc/wireguard/privatekey

   [Peer]
   PublicKey = <edge public key>
   Endpoint = <edge ip>:51820
   AllowedIPs = 10.255.0.0/16, 10.64.0.0/12
   PersistentKeepalive = 25
   CONF
   systemctl enable --now wg-quick@wg0 && wg show'
   ```

   Keep `edge_wireguard_public_key` in `prod.local.tfvars` anyway, so a
   replacement control VM comes up peered.

What travels over the tunnel: the edge reaches the api's `/internal` and
gRPC listeners at `10.255.255.1:8444` and `:8443` (the `api-grpc` app's port
mappings, `ops/coolify/README.md`), and the monitoring server, once it is a
peer too, scrapes the api's metrics there. Hosts do **not** use it to reach
the api: they dial `control_private_ip:8443` on the VNet, because a host
registers before it has a tunnel (DECISIONS I-92).

## Draining and destroying a host

Shrinking the `hosts` list destroys a VM and everything on it. Do this, in
order:

1. `repose-admin hosts drain host-02` — the scheduler places nothing new.
2. Move or stop every project on it: `repose-admin projects move` for each,
   or let tenants stop them. `repose-admin hosts list --json` shows the count.
3. `repose-admin hosts retire host-02`.
4. Remove the name from `hosts` in `prod.tfvars`.
5. The data disk and its attachment carry `prevent_destroy`, so the plan will
   fail. That is the point. Delete the `prevent_destroy = true` lines in
   `azure/modules/host/main.tf` in a commit somebody reads, apply, and put
   them back.

Step 5 is friction on purpose: a host's data disk holds every tenant volume on
that host, and the mistake it prevents is a one-character edit to a list.

## Recovering from a stuck state lease

An apply that was killed leaves a lease on the state blob and the next one
says `Error acquiring the state lock` with an ID. So does a **plan** that lost
its connection to Azure Resource Manager mid-refresh: this happened twice from
the dev box on 2026-09-20, with `context deadline exceeded` on a single
resource read followed by `Error releasing the state lock`.

"Confirm nobody is running an apply" does not have to be a guess. The lock
blob's metadata records which operation took it, from where, and when:

```bash
az storage blob metadata show --account-name reposetfstate3912 \
  --container-name tfstate --name azure.tfstate --auth-mode login \
  --query terraformlockid -o tsv | base64 -d
# {"ID":"946e71cf-...","Operation":"OperationTypePlan","Who":"azureuser@woker-1",
#  "Created":"2026-09-20T04:16:35Z",...}
```

`OperationTypePlan` from a machine where no `tofu` process is alive is safe to
break; `OperationTypeApply` from somewhere you cannot see is not.

```bash
make force-unlock ENV=prod LOCK_ID=<the id from the message>
```

If the state file itself is wrong, the container has blob versioning:
`az storage blob list --account-name reposetfstate3912 --container-name
tfstate --include v` shows the versions, and
`az storage blob copy start` from a version restores it.

## Checks

```bash
make check                 # fmt-check + validate (all roots) + tfsec
make plan ENV=staging      # costs nothing; a plan creates nothing
```

`policy/tfsec/repose_tfchecks.yaml` holds two rules:

- **REPOSE-NET-001**: the NSG named `nsg-hosts` must have no security rules.
  Hosts have no inbound (`docs/DESIGN.md` §4, §7); Azure's built-in
  `AllowVnetInBound` already lets the edge's jump connection through, so an
  explicit rule here is always a mistake.
- **REPOSE-VM-001/002**: no VM may set `secure_boot_enabled` or
  `vtpm_enabled`. Trusted Launch disables nested virtualization, and a host
  with no `/dev/kvm` boots perfectly and serves nothing.

tfsec is in maintenance upstream; trivy is its successor. Two checks is a
small enough surface to port when that day comes.

## Cost

Re-checked on **2026-09-20**, the date of the first apply, against the Azure
Retail Prices API (`https://prices.azure.com/api/retail/prices`,
`armRegionName eq 'eastus'`, `priceType eq 'Consumption'`), for what
`prod.tfvars` actually creates: one `D16s_v7` host with a 512 GB Premium SSD
v2 data disk (DECISIONS I-14, I-39), the edge, and **the control-plane VM**
(`coolify_count = 1`, DECISIONS I-71). 730 hours to the month, Linux rates, no
reservation.

| Resource | Unit price | Monthly |
|---|---|---|
| Host `Standard_D16s_v7` | $1.058/h | $772.34 |
| Host OS disk, Premium SSD P15 (256 GiB) + mount | $38.01 + $1.83 | $39.84 |
| Host data disk, Premium SSD v2, 512 GiB | $0.00011/GiB/h | $41.12 |
| — its 16,000 IOPS (3,000 free) | $0.000007/IOPS/h | $66.43 |
| — its 600 MB/s (125 free) | $0.000055/MBps/h | $19.07 |
| NAT gateway | $0.045/h + $0.045/GB processed | $32.85 + egress |
| Edge `Standard_D2s_v7` | $0.132/h | $96.36 |
| Edge OS disk, Premium SSD P6 (64 GiB) + mount | $10.21 + $0.47 | $10.68 |
| Two Standard static IPv4 (NAT, edge) | $0.005/h each | $7.30 |
| Blob, 500 GB of snapshots, Cool LRS | $0.0152/GB/month | $7.60 |
| Key Vault standard, operations | $0.03 per 10K | under $1 |
| **Subtotal, one host and the edge, before egress** | | **about $857** |
| Control plane `Standard_D4s_v7` | $0.265/h | $193.45 |
| — its OS disk, Premium SSD P15 (256 GiB) + mount | $38.01 + $1.83 | $39.84 |
| — its static IPv4 | $0.005/h | $3.65 |
| **Total, one host and the control plane, before egress** | | **about $1,094** |

**The 2026-09-19 table had the control plane $53 a month too cheap.** It
listed the `D4s_v7` at $140.16 a month; the API returns $0.265 an hour, which
is $193.45. The re-query above is what caught it, which is the whole reason
this item is on the checklist and dated.

So the control plane is about **$237** a month, and one host plus the control
plane is about **$1,094** — over the $1,000 monthly budget alert in
`docs/ops/AZURE-SETUP.md` step 7. Raise the budget to $1,200, or drop the
control plane to a `D2s_v7` (2 vCPU, 8 GB) and save $97. Coolify, Logto,
Postgres, the api and the dashboard on 8 GB is tight but not absurd for a
month of testing alone; 16 GB is the size that does not need thinking about.

The other thing this table says that the earlier estimate did not:
**Premium SSD v2 provisioned performance is most of the disk bill.** Capacity
is $41; the IOPS and throughput above the free tier are another $85.
`host_data_disk_iops` and `host_data_disk_mbps` are variables for exactly that
reason, and both can be raised in place later without downtime, so the first
host can start at the free tier (3,000 IOPS, 125 MB/s) and save $85 a month
until there is a guest whose builds justify more.

At launch sizes (`Standard_D64s_v7`, 2 TB data disk, control plane on) the
same table totals about **$2,850**. The $10k credit funds roughly three and a
half months of that with one host. Reservations generally cannot be bought
with sponsorship credits; check before assuming a reserved rate applies.

One figure is not from the API: Azure does not expose a NAT Gateway meter
under any `serviceName` in `eastus` that the retail endpoint will return, so
$0.045 per hour and $0.045 per GB processed are the published rates, not
queried ones.

## What is deliberately not here

- **The NixOS configurations.** `nix/hosts` and `nix/edge` are workstreams 01
  and 06; this only invokes them.
- **Coolify's application definitions.** Coolify keeps them in its own
  database, which no `tofu plan` can read or diff; the click path is
  `docs/ops/coolify.md`, with the short form in `docs/ops/RUNBOOK.md`
  "Control plane".
- **The api's Entra app registration and client certificate.** A credential:
  creating it is a human step next to the other identity setup in
  `docs/ops/AZURE-SETUP.md`, and it does not belong in a state file
  (`docs/DECISIONS.md` I-21). Pass the app's object id as
  `api_identity_object_id` and the Key Vault wrap/unwrap policy appears.
- **Postgres backups.** Coolify's, to a destination in the owner's own
  Coolify. No bucket, no token, no schedule here (DECISIONS I-112).
- **A Hetzner module.** Designed for in `docs/workstreams/11-infra-opentofu.md`
  §5, not built. The `host` module's variable surface (`name`, `join_token`,
  `class`, `data_disk_gb`) and its outputs (`private_ip`, `ssh_jump`) are the
  contract a second provider implements.
- **Automatic capacity.** Capacity is added by a human at an 80 percent memory
  alert (`docs/DECISIONS.md` R3-5).

## Notes for the workstreams this hands off to

- **01 host-nixos.** The v7 sizes are NVMe-only (DECISIONS I-39): the OS
  disk is `/dev/nvme0n1`; the data disk (attached at LUN 10) is found at
  install time by the disko layout's Azure hook as the one NVMe disk that is
  not the OS disk, `/dev/disk/repose/data` (I-41), and the post-install
  check asserts `/dev/vg-guests/thin` exists. `nixos-anywhere` destroys
  whatever is at the OS path.
- **01 host-nixos.** `hostd.service` is the unit name this configuration
  restarts after writing a join token, taken from
  `docs/interfaces/host-conventions.md`. If the unit is named differently,
  set `hostd_unit` rather than renaming anything.
- **06 gateway-edge.** The edge subnet NSG opens 22, 443 and 51820/udp to the
  internet and the operator SSH port to `operator_cidrs` and the VNet, which
  is exactly the port list in `docs/workstreams/06-gateway-edge.md` §5.1. The
  operator sshd must be on 2222, because every host provisioner jumps through
  it while 22 belongs to the user gateway.
