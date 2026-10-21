# 18 · idle pause (a machine nobody is using rests, and comes back at once)

## 1. Goal

A running machine that nobody is attached to, and whose agents all sit at
their prompts, stops spending the host's CPU within minutes, and comes
back the moment anything reaches for it. Every process, every open file
and all of the guest's memory survive: the machine is paused, not stopped
and not snapshotted. The user sees a machine that is still `running`,
marked frozen, and a first keystroke that lands in well under a second.

What this is not:

- Not a stop. R1-5 stands: no state change, no shutdown, no boot, no
  snapshot, no lost process.
- Not the memory half. The guest's memory stays resident on the host.
  Nothing is written to host swap, there is no image file, and so there
  is no fourth home for a secret (`CLAUDE.md`, "Secrets have three homes").
- Not an oversell. Seats, the scheduler's memory arithmetic and the
  public seat promise in `billing.md` and `limits.md` are untouched: a
  frozen machine holds its plan memory exactly as a running one does.
  The analysis for the memory half and the seats it would sell is §9 of
  `../proposals/2026-10-11-pause-idle-machines.md`, which stays a
  proposal and needs the owner's ruling on its decisions 2, 6 and 8.

The resource this frees is CPU, which is what binds a host today:
`../proposals/2026-10-14-host-cpu-capacity.md` measured one tenant's
agent at full tilt making every other guest on host-01 type at 17 ms p50
instead of 2.7 ms. An idle guest is not free -- `.claude-wrapped` at its
prompt burns about 2 s of CPU a minute, and 12.2 CPU hours of the prod
sample's idle minutes went to it alone (proposal §5).

## 2. The shape, settled

**S1. The api decides, hostd executes, either may wake.** The proposal's
§6 wanted hostd to evaluate the rule from its own live samples. It cannot:
two of the rule's seven terms exist only in Postgres -- an open gateway
session (`gateway_sessions`) and an op in flight (`ops`) -- and a third,
the keep-awake key, is a column. A 15 minute quiet window makes the api's
one-minute sample lag immaterial, and one rule in one place cannot drift
from itself. hostd keeps the safeties only it can see: it refuses a pause
while it has a command in flight for that guest or the host is going
down, and it wakes a guest itself before anything that needs guestd.

**S2. The state stays `running`.** `resting` is an additive marker on the
wire, on the row and on the sample, never an eleventh guest state. A new
state string would stop billing counting the minute
(`internal/billing/rollup.go:72` counts `state = 'running'`), break the
ten-value enum in `docs/interfaces/README.md`, `guest/manager.go`'s
Prometheus labels and `cli/status.go`'s `--wait`, and be dropped on the
floor by `ops/engine.go`'s Hello reconcile.

**S3. The word a user reads is "frozen".** The owner chose it with the
look (I-683), and it is free: "frozen" appears in CLI code only in
comments about the host-restart freeze and in no user-visible string.
The wire and the column keep the name I-683 already shipped, `resting`.
"At rest" is not available: `apps/web/src/lib/projectList.ts` spends it
on a stopped machine.

**S4. A resting guest's sample keeps `guestd_ok` true** and repeats the
last guest-written signals it read, with `resting` set. The guest is
paused, not lost. This one rule is what keeps four other things honest:
`idle.usedSQL` (which reads `not guestd_ok` as *use*, and would otherwise
silence the I-262 unused notice for exactly the machines that rest),
`http/projects.go`'s `guestdDead` (which would otherwise make `repose
start` and the dashboard's Start **reboot a frozen machine and lose every
process**), the `repose_host_guestd_lost` gauge behind the `GuestdLost`
page, and `cli/status.go`'s "the environment's agent is not answering".

**S5. No new guestd RPC.** The prepare (`sync`, and the cache drop when
the guest has the balloon) and the clock step (`date -u -s @<unix>`) reuse
`Exec`, which is what the I-652 freeze already does
(`guest/freeze.go:316-345`, `:1618-1622`). A new request would need the
version negotiation `vsock-guestd.md:17` promises and the code does not
have, and a guest base rebuild on every machine. Nothing in the guest
changes, so this feature reaches machines that are already running.

**S6. A pause is invisible.** No notification, no user-read event, no
line on `run` or `attach`. The marker in the state cell is the whole
surface (I-484, I-485). A failed wake is the exception: that is a failure,
and it alerts the operator and shows the user why.

**S7. Nothing may pause a guest the freeze is about to take.** This is
the feature's own worst failure: `guest/freeze.go` admits any guest whose
bbolt state is `running` with an active unit and then calls `vm.pause`;
CH refuses a second pause, the freeze fails, the image is discarded, and
that guest **cold-boots and loses its memory at the next host restart** --
I-796's incident, re-created by this feature. The freeze must treat an
already-paused guest as paused and snapshot it as it is, and `ch.Fake`
must refuse a second pause so a test can see it.

## 3. The rule

A running project may rest when all of these hold:

- its host's Hello named the `pause` feature;
- `keep_awake` is false;
- no op is open for it, and no gateway session is open;
- no `repose-ask` question of its is still open (I-822): a question
  carries a wall-clock expiry inside the guest, the guest's clock keeps
  running while it rests (measured, §7) and the sweep that expires
  questions cannot, so a machine that rested an hour comes back with
  every open question already past its expiry;
- each of its last 15 one-minute samples has `guestd_ok`,
  `ssh_sessions = 0`, `tmux_clients = 0`, every agent in `idle` or
  `needs_input` (anything else, `unknown` included, counts as working),
  `cpu_ns` under 6 s (0.10 of a core), and `net_rx + net_tx` under 64 KiB;
- no user timer inside the guest is due within the next 15 minutes
  (hostd reads the next *realtime* elapse before it pauses, §4; a
  monotonic timer counts the guest's own uptime, which stops with its
  vCPUs, so it keeps its remaining time across a rest and misses
  nothing).

hostd refuses a pause it must not take, and says why rather than failing:
a command in flight for that guest, the host going down, the guest not
running or already resting, a freeze record on it, no guestd session, or
a timer due inside the hold.

A container does not block a rest on its own: CPU and network already
cover a container doing work, and a port a container serves is reached
over a forward, which wakes the machine (proposal §6).

## 4. Items

The decisions are not one per item: each entry is one ruling, and most
items touch several. I-818 is the pause itself (the rule, the split, the
contract, the metrics); I-820 is `keep_awake`; I-822 is what wakes a
machine; I-824 is what the pause must never cost it; I-826 is the word
and the marker's whole surface; I-828 is the memory half, deliberately
unbuilt. An item's row names the entries its code cites.

| Item | What | Decisions | Owner |
|---|---|---|---|
| P1 | The contract: `PauseGuest`/`WakeGuest`, `GuestStatus.resting_at`, `GuestSample.resting`, the `GuestResting` event, the `pause` feature, migration 0029 | I-818 | cut already |
| P2 | hostd: pause, wake, the bbolt marker, reconcile, the sampler, the freeze interaction, the timer wake, metrics | I-818, I-822, I-824 | builder |
| P3 | api: the rule, the resting service, the wake before every guest-bound path, the marker on the Project, `keep_awake` | I-818, I-820, I-822, I-824 | builder |
| P4 | gateway: the wake at `dialGuest`, the route flag | I-818, I-822 | builder |
| P5 | CLI: the marker in the `ls` state cell and the `status` header, `--json`, `cli.md` | I-820, I-824, I-826 | builder |
| P6 | web: the flag goes, the two breakpoints agree, the unasked band goes, the keep-awake switch | I-820, I-826 | builder |
| P7 | docs and operations: `lifecycle.md`, `machine.md`, `run-and-attach.md`, `config.md`, `troubleshooting.md`, the agent guide, the metric list and the alerts | I-818, I-822, I-824, I-826 | builder |
| P8 | The host switch `repose.host.pauseIdle`, off by default, which is what puts `pause` in `Hello.features` | I-818 | integrator |

Shared numbers, cut and generated already (P1): `Command.pause_guest = 24`,
`wake_guest = 25`; `Result.pause = 17`, `wake = 18`; `GuestStatus.resting_at = 8`;
`GuestSample.resting = 17`; `Event.guest_resting = 16`; the feature name
is `pause`; migration `0029_resting` adds `projects.resting_since`,
`projects.keep_awake` and `meter_samples.resting`.

### P2, hostd

- `state.Guest.Resting *Resting {Since, NextTimer}`, written only by the
  pause and the wake, reported as `GuestStatus.resting_at`.
- `PauseGuest`: refuse (`paused=false`, a lower_snake `why_not`) when a
  command is in flight for the guest, the host is stopping
  (`guest/shutdown.go`), the guest is not running, or a user timer is due
  within 15 minutes. Otherwise `Exec` the guest's `sync` (and the cache
  drop when it has the balloon), read the next timer elapse, `CH.Pause`,
  write the marker, arm the timer wake, raise `GuestResting`.
- `WakeGuest`: idempotent. `CH.Resume` confirmed against `vm.info`, the
  retry shape of `freeze.go:1012-1038`; then the clock step; then
  guestd's ping; clear the marker; raise `GuestResting`; answer with
  `wake_ms`. It must be a `noSlot` job on the guest's own queue
  (`manager.go:405`, `:1125`) or a wake queues behind that guest's build.
- Both commands need a case in the `Kind` **and** `Target` switches
  (`manager.go:974-1039`), or the command is refused, or serialised on
  the host queue instead of the guest's.
- hostd wakes the guest itself, before the call, in every
  `Manager.session` caller except the sampler: apply, secrets, principals,
  `GrowFs`, `Exec`, `AnswerQuestion`, the snapshot's fsfreeze.
- The sampler (`samples.go:200`) skips the guest-side `Sample` for a
  resting guest, keeps the last signals with `guestd_ok` true, sets
  `resting`, and leaves `state` at `running` (S4). Without this the
  sampler also blocks 10 s per resting guest, serially, against a 60 s
  tick.
- Reconcile reads `vm.info`: a guest whose unit is active and whose
  hypervisor says `Paused` with a marker in bbolt stays resting and is
  reported so; one that says `Paused` with no marker is the leftover of a
  freeze that did not finish, which `ThawAll` already owns.
- The monitor: `checkUnits` stays right (a paused guest's unit is
  active). `checkLost` runs only after the vsock session drops, and a
  paused guest's session stays open, so nothing there needs suppressing
  -- but a wake must not be mistaken for a reconnect.
- The freeze (S7), and `SyncClocks`, must both read the marker.
- Metrics: `repose_host_guests_resting` (gauge),
  `repose_host_guest_pause_total{reason,outcome}`,
  `repose_host_guest_wake_seconds` (histogram),
  `repose_host_guest_wake_failed_total{reason}`.
- `hostd pause|wake` on the control socket, the shape of `hostd
  freeze|thaw` (`hostd/control`, `app/freeze.go:119`).

### P3, api

- `internal/api/resting`: the rule as one SQL read over the last 15
  samples, a pause sent per tick to what passes it, and `Wake(ctx,
  project)` -- send and wait for the result, the shape of
  `internal/api/questions` (`hostmgr.Send` plus an `OnResult` hook), with
  its own short deadline. A wake is **not** an op: the engine allows one
  open op per project and the wake is needed precisely when that op is
  open (`ops/engine.go:159`), so a wake modelled as an op deadlocks
  against itself.
- The ops engine wakes before the first guest-bound phase of every plan,
  so no phase ever meets a paused guest. `ops/recover.go:96` turns a
  `guest_unresponsive` from `apply_config` into a **full reboot**, which
  on a resting machine would lose every process; the wake-first rule is
  what keeps that path unreachable, and a `guest_unresponsive` from a
  project the api believes resting is retried as a wake, never recovered.
- Wake first on: the questions deliver, `secrets set`, a config apply,
  `GrowFs`, resize, stop, fork, a snapshot the user asked for.
- `projects.resting_since` from the `GuestResting` event and reconciled
  from Hello; `resting?: {since}` on the Project with no 24-hour gate
  (a pause happens after about 15 minutes, and the marker must not hide
  for a machine's first day); `keep_awake` on `PATCH /projects/:id`,
  effective at once.
- `meter/ingest.go` writes `meter_samples.resting` and keeps `state`.
- The `GuestResting` event updates the row and raises **no** user event
  and no notification (S6). It must not be routed through
  `events.go:382`'s state-change path, which would cancel the project's
  open questions.

### P4, gateway

`session.dialGuest` (`relay.go:189`) is the one point every user-facing
SSH action converges on -- plain `ssh`, `scp`, VS Code Remote-SSH, every
`repose` command and the I-223 fast attach, which makes no api call of
its own. The wake is asked for there and awaited inside the dial's 10 s
budget; `Route` carries `resting` for the log line and nothing refuses on
it, because the 5 s route cache makes the flag stale in both directions.

### P5, CLI

The marker goes in the `ls` STATE cell and the `status` header -- "state
that belongs to a row is a column" (I-484) -- and `up 31h` stays, because
a pause does not move `started_at`. The CLI's own `Project` struct must
gain the field or `--json` silently drops it. The unused line (I-262) is
unchanged and still prints: it says something else, that a machine the
user has not touched for a day is holding plan memory. `repose stop
--unused` still stops a frozen machine; that is the user asking.

### P6, web

The look is built (I-683). What is left: the field becomes real, so
`FROZEN_LOOK`, `frozenSince`'s flag term and the Playwright test that
asserts production shows no frozen machine all go; the table and the
stacked row must agree (today the mock replaces `unused` with `frozen` in
one and prints both in the other); the ice band's "It wakes when you
connect." is cut as unasked narration (I-484, I-485 -- it passes
`copy.test.ts` only because no regex happens to catch it); and
`keep_awake` gets its switch in the Config page's rail beside base
updates and `machine.nix`.

### P7, docs and operations

`lifecycle.md` gains the behaviour beside "Unused machines" (the state
stays running, every process survives, what wakes it, that an outbound
connection idle past 30 minutes may have dropped). `machine.md`: a timer
on a frozen machine, and a flat CPU chart. `run-and-attach.md`: a first
connection takes a second or two. `config.md` and `cli.md`: the
keep-awake switch and the marker. `troubleshooting.md`: a wake that
failed. The agent guide carries two promises this feature touches -- "It
runs with nobody attached" (timers) and "A new value reaches your next
command within seconds" (secrets) -- and both must stay true or change.
`10-observability.md` §5 gains the four metrics (`TestHostFamily` fails
otherwise) and `ops/alerts.yaml` the wake alert.

## 5. Checklist

- [x] The contract ships with the code. Evidence: `buf lint`, `buf
      generate` and `buf breaking --against '.git#branch=main'` all
      silent; the commits carry `grpc-hostd.md`, `api.md`,
      `db-schema.md`, `host-conventions.md`, `vsock-guestd.md`,
      `ssh-gateway.md` and `interfaces/README.md`.
- [x] A pause of a guest the freeze then takes does not lose its memory.
      Evidence: `TestFreezeOfARestingGuestKeepsItsMemory`, shown red with
      the `pausedAlready` guard removed ("freeze of a resting guest:
      pause failed", the image discarded); `ch.Fake` now refuses a second
      pause as Cloud Hypervisor does, so the test can see it. Also
      `TestFreezeDoesNotWaitOnARestingGuest`: the pre-freeze write-back
      no longer spends 10 s of the deadline on a guest that cannot
      answer, which is what made a later guest lose its image.
- [x] `repose start` and the dashboard's Start on a frozen machine do not
      reboot it. Evidence: `TestStartOnAFrozenMachineDoesNotReboot`,
      shown red with the guard removed (`202 {"restart":true}`), and the
      CLI's `TestFrozenMachineIsNotGuestdDead`. The guard is in
      `guestdDead` itself, so every caller is covered.
- [x] No op phase meets a paused guest, and a `guest_unresponsive` from a
      resting project is not recovered as a reboot. Evidence:
      `TestGuestUnresponsiveFromARestingProjectWakesInstead`, shown red
      ("commands ApplyConfig,WakeGuest,StopGuest,ApplyConfig,StartGuest"
      -- the machine was rebooted -- against the wanted
      "ApplyConfig,WakeGuest,ApplyConfig").
- [x] A resting minute still bills and still counts as unused. Evidence:
      `meter/ingest.go` keeps `state` as hostd sent it (`running`, what
      `rollup.go:72` bills) and writes the marker to its own column;
      `TestFrozenStretchStillRaisesTheUnusedNotice`. The one rule both
      rest on is the sampler keeping `guestd_ok` true (S4).
- [x] A wake is idempotent, is answered on a machine already awake, and
      never queues behind a build. Evidence:
      `TestWakeOfAMachineWithNothingToWake`,
      `TestConcurrentWakesSendOneCommand` (one command for three
      callers), and the `noSlot` job test; `runJob` now honours `noSlot`
      for a command, not only an internal job.
- [ ] **Wake latency measured end to end on a real host**: the gateway's
      dial to the first keystroke, and `wake_ms` from
      `repose_host_guest_wake_seconds`, for a guest resting 1 minute, 30
      minutes and 2 hours. **Owed, and this box cannot run it**: no host,
      and `nix develop` is the only thing meant to run here. `lifecycle.md`
      and `run-and-attach.md` both say "a second or two" and must be
      corrected if the figures disagree; `GuestWakeSlow`'s 5 s threshold
      rests on the same guess.
- [x] **That a guest with two vhost-user devices answers at all after a
      plain `vm.pause` and `vm.resume`.** Measured on host-stg-01,
      2026-10-21: it answers after every hold up to 35 minutes, 1,216
      store paths listed and a binary from the store run, with the boot id
      unchanged. §7 has the figures. I-652's finding is a *snapshotted*
      VM, a different path.
- [x] The guest's clock after a wake, and whether a user timer due during
      a rest fires. Measured (§7): the clock does not stop, so there is
      nothing to correct and the step is a safety net; a timer due inside
      a 300 s pause fired at the resume, 221 s late, which is what makes
      hostd's armed wake necessary rather than an optimisation.
- [~] An outbound connection idle across a rest. Measured at 35 minutes
      (§7): gone, and the program read it closed rather than hanging.
      Still owed: a 25-minute hold, to separate the NAT gateway's 30
      minutes from the far end's own timeout, and what Claude Code and an
      MCP server do with the close.
- [x] `GuestdLost` does not page for a resting machine. Evidence: the
      promtool case "GuestdLost stays quiet for a host whose guests are
      frozen", shown red by flipping `repose_host_guestd_lost` to 1.
      `ops/check.sh`: 43 rules parse, every alert has a runbook heading,
      the route is as intended.
- [x] The metrics are in `10-observability.md` §5 and the gates cover
      them. Evidence: `TestHostFamily` and `TestAPIFamily` name all five
      new families; `TestHostFamily` was green while blind to them and
      fails on a wrong name (`repose_host_guests_resting_XX`).
- [x] Every user-visible line obeys I-484 and I-485, the mocked band
      included. Evidence: `vitest src/lib/docs.test.ts src/lib/copy.test.ts`
      20/20 with a dead anchor shown to fail it; the ice band's second
      sentence cut and `unasked.ts` given a screens-only rule so it
      cannot come back; `TestSuccessOutputNamesNoCommand` and
      `TestCLIReassures` pass; the CLI's new lines are in the cli commit.
- [x] The docs ship in the same commit. Evidence: `go test ./internal/cli
      -run 'TestDocs|TestAgentGuide'` passes, including
      `TestDocsNameEveryCommandAndFlag`, `TestDocsNameEveryConfigKey` and
      the three agent-guide cases.
- [x] Judged at 1440 and 390 in both schemes at 1x with a frozen row and
      a frozen project page. Evidence: the web builder's captures;
      Playwright 224 passed (the one failure, billing's support-link
      case, passes alone and is a flake under the full parallel run).
- [x] The whole tree builds and tests. Evidence: `go build ./...`, `go
      vet ./...`, `gofmt -l` all clean; `go test ./...` 91 packages ok.
      `internal/guestd`'s two failures (`TestHerdrProjectWarnsHerdrDown`,
      `TestStraceReadsEnvironOnlyForTheHookPaneLookup`) pass alone, are
      in a package this branch changes by zero lines, and the strace one
      fails on unmodified `main` too.
- [x] The host switch is real and off. Evidence: `nix eval` of
      `host`.hostd.ExecStart and `host-01`'s carry no `--pause-idle`;
      with `pauseIdle = true` on host-01 it does.
- [ ] A live check on a host, recorded in the queue entry: that a machine
      rests at all under the real rule, and that `hostd pause|wake`
      works against a real control socket (only the routes, the client
      and the wiring are covered by Go tests).
- [x] A second pass over the committed branch, adversarially verified.
      Evidence: five lenses (what still loses a tenant's processes; a path
      that does not wake; a rule that freezes work or never fires; what is
      declared but not wired; every line a reader sees), then one refuter
      per finding that defaulted to not-real. Ten confirmed, nine refuted.
      The ten are fixed in the commits that name them, each with the test
      shown red first. The pass also recorded 66 things it checked and
      found sound, including that the nix switch reaches the unit, both
      host-stream hooks are registered in the stated order, the tick holds
      its own advisory lock, 0029 is embedded by glob, both commands are
      in all three switches, and all five metrics appear on a real scrape.

## 6. Where the live checks run

Staging, `host-stg-01` (`../../infra/README.md`, "Staging"): a real Intel
host with nested KVM, `hostdev` on the staging edge standing in for the
api, and no tenant guests. Nothing there needs a quarter of an hour of
real idleness -- `hostd pause <guest_id>` on the control socket pauses one
guest directly, and a guest can sit paused for hours before `hostd wake`
takes it back, which is how the clock, the timers and the dropped sockets
get their figures.

As of 2026-10-20 02:20 UTC that host is **down and needs a reboot**: its
MANA NIC has been failing to probe in a loop since about 8.8 hours into
its uptime (`mana ...: gdma probe failed: err = -71`, repeating to the end
of the serial log), so it answers no ICMP and no SSH, and `hostdev status`
on the edge reports `connected: false` with a heartbeat 9 hours old while
Azure still calls the VM running. The restart is the owner's. It is worth
an incident note of its own: nothing alerted, and a host that loses its
NIC and never comes back is what `HostUnreachable` and `HostScrapeDown`
exist for.

## 7. What the host said (host-stg-01, 2026-10-21)

Measured on the staging host after a reboot brought it back: a
`Standard_D8s_v7`, hostd `6ff9739` (not this branch -- the pause is
`vm.pause` on the guest's own API socket, so the mechanism needs nothing
of this branch deployed), Cloud Hypervisor 53, one `small` guest built
from base `793e3c1f` with both vhost-user devices and the login share.
Pauses driven with `curl -X PUT .../vm.pause` and `vm.resume` on
`/var/lib/repose/guests/<id>/ch.sock`; the guest read through `hostdev
exec`.

| | |
|---|---|
| Boot, for scale | 9,245 ms total (volume 757, prepare 319, ready 4,111, paths 1,350, setup 2,706) |
| Holds | 60 s, 120 s, 300 s, 2,101 s (35 min) |
| **`vm.resume`** | **9 to 10 ms**, the same at every hold |
| **virtio-fs after the resume** | **answers** at every hold: 1,216 store paths listed and a binary from the store ran (`uname -r`) |
| Boot id | unchanged across every pause: it never rebooted |
| A process that counted | came back with a gap of exactly the pause (61, 300, 2,101 s) |
| tmux | both sessions alive, with their original creation times |
| **The guest's clock** | **does not stop.** After 120 s and after 2,101 s the guest's `CLOCK_REALTIME` fell inside a host-clock bracket taken around the reading, and its uptime advanced by the full wall time |
| A timer due during a rest | **fires at the resume, late by the overshoot**: due at `…659`, paused `…580` to `…880`, fired `…880` -- 221 s late |
| An outbound connection | **gone.** An echo connection to the internet that answered a probe before the pause read closed after the 2,101 s one |

What this settles:

- **The gate.** I-652's "a VM must not be resumed" is a *snapshotted* VM,
  whose virtio-fs stops answering. A plain pause is a different path and
  it is sound: every program in a guest runs from `/nix/store` over
  virtiofs, and it still did after 35 minutes paused.
- **The clock step corrects nothing** on this path, which the first draft
  of I-818 and I-822 had wrong in its mechanism. The consequences those
  entries name are real but their cause is real time passing with nothing
  in the guest running, not a clock that jumps. The step stays as a safety
  net and the reasoning is corrected where it was written down.
- **hostd's armed wake is necessary.** Without it a scheduled job is late
  by the length of the rest, which is `machine.md`'s promise broken; 221 s
  late on a 300 s pause is the measurement.
- **A held outbound connection does not survive a long rest**, and the
  program saw it closed rather than hanging. One trial against one far
  end, so this does not separate the Azure NAT gateway's 30 minutes from
  the far end's own timeout, and it says nothing yet about whether Claude
  Code or an MCP server reconnects.

Still owed, and none of it is the mechanism: the wake as a user feels it
end to end (this branch's hostd is not on that host, so the figure here is
the resume alone, and the 9 ms says the cost is the control-plane round
trips rather than the hypervisor); the same matrix with the branch's own
`PauseGuest`/`WakeGuest` and `hostd pause|wake` against a real control
socket; a 25-minute hold to separate the NAT's timeout from a far end's;
and what the agents do with a dropped socket.
