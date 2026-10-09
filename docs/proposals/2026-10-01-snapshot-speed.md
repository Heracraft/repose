# Snapshot speed (proposal, 2026-10-01)

**Status: proposal. Nothing here is decided and no code is written.** The
restore half of this work shipped as DECISIONS I-403..I-405 and I-409 (CLI
v0.1.25). This file records what limits a snapshot today, what was tried,
and the options for making stop and destroy faster, so the next session
starts from the numbers instead of measuring again.

## Where a snapshot's time goes

A snapshot (hostd `snapshotGuest`, `internal/hostd/guest/snapshot.go`)
freezes the guest, takes an LVM thin snapshot, thaws, then reads the
filesystem's used blocks (the extent format, I-164), compresses them with
`zstd -T4 -3` and uploads the stream to Blob in 8 MiB blocks, 4 at a time.

Measured on host-01 (Standard_D16s_v7, data disk Premium SSD v2, 512 GB,
16,000 IOPS, 600 MB/s provisioned), against scratch volumes holding real
snapshots:

| Stage | `job` (5.0 GB used) | `unwrap` (6.4 GB) | 19.4 GB used |
|---|---|---|---|
| dumpe2fs | 25 ms | | |
| `lvcreate -s`, `lvchange -ay -K`, `lvremove` | 50 to 100 ms each | | |
| Read the used blocks | 5.4 to 7.2 s | 9.1 to 9.6 s | 28.6 to 30.8 s |
| Read + zstd + upload | 7.1 s | 9.5 s | |
| In production (`snapshot done`) | 11.4 s | 14.1 to 14.5 s | 46 to 55 s |

The read runs at 650 to 700 MB/s, which is the disk's provisioned rate.
zstd and the upload keep up with it. Production is slower than the scratch
runs because the guests share the disk.

A snapshot therefore costs about one second per 600 MB the filesystem uses. That
is what a user waits for in `repose stop` (since I-404 the guest shuts
down during the upload, so a stop is the snapshot's length) and in
`repose rm --wait` (stop, then snapshot of the stopped volume, I-165).

## Tried and rejected (2026-10-01)

| Change | Result |
|---|---|
| `zstd -T8` or `-T16` instead of `-T4` | Same time |
| `zstd -1`, `-2`, `--fast=3` | Same time; blob 7 to 40% larger |
| Upload 8 or 16 blocks in flight, 8 or 16 MiB blocks | Upload adds about 0.2 s whatever the setting |
| O_DIRECT reads, 8 chunks in flight | 5.5 vs 5.5 s, 9.3 vs 9.1 s, 30.2 vs 28.6 s; no page-cache difference either |

Details are in DECISIONS I-404.

## Options

### 1. A faster data disk (infra, no code)

Premium SSD v2 sets throughput apart from size, so host-01's 600 MB/s can
be raised without a new disk. Snapshot reads and restore writes both scale
with it until the next limit, which is the VM size's own uncached disk
throughput. Before deciding, check:

- the D16s_v7's uncached disk throughput limit (Azure's size page);
- the monthly price per provisioned MB/s above the free baseline (Azure
  pricing page, eastus);
- that the change is applied live (`az disk update --disk-mbps-read-write`)
  and lands in `infra/` so tofu does not revert it.

Doubling the rate would roughly halve every snapshot and the write half of
every restore. It is the only option here that needs no code.

### 2. Return from stop before the upload ends

Since I-404 the LVM snapshot is fixed before the guest shuts down. A stop
could answer as soon as the guest is down and finish the upload in the
background: `repose stop` would take the shutdown time (6 to 7 s)
whatever the disk holds.

It needs:

- an op that outlives its command: the stop op ends, a snapshot op (or a
  hostd-side job reported by event) carries the upload, and the snapshot row
  appears when it lands;
- a decision for a start that comes while the upload runs (it can boot: the
  LVM snapshot is separate from the volume) and for a destroy that comes
  while it runs (it must wait for the upload, or the final snapshot is the
  previous one);
- a failure path that reaches the user after the stop said it was done: the
  existing `snapshot_failed` event and notification fit;
- reconcile: a hostd restart mid-upload finds the `snap-*` volume and either
  resumes the upload or removes it and says so (today every `snap-*` at
  start is an orphan).

This makes stops feel instant but does not make snapshots faster, and it
changes what "stopped" promises: the snapshot of that moment is not yet
safe when the command returns.

### 3. Incremental snapshots

Most of a stop's snapshot is data the previous snapshot already holds.
dm-thin knows which blocks changed between two thin snapshots of a volume:
`thin_delta` (thin-provisioning-tools) reads the pool's metadata, after
`dmsetup message <pool> 0 reserve_metadata_snap`, and lists the ranges
that differ. With the last snapshot's LVM volume kept per guest, the next
snapshot could read only the changed ranges, usually a small fraction of
the used bytes for a machine that was stopped and started.

The blob is the hard part. Today each blob is one zstd
stream of the whole extent format, so a blob cannot reuse ranges of
another. Two ways:

- **A chain.** Each blob holds the changed ranges since its parent; a
  restore applies the base and then each delta. Restores get slower as the
  chain grows, expiry must keep every parent a live snapshot needs, and a
  periodic full snapshot bounds the chain.
- **Full blobs composed on the server.** Change the format to independent
  zstd frames, one per fixed region (say 4 MiB of the volume), with an
  index. A new snapshot uploads only the changed regions' frames and copies
  the unchanged ones from the previous blob with Put Block From URL, then
  commits a block list: every blob is still a full snapshot, and expiry and
  restore keep their shape. The same framing lets a restore decompress
  regions in parallel. This one is the larger change and the better result.

Costs either way: the kept LVM snapshot holds pool space as the volume
diverges from it (bounded by the change between two snapshots); the first
snapshot after a host move, a restore or a lost kept volume is full; the
format change needs a version in the magic (the extent format's
`RPSXT001`) and a restore that reads all three formats.

### 4. Not worth doing

- Leaving caches out of the snapshot (`node_modules`, `~/.cache`): the
  snapshot would stop being the machine, and what counts as a cache is the
  user's call.
- Parallel decompression on restore without option 3's framing: a restore's
  write is disk-bound already.

## Suggested order

Option 1 first if its price is acceptable: it helps snapshots and restores
alike, today, with no code. Then option 3 with server-side composed blobs,
which turns most stops into seconds and keeps restore as it is. Option 2
can be done instead of option 3 when the stop's wait is what matters and
the snapshot's own length does not.
