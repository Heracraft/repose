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

$ repose snapshots list            # from ~, which is no project (I-904)
PROJECT               ID        TAKEN             STORED  REASON
todo-app              4c5e6f7a  2026-09-17 03:00  2.1 GB  scheduled
api                   1d2e3f40  2026-09-17 03:00  3.4 GB  scheduled
spike (destroyed)     0f1a2b3c  2026-09-16 19:02  1.1 GB  stop

$ repose snapshots create
Snapshot 0199a1c2-3f40-7b8e-9d21-4c5e6f7a8b90 of todo-app taken in 41s (1.9 GB).

$ repose snapshots restore 0199a1c2-3f40-7b8e-9d21-4c5e6f7a8b90
todo-app must be stopped before restoring over it: `repose stop todo-app` first, or restore into a new project with --as NAME.

$ repose stop && repose snapshots restore 0199a1c2-3f40-7b8e-9d21-4c5e6f7a8b90
Stopping todo-app.
✓ Stopped todo-app  12s                  # the restore waits for the stop (I-710)
Replace todo-app's disk with its snapshot of 2026-09-16 03:00? The stop snapshot of 2026-09-16 17:49 keeps the disk as it is now. [y/N] y
Restored todo-app; it is running (large).

$ repose snapshots restore 0199a1c2-3f40-7b8e-9d21-4c5e6f7a8b90 --as todo-app-yesterday
Restored into a new project, todo-app-yesterday.
```

Snapshot ids are UUIDv7 like every id (interfaces/README.md). TAKEN is
the laptop's local time. `--yes` skips the question. The question names
the snapshot by its time and says whether a snapshot keeps the disk as it
is now (the newest, when a stop took it after the last start) or what is
lost for good; a no exits 1 (DECISIONS I-614).
`list`, `create` and `restore` act on the checkout's project, or the one
named first: `repose snapshots list izma`, `repose snapshots restore izma
SNAPSHOT_ID` (DECISIONS I-566).

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
  slower and equally restorable.
- A snapshot writes a `snapshots` row with size, reason and SHA-256 only
  after the upload has finished and Blob holds as many bytes as hostd sent
  (DECISIONS I-462). hostd hashes the bytes it uploads; the digest lives in
  Postgres, out of reach of anyone who can only write to Blob.
- A running agent is not paused for a snapshot. Docker containers are not
  paused. A database mid-write in the guest gets a crash-consistent copy,
  which is what a power cut would give; the doc says so.

Retention (DECISIONS R4-11):

- Seven daily snapshots per project, oldest deleted after the newest
  succeeds, never before. Manual snapshots count toward the seven, and
  so do a busy project's stop snapshots: the count is enforced as well
  as the seven-day window since I-900, which is what keeps a project
  stopped ten times a day to seven images rather than seventy. A
  snapshot with an `expires_at` of its own is not one of the seven and
  goes at that time (I-703; nothing writes one since I-1082).
- After `destroy`, the last snapshot is kept 30 days and listed under
  "Recently destroyed" in the dashboard and in `repose ls
  --destroyed`; `repose restore <name>` (or the dashboard's Restore)
  brings it back as a new project (DECISIONS I-167), and `snapshots
  restore <id> --as` still does.
- After account cancellation, all guests stop, snapshots are kept 30 days,
  then deleted with the account's other data.
- A project that has been stopped for months keeps its most recent
  snapshot indefinitely (the seven-day window only rolls while new
  snapshots are taken), because the volume it backs still exists and
  still counts toward the plan's disk.

Restoring:

- `restore` into the same project requires the guest to be stopped. On a
  project that is not stopped the CLI refuses and exits 5, naming `repose
  stop` and `--as`; it does not stop it. `repose stop` takes a final
  snapshot, and that snapshot makes an in-place restore reversible.
- `restore --as NAME` creates a new project with the same class and
  volume size, on the host with the most free memory, and starts it. It
  counts toward the account's project cap (100, running or stopped,
  DECISIONS I-569).
- Restore works onto any host, not only the one the snapshot came from. The
  release checklist rehearses exactly that.
- A restore downloads the blob once and checks it against the recorded
  SHA-256 as it writes the new volume, with the decompressor sandboxed
  (I-700, amending I-462's check before the write). A mismatch fails the
  restore with "snapshot checksum mismatch", removes the volume and leaves
  the guest in error, so nothing boots it. A snapshot taken before I-462
  has no digest and restores unchecked.
- A failed restore leaves no volume behind: hostd removes the new one. An
  in-place restore has already destroyed the old guest, so the project
  lets go of the old guest and its address when that destroy finishes,
  shows `error`, and `start` refuses with `restore_unfinished` until a
  restore succeeds (I-461).
- A held project (I-239) cannot be copied: a restore as a new project is
  refused whether or not the copy would start (I-460).

`repose fork` was removed (I-1082, superseding I-254, I-701, I-702 and
I-703). `repose snapshots restore ID --as NAME` is the one way left to
copy a machine's disk; `repose run --worktree -p` runs another agent on
its own branch on the same machine, and `repose run NAME` makes a second
machine for the checkout. A copy made under another name reopens none of
the source's agent windows: the records in `~/.repose` name the source's
project id, and the restore skips a record of another project (I-646).
An in-place restore keeps the project id and reopens them. The copy's
checkout is the source's, at the same path, since `~/.repose/checkout`
comes with the volume (I-368); for a source set up before I-368 the
copy's `~/<slug>` is a symlink to the source's, made by guestd's
`SetupProject` (I-255).

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
