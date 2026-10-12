# Making a frozen guest come back in seconds (2026-10-12)

The guest freeze shipped on 2026-10-12 (DECISIONS I-652, I-653) restored
four loaded guests on staging in 27 s, and the owner's verdict was "27 is
still slow". This is what the 27 s was made of, what was tried against it,
and what it is now: **the same four guests come back in 10.6 s, and after
a real `az vm restart` their machines are usable again 65 to 70 s after
they stopped rather than 88 to 98** (DECISIONS I-685).

Everything below ran on woker-1 (Azure D-series, AMD, 8 vCPU, 15 GiB,
nested KVM, local NVMe) and on host-stg-01 (Standard_D16s_v7, Intel, 16
vCPU, 62 GiB, a 512 GB Premium SSD v2 data disk at host-01's 16000 IOPS
and 600 MB/s). woker-1's disk is several times faster than a host's, so it
measures bytes and CPU, not a host's wall clock; staging measures the wall
clock.

## Where the 27 s was

A restore reads the guest's whole memory back into the hypervisor before
the VM resumes, and a freeze writes it. Four guests (small, small, large,
xl) under load held 16.1 GB of images; host-01's data disk does 600 MB/s.
16.1 GB at 600 MB/s is 27 s. Nothing in the restore was wrong: it was the
bytes.

So the question was not how to read the image faster but how to have
fewer bytes to read. Three candidates could do that -- compressing the
image, not reading it at all (on-demand paging), and cutting what the
image holds -- and one could overlap what was left (concurrency).

## What won: the image is compressed

The image volume is now btrfs with `compress-force=zstd:1`, on the same
dm-crypt as before. Compression happens above the encryption, so the disk
still sees only ciphertext (I-653 is unchanged in what it defends).

`-force` matters. Plain `compress=zstd` tries a file's first block, and
gives up on the whole file when that block does not shrink: the first
pages of a memory file are exactly that case.

Level 1 is the fastest level, and it decompresses at about 960 MB/s on one
woker-1 core -- above the host disk's 600 MB/s, so a restore stays bounded
by the disk rather than by the CPU. Higher levels compress slower, and the
freeze is the deadline-bound half.

### Bytes, through hostd's own code

`ops/dev/freeze-bench.sh hfreeze`/`hthaw` drive a bench guest through the
packages hostd uses (`freeze.Image` on a thin volume of a loop-backed VG
with 64 KiB chunks, zeroing on and discards passed down, as a host's pool
is; dm-crypt plain; `ch.Freezer`; the record store; `Fingerprint`). One
large guest under stress-ng (`--vm 1 --vm-bytes 40% --vm-keep --cpu 1
--cpu-load 60`), 3.96 GB resident, the host's page cache dropped before
each restore. "Device" is what the block layer under the pool actually
wrote and read (`/sys/block/loop0/stat`), which is what a host's disk
charges for.

| image | device written | device read back | snapshot | thin volume used |
|---|---|---|---|---|
| ext4 (I-653, shipped) | 4194 MB | 4059 MB | 6.55 s | 45.9 % |
| btrfs zstd:1 (I-685) | 681 MB | 344 MB | 4.23 s | 3.41 % |
| btrfs zstd:1, again | 685 MB | 345 MB | 4.59 s | 3.40 % |

6.1x fewer bytes written, 11.8x fewer read, a faster snapshot, and a
tenth of the pool. On woker-1's NVMe the wall clock barely moves (the
restore to a working guest went 6.4 s to 5.3-6.1 s), because that disk was
never the limit there; the bytes are the result.

### How compressible is a guest's memory, really

A synthetic load flatters compression, so three cases were measured, all
one large guest, all with the free-page-reporting balloon and the cache
drop of I-652:

| what the guest held | image bytes, ext4 | image bytes, btrfs zstd:1 | ratio |
|---|---|---|---|
| stress-ng `--vm` (synthetic) | 3.99 GB | 0.33 GB | 12x |
| 3 GB of `/dev/urandom` in tmpfs | 5.03 GB | 2.89 GB | 1.7x |
| real agent memory (see below) | -- | -- | 5.3x |

The incompressible case is the floor, and it is still a win: a guest
holding 3 GB of random bytes also holds a kernel, page tables, zeroed
pages and a store cache, and those compress. Its snapshot was *faster*
with compression (7.5 s against 5.5 s), because less had to reach the
disk.

The 5.3x is woker-1's own agent processes, which is the closest thing to
hand to a tenant's working memory: every resident anonymous page of four
Claude Code and codex processes (0.85 GB in all), read through
`/proc/<pid>/pagemap` and piped to `zstd -1`. **5x is the honest figure to
plan with.**

### Four at a time, both ways

I-652 froze and restored two guests at a time, because snapshots shared
one disk. With the image compressed, both halves are bounded by one core
per guest instead, so four at once finish sooner: on host-stg-01 the four
guests froze in 38.8 s at four against 45.3 s at two, and the thaw was
10.6 s either way (there the disk is still the limit at four). Each guest
is paused a little longer and all of them are done sooner, which is what
their gap is made of. `RestoreConcurrency` is never fewer than
`Concurrency`.

### Room for the pathological image

The image volume is now the class's memory plus 1536 MiB, not plus 256
MiB. btrfs allocates data in chunks of up to 1 GiB and needs a metadata
and a system chunk besides, so a guest whose memory is full of
incompressible bytes needed headroom the old size did not have. The volume
is thin: what it does not write costs nothing in the pool.

## What was rejected, with numbers

### `memory_restore_mode=ondemand` (lazy restore through userfaultfd)

Cloud Hypervisor 53's on-demand restore maps the image with userfaultfd
and pages it in as the guest touches it. I-652 had already rejected it on
the 2026-10-11 evidence; it was re-measured here because CH 53 does now
prefault in the background, which looked like an answer to the objection
that the image can never be released.

One large guest, same load, encrypted image, cold host cache:

| | copy (shipped) | ondemand |
|---|---|---|
| hypervisor's restore call | 3.94 s | 0.52 s |
| guest answering over vsock | 4.73 s | 1.79 s |
| a command run in the guest | 5.0 s | 2.7 s |
| image released by the hypervisor | at once | 13.5 s |
| the *next* freeze's image | 4.07 GB | 8.59 GB (the whole class) |
| the *next* snapshot | 4.1 s | 13.0 s |

It is genuinely faster to first answer, and it is still the wrong trade:

- **The image is held for the life of the prefault.** CH's handler
  prefaults one page per idle loop and walks every page of every range,
  holes included, so a sparse 8 GiB image takes 13.5 s of wall time (and
  far longer under load) before the file can be released. The image is
  encrypted guest memory on the host's disk, and I-653's lifecycle --
  removed as soon as it has been read -- is what keeps that window short.
- **The guest's memory is left fully populated.** Every page the prefault
  installs is a page the hypervisor's memfd now holds, free-page
  reporting or not, so the next freeze writes the whole class size. That
  is a 2x worse image and a 3x worse snapshot for the rest of the guest's
  life -- the opposite of what this work is for.
- **vhost-user is not supported.** Upstream's `docs/snapshot_restore.md`
  on main says on-demand restore does not support vhost-user devices. A
  repose guest has two: the store share and the login share. The hazard is
  visible in CH 53's source: the uffd is registered on the guest's memory
  mappings, while virtiofsd maps the same memory directly through the
  vhost-user memory table and does not fault through the hypervisor's
  handler.

Compression gives a bigger win on the number that matters (bytes), keeps
the image's lifetime short, and leaves the guest's memory sparse.

### Compressing the image file after the snapshot (`zstd -1 -T4`)

Measured only as a ratio, not built: the snapshot would have to be written
once and read and rewritten again, inside the freeze's deadline, and the
restore would have to decompress to a temporary file before the hypervisor
could read it, in the window where hostd answers nothing. Filesystem
compression gets the same bytes with none of that.

### Restoring the guests with working agents first

Ordering changes nothing when every restore is bounded by one shared disk
and all of them start within seconds of each other, and it would add a
priority input (I-646's agent-window record) for no measured gain.
Smallest first stays for the freeze, where the deadline decides how many
guests are saved.

### Dropping the guest's page cache before the snapshot

Already in I-652, and still worth it: it is what makes free page reporting
hand the pages back before the image is written.

## Staging: the four-guest run

host-stg-01 at host-01's shape: Standard_D16s_v7 (Intel, 16 vCPU, 62 GiB)
with a 512 GB Premium SSD v2 data disk at 16000 IOPS and 600 MB/s, the
freeze on with staging's own Key Vault, `hostdev` on the staging edge as
the api. Four guests of one closure -- `small`, `small`, `large`, `xl` --
each holding text-like bytes in shmem (1.6, 1.6, 3.3 and 6.5 GB; symbol
tables and strings out of the store's binaries, which is the closest thing
to an agent's heap that a staging guest can hold), each running a
one-second appender, a tmux window with a counter and a reader touching
its memory. 18.6 GB of guest memory in all.

**The same guests, the same memory, in both arms**: `before` is main's
build (ext4 images, two at a time), `after` is this branch (btrfs with
zstd:1, four at a time). The host was switched between arms with
`switch-to-configuration`, which restarts hostd and stops no guest.

### The operator's path: `hostd freeze` then `hostd thaw`

| | before | after |
|---|---|---|
| freeze, four guests | 50.0 s | 38.8 s |
| per guest | 13.5 / 14.8 / 28.7 / 29.3 s | 22.3 / 25.2 / 31.4 / 33.0 s |
| image bytes in the filesystem | 18.6 GB | 18.5 GB |
| **image bytes on the disk** | **17.4 GB** | **4.87 GB** |
| thaw, four guests | **31.8 s** | **10.6 s** |
| per guest | 31.4 / 8.3 / 24.8 / 25.5 s | 5.2 / 5.2 / 6.9 / 10.3 s |

The per-guest freeze times rise while the total falls: four snapshots
share the cores instead of two sharing the disk. What a guest feels is the
total, because it stays paused until the host goes down.

### A real restart: `az vm restart`

| | before | after |
|---|---|---|
| the call to Azure's shutdown request | 25 s | 23 s |
| the request to "freezing guests" | 0.4 s | 0.4 s |
| four guests frozen | 56.1 s | 35.9 s |
| the request to the next kernel | 77 s | 57 s |
| hostd started to every guest restored | 30.9 s | 10.1 s |
| per guest | 7.7 / 18.1 / 25.4 / 30.8 s | 5.0 / 5.0 / 6.7 / 10.1 s |
| **the call to every guest running** | **142 s** | **100 s** |
| **the guest-visible gap** | **88 to 98 s** | **65 to 70 s** |
| `az` returned after | 103 s | 72 s |

### What the restore spends its time on

The phase timings hostd now logs, from the `az vm restart` above (one
line per guest): `net_ms` 49 to 51, `virtiofsd_ms` 65, `store_view_ms`
644, `image_read_ms` 3167 to 7855, `resume_ms` 166 to 602, `guestd_ms` 92
to 137. Reading the image is still almost all of it, which is why its size
is the whole story; the 0.6 s of store view and 0.2 s of plumbing are the
floor a restore cannot go below today.

### Nothing was lost, either way

In both arms, for all four guests: the same guest boot id before and
after, the same pids for the appender and the tmux pane, the tmux
counter continuing with no gap in its sequence, shmem intact to the
megabyte, the guest clock within a second of the host's, and no image
volume, record or dm mapping left behind (`lvs` back to the pool's 0.51
percent, `ls /var/lib/repose/hostd/freeze` empty, `dmsetup ls | grep
repose-frz` empty). `systemctl --failed` was empty on the host after
every cycle.

### The ratio is the result

An earlier pass of the same scenario filled the guests with whole store
files instead, much of which is already compressed (kernel modules,
firmware, archives): 1.35x, and the thaw went 27.1 s to 20.1 s. The same
code, the same disk, a quarter of the gain. **What a restore costs is what
the guest's memory compresses to**, so the honest promise is "about a
fifth of the bytes, and so about a third to a fifth of the time, for
memory that looks like an agent's" -- not a fixed number.

## What correctness still looks like

Through hostd's own code on woker-1, with the compressed image: the guest
kept the same pids, its tmux session, the appender's log with no step
back, and its clock within a second of the host's; after each cycle no
image volume, no dm mapping and no record was left, and the thin pool
returned to 0.00 percent. The host-freeze VM test builds an image volume
the way `freeze.Image` builds it and writes 512 MiB of guest-like pages:
22441984 bytes reach the disk.
