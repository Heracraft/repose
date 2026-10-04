# Host conventions

What every host guarantees, so hostd, infra and operators agree. Every path,
device, table and unit here is created by `nix/hosts/` (workstream 01); a
change to either happens in the same commit.

## Filesystem

| Path | What |
|---|---|
| `/var/lib/repose/hostd/` | `cert.pem`, `key.pem` (mTLS to api), `host.json` (see below), `state.db` (bbolt: guest table for reconciliation). Mode 0700, written by `hostd register`. |
| `/var/lib/repose/guests/<guest_id>/` | `ch.args` (the rendered cloud-hypervisor argv, one argument per line; DECISIONS I-27), `guest.json` (non-secret copy of the guest record for `hostd reconcile --rebuild`), `ch.sock` (Cloud Hypervisor API), `vsock.sock` (host side of the guest's vsock, `CONNECT 5000` reaches guestd), `console.sock` (serial; hostd copies it into `console.log`, rotated at 64 MB keeping 3), `virtiofsd/virtiofsd.sock`, `virtiofsd-auth/virtiofsd.sock` (the login share, I-278). The parent is `0711 root`; the directory is `1770 root:hostd` so the unprivileged `guest@<id>` (I-51) can create its sockets but not remove hostd's files; `virtiofsd/` is `0750 virtiofsd:hostd` and the socket in it is group `hostd` (`--socket-group`); `virtiofsd-auth/` is the same with owner `repose-auth`. hostd creates each of these without following a link and replaces anything at that name that is not a directory it or the intended owner owns, since the hypervisor's user can create entries in the guest directory (DECISIONS I-466). Secrets are never written here: they are delivered to the guest's tmpfs over vsock. |
| `/var/lib/repose/users/<user_id>/` | The Claude login share (DECISIONS I-278). `0711 root` like its parent; `claude-auth/` is `0700 repose-auth` and holds only the `.credentials.json` Claude Code writes from inside the user's guests. `claude-auth/` is the mount point of the user's own 16 MiB ext4 volume `vg-guests/auth-<user_id>` (256 inodes, mounted `nosuid,nodev,noexec`), so what a guest writes there cannot fill the host's root filesystem (DECISIONS I-464); a pre-I-464 share directory found at the first mount is renamed `claude-auth.legacy/` and removed by the sweep once no guest of the user runs. hostd creates both, never opens anything inside `claude-auth/`, and stamps `last-guest` (root) when a guest of that user boots and at each sweep while one exists; the sweep (hostd start, daily) unmounts and removes the volume and the whole directory 30 days after the stamp. Not on any guest volume, so in no snapshot. |
| `/var/lib/repose/builds/<revision_id>/` | `fragment.nix` for a `Build`; see `nix-build-contract.md` |
| `/var/lib/repose/base/<base_ref>/` | checkout of the platform repository at that revision (its `nix/` is the flake hostd evaluates) |
| `/run/repose/hostd.sock` | hostd's operator control socket (`hostd status`, `guests`, `snapshot-all`, `drain`, `reconcile`) |
| `/run/repose/join-token` | one-shot registration token, written to the installed system over SSH by `infra/azure/modules/host` or by hand per the runbook, mode 0600, deleted after Register. Not from cloud-init: nixos-anywhere replaces the system that ran cloud-init (DECISIONS I-20) |
| `/run/repose/host.env` | rendered from `host.json` at boot by `repose-host-net`: `HOST_ID`, `GUEST_CIDR`, `BRIDGE_ADDR`, `WG_ADDR`, `LOKI_HOST`, `LOKI_PORT`. Read by units that need the addresses (node_exporter, Fluent Bit); hostd may read it too. No secrets in it. |
| `/run/repose/wg0.conf`, `/run/repose/host_ca.pub`, `/run/repose/sshd.conf` | also rendered from `host.json`; wg-quick, sshd `TrustedUserCAKeys` and sshd `ListenAddress` respectively. |
| `/run/repose/store-view/` | Mount point of each guest's store view (DECISIONS I-463); empty on the host. `virtiofsd@<id>` runs with `PrivateMounts=yes` and `TemporaryFileSystem=/run/repose/store-view:mode=0755,nosuid,nodev,size=16m`, shares this path, and pivots into it; hostd then bind-mounts every path of the guest's closure, and of the closures it ran before (up to 16 recorded, plus the project's `rev-*` roots, those still on the host) (`nix-store -qR`) into that namespace, read-only, `nosuid,nodev`, private propagation (a symlink store path is recreated as a symlink). An in-place apply adds the new closure's paths before the switch; nothing is removed until the guest's next start. Nothing is mounted in the host namespace. |
| `/run/repose/store-export/` | Kept for one release (I-463): read-only bind of `/nix/store` with an empty tmpfs over `.links`, the whole store. Guests running when the host switches to I-463 are restarted at the switch (`hostd guests` shows them as `whole-store`); `hostd --store-export /run/repose/store-export` is the rollback that shares it again for every new start. Removed the release after. |
| `/nix/var/nix/gcroots/repose/<guest_id>` | GC root for the guest's system closure; removed on destroy. `rev-<project_id>-<revision_id>` roots keep the last 3 built revisions per project and are all removed when the project's guest is destroyed (DECISIONS I-115; a later restore rebuilds). |
| `/dev/vg-guests/thin` | thin pool (95 percent of the data disk, autoextend at 80 percent by 10 percent, discards passdown, zeroing on); volumes `/dev/vg-guests/g-<guest_id>`, snapshots `snap-<guest_id>-<ts>`, login shares `auth-<user_id>` (16 MiB each, I-464) |
| `/var/log/repose/` | hostd log (journald is primary), build logs per op |
| `/var/lib/node_exporter/textfile/` | node_exporter textfile collector; `repose_lvm.prom` is written every 5 minutes by `repose-pool-monitor.timer` (`repose_lvm_pool_present`, `repose_lvm_pool_size_bytes`, `repose_lvm_pool_data_percent`, `repose_lvm_pool_metadata_percent`, `repose_lvm_volumes`) |

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

## Network

- Provider NIC (`eth0` on Azure): DHCP, default route, NAT egress for
  guests. Nothing listens on it. No public IP; outbound via the Azure NAT
  gateway on the subnet.
- `br-guests` bridge (systemd-networkd netdev, STP off), host address `.1`
  of the host's `/22`, configured at boot from `host.json`.
- Guest address = `.2 + index` allocated by hostd from `state.db`; MAC
  `52:54:` plus the first four bytes of sha256(guest id) (DECISIONS
  I-120; a guest record created before then keeps the MAC stored in it).
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
    DHCP), `guest_fwd` (policy drop;
    established; `wg0 → br-guests` tcp 22 for the gateway; from
    `br-guests`: IPv6 dropped, tcp 25 to `smtp_drop` (DECISIONS I-238),
    tcp 3333, 5555, 7777, 14433 and 14444 to `stratum_drop` (I-239), `ct
    state new` over the per-guest bucket in set `guest_flow_rate`
    (`limit rate over 200/second burst 2000 packets`, I-240) to
    `flows_drop`, jump `guest_dyn`, then drop
    `169.254.169.254` and `168.63.129.16`, drop `10.64.0.0/12`, allow the
    edge WireGuard address on tcp 8443 and 6081, drop every private range
    (`10/8`, `172.16/12`, `192.168/16`, `100.64/10`, `169.254/16`), accept
    to the provider NIC), `smtp_drop`/`stratum_drop`/`flows_drop` (jump
    the hostd-owned `guest_smtp`/`guest_stratum`/`guest_flows`, then
    drop), `guest_dyn`, `guest_smtp`, `guest_stratum`, `guest_flows`
    (empty at boot, hostd-owned), `nat`
    (masquerade `10.64.0.0/12` out of the provider NIC), `output` (accept).
  - `bridge repose`: set `guests` (`ether_addr . ipv4_addr . ifname`,
    hostd-owned), chain `forward` (policy drop: no frame is switched
    between taps), chain `input` (frames from `tap-*` to the host: ARP and
    IPv4 from a tuple in `guests`, nothing else).
  - Per-guest egress metering: `nft add counter inet repose
    egress-<guest_id>` and `nft add rule inet repose guest_dyn ip saddr
    <ip> counter name egress-<guest_id>`; read with `nft list counter`,
    removed on destroy. Blocked attempts likewise, one counter and rule per
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
    the counters, the `guest_flow_rate` elements and the `guests` set
    survive.
- Per-guest egress shape, 200 Mbit/s, on what the guest sends (hostd,
  DECISIONS I-217). A guest's egress is its tap's ingress, so each tap
  gets `tc qdisc add dev <tap> handle ffff: ingress` (only when absent)
  and three filters, each a `tc filter replace ... parent ffff: protocol
  ip prio <n> handle 1`: prio 1 `flower dst_ip 10.64.0.0/12 action
  pass`, prio 2 `flower dst_ip 10.63.255.254/32 action pass`, prio 3
  `flower action police rate 200mbit burst <500 ms> mtu 64kb
  conform-exceed drop/ok` (a flower with no match: `matchall` cannot be
  replaced in place). Traffic to the host itself (the gateway, the
  host services address) is never limited, and nothing limits what the
  host sends to a guest. hostd re-applies the shape to every running
  guest when it starts; `replace` makes that a no-op or an in-place swap.
  The exact commands are `internal/hostd/net/testdata/*.golden`.
  Before I-217 the shape was `tc qdisc replace dev <tap> root handle 1:
  htb default 10` with class `1:10`, which limited host-to-guest traffic
  instead. For one release a tap may still carry it: hostd's shape
  removes it (`tc qdisc del dev <tap> root`) after the policer is in
  place, and `sch_htb` stays loaded. `sch_ingress`, `cls_flower`,
  `act_police` and `act_gact` are loaded (`kernel.nix`).
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
'guest@*'` shows every guest. Host units, in start order:
`nftables.service`, `repose-store-export.service`,
`repose-host-net.service`, `wg-quick-wg0.service`, `repose-register.service`
(oneshot, skipped when `host.json` exists or there is no token),
`repose-guests-slice.service` (sets `guests.slice` `MemoryMax` to RAM minus
the reserve: 8 GiB below 128 GiB, 16 GiB above), `hostd.service`,
`fluent-bit.service` (ships journald and every guest's console log to Loki,
and serves its own Prometheus metrics on `<wg0>:2021/api/v1/metrics/prometheus`
so that a host which has stopped shipping is visible),
`prometheus-node-exporter.service` (on
`<wg0>:9100`), `sshd.service` (on `<wg0>:22`), `repose-snapshot.timer`
(nightly 03:00 local), `repose-pool-monitor.timer` (every 5 minutes),
`nix-gc.timer` (weekly, `--delete-older-than 14d`), `fstrim.timer`.

`guests.slice` has `CPUWeight=100`; `nix-daemon.service` (tenant builds)
has `CPUWeight=50`. Nix: `max-jobs 2`, `cores 8`, `min-free 50G`,
`max-free 100G`, sandboxed, `trusted-users root` only.

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
`repose-cache-volume.service` (idempotent). The cache and the mirror do
not start without that mount (`ConditionPathIsMountPoint`); guests then
use upstream. Log lines carry status, cache status, bytes and time,
never a URL (a package name can be a private one). `repose.host.caches.enable
= false` and a switch removes both caches; the front is removed with them,
so publish a guest base without the settings first (see I-208).

## Cloud Hypervisor invocation (per guest)

hostd renders the `cloud-hypervisor` argv from the guest's system closure
(`kernel`, `initrd`, `init`, `kernel-params`) and its record (DECISIONS
I-27) and runs it with `systemd-run --unit guest@<id> --property
MemoryMax=<class RAM + 512M> --property MemoryHigh=<class RAM + 384M>
--property CPUQuota=<vcpus*100>% --property Slice=guests.slice --property
User=hostd` (MemoryHigh since I-230; a unit started earlier has only
MemoryMax until its next start) and the sandbox of DECISIONS
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
device nodes. The devices: `--disk path=/dev/vg-guests/g-<id>,image_type=raw,direct=on`
(I-63; `direct=on` since I-230: the volume is opened O_DIRECT so the guest's
disk I/O never lands in host page cache charged to the unit, whose RAM part
is unreclaimable shmem; the guest sees the volume's own 4096-byte logical
blocks either way),
`--net tap=tap-<8hex>,mac=52:54:<4 bytes of the id's sha256>`, `--fs tag=ro-store,socket=
virtiofsd/virtiofsd.sock`, `--vsock cid=<1000+index>,socket=vsock.sock`,
`--serial socket=console.sock`, `--console off`, `--memory
size=<RAM>M,shared=on`, `--seccomp true` (the default, written out). The CH
API socket is used for `shutdown` (after guestd's Shutdown timed out),
`pause`, `resume`, `resize-disk` (after `lvextend`, before guestd's
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

## Operator access

Operators reach hosts over the edge's WireGuard (`ssh root@10.255.0.x` with
an operator certificate from the Host CA; sshd trusts `host_ca_pub` from
`host.json`). Root is the only account; there is no sudo and no password.
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
