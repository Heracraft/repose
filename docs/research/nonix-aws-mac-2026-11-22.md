# The Debian Mac appliance on a real M4, 2026-11-22 (No-Nix PR 11)

The No-Nix program's Mac appliance (`appliance-2026.11.22`, Debian 13,
kernel 6.18.57, 84 MB) had never booted on a Mac. This is the first run
of the customer's path on one: `repose host add` on an AWS
`mac-m4.metal`, the host joining the owner's account, Ubuntu machines
made, used, stopped, started and destroyed on it, then the teardown.
Spark: role `spark mac`, ticket ENG-169, branch `nonix-aws-mac`.

**Verdict.** The first published image, `appliance-2026.11.22`, could
not make a machine: every Mac customer's first `repose run` failed.
Two defects, both in the image, not in the CLI or the api:

1. **Its hostd cannot fetch a split base image.** `guest-2026.11.22.8`
   publishes `arm64-rootfs.erofs` only as `.part0` and `.part1`
   (DECISIONS I-1504, I-1506), and the hostd baked into the appliance
   predates the part fallback, so the fetch answers 404.
2. **Its root is too small for the base image cache.** hostd keeps the
   cache in `/var/lib/repose/images` on the appliance's 2 GiB root;
   the arm64 `rootfs.erofs` is 2.86 GB, so the fetch fails with "no
   space left on device".

With both fixed in a lab copy of the image (hostd from main 74850e02,
and a 16 GiB thin volume for the cache, the fix the appliance builder
is now making), the whole sequence works, and fast: the host is online
86 s after `repose host add`, the first machine is ready in 1m54s (77 s
of it the 2.9 GB base download), a start takes 33 s, a later create 38
to 43 s. The image rebuilt with both fixes, `appliance-2026.11.22.1`,
then ran the same path from GitHub at the same speed (section 7): online
40 s after `repose host add`, first machine ready in 1m25s, start 34 s.
`apps/web/src/content/docs/own-hardware.md` still says a
machine on a Mac "takes about four minutes to start"; that was the
NixOS appliance.

## 1. Method

- **Hardware.** EC2 Dedicated Host `h-0bb8c3933099faac7`, `mac-m4.metal`,
  eu-west-1a; instance `i-020ec7fa1347981e3` from the sequoia AMI
  `ami-0ee4e9086cb4fb634`, 150 GiB gp3 root. macOS 15.8.3 (24H241),
  Apple M4, 10 cores, 24 GiB, `kern.hv_support` 1, 128 GiB free.
  Driver: `ops/dev/macmini` (`MACMINI_PROVIDER=aws
  MACMINI_AWS_REGION=eu-west-1 AWS_PROFILE=repose-lab`).
- **CLI.** `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o
  repose-darwin-arm64 ./cmd/repose` from `git archive 74850e02`
  (sha256 `eb0973d9546d9e2ce983418787a0fe54def41da23c40a186cda0d41e4b960411`),
  installed as `/usr/local/bin/repose`.
- **Account.** The owner's (D-22), through a one-hour session from
  `ops/dev/dashboard-session` written as `~/.config/repose/credentials.json`
  (0600) on the Mac, minted again before each hour ran out. Every name
  created was `e2e-…`; nothing of the owner's existing projects was
  touched.
- **Customer path.** Everything ran on the Mac over ssh as the customer
  would type it, from a non-git scratch folder: `repose host add
  e2e-mac`, `repose host ls`, `repose run --temp --size small -d
  NAME`, `repose exec`, plain `ssh NAME.repose`, `repose config add`,
  `repose stop`, `repose start`, `repose rm`, `repose host rm`. Long
  commands ran detached (`( cmd >log 2>&1 </dev/null & )`); each output
  line was stamped with the seconds since the command started, and
  `~/t` (a perl wrapper) timed the short ones. hostd's own `boot
  timings` lines in the appliance console give the host-side split.
- **Not starved.** Before each measured step the Mac's load average was
  1.1 to 1.9 on 10 cores and `top` showed nothing busy.
- **Numbers.** n=1 unless two are given; a second run is given for the
  numbers that matter (start, warm create).

### A harness error, corrected

My first lab image was corrupt, and it cost three installs. I wrote the
edited root partition back into the disk image with `dd conv=sparse`,
which skips the zero blocks of the source, so wherever the new hostd
had zeros the old hostd's bytes stayed. That hostd hung at `exec`, and
the host stayed `waiting`. The release image, reinstalled between two
of those runs, joined at once, which pointed at the lab image. Written
again without `conv=sparse`, the root partition inside the image is
byte-identical (`cmp`) to the one debugfs edited, and it joins. No
number below comes from the corrupt image.

## 2. Price and billing

Pricing API (`aws pricing get-products --region us-east-1
--service-code AmazonEC2`, `regionCode=eu-west-1`, `productFamily=Dedicated Host`):
`EU-HostUsage:mac-m4`, **$1.353 an hour** on demand, so the 24-hour
minimum is $32.47. The instance adds no rate; the 150 GiB gp3 volume
is billed by the GiB-month.

| UTC | Action | Resource | Rate | Billed |
|---|---|---|---|---|
| 2026-11-22T14:04:17Z | allocate host | h-0bb8c3933099faac7, mac-m4.metal, eu-west-1a | $1.353/h | 24 h minimum |
| 2026-11-22T14:04:28Z | run instance | i-020ec7fa1347981e3 | host-billed | |
| not yet | terminate | i-020ec7fa1347981e3 | | kept up for the owner's customer review (section 8) |
| from 2026-11-23T14:04:17Z | release | h-0bb8c3933099faac7 | | 24 h minimum, $32.47 at least |

The same lines are in `/home/azureuser/briefs/nonix-aws-billing.log`.

## 3. The release image, `appliance-2026.11.22` from GitHub

| Step | Result |
|---|---|
| Download (curl on the Mac, before the install) | 84,275,749 B; 3.04 s cold (27.7 MB/s, connect 9 ms, first byte 305 ms), 0.41 s warm from GitHub's CDN; sha256 `4206efbb6471b2f2c9133fc0fd6d64e3f43924ed2b7eec37ed10ece6074ddc37`, equal to the `.sha256` asset |
| `repose host add e2e-mac` | refused at first, exit 1 in 0.3 s: "Your plan takes one host." The owner's account held PR 12's `e2e-x86`; an exempt account gets one host (`internal/billing/ownhardware.go`, `ExemptHosts = 1`). Run once PR 12 removed it. |
| `repose host add e2e-mac`, 14:18:13Z | exit 0 at 5.40 s: vfkit fetched in 2.9 s, image 0.6 s (CDN warm), unpacked 1.6 s; "launchd took no agent for this session; the appliance starts detached" |
| VM running | 14:18:19Z (launcher log) |
| hostd registered | 14:18:24.18Z (`registered_at`), 11 s after the command; the appliance booted to hostd in about 4 s |
| first stream dial | timed out at 14:18:44Z (`dial tcp 10.255.255.1:8443: i/o timeout`), connected 14:18:51Z |
| online | by 14:18:53Z, **about 40 s** after the command |
| `repose host ls` | `e2e-mac  online  18 GB  0  0  just now`; JSON: `arch` aarch64, `vcpus` 8, `mem_bytes` 18,840,113,152 |
| APFS after the join | `disk.raw` 100 GiB apparent, 283 MB allocated (`du`); the Data volume's used space +361.5 MiB, container free -379 MB |
| first create | **fails**, see below |

A second install of the release image (14:28) registered at +8.5 s and
was online at about +35 s.

**The first create, as a customer sees it.** `repose run --temp --size
small -d --host e2e-mac e2e-mac-u1`:

```
    1.73 Creating e2e-mac-u1...
   19.59 Could not start e2e-mac-u1: base image fetch failed: base image 2026.11.22.8, rootfs.erofs: the server answered 404 (internal). `repose start e2e-mac-u1` tries again.
```

hostd in the appliance console, 14:19:29Z:
`{"msg":"base image fetch failed","base_version":"2026.11.22.8","arch":"aarch64","event":"base_image_fetch","file":"rootfs.erofs","reason":"status","err":"the server answered 404"}`.

The cause, confirmed: in `guest-2026.11.22.8`, `arm64-rootfs.erofs`
answers 404 and `.part0` answers 200 (2 parts, 1,992,294,400 and
862,777,344 B, listed in `arm64-manifest.json`). The part fallback is
`baseimg.(*Cache).fetchParts`, added in f65cffe6 (2026-11-22T09:30Z).
In the decompressed release image the hostd binary
(`/usr/local/bin/hostd`, go1.26.7, 24,969,378 B) has the symbols
`baseimg.(*Cache).fetchOnce` and `fetchTo` and no `fetchParts` or
`fetchWhole` (grep count 0); the hostd built from 74850e02 has them.
The appliance cannot repair itself: its `repose-hostd-update.timer`
reads the `hostd-stable` channel, which is not published.

## 4. Lab image 1: the release image with main's hostd

Built on woker-1: the release image decompressed, its root partition
(offset 133120 sectors, 2 GiB ext4) edited with `debugfs -w` to replace
`/usr/local/bin/hostd` by `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go
build -trimpath -ldflags "-s -w -X main.version=lab-74850e02"
./cmd/hostd` from `git archive 74850e02` (sha256
`ede7129bfd4d3e5beaae5b63f8b146d58527fe75260e0267b465d49407c55eb2`,
mode 0755 root), `e2fsck -fn` clean, recompressed with zstd and
installed with `REPOSE_APPLIANCE_URL=file://$HOME/repose-appliance-lab4.raw.zst
repose host add e2e-mac` (sha256 `962f6495…c6a9`).

- host add exit 0 at 2.39 s; registered 14:37:17Z, online 14:37:23Z,
  about 64 s (the first register dial timed out at 20 s; the retry at
  50 s took).
- First create, `repose run --temp --size small -d e2e-mac-u1` (no
  `--host`: the default put it on e2e-mac):

```
    0.68 ✓ Created e2e-mac-u1 (small, temporary until Nov 23 14:37)  0.1s
   63.82 Could not start e2e-mac-u1: base image fetch failed: base image 2026.11.22.8, rootfs.erofs: write /var/lib/repose/images/.tmp-2026.11.22.8-aarch64/rootfs.erofs.part: no space left on device (internal). `repose start e2e-mac-u1` tries again.
```

The cause: `/etc/fstab` mounts `/` from partition 2, the 2 GiB ext4
root (host-conventions.md, "Mac appliance": "2 `root`, 2 GiB ext4"),
and `hostd.service` passes `--images-dir /var/lib/repose/images`,
which is on it. Root had about 1.6 GiB free (the boot's `fstrim`
trimmed 1.6 GiB); the arm64 `rootfs.erofs` alone is 2,855,071,744 B.

## 5. Lab image 2: and the cache on its own volume

Lab image 1 plus one unit, `repose-lab-images.service` (After and
Requires `repose-guest-pool.service`, Before `hostd.service`): at
first boot `lvcreate -V 16G -T vg-guests/thin -n images`, `mkfs.ext4`,
and at every boot `lvchange -ay -K` and a mount on
`/var/lib/repose/images`. The appliance builder is adding the same to
the image. Image sha256 `13a97bdf03315d753d5c3ceceec0102a00858c58885e5b629ae82d89ac5ceda3`.

| Step | Measured |
|---|---|
| `repose host add e2e-mac` (local image) | exit 0 at 2.40 s (vfkit 0.4 s, image 0.1 s, unpack 1.4 s) |
| registered | 14:40:51Z, +58 s (first dial timed out at 20 s, retry at 50 s) |
| online | 14:41:19Z, **+86 s** (the first stream dial timed out once too) |
| first create `e2e-mac-u1` | **Ready in 1m54s** (113.8 s): created 0.1 s; base image 2,915,395,010 B fetched in 76.7 s (38 MB/s, GitHub parts); hostd `boot timings` total 31.5 s (identity 1.3, volume 1.8, prepare 0.5, ready 24.4, setup 3.5) |
| `repose exec e2e-mac-u1 -- uname -a` | 1.21 s: `Linux e2e-mac-u1 6.8.0-151-generic #151-Ubuntu SMP PREEMPT_DYNAMIC ... aarch64` |
| `repose exec ... cat /etc/repose/base-version` | 1.01 s: `2026.11.22.8` |
| inside | Ubuntu 24.04.5 LTS, 1 vCPU, 3,905 MiB, `/` overlay 20G |
| `ssh e2e-mac-u1.repose` | 0.38 s and 0.34 s, user `dev` |
| `repose config add --project e2e-mac-u1 redis` | **21.85 s**, "Applied revision 68740554 in 21s"; `redis-cli ping` answers PONG |
| `repose stop` | 1.16 s and 1.22 s to return; a `repose start` run at once waited 21.0 s for the stop to finish before "Starting" |
| `repose start` on a stopped machine | **33.5 s and 33.27 s** (the first is 54.5 s less the 21.0 s wait for the stop; the second started from `stopped`); hostd total 30.7 s and 32.0 s; redis still answers PONG |
| second create `e2e-mac-u2` (cache warm) | **Ready in 38.0 s** (hostd total 32.6 s) |
| third create `e2e-mac-u3`, u2 running | Ready in 43.0 s (hostd total 37.4 s) |
| `repose rm` | 0.40 s; two at once 0.55 s |
| `repose host rm --yes e2e-mac` on the Mac | 2.46 s; `~/Library/Application Support/repose` gone, vfkit gone, APFS free back to within 6 MB of before the install |
| APFS with the base cached and two machines | `disk.raw` 4.2 GiB allocated; container free -4.6 GB |
| memory | the VM process resident 5.4 GB with two small machines running |

**Park.** Not available on this Mac: the launcher's park key lives in
the login keychain, and over ssh with no GUI login session `security
add-generic-password` fails with exit 152 ("Unable to obtain
authorization for this operation"); hostd logs "image key not fetched;
guests cannot be parked yet". A customer's Mac mini usually has a
login session; this one does not, so the park is unmeasured here.

## 6. Findings

1. **Blocker: the appliance's hostd predates I-1504**, so a split base
   image answers 404 and no machine can be made (section 3). The image
   must be rebuilt with main's hostd, or `hostd-stable` published.
2. **Blocker: the base image cache is on the 2 GiB root** (section 4).
   The image needs the cache on a volume of the pool (section 5's
   unit), or a bigger root.
3. **Every new wg session's first dial to the api times out once** (20 s,
   `dial tcp 10.255.255.1:8443: i/o timeout`), in all five installs of a sound image, at
   register or at the first stream; the retry always took. It costs 20
   to 50 s of the join.
4. **The host reports `provider: "azure"`** in `repose host ls --json`
   for a Mac appliance.
5. **The CLI says "about 200 MB"** for an 84 MB download
   (`internal/cli/appliance.go`); own-hardware.md says about 85 MB. With
   `REPOSE_APPLIANCE_URL` set it still names the pinned version
   (`fetching the appliance image 2026.11.22`) whatever it fetches.
6. **own-hardware.md's start time is stale**: "about four minutes to
   start, the first time and every time after"; measured on the Debian
   path, a start is 33 s and a later create 38 to 43 s.
7. **One host per account, exempt included** (`ExemptHosts = 1`): two
   sparks cannot test own hardware on the owner's account at once.
8. **Park unmeasured** without a GUI login session (section 5).
9. The `e2e-mac` host row was reissued from 2026-11-19 (`created_at`)
   each time, as designed (a removed host's name is free again).

## 7. The republished image

The appliance builder rebuilt the image from main with both fixes and
published it as `appliance-2026.11.22.1` (the first release is
immutable). The CLI from 74850e02 still pins `appliance-2026.11.22`
(`applianceURL` in `internal/cli/appliance.go`, until v0.1.56), so this
run set `REPOSE_APPLIANCE_URL=https://github.com/Heracraft/repose/releases/download/appliance-2026.11.22.1/repose-appliance-aarch64.raw.zst`
and otherwise typed what a customer types. Same CLI binary, n=1, load
1.2 on 10 cores.

| Step | Measured |
|---|---|
| `repose host add e2e-mac`, 15:11:41Z | exit 0 at 6.38 s: image fetched from GitHub in 3.83 s cold (84,279,305 B, about 22 MB/s; checksum `7ee4d427…62ee` verified by the CLI), unpacked in 1.60 s |
| registered | 15:11:52Z, **+11 s**, first try |
| online | by 15:12:21Z, **+40 s** (the first stream dial timed out at 15:12:16 and connected at 15:12:20) |
| the image cache volume | `repose-images-volume: event=volume_created size=16G`, `event=volume_mounted` at 7.1 s of boot |
| APFS after the join | `disk.raw` 676 MB allocated |
| `repose run --temp --size small -d e2e-mac-u1` | **Ready in 1m25s** (84.7 s): base image 2,915,395,010 B in 47.6 s (61 MB/s); hostd total 31.8 s (ready 24.2 s) |
| `repose exec ... uname -a` | 1.06 s, `6.8.0-151-generic ... aarch64` |
| `repose exec ... cat /etc/repose/base-version` | 1.12 s, `2026.11.22.8` |
| `ssh e2e-mac-u1.repose` | 0.36 s, user `dev` |
| `repose config add --project e2e-mac-u1 redis` | **22.17 s**, "Applied revision cf7091e8 in 22s"; PONG |
| `repose stop` | 1.17 s |
| `repose start` from `stopped` | **34.24 s** (hostd total 32.4 s); PONG after |
| APFS, base cached and one machine | `disk.raw` 3.8 GiB allocated |
| `repose rm --yes e2e-mac-u1` | 0.43 s |
| `repose host rm --yes e2e-mac` | 1.40 s; the Data volume back to within 3.3 MB of before |

Every number agrees with lab image 2 within its noise (start 33.3 to
34.2 s; config add 21.9 and 22.2 s), so section 5 stands for the
published image, and findings 1 and 2 are closed by it. Findings 3 to 8
still hold on it.

## 8. Teardown

The conductor kept the Mac for the owner's customer review, which
takes it after this run; so the instance is not terminated and the host
not released by this session. What this session left:

- No host on the owner's account (`repose host ls`: "You have no
  hardware of your own."), no `e2e-…` machine (`repose ls`), no
  appliance on the Mac (`~/Library/Application Support/repose` absent,
  no vfkit).
- The credentials file removed, and no `repose` item in the keychain;
  `~/.config/repose`, `~/.ssh/repose`, the `Include` line in
  `~/.ssh/config`, `~/Library/Logs/repose` and the harness files
  removed. `/usr/local/bin/repose` (the darwin build of 74850e02) is
  left in place.
- The key pair `repose-macmini-lab` and security group
  `sg-0d0dadbeca767ef3d` predate this session (2026-11-19) and stay.

The release, for whoever ends the review: the host can be released
from **2026-11-23T14:04:17Z**, with
`MACMINI_PROVIDER=aws MACMINI_AWS_REGION=eu-west-1 AWS_PROFILE=repose-lab ops/dev/macmini destroy`,
which terminates `i-020ec7fa1347981e3`, waits for it, and retries
`aws ec2 release-hosts --host-ids h-0bb8c3933099faac7 --region eu-west-1`
every 5 minutes until AWS accepts it. The host bills $1.353 an hour
until then, instance or not.
