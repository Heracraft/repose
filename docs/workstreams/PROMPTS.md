# Launch prompts and model choice per workstream

## Model choice

| Workstream | Model | Why |
|---|---|---|
| 01 host-nixos, 02 guest-base, 12 nix-config-pipeline | Fable 5.1 | NixOS internals, microvm.nix, restricted eval: the deepest and least-documented work, and mistakes here are silent |
| 03 hostd | Fable 5.1 | systems code touching LVM, nftables, KVM, gRPC replay semantics; the tenant-safety core |
| 05 control-plane-api | Fable 5.1 | auth, CA, secrets, scheduler in one binary; a mistake is a security issue |
| 06 gateway-edge | Fable 5.1 | SSH protocol relay and certificate handling |
| 14 security | Fable 5.1 | adversarial review of everything above |
| 04 guestd | Opus 5 | well-specified, moderate |
| 09 billing | Opus 5 | money, but the shapes are conventional Stripe |
| 10 observability, 11 infra-opentofu | Opus 5 | broad but conventional |
| 07 cli, 08 dashboard, 13 notifications | Sonnet 5 | well-specified, user-facing, fast iteration |
| 00 benchmark (deferred) | Sonnet 5 | a procedure to follow |

If Fable is unavailable or rate-limited, Opus 5 takes its rows. Never
Haiku for building; it is fine for the `done-check` grep pass at the end.

The table is the split the first waves used. Since 2026-09-23 every
worker, conductor-run or `/ws`, has been Opus 5.5.

Sequential sessions work the same way: one prompt per session, same
model choice, and `STATUS.md` carries the state between them.

## The short form: `/ws <nn>`

The repo ships a project skill at `.claude/skills/ws/SKILL.md`. In a worktree,
start Claude Code and type `/ws 03`; it loads the preamble and the `03`
block below and begins. `/ws m4` and `/ws m5` run the integration
sessions (the "M<n> bring-up" sections below; M1 to M3 are done). The full
text below is what the skill expands to, kept here so it can be read and
edited in one place.

## Launching agents in parallel

Parallel agents in one checkout collide on `go.mod`, `STATUS.md`,
`DECISIONS.md` and the proto files. Each agent therefore gets its own git
worktree and branch, created by the owner (the repo rule that agents do not
create branches still holds):

```
cd the repository root (`git rev-parse --show-toplevel`)
git worktree add ../repose-ws/03-hostd -b ws/03-hostd main
cd ../repose-ws/03-hostd && claude      # paste preamble + block 03
```

One worktree per workstream, all branched from the same `main` commit.
When an agent reports done, the owner reviews and merges: `git merge
ws/03-hostd` on main, resolving the shared files (keep both sides of
`go.mod` requires and run `go mod tidy`; concatenate `STATUS.md` and
`DECISIONS.md` entries, renumbering `I-<n>` if two agents used the same
number). Merge the workstreams that own interfaces first (03, 05), then
their consumers. Delete the worktree after merging: `git worktree remove
../repose-ws/03-hostd`.

Since 2026-09-23 the conductor runs workers in rounds instead, with
pre-assigned decision numbers; `docs/ops/ORCHESTRATION.md` describes it.

Nix and Go caches are shared across worktrees (`/nix/store`, `~/go/pkg`),
so parallel builds do not multiply disk use. The store lives on the dev
box's temp disk (`docs/ops/DEV-BOX.md`); keep 40 GB free on `/nix`.

## M1 and M2 bring-up (done)

Both milestones are closed (`docs/MILESTONES.md`). The prompts those
sessions ran are in git history: `git show
d9b0090:docs/workstreams/PROMPTS.md`.

## M3 bring-up (`/ws m3` and `/ws m3-web`, done as far as one host allows)

The two sessions ran on 2026-09-20 and 2026-09-21; `docs/MILESTONES.md` M3
says what is still open. The full prompt, including the numbered steps of
"M3 bring-up / m3" that the headers of `ops/checks/*.sh` cite, is in git
history: `git show d9b0090:docs/workstreams/PROMPTS.md`.

## M4 bring-up (`/ws m4`, Opus 5, `../repose-ws/m4-billing`)

Gate (`docs/MILESTONES.md` M4): a real card is charged the right amount
for a known usage pattern (one large guest, 100 hours, 40 GB, 10 GB
egress) and the Stripe invoice matches the `usage` rows to the cent; trial
credit depletes and blocks a start at zero; a failed payment stops guests
after 3 days. Workstream 09 is merged and passes its fixture locally
(`internal/billing`, DECISIONS I-16, I-77); nothing has talked to a real
Stripe account yet. In order:

1. **Stripe objects in test mode** (`docs/ops/M4-GATE.md` §1, DECISIONS
   I-180): the owner hands over the test secret key and nothing else;
   `ops/stripe/bootstrap.sh` creates the product, meters, prices, portal
   configuration and webhook endpoint and prints the `STRIPE_*` block,
   which goes in the api's Coolify environment, never in a file here.
   Pasting it is the whole step; Coolify restarts the app itself
   (`ops/coolify.md` fact 16). The rest of this block is
   `docs/ops/M4-GATE.md` §2-§5, command by command.
2. **The fixed usage pattern, end to end**: drive it through the api
   against host-01 where a real guest can produce it (a large guest left
   running for the hours with the volume and egress the pattern names;
   the rollup fixture in 09 §7 for what the hours cannot wait for), run
   the hourly rollup, push usage, and compare the Stripe test invoice to
   `repose-admin billing explain` to the cent. Then the six webhooks
   against the real endpoint (Stripe CLI forwarding is fine), the past-due
   3-day stop with snapshot and notification, the trial-credit depletion
   blocking a start at zero, and the limit change after the first paid
   invoice. Close every row of 09 §9 with evidence.
3. **Live mode, one charge**: the owner adds their own card at
   `https://repose.herakraft.co/billing` and is charged for a known short
   pattern; the invoice, `explain` and the `usage` rows agree to the cent;
   the Stripe Tax address is collected. Refund is the owner's call.
4. `docs/PRICING.md` and `features/pricing.md` match what Stripe shows;
   `ops/RUNBOOK.md` rows for push backlog, mismatch, "user says they were
   overcharged".

Rules on top of the preamble (they were the M2 block's): never `tofu
apply`, never `force-unlock`, never touch the Coolify UI yourself; the
owner and the conductor do those, and you tell them exactly what to click
or run. Nothing about the owner's personal server (addresses, keys) goes in
any file. Read `docs/ops/coolify.md` "Coolify facts that cost a round trip
each" before touching a Coolify resource. Anything that costs money is announced to the
conductor before it runs.

## M5 bring-up (`/ws m5`, Fable 5.1, `../repose-ws/m5-release`)

Gate (`docs/MILESTONES.md` M5): `docs/CHECKLIST.md` "Release (M5)" closed
and the landing page live. This session is 14's final review plus the
release checklist, in order:

1. **Adversarial review of `main` as deployed** (14 §final): every boundary
   in `docs/SECURITY.md` re-verified on host-01 and the edge as they run
   today, the audit log, operator access, the never-log list against the
   live journals and Loki. Findings are fixed on the branch or filed as
   DECISIONS entries with an owner and a date.
2. **Rehearsals the checklist names**: snapshot restore onto a different
   host (needs a second host; the conductor adds one from `prod.tfvars`
   for the rehearsal and removes it after), host loss (deallocate that
   host, restore its projects elsewhere, users notified), the five bad
   fragments (syntax error, missing attribute, 31 minutes, closure cap,
   arbitrary fetchurl) each producing the documented error and nothing
   else.
3. **A second human**: login, run, attach, stop, start, secrets, config
   apply, snapshot restore, destroy on their own laptop, unassisted, with
   the steps they had to guess written into the docs.
4. **Release mechanics**: `repose --version`, `install.sh` on the four
   targets, Grafana dashboards and alerts wired to the owner's stack with
   runbook rows for every alert, privacy and terms published. The leaked
   key's commit (`b1a5915`) is settled (DECISIONS I-193): the key was
   rotated and the history stays.
5. Tick every row of `docs/CHECKLIST.md` "Release (M5)" with its evidence,
   and the workstream checklists' remaining rows with theirs.

## Shared preamble

Paste this first, then the workstream block.

```
You are building one workstream of repose in the repository root (`git rev-parse --show-toplevel`).
Read, in order: CLAUDE.md, docs/README.md, docs/DESIGN.md, docs/DECISIONS-INDEX.md
(then docs/DECISIONS.md at the entries you need), docs/workstreams/README.md, then your workstream doc and every docs/interfaces/
file it owns or consumes. Do not skim; the checklists reference exact names.

Rules:
- Add your claim line to docs/workstreams/STATUS.md before writing code, and
  update it when you stop, with what is done and what is not.
- Build the entire workstream, not the easy parts. Your doc's section 9
  checklist is the definition of done; docs/CHECKLIST.md applies on top.
  Close each item with the evidence it names, pasted into your final report.
  A passing build closes nothing.
- Interfaces are law. If a contract is wrong, record the change in
  docs/DECISIONS.md under "Made during implementation" as I-<next number>,
  update the interfaces/ doc and the proto in the same commit, and keep the
  old shape accepted for one release.
- Where the producer of an interface you consume does not exist yet, write
  or use the fake named in docs/interfaces/README.md.
- You are in a git worktree on a branch named ws/<nn>-<name> that the
  owner created for you. Commit there in small steps with messages that
  name the workstream. Do not create further branches, do not merge, do not
  push, do not touch main. The owner merges branches into main.
- Never install anything from nix/ on this machine; it is an AMD dev box,
  not a host. nix build and nix flake check are fine.
- Never log prompts, terminal contents, process arguments, environment
  variables, secret values, tokens, certificate bodies, or email.
- Trust the reader (CLAUDE.md, DECISIONS I-484, I-485): CLI output that
  worked says what happened and stops; docs, copy and emails give only
  help the reader asked for. `just done-check` and `release-queue add`
  run the guards.
- Run `just done-check` and `just lint` before reporting. Finish with a
  report listing every checklist item and its evidence, then what is not
  done and why.
```

## Per-workstream blocks

### 01 host-nixos

```
Workstream: docs/workstreams/01-host-nixos.md. Build nix/hosts/ so a fresh
Azure Ubuntu VM becomes a repose host via nixos-anywhere: disko layout,
kernel modules, br-guests, the nftables table in docs/interfaces/host-
conventions.md including the IMDS block and per-guest counters, tc shaping,
LVM thin pool on the data disk, virtiofsd and cloud-hypervisor packages,
WireGuard client, Fluent Bit, node_exporter, the hostd systemd unit, the
join-token path, GC roots directory, hardening. `nix flake check` must pass
and `nix build .#nixosConfigurations.host.config.system.build.toplevel`
must succeed locally. Anything that needs a real host is marked in the
checklist as such; do everything else.
```

### 02 guest-base

```
Workstream: docs/workstreams/02-guest-base.md. Build nix/guest/base/ and
nix/guest/microvm.nix: the platform guest module per docs/interfaces/guest-
conventions.md (dev user, docker, tmux config, sshd trusting the CA with
principals file, agents from nix/overlay/agents with wrappers and hooks,
headless chromium and playwright browsers, Xvfb/x11vnc/noVNC socket-
activated, inotify sysctls, TZ/LANG, guestd unit, secrets tmpfs, base-
version stamp), and the mkGuest function that composes base plus a user
fragment into a microvm.nix Cloud Hypervisor runner with the virtio-fs
store share and the thin-volume overlay. Retire packages/core/flake.nix
into nix/guest/base/tools.nix. `nix build` of a runner with an empty
fragment must succeed locally, and the runner must boot under
cloud-hypervisor on any KVM machine if one is available; if not, say so.
```

### 03 hostd

```
Workstream: docs/workstreams/03-hostd.md. Build cmd/hostd and its internal
packages: Register and the mTLS Session stream with reconnect and command
replay per docs/interfaces/grpc-hostd.md, the guest state machine, every
command (Create, Start, Stop, Destroy, Resize, Build, ApplyConfig,
Snapshot, Restore, UpdateSecrets, SetPrincipals, Exec, Drain) implemented
against LVM, nftables, tc, systemd-run, the Cloud Hypervisor API socket,
virtiofsd and the microvm.nix runner, bbolt state and reconciliation at
start, GC roots, samples every 60 s, console capture, snapshot streaming to
Blob, Prometheus metrics, and the one-host dev driver cmd/hostdev
(DECISIONS I-17). Generate the Go code from proto/ with buf.
Everything that shells out goes behind an interface with a fake so the
state machine is unit-tested without a host; the host-level tests are
marked as such in the checklist.
```

### 04 guestd

```
Workstream: docs/workstreams/04-guestd.md. Build cmd/guestd: the vsock
server per docs/interfaces/vsock-guestd.md with the unix-socket dev mode,
every request (Ping, Freeze with the thaw watchdog, Thaw, Switch, GrowFs,
WriteSecrets, SetPrincipals, SetupProject with tmux, Sample from /proc and
tmux, Exec, Shutdown), the Notify messages, the hook socket at
/run/repose/hooks.sock and the repose-hook helper. Unit-test everything
against a fake filesystem and a real tmux where available.
```

### 05 control-plane-api

```
Workstream: docs/workstreams/05-control-plane-api.md. Build cmd/api and
cmd/repose-admin: every route in docs/interfaces/api.md with Logto JWT
verification, the Postgres schema in docs/interfaces/db-schema.md as
migrations, the gRPC server side of docs/interfaces/grpc-hostd.md with
the fake hostd for tests, the scheduler, SSH CA issue and revoke and the
guest host-key generation (I-3), envelope-encrypted secrets with a fake Key
Vault for tests, config revisions with SSE build logs, snapshots, events
ingest and the notification outbox, meter ingest and hourly rollup,
internal routes for the gateway, rate limits, the full repose-admin
surface named in DECISIONS I-9, Dockerfile with health check for Coolify,
OpenTelemetry wiring. Integration tests run against a real Postgres.
```

### 06 gateway-edge

```
Workstream: docs/workstreams/06-gateway-edge.md. Build cmd/gateway and
nix/edge/: the SSH gateway per docs/interfaces/ssh-gateway.md (certificate
verification, revocation cache, route lookup, terminate-and-redial with a
gateway-issued 5-minute certificate from /internal/gateway-certs, channel
relay for session, port forwards and agent forwarding, host certificate,
session reporting, metrics), wgsync from /internal/hosts, the hook ingest
forwarder, the preview-proxy stub on 443, and the edge NixOS config with
WireGuard hub and firewall. Test the relay end to end with
internal/ca/testca and an in-process SSH server standing in for a guest.
```

### 07 cli

```
Workstream: docs/workstreams/07-cli.md. Build cmd/repose: every command
listed there including events and notify (DECISIONS I-8), Logto login with
PKCE loopback and device-code fallback, project resolution and remote
normalisation per docs/interfaces/cli-config.md, certificate refresh and
SSH config writing per docs/interfaces/ssh-gateway.md, the run sequence
with the git-based sync and dirty-tree refusal, credential file sync per
docs/interfaces/guest-conventions.md, tmux attach and prompt send, open
<port> and --desktop, status, SSE build log rendering, exit codes,
install.sh and goreleaser config for four platforms, shell completion.
Test against internal/fakes/api and a local sshd.
```

### 08 dashboard

```
Workstream: docs/workstreams/08-dashboard.md, and read docs/DESIGN-
LANGUAGE.md before any markup. Build apps/web: Logto SPA login, projects
list with status and cost, project detail with events and signals, secrets,
the config menu from GET /catalog rendering to a fragment plus a raw
fragment editor showing build errors, snapshots, billing card setup and
portal, notification settings with the test button, Dockerfile and health
check for Coolify rolling deploys. Copy the recruiting app's typography,
palette, buttons and fields; do not copy its chip, segmented or card
radios. Test against internal/fakes/api.
```

### 09 billing

```
Workstream: docs/workstreams/09-billing.md. Build the billing package and
its api routes: Stripe customer at signup, SetupIntent card on file, the
trial credit ledger (one day of compute, I-205), hourly usage records from usage_hours, the
per-project monthly cap per size, storage and egress lines, monthly
invoices, past_due handling with the 3-day stop and 30-day retention,
webhooks, the reconciliation job, and the test-mode fixture for the
CHECKLIST usage pattern that must match to the cent.
```

### 10 observability

```
Workstream: docs/workstreams/10-observability.md. Build the shared
telemetry package (structured logs with the never-log list enforced by a
test, repose_* metrics, OTel wiring), the Fluent Bit config in nix/hosts,
the Prometheus scrape config and WireGuard peer for the personal Grafana
server, and every dashboard and alert rule listed in docs/CHECKLIST.md as
Grafana JSON and Prometheus rules under ops/grafana/.
```

### 11 infra-opentofu

```
Workstream: docs/workstreams/11-infra-opentofu.md, after the human steps in
docs/ops/AZURE-SETUP.md are done. Build infra/azure: network with NAT
gateway and no inbound, the host module (Intel Dsv5 size from the host_size
variable, default Standard_D16s_v7 per DECISIONS I-14 and I-39, security type
Standard, Premium SSD v2 data disk, cloud-init writing the join token,
nixos-anywhere provisioner), edge VM, Coolify Ubuntu VM with cloud-init,
Blob with lifecycle rules, Key Vault with the wrapping key, the remote
state backend. No backup bucket: backups are Coolify's, against the
owner's own destination (DECISIONS I-112). `tofu validate` and `tofu
plan` must be clean; apply only when the owner says so.
```

### 12 nix-config-pipeline

```
Workstream: docs/workstreams/12-nix-config-pipeline.md. Build the fragment
contract and its enforcement: restricted pure evaluation with the exact
nix flags, the allowed inputs, eval and build timeouts, core and closure
caps, error extraction with fragment line numbers, the catalog format and
the menu-to-fragment renderer used by the api, base bump flow with the
hold flag, kernel_changed detection, and a test corpus containing the five
error cases from docs/CHECKLIST.md producing their exact messages.
```

### 13 notifications

```
Workstream: docs/workstreams/13-notifications.md. Build the event pipeline
end to end: per-agent hook installation in the wrappers (Claude Code
Notification and Stop hooks; the documented equivalents or tmux-idle
heuristic for opencode, codex, gemini-cli, pi), the outbox with retries and
dedupe in the api, Resend email and ntfy delivery, user settings, the
notify-test route, and what repose status, repose events and the
dashboard show.
```

### 14 security

```
Workstream: docs/workstreams/14-security.md. Review, do not build features:
verify every isolation claim in docs/SECURITY.md against the code and Nix
config as it exists, write the isolation test scripts from
docs/CHECKLIST.md, audit log coverage, secrets handling, operator access;
write the privacy policy and terms text with the process-sample boundary
and the Anthropic hosted-use statement verbatim. Report findings as a list
with severity and the exact file and line; fix only what is a clear bug.
```

### 15 dev-ergonomics

```
Workstream: docs/workstreams/15-dev-ergonomics.md, decided as DECISIONS
I-195..I-205, with the reasoning in docs/proposals/2026-09-23-dev-ergonomics.md
(read it first). Build §2 in its order, one mergeable commit series per
part: timezone on every run/attach, git config carry minus the denylist,
Claude Code config carry with the jq settings.json merge, .env carry over
SSH, auto-forward on the ControlMaster printed inside tmux, repose cp,
guestd-owned oom_score_adj, hybrid first sync, npm and Docker Hub caches
on each host, the one-day trial. §3 lists what you must not start (Claude
login, image paste, config.toml, any GitHub App). Nothing may add visible
latency to run or attach: measure, and paste the table. Test against the
local sshd harness, the fake api and guest NixOS tests; the timing,
forward and cache rows need a real guest, and the conductor or owner
provides one.
```

