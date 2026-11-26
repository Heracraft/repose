# gRPC: api ⇄ hostd

`proto/repose/hostd/v1/hostd.proto`. The **host dials the api** and opens
one long-lived bidirectional stream; the api sends commands down it and the
host sends results, samples and events up it. There is no api-to-host
connection. Transport: gRPC over TLS 1.3 with mutual auth; the host presents
the client certificate issued at registration (CN = host id). Reconnect with
exponential backoff capped at 30 seconds; the api treats a host with no
heartbeat for 90 seconds as `unreachable`.

## Registration (unary, before the stream)

```
rpc Register(RegisterRequest) returns (RegisterResponse)
RegisterRequest  { string join_token; HostInfo info }        // join token from infra, single use
RegisterResponse { string host_id; bytes client_cert; bytes client_key;
                   string guest_cidr; WireguardPeer edge; string wg_private_key;
                   string loki_url; string host_ca_pub; }
HostInfo { string hostname; string sku; uint64 mem_bytes; uint32 vcpus;
           uint32 cores; string nixos_system; string ch_version;
           uint64 pool_bytes; string arch; }
```

`repose-admin hosts add` mints the join token, and it reaches the host's
`/run/repose/join-token` **over SSH after the install**, by the `file`
provisioner in `infra/azure/modules/host`.

A token may also be minted for a user's own machine, by `POST /hosts` or
by `repose-admin hosts add --owner <handle>` (DECISIONS I-1366). Nothing
on this wire changes: the token already names one host row, and that row
now carries `owner_account_id`, so the host binds to its owner by
registering at all. `Register` and `Rotate` neither read nor report it,
`HostInfo` gains no field, and a host cannot say whose it is. What
changes is on the api's side of the row: placement puts only that
account's projects there, and the seat count and the host gauges leave
it out. Not through cloud-init:
`custom_data` stays in the Azure VM model and is readable through IMDS,
so nothing secret goes in it (DECISIONS I-20). hostd calls Register once, stores the
certificate at `/var/lib/repose/hostd/{cert,key}.pem` and rotates it with
`Rotate` (unary, same shape) every 30 days.

`cores` (field 8, I-735) is the host's physical cores, the number
placement counts committed guest vCPUs against (I-736); `vcpus` is the
SMT thread count `runtime.NumCPU()` reports and stays recorded beside
it. A hostd older than I-735 sends 0, and the api records `vcpus / 2`
instead, which is the exact core count on every shape repose can run
(DESIGN.md §4); the old shape is accepted for one release. `Rotate`
carries `HostInfo` too and records `vcpus`, `cores` and `arch` from it, each
only when non-empty/above 0, because `Register` runs once per
host: the join token is single use, so a rotate is an already-registered
host's only way to report what it now has. Not `Limits.cores`, which is
a nix build's job count.

`arch` (field 9 of `HostInfo` and field 10 of `Hello`, DECISIONS I-1448)
is the host's CPU architecture in Nix/Linux form (`x86_64`, `aarch64`),
never parsed from `nixos_system` (which holds the `/nix/store/...`
closure path from `readlink /run/current-system`). `Hello` carries it so
an already-registered host populates `hosts.arch` on its first reconnect
after upgrading hostd without re-registering. The api accepts only a
plain identifier (`[a-z0-9_]{1,32}`); a hostd older than I-1448 sends `""`
and keeps whatever `hosts.arch` holds.

`Rotate` and `Session` admit a certificate only when its serial is the
host's `cert_serial`, or the serial the last rotate replaced until the host
first opens a stream with the new one (I-432). A host in state `lost` or
`retired` is refused whatever it presents, and an open stream ends at its
next heartbeat; all of these refusals are `PermissionDenied`. `repose-admin
hosts mark-lost` and `hosts retire` clear the serials; a re-imaged machine
joins again with `hosts add --name <name> --reissue`.

`loki_url` is where that host's Fluent Bit ships journald and every guest's
console log, as `scheme://host[:port]`; it lands in `host.json` under the
same name, which `host-conventions.md` has documented since workstream 01
and nothing filled until DECISIONS I-95. It is a setting
(`repose-admin edge loki`), not a per-host value, and `Rotate` carries the
current one so a host registered before a Loki existed learns it. Empty
means the host ships nothing, which is also what an api that predates the
field sends; hostd then keeps whatever `host.json` already had.

`host_ca_pub` is the SSH Host CA's public key as one authorized_keys line
(what `repose-admin ca show` prints as `# host ca:`). It lands in
`host.json` under the same name and `repose-host-net` renders it into
`/run/repose/host_ca.pub`, sshd's `TrustedUserCAKeys`, so an operator
certificate from `repose-admin operator-cert` (principals `root`,
`operator`, 8 hours) opens the host (`host-conventions.md` "Operator
access"). Until DECISIONS I-139 nothing sent it and the file was always
empty (security review M-1). `Rotate` carries the current value too, and
hostd re-runs the renderer when a rotate changes it or `loki_url`; empty
keeps what `host.json` has, so an older api leaves a host's trust as it
was.

## Stream

```
rpc Session(stream HostMessage) returns (stream ApiMessage)

ApiMessage  = oneof { Command command; Ack ack; }
HostMessage = oneof { Hello hello; Heartbeat hb; Result result; Samples samples;
                      Event event; BuildLog log; }
```

**Hello** (first message): `host_id`, current guest states (so the api can
reconcile after a hostd restart), free memory, pool free bytes,
`pool_bytes` (field 5, I-586), the thin pool's data size, and
`pool_unavailable` (field 6, I-644): the thin pool cannot serve guest
volumes (inactive after a reboot, its VG missing, or its health `failed` or
`metadata_read_only`). While it is set both pool byte fields are 0, so an
api older than the field also places nothing there. Absent from a hostd
older than I-644, which reads as false.

`GuestStatus.ip` (field 3) and `vsock_cid` (field 4) are the address the
host allocated the guest, held in bbolt across a stop. The api records
them on the project when its row holds no address, or another one, for a
guest the host owns, and leaves them alone otherwise (DECISIONS I-720);
the host's word wins, because it allocates inside the disjoint /22 the
api handed it at registration. An empty `ip` says nothing and is
ignored. A stopped guest keeps its address on the row: the gateway routes
on the project's state, not on whether an address is recorded. An api
older than I-720 read neither field.

Resume after a host restart (DECISIONS I-645), all additive:

- `Hello.host_boot_id` (field 7) is the host's current
  `/proc/sys/kernel/random/boot_id`, and `Hello.host_boot_at` (field 8)
  when that boot began, unix seconds (`btime` of `/proc/stat`).
- `GuestStatus.interrupted_boot_id` (field 6) and `interrupted_at` (field
  7, unix seconds) mark a guest that hostd's first start in a new boot
  found `running` or `starting` with no `guest@` unit: the host restarted
  under it. hostd sends the guest as `stopped` (or `error`) with the mark
  until the guest next reaches `running`, or a `StopGuest`, `DestroyGuest`
  or `Restore` clears it, and drops a mark nothing cleared after 7 days.
  The mark lives in bbolt (host-conventions.md), so every Hello after a
  hostd or api restart carries it again.
- The api writes one resume row per (project, boot id) from these, and
  only for a guest whose project is placed on that host (I-447). If it
  cannot write the row it refuses the Hello: the stream closes with
  `UNAVAILABLE` and hostd sends Hello again when it reconnects.
- A hostd older than I-645 sends none of the three; the api then starts
  nothing on its own, which is the behaviour before. An api older than
  I-645 ignores them.

The idle pause (DECISIONS I-818), additive:

- `GuestStatus.resting_at` (field 8, unix seconds) is when hostd paused
  the guest: its vCPUs are stopped and its memory stays resident on the
  host, so every process, socket and open file survives. The guest's
  `state` stays `running` -- resting is a **marker on a running guest,
  never an eleventh state**. hostd settles the marker against the
  hypervisor's `vm.info` at its start, so every Hello after a hostd or an
  api restart carries it again, and the api wakes such a guest before
  anything that needs the machine. 0 means not resting, which is also
  what a hostd older than I-818 sends.

Burst cores (DECISIONS I-1126, I-1128), additive:

- `GuestStatus.burst_denied` (field 9, bool) is the api's standing
  withdrawal of this guest's burst: the account used the period's burst
  core-hours and overage is off, so the guest runs on the base cores its
  class sells until the period ends. hostd keeps it in bbolt and sends it
  in every Hello, so an api restart reconciles instead of resending.
  False is "may burst", which is also what a hostd older than I-1126
  sends.

`Hello.features` (field 9, DECISIONS I-701) lists what this hostd takes
beyond the commands every hostd takes: `local_clone`, a `Restore` with
`clone`; `pause` (I-818), `PauseGuest` and `WakeGuest`; `burst` (I-1126),
`SetBurst`, and with it a host that boots guests at their class ceiling
and runs the `cpu.max` controller; `nix` and `erofs` (I-1458), which
guests the host can run, below. The api keeps it
in `hosts.features` and sends a shape only to a host that names it, so a
host without `pause` is never asked to rest a machine and a host without
`burst` is never sent a `SetBurst`. A hostd older than
I-701 sends none.

### erofs guests (DECISIONS I-1458)

An **erofs guest** boots an Ubuntu base image instead of a Nix closure:
a read-only `rootfs.erofs` as `vda`, the guest's thin volume as `vdb`, and
a kernel and initramfs from the same release. A **Nix guest** is every
guest before this. A project's `guest_runtime` (`nix|erofs`,
`db-schema.md`) says which it is, for life.

```
BaseImage { string version; string url; string sha256; string kernel_version;
            string initramfs_url; string initramfs_sha256;
            string kernel_url; string kernel_sha256; }
MenuItem  { string id; map<string,string> options; string package; }
MenuSpec  { repeated MenuItem items; string revision_id; }
```

- **Three files, each checked.** `url` is `rootfs.erofs`, `kernel_url` the
  kernel (a plain bzImage on `x86_64`, an uncompressed `Image` on
  `aarch64`), `initramfs_url` the initramfs. Each `*sha256` is the
  lower-case hex SHA-256 of that file. hostd fetches each file on its own,
  resumes or refetches one without the others, and boots nothing whose
  digest differs (`internal`, "base image checksum mismatch"). `version`
  is the `base_versions` label (`[A-Za-z0-9._-]{1,64}`) and names hostd's
  cache directory; `kernel_version` is the kernel's `uname -r`, for logs
  and the dashboard. The api picks the files for the host's `arch` from
  the base row (`image_amd64_*` for `x86_64`, `image_arm64_*` for
  `aarch64`); hostd never chooses a file.
- **Which guest a command is for.** `CreateGuest` (field 18), `StartGuest`
  (field 12) and `Restore` (field 21) carry `base_image`. With
  `base_image` set and `system_closure` empty the command is for an erofs
  guest; with `system_closure` set and `base_image` absent it is for a Nix
  guest, exactly as before; **both set is `invalid_argument`**. A
  `StartGuest` with neither keeps what the guest last booted, as an
  old-shape `StartGuest` always has. A `StartGuest` whose `base_image`
  names another version than the recorded one boots the new base; the
  initramfs rotates the overlay's upper directory and keeps `/home/dev`,
  `/var/lib/docker` and `/var/lib/repose/services` (proposal §7.3).
  `system_closure` on all three is **accepted for one release** in its old
  meaning; it is retired with the last Nix guest.
- **The menu.** `MenuSpec` is the project's whole `config_revisions.menu`
  selection in the user's order (`api.md` MenuSelection): each item
  exactly one of `id` (a catalog entry, with its `options`) or `package`
  (a Ubuntu package name). It is a state, not a diff. `CreateGuest` (field
  19) and `StartGuest` (field 13) carry it, and hostd hands it to guestd's
  `ApplyMenu` with `replay_after_base_upgrade` set once the guest has run
  `SetupProject`, so the first boot after a base bump installs again what
  the rotated upper directory dropped. `ApplyConfig` (field 4) carries it
  for a menu change on a running erofs guest, with
  `replay_after_base_upgrade` false. A `menu` beside `system_closure` is
  ignored on `CreateGuest` and `StartGuest`, and `invalid_argument` on
  `ApplyConfig`, which takes exactly one of the two.
- **Progress and failure.** guestd's progress lines reach the api as
  `BuildLog` under the command's `command_id`, as a Nix build's do (the
  api binds an `apply_config` command to its op as it binds a build's,
  I-1536), and
  the CLI shows them through the op's log (`api.md`). A menu that fails on
  `ApplyConfig` answers `menu_failed` (an item did not install or start;
  `message` is guestd's last line) or `invalid_argument` (an unknown
  catalog id or option, a bad package name); the guest keeps running with
  what it had. A menu that fails at a boot does **not** fail the boot:
  `CreateResult.menu_error` (field 4) or `StartResult.menu_error` (field 8)
  carries the code and line, and the guest runs.
- **What the host reports.** `GuestStatus.base_version` (field 10) is the
  base an erofs guest boots, from hostd's record; `StartResult.booted_base_version`
  (field 7) the one a start booted, which is what clears the project's
  `pending_base_version`. Both are empty for a Nix guest and from a hostd
  older than I-1458.
- **Which host takes which guest.** `Hello.features` names `nix` when the
  host builds and boots Nix guests and `erofs` when it boots erofs guests;
  a host may name both. A hostd older than I-1458 names neither and is
  read as `nix` alone. The api places an erofs project only on a host that
  names `erofs` and whose `hosts.arch` has an image in the base row, and a
  Nix project only on a host that names `nix` or names neither. A host
  that names `erofs` and not `nix` is never sent `Build`.
- **Refusals.** Each is `invalid_argument`, decided before hostd touches
  anything: `base_image` beside `system_closure`; a `base_image` on a host
  that does not name `erofs`; a `base_image` whose `version` is not a label, or
  whose three files are not each a non-empty URL with a 64-hex-digit
  digest; an `ApplyConfig` with both `system_closure` and `menu`, or with
  the one the guest's runtime does not take. A `system_closure` create,
  start or restore, or a `Build`, on a host running with Nix off (one
  that does not name `nix`) is **`nix_unsupported`** instead: a
  precondition of the host, which the api fails the op on and never
  retries, since no resend to that host can succeed.
- **Snapshots.** `StopResult.base_version` (field 5),
  `SnapshotResult.base_version` (field 5) and `SnapshotDone.base_version`
  (field 6) are the erofs base the guest ran when the snapshot was taken;
  the api stores them as `snapshots.base_version`. hostd may send them
  empty for one release, and the api then records the project's
  `base_version`. Empty for a Nix guest. A `Restore` pins the base it
  boots with its `base_image`; nothing else is needed on the request.
- **What an erofs guest does not take.** `Build` is never sent for one.
  hostd never registers store paths in it, so guestd's `RegisterPaths` and
  `Switch` answer `unimplemented` there (`vsock-guestd.md`).

**Heartbeat** every 15 seconds: `free_mem_bytes`, `pool_free_bytes`,
`load1`, `running_guests`, `draining` (bool; set after `Drain`, the scheduler
places nothing on a draining host), `pool_bytes` (field 6, I-586) and
`pool_unavailable` (field 7, I-644, as in Hello: the api keeps
`hosts.pool_unavailable` from it and places nothing on such a host). The
api keeps `hosts.pool_bytes` from the newest non-zero `pool_bytes`, since
autoextend grows the pool after registration, and places a new project
only where the pool stays at most 70 percent used with the new volume's
first bytes in it. A hostd older than I-586 sends 0, which keeps the
registered size, and on a host with no size the api places by the whole
volume free, as before. A Heartbeat also goes right before
every Result, so `free_mem_bytes` already counts a guest the command
stopped, destroyed or created when the api places the next one
(DECISIONS I-409).

**A host its user owns** (DECISIONS I-1626, I-1628, I-1640). hostd runs with
`--owned-host` on the Mac appliance and on an x86 machine set up by
`ops/install-host-x86.sh`; the installer writes it, the api does not send
it. No field changes shape; what some mean there:

- Hello `features` names `shared` (only with `--owned-host`): the api
  places on such a host by its reported free memory, not by class
  (the api's owned placement). An owned host whose hostd does not name it predates I-1626
  and keeps the class arithmetic.

- `class` on CreateGuest, StartGuest and Restore is still validated and
  recorded, and reported back in samples, but sizes nothing: every guest
  boots with the host's placeable memory (MemTotal less the owned reserve
  less the hypervisor's 512 MiB overhead) and, on bare metal, every vCPU
  the host has; when hostd itself runs under a hypervisor (the Mac
  appliance, an x86 VM) two vCPUs (I-1640); `--owned-vcpus N` in
  hostd.env overrides both, never above the host's. No class disk caps,
  `CPUWeight=100` per guest, no per-guest `CPUQuota`, burst off;
  `guests.slice` has `CPUWeight=50` and no `CPUQuota` (I-1640: a quota
  throttled the nested guests, which hung), and under a hypervisor
  `MemorySwapMax=0`: guest RAM is never swapped there (I-1656). `volume_bytes` is the volume's
  size as on the fleet.
- `free_mem_bytes` (Hello, Heartbeat) is the host's real free memory:
  `max(0, min(MemAvailable, MemTotal - reserve - guests.slice
  memory.current))`, not total less the reserve less class reservations.
  A create, restore or start is refused `insufficient_capacity` when it
  is under 1 GiB, with a message that holds "not enough free memory"
  (the api's own threshold is the same number).
  Under a hypervisor, also when the guests not stopped, failed or
  destroyed and the new one would boot with more vCPUs together than the
  host's less two (`--owned-max-vcpus N` replaces that bound), with a
  message that holds "not enough free CPU" (I-1640).
- hostd stops the newest running guest itself when the host's free memory
  (`free_mem_bytes` above) stays under 512 MiB for 5 s, and reports it as
  the OOM killer's below (I-1650): on the Mac appliance the kernel neither
  swapped nor OOM-killed at the limit, and every guest hung. Guest RAM
  keeps Cloud Hypervisor's 2 MiB pages; `--owned-guest-thp=false` in
  hostd.env boots it as 4 KiB shmem (`thp=off`), which reclaim can swap
  but which made the Mac appliance's guests unusably slow.
- A guest the kernel's OOM killer ended (systemd's `Result=oom-kill` on
  `guest@<id>`, seen by the monitor or by the reconcile at hostd's start)
  is torn down and sent as `guest_state_changed {state: stopped, reason:
  oom_killed}`. Its volume is intact and a StartGuest brings it back. On
  the fleet such a guest stays `error`, `hypervisor exited <status>`.

**Command** carries `command_id` (UUIDv7, idempotency key; a repeated
command_id returns the stored result) and one of:

| Command | Fields | Result payload |
|---|---|---|
| `CreateGuest` | project_id, guest_id, class, volume_bytes, system_closure (store path of the built system; or base_image and menu for an erofs guest, I-1458, "erofs guests"), secrets (list of name → bytes, delivered to tmpfs), env (TZ, LANG), ssh_ca_pub, principals (list), hooks_config, host_key (guest sshd ed25519 private key, generated by the api), host_cert (its certificate signed by the Host CA with principals guest_ip and `<slug>.<handle>`), user_id, project_slug, remote_url, project_json (I-26) | guest_ip, vsock_cid, boot (I-707: `BootTimings {volume_ms, prepare_ms, ready_ms, paths_ms, setup_ms, total_ms}`, where the boot's time went; absent from a hostd before it, and an api that ignores it reads the result as before) |
| `StartGuest` | guest_id, base_image and menu (I-1458, erofs guests; "erofs guests"), plus optionally the same delivery fields as CreateGuest: secrets, env, ssh_ca_pub, principals, hooks_config, host_key, host_cert, project_json (I-26: hostd keeps none of them on disk, so the api sends them on every start; a StartGuest with only guest_id is accepted while hostd still has them in memory), and class (I-260: the project's class; when it differs from the recorded one, hostd boots at the new class and records it; empty keeps the recorded class, so an api that does not send it is still accepted) | `StartResult {booted_closure, failed_closure, boot_error}` (I-590; field 16 of the payload, absent from a hostd before it): the closure the guest runs; after a boot that never reached Ready and fell back to the guest's last good closure, `failed_closure` is the one that failed and `boot_error` why (`code`, hostd's fixed `message`, `console_tail`). An api that ignores it reads ok as before. `boot` (I-707) is the BootTimings of the boot that reached running, as on CreateResult. `guest_ip` (field 5) and `vsock_cid` (field 6) are the started guest's address, as on CreateResult (I-720): a start after a create or restore that failed past the guest's own boot is what gives the api an address it never received. Empty and 0 from a hostd before I-720, which the api reads as "unchanged"; an api before it ignores them |
| `StopGuest` | guest_id, snapshot_first (bool), timeout_s | snapshot_id if taken (assigned by the api), blob_path, bytes (I-26), sha256 (I-462), snapshot_error (field 6, I-1636: the guest stopped and its snapshot could not be written, `<code>: <message>`; the stop is ok, there is no blob, and the api keeps the project `stopped` with `last_error` `snapshot_failed: ...` and a `snapshot_failed` notification. A hostd before it failed the whole stop instead, which an api of either age reads as a failed stop) |
| `DeleteSnapshot` | blob_path | (none) (DECISIONS I-1636). Removes one snapshot this host keeps on its own disk (`--snapshot-dir`, an owned host), with its `.meta` and any `.part`; a snapshot already gone is ok. Sent by the api's daily retention for each expired row whose host is an owned host still registered, outside any op, through `hostmgr.Send`: a host with no stream keeps the row for the next run. Error codes: `invalid_argument` (a path with `..` or a leading `/`, or a hostd whose store is Blob), `internal` (the delete failed). A hostd before it answers `invalid_argument` (unknown command) and keeps the file |
| `DestroyGuest` | guest_id, keep_volume (bool) | |
| `ResizeVolume` | guest_id, new_bytes | none; refused with `insufficient_capacity` while the thin pool is 85 percent used or more, data or metadata (as `CreateGuest` and `Restore` are; DECISIONS I-586, which replaced I-449's bound on the sum of the volumes' sizes). `StartGuest` is refused the same way at 95 percent. A running guest's filesystem grows at once (`GrowFs`); a stopped guest's volume only grows, and hostd asks guestd for `GrowFs` once each boot reaches running, which does nothing on a filesystem that already fills its volume (I-1660) |
| `Build` | (Nix guests only; never sent for an erofs project, I-1458) project_id, revision_id, fragment (bytes), base_ref (git rev of nix/ in the platform repo), limits {eval_s, build_s, cores, closure_bytes}, base_version (the `base_versions` label the closure is stamped with, `[A-Za-z0-9._-]{1,64}`; optional, I-118: empty keeps the flake's own stamp), personal (bytes, field 7: the account's machine.nix written as `personal.nix` beside the fragment; optional, I-490: empty is the build without it, and a hostd that predates the field ignores it) | system_closure, closure_bytes, kernel_changed (bool) |
| `ApplyConfig` | guest_id, system_closure, force_reboot (bool); or, for an erofs guest, menu (I-1458: exactly one of the two; progress as `BuildLog`, failure `menu_failed`; "erofs guests") | rebooted (bool), reboot_required (bool: the closure changes kernel or initrd and force_reboot was false; nothing was applied). On a stopped guest hostd moves the root after checking the closure's kernel, initrd, init and requisites, else `not_found` (I-591). A Switch error guestd codes `not_found`, `invalid_argument` or `store_path_hidden` keeps its code; any other is `internal` (I-593). `store_path_hidden` (I-589, added with it; a guestd before it answers `not_found`) is guestd refusing a closure, or a path of it, that an overlayfs whiteout in the guest's own store hides; the guest keeps its system, and a start removes the whiteouts (I-587). A forced reboot that never reaches Ready falls back to the guest's last good closure and answers `boot_failed`, the guest running (I-590) |
| `Snapshot` | guest_id, reason (`scheduled|stop|manual`) | snapshot_id, blob_path, bytes, sha256 (I-462: hex SHA-256 of the bytes uploaded; hostd checks the store holds `bytes` before answering) |
| `Restore` | project_id, guest_id (new), blob_path or clone (I-701; see below), class, volume_bytes, system_closure (I-26; empty means the api rebuilds before StartGuest) or base_image (I-1458, an erofs guest; both is `invalid_argument`), sha256 (I-462; see below), plus every CreateGuest field (secrets, env, ssh_ca_pub, principals, hooks_config, host_key, host_cert, user_id, project_slug, remote_url, project_json) | as CreateGuest; the guest ends `stopped`. A failure removes the new volume (I-461) |
| `UpdateSecrets` | guest_id, secrets | none. `secrets` is the guest's whole named set; an empty list removes every named secret. The sshd material from the last create, start or restore stays (I-732) |
| `SetPrincipals` | guest_id, principals | |
| `Exec` | guest_id, argv, timeout_s, audit_id (required, I-26; operator use only, audited: hostd logs `{audit_id, guest_id, argv}` at notice) | exit_code, stdout, stderr (each capped 64 KB) |
| `Drain` | (host) | |
| `AnswerQuestion` | guest_id, question_id, status (`answered\|cancelled\|expired\|no_channel\|extended`), answer, expires_unix (field 5, I-1242) | none. Relayed to guestd's request of the same name (DECISIONS I-244). `not_found` when the guest is not running or guestd does not know the question, which the api takes as final; any other error is retried by the api with a new command_id. Status `extended` (DECISIONS I-1242) closes nothing: the owner is answering on the dashboard, and `expires_unix` (unix seconds) is the question's new deadline, which only moves it later. hostd wakes a resting guest for it as for an answer. A guestd from before the status answers `invalid_argument`, which hostd passes on and the api logs; the api sends a hold once and never retries it. The answer is tenant text and is never logged |
| `SayToAgent` | guest_id, say_id, window, text, sender, from_owner | `SayResult {state, window, replaced}` (DECISIONS I-1144). Relayed to guestd's request of the same name, which types the message into the agent's own tmux window. `state` is `delivered` (it is in the terminal now) or `waiting` (the agent is mid-turn and it goes in at the end of the turn); `window` is the window guestd resolved it to; `replaced` is true when a message was still waiting there. `window` empty means the guest's single agent window. hostd wakes a resting guest first, as it does for `AnswerQuestion`. `not_found`: the guest is not running, or no agent is in that window. `invalid_argument`: no text, a text over 4 KB, more than one agent and no window named (the message names them), or an agent under herdr, which this does not reach. The text is tenant content and is never logged |
| `SetTriggers` | guest_id, triggers (the project's whole set: name, kind, prompt_path, workdir, agent, timeout_s, concurrency, gate, dedupe, enabled) | none (DECISIONS I-1248, I-1250). The set a guest's receiver and runner work from, sent at create, start, restore and on every change, as `UpdateSecrets` sends the whole named set. An empty list removes every trigger. **A state the api owns, not an event**: hostd forwards it and keeps none of it, and the api re-sends it on every start, so a guest that lost it comes back with it. The prompt's text is not in it: a trigger carries a path (I-1248) |
| `RunTrigger` | guest_id, trigger, run_id, source (`clock\|hand`), payload_run (optional: replay the payload of an earlier run), payload_path (optional: the file `fire --payload` wrote, always under the trigger's own folder), dry_run (bool) | `TriggerRunResult {state, window, reason}` (I-1252, I-1256). Relayed to guestd's request of the same name. `state` is `working` (the window is open and the agent has its prompt), `skipped` (admission refused it; `reason` is the bounded enum) or `failed` (the prompt file is gone, no agent is installed). The api sends it with the run's id as the `command_id`, so a re-send after a reconnect returns the stored result and never starts a second agent. `not_found` when the guest does not know the trigger, which is an api that has not re-sent `SetTriggers` since a restart |
| `WriteTriggerPrompt` | guest_id, trigger, workdir, prompt_path, text (bytes, at most 64 KB) | none (DECISIONS I-1266). Relayed to guestd's request of the same name, which writes the file as `dev` under the trigger's working directory. `repose triggers add -p` writes that file over SSH; the dashboard has none, so the api carries the text and stores none of it. The text is the user's prompt and the trigger's name is the user's word, so neither is a log field: the line carries the size. `not_found` when the guest is not running, which the api answers by leaving the prompt on its hand-off row for the next pass |
| `PauseGuest` | guest_id, reason (lower_snake, `idle\|operator`) | `PauseResult {paused, why_not, at}` (DECISIONS I-818; only to a host whose Hello names `pause`). Stops the guest's vCPUs and leaves its memory resident: the hypervisor, virtiofsd, the tap and vsock stay up and nothing is written anywhere. Before it pauses, hostd asks the guest to `sync` (and to drop its page cache when the guest has the balloon) and reads the next elapse of a systemd timer inside it, in the system manager and in `dev`'s. `paused=false` with a `why_not` is a **refusal, not a failure**, and is answered `ok`: `not_running`, `already_resting`, `command_in_flight` (the api has a command for this guest in flight or queued), `host_stopping`, `parked` (the guest has a park record; `frozen` until I-1002, and the api only logs this word, so both read the same to it), `timer_due` (a user timer elapses within 15 minutes), `in_use` (a session or tmux client appeared between the api's newest sample and this pause) or `guest_unreachable`. Every refusal is decided before hostd does anything to the guest, so a guest refused at every tick is never asked to sync or drop its cache. `at` is when it paused, unix seconds. Error codes: `not_found`, `invalid_argument`, `internal` (the hypervisor refused `vm.pause`; the guest keeps running) |
| `SetBurst` | guest_id, allowed (bool) | no payload (DECISIONS I-1128; only to a host whose Hello names `burst`). Says whether this guest may use cores above the base its class sells. It is a **state the api owns, not an event**: the api sends `allowed=false` when the account has used the period's burst core-hours with overage off, and `allowed=true` at the next period. The same value twice changes nothing, so a resend is harmless, and a guest hostd has heard nothing about may burst. `allowed=false` puts the guest at its base in that call, not at the next controller tick. hostd stores it and reports it as `GuestStatus.burst_denied`. Error codes: `not_found`, `invalid_argument` (no guest_id, or this host does not burst) |
| `WakeGuest` | guest_id, reason (lower_snake) | `WakeResult {was_resting, wake_ms}` (DECISIONS I-818). Resumes a resting guest, sets its clock to the host's and waits for guestd to answer. **Idempotent**: a guest with no resting marker is answered `ok` with `was_resting=false`, whatever its state, so the api may ask for a wake it does not know it needs. `wake_ms` is from `vm.resume` to guestd's answer. A wake takes no op slot and runs on the guest's own queue, so it never waits behind the host's builds. Error codes: `not_found`, `invalid_argument` (the guest is not running), `guest_unresponsive` (the hypervisor did not resume, or the guest did not answer after it: the one part of this feature a user sees fail, and hostd raises `guest_wake_failed` with it) |

**Result**: `command_id`, `ok`, `error {code, message}`, payload. Snapshot
blob paths are `<user_id>/<project_id>/<ts>.img.zst` (`<project_id>/<ts>`
when the guest was created without `user_id`). The blob is a zstd stream
of either a raw volume image or, since DECISIONS I-164, the extent format
(after decompression: `RPSXT001`, the device size, then `offset, length,
bytes` records of the used non-zero blocks, then a trailer; little-endian
u64s). `Restore` accepts both and tells them apart by the magic, so every
existing snapshot stays restorable; a hostd older than I-164 cannot
restore an extent blob (its e2fsck fails the restore, nothing is
overwritten but the new volume), so every host is switched before a
snapshot moves between hosts.

`sha256` (added with DECISIONS I-462) is the lower-case hex SHA-256 of
the stored blob, as hostd computed it while uploading. The api records it
from `SnapshotResult`, `StopResult` or the `SnapshotDone` event and sends
it in `Restore`. With it set, hostd downloads the blob once, at the
version it found (the ETag), hashing it as it writes the new volume, with
the decompressor in a sandbox with no device (I-700, which replaced
I-462's download to hash before the write). When the digest differs it
fails with `internal` "snapshot checksum mismatch", removes the volume
and leaves the guest `error`, so nothing starts it. Empty means
unchecked: a snapshot from before I-462, or an api older than it. A
value that is not 64 hex digits is `invalid_argument`.

`clone` (field 19, I-701) is **no longer sent**: `repose fork` was its
only caller and is gone (I-1082). hostd still accepts it for one
release, so a host that has not been switched yet keeps working with an
api that predates the removal; the field and `local_clone` become
`reserved` in the release after v0.1.45, and the description below is
what hostd still does with a clone that arrives.

`clone` is set instead of `blob_path` for a copy on its source's host,
only to a host whose Hello names `local_clone`:
`{source_guest_id, point_id, sibling_guest_ids}`. The first copy to
arrive freezes the source on its own worker, snapshots its volume
into `fp-<point_id>` and thaws; every copy (`sibling_guest_ids`, this one
included) becomes a thin clone of that point, grown to `volume_bytes`
when the source's is smaller (smaller is `invalid_argument`), with no
e2fsck. A source not on the host is `not_found`; `point_id` not 32 hex
digits, a `sibling_guest_ids` without the guest, or both `blob_path` and
`clone`, is `invalid_argument`. The point is removed once every sibling
has finished its restore, after 30 minutes, or at hostd's next start; a
copy whose Restore first arrives after that takes a new point, a later
moment than its siblings.
`restore_done` then logs `source: clone`, `point_ms` and `clone_ms`
(`source: blob` and `verify_ms` 0 otherwise). A Restore sent again for a
copy already `stopped` answers as the first did. Error codes:
`invalid_argument`, `not_found`, `already_exists`, `insufficient_capacity`,
`build_failed`, `build_timeout`, `eval_failed`, `closure_too_large`,
`guest_unresponsive`, `boot_failed`, `store_path_hidden`, `volume_unavailable`, `menu_failed`, `nix_unsupported`, `internal`. `store_path_hidden` (I-589) is ApplyConfig only; see its row. `menu_failed` (I-1458) is an erofs guest's menu that did not apply on `ApplyConfig`, and `nix_unsupported` (I-1458) a Nix command to a host running with Nix off; see "erofs guests".
`volume_unavailable` (I-644; an api older than it shows it as it is) is any
command that needs the thin pool while it is unavailable (as Hello's
`pool_unavailable`), and a `StartGuest` whose `g-` or `auth-` volume did
not activate on hostd's one try. A start answers it before anything
boots, so the guest stays `stopped` and never falls back (I-590); the
api leaves the project `stopped`. `CreateGuest`, `Restore`,
`ResizeVolume`, `Snapshot` and `DestroyGuest` answer it too; a destroy
then keeps the volume. `boot_failed` (I-592) is
a boot that never reached Ready whose console shows it stopped before
the system started (stage 2 missing, a kernel panic, a failed fsck,
stage 1's error); `message` is a fixed sentence, and `console_tail`
(`Error` field 5) the end of what that boot printed, at most 200 lines
and 16 KiB, tenant output that is never logged. A boot whose console
shows the system started, or nothing hostd recognises, stays
`guest_unresponsive`, also with `console_tail` -- including a start whose
guestd answered and which then ran out of time registering the closure's
paths or setting the project up, which carried none until I-1408 and so
left `repose logs --kind console` nothing to print for the commonest
failure on an owned Mac. That message names the phase that ran out and
the deadline the host gave it, after the `RegisterPaths:` or
`SetupProject:` wording an api before I-1408 matches on. An api older
than I-592 shows a `boot_failed` message as it is. `build_failed` and `eval_failed` carry the
Nix error text verbatim in `message` (capped 32 KB) plus `fragment_line` when
parseable, or `personal_line` (field 4, I-490) when the location is in the
personal layer.

**BuildLog**: `command_id`, `seq`, `line`. Streamed during `Build`; the api
forwards to the CLI over SSE.

**Samples** every 60 seconds, one message per host, and one more each time
a guest's tap is torn down (stop, destroy, a failed boot, an unexpected
exit): that guest alone, state `stopping`, with the rx and tx bytes since
its last sample (DECISIONS I-448). Every delta counts from the guest's
boot; a guest's first sample after a start is no longer a zero baseline.
`ts` is never at or before the previous message's, so two messages never
share a (guest, second). Before I-448 destroy sent such a sample from the
nft counter with state `destroying`; the api treats both the same (only
`running` samples count as hours).

```
Samples { int64 ts; repeated GuestSample guests; HostSample host; }
GuestSample { string guest_id; string state; string class; uint64 cpu_ns_delta;
              uint64 mem_rss_bytes; uint64 net_tx_bytes_delta; uint64 net_rx_bytes_delta;
              uint64 disk_alloc_bytes; uint64 disk_used_bytes;
              GuestSignals signals; repeated ProcSample procs;
              uint64 cpu_pressure_us_delta; uint64 host_cpu_wait_us_delta;
              uint64 guest_mem_used_bytes;     // the three before: I-493
              uint64 root_used_bytes; uint64 root_size_bytes;      // I-567
              bool resting;                                        // I-818
              optional uint64 net_egress_bytes_delta;              // I-1063
              optional uint64 burst_cpu_ns_delta;                  // I-1126
              uint32 burst_cap_millicores;                         // I-1126
              int64 parked_waiting_since; }                        // I-1672
GuestSignals { uint32 ssh_sessions; uint32 tmux_clients; repeated AgentProc agents;
               uint32 docker_containers; bool guestd_ok;
               uint64 last_input_unix; }                             // I-1272
AgentProc { string agent; string tmux_window; string state;      // state: working|idle|needs_input|running|unknown (running: I-694)
            string agent_id; string terminal; }                   // I-1676
// tmux_window: a tmux window name or a herdr agent key, at most 64 bytes (I-504); empty
// for an agent outside any terminal repose can see (Claude Desktop's session, I-1676)
// agent_id (I-1676): opaque, "a" and 16 hex digits, guestd's keyed hash of the agent's
// process (never a pid), or, where guestd knows no process (a herdr pane), the terminal;
// terminal is "tmux:<window>", "herdr:<key>" or empty. hostd drops either in any other
// form and keeps them as one line of at most 72 bytes. A guestd older than I-1676
// sends neither, and the api reads such an agent as agent_id = terminal = "tmux:<window>"
// for one release (a herdr key from such a guestd reads as a tmux window).
// tmux_clients: tmux attach clients only; 0 on a herdr machine, whose clients count in ssh_sessions (I-511)
// last_input_unix (I-1272): the newest `client_activity` of a tmux client of the project's
// session, which tmux moves on when a client sends input: when somebody last typed in the
// machine. It is a time and never what was typed. guestd reports when the watcher started
// while nobody has typed yet, so 0 means only "no answer" -- tmux has never been readable,
// or this is a guestd from before I-1272 -- and the api's notification hold then reads the
// two session counts instead (I-689's rule). It only ever moves forward: tmux forgets a
// client the moment it detaches, and nobody attached now is not nobody typing a moment ago.
ProcSample { string comm; uint64 cpu_ns_delta; uint64 rss_bytes; }  // comm only; never argv
// resting (I-818): this guest is paused. `state` stays `running`, which is what bills
// the minute; `signals` repeats the last figures the guest wrote and `guestd_ok` stays
// TRUE, because guestd is paused with its guest and not lost; `procs` and the pressure
// delta are empty, and cpu_ns_delta and the net deltas are the host's own counters and
// read about zero. False from a hostd older than I-818.
// burst_cpu_ns_delta (I-1126): nanoseconds this guest used above the base share its
// class sells, since the last sample, summed per second by the host's controller --
// not from the minute's average, which undercounts a guest that bursts in spurts.
// hostd bounds it at (ceiling - base) x 66 s and the api clamps it again. Absent from
// a hostd older than I-1126 and from every host without the `burst` feature, which
// billing reads as no burst. burst_cap_millicores is the cap the controller has the
// guest at now, in thousandths of a core; 0 on a host that runs no controller.
HostSample { uint64 mem_free; uint64 pool_free; double load1; uint32 builds_running; }
```

**Event** (host-originated, at most once each, api acks by `event_id`):
`guest_state_changed {guest_id, state, reason}`, `agent_event {guest_id,
agent, kind (completed|needs_input|error|agent_message|agent_done|agent_prompted;
agent_done from `repose-notify --done`, I-688; agent_prompted is a
`repose say` that reached the agent's terminal, whose summary is the
sender's name and never the message, I-1144), summary (capped 1 KB),
tmux_window (the window guestd resolved for the hook, empty when unknown;
I-121; a tmux window name or a herdr agent key since I-504), link_url and
link_label (fields 6 and 7, I-931: a `repose-notify --link`, which hostd
checks with internal/msglink and drops, keeping the event, when it fails),
agent_id (field 8, I-1676: AgentProc.agent_id of the agent the hook came
from, set by a guestd with the agent registry for an agent outside any
terminal; empty otherwise and from an older guestd; the api does not
store it yet)}`,
`agent_question {guest_id, question_id, agent, tmux_window, text (capped 1
KB), options, timeout_s, state, reason}` (a guest's `Question` notify, relayed;
DECISIONS I-244: the api stores it by question_id, so a re-announcement
after a hostd restart is a no-op, and `state` cancelled|expired closes it;
the text is tenant content and is never logged; `reason` (field 9, I-690)
says why the guest closed it: `terminal` (answered in the agent's terminal,
stored as `answered` with `answered_via` `terminal`), `withdrawn` (the
agent took it back, stored as `cancelled` with `answered_via` `agent`),
`interrupted` or `asker_gone` (stored as a plain cancel); empty for an
expiry and on an open question; an older hostd drops it and the close is a
plain cancel; `secret` (field 10, I-929) is the named secret a `repose-ask
--secret` asks for, `[A-Z][A-Z0-9_]{0,63}`, with no options beside it;
hostd drops a question whose name breaks that rule or that has both;
`link_url` and `link_label` (fields 11 and 12, I-931) are the ask's
`--link`, checked as on `agent_event`),
`guest_resting {guest_id, resting, at, reason}` (DECISIONS I-818: the guest
was paused or woken, so the api shows the marker within seconds rather than
at the next minute's sample. It is **not** a `guest_state_changed`: the
guest's state does not change, and the api must not route it through its
state-change path, which cancels the project's open questions),
`snapshot_done {guest_id, blob_path, bytes, sha256}` (sha256 added with I-462), `host_warning {kind, detail}` with kinds `pool_high` (70
percent), `store_high` (80 percent), `build_queue_deep`, `cache_unreachable`
(substituter down; builds fall back to source and will be slow),
`guestd_lost` (no vsock for 60 s), `freeze_timeout`, `guest_wake_failed`
(I-818: a resting guest did not come back; its processes are still there
and still paused, and the runbook's wake is the operator's step), and the guest kinds
hostd relays from guestd's `Warning` (`disk_high`, `inotify_exhausted`,
`docker_down`, `store_path_missing`, `oom`, `tmux_down`, `herdr_down`
(I-507; hostd and the api take it in the release before any base sends
it, and an older hostd sends it as `guest_other`); any other guest kind is
sent as `guest_other`); `operator_login
{pam_type, user_present, key_id, serial, key_fingerprint}` for every SSH
login to the host, from the PAM hook that runs `hostd audit-login`
(DECISIONS I-140): the certificate's key id and serial, or a plain key's
SHA256 fingerprint, never a key or certificate body and never the source
address; the api writes the `audit_log` row 14 §5 requires, one per host
event id however often the event is re-sent.

**Guest-raised events** (DECISIONS I-445). `agent_event`, `agent_question`
and a relayed `host_warning` start as a guest's `Notify`, which root in the
guest can write, so hostd bounds them before sending: `agent_event.kind` is
one of the six above (another is dropped; a hostd before I-688 drops
`agent_done` too, so hostd ships before a base whose `repose-notify` sends it); `agent` is at most 32 bytes of
`[a-z0-9_-]`, anything else `unknown`; `tmux_window` at most 64 bytes and
`summary` and `text` at most 1 KB, valid UTF-8 without control characters
(newline and tab allowed in `summary` and `text`); `question_id` is a uuid
and `state` empty, `cancelled` or `expired` (else dropped), and `reason`
one of the four above on a close (anything else becomes empty); at most 3
`options` of 64 bytes; a relayed warning's `detail` is written by hostd
(`guest <id>` and, for the known kinds, the numbers or the process name
guestd reported). A `trigger_run` (I-1256) is bounded the same way: `run_id` a uuid hostd
passed in a `RunTrigger` or saw opened by a delivery, `state` one of
`working`, `done`, `failed`, `timeout` or `skipped` (else dropped),
`reason` one of the enum in `features/triggers.md` §4.1 or, for `gated`,
a gate's line at 200 bytes under the same UTF-8 rule as `summary`,
`summary` at most 1 KB, `trigger` at most 32 bytes of the name charset
(empty is dropped), `window` at most 64 bytes, `dedupe_key` at most 128
bytes (it is a provider's own event id, which is why it is on the run row
and in no log line), `source` one of `route`, `clock` or `hand` (anything
else becomes empty) and `payload_bytes` as sent. Each guest gets a burst of 30 such notifications and one
every 2 s after it; the rest are dropped and counted in
`repose_host_guest_notify_dropped_total{reason}`. The first close (a
`Question` with a state) of a question hostd passed as open passes without
a token (I-690): hostd keeps the ids it passed as open per guest (at most
512, for 24 h 10 min), so these closes are bounded by the opens the bucket
counted. Any other close (an id hostd never saw open, a repeat, one opened
before a hostd restart) takes a token. hostd keeps guest-raised
events awaiting an ack apart from its own (at most 2,000 against 10,000),
and sends them without blocking its own events or results.

The api accepts an event in the old, unbounded shape for one release
(hostd before I-445) and applies the same bounds itself: it stores an
`agent_event` kind outside the four as `error`, cleans and cuts `agent`,
`tmux_window` and `summary`, counts a `host_warning` kind outside the list
above as `other` and logs at most 256 bytes of `detail` on one line, and
stores at most 600 guest-raised events per project per hour, counting
these and the ones the edge's hook path (`POST /internal/events`) brings
together.

**Host scope** (DECISIONS I-447). An event, a `Hello` entry or a sample
for a guest counts only when the guest's project is placed on the host
whose stream carried it; any other is dropped and counted in
`repose_api_host_reports_refused_total{reason="foreign_guest"}`. A
`Result` or a `BuildLog` line counts only from the host the api sent the
command to, whatever the command (an op's, a `PauseGuest`, `WakeGuest`,
`SayToAgent`, `AnswerQuestion` or `RunTrigger`); any other is dropped and
counted under `reason="foreign_command"` (DECISIONS I-1368). A
`snapshot_done` blob path must be `<user_id>/<project_id>/<name>.img.zst`
(or `<project_id>/<name>.img.zst`) for that project, and its time is
clamped to the api's clock.

**Samples** (DECISIONS I-446). hostd cleans the guest-reported part of a
`GuestSample` (at most 32 `agents` and 128 `procs`, `comm` at most 16
bytes without control characters, agent `state` one of `working`, `idle`,
`needs_input`, `running`, `unknown`; a hostd before I-694 sends `running`
as `unknown`). The api stores each guest's rows in a batch of
its own; when a guest's rows are refused it stores the host-measured
fields alone, and counts it in `repose_api_samples_failed_total{reason}`.

CPU pressure and guest memory (DECISIONS I-493).
`cpu_pressure_us_delta` is how far the guest's `/proc/pressure/cpu`
"some" total moved since the last sample (guest-written; hostd keeps the
cursor and caps one move at 300 s). `host_cpu_wait_us_delta` is the same
for the `cpu.pressure` of the guest's hypervisor unit cgroup on the host:
time its vCPU threads waited for a host CPU (host-measured).
`guest_mem_used_bytes` is MemTotal less MemAvailable in the guest
(guest-written, capped at 1 TiB). The api stores them in `meter_samples`
as `cpu_pressure_us`, `host_cpu_wait_us` and `mem_used`. A hostd older
than I-493 sends zero for all three, which is the old shape and stays
accepted.

Root filesystem (DECISIONS I-567). `disk_used_bytes` is the thin
volume's allocated blocks (`lvs data_percent`, host-measured), which
keep a deleted file's blocks until the guest's `fstrim` (daily, and at
every stop since I-585); it keeps that meaning, and is sampled for a
stopped guest as for a running one. The api keeps the newest non-zero
figure no larger than `disk_alloc_bytes` as the project's
`disk_held_bytes`, which the plan's disk counts (I-585). `root_used_bytes` and `root_size_bytes` are the guest's
root filesystem from statfs (blocks less those available to `dev`, and
blocks), guest-written: hostd sends both as 0 unless the size is
non-zero, no larger than `disk_alloc_bytes`, and used is no larger than
the size, and the api applies the same check. The api stores them in
`meter_samples` as `root_used` and `root_size` and serves the newest as
the project's `root_used_bytes` and `root_size_bytes`. A hostd or guest
older than I-567 sends 0 for both, which is the old shape and stays
accepted.

Parked and waiting (DECISIONS I-1672). `parked_waiting_since` is when a
guest was parked, unix seconds, while it waits for its host's user to log
in before it can be restored; 0 otherwise. Only hostd on a Mac without the
appliance sets it (Virtualization.framework restores a save file only in
the user's unlocked session, I-1662); its `guestd_ok` is false, since no
guest runs, and the api reads the field before that. The api keeps it only
from a host an account owns. 0 from every other hostd.

Resting (DECISIONS I-818). A resting guest's sample keeps `guestd_ok`
**true** and repeats the last guest-written figures (`signals`,
`guest_mem_used_bytes`, `root_used_bytes`, `root_size_bytes`), with
`resting` set and `state` still `running`. hostd asks a paused guest for
nothing: a `Sample` into it would wait out its own 10 s, in series,
against a 60 s tick. This one rule is what keeps four other things
honest: the api's unused notice (which reads `not guestd_ok` as use),
`repose start` and the dashboard's Start (which would otherwise reboot a
frozen machine and lose every process), the `repose_host_guestd_lost`
gauge, and the CLI's "the environment's agent is not answering".

## Idempotency and ordering

hostd executes commands for one guest in order; commands for different guests
run concurrently (bounded, 8 at a time, builds 2 at a time). A `WakeGuest`
is the exception to the bound, not to the order: it runs on its guest's
queue, like every other command for that guest, but takes no concurrency
slot, since a user is waiting for it and the host's builds hold the slots
(I-818). A hostd restart
replays nothing; the api re-sends unfinished commands after `Hello` with the
same command_id and hostd returns the stored result, re-executes a command
that had started but not finished (every command except `Exec` is written
to be safe to re-run; `Exec` returns `internal: command interrupted`), and
ignores a command_id that is still executing (its result follows).
hostd keeps results for 7 days. A command cut off because the host is
going down (a boot that fails while systemd says `stopping`) is neither
stored nor sent, so its resend runs it again; the guest keeps its record
as it was, and a resent `CreateGuest` continues a `stopped` record that
never reached Ready. A boot cut off by a hostd restart alone fails and is
stored as before (I-650).

## Fake

`internal/fakes/hostd`: implements the client side in-process, "creates"
guests as structs, emits Samples on a ticker, completes Build after a
configurable delay with a fake closure path, and can be told to fail any
command with any error code. The api's tests and the CLI's end-to-end tests
use it. It names `nix` in `Hello.features` unless `Options.NoNix`, and
`erofs` with `Options.Erofs`; it records each guest's `base_image` and
`menu` (`Guest.BaseImage`, `Guest.BaseVersion`, `Guest.Menu`), reports
`base_version` and `booted_base_version`, and refuses every shape in
"erofs guests" "Refusals" with hostd's code. `SetMenuFail` fails menus:
`ApplyConfig` answers the code, and a boot reports it as `menu_error` with
the guest running (DECISIONS I-1458).
