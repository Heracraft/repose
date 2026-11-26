# Host conventions

Every host keeps the conventions below, so hostd, infra and operators agree. Every path,
device, table and unit here is created by `nix/hosts/` (workstream 01); a
change to either happens in the same commit.

## Filesystem

| Path | What |
|---|---|
| `/var/lib/repose/hostd/` | `cert.pem`, `key.pem` (mTLS to api), `host.json` (see below), `state.db` (bbolt: guest table for reconciliation; the `meta` bucket's `host_boot_id` key is the boot id the last reconcile ran under, and `hostd_stopped_at` (RFC 3339) is when hostd last stopped gracefully, cleared by the next reconcile, and a guest row's `interrupted` object, `{boot_id, at, prev_state}`, marks a guest a host restart took down, DECISIONS I-645; both are additive JSON with no schema bump, and `guest.json` carries `interrupted` too). A guest row's `resting` object, `{since, reason, next_timer}`, marks a guest the idle pause has paused: its vCPUs are stopped and its memory is resident, its `state` stays `running`, and `next_timer` is the next systemd timer elapse hostd read inside it, which is what the armed wake fires on (DECISIONS I-818; additive JSON, no schema bump, and `guest.json` carries it too, so `hostd reconcile --rebuild` keeps it). Only a pause and a wake write it, never `setState`. Mode 0700, written by `hostd register`. `hostd guests --json` prints each guest's `interrupted_boot_id` and `interrupted_at`, and its `resting` and `resting_at`. `freeze/<guest_id>.json`: a parked guest's record (DECISIONS I-652, I-653): state `writing` or `complete`, the closure, class, volume size and Cloud Hypervisor version it was parked with, the guest volume's fingerprint, and the image key wrapped by Key Vault (or, on the Mac appliance, by the launcher's keychain key, `key_id` `keychain-<16 hex>`, I-1386) with its check; written atomically with fsyncs, removed with the image. `freeze/.tally.json`: the host's running totals of guests its parks left skipped and guests its unparks could not restore, by reason, plus the last park's own counts for the log; published into `repose_host_guest_park_skipped_total` and `repose_host_guest_unpark_discarded_total` at each start and added to by every park and unpark, never reset (DECISIONS I-796); a dot file, so it is never read as a guest's record. |
| `/var/lib/repose/guests/<guest_id>/` | `ch.args` (the rendered cloud-hypervisor argv, one argument per line; DECISIONS I-27), `guest.json` (non-secret copy of the guest record for `hostd reconcile --rebuild`), `ch.sock` (Cloud Hypervisor API), `vsock.sock` (host side of the guest's vsock, `CONNECT 5000` reaches guestd), `console.sock` (serial; hostd copies it into `console.log`, rotated at 64 MB keeping 3, at most 2 KiB a second with a 1 MiB burst; output over that is read and dropped, and the log gets one `[repose: N bytes of console output dropped, ...]` line, DECISIONS I-452), `virtiofsd/virtiofsd.sock`, `virtiofsd-auth/virtiofsd.sock` (the login share, I-278). The parent is `0711 root`; the directory is `1770 root:hostd` so the unprivileged `guest@<id>` (I-51) can create its sockets but not remove hostd's files; `virtiofsd/` is `0750 virtiofsd:hostd` and the socket in it is group `hostd` (`--socket-group`); `virtiofsd-auth/` is the same with owner `repose-auth`. hostd creates each of these without following a link and replaces anything at that name that is not a directory it or the intended owner owns, since the hypervisor's user can create entries in the guest directory (DECISIONS I-466). Secrets are never written here: they are delivered to the guest's tmpfs over vsock. |
| `/var/lib/repose/users/<user_id>/` | The Claude login share (DECISIONS I-278). `0711 root` like its parent; `claude-auth/` is `0700 repose-auth` and holds only the `.credentials.json` Claude Code writes from inside the user's guests. `claude-auth/` is the mount point of the user's own 16 MiB ext4 volume `vg-guests/auth-<user_id>` (256 inodes, mounted `nosuid,nodev,noexec`), so what a guest writes there cannot fill the host's root filesystem (DECISIONS I-464); a pre-I-464 share directory found at the first mount is renamed `claude-auth.legacy/` and removed by the sweep once no guest of the user runs. hostd creates both, never opens anything inside `claude-auth/`, and stamps `last-guest` (root) when a guest of that user boots and at each sweep while one exists; the sweep (hostd start, daily) unmounts and removes the volume and the whole directory 30 days after the stamp. Not on any guest volume, so in no snapshot. |
| `/var/lib/repose/builds/<revision_id>/` | `fragment.nix` for a `Build`; see `nix-build-contract.md` |
| `/var/lib/repose/base/<base_ref>/` | checkout of the platform repository at that revision (its `nix/` is the flake hostd evaluates) |
| `/run/repose/hostd.sock` | hostd's operator control socket (`hostd status`, `guests`, `snapshot-all`, `drain`, `reconcile`, `park`, `unpark`, `pause <guest_id>`, `wake <guest_id>`). `park` and `unpark` were `freeze` and `thaw` until I-1002; the socket answers `POST /freeze` and `POST /thaw` as well for one release, and they are removed the release after. The record directory and the image volumes keep their `freeze`/`frz-` names on purpose (I-1002): a switch replaces hostd and the host then reboots, so guests are parked by the old binary and unparked by the new one, and renaming either would lose a guest's memory on the very restart that shipped the rename. |
| `/run/repose/join-token` | one-shot registration token, written to the installed system over SSH by `infra/azure/modules/host` or by hand per the runbook, mode 0600, deleted after Register. Not from cloud-init: nixos-anywhere replaces the system that ran cloud-init (DECISIONS I-20). On the Mac appliance, written by `repose-launcher-join` from the launcher's share (below, I-1380) |
| `/run/repose/hub.json` | the host's place on the hub before it has registered (DECISIONS I-1380): `{private_key, address, edge_pubkey, edge_endpoint}`, the `wg` half of the join bundle `POST /hosts` minted. Mode 0600, written by `repose-launcher-join`; `repose-host-net` renders `wg0.conf` from it while there is no `host.json`, so `hostd register` reaches the api at the control plane's hub address (I-1382), and removes it once `host.json` exists. Absent on a host that registers over the VNet |
| `/run/repose/launcher/` | the Mac appliance only (`nix/hosts/mac.nix`): the directory the launcher shares in over virtio-fs (mount tag `repose-launcher`). `join` in it is the join bundle, 0600, read once by `repose-launcher-join` and deleted |
| vsock CID 2, port 1386 | the Mac appliance only: the launcher's park-key service (DECISIONS I-1386, I-1394; `internal/parkkey`), which vfkit forwards to `park-key.sock` beside the disk on the Mac, where `repose host run` listens for as long as the VM runs. hostd (`--park-key-vsock 1386`, `repose.host.park.launcherPort`) dials it once per request, one newline-ended JSON object each way: `{"v":1,"op":"ready"}`, `{"v":1,"op":"wrap","key":b64}` and `{"v":1,"op":"unwrap","wrapped":b64,"key_id":s}`, answered `{"v":1,"ok":true,...}` with `key_id`, `wrapped` or `key`, or `{"v":1,"ok":false,"error":s}`. The launcher wraps with AES-256-GCM under the one key it keeps in the user's keychain, the key id (a salted hash of that key) as additional data; the wrapping key never crosses, and a record from a key the keychain no longer holds is refused by name. A request that is not JSON, not version 1, over 4 KiB or an unknown op is answered with an error. A `v` the other side does not know is refused, and the shape stays accepted for one release after a change |
| `/run/repose/host.env` | rendered from `host.json` at boot by `repose-host-net`: `HOST_ID`, `GUEST_CIDR`, `BRIDGE_ADDR`, `WG_ADDR`, `LOKI_HOST`, `LOKI_PORT`. Read by units that need the addresses (node_exporter, Fluent Bit); hostd may read it too. No secrets in it. |
| `/run/repose/wg0.conf`, `/run/repose/host_ca.pub`, `/run/repose/sshd.conf` | also rendered from `host.json`; wg-quick, sshd `TrustedUserCAKeys` and sshd `ListenAddress` respectively. |
| `/run/repose/store-view/` | Mount point of each guest's store view (DECISIONS I-463); empty on the host. `virtiofsd@<id>` runs with `PrivateMounts=yes` and `TemporaryFileSystem=/run/repose/store-view:mode=0755,nosuid,nodev,size=16m`, shares this path, and pivots into it; hostd then bind-mounts every path of the guest's closure, and of the closures it ran before (up to 16 recorded, plus the project's `rev-*` roots, those still on the host) (`nix-store -qR`) into that namespace, read-only, `nosuid,nodev`, private propagation (a symlink store path is recreated as a symlink). An in-place apply adds the new closure's paths before the switch; nothing is removed until the guest's next start. Nothing is mounted in the host namespace. |
| `/run/repose/store-export/` | Kept for one release (I-463): read-only bind of `/nix/store` with an empty tmpfs over `.links`, the whole store. Guests running when the host switches to I-463 are restarted at the switch (`hostd guests` shows them as `whole-store`); `hostd --store-export /run/repose/store-export` is the rollback that shares it again for every new start. Removed the release after. |
| `/nix/var/nix/gcroots/repose/<guest_id>` | GC root for the guest's system closure; removed on destroy. `rev-<project_id>-<revision_id>` roots keep the last 3 built revisions per project and are all removed when the project's guest is destroyed (DECISIONS I-115; a later restore rebuilds). `good-<guest_id>` keeps the guest's last good closure, the one a boot that never reaches Ready falls back to; removed on destroy (I-590). |
| `/dev/vg-guests/thin` | thin pool (95 percent of the data disk, autoextend at 80 percent by 5 percent of the pool (I-643), discards passdown, zeroing on); volumes `/dev/vg-guests/g-<guest_id>`, snapshots `snap-<guest_id>-<ts>`, login shares `auth-<user_id>` (16 MiB each, I-464), parked guests' memory images `frz-<guest_id>` (class RAM + 1536 MiB, dm-crypt plain aes-xts-plain64 as `/dev/mapper/repose-frz-<guest_id>`, btrfs with `compress-force=zstd:1` above the dm-crypt so the disk still sees ciphertext and holds about a fifth of the guest's memory, mounted on `<guest dir>/freeze` only while written or read; an image written before I-685 is ext4 and its record says so; DECISIONS I-653, I-685). The pool is overcommitted by design: the plan's disk counts what volumes hold, not their sizes (DECISIONS I-585), so I-449's bound on the sum of the sizes is gone. Guarded by real use, data or metadata whichever is fuller (I-586): at 70 percent the api places no new project there and hostd sends `pool_high`; lvm autoextends at 80; hostd refuses (`insufficient_capacity`) a create, restore or resize at 85 and a start at 95; `PoolFull` pages at 90 and `PoolMetadataHigh` at 80 percent metadata. A running guest is never stopped for the pool. Only `repose-guest-pool.service` activates it (DECISIONS I-643): the initrd has no LVM, lvm.conf has `auto_activation_volume_list = [ ]` and the VG's autoactivation flag is off, so udev's event activation activates nothing. While the pool is inactive, missing, `failed` or `metadata_read_only`, hostd answers `volume_unavailable`, reports `pool_unavailable` and exports `repose_host_pool_available 0` (I-644) |
| `/var/log/repose/` | hostd log (journald is primary), build logs per op |
| `/var/lib/node_exporter/textfile/` | node_exporter textfile collector; `repose_lvm.prom` is written every 5 minutes by `repose-pool-monitor.timer` (`repose_lvm_pool_present`, `repose_lvm_pool_active`, `repose_lvm_pool_monitored`, `repose_lvm_pool_size_bytes`, `repose_lvm_pool_data_percent`, `repose_lvm_pool_metadata_percent`, `repose_lvm_volumes`, `repose_lvm_volumes_inactive`; a metric with no value, as an inactive pool's percentages, is left out, I-643), and again by `repose-guest-pool` when it ends. `repose_kernel.prom` is written every minute by `repose-kernel-check.timer` (`repose_kernel_check{check}` for `large_hash_linear`, `cmdline_recognised`, `large_hash_vmalloc_absent` (I-1010) and `fault_around_off` (I-1042) -- four checks, so an alert written from a list of three misses the last); its own file, not `repose_lvm.prom`, because node_exporter rejects a whole file for one bad line |
| `/var/lib/repose/root-console.hash` | root's password hash for the serial console, placed by hand (`install -m 0400`), never in the repo; read at each activation. Absent, root is locked. Hosts and the edge (DECISIONS I-654) |

### `host.json`

Written by `hostd register` (0600), read at every boot by
`repose-host-net`. This is the only input the host configuration takes at
runtime; hostd does not write `wg0.conf` or any other network file
(DECISIONS I-18).

```json
{
  "host_id": "01926b3e-7c7a-7f1e-9b0a-4a2f6c1d3e55",
  "guest_cidr": "10.64.4.0/22",
  "wg": {
    "private_key": "<base64>",
    "address": "10.255.0.7/16",
    "edge_pubkey": "<base64>",
    "edge_endpoint": "edge.repose.herakraft.co:51820"
  },
  "host_ca_pub": "ssh-ed25519 AAAA... repose-host-ca",
  "loki_url": "http://10.255.0.1:3100"
}
```

`host_ca_pub` comes from `RegisterResponse.host_ca_pub` (the api's SSH
Host CA, DECISIONS I-139; hostdev sends its own CA), on `Register` and on
every `Rotate`, and hostd restarts `repose-host-net` after a rotate that
changed it so `/run/repose/host_ca.pub` follows. A host registered before
I-139 has an empty one until its first rotate; `ops/RUNBOOK.md` "Operator
certificate refused by a host" is the by-hand step for the interval.

`loki_url` comes from `RegisterResponse.loki_url`, which the api fills
from the `loki_url` setting (`repose-admin edge loki`, DECISIONS I-95).
It is absent when no Loki has been recorded; `repose-host-net` then
renders an empty `LOKI_HOST` and `fluent-bit.service` refuses to start
with that reason in the journal, rather than retrying a connection to
nothing for ever.

After writing it, `hostd register` exits and the unit runs `systemctl
restart repose-host-net.service`; a running hostd that re-registers or
rotates keys does the same restart itself.

## hostd subcommands the host calls

| Command | Called by | Contract |
|---|---|---|
| `hostd --state /var/lib/repose/hostd --api-addr <addr> [--api-ca <pem>] (--blob-url <url> --blob-container <name> [--blob-identity <client id>] \| --snapshot-dir <dir>)` | `hostd.service` | the daemon. The api address, its CA (only for the `hostdev` stand-in, I-17) and the snapshot target come from `repose.host.apiAddr`, `apiCA` (a PEM in the store) or `apiCAFile` (a path on the host, I-75), and `snapshots.*` (DECISIONS I-40); hostd refuses to start without a snapshot target. The binary is on the operator's PATH: the `hostd status`, `hostd guests` and `hostd reconcile` of docs/ops/RUNBOOK.md are this same binary over the control socket. Exit status 3 means "join token used or invalid"; the unit does not restart on it. |
| `hostd register --state <dir> --join-token /run/repose/join-token` | `repose-register.service`, once, before hostd | exit 0 with `host.json`, `cert.pem`, `key.pem` written and the token deleted; exit 0 doing nothing if `host.json` exists; exit 3 on a rejected token; any other non-zero is retried after 30 s. |
| `hostd audit-login` | PAM session hook on every sshd login | environment `PAM_TYPE`, `PAM_USER` and sshd's `SSH_AUTH_INFO_0`; writes the journal line `operator_login` with the certificate's `key_id` and `cert_serial` (or a plain key's `key_fp`), never a key body, and on `open_session` hands the same identifiers to the running daemon over `/run/repose/hostd.sock` (`POST /operator-login`), which sends them to the api as the `operator_login` host event for the `audit_log` row (DECISIONS I-140). Must be quick and never block a login: a 2 s timeout, and without a daemon the journal line is the record. `PAM_RHOST` is never recorded. |
| `hostd snapshot-all` | `repose-snapshot.timer` at 03:00 local | snapshots every running guest without the api. |
| `hostd park --shutdown` | `repose-guest-park.service` ExecStop (only with `repose.host.park.enable`) | parks every running guest to its encrypted image within `--park-deadline` (240 s, DECISIONS I-796), smallest first (DECISIONS I-652); does nothing unless `systemctl is-system-running` says `stopping`, or when the park is off. A snapshot is started only when it and the ones already running still fit the deadline at `--park-write-rate`; a guest left out keeps running, loses its memory at the restart, and is counted in `repose_host_guest_park_skipped_total{reason}`. The park waits for nothing past the deadline plus 120 s, whatever a snapshot is doing, so its ExecStop returns before systemd's TimeoutStopSec and the count is always written (I-796). hostd restores each complete image at its next start, before Hello, within `--park-restore-timeout` plus the images' own read time; one it discards is counted in `repose_host_guest_unpark_discarded_total{reason}`. `hostd park` (no flag) and `hostd unpark` are the operator's. The daemon flags: `--park`, `--park-key-vault <url> --park-key-name <name>` or `--park-key-file <pem>`, `--park-deadline`, `--park-write-rate <MB/s>`, `--park-restore-timeout`, `--free-page-reporting`, `--scheduled-events --vm-name <name>`. |

## Network

- Provider NIC (`eth0` on Azure): DHCP, default route, NAT egress for
  guests. Nothing listens on it. No public IP; outbound via the Azure NAT
  gateway on the subnet.
- `br-guests` bridge (systemd-networkd netdev, STP off), host address `.1`
  of the host's `/22`, configured at boot from `host.json`.
- Guest address = `.2 + index` allocated by hostd from `state.db`; MAC
  `52:54:` plus the first four bytes of sha256(guest id) (DECISIONS
  I-120; a guest record created before then keeps the MAC stored in it).
  An address is reused once its guest is destroyed, so hostd deletes the
  host's neighbour entry for it on `br-guests` (`ip neigh del <ip> dev
  br-guests`, an absent entry ignored) when the destroy has removed the
  tap, and again at every boot and restore before the guest can be
  dialled: a STALE entry with the old guest's MAC otherwise left the
  next guest unreachable for 30 to 55 s (DECISIONS I-1530).
- Tap per guest `tap-<8 hex of sha256(guest id)>` (I-120: the id's own
  first eight hex are its UUIDv7 timestamp, shared by guests created in
  the same minute; hostd refuses a create whose tap or MAC another record
  holds), attached to the bridge by hostd with exactly these settings,
  which the nftables bridge table depends on:

  ```
  ip tuntap add tap-<8hex> mode tap user hostd
  ip link set tap-<8hex> master br-guests up
  bridge link set dev tap-<8hex> isolated on learning off flood off
  bridge fdb add <mac> dev tap-<8hex> master static
  nft add element bridge repose guests { <mac> . <ip> . tap-<8hex> }
  ```

  `isolated on` stops frames between taps at the bridge; `learning off`
  plus a static FDB entry stops a guest from claiming another guest's MAC;
  the set element is what lets the guest's IPv4 and ARP frames reach the
  host at all. Removed in reverse on stop.
- nftables, two tables, both declared by the host and reloaded without
  touching what hostd added:
  - `inet repose`: chains `input` (policy drop: lo, established, wg0 for
    ssh/9100/9101 and the Fluent Bit metrics port 2021 (DECISIONS I-56),
    DHCP and ICMP on the provider NIC; from `br-guests` jump
    `guest_in`), `guest_in` (replies to flows the host itself opened,
    matched as `ct direction reply ct state established,related`, so an
    operator can reach a guest on 22 by jumping through the host until the
    gateway exists (DECISIONS I-74); ICMP echo to the host rate-limited to
    5/second; everything else dropped, and a guest's own first packet is
    the original direction, so nothing a guest opens reaches the host; no
    DHCP; new flows to the cache ports go to `flows_drop` past the guest's
    connection cap in set `guest_conns` or over its bucket in set
    `guest_cache_rate`, 200/second burst 2000, I-453), `guest_fwd` (policy drop;
    established; `wg0 → br-guests` tcp 22 for the gateway; from
    `br-guests`: IPv6 dropped, tcp 25 to `smtp_drop` (DECISIONS I-238),
    tcp 3333, 5555, 7777, 14433 and 14444 to `stratum_drop` (I-239), `ct
    state new add @guest_conns { ip saddr ct count over 16384 }` to
    `flows_drop` (I-453), `ct
    state new` over the per-guest bucket in set `guest_flow_rate`
    (`limit rate over 200/second burst 2000 packets`, I-240) to
    `flows_drop`, then drop
    `169.254.169.254` and `168.63.129.16`, drop `10.64.0.0/12`, allow the
    edge WireGuard address on tcp 8443 (hook ingest; 6081 went with
    DECISIONS I-1216, where the browser view's relay took that port and
    dials the guest rather than the other way round), drop every private range
    (`10/8`, `172.16/12`, `192.168/16`, `100.64/10`, `169.254/16`), accept
    to the provider NIC), `smtp_drop`/`stratum_drop`/`flows_drop` (jump
    the hostd-owned `guest_smtp`/`guest_stratum`/`guest_flows`, then
    drop), `guest_meter` (forward hook at `filter + 10`, policy accept:
    from `br-guests` out of the provider NIC, jump `guest_dyn`; I-1063),
    `guest_dyn`, `guest_smtp`, `guest_stratum`, `guest_flows`
    (empty at boot, hostd-owned), `nat`
    (masquerade `10.64.0.0/12` out of the provider NIC), `output` (policy
    accept; sockets of the `nixbld` group and of the build user jump to
    `build_out`), `build_out` (accept DNS to the resolved stub
    `127.0.0.53`, drop anything leaving on `lo`, drop IMDS, the wire
    server and every private, CGNAT, loopback, link-local and reserved
    range, drop anything not leaving on the provider NIC; DECISIONS
    I-439).
  - `bridge repose`: set `guests` (`ether_addr . ipv4_addr . ifname`,
    hostd-owned), chain `forward` (policy drop: no frame is switched
    between taps), chain `input` (frames from `tap-*` to the host: ARP and
    IPv4 from a tuple in `guests`, nothing else).
  - Per-guest egress metering: `nft add counter inet repose
    egress-<guest_id>` and `nft add rule inet repose guest_dyn ip saddr
    <ip> counter name egress-<guest_id>`; read with `nft list counter`,
    removed on destroy. `guest_dyn` is jumped from `guest_meter`, a
    forward chain after `guest_fwd` that sees only what `guest_fwd`
    accepted, for packets from `br-guests` out of the provider NIC. So the
    counter holds every byte the guest sent the internet and nothing it
    sent the edge (SSH, port forwards, hooks, the browser view) or the
    host (the caches); `GuestSample.net_egress_bytes_delta` is its delta,
    and `net_tx_bytes_delta` and `net_rx_bytes_delta` are still the tap's
    (DECISIONS I-1063). Blocked attempts likewise, one counter and rule per
    kind (I-238..I-240): `nft add counter inet repose <kind>-<guest_id>`
    and `nft add rule inet repose guest_<kind> ip saddr <ip> counter name
    <kind>-<guest_id>` for `kind` in `smtp`, `stratum`, `flows`; hostd reads
    them all with `nft list counters table inet repose` each sample. At
    every start hostd adds a counter `nft list counters` lacks and a rule
    its chain lacks (`nft -a list chain`), so a guest started again after
    a stop, whose rules went and whose counters stayed, gets its rules
    back; a stop deletes the rules by handle, a destroy the counters. A
    `systemctl reload nftables` flushes only the chains this configuration
    declares; `guest_dyn`, `guest_smtp`, `guest_stratum`, `guest_flows`,
    the counters, the `guest_flow_rate`, `guest_cache_rate` and
    `guest_conns` elements and the `guests` set survive.
  - Connection tracking (I-453): `nf_conntrack_max` 1048576 and
    `nf_conntrack_tcp_timeout_established` 86400 (`kernel.nix`); a guest
    holds at most 16384 entries (`repose.host.guestEgress.maxConnections`),
    outbound and to the caches together, and a flow past that is dropped
    and counted as `flows-<guest_id>`.
- Per-guest egress shape, 200 Mbit/s, on what the guest sends (hostd,
  DECISIONS I-217). A guest's egress is its tap's ingress, so each tap
  gets `tc qdisc add dev <tap> handle ffff: ingress` (only when absent)
  and three filters, each a `tc filter replace ... parent ffff: protocol
  ip prio <n> handle 1`: prio 1 `flower dst_ip 10.64.0.0/12 action
  pass`, prio 2 `flower dst_ip 10.63.255.254/32 action pass`, prio 3
  `flower action police rate 200mbit burst <500 ms> mtu 64kb
  conform-exceed drop/ok` (a flower with no match: `matchall` cannot be
  replaced in place). Traffic to the host itself (the gateway, the
  host services address) is never limited.
- Per-guest download shape, 1 Gbit/s, on what the host sends the guest
  from anywhere but the host itself (DECISIONS I-451): the tap's root is
  `tc qdisc replace dev <tap> root handle 2: htb default 20` (only when
  `tc qdisc show` lacks `htb 2:`), then `tc class replace ... parent 2:
  classid 2:10 htb rate 10gbit burst 1mb cburst 1mb quantum 65536`, `tc
  class replace ... parent 2: classid 2:20 htb rate 1000mbit ceil 1000mbit
  burst <10 ms, at least 128 KiB> cburst <the same> quantum 65536`, `tc qdisc replace ... parent
  2:20 handle 20: fq_codel`, and `tc filter replace ... parent 2: protocol
  ip prio <n> handle 1 flower src_ip <prefix> classid 2:10` for
  `10.64.0.0/12` (prio 1) and `10.63.255.254/32` (prio 2), so the gateway
  and the caches are not limited. hostd re-applies both shapes to every
  running guest when it starts; `replace` makes that a no-op or an
  in-place swap. The exact commands are
  `internal/hostd/net/testdata/*.golden`. Before I-217 the root was `htb
  1:` with class `1:10` at 200 Mbit/s; a tap still carrying it has it
  replaced by `2:` in the same `tc qdisc replace`. `sch_htb`,
  `sch_fq_codel`, `sch_ingress`, `cls_flower`, `act_police` and
  `act_gact` are loaded (`kernel.nix`).
- WireGuard `wg0` (`wg-quick-wg0.service`, config rendered from
  `host.json`) to the edge; host address from the edge's `10.255.0.0/16`
  pool, `AllowedIPs 10.255.0.0/16`, keepalive 25 s. `AllowedIPs` on the
  edge side = the host's guest `/22` plus its wg address.

## Services

`hostd.service` (Restart=always, RestartSec=2, KillMode=process,
RestartPreventExitStatus=3), `virtiofsd@<guest>.service`,
`virtiofsd-auth@<guest>.service` (the login share; absent for a guest with
no user id) and
`guest@<guest>.service` are **transient** units created by hostd with
`systemd-run` inside `guests.slice`, named so `systemctl list-units
'guest@*'` shows every guest. A static drop-in for the template,
`/etc/systemd/system/guest@.service.d/overrides.conf` (`nix/hosts/hostd.nix`),
applies to every `guest@` unit, running ones included after the switch's
daemon-reload: `After=virtiofsd@%i.service virtiofsd-auth@%i.service`
(a shutdown stops the guest before its store share) and
`ExecStop=repose-guest-clean-stop %i`, which, only while systemd says the
system is stopping, presses the guest's ACPI power button through
`ch.sock` and waits up to 20 s for the hypervisor to exit before
systemd's SIGTERM (DECISIONS I-650). Host units, in start order:
`nftables.service`, `repose-store-export.service`,
`repose-host-net.service`, `wg-quick-wg0.service`, `repose-register.service`
(oneshot, skipped when `host.json` exists or there is no token; ordered
after `wg-quick-wg0.service` too, for the bootstrap tunnel of I-1380),
`repose-guest-pool.service` (oneshot, `RemainAfterExit`, up to 30
minutes; the one unit that activates `vg-guests/thin` after boot, clears
the leftovers of an interrupted activation top down at open count 0,
never retries or repairs a failed thin_check (exit 2), turns dmeventd
monitoring on and checks it; never restarted by a switch; a failure
leaves `/run/repose/pool-hold` and its `ExecCondition` skips the unit
until that file is removed, so hostd's restarts never re-run it; DECISIONS
I-643; hostd and the cache volume are ordered after it, hostd only
`Wants` it), `repose-nvme-timeout.service` (writes the NVMe
`io_timeout`, I-649),
`repose-guests-slice.service` (sets `guests.slice` `MemoryMax` to RAM minus
the reserve: one eighth of total memory, at least 2 GiB and at most 16 GiB), `hostd.service`,
`fluent-bit.service` (ships journald and every guest's console log to Loki,
through two outputs with a disk buffer each, so consoles cannot evict
host lines (DECISIONS I-452),
and serves its own Prometheus metrics on `<wg0>:2021/api/v1/metrics/prometheus`
so that a host which has stopped shipping is visible),
`prometheus-node-exporter.service` (on
`<wg0>:9100`), `sshd.service` (on `<wg0>:22`), `repose-snapshot.timer`
(nightly 03:00 local), `repose-pool-monitor.timer` (every 5 minutes),
`nix-gc.timer` (weekly, `--delete-older-than 14d`), `fstrim.timer`.
With `repose.host.park.enable`: `repose-guest-park.service` (oneshot,
RemainAfterExit, after hostd and the pool unit; its ExecStop is `hostd
park --shutdown`, TimeoutStopSec the deadline plus 150 s (I-796), never restarted
by a switch) and a drop-in on `guest@`, `virtiofsd@` and `virtiofsd-auth@`
with `Before=repose-guest-park.service`, so at shutdown no guest unit
stops until the park is done (DECISIONS I-652).

`guests.slice` has `CPUWeight=100`; `nix-daemon.service` (tenant builds)
has `CPUWeight=50`. Inside the slice each `guest@<id>` carries
`CPUWeight=<milli_vcpus*50/1000>` (25, 50, 100, 200 by class, DECISIONS
I-737 and I-1168; the
multiplier doubled with I-784, which halved the classes' vCPUs, so the
three weights are unchanged), so contention between guests divides in
proportion to the class;
`virtiofsd@<id>` and `virtiofsd-auth@<id>` stay at the default 100. Nix:
`max-jobs 2`, `cores 8`, `min-free 50G`, `max-free 100G`, sandboxed,
`trusted-users root` only.

**Burst cores** (`repose.host.burst.enable`, DECISIONS I-1126; off on
every host at the time of writing). On a host that names the `burst`
feature, a guest cold-started there boots with its class *ceiling* of
vCPUs -- tiny 2, small 4, large 6, xl 8 -- and its unit still starts at the
`CPUQuota` its class sells. A loop in hostd, once a second, reads the
root cgroup's `cpu.stat usage_usec` and each `guests.slice/guest@<id>.service`'s,
and writes `cpu.max` straight into each guest's cgroup: its base when the
host's busy CPU is at or above `repose.host.burst.gate` (0.75 of physical
cores, UNMEASURED) and up to its ceiling, one core a tick, when it is
below. **No cap is ever below the base the class sold.** The headroom is
shared among the guests that used 90 percent of their current cap, in
proportion to their `CPUWeight`, so an `xl`'s burst outranks a `small`'s
and no guest's base loses to another's burst. A resting guest (I-818) and
a guest the api has sent `SetBurst allowed=false` (I-1128) are held at
their base. hostd writes every running guest's base at its start, before
the first tick, so a hostd that died with caps raised does not leave the
host oversubscribed. The cap is written to the cgroup and not set as a
runtime `CPUQuota` over D-Bus, because a daemon reload then undoes it by
re-asserting the unit's own property, which is the base: the failure is a
guest that loses its burst, never a host above the gate. Placement, the
seat count and `CPUWeight` all keep counting the base vCPUs (I-734,
I-736, I-737, I-739).

## Caches (DECISIONS I-202, I-208; `nix/hosts/caches.nix`)

Every host serves guests an npm registry cache and a Docker Hub mirror at
the **host services address** `10.63.255.254`, a `/32` on `br-guests`
that is the same on every host (outside `10.64.0.0/12`), so the guest base
names it without knowing its host. Guests route it through their default
gateway; `inet repose guest_in` accepts `ip daddr 10.63.255.254 tcp dport
{ 4873, 5000 }` and nothing else. Neither port is reachable on `wg0` or
the provider NIC.

| Port | Unit | What |
|---|---|---|
| `10.63.255.254:4873` | `nginx.service` (the front) | proxies to the cache; when the cache refuses or fails (502, 504) it serves from `registry.npmjs.org` itself and logs a `repose_npm_fallback:` line; upstream connect and DNS bounded at 5 s; gzips `application/json` and `application/vnd.npm.install-v1+json` answers for a client that sends `Accept-Encoding: gzip` (level 1, I-217), never tarballs or an answer already encoded |
| `127.0.0.1:4874` | `repose-npm-cache.service` (a second nginx, `-c` its own config) | `proxy_cache` in `/var/cache/repose/npm`, `max_size` 40 GB, least recently used evicted, `inactive` 30 days; tarballs cached 30 days, package documents 5 minutes (stale served while one refreshes, and while the registry is down); tarball URLs in documents rewritten to the front; requests with `Authorization` never cached |
| `10.63.255.254:5000` | `docker-registry.service` | distribution in proxy mode for `https://registry-1.docker.io`, blobs kept 168 h; restarts every 10 s without a start limit (it exits when Docker Hub cannot be reached at start) |

Storage: the thin volume `vg-guests/repose-cache` (64 GB, ext4, label
`repose-cache`), created, formatted and mounted on `/var/cache/repose` by
`repose-cache-volume.service` (idempotent). It requires and follows
`repose-guest-pool.service`, is wanted by it, and is skipped while the
pool is not active (`ExecCondition`); `repose-npm-cache` and
`docker-registry` are wanted by it, so starting the pool unit brings all
three back (DECISIONS I-643). The cache and the mirror do
not start without that mount (`ConditionPathIsMountPoint`); guests then
use upstream. Log lines carry status, cache status, bytes and time,
never a URL (a package name can be a private one). `repose.host.caches.enable
= false` and a switch removes both caches; the front is removed with them,
so publish a guest base without the settings first (see I-208).

## Cloud Hypervisor invocation (per guest)

hostd renders the `cloud-hypervisor` argv from the guest's system closure
(`kernel`, `initrd`, `init`, `kernel-params`) and its record (DECISIONS
I-27). `--cpus boot=<class vCPUs>`, or `boot=<class ceiling>` on a host
with `repose.host.burst.enable` (I-1126): the hypervisor takes one number
and a running guest cannot gain a vCPU, so the ceiling is there from the
first boot and the cgroup holds the guest to its base. hostd runs it with
`systemd-run --unit guest@<id> --property
MemoryMax=<class RAM + 512M> --property MemoryHigh=<class RAM + 384M>
--property CPUQuota=<milli_vcpus/10>% --property CPUWeight=<milli_vcpus*50/1000>
--property Slice=guests.slice --property User=hostd` (MemoryHigh since
I-230; a unit started earlier has only MemoryMax until its next start.
CPUWeight since I-737; a unit started earlier keeps `cpu.weight=100`
until its next start. `CPUQuota` stays the base whatever the host's burst
flag says; the controller writes `cpu.max` at run time, I-1126) and the sandbox of DECISIONS
I-51, pinned verbatim by `internal/hostd/guest/testdata/unit.golden`:
`NoNewPrivileges=yes`, `CapabilityBoundingSet=` (empty), `UMask=0077`,
`ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes`,
`ProtectKernelTunables=yes`, `ProtectKernelModules=yes`,
`ProtectKernelLogs=yes`, `ProtectControlGroups=yes`,
`ProtectProc=invisible`, `RestrictNamespaces=yes`, `RestrictRealtime=yes`,
`RestrictSUIDSGID=yes`, `LockPersonality=yes`,
`SystemCallArchitectures=native`, `TemporaryFileSystem=/var/lib/repose/guests`,
`BindPaths=<guest dir>`, `ReadWritePaths=<guest dir>`,
`DevicePolicy=closed`, `DeviceAllow=/dev/kvm rw`, `DeviceAllow=/dev/net/tun
rw`, `DeviceAllow=/dev/vg-guests/g-<id> rw`, `RestrictAddressFamilies=AF_UNIX
AF_VSOCK`. Cloud Hypervisor therefore runs as `hostd` (in group `kvm`,
owner of the tap, group of its own volume through the udev rule in
`virt.nix`), sees only its own guest directory, and can open exactly three
device nodes. The devices: `--disk path=/dev/vg-guests/g-<id>,image_type=raw,direct=on,bw_size=<bytes>,bw_refill_time=1000,ops_size=<iops>,ops_refill_time=1000`
(I-63; `direct=on` since I-230: the volume is opened O_DIRECT so the guest's
disk I/O never lands in host page cache charged to the unit, whose RAM part
is unreclaimable shmem; the guest sees the volume's own 4096-byte logical
blocks either way; the rate limiter since I-450, reads and writes together,
per class: `tiny` 1000 IOPS and 40 MB/s, `small` 2000 and 80, `large` 3000 and 120, `xl` 4000
and 150, taken at the guest's next start),
`--net tap=tap-<8hex>,mac=52:54:<4 bytes of the id's sha256>`, `--fs tag=ro-store,socket=
virtiofsd/virtiofsd.sock`, `--vsock cid=<1000+index>,socket=vsock.sock`,
`--serial socket=console.sock`, `--console off`, `--memory
size=<RAM>M,shared=on`, `--seccomp true` (the default, written out), and
with `--free-page-reporting` `--balloon size=0,free_page_reporting=on`
(DECISIONS I-652; from the guest's next start).

On a host its user owns (hostd `--owned-host`, DECISIONS I-1626) the class
sizes nothing: `--cpus boot=<host CPUs>`, `--memory size=<MemTotal - owned
reserve - 512M, down to 128M>M,shared=on`, no `bw_size`/`ops_size`, the
balloon always, and the unit's `CPUQuota` and class weight are replaced by
`CPUWeight=100` and `OOMScoreAdjust=1000`; MemoryMax and MemoryHigh follow
the same RAM + 512M and + 384M rule. Each time a guest reaches running,
hostd rewrites the running hypervisors' `/proc/<pid>/oom_score_adj` by
start order, newest 1000 and each older one 250 lower, never under 100,
so the OOM killer takes the newest guest first and never the appliance's
services (`hostd.service` keeps -900, the rest systemd's 0). At its start
hostd sets `guests.slice` `MemoryMax` to MemTotal less the owned reserve
(I-1628) with `systemctl set-property --runtime`. `repose-zram.service`
gives the host a zstd zram swap of half its memory at priority 100,
`vm.swappiness=150` and `vm.page-cluster=0`. hostd drops the page cache
(`sync; echo 1 > /proc/sys/vm/drop_caches`, then `echo 1 >
/proc/sys/vm/compact_memory` so free page reporting, which returns only
free 2 MiB blocks, can hand the freed pages back; through guestd) of a guest
idle for five samples in a row, once until it is busy again, and of every
running guest when free memory is under 1 GiB, at most every 5 minutes. A parked guest is
restored with `cloud-hypervisor --api-socket ch.sock --restore
source_url=file://<guest dir>/freeze/snap,memory_restore_mode=copy
--seccomp true` in the same unit and sandbox; every device comes from the
image. The CH
API socket is used for `shutdown` (after guestd's Shutdown timed out),
`pause`, `resume`, `snapshot` (the park), `resize-disk` (after `lvextend`, before guestd's
`GrowFs`; I-66) and stats; hostd connects to the guest's sockets as root.
virtiofsd runs as `virtiofsd:virtiofsd` with `--sandbox namespace` (a
user and mount namespace with the export pivot_rooted in; `chroot` is
root-only and virtiofsd refuses it for an unprivileged user, DECISIONS
I-48) sharing `/run/repose/store-view`, the guest's own store view
(I-463; `--store-export` names the whole-store export for one release),
binding `virtiofsd/virtiofsd.sock` with `--socket-group hostd` so the
hypervisor can connect. A guest with a user id also gets `--fs
tag=claude-auth,socket=virtiofsd-auth/virtiofsd.sock`, served by
`virtiofsd-auth@<guest>` as `repose-auth:repose-auth` (in group `hostd`
for the same two reasons as `virtiofsd`, I-69) with `--sandbox namespace
--cache never --translate-uid map:1000:<repose-auth uid>:1
--translate-gid map:1000:<repose-auth gid>:1` sharing
`/var/lib/repose/users/<user_id>/claude-auth` (the mounted `auth-<user_id>`
volume, I-464), MemoryMax=64M (DECISIONS I-278). hostd adds the second `--fs` only once that socket exists; a share
that fails to start is logged (`auth_share`) and the guest boots without
it. `repose.host.claudeLoginShare = false` (hostd
`--claude-login-share=false`) starts no share at all.

## Base images on the host

An erofs guest (DECISIONS I-1458) boots a base bundle from the host's
image cache instead of a Nix closure (I-1478, I-1480).

| Path | What |
|---|---|
| `/var/lib/repose/images/` | The cache, `0755 root`, `hostd --images-dir` (the default; `repose.host` passes it on every host, `nix/hosts/hostd.nix`). An empty `--images-dir` boots no erofs guest and drops `erofs` from `Hello.features`. |
| `/var/lib/repose/images/<version>-<arch>/` | One bundle: `vmlinuz`, `initramfs.img`, `rootfs.erofs`, `0644 root`. `<version>` is the `base_versions` label (`[A-Za-z0-9._-]{1,64}`), `<arch>` the host's (`x86_64`, `aarch64`). A directory under this name is always whole and verified: it only ever appears by rename. |
| `/var/lib/repose/images/.tmp-<version>-<arch>/` | A fetch in progress: each file arrives as `<name>.part`, is checked against the `BaseImage`'s SHA-256 as it streams, continues with a `Range` request after a 60 s stall or a broken connection (three attempts, I-1414's pattern), and is renamed to `<name>` once its digest matches. When all three are in, the directory is renamed to `<version>-<arch>/`. A file whose digest differs removes the whole directory and the fetch answers `internal` ("base image checksum mismatch"). The name is fixed, not random, so a hostd that restarts mid-fetch continues from the part files. A second command for a version being fetched waits for the first fetch. A file may be published whole or split (DECISIONS I-1504): when its URL answers 404 (a `file://` path that does not exist counts as one), hostd fetches `<url>.part0`, `<url>.part1`, ... until the first 404 after at least one part, appending each to the same `.part` file with its own attempts and Range resume, and then checks the whole file's SHA-256 from the `BaseImage` as for a whole file. Each part is at most 1900 MiB (GitHub's cap on a release asset is 2 GiB); the part count is in no contract. `<name>.parts` beside the `.part` file records how many parts it holds and their bytes, so a hostd that restarts mid-fetch continues at the part it was on. A part missing in the middle ends the file early, so its digest differs and the bundle is refused like any mismatch. GC removes a temporary directory older than 24 h that no fetch is using. |

- **Records.** bbolt's `base_images` bucket holds one record per bundle,
  `{key, version, arch, bytes, fetched_at, unreferenced_since}` (additive,
  no schema bump). A guest row's `base_version` names its bundle; a Nix
  guest's is empty. `base_needs_fetch` is set by reconcile on a guest whose
  bundle directory is missing (a disk wipe, a GC race): the guest is
  reported as it is, and its next start fetches the bundle from that
  start's `base_image`; a start with none answers `not_found` ("base image
  ... missing from the host; api must resend base_image"). `menu` is the
  last `MenuSpec` a create, start or `ApplyConfig` carried, kept so a
  boot's replay has it (I-1482); package names are tenant content and are
  never logged.
- **GC**, hourly and at start: a bundle no guest names (running, stopped,
  restoring, any state) and that is not the newest fetched for its arch is
  marked `unreferenced_since`; one still unreferenced 24 h later is removed
  with its record. A guest naming it again clears the mark.
- **Metrics**: `repose_host_base_image_fetch_errors_total{reason}`
  (`checksum`, `status`, `stall`, `network`, `disk`),
  `repose_host_base_image_bytes`, `repose_host_base_image_versions`,
  `repose_host_base_image_fetch_duration_seconds`.
- **Cloud Hypervisor.** An erofs guest's argv differs from a Nix guest's
  ("Cloud Hypervisor invocation" above) in four places: `--kernel` and
  `--initramfs` are the bundle's `vmlinuz` and `initramfs.img`; the
  disks are `--disk path=<bundle>/rootfs.erofs,image_type=raw,readonly=on,id=vda`
  then `--disk path=/dev/vg-guests/g-<id>,image_type=raw,direct=on,id=vdb,<the class's limits>`;
  there is no `--fs tag=ro-store` (the login share's `--fs tag=claude-auth`
  stays); and `--cmdline` is `console=<tty> ip=... systemd.hostname=<slug>
  panic=-1 reboot=t`, with no `init=` (the initramfs mounts vda and vdb
  and switches root to `/sbin/init`). `vm.resize-disk` names `vdb` on an
  erofs guest and `_disk0` on a Nix guest, so a resize never reaches the
  read-only disk. No `virtiofsd@<id>` runs for an erofs guest.
- **The unit.** `guest@<id>` of an erofs guest has the Nix guest's whole
  property list plus `TemporaryFileSystem=/var/lib/repose/images` and
  `BindReadOnlyPaths=/var/lib/repose/images/<version>-<arch>`, after
  `ReadWritePaths=<guest dir>`: the hypervisor sees its own bundle,
  read-only, and no other version (`internal/hostd/guest/testdata/unit-erofs.golden`).
- **The volume.** The first create of an erofs guest formats its thin
  volume `mkfs.ext4 -q -L repose-rw -E lazy_itable_init=1`; the guest's
  initramfs lays out `/home-dev`, `/docker`, `/services`, `/etc-persist`
  and `/overlay` on its first boot (`guest-conventions.md`, "The erofs
  guest").
- **A base bump.** A `StartGuest` whose `base_image` names another version
  fetches it first (a failed fetch leaves the guest as it was and answers
  the error), records it on the guest and boots it; the initramfs rotates
  the overlay's upper directory, and hostd's `ApplyMenu` with
  `replay_after_base_upgrade` after `SetupProject` installs the menu again.
  hostd sends that `ApplyMenu` after every erofs boot that has a menu;
  guestd does nothing unless the base changed or the menu did.
- **Snapshots.** The blob's metadata carries `base_version`, and so do `SnapshotResult`, `StopResult` and `SnapshotDone`. A `Restore`
  with `base_image` records it on the new guest and fetches it ahead (a
  fetch that fails there sets `base_needs_fetch` and fails nothing); a
  fork of an erofs guest (`clone`, no `base_image`) takes the source's
  base, or the newest cached one when the cache no longer holds it.
- **`--nix-mode off`** (default `on`): no Nix builder, GC roots or store
  view are constructed, `Hello.features` drops `nix`, and `CreateGuest`,
  `StartGuest`, `Restore`, `Build` and `ApplyConfig` for a Nix guest answer
  `nix_unsupported`. A host with no `nix` on PATH is the same. hostd
  refuses to start with Nix off and `--images-dir` empty.

## Operator access

Operators reach hosts over the edge's WireGuard (`ssh root@10.255.0.x` with
an operator certificate from the Host CA; sshd trusts `host_ca_pub` from
`host.json`). Root is the only account; there is no sudo and SSH takes no
password. On the Azure serial console root logs in with the password
whose hash is `/var/lib/repose/root-console.hash`, and is locked without
it; the systemd-boot menu waits 10 s there (DECISIONS I-649, I-654).
Every host and the edge also boot with `nvme_core.io_timeout=240` (a udev
rule and `repose-nvme-timeout.service` set the running disks to 240000 ms)
and `mana_ib` blacklisted, and carry `ethtool` (I-649).
Every interactive login is logged to `audit_log` via a PAM hook that calls
`hostd audit-login`. `repose-admin exec` is the audited path for touching a
guest. Before the edge exists, `repose.host.bootstrap` (workstream 11) also
opens sshd on the provider NIC for a plain operator key.

## Provisioning

`nixos-anywhere --flake .#host-<name> root@<ip>` from a checkout; the
layout is `nix/hosts/disko-layout.nix` (OS disk GPT with a 1 GB ESP and
ext4 root; data disk one PV, `vg-guests`, thin pool `thin`). The data
device defaults to `/dev/disk/repose/data`, resolved at install time by the disko hook (DECISIONS I-41), and is
`repose.host.dataDevice`. The join token arrives through cloud-init
`write_files` in the instance user-data as `/run/repose/join-token`
(workstream 11 passes it); cloud-init on the host runs only that module.

### A host the user owns (DECISIONS I-1366, I-1370, I-1372)

`repose.host.owned = true` says the machine belongs to a user and not to
repose. Two things follow, and the module asserts the second:

- **The join token arrives without cloud-init.** `repose-register.service`
  (`nix/hosts/registration.nix`) consumes `/run/repose/join-token`
  through `hostd register` and deletes it, and it waits on
  `cloud-final.service` only where `repose.host.provider == "azure"`. So
  anything that writes that file before `hostd.service` registers the
  host: the Mac launcher's join unit (below), an installer's
  `--extra-files`, or a hand `install -m 0600`. The token comes from
  `repose host add`, is single-use and lasts 24 hours; it is
  `hosts.join_token_hash` on the host's row and is stored nowhere else.
- **The host is on the hub before it registers** (DECISIONS I-1380). A
  machine of the user's has no route to the api's VNet address, so
  `POST /hosts` gives it its guest `/22`, its hub address and a bootstrap
  key pair, and the install step carries the private half beside the
  token (the join bundle, `api.md`). `/run/repose/hub.json` is that
  material on the host; `repose-host-net` renders `wg0` from it until
  `host.json` exists; `hostd register` dials `repose.host.apiAddr =
  10.255.255.1:8443`, the control plane's hub address (I-1382); Register
  answers a fresh key and the same address, and `host.json` takes over
  at the restart the register unit asks for.
- **Snapshots stay on the host's own disk.** `snapshots.blobUrl` and
  `snapshots.identityClientId` must be empty, which makes hostd take
  `--snapshot-dir <snapshots.localDir>` and write `FileBlob` under
  `/var/lib/repose/snapshots`. A dead disk loses every snapshot on it,
  which `apps/web/src/content/docs/own-hardware.md` says in those words.
  The alternative, a storage bucket's keys on a machine in someone's
  house, would be a fourth home for a secret, and there are three
  (CLAUDE.md, `docs/features/secrets.md`).

### The Mac appliance (DECISIONS I-1376, I-1380, I-1494)

Debian 13 arm64 with no Nix, built by `images/appliance-arm64/build.sh`
and published as the release `appliance-<version>`
(`repose-appliance-aarch64.raw.zst` and `.sha256`). It is the owned host
above with these differences. An appliance installed from the NixOS
image (`nix/hosts/mac.nix`, `repose-appliance.raw.gz`) keeps running it;
`repose host add` installs the Debian one.

- **One disk, `/dev/vda`**, GPT with fixed partition GUIDs
  (`7a1e0c4b-5e2f-4c1d-9b3a-1d6f0e2c8a0<n>`): 1 `esp`, 64 MiB vfat, with
  systemd-boot and `vmlinuz.efi`; 2 `root`, 2 GiB ext4; 3 `guests`, 2 MiB
  as shipped. Apparent size 2,217,738,240 bytes (the CLI's
  `applianceImageApparentBytes`); the launcher grows the file to the
  user's disk, at least 20 GB. At first boot `repose-guest-vg-init`
  grows partition 3 and `repose-pool-create` makes `vg-guests/thin` on
  it (95 percent of the VG, metadata one percent of the PV clamped to
  1..16 GiB, discards passdown, metadata profile `repose-thin`: autoextend
  at 80 by 5); it refuses a partition with any other signature.
  `repose-mac-grow` then runs at every boot as before and logs the same
  events.
- **The image cache volume**: `vg-guests/images`, 16 GiB thin, ext4
  labelled `repose-images`, made once by `repose-images-volume` (after
  the pool and `repose-mac-grow`, before `hostd`, which wants it) and
  mounted at every boot on `/var/lib/repose/images`, hostd's
  `--images-dir`; a cache already on the root is moved onto it. It logs
  `volume_created size=`, `volume_mounted`, `volume_mounted_already`,
  `cache_moved`. 16 GiB is two arm64 bundles of about 3 GB and a third
  being fetched during a base rotation, with room for the root image to
  grow; thin, so it costs what it holds.
- **The snapshot store volume** (DECISIONS I-1636): `vg-guests/repose-snapshots`,
  thin, half the volume group's size at creation rounded down to a GiB
  (at least 4), ext4 labelled `repose-snapshots`, made once by
  `repose-snapshots-volume` (after the pool and `repose-mac-grow`, before
  `hostd`, which wants it) and mounted at every boot on
  `/var/lib/repose/snapshots`, hostd's `--snapshot-dir`. Snapshots already
  on the root are copied onto it, compared, and only then deleted from the
  root; a copy that fails or differs leaves them where they were and logs
  `snapshots_not_moved`. It logs `volume_created size_gib=`,
  `volume_mounted`, `volume_mounted_already`, `snapshots_moved files=`. The
  x86 installer installs and starts the same unit.
- **Host files hostd brings** (DECISIONS I-1638): at every start, before
  anything writes a snapshot, hostd on a host with
  `/etc/repose/appliance-version` or a `hostd.env` that begins with the x86
  installer's header writes `repose-zram`, `repose-snapshots-volume` and
  their two units when missing or different (temporary file, fsync,
  rename), runs `systemctl daemon-reload` when a unit changed, `systemctl
  enable --now` each unit not yet enabled, and last puts `--owned-host`
  at the front of `HOSTD_ARGS` in `hostd.env` (the same rename). A changed
  `hostd.env` ends that start with exit 0 and systemd starts hostd again
  with it (`Restart=always`; `KillMode=process` keeps the guests). One
  `host_file` event per file written and per unit started. A host with
  neither marker, every NixOS host, is never written to.
- **The kernel**: kernel.org 6.18.57 with Debian's cloud-arm64 config,
  release `6.18.57-repose`, no initramfs (`root=PARTUUID=...-8a02`,
  `console=hvc0`, the fleet's `transparent_hugepage=madvise hashdist=0
  panic=30`). 7.0 is never shipped (I-1376).
- **Files**: hostd at `/usr/local/bin/hostd`, Cloud Hypervisor 53.0 and
  `ch-remote` at `/usr/local/bin`, `virtiofsd` linked there from
  `/usr/libexec`; the scripts at `/usr/local/lib/repose/`; hostd's flags
  in `/etc/repose/hostd.env` (`--nix-mode off`, `--images-dir
  /var/lib/repose/images`, the api at `10.255.255.1:8443` with the
  platform CA at `/etc/repose/api-ca.pem`, `--snapshot-dir`, the M4
  timeouts, the park through the launcher's vsock port 1386); the
  release key at `/etc/repose/release.pub`.
- **Units**, ported from `nix/hosts/` with the same names and logs:
  `hostd`, `guests.slice`, `repose-guests-slice`, `repose-guest-pool`,
  `repose-host-net` (wg0 is `wg-quick@wg0`, `/etc/wireguard/wg0.conf` a
  link to `/run/repose/wg0.conf`), `repose-launcher-join` (the share at
  `/run/repose/launcher`, tag `repose-launcher`), `repose-register`,
  `repose-guest-park`, the `guest@`, `virtiofsd@` and `virtiofsd-auth@`
  drop-ins (`guest@` also after `repose-images-volume`,
  `blk-availability` and the cache's mount, so a shutdown stops guests
  before their base bundle's volume goes, I-1550), `repose-snapshot.timer`, `repose-fault-around`,
  `repose-mac-trim`, and `repose-nftables`, which loads
  `/etc/repose/nftables.nft` (the tables above, its chains declared and
  flushed, hostd's chains and sets kept). The input and forward chains
  end in `host_input` and `host_forward` from
  `/etc/repose/host-chains.nft`, which are empty, so everything else is
  dropped; the provider NIC is `wan0`, named by driver.
- **Not ported**: the npm and Docker Hub caches, node_exporter, Fluent
  Bit, sshd and the Nix build account, and with them the cache rules and
  `build_out`. The bridge's forward chain drops on `br-guests` only.
- **hostd updates** (I-1496): `repose-hostd-update.timer`, every 30
  minutes, lists `https://api.github.com/repos/Heracraft/repose/releases?per_page=30`
  anonymously and takes the newest non-draft `hostd-<version>` (version
  order, `sort -V`) that carries a `manifest.json`; `hostd-stable` is a
  dead name, never read. The manifest is
  `{"version", "arches": {"amd64"|"arm64": {"url", "sha256"}},
  "signature"}`, the signature being the release key's over
  `repose-hostd-manifest v1\nversion V\namd64 SHA URL\narm64 SHA URL\n`.
  Each verified manifest is kept in `/var/lib/repose/hostd-update/`
  (0700 root); a lookup answered 403 or 429 (the API's anonymous rate
  limit) uses the kept one and exits 0, which for a host on it restarts
  nothing. A bad signature, an older version, a wrong sha256 or a binary
  that reports another version changes nothing; otherwise the binary is
  renamed over `/usr/local/bin/hostd` and hostd restarted
  (`KillMode=process`, guests kept). It logs `release_found tag=`,
  `lookup_failed status=`, `nothing_to_do reason=no_cached_manifest`,
  `no_release`, `manifest_verified`, `sha256_verified`,
  `updated from= to= seconds=`, `up_to_date` and
  `update_refused reason=signature|older|sha256|version_mismatch|manifest_shape`.
- **Memory**: the launcher gives the VM three quarters of the Mac's (12
  GiB of 16, 18 of 24) and all but two cores; the reserve inside is the
  fleet rule (I-724). `repose host ls` shows the VM's memory.

### An x86 machine (DECISIONS I-1498)

`ops/install-host-x86.sh`, served as `https://repose.run/install-host.sh`,
puts the same files and units on Ubuntu 24.04 or Debian 12+ x86_64
(generated into it from `images/appliance-arm64/host/` by `embed.sh`),
with these differences: Cloud Hypervisor's x86_64 static build, packages
from apt, hostd from the signed manifest (or `--hostd-binary PATH`), the
pool on `--device` or on a file, `/var/lib/repose/guests.img` by
default, attached at boot by `repose-guest-loop` (`/etc/repose/pool.env`),
the image cache volume made the same way on the machine's pool, the
uplink the default route's interface, `host_input` and
`host_forward` set to `accept` because the machine's own traffic is its
owner's, no `repose-mac-*` units and no park. The join argument is
written to `/run/repose/launcher/join` for `repose-launcher-join`, and a
`--join-file` it came from is deleted (I-1592). `--uninstall` removes
all of it again, the pool's volume group and physical volume label
included, and is refused while hostd's journal does not show the api's
`host is retired` or `host is lost` for a registered host, unless
`--force` (I-1590).
