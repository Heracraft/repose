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

**The owner, 2026-11-19, verbatim: "You are free to question the initial
assumptions made by the development process. For example, do we have to use
Cloud Hypervisor? Perhaps a different one for the Mac minis, something more
efficient for their environment. As long as guests have NICs and boot, we're
good. Weigh the guest cost against maintenance."** So everything below is a
default and not a wall. A session that finds a better shape says so with
numbers rather than building to the list.

The live example, which the council's engineer hat and 19f both owe an
opinion on: the deepest alternative on a Mac is **no appliance at all** -
each project's machine run directly on Virtualization.framework by a macOS
hostd, which removes the nesting, the appliance's kernel pin and one layer
of memory, and would attack the four-minute start at its root. The cost is
a second hostd platform to maintain: LVM thin pools become APFS clones,
taps become vmnet, systemd becomes launchd. A middle option, keeping the
appliance and the x86 hostd but a lighter VMM inside it, buys little. The
honest comparison in a few lines, with numbers from the Mac we have, goes
to the owner with the council's report. **Nothing is rebuilt on this before
the release.**

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

**The owner's scope, 2026-11-19, verbatim: "The last review must be super
ruthless, like going through every combination, possible combination.
Every feature, try it out, find errors, be a real customer. CLI and
dashboard."** So the council is not the Mac mini path alone. It is every
feature the product has, every command of the CLI and every action of the
dashboard, in every combination a customer could reach: plans and plan
changes, the four sizes, projects created and destroyed and restored,
secrets, triggers and webhooks, the phone ask, pause and resume, snapshots,
`host add` and `host rm`, cancellation, the lot. Every error reproduced and
ranked.

That is more than one session's day, so it is **two or three council
sessions** with a split, each a real customer with a stopwatch, conferring
at the end into one report: one on the CLI, one on the dashboard, one on the
Mac path. Each writes what it did, what it waited for, what it had to read
twice and what it would have tweeted; the confer produces the single
ranked list. A finding one session cannot reproduce is the other's to try
before it is dropped.

The Mac mini half, which the rest of this section describes, is one of
those three and the one the owner cannot test at all:

The owner does not own a Mac mini and cannot test this; the council is
the stand-in for the users who would otherwise scream. After phase 3
the conductor launches one session that *is* a Mac mini owner: it has
the landing page, the docs page, the dashboard, the CLI, and the real
Mac (the AWS one). It does the genuine thing, start to finish, with a
stopwatch: finds repose, signs up, pays for the Own hardware plan in
the Polar sandbox, reads how to add a Mac mini, runs the install on
the Mac, waits for the host to appear, starts a project, measures how
long the first guest takes to answer and how a second one feels,
opens the phone ask, closes the laptop, comes back, reboots the Mac,
removes the host, cancels. Every wait in seconds, every line it had
to read, every place it guessed, every error it saw, every moment it
would have tweeted. Then it judges with four hats, each writing its own short verdict before they
confer: the daily driver (does it work, is it pleasant, what broke),
the economist (is it worth $10, what does it cost us, what is the
relay cost per user), the brand skeptic (does any line oversell, does
anything look unfinished, does it read like repose), the engineer
(security of a stranger's host, idempotency, what happens on restart,
sleep, Wi-Fi drop, a full disk, how slow the guests are against the
hosted ones in `docs/research/boot-speed.md`). The report's first
section is "What would make a user scream", ranked, before the hats. Output: `docs/reports/<date>-mac-mini-council.md`
with findings ranked P0 (blocks launch), P1 (fix before queue), P2
(after), each with the exact reproduction. The conductor fixes P0 and
P1 by relaunching the responsible builder with the finding, then runs
the council once more. The second report's P0 list must be empty before
the queue.

## 7. Checklist (the conductor closes these, with evidence)

Closed by the mini-conductor on 2026-11-20, each against the evidence
named beside it rather than against a session's word that it was done.
Four are open and two of those are open on purpose; an item that is half
closed says which half, because "mostly" is how a workstream ships at 99
percent.

- [x] **Gate 0 recorded.** `docs/research/mac-mini-nested-kvm.md` §7
      "What the Mac said (AWS mac-m4.metal, eu-west-1a, 2026-11-19)",
      raw files under `docs/research/mac-mini-nested-kvm-data/`
      (`kvm-ok.txt`, `l1-facts.txt`, `l1-measure.log`, `l2-boot-1.log`,
      `l2-boot-2.log`, `ch-version.txt`, `host.txt`). **Steps 1, 2, 3, 4
      and 7 pass**: nested KVM works, a guest boots under the pinned
      6.18.57 appliance kernel and hangs under 7.0 with timer wakeups
      lost, no measurable CPU nesting tax (guest/appliance sha256 ratio
      1.07), and vfkit boots the same disk. Steps 5 and 6 are hand steps
      and were not taken, so the freeze-and-restore gate I-1376 names is
      **not** closed by gate 0.
- [x] **Ownership cannot leak.** `internal/api/scheduler/scheduler_test.go`:
      `TestOwnershipPlacesBothWays` (:580), `TestOwnHardwarePlanNeverReachesTheFleet`
      (:622), `TestOwnerAsksForTheFleet` (:651),
      `TestOwnHostOffIsNotAFleetPlacement` (:667),
      `TestFreeingRespectsOwnership` (:692). Both directions, which is
      what the item asks for. The red-with-the-filter-removed run is
      19a's claim in its STATUS line and was not re-taken here.
- [ ] **A host cannot reach another host or the control plane beyond its
      ports.** Half closed. The ruleset snapshot check exists and is
      wired (`nix/flake.nix:451`, `checks.x86_64-linux.edge-ruleset-snapshot`).
      **The packet test on staging's edge was never run** — the item
      asks for `nft list ruleset` on the live edge and a `nc` from a host
      to another host's wg address timing out, and no session recorded
      either. What closes it: those two commands on staging, output in
      the batch record. A snapshot test proves the rules we wrote; only
      the packet proves the rules that are loaded.
- [x] **The gateway refuses a connection to a project on someone else's
      host.** `internal/gateway/foreignhost_test.go`, with
      `noaddress_test.go` beside it. 19b's STATUS line records it red
      first.
- [ ] **`repose host add|ls|rm` exist, `cli.md` agrees, the dashboard
      does the same three, screenshots at 1440 and 390 in both
      schemes.** Half closed. The three commands are in `cli.md` (:482
      to :487) with the state column, the name rule and the exit codes;
      `internal/cli/docs_test.go` is green inside the 101-package run;
      the dashboard page is `apps/web/src/routes/settings/hosts/+page.svelte`.
      **No screenshots exist** — nothing under `docs/` holds a Hosts page
      capture at either width in either scheme, and the repo's own rule
      is that a screenshot is evidence only at the size a visitor sees.
      What closes it: four captures of that page at 1x, 1440 and 390,
      light and dark.
- [ ] **The plan exists in Polar sandbox and production, PRICING.md and
      billing.md and the landing agree on $10 and one host.** **Open by
      the owner's own hold, and it is the item to read before cutting
      anything.** The product exists in production: `GET /v1/billing`
      returns the `own` plan `available: true`, `price_cents: 1000`,
      `hosts: 1`, `trial_days: 7` (read 2026-11-20). `docs/PRICING.md`
      §"Own hardware" says $10 a month for one host. **`billing.md` and
      the landing say nothing**, because that is exactly what the held
      commit `7db572b5` adds, and it is held on the customer review
      passing. So the shipped tree is incoherent in a way a customer
      meets: `apps/web/src/content/docs/own-hardware.md` links twice on
      the words "Own hardware plan" to `/docs/billing`, which has no such
      section without that commit. Board: ENG-47, ENG-129.
- [x] **The aarch64 guest closure builds on the Mac's builder and is
      under the limit.** `docs/research/mac-mini-nested-kvm.md:307`:
      **6,670,370,872 bytes (6.21 GiB)** against the 7 GiB limit
      (7,516,192,768), 232 MB larger than the x86 closure's 6,438,215,064
      at I-1056. Build 169 s for the first pass (979 cached paths, 1.9
      GiB, 421 derivations), the runner 13 s and the closure check 4.8 s
      on the second. `checks.aarch64-linux.guest-closure-size` is the
      same 7 GiB rule as the x86 one and printed that number.
- [ ] **On the AWS Mac: the launcher boots the appliance, the host
      registers, Hello arrives, `repose run` lands a project on it,
      `repose ls` shows the host, the phone ask reaches a session
      there.** Half closed, and the half that is open is the product's
      own pitch. Closed: the appliance installs and boots, the host is
      online 40 s after a 27 s install with zero FAILED units, hostd
      starts, and five consecutive stop-then-start rounds on mac-rig3 all
      exit 0 with state `running` (starts 219/216/282/187/190 s, stops
      12/11/12/9/9 s; hostd's own `total_ms` 200.7/192.8/254.9/162.6/169.7
      agrees with the api's op records). **The phone ask is not closed**:
      it failed on the published build (ENG-105, `repose-ask` giving up in
      6.1 s on a `--timeout 3m`), its fix is merged, and **no live guest
      has ever run the fixed build** — the spark's field numbers measured
      the open on a loaded guest but its third run tested the published
      binary, so it is a control on the old behaviour, not a
      verification. What closes it: one `repose-ask` round trip on a
      guest from an image built at `mac-mini` head.
- [ ] **Restart: the Mac reboots, the launcher brings the appliance back,
      hostd reconciles, the project is reachable again without a
      command, and with what it was doing** (I-1386, ENG-2). Evidence
      the item asks for: the launcher log's `park-key:` lines and the
      appliance journal's `guest_park` and `guest_restored` lines with
      their phase timings, **from a real restart of the Mac**. **Half
      closed, and the half that is closed was a defect an hour ago.**
      The cause of the cold boots is found and fixed: `VsockDialer`
      wrapped an AF_VSOCK descriptor with `net.FileConn`, which cannot
      work on any platform, so hostd never reached the launcher, the
      image key was never fetched and every park was skipped — hostd
      logged it correctly throughout ("getsockname: address family not
      supported by protocol", then "park skipped: no image key").
      `5bed14eb`, merged. **Verified on the rig at 07:10 to 07:20 UTC**:
      `park-key: event=keychain_item_created service=run.repose.appliance
      account=park-wrapping-key`, then `event=wrapped` and
      `event=unwrapped` on `key_id=keychain-79a9a0260addb566`, and the
      guest came back from `launchctl bootout` then `bootstrap` with
      `/tmp/marker.txt` intact and uptime continuous across the stop
      (319.27 s before, 400.95 s after) — against the same test an hour
      earlier on `e167476d`, which had no `park-key` line at all, the
      marker gone and uptime reset to 193. **And it answers a question
      only that machine could answer: `security` serves a launchd agent
      in `gui/501` without a prompt**, so the park does not fail
      silently for a customer. **Still open:** that was a `bootout`, not
      a restart, so the launcher's `ExitTimeOut` of 360 s was never
      stressed — the stop took 20 s. What closes it is `sudo shutdown -r
      now` on the Mac, which the spark is running.
- [ ] **Council report exists, second pass has no P0.** The reports
      exist and now ship with the work: `docs/reports/2026-11-19-mac-mini-council.md`
      (the confer), `-mac.md`, `-cli.md`, `-dashboard.md`. **The second
      pass has not run** and **two P0 are open**: ENG-71 (the install
      refuses correctly, then the retry adopts the `disk.raw` the refusal
      left behind, skips the guard, and produces an appliance whose grow
      unit fails) and ENG-119 (the documented way to remove a Mac wedges
      the account and the escape its refusal names cannot exist). Both
      are with builders. The second pass runs against an appliance built
      at `mac-mini` head, not the published one, or it re-finds what is
      already fixed and proves nothing.
- [x] **`just done-check`, `golangci-lint`, the Go suite.** `just
      done-check` green on this tree 2026-11-20 07:03 (its greps, the
      two trust-the-reader tests, and the web docs and copy vitest at 31
      passed). `golangci-lint run ./...` 0 issues. `go test -p 2 -timeout
      25m ./internal/... ./cmd/...` 101 packages ok, 0 failures.
      `go build ./...` and `GOOS=darwin GOARCH=arm64 go build
      ./cmd/repose/...` exit 0. **Said exactly:** `-race` has been run
      per package by the builders who touched them, not over the whole
      tree here, and the nix checks run one at a time under a 6 GB cap
      because a whole-flake evaluation costs this box 8.5 to 12 GB —
      `guestd`, `host-services`, `host-mac` pass, the rest in flight, and
      the six `aarch64-linux` checks need a builder the Mac has and this
      box does not.
- [ ] **Every user-visible change has its docs page in the same commit;
      every decision above is in DECISIONS.md with its reserved id and
      the index regenerated.** The index is current (1044 entries) and
      every id this program used has an entry **except one: I-1362 has no
      entry at all**, and it is already cited by another entry
      ("The Mac mini appliance (I-1362, I-1376)…"), so the repository
      carries a citation to a decision that does not exist. It is the
      spark's to write, with gate 0's numbers, per §4. Nothing caught
      it: `just done-check` and `decisions-index.py --check` both pass
      with a dangling citation. I-1384 is reserved and unused, which is
      fine; I-1394 is on the unmerged `fix-park`.
- [ ] **`mac-mini` queued with `release-queue add` and a `--live` line
      that names the AWS Mac check.** Not queued. The release conductor
      will not cut it while the second pass is open, because the plan is
      purchasable today, and that disagreement is with the owner as
      ENG-129.
