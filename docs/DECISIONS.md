# Decisions

Every settled decision, the alternatives that lost, and why. Numbered by the
interview round they were made in (R1 to R5) so the transcript can be found.
Add new entries at the bottom under "Made during implementation". To reverse
a decision, add a new entry that references the old one; never edit the old
one.

After adding an entry, run `python3 ops/dev/decisions-index.py` to regenerate
[DECISIONS-INDEX.md](DECISIONS-INDEX.md), the one-line-per-entry index
sessions read before opening this file.

Format: **id. Title.** Decision. *Rejected:* the alternatives. *Why:* the
reason. *Revisit when:* the trigger, if any.

## Scope

**R1-1. First release is multi-tenant.** The single-user v0.01 (one Ubuntu VM,
home-manager flake) exists and works; the product being built is the
multi-user service. *Rejected:* single-tenant v0 then grow. *Why:* the owner
wants the ambition intact and the architecture questions answered now.

**R1-2. Unit of environment is one microVM per project on shared hosts.**
*Rejected:* one Azure VM per user holding all projects (no isolation between
projects, one runaway agent hurts the rest); one Azure VM per project (boot
time, idle cost, duplicated Nix stores). *Why:* per-project isolation with a
per-host shared store gives both properties.

**R1-3. Git is the exchange channel, plus a one-shot sync of the uncommitted
diff at launch.** *Rejected:* bidirectional file sync (mutagen) fights a
running agent writing files; remote-only editing forces a habit change.
*Revisit when:* users ask for live sync; consider a `repose sync --watch`.

**R1-4. `run` attaches, `run "prompt"` starts an agent, with a prelisted agent
picker defaulting to Claude.** Build both from the start.

**R1-5. Guests are always-on until `stop`.** No idle auto-stop in the first
release; the signals to design one are recorded from day one. *Why:* idle
detection that kills a long agent job is worse than a bill. *Revisit when:*
the recorded signals show what an unattended agent looks like from outside.

**R1-6. No Tailscale for user access.** *Why:* per-user tailnets do not fit a
multi-tenant product. *Replaced by:* R2-5 (SSH CA and gateway) and R4-3
(WireGuard between edge and hosts).

**R1-8 (amended R2).** Secrets: see R2-8.

## Hosts and guests

**R2-1. NixOS guests on Cloud Hypervisor via microvm.nix with the host's Nix
store shared read-only over virtio-fs.** *Rejected:* Ubuntu guests on
Firecracker with a per-host binary cache (saves bandwidth, not disk or boot
time; Firecracker has no virtio-fs); apt-baked images (abandons Nix). *Why:*
the shared store is only real if guests consume it directly, and a declared
guest removes the hardcoded-user problem and the "exactly their config"
problem at once. Cost: commitment to NixOS on host and guest now.

**R2-2. NixOS host installed with nixos-anywhere.** *Rejected:* Ubuntu host
with a hand-rolled daemon doing microvm.nix's job.

**R2-3. Guest disks are host-local with snapshots to Blob; projects are pinned
to a host.** *Rejected:* one managed disk per project (attach limits, attach
latency, per-disk cost); no snapshots. *Revisit when:* host maintenance moves
become frequent.

**R2-17. Host SKU is Intel D64s_v5 with guest images on a Premium SSD v2
managed data disk.** *Rejected:* D64ds_v5 local NVMe (ephemeral; a routine
deallocate loses every tenant's disk unless the snapshot ran); E-series until
memory data says otherwise. *Watch:* the owner asked to keep researching
this. *Revisit when:* snapshots are proven and hot overlay data could move to
local SSD.

**R2-18. Nested virtualization is accepted, gated by a one-day benchmark
before anything else is built.** Threshold roughly 20 percent penalty on CPU,
disk, network. *Rejected:* no benchmark; a second provider from day one.
*If it fails:* hosts go to Hetzner metal, design unchanged (R3-20).

**R3-2. Three size classes: small 2/4, large 4/8, xl 8/16. CPU 2:1
oversubscribed, memory never.** *Rejected:* one or two classes. *Why:* Docker-
heavy test suites are the XL case people pay for; a balloon reclaiming memory
mid-build turns "slow" into OOM.

**R3-3. Config changes apply in place via switch-to-configuration; reboot
only when the kernel or share layout changed.** *Rejected:* reboot always;
refuse while an agent runs. *Why:* losing an unattended agent because the
user added a package is the product's own failure mode.

**R3-6. LVM thin pool, one thin volume per guest, snapshot with a guest-side
fsfreeze over vsock, streamed compressed to Blob. Nightly plus on stop, keep
7.** *Rejected:* qcow2 external snapshots; whole-disk Azure snapshots (one
tenant's restore needs the whole disk mounted).

**R3-20. Azure hosts while credits last; the OpenTofu host module is written
so Hetzner is a second module.** *Rejected:* AWS metal (same cost as Azure,
no credits, and what it gives, Hetzner gives at a tenth); Hetzner now (loses
the credits and the benchmark's information). The owner may get AWS credits
later; that does not change this.

**R5-1. hostd builds each guest's microvm.nix runner on demand and starts it
as a transient systemd unit.** *Rejected:* rewriting the host NixOS config per
guest and rebuilding the host (minutes per tenant action; the host config
becomes a database).

**R5-2. One in-guest Go agent (`guestd`) on vsock only.** *Rejected:* several
scripts driven over SSH from hostd. *Why:* one process, one channel, works
before the guest has network.

**R5-4. User builds: substitutes from cache.nixos.org and the platform overlay
cache; source builds allowed, capped at 30 minutes and 8 cores; evaluation
capped at 60 seconds; per-project closure cap 20 GB.** *Rejected:* cache-only
(breaks every override); no caps (noisy neighbour).

**R5-5. Default volumes 20/40/80 GB thin, resizable up, billed on allocated.
Egress shaped at 200 Mbit/s, 500 GB/month included.**

**R4-5. Base bumps apply automatically weekly (security sooner) with a
per-project hold flag and a changelog line in status.** *Rejected:* opt-in per
bump (agent versions and kernel fixes never reach everyone).

**R4-4. Builds run on the project's host; a central builder plus cache comes
when there is more than one host.**

## Network and access

**R2-5. SSH certificate authority in the control plane, short-lived
certificates, a gateway that routes by login name.** *Rejected:* temporary
keypair injected into guests per run (key distribution into running guests);
per-user WireGuard (a second product to run). Certificate lifetime 12 hours,
24 since I-267 (R3-9), refreshed silently while the Logto refresh token is valid.

**R2-6. Dev-server access is `repose open <port>` (SSH forward) now; per-
project HTTPS preview URLs later, documented from day one.**

**R3-7. The gateway is custom Go, not OpenSSH ProxyJump.** *Why:* OpenSSH
cannot enforce "this user reaches only this guest" without principals files
on every guest; the Go gateway is also the future preview proxy.

**R3-8. hostd dials out to the API over a mutual-TLS gRPC stream; hosts have
no inbound ports.** *Rejected:* API calling hostd; a broker.

**R4-3. WireGuard between edge and hosts, hosts dial the edge.** *Rejected:*
multiplexing SSH over the gRPC stream; gateway inside the Azure VNet. *Why:*
same outbound-only posture as hostd, and it makes "hosts anywhere" true.

**R4-6. Default deny guest-to-guest and guest-to-host; egress only, shaped;
Azure IMDS blocked from guests.** *Rejected:* allowing cross-project traffic.
*Revisit when:* a user asks to wire projects together; make it explicit.

**R4-13. Gateway and WireGuard hub run on a small NixOS edge VM, not on
Coolify.** *Why:* a Coolify port mapping disables rolling deploys for that
app, WireGuard wants the kernel, and NixOS is already the appliance tool.

## Identity, secrets, policy

**R2-7. Logto (already self-hosted) with the GitHub connector is the identity
provider.** *Rejected:* GitHub OAuth directly; email magic links.

**R5-10. CLI login is authorization code with PKCE and a loopback redirect,
device code as the headless fallback.** Logto supports both.

**R2-8 amended. Tool logins sync from the laptop (gh, Codex, opencode, git
identity); Claude never; named secrets are held centrally with envelope
encryption.** *Rejected:* copying Claude's credentials file (copied refresh
tokens do not refresh, issue closed as not planned); central storage of any
tool login (Anthropic's terms require each user to authenticate with their
own credentials on hosted platforms and forbid third-party reuse of
subscription OAuth).

**R2-15. Claude logs in inside the guest by default; a setup token stored as
a named secret is the headless fallback.** *Why:* the default keeps Remote
Control, which is the "check from my phone" feature.

**R3-10. Named secrets: envelope encryption in Postgres, DEKs wrapped by a Key
Vault key. Disks rely on Azure managed-disk encryption.** *Rejected:* one Key
Vault secret per value (slow, per-operation pricing); per-project LUKS now.
*Revisit when:* you want to make a claim about operator access to tenant
disks.

**R2-10. Egress is unrestricted but shaped; abuse control is card on file plus
GitHub-linked identity plus recorded process samples.** *Rejected:* an egress
allowlist (agents need arbitrary egress).

**R5-3. Process samples contain process names, per-process CPU, memory and
network bytes, sampled every minute. Never arguments, environment, paths or
terminal contents.** The privacy policy states this in the same words.

## Agents and environment

**R2-11 + R2-13 + R2-14 + R2-16. Agents: Claude Code, opencode, Codex CLI,
Gemini CLI, pi. Browser: headless Chromium with Playwright MCP and
chrome-devtools-mcp, plus on-demand Xvfb and noVNC. Laptop-bound MCPs
unsupported at first, `repose mcp forward` planned. Notifications: platform
hooks for every agent plus each agent's own features.** *Rejected:* a longer
agent list (unpackaged tools become packages you maintain); a laptop Chrome
bridge first (only works while the laptop is open, the case being escaped).

**R3-19. Agents come from a platform-owned overlay bumped on a schedule, not
nixpkgs.** *Why:* nixpkgs lags Claude Code by weeks and Gemini CLI by months;
owning the overlay means the platform decides when a tenant's agent changes.

**R3-13. Fixed guest user `dev`, passwordless sudo, project at
`/home/dev/<project>`.** *Rejected:* platform username as Unix user (per-user
values back in the Nix config); `/workspace`. *Why:* MCP configs key on
absolute paths; Claude Code refuses to skip permissions as root.

**R3-14 + R4-10. A prompt starts the agent's TUI in a tmux window named after
the agent; a second `run` with a prompt opens another window with a warning.**
*Rejected:* headless print mode to a log (nothing to attach to); refusing a
second agent; automatic worktrees.

**R3-12. Sync refuses if the guest tree is dirty and offers stash or discard.**
*Rejected:* silently overwriting (clobbers an unattended agent's work).

**R3-11. Projects are keyed on git remote plus user with `--name` override;
nothing committed to the repo.**

## Control plane and operations

**R2-4 + R3-4. Go for api, hostd, gateway, guestd and CLI; Postgres. The
control plane is decoupled from Azure: containers on Coolify, only compute
and storage are Azure-specific.** *Rejected:* TypeScript everywhere; Rust;
a compose monolith in prod (no rolling updates).

**R4-2 + R5-11. Control plane on an Ubuntu LTS VM in Azure running Coolify,
Logto, api and dashboard as single-container apps; Postgres Coolify-managed.**
*Rejected:* NixOS for that VM (Coolify rejects NixOS as a host or managed
server, verified 2026-09-17, open issue since 2024); Azure Database for
PostgreSQL (an Azure dependency in the state store).

**R4-14. Postgres backups to Cloudflare R2.** *Rejected:* Azure Blob behind an
s3proxy (a translation proxy in the backup path). Coolify only backs up to
S3-compatible targets.

**R3-1. Provisioning is OpenTofu for Azure resources with nixos-anywhere as a
provisioner; hostd self-registers.** *Rejected:* Azure SDK calls from the API
(later, for automatic capacity); az CLI scripts.

**R3-5. Capacity is added manually at an 80 percent memory alert.**

**R2-9. Observability reuses the existing Loki, Grafana and Fluent Bit; add
Prometheus there; OpenTelemetry in the control plane with no traces backend
yet. Record everything non-invasive.**

**R2-12 + R3-16 + R4-7 + R4-8. Stripe from day one, card required before the
first guest, $10 credit trial. Meters: guest-hours by class, volume GB-months,
egress GB. Hourly rate with a monthly cap per project equal to the flat price
(49/99/199).** *Rejected:* deferring billing (the owner asked why not bill
now; the abuse argument is handled by the card, and invoices are the only
real test of the meters); flat fee that charges stopped guests; per-account
hour bundles.

**R4-11. Retention: destroy deletes the volume and keeps the last snapshot 30
days; cancellation stops guests, keeps snapshots 30 days.**

**R4-12 + R4 domain note. The name is `repose` everywhere. Hosted under
`herakraft.co` (`repose.herakraft.co`, `api.repose.herakraft.co`,
`ssh.repose.herakraft.co`) until it graduates to its own domain.** *Why:*
velocity and clear ownership; an apps dashboard at `www.herakraft.co` lists
side projects.

**R5-6. No teams in the first release.**

**R5-7. One repository, one Go module, `nix/` and `infra/` alongside the
existing Turborepo.**

**R5-8. Milestone order: benchmark, then hostd and guestd with your own
projects over WireGuard, then edge and auth, then Coolify control plane and
dashboard, then billing, with observability from milestone two onward.** The
owner then asked for all of it to be built rather than a v0.01 rebuild, with
parallel agents; `MILESTONES.md` keeps the gates, `workstreams/` gives the
parallel split.

## Made during implementation

Add entries here as `I-<n>. Title.` with the workstream, the decision, the
alternatives and why. An implementation decision that changes an interface
must also update the `interfaces/` doc in the same change.

**I-1. Gateway terminates and re-dials with a gateway-issued 5-minute
certificate.** (06) *Rejected:* raw TCP relay after auth (the login name is
only known after the client's key exchange completes); falling back to the
client's forwarded agent (kept out of the first release so the guest sshd
check is uniform). Interface: `ssh-gateway.md`, `api.md` (`/internal/gateway-certs`).

**I-2. The gRPC listener for hosts runs as a separate Coolify app
`api-grpc` from the same image with `--mode grpc`.** (05) *Why:* Coolify's
proxy only handles HTTP; a port mapping on the HTTP app would cost it rolling
deploys. The gRPC app restarts with seconds of stream loss that hostd's
reconnect absorbs.

**I-3. The api generates each guest's sshd host key and Host-CA certificate
and passes them in `CreateGuest` and `Restore`.** (05) *Rejected:* hostd
generating keys and asking the api to sign (an extra round trip on the
create path, and hostd holding a signing client). Interface: `grpc-hostd.md`.

**I-4. Internal routes `GET /internal/hosts`, `POST /internal/gateway-certs`,
`POST /internal/events`.** (06) The edge syncs WireGuard peers from the host
list every 30 s; hook events can reach the api over HTTP through the edge
when guestd is unavailable, deduped with the vsock path. Interface: `api.md`.

**I-5. `Heartbeat.draining` and `ApplyResult.reboot_required`,
`ApplyConfig.force_reboot`.** (03) A drained host rejects placements while
serving everything else; a kernel-changing apply does nothing until the user
confirms. Interface: `grpc-hostd.md`.

**I-6. Preview hostnames carry the handle:
`<port>-<slug>-<handle>.repose.herakraft.co`.** (features) *Why:* slugs are
unique per user, not globally. Not built in the first release. Amended by I-438: the form cannot be split when a slug or handle has a dash.

**I-7. `POST /me/notify-test`, and the SSE build-log route accepts
`?access_token=`.** (08, 13) Browsers cannot set headers on EventSource; the
token is never logged and no other route accepts it. Interface: `api.md`.

**I-8. CLI gains `events` and `notify set|test`.** (13) Interface:
`DESIGN.md` §10, `07-cli.md`.

**I-9. The runbook's `repose-admin` surface is the required admin CLI.**
(05, ops) Subcommands named in `ops/RUNBOOK.md` and `11-infra-opentofu.md`
(hosts add/drain/retire/reconcile/mark-lost, projects move/restore/restart,
users suspend, billing rollup/resync, base publish, operator-cert, edge
init, ca sign-host, db migrate/rollback/verify, audit) are part of
workstream 05's checklist.

**I-10. Guest sshd material travels as explicit `CreateGuest` fields and lands
in the guest's secrets tmpfs under reserved names.** (02, 03, 05) hostd writes
`host_key`, `host_cert` and `ssh_ca_pub` to `/run/repose/secrets/` as
`ssh_host_ed25519_key`, `ssh_host_ed25519_key-cert.pub`, `user_ca.pub`, which
the guest base's sshd config reads. The api rejects those names on `PUT
/secrets`. *Why:* one delivery mechanism in the guest, one explicit contract on
the wire.

**I-11. Warning kinds are enumerated.** (04, 12) guestd: `disk_high`,
`inotify_exhausted`, `docker_down`, `freeze_timeout`, `store_path_missing`.
hostd: `pool_high`, `store_high`, `build_queue_deep`, `cache_unreachable`,
`guestd_lost`, `freeze_timeout`. Interfaces: `vsock-guestd.md`, `grpc-hostd.md`.

**I-12. The M0 benchmark is deferred; the first M1 host measures itself.**
(owner, 2026-09-17) The owner chose not to run the standalone benchmark.
Hosts are still Intel `D64s_v5` with security type Standard, and
workstream 03's checklist records build, Docker and clone timings from the
first real host into `RESEARCH.md`. R3-20's Hetzner fallback remains the
escape if those numbers are bad. `00-benchmark.md` stays as the procedure
to run if a host ever needs to be compared.

**I-13. Dashboard design language is the recruiting app's, minus its
non-text controls.** (08) `DESIGN-LANGUAGE.md` lists what to copy (Noto Serif
headings, zinc palette, single blue accent, borders not boxes, button and
field classes) and what to replace (chip and segmented and card radios).
*Superseded by I-369 (2026-09-30): the dashboard's design language is
repose's own, one foundation under every page, and no longer follows the
recruiting app.*

**I-14. Pre-launch host is `Standard_D16s_v5`; the host size is a variable,
not a constant.** (owner, 2026-09-17) The owner will test alone for about a
month with at most 20 projects. Any Intel Dsv5 size supports nested
virtualization, so the design is unchanged; the data disk starts at 512 GB
Premium SSD v2 instead of 2 TB, and the quota request drops to 64 vCPUs in
the family. Growing to `D64s_v5` at launch is an in-place resize (deallocate,
resize, start) because guest volumes are on the managed disk. *Rejected:*
starting on `D64s_v5` (about $2,240 a month of credits spent on empty
capacity); an AMD or B-series size (no or unmeasured nested virt).

**I-36. Guests set `NPM_CONFIG_PREFIX=/home/dev/.npm-global` and put its
`bin` on `PATH`.** (02) npm's default global prefix is the nodejs derivation
itself, so `npm i -g` in a guest fails with EACCES on the read-only store.
*Rejected:* telling users to package everything through config fragments
(agents run `npm i -g` on their own and would hit the error unprompted).
Interface: `guest-conventions.md` "Environment".

**I-15. The product is named Repose.** (owner, 2026-09-19) Replaces
`factory` everywhere: CLI binary, Go module `github.com/heracraft/repose`,
proto packages `repose.*`, config directory `~/.config/repose`, paths under
`/run/repose`, `/var/lib/repose`, `/etc/repose`, env `REPOSE_*`, metrics
`repose_*`, systemd units `repose-*`, hostnames `repose.herakraft.co`,
`api.repose.herakraft.co`, `ssh.repose.herakraft.co`. *Why:* the word says
what the product does (you rest, the environment does not) and no software
trademark or dev-tool product holds it. *Known collisions, accepted:* dead
`repose` packages on npm, PyPI and crates.io (publish scoped or not at
all); an Arch Linux repo tool that ships a `repose` binary (the installer
puts ours first on PATH and warns if another is found, see 07-cli); Rackspace's
dormant OpenRepose API proxy; a supplement brand at getrepose.com. `repose.run`
was free on 2026-09-19 and should be registered. The on-disk checkout may
still be called `factory`; nothing in the repo depends on the directory
name. Supersedes R4-12's naming half.

**I-16. Stripe is not needed until milestone M4; accounts can be billing-
exempt.** (owner, 2026-09-19) `users.billing_status` gains the value `exempt`,
set only by `repose-admin users exempt <handle>`. An exempt user passes every
"card required" and "trial depleted" check, accrues `usage_hours` rows like
anyone else (so the meters are exercised), and is never pushed to Stripe.
The api starts without `STRIPE_*` variables set; billing routes return
`503 billing_disabled` until they are. Workstream 09 removes nothing here:
exempt stays as the operator and design-partner path. *Why:* the owner tests
alone for a month; wiring Stripe before there is anything to bill is work
done in the wrong order.

**I-17. hostd ships a one-host dev driver, `cmd/hostdev`.** (03) The M1 gate
("your own projects run as guests, driven by a local client") needs the api
side of the gRPC contract without the api. `hostdev` is that: one host, one
operator, a JSON state file, every command as a subcommand, build logs and
samples printed. It stays as the break-glass tool for a host that has lost
the api. *Rejected:* building the api first (puts auth, Postgres and Logto on
the critical path to the first running guest).

**I-18. Host runtime configuration has one input, `host.json`; the host
owns the network files, the bridge isolation lives in an nftables `bridge`
table, and guests are cut off from every private range.** (01)
Recorded because the docs disagreed with each other or with the kernel:

- hostd writes `host.json` only and restarts `repose-host-net.service`
  (which renders `wg0.conf`, `host.env`, `host_ca.pub`, `sshd.conf` and
  the bridge address). 03-hostd §5.2's "hostd writes
  `/var/lib/repose/hostd/wg0.conf` and restarts `wg-quick@wg0`" is
  superseded; `wg-quick-wg0.service` reads `/run/repose/wg0.conf`.
  *Rejected:* two writers of WireGuard config (a rotation by hostd and a
  boot render by the host would race).
- The nightly timer calls `hostd snapshot-all` (03's name), not `hostd
  snapshot --all --reason scheduled` (01's text). 03 owns the binary.
- Guest-to-guest traffic on the same bridge never traverses the `inet`
  forward hook, so the documented `10.64.0.0/12` drop cannot isolate
  tenants by itself. A `bridge repose` table drops all switched frames
  and admits only ARP and IPv4 from `(mac, ip, tap)` tuples hostd
  registers in its `guests` set; taps are attached `isolated on learning
  off flood off` with a static FDB entry. The `inet` drop stays for
  routed traffic. *Rejected:* `br_netfilter` (routes bridged frames
  through iptables hooks, slower and a global switch); trusting port
  isolation alone (no anti-spoofing).
- hostd's per-guest objects live in `guest_dyn` and in the `guests` set,
  and the host's ruleset is applied by flushing only its own chains, so a
  `nixos-rebuild switch` never loses a running guest's counter or
  admission. 03's "nft add element repose guests { <ip> . tap }" becomes
  the bridge-table element above.
- Guests are dropped to every private range (`10/8`, `172.16/12`,
  `192.168/16`, `100.64/10`, `169.254/16`) and to the Azure wire server
  `168.63.129.16` (it serves extension protected settings), not only to
  IMDS and `10.64.0.0/12`. DESIGN §7's "everything else is egress to the
  internet" is the intent; the VNet, the WireGuard mesh and the Coolify
  VM's private address are not the internet.
- `kernel.unprivileged_userns_clone` is a Debian patch; the NixOS kernel
  equivalent `security.allowUserNamespaces` is set instead.
- The guests slice reserve follows DESIGN §4 (8 GiB below 128 GiB of RAM,
  16 GiB above), computed at boot, rather than 01's flat 16 GiB.
- `system-features = kvm` and membership of the `kvm` group are required
  on the dev box for the host VM tests (`docs/ops/DEV-BOX.md`).

Interfaces: `host-conventions.md` rewritten with the `host.json` shape,
the tap attach sequence, both tables, the store export path and the
`hostd` subcommand contract. The old inet-only rules are not kept: no
host has been provisioned yet.
**I-19. The state store and its resource group are created outside the
environment's apply.** (11) `repose-prod` and the storage account
`reposetfstate3912` inside it were created by hand on 2026-09-19
(`ops/AZURE-SETUP.md` steps 4 and 5) and are read by the environment roots as
a data source, never managed by them. `infra/bootstrap` declares the same
shape — resource group, account with versioning and soft delete, private
`tfstate` container — so a second environment is one apply, and takes
`state_account_name = null` for an environment that keeps its state in another
one's account under a different key (staging does). *Rejected:* importing
production's state account into `infra/bootstrap` now (a corrupted bootstrap
state could then destroy the state of every other root; the import commands
are written down in `bootstrap/main.tf` for the day that trade looks
different); a separate resource group for state, as workstream 11 §2
originally said (it would have meant a second group to protect and a second
one to remember, for no isolation that the `prevent_destroy` on the account
does not already give).

**I-20. The join token reaches a host over SSH after the install, not through
cloud-init.** (11) `docs/workstreams/11-infra-opentofu.md` §2 described
cloud-init writing `/run/repose/join-token`. It cannot work: cloud-init runs
on the Ubuntu image, and nixos-anywhere kexecs and replaces that system
minutes later, so `/run` is a fresh tmpfs by the time hostd starts. The token
is therefore written by a provisioner that connects to the installed NixOS
system through the edge, as file content rather than as a command-line
argument, and `hostd` is restarted. *Why this is better than fixing it with
`--extra-files`:* `custom_data` stays in the Azure VM model and is readable
through IMDS for the life of the VM, and `--extra-files` would put a
single-use secret on the persistent root disk. *Interface:*
`interfaces/host-conventions.md`, whose `/run/repose/join-token` row said
"from cloud-init". The path, the mode and the one-shot semantics are
unchanged, so nothing that reads the file changes; the runbook's
"Host never registered" recovery was already this exact mechanism by hand.

**I-21. Credentials stay human steps: the api's Entra app registration and
the R2 API token.** (11) `infra/` creates the Key Vault, the wrapping key and
an access policy for the api's service principal given its object id
(`api_identity_object_id`, null until it exists), and creates the R2 bucket
and its lifecycle rule. It does not create the app registration, its client
certificate, or the R2 token. *Rejected:* the `azuread` provider plus
`tls_private_key` (the api's private key would sit in the state file in clear
text for the life of the environment); `cloudflare_api_token` (same, for the
backup credential). *Why:* `ops/AZURE-SETUP.md` already draws this line —
one-time human actions that OpenTofu cannot do, or that agents should not be
trusted to do with the owner's money and identity — and a credential in state
is a credential in every backup of that state.

**I-22. `.terraform.lock.hcl` is committed.** (11) It was in `.gitignore`.
A dependency lock file that is not committed means CI resolves whatever
provider version shipped that morning, so the plan a reviewer reads and the
plan CI runs can differ. The files are locked for `linux_amd64`,
`darwin_arm64` and `darwin_amd64` so the owner's laptop and the dev box agree.
*Rejected:* pinning exact versions in `required_providers` instead (it pins
the version but not the checksum, and it has to be edited in four roots).

**I-23. The edge VM is `Standard_D2s_v5` and its NSG opens 22, 443,
51820/udp and 2222.** (11) `docs/workstreams/11-infra-opentofu.md` §2 said
`Standard_B2s` and "inbound 22/tcp and 51820/udp"; the size note at the top
of the same document, added with I-14, says `Standard_D2s_v5`. The later note
wins, and the burstable size is the wrong shape anyway: the thing that would
throttle when its credits run out is every user's SSH session. The port list
comes from `workstreams/06-gateway-edge.md` §5.1, which is the document that
owns the edge's listeners: 22 is the user gateway, 443 the preview-proxy
stub, 51820/udp the WireGuard hub, and 2222 the operator sshd — restricted to
the operator address list and the VNet, because every host provisioner jumps
through it and hosts have no public IP.

**I-24. The control-plane VM is not created until wave 3.** (11, owner,
2026-09-19) `coolify_count` defaults to 0 in both environment roots. The api,
the dashboard and Logto are workstreams 05 and 08; until they exist the VM
bills about $180 a month for nothing, while the edge and the first host are
worth paying for early, because installing NixOS onto an Azure VM with
nixos-anywhere is the riskiest unproven step in the plan and workstream 01's
data-disk device path stays unverified until a real install happens.
*Rejected:* creating it with everything else (the original shape of workstream
11 §2) and stopping it by hand (a deallocated VM still bills its 256 GB
Premium OS disk, and a VM that exists is a VM somebody configures). Setting
`coolify_count` back to 0 after the VM exists destroys it and its OS disk,
Postgres included; the retention that matters is the R2 dump.

**I-25. The installer reaches a host through the edge, never through a
temporary public IP.** (11) `nixos-anywhere`, the post-install checks and the
join-token delivery all connect to the host's private address with the edge as
an SSH jump host, and the module graph makes a host depend on the edge being
installed. *Rejected:* giving the host a public IP for the length of the
install and removing it afterwards. *Why:* a public IP on a host needs an
inbound rule on the hosts subnet, which is the one thing
`infra/policy/tfsec` forbids and `DESIGN.md` §4 and §7 promise never exists;
the window is not short (kexec, disko, closure copy and reboot is about ten
minutes) and what sits in it is a stock Ubuntu image accepting root SSH; and
the edge path is the same one the runbook's "Host never registered" recovery
already used, so the recovery path is exercised by the happy path rather than
first tried in an incident. *Cost:* the edge must exist and be reachable
before the first host, and its operator sshd must be listening on
`edge_operator_ssh_port`. Until workstream 06 moves it, `nix/edge` serves sshd
on 22, so the first apply sets `edge_operator_ssh_port = 22`.
**I-26. Commands carry what hostd cannot keep: StartGuest repeats the
delivery fields, CreateGuest and Restore name the user, slug and remote,
Restore names the closure, Exec carries an audit id, StopResult carries
the snapshot's blob path.** (03) hostd holds secrets and sshd material in
memory only (secrets have three homes and the host disk is not one), so
after a hostd restart a `StartGuest` with only `guest_id` would boot a
guest without its secrets or host key. `StartGuest` now accepts the same
optional fields as `CreateGuest` (`secrets`, `env`, `ssh_ca_pub`,
`principals`, `hooks_config`, `host_key`, `host_cert`, `project_json`); the
api sends them on every start, and the old shape (guest id only) still
works while hostd has the values cached. `CreateGuest` and `Restore` gain
`user_id` (the snapshot path is `<user_id>/<project_id>/<ts>.img.zst`),
`project_slug` and `remote_url` (guestd's `SetupProject` needs them) and
`project_json`; `Restore` gains `system_closure` (the doc said "the closure
the api passed" but the message had no field). `Exec` gains `audit_id`,
required, because the doc says Exec is only accepted with one. `StopResult`
gains `blob_path` and `bytes` because hostd never knows the api's
`snapshot_id`. *Rejected:* an encrypted secrets cache on the host disk (a
fourth home for secrets); hostd asking the api for secrets over the stream
(a request channel the contract does not have). Interface: `grpc-hostd.md`,
`hostd.proto`.

**I-27. hostd launches Cloud Hypervisor directly from the guest's system
closure; no per-guest microvm.nix runner is built.** (03) The NixOS
toplevel already carries `kernel`, `initrd`, `init` and `kernel-params`;
hostd renders the `cloud-hypervisor` argv from them plus the guest record
(tap, MAC, CID, volume, class) and writes it to
`/var/lib/repose/guests/<id>/ch.args`. microvm.nix's runner would only wrap
the same values, and building one per guest means evaluating the whole
NixOS system on every start (tens of seconds, against the 5 s start in
DESIGN §5), while one runner per base cannot take per-guest arguments.
The guest side (virtio-fs tag `ro-store`, root on the disk, the writable
store overlay) stays in 02's module; `mkGuestRunner` remains for VM tests
and `nix flake check`. The kernel command line hostd adds is `init=`,
`console=ttyS0` and the static `ip=` line 02 documents. *Rejected:* runner
per guest at start (eval cost); one runner per base with arguments
(microvm.nix does not produce one). Interface: `host-conventions.md`
(`ch.args` replaces `runner`).

**I-28. The platform flake takes the user fragment as a non-flake input
named `fragment` and exposes `guestSystem`; hostd fetches base checkouts
with git.** (03, for 12) Pure evaluation forbids reading any absolute path
outside the flake's own source, including store paths given as literals,
so a fragment file cannot be passed with `--apply` or `--arg`. It is
passed as `--override-input fragment path:/var/lib/repose/builds/<rev>`,
which Nix copies into the store and lets the flake read as
`${fragment}/fragment.nix`. Error locations come out as `fragment.nix:L:C`,
which is what the error mapping parses. A base checkout lives at
`/var/lib/repose/base/<base_ref>` and hostd clones the repository there
with `--base-repo-url` when it is missing. The exact contract is
`docs/interfaces/nix-build-contract.md`. *Rejected:* `--impure` (opens
environment and path access to fragments); tarballs delivered in `Build`
(a 30 MB message per build).
**I-29. Two more guestd warning kinds: `oom` and `tmux_down`.** (04) I-11
enumerated five guestd kinds, but `04-guestd.md` §5 and §6 and
`02-guest-base.md` §6 each describe a condition outside that list: the kernel
killing a process for memory, and no tmux server running for `dev`. Both are
things the user sees as an agent that vanished or a `repose attach` that finds
nothing, so both are worth a notification. *Rejected:* folding them into
`disk_high`-style generic text (a kind is what the dashboard and the runbook
key on); dropping them (the two docs that describe them would then be wrong).
The `oom` detail carries the killed process's *name*, which is the one thing
already on the allowed side of the sampling boundary (R5-3). Interface:
`vsock-guestd.md`. Note also that `04-guestd.md` §5 writes the disk kind as
`disk_90`; the enumerated name is `disk_high` and that is what the code uses.

**I-30. `WriteSecrets` carries the whole set, and validates before it
writes.** (04) The request replaces the guest's named secrets: a secret on the
tmpfs that is absent from the list is removed, and `secrets.env` is rewritten
from the list. *Rejected:* treating the list as a partial update (then `repose
secrets rm` never reaches a running guest, and a revoked token keeps working
until the next stop); removing reserved names the same way (they arrive on the
create path, not the secrets path, so they are exempt). Validation of every
name and size happens before the first write, so a rejected batch leaves the
guest exactly as it was. Interface: `vsock-guestd.md`.

**I-31. `Sample` serves the tmux and Docker signals from a 5 s cache, and
carries a `partial` flag.** (04) `04-guestd.md` §5 budgets a sample at under
20 ms and §6 says an over-budget sample returns partial data; forking `tmux
list-windows` and `tmux list-clients` on the call costs more than the whole
budget on its own. A background watcher refreshes them every 5 seconds, which
is also what the agent-state machine and the 90 s pane-idle heuristic need to
run on; `Sample` walks `/proc` fresh and reads the rest from memory. Measured:
4.5 ms for 300 processes. `SampleResult` gains `bool partial = 3`, set when a
signal is missing rather than zero, so hostd can tell "no sessions" from "not
known". *Rejected:* forking on the sampling path (over budget, and it competes
with the agent for a 2-vCPU guest); dropping the accuracy claim (the signals
decide the idle policy later, and a signal that is silently stale is worse
than one that says so). Interfaces: `vsock-guestd.md`,
`proto/repose/guestd/v1/guestd.proto`.

**I-32. `guestd call` is the client side of the vsock contract, in the same
binary.** (04) The NixOS VM test and an operator on a guest that has lost
hostd both need to send a request and read the response; hostd is the only
other client and it is a different workstream's binary. `guestd call <request>
[json]` dials the dev socket or a vsock CID, prints the response as JSON, and
exits non-zero on an error response. *Rejected:* a separate test-only binary
(a tool that exists only in tests is a tool nobody maintains); waiting for
hostd (the VM test is 04's checklist item, not 03's). Interface:
`vsock-guestd.md` "Dev mode and the client", `ops/RUNBOOK.md`.
**I-33. The on-demand desktop is display `:99`, socket-activated, with a
per-start password file.** (02) `features/browser.md` said `:1` and
`workstreams/02-guest-base.md` said `:99`; the module uses `:99` (the
conventional Xvfb display, never taken by a real seat) and the feature doc
is corrected. `repose-novnc.socket` on 127.0.0.1:6080 pulls in websockify
(6081), x11vnc (5900), openbox and Xvfb through `systemd-socket-proxyd`
because websockify cannot inherit a listening socket; a per-minute check
stops the chain after 30 minutes without a client and `systemctl start
repose-desktop-idle` stops it now. x11vnc gets a fresh 8-character password
at every start, kept in `/run/repose/desktop/vnc-password` (0600 dev) for
the CLI to print; noVNC is only reachable through the SSH forward, so the
password is defence in depth. *Rejected:* `Accept=yes` per-connection
websockify (noVNC's page makes several requests, each would fork a server);
no password (a stray forward on a shared laptop would expose the desktop).

**I-34. `mkGuestRunner` takes every per-guest value at run time; the
system closure is guest-independent.** (02, 03) `workstreams/02-guest-base.md`
listed `guestId, ip, gatewayIp, cid, volumeDevice, vcpu, mem` as
evaluation arguments. Baked in, they would put the address allocation
before `Build` (which produces `system_closure` per revision, before
`CreateGuest` allocates an ip), make one closure per guest instead of per
revision, and make `kernel_changed` a per-guest comparison. The function
still accepts those attributes as defaults, but the runner's `bin/run`
takes them as arguments (contract in `interfaces/guest-conventions.md`
"Runner contract"), the kernel line carries `ip=` and the guest's
systemd-network-generator applies it. hostd builds one runner per base
version and revision and starts any guest from it. The vsock device is a
unix socket `vsock.sock` in the guest's state directory (Cloud Hypervisor
implements vsock in user space; the host's `vhost_vsock` module is not
involved), added to `interfaces/host-conventions.md`. *Rejected:*
`config.microvm.declaredRunner` as the output (its script has the sockets,
tap and volume fixed at evaluation).

**I-35. sshd material: reserved secrets at `/run/repose/`, symlinked into
`/etc/ssh/`, a throwaway key until delivery, reload re-reads.** (02, 04)
Three docs disagreed on where `user_ca.pub` and the host key live
(`/run/repose/secrets/`, `/run/repose/`, `/etc/ssh/`). guestd writes the
reserved names to `/run/repose/` (04's design), `/etc/ssh/` holds symlinks
to them so `sshd_config` reads the paths `interfaces/ssh-gateway.md`
shows, and the api keeps rejecting the reserved names on `PUT /secrets`
(I-10). Because `Ready` is sent once sshd listens and the secrets follow
`Ready`, sshd's preStart generates a throwaway ed25519 key when none was
delivered and an empty CA file (trusts nobody); the guest's sshd unit gets
an `ExecReload` (`SIGHUP`, which re-execs sshd) so guestd's `systemctl
reload sshd` after `WriteSecrets` and `SetPrincipals` picks up the real key,
certificate and CA. *Rejected:* delaying sshd until the secrets arrive (a
guest whose hostd died before delivery would have no way in at all).

**I-37. Two vsock RPC implementations exist for one release.** (merge of 03
and 04, 2026-09-19) 03 and 04 each wrote `internal/vsockrpc` and a guestd
fake against the same contract, with the same uvarint-length protobuf
framing, so they interoperate on the wire. 04's stays as the shared
`internal/vsockrpc` (guestd is the canonical server side); 03's moved to
`internal/hostd/vsockrpc` and `internal/hostd/fakeguestd`. hostd should
migrate to the shared package when it is next touched; the M1 integration
session proves the two speak to each other.

**I-38. Generated protobuf code is tracked and also regenerated in the Nix
sandbox.** (merge, 2026-09-19) `internal/gen/` is committed so `go build`
works without buf; `nix/packages.nix` regenerates it with local plugins
inside the build so a stale checkout cannot ship stale stubs. One
`packages.nix` builds every Go binary (guestd, repose-hook, hostd, hostdev)
from one vendor hash.

**I-39. Sizes are Intel v7 (Granite Rapids): host `Standard_D16s_v7`, edge
`Standard_D2s_v7`, control plane `Standard_D4s_v7`, launch host
`Standard_D64s_v7`.** (owner's first apply, 2026-09-20) The first apply
failed with `SkuNotAvailable`: this subscription has every v5 and v6
general-purpose size marked NotAvailableForSubscription in East US and East
US 2, all zones (`az vm list-skus --all`), which is a subscription-level SKU
gate, not capacity. The v7 families are unrestricted in all three zones with
a 350 vCPU quota each already granted. Verified against the size pages:
Intel Xeon 6, x86-64, nested virtualization Supported, Gen2 only, security
type Standard allowed by setting it explicitly, NVMe disk controller only.
Consequences: disks are `/dev/nvme0n1` (OS) and `/dev/nvme0n2` (data LUN 0)
instead of `/dev/sda` and the SCSI udev path; the host module's validation
accepts `Standard_D<n>(l|d|ld)?s_v[567]`; cost is about $772 a month for the
host and $96 for the edge, about 40 percent more than the v5 figures in
`PRICING.md`, which now describe launch economics on a v5 reservation or
Hetzner. R2-17's AMD exclusion stands: `a`-sizes stay out. *Rejected:*
requesting v5 SKU enablement through support (days, uncertain); another
region (same gate); Hetzner now (R3-20's fallback remains available).

**I-40. A production host is a named configuration; the api CA and the
snapshot target are host module options; a host registered by `hostdev`
comes up without WireGuard or a Host CA.** (m1 integration, 2026-09-20)
Bringing host-01 up against `hostdev` on the edge (I-17) found four gaps
between the merged host configuration and a real host:

- `hostd.service` passed no snapshot target, and hostd exits at startup
  without one (`hostd: set --snapshot-dir or --blob-url`). The host module
  gains `repose.host.snapshots.{blobUrl, container, identityClientId,
  localDir}`; the unit passes Blob when `blobUrl` is set and
  `--snapshot-dir` otherwise, with a build warning on an Azure host that
  has no Blob account. *Rejected:* defaulting the Blob URL in the module
  (the account name is an infra value; see `prod.tfvars`).
- hostd had no way to trust hostdev's self-signed CA. `repose.host.apiCA`
  (PEM text, public material) is written to the store and passed as
  `--api-ca` by both `hostd.service` and `repose-register.service`.
- `repose-host-net` used `jq -e` on `host_ca_pub` and the WireGuard fields,
  which the api fills and hostdev does not, so registration by hostdev
  left the bridge unconfigured. It now renders `wg0.conf` only when every
  WireGuard field is present (the unit is already conditioned on the file),
  writes an empty CA file otherwise, and says so in the journal.
- The generic `host` attribute has bootstrap sshd off, and until workstream
  06 puts hosts on WireGuard the installer, the join-token delivery and
  operators reach a host only over the VNet through the edge, which the
  nftables `input` chain admits only while `repose.host.bootstrap.enable`
  is on. So a production host is its own attribute, `nixosConfigurations.
  host-<name>` from `nix/hosts/<name>.nix`, selected by the new root
  variable `host_flake_attrs` in `infra/azure/{prod,staging}`; `host-01`
  sets the edge address, hostdev's CA, the operator key for bootstrap and
  the Blob endpoint and identity. *Rejected:* setting these on the generic
  `host` (every future host would trust a dev CA and expose bootstrap
  sshd); passing them at install time (nixos-anywhere takes a flake
  attribute, nothing else). Interfaces: `host-conventions.md` (the
  `hostd.service` command line). The edge firewall also opens 443, which
  the NSG already did, for hostdev now and the preview-proxy stub later.

**I-41. The data disk is found at install time, not named in advance.**
(m1 integration, 2026-09-20) I-39 named the data disk `/dev/nvme0n2` "LUN
0", while infra attaches it at LUN 10 and Azure's remote-NVMe FAQ says v7
sizes put cached disks (the OS disk) on one controller and uncached data
disks on a second one, which would make a Premium SSD v2 data disk
`/dev/nvme1n1`. Neither name has been seen on a real v7 host. The disko
layout's Azure hook, which already runs before the data disk is touched,
now resolves `/dev/disk/repose/data` itself: the SCSI by-LUN path when the
size is SCSI, otherwise the single NVMe disk that is not the OS disk, and
it fails with the list of disks it saw when there is not exactly one.
`repose.host.dataDevice` defaults to that symlink and the post-install check
asserts the thin pool `/dev/vg-guests/thin` exists rather than a device
name. *Rejected:* fixing a namespace number (wrong on one of the two
controller layouts, and wrong again if the LUN changes); a udev rule in the
installer (nixos-anywhere's kexec image takes none). *Revisit when:* a host
has more than one data disk.


**I-42. The api's implementation shape: phased ops driven by one replica,
/internal on the gRPC app, CA material in the secrets table, guest host
certificates re-signed once the address is known.** (05, 2026-09-20)
Recorded where the code had to choose beyond what the docs said, or where
a doc disagreed with a contract:

- *Ops.* Every long operation is an `ops` row with a list of phases fixed
  at enqueue (`create` = build, create_guest; `destroy` = final snapshot,
  destroy_guest; `restore` = destroy old guest, build if no closure,
  restore, start; ...). Each phase is one hostd command whose
  `command_id` is written to the row before it is sent and whose result
  lands in `command_result`; the command is rebuilt from the database and
  re-sent with the same id after an api restart or on the host's next
  `Hello`, so nothing but the row has to survive. The driver runs on the
  replica holding advisory lock `LockOps`; a second replica serves HTTP and
  the stream but would need the internal forwarding hop 05 §5.1 describes
  before it can drive ops for hosts connected to it. *Rejected:* a
  goroutine per op (lost on restart); storing the serialised command
  (secrets in plaintext in a fourth place).
- *The gRPC app also serves `/internal`.* Coolify's proxy terminates TLS
  for the HTTP app, so it cannot require the gateway's client
  certificate. `/internal/*` is served by the `api-grpc` process on
  `API_INTERNAL_LISTEN` (8444) over HTTPS with
  `RequireAndVerifyClientCert` against the platform's X.509 host CA, the
  same authority that signs host certificates at `Register`; `repose-admin
  ca sign-client --name gateway` issues the client certificate. Gateway
  session reports are persisted in `gateway_sessions` so the HTTP app can
  show them.
- *CA material lives in the secrets table.* The two SSH CAs and the X.509
  host CA (certificate and key) are rows of the platform pseudo-project
  (`00000000-0000-7000-8000-000000000000`, owned by the pseudo-user
  `repose-platform`), envelope-encrypted like any secret, created by
  `repose-admin ca init` and loaded at start. The `HOST_CA_CERT` and
  `HOST_CA_KEY` variables 05 §5.14 listed are gone; `GRPC_SERVER_CERT/KEY`
  remain for the public listener. *Rejected:* PEM files in Coolify
  secrets (a fourth home for a signing key, and no rotation path).
- *Guest sshd keys are reserved secrets.* The key I-3 says the api
  generates is stored under the reserved names of I-10 in the project's
  secrets rows, so every `StartGuest` and `Restore` delivers the same key.
  At `CreateGuest` the host certificate can only carry
  `<slug>.<handle>`: the guest's address is assigned by hostd and comes
  back in the result. The api then re-signs the certificate with both
  principals for the next start. The gateway therefore verifies a guest's
  host key with `<slug>.<handle>` as the expected principal, which it
  knows from the login name, not with the address. Interface:
  `ssh-gateway.md`.
- *Samples are inserted, not copied.* `Samples` messages are written with
  `insert ... on conflict do nothing` in one batch per message rather than
  the single `COPY` of 05 §5.4, because hostd re-sends buffered samples
  after a reconnect and a duplicate primary key would fail the whole
  `COPY`.
- *Schema additions.* `hosts` gained `name`, the heartbeat columns and the
  join-token hash; `ops` gained phases, `params`, `command_result`,
  `result`, `revision_id`, `snapshot_id`, `audit_id`, `reboot_required`;
  `config_revisions` gained `kernel_changed` and `reboot_required`;
  `events` gained `tmux_window` (`window` is reserved in SQL),
  `ts_second`, `source`, `skew_seconds`, `host_event_id`, and its dedupe
  index applies only to hook kinds so state changes may repeat within a
  second; `snapshots` gained `restoring_op_id` (the guard the expiry job
  respects); `usage_hours` gained the three cost parts and the storage
  remainder the cap rule needs (shared with 09); new tables
  `events_outbox` (13's shape), `host_sessions`, `gateway_sessions`,
  `settings`. `db-schema.md` is the reference.
- *Consumed packages that do not exist yet.* `internal/billing` carries the
  price table and a `Disabled` pusher and portal (`503 billing_disabled`,
  I-16); the notification senders live in `internal/api/notify` with the
  documented headers; `internal/nixmenu` is the api's view of the catalog
  with a package-only menu (12 owns the contents and the service
  snippets). When 09, 12 and 13 land, the api swaps the implementation
  behind the same interfaces.
- *Small contract points.* `POST /projects` validates the name as 05 §5.3
  says (`[A-Za-z0-9._-]{1,64}`; a space is refused rather than slugged as
  `features/projects.md` suggests), and refuses with `payment_required
  {reason: card_required}` when the user has no card rather than creating
  a row that can never boot. `GET /logs?kind=console` returns the console
  excerpts hostd attaches to failed ops; the full console is in Loki. The
  SSE route's `data:` is `{seq, line}` JSON and `POST /internal/sessions`
  takes `{project_id, event: opened|closed, cert_serial}` (`api.md`).
  `repose-admin` talks to Postgres directly and has no `login`; the
  runbook's `repose-admin login` line is withdrawn. Rate-limit buckets are
  per replica.
**I-43. The fragment contract is enforced by a NixOS module,
`nix/guest/contract.nix`: `repose.overlays` from a pre-pass, `repose.system`
through a static allowlist, and one class-independent closure.** (12)
`workstreams/12-nix-config-pipeline.md` sketched `composeGuest { baseRef,
fragment, menuSnippet, guestParams }` with the menu's NixOS snippet as a
separate module. The wire carries one file (`Build.fragment`, I-28), so
the snippet travels inside the fragment as `repose.system = [ { ... } ]`,
a list of plain attribute sets whose first two levels must be in
`nix/guest/system-allowlist.json`; the composer defines each allowlisted
path statically and refuses anything else through an assertion naming the
option. A hand-written fragment may use the same door under the same list,
which is what makes the boundary real whatever produced the file. Two
things the sketch could not have known: home-manager's module list itself
needs `pkgs`, so `nixpkgs.overlays` cannot be read back from the evaluated
home-manager configuration (infinite recursion); `repose.overlays` is
instead read off the fragment in a pre-pass that calls a function fragment
once with the platform's own `pkgs` and `lib`, and may use nothing else.
And `Build` carries no class, so the browser slice's ceiling is a
percentage of guest memory (37.5 percent: 1.5/3/6 GB) instead of a
per-class constant, which makes the closure serve any class (I-34). Errors
are attributed to `fragment.nix` because `compose.nix` hands the path to
home-manager unimported; the contract module is not called `fragment.nix`
so the error mapping's `fragment.nix:L:C` can only mean the user's file.
`composeGuest { fragment | fragmentPath, class, baseVersion, guestd, hook,
extraModules }` replaces the sketch's signature; `guestSystem` is
`composeGuest { fragmentPath = "${fragment}/fragment.nix"; }`. *Rejected:*
trusting the api to be the only producer of `repose.system` (nothing
distinguishes its file from a user's); overlays as a home-manager option
(recursion); a second `Build` field for the snippet (a second file, a
second override input, and the takeover flow would have two things to
copy). Interfaces: `nix-build-contract.md`, `guest-conventions.md`
(browser slice), `features/config.md` "Writing a fragment".

**I-44. The menu package is `internal/menu`; `GET /catalog` carries `kind`
and `options`.** (12, for 05 and 08) `05-control-plane-api.md` named it
`internal/nixmenu`; the workstream that owns it (12) names it
`internal/menu`, and 05's text is corrected. The catalog is a YAML file
embedded in the package; `Load` validates it and lints every `nixos`
snippet against the allowlist, so a catalog entry outside the list fails
`go test`, not a build on a host. `api.md`'s `[{id, label, group,
description}]` gains `kind` and `options` (enum id, values, default),
which the dashboard needs to render a select; the old fields keep their
meaning. A generated fragment's second line, `# repose-menu: <json>`, is
the selection, so a menu-managed project round-trips without a second
store. Playwright MCP stays nixpkgs's (02's coupling to
`playwright-driver`), so `versions.json` does not list it.

**I-45. Fragment evaluation and builds run as `nixbuild` inside a
transient scope, against a `git+file://` flake, with `allowed-uris`
derived from the base checkout's lock file, and `--show-trace`.** (12, 03)
Four things the contract as written could not do, found by running it:
`path:<checkout>/nix` copies only `nix/` into the store, and
`nix/packages.nix` builds guestd from `../.`, so the flake must be named
`git+file://<checkout>?dir=nix` (the checkout is a git clone anyway);
restricted mode refuses the locked inputs the flake machinery fetches
during evaluation unless each exact URI (`github:owner/repo/rev?narHash=`)
is in `allowed-uris`, so hostd derives that list from `flake.lock` and adds
the fragment's directory, which admits those trees and nothing a fragment
can name; a truncated trace loses the fragment's line and column for
errors raised inside the module system, so the eval passes `--show-trace`
and the mapping takes the innermost `fragment.nix:L:C`; and `systemd-run
--scope` cannot switch user, so hostd runs `setpriv` to `nixbuild` (all
capabilities dropped, no new privileges) inside the scope, with
`RuntimeMaxSec` as a backstop 30 s past `timeout`. The messages are the
workstream doc's exact first lines (`syntax error at fragment.nix:L:C,
...`, `build timed out after 30 minutes while building X`, `closure is
31.2 GB, limit is 20 GB; largest paths:`); the doc's `eval_timeout` code
is the interface's `eval_failed` with "evaluation exceeded 60 s", because
`grpc-hostd.md`'s enum is what the api and CLI switch on. `nixbuild`
exists on every host (`nix/hosts/hostd.nix`), `/var/lib/repose/builds` is
0711, and the platform cache is a host option (`repose.host.overlayCache`)
passed as `--substituters`, not a constant in hostd. *Rejected:* keeping
`internal/nixbuild` as a second package next to 03's
`internal/hostd/nixbuild` (one implementation of one contract; 03's
package is extended in place, and the fixtures stay where the contract
says).

**I-46. The agent overlay is built from upstream release binaries pinned
in `versions.json` and cached on Cachix.** (12) Claude Code from
Anthropic's release bucket (the ELF the npm installer fetches), opencode
and pi from their GitHub release tarballs (bun-compiled, dynamically
linked, `autoPatchelfHook`), Codex from its static musl tarball, Gemini
CLI from the npm registry (a single bundle with no dependencies, run with
the guest's node). `scripts/bump-agents.sh` reads each upstream's latest,
prefetches, rewrites `versions.json`, builds, runs `--version`, and with
`--pr` opens the pull request; `.github/workflows/bump-agents.yml` runs it
daily. The binary cache is the Cachix cache `repose`
(`https://repose.cachix.org`): CI pushes the seven overlay packages on
every push to `main` when `CACHIX_AUTH_TOKEN` is set, and hosts add it
through `repose.host.overlayCache` once the owner has created the cache
and pasted its public key (`ops/AZURE-SETUP.md` step 16). *Rejected:*
nixpkgs as the source (R3-19; it also now marks `gemini-cli` for removal,
which is Google's tiering change, not a reason to drop an agent that works
with an API key); an S3 bucket served by `nix-serve` on the Coolify VM (a
service to run and a signing key to keep, for a cache of public
binaries); R2 through Nix's S3 support (works, but is a second credential
in CI for no gain until Cachix's free tier is outgrown). *Revisit when:*
the cache passes 5 GB or a private overlay package appears.

**I-47. Base bumps are a planner and a runner in `internal/basebump` over
two interfaces the api implements.** (12, for 05) The api does not exist
yet, so the policy is a package with `NewPlan` (which projects a version
reaches: not held, last build not failed, running or stopped, not already
on it; spread over 24 h, 2 h for `--security`, two at a time per host),
`Runner.Run` (build, then `ApplyConfig` for a running guest; a stopped
guest is `built` and boots the closure at its next start; `kernel_changed`
ends as `needs_reboot` with the `base_update_ready` event; any failure is
`failed` with `base_update_failed` and the project keeps its base),
`Summarize` for `repose-admin base status`, `Rollback` for `base rollback`
and `StatusLine` for the base part of `repose status`. Workstream 05 wires
`Dispatcher` and `Recorder` to Postgres and the stream and schedules the
run from `base publish`. The checklist's "three projects" evidence is the
package's test until the api exists.
**I-48. virtiofsd's sandbox is `namespace`, and hostd attaches taps with
exactly the host-conventions sequence.** (14, review of 01 and 03,
2026-09-20) Two places where merged code disagreed with the merged
contract, found by reading them side by side:

- `internal/hostd/virtiofs` started virtiofsd as user `virtiofsd` with
  `--sandbox chroot`. chroot(2) needs CAP_SYS_CHROOT, and virtiofsd 1.14.0
  refuses the combination outright: `Error entering sandbox: sandbox mode
  'chroot' can only be used by root (Use '--sandbox namespace' instead)`
  (reproduced on the dev box, exit 1). Every guest create would have failed
  at step 8 on a real host, and the tempting "fix" of dropping `User=`
  would have put a root virtiofsd with the whole store in front of every
  tenant. Namespace mode is what `03-hostd.md` §5.5 and the runner's
  `bin/virtiofsd` already said; `host-conventions.md` and `01-host-nixos.md`
  said chroot and now say namespace. The host enables unprivileged user
  namespaces (`security.allowUserNamespaces`, `kernel.nix`) for this.
  *Rejected:* `AmbientCapabilities=CAP_SYS_CHROOT` on the unit (a
  capability on a process that faces tenant-controlled FUSE traffic, to
  keep a mode whose only advantage is not needing user namespaces).
  *Verify on the first host:* `systemctl status virtiofsd@<guest>` is
  active and `ls /nix/store` works in the guest; the dev box cannot run
  namespace mode itself (Ubuntu's `apparmor_restrict_unprivileged_userns`).
- `internal/hostd/net` created taps with `ip tuntap add ... mode tap` and
  attached them with a bare `ip link set master`, then added the guest to
  a set `inet repose guests { ip . tap }` that no host declares: I-18
  moved admission into the `bridge repose` table with type `ether_addr .
  ipv4_addr . ifname`, and 03's code predates that. On a host, `nft add
  element` fails and every create stops at step 6; had the set been
  declared to make it pass, taps without `learning off` and a static FDB
  entry would let a guest claim another guest's MAC and receive its
  inbound frames (the bridge learns before the nftables input hook
  drops). The `Net` interface now carries the MAC and the golden test is
  the command list from `host-conventions.md` "Network", verbatim.
  Interface text unchanged; the code follows the doc.








**I-49. The tmux-idle heuristic never reads pane content, and its metrics
carry the `repose_api_*` prefix, not `repose_notify_*`.** (13, review of 04
and 05) Two places where code merged ahead of this workstream disagreed
with `13-notifications.md` as written, found by auditing 04 and 05's
already-built pipeline against it:

- §5.4 described a `needs_input` heuristic that runs `tmux capture-pane`
  and matches prompt patterns (`❯`, `[y/N]`, ...) against the pane's last
  three lines. `internal/guestd/sample` (04) never captures pane text at
  all: `needs_input` comes only from a real hook (`RecordHook`), and the
  heuristic's only signal is whether the pane's process tree has consumed
  CPU since the last refresh, per `docs/workstreams/04-guestd.md` §5 and
  `docs/features/agents.md` (already correct). This is strictly *more*
  private than the documented design (there is no `patterns.go`, no
  `capture-pane` call to grep for, so the checklist's "pane contents are
  never logged, stored or sent" item is satisfied by construction rather
  than by discipline), and it was the right call: a hookless agent's last
  line is exactly the terminal content §5.1 says a summary must never carry
  beyond what a hook payload itself gives, and a heuristic has no hook
  payload. *Rejected:* implementing capture-pane matching to match the
  original doc (adds the exact surface area the privacy boundary exists to
  avoid, for a `needs_input` signal only three of five agents lack, and
  those three already get it from `RecordHook` once they gain a hook).
  §5.4 below is rewritten to describe the built heuristic (CPU-busy,
  90-second quiet window for hookless agents from `features/agents.md`,
  not the 30/30 split originally written); the `needs_input` row is
  removed from the heuristic's state table because no code path produces it
  outside a real hook.
- 05 gave every api metric the `repose_api_` prefix for one family per
  component (`repose_api_notify_total`, `repose_api_outbox_depth`, ...),
  not the bare `repose_notify_*` names §5.8 listed, and `EventsTotal`
  carries `{kind}` only, with `agent` and `source=hook|heuristic` never
  added (the heuristic's synthetic completions and a real hook's are the
  same `kind` in the same table; splitting them needed a label 05 had no
  reason to add before this workstream existed). This workstream adds
  `repose_api_notify_delivery_latency_seconds` (a histogram of event ts to
  delivered ts, the one 5.8 metric with no equivalent) and leaves the
  `repose_api_*` convention alone rather than renaming a dozen already-
  deployed families for one workstream's original wording: a consistent
  per-component prefix is worth more than matching a name picked before
  the component existed. §5.8 is rewritten to the real names.

Also closed here, because the pipeline existed but the specific behaviour
did not: the unsubscribe link (`GET /v1/notify/unsubscribe?token=`, a
non-expiring HMAC-signed user id, keyed by a secret auto-provisioned into
the platform pseudo-project the first time the api starts — an operator
step here, unlike `repose-admin ca init`, would leave the very first
account's unsubscribe link broken until someone remembered to run it);
`billing_stopped`'s dedicated subject line; and `host_moved` /
`snapshot_failed` actually reaching `events` (the restore result handler
and `onFail`'s `snapshot` case previously only logged or set
`projects.last_error`). `ops.Engine` gained an `EventSink` interface
(satisfied by `events.Ingest.Platform`, nil in the admin CLI's ad-hoc
engine) for this. `billing_stopped` still has no producer: workstream 09
is the one that will call `events.Ingest.Platform` for it. Interfaces:
`api.md` (`/notify/unsubscribe`).

**I-50. Gemini CLI and pi both gained hook mechanisms since 5.3's "at time
of writing" rows were written; Gemini CLI itself stopped serving
individual-tier requests on 2026-06-18.** (13, 2026-09-20) 5.3 and the
checklist require resolving "at time of writing" rows before calling this
workstream done. Checking now, against the agents' own current docs:

- **Gemini CLI** ships a hook system (`geminicli.com/docs/hooks/`,
  `google-gemini/gemini-cli` `docs/hooks/reference.md`) including a
  `Notification` hook (fires on idle, awaiting-input and tool-confirmation,
  which is exactly `needs_input`) and a post-agent-loop hook usable as
  `completed`. The tmux-idle heuristic this workstream ships for Gemini is
  therefore not "the mechanism" any more, just the fallback for a version
  where hooks are absent or the platform has not wired them.
- More urgently: Google stopped serving `gemini-cli` requests for free,
  Pro and Ultra tier accounts on 2026-06-18, replacing it with a separate,
  closed-source binary, Antigravity CLI (Google's own developer blog,
  "Transitioning Gemini CLI to Antigravity CLI"; enterprise accounts with a
  Code Assist license or a bare API key are unaffected). `nix/overlay/agents`
  still packages `gemini-cli` (I-46); for any user without an API key or an
  enterprise license, the agent DESIGN.md lists as one of five now fails to
  authenticate at all, hook or no hook. This is a product decision beyond
  this workstream's remit (DESIGN.md §11, R2-11's agent list, and 02/12's
  packaging), not something to silently patch here.
- **pi** (`earendil-works/pi`, the coding agent this platform ships) has a
  real hooks directory, `~/.pi/agent/hooks/`, with `onStop` and
  `ctx.ui.notify()`. The "its hooks if present in the shipped version"
  branch of 5.3's row is therefore live, not hypothetical.

None of this is implemented here: mapping Gemini's and pi's actual hook
JSON into `{agent, kind, summary}` needs the real binaries to verify wire
shapes against (the fixture-per-shape discipline `internal/guestd/hooks/
testdata` already follows), which this session does not have, and 04's
already-reviewed `internal/guestd/hooks` package is not this workstream's
to extend blind from search-engine snippets — a wrong mapping silently
drops or mis-labels every Gemini and pi notification, which is worse than
the honest heuristic currently in place. *Rejected:* implementing the
mapping now from documentation alone (no way to verify against a real
payload before shipping); leaving 5.3's "at time of writing" wording
unresolved (the checklist item exists precisely so this gets checked and
written down, whichever way it comes out). The heuristic stays as the
current, working mechanism for both agents; `docs/features/agents.md` and
`13-notifications.md` §5.3 are annotated to point here rather than
rewritten to describe an unverified mapping. **The owner should decide
whether Gemini CLI stays in the agent list at all**, given it no longer
authenticates for the tier most users are expected to be on.


**I-51. `guest@<id>` runs Cloud Hypervisor as the `hostd` user inside a
systemd sandbox; hostd itself stays root.** (14 follow-up, review H-2,
2026-09-20) `docs/SECURITY.md` accepted that a KVM escape lands in the
Azure VM; as built it landed as root, which is every tenant on the host,
the host's mTLS identity and its Blob credential. The transient unit now
carries `User=hostd` and the property list pinned by
`internal/hostd/guest/testdata/unit.golden`: `NoNewPrivileges`, an empty
`CapabilityBoundingSet`, `ProtectSystem=strict`, `ProtectHome`,
`PrivateTmp`, the `ProtectKernel*`/`ProtectControlGroups`/`ProtectProc`
set, `RestrictNamespaces`, `RestrictRealtime`, `RestrictSUIDSGID`,
`LockPersonality`, `SystemCallArchitectures=native`, `DevicePolicy=closed`
with `DeviceAllow` for `/dev/kvm`, `/dev/net/tun` and the guest's own
`/dev/vg-guests/g-<id>` only, `RestrictAddressFamilies=AF_UNIX AF_VSOCK`
(Cloud Hypervisor's tap ioctls use AF_UNIX sockets; AF_INET is needed only
for `--net ip=`, which hostd never passes), and `TemporaryFileSystem=
/var/lib/repose/guests` with `BindPaths=` of the guest's own directory, so
one hypervisor cannot see, let alone connect to, another guest's
`vsock.sock` (a direct line to that guest's guestd). `--seccomp true` is
written out on the argv. What the host provides for it: `hostd` in group
`kvm`; a udev rule making `dm-*` nodes with `DM_VG_NAME=vg-guests` and
`DM_LV_NAME=g-*` group `hostd` mode 0660 (snapshots and the pool stay
`root:disk`); `/var/lib/repose/guests` 0711 with each guest directory
`1770 root:hostd` (the sticky bit keeps `ch.args`, `guest.json` and
`console.log` out of the hypervisor's reach) and a `virtiofsd/`
subdirectory `0750 virtiofsd:hostd` where virtiofsd binds
`virtiofsd.sock` with `--socket-group hostd`. The socket path moved from
`<dir>/virtiofsd.sock` to `<dir>/virtiofsd/virtiofsd.sock`; nothing
outside hostd read the old path. hostd remains root (LVM, nftables, taps)
and connects to the guest's sockets with root's override.
*Rejected:* one system user per guest (`DynamicUser=`): the cleanest
separation, but taps and volumes need a known owner before the unit
exists, and the shared-uid gap it would close is already narrowed by
`DeviceAllow` and the private guests directory; recorded as review L-11
for a later pass. `AmbientCapabilities=` of any kind: Cloud Hypervisor
needs none with `kvm` group access. Dropping `ProtectSystem=strict`
because the store is on `/`: the store is read-only for the unit either
way and the unit reads only the closure's kernel and initrd.
*Verify on the first host:* `systemctl show guest@<id> -p User` is
`hostd`; `ps -o user= -p $(systemctl show -p MainPID --value guest@<id>)`
is `hostd`; `ls -l /dev/mapper/vg--guests-g--*` is `root hostd`; the
guest boots and `ls /nix/store` works inside it; `test/isolation`
`TestHypervisorRunsAsHostdUser`.
**I-52. The `obs` package fixes what §5 left to call sites, and its
component and label lists are wider than §5's by two and three.** (10)
`internal/obs` is the only place a repose binary gets a logger or a metrics
registry, and both constructors enforce the rules rather than documenting
them: the log handler adds `component` (so no call site can omit it) and
redacts the never-log field names; the registry refuses a metric outside the
`repose_` namespace or with a label outside the low-cardinality list, at
registration, so a bad name stops the binary at startup. Three list changes
were needed to describe what exists:

- Components gain `hostdev` and `hook`. §5 names six
  (`api|hostd|guestd|gateway|cli|admin`), but `cmd/hostdev` (I-17) and
  `cmd/repose-hook` (04) are separate binaries, and a Loki query that cannot
  tell hostd from the api stand-in it talks to is not worth running.
  *Rejected:* logging hostdev as `api` (its lines would be mixed with the
  real api's in the same query for the rest of the project's life).
- Metric labels are §5's list (`component, host_id, class, state, kind,
  reason, route, status`) plus the ones §5's own families use (`result`,
  `direction`, `method`, `channel`), plus `version` for `repose_build_info`
  and `phase` for I-54. §5's prose list was incomplete against its own
  tables; the tables are the law.
- `repose_host_guestd_unreachable{guest_id}` is deleted. It broke the rule
  in the same section that defined it ("`project_id` and `guest_id` are
  never labels in Prometheus"); `repose_host_guestd_lost`, the count, is the
  metric, and which guest it is comes from the `guestd_lost` log line and
  the `Warning` the api receives.

The redaction floor is docs/ops/OBSERVABILITY.md's six names plus the
never-log entries that have an obvious field name (`email`, `handle`,
`remote_url`, `prompt`, `args`, `argv`, `env`, `cmdline`, `command_line`,
`user_agent`), matched exactly rather than by prefix so that `cert_serial`,
`key_id` and `token_used` survive. Guests import `obs` for the logger only
and pay 2.5 MB of binary for the metrics and trace code that comes with one
package (19.8 MB to 22.3 MB, measured); §2 asks for one package and 2.5 MB
in a guest with a 20 GB volume is not a reason to split it.

**I-53. An operator's `Exec` argv is not logged, only its `audit_id` and
length.** (10, amends 03) hostd logged `argv` on the audited-exec path with a
comment saying it was the one place that was allowed. It is not: process
arguments are on the never-log list without an exception for operators, and
the audit trail that must carry the command is the api's `audit_log` row
keyed by the same `audit_id` (`db-schema.md`: "every Exec"). A Loki reader
with the Grafana password is not the same audience as an auditor with
Postgres access. *Rejected:* keeping it with a redaction filter (the argv of
`repose-admin exec -- cat /home/dev/app/.env` is exactly what the list
forbids, whoever typed it).

**I-54. `meter_samples` carries `guestd_ok`.** (10 and 05, independently)
§5's day-one signals and `grpc-hostd.md`'s `GuestSignals` both carry
`guestd_ok`, the api receives it on every sample, and `db-schema.md` had
nowhere to put it, so the per-guest dashboard could not show the gaps where a
guest's signals are unknown rather than zero. Workstream 10 proposed the
column and workstream 05 had already added it by the time the two merged; the
shape kept is 05's, `guestd_ok bool` with no default, because a sample from
before the column existed is honestly null rather than optimistically true.
The Per-guest resources dashboard therefore reads `guestd_ok is false`, not
`not guestd_ok`. *Rejected:* inferring it from null signals (a guest with no
sessions and a lost guestd would look the same, which is the distinction I-31
added the `partial` flag for). Interface: `db-schema.md`.

**I-55. Dashboards are generated from `ops/dashboards/gen.py` and the JSON is
committed; `ops/` is laid out as §2 says, not as PROMPTS.md says.** (10) A
Grafana dashboard is 400 lines of JSON of which four matter, and the same
panel shape appears thirty times; seven hand-maintained files drift.
`gen.py` is the source of truth, the JSON next to it is committed because
Grafana provisioning reads files, and `ops/check.sh` fails when they
disagree — the arrangement of I-38. `docs/workstreams/PROMPTS.md` says
"dashboards and alert rules under `ops/grafana/`" while the workstream doc
§2 says `ops/dashboards/` and `ops/alerts.yaml`; the workstream doc wins and
`ops/grafana/` holds Grafana's provisioning files only. *Rejected:* writing
the JSON by hand (the first panel rename proves why); keeping the generator
out of the repository and committing only its output (nobody can then
regenerate it).

**I-56. Two alerts beyond §5's eleven, and the Fluent Bit metrics port is
open on wg0.** (10) §6 describes two failures whose only symptom is silence:
Loki unreachable from a host (Fluent Bit buffers and retries forever) and
Prometheus unable to scrape a host (metering continues over the gRPC stream,
so nothing else complains). `FluentBitStuck` and `HostScrapeDown` are those,
with runbook headings of their own. Seeing the first needs Fluent Bit's own
metrics, so it serves them on the WireGuard address
(`repose.host.observability.fluentBitMetricsPort`, default 2021) and the
nftables `input` chain admits that port from `wg0` alongside 22, 9100 and
9101. *Rejected:* scraping Fluent Bit through node_exporter's textfile
collector (a shipper's health reported by a cron job that writes a file the
shipper's failure does not affect); leaving it unmonitored (a host stops
shipping logs and nobody knows until they go looking for a line that is not
there). Interface: `host-conventions.md`.

**I-57. `repose_host_build_phase_duration_seconds{phase}` splits eval from
build.** (10, for 03 and 12) §5's Builds dashboard asks for "eval vs build
time" and nothing measured either: `nixbuild.Build` runs `nix eval` and then
`nix build` and timed only the pair. The two have different caps (60 s and
30 minutes, R5-4) and different causes — a slow eval is the fragment, a slow
build is a substituter or a source build — so a single number cannot answer
the question the panel asks. `nixbuild.Result` now carries both durations,
the manager observes them, and `build_done` logs `eval_ms` and `build_ms`.
*Rejected:* parsing the phase out of the build log (the log is the tenant's
Nix output, not a metric source).

**I-58. `repose-hook` reads `REPOSE_HOOK_AGENT`, the name the wrappers
export.** (10, fixing 02 and 04) The Go binary of workstream 04 read
`REPOSE_AGENT`; the wrappers of workstream 02
(`nix/overlay/agents/wrap.nix`) export `REPOSE_HOOK_AGENT`, which is also
what `docs/interfaces/guest-conventions.md` documents; and `nix/flake.nix`
ships the Go binary in every guest. So every agent hook in every guest read
an empty agent name and exited without posting: no `agent_event`, no
notification, and nothing in any log to say so. The guest-base VM test found
it by waiting 15 minutes for a hook that could never arrive.

The binary now prefers `REPOSE_HOOK_AGENT` and keeps `REPOSE_AGENT` for one
release, and accepts the socket under both `REPOSE_HOOK_SOCKET` (its own
name) and `REPOSE_HOOKS_SOCKET` (the shell implementation's). *Rejected:*
changing the wrappers instead (the interface doc names the variable, and a
wrapper is what a user's own agent config may already set); keeping two names
permanently (a second name for the same thing is how a grep misses half the
uses). *Why this workstream:* `agent_event` is one of the events
docs/workstreams/10-observability.md §5 requires guestd to emit, and it could
not fire. Interface: `guest-conventions.md`.

**I-59. `internal/obs` is three packages, because a guest pays for what it
imports.** (10, amends I-49) §2 asks for "one Go package used by every
binary", and one package it was until the guestd VM test failed on
`docs/workstreams/04-guestd.md` §7's budget: guestd's resident memory came to
20.2 MB against a 20 MB limit, because importing `obs` for the logger linked
in the Prometheus client and the OpenTelemetry SDK with their package
initialisers. guestd has no metrics endpoint and no traces of its own — it
speaks vsock and nothing else — so it was paying 2.5 MB of binary and 700 KB
of RSS for code it cannot reach.

The split follows the dependency weight: `internal/obs` is logs and names
(stdlib only: the logger, the component and event lists, the request-id
context helpers), `internal/obs/metrics` is the Prometheus registry and the
api and gateway families, `internal/obs/instrument` is the OpenTelemetry
setup, the gRPC options and the api's HTTP middleware. The rules are enforced
in the same places as before, and the naming test covers all three. guestd now
links neither heavy dependency: `go list -deps ./cmd/guestd | grep -cE
'prometheus|opentelemetry'` is 0, and the stripped binary is 13.7 MB.
*Rejected:* raising 04's budget (the budget exists because guestd competes
with the agent for two vCPUs and 4 GB, and it was right); keeping one package
and hoping the linker drops the unused half (package initialisers are always
kept, which is what the measurement showed); a build tag (a binary whose
behaviour depends on how it was built is worse than a package boundary).

**I-60. One observability package, one tracing setup, one api metric family.**
(merge of 10 with wave two, 2026-09-20) Workstreams 05 and 10 each built what
they needed while the other was unmerged, so the merge found three pairs:

- `internal/obs`. 05's version said in its own header "Workstream 10 owns the
  naming rules; this is the subset the api needs", so 10's is the package and
  05's is gone. Two things of theirs were better and were kept: redaction
  matches a *substring* of the field name, because a call site writes
  `access_token` and `user_email` rather than the bare word, with an exact
  allowlist for the ids and counts that contain one (`cert_serial`,
  `key_version`, `secrets_count`, and 10's own `argv_len` and
  `summary_bytes`); and `WithLogger`/`Logger(ctx, fallback)`, which is how the
  api gives every handler the request's fields.
- `internal/otel` and `internal/obs/instrument`. Both installed the OTLP
  exporters and the noop provider. 05's had the bug 10's on-path test caught
  the day before: `resource.Merge` of `resource.Default()` (schema 1.43.0)
  with a `semconv/v1.26.0` resource returns "conflicting Schema URL", so the
  api would have refused to start the moment anyone set
  `OTEL_EXPORTER_OTLP_ENDPOINT`. `internal/otel` is gone; `SetupTracing`
  gained the standard `OTEL_EXPORTER_OTLP_PROTOCOL` switch so 05's OTLP/HTTP
  deployment and 10's gRPC one both work, and a test covers each. The OTLP
  *metric* exporter 05 also installed is not replaced: DESIGN §15 and §5 make
  Prometheus the metrics path, and pushing metrics to a collector nobody runs
  is weight without a reader.
- The api metric family. 05 implemented every name §5 lists (and more) in
  `internal/api/metrics`; 10's `APIMetrics` is gone, and
  `families_api_test.go` holds 05's to §5 the way `families_host_test.go`
  holds hostd's. The api's registry now comes from `internal/obs/metrics`, so
  all 26 of its series are checked for the namespace and the label list at
  startup — which is how `repose_api_secrets_ops_total{op}` was noticed and
  `op` added to the list as the bounded enum it is. `HTTPMiddleware` went the
  same way: 05's server already emits the `request` event with a request id.

Three gaps the same check found in 05's code, fixed here: the no-capacity
placement path logged nothing and counted nothing (`schedule_fail` and
`repose_api_schedule_total{result}`), the partition maintenance failure had no
series for the `PartitionDropFail` alert to read, and `admin_action` had no
producer — it moves from the api's required events to the admin CLI's, because
05 built `repose-admin` against Postgres directly and the line belongs where
the `audit_log` row is written. `stripe_webhook` stays required of the api and
is listed in `obs.PendingEvents` as owed by workstream 09, which has no
webhook route yet.

*Why this workstream made the calls:* `docs/workstreams/README.md` gives 10
the metric and log naming, and a merge that keeps both of everything is how
`repose_api_*` ends up meaning two things. *Rejected:* keeping 05's obs and
deleting 10's (it has no component enum, no event registry, no source lint and
no metrics enforcement); keeping both tracing setups behind a flag.

**I-61. The store export bind is made private before `.links` is masked.**
(m1 integration, 2026-09-20) On host-01 every store write failed with
`Read-only file system` on `/nix/store/.links`: the tmpfs mask
`repose-store-export.service` mounts over `/run/repose/store-export/.links`
had propagated onto `/nix/store/.links`, because a bind mount joins its
source's peer group and NixOS mounts `/` shared. `nix copy` into the host,
`nix-store --optimise` and hostd's `Build` all write there. The unit now
runs `mount --make-private` on the export before the remount and the mask,
and the host-services VM test asserts `/nix/store/.links` is not a mount
point. *Rejected:* dropping the mask (the enumeration leak 01 §5 closes);
masking with a bind of an empty directory (propagates the same way).

**I-62. virtiofsd's socket lives in a subdirectory it owns, and hostd fails
step 8 when virtiofsd exits.** (m1 integration, 2026-09-20) The first
guest on host-01 died a minute after start with "guest did not become
ready": virtiofsd, which I-48 runs as the unprivileged `virtiofsd` user in
namespace sandbox mode, had exited at once because it could not create
`virtiofsd.sock` in the root-only guest directory (0750 under a 0700
parent), and Cloud Hypervisor retried the missing socket for 60 s. hostd
now creates `/var/lib/repose/guests/<id>/` as root 0710 with group
`virtiofsd` and `<id>/virtiofsd/` owned by that user, the socket is
`virtiofsd/virtiofsd.sock`, `/var/lib/repose/guests` is a 0710
root:virtiofsd tmpfiles directory instead of a `StateDirectory`, and step 8
waits up to 10 s for the socket while checking the unit, so a dead
virtiofsd fails the create as step 8 with its unit named. *Rejected:*
making the guest directory group-writable (virtiofsd could then rewrite
`ch.args`, which hostd hands to a root Cloud Hypervisor at the next start);
running virtiofsd as root in chroot mode (what I-48 moved away from);
socket activation through a transient socket unit (an fd-passing path
nothing else in hostd uses). Interface: `host-conventions.md` (guest
directory row and the CH invocation).

**I-63. The guest disk is passed to Cloud Hypervisor with
`image_type=raw`.** (m1 integration, 2026-09-20) With the type
auto-detected, Cloud Hypervisor 53 logs "Autodetected raw image type.
Disabling sector 0 writes" and rejects the guest's first write to sector 0;
ext4 keeps its primary superblock there, so `/sysroot` failed to mount with
`I/O error, dev vda, sector 0` and the initrd dropped to emergency mode
(console log of host-01's second guest). hostd names the type explicitly
in `ch.args`. Interface: `host-conventions.md` (the CH invocation).

**I-64. guestd binds its vsock listener to any CID.** (m1 integration,
2026-09-20) `internal/vsockrpc.Listen` bound `VMADDR_CID_HOST` (2), which a
guest kernel refuses with `cannot assign requested address`; guestd
restarted every two seconds and never sent `Ready`, so the first fully
booted guest on host-01 failed create at step 10. The dev-socket mode and
the QEMU VM test never exercise the vsock bind, which is why it survived
until a real host. The listener now binds `VMADDR_CID_ANY`.

**I-65. virtiofsd does not announce submounts.** (m1 integration,
2026-09-20) Inside the first running guest on host-01 every nix client got
`Connection reset by peer` and `journalctl -u nix-daemon` said `creating
directory "/nix/store/.links": Object is remote`. The store export masks
`.links` with a tmpfs (01 §5), virtiofsd 1.14 announces that mountpoint to
the guest by default, the guest kernel mounts it as its own virtiofs
submount under `/nix/.ro-store/.links`, and overlayfs refuses lookups that
cross a mount boundary inside a lower layer with EREMOTE. The nix daemon
creates `.links` at startup, so it died on every connection, which also
failed `home-manager-dev.service` at boot. hostd now passes
`--no-announce-submounts`; the guest sees an ordinary empty directory and
the enumeration leak stays closed. *Rejected:* dropping the mask (01 §5's
reason stands); a guest-side `nix.conf` workaround (the daemon creates the
directory unconditionally).

**I-66. `ResizeVolume` on a running guest calls Cloud Hypervisor's
`vm.resize-disk` between `lvextend` and `GrowFs`.** (m1 integration,
2026-09-20) On host-01 a resize from 40 to 60 GB returned ok, LVM showed
60 GB and guestd ran `resize2fs`, but the guest's `/dev/vda` still reported
40 GB: virtio-blk keeps the capacity the device was created with until the
hypervisor is told. hostd now calls `vm.resize-disk` on `_disk0` first; a
stopped guest picks the size up at its next boot as before.

**I-67. hostd registers the guest's closure in the guest's nix database:
`RegisterPaths` after `Ready`, and `registration` inside `Switch`.** (m1
integration, 2026-09-20) Inside the first running guest on host-01,
`nix path-info /run/current-system` said "is not valid": the guest's
database is created empty on its thin volume and the shared store puts
paths on disk without registering them. So `ApplyConfig` failed at
guestd's `nix-env --set` ("nix-env exited 1"), `home-manager-dev.service`
failed at every boot, and a user `nix` command touching a system path
would have tried to fetch it. The host has the metadata: hostd now sends
`nix-store --dump-db` of the closure (480 KB for the 6 GB base) as
`RegisterPaths` right after `Ready` and as the `registration` field of
every `Switch`; guestd runs `nix-store --load-db` (idempotent) and writes
`/run/repose/paths-registered`, which `repose-paths.service` waits for
before `home-manager-dev.service` runs. *Rejected:* a registration file in
the shared store named on the kernel command line (a store path that would
need its own GC root and a second delivery path); computing hashes in the
guest (`nix-store --load-db` needs the NAR hashes only the host has);
skipping `nix-env` in `Switch` (leaves the database wrong for every later
nix command). Interfaces: `vsock-guestd.md`, `guest-conventions.md`,
`proto/repose/guestd/v1/guestd.proto` (old shape accepted: an empty
registration means the previous behaviour).

**I-68. Reconcile removes `snap-*` volumes left by an interrupted
snapshot.** (m1 integration, 2026-09-20) `kill -9` of hostd on host-01
between the LVM snapshot and its removal, with a Blob upload in flight,
replayed the Snapshot command correctly after the restart (same
`command_id`, a fresh snapshot, result ok) but left
`snap-<guest>-<ts>` from the killed attempt in `vg-guests`, holding thin
pool space for ever. Reconcile at start now removes every `snap-*`
volume: hostd has no snapshot in flight when it starts, and a replayed
command makes its own. *Rejected:* naming the snapshot after the
`command_id` and reusing it on replay (an upload that died half way would
resume from a snapshot taken before the guest wrote more, which is
correct but the same as a fresh one, for extra state).

**I-69. The `virtiofsd` user is in group `hostd`.** (m1 integration,
2026-09-20) The first create on host-01 under the I-49 sandbox failed at
step 8: virtiofsd logged "`<guest dir>/virtiofsd` does not exist or is not
a directory" because the guest directory is `1770 root:hostd` and the
virtiofsd user was in no group but its own, so it could not traverse it;
and `--socket-group hostd` needs the same membership, since an
unprivileged process can only chgrp into a group it belongs to. The user
gains `extraGroups = [ "hostd" ]`. What that widens: virtiofsd can create
files in a guest directory before it sandboxes itself (the sticky bit
keeps it from removing hostd's, and `ch.args` is `0640 root`); it gains
nothing under the store export, which is what the user exists to protect.
*Rejected:* `1771` on the guest directory (does not fix the chgrp); a
socket directory under `/run` outside the guest directory (a second
layout for one file).

**I-70. A secret or principal push to a running guest is an op, so a
lifecycle request issued in the same second can answer 409.** (merge,
2026-09-20) `PUT`/`DELETE /projects/:id/secrets/:name` and `SetPrincipals`
queue an `update_secrets` op the caller gets no id for; `stop`, `start`,
`resize` and `destroy` answer `409 conflict "an operation is in progress"`
while it runs, usually well under a second. The CLI (07) retries such a 409
for up to 10 s before surfacing it; the api test harness drains with
`WaitIdle`. *Rejected:* exempting `update_secrets` from the one-op rule
(a stop racing a secrets push is exactly what the rule prevents).
**I-71. The control plane is created now, with its Coolify pinned and its
dashboard off the network.** (11, owner, 2026-09-20) `coolify_count = 1` in
`prod.tfvars`. I-24 deferred the VM until "wave 3" on the grounds that it
would bill for nothing while the api did not exist; the api and `repose-admin`
are merged, so the trade has flipped. Three things were settled with it:

- **The Coolify release is pinned** (`coolify_version`, `4.3.23`) and
  `AUTOUPDATE=false`. The installer's own default is the moving `latest`, so
  the VM could not be rebuilt onto the version it had been running, and the
  thing that deploys the api could upgrade itself overnight. *Rejected:*
  tracking `latest` and pinning nothing (a rebuild after a loss is the moment
  a version surprise is least affordable).
- **Coolify's dashboard is not in the NSG.** It listens on 8000 over plain
  HTTP and, before an admin account exists, anyone who reaches it can create
  one. Operators reach it with `ssh -L 8000:127.0.0.1:8000`, on the port the
  NSG already opens to `operator_cidrs`, and the control subnet's NSG carries
  a `postcondition` that fails the plan if 8000, 6001, 6002 or `*` ever
  appears as an inbound rule. *Rejected:* opening 8000 to `operator_cidrs`
  (an unauthenticated admin panel on the internet for the length of one
  setup, and NSG lists are edited more often than they are re-read).
- **The apply waits for Coolify to be healthy.** `terraform_data.ready` reads
  the `coolify` container's Docker health status — the signal the installer
  itself waits on — and also fails when `rclone` or `pg_restore` is missing,
  because those are the first two commands of the runbook's restore
  procedure. *Rejected:* returning as soon as the VM boots (the operator
  cannot tell a machine still pulling images from one whose cloud-init died
  twelve minutes ago).

The cost re-query this forced corrected the estimate: the `D4s_v7` is
$0.265/h, not the $140.16 a month the 2026-09-19 table carried, so the control
plane is about $237 a month and the environment about $1,094, over the $1,000
budget alert (`ops/AZURE-SETUP.md` step 7). Setting `coolify_count` back to 0
destroys the VM, its OS disk, Postgres and every Coolify application
definition; the retention that matters is the R2 dump *and*
`/data/coolify/source/.env`, whose `APP_KEY` decrypts the credentials in that
dump. `ops/coolify.md` is the click path and holds that last point where it
will be read before a restore rather than after one.

**I-72. `manage_dns` defaults to false, and the absence of a record is not
the absence of an answer.** (11, 2026-09-20) The roots defaulted `manage_dns`
to true while no `CLOUDFLARE_API_TOKEN` existed, so every plan depended on the
local tfvars turning it off, and a plan without them failed inside the
Cloudflare provider with an authentication error naming neither the variable
nor the step that creates the token. It now defaults to false in both roots,
`infra/dns` validates the zone id where a null one would otherwise reach the
provider, and `make plan ENV=r2` fails on the missing token with the step that
creates it.

The reason this matters beyond tidiness was found by resolving the names on
2026-09-20: **`herakraft.co` answers every name under it from a proxied
wildcard record.** `ssh.repose.herakraft.co` therefore resolves today, to
Cloudflare's proxy, which carries neither SSH nor WireGuard — so a user
following the documented hostname, and any host configured with it as a
WireGuard endpoint, fails in a way that looks like a firewall problem. The
environment module raises it as a plan-time `check` warning whenever
`manage_dns` is false, `infra/README.md` has the four records to create by
hand until a token exists, and `ops/RUNBOOK.md` has it as a symptom entry.
*Rejected:* creating the records by hand and saying nothing (the next person
to read `dig` output would have to rediscover the wildcard); making
`manage_dns` a required variable (a plan-only CI run has no business
supplying a Cloudflare value).

**I-73. Points 07-cli.md and cli-config.md left implicit, settled while
building `cmd/repose`.** (07, 2026-09-20)

- *Remote normalisation lowercases the whole string, not only the host.*
  cli-config.md's rule said "lowercase host", but its own worked example
  (`git@github.com:A/B.git` and `https://github.com/a/b` both become
  `github.com/a/b`) only holds if the path is lowercased too. The doc is
  corrected to match the example, which is what a case-insensitive host
  like GitHub's actually needs.
- *Logto's OIDC endpoints live under `/oidc` relative the issuer.*
  `internal/api/auth` already hits `<issuer>/oidc/jwks` and
  `<issuer>/oidc/token` off the same `logto_issuer` value, and 07-cli.md's
  own device-code step names `POST /oidc/device/auth`; the CLI's discovery
  fetch is `<issuer>/oidc/.well-known/openid-configuration`, matching, and
  its login's OAuth client id is `repose-cli` (Native, PKCE loopback and
  device code), the same one `ops/AZURE-SETUP.md` step 12 and
  `ops/RUNBOOK.md` already name. *Not verified against a real Logto*: no
  self-hosted instance is reachable from this workstream's dev box; the
  path is inferred from the api's own two call sites, which is the closest
  evidence available. *Revisit when:* the first real `repose login` runs
  against the deployed Logto (M2's gate).
- *`repose resize SIZE` is a hidden cobra command, not documented in
  `--help`.* 07-cli.md §5.6 says it exists as "a hidden alias" and is
  "document[ed] in `features/config.md` only"; `cobra.Command.Hidden`
  is that hiding mechanism.
- *`ensureCert` recovers from `rate_limited` only when a certificate is
  already on disk with validity left*; with none, the error surfaces
  as-is (07-cli.md doesn't say what happens with no certificate at all to
  fall back to, and there is nothing sensible to reuse).
- *The Docker fixture `test/guest-sshd/` 07-cli.md §7 names is
  `internal/testguest` instead*: an in-process Go SSH server running real
  `git`, `tar` and `tmux` against a scratch `$HOME`, authenticating with a
  plain key rather than the CA certificate chain (that chain is
  `internal/ca/testca`'s and `ssh-gateway.md`'s contract, exercised by
  `cert_test.go`). The same trade 06-gateway-edge made for its own tests
  ("an in-process SSH server standing in for a guest"). *Rejected:* the
  Docker fixture (a container dependency in `go test` for behaviour a Go
  SSH server already reproduces exactly: real git, real tmux, over a real
  SSH session).
- *`PatchMeRequest.Notify.NtfyURL` is `**string`*, not `*string`: the api
  needs to tell "clear the URL" (JSON `null`) from "leave it alone" (field
  omitted), which a single pointer with `omitempty` cannot express — a nil
  outer pointer omits the field, a non-nil one pointing at a nil inner
  pointer marshals to `null`.

**I-74. The host reaches its guests through a declared `ct direction reply`
rule, not a rule an operator inserts by hand.** (01/03 follow-up,
2026-09-20) Until the gateway exists (workstream 06), an operator reaches a
guest by jumping edge → host → guest, and the host's `input` chain sends
every frame from `br-guests` to `guest_in`, which dropped all but
rate-limited ICMP: the host could open a TCP connection to a guest and
never see the reply. The M1 session worked around it with a runtime
`nft insert rule inet repose input iifname "br-guests" ct state
established,related accept` that a `systemctl reload nftables` or a reboot
removed, and that also let a repeated ICMP echo from a guest count as
established and skip the rate limit. `guest_in` now starts with
`ct direction reply ct state established,related accept`: only packets in
the reply direction of a flow the host itself opened match, so a guest's
own first packet is still dropped and the ICMP limit still holds, and the
rule survives a reload because it is in the ruleset. *Rejected:* keeping it
manual until 06 (an operator procedure that a reload silently undoes, and
`hostd` has no other way to reach a guest's sshd today); narrowing it to
tcp sport 22 (the host also curls a guest's noVNC relay, and "replies to
what the host opened" is the honest rule).

**I-75. `repose.host.apiCAFile` names an api CA that only exists at run
time.** (01/03 follow-up, 2026-09-20) `repose.host.apiCA` puts a PEM in the
store, which is how host-01 names the `hostdev` CA (I-40), but the CA the
host-services VM test registers against is generated when its `hostdev`
state is built, and reading it back at evaluation time would be an import
from derivation in every `nix flake check`. The option takes a path
instead, passed straight to `hostd --api-ca`, and an assertion refuses both
being set. It is also what a host whose CA is delivered beside the join
token needs. *Rejected:* IFD on the generated CA (a `flake check` that
builds a derivation to evaluate); a fixed CA keypair committed under
`nix/hosts/tests/fixtures` (a private key in the repository, and `hostdev`
has no flag to adopt one).

**I-76. hostd takes an identity `repose-register.service` wrote while it was
running.** (01/03 follow-up, 2026-09-20) `EnsureIdentity` read the state
directory once and then looped on the join token alone, so a hostd that
started before the token arrived kept logging `waiting for join token` for
ever after the register unit consumed that token and wrote `host.json`:
only a restart moved it. On a host the unit is ordered before hostd, so
this is the operator path of ops/RUNBOOK.md (write the token, then
`systemctl start repose-register`). The `ErrNoToken` branch now re-reads
the identity before waiting again. Pinned by
`TestEnsureIdentityTakesTheIdentityTheUnitWrote`, which fails on the old
code. *Rejected:* watching the state directory with inotify (30 s is soon
enough for a host that has just booted); having the unit restart hostd (a
restart in the middle of registration is what `RestartPreventExitStatus=3`
exists to avoid).
**I-77. Usage records are Stripe billing meter events, not subscription-item
usage records.** (09) `09-billing.md` §5.5 was written against Stripe's
`usage_type = metered` / `aggregate_usage = sum` prices and the
`POST /v1/subscription_items/{id}/usage_records` endpoint with `action =
increment`. Stripe has retired that model: the current API and every
maintained SDK (`stripe-go` v83, which this repo now depends on) expose
billing **meters** and `POST /v1/billing/meter_events` instead, and there is
no `usagerecord` package left to call. The shape §5.5 asked for is kept
where it matters and moved where it cannot be:

- Still three lines per period, still cents as the unit: one product, three
  metered prices at 1 cent per unit, each attached to a meter
  (`repose_compute_cents`, `repose_storage_cents`, `repose_egress_cents`),
  and one subscription per user carrying all three, created at first card
  attach with the period anchored then.
- The idempotency key §5.5 specified becomes the meter event's
  `identifier`, `usage:<project_id>:<hour>:<part>`, which Stripe enforces as
  unique over a rolling window of at least 24 hours. It is sent as the HTTP
  idempotency key as well, so a retried push is deduplicated twice.
  `usage_hours.stripe_usage_record_id` holds the `usage:<project_id>:<hour>`
  base and a row that has one is never pushed again, exactly as §5.5 says.
- The `credit_cents` price §5.5 mentioned in passing is still not created: a
  negative meter event is not allowed, so the trial credit is consumed
  before the push and the invoice carries a memo line.
- Reconciliation reads the other side back with
  `GET /v1/billing/meters/{id}/event_summaries`, which is why the
  `STRIPE_METER_ID_*` variables exist alongside the event names; without
  them `repose-admin billing reconcile` says it could not compare rather
  than reporting every account as a mismatch against zero.

*Rejected:* pinning an old `stripe-go` that still has usage records (a
payments library frozen at a version that will stop being served); writing
the retired endpoint by hand over `net/http` (the same bet, minus the
library); Stripe's newer `/v2/billing/meter_events` stream (higher
throughput, at-least-once semantics and a separate session object, for a
push of at most one event per project per hour). *Also recorded:* the
webhook endpoint must be created with the SDK's API version
(`stripe.APIVersion`, `2025-10-29.clover` today) or `webhook.ConstructEvent`
refuses every delivery as a version mismatch; `ops/AZURE-SETUP.md` step 17
says so, and the SDK's strictness is kept rather than disabled because an
object rendered under another version may deserialise wrongly, which in this
package means the wrong amount. Interfaces: `db-schema.md`, `api.md`
(`POST /billing/webhook`), `09-billing.md` §5.5.

**I-78. The rollup lives in `internal/billing`, the billing period is
anchored at signup and stored on every row, and `users.trial_credit_cents`
is a trigger-maintained projection of the ledger.** (09, amends 05) Three
places where the code the api already had did not match what
`09-billing.md` asks for, resolved in the workstream doc's favour:

- *Where it lives.* Workstream 05 built the hourly rollup as
  `internal/api/meter.Rollup` with a price table in `internal/billing`
  (I-42: "when 09 lands, the api swaps the implementation behind the same
  interfaces"). §2 puts the rollup in `internal/billing`, and that is where
  it is now; `internal/api/meter` keeps the sample ingest, which is the half
  §3 says this workstream does not own. `internal/obs/obslint` counts
  `internal/billing` as the api component, because it runs in the api
  process.
- *The period.* §5.1 says "month" is the user's Stripe billing period
  anchored at signup, not the calendar month; 05's rollup used
  `date_trunc('month')` for the cap, the egress allowance and the storage
  remainder. `users.billing_anchor` is that anchor, periods are counted from
  it with Stripe's short-month clamping (an anchor on the 31st bills on the
  28th in February and returns to the 31st in March), and `period_start` /
  `period_end` are written on every `usage_hours` row so the running-total
  query cannot drift from the rule that priced the row. The storage
  remainder resets at each period start, which is what makes a period sum to
  exactly `gb_alloc * 10` for a period of any length rather than only for a
  30-day one.
- *The balance.* §5.3 rejected caching the balance on `users` because "two
  hourly jobs racing would drift it", but `users.trial_credit_cents` exists
  and `/me` and the card gate both read it. It is kept as a projection
  maintained by an `after insert` trigger on `credit_ledger`, so it is
  updated inside the same transaction and under the same row lock as the
  ledger row; the drift the section rejected cannot happen, and
  `sum(credit_ledger.cents)` is still the balance of record. A usage debit
  that is recomputed differently on a re-run is corrected by an
  `adjustment` row, never by editing the original: the ledger is
  append-only.

Also settled here: the cap for a project that changed class mid-period is
the cap of the largest class it ran in during the period (§5.1's "bounded by
the larger class's cap"), carried as `Inputs.CapClass`; an exempt account
(I-16) accrues `usage_hours` and debits its credit ledger like anyone else
but is marked `stripe_usage_record_id = 'exempt'` instead of being pushed;
and the billing metrics keep the `repose_api_*` prefix of I-49 and I-60
rather than §9's `repose_billing_*` wording, so the two new families are
`repose_api_billing_stripe_push_backlog_seconds` and
`repose_api_billing_mismatch_cents` next to the existing
`repose_api_billing_gap_minutes_total` and `repose_api_rollup_duration_seconds`.
*Rejected:* leaving the rollup in `internal/api/meter` and adding the period
there (two packages owning one rule); computing the period from
`created_at` at read time instead of storing it (a period boundary that
moves when an anchor is corrected silently reprices history); renaming a
dozen deployed metric families for one workstream's original wording.
**I-79. The api's user-facing routes answer CORS on every response, with a
wildcard origin.** (08, found running the dashboard against a real browser)
`repose.herakraft.co` and `api.repose.herakraft.co` are different origins,
and nothing in 05's implementation or `api.md` set a CORS header, so a
browser blocks the dashboard's very first `fetch` with `blocked by CORS
policy` — a login was never enough to reach `GET /me`; every request failed
before this fix, found only because 08 ran the real Logto-plus-fetch flow
in an actual browser rather than trusting `internal/fakes/api`, which had
the same gap and returned successful test runs anyway. `Server.wrap`'s
`http` branch (not `internal`, which the gateway calls over mTLS, never a
browser) now sets `Access-Control-Allow-Origin: *`,
`Access-Control-Allow-Methods`, and `Access-Control-Allow-Headers:
Authorization, Content-Type` on every response, and answers `OPTIONS`
preflights with 204 before the request reaches the mux (which has no
`OPTIONS` handlers registered and would otherwise 404 them).
`internal/fakes/api` gets the same middleware, so a consumer testing
against the fake sees the same behavior the real api now has. *Rejected:*
an explicit origin allowlist (every route here is bearer-token
authenticated, never cookie-based, so a wildcard origin leaks no ambient
credential a page could ride on; an allowlist only adds an origin to
maintain in step with `repose.herakraft.co`'s eventual own domain, DESIGN
§16); a reverse proxy adding the header in front of Coolify (a second place
to keep in sync with the route list, for a header the api can set once).
Interface: none changed, `api.md`'s routes and bodies are the same; this is
a missing behavior the doc's "cli, dashboard, gateway" consumer list already
implied.

**I-80. `internal/fakes/api`'s catalog gains `kind` and `options`, and a
fragment containing `repose-force-eval-error` answers the first canonical
`eval_failed` message instead of applying.** (08) Two gaps between the fake
and what it fakes, found writing the dashboard's tests against it:
`CatalogItem` predated I-44 and had no `kind` or `options` field, so the
Menu tab's "Services" grouping and per-package option selects (config.md
"Menu versus fragment") had nothing to render; and `PUT /config` always
answered `applied` with no way to reach the config page's error-block and
fragment-line-highlight path (nix-build-contract.md "What the user reads")
without a real Nix evaluation. Both are additions to the fake only —
`api.md` already documented `kind`/`options`, and nix-build-contract.md's
messages are unchanged, so no interface doc moves. *Rejected:* a build flag
or admin endpoint to toggle the failure globally (a fragment-content marker
composes with parallel tests without shared state; the fake's existing
`Fail`/`FailNext` switch is per-route, not per-payload, so it cannot express
"this specific fragment fails").
**I-81. The gateway's metrics live in `internal/obs/metrics` as an extended
`GatewayMetrics` family, and `auth_fail_total`'s reason enum is the union the
gateway actually distinguishes.** (06, 2026-09-20) Workstream 10 owns metric
naming (`AGENTS.md`, `workstreams/README.md`) and had already merged a
`GatewayMetrics` with five series (`repose_gateway_sessions`,
`sessions_total`, `auth_fail_total{reason}`, `dial_fail_total`,
`route_duration_seconds`) wired into `ops/dashboards/gateway.json` and the
`GatewayAuthSpike` alert. `06-gateway-edge.md` §5.5 listed a richer set under
different names (`connections_open`, `auth_total{result}`,
`dial_errors_total`, `session_seconds`, `revocation_cache_age_seconds`,
`wgsync_*`, `relay_bytes_total`). Rather than a second gateway metrics
package that the dashboards would not match, this workstream extends 10's
`GatewayMetrics` in place with the missing series (`relay_bytes_total`,
`session_seconds`, `revocation_cache_age_seconds`, `wgsync_peers`,
`wgsync_errors_total`, `hook_events_total`), keeping 10's five names for the
overlap (`connections_open` becomes `sessions`, `dial_errors_total` becomes
`dial_fail_total`, and `auth_total{result=ok}` is dropped because an accepted
relay is already counted by `sessions_total`). `AuthFailReasons` grows from
10's `{bad_cert, expired, revoked, wrong_principal, stopped, not_found}` to
the reasons the gateway can tell apart: `{no_cert, bad_ca, expired, revoked,
wrong_principal, stopped, route_error, rate_limited, not_found, bad_login,
busy}`. The `GatewayAuthSpike` alert is a `rate()` over the whole counter and
the dashboard groups by reason, so both survive the wider enum; the runbook's
`bad_cert` references become `no_cert`/`bad_ca`. The same reason string is the
`reason` field of the `auth_fail` log event. *Rejected:* a `internal/gateway`
metrics package (the dashboards reference `internal/obs/metrics`' names, and
two families for one component is what I-45 and I-59 already refused
elsewhere); mapping the gateway's finer results onto 10's six reasons (a
scan showing as `bad_cert` and an api outage showing as the same reason hides
the distinction the runbook's `GatewayAuthSpike` triage needs). Interfaces:
`internal/obs/metrics/families.go`, `06-gateway-edge.md` §5.5,
`10-observability.md` §5.

**I-82. The gateway relay closes the client channel only after the guest's
in-flight request replies are delivered, and the connection tears down guest
first.** (06, 2026-09-20) Two ordering bugs found by the §7 soak (100
concurrent relays): an OpenSSH-style client surfaces a command's exit status
only when the channel's request stream closes, so the gateway must send the
full `CHANNEL_CLOSE` (not just EOF) once the guest is done; and the reply to
a client's `exec` travels on the client channel, so a guest that finishes and
closes faster than that reply propagates would have the gateway close the
client channel first, failing the client's request with `EOF` and discarding
the buffered output. The relay therefore closes the client channel only after
(a) all guest-to-client data is flushed and (b) no client-request reply is
still in flight, and on connection teardown it closes the guest side first
and drains the relays before closing the client transport. *Rejected:* a raw
bidirectional `io.Copy` with a single close on first EOF (loses exit status
and truncates output under load); a fixed delay before closing (a race is not
a timing constant). Interface text unchanged; the behaviour is in
`internal/gateway/relay.go`.

**I-83. The control VM is a server managed by the owner's existing Coolify
instance; Coolify itself is not installed on it.** (conductor, owner,
2026-09-20) The interview settled that the control plane "will be added to
Coolify", meaning the instance the owner already runs on their personal
server (which also runs Logto, Loki and Grafana). Workstream 11's text
turned that into a second Coolify installed by cloud-init on the control
VM, and the control-plane session built and applied it. The owner caught it
on first contact. The VM now boots to what Coolify's "Validate & configure"
expects of a server: root login by key (operator keys plus the instance's
own public key, `coolify_public_key` in `prod.tfvars`), Docker Engine with
the compose plugin from Docker's apt repository, `rclone` and
`postgresql-client` for the restore procedure, the WireGuard peer, and
nothing listening but sshd. The control NSG opens 22 to
`coolify_manager_cidrs` (the instance's address, `prod.local.tfvars`) next
to `operator_cidrs`; 80 and 443 stay as they were, because the proxy
Coolify installs on the server is what terminates TLS for
`repose.herakraft.co`, `api.` and `auth.`. `terraform_data.ready` checks
those facts instead of a `coolify` container's health. Consequences: there
is no admin account, no port 8000, no `APP_KEY` and no `.env` on this VM;
all of those belong to the owner's instance and its own backup. The
platform Postgres, its R2 backup job, Logto, the api and the dashboard are
still resources of that instance deployed onto this server, so
`docs/ops/coolify.md`'s click path survives with "localhost" replaced by
the added server. The live VM was converted in place (Coolify's containers,
network, images and `/data/coolify` removed, the key added), which the
`custom_data` `ignore_changes` on the VM makes equivalent to a rebuild.
*Rejected:* a second Coolify (two control planes to upgrade, back up and
log into, for one api); Coolify's "localhost" server on the personal server
itself (the api must sit in the Azure VNet next to the edge and the hosts,
DESIGN §15). `coolify_version`, `coolify_autoupdate` and
`coolify_install_url` are gone from every root; upgrading Coolify is the
owner's existing routine, not a step here.

**I-84. Logto is the owner's existing instance at `accounts.herakraft.co`;
no Logto container, and no `auth.repose.herakraft.co`.** (owner, 2026-09-20)
The owner already runs Logto with the GitHub connector configured; a second
one would be a second user database for the same people. `LOGTO_ISSUER`
(api) and `PUBLIC_LOGTO_ENDPOINT` (dashboard) and the CLI's default issuer
are `https://accounts.herakraft.co`; the issuer claim, JWKS and token
endpoints are under `/oidc` of that. What is still created there: the API
resource `https://api.repose.herakraft.co` and the `repose-cli` (Native,
device flow) and `repose-web` (SPA) applications, plus the M2M application
the api provisions users with. The `auth.` DNS record and the Logto step in
`docs/ops/coolify.md` are gone. *Rejected:* a Logto on the control VM
(DECISIONS R4-2's text; superseded here for the identity provider only,
Postgres, api and dashboard stay on the VM).

**I-85. The api verifies `iss` as `<LOGTO_ISSUER>/oidc`.** (conductor,
2026-09-20) `LOGTO_ISSUER` has always been the Logto *endpoint*: the api
fetched `<it>/oidc/jwks` and posted to `<it>/oidc/token`, and the CLI
discovers `<it>/oidc/.well-known/openid-configuration`. But the verifier
compared the token's `iss` with the bare endpoint, which only
`internal/fakes/logto` ever minted; real Logto (and `test/fake-logto`, the
dashboard's fake, which copies it) sets `iss = <endpoint>/oidc`. Every real
token would have been refused as "invalid issuer" at M2. The verifier now
expects `<endpoint>/oidc` and the api's fake mints that. The variable keeps
its name; renaming it would touch every env example and the docs of three
workstreams for no behaviour change.

**I-86. The owner's Coolify reaches the control VM over Tailscale; the
public-IP rule is the fallback.** (owner, 2026-09-20) The NSG rule for
`coolify_manager_cidrs` needs the owner's personal server to keep a fixed
public address, which it will not when that server moves, and the failure
mode is a silent hang in "Validate & configure". The owner's server and dev
box are already on a tailnet. cloud-init installs Tailscale on the control
VM (not joined: the auth key is a secret and `custom_data` is readable
through IMDS); the owner runs `tailscale up` once over SSH and adds the
server in Coolify by its tailnet address. Nothing about the owner's server
appears in any file, git-ignored or not, and port 22 on the public IP stays
operators-only. `coolify_manager_cidrs` remains for a Coolify that is not on
the tailnet. *Rejected:* a Tailscale auth key in cloud-init (secret in the
VM model); Tailscale SSH replacing sshd for Coolify (Coolify wants a plain
key it holds).

**I-87. Postgres and its backup are one Docker Compose resource from
`ops/coolify/postgres/docker-compose.yml`; api, api-grpc and web stay
separate Coolify Dockerfile applications with rolling deploys; the grpc api
issues its own server certificate; the Logto M2M application is
`repose-api`.** (owner, conductor, 2026-09-20) The owner wants the
deployment in files, not built by hand in Coolify's UI, and each thing
deployed on its own. A first draft put everything in one compose file; the
owner rejected it within the hour: a compose deploy recreates services
instead of rolling them, and one file means one deploy for all. So: the
database, which has no rolling deploy to lose, is a compose file in the
repository added as a Coolify Service (Postgres alone, the *service* named
`repose-postgres` and "Connect to predefined network" on: Coolify's parser
overwrites `container_name` with `<service>-<uuid>` and drops network
`aliases`, but Compose aliases every service by its name on each network
it joins, so the service name is the hostname that survives on the shared
`coolify` network where the applications already are); its backup is Coolify's
own scheduled dump to R2 on that service, which the owner already runs
elsewhere and which `repose-backup-check` verifies, rather than two
sidecar services the first draft wrote and the owner struck the same day
as code to maintain for a thing Coolify does; the three applications are Coolify "Dockerfile" builds
from the repository, each with an env file in `ops/coolify/` to paste,
reaching Postgres by that name with the password as a project shared
variable. Coolify's own health check cannot run in the distroless api
image (no curl or wget), so the image carries `HEALTHCHECK CMD api
-healthcheck`, a probe of its own listener, and Coolify's check is turned
off on `api` and `api-grpc` so the image's drives the rolling deploy.
Found on the way: `Validate()` refused the grpc app without certificate
files that nothing in the repository could issue (`repose-admin ca` signs
host and client certificates only), while `hostmgr.TLSConfig` already
issues a server certificate from the CA in Postgres when no file is given;
the requirement is now `GRPC_SERVER_NAMES`, and hosts verify that name
(`apiServerName`). The M2M application the api provisions users with is
`repose-api`. *Rejected:* one compose resource for everything (above);
Coolify's API driven by a script (unstable across 4.x, untestable from
here); pg_dump and rclone sidecars in the compose file (code to maintain
for what Coolify's backup does; the file is a Service rather than a git
application precisely so that Backups tab exists for it).

**I-88. The api reaches Postgres as `repose-postgres-<service uuid>`, the
container name, not the service name.** (conductor, 2026-09-20) I-87
assumed Compose's service-name alias would exist on the shared `coolify`
network once "Connect to predefined network" was on. It does not: Coolify's
generated compose lists only the resource's own network, and the shared
one is joined afterwards by `docker network connect`, which registers the
container name alone. Verified on the control VM (busybox on `coolify`:
service name NXDOMAIN, container name reachable on 5432); the first api
deploy failed on exactly this lookup. The env files carry the container
name, with the uuid pasted from the resource's Coolify URL. *Rejected:*
aliases in the compose file (stripped by the parser, and the shared network
is not in the file anyway); a `docker network connect --alias` by hand
(lost on the next redeploy); putting the applications on the resource's
network (Coolify applications have no such setting).

**I-89. The Postgres compose file joins the `coolify` network itself; the
api reaches it as `repose-postgres`. Supersedes I-88.** (owner, conductor,
2026-09-20) I-88 read the parser wrong: its "ignore aliases" is about
top-level network definitions, and serviceParser passes a service's own
`networks:` map through, only appending the per-resource network. With
`networks: { coolify: { aliases: [repose-postgres] } }` on the service and
`coolify` declared external, Docker registers the service name on the
shared network at `compose up`. Verified on the control VM with a
throwaway compose beside the live database: aliases on `coolify` were the
container name, `repose-postgres` and the explicit alias; a busybox
reached 5432 by name. The owner's preference, and the right one: the
hostname is a name chosen in a file, not a uuid copied out of a URL into
three env files. "Connect to predefined network" stays off for the
Service; its after-the-fact `docker network connect` is what registered
the container name alone. *Rejected:* the container-name host of I-88
(changes when the Service is recreated, and lives in every env file).

**I-90. The api bootstraps itself: it applies pending migrations at start
and generates the platform CA when none exists, both idempotent and
serialised across replicas on advisory locks.** (owner, conductor,
2026-09-20) 05 §5 had migrations as a Coolify pre-deploy command and
`docs/ops/coolify.md` had `repose-admin ca init` as a step after the first
deploy. Coolify's source (`ApplicationDeploymentJob::run_pre_deployment_command`,
4.3.23) runs that command with `docker exec` in a currently running
container of the app and skips it when there is none: on the first deploy
nothing ran, and the api exited on "relation secrets does not exist"; had it
survived, it would have exited on the missing CA, and an unhealthy
container is removed before anyone can exec into it. Both documents assumed
Coolify behaviour nobody had read. Now: `API_MIGRATE` (default `1`) makes
the api call `db.MigrateUp` at start, and a missing CA triggers `ca.Init`
under `LockCAInit`; a replica that loses the lock waits and loads what the
winner wrote. Writing the test for two replicas against an empty database
exposed two pre-existing races that the same fix closes: `MigrateUp` chose
the pending set before taking its lock (now re-checked under it, "already
applied" is a skip), and `EnsurePartitions` ran `create table if not
exists` outside any lock (now one transaction under `LockPartitions`).
`repose-admin db migrate` and `ca init` remain for operators; `ca init`
now refuses with a typed `ErrAlreadyInitialised`. The owner's condition
was idempotence; `internal/api/app/bootstrap_test.go` starts two replicas
at once and a third afterwards and asserts one schema and one CA.
*Rejected:* a Coolify one-off command (needs a running container); an init
container in a compose file (the api applications are single-container
Dockerfile apps by I-87).

**I-91. The api's Key Vault policy is Get, WrapKey and UnwrapKey.**
(conductor, 2026-09-20) The first CA init in production failed with 403
"does not have keys get permission": the api reads the key's current
version with GetKey before every wrap, and the policy, written as
"wrap/unwrap only, never get", withheld it. That rule was borrowed from
secrets, where Get returns the value; for a Key Vault key, Get returns the
public half and attributes, and the private key is non-exportable whatever
the permission, so Get costs nothing. Added rather than reworking the wrap
path to infer the version from WrapKey's response, which would leave the
rewrap job (which needs the current version without wrapping anything)
with the same need. The policy was also never applied: `api_identity_object_id`
was in `prod.local.tfvars` after the last apply. Applied 2026-09-20.

**I-92. Hosts dial the api at the control plane's VNet address and get
WireGuard from registration; the edge reaches `/internal` over a static
tunnel peer that `wgsync` keeps; the production edge's facts live in
`nix/edge/edge-01.nix`.** (m2 integration, 2026-09-20) The docs left the
host bootstrap circular: a host reaches the api "over WireGuard"
(`ops/coolify/README.md` puts gRPC on `10.255.255.1`), but its own WireGuard
key, address and the hub's peer arrive in `RegisterResponse`, and the edge
only admits a peer `wgsync` has read from `/internal/hosts`, which lists a
host after it registered. Settled as follows:

- *Hosts register and stream over the VNet.* `repose.host.apiAddr` is the
  control VM's private address on 8443 (`control_private_ip`, allocated
  statically as the subnet's first usable address so it can be written into
  a host's configuration), `apiServerName` is `api.repose.herakraft.co` and
  `apiCA` is the platform x509 host CA certificate (public, printed by
  `repose-admin ca show`). Azure's `AllowVnetInBound` admits it; the control
  NSG never opens 8443 on the public IP. Registration then returns the
  WireGuard material, `repose-host-net` brings `wg0` up, and the edge adds
  the peer within 30 s. WireGuard carries the gateway-to-guest path and
  observability, never the api. *Rejected:* the edge's key and endpoint in
  the host's Nix config so `wg0` is up first (the host's own key and address
  are assigned by `Register`, and the edge accepts no peer it has not been
  told about, so nothing could travel before registration either way); a
  bootstrap hop through the edge on the VNet (a second path for one RPC
  when the api is on the same VNet); the public name of DESIGN §7 (8443 is
  neither behind Coolify's proxy nor in the NSG). *Revisit when:* a host
  outside the VNet (Hetzner, R3-20) needs 8443 reachable from its address.
- *`GRPC_SERVER_NAMES` carries the addresses as IP SANs*
  (`api.repose.herakraft.co,10.255.255.1,10.200.3.4`; `pki.IssueServer`
  already made an IP an IP SAN), so the gateway, which dials the WireGuard
  address by IP, and hostd both verify the certificate the `api-grpc` app
  issues at start without a server-name override.
- *The edge's `API_URL` is `https://10.255.255.1:8444`* (option
  `repose.edge.controlWgAddress`), not the public name: Coolify's proxy
  terminates TLS for that and cannot present the gateway's client
  certificate (I-42). The control VM ⇄ edge tunnel has a static edge-side
  peer (`repose.edge.staticPeers`, declared to `networking.wireguard` and
  passed to `wgsync` as `WG_STATIC_PEERS`, which it never removes: without
  that the reconciler tore down the tunnel it reads `/internal/hosts`
  through, every 30 s). The VM side is written by hand on a live VM
  because its `custom_data` is in `ignore_changes`, so
  `edge_wireguard_public_key` only reaches a VM created after it is set;
  `infra/README.md` "Wiring the control plane to the edge" has the file.
- *`nix/edge/edge-01.nix`* holds what makes the production edge differ from
  the module (operator source addresses, the control plane's public key)
  and is imported by `nixosConfigurations.edge`: the attribute keeps its
  name because `infra/azure/modules/edge` re-runs nixos-anywhere when
  `flake_attr` changes, and a live edge must never be reinstalled by a
  rename. The operator `/32` is the first committed copy of a value
  `prod.local.tfvars` keeps local (`operator_cidrs`); the NSG stays the
  outer gate with the same list. *Rejected:* a runtime file under
  `/var/lib/repose/edge` read into an nftables set by a unit (machinery
  for one address, and one more file a reinstall would lose).
- *`repose-admin ca show` and `ca sign-server`* exist because the edge needs
  the CA certificate (`api-ca.pem`, and every host's `apiCA`) and a server
  certificate for the hook-ingest listener, and nothing printed either.
- *`bootstrap.enable` stays on for host-01*: the token delivery and the
  post-install checks reach a host on the VNet through the edge, and a
  reinstalled or re-tokened host needs that path again; sshd binds the
  WireGuard address alone once `host.json` carries it (`network.nix`),
  so the provider-NIC rule admits nothing after registration.

**I-93. hostd hands the base checkout to the build user.** (m2
integration, 2026-09-20) The first `Build` on host-01 (a one-line
home-manager fragment against main, through `hostdev build`) failed before
evaluation: hostd clones the base as root, the evaluation runs as
`nixbuild` (I-45), and Nix's libgit2 refuses a `git+file://` repository
owned by another user (`repository path ... is not owned by current user
(libgit2 error code = 7)`). No unit test could see it: the fake runner's
clone has no owner. `ensureBase` now runs `chown -R <user>: <checkout>`
after a clone and on an existing checkout too, so a checkout an operator
placed by hand (`ops/RUNBOOK.md` "Build: base unavailable") is handed over
the same way. With that, the build ran through: eval plus build of the
6.0 GB guest closure, on a store that already held the M1 base, took 35 s (eval 13.0 s, build 21.9 s), recorded in
`docs/RESEARCH.md` §11. *Rejected:* `safe.directory = *` in a git config
for the build user (libgit2 honours it, but a directive that disables the
check everywhere for a user that evaluates tenant input is the wrong
direction); cloning as the build user (hostd would need the deploy key
readable by that user, which is the key a tenant's evaluation runs next
to).

**I-97. `/run/repose` is 0755; the join-token delivery no longer makes it
0700.** (m2 integration, 2026-09-20) The first `repose-admin hosts smoke`
against host-01 on the real api built its guest in 19 s and then failed
`CreateGuest` at step 8: virtiofsd logged `/run/repose/store-export does
not exist`. The export was there; `/run/repose` had just been recreated
`0700 root` by infra's token-delivery provisioner (`install -d -m 0700`),
so the unprivileged `virtiofsd` user (I-48) could not traverse to it, and
virtiofsd reports a failed `stat` as "does not exist". It never showed on
M1 because that host's `/run/repose` had been made by `repose-host-net`
(0755) before the token arrived, and the M1 guests were created hours
later; on M2 the re-tokening came after the reboot-free switch and the
first create followed within a minute. The directory holds the export, the
rendered network files and the control socket, none of them secret (the
token file inside stays 0600); the provisioner, the runbook's by-hand line
and `repose-host-net` (which now `chmod 0755`s the directory whatever made
it) agree on 0755. *Rejected:* moving the token to its own 0700 directory
(a second path in the runbook, the host module and hostd for one file's
mode, which the file already carries).
**I-94. A scrape is a forwarded packet, so the edge needs a forward rule;
the control plane is `10.255.255.1` on the hub, and its two applications
are two scrape targets.** (m3-web, 2026-09-20) `ops/prometheus/
wireguard-peer.conf` said in as many words that "the edge's firewall needs
nothing new" for the monitoring peer. Read against the edge as built, and
against the live edge's ruleset, that is wrong three times over, and each
of the three would have presented as the same symptom: a WireGuard
handshake that looks perfect and a Prometheus with every target down.

- *The forward chain.* `nix/edge/default.nix` gives `forward` a policy of
  drop with `ct state established,related accept` and nothing else, and
  its comment says why: "hosts never route through the edge to one
  another". But Prometheus is not in Azure and hosts have no inbound, so
  every scrape of a host, and of the control plane, is a packet the edge
  forwards from one peer to another, and so is every Fluent Bit push to
  Loki. Both were dropped. `repose.edge.monitoring.{peerCIDRs,
  scrapePorts, logPorts}` adds exactly two rules when a monitoring peer is
  declared and none when it is not: that peer may reach 9100, 9101, 2021,
  9103 and 9104 on another peer, and another peer may reach 3100 on it.
  *Rejected:* `iifname "wg0" oifname "wg0" accept` (it would also let one
  tenant's host reach another's, and the control plane's gRPC listener,
  which is the thing per-host AllowedIPs and this policy exist to
  prevent); a route on the monitoring server straight to each host (hosts
  have no address anyone outside the mesh can route to, which is the
  point).
- *The control plane's address.* The same file, and the `api` job of
  `ops/prometheus/prometheus.yml`, put the control VM at `10.255.0.2`.
  DECISIONS I-92 put it at `10.255.255.1` and that is what `wg show` on
  both machines says today. The file with the wrong address was the one an
  operator would have followed.
- *One job, two targets.* `api` and `api-grpc` are separate Coolify
  applications from the same image (I-2), so they are two processes with
  two registries, and the families split between them: the HTTP families
  come from `api`, the stream, ops and outbox families from `api-grpc`.
  The job now has both with an `app` label. Verified on the control VM:
  `api` serves 57 `repose_*` series on its container address and
  `api-grpc` 62 on the host's 9104.

Also found and *not* fixed here, because it is a Coolify field and this
session does not touch the UI: `ops/coolify/README.md` prescribes a
`9103:9103` port mapping on the `api` application and the live application
has no port mappings at all, so its metrics are reachable only from inside
the container network. `api-grpc`'s `9104:9103` is in place. One field,
for the conductor.

Interfaces: none. `ops/prometheus/prometheus.yml`,
`ops/prometheus/wireguard-peer.conf` and `nix/edge/default.nix` change
together because they are three halves of one path.

**I-95. `RegisterResponse` carries `loki_url`, from a setting an operator
records with `repose-admin edge loki`; Fluent Bit refuses to start without
one.** (m3-web, 10 and 05, 2026-09-20) `docs/interfaces/host-conventions.md`
has documented a `loki_url` field of `host.json` since workstream 01,
`nix/hosts/network.nix` renders `LOKI_HOST` and `LOKI_PORT` from it,
`nix/hosts/fluent-bit.nix` uses them as its Loki output's address, and
`ops/RUNBOOK.md`'s FluentBitStuck entry says in as many words that the value
"comes from `loki_url` in `host.json`, which the api sends at registration".
Nothing sent it: `RegisterResponse` had six fields and none of them was
this one, and `internal/hostd/register` declared the struct field and never
assigned it. Every host would have rendered `LOKI_HOST=` and shipped
nothing, and the symptom — a Fluent Bit retrying a connection to an empty
host name for ever — is indistinguishable in the journal from a Loki that
is down. So M3's "Fluent Bit on host-01 ships journald and guest console
logs" could not have been closed by configuration alone.

- *A setting, not an environment variable.* The Loki names a machine
  outside this deployment, it changes without the api changing, and
  moving a log sink should not need a redeploy of the api — the same
  three reasons the edge's WireGuard endpoint and public key are already
  settings written by `repose-admin edge init`. `repose-admin edge loki
  [URL]` prints, records, or (with an empty string) clears it, refuses a
  URL with no scheme because that is the mistake that produces a fleet
  shipping nowhere, and writes an `audit_log` row like every other admin
  action. *Rejected:* a `LOKI_URL` variable in `ops/coolify/api.env`
  (a redeploy of the api to change where hosts send logs, and the api
  redeploy is the one this milestone coordinates most carefully); a
  per-host column (there is one Loki, and a per-host value is a per-host
  mistake).
- *`Rotate` carries it too.* A host registers once, so a Loki recorded
  after the fleet exists would never reach it. `Rotate` runs every 30
  days and already returns a `RegisterResponse`; it now carries the
  current value, which bounds "an operator recorded a Loki" to at most a
  month, and the runbook's edit-and-restart is the immediate path.
- *Empty is still the old behaviour, both ways.* An api that predates the
  field sends nothing, and hostd then keeps whatever `host.json` already
  had rather than clearing a working host's sink at its next rotation; a
  host that has never been told renders an empty `LOKI_HOST`, and
  `fluent-bit.service` now refuses to start with that reason in the
  journal instead of retrying nothing for ever. *Rejected:* defaulting to
  the edge's address (the edge is not a log store, and guessing an
  address is how a fleet ships to a machine nobody is reading).

Interfaces: `grpc-hostd.md` (`RegisterResponse.loki_url`),
`host-conventions.md` (where the field comes from and what an empty one
means), `proto/repose/hostd/v1/hostd.proto`. The old shape stays accepted:
field 7 is additive and an absent value means what it meant before.

**I-96. Where `features/` promised a dashboard that was never specified,
the feature doc is corrected, not the dashboard.** (m3-web, 08, 2026-09-20)
The M3 web session's last item is "`docs/features/*` match what is live".
Two of them did not, and in both cases the feature doc was written before
`workstreams/08-dashboard.md` §5.2 fixed the page list, and promised more
than §5.2 ever asked for:

- `features/status-and-logs.md` "Dashboard" promised a sortable project
  list with a cost sparkline, and a project page with a state timeline,
  a ports card and a cost breakdown by meter, and an account page
  carrying limits. §5.2 specifies none of those: the list is a plain
  table, the project page is seven cards, secrets and config are their
  own pages, and billing, settings and account are three pages. The
  section now describes the built pages route by route, and each promise
  it dropped is named in "Deferred" rather than deleted, so the next
  person to want a state timeline finds that it was considered.
- `features/notifications.md` promised the project page would show
  "delivery status per channel, so a user who got nothing can see ...
  whether the delivery failed". `GET /projects/:id/events` returns
  `{id, ts, kind, agent, summary}` and has no per-channel outcome in it;
  the outbox's state is in `events_outbox`, which no user route exposes.
  The doc now says what the Events card does answer (did the event
  happen), points the other half at `ops/RUNBOOK.md` "No notifications
  arriving", and defers the feature with the route change it needs.

*Why the doc and not the code:* AGENTS.md's rule is that the code follows
the docs, and the tie-break when two docs disagree is the one that owns
the thing. `workstreams/08-dashboard.md` owns the dashboard, its §9
checklist is what 08 was built and tested against, and `features/` is
meant to describe user-visible behaviour rather than to widen scope by
prose. Building four features in an integration session to make a sketch
true is the wrong direction, and shipping a doc that describes a product
nobody has is worse than shipping a shorter doc. *Rejected:* building
them (scope, in a session whose job is to close what exists); deleting
the promises without trace (the reasoning behind per-channel delivery
status — a support call the platform cannot otherwise answer — is worth
keeping).

**I-100. A first sign-in without a GitHub identity gets a `user-<sub>` handle;
`repose-admin users rename` and `projects destroy` exist for the operator
to put that right.** (m2 integration, 2026-09-20) Before the gate, the
production database held one user, `user-c7fh26yzrl93`, from the owner's
afternoon dashboard sign-in. Logto's Management API for that subject shows
`identities: []` and `username: null`: the account was created with email,
not through the GitHub connector, so `auth.Provisioner`'s fallback did what
it says, and the handle, which is the SSH login suffix and the certificate
`key_id`, is fixed at first sign-in and never rederived. A user row cannot
be deleted (the trial credit's ledger row references it and the ledger is
append-only, I-78), so the repair is `repose-admin users rename OLD NEW
[--github-login L]`, refused once the user has projects. The same session
found no admin way to remove a project whose create failed (`projects` had
start/stop/restart/snapshot/resize/move/restore/exec); `projects destroy
ID` enqueues the op `DELETE /projects/:id` would. Both are listed in
`repose-admin`'s usage. What the owner does in Logto: sign in with GitHub
(Console → Sign-in experience → Sign-up and sign-in: GitHub under social
sign-in, and the GitHub connector enabled), or link GitHub on the existing
account; either way the api's row keeps its handle until renamed.
*Rejected:* rederiving the handle at every sign-in (a login name that
changes under a user's SSH config); deleting the user (the ledger);
provisioning only when a GitHub identity is present (an email sign-in to
the dashboard must still work, and the handle fallback is the documented
shape for it).
**I-98. CLI releases are GitHub releases of the `Heracraft/factory`
repository, cut from `v*` tags; the dashboard serves `install.sh`.**
(conductor, 2026-09-20) The M2 gate's first step was to install the CLI,
and `install.sh` pointed at `heracraft/repose`, a repository that does not
exist, with no release to point at and no route serving the script at the
URL the landing page prints. Three fixes: `install.sh` names the repository
as it is (the rename to `repose` stays the owner's step; GitHub redirects
the old name afterwards, so the script keeps working through it);
`.github/workflows/release.yml` runs GoReleaser on a tag push and publishes
the four archives plus `checksums.txt`; the dashboard's build copies
`install.sh` into its static assets, so `curl -fsSL
https://repose.herakraft.co/install.sh | sh` is what the page says it is.
`v0.1.0` is the first tag. *Rejected:* waiting for the rename (the gate is
today); serving the script from the api (the page prints the dashboard's
host, and a static file needs no code).

**I-99. The CLI's OAuth client id is Logto's App ID for `repose-cli`, a
config value with that default, recorded in the credentials file.**
(conductor, owner, 2026-09-20) The first `repose login` of the M2 gate
answered `oidc.invalid_client: invalid client repose-cli`: the CLI sent the
application's *name* as `client_id`, and Logto identifies applications by
an opaque App ID it assigns (`jccig5bb3i4d78bq4farv` for `repose-cli`, the
way the dashboard bakes in `PUBLIC_LOGTO_APP_ID`). The id is public, so it
is the built-in default; `logto_client_id` in `config.toml` overrides it
for another Logto; and `credentials.json` records the id its refresh token
was issued to, so a refresh needs no config and an old file (no field)
falls back to the default. Interface: `docs/interfaces/cli-config.md`.
Shipped as v0.1.1. *Rejected:* a Logto application whose id equals its
name (Logto does not offer that); reading the id from the api at login (a
second round trip before the first, for a value that never changes).

**I-101. `repose login` uses the device-code flow by default; the loopback
PKCE flow is `--browser`.** (conductor, owner, 2026-09-20) The second
attempt at the M2 gate answered `oidc.invalid_redirect_uri`: the
`repose-cli` application in the owner's Logto is a Native app with device
flow enabled and, as that app type's settings page shows, no Redirect URIs
field, so the loopback redirect the PKCE flow registers on the fly can never
match. Logto also matches redirect URIs exactly, so the doc's
`http://127.0.0.1:*/callback` was never registrable. Device code needs
nothing registered, works in every terminal (including over SSH) and is what
`gh auth login` does; it is now the default, and `--browser` remains for a
Logto application that does register loopback URIs (07 §5.2 step 2 is
demoted to that case). *Rejected:* a fixed loopback port registered in Logto
(collides with anything else on the laptop, and a second Logto still needs
the entry); detecting the failure and falling back (Logto renders the error
in the browser and never redirects, so the CLI would wait on a callback that
never comes).

**I-102. Every Logto token request from the CLI carries
`resource=https://api.repose.herakraft.co`.** (conductor, owner, 2026-09-20)
The third attempt at the M2 gate logged in through the device flow and then
failed with `unauthenticated: invalid token` from the api: the CLI sent the
`resource` parameter only on the browser flow's authorization request, so
the device-code request, its token poll and the refresh grant got an opaque
token for Logto's userinfo endpoint, not a JWT with the api as audience,
and the api's verifier (I-85) refused it. `resource` now goes on the device
authorization request, the device-code and authorization-code token
requests and the refresh grant; `internal/fakes/logto` and
`test/fake-logto` both key the audience on it, so the CLI's tests assert the
shape. Shipped as v0.1.3. *Rejected:* accepting opaque tokens at the api by
calling Logto's userinfo (a round trip per request and a token that any
Logto application could mint).
**I-103. Coolify owns the backups, the destination is the owner's own S3
storage, and no credential for it comes through this repository or an
agent session.** (owner, 2026-09-20) `infra/r2` created a bucket, and
`AZURE-SETUP.md` step 10, `ops/coolify.md`, `ops/coolify/README.md`, the
control VM's `repose-backup-check` and the m3-web brief all assumed an R2
API token would arrive and be used here. The owner's decision is that it
will not: the backup destination is an S3 storage configured in their own
Coolify — quite possibly one that already exists for their other
databases — and nothing on the repose side ever holds a credential for
it. This is the same rule as I-21 and the "secrets have three homes"
rule, applied to a credential that had been treated as merely
inconvenient rather than as out of scope.

What changes:

- *`repose-backup-check` checks the near end, with no credential.* It
  used to list an R2 bucket through an rclone remote an operator had to
  create from the token. It now reports the age of the newest dump
  Coolify has written under `/data/coolify/backups` on the control VM,
  and says in its own output that this proves the dump was **taken**,
  not that it was **uploaded**. That is worth keeping rather than
  deleting: a dump that was never taken is the failure that silently
  leaves no restore point at all, and it is invisible in Coolify's UI
  until someone looks. The upload is the Backups tab, which is also
  where Coolify reports its own failures, and `RUNBOOK.md`
  "PostgresBackupStale" now walks both halves and says which tool
  answers which. *Rejected:* deleting the check (it would have left the
  near-end failure unwatched); having it call Coolify's API (unstable
  across 4.x, and it would need a Coolify token on the VM, which is the
  same problem one layer along).
- *`ops/restore-rehearsal.sh` takes a file.* The dump comes from the
  database's Backups tab, which is where a human already is when they
  need a restore. `--from-bucket` stays for whoever has a remote of
  their own, because it is three lines and removing it would not make
  anything safer.
- *`infra/r2` stays, marked optional and unused by production*, rather
  than being deleted: a second environment or a different owner may want
  a bucket, the module is written and validated, and deleting a working
  module to express a policy is how the policy gets re-litigated by
  someone who needs the module. Its header says plainly that production
  does not use it. `backup_bucket` is gone from
  `infra/azure/modules/{environment,coolify}`, since nothing on the VM
  reads a bucket name any more; `backup_max_age_hours` stays.

*Why this is better than the arrangement it replaces:* the credential
that would have been pasted into an agent's tfvars, an operator's shell
history and an rclone config on a VM now exists in exactly one place the
owner already manages. The cost is that this side cannot prove the
upload happened — which is honest, because it never really could: an
rclone listing proves an object exists, not that it is last night's
database.

**I-106. `repose run` waits on the op a create leaves in flight instead of
starting the project.** (conductor, 2026-09-20) The first real `repose run`
answered `conflict: recruiting is already starting`: the api's create
returns `state: creating` with the create op's `op_id`, and the CLI's
`ensureRunning` saw "not running" and issued a start, which the engine
refuses while the create op runs. The fakes never showed it because their
create completed instantly. The CLI's `Project` now carries `op_id`;
`ensureRunning` waits on that op (streaming its build log) or, without an
op id, on the state leaving `creating`/`starting`, then re-reads before
deciding to start. `internal/fakes/api` gains `Options.CreateDelay`, which
answers a create the way the engine does and refuses a start meanwhile; the
regression test runs the flow against it. *Rejected:* retrying the start
until it is accepted (the op-conflict retry already exists and would have
raced the create's own start for the whole build).

**I-107. guestd's `SetupProject` points `origin` at the project's remote.**
(conductor, 2026-09-20) The first real sync failed in the guest with
`'origin' does not appear to be a git repository`: guestd ran `git init` in
the project directory and never added a remote, while the CLI's sync (and
`docs/features/sync-at-launch.md`) expect "a clone with an origin remote".
`Setup` now adds `origin` as the SSH form of the api's normalised remote
(`github.com/owner/repo` becomes `git@github.com:owner/repo.git`, fetched
through the agent the CLI forwards), or `set-url`s an existing one.
Guests built before this carry the old guestd until the next base version;
the M2 test guest had its remote added by hand. *Rejected:* cloning at
setup (the CLI's first sync fetches exactly what it needs and the agent is
only present during a run); HTTPS URLs (private repositories need the
user's credentials, which only the forwarded agent carries).

**I-108. The generated `~/.ssh/repose/config` sets `IdentitiesOnly yes`.**
(conductor, 2026-09-20) `ssh <slug>.repose` from a plain terminal answered
`permission denied (certificate required)` while the CLI's own connection
worked: with an ssh-agent loaded, ssh offered the agent's plain key before
the configured certificate identity, and the gateway refuses plain keys by
design. The generated host block now restricts ssh to the certificate
identity it names; `ForwardAgent yes` stays, since the agent is still
forwarded for the guest's own git.
**I-104. The CLI sends an IANA zone name or no `tz` at all.** (conductor,
owner, 2026-09-20) The owner's first `repose run` failed at create with `tz
is not an IANA zone name`: Go names `time.Local` "Local" unless `TZ` is
set, and the CLI's fallback sent the zone abbreviation ("EAT"), which the
api rightly refuses. `localTZ` now resolves `TZ`, the Local name, the
`/etc/localtime` symlink or `/etc/timezone`, validates each with
`time.LoadLocation`, and omits `tz` when none is known so the api applies
its default. *Rejected:* accepting abbreviations at the api (ambiguous:
"CST" is three zones).

**I-105. The provisioner reads the GitHub login from Logto's
`rawData.userInfo.login`; the fake Logto emits that shape.** (m2 gate,
2026-09-20) The conductor's device-code login, approved by the owner
through "Continue with GitHub", provisioned `user-yv7ryeiczbkz` although
Logto's Management API shows a `github` identity for that subject. The
connector stores `details = {id, name, avatar, email, rawData}` with
`rawData = {userInfo, userEmails}`, and GitHub's `login` sits inside
`userInfo`; `auth.Lookup` tried `details.login` and `rawData.login`, both
absent, and fell through to the `user-<sub>` fallback of I-100. The unit
test passed because `internal/fakes/logto` rendered the identity as the
flat `details.login` no Logto version sends. `Lookup` now tries the three
shapes in order and the fake renders the real one (a `LegacyShape` flag
keeps the flat form for the fallback's own test; a `User` without a
GitHub login renders no identity, the email sign-in case). Existing rows
are not rederived: `user-c7fh26yzrl93` (an email account, no identity to
derive from) and `user-yv7ryeiczbkz` (renamed once its test project is
gone; a handle with projects is never renamed because the guest host
certificate carries `<slug>.<handle>`, I-42). *Rejected:* fetching GitHub's
profile from the api with the connector's token (the Management API
already returns it); a rename that re-signs host certificates (an
operator path for a one-time repair).

**I-110. The relay half-closes towards the client when the guest's side of
a channel ends; the fake guest reads its agent channel with one reader.**
(m2 gate, 2026-09-20) The conductor's end-to-end run found that an exec
through the gateway with agent forwarding (`ssh -A … 'ssh-add -l'`) printed
its output and never returned, which stalled the CLI's sync (`git fetch`
over the forwarded agent) for 13 minutes. `pipe` forwarded the client's
EOF to the guest (`CloseWrite` on the guest channel once the client's
stdin ended) but never the reverse: when the guest's side of a channel
ended it waited for the guest's full close and only then closed the
client's channel. For a session that works, because the program's exit
closes both directions at once. For the agent channel it deadlocks:
sshd sends `CHANNEL_EOF` when the program's agent socket closes and sends
`CHANNEL_CLOSE` only after it has received the peer's EOF; OpenSSH's client
closes its agent socket, and so sends its own EOF, only when it sees EOF
from the channel; the gateway sat between them forwarding neither. The
relay now issues `CloseWrite` towards the client as soon as the guest's
inbound data (data and extended data) has ended, symmetric with the other
direction; the full close still follows the rules of I-82. Two things about
the test: `TestRelayAgentForwarding` passed on the old relay because the
fake guest closed its agent channel outright, so the fake now ends it the
way sshd does (EOF, wait for the peer's EOF, then close), and the exec is
bounded so a regression fails in 15 s rather than hanging CI; and the fake
hands `agent.NewClient` a plain `io.ReadWriter`, because given a Closer
x/crypto v0.55 starts a pipelined reader that keeps reading the channel
after `List` returns, and x/crypto's channel EOF wakes only one of two
readers, which left the fake's own read asleep and looked, for an hour,
like the gateway bug it was masking. *Rejected:* closing the client
channel on the guest's EOF (loses the exit status ordering I-82 fixed);
a timeout on idle channels (a race is not a timing constant).
**I-109. The guest base disables NixOS's systemd ssh proxy include.**
(conductor, 2026-09-20) The first `git fetch origin` inside a real guest
failed with `Bad owner or permissions on
/nix/store/...-systemd/lib/systemd/ssh_config.d/20-systemd-ssh-proxy.conf`:
NixOS's `programs.ssh` includes systemd's drop-in from the store in every
client invocation, and over the shared virtio-fs store that file is owned
by `nobody:nogroup` as far as the guest can tell, which ssh refuses for
any config it reads. The proxy exists for reaching VMs over AF_VSOCK from
a host, which a guest never does; `programs.ssh.systemd-ssh-proxy.enable =
false` removes the include. Verified by the guest-base VM test and the
live guest on base 2026.09.20.2. *Rejected:* mapping store ownership to
root in the guest (virtio-fs's uid squashing is what keeps the shared
store read-only and tenant-safe, DESIGN §6).

**I-111. The guest pins GitHub's SSH host key and trusts other forges on
first use.** (conductor, 2026-09-20) With `origin` set by guestd (I-107) the
first real sync on the new base failed with `Host key verification failed`:
a fresh guest has no known_hosts, and git's ssh refuses an unknown host
rather than prompting inside a non-interactive fetch. The guest base pins
`github.com`'s published ed25519 key through `programs.ssh.knownHosts` and
sets `StrictHostKeyChecking accept-new` for everything else, which is the
policy a laptop's first clone applies and never overrides a pinned key.
*Rejected:* `StrictHostKeyChecking no` (accepts a changed key too);
seeding known_hosts from the laptop at sync (one more file the CLI copies,
and the laptop may never have connected either).
**I-112. Backups are entirely Coolify's, and Coolify redeploys on every
push to `main`.** (owner, 2026-09-20) Supersedes what is left of I-103,
which had kept a foot in the door: an on-VM check, a restore rehearsal
script, a runbook alert entry and `infra/r2` as an "optional" module. The
owner's position is simpler and better: the Postgres backup and its
restore are configured on the Postgres service's Backups tab in their own
Coolify, and this repository has no part in them at all.

Removed rather than reworded, because a half-owned responsibility is the
one nobody holds: `repose-backup-check` and its `backup_max_age_hours`
variable are out of the control VM's cloud-init and out of both infra
modules; the readiness provisioner no longer asserts `rclone` and
`pg_restore`, and cloud-init no longer installs them, since the restore
procedure that justified them is gone; `ops/restore-rehearsal.sh` is
deleted; `infra/r2` is deleted along with the Makefile's `ENV=r2` root and
its Cloudflare token guard (a token is still needed for `manage_dns`, a
different scope); the backup and restore sections are out of
`ops/coolify.md`, `ops/coolify/README.md`, `AZURE-SETUP.md` step 10 and
`RUNBOOK.md`, and the release checklist's item is now "configured in the
owner's Coolify; nothing here".

*What this costs, recorded so nobody rediscovers it as a surprise:* the
near-end failure — a dump that was never taken — is no longer watched
from this side, and it is invisible until someone opens the Backups tab.
I argued for keeping the check for exactly that reason and the owner
overruled it, correctly: a check that proves a dump exists but not that
it is last night's database, on a machine whose backups somebody else
owns, is a second place to look that can disagree with the first. One
owner, one place. *Also kept deliberately:* `ops/coolify.md`'s note that
Coolify encrypts its stored credentials with `APP_KEY`, so a dump
restored without it is ciphertext. That is not backup machinery, it is a
fact about the owner's own restore, and it is worth more to them now that
the whole procedure is theirs.

*And the second half.* Coolify watches the repository, so `api`,
`api-grpc` and `web` rebuild and roll on every push to `main`: a deploy
is a consequence of merging, not a step. Every "then redeploy X"
instruction is therefore wrong and is gone from `coolify.md`, the ops
README, the runbook and the launch prompts. Two survive as what they
are: a rollback (Coolify's deployment history — the one deploy nobody
gets automatically) and a re-paste of the Postgres Service, which is a
pasted resource rather than a watched one. It also means fact 13's
dropped request happens on **every merge**, not only on deliberate
deploys, which is what turns it from a curiosity into the owner's
decision about the proxy's drain. Recorded as `coolify.md` facts 15
and 16.

**I-117. Build-log flushes are serialised and `Read` always flushes first,
so a reader never misses the batch a flush is inserting.** (conductor,
2026-09-20) CI failed `TestSSELiveStreamAndConcurrentLoad` with "stream 0
saw 1 lines" of 3: `Flush` took the pending batch out under the mutex,
released it, then inserted the rows in a transaction; a `Read` in that
window saw nothing pending, skipped its own flush and queried the table
before the insert committed. With the op already finished at connect time
the SSE handler does exactly one catch-up read, so the stream ended short.
`Flush` now holds a flush mutex for its whole run and `Read` calls `Flush`
unconditionally, which makes it wait for one in flight; a test races
appends and flushes against a reader. *Rejected:* publishing to subscribers
before the insert (a subscriber would then see lines the table does not
yet have, and a `since` replay could skip them).
**I-113. `repose-admin projects create` makes a project for a synthetic,
billing-exempt user; `hosts smoke` uses the same path.** (m3 integration,
conductor, 2026-09-20) The M3 checks that must not run as the owner (two
tenants for the isolation rows, throwaway guests for notifications and
audit rows) need a guest under a user that cannot sign in, the way `hosts
smoke` runs under `repose-smoke`. `projects create --user HANDLE --name N
[--class C] [--host N] [--create-user] [--wait]` is smoke's create step
factored out: with `--create-user` a missing handle becomes an exempt
account with limits 100/100 and no Logto identity (I-16), the project and
its empty revision are inserted the way `POST /projects` does and the
create op is enqueued for api-grpc to drive, pinned to a host when asked.
Audited as `project_create`. *Rejected:* inserting rows by hand with
`psql` (the op has to come from the engine); a Logto identity for
synthetic users (a second identity path in the api for a test).

**I-114. The CLI waits through `building`, and reads an op's error as
`{code, message}`.** (m3 integration, 2026-09-20) Two things the first
`repose run` and `repose config apply` on host-01 under a real build
showed: I-106's wait covered `creating` and `starting`, but the create
op's first phase puts the project in `building` a moment after `POST
/projects` answers, so the CLI issued a start and got the api's
`conflict: m3-check is building` (the fake completed its create before
any state could be read; it now reports `building` while its create
delay runs, which reproduces the race). And `Op.Error` was a string while
the api stores and returns the hostd result's `{code, message}` (I-42),
so the secret-in-fragment refusal rendered as `error: ` with nothing
after it. `OpError` decodes both shapes, and `RenderBuildError` takes the
code and prints the prefix `nix-build-contract.md` "What the user reads"
assigns it (`config error: ` for `eval_failed` and the api's `invalid`,
`config too large: ` for `closure_too_large`, none for `build_failed` and
`build_timeout`, `error: ` otherwise). *Rejected:* changing the api to
return a string (the code is what the CLI switches on, and the dashboard
reads the same shape).

**I-115. A destroyed project's `rev-*` GC roots go with its guest, and a
restore of a destroyed project rebuilds its closure.** (m3 integration,
2026-09-20) `host-conventions.md` says the guest's root is removed on
destroy and the `rev-<project>-<revision>` roots keep the last three
built revisions; hostd removed the first and never the second, so every
destroyed project on host-01 left its revision roots (seven of them
after one evening) and 6 GB of store each, for ever. `DestroyGuest` now
prunes every `rev-<project>-*` root of that project. Because a restore
of a destroyed project (`POST .../snapshots/:sid/restore` with
`as_new_project`, within the 30-day retention) would otherwise hand
hostd a closure the next `nix-collect-garbage` may have removed, the
api copies the revision without its closure when the source is
destroyed, so the restore plan is `build, restore, start_guest`.
*Rejected:* keeping the roots for 30 days to match snapshot retention
(the store is the host's scarce resource and the closure is a
deterministic build of a fragment the database still holds).

**I-116. `repose_host_guests` publishes every state and class at zero.**
(m3 integration, m3-web, 2026-09-20) m3-web saw no `repose_host_guests`
series on host-01 at all. The wiring was fine (the gauge appeared as soon
as a guest existed: `repose_host_guests{class="small",state="running"} 1`
at 23:45Z); a Prometheus `GaugeVec` with no children exposes nothing,
not even HELP, so a host with no guests read as "no data" on the
capacity panel rather than 0. `refreshGuestGauge` now sets every
`state × class` combination to 0 before counting, so the family has
thirty series from the first refresh. *Rejected:* a separate
`repose_host_guests_total` gauge (a second name for the same count).

**I-118. `Build` carries `base_version`, hostd writes it beside the
fragment, and the flake stamps the guest with it.** (m3 integration,
2026-09-20) Every guest built by hostd on host-01 had
`/etc/repose/base-version` and its NixOS label reading `dirty`
(`repose-guest-profile` prints it, `nixos-version` shows it, the build
log names `nixos-system-repose-guest-dirty`). `nix/flake.nix` derives the
stamp from `self.shortRev`, which is present for the `git+file://`
checkout (`nix flake metadata` on host-01 as `nixbuild` shows the
revision) and absent the moment the evaluation overrides the `fragment`
input, which every hostd `Build` does (I-28); reproduced on host-01 with
hostd's exact command. The flake's comment already wanted the api's
`base_versions` label and the guest file to carry the same string, and
only the api knows that label. `Build` gains `base_version` (optional;
the old shape is accepted and keeps today's stamp), hostd writes it as
`base-version` next to `fragment.nix` after validating it as a label,
and `guestSystem` reads it into `repose.baseVersion` when present. The
api sends the revision's base version. *Rejected:* fetching the base with
`?rev=` and hoping `self.rev` survives the override (it is the override,
not the ref, that drops it); stamping the git revision from hostd (the
label users see is the version, `2026.09.20.3`, not a sha). Interfaces:
`grpc-hostd.md`, `nix-build-contract.md`, `hostd.proto`.

**I-119. The api renders the menu with `internal/menu`; `internal/nixmenu`
is gone.** (m3 integration, 2026-09-20) I-42 recorded that the api carried
its own package-only stand-in for the catalog "until 12 lands", and I-44
that "the api calls it" once it had; the swap never happened, and the
first `GET /catalog` on the real api returned the stand-in's 23 package
entries (no services, no `kind: service`, no options beyond nodejs's
version) with a generated fragment whose second line was not the
selection the feature doc and I-44 describe. `PUT /config {menu}` and
`GET /catalog` now go through `menu.Load()` (validated once per process,
`sync.OnceValues`), `Catalog.Render` and `Catalog.Public`; a `*menu.Error`
is the api's `invalid` with its message; `menu.IsGenerated` is the
menu-versus-fragment switch. A project generated by the stand-in (header
only, no selection line) still counts as menu-managed through its
`config_revisions.menu` column. The stand-in package and its tests are
deleted. Found by `ops/checks/menu.sh` on host-01, whose bun menu apply
worked either way (built in 5 s, on PATH without a reboot).

**I-120. The tap name and MAC come from a hash of the guest id, not its
first eight hex; a create refuses a tap or MAC another guest holds.** (m3
integration, 2026-09-21) Two synthetic tenants created on host-01 within
the same minute (`repose-admin projects create`, 00:00:33Z) got guest ids
`01a0c143-f202-…` and `01a0c143-f36d-…`: a UUIDv7 starts with its
millisecond timestamp, so the first eight hex, which `host-conventions.md`
made the tap name and the first four bytes the MAC, are the same for every
guest created within about 65 seconds. The second create failed at step 6
on the first guest's htb root (`tc: Change operation not supported by
specified qdisc`), and its retry attached a second Cloud Hypervisor to
`tap-01a0c143` with MAC `52:54:01:a0:c1:43`: two tenants, one tap, one MAC,
both admitted by the bridge set. A guest is a per-project microVM behind
a per-guest tap (DESIGN §7, SECURITY boundary 2); the name was the hole.
Now `tap-<first 8 hex of sha256(guest id)>` and `52:54:<first 4 bytes of
sha256(guest id)>`, and `CreateGuest` returns `already_exists` when
another record on the host has that tap or MAC rather than adopting the
device (`AddTap` was written to be re-run for the same guest and could
not tell the two apart). Existing records keep the tap and MAC stored in
`state.db`, so guests running through the hostd upgrade are untouched;
only new guests are named by the hash. *Rejected:* `tap-<index>` from the
address allocator (an index is reused after a destroy while the previous
guest's nftables objects may still be going away); the last eight hex of
the id (random, but a name should not depend on which part of an id
format is random). Interface: `host-conventions.md`. Verify on host-01
after the next host switch: two creates in the same minute, two taps,
`nft list set bridge repose guests` with two distinct tuples.

**I-121. `AgentEvent` on the host stream carries `tmux_window`, and a
Claude `Stop` without a readable transcript is summarised as "claude
finished".** (m3 integration, 2026-09-21) The first hook events from a
real guest on host-01 (`ops/checks/notifications.sh`, 00:08Z) reached the
api and were delivered to ntfy and email within a second, and every row
had `tmux_window` null and, for Claude's `Stop`, an empty summary. Two
gaps between three contracts: guestd's `AgentEvent` (vsock) carries
`tmux_window`, the `events` table and `13-notifications.md` §5.1 carry
`window`, but hostd's `AgentEvent` (gRPC, `grpc-hostd.md`) had no such
field, so hostd dropped what guestd had resolved and `repose status` could
never say which window finished. And `guest-conventions.md` says a `Stop`
summary is the transcript's last assistant line "else `claude finished`";
`mapClaude` sent the empty string instead. `hostd.proto` `AgentEvent`
gains `tmux_window = 5` (old shape accepted, empty means unknown), hostd
forwards guestd's value, the api's ingest stores it; the mapper falls back
to "claude finished". Interface: `grpc-hostd.md`.

**I-122. guestd's watcher accepts every process name an agent runs as;
Gemini CLI is `node`.** (m3 integration, 2026-09-21) On host-01 the
pane-idle heuristic (I-49) fired for pi 101 s after its window went quiet
and never for Gemini CLI: the watcher took a window for an agent's only
when the pane's process tree held a process named exactly after the agent
(`gemini`), and Gemini CLI is a bundle the guest's node runs (I-46), so
its process is `node`. The window was never an agent window, so neither
`AgentState` nor the completion existed for it. `binaries` is now a list
per agent (`gemini: gemini, node`) used by both the liveness check and the
foreground-command check. *Rejected:* matching the window name alone (a
user's shell renamed `gemini` would be reported as an idle agent, which
is what the process check exists to prevent). Verify on the next base:
`ops/checks/notifications.sh`'s gemini row.


**I-123. Gateway session reports are ordered and sent at most once.** (m2
gate, 2026-09-21) CI saw `TestSessionReportsAndCertCache` collect
`[opened, closed, opened, closed, closed]` for three connections: one
session's open never arrived and one close arrived twice. Both came from
`session.report`: the open was posted on the session's own context, so a
client that connected and left within the round trip cancelled its own
"opened" mid-flight (both attempts, since the retry shared the context),
and every report retried on any error, so a close whose answer was lost
after the api had recorded it was posted again. Nothing ordered the two,
either. Now both reports run on a context the session's end does not
cancel, the close report waits for the open report to finish, and a retry
happens only when the request never reached the api (a connection error),
never after a timeout or a refused answer. The api's `/internal/sessions`
keeps its set semantics per `(project_id, cert_serial)`, so a duplicate
would be harmless there but an open after its close would leave a
phantom `ssh_sessions` signal, which is the case the ordering closes. The
test asserts that closes never outnumber opens in report order and that no
event is delivered twice, run under the race detector twenty times.
*Recorded, not fixed:* `gateway_sessions` is keyed by `(project_id,
cert_serial)`, so several connections under one certificate count as one
session in `ssh_sessions`; a per-relay session id in the report would fix
the count and is an interface change for a later pass. *Rejected:*
retrying on every error with idempotent bodies (no session id exists to
make them idempotent).

**I-124. A destroy whose plan is empty still ends with the project
destroyed.** (m3 integration, 2026-09-21) The smoke project of the
mistyped base (create failed at the checkout, before `CreateGuest`) could
not be destroyed: `PlanDestroy` is empty for a project with no guest, the
op finished `done` at once, and the finaliser that sets `destroyed_at`
runs only from the `destroy_guest` phase, so the row stayed `error` with
its host set, and a user's `DELETE /projects/:id` on such a project would
have left them a dead project counting against their limit. `finish`
now calls the same finaliser for a `destroy` op whose project is not yet
destroyed; the phase result path is unchanged.
`TestDestroyWithoutAGuestMarksTheProjectDestroyed`.

**I-125. guestd's watcher also matches an agent by the executable's
name.** (m3 integration, 2026-09-21, amends I-122) On base 2026.09.21.1,
with `node` in gemini's name list, the gemini window still never counted
as an agent's: node renames its main thread, so `/proc/<pid>/comm` of
every Gemini CLI process reads `MainThread` (seen in m3-stamp: pids 909,
919, 1139, 1149, all `MainThread`, all `exe` = node). `treeHasComm` now
also compares the basename of the `exe` link, which names the binary
whatever the thread is called; `cmdline` and `environ` stay unread, as
`docs/SECURITY.md` promises and the strace test pins. Process samples
keep `comm` as their name. Next base.

**I-126. The api's parse-time syntax error is worded like hostd's.** (m3
integration, 2026-09-21) `PUT /config` checks syntax with
`nix-instantiate --parse` before any build (05 §5.7), so a syntax error
never reaches hostd's mapping and the user read Nix's own order, `syntax
error, unexpected ';' at fragment.nix:1:34 (fragment.nix:1)`, where
`nix-build-contract.md`, the fake api and the dashboard's tests all say
`syntax error at fragment.nix:1:34, unexpected ';'` (`ops/checks/menu.sh`
on host-01). The api's summariser now renders the contract's line and
the `invalid` message carries no `(fragment.nix:N)` suffix; the line is
in `detail.fragment_line` as before. Note for readers of the CLI's
output: it prints the local file's name in place of `fragment.nix`
(`RenderBuildError`), so the same refusal reads `at syntax.nix:1:34` on
a laptop.

**I-127. The CLI reads the op again when the build log stream ends.** (m3
integration, 2026-09-21) With I-114 in place, `repose config apply` on
host-01 still printed `error:` and nothing for a build that failed after
streaming: `waitOp` read the op once (running, no result), streamed the
SSE log, and when the stream's `done` event said `error` it returned that
stale op with its state flipped, so the code, message and fragment line
the api had written by then never reached `RenderBuildError`. A failure
the api knows at `PUT` time (the parse check, the secret-in-fragment
refusal) has no stream and was unaffected once I-114 landed. `waitOp`
now reads the op again after the stream ends.
`TestWaitOpReadsTheOpAgainAfterTheStreamEnds` (an httptest server that
answers running, streams, then answers the error).

**I-128. The CLI prints the verbatim block of a build error.** (m3
integration, 2026-09-21) `nix-build-contract.md` "What the user reads"
makes the message a summary line, a blank line, then the verbatim output,
and 07-cli.md §5.10 has the CLI print the summary, the fragment context,
then that block. `RenderBuildError` printed the summary and the context
only, so the first real closure over the cap on host-01 (`closure is
26.6 GB, limit is 20 GB; largest paths:`) named no path, and a
`build_failed` showed none of the builder's log. The block now follows
the context, newlines trimmed, indentation kept.
`TestRenderBuildErrorPrintsTheVerbatimBlock`.


**I-129. M2's two-person gate was closed with one person and a second
account.** (owner, 2026-09-21) `MILESTONES.md` asked for a second person
with a GitHub account to run `repose login` and `repose run` on their own
laptop, be refused the first person's guest, and get a notification. No
second person was available on the night, and the owner waived the clause
for M2 at 01:02Z on this evidence: the owner's own laptop run through the
released CLI (v0.1.4 by `install.sh`, device-code login on the email
account `user-c7fh26yzrl93`, `nuru-playground` and `age-calculator` created,
built and running on host-01, relays from the laptop through the switched
edge); m3's isolation suite on host-01 under two accounts (18 rows pass,
neighbour ratio 1.02, password attempts refused and logged on host and
edge); and the finished-agent deliveries to ntfy for claude, codex,
opencode and pi. What the waiver does not cover and stays open: a second
*human*'s laptop, OS keychain and SSH agent meeting the gateway, which
M5 step 3 still requires unchanged. The rehearsal that preceded the run
found and fixed twelve gate-blocking defects on the day (I-99, I-101,
I-102, I-104..I-111, I-120), which is what the two-person gate exists to
surface; the owner judged the remaining risk to be in the second laptop,
not the second account. *Rejected:* keeping M2 open until a second person
appears (M3 work on the shared host was waiting on it).

**I-130. One refused blob delete does not end the expiry run.** (m3
integration, 2026-09-21) `snapshots.Expiry.Once` looped "next expired row,
delete its blob, mark it" and returned on the first error, so the run that
hit I-131's `403 AuthorizationPermissionMismatch` (01:27:06Z, request id
`97d95da0-001e-0101-4468-49cafa000000`, the first of two aged m3-check
snapshots) deleted nothing, and would have re-selected the same row at
every run for as long as that one blob was refused: a single bad blob (a
lease, a mismatched name, a permission) would have held the whole
retention rule hostage. The run now reads its candidates once, then
handles each in its own transaction (`for update skip locked`, re-checked
against the rule so a restore that started meanwhile still holds its
snapshot); a refused delete logs `snapshot_expiry_fail` with the
snapshot id, leaves the row for the next run and moves on; the run ends
with an error naming how many rows it kept, so the job's own
`snapshot_expiry_fail` line still marks the run failed and the gauge of
oldest live snapshot still rises. `TestExpiryContinuesPastOneFailedBlob`
(fake store refusing one path). *Rejected:* marking the row deleted
anyway and letting the 45-day lifecycle rule collect the blob (the row is
the audit of what is in the container; a row that says deleted while the
blob is there is the shape 05 §5.8 forbids); retrying the same blob inside
the run (the failures seen are not transient at the run's timescale).

**I-131. The api's service principal gets Storage Blob Data Contributor on
the snapshots container.** (conductor, 2026-09-21) 05 §5.8 has the expiry
job delete expired snapshot blobs "through the Blob SDK with the api's
identity", and workstream 11 assigned that role to the hosts' managed
identity only. In production the first real expiry run (01:27Z, two
snapshots aged to 8 days on m3-check) was refused on its first delete with
`403 AuthorizationPermissionMismatch`, so the retention rule had never
deleted a blob; the lifecycle rule's 45-day delete was the only thing that
would ever have run. `modules/storage` now takes `api_identity_object_id`
(the same value `modules/keyvault` already uses for wrap and unwrap) and
assigns the role scoped to the container, created only when the id is
supplied, and `modules/environment` passes it through. Applied by the
owner with the usual `tofu apply`; until then the expiry row of 05 stays
open. *Rejected:* a custom role with only `blobs/delete` (a second role
definition to maintain for one verb, and the api already has read on the
same blobs through restore); running deletes from the hosts (the host's
identity is the one a compromised host holds, and retention is the
control plane's decision).

**I-132. A base bump that needs a reboot says so in its event.** (m3
integration, 2026-09-21) `basebump.OnOpFinished` raised `base_updated`
with the summary "base X applied" for every finished bump op, including
one whose apply ended with `reboot_required` (a kernel change on a running
guest, which 12 §5 leaves for the next stop/start): the owner would have
read "applied" on the notification while the guest still ran the old
kernel. The summary now says the base is built, that it changes the
kernel, and that it takes effect at the next `repose stop && repose start`;
the kind stays `base_updated` (13 §5 lists it, and an interface keeps its
shape one release). `TestBumpNeedingRebootSaysSo` (the fake hostd reporting
`kernel_changed`). *Rejected:* a new kind `base_update_ready` (a second
kind for one outcome of one job, which every channel and the CLI's
`events` would have to learn).

**I-134. The build phase takes its base from the revision, not the
project.** (m3 integration, 2026-09-21) `Engine.baseRef` read
`projects.base_version`, so a base bump, whose revision names the new base
while the project still records the old one, was built against the base
it was leaving, then the apply phase set the project's base to the new
version from the revision. On host-01 the 02:08Z security publish of the
kernel-flip base (38d1cbf) swept four projects in 5 s each, `build_ms`
around 330, `kernel_changed` false, no new checkout under
`/var/lib/repose/base`, and `repose-admin base status` reporting every
one "applied on 2026.09.21-m3-0208": the api believed the fleet was on a
base no guest had been built from; every earlier sweep on host-01 went
the same way, unnoticed because no published base had differed in
anything a 5 s eval would show. The revision's base wins now, then the project's,
then the newest published, then the dev checkout;
`TestBumpNeedingRebootSaysSo` asserts the fake hostd received the new
base's `nix_rev` and version in the Build. *Rejected:* setting the
project's base_version at enqueue time (a failed bump would then have
moved the project to a base it does not run).

**I-133. The api's `/metrics` is a Traefik router on the app, behind an
IP allow-list; no collector and no host port.** (owner via conductor,
2026-09-21) The `api` application publishes no port and cannot: a
published host port stops the old and the new container coexisting, so
Coolify could not roll it, which is the constraint that split `api-grpc`
off in the first place (I-2). Its 57 `repose_*` series therefore needed
a route of their own, and the choice between two was left open for the
owner. It is the router.

The proxy that already fronts the api serves `/metrics` from the same
container port, through custom labels on the app:
`Host(api.repose.herakraft.co) && Path(/metrics)` on the https entry
point, `loadbalancer.server.port=9103`, and an `ipallowlist` middleware
for `10.255.0.0/16, 10.200.0.0/16`. Nothing new runs, the rolling deploy
is untouched, and it rides the certificate Traefik already has.

*Rejected: Grafana Alloy as a Coolify service*, scraping `api:9103` and
`api-grpc:9103` by name on the docker network and remote-writing out. It
is the tidier shape on paper — the scrape never leaves the network, no
allow-list, and the same agent could later replace Fluent Bit on hosts
and the edge and make I-94's forwarding problem disappear. It loses on
two concrete grounds: it is a second resource to run, upgrade and back
up for one endpoint, and it needs a `remote_write` receiver that the
owner's Prometheus does not currently expose, so choosing it would have
meant changing the owner's stack to suit ours. The Alloy argument
survives intact for the day Fluent Bit is reconsidered; it just should
not have ridden in on this decision.

Three consequences worth writing down, because each is a way to get it
wrong:

- *The router matches on the `Host` header*, so a scrape aimed at
  `10.255.255.1:443` does not match it. The monitoring server resolves
  `api.repose.herakraft.co` to the tunnel address in `/etc/hosts` and
  scrapes the name, which keeps header, SNI and certificate correct;
  `prometheus.yml`'s `api` job is the name, not the address.
- *The allow-list is the only thing keeping `/metrics` off the
  internet*, since the router sits on the public entry point, and it
  matches the source address **Traefik sees**. That should be the
  monitoring peer's `10.255.0.x` over WireGuard, but it is unverified
  until the peer exists, and the failure mode is silent and open rather
  than loud and closed. `curl https://api.repose.herakraft.co/metrics`
  from anywhere else must answer 403; that check is part of closing 10's
  scrape row, not an optional extra.
- *The edge must forward 443 to the control plane*, not 9103, so
  `repose.edge.monitoring.scrapePorts` drops 9103 and gains 443. An edge
  built before this change would admit a port nothing listens on and
  refuse the one that answers.

Interfaces: none. `ops/coolify/README.md` holds the label block as the
documented step — a manual paste, which is the one thing here that I-87
would rather have in a file, so the block in the repository stays the
source of truth and a label that drifts from it is a bug.

**I-136. The api's user listener does not serve `/metrics`; the metrics
listener is the only place the registry is served.** (m5-release, 14 final
review, 2026-09-21) `internal/api/http.Server.New` mounted `GET /metrics`
on the user mux beside `/healthz` and `/readyz`, and the user mux is what
Coolify's proxy fronts on `api.repose.herakraft.co` with a `PathPrefix(/)`
router. So `curl https://api.repose.herakraft.co/metrics` answered 200 to
the internet: verified 2026-09-21 02:05Z from the dev box, from the edge's
public address and from the control VM, 57 `repose_*` series plus the Go
runtime's, no token asked. `docs/ops/OBSERVABILITY.md` promises "nothing
is reachable from the internet: every scrape and ship goes over the
edge's WireGuard", and I-133 built its whole argument on the allow-list
being "the only thing keeping `/metrics` off the internet" while the
application itself was serving it on the public port underneath. The
mount is gone; `MetricsHandler` on `API_METRICS_LISTEN` (`:9103`) is the
one metrics endpoint, which is also the port I-133's router already
points at (`loadbalancer.server.port=9103`), so the router keeps working
and the allow-list becomes defence in depth rather than the only gate.
`TestMetricsIsNotOnTheUserListener` pins it. What the series exposed:
counts of hosts, guests by state, ops, schedule results, secrets
operations, build failures, notification deliveries, rate-limit hits; no
tenant identifier (the registry refuses those labels, I-52) but a live
picture of the platform's size and activity, and an unauthenticated
handler on the public entry point that any scanner can hammer.
*Rejected:* keeping the mount and relying on I-133's `ipallowlist`
router (a label pasted by hand into Coolify, absent on the live app as of
this review; a defence that has to be present to work is not the layer
the application should depend on); a bearer check on the user-mux
`/metrics` (a second auth path for one endpoint that already has its own
listener). Interfaces: none (`api.md` never listed `/metrics`);
`05-control-plane-api.md` §5.1 corrected.

**I-137. hostd writes `host.json` and nothing else at registration; the
second `wg0.conf` under its state directory is gone.** (m5-release, 14
final review, 2026-09-21; closes review M-5) I-18 made `host.json` the
host's only runtime network input, rendered into `/run/repose/wg0.conf`
by `repose-host-net`; `internal/hostd/register.Register` still wrote
`/var/lib/repose/hostd/wg0.conf` and restarted `wg-quick-wg0.service`
itself, so a registration had two writers of one tunnel and a second copy
of the WireGuard private key on the persistent disk. Seen on host-01 as
deployed: `/var/lib/repose/hostd/wg0.conf`, 0600 root, dated the
registration (2026-09-20 18:15Z), unread by any unit. `writeWG`, the
`WGFile` constant and `Config.{Runner, WGUnit}` are removed; `WGConf`
stays as the renderer tests and operators compare against; `--no-wg` is
accepted for one release and ignored (its only effect was to skip the
restart that no longer happens; the `repose-host-net` restart after a
self-registration stays, per I-40). A host registered before this carries
the stale file until its next switch; it is inert, and the operator may
delete it. *Rejected:* keeping the file as a fallback for a host without
`repose-host-net` (every host has it; a fallback nobody runs is a second
truth).

**I-138. A project without a remote syncs its whole tracked tree and
commits it in the guest.** (m5-release, 07, 2026-09-21; review M5-9)
`repose run --name X` in a directory with no git remote, the case
`cli-config.md` gives `--name` for, created and booted its guest and then
failed at step 5c with `fatal: 'origin' does not appear to be a git
repository`: guestd sets `origin` only from the project's `remote_url`
(I-107) and the sync fetched it regardless. With `remote_url` empty the
sync now skips the fetch, the push prompt and the checkout; the tracked
files travel as a tar of their working-tree contents, are `git add`ed and
committed in the guest under a placeholder identity
(`repose <repose@localhost>`, a commit that exists only in the guest since
there is no remote it could reach), and the untracked files follow as
before. The commit is what keeps the guest tree clean, so the next run's
dirty-tree check still means what it means for every other project. A
file deleted on the laptop is not deleted in the guest: with no remote
there is no commit to derive the deletion from, and `git clean` against
an agent's tree is the one thing the sync must never do. *Rejected:* a
diff against the empty tree with `git apply --index` (a second run fails
on "already exists in index" unless the index and tree are cleared first,
which is the `git clean` above); leaving the staged files uncommitted (the
next run's dirty check refuses its own previous sync); refusing `--name`
without a remote (the flag exists for that directory). Interfaces: none;
`07-cli.md` §5.5f and `features/sync-at-launch.md` describe it.
*Superseded by I-150 (2026-09-23): the laptop's commits now travel as a
bundle for every project, remote or not.*

**I-139. `RegisterResponse` carries the SSH Host CA's public key; hostd
writes it to `host.json` and re-renders the host's network files after a
rotate that changes it.** (m5-release, 14 final review, closes review
M-1, 2026-09-21) `host-conventions.md` has documented `host_ca_pub` in
`host.json` since workstream 01, `repose-host-net` renders it into
`/run/repose/host_ca.pub`, sshd's `TrustedUserCAKeys` points there and
`repose-admin operator-cert` signs certificates for it; nothing ever sent
it. On host-01 as deployed the file is 0 bytes and every operator login
(750 in 24 hours) is by the bootstrap key in
`/etc/ssh/authorized_keys.d/root`, a static key with no serial in the
audit line, which is what `docs/SECURITY.md` "Not mitigated" recorded as
M-1. `RegisterResponse.host_ca_pub = 8` now carries `ca.HostCAPub()` from
the api (the same line `GET /internal/ca` and `POST /certs` already give
the gateway and the CLI), on `Register` and on `Rotate`; hostdev sends
its own SSH CA (the one it signs guest host keys and operator user
certificates with, review L-3); hostd's `write` keeps the previous value
when the field is empty, the same rule as `loki_url`, so an api that
predates the field changes nothing. Because the renderer runs at boot
and after registration only, the rotation loop now restarts
`repose-host-net` when a rotate changed `host_ca_pub` or `loki_url`
(I-95 said `Rotate` carries the Loki and never said how it reached the
file). What this does not do: remove the bootstrap key. host-01 keeps
`repose.host.bootstrap.enable` for the token and reinstall path (I-92);
"only with a certificate" (14 §9) needs that turned off once operators
hold certificates, which is 01/11's row. host-01 itself learns the CA at
its first rotate (about 2026-10-15) or by the runbook's by-hand step
("Operator certificate refused by a host"), taken at the host switch
that carries this change. *Rejected:* a separate unary RPC to fetch the
CA (a second round trip for one line that registration already answers);
delivering it in `Hello`'s ack on the stream (the stream is commands and
results; identity material travels on the unary path with the
certificate); hostd polling `/internal/ca` (hostd holds no gateway client
certificate). Interfaces: `grpc-hostd.md`, `host-conventions.md`,
`hostd.proto` (old shape accepted: field 8 is additive).

**I-140. Operator SSH logins reach `audit_log` as an `operator_login` host
event carrying the certificate's key id and serial, never its body.**
(m5-release, 14 final review, closes review L-13 and L-7, 2026-09-21) 14
§5 lists "every operator SSH login to a host or the edge" among the
audited actions; on host-01 as deployed the PAM hook wrote a journal line
(`pam_type`, `user_present`) and nothing reached Postgres: 750 accepted
logins in 24 hours, zero rows. And the line could not say which
certificate logged in (L-7). Now `hostd audit-login` parses sshd's
`SSH_AUTH_INFO_0` with `x/crypto/ssh`, keeps a certificate's `KeyId` and
`Serial` and the SHA256 fingerprint of the key (a plain key gives the
fingerprint alone, which is how the bootstrap key shows up), logs them,
and on `open_session` posts them to the daemon's control socket
(`POST /operator-login`, a 2 s timeout, failure logged and the login
never blocked); the daemon emits `Event.operator_login = 14` on the
stream with the usual event id and ack; the api's ingest writes
`audit_log (actor = key_id | "operator:key:" + fingerprint, action =
operator_login, target = host id, detail = {pam_type, user_present,
key_id, serial, key_fingerprint, host_event_id, ts})` and refuses a
second row for the same host event id, since a host re-sends an event
whose ack was lost. `PAM_RHOST` is read by nothing: the source address
is on the never-log list and an operator's address is not the audit's
business. *Rejected:* the api tailing the host's journal through Loki (a
log store is not an audit store, and no Loki is wired); writing the row
from the hook directly (the hook has no database and no api client, and
must never block a login on either); a separate unary RPC (the stream's
event path already has ids, acks and a re-send buffer). Interfaces:
`grpc-hostd.md` (Event kinds), `host-conventions.md` (`hostd
audit-login`), `hostd.proto` (old shape accepted: an api that predates
the kind ignores it and acks).

**I-141. A security sweep is due while the release is newer than this
process's last sweep.** (m3 integration, 2026-09-21) `basebump.Run`
checked every ten minutes for a security base released in the last ten
minutes. api-grpc is redeployed on every push to main, and a redeploy
inside that window restarts the ticker, so the release's only chance came
after its ten minutes were up and it waited for 04:00 UTC: the LTS
republish 2026.09.21.3 (02:32:36Z) met the 02:39:58Z rollout on host-01
and no project was rebuilt. The tick now sweeps when the newest base is a
security release newer than the last sweep this process ran, so a fresh
process sweeps it at its first tick and an old one does not repeat a
sweep it already made; a project already on the base is not touched by
either. `TestSecurityDueSurvivesRestart`. *Rejected:* a sweep at start
(a rollout of two replicas would race for the lock for nothing, and the
ten-minute tick is what `repose-admin base publish` promises: "unheld
projects rebuild at the next security sweep (within 10 minutes)"); recording the last
sweep in Postgres (a second replica's sweep would then hide a restart of
the first, which is fine, but the row is more state for a decision one
query answers).

**I-142. `host_moved` is raised only when a restore leaves the project's
host.** (m3 integration, 2026-09-21) The restore phase notified
`host_moved` ("was restored onto a new host") for every restore, including
`repose snapshots restore` over the stopped project's own volume on the
same host; on host-01 the three restores of 2026-09-21 (01:26, 01:57,
02:02Z) each sent the owner of m3-check that message while the project
never left host-01. `buildRestore` now records `host_moved` in the op's
params once it has picked the host (the project's when ready, else the
scheduler's), fixed on the first build like `new_guest_id` so a re-sent
command says the same; the restore result raises the event only when that
is true, and logs `restored` otherwise. `TestRestoreEmitsHostMovedEvent`
covers both directions (a same-host restore, then a project whose host
row is unreachable). *Rejected:* a `restored` notification kind for the
same-host case (the user asked for the restore and the CLI reports it; 13
§5's kinds are for what happens without them).

**I-143. The system activation leaves guestd running; guestd restarts
itself after a switch.** (m3 integration, 2026-09-21) A switch is run by
guestd. With the guestd unit at NixOS defaults, the new system's
activation stopped guestd whenever its binary had changed, and because
`switch-to-configuration` was guestd's child the stop killed it before
guestd's start step: the 2026.09.21.3 sweep on host-01 (02:59Z) left
age-calculator, m3-iso-c and m3-held with no guestd, `/run/current-system`
on the old system, the op `guest_unresponsive` (`guestd Switch: vsockrpc:
EOF`) and every later start re-applying the same revision with the same
result. `nix/guest/base/guestd.nix` sets `restartIfChanged = false` and
`stopIfChanged = false`, so the activation of any base from here on never
touches guestd, whichever guestd runs it: that is what makes the fix reach
the stranded guests, since it is the new closure's activation that
decides. guestd then compares the new system's `guestd.service` ExecStart
with its own binary and, when they differ, schedules `systemctl restart
guestd` 3 s later through `systemd-run --on-active`, after its Switch
result has reached hostd; hostd logs `guestd_lost` then `guestd_regained`.
The activation itself runs through `systemd-run --wait --pipe` as a
transient unit, so no stop of guestd can kill a switch again.
`TestSwitchAppliesWithoutReboot` (the wrapper, no restart for the same
binary) and `TestSwitchSchedulesGuestdRestartWhenItsBinaryChanged`.
*Rejected:* `KillMode=process` on guestd's unit (it is the old unit's
KillMode that applies, so it would not have helped the guests already
running); hostd re-checking the guest after an EOF (guestd was stopped,
not restarting, so there was nothing to wait for).

**I-144. One clone per base ref.** (m3 integration, 2026-09-21) Two
builds that needed the same new base at once each cloned into
`<ref>.tmp`; the second clone's pack landed in the first's directory and
one of them failed with `fatal: fetch-pack: invalid index-pack output`
(nuru-playground's bump onto 2026.09.21.3, 02:59:18Z, its revision
`failed`). `ensureBase` is serialized on the builder; the second build
finds the first's checkout. `TestEnsureBaseClonesOnceUnderConcurrency`.
*Rejected:* a per-ref lock (a map to maintain for a lock held for the
seconds of a clone a few times a week).

**I-145. A bump that built but could not switch says so.** (m3
integration, 2026-09-21) `base_update_failed` read "base X failed to
build" for every failed bump op; three of the 02:59Z sweep's had built and
failed at the switch. When the revision row is `built` the summary is
"base X built, but switching the running guest to it failed; the guest
keeps its current system", then the op's message.
`TestBumpSwitchFailureSaysSwitch`.

**I-146. A bump that failed against an older base is tried again on the
next.** (m3 integration, 2026-09-21) The sweep skipped every project whose
newest revision was `failed`, meant for a fragment the user has to fix
first. A bump can fail for the platform's reasons (I-144's clone), and the
rule then held the project on its old base until the user applied
something by hand. The skip now applies only when the failed revision is
on the latest base already; a newer base is a new attempt.
`TestSweepBuildsUnheldSkipsHeld` (2026.09.30 after the failed
2026.09.29). A fragment that is really broken fails again on the next
base and the user gets one `base_update_failed` per base, which is the
right amount of noise.

**I-147. A start applies only a built revision newer than the one the
guest runs.** (m3 integration, 2026-09-21) `buildApply` picked the newest
`built` row with a closure, and the http start route asked whether any
such row existed. A bump that ends `built` and is later superseded by an
applied newer revision leaves that row `built` forever; m3-held's start
at 04:38Z (its 2026.09.21.3 bump `built` with a reboot pending, then
2026.09.21.4 applied in place) applied the .3 closure over the running .4,
and that older base's pre-I-143 activation stopped guestd. Both now
select the newest built row created after the project's current revision
(`ops.PendingRevision`); a superseded row is not pending. *Rejected:*
marking superseded rows `stale` (a fourth status for what a timestamp
comparison says).

**I-148. The activation's output goes to a file, and hostd asks again
once when guestd went away mid-switch.** (m3 integration, 2026-09-21)
I-143 ran the activation as a transient unit with `systemd-run --pipe`;
when an older base's activation stopped guestd (I-147's case) the pipe's
reader was gone and the activation died of SIGPIPE before its start step,
which is the strand again by another route. The unit now appends to
`/run/repose/switch.log`, which guestd reads after `--wait`; nothing the
activation does to guestd can end it. On hostd's side a transport error
from Switch (EOF, not a remote error) waits for the guest's next session,
up to `guestd_lost_after` plus 30 s, and sends the same Switch once more:
the profile is set and the activation of the same system is a no-op, so
the retry is idempotent, and an activation that restarted guestd ends as
`done` instead of `guest_unresponsive`. `TestSwitchAppliesWithoutReboot`
(the log file, no pipe) and hostd's `TestBuildAndApply` (the fake guestd
dropping the first Switch, answered on the second connection).

**I-156. A destroy the user asked for always finishes; DELETE answers
with the op to wait on.** (server-robust, 2026-09-23) age-calculator's
guestd died in the 2026-09-21 base switch while its unit kept running;
each `repose destroy` (2026-09-22 02:05Z, 2026-09-23 00:03Z) failed at
step 0 with `guest_unresponsive` because the final Snapshot needs guestd's
Freeze, the project stayed in `error` billing its disk, and the CLI had
already printed "Destroyed." from the 202. The ops engine now has one
recovery per op (`ops.recoverFrom`, recorded in the op's params as
`recovered`): a destroy whose snapshot or stop fails `guest_unresponsive`
is replanned from that step as stop (no snapshot; hostd's stop falls back
to the hypervisor's shutdown and then to killing the unit, none of which
needs guestd), snapshot of the stopped volume (crash-consistent at worst,
reason `stop`, expiring in 30 days like any destroy snapshot), destroy.
If that snapshot still cannot be taken it is skipped with a
`snapshot_failed` event saying the newest earlier snapshot is kept, and
the destroy goes on. A `not_found` from the host in any destroy phase
means the guest is already gone and the op is done. `error` remains for a
real host failure (lvremove, the blob upload), with the host's code, and a
later destroy resumes: the stopped guest is snapshotted and removed.
`DELETE /projects/:id` answers `202 {op_id, state}` (the `state` field is
new; `op_id` was already there), and a DELETE while a destroy op is open
answers with that op instead of `409`, so a client that lost the first
response can still wait. hostd is unchanged: every step above uses
commands and states the running hostd already has. Healthy guests take
the old path (freeze, snapshot, stop). `TestDestroyWithDeadGuestdReachesDone`
(from `error` and from `running`), `TestDestroySkipsAnImpossibleFinalSnapshot`,
`TestDestroyOfAGuestTheHostLostIsDone`, `TestDestroyAndRestartContract`.
*Rejected:* always stopping before the destroy snapshot (it changes the
healthy path for a case the fallback covers); a hostd change to freeze-or-
fall-back inside Snapshot (needs a host switch, and a snapshot that
silently stops being clean is worse than an op that says so).

**I-157. `repose start` on a project in `error`, or on a running one
whose guestd stopped answering, restarts it onto its newest built
revision.** (server-robust, 2026-09-23) age-calculator's start sent
StartGuest (ok: the unit was running) then ApplyConfig (guestd dead),
four times in two minutes; only `repose-admin projects restart` could
bring it back. The start route now enqueues `ops.PlanRestart` for
`error`, and for `running` when the newest meter sample (under five
minutes old) says `guestd_ok = false`: stop without a snapshot, apply the
pending revision (I-147's `PendingRevision`) to the stopped guest, which
hostd does by moving its GC root without guestd, then StartGuest, which
boots that closure. The response is `{op_id, restart}`. A start op whose
ApplyConfig fails `guest_unresponsive` (the I-143 strand on the old plan)
turns into the same restart once. Booting on the new closure instead of
switching into it is what makes this safe for I-143's case: the
activation that stopped guestd is not run. `TestStartFromErrorRestarts`,
`TestStartWhoseApplyLosesGuestdRestarts`, `TestDestroyAndRestartContract`.
*Rejected:* a separate `restart` op kind or route (the op kinds are
fixed, and the user's recovery command should be the one they already
type); detecting a dead guestd from `guestd_lost` warnings (the sample's
`guestd_ok` is already stored per project).

**I-158. Stop, resize and snapshot on a dead guestd.** (server-robust,
2026-09-23) The same "fails instantly for ever" check across the other
ops. Stop with a snapshot sent StopGuest{snapshot_first}, whose freeze
needs guestd: it now recovers as stop without a snapshot, then a snapshot
of the stopped volume (reason `stop`); if that snapshot fails, the
project stays `stopped` with `last_error` and a `snapshot_failed` event
instead of going to `error`. Resize extends the volume and tells the
hypervisor before GrowFs needs guestd, and a retry repeats the same
failure; manual snapshots need a freeze. Neither can succeed without
guestd and neither should reboot the user's guest unasked, so both end
`error` with a message naming `repose start`, and resize's retry after
the restart completes (hostd skips the lvextend already done).
`TestStopWithDeadGuestdStopsThenSnapshots`,
`TestUnresponsiveErrorsAreSentences`. Not fixed: nothing grows the guest
filesystem after a resize of a stopped guest (no boot-time growfs in
`nix/guest`); that predates this and is recorded here for workstream 02.

**I-159. An op's error message is a sentence; the host's wording is
`detail`.** (server-robust, 2026-09-23) `op.error.message` and
`projects.last_error` carried hostd's text verbatim, e.g. `guestd
unreachable for guest 01a0c161-…`, a guest id the user never sees
anywhere else. `failWithLine` now maps the codes users meet to a sentence
that says what to do (`guest_unresponsive`: "the environment's agent
(guestd) stopped answering; `repose start` restarts it", with variants for
a boot that never answered, a resize and a snapshot; `host_unreachable`;
`insufficient_capacity`), keeps `code`, and moves the host's message to
`error.detail` for operators. Other codes keep their message. Logs are
unchanged: the op-failure line carries the code, never either message.
`TestUnresponsiveErrorsAreSentences`.
**I-160. A create reuses a closure the host already runs.** (provision-speed,
2026-09-23) The owner's `izma` create spent 6.1 s in `Build` (eval 5.7 s,
build 0.33 s, 6.2 s CPU, 872 MB peak) to produce the closure four guests on
host-01 were already running: the fragment was the default one and the
base the latest. The system closure depends only on the fragment's text,
the base ref and the base version label (I-34, I-43; checked in
`nix/flake.nix`: `guestSystem` reads nothing else), so the create's
`build` phase now looks for the applied revision of another live project
on the chosen host (not destroyed, not `destroying` or `error`, guest id
set) with the same fragment on the same published base version; when one
exists the revision becomes `built` with that closure and
`kernel_changed = false`, no `Build` is sent, and `CreateGuest` follows in
the same engine pass. That guest's GC root (`guests/<id>`, removed only by
`DestroyGuest`) keeps the path on the host. Only published bases qualify:
`dev` names the api's `--base-ref`, which can move. A create whose
fragment matches no running closure, every restore and every
`config apply` still build. The build phase also records `base_version` on a create's revision
now (the `status <> 'building'` guard skipped that update for every create,
so those rows had none; the reuse query reads the project's base for
them). Saves the whole `Build` phase (5–6 s on a warm host, more on a cold
one) for most creates. `TestCreateReusesAClosureOnTheSameHost`.
*Rejected:* a per-host cache keyed by a fragment hash (a table to keep in
step with GC for what one indexed join answers), and sharing across hosts
(the path would have to be copied, which is a build's cost again).

**I-161. The guest boot's critical chain: no wait for Docker, the console or
a mount rate limit.** (provision-speed,
2026-09-23) m3-check's boot on host-01 (base 2026.09.21.x, small, a start):
Cloud Hypervisor started 02:31:55.24, hostd `running` 02:32:09.38, 14.1 s.
In the guest: kernel 0.84 s, initrd 4.21 s, then `docker.service`
8.66 → 11.04 s, and guestd (`After=docker.service`), sshd (`After=guestd`)
and everything behind them waited for it; after guestd started, Ready came
at the second 0.5 s poll of `/proc/net/tcp` (11.69 s), `RegisterPaths`
at 12.14 s, `repose-paths` saw its stamp at its next 0.5 s poll (12.65 s),
home-manager ran 1.07 s, `systemd-user-sessions` lifted `/run/nologin` at
13.79 s and SetupProject's `systemctl --user -M dev@` (a login session)
returned at 13.85 s, which is when hostd says `running` and when an SSH
login is first accepted. Nothing guestd does at boot needs Docker, so
guestd is now ordered only after tmpfiles and `network.target`; Docker
starts in parallel. guestd's `docker_down` warning waits 60 s from its
start (`sample.DockerGrace`) unless the socket has answered once, so a
Docker still starting is not reported as down. The Ready poll is 0.1 s
(`guestd.ReadyPollInterval`) and so is `repose-paths`' wait: both sit on
the path to the first login.

Two silent waits in the same boot were found by booting the guest runner
with Cloud Hypervisor on the dev box (unprivileged, virtiofsd
`--sandbox none`, `systemd.log_level=debug`; its untouched baseline
matched host-01 within 0.1 s): systemd queried the serial console's size
and terminfo and waited out the timeout, 0.67 s in the initrd and 0.33 s in
stage 2, and the initrd services' credential mounts tripped systemd's
mount-monitor rate limit, which held `sysroot.mount` and the store mount
back for about 0.7 s. The base now passes
`systemd.tty.{term,rows,columns}.console` on the kernel line (all three are
needed) and clears `ImportCredential` on the initrd's services (a drop-in
for the upstream `systemd-fsck-root` and `systemd-tmpfiles-setup-sysroot`;
without it the initrd refused the unit). On the dev box, CH start to
guestd Ready went from 11.88 s to 6.22 s and 6.39 s with this commit's
runner (`nix build ./nix#guest-runner`), about 5.5 s off every boot
(create, start, restore, reboot); on host-01 that is expected to take
CH-to-`running` from 14 s to about 8.5 s, confirmed only after the next
base publish. Not adopted: virtiofsd `--cache always` (hostd) took another
0.9 s off on the dev box, but an in-place `ApplyConfig` needs the guest to
see store paths that appear after boot, which that mode's long-lived
dentry cache is not known to do; it needs its own test first. An
uncompressed initrd changed nothing. `TestDockerDownWarnsOnce` (no warning inside the
grace), `TestDockerDownAfterItAnsweredWarnsInsideTheGrace`.
*Rejected:* keeping guestd after Docker and moving only sshd
(hostd's `running` waits for guestd's Ready either way).

**I-162. mkfs leaves the inode tables to the guest's lazy init.**
(provision-speed, 2026-09-23) izma's CreateGuest on host-01 spent 1.50 s
between `creating` (00:06:54.05) and `starting` (00:06:55.56), against
0.13 s for m3-check's StartGuest (02:31:55.10 → 55.23), which does the same
steps without the volume. `lvs` answers in 24 ms and `blkid` in 2 ms there,
so the difference is `mkfs.ext4 -E lazy_itable_init=0`: the thin volumes
report `write_zeroes_max_bytes` 0, so mke2fs writes the inode tables as
real zeros, about 670 MB for a 40 GB volume (izma's volume was 2.23 percent
allocated right after the create, the 20 GB ones 0.8 percent), at the
disk's 600 MB/s. hostd now runs `mkfs.ext4 -E lazy_itable_init=1`; the
guest kernel's ext4lazyinit zeroes the tables in the background at its own
low rate, and unprovisioned thin blocks read as zeros meanwhile. Expected:
about 1 s off every create (not start) and no 670 MB write burst on a disk
every guest on the host shares. Needs a host switch.
`internal/hostd/lvm` test pins the argv. *Rejected:* `noinit_itable` in the
guest's mount options (never zeroing is safe on thin but is one more
guest-visible difference for no time the create would see).

**I-163. An op enqueued in one api process wakes the driver in the other
through NOTIFY.** (provision-speed, 2026-09-23) The api and api-grpc each
run an ops engine and only the one holding the ops lock drives; `Kick` only
woke its own process. On 2026-09-23 api-grpc drove: izma's Build result to
the CreateGuest send took 45 ms (the result lands in api-grpc and kicks
locally), but `POST /projects` is served by the api, so the op waited for
api-grpc's 500 ms poll before its Build went out (the api logged the POST
and api-grpc the command in the same second; hostd started the Build at
00:06:47.86). A `Kick` in an engine that is not driving now also runs
`select pg_notify('repose_ops', '')`, and the driver `LISTEN`s on a
dedicated connection and turns each notification into a tick. The 500 ms
poll stays as the fallback, so a lost listener or a failed notify costs
latency, never an op. Expected: up to 0.5 s (0.25 s on average) off the
start of every op a user or `repose-admin` enqueues, and the same off each
phase when the api rather than api-grpc holds the lock.
`TestEnqueueInAnotherProcessWakesTheDriver` (driver polling once an hour,
the create done 1.8 s after the other engine's kick). *Rejected:* a
shorter poll (four times the queries for half the gain) and routing the
enqueue over the internal gRPC hop (a new RPC for what Postgres already
carries).

**I-149. The CLI has its own passphrase-less key, and one SSH connection
per command.** (cli-ux, 07, 2026-09-23; owner's v0.1.4 session) The
certificate was issued for `~/.ssh/id_ed25519`. The owner's is
passphrase-protected and not in an agent, and every `runSSH` opened a new
connection, so one `repose run` asked for the passphrase four or five
times and the gateway closed the connection while a prompt waited
(`Connection closed by … port 22`, exit 255). The CLI now generates
`~/.ssh/repose/id_ed25519` in process (ed25519, no passphrase, 0600; no
`ssh-keygen` needed) and the certificate is issued for it; a certificate
on disk for any other key is re-issued, so a v0.1.4 laptop moves over on
its next command. The user's own keys are never read, written or
offered, and nothing is `ssh-add`ed. The generated config points
`IdentityFile` at the new key, keeps `IdentitiesOnly yes` (I-108), and
adds `ControlMaster auto`, `ControlPath ~/.ssh/repose/cm-%C`,
`ControlPersist 10m` (not on Windows, whose OpenSSH has no
multiplexing): the first ssh of a command opens the connection and every
later one (probe, sync, tmux, attach) is a session on it, one handshake
per `repose run`. The persisted master counts as an open gateway session
for up to ten minutes after the command ends; nothing acts on that count
today. `runSSH` bounds the wait for ssh's pipes (`WaitDelay`) so an old
client whose master held them could not hang the CLI. A connection the
gateway refuses (`Permission denied`, a revoked certificate) gets one
forced re-issue before the 60-second wait continues (the HANDOFF finding
"no certificate re-issue on certificate revoked").
`TestEnsureCertMovesOffTheUsersKey`, `TestRenderSSHConfigGolden`,
`TestSyncOverAMultiplexedConnection` (one TCP connection seen by the fake
guest for the whole sync). *Rejected:* asking for the passphrase once
and loading the key into an agent (needs an agent, and the key would
still be offered elsewhere); `ssh-add` of the certificate (the old
behaviour, which prompted too). Interfaces: `ssh-gateway.md` "CLI side"
and `cli-config.md` in this commit.

**I-150. The laptop sends its commits to the guest; the guest never
fetches origin during a sync.** (cli-ux, 07, 2026-09-23; owner's session)
Step 5c ran `git fetch origin` in the guest, which has no credentials for
a private repository (and none for a public one with an SSH remote,
which is what guestd sets, I-107): `git@github.com: Permission denied
(publickey)`, and izma's checkout stayed an empty `git init`. The sync is
now two ssh round trips on the multiplexed connection. The first creates
the checkout if missing, and reports the dirty list, the commits every
guest ref points at, and whether `origin` exists. The laptop filters
those commits to the ones it has, and `git bundle create`s `HEAD` and its
own `refs/remotes/origin/<branch>` excluding them (the whole history the
first time; nothing when the guest is current). The second carries one
tar (bundle, `git diff HEAD --binary`, the untracked tar) and one script:
stash or discard if asked, `git fetch` from the bundle, move
`origin/<branch>` to the laptop's view of it (only forward), add
`origin` if missing (guestd's URL rule), then check out: the branch is
created, or fast-forwarded when it is behind, and left alone with the
laptop's commit checked out detached when the guest's branch has commits
the laptop lacks (an agent's work is never moved off its branch), with a
warning saying so; then the diff and the untracked files as before. The
push prompt of 5.5c is gone: the commit travels whether or not it was
pushed, and nothing is pushed. A `--name` project with no remote uses the
same path without the remote-tracking ref, which replaces I-138's
placeholder-author commit and makes a laptop deletion a guest deletion (a
guest synced the I-138 way has a placeholder commit on its branch, so its
first sync this way checks out detached and says so). A laptop checkout
that is not a repository, has no commit, or is shallow gets a sentence
saying what to run. Credentials sync first, in one ssh: the allowlisted
files, the git identity through `git config --global` (from files in the
payload, not the command line), and, when gh's login travelled and the
remote is on github.com, `url.https://github.com/.insteadOf
git@github.com:` plus gh as the credential helper for github, so an
agent's `git push` works. gh 2.40+ keeps its token in the laptop keyring
and `hosts.yml` has none; the CLI then writes `gh auth token`'s value
under `github.com:` in the copy that travels (the same login, copied over
SSH: the second of the three homes for secrets).
`TestSyncSendsAnUnpushedCommit`, `TestSyncIntoAnEmptyGuestRepo`,
`TestSyncLeavesADivergedGuestBranchAlone`, `TestSyncNoRemoteSendsHistory`,
`TestSyncCredentialsCopiesExactlyTheFourRows`,
`TestSyncCredentialsCarriesAKeyringGhToken`. *Rejected:* forwarding the
laptop's agent for the fetch (ForwardAgent stays, but the owner had no
agent, and HTTPS remotes need a token anyway); pushing for the user (the
v0.1.4 prompt: it publishes work the user did not ask to publish, and
fails the same way when the push is refused). Interfaces:
`guest-conventions.md` (the `.gitconfig` row) in this commit.

**I-151. The CLI proves the `<slug>.repose` alias works and says exactly
how to fix it when not.** (cli-ux, 07, 2026-09-23; HANDOFF finding) On
the owner's laptop `ssh age-calculator.repose` did not resolve after the
Include line was written. `ensureIncludeLine` only checked that the text
appeared somewhere; an `Include` after a `Host` or `Match` line applies
to that block only, and a symlinked `~/.ssh/config` (home-manager) was
replaced by a regular file. Now an Include counts only before the first
Host/Match line (a fresh one is prepended otherwise, the misplaced one
left where the user put it), a symlinked config is edited at its target
when writable and never replaced, and after writing, `ssh -G
<slug>.repose` must resolve to the gateway host and the `<slug>.<handle>`
user. When it does not, the command warns with the reason and the line
to add (for a read-only link, where to add it, with the home-manager
option), and the CLI's own connections use `ssh -F ~/.ssh/repose/config`
so the run still works. An unwritable `~/.ssh/config` is therefore a
warning, not the exit 1 of 07-cli.md §6. `TestIncludeEffective`,
`TestEnsureIncludeLineLeavesAReadOnlyLinkAlone`.

**I-152. A directory's cached project must share its remote, and naming
a project never writes the directory cache.** (cli-ux, 07, 2026-09-23;
owner's session) In the nuru-wasm checkout `repose run` and `attach`
went to age-calculator: `resolveProject` trusted `by_dir[cwd]`, and every
explicit `--project` wrote `by_dir[cwd]`. Now an explicit project
(positional, `--project`, `$REPOSE_PROJECT`) writes nothing; `by_dir` is
written only when `run` creates a project with no remote (the case it
exists for, `cli-config.md`), keyed by the repository root so a
subdirectory finds it; and a `by_dir` or `by_remote` entry is believed
only when that project's `remote_url` equals the directory's normalised
remote (or both are empty). A mismatched or 404 entry is deleted from
`projects.json`, which cleans the entries v0.1.4 wrote.
`TestResolveProjectOrder` (poisoned entry ignored and forgotten, explicit
project writes nothing, root key).

**I-153. The CLI says what actually happened: the true state, why, and
the next command.** (cli-ux, 07, 2026-09-23; owner's session) `repose
destroy` printed "Destroyed." from the 202 while the op failed and the
project stayed; `attach` said "is stopped. Run `repose start`" for a
project in `error`; errors reached the user as `guestd unreachable for
guest 01a0…` or `ssh cd ~/izma && git status --porcelain: exit status
255: …`. Destroy now asks `Destroy <slug>? A final snapshot is kept for
30 days. [y/N]` (the owner's wording; typing the name added nothing
given the snapshot; `--yes`/`-y` skips it, and without a terminal the
CLI asks for `--yes` instead of assuming), waits on the op DELETE
returns (api.md, I-156), prints `Destroyed <slug>` only when it is done
and `GET` answers 404, and otherwise `Could not destroy <slug>: <reason>
(<code>). <slug> is still there, in state error. \`repose destroy
<slug>\` tries again.` An api without the op id is waited on by polling
the project. Every command that needs a running guest names the real
state with its own next step (exit 5 kept); an `error` project's reason
comes from the api's `last_error`, which since I-159 is a sentence and is
shown as is, while an older api's host wording is mapped by code and
never shows a guest id. Failed ops read `Could not <verb> <slug>:
<reason> (<code>). <next>`, with the host's `detail` only under `-v`; a
failed ssh reads `Could not <step>: <why> (<ssh's last line>).` and never
includes the remote command. `repose projects` has a header row, a dash
for what does not apply (uptime only while running; v0.1.4 printed 47h
for a project two days in `error`), and one line per errored project
with its reason; `--json` is unchanged. The `[y/N]` prompts now default
to no on an empty answer (v0.1.4's helper said yes to an empty answer
on `snapshots restore`'s `[y/N]`), and `secrets set` reads the value with
echo off. The destroy message names the project id for the restore,
since a destroyed project no longer resolves by name and the snapshot
commands accept that id. The api's `last_error`, `host_unreachable` and
`signals.guestd_ok` are read when present (they are in the api's Project
JSON, not yet in api.md's shape). `TestDestroyReportsAFailedOp`,
`TestDestroyConfirmationIsYesNo`, `TestNotRunningMessagesSayTheTruth`,
`TestProjectsTable`, `TestSSHErrorsAreSentences`.

**I-154. Long commands show live phases.** (cli-ux, 07, 2026-09-23;
owner's session) `repose run` on a new project sat silent for over a
minute. Commands that wait (run, start, stop, destroy, resize, snapshot
create and restore) now show the phase on stderr: on a terminal one
spinner line with the elapsed time, turned into `✓ <done>  <time>` when
a phase has its own result (Created, Built the environment, Booted), and
elsewhere one plain `<phase>...` line per phase; `run` ends with `Ready
in <time>.` The phase follows the project's state while an op runs
(creating, building, starting), a start the api turned into a restart
says `Restarting <slug> (its agent stopped answering)` (I-157), and the
build log streams through the same line without tearing it. Ops and the
project are polled every 500 ms (two cheap GETs), not 2 s; the build log
stream no longer inherits the api client's 30-second timeout, which cut
every longer build's log off, and a stream cut short resumes from its
last line. `--json` commands show nothing; Ctrl-C clears the line and
exits 130. `TestProgressOutput`, `TestStartFromErrorSaysRestarting`.

**I-155. A project is the argument of the commands whose object it is.**
(cli-ux, 07, 2026-09-23; owner's request, "like docker logs
<container-name>") `attach`, `start`, `stop`, `destroy`, `status`, `logs`
and `events` take `[PROJECT]`; `--project` and `$REPOSE_PROJECT` still
work, and naming two different projects is a usage error. `run` keeps
PROMPT as its argument (now everything after the flags, so quoting is
optional), and a one-word prompt that is exactly one of the user's
project slugs is refused with exit 2 and the right command (`repose run
izma` was almost certainly not a prompt; `--agent` sends it anyway).
`open`, `secrets`, `config` and `snapshots` keep `--project`, since their
argument is a port, a name, a path or a snapshot id. A stray argument on
a command that takes none is now a usage error (v0.1.4 ignored `repose
attach projects` and attached to the checkout's project), and cobra's own
refusals (unknown command, flag or arity) exit 2 instead of 1.
Completion offers the account's slugs for the argument and for
`--project` (the api with a two-second limit, else the cached slugs),
and fixed values for `--agent`, `--size`, `--kind`.
`TestPositionalProject`, `TestRunRefusesAPromptThatIsAProjectName`.

**I-164. A snapshot reads the blocks the filesystem uses, not the whole
volume.** (destroy-restore, 03, 2026-09-23; owner's `repose destroy izma`,
38 s) izma's destroy on host-01: StopGuest 02:22:46.48, `snapshot start`
the same millisecond, `snapshot done` 02:23:19.33 (`duration_ms` 32853,
`bytes` 2059646), stop 3.5 s, DestroyGuest 0.1 s. Nothing logged in the
33 s because nothing but one pipeline ran: `dd if=<snapshot> bs=4M |
zstd -T4 -3` reads every byte of the thin volume, and the unprovisioned
ones read as zeros that compress to nothing. The journal since
2026-09-21 shows the time follows the volume, not the data: every 20 GB
volume took 15.0-16.5 s (1.5-4.0 MB uploaded), every 40 GB one 30.9-34.8 s,
whether it carried 2 MB or 227 MB (02:26 and 02:31 the same night). The
freeze, `lvcreate -s`, the upload and the lvremove are each well under a
second. Every stop, nightly and destroy snapshot paid it, and every
restore paid it again writing 40 GB back through `dd conv=sparse`.
hostd now runs `dumpe2fs` on the activated snapshot and, when the
filesystem is ext4, `clean` and without `needs_recovery` (ext4's freeze
flushes the journal and clears that flag, and so does a clean shutdown),
reads only the ranges the block bitmaps mark used, drops 64 KiB pieces
that are all zero (the inode tables `lazy_itable_init=1` leaves unzeroed,
I-162), and frames the rest as `offset, length, bytes` records behind a
`RPSXT001` header and before a trailer with the byte count, through the
same `zstd -T4 -3` to the same blob path. Restore peeks the magic after
`zstd -d`: extent streams are `pwrite`n into the fresh volume (which reads
zeros everywhere else, the assumption `conv=sparse` already made) and
checked against the trailer, raw streams take the old `dd`. Anything the
bitmaps cannot be trusted for (a killed guest's journal, a volume that is
not ext4, dumpe2fs output that does not list every group) falls back to
the raw read, and the log line says which and why (`format`,
`raw_reason`, `used_bytes`, `volume_bytes` on `snapshot done`). On the dev
box a 40 GB sparse ext4 holding a 1 GB file: raw 23.6 s, extents 0.23 s,
same compressed size (`TestExtentSnapshotTiming`, a sparse file standing
in for a thin volume on an AMD box, so direction and scale, not host-01's
number). Expected on host-01: izma's snapshot from 33 s to well under a
second of reading plus the upload of what it holds; a stop from 38 s to
about 5 s. Needs a host switch; the api is unaffected. Old blobs restore
as before; an extent blob cannot be restored by a hostd older than this
(e2fsck fails that restore and only the new volume is touched), which
matters only once a second host exists. `TestExtentSnapshotRoundTrip`
(mkfs.ext4 -d, round trip onto an empty device, e2fsck -fn clean, files
equal via debugfs), `TestRawFallbacks` (not ext4; `needs_recovery` set
with debugfs), `TestParseDumpe2fsRefusals`,
`TestExtentStreamRefusesATruncatedOrOversizedStream`. *Rejected:* the
thin pool's own mapping (`thin_dump` needs `reserve_metadata_snap` on the
live pool every guest shares, and knows provisioned blocks, not used
ones); `e2image -ra` to a pipe (writes the zeros itself, so the time
stays proportional to the volume); keeping the LVM snapshot on the host
and exporting it later in the background (a host-side queue to reconcile
across restarts for seconds of upload nobody waits on once the CLI
returns at once, I-166, and a destroyed project whose only copy is on
one host is not restorable elsewhere until it lands).

**I-165. A destroy stops the guest first, reads `destroying` from the
moment it is accepted, and says so when it fails.** (destroy-restore, 05,
2026-09-23) izma's destroy froze the running guest, snapshotted it for
33 s (I-164) with the guest still running and billed, then stopped it.
`PlanDestroy` is now stop (no snapshot), snapshot of the stopped volume
(reason `stop`, 30-day expiry), destroy; a stopped project skips the
stop. Ending the guest first ends its hours and its sessions at once, and
the snapshot of a shut-down filesystem is clean without guestd's freeze,
so the dead-guestd case of I-156 is now the ordinary path (the recovery
stays for ops enqueued before this). `DELETE` sets the project to
`destroying` in the same transaction as the enqueue, and the engine keeps
it there through the stop and the snapshot (it used to read `stopping`,
then `running` again during the snapshot of a stopped project, and
`destroying` only for the last 0.1 s). A destroy that fails leaves the
project in `error` with `last_error` as before and now also records a
`destroy_failed` event, which notifies like `snapshot_failed`: the CLI no
longer waits for the destroy (I-166), so the failure has to reach the
user by itself. A destroy op enqueued before this release with the old
plan (stop, destroy) still snapshots inside its stop, because the stop
snapshots unless a snapshot phase follows. Timeline for izma, api-only
(before a host switch): DELETE → `destroying` at once; stop 3.5 s;
snapshot 33 s (the guest already down); destroy 0.1 s. With I-164 on the
host: stop 3.5 s, snapshot about 1 s plus the upload of what the volume
holds, destroy 0.1 s. `TestDestroyStopsFirstAndReportsItsFailure`,
`TestDestroyWithDeadGuestdReachesDone` (no recovery needed now),
`TestRestoreByName` (the state right after DELETE). *Rejected:* keeping
the freeze-then-stop order (the guest runs and bills through the whole
upload for a snapshot that is less clean); marking the project
`destroyed` right after the stop and finishing the snapshot and the
volume in the background (a project listed as destroyed whose final
snapshot may still fail is a promise the list cannot keep; I-164 makes the
remaining work seconds).

**I-166. `repose destroy` returns when the api has accepted the destroy.**
(destroy-restore, 07, 2026-09-23; owner: "perhaps we need to make it
background, but the issue is getting that restore command") v0.1.5 waited
the whole op (38 s for izma) to print the restore command. After the
`[y/N]` the CLI now sends `DELETE`, and on the `202` prints `Destroying
<slug>. Bring it back within 30 days with: repose restore <slug>` and
exits 0: one api round trip, well under the 1-2 s target. The restore
command no longer needs anything the op produces, because `repose
restore` resolves the name and the newest snapshot itself (I-167).
Failures stay visible without the wait: the project reads `destroying`
from the DELETE on (I-165), a failed destroy leaves it in `error` with a
reason whose next step is `repose destroy <slug>` (the api writes it into
`last_error`, and `repose projects`/`status` show it instead of the
generic "`repose start` restarts it"), and `destroy_failed` notifies.
`--wait` keeps the I-153 behaviour for scripts (wait on the op, then on
the 404, `Destroyed <slug> in <time>` only when gone, `Could not destroy`
and exit 1 otherwise) and its last line now names `repose restore <slug>`
too. Found on the way: `stop` and `destroy --wait` took the *last*
element of `GET /snapshots` as the newest, but the api lists newest first
(the fake oldest first), so a stop printed its project's oldest snapshot
id; both now pick the newest by `created_at` (`newestSnapshot`). The
"destroying/destroyed" not-running message names `repose restore` as
well. `TestDestroyConfirmationIsYesNo`, `TestDestroyThenRestoreByName`,
`TestProjectsShowAFailedDestroy`, `TestNewestSnapshotIgnoresOrder`,
`TestDestroyReportsAFailedOp` (now with `--wait`). *Rejected:* waiting
only for the stop and returning before the snapshot (still 4 s, and the
stop is not what the user cares about); a background process on the
laptop that waits and notifies (a laptop that closes loses it; the api
already notifies).

**I-167. Restore by name: `GET /projects/destroyed`, `POST
/projects/restore`, `repose restore NAME`.** (destroy-restore, 05/07/08,
2026-09-23; owner: "the restore command is too complicated") The destroy
message ended with `repose snapshots restore <snapshot id> --project
<project id> --as-new NAME`, because a destroyed project no longer
resolves by name. `GET /projects/destroyed` lists the user's destroyed
projects that still have a restorable snapshot (not deleted, not past
`expires_at`), newest destroy first, with that snapshot, its expiry as
`restorable_until`, and `name_free`. `POST /projects/restore {slug |
project_id | snapshot_id, name?, start?}` resolves a slug the way a user
means it (the live project with that slug if there is one, else every
destroyed project that had it), takes the newest restorable snapshot
unless one is named, and restores it as a new project called `name`,
default the source's name; a taken name is `409 conflict` with
`detail.reason = "name_taken"` so a client can ask for another, and a
project with nothing left is `404` with `detail.reason = "no_snapshot"`.
The new project also gets the source's `remote_url` when no live project
has it (the old as-new restore dropped it, so a checkout never found the
restored project), and `POST /projects/:id/snapshots/:sid/restore
{as_new_project}` now shares that code. Both routes are new, nothing old
changes shape. api.md also gains the Project fields the api has returned
since I-157/I-159 (`last_error`, `host_unreachable`, `signals.guestd_ok`)
and the restore route's `start` and `project_id`. The fake api has both
routes, `destroyed_at`-ordered, and the three fields.
`TestRestoreByName` (api, against Postgres), `TestWalkthrough` (fake).
*Rejected:* restoring in place when the name is live (that replaces a
running project's disk; `repose snapshots restore ID` still does it, with
its stop prompt); a `?destroyed=true` switch on `GET /projects` (one
route, two shapes); resolving by slug inside the existing snapshot route
(its path starts with a project id, which is what the user does not have).
CLI: `repose restore NAME [--as NEW-NAME] [--snapshot ID]` posts that
route (NAME may also be a project id, so the v0.1.5 destroy message's id
still works), waits with the create phases and ends `Restored <slug> from
its snapshot of <time> in <elapsed>; it is running (<class>). \`repose
attach <slug>\` to get in.` A name in use asks for another on a terminal
(empty cancels) and otherwise exits 2 naming `--as`; nothing to restore
exits 3 with the api's sentence and `repose projects --destroyed`, which
lists `PROJECT CLASS DESTROYED SNAPSHOT SIZE RESTORABLE UNTIL` (`--json`
is the api's list). Completion for `restore` offers the destroyed slugs
(the api, two seconds at most). `repose snapshots restore` is unchanged.
`TestDestroyThenRestoreByName` (fake api: destroy, list, restore by name
with the remote, live-name 404, name taken without and with a terminal,
unknown name). Not done: `repose restore` with no NAME inside a checkout
whose project was destroyed (the remote could find it; it asks for the
name instead).

**I-168. The dashboard lists recently destroyed projects with a Restore.**
(destroy-restore, 08, 2026-09-23; owner: "perhaps they can use the
dashboard?") `/projects` gains a "Recently destroyed" section under the
table, from `GET /projects/destroyed` (I-167): name, class, a `name in
use` badge, "Destroyed <relative time> · snapshot <time>, <size> ·
restorable until <date> (N days left)", and `Restore…`, which opens a
name field with the old name (or `<slug>-restored`, `-2`… when a live
project has it), posts `POST /projects/restore {project_id, name}`, and
goes to the new project's page, where its state shows the restore
running. A `name_taken` 409 shows under the field instead of a toast. The
section is a `form-section` with a `font-display` heading and `btn-ghost`
row actions, per DESIGN-LANGUAGE.md; nothing new in `layout.css`. The
section is asked for only after the live list loaded, because its
answer would otherwise clear the "cannot reach the api" bar that a
failed list had just raised (the failure-mode test caught this), and an
error from it (an api older than I-167) hides the section silently. A
project in `error` now shows its `last_error` sentence under its state
in the table, so a destroy that failed after the dashboard moved on is
visible there too, with the retry the api wrote (I-166). The destroy
toast says the destroy continues and where to restore from. Tests:
`tests/project-lifecycle.spec.ts` (destroy through the UI, the row with
its expiry, Restore under the same name, lands on the new project),
`src/lib/destroyed.test.ts`. *Rejected:* a separate `/projects/destroyed`
page (one more route for a list that is short and belongs next to the
live one); restoring on one click without a name field (it creates a
billed project, and the name may be taken).

**I-170. The owner's monitoring server is peer 10.255.0.3 on the edge, over
plain WireGuard, interface `wg-repose`.** (owner, conductor, 2026-09-23) The
owner asked why not Tailscale: it would work (a tagged auth key per host and
a tailnet ACL allowing only `tag:repose-host` to reach Loki), but it puts
hosts that run tenant code on the owner's personal tailnet, where
containment rests on an ACL the repository cannot check, and adds a secret
per host. The WireGuard peer needs one public key, and the edge's forward
chain (I-94) already confines it to the scrape ports outward and 3100
inward. The monitoring server runs `wg-quick` on the host rather than a
container: host networking plus `NET_ADMIN` is no narrower, and a unit on
the host survives Coolify restarts. The interface is `wg-repose` (the
file name), not `repose`, so it reads as a tunnel on a machine that runs
other things. `repose.edge.lokiUrl` is set to the same Loki, so the edge
ships its journal too. *Rejected:* Tailscale (above); a WireGuard container.

**I-179. The billing period is the Stripe subscription's, stored at the
hour; each usage hour is reported to Stripe at its last second.** (m4-billing,
09, 2026-09-23) The rollup anchored periods at `users.created_at` (the
customer is created at first `GET /me` and `EnsureCustomer` wrote
`billing_anchor = coalesce(billing_anchor, created_at)`), while Stripe's
subscription, created at card attach, anchors its invoices at that moment.
A user who signed up on the 1st and added a card on the 10th had the cap,
the storage spread and the egress allowance reset on the 1st while the
invoice ran 10th to 10th, so one invoice could carry parts of two capped
periods, more than the flat cap, and `reconcile` compared different
windows. `EnsureSubscription` now stores the subscription's
`billing_cycle_anchor`, truncated to the hour, as `billing_anchor`, which is
what `features/pricing.md` already promised ("a month from the day the
account added its card"). No usage exists before that, because compute
needs a card (R2-10). Stripe's period starts at the exact second (say
14:32:10) and the rollup's at 14:00, so a meter event stamped at the start
of its hour would put the anchor hour before the subscription existed and,
every later month, the anchor-day 14:00 hour into the previous invoice.
Events are stamped at hh:59:59 instead; for any anchor inside an hour, an
hour lands in the same period on both sides
(`TestMeterTimestampLandsInTheRollupsPeriod`, four anchors including the
31st and an exact hour, four months of hours each). The identifier, and so
the idempotency, is unchanged. *Rejected:* `backdate_start_date` to the hour
(behaviour of the cycle anchor under backdating is not something to rely
on for money); a future `billing_cycle_anchor` at the next hour (a prorated
stub invoice for every new card).

**I-180. `repose-admin billing stripe-bootstrap` makes the Stripe objects
and prints the api's environment.** (m4-billing, 09, 2026-09-23)
`docs/ops/AZURE-SETUP.md` step 17 asked a human to click together a
product, three meters, three prices and a webhook endpoint pinned to the
SDK's API version, then copy eleven ids into Coolify; one wrong id bills
nothing or rejects every event, silently. The command takes
`STRIPE_SECRET_KEY` from the environment only (never argv), refuses a live
key without `--live` before sending anything, and finds every object before
creating it by a key Stripe holds: product id `repose`, meter event names,
price lookup keys `repose_<part>_cents_<PriceVersion>`, a portal
configuration marked in metadata, the endpoint's URL. A second run creates
nothing and prints the same block, except the webhook signing secret,
which Stripe shows only at creation; the block then says to keep the
existing value, and `--rotate-webhook` replaces the endpoint. An existing
meter or price with a different shape, or an endpoint on another API
version, stops the run with what to do rather than being edited (a price is
never edited, 09 §8). Stripe Tax is read, not activated (activation needs
the owner's business address), and decides `STRIPE_AUTOMATIC_TAX`. The
customer portal configuration it creates allows card, address, email, name
and tax id updates and invoice history, and not cancelling the
subscription, which is the platform's rather than a plan the user picked;
the api passes it as `STRIPE_PORTAL_CONFIGURATION`, because a fresh account
has no default configuration and every portal session would fail. Progress
goes to stderr, the block alone to stdout. `ops/stripe/bootstrap.sh` wraps
it with a silent prompt for the key. *Rejected:* a curl and jq script (no
API-version pinning, and nothing to test it against); Stripe CLI fixtures
(not idempotent, and a second tool to install).

**I-181. A card is never refused over tax configuration.** (m4-billing, 09,
2026-09-23) With `automatic_tax` on, Stripe refuses to create a
subscription for a customer it cannot locate (`customer_tax_location_invalid`)
or when Stripe Tax is not set up, and `OnCardAttached` runs inside the
`setup_intent.succeeded` webhook: the card would be on file, `has_card`
true, and no subscription, so no usage could ever be invoiced. The
payment method's billing address is now copied onto the customer before
the subscription is created (Stripe Tax reads `customer.address`), and a
tax refusal retries once without automatic tax under its own idempotency
key, with a warning log line (`action=subscription_create_no_tax`). The
charge is still correct; the invoice carries no tax line. In the same
path, the subscription's idempotency key was `subscription:<user id>` for
ever: a card added again within 24 hours of `customer.subscription.deleted`
got the deleted subscription back from Stripe's key cache, and a webhook
retried more than 24 hours after Stripe created a subscription whose id
never reached the database created a second one; two subscriptions on the
same meters each invoice the same usage.
`EnsureSubscription` now lists the customer's subscriptions first, adopts
a live one on the compute price, and otherwise creates under
`subscription:<user id>:<how many the customer has ever had>`
(`TestSubscriptionIsAdoptedNotDoubled`).

**I-182. The dashboard adds a card on Stripe's hosted Checkout page; the
publishable key is retired.** (m4-billing, 08/09, 2026-09-23) The billing
page embedded a `PaymentElement` that needed `PUBLIC_STRIPE_PUBLISHABLE_KEY`
baked into the web image as a build argument, a second key for the owner to
find and a rebuild of `web` to turn billing on, and it collected no full
address, which Stripe Tax needs (09 §5.9). `POST /billing/setup` now also
takes `{"flow": "checkout"}` and answers `{url}` of a Checkout session in
setup mode with `billing_address_collection=required` and
`customer_update.address=auto`; with no body it still answers
`{client_secret}` (the old shape stays, api.md). Checkout confirms a
SetupIntent, so the same `setup_intent.succeeded` webhook attaches the
card and creates the subscription. It returns to `/billing?card=saved` (the
page waits up to 15 s for the webhook to land) or `?card=cancelled`.
`@stripe/stripe-js` and the `PUBLIC_STRIPE_PUBLISHABLE_KEY` argument are
gone from `apps/web`. *Rejected:* keeping both forms (two code paths for one
button); serving the publishable key from the api (still a second key to
find).

**I-183. `GET /billing/invoices` returns documented names.** (m4-billing,
09, 2026-09-23) api.md said only "from Stripe"; the api answered
`total_cents`, `created`, `hosted_invoice_url` and `pdf`, and the dashboard
read `amount_cents`, `created_at` and `pdf_url`, so every real invoice
would have rendered as `$NaN` with no date. Each element is now `{id,
number, status, currency, amount_cents, subtotal_cents, tax_cents,
created_at, period_start, period_end, hosted_url, pdf_url}`, written into
api.md; the four old names are still sent for one release. The row's
"cached 5 min" was never built and is dropped from it: the page asks once
per visit, well inside Stripe's rate limits.

**I-184. A $0 invoice settles nothing, and a card arriving at zero credit
ends the trial.** (m4-billing, 09, 2026-09-23) Two holes in the trial's
edges. First, Stripe marks $0 invoices `paid` (the one issued when a
subscription is created, and every month the trial credit covers), and
`invoice.paid` raised the limits to 10/10 and cleared `past_due`: the
"3/1 until the first paid invoice" rule (§5.8) was void from the moment a
card was added, and a $0 invoice could wipe a failed one. `invoice.paid`
with `total <= 0` now records the invoice and changes nothing else.
Second, the end of the trial requires a card, so an account whose card was
removed while its guests ran (§6 lets them run) spent its credit and stayed
`trial` at zero; adding a card back left it refused as `trial_depleted` with
a card on file, the lockout 09 §5.3 set out to prevent. `setup_intent.succeeded`
now moves a `trial` account with no credit to `active` in the same
statement that sets `has_card`. Tests: `TestZeroInvoicePaidRaisesNothing`,
`TestTrialCreditDepletesThroughTheRollup`.

**I-185. The M4 gate is proven on Stripe test clocks with the real rollup,
not with 100 real hours; "blocks a start at zero" means the gate's three
refusals, not a stop.** (m4-billing, 2026-09-23) The milestone's pattern (one
large guest, 100 hours, 40 GB, 10 GB egress) needs a whole billing period
to invoice. `TestStripeTestModeM4Gate`, which runs only with
`REPOSE_STRIPE_TEST_KEY=sk_test_...`, puts a customer on a test clock
frozen 33 days back (inside Stripe's 35-day meter-event window, so every
event is in real time's past; the clock stands a minute before the period end while they are pushed), subscribes it
through `OnCardAttached`, writes the pattern into `meter_samples`, rolls up
every hour of the period with the production rollup (which pushes real
meter events), advances the clock across the period end and asserts the
invoice lines equal the `usage_hours` rows per part and 800 cents in total.
The failed-payment half uses `pm_card_chargeCustomerFail` (the
4000000000000341 card), feeds the real `invoice.payment_failed` and
`invoice.paid` event objects through the webhook handler, and runs the
dunning job at day 2 and day 3 plus an hour; the job's clock is its `Now`,
because `past_due_since` is the api's time, not Stripe's. What is simulated
is only the guest's minutes (samples), which is the one input the rollup
reads (09 §3); a real guest's samples are proven separately by the live
short-pattern run in `docs/ops/M4-GATE.md`. On the trial, `PRICING.md` and
`features/pricing.md` settled that the hour spending the last cent moves an
account with a card to `active` and "nothing stops" (09 §5.3); the milestone's
"depletes and blocks a start at zero" is therefore proven as: the credit
depletes to exactly zero through the rollup and the account moves to
`active` in the same transaction; at zero without a card every start and
create is refused `card_required`; a `trial` account at zero is refused
`trial_depleted`; and a card arriving ends the trial (I-184). *Rejected:*
blocking starts at zero for accounts with a card (contradicts the pricing
page and turns the trial's end into an outage); waiting 100 real hours on
host-01 (proves the sampler, which M1 already did, at the cost of four days
per retry).

**I-186. Console capture ends only after the hypervisor has exited; closing
it drains first.** (live-polish, 03, 2026-09-23; conductor's destroy of
e2e-a-private at 04:03:35Z) Three stops of e2e-a-private guests on base
2026.09.23 each waited out hostd's 60 s, then `vm.shutdown` 15 s, then
SIGKILL, while branding and e2e-polish guests stopped in 3.5-4 s. The
guest's persistent journal (e2e-a-private's destroy snapshot restored as
e2e-polish) shows the hung shutdown's PID 1 stop mid-list, 18 ms after
`Stopping Serial Getty on ttyS0...`, where a clean one goes on to sshd
3 ms later; the vCPUs sat in `kvm_vcpu_block`. Reproduced on demand with
the conductor's path (`repose run` with the full sync from the e2e-a
checkout into e2e-polish, stop 25 s later: `Stopped e2e-polish in 1m00s`),
and host-01's `/proc/<ch>/task/*/comm` shows Cloud Hypervisor's
`serial-manager` thread present at 04:31:08 and gone at 04:31:11, the
second the stop began. Cause, from CH 53's source: hostd's stop removed
the guestd monitor and with it console capture right after sending
Shutdown, while the guest was printing its shutdown. A unix socket closed
with bytes unread hands its peer ECONNRESET 
(`TestCloseWithUnreadBytesResetsThePeer`); CH's serial manager returns on that read error without
detaching the socket, so every later byte the guest's UART sends fails,
`Serial::handle_write` returns before `thr_empty()` and the error is
dropped (`.ok()`), the transmit-empty interrupt never comes, and every
userspace write to ttyS0 blocks for good: PID 1's console status lines,
agetty, and guestd, whose stdout is `journal+console` (it logged "shutting
down" cut mid-line in the conductor's console.log). Fix, hostd only: the
stop keeps console capture until the guest unit is inactive
(`monitor.stopKeepConsole`); `console.Tailer` reads until the socket has
been quiet 100 ms (at most 2 s) before closing, so a hostd restart or a
restart of capture does not leave bytes unread either; `WaitInactive`
returns the deadline instead of reading a systemctl killed by it as
"inactive" (the 04:11:19 stop logged no fallback and tore down a running
guest); the fallback sends `vmm.shutdown` after `vm.shutdown`, which alone
left the VMM process waiting for a new VM for the whole 15 s.
`TestStopKeepsConsoleUntilTheHypervisorExits` (fails on the old stop.go),
`TestRunDrainsBeforeClosing` (the old Tailer broke the writer's pipe),
`TestWaitInactiveTimeoutIsNotInactive`. Needs a host switch; no base
publish. Not fixed: the CH bug itself (a patch that treats a read error as
a detach and raises THRE after a failed write belongs in an overlay, with
a CH rebuild and a real boot to prove it), and the same stall after a
hostd restart that races a console write, which the drain makes unlikely
but not impossible. That race is a candidate for age-calculator's guestd
that "died" in the 2026-09-21 switch while its unit ran (I-143, I-156):
guestd's next log line would block on the stalled console. *Rejected:*
dropping `console` from guestd's output and the ttyS0 getty in the base
(needs a publish and treats the symptom; the getty is also M-6's finding
and stays for workstream 02 to decide).

**I-187. Reads have their own rate-limit bucket; the CLI waits out a 429
and polls less as a wait grows.** (live-polish, 05/07, 2026-09-23;
conductor's `repose restore e2e-a-private` ended `rate_limited: too many
requests`) I-154's 500 ms poll is two GETs, 240 a minute, against a 60/min
bucket per user that every request shared, including the owner's
dashboard tab. The api now counts GETs in a 600/min bucket and every other
method in the 60/min one (`RateLimits.Reads`, default ten times
`General`); the fake does the same when `RateLimit` is on. The CLI's
client treats `rate_limited` as "nothing ran" (the api refuses before any
handler) and sends the request again after `Retry-After` (2 s without it,
at most 15 s per wait) for up to 60 s, and a wait polls every 500 ms for
10 s, then 1 s, then 2 s after a minute (`pollDelay`). A refusal that
outlasts that is a sentence ("The api is refusing this account's requests
for now: too many in the last minute…"), never `rate_limited: …`.
`TestRateLimits` (240 polls pass, reads end at 600, writes at 60),
`TestRateLimitedRequestIsRetried`, `TestRateLimitedNeverShowsTheRawCode`,
`TestPollDelayBacksOff`. api.md "Rate limits" in this commit. *Rejected:*
exempting op and project GETs entirely (an uncapped read load on the
database); polling every 2 s from the start (I-154's
phases would lag the boot by seconds).

**I-188. A command that creates a project ends with SSH to it working, and
closes the multiplexed connection it no longer means.** (live-polish, 07,
2026-09-23) After `repose restore e2e-a-private`, `ssh
e2e-a-private.repose` said `certificate not valid for this project`: the
certificate's principals are project ids and the restore made a new one,
and only a command that connects (run, attach) re-issued it. `repose
restore` and `repose snapshots restore --as-new` now end with
`refreshSSHAccess`: the account's projects, `ensureCert` (re-issued only
when a project is missing from the certificate) and the per-project Host
blocks. A failure there is a warning naming `repose attach`. Stop, destroy
and restore also run `ssh -O exit` for the slug: I-149's ControlPersist
master outlives the guest it reached, and a restored project with the same
slug has the same ControlPath, so the next command's sessions rode a dead
connection (`Could not read the guest's checkout: the SSH connection to
the guest failed`, 04:08:50). Found on the way: the package's tests wrote
the developer's real `~/.ssh/repose` (certificate, config, known_hosts with
the fake CA) once restore renewed certificates; a package `TestMain` now
points HOME and XDG_CONFIG_HOME at a temporary directory.
`TestRestoreRenewsTheCertificate`. Live: `repose restore <id> --as
e2e-polish`, then a plain `ssh e2e-polish.repose` listed the guest's boots.

**I-189. One refusal banner per connection, on its own line, and words
that fit the state.** (live-polish, 06, 2026-09-23) ssh offers the
certificate and then the plain key on one connection, and each refusal's
banner arrived without a newline: `e2e-a-private is destroying;
environment is not accepting connections yetpermission denied
(certificate required)…`. A banner now ends in `\n`, the plain key's
"certificate required" after another banner is not sent, nor the same
banner twice. States read: stopped `X is stopped; run \`repose start X\``,
destroying `X is being destroyed; \`repose restore X\` brings it back once
that is done`, error `X is in error; \`repose status X\` says why`,
transitional `X is creating and not accepting connections yet; try again
in a few seconds`, unreachable host `X is on a host the control plane
cannot reach right now; try again shortly`. `TestOneBannerPerRefusal`,
`TestStoppedAndOtherStates`. ssh-gateway.md in this commit. Needs an edge
switch.

**I-190. Restoring a project whose destroy is still running waits for its
final snapshot.** (live-polish, 05/07, 2026-09-23) `repose restore` right
after `repose destroy` said "has no snapshot left to restore": the live
project with the name was `destroying` and its final snapshot is the
destroy's second step (I-165); an earlier snapshot of it would have been
restored instead, which is worse. `POST /projects/restore` by slug now
answers `409 conflict`, `detail.reason = "destroying"`, "…is still being
destroyed; its final snapshot is not taken yet. Try again in a few
seconds", and the CLI shows `Waiting for X's destroy to take its final
snapshot` and retries every 2 s for up to 3 minutes; against an older api
it recognises the same case from `no_snapshot`, or from `name_taken` once
the snapshot exists but the destroy has not finished, plus a `destroying`
project of that name. Live against the current api: `repose destroy -y
e2e-polish; repose restore e2e-polish` printed `Waiting for e2e-polish's
destroy to take its final snapshot...`, then restored, and a plain `ssh
e2e-polish.repose` answered at once (I-188). `TestRestoreOfADestroyingProjectSaysSo` (api,
Postgres), `TestRestoreWaitsForADestroyInProgress` (CLI, fake). api.md in
this commit.

**I-191. A phase is printed once, and it names the slug.** (live-polish,
07, 2026-09-23) Without a terminal `repose run` printed `Creating
e2e-a-private...` twice (run's own phase, then the project's `creating`
state), and the owner's run showed `✓ Created teksafari.org (large)`
followed by `Booted teksafari-org`: the create phase used the name as
typed. `progress.Phase` with the label already running is a no-op (the
first done text stays), the create phase starts when the api has answered,
with its slug, and restore's phase uses the state's label (`Restoring X`,
the snapshot's time is on the last line). `TestProgressPrintsAPhaseOnce`.

**I-192. `repose projects --destroyed` is one row per name; `status` names
the host and the newest event.** (live-polish, 07, 2026-09-23) izma was
listed three times with nothing saying which one `repose restore izma`
takes. The table now has one row per name, the one restore picks (the
newest snapshot of the destroyed projects with that name, I-167), an
`EARLIER` count, a line with `repose restore <id> --as NEW-NAME` for a
name a live project holds (where `restore NAME` means the live one), and
`--all` lists every row with its id. `repose status` printed the host's
uuid and, for a running project, `last event … guest_state_changed
"creating"`: the api's route has carried `host_name` since M2, and events
and snapshots were taken from the end of a list the api sends newest
first; both are now picked by time (`newestEvent`, `newestSnapshot`).
`TestDestroyedListIsOneRowPerName`, `TestStatusShowsHostNameAndNewestEvent`.
Live: `e2e-polish small running … host host-01 … last event 12m ago:
guest_state_changed "running"`.

**I-193. The key in b1a5915 stays in history; it was rotated.** (conductor,
2026-09-23) The owner rotated the key committed in b1a5915, so the value in
public history authorises nothing; rewriting history and force-pushing
would break every clone and worktree for no security gain. *Rejected:*
a history rewrite, and making the repository private for this reason
alone. Any future leaked credential is rotated first, the same way.

**I-194. Dependency directories never travel; symlinks travel as links.**
(conductor, 2026-09-23) The owner's `repose run` in teksafari.org failed
with `read …/cms/node_modules/.pnpm/…/@actions/exec: is a directory`, and
the guest was left with an empty checkout: `cms/node_modules` was not
gitignored, pnpm builds it from symlinks to directories, and the sync
followed them. Untracked files are now `Lstat`ed: a symlink is written as a
tar symlink, a directory or special file is skipped, an unreadable file is
skipped. `node_modules` and similar dependency and cache directories are
skipped at any depth whatever .gitignore says, named once in a warning:
they are rebuilt in the guest for its platform, and shipping them is slow
and wrong. `sync.exclude` patterns now match any leading directory, and one
sync sends at most 500 MB of untracked files. *Rejected:* following
symlinks (duplicates whole trees, loops) and failing the sync on the
first odd file (the whole environment stays empty for one bad path).
**I-171. A running guest's snapshot takes the extent path; the premise
that it could not was wrong.** (hardening, 03, 2026-09-23; HANDOFF finding
"running-guest snapshots still read the whole volume") The finding held
that a mounted ext4's superblock says `not clean`, so I-164's check
(`clean`, no `needs_recovery`) would send every frozen running guest to
the raw read. It does not: ext4 clears `EXT4_VALID_FS` on mount only for a
filesystem without a journal, so a journalled ext4 reads `clean` while
mounted (seen on this box's 6.17 kernel with a loop mount), and what
changes while mounted is `needs_recovery`, which `ext4_freeze` clears
after `jbd2_journal_flush` has checkpointed the journal into place and
before it commits the superblock, and `ext4_unfreeze` sets again. The
LVM snapshot is taken inside the freeze, so the snapshot's own superblock
is the witness: no `needs_recovery` means the freeze held when the
snapshot was cut (the delayed allocations were forced out by the
freeze's `sync_filesystem`, and the on-disk bitmaps are current or
over-mark, never under-mark: preallocations and open-unlinked files are
marked used). If guestd's 10 s watchdog thawed before `lvcreate`
returned, the flag is set in the snapshot and the raw read follows, which
is the right answer. host-01 already shows it since the 03:05Z switch:
manual snapshots of two running guests at 03:08:16Z,
`format: extents`, 763 ms (40 GB volume, 1.07 GB used) and 1314 ms (20
GB, 570 MB used), against 15-35 s for every running-guest snapshot
before. So no code change: the check stays `clean` and no
`needs_recovery`, and does not start accepting `not clean` on the word
of hostd's freeze record (a `not clean` journalled ext4 means errors or a
journal-less filesystem, where no freeze witness exists).
`TestFrozenRunningVolumeRoundTrip` proves it on a real mount (root, `go
test -c` and the binary under sudo): writes left in the page cache, a
deleted file and an open unlinked one, `fsfreeze -f`, a sparse copy of
the device as the LVM snapshot; the copy has no `needs_recovery`, goes
out as extents, every byte the bitmaps mark used (76 MB of 1 GiB) comes
back identical and every other byte zero, `e2fsck -fn` reads the
snapshot and its restore identically, and the restore's own `e2fsck
-fp` leaves `-fn` clean; the same mount copied without the freeze says
`needs_recovery` and goes raw. RUNBOOK's slow-snapshot entry names the
watchdog case. *Rejected:* accepting `not clean` when hostd recorded a
successful freeze (unneeded, and it would trust hostd's bookkeeping over
the filesystem's own statement).

**I-172. `repose restore` with no NAME finds the project by the
checkout's remote.** (hardening, 07, 2026-09-23; I-167's "not done")
Inside a checkout, the CLI lists `GET /projects/destroyed` and keeps the
rows whose normalised `remote_url` is the checkout's `origin`. One name
(destroyed once or several times) restores the newest destroy of it, by
`project_id`, from its newest snapshot, with no question; two or more
names ask `Several destroyed projects were checkouts of <remote>: a, b.
Which one (empty to cancel)?` on a terminal and otherwise exit 2 listing
them; no match exits 4 naming the remote; a directory without a remote
exits 2 asking for the name. By id rather than by slug, because the api
resolves a slug to the live project first, which is not what "this
checkout's destroyed project" means. A name that is taken asks for
another, as with NAME. `TestRestoreWithoutANameInACheckout` (a real git
checkout against the fake api: no remote, nothing destroyed, one match
with no question, two names without and with a terminal, an unknown
answer asked again). Needs a CLI release.

**I-173. `base publish` takes only a full sha that is on main.**
(hardening, 05/12, 2026-09-23; STATUS 2026-09-21 finding) 2026.09.21 was
published as `3f83664f`, which does not exist, and every create on that
base failed at the host's checkout until the next publish. `repose-admin
base publish` now refuses, before writing the row, a rev that is not 40
hex characters (with the `git rev-parse` to run), one GitHub does not
have (`push it`), and one not on the branch (`merge it to main first`);
the check is GitHub's compare API, `compare/main...<sha>`, which answers
`behind` or `identical` exactly when the sha is main or an ancestor of it
(verified against the live repository: af916e5 is `behind`, the 2026.09.21
sha is 404). The repository is `--repo`, else `$REPOSE_BASE_REPO`, else
`https://github.com/Heracraft/factory.git` (host-01's `baseRepo.url`);
the branch `--branch`, default main; `GITHUB_TOKEN` is sent when set.
Unanswerable (GitHub down, a non-GitHub repository) is an error that
names `--unverified-rev`, which skips only the repository check (the
rev must still be a full sha) and is recorded in the audit row. The rev
is stored lowercased. `TestBaseRevGate` (a fake GitHub: on main, short,
a branch name, unknown, off main, a 502; the three GitHub URL shapes),
`TestAdminSurface` (short refused, full published through the command,
the checker asked for the right repository and branch). Rolls with the
api image on merge; the control VM needs egress to api.github.com, which
Coolify's own pulls already use. *Rejected:* resolving a short sha (the
owner types what `git log --oneline` shows, and a short sha that is
unique today may not be tomorrow; hosts check out exactly the stored
string); `git ls-remote` (lists refs, not commits, and the api image has
no git).

**I-174. api-grpc's metrics port is published on the WireGuard address
only; Traefik's 8080 mapping goes.** (hardening, 10/11, 2026-09-23;
review 2026-09-21 M5-4, M5-5) `0.0.0.0:9104` put api-grpc's plain-HTTP
metrics in reach of every VNet member (the edge, hosts). Its only
consumer is the monitoring peer over the tunnel (10.255.255.1:9104,
`ops/prometheus/prometheus.yml`), so the Coolify mapping becomes
`10.255.255.1:9104:9103`; 8443 and 8444 stay on every address (hosts
register over the VNet before they have a tunnel, and both are mTLS).
Docker cannot publish on an address that does not exist yet, so Docker
now starts after `wg-quick@wg0` (a `docker.service.d` drop-in in the
control VM's cloud-init; `Wants`, not `Requires`, so a VM whose tunnel is
not configured still starts Docker). The VM ignores cloud-init changes
(`ignore_changes = [custom_data]`), so the drop-in and the mapping are
owner steps, in order, in `ops/coolify/README.md` "Two owner steps on
the control VM", with their checks; `ops/dev/tunnel-prod.sh` now dials
the WireGuard address. 8080: `coolify-proxy` publishes it with Traefik's
insecure API off, so nothing answers; the fix is one line deleted from
Coolify's proxy configuration (same section). Nothing in the repository
can do either: both live in Coolify's database. *Rejected:* a
`DOCKER-USER` iptables rule dropping 9104 unless it arrives on wg0 (a
second firewall on the VM that nothing in the repository owns);
`net.ipv4.ip_nonlocal_bind` so the bind succeeds before wg0 (the review
already flagged it on hosts, L-10).

**I-175. A certificate refusal gets one re-issue; a second ends the wait
at once; other refusals never spend it.** (hardening, 07, 2026-09-23;
STATUS 2026-09-21 finding "no certificate re-issue on certificate
revoked") I-149 added the forced re-issue, which covers `permission
denied (certificate revoked)`: every command that connects (run, attach,
open, desktop) goes through `connect`. Two gaps remained. Any `Permission
denied` triggered it, and the gateway sends that for refusals a
certificate cannot fix (`environment is not accepting connections yet`,
`gateway cannot reach control plane`, busy, rate limited, stopped, no
such project), so a boot-time refusal could spend the one re-issue before
a real revocation; and after the re-issue, a second certificate refusal
kept retrying for the rest of the 60 s and ended with "SSH did not answer
in 60s". Now `isCertRefusal` reads the gateway's banner: the six
certificate banners (revoked, expired, not yet valid, wrong CA, no
certificate, not valid for this project) or a bare `Permission denied`
with none of the other banners (an older gateway) count; the first gets
the re-issue and an immediate retry, a second returns `The gateway
refused a certificate issued just now (<its words>). \`repose login\` (as
the account that owns the project) and try again; ...`.
`TestRevokedCertificateIsReissuedOnce` (the banners are the gateway's own
constants, so a reworded banner fails it). Needs a CLI release.

**I-176. A gateway session is a relay, not a certificate:
`/internal/sessions` carries `session_id`.** (hardening, 05/06,
2026-09-23; I-123's "recorded, not fixed") `gateway_sessions` was keyed
`(project_id, cert_serial)`: two terminals under one certificate (or
`repose open` beside an attach; I-149's single certificate per laptop
makes that the normal case) were one row, and the first to close deleted
it while the other was open, so `signals.gateway_sessions` read 0 under a
live session. The gateway now gives each relay 16 random bytes (hex) and
sends them as `session_id` with the open and the close; the api keys the
row `(project_id, cert_serial, session_id)` (migration 0004, which adds
the column with default `''` and moves the primary key; its down deletes
the per-relay rows, which cannot fit the old key, the table being a
display count). A report without `session_id`, from a gateway older than
this, is accepted for one release and keeps the old one-row-per-cert
behaviour under `''`; an id that is not up to 64 of `[A-Za-z0-9-]` is
`400 invalid`. Interfaces `api.md`, `ssh-gateway.md` and `db-schema.md`
in this commit; the fake api records the id. The count feeds the
dashboard and `repose status` only; the idle policy reads guestd's own
`ssh_sessions`. Tests: `TestSessionReportsAndCertCache` (three
connections under one certificate report three distinct ids, each opened
and closed once), the api's internal-routes test against Postgres (two
relays of one certificate count two, closing one leaves one, the old
shape still accepted, a bad id refused), `TestMigrateUpDownUp` (0004
down and up). The api rolls on merge (it must be deployed before the
edge, which it is: the old shape stays accepted either way); the gateway
needs an edge switch. *Rejected:* a counter column incremented on open
and decremented on close (keeps the body unchanged, but one duplicated or
lost report skews it for a day, the failure I-123's set semantics were
chosen to absorb).

**I-177. The bootstrap key can be retired per host once the Host CA is
there, and a key file in root's home is never read.** (hardening, 01/14,
2026-09-23; review 2026-09-21 M5-6, M-1's other half) While
`repose.host.bootstrap.enable` is on, the operator's plain key is in
`/etc/ssh/authorized_keys.d/root`, so 14 §9's "operator access only with a
certificate" never holds, and turning bootstrap off before operators
hold certificates would lock the conductor out (it reaches hosts with the
dev-box key through the edge jump). New option
`bootstrap.keyUntilHostCA` (default off): the key moves to
`/run/repose/bootstrap_authorized_keys`, which `repose-host-net` fills
only while `/run/repose/host_ca.pub` is empty and empties when it is not.
A host that knows the Host CA takes certificates only; a host that lost
it (no `host.json`, a CA that never arrived) takes the key again, which
is the break-glass. Off by default and off on host-01, because turning
it on ends plain-key logins the moment the CA is present, and host-01's
`host_ca.pub` is still 0 bytes (I-139's runbook step not yet done), so
the order is the owner's: CA on the host, `ops/dev/operator-cert.sh`
(new: an 8 h certificate signed inside the api container through
`docker exec -i ... repose-admin operator-cert --pubkey -`, which now
reads stdin, written next to `~/.ssh/id_ed25519` so the same `ssh -J`
line offers it), a certificate login seen in the journal, then the
option on and a switch (RUNBOOK "Retiring a host's bootstrap key").
Found on the way: host-01 accepts the key from
`/root/.ssh/authorized_keys` (written by the install on 2026-09-20, the
same key; sshd's log line says `found at /root/.ssh/authorized_keys:1`),
a static key no option controlled, so hosts now set
`authorizedKeysInHomedir = false`. That part is safe to switch now: the
same key stays in `authorized_keys.d` while bootstrap is on and
`keyUntilHostCA` is off. The provider-NIC port-22 rule stays with
`bootstrap.enable` (sshd binds the tunnel address once registered, so
it admits nothing then; turning bootstrap off removes it). VM test
`host-services`: before registration the key file is filled and the key
logs in; after registration brings the CA the file is empty and the key
is refused even with a copy in `/root/.ssh/authorized_keys`; the
certificate login still works. *Rejected:* removing the bootstrap key
from host-01 now (locks the conductor out until the CA and certificates
are in place); a `from="10.255.0.1"` restriction on the key (whoever
holds the key can use the edge, which takes the same key).

**I-195..I-205: laptop parity, settled with the owner on 2026-09-23 before
any code.** (design, owner and conductor, 2026-09-23) The owner's list
after using repose on real projects. The reasoning, the options that lost
and the owner's own words are in `docs/proposals/2026-09-23-dev-ergonomics.md`.
The build is workstream 15 (`docs/workstreams/15-dev-ergonomics.md`). Each
entry below is the decision; the feature and interface docs it names change
in the same commit as the code, as usual, and not before.

**I-195. `run` and `attach` carry the laptop's git config, minus a
denylist.** The effective config for the repository (`git config --global
--list --includes`, run in the repo, so `includeIf` is flattened) is
written to the guest's `~/.config/git/repose-carried`, included from its
`~/.gitconfig` and replaced whole on each carry. Denied: `credential.*`,
`core.sshCommand`, `ssh.*`, `url.*`, signing (`user.signingkey`, `gpg.*`,
`commit.gpgsign`, `tag.gpgsign`), proxies and TLS client settings,
`core.hooksPath`, `init.templateDir`, `safe.directory`, `include*`, diff
and merge tools; also any value that is an absolute or `~/` path missing
in the guest, and a `core.pager`/`core.editor` whose command is not on the
guest's PATH. `core.excludesFile` travels as its contents. Replaces the
"name and email only" row of `guest-conventions.md`. *Rejected:* the
whole file (laptop keychains, 1Password signers and https-to-ssh rewrites
break the guest); signing through the forwarded agent (gone when the
laptop closes, which is when agents commit).
*Amended 2026-10-05 (base-git-gpg, I-525):* every carried value git runs
as a command is checked against the guest's PATH, not only `core.pager`
and `core.editor`: also `sequence.editor`, `interactive.diffFilter`,
`diff.external`, `diff.<driver>.textconv` and `.command`,
`merge.<driver>.driver`, and `pager.<cmd>` when its value is not a git
boolean. A laptop's `interactive.diffFilter = delta --color-only` with no
delta on the machine made every `git add -p` fail ("mismatched output
from interactive.diffFilter"). `filter.<x>.clean`/`.smudge` stay
unchecked on purpose: with `filter.<x>.required` a missing filter fails
the add, where dropping it would commit what git-crypt would have
encrypted.

**I-196. `run` and `attach` carry the laptop's Claude Code config, and
merge `settings.json`.** Carried: `~/.claude/CLAUDE.md`, `settings.json`,
`skills/`, `agents/`, `commands/`, `output-styles/`, `keybindings.json`,
and scripts under `~/.claude/` that settings reference. Never carried:
`.credentials.json`, `projects/`, `history.jsonl`, and every other state
directory; `~/.claude.json` stays excluded. `settings.json` is merged in
the guest with `jq`: the guest file is the base, the laptop file goes on
top, `permissions.allow|deny|ask` are unioned, entries whose command
contains `repose-hook` are stripped from both sides and the platform's
re-added, laptop home paths are rewritten to `/home/dev/`, and hooks or a
`statusLine` whose command is missing are dropped and named once. It's
written through a temp file checked with `jq empty`, and the previous file
is kept as `settings.json.repose-prev`. An invalid laptop file is skipped
with one warning. Marketplace plugins named in `enabledPlugins` and missing
in the guest are installed in the background. Credentials are untouched:
I-196 carries config, and the Claude login question is open (proposal item
9). *Rejected:* moving platform hooks to managed settings (the docs
contradict themselves on the path and on whether managed hooks combine
with user hooks); a laptop-wins overwrite (guest "don't ask again" and
`/model` write to the same file).

**I-197. Gitignored `.env` files travel over SSH at `run`.** This reverses
the rule in `features/sync-at-launch.md` that ignored files never travel.
`.env` and `.env.*` files at any depth outside dependency directories, up
to 1 MB each, go laptop to guest over the command's SSH connection, mode
0600, with no API involved. As with tool logins, the newer side wins by
mtime, and the CLI says which side won when it skips. It's the "copied
over SSH" home generalised, so there are still three homes. They sit on
the guest disk and so are in snapshots, like the gh token and the code.
Named secrets remain for values that must change without a laptop.
*Rejected:* tmpfs with a symlink (lost at the 03:00 base-bump reboot, so
the agent breaks overnight); importing into named secrets and rewriting
the file (two sources of truth, and a guest-side edit lost at the next
start).

**I-198. The guest's timezone follows the laptop on every `run` and
`attach`,** not only at create (I-104). The CLI sends `tz` each time.
When it differs, `/etc/repose/env` is updated and `tmux set-environment -g
TZ` is run so new windows and agents get it.

**I-199. Ports are auto-forwarded while a CLI session is attached.**
Every port the guest starts listening on is forwarded to the same port on
the laptop's localhost with `ssh -O forward` on the command's
ControlMaster, and cancelled when it closes. It's detected within a second.
A port taken on the laptop gets the next free one, and the message says so.
Output goes inside tmux: a `display-message` on each new forward and the
live list in the status bar. Portless's proxy port (1355) is never silently
remapped; on a collision the message names the port used instead.
`repose open` stays. *Rejected:* preview URLs as the dev path (not
localhost: OAuth redirect URIs, cookie domains, secure context), Traefik
(the Go gateway is already the future preview proxy). Preview URLs stay
designed for later.

**I-200. Agents outlive dev servers under memory pressure; nothing is
killed on a timer.** Agent windows and the tmux server run with a strongly
negative `OOMScoreAdjust`, so the kernel's OOM killer picks a stale dev
server before an agent. `repose status` lists listening processes with
their age and memory, and guestd's `oom` warning names what was killed.
*Rejected:* idle reaping (it eventually kills the one server someone
needed).
*Made during implementation* (base-hostd-guestd, 2026-10-06): "agent
window" covers every pane of the session; guestd finds agents with
`tmux list-panes -s`, since `list-windows` reports only each window's
active pane and left claude in a split pane at 0.

**I-201. `repose cp`.** `repose cp <project>:<path> <local>` and the
reverse, a thin wrapper over scp with the project resolved as other
commands resolve it (`:<path>` is the current project), and guest-relative
paths taken from `~/<slug>`.

**I-202. Each host runs a pull-through cache for the npm registry and for
Docker Hub.** Guests reach them over WireGuard and use them by default
(`npm_config_registry`, the Docker daemon's `registry-mirrors`). Private
registries and authenticated requests bypass them. *Rejected:* a pnpm store
shared between guests (writable means cross-tenant package poisoning;
read-only means guests cannot add packages, pnpm cannot hard-link across
virtiofs, and a symlinked store puts module resolution on virtiofs).

**I-203. The first sync of a large GitHub repository clones in the guest.**
When the guest has no commits, the remote is on github.com, the repo is
public or gh's login travelled, and `git count-objects -v` reports a
`size-pack` over 20 MB (to be set from measurement), the guest runs a full
`git clone` from GitHub. The laptop then bundles only what GitHub lacks,
and the diff and untracked files follow as today. Later syncs are
unchanged. *Rejected:* `--filter=blob:none` (lazy blob fetches fail later
once the token expires or the repo goes private); always cloning (a
GitHub round trip that small repos don't need).

**I-204. Nothing on GitHub may name the platform.** It's the user's
machine, so every GitHub action taken from it is the user's. The GitHub
App connector is dropped: installation tokens act as `repose[bot]`, and
user tokens list "Repose" among the user's authorised apps. GitHub access
stays gh's own login (copied from the laptop, or `gh auth login` in the
guest). *Revisit when:* a way to get per-repo scope without platform
branding is found.

**I-205. The trial is one day of compute.** This amends R4-8's $10. The
credit is one day on large, $3.36, which is 48 hours on small. It stays a
dollar credit so there is one meter, and every user-facing string calls
it "your first day of compute", never an amount. The card is still
required first; the new-account limits are unchanged.

**I-206. The carry's hashes live in the guest; `attach` carries through a
session helper; tmux stops taking `TZ` from the attaching client.**
(15-dev-ergonomics, 2026-09-23) Settled while building I-198, the first
part of workstream 15, because every later carry part rests on them.

- *Markers, not `projects.json`.* `15-dev-ergonomics.md` §4 put the
  per-project carry hashes in the laptop's `projects.json`. They live in
  the guest instead, one file per carried item under
  `~/.repose/carry/`, holding the hash of the laptop input last applied,
  and come back as `#marker` lines in the sync's first ssh (no round trip
  added to `run`). A hash on one laptop is wrong after a restore, after a
  run from a second laptop, and after the item is edited inside the
  guest; the guest's own record is right in all three. `cli-config.md`
  therefore gains nothing for it.
- *The session helper.* `attach` is `exec ssh`, so nothing of the CLI
  survives into the session, yet the carry must run beside the attach
  and the auto-forward (I-199) must run for as long as it lasts. `run` and
  `attach` start `repose __session` just before the exec: detached, in its
  own process group, stdio on `/dev/null`, its options base64 JSON in
  `REPOSE_SESSION` so no path shows in `ps`. It speaks to the guest only
  (never the api), over the same multiplexed target, reports only through
  `tmux display-message` (it waits up to 8 s for a client to exist), and
  ends when its parent pid changes, which is when the ssh the CLI became
  exits. Not on Windows (no multiplexing, no exec). *Rejected:* keeping
  the CLI as ssh's parent instead of exec'ing (the checklist item "ps
  shows no repose parent during a session", and signal and terminal
  handling that plain ssh already gets right).
- *Each carry part runs as its own `sh -e`* inside the one payload, so a
  part stops at its own first error, reports `#failed <label>`, and never
  stops the credentials or another part.
- *`TZ` leaves tmux's `update-environment`.* With it there, an attach
  from a terminal that has no `TZ` (the usual laptop) marks the session's
  `TZ` removed, which shadows the global one, and an agent started in a
  new window (`tmux new-window '<agent>'`, not a login shell) runs in UTC:
  the owner's "claude reads my time in us east or utc". The base drops it
  and `repose-tmux-session` sets the global `TZ` from `/etc/repose/env`;
  the CLI sets the global and every session's `TZ` on each carry, and its
  attach sends `TZ` (`SendEnv`) for bases that still list it.
- *The stored zone moves too.* `PATCH /projects/:id` accepts `tz`
  (validated as an IANA name), sent beside the connect only when the
  laptop's zone differs from the project's, so the next start's
  SetupProject writes the zone the running guest already has. The CLI
  waits at most 2 s for it before the attach. Interfaces: `api.md`,
  `guest-conventions.md` in this commit.

**I-207. `repose status` reads the listening processes from the guest
over SSH; the OOM priority is -800, set by guestd on the agent process
only, and resets only negative values.** (15-dev-ergonomics, 2026-09-23)
Settled while building I-200.

- *Where `repose status`'s process list comes from.* A version of this
  workstream carried the list in guestd's sample (`GuestSignals`, stored
  in `meter_samples`). The repository's policy test stopped it: the
  privacy policy says, verbatim, that the platform records process
  names, CPU time, memory use and network bytes, and listening ports and
  process ages recorded every minute are more than that. Changing the
  policy is the owner's call, not a workstream's, and the list is only
  needed at the moment someone asks. So `status PROJECT` of a running
  guest runs `ss -Hltnp` and `ps` in the guest over the user's own SSH
  and prints the forwardable listeners with name, age and memory; the
  platform never sees them. It departs from `status-and-logs.md`'s "status
  never makes an SSH connection" in one bounded way, recorded there: the
  read rides an open ControlMaster when there is one, never leaves one
  open (`ControlMaster=no`, so no gateway session lingers), has 4 seconds,
  and is left out when it fails, so status still works when the guest
  cannot be reached. *Revisit when:* the owner wants the list on the
  dashboard, which needs it recorded, which needs the policy text first.
- *The value.* -800, not -1000: an agent that itself runs away can still
  be killed rather than hang the guest.
- *Which process is the agent.* Name matching alone protects too much:
  Gemini CLI runs as `node` (I-122), and so does every vite. The
  protected process is the shallowest one in each agent window's tree
  whose name or executable is the agent's binary; its descendants are its
  work and go back to 0.
- *Only negative values are reset to 0.* A value above 0 is one the user
  chose (`choom`), since nothing else in the guest raises it.
Interfaces: `vsock-guestd.md` (the refresh sets `oom_score_adj`),
`guest-conventions.md` "Memory pressure"; no proto, api or schema change.

**I-208. The caches live at one fixed address on every host; npm is
pointed at them through `~/.npmrc`, not `npm_config_registry`; npm's
fallback is an nginx front.** (15-dev-ergonomics, 2026-09-23) Settled
while building I-202; each point is a place where the workstream doc
left the mechanism open or named one that does not work.

- *Where guests reach the caches.* The guest base is host-independent
  (I-34), so it cannot name a host's own bridge address, and I-202's
  "over WireGuard" has no route from a guest. Every host puts
  `10.63.255.254/32` (the host services address, outside
  `10.64.0.0/12`) on `br-guests`; a guest reaches it through its default
  gateway, and `guest_in` admits exactly ports 4873 and 5000 there. The
  address is a base constant as well as a host option.
- *npm has no fallback of its own* (checked: npm 11.19 and pnpm 11.25
  have no second registry). So the host provides it at the HTTP layer:
  nginx on the address is a stateless front that proxies to the cache
  (a second nginx, `repose-npm-cache`, with `proxy_cache` capped at
  40 GB, LRU) and, when the cache refuses or fails, proxies to the
  registry itself and logs `repose_npm_fallback:`. `host-caches` shows it
  with the cache stopped. Rejected: verdaccio (storage without a size cap
  or LRU, and a second copy of every package document).
- *Tarballs through the cache.* Package documents name
  `registry.npmjs.org` tarball URLs; npm rewrites those to its
  configured registry, pnpm and yarn do not. The cache rewrites them in
  the documents it serves (`sub_filter`), after caching, so the store
  holds the registry's bytes.
- *`~/.npmrc`, not `npm_config_registry`.* npm lets the environment
  variable beat a project's own `.npmrc` (a company registry would stop
  working), and pnpm 11 reads registries from `.npmrc` files only (it
  ignores `NPM_CONFIG_GLOBALCONFIG` too). A `dev` user unit appends one
  `registry=` line to `~/.npmrc` at boot, once, only when the front
  answers, and never when the file already names a registry or holds a
  `registry.npmjs.org` token: that is I-202's "authenticated requests go
  direct". Project `.npmrc` files still win for npm and pnpm.
- *Docker.* The distribution registry in proxy mode, which has a TTL, not
  an LRU: blobs expire after 168 h, and the bound is the thin volume both
  caches share (`vg-guests/repose-cache`, 64 GB), so a cache can never
  fill the OS disk that holds `/nix/store`. It exits when Docker Hub is
  unreachable at start, so it restarts every 10 s without a start limit.
- *Rollout order.* A guest on a host without the caches would send its
  first Docker pull to an address that routes nowhere (a connect timeout
  before dockerd falls back) and would not add the npm line. So: host
  switch first, base publish after; and for removal, a base without the
  settings first, then the host switch.
Interfaces: `host-conventions.md` "Caches", `guest-conventions.md`
"Caches", RUNBOOK entry, in this commit.

**I-209. guestd's paths are absolute on a real guest.** (15-dev-ergonomics,
2026-09-23; found by the guestd VM test) `sysdep.Paths` joined its paths
under `Root`, which is empty on a real guest, and `filepath.Join("",
"run", ...)` is the relative `run/repose/switch.log`. Every other use
worked by accident (guestd's working directory is `/`), but I-148 passes
the switch log to `systemd-run -p StandardOutput=append:<path>`, which
refuses a relative path, so every `Switch` answered `internal:
switch-to-configuration switch exited 1` ("Path run/repose/switch.log is
not absolute"). An empty `Root` now means `/`. It reaches guests with the
next base publish; until then a config apply or a base bump on a running
guest fails at the switch. `TestPathsAreAbsoluteOnARealGuest`, and the
guestd VM test's "Switch applies a new generation" subtest.

**I-210. A guest tree that is exactly what the last sync left is not
dirty.** (ws15-fixes, 2026-09-23; reported by the 15-dev-ergonomics
session) The sync applies the laptop's diff with `git apply --index` and
extracts its untracked files into the checkout (I-150), so after any run
that carried a modified or untracked file the guest's tree is dirty by
construction, and the next `repose run` refused with exit 6 as if an
agent were working. Now the end of the apply ssh stores, when the tree
it leaves is dirty, a fingerprint of it in the checkout's
`.git/repose-synced`: `HEAD` and the tree `git add -A` would record
(index, working tree and every non-ignored untracked file), written
through a copy of the index so the real one is untouched and only
changed files are hashed. The next probe computes the same fingerprint
when `git status --porcelain` is non-empty; equal means the dirt is the
laptop's own, so the run goes on and the apply sets it aside with `git
stash push -u -m "repose run: last sync"` before laying down the new
diff, and the summary line says so (the laptop still has those changes,
or newer ones; a stash rather than a reset, so a write that lands after
the last check, or anything misjudged, is recoverable, with one gap
that is git's own: `git stash push -u` records the untracked files and
then cleans them, so a file written between those two steps is lost;
the same apply drops all but the newest 10 stashes whose
message is exactly `repose run: last sync`, never the user's or
`--stash-remote`'s). Anything else, an edited
synced file, a new file, a commit, is an agent's work and still refuses.
The apply computes the fingerprint again before it stashes, and refuses
with exit 6 if an agent wrote between the two ssh round trips. No ssh
is added: both checks ride the existing probe and apply. The fingerprint is "failed" (never matches) when `git status
--porcelain=v2` shows any submodule change, because the superproject's
tree records a submodule only as its commit and an edit inside one would
not change it; it is built with a throwaway object directory, so a probe
writes no objects. Every status, stash and reset in the sync runs with
`-c status.showUntrackedFiles=normal -c submodule.recurse=false`
(`--ignore-submodules=none` for status): the carry keeps both keys,
since they are the user's preferences for the agent's own git, and the
sync overrides them for itself instead. A run from a second laptop
stashes the first laptop's synced changes the same way. A tree that was
clean at the probe is checked again before the fetch and the tar (exit 6
if an agent wrote since), so an untracked file at a path the laptop also
sends is never overwritten. The index copy is `cp -p`, keeping git's
racy-clean check; `filter.lfs.required=false` rides every sync git call,
so a repository with LFS attributes in a guest without git-lfs still
fingerprints instead of exiting 6 on every run. The stash prune records
each stash's commit when it lists them and drops one only while its
index still names that commit; a stash pushed meanwhile stops the prune
for that run. *Rejected:* a
list of the paths the sync wrote (an agent's edit to one of them would
look like the sync's); `git stash create` (it leaves untracked files
out); keeping the fingerprint on the laptop (wrong after a run from a
second laptop, as I-206 found for the carry markers).
*Amended 2026-10-05 (base-git-gpg, I-525):* the guest has git-lfs now,
so the sync's shell exports `GIT_LFS_SKIP_SMUDGE=1` before any git call:
a sync's checkout leaves LFS files as pointer files and never downloads
objects or waits on LFS credentials. `filter.lfs.required=false` stays,
for guests on an older base and for a project that removes git-lfs.

**I-211. The carry leaves every secret on the laptop, by key as well as
by file.** (ws15-fixes, 2026-09-23; the conductor's review of workstream
15) I-195 and I-196 named files and sections, and two kinds of secret
went through the gaps, against "Claude Code credentials are never
copied anywhere" and the three homes of `features/secrets.md`.

- *Claude `settings.json`.* It was carried whole, so `env` (where
  `ANTHROPIC_API_KEY` and MCP tokens usually sit), `apiKeyHelper`,
  `awsAuthRefresh`, `awsCredentialExport` and `otelHeadersHelper` landed
  on the guest disk and in its snapshots, and an `apiKeyHelper` rewritten
  to a `/home/dev` path turned the guest to API-key auth. The laptop now
  removes `env`, `apiKeyHelper`, `otelHeadersHelper`, `forceLoginMethod`,
  `forceLoginOrgUUID` and every key starting `aws` or `gcp` before the
  file is hashed or sent. Removed on the laptop, not in the guest's
  merge: what never leaves cannot be in a snapshot.
- *git config.* The denylist gains every key that holds a secret:
  `http.extraHeader` (also `http.<url>.extraHeader`, where PATs are
  usually put), `http.cookieFile`, `sendemail.smtpPass`, `github.token`,
  `hub.oauthtoken`, `core.gitProxy`, `*.proxy` in any section, and any key
  whose last part ends in `token`, `pass`, `password`, `passwd` or
  `secret`. A denylist, not an allowlist: the useful keys (aliases, diff
  and merge options, per-tool settings) are open-ended, while secrets
  are named by those words. *Revisit when:* a secret-bearing key turns up
  that none of the words matches.
- *Credentials in values, and secret-looking files.* (Second review.) A
  git config entry whose key or value, or a `settings.json` entry whose
  strings, hold a credential by its shape (a URL with `user:password@`,
  or a token prefix: `sk-ant-`, `ghp_`/`gho_`/`ghu_`/`ghs_`/`ghr_`,
  `github_pat_`, `glpat-`, `xox?-`, `AKIA…`, `Bearer` followed by 20 or
  more token characters, so `Bearer $TOKEN` and a grep for the word are
  kept) is left out on
  the laptop and counted in one line that never shows the value (git names only the
  sections, since a subsection can hold the credential; skipped Claude
  files are named, once per change, since names are not values): a hook
  or the `statusLine` whose command carries one, a permission rule, a
  marketplace whose URL does (and the plugins enabled from it). Files
  named `.env*`, SSH identities (`id_rsa`, `id_ed25519`, … and their
  `.pub`, not every `id_` file), `*credentials*`, `*.pem`, `*.key`, `*.p12`,
  `*.pfx` are never carried from `skills/`, `agents/`, `commands/`,
  `output-styles/` or as a hook script. Pattern matching can miss a
  secret of a shape it does not know; the key denylist above stays the
  first line.
- *The rest of the review, where it touched a documented behaviour.*
  The settings rewrite replaces the laptop home followed by `/` anywhere
  in a string, and maps a `CLAUDE_CONFIG_DIR` directory to the guest's
  `~/.claude`. A `.env` the last carry wrote and the guest no longer has
  is sent again (the apply lists the paths in `~/.repose/env-paths`; the
  probe checks them). The forward's port check binds `127.0.0.1`, `::1`,
  `0.0.0.0` and `::`. A hybrid first sync bundles against the laptop's
  `origin/*` and tags that the fresh clone has. The Claude files' hashes
  are cached on the laptop by size and mtime (`carry-hashes.json`,
  `cli-config.md`), and `run` reads its side of the carry while the probe
  is in flight.

**I-212. On a session, the gateway relays the exit status before the
EOF, and answers the guest's channel keepalive itself.** (ws/15 live
fixes, 2026-09-23; a first sync of golang/go failed, and `ssh
<p>.repose 'sleep 12; echo ok'` over the ControlMaster returned 255
about twice in eight runs.) sshd ends a command with its output, EOF
when the pipe drains, then `exit-status` when it reaps the child. The
gateway relayed the EOF from the data goroutine the moment the guest's
output ended and the exit status from the request goroutine, so the two
raced. An OpenSSH client whose stdin is already closed (every
ControlMaster session run with stdin from a pipe or /dev/null, which is
how the CLI runs its commands) answers EOF with CHANNEL_CLOSE at once;
an exit status arriving after that is dropped and ssh exits 255 (the
master's -vvv log: EOF, then its own close, no exit-status). Now a
session's EOF towards the client waits until an `exit-status` or
`exit-signal` has been relayed, or the guest's request stream has
ended; exit status before EOF is valid SSH and is what sshd itself sends
when it reaps the child first. Separately, sshd's ClientAliveInterval
check (30 s on guests) arrives as a channel `keepalive@openssh.com`
wanting a reply; the gateway relayed it to the laptop, which held every
later request of the channel, the exit status among them, on a laptop
round trip, and the check is about the gateway, sshd's client, anyway.
The gateway now answers it. `TestRelayExitStatusOverOpenSSHControlMaster`
reproduces the 255 with the real OpenSSH client over a ControlMaster
(exit 255 before, 7 in eight runs after);
`TestRelayDeliversExitStatusBeforeEOF` pins the order with a client that
closes on EOF. *Not explained:* why the live failures clustered around
the gateway's own 30 s keepalive; the race does not need it, and the fix
does not depend on it.

**I-213. An agent's process is found by its nix wrapper name too, and
the OOM warning names what the kernel killed.** (ws/15 live fixes,
2026-09-23) nixpkgs' makeWrapper moves a program to `.X-wrapped`, so
the guest's claude runs as `.claude-wrapped`, in comm and in the exe
link alike, and I-207's protection matched `claude` only: it never
reached claude. Every binary in the agent table now also matches
`.X-wrapped`, and comm's 15-byte cut of it (`.opencode-wrapp`); the
same rule serves the idle heuristic's pane check. The `oom` warning
fired on the kernel's first line, "<comm> invoked oom-killer", which
names the process that asked for memory and not the victim, so the
warning read "reported an out-of-memory condition" and the rate limit
swallowed the "Killed process" line after it. That first line is now
skipped, and the name is unwrapped (`the kernel killed claude`).
`TestOOMPriorityFindsNixWrappedAgents`,
`TestOOMWarningNamesTheKilledProcess`.

**I-214. The npm cache ignores the registry's cookie.** (ws/15 live
fixes, 2026-09-23) Live, the cache held 4 KB after 495 misses and no
hit: registry.npmjs.org answers through Cloudflare, which sets its
`__cf_bm` bot-management cookie on every response, and nginx stores no
response that sets a cookie. The cache server now has
`proxy_ignore_headers Set-Cookie` and `proxy_hide_header Set-Cookie`:
the cookie means nothing to npm, and a guest never receives it.
Requests carrying credentials stay uncached exactly as before
(`proxy_cache_bypass`/`proxy_no_cache` on Authorization). The Docker Hub
mirror (distribution v3) also stops exporting OpenTelemetry traces to
localhost:4318, where nothing listens (`OTEL_TRACES_EXPORTER=none`,
`OTEL_SDK_DISABLED=true`). host-caches: the fake registry sets a cookie
on every answer, and the test asserts the second tarball and document
fetches are hits and that no cookie reaches the client (it failed before
this change at the Set-Cookie assertion, with the second fetch a MISS).

**I-215. Live polish of workstream 15: system listeners are not
forwarded, the status clock follows the carried zone, and a guest's
newer `.env` is named once.** (ws/15 live fixes, 2026-09-23)

- *Listeners.* systemd-resolved's LLMNR responder listened on
  0.0.0.0:5355 in every guest, so every status bar showed `⇄ 5355`. The
  guest base turns LLMNR off (`services.resolved.llmnr = "false"`: a
  guest has one routed interface and nobody on its link to ask), and
  auto-forward and `repose status` skip 5355 and 5353 (mDNS) as they
  skip the desktop's ports, for bases published before this. The rule
  is by port, not by owner: ports under 1024 were never forwarded, and
  a container the user publishes is served by root's docker-proxy, so
  "root-owned means system" would have stopped forwarding those.
- *The tmux status clock.* The server's strftime uses the zone the
  server started in, and the carry cannot move a running server's zone
  (`set-environment -g TZ` changes new windows only). The base's
  status-right is tmux's default with the time from `#(date ...)`, a
  job that runs with the global environment, so it follows the carried
  `TZ`; it costs one fork per status-interval.
- *A `.env` edited in the guest.* 15-dev-ergonomics §6 says a guest
  copy newer than the laptop's is kept with one line. When the laptop's
  set had not changed the part was not sent at all and nothing was
  said. The apply now records each carried file with its mtime in
  `~/.repose/env-paths` ("<mtime> <path>"; the previous paths-only shape
  is still read), and the probe reports a file newer than recorded,
  then records the new mtime: named once per change, never on every run.
- *Wording.* The credential notes agree in number ("1 git config entry
  that holds").
- *Addendum (base-docker-system, 2026-10-05).* resolved's mDNS responder
  still listened on 0.0.0.0:5353 and [::]:5353 and answered on docker0
  and every container's veth. The base now sets
  `services.resolved.settings.Resolve` with `LLMNR = "false"` and
  `MulticastDNS = "false"` (`services.resolved.llmnr` worked only through
  a rename shim), and the VM subtest asserts no `:5353` or `:5355` in
  `ss -Hlun`. The CLI keeps skipping both ports for older bases.

**I-216. The dashboard is developed against the live api and Logto, not
the fake.** 08-dashboard.md §7 builds cmd/fakeapi and cmd/fake-logto for
the Playwright suite. The design preview (`ops/dev/web-preview.sh`) reused
the fake api for local work, behind a dev-only login shortcut
(`PUBLIC_PREVIEW_TOKEN`) and seeded projects. That showed invented data,
and every new endpoint had to be added to the fake before a page could use
it. `just web` now runs Vite with the production `PUBLIC_*` values
(`apps/web/.env.example`, now with the repose-web app id, which is public)
behind `tailscale serve`, at https://azurewoker-01.tail05d19d.ts.net/. It
has to be HTTPS: Logto's PKCE uses `crypto.subtle`, which browsers only
expose in a secure context, and a plain `http://100.x` address is not one
(tried first; the sign-in button did nothing). The api already answers any
origin (CORS `*`). The repose-web Logto application lists that origin's
`/callback` and `/` as redirect and post-sign-out URIs.
The preview script, the shortcut and the Vite `/fakeapi` proxy are
removed. The fakes stay for the Playwright suite only. The cost: a dev
server's buttons act on the signed-in account, so anything destructive is
tried on an `e2e-` project.

**I-226. Request log lines name the route, the user and the client.**
(conductor, 2026-09-23) The owner's nuru-playground was destroyed at
19:15:44Z and the api logs could not say by whom: every `request` line
had `route: "unmatched"` and no `user_id`, because the mux sets `Pattern`
on the copy of the request it is handed and `authed` puts the user in a
context the wrapper never sees (the per-route Prometheus series were all
"unmatched" for the same reason). `wrap` now keeps the request it passes
to the mux and a small `reqInfo` that `authed` fills. Each line also gets
`client`: `cli` (the CLI sends `User-Agent: repose-cli`), `dashboard` (a
browser) or `other`. The User-Agent itself is still never logged
(OBSERVABILITY.md redacts `user_agent`).
**I-220. The menu takes any nixpkgs package by attribute path, and `repose
config add/remove` edit it.** (16-guest-tooling, the owner's nuru-playground
session) The owner needed `gcc` (for `go install` of air with cgo) and
the menu offered only its 26 catalog entries; `features/config.md` already
showed `repose config add bun postgresql`, which the CLI never had.
- *Shape.* A `MenuSelection` item is `{id, options?}` as before or
  `{package: "<attribute path>"}`, a new field rather than a reserved id
  with an `attr` option, so several packages do not collide on one id and
  the dashboard can tell the two kinds apart without the catalog. The
  change is additive: every selection the api accepted before is still
  valid and renders byte for byte as before (a selection without packages
  gets no helper; `TestExampleFragmentIsCurrent` is unchanged).
- *A catalog id wins.* The CLI turns a name the catalog has into `{id}`;
  the api refuses a `{package}` equal to a catalog id, so `redis` always
  means the service, never `pkgs.redis`.
- *No Nix injection.* A package matches
  `^[A-Za-z_][A-Za-z0-9_+-]*(\.[A-Za-z_][A-Za-z0-9_+-]*)*$`, at most 200
  characters, checked by the api, again by the renderer, and by the CLI
  before any request. It is rendered as a list of string literals,
  `(nixpkg [ "python312Packages" "black" ])`, which the pattern cannot
  escape (no `"`, `\`, `$`, whitespace), and the fragment's `nixpkg`
  helper resolves it with `lib.attrByPath`. So the name is data, never
  Nix syntax.
- *A missing package names itself.* `pkgs.foo` on a missing attribute
  would fail with Nix's `attribute 'foo' missing` at a generated line the
  user never wrote; the helper throws `nixpkgs has no package "foo";
  search https://search.nixos.org/packages` (and `... is not a package`
  for an attribute set like `python312Packages`), which hostd's existing
  mapping already makes the `eval_failed` summary, so hostd is unchanged.
  An unfree package outside the allowlist fails with nixpkgs' own
  "Refusing to evaluate package ... unfree license", as a hand-written
  fragment does. Proven by `TestRealNixMissingPackage` through the exact
  restricted eval hostd runs.
- *Empty is allowed.* `config remove` of the last item PUTs `[]`, which
  renders an empty generated fragment and keeps the project in menu mode;
  the api used to refuse an empty selection, for no reason the docs gave.
- *The fake api and the dashboard used another shape.* cmd/fakeapi
  accepted `{packages, services, options}` and the dashboard's menu tab
  sent it, so the tab could not apply against the real api (400) and read
  every real selection as empty. The fake now validates and renders with
  `internal/menu` itself (real catalog, real fragment, the custom-fragment
  409), and the tab reads and writes the documented array, listing
  packages as "Extra packages" with a remove button.
**I-217. A guest's 200 Mbit/s shape limits what it sends, on its tap's
ingress, and never traffic to the host; the npm front gzips package
documents.** Found in the owner's nuru-playground session: `pnpm install`
of 763 packages warned "Request took 13-15 s" against the host's npm cache,
which answers in milliseconds. Two causes.
- *The shape was on the wrong side.* hostd put an HTB root with one 200
  Mbit/s class on each tap. A tap's egress is what the host sends to the
  guest, so the class capped every download into the guest, the host-local
  caches included (25 MB/s), and left what the guest sends to the
  internet, which DESIGN §4 ("per-guest egress shaping") and §7 ("guest
  ──NAT──▶ internet (shaped)") mean to limit, unlimited. The guest's
  egress is the tap's ingress, where the only tc tool is a policer: an
  ingress qdisc and, in order, `flower dst_ip 10.64.0.0/12 action pass`,
  `flower dst_ip 10.63.255.254/32 action pass`, `flower action police
  rate 200mbit burst <500 ms> mtu 64kb conform-exceed drop/ok`
  (host-conventions.md "Network"; a flower with no match rather than
  `matchall`, which refuses `tc filter replace` on an existing filter
  with EEXIST, seen in host-network). The first two keep everything a guest
  sends to the host itself (gateway, DNS, the caches) out of the limit;
  other guests are dropped by nftables anyway, and the edge's WireGuard
  address is 10.255/16, so it stays limited with the internet. `mtu 64kb`
  because a vnet_hdr tap delivers GSO packets up to 64 KB. Nothing limits
  host-to-guest traffic: DESIGN asks for none, and a guest's download
  volume is bounded by what it asks for.
- *Considered.* An IFB device per guest with the ingress redirected to an
  HTB on it queues instead of dropping, which TCP likes a little better,
  but doubles the devices hostd creates, deletes and reconciles per guest.
  An HTB on the provider NIC with a class per guest by nftables mark
  exempts host-local traffic by construction, but it is one shared qdisc
  hostd would edit for every guest, and it shapes the host's own traffic
  too. The policer lives and dies with the tap. Its burst is 500 ms of
  the rate (12.5 MB at 200 Mbit/s): in host-network, a single TCP flow
  through a 20 Mbit/s policer ran at 10 Mbit/s with a 100 ms burst
  (sawtooth on drops) and at 21 Mbit/s with 500 ms.
- *Idempotent, and it reaches running guests.* The ingress qdisc is added
  only when `tc qdisc show` lacks it and each filter is `tc filter
  replace` with a fixed prio and handle, so a re-run or a new rate swaps
  it in place. hostd's reconcile now re-applies the shape to every running
  guest at start, so a hostd upgrade moves live guests. A tap still
  carrying the old root HTB (accepted this one release) has it deleted
  after the policer is in place; the few packets queued in it are dropped
  and TCP resends them, no connection is lost. Unshape removes both.
- *Compression.* The cache fetches package documents uncompressed to
  rewrite their tarball URLs (`sub_filter`), and nothing compressed them
  again: `next`'s document reached the guest as 25.5 MB where npmjs sends
  2.2 MB gzipped. The front (`repose-npm` vhost) now gzips
  `application/json` and `application/vnd.npm.install-v1+json` for a
  client that asks, at level 1: the link is the host's bridge, a large
  document is compressed on every request (proxy_cache stores the
  upstream's plain body; the rewrite and the gzip run per response), and
  JSON gets most of its ratio at the lowest level. Tarballs are
  `application/octet-stream`, already gzip, and not listed; the fallback's
  answers come from the registry already encoded and nginx does not
  compress an encoded body. The Docker Hub mirror on :5000 has no
  counterpart problem: nothing rewrites its bodies, layers are compressed
  blobs and manifests are small; it was slow only through the tap shape.
**I-218. The guest base has a C toolchain, the everyday CLIs, nix-ld,
and `nixpkgs` pinned to its own nixpkgs.** (guest tooling, 2026-09-23)
The owner's first real project (a pnpm/turbo Next.js and Go-wasm
monorepo) hit `cgo: C compiler "gcc" not found` on `go install air`,
and the same gap breaks rustup's linking, node-gyp and Python C
extensions; prebuilt binaries that npm or an install script downloads
(prisma engines, biome, Playwright's own browsers) failed because the
guest has no `/lib64/ld-linux-x86-64.so.2`.
- *Toolchain* (`nix/guest/base/tool-list.nix`): `gcc` (the wrapper: cc,
  gcc, g++, c++ against the guest's glibc), binutils, gnumake,
  pkg-config, cmake. gcc is most of the cost (+319 MB); cmake +74 MB is
  kept because CMake builds are the common native-addon path (and
  `nix shell` would re-fetch it on every use).
- *CLIs*, each cheap and each something a developer reaches for on any
  Linux box that the base lacked on PATH: file, lsof, zip, dnsutils (dig,
  nslookup), sqlite (sqlite3), psql with pg_dump/pg_dumpall/pg_restore/
  pg_isready only (a symlink set from postgresql, so no server binary is
  on PATH; a database is a menu service), openssl, gnupg (git commit
  signing), psmisc (killall). Already present and not added: unzip, nc,
  patch, less, man, strace, rsync, which, host, python3 with venv.
  Considered and left to `nix shell`: gdb, valgrind, autotools.
- *nix-ld* (`nix/guest/base/devtools.nix`): enabled with the module's
  default set plus icu, expat, libffi, ncurses, readline, krb5, and the
  libraries Chromium's headless shell loads (glib, nss, nspr, dbus, atk,
  at-spi2, cups, libdrm, libgbm, libglvnd, libxkbcommon, pango, cairo,
  alsa-lib, freetype, fontconfig, gtk3, the X11 client libs). Nearly all
  are in the closure already through chromium.
- *Registry*: `nixpkgs.flake.source` is the flake's nixpkgs (set by
  lib.nixosSystem for the runner and by `nixosModules.guestBase` for the
  VM tests; the base asserts it), so `nixpkgs` in the system registry and
  NIX_PATH is the base's nixpkgs source, already in the closure
  (205 MB). `nix profile add nixpkgs#X` evaluates offline and substitutes
  X at the base's revision. `nix.settings.flake-registry = ""`: nix
  otherwise downloads channels.nixos.org's global registry before
  resolving `nixpkgs#` even with the system entry, and without network
  that was a hang of over a minute ending in an error (seen in the VM test). The cost:
  other indirect names (`templates#`, `home-manager#`) need a `github:`
  URL in the guest.
- *Closure budget*: the 6 GiB check (02 §7) had 400 MB of room, less
  than the additions. The desktop's noVNC was the cheapest cut:
  `pkgs.novnc` in systemPackages brought a second Python (3.14) through
  its novnc_proxy wrapper, and websockify's optional numpy brought
  numpy twice plus openblas, blas and lapack. The base now serves only
  noVNC's static files (copied out of the package) and runs websockify
  without numpy, which only speeds up unmasking what the browser sends
  (keys, pointer). `guest-desktop` still passes. Python 3.14 stays in the
  closure through git. Net: 6,017,269,544 to 6,165,108,296 bytes
  (+148 MB, 5.6 to 5.7 GiB).

**I-219. An unknown command in the guest names the nixpkgs package that
has it.** (guest tooling, 2026-09-23) The owner typed `air` and `tsc`
and got bash's bare "command not found"; NixOS's own command-not-found
reads a channel's programs.sqlite, which a flake-built system does not
have, so the base had it disabled. The base now takes nix-index-database
(github:nix-community/nix-index-database, pinned in `nix/flake.lock`,
nixpkgs following ours) and installs `nix-index-with-small-db`: the
/bin-only index as a fixed-output file in the closure, so a lookup never
touches the network (0.07 s in the VM). The index is built weekly from
nixpkgs-unstable, not from the base's revision; an attribute that exists
in one and not the other is rare and fails visibly at install.
`repose-command-not-found` is bash's `command_not_found_handle` (also
zsh's and fish's if a fragment enables them) and prints, on stderr, with
exit 127:

```
air is not installed. It is in the nixpkgs package air:
  now, in this guest:              nix profile add nixpkgs#air
  from your laptop, kept for good: repose config add air
```

plus `Also in: a, b` when other packages have the same binary (the
package named like the command wins, else the first). `nix profile add`
rather than `install`, which this nix prints as a deprecated alias. A
command nixpkgs lacks gets the plain `<cmd>: command not found`. While a
background installer works, it lists command names, one per line, in
`/run/user/<uid>/repose-installing`; a listed name gets `<cmd> is still
being installed; try again in a moment` instead. `nix-locate` is on PATH
for other guest tooling; the invocation that maps a binary to an
attribute is `nix-locate --minimal --no-group --type x --type s
--whole-name --at-root /bin/<cmd>` (nix-index 0.1.11 has no
`--top-level`; only top-level attributes are listed unless `--all`), one
`attr.output` per line, e.g. `cowsay.out`. Cost: about 30 MB of closure.
**I-221. `run` carries the laptop's global tools; the guest installs what
it lacks in the background, from nixpkgs first.** (implementation,
tools-carry worker, 2026-09-23) The owner's nuru-playground session lacked
`portless`, `tsc` and `air`, all installed on the laptop. The CLI now lists
the laptop's global tools by reading the managers' install directories:
npm's global prefix (`$NPM_CONFIG_PREFIX`, the `prefix=` line of
`~/.npmrc` and nothing else from it, else the directory above `node`), pnpm's
`$PNPM_HOME/global/*/package.json`, bun's `~/.bun/install/global`, Go
binaries in `$GOBIN`/`$GOPATH/bin`/`~/go/bin` through `debug/buildinfo`
(package path and main-module version; a `(devel)` build is a laptop
checkout and stays), cargo's `.crates2.json` (registry crates only), and
`uv tool` and `pipx` venvs. Running `npm ls -g` and friends was rejected:
each is 100-500 ms of process start on every run, against a 20 ms budget;
the reads are about 0.5 ms on the dev box. The list (name, commands, manager,
package, version; no path, no config value) is one carry part with marker
`tools`, sent only when its hash changes. In the guest,
`repose-tools-install plan` answers in the same ssh with what is missing
(the one line "Installing 3 of your tools in the background: ...") and
starts the user unit `repose-tools-carry`, which installs at low priority
after `run` has moved on: `nix profile add nixpkgs#<attr>` when a package
has `bin/<command>` (nix-locate, from the base's index), else the laptop's
manager into a directory already on the login PATH (`GOBIN=~/.local/bin`,
`cargo install --root ~/.local`, because `~/go/bin` and `~/.cargo/bin` are
not on it). nixpkgs first because a prebuilt binary needs no compiler
(air's `go install` failed on cgo in the owner's guest) and lands in dev's
profile, which the store overlay pins. `nix profile add`, not `install`,
which the guest's nix deprecates; the script falls back to `install` on a
nix without `add`. A failure is logged in `~/.repose/tools-install.log` and
said once at the next `run` (`~/.repose/tools-notices`, surfaced through
the probe's markers), and not retried until the laptop's entry for the
tool changes, so one broken tool does not cost every run. While a tool
installs its commands are in `$XDG_RUNTIME_DIR/repose-installing` for the
command-not-found handler. *Rejected:* installing before Ready (the owner:
startup must not get slower); a guest-side list of "known tools" (the
laptop is the source); uninstalling what the laptop removed (the guest may
use it).

**I-222. `run` scans the checkout for the commands its scripts run and the
node major it pins; `repose scan` shows the result.** (implementation,
tools-carry worker, 2026-09-23) Read, not walked: the root and every
workspace package (pnpm-workspace.yaml, package.json `workspaces`) are
checked for package.json scripts, Makefile/justfile recipes, Procfile,
`.air.toml` and compose files, and the root for `.nvmrc`,
`.node-version`, `.tool-versions`, `volta.node`, `engines.node`,
`packageManager`, `go.mod`, `rust-toolchain(.toml)` and
`.python-version`. The first word of every simple command, after
assignments and wrappers (`env`, `cross-env`, `dotenv --`, `time`,
`sudo`, `pnpm exec`, the commands `concurrently` is given), is a
candidate unless the base has it, a dependency of the workspace or the
root provides it (same name, a table of packages whose command differs,
`typescript` -> `tsc`, or `node_modules/.bin`), a workspace `bin` or a
pyproject script or dependency defines it, or it names another script
(a script named like the command it runs, `"stripe": "stripe listen"`,
does not count). `npx`, `pnpm dlx`, `bunx` and `uv run` name nothing.
Candidates join I-221's list with no manager; the guest resolves each
through nix-locate, and a small table gives the npm package for
commands nixpkgs lacks (`portless`). A single pinned node major the guest
does not have is added as `nodejs_<major>` to dev's profile only when
that makes it the `node` of new login shells (the installer checks where
`node` resolves from and verifies with `bash -lc 'node --version'`,
removing it otherwise); when a `node` earlier on PATH would win, it
installs nothing and says to run `repose config add nodejs_<major>`. An
open range (`>=18`) pins nothing. Go, Rust and Python versions are
reported by `repose scan` and left to GOTOOLCHAIN, rustup and uv, which
fetch them. `repose scan [DIR]` is the dry run the owner validates
projects with. *Rejected:* walking the whole tree (a monorepo's
node_modules), resolving candidates on the laptop (no nix-locate there),
and making engines ranges that allow several majors pick one.
**I-223. `repose run` and `attach` spend round trips only where something
changed; `REPOSE_TIMING=1` shows where the time goes.** Measured on the
live service from the dev box (gin-gonic/gin checkout, e2e guest on
host-01), with `ops/dev/startup-bench.sh` driving the CLI in a pty up to
tmux's alternate screen, and `ops/dev/latency-proxy.py` adding a laptop's
200 ms round trip to the api and the gateway without touching the
network. The dev box is next to the service, so its own numbers hide the
cost the owner sees: at 200 ms, v0.1.9 took 4.5 s for a run with nothing
changed, 2.2 s for `attach`, 6.0 s with no master left; it was round
trips, not work. v0.1.9's warm run was: GET project (3 RTT with TLS),
GET project again, GET /me then GET /projects, `ssh true`, probe,
credentials+carry ssh, apply ssh, attach: about 15 RTT. Changes, all in
the CLI:
- `REPOSE_TIMING=1` prints `repose-timing +<ms> <what> <ms>` per phase,
  per api call (route with ids replaced) and per ssh (step name and
  sizes, never a command or path).
- Step 2 reuses the project step 1 read. `GET /me` and `GET /projects`
  run in parallel when needed at all.
- connect's fast path: when `~/.ssh/repose` covers the project (the
  certificate for the CLI key carries its id with 30 minutes left,
  `known_hosts`, its Host block, the alias resolves), no api call; when a
  ControlPersist master is up (`ssh -O check`, local), no `ssh true` either.
  A live master is proof enough: the gateway closes a client connection
  when its guest connection ends. A certificate refusal still gets its
  one re-issue (I-175), reading the account then.
- `run` starts the probe beside step 1's GET when the projects cache names
  the project and the files cover it; with no master, the probe's
  connection becomes the master, so the ssh handshake (about 10 RTT)
  overlaps the api too. It is used only when the project resolves to the
  guessed id and was running before the command; otherwise it is dropped
  and its master closed (a refused connection to a stopped guest would
  otherwise be the master the next ssh rides for up to 10 s).
- `attach` with the cache naming the project and a master up makes no api
  call. Lost: the class in `Connected to`, and the not-running message,
  which a live master rules out.
- The op poll stays at 500 ms for 30 s, not 10 s: a start takes 10-11 s
  on host-01, and the 1 s step from 10 s cost half a second of every
  start; two reads per poll for 30 s is 120 of the 600 a minute (I-187).
- `guestdDead` ignores a `guestd_ok=false` sample taken before the
  project's `started_at` (I-225's api fix, so the CLI benefits before the
  api is deployed).
*Rejected:* longer `ControlPersist` (it would turn most cold runs warm, but
it is `interfaces/ssh-gateway.md`'s and holds a gateway connection and a
multiplexing socket open for longer; left to the conductor with the
numbers); skipping `GET /projects/:id` on `run` as well (it is what
restarts a guest whose guestd died, I-157, so it is overlapped instead).

**I-224. The sync's writes are one ssh, and none when nothing changed.**
The tool logins and the carry had their own ssh between the probe and the
apply (2 RTT plus about 120 ms in the guest on every run), and the apply
re-stashed and re-applied the same diff when nothing had changed (about
600 ms of guest time: the guest pays 8-15 ms per process, nested
virtualisation on host-01, and the apply runs about 40). Now:
- The logins and the carry are built as before and run first in the
  apply's ssh: their script and tar travel inside the apply's tar, run with
  the same stdin, their reply lines come back prefixed `#carry `. A
  failure of their top-level lines stops the apply before the checkout is
  touched, with the same "Could not copy your tool logins" as before. A
  first sync that clones in the guest (I-203) sends them on their own
  first, since the clone needs gh's login.
- The apply records a key (sha256 of HEAD, branch, tracked ref, remote,
  diff, untracked tar) in `.git/repose-synced-key`, emptied before it
  touches the checkout and written at its end. The probe returns it, HEAD
  and `.git/HEAD` (two builtins, one git call moved). When the key
  matches, HEAD and branch match, no commits are to send, no `.env`
  changed, and the tree is either clean with a clean laptop or dirty
  with only the last sync's changes (I-210's fingerprint), the apply is
  skipped: nothing is stashed again, and the summary says "the guest
  already had them". Any change on either side makes it run.
- The logins get the carry's marker treatment: `creds` holds the hash of
  every row's bytes and mtime plus the identity and gh helper lines;
  `~/.repose/creds-paths` lists what they wrote and the probe prints
  `#credsmissing` when one is gone. Unchanged and present: not sent.
  A guest login newer than the laptop's is kept as before; the only
  difference is that "Kept the guest's gh login" is said when it happens,
  not on every run.
Together with I-223, a run with nothing new is the probe (beside the api)
and the attach. *Rejected:* rewriting the apply's shell to spawn fewer
processes (the I-210 script went through three reviews; skipping it when
it has nothing to do gets the same time without touching it).

**I-225. Server side of a start: hostd dials a booting guest's guestd
every 200 ms, guestd skips a registration it already loaded, and a sample
from before a start is not the new guest's.** Measured on host-01 (e2e
guest, base 2026.09.23.2, a start is 10.2 s starting→running): guestd
listened 6.3 s into the boot, hostd's first session came about 1 s later
(it dialled every `GuestdRetry`, 2 s), `RegisterPaths` took about 0.7 s
(dump-db 70 ms on the host, `nix-store --load-db` of 482 KB in the guest,
about 200 ms warm, more at boot), then home-manager 1.35 s before
`systemd-user-sessions` and SetupProject.
- hostd: a monitor dials every `GuestdBootRetry` (200 ms) until its first
  session, then `GuestdRetry` as before. Unit test: guestd listening
  300 ms into the boot, create done in 430 ms, 2.06 s with the old retry.
- guestd: `RegisterPaths` stores the sha256 of what it loaded in
  `/var/lib/repose/paths-loaded` (on the volume) and, sent the same bytes
  again with the database present, only writes the boot stamp.
- api: a newest sample older than the project's `started_at` (taken
  while the previous run was being stopped: guestd already gone, state
  still running) no longer makes `start` a restart or the project's
  `guestd_ok` false; the project shows `guestd_ok` null until a sample of
  this run arrives. Seen live: for a minute after every start `repose
  status` said guestd was not answering and `repose run` sent a start
  that came back 409.
Not changed, measured for whoever takes it: the guest boot itself (kernel
1.1 s, initrd 2.75 s of which switch-root 1.1 s, userspace to guestd
2.3 s through zram setup and tmpfiles), home-manager's 1.35 s activation
on the path to the first login, the 5.4 s no-op nix build a create pays
when no running guest has the closure (I-160 reuses only a closure the
host runs), and the guest's per-process cost.

**I-228. Tools that download their own binaries work in the guest with
their stock commands.** (guest tooling, 2026-09-23) nix-ld (I-218) runs
a downloaded ELF, but an audit on a live guest (base 2026.09.23.3) found
four places where the tool decides what to download, or where to put it,
wrongly on NixOS. `nix/guest/base/compat.nix` fixes each for every
project, with no per-project setup:
- *Prisma* asked binaries.prisma.sh for `linux-nixos` engines, which do
  not exist ("Precompiled engine files are not available for nixos", then
  a 404). `@prisma/get-platform` takes the target from `ID=` in
  /etc/os-release alone and has no override (`PRISMA_CLI_BINARY_TARGETS`
  only adds downloads; the `PRISMA_*_ENGINE_*` path variables must match
  the project's exact engines commit, so no base-wide value exists).
  Every engine URL is built as `$PRISMA_ENGINES_MIRROR/<channel>/<commit>/
  <target>/<file>`, so the guest sets `PRISMA_ENGINES_MIRROR=
  http://127.0.0.1:850`: a socket-activated redirect (one bash instance
  per connection, nothing proxied, cached or logged) that answers a
  `linux-nixos` path with a 302 to the `debian-openssl-3.0.x` build of
  the same commit and any other path with a 302 to the same path
  upstream. The engines are saved under their linux-nixos names and run
  through nix-ld (libssl.so.3 from its set; the Node-API library loads
  in node, which already has OpenSSL 3 loaded). Works for any Prisma
  version with a debian-openssl-3.0.x build (4.x on). Prisma still prints
  its nixos warning on each command; it is harmless, and nothing short of
  the per-commit path variables silences it. Rejected: changing `ID=` in
  os-release (every other tool that special-cases NixOS would be lied
  to); a `NODE_OPTIONS` preload that fakes the file (every node process
  pays it); a route on the host caches (a host switch, and the guest has
  internet anyway). Port 850: loopback and under 1024, so the CLI never
  auto-forwards it (I-199).
- *Playwright*'s `PLAYWRIGHT_BROWSERS_PATH` was the read-only store path
  of the packaged browsers: `npx playwright install` of any version hung
  ten minutes on a lock file it could not create, and a project on
  another Playwright version could not get its revision at all. The
  variable is now `/home/dev/.cache/ms-playwright` (Playwright's own
  default); `repose-playwright-seed.service` links the packaged revisions
  into it at boot (never over a real directory; links into an older
  base's store are repointed or removed), so the base's version still
  needs no download. Playwright's cache GC may remove the links when the
  user installs another version; the next boot restores them. The MCP
  server's wrapper sets the store path itself and is unaffected.
- *The system python3* (nixpkgs) cannot load a manylinux wheel's
  libstdc++ (numpy, pandas, grpcio fail on import in a `python3 -m venv`
  or a `uv venv` made before any `uv python install`). `python`,
  `python3` and `python3.12` on the system PATH are now a wrapper that
  appends nix-ld's library directory to `LD_LIBRARY_PATH` and execs the
  interpreter under its own name (`exec -a "$0"`), so `sys.executable` is
  the wrapper and a venv made from it links back to it. uv's managed
  Pythons already ran through nix-ld and were fine.
- *pkg-config* found no system library, so `openssl-sys` (every
  reqwest/native-tls crate) failed to build. `PKG_CONFIG_PATH` names the
  dev outputs of openssl, zlib, sqlite and libffi; cc's ld-wrapper adds
  the rpath.
Audited and needing nothing beyond I-218: rustup's toolchain and `cargo
build`, uv-managed Python 3.12 (ssl, sqlite3, tkinter) and its wheels
(numpy, psycopg[binary], cryptography, pandas, pillow, lxml, grpcio,
pydantic-core, orjson), miniforge, sharp, better-sqlite3 (prebuilt and
node-gyp), bcrypt, esbuild, @swc/core, lightningcss, @tailwindcss/oxide,
sass-embedded, @parcel/watcher, wrangler's workerd, Puppeteer (system
chromium and its own downloaded Chrome), bun from npm, deno's install
script, `go install` with cgo, GOTOOLCHAIN downloads, golangci-lint's
install script, and the official Linux binaries of terraform, tofu,
kubectl, helm, awscli v2, gcloud, stripe, supabase, flyctl, pulumi,
protoc and node 22. Not checked: pyenv (builds CPython from source and
needs headers beyond pkg-config's; `uv python install` is the path the
base supports). Cost: 8,872 bytes of closure (6,165,108,296 to
6,165,117,168; the four dev outputs were already in it). Tested by the
VM test `guest-compat` (redirect, the 6.16 debian schema engine running
through nix-ld, the seed, a manylinux numpy wheel under `python3 -m venv`
and `uv venv`, pkg-config) and by hand on a live guest.

**I-227. Every package manager's user bin dir is on PATH for every
process of dev's.** (guest tooling, 2026-09-23) The owner ran
`go install github.com/air-verse/air@latest`; it worked and `air` was
still "command not found": `~/go/bin` was not on PATH. Only
`~/.local/bin`, pnpm's and npm's dirs were, and only in shells that
sourced `/etc/profile.d/repose.sh`.
- *One list*: `nix/guest/base/user-bin-dirs.nix` names each manager's
  dir: `.local/bin` (uv tool, pipx, pip --user, stack, cabal), pnpm's
  `PNPM_HOME`, `.npm-global/bin` (npm -g, yarn v1 global), `go/bin`,
  `.cargo/bin`, `.bun/bin`, `.deno/bin`, `.yarn/bin`,
  `.local/share/gem/bin`, `.config/composer/vendor/bin`,
  `.dotnet/tools`, `.ghcup/bin`, `.cabal/bin`, `.opam/default/bin`,
  `.luarocks/bin`, `.mix/escripts`, `.nimble/bin`, `.juliaup/bin`,
  `.julia/bin`, `.krew/bin`, `.volta/bin`. env.nix sets it as
  `environment.sessionVariables.PATH`, which NixOS puts ahead of the
  profiles in both `/etc/set-environment` (every bash) and
  `/etc/pam/environment` (the SSH session, so an SSH command's `bash -c`,
  and dev's systemd user manager, so user units and the tmux server it
  starts). User dirs come first so a user's install wins over the base's
  copy. The PATH line in `repose.sh` is gone.
- *Variables*, also session variables now (they reach user units too):
  the existing `NPM_CONFIG_PREFIX` and `PNPM_HOME`, plus the tools' own
  defaults made explicit (`GOPATH`, `CARGO_HOME`, `RUSTUP_HOME`,
  `BUN_INSTALL`, `DENO_INSTALL_ROOT`, `COMPOSER_HOME`) and `GEM_HOME`,
  which is not a default: without it `gem install` writes to ruby's
  store path. `GOBIN` is not set: with it set, `go install` refuses
  cross-compiled binaries.
- *tmux*: a window a client outside tmux creates with a command (how
  `repose run` starts an agent over SSH) gets the client's PATH, not the
  server's (tmux 3.x), so an agent gets the fresh SSH PATH even from a
  server started under an older base. What runs with the server's own
  environment (run-shell, `#()` status jobs, a window opened from inside
  tmux with a command) had the PATH of the `repose-tmux-session` unit,
  because NixOS gives every unit it defines its own PATH (the VM test
  caught every user dir missing there); the unit now sets the server's
  global PATH from a clean login shell right after creating the
  session. A new interactive pane is a login shell and re-reads
  `/etc/set-environment`. The same holds for units a fragment or NixOS
  defines: they keep their own `path`; units a user starts
  (`systemd-run --user`, their own unit files) get the manager's PATH,
  which is the PAM one above.
- *After a base applied without reboot*: an activation script sets the
  new login PATH on dev's running tmux server (`set-environment -g`) and
  user manager (`systemctl --user set-environment`); shells already open
  keep their PATH until the user opens a new one.
- *Not covered by a static dir*, because the tool sets PATH from its own
  shell init (which it writes into `~/.bashrc` itself) or its bin dir
  moves per version: nvm, fnm, sdkman, rbenv/pyenv shims, and
  `gem --user-install` (`~/.local/share/gem/ruby/<version>/bin`; plain
  `gem install` goes to `GEM_HOME` instead). `corepack enable` writes
  shims next to node, in the read-only store; use `corepack enable
  --install-directory ~/.local/bin`.
- Found on the way: `repose-tmux-session` exited 2 (and systemd killed the
  new tmux server with its cgroup) when `/etc/repose/env` was missing,
  because `sed` on a missing file fails under pipefail. guestd writes
  the file before project.json, so no guest hit it, but the lookup now
  tolerates its absence.

**I-230. Guest disks are opened O_DIRECT, and guest@ units get a
MemoryHigh 128 MiB under MemoryMax.** (implementation, guest-memory
worker, 2026-09-23) host-01 20:16:42Z: the `large` guest of e2e-tools was
killed by its own unit's memory cgroup while an agent ran installs
(miniforge, playwright, gcloud, rustup, `uv pip install torch`). Kernel:
`iou-wrk-683063 invoked oom-killer: gfp_mask=...GFP_NOFS|__GFP_WRITE,
order=3`, `usage 8912884kB, limit 8912896kB`, stats `shmem 8586883072,
file 9049001984, file_writeback 460988416`, stack
`io_write -> blkdev_write_iter -> iomap_file_buffered_write`; the victim
was cloud-hypervisor, the unit's only process. The guest's RAM is a
`shared=on` memfd (virtio-fs needs it), charged to the unit as shmem and
never reclaimable without swap, so of the unit's `MemoryMax` (class +
512 MiB) only the 512 MiB is elastic. Cloud Hypervisor opened the thin
volume without O_DIRECT, so every guest disk write became host page cache
charged to the same unit; pages under writeback cannot be reclaimed on
demand, 460 MB of them filled the overhead, and a GFP_NOFS order-3
allocation had nowhere to go. Any write-heavy tenant could lose the whole
VM (tmux, agents, unsaved state). The other large guest on host-01 was at
8551/8704 MiB with 1.1 GiB of clean host cache when this was found.
- *`--disk ...,image_type=raw,direct=on`* (ch.go, and the runner in
  microvm.nix). The guest has its own page cache; the host's second copy
  bought nothing and is what killed it. Cloud Hypervisor 53 probes the
  volume's topology (BLKSSZGET) whether or not the disk is direct, so a
  guest on host-01's 4096-byte-sector thin volumes already sees 4096-byte
  logical blocks and nothing about the guest-visible disk changes; for an
  unaligned request CH 53 bounces through an aligned buffer
  (`block/src/aligned_file.rs`). Reads are no longer served from host
  cache either, which is right for the same reason.
- *`MemoryHigh = MemoryMax - 128 MiB`* (unit.go, HighMarginMiB). With the
  disk direct, what the unit holds besides shmem is page tables and KVM's
  second-level ones (about 4 MiB per GiB of RAM), slab, io_uring and the
  cache of the kernel and initrd CH read: 75 MiB `kernel` on host-01's
  8 GiB guest, 21-27 MiB of file cache in the repro. MemoryHigh only acts
  if something unforeseen grows there again: the kernel then reclaims and
  throttles the allocating thread instead of going straight to the OOM
  killer, which inside the unit can only pick the hypervisor.
- *OverheadMiB stays 512*, so host capacity math does not change:
  hostd's FreeMemBytes and create check reserve class + 512 MiB, the api's
  scheduler takes the least of that and its own class-RAM count, and
  guests.slice is RAM minus the host reserve (hostd.nix). virtiofsd is its
  own unit (`virtiofsd@<id>`, `MemoryMax=1G`, outside the reservation): it
  serves the read-only store export, never writes, and its host cache is
  clean and reclaimable, so `--cache auto` stays; its writes cannot reach
  the guest unit because there are none and it is a different cgroup.
- *No OOMScoreAdjust/OOMPolicy change*: the guest unit has one process,
  so there is nothing to prefer inside it, and killing virtiofsd instead
  would leave a guest without its store (CH does not reconnect a
  vhost-user-fs backend).
- *Rollout*: the argv and properties are rendered at each boot (create
  and start), and reconcile never restarts a running guest, so a host
  switch changes nothing for running guests; each gets `direct=on` and
  MemoryHigh at its next stop/start (`ch.args` shows which a guest has).
  The conductor may stop/start guests at a convenient time to close the
  window; no migration is needed, and the old unit shape stays valid.
- *Evidence*: `nix/hosts/tests/guest-memory.nix` (check
  `host-guest-memory`, a script: a VM test would nest the guest three
  levels deep, and it never booted that way on the dev box) runs a 512 MiB
  guest with 200 MiB pinned and the class+overhead unit shape from the
  goldens, before and after, on the dev box (Azure AMD, not a host; NVMe
  temp disk). Throughput run, before: host cache in the unit 612 MiB,
  dirty+writeback 398 MiB, memory.current 1024 of 1024 MiB, memory.events
  `max 7353`; after: 25 MiB (kernel and initrd), 0, 543 MiB, `max 0`.
  Sustained small+large files (the e2e-tools pattern), before: 494 MiB,
  492 MiB dirty+writeback, 1023 of 1024, `max 1278`, 344 MiB/s; after:
  0, 0, 518 MiB, `max 0`, 499 MiB/s. On the slower root disk the
  sustained run was 404 MiB cache, 12.1 MiB/s before and 21 MiB,
  22.5 MiB/s after. Guest throughput before -> after: seq write 1M direct
  545 -> 539 MiB/s, buffered+fsync 398 -> 500, 4k QD1 direct write
  17430 -> 14895 IOPS (-15 percent, the cost of a real write per 4k),
  small files 405 -> 500 MiB/s, seq read 1M direct 1056 -> 1067 MiB/s.
  None of the runs reached the kill: the dev box drains writeback faster
  than host-01 did (460 MB under writeback there); every before run sat
  at MemoryMax over a thousand times, one slow flush from host-01's kill.
  The dev box's disk file has 512-byte DIO alignment, so the guest saw
  512-byte blocks there; the 4096-byte path (host-01's thin volumes) is
  covered by CH's topology probe and bounce code, not by this run, and is
  checked live after the first restart (`cat
  /sys/block/vda/queue/logical_block_size` in the guest: 4096, as before).
  *Rejected:* a bigger overhead (any fixed margin fills at disk speed);
  tuning the host's dirty limits (global, and pages under writeback are
  the unreclaimable part anyway); moving the RAM to hugetlbfs (a separate
  accounting change, not needed for this).

**I-231. A guest boot's path to Ready and to its first login carries only
what they need: a scripted stage 1, no mount-rate-limit stall, zram and the
setuid wrappers off the chain, and no home-manager run for an unchanged
generation.** (boot time, 2026-09-24) Measured on host-01 (e2e-boot, large,
base 2026.09.23.4, `ops/dev/startup-bench.sh -r 200 stopped`): 12.6-13.1 s
from `repose run` to the tmux screen (one outlier 28.7 s), of which
StartGuest to `running` 8.24-8.34 s. In the guest: kernel 0.98 s, systemd
initrd 2.94 s (0.55 s of it the store mounts held back by systemd's
mount-monitor rate limit, 1.18 s the activation script), stage 2 to guestd
listening 2.6 s (credential tmpfs mounts and API mounts tripping the same
rate limit, 0.6 s; zram before swap.target, 0.37 s, which every tmpfs mount
waits for; the setuid wrappers before sysinit.target; the BPF LSM program
0.27 s), then 1.19 s of home-manager on the path to the first login (hostd's
SetupProject is a login). Reproduced on the dev box with the guest runner
under Cloud Hypervisor 53, an ext4 volume, virtiofsd and a tap, driven by a
stand-in for hostd's boot+deliver (Ping at the dial interval, RegisterPaths,
SetPrincipals, SetupProject): CH start to `running` 8.9-9.3 s before, the
same shape as host-01. Changes, each measured there:
- *Scripted stage 1* (`boot.initrd.systemd.enable = mkForce false`;
  microvm.nix sets systemd's with mkDefault). It loads the virtio modules,
  mounts the volume, the store share and the overlay in 0.6 s against
  2.1 s; the initrd is 11 MB against 27. The activation script runs in
  stage 2's init as it did before NixOS 24.11 (0.66 s). The store
  overlay's options now name `/mnt-root`, not `/sysroot`:
  `repose-pin-profile` strips both (without it its "already pinned" check
  looked in a path that does not exist and every pin touched the whole
  profile closure again; guest-base's own check of the upper dir failed),
  and so does the test.
  Running: 7.3 -> 5.9 s.
- *Stage 2 without the rate-limit stall*: `ImportCredential=` emptied for
  the twelve units that import credentials (each got a tmpfs on
  /run/credentials; the guest is passed none), and debugfs, tracefs,
  configfs, fusectl and hugetlbfs no longer mounted
  (`systemd.suppressedSystemUnits`; `sudo mount` gives any of them back).
  The burst was more than five mount-table changes in a second; systemd
  then starts no mount unit until the second is over (hard-coded, not
  configurable in 261).
- *zram off the chain*: zram-generator's swap is `Before=swap.target` and
  every tmpfs mount is `After=swap.target`, so /run/wrappers,
  local-fs.target and all after waited for the device, mkswap and swapon.
  `repose-zram-swap.service` makes the same swap (zstd, half the RAM up to
  2 GiB, priority 5) with no default dependencies, wanted by
  multi-user.target. Docs 02 updated.
- *Setuid wrappers*: before `systemd-logind` and `systemd-user-sessions`
  instead of sysinit.target (PAM's unix_chkpwd is the only boot-time user:
  logind starts dev's lingering user manager; without the ordering its
  first start failed). Still 1.2-1.5 s under the boot's load (45
  processes), so the finished directory is kept in
  `/var/lib/repose/wrappers/<key>` (root-only; key = hash of NixOS's
  script, which names every wrapper, owner, mode and capability) and an
  `ExecCondition` copies it back with one `cp -a` and skips the script;
  a changed key runs the script and replaces the copy (`ExecStartPost`).
  `sudo` works and `getcap` shows the capabilities after a restore.
- *No BPF LSM* (`security.lsm = [ landlock yama ]`): it only serves
  RestrictFileSystems=, and loading it cost 0.27 s after the switch.
- *home-manager*: `home-manager-dev` ends at once when
  `~/.local/state/home-manager/gcroots/current-home` already is this
  generation (the fallback is home-manager's hm-setup-env, line for line).
  A new base or fragment is a new generation and activates as before.
  Lost: a boot no longer re-links a managed dotfile the user deleted, and a
  fragment's `home.activation` scripts run when the generation changes, not
  at every boot. 1.19 s on host-01, 1.9 s on the dev box.
- *repose-npm-registry* (user unit, I-202/I-208) has no default
  dependencies: default.target waited for it, the tmux session waits for
  default.target, and with the cache not answering it retries for 60 s,
  so a start with the cache down failed SetupProject's 60 s timeout.
- *guestd's SetupProject* runs one `systemctl --user -M dev@ start` for
  the tmux unit, not `is-active` and then `start`: each `-M dev@` is a PAM
  login and a bridge (about 0.2 s at boot), and systemd does not repeat a
  start of an active unit. The `tmux_started` log field is gone.
Result on the dev box, the same runner and volume, interleaved: CH start to
guestd Ready 6.48-6.72 s -> 4.35-4.58 s, to `running` 8.88-9.28 s (median
9.09) -> 5.49-5.83 s (median 5.71). Where the 5.7 s go now: kernel 0.85,
stage 1 0.61, activation 0.66, systemd to local-fs 1.04, to basic.target
0.81, guestd listening +0.19, logind 0.2, dev's user manager 0.66 (unit
loading 0.3), tmux session 0.36, hostd's side and its dial the rest.
Expected on host-01: StartGuest to `running` from 8.3 s to about 5.2 s.
Not changed, measured: users (perl) 0.27 s and /etc 0.15 s of the
activation (`services.userborn` and an /etc overlay would cut them but
change how users are made on existing volumes); dev's user manager's own
start; the kernel's memory init (0.35 s for 8 GiB, grows with the class).
*Rejected:* dropping the initrd (the init lives in the store, which only
stage 1 can mount); `Nice=` on the wrappers (no change: it is exec cost).
VM tests: guest-base, guest-parity, guest-compat, guest-tools-carry,
guest-devtools, guest-desktop, guest-docker, guestd, guest-closure-size,
guest-runner-builds, host-services pass.

**I-232. hostd's start path: the boot dial every 50 ms, virtiofsd's socket
looked for every 10 ms, and the registration read while the guest boots.**
(boot time, 2026-09-24) host-01's journal for a start: StartGuest to
virtiofsd started 45 ms, to Cloud Hypervisor started 160 ms (virtiofsd's
socket was checked every 100 ms and is there a few ms after start), and
after guestd answered, `nix-store -qR` plus `--dump-db` (70 ms) ran before
RegisterPaths. Now `GuestdBootRetry` defaults to 50 ms (a failed dial is a
connect to CH's socket and one line; 200 ms averaged 100 ms of waiting),
`waitVirtiofsSocket` stats every 10 ms and asks systemd about the unit every
tenth step, and the dump is read in a goroutine started when CH is, which
deliver waits for (a failure still fails the create or start at step 10 as
before). Expected: about 0.25 s off every start and create. Needs a host
switch. `TestBootDialFindsGuestdSoon` and the guest package's tests.

**I-233. Resuming a stopped guest from a memory snapshot is not adopted
yet; the numbers and what it needs are recorded.** (boot time, 2026-09-24)
Measured on the dev box with Cloud Hypervisor 53 and the I-231 runner
(large, 8 GiB, booted, 20 s idle): `vm.pause` 6 ms, `vm.snapshot` 0.29 s,
the memory file sparse at 0.78 GB (what the guest had touched); restore and
`vm.resume` 0.54 s with the file in the host's page cache, 1.2 s cold;
guestd answered 10 ms later and tmux still had its session. A start would
go from about 5 s of boot to about 1 s. Not adopted because: the image
holds everything in the guest's RAM, secrets from /run/repose/secrets and
tool logins included, on the host's disk, which is a fourth home for
secrets (CLAUDE.md) unless encrypted with a key the host does not keep;
the guest clock resumes at the snapshot time (seen: 10 s behind) until
guestd sets it; a guest that did real work has GBs of page cache, so up to
the class's RAM per stopped guest on the host and seconds per stop, unless
guestd drops caches first; virtiofsd is a new process after restore and
the guest kernel's FUSE inodes come from the old one (it worked for the
commands tried, but CH does not migrate virtiofsd's state and nothing
proves open store files survive); the snapshot must be discarded whenever
the volume changes outside the guest (restore, resize), the closure
changes (base upgrade, ApplyConfig) or the guest moves hosts, and stopped
storage would need a price. Worth doing, after those are designed, as a
fast path beside the boot, never instead of it.

**I-234. Two regressions of the faster boot, found live.** (conductor,
2026-09-24) On host-01 after base 2026.09.24 (I-231, I-232), one start of
five came back `guest_unresponsive` and one took 26 s. (1) guestd now
answers while sshd's boot-time start job is still queued, and
`systemctl reload-or-restart sshd` after writing the host key exited 1
against it; WriteSecrets failed and so did the start. The reload is now
retried every 250 ms within its 20 seconds (`TestSSHDReloadRetriesWhileItsStartIsQueued`).
(2) The activation script ran `repose-pin-profile` in the foreground; after
a stop had cut the previous background pass short, a boot copied 98 store
paths up before systemd started (console: "pinned 98 store paths", first
journal entry 23 s after StartGuest). At boot the unit now runs from
`multi-user.target` in the background (Nice 10, idle I/O), and a switch
starts it with `--no-block`. Waiting bought nothing: a pin protects against
a later host garbage collection, never an earlier one.

**I-235. Keeping a stopped guest's processes: the options for secrets,
recorded; nothing built.** (conductor with the owner, 2026-09-24) I-233
measured a memory snapshot and resume (about 1 s instead of a boot) and
left it out because the image is a fourth home for secrets. The threat it
adds is at rest, not in use: the host can already read a running guest's
RAM (no confidential computing) and the volumes are not encrypted on the
host. What the image adds is secrets outliving the session: named
secrets, tool logins and whatever agents hold in memory (Claude's and
`gh`'s tokens, environment values), on the host's disk after a stop, and
a secret deleted or rotated while the guest is stopped still in the image.
The options, in the order the owner would take them:
1. *Pause in RAM ("warm stop").* `stop` pauses the VM and leaves its
   memory resident on the host; `start` resumes it in milliseconds with
   tmux, agents and dev servers alive. No image, so no new home for
   secrets. Costs host RAM while paused: after N idle hours, or when the
   host needs the memory, it falls back to today's full stop; paused time
   needs a price, since it holds capacity. Needs a clock step on resume
   and a fall back to a boot on a base change. The recommended first step.
2. *Encrypted image, key held by the api.* At stop hostd encrypts the image
   with a fresh key and hands the key to the api, which stores it sealed
   like a named secret in Postgres; at start the api returns it, hostd
   decrypts and resumes. The image stays on that host (never uploaded) and
   is deleted after resume or a time limit, after which the start is a
   boot. At rest it is ciphertext whose key the host does not keep, the
   Postgres model; a compromised host at resume time is not defended
   against, and it could read live RAM anyway. The api discards the image
   whenever it would be wrong: a secret changed or deleted, a base
   upgrade or config apply, a volume restore or resize, a move of host.
   Adopting it amends "secrets have three homes" (CLAUDE.md,
   features/secrets.md) with a decision entry.
3. *Wipe the known secrets before the snapshot.* guestd clears
   /run/repose/secrets before `vm.snapshot`; every start re-delivers them
   (WriteSecrets). Keeps named secrets out of the image but not copies in
   process memory, so it is a layer on 2, never enough alone.
4. *Confidential VMs (Intel TDX).* Guest memory encrypted from the host.
   The strongest answer, but Cloud Hypervisor cannot snapshot a TDX guest
   and it is a platform change; later.
5. *Opt-in per project* ("keep processes across stop"), with any of the
   above, so a project that never opts in keeps today's behaviour.
Decision for now: none of these is built. Documented so the next session
starts from here; the owner's lean is 1, then 2 with 3 if resumes after
long stops are wanted.
**I-236. Waiting on an op is a long-poll: the api answers the moment the
op or its project changes.** (cli startup, 2026-09-24) After I-231..I-234 a
`repose run` on a stopped guest took 8.9 s at a laptop's 200 ms (median,
`ops/dev/startup-bench.sh -r 200 stopped`), and the CLI learned that the
start had finished late: each poll was `GET /projects/:id` (for the phase
label), then `GET .../ops/:op_id`, then a 500 ms pause, so a 0.9 s cycle,
and then one more project read before the first ssh. Simulated at 200 ms
(fake api, start of 2-3 s, 10 runs each), the time from the op finishing to
the first ssh being free to go was 684-844 ms median, 1.1 s worst.
- api: `GET /projects/:id/ops/:op_id?wait=<d>&seen=<version>` holds the
  read until the op's `version` (state, step, the project's state)
  differs from `seen` (or from what it was when the request came), the op
  is finished, the wait (capped at 20 s, under the proxies' idle timeouts)
  runs out, or the api starts draining (`SetReady(false)`: a rolling
  deploy answers its held reads at once instead of waiting 20 s on them).
  The handler re-reads one small row (`store.GetOpMark`) every 100 ms and
  holds no connection in between: hostd's results land in api-grpc, a
  different process, so there is no in-process event to wait on, and a
  NOTIFY listener per waiter would hold a connection each. Held reads are
  bounded, 4 per user and 1000 in all; past that the read answers at once
  without `Repose-Long-Poll: 1` and the client pauses as before. Every op
  read now also carries `version`, `project_state` and `phase`.
- CLI: the first read is plain; when it carries `version` the next ones
  are held, back to back, and the phase label comes from `project_state`
  (no project reads). An api without them (the one live now) is polled as
  before, but with the project read beside the op read rather than before
  it, and the 500 ms counted from one read's start to the next (I-187's
  budget counted it that way; at 200 ms the reads had stretched it to
  0.9 s). A 502/503/504 page from the proxy or a dropped connection
  (Coolify's rolling redeploy) is retried every 500 ms for up to 30 s
  instead of ending the command. `repose start` acts on the project its
  resolve read, like `run` (I-223), instead of reading it again.
- Measured: the same simulation gives 296 ms median (103-571) for the new
  CLI against the current api, 157-167 ms (101-200) with the long-poll.
  Live at 200 ms against the current api, ensure-running went from 5365 to
  5127 ms median (the boot is steady to a few ms there, so the old 0.9 s
  cycle's phase decided the number; the simulation's spread is the honest
  figure). Expected with the api deployed: about 0.15 s more off the
  median and no tail past 0.2 s. The request histogram of this route will
  show held reads of up to 20 s; they are waits, not slow answers.
*Rejected:* SSE for ops (the CLI already has a stream for the build log,
but a second stream per wait costs a connection per waiter behind the
proxy, and a long-poll degrades to the old poll on any api); an in-process
channel from the ops engine (results arrive in api-grpc; the engine in the
api would miss them); LISTEN/NOTIFY per waiter (a pooled connection held
for up to 20 s each).

**I-237. The first ssh to a guest that was just started goes out the
moment its op finishes, and it is the sync's probe.** (cli startup,
2026-09-24) After the op, `run` read the project, checked the files, ran
`ssh <slug>.repose true` (1.9 s at 200 ms: about 9 round trips, TCP, key
exchange, `none` and a public key query before the signed one, channel
open, exec) and then the probe (0.58 s) over the master. Now, when
`~/.ssh/repose` covers the project, the op's end starts the probe itself
as the first connection (`startBootProbe`), beside the project read that
follows; `connect` takes that connection as the master, and the sync
takes its reply. `run --no-sync` sends `true` instead. Before dialling,
anything earlier is closed: an early probe (I-223) refused because the
guest was stopped is waited for and marked unusable, and the slug's
master is closed (`ssh -O exit`), so the new connection is never a session
on a stale master. A boot probe that failed (refused, certificate, a
guest not answering yet) is never used: `connect` closes its master and
falls back to `ssh true` with its retries and the certificate re-issue,
and the sync runs its own probe. The gateway caches a route answer for 5 s
(`RouteTTL`), refusals included, so a boot probe whose early probe was
refused (a checkout with a remote the cache knows) does not dial into the
cached refusal: the refusal was cached when the early probe reached
authentication, a fixed number of round trips after it began, and the new
connection reaches authentication after as many, so it starts no sooner
than 5.3 s after the early probe started (usually it is later anyway). Live at 200 ms against the current api (`e2e-rt`, 8 runs,
v0.1.10 median 8988 ms): connect+probe 1919 + 576 ms became one 2037 ms
ssh, started 204 ms earlier, and the whole run 8092 ms median; from a
checkout with a remote (`e2e-rt2`, early probe refused, 5 runs each) 9009
became 8596 ms.
*Rejected:* dialling during the boot: the gateway refuses a guest that is
not `running` at authentication and caches the refusal for 5 s, so a dial
before the op ends costs up to 5 s; the op reports nothing earlier than
its end (sshd's readiness is the tail of `StartGuest`). Holding the
authentication in the gateway while the project is `starting` (it
would hide about 7 of the 9 round trips, some 1.4 s at 200 ms) is the next
cut, and belongs to the gateway (`interfaces/ssh-gateway.md`). Sending the
start beside the resolve read (1 round trip): a start is billed and cannot
be taken back, and `run` refuses some commands after the read (a one-word
prompt that is a project's name) and resolves the project by remote,
which the cache can only guess. Folding the sync's apply into the probe's
ssh: the apply is built from the probe's answer (commits the guest lacks,
its dirty state, the carry's markers); a guarded speculative apply would
re-open I-210's three-times-reviewed script to save one round trip on
runs that change something. On a run with nothing changed the apply is
already skipped (I-224), so a stopped run is now one ssh before the
attach.

**I-241. Gaps the user docs found, closed in code rather than documented
as broken.** (docs session, 2026-09-23; ported onto main 2026-09-24. The
web/landing-page branch numbered this I-227, which main uses for the user
bin dirs on PATH.) Writing the user-facing docs (served at `/docs`)
against the code turned up six places where the code disagreed with the
feature docs or did nothing; each was confirmed still broken on main by a
test before the fix:
- `repose open --desktop` ran `systemctl --user start repose-desktop`, a
  unit no guest has (the desktop is socket-activated system units,
  I-33), so it failed at "start the desktop in the guest", and it never
  printed the VNC password noVNC asks for. It now runs
  `repose-guest-profile desktop start`, which starts the chain and prints
  the password, and prints `VNC password: ...`. `--desktop --stop` runs
  `repose-guest-profile desktop stop`, as `features/browser.md` specified;
  `--stop` without `--desktop` is a usage error.
- `sync.exclude` in `config.toml` was read only as the quoted top-level key
  `"sync.exclude"`; the `[sync]` table with `exclude = [...]` that the
  docs and every TOML reader expect was silently ignored. Both spellings
  now reach the sync (`TestLoadConfigSyncExclude`; `interfaces/cli-config.md`
  names both).
- `--agent` took any word and opened a tmux window running it;
  `features/run-and-attach.md` says anything but the five exits 2 listing
  them. It does now, before the api or the guest is reached.
- `default_agent` in `config.toml` had no effect: the api stores
  `agent_default = claude` for every new project and `run` reads the
  project's first. The CLI now sends `agent_default` from `config.toml`
  on create when it names one of the five (the api already accepted it;
  `interfaces/api.md` now lists it, and the fake api takes it). Existing
  projects keep theirs; nothing yet changes one after creation (`PATCH`
  has the field; no CLI or dashboard control).
- Agents started by `repose run "prompt"` run as tmux `new-window`'s
  command, a bash that is neither login nor interactive, with the
  environment of a tmux server a user unit started. Main's I-227 already
  gives that command the login PATH (sessionVariables reach the SSH
  client and tmux copies an outside client's PATH into the window), but
  not what `/etc/profile.d/repose.sh` adds: `REPOSE_PROJECT` from
  `/etc/repose/env` and the named secrets from `/run/repose/secrets.env`
  (so the `CLAUDE_CODE_OAUTH_TOKEN` fallback of `features/agents.md`
  could not work for a prompt run). guest-devtools on main showed it: a
  wrapped probe agent started exactly as `startAgentWindow` starts one
  got `path=ok` and empty `REPOSE_PROJECT` and secret. The agent wrapper
  (`nix/overlay/agents/wrap.nix`) now sources that file before exec; it
  is idempotent, and it also picks up a secret set after the tmux server
  started. Needs a base release to reach guests.
- `repose mcp forward` and `repose browser bridge` pointed users at
  `docs/features/agents.md` and `docs/DESIGN.md §18`, files a user does
  not have; they now name the public docs page
  (`https://repose.herakraft.co/docs/agents#mcp-servers`,
  `.../docs/browser#things-that-dont-work-yet`).
The dashboard's project page also gains Config and Secrets links: the
secrets page existed with nothing linking to it. `features/browser.md`
and `features/notifications.md` examples now match what shipped.
**I-238. Guests cannot send mail straight to port 25; submission ports
stay open, and blocked attempts are counted per guest.** (anti-abuse,
2026-09-24) The owner: "def crack down on abuse, smtp, crypto bros".
DESIGN §15 left abuse manual, and nothing stopped a guest from being a
spam relay: a rented cloud address sending to other servers' port 25 is
the spam a mail provider cannot see coming, and the address that gets
blocklisted is the host's, shared by every guest through the NAT.
`guest_fwd` now sends any guest's `tcp dport 25` to a static chain
`smtp_drop`, before `jump guest_dyn`, so a blocked SYN is never counted
as egress: `smtp_drop` jumps to the hostd-owned chain `guest_smtp`, where
hostd keeps one `ip saddr <ip> counter name smtp-<guest_id>` per guest,
and then drops. The drop is the host's, not in hostd's rule, so a guest
whose rules hostd has not written yet (a crash mid-start) is blocked all
the same. 465 and 587 (authenticated submission to SES, Postmark, Resend,
Gmail) stay open: that is how an app in a guest should send mail, and the
terms say so. Guests have no IPv6 (the rule above drops all of it), so
there is no v6 path to close. hostd reads every guest's blocked counters
with one `nft list counters` per 60 s sample and exports
`repose_host_egress_blocked_total{reason}` (packets, summed per host) and
`repose_host_egress_blocked_guests{reason}` (guests whose count over the
last ten minutes passed the reason's threshold: 100 for smtp, about
fifteen blocked connects, each being about six SYNs). guest_id is never a
label (`internal/obs/metrics` refuses it), so the guest is named by the
`egress_blocked` log line hostd writes when it crosses, as with
`guestd_lost`. The EgressBlocked alert (warn) reads the gauge. A blocked
attempt is a number only: no address and no contents, in the metric, the
log or anywhere else; the privacy policy says so.
Found on the way: `AddGuestRules` skipped the egress rule whenever the
guest's counter existed, and a stop removes the rule but keeps the counter
until destroy, so every guest started again after a stop had no egress
rule. It now adds a counter when `nft list counters` lacks it and a rule
when the chain lacks one naming the counter, for the egress counter and
the three new ones alike (`TestGuestRulesReconcileAcrossRestart`).
*Rejected:* a per-guest opt-in to port 25 (nobody has asked; a
provider's submission port does the job); dropping in hostd's rule only
(a guest without rules would be open); counting in a dynamic nft set
keyed by address (addresses are reused by the next guest, so the
per-guest history would be wrong).

**I-239. A known cryptocurrency miner stops its guest automatically;
three stops in 24 hours hold the project until an operator clears it; the
pool ports are blocked; full CPU with nobody there for six hours is an
alert.** (anti-abuse, 2026-09-24) Four parts.
(a) Stratum ports. `guest_fwd` drops `tcp dport { 3333, 5555, 7777,
14433, 14444 }` through `stratum_drop`/`guest_stratum`, counted like
port 25 (threshold 30 packets in ten minutes: a miner retries its pool
every few seconds). These are the defaults mining pools publish
(3333/5555/7777 on most Monero pools, 14433/14444 on nanopool's). Left
open on purpose: 4444 (Selenium Grid's hub, which developers do reach
remotely), 8888 (Jupyter), 9000 and 9999 (too many ordinary services).
Pools also listen on 443 and 80, which are never blocked, so this catches
default configurations only; the name check is the real one. The list is
`repose.host.guestEgress.blockedTcpPorts`.
(b) The miner's name. guestd already sends each process's name (comm, at
most 15 bytes) and CPU every 60 s, the privacy policy already says so,
and the api receives them in `Samples` on the host stream.
`internal/abuse` holds the names (xmrig, xmr-stak, cpuminer, minerd,
ccminer, t-rex, nbminer, lolminer, gminer, teamredminer, phoenixminer,
ethminer, nanominer, srbminer, bzminer, onezerominer, rigel, kdevtmpfsi
and more), matched on the lower-cased name: by prefix for miners whose
forks add a suffix (xmrig-notls, SRBMiner-MULTI, cpuminer-opt), exactly
for names too short or too generic to be a prefix (rigel, miniz, xmr);
`minizinc`, `minikube`, `miner` and `node` do not match
(`miners_test.go`). guestd's watch list uses the same matcher, so a
throttled miner below the top 50 by CPU still reaches the api in any
spelling. The api's `abuse.Guard.OnSamples` runs after meter ingest: for
a running guest whose sample names a miner, under a row lock on the
project, it enqueues the ordinary stop op with `snapshot: true` (nothing
on the disk is lost) and `reason: abuse`, inserts an `abuse_events` row
(migration 0005: project, user, kind `miner`, `detail.process` = the
name, op id, `hold`), sets `last_error` to `abuse_stopped: stopped: a
cryptocurrency miner (xmrig) was running; mining is not allowed on
repose, see the terms at <dashboard>/terms`, records an `abuse_stopped`
platform event (the user's email and ntfy, subject "Your guest was
stopped: a cryptocurrency miner was running"), counts
`repose_api_abuse_stops_total{kind="miner"}` (the MinerStopped alert,
which is the operator's notification) and logs `abuse_stop` with ids and
the process name. It is idempotent: a project that is not
`running`/`starting`, one with an op open (this stop, still running), or
a sample older than the project's `started_at` (resent after a
reconnect) is left alone, so one miner is one stop however many samples
name it, and a restart that runs it again is stopped again. `repose
status`, `repose projects`, a command that needs the guest running, and
the dashboard show the sentence on a stopped project whose `last_error`
has the `abuse_stopped` code (a successful start clears `last_error`, as
before). The stop that makes three uncleared ones within 24 hours sets
`hold`; `POST /projects/:id/start`, and a restore with `start` from that
project's snapshots, then answer `403 forbidden` with `detail.reason:
"abuse_hold"` and a sentence naming the process and the terms, until
`repose-admin abuse clear <project>` clears every uncleared stop (the
hold and the strikes toward the next one), audited as `abuse_clear`.
`repose-admin abuse list [--all]` shows the stops. The user is never
suspended from here: `repose-admin users suspend` stays a human's
decision, as DESIGN §15 and the privacy policy say.
(c) Busy with nobody there. Every five minutes the api (every replica;
the queries only read) counts the projects whose samples over the last
six hours show every vCPU at 90 percent or more (host-side `cpu_ns` over
`samples * 60 s * the class's vCPUs`), at least 300 samples starting in
the window's first ten minutes, no SSH session, no tmux client and no
agent in any sample, and guestd answering in all of them (a sample with
guestd down says nothing about who is there). An agent that works for
hours with nobody attached is what repose sells, so an agent's presence
excludes a project outright. `repose_api_abuse_busy_unattended_projects`
is the BusyUnattended alert (warn, `for: 15m`); nothing is stopped, since
a runaway build looks the same. The same job now sets
`repose_api_egress_alert_projects` (over 1 TB in 24 h), which was
registered but never set, so EgressHigh could not fire, and
`repose_api_abuse_held_projects`.
(d) A renamed miner passes the name check. (a), (c) and the Abuse
dashboard's top-name panel are for that; a name is the cheapest certain
signal, not the only one.
*Rejected:* suspending the user on a match (the owner: the operator
decides; a false positive would cut off every project of a paying user);
killing the process inside the guest through guestd (a guest the user
controls can hide or restart it, and the stop with a snapshot is the one
path already safe for data); matching command lines or binary hashes
(arguments and paths are what the privacy policy promises never to read);
a hold that lapses by itself after 24 hours (the owner asked for the
operator to clear it).

**I-240. New outbound flows are rate-limited per guest, far above what
development does; flows over the limit are dropped and counted, open ones
are never cut.** (anti-abuse, 2026-09-24) A guest could scan the
internet or flood a target from the host's address at line rate.
`guest_fwd` now keeps an nft set `guest_flow_rate` (`ipv4_addr`, dynamic,
one-minute timeout) and sends the `ct state new` packets of a guest
address whose token bucket is past `limit rate over 200/second burst 2000
packets` to `flows_drop`/`guest_flows` (counted per guest; EgressBlocked
at 1000 dropped in ten minutes). Only new flows are metered: established
traffic is accepted above, so a download or an SSH session is never
touched, and flows to the host's caches and the gateway never reach the
forward hook. Measured on a large guest on host-01 (`e2e-abuse`,
destroyed after) with tcpdump of every outbound SYN, UDP datagram and
ICMP packet the host forwards, counted as conntrack would (a TCP
retransmit, or a UDP packet on a live 5-tuple, is not new): cold `pnpm
install` of the vite monorepo through the host npm cache 14 flows, peak
6/s; the same straight from registry.npmjs.org 79 flows, peak 74/s in
one second and 7.8/s over the busiest ten; `git clone --depth 1` of ruff
9, peak 8/s; cold `cargo fetch` of ruff 20, peak 8/s; cold `go mod
download` of terraform 16, peak 5/s; `docker pull` of node:22,
postgres:16 and python:3.12 through the host mirror 1; images from
ghcr.io, quay.io and mcr.microsoft.com 39, peak 13/s; cold `pip install`
of jupyterlab, pandas, scikit-learn and boto3 7, peak 6/s; `npm install`
of react-scripts, @angular/cli and aws-sdk with no lockfile (965
packages) 15, peak 12/s. The heaviest was not a package manager: headless
Chromium loading six ad-heavy news sites one after another opened 1114
flows, peak 196/s in one second and 68.7/s over ten; all six at once
1718, peak 152/s and 82.2/s over ten (a quarter of them DNS to 1.1.1.1).
So the sustained rate is 200/s, about 2.4 times a browser agent's busiest
ten seconds and 25 times any package manager's, and the burst of 2000
covers the six-site run whole. A scanner held to 200 new flows a second
does a small fraction of what it does unlimited, and the alert names it
within minutes. The rate and burst are
`repose.host.guestEgress.flowRate`/`flowBurst`; the host VM test runs at
20/s with a burst of 20 and checks that 30 flows at 10/s lose none, 300
at once lose 280, another guest's bucket is its own, and a connection
opened before the flood still sends after it.
*Rejected:* a lower rate (50/s would throttle a browser agent's busy
seconds); disconnecting or stopping the guest over the limit (a burst is
not abuse, and a sustained scan is a human's call); limiting only UDP or
only TCP (floods use both); a per-destination limit (a scan's signature
is many destinations, which a per-destination meter never sees).

**I-242. A feature without user docs is not done, and a test says so.**
(docs audit worker, owner, 2026-09-24) The owner's rule: no ghost
features that are not documented. An audit of every `repose` command and
flag, config.toml key, environment variable, exit code, dashboard page and
guest behaviour, and of I-149..I-241, against the 14 pages of /docs found
about 40 gaps and 6 wrong statements (listed in
`docs/workstreams/DOCS-AUDIT.md`): snapshot retention was "the seven
newest" where the code keeps 7 days plus the newest; project limits said
higher limits "aren't self-serve" where the first paid invoice raises them
to 10 (I-184); the `small` pricing example charged for the free first day;
yarn was said to use the npm cache (only v1 does); the dashboard was said to
show what `repose status` shows, ports included; secret names lacked the
leading-letter and 64-character rules. All are fixed in the docs. Three
code changes come with it. `repose resize SIZE` is no longer hidden (07
§5.6 hid it and documented it only in features/config.md): it is a working,
billed and irreversible action the dashboard offers too, so hiding it is
the ghost the rule is about. The `gateway` config.toml key is gone: it was
decoded and never read; a file that still sets it loads as before, since
unknown keys are ignored (the old shape stays accepted). Ctrl-C's 130 is
`ExitInterrupted` so the exit-code table can be checked. `repose __session`
stays hidden: it is the helper the CLI starts for itself and does nothing
typed by hand. `repose login --browser` and `--no-browser` stay visible and
are documented as what they are (a flow the hosted Logto app can't serve,
I-101, and a no-op kept for scripts). Enforcement: `internal/cli/docs_test.go`
walks the cobra tree and fails when a visible command has no heading or row
in `apps/web/src/content/docs/cli.md`, when a flag (long and short) is not
named with its command, when a hidden command or flag has no written reason,
when a `Config` toml key, an entry of `userEnvVars` (the new registry in
`env.go`; every `REPOSE_*` read in the package must be in it or in the
test's internal list) or an `Exit*` constant is missing from its section,
and the other way round when cli.md names a command, flag, key, variable or
exit code the CLI does not have. `docs/CHECKLIST.md` and `CLAUDE.md` carry
the rule. *Rejected:* generating cli.md from cobra (the page is written for
people, with examples and grouping a generator would lose; a check keeps
both); checking only that flag names appear somewhere on the page (a flag
documented under the wrong command would pass); leaving resize hidden and
allowlisted (the allowlist is for things no user should type).

**I-243. Every agent in the guest is told what the machine offers, from one
source, without a word written into the user's files.** (agent-guide worker,
2026-09-24) Agents on a machine did not know that their servers' ports
reach the user's laptop, that `nix profile add` installs now and `repose
config add` keeps, where secrets are, that port 25 is blocked, or that the
laptop is out of reach, so they guessed (tunnels, SSH keys, `apt`). The
guide is `nix/guest/base/agent-guide.md`: short, factual lines, each ending
in a comment naming the /docs section it summarises. `agent-guide.nix`
renders it (comments dropped; a line marked `needs: CMD` kept only when the
guest's system path has `CMD`, so `repose-notify`/`repose-ask` lines appear
with the command that serves them and never before) and installs it where
each agent reads global instructions: Claude Code's managed memory
`/etc/claude-code/CLAUDE.md` (the path the 2.1.280 binary builds as
`<managed dir>/CLAUDE.md`, managed dir `/etc/claude-code` on Linux); Codex's
system config layer `/etc/codex/config.toml` as `developer_instructions`
(a user-level `developer_instructions` replaces it, which is the user's
call); opencode's managed config dir `/etc/opencode/opencode.json`
`instructions`, which opencode unions with the user's list; Gemini CLI and
pi through an extension each (`~/.gemini/extensions/repose-machine-guide`,
`~/.pi/agent/extensions/repose-machine-guide.js`), symlinks into `/etc`
that `repose-agent-setup` makes on every start, because neither has a
system-level context file, Gemini refuses `@` imports from outside the
workspace, and pi's extensions can add a system prompt section. Nothing is
merged into `CLAUDE.md`, `AGENTS.md` or `GEMINI.md`; the carried
`~/.claude/CLAUDE.md` (I-196) stays the user's. Enforcement:
`internal/cli/agent_guide_test.go` fails when an H2/H3 of the machine,
agents or limits page is referenced by no guide line and not in its
allowlist with a reason, when a reference names a missing page or heading,
when a guide line has no reference, when a `repose ...` span names no CLI
command, and when another command the guide names is not in its
guest-provenance table (checked against the package list or module) or
marked `needs:`; it keeps `agent-guide.commands` current, which the
`guest-agent-guide` VM test runs `command -v` over on a real guest. That
test also runs all five agents against a stand-in model API and asserts each
one's first request carries the guide and the user's own instruction file,
with the user's files unchanged. `docs/CHECKLIST.md` requires a guide
update with a new guest capability. *Rejected:* a marked block merged into
each user file (the task allowed it as a fallback; no agent needed it, and
editing a user file is what the carry promises not to do); `SYSTEM.md` or
`GEMINI_SYSTEM_MD` (they replace the agent's own system prompt); pi's
`--append-system-prompt` in the wrapper (a flag on every invocation,
subcommands included); a system-level `includeDirectories` for Gemini (only
loads memory with `loadMemoryFromIncludeDirectories`, which would change
how the user's own include directories load).
**I-247. The laptop's ssh-agent is never forwarded; GitHub pushes go over
HTTPS with the carried gh login.** (round-3 CLI fixes worker, owner,
2026-09-24; supersedes the `ForwardAgent yes` kept by I-108 and I-150) The
owner: "there is no point in doing the vm then if we are exposing our
users to more danger. NO." With `ForwardAgent yes` in the generated
`~/.ssh/repose/config`, any process in the guest while the user is
attached (an agent running with `--dangerously-skip-permissions`, an npm
postinstall script) could ask the laptop's agent to sign with every key
it holds: GitHub, production servers, anything. Three layers now refuse
it. The CLI writes `ForwardAgent no` (explicit, so it wins over a later
`Host *` block with `ForwardAgent yes`; the Include sits at the top of
`~/.ssh/config`). The gateway answers `auth-agent-req@openssh.com` with
failure without forwarding it and rejects a guest's
`auth-agent@openssh.com` channel, so `ssh -A` and an old CLI's config get
nothing (`TestRelayRefusesAgentForwarding`). The guest's sshd has
`AllowAgentForwarding no`. The CLI rewrites the whole generated file on
its slow path, and the connect fast path (I-223) treats a file containing
`ForwardAgent yes` as not covering the project, so the first command after
the upgrade rewrites it (`TestSSHFilesCover`). Pushing still works for
GitHub: whenever gh's login travels, whatever the project's remote, the
guest's `~/.gitconfig` gets `url.https://github.com/.insteadOf` for both
`git@github.com:` and `ssh://git@github.com/` (each set by value with
`--replace-all` and a value pattern, so a second run adds nothing and
another value under the key stays) plus `!gh auth git-credential` as the
helper for github.com; the carry hash part changed so guests synced before
get the second rewrite on the next run. The condition used to be "the
remote is on github.com", which left a submodule or a repository cloned in
the guest to the forwarded agent. Other git hosts get two documented
options (public docs, secrets "Other git hosts"; features/secrets.md): an
HTTPS token as a named secret with a per-host credential helper and
insteadOf, or a deploy key generated in the guest for that one
repository. Interfaces: `ssh-gateway.md` (step 4 and 6, the CLI block) and
`guest-conventions.md` (the `.gitconfig` row) in this commit; the old
config shape still connects (only the agent request is refused).
*Rejected:* forwarding only while attached (the exposure is exactly while
attached); a confirm-each-use agent (`ssh-add -c`, which needs an askpass
on every laptop and still lets a guest ask); keeping forwarding for hosts
other than GitHub (the key used for GitLab is usually the same key).

**I-248. `repose run` with nothing new on the laptop attaches without
syncing instead of refusing a guest that changed.** (round-3 CLI fixes
worker, owner, 2026-09-24; narrows I-210's refusal) The owner ran
`repose run` in a checkout whose guest an agent had been working in and
got "The guest's working tree has uncommitted changes (27 files): ... Re-run
with --stash-remote ... or --discard-remote", and read it as run trying to
restart or rebuild the machine. With nothing new on the laptop there is
nothing to write over, so there is nothing to refuse. The sync now works
out the laptop's sync key (I-224) before deciding: when it equals the key
the guest recorded at its last completed sync and the guest has every
commit the laptop would send, the checkout is left alone whatever changed
there (uncommitted files, the agent's commits, another branch), only the
logins and carry go, and the run prints "The machine has changes your
laptop doesn't have (27 files); attaching without syncing. `repose run
--stash-remote` puts them in git stash and syncs your laptop's work." and
attaches. This also ends the detached checkout of the laptop's older
commit over a guest whose agent committed on the branch when the laptop
had nothing new. Only when the laptop has new work does exit 6 remain,
and its message now says what `run` does ("copies your laptop's work onto
the machine. It doesn't restart or rebuild anything"), what is on the
machine (eight paths without git's status letters, then "and N more"),
and the three choices, one per line, aligned. `--stash-remote` and
`--discard-remote` still force a sync. A guest with no recorded key (an
interrupted sync, a CLI before I-224) is refused as before.
`TestSyncLeavesTheGuestAloneWhenTheLaptopHasNothingNew`,
`TestSyncLeavesTheGuestsCommitsAlone`, `TestSyncRefusalSaysWhatRunDoes`,
`TestRunAttachesWhenOnlyTheMachineChanged`; the I-210 tests now add a
laptop edit before expecting a refusal. *Rejected:* a prompt ("sync
anyway?") (the run is often scripted, and the right answer with nothing
new is always "no"); skipping when the file lists merely differ (the key
already says whether the laptop moved).

**I-249. The command-not-found hint is the plain bash line plus two
aligned commands.** (round-3 CLI fixes worker, owner, 2026-09-24; the
layout of I-219) The owner: "this formatting is ass, No need for fancy,
just organized". The hint is now

```
air: command not found
  nix profile add nixpkgs#air  install it on this machine
  repose config add air        keep it on every rebuild (run this on your laptop)
Other packages with air: air-formatter
```

with the last line only when nix-locate found other packages. The first
line is what bash prints for any unknown command, so the hint reads as an
addition to it; the commands come first so they can be copied, and the
description column is aligned to the longer one. The guest-devtools VM
test asserts the exact lines.
**I-244. Agents message the owner with `repose-notify` and ask with
`repose-ask`; the answer comes back over the hostd channel.** (agent
questions worker, owner, 2026-09-24) An agent that needs a decision today
stops at a permission prompt or guesses; the owner hears "needs input" and
has to attach to answer. Two guest commands close that loop. They are
`repose-hook` under two more names (symlinks in the same package, so the
guest base gains no binary; `repose-hook notify|ask` are the same), which
keeps "repose" as the only prefix. `repose-notify TEXT` POSTs the hook
socket's new `/notify` route and becomes an `AgentEvent` of the new kind
`agent_message`, so an old hostd relays it unchanged and the api's outbox,
channels and 30-an-hour cap apply as they are; messages are exempt from the
60-second collapse, since two messages are two messages. `repose-ask`
POSTs `/ask`, which guestd answers with a UUIDv7 it chose, writes the
question to `/run/repose/questions/<id>.json` (root, 0600) and announces it
as the new `Question` notify, relayed by hostd as the `agent_question`
event; the ask then long-polls `GET /ask/<id>` (25 s at a time) and prints
the answer. The answer travels down as the new `AnswerQuestion` command
(api → hostd) and request (hostd → guestd). This design survives what it
has to: a hostd restart loses unacked events, so guestd re-announces every
open question on each new hostd connection and the api inserts by
question id; an api restart loses nothing, because questions are rows and
the delivery worker (grpc process, advisory lock 1010) picks every close
not yet acknowledged and sends it again with a fresh command_id every 30
s (a reused command_id would get hostd's stored failure back forever),
while guestd treats a repeat answer as a no-op; a guestd restart keeps the
tmpfs files and the ask reconnects for up to 2 minutes; a guest reboot
empties the tmpfs, the asker died with it, and a later AnswerQuestion is
`not_found`, which the api takes as final. The ask's exit codes are the
contract agents script against: 0 answered, 1 guestd unreachable, 2 usage,
3 timeout, 4 no channel on (the api closes the question at once rather
than let it wait out its timeout unseen), 5 cancelled (dismissed, the
guest stopped, or the question is gone), 130 interrupted (the question is
withdrawn with `DELETE /ask/<id>`). The agent is `--agent`, else
`REPOSE_HOOK_AGENT` (inherited by every shell an agent starts), else the
first of the five found by process name among the caller's ancestors
(`comm`, never arguments), else `shell`. Limits: 1 KB of text, at most 3
options of 64 bytes (ntfy shows three buttons), timeout 30 minutes by
default and 24 hours at most, 16 open questions per guest. The text is
tenant content: it reaches the owner's channels and the dashboard and is
never logged; every log line on the path carries ids, states, counts and
byte sizes only. *Rejected:* a guest long-poll to the api through the
edge's hook ingest (a second path to authenticate by source address, and
the edge is the secondary path by I-4); `Exec` to push the answer (an
operator-only, audited command carrying tenant text in argv); an ops-engine
op for the delivery (one op per project at a time would block a start
behind an unanswered question); separate `repose-notify` and `repose-ask`
binaries (two more packages for the same socket client).

**I-245. Questions are rows; the owner answers from ntfy, email, the
dashboard or the CLI, and the first answer wins.** (agent questions
worker, owner, 2026-09-24) Migration `0006_questions` adds `questions`,
keyed by the guest's id, with the text, up to three options, a state
(`pending|answered|cancelled|expired|no_channel`), the answer and where it
came from, the expiry, and the delivery bookkeeping (`deliver_command_id`,
attempts, next try, `delivered_at`, `delivery` ok|gone|given_up|guest).
Text and answer are stored as plain text, like `events.summary`: they are
what the owner is shown, not secrets, and no other tenant text is
encrypted, so encrypting these would protect nothing the summary column
does not already hold and would add a fourth place for key material. Each
question is also an `agent_question` event (inserted by a fixed id after
the row, so a failure between the two is repaired by the host's resend),
and its outbox rows carry, per channel, one signed reply link per option:
the token is the question id, the option index and the question's expiry,
HMAC-SHA256 under the unsubscribe key with a separate domain string, so
nothing is stored per link and no login is needed. The link is single use
because the question is: the first answer closes it, and every later click
says what the answer was. ntfy gets an `Actions` header in its JSON form
(escaped to ASCII, so an option with a comma needs no quoting), one `http`
POST button per option with `clear=true`, or a `view` button to the
project page for a free-text question; email lists the links, the
dashboard link, `repose reply <project>` and the expiry. A GET of a link
only shows the question and a button that POSTs, because mail scanners
open links and a GET that answered would answer for the owner; 20 tries
per question a minute. Routes: `GET /questions`, `GET
/projects/:id/questions`, `POST .../answer` (options enforced
case-insensitively, stored in the option's spelling; `409 conflict` once
closed), `POST .../cancel`, and the public `GET|POST /questions/reply`.
The dashboard shows pending questions on the project page; the CLI adds
`repose questions [PROJECT]` and `repose reply [PROJECT] [ANSWER...]`,
which lists instead of guessing when more than one is waiting. A guest
that stops cancels its pending questions; the worker expires a pending
question 30 s after its timeout (the guest reports its own first) and
gives up delivering after 25 hours. *Rejected:* answering by replying to
the email (inbound mail, parsing and spoofing, for a path the links
cover); a per-question random token stored hashed (a table of secrets for
what a MAC does statelessly); answering on GET (scanners); folding
questions into `repose status` (a question is an action item across
projects, and status is one project's state; `repose questions` lists
them all).
**I-246. The agents' browser is one headed Chromium on the desktop's
display, shared by both MCP servers over CDP, and the desktop only views
it.** (2026-09-24) The owner ran `repose open --desktop` and saw an empty
desktop: both MCP servers were registered with `--headless`, so each
launched a private headless browser and nothing an agent did could be
watched or taken over, while the homepage said "`repose open --desktop`
lets you watch". Two designs were measured. (a) MCP servers launch headed
browsers with `DISPLAY=:99` and Xvfb started on demand; (b) one long-running
headed Chromium on `:99` with a DevTools port, which both servers attach to.
(b) is chosen: the user's clicks and the agent share one window and one
profile (log in or solve a captcha once, the agent continues), both servers
see the same tabs, and there is one browser per guest instead of one per
server. `repose-browser.socket` listens on 127.0.0.1:9224 and its
`systemd-socket-proxyd` service pulls in `repose-browser.service`
(Chromium as dev, profile `~/.local/share/repose/browser`, DevTools on
127.0.0.1:9225, `--disable-gpu --enable-unsafe-swiftshader`), which pulls
in Xvfb (1440x900) and openbox (every window maximised); nothing runs until
the first DevTools connection. Playwright MCP is registered with
`--cdp-endpoint http://127.0.0.1:9224` and uses the browser's default
context (nixpkgs's wrapper forced an isolated in-memory context whenever no
user data dir was set; the overlay now skips that when a CDP endpoint is
given, with an assert that fails the build if the wrapper changes);
chrome-devtools-mcp with `--browserUrl` (its wrapper passed
`--executablePath`, which yargs refuses beside `--browserUrl`, so it is now
added only when the server launches its own browser; `--no-performance-crux`
is the default because CrUX lookups send the traced page's URL to Google).
Robustness, checked in guest-desktop: the browser killed with SIGKILL
stops the unit and the proxy (`BindsTo`), the next call through a still
running MCP server restarts both through the socket with the same profile
(both servers reconnect when `browser.connected` is false); the crashed
flag in the profile is reset so no restore prompt covers the page. The
browser runs in the system slice `repose-browser.slice` with the same
37.5 percent `MemoryMax` as the user slice (1.5 GB small), `OOMPolicy=continue`
so a renderer killed at the limit is a crashed tab, not a dead browser.
The viewer (x11vnc, noVNC) wants the browser, so `repose open --desktop`
always shows it; `--stop` stops only the viewer; Xvfb and openbox are
`StopWhenUnneeded`. The idle check stops the viewer after 30 minutes
without a client (as before) and the browser after 30 minutes with no
client on 9225 and no viewer (an MCP server keeps its connection for the
whole agent session). noVNC's `defaults.json` sets `resize=scale`.
Measured in the VM test (1440x900, one static page, 3 GB guest): headed
Chromium 155 MiB anonymous (554 MiB charged including page cache, which is
reclaimable and was first read by this cgroup), headless 145 MiB anonymous;
Xvfb 9 MiB, openbox 3 MiB; idle CPU over 30 s 1.01 s headed plus Xvfb
against 0.75 s headless. Live on e2e-r3-desk (small, old base, the same
flags started by hand): browser 427 MiB PSS, Xvfb 22, openbox 6, x11vnc 14,
websockify 13. So a guest that browses pays about 25 MiB more than before
and a guest that never browses pays nothing. Without `--disable-gpu` the
GPU process failed EGL initialisation and relaunched 29 times at startup;
with it, none, and WebGL still works. `DISPLAY=:99` stays exported into new
shells while `/tmp/.X11-unix/X99` exists, which is now whenever the browser
or the viewer runs: Playwright, Puppeteer and Cypress default to headless
whatever `DISPLAY` says, so project test suites stay headless, and a headed
browser someone asks for appears on the desktop instead of failing. A
guest's existing `~/.claude.json` entries with the old `--headless` shape
are replaced by `repose-agent-setup` (`/etc/repose/mcp.json` lists them
under `repose_retired`; an entry the user changed is kept); agents already
running keep their headless browser until restarted. The CLI never
auto-forwards 9224 or 9225 (a laptop process must not drive the guest's
logged-in browser); an older CLI will forward them while attached until
upgraded, which is why they are not 9222, the port a user's own Chromium
uses. `repose-guest-profile`'s `desktop.running` and `desktop status` now
mean the viewer (`repose-x11vnc`), since the display can be up for the
browser alone. *Rejected:* (a) (two browsers per agent session, no shared
logins, the user's clicks land in a browser the agent's next launch
discards); headless by default with a headed switch when the desktop opens
(needs an agent restart or a second browser, the page the agent is on is
lost); binding the DevTools endpoint to 127.0.0.2 so old CLIs skip it
(headed Chromium ignores `--remote-debugging-address`); a user unit named
`repose-desktop` for pre-0.1.12 CLIs (they would forward without printing
the password; the fix is the CLI upgrade).

**I-256. Vercel and portless stay menu entries, voice mode is not a
repose feature, and a quick path to production stays deferred.** (conductor,
owner, 2026-09-25; items 1, 2 and 5 of
`docs/proposals/2026-09-24-backlog-triage.md`) The owner approved the
proposal's recommendation on all three. (1) `vercel` and `portless` remain
opt-in entries in the menu's `deploy` group, and the tools carry (I-221)
already reinstalls them when the laptop has them globally; nothing is added
to the base. portless gives each app a random port behind a local HTTPS
proxy with its own CA, which per-port forwarding cannot present on the
laptop; if its users ask, the answer is preview URLs, not a base change.
(2) Claude Code's voice dictation needs a local microphone and does not
work over SSH, so repose builds nothing for it; the user's OS dictation on
the laptop types into the terminal, and the Claude app's Remote Control
works once the user has logged in inside the guest. (3) Repose does not
host production. Deploying to the user's own Vercel, Coolify or Fly stays
deferred (proposal 2026-09-23 item 13) until invited users ask for it; the
menu already has wrangler, supabase, flyctl, vercel and, since I-251,
cloudflared. *Rejected:* portless or vercel in the base (a few hundred MB
per guest for tools most projects never call); a microphone relay from the
laptop into the guest (a device channel into every guest for a feature the
OS already offers).

**I-257. The terms say a machine is not for serving production traffic
to others.** (conductor, owner, 2026-09-25; item 15 of
`docs/proposals/2026-09-24-backlog-triage.md`) Nothing technical stops
someone running Coolify, a public tunnel or a small site from a guest, and
nothing will: there is no inbound path (R4-6), egress past 500 GB is
billed, and there is no stable address. The owner chose to write the rule
down anyway, so that a guest quietly serving a live product is plainly
outside the terms. The acceptable-use list in
`apps/web/src/content/legal/terms.md` and its summary in
`/docs/limits` gain one line: don't serve production traffic to other
people. Showing work in progress to someone through a tunnel or a
forwarded port is still development and stays allowed. *Rejected:*
blocking tunnels such as cloudflared or ngrok (they are how a preview gets
shown until preview URLs exist, and I-251 adds cloudflared to the menu);
metering inbound tunnel traffic (repose cannot tell a demo from a product
without reading traffic, which it never does).
**I-252. `repose paste` sends the laptop's clipboard image to the guest
and pastes its path; one direction, no socket.** (2026-09-25;
backlog triage item 3, owner-approved; dev-ergonomics item 8 had
rejected a clipboard socket into the guest.) `repose paste
[PROJECT] [--window NAME] [--print]`:

- *Clipboard.* A PNG from `pngpaste -`, else `osascript` writing `the
  clipboard as «class PNGf»` to a temp file (macOS); `wl-paste` when
  `WAYLAND_DISPLAY` is set, else `xclip` when `DISPLAY` is (Linux, the
  offered types listed first so "no image" and "tool failed" read
  differently). Windows is refused with a pointer to `repose cp`; WSL is
  Linux, with a hint that a Windows-side image may not be on its
  clipboard. The reader is an interface so tests need no clipboard.
- *Transport.* One `runSSH` over the project's ssh target, the
  ControlMaster `repose cp` rides, with the image on stdin: no scp, so
  no SFTP-versus-legacy quoting, and saving, cleanup and the paste are
  one round trip.
- *Where and how long.* `/tmp/repose-paste/<UTC yyyymmdd-hhmmss-ms>.png`,
  umask 077 (directory 0700, file 0600, `dev`'s). /tmp is shared, so a
  directory that is a symlink or not `dev`'s is refused, not followed.
  Each paste first deletes pastes over a day old and all but the newest
  50; one image is capped at 20 MB (a 5K screenshot is about 15). /tmp,
  not the home volume: pastes are throwaway and must not reach snapshots.
- *The paste.* `tmux set-buffer` + `paste-buffer -p -d` into the active
  pane of the session's current window (`=<slug>:`), or of `--window
  NAME`. `-p` makes it a bracketed paste when the program asked for one,
  which is how a terminal delivers a dropped file and what Claude Code
  attaches as an image; `send-keys -l` (the task's first sketch) types
  key by key, which a TUI may not treat as a paste. No Enter: the user
  adds words. The pane is found with `list-panes`, because
  `display-message` falls back to the current pane for a missing window.
  A missing window leaves the file and prints its path, exit 1.
  `--print` only prints the path.
- *Exit codes.* No new code: no image, not a PNG, too large, no tool,
  Windows are exit 1 with a message naming the tool to install; 4 and 5
  as every project command. Nothing a script branches on needs a code
  of its own, and the codes are a shared interface.
- *Key binding.* Documented for kitty (`launch --type=background
  --cwd=current`) and WezTerm (`background_child_process`), not shipped;
  none for iTerm2 or Ghostty, whose key actions do not run a background
  program on the laptop that we could verify.
- Nothing is logged; the image and the path stay between the laptop and
  the guest.

*Rejected:* a clipboard socket or `xclip` shim in the guest (anything in
the guest could read the laptop's clipboard while attached); the CLI as a
pty proxy catching Ctrl-V (real work; revisit if the explicit command is
used); typing Claude's `@path` (a pasted path is what drag and drop
sends, and works for every agent that opens files).
**I-250. Claude Code in a guest starts in `bypassPermissions` unless the
user set another default.** (round-4 worker A, owner, 2026-09-25; backlog
triage item 11) `/etc/repose/claude-settings.json` now carries
`permissions.defaultMode: "bypassPermissions"` and
`skipDangerousModePermissionPrompt: true` beside the repose-hook entries.
A guest with no `~/.claude/settings.json` gets the whole file from
`repose-agent-setup claude` (the wrapper runs it at every start). An
existing file, which every guest made before this base has and which the
I-196 carry may have written before Claude first ran, gains both keys only
when it has no `permissions.defaultMode`, and
`skipDangerousModePermissionPrompt` only when that is unset too; a file
whose `permissions` is not an object is left alone. So existing guests get
the default at their first Claude start after the base update, and a
`defaultMode` the user set in the guest or carried from the laptop is never
touched: the I-196 merge is still the guest file under the laptop file
(`$G * $L`), so a laptop `defaultMode` wins, and with none the guest's
value stays. The merge itself is unchanged; it reads only `hooks` from the
platform file, which the goldens now show with the new platform file
(`mode-kept`, `mode-laptop-wins`). Opting out is setting another mode
(`default`, `acceptEdits`, `plan`, `auto`); deleting the key brings the
default back at the next start, so the docs tell users to set a mode.
Verified against Claude Code's docs (permission-modes.md and
settings-reference.md, fetched 2026-09-25; the base has 2.1.280): the
setting is `permissions.defaultMode` with value `bypassPermissions`,
honoured from user, `--settings` and managed settings and ignored from a
project's `.claude/settings*.json` since 2.1.257;
`skipDangerousModePermissionPrompt` is a top-level boolean, scope "User,
local, or managed", which Claude Code itself writes to user settings when
the warning dialog is accepted; the mode is refused as root or under sudo,
and `dev` is not root; deny rules, explicit ask rules and `rm`/`rmdir` of
critical paths still apply. On Pro, Max and Team, Claude Code asks once
whether to change a non-auto `defaultMode` to auto; the user docs say to
answer no to keep bypass. The other agents, not built: Codex needs two
keys (`approval_policy = "never"`, `sandbox_mode = "danger-full-access"`,
from its config reference) in `config.toml`, which the setup edits only by
prepending `notify`, and doing it without clobbering a user's value would
need a TOML-aware merge; Gemini CLI's `general.defaultApprovalMode` accepts
`default`, `auto_edit` and `plan` only, and YOLO is command-line only
(`--yolo`), so it would take a wrapper flag the user could not turn off
from config; opencode already allows most tools by default (`doom_loop`
and `external_directory` ask), and `"permission": "allow"` in
`opencode.json` would allow the rest, but that file may be JSONC, which the
setup's jq cannot parse; pi was not checked. The user docs keep Codex's two
keys and point at each agent's own docs for the rest. *Rejected:* managed
settings (`/etc/claude-code/managed-settings.json` or `.d/`), which outrank
user settings, so a user who wants another mode could not have it; setting
the mode only in the carry merge (a guest never reached by `repose run`, or
a CLI older than this, would not get it, while the wrapper runs at every
start); adding the keys whatever the user has (overwrites a chosen mode); a
wrapper `--permission-mode` flag (outranks settings, so the user's default
would lose).

**I-251. cloudflared is a menu entry in group `deploy`.** (round-4 worker A,
owner, 2026-09-25; backlog triage item 14) A project's ports have no public
URL, and the machine docs already pointed at `cloudflared` for showing a
running app. The entry is `pkgs.cloudflared` in `home.packages`, like
wrangler and flyctl (nixpkgs carries it under Apache-2.0, so the unfree
allowlist is unchanged), added with `repose config add cloudflared` or the
dashboard menu. The machine docs now give the quick tunnel command
(`cloudflared tunnel --url http://localhost:3000`) and its limits from
Cloudflare's TryCloudflare page: a random `trycloudflare.com` URL, no
account, at most 200 in-flight requests, no server-sent events, testing
only, and it does not work while `~/.cloudflared/config.yaml` exists; a
named tunnel on the user's own domain is the stable option. *Rejected:*
cloudflared in the base (most projects never need a public URL, and the
menu makes it one command); ngrok (unfree, and needs an account for any
tunnel).
**I-253. Any number of agent windows in one guest, and `repose run
--worktree` puts one in its own git worktree beside the checkout.**
(round-4 worker C, owner-approved backlog item 12, 2026-09-25; extends
R3-14 + R4-10) `windowNameFor` returned `<agent>` or `<agent>-2`, so a
third prompt opened a second window also named `claude-2`. It now picks
the lowest free `<agent>-N` (N >= 2, no cap), so a closed window's number
is handed out again. Everything that reads window names already took any
digits: guestd's `sample.AgentOf` (states, hooks, OOM protection, I-213),
`repose-hook`, questions and the dashboard carry the name as an opaque
string. The shared-working-tree warning now fires when any other window of
that agent is open (not only the bare name) and points at `--worktree`.
`--worktree` (needs a PROMPT, exit 2 without one; allowed for the first
window too, since "this agent gets its own tree" does not depend on
whether another is running) makes `git worktree add -b repose/<window>
~/<slug>-<window> <HEAD>` in the guest and opens the window there. Where:
a sibling of `~/<slug>`, never inside it, so the sync's `git status`,
I-210 fingerprint, stash, `--discard-remote` and untracked tar see
nothing, and the guest does not look dirty (a nested worktree would show
as an untracked directory and be refused or stashed). The sync's
`for-each-ref` tips do include the `repose/*` branches; unknown tips are
only ever excluded from the bundle, and I-248's "the guest has every
commit the laptop would send" is unaffected, so they neither dirty the
guest nor travel to the laptop. Branch: `repose/<window>` from the
checkout's HEAD commit, whatever branch it is on; uncommitted changes in
the checkout are not in it, and the CLI says so when the checkout is
dirty (carrying the diff would be a second sync with its own conflict
cases). Later runs: never reuse. The name skips any N whose window,
`~/<slug>-<window>` path or `repose/<window>` branch exists, so every
`--worktree` run starts clean from HEAD and never lands a new prompt on
an old attempt's files. Refusals, exit 2 like the laptop's "not a git
checkout": `~/<slug>` without `.git` (possible only with `--no-sync`) and
a checkout with no commit. Cleanup: the user's, documented as `git
worktree remove` plus `git branch -D`; no command. The probe (windows,
HEAD, dirty, existing dirs and branches) is one ssh and the `worktree add`
a second, only on `--worktree`. The worktree has no `node_modules` and no
carried `.env` (gitignored files are not copied); /docs says so. Claude
Code asks whether to trust a folder it has not seen, and each worktree is
a new folder; whether that dialog eats the prompt the CLI types is to be
checked live (the same holds for a fresh `~/<slug>`).
`TestPromptSendAndSecondWindowNaming` (cat, cat-2, cat-3, a closed cat-2
reissued), `TestPickWindowHasNoCap` (claude-10), `TestRunWorktree` (pane
path, sync with a worktree present leaves it alone and the probe clean,
dirty notice, no reuse, cleanup), `TestRunWorktreeRefusals`,
`TestRunWorktreeThenPlainRun`. *Rejected:* worktrees inside the checkout
(`.worktrees/`, needs an ignore rule in the user's repo or the sync sees
them); under `~/.repose/` (hidden from the user who has to merge them);
reusing a worktree whose window closed (a new prompt on stale work, and
"which one did I get" is not visible); `repose worktree list/remove`
(git already has both); worktrees by default (R4-10 still holds: most
second prompts are follow-ups on the same tree).
**I-254. `repose fork`: one snapshot, N new projects created in one api
transaction, each its own machine.** (fork, 05/07, 2026-09-25; owner
approved backlog triage items 10 and 12) N agents trying N approaches from
one starting point, each with full permissions on a machine of its own,
built on the snapshot and as-new restore that exist. CLI: `repose fork
[PROJECT] [-n/--count N] [--name NAME] [--size S] [--snapshot ID]
[--prompt TEXT [--agent A]] [--json]`. PROJECT is the argument, as for
every command whose object is a project (I-155), so the prompt is a
flag, not `run`'s positional; the same prompt to every copy is best-of-N,
and a different prompt per copy is `repose attach` or `repose run
--project X --no-sync "..."`. Count is 1 by default and at most 10.
The source is a manual snapshot taken now (a running guest's snapshot
freezes it for under a second, I-171, and the source keeps running),
found from the snapshot op's `result.snapshot_id` (the api has always set
it; api.md now says so), or the newest snapshot for an api that omits
it; `--snapshot ID` takes one of the project's own instead. Copies are
named `<slug>-fork-<k>`, or `NAME-<k>`, with `k` the lowest numbers no
live project uses, trimmed to fit a 40-character slug; always numbered,
also for one copy, so the rule has no exception. api: `POST
/projects/:id/fork {snapshot_id, count, name?, class?, start?,
request_id?}` → `202 {snapshot_id, snapshot_created_at, from_project_id,
projects: [{project_id, name, slug, class, op_id}]}`, a new route (nothing
old changes shape). An endpoint rather than N calls to `POST
/projects/restore`, because the owner's rule is that a fork past the
project limit creates nothing, and only one transaction holding the
user's row lock (the lock create and restore take) can check the limit
for all N and create them; the CLI's own look at `GET /me` and `GET
/projects` first only spares a snapshot the api would refuse. Past the
limit the answer is the existing `400 invalid` with `detail: {limit,
projects, requested}`, past the xl limit `{xl_limit, xl, requested}` (an
xl source forked twice would otherwise make three xl projects; the
as-new restore still does not check the xl limit, which is a separate
gap). Idempotent: the CLI sends a random `request_id`, stored in each
restore op's params (`fork_request_id`, beside `fork_of`); a request
whose id is already there answers with those projects, and the CLI
resends with the same id while the api is away (502/503/504 or no
connection, the 30 s budget of an op wait), so a redeploy mid-request
never makes 2N. No migration. Partial failure: all N rows exist or none
do, but each restore is its own op on its own host placement, so one can
fail (capacity, a host) while the others run; the CLI waits on each,
lists every copy with its state or the api's `last_error`, exits 1 with
`N of M forks did not start`, and says the failed copy is a project to
destroy and that `repose fork X --snapshot <id>` makes another from the
same snapshot. A copy gets the source's class (or `--size`), volume size,
zone, agent, base and configuration revision with its closure, through
the code the as-new restore already used (now `insertRestored`, shared),
and the source's named secrets: the ciphertext rows are copied in the
same transaction (same user key, bound to the same names, same table, so
no new home; the lowercase sshd material is not copied, every guest gets
its own, I-3). Secrets live on a tmpfs, not on the disk the snapshot
copies, so without this an agent in a copy would lack the keys the
source's agent had, and "same starting point" would be false. A copy has no
`remote_url`: the source is live and keeps its remote (the rule the as-new
restore follows), so `repose run` in the checkout still means the source,
and no `by_dir` entry is written. Getting back is plain: `repose
projects` lists the copies (the names say where they came from), `repose
attach <copy>` gets in, work comes back through git (commit in the copy,
push a branch, fetch on the laptop; the copy's disk has the origin remote
and the gh login `run` carried into the source), and `repose destroy
<copy>` removes the rest. After the forks run, the CLI closes any ssh
master left under a reused name and renews the SSH certificate, as
`restore` does. With `--prompt`, the agent starts in each running copy
through `run`'s own code with `--no-sync --no-attach`. Billing: each copy
is a project and is billed like one; the CLI and the docs say so. Logins
made inside the source (Claude Code's included) are on its disk and so in
its snapshot and in every copy, exactly as with `restore --as-new` today;
whether that is "copying" Claude credentials is the open question of the
Claude-login proposal (2026-09-24), which would take the file off the disk,
and is the owner's to settle, not this entry's. Tests: `TestFork` (api,
Postgres: result.snapshot_id, missing snapshot_id, count 0, someone else's
snapshot, over the limit with nothing created and the detail, the xl
limit, two copies named -1/-2 running without the remote with the secret
and without shared sshd rows, the source unchanged, the resend answering
the same two and creating none, lowest-free numbering, a long name
trimmed, `start: false`, another user 404), `TestFork` (CLI against the
fake: the summary, the prompt to each copy with the agent, the limit
refused before a snapshot, `--count 11`, `--snapshot` + `--name` +
`--size` + `--json`, an unknown snapshot creating nothing),
`TestNewRequestIDIsAUUID`. Not built, on purpose: fork lineage in the api
(a `forked_from` column; the names carry it), a fork list or a "keep
this one" command, promoting a copy to own the checkout's remote (a
`PATCH remote_url` someone can design when asked), a fork of a destroyed
project, per-copy prompts, `--no-start` in the CLI (the api has `start`),
and the dashboard (a Fork button needs a count field, the limit message
and the N-op progress; not trivial, so not done). *Rejected:* CLI-only
orchestration of N restores (cannot refuse the whole fork at the limit
without racing another create, and leaves 1 of 3 on a failure halfway);
the api taking the snapshot itself (a restore op waiting on a snapshot op
is a dependency the engine does not have, for a step the CLI already
does in one call); a positional PROMPT (conflicts with I-155's
positional PROJECT); unnumbered names for one copy.

**I-255. A volume set up under another slug links its old checkout to
the new name.** (fork, 04, 2026-09-25) The guest's checkout is
`/home/dev/<slug>`, and the tmux session, `run`'s sync and every agent
window start there. A volume restored into a project with another slug
(every fork, and every `restore --as-new NEWNAME` since R3-6) had its
code at `~/<old slug>` while `SetupProject` created an empty, git-inited
`~/<new slug>`, so the user attached to an empty repository. guestd's
`SetupProject` now reads the slug the volume's
`/home/dev/.repose/project.json` named before rewriting it, and when
`~/<slug>` does not exist and `~/<old slug>` resolves to a directory
inside the home, makes `~/<slug>` a relative symlink to it (to the real
directory, so a fork of a fork does not chain links), instead of a new
directory. A symlink rather than a rename, because absolute paths inside
the checkout keep working: a virtualenv's scripts, a compose file's bind
mount, the agent's own session history under the old path. Idempotent:
on every later start `~/<slug>` exists and nothing changes; a dangling
link (the user deleted the old checkout) is replaced by an empty
directory; a `project.json` naming a slug that is not a directory of the
home (a link out of it, `..`) links nothing. The directory is made
before `project.json` is rewritten, so a guestd killed between the two
still finds the old slug next time. The log line gains `dir_linked`.
guest-conventions.md and vsock-guestd.md say so in this commit.
`TestSetupUnderANewNameLinksTheOldCheckout`,
`TestPreviousCheckoutStaysInTheHome`. Needs a base publish; a copy of a
project whose base predates it gets the empty directory until its base
is bumped (its guestd comes with the copied closure). *Rejected:* moving
the directory (breaks the absolute paths above); telling the api the old
slug in `SetupProject` (the volume already knows it, and hostd would need
a new field for the same answer); the CLI fixing it over SSH after the
fact (the tmux session has already started in the empty directory).

**I-258. The sync keeps the laptop's split between staged and unstaged
work.** (landing page review, 2026-09-26; the owner asked why edits that
were unstaged on the laptop showed as staged on the machine) The sync
sent one patch, `git diff HEAD --binary`, and applied it with `git apply
--index`, so every tracked change arrived staged whatever its state on the
laptop. It now sends two: `git diff --cached --binary` (HEAD to index),
applied with `git apply --index`, then `git diff --binary` (index to
working tree), applied with plain `git apply`, and `git status` on the
guest lists the same files as staged and unstaged as the laptop's. The
file contents are what they were before. The sync key (I-224) hashes both
patches and its version moves to `sync-2`, so no guest skips the first
apply of the new shape. The synced-tree fingerprint (I-210) is the tree
`git add -A` would record, which does not depend on the index, so a guest
left exactly as the last sync left it is still not dirty.
`TestSyncKeepsStagedAndUnstaged`. Interfaces: none;
`features/sync-at-launch.md` and `07-cli.md` §5.5d say so in this commit.
*Rejected:* keeping one patch and resetting the index afterwards (loses
what the owner did stage); `git stash create` on the laptop and applying
the stash on the guest (needs the stash's objects in the bundle and a
stash entry the user never made).

**I-267. User SSH certificates last 24 hours.** (feature research round,
2026-09-26; the owner asked for it) R3-9 set 12 hours, so someone who
opened an editor over Remote-SSH in the evening got `Permission denied`
the next morning until a `repose` command renewed the certificate. 24
hours covers a working day plus the night. `sshca.UserCertTTL` is now 24
hours; the fake api signs with the same constant. Revocation is unchanged:
`repose logout` from another device still revokes a lost laptop's
certificates at once, and the gateway still checks the revocation list,
so the longer lifetime only matters for a stolen laptop nobody logged out.
The CLI's reuse margin (30 minutes, `cert.go`) is unchanged. hostdev's
own 12-hour certificates are a dev tool and stay. Interfaces:
`ssh-gateway.md` says `now+24h`; nothing parses the lifetime, so there is
no old shape to keep. /docs (`run-and-attach`, `troubleshooting`, `cli`),
the terms ("within twenty-four hours"), DESIGN, SECURITY and RUNBOOK say
so in this commit. Needs an api redeploy; existing 12-hour certificates
run out on their own. *Rejected:* a `ProxyCommand` that renews on every
connection (more code for the same morning; the research ranked it low).
**I-262. An idle running machine is announced, never stopped.**
(feature round, 2026-09-26; research report breakage 2: a forgotten
`large` machine bills up to its $99 cap plus disk) R1-5 stands: repose
does not stop a machine for being idle. It now says so, from the signals
R1-5 had recorded since day one (`meter_samples.ssh_sessions`,
`tmux_clients`, `agents`, `guestd_ok`, one row a minute per guest). No
new signal, no guestd or hostd change and no migration. A `running`
project is idle when, counted from `started_at`, no sample for 24 hours
showed an SSH session, a tmux client, an agent in a state other than
`idle` or `needs_input`, or guestd not answering, and its newest sample
is at most 10 minutes old (an unreachable host, or a gap in samples, is
not evidence that nobody is there). The brief said "no agent process";
this counts an agent at its prompt as not working, because the machine
people forget is the one whose `repose run` left Claude open at its
prompt, and a warning (unlike a stop) costs nothing when it is wrong.
Surfaces: `idle: {since, hourly_cents}` on the Project of GET /projects
(api.md, added in this commit; absent means not idle, so older clients
and older apis agree); a line under `repose projects` and in `repose
status` ("idle 26h, billing ~$0.14/h; `repose stop <slug>` stops it");
one line on `run` and `attach` naming the other idle projects, once per
idle stretch, remembered in `~/.config/repose/idle-noted.json`
(cli-config.md), skipped on the attach fast path because that path makes
no api call (I-223); the dashboard's project list shows "idle 26h ·
~$0.14/h" under the state; and one `idle_running` event per idle
stretch, which goes through the ordinary outbox to email and ntfy under
the user's existing settings and unsubscribe link (title `<project>:
idle, still billing`). The warner runs on the hourly rollup tick under
its advisory lock; "once" is `no idle_running event with ts at or after
the stretch's start`, so a restart, a second replica or a resent tick
never warns twice, and a machine that is used and goes idle again warns
again. GET /projects looks back at most 14 days for the last use, so a
machine up for months costs a list two weeks of its samples and shows
"idle 14d" at most; the warner looks back to `started_at`, where a
stretch's start does not slide. The event summary carries the slug,
hours, class and price only.
`TestIdleRunningProjectWarnsOncePerStretch`, `TestProjectJSONIdle`,
`TestSince`, `TestIdleOthersNoteOncePerStretch`,
`TestRunMentionsOtherIdleProject`. Needs an api redeploy and a CLI
release; the dashboard ships with the web app. *Rejected:* an opt-in
`idle_stop` (overrides R1-5; the owner's call, research report question
2); a new guestd "last attach" signal (the minute samples already say
it); a daily repeat of the email (a nag the user cannot silence short of
turning email off); a column in the `projects` table (every row would
pay for a rare state).
**I-263. Submodules travel with the sync, their commits bundled from the
laptop like the superproject's.** (feature round, worker D, 2026-09-26; a
fact-check found the carry never recursed into submodules and nothing ran
`git submodule update` on the guest, so a submodule arrived as an empty
directory and edits inside one never travelled) Every submodule the
laptop has checked out (a `.git` in its directory), nested ones
included, parents first, is carried the way I-150 carries the
superproject: the probe lists each checked-out submodule of the guest's
checkout with the commits its refs and `HEAD` point at; the laptop
bundles its submodule's `HEAD` minus what the guest's copy has; the apply
`git init`s the directory when needed, `git bundle unbundle`s, checks out
the laptop's submodule `HEAD` (its branch created or fast-forwarded when
the laptop is on one, detached otherwise, an agent's diverged branch left
alone), records that commit in `refs/repose/laptop-head` (so the next
probe finds it after an agent's commit moves `HEAD`, and gc keeps a
commit only the laptop has), applies the staged and unstaged diffs as
I-258 does, and runs `git submodule init` and `sync` for it, so its
`origin` is what `.gitmodules` names, resolved against the guest's
origin. A laptop submodule `HEAD` that differs from the recorded commit
arrives as it is, so `git status` reads "new commits" on both sides. The
superproject's diffs now pass `--ignore-submodules=all` (a "Subproject
commit" hunk does not apply to a checkout), and a staged submodule
change, addition or removal is set with `git update-index --cacheinfo`
/ `--force-remove` in each repository. Untracked files of every
submodule join the one untracked tar, so I-194's limits and skipped
directories count them together; gitignored `.env` files in submodules
join I-197's carry. The summary's modified count counts files inside
submodules and no longer counts a submodule as a file. I-210's
fingerprint no longer fails on any submodule change: it appends each
checked-out submodule's own `HEAD` and `git add -A` tree, so the tree a
sync left with submodule edits is still recognised as the last sync's,
and `--stash-remote`, `--discard-remote` and the last-sync stash (with
its prune) run in every submodule before the superproject. I-248 counts
submodule commits in "nothing new". Both the laptop and the guest skip
the listing when a repository has no `.gitmodules` (the laptop also
reads `.gitmodules` in `HEAD` for a staged removal), so a repository
without submodules pays a stat. The sync key hashes all of it and its
version moves to `sync-3`. A shallow laptop submodule cannot be bundled:
the guest runs `git submodule update --init` for it itself
(`GIT_TERMINAL_PROMPT=0`, ssh in batch mode, so nothing prompts), which
works for github.com with the carried gh login; a failure prints
`Submodule <path> is empty on the machine: ...` with git's last line
and the run goes on, and edits inside a shallow submodule are not sent,
with a warning naming `git -C <path> fetch --unshallow`. A submodule the
laptop never checked out stays empty in the guest. Not done: I-203's
GitHub clone for a large submodule's first sync (the laptop sends the
whole history); the laptop's `origin/<branch>` refs inside submodules.
`TestSyncCarriesSubmodules`, `TestSyncNestedAndNewSubmodulesIntoAnEmptyGuest`,
`TestSyncLeavesAnAgentsSubmoduleCommitAlone`,
`TestSyncShallowSubmoduleFailsSoftly`,
`TestSyncedFingerprintCoversSubmodulesUnderPOSIXSh`,
`TestEnvCarryIncludesSubmodules`. Interfaces: none (CLI and stock git on
the guest; no guestd or base change). `features/sync-at-launch.md`,
`07-cli.md` §5.5 and the public `sync.md` say so in this commit.
*Rejected:* `git submodule update --init --recursive` on the guest for
every submodule (needs credentials for each remote, which the guest has
only for github.com, and loses unpushed submodule commits, the reason
I-150 bundles); carrying the submodule's files as a tar (no history, and
an agent could not commit in it); keeping I-210's "any submodule change
fails the fingerprint" (every sync that carried a submodule edit would
refuse the next run with new work as an agent's).
**I-260. `repose resize --size` changes a project's class, and every
start carries the class to the host.** (feature round, 2026-09-26; the
research report's breakage 8: small OOMs on real stacks, and the class
could not be changed from the CLI) The api already accepted `class` on
`PATCH /projects/:id` while the project is stopped. The CLI shape is
`repose resize [DISK] [--size small|large|xl] [--yes|-y]`: `resize`
already means "give this project more", `--size` is the flag `run` and
`fork` use for the class, and the disk argument (renamed `DISK` in the
usage from `SIZE`, which read as the class) stays as it was, so both can
go in one command. A stopped project is patched and starts at the new
class; a running one is stopped with a snapshot, patched and started
again, after a `[y/N]` question naming what the stop ends (agents
included), which `--yes` skips and which without a terminal is exit 2,
as `destroy` does. The same class prints "already" and does nothing; a
PATCH refused after the stop (the xl limit) starts the project again at
its old class before reporting why. The CLI prints the class's vCPUs,
memory, hourly price and monthly cap from its own table
(`classSpecs`), pinned by a test to `internal/billing` and hostd's
`Classes`; it does not import billing, which pulls Stripe into the CLI.
Checking that the host applies it found it did not: hostd booted a
guest from the class recorded at `CreateGuest`, and `StartGuest` carried
none, so a PATCH changed the api's label and nothing else, while the
samples, and so billing, kept the old class. `StartGuest` gains `class`
(field 10); the api sends the project's class on every start, and hostd,
when it differs, checks memory for the new class, boots at its vCPUs and
memory (unit `MemoryMax` included) and records it. Empty keeps the
recorded class, so an api without the field is still accepted, and an
old hostd ignores it (the class then changes at the next hostd deploy's
first start). A host without memory for the larger class refuses the
start with `insufficient_capacity` as any start would; the project is
not moved. Billing already handles a mid-period change (the period's
cap is the largest class run in it; billing.md says so now). The `oom`
guestd warning the brief asked to name the command reaches only the api
log and metric (`events.go`), never the user, so there is no warning
text to change; machine.md's memory paragraph and troubleshooting.md
name `repose resize --size` instead. `TestResizeClass`,
`TestClassSpecsMatchBillingAndHost`, `TestStartAppliesChangedClass`,
`TestLifecycle` (StartGuest carries the class). Interfaces:
grpc-hostd.md says so in this commit. *Rejected:* a separate `repose
size` command (a second verb for one idea); `--class` (the CLI calls the
class "size" everywhere users see it); restarting a running project
without asking (it ends running agents and changes what hours cost);
refusing a running project with "stop it first" (the stop and start are
what the user would type next).

**I-261. `repose open` reaches a server on `::1`, and `open --desktop`
picks a free laptop port.** (feature round, 2026-09-26) `repose open
PORT` always forwarded to the guest's 127.0.0.1, so a dev server
listening only on ::1 (Vite where localhost resolves to ::1 first) was
unreachable, while auto-forward (I-199) already knew better. `open` now
reads the guest's listeners once (`ss -Hltn` over the connection it has
just made, bounded at 5 s) and forwards to ::1 when the port listens
only there, else 127.0.0.1, which also reaches 0.0.0.0 and ::; the
line parser is auto-forward's, shared (`parseListenerLine`), without
auto-forward's platform-port filter since the user named the port. When
nothing listens yet it forwards to 127.0.0.1 and says so on stderr; a
server that later binds ::1 alone needs `repose open` again. `open
--desktop` bound laptop port 6080 with no check, so a second project's
desktop (or anything on 6080) failed after the desktop had started; it
now uses 6080 when free and otherwise a free port, with the same
"port N is taken; forwarding to M instead" line `open PORT` prints, and
the URL names the port. Both use auto-forward's laptop check
(`laptopPortFree`: 127.0.0.1, ::1 and the wildcards), not the old
127.0.0.1-only one, so a laptop server on ::1 no longer hides the remap.
`TestOpenForwardReachesWhereTheServerListens`,
`TestOpenForwardsToAnIPv6OnlyServer` (a real sshd: the new forward
reaches a ::1-only server, the old one does not),
`TestPickLocalPortRemapsABusyPort`. *Rejected:* forwarding to
`localhost` and letting the guest's sshd try each address (works only
while the guest's resolver lists both, and hides which one was used);
forwarding both addresses (ssh -L has one target per local port).
**I-259. Agents start in the checkout's dev environment.** (feature round,
2026-09-26; the machine guide and /docs/machine told users to `use flake`
in `.envrc`, but only an interactive shell's direnv hook read it) An agent
`repose run` starts is `tmux new-window <agent>`, a bash that is neither
login nor interactive, and the wrapper sourced only
`/etc/profile.d/repose.sh`, so the agent and every command its tools ran
missed the flake's tools and variables. The agent wrapper
(`nix/overlay/agents/devshell.sh`, sourced by `wrap.nix`) now loads the
environment into its own process before it execs the agent, so it holds
however the agent starts (`repose run`, `--worktree`, typed in a shell):
the `.envrc` direnv finds from the working directory up, via `direnv
export bash`; else a `flake.nix` below `$HOME` whose text mentions
`devShell`, through a generated `.envrc` (`use flake <dir>`) under
`~/.cache/repose/devshell/<hash>`, so nix-direnv's cache and GC root are
kept and nothing is written to the checkout; else nothing. A load that
fails prints `repose: the dev shell from X did not load` and the agent
starts anyway: a non-zero `direnv export` leaves the environment as it
was, and a flake that fails to evaluate (nix-direnv falls back to the
last dev shell it built and sets `NIX_DIRENV_DID_FALLBACK`, while direnv
reports success) keeps that last one, or none. An
environment the process already has (`DIRENV_DIR` names the directory) is
not announced again and `direnv export` is a no-op. **Not allowed:** an
`.envrc` never allowed on this guest is allowed by the wrapper, which
says so in the pane. Every new guest, every `--worktree` directory and
every edit of the file would otherwise start the agent without its
environment until someone opened a shell to allow it, and the agent is
about to run this checkout's code (its build, its tests, its scripts)
with the same rights anyway; the file came from the user's own checkout.
An `.envrc` the user **denied** (`direnv deny`) is respected: the agent
starts without it and the pane says so, and no flake is loaded instead.
**Slow first load:** while the wrapper loads inside tmux it sets the pane
option `@repose-devshell=loading`; `waitPaneIdle` does not count that time
against its 30 s and waits up to 30 minutes, and `repose run` shows
`Loading the project's dev shell`, so the prompt is not typed into a pane
whose agent has not started. A CLI without this waits 30 s as before; a
guest without it never sets the option. `TestPromptWaitsForDevShellLoad`
(fails with the option ignored), VM test `guest-devshell`.
Interfaces: `guest-conventions.md` "Agent wrappers" step 3, in this
commit. Needs a base publish (the wrapper) and a CLI release (the wait).
*Rejected:* `direnv exec DIR agent` (a failing `.envrc` exits non-zero
and the agent never starts: a dead window); `nix develop --command` for a
bare flake (no cache or GC root, re-evaluates every start); leaving an
unallowed `.envrc` out with a message (the common case, a fresh guest or
worktree, would silently miss the environment); allowing all of
`/home/dev` in direnv's config (changes interactive shells too and
overrides a deny); evaluating the flake to check for a devShell (a full
evaluation, input fetches included, on every start without one).
**I-264. tmux passes modified keys, OSC 8 links and passthrough to the
laptop's terminal.** (feature round, 2026-09-26; research breakage 11:
Shift+Enter in Claude Code sent the prompt instead of a newline)
`/etc/tmux.conf` gains `extended-keys on`, `extended-keys-format csi-u`,
`terminal-features ",*:extkeys"`, `terminal-features ",*:hyperlinks"`
and `allow-passthrough on`, checked against the base's tmux 3.7c. With
`extkeys` tmux asks every client terminal for modifyOtherKeys
(`\E[>4;2m`); a program that asks tmux for extended keys (Claude Code
does) then gets Shift+Enter as `\E[13;2u` while Enter stays `\r`, so
guestd's `send-keys '<prompt>' Enter` is unchanged. `extkeys` and
`hyperlinks` are declared for every `TERM`, not a list: laptops present
`xterm-256color`, `xterm-ghostty`, `xterm-kitty`, `wezterm` and more,
and a terminal without the feature ignores the request (modifyOtherKeys)
or the OSC 8 wrapper. `allow-passthrough on` (not `all`) lets only the
visible pane write `DCS tmux;` sequences to the laptop's terminal: that
is what the user is looking at, and the terminal already accepts OSC 52
clipboard writes from it (`set-clipboard on`). `csi-u` over `xterm`
because it is the form Claude Code, crossterm (Codex) and most TUI
libraries parse. Synchronized output (`sync`) is not added: tmux 3.7
already declares it for the terminals that have it, and nothing here
measured flicker. What a user may need: a terminal that answers
modifyOtherKeys (Ghostty, WezTerm, iTerm2, xterm); elsewhere (Apple's
Terminal) Shift+Enter still submits, and `\` Enter or Ctrl+J is the
newline; run-and-attach.md says so. guest-base checks the running
server's options and, with a tmux of its own on `/etc/tmux.conf`, that
a pane in extended-keys mode 1 reads Shift+Enter as `\E[13;2u` and
Enter as `\r`. Needs a base publish; takes effect when a machine's tmux
server starts (a machine's next start). *Rejected:* a per-terminal list
in `terminal-features` (misses every terminal not on it, and the
request is harmless where unsupported); `extended-keys always` (forces
mode 1 on programs that never asked, which changes the bytes shells and
older TUIs see); `allow-passthrough all` (invisible panes writing to the
laptop's terminal).

**I-265. Ruby and Java pins are installed like the Node pin; Rails'
native gem libraries are in the base.** (feature round, 2026-09-26;
research breakage 13) The scan (I-222) read no Ruby or Java pin, and
pkg-config found only openssl, zlib, sqlite and libffi, so `bundle
install` of a Rails app failed at psych, pg or mysql2. The scan now
reads `.ruby-version`, a Gemfile's `ruby "x.y.z"` line, `.java-version`,
`.sdkmanrc`'s `java=` and the `ruby`/`java` lines of `.tool-versions`;
the first pin of each wins, in the order `.tool-versions`,
`.ruby-version`, Gemfile and `.tool-versions`, `.java-version`,
`.sdkmanrc`. nixpkgs keeps one Ruby per minor (ruby_3_3, ruby_3_4,
ruby_4_0 in the base's nixpkgs today) and one JDK per major (8, 11, 17,
21, 25), so a pin resolves to its series or major, else the oldest newer
one (Ruby 3.2 → 3.3, Java 9 → 11: newer runs older code more often than
the reverse), else the newest; `repose scan` prints which, and says so
when it is not the pinned one. Pre-release, JRuby, TruffleRuby and
ranges (`>= 3.2`) install nothing. The CLI resolves, not the guest,
because the scan must say what happens without a machine; the list lives
twice, in `internal/cli/scan_runtimes.go` and
`nix/guest/base/runtime-versions.json`, and the base's build asserts
each attribute exists in its nixpkgs while `TestRuntimeVersionsFile`
fails when the two lists differ, so a nixpkgs bump that drops a version
fails loudly. tools-wanted.json gains `"ruby":"x.y"` and
`"java":"<major>"` (additive; an older installer ignores them), and
repose-tools-install's node step becomes one step per runtime (node,
ruby, java), with the same rules: installed with `nix profile add` only
when dev's profile comes first on PATH, else a `#warn` naming `repose
config add <attr>`; recorded in `~/.repose/tools/<runtime>`; undone and
said once when a new login shell does not report the version. Java is
`jdk<N>_headless`: servers, Gradle and Maven need no AWT, and the full
JDK pulls GTK into dev's profile. `GEM_HOME` was already
`~/.local/share/gem` (I-227), so gems and `bundle install` land on the
volume. The scan counts `bundle`, `rake`, `java`, `javac` and the other
commands the pinned runtime brings as provided. compat.nix adds libyaml,
libpq, libxml2, libxslt and libmysqlclient (mariadb-connector-c) to
`PKG_CONFIG_PATH` and puts `pg_config` (libpq's) and
`mysql_config`/`mariadb_config` on PATH, because pg looks for pg_config
first and mysql2 never asks pkg-config; nokogiri uses its precompiled
gem and needs none of it unless told to use system libraries.
guest-compat compiles and runs a C program against all five libraries
through `pkg-config --cflags --libs`; guest-tools-carry installs stand-in
`ruby_3_3` and `jdk21_headless` from the golden list. The libraries add 11,889,808 bytes
to the base closure (guest-closure-size 6,151,526,064 at b49e83a → 6,163,415,872, limit 6 GiB). Needs a CLI release (scan, the list's
new keys) and a base publish (installer, libraries). *Rejected:*
installing the exact patch release (nixpkgs has one per series; building
Ruby from source per project is minutes of CPU on the tenant's machine
and a toolchain we would support); mise or rbenv in the base (a second
version manager beside nix profiles, and each needs its own download
and build); the full `jdk<N>` (GTK).

**I-266. mosh is not offered.** (feature round, 2026-09-26; research
breakage 15 suggested "ship mosh in the base") mosh-server listens on a
UDP port of the machine and the client sends datagrams straight to the
address it reached with SSH. A guest has no address the laptop can
reach: it is `10.64.x.y` behind the host's WireGuard tunnel to the edge
(DESIGN §7), the host has no inbound, and the only way in is the SSH
gateway, which terminates SSH and relays channels (ssh-gateway.md).
SSH channels carry TCP streams, not datagrams, so there is no cheap
path: mosh in the base alone would install a program that can never
connect. What mosh would take: a UDP relay on the edge with a public
port range; the gateway reading mosh-server's `MOSH CONNECT <port>
<key>` line in the session, allocating an edge port for that guest and
rewriting the line so the client aims at the edge; nftables rules from
the edge to the guest's port over WireGuard; expiry of idle mappings.
That is L-sized work across the gateway, the edge's firewall and the
host's nftables, and it is security-relevant: a UDP flow after setup is
authenticated only by mosh's session key, so certificate revocation
(refreshed every 30 s) and the 12 h certificate lifetime would no
longer end a session, and `POST /internal/sessions` (I-176) would not
see it end. A UDP-over-SSH tunnel (socat both ends) keeps mosh's local
echo but puts it back on TCP, loses roaming, and is two more processes
per session. The latency complaint behind the request is better met by
a region nearer the user (L, a product decision) and, for dropped
connections, by what already exists: agents run in tmux and `repose
attach` reconnects. run-and-attach.md says in one line that mosh does
not work and why. Revisit if the gateway gains a UDP path for another
reason (previews over QUIC, a region with its own edge).

**I-268. `repose resize` takes the project as its first argument.**
(live check of v0.1.17, 2026-09-26) `repose resize --size large e2e-fr`
failed with `"E2E-FR" is not a size like 80G`: resize's one positional
was DISK, while every other command whose object is a project takes the
project there (I-155). The shape is now `resize [PROJECT] [DISK]`. Two
arguments are PROJECT then DISK. One argument is DISK when it parses as a
size, so `repose resize 80G` in a checkout keeps working, and PROJECT
otherwise. A project whose name parses as a size (`80g`) is named with
`--project`. A positional project and a different `--project` are a usage
error, as elsewhere. Completion offers slugs for the first argument only.
`TestParseResizeArgs`, `TestResizeTakesProject` (through the command
tree). /docs `cli.md`, `features/projects.md` and `07-cli.md` say so in
this commit. Needs a CLI release. *Rejected:* a separate verb for the
class (a second name for resizing); making DISK a flag (breaks
`repose resize 80G`, which the docs and the machine guide name).

**I-272. The laptop checkout gets a fetch-only `repose` git remote for
the machine's checkout.** (dev-friendly CLI round, W3, 2026-09-26; owner:
"as close to their existing workflows and tools as possible") The only way
back was the agent pushing to origin and the user pulling, which needs
the machine's git credentials, a round trip through GitHub and a push the
user may not want published. `repose run` and `repose attach` now add
`remote.repose.url = <slug>.repose:~/<slug>` to the checkout's
`.git/config`: the checkout the sync writes (~/<slug>, I-150), over the
`<slug>.repose` alias every other ssh use goes through (I-151, and the
wildcard of I-281), so ControlMaster, the certificate and the gateway's
exec relay are the ones `repose run` already uses. `git fetch repose`,
`git log repose/main`, `git diff main repose/main`, `git cherry-pick`,
`git merge repose/main` and `git pull repose main` are then plain git.
*When:* on `run` after the sync (also with `--no-sync`), and on
`attach` (both the api and the no-api path of I-223), so a project made
before this release gets it on the next command; a local `git config`
read, no ssh. *Which project:* only the checkout's own: the project's
remote is the checkout's origin, or, for a `--name` project without a
remote, the directory's by_dir entry (I-152). A project named with
`--project`, and every `repose fork` copy (whose remote_url is empty,
I-254), gets no remote, because the rule "`repose` is this checkout's
machine" has no exception that way; /docs shows the one `git remote add`
line for a fork copy. *Ours or not:* a remote named repose whose URL has
the exact shape `<s>.repose:~/<s>` is the CLI's and may be retargeted
(set-url) or removed; any other URL is the user's, left alone, and the
CLI says once (recorded as `repose.remoteNoted=true` in that checkout's
config) how to add the machine under another name. No marker key: the
shape is what a user can see in `git remote -v`, and a hand-made remote
of that shape points where the CLI would point it anyway. *Fetch-only:*
the machine's checkout is non-bare with the branch checked out, so a
push is refused (`receive.denyCurrentBranch`) or, if allowed, moves the
agent's branch under its working tree. `remote.repose.pushurl` is the
text `this remote is fetch-only; repose run sends your work to the
machine`: with no colon or slash git takes it as a local path and
prints it back (`fatal: '<text>' does not appear to be a git
repository`), so the refusal explains itself with no hook. Sending work
stays `repose run`. `remote.repose.skipFetchAll = true`: `git fetch
--all` and IDE auto-fetch of all remotes would otherwise reach for a
machine that may be stopped. *Worktree branches* (I-253): git's default
refspec, so the machine's `repose/claude-2` is `repose/repose/claude-2`
here. Kept: the rule "remote name, then the machine's branch name" has
no exception, and `git pull repose repose/claude-2` names the branch as
`repose run --worktree` printed it. *Rejected:* a second refspec mapping
`refs/heads/repose/*` to `refs/remotes/repose/*` (a machine branch named
`claude-2` and the worktree branch `repose/claude-2` would land on the
same ref, and one branch would have two names). *Removal:* `repose
destroy` (to be `rm`, I-273) run in the checkout removes the remote's
config section when it is the CLI's and points at that project; the
fetched refs `repose/*` stay, so work fetched just before the destroy is
not deleted with it (`git remote remove` would delete them). A restore
gets the remote back on the next run. `logout --purge` leaves remotes in
checkouts alone (it does not know where they are). *R3-11:* nothing is
committed to the repository; `.git/config` is local to the clone and
never travels with a push, and the sync sends refs and files, not
config. *Stopped machine:* the fetch fails with the gateway's banner
(`todo-app is stopped; run \`repose start todo-app\``) and ssh's error;
troubleshooting.md says so. Only commits travel this way; uncommitted
work stays on the machine, and the agent guide now tells agents that
committing is enough for the user to get their work. The two sync
messages that said to push from the machine and pull (guest ahead,
diverged branch) now name `git fetch repose`.
`TestReposeRemoteAddedOnce`, `TestReposeRemoteLeavesAForeignOneAlone`,
`TestIsReposeRemoteURL`, `TestCheckoutOwnsProject`,
`TestFetchReposeBringsTheMachinesCommits` (a real `git fetch repose`
over SSH to the in-process fake guest with the URL the CLI writes:
guest commit and worktree branch arrive, merge, cherry-pick, pull,
`fetch --all` skips it, removal keeps the refs),
`TestRunAddsTheReposeRemote` (run adds and says so once, destroy
removes). Needs a CLI release and a base publish (the guide line); the
guest needs `git-upload-pack` on dev's non-interactive PATH (it is in
the git package the base installs), to be checked live.
**I-269. A capacity waitlist holds a new user's first project when the fleet is near full.**
(owner, 2026-09-26: "we should just have a waitlist once hosts are close
to being full. And we email people once done.") Memory is never
oversubscribed and hosts are added by hand at HostMemory80, so a burst of
sign-ups could fill the fleet before the next host is up and turn every
later `repose run` into `capacity`. Now `POST /projects` from a user who
has never had a project (any `projects` row, destroyed included), was
never admitted and is not `exempt` checks the fleet: the memory reserved
on `ready`, undrained hosts, plus 8 GB (a large, the default class) for
each user admitted in the last 72 hours who has no project yet, plus the
new project's class, against `WAITLIST_PERCENT` (default 80, the alert's
line; 0 turns it off) of those hosts' usable memory (RAM minus the host
reserve). Past the line, or with anyone already waiting, the answer is
503 `waitlisted` (a new error code, api.md) with `{position, joined_at,
email}` and a message that is the whole sentence, and the user gets a
`waitlist` row; a retry keeps the place. The CLI prints `repose is at
capacity. You're number N on the waitlist; we'll email <address> when
there's room.` and exits 8, the capacity code: the action is the same
(wait, run again) and scripts that know 8 need nothing new; an older CLI
prints `waitlisted: ` and the api's sentence and exits 1. `GET /me` carries `waitlist:
{position, joined_at}` and the dashboard's empty projects page shows it
(the dashboard creates no projects, and restore and fork need a project
already, so `POST /projects` is the only gate). Admission: every minute,
under advisory lock 1011 on the grpc app, the api admits waiting users
oldest first while the projection with one more large fits, stopping at
the first that does not (strict order); `admitted_at` and the email's
event and outbox row are written in one transaction guarded by `admitted_at
is null`, so a restart, a second replica or `repose-admin waitlist admit`
racing the tick still sends one email. With `WAITLIST_PERCENT=0` the tick
admits everyone still waiting. `repose-admin waitlist list | admit HANDLE
| admit --next N` (audited `waitlist_admit`) shows and moves the queue.
The email goes through the notification outbox like every other: `events`
gains a nullable `user_id` and `project_id` becomes nullable, with a check
that one is set (migration 0007; the down script deletes the user-only
events first), and the outbox reads the user through either. It is
transactional: sent whatever `notify_email` says and without an
unsubscribe link, since it answers the user's own request and nothing
else tells them it is their turn; its text is fixed. Suspended,
cancelled and deleted accounts hold no place. With no usable host at all
(every host down or draining) the gate holds nobody and placement answers
`capacity`: that is an outage, not a full fleet. Metrics
`repose_api_waitlist_waiting`, `_joined_total`, `_admitted_total`; logs
`waitlist_join` (user_id, position, class), `waitlist_admit` (count), no
address. RUNBOOK "Waitlist growing": add a host, admission is automatic.
`TestWaitlistGateAndAdmission`, `TestWaitlistCommands`,
`TestRunWaitlistedPrintsPlaceAndExits8`, `TestWaitlistAdmissionEmail`.
Needs an api redeploy (it migrates itself), a web deploy and a CLI
release. *Rejected:* gating every create (users with machines would be
refused by a queue meant for newcomers; they still get `capacity`); a
separate mail path beside the outbox (a second retry schedule and no
`delivered` record); honouring `notify_email` (the user would wait for an
email that never comes); letting a newcomer take room while others wait
(jumps the queue); a new exit code (nothing a script would do differently
from 8); holding the admitted room forever (a user who never comes back
would block the queue; 72 h is three days to read an email).
**I-273. `repose ls` and `repose rm` are the names; `projects` and
`destroy` are aliases.** (dev-CLI round, 2026-09-26; the owner: "we might
as well just not advertise `projects` and `destroy` and keep the aliases as
the 'real' ones") The command that lists projects is `repose ls` and the
one that destroys a project is `repose rm [PROJECT]`, with destroy's
confirmation, `-y`/`--yes` and `--wait` unchanged, as `docker ps`/`rm`,
`fly apps list`/`destroy` users type. The old names stay as cobra aliases,
so scripts and muscle memory keep working: they are not in `repose --help`'s
list, `repose ls --help` shows them on its `Aliases:` line, and cli.md and
lifecycle.md say in one line each that the old names still work. Every
message that tells a user a command to type now names `ls`/`rm` (the CLI's
hints, the api's destroy-failed reason in `ops/phases.go`), and so do
/docs, `docs/features/*`, RUNBOOK and 07-cli.md's command tree; DECISIONS
entries and status archives keep the words of their day. **Names are fixed
(CLAUDE.md):** `ls` and `rm` are the names from now on; `projects` and
`destroy` must not reappear in text as a command to type, and no third
name is added. The nouns "project" and "destroy" (the operation, the
`destroying` state, `destroy_failed`, the dashboard's **Destroy**) are
unchanged: `rm` is the verb that starts a destroy. The two `list`
subcommands, `secrets list` and `snapshots list`, gain the alias `ls` so
`ls` means list everywhere; `secrets rm` already existed. ops/checks keep
calling `projects`/`destroy`, which the released CLI they may run has.
`TestPositionalProject` (both names), `TestUnknownCommandSuggests`.
Needs a CLI release. *Rejected:* hidden duplicate commands (two command
objects to keep in step, and `docs_test`'s hidden list); removing the old
names (breaks scripts for no gain).

**I-274. `repose ps` lists the tmux windows.** (dev-CLI round, 2026-09-26) `repose ps
[PROJECT]` is one ssh over the project's multiplexed connection running
`date +%s` and `tmux list-windows -t =<slug>` with index, name,
`pane_current_command`, `window_activity` and `window_active`, printed as
`WINDOW COMMAND ACTIVE` (`1:claude*  claude  now`, `*` the current window,
as tmux marks it). COMMAND is the process name tmux reports, never its
arguments; the output goes to the user's terminal and is not logged
anywhere. ACTIVE is the guest's clock minus the window's last output, so a
laptop clock that is off does not skew it; rounded to now, minutes, hours
(under 48) or days. `-q`/`--quiet` prints names only, `--json` records
`{index, name, command, current, activity, idle_seconds}`. A project that
is not running is exit 5; no session says `repose attach` starts one.
`TestPsListsWindows` (real tmux over the sshd harness, asserts no
argument is printed), `TestParsePs`. Needs a CLI release only.
*Rejected:* guestd's agent states from the api (per agent, not per window,
and a minute stale); `ps` of the guest's processes (arguments, and not
what the user means by "what's running").

**I-275. `repose exec` runs one command in the checkout; `repose ssh`
opens a shell there.** (dev-CLI round, 2026-09-26) `repose exec [PROJECT] -- COMMAND
[ARG...]`, `docker exec` shaped: `--` is required (a project and a command
cannot otherwise be told apart), each argument is single-quoted so it
arrives as typed (a pipeline is `sh -c`'s job, as with docker), stdin is
passed only with `-i`/`--interactive`, a remote terminal only with
`-t`/`--tty` (`ssh -tt`, so it is given even when stdin is not a
terminal, as docker's `-t`). The remote side is `cd ~/<slug>` (home, with
a `repose:` note on stderr, when the checkout is not there), then
`/etc/profile.d/repose.sh` (project variables, named secrets), then the
agent wrappers' own dev environment loader, so the command sees exactly
what an agent sees (I-259): the base now installs `devshell.sh` as
`/etc/repose/devshell.sh` (overlay attribute `reposeDevshell`, which
`wrap.nix` uses too); a base without it gets `direnv export bash`, which
is what an interactive shell has. **Exit codes:** before the command
runs, a failure is one of repose's own codes with a message (4 no
project, 5 not running, 3 not logged in); once it runs, repose exits with
the command's status, whatever it is, and prints nothing, so `repose exec
-- make check && deploy` works; 255 is ssh losing the connection. This
is `docker exec`'s rule without its 125-127 band: a command that is not
found is the guest shell's 127, which means the same. cli.md's exit code
table says so. `repose ssh [PROJECT]` replaces the CLI with `ssh -t
<slug>.repose` running the login shell in the checkout, outside tmux;
arguments are not passed to ssh (that is `exec`, or plain `ssh
<slug>.repose` with the user's own flags). Both require a running project
and reuse `connect` (certificate, config, fast path), without touching
cert.go's rendering. `TestExecRunsInTheCheckout` (cwd, arguments with
spaces, `$` and quotes unchanged, no stdin without `-i`, `-i` passes it,
exit 42 and 127 come back), `TestExecScriptGolden` holds
`internal/cli/testdata/exec-script.sh`, which VM test `guest-devshell`
runs over ssh in a checkout with a flake and no `.envrc` and asserts the
flake's variables and tool, `REPOSE_PROJECT` and the cwd. docs_test's
ghost check now accepts `--` as a token. Needs a CLI release and a base
publish (`/etc/repose/devshell.sh`; without it exec falls back as above).
*Rejected:* `repose exec PROJECT CMD` without `--` (`repose exec npm
test` in a checkout would look up a project called npm); running the
command through `bash -lc STRING` (the user's quoting would be read by a
second shell); `direnv exec` (a failing `.envrc` would stop the command,
where the agent wrapper starts anyway).

*Amended 2026-09-27 (found live on v0.1.18):* exec loads the dev shell
with `REPOSE_DEVSHELL_QUIET=1`, so a load that works prints nothing: its
stderr is the user's command's. direnv's messages are held back and shown
only when the load fails, and the "loading the dev shell" line appears
only after 2 seconds. Agent windows keep every message, since the pane is
where a slow or broken load should be seen. The `guest-devshell` VM test
asserts a cached exec's stderr is empty.

**I-276. Did-you-mean for commands, `-q` on listings.** (dev-CLI round, 2026-09-26)
cobra suggested only at the top level, only by a command's own name, and
a group with an unknown subcommand (`repose secrets lsit`) printed the
group's help and exited 0. `Execute` now checks the arguments first on a
throwaway command tree: an unknown word under the root or under a group is
`unknown command "lsit" for "repose secrets"` / `Did you mean `repose
secrets list`?` / `Run `repose secrets --help` for its commands.`, exit 2.
Candidates are visible subcommands whose name or alias is within one edit
(names of four letters or fewer) or two (longer), Damerau-Levenshtein so a
swapped pair counts once, or starts with what was typed, or that list the
word in `SuggestFor` (`list` and `project` lead to `ls`, `delete` and
`remove` to `rm`); at most three, closest first. Aliases count, so
`projetcs` suggests `ls`. `__complete` and `help` are left to cobra.
`logs -f` and `events -f` already existed. `-q`/`--quiet` prints names or
ids one per line on `repose ls` (slugs; with `--destroyed`, each name
once), `repose snapshots list` (ids) and `repose ps` (window names), for
`repose ls -q | xargs -n1 repose stop`; with `--json` it is a usage
error. No command had `-q` before. `TestUnknownCommandSuggests`,
`TestProjectsTable`, `TestPsListsWindows`. Needs a CLI release.

**I-277. `repose secrets import` sets every NAME=VALUE of a .env file.**
(dev-CLI round, 2026-09-26) `repose secrets import [FILE]` reads `./.env` by default,
`-` for stdin (so `op inject … | repose secrets import -` never puts
values on disk), and PUTs each name through the api path `secrets set`
uses: the values go to named secrets' one home and nowhere else, the file
is only read, and only names are printed. Format, as docker compose and
the dotenv libraries share it: `#` comments and blank lines skipped, an
`export ` prefix ignored, `'single'` literal, `"double"` with `\n \r \t
\" \\ \$` escapes, quoted values may span lines (PEM keys), an unquoted
value ends at ` #` and is trimmed, no `${VAR}` expansion, a repeated name
takes its last value. A parse error names the line and never its text.
Every name and value is checked first (name pattern, reserved names, 64 KB)
and a file with any bad one is refused whole, exit 2, listing the lines:
half an import is worse than none. **Existing names are replaced**, as
`secrets set`, `fly secrets import`, `gh secret set -f` and `heroku
config:set` do; the summary marks them `NAME (replaced)` (from a `GET
.../secrets` first), and `--dry-run` shows the same list and sends
nothing. A failure part way prints which names were set before it; running
again is safe. No count limit exists server-side; each PUT to a running
project enqueues the usual UpdateSecrets. `TestParseDotenv`,
`TestSecretsImport` (values as the api received them, no value in any
output, bad names send nothing). Needs a CLI release. *Rejected:*
refusing existing names without `--overwrite` (vercel's `env add` does,
but import's usual reason is updating values, and `set` already
replaces); expanding `${VAR}` (would read the laptop's environment into a
secret silently).

**I-281. Every ssh to `<project>.repose` first runs `repose ssh-prepare`,
so plain ssh, scp, rsync, git and editors reach every project.** (dev-
friendly CLI round, W2, 2026-09-26; found live 2026-09-25: a project
created on another laptop had no Host block, and an editor failed once the
certificate expired) `~/.ssh/repose/config`, the file `~/.ssh/config`
includes (I-151), is now `Match originalhost "*.repose,!*'*" exec
"'<repose>' ssh-prepare '%n'"`, `Match all`, `Include ~/.ssh/repose/hosts`,
and the Host blocks moved to `hosts`, each with a `# project <id>` line.
ssh runs the exec before it reads the next line and opens `hosts` only at
the Include, so what the prepare writes is what that connection reads;
checked with the system ssh of OpenSSH 8.2, 8.8, 9.6 and 10.5
(`TestPlainSSH*` pass on each). The prepare's fast path (block present,
certificate for the CLI's key carrying the block's id with 30 minutes
left, known_hosts) reads files and returns: 2 ms per run measured, no api
call (the test counts them). Otherwise it takes
`~/.ssh/repose/.prepare.lock` (flock; concurrent connections queue and
the rest find the files ready: 6 at once issue one certificate), reads
the Env after the lock (a token refresh by another prepare rotated the
refresh token), lists the projects and runs `ensureCert`, bounded by 10 s
(VS Code's connect timeout is 15). It never prompts: not logged in,
an unknown project (`repose: you have no project called X`) or an api
it cannot reach is one stderr line and a failed ssh, unless the
certificate on disk is still valid for a minute, which is then used with
a warning. *Stopped machines:* connecting does not start one. An editor
reconnects in the background, so a start there would restart a machine
the user stopped on purpose and bill for it, and a start takes longer
than VS Code's connect timeout; the gateway's banner already names
`repose start`. The same rule as attach, exec and ssh. *Binary path:* the
PATH entry when it is the same file as the running binary (install.sh's
`~/.local/bin`, a package-manager symlink, both stable across upgrades),
else `os.Executable()`; a binary not named `repose` (the test binary,
which would run its whole suite: it did once, before this rule) gets no
Match line, and neither does a path with a quote, backslash or `%`, nor
Windows (Win32-OpenSSH runs the exec through `system()`/cmd.exe, and the
CLI is not supported there). The Include is unconditional, so a failed
prepare or a moved binary leaves the blocks on disk working as before;
`sshFilesCover` treats a `config` that is not what this binary writes as
not covering, so the next command that connects rewrites it, and
`repose login` writes it too, so a new laptop needs no `run`. The CLI
sets `REPOSE_SSH_PREPARED=1` for its own children (captured before it is
set), and the prepare returns at once for them; without it, ensureCert's
`ssh -G` would wait on its own lock. `repose code` removes it from the
editor's environment. Paths in the files are spelled `~` when $HOME is
the passwd home (what ssh expands `~` to), else absolute. `!*'*` keeps a
host name with a quote out of the shell; OpenSSH 9.6+ refuses such names
anyway. *Rejected:* `Host *.repose` with `User %n`-style tokens (User
takes no tokens before OpenSSH 10: `ssh -G` printed `user %n-user` on
8.2, 8.8 and 9.6); `ProxyCommand repose ssh-proxy` to renew the
certificate (every version tested loads `CertificateFile` before the
proxy command produces a byte, so the connection offers the old one, and
the User problem remains); `ProxyCommand ssh -W` through the gateway (the
gateway terminates SSH and relays channels, and the inner connection has
the same User problem); writing blocks only on run/attach (the bug).
`TestPlainSSHToAProjectNeverRunHere` (ssh, scp, rsync, git ls-remote and
clone against a project with no block and an expired certificate, a fake
guest that checks the certificate like the gateway),
`TestPlainSSHConcurrentConnectionsIssueOnce`, `TestPlainSSHNotLoggedIn`,
`TestPlainSSHUnknownProject`, `TestRenderSSHEntry`, `TestSSHFilesCover`.
Interfaces: `ssh-gateway.md` "CLI side", `cli-config.md`; /docs page
`ssh-and-editors.md`. Needs a CLI release; JetBrains Gateway (its own
SSH client, which does not run the exec) is documented with a manual
`ssh <project>.repose true` first and is untested.

**I-282. `repose code [PROJECT]` opens the checkout in VS Code, Cursor or
Zed over that host.** (dev-friendly CLI round, W2, 2026-09-26) VS Code
and Cursor get `--remote ssh-remote+<slug>.repose /home/dev/<slug>`, Zed
`ssh://<slug>.repose/home/dev/<slug>` (the checkout of
guest-conventions.md). The editor is `--editor code|cursor|zed`, else
`REPOSE_EDITOR`, else the first found in that order: on PATH (`zed` or
`zeditor`), then on macOS the launchers inside `/Applications` and
`~/Applications`, for an editor installed without its shell command.
None found is exit 1 naming the three and the /docs page; an unknown name
is exit 2. Before launching it proves the connection the way `exec` and
`ssh` do (`connectRunning`, shared with them now): the project must be
running (exit 5, as attach; see I-281 for why nothing starts a machine
implicitly), and the certificate and block are written, so the editor's
own ssh finds them. The editor's environment has no
`REPOSE_SSH_PREPARED`, so its later connections renew the certificate
themselves. `TestCodeOpensTheCheckoutOverSSH`. /docs `cli.md` and
`ssh-and-editors.md`. *Rejected:* a JetBrains Gateway launcher: its
`jetbrains-gateway://connect#...` URL is not documented by JetBrains and
could not be checked here; starting a stopped machine (I-281).
**I-280. `run` and `attach` proxy the terminal, so a dropped file or a
Ctrl+V image reaches the agent in the guest.** (dev-friendly CLI round,
worker W1, 2026-09-26; the owner: "I want to be able to do native image
dropping in and it gets to the agent session. It must happen.")
Supersedes I-252's "*Rejected:* the CLI as a pty proxy catching Ctrl-V"
and I-206's "*Rejected:* keeping the CLI as ssh's parent"; `repose paste`
itself stays.

- *Probe first.* Claude Code 2.1.280 on this box, in a scratch tmux
  session, no prompt sent: an absolute PNG path delivered with `tmux
  paste-buffer -p` (a bracketed paste) became `[Image #1]`, plain, in
  single quotes, in double quotes, and backslash-escaped with spaces; two
  paths separated by a space became two images; two separated by a
  newline, a `file://` URI and a relative path stayed text; the same path
  sent as keys (no paste markers) stayed text; `ESC[200~path ESC[201~`
  written into a tmux client's terminal (as the laptop's terminal sends
  it) reached Claude as `[Image #n]`. I-252's assumption (a bracketed
  paste of the path attaches, no `@path`) holds. tmux 3.7c (the guest's)
  turns bracketed paste on in the attached terminal whatever the pane
  runs, so a drop always reaches the CLI with paste markers from any
  terminal that sends pastes bracketed.
- *Shape.* On macOS and Linux, when stdin and stdout are terminals,
  `attachTmux` runs the same `ssh -t [-o SendEnv=TZ] <target> tmux attach`
  on a pty (github.com/creack/pty, golang.org/x/term for raw mode)
  instead of exec'ing it: output copied out, SIGWINCH copied to the pty,
  SIGHUP/TERM/INT/QUIT handed to ssh, and ssh's exit status returned
  (128+N for a signal), so the documented "the exit code is ssh's"
  holds. ControlMaster use is unchanged (same target args). Windows, a
  non-terminal, or a pty that cannot open: exec as before.
  `REPOSE_INPUT_PROXY=0` is the kill switch (cli.md, cli-config.md). The
  session helper needs no change: its parent is the CLI, which now exits
  when ssh does.
- *The scanner* (`inputproxy.go`, pure, table-tested at every split
  offset) passes input through byte for byte. It holds back only a cut
  prefix of a watched sequence (paste start, the two Ctrl+V encodings),
  for 30 ms at most, so a lone Escape is delayed by that and nothing else
  is. Two interceptions: a bracketed paste whose content is only absolute
  paths of existing regular files (backslash-escaped as Terminal.app,
  iTerm2, Ghostty and WezTerm's default send them, quoted as kitty, GNOME
  Terminal and Konsole do, `file://` URIs, space- or newline-separated, or
  one unquoted path with spaces); and Ctrl+V as 0x16, CSI u `118;5u` or
  modifyOtherKeys `27;5;118~` (the guest's tmux asks for extended keys,
  I-264). A paste of text, of a missing path, of a path under a system
  directory (`/etc`, `/usr`, `/nix`, ... where the user means the
  machine's own file), of a hidden file or a file in a hidden directory
  (an agent that asks the user to paste `~/.ssh/id_ed25519`'s path must
  not get the key), or over 64 KiB goes through unchanged. A whole read
  with no markers that is only such paths also counts as a drop (a
  terminal not asked for bracketed paste; a person typing sends a key per
  read). Which terminals quote how was taken from their documentation
  and source as far as known, not tested on each: the parser accepts
  every form, so an unlisted terminal works if it sends one of them.
- *What a drop does.* Every file goes over the project's multiplexed ssh
  with `repose paste`'s save script, now shared (`pasteSaveScript`:
  umask 077, symlink or foreign directory refused, pruning over a day
  and past 50 now applies to every file in the directory), as
  `/tmp/repose-paste/<UTC ts>-<n>-<name>`, the name reduced to
  `[A-Za-z0-9._-]` so the extension (how Claude Code tells an image)
  survives. The proxy then types one bracketed paste of the guest paths,
  backslash-escaped, space-separated, with the drop's trailing space
  kept. A file inside the checkout the command was run from, when that
  checkout is the project's (the session helper's `RepoDir`), is not
  copied: one ssh reads the size of `$HOME/<slug>/<rel>` and, when it
  matches, that path is typed (a file not yet synced, or changed since,
  is copied instead). Non-images are copied too: the agent can read a
  PDF or a log by path. *Limits:* 20 files and 20 MB a file, the same
  cap as a pasted image, because the copy holds up the keys typed after
  it and /tmp is not for large files (`repose cp` is); over either,
  nothing is copied, the original paste goes through, and the tmux status
  line says why.
- *Ctrl+V* reads the clipboard with `repose paste`'s reader, bounded at
  2 s. A PNG (up to 20 MB) is copied as `<ts>.png` and its path typed;
  no image, no tool, or a timeout sends the key on untouched and prints
  nothing, so vim's block select and readline's quoted insert keep
  working; a missing tool on a desktop is said once per session. With
  an image on the clipboard, Ctrl+V pastes it in every window: the
  documented trade.
- *While a copy runs* later input waits in a queue and follows in order;
  after 0.5 s the status line says a copy is running. Messages go through
  `tmux display-message` over the multiplexed connection, never over the
  pane. Nothing is logged; file names appear only in the user's own
  status line.
- *Docs.* run-and-attach's "Paste an image" became "Drop a file or paste
  an image", drop and Ctrl+V first, `repose paste` for scripts and other
  windows; its kitty and WezTerm key bindings are gone (Ctrl+V does it).
  cli.md (attach, paste, `REPOSE_INPUT_PROXY`), troubleshooting (Cmd+V
  sends nothing with only an image on the clipboard; which cases paste
  the laptop's path), and the agent guide line (the base publish carries
  it).

*Rejected:* tmux-side detection (the guest cannot read the laptop's
files; the laptop path is useless there); an OSC 52 or clipboard socket
into the guest (I-252's reasons stand); uploading on every paste that
merely contains a path (a sentence mentioning a file must stay text);
a larger cap for non-images (the same keystroke-queue and /tmp reasons);
showing progress over the pane (it would corrupt the tmux screen). Not
verified here: a real Mac or Linux desktop terminal, and Terminal.app's
bracketing of a drop; the owner's live check is in 07-cli.md's
checklist.
**I-278. One Claude login per user: the login share.** (owner,
2026-09-26; supersedes the "log in inside each guest" half of R2-8 and
amends CLAUDE.md's "Secrets have three homes") Logging in to Claude Code
once per project was, in the owner's words, load-bearing: users expect
one login and only notice when it is missing. Each user now has one
directory per host, `/var/lib/repose/users/<user_id>/claude-auth`
(0700, host account `repose-auth`), shared into every guest of that user
as the read-write virtio-fs tag `claude-auth` by its own
`virtiofsd-auth@<guest id>` (unprivileged, `--sandbox namespace`,
`--cache never`, guest uid/gid 1000 translated to `repose-auth`). In the
guest, `repose-claude-auth.service` mounts it at `/run/repose/claude-auth`
and bind-mounts its one file, `.credentials.json`, over
`~/.claude/.credentials.json` before any login session starts. The user
runs `/login` in any guest, through Anthropic's own flow, with the
unmodified binary; the file Claude Code writes is then the file every
other guest of that user reads, and a token refresh in one is seen by
all. Repose code never opens, reads, copies, moves or transmits the file:
hostd creates and serves the directory, the guest unit creates an empty
file and mounts it.

Evidence (docs/proposals/2026-09-24-claude-login-shared-folder.md,
experiments A and B, Claude Code 2.1.280/2.1.281 on host-01): Claude Code
writes the file as a temp file renamed over it, so a symlink into a
shared directory is silently replaced by a local file; over a file bind
mount the rename fails with EBUSY and Claude Code rewrites the file in
place, which the other guests see. One `/login` in e2e-cc-a served
e2e-cc-b; a forced refresh in one was picked up by the other's running
session with no restart; three rounds of both refreshing at once left
both signed in and the file valid. The production virtiofsd shape was
checked the same day on host-01 (guest sees dev 1000:1000, rename
refused, in-place write lands, root-created files map to dev too).

Only the one file is shared, never `~/.claude`: `settings.json` holds
hooks, and a shared hook would let an agent in one project run commands
in every other project of the user. The worst an agent can do with the
shared file is read the token (it can already, in its own guest) or
corrupt it, which signs the user out everywhere until the next `/login`.

First-run onboarding (theme, then "Select login method") shows even when
the shared file holds a valid login and does not skip on Esc, so
`repose-agent-setup claude` sets `hasCompletedOnboarding: true` in
`~/.claude.json` when the share is mounted and the key is absent (a user
value, including false, wins). No auth method is removed; `/login` and
`/logout` are Claude Code's own.

Retention: the share is not on the guest volume, so it is in no snapshot
and survives destroy, restore and base changes. hostd stamps
`users/<id>/last-guest` whenever a guest of that user boots and at every
sweep while one exists; the sweep (hostd start and daily) removes a
user's directory 30 days after their last guest on the host went, the
same window account cancellation keeps snapshots in. No new command: the
api needs no change and an account cancellation (which destroys every
project) empties the host within 30 days.

Failure is local: a guest with no user id, a user id that is not a safe
path segment, or a share whose virtiofsd fails to start boots without
the share (logged `auth_share`), and the guest unit leaves Claude Code
with its own login in the guest. A running guest picks the share up at
its next start; hostd attaches it only after its socket exists. The CLI's
"not logged in yet" check (07-cli.md §5.5) is `test -s`, since the
bind-mounted file always exists and is empty until the first login.

Consequences: a login made inside a guest before this is hidden under the
mount (still on the volume) and the user logs in once more, after which
every project is signed in; the owner accepted this over any migration,
which would mean repose code moving a credential. One login per host:
guests of one user on two hosts need one login each (today there is one
host); scheduling a user's guests together is for when there is a second.
The fallback EBUSY write is undocumented Claude Code behaviour; a release
that drops it fails every refresh on the share, so a Claude Code version
bump in the base re-runs the experiment-A trace
(`ops/dev/claude-auth-trace.sh`) before publish. Built before Anthropic's
answer to experiment C (whether this counts as "store or intermediate"),
on the owner's call; if the answer is no, `repose.host.claudeLoginShare =
false` (hostd `--claude-login-share=false`) starts no share and the
per-guest login returns.
`TestCreateAttachesTheUsersLoginShare`, `TestNoUserIDNoLoginShare`,
`TestLoginShareFailureStillBoots`, `TestSweepAuthShares`,
`TestStartAuthRendersTranslatedUncachedShare`, VM check
`guest-claude-auth`. *Rejected:* a symlink (replaced on the first
refresh); `CLAUDE_CONFIG_DIR` on the share (shares hooks); the CLI or
hostd copying the file between guests (copies a credential, and the
terms name that); a new hostd command at account deletion (the sweep
already bounds it by the snapshot window).

**I-283. No auto-mode offer on a machine in bypass mode.** (live check of
I-278, 2026-09-27) On a signed-in machine whose `~/.claude.json` lacks
`hasSeenAutoDefaultNudge`, Claude Code 2.1.281 opens its first
interactive session with "Make auto mode your default permission mode?",
"Yes" preselected. Before I-278 a new machine had no login, so `repose
run "prompt"` attached the user for `/login` and they met the dialog
themselves; with the login share the CLI sends the prompt at once, and the
dialog takes it: on e2e-ls-a the prompt was lost once, and a typed prompt
plus Enter answered "Yes", which wrote `permissions.defaultMode: "auto"`
into settings.json (and `hasSeenAutoDefaultNudge: true`), silently
undoing I-250's bypass default. Setting only that key skips the dialog;
removing it brings it back (checked both ways on e2e-ls-b). So
`repose-agent-setup claude` sets `hasSeenAutoDefaultNudge: true` when
settings.json's `permissions.defaultMode` is `bypassPermissions` and the
key is absent; a user's value, including false, is kept, and a machine in
any other mode keeps Claude Code's own behaviour. Auto mode stays one
`Shift-Tab` or `defaultMode` away, as /docs `agents.md` now says instead
of "answer no to keep bypass". VM check `guest-base` (agent-setup
subtest). Also seen in the same check, not changed: after `/logout`,
Claude Code's unlink of the bind-mounted file fails with EBUSY, so the
share keeps a revoked token; every machine then says "Not logged in · Run
/login" and one `/login` fixes all of them, but the CLI's `test -s`
counts the file as a login and sends the prompt instead of attaching.
*Rejected:* answering the dialog from the CLI (it would press keys into a
screen it cannot see); waiting for the dialog in `startAgentWindow` (a
Claude Code UI string to match, per release).


**I-284. The nothing-new check trusts the commits the last sync recorded,
not the guest's ref tips.** (landing-page session, owner, 2026-09-27;
fixes I-248) The owner's `repose run` from a laptop at 75991e4 checked
that commit out detached over the guest's `main`, where an agent had
pulled origin (114 newer commits, new tags and branches) and committed on
top; the dev server in that checkout then served a days-old page. The
laptop had nothing new: its sync key matched the guest's. But I-248 also
asked the laptop to show that the guest has every commit it would send,
and the laptop did that with `rev-list HEAD origin/<branch> --not <guest
tips it knows>`. After the pull no guest ref pointed at a commit the
laptop had, so the count was the laptop's whole history, `nothingNew` was
false, and the full apply's checkout rule (laptop commit detached when the
guest's branch is ahead) ran. I-248's tests passed only because their
guest's `origin/main` still sat on the laptop's commit. Now the apply
writes, under the key in `.git/repose-synced-key`, the commits that sync
delivered (the laptop's HEAD and origin/<branch>), one per line, and the
probe answers `#synchas` when `git cat-file -e` finds them all. The key
covers those same two commits, so an equal key with `#synchas` means
nothing to send; the tip count stays as the fallback for a key file
written before this. No extra round trip, no probe input from the laptop
(the probe starts before the laptop computes its side). Submodules had
the same gap (their tips counted in `planSubCommits`): each bundled
submodule's HEAD is recorded too, as `<sha> <path>`, and the probe checks
it with `git -C <path> cat-file -e`; a shallow submodule is fetched by the
guest, not bundled, and is not listed. The key already covers every
submodule's path and HEAD. `TestSyncLeavesTheGuestAloneWhenItPulledPastTheLaptop`
failed before (guest HEAD moved to the laptop's commit) and passes;
`TestSyncLeavesASubmoduleThatPulledPastTheLaptopAlone` failed with the
superproject half alone (refused as new laptop work) and passes. *Rejected:* sending
the laptop's SHAs in the probe (the probe runs before `syncGuest` knows
them, I-225's startup overlap); trusting the key alone (a guest that lost
the commits, a gc after a reset, would be told nothing is missing).
**I-286. The repository is `Heracraft/repose`.** (owner, 2026-09-27) The
rename I-98 left as the owner's step is done: GitHub answers the old
`Heracraft/factory` URLs with a redirect (web 301, git fetch and clone,
release downloads), so a CLI, `install.sh` or host still naming it keeps
working. `install.sh`, `DefaultBaseRepo` (`repose-admin base publish`),
`nix/hosts/host-01.nix` `baseRepo.url` and the source links on the landing
page and /docs now name `repose`; host-01 picks its URL up on its next
switch, and hostd clones only a missing base checkout, so no existing
checkout changes. Evidence links in workstream docs and STATUS keep the old
URLs, which redirect. The checkout on the dev box stays at
`~/projects/factory`. *Rejected:* keeping the old name (the product, module
path and binary are `repose`, and a second name is how a grep misses half
the uses).
**I-287. The landing has a design system of its own, drawn from its
pictures.** (owner asked for a design language built on what the landing
already had, 2026-09-27) The pictures draw the machine as a hairline panel
whose edge is the wall an attack stops at; the page now takes that
drawing as its grammar: two rails the content stands between, rules that
run wall to wall and are ticked at each rail, section heads on paper,
pictures on stages that fill the width between the rails, and
features, steps and sizes as cells cut by the same hairlines. It lives in
`apps/web/src/routes/landing.css`, imported by `+page.svelte` alone, over
the house tokens in `layout.css`; `docs/LANDING.md` "The page's grammar"
records the rules and `SectionHead.svelte` and the four feature pictures
(`ComesBack`, `Localhost`, `Browser`, `Editor`) changed shape to fit
(the picture alone in one `.shot` frame, the copy on the page). The
pictures, the palette, the blue bar and the shape rules of "Shape
language" are unchanged. *Rejected:* a second colour or a new typeface
for the system (the pairing is the identity, `DESIGN-LANGUAGE.md`); cards
with gaps for the features and sizes (a floating box is not how the
pictures draw anything); a section label inside a picture (LANDING.md
forbids chapter labels); a numbered running label on each head ("01 Run"),
tried and sent back as generic (owner, 2026-09-27). Amended the same day:
the hero picture's snapshots panel and the internet stack on the
machine's right (the snapshots titled, the internet a bare globe below
them, no window, as it is nobody's machine), so nothing hangs off an
edge; the rogue agent is the red mark alone, no halo; and the five
agents' row shows the marks alone.
**I-288. Every landing shape names a feature and appears where the
feature is; the footer collects them; the logo is an r-mark.** (owner,
2026-09-27: the footer's shapes "feel like they're just spawning" and the
ring logo was sent back) Each shape stands for one thing on the page and
is shown there first: the pinwheel is a snapshot (the hero's snapshots
panel and miniatures, the "Let it break" card and its title), the sphere
is the internet (the hero, with meridians over it), the pill the sync,
halves the localhost forward, the ring the watched browser, the arch the
editor's door in, the asterisk the toolchain, the star the agents; the
footer's row is those eight in page order and nothing else, so it reads
as the page's symbols. Sun, moon and leaf name nothing and are not
shown. The logo is a lowercase r in the same language, a stem in ink and
a quarter disc in blue; `static/favicon.png` is rendered from it. Same
day: the ring is redrawn as a lens (disc, paper ring, ink pupil), since
its four tones did not work at a title's size; and the snapshot mark
moves with its meaning, a quarter turn when a snapshot is taken and a
full turn back when one is restored.
`docs/LANDING.md` "Shape language" carries the mapping. *Rejected:* the
moon as the logo (rest fits the name, but a crescent in a header reads as
a dark-mode toggle); keeping the unassigned shapes in the footer for
completeness (that is the spawning the owner named).

**I-289. Monthly plans through Paddle: Solo and Pro buy memory that may run
at once, disk and egress; a week free with a card; no hourly meter.**
(owner, 2026-09-27: "We're going to launch with the new pricing ... I'm
definitely going to do Paddle ... We'll still require a card ... use Azure
as a cash sink for now") Supersedes R2-12 (hourly through Stripe), R4-7
(the monthly cap per project), R4-8 and I-205 (the trial credit), I-77 and
I-179 (meter events), I-180 to I-185 (the Stripe subscription, the card
saved on a SetupIntent, invoices from Stripe, the test-clock gate). What is
sold is in `docs/PRICING.md`: Solo, $29 a month, 8 GB running at once,
100 GB disk, 250 GB egress; Pro, $59, 16 GB, 250 GB, 500 GB; egress past
the allowance $0.05 a GB as one overage line, and at four times the
allowance the user's machines stop for the period; disk is a hard limit;
projects 10 and 25; seven days free on the card taken at checkout. Why a
plan: the product's promise is a machine left working, and an hourly
meter argued with it; money is taken before compute runs, so a stolen card
buys a week and one seat instead of a month of arbitrary usage; and the
plan is an ordinary Paddle subscription, where hourly meters were a
workaround. Why Paddle: it is the merchant of record, so tax in every
buyer's country is its problem and the M4 "tax" row closes; the price is
5% + 50¢ against 2.9% + 30¢, about $2 a Solo month. Why these prices: set
for the host repose ends up on (a seat is about €8 on Hetzner metal,
`proposals/2026-09-26-subscription-paddle-hetzner.md` §3); on the launch
`D64s_v7` a seat that never stops costs about $100, which the Azure credit
absorbs and the seat count bounds at one host's worth. Why no free launch
accounts: cash; Paddle discount codes exist for a promotion and need no
code. Mechanics: `internal/billing` keeps `plans.go` (the table above, one
place), the `usage_hours` rollup as the internal record of hours, disk and
egress (its compute cents are 0 from `price_version = "plan-v1"`), the
3-day stop and the gates; the Stripe client, meters, SetupIntent, credit
ledger arithmetic, cap and test-clock proof are removed. A `subscriptions`
table (id = Paddle's subscription id, user_id, plan, status
`trialing|active|past_due|paused|canceled`, seats, paddle_customer_id,
period_start, period_end, next_billed_at, trial_end, cancel_at,
scheduled_plan, overage_charged_for, created_at, updated_at) is the record;
`users.billing_status` keeps its enum plus `none` (no subscription yet) and
is a projection of it; `users.stripe_customer_id` becomes
`paddle_customer_id`; `stripe_events` becomes `paddle_events`;
`overage_charges` records each period's egress line once
(unique on subscription and period). Checkout: `POST /billing/checkout
{plan}` creates a Paddle transaction server-side (customer, price, seven
day trial, `custom_data.user_id`) and the dashboard opens it with
Paddle.js and the public client token from `GET /billing`; the subscription
arrives by webhook (`Paddle-Signature`, HMAC of `ts:body` with the endpoint
secret, five-minute skew, deduped on event id). Plan changes and
cancellation go through the api (`POST /billing/plan`, `/billing/cancel`,
`/billing/resume`), the card and receipts through Paddle's customer portal
(`POST /billing/portal`), invoices from Paddle's transactions
(`GET /billing/invoices`, same shape as before). The overage line is sent
in the hourly tick when `next_billed_at` is within three hours and the
period has none yet, because Paddle locks the invoice about thirty minutes
before charging; account deletion charges it first and then cancels,
because a cancelled subscription drops one-time charges. `payment_required`
carries `detail.reason` in `subscription_required | plan_limit |
disk_limit | egress_limit | past_due | suspended` with `detail.plan`,
`detail.limit_gb`, `detail.used_gb` and `detail.projects` (the slugs using
the memory) so the CLI can print the whole sentence. Environment:
`PADDLE_API_KEY` (sandbox or live, told apart by the key prefix, and a test
refuses live), `PADDLE_WEBHOOK_SECRET`, `PADDLE_CLIENT_TOKEN`,
`PADDLE_PRICE_SOLO`, `PADDLE_PRICE_PRO`, `PADDLE_PRODUCT_OVERAGE`;
`repose-admin billing paddle-bootstrap` creates the products, prices and
the notification destination idempotently and prints the block. With no
`PADDLE_API_KEY` the billing routes answer `503 billing_disabled` and the
gate refuses non-exempt starts with `subscription_required`, so a deploy
without keys is safe and useless rather than free. The M4 gate becomes: in
Paddle's sandbox, a checkout with a test card creates a `trialing`
subscription and a seat, the webhook makes the account `trial`, a
simulated `transaction.completed` makes it `active`, a simulated
`transaction.payment_failed` makes it `past_due` and the 3-day tick stops
the machine, and an overage charge for a known egress appears on the next
transaction to the cent (`docs/ops/M4-GATE.md`). *Rejected:* staying on
Stripe (tax filing per country for a solo operator); hourly on Paddle
(one-time charges per hour, fighting the invoice lock); a shared CPU and
memory pool like exe.dev's (guests are sized per project, so "running at
once" is the honest unit); Paddle.js checkout opened with a price id from
the browser (the server has to hold the seat and stamp the user id first);
a hard stop at the egress allowance (an agent mid-task loses its network
for $0.05 a GB); free plans for the first five (cash); keeping the credit
ledger for goodwill (Paddle adjustments and discounts are that).
**I-290. Seats: the waitlist gates checkout, not the first project; a
seat is 8 GB running at once; invitations hold a seat 72 hours.** (owner,
2026-09-27: "assume I have the big host ... build a waitlist that allows
that many amount of users in, and then put them in a waitlist") Amends
I-269. With plans (I-289) the fleet's unit is the seat: `floor((RAM -
reserve) / 8 GB)` over `ready`, undrained hosts, or `SEATS_TOTAL` when set
(`0` derives it; the launch `D64s_v7` is 30), replacing `WAITLIST_PERCENT`
(memory is never oversubscribed and a seat is exactly what a running
`large` takes, so a percentage of it was the placement alert's number, not
a sales limit). Held seats are the seats of every subscription
`trialing|active|past_due` plus one for each waitlist invitation whose
`hold_until` has not passed. `POST /billing/checkout` needs the plan's
seats free (an invited user's hold counts toward their own checkout);
otherwise, and on `POST /billing/waitlist`, the user joins the waitlist
and gets `503 waitlisted` with `{position, joined_at, email}` as before.
`POST /projects` no longer gates: a user without a subscription is refused
compute with `subscription_required`, and the dashboard's plan page is
where the seats question is answered. Every minute, under lock 1011, the
api invites waiting users oldest first while a seat is free, strictly in
order: `invited_at`, `hold_until = now + 72 h` and the `waitlist_invited`
email in one transaction guarded by `invited_at is null`. A hold that
expires unconverted moves the user to the back (`joined_at = now`,
`expired_invites + 1`) so a ghost cannot block the queue, and the email
says so; a subscription created for an invited user sets `converted_at`
and the row stays for the count. Suspended, cancelled and deleted
accounts hold no place. `GET /public/seats` (no auth) answers `{total,
free, waiting}` for the landing page, and `GET /billing` carries the same
plus the user's own place, because the launch is a gauge of interest and
the count is the reading. `repose-admin waitlist list | admit HANDLE |
admit --next N` keep their names (admit now means invite; the audit kind
stays `waitlist_admit`) and `repose-admin seats` prints total, held, free
and the source. Metrics `repose_api_seats_total`, `_held`,
`repose_api_waitlist_waiting`, `_joined_total`, `_invited_total`,
`_converted_total`. *Rejected:* a card to join the waitlist (Paddle has no
save-a-card step without a subscription, and a $0 subscription per
waiting user is a mess of ghosts); inviting in batches (strict order is
what "first come" means on a launch tweet); holding a seat forever
(blocks the queue); dropping an expired user (three days is enough to
read one email, but a launch weekend is not a reason to lose them).
**I-291. Every email is HTML with a plain-text twin, from one template,
and the account emails exist.** (owner, 2026-09-27: "another thing you
got to design is emails") Notifications were plain `fmt.Sprintf` text
and the only account emails were `billing_stopped` and
`waitlist_admitted`. Now `internal/api/notify` renders every kind through
one HTML template (`templates/`, Go `html/template`, table layout, inline
styles, system fonts, the landing's paper and ink colours, the r-mark as
text, no images, no tracking) and a text template beside it, sent as
`html` and `text` in one Resend call; golden files under `testdata/` pin
both. The kinds added: `welcome` (first sign-in: install, run, the plan
page), `waitlist_joined` (place and what happens next), `waitlist_invited`
(replaces `waitlist_admitted`: 72 hours, the checkout link, what an
expired hold means), `trial_ending` (two days before `trial_end`, the
amount and the date), `payment_failed` (day 0 and day 2 of `past_due`, the
portal link), `subscription_cancelled` (the end date, what stops then),
`subscription_ended` (machines stopped, 30-day retention), `plan_changed`,
`egress_stopped`. Account emails are transactional: sent whatever
`notify_email` says and without an unsubscribe link, since each answers
something the user did or is about to be charged for; agent notifications
keep their unsubscribe line. Subjects stay `[repose] <subject>`. Paddle's
own receipts and payment-failure emails stay on in Paddle's dashboard, so
a failed payment produces Paddle's email about the card and ours about
the machines. *Rejected:* a third-party template service (one more
account and a tracking pixel); React Email or MJML (a build step for
eleven emails); images or the logo as an attachment (blocked by default
in most clients, and the r-mark reads fine as a letter).
**I-293. How the plans landed in the code: repose_api_ metric names, the
limits an exempt account keeps, stops counted, once-only emails derived
from the events table, and a subscriptions-only seat count until I-290
merges.** (ws/paddle, 2026-09-27) Implementing I-289 settled six things
the spec left open. (1) Metrics keep the `repose_api_` prefix every api
family has (I-49, I-60, I-78) and the checked registry's label list:
`repose_api_billing_webhook_total{kind,result}` (`kind`, not `type`, is
the allowed label), `repose_api_billing_overage_charges_total{result}`,
`repose_api_billing_gate_refused_total{reason}`,
`repose_api_billing_subscriptions_total{plan,status}` (`plan` is added to
the allowed labels: a two-value enum) and, beyond the spec's list,
`repose_api_billing_stops_total{reason}` so the `BillingStopped` alert has
a series to read. (2) `users.project_limit` and `xl_limit` stay and are
what an exempt or plan-less account works within (`repose-admin users
limits` still sets them); a subscribed account has its plan's numbers and
the xl count limit is gone, memory decides. New rows start at Solo's 10
projects and no xl. (3) "Once" (day 2's `payment_failed`, `trial_ending`,
`egress_stopped` per period) is derived from the `events` table (no second
row of the kind since the moment it counts from) rather than new columns:
no migration, and the email that went out is the guard. (4) A refused
overage charge leaves its `overage_charges` row without a transaction id
and the period unmarked, and is never resent by the job: a second attempt
after Paddle accepted-then-errored would double a line, so the operator
sends it (`billing overage-now`) after reading Paddle's error;
`transaction.completed` stamps the id when the line appears on a
transaction. (5) A trialing subscription's `transaction.completed` (the $0
checkout) does not make the account `active`, and
`transaction.payment_failed` without a subscription (a card declined at
checkout) changes nothing. (6) `billing.SubscriptionSeats` counts held
seats from `subscriptions` against `SEATS_TOTAL` and never waitlists,
standing in for I-290's implementation, which replaces it in
`internal/api/app` at merge; `billing.WaitlistPlace` reads the 0008
waitlist row for `/me`, `/billing` and the gate's detail because the
`store.WaitlistEntry` query still names the renamed columns until that
workstream lands. Also: `idle.hourly_cents` answers 0 and gains
`memory_gb`; the idle notification names the class's share of the plan's
memory instead of a rate; growing a volume is gated on the growth with
every live project's current size counted. *Rejected:* a `settings` key or
a `dunning` migration for the once-only guards (a second source of truth
for what the events table already records); `repose_billing_*` names
(every api family is `repose_api_*` and the registry refuses other
labels); resending a refused overage charge automatically (a doubled line
is worse than a late one); making a trialing account `active` on the
checkout transaction (it is $0 and the trial has a week to run).

*Amended 2026-09-27 (merge):* the http test harness gives every signed-in
test user a Pro subscription so the compute gate lets its projects
through; a test about seats strips it with `subscribe(t, sub, "")`, which
is why `TestSeatsWaitlistAndInvitations` invited nobody at the merge (the
waiting users' own seats filled the fleet). The same test asserts what
I-290 says of `POST /projects`: a plan-less user is refused with 402
`subscription_required` and the place in `detail.waitlist`, not `503
waitlisted`. On (2): the fork's project count refusal names whose limit
it is (`You have 10 of 10 projects (Solo's limit), ...`; an exempt account
reads `your account's limit`), and `TestFork` reaches it by filling Solo's
ten through the fake's `SetBilling`/`SetPlan` knobs, since the fake's
default account is exempt (I-295) and reads Pro's 25.
**I-294. Seats and emails, the choices the spec left open: one account-event
helper, the sentence, a re-queue on a new checkout, no `!` in an email.**
(seats-email worker, 2026-09-27, building I-290 and I-291) Where I-290
and I-291 were silent: (1) Every user-only event goes through
`events.InsertAccount(ctx, q, userID, ts, kind, payload)`, which writes
the event and its email outbox row on the caller's `Querier` (a pool or
the transaction that also sets `invited_at` or inserts the user), refuses
a kind outside `events.AccountKinds`, and marshals the payload into
`events.summary` as JSON; `notify.transactional` reads the same map, so a
producer cannot add an account kind the outbox would treat as project
mail. The welcome email is written by `auth/users.go` in the user's insert
transaction. (2) The `waitlisted` sentence is `repose is full right now.
You're number N on the waitlist; we'll email <address> when there's a
seat.` (`waitlist.Message`), and the CLI prints the api's message as it
is, building one from `detail` only when the message is empty; the old
"at capacity ... when there's room" wording went with the first-project
gate. (3) A checkout by a user whose row is converted (a plan that has
since ended) or whose hold ran out re-queues the row (`joined_at = now`,
`converted_at` cleared, `expired_invites + 1` for the expired case) when
no seat is free, so a returning user waits like a newcomer and the count
stays honest; a waiting or holding row is left alone. (4) Expiries run
before invitations in the same tick, each expiry in its own transaction
under the waitlist lock and guarded by `hold_until < now and converted_at
is null`, so a webhook that converts the user in the same minute wins.
(5) `repose-admin waitlist admit` invites without checking for a free
seat: an operator letting someone in ahead means it, and the hold then
counts against the next automatic invitation. (6) `repose-admin seats`
reads `SEATS_TOTAL` from its own environment; with none it reports the
hosts' count and says so. (7) The email copy has no exclamation mark and
no dash, checked by `TestGoldenEmails`; the per-kind partials are
`text/template` files producing strings that the `html/template` layout
escapes on placement, so tenant text is escaped once, where it is placed.
(8) `store.User` follows migration 0008 now (`paddle_customer_id`, no
subscription column; the `StripeSubscriptionID` field stays with `db:"-"`
until the Stripe client goes), because no test could run against the
committed schema otherwise. *Rejected:* a second event helper per
producer (each would re-implement the outbox rule); keeping the old
sentence (it told the user to run `repose run` again, which does nothing
for a seat); dropping a converted row on re-checkout (loses the converted
count); refusing `admit` on a full fleet (the operator has no other way to
let a tester in).

*Amended 2026-09-27 (merge of ws/paddle, ws/seats-email and ws/web):* (1)
holds for the billing producers too: `billing.AccountEvent` and its
English summaries are gone, and dunning, the webhooks, the service and the
overage job write typed payloads (`billing.TrialEndingPayload`,
`PaymentFailedPayload`, `SubscriptionCancelledPayload`,
`SubscriptionEndedPayload`, `PlanChangedPayload`, `EgressStoppedPayload`,
the fields of the notifications.md table) through `events.InsertAccount`,
so the row and its one outbox row are written once, by one helper.
`payment_failed` carries no `portal_url` (the webhook has no portal
session for the user; the template links the plan page, where the portal
button is), `subscription_ended.retention_until` is `ended_at` plus 30
days (`billing.RetentionDays`), and `plan_changed.effective_at` is the
moment of the change for an upgrade and the webhook alike.
`TestAccountPayloadsRender` and the producer tests render each event
through `notify.Render` and check the plan's name, the amount and the
date in the HTML and the text. On (2): the one builder is
`waitlist.Message`, in the waitlist package rather than billing, because
billing imports waitlist for the seats (a builder in billing would be a
cycle); `billing.WaitlistedError.Message`, the compute gate's
`subscription_required` refusal while the user waits (which no longer
appends the plan page's URL, so the three sentences are one) and the fake
api's checkout and gate all call it. The CLI keeps its own fallback for an
api that sent no message and prints the api's sentence as it is: a
plan-less user's `POST /projects` prints it and exits 7, a `waitlisted`
checkout refusal or an older api's first-project gate prints it and exits
8 (`TestRunWaitlistedPrintsPlaceAndExits8`).
**I-295. The dashboard under plans: the fake's default is exempt, the
Paddle stub, one site-wide CSP, and what the pages stop showing.**
(web workstream, 2026-09-27, building I-289 and I-290 into `apps/web` and
`internal/fakes/api` before the api's own rewrite landed.) Decisions the
spec left open: (1) `internal/fakes/api` starts with billing off and the
account `exempt`, so every test that is not about billing keeps its
compute; the billing modes (`none|trial|active|past_due|suspended|exempt`)
are a knob, and `SetWaitlisted(n)` keeps its name but now means "no plan,
no free seat, place n", answered by the gate as `payment_required`
`subscription_required` with `detail.waitlist` (I-290 moved the waitlist
off `POST /projects`); the CLI's waitlist test changes with the CLI. The
fake refuses a `suspended` account at the compute gates only and keeps
answering reads, so the dashboard can draw the suspended state; the api's
three-route rule for suspended accounts (api.md) is the api's to enforce.
(2) Against `paddle.environment = "fake"` the dashboard calls
`window.__reposePaddleStub.open({transactionId, onCompleted})` instead of
loading Paddle.js, and a Playwright test installs a stub that completes
the transaction through the fake's admin listener (`POST
/paddle/complete`, CORS on) and reports completion; the same page code
then polls `GET /billing`, so the flow is the production flow minus the
overlay. (3) The app had no Content-Security-Policy and nothing in front
of it sets one, so `svelte.config.js` sets one site-wide through
`kit.csp` (SvelteKit cannot scope it to a route): `script-src 'self'
https://cdn.paddle.com https://*.paddle.com`, `frame-src` Paddle's
checkout, `connect-src 'self' https:` plus the loopback the test fixtures
use (the api and Logto are runtime `PUBLIC_*` values, so they cannot be
named at build time), `style-src` with `'unsafe-inline'` for Svelte's
style attributes. (4) With compute cents 0 under `plan-v1`, the project
page's Cost card becomes a Plan card (the class's memory of the plan's)
and the projects list drops its Today and This month columns and the
money in its summary; `cost_today_cents`, `cost_month_cents` and
`idle.hourly_cents` are still read from the api and ignored. A
`disk_limit` on a resize is shown as the same banner as a start's
refusal. (5) The landing's Units squares count the memory that runs at
once (8 and 16), not vCPUs, since a plan is sold by memory. (6)
`refunds.md` says two things `PRICING.md` "Refunds" does not: an egress
overage is not refunded (it records traffic that was sent) and a charge
made in error is refunded whenever it happened. *Rejected:* enforcing the
suspended account's three-route rule in the fake (the dashboard's other
pages would need a state the api has not specified); a CSP as a
dynamically inserted meta tag on the billing page alone (a meta CSP
cannot be withdrawn on the next client-side navigation, so it would apply
to the rest of the session anyway, unstated); keeping the Cost card with
three zeros.

**I-292. Watching the agent's browser is one command: `repose browser`
opens a viewer page repose ships, sized to the tab, on TigerVNC's Xvnc,
with the password in the URL fragment and the forward in the
background.** (launch round, 2026-09-27; the owner: "let's streamline
the process of having a VNC and whatever because it's all cumbersome
right now ... make it more crisp") `repose open --desktop` took a second
terminal (the forward ran in the foreground until Ctrl-C), printed a
password the user had to type into stock noVNC's dialog, and showed a
fixed 1440x900 Xvfb screen scaled to whatever the tab was, blurry on
anything else, with a new password at every start so a tab left open
never reconnected. Four changes. (1) `repose browser [PROJECT] [--stop]
[--no-open]` runs `repose-guest-profile desktop start`, starts `ssh -N`
as a detached child in its own session (`ExitOnForwardFailure`,
`ServerAliveInterval 15`, `ServerAliveCountMax 3`, off the
ControlMaster as before, I-149) to laptop port 6080 or a free one
(I-261), waits until the viewer's `GET /healthz` answers through it,
records port and pid in `~/.config/repose/browser-forwards/<slug>.json`,
prints one line with the URL and opens it. A second run finds the record,
probes the port for our `healthz` and reuses the forward; `--stop` stops
the guest's viewer and kills the recorded pid, but only while the port
still answers as our viewer, so a recycled pid is never killed. `repose
open --desktop [--stop] [--no-browser]` stays as the hidden old name,
with one stderr line pointing at the new one. (2) The password travels
in the URL fragment (`#p=`): a browser never sends the fragment with a
request, so the forward, websockify and any log see only the path, the
user types nothing, and I-33's password stays as defence in depth. The
guest generates it once per boot (`/run` is a tmpfs, so a boot is its
lifetime) instead of at every start, so the link in an open tab survives
the idle stop and a `--stop`; a reboot changes it and the page says so.
(3) The guest serves its own page (`nix/guest/base/desktop/viewer/`:
`index.html`, `viewer.js`, `viewer.css`, `healthz`) on noVNC 1.7's ES
module core (`core/` and `vendor/` copied out of `pkgs.novnc`, nothing
else of it: a store link would carry its Python and numpy, 260 MB), no
framework, no build step: it connects at once with the
fragment's password, `resizeSession` and `scaleViewport` on,
`clipViewport` off, quality 9 and compression 1 (`?q=`, `?c=` adjust),
a slim bar (project name from `project.json` written at viewer start,
state, remote size, full screen, copy link with the fragment), reconnect
with backoff so the socket activation wakes an idled viewer, the
clipboard bridged both ways where the browser allows, and a plain "the
machine's desktop is off; start it with repose browser" state after three
failed connections. (4) TigerVNC's Xvnc replaces Xvfb plus x11vnc: it is
the X server and the VNC server in one process and implements the
client's SetDesktopSize, so the screen takes the tab's size (a 2560x1440
tab gets a 2560x1440 desktop) instead of a scaled 1440x900; openbox
re-maximises the browser to the new screen (checked in the VM test with
xdotool after resizes to 2560x1440 and 800x600), Chromium's
`--window-size` is dropped, `repose-vncconfig` carries the clipboard,
fontconfig gets grayscale antialiasing with slight hinting (subpixel
fringes do not survive the trip as an image) and Noto defaults. The
viewer is now `repose-novnc.service` (the display, `repose-xvnc`, is up
for the browser alone); Xvnc's VNC port is therefore up whenever the
display is, on loopback with the password, never auto-forwarded. Closure:
Xvfb, x11vnc and libvncserver out (5 MB), tigervnc in with fltk, ffmpeg's
libraries and GLU for the vncviewer nobody runs (about 55 MB); the next
cut, if the 6 GiB cap bites, is a tigervnc built with `BUILD_VIEWER` off.
A Retina
tab is shown at 1x pixels: Chromium reads its scale factor at start, so
following `devicePixelRatio` would need a browser restart; still sharper
than the stretched 1440x900, and the bar says "at 1x". Tests: `TestBrowserURLCarriesThePasswordInTheFragment`,
`TestBrowserForwardArgs`, `TestViewerHealthyKnowsOurViewer`,
`TestBrowserCmdWatchesReusesAndStops` (fake api, real sshd, a stand-in
viewer; the printed line, no password on stderr, the reuse, `--stop`),
`TestBrowserCmdReportsAForwardThatCannotStart`,
`TestOpenDesktopIsTheOldNameOfBrowser`; guest-desktop gains a plain RFB
3.8 client (`nix/guest/tests/rfb-client.py`, VncAuth with its own DES)
that authenticates, receives the framebuffer, and sends SetDesktopSize,
with `xdpyinfo`, `xrandr` and the Chromium window geometry checked after.
Measured while landing this, in one test guest back to back: a cold
Chromium answered DevTools after 333 s and 153 s on Xvfb, 124 s and 145 s
on Xvnc (the guest's load at 5 on 2 vCPUs; four workers on the four-core
dev box), and a warm `/json/version` took 1 to 3 s on both, so the display
server is not what the MCP servers' fixed 30 s connect timeout trips
over; the test's `mcp()` helper tries a connect timeout again, up to
four times, and a quiet box never retries. Ships with the base after
2026.09.27.2 and the CLI after v0.1.20; the docs say what an older half
does against a newer one. *Rejected:* `ssh -f` for the forward (the forked child's pid is unknown
to the parent, so `--stop` could not end it; a detached `ssh -N` whose pid
the CLI keeps does the same and can be stopped); keeping stock `vnc.html`
with `defaults.json` (its dialog asks for the password, its resize mode is
a setting the user finds, and it cannot read the fragment); a relay on
the edge (`:6081` in 06-gateway-edge) or a public URL (a desktop reachable
without the SSH forward is a new attack surface for a feature the forward
already serves); a dashboard embed (the page would need the forward to
exist before the click; the command is the forward); dropping the
password now that it is invisible (a stray forward on a shared laptop
would still expose the desktop, I-33); Xorg with the dummy driver plus
x11vnc (RandR modes would have to be added on the fly and x11vnc still
does not implement SetDesktopSize); `--force-device-scale-factor` from
the viewer's `devicePixelRatio` (needs a Chromium restart, which loses
the agent's page); `<decor>yes</decor>` in openbox (a title bar above
Chromium's own tab strip, wasted rows in a window nobody moves).
**I-296. `repose browser bridge` lends the guest's browser tools the
laptop's own Chrome, through Chrome's DevTools switch, a front that
answers `/json/version`, and a reverse tunnel whose remote command holds
the guest's endpoint switched.** (worktree session, owner asked for "the
Chrome reverse bridge" as a launch feature, 2026-09-27) The reserved
command is built, and not the way features/browser.md sketched it. That
sketch rewrote the guest's `playwright` and `chrome-devtools` MCP entries
to a tunnelled port for the life of the CLI; an MCP entry is read when
the agent starts, so the agent would have had to be restarted twice, and
a CLI that died mid-way left the entries pointing at nothing. Now the
entries never change: the guest's endpoint 127.0.0.1:9224 is a socket
unit, and the bridge swaps which socket unit holds it.
`repose-browser-bridge on` stops `repose-browser.socket` and its proxy
and starts `repose-browser-bridge.socket`, whose `systemd-socket-proxyd`
goes to 127.0.0.1:9226, where the guest's sshd listens for the CLI's `-R`;
`off` is the reverse. Stopping a proxy ends the connections the MCP
servers hold through it, and both reconnect on their next call (I-246),
so a running agent switches browsers with nothing restarted, both ways.
The guest side lives exactly as long as the tunnel: the ssh's remote
command is `repose-guest-profile browser bridge hold`, which switches on,
prints `on`, waits for its stdin to close or for the 9226 listener to go,
and switches off in its EXIT trap; the desktop idle check switches off
any bridge without a listener, for the case where nothing else did.
The laptop side: Chrome 144's `chrome://inspect/#remote-debugging`
writes `DevToolsActivePort` (port, and a websocket path with an
unguessable id) in the profile directory, the same file puppeteer's
`channel` connect and chrome-devtools-mcp's `--autoConnect` read; the CLI
reads it, checks the port answers, and when the switch is off opens the
page in Chrome and polls for five minutes. That server is websocket only
(every HTTP request is 404), and Playwright MCP's `--cdp-endpoint` and
chrome-devtools-mcp's `--browserUrl` both discover the websocket through
`GET /json/version`; so the CLI's front, the port the tunnel reaches,
answers that one request itself with `ws://<Host>/devtools/browser/<id>`
(the Host being the guest's 127.0.0.1:9224, which leads back through the
tunnel) and passes every other request to Chrome byte for byte. `--cdp
URL` bridges any DevTools server through its own `/json/version`;
`--user-data-dir` names another profile. Chrome asks the user to allow
each connection in switch mode and shows its "controlled by automated
test software" bar; the docs say what the user lends (any process on the
guest, every site the Chrome is logged in to) and that it only lasts
while the laptop is awake. `--bridge` on `run` and `attach` runs the same
bridge in the session helper. 9226 joins the platform ports never
auto-forwarded. *Rejected:* rewriting the MCP entries (above); the CLI
launching a Chrome of its own with a repose profile (a second profile has
none of the logins that are the point; `--cdp` covers whoever wants
that); a Unix-socket reverse forward, which sshd's
`StreamLocalBindUnlink` would have made take over from a stale bridge
for free (the gateway relays `forwarded-tcpip` channels only, and an edge
change for this was not worth a launch dependency; `bridge release`
kills the earlier session's sshd process instead, which runs as dev);
bridging Claude in Chrome itself (the extension talks to Anthropic's
relay, not to a port); waiting for a chrome-devtools-mcp `--autoConnect`
in the guest (it reads a file on the machine it runs on). Evidence:
`TestBridgeEndToEnd` runs a real ssh `-R` against the fake guest
(internal/testguest now answers `tcpip-forward`), `TestCDPFront...`
covers the front, and the guest-desktop VM test's five bridge subtests
cover the swap, a running server following it, `hold`'s two ends and
the idle guard.

**I-297. The user docs have a Tutorials section: one job per page, in
the order a new user meets them.** (owner, 2026-09-27: "a tutorial
section with three things", and the git workflow, and the conductor)
The reference pages say what each command does; nobody arriving from the
landing page reads them in order. Tutorials are pages that each get a
user through one thing they came for, with real commands and real
output, and link to the reference for the rest: git with repose (what
travels, what comes back, the `repose` remote), watching the agent's
browser, lending it your Chrome (I-296), a git workflow for several
agents, and running a swarm with a conductor session (`ops/ORCHESTRATION.md`
in user terms). The section sits between "Using repose" and "Account" in
`apps/web/src/lib/docs.ts`'s `SECTIONS`; the quickstart's "Next" list
points at it. A tutorial states only behaviour the reference already
documents, so `docs_test.go`'s rule (a command exists only if `cli.md`
names it) keeps holding: the tutorials add no names. *Rejected:* one
long "guide" page (the landing sends a visitor to one job, and a page
per job is what search and the sidebar can point at); moving the how-to
paragraphs out of the reference pages (they answer the reader who is
already there).

**I-298. The Vercel CLI's login stays on the laptop.** (owner request,
2026-09-27) This amends the I-195..I-205 list, which added the Vercel
CLI's `auth.json` to the logins `repose run` copies (proposal item 3; it
never had an entry of its own, and `creds.go` cited I-205, the trial
decision). That file holds a token for the whole Vercel account: every
team and every project the user has, not just this one. An agent running
with full permissions on the machine can deploy, delete projects or read
the env vars of unrelated projects with it, and with open egress it can
take the token off the machine. The PocketOS incident (April 2026) is this
class: an agent found a root-scoped Railway token in an unrelated file and
deleted a production database and its backups in one API call. A snapshot
undoes none of that. The research is in `reports/Repose credentials
without a firewall.md` §4 (it ranks deploy tokens second, after GitHub,
and calls this the easiest fix) and `reports/Repose credential proxy
research.md` (a proxy would inject the same token, so only scope helps).
So the row leaves the copied list. A user who wants Vercel on the machine
has two existing paths, and no new flag or config key: `vercel login` in
the guest (the file lands on the volume like any file the user writes,
and the copy rule never touches it since the laptop no longer sends one),
or a token scoped to one team with an expiry, stored as `repose secrets
set VERCEL_TOKEN`, which the Vercel CLI reads from the environment.
Guests that already hold a copy: `run` sends the SHA-256 of the laptop's
file (never its bytes) and the guest removes
`~/.local/share/com.vercel.cli/auth.json` only while its SHA-256 is the
same, printing one notice; a login made in the guest, or a copy the
guest's CLI has since rewritten, differs and is left, and the public docs
say how to delete it by hand. The creds marker version moves to `creds-2`
so every guest takes the logins' part once more; after that the removal
is a no-op. Snapshots taken before keep the copy; the docs say to revoke
that token to be sure. gh's login is unchanged here; scoping it is a
separate decision. `TestSyncCredentialsLeavesVercelsLoginHome`.
*Rejected:* an opt-in knob (`carry_vercel` or a flag; the two paths above
already exist, and a knob would keep the full-account token one line
away); leaving old copies in place (the risk is the same whether the copy
is new or old); removing any file at that path (it would delete a login
the user made in the guest); comparing mtimes (a rotated laptop token
makes them ambiguous); minting a project-scoped Vercel token per machine
(needs a full-account parent token in repose's database, a bigger change
the research puts after the user study).

**I-330. A signed-in visitor can read the landing page.** (owner request,
dogfood 2026-09-28) The root layout used to send a signed-in visitor from
`/` to `/projects`, so the owner could not see the landing page without
signing out. The redirect is gone; signed-out visitors on a private route
still go to `/`. On the landing, a signed-in visitor's header shows
**Dashboard** (the word the docs header already uses) in place of **Sign
in**, and the hero and pricing buttons read **Open the dashboard** in place
of **Sign in with GitHub** / **Start with GitHub**. Sign-in itself still
ends on `/projects` (`routes/callback`). Changes `08-dashboard.md` §5.2,
RUNBOOK's sign-in loop step 3 and `tests/auth.spec.ts`. *Rejected:* a
`?home` escape hatch on the redirect (nobody would know it), keeping the
sign-in buttons for signed-in visitors (they would restart a login for
someone already in).

**I-331. Sign-out leaves the page alone until the browser goes, and no
page paints before its stylesheet.** (owner report, dogfood 2026-09-28)
`signOut()` set `authState.authenticated = false` before Logto's
end-session redirect, so for a moment the signed-in page re-rendered as
signed out and the layout sent it to `/` (the landing flashed) before the
browser left for Logto's page. Now it only calls Logto; the local flip
and `goto('/')` happen only if that call throws. Logto's own end-session
page is hosted by Logto and keeps its own theme; that part is not ours.
Measured separately: the SPA renders a route as soon as its script runs,
and the route's CSS is not guaranteed to be in by then (a probe with CSS
held back 1.5 s painted the unstyled page, a full-screen logo); on a
normal load both stylesheets were in at 159 ms. So `app.html` hides the
body until `layout.css` makes it visible (the inline `html` background
already has the right colour for either scheme), the landing's `.rails`
stays hidden until `landing.css` applies, and `--ink`, `--ink-muted` and
`--ink-faint` move from `landing.css` to `layout.css`. Test: `auth.spec.ts`
"sign-out does not flash the landing page" fails with the old `signOut`.
*Rejected:* linking the hashed CSS files from `app.html` (names are only
known after the build), a loading overlay (more to paint, same effect).

**I-332. Settings save as they change; the ntfy URL keeps a Save.** (owner
request, dogfood 2026-09-28) One Save at the bottom of `/settings` saved
the timezone, the email checkbox and the ntfy URL together, and the owner
toggled email and left without saving. Now **Email notifications** sends
`PATCH /me {notify:{email}}` on change, toasts "Email notifications
on." / "…off." and reverts the box when the call fails; the timezone
select sends `PATCH /me {tz}` on change ("Timezone set to X.", reverts on
failure). The ntfy URL is typed, so saving per keystroke would store half
URLs: it has its own **Save** next to the field, disabled until the value
differs, a "Not saved yet." line while it does, and a confirm when leaving
the page (a browser prompt on reload or close) while it is unsaved. With no
page-wide Save, an account whose `tz` is null now gets the browser's
detected zone stored on first visit, quietly, so the page never shows a
zone the account does not have. The account's own zone is always an option
in the select: Chromium's `Intl.supportedValuesOf('timeZone')` leaves out
`UTC`, and the select showed blank for such accounts. No api change
(`PATCH /me` already treats absent keys as unchanged). Public docs:
`notifications.md`.

**I-333. "Recently destroyed" shows ten rows, then more on request.**
(owner request, dogfood 2026-09-28) `GET /projects/destroyed` returns up to
100 rows with no cursor (`internal/api/http/restore.go`), and the owner's
list ran for screens. The section shows the newest 10 and a "Show N more"
button with "10 of 35 shown", adding 20 a click; the count survives the
list's polling. No api change: a cursor would be new contract for a list
capped at 100 that the page already holds. Public docs: `lifecycle.md`.
**I-300. A project being destroyed does not count toward the project
limit; one left in error by a failed destroy does.** (owner dogfood,
2026-09-28) stop-start-destroy.md already promised "Frees the project
slot immediately", but the create, restore-as-new, fork and xl-resize
checks counted every row with `destroyed_at is null`, so `repose rm` then
`repose run` at the limit was refused for the length of the destroy. All
four now share `countsTowardLimit` (`destroyed_at is null and state <>
'destroying'`), xl included; the fake's fork check does the same. A
project in `error` after a failed destroy still counts: its volume is
still on the host and `repose rm` again resumes the destroy. The unique
indexes on live rows keep the name and remote taken until
`markDestroyed`, which I-301 handles. For the length of a destroy an
account can hold one volume more than its limit; the destroy always
finishes (I-156), so that is bounded. `TestDestroyingFreesTheSlot`.
*Rejected:* counting `error` out too (it holds a volume, and a user could
stack failed destroys); freeing the name early (a restore of the old
project and the new one would fight over it).

**I-301. `repose run` on a project being destroyed waits and starts
over.** (owner dogfood, 2026-09-28) It stopped at "is destroying". Now,
when resolve finds the project `destroying`, run shows "Waiting for the
old <slug> to finish destroying", polls the project until it is gone
(404 or `destroyed`, up to 10 minutes), drops every cache entry naming
it, and goes on as if no project were found, with the old project's name
as `--name` when none was given, so the fresh project keeps the name and
the checkout's remote. A destroy that ends in `error` stops the run with
the project's own message and creates nothing. Named with `--project`
from another checkout, it refuses before waiting (the new project would
take this directory's remote). `repose rm` then `repose run`, and `repose
rm --wait && repose run`, are the documented way to get a fresh machine
(lifecycle.md "Start over with a fresh machine"). attach and the rest
keep refusing, with a message that points at this. The fake gains
`DestroyDelay`. `TestRunStartsOverAfterADestroy`,
`TestRunStopsWhenTheDestroyFails`.

**I-302. `repose sync [PROJECT]`.** (owner dogfood, 2026-09-28) A
command of its own for `repose run --no-attach` without a prompt: sync
the checkout, creating or starting the machine if needed, and return.
Flags: `--stash-remote`, `--discard-remote`, `--size`, `--name`, as on
run. `run --no-attach` keeps working. *Rejected:* a sync that never
creates (it would need its own resolve path and error, for no gain).

**I-303. A run with nothing new prints no sync line.** (owner dogfood,
2026-09-28) With the apply skipped (I-224, I-248), run printed
"Synced: 3 modified, 0 untracked; the guest already had them", which
reads as if something happened. An attaching run now prints nothing
about the sync; `repose sync` and `run --no-attach`, which have nothing
else to say, print "Nothing new to sync: the machine already has this
checkout." Any sync that sent something prints the line as before.
`TestSecondRunIsQuietAboutAnUnchangedSync`.

**I-304. The attach after `repose run PROMPT` falls back to the
session.** (owner dogfood, 2026-09-28) "Ready in 6.0s" then tmux's "can't
find window: claude": the agent exited between the prompt and the
attach, and its window went with it. The attach's remote command now
checks the window with `tmux has-session -t <slug>:<window>` (tmux's
`display-message -t` falls back to the current window, so it cannot
check) and, when it is gone, prints "The <window> window closed before
the attach: the agent in it exited. Attached to the session instead;
start the agent again there." to the terminal and in tmux's status line,
and attaches to the session. One ssh, no extra round trip.
`TestAttachFallsBackToTheSessionWhenTheWindowIsGone` (real tmux, pty).

**I-305. `repose attach --bridge` keeps `--bridge` on the fast path.**
(owner dogfood, 2026-09-28) The no-api attach (I-223) built the session
helper's options without the flag. `fastAttachHelper` builds them with
it. `TestFastAttachKeepsBridge`.

**I-306. A guest in bypass mode always skips Claude Code's bypass
warning.** (owner dogfood, 2026-09-28) The likely cause of I-304's exit:
I-250 added `skipDangerousModePermissionPrompt` only together with the
platform's `defaultMode`, so a `defaultMode: bypassPermissions` the
laptop's settings.json carried in (I-196's merge, guest file as base)
came without it. Claude Code then opens its bypass-mode warning on
start, and the prompt and Enter `repose run` types answer that dialog
instead of reaching the agent; declining exits Claude Code and closes
the window. This is inferred from the code and the symptom, not
reproduced on a guest. repose-agent-setup now sets the flag to true
whenever the effective mode is `bypassPermissions` and the key is
absent, whoever set the mode, in the same place as I-283's
`hasSeenAutoDefaultNudge`; the user's own value, false included, is
kept, and the mode itself is never changed. It runs at every agent start,
after the carry. A guest change: it reaches machines with the next base.
guest-base VM test asserts both cases.
**I-320. Config commands show run's ✓ steps, read off the build log.**
(owner request, dogfood 2026-09-28) `repose config add/remove/apply/edit`
waited with a bare op poll and streamed `nix › ` lines; the owner saw no
progress, pressed Ctrl-C, and could not tell what had happened. They now
use run's step renderer (`progress.go`, I-154): "Waiting for a build
slot", "Fetching the base", "Evaluating your config", "Fetching 17/42
paths (123.4 MiB)", "Building 3/12 derivations", "Switching the
machine", each ending as a ✓ line with its time; Nix's lines show under
`-v` or without a terminal (on stderr, where `--json` also sends them).
The op's `phase` cannot drive this alone: the CLI holds the log stream
until the op ends and does not read the op meanwhile, and a config build
never changes `project_state`. So the steps come from named lines in the
log, now a contract (`docs/interfaces/api.md` "Build log lines"): hostd's
existing `evaluating configuration`, `building <name>` and `built <path>`,
two new hostd lines, `waiting for a build slot` (sent at dispatch when the
host's build workers are all busy; the build's own lines continue its
sequence) and `fetching the base` (only when the base is cloned), and one
line the api appends itself, `switching the machine`, when it sends a
build op's apply phase (`buildlog.Store.Note`, numbered after the last
line; hostd's apply command has its own command id and is not bound to the
log). An apply-only op (`config apply` with no file) has no log and uses
the op's `phase` (`apply_config`). The counts are parsed from Nix's own
`these N derivations will be built:` / `these N paths will be fetched (X
MiB download` lines and the `copying path` / `building '…'` lines that
follow, checked against Nix 2.35's non-terminal output. I-57 rejected
parsing phases out of build logs for metrics, where a changed Nix wording
would silently corrupt a dashboard; for a display the failure mode is a
step that stays "Working out what to fetch" until the build ends, and
the parse is not part of the contract. Ctrl-C prints "Interrupted. The
build keeps going on the machine; `repose config show --revisions` shows
when it lands." and exits 130. `TestConfigOpShowsSteps`,
`TestConfigOpInterrupted`, `TestBuildQueueFull`,
`TestSSELiveStreamAndConcurrentLoad`. *Rejected:* an SSE `event: phase`
frame (an older CLI reads any non-`done` frame as a line and resets its
resume cursor to 0); reading the op beside the stream (a second request
per second for every build watcher).

**I-321. `repose config apply` with no file applies the configuration
again.** (owner request, dogfood 2026-09-28) With no PATH and no
`./repose.nix`, it used to fail reading the file. It now takes the
project's newest revision that built (`status` `built` or `applied`,
newest first) and applies it with `POST …/config/revisions/{rev}/apply`,
which the CLI never called: the active one again, or a newer one whose
switch failed. It says which before it starts. A stopped project is told
that it starts on its newest built revision; a revision that changes the
kernel of a running machine (the api's `conflict`) is told to restart.
With a PATH, or with `./repose.nix` present, nothing changed.
`TestConfigApplyWithoutFileReapplies`.

**I-322. Build log lines carry the time they reached the api.** (owner
request, dogfood 2026-09-28) `build_logs` had no time, so `repose logs
--kind build` printed `0001-01-01T00:00:00Z` on every line and `--follow`
had no cursor (it re-printed the whole log every 2 s). Migration
`0009_build_log_ts` (numbered 0008 until it met the launch round's `0008_plans` at merge) adds `ts timestamptz not null default now()` (old rows
get the migration's time; an older api inserting without the column gets
now()); the buildlog store stamps each line when it is appended; the SSE
data and `GET /logs?kind=build` lines carry `ts` (and `kind: build`), and
`since` on build lines keeps those after it. The CLI prints no time for a
line without one (an older api), follows with a nanosecond cursor and
skips a line the previous poll printed, and turns a `--since` duration
(`1h`) into a time: the api reads only RFC 3339, so `--since 1h` had been
ignored for every kind, and for `repose events` too. `TestLogLinesAndSince`
and the build-log assertions in `TestSignInAndProjectsLifecycle`'s events-and-logs
block.

**I-323. `config add` and `config remove` honour `reboot_required`.**
`putMenuAndRender` printed "Applied revision X." whatever the result,
while `applyFragmentAndRender` looked the flag up on the revision. When
a running machine's revision changes the kernel, the api builds it and
does not switch (the project's revision pointer does not move), so
"Applied" was false. Both now read `reboot_required` from the op (which
the api has always sent; the CLI's `Op` did not decode it), falling back
to the revision for `apply`/`edit`, and print "Built revision X. It
changes the kernel, so it applies when P restarts: …". `TestConfigOpRebootRequired`.

**I-324. Port forwards that appear together get one message, in the
status bar's colours.** (owner request, dogfood 2026-09-28) Each new
same-number forward showed its own 4 s `display-message` in tmux's
default yellow; a CLI test suite starting a dozen servers flashed the
status line for most of a minute. A same-number forward is now held until
a poll finds nothing new after it (900 ms quiet, at most 5 s), then the
held ones still forwarded are said together: one as before, several as
`⇄ 10 ports on localhost: 3000, 3001, …` (eight named). A remapped port
(taken on the laptop) and portless's are still said at once, since each
needs its own explanation. The forward itself is never delayed. The
guest's tmux sets `message-style "bg=green,fg=black"`, the default status
bar's colours, for every message rather than restyling the CLI's alone
(a per-session option would be the same thing with more ssh). Guests pick
the style up with the next base. `TestForwarderCoalescesMessages`.

**I-325. hostd refuses an apply whose record says "not running" while the
hypervisor runs.** (dogfood 2026-09-28: `config add cloudflared`
interrupted, the rerun said "already in", cloudflared was not on PATH.)
"Already in" means the api recorded `apply_config` successful: the
revision pointer moves only in that result handler, and not when
`reboot_required`. hostd's `apply` returned success without switching
whenever its own record said the guest was not running (it moves the GC
root for the next boot). Reading the code, the record can differ from the
hypervisor only briefly: every path that sets `error` or `stopped` tears
the hypervisor down first, start and apply share the guest's queue, and a
hostd restart reconciles `stopped`/`error` with an active unit back to
`running`. So this path is not a likely cause of the owner's case, and
the live guest was recreated before it could be checked; guestd's
`Switch` returns an error on a failed `switch-to-configuration`, so a
recorded success means the activation exited 0. The path is still made
loud: when the record says not running, hostd asks systemd whether
`guest@<id>` is active and, if it is, fails the apply (`internal`, "the
machine is running but the host's record says …; nothing was applied")
instead of moving the root, so the revision stays unapplied and `repose
config apply` (I-321) can be run again. `TestBuildAndApply`.

**I-326. Config builds: two derivations at a time, and two reads in
parallel.** Cheap wins only (owner's brief). hostd ran `nix build
--max-jobs 1`; a non-trusted client's `max-jobs` does reach its daemon
worker (it is a fixed field of the daemon's set-options), so the chain of
small local derivations at the end of every system build
(home-manager files and generation, system-path, etc, units, the
toplevel), several of them independent, was built one after another.
It is now 2, the host daemon's own `max-jobs` (`nix/hosts/gc.nix`) and
workstream 12's number; substitution was never limited by it. `repose
config add` reads the catalog and the config at the same time (one round
trip instead of two). Nothing was measured: this box is not a host.
Bigger ideas, not done: clone each new base on every host when it is
published rather than at its first build (the clone is on the first
build's path); evaluate with the eval cache or a persistent evaluator
(every build evaluates the whole NixOS system from scratch); send only
the paths the guest lacks in `Switch`'s registration (`nix-store
--dump-db` of the whole closure on every apply); stream the closure-size
check and GC-root work after the result instead of before.

**I-327. The "Config" docs page is "Installing software".** (owner
request) The page keeps its file and URL (`/docs/config`; the docs routes
are the file names, so no link breaks and no redirect is needed) and the
command stays `repose config`. Its opening now sets it against
installing on the machine (The machine, "Installing more"), which links
back, so the two pages no longer read as two answers to the same
question. It describes the new step output, Ctrl-C, the kernel-change
case and `config apply` with no file.
**I-310. `repose browser [PROJECT]` is the machine's own browser, on its
desktop; `repose browser bridge` stays the laptop's Chrome; `repose open
--desktop` stays as the same command.** (dogfood round, owner approved the
tree, 2026-09-28) The owner looked for the agents' browser under `repose
browser` and found only the bridge; the desktop was a flag on `open`,
which is about ports. Now `repose browser` (with `--stop` and
`--no-browser`) runs what `open --desktop` ran (OpenDesktopCmd,
StopDesktopCmd, unchanged), and `open --desktop [--stop]` keeps working
with its cli.md rows saying it is the same as `repose browser`. Cobra
routes `repose browser bridge ...` to the subcommand and `repose browser
anything-else` to the parent with that word as PROJECT, since the parent
has its own `Args`; a project literally named `bridge` is `repose browser
--project bridge`, which cli.md says. `TestBrowserCommandTreeParses`
covers both shapes, flags before the project, and two arguments as a
usage error. The user docs now say `repose browser` wherever they said
`open --desktop`; the landing page (W4's lane) still shows `repose open
--desktop`, which keeps working. *Rejected:* `repose browser desktop` (a
third word for the common case); removing `open --desktop` (it is in
released docs, the landing, and people's history).

Amended at merge (2026-09-28, conductor): the launch round's I-292 had already made `repose browser [PROJECT]` the machine's browser, with a background forward and the password in the link, and `repose open --desktop` a hidden old name. I-292's command is the one kept; this entry's own `browser` command (foreground forward, printed password) was dropped at the merge. What stays from this round is `bridge` as a subcommand with I-311's `--allow`.

**I-311. The bridge enforces what the agents may do in the laptop's
Chrome itself, at the CDP layer: always-on refusals, and `--allow HOST`
enforced by a CDP connection of the bridge's own.** (dogfood round, owner
approved "enforcement in the bridge, not an MCP flag", 2026-09-28) An
agent on the machine has a shell and can speak CDP to 127.0.0.1:9224
itself, so Playwright MCP's `--allowed-origins` or anything else the MCP
servers are told is advice. The front (I-296) already sat between every
tool and Chrome; it now reads the browser websocket message by message
(its own RFC 6455 framing, `bridgews.go`; the upgrade's
`Sec-WebSocket-Extensions` is removed so nothing is compressed, and a
frame with a reserved bit ends the connection). A tool's message is
decoded, checked, and re-encoded from what was decoded, so Chrome gets
exactly what was checked (two spellings of one key cannot mean one thing
to the check and another to Chrome). Only the browser websocket and
`/json/version` are served; the other `/json` endpoints (`/json/new?url`
went round any check) and per-page websockets are 404, which both MCP
servers never use (I-296's live check used `PUT /json/new`; it now 404s).

Always, with or without `--allow`: refused are closing or crashing Chrome,
file inputs and file drags (laptop paths), `DOM.getFileInfo`, permission
grants (laptop clipboard, camera, microphone), turning off certificate
checks, `Target.exposeDevToolsProtocol`, the `Extensions` and `Tethering`
domains, unflattened sessions (`Target.sendMessageToTarget`, and
`attachToTarget`/`setAutoAttach` without `flatten: true`, whose traffic
the front could not read), and navigations or new tabs to anything but a
web page (`file:`, `chrome:`, `chrome-extension:`, `view-source:`,
`devtools:`, `about:` other than blank). Targets showing such pages
(Chrome's settings, extensions' pages and background workers) are hidden
from target lists and events, and one a tool's auto-attach picks up is
detached by the front with a command of its own whose answer is dropped.
Cookies do not leave Chrome: `Network.getCookies`, `getAllCookies`,
`Storage.getCookies` and the two cookie clears are refused, and Cookie,
Set-Cookie, `headersText` lines and the cookie lists are removed from
network and fetch events; the bridge lends logins while it is open, and a
copied cookie would outlive it. `Browser.setDownloadBehavior` (and
`Page.`) is answered as done and dropped: Playwright sends it on every
connect with a folder on the machine (refusing it failed
`connectOverCDP`, found by `TestBridgeWithPlaywright`), and passing it
would let a tool pick where on the laptop a file is written.

With `--allow` (repeatable or comma-separated; `example.com` is that host,
`*.example.com` is it and every subdomain; no scheme, port or path): a
tool's `Page.navigate` and `Target.createTarget` off the list get a CDP
error naming the host and saying to ask the user; targets are visible to
the tools only when on the list, or a blank tab they or a visible page
opened, and a visible tab stays theirs ("lent") whatever it shows next;
cookie writes and storage clears for other hosts are refused. The
boundary is the warden (`bridgewarden.go`): a second CDP connection the
CLI makes before the tunnel opens, which auto-attaches to every tab and
frame with `waitForDebuggerOnStart`, enables `Fetch` for Document
requests there, and fails with `BlockedByClient` every document off the
list in a lent tab, whatever started it (script `location=`, click,
redirect, form post, popup with or without opener, frame); a lent tab
that shows a page off the list without a request the warden saw (a
back-forward cache restore, a `chrome://` page typed into it) is sent to
about:blank. Your other tabs are continued untouched. If the warden's
connection ends, the bridge ends (`errWardenLost`) rather than run
without the list. Through Chrome's switch this is one more "Allow"
dialog, at the start, which the CLI announces.

Evidence against a real Chromium 154 (`TestBridgeAgainstChromium`,
opt-in with `BRIDGE_TEST_CHROMIUM`): none of navigate, script, redirect,
popup, noopener popup, form post and iframe reached other.test; the
user's pre-existing tab on other.test was hidden and not attachable, and
still navigated freely. Playwright 1.63 (the guest's playwright-core,
`TestBridgeWithPlaywright`) and chrome-devtools-mcp 1.10.1 over MCP stdio
(`TestBridgeWithChromeDevtoolsMCP`) work through the front with and
without `--allow`. Fake-peer unit tests cover every refusal, the
scrubbing, hiding and the injected detach.

Gaps, stated in the user docs: requests a page on an allowed site makes
for images, scripts or fetch to other hosts are not blocked, and carry
whatever cookies those sites allow cross-site (the tool cannot open or
read those sites' pages); a page's own JavaScript can read what the page
can (non-HttpOnly cookies, local storage); a tab the user opens on an
allowed site while the bridge runs becomes visible to the tools; frames
already loaded in a page open before the bridge are not re-checked.
*Rejected:* an MCP flag (not a boundary, above); blocking every
subresource off the list (breaks nearly every site's CDN and fonts, and
the user would have to list hosts they never see); holding every tab to
the list (the user's own browsing would break while a bridge runs);
refusing `setDownloadBehavior` (breaks Playwright); an HTTP proxy for
Chrome (the laptop's Chrome is the user's, and its proxy is theirs).

**I-312. The bridge needs a running machine and does not start one; there
is no detached bridge.** (dogfood round, 2026-09-28) `code`, `exec` and
`ssh` all refuse a stopped machine (`connectRunning`: "none of them starts
a machine", exit 5 with `repose start` named), so the bridge, which is for
an agent that is working, does the same rather than be the one command
that starts a machine as a side effect. The docs say it needs a running
machine and what to run. A background bridge is not offered, on purpose:
a bridge nobody can see is one the user forgets is open; the two shapes
are `--bridge` (lives as long as the attach) and a terminal running
`repose browser bridge`. The user page leads with `--bridge`, one
terminal, and puts the two-terminal flow second.

**I-313. Closing a bridge is one ssh, bounded at 4 s, and never waits for
a tmux client.** (dogfood round, owner saw Ctrl-C hang, 2026-09-28)
`BrowserBridgeCmd` printed "Bridge closed" and then ran `bridge stop` (up
to 10 s) and `tmuxMessage`, which polls `list-clients` for 8 s when
nobody is attached, with an always-true alive callback. Now `bridgeStop`
runs `repose-guest-profile browser bridge stop` and, in the same ssh,
`if tmux list-clients ... | grep -q .; then tmux display-message ...; fi`,
bounded by `bridgeStopWait` (4 s; over the ControlMaster it is a fraction
of a second, and a guest that does not answer has switched back by
itself through the hold's EXIT trap or the idle guard). The "on" message
at the start uses the same one-shot check. Measured with the fake guest
(testguest, real ssh), cancel to return: 8.191 s before (old
`bridgeStop` + `tmuxMessage`), 54 ms after
(`TestBridgeCloseReturnsPromptly`). session.go's `tmuxMessage` is
unchanged (W3's lane); only the bridge stopped using it.

**I-314. The bridge prints a navigation log on the user's own terminal:
time, host and path, `blocked` or not, never a query or fragment, and
nothing is stored.** (dogfood round, 2026-09-28) One line per top-level
page load the tools see (`Page.frameNavigated` without a parent, deduped
by loader id across both MCP servers' connections) and one per page the
allowlist stopped (deduped for 2 s per place, so a refused navigate and
the warden's failed request are one line). Queries and fragments carry
sign-in and reset tokens, so `logPlace` keeps host and escaped path only,
path cut at 80 characters. CLAUDE.md's "never log what a tenant typed"
is about repose's logs; this is printed to the user's own terminal about
the user's own browser, goes to no file, no api and no log, and the docs
say so. With `--bridge` there is no terminal of its own, so only the
`blocked` lines appear, as tmux messages. Without `--allow`, the tools
usually attach to every tab, so the user's own page loads are listed too;
the docs say so.

**I-315. `--bridge-allow HOST` on `run` and `attach` is the allowlist for
`--bridge`, and implies it.** (dogfood round, 2026-09-28) The one-terminal
flow is the one the docs lead with, so the allowlist belongs there too.
It is cheap: a `RunOptions.BridgeAllow` and a `sessionOptions.BridgeAllow`
(JSON `bridge_allow`) carried to the session helper, which runs the same
`runBridge` with the same policy; the values are checked as a usage error
before anything connects. A separate name, not `--allow`, because on
`run` a bare `--allow` reads as a permission for the agent. This touches
W1's `run.go` (one field, one line) and session.go's options (not
`tmuxMessage`).

**I-316. The user's Chrome has one page, "Lend the agents your Chrome"
(`/docs/your-chrome`), under Using repose; the tutorial page it replaces
is removed.** (dogfood round, owner asked for the feature "documented very
well", 2026-09-28) This amends I-297's tutorial list: the Chrome tutorial
had become half a reference, duplicated in machine.md. The page covers
what the bridge is, Chrome's switch and per-connection dialog, `--bridge`
first and the second terminal next, `--allow`, the page list, what the
agents can and cannot do (and I-311's gaps), how to stop it, and
troubleshooting (a stopped machine, no Chrome dialog, the tools can't
connect, a bridge still held, `Ctrl-b` inside the user's own tmux).
machine.md keeps a short "Use your own Chrome" section pointing at it
(the agent guide's anchor), cli.md has rows for `repose browser`,
`--allow`, `--bridge-allow`, and the agent guide tells agents that a site
off the allowlist fails with a named error and to ask the user rather than
work around it. Links to `/docs/tutorial-your-chrome` in the docs now go
to `/docs/your-chrome`; the old URL goes on to the new one (a
client-side redirect the conductor added at merge, `movedDoc` in
`apps/web/src/lib/docs.ts`, because the link had been shared).

**I-328. The build log stream reads the table on its tick, so lines
another process stored arrive while the op runs.** (conductor, dogfood
round live check, 2026-09-28) Live, `repose config add sl -v` printed
nothing for 12 s and then every line, "evaluating configuration" included,
within 60 ms at the end; I-320's steps therefore all read 0.0s. hostd's
lines go to `api-grpc`, which stores them and tells its own subscribers;
`GET …/ops/{id}/log` is served by `api`, a separate process, whose
subscription only hears lines appended there, and whose one-second tick
checked the op's state and sent a keepalive without reading the table. The
lines reached the client only in the final catch-up at `done`. The tick now
reads new lines (the same `catchUp` as at connect, seq-ordered, from the
last id sent) before the keepalive, every 500 ms instead of every second,
so a line waits at most half a second. One indexed query per open stream
per tick, and a stream is open only while its build runs. Postgres
LISTEN/NOTIFY across the two processes would be exact, and was left for
when the tick's cost shows. Test: `TestSSEDeliversLinesAnotherProcessWrote`
(a second `buildlog.Store` writes after the stream's first catch-up; fails
before the change, the line arrives 1 ms after it is stored after it).
**I-299. A first sign-in with no GitHub identity takes its handle from the
email address, the part before the `@` and before any `+tag`; `user-<sub>`
is left for an address with nothing usable there.** (owner, 2026-09-28:
"login process is shit ... log in with email and not github to see the full
mess") Amends I-100. Email sign-in is a real path now (I-340), and I-100's
fallback gave every such user a handle like `user-c7fh26yzrl93`, which is
the SSH login suffix, the certificate `key_id` and the tail of every
preview hostname (I-6). `auth.Provisioner` now tries the GitHub login,
then Logto's username, then the address's local part, then `user-<sub>`;
the local part goes through `DeriveHandle` and collides the way a login
does (`first-last`, `first-last-2`). An address whose local part has no
character in `[a-z0-9]` (`日本@example.com`) keeps `user-<sub>`, so those
users don't all share `user`, `user-2`. The handle is still fixed at first
sign-in (I-100): existing rows keep theirs, and linking GitHub later
changes nothing; `repose-admin users rename` is the repair while a user
has no projects. `TestFirstSignInCreatesUserAndCollisionsSuffix`.
*Rejected:* asking for a handle at first sign-in (a form between Logto and
the dashboard, and the CLI's device flow has no page to show it on);
Logto's username as a required sign-up field (the tenant is shared with
the recruiting app, whose users would be asked too); rederiving at each
sign-in (I-100's reason: a login name that changes under a user's SSH
config).

**I-340. repose and the recruiting app (Job Alerts) share the Logto tenant
at `accounts.herakraft.co`; everything a person sees there names the app
they came from, and the setup lives in `ops/logto/`.** (owner, 2026-09-28)
I-84 put repose on the owner's existing Logto, which had been set up for
Job Alerts: the sign-in page showed the recruiting logo, every
verification email was titled "Your Job Alerts sign-in code" from
`login@`, threaded with every earlier code in Gmail, and the dashboard's
buttons said "Sign in with GitHub" over a page that leads with email. One
tenant stays (open-source Logto has one tenant per instance, and a second
instance is a second thing to run); what differs per app now comes from
the application:
- Application names are what the emails say: `repose` for the dashboard
  and the CLI, `Job Alerts` for the recruiting app. The SMTP connector's
  templates use `{{application.name}}` in the subject, the sender name
  (`sendFrom`) and the body; Logto fills it for every usage type except
  Generic and OrganizationInvitation, which say "Herakraft". Logto's
  substitution is a regex with no conditionals, and a variable whose root
  is missing stays in the email as typed, so no other variable is used
  but the logo below.
- The code is in the subject (`278278 is your repose sign-in code`), so
  each email is its own thread and the code reads from the notification.
- Each application has its own sign-in experience: the logo (repose's
  r-mark, the recruiting app's icon, both PNG over https because the
  sign-in emails show it through `{{application.branding.logoUrl}}` and
  Gmail shows neither SVG nor `data:` images; only the four sign-in usage
  types carry it, since account-center emails have no logo), and for
  repose the terms and privacy links. The tenant's custom CSS puts both products'
  shared house style on every page (paper, hairlines, 3px corners, Noto
  Serif titles). Logto's "Powered by" badge stays: the open-source build
  refuses `hideLogtoBranding` and pins the badge with inline styles.
- The fallback for an unknown session is `https://repose.herakraft.co`.
- Automatic account linking is on (a social sign-in whose verified email
  matches an account joins it). The owner's GitHub identity, which had
  made a second empty account on 2026-09-28, was moved onto the email
  account that holds the projects, and the empty one deleted.
- The dashboard's buttons are "Get started" and "Start a free week"; the
  header keeps "Sign in". `repose login` prints Logto's
  `verification_uri_complete`, so the page opens with the code filled in.
`ops/logto/README.md` has each setting, where it lives in the console,
and how to rebuild the templates (`ops/logto/emails/build.py`).
*Rejected:* a second Logto instance for repose (one more service and
database for a difference that application settings cover); product names
typed into the templates (the recruiting app would have read "repose");
Logto's per-language email templates (they have no per-app variant
either); hiding the badge through CSS tricks against Logto's pinning.

**I-344. The docs sidebar is a drawer below `lg` that keeps its state,
and the docs are prerendered; the docs stay in-house.** (docs site,
2026-09-28; owner note: no side menu on phones, the docs don't keep
state, and perhaps a docs framework styled like the site would do)
On a phone the sidebar was a full-screen sheet behind a text "Menu"
button, shown and hidden with `display: none`, so every opening started
at the top of the list with the search cleared, and it covered the page
it was opened from. Now, below `lg`:
- A hamburger at the left of the header opens a drawer from the left,
  `min(20rem, 85vw)` wide, over a dimmed backdrop. A tap on the
  backdrop, Escape or the button closes it; the page behind doesn't
  scroll while it is open; focus goes into the drawer and back to the
  button.
- The drawer is always mounted and moved off screen when closed
  (`invisible -translate-x-full`), so its scroll position and the
  search query survive opening, closing and navigating. Opening it
  scrolls the current page's link into view only when it isn't.
- A navigation closes it, a search hit's `#heading` on the same page
  included.
At every width the sidebar's scroll is kept in `sessionStorage` per tab
and restored on a reload, and a page opened directly scrolls its own
link into view. Pages with more than one section get a folding "On this
page" under the description below `xl`, where the right rail is absent.
`/docs` and every `/docs/<slug>` (and the old slugs that redirect) are
prerendered with SSR on, as the legal pages already were: a reload
paints the page before scripts run, so the browser restores the reading
position, and curl or a crawler reads the text. An unknown slug is still
rendered on request (`prerender = 'auto'`) and says there's no such
page. The header's sign-in state fills in once the page runs.
*Rejected:* Sveltepress, Starlight or another docs framework. The docs
share the app's header, sign-in state, tokens (`--page`, `--rule`,
`--sunken`), fonts and search, and are ordinary routes of the one
SvelteKit app. A framework would own the routes and the theme, and
keeping the design would mean restyling its theme to match. The two
missing behaviours, a drawer and kept state, were each a small change
to one layout.

**I-345. Docs code blocks scroll; the docs are written to fit the
column.** (docs site, 2026-09-28; owner note: the install command was
wrapped onto a second line, "I think the fix should have been to just
rewrite the commands properly ... Just update the content itself")
Supersedes the wrapping of e94f36e (pre-wrap with a hanging indent per
line). A wrapped command reads as two commands. A `pre` scrolls sideways again (`white-space: pre`), as in a
terminal. The column from 1280 wide up, the narrowest above a phone
(582 px, beside the right rail), holds 70 columns of the 13 px
monospace, and the Copy button covers the last 8 of a block's first
line, so every line of every block is at most 70 columns and the first
line of a block with a Copy button at most 62. `docs.test.ts` fails on
a longer line. Below `sm` the Copy button gets a strip of its own above
the code, since most lines are wider than a phone. To fit, the examples
were rewritten: shorter prompts and names, flags split with a trailing
backslash (a long prompt continues inside its quotes with `\`, which the
shell joins), comments aligned tighter. Output the CLI prints on one
long line (the sync refusal, the bridge banner, `repose stop`) is broken
at a phrase with its words unchanged; the one table row that can't be
(`repose ls`, 79 columns) shows its agent column cut as `…`. The
install one-liner stays one line and scrolls on a phone. The
conductor tutorial's prompt also lost the backticks around `npm test`,
which the shell would have run inside the double quotes.
*Rejected:* wrapping with a hanging indent (e94f36e, above); a smaller
font for code (below 13 px it is hard to read); widening the column
(the reading measure of the prose would suffer); shortening the CLI's
own messages to fit (a CLI change with its own spec in the design doc,
not a docs fix).

**I-341. On macOS, Cmd+V with an image on the clipboard pastes it, by a
watcher that gives an image-only clipboard the path of a copy.** (paste,
07, 2026-09-28; owner note on "press Ctrl+V, not Cmd+V": "this is a
downgrade. can we fix it?") With only an image on the clipboard,
Terminal, iTerm2, Ghostty, kitty and WezTerm send the terminal nothing
for Cmd+V, so the input proxy (I-280) has no byte to act on. While the
proxy runs on macOS, one `osascript -l JavaScript` process polls
`NSPasteboard.changeCount` every 250 ms. When the clipboard has
`public.png` or `public.tiff` and neither `public.utf8-plain-text` nor
`public.file-url`, it writes a PNG copy to
`~/Library/Caches/repose/clipboard/clipboard-<time>.png` (0700
directory; TIFF converted) and writes the clipboard back with every type
it had plus plain text, that path. Cmd+V then pastes the path as a
bracketed paste, which the proxy already treats as a drop: the copy
goes to `/tmp/repose-paste/` on the machine and its path there is typed
in its place, so Claude Code shows `[Image #1]`. The image types stay,
so an app that takes an image still gets one; a plain text field gets
the path, which /docs says. The write-back is skipped when the change
count moved while the copy was being made. At the end of the session the
CLI kills the watcher and runs a second osascript (bounded at 2 s) that
takes the text type off again, only when the change count and the text
are still the watcher's; a clipboard copied since is left alone. The
watcher prints a heartbeat line every 2 s, so when the CLI dies without
stopping it, the write fails and osascript exits. Copies older than a
day and all but the newest 20 are deleted when a watcher starts.
`REPOSE_CLIPBOARD_PATH=0` turns the watcher off (Ctrl+V still works);
`REPOSE_INPUT_PROXY=0` turns off the proxy and the watcher with it.
Two attached terminals converge: a clipboard that already has text is
skipped, and a restore by one lets the other add the path again. Tests:
`TestClipWatchStopRestoresTheLastSet` (stand-in scripts for both
osascript runs), `TestParseClipSet`, `TestPruneClipboardDir`,
`TestClipboardPathIsADrop`, `TestClipboardWatchEnabled`; both scripts
pass `node --check`. Not run on a Mac from this machine: the JXA bridge
calls (`dataForType`, `NSPasteboardItem`, `writeObjects`) are to be
checked live before release notes claim the feature.
*Rejected:* a key handler for Cmd+V (the terminal sends nothing);
reading the clipboard on every bracketed paste (Cmd+V with an image
sends no paste at all); a token in place of the path (a plain text field
elsewhere would get a meaningless string, where a path names the file);
`pngpaste` for the watcher (not installed by default, and one process
per poll).

**I-342. `--worktree` names are `<slug>-worktree-<N>` on branch
`worktree-<N>`, numbered apart from the window.** (worktrees, 07,
2026-09-28; owner note: "name a worktree something standard. Maybe
project name-worktree-number", and "repose/repose/claude-2 ???")
Supersedes I-253's `~/<slug>-<window>` on `repose/<window>`. The branch
`repose/claude-2` arrived on the laptop as `repose/repose/claude-2`,
since `git fetch repose` files every branch of the machine under
`repose/` (I-272). Now the directory is `~/<slug>-worktree-<N>` and the
branch `worktree-<N>`, which the laptop sees as `repose/worktree-<N>`. N
is the lowest number from 1 whose directory and branch are both free
(a branch left after its directory was removed still holds its number),
so a worktree is still never reused. The tmux window keeps the agent's
name, `<agent>` or `<agent>-N`, picked like any other window; guestd's
`sample.AgentOf`, hooks and questions read the agent from the window
name, so the window can't be named after the worktree. `run` prints
`Worktree: ~/todo-app-worktree-1 on branch worktree-1`. Worktrees made
before stay as they are; nothing reads their names. Tests:
`TestRunWorktree` (numbering, window names apart, a left branch holding
its number, cleanup freeing it), `TestRunWorktreeThenPlainRun`,
`TestReposeRemote*` (the laptop's `repose/worktree-1`).
*Rejected:* `<slug>-<agent>-worktree-<N>` (long, and the agent is on the
window already); keeping `repose/` in the branch (the double prefix is
what the owner asked about); naming the window `worktree-N` (breaks the
agent-from-window rule above).

**I-343. A `--worktree` gets the checkout's gitignored `.env` files.**
(worktrees, 07, 2026-09-28; owner question: is "gitignored files such as
.env aren't copied" true? It was.) `git worktree add` checks out tracked
files only, so an agent in a worktree had no `.env`, and a dev server or
test that reads one failed there while it worked in the checkout. The
worktree command now copies, in the same ssh, every gitignored `.env`
and `.env.*` file of the machine's checkout (the set a sync carries,
I-197), keeping mode and time, never following a symlink or overwriting.
Ignored directories are listed collapsed (`node_modules/`), so no
dependency tree is walked and a `.env` inside one isn't copied.
Submodules aren't checked out in a worktree and get none. `run` prints
`Copied N .env files from ~/<slug>` when there were any; the contents
never reach a log. Other gitignored files (build output, databases,
`node_modules`) are still not copied. Test: `TestRunWorktree` (a root and
a nested `.env.local` copied with 0600 kept, `build.log` and
`node_modules/pkg/.env` not, the worktree's status clean).

**I-346. `repose cp` takes several sources, and an argument refusal
names what it got.** (07, 2026-09-28; the owner's `repose cp ./Fwd_*
partition-poster:/tmp` failed six times with "takes a source and a
destination".) The glob expanded to more than two words, `cp` accepted
exactly two, and its message was the same for every count, so it read as
a syntax problem when the syntax was right. `repose cp [-r] SRC... DST`
now works as scp does: the sources are all on one side and, when on the
guest, name one project; they go into the directory DST. Mixed sides, two
projects, and no guest side are usage errors that print the arguments.
`:izma:/tmp` (a leading colon before a project name) is refused with
"write izma:/tmp", since it otherwise means the relative path `izma:/tmp`
in this checkout's project. To keep the class from coming back:
`gotArgs` spells out a refused argument list ("got 4 arguments: …"),
`repose scan` uses it too, and `TestArgErrorsSayWhatTheyGot` walks every
visible command, gives it nine words and none, and fails when a refusal
is not a usage error or does not name the count (`repose exec` is exempt:
its refusal is the missing `--`). The walk found that cobra's
MinimumNArgs refusal ("requires at least 1 arg(s)", `repose config add`
with no package) exited 1 instead of 2; it now exits 2 like the rest.
`docs/CHECKLIST.md` asks for the test and one run with a glob for every
command that takes paths. Tests: `TestCpBothWays` (two files each way,
one name with a space, the four refusals), `TestCpArgs`.
*Rejected:* expanding a glob inside repose (the shell already did, and a
quoted remote glob already goes to the guest as scp's); a clearer message
alone without the multi-source form (the user's intent was plain and scp
supports it).
**I-347. `repose run --temp` makes a temporary machine: it lives 24
hours from creation, is destroyed with no snapshot, and `repose keep`
makes it a normal project.** (owner, 2026-09-28; built by I-348..I-355)
The owner wants a machine with no project, no git and no checkout, which
nobody expects to find the next day. Today that takes `run --name X
--no-sync` and a `repose rm` you have to remember, and the destroy keeps a
snapshot for 30 days that nobody wants. The spec is in
`features/stop-start-destroy.md` "Temporary machines". The choices:
- The lifetime is wall-clock time from creation: 24 hours by default,
  `--temp DURATION` for less, 10 minutes at least and 24 hours at most.
  An idle timer (Codespaces, Coder, Daytona) exists to free storage, and
  its users report agents stopped while they still worked.
- While an ssh session or tmux client is open, or an agent is working, the
  destroy waits and the api looks again each minute, up to 24 hours past
  the expiry. Nothing is stored for the delay: the reaper decides from
  `expires_at` and the latest meter sample.
- At expiry the plan is `[destroy_guest]`. DestroyGuest stops a running
  guest itself, so hostd does not change. `markDestroyed` sets the
  project's snapshots to expire at once, and `snapshots.Expiry` deletes
  any the nightly 03:00 run took. A temporary project cannot be restored,
  and `repose rm` on one says so.
- `--temp` changes where the machine comes from, and the sync works as
  it does on any run: in a checkout it sends the checkout, so the owner
  can try a branch on a throwaway machine, and in a directory that is not
  a git repository it skips the sync and prints `Not a git repository,
  so nothing was synced.`, since an empty machine is the only thing it
  could mean there. A repository with no commit or a shallow clone still
  refuses (the owner meant to send code), and every refusal of that kind,
  temporary or not, now comes before the create instead of after the
  machine has booted. The
  CLI writes no `by_dir` entry and adds no `repose` git remote (that name
  belongs to the checkout's own project; the agent's commits are fetched
  by URL, `git fetch <name>.repose:~/<name>`). The project's `remote_url`
  is null, as a fork's is (I-254), so a temporary machine made in a
  checkout never collides with that checkout's project, and the first
  sync sends the whole history itself instead of having the guest clone
  from GitHub (I-203). The name is `--name` or `tmp-` plus four base32
  characters. The prefix shows in `ls`, the SSH hosts file and logs;
  there is no word list to keep.
- Ending the tmux session (exiting its last window, as against detaching)
  destroys the machine right away, as `docker run --rm` does. The CLI
  checks `tmux has-session` over the open ControlMaster after the attach
  returns, which works only on the input-proxy path. Elsewhere (Windows,
  no TTY, `REPOSE_INPUT_PROXY=0`) the machine waits for its expiry.
- `repose keep NAME` sends `PATCH /projects/:id {expires_at: null}` and
  the project becomes a normal one, with no remote.
- A temporary machine counts toward the project limit and the plan's
  memory and disk while it lives, like any project. The gate does not
  change; a Solo account at 10 projects cannot open one.
- One `temp_expiring` notification goes out an hour before the expiry,
  and `temp_destroyed` when the destroy finishes. "Once" is read from the
  events table (I-293(3)). `run`, `attach` and `ls` print the time left.
- `projects.expires_at timestamptz null` (migration 0010). I-262 kept the
  idle state out of `projects` because every row would pay for a rare
  state; this one is set at create and is what makes the project
  temporary, and the reaper's query needs it indexed.
- The reaper runs on the api's per-minute tick under `LockSweeper`
  (1007, declared and unused until now), a row per transaction with `for
  update skip locked` and the rule checked again inside, as
  `snapshots/expiry.go` does. It enqueues with `allowQueue=true`, so an
  op already open delays the destroy instead of failing it. The `daily`
  ticker would not do: a redeploy restarts it (I-112).
R1-5, I-200 and I-262 say the platform never stops or reaps a machine
because it looks unused. That stands. A temporary machine's end is a
lifetime its owner chose when creating it, and `repose keep` takes it
back. The interface changes (`POST /projects` `expires_in_s`, `Project`
`expires_at`, `PATCH` `expires_at: null`, the two event kinds, the
`--temp` flag and `repose keep`) ship with the code, in `api.md`,
`db-schema.md` and `cli.md`.
*Rejected:*
- An idle timer, which stops agents that work with nobody attached.
- Keeping the final snapshot, which costs Blob storage for a machine the
  owner said nobody would want back.
- Asking before the destroy when the tmux session ends, which adds a
  prompt to the one path where the owner has already said they are done.
- A temporary machine outside the project limit, which would need a
  second gate for a rare case.
- A generated word pair for the name: `tmp-` shows what the machine is.
- `--temp` implying `--no-sync` (the owner, same day): trying a
  checkout on a machine that will not outlive the test is half of what
  the flag is for.
- Skipping the sync in a directory with no repository for every run, not
  only `--temp`: a project meant to last, made in the wrong directory,
  would come up empty and the owner would find out when they looked for
  the code.

**I-348. An explicit `--name` on `run` and `sync` means the project with
that name; a new one in a checkout whose remote is taken has no remote.**
(07, 2026-09-29; the owner's `repose run --no-sync --name boxd` in `~`
attached to `issuer-migration`.) `resolveProject` returned the `by_dir`
entry for the directory before `--name` was looked at, and `--name` was
used only when nothing resolved. The same order sent `run --name X` in a
checkout to the checkout's own project, while `/docs/lifecycle` "A second
machine for the same repository" promised a new project called X. Now
`resolveForRun` looks `--name` up first, by name or by the slug the name
gets, wherever the command runs: found, it is the project; not found, the
run creates it. It never lands on a project of another name. A new NAME
takes the checkout's remote only when no project has it yet (a first
project for a repository, named); otherwise, since the database allows
one live project per `(user, remote_url)` (`projects_user_remote`), it
gets `remote_url` null as a fork's copies do (I-254), is reached by name,
is not written to `projects.json`, and gets no `repose` git remote (that
name stays with the checkout's own project; `checkoutOwnsProject` already
says no). Its first sync sends the whole history. A NAME that is another
repository's project (both have remotes and they differ) exits 2 instead
of syncing one repository into the other's machine. `--name` with a
different `--project` exits 2. With `--name` or `--temp` the early probe
(I-223) is skipped: it would create the checkout's directory in the
cached project's guest. For the home directory: a `--name` project with
no remote is still written to `by_dir` (it has nothing else to be found
by, I-152), so a plain `repose run` there lands on the last one made. That
stays, and when the directory is not a git repository the run says so on
stderr: `Using boxd, the machine last made in this directory with
--name. ...`, naming `--name NEW` for another and `--temp` for a
throwaway one. `--temp` there always makes a new machine (I-351).
*Rejected:*
- Dropping `by_dir` for directories that are not repositories: a plain
  `repose attach` or `repose status` in `~/scratch` after `run --name
  scratch` would stop working, which I-152 built on purpose.
- Letting the second project take the remote from the first: it would
  move `repose run` in the checkout to the experiment.

**I-349. Temporary machines in the api: `expires_at` (0010), the plan
without a snapshot, and `keep`.** (05, 2026-09-29; builds I-347.)
Migration `0010_project_expires_at` adds `projects.expires_at timestamptz`
and a partial index on it where it is set on a live row. `POST /projects`
takes `expires_in_s` (600..86400, `400 invalid` outside, or with a
`remote_url`); `Project` carries `expires_at` only while it is set, so an
older client sees nothing new. `PATCH /projects/:id` takes `expires_at`
as a raw field: absent leaves it, `null` clears it (`repose keep`), any
other value is `400 invalid`, and on a project already `destroying` it is
`409 conflict`, since the destroy is under way. `PlanDestroy` for a
temporary project is `[destroy_guest]` whatever its state: hostd's
DestroyGuest stops a running guest itself, and a `repose rm` of one uses
the same plan. `markDestroyed` sets the project's snapshots to expire now
(`least(coalesce(expires_at, now()), now())`, so the second call from
`finish` does not move them), the snapshot expiry deletes a nightly one
on its next run, and `RestorableSnapshotWhere` already hides them from
`ls --destroyed` and `restore` meanwhile. `temp_destroyed` is recorded
only when the op has `params.expired` (the reaper's) and only by the call
that marks the row, so a `repose rm` or a session end, which the user
asked for, sends no notification.

**I-350. The reaper: once a minute under `LockSweeper`, a row per
transaction, with a backoff after a failed destroy.** (05, 2026-09-29;
builds I-347.) `internal/api/temp` runs on the api's minute tick under
`db.LockSweeper` (1007). It first raises `temp_expiring` for projects
whose `expires_at` is within the hour, made with a lifetime of more than
an hour (an hour or less would warn at once), and with no such event yet.
Then each expired project not `destroying` or `destroyed` is locked with
`for update skip locked`, checked again, and skipped when a destroy op is
open, when the last destroy failed less than 10 minutes ago (each failure
sends `destroy_failed`, and I-347's "each minute" would send up to 60 an
hour for a host that cannot delete a volume), or when the newest sample,
under 10 minutes old and before `expires_at + 24h`, shows an ssh session,
a tmux client, or an agent that is not `idle` or `needs_input`. A sample
with guestd not answering does not hold it, unlike the idle warning's
count of use: it says nothing about anyone, and the 24 hours bound the
wait anyway. Otherwise the destroy is enqueued with `allowQueue=true` and
`params.expired`, and the project marked `destroying` in the same
transaction, as `DELETE` does. The plan names `destroy_guest` even for a
project with no guest yet: an op it queues behind may be the create that
places the guest, and the phase reads the guest when it is sent (and
skips with none); a plan fixed at enqueue as `[]` would have ended with
the project marked destroyed and its guest left on the host.

**I-351. `--temp` in the CLI: flag, name, cache, lines.** (07,
2026-09-29; builds I-347.) `--temp` is a string flag with cobra's
`NoOptDefVal`, on `run` and `sync`. Bare, it is 24h; `--temp=3h` always
takes the value; `--temp 3h` takes the argument after it when that parses
as a Go duration (`3h`, `90m`, `1h30m`), so a prompt that begins with a
duration needs the `=` form. Outside 10m..24h it exits 2. It never
resolves, never writes `projects.json`, and with `--project` (or a
`PROJECT` on `sync`) exits 2; `REPOSE_PROJECT` is ignored. The name is
`--name` or `tmp-` plus four characters of `a-z2-7` from crypto/rand,
with run's usual `-2` retry. The create line is `Created tmp-k3f9 (large,
temporary: destroyed Sep 29 14:02)`; `run`, `sync` and `attach` print
`tmp-k3f9 is temporary: destroyed in 5h.` on stderr (the spec's line, no
keep hint, so it fits a docs code block); `ls` prints it with "`repose
keep tmp-k3f9` keeps it." under the table, and `status` as `temporary:
destroyed in 5h; ...`. The time left rounds up (hours from an hour,
minutes under), so "in 1h" is never early; past the expiry it says the
machine goes once nobody is attached. `repose rm` asks `Destroy tmp-k3f9?
It is temporary: no snapshot is kept and it cannot be restored. [y/N]` and
prints no restore hint. The dashboard's project list shows a `temporary`
badge after the name and the same "destroyed in 5h" under the state.
`repose keep` on a project that is not temporary prints `NAME is not
temporary.` and exits 0. (`ls` and `status` changed with I-484: a `LEFT`
column, and no keep hint.)

**I-352. The session end destroys a temporary machine only when tmux says
the session is gone.** (07, 2026-09-29; builds I-347.) After the attach
returns on the input-proxy path (`attachTmux` now takes an `after` hook,
run only there), `run` and `attach` run `tmux has-session -t =<slug>`
over the command's ssh master. Only exit 1 (the session, or the whole
tmux server, is gone) counts: an ssh that could not connect (255) says
nothing, and the machine then waits for its expiry. It prints
`tmp-q7wd is temporary and its session has ended; destroying it.`, sends
the DELETE without asking, and closes the master; a DELETE that fails
says so and that the expiry still applies. The fast attach (I-223) never
sees a temporary project, since `projects.json` never names one, so it
needs no hook.

**I-353. Every sync refusal of the checkout comes before the create.**
(07, 2026-09-29; builds I-347.) `syncPrecheck` (not a git repository, no
commit, a shallow clone) is what `syncGuest` checked first; `run` and
`sync` now run it in a goroutine beside the resolve and wait for it
before creating, starting or restarting anything, so a refused run no
longer boots a machine first. The resolve's own refusal ("This directory
has no git remote. Pass --name NAME") and the prompt-is-a-project check
still come first: they name the more useful fix. With `--temp`, a
directory that is not a repository skips the sync and prints `Not a git
repository, so nothing was synced.` (the carry then runs in the session
helper, as with `--no-sync`); a repository with no commit or a shallow
clone still refuses. `syncGuest` keeps its own check for its other
callers.

**I-354. What agents on a temporary machine are told: nothing yet.** (07,
2026-09-29.) The guest cannot know it is temporary: `project.json` is
written at SetupProject and is not rewritten by `repose keep`, so a field
there would go stale, and a marker the CLI writes over ssh would be a new
guest contract for one line of the agent guide. The CLI's time-left line
and the `temp_expiring` notification reach the person who chose the
lifetime. If agents need it, the way is a guestd field refreshed at every
start and keep, with a base publish; not in this round.

**I-355. Tests and evidence for temporary machines.** (2026-09-29.) The
spec's list, as built: `TestRunTempCreatesWithoutRemote`,
`TestTempWithoutRepoSkipsSync`, `TestSyncRefusalComesBeforeCreate`,
`TestKeepClearsExpiry`, `TestTempSessionEndDestroys` (a real tmux in the
test guest, killed), `TestRmAndLsOfATemporaryProject` and
`TestTempFlagParsing` in the CLI against the fake api, which accepts
`expires_in_s` and `PATCH expires_at: null`, destroys a temporary project
without a snapshot, and has `ExpireTemporary` for tests; and
`TestTempCreateAndKeepContract`, `TestTempExpiryWaitsWhileAttached`,
`TestTempExpiryDestroysWithoutSnapshot` and `TestTempDeleteKeepsNoSnapshot`
against Postgres and the fake hostd. I-348's two cases are
`TestRunNameInHomePicksTheNamedProject` and
`TestRunNameInCheckoutMakesASecondProject`. The live run to expiry with a
short `--temp` on an `e2e-*` project is left for after the deploy.

**I-356. `run` reports a create that failed at once, instead of starting
the project it left behind.** (conductor, live check of I-349, 2026-09-29)
With host-01 full, `repose run --temp 10m --name e2e-tmpexit` printed
"Could not start e2e-tmpexit: project has no guest; create it first". The
create op had failed at placement with `capacity` within the POST's own
second, so the project read back as `error` with no op, and
`ensureRunningFrom` (not fresh: a project just created is read again)
treated it like any errored project and sent a start. It now keeps the
create's `op_id` across that read, and when the project reads `error`
with no op, reads that op and, if it failed, reports it: "Could not
create e2e-full: no host with capacity (capacity). Try again in a few
minutes; we have been alerted." No start is sent. Not specific to
`--temp`: every create on a full host did this.
`TestCreateThatFailedAtOnceReportsItsOwnError` (sent one start before;
none after).

**I-357. The waitlist's minute tick runs under its own lock,
`LockWaitlistTick` (1012), not `LockWaitlist`.** (conductor, live check of
I-349, 2026-09-29) Expired temporary machines were not destroyed. In
production `pg_locks` showed the api loop's connection holding the
session lock 1011 (`TryLock(LockWaitlist)`, app.go's minute case) and a
second connection of the same process blocked for 7 minutes on
`pg_advisory_xact_lock(1011)`: `Inviter.Run`'s own transactions take that
lock on other pool connections, and advisory locks belong to a session, so
the tick waited on itself for good. Everything after it in the loop's
`select` stopped with it: the temporary-machine reaper (I-350), the
billing rollup, the limiter cleanup and the 15-second host sweep; and
every `Reserve` and `Join` (checkout and the waitlist, I-290) would have
queued behind the stuck lock. It happened from the launch round's first
deploy after every restart; its tests called `Inviter.Run` without the
loop's lock. The tick now takes `LockWaitlistTick` to pick one replica;
`LockWaitlist` stays the transactions' own lock. `TestSeatsWaitlistAndInvitations`
now runs the first tick under the loop's lock with a 10 s deadline: under
1011 it fails with "context deadline exceeded", under 1012 it passes.

**I-358. A plain `repose run` in a directory with no git remote creates a
project named after the directory; outside a repository it skips the
sync.** (owner, 2026-09-29; `repose run` in `~/Downloads/job search` said
"Pass --name NAME", and `--name job` then refused the sync because the
directory is not a git checkout.) Replaces the exit 2 in 07-cli.md §5.3
and projects.md's "never named after the directory". That rule's reason,
that the directory name is not unique and a second checkout could not
find the project, holds for `--name` too, and `by_dir` already makes such
a project findable from where it was made. The name is the repository
root's basename, else the cwd's, with runs of characters outside
`[A-Za-z0-9._-]` (the api's name rule) made one `-` and trimmed to 64
(`job search` is `job-search`); a name that ends up empty (`/`) exits 2
asking for `--name`. A taken name gets the usual `-2` retry. The project
has no remote and is written to `by_dir`, so the next plain `run` there
lands on it with "Using job-search, the machine last made in this
directory." (the I-348 warning, no longer saying "with --name"). A
directory that is not a git repository now skips the sync for every
`run`, as I-353 had it do for `--temp` only, and prints `Not a git
repository, so nothing was synced.`; `repose sync` there still refuses, and
a repository with no commit or a shallow clone still refuses for both.
`TestRunWithoutRemoteNamesTheProjectAfterTheDirectory`; the "not a
repository" case left `TestSyncRefusalComesBeforeCreate`.

**I-359. kanali, the owner's coordinator guest, is WireGuard peer
10.255.254.1 on the edge hub, with no forward rule.** (owner, 2026-09-29)
The conductor moved off the Azure dev box into `kanali`, a repose guest on
host-01. Guests reach none of the operator paths: host-01's NAT address
is not in `operator_cidrs`, and the WireGuard network is closed to guests
(DESIGN §7). kanali dials the hub like the monitoring server (I-170) and
is declared in `nix/edge/edge-01.nix` `staticPeers`, so wgsync keeps it.
The input chain already admits the operator sshd on 2222 from
10.255.0.0/16, so the peer needs no forward rule. kanali reaches the
control VM (`10.255.255.1:22`) and hosts by jumping through that sshd:
`ssh -J root@10.255.0.1:2222`. Its plain key goes in the edge's and the
control VM's root `authorized_keys` and in `operator_authorized_keys`.
Hosts take it by an 8 h certificate from `repose-admin operator-cert`
(I-139), never by a key in `host-01.nix`. Operator peers take
10.255.254.0/24, outside the host allocator's 10.255.0.2 to 10.255.3.233
(`hostmgr.allocate`, host n is 10.255.0.(n+1)). The same rule shows the
monitoring server's 10.255.0.3 is host-02's address; it has to move
before a second host registers. kanali runs on a host it may switch, so
the laptop and the dev box keep their operator paths as break-glass, and
host-01 switches stay with the owner. *Rejected:* Tailscale, which puts a
guest running agents and a browser on the owner's personal tailnet, held
by an ACL the repository cannot check (I-170's reason); host-01's NAT
address in `operator_cidrs`, which opens operator SSH to every tenant on
that host; forward rules to port 22, which would open every host's sshd to
kanali directly, where the jump keeps each hop in the edge's verbose sshd
log and leaves one key on the edge to revoke.

**I-360. kanali's tunnel carries only packets from 10.255.254.1.**
(owner, kanali, 2026-09-29) Amends I-359. The first config added a
main-table route for 10.255.0.0/16 into the tunnel. The gateway dials
every guest from the edge's 10.255.0.1 through the guest's host, so
kanali's replies to it (source 10.64.0.8) went into the tunnel instead,
and the edge dropped them, because a peer may send only from its
AllowedIPs. While the tunnel was up, `repose attach kanali` failed with
"environment is not accepting connections yet"; the owner got back in by
restarting kanali, which left the tunnel down. The config now has `Table =
off`, a rule `from 10.255.254.1 lookup 51820` and the 10.255.0.0/16 route
in table 51820 only, and the `repose-edge` SSH alias binds 10.255.254.1.
Evidence with the tunnel up: `ip route get 10.255.0.1 from 10.64.0.8` goes
via eth0 and `from 10.255.254.1` via wg-repose; the edge read kanali's SSH
banner from 10.255.0.1; tcpdump showed a live gateway session to
10.64.0.8:22 flowing both ways on eth0. The rule for any guest that is
also a hub peer: never route the edge's address from the main table.
Check an inbound gateway session before calling such a tunnel done.
*Rejected:* adding kanali's 10.64 address to its AllowedIPs on the edge,
which would let a peer send as part of host-01's guest range; narrower
AllowedIPs here, since the operator sshd is on 10.255.0.1 itself.

**I-361. kanali runs tofu as its own service principal; the Key Vault
operator policy is pinned to the owner.** (owner, kanali, 2026-09-29)
`kanali-tofu` has Contributor and Storage Blob Data Contributor on
`repose-prod`, and its credentials are kanali's `ARM_*` secrets; the state
backend uses Entra auth (`use_azuread_auth`), so Contributor alone cannot
read the state. The vault's operator policy took `coalesce(operator_object_id,
<caller>)`, and the prod and staging roots never passed
`operator_object_id`, so the first plan as the service principal wanted to
replace the owner's policy with its own (`object_id 07b58b32... ->
0be39425... # forces replacement`). An apply would have left the owner
without key management on the vault that wraps every user's DEK. Both roots
now declare and pass it, and the local tfvars set it to the owner. The
service principal got Get, List and GetRotationPolicy on keys by `az
keyvault set-policy`, with the owner's approval, because a plan refreshes
the key; that policy is outside tofu, which manages each policy as its own
resource and leaves others alone. Contributor can edit vault access policies,
so this principal can grant itself more; that is the owner's accepted risk
for a coordinator that applies. Evidence: `make -C infra plan ENV=prod` as
the service principal, "No changes"; `make -C infra validate` passes.
*Rejected:* running tofu from a copied `~/.azure` (the dev box's way; the
refresh token lapses after 90 days idle and device-code login is blocked
from a VM); RBAC on the vault (a migration of a vault holding live keys, for
one reader).
**I-362. A third plan: Pro becomes Plus, and a new Pro at $99 buys 32 GB
running at once.** (owner, 2026-09-29: "we need one more pricing plan";
"pro -> plus, new -> pro? no users rn havent launched yet"; "lets not cap
pro customers. they are the real feedback pipelines") Amends I-289 and
I-290. The plans are Solo ($29, 8 GB, 100 GB disk, 250 GB egress, 10
projects, 1 seat), Plus (what Pro was: $59, 16 GB, 250 GB, 500 GB, 25, 2
seats) and Pro ($99, 32 GB, 500 GB, 1000 GB, 50, 4 seats). Why a bigger
plan and no smaller one: one agent session needs a `large`, because a
harness with its language servers, builds and browser fills 8 GB and a
smaller machine gets the session OOM-killed partway through, so memory
sets the floor and Solo is one agent working, Plus two. The buyer the
2026-09-26 marketing research found best is the heavy Claude Code user
already paying Anthropic $100 to $200 a month and running several agents
across repositories; on Plus that user hit the `plan_limit` refusal with
nothing to upgrade to. Why $99: it sits under the Max price that buyer
already pays, prices four agents at about one Max seat, and gives a small
volume discount per GB ($3.09 against Solo's $3.63 and Plus's $3.69) so
the upgrade reads as worth it. Why the rename is free: no subscriber
exists and `paddle-bootstrap` has not run against a live key, so no Paddle
product sells the old Pro. Why no cap on Pro beyond the seat count: Pro
users are the product's feedback, and the waitlist already bounds the
fleet; on the launch `D64s_v7` a Pro user running four seats all month
costs about $400 and loses about $300, which the Azure credit absorbs as
I-289 accepted for Solo. Mechanics: `internal/billing/plans.go` gains
`Plus` and `SmallestFor(class)`, so the refusal for an `xl` on Solo says
"Upgrade to Plus" (the cheapest plan that holds one); `resize --class xl`
says it needs the Plus plan. `PADDLE_PRICE_PLUS` joins the environment and
`Validate` refuses a key without all three prices or with two plans on one
price; `paddle-bootstrap` makes eight objects (three products and prices,
the overage product, the webhook). Migration 0011 widens the
`subscriptions.plan` and `scheduled_plan` checks to `solo|plus|pro`. Plan
changes between any two plans follow the seat count as before: more seats
at once and prorated, fewer at the renewal after the fit check. An exempt
account's `limits` block shows the biggest plan, now 32 GB. The api's
`plan` field and the `repose_api_billing_subscriptions_total{plan}` label
gain one value; nothing that read `solo|pro` breaks. *Rejected:* a plan
below Solo (the floor is a `large`, and at $100 a seat on Azure a cheaper
always-on seat needs suspend-when-idle first); Max or Team as the name
(Anthropic's plan, and seats for other people, which R5-6 defers); $119
(matches Plus per GB exactly, but gives the heavy user no reason to pick
Pro over two Plus accounts); a sales cap on Pro during the Azure period.

**I-363. The logo is the owner's cross-and-blocks sketch, traced; it
replaces the r.** (owner, 2026-09-29: a notebook drawing, then "T2 is it.
just apply the existing brand colors and go use it as the logo") The mark
is a thin cross (stems 4 on a 100 grid) whose crossing sits left of
centre and low, the vertical longer than the horizontal as drawn, with
three blocks against the crossing: skinny above-left (7 by 32), a middle
square above-right on the arm (12 by 14), and the big square below-right
(20 by 18). Colours are the landing's: the cross in the text's ink, the
blocks in `--sh-grey`, `--sh-light` and `--sh-accent`. `Logo.svelte`
draws it cropped to the drawing at 28px tall (24 for `sm`), since at the
r's 20px square the stems fell under a pixel. The favicon is a heavier
cut of the same arrangement (stems 10, blocks a little larger), because
the traced stems vanish at 16px: `static/favicon.svg` flips its colours
with the scheme so the cross shows on a dark tab bar, and
`static/favicon.png` is the light version at 128px on transparent; both
links carry `?v=2` so browsers drop the cached r. The Logto sign-in page
and the sign-in emails load that PNG, so they change with the next
deploy of the web app.
The studies are in the owner's artifact "repose mark studies" (rounds
one to four; T2 in round four). *Rejected:* the same arrangement with
blocks 1.5 times larger and a heavier stem (T6, T7: they read better at
16px, but the owner chose the traced proportions); a fourth block
below-left, which the sketch has; three axes with a cube face; product
metaphors (a whole rest, pause and continue, a lit cursor); off-brand
hues for the big square.

**I-364. tmux's mouse mode is off in the guest.** (owner, 2026-09-29;
amends DESIGN.md §guest base "mouse on") With `set -g mouse on`, tmux
takes every click and drag, so selecting text in the laptop's terminal
selects inside tmux instead and copying needs tmux's own keys; the owner
wants the terminal's selection and copy back. `/etc/tmux.conf` now says
`set -g mouse off`. tmux reads `~/.tmux.conf` after it (checked with tmux
3.7c: a home file with `mouse on` wins over the system file's `off`), so a
user who wants the mouse back writes that line there; the home directory
survives a stop. What a user loses: clicking a window name and wheel
scrollback through tmux's history (`Ctrl-b [` scrolls). A running tmux
server keeps its setting until the base is bumped and the guest restarts.
The `guest-base` VM test greps for `mouse +off`. *Rejected:* a
`config.toml` or `repose config` switch (one line in `~/.tmux.conf` already
does it, per machine, without a new key).

**I-365. `repose secrets set` echoes one `*` per character.** (owner,
2026-09-29) The prompt read with echo off, so after a paste the screen
showed nothing and the user could not tell whether the value had
arrived. The terminal now goes into raw mode (`golang.org/x/term`) and
the CLI draws one `*` per character, counting a multi-byte character
once; Backspace and Ctrl-H erase one, Ctrl-U all, Enter ends, Ctrl-C
restores the terminal and exits 130, Ctrl-D on an empty line is "No value
given." (exit 2) as EOF was before; arrow-key sequences and other control
characters are dropped. The prompt loses "(not shown)". When raw mode is
unavailable the old echo-off read runs. `--from-file` and `--from-env` are
unchanged. The stars show the value's length to anyone watching the
screen; the owner accepted that, since a fixed `****` could not show a
doubled paste. `TestReadMaskedEchoesAStarPerCharacter`,
`TestReadHiddenLineOnATerminal` (real pty: stars on the screen, the value
never, echo and canonical mode back after). *Rejected:* a fixed-width mask
(hides a doubled or truncated paste).

**I-366. `run --no-sync` still copies the tool logins and the carry.**
(owner, 2026-09-29; reverses the "Skip the git sync and the copied
logins" of `cli.md` and 07-cli.md §5.5 step 5's "unless `--no-sync`")
The logins rode only the sync's apply ssh (I-224), so `repose run
--no-sync`, and since I-358 every run outside a repository, attached to a
machine without the laptop's gh, Codex or opencode login; the owner read
that as a bug, since `--no-sync` is about the checkout. When a run leaves
the checkout alone it now sends the same payload the apply would have:
logins (mtime rule unchanged), git identity, Claude files, tools list and
zone, in two ssh commands (the guest's markers with the `#credsmissing`
check the probe uses, then only what changed; none when nothing did),
before the attach, and prints the same `Credentials:` line. It replaces
the session helper's carry on those two paths, so `--no-attach` gets it
too. A failure is a warning and the run goes on. `repose attach` is
unchanged. `TestRunWithoutSyncStillCopiesToolLogins` failed before
(guest `hosts.yml` empty) and passes. *Rejected:* adding the logins to the
session helper (it does not run with `--no-attach`, and its messages only
reach tmux's status line).

**I-367. `repose run` syncs the checkout only into a machine that has no
commit yet; `repose sync` is the explicit sync.** (owner, 2026-09-29;
amends R1-3's "one-shot sync of the uncommitted diff at launch" to the
first launch, moves R3-12's refusal and I-248's notice to `repose sync`,
and ends I-302's "`repose run --no-attach` under its own name") The
owner ran `repose run` on a machine where an agent had left two
uncommitted files and got I-248's exit 6 with three choices, and judged
it the wrong shape: every attach made the user weigh what the machine had
against what the laptop had, a workflow nobody asked for. `run` should
mean "give me my machine now". So `run`'s sync is `FirstOnly`: after the
probe, a guest checkout with any commit (`probe.tips` non-empty) is left
alone whatever either side holds; the logins and carry still go, in the
apply's one ssh or none (I-224); nothing is stashed or refused. A guest
with no commit, which is what guestd's `git init` leaves on a new machine
(and what `run --no-sync` leaves), takes the full first sync as before.
When the laptop's sync key differs from the guest's and it has a modified
or untracked file or a commit the guest lacks, the run prints `Not synced:
your laptop has work the machine doesn't (2 modified, 1 untracked, 3
commits). \`repose sync\` sends it.` (zero counts left out) and attaches;
with nothing new it prints nothing about the sync. `repose sync` runs the
whole sync as before, refusal included, and its messages now name itself:
exit 6 says "`repose sync` copies your laptop's work onto the machine" and
offers `repose sync --stash-remote` / `--discard-remote`; I-248's notice
becomes "Nothing new to sync. The machine has changes your laptop doesn't
have (N files); `repose sync --stash-remote` puts them in git stash and
lays your laptop's work over them." (or, for commits, "... `git fetch
repose` brings them to your laptop."), since `sync` never attaches. `run
--stash-remote` and `--discard-remote` are hidden and exit 2 with
"`repose run` no longer syncs a machine that already has your checkout;
`repose sync --stash-remote` does." for one release. The `repose`
remote's push URL text names `repose sync` for remotes added from now on.
The cost is unchanged: the laptop still computes its sync key (the diffs
and the untracked tar) to know whether to print the line.
`TestRunLeavesAnExistingCheckoutAlone` (the owner's case: agent file and
laptop edit, run exits 0, both untouched, the line printed, gh copied),
`TestRunNamesLaptopCommitsItDidNotSend`,
`TestRunIsQuietWhenTheLaptopHasNothingNew`, `TestRunSyncsIntoANewMachine`
(first run syncs, second leaves it), `TestSyncCommandSyncsAnExistingCheckout`
(sync lays work over it, refuses over an agent's file naming its own
flags, `--stash-remote` goes through), `TestRunStashRemoteSaysUseSync`.
The I-210, I-248 and I-303 tests that went through `run` now go through
`repose sync` (`Sync: true`), where that machinery lives. *Rejected:*
syncing on `run` when the guest is clean (a clean guest can still hold an
agent's commits, and "sometimes it syncs" is the confusion being removed);
a prompt on `run` (scripts; and I-248 already rejected it); dropping the
key computation on skipped runs (then the line could not be printed).

**I-368. The machine's checkout is named after the laptop folder of its
first sync; a machine with no checkout works in the home directory.**
(owner, 2026-09-29; amends the `/home/dev/<slug>` of DESIGN.md,
guest-conventions.md and I-255) The owner ran `repose run --name kanali`
in `~/Downloads/projects/factory`, then `scp -r ./infra
kanali.repose:~/kanali/factory/`, which failed four ways with "realpath
... No such file": the checkout was `~/kanali`, named after the project,
and nothing on screen had said where it was. The owner's rule: a sync of
a folder puts it in `/home/dev/<folder>`; a project named like its
folder, the common case, looks the same as before; with nothing synced
the user lands in `/home/dev`. How:

- *One rule, three readers.* `~/.repose/checkout` holds the directory's
  name. The checkout is the directory it names when that exists, else
  `~/<slug>` when that exists (every machine set up before this, whose
  guestd made it at each start), else none, and work happens in `~`. The
  CLI's `checkoutVar` (shell, in every remote script that works "in the
  checkout": exec, `repose ssh`, agent windows, worktrees, attach, cp,
  code), guestd's `findCheckout` (Go, which also refuses a name that
  resolves outside the home) and the base's new `repose-checkout` (the
  tmux session unit and `repose-guest-profile`) apply it.
  guest-conventions.md "The checkout" is the contract.
- *Only a sync makes one.* The sync's probe, finding none, makes
  `~/<name>` with `<name>` the laptop root's basename run through I-358's
  `dirProjectName` (so `job search` is `job-search` and a leading dot
  goes), or the slug when that is empty or `~/<name>` is a non-empty
  directory or not a directory (`~/go` from Go tooling); it writes the
  name, git-inits, and prints `#created`. The run then says `Checkout:
  ~/<name> on the machine` once. guestd's `SetupProject` no longer makes
  `~/<slug>`, and git-inits and sets `origin` only in a checkout the rule
  finds. The early and boot probes no longer run outside a repository,
  where they would have made a checkout for a run that syncs nothing.
- *The session catches up.* A new machine's tmux session starts in `~`,
  before any checkout exists. The sync that makes the checkout respawns
  the `shell` window into it when that window is an idle shell in `~`
  (never anything busy), and every attach passes `tmux attach -c
  <checkout>`, so Ctrl-b c opens there.
- *The `repose` remote* is `<slug>.repose:~/<checkout>`: run passes the
  name its sync or carry just learned; attach keeps an existing remote of
  the CLI's shape and asks the guest (one ssh) only when it must add one;
  a machine with no checkout gets none. The CLI's shape is now any
  `<slug>.repose:~/<name>`, so `todo-app.repose:~/other` counts as the
  CLI's and may be retargeted. `repose cp` and `repose code` spend one ssh
  asking the guest for the name.
- *Compatibility.* A machine set up before keeps `~/<slug>` everywhere,
  new CLI or old; an old CLI against a new base still works, because the
  rule's second branch is its layout and the probe it runs made
  `~/<slug>` itself. A fork of a new machine has the same checkout path,
  since the file comes with the volume; a fork of an old one keeps I-255's
  link. Worktrees are `~/<checkout>-worktree-<N>`. Needs a base publish
  for guestd, the tmux unit and `repose-checkout`; until then new
  machines behave as old ones (guestd makes `~/<slug>` first).

`TestFirstSyncNamesTheCheckoutAfterTheLaptopFolder` (folder `factory`,
project `proj`: `~/factory`, `~/.repose/checkout`, no `~/proj`, the
Checkout line once, remote `proj.repose:~/factory`, exec and an agent
window in it), `TestLaterRunsKeepTheRecordedCheckout`,
`TestFirstSyncFallsBackToTheSlugWhenTheNameIsTaken`,
`TestAnOldCheckoutStaysUnderTheSlug`, `TestNoSyncMakesNoCheckout`,
`TestFirstSyncMovesTheIdleShellIntoTheCheckout` (idle shell moved, busy
one left), `TestCpGuestPathFollowsTheCheckout`,
`TestCheckoutNameFromTheFolder`; guestd `TestSetupUsesTheRecordedCheckout`,
`TestRecordedCheckoutStaysInTheHome`; VM tests `guest-base` (session in
`/home/dev` with no checkout, in `~/factory` once recorded, `../etc`
ignored, profile `dir`) and `guestd` (SetupProject makes nothing, then
git-inits `~/factory` and sets its origin). *Rejected:* keeping
`~/<slug>` and printing it (the owner's point is that the folder name is
what the user already knows); a symlink `~/<folder>` to `~/<slug>` (two
names for one directory in `ls ~`); recording the name in `project.json`
(guestd rewrites that file at every start from the api's record); the
laptop's projects cache (a second laptop or a fork would not have it).

**I-369. One design foundation under every page; the dashboard no longer
follows the recruiting app.** (owner asked for the design critique's
fixes, 2026-09-30; supersedes I-13, amends I-287) The critique found two
specs with nothing under them: `DESIGN-LANGUAGE.md` for the dashboard and
`LANDING.md` for `/`, with four headers, two "muted" greys, two monospace
faces and no type scale between them. `DESIGN-LANGUAGE.md` now opens with
a Foundation (tokens, palette, contrast floor, radii, faces and type
steps, spacing, motion, icons, the logo, the page frame and header) that
the dashboard, the docs, the legal pages and the landing all stand on.
The Dashboard part follows it, and `LANDING.md` points at the Foundation
for shared tokens instead of restating them, so the landing's grammar
(I-287) is a layer over the Foundation rather than a system beside it.
I-13's "copy the recruiting app" had been false since the restrained
redesign (630cde8) and still steered readers of `docs/README.md` to that
app. `CHECKLIST.md` gains design greps with their expected counts, so the
rules have something that fails when they drift. *Rejected:* a third
document for the foundation (the dashboard rules are short, and a reader
building a screen needs both parts at once); leaving the landing's tokens
in `LANDING.md` (the same `--ink*` values were then specified twice).

**I-370. Shared text and edge tokens with a contrast floor: 4.5:1 for
text, 3:1 for control edges and state marks.** (design critique,
2026-09-30; amends I-331's placing of `--ink*` for the landing alone)
`--ink`, `--ink-muted` and `--ink-faint` are every page's text colours,
exposed as `text-ink`, `text-ink-muted` and `text-ink-faint` through
`@theme inline` so each utility follows the scheme switch. Values: ink
zinc-900 / zinc-100; muted zinc-600 / zinc-400 on every page, which
settles the two greys the dashboard and the landing called "muted"; faint
`#6b6b66` / `#8f8f8a`. Each holds 4.5:1 on `--page`, `--surface` and
`--sunken` in its scheme (faint: 4.86 on light `--sunken`, 5.31 on dark).
The old faint, zinc-500 in both schemes, was 4.14:1 on dark `--page`,
and zinc-400 text at 2.56:1 carried the dashboard's 12px metadata. A new
`--control-edge` (`#888883` / `#6e6e6a`, 3.2:1 or better on page,
surface and sunken, WCAG 1.4.11; the dark value was `#6a6a66` until I-391
found it at 3.17:1 on `--sunken`) edges fields, quiet buttons and the
hollow state dot; the old field edge, `--rule-strong`, was 1.56:1 and
stays for rules, badges and table heads, which nobody has to find to use.
`--color-zinc-500` is re-toned from `#767671` to `#70706b` so the darkest
grey pages still use for small text reaches 4.52:1 on light `--sunken`
(was 4.15). In the dark it is 3.5 to 3.8:1 and is never text there. `--radius-xs` is 2px (was 1px), so `rounded-xs` on badges, dots
and keys is a real corner and "2 to 4px" is true of the whole scale.
`.card` lost its fill to match the doc's "no fill or shadow";
`.btn-quiet` and `.banner` keep their `--surface` fill, which lifts them
off `--page` by one step and is written into `DESIGN-LANGUAGE.md`.
Ratios for every pair are in commit 3a38564. *Rejected:* darkening
`--rule-strong` itself (every hairline on the site would get heavier to
fix fields); keeping hand-paired `text-zinc-500 dark:text-zinc-400` with
new values (76 strings to keep in step by hand).

**I-371. The fonts are self-hosted, and JetBrains Mono is the one
monospace.** (design critique, 2026-09-30) Google Fonts served Noto Serif
and JetBrains Mono on every route, so each visitor's IP reached a
processor `privacy.md` does not list. The faces now come from
`static/fonts`: Noto Serif 400, 600 and 700, JetBrains Mono 400 and 600
and 400 italic (the docs' code comments), latin plus latin-ext, from
@fontsource 5.3.0, with their OFL texts. `app.html` preloads the heading
cuts 600 and 700; metric-matched local fallbacks (`Noto Serif Fallback`
over Georgia, `JetBrains Mono Fallback` over Courier New, sizes from
@capsizecss/metrics) keep a heading from reflowing when the webfont
arrives. `--font-mono` leads with JetBrains Mono. Before, it left the
face out, so the hero's `.cmd` rendered in Liberation Mono beside
pictures drawn in JetBrains Mono. `--font-sans` is written out as
Tailwind 4.3's default stack, so a Tailwind upgrade cannot change the
body face unnoticed. *Rejected:* keeping Google Fonts and naming Google
in the privacy policy (a processor for a font is not worth a policy
change); "system mono" as the doc had it (the landing's pictures are
drawn in JetBrains Mono, and two faces for one role showed side by side).

**I-372. A focused field shows the house focus ring.** (design critique,
2026-09-30; supersedes `DESIGN-LANGUAGE.md`'s "focus turns the border
zinc-900 with no ring") `.field` had `focus:outline-none` and marked
focus by darkening its border, the same border `.field--set` draws for a
field holding a value, so a focused field and a set one looked alike,
and in forced colours focused and blurred were identical. Fields now get
the global `:focus-visible` ring: 2px, `--focus` (blue-600 / blue-400),
offset 2px. The CodeMirror editor's focus shows the same ring.
*Rejected:* a distinct focus border colour (a 1px change is the thing
that failed; forced colours flattens every border to one colour).

**I-373. State dots: busy is ink, stopped is hollow, and running and
error differ in lightness.** (design critique, 2026-09-30) `.dot--busy`
was the accent blue, pulsing without a reduced-motion check, on six
states. Blue is for links, focus and selection, and blue on `destroying`
made the link colour also mean "something is happening". Busy is now ink
(zinc-700 / zinc-300) and pulses only under `prefers-reduced-motion:
no-preference`; the state word beside the dot carries the meaning. A
plain `.dot` (stopped, destroyed) is a hollow square edged in
`--control-edge`: the old zinc-400 / zinc-600 fill sat at 2.5:1 and
nearly vanished, and hollow reads as "off" without colour. Filled dots
are running (emerald-500 / emerald-300), error (red-600 / red-500) and
busy. In the dark, running moved from emerald-400 to emerald-300 and
error from red-400 to red-500, because the old pair had nearly the same
lightness and a red-green colour-blind eye could not tell them apart.
`StateDot.svelte` hides the square from assistive tech and always prints
the word. *Rejected:* amber for busy (amber means "needs attention" on
badges and banners, and a starting machine needs none).

**I-374. Toasts and docs code highlighting take the house colours.**
(design critique, 2026-09-30) `<Toaster richColors />` drew sonner's own
look: 8px corners, a drop shadow and `#e60000` text, with light success
and error text at 4.26 and 4.35:1, on the feedback users see most (21
call sites). The Toaster now mounts without `richColors`, and
`layout.css` maps sonner's variables to the `.banner--ok`, `--error` and
`--warn` pairs (9:1 or better in both schemes), with a 2px corner, a
hairline, no shadow and the house focus ring. Docs code highlighting
drops the stock purple and blue: keywords are ink at weight 600, names
full ink, strings and URLs emerald-700 / emerald-300, literals amber-700
/ amber-300, comments and punctuation `--ink-faint`, each 4.5:1 or better
on `--sunken`. In the docs, blue is a link. *Rejected:* a separate toast
palette (a toast and a banner say the same kind of thing and should look
alike).

**I-375. A type scale with two named small steps and one size per
heading level.** (design critique, 2026-09-30) Pages had written
`text-[13px]`, `text-[0.8rem]`, `text-[0.7rem]` and `text-[0.75rem]` by
hand, and an h2 rendered at 16, 18 or 20px for the same role. `@theme`
adds `--text-compact` (0.8125rem, 13px: a mono reading beside sans text,
a code block, the dashboard nav on a phone) and `--text-2xs` (0.6875rem,
11px: a badge, a landing label, and the floor for any text). Neither sets
a line height, so each keeps the leading of the text around it. Every
dashboard h2, card titles and billing's plan cards included, is `text-xl
font-semibold`; the danger zone's h2 is the same size in red on the
project and account pages. Billing's plan cards were h3 directly under
the h1 and are h2. The serif comes from the base style on every heading,
so pages drop a redundant `font-display` on headings. The first
`.form-section` on a page draws no top rule (`layout.css`), since the
title rule sits directly above it and two hairlines 40px apart read as a
gap where something failed to render. *Rejected:* a separate
`text-base` role for card titles, tried in the project pass (billing's
plan cards were already `text-xl` h2, and two sizes for one level is the
drift the critique flagged).

**I-376. Buttons come in two sizes: `.btn--sm` and the default.** (design
critique, 2026-09-30) Seventeen `!py-*` and `!px-*` overrides resized
buttons one page at a time. `.btn--sm` (`px-3 py-1.5`) is for rows,
toolbars and header bars. A `.btn--lg` for a page's single call to action
was added with it and deleted under I-378 when nothing used it (I-391);
it comes back with its first user. `.btn--sm` replaces the overrides, so one change
in `layout.css` resizes every compact button. The landing's
`+page.svelte` still carries six overrides, left to the landing-critique
branch that rewrites that file.

**I-377. Forced colours are part of the system.** (design critique,
2026-09-30) No `forced-colors` rule existed, so the state dots, the meter
fill and the current nav item vanished in Windows High Contrast. An
unlayered `@media (forced-colors: active)` block in `layout.css` puts
each state back in a system colour: dots filled or hollow in
`CanvasText`, the current nav link a 2px underline the others lack,
selects back to the native arrow (the drawn chevron was a fixed grey),
the search glass dropped, button edges `ButtonText` and disabled buttons
`GrayText`, badge and key edges `CanvasText`. `Meter.svelte` fills with
`CanvasText`, and `Highlight` past the limit. The meter's track also
gained a 1px `--control-edge` border (it sat at 1.05 to 1.10:1 on its
card), and past the limit the reading adds the word "over" and
`aria-valuetext` says "over the limit", so the state never depends on
amber alone. A new component that carries state in a fill or a border
colour adds its rule to that block.

**I-378. Unused patterns are deleted rather than documented.** (design
critique, 2026-09-30) `.switch`, `.radio-row*`, `.badge--warn`,
`.badge--info`, `.dot--warn`, `.row--group`, `--sh-stop` and
`UsageChart.svelte` had no user. UsageChart carried an orange and green
palette, `shadow-sm` and `[data-theme]` selectors, and `.switch` had the
one focus ring with no dark variant; left in place, either would bring
those back the day someone reused it. `DESIGN-LANGUAGE.md` no longer
offers `.switch` or `.radio-row`. A page that needs a switch or a list of
radios with help text builds it again with forced-colour states and adds
it to the doc.

**I-379. In the dark, the landing's small ink details are lit marks.**
(design critique, 2026-09-30) Dark `--sh-ink` was zinc-900 on
`--sh-paper` `#161615`, 1.02:1, which erased the ring's pupil and the
arch's door, the details `LANDING.md` says the shapes mean. Dark
`--sh-ink` is zinc-400 (7.06:1): a hole in the light scheme, a lit mark
in the dark one, still a mid value beside the zinc-500 shapes.
`LANDING.md` "Shape language" says so.

**I-380. One header frame for the dashboard, the docs and the legal
pages; form pages sit flush left.** (design critique, 2026-09-30) Four
headers at four widths put the logo at x=164, 216, 228 and 356 at 1440,
so it jumped as you clicked between pages. `HeaderFrame.svelte` is now
the header of the dashboard, the docs and the legal pages: 56px tall
over a `--rule` hairline, its content on `max-w-5xl px-5`, the logo at
x=228 at 1440 and x=20 at 390. The docs keep theirs sticky. The landing's
60px, 1120px top bar is the one exception until the landing-critique
branch lands. `PageShell` keeps `max-w-2xl` for forms but places that
column flush left inside the `max-w-5xl` frame, so a form's title starts
under the logo (x=228) instead of centred (x=404);
`DESIGN-LANGUAGE.md` gave the widths without saying the narrow column was
centred on purpose, and a centred column under a left-aligned header
made the page shift sideways between list and form pages. The
breadcrumb separator is `·`, as the doc said; the code's `/` was the
drift.

**I-381. The I-363 mark is in every header.** (design critique,
2026-09-30; amends I-363) `Logo.svelte` defaulted to the word alone, so
the mark appeared only on `/`. Every header now shows it: the 28px mark
and word from `sm` up; below `sm`, the 24px `sm` cut on the docs and
legal pages, and the 28px mark alone on the dashboard, whose five links
left 16px too little at 390 and 46px at 360 for both. The favicon is the
mark alone already, so a mark-only phone header matches the tab. In the
dark, the mark's two grey blocks take zinc-400 and zinc-500 (7.4 and
3.8:1 on `--page`) instead of `--sh-grey` and `--sh-light`, which left
the small blocks at 2.7:1 at 24 and 28px; the light block stays the
fainter of the two. The landing's large shapes keep their darker greys.
The mark is never drawn under 24px tall; below that the favicon's
heavier cut is the one to use.

**I-382. Docs and legal prose hold a readable measure.** (design
critique, 2026-09-30; amends I-345) `DocPage` and `LegalPage` set
`max-w-none`, so prose ran 94 to 114 characters a line, against I-345's
own reasoning about measure. Running text is now held to 33rem (a median
66 to 67 characters of the body sans a line at 1440). The docs column is
68ch (622px), and code blocks, tables and h2 rules use all of it, so
I-345's 70 columns of 13px mono fit with no scroll. The legal pages have
no wide content and hold their whole column to 33rem. *Rejected:* a flat
68ch for prose (68ch is measured on the "0" glyph, and at 68ch the body
sans set 79 to 81 real characters a line).

**I-383. The docs' right rail moves into the sidebar, and the menu button
moves to the right.** (design critique, 2026-09-30; amends I-344 and
I-345) In the shared 1024px frame, a rail beside the 240px sidebar left
a 456px column, under the 582px I-345 needs for 70 columns. The current
page's sections are listed under its link in the sidebar from `lg` up,
and in the "On this page" fold below `lg`. Below `lg` the menu button
sits at the right end of the header, so the logo is at x=20 as on the
dashboard and legal pages; the drawer still opens from the left, with
everything else I-344 describes.

**I-384. Legal pages use the docs' prose styles, show their effective
date, and keep a Draft banner that names nothing internal.** (design
critique, 2026-09-30) The legal pages showed literal backticks in
600-weight mono around inline code and a public banner quoting
`docs/SECURITY.md, test/isolation`. They now use `.doc`: inline code is a
quiet chip at body weight with no backticks, links take the docs' colour,
and h2 sections are separated by a rule. Each shows its frontmatter
effective date under the title. The Draft banner stays, since no entry
here says the policies are final; privacy's reads "this policy is under
review before launch. The two sentences in bold under "Process samples"
already bind the service today." (the policy's own opening claim, which
names them the same way; "the sentences in bold" also took in every
section's bold lead-in, I-391), and terms' reads "these terms
are under review before launch." Page titles read "Privacy · repose" and
the like, without an em dash.

**I-385. Only a page's first load can fail to a banner with Retry.**
(design critique, 2026-09-30) Account, settings, secrets and config sat
on "Loading…" for good when their first fetch failed, since the toast
that named the error was gone after a few seconds; only billing had a
failed state, and it was a dead end. `LoadState.svelte` wraps a page's
first load: "Loading…" (`role=status`) while it runs, then either the
content or the error in a `.banner--error` (`role=alert`) with a Retry
button that re-runs the same load and reads "Retrying…". A later refresh
that fails keeps the content on screen and reports through a toast.
`loadErrorText` gives the banner the sentence `toastApiError` would
show. *Rejected:* sending every failed refresh to the banner (blanking a
page someone is reading loses their place for a failure they can ignore).

**I-386. One confirmation pattern per consequence, and no native
`confirm()`.** (design critique, 2026-09-30) The dashboard had four
patterns, and restoring a snapshot over the disk, as irreversible as
Destroy, sat behind `window.confirm()` and a plain `.btn-ghost`. Now: an
action that cannot be undone (Destroy, Delete account, Restore over the
disk) uses `ConfirmType`, typing the slug or handle; a single deletion
whose cost is recoverable (a secret) is an inline two-step in its row,
as Cancel plan already was; an unsaved edit guard cancels the SvelteKit
navigation and asks in an inline `.banner--warn` with Stay (focused) and
Leave. The snapshot panel's **Restore…** is `.btn-ghost-danger` and
opens `ConfirmType`, which refuses while the project is not stopped and
says to stop it or restore as new, mirroring `features/snapshots.md`.
`ConfirmType` is a form (Enter confirms once the word matches) with an
optional busy label and Cancel. *Rejected:* the native dialog (it
ignores the house type, colour and scheme); a modal (the inline panel
keeps the thing being confirmed on screen).

**I-387. Restore-as-new is one `RestoreNameForm`.** (design critique,
2026-09-30) The snapshot list and "Recently destroyed" each built the
restore-as-new form, and the copy in `RecentlyDestroyed.svelte` had
drifted: no label, no error text, no wrapping, and at 390px it pushed the
page to 452px wide. `RestoreNameForm.svelte` is the one form: a visible
label, `.field-error` tied by `aria-describedby` and `aria-invalid`, a
submit with a busy label, Cancel, and wrapping rows.

**I-388. The config editor's Menu and Nix switch is ARIA tabs styled like
the header's current page.** (design critique, 2026-09-30) The switch was
blue with a 2px underline and bold text, one of three places blue broke
its rule, and had no tab semantics. It swaps a panel in place without
changing the URL, so it is `tablist`, `tab` and `tabpanel` with a roving
tabindex and the arrow, Home and End keys. The current tab is ink with a
1px underline, the others `--ink-muted`, with no accent and no weight
change, as the header marks the current page. *Rejected:* buttons with
`aria-pressed`, proposed in the project pass (they announce a toggle,
and the control selects one of two panels); links (nothing navigates).

**I-389. The accessibility gate fails on any failed binary audit, audits
signed in for real, and covers every page in both schemes at two
widths.** (design critique, 2026-09-30; widens `08-dashboard.md` §9)
`tests-a11y/a11y.spec.ts` passed at a Lighthouse score of 90 or more and
only logged failed audits, and one colour-contrast failure still scores
above 90, which is how sub-4.5:1 muted text shipped. It now also fails
on any binary audit scoring 0 on any audited page, unless
`KNOWN_FAILURES` names that audit on that page with a reason (it starts
empty). It audits in a Playwright persistent context: Lighthouse's CLI
tab opens in the browser's default context, and only a persistent
context shares the Logto localStorage session with it and gets
Playwright's `colorScheme` emulation (checked with a probe against
Lighthouse 13.5.0). So the earlier "/projects and config score 100"
evidence in `08-dashboard.md` §9 came from the signed-out landing the
dashboard redirected to. Each run asserts the audited URL's path, so a
bounced audit fails. The gate covers every routable page but `/callback`
(13), light and dark, at 1440x900 and 390x844, the sizes of CLAUDE.md's
"Judge visuals at real size": 52 runs.

**I-390. A 503 the api gives as an answer is not an outage, a 500 is not
"cannot reach", and one failure is said once.** (design critique repair,
2026-09-30; narrows `08-dashboard.md` §6 "API 5xx or unreachable") The
client set the outage bar on every status of 500 or more, and the api
answers `billing_disabled` and `waitlisted` with a 503 on purpose, so
/billing showed "Cannot reach the API. Retrying…" over "Billing is not
switched on yet." whenever billing was off. `isOutage` in
`lib/api/errors.ts` now leaves those two codes out; any other 5xx, and a
5xx whose body is not the api's JSON (a proxy's page), still raises the
bar. The bar has two wordings: "Cannot reach the API. Retrying…" when a
request got no answer, and "The API is failing right now. Retrying…" on a
5xx, since an api that answered 500 was reached. A failed first load
said one failure three times (the bar, the LoadState banner and a toast
with the same text); the projects list and the project page now raise the
banner alone before their first load, as the other four pages already
did, and the bar says something different. `errorText`, shared by the
toast and the banner, replaces the api's bare "internal error" with the
caller's sentence and "The API failed on its side; try again shortly."
Pinned by `errors.test.ts` and by `failure-modes.spec.ts` and
`billing.spec.ts`. *Rejected:* hiding the bar whenever a page shows its
own banner (the bar is the only signal that polling has backed off to
60s).

**I-391. Design critique repair: focus follows in-place panels, one
disabled look, and the gaps the first pass left.** (design critique
repair, 2026-09-30; amends I-370, I-376, I-381, I-384) The first pass
left these, found by the verify round and the a11y gate:
- Focus. A panel that opens in place of the button that asked for it (a
  secret's two-step delete, a snapshot's Restore and Restore as new, a
  destroyed project's Restore) removed that button, and focus fell to
  `<body>` (WCAG 2.4.3). The panel now takes focus ("Keep it", or the
  field of `ConfirmType` or `RestoreNameForm`), Cancel and Keep it give it
  back to the button, and a deleted row passes it to the next row's
  Delete (`lib/focus.ts`).
- `ConfirmType` has a visible label, "Type `slug` to confirm", where it
  had only a placeholder and an `aria-label`, which broke the Fields rule
  it is the main user of.
- Disabled buttons have one look: a `--rule-strong` outline on
  `--surface` with `--ink-faint` text. At `opacity-50` a primary button
  was a grey block (dark text on mid grey in the dark) and a danger
  button a pale red outline.
- Edges: the Nix editor (1.56:1) and the docs' copy button (1.2:1) take
  `--control-edge`; dark `--control-edge` is `#6e6e6a` (3.37:1 on
  `--sunken`, was 3.17). The Nix editor's content gets an `aria-label`,
  which the gate found missing once it audited the config page for real.
- The mark's grey blocks in the light are zinc-500 and `#888883` (4.8 and
  3.4:1), where the landing's shape greys were 2.5 and 1.5:1. Below `sm`
  the dashboard's mark alone is the 24px cut the docs use, not 28px.
- Docs and legal prose map the typography plugin's `--tw-prose-*` colours
  to the ink and rule tokens (its `prose-zinc` is Tailwind's stock cool
  zinc, and `prose-invert` made the dark h2 pure white); every heading is
  600. Inline code with no space in it (a command, a flag, a path) does
  not break across lines, and a command used as a docs heading is mono
  text, not a chip. The sidebar's "On this page" links are 28px tall
  (the gate's target-size audit failed at 22px).
- The breadcrumb spaces its `·` with a flex gap (template whitespace put
  4px before it and 10px after). The projects table's size sits on the
  state's baseline; a destroyed row shows its size as mono text in its
  meta line, not a badge, so a size looks the same in both lists. The
  outage bar uses the `.banner--error` class. Toasts line up with the
  content column from 600px up.
- `.field--set` and `.btn--lg` had no user and are deleted (I-378). The
  CSP no longer allows Google Fonts, and the CHECKLIST grep for font
  hosts covers `svelte.config.js`.
- The a11y gate's config target waits for the Menu tab, not a button
  (I-388 made it a tab). `KNOWN_FAILURES` names colour contrast on `/`:
  the landing pictures at 390 set their own greys inside
  `components/landing/*.svelte`, which the landing-critique branch
  rewrites; the entry goes when that branch lands.
*Rejected:* focusing the danger button of a two-step when it opens
(Enter would then delete); moving the toast to the top on a phone (it
would cover the header's links instead of the page's last button).

**I-392. Design repair round 2: ghost buttons show they can be pressed,
one accent token, pictures keep their tools' colours, and the keyboard
path is tested.** (design critique repair, 2026-09-30; amends I-374,
I-376, I-382, I-384) What the second verify round found, and what was
settled fixing it:
- Palette layers. DESIGN-LANGUAGE.md allowed no orange or pink anywhere,
  while LANDING.md prescribes Claude Code's orange mascot and its pink
  bypass line inside the hero. Both are right about different things. The
  no-new-hue rule covers what the site draws for itself (text, controls,
  rules, state, the landing's shapes and bar); a picture of a real tool
  keeps that tool's colours inside its frame, since it shows what the
  visitor will see.
- One accent token. `--accent` (link text), `--accent-strong` (a link
  under the pointer) and `--selection` join `--focus` on `:root`, with
  `text-accent` utilities, so no page picks a blue step.
  `src/lib/app-html.test.ts` holds `app.html`'s first-paint colours equal
  to `--page` and `--ink`.
- Ghost buttons. A muted word with no resting mark read as plain text
  ("Resize…", "Create", "Send test"). It now carries a hairline underline
  in `--control-edge` that turns to the text colour under the pointer;
  grey and underlined, it is still distinct from a blue link. Hover and a
  new pressed state (`active:`, one step past hover) apply to enabled
  buttons only. Transitions name their properties, because
  `transition-colors` animated the focus ring in. Choices of equal weight
  (a question's answers) are `.btn-quiet`, not a row of `.btn`.
- Retry keeps focus. A page clears its load error only on a load that
  worked, and LoadState marks Retry `aria-disabled` while it runs rather
  than `disabled`, since a button that turns disabled drops focus to
  `<body>`. `aria-disabled` takes the one disabled look.
- The Disk card's resize panel follows I-391's focus rule: the select
  takes focus, Cancel gives it back to "Resize…", and a grow that ends
  after a poll returns focus only if it was still in the panel.
- Skip link. "Skip to content" is every page's first tab stop, drawn after
  mount and focusing whichever `<main>` is on screen. The landing's
  `<main>` is in a file this round could not edit, so the prerender
  rejected a `#main` link there and Lighthouse's skip-link audit failed
  it; the root layout gives that `<main>` its id after each navigation.
- Ligatures. JetBrains Mono's contextual alternates spaced `://` apart;
  `font-variant-ligatures: no-contextual` is set on `html`, since the
  landing's pictures set the mono in their own styles.
- Docs. Only inline code of 30 characters or fewer stays on one line (the
  44-character ntfy URL scrolled /docs/notifications sideways at 390);
  longer spans wrap, `docs.test.ts` holds the limit. The docs h1 is the
  dashboard's `text-3xl` at every width. The copy button is 28px tall and
  its "Copied" is also said in a live region. The "On this page" chevron
  does not turn under reduced motion.
- Legal pages list their sections at the column's right edge from `lg`
  up. A 33rem policy alone left 450px of the 984px column empty. The text
  stays flush left under the logo as I-380 has it.
- Rows and readings. Row-end ghost buttons on secrets and config take
  `-mr-2`, like the snapshots row. The Disk card reads "X of Y", like
  billing. The Projects limit on billing is a meter like the other
  three. Event times sit on the summary's baseline. `.codeblock` lines
  scroll rather than wrap. Toasts on a phone span the 20px gutters.
- ARIA tabs on the config page: the keys are handled on the tabs, so the
  tablist has no tabindex, and only the current tab carries
  `aria-controls`. The menu's version select has its own `aria-label`.
- `illustrations/Session.svelte` and `session.json` had no importer and
  are deleted (I-378); `ops/dev/record-hero.md` says how to get them back.
- `ops/dev/decisions-index.py` read only the first id after "amends", so
  I-391's list marked I-370 alone; it now reads a whole list.
`tests/design.spec.ts` drives the skip link, the resize panel's focus,
Retry, forced colours, reduced motion, the phone width of five docs pages
and the ligature setting. *Kept:* the dashboard header's mark without the
word below `sm` (I-381, I-391); the docs' shell blocks without token
colour (I-388); the outage bar alongside a page's own banner (I-390); the
toast at the bottom on a phone (I-391).

**I-393. Design repair round 3: one failure is reported once, 503
answers come from one list, and links drawn as buttons answer the
pointer.** (design critique repair, 2026-09-30; amends I-363, I-381,
I-385, I-390, I-391, I-392) What the third verify round found, and what
was settled fixing it:
- Button links. I-392 gated the hover and press fills behind Tailwind's
  `enabled:`, and `:enabled` never matches an `<a>`, so the docs header's
  Dashboard and the landing's three button links lost both. The kinds use
  plain `hover:` and `active:` again, and the disabled rule, which already
  outranks them, keeps a disabled button inert. Ghost buttons use
  `:not(:disabled, [aria-disabled='true'])` for the same reason.
- 503 answers. `capacity` is a 503 the api gives on purpose, like
  `waitlisted` and `billing_disabled` (`statusOf` in
  `internal/api/http/server.go`), and it raised the outage bar beside
  Start's capacity banner. api.md's "Errors" paragraph now lists the
  three, `ANSWER_503` in `lib/api/errors.ts` holds them, and
  DESIGN-LANGUAGE.md and 08-dashboard.md point to api.md instead of
  repeating the list. Only a 503 is an answer: a 500 with any code is
  still an outage.
- One failure, one report. The project page's events and snapshots polls
  each toasted on every failed tick, beside the load banner; the projects
  list toasted on every failed refresh. `PollFailure` (`lib/api/toast.ts`)
  toasts on the first failure only, not at all while the load banner or
  the outage bar says it, and dismisses its toast when a tick gets
  through. The projects list, the project page's three polls and
  QuestionsCard use it; `toast.test.ts` holds it.
- The outage bar is said in a `role=status` region (`#outage`) that is
  always in the layout, so a screen reader announces the text when it
  arrives; inserted with its text, the region was often not read.
- Focus. Billing's Cancel plan now follows the documented two-step: the
  button turns into the question, the danger button is `.btn--sm`, Keep
  it takes focus when it opens and gives it back to Cancel plan, and a
  cancel that goes through puts it on Resume plan. Settings' Stay gives
  focus back to the link that asked to leave, or to the ntfy field when
  the browser's Back asked.
- Type. A legal page's h1, from its markdown, was the typography plugin's
  36px; `.doc h1` is `text-3xl` like every other page title. Running mono
  (a build error, a remote URL) was 12px against the 13px floor for mono
  that is more than a label; it is `text-compact`, and CHECKLIST.md has a
  grep for it.
- Forced colours mark the current config tab with a 3px `Highlight`
  edge; the others' edges are Canvas.
- Logo. The mark's middle block is `--control-edge` in both schemes
  rather than its light hex. The docs said the blocks were `--sh-grey` and
  `--sh-light` and that `--sh-*` never leaves `/`; the code, which I-381
  and I-391 settled, gives the mark its own greys and its big square
  `--sh-accent` in every header. LANDING.md and DESIGN-LANGUAGE.md now
  say so, naming the square as the one `--sh-*` token off the landing.
- Headers below `sm` show the mark alone on every page. The dashboard
  needs the room, and the docs and legal headers keeping the word made
  the header change between pages on a phone (amends I-391's "with the
  word on the docs and legal pages").
- Docs code with spaces (`--api-url URL`) breaks only at its spaces:
  each word of 30 characters or fewer is a `.nobreak` span, so a line no
  longer ends at `--`.
- The legal pages' "On this page" list sits 64px right of the text, not
  at the column's edge 230px away (amends I-392).
- The Nix editor is on `--sunken`, where code sits everywhere else, with
  the current line on `--surface`; on `--surface` it was a pure white
  block on the warm page in the light.
- The secrets page's empty state is an h2 and a sentence naming the form
  and `repose secrets set`, as "States" asks. DESIGN-LANGUAGE.md's
  "Replace" heading, left from the I-13 framing, is "Controls".
*Kept:* the landing header's 60px height and 216px inset (landing files
belong to the landing-critique branch).

**I-394. Design repair round 4: a quiet poll failure does not latch,
billing opens one panel at a time, and code wraps where it should.**
(design critique repair, 2026-09-30; amends I-393) What the fourth verify
round found, and what was settled fixing it:
- `PollFailure` latched a failure it kept quiet. A project's events poll
  whose first tick failed under the load banner never toasted after
  Retry loaded the project, however long it kept failing. Only a toast
  latches now; a quiet failure is looked at again on the next tick.
- Billing. Resume plan gives focus to Cancel plan, the mirror of what
  cancel does (WCAG 2.4.3). Change plan and the Cancel plan question
  close each other, so the question renders right under the action row,
  where its button was, and not under the plan list.
- Wrapping. The project page's remote URL uses `wrap-anywhere`, not
  `break-all`, so it breaks at a hyphen or a slash before mid-word. The
  legal pages render inline code with the docs' renderer, moved to
  `lib/codespan.ts`, so `repose-notify` no longer splits at its hyphen.
- Docs code blocks wider than their box take `tabindex=0` (set in
  DocPage, rechecked on resize, so a block that fits is no tab stop) and
  show a shade at the hidden edge; axe's scrollable-region-focusable
  would flag them in engines that do not focus a scroller on their own.
- DESIGN-LANGUAGE.md: the Nix editor's `--sunken` fill (I-393) is in
  the token table and under "Hairlines, not boxes"; the mark-alone rule
  below `sm` covers the `HeaderFrame` headers and names the landing, whose
  files the landing-critique branch owns, as the exception.
*Kept:* the landing's header and footer logos as they are, for that
branch to settle.

**I-395. Design repair round 5: a scroll edge is a one-colour bar, and
polls on one page share one toast.**
(design critique repair, 2026-09-30; amends I-393, I-394) What the fifth
verify round found, and what was settled fixing it:
- The docs code blocks' scroll shade (I-394) was two two-colour linear
  gradients and two radial ones, against DESIGN-LANGUAGE "Corners" and
  its CHECKLIST grep, and the radial layer read as a shadow. It is now a
  2px `--ink-faint` bar at each edge, drawn with one-colour
  `linear-gradient(c, c)` layers; two covers in the box's own fill
  scroll with the text and hide a bar once its edge is reached. The rule
  and the grep stay as they were.
- Docs tables get the same bar and the same tab stop as code blocks. At
  390, four `/docs/cli` tables were wider than their box, and axe flagged
  two as scrollable-region-focusable. Below `sm` a command in a table
  wraps at its spaces, which leaves one table (the environment
  variables, 15px over) that scrolls. The a11y gate now covers
  `/docs/cli`, and its tables' empty heads are labelled ("What it
  does").
- One failure, one report reaches the rest of the dashboard. The project
  page's polls and its questions card share a `PollGroup`: a 429 or a
  403 on one tick is one toast, gone when every poll it covers gets
  through. Billing toasts a failed account load only when the billing
  call answered; if billing failed too, its banner or the outage bar
  says it.
- The Nix editor's current line lifts in the dark as well: there
  `--surface` is darker than `--sunken`, so the line is `--sunken` with
  5% `--ink` mixed in (`#242423`; `--ink-faint` line numbers on it hold
  4.8:1).
- DESIGN-LANGUAGE "Type" now matches the CHECKLIST: mono under 13px is a
  badge and nothing else; the landing's labels are its own.

**I-396. The docs take a wider frame, with the "On this page" rail back
at the right and a sidebar that lists pages only.** (owner's review,
2026-10-01; amends I-380 and I-383) I-383 listed the open page's
sections under its link in the sidebar, so the sidebar grew and shrank
from page to page and moved what was under it, and at 1440 the right
third of the page was empty. The owner called it a downgrade. The docs
now pass `width="docs"` to `HeaderFrame` and put header and body on
`max-w-7xl px-5`: a 240px sidebar of pages only, the text column, and
from `xl` up a 224px rail at the frame's right edge. Inside the 1240px
frame that leaves the text track 696px, over the 622px (68ch) column
I-382 needs for I-345's 70 columns of 13px mono. The rail's column is
drawn on every page, empty when a page has fewer than two h2s, so
nothing moves sideways between pages: at 1440 the sidebar is at x=100,
the text at x=380 (622 wide), the rail at x=1116 on `/docs`, `/docs/cli`
and `/docs/secrets` alike, loaded or reached by a client navigation,
with no layout-shift entries (`tests/docs.spec.ts`). The rail sticks at
57px, under the header and its hairline, scrolls on its own when long,
and marks the section being read (an `IntersectionObserver` on the h2s,
`aria-current="true"`, ink, not the accent). Below `xl` the sections
fold under the description as before. On the docs the logo sits at x=100
at 1440 instead of the dashboard's 228: three columns do not fit the
dashboard's 1024px frame without squeezing the code, and a logo that
moves when you cross from the dashboard to the docs costs less than
either. At 390 it is x=20 on every page, as I-380 has it. *Rejected:*
widening the dashboard to match (its lists and forms would run long
lines for nothing); hiding the rail's column on pages with no sections
(the text would keep its x, but the frame would change from page to
page for no reader's gain, and the column costs nothing when empty).

**I-397. Landing repair round: the landing-critique branch is abandoned,
so the landing joins the house header, one picture palette, one large
button and the a11y gate, and its pictures stop when motion is turned
off.** (implementation, 2026-10-01: the owner's act is abandoning the
landing-critique branch, and the rest is implementation the owner has
not reviewed, LANDING.md "Since the owner's notes"; closes the items
I-393 to I-395 kept for that branch; amends I-380, I-389 and I-392) The
owner abandoned the
landing-critique branch; the landing on main is the base, and nothing of
that branch is merged or read. What I-393 and I-394 kept for it is
settled here:
- The landing's header is `HeaderFrame`, 56px over a `--rule` hairline
  like every other page, with `width="landing"`: the 1120px measure
  (`--land-w`) and the text inset (`--land-x`), so the logo stands over
  the headline at x=216 at 1440 and x=20 at 390. Below `sm` it shows the
  mark alone, as every header does (I-393). Docs and Pricing are visible
  at every width, GitHub from `sm` up, then the sign-in button; Pricing
  is in the footer too. The 60px top bar and its own logo rule go.
- The drawn pictures take their colours from six `--pic-*` tokens in
  `layout.css`, beside `--sh-*`: `--pic-accent`, `--pic-ink`,
  `--pic-dim`, `--pic-faint` (which is `--ink-faint`), `--pic-stop` and
  `--pic-add`. Each picture had picked its own steps, and the muted rows
  in zinc-400 (light) and zinc-600 (dark) held under 4.5:1 on `--sunken`.
  Red and green are a diff stat's, for deleted and added; I-392's
  exemption for real tools' colours is unchanged and does not reach these.
- `.btn--lg` (`px-5 py-2.5`) is the large button step, for the hero's
  "Get started" and pricing's "Start a free week", which used `!px-5
  !py-2.5` against I-376's no-override rule.
- Pictures follow `prefers-reduced-motion` while the page is open
  (`watchReducedMotion` in `landing/inview.ts`): turned on mid-loop, each
  picture stops on its final frame and each waiting shape group lands.
  Read once at mount, as before, the setting was missed until a reload.
- The hero's still frame (reduced motion, and before its loop starts) is
  its end state: the machine restored from the newest snapshot with the
  good work on it, the story's last beat, not its first.
- The a11y gate audits the landing under `prefers-reduced-motion:
  reduce`, in a Chromium of its own on the next CDP port (Playwright sets
  reduced motion per context, and applies it to the tab Lighthouse opens
  as it does the colour scheme). With motion on, Lighthouse sampled a
  different moment of each loop, and a row mid-fade failed contrast in
  one run and passed in the next. The landing's `color-contrast` entry
  in `KNOWN_FAILURES` now names what stays: the Editor capture's line
  numbers (`span.ln`), Tokyo Night's own grey.
- CHECKLIST.md runs the design greps over the landing too, with one
  named allow-list: `Editor.svelte`, `Browser.svelte`, `Ready.svelte` and
  `Localhost.svelte` keep their tool's hex and ANSI colours, and the
  hero's Claude Code colours are the only hex left outside them.
- A picture of a real tool may draw under the 11px floor and the 13px
  mono rule, as I-392 lets it keep its colours: it is drawn at the scale
  that fits the whole capture in its frame (the Editor's LazyVim screen
  is 8.4px on a phone, so 69 columns fit). A drawn picture keeps the
  floor.
*Rejected:* keeping a landing-only header (two headers drift, as the
logo rule did between I-381 and I-393); auditing every page under
reduced motion (the dashboard's only motion is the busy dot's pulse, and
the gate should see the pages as most visitors do); taking the drawn
pictures' colours from the Foundation's `--ink*` and `--accent` alone
(those are for text and links, and a picture needs a line colour and a
stop colour the text tokens do not have).

**I-398. Landing repair round, the details: the landing is prerendered,
`html.js` marks a scripted page, the hero's first paint is the empty
machine, captures move without layout, and every picture says only what
the product does.** (implementation, 2026-10-01; amends I-397's still
frame, I-392 and I-367's landing figure) Settled while the I-397 bundles
landed:
- The landing is prerendered (`routes/+page.ts`, `ssr` and `prerender`),
  as the docs and legal pages are: the headline, lead and plans come in
  the first response. Its sign-in buttons render signed out and swap in
  place; both labels share one grid cell, so "Get started" and "Start a
  free week" are as wide as "Open the dashboard" and the swap moves
  nothing. `<main id="main">` is in its markup, so the layout's skip link
  is prerendered on every page and the `afterNavigate` id patch is gone.
  An old docs slug's page now holds a `<main id="main">` with the new
  page's link, since the prerender rejects a `#main` link to a page with
  no target.
- `app.html` carries one inline script that adds `js` to `<html>` before
  the first paint; `svelte.config.js` allows it by its sha256 in
  `script-src`, and editing the script changes the hash.
- The hero has two still states. With reduced motion or scripts off it
  is the end of the loop: the machine restored, the stop cross and lit
  wall where the skill was turned back, no skill chip (in a restored
  machine it would read as still infected). With scripts on and motion
  allowed, the first paint is the empty machine the loop starts from, so
  the wreck never flashes. This replaces I-397's "before its loop starts"
  half.
- Every picture follows a mid-session reduced-motion change through
  `watchReducedMotion`, the hero included.
- Drawn-picture text that names context (paths, struck names, the
  `node_modules/` row) is `--pic-faint`, 4.5:1 or better on its row in
  both schemes; the strike and the stop sign carry the meaning without
  colour.
- Captures move by `translate`, `scale` and `opacity` only: the Browser
  page pans by `translate`, its ring is four 2px edges scaled to size.
  The captured log rows rest at 0.7 opacity, not 0.45 (5.0:1 at the
  least); the prompt's `❯` glyph keeps the tool's own 3.3:1 under I-392.
  The Browser picture's chip keeps `blue-600` in both schemes, since it
  sits on the captured page, which is white in both.
- The tmux status bars are cropped by the session name's ten columns,
  and Ready drops the first line of each two-line prompt as whole rows,
  so no capture shows `recruiting` there; nothing is retyped. Ready's
  rows crop on the right on a phone instead of re-flowing.
- OneCommand reads "Ready in 14s", the docs quickstart's figure, and the
  machine shows no `node_modules/`, since sync leaves dependency
  directories behind (`content/docs/sync.md`). Stacked on a phone, the
  copies travel only through the gap between the panels, and the chip's
  label wraps inside the frame at 320px.
- ComesBack's logins row is gh and Codex: the Claude Code login lives on
  the user's login share, which a snapshot does not hold (I-278). The
  Codex half is from `content/docs/agents.md`, not read on the owner's
  machine. The older tile folds by `clip-path`, its box keeping its
  height.
- Every step in "Three commands" has a Copy button; below `md` the
  commands wrap instead of ending in an ellipsis. With scripts off the
  buttons show and do nothing; hiding them waits on a rule under
  `html.js`.
- The hero's lead is `text-wrap: balance`; `pretty` left a lone "A" at
  1440. The sphere is drawn flat, with a `meridians` prop the footer's
  frieze uses.
- `landing/Sandbox.svelte` is deleted; nothing imported it.
*Rejected:* a hydrated landing with an SPA shell (the lead and plans
painted only after the bundle ran); cropping the Editor capture on a phone (no
left crop leaves the explorer whole; it is scaled to 8.4px, I-397);
showing the skill chip in the still frame.

**I-399. Landing repair round 2: the drawn pictures' rows are 12px
mono, every picture motion is in LANDING.md's list, OneCommand takes the
picture palette, the repair round's notes leave the owner's sections,
and the a11y gate's landing allowance is phone width only.**
(implementation, 2026-10-01; amends I-397 and I-398) A review of the
I-397/I-398 round found the docs and the code apart:
- A drawn picture's rows (a file name, a diff stat, a time, a git
  letter) are mono at 12px to 12.5px, 11px on a phone. I-397 said a
  drawn picture keeps the 13px mono rule, but the pictures never did:
  "Your working state" drew its rows at 12px when the owner chose it,
  and the hero and "Break it and roll it back" match it. The 11px floor
  holds; the 13px rule stays for the page's own text.
- Every motion the pictures run is in LANDING.md's "Motion" list or was
  brought to a listed kind: the skill's lunge is travel (`inOutCubic`),
  the wall's light and the Browser's typed lines are 200ms reveals, the
  Browser's log scrolls as travel, fades out are `inQuad` exits, the
  Localhost chip arrives with `outBack`, and ComesBack's snapshot mark
  turns a quarter in 420ms out-quad like the hero's. Three motions are
  added to the list with their timing: the hero's shutter, the skill's
  knock off the wall, and the Browser pointer's press. A wire's
  `stroke-dashoffset` joins the properties the pictures may animate; it
  is paint, not layout.
- OneCommand's git letters and "Ready" line drop amber, emerald and
  zinc steps of their own: `M` is `--pic-dim`, `U` is `--pic-add`, and
  the line beside the picture is `--ink-muted`. The Browser picture's
  ring and log bar are named for the ground they sit on (`--chip`,
  blue-600 on the white page; `--on-log`, blue-400 on the dark log). The
  green running dots stay open for the owner. CHECKLIST's landing hue
  grep now catches amber, emerald, zinc and the other Tailwind hues.
- The repair round had written its own rules into LANDING.md sections
  marked as the owner's direction (the picture palette in "Shape
  language", the logins row in "The grid cards", the "Ready in 14s"
  figure in "Your working state"). They move to a section of their own,
  "Since the owner's notes", which says they wait for the owner's review;
  the owner's sections read as the owner wrote them. I-397's heading
  said "owner", and the owner's only act in it is abandoning the branch.
- The a11y gate's `KNOWN_FAILURES` entries may name the widths they hold
  at. The landing's colour-contrast entry is phone width only: at 1440
  the Editor capture draws large enough to pass, and the desktop runs
  score 100 in both schemes.
- The hero's snapshot slot is server-rendered at the size measure()
  finds from 1280 to 1920 wide (an 84px miniature of a 344 by 296
  machine). The default had been 360 by 236, so hydration grew the slot
  and pushed the globe down, one 0.0002 layout shift at 1440. The hero
  hands over from its CSS first paint to the timeline (`.live`) when the
  first cycle runs, not when anime.js arrives: a picture under the
  observer's threshold at load drew the restored machine until it
  scrolled into view.
- The docs rail picks its section on scroll, once a frame, besides the
  observers: one jump past a heading (a wheel fling, `scrollTo`) left it
  stale or empty.
- The test fixtures start `go run` in a process group of its own and
  stop the group: a SIGTERM to `go run` never reached the binary it ran,
  and every suite left fakeapi and fake-logto running.
*Rejected:* setting the drawn pictures' rows at 13px (it changes the
picture the owner chose); masking `recruiting` in the Editor capture's explorer (no crop
removes it, and a blanked cell is an edit of a capture; a re-capture on
a machine named job-alerts is open for the owner, STATUS.md); auditing
the landing with motion on as well (a loop samples a different frame
each run, I-397; the gate checks the end state, and the first paint and
mid-loop frames are judged on captures, which is a gap the gate does not
close).

**I-400. Landing repair round 3: the Editor capture's rows are inert,
so the a11y gate has no allowance left; a restored row is blue, every
fade out names its ease, and the hero shows its still frame when the
app never mounts.** (implementation, 2026-10-01; amends I-397, I-398
and I-399) A review of the I-399 round found:
- The landing scored 96 at 390 in both schemes on colour contrast, all
  six nodes in the Editor capture: the explorer's file name and count,
  two comment lines, the branch and the position in the statusline
  (Tokyo Night's own #636da6, #545c7e and #82aaff on its grounds, 2.46
  to 4.27). I-399's `KNOWN_FAILURES` reason called them line numbers;
  they were not. Its claim that the capture "draws large enough to
  pass" at 1440 was also wrong: axe could not decide there
  (`pseudoContent`) and passed nothing in the frame. Each row of the
  capture is now `inert` beside its `aria-hidden`: the screen is one
  `role="img"` with its own label, its text is a picture's, which WCAG
  1.4.3 exempts as incidental, and `inert` takes it out of the tab
  order, find in page and selection as a screenshot would be. axe skips
  inert nodes, so the landing scores 100 at both widths and
  `KNOWN_FAILURES` is empty. The colours are the tool's, unchanged.
- "Break it and roll it back" no longer turns a restored row green or
  draws its tick green: both take `--pic-accent`, what moves and comes
  back. `--pic-add` is a diff stat's added count and nothing else.
- Every fade to 0 in the pictures names `inQuad` and lasts 300ms or
  more (ComesBack's travelling copy and arrows, the Browser ring, the
  OneCommand stop mark, the hero's database and node values). The
  motions I-399 left out of LANDING.md's list are in it: the Browser
  log's rows dimming to rest (`outQuad`, now named), the hero's file
  row lighting as it is written, the class-driven colour transitions
  (the `repose run` chip's fill, the address bar's edge, the log's bar,
  ComesBack's edges, tints and marks), and hover on links and buttons,
  which the Foundation already lists.
- The hero's first paint hides its rows under `html.js` until the loop
  runs. If the app's script never mounts the picture (a bundle that
  fails to load or throws), a 0s CSS animation with a 4s delay shows the
  still frame; onMount sets `.mounted` first thing, which drops it. The
  header comment no longer says the still frame shown before the script
  carries the return arrow: the connectors are measured by script.
- `/callback` had no `<main>`, so the layout's skip link pointed at
  nothing there. It has one, and `src/lib/skip-target.test.ts` holds
  every `+page.svelte` to a `<main id="main">` of its own or a shell
  that draws one.
- The hero's lead keeps each sentence whole (balance alone ended line 1
  on "The"); the pricing spec line keeps each dot with the fact before
  it, so no line starts with one. /projects breaks a remote URL after a
  slash or a hyphen, not mid-word.
- CHECKLIST's landing allow-list leaves out only the files that are a
  capture whole (Editor, Ready) for the hue greps; Browser and Localhost
  hold drawn marks and are read, with their counts (the Browser's two
  marks; five green dots, Localhost's and OneCommand's "Ready" dot
  among them). The hex and size greps still leave the two out for their
  captured parts.
- LANDING.md "Since the owner's notes" lists the rest of the repair
  rounds' changes to what the owner's sections govern: the flat sphere
  and the footer's meridians, the grid cards' lines, Copy on every step,
  Ready's dropped prompt rows and its crop on a phone, and the pricing
  caption in agents. The dead `.step p` rule and its documented size
  are gone; a step has no sentence.
- I-399 said OneCommand's "Ready" line dropped emerald. Its text did;
  the dot before it is still the green running dot (emerald-500), the
  fifth under STATUS.md's open item, and CHECKLIST counts it.
- "Your working state" drew a re-sync, which `repose run` no longer does
  (I-367): master's `7ea43a8` sat on the machine before the run and only
  the branch's two commits travelled, beside a "Ready in 14s" that only
  a new machine takes. The picture is now that first run: the machine
  starts with no commit and all three travel. The header comment no
  longer quotes the pre-I-367 "Synced: ... (2 new commits)" output.
- ComesBack's counts last 500ms, the top of the reveals' 200ms to 500ms
  (they were 650ms). Its snapshot mark no longer turns back a quarter
  when the shot is done: a class set the angle and its transition played
  in reverse when the class came off. The loop adds 90deg to a `--turn`
  per snapshot and the restore's full turn runs from that angle. (The
  hero's mark did not, as this said it did: it was set back to 0 at each
  loop's start, a half-turn cut; I-401.)
- The hero's reach wire to the internet is stroked `--pic-stop`, not
  `--rogue`: it is a drawn wire, and `--rogue` is the mascot's own red.
- The docs drawer scrolls the least that shows the current page's link,
  16px clear of the edge. It put that link a third of the way down, so
  on a phone /docs/cli opened with the search and "Start here" scrolled
  away while CLI reference was already on screen.
- Above a docs page's first h2 the "On this page" rail marks no section:
  the intro is no section. That was the code's behaviour;
  DESIGN-LANGUAGE.md now says so.
- `nix/guest/base/agent-guide.md` lists the three plans of I-362 (Solo
  8 GB and 250 GB, Plus 16 GB and 500 GB, Pro 32 GB and 1 TB, as
  `internal/billing/plans.go`); I-362 left the guide at the two-plan
  figures, and fb3995a fixed it without an entry. It is guest behaviour
  (`/etc/repose/agent-guide.md`, `nix/guest/tests` `guest-agent-guide`),
  so guests carry the new figures from the next base image, not before.
*Rejected:* recolouring the capture's text to 4.5:1 (it would no longer
be the tool's screen); rendering the capture as an image (a raster loses
the font and the crispness at every scale, and an SVG `<text>` grid is
the same text to axe); hiding the text from axe with CSS `content` (it
would quiet the audit and say nothing true about the page); renumbering
the I-369 collision with restore-fast here (whichever branch merges
second renumbers, STATUS.md).

**I-401. Landing repair round 4: the snapshot marks only turn as listed,
the hero's lead wraps inside a sentence before it scrolls, the docs
sidebar scrolls only for a cut link, and the dashboard's command block
shows where its line runs on.** (implementation, 2026-10-01; amends
I-400) A review of the I-400 round found:
- The hero's snapshot mark cut a half turn at each loop's start. Two
  quarter turns and the full turn back leave it at -180deg, and `pre()`
  set it to 0 with no motion while the panel title's pinwheel was in
  view, its two quarters (full and 0.45 opacity) swapping places. `pre()`
  now keeps the angle, folded into 0 to 360. ComesBack's comment and
  I-400 said the hero's mark never turned back; both are corrected.
- ComesBack's rewind hung on `.lit`, which the still frame (markup and
  `still()`) also sets, so with motion allowed the mark spun a full turn
  on the still frame at load and when reduced motion was turned off
  again. It now hangs on `.rewind`, set only by the loop's restore.
- The hero lead's sentences were `white-space: nowrap`: at 320px under
  WCAG 1.4.12 text spacing the first ran to x=366 and the page scrolled
  sideways (scrollWidth 366). Each is now an inline block no wider than
  the column, whole where it fits and wrapping inside itself where it
  does not.
- The docs sidebar scrolled whenever the current link sat within 16px of
  an edge, so at 1440x900 /docs/troubleshooting moved the list up 15px,
  and the scroll stayed for the next page. A link that shows whole now
  moves nothing. A cut one is scrolled to with 16px to spare, and going
  down the next link shows whole too, or the list's end for the last
  link: the 390 drawer on /docs/cli cut "Troubleshooting" at the bottom
  edge (I-400's "least scroll").
- `.codeblock` scrolls sideways as the docs' blocks do but showed neither
  their edge bars nor a tab stop: at 390 the /projects install line was
  cut at ".../inst". It now takes the same `--edge-*` layers, and the
  /projects block takes a tab stop while it overflows
  (`tabStopWhenScrolls`, `lib/scroller.ts`). DESIGN-LANGUAGE.md says so.
- Localhost's card line said a server is on your localhost "within a
  second", where the docs say "within a second or so" and ports below
  1024 are not forwarded, and it explained the picture, which the
  owner's Copy rule rules out for a card. It is now "Ports from 1024 up,
  while `repose run` is open. Cookies and OAuth redirects behave as they
  do locally.", facts the picture does not show.
- Step 3's Copy button copied `cd ~/code/job-alerts && repose run`, a
  path a visitor's laptop does not have. It copies `repose run`; the row
  still shows the example, and the button's name says what it copies.
- The pricing sentence, the spec line and the meta description changed
  in I-397 without a line in LANDING.md "Since the owner's notes"; they
  have one now.
- The Editor comment gave the capture's failing contrast as 3.1 to 4.27;
  the colours it names (#545c7e, #636da6, #82aaff on their grounds) run
  2.46 to 4.27, as I-400 says. The comment now agrees.
- LANDING.md "Motion" said every listed motion runs only without reduced
  motion; the colour transitions on hover and on a class change are
  unconditional CSS. The intro now says so. `app.html`'s comment said
  the app renders only in the browser; the landing, docs and legal pages
  are prerendered.
*Rejected:* fitting the docs list into 843px at 1440 by trimming the
sidebar's padding (it fits only at that height, and a link one pixel
cut at a shorter window would still move the list); resetting the hero's
mark with a 180-degree turn (a motion LANDING.md does not list); copying
the shown command and leaving the path to the visitor (the copied text
would fail as pasted).

**I-402. Pricing says "memory" and counts no agents.** (implementation,
2026-10-01; amends I-397, I-290's wording)
The landing's pricing captions ("One agent at a time", "Two agents at
once", "Four agents at once", I-397) read to the owner as a cap on what
repose does, when the plan caps memory and nothing else. And "8 GB
running at once", the phrase the billing page, the waitlist and plan
emails, the docs and the fake api's refusals used, does not say it is
memory. The cards now drop the caption and read "N GB of memory · disk ·
egress"; the pricing sentence adds "A plan's memory is shared by the
machines you have running; a stopped machine uses none." Everywhere a
user reads the limit it is "N GB of memory for running machines": the
billing page's change rows, the waitlist_joined and plan_changed emails,
`limits.md`, the terms, the fake api's over_plan and payment_required
messages, and the Paddle product description that `billing bootstrap`
creates. Bootstrap only creates products, so products already in Paddle
keep the old description until it is edited in Paddle's dashboard.
Internal docs (DESIGN.md, RUNBOOK, OBSERVABILITY, PRICING's prose) keep
"may run at once", which their readers know means memory.
*Rejected:* counting machines by size class ("one large, or two
small"): a visitor has not met the classes, and a count is still a
ceiling.

**I-403. A restore writes the volume with O_DIRECT, eight writes in
flight, and downloads the snapshot as eight ranged GETs at once.**
(owner, 2026-10-01; amends the restore steps of 03-hostd.md §5.9 and the
raw path of I-164) The owner's `repose restore job` took 2m17s. The
Restore command on host-01 took 124 s of that (04:07:27 to 04:09:31Z),
for a snapshot of 902 MB compressed, 5.0 GB used on a 40 GB volume, which
took 11.4 s to make. Measured stage by stage on host-01 against that
blob and a scratch thin volume: the download 8.7 s on one GET, `zstd -d`
2.5 s, the 1,796 `pwrite`s 1.0 s, `e2fsck -fp` 0.6 s, and the `fsync`
125.6 s. The 4.3 GB of records went into the page cache at once and the
kernel's writeback drained them into the new thin volume at 34 MB/s; the
data disk is Premium SSD v2 at 16,000 IOPS and 600 MB/s. Sixteen
buffered writers made no difference (42 MB/s). With O_DIRECT the same
stream took 8.0 s on one writer, 5.6 s on four, 5.0 s on sixteen and
6.7 s on sixty-four, `e2fsck -fn` clean every time. How:

- *Direct writes* (`internal/hostd/snapshot/direct.go`). The restore
  opens the volume twice, once with O_DIRECT. Records go to eight
  goroutines from a pool of ten 4 MiB buffers aligned to 4096, so a
  restore holds 40 MiB however large the volume. A record whose offset,
  length or start in its buffer is off a 4096 boundary waits for the
  writes in flight to land and switches the rest of the restore to the
  page cache, which would otherwise read the record's partial page from
  the device under a direct write to the rest of it. A record that
  overlaps one in flight waits for it, so the result is what one writer
  in stream order gives (the stream's records are ordered and disjoint,
  so neither happens on an ext4 snapshot). A filesystem without O_DIRECT
  falls back to the page cache. The final `fsync` stays: it is the flush
  that commits the thin pool's mappings.
- *Raw images too.* The raw path was `zstd -d | dd bs=4M
  conv=sparse,fsync`, which has the same writeback. It is now the same
  writer: each 4 MiB chunk that is all zero is skipped and any other is
  written from its first non-zero 64 KiB piece to its last, never less
  sparse than dd was. An image longer than the device is refused, as dd's
  write past the end was. dd is no longer used on the way in.
- *Ranged download* (`AzureBlob.Download`, `parallelRanges`). Once the
  writes took 5 s the single GET (104 MB/s) was the floor. The blob's
  length and ETag come from one `GetProperties`; then 8 MiB ranges, eight
  in flight and each pinned to that ETag with If-Match, are written to the
  pipe in order. Memory is nine blocks, 72 MiB. The first error, from a
  range or from the pipe, cancels the rest. The 902 MB blob downloads in
  1.2 to 1.6 s (8.7 s before), the 4.0 GB one in 5.7 s.

Measured on host-01 with the real `AzureBlob.Download` and
`Pipeline.Write` built from this change, onto scratch thin volumes
(`bench-*`, removed after): the `job` snapshot in 5.4 and 6.1 s (124 s
before); the same snapshot restored by the old path onto a second volume
in 135 s, and `cmp` of the two 40 GB volumes found them identical; the
largest snapshot on the host (4.0 GB compressed, 19.4 GB used) in 29.2 s,
which is the disk's 600 MB/s, then `e2fsck -fp` 3.5 s; and a raw image
(a 4 GiB volume with no filesystem and 1 GiB of scattered random runs,
read by `Pipeline.Read`) written back in 3.0 s, `cmp` identical and both
volumes at 25.00% of their thin allocation. hostd now logs `restore done`
(`restore_done`: bytes downloaded, `duration_ms`, `write_ms`, `fsck_ms`)
and `restore failed` (`restore_fail`), so the next slow restore says where
its time went without a benchmark. A restore now
takes about the Build phase (5.7 s of eval for a destroyed project,
I-115), the write, and the start (6 s). Tests: `TestDirectRestoreMatchesBuffered`,
`TestUnalignedRecordGoesBuffered`, `TestOverlappingRecordsLaterWins`,
`TestDirectWriteErrorIsReturned`, `TestRestoreRefusalsStillHoldWithWriters`,
`TestRawRestoreIsSparseAndExact`, `TestRawRestoreRefusesALongerImage`,
`TestRawThroughPipelineIsDirect`, `TestOpenDirectFallsBack`,
`TestParallelRangesOrderAndSizes` (which caught a ninth fetch in flight
with eight allowed), `TestParallelRangesErrors`; the existing round trips
now run on the direct path. *Rejected:* `dd oflag=direct` for the raw
path (a short read from the pipe makes an unaligned write unless
`iflag=fullblock`, and it would still be one write at a time);
`sync_file_range` to push writeback along (the drain rate, not its start,
was the problem); turning off the thin pool's zeroing (not measured: it is a
pool-wide setting that changes every guest's first writes, and the direct
path already runs at the disk's rate); azcopy
(it cannot write to a pipe, as I-164 found for upload). *Not done:* the
snapshot side still reads through the page cache (11.4 s for this
volume), and Build still runs before Restore rather than beside it.

**I-404. A stop uploads its snapshot while the guest shuts down; the
snapshot read itself stays as it was.** (owner, 2026-10-01: "work on the
optimizations for snapshotting") Measured on host-01 against scratch
volumes holding the `job`, `unwrap` and a 19.4 GB-used snapshot: dumpe2fs
25 ms; reading the used blocks 5.4 to 7.2 s, 9.1 to 9.6 s and 28.6 to
30.8 s, which is the data disk's provisioned rate (600 MB/s, read at 650
to 700); `zstd -T4 -3` keeps up (the whole read plus compress took the
same time at -T4, -T8 and -T16, and at -1, -2 and --fast=3, which only
made the blob 7 to 40% larger); the upload added about 0.2 s whether 4,
8 or 16 blocks of 8 or 16 MiB were in flight; `lvcreate -s`, `lvchange
-ay -K` and `lvremove` 50 to 100 ms each. Reading with O_DIRECT, eight
chunks in flight, took the same time as the page cache (5.5 vs 5.5 s,
9.3 vs 9.1 s, 30.2 vs 28.6 s) and showed no page-cache difference in
`/proc/meminfo`, so it was not kept. A snapshot is the disk's speed;
reading less (an incremental snapshot) or a faster disk is what would
change it.

What the user waits for can change. A `repose stop` froze, took the LVM
snapshot, thawed, read and uploaded the whole snapshot with the guest
still running and billed, and only then shut the guest down (5 s for
`job`). Since the LVM snapshot is a fixed point in time once taken,
`StopGuest` with `snapshot_first` now takes it (`takeSnapshot`), starts
the upload (`uploadSnapshot`) and shuts the guest down at the same time,
returning when both are done: the stop waits for the longer of the two,
and the guest's hours and sessions end right after the freeze. A freeze
that fails returns before anything shuts down, as before, so the api's
I-157 recovery is unchanged. An upload that fails while the guest stops
is taken again from the stopped volume (the I-158 path); if that fails
too the stop reports the upload failure with the guest down and its
volume kept, the project goes to `error`, and `repose start` boots it
(PlanRestart), as for any failed stop. Tests:
`TestStopUploadsWhileTheGuestShutsDown` (the upload waits for the guest
to leave `running`; with the old order it times out, checked by running
it against the old `stop.go`), `TestStopRetriesTheSnapshotFromTheStoppedVolume`
(also fails on the old order), `TestStopReportsASnapshotThatFailsTwice`,
`TestStopWithAFailedFreezeStopsNothing`; the snapshot, restore and stop
tests that were there pass unchanged. `MemBlob` gained `FailNext` and
`BeforeUpload` for them. *Not done:* incremental snapshots (a design
change of the blob format and of restore) and a faster data disk (an
infra cost: the owner's call).

**I-405. hostd caches an evaluation by its inputs and skips `nix eval`
when they recur.** (owner, 2026-10-01: "faster restores") A restore of a
destroyed project runs Build before Restore (I-115), and on host-01 that
Build was 5.3 s of `nix eval` and 0.35 s of `nix build` for the
`job` restore: the configuration was the one evaluated when the project
was made. With `pure-eval`, `restrict-eval`, no import-from-derivation
and inputs locked by the base's `flake.lock`, the evaluation reads the
base checkout (a git revision, checked out once), the flake attribute,
the fragment and the `base_version` label, nothing else.
`internal/hostd/nixbuild/evalcache.go` hashes those into a key and keeps
`/var/lib/repose/builds/.evalcache/<key>` holding the derivation of a
build that succeeded. A hit whose `.drv` is still in the store
(`keep-derivations = true` on hosts keeps it while the closure is
rooted) goes straight to `nix build`; a hit whose build fails is removed
and the build runs again with a fresh eval, so the cache can cost time
and never change a result. The build log lines are unchanged (the CLI
reads them, I-320); `build done` gains `eval_cached`.
nix-build-contract.md "What hostd runs" says so. Running Build beside
Restore was rejected for now: an op holds one command at a time
(`ops.command_id`), so it needs a migration and engine change for 5 s.
Tests: `TestEvalCache` (hit, each input missing, a collected `.drv`, a
stale hit rebuilt from a fresh eval, a failed build not cached, log lines
unchanged); `TestRealNixEvalCache` with real Nix against the test flake:
the second revision with the same fragment built from the cache in 384
ms with the closure a fresh eval of its directory gives, and a changed
fragment evaluated; the real-Nix corpus passes.

**I-406. `start` on a project with no guest runs its create again.**
(dogfood, 2026-10-01) `repose run` made `recruiting-2`, whose create op
failed at placement with `capacity`; I-356 reported that, but the project
stayed in `error` with no `guest_id`, and every later `repose run` and
`repose start recruiting-2` enqueued a restart whose `start_guest` phase
answered "project has no guest; create it first", while `repose ls`
suggested that same `repose start`. No command creates an existing
project, so the only way out was `repose rm`. `POST /projects/:id/start`
on a project whose `guest_id` is null now enqueues a `create`
(`build`, `create_guest`), sets the project `creating` in the same
transaction, clears `host_id` first when that host is not ready or is
draining (a guest-less project holds nothing there), and answers
`{op_id, restart: false, create: true}`. The create's own failure
(capacity again, a build error) is reported as any create's is. The CLI
shows "Creating NAME" for it; an older CLI shows "Starting NAME" and
waits on the op as it always did. Rejected: a `repose create` command or a
create flag on start (one more thing to learn for a state the user did
not choose); destroying the project when its create fails (the name and
any secrets the user set would go with it). api.md's start row gains
`create`. Tests: `TestStartCreatesAProjectWhoseCreateFailed` (on the old
code: `restart: true` and the create-it-first error),
`TestStartOfAProjectWithNoGuestShowsTheCreate`.

**I-407. `repose run` waits for a destroy that holds the name it wants,
instead of creating NAME-2.** (dogfood, 2026-10-01) `recruiting` had been
restored with no remote; `repose rm recruiting` and, five seconds later,
`repose run` in the checkout: resolve matches by remote, so it did not
find `recruiting`, the create met the unique slug index (live until the
destroy ends) and run went on to `recruiting-2`. I-301 already waits when
the resolved project is the one being destroyed. On a `conflict` from
`POST /projects`, run now looks the name up (`findByName`) and, when that
project is `destroying`, waits for it with I-301's wait (same phase line,
same 10-minute bound) and creates the same name; each project is waited on
once. A live project with the name still gets NAME-2. Test:
`TestRunWaitsForANameHeldByADestroy` (the old code made recruiting-2).

**I-408. Placement waits up to three minutes for a guest being stopped
before it answers `capacity`.** (dogfood, 2026-10-01) The same
`recruiting-2` create came 5 s after `recruiting`'s destroy began, on
host-01 (62 GB, five other guests). hostd counts a guest in `stopping`
in `free_mem_bytes` (class RAM plus 512 MiB), so free memory read about
7.8 GB until the stop ended, and the large create failed at once; 6 s
later host-01 had 16 GB free. Placement (`build` of a create, `restore`)
that finds no host now asks `scheduler.Freeing`: is there a host
`PickHost` could choose that fits the class once the guests on it in
`stopping` or `destroying` are down (their class RAM back in
`free_mem_bytes`, and in the reservations for `stopping`; `destroying` is
already out of `host_reservations`)? If so, and the op is younger than
`ops.Config.PlacementWait` (3 min: a stop's 60 s timeout plus heartbeats),
the phase is not failed and the next engine tick places again; the first
wait logs `schedule_wait` (registered in obs). Otherwise `capacity` as
before, with `schedule_fail`. Rejected: counting `destroying` guests in
`host_reservations` (it makes the host look fuller, the opposite of what
the create needs); retrying in the CLI (every client would need it, the
dashboard too). Tests: `TestPlacementWaitsForAGuestBeingStopped` (the
create waits while the old guest is `stopping`, places once it is
`stopped`; with a 1 s wait and a guest that stays `stopping`, `capacity`
after the wait; on the old code `capacity` at once); `TestCapacityError`
unchanged.

**I-409. hostd sends a heartbeat ahead of every command result.** (owner,
2026-10-01, the live check of I-403..I-405) On host-01, `repose rm
e2e-giftbox --wait` then `repose restore e2e-giftbox` spent 7 s before the
restore's Build was sent: the destroy finished at 20:19:04Z and the api
logged `schedule_wait` (I-408's placement wait), placing the project at
20:19:11Z. The host was near its memory, and the api places from
`hosts.free_mem_bytes`, which hostd refreshed only in its 15 s heartbeat,
so the memory the destroy had just freed was not seen until the next one.
hostd's stream now sends a Heartbeat right before each Result
(`internal/hostd/stream`), and the api handles a session's messages in
order, so `free_mem_bytes` is current when the engine (500 ms tick) next
tries the placement. A heartbeat is one `update hosts`; one per command is
nothing next to the command. grpc-hostd.md says so. Test:
`TestHeartbeatAheadOfEveryResult` (two commands give `hb,result,hb,result`;
`result,result` against the old sender).

**I-422. The laptop chooses which logins `run` copies: `repose secrets
choose` and `[logins] skip` in config.toml.** (owner, 2026-10-03: "for choosing
what credentials are copied over, think it should be a per CLI config?
like some way to repose .... and it lists credentials detected and you
tick/untick what you dont want sent off? plus perhaps a config file?")
This amends R2-8, I-150 and I-197, which copy gh's, Codex's and opencode's
logins and every gitignored `.env` file at each `run`, with no way to
leave one behind. A user who does not want an agent holding a full-scope
`gh` token, or a `.env` with live keys, had to log out on the laptop or
move the file. `docs/proposals/2026-09-27-credentials-convenience-first.md`
planned the `.env` opt-out; this is that item, with the tool logins in the
same list.

The choice lives on the laptop, in `~/.config/repose/config.toml`:
`[logins] skip = [...]` for every project, `[projects.NAME.logins] skip`
for one (NAME is the project's name; its list replaces the global one, and
`skip = []` copies everything for it). The names are the credRows labels
(`gh`, `codex`, `opencode`) and `env`. The api never holds it: the logins
go laptop to guest over SSH and the api never sees them (R2-8), so a
dashboard switch would be a setting about files the api has never seen,
and each laptop decides for the logins it has. Nothing changes for a user
who sets nothing: everything is copied, as before, and the `Credentials:`
line adds "Choose which logins are copied with `repose secrets choose`" when a
login travelled and no list exists (not on the runs that name only `git`,
which is every run).

`repose secrets choose` shows a toggle list on a terminal (space, Enter,
`q`) and prints the list otherwise, with what this laptop has (a login
missing, the checkout's `.env` count); `--off NAME...` and `--on NAME...`
set it from a script; `--reset` drops the scope's table; `--project` picks
the project's own list. `repose secrets list` on a terminal shows both
kinds, the secrets repose stores and what this laptop copies; piped, it
keeps its one `NAME<tab>DATE` line per secret. The command sits under
`secrets` because the docs already put the copied logins on the secrets
page, and a top-level `repose logins` was one letter from `repose login`,
which is the account (owner, same session: "repose logins kinda makes me
think its a repose secrets thing"; "repose secrets copy sounds like you
are telling it to copy the secrets", so the verb is `choose`). The command edits only its own table, keeping the rest of the
file and that table's comment lines byte for byte, writes a symlinked
config at its target, keeps the file's mode, and refuses (leaving the file
alone) when the result would not decode to the list asked for, as with a
hand-written top-level `logins.skip` dotted key.

A skipped login is not sent and, for `gh`, the git helper and `insteadOf`
rewrites are not set. The copy an earlier run left is removed by the I-298
rule: only its SHA-256 travels, and the guest deletes its file only while
it is byte for byte what the laptop would send (`hosts.yml` with the
keyring token written in), printing `#warn` once; a login made on the
machine stays. The removal lines are in the creds part's hash
(`skip <label> <sha>`), so turning a login off sends the part once. With
`env` off the sync writes no `.env` file; when the guest has an earlier
carry (its `env` marker, or `#envmissing`), the apply removes each copy
whose SHA-256 is the laptop file's, names the ones that differ
(`#envleft`, edited on the machine) and deletes `~/.repose/env-paths` and
the marker, so turning `env` back on sends the set again. Snapshots taken
before keep their copies; the docs say to revoke the token.

*Rejected:* a top-level `repose logins` (above); `repose secrets copy`
(reads as an order to copy); a per-project setting in the api, shown in the dashboard (the
reasons above); a file in the checkout (a committed file would decide for
every teammate's laptop, and what travels depends on what each one is
logged in to); an allow list (`only = [...]`; a login type added later
would be off by default, against the convenience default of the proposal;
it can be added beside `skip` if asked for); a prompt at the first `run`
(it would stop scripts and `--no-attach` runs).
**I-416. Work happens in worktrees and reaches main through a release
queue.** (owner, 2026-10-03: "start a release queue so multiple agents can
work on features and queue and coordinate a full release. Also the default
should be working in a worktree.") Four agent sessions were working in
this checkout at once, on their own branches and on main. CLAUDE.md said to
work on main and ask before branching, so a session either edited the main
checkout under the others or asked first. Decision ids collided: on
2026-10-03 feedback-batch and start-fast had both written I-410, as
restore-fast and the design round had both written I-369 two days before.
Merges and deploys happened from whichever session finished, so a release
was never a known set of branches.

Now every change starts in `~/<checkout>-<slug>` on its own branch with no
need to ask, and the main checkout stays on main. `ops/dev/release-queue`
(bash, state in the common git dir under one flock, on no branch) does
four things: `id` reserves decision ids after reading every local branch,
every worktree's uncommitted DECISIONS.md and earlier reservations; `add`
queues a clean branch that merges into main (a conflict only in the
generated DECISIONS-INDEX.md is allowed) and names its deploy targets from
its diff (Go through `go list -deps`, so a shared package counts for each
binary that imports it); `cut` and `resume` merge the queue in order into
a `release/<id>` worktree, regenerating the index when that is the only
conflict; `done` refuses until main holds the release, then removes its
worktree. One conductor session verifies the merge, asks the owner once
per release, fast-forwards and pushes main, publishes the base and tags
the CLI as the targets need, runs each branch's live check and records the
release in STATUS. `docs/ops/RELEASE.md` is the procedure; host and edge
switches stay the owner's.

*Rejected:* a queue file committed on main (every `add` would be a commit
on main from a worktree, racing the conductor's merges); per-branch queue
files union-merged into the repository (the conductor would have to scan
every branch to learn what is queued); a GitHub merge queue (pull requests
are not used here, and agents do not push).
**I-415. The feedback board is Fider's hosted `repose.fider.io`, and you
sign in there with your repose account through Logto.** (owner,
2026-10-01)
The owner opened a free Fider board for bugs and ideas. Fider's custom
OAuth provider "repose account" points at the shared Logto tenant
(I-340) through a fourth repose application, `repose feedback`
(Traditional web, `wc2np1n3r9z4acp2wutev`), so a person posts with the
login they already have and Fider never asks for a password. The
application's name is what Logto's emails say ("... is your repose
feedback sign-in code"); the owner asked for a name that tells it apart
from the dashboard and CLI apps, which are both `repose`. Fider reads the
profile from `/oidc/me` with the scope `openid profile email`: id `sub`,
name `name, username`, email `email`. The name path stops before
`email`: an account made with an email code has neither name nor
username, and Fider shows names publicly, so Fider's own fallback (the
part before the @) is what such a person shows until they change it in
Fider. Sign-up does not start collecting given, family or user names for
this: Logto's profile collection is tenant-wide, so it would add a step
to every repose and Job Alerts sign-up for a display name on one board.
The site links the board from the landing footer and from
Troubleshooting's last section, and the privacy policy names Fider as
the host that receives a poster's name and email. The footer's seven
links stand four over three below md, and wrap freely below 360px, where
four with WCAG 1.4.12's spacing are 341px wide. `ops/fider/README.md`
has every setting.
Fider's Facebook, Google and GitHub sign-ins are off (owner), so a
person has one identity on the board; Fider's email sign-in stays on.
*Rejected:* a Feedback link in the dashboard header (five items already
share 350px at phone width).
**I-417. A boot sets the old /tmp aside in one rename and deletes it after
the boot.** (owner, 2026-10-02: "starting a stopped project is taking 27
seconds") On host-01, StartGuest for a stopped large guest ran 01:32:13Z
to 01:32:40Z. hostd had Cloud Hypervisor up within 0.1 s; the guest's
console showed systemd-tmpfiles-setup ("Create System Files and
Directories") running for 20 s before sysinit.target, and guestd, sshd and
the ready signal all wait for sysinit. kanali's own boot measured the same:
19.7 s in that unit, 18 s of it between two entries of /tmp. The cause was
`boot.tmp.cleanOnBoot`, whose `D! /tmp` rule deletes the last boot's /tmp
file by file, and a guest's /tmp is on its persistent volume, where a day
of go test, browsers and builds leaves hundreds of thousands of files.

/tmp still starts every boot empty. `repose-tmp-rotate` (before
systemd-tmpfiles-setup, no default dependencies) renames a non-empty /tmp
into `/var/lib/repose/tmp-old/<random>/tmp` and makes a new 1777 /tmp; the
rename is one syscall (0.07 s for 200,000 files on kanali, where deleting
them took 2.9 s on a quiet disk and 18 s at boot).
`repose-tmp-purge.timer` deletes the set-aside trees a minute after the
boot at Nice 19 and idle I/O class, and nothing a boot waits for depends
on it. A /tmp that is a mount point (a tmpfs a user configured) is left
alone. The rotate runs once a boot (RemainAfterExit; on host-01 a second
start request ran it again 0.24 s after the first) and exits once
systemd-tmpfiles-setup is active: a base
applied without a reboot restarts the active targets, and sysinit.target
would otherwise start it on a running machine, under its tmux and
browsers. A stop before the purge snapshots the set-aside tree, the same
bytes the old /tmp held.

*Rejected:* /tmp on a tmpfs (a guest's large builds and browser profiles
in /tmp would then take its memory); clearing /tmp at stop instead (a stop
is waited on too, and a guest that crashes skips it).
No public doc changes: /tmp's behaviour is the same, and the docs already
say a start takes about 10 seconds (`/docs`, index). Test: guest-tools-carry
subtest "I-417" (a 2,000-file /tmp is gone after a reboot, set aside under
tmp-old, no `D! /tmp` rule, a start of the unit on the running guest
leaves /tmp alone, the purge empties tmp-old). Measured on host-01
(2026-10-03) with main's guest system and this one booted by hand on one
scratch thin volume (2 GB, 4 vCPU, the production kernel, initrd and
Cloud Hypervisor arguments, no network, no hostd), /tmp holding 200,405
entries before each measured boot: main spent 4.72 s and 4.43 s in
systemd-tmpfiles-setup and guestd listened at 9.78 s and 8.43 s; this
one spent 0.16 s and 0.15 s and guestd listened at 4.72 s and 4.74 s. After
each boot /tmp held only that boot's own 4 entries, and the purge ran at
60 s and emptied tmp-old in 3.1 s and 3.3 s.
**I-410. The command-not-found hint survives a command only one package has.** (owner,
2026-10-03, dogfood on unwrap) Typing `az` on a machine without it printed
nothing and returned 1. The handler (`nix/guest/base/devtools.nix`, I-219)
runs under `writeShellApplication`'s `set -euo pipefail`, and its "other
packages" line is `grep -vxF "$attr"` over the list of packages with the
command. With one package (`az` is only in `azure-cli`; `htop` showed up
fine because `htop-vim` has it too) that grep matches nothing, exits 1, and
the script ends before it prints a word. The grep is now `{ grep ... ||
true; }`. Checked on unwrap with the patched script: `az` prints the
not-found line and `nix profile add nixpkgs#azure-cli`, exit 127. VM test:
`guest-devtools` subtest "I-410". Reaches machines with the next base.

**I-411. `repose exec` takes the command with or without `--`.** (owner,
2026-10-03) `repose exec grep ADMIN_PASSWORD prod.env`, inside the
project's checkout, answered `put the command after --`, and so did the
same with the project named; the agent on the machine had told the owner
to run it that way, and `ssh-and-editors.md` already showed `repose exec
pwd`. Flags now stop at the first word (`SetInterspersed(false)`), so the
command's own flags reach it, as with `docker exec`; exec's `-i`/`-t` go
before the command. With no `--` and no `--project`, a first word that is
one of the account's slugs is PROJECT (one `ListProjects`, skipped when
the api fails), and that word alone is refused with exit 2, since running
it would print only "command not found". The I-275 form `PROJECT [-i] [-t]
-- COMMAND` still works: a `--` after one word and exec's flags only is the
separator, and any other `--` is the command's (`repose exec git log --
main.go`). `repose exec -- COMMAND` runs a command named like a project.
Tests: `TestExecSeparated`, `TestExecCommandFlagsPassThrough`, and the
project-word cases in `TestExecRunsInTheCheckout`.

**I-412. The docs as markdown at /llms.txt, and the laptop's CLI version on the machine.** (owner,
2026-10-03) An agent on unwrap gave the owner a `repose exec` command line
from memory that the CLI refused. The owner asked for a link to the docs on
top of the machine guide, and for the user's CLI version, since the docs
describe the latest release and users run older ones. The web app now
prerenders `/llms.txt` (every page, by section, with its description) and
`/docs/<slug>.md` (title, description, body, with `/docs/...` links made
absolute `.md` links), from the same `content/docs` the site renders
(`apps/web/src/lib/llms.ts`). The machine guide's opening says to read the
page before telling the user a `repose` command, and to compare
`~/.repose/cli-version`. That file is a carry part (marker `cli-version`,
sent when the version changes) written by `run` and `attach`; a test binary
(version "") sends none. guest-conventions.md lists it. The guide is in the
base, so it reaches machines with the next base; the file arrives with the
next CLI release.

**I-413. The tools carry reads Homebrew formulae and installs them from nixpkgs.** (owner,
2026-10-03, "why not done: az") `az` never reached unwrap: the tools
carry (I-221) read npm, pnpm, bun, Go, cargo, uv and pipx, and the
owner's `az` came from Homebrew, so `~/.repose/tools-wanted.json` on
unwrap listed eleven tools and no `az`. The CLI now reads
`<prefix>/Cellar/<formula>/<version>/INSTALL_RECEIPT.json` under
`$HOMEBREW_PREFIX`, else `/opt/homebrew` and `/usr/local` on macOS and
`/home/linuxbrew/.linuxbrew` and `~/.linuxbrew` on Linux, and keeps the
formulae with `installed_on_request` (no dependency travels) that have
commands in their `bin`. Each is an item with `manager: "brew"` and no
`pkg` or `version`: a formula's name and version mean nothing to nix, and
the guest installer already tries nixpkgs by the first command before the
manager, so `az` becomes `nixpkgs#azure-cli` with no guest change, on old
bases too; one nixpkgs lacks fails into the notices as "no nixpkgs package
has bin/X". Brew is read last, so a command npm or Go installed stays
theirs. Casks are apps and are not read. Test: `TestReadGlobalTools`
(asked for, a dependency, one in the base, one npm already has).
*Superseded by I-423 (2026-10-03): the Homebrew reader came out before any
CLI shipped it.*

**I-414. Events page back: `before` and `limit` on the api, Show older on the dashboard, and `repose events` reads the whole window.** (owner,
2026-10-03) The project page fetched the api's newest 50 events and
showed 20, with no way to older ones. `GET /projects/:id/events` takes
`limit` (1..200, default 50) and `before=<event id>`, keyset on `(ts, id)`
so events in the same second page cleanly (`store.ListEventsBefore`; the
`events_project_ts` index serves it). The answer stays a bare array, so
old clients are unchanged. The dashboard polls the newest 50, shows 20,
and Show older shows 20 more, fetching the 50 before the oldest held when
it runs out. `repose events --since` pages back with `before` until the
window is covered (it printed only the newest 50 of a busy day), prints
oldest first, and `--follow` asks for what came after the newest event
printed: it used the last element of a newest-first list, the oldest,
so every poll printed the whole window again. The fake api answered
oldest first, which is why the tests never saw it; it now answers as the
api does. Tests: `TestEventsPageBack` (api, Postgres, ties in one
second), `TestEventsPagesAndFollows` (CLI, 130 events, follow prints a new
event once). The same survey found other capped lists, recorded in
STATUS for the owner to pick from.

**I-418. Claude Code's `idle_prompt` is no event.** (owner, 2026-10-03)
`repose ls` showed `kanali ... claude: needs_input` while `repose
questions` said nothing was waiting. Claude Code sends a `Notification`
with `notification_type: idle_prompt` ("Claude is waiting for your
input") a minute after every turn that ends, and the hook mapping
(guestd's `hooks/mapper.go` and `repose-hook`) counted it as
`needs_input`, as 04-guestd.md said. kanali's events show the pattern
exactly: each `completed` followed 60 s later by `needs_input`. So every
finished turn sent two notifications ("claude finished", then "claude
needs input", which the docs describe as the agent asking you something),
both counting toward the 30 an hour, and a finished agent showed
`needs_input` until its next turn. `idle_prompt` now maps to no event,
typed or classified from its message; `permission_prompt` and
`agent_needs_input` still raise `needs_input`. A finished agent shows
`idle`. Test: `TestMapClaudeIgnoresUnreportableHooks` (typed and untyped
idle fixtures). Reaches machines with the next base.

**I-419. `repose questions` says where it looked, names terminal waits, and asks for one project's list.** (owner,
2026-10-03) From the home directory the owner ran `repose questions`
("No questions are waiting."), then `repose status` ("No repose project
here"), and `repose ls` showed `kanali ... claude: needs_input`. With no
PROJECT, `questions` covers every project and ignores the directory, but
its answer did not say so. It now says "No questions are waiting in any of
your projects." or "No questions are waiting on <slug>.", and lists the
running agents in `needs_input` (a terminal prompt `repose reply` cannot
answer) with `repose attach <slug>`. `--json` is unchanged, the questions
only. With PROJECT it reads `GET /projects/:id/questions?state=pending`
instead of filtering the all-projects list, which stops at 50, so another
project's newer questions no longer hide this one's. Tests:
`TestQuestionsSaysWhereItLooked`, `TestQuestionsForOneProjectPastTheCap`.

**I-420. The destroyed list pages: `before` and `limit`, Show more past the first 100, and the CLI reads every page.** (owner,
2026-10-03) `GET /projects/destroyed` stopped at 100 rows, so the
dashboard's "N of M shown" never counted past 100 and `repose restore
--destroyed` and `repose projects --destroyed` never listed the 101st.
The route takes `limit` (1..200, default 100, so old clients are
unchanged) and `before=<project id>`, keyset on `(destroyed_at, id)`. The
projects page still polls the newest 100; "Show more" past them fetches
the 100 before the oldest held, reads "Show more" and "N shown" while the
api may hold more, and "Show N more" and "N of M shown" once it has sent
its last page (I-333's steps of 20 kept). `Client.ListDestroyed` pages in
200s until a short page, or a page with nothing new, which is what an api
older than I-420 sends. Tests: `TestDestroyedPageBack` (Postgres, 130
destroys with same-second ties, 4 pages of 40), `TestListDestroyedReadsEveryPage`
(230 through the fake), playwright "recently destroyed pages past the
first hundred" (10 -> 130, asks with `before` the 100th id).

**I-421. A window counts as an agent window while an agent is its foreground program, whatever its name.** (owner,
2026-10-03) kanali had claude running in two windows, `claude` and
`shell` (claude typed in the shell window), and `repose ls` showed one.
guestd (`sample.refreshTmux`) took a window as an agent's only by its name
(`AgentOf`: `claude`, `claude-N`, ...), so an agent started by hand
anywhere else was never sampled, never had its state shown, and its
hooks named a window guestd did not know. A window with another name now
counts while its pane's foreground command (`#{pane_current_command}`)
is an agent's program (`AgentByCommand`, nix's `.claude-wrapped` and its
15-byte cut included), and stops counting when the agent exits. Gemini is
left out: its process is `node`, and so is any dev server. The window
keeps its own name, so hooks from it (`window: "shell"`) land on it.
Test: `TestAnAgentInAWindowWithAnotherName` (claude in `shell` counted
and set to needs_input by a hook from `shell`, node in `server` and a
plain shell not, the window dropped when claude exits). Reaches machines
with the next base.

**I-423. The tools carry does not read Homebrew; a curated list is the likely next step.** (owner,
2026-10-03) I-413's Homebrew reader came out. The owner's `az` came from
pacman on an Arch laptop (installed with Octopi), so I-413 would not have
carried it, and a Mac with years of `brew install` would queue dozens of
formulae for background install on every fresh machine. The reader,
its test rows and its docs are gone; the carry reads npm, pnpm, bun, Go,
cargo, uv and pipx as before I-413. No CLI shipped I-413: main had it from
r20261003-1, but the conductor held that release's CLI tag, so the next
tag carries this removal. The guest installer never needed a change, so
nothing on a base changes. The options for system packages and Homebrew,
with the owner leaning towards a short curated list of development CLIs
found on the laptop's PATH, are in
`docs/proposals/2026-10-03-tools-from-system-packages.md`. Until one is
picked, the I-410 hint names the nixpkgs package and `repose config add`.

**I-424. Main is integrated often and released when the owner asks.**
(owner, 2026-10-03: "we can merge stuff into main and hold on until we
have a bit then release") I-416 had one step: a cut merged the queue,
moved main, pushed and shipped. That gave two releases on 2026-10-03,
and the owner stopped a third ("cant the release queue fill up a
little"). But holding branches in the queue leaves agents building on a
main that lacks each other's work. Now the conductor merges verified
batches into main on this machine without pushing (`release-queue done`
marks them `on-main`), and `ls` reports what main holds unreleased and
what it ships as. A release pushes main, publishes the base and tags the
CLI, when the owner asks. Coolify deploys GitHub's main, so nobody pushes
main between releases, and a fix for production goes out with everything
integrated before it. GitHub CI runs only on the push, so the batch
verification is the gate until then. *Rejected:* deploying from a
release branch or a tag (a Coolify and infra change, owner-side, for the
same effect); keeping branches queued until a release (agents keep
merging each other's work late, and conflicts grow).
**I-425. Claude Code in a guest starts with the fullscreen renderer unless the user chose one.** (owner,
2026-10-03) On a new temporary machine the owner's first `claude` drew
inline in the tmux pane: the prompt under the shell's output and the
scrollback mixed with tmux's, while older machines drew fullscreen.
Claude Code 2.1.283 picks its renderer from `CLAUDE_CODE_NO_FLICKER`,
then settings.json's `tui`, then a server-side flag
(`tengu_pewter_brook`) read from the cache in `~/.claude.json` at start
and held for the process. A new machine's `~/.claude.json` is the one
`repose-agent-setup` writes, with no flag cache, so the first start read
the flag as false; older machines had it cached true. Neither laptop
route reaches the guest: the carry drops settings.json's `env` (I-211)
and never reads `~/.claude.json` (I-196). `/etc/repose/claude-settings.json`
now carries `tui: "fullscreen"`, and `repose-agent-setup` adds it where
the user's file has no `tui`, so the user's `"default"` (set in the guest,
with `/tui default`, or carried from the laptop, which the merge puts on
top) is kept. Checked on kanali: `claude --settings '{"tui":"fullscreen"}'`
in a tmux pane gives `#{alternate_on}` 1, `"default"` gives 0. Test: the
guest-base VM test asserts the key in the fresh, existing and `plan`
files and keeps a user's `"default"`. Reaches machines with the next
base; on an older base, `/tui fullscreen` sets it.

**I-426. The guest's Codex ships with its code-mode host.** (2026-10-03)
Codex 0.157.1 on every guest failed each shell command with "failed to
spawn code-mode host .../codex-0.157.1/bin/codex-code-mode-host: No such
file or directory", so a Codex turn could read and answer but not run
`ls`. Found while testing T3 Code against a guest; plain `codex` in tmux,
`codex exec` and the Codex app over SSH hit the same wall, and
`-c features.code_mode=false` does not get round it. Codex runs commands
through `codex-code-mode-host`, which upstream ships as its own release
asset and looks for next to its executable; `codex.nix` fetched only the
`codex` binary. The package now fetches the host asset too, pinned in
`versions.json` under `codex."code-mode-host"`, and installs it beside
`codex` (the binary wrapper keeps `.codex-wrapped` in the same `bin`).
`scripts/bump-agents.sh` moves the host with the binary and checks it is
in the built package. The full `codex-package` tarball (148 MB, with
bwrap, rg and a voice host) was not taken: the base already supplies
bubblewrap and ripgrep. Checked: the built package ran `echo` on kanali,
and on a temporary guest with the package imported, a T3 Code Codex
thread ran `cat hello.txt && date +%Y` and replied with the file's word
and the year. Reaches machines with the next base.

**I-427. The web server bundles its packages; an unknown docs page is a 404; a docs page can be experimental.**
(owner, 2026-10-03: "mark it as experimental, add 404 thing") On the live
site `/docs/<unknown slug>` answered 500: the web container's log said
`Cannot find package 'marked'`. The runtime image (apps/web/Dockerfile)
holds `build/` and no `node_modules`, and adapter-node leaves the
packages in `dependencies` (marked, @logto/browser, animejs,
svelte-sonner, the nix highlighter) external to the server bundle. Every
known page is prerendered, so only an on-request render met it. Vite now
bundles every package (`ssr.noExternal: true`), so the image needs no
`node_modules` and a package added later cannot fail the same way.
Reproduced and checked with `build/` copied to a directory without
`node_modules`: before, 500 and ERR_MODULE_NOT_FOUND; after, 404.

An unknown slug was also a soft 404 (200 with "No such page"). The docs
page's `load` now throws 404 for a slug that is no page and no moved page,
and `docs/+error.svelte` shows the same text, so a stale link reads as
missing to people and to monitors. Test: docs.spec.ts "an unknown docs
page says so, with a 404".

A docs page with `status: experimental` in its frontmatter shows a warning
banner under its title (the site's `banner--warn`), and its markdown
(`/docs/<slug>.md`, `/llms.txt` readers) carries the same line. The T3 Code
tutorial is the first: it is new, and T3 Code itself is a 0.0.x alpha.
Test: docs.spec.ts "an experimental page says so under its title".
Checked at 1440 and 390, light and dark. *Rejected:* moving the five
packages to devDependencies (adapter-node bundles those; it fixes today's
five and not the next one added to dependencies).

**I-428. Agent bumps run downloaded binaries in a job with no write access.**
The daily bump-agents workflow builds each new agent release and runs
`<agent> --version` to prove it starts. That binary is upstream code that
nobody has reviewed yet, and it ran in the same job that held a token with
`contents: write` in its environment and in `.git/config`. The workflow is
now two jobs. `build` has `contents: read`, checks out with
`persist-credentials: false`, and runs `scripts/bump-agents.sh`, which no
longer commits or pushes and starts each agent under `env -i` with only a
scratch `HOME` and `PATH`. It uploads the rewritten `versions.json` as an
artifact. `pr` holds `contents: write` and `pull-requests: write`, runs no
Nix and nothing `build` downloaded, and hands the artifact to
`scripts/bump-agents-pr.sh`. That script accepts the file only when it has
the same entries as the committed one and only values changed: version
strings, `https` URLs on downloads.claude.ai, github.com or
registry.npmjs.org, and `sha256-` SRI hashes. It then commits it on a
`bump/agents-<date>` branch and opens the PR, which a person reviews before
it reaches `main`. `scripts/bump-agents.sh --pr` is gone and says where it
moved. Tests: `test/supplychain` (`TestBumpAgentsRunsDownloadsWithoutWriteAccess`,
`TestBumpAgentsPRAcceptsOnlyValueChanges`). *Not done here:* checking
upstream provenance (npm provenance, GitHub attestations) before recording
a hash; the hash is still whatever upstream served at bump time.
`main` deploys api, api-grpc and web on every push (I-112), so its
repository ruleset, an owner setting, is part of this boundary.

**I-429. CI pins every action to a commit and every tool to a version.**
A tag such as `actions/checkout@v4` can be moved by whoever controls that
repository, and `go install ...@latest` or GoReleaser `~> v2` run whatever
was released last. In jobs that hold a write token or a signing key that is
code from outside the repository running with our credentials. Every
`uses:` in `.github/workflows/` is now `owner/repo@<40-hex commit> # vX.Y.Z`;
`.github/dependabot.yml` proposes new commits weekly as PRs. GoReleaser is
`v2.18.2`, golangci-lint `v2.14.0`, buf `1.73.0`, Lighthouse `13.5.0`, and
the Cachix CLI comes from the flake's locked nixpkgs instead of the
registry. Workflow-level permissions are read-only or empty everywhere;
`infra.yml`'s `id-token: write` moved to the `plan` job, the only one that
exchanges it for Azure credentials. Tests: `test/supplychain`
(`TestActionsArePinnedByCommit`, `TestToolsHaveExactVersions`,
`TestNoWorkflowGrantsWriteToEveryJob`). The repository's Actions
setting that requires full-length commit SHAs is the owner's, and keeps a
new workflow from skipping the pin.

**I-430. CLI releases sign checksums.txt; install.sh refuses a release it cannot verify.**
install.sh checked the archive against `checksums.txt` from the same
release, which guards against a broken download and nothing more: whoever
can upload one file can upload both. A release now goes through two jobs.
GoReleaser uploads to a draft (`release.draft: true`), which is never
`releases/latest`. The `sign` job, in the `release` environment that holds
`REPOSE_RELEASE_SIGNING_KEY` (an ECDSA P-256 private key, PEM), downloads
the draft's assets, checks the four archives against `checksums.txt`,
signs it with `openssl dgst -sha256 -sign` into `checksums.txt.sig`,
verifies that signature against the public key install.sh embeds,
attests the archives' build provenance (`actions/attest-build-provenance`,
so `gh attestation verify` works too), uploads the signature and publishes
the draft. A missing secret fails the job and the draft stays unpublished.

install.sh embeds the public key. For a release it does not pin, it needs
`openssl`, downloads `checksums.txt.sig` and refuses to install when the
file is missing or does not verify. Releases v0.1.0 to v0.1.27, cut before
signing, have no signature; install.sh pins the SHA-256 of each one's
`checksums.txt` as published and refuses one that differs, so
`--version v0.1.11` keeps working and stays checked. P-256 with `openssl
dgst` was chosen because the LibreSSL in macOS and the OpenSSL in Linux
distributions both verify it; the LibreSSL 3.3 that macOS ships has no
Ed25519 in its command line tool, and minisign or cosign would be a tool the user has to install first. For
tests, `REPOSE_INSTALL_PUBKEY` replaces the key, and only when
`REPOSE_INSTALL_BASE_URL` is set. Tests: `test/supplychain`
(`TestInstallShRequiresASignedChecksumsFile`,
`TestInstallShPinsReleasesBeforeSigning`,
`TestReleaseIsSignedBeforePublishing`) and ci.yml's `cli-install` job,
which signs its fake release and checks the refusals on Linux and macOS.
Rotating the key: `docs/ops/RELEASE.md` "The release signing key".
*Limits:* install.sh and the key are served from the site that `main`
deploys, so this stops a forged release, not a forged `main` (I-428's
ruleset covers that). GoReleaser and the signer share a workflow run; a
compromised GoReleaser could still change the archives before they are
signed (I-429's pin narrows that). The `release`
environment and its secret are owner setup (`docs/ops/RELEASE.md` "The
release signing key").
**I-431. The api's `/internal` listener admits only the gateway's certificate.** (security release, 2026-10-03)
One X.509 CA signs the hosts' client certificates (CN = host id) and the
gateway's (CN `gateway`), and the `/internal` listener on 8444 accepted any
client certificate that CA had signed. A host's certificate therefore worked
as the gateway's, and `/internal` includes routes that only the gateway may
call, among them the one that issues short-lived user certificates for a
project. The listener's TLS configuration now requires a verified client
certificate whose CN is exactly `gateway` (`pki.RequireClientName`,
`pki.GatewayClientName`); any other identity, a host's included, fails the
TLS handshake, so no `/internal` handler runs for it. Test:
`internal/api/http` `TestInternalAdmitsOnlyTheGateway` (a host's
certificate, none, and two near-miss names fail on every `/internal` route;
the gateway's reaches `/internal/ca`).

The change is immediate, with no release accepting the old shape: the old
shape is "any certificate from the host CA", and accepting it for a release
keeps the hole open for that release. The edge needs nothing new. Its
certificate was always made with `repose-admin ca sign-client --name
gateway` (RUNBOOK "Edge", step 4, I-92), and `sign-client` sets the CN from
`--name` whatever the CSR says. An edge whose `gateway.crt` carries another
CN loses `/internal` (route lookups, revocations, wgsync) until it is
signed again with `--name gateway`; check with `openssl x509 -noout
-subject -in /var/lib/repose/edge/gateway.crt` before the api deploys.
*Rejected:* a separate CA for the gateway (a new key to hold in the
platform secrets, a new `api-ca.pem` on the edge and an edge switch, for no
more than the CN check gives while only an operator can sign a client
certificate with a chosen name); a per-handler check (a route added later
without it would reopen the hole).

**I-432. A host's mTLS identity ends when the host is lost or retired, and only its latest certificate counts.** (security release, 2026-10-03)
`Rotate` and `Session` checked only that the CN named an existing host
row, so a machine marked `lost` or `retired` kept a working identity and
could rotate itself a fresh certificate before each one expired;
`hosts.cert_serial` was written and never compared. Now `Rotate`, `Session`
and every heartbeat on an open stream run one check (`admit` in
`internal/api/hostmgr`): a host in state `lost` or `retired` is refused,
and the presented certificate's serial must be the row's `cert_serial` or
its new `prev_cert_serial` (migration 0012). `Rotate` moves the presented
serial to `prev_cert_serial` and records the new one, guarded by the state
in the same `UPDATE`; the first stream opened with the new certificate
clears `prev_cert_serial`. The window exists because hostd writes the new
files after `Rotate` answers; a host whose write failed keeps working on
the old certificate and rotates again. Refusals are `PermissionDenied`;
an open stream ends at its next heartbeat. `hosts mark-lost` and `hosts
retire` clear both serials, `hosts rotate-cert` keeps the old serial as
the previous one (the operator installs the new files afterwards) and
refuses a lost or retired host, and `Register` clears `prev_cert_serial`.
A re-imaged machine joins again through `hosts add --name <n> --reissue`.
Tests: `internal/api/hostmgr` `TestHostCertificateSerialIsEnforced`,
`TestLostAndRetiredHostsAreRefused` (lost and retired, an open stream
included); `internal/admin` `TestAdminSurface` checks mark-lost clears
the serials and rotate-cert refuses. Hosts registered or rotated through
the api always had their serial recorded, so a running host is admitted
after the deploy; one that is not shows `PermissionDenied` in hostd's log
and `hosts rotate-cert` recovers it. *Rejected:* a CRL for the host CA
(every verifier would need it fetched and fresh, where the api already
holds the row it needs).

**I-433. A named secret's ciphertext is being bound to its project as well as its name, over two releases.** (security release, 2026-10-03)
AES-GCM's additional data was the name alone, and every row carries its
own wrapped data key, so a row copied by a database write into another
project under the same name decrypted there and reached that project's
guest; that included the platform's CA keys under the platform
pseudo-project. The project-bound form's additional data is
`repose-secret-v2`, a zero byte, the project id, a zero byte and the name
(`secrets.SealBound`; `secrets.Seal` and `secrets.Open` take the project
id).

An api image from before this decision cannot open the bound form. Coolify
deploys by starting the new container before stopping the old one, and an
image rollback runs an older one too, so writing the bound form in the same
release that teaches the api to read it would break secrets for the old
container and for any rollback past this release. The change is therefore
split.

This release: `Open` accepts both forms, and `Seal` still writes the
name-only form (the `sealBound` constant in `internal/api/secrets` is
off). `Store.Reseal`, which rewrites every name-only row in the bound form
for the project it sits in, exists and is tested but nothing runs it. A
name-only row outside the platform pseudo-project whose data key is one of
the platform's is refused on read (`ErrPlatformKey`), and `Reseal` leaves
such a row alone, so platform CA material copied into a project does not
decrypt there. `repose fork` no longer copies ciphertext in SQL:
`Store.CopyNamed` opens each named secret and seals it for the new project
in the fork's transaction under the same wrapped key, so a bound row stays
bound to its own project; a fork now depends on Key Vault, as `secrets set`
always did. What is not bound in this release: a user's name-only row
copied into another project under the same name still decrypts there.

Next release, once no image older than this one can be deployed: turn on
`sealBound`, run `Store.Reseal` at api start (retrying while Key Vault is
unreachable; it is idempotent and its `UPDATE` matches the old
ciphertext), and in the release after that drop the name-only read path.
Tests: `internal/api/secrets` `TestRoundTripAndAAD` (the bound form does
not open under another project or name), `TestCiphertextIsBoundToItsProject`
(this release's boundary, both what is bound and what is not),
`TestResealLegacyRows` (`Put` writes what an older image opens; `Reseal`
binds it and refuses the platform-key copy); `internal/api/http` `TestFork`
opens the copied secret in the fork. *Rejected:* a column recording the
additional-data version (a database writer sets it as easily as the
ciphertext; a failed tag check under the bound data says the same thing for
free); binding in one release (breaks the rolling deploy and rollback).
**I-441. Guests report guest kinds only; platform kinds come from the api.**
(security release, 2026-10-04) `notifyKinds` held the guest kinds and the
platform kinds in one map, and both guest paths (an `AgentEvent` over
vsock, and the edge's `POST /hooks` forwarded to `/internal/events`)
accepted any kind in it, with the agent name passed through as given. A
tenant's own code could therefore make the api send its owner mail with a
platform subject and template, and `notifications_paused`, which skips the
dedupe and the hourly cap, could be reached from a guest. Now
`events.GuestKinds` (`completed`, `needs_input`, `error`, `agent_message`)
is the set a guest may report: `FromEdge` refuses any other kind with
`400 invalid`, a vsock `AgentEvent` with any other kind is stored as
`error` (as an unknown kind already was), and an agent name outside
guestd's five and `shell` is stored as no agent on both paths and on
questions. The edge hook ingest refuses the same kinds and agents before
forwarding. Platform producers keep the full set through `Platform` and
`Insert`. No transition window: no guestd or wrapper ever sent a
platform kind, so the old shape had no legitimate sender.
Tests: `TestGuestCannotSendPlatformKinds`,
`TestHookIngestBodyCapAndValidation`. docs/SECURITY.md boundary 9.

**I-442. The unsubscribe link confirms before it acts, and expires.**
(security release, 2026-10-04; amends I-49's non-expiring token) A GET of
`/v1/notify/unsubscribe` turned email off, so a mail scanner or link
preview that fetched the link did it for the user, and the token (an
HMAC of the user id) worked forever. GET now shows a page whose button
POSTs; `POST /v1/notify/unsubscribe` acts, and is also the target of the
RFC 8058 one-click headers (`List-Unsubscribe`, `List-Unsubscribe-Post`)
every agent email now carries. New tokens sign the user id and an expiry
90 days after the send (`notify.UnsubTTL`), under their own MAC domain;
an expired one gets `410`. The first shape (id alone) still verifies
until the release after next, so links in mail already sent keep
working, and they too now act only on POST. *Rejected:* a nonce stored
per link (a table for a link the dashboard setting makes redundant).

**I-443. An unknown JWT key id fetches the JWKS at most once per 30 seconds.**
(security release, 2026-10-04) The verifier refetched Logto's JWKS for
every token whose `kid` it had not cached, and the key lookup runs before
the signature is checked, so any request with a fresh `kid` made the api
fetch. Fetches now go one at a time and at most once per
`auth.MinRefreshInterval` (30 s) after the last attempt, successful or
not; inside it an unknown kid is `invalid token` with no fetch, and a
failed fetch keeps serving cached keys up to 24 h as before. A key Logto
rotates in is picked up at most 30 s after the previous fetch. The fetch
runs detached from the request's context so one client giving up does
not fail the requests queued behind it. Test:
`TestUnknownKidDoesNotRefetchEachRequest`.

**I-444. The ntfy sender reaches public addresses only and follows no redirect.**
(security release, 2026-10-04) `ntfy_url` is the user's, and the api
POSTed to it with a default client, from inside the platform's network.
The default ntfy client (`notify.PublicClient`) checks the address it is
about to dial, after resolution, and refuses loopback, private (the
`10.255.0.0/16` mesh and the VNet included), link-local (IMDS),
multicast, unspecified, CGNAT, other reserved ranges and the Azure wire
server; it ignores proxy environment variables (the check would see the
proxy) and refuses every redirect. Both refusals are permanent outbox
failures. `PATCH /me` refuses an `ntfy_url` whose host is a literal such
address or `localhost` with `400 invalid` ("ntfy_url must point at a
public address"), so the mistake shows on save; a name is checked only
when dialled. A self-hosted ntfy on a private network was never
reachable from the api and is now refused plainly. Test:
`TestNtfyDefaultClientRefusesNonPublic`, `TestPatchMeNtfyNullClears`.
docs/SECURITY.md boundary 10.
**I-439. Tenant builds on a host reach the public internet only.**
(security review, 2026-10-03) hostd evaluates a project's fragment as the
build user and nix-daemon builds it as `nixbld` users. Nix gives a
fixed-output derivation network access by running its builder in the
host's network namespace; every other derivation gets a namespace with
only `lo` (checked on a repose guest with Nix 2.34.8). The host's nftables
filtered forwarded guest traffic only and its `output` chain accepted
everything, so builds were not held to the network boundary guests are.

The `inet repose` `output` chain now sends sockets whose group is `nixbld`
(`config.ids.gids.nixbld`, the group nix-daemon runs every builder under)
and sockets of the build user (`repose.host.buildUser`, which runs the
fragment's eval and so its fetches) to a new chain `build_out`. It accepts
DNS to the resolved stub at 127.0.0.53 (the sandbox copies the host's
resolv.conf, which names the stub; resolved forwards as its own user),
then drops anything leaving on `lo`, IMDS, the wire server, `0/8`, `10/8`,
`100.64/10`, `127/8`, `169.254/16`, `172.16/12`, `192.168/16`, multicast
and reserved, the IPv6 loopback, ULA, link-local and multicast ranges, and
anything not leaving on the provider NIC. Root, hostd, the daemon's own
substitution and operators are untouched. Ordinary derivations need no
rule (their namespace has no route out), and eval-time fetches run as the
build user and are covered by the same match. An assertion refuses
`nix.settings.auto-allocate-uids`, under which builders would not carry
the `nixbld` group. NixOS checks the ruleset in a sandbox without the
build user, so `preCheckRuleset` swaps its name for `nobody` there.

Checked: on a repose guest the same two chains (the build user stood in
by `nobody`) turned the fixed-output builder's loopback and VNet requests
from reached to dropped, kept https to cache.nixos.org and DNS working,
and left root and `dev` reaching the same listener; the host-network VM
test gains the subtest; host-01's toplevel evaluates and its ruleset
passes `nft --check`. *Rejected:* moving nix-daemon into its own network
namespace now (its substitution, hostd's store queries and operators'
copies all go through it, and it needs a veth, NAT and the same drops
anyway), and an exception for Azure DNS at 168.63.129.16 (builders resolve
through the stub, so they never need it).

**I-440. Build log redaction matches multi-line and encoded values, and covers a failed build's error.**
(security review, 2026-10-03) The api redacted a project's secret values
from build log lines by whole-value substring, one line at a time, so a
value of several lines (a PEM key) never matched and its lines were stored
as written; its base64 form passed too, the error a failed build stores
(with the builder's log tail) was not scanned, and the fragment check
looked for the whole value only. `buildlog.Needles` now gives, per value of
4 bytes or more: the value, its standard and URL-safe base64, and each
trimmed line of a multi-line value except PEM armour lines, longest first
so a whole value is replaced before a line of it. The log store, the
failed-op error (`ops.error` and the project's `last_error`) and the
fragment refusal all use it. Values under 4 bytes stay unmatched and
docs/features/secrets.md and the user docs now say so. Tests:
`TestRedactionOfMultiLineAndEncodedValues`, and
`TestRestoreOntoHostAndSecretValueInFragmentRefused` extended (one line of
a key in a fragment refused; a failed build's message stored redacted);
both new checks fail on the old code.
**I-445. Guest notifications are bounded and rate-limited at hostd, and the api bounds them again.**
(security release, 2026-10-03) Root in a guest can replace guestd, so a
`Notify` is tenant-written. Before this, hostd forwarded a warning's kind
and detail as sent, an agent event's agent, kind and window unbounded, and
any number of them; the api used the warning kind as a Prometheus label
value, logged the detail as sent, and stored every agent event. hostd now
keeps the fixed sets of `docs/interfaces/grpc-hostd.md` ("Guest-raised
events"): agent event kinds `completed|needs_input|error|agent_message`
(another is dropped), agent names as `[a-z0-9_-]{1,32}` or `unknown`,
window names 64 bytes, summary and text 1 KB of clean UTF-8, question ids
that parse as uuids and states `""|cancelled|expired`, three options of
64 bytes, warning kinds from guestd's list or `guest_other`, and a warning
detail hostd writes from the numbers and process name the known kinds
carry. A token bucket per guest passes 30 at once and one every 2 s
(an agent turn is one or two notifications; guestd sends a warning kind at
most once in 10 minutes), counted in
`repose_host_guest_notify_dropped_total{reason}`. Guest-raised events wait
for their ack in their own list (2,000; the host's own keep 10,000) and
are sent from it without blocking `Event` or `Result`, so a flood cannot
evict a state change or delay a command result. The api accepts the old
shape for one release and applies the same bounds: an unknown agent event
kind is stored as `error` (a platform kind such as `billing_stopped` is no
longer accepted from a guest), text is cleaned of NUL and other control
characters before insert (a NUL made an insert fail, which left the event
unacked and resent on every reconnect), a warning kind outside the list
counts as `other`, the logged detail is one line of 256 bytes, and a
project stores at most 600 guest-raised events an hour, counted over
vsock and the edge's hook path together; past it an edge event is dropped
like a vsock one (202 with a nil event id)
(`repose_api_host_reports_refused_total{reason="project_cap"}`).
Tests: guestinput_test.go (hostd), TestGuestEventsCannotEvictOrBlockHostEvents
(stream), TestGuestKindsTextAndCaps and TestHostWarningKindsAreAFixedSet
(api events), TestEdgeHookPathSharesTheGuestCap. *Rejected:* a retention job deleting old events (events are
the history `repose events` pages through, I-414; the hourly cap bounds
growth, and how long to keep history is a product decision); a fixed list
of agent names at hostd (a new agent would need a host release first).

**I-446. Each guest's sample rows are stored apart, and the guest's part of a sample is cleaned.**
(security release, 2026-10-03) The api queued every guest's
`meter_samples` and `proc_samples` rows of one Samples message in one
`pgx.Batch`, which runs as one transaction: one row Postgres refused (a
NUL in a process name or an agent name, which a guest controls) lost the
minute for every guest on the host, and the rollup then saw a gap with no
running time and no egress for each of them. hostd now caps and cleans the
guest-reported part of a sample (32 agents, 128 processes, `comm` 16
bytes without control characters, agent state from guestd's four), the
api cleans the same fields again, and each guest's rows go in a batch of
their own. When a guest's batch fails, the api stores its host-measured
fields alone (state, class, CPU, memory, network, disk), so running time
and egress do not depend on what the guest reported; a failure is counted
in `repose_api_samples_failed_total{reason}` (`guest_fields` or `insert`)
and alerted on (`SamplesFailing`, ops/alerts.yaml). Test:
TestOneGuestsSampleCannotSinkTheHostsBatch (api meter) and
TestSampleGuestFieldsAreCleaned (hostd). *Rejected:* savepoints inside
one transaction (a batch per guest is the same isolation with less code,
and a sample runs once a minute per host).

**I-447. A host's reports count only for its own guests; a question id acts only inside the sending guest's project.**
(security release, 2026-10-03) Commands and results were already scoped
to the stream's host; events, `Hello` and samples were resolved by guest
id alone. The api now takes a guest state change, agent event, question,
snapshot_done, `Hello` entry, sample or miner stop from a host only when
the guest's project is placed on that host (`store.GetProjectOnHost`), and
counts the rest in `repose_api_host_reports_refused_total{reason="foreign_guest"}`.
A project whose placement was released (a capacity failure sets
`host_id` null) takes no host reports until it is placed again, which is
the state it is in. A `snapshot_done` blob path must sit under the
project's own prefix (`<user_id>/<project_id>/` or `<project_id>/`), and a
snapshot time in the future is clamped to now. A guest-side question close
updates only a question of the sending guest's project, and an id that
another project already holds is ignored rather than announced or read
back. Tests: TestHostReportsCountOnlyForItsOwnGuests (api events),
TestHelloCountsOnlyForTheHostsOwnGuests (api ops),
TestQuestionCloseAndReuseStayInTheSendersProject (api questions).
**I-434. The gateway remembers a revoked serial for the full user certificate lifetime, and refuses certificates that would outlive that memory.**
(security release, 2026-10-03) The gateway's in-memory revocation set
dropped a serial 13 hours after first seeing it, a window sized for the
12-hour certificates of R3-9. I-267 raised the lifetime to 24 hours and
the window stayed, so on a gateway that had been up long enough a
certificate revoked early in its life became usable again for the rest
of it. The window is now `sshca.UserCertTTL` plus one hour of clock skew,
derived from the constant so the two cannot drift apart again, and the
gateway refuses any user certificate whose validity span is longer than
that window, or that never expires (`permission denied (certificate not
signed by the repose CA)`, result `bad_ca`). The api issues
`UserCertTTL` plus one minute of backdating, so nothing it signs is
refused. A restart still re-reads the whole list. Tests:
`TestRevocationKeptForTheCertLifetime` (a revoked 24-hour certificate is
still refused 6, 13, 18 and 23 hours later across incremental
refreshes), `TestCertLongerThanRevocationMemoryRefused`. *Rejected for
now:* carrying `expires_at` in `/internal/revoked` so each serial is
dropped at its own expiry; it changes an internal contract for a memory
saving of a few hours of serials.

**I-435. The gateway bounds unauthenticated connections separately from relays.**
(security release, 2026-10-03) Before, a connection took one of the 200
relay slots before the per-source check, and held it through the
handshake for up to 30 seconds whether or not it ever authenticated, so
idle connections from one address could hold every slot. Now:

- The per-source check (4 connections in the handshake per address, an
  IPv6 source counted by its /64) and the ban run first, then a global
  budget of 512 connections in the handshake. A connection over either is
  sent one plain line (`repose gateway: too many authentication attempts
  from your address; try again later` or `repose gateway: gateway busy`)
  ahead of any SSH version string and closed, with no key exchange.
  OpenSSH shows that line only with `-v`; the user sees
  `kex_exchange_identification: Connection closed by remote host`.
- The handshake and authentication must finish within 10 seconds (was
  30).
- A relay slot (200) is taken only once the handshake has finished, once
  per connection, with a per-user share of 32 relays keyed on the user id
  in the `key_id` of the certificate that signed. ssh asks the gateway
  about each key a client offers before any signature, so the gateway
  keeps what it decided per certificate and uses only the one that
  signed. With no slot, every session the client opens gets `gateway
  busy` or `too many open connections for your account; close some and
  try again` on stderr with exit status 255, result `busy`; the CLI counts
  both among the refusals a new certificate cannot fix.
- The edge's nftables admits at most 64 open connections and 20 new ones
  a second (burst 40) per source address on tcp/22.

These replace the limits line of `06-gateway-edge.md` §5.2. Tests:
`TestOneSourceCannotFillTheGateway` (400 idle connections from one
address hold at most 4 handshake slots and no relay slot; a client from
a second address connects and runs a command), `TestPreAuthGlobalCap`,
`TestAuthDeadline`, `TestPerUserRelayCap`,
`TestKeyQueriesTakeNoRelaySlot`,
`TestConnectionCapAndPerSourceAuthLimit`; the edge ruleset passes
`nft -c`.

**I-436. A relay ends when its certificate is revoked or expires.**
(security release, 2026-10-03) Revocation and expiry were checked only at
authentication, and the CLI's ssh config multiplexes every later command
over the first connection (`ControlMaster`), so a connection opened
before `repose logout` on another device, or before the certificate
expired, kept opening new sessions until the 24-hour connection cap. The
gateway now keeps its open relays by certificate serial: every
revocation refresh and push ends the relays whose serial is revoked, and
each relay lasts at most until its certificate's `valid_before` (and
never past the 24-hour cap). Ending a relay closes the guest connection
first, then the client's. `session_close` log lines carry `reason`
(`revoked`, `cert_expired`, `session_cap`, `closed`).

So that a connection the CLI opens is not cut short soon after it
starts, the CLI now reuses a certificate on disk only while it has 12
hours left (was 30 minutes), and certificates are issued about twice a
day instead of once. A command multiplexed over a `ControlMaster` would
still ride the certificate that master logged in with, so after issuing
a certificate the CLI runs `ssh -O stop` for each project's alias when a
control socket exists: the old master takes no new sessions (those on it
carry on) and the next command starts a master with the new
certificate. A command therefore keeps its connection for at least 12
hours. Checked with OpenSSH 10.5 against a local sshd: after `-O stop`
the socket is gone, a running session finishes, and the next ssh is a
new master. Test: `TestEnsureCertReissuesWhenProjectAdded` (both
aliases stopped), `TestEnsureCertReusesValidCertificate` (none on
reuse). Editors
over Remote-SSH reconnect on their own, and the reconnect runs
`repose ssh-prepare`, which renews the certificate. Tests:
`TestRevocationEndsOpenRelays` (push and api refresh each end their
relay; a relay on another certificate keeps running),
`TestCertExpiryEndsOpenRelay`, `TestCertUsableFor`. Old CLIs keep the
30-minute margin and no stop, so their connections can be cut sooner.

**I-437. The gateway answers a login under another user's handle the same way whether or not the project exists.**
(security release, 2026-10-03) The gateway looked up the route for
whatever `<slug>.<handle>` a client sent before checking the
certificate's principals, and answered `no such project` for a missing
project and `certificate not valid for this project` for one that
exists. Any user with a valid certificate could learn which project
slugs another user has. The gateway now compares the login's handle with
the handle the api signed into the certificate's `key_id`
(`<user_id>:<handle>`) right after the revocation check: a different
handle, or a `key_id` that is not of that shape (the gateway's own
`:via-gateway` certificates of I-431 included), gets `certificate not
valid for this project` with result `wrong_principal` and no route
lookup. Within your own handle `no such project` is unchanged. After an
operator renames a handle, the next connection with the old certificate
gets the same banner, and the CLI's one re-issue on it brings a
certificate with the new handle. This is SECURITY.md control 5
("cross-user lookups return 404") applied to the gateway. Tests:
`TestOtherUsersLoginsAnswerAlike`, `TestGatewayCertFromClientRefused`,
`TestCertUser`.

**I-438. The preview host parser no longer splits a slug from a handle.**
(security release, 2026-10-03; amends I-6) `<port>-<slug>-<handle>` has
more than one reading when either part contains a dash, and both may:
`3000-todo-app-hera-craft` is `todo-app` of `hera-craft` and
`todo-app-hera` of `craft`. A proxy built on that split could serve one
user's project at another user's preview name. The stub's `Route` now
returns the port and the label after it as one opaque string, and the
host form is `<port>-<name>.repose.herakraft.co`. When previews are
built, `<name>` must be a per-project preview name the api keeps unique;
how it is chosen is decided with the feature. Previews are not built, so
nothing a user sees changes. Tests: `TestRoute`,
`TestRouteNeverSplitsSlugAndHandle`.
**I-448. Egress and CPU are metered from a guest's boot to its stop.**
(security review, 2026-10-03) A guest's sample cursor (its last reading of
the tap's bytes and the unit's CPU time) outlived a stop, while the tap and
the unit it read did not: the first sample after a start compared a fresh
counter with the old cursor and counted nothing, and teardown deleted the
tap without reading it, so bytes before a guest's first sample and after
its last went unmetered. These counts are the egress the plan's allowance,
the overage charge and the 4x stop are measured against.
- The cursor starts at zero when boot creates the tap, so the first tick
  counts from boot.
- `teardown` reads the tap once more after the hypervisor has stopped and
  before deleting it, and sends one `Samples` with that guest's last rx and
  tx deltas. Every path that ends a guest (stop, destroy, a failed boot, an
  unexpected exit, reconcile) goes through it. Its state is `stopping`, so
  the api's hours, which count `running` samples, do not gain a minute.
- A reading below the cursor is a counter that started again from zero
  (a tap or a unit recreated) and counts in full instead of becoming a new
  baseline.
- The tick and the final reading read the tap and move the cursor under one
  lock, so the same bytes are never counted by both.
- A `Samples` timestamp is never at or before the previous one: the api
  keys samples on (project, second) and keeps the first row for a second,
  so a final reading in the same second as a tick would have been lost.
- Destroy no longer sends a final sample from the nft egress counter: its
  bytes since the last tick are the bytes the stop's tap reading already
  sent. The counter stays, as host-conventions.md describes it.
Not changed: a hostd restart under a running guest still loses the bytes
since the last tick before the restart (the cursor is in memory), and
hours still count 60 s per running sample, so a run shorter than a tick
bills its egress but no running time. Tests:
`internal/hostd/guest/metering_test.go`.

**I-449. The pool's thin volumes together are at most 1.5 times the
pool.** (security review, 2026-10-03) Placement and
resize checked a volume against the pool's free space and the plan's disk,
never against what the host's volumes could grow to, and a full thin pool
fails the writes of every volume in it. hostd now refuses, with
`insufficient_capacity`, a create, a restore or a resize after which the
virtual sizes of every thin volume in the pool, guests and the caches'
`repose-cache` volume (not `snap-*`, which share their origin's blocks for
the length of an upload), would pass `PoolOvercommit` (1.5) times the
pool.
1.5 is the ratio the design already sells: 30 seats on a 256 GB host at
100 GB of plan disk each is 3 TB on DESIGN §4's 2 TB pool. Volumes already
past the budget keep running and starting; they cannot grow. The api
shows the refusal as it shows any capacity refusal ("the host has no room
for this project right now").
- *Rejected by the owner (2026-10-03): a bound on one volume* (at most half
  the pool, so filling it would take more than one tenant). Plans sell up
  to 500 GB of disk per account and one project may hold all of it; a
  per-volume cap below that breaks what is sold, and on host-01's 476 GiB
  pool it would have been about 238 GiB. The cost of the choice: one
  tenant can still fill the pool alone, up to their plan's disk. The
  remedy is a larger data disk later, which also raises the budget.
Not done
here: the api's scheduler still places by free space alone and learns of
the budget only from hostd's refusal, and nothing yet acts at 90 percent
beyond refusing creates. Tests: `internal/hostd/guest/poolbudget_test.go`,
`internal/hostd/lvm` `TestRealAllocated`.

**I-450. Each guest's disk is rate-limited by its size class.** (security
review, 2026-10-03) Every guest volume is on one data disk (16,000 IOPS and
600 MB/s on host-01), opened `direct=on` with no limit, so the guests and
the host's snapshots and restores shared the disk with nothing keeping one
guest from most of it. Cloud Hypervisor's own `--disk` rate limiter now caps reads and
writes together, as token buckets refilled every second (`bw_size=<bytes a
second>,bw_refill_time=1000,ops_size=<IOPS>,ops_refill_time=1000`):
`small` 2,000 IOPS and 80 MB/s, `large` 3,000 and 120, `xl` 4,000 and
150, each at most a quarter of the data disk. The limiter sits in the
hypervisor, so it holds with `direct=on` and leaves what the host itself
does to a volume (snapshot reads, restore writes) unlimited. A cgroup
`io.max` on the volume was the alternative; it needs the unit's device
resolved by systemd and says nothing in `ch.args`, where an operator reads
the guest's shape. The limits are in the argv, so a guest takes them at
its next start. Checked: Cloud Hypervisor 53 parses the options (an
unknown one fails with "Error parsing --disk"). Tests:
`internal/hostd/ch/testdata/args.golden`,
`internal/hostd/guest/limits_test.go`.

**I-451. What a guest receives from outside the host is shaped to 1 Gbit/s
(amends I-217).** (security review, 2026-10-03) I-217 moved the shape to what
a guest sends and left downloads unlimited, so the guests and the WireGuard
tunnel shared the host NIC's receive bandwidth with no per-guest bound. Each tap's root now carries an HTB, handle `2:`, whose
default class `2:20` is `rate 1000mbit ceil 1000mbit` with 10 ms of burst (at least 128 KiB)
and fq_codel under it; class `2:10` at 10 Gbit/s takes what comes from a
`ShapeExempt` source (`10.64.0.0/12`, the gateway; `10.63.255.254`, the
caches), so the caches stay as fast as I-217 made them. Quantum and burst
are 64 KB or more because the host hands the tap GSO packets of that size.
1 Gbit/s is five times the upload limit; a 1 GB image pull takes about 8 s.
The root is added with `tc qdisc replace` when `tc qdisc show` lacks
`htb 2:`, which also swaps out a tap still carrying the root HTB `1:` from
before I-217 (the one release I-217 accepted it for is over); classes,
leaf and filters are `replace` with fixed handles. hostd re-applies the
shape to running guests when it starts (reconcile), so this reaches them
without a restart. `sch_htb` and `sch_fq_codel` are loaded. Checked on a
tap in a network namespace: a tap with the old `1:` root ends with `2:`
and both classes, and a second run and a new rate change nothing else.
Tests: `internal/hostd/net/testdata/{create,reshape}.golden`.

**I-452. A guest's console reaches its log at 2 KiB a second, and the
console has a log buffer of its own.** (security review, 2026-10-03) The
Tailer copied a guest's serial output without a limit, and Fluent Bit sent
the host journal and every console through one output with one 1 GB disk
buffer that drops its oldest chunks when full, so while Loki was
unreachable every console and the host journal competed for the same
buffer.
- The Tailer keeps a token bucket per guest: 2 KiB a second, 1 MiB at once
  (a boot log fits). Output over it is still read from the socket, so the
  guest's console never stalls (I-186), and is dropped; the log then gets
  one line, `[repose: N bytes of console output dropped, over the limit of
  2048 bytes a second]`, before the next output it keeps, or at close.
- Fluent Bit has two outputs to the same Loki, `host.*` with `bufferLimit`
  and `console.*` with the new `consoleBufferLimit` (1G each), so consoles
  fill only their own buffer.
- `ops/loki/retention.yaml` sets the ingestion limits it had commented out
  (16 MB/s tenant, 5 MB/s per stream). A console stream is one guest.
Tests: `internal/hostd/console` `TestRateLimit`; Fluent Bit's
`--dry-run` passes on the rendered configuration.

**I-453. A guest holds at most 16,384 tracked connections, and the host's
table holds 1,048,576.** (security review, 2026-10-03) Every connection a
guest opens through the host, or to the caches, takes an entry in the
host's one conntrack table. I-240 limits how fast a guest opens new flows
but not how many it keeps, an idle established TCP entry lasted five days,
and new flows to the caches had no rate. The table is shared by every
guest and the host; when it is full, the kernel refuses new flows for all
of them.
- `guest_fwd`, before the I-240 rate: `ct state new add @guest_conns {
  ip saddr ct count over 16384 } goto flows_drop`. `guest_in` does the
  same for the cache ports, then a new-flow rate of its own (set
  `guest_cache_rate`, the same 200/s and 2000 burst as I-240), then
  accepts. Drops are counted with the flows kind (`flows-<guest_id>`), as
  new flows the host refused; the EgressBlocked alert's threshold for
  flows already covers them. `guest_conns` is dynamic with no timeout: an
  element goes with its last connection.
- `nf_conntrack_max` is 1,048,576: a D64's most guests (55, all small) at
  the cap take 901,120, which leaves the rest for the host's own flows.
  `nf_conntrack_tcp_timeout_established` is 86,400 s, as kube-proxy sets
  it; SSH and the agents' connections keep alive well inside a day.
  `nf_conntrack` and `nft_connlimit` load at boot, before the sysctls.
16,384 open connections is far past a browser, a test suite or a crawl of
one's own app. Checked: the rendered ruleset loads into a kernel
(`nft -c` and `nft -f` in a network namespace), and a `ct count over 3`
rule in a namespace let three connections through and dropped the next
three.
**I-460. An abuse hold covers every copy of the held project.**
(security release, 2026-10-03) The I-239 hold kept a held project from
starting, and a restore or fork that starts its copy was refused, but a
copy made with `start: false` was a new project with no abuse record of
its own, so it could be started later and the operator review I-239
promises never happened. The hold now refuses every copy of a held
project, started or not: `POST /projects/restore`,
`POST /projects/:id/snapshots/:sid/restore` with `as_new_project`, and
`POST /projects/:id/fork` answer `403 forbidden` with
`detail.reason = "abuse_hold"` whatever `start` says. An in-place restore
that does not start stays allowed: the project keeps its own hold, and a
held user can still roll a volume back while waiting for review. Test:
`TestMinerStopsGuestAndThirdStrikeHoldsStarts` (every unstarted copy route
refused, no project created). *Rejected:* copying the source's hold
rows onto the copy (a second set of rows to clear, and an operator
clearing one would leave the other).

**I-461. A failed restore leaves no stale guest address and no volume to boot.**
(security release, 2026-10-03) An in-place restore destroys the old guest
first, and hostd releases its address then, but the api cleared
`projects.guest_ip` only when it sent the Restore. A restore that failed
in between (no capacity, a deleted snapshot, a failed build) left the
project holding an address the host could give the next guest, and two
live rows with one address make the edge's hook lookup fail for the new
guest. Now:

- The destroy_guest result of a restore clears `guest_id`, `guest_ip` and
  `vsock_cid` at once. A `not_found` from that destroy (the guest a
  restore replaces was never recorded, after a restore that failed before
  hostd wrote its state) counts as done, so the restore can be run again.
- On Hello, a project in `error` whose guest the host does not report,
  with no open op, lets go of its address (rows left before this fix)
  only when its newest restore finished destroy_guest and failed after
  it. A Hello that leaves out a guest that still exists (a partial Hello,
  a hostd that lost its state) releases nothing on its own, since a
  start would not get the address back. Its guest id stays.
- hostd removes the volume of a restore that failed, and a restore run
  again on the same guest id starts on a fresh volume, so no half-written
  volume is left to boot.
- `POST /projects/:id/start` on a project whose newest create or restore
  is a restore that failed in its build or restore phase (after the old
  guest was destroyed, or with none, as a new project) answers
  `409 conflict` with `detail.reason = "restore_unfinished"`, naming the
  restore. Before, a start of such a project either failed at the host
  or would have booted an empty volume. A restore that failed in
  destroy_guest left the old guest and its volume, and one that failed in
  its start phase restored the volume; neither is refused.

Tests: `TestFailedInPlaceRestoreReleasesTheGuest`,
`TestRestoreFailingAfterDestroyReleasesTheGuest`,
`TestHelloReleasesAddressOfAMissingGuest`, and the failed-restore volume
check in `TestSnapshotRestoreRoundTrip`. *Rejected:* scoping the edge's
lookup by host (the edge sends only the source address, and the stale row
was the bug).

**I-462. Snapshots carry a SHA-256 recorded in Postgres, and a restore checks it before writing.**
(security release, 2026-10-03) features/snapshots.md said a snapshot was
verified by checksum and a restore checked one; nothing did. Now hostd
hashes the bytes it hands the store while uploading, checks that the
store holds that many bytes, and returns the hex SHA-256 in
`SnapshotResult`, `StopResult` and `SnapshotDone` (`sha256`, new). The
api keeps it in `snapshots.sha256` (migration 0013) and sends it in
`Restore.sha256`. hostd reads the blob's version (the ETag), downloads
that version once to hash it, and refuses the restore with
`internal: snapshot checksum mismatch` before any guest state or volume
exists when it differs. The write then reads the same version, pinned
per range, and is hashed again; a difference there fails the restore and
removes the volume. So the bytes a restore writes match what the host
uploaded, and a holder of the snapshot store's write access (the shared
Blob identity, SECURITY.md "Not mitigated") cannot get altered bytes
into a tenant's machine. The `Blob` interface gains `Stat` (size and
version, replacing `Exists`) and a version argument on `Download`.

Compatibility, for one release: a `Restore` without `sha256` (an older
api, or a snapshot row from before this entry) restores unchecked, and an
older hostd ignores the field; a result without `sha256` (an older hostd)
writes a row with none. Rows from before 0013 have no digest and restore
unchecked until they expire; a project stopped for months keeps such a
snapshot. The extra download costs about what the restore's own download
does (eight parallel ranges took 1.4 s for a 902 MB snapshot on host-01,
I-403). Tests: `TestSnapshotRestoreRoundTrip` (digest in the result and
the event, upper-case accepted, a malformed one refused, a wrong one
refused with no guest record and no LVM call, bytes changed between check
and write fail and leave no volume), `testVersions` for `FileBlob` and
`MemBlob`, and the digest round trip in
`TestRestoreOntoHostAndSecretValueInFragmentRefused`. *Rejected:*
downloading to a host file before writing (disk the size of the snapshot
on every restore); hashing only during the write (decompresses and
writes bytes before they are known to be good); Blob's own MD5 or CRC64
(it lives in the store and changes with the blob).
**I-463. Each guest's store is a view of its own closure.** (security
review, 2026-10-03; amends R2-1, I-48 and I-61) Every guest's virtiofsd
used to share `/run/repose/store-export`, a read-only bind of the whole
host store with `.links` masked. Masking `.links` stopped one way of
listing the store, and the store directory itself stayed listable, so a
guest could read every project's closure and fragment source on the host
and the host's own system closure. Two fixes were weighed. Keeping
fragment sources out of the store (or deleting them after each build)
leaves every other project's built system, with whatever its fragment put
into `/etc` or a home directory, and the host closure in view. Making the
export root unlistable breaks the guest: its `/nix/store` is an overlay
whose readdir needs the lower layer, and the guest's own garbage
collection lists the store. A per-guest view removes all three exposures
and keeps R2-1's design: guests still read the host's store paths
directly, with no copy.

Which paths: the closure the guest runs, the closures it ran before
(hostd records up to 16 per guest) and the project's kept revisions
(`rev-*` roots), those still on the host. The guest's nix database lists
every closure it was ever given as valid, and nix never fetches a valid
path again, so a package a user installed that the system already had
points into a system closure; with only the current closure in the view
that package would break at the first restart after the next base or
config change. The whole-store export showed those paths for as long as
the host kept them; the view shows this guest's own, under the same
condition. A path the host has garbage-collected is missing in the guest
as it was before; the guest's own `nix-collect-garbage` tolerates such a
path (checked on a local store: a valid path with its files gone is
deleted without error).

How: `virtiofsd@<id>` runs with `PrivateMounts=yes` and
`TemporaryFileSystem=/run/repose/store-view` (root-owned, mode 0755,
16 MiB), shares `/run/repose/store-view`, and pivots into it as before
(`--sandbox namespace`). Once it has, hostd lists the guest's closure
(`nix-store -qR`) and, from a thread that joins that unit's mount
namespace, bind-mounts each store path in read-only, nosuid, nodev, with
private propagation (a clone of a store path keeps the host store's peer
group otherwise); a store path that is a symlink is recreated as one.
Nothing is mounted in the host's namespace, so systemd tracks none of it.
At boot this runs before the hypervisor starts; an in-place apply adds the
new closure's paths before guestd switches, and leaves the old ones until
the guest's next start, since processes the switch did not restart still
use them. 1,119 paths (this guest's closure) bound in 47 ms with
virtiofsd 1.14 serving, and a path added while it served appeared in its
root (internal/hostd/storeview, TestVirtiofsdServesTheView, root only).
A view that cannot be filled fails the boot at the virtiofsd step.

Guests running when a host switches to this are restarted at the switch:
`hostd guests` shows a guest whose virtiofsd still shares the whole store
as `whole-store`, reconcile logs `store_view_restart_needed` for each, and
the host switch runbook restarts them through the api (hostd binds nothing
into a virtiofsd whose root is not a tmpfs, so until then they run as
before). The whole-store export stays one release as the rollback:
`hostd --store-export /run/repose/store-export` switches every new start
back to it. The release after removes
`repose-store-export.service` and the flag's old meaning. *Rejected:* a
hard-link farm per closure (about 57,000 directories per guest on the root
filesystem, seconds per start, and the 65,000-link limit on popular
inodes); bind mounts in the host namespace (one systemd mount unit per
store path per guest); `systemctl bind` for the in-place additions (it
leaves the new mount shared with the host store's peer group). Interface:
`host-conventions.md` (store view row, virtiofsd invocation).

**I-464. Each user's Claude login share is its own 16 MiB volume.**
(security review, 2026-10-03; amends I-278) The share was a directory on
the host's root filesystem that a guest writes freely, so one guest could
fill `/` and with it the store, hostd's state, builds and logs for every
tenant on the host. hostd now creates `vg-guests/auth-<user_id>` (thin,
16 MiB, ext4 with no reserved blocks and 256 inodes) and mounts it
`nosuid,nodev,noexec` at the same `users/<user_id>/claude-auth` path, so
the virtiofsd and the hypervisor arguments are unchanged and the most one
user can write on a host is that volume. The guest cannot write the
volume's metadata, only files through virtiofsd. A share directory from
before is renamed `claude-auth.legacy` at the first mount, never opened,
and the sweep removes it once no guest of that user runs; the user signs
in to Claude Code once more on that host. The sweep unmounts and removes
the volume with the user's directory, and removes a volume whose directory
is gone. All of this runs under a per-user lock, so two guests of one
user booting together create, format and mount the volume once, and the
sweep checks again under that lock that no guest of the user exists
before it removes anything; each step also accepts an existing volume,
filesystem or mount. A volume that cannot be created or mounted leaves the guest
without the share (I-278's failure rule). *Rejected:* ext4 project quotas
on the root filesystem (enabling the quota feature needs it unmounted);
one shared volume for every user (one user filling it signs every user on
the host out); copying the old file into the volume (I-278: repose code
never reads or copies it). Interface: `host-conventions.md` (users row,
thin pool row, virtiofsd-auth invocation).

**I-465. dumpe2fs, e2fsck and blkid run in a sandboxed transient unit.**
(security review, 2026-10-03) A guest writes every byte of its volume,
and hostd ran `dumpe2fs` on each snapshot and `e2fsck -fp` on each
restored volume as root in its own service, which holds the host's mTLS
key and the snapshot storage credential. A memory-safety bug in
e2fsprogs's parsing would have landed there. Both now run through
`systemd-run --pipe --wait` as a dynamic user with no capabilities,
`PrivateNetwork`, `RestrictAddressFamilies=AF_UNIX`, `/var/lib/repose` and
`/run/repose` inaccessible, the `@system-service` syscall set, 2 GB of
memory at most, and `DevicePolicy=closed` with `DeviceAllow` naming only
that volume (read-only for dumpe2fs); the unit joins the device node's
group so it can open it. Output and exit codes pass through, so restore
still reads e2fsck's 1 and 4. Checked on a loop device: dumpe2fs output
and e2fsck exit 1 then 0 came back, another block device was refused with
EPERM, an inet socket with EAFNOSUPPORT. `blkid`, which hostd runs before
mkfs to see whether a volume already has a filesystem, goes through the
same sandbox with read-only access: a create retried for a guest whose
first create failed probes a volume the guest may have written, and its
exit 2 ("nothing found") passes through like the others. *Rejected:*
`RestrictAddressFamilies=none` (systemd-run refuses it for a transient
unit); denying `@network-io` (dumpe2fs dies on it); running as root
without capabilities (still reads every root-owned file).

**I-466. hostd creates the guest directory's socket directories without
following a link.** (security review, 2026-10-03) The guest directory is
writable by the hypervisor's user, so `virtiofsd/` or `virtiofsd-auth/`
may already exist there as something hostd did not create, and hostd's
`MkdirAll`, `Chown` and `Chmod` followed a symlink to its target. hostd
now removes anything at those names that is not a directory, removes a
directory owned by anyone but itself or the intended owner, and sets
owner and mode through a descriptor opened `O_DIRECTORY|O_NOFOLLOW`, so a
link swapped in after the check fails the open. The guests directory, the
guest directory and the users directories go through the same function.
**I-467. The bridge keeps credentials and traffic bodies in the laptop's Chrome, not only cookies.**
(security review, 2026-10-03; amends I-311) I-311 kept cookies in
Chrome so that nothing a tool reads works after the bridge closes. The
same holds now for the other things a login hands the browser. In the
copy of network, fetch and audit events the tools get (Chrome's own
traffic is untouched), the front removes the `Authorization`,
`Proxy-Authorization` and common API-key headers (`X-Api-Key`,
`X-Auth-Token`, `X-Access-Token`, `X-Amz-Security-Token`,
`X-Goog-Api-Key`, `X-Vault-Token`, `Private-Token`) beside Cookie and
Set-Cookie, from header maps, header lists and header text alike; drops
request bodies (`postData`, `postDataEntries`), WebSocket frame payloads
(`payloadData` of `webSocketFrameReceived`/`Sent`), server-sent event
data (`eventSourceMessageReceived`) and streamed body chunks
(`dataReceived.data`); and drops
`rawCookieLine` from audit issues, where Chrome puts a whole Set-Cookie
line for a cookie it rejected. Every `Network.*`, `Fetch.*` and
`Audits.*` event is now decoded and scrubbed, not a list of known ones,
so an event Chrome adds later is covered. Refused always: the readers of
request and response bodies (`Network.getResponseBody`,
`getRequestPostData`, `getResponseBodyForInterception`,
`takeResponseBodyForInterceptionAsStream`, `searchInResponseBody`,
`streamResourceContent`, `loadNetworkResource`, `Fetch.getResponseBody`,
`takeResponseBodyAsStream`, `Page.getResourceContent`,
`searchInResource`, `Audits.getEncodedResponse`), and the `DOMStorage`,
`IndexedDB` and `CacheStorage` domains, which read any site's stored data
rather than the page's. `IO.read` stays: no body stream can be opened,
and Playwright's PDFs arrive through it.

What the agents lose: a tool's "show me the response body" fails with a
refusal that says to read the page, and a tool that rewrites a paused
request's headers sends it without the removed ones. They still read
pages, run scripts in them, and see URLs, statuses and the other headers.
What stays an accepted gap (user docs): a page's own JavaScript can read
what that page can, including bodies it fetches from its own site and
the messages on its own WebSockets.
Evidence: `TestBridgeCredentialsAgainstChromium` against a real Chromium
(a page's fetch carried its Authorization header to the server; the tool
watching the network saw none of the header, the body or the rejected
cookie line, the WebSocket messages either way or the server-sent event,
and a body read was refused); fake-peer tests for every
refusal and scrub; Playwright and chrome-devtools-mcp still work through
the front.

**I-468. A dropped path is judged by the file it reads, and key files are never a drop.**
(security review, 2026-10-03; amends I-280) The input proxy checked the
pasted path for system directories and hidden components, then read
whatever the path led to. It now follows every symlink first and applies
the same checks to the file that would be read, both when the paste is
scanned and again just before the copy, and reads only a regular file. A
file named like a private key or key store is text, not a drop, whatever
folder it is in: `id_rsa`, `id_dsa`, `id_ecdsa`, `id_ed25519` (not
`.pub`), and `.pem`, `.p12`, `.pfx`, `.p8`, `.ppk`, `.jks`, `.keystore`,
`.kdbx`, `.keychain`, `.keychain-db`. Nor is a file of any name whose
first 4 KiB hold a PEM, OpenSSH or PGP private key, or a `.gpg`/`.pgp`
file that starts with an OpenPGP secret-key packet (tag 5). `.key` and
`.asc` are judged by that content, not by name: Keynote uses `.key`, and
`.asc` is mostly public keys and signatures; `.gpg` is mostly encrypted
files, which still copy. A file that cannot be read is not a drop. Every copy names its files on the tmux status line
("copied report.pdf to the machine"), so a path pasted because an agent
asked for it never leaves the laptop unseen. Tests: the scanner table
(key names), `TestDropFileFollowsLinks` (a link to a hidden file, to a
hidden directory, to a system file and to a key are not drops; key
content under `.key`, `.txt`, `.asc` and `.gpg` names is refused, and a
Keynote-like `.key`, a public key, a signature, an encrypted `.gpg` and
a public keyring copy), and the
pty test (a link to a hidden file is typed as text and nothing is
copied; a copy is named).
signed (I-429's pin narrows that). The `release`
environment and its secret are owner setup (`docs/ops/RELEASE.md` "The
release signing key").
ciphertext; a failed tag check under the bound data says the same thing for
free); binding in one release (breaks the rolling deploy and rollback).

**I-477. The landing leads with replicating your laptop's dev environment,
for solo founders.** (owner, 2026-10-04; amends docs/LANDING.md "What we
sell", "The hero" and "Shape language"'s blue bar)
A competitor (boat.dev) sells cheap agent sandboxes by the second, with a
public URL, a virtual desktop and an SDK for fleets. Its page is written
for people building agent products. The owner's launch video calls
repose "Replicate your dev environment", and the owner wants the page to
own that claim for solo founders: one person whose agents do the work. The
headline is now "Your dev environment, replicated in the cloud", with the
blue bar under "replicated"; above it a label reads "For solo founders and
their agents". The lead keeps its three beats (the machine, the wreck, the
snapshot) and carries "full permissions" in its second sentence. "Your
working state, in one command" becomes "Your laptop's setup, in one
command", with one sentence naming what the picture cannot show: the
globally installed CLIs and the copied logins (`sync.md`, `machine.md`
"Your laptop's tools come along", `secrets.md` "Logins copied from your
laptop"), and that the SSH keys stay home. The title and meta description
say the same.
*Rejected:* naming boat or any other product on the page; a price
comparison (boat bills per second, repose is a flat month, and the
numbers swap with the hours run).

**I-475. Each bash command loads the current secrets through BASH_ENV, without replacing a value the process set itself.**
(secrets-env, 2026-10-04; amends I-241) A secret set after an agent
started reached `/run/repose/secrets/NAME` but not the agent's commands.
`secrets.env` was sourced only by `/etc/profile.d/repose.sh`, which a
shell reads when it starts, and an agent runs each command as a
non-login, non-interactive `bash -c` that inherits the agent's
environment (Claude Code's shell snapshot exports only PATH). The agent
guide said secrets were environment variables, so agents guessed and
spent turns on it.
`BASH_ENV=/etc/repose/bash-env.sh` is now a session variable, so it is
in `/etc/set-environment`, the PAM environment, dev's user manager and
the tmux server; the activation script that refreshes PATH there after a
base switch sets it too. A stable `/etc` path rather than a store path,
so a process holding BASH_ENV across a switch still finds it.
The rule is by value. For each name guestd keeps the values it exported
before and no longer does, distinct, oldest first, the last 16 per name,
in `/run/repose/secrets.state` (root 0600, tmpfs), and writes
`/run/repose/secrets.refresh` (dev 0400, tmpfs) from them: per current
secret `case ${NAME+s$NAME} in ''|s'earlier'...) export NAME='current'
;; esac`, per removed one the same patterns without `''` and `unset NAME`.
A process that lacks a secret gets it; one that holds an earlier value
guestd exported gets the current one; any other value is the process's
own and stays across any number of later writes: the project's `.envrc`
(loaded by the agent wrappers after the profile, I-259), an override for
one command (`STRIPE_KEY=sk_test ./run-tests.sh`), and a removed secret
the `.envrc` now provides with another value. A first version re-sourced
the whole file whenever the generation differed; since an agent's own
generation never changes, every command after the first write replaced
the `.envrc`'s values with the secrets, which reviewers reproduced.
Each environment guestd gives out also carries `REPOSE_ENV_GEN=<16 hex>`,
which changes when the exported set does and only then. The loader
sources the refresh file only when its first line, `# repose-env-gen
<gen>`, names another generation than the process's, so a process formed
from the current set pays one `read` per bash. The marker decides nothing
else. Its name holds none of `KEY`, `SECRET` or `TOKEN`, so a sandbox
that drops those (Codex's `shell_environment_policy`) keeps the fast path.
Second review (2026-10-04): a second version compared each value with
what the process's own generation delivered, keeping the last 16
generations. Every write is a generation and `secrets import` makes one
per line, so after 16 writes to any names an agent's generation expired:
a rotation and a removal stopped reaching it, `exec $SHELL` stopped
working, and a name its sandbox dropped came back. Bounding history per
name instead of per write removes the expiry for everything but a name
changed more than 16 times while an agent holds its oldest value (that
process keeps it, as it would any value of its own), and drops about
150 lines of generation bookkeeping. What it gives up, stated rather
than half kept: a name a process lacks is filled in on its next bash
after a change, whatever removed it. The generation version kept a
sandbox's drop only for names that existed when the agent started and
only within the 16 generations; a `*KEY*` secret set later reached the
sandbox anyway. To keep a secret out of a command, give the variable an
empty value or leave `BASH_ENV` out of its environment (in Codex,
`shell_environment_policy.exclude`). A value that equals an earlier
secret value follows the secret: an `.envrc` that loads the same `.env`
the user imported gets the user's later changes, and loses the variable
when the secret is removed (the generation version did the same).
A process with no generation (an ssh login, a user unit, a process from
before this guestd) follows the same rule. `/etc/profile.d/repose.sh`
sources the loader too, so a login shell does; it falls back to sourcing
`secrets.env` when no refresh file exists (a guest whose secrets an older
guestd wrote). `secrets.env` is now `export REPOSE_ENV_GEN=<gen>` and one
`export` line per current secret.
The refresh file holds the earlier values, a removed secret's included,
readable by dev, until the guest restarts or 16 newer values of that name
replace them: without them it could not take a removed value out of a
process that inherited it. The public docs say so, and tell the user to
revoke a leaked key at its provider. The comparison runs in the shell, so
it compares values, not hashes: hashing would need a program per name per
command. Cost for a process with an older generation, measured with bash
5.3 (`bash -c true`, 2.0 ms without the loader): ten 64 B secrets each
changed 16 times +0.6 ms; thirty 1 KiB ones each changed 16 times (526 KB
file) +5.5 ms; twenty 64 KiB ones, one changed 16 times (2.4 MB) +28 ms,
all twenty changed 16 times (22 MB) +235 ms. A process with the current
generation pays nothing measurable. The large cases are outside what
secrets are used for; per-generation delta files would cut them to the
names that changed but need the per-generation history this entry
removed, and are the next step if they show up.
The loader runs builtins only and is POSIX sh, prints nothing (xtrace and
verbose are off while it runs, so `bash -x` never echoes a value), keeps
`$?`, `$_`, the positional parameters and the shell options, and does
nothing when the file is missing or unreadable (nobody and other users,
before the first write). `$_` is saved first and put back by a final
`: "$saved"` whose trace goes to /dev/null, so a script's first command
still sees its own path; the saved copy stays as an unexported
`__repose_bash_env_u`, since unsetting it would change `$_` again. Root
reads the dev file and loads the secrets, as `/etc/profile.d/repose.sh`
already did for a root login shell. Secrets named `BASH_ENV`, `ENV` or
`REPOSE_ENV_GEN`, or starting `__repose_`, would switch the refresh off:
the api and the CLI refuse them on set (one stored before stays listable
and deletable), and guestd writes the file but never exports it.
Rejected: a PROMPT_COMMAND or DEBUG trap (interactive shells only, and
agents' shells are not); re-exec'ing agents on a change (kills their
work); exporting secrets through the agent wrappers (still fixed at
agent start); secrets winning over `.envrc` after any rotation (the
first version's behaviour; a test run would silently use a live key);
per-generation history, bounded (the second version; expires) or kept
for the guest's life (every write rewrites a table that grows with
writes); dropping a removed value from the file at once (an agent would
keep the removed key). `sh` does not read BASH_ENV, so a command an agent
runs with `sh -c` or a `#!/bin/sh` script keeps the inherited values; the
public docs and the agent guide say so.
Tests: `internal/guestd/secrets/loader_test.go` runs the loader with real
bash under `set -euo pipefail`, `set -u` and a script, with the file
missing, present, unreadable and empty; an override kept with a current
and with a stale generation across rotations and removals, a dropped
name filled in and an emptied one kept empty
(`TestLoaderKeepsOverridesAcrossLaterWrites`); a rotation and a removal
reaching an agent after 64 writes to other names, and its oldest value
kept only after 17 values of its own name
(`TestLoaderAfterManyWritesToOtherNames`); no generation; `$_` equal to a
run without the loader for a script and `-c`, with and without `-x`
(`TestLoaderKeepsUnderscore`); tricky values from bash and sh; `bash -x`
silent. `secrets_test.go` `TestEarlierValuesAreBoundedPerName`.
`e2e_test.go` drives an agent-like bash in a real tmux window through 35
writes, 32 of them each adding another name, and finds no value of the
run in any `/proc/*/cmdline`; VM subtests in `nix/guest/tests/guestd.nix` (a
`systemd-run` parent as dev that sets its own value, then `WriteSecrets`)
and `nix/guest/tests/default.nix` (a tmux window opened over ssh, and a
login shell).
*Made during implementation* (base-hostd-guestd, 2026-10-06): a guest whose
secrets an older guestd wrote had no `secrets.refresh` after the live
switch onto this base, so the loader did nothing, and the switch restarted
the tmux session before guestd, so `tmux new-window` (how `repose run`
starts agents) gave agents no secrets until the next write. guestd now
rebuilds the refresh file at start, before serving, when it is missing
and `/run/repose/secrets/` holds files: the set is read from those files
and goes through the same path as a write (a new generation, no earlier
values, then `secrets.refresh` and `secrets.env`), then to tmux. It does
nothing when the refresh file exists. `/etc/profile.d/repose.sh` keeps
its `secrets.env` fallback, and the loader gets none: the old
`secrets.env` has no `REPOSE_ENV_GEN`, so a loader that sourced it would
override an `.envrc` on every command, the first version's bug. Test:
`TestRestoreRebuildsTheRefreshFileAfterALiveSwitch`.

**I-476. Removed secrets leave running processes, and WriteSecrets updates the tmux environment through stdin.**
(secrets-env, 2026-10-04) Loading secrets alone cannot take away a
variable a process inherited, so `repose secrets rm` left the old value in
an agent's commands. `secrets.refresh` now has a guarded `unset NAME` line
for every name guestd exported before and does not now (I-475's rule:
only when the process holds one of the values guestd exported for it).
The reserved names of I-10 never enter the state or either file. On each
write guestd also pipes a tmux configuration to `tmux source-file -`, as
dev: `set-environment -g NAME "value"` per secret, `-gu NAME` per removed
name and `-g REPOSE_ENV_GEN` last, so a new tmux window matches even when
its command is not bash. The first version passed the values as
arguments: `/proc/<pid>/cmdline` is readable by every uid, and the tmux
client refuses a command over 16 KiB (`command too long`), so one 64 KiB
secret broke the push. On stdin neither applies. Each value is one
double-quoted token: `\`, `"` and `$` get a backslash, and `~` (expanded
even inside quotes at a token's start) and every byte outside printable
ASCII become octal escapes. No tmux server, a tmux error or a missing
tmux is logged by exit code only, never with stderr, and does not fail
the request: the tmpfs and BASH_ENV already carry the secrets. The
generation goes last so a failure part way leaves tmux refreshing.
Tests: `TestRefreshUnsetsARemovedName`, `TestHistorySurvivesAGuestdRestart`,
`TestUnreadableStateStartsANewHistory`, `TestShellReservedNamesAreNotExported`,
`TestSecretsArePushedToTmux`, `TestTmuxFailureDoesNotFailTheWrite`,
`TestTmuxArgumentsAgainstARealTmux` (two 64 KiB values and the tricky
ones through a real tmux), `TestEndToEndAgentInTmux`.

**I-474. Named secrets are written bound to their project, and the api rebinds older rows at start.**
(security release, 2026-10-04; step 2 of I-433) `secrets.Seal` now
always writes the project-bound form: the `sealBound` switch and
`secrets.SealBound` are gone, and `Put`, `PutReserved`, `CopyNamed` (fork)
and `Reseal` all go through `Seal`. `Open` still accepts a row bound to
its name alone for this release; step 3 of I-433 removes that path in the
release after this one. An api from before I-433 cannot open what this one
writes, which is why I-433 shipped the reader first.

Every api process (`http`, `grpc` and `all`) starts `Store.ResealLoop` in
the background from `App.Run`, so startup does not wait on Key Vault. A
pass takes the advisory lock `db.LockSecretsReseal` (1013) so the api and
api-grpc containers, and the old and new container of a Coolify rolling
deploy, do not unwrap the same keys at once; a process that finds the lock
held tries again later. Inside a pass each `UPDATE` matches the ciphertext
it read, so two overlapping passes, or a `secrets set` landing between a
pass's read and its write, leave every row as one whole writer left it. A
failed pass (Key Vault or Postgres unreachable) is retried from 5 seconds,
doubling to 5 minutes. A pass checks Key Vault with `CurrentVersion`
first. A row whose data key does not unwrap (a disabled or purged key
version, a corrupt `dek_wrapped`) is skipped and counted in `failed`, so
one bad row does not stall every pass; three distinct data keys failing
in a row end the pass as a Key Vault outage. The loop makes a second pass
15 minutes after the first, because the container a rolling deploy
replaces keeps writing name-only rows until it stops, and ends at the
first pass from the second on that rewrote nothing. With no row skipped it
logs `secrets_name_only_none`, the operator's signal that no row needs the
name-only read path and step 3 can be scheduled. With the same number of
rows skipped as the pass before it logs `secrets_reseal_incomplete` with
that count instead, and step 3 waits until those rows are repaired or
deleted. A name-only row under the platform's data key outside the
platform project is still refused on read and left as it is by the
reseal (counted in `refused`). `secrets_reseal_fail` carries `code`
(`key_service_unavailable`, or `db` for any Postgres failure including
the lock) and the error text; no line carries a name, a project or a
value (`docs/ops/OBSERVABILITY.md`).

What this does not close: a user's name-only row copied into another
project by a database write before the reseal reached it is resealed in
the project it sits in, as I-433 accepted for its first step; a copy made
after the reseal does not decrypt.
Tests (real Postgres): `internal/api/secrets` `TestRoundTripAndAAD` (`Put`
writes the bound form; it does not open under another project or in the
name-only form), `TestCiphertextIsBoundToItsProject` (a bound row copied
into another project does not open, a name-only row does until `Reseal`,
the platform-key copy is refused before and after), `TestResealLegacyRows`
(converts user and platform rows, refuses the platform-key copy, a second
run rewrites nothing), `TestConcurrentResealKeepsEveryRow` (three
concurrent passes over 60 name-only rows: each row counted once, each
opens bound in its own project with its own value),
`TestResealDoesNotOverwriteANewerPut`, `TestCopyNamedBindsToTheFork`,
`TestResealSkipsARowKeyVaultWillNotUnwrap`,
`TestResealLoopStopsOnARowThatNeverUnwraps`,
`TestResealLoopLogsDBCodeWhenPostgresIsDown`,
`TestResealLoopRetriesWhileKeyVaultIsDown` (a name-only row written after
the first pass is bound by the second; `secrets_name_only_none` follows
the third, which rewrote nothing); `internal/api/app`
`TestStartResealsAfterKeyVaultReturns` (the api is healthy with Key Vault
down and rebinds the row once it answers); `internal/api/http` `TestFork`.
*Rejected:* resealing inside `New` before serving (a Key Vault outage
would keep the api down); one pass per start only (misses rows the old
container writes during the rolling deploy); ending the pass on the first
unwrap error (one unreadable row would stall every pass and hide
`secrets_name_only_none` behind a Key Vault outage that is not
happening); a gauge of name-only rows
(counting them means opening every row on every scrape; a pass already
does that, so the pass logs the result).
**I-469. A dropped attach attaches again, and `repose open` reconnects.**
(edge-zero-downtime, 2026-10-04) An attach's ssh that ended with 255
(ssh's own failure: Wi-Fi went, the laptop slept, the edge restarted)
ended the command, though the tmux session on the machine was still there.
On the input-proxy path (macOS and Linux with a terminal, I-280) the CLI
now keeps the terminal raw and its one input reader, prints `repose: lost
the connection to <slug>. Reconnecting; Ctrl-C stops.`, runs `ssh <target>
true` every second for up to 2 minutes, and attaches again to the session
(not to the agent window the first attach named, so the user lands where
they were). Keys typed while it waits are dropped; Ctrl-C or Ctrl-D stops
the wait. A certificate refusal gets a renewal through `connect` (a
relay ends at its certificate's expiry, I-436), tried again on the next
refusal if it failed; a refusal after a renewal that worked ends the
wait with the gateway's line, as does a stopped, destroyed,
errored or unknown project; an
attach that drops again within 5 seconds of a reattach ends it too, so a
connection that cannot hold is not retried for ever. Past 2 minutes:
`could not reach <slug> for 2 minutes. \`repose attach <slug>\` attaches
again once it answers.` and exit 255 as before. `repose open` runs its
ssh as a child instead of becoming it, and reconnects the same way on
the same local port; Ctrl-C ends it with status 0 as before. The session
helper's auto-forward (I-199) keeps the ControlMaster's pid and, when a
reconnect brings a new master, adds its forwards again, unannounced.
Where the CLI has become ssh (Windows, `REPOSE_INPUT_PROXY=0`, no
terminal) nothing changes. *Rejected:* mosh or Eternal Terminal (a second
transport through the gateway for a problem tmux already solves);
reconnecting inside ssh with `ServerAliveCountMax` (ssh cannot reconnect).
Tests: `TestAttachLoopReattachesAfterADrop` (real ssh on a pty through a
proxy that cuts the connection and refuses for 2 s: the same program gets
what is typed before and after), `TestReattach*` (the waits, the final
banners, one renewal, Ctrl-C, flapping, a cancelled `open`),
`TestForwardOverTheControlMaster` (after `ssh -O exit` and a new master
the forward answers again on the same port with no second message).

**I-470. systemd holds the gateway's SSH socket.** (edge-zero-downtime,
2026-10-04) `gateway-ssh.socket` listens on 22 with
`FileDescriptorName=ssh` and a 4096 backlog; `gateway.service` requires
it, takes the socket from `LISTEN_FDS` and is `Type=notify` (READY=1 once
it serves). While no gateway runs (a crash, `systemctl restart gateway`)
a client's connection waits in the backlog and the next gateway serves
it, where before it was refused for a second or more. The socket carries
the service's `ConditionPathExists`, so a fresh edge does not queue
connections for a gateway that cannot start. 443 and the WireGuard
listeners (hook ingest, metrics) stay the gateway's own: without its
wildcard certificate the preview stub does not serve, and a held 443
would hang clients where it refused them. Outside systemd the gateway
binds `GATEWAY_LISTEN` as before. NixOS's switch never restarts a changed
`.socket` unit (switch-to-configuration-ng leaves sockets alone), so a
change to it is a deliberate `systemctl restart gateway-ssh.socket
gateway`, which drops sessions (ops/RUNBOOK.md "Switch the edge").
Tests: `TestRestartQueuesConnections` (a dial made while no gateway runs
completes on the next one), `TestSystemdReloadAndRestart` (the same under
this machine's systemd with the edge's unit settings, a client dialing
while the unit is stopped is served after `systemctl start`).

**I-471. A switch hands the gateway over instead of restarting it.**
(edge-zero-downtime, 2026-10-04) An edge switch restarted the gateway,
and since the gateway terminates SSH (I-1), every user's terminal,
editor, forward and copy died with it. `gateway.service` is now
`reloadIfChanged`, and its `ExecReload` is `gateway handover` from the
new build: it sends its executable path and environment (the new unit's,
so a changed `Environment=` takes effect) over
`/run/repose-gateway/control.sock` (the unit's RuntimeDirectory, 0700,
peer uid checked). The running gateway starts that build with its
listening sockets as `LISTEN_FDS` (named, no `LISTEN_PID`) and the
notify socket, waits up to 30 s for it to write `ready` on a pipe, then
sends `MAINPID=<new>` and a `BARRIER=1` so systemd has read it before
anything else happens, answers the client, stops accepting, shuts its
HTTP listeners down and serves its open relays until each ends (a relay
lasts at most 24 hours, I-436), then exits. `NotifyAccess=all`, since the
new process says READY=1 before it is the main one. A new build that
does not come up is killed and the old one serves on; the reload, and so
the switch, fails with the reason. SSH session keys cannot leave the
process (`x/crypto/ssh` keeps them private), so this is a drain, not a
migration. What it costs: during a drain each process keeps its own
relay counts, so the 200-relay and 32-per-user caps apply per process
and a user can briefly hold more than 32; the draining process is not
scraped (its metrics listener went to the new one) and its relays are
missing from `repose_gateway_sessions`; it still refreshes revocations
every 30 s and ends revoked relays, but no longer gets a push. A crash of
the new main process makes systemd restart the unit, which ends the
draining process too. systemd logs `Supervising process N which is not
our child. We'll most likely not notice when it exits.` on each handover;
systemd 261 does notice (pidfd), checked by killing the handed-over
process. `systemctl restart gateway` still ends every session at once,
for a fix in relay code that must reach open connections. The first
switch onto this build cannot hand over (the running gateway has no
control socket, and it holds :22, so the new socket unit fails to
start); RUNBOOK "Switch the edge" has the one restart it needs.
*Rejected:* cloudflare/tableflip (it re-executes the running binary's own
path, and on NixOS the new build is another store path); a master
process that never changes and supervises workers (the master's code
could then only change by a restart, and systemd tracks a non-child
main process anyway); SO_REUSEPORT between two units (a second unit
name per build, and a switch would still stop the old one). Tests:
`TestHandoverKeepsOpenSessions` (real processes: a session opened before
the handover keeps echoing, new connections and new sessions on the old
connection work, MAINPID names a process running the new path, the old
process exits once its last relay closes, and a second handover back
works), `TestHandoverFailureKeepsServing`, `TestSystemdReloadAndRestart`
(this machine's systemd 261, DynamicUser and the edge's sandbox: rewrite
the unit to name build B, daemon-reload, reload; MainPID moves to B, the
session survives, the unit stays active, the old pid exits after its
client closes; SIGKILL to B restarts the unit).

**I-472. An edge switch leaves the network up.** (edge-zero-downtime,
2026-10-04) Two units took the network down on an ordinary switch.
`wireguard-wg0`'s script names the store paths of `ip` and `wg`, so any
nixpkgs bump changed it, and its restart deletes wg0 with every host peer
wgsync added: each relay died, and hosts were unreachable until wgsync's
next pass up to 30 s later. It is now `reloadIfChanged` with an
`ExecReload` that re-applies the key, port and address to the live
interface (creating it only if it is missing) and keeps every peer. A
changed address is added beside the old one; removing an address is a
deliberate `systemctl restart wireguard-wg0`. Static peers
(`wireguard-wg0-peer-*`) still restart when their unit changes, a
sub-second gap on the control plane's and the monitoring server's
tunnels that no relay uses. dhcpcd restarts whenever its package changes
and, by default, removes eth0's address and default route when it stops;
`networking.dhcpcd.persistent = true` leaves the interface configured
across the restart (Azure's address is static). Checked: systemd 261
runs `ExecReload` on a `oneshot` `RemainAfterExit` unit;
switch-to-configuration-ng reloads an `X-ReloadIfChanged` unit instead
of restarting it; the built edge's units carry both. Not checked on the
edge itself before its first switch with this (ops/RUNBOOK.md "Switch the
edge" says what to look at).

**I-473. One edge for now; the way to two is written down.**
(edge-zero-downtime, 2026-10-04) I-470..I-472 make a switch harmless, but
the edge is still one VM: a reboot (a kernel update), an Azure host
event or a crash of the VM ends every connection and nothing connects
until it is back. The CLI's reattach (I-469) and editors' reconnects
hide a short one. The fix is two edges behind an Azure Standard Load
Balancer on 22 (and 443 when preview URLs exist): the load balancer
stops sending new flows to an edge whose health probe fails and lets its
established TCP flows continue "until idle timeout or connection closure"
(Azure's health probe documentation, checked 2026-10-04; the idle timeout
is 4 minutes by default, up to 100, and the CLI's `ServerAliveInterval
30` keeps a quiet session under it), so an edge is drained by failing its probe
(a file the probe checks, or stopping a small health listener), switched
or rebooted once its relays are gone, and put back. What it needs:
each host peers with both hubs (wgsync on each edge, hosts' WireGuard
config listing two endpoints), a guest route that works through either
edge, the revocation push sent to both, and caps (200 relays, 32 per
user) that are per edge, which halves a user's share on each unless the
edges share counts. Costs: the load balancer's hourly charge and its
per-GB data processing, and a second D2s_v7. *Revisit when:* the edge
needs a reboot that cannot wait for a quiet hour, uptime is promised in
the terms, or one edge's relays near 200 (`repose_gateway_sessions`).
Until then a kernel update on the edge is announced and done at a quiet
hour (RUNBOOK "Switch the edge").

**I-479. A TLS side listener opens only once its certificate loads.**
(gateway-tls-listener, 2026-10-04; fixes I-470) Moving the gateway's
listeners into `listenerSet` replaced `ListenAndServeTLS`, which closes
its listener when the certificate fails to load, with a listen followed
by `ServeTLS`, which returns on that failure and leaves the listener
open. On the edge, where the preview stub's wildcard certificate is not
placed yet, 443 then held every client until it timed out, where before
it refused at once; seen after the switch of 2026-10-04 20:51Z (`curl`
to 443 timed out). `tlsListener` now loads the key pair before it opens
the port and logs `listener disabled: certificate does not load`, and
`serveHTTP` closes its listener when serving ends in an error. An
inherited socket for a listener that does not open is closed with the
other unused ones. Test: `TestTLSListenerWithoutCertificateStaysClosed`.

**I-483. A flake dev shell keeps its lock out of the checkout, and a flake applied as a fragment says so.**
(flake-devx, 2026-10-04; amends I-259's "nothing is written to the
checkout") A live test of a project with a `flake.nix` at its root, on a
temporary project on the 2026.10.04.1 base, found three things the docs
and messages got wrong. First, with no `flake.lock` in the repository,
the first dev shell load writes one into the checkout and stages it
(`git status`: `A flake.lock`), because `use flake` locks unlocked
inputs; the next `repose sync` from the laptop then stops on "uncommitted
changes ... flake.lock". The doc's "Nothing is written to the checkout"
held only for a repository that commits its lock. The code now keeps the
promise: for a flake with no `.envrc` and no `flake.lock`, the generated
`.envrc` in the shadow directory says `use flake ROOT
--reference-lock-file SHADOW/flake.lock --output-lock-file
SHADOW/flake.lock`, so Nix reads and writes its lock there; once the
repository has its own `flake.lock` the arguments go and that lock is
used. Checked on a guest by sourcing the changed devshell.sh with the
base's store paths: checkout clean after the first load, second load
from cache, the switch to a committed lock reloads. A user's own `.envrc`
with `use flake` still writes the lock into the checkout; that is
direnv's and nix-direnv's behaviour, and the docs say so. The docs now
also say to commit `flake.nix` (Nix ignores untracked files, and sync
carries them anyway) and `flake.lock`, to define the shell for
`x86_64-linux`, and that a flake's `nixosConfigurations` and the like are
not read. Second, `repose config apply
flake.nix` reached home-manager as options and failed with `option
'description' does not exist in a fragment` plus a did-you-mean about
`home-manager.users.dev.xsession`. `MapEvalError` now answers an unknown
top-level `description`, `inputs`, `outputs` or `nixConfig` with "this
file is a Nix flake; `repose config apply` takes a home-manager module
such as repose.nix ..." and drops the did-you-mean. Third, the
unknown-option hint and the `repose.system` refusal named `repose config
menu`, a command the CLI has never had; they now name `home.packages`,
`repose config add` and `repose.system` (hostd) and `repose config add`
or the dashboard's Config menu (the base's contract.nix, next base).
Test: the `flake.stderr` fixture, captured from the live apply.

**I-490. The personal layer: an account's machine.nix on every machine, applied without asking and never holding a machine up.**
(machine-nix-personal, 2026-10-04; stages 2 and 3 of
`docs/proposals/2026-10-04-your-machine-nix.md`, with the owner's
settlement of the same day: `repose config --global`, `repose run
--no-personal` plus a dashboard switch, temporary machines included,
stored on the account, applied automatically) An account has one
machine.nix, a home-manager module for `dev` under the fragment contract.
Each save is a row of `personal_revisions` (migration 0015; 0014 is
I-493's); the newest row is current and an empty text means none. Every project revision
records the personal text it was built with, the account revision it
came from and whether the project had opted out
(`config_revisions.personal`, `personal_revision_id`,
`personal_opt_out`), so a rebuild, a restore or a fork reproduces it.
`projects.personal_opt_out` is the opt-out.

The wire and the flake. `Build` gains `personal` (field 7) and `Error`
gains `personal_line` (field 4). hostd writes a non-empty personal text as
`personal.nix` beside `fragment.nix`; the flake's `guestSystem` passes it
as `personalPath` when the file exists, and `contract.nix` imports
`repose.personal` into dev's home-manager configuration before the
fragment, under the same rules (the `repose.system` allowlist, the
`repose.overlays` pre-pass with the personal overlays first). Lists merge;
two definitions of one single-valued option fail with home-manager's
conflict, which `MapEvalError` summarises as `option 'X' is set in both
machine.nix and the project's configuration; wrap the one that should
win in lib.mkForce`. Every `personal.nix` in a message reads
`machine.nix`, the name the user knows the file by. A base from before
this entry would ignore the file, so hostd writes it only when the base's
`nix/flake.nix` contains `personal.nix`, and otherwise says `this base
predates machine.nix; building without it` in the build log; the next
base update carries it. The eval cache key (I-405) adds the personal text
only when there is one. No personal text is exactly the build before:
`home-manager-generation.drv` for the documented example fragment is
the same store path on this branch and on its parent
(`qxx4vgpx...-home-manager-generation.drv` on both).

Where a change goes. A save gives every live project of the account that
has not opted out and is not `error` or `destroying` a revision with its
current fragment and the new text, running projects first and at most
50 per save (plans allow far fewer; a project past the bound gets it with
its next change), each with a `build` op queued behind whatever the
project is doing and planned as build plus apply: a running machine
switches in place, a stopped one keeps the revision built for its next
start (the pending-revision path, I-147). A personal build that has not
started takes a newer text instead of a second op queueing behind it. A
start of a stopped machine whose only open ops are personal builds is not
refused: the builds not started are queued again behind the start, so the
machine boots on what it has and switches once built. A project
configuration change is not refused either (stage 1's run sends
`repose.nix` seconds after its own machine.nix push, and a new machine's
deferred build is still running then): a personal build not started is
superseded, since the new revision carries the account's current text
too, and one already building is queued behind. Ops queued in one
transaction now get `clock_timestamp()` as `created_at` and the loop
orders ties by id, because the start and the build queued after it shared
`now()` and interleaved in the test. A project configuration change takes
the account's current text; a base bump keeps the applied revision's, so a
machine.nix that never built cannot hold a security base back. A failed
personal build leaves the machine on its revision and records
`personal_failed` (`machine.nix did not apply to SLUG, which keeps its
current configuration: <error>`), which notifies. That answers the
proposal's open question as it proposed. The secret refusal covers the
text (`machine.nix contains the value of secret NAME`), at build time
against the project's own secrets, as for a fragment.

Never block. A new machine's first revision carries the account's text.
At its build phase the api first asks whether the host already holds the
combined closure (the I-160 query, now matching the personal text too);
on a hit the machine boots on it with no build at all. On a miss the
create is turned into a create on the project layer alone (a copy of the
revision without the personal text, which for a new project is the
default fragment and is nearly always on the host) and the combined
revision's build and apply are queued behind the create once
`create_guest` succeeds. The argument is in the numbers already
measured: a create that reuses a closure reached `running` in 7 s on
host-01 (StartGuest to running, r20261003-1), while a combination the
host has not seen costs 16 s warm and 45 s cold (evaluate 7.9 s, fetch
21 s, glue 12 s, switch 3 s, proposal "Build time"), unbounded for a
package that builds from source. Building first on a miss would put that
in front of the boot. An eval cache hit alone is not enough to build
first: it saves the evaluation, not the fetch. The reusable closure is
the one signal that the build costs nothing, so it is the one that skips
the deferral. The cost of deferring is a first minute on the project
layer; new shells see the personal packages after the switch.

The CLI. `repose config --global show|edit|add|remove|apply` is one
persistent flag on the existing noun and refuses `--project`. The laptop
copy is `~/.config/repose/machine.nix`; `machine.nix.state` beside it
records the account revision and the text's SHA-256 that the last push or
pull left both sides on. `repose run` reads the account's copy beside its
resolve and, when the laptop copy changed since the state, pushes it with
`base_revision_id` set to the state's revision: before a create, so the
new machine's first revision has it, or once an existing machine runs, so
its build never holds up the start. An unchanged laptop copy takes a copy
saved on the dashboard since; both changed is one refusal line naming
`repose config --global apply` and `show`, and the run goes on. `PUT
/me/config` refuses a stale `base_revision_id` with 409; the dashboard
sends the revision its editor loaded. `add` and `remove` edit the file's
`home.packages = with pkgs; [ ... ];` list and refuse a file without one.
`repose run --no-personal` creates a machine opted out or opts an existing
one out, for good; turning it back on is the dashboard's switch (a CLI way
back is left to the owner, below). `logout --purge` keeps machine.nix,
which is the user's file.

Precedence (stage 3). With a machine.nix on the account and the machine
not opted out, the tool carry leaves out the laptop's global tools; with
a `repose.nix` at the checkout root it leaves out the commands the
project's scripts run. The node, ruby and java pins still travel: they
are declarations, not guesses. `repose scan` says which half it skipped
and why, asking the account when logged in and going by the laptop copy,
said so, when not.

The dashboard: a machine.nix section on the Account page (editor, save
with the loaded revision as the base, a conflict banner that offers to
load the account's copy, the projects that opted out) and a switch on
each project's Config page.

Not done here: stage 4 (build on save with a GC root per account,
rebuild ahead of a base publish), stage 5 (a first machine.nix written
by `repose scan`), and stage 1 (`repose.nix` from the repository on
run, another branch). Left to the owner: whether `repose run` should
take a way to turn the layer back on for one machine (for instance
`--personal`), and whether `repose sync` should take `--no-personal`
too.
**I-480. One machine holds several checkouts: `repose run --on PROJECT`.**
(multi-checkout, 2026-10-04; extends I-368, I-253) Until now the CLI
treated a machine and a checkout as one thing: every `repose run` in a new
repository made a new project. On the flat plans a project costs running
memory, not a slot, but a Solo user with agents in three repositories at
once still needed three machines, although a small guest carries three
agents that spend most of their time waiting on the model API. The owner
chose (2026-10-04) to keep "project" meaning the machine and let a second
folder join one: `repose run --on PROJECT` in that folder claims
`~/<folder>` on the machine (the name made safe as for I-368, else
`<folder>-2`, ..., skipping the checkout, its worktrees, names already
taken and non-empty directories), lists it in `~/.repose/checkouts`,
syncs into it with the folder's own remote, and records the folder under
a new `checkouts` key of `projects.json`, never in `by_dir`, so a CLI
from before this entry makes the folder a machine of its own instead of
syncing it over the checkout. Resolution reads `checkouts` first;
`PROJECT:CHECKOUT` names one from anywhere. The CLI carries the checkout
name on its ssh target, so sync, exec, ssh, cp, code, the git remote,
the drop handler and agent windows all work in it. Agent windows there
are `<checkout>/<agent>` (`:` would be read by tmux as the session
separator in a target), and an attach with no window opens the
checkout's most recently used window, else a new shell window
`<checkout>`. The early and boot probes, which read the machine's own
checkout, are skipped for such a folder, and the attach fast path takes
the slow path. Shared on purpose and documented: secrets and logins,
the machine's `repose.nix`, ports, disk, snapshots, undo and destroy.
Known gap: the `.env` carry keeps one marker and one paths file per
machine, so runs from two folders by turns resend each folder's set, and
the git carry writes one flattened config per machine, so the identity
is the one of the folder that ran last.
guestd and `repose-checkout` are unchanged and know only the checkout.
The flag name was chosen over a `repose checkout` noun (cli-surface
rule: no new top-level command when a flag of an existing one does).
Fixed on the way: the drop handler looked for laptop files in
`~/<slug>`, which is the checkout only on machines from before I-368;
it now applies the shared rule. Tests: `TestRunOnAddsAnotherCheckout`,
`TestRunOnRefusals`, `TestClaimCheckoutNames`,
`TestProjectsCacheCheckoutsRoundTrip`, `TestWindowLabel`. Proposal:
`docs/proposals/2026-10-04-several-checkouts.md`.
**I-484. A command that worked says what happened and stops; the next
command is for failures and refusals.** (07, quiet-success, 2026-10-04;
narrows I-153, replaces the `ls` and `status` lines of I-351)

The problem. I-153 gave every error a next command: you are stuck, so the
CLI tells you how to get unstuck. The same suffix then spread to output
where nothing went wrong. By 2026-10-04 `start` ended with "`repose
attach X` to get in", every `stop` with "`repose rm X` to stop that",
`ls` printed a line per temporary machine under the table ("`repose keep
X` keeps it"), `ls --destroyed` added two lines on how to read its own
table, `fork` closed with a line of commands, `repose secrets` and `run`
pointed at `repose secrets choose`, and a `run` in the home directory
offered two ways to make other machines. The owner read `repose ls` with
two temporary machines and called it "such handholding".

The failure mode. Each hint looks harmless on the day it is written: one
feature, one line, reviewed alone, and kind to a first-time user. The
cost lands on you the hundredth time you run the command and read the
same lesson, in output whose job is to show state. Listings collect the
most, because each feature adds its own line under the same table, so
the table you asked for arrives followed by text you didn't. Scripts
that count or grep lines see the extra lines too. Review doesn't catch
it, because a reviewer reads one diff, and in one diff the hint is a
single friendly line.

The rule. A success line or a listing says what happened, or what is,
and stops. A next command goes where you are stuck: a failure, a
refusal, a warning that your work did not go, or a change that does
nothing until you act (a built kernel change waiting for a restart).
Facts that matter stay, without the command: `Disk is still billed.`,
`Its final snapshot is kept for 30 days.`, `Using boxd, the machine last
made in this directory.` State that belongs to a row is a column: a
temporary machine's time left is `ls`'s `LEFT` column, shown only while
one is listed; `status` says `temporary: destroyed in 5h`. The docs
teach the commands.

Changed: `start`, `stop` (and already stopped), `rm` (both paths),
`restore`, `snapshots restore`, `resize`, `fork`, `browser`, the idle
lines in `ls`, `status`, `run` and `attach`, `ls --destroyed` and `--all`
(no footer; the line for a name in use stays, since the plain `restore
NAME` is wrong for that row), `questions` (`options: yes|no` in place of
a `repose reply` line; a waiting terminal is `claude on todo-app`),
`secrets` list, `run`'s logins line (nothing before a choice, `Left on
your laptop: env, gh.` after), the home-directory `Using` line, and `ls`
with no projects (`No projects yet.`).

Enforced by `TestSuccessOutputNamesNoCommand`
(`internal/cli/quiet_success_test.go`). It parses the CLI's sources and
fails on any string that names a `repose` command unless the string is
an argument of a failure call (`exitf`, `opFailed`, `withNext`,
`Errorf`, `errors.New`), cobra help or flag text, inside a known error
builder, or listed in `quietAllowed` with its reason. Stderr is not
exempt: `run` and `attach` print notes there, and a trial merge of the
queue on 2026-10-04 showed a success note on stderr ("Applying repose.nix
... in the background. `repose config show --revisions` shows when it is
done.") passing a first version of the test that exempted it. An entry that no longer matches fails the test too. What it
cannot see: a command assembled at run time (the destroy lines built
theirs with `restoreHint`), so a reviewer still reads every new success
line (CHECKLIST, "For every change").

Alternatives. An environment variable or flag that turns hints off:
rejected, since the default is what everyone reads. Hints shown only
the first time, remembered on the laptop as the idle note is: rejected
for now; it adds state to deliver a lesson the docs already give.

**I-485. Trust the reader: say what is true, where they look for it,
once, and stop.** (all surfaces, quiet-success, 2026-10-04; generalizes
I-484 and LANDING.md's "No hand-holding" of 2026-09-27)

The pattern. Twice in a week the owner cut text for the same reason:
the landing's explanatory captions and paragraphs (2026-09-27), and
the CLI's next-command hints on success lines and listings (I-484).
Both were written for an imagined beginner who is lost. The person who
actually reads them is not lost: you have run `repose ls` many times,
you came to a docs page for one answer, you looked at the landing to
see what repose is. Each line was added on its own and looked helpful
on its own. Together they cost every reader attention on every read,
and they tell you the product doubts you can find your way.

The rule. Help the reader asked for is welcome: `--help`, a docs page,
a tutorial, an error (being stuck is asking). Help nobody asked for is
cut. Say each fact once, on the page or in the line where the reader
looks for it, and link to it from elsewhere.

The test for any line, in any surface: picture the user on their
fiftieth visit. If the line gives them nothing, cut it, unless it
prevents a loss they cannot undo or unblocks them.

By surface:

- CLI output: I-484. A result line reports the result; a next command
  goes on failures and refusals; per-row state is a column.
- Docs: a page delivers what its title promises. No narrating what the
  reader is about to see ("A tab opens and you're looking at..."), no
  reassurance ("Nothing to type", "don't worry", "that's it"), no
  "next, read X" endings, no restating the previous paragraph. A fact
  lives on one page; other pages link to it. A tutorial walks through
  steps because you opened it for that, and still skips what the screen
  already shows.
- Copy (landing, dashboard, emails): a heading is its title; one
  sentence of fact; no instructions for reading the page, no "get
  started in seconds", no next step in a success toast. An empty state
  says so, and teaches how to add one only when the screen has no way
  to (DESIGN-LANGUAGE.md "States"). An email the user did not ask for
  states the fact, the date or amount, and the one action when they must
  act. LANDING.md "Copy" has the landing's specifics.
- Agent guide: the same, read by an agent: facts and commands it needs,
  no encouragement.

Enforced, for future text as much as today's, in three places:

- Tests. `TestSuccessOutputNamesNoCommand` (I-484) and
  `TestCLIReassures` in `internal/cli/quiet_success_test.go`; in
  `apps/web`, `docs.test.ts` holds every docs page and `copy.test.ts`
  every route, component and email template to the phrasings in
  `src/lib/unasked.ts` (reassurance, narrating the screen, "next, read
  X"). The Go list is the TypeScript list's twin; change both.
- Gates. `just done-check`, which every workstream runs before it
  reports, runs all four. `ops/dev/release-queue add` runs the CLI pair
  on a branch that ships as `cli` and the web pair on one that ships as
  `web`, and refuses to queue on a failure.
- Instructions. CLAUDE.md "Trust the reader", the workstream preamble
  (docs/workstreams/PROMPTS.md), CHECKLIST "For every change" and
  RELEASE.md integrate step 4.

A grep only catches phrasings; prose judgement stays with review.
Alternatives: a style guide page of examples alone, rejected, since the
two cuts this week both passed review that had LANDING's rule in hand;
the tests stop the phrasings, and the rule in CLAUDE.md reaches the
agents who write the text.
**I-481. One opencode plugin serves version 1 and OpenCode 2, and a base replaces only its own earlier copies.**
(opencode-herdr, 2026-10-04) OpenCode 2 (2.0.22, 2026-10-02) ships on a
channel of its own (`opencode.ai/v2/install`, npm `@opencode/cli`); the
GitHub "latest" release and the default installer are still 1.18
(1.18.34). Version 2 does not run version 1 plugins, drops
`session.idle`/`session.error` from its event stream (a turn ends with
`session.execution.succeeded` or `session.execution.failed`; checked by
logging every event of a real 2.0.22 run), and runs plugins in a
background service the repose wrapper never starts, so
`REPOSE_HOOK_AGENT` is absent there and `repose-hook` refused the
payload ("no agent given"). Decided: (1) the guest keeps 1.18 as
`opencode` (bumped to 1.18.34, bug fixes only); a user may install
OpenCode 2 beside it as `opencode2` (`/docs/agents#opencode-2`). (2)
`opencode-plugin.js` default-exports `{id, server, setup}`, the shape
OpenCode's migration guide gives for one package serving both (version
1 reads `server` from 1.18.29 on, version 2 reads `setup`), maps V2's
`session.execution.succeeded` (summary: the last `session.text.ended`
text), `session.execution.failed` and `permission.asked`, and calls
`repose-hook --agent opencode`; the shell shim takes `--agent` too.
(3) `repose-agent-setup opencode` replaces `repose.js` when its sha256 is
one an earlier base installed (`opencode_retired`, today the 2026-09-19
file) and leaves any other file, so an existing guest gets the new
plugin and a user's edit survives; until now an installed plugin was
never replaced. (4) A user unit, `repose-agent-hooks`, runs that at
login, so an OpenCode 2 that never went through the wrapper still finds
the plugin. Alternatives: ship OpenCode 2 as the guest's `opencode`
(breaks every version 1 plugin a user has, and its line is not the
default upstream yet); ship both binaries (another 200 MB in every
closure for a minority, and it updates daily); a second plugin file for
version 2 (two files to keep in step, and version 1 would fail to load
it). Login sync is unchanged: it copies `auth.json`, which version 2
imports once into `opencode.db` and then stops writing, so logins made
in OpenCode 2 on the laptop do not travel; the docs say so. Checked on
temporary guest `oc-herdr`: 1.18.32 TUI under herdr sent needs_input
and completed; `opencode2 run` through the service sent completed
(summary 431 bytes) and needs_input for an `external_directory` ask.
*Revisit when:* upstream makes 2.x the default channel, at which point
the guest's `opencode` moves to it and the version 1 half of the plugin
stays for one release.

**I-482. herdr is documented, not packaged, and gets no boot unit.**
(opencode-herdr, 2026-10-04; settles the herdr boot unit left open by
the 2026-10-03 client report) The report proposed a guest user unit to
start `herdr server` after `repose start`, because `herdr machine
status` showed the machine as stopped. Tested again: an open herdr
client re-runs `herdr remote-client-bridge` within about 30 seconds,
which starts the server, restores `session.json` and resumed opencode
with `--session <id>`. Only the status command, which never starts
anything, reports "stopped" meanwhile. A unit would start a server
nobody is watching and that herdr starts anyway. Packaging herdr in the
base does not remove the install prompt either: herdr takes a remote
binary only when its version matches the laptop's, and herdr ships
several releases a week (nixpkgs 0.9.3, the guest's pinned nixpkgs 0.9.0). Decided: a
tutorial, `/docs/tutorial-herdr`, with the limits it keeps (tmux-only
`repose status` and `repose run`, the idle notice, the shared `Ctrl-b`,
restore rolling back `~/.config/herdr`). The "Bad owner or permissions
on /etc/ssh/ssh_config" failure happens only where the client is itself
a guest (the store's files are owned by `nobody`); `[remote]
manage_ssh_config = false` avoids it and was checked from kanali.
*Revisit when:* herdr can run a matching server from a binary on PATH
across versions, or users ask for `repose status` to see herdr panes.
*Superseded by I-501 (2026-10-05): both premises failed the live test;
herdr ships in the base, a boot unit starts it on projects that choose
it, and guestd reads its socket.*

**I-487. The guest's Codex is a complete Codex package.** (2026-10-04)
On base 2026.10.04.1, on a new project and on kanali, `codex` stopped at
"this CLI has no complete local package; install a packaged Codex CLI or
use the standalone installer". `codex exec` and `codex --version` still
ran, which is why the bump check (`--version`) passed. Codex 0.157.1's
interactive `codex` starts a background app-server daemon (feature
`daemon_auto_start`; upstream seems to have turned it on for this version
after release, since I-426's own test on 2026-10-03 reached the TUI).
`codex app-server daemon start` copies the package holding the running
executable into `~/.codex/packages/app-server-daemon` and refuses unless
that package has `codex-package.json`, `bin/codex`,
`bin/codex-code-mode-host`, `codex-path/rg` and `codex-resources/bwrap`,
with the running executable as the manifest's entrypoint. `codex.nix`
installed the binary alone and wrapped it with makeBinaryWrapper, so the
executable was `bin/.codex-wrapped` with no manifest beside it. The
package is now laid out the way upstream's `codex-package-<target>`
asset is: the manifest (written from `versions.json`), the real binary at
`bin/codex` with no wrapper, the code-mode host, and copies of nixpkgs's
`rg` and `bwrap` (the daemon refuses a link out of the package; Codex puts
`codex-path` on its own PATH, so the PATH wrapper is gone). The voice host
and bundled zsh of the full asset are left out: the daemon does not need
them. The derivation's install check runs `codex app-server daemon start`
with a scratch `CODEX_HOME` and fails the build unless it reports
`started`; with `bwrap` left out it fails with "local Codex package is
missing codex-resources/bwrap", so a later layout change upstream breaks
the bump instead of reaching guests. Users need do nothing: the next base
update switches the package in place; until then `codex --no-daemon`
starts the TUI (troubleshooting page). Known consequences, from upstream
and not changed here: the daemon's copy costs about 370 MB in
`~/.codex/packages` per Codex version, and the daemon starts an updater
(`codex app-server daemon pid-update-loop`) that follows upstream's
production channel, so the daemon that runs turns can move past the
version `versions.json` pins. The owner chose (2026-10-04) to keep the
daemon on, as upstream ships it: `codex agents`, `codex queue` and remote
control work without a manual start, at the cost of the disk and the
drift above. Pinning (`features.daemon_auto_start = false` in
`/etc/codex/config.toml`) stays the way out if either becomes a problem. Checked on kanali: the built package's TUI in tmux reached
the sign-in screen with a scratch `CODEX_HOME` where the old package
printed the error; `codex exec` ran `cat f.txt` and answered with its word.

**I-489. `run` and `sync` apply the checkout's `repose.nix` without being
asked.** (machine-nix-repo, 2026-10-04; stage 1 of
`docs/proposals/2026-10-04-your-machine-nix.md`) A `repose.nix` committed
at a repository's root did nothing until the user ran `repose config
apply`, so a new machine for that repository booted on the bare base. The
owner chose (2026-10-04) that a project's `.nix` file configures its
machines automatically. After a run's or sync's sync, the CLI sends the
root `repose.nix` of the project's own checkout (`checkoutOwnsProject`,
or, on a temporary machine, which has no remote, the checkout the run
just synced into it) as the project's fragment
through the existing `PUT /projects/{id}/config`, does not wait for the
build, and prints one line naming the revision (no command in it, I-484). The api skips only a
fragment that is already applied, so the CLI keeps
`~/.config/repose/repo-config.json`, per project the file's SHA-256 and
the revision it made: the same file is not sent again while that
revision is building, built or applied, and after it failed the run
names the error instead of building it again (`repose config apply`
still retries on demand). A refusal from the api (`invalid`: syntax, a
secret in the file) and a failed send are warnings; the run goes on. A
run with `--no-sync`, or outside a repository, sends nothing, and another
repository's `repose.nix` (another checkout of the machine, I-480, or a
run by name from elsewhere) never replaces the machine's configuration.
With the file in the repository, the file is the configuration: a menu
or dashboard change is replaced the next time the file is sent, and the
docs say so. Tests: `TestRunAppliesRepoConfig`,
`TestRepoConfigOnlyFromTheProjectsCheckout`.
**I-486. The CLI marks the folder it starts Claude Code in as trusted.**
(claude-trust, 2026-10-04; found by the flake-in-root test session,
`reports/Flake in root DevX.md` item 4) On a new checkout, Claude Code
2.1.283 opens with "Quick safety check: Is this a project you created or
one you trust?", cursor on "No, exit". `repose run "prompt"` waited for
the pane to settle, typed the prompt and Enter into that dialog, and
Claude Code quit while `run` printed `Ready`; seen twice on flk-devx,
reproduced here with a scratch `HOME`. The flag that skips it is
`projects["<folder>"].hasTrustDialogAccepted` in `~/.claude.json`, keyed
by the resolved path (checked on 2.1.283: set it and the dialog is gone).
Now `startAgentWindow` puts `claudeTrustScript` in front of claude's
`tmux new-window`, in the same SSH command: it resolves the window's
folder with `pwd -P` and sets the flag with jq when it is not already
true, replacing a false that an earlier refusal left, keeping every other
key, atomically, and never touching a file that is not valid JSON. Only
folders repose starts an agent in get it (the checkout, `--worktree`
directories, and with I-480 another checkout, since the script takes the
folder the window is given). A user who types `claude` in another folder
still sees Claude Code's own dialog. The laptop's `~/.claude.json` is
never carried (I-196), so no laptop trust reaches the guest and the carry
cannot undo this write; repose-agent-setup's edits of the same file keep
`projects`. As defence in depth, `waitPaneIdle` looks for the dialog's
lines in the settled capture (and at its deadline) and returns
`agentDialogError` instead of typing; `run` then says the prompt was not
typed and attaches, or with `--no-attach` exits 1 naming `repose
attach`. I-283 turned down matching a dialog's text; this match is
narrower than what it turned down, since it never presses a key, and when
a Claude Code release rewords the dialog it misses and the CLI behaves
as before, which the pre-trust should make rare. A CLI change, so it reaches every base at
once. Race: a Claude Code already running in another window can rewrite
`~/.claude.json` between jq's read and the rename; the same window
exists for repose-agent-setup's writes at each start, and the dialog
check covers a lost flag. Tests: `TestClaudeTrustScript`,
`TestAgentWindowCommandTrustsOnlyForClaude`,
`TestPromptNotTypedIntoTrustDialog`, `TestPaneShowsDialog`. Live on
production with a CLI built from the branch (2026-10-04): v0.1.29 on a
new temporary machine printed `Ready` with no claude window left; the
branch CLI on a new machine left the flag true for the checkout and the
worktree, and the prompt sat in Claude Code's input; with jq hidden and
the checkout's entry removed, it exited 1 with the window still on the
dialog. Seen on the way: Claude Code 2.1.283 did not ask in a
`--worktree` directory whose own entry was missing while the checkout
was trusted, so a worktree seems to inherit the repository's trust; the
flag is written for it anyway.
*Rejected:* setting the flag in repose-agent-setup from the wrapper
(it reaches machines only with a new base, and the wrapper cannot tell a
folder `run` chose from one the user changed into); trusting `/home/dev`
as a parent (trusts every folder on the machine); answering the dialog
with keys (its wording has changed between releases, and on 2.1.283 the
cursor starts on "No, exit", so a key sent blind can quit Claude Code).
**I-491. A kept ssh master is reused only after it answers, and an attach
keeps the access token fresh.** (attach-check, 2026-10-04; amends I-223)
The owner reported that reattaching after an hour or more took seconds.
Two causes, read from the code. First, I-223's fast path took a master
that answered `ssh -O check` as proof the guest answers, but the check
asks only the master's local socket: after the laptop slept or changed
networks the master process is still up with a dead TCP connection, so
`attach` printed "Connected", opened its session on it and hung until
ssh's keepalives gave up (`ServerAliveInterval 30`, count 3: up to 90
seconds), then I-469's reattacher connected afresh. `masterAlive` now
also runs `true` over the master within 2 seconds
(`masterProbeTimeout`); on a timeout it sends `-O stop` (stop, not exit,
so sessions already on a slow but healthy master, another terminal's
attach, keep running) and the caller takes the cold path. This costs
one multiplexed session (two round trips) on every warm `run` and
`attach`, which I-223 had made free; a dead master costs 2 seconds
instead of up to 90. Second, with no master left, the first api call
refreshed an access token that had expired during the attach (Logto's
default 1-hour lifetime; the configured value is not in this repository), a round trip to Logto before anything else. An
attached `run` or `attach` now runs `KeepFresh`: once a minute, against
the wall clock because a sleeping laptop stops timers, it refreshes the
token when under 10 minutes remain, backing off to 15 minutes on
failure, and ends on logout instead of writing the session back. Every
refresh now holds `credentials.json.lock` and first takes a newer token
another process wrote, or its rotated refresh token, so two attached
terminals do not spend the same refresh token. Not changed:
`ServerAliveCountMax` (a gateway interface change, left for later).
Tests: `TestMasterAliveDeadConnection`, `TestMasterAliveHealthy`,
`TestKeepFreshRefreshesBeforeExpiry`, `TestKeepFreshStopsAfterLogout`,
`TestTokenSourceTakesAnotherProcessesRefresh`,
`TestTokenSourceRefreshesWithRotatedToken`.

**I-488. A fragment's session variables reach every process, and your own shells load the flake dev shell agents get.**
(machine-nix-shell, 2026-10-04; stage 0 of
`docs/proposals/2026-10-04-your-machine-nix.md`, items 2 and 3 of
`docs/proposals/2026-10-04-flake-in-root.md`; amends I-259 for the
user's shells) Two gaps found in the flake-in-root test. First,
`home.sessionVariables` and `home.sessionPath` in `repose.nix` built and
did nothing: home-manager writes them to `hm-session-vars.sh`, which only
a shell home-manager manages sources, and nothing on the guest does. The
`/docs/config` example and the menu's Java and .NET entries
(`JAVA_HOME`, `DOTNET_CLI_TELEMETRY_OPTOUT`) rely on it. `contract.nix`
now copies them, as evaluated (so a home-manager module the fragment
enables counts too), into NixOS `environment.sessionVariables`: a
fragment value at priority 90 wins over the base's own (`EDITOR`), and
sessionPath entries go before the base's `PATH` with `mkBefore`. That
reaches PAM and `/etc/set-environment`, as the base's variables do (SSH
sessions, login and interactive shells, the user manager). Two more
places cover what PAM and a stale tmux server miss. The values are also
written to `/etc/repose/session-vars.sh` in home-manager's quoting,
sourced by `/etc/profile.d/repose.sh`, which every agent wrapper and
`repose exec` source: an agent started by a tmux server that predates an
in-place apply gets them, and `$OTHER` references expand as in a shell
(PAM expands only `$HOME` and `$USER`). And the activation script that
already pushes `PATH` and `BASH_ENV` into a running tmux server and user
manager pushes these names too, and unsets the ones the previous
configuration had and this one dropped (`/run/repose-session-vars.names`).
home-manager's own `LOCALE_ARCHIVE_2_27`, which names the archive NixOS
already sets, stays out so an empty fragment changes nothing. `PATH`
(use sessionPath), `BASH_ENV`, `ENV`, `REPOSE_ENV_GEN` and `REPOSE` are
refused, and so is a double quote in a value, which PAM cannot hold;
each fails evaluation with the reason, which hostd shows as the first
failed assertion. `home.sessionSearchVariables` other than `PATH` are
not carried. Second, an agent in a checkout with a `flake.nix` and no
`.envrc` had the flake's tools and the shell next to it did not. Every
interactive bash now sources `/etc/repose/devshell.sh` and redefines
direnv's `_direnv_hook` to call `_repose_devshell_prompt`. Where direnv
finds an `.envrc` (here or above), or no `flake.nix` naming a dev shell
lies between the folder and `$HOME`, it is direnv's export unchanged, so
an `.envrc` still needs `direnv allow` in your shell and a denied one
stays out. Otherwise it exports from the generated `.envrc` the agent
wrapper uses, which the two share through `_repose_devshell_shadow`.
Entering prints `repose: loading the dev shell from <dir>/flake.nix (the
first load can take minutes; Ctrl-C skips it until you leave the
folder)`; leaving unloads it, as direnv does. The rule is the agents', so
it holds in any checkout or worktree under `$HOME`, `~/.repose/checkouts`
included. On a cold load the prompt waits, as an agent does, rather than
building in the background: the agent window or the plugin installer has
usually built it within seconds of boot, a background build would race
nix-direnv's cache, and knowing whether the cache is warm means reading
nix-direnv's internals. Ctrl-C works because direnv's export handles
SIGINT itself, and direnv records the interrupted load as loaded, so the
prompt does not retry until the folder or `flake.nix` changes. A prompt
inside a loaded checkout costs one direnv export, as before (19 ms
measured on kanali); the root and generated directory are cached in two
shell variables. Only bash: the guest's login shell. Checked on kanali
with the loader's store paths substituted: cold load, Ctrl-C, re-entry,
warm load, unload on `cd ..`, an `.envrc` blocked, allowed and denied,
then removed. The `guest-devshell` VM test asserts the fragment variable
in an SSH command, a login shell, an agent window and a bash its command
runs, and the flake's tool in a tmux shell window plus its unload; it
builds but was not run (the nested VM does not boot here).
`fragment-contract` gains three refusals.
**I-492. The project page shows the machine: its size spelled out, and
charts of its minute samples over an hour, a day or a week.**
(machine-monitor, 2026-10-04; amends the "Deferred" graphs of
features/status-and-logs.md and DESIGN-LANGUAGE.md's "no chart
component") A user saw a session lag for seconds while builds held every
core and had only `htop` inside to look with; the dashboard said `large`
and nothing about what that means. `GET /projects/:id/samples?window=`
serves `meter_samples` and `proc_samples` for one project, bucketed to 60,
300 or 3600 s so no window passes 288 points, CPU as a share of the
row's own class, and the eight busiest process names. The data is the
minute sampling the privacy policy already names and the platform
already stores, so the feature adds no storage, no collector and no
dependency: the cost is one indexed range scan per page view a minute,
and an AWS host later adds nothing, since samples already travel hostd
to api. The rule that request paths never read these tables gains this
one exception (db-schema.md). The chart is `UsageChart.svelte`: one ink
series per chart, small multiples rather than two scales on one axis.
The cards carry figures and labels only; what each measure means is in
`/docs/machine` (the owner cut the explanations and the table view from
the first draft as hand-holding). *Rejected:* per-tenant Grafana or a second
time-series store (the metric registry refuses `project_id` labels on
purpose; tenant auth in front of Grafana; the owner's monitoring server
becoming part of the product); a third-party monitoring service (a new
processor in the privacy policy, a vendor bill per host, for data we
hold). *Revisit when:* someone needs finer than a minute (I-494's live
view) or longer than seven days.

**I-493. Samples carry CPU pressure inside the guest, the host CPU wait
of its hypervisor, and memory in use as the guest sees it.**
(machine-monitor, 2026-10-04) CPU percentage says the cores were busy,
not that anything waited; a laggy session is tasks waiting. guestd adds
the `/proc/pressure/cpu` "some" total and MemTotal less MemAvailable to
`SampleResult`; hostd turns the total into a delta per sample (capped at
300 s, the I-446 bound for a guest-written counter) and adds the
`cpu.pressure` "some" delta of the guest unit's cgroup on the host,
which is the guest's steal: time its vCPU threads waited for a host CPU
under the 2:1 oversubscription of R3-2. That one tells a user whether a
slow minute was their own builds or the server. `mem_rss` stays the
billing and capacity figure, but it is what the host backs and never
falls without a balloon, so the chart draws the guest's own figure.
Migration 0014 adds `cpu_pressure_us`, `host_cpu_wait_us` and `mem_used`
(default 0); a row from an older guest has `mem_used` 0, and the route
returns null pressure and memory for it rather than a false zero. The
privacy policy's metering list names the wait time.

**I-494. In the guest, SSH sessions and the tmux server run at ten
times a pane's CPU weight; a live `repose status --watch` is documented,
not built.** (machine-monitor, 2026-10-04) tmux already puts each pane
in a `tmux-spawn-*.scope` of its own and its server in
`repose-tmux-session.service`, and logind puts each SSH connection with
its `tmux attach` client in a `session-N.scope`; the cpu controller is
delegated to the user manager. `CPUWeight=1000` on the server unit and,
by a `session-.scope` prefix drop-in, on every session scope, against
100 per pane, gives the keystroke path the CPU first while builds hold
every vCPU, and caps nothing when the machine is idle. *Rejected:*
`nice` on builds (they are started by agents and users we do not
control); `CPUQuota` on panes (it would slow a build on an idle
machine). The live view would read `/proc` over the user's own SSH,
store nothing and work when the api is down (I-200's pattern); it waits
until someone needs detail finer than the dashboard's minute.
*Revisit when:* a user asks for a live view, or the session still lags
under load with these weights (then: `io.weight` on the same units).

**I-495. The guest's Codex ships upstream's own bwrap.** (2026-10-05)
Amends I-487. On base 2026.10.05 every Codex command that runs in its
sandbox failed with "bundled bubblewrap digest mismatch for
.../codex-resources/bwrap: expected sha256:77360cb7..., got
sha256:8fa81357..." (found by the release live check on e2e-rel1). Codex
uses a `bwrap` on PATH when there is one; otherwise it runs
`codex-resources/bwrap` only after checking it against a sha256 compiled
into the binary, the digest of upstream's `bwrap-<target>` release asset.
I-487 copied nixpkgs's bwrap there. A guest has no bwrap on PATH (the
makeBinaryWrapper that added one went with I-487), so the check ran and
refused it. I-487's own test on kanali passed because it ran from inside
Claude Code, whose wrapper puts nixpkgs's bubblewrap on PATH, so Codex
never looked at the bundled one. `codex.nix` now fetches the
`bwrap-x86_64-unknown-linux-musl` asset at the Codex version, pinned in
`versions.json` under `codex.bwrap` and moved by `scripts/bump-agents.sh`
with the binary, and installs it unchanged (its sha256 is the expected
77360cb7...). The install check also runs `codex sandbox cat probe.txt`
with PATH set to coreutils alone, which goes through the bundled bwrap
the way an agent turn does and needs no login; with nixpkgs's bwrap the
build fails with the same digest error. `codex app-server daemon version`
printing "failed to connect to .../app-server-control.sock: No such file
or directory (os error 2)" is upstream's answer when no daemon is
running (`codex exec` does not start one); after `daemon start` it
prints `"status":"running"`. Users need do nothing: the next base update
switches the package in place; until then a bwrap on PATH (`nix profile
add nixpkgs#bubblewrap`) gets round it (troubleshooting page). The sandboxed check failed on GitHub's runner (2026-10-05, run 37311472836) after Codex had accepted the bwrap: the runner refuses bwrap's loopback setup ("bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted"). The check now fails on a digest mismatch or any other error, and passes with that output printed when the only failure is bwrap being refused a namespace operation.

**I-496. A base switch never restarts the tmux session unit.**
(tmux-no-restart, 2026-10-05; fixes I-494) Base 2026.10.05 added
`CPUWeight = 1000` to the `repose-tmux-session` user unit (I-494). The
switch at the 04:00 UTC sweep saw the unit change and restarted it, as
`switch-to-configuration` does for any changed user unit without
`X-RestartIfChanged=false`; with `KillMode=control-group` the stop ended
the tmux server and every pane, so every agent and shell on the five
guests on that base ended (kanali's journal, 04:00:11Z: "Stopping
repose: project tmux session", then each "tmux child pane" scope).
Earlier bases never changed this unit, which is why no weekly update had
done it. The unit now sets `restartIfChanged = false`; a changed unit
takes effect at the session's next start (a stop and start, or a
restart of the machine). The switch reads the flag from the new unit, so
the base that carries this fix does not restart the session either.
Check `guest-session-survives-switch` fails the build when the built
unit lacks `X-RestartIfChanged=false` (shown failing with the line
removed). The other user units (`repose-tools-carry`,
`repose-npm-registry`, `repose-agent-hooks`) are one-shot jobs that hold
no session. Not covered: a VM test that switches a running guest between
two bases with a pane open; it needs the dev box.

**I-497. Solo costs $20 a month with 100 GB of egress for its first three
months, then $29 with 250 GB, on an account's first subscription.**
(owner, 2026-10-05: "lets make the $20 now $29 in 3 months a reality",
then "lets cut the egress donw too"; research in
`proposals/2026-10-04-solo-at-20.md`, option A) Amends I-289 and I-362
(the price table stands; this adds an introductory price). While Azure
credit pays for the hosts, the price barely changes the burn, and $20 is
the cheapest always-on 8 GB in the market; at $29 after three months the
plan keeps the price set for the host repose ends up on. The $27 a
customer gives up is acquisition cost.
*Mechanics.* `Plan` gains `IntroCents` and `IntroMonths` (Solo: 2000, 3)
in `plans.go`, the one place prices live; PRICING.md states it in one
sentence that `TestPlansMatchPricingDoc` parses. Paddle charges it as a
catalog discount the bootstrap creates once: `flat`, $9 (Solo's price
minus the introductory one), `recur: true`, `maximum_recurring_intervals:
3`, `restrict_to` Solo's price, `custom_data.repose =
intro-solo-2000-3`, found again by that key and that price on a rerun.
Its id is `PADDLE_DISCOUNT_INTRO`, which `Validate` requires while a plan
has an introductory price, so a deploy cannot promise $20 and charge $29.
`POST /billing/checkout` puts `discount_id` on the transaction when the
plan has an introductory price and the account has no `subscriptions` row
in any status (`EverSubscribed`): a cancelled or returning account pays
the full price, so the discount is not renewable by cancelling. Paddle
starts a recurring discount after the trial, so the three discounted
charges are the first three after the free week. Paddle applies a catalog
discount to a checkout only when it is `enabled_for_checkout`; it has an
auto-generated code that is never shown. Because the discount is
restricted to Solo's price, an upgrade to Plus or Pro ends it.
*Record.* Migration 0016 adds `subscriptions.intro` and `intro_until`:
the webhook sets `intro` when the Paddle subscription's `discount.id` is
`PADDLE_DISCOUNT_INTRO` and copies its `ends_at`, so code that reads a
subscription (the gate, the overage tick, `/me`, the admin explain) needs
no Paddle configuration. `Sub.IntroAt(t)` is true while the offer covers
`t` (or before Paddle fixes its end). `Sub.ChargeCents(t)` is the
introductory price then and the plan's price otherwise; the
`trial_ending` and `payment_failed` emails and `GET /billing` use it, so
no email says $29 before a $20 charge.
*Egress.* While the offer runs the allowance is `IntroEgressGB` (Solo:
100 GB), then the plan's 250 GB. `Sub.PlanFor(periodStart)` is the plan
with that allowance for a period, and every egress reading uses it: the
overage line ($0.05 a GB past 100 GB), the hard stop (four times, 400
GB) in the gate and the tick, `usage.egress_included_gb`, `/me`'s
`limits.egress_gb`, `repose-admin billing show` and `explain`. A period
belongs to the offer when it starts before `intro_until`, so the free
week does too. Why: on Azure 250 GB of egress costs $21.75, more than a
$20 month, so the worst case lost money on bandwidth alone; prod measured
28 GB for the whole fleet in two weeks, so 100 GB costs a typical user
nothing and caps that worst case at $8.70.
*Contract, additive.* `GET /billing`: `plans[].intro_price_cents`,
`plans[].intro_months`, `plans[].intro_egress_gb`, `intro_eligible`, and on the subscription
`next_charge_cents` and `intro_until`. The dashboard's plan card shows
$20, "For 3 months, then $29 and 250 GB egress" and 100 GB of egress to
an eligible account; a trial reads
"Trial. First charge of $20 on DATE; $29 a month from DATE." and an
active subscription inside the three months "Active. Renews DATE at $20;
$29 a month from DATE." The landing's Solo card shows $20 and 100 GB of egress with "First 3
months for new subscribers, then $29 and 250 GB egress", since everyone
sees it, a returning account too; and the user docs' pricing page carries the sentence under the
table. The fake api models it, with `intro_used` to make a returning
account.
*Not checked here.* Against Paddle's sandbox: that a transaction with a
catalog discount and a trialing price completes with the discount on the
subscription, and that the subscription's `discount.ends_at` is the end
of the third discounted period. Both are the live check before the price
is shown on a live key. *Rejected:* a separate $20 Solo price that the
api moves to $29 after three charges (one more job that must run on time
for every subscriber, and a missed run overcharges nobody but undercharges
forever); a coupon code the user types (a field in the checkout that
everyone without the code reads as a missed discount); founder pricing at
$20 for life (option B: permanent loss-making seats if the hosts are
still on Azure when the credits end).
**I-498. The landing drops the headline's bar and the star, and links Feedback in the top bar.**
(landing-hero-nav, 2026-10-05; owner) The owner judged the blue bar
under "replicated" ugly: it underlined one word of a two-line serif
headline and left a stripe ending mid-line. The headline now has no bar;
the prices keep theirs. The star shape was Gemini's sparkle and read as
Gemini's logo on a page that is not Gemini's, so it left the shape set
and the footer's row, which now has seven cells. Gemini CLI's mark in
the toolchain box is the same sparkle and stays, since there it names
Gemini CLI. Feedback was linked only in the footer; the top bar now
carries it after GitHub, and it stays on a phone (GitHub hides below
640px). `docs/LANDING.md` follows.
**I-520. pnpm 11's global bin dir is on PATH, and yarn is corepack's.**
(base-languages, 2026-10-05; amends I-227) Base 2026.10.05 carries
pnpm 11.27.0, which links `pnpm add -g` bins into `$PNPM_HOME/bin`, not
`$PNPM_HOME`, and refuses when that dir is not on PATH: `pnpm add -g`
failed with ERR_PNPM_GLOBAL_BIN_DIR_NOT_IN_PATH and `pnpm bin -g` exited 1
(checked on kanali). I-227's list gains `.local/share/pnpm/bin`, directly
before `.local/share/pnpm`, which stays: a project pinning pnpm 10
through corepack (this repository pins pnpm@10.0.0) still links into
`PNPM_HOME`. tools-carry.sh's fallback PATH names both, and tmpfiles
creates the new dir. `command -v yarn` found nothing, though the docs and
agent guide named yarn v1, and I-227's advice (`corepack enable`) fails
with EROFS, writing next to node in the store. The base now has `yarn`
and `yarnpkg` as links to corepack's own `dist/yarn.js` and
`dist/yarnpkg.js`, from `nodejs-slim_24`'s corepack output (`nodejs_24`
has no such output; this one is the `corepack` the system already runs,
so the closure grows by two links). They run the version a
`packageManager` field pins, else yarn 1 (1.22.22 and 4.5.0 checked on
kanali). pnpm stays nixpkgs's. `COREPACK_ENABLE_DOWNLOAD_PROMPT=0`, so a
first run in an agent's window does not wait on a question nobody sees.
yarn itself is fetched from registry.yarnpkg.com, outside the server's
cache; yarn v1's package installs use the `~/.npmrc` registry, so they go
through it. guest-devtools asserts `pnpm bin -g` and where `yarn`
resolves; `yarn --version` needs the network the test VM lacks.

**I-521. `/bin/bash`, `/usr/bin/python3` and `/etc/ssl/cert.pem` exist.**
(base-languages, 2026-10-05) machine.md promises programs from other
Linux systems run as on Ubuntu, but `/bin` had only `sh` and `/usr/bin`
only `env`: a `#!/bin/bash` or `#!/usr/bin/python3` script exited 126
(bad interpreter) and a Makefile with `SHELL := /bin/bash` failed with
Error 127. compat.nix adds tmpfiles `L+` links from `/bin/bash`,
`/usr/bin/bash`, `/usr/bin/python3`, `/usr/bin/python` and `/usr/bin/perl`
to `/run/current-system/sw/bin`, so they follow a base switch; python3
there is I-228's nix-ld wrapper, whose `exec -a` keeps `/usr/bin/python3`
as `sys.executable`. `/bin/sh` and `/usr/bin/env` stay NixOS's. Not
envfs, which resolves any path but adds a FUSE mount to the boot (I-231).
The CPython uv downloads (python-build-standalone) has
`openssl_cafile=/etc/ssl/cert.pem` compiled in; NixOS has no such file,
so `urlopen("https://pypi.org")` from a `uv python install 3.11` failed
with CERTIFICATE_VERIFY_FAILED and returned 200 with the bundle given.
`/etc/ssl/cert.pem` is now the same source as
`/etc/ssl/certs/ca-certificates.crt`. No `SSL_CERT_FILE` or
`NIX_SSL_CERT_FILE` in the session: they override every program's own
choice. Checked on kanali in a bwrap with the same links: the scripts,
the Makefile and the uv Python's https request ran. guest-compat
asserts each.

**I-522. A carried cargo tool gets rustup a default toolchain first.**
(base-languages, 2026-10-05) The base has rustup with no toolchain, and
machine.md asks the user to run `rustup default stable` once. The tools
carry's cargo fallback ran `cargo install` without one, so every crate
from the laptop that nixpkgs lacks failed with "Could not install X".
Before the first cargo install, tools-carry.sh now checks `rustup show
active-toolchain`; with none it installs stable with the minimal profile
and makes it the default, and logs that once. A laptop with
cargo-installed crates is a Rust user's, who would run the same command;
a default the user chose is kept. No first-boot toolchain download for
anyone else. rust-analyzer is rustup's proxy in the base: without the
component, `rust-analyzer --version` loops until "infinite recursion
detected". The component is not added (another download nobody asked
for); machine.md and the agent guide say `rustup component add
rust-analyzer`. guest-tools-carry runs a cargo item against stand-in
rustup and cargo that fail without a default.

**I-523. Python packages go in a venv; pipx gets the nix-ld python3; Tk
comes with a uv Python.** (base-languages, 2026-10-05; extends I-228)
nixpkgs#pipx, which the not-found hint offers, defaults to its own
unwrapped python3.14, so `pipx run --spec numpy` failed with
libstdc++.so.6; `PIPX_DEFAULT_PYTHON=/run/current-system/sw/bin/python3`
is now a session variable and pipx is not added to the base. pip stays
out of the system python: `python3 -m pip`, ensurepip and `uv pip
--system` are refused (externally managed), so machine.md, the
troubleshooting page and user-bin-dirs.nix no longer list `pip install
--user`. `PIP_USER=1` is not set: it breaks pip inside a venv. The docs
say packages go in a venv and CLIs in `uv tool install`. The system
python3 has no `_tkinter`; the CPython uv downloads has Tk. The docs say
so. Adding Tk to the system python (a joined interpreter with
`_tkinter` in lib-dynload, about 13 MB) waits for closure budget;
`withPackages` is ruled out because it breaks I-228's venv links.

**I-524. A carried Ruby with RubyGems 3.7 gets Bundler 2.7.**
(base-languages, 2026-10-05; extends I-265) nixpkgs's ruby_3_4 has
RubyGems 3.7.2 and a default Bundler 2.6.9, which redefines RubyGems
constants: `bundle -v` printed 18 "already initialized constant
Gem::Platform" warnings and `bundle exec` 36. `-W:no-deprecated` does not
hide them and `-W0` hides every warning. With Bundler 2.7 there are none.
After the tools carry makes a pinned Ruby the default, or finds one an
earlier pass pinned already the default, it installs `bundler '~> 2.7'`
into `GEM_HOME` with `--env-shebang` when that Ruby's RubyGems is 3.7 or
newer and the `bundle` it runs is older than 2.7 (ruby_4_0, with RubyGems
3.7.2 and Bundler 4.0.20, gets nothing), logs the version, and on
failure says so once and goes on. Nothing is recorded: the next pass
reads `bundle -v` again, so it installs nothing twice. A
`Gemfile.lock` with `BUNDLED WITH` 2.6 still switches to the old Bundler;
`bundle update --bundler` moves it. No VM assertion: the test VM has no
bundler gem offline. Checked on kanali against nixpkgs's ruby_3_4 (2.7.2
installed, then `bundle -v` printed one line; a second call installed
nothing) and ruby_4_0 (nothing installed). A guest whose Ruby an earlier
pass pinned gets Bundler at the next pass, which runs when the laptop's
tool list changes.
**I-512. The base ships terminfo for Ghostty's and kitty's own TERM.**
(base-shell-terminal, 2026-10-05; extends I-264) `repose run` and
`repose attach` pass the laptop's `TERM` to the guest unchanged
(`internal/cli/run.go` attachTmux), and I-264 names `xterm-ghostty` and
`xterm-kitty` among the TERMs laptops present. ncurses has entries for
alacritty, wezterm and foot but not for those two, so on base 2026.10.05
`infocmp xterm-ghostty` and `infocmp xterm-kitty` failed,
`TERM=xterm-ghostty tmux attach` refused with "missing or unsuitable
terminal: xterm-ghostty" and `TERM=xterm-kitty clear` printed "unknown
terminal type". `nix/guest/base/shell.nix` adds `pkgs.ghostty.terminfo`
and `pkgs.kitty.terminfo` to the system packages: 5,024 and 4,520 bytes,
no references (`nix path-info -rsS` against cache.nixos.org), where
`/run/current-system/sw/share/terminfo` is on `TERMINFO_DIRS`, which
sudo keeps. With them on `TERMINFO_DIRS`, `infocmp` prints
`xterm-ghostty|ghostty|Ghostty` and `xterm-kitty|KovIdTTY`, and
`TERM=xterm-ghostty tput colors` prints 256. guest-base asserts
`infocmp` for the five TERMs and `TERM=xterm-kitty clear`.
*Rejected:* `environment.enableAllTerminfo`, which pulls every
terminal's terminfo output and grows with nixpkgs.

**I-513. A login bash reads ~/.bashrc when the user has no login file of their own.**
(base-shell-terminal, 2026-10-05; makes the `~/.bashrc` advice of I-227
(the PATH entry, "Not covered by a static dir"), troubleshooting.md and
agents.md true) Every tmux pane, ssh shell and editor terminal on a guest
is a login bash. A login bash reads `/etc/profile` (which sources
`/etc/bashrc`) and then the first of `~/.bash_profile`, `~/.bash_login`
and `~/.profile`, never `~/.bashrc`, and `dev` has none of the three. So
a `~/.bashrc` written by nvm, sdkman, conda or the OpenCode installer
was never read: with a temp HOME whose `.bashrc` exported a marker,
`bash -l -i` did not see it and `bash -i` did. The last lines of
`/etc/bashrc`'s interactive part (`programs.bash.interactiveShellInit`
at `lib.mkOrder 2000`, after the base's aliases, starship, zoxide,
direnv and the I-488 dev shell hook) now source `~/.bashrc` in a login
shell when it is readable, there is no `~/.bash_profile` or
`~/.bash_login`, and `~/.profile` does not mention bashrc. A user who
has one of those decides for themselves. Last so that what the file sets
wins over the base: an alias `ll` in `~/.bashrc` replaces the base's.
Nothing is written to `/home/dev`. Checked with the evaluated
`/etc/bashrc` in a temp HOME: the marker, an alias and `HISTSIZE=42`
from `~/.bashrc` arrive in a login shell, and none of them with an empty
`~/.bash_profile` or a `~/.profile` that sources `~/.bashrc`. guest-base
checks a new tmux window and `ssh ... bash -lic`. *Rejected:*
`programs.bash.loginShellInit`, which runs in `/etc/profile` before
`/etc/bashrc`'s interactive part, so the base's init would override the
user's settings; writing a `~/.bash_profile` into the home, which a
machine that has one already would not get and a user's own would
conflict with.

**I-514. Shell defaults: GNU ls, long history, fzf's keys, starship that waits, vi and vim.**
(base-shell-terminal, 2026-10-05) Five settings of the base's bash.
- `ls` is GNU ls again (NixOS's `ls --color=tty`). The base aliased it to
  `eza -al --group-directories-first --no-permissions --no-user`. With a
  stdin that is not a terminal and no path, eza reads its file list from
  stdin, so a `while read` loop that called `ls` swallowed the loop's
  input; `ls -lt` failed with "a value is required for --time" and
  `ls -ltr` with "invalid value 'r'"; `ls -l` showed no mode bits and no
  owner. `ll` is now `eza -al --group-directories-first` (permissions
  and owner kept); `la` and `lt` are unchanged. *Rejected:* a `[ -t 0 ]`
  wrapper, which still differs from ls in its flags.
- History: `shopt -s histappend` and, when unset, `HISTSIZE=100000`,
  `HISTFILESIZE=200000` and `HISTCONTROL=ignoredups`, first in
  `/etc/bashrc`'s interactive part so `~/.bashrc` may change them. A
  machine lives for months and kept 500 lines, and each pane's exit
  rewrote the file with its own history. `ignoredups` rather than
  `ignoreboth`: a command typed with a leading space is still kept.
- `programs.fzf.keybindings` and `fuzzyCompletion`: fzf was installed
  but Ctrl-R was bash's own search. `FZF_DEFAULT_COMMAND` stays unset:
  fzf 0.74's walker already skips `.git` and `node_modules`.
- starship: `command_timeout = 2000`. A cold `starship prompt` in a large
  checkout took 0.86 s and printed `[WARN] Executing command ".../node"
  timed out` above the prompt with the default 500 ms. A user's
  `~/.config/starship.toml` replaces the whole file. And the module
  exported `STARSHIP_CONFIG=<the system file>` when the user had no file
  at the shell's start, so a nested shell (`exec bash`, `nix develop`, an
  editor's terminal) inherited the path and kept ignoring a
  `~/.config/starship.toml` written since. A line before the module's
  unsets it when it still names the system file and the user's file
  exists; the path is computed from the final settings as the module
  does, so it is the same store path.
- `programs.neovim.viAlias` and `vimAlias`: `which vi vim` found
  nothing, while `internal/cli/carry_tools.go` counts both as base
  commands, so a laptop's vim was never carried and a carried
  `core.editor=vim` failed. The wrapper gains two symlinks (384 bytes).
guest-base asserts each in `ssh ... bash -lic`.

**I-515. tmux sends 24-bit colour only to terminals that have it, and sets the laptop's title.**
(base-shell-terminal, 2026-10-05; amends I-264's `/etc/tmux.conf`)
`/etc/tmux.conf` had `set -ga terminal-overrides ",*:Tc"`, so tmux sent
`38;2` to every client and never turned a pane's 24-bit colour into the
nearest of 256 for a terminal without it, Apple's Terminal on macOS 15
and earlier among them. Panes keep `COLORTERM=truecolor` (env.nix), so
programs emit 24-bit colour; tmux decides per client what reaches the
laptop. `*:Tc` is gone, and `terminal-features` gains
`xterm-ghostty:RGB,xterm-kitty:RGB,alacritty:RGB,wezterm:RGB,foot*:RGB,*-direct:RGB`
(terminfo for all of them since I-512). tmux 3.7 also gives a client RGB
when the client's own `COLORTERM` is `truecolor` or `24bit` (checked
with tmux 3.7c: a client with `TERM=xterm-256color` lists `RGB` in
`client_termfeatures` with `COLORTERM=truecolor` and not without it;
`TERM=alacritty` lists it either way). The planned way to carry the
laptop's `COLORTERM` was `ssh -o SendEnv=COLORTERM`, which the guest's
sshd (`AcceptEnv`) and the gateway allow. It does not work: the guest's
PAM environment sets `COLORTERM DEFAULT="truecolor"` and sshd applies it
after the client's variables (a second sshd on this guest with
`AcceptEnv COLORTERM`, `UsePAM yes`: `COLORTERM=foo ssh -o
SendEnv=COLORTERM` gave the shell `truecolor`). So the CLI's attach
command starts with `unset COLORTERM; ` when the laptop's `COLORTERM` is
not `truecolor` or `24bit` (`attachColour`, `internal/cli/run.go`), and
the `tmux attach` client then has RGB only by its TERM. Panes still get
`COLORTERM=truecolor`: tmux 3.7c sets it in every pane it starts, even
after `update-environment` marks the session's `-COLORTERM` (checked: a
new window in a session attached without `COLORTERM` printed
`truecolor`). A base switch leaves a running tmux server on the options
it started with (I-496), so a machine gets these at its next start. `ssh <slug>.repose` and
`repose ssh` are not changed: there is no tmux client on the way, and a
login shell sets `COLORTERM` again. `set -g set-titles on` with
`set-titles-string "#h: #S"` puts the machine and session in the
laptop's tab title. guest-base checks the options of the running
server. *Rejected:* `if-shell` on `TERM_PROGRAM`, evaluated once in the
server and never sent over ssh; dropping `COLORTERM` from the session
variables, which would take 24-bit colour from every pane.

**I-516. An agent's bash -c names the package of a missing command.**
(base-shell-terminal, 2026-10-05; extends I-219 and I-475) The
command-not-found handler was defined only in `/etc/bashrc`'s
interactive part. Agents run each command as `bash -c` with
`BASH_ENV=/etc/repose/bash-env.sh` and got bash's bare "command not
found", while the agent guide says that typing a missing command prints
the package that has it. `bash-env.sh` now defines
`command_not_found_handle` (calling `repose-command-not-found`, then
`return 127`) when `BASH_VERSION` and `BASH_EXECUTION_STRING` are set and
no handler is defined yet. `BASH_EXECUTION_STRING` is set only for a
`-c` string, so `./configure`, a script file and `sh -c` keep bash's
plain message. The block sits before the file's restore of the shell
options and `$_`, so I-475's promises hold: it prints nothing, `$_` is
the caller's, `$?` is 0, xtrace stays off while it runs. Checked live:
`BASH_ENV=<new file> bash -c 'figlet hi; echo status=$?'` prints the
I-249 hint and `status=127`; the same in a script file and in `sh -c`
prints the plain line; `bash -c 'echo $_'` still prints the caller's
`$_`. guest-devtools asserts the three cases.

**I-517. The not-found hint skips test attributes, prefers top-level ones and answers apt, pip and cron itself.**
(base-shell-terminal, 2026-10-05; amends I-219, keeps I-249's layout)
`repose-command-not-found` took nix-locate's first attribute as found:
`apt-get install jq` suggested `nixpkgs#apt`, `yum` and `dnf` suggested
`python313Packages.dnf4`, `vim` listed
`tests.vim.test-all-plugins-have-vimPlugin-true`, and `pip` suggested
`python314Packages.pip`, which installs for another interpreter than the
base's python3 and outside its nix-ld wrapper. Now: attributes that
start with `tests.` are dropped, and top-level attributes come before
nested ones. `apt`, `apt-get`, `aptitude`, `dpkg`, `yum`, `dnf`, `apk`,
`pacman`, `zypper`, `brew`, `port` and `snap` print the not-found line
and the two I-249 lines with `NAME` in place of a package. `pip` and
`pip3` print `python3 -m venv .venv && . .venv/bin/activate` (a virtual
environment, with pip in it) and `uv tool install NAME`, not `uv venv`,
which makes an environment without pip. `crontab`, `cron`, `crond` and
`at` print one line pointing at /docs/machine#scheduled-jobs (I-518)
instead of `mcron`, which has no daemon here. machine.md's example now
shows I-249's lines as the handler prints them, on one line each.
guest-devtools asserts apt-get's and pip's output, crontab's link and no
`tests.` in vim's.

**I-518. A scheduled job is a systemd user timer; the base has no cron.**
(base-shell-terminal, 2026-10-05) No cron daemon is installed, and the
not-found hint offered `mcron`, so a job set up that way never ran. User
timers work: `dev` lingers, so its user manager runs with nobody
attached, user units get the full PATH (I-227), and unit files in
`~/.config/systemd/user` are in `/home/dev` and survive a stop. A
transient `systemd-run --user --on-calendar` unit lives in `/run` and is
gone after a stop, so the docs show unit files: `NAME.service` with
`Type=oneshot` and `ExecStart=/run/current-system/sw/bin/bash -lc
'CMD'` (a login shell, so the job gets the secrets and the project's
variables), `NAME.timer` with `OnCalendar=daily`, `Persistent=true`
(a run missed while the machine was stopped happens at the next start)
and `WantedBy=timers.target`, enabled with `systemctl --user
daemon-reload && systemctl --user enable --now NAME.timer`. machine.md
"Scheduled jobs" and the agent guide say so. With I-521's link in
place, both write `ExecStart=/bin/bash -lc 'CMD'`, the same bash.
*Rejected:* installing cronie, a second scheduler beside systemd's with its own environment.

**I-519. home.shellAliases from machine.nix or repose.nix reach every shell.**
(base-shell-terminal, 2026-10-05; amends I-488 and I-490) `/docs/config`
and `docs/features/config-examples/personal/machine.nix` show
`home.shellAliases`, and no alias appeared: home-manager writes aliases
into the `~/.bashrc` it manages, and its `programs.bash.enable` is off on
the guest (`nix/guest/microvm.nix`). `contract.nix` carried
`home.sessionVariables` and `home.sessionPath` (I-488) but not aliases.
It now sets `programs.bash.shellAliases`, `programs.zsh.shellAliases` and
`programs.fish.shellAliases` from `home.shellAliases`, each at
`lib.mkOverride 90`, so a user's alias of a name the base defines (`ll`)
wins without `mkForce`. *Not carried:* `programs.bash.initExtra` and the
other shells' init options. The base already initialises starship,
zoxide, direnv and fzf, and enabling home-manager's bash to run initExtra
would initialise them a second time and take over `~/.bashrc`. Shell
code goes in `~/.bashrc`, which every bash now reads (I-513); config.md
says initExtra is not carried. `checks.fragment-examples` asserts that the
personal example's `gs` and `ll` are in the composed system's
`/etc/bashrc`.
**I-499. The Claude settings merge unions hooks per event, and takes out
the hooks the previous laptop file added.** (herdr-fixes, 2026-10-05;
amends I-196) The merge used jq's `*`, which replaces arrays, so any
`SessionStart` hook in the laptop's `~/.claude/settings.json` replaced
the guest's whole `SessionStart` list on every `run` and `attach`.
`herdr integration install claude` writes its resume hook there
(`bash '/home/dev/.claude/hooks/herdr-agent-state.sh' session`), so a laptop with
one `SessionStart` hook of its own left herdr unable to restore Claude
after a stop. Each hook event that is a list on either side is now the
guest's groups, then the laptop's; a group both sides hold identically is
kept once, at the laptop's place, so a second run gives the same bytes.
A plain union would keep every hook the laptop ever had: a hook edited
or deleted on the laptop would stay in each guest beside its
replacement. So the merge writes the hooks this laptop file added (after
the repose-hook strip and the missing-command drop) to
`~/.repose/claude-laptop-hooks.json`, and the next merge removes those
from the guest's lists before the union. A group the guest had before
the laptop also carried the same group goes with it when the laptop
drops it; that needs identical JSON on both sides, and we accept it.
The `commands` pass checks the unioned hooks, so a guest hook whose
command is missing is dropped with a note as a laptop one is. Checked
by `TestClaudeSettingsMergeKeepsGuestHooks` (herdr's hook and a laptop
`SessionStart` hook, a laptop change, a laptop with no hooks) and the
`hooks` golden, whose guest-only `Stop` entry is now kept.

**I-500. `repose stop` names the agents it interrupted, without a resume
command.** (herdr-fixes, 2026-10-05; narrows features/stop-start-destroy.md
under I-484 and I-485) The stop spec said the CLI tells you a Claude
session can be resumed with `claude --resume` when an agent window was
open. Nothing printed it. I-484 later ruled that a success line says
what happened and names no next command, and I-485 that a line the
fiftieth-time reader gains nothing from goes; `claude --resume` is a
lesson the docs already give (run-and-attach, "When the machine
stops"), and with herdr the agents come back on their own. What the
stop did is still news: it ended an agent in the middle of a turn, or
one waiting for your answer. So after the stop line the CLI prints
`Interrupted claude (working) and claude-2 (needs input).`, built from
the signals of the project as `stop` read it before stopping. Idle
agents lose nothing and are not named; no busy agent, no line. The
sample can be up to a minute old, so the line is what the machine last
reported. Checked by `TestStopNamesInterruptedAgents`.

**I-501. herdr is a supported multiplexer: in the base at a pinned
release, started by a boot unit on projects that choose it, read by
guestd.** (multiplexer-spec, 2026-10-05; supersedes I-482; owner
decision 1 of `proposals/2026-10-05-session-backends.md`) I-482 rested on
two premises the live test on `herdr-live` disproved. Any herdr from
0.9.0 accepts a remote of endpoint protocol generation 1, so the
laptop's version need not match the guest's (a 0.9.3 laptop added a
0.9.0 guest in 1.1 s with no install prompt). And the server resumes
agents with no client attached: after a stop and a start, `herdr server`
from a user unit restored two workspaces and ran `claude --resume <id>`
before any client connected. Its "revisit when" is met.
`nix/overlay/agents/herdr.nix` installs upstream's static-pie x86-64
release, pinned in `versions.json` and moved by `scripts/bump-agents.sh`
as the other agents are (R3-19). Its install check runs `herdr status
client --json` and fails the build unless `endpoint_protocol_generation`
is 1 and `protocol` is at least 22, so a bad bump fails before a guest
gets it (the I-487 lesson). The package is in `environment.systemPackages`
on every guest, which puts it at `/run/current-system/sw/bin/herdr`
where a laptop herdr's remote discovery looks. The user unit
`repose-herdr-server.service` runs `herdr server` from a login bash,
restarts on failure (a restart resumes agents), keeps
`restartIfChanged = false` (I-496) and sets no `CPUWeight` (I-505 says
why). The guest seeds `~/.config/herdr/config.toml` when it is absent
with login shells (herdr's Linux default is non-login, tmux's is login)
and no update check. tmux stays in the base and stays the default. The
contract is `interfaces/guest-conventions.md`, "herdr". Built
(mux-base): `nix/overlay/agents/herdr.nix` with 0.9.3 pinned, its install
check shared as `passthru.protocolCheck` and run by the flake check
`herdr-protocol-check` against fake binaries (generation 2, protocol 21,
no generation and unreadable output refused); `nix/guest/base/herdr.nix`
(the unit, `repose-herdr-workspace`, the seeded config written by
`ExecStartPre`), `nix/guest/base/multiplexer-is.nix` shared by both
session units, `HERDR_AGENT` in `wrap.nix`, the integration step in
`agent-setup.nix`, and `guest-session-survives-switch` reading both unit
files' text (no guest build) and refusing a path unit. Versions.json uses
the agents' `x86_64-linux.url` and `.hash` keys, which
`bump-agents-pr.sh`'s shape check requires.

**I-502. One multiplexer per project: chosen at create, changed with
`--multiplexer`, applied at the next start.** (multiplexer-spec,
2026-10-05; owner decisions 2 to 4) The word is `multiplexer`, values
`tmux` and `herdr`, in every place: the column `projects.multiplexer`
(migration 0017, default `tmux`), the api's `Project.multiplexer` and the
POST and PATCH field, the `multiplexer` key of `project.json`, the
`default_multiplexer` key of `config.toml` and `repose run
--multiplexer`. The repository used the word nowhere before, so one grep
finds every use; "session" already meant SSH sessions, the session
helper and the tmux session. A new project takes `--multiplexer`, else
`default_multiplexer`, else herdr when the CLI runs with `HERDR_ENV=1`
(inside a laptop herdr pane, where tmux would sit nested in the user's
herdr), else tmux. The `HERDR_ENV` pick skips a temporary machine, which
never enters the herdr sidebar (I-510), and a request the base gate
refuses, which falls back to tmux. `--multiplexer` on an existing project PATCHes it and
the value sticks, as `--no-personal` does (I-490). The guest reads
`project.json` at each start, so a change made while the machine runs
takes effect at its next start, and the running multiplexer and its
agents keep going until the stop. Starting the second server at once was
rejected: two sets of panes and two resume paths for the hours until the
stop. The api answers a request for `herdr` with `409 conflict`,
`detail.reason = base_update_needed`, when the project's base (for a
POST, the newest published base) is older than the first base with
herdr; without the gate a held base (`hold_base_updates`) would start
tmux with no word why. A request for `tmux` is never refused. Fork and
restore as a new project copy the source's value. Old clients ignore the
field; an absent field reads as tmux everywhere. Built (mux-api): migration 0017, `store.Project.Multiplexer`, POST and
PATCH in `internal/api/http/projects.go`, the gate in
`internal/api/http/multiplexer.go` with `herdrMinBase` empty, the copy
in `insertRestored`, `multiplexer` in `project_json`; a fork or restore
as new gates on the source's base (I-549). Tests: `TestMultiplexerField`, `TestMultiplexerForkCopies`,
`TestMigrateUpDownUp`, the fakes' `TestMultiplexerGate` and
`TestProjectJSONPassesThrough`.

**I-503. guestd starts the session unit `project.json` names, and the
path unit goes.** (multiplexer-spec, 2026-10-05) The user path unit
`repose-tmux-session.path` started tmux as soon as `project.json`
existed, which at boot is the previous boot's file: after a switch made
while stopped it would start the old multiplexer before `SetupProject`
wrote the new file. guestd already started the tmux unit right after
writing the file (I-231), so `SetupProject` now starts
`repose-tmux-session.service` or `repose-herdr-server.service` by the
file's `multiplexer` (absent or any other value: tmux), and the path
unit is removed. Both units carry `ExecCondition=repose-multiplexer-is
<name>`, which skips the start when the file names the other
multiplexer or the other unit is active. A hand `systemctl --user start`
of the wrong unit therefore starts nothing, and a machine switched while
running keeps its old multiplexer until the stop. A base switch starts
and stops no session unit (I-496). Built (mux-guestd): `ensureSession` in `internal/guestd/project`, `Handler.Multiplexer`; `TestSetupStartsTheSessionUnitProjectJSONNames`.

**I-504. guestd reads herdr's agents from its socket, on every machine,
by polling `agent.list`.** (multiplexer-spec, 2026-10-05; amends I-31,
I-49) Agent discovery goes through one source per multiplexer and the
watcher takes the union of both, so a machine switched while running, or
one where you started the other multiplexer by hand, still lists its
agents; a source whose server is absent costs a `stat`. The herdr source
dials `/home/dev/.config/herdr/herdr.sock` once per 5 s refresh (herdr
answers one request per connection), refuses a peer whose `SO_PEERCRED`
uid is not dev's (guestd is root and the path is dev's to replace), sends
`ping` after any failed dial, `agent.list` on every refresh and
`workspace.list` (decoded to `id` and `label`, to match checkout names)
at most once a minute, and reads one line of at most 1 MiB per answer. It forks nothing, so I-31 holds. The decoder
keeps `pane_id`, `workspace_id`, `name`, `agent`, `agent_status` and
`state_change_seq` and drops every other field, titles and the agent
session among them; guestd never calls `session.snapshot` or
`pane.process_info`, whose answers carry cwd, argv and cmdline (R5-3).
States map `working` to working, `blocked` to needs_input, `idle` and
`done` to idle, and `unknown` to unknown. A `state_change_seq` that moved
while the status reads `idle` or `done` is a finished turn, even one
shorter than a refresh; for gemini and pi, which have no hook, it raises
the `completed` event (`<agent> went idle`) that I-49's heuristic raises
under tmux, and for hooked agents it raises nothing, so no event arrives
twice. A protocol below 22, or a refused dial for two refreshes on a
herdr project, is `herdr_down` (I-507). After an EOF the panes keep their
state for two refreshes before going `unknown`, so a herdr restart that
resumes its agents shows no flap. Events only was rejected: a missed
subscription or `events_lost` leaves a wrong state until something else
changes. `events.subscribe` may later wake the watcher early; the poll
stays the truth. Built (mux-guestd): `internal/guestd/sample/source.go` (the `Pane` and `Source` seam, `tmuxSource` statting the socket before any fork) and `herdr.go` (`herdrSource`), unioned in `Watcher.refreshPanes`; `TestHerdrWorkingThenDoneGemini`, `TestHerdrSequenceJumpGivesOneCompletion`, `TestHerdrPeerWithAnotherUIDIsRefused`, `TestHerdrEOFGrace`, `TestHerdrOldProtocolIsDown`, `TestHerdrReplyOverTheCapIsDropped`, `TestHerdrDecoderKeepsSevenFields`, `TestHerdrAgentKeys`, and `TestHerdrLiveBinary` against herdr 0.9.3 (`REPOSE_TEST_HERDR`).

**I-505. The herdr server and its agents get I-200's memory protection
and run at nice -5.** (multiplexer-spec, 2026-10-05; owner decision 7;
amends I-200, I-494) guestd's 5 s refresh sets `oom_score_adj` -800 on
the herdr server (the `herdr` process in `repose-herdr-server.service`
whose parent is dev's `systemd --user`) and on every agent process in
its tree (the shallowest process whose name or executable is one of the
five agents' binaries), as it does for the tmux server and agent
windows; the live test found claude at 200 under herdr. Beside that
write, guestd sets nice -5 on every thread in `/proc/<server>/task`
(nice is per thread on Linux), and sets any other process in the
server's tree found below 0 back to 0, since a pane forked from a
reniced thread inherits -5 and a build in it would get the priority
meant for the server. `CPUWeight=1000` on the unit, I-494's
answer for tmux, would raise every build too, because herdr's panes share
the server's cgroup; wrapping each pane in its own scope would orphan
panes when the server dies. The SSH `session-.scope` weight still covers
the laptop's bridge. Closed by I-494's load test (keystroke echo with
`stress-ng` on every core, before and after). Built (mux-guestd): `herdrServers`, `herdrAgentPIDs` and `applyNice` in `internal/guestd/sample/oom.go`; `TestHerdrServerOOMAndNice` on a fixture, `TestHerdrLiveRenice` as root against herdr 0.9.3 (a pane shell forked after the renice inherited -5 and went back to 0). `node` is left out of the agent walk (I-535).

**I-506. A hook's window may be `herdr:<pane_id>`, sent by repose-hook;
a window that resolves to no pane changes no agent's state.**
(multiplexer-spec, 2026-10-05; amends I-244) Inside a herdr pane
`$TMUX_PANE` is unset, and `herdr-fixes` (I-499's branch) made such a
hook relay under the agent's name without touching a tmux window.
`repose-hook`, `repose-notify` and `repose-ask` now send `window =
"herdr:" + $HERDR_PANE_ID` when `REPOSE_AGENT_WINDOW` is empty,
`$TMUX_PANE` is unset, and `HERDR_ENV=1`; they read their own
environment, so guestd reads no new variable and the one exception in
`SECURITY.md` stays as written. guestd resolves a `herdr:` window through
the herdr source to the agent's key and records the hook on it. One that
resolves nowhere is relayed with the agent's name for display, and no
state changes. A pane id longer than 58 bytes or holding a character
outside `[A-Za-z0-9:_-]` is treated as unresolved. Built (mux-guestd): `windowDefault` in `cmd/repose-hook`, `windowOf` in `internal/guestd/hooks`, `Watcher.ResolveHerdr`; `TestHookNamesTheHerdrPane`, `TestNotifyNamesTheHerdrPane`, `TestHerdrHookWindows`.

**I-507. `herdr_down` joins the guest warning kinds.** (multiplexer-spec,
2026-10-05; amends I-29) guestd sends `Warning{kind: "herdr_down"}` when
`project.json` says herdr and the socket refused (or answered a protocol
below 22) on two refreshes in a row, at most once per 10 minutes like
every kind. `tmux_down` is sent only when the file says tmux or has no
key. hostd and the api add the kind to their lists and ship before the
base that sends it; an older hostd relays it as `guest_other`, which the
api already stores. The detail is hostd's own `guest <id>`. Built (mux-api): `guestWarningKinds` in hostd and `warningKinds` in the
api's events; tests `TestGuestWarningKindAndDetailAreHostWritten`,
`TestHostWarningKindsAreAFixedSet`; RUNBOOK "herdr_down".

**I-508. Under herdr, secrets, TZ and PATH reach panes through the
login shell, `BASH_ENV` and the wrappers.** (multiplexer-spec,
2026-10-05; amends I-475, I-476, I-488) herdr has no
`set-environment`, so `WriteSecrets` pushes into tmux only when the tmux
socket exists, and the post-switch push into dev's user manager reaches
the herdr server at its next start. herdr panes start login shells
(the seeded config), wrapped agents source `/etc/profile.d/repose.sh`
(I-241) and each bash command reads `BASH_ENV` (I-475), so agents and
shell commands get current secrets, TZ and PATH. A program that is not
bash, started from a pane opened before the change, keeps the
environment the server started with. The CLI's TZ push on `run` and
`attach` writes `/etc/repose/env` alone on herdr. `secrets.md` and
guest-conventions say so. Built (mux-base): the server unit reads
`/etc/repose/env` (`EnvironmentFile`) and starts from `bash -l`, the
seeded `shell_mode = "login"` makes each pane a login shell, and the
post-switch push in `env.nix` leaves the herdr server alone. Checked on
kanali with the unit's own commands: the pane's shell ran as `-bash`
with `HERDR_ENV=1`, `REPOSE_PROJECT` and `TZ`.

**I-509. On a herdr project, `run "prompt"`, attach, `ps`, `paste`,
messages and the temporary session end go through herdr, and `run` in a
laptop herdr pane opens no client.** (multiplexer-spec, 2026-10-05;
owner decision 6; amends R4-10, I-253, I-274, I-280, I-304, I-352,
I-469, I-480) The CLI picks the backend from the running unit
(`systemctl --user -q is-active repose-herdr-server` over the ssh master
it holds), since the setting can differ from what runs until the next
start. A prompt opens a tab in the checkout's workspace (created when
missing; with `--worktree` the worktree is opened with `herdr worktree
open`), starts the agent with `herdr agent start`, waits past herdr's
300 s start cap with `herdr agent wait` up to the CLI's 30 minute
dev-shell limit, and types with `herdr agent prompt`. A pane that
settles on `blocked` gets no prompt (I-486's dialog error, naming the
tab). Attach has three paths: in a laptop herdr pane (`HERDR_ENV=1`) the
CLI prints `<slug> is in herdr's sidebar.` and stays in the foreground as
the session helper (forwards, bridge, carry) until Ctrl-C; with herdr
0.9.0 or newer on the laptop PATH it runs `herdr --remote <slug>.repose
--session default` as its child, never through exec, so the helper lives
as long as the client; otherwise `ssh -t <slug>.repose herdr` under the
input proxy and the reattacher. `ps` lists herdr's agents (WORKSPACE,
AGENT, NAME, STATE; no cwd, no title), `paste` sends the path with
`herdr pane send-text` to the focused pane, messages use `herdr
notification show repose --body`, and a temporary machine ends when its attach returns
and herdr reports no panes. A client nested inside the user's herdr was
rejected. A tmux project behaves as before. Built (mux-cli): the seam is
`internal/cli/mux.go` (the interface is `muxer`, since the package
`internal/multiplexer` holds the name), `mux_tmux.go` and `mux_herdr.go`;
the probe is `muxFor`. Checked by `TestHerdrScriptsAgainstHerdr` (the
guest scripts run with bash against a herdr 0.9.3 server:
workspace create, tab create, agent start, agent prompt, agent list,
pane send-text, worktree open, the focus step), `TestHerdrAttachPath`
(the three paths by `HERDR_ENV`, a temporary machine, the laptop
herdr's version and the alias), `TestHerdrStatePick`,
`TestParseHerdrStateDropsCwd` and `TestStatusNamesHerdr`.

**I-510. The CLI keeps the laptop herdr's machine list for repose's
machines.** (multiplexer-spec, 2026-10-05; owner decision 5) When `herdr`
0.9.0 or newer is on the laptop PATH and `~/.ssh/config` includes
repose's file, the CLI reconciles `herdr machine list --json` where it
already holds the project list after writing the ssh files (`run`,
`attach`, the certificate refresh), and on `rm`. It adds `herdr machine
add <slug>.repose --label <slug> --remote-session default` for each
running herdr project with no entry, in the background so `Ready in`
does not move; removes each entry whose target is `<slug>.repose` and
whose slug is no project of the account; and leaves every entry whose
target is anything else. An entry made by hand or by the tutorial for
`<slug>.repose` is repose's from then on. A disabled entry stays
disabled, a stopped machine's entry stays, and a temporary machine is
never added. Registration behind a config key was rejected: the sidebar
is where a herdr user looks for the machine. Built (mux-cli):
`internal/cli/herdr_catalog.go`. Checked by `TestPlanHerdrCatalog`,
`TestSyncHerdrMachinesRunsHerdrsCommands`,
`TestSyncHerdrMachinesAddFailsOnce`, `TestSyncHerdrMachinesDoesNothing`
(herdr 0.8.5, a failing list, no Include line, no herdr),
`TestForgetHerdrMachine` and `TestEnsureEntry`; where adds run is
I-542.

**I-511. A laptop herdr's SSH bridge counts as someone at the machine.**
(multiplexer-spec, 2026-10-05; owner decision 8) A laptop herdr keeps an
SSH bridge to each machine in its sidebar, and each SSH session counts in
`ssh_sessions`, which the idle notice (I-262), the temporary machine's
expiry wait (I-350) and the miner check read. It counts as a `tmux
attach` left open counts today; nothing in the api changes. Stage 5 of
the proposal measures whether herdr's idle bridge cleanup closes bridges
to machines nobody has selected, and how many ssh-prepare calls a bridge
retrying against a stopped machine makes; a decision to discount bridges
would be a new entry.

**I-549. A fork or restore as new gates herdr on the source's base.**
(mux-api, 2026-10-05; amends I-502) I-502 and api.md had a fork and a
restore as a new project gate `herdr` on the newest published base, as a
POST does. `insertRestored` copies the source's `base_version` into the
copy, and the copy boots on that base until a base update moves it, so
the gate reads that version: a herdr source held on a base older than
`herdrMinBase` gives tmux copies. A null source base reads as the newest
published base, as for a POST. Gating on the newest base was rejected:
the copy would keep herdr on a base without it and start tmux with no
word why, the case the gate exists to prevent. The gate also gets a
third message, `<slug> has no base yet; herdr needs <needs> or newer.`,
for a PATCH on a project whose `base_version` is null, which the two
messages I-502 listed could not word. The fake api follows: its fork
and restore as new copy the source's base and gate on it. Tests:
`TestMultiplexerForkCopies` (fork and restore as new, the source on and
behind the min base), the fakes' `TestMultiplexerCopyGatesOnSourceBase`.
**I-551. mux-base as built: the tmux unit restarts its server after
5 s, and the herdr steps' exact rules.** (mux-base, 2026-10-05; amends
I-501, I-503) Five points where the base differs from the spec
`multiplexer-spec` wrote.

1. The path unit I-503 removed also brought the session back on a
running machine: systemd.path(5) checks the paths again when the unit
it triggered stops, and `project.json` always exists, so `exit` in the
last window or `tmux kill-server` got a new session at once. Without it
the machine had no session until the next start, and `repose attach`
and `repose run "prompt"` failed with tmux's "can't find session".
`repose-tmux-session.service` now has `Restart=always` and
`RestartSec=5s`. A start the `ExecCondition` skipped is never restarted,
and neither is a `systemctl stop`, so I-503's rules hold. The 5 s let
I-352's check after an attach (`tmux has-session` over the attach's ssh
master) see the session gone on a temporary machine; the path unit
came back at once and could beat that check. A CLI fallback (start the
unit when `has-session` fails) was rejected: `internal/cli` is
`mux-cli`'s, and the CLI is not the only reader of the session (guestd's
tmux source, `repose ps`). The herdr unit keeps `Restart=on-failure`;
how its server behaves with no pane left is for the temporary-machine
check in `workstreams/16-multiplexer.md`.
2. `repose-herdr-workspace` waits for herdr by wall time: at most 10 s,
each `herdr workspace list` capped at 2 s, then at most 5 s for the
create. A count of tries let a herdr that accepts without answering
hold `ExecStartPost` for minutes, past the user manager's 90 s
`TimeoutStartSec` (a failed start kills the server's cgroup) and
guestd's 60 s `SetupProject` budget. Measured on kanali against a socket
that never answers: 10.37 s, exit 0. It exits 0 at once when the unit's
`ActiveState` is not `active`, `activating` or `reloading`, exits 0 when
herdr never answers, and exits 1 only when the create fails, which the
unit's `-` prefix ignores.
3. `versions.json` holds herdr as `herdr.version`,
`herdr.x86_64-linux.url` and `herdr.x86_64-linux.hash`, the agents'
shape that `bump-agents-pr.sh` checks, in place of `herdr.sha256`.
4. `repose-agent-setup` reinstalls herdr's integration when `herdr
integration status` has no `<agent>: current ` line. herdr 0.9.3's
`--outdated-only` prints an update notice and lists nothing
(`src/cli/integration.rs`), so the spec's rule would never find an
outdated integration. Not installed, `outdated (vN < vM)` and `needs
repair (vN)` (an installed version at or above the expected one whose
files are wrong) are all reinstalled.
5. The seeded `config.toml` is written by the server unit's
`ExecStartPre`, as dev, before herdr reads it, and only on a project
that runs herdr. `repose-multiplexer-is` exits 2 for an argument other
than `tmux` or `herdr`; systemd skips that start like a refusal, and the
2 tells a broken unit apart from a refusal in the journal.

**I-560. The session units outlive their own servers' exits: herdr's
keeps its panes through a handoff and an OOM kill, tmux's never
restarts a start that failed.** (mux-base, 2026-10-06; amends I-551,
I-501) A review of mux-base found four faults in the units and two
claims the code did not keep.

1. herdr's panes run in `repose-herdr-server.service`'s cgroup, and the
unit inherited the user manager's `DefaultOOMPolicy=stop`: the kernel's
OOM kill of a test run in one pane stopped the unit and, through
`KillMode=control-group`, the server and every agent. The unit now has
`OOMPolicy=continue`, as the browser unit has. The tmux unit needs none:
each tmux pane is a scope of its own.
2. A live handoff (`herdr update --handoff`, the `server.live_handoff`
request) starts the new server as a child of the old one, and the old
one, the unit's main process, exits 0. With the default
`ExitType=main` systemd ended the unit there and killed the new server
and every pane. The unit now has `ExitType=cgroup`. That alone kept a
unit up with no server for as long as one pane process outlived a crash
or `herdr server stop` (measured: a failed main process with a leftover
child stays `active`), so the unit also starts `repose-herdr-watch` in
the background. It kills the cgroup once no process named herdr whose
parent is dev's systemd runs in it (guestd's rule for the server,
I-505), checking every 0.5 s until the first server and every 5 s
after. The herdr unit moves from `Restart=on-failure` to
`Restart=always`, `RestartSec=5s`, so a stopped or crashed server is
back 5 s later with its workspaces, as tmux's is (I-551), and
`StartLimitBurst=5` in 60 s, so a server that cannot start is not tried
every 5 s forever. Measured on kanali with herdr 0.9.3 in a transient
user unit with these settings: after a live handoff the unit stayed
`active` and `sleep 4242` in a pane kept running; `herdr server stop`
with a `nohup` process in a pane ended the unit, killed that process
and restarted the server 5 s later with workspace `app` restored; an
OOM kill in a pane under `MemoryMax=300M` left the server's pid and
`NRestarts` unchanged; `kill -KILL` of the server gave `Failed with
result 'signal'` and a restart; a server failing at start stopped at
`start-limit-hit` after five restarts.
3. `Restart=always` on the tmux unit, which is `Type=forking`, restarted
every start that left nothing running: a session on a tmux server
started outside the unit (over ssh, in the 5 s wait) made the script
exit 0 with an empty cgroup, and a `project.json` with no slug made it
exit 1; either repeated every 5 s, below the default start limit.
`RestartPreventExitStatus` does not help: it reads the main process,
which a forking unit's start is not (measured: exit 1 and exit 3 with
`RestartPreventExitStatus=1 3` each restarted three times in 4 s). The
unit now has `Restart=on-success` and `RestartForceExitStatus=SIGKILL
SIGSEGV SIGABRT SIGBUS`: a server's exit and its crash or OOM kill are
restarted, a failed start is not. `repose-tmux-session` exits 3 when the
slug's session belongs to a server outside the unit's cgroup (checked
only when it runs in `repose-tmux-session.service`'s cgroup), and when
`project.json` is empty. Measured on kanali in a transient unit with the
built script: `kill-server` gave the session back after 5 s in the
unit's cgroup; a server started outside during the wait ended the unit
`failed` with `NRestarts` unchanged 12 s later; `kill -KILL` of the
server restarted it; a `project.json` of `{}` failed with no restart.
4. guest-conventions and `repose-agent-setup` said the base's herdr
integration replaces one the laptop carried. herdr reports any
installed version at or above its own as `current` (measured with
0.9.3: v11 `current`, v9 `outdated (v9 < v10)`), so a newer hook from a
newer laptop herdr is kept. The texts now say so; the behaviour stays,
since a newer hook is the newer herdr's to keep.
5. The public sentence on a temporary machine's last window
(`run-and-attach.md`) said it is destroyed; that holds only when the
attach of `repose attach` or `repose run` returns and the CLI's check
runs (I-352). It now says so. The file is `mux-cli`'s; `mux-base`
carries this one sentence because the behaviour ships in the base, and
STATUS.md records it for `mux-cli`.

**I-563. The session units start their servers outside a login shell,
herdr's panes load the current environment, and a running herdr rereads
a changed config.** (mux-base, 2026-10-06; amends I-501, I-508, I-227)
Another session's findings on machine.nix and the tmux session
(`reports/nix-demo tmux findings.md`, findings 1, 2, 4, 6 and 8) named
five gaps a herdr project would hit; tmux shares 3 and 5.

1. herdr's unit ran `bash -lc 'exec herdr server'`. That login shell
exports `__NIXOS_SET_ENVIRONMENT_DONE` and `__ETC_PROFILE_DONE` into the
server, and every pane inherited them, so a pane's shell skipped
`/etc/set-environment` and `/etc/profile.d/repose.sh`: no fresh session
variables, zone or secrets, where a new tmux window gets them. The unit
now runs `repose-herdr-start`, which clears those guards and
home-manager's `__HM_SESS_VARS_SOURCED` before it execs the server. A
pane's shell, login or not (`/etc/bashrc` reads `/etc/profile` when the
guard is unset), then loads the current environment.
2. herdr has no `set-environment`, so a session variable a later switch
dropped stayed in every new pane. `repose-herdr-start` also unsets the
names in `/etc/repose/session-vars.names`; the panes' shells and the
agent wrappers set the current ones. tmux gets the same result from the
activation script's `set-environment -g -u` (I-488).
3. Both units read the login PATH through a login shell, and a
machine.nix can write the user's profile (`programs.bash.profileExtra`).
Text a profile prints went into tmux's PATH, and a profile that runs
`exec zsh` replaced herdr's shell before the server started. Both now
call `repose-login-path`: a login shell with an empty environment, no
stdin and at most 10 s that prints PATH after a marker line, so printed
text is ignored and a replaced shell gives nothing, in which case the
unit's own PATH stays. herdr's server is resolved on that PATH, so a
`herdr` in `~/.local/bin` or in `home.packages` wins over the base's, as
I-501 accepts; guestd's protocol check (I-504) reports a server it
cannot speak to as `herdr_down`.
4. herdr reads `config.toml` at server start, and the server lives for
the session (I-496), so a machine.nix that changed or removed it reached
a running machine only at its next start. `repose-herdr-reload` runs
after every home-manager activation (`home-manager-dev`'s
`ExecStartPost`, beside I-552's tmux reload): with a server's socket
there it puts the seed back when the file went away, and when the
file's digest changed since its last run it runs `herdr server
reload-config`. It never fails the switch.
5. home-manager writes user units to `~/.config/systemd/user`, which
systemd reads before `/etc/systemd/user`, so a fragment's
`systemd.user.services.repose-herdr-server` (or `repose-tmux-session`)
would replace the base's. The contract now refuses any
`systemd.user.services` name that starts with `repose-`.

A `config.toml` that machine.nix manages is a read-only link and
replaces the seeded file; it keeps herdr's defaults for anything it
leaves out, including a non-login pane shell, and herdr cannot save
onboarding into it. The public docs say to carry `shell_mode = "login"`.
Evidence: `nix flake check`'s `guest-session-environment` runs both
scripts against a scratch HOME and a fake herdr (a profile that prints
text, one that runs `exec sh`, the guards and a dropped name, the
reload on change only and the seed restored), and fails when the
guards are kept or the marker is dropped; `fragment-contract`'s
`user-unit-repose-name` fails evaluation and passes when the assertion
is removed. Not booted on a guest: no VM test runs on kanali.
**I-535. Under herdr, guestd protects the agents it can name by binary,
and leaves `node` out.** (mux-guestd, 2026-10-05; amends I-505) I-505
protects every process in the herdr server's tree whose name or
executable is one of the five agents' binaries. Gemini CLI runs as
`node` (I-46), and so does a dev server started in a shell pane; under
tmux the window name tells them apart (a `node` counts only in a window
named `gemini`), but under herdr guestd has no pane-to-process map,
because the only herdr answer that carries pane pids, `pane.process_info`,
also carries argv and cwd (R5-3). Protecting every shallowest `node` in
the tree would put a vite or a test runner at -800, which is the
opposite of I-200. So the herdr walk looks for `claude`, `opencode`,
`codex`, `gemini` and `pi` (and nix's `.X-wrapped` names) only, and
Gemini CLI under herdr gets no -800: it runs as `node` (I-46), so no
process of it is named `gemini`, and it stays at the kernel's default
until herdr reports a pane's root pid. The herdr server itself is protected as I-505 says,
and its pane shells and their children are set back to 0 when they hold
a negative value. Revisit when herdr reports a pane's root pid in
`agent.list` without argv or cwd.

**I-561. A herdr agent's turn finishes when `completion_seq` rises, the
read after a failed one is a baseline, and of two herdr agents with one
key the first is reported.** (mux-guestd, 2026-10-06;
amends I-504 and I-506) I-504 took a `state_change_seq` that moved while
the status reads `idle` or `done` as a finished turn. herdr bumps that
sequence on every state change, including `unknown` to `idle` when a
named agent's process is first found or a pane respawns, which herdr
itself does not count as a completion (herdr 0.9.3+42,
`finish_agent_process_acquisition`). That sent `gemini went idle` for an
agent that had run no turn. The decoder now keeps a seventh field,
`completion_seq` (a number or absent, no tenant text), which herdr sets
only for idle reached from working or blocked, and clears at the next
change. A `completion_seq` above the one the previous read saw for that
pane is a finished turn, even one shorter than a refresh. herdr counts
both sequences per server process from 0 and never saves them, so the
first read after a failed one only records them, and a `state_change_seq`
lower than the previous read's is a restart, never a finished turn.
`workspace.list` goes at most once a minute, and at once for a
`workspace_id` the last `workspace.list` was not asked about; a failed
one, or one that lacks an agent's workspace, waits the minute. Keys: a
refresh that cannot read tmux keeps the tmux window names of the last
one that could, so a `<key> (herdr)` does not flip to `<key>`; of two
herdr agents with one key, the first in `agent.list` is reported, and a
hook from the other's pane is unresolved (I-506's no-state rule); a key
that passes from a tmux window to a herdr agent, or the other way, drops
the hook recorded on it, and a herdr agent whose key changes keeps its
hook. Built: `herdrSource.Panes` and `Watcher.refreshPanes` in
`internal/guestd/sample`; `TestHerdrUnknownToIdleIsNoCompletion`,
`TestHerdrSequenceJumpGivesOneCompletion`,
`TestHerdrRestartInTheGraceSendsNoCompletion`,
`TestHerdrLowerSequenceSendsNoCompletion`,
`TestHerdrLabelsAreNotReadEveryRefresh`,
`TestHerdrDuplicateKeyHookResolvesToNothing`,
`TestHerdrKeyKeepsItsSuffixWhileTmuxIsUnreadable`,
`TestHookStaysWithItsAgentWhenTheKeyMoves`.

**I-562. `tmux_down` and `herdr_down` wait for this boot's SetupProject,
and the watcher sends each warning kind at most once per 10 minutes.**
(mux-guestd, 2026-10-06; amends I-29 and I-507) The session unit starts
only at SetupProject (I-503), which hostd sends after RegisterPaths,
WriteSecrets and SetPrincipals, while guestd's watcher starts at boot
with the multiplexer the last boot's `project.json` named. A boot whose
chain took longer than a refresh sent `herdr_down` (or `tmux_down`)
before the server had been asked to start. guestd now counts no refresh
toward either warning until SetupProject has run since guestd started,
whether or not the unit started, or until 60 s after guestd started, the
end for a guestd restarted on a running machine, which gets no
SetupProject. `herdr_down` then needs two failed refreshes counted from
that point. The contract already said each kind is sent at most once per
10 minutes, but the watcher sent again each time a condition cleared and
came back, so a herdr restarting every minute (`Restart=on-failure`)
sent `herdr_down` every minute. The watcher's warnings (`tmux_down`,
`herdr_down`, `docker_down`) now keep a last-sent time per kind, and a
condition that holds when the 10 minutes pass is sent then. Built:
`Handler.SetupDone` in `internal/guestd/project`, `sessionExpected` and
`oneShot` in `internal/guestd/sample`; `TestSetupDoneOnlyAfterSetup`,
`TestHerdrDownWaitsForSetup`, `TestHerdrDownFlapSendsOncePerRepeat`,
`TestHerdrProjectWarnsHerdrDown`.
would be a new entry. Built (mux-cli): nothing in the CLI
counts sessions; the public docs say a sidebar entry holds the machine
in use (lifecycle "Idle machines", run-and-attach, the herdr tutorial).
The stage 5 measurements wait for the release that ships herdr.

**I-542. The multiplexer probe runs only for a prompt or an attach; a
certificate refresh only removes sidebar entries; herdr's notifications
need its toast delivery on.** (mux-cli, 2026-10-05; narrows I-509,
I-510) Three things the contracts left open met the code. First, the
probe (`systemctl --user -q is-active repose-herdr-server`) is one more
ssh, and `repose sync` and `run --no-attach` with no prompt need no
answer: they ask nothing, and the sidebar takes the project's stored
`multiplexer` for them; `TestRunStartedGuestFirstConnectionIsTheProbe`
still counts two ssh commands. `repose status` reads the answer in the
ssh it already makes for listening processes. Second, `ensureCert` is
also run by `repose ssh-prepare`, which ssh starts and which exits as
soon as the files are written, so a background `herdr machine add` there
would be killed half done: the certificate refresh removes entries and
adds none, and `run`, `attach` and `sync` add (`sync` waits up to 30 s
for its adds). Third, herdr 0.9.3 shows `notification show` only when
`[ui.toast] delivery = "herdr"` is in the server's config; its default,
`off`, answers `reason: "disabled"`. The CLI sends the messages I-509
names either way; whether the base seeds that key is mux-base's call
and is not made here.

Review of mux-cli added four more (amends I-509, I-510). A temporary
machine runs tmux only: I-352 destroys it when the last terminal closes,
and herdr 0.9.3 with a client attached opens a fresh workspace and shell
as soon as its last pane closes (`ensure_default_workspace`), so a herdr
session never reaches zero panes and the machine would wait out its
expiry. `--temp` takes tmux from `default_multiplexer` as from
`HERDR_ENV`, `--temp --multiplexer herdr` exits 2, and `--multiplexer
herdr` on a temporary project exits 2 naming `repose keep`. Telling an
idle shell from a running job would need herdr's process info, whose
answer carries argv, which R5-3 keeps out of reach. Second, the sidebar
reconcile adds other projects by their stored `multiplexer`: the api has
no other answer, and probing each would cost an ssh per project. A
project switched to herdr while it runs tmux can therefore enter the
sidebar before its next start, and herdr's `machine add` prepares the
remote and starts a herdr server there, beside tmux and outside the
unit, until the stop. The current project is added only on the guest's
answer, or, on a `sync` that asks nothing, by the value it had before
that same command switched it while running. Third, a failed add's line
is held while an attach owns the terminal and printed after it returns.
Fourth, the public docs name the test for a CLI that knows herdr
(`repose run --help` lists `--multiplexer`) rather than a version, which
is not known before the conductor tags the release; the release notes
name the version.

A second review pass added five (amends I-509, I-510). The probe reads
the unit's `ActiveState`, this boot's `multiplexer` in `project.json`
and whether tmux answers, in the same one ssh: `active`, `activating`
(the restart wait) and `reloading` are herdr, a tmux that answers is
tmux, a boot that named herdr with neither exits 1 (`herdr is not
running on <slug>`) instead of attaching to a tmux server that is not
there, and an ssh that failed takes the stored value. A command that
read no project list (the fast attach, a `run` whose connection was
already up) reconciles its own project alone, adding and never
removing, so I-223's fast attach makes no api call for the sidebar.
Each add reads herdr's list again under `herdr-sidebar.lock` in the
config directory, since herdr's `machine add --label` makes a second
entry for a target it has; the reconcile removes all but one entry per
live slug. An attach that execs ssh in place of the CLI waits up to
10 s for adds in flight, which the exec would kill halfway. The guest's
herdr shows repose's messages only with `[ui.toast] delivery =
"herdr"`, which no base seeds yet; until mux-base or the conductor
records who seeds it, the public docs say so and give the two lines,
and say that drops and Ctrl+V from a laptop herdr go to herdr and copy
nothing.

**I-564. The base's herdr config turns on herdr's toast delivery.**
(owner, 2026-10-06; settles the open part of I-542) herdr shows
`notification show` only with `[ui.toast] delivery = "herdr"`, and its
default is `off`, so on a stock machine every message I-509 sends
(copied files, new forwards, the time zone) went nowhere. The owner
chose to seed the key: `repose-herdr-config` writes it into a new
`~/.config/herdr/config.toml` beside `shell_mode` and `version_check`.
A file that was there before the seed is left as it is (I-501's rule:
the file is dev's, and herdr edits it), so a machine where the user
ran herdr by hand keeps their setting; the public docs give the two
lines for that case. `guest-session-environment` fails when the seed
lacks the key, and the guest-base VM test reads the seeded file.

**I-565. The herdr config seed gives back the file home-manager moved
aside.** (herdr, 2026-10-06; amends I-563, from nix-demo's notes on
machine.nix) herdr reads one config file with no system layer, so the
base cannot keep its settings in /etc and still let a user's file win;
the seed stays in `~/.config/herdr/config.toml`. home-manager
(`backupFileExtension = "repose-bak"`, microvm.nix) moves a real file to
`config.toml.repose-bak` when a machine.nix starts managing it, and
refuses the next activation while that backup exists. Before this, a
machine.nix that managed the file, dropped it, then managed it again
failed the third switch: `repose-herdr-reload` had seeded a new file
over the gap and the old backup was still there. Now, when the file is
gone and a regular `.repose-bak` is beside it, the seed moves the
backup back and writes nothing new. `guest-session-environment` covers
the take-over, the release and the restore.
**I-525. The base installs git-lfs with its filter in /etc/gitconfig.**
(base-git-gpg, 2026-10-05; amends I-195 and I-210) A laptop that ran
`git lfs install` carries `filter.lfs.clean`, `.smudge`, `.process` and
`.required=true` in its global config (I-195), and the base had no
git-lfs, so `git add .` in an LFS repository failed with "git-lfs:
command not found ... clean filter 'lfs' failed" and a clone failed at
checkout. The sync docs told users to `repose config add git-lfs`, which
installs the binary without the filter config, so `git lfs pull` said
"Git LFS is not installed for this repository" and a commit stored the
raw file. `programs.git.lfs.enable` installs git-lfs and writes
`[filter "lfs"]` to `/etc/gitconfig`; `git lfs pull` fetches a synced
repository's objects with no other step. About 13.8 MiB of closure
(git-lfs 13.5, pinentry-curses and libsecret for I-528 the rest),
measured as the NAR sizes of the paths base 2026.10.05 lacks. The
command-key check of I-195 is widened in the same change (see its
amendment), and the sync sets `GIT_LFS_SKIP_SMUDGE=1` (I-210's
amendment). *Rejected:* dropping a carried `filter.*.required` key so the
add succeeds without the filter (for git-crypt that commits plaintext).

**I-526. gh is the guest's credential helper for GitHub, by command
name, in /etc/gitconfig.** (base-git-gpg, 2026-10-05) `features/secrets.md`
told a user who logs in to gh on the machine to run `gh auth setup-git`.
That writes `helper = !/nix/store/...-gh-2.100.0/bin/.gh-wrapped auth
git-credential` for github.com and gist.github.com into `~/.gitconfig`:
a path that a base update and the host's store GC remove, and that skips
the wrapper's `GH_TELEMETRY`. The carry (I-247) wrote a helper for
github.com only, so `git credential fill` for gist.github.com failed. The
base now sets `credential."https://github.com".helper` and
`credential."https://gist.github.com".helper` to `!gh auth
git-credential` in `/etc/gitconfig`; with no gh login gh answers nothing
and git prompts, so it is safe on every machine. The carry adds the gist
helper beside its github.com one (hash part `gh-helper-3`), and keeps the
two `url.insteadOf` rewrites conditional on a carried login: set
system-wide they would send an SSH deploy key made on the machine (I-247)
over HTTPS. `repose-gh-helper-cleanup`, run from dev's user activation
(at login and at each base switch, since dev's user manager lingers and
a unit wanted by `default.target` would wait for the next boot), removes
the helpers for those two hosts in `~/.gitconfig` whose value
matches `^!/nix/store/[^ ]*gh[^ ]* auth git-credential$`, with the empty
`helper =` that `gh auth setup-git` writes before each; any other helper
stays. The secrets page now says a `gh auth login` on the machine is
enough for HTTPS URLs. *Rejected:* `url.insteadOf` in `/etc/gitconfig`
(deploy keys); cleaning up from the carry (a user with no laptop gh login
never gets that part).

**I-527. git defaults for a fresh HOME, at system scope.** (base-git-gpg,
2026-10-05) `/etc/gitconfig` was empty, so with no carried config `git
init` made `master`, a divergent `git pull` stopped with "Need to specify
how to reconcile divergent branches", and the first `git push` of a new
branch failed for want of an upstream, each a stop for an agent working
alone. The base sets `init.defaultBranch=main`, `pull.rebase=false` and
`push.autoSetupRemote=true` in `/etc/gitconfig`, as `mkDefault`. System
config is read before the global file and the carried file it includes
(I-195), so any of these the laptop sets wins, and a key set by hand in
`~/.gitconfig` wins over both. *Rejected:* `rerere`, `merge.conflictStyle
zdiff3`, `fetch.prune`: preferences, which the laptop's config carries
for the users who hold them.

**I-528. gpg-agent's pinentry is pinentry-curses, set in
/etc/gnupg/gpg-agent.conf.** (base-git-gpg, 2026-10-05) The base lists
gnupg for commit signing, yet gnupg 2.4.9's built-in pinentry path
(`<gnupg>/bin/pinentry`) does not exist and nothing provided one, so
`echo test | gpg --symmetric` failed with "No pinentry" (exit 2) with or
without a terminal. gpg-agent 2.4 reads `gpg-agent.conf` in its
sysconfdir, `/etc/gnupg`, before the user's: on kanali (base 2026.10.05)
the same command succeeded once a test pinentry was named there, inside
a mount namespace with `/etc` overlaid, and failed without it.
`/etc/gnupg/gpg-agent.conf` names `pinentry-curses`, and interactive
bash exports `GPG_TTY` as `tty` names it, when there is one, so it
draws in the pane. A user's own
`~/.gnupg/gpg-agent.conf` still applies on top. `gpgconf
--list-components` keeps printing the compiled-in path, which the agent
does not use once the file names another. Agents have no terminal; the
machine guide tells them to pass `--batch --pinentry-mode loopback
--passphrase-fd`, which needs no pinentry (`allow-loopback-pinentry` is
gpg-agent's default since 2.1.12). A gpg-agent already running at a
base switch read its config before the file existed, so dev's user
activation runs `gpgconf --reload gpg-agent` when the agent's socket
exists, bounded at 10 s and never failing the activation; no agent is
started for it. *Rejected:* `programs.gnupg.agent`,
which adds socket-activated user units for one config line.

**I-529. The guest deletes, weekly, the unused store paths only its
overlay holds.** (base-nix-store, 2026-10-06) The guest never collected
garbage (`nix.gc.automatic = false`, `min-free` 0, dev's profile-1..14
never pruned). On kanali (base 2026.10.05) `/` was 40 GB and 89 to 95
percent full, with 17 GB in `/nix/.rw-store`; of the 3,248 paths
`nix-store --gc --print-dead` listed, 2,500 existed only in the overlay's
upper dir, 8.6 GB, mostly flake `-source` copies of a dirty checkout
(about 1.9 GB each), old codex and deno builds and go-modules. A
whole-store GC (`nix.gc.automatic`, `min-free`, `nix-collect-garbage -d`)
is unsafe here: deleting a path that also exists in the lower layer
(`/nix/.ro-store`, the host's copy) writes an overlayfs whiteout into the
upper dir, and the whiteout keeps hiding the host's copy. hostd registers
earlier closures again (`--load-db` at every switch, I-67; the store view
keeps up to 16 earlier closures and the `rev-*` roots, I-463), so a later
switch or rollback finds a valid path with no files. kanali already has
2,047 whiteouts from a manual GC on 2026-10-03, 1,157 of them over paths
that are in `.ro-store`. `repose-store-gc.service` (Nice 19, idle I/O)
runs from a weekly timer (`RandomizedDelaySec=1h`, `Persistent=true`):
it deletes dev's profile generations older than 14 days, as dev (`nix
profile wipe-history --older-than 14d`; the system's generations are
hostd's), then keeps from `--print-dead` the paths whose basename exists
in the upper dir (read from /proc/mounts as `repose-pin-profile` reads
it), is not a character device there (a whiteout) and exists in no
lower dir, and passes them to `nix-store --delete`, which refuses live
paths. `nix-store --delete` refuses the whole list, deleting nothing,
when a dead path outside the list refers to one in it (checked on a
scratch store), so the closure of the dead paths that stay is left out;
on kanali that removed none of the 2,500. A path that became live
between the listing and the deletion makes the call fail, so the
selection runs once more. The script, run on kanali with the deletion
replaced by a listing, selected 2,534 paths (8.6 GB) in 6 s and no path
in `.ro-store`. `nix.gc.automatic` stays false and `min-free` at its
default. VM test `guest-base` ("I-529"): a path the guest added goes, a
dead path in the lower layer stays with no whiteout, live paths stay.
Follow-up, not built: guestd could remove a whiteout that hides a path
`RegisterPaths` registers, which would repair guests a manual GC already
damaged; it changes what the registration step writes to the overlay and
needs its own decision.

**I-530. dev is a trusted nix user.** (base-nix-store, 2026-10-06)
`nix store info` said `Trusted: 0` and nix.conf had `trusted-users = root
root`, so a flake's `nixConfig.extra-substituters` was dropped even with
`--accept-flake-config` ("ignoring untrusted substituter"), and `cachix
use` could not work. dev already has passwordless sudo (R3-13), so trust
grants nothing dev could not take. `trusted-users = [ "root" "dev" ]`;
`accept-flake-config` stays false, so a flake's caches apply only when
the command asks (`nix develop --accept-flake-config`, which agents need,
having no TTY for nix's prompt, or `--option extra-substituters URL
--option extra-trusted-public-keys KEY`). cache.nixos.org stays the
default; its duplicate in nix.conf (the module's default plus ours) is
harmless and left alone. Docs: machine.md "Projects with a flake.nix",
the agent guide. VM test `guest-devtools` checks `Trusted: 1`.

**I-531. `nixpkgs` in the guest's global flake registry is the base's
nixpkgs, locked.** (base-nix-store, 2026-10-06; amends I-218) Nix 2.34
locks a flake's indirect inputs against the global registry only, never
the system one, so with `flake-registry = ""` (I-218) a flake with
`outputs = { self, nixpkgs }` and no inputs, or `inputs.nixpkgs.url =
"nixpkgs"`, failed with "cannot find flake 'flake:nixpkgs' in the flake
registries" in the cd hook, `nix develop` and `nix flake lock`. The
global registry is now a file the base writes with one entry: `nixpkgs`
to `github:NixOS/nixpkgs` at the flake input's `rev`, `narHash` and
`lastModified`, which `repose.nixpkgsLocked` carries into the guest
(set by `nixosModules.guestBase` and the runner). Nix finds the narHash
in the store, so the lock is made offline: on kanali an input-less flake
locked to `github:NixOS/nixpkgs/b1b8759...?narHash=...` with
`--offline`, where the empty registry failed, and the lock is a URL a
laptop and CI can fetch. Without a known rev the entry points at the
source path (`type = "path"`), which locks but only to a path. The
system registry keeps the path entry, so `nix profile add nixpkgs#X`
behaves as before; `templates#` and `home-manager#` stay unresolved, as
I-218 intends. VM test `guest-devtools` ("I-531") locks an input-less
flake offline and checks the lock is the github entry.

**I-532. The guest has no nix channels.** (base-nix-store, 2026-10-06;
amends I-218) NIX_PATH was `nixpkgs=flake:nixpkgs:/nix/var/nix/profiles/per-user/root/channels`;
that directory never exists in the guest, so every `nix-shell -p`
warned that it does not exist. `nix.channel.enable = false`: NIX_PATH is
`nixpkgs=flake:nixpkgs` alone and `nix-channel` is not installed.
Nothing in the base runs `nix-channel` or reads a channel's
`programs.sqlite` (command-not-found uses nix-locate, I-219). No
`~/.nix-defexpr` link is added for `nix-env`: dev's profile is a `nix
profile` manifest, which `nix-env -i` refuses anyway. VM test
`guest-devtools` checks NIX_PATH and that `nix-channel` is absent.

**I-533. The libraries a guest-built binary links are pinned in the
overlay.** (base-nix-store, 2026-10-06) A binary built in the guest names
base store paths: on kanali, a `cc` build against openssl has
`/nix/store/...-glibc-2.42-84/lib/ld-linux-x86-64.so.2` as its
interpreter and a RUNPATH of openssl-3.6.4, glibc-2.42-84 and
gcc-15.3.0-lib; cgo, node-gyp and cargo builds are the same. On the host
only the current SystemClosure and the `rev-*` roots keep those paths, a
stop drops the guest's root, and the host GC runs weekly with
`--delete-older-than 14d`, so after a base change and a GC such a binary
fails with ENOENT. `repose-pin-profile` copied up only dev's profile.
It now also copies up, at every boot and switch (the same service, which
runs while the base's paths are still in the lower layer; a switch-time
hook would be too late once a stop has dropped the root), the closure of
a list fixed at eval time: the gcc wrapper's libc and `cc.lib`, and the
runtime outputs of openssl, zlib, sqlite, libffi, libyaml, libpq,
libxml2, libxslt and libmysqlclient (compat.nix's `PKG_CONFIG_PATH`
set). Headers are left out: a rebuild uses the new base's wrapper. Each
target is rooted at `/nix/var/nix/gcroots/repose-link-targets/<name>`,
and roots from earlier bases stay, so I-529's GC never deletes them; the
upper dir is never cleared of them. Measured on kanali with `nix
path-info -sS`: glibc's closure is 36.0 MiB, openssl 8.9 MiB on its
own, gcc-lib 9.8 MiB, the whole set 72.3 MiB; an unchanged path costs
nothing (the existing `-e` test), and on kanali all but 2.1 MiB were in
the upper dir already. All eleven are in the base closure already, so
the closure does not grow. The service now waits for `repose-paths`
(I-67), since before the registration `nix-store -qR` of a system path
finds nothing. At a switch the activation script runs before
switch-to-configuration reloads systemd, so starting
`repose-pin-profile.service` from it ran the previous base's script,
which knows only the previous base's targets; it starts this base's
script as a transient unit (`systemd-run --no-block`) instead.
VM test `guest-base` ("I-533") builds against openssl,
checks each linked path is copied up and rooted, and runs the binary
from the upper copies alone (the test VM's lower layer is the host's
read-only store, so a path cannot be taken out of it there).

**I-534. The guest has man pages.** (base-nix-store, 2026-10-06; amends
I-218) `man ls`, `man git` and `man tmux` said "No manual entry":
`documentation.enable = false` gates every documentation option, so
`/share/man` was never linked into the system path and no `man` output
was installed (`man` itself comes from the laptop's tools). Now
`documentation.enable` and `documentation.man.enable` are on, with
`man.cache.enable` (formerly `generateCaches`), `doc`, `info`, `dev` and
`nixos` off. Most pages were in the closure already, inside the
packages' `out` (coreutils-full, git). The delta is the `man` outputs of
installed packages not yet in the closure: 49 of them, 13,119,200 bytes
of NAR (cache.nixos.org narinfo; the largest are openssl's 4.1 MB and
systemd's 2.1 MB); man-db and its closure were in the base already.
That is within the 6 GiB check's room (the 2026.10.05 base closure is
6,306,239,088 bytes). VM test `guest-devtools` runs `man -w` for ls,
git, tmux and nix.
**I-550. A guest's hostname is its project's slug.**
(base-hostd-guestd, 2026-10-06) Every guest called itself `repose-guest`
(`hostname`, the shell prompt, `t3 pair`'s "Pairing with"). hostd
rendered `ip=<ip>::<gateway>:<netmask>::eth0:off` with the name field
empty, and filling it alone would change nothing: the closure writes
`/etc/hostname` from `networking.hostName`, and systemd applies that over
the kernel's name. hostd now sets `ch.Spec.Hostname` to the project slug
when it is a DNS label (a-z, 0-9 and `-`, 1 to 63 characters, no
leading or trailing `-`) and renders it in the `ip=` name field and as
`systemd.hostname=<slug>`, which systemd prefers over `/etc/hostname`.
An empty or invalid slug renders neither, so the guest keeps
`repose-guest`, as NixOS test nodes and slugless guests do; the closure
stays the same for every guest (no per-guest `networking.hostName`).
Create, start, restore and a rebuild all render the line from the
recorded `project_slug`, so a guest gets the name at its next boot. The
runner's `bin/run` passes `systemd.hostname=` too.
*Rejected:* a per-guest `networking.hostName` (one closure per project);
`hostnamectl hostname` from guestd (`/etc/hostname` is a read-only store
link). Tests: `TestCmdlineHostname`, `TestCmdlineHostnameFallback`,
`TestCreateReachesRunningWithEverythingWired` and
`TestSnapshotRestoreRoundTrip` read `ch.args`.
**I-536. A base switch never restarts dockerd, the desktop or the
agents' browser; containers outlive dockerd.** (base-docker-system,
2026-10-05; the failure class of I-496) guestd runs
`switch-to-configuration` at the 04:00 UTC sweep
(`internal/guestd/system/system.go`). `docker.service` had no
`X-RestartIfChanged=false` (the pinned nixpkgs sets it only on
`docker-prune`), its unit embeds the docker, systemd, coreutils and kmod
store paths, and live-restore was off, so the first switch after a
nixpkgs bump would stop every container, and a `docker run -d
postgres:17` with no restart policy would stay down. The desktop
(repose-xvnc, -openbox, -vncconfig, -novnc, -novnc-proxy) and the
browser (repose-browser, -browser-proxy, -browser-bridge-proxy) had the
same gap, and repose-browser `BindsTo` repose-xvnc, so a changed Xvnc
unit would have closed the agents' pages mid-session. All of them now
set `restartIfChanged = false`, and the daemon sets `live-restore =
true` directly (the `virtualisation.docker.liveRestore` option is a
rename alias). A new dockerd runs from the machine's next start; the
desktop and browser stop on their own (StopWhenUnneeded, the 30-minute
idle check) and the next socket activation starts the new closure. The
socket units are left alone: they hold no process. live-restore is
incompatible with swarm mode, which no guest runs; a user who wants
swarm sets `live-restore` false in their own daemon config. Check
`guest-session-survives-switch` now also greps each of these units
(and a nixpkgs unit's `overrides.conf` drop-in) for the flag;
`guest-docker` asserts `LiveRestoreEnabled` true. Not covered: a VM
test that switches a running guest between two bases with a container
up; it needs the dev box. `docker.service` `Requires=docker.socket`, so
a switch that changes the socket unit's text still stops dockerd; that
unit embeds no store path, and live-restore keeps the containers
through it.

**I-537. Containers resolve through resolved on 172.20.0.1.**
(base-docker-system, 2026-10-05) On the default bridge, and in `docker
build` RUN steps, an Alpine (musl) container waited 2.50 s on every
lookup: musl sends A and AAAA from one UDP socket at once to
1.1.1.1/8.8.8.8, the second answer is lost past the guest, and musl
retries at +2.5 s. A Python two-query test without Docker lost the
second answer 6/6 to 9/10, against 0/10 with a 50 ms gap or through
127.0.0.53; guest conntrack counters were clean and the guest has no
firewall. dockerd now pins the default bridge to `bip 172.20.0.1/24`
(the first /24 of the 172.20.0.0/14 pool, which docker0 already had)
and gives containers `dns [172.20.0.1]`; resolved's stub also listens
there (`DNSStubListenerExtra`, bound with IP_FREEBIND, so it needs no
docker0 at boot). The same daemon `dns` feeds the embedded resolver
(127.0.0.11) of user networks and BuildKit. No `dns-opts` timeout:
with resolved answering, nothing waits. Checked on kanali (base
2026.10.05) with a runtime resolved drop-in: `docker run alpine getent
ahosts github.com` took 2.50 s per lookup before, 0.00 to 0.02 s with
`--dns 172.20.0.1` on the default bridge and on a user network;
`ss -Hlun` showed 172.20.0.1:53. `guest-docker` asserts the bridge
container's resolv.conf and the listener. *Host root cause, not
measured:* the per-guest `ct count`/`limit` rules in
`nix/hosts/nftables.nix` may drop the clashing second packet of a new
UDP flow. Someone should read `conntrack -S` and flows_drop on host-01
during the two-query test and, if confirmed, add `iifname "br-guests"
meta l4proto udp th dport 53 accept` ahead of them. nftables is not
changed without that measurement.

**I-538. Open files: 524288 soft for the user manager and dev's
logins.** (base-docker-system, 2026-10-05) The user manager's
defaults were `DefaultLimitNOFILESoft=1024` (hard 524288), so
`repose-tmux-session.service`, the tmux server and every pane had a
soft limit of 1024; Python in a pane hit EMFILE at about 1021 open
files while Claude Code's own shells had 524288. The base sets
`systemd.user.settings.Manager.DefaultLimitNOFILE = "524288:524288"`
(`systemd.user.extraConfig` is removed in the pinned nixpkgs) and
`security.pam.loginLimits` soft nofile 524288 for `dev`, which covers
SSH, `repose exec` and `repose code`. System daemons keep systemd's
defaults. The guest VM test reads the tmux server's and a pane's
`/proc/PID/limits` and `ulimit -Sn` in a `su - dev` login. On a live
base switch a new SSH login gets the limit at once, but the running
tmux server and its panes keep 1024 until the machine's next start,
since the switch never restarts the session (I-496).

**I-539. dev may ptrace its own processes.** (base-docker-system,
2026-10-05) `kernel.yama.ptrace_scope` was 1 (yama's default, kept by
I-231's `security.lsm = [ "landlock" "yama" ]`), so `strace -p`, `gdb
-p` and py-spy on dev's own process failed. The guest has a single
user with passwordless sudo, and the VM is the boundary; Landlock
(Codex) and the bwrap PID namespaces still confine the agents. The
sysctl is now 0, and yama stays in `security.lsm` so the knob exists.
The sysctls subtest asserts it.

**I-540. The browser has CJK fonts.** (base-docker-system, 2026-10-05)
`fc-list :lang=ja` (zh, ko) returned nothing, so Chinese, Japanese and
Korean rendered blank or as boxes in Chromium and in the agents'
screenshots. `fonts.packages` adds `noto-fonts-cjk-sans`, the variable
OTC build alone (narSize 64,592,096 bytes, no references, from
cache.nixos.org), and "Noto Sans CJK SC" follows "Noto Sans" in the
sans-serif default. The serif package and the static build are left out:
either breaks the 6 GiB cap. The base closure was 6,306,239,088 bytes;
with the font it is about 6,370,831,184 plus the fontconfig cache
growth, leaving about 71.6 MB (68 MiB) under the cap. Review measured
that growth by building the `fc-cache` derivation alone: 1,766,776
bytes against 1,648,120 on base 2026.10.05 (+118,656), so the delta is
about 64.7 MB and the headroom about 71.5 MB, shared with every other
branch that adds to the closure (base-git-gpg adds about 13.8 MiB).
With that cache, `fc-list :lang=ja` (zh, ko) lists 10 faces and
`fc-match sans-serif:lang=ja` picks Noto Sans CJK JP. The guest system
itself was not built; `guest-closure-size` is the check.

**I-541. `BROWSER` prints the URL.** (base-docker-system, 2026-10-05)
`BROWSER` was unset and the guest has no `xdg-open`, so `gh browse`
and `gh pr create --web` failed with `exec: "xdg-open,...": executable
file not found` and never showed the URL. `environment.variables.BROWSER`
is now a store-path script that prints `Open in your browser: URL` to
stderr and exits 0, so no new command lands on PATH. It never opens the
agents' Chromium: that would put the user's logins in the agents'
browser. A user's own `BROWSER` wins. Python's `webbrowser.open`, which
launched a separate Chromium, now prints too.
**I-552. Removing a machine.nix leaves nothing behind: tmux follows its config, and a deleted file is named.**
(personal-removal, 2026-10-06; follows I-490, I-496) On kanali the owner
pushed a machine.nix that themed tmux and reloaded it into the running
server, then emptied it and applied: home-manager removed every file,
and the purple status bar stayed. tmux reads `/etc/tmux.conf`,
`~/.tmux.conf` and `~/.config/tmux/tmux.conf` once, when the server
starts, and since I-496 the server lives as long as the session, so a
fragment or machine.nix that added, changed or removed a tmux config
did nothing on a running machine, and one sourced by hand outlived its
file. `repose-tmux-reload` (nix/guest/base/tmux-reload.nix) now runs as
`ExecStartPost=-` of `home-manager-dev`, which also restarts when
`/etc/tmux.conf` changes: when the files' contents changed since its
last run and a server is running, it unsets every global server,
session and window option, replaces the key tables with tmux's defaults
(from a server that read no file), and sources the files in tmux's
order. Sourcing alone was rejected: it only adds, and appends to array
options such as `terminal-features` on each run. Options a session sets
for itself (the CLI's port forward line), the global environment (TZ,
PATH) and every window and pane are kept; a global option a user set by
hand is lost at the next config change. With no server (as at boot) it records
the digest only; the first run on a running machine reloads, so the base
that brings the script also brings its own tmux changes (I-515's
terminal-features and set-titles) to sessions already open. The digest is
recorded after the reload, so one cut short runs again. Every tmux call
has a 10 s timeout, so a hung server or a config blocking in `run-shell`
cannot hold the switch, and the `-` keeps a failed reload from failing
it; a file with an error still loads its other lines. Nothing is reset
unless tmux's default key bindings were read first (at least 100
`bind-key` lines), since emptied key tables would leave the session with
no way to detach.
Check `guest-tmux-follows-config` asserts the unit line, and on a real
tmux in the build sandbox that the first run keeps the file's options
and every default binding without doubling array options, that removal
resets an option, a binding and an array entry to tmux's defaults with
exactly the default key tables while a session option survives, and that
an added file loads. Shells already open keep the aliases and functions their
`.bashrc` loaded; that is documented, not fixed.

The CLI side: deleting `~/.config/repose/machine.nix` pushes nothing,
so the account kept applying it to new machines with no word. `run`
now says once, when this laptop pushed the account's current revision
and its file is gone since, that the account still has it
(`missing_noted` in machine.nix.state keeps it to once).
`config --global apply` with no file names `apply /dev/null` as the way
to remove it when the account has one. The docs gain the removal steps,
the starship sentence (the machine already runs starship, so a prompt
needs only its config file; aliases work through I-519), and that a tmux config reaches the running
session. Not covered: a VM test that switches a running guest's
personal layer off with a pane open; it needs the dev box.

**I-570. A stop says how long it took and how big its snapshot is, and
nothing about cost.** (dogfood, 2026-10-07: "Stopped waterville in 24s.
Snapshot 01a10e86-e9fd-71b8-80a6-4b74cd7d1196 (2.6 GB). Disk is still
billed." "wtf bro") The billing claim was false: since I-289 a plan buys
memory that may run at once, disk, egress and a number of projects; a
stopped project costs nothing, and its disk counts toward the plan's disk
total whether it runs or not (billing.md). This amends I-484, which kept
`Disk is still billed.` as a fact worth a line. The snapshot's id went
too: nobody types a 36-character id from a stop line, and `repose
snapshots` lists it where it is used. The size stays, since it is what
the stop's time went on. `repose stop` prints `Stopped NAME in 11s with
a 2.1 GB snapshot.`, `Stopped NAME in 6.2s.` with `--no-snapshot`, and
`NAME is already stopped.`; the size is that of the snapshot the stop
op's result names (`snapshot_id`, which the api has set since
recordSnapshot), so a stop whose snapshot failed (I-158, which says so
on stderr) never shows an older snapshot's size. With an api that names
none, the newest snapshot is used when a stop took it and the project
carries no error. The same false claim went from the other places that
carried it: `repose fork`'s footer after its table ("billed like any
project", cut whole under I-484), its `--help`, and the public docs
(lifecycle, index, cli, machine: "costs only its disk", "billed like
any project", "the larger disk is billed", "hours are billed at the
size") now say what counts toward the plan. lifecycle.md also stops
promising a cost and a projected monthly cost on the project page, which
shows neither. Tests: `TestStopLineSaysWhatTheStopDid`,
`TestStopNamesInterruptedAgents` and the fork summary test, updated.

**I-571. A snapshot reads eight chunks at a time, around the page
cache, and hostd logs a stop's phases.** (dogfood, 2026-10-07: stops of
24 s and 43 s.) hostd's log on host-01 for the two stops: waterville
froze in 160 ms, was down 5.1 s after the stop began and finished its
snapshot (9.8 GB used, 2.77 GB stored) at 24.1 s; parth-event froze in
970 ms, was down at 6.2 s and finished its snapshot (5.1 GB used,
0.96 GB stored) at 42.4 s. Every other stop since 2026-10-01 holding
2 GB or more read its used data at 370 to 530 MB/s, so a stop's time
was the snapshot's, about a second for each 400 MB used; parth-event's 121 MB/s was an outlier
whose cause the log cannot show (no overlapping snapshot or restore on
the host; kanali, on the same data disk, was out of memory at the time).
The guest's shutdown took 3 to 6 s in 68 of 87 stops since 2026-09-28.
I-404's 650 to 700 MB/s was the read alone, measured on volumes a
restore had just written. The pipeline read one 4 MiB chunk, handed it
to zstd, and only then read the next. Measured on host-01 against
waterville's stopped volume (read only, 2026-10-07): one O_DIRECT
reader 637 to 690 MB/s, eight at once 1,277 to 1,715 MB/s; the same
2.5 GB of it through `zstd -T4 -3` went at 365 MB/s from one buffered
reader and 898 MB/s from eight O_DIRECT readers (987 at `-T8`). This
amends I-404, which tried eight O_DIRECT reads in flight and kept the
page cache because the time was the same on those volumes. writeExtents
now reads `snapshotReaders` (8) chunks at once into aligned buffers and
emits them in order, so the stream is byte for byte the one the serial
reader wrote; a chunk goes through O_DIRECT when the filesystem block,
the device size and the chunk allow it, through the page cache
otherwise or when the direct read fails. Reading around the page cache
also stops a snapshot from filling the host's page cache with a guest's
disk. zstd stays at `-T4`: the guests share those cores. Expected: a
stop the size of waterville's in about 11 s instead of 24; to be checked
on host-01 after the hostd switch. Phase timings, durations only:
`snapshot done` gains `freeze_ms` and `read_wait_ms` (how long the
stream waited for the device; close to `duration_ms` means the disk set
the pace, since the stream is written to zstd through a pipe and a
plain read time would include zstd's and the upload's pauses); stopGuest logs
`guest stopped` with `power_off_ms`, `duration_ms` and `escalated`
(`none`, `hypervisor`, `kill`); a stop with a snapshot logs `stop
timings` with `down_ms` and `total_ms`. RUNBOOK "Snapshot, stop or
destroy slow" says how to read them. The CLI and the api add nothing:
the CLI printed 24 s for a hostd command of 24.2 s. Tests:
`TestParallelSnapshotReadMatchesSerial` (buffered and O_DIRECT streams
equal the old serial reader's, including unaligned ranges, a range
past the device and an unaligned device size, and restore to the
source), `TestParallelSnapshotReadStopsOnAFailedRead`,
`TestParallelSnapshotReadStopsWhenTheWriterFails`,
`TestStopLogsItsPhases`. *Not done:* incremental snapshots (I-404).

**I-572. A guest's shutdown waits at most 10 s for dev's user
manager.** (found with I-571, 2026-10-07) The only unit any guest console on host-01
showed holding a shutdown was `user@1000.service` ("A stop job is running
for User Manager for UID 1000", 76 such lines across the consoles,
2026-10-07), which holds every tmux pane, shell and agent; one of
waterville's earlier stops waited 22 s on it, and 10 of 87 stops since
2026-09-28 took 10 s or more to go down (up to 27 s, and once 88 s). Its stop bound was
systemd's 2 minutes. `systemd.services."user@".serviceConfig.TimeoutStopSec
= "10s"` (users.nix) kills what is left of it after 10 s. A process that
has ignored SIGTERM and SIGHUP for 10 s during a poweroff is not
finishing anything, and a stop's snapshot is taken before the shutdown
begins (I-404); a `--no-snapshot` stop keeps the disk as the kill left
it, which is what a crash leaves and ext4's journal recovers. A
switch never restarts `user@`, but its daemon-reload rereads the unit,
so a running guest's next stop is bounded once a switch has applied the
base that carries it. Test: guest-base
asserts `TimeoutStopUSec=10s` on `user@1000.service`.
