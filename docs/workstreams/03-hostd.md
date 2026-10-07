# 03. hostd

## 1. Goal

`hostd` is the only program on a host that touches tenants. It turns commands
from the api into guests: it builds their closures, carves their volumes,
wires their network, runs Cloud Hypervisor, snapshots them, and reports what
they are doing. If hostd is correct, a host is a fleet of appliances; if it is
sloppy, one tenant's mistake becomes another's outage.

## 2. Scope: builds

- `cmd/hostd/main.go` and the packages under `internal/hostd/`:
  `register`, `stream`, `guest` (state machine), `lvm`, `net` (nftables,
  tc, tap), `ch` (Cloud Hypervisor API client), `virtiofs`, `nixbuild`,
  `gcroot`, `snapshot`, `samples`, `console`, `vsockclient`, `state`
  (bbolt), `metrics`.
- Registration with a one-shot join token, mTLS certificate storage and
  30-day rotation.
- The bidirectional gRPC stream: Hello with reconciliation, heartbeats,
  command execution with idempotency, result storage, replay after restart.
- Every command in `interfaces/grpc-hostd.md`: `CreateGuest`, `StartGuest`,
  `StopGuest`, `DestroyGuest`, `ResizeVolume`, `Build`, `ApplyConfig`,
  `Snapshot`, `Restore`, `UpdateSecrets`, `SetPrincipals`, `Exec`, `Drain`.
- Samples every 60 seconds merging host-side counters with `guestd`'s
  `Sample` reply, and Events for state changes, agent events, snapshots and
  host warnings.
- Console capture per guest to `/var/lib/repose/guests/<id>/console.log`
  with rotation, shipped by Fluent Bit (workstream 10 owns the shipping
  config; hostd owns the file).
- The nightly snapshot timer (`repose-snapshot.timer` calls `hostd
  snapshot-all`, which enqueues a `Snapshot` per running guest locally, not
  via the api, so a control-plane outage never skips a night).
- Prometheus `/metrics` on the WireGuard address, port 9100 is
  node_exporter, hostd on 9101.
- A `hostd` subcommand set for operators: `hostd status`, `hostd guests`,
  `hostd reconcile`, `hostd snapshot-all`, `hostd drain`.

### Dev driver `cmd/hostdev` (DECISIONS I-17)

The M1 gate is reached before the api exists, so this workstream also ships
`cmd/hostdev`: a single binary that plays the api side of
`interfaces/grpc-hostd.md` for exactly one host. It listens for the host's
Session stream with a self-signed CA it generates on first run (`hostdev
init` prints the join token and writes the client certificate material hostd
expects), keeps state in one JSON file, and exposes the commands as
subcommands: `hostdev create --project todo --class large --fragment ./f.nix`,
`start`, `stop`, `destroy`, `build`, `apply`, `snapshot`, `restore`,
`secrets set`, `exec`, `drain`, `status`, `logs` (streams BuildLog), and
`samples` (prints the last Samples). It is the tool the M1 integration
session uses and the one operators keep for a host that has lost the api.
It must not grow scheduling, users or billing; it is one host, one operator.

## 3. Scope: does not build

- The host NixOS configuration, bridge, thin pool creation, WireGuard,
  Fluent Bit, node_exporter: workstream 01.
- The guest base module and the microvm.nix runner function hostd calls:
  workstream 02. hostd invokes `nix build` on a flake reference; it does not
  author Nix.
- `guestd` itself: workstream 04. hostd only speaks the vsock protocol.
- The restricted evaluation rules, build limits and error mapping for user
  fragments: workstream 12 owns the policy and the `nix.conf` fragments;
  hostd applies the numbers it is told in the `Build` command.
- The api's side of the stream, the scheduler, the database: workstream 05.
- Metric and log naming conventions: workstream 10; hostd follows them.

## 4. Interfaces owned / consumed

Owned: message shapes in `interfaces/grpc-hostd.md` (jointly with 05, which
owns the server), `interfaces/vsock-guestd.md` (jointly with 04).

Consumed: `interfaces/host-conventions.md` (paths, bridge, nftables table
names, unit names), `interfaces/guest-conventions.md` (secrets path, boot
expectations).

Fake it provides: `internal/fakes/hostd`, the in-process client the api's
tests use. hostd's own tests use `internal/fakes/guestd` over a Unix socket.

## 5. Design detail

### 5.1 Process layout

One process, several goroutines with clear ownership:

```
main
 ├─ register.EnsureCertificate()          once, then a 30-day rotation ticker
 ├─ state.Open("/var/lib/repose/hostd/state.db")
 ├─ guest.Manager (owns the per-guest state machines, one goroutine each)
 ├─ stream.Run (dial, Hello, heartbeats, dispatch commands to Manager)
 ├─ samples.Loop (60 s ticker, asks Manager for the current guest list)
 ├─ console.Tailers (one per running guest)
 └─ metrics.Serve
```

The Manager is the single writer to bbolt. Commands are dispatched to the
guest's goroutine by `guest_id`; that goroutine executes them serially. A
host-level semaphore bounds concurrent guest operations at 8 and builds at 2
(`--max-ops`, `--max-builds`). `Build` for one project and `StartGuest` for
another may overlap; two commands for the same guest never do.

### 5.2 Registration

At first start `/var/lib/repose/hostd/cert.pem` does not exist. hostd reads
`/run/repose/join-token`, collects `HostInfo` (hostname, `sku` from IMDS
`compute/vmSize` when IMDS is reachable from the host, `mem_bytes` from
`/proc/meminfo`, `vcpus` from `nproc`, `nixos_system` from
`/run/current-system`, `ch_version` from `cloud-hypervisor --version`,
`pool_bytes` from `lvs --units b vg-guests/thin`) and calls `Register` over
TLS with server verification only. It writes the returned certificate, key,
`host.json` (`host_id`, `guest_cidr`, `wg_private_key`, edge peer) with mode
0600, then deletes the join token. If Register fails, hostd exits non-zero
and systemd retries every 30 seconds; a token that has been consumed
produces `register: join token already used`, logged and the unit stops
retrying (`Restart=on-failure` with `RestartPreventExitStatus=3`).

WireGuard keys returned at registration land in `host.json` and nowhere
else: `repose-register.service` restarts `repose-host-net`, which renders
`/run/repose/wg0.conf` for `wg-quick-wg0.service` (DECISIONS I-18). hostd
wrote a second `wg0.conf` under its state directory until I-137 removed
it (security review M-5: two writers of one tunnel and a second copy of
the private key on the persistent disk).

Rotation: 5 days before `cert_expires_at`, call `Rotate` on the unary API
with the current certificate; on success swap files atomically (`rename`)
and reconnect the stream.

### 5.3 The stream

`stream.Run` dials `api.repose.herakraft.co:443` with the client
certificate, opens `Session`, and sends `Hello` built from bbolt: every
guest id, its stored state, `free_mem_bytes`, `pool_free_bytes`. The api
replies with any commands it considers unfinished; hostd looks each
`command_id` up in bbolt:

- result stored: send it again;
- started, not finished (hostd died mid-command): re-execute if the command
  is idempotent by construction (all of them are written to be: `lvcreate`
  checks existence first, `systemd-run` checks the unit, `nix build` is
  idempotent), else return `internal: command interrupted` for `Exec`;
- unknown: execute normally.

Heartbeats every 15 seconds, and one ahead of every Result (I-409). Reconnect on any stream error with backoff 1,
2, 4, 8, 16, 30, 30... seconds and jitter. Commands received while a
previous instance of the same `command_id` is still executing are
acknowledged and ignored (the result will be sent when the first finishes).

Results are written to bbolt before being sent, so a crash between the two
still produces a result on the next Hello. Results older than 7 days are
pruned.

### 5.4 Guest state machine

States are the enum in `interfaces/README.md`. Transitions hostd performs:

```
(none) ─CreateGuest─▶ creating ─▶ starting ─▶ running
running ─StopGuest─▶ stopping ─▶ stopped
stopped ─StartGuest─▶ starting ─▶ running
stopped|running ─DestroyGuest─▶ destroying ─▶ destroyed
(none) ─Restore─▶ restoring ─▶ stopped ─▶ (StartGuest by api)
any ─(failure)─▶ error   (with reason; api decides what next)
```

`building` is an api-side state during `Build`; hostd reports build progress
through `BuildLog`, not through guest state, because a build may run for a
project that has no guest yet.

Every transition is written to bbolt first, then executed, then confirmed,
so `Hello` can always say where each guest was. Every transition emits
`Event{guest_state_changed}`.

### 5.5 CreateGuest, step by step

Inputs: `project_id`, `guest_id`, `class`, `volume_bytes`, `system_closure`,
`secrets`, `env`, `ssh_ca_pub`, `principals`, `hooks_config`.

1. Validate: closure exists (`nix path-info <closure>`), class known,
   `volume_bytes` within 10 GB to 2 TB, memory for the class is available
   after reserve (`free_mem_bytes - 16 GiB >= class RAM`), else
   `insufficient_capacity`.
2. Allocate an IP and MAC: next free index in bbolt for the host's `/22`,
   MAC `52:54:` plus the first four bytes of the guest id. Write to bbolt as
   `creating`.
3. Volume: `lvcreate -V <volume_bytes>b -T vg-guests/thin -n g-<guest_id>`,
   then `mkfs.ext4 -L guest -E lazy_itable_init=1 /dev/vg-guests/g-<id>`
   (the guest kernel zeroes the inode tables in the background; DECISIONS
   I-162).
   Skip both if the volume already exists with the label (idempotent
   re-run).
4. GC root: `ln -sfn <system_closure>
   /nix/var/nix/gcroots/repose/<guest_id>`.
5. Runner: `nix build --out-link /var/lib/repose/guests/<id>/runner
   <platform-flake>#runner --override-input system <closure> ...` per
   workstream 02's documented runner function signature. The runner script
   takes the guest id, volume path, tap name, vsock CID, memory and vCPU
   count as arguments so one runner serves all guests of a base version.
6. Network: `ip tuntap add tap-<8hex> mode tap user hostd`, `ip link set
   tap-<8hex> master br-guests up`; `nft add element repose guests { <ip>
   . tap-<8hex> }` (a set the ruleset's chains reference, so per-guest rules
   are set membership, not new chains); `nft add counter repose
   egress-<guest_id>` and a rule in `guest_fwd` counting from `<ip>`; `tc
   qdisc add dev tap-<8hex> root handle 1: htb default 10`, `tc class add
   ... rate 200mbit ceil 200mbit`.
7. Secrets: held in memory only. Nothing is written to host disk. After
   `Ready` (step 10) they are delivered with `guestd WriteSecrets` into the
   guest's own tmpfs, per `guest-conventions.md`.
8. virtiofsd: `systemd-run --unit virtiofsd@<id> --uid virtiofsd
   virtiofsd --socket-path .../virtiofsd/virtiofsd.sock --shared-dir
   /run/repose/store-export --sandbox namespace --cache auto --xattr
   --socket-group hostd`. virtiofsd has no read-only flag; read-only is
   enforced twice: the `virtiofsd` user has no write permission anywhere
   under the export, and the guest mounts the tag with `ro`. The socket
   directory is `0750 virtiofsd:hostd` inside the `1770 root:hostd` guest
   directory (host-conventions.md "Filesystem").
9. Cloud Hypervisor: `systemd-run --unit guest@<id> --property
   MemoryMax=<RAM+512M> --property CPUQuota=<vcpus*100>% --property
   Restart=no --property User=hostd` plus the sandbox properties of
   DECISIONS I-51 (`DevicePolicy=closed` with `/dev/kvm`, `/dev/net/tun`
   and the guest's volume allowed, a private view of the guests directory,
   no capabilities, `AF_UNIX`/`AF_VSOCK` only), then
   `/var/lib/repose/guests/<id>/runner/bin/run <args>`. Cloud Hypervisor
   never runs as root: an escape lands as `hostd` holding three device
   nodes and one directory. The
   runner starts CH with `--api-socket .../ch.sock`, `--serial file=...
   console.log`, `--vsock cid=<cid>,socket=...`, `--net tap=tap-<8hex>`,
   `--disk path=/dev/vg-guests/g-<id>`, `--fs tag=store,socket=...`,
   `--cpus boot=<n>`, `--memory size=<RAM>,shared=on` (shared is required
   for virtio-fs).
10. Wait for `Ready` from guestd over vsock (connect retry every 500 ms, up
    to 60 s; `guest_unresponsive` after). On Ready: `WriteSecrets`,
    `SetPrincipals`, `SetupProject`. State `running`. Result carries
    `guest_ip` and `vsock_cid`.

Failure at any step rolls back what that step created (tap, nft elements,
tc, units) but never the volume, which is the tenant's data; the guest goes
to `error` with the step named: `create: step 9 (cloud-hypervisor) failed:
<stderr tail>`.

### 5.6 Stop, Start, Destroy, Resize

`StopGuest`: if `snapshot_first`, freeze, take the LVM snapshot and thaw
(Snapshot steps 1 to 3), then upload it while the guest shuts down, and
return when both are done (DECISIONS I-404). An upload that fails is
taken again from the stopped volume; a freeze that fails stops nothing.
The shutdown is `guestd Shutdown(timeout_s)`; wait for the `guest@<id>` unit to exit; after
`timeout_s` (default 60) send `shutdown` via the CH API; after a further 15
seconds `systemctl kill guest@<id>`. Tear down tap, tc, nft membership,
virtiofsd unit. State `stopped`. The volume, GC root and IP allocation
remain.

`StartGuest`: steps 6 to 10 of Create, reusing the stored IP and MAC and the
GC-rooted closure. If the GC root is missing (a host rebuilt from a
snapshot restore path), fail with `not_found: system closure missing; api
must rebuild` so the api triggers a `Build`.

`DestroyGuest`: Stop if running, then unless `keep_volume`, `lvremove -f
vg-guests/g-<id>`, remove the GC root, release the IP, delete the guest
directory, drop bbolt entries, and remove the nft counter after reading its
final value into one last `Samples` message. State `destroyed`.

`ResizeVolume`: grow only; `lvextend -L <new>b vg-guests/g-<id>`, then if
running `guestd GrowFs`, else nothing (the guest resizes at next boot via a
`guestd` startup check). Shrinking returns `invalid_argument: volumes only
grow`.

### 5.7 Build

Inputs: `project_id`, `revision_id`, `fragment`, `base_ref`, `limits`.

1. Write the fragment to `/var/lib/repose/builds/<revision_id>/fragment.nix`
   (0600, hostd user).
2. Evaluate: `nix eval --raw --option restrict-eval true --option
   allow-import-from-derivation false --option pure-eval true
   --max-call-depth 10000 --option eval-cache false
   <platform-flake>#guestSystem.<base_ref> --apply 'f: f { fragment =
   /var/lib/repose/builds/<rev>/fragment.nix; }'` under `timeout
   <eval_s>`. The exact flake expression is workstream 12's; hostd runs
   what 12 documents in `interfaces/nix-build-contract.md` (12 writes that
   file). Non-zero exit maps to `eval_failed` with stderr verbatim, and
   `fragment_line` parsed from `at /var/lib/repose/builds/<rev>/fragment.nix:<line>:`.
   Exit 124 from `timeout` maps to `eval_failed` with message `evaluation
   exceeded 60 s`.
3. Build: `nix build --no-link --print-out-paths --option sandbox true
   --max-jobs 1 --cores <cores> --option substituters
   'https://cache.nixos.org https://cache.repose.herakraft.co' <drv>` under
   `timeout <build_s>` and `systemd-run --scope -p CPUQuota=<cores*100>%
   -p MemoryMax=16G`. Stream stdout and stderr line by line as `BuildLog`.
   Non-zero exit is `build_failed` with the last 32 KB of stderr; exit 124 is
   `build_timeout` with `build exceeded <build_s> s; last derivation: <name
   from the last "building '/nix/store/...drv'" line>`.
4. Closure size: `nix path-info -S <out>`; over `closure_bytes` is
   `closure_too_large` with the ten largest paths from `nix path-info -rS
   <out> | sort -k2 -n | tail`.
5. GC root at `/nix/var/nix/gcroots/repose/rev-<revision_id>` (kept until
   the api sends `DestroyGuest` or a later `ApplyConfig` supersedes it and
   the api asks for pruning via a `Build` result ack; simpler rule: keep
   the last 3 revisions per project, pruned on each successful Build).
6. `kernel_changed`: compare `<out>/kernel` and `<out>/initrd` targets with
   the running guest's (stored in bbolt at last Apply). Result carries
   `system_closure`, `closure_bytes`, `kernel_changed`.

Builds are queued FIFO with a depth limit of 20; beyond that
`insufficient_capacity: build queue full`. Queue depth is a metric and a
`host_warning` at 10.

### 5.8 ApplyConfig

`guestd Switch(system_closure)`. If the reply says `needs_reboot`, and the
command did not carry `force_reboot`, return `ok` with `rebooted=false` and
a payload flag `reboot_required=true`; the api tells the CLI to ask the user.
With `force_reboot`: Stop (with snapshot) then Start using the new closure;
`rebooted=true`. Update the guest's GC root symlink to the new closure and
record the new kernel and initrd in bbolt.

### 5.9 Snapshot and Restore

Snapshot:

1. `guestd Freeze` (guestd's own 10 s watchdog protects against a hostd
   crash here).
2. `lvcreate -s -n snap-<guest_id>-<ts> vg-guests/g-<guest_id>`.
3. `guestd Thaw`. Freeze window measured and exported as a histogram; over
   2 s is a warning.
4. `lvchange -ay -K vg-guests/snap-...`, then `dumpe2fs` the snapshot.
   A clean ext4 (state `clean`, no `needs_recovery`: a freeze and a clean
   shutdown both leave it so; a journalled ext4 keeps `clean` while
   mounted, so a frozen running guest qualifies, I-171) is streamed in the extent format
   (DECISIONS I-164): the blocks the bitmaps mark used, minus 64 KiB
   pieces that are all zero, framed as offset/length records, through
   `zstd -T4 -3`. A killed guest's journal (`needs_recovery`) is first
   replayed on the snapshot (`e2fsck -E journal_only -p`, sandboxed with
   write access to the snapshot alone; the guest's volume is untouched),
   after which the snapshot qualifies (I-594). Anything else (a replay
   that fails, a state other than `clean`, a volume that is not ext4)
   falls back to `dd if=/dev/vg-guests/snap-... bs=4M
   status=none | zstd -T4 -3`, which reads the whole volume. Either goes
   into the Azure Blob Go SDK's
   block-blob `UploadStream` (block size 8 MiB, concurrency 4). azcopy is
   not used because it cannot read from a pipe. Blob path
   `<user_id>/<project_id>/<ts>.img.zst`, metadata `guest_id`, `class`,
   `volume_bytes`, `reason`. Authentication is the host's managed identity
   scoped to the `repose-snapshots` container (infra grants it); IMDS is
   reachable from the host, only guests are blocked.
5. `lvremove -f vg-guests/snap-...`. Even on upload failure.
6. `Event{snapshot_done}` and the Result with `snapshot_id` (the api
   assigns it; hostd returns `blob_path` and `bytes`).

The raw fallback reads the whole logical volume: upload size stays
proportional to real data (the pool runs with `--discards passdown` and
guestd runs `fstrim /` weekly, so unallocated blocks read as zeros and
zstd compresses them to almost nothing), but time does not. Before I-164
every snapshot took that path: 15-16 s for every 20 GB volume and 32-34 s
for every 40 GB one on host-01, whether it carried 1.5 MB or 227 MB. The
`snapshot done` line carries `format`, `raw_reason`, `journal_replayed`,
`used_bytes` and `volume_bytes`, so a slow snapshot says which path it took. Uploaded
bytes are reported in the Result so the cost stays visible.

Restore:

1. `lvcreate -V <volume_bytes>b -T vg-guests/thin -n g-<new guest_id>`.
2. Download the blob as 8 MiB ranged GETs, eight at once and pinned to
   its ETag, in order through `zstd -d`. An extent stream (magic
   `RPSXT001`) is written record by record into the new volume, which
   reads as zeros everywhere else; a raw stream (every snapshot from
   before I-164) is written 4 MiB at a time, skipping chunks that are all
   zero. Both go around the page cache (O_DIRECT, eight writes in
   flight), then `fsync`: written through it, the writeback drained into
   the thin volume at 34 MB/s (DECISIONS I-403).
3. `e2fsck -fp` on the volume; a non-zero exit above 1 fails the restore
   with `internal: filesystem check failed after restore`.
4. Continue as CreateGuest from step 4 with the closure the api passed
   (the api rebuilds if needed), ending in `stopped`, not `running`; the
   api sends `StartGuest` separately so a restore can be inspected first.

### 5.10 Samples

Every 60 seconds, for each guest in bbolt:

- `cpu_ns_delta`: from `guest@<id>` unit's `CPUUsageNSec` via D-Bus
  (`systemctl show -p CPUUsageNSec`), delta since last sample.
- `mem_rss_bytes`: unit `MemoryCurrent`.
- `net_tx/rx_bytes_delta`: `/sys/class/net/tap-<8hex>/statistics/` (tx on
  the tap is rx for the guest and vice versa; hostd reports from the
  guest's point of view).
- `disk_alloc_bytes`: from bbolt; `disk_used_bytes`: `lvs --units b -o
  data_percent` times size.
- `signals` and `procs`: from `guestd Sample`, with `guestd_ok=false` and
  empty lists when the vsock call fails.

Plus `HostSample`. The message is sent on the stream; if the stream is down,
samples are buffered in memory up to 60 minutes and sent on reconnect, then
dropped oldest-first, with a counter of dropped samples.

### 5.11 Exec

Only accepted when the command carries an `audit_id` the api attached; hostd
logs `{audit_id, guest_id, argv}` to journald at `notice` before running
`guestd Exec`. Output capped at 64 KB each.

### 5.12 Drain

Sets bbolt `draining=true`, reports it in the next heartbeat (`draining`
field, added to the heartbeat by this workstream, update grpc-hostd.md),
rejects `CreateGuest` and `Restore` with `insufficient_capacity: host
draining`, and continues serving everything else.

### 5.13 Console capture

The runner passes `--serial socket=/var/lib/repose/guests/<id>/console.sock`
because Cloud Hypervisor cannot reopen a log file for rotation. hostd's
`console.Tailer` reads the socket and writes
`/var/lib/repose/guests/<id>/console.log`, rotating at 64 MB and keeping 3
files. Fluent Bit tails those files with `guest_id` taken from the path
(workstream 10 owns that config).

### 5.14 Metrics

Prefix `repose_host_`: `guests{state}` gauge, `commands_total{kind,result}`,
`command_duration_seconds{kind}` histogram, `build_queue_depth`,
`build_duration_seconds` histogram, `snapshot_bytes_total`,
`snapshot_freeze_seconds` histogram, `stream_connected` gauge,
`stream_reconnects_total`, `samples_dropped_total`, `pool_free_bytes`,
`store_bytes`, `guestd_unreachable{guest_id}` gauge. Naming rules are
workstream 10's.

## 6. Failure modes

| Failure | What hostd does | What the operator or user sees |
|---|---|---|
| Join token already used | exit status 3, unit stops retrying | journal: `register: join token already used`; infra alert on failed host registration after 5 min |
| api unreachable at start | keeps retrying the stream, serves running guests, runs nightly snapshots locally | `repose_host_stream_connected 0`; alert after 5 min |
| Thin pool over 90 percent | `host_warning{pool_high}`; CreateGuest and Restore return `insufficient_capacity: thin pool 9x% full` | api alerts operator; CLI: `host is out of disk; try again later or contact support` |
| Store over 80 percent | `host_warning{store_high}`; Build refuses with `insufficient_capacity: host store full` | same shape |
| Guest never sends Ready | after 60 s: stop CH, tear down, state `error` reason `guest did not become ready` | CLI: `guest failed to boot; console log attached` with the last 50 console lines from the api |
| virtiofsd crashes while guest runs | guest sees I/O errors on the store; hostd notices the unit exit, marks `error` reason `virtiofsd exited`, stops the guest cleanly | user: `environment crashed (store share); restarting` and the api issues StartGuest |
| CH process exits unexpectedly | unit exit watched; state `error` reason `hypervisor exited <code>`; tap and tc torn down | api restarts once automatically, then leaves in error with an event |
| Freeze without Thaw (hostd crashed between) | guestd thaws itself after 10 s | `Warning{freeze_timeout}` event, snapshot marked failed |
| Blob upload fails | LVM snapshot removed, Result `internal: snapshot upload failed: <err>`, retried by the api's timer once | alert if a running project has no snapshot older than 36 h |
| Build exceeds time | `build_timeout` with last derivation | CLI prints it and exit code 10 |
| Command for unknown guest | `not_found` | api reconciles |
| bbolt corrupt | hostd refuses to start, logs the path | operator restores from the api's view with `hostd reconcile --from-api` which rebuilds bbolt from the Hello response and disk facts (`lvs`, units, taps) |
| Two hostd instances (operator error) | flock on state.db, second exits `hostd already running` | journal |

## 7. Testing

- Unit tests for the state machine (table-driven over every transition and
  every failure step), the idempotency store, IP allocation, nft rule
  rendering (golden files), tc command rendering, error mapping from Nix
  stderr (fixtures with real Nix error output).
- Integration test against `internal/fakes/guestd` over a Unix socket for
  the Ready wait, WriteSecrets, Freeze and Thaw watchdog, Sample merging.
- Host tests, run only on a real host (`go test -tags host ./internal/hostd/
  ...` as root): create a real thin volume, a real tap on a scratch bridge,
  real nft rules in a scratch table, start a real CH guest from the
  workstream 02 test runner, wait for Ready, snapshot to a local file
  target (Blob client with a file backend for tests), restore into a second
  volume, diff filesystems, destroy, assert nothing is left (`lvs`, `ip
  link`, `nft list table repose`, `systemctl list-units 'guest@*'`).
  Output is pasted into the PR.
- Chaos: kill hostd mid-Create and mid-Snapshot, restart, assert Hello
  reconciliation leaves no half-state (the host test script does this).

## 8. Rollback

hostd is a NixOS package; rolling back is `nixos-rebuild switch
--rollback` on the host (workstream 01 documents). bbolt schema changes
carry a version key; a downgraded hostd that finds a newer schema refuses
to start rather than misread it, and `hostd state export` produces JSON that
the older version can import. Guests keep running through a hostd restart
because they are separate transient units.

## 9. Checklist

- [x] Registration with a fresh join token produces `cert.pem`, `key.pem`,
      `host.json`, deletes the token, and the host appears `ready` in the
      api. Evidence: api `hosts` row and the journal lines pasted. — closed:
      STATUS 2026-09-20 01/03-host-followup done line (host-services VM test:
      real `hostd register` against hostdev, cert.pem/key.pem written, token
      consumed once, hostdev `token_used`) and STATUS 2026-09-20
      m2-integration step-3 line (host-01 registered with the real api,
      `repose-admin hosts list` shows host-01 ready); host.json and token
      deletion also in TestRegisterThenRotate (internal/hostd/register)
- [ ] A reused join token exits with status 3 and the unit stops.
      Evidence: `systemctl status hostd` output pasted. — open: only the Go
      error is tested (TestRegisterThenRotate "join token already used"); no
      `systemctl status hostd` showing status=3 and no retry on a real or VM
      host is recorded
- [x] Certificate rotation at T-5 days swaps files atomically and the
      stream reconnects without dropping a command. Evidence: test with a
      6-day certificate and a clock override. — closed: TestRegisterThenRotate
      (internal/hostd/register: 6-day certificate, ShouldRotate false at day 0
      and true at day 2 via the clock argument, Rotate writes through
      atomicWrite and the new cert authenticates), the stream reconnect on
      request in TestSessionCommandsResultsEventsAndReconnect, and unfinished
      commands re-sent on Hello in TestOpSurvivesStreamDrop (internal/api/ops)
- [ ] Stream reconnects with backoff and replays unfinished commands after
      `kill -9` mid-command, for each of Create, Build, Snapshot. Evidence:
      chaos test output. — open: Snapshot is covered on host-01 (RESEARCH §11
      "hostd restart and kill -9" row, replay done 57 s after restart) and
      backoff by TestBackoffSchedule; no kill -9 mid-Create or mid-Build chaos
      output is recorded
- [x] Every command in `grpc-hostd.md` is implemented and returns the
      documented error codes. Evidence: a table test enumerating the enum
      of commands against the handler map, failing on a missing one. — closed:
      TestEveryCommandKindIsHandled (internal/hostd/guest/manager_test.go,
      walks the Command oneof, fails on an unhandled kind, expects 13)
- [ ] CreateGuest on a real host reaches `running` with a guest that has
      the store mounted read-only (`mount | grep store` shows `ro`), the
      right IP, a working tap, a 200 Mbit/s tc class, and an nft counter.
      Evidence: host test output. — open: STATUS 2026-09-20 m1-integration
      done line records create from the shared store and the nft tables, and
      14 §9 TestGuestCannotWriteStore covers the read-only store; no host
      output of the guest's `tc class` (200 Mbit/s) and its nft counter is
      recorded
- [x] A guest cannot reach 169.254.169.254, another guest, or the host's
      bridge address; it can reach the internet. Evidence: `curl` and `ping`
      from inside the guest, pasted. — closed: 14-security §9 boundary row
      (host-01 2026-09-21 00:37Z, `isolation-go-20260921T003759Z.txt`:
      TestGuestCannotReachIMDS curl to IMDS and the wire server refused,
      TestGuestACannotReachGuestB ping and 22 refused,
      TestGuestCannotReachHost, cache.nixos.org reachable) and STATUS
      2026-09-20 m1-integration done line (IMDS blocked, github reachable,
      guest-to-guest blocked)
- [ ] Rollback on Create failure leaves no tap, tc, nft element, or unit,
      and leaves the volume. Evidence: induced failure at each step (fault
      injection flag `--fail-at-step N` in test builds), then `lvs`, `ip
      link`, `nft list`, `systemctl` pasted. — open:
      TestCreateRollbackAtEveryStep proves it against the fakes (FailAtStep at
      every step, no leftovers, volume kept); the host run with
      `--fail-at-step N` and pasted `lvs`, `ip link`, `nft list`, `systemctl`
      is not recorded
- [ ] StopGuest with snapshot uploads a blob whose restore produces an
      identical filesystem. Evidence: `diff -r` after restore, host test. —
      open: restore round-trips on host-01 are recorded (STATUS m1-integration
      done line; conductor 2026-09-23 03:40Z line, sha256 of one marker file
      identical) and TestExtentSnapshotRoundTrip compares files on real ext4,
      but no `diff -r` of a restored filesystem on a host
- [x] A stop or destroy snapshot of a 40 GB volume holding under 2 GB
      takes under 5 s, not 33 s (I-164). Evidence: the `snapshot done`
      journal line on host-01 with `format: extents` and its
      `duration_ms`, and a restore of that blob reaching `running`. — closed:
      STATUS conductor 2026-09-23 03:40Z line (host-01 after the I-164 switch:
      40 GB volume with 50 MB, snapshot 1.3 s, restore 12 s to running) and
      hardening 2026-09-23 line (running-guest snapshots 763 ms and 1314 ms on
      host-01); the journal line itself is not pasted
- [x] Freeze window under 2 s at p99 on a guest running a `dd` write loop.
      Evidence: histogram screenshot. — closed: RESEARCH §11 freeze row
      (host-01, `dd conv=fsync` loop, 4 samples all under 0.5 s, histogram sum
      0.75 s); the numbers, not a screenshot
- [x] Build maps syntax error, missing attribute, timeout, and closure cap
      to `eval_failed`, `eval_failed`, `build_timeout`, `closure_too_large`
      with the documented messages and `fragment_line` populated for the
      first two. Evidence: fixtures in the repo and test output. — closed:
      fixtures in internal/hostd/nixbuild/testdata, TestMapEvalErrorFixtures
      (eval_failed with fragment_line), TestMapBuildErrorAndTimeouts,
      TestRealNixCanonicalCases, TestRealNixBuildTimeout,
      TestRealNixClosureCap; on host-01 RESEARCH §13 closure-cap row and
      commit bf31776 (build_timeout after 1805 s)
- [ ] Build streams `BuildLog` lines within 1 s of Nix printing them.
      Evidence: timestamps in the api's SSE test. — open: no test or record
      measures the delay from Nix printing a line to the SSE event; RESEARCH
      §13 has 40 BuildLog lines over SSE for a 5 s build without per-line
      timestamps
- [x] ApplyConfig with no kernel change switches without reboot and the
      guest's tmux session survives. Evidence: `tmux ls` before and after. —
      closed: RESEARCH §11 ApplyConfig-in-place row (host-01, tmux session
      kept, 2.2 s) and RESEARCH §13 menu row (same boot_id and tmux session
      after the apply)
- [x] ApplyConfig with a kernel change reports `reboot_required` and does
      nothing without `force_reboot`. Evidence: test. — closed:
      TestBuildAndApply (internal/hostd/guest: reboot_required, nothing
      applied without force_reboot); host-01 RESEARCH §11 (refused in 0.3 s)
      and §13 kernel sweep row
- [x] GC roots exist for every running and stopped guest and for the last
      3 revisions; `nix-collect-garbage` on the host removes nothing a
      guest uses. Evidence: run it on the test host, start every guest. —
      closed: RESEARCH §11 nix-collect-garbage row (host-01, two guests
      running, 1.1 GiB freed, rooted closures kept, guests unaffected), STATUS
      2026-09-20 m1-integration done line; last 3 revisions in
      TestSetGetRemovePrune (internal/hostd/gcroot)
- [ ] Samples every 60 s contain non-zero CPU and network deltas for a
      busy guest and `guestd_ok=false` when guestd is stopped. Evidence:
      api-side log. — open: TestSamplesMergeHostAndGuestd proves the deltas
      and guestd_ok=false against fakes; no api-side log of a busy real
      guest's non-zero CPU and network deltas and of guestd_ok=false with
      guestd stopped is recorded
- [x] Buffered samples during a 10-minute stream outage arrive after
      reconnect. Evidence: test with the fake api. — closed:
      TestSessionCommandsResultsEventsAndReconnect (internal/hostd/stream:
      samples buffered while the fake api's stream is down arrive after
      reconnect; the default buffer is 60 samples, an hour at 60 s)
- [ ] Nightly timer snapshots every running guest with the api down.
      Evidence: stop the fake api, fire the timer, blobs appear. — open: the
      host-services VM test only runs the timer's unit with no guest ("queued
      0 snapshot"); no run with the api stopped and a guest present showing
      the blobs
- [x] Drain rejects Create and Restore and keeps serving Stop, Start,
      Snapshot. Evidence: test. — closed: TestDrainRejectsPlacementsOnly
      (internal/hostd/guest); drain also exercised on host-01 (STATUS
      2026-09-20 m1-integration done line)
- [x] `/metrics` exposes every metric named in 5.14. Evidence: `curl` output
      pasted and checked against the list. — closed:
      TestDocumentedMetricsExposed (internal/hostd/metrics, checks every §5.14
      name) and host-01's hostd /metrics answering 200 with 195 repose_host_*
      series through the edge (commit 1362554)
- [ ] Console logs rotate at 64 MB and Fluent Bit picks up the new file.
      Evidence: fill with `yes` in a guest, observe in Loki. — open: STATUS
      2026-09-20 m1-integration done line says the fill did not reach 64 MB;
      TestRotation covers rotation at a small size; needs a `yes` fill past 64
      MB in a guest and the new file's lines seen in Loki
- [x] Two hostd processes cannot run at once. Evidence: second start's
      output. — closed: STATUS 2026-09-20 m1-integration done line ("second
      hostd refused", host-01) and TestSecondOpenIsLocked
      (internal/hostd/state); the second start's output is not pasted
- [x] `hostd reconcile --from-api` rebuilds a deleted bbolt to match disk
      and api. Evidence: delete state.db on the test host, run it, `hostd
      guests` matches `lvs` and units. — closed: STATUS 2026-09-20
      m1-integration done line ("reconcile after a deleted state.db", host-01;
      `--from-api` is the same code path as `--rebuild`, cmd/hostd/main.go)
      and TestRebuildFromDisk (internal/hostd/guest)
- [x] No `TODO`, `FIXME`, `panic(` outside main, or `_ = err` under
      `cmd/hostd` and `internal/hostd`. Evidence: `rg` output empty. — closed:
      upkeep 2026-09-23 at d3b72d3, `rg -n 'TODO|FIXME|_ = err\b' cmd/hostd
      internal/hostd` and `rg -n 'panic\('` outside tests both empty
- [x] `ops/RUNBOOK.md` has an entry for each row in section 6. Evidence:
      links. — closed: ops/RUNBOOK.md "hostd: join token already used" through
      "hostd: two hostd processes" (one section per §6 row; bbolt corrupt is
      "hostd: state.db corrupt or lost")
- [x] `interfaces/grpc-hostd.md` updated for the `draining` heartbeat field
      and the `reboot_required` payload flag. Evidence: the diff. — closed:
      interfaces/grpc-hostd.md heartbeat `draining` and the ApplyConfig
      `reboot_required` result field (present since e30fba4)
