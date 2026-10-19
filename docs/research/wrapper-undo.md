# Undo on a machine we do not own (2026-10-19)

`proposals/2026-10-19-wrapper-only.md` asks what replaces the LVM thin
snapshot when there is no guest and no host of ours. Undo is the one
feature of that shape with no obvious substitute, so it was measured
before the proposal recommended anything.

**The answer: a btrfs subvolume on a plain file. A rewind point costs
0.10 to 0.15 s and 32 KiB. A fork costs about 0 s. A rewind of a wrecked
37,000-file project takes 2.19 s and brings back the untracked files and
a 200 MB database with it. Putting btrfs in a loopback file on top of
ext4, which is what makes this work on any Linux, costs 3 percent on a
sequential write and nothing measurable on small files.**

Everything below ran on woker-1 (Azure D-series, AMD, 8 vCPU, 15 GiB,
local NVMe; the 433 GB ext4 data disk, not the root disk). woker-1's disk
is faster than a laptop's and slower than a workstation's NVMe, so these
are the shape of the numbers, not a promise about a user's box. The raw
output is at the end.

## The test project

A project built to look like one an agent works in, not like a tarball:

- 30,000 files of 1 to 20 KiB in 1,500 `node_modules/pkg*/lib`
  directories
- 500 TypeScript files in `src`
- a 200 MB incompressible `app.db`, standing in for the dev database
  that HN 45505879 says Claude dropped three times
- a real `git init` with one commit

37,218 files, 460 MB apparent, 562 MB on disk, on btrfs with
`compress=zstd:1` in a 40 GB sparse file on the ext4 disk, through a loop
device.

## Taking a rewind point

| | |
|---|---|
| `btrfs subvolume snapshot -r` | 0.10, 0.13, 0.15 s |
| bytes it costs at rest | 32,768 |
| bytes after 100 MB of the 200 MB database is rewritten | +100 MiB, the changed blocks and nothing else |

A point is three orders of magnitude cheaper than today's snapshot path,
which freezes the filesystem, takes an LVM snapshot, and streams it
zstd-compressed to Blob (`features/snapshots.md`; the doc's own example
is 41 s for 1.9 GB). It is cheap enough to take on every `repose run
"prompt"`, which is what item 2 of `proposals/2026-09-26-feature-research-backlog.md`
wanted and costed at 10 to 15 s on LVM.

## Forking

A writable snapshot of the live project is the fork: 0.82 s for the
first, then 0 s. The source keeps running and nothing is copied, which is
the same property I-701 gets from thin clones on a host.

## Rewinding after an agent wrecks the project

The damage: `src` deleted, ten `node_modules` packages deleted, 50 MB of
the database overwritten with random bytes, `package.json` replaced with
garbage. 37,218 files down to 31,321.

The rewind is two commands — delete the live subvolume, take a writable
snapshot of the rewind point in its place — and takes **2.19 s**. All
37,218 files come back, `app.db` is 209,715,200 bytes again, and `git
status` is clean. The untracked files and the database come back with the
code, which is the property a file-level checkpoint cannot have.

`btrfs subvolume delete` needs root, or the `user_subvol_rm_allowed`
mount option. A daemon on the user's own box has root, so this is a note,
not an obstacle.

## The constraint: a rewind must stop the project first

With a writer running with its cwd inside the subvolume, the delete
**succeeds anyway**, the writer stays alive, and it goes on writing into
a subvolume no path reaches. Its file does not appear in the restored
project.

So `rewind` stops the project's processes before it swaps the subvolume,
the way `stop` does today, and the agent is restarted after. A rewind
that forgets this loses the agent's last writes silently, which is worse
than refusing.

## Against the file-level alternative

A shadow bare repository with `GIT_DIR` pointing away from the user's
own repo, committing everything including ignored files — the shape of
`/rewind`-class tools — over the same tree:

| | shadow git | btrfs subvolume |
|---|---|---|
| first point | 3.29 s | 0.13 s |
| a point after two source files change | 0.09 s | 0.13 s |
| a point after 20 MB of the database changes | 2.48 s | 0.13 s |
| three points on disk | 447 MB | 96 KiB + changed blocks |
| covers the database | as a whole new blob each time | yes, by block |
| covers system packages | no | only what lives in the subvolume |

Shadow git is not slow on source code; it is slow and large on
everything else, and it grows with the binary files an agent's project
holds. For scale on the same tree, `cp -a` takes 5.26 s.

## The loopback file costs almost nothing

This is what decides whether the mechanism works on a box that is
already installed, with no spare disk and no repartitioning. btrfs in a
file on ext4, against the same ext4 disk directly, with incompressible
data:

| | btrfs on a loop file | ext4, the disk itself |
|---|---|---|
| 5,000 small files, 4 KiB each | 0.72 s | 0.87 s |
| 1 GiB sequential, `conv=fsync` | 3.00 s | 2.91 s |

Three percent on the sequential write. The small-file case came out
faster on btrfs, which is within the noise of one run and not a claim
that btrfs is faster; the claim is that the loop device is not a tax
worth designing around. `mkfs.btrfs` on a 40 GB sparse file takes 0.02 s,
so provisioning is instant and the file only occupies what it holds.

**Not measured.** ZFS, which is not installed on this box: its
`snapshot`, `rollback` and `clone` are a better fit than btrfs on paper,
and `zfs diff` would answer "what did the agent touch", but it is an
out-of-tree module that breaks on a kernel upgrade of a machine nobody
on the team can log into. Docker's data directory inside the subvolume,
so that container volumes rewind too. Any of this on macOS, which has
neither btrfs nor KVM.

## Raw output

```
# undo-bench 2026-10-19T00:21:39Z on 7.0.0-1014-azure, disk nvme1n1 (433G, ext4)

## setup
mkfs.btrfs on a 40G sparse file: .02s
loop device: /dev/loop1

## the project
built in 7.88s
files: 37218, apparent size: 460M, on disk: 562M

## take a rewind point (btrfs read-only subvolume snapshot)
  snapshot 1: .15s
  snapshot 2: .10s
  snapshot 3: .13s

## fork (writable clone of the live project)
  clone 1: .82s
  clone 2: 0s
  clone 3: 0s
space used by 3 rewind points + 3 forks: total=2.01GiB, data allocated

## an agent wrecks the project, then rewind
  damage done in .39s (31321 files now)
  (btrfs subvolume delete needs root, or the user_subvol_rm_allowed mount flag)
  REWIND (delete live subvolume + writable snapshot of rew-1): 2.19s
  files back: 37218, db size: 209715200
  git reports clean: yes

## the file-level alternative on the same tree (shadow git, what /rewind-class tools do)
  first shadow commit (cold, 37k files + a 200 MB db): 3.29s
  second shadow commit (two source files changed): .09s
  third shadow commit (20 MB of the db rewritten): 2.48s
  shadow repo size after three points: 447M

## naive copy, for scale
  cp -a of the project: 5.26s


## space cost of keeping a rewind point (btrfs CoW, bytes actually used)
  used before the point: 867536896 B; after taking it: 867569664 B
  after rewriting 100 MB of the 200 MB db: 972689408 B  (+100 MiB)
  the point costs only the blocks that changed; a shadow-git point re-stores the whole 200 MB file

## loopback btrfs overhead against the bare ext4 disk under it (incompressible data)
  btrfs on a loop file  : 5000 small files .72 s, 1 GiB incompressible 3.00 s
  ext4, the disk itself : 5000 small files .87 s, 1 GiB incompressible 2.91 s

## rewind while something is still running in the project
  a writer is running with its cwd in the project (pid 2189305)
    Delete subvolume 263 (no-commit): '/mnt/nixstore/wrapper-undo/mnt/proj'
  writer still alive: yes
  rewind completed anyway
  the running writer now writes into a subvolume nothing can reach:
    its file visible in the project: no
  => rewind must stop the project's processes first, like 'stop' does today
```

The script is `undo-bench.sh` in the session scratchpad; it builds the
project, measures each mechanism and leaves nothing mounted. The two
measurements it got wrong on the first run, and which are corrected
above: `dd if=/dev/zero` through `compress=zstd` measures the compressor,
not the disk, and `btrfs filesystem df` reports chunks allocated, not
bytes used, so a 100 MB rewrite looked free.
