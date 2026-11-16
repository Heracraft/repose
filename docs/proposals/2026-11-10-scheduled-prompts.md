# Scheduled agent prompts (proposal, 2026-11-10)

**Status: parked for later. This document records the thinking so that
it is not lost. Do not build it now.** The idea is future item 6 in
`reports/2026-11-04-product-value-council.md`: `repose schedule
'weekdays 07:00' -p 'triage new issues'` starts an agent with that
prompt on a schedule, and the agent's result reaches the user as a
notification. The council scored it pull 5.2, retention 7.0, novelty 4.8,
size S, with 3 build-next votes, and called it "a CLI wrapper" over
timers, wake and notify. This document shows that two questions decide
whether it stays size S, and it lays out the options for each. It does
not answer them. Nothing here is decided, and no DECISIONS id is
reserved.

## Summary

| Question | Options | What it turns on |
|---|---|---|
| (a) Where does a schedule live? | A. In the api (control plane). B. In the guest, as a systemd user timer. C. Both: the guest runs it, the api holds a copy to start a stopped machine | Whether a stopped or destroyed machine can still run it, and whether repose stores a prompt (the privacy policy says it never records prompts) |
| (b) May a schedule start a stopped machine? | No; yes, with a stop after; yes, and it stays up | Plan memory held, `plan_limit` refusals at night, R1-5 and I-262 (repose never stops a machine on its own) |
| Where the result goes | `repose-notify --done`, sent by the agent or by the wrapper | I-688: a finished turn notifies nobody |
| Busy or attached machine | Run in its own window; skip if the last run still works | What the user sees in tmux |
| Missed runs | `Persistent=true` catches up one run at the next start | A week stopped gives one catch-up run |
| Time zone | The guest system clock is UTC; the laptop's zone is only `TZ` for shells | A 07:00 timer may fire at 07:00 UTC today |
| CLI | `repose run --every`, a guest helper, or a new noun | The owner's rule: fold into an existing noun |

## 1. The repo today

**Timers in the guest.** I-518 made a scheduled job a systemd user
timer, with no cron in the base. `dev` lingers, so the user manager runs
with nobody attached, and unit files in `~/.config/systemd/user` survive
a stop. The guest guide tells agents how to write one
(`nix/guest/base/agent-guide.md:34`), and the public docs show the two
unit files (`apps/web/src/content/docs/machine.md:122-150`). A transient
`systemd-run --user --on-calendar` unit is gone after a stop (I-518).

**How a timer wakes a frozen machine.** The idle pause (I-818) refuses
to freeze a guest whose next user timer is due within 15 minutes
(`PauseHold`, `internal/hostd/guest/pause.go:43`; `why_not` is
`timer_due`). Before a pause, hostd reads `NextElapseUSecRealtime` of
every system and user timer through guestd
(`nextUserTimer`, `pause.go:335-349`) and stores it in the resting mark.
`armTimerWake` (`pause.go:378`) then sets a host-side timer that wakes the
guest one minute before the elapse (`TimerWakeLead`, `pause.go:48`). If
hostd cannot read the time, the pause goes ahead with no armed wake
(`pause.go:342-346`). The feature doc says the same:
`docs/features/stop-start-destroy.md:103-135`, and the user sees it in
`apps/web/src/content/docs/lifecycle.md:115-117`. A stopped machine runs
nothing: no host-side component starts a machine on a schedule today.

**Starting an agent with a prompt.** `repose run -p` opens a tmux window
on the machine over SSH and starts the agent with the prompt on its
command line (`startAgentWindow`, `internal/cli/mux_tmux.go:361`). That
path runs on the laptop. A guest-side scheduler would need its own way to
open the window, or run the agent headless (`claude -p`, `codex exec`).

**Result channel.** I-688: a finished turn notifies nobody; an agent
says it is done with `repose-notify --done`. I-931 added `--link URL
--label TEXT`. The event row keeps the summary (1 KB) and the link as
tenant content (`docs/interfaces/db-schema.md:113-119`).

**Time zone.** I-198: `run` and `attach` carry the laptop's zone each
time; guestd writes `TZ=` into `/etc/repose/env`
(`internal/guestd/project/project.go:247`) and sets tmux's `TZ`. The
guest's system zone stays UTC (`nix/guest/base/default.nix:46`).
`/etc/repose/env` is sourced by shells through
`/etc/profile.d/repose.sh`; the user manager gets only the static PAM
variables (`nix/guest/base/env.nix:1-13`, `:64-68`).
Nothing in `nix/guest` passes it to the systemd user manager, so an
`OnCalendar=Mon..Fri 07:00` with no zone suffix appears to fire at 07:00
UTC. This is unverified on a running guest; `systemctl --user
list-timers` on a machine whose laptop is in Europe/Berlin settles it.
The public docs (`machine.md:122-150`) say nothing about the zone.

**Plan memory.** A start that would pass the plan's memory is refused
with `payment_required`, `reason: plan_limit`, and the slugs of the
machines that use it (`docs/interfaces/api.md:370`).

**What repose promises about prompts.** The privacy policy says: "We
never record ... the prompts you give to any agent"
(`apps/web/src/content/legal/privacy.md:47-49`), and CLAUDE.md's "Never
log what a tenant typed" lists prompts.

## 2. Where schedules live (question a)

### Option A: in the api

The api holds `schedules (project_id, spec, tz, agent, prompt,
next_at)` and a tick that fires each due row. A fire on a running
machine sends a command through hostd to guestd, which opens the window.
A fire on a stopped machine starts it first (question b).

- **For.** A schedule survives a stop, a destroy and restore, and a
  move to another host. It can start a stopped machine. The dashboard
  can list and edit schedules for all machines in one place. The api
  already has ticks (idle, expiry, unused).
- **Against.** The prompt becomes a stored row. That breaks the privacy
  promise in its current words. Storing it as tenant content, as
  `events.summary` and `questions.text` already are, needs a
  DECISIONS entry and a privacy policy change. A new hostd command and a
  guestd handler are needed, which makes it size M. A schedule that
  points at a destroyed project needs a rule.

### Option B: in the guest

`repose schedule` (or a guest helper) writes `NAME.service` and
`NAME.timer` under `~/.config/systemd/user`, as I-518 already
documents. The service runs a small wrapper that opens a tmux window and
starts the agent with the prompt from a file in `~/.repose/schedules/`.

- **For.** Nothing new on the host or the api. The freeze already wakes
  the machine for it. The prompt stays on the tenant's disk, where
  snapshots already hold it, so the privacy promise holds. An agent on
  the machine can make a schedule itself, which fits the guest guide.
  This is the size S the council saw.
- **Against.** A stopped machine runs nothing, and `Persistent=true`
  runs the missed run only at the next start. A destroy removes the
  schedule; a restore brings back the snapshot's copy. The dashboard
  cannot list schedules without a new guestd read. The CLI must reach
  the machine to list or remove one, so it must be running or wake.

### Option C: both

The guest timer runs the job. The api holds only the next due time and
the project id (no prompt), and starts a stopped machine a few minutes
before that time; the guest timer then fires on a running machine. guestd
reports `NextElapseUSecRealtime` for schedule timers in its sample, as
hostd already reads it for the pause.

- **For.** The prompt stays in the guest. A stopped machine can still run
  its schedule.
- **Against.** Two places agree on one time, and the api's copy goes
  stale while the machine is stopped (a user who edits the timer by hand
  on a stopped machine cannot). It inherits every cost of question b.

## 3. May a schedule start a stopped machine? (question b)

Option B with no start is the cheapest answer: a schedule runs only on a
running machine, and the docs say so. A user who wants 07:00 runs leaves
the machine running; the freeze returns its cores while it waits.

If a schedule may start a machine, these costs come with it:

| Cost | Detail |
|---|---|
| Plan memory | The started machine holds its class memory against the plan while it runs. A user with a full plan at night gets nothing. |
| `plan_limit` at fire time | The api cannot ask the user at 07:00. Options: skip the run and notify (`Scheduled run of triage on todo-app skipped: your plan's 16 GB are in use by api and web.`), or queue it until memory frees, which can run hours late. |
| How long it stays up | R1-5 and I-262: repose never stops a machine on its own. A schedule that starts a machine and leaves it running holds memory until the user stops it. A schedule flag `--stop-after` (the user's own instruction, so not an automatic stop) would stop it when the agent's turn ends or after a time box, whichever comes first. Without a time box, a run that never ends holds the plan forever. |
| The unused notice | A machine started each morning that nobody attaches will get I-262's 24 h unused notice. The notice must count a scheduled run as use, or say why the machine is up. |
| Capacity | A fleet-wide 07:00 Monday start is a burst of boots and builds on every host. The api spreads fires by a few minutes, or the user accepts some lateness. |
| Billing | Plans sell memory that may run at once (I-289), so a scheduled start costs no extra money. It does use the user's own memory at a time they may not expect. |
| Temporary machines | A temporary machine (I-347) lives 24 hours at most. A schedule on one is refused, or lives and dies with it. Refusing is simpler and avoids a run on a machine about to be destroyed. |

## 4. Other behaviour to settle

**Where the result goes.** Two options:

1. The prompt says to finish with `repose-notify --done "WHAT, AND WHERE
   TO LOOK"`. The guest guide already tells every agent to do this when
   the work the user asked for is done. An agent that forgets sends
   nothing.
2. The wrapper waits for the agent's turn to end (the agent state goes
   `idle` or the headless process exits) and sends `repose-notify --done`
   itself, with the last lines of the agent's own summary, if the agent
   did not send one. This is the more reliable path, and it reads agent
   output in the guest only; the api receives only the 1 KB summary that
   already counts as tenant content.

A run that fails (agent missing, not logged in, Claude Code needing
`/login`) sends a plain `repose-notify` that names the failure.

**Busy or attached machine.** A scheduled run opens its own window,
named after the schedule (`sched-triage`), so a user who is attached
sees a new window and nothing they typed changes. If the last run of the
same schedule is still working, the new run is skipped and that is
written to the journal; two triage agents on one tree help nobody. A
`--worktree` option would give each run its own tree, as `run
--worktree` does today.

**Missed runs.** `Persistent=true` runs one missed run at the next
start, however many were missed. That fits a triage job. For a job that
should not run late (a 07:00 standup summary at 15:00), `--no-catch-up`
writes `Persistent=false`. A frozen machine misses nothing, because the
armed wake fires one minute early.

**Time zones.** The schedule should carry its zone in the `OnCalendar`
expression (`Mon..Fri 07:00 Europe/Berlin`, which systemd accepts),
taken from the laptop at creation, from the `tz` the CLI already sends
(I-104). A user who travels then keeps the zone they set. The other
choice, following the guest's carried zone, moves the run each time the
user attaches from another zone, and needs the user manager to get `TZ`
first. Either way, `machine.md` "Scheduled jobs" should say which zone a
hand-written timer uses, since that looks wrong today.

**Which agent.** The project's default agent, or `--agent`, as on `run
-p`. Headless (`claude -p`) is easier to wait on, but the user cannot
attach and answer a permission prompt; a tmux window keeps `repose-ask`
and attach working, which is the loop the council ranked first.

## 5. CLI placement

The owner's rule: fold a new command under an existing noun, avoid near
duplicates, and propose the name before building
(`cli-surface-taste`). Options:

| Shape | Example | For | Against |
|---|---|---|---|
| A flag on `run` | `repose run --every 'weekdays 07:00' -p 'triage new issues'` | `run -p` already starts an agent with a prompt; no new noun | `run` has many flags; listing and removing need a home too (`repose ps` could list, `repose stop -w sched-triage --schedule` remove) |
| A guest helper only | `repose-schedule 'weekdays 07:00' -- claude -p '...'` (the daily driver's card) | Agents can make schedules; no laptop command; no new top-level noun | Not visible from the laptop; the user must attach |
| A new noun | `repose schedule add|list|rm` | Easy to find, room for list and remove | Top-level command count is already high (36 in October); near `repose snapshots` in help |

The helper and the `run` flag can share one implementation: the flag
writes the units over SSH, as the helper does in the guest.

## 6. Decide before anyone builds it

1. **Where schedules live:** A (api), B (guest) or C (guest runs, api
   wakes). B is the size S the council costed.
2. **Prompts at rest.** If A, a DECISIONS entry and a privacy policy
   change to store a prompt as tenant content; if B or C, confirm that
   the prompt never leaves the guest.
3. **Starting a stopped machine:** never, or allowed. If allowed: what a
   `plan_limit` at fire time does (skip and notify, or queue), whether
   `--stop-after` exists and its default time box, and how the unused
   notice (I-262) treats a scheduled run. Each of these amends R1-5 or
   I-262 and needs its own line in the entry.
4. **Temporary machines:** refuse a schedule, or let it die with the
   machine.
5. **Result path:** the agent's own `--done`, or the wrapper's fallback.
6. **Overlap rule:** skip a run while the last one works, or queue it.
7. **Time zone:** fixed at creation, or following the carried zone. Fix
   the docs for hand-written timers either way, after checking which zone
   a guest timer uses.
8. **CLI shape:** `run --every`, a guest helper, or a new noun, with a
   preview of help and output for the owner first.
9. **A DECISIONS entry** that cites I-518, I-688, I-818, I-198, R1-5 and
   I-262, and the docs in the same commit: `machine.md`, `lifecycle.md`,
   `cli.md`, the guest guide.

## Sources

- `reports/2026-11-04-product-value-council.md`, future item 6 and the
  scheduled-jobs rows of each member's card.
- Repo: DECISIONS R1-5, I-104, I-198, I-262, I-289, I-347, I-518, I-688,
  I-818, I-931; the files and lines named in section 1.
- systemd.time(7), "Calendar Events": a time zone name may end the
  expression.
