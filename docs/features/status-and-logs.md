# Status and logs

`repose status` answers "what is it doing and how long has it run" in
one screen. `repose logs` shows the guest's console, the last build, or the
operations history. The dashboard shows the same data with history.

## What the user sees

```
$ repose ls
PROJECT     CLASS  STATE    UP     AGENTS
todo-app *  large  running  2h14m  claude: working
api-v2      xl     stopped  -      -
Solo: 8 of 8 GB running, 41.3 of 100 GB disk, 212 of 250 GB egress this month

$ repose status todo-app          # or --project todo-app, or from the checkout
todo-app  running 2h14m  large
  agents     claude-2 needs input, claude working
  checkout   main: 3 commits not on this laptop, 2 files not committed
             worktree-1: nothing new
  attached   1 SSH session, 1 tmux client
  listening  node :5173 up 3d 410.0 MB
             :5432
  disk       6.2 GB of 40.0 GB, snapshot 11h08m ago
  last event 14m ago, claude done "Added auth flow"
```

(`internal/cli/status.go`. A project the platform stopped for mining, or
in `error`, gets one more line saying why and what to run.) Since
DECISIONS I-616 the rows are labelled, the running hours are gone (a
plan bills none), and the host and the guest's address print only under
`-v` (`host       host-01, ip 10.100.0.12`); `--json` has them as
`host_id` and `guest_ip`. `docker N containers` prints only when there
are some. `*` in `ls` marks the project a command run in that folder
acts on (REPOSE_PROJECT, the folder's link, its remote). The line under
the table is GET /billing's plan and usage; warning lines follow it near
a limit that refuses, stops or charges (disk past the plan, egress past
the allowance, `past_due`).

The `checkout` row (I-616) is read in the same ssh as the listeners: the
laptop sends the commit at every branch tip of its checkout (its fetched
`repose/*` branches included) on that ssh's stdin, only when the folder
status runs in is the project's checkout (its `repose` remote names the
project, or its origin is the project's remote). The guest keeps the
ones it has (`git cat-file --batch-check`) and, for its checkout and
each `git worktree`, counts the commits not reachable from them
(`rev-list --count HEAD --not`), the lines of `git status --porcelain`
(given 2 seconds) and the last commit's time. Without the laptop's
commits the row shows the last commit's age. `--json` carries the rows
as `git: [{worktree, branch, commits_not_on_laptop, uncommitted_files,
last_commit_at}]` beside the Project's fields. Nothing is sampled or
stored by the platform: the read is the user's own ssh, and only counts,
branch names, folder names and a time come back.

`--watch` redraws in place on a terminal, prints a block only when it
changed when piped, and keeps going through an unreachable api, a 5xx or
a 429 (a line says the read failed). `--wait STATE [--timeout 10m]`
polls the project every 2 seconds, prints the status once it is in
STATE, and exits 1 on `error`, on destroyed or at the timeout.

On a herdr project (DECISIONS I-509) the header reads `todo-app  running
2h14m  large  herdr` and the attached row counts SSH sessions only:
herdr's clients arrive over SSH and are in them. The `herdr` word
is what runs now, read from the guest in the same ssh that lists the
listening processes (`systemctl --user -q is-active
repose-herdr-server`); a project switched while running shows its old
multiplexer until its next start. When the guest does not answer within
that ssh's few seconds, the word comes from the project's stored
`multiplexer`.

The same ssh reads the guest's root filesystem (`stat -f /`), and `disk`
is its used over its size, counted as guestd's `disk_high` counts it.
A guest that did not answer falls back to the api's `root_used_bytes`
and `root_size_bytes`, the same figure from the newest minute sample;
a stopped project, or a sample without it, shows the disk's size alone
(DECISIONS I-567). At 90 percent or more a line under the host
line reads `disk 95 percent full; \`repose resize todo-app 80G\` grows
it`, the size double the disk up to 320 GB. AGENTS names the one agent,
or counts several by state: `3 agents: 1 needs input, 2 working`; text
prints `needs input` in words, `--json` keeps `needs_input` (I-617).

```
$ repose logs                    # console of failed boots, last 200 lines each
$ repose logs --kind build       # the last build's output
$ repose logs --kind ops         # create/start/stop/apply/snapshot history
```

`repose ls` is that table (header row, `-` where a column does
not apply, uptime only while running), followed by one line per project
in `error` with the reason the api recorded and the command that fixes
it, e.g. `age-calculator: the environment's agent (guestd) stopped
answering; \`repose start\` restarts it.` (DECISIONS I-153), and one per
running project whose new system did not boot, which runs its previous
one (I-590), e.g. `kanali: its new system did not boot, so it runs its
previous one: ...`. While a
listed project's disk is 90 percent full or more (the api's root
filesystem figure), a `DISK` column reads `93% full` for it and `-` for
the rest, there only then, as `LEFT` is (I-567). `--json` is
the api's list, unchanged. `repose status`, `logs` and `events` take the
project as their argument (`repose logs izma -f`, I-155).

## Behaviour that must hold

Status:

- `repose ls` lists every non-destroyed project with class, state,
  uptime since the last `running` transition and agent state (counted by
  state when there are several; guestd's `unknown` is not named, I-567).
  The running hours it showed until I-616 are gone from both: nothing is
  priced by the hour (I-289). `repose status` prints the header, then
  its labelled rows.
- Agent state per window comes from guestd's latest `AgentState`
  (`working`, `idle`, `needs_input`, `unknown`) and is at most 60 seconds
  stale on a healthy guest.
- Not built: git state in `Sample` (status reads it over ssh instead,
  I-616), the base and config revision lines, and marking stale agent
  state with `?`.
- Listening processes (DECISIONS I-200, I-207): `repose status PROJECT`
  of a running guest lists each loopback or wildcard TCP listener on port
  1024 and up with its process's name, age and memory, so a dev server
  left running for days is easy to see and stop. They are read at that
  moment over the user's own SSH (`ss` and `ps` in the guest), never
  sampled or stored by the platform, whose records stay what the
  privacy policy lists. That read is the one SSH `status` makes: it uses
  an open multiplexed connection when there is one, never leaves one
  open, and is given 4 seconds; when the guest does not answer, the list
  is simply missing and every other line is the api's. Nothing is
  stopped for the user; under memory pressure the kernel kills a dev
  server before an agent, the tmux server or the SSH connection
  (guest-conventions.md "Memory pressure"), and
  the `oom` notification names what it killed. While attached, the same
  ports are forwarded to the laptop (ports-and-previews.md).
- `status` never triggers a certificate refresh, and its lines come from
  the API, so it works when the guest is unreachable, showing the last
  known data with its age. The one exception is the listening list above:
  a bounded, best-effort SSH read that is left out when it fails.
- `--json` prints the `Project` object from `interfaces/api.md` verbatim.
- Exit code is 0 even when a project is in `error`; the state is the
  information. `status X` (or `--project X`) on an unknown project exits 4.

Logs:

- `console`: what the guest printed during each boot that never reached
  Ready, as hostd captured it: the last 200 lines (16 KiB) of each, from
  the project's last 20 operations (DECISIONS I-592). A guest that booted
  cleanly has none, and the command says so. `-f` polls every 2 s. It
  holds boot messages and kernel output, not application logs, which stay
  in the guest wherever the app writes them. The whole serial console of
  every boot goes to the operators' log store (OBSERVABILITY.md).
- `build`: the most recent config build's output, complete, with secret
  values redacted (secrets.md).
- `ops`: one line per operation with timestamps, duration, and result;
  errors expanded.
- Log lines are the platform's own; nothing is read from inside
  `/home/dev`. A user's application logs are theirs and stay in the guest.
- Retention: console 7 days, build logs 90 days, ops forever with the
  project row.

Dashboard (as built; `workstreams/08-dashboard.md` §5.2 is the page list
this describes, and it is narrower than an earlier draft of this section
promised, DECISIONS I-96):

- `/projects`: one row per project — name, class, state, uptime, agent
  state, running hours today and this month. The same figures as `status`, from
  the same `usage_hours` rows. Not sortable, and no sparkline.
- `/projects/[id]`: cards for connect (the `repose run` and `ssh` lines),
  signals, usage (running hours today and this month), disk (the guest's
  root filesystem used, of the volume's size, and `N percent full` at 90
  or more, I-567) with a resize control, events newest first, the
  last build with a link to the config page, and snapshots with restore
  and restore-as-new. Start, Stop, Resize and Destroy are the header
  actions; Destroy makes you type the slug.
- `/projects/[id]/config` and `/projects/[id]/secrets` are their own
  pages, not cards: the config page carries the menu, the Nix editor, the
  streaming build log and the revision list, and the secrets page the
  names and their dates.
- `/billing`: card on file, invoices, usage for the month by class.
  `/settings`: the account (handle, email, GitHub login), timezone,
  email toggle, ntfy URL and its test button, machine.nix, and deletion
  last; `/account` redirects there (I-578).

Usage on the dashboard (DECISIONS I-492, I-493):

- The project page's Machine card spells out the size (vCPUs and memory),
  the plan memory it takes while running, and `repose resize --size`
  with the other two sizes.
- Its Usage card draws `GET /projects/:id/samples` over an hour, a day or
  a week: CPU (share of the class's vCPUs), memory in use as the guest
  sees it, time a task in the guest waited for a vCPU, and time the
  machine waited for a host CPU. Below them, the eight process names
  with the most CPU in the window. It reads the minute samples the
  platform already keeps (`meter_samples`, `proc_samples`), so it stores
  nothing new, and a stopped machine draws a gap.
- A guest from before I-493 has no pressure or guest memory figure; the
  card draws a gap there, never a zero.

Not built, documented for later (DECISIONS I-494): `repose status
--watch`, a live view refreshed every two seconds. It would read `/proc`
in the guest over the user's own SSH, as the listening list does, store
nothing, and keep working when the api is down. It costs CLI code only.
It waits until someone asks for detail finer than the minute the
dashboard draws.

## Depends on

Workstreams 05 (project and usage routes, ops history, log storage), 03
(console capture, build logs), 04 (signals including git and ports), 07
(`status`, `logs`), 08 (dashboard pages), 09 (cost figures).

## Deferred

Application log shipping from the guest (opt-in). A live view
(`repose status --watch`, above; I-494).
A state timeline and a per-meter cost breakdown on the project page, a
sortable project list with a cost sparkline, a ports card, and the account
limits on a page of their own: each was in an early draft of the Dashboard
section above and none is built (DECISIONS I-96).
