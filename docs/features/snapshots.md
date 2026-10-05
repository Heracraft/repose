# Snapshots

Every project's volume is snapshotted nightly and on every stop, streamed to
Azure Blob, and kept for seven days. A snapshot restores into a fresh
volume on any host, which is also how a project moves hosts and how it
survives a host dying.

## What the user sees

```
$ repose snapshots list
ID                                    TAKEN             SIZE    REASON
0199a1c2-3f40-7b8e-9d21-4c5e6f7a8b90  2026-09-17 03:00  2.1 GB  scheduled
0199a0f1-8e22-7c01-a3b4-1d2e3f405162  2026-09-16 22:14  2.0 GB  stop
01999c9b-1a07-7d55-8e66-0f1a2b3c4d5e  2026-09-16 03:00  1.9 GB  scheduled

$ repose snapshots create
Snapshot of todo-app taken in 41s.

$ repose snapshots restore 0199a1c2-3f40-7b8e-9d21-4c5e6f7a8b90
todo-app must be stopped before restoring over it: `repose stop todo-app` first, or restore into a new project with --as-new NAME.

$ repose stop && repose snapshots restore 0199a1c2-3f40-7b8e-9d21-4c5e6f7a8b90
...
Restore over the current volume? Anything since the snapshot is lost. [y/N] y
Restored todo-app; it is stopped.

$ repose snapshots restore 0199a1c2-3f40-7b8e-9d21-4c5e6f7a8b90 --as-new todo-app-yesterday
Restored into a new project, todo-app-yesterday.
```

Snapshot ids are UUIDv7 like every id (interfaces/README.md). TAKEN is
the laptop's local time. `--yes` skips the question.

## Behaviour that must hold

Taking (DECISIONS R3-6):

- Schedule: 03:00 in the host's timezone for every running project, and on
  every `stop` unless `--no-snapshot`. Manual with `snapshots create`.
- Consistency: guestd freezes the filesystem, hostd takes an LVM thin
  snapshot, guestd thaws. The frozen window is under one second; a test
  measures it. If the thaw does not arrive within 10 seconds guestd thaws
  itself and reports a warning, because a guest frozen for a minute looks
  like a hung agent.
- The LVM snapshot is streamed zstd-compressed to Blob at
  `<user>/<project>/<timestamp>.img.zst`, then removed. Only used blocks
  are read and travel: hostd reads the filesystem's bitmaps and sends the
  used, non-zero blocks (DECISIONS I-164), so a 40 GB volume with 2 GB
  used reads and uploads about 2 GB, and the time follows the data, not
  the volume's size. A volume whose journal still needs replaying (a
  guest that was killed rather than stopped) is read whole, which is
  slower and just as restorable.
- A snapshot writes a `snapshots` row with size, reason and SHA-256 only
  after the upload has finished and Blob holds as many bytes as hostd sent
  (DECISIONS I-462). hostd hashes the bytes it uploads; the digest lives in
  Postgres, out of reach of anyone who can only write to Blob.
- A running agent is not paused for a snapshot. Docker containers are not
  paused. A database mid-write in the guest gets a crash-consistent copy,
  which is what a power cut would give; the doc says so.

Retention (DECISIONS R4-11):

- Seven daily snapshots per project, oldest deleted after the newest
  succeeds, never before. Manual snapshots count toward the seven.
- After `destroy`, the last snapshot is kept 30 days and listed under
  "Recently destroyed" in the dashboard and in `repose ls
  --destroyed`; `repose restore <name>` (or the dashboard's Restore)
  brings it back as a new project (DECISIONS I-167), and `snapshots
  restore <id> --as-new` still does.
- After account cancellation, all guests stop, snapshots are kept 30 days,
  then deleted with the account's other data.
- A project that has been stopped for months keeps its most recent
  snapshot indefinitely (the seven-day window only rolls while new
  snapshots are taken), because the volume it backs is still billed and
  still exists.

Restoring:

- `restore` into the same project requires the guest to be stopped. On a
  project that is not stopped the CLI refuses and exits 5, naming `repose
  stop` and `--as-new`; it does not stop it. `repose stop` takes a final
  snapshot, which is what makes an in-place restore reversible.
- `restore --as-new NAME` creates a new project with the same class and
  volume size, on the host with the most free memory, and starts it. It
  counts toward the project limit.
- Restore works onto any host, not only the one the snapshot came from. The
  release checklist rehearses exactly that.
- A restore checks the whole blob against the recorded SHA-256 before it
  creates the new volume or any guest state, then writes from the same
  blob version and checks it again (I-462). A mismatch fails the restore
  with "snapshot checksum mismatch". A snapshot taken before I-462 has no
  digest and restores unchecked.
- A failed restore leaves no volume behind: hostd removes the new one. An
  in-place restore has already destroyed the old guest, so the project
  lets go of the old guest and its address when that destroy finishes,
  shows `error`, and `start` refuses with `restore_unfinished` until a
  restore succeeds (I-461).
- A held project (I-239) cannot be copied: a restore as a new project or
  a fork of it is refused whether or not the copy would start (I-460).

Forking (DECISIONS I-254, I-255):

- `repose fork [PROJECT] [-n N] [--name NAME] [--size S] [--snapshot ID]
  [--prompt TEXT [--agent A]]` takes a manual snapshot of a live project
  (running or stopped; the source keeps running) unless `--snapshot`
  names one of its own, then `POST /projects/:id/fork` restores it into N
  new projects called `<slug>-fork-<k>` (or `NAME-<k>`), `k` the lowest
  numbers no live project uses, each started on the host with the most
  free memory.
- The N projects are created in one transaction under the user's row
  lock: the project limit (and the xl limit for xl copies) is checked for
  all N first, and past it nothing is created (`invalid`, as for create).
  A resent request with the same `request_id` answers with the projects
  the first one made. Each copy's restore is its own op; one failing
  leaves the others running, and the CLI lists which failed and exits 1.
- A copy gets the source's volume size, configuration revision and named
  secrets (ciphertext rows copied; not the guest's sshd material), and no
  `remote_url`: the source keeps its checkout, so `repose run` there still
  means the source. The copy's checkout is the source's, at the same
  path, since `~/.repose/checkout` comes with the volume (I-368); for a
  source set up before I-368 the copy's `~/<slug>` is a symlink to the
  source's `~/<slug>`, made by guestd's `SetupProject` (I-255).
- With `--prompt`, the CLI starts the agent with that prompt in each
  running copy, without syncing the laptop into it, and does not attach.
- Each copy is a project: it counts toward the limit and is billed like
  one. There is no fork lineage in the api, no "promote a copy", and no
  dashboard action yet.

Alerts:

- A running project whose newest snapshot is older than 36 hours raises an
  operator alert, because it means the nightly job failed twice.

## Depends on

Workstreams 03 (snapshot and restore commands, LVM, Blob upload), 04
(Freeze, Thaw), 05 (snapshots routes, retention job, scheduling), 07
(`snapshots` commands), 11 (Blob container, lifecycle rules as a backstop),
10 (snapshot age metric).

## Deferred

Incremental uploads (block-level deltas between snapshots). User-set
schedules and retention. Cross-region copies. Restoring a single file or
directory from a snapshot.
