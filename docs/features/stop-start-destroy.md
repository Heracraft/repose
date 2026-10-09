# Stop, start, destroy

A project's guest is always on until the user stops it (DECISIONS R1-5).
Stopping snapshots and deallocates; the disk stays, trimmed at shutdown,
and what it holds keeps counting toward the plan's disk total, and the
stopped project costs nothing (DECISIONS I-289, I-570, I-585).
Destroying deletes the disk and keeps the last snapshot for 30 days.

## What the user sees

Every command takes the project as its argument, or finds the checkout's
(DECISIONS I-155). While it waits, stderr shows the phase with a spinner
and elapsed time on a terminal, or one line per phase elsewhere (I-154).

```
$ repose stop todo-app
Snapshotting and stopping todo-app...
Stopped todo-app in 11s with a 2.1 GB snapshot.

$ repose stop --no-snapshot
Stopped todo-app in 6.2s.

$ repose start todo-app
Starting todo-app...
todo-app is running (large), ready in 4.1s.

$ repose start age-calculator          # in `error`: the api restarts it (I-157)
Restarting age-calculator...
age-calculator is running (large), ready in 21s.

$ repose start kanali                  # its new system does not boot (I-590)
Starting kanali...
Booting kanali...
kanali: its new system did not boot, so it runs its previous one: the system it boots is missing from the machine's store (stage 1 found no stage 2 init); `repose logs --kind console` shows what the new one printed.
kanali is running (xl), ready in 1m31s.

$ repose rm todo-app
Destroy todo-app? A final snapshot is kept for 30 days. [y/N] y
Destroying todo-app. Its final snapshot is kept for 30 days.

$ repose ls --destroyed
PROJECT   CLASS  DESTROYED         SNAPSHOT          SIZE    RESTORABLE UNTIL
todo-app  large  2026-09-23 02:23  2026-09-23 02:23  2.0 MB  2026-10-23

$ repose restore todo-app
Restored todo-app from its 2.0 MB snapshot of 2026-09-23 02:23 in 31s; it is running (large).
```

The destroy returns as soon as the api has accepted it (DECISIONS
I-166); the project reads `destroying` until it is gone. A destroy that
fails shows in `repose ls` and `repose status` as `error` with the
reason and the retry, and as a `destroy_failed` notification (I-165).
`--wait` waits and reports, for scripts, and never prints "Destroyed"
for a destroy that failed (I-153):

```
$ repose rm age-calculator --yes --wait
Could not destroy age-calculator: the host could not remove the volume (internal). age-calculator is still there, in state error. `repose rm age-calculator` tries again.
```

## States

`creating → building → starting → running → stopping → stopped → starting
…`, plus `restoring`, `destroying`, `destroyed`, `error`. The enum is in
`interfaces/README.md` and the CLI shows the same words.

| State | Guest | Volume | Counts toward the plan | Reachable |
|---|---|---|---|---|
| creating, building | none yet | allocating | disk, project | no |
| starting | booting | attached | memory, disk, project | no |
| running | on | attached | memory, disk, project, egress | yes |
| stopping | shutting down, snapshotting | attached | memory until `stopped` | no |
| stopped | none | kept | disk, project | no (`repose start`) |
| restoring | none | being rewritten | disk, project | no |
| destroying | none | deleting | disk until `destroyed` | no |
| destroyed | none | gone | nothing; last snapshot kept 30 days | no |
| error | may be on | attached | as `stopped`, memory while on | maybe |

A plan is a monthly price for memory that may run at once, disk, egress
and a number of projects (DECISIONS I-289); nothing is billed by the hour,
and a stopped project costs nothing. Hours are still counted and shown.

## Behaviour that must hold

Stop:

- `stop` sends `StopGuest{snapshot_first: true, timeout_s: 60}`. guestd gets
  `Shutdown`; systemd in the guest stops services, which includes tmux or
  herdr and any agent in it. The agent is interrupted; a Claude session can
  be resumed in the guest after `start` with `claude --resume`, and on a
  herdr project herdr resumes agents with an integration by itself
  (I-501). The CLI's stop line is
  followed by `Interrupted claude (working) and claude-2 (needs input).`
  when the project's newest sample showed agents working or waiting; idle
  agents are not named, and the line names no command (DECISIONS I-500,
  I-484). After 60 seconds without a
  clean shutdown, hostd shuts the VM down through Cloud Hypervisor. The
  guest's user manager, which holds every tmux pane and agent, gets 10
  seconds to stop before what is left of it is killed (I-572).
- The snapshot is taken at a filesystem freeze just before the shutdown
  starts, and uploads while the guest shuts down; the stop waits for the
  longer of the two (I-404). Its time follows the data the filesystem
  uses, read eight chunks at a time (I-571). `--no-snapshot` skips it.
- The stop line is `Stopped <slug> in <time> with a <size> snapshot.`,
  or `Stopped <slug> in <time>.` without one; a stop whose snapshot failed
  says so on stderr (I-158). It says nothing about cost: a stopped project
  costs nothing (I-570).
- SSH sessions to a stopping guest are closed; the gateway rejects new ones
  with `todo-app is stopped; run \`repose start\`` from the moment the
  state leaves `running`.
- A stopped project's secrets, config, and events are all retained and
  visible.

Start:

- `start` on a stopped project boots the same volume on the same host. If
  that host is `unreachable`, `retired` or `lost`, the api answers
  `conflict` (`<slug>'s host is <state>; restore its latest snapshot onto
  another host`, `detail.host_state`). There is no `start
  --restore-latest`; the way out is `repose snapshots restore <id>
  --as-new NAME`.
- The newest built revision not yet applied (a change that needed a
  reboot, including a base bump) is put in place during start
  (`ops.PendingRevision`).
- Start is refused with `payment_required` when billing is `past_due` for
  more than 3 days or `suspended`, with the dashboard billing link.
- `run` on a stopped project starts it implicitly; `attach` does not, and
  says to `start` or `run`. Every command that needs a running guest names
  the real state (stopped, still building, stopping, in `error` with the
  reason the api recorded) and the command that fits it (I-153).
- `start` is also the recovery path (I-157). On a project in `error`, or a
  running one whose guestd (the agent inside the environment) has stopped
  answering, it restarts: the unit is stopped without a snapshot (nothing
  can freeze the filesystem without guestd), the newest built revision is
  put in place while the guest is down, and the guest boots on it. The
  response says `restart: true`; the CLI says it is restarting, and names
  "its agent stopped answering" only when that is the reason. A running
  project with a healthy guestd still answers "already running".
- A start whose new system never reaches Ready boots the system the
  machine last ran, once (I-590): the machine runs, the revision that did
  not boot is failed and is not tried again by a start or the nightly
  base sweep, the one that booted is the applied one, and the op ends
  `done` with `result.warning`; `last_error` and a `boot_failed` event
  carry the same sentence, which `repose ls`, `repose status` and the
  dashboard show while the machine runs on it. A start whose switch of
  the running machine to a newer revision fails ends the same way, the
  machine on what it had and the revision still pending. Neither puts a
  running project in `error`.

Destroy:

- Asks `Destroy <slug>? A final snapshot is kept for 30 days. [y/N]`
  unless `--yes` (`-y`); an empty answer is no, and without a terminal the
  CLI asks for `--yes` rather than guessing (the owner's request,
  2026-09-23: the snapshot makes a typed name redundant). The CLI returns
  once the api has accepted the destroy and prints `repose restore
  <slug>` (I-166); `--wait` waits for the op and reports `Destroyed` only
  when it is done and the project is gone, and a failed op with the
  project's state and the command that retries (DECISIONS I-153,
  api.md).
- The project is `destroying` from the moment the api accepts. The op
  stops the guest if it is not stopped (no snapshot in the stop), then
  takes the final snapshot of the stopped volume, which is clean without
  a freeze, then deletes the guest (I-165). The snapshot reads only the
  blocks the filesystem uses (I-164), so its time follows the data, not
  the volume's size.
- Deletes the thin volume, the guest's units, GC roots for its closures,
  its tap and nftables entries, and the tmpfs secrets. Keeps the project row
  (`destroyed_at` set), its events, its usage, and its newest snapshot with
  `expires_at` 30 days out.
- Frees the project slot immediately for the account's limit: a
  `destroying` project does not count, one left in `error` by a failed
  destroy does (I-300). The name and remote stay taken until the destroy
  ends, so `repose run` on a `destroying` project waits for it ("Waiting
  for the old <slug> to finish destroying"), forgets the cached project
  and creates a fresh one with the same name (I-301).
- Always finishes once asked (I-156). If guestd is dead the guest cannot
  be frozen, so the unit is stopped (the hypervisor's shutdown, then a
  kill) and the final snapshot is taken of the stopped volume, which is
  crash-consistent at worst. If even that is impossible the snapshot is
  skipped with a `snapshot_failed` event and the newest earlier snapshot
  is the one kept 30 days. A guest the host no longer has is already
  destroyed. The op ends `error` only for a real host failure (deleting
  the volume, uploading the snapshot), and destroying again resumes.
- `DELETE` answers with the destroy's `op_id`; `repose rm --wait`
  waits for that op and prints "Destroyed." only when it is `done`. A
  failed destroy leaves the project in `error` with a reason that names
  `repose rm <slug>` as the retry, and sends `destroy_failed`.
- Within 30 days, `repose restore <slug>` (or plain `repose restore` in
  the project's checkout, which finds it by the git remote, I-172) brings
  it back as a new project under the same name (or `--as NEW-NAME` when a live project has it),
  from its newest snapshot or `--snapshot ID`, with its class, volume
  size, configuration and remote (I-167). `repose ls --destroyed`
  and the dashboard's "Recently destroyed" list what can be restored and
  until when. `repose snapshots restore <id> --as-new <name>` still
  works. After 30 days the snapshot is deleted by the retention job and
  neither lists it.

Account cancellation (`DELETE /me`, dashboard button):

- Every running guest is stopped with a snapshot; every project is marked
  destroyed; snapshots kept 30 days; billing closes at the end of the
  period; after 30 days the user's data is deleted except invoices and the
  audit log, which the terms say are retained.

Failure handling:

- A guest that fails to boot, with no earlier system to fall back to,
  goes to `error`, its reason classified from its console (`boot_failed`:
  stage 2 missing, a kernel panic, a failed disk check, a stage 1 error;
  else `guest_unresponsive`, I-592), and the console's last 200 lines
  attached to the op; `repose logs --kind console` shows them. The volume
  is untouched; `start` retries; `snapshots restore` is the escape.
- A dead guestd never makes an op fail instantly for ever. `stop` stops
  the unit anyway and snapshots the stopped volume; `destroy` finishes as
  above; `start` restarts. A manual snapshot and a resize need guestd (to
  freeze, to grow the filesystem) and do not reboot the guest unasked:
  they fail with a message that says to run `repose start`, and succeed
  when run again after it (I-158).
- An op's error is a sentence with the way out, and its code: code
  `guest_unresponsive`, message "the environment's agent (guestd) stopped
  answering; `repose start` restarts it". The host's own wording, with
  internal ids, is the error's `detail` (I-159).
- hostd restart mid-operation: the op is replayed by the API with the same
  command id and completes or is idempotently skipped
  (`interfaces/grpc-hostd.md`). No state is lost.
- A guest whose process sample names a known cryptocurrency miner (xmrig
  and the like, by name only) is stopped by the platform through the
  ordinary stop, snapshot first (DECISIONS I-239). `repose status`,
  `repose ls` and the dashboard then say `stopped: a cryptocurrency
  miner (xmrig) was running; mining is not allowed on repose, see the
  terms at https://repose.herakraft.co/terms`, and an `abuse_stopped`
  notification goes out. `repose start` works as usual, and a guest that
  runs it again is stopped again; the third such stop within 24 hours
  holds the project: `repose start` and a restore answer "hashy is on
  hold: it was stopped 3 times within 24 hours because a cryptocurrency
  miner (xmrig) was running ..." until an operator runs `repose-admin
  abuse clear`. The account itself is never suspended by this.

## Temporary machines

A machine for a test or a spike that nobody will want the next day
(DECISIONS I-347, built with I-348..I-355). It lives 24 hours from
creation and is destroyed with no snapshot. The public docs are
`lifecycle.md` "Temporary machines" and `cli.md`.

```
$ cd ~/code/todo-app
$ repose run --temp                          # this checkout, on a throwaway machine
✓ Created tmp-k3f9 (large, temporary: destroyed Sep 29 14:02)  4s
✓ Synced main (full history)
[tmux]

$ cd ~/Downloads
$ repose run --temp spike             # no git here: an empty machine
✓ Created spike (large, temporary: destroyed Sep 29 14:05)  4s
Not a git repository, so nothing was synced.

$ repose run --temp 3h --no-sync             # a shorter life
✓ Created tmp-q7wd (large, temporary: destroyed Sep 28 17:10)  4s

$ repose ls
PROJECT   CLASS  STATE    UP  AGENTS  TODAY  MONTH  LEFT
todo-app  large  running  3d  1       ...    ...    -
spike     large  running  2h  0       ...    ...    22h
tmp-k3f9  large  running  1h  1       ...    ...    23h

$ repose keep spike
spike is no longer temporary.

$ repose rm tmp-q7wd
Destroy tmp-q7wd? It is temporary: no snapshot is kept and it cannot be restored. [y/N] y
Destroying tmp-q7wd.
```

On `exit` from the last tmux window (a detach does not count):

```
tmp-q7wd is temporary and its session has ended; destroying it.
```

### Behaviour that must hold

Create:

- `--temp` takes an optional duration (`--temp 3h`, `--temp 90m`); bare
  `--temp` is 24h. Less than 10m or more than 24h exits 2. It is a flag
  of `run` and `sync`.
- `--temp` always creates a new project. It never resolves the
  directory's project, `--project` with it exits 2, and it never writes
  `projects.json` (`by_dir` or a remote key).
- The name is PROJECT (`repose run --temp spike`), or `tmp-` plus four lowercase base32 characters.
  A taken name goes through run's usual `name-2` retry.
- `POST /projects` carries `expires_in_s` and no `remote_url`; the api
  sets `expires_at = now() + expires_in_s` and refuses `expires_in_s`
  outside 600..86400, or with `remote_url`, with `400 invalid`.
- The billing gate and the project cap apply as to any create
  (`countsTowardLimit` unchanged). A refusal says what it says today.

Sync:

- `--temp` does not change the sync. In a checkout the whole checkout
  goes up, uncommitted work included, as for any first sync of a project
  with no remote: the CLI sends the full history itself (no GitHub
  clone). In a directory that is not a git repository, `--temp` skips the
  sync and prints `Not a git repository, so nothing was synced.` after
  the create line; `--no-sync` there prints nothing.
- A git repository with no commit, or a shallow clone, refuses with the
  usual sentence (`git init && git add -A && git commit -m init`, `git
  fetch --unshallow`, or `--no-sync`), exit 2, before anything is
  created. The same check comes before the create for every `run` and
  `sync`, temporary or not (I-353); until then it ran after the machine
  had booted. Without `--temp`, a directory that is not a git repository
  skips the sync the same way (I-358).
- The checkout gets no `repose` git remote; that name stays with the
  checkout's own project. `git fetch tmp-k3f9.repose:~/tmp-k3f9 BRANCH`
  brings back what an agent did.

Lifetime:

- The project JSON carries `expires_at` while it is temporary. `run` and
  `attach` print `tmp-k3f9 is temporary: destroyed in 5h.` before they
  attach; `ls` shows the time left in a `LEFT` column, there only
  while a temporary machine is listed (I-484); `status` shows it; the dashboard shows a `temporary` badge after
  the name and "destroyed in 5h" under the state.
- `repose keep NAME` sends `PATCH /projects/:id {expires_at: null}`. The
  project is a normal one from then on, still with no remote. `keep` on a
  project that is not temporary prints `NAME is not temporary.` and
  exits 0. Setting `expires_at` to anything but null answers `400
  invalid`.
- An hour before `expires_at` a `temp_expiring` notification goes out,
  once per project (read from the events table). A machine made with a
  lifetime of an hour or less gets none (I-350).

Expiry:

- The api's per-minute tick, under `LockSweeper`, looks at projects with
  `expires_at <= now()`, not `destroying` or `destroyed`, one per
  transaction with `for update skip locked`, checks the rule again
  inside, and enqueues the destroy with `allowQueue=true`.
- The destroy waits while the latest meter sample (under 10 minutes old)
  shows an ssh session (a laptop herdr's bridge is one, I-511), a tmux
  client, or an agent that is not `idle` or `needs_input`, and is looked
  at again the next minute. From
  `expires_at + 24h` it goes ahead regardless.
- A stopped or errored temporary project expires the same way.
- The plan is `[destroy_guest]`, also for a project with no guest yet:
  the reaper queues behind an open op, which may be the create that
  places the guest, and the phase reads the guest when it is sent and
  skips when there is none (I-350). No snapshot is taken. `markDestroyed` sets every snapshot of the
  project to `expires_at = now()`, so the nightly one, if the machine
  lived through 03:00, goes on the next expiry run.
- A failed destroy is `error` and `destroy_failed` as for any destroy,
  and the reaper tries again once 10 minutes have passed since it failed
  (I-350), so a host that cannot delete a volume does not notify every
  minute.
- When a destroy the reaper started finishes, a `temp_destroyed` event
  records it (it notifies). A `repose rm` or a session end does not: the
  user asked (I-350). The project does not appear in `repose ls --destroyed`, the
  dashboard's "Recently destroyed" or `repose restore`, since it has no
  restorable snapshot.
- `repose rm` on a temporary project uses the same no-snapshot plan and
  says so in its question.

Ending the session:

- After the attach returns, on the input-proxy path only, the CLI runs
  `tmux has-session -t =<slug>` over the still-open ControlMaster. When
  the session is gone and the project is temporary, it prints the line
  above and sends the DELETE without asking. A detach leaves the session,
  so it never destroys. An agent window still open keeps the session, so
  an agent working never loses its machine this way.
- On Windows, without a TTY or with `REPOSE_INPUT_PROXY=0` the CLI has
  exec'd ssh and cannot look; the machine waits for its expiry.
- On a herdr machine nothing ends the session: herdr opens a fresh shell
  when its last pane closes, so the CLI does not look after an attach,
  and the machine goes at its expiry (I-602). It is in the laptop herdr's
  sidebar like any herdr machine. Past the expiry, the destroy waits
  only for a working agent: the sidebar's SSH bridge (I-511) does not
  count as someone attached.

Built (I-348..I-355): `TestRunTempCreatesWithoutRemote`,
`TestTempExpiryWaitsWhileAttached`, `TestTempExpiryDestroysWithoutSnapshot`,
`TestKeepClearsExpiry`, `TestTempSessionEndDestroys`,
`TestTempWithoutRepoSkipsSync`, `TestSyncRefusalComesBeforeCreate`, the `docs_test.go`
rows for `--temp` and `repose keep`; also `TestTempDeleteKeepsNoSnapshot`,
`TestTempCreateAndKeepContract`, `TestRmAndLsOfATemporaryProject`. Still
to do after the deploy: a live run on an `e2e-*` project that reaches
expiry with a short `--temp`.

## Depends on

Workstreams 03 (StopGuest, StartGuest, DestroyGuest, cleanup, replay), 04
(Shutdown), 05 (state machine, ops, retention job, cancellation), 07
(commands and prompts), 09 (billing states gating start), 06 (rejecting
sessions to non-running guests).

## Deferred

Idle auto-stop (R1-5; needs the recorded signals). Scheduled start and stop.
Pause (Cloud Hypervisor pause without snapshot) as a cheaper stop.
