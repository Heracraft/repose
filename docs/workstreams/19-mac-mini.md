# 19 · mac-mini (bring your own Mac mini: the repose appliance VM)

Program doc for the pipeline the owner asked for on 2026-11-19: a spark
session (this one) sets up, a mini-conductor runs builders, a user
council judges, and the end state is a Mac mini owner adding their
machine to their repose account and running agents on it. The research
behind it is `docs/research/mac-mini-nested-kvm.md`; the business shape
is `docs/proposals/2026-10-10-bring-your-own-host.md` and DECISIONS
I-647 (shape A: hosted control plane, hosts the user owns). Reserved
decision ids for this program: I-1362 to I-1384, even numbers, assigned
below; a builder uses only the ids its item names.

## 1. Goal

A user with an M3 or newer Mac mini on macOS 15 or later runs one
install step, their Mac shows up under their account as a host, and
from then on `repose run` and the dashboard put that user's projects on
it: each project its own Cloud Hypervisor microVM inside one sealed
aarch64 NixOS appliance VM that hostd runs. Everything else (api,
gateway, hub, dashboard, notifications, triggers, the phone ask) is the
hosted control plane, unchanged. The plan is $10 a month for one host.
Nothing of the user's is wiped; the appliance is a VM on the
Virtualization framework, launched at login, and the Mac stays a Mac.

```
Mac mini, macOS on the hardware               L0, not a VM
└─ repose appliance (aarch64 NixOS, hostd)    L1, Apple's hypervisor, vfkit
   └─ one microVM per project                 L2, KVM inside the appliance
```

## 2. Roles and sessions

| Role | Session | Where | Model |
|---|---|---|---|
| Spark | session `factory-3f`; runs the M4 measurement (gate 0), owns `ops/dev/macmini` and the AWS lab | `~/kanali-macmini-research`, branch `macmini-research` | Fable |
| Mini-conductor | integrates, launches and restarts builders, merges into `mac-mini`, queues; never builds an item itself | `~/kanali-mac-mini`, branch `mac-mini` (from `macmini-research`) | Opus |
| Builders | one per item 19a to 19g, each in its own worktree and branch | `~/kanali-mac-mini-<item>`, branch `ws/19<x>-<name>` from `mac-mini` | per item below |
| User council | a session that *is* a Mac mini owner, with four hats, on the real Mac | `~/kanali-mac-mini-council`, branch `council/mac-mini` | Fable |
| Owner | the only one who spends money or says "ship" | | |

Sessions are tmux windows briefed by `docs/workstreams/PROMPTS.md`
(`/ws 19`, `/ws 19a` …), per `docs/ops/ORCHESTRATION.md` "Sessions in
tmux windows". Sessions message each other with `SendMessage` after
`ListAgents`; the conductor subscribes to builders with
`notify_when_idle` rather than polling. Nobody runs `tmux kill-server`
on the default socket.

**Money.** A Mac day on AWS is about $30 (mac-m4.metal, 24 h minimum,
billed from the allocation call). The owner approved the first
allocation on 2026-11-19. Every further allocation is the conductor
asking the owner in one line with the reason and getting a yes; the
conductor's first message to the owner asks for an envelope (three Mac
days is the estimate for phases 2 and 3) so it does not have to ask per
day. `ops/dev/macmini destroy` runs the moment a day's work is done.

## 3. Gates and phases

**Gate 0, the measurement (spark).** `docs/research/mac-mini-nested-kvm.md`
§4 steps 1 to 7 on the AWS M4. Pass: `/dev/kvm` inside the appliance
VM, a Cloud Hypervisor guest boots in it, the nesting tax is recorded,
vfkit boots the same disk with `--nested`. The numbers go to
`docs/research/mac-mini-nested-kvm-data/` and a §7 "What the Mac said"
in the research doc, and the spark messages the conductor. **If step 2
fails** the appliance path stops here: items 19e and 19f are cancelled,
19a to 19d still ship (they are bring-your-own-host on any x86 box, the
proposal's `metal` path), and the owner decides the next shape.

**Phase 1, in parallel, from now:** 19a, 19b, 19c, 19d. None needs the
Mac or gate 0. They are the hosted side of bring your own hardware and
they are the security items that must exist before any stranger's
machine joins the hub.

**Phase 2, after gate 0 passes, needs the Mac alive:** 19e then 19f
(19f consumes 19e's image). The Mac's appliance VM is the aarch64
builder for both; the lab driver's `ssh` gets a builder there.

**Phase 3, integration on the real Mac (conductor with 19f's builder):**
dashboard adds the host, launcher installs, registration over the hub,
`repose run` lands a project on the Mac, phone ask reaches it,
`repose rm` and a host restart behave. Findings go back as commits on
`mac-mini`.

**Phase 4, the user council**, then fixes, then a second council pass,
then the queue. §6.

## 4. Settled shape (the assumptions the builders build on)

Each is a decision entry a builder writes under "Made during
implementation" with the id given, unless the owner overrides it first.

- **I-1362** Bring your own hardware ships as the Mac mini appliance
  first, x86 metal second; shape A (I-647) unchanged. Spark writes it
  with gate 0's numbers.
- **I-1364** Plan: "Own hardware", $10 a month, one host per account,
  no seats, no memory cap from the plan, the host's memory is the cap.
  Hosted plans unchanged. Polar product, PRICING.md, billing.md,
  landing. (19d)
- **I-1366** A host has an owning account. Owned hosts are placed only
  with the owner's projects and never with anyone else's; they are
  outside seats, the waitlist and the pool gauges. (19a)
- **I-1368** The hub treats a host as hostile: no host-to-host traffic,
  from a host only the ports the design names, gateway routes to a host
  only its owner's projects, every project-naming hostd RPC checked
  against the calling host. SECURITY.md gains the section. (19b)
- **I-1370** `repose host` is the noun: `host add` (prints or opens the
  install), `host ls`, `host rm`; the dashboard has the same three
  (dashboard parity rule). The join token is minted per `host add` and
  single-use as today. (19c)
- **I-1372** Snapshots of an owned host's guests stay on the host's own
  disk (`FileBlob`); no bucket keys on a host. The docs say a dead disk
  loses them. (19c; revisit when a user asks)
- **I-1374** The flake has `aarch64-linux` outputs for the guest base
  and the host system; the host kernel pin is per architecture; the
  runner's console is `ttyS0` on x86 and `ttyAMA0` on aarch64; the
  closure limit applies to both. (19e)
- **I-1376** The Mac appliance: one `raw-efi` NixOS image, one disk with
  a partition for `vg-guests`, host reserve scaled to the Mac's memory,
  provider `mac`, launched by a vfkit-based launcher at login with the
  Mac kept awake; freeze and restore gated on gate 0 step 5. (19f)
- **I-1378** Owned hosts update themselves from a published host system
  (Cachix) on a timer; the api accepts the previous hostd release for
  one release. (19g, may ship after launch)
- **I-1380** The Mac launcher: what it is (a signed, notarised macOS
  program or a plist over vfkit), how it holds the token, what it
  logs (nothing of the user's). (19f)
- **I-1382, I-1384** reserved for what the council or integration finds.

## 5. Items

### 19a host ownership (api, scheduler, dashboard) — Opus

`internal/db/migrations`: `hosts.owner_account_id` nullable, index;
`host_join_tokens` carry the owner. `internal/api/scheduler`: `PickHost`
takes the project's account; an owned host is eligible only for its
owner and a hosted host never for a project whose account owns a host
unless the owner asks for hosted (a project setting, default: own
host). Seats, waitlist and pool gauges exclude owned hosts. api routes:
`POST /hosts` (mint token, returns the install command), `GET /hosts`,
`DELETE /hosts/{id}` (drain, retire). Dashboard: Hosts page with add,
list (name, state, memory, running projects, last heartbeat), remove.
Interfaces: `api.md`, `db-schema.md`, `grpc-hostd.md` (Hello carries
nothing new; the token already binds the host id). Tests: a placement
test that fails red when ownership is ignored, in both directions.

### 19b the hub and the gateway treat a host as hostile — Fable

`nix/edge`: nftables forward chain drops wg0 host-to-host; from a host
only the api's gRPC, Loki push, the hub itself; the monitoring peer
rules stay. Gateway: a connection to project P is routed only if P's
host is P's owner's host or a hosted host. hostd RPC audit: every call
that names a project checks the project lives on the calling host
(list the RPCs in the decision). `docs/SECURITY.md` section "A host you
do not own". Tests: nftables ruleset snapshot test (the flake already
exports the edge ruleset), gateway routing test, hostd audit test shown
red first.

### 19c `repose host` and the install path — Opus

CLI noun `host` (`add`, `ls`, `rm`), `cli.md` and `docs_test.go`
green, the public docs page "Add your own hardware" (Mac mini first,
x86 box second, requirements from the research doc), registration
without cloud-init: the install carries the token to `hostd register`
(`nix/hosts/registration.nix` already takes it from a file). The host's
snapshot store is `FileBlob` on its own disk (I-1372). `repose ls` and
the dashboard say when the host is offline. Trust-the-reader rules on
every line.

### 19d the plan — Opus

Polar product "Own hardware" $10 a month, one host; `PRICING.md`,
`billing.md`, the landing's plans, terms unchanged (12-month price
hold applies). The founding-price mechanics are Solo's only and stay
so. Checkout, webhook, `GET /public/seats` untouched for hosted plans.
Tests: billing fakes for the new product; the webhook accepts it.

### 19e aarch64 outputs — Fable (needs the Mac)

`nix/flake.nix` parameterised over system for guest base, guest
runner, host system and installer; `nixpkgs-host-kernel` pin per arch;
console per arch; the agents overlay's arm64 binaries; closure limit
check on both; Cachix push of the aarch64 base. Built on the Mac's
appliance VM as a Nix remote builder (`ops/dev/macmini ssh`); the
closure size and build time recorded in the research doc.

### 19f the appliance image and the launcher — Fable (needs the Mac, 19e)

`nix/hosts/mac.nix` provider: one-disk disko layout, `vg-guests`
partition, host reserve for 16 and 24 GB, `kvm` (the generic module,
not `kvm-intel`), no Azure, no cloud-init, token from a file the
launcher writes. `raw-efi` image output. The launcher: vfkit with
`--nested`, EFI, virtio-blk on the image, NAT, started by a launchd
agent at login, `pmset` keeps the Mac awake, logs nothing of the user.
Proven on the AWS Mac: appliance boots, registers over the hub, hostd
reports Hello, a project runs. Public docs page updated with the exact
install step.

### 19g unattended updates — Opus (after launch unless time allows)

A timer on an owned host that fetches the published host system from
Cachix and switches; the api accepts the previous hostd release for
one release; RUNBOOK section.

## 6. The user council

After phase 3 the conductor launches one session that *is* a Mac mini
owner: it has the docs page, the dashboard, the CLI, and the real Mac
(the AWS one). It goes through the whole thing as a user, then judges
it with four hats, each writing its own short verdict before they
confer: the daily driver (does it work, is it pleasant, what broke),
the economist (is it worth $10, what does it cost us, what is the
relay cost per user), the brand skeptic (does any line oversell, does
anything look unfinished, does it read like repose), the engineer
(security of a stranger's host, idempotency, what happens on restart,
sleep, Wi-Fi drop, a full disk). Output: `docs/reports/<date>-mac-mini-council.md`
with findings ranked P0 (blocks launch), P1 (fix before queue), P2
(after), each with the exact reproduction. The conductor fixes P0 and
P1 by relaunching the responsible builder with the finding, then runs
the council once more. The second report's P0 list must be empty before
the queue.

## 7. Checklist (the conductor closes these, with evidence)

- [ ] Gate 0 recorded: research doc §7 with the pass or fail line of
      each step and the raw files under `mac-mini-nested-kvm-data/`.
- [ ] Ownership cannot leak: a test places an owner's project on their
      host and refuses a stranger's project there, shown red with the
      ownership filter removed.
- [ ] A host cannot reach another host or the control plane beyond its
      ports: the edge ruleset snapshot, and a packet test on staging's
      edge (`nft list ruleset`, a `nc` from a host to another host's
      wg address times out).
- [ ] The gateway refuses a connection to a project on someone else's
      host: test shown red first.
- [ ] `repose host add|ls|rm` exist, `cli.md` agrees (`docs_test.go`
      green), the dashboard does the same three, screenshots at 1440
      and 390 in both schemes.
- [ ] The plan exists in Polar sandbox and production, PRICING.md and
      billing.md and the landing agree on $10 and one host.
- [ ] `nix build ./nix#guest-system --system aarch64-linux` (or the
      output the builder names) succeeds on the Mac's builder; closure
      under the limit; the size in the research doc.
- [ ] On the AWS Mac: the launcher boots the appliance, the host
      registers, Hello arrives, `repose run` on a project lands on it,
      `repose ls` shows the host, the phone ask reaches a session there.
      Evidence: the api log lines (ids only), `repose ls` output, the
      session transcript's first line.
- [ ] Restart: the Mac reboots, the launcher brings the appliance back,
      hostd reconciles, the project is reachable again without a
      command.
- [ ] Council report exists, second pass has no P0.
- [ ] `just done-check`, `just lint`, `go test -race`, `golangci-lint`
      on `mac-mini`; `docs/CHECKLIST.md` greps clean.
- [ ] Every user-visible change has its docs page in the same commit
      (I-242); every decision above is in DECISIONS.md with its reserved
      id and the index regenerated.
- [ ] `mac-mini` queued with `release-queue add` and a `--live` line
      that names the AWS Mac check; STATUS.md updated by every session.
