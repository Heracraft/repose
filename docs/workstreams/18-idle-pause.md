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
  carries a wall-clock expiry inside the guest, and the wake steps that
  clock forward by the whole rest, so a machine that rested an hour would
  expire every open question in the second the user came back;
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

- [ ] The contract ships with the code: `buf lint`, `buf generate` and
      `buf breaking --against main` clean, and `git show --stat` carries
      `grpc-hostd.md`, `api.md`, `db-schema.md`, `host-conventions.md`
      and `interfaces/README.md`. Evidence: the three command outputs and
      the stat.
- [ ] A pause of a guest the freeze then takes does not lose its memory.
      Evidence: a Go test that freezes an already-paused guest and finds
      an image written and no `unfrozen{reason="failed"}`, shown red
      against `ch.Fake` made to refuse a second pause.
- [ ] `repose start` and the dashboard's Start on a frozen machine do not
      reboot it. Evidence: a test that a resting sample leaves
      `guestdDead` false and `restart` false.
- [ ] No op phase ever meets a paused guest, and a `guest_unresponsive`
      from a resting project is not recovered as a reboot. Evidence: the
      engine test, plus `ops/recover.go`'s own case.
- [ ] A resting minute still bills and still counts as unused. Evidence:
      a rollup test over a resting sample, and an `idle` test that a
      resting stretch still raises the 24-hour notice once.
- [ ] A wake is idempotent, is answered on a machine already awake, and
      never queues behind a build. Evidence: the hostd tests.
- [ ] Wake latency measured end to end on a real host: the gateway's dial
      to the first keystroke, and `wake_ms` from the hostd metric, for a
      guest resting 1 minute, 30 minutes and 2 hours. Evidence: the
      figures, on a host, pasted in the queue entry's live note. **Owed:
      this box cannot run it** (no host, and `nix develop` is the only
      thing meant to run here).
- [ ] The guest's clock after a wake, and whether a user timer due during
      a rest fires. Evidence: the same live run.
- [ ] An outbound connection idle across a rest: what breaks after 25,
      35 and 60 minutes, and whether Claude Code and an MCP server
      recover. Evidence: the same live run; the docs say what was found.
- [ ] `GuestdLost` does not page for a resting machine. Evidence: a
      promtool case in `ops/check.sh`.
- [ ] The four metrics are in `10-observability.md` §5 and
      `TestHostFamily` passes. Evidence: the test run.
- [ ] Every user-visible line obeys I-484 and I-485, the mocked band
      included. Evidence: `pnpm --filter web exec vitest run
      src/lib/docs.test.ts src/lib/copy.test.ts`, the CLI's
      `TestSuccessOutputNamesNoCommand|TestCLIReassures`, and the new
      lines pasted in the commit with what was cut.
- [ ] The docs ship in the same commit: `go test ./internal/cli -run
      'TestDocs|TestAgentGuide'` passes. Evidence: the run.
- [ ] Judged at 1440 and 390 in both schemes at 1x, with a frozen row and
      a frozen project page. Evidence: the captures.
