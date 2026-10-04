# repose: full design

Status: agreed 2026-09-17 after a structured design interview. This is the
whole system. Every other doc under `docs/` is a slice of this one; when a slice
needs more detail than this doc carries, the slice wins on detail and this doc
wins on intent. Changes go through `DECISIONS.md`.

## 1. The one idea

A developer's laptop is the wrong place for a coding agent to run for six
hours. It sleeps, it loses Wi-Fi, it gets closed and carried to the gym. repose
gives each project a persistent Linux microVM on a shared host, provisioned from
a Nix configuration, reachable over SSH through a gateway, where an agent runs
inside tmux and keeps going. The developer checks back from the laptop, a phone,
or an IDE's remote mode. The environment is the same one every time because it
is a NixOS closure, not a snowball of apt installs.

The commercial idea is the same sentence: devs will pay a flat monthly amount to
not think about this, and power users will pay by the hour past that.

## 2. What a user experiences

```
$ cd ~/code/todo-app
$ repose login                     # browser opens, Logto, GitHub sign-in, done
$ repose run                       # creates project "todo-app" on first use,
                                    # boots a guest, syncs the working tree,
                                    # attaches to tmux inside it
dev@todo-app:~/todo-app$            # tmux session "todo-app", window "shell"
^b d                                # detach; the guest keeps running
$ repose run "finish the auth flow, run the tests, commit"
                                    # opens tmux window "claude", starts Claude
                                    # Code's TUI, types the prompt
$ repose status
todo-app   large   running  2h14m   claude: working   $0.31 so far today
$ repose open 3000                 # http://localhost:3000 -> guest's :3000
$ repose attach                    # back into tmux, see what the agent did
$ repose stop                      # snapshot, then deallocate; disk billed only
```

Everything else (`secrets`, `config`, `snapshots`, `logs`, `destroy`, `start`)
is a variation on that loop. The dashboard at `repose.herakraft.co` shows the
same projects, their cost, secrets, and a config menu for people who do not
write Nix.

## 3. Tenancy model

- A **user** is a Logto identity, signed in with GitHub. One user, one card,
  many projects. No teams in the first release.
- A **project** is one microVM (the **guest**) plus its thin volume, its
  snapshots, its Nix fragment, and its metering rows. Projects are identified by
  the git remote URL plus the user, overridable with `--name`. Nothing is
  written into the user's repository.
- A **host** is an Azure VM running NixOS that holds many guests from many
  users. Isolation between users is the hypervisor boundary (Cloud Hypervisor on
  KVM), never a container boundary.
- A project is pinned to one host for its lifetime. It moves only by restore
  from snapshot onto another host.

Each user's project count and per-size limits are enforced in the API, not the
host: a new account may run 3 projects and 1 XL until they have paid an invoice.

## 4. Hosts

**Hardware.** Azure Intel Dsv7 family (Granite Rapids; the subscription
cannot create v5 or v6 sizes, DECISIONS I-39), East US, created with
`--security-type Standard`. The launch size is `Standard_D64s_v7` (64 vCPU,
256 GB). Until launch the single host is `Standard_D16s_v7` (16 vCPU, 64 GB,
about $770 a month on-demand), which holds 7 large or 14 small running guests
with the 8 GB host reserve; stopped projects cost only disk, so 20 projects
fit as long as about a dozen small ones run at once. If that is tight,
`D32s_v7` (128 GB, about $1,540 a month) holds 15 large. The host size is an
OpenTofu variable; growing it is deallocate, resize, start, with guests
stopped for the minutes it takes, because the guest volumes live on the
managed data disk, not the local temp disk. See DECISIONS I-14. Trusted Launch and Confidential
VMs disable nested virtualization. AMD sizes (`Da*`, `Ea*`, and the `v7` AMD
line this repo's dev box runs on) are excluded: measured nested-KVM penalties
on Azure AMD are 50 to 90 percent versus roughly 10 percent on Intel. ARM sizes
do not support nested virtualization at all.

**OS.** NixOS, installed onto the stock Ubuntu image with `nixos-anywhere`
from `nix/hosts/`. The host configuration declares:

- `hostd` (Go, systemd service) and nothing else that touches tenants.
- A bridge `br-guests` with a per-host `/22` from `10.64.0.0/12`, allocated by
  the control plane at host registration.
- nftables: NAT for guest egress, default deny guest-to-guest and
  guest-to-host, a drop rule for `169.254.169.254` (Azure IMDS hands out
  managed-identity tokens), per-guest egress shaping at 200 Mbit/s.
- An LVM thin pool `vg-guests/thin` on one Premium SSD v2 managed data disk,
  2 TB to start, one thin volume per guest.
- `virtiofsd` per guest, exporting `/nix/store` read-only.
- Kernel with KVM enabled, `vhost_vsock`, `nf_tables`, and the Cloud Hypervisor
  package from nixpkgs (v53 at time of writing).
- WireGuard client to the edge (outbound only), Fluent Bit, node_exporter.
- No public IP. Inbound NSG: nothing. Outbound: unrestricted (guests need it).

**Capacity.** Memory is the binding constraint and is never oversubscribed.
The host reserve is 8 GB below 128 GB of RAM and 16 GB above. A `D64s_v7`
fits 30 large guests, 55 small, or 15 XL, in any mix; a `D16s_v7` fits 7
large or 14 small. v7 sizes expose disks over NVMe only: OS at
`/dev/nvme0n1`, the data disk resolved at install time (DECISIONS I-41). CPU is oversubscribed 2:1 against the 64 vCPU. Capacity is added manually:
an alert fires at 80 percent reserved memory and a human runs the OpenTofu
apply for the next host.

**Benchmark gate.** Deferred (DECISIONS I-12): the separate M0 benchmark
was not run; the first M1 host measured itself instead (`RESEARCH.md` §11
onwards). The rule stands: if nested virtualization costs more than roughly
20 percent on CPU, disk or network against a plain Azure VM of the guest's
size, hosts move to Hetzner metal and nothing else in this design changes.

## 5. Guests

**Runtime.** One Cloud Hypervisor microVM per project, launched by `hostd` as a
transient systemd unit from a runner package built by `microvm.nix`. The
host's `/nix/store` is mounted read-only over virtio-fs at `/nix/store`;
the guest's own writable overlay for `/nix/var` and everything else lives on
its thin volume. Guests never build; the host builds and the guest sees the
result.

**Size classes.**

| Class | vCPU | RAM | Default volume | Monthly cap (see PRICING) |
|---|---|---|---|---|
| small | 2 | 4 GB | 20 GB | $49 |
| large | 4 | 8 GB | 40 GB | $99 |
| xl | 8 | 16 GB | 80 GB | $199 |

Volumes are thin-provisioned, resizable upward from the CLI, billed on
allocated size. Resize is one `lvextend` on the host plus an online filesystem
grow issued by `guestd`.

**Base image (the platform NixOS module).** Everything a guest has regardless
of user config:

- NixOS unstable, kernel with overlayfs, cgroups v2, nf_tables, vsock.
- User `dev`, uid 1000, passwordless sudo, in `docker`. Nothing runs as root
  except `guestd` (for freeze and switch) and system services. Claude Code
  refuses `--dangerously-skip-permissions` as root, so `dev` is not optional.
- `guestd` (Go) on vsock, no network listener.
- Docker daemon (rootful), containerd, `docker compose`.
- tmux with `set -g set-clipboard on` (OSC52 copy-out over SSH), mouse off
  (DECISIONS I-364),
  256-colour, a session named after the project created at boot.
- OpenSSH trusting the platform CA, `AuthorizedPrincipalsFile` lists the
  project id; password auth off; only `dev` may log in.
- The agents from the platform overlay: `claude-code`, `opencode`, `codex`,
  `gemini-cli`, `pi-coding-agent`. Each has a `repose` wrapper that installs
  the notification hooks and names its tmux window.
- One headed Chromium for the agents on the X display `:99` (TigerVNC's
  Xvnc, openbox), shared by Playwright MCP and chrome-devtools-mcp over
  CDP, plus `playwright-driver.browsers` (DECISIONS I-246). The viewer
  (websockify and repose's page) stays off until `repose browser` asks,
  and only views that same browser, at the size of the user's tab
  (`features/browser.md`, I-292).
- Toolchain from `nix/guest/base/tool-list.nix`: node 24, pnpm, python
  3.12, uv, go, rustup, just, ripgrep, jq, gh, git, direnv with nix-direnv,
  starship, zoxide, eza, a C toolchain and everyday CLIs (I-218).
- `fs.inotify.max_user_watches=1048576`, `max_user_instances=1024`.
- `TZ` and `LANG=C.UTF-8` set from the user's profile; `TZ` defaults to the
  laptop's zone as reported by the CLI at project creation.
- Fluent Bit is *not* in the guest. Console output goes to hostd over the
  serial device; application logs stay in the guest.
- `repose-guest-profile`: a small script the CLI's `open`, `sync` and hooks
  rely on, documented in `interfaces/guest-conventions.md`.

**User config (the fragment).** A home-manager module supplied by the user,
either written by hand or generated from the dashboard's menu. It is merged on
top of the base. Evaluation is pure, restricted (`restrict-eval`, no
import-from-derivation, no `builtins.fetch*` outside the flake inputs the
platform provides), and capped at 60 seconds. Builds are sandboxed, capped at
30 minutes wall time and 8 cores, with substitutes from cache.nixos.org and the
platform's overlay cache. Errors are shown verbatim in the CLI with the file
and line in the fragment. See `workstreams/12-nix-config-pipeline.md`.

**Applying a change.** The host builds the new guest system closure into the
shared store. `hostd` tells `guestd` over vsock to run the new system's
`switch-to-configuration switch`. The guest does not reboot unless the kernel,
initrd, or the virtio-fs share layout changed, in which case the CLI says so
and asks. Base bumps from the platform are applied the same way, weekly, with
a per-project `hold` flag and a changelog line in `repose status`.

**Boot.** A stopped guest starts in under 5 seconds because its closure is
already in the host store. A new project's first start is bounded by building
its fragment; the CLI streams the build.

## 6. Storage and snapshots

- Thin volume per guest on `vg-guests/thin`, ext4, mounted by the guest as its
  root overlay's upper dir and `/home`.
- Snapshot = `guestd` runs `fsfreeze -f /` , hostd takes an LVM thin snapshot,
  `guestd` runs `fsfreeze -u`, freeze window under one second. The snapshot's
  used blocks (DECISIONS I-164) are streamed `zstd`-compressed to Azure Blob (`repose-snapshots` container,
  path `<user>/<project>/<timestamp>.img.zst`), then the LVM snapshot is
  removed.
- Schedule: nightly at 03:00 in the host's timezone, and on every `stop`.
  Retain 7 daily. After `destroy`, keep the last snapshot 30 days. After
  account cancellation, stop all guests, keep snapshots 30 days, then delete.
- Restore creates a fresh thin volume from the stream, on any host. This is
  the only way a project moves hosts.
- Blob uses Azure server-side encryption. Per-project LUKS is a documented
  upgrade (DECISIONS: R3-Q10), not built.
- Every snapshot writes a `snapshots` row; the CLI's `snapshots list` and
  `snapshots restore` read it.

## 7. Network

```
laptop ──ssh──▶ edge (public IP)  ──wireguard──▶ host br-guests ──▶ guest
                 gateway :22                        10.64.x.y
                 wg hub  :51820/udp
laptop ──https─▶ Coolify VM (public IP) : api, web, logto
host ──grpc/mtls (outbound)──▶ api.repose.herakraft.co
host ──wireguard (outbound)──▶ edge
guest ──NAT──▶ internet (shaped)
guest ──vsock──▶ hostd
```

- Hosts have no inbound. They dial the edge's WireGuard endpoint with a key
  issued at registration, and the API's gRPC endpoint with a client
  certificate issued at registration.
- The edge advertises `10.64.0.0/12` into its routing table via the per-host
  WireGuard peers. The gateway dials `10.64.x.y:22` for a guest.
- Guests reach the edge's WireGuard address only on the hook-ingest port and
  the noVNC relay; everything else is egress to the internet.
- Preview URLs (`3000-todo-app-heracraft.repose.herakraft.co`; the hostname
  carries the handle because slugs are unique per user, not globally) are
  not built in the first release. The design: the edge terminates TLS with a wildcard cert,
  authenticates with a cookie from Logto, and proxies to the guest port over
  WireGuard. It is documented in `features/ports-and-previews.md` so the
  gateway is written with that path in mind.

## 8. Identity and access

- **Login.** Logto (self-hosted, already running) with the GitHub connector.
  The CLI uses authorization code with PKCE and a loopback redirect; when no
  browser is available it falls back to device code (Logto supports RFC 8628
  since v1.38). The CLI stores the refresh token in the OS keychain where one
  exists, else `~/.config/repose/credentials.json` mode 0600.
- **API auth.** JWT access tokens for the `https://api.repose.herakraft.co`
  resource, verified by the API against Logto's JWKS.
- **SSH.** The API holds an SSH CA (ed25519, key in the secrets store). On
  `run` and `attach` the CLI sends the public half of its own key
  (`~/.ssh/repose/id_ed25519`, generated without a passphrase and used for
  nothing else; the user's keys are never touched, DECISIONS I-149) and
  gets back a certificate with principal `<project-id>`, valid 24 hours,
  extensions `permit-agent-forwarding,permit-port-forwarding,permit-pty`.
  The CLI writes `~/.ssh/repose/hosts` with one `Host` block per project
  (multiplexed, so a command makes one connection), and
  `~/.ssh/repose/config`, included from the user's main config by a line
  the CLI adds once and verifies with `ssh -G` (I-151). That config runs
  `repose ssh-prepare` before any ssh to `<project>.repose` reads the
  blocks, so plain ssh, scp, rsync, git and editors reach every project,
  one created elsewhere or with an expired certificate included (I-281).
  The CLI refreshes the certificate silently while the Logto refresh
  token is valid.
- **Gateway.** Go, `golang.org/x/crypto/ssh`. Login name is
  `<project-slug>.<user-handle>` (e.g. `todo-app.heracraft`). The gateway
  verifies the certificate against the CA, resolves the project through the
  API, and proxies the connection to the guest, presenting a host certificate
  signed by the same CA so the client verifies the gateway without
  known_hosts prompts. It never terminates the user's session into a shell
  itself; it is a TCP-level relay after auth.
- **Guest side.** sshd trusts the CA and requires principal `<project-id>`.
  A certificate for one project cannot open another.
- **Revocation.** Logout or a stolen laptop: the API adds the certificate's
  serial to a revocation list the gateway checks; 12-hour expiry bounds the
  rest.

## 9. Control plane

Go binaries in one module (`cmd/`), one Postgres.

- **`api`**: HTTP JSON for the CLI and dashboard, gRPC server for hosts,
  scheduler, SSH CA, secrets, metering aggregation, Paddle webhooks, hook
  ingest, notification fan-out. Stateless; scale by replicas behind Coolify.
- **`hostd`**: on each host. Holds the gRPC stream, executes guest lifecycle
  (create, start, stop, destroy, resize, snapshot, restore, apply-config),
  runs builds, reports metrics and guest process samples.
- **`guestd`**: inside each guest, vsock only. Freeze, switch, resize-fs,
  tmux setup, process sampling, hook relay, notification of readiness.
- **`gateway`**: on the edge. SSH relay plus, later, the HTTPS preview proxy.
- **`repose`**: the CLI.
- **`repose-admin`**: the operator's CLI, run in the api container.
- **`repose-hook`**: in each guest; agent hooks run it to POST events to
  guestd's hook socket.

Postgres holds users, projects, hosts, guests, volumes, snapshots, secrets
(ciphertext), certificates issued, meter samples, invoices, notifications,
config revisions, build logs. Schema in `interfaces/db-schema.md`.

The scheduler is a function, not a service: on `create`, pick the host with
the most free reserved memory that is `ready`, reserve the guest's memory in a
transaction, and send the create command. A host that has not heartbeated for
90 seconds is `unreachable` and receives no placements.

**Deployment.** `api` and the dashboard are single-container Coolify
applications (Dockerfile, health check, so they get rolling deploys) on an
Ubuntu LTS VM in Azure that the owner's existing Coolify instance manages as a
server (DECISIONS I-83); Logto runs there the same way, and Coolify itself
runs where it already did. Postgres is a Coolify-managed database on that VM
with nightly dumps to Cloudflare R2. The edge is a separate NixOS VM
(`nix/edge/`) deployed with `nixos-rebuild switch` because the gateway needs
a raw port and WireGuard needs the kernel module, and a Coolify port mapping
would cost it rolling deploys. Coolify cannot manage NixOS servers, hence the
Ubuntu VM. A switch of the edge keeps open SSH sessions: systemd holds
port 22, and the old gateway hands new connections to the new one and
serves its own until they end (DECISIONS I-470..I-472). The edge is still
one VM, so its reboot ends every session; the way to two edges behind a
load balancer is DECISIONS I-473.

## 10. The CLI

`repose`, one static Go binary, installed by `curl | sh` or `nix run`. The
command list and flags are `apps/web/src/content/docs/cli.md`, which
`internal/cli/docs_test.go` keeps equal to the binary. Global `--project`
overrides the cwd-derived project.

Project resolution: read `git remote get-url origin`, normalise
(`git@github.com:a/b.git` and `https://github.com/a/b` are the same), look up
`(user, remote)` in `~/.config/repose/projects.json`, then the API. No remote
and no `--name` is an error with a one-line fix.

`run` sequence:

1. Resolve project; create it if new (size from `--size`, default large;
   agent default claude).
2. Ensure the guest is running; stream build output if a config build is
   pending.
3. Get or refresh the SSH certificate, write SSH config.
4. Sync: the laptop's commits travel in a git bundle over SSH (the guest
   never fetches from origin, nothing is pushed), the laptop's `HEAD` is
   checked out, and staged and unstaged changes follow as two diffs, with
   untracked files as a tar. If the guest's tree has changes of its own,
   refuse and offer `--stash-remote` or `--discard-remote`
   (`features/sync-at-launch.md`). Since DECISIONS I-367 `run` does this
   only into a guest whose checkout has no commit yet; `repose sync` does
   it on demand.
5. Sync credential files listed in `features/secrets.md` (gh, Codex,
   opencode). Never `~/.claude/.credentials.json`.
6. If a prompt was given: open a tmux window named after the agent, start the
   agent's TUI, send the prompt. Otherwise attach to the session's current
   window.

All of it is idempotent; running `repose run` twice attaches twice.

## 11. Agents, browser, MCP

- Agents ship from `nix/overlay/agents/`, a platform-owned overlay that
  repackages upstream binary releases (as the community flakes do) and is
  bumped by a scheduled job that opens a PR with the version diff. nixpkgs is
  the fallback, never the primary, because it lags Claude Code by weeks and
  Gemini CLI by months.
- Each agent's wrapper installs hooks: on completion and on needs-input, POST
  to `guestd`'s hook endpoint (a Unix socket), which relays over vsock to
  hostd, which forwards to the API. Claude Code uses its `Notification` and
  `Stop` hooks; the others use their equivalents or a tmux pane-idle
  heuristic where no hook exists, and the doc for that agent says which.
- Claude login happens inside a guest, once per user: the first `run` with
  agent claude detects no credentials and runs `claude` so the user pastes
  the code from the browser. The file Claude Code writes is shared into
  every guest of that user on the host, so the other projects are signed in
  too (DECISIONS I-278). `repose secrets set CLAUDE_CODE_OAUTH_TOKEN` (from `claude
  setup-token` on the laptop) is the headless fallback and loses Remote
  Control, connectors and Claude in Chrome. Anthropic's terms require each
  user to authenticate with their own credentials on hosted platforms; the
  platform never reads, copies or proxies Claude auth; the login share is
  storage the user's own guests share, which hostd creates but never opens.
- Browser: one headed Chromium on a virtual display, shared by Playwright
  MCP and chrome-devtools-mcp, in every guest (I-246). `repose browser`
  starts the viewer and forwards its port in the background so the user
  can watch or take over the browser the agent is using, in one command
  (I-292). Claude in Chrome cannot work from a guest; `repose browser
  bridge` (I-296) reverse-tunnels the laptop's own Chrome (its DevTools
  switch, Chrome 144+) to the guest's endpoint so the same two MCP
  servers drive the laptop's browser, logins included, while the laptop
  is open.
- MCP: HTTP and API-backed servers work as on a laptop. Laptop-bound stdio
  servers are unsupported in the first release; `repose mcp forward` (wrap
  with mcp-proxy, reverse-tunnel, register in the guest) is the planned path
  and is written up in `features/agents.md`.

## 12. Secrets

Three kinds, three treatments:

1. **Tool logins the laptop already has**: `~/.config/gh/hosts.yml`,
   `~/.codex/auth.json`, `~/.local/share/opencode/auth.json`, and the
   laptop's whole effective git config minus a denylist (credentials, ssh,
   signing, proxies; DECISIONS I-195). Copied at `run` over SSH into the guest,
   mode 0600, owned by `dev`. The platform never sees them.
2. **Claude Code**: never copied. The user's own `/login` writes it to a
   per-user directory on the host that only that user's guests mount
   (DECISIONS I-278). See section 11.
3. **Named secrets** (`repose secrets set NAME`): encrypted by the API with a
   per-user data key, itself wrapped by an Azure Key Vault key. Ciphertext and wrapped DEK in Postgres. Delivered to
   the guest at start as `/run/repose/secrets/NAME` (tmpfs, 0400 dev) and
   exported into login shells as environment variables. Rotating the Key Vault
   key re-wraps DEKs without touching ciphertext.

Guest disks are protected by Azure's encryption of the host's managed disk.
An operator with root on a host can read a tenant's volume; per-project LUKS
closes that and is a recorded upgrade.

## 13. Notifications

`api` receives hook events `(project, agent, kind in {completed, needs_input,
error}, summary)` and delivers by the user's configured channels: email
(Resend) and ntfy (any ntfy-compatible endpoint the user sets, so phone apps
work). Telegram and Discord webhooks are next. Events also show in `repose
status` and the dashboard. Claude Code Remote Control and channels remain
available to users who log in with a subscription; the platform does nothing
to them.

## 14. Billing and metering

A monthly plan through Paddle, chosen before the first machine starts,
with a card at checkout and a week free (DECISIONS I-289, superseding the
hourly meter this section first described). Solo, $29 a month, buys 8 GB
of memory that may run at once (one `large`, or two `small`), 100 GB of
disk and 250 GB of egress; Plus, $59, buys 16 GB, 250 GB and 500 GB;
Pro, $99, buys 32 GB, 500 GB and 1000 GB (I-362).
Projects are unlimited while stopped (10, 25 and 50 in all); egress past the
allowance is $0.05 a GB as one line on the next invoice, and at four times
the allowance the machines stop for the period. Paddle is the merchant of
record, so tax is its problem. `PRICING.md` has the rules, the cost floor
and the reasoning.

Meters, sampled by hostd every 60 seconds and aggregated hourly by the API
into `usage_hours`, are the record of what ran: guest-hours by size class
(what `repose status` shows), disk allocated, egress bytes (what the
overage line and the hard stop read). Nothing is priced per hour.

A failed payment refuses new starts from day 0, stops the running machines
on day 3, and destroys nothing for 30 days.

## 15. Observability and anti-abuse

Ship everything, invade nothing.

- Host: node_exporter, hostd's own metrics (`repose_host_*`), Cloud
  Hypervisor per-guest stats, LVM pool usage, nftables byte counters per
  guest, Fluent Bit shipping journald and each guest's console log to the
  existing Loki. Prometheus is added to the personal Grafana server and
  scrapes hosts over WireGuard.
- Guest process samples: `guestd` reports every 60 seconds the list of
  process names with CPU seconds, RSS, and per-process network bytes where
  cgroups expose them. No arguments, no environment, no file paths, no
  terminal contents. This boundary is written in the privacy policy in the
  same words.
- Signals recorded from day one because later policies need them: SSH
  sessions open, tmux clients attached, agent process present and its window
  state, CPU busy fraction, network bytes, Docker containers running. These
  decide the idle policy and the tier shapes later, and they are what
  anti-abuse looks at (a process named `xmrig`, sustained 100 percent CPU
  with no session for days, egress in the terabytes).
- Control plane: structured JSON logs, OpenTelemetry traces exported to
  nothing yet (an OTLP endpoint env var, unset), Prometheus metrics.
- Abuse response on an account is manual in the first release:
  `repose-admin suspend <user>` stops guests and freezes billing. Rate
  limits: 200 Mbit/s egress shaping, 10 projects per user, 3 until first
  paid invoice. Three things are automatic and never touch the account
  (DECISIONS I-238..I-240): a guest cannot connect out to tcp 25 or the
  mining pools' default ports, and its new outbound flows are limited to
  200 a second (dropped past it, counted, alerted); a guest whose process
  sample names a known miner is stopped with a snapshot, and the third
  such stop in 24 hours holds the project until `repose-admin abuse
  clear`; six hours at full CPU with no session, tmux client or agent
  is an alert.

## 16. Provider portability

Only these things are Azure-specific: the OpenTofu host module, the managed
disk, Blob as snapshot target, Key Vault as the key wrapper, and the Coolify
VM's cloud-init. Hosts dial out for everything, so a Hetzner AX162-R (about a
tenth of the cost per guest) becomes a second OpenTofu module plus a
snapshot-target abstraction (Blob or S3-compatible). The control plane moves
by restoring one Postgres dump into a Coolify elsewhere and re-pointing DNS.

## 17. Repository layout

```
repose/
  go.mod                     module github.com/heracraft/repose
  cmd/api  cmd/hostd  cmd/guestd  cmd/gateway  cmd/repose  cmd/repose-admin
  cmd/repose-hook            (plus fakeapi, fake-logto, hostdev for tests)
  internal/                  shared Go: proto, db, ca, secrets, meter, ...
  proto/                     hostd.proto, guestd vsock messages
  nix/
    flake.nix                one flake: hosts, edge, guest base, overlay, dev shell
    hosts/                   NixOS host config, nixos-anywhere disko layout
    edge/                    NixOS edge config
    guest/base/              platform guest module (tools.nix, tool-list.nix)
    guest/microvm.nix        microvm.nix wiring, runner package function
    overlay/agents/          claude-code, opencode, codex, gemini-cli, pi
  infra/                     OpenTofu: azure/, dns/, bootstrap/, policy/
  apps/web/                  SvelteKit dashboard (existing)
  packages/                  TS packages: ui, eslint-config, typescript-config
  docs/
```

Turborepo keeps running only the TypeScript side. The old
`packages/core/flake.nix` is gone; its tools are
`nix/guest/base/tool-list.nix`, shared with the dev shell.

## 18. Out of scope for the first release

Teams and shared projects. Preview URLs. Idle auto-stop. Per-project LUKS.
Central build farm. Automatic host provisioning. GPU guests. `repose mcp
forward` (`repose browser bridge` shipped, I-296). Telegram and Discord delivery. Any
provider other than Azure hosts. Each has a section in `DECISIONS.md` saying
when it becomes in scope.

## 19. Known risks

- Nested virtualization on Azure is officially unsupported by Microsoft even
  though Microsoft ships it in AKS. The benchmark gate (deferred, I-12)
  exists for this.
- Cloud Hypervisor's virtio-fs snapshot/restore matured only in v52. We do not
  use CH snapshot/restore; we stop and start guests and snapshot the volume.
- microvm.nix's shared-store model means a guest cannot outlive a host
  garbage-collecting its closure. hostd registers every running and stopped
  guest's system closure as a GC root and unregisters on destroy.
- A user fragment that pulls a huge closure (CUDA, a browser build) fills the
  host store. Store usage is metered per closure and a per-project closure cap
  (20 GB) errors at build time with the offending paths listed.
- Coolify's own upgrade path (v5, PHP refactor, no date) may change deploy
  semantics; the control plane containers depend on nothing Coolify-specific.
