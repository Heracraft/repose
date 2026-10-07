# Runbook

Symptom-titled. Each alert in `../CHECKLIST.md` and `../workstreams/10-observability.md`
has an entry with the same name. Commands assume an operator certificate
(`repose-admin operator-cert`) and the edge WireGuard up on the operator's
machine.

## First-time setup

### Operator machine

```bash
nix develop                                  # go, tofu, az, wg, promtool, grafana-cli
az login
ops/dev/operator-cert.sh <control>           # 8h Host-CA cert next to ~/.ssh/id_ed25519
```

Postgres publishes no port: it is on the control VM's `coolify` Docker
network only (`ops/coolify/postgres/docker-compose.yml`). `repose-admin`
therefore runs inside the api container, which has `DATABASE_URL`:
`ssh root@<control> docker exec <api container> repose-admin ...`, and
`psql` is `docker exec -it <postgres container> psql -U repose`. The
operator reaches the control VM and the edge's sshd on 2222 either from an
address in `operator_cidrs`, or as a WireGuard peer of the edge, which the
edge's input chain admits on 2222; hosts are then `ssh -J
root@<edge>:2222 root@10.255.0.x` with the certificate. kanali, the
owner's coordinator guest, is the second kind (DECISIONS I-359): `sudo
wg-quick up ~/.kanali/wg-repose.conf`, then `ssh repose-edge`,
`repose-control`, `host-01`. A guest's tunnel must carry only packets from
its own peer address (I-360): the gateway reaches every guest from the
edge's 10.255.0.1, and a main-table route to that address sends the
guest's replies into the tunnel, where the edge drops them.

### Control plane (control VM, a server of the owner's Coolify)

The full click path, with the reasons, is `coolify.md`. The short form:

1. `make -C infra apply ENV=prod` with `coolify_count = 1` in `prod.tfvars`
   and the owner's Coolify address in `coolify_manager_cidrs`
   (`prod.local.tfvars`). The apply does not return until the VM is what
   Coolify's validation expects: root login by the instance's key, and
   Docker with the compose plugin.
2. `ssh root@<control ip> tailscale up` once (I-86), then in the owner's
   Coolify: Servers, Add. Address the VM's tailnet IP, user `root`, port
   22, the private key whose public half is `coolify_public_key`. Validate
   & configure; Coolify starts its proxy on 80 and 443. Nothing of Coolify
   runs on the VM itself (I-83), so there is no admin account, no port 8000
   and no `.env` to copy off it.
3. Logto is the owner's `accounts.herakraft.co` (I-84). There: API resource
   `https://api.repose.herakraft.co`, applications `repose-cli` (Native,
   device flow on) and `repose-web` (SPA), and the M2M application
   `repose-api` (Management API role) for `LOGTO_M2M_CLIENT_ID/SECRET`.
4. Add Postgres as a Service (Docker Compose Empty, paste
   `ops/coolify/postgres/docker-compose.yml`, I-87), then `api`,
   `api-grpc` and `web` as Dockerfile applications, each with its
   `ops/coolify/*.env.example` pasted in ("Connect to predefined
   network" stays **off** for the Service: the compose file joins the
   `coolify` network itself, I-89; the apps are on it already). Domains,
   port mappings and the health-check settings: `ops/coolify/README.md`.
   Secrets go in each resource's Environment tab, nowhere else. **No
   pre-deploy command on any of them** — the api applies its own
   migrations and creates the CA at start, and a pre-deploy command
   would not run on the first deploy anyway (I-90). Backups are set up
   on the Postgres service's Backups tab and are the owner's own
   (I-112).
5. The applications deploy themselves: Coolify redeploys on every push
   to `main` (`coolify.md` fact 16). `repose-admin ca init` is not a
   step either — the api does it at first start (I-90) — and it remains
   for an operator.
6. Add the control VM as a WireGuard peer of the edge (`ops/wg/coolify.conf`).

### Edge

`make -C infra apply ENV=prod`; nixos-anywhere installs `.#edge` (its
production values are `nix/edge/edge-01.nix`). Every unit is conditioned on
its files under `/var/lib/repose/edge/` (0640 `root:repose-edge`), placed
once, in this order (DECISIONS I-92):

1. `wg.key`: `wg genkey | tee wg.key | wg pubkey > wg.pub` on the edge;
   the private key never leaves it. `systemctl start wireguard-wg0`.
2. `repose-admin edge init --endpoint <edge ip>:51820 --pubkey "$(cat
   wg.pub)"` in the api container (`docker exec` on the control VM):
   records the hub, so hosts registering from now on receive it.
3. `api-ca.pem`: `repose-admin ca show` (the x509 host CA certificate; the
   two SSH CA lines it prints as comments are informational).
4. `gateway.key` and `gateway.crt`, the mTLS client for `/internal`. The
   key is made on the edge and never leaves it: `openssl ecparam -name
   prime256v1 -genkey -noout -out gateway.key && openssl req -new -key
   gateway.key -subj /CN=gateway -out gateway.csr`, then `repose-admin ca
   sign-client --name gateway --csr /dev/stdin < gateway.csr >
   gateway.crt` (the CSR is public; pipe it through `docker exec -i`).
   The name must be `gateway`: `/internal` admits no other CN (I-431).
5. `ssh_host_ed25519_key` (`ssh-keygen -t ed25519 -N ''`) and its
   certificate `ssh_host_ed25519_key-cert.pub` from `repose-admin ca
   sign-host --principal ssh.repose.herakraft.co,<edge ip> --pubkey
   ssh_host_ed25519_key.pub`, so clients verify the gateway through the
   `@cert-authority` line the CLI writes.
6. `tls/edge-internal.key`, `.crt`: the same CSR dance with
   `repose-admin ca sign-server --name 10.255.0.1 --csr /dev/stdin` for
   the hook-ingest listener. `tls/wildcard.*` (the preview
   stub) waits on a DNS-validated wildcard certificate; without it the
   gateway logs `listener disabled` for `preview` and serves everything
   else.

Then `systemctl restart gateway wgsync` (the gateway's `Requires=`
starts `gateway-ssh.socket`, whose condition is now met) and verify `ssh -p 22
probe.nobody@ssh.repose.herakraft.co` returns `certificate required`, and
`wg show` on the edge lists the control plane's peer with a recent
handshake (`infra/README.md`, "Wiring the control plane to the edge").

### First host

See `../workstreams/11-infra-opentofu.md` §5 "Adding a host". Then
`repose-admin hosts list` shows `ready`, and `repose-admin hosts smoke
<host>` creates, starts, snapshots and destroys a throwaway guest.

### Observability

On the personal server, from this repository's `ops/` (its README has the
copy-paste version):

- `ops/prometheus/prometheus.yml` is the scrape config; hosts are listed in
  `ops/prometheus/targets/hosts.yml`, which Prometheus re-reads every minute,
  so adding a host needs no restart. Run Prometheus with
  `--storage.tsdb.retention.time=90d`.
- `ops/alerts.yaml` goes in its rule files, `ops/alertmanager/repose-route.yaml`
  into Alertmanager (ntfy to the owner).
- `ops/grafana/provisioning/` and `ops/dashboards/*.json` are Grafana's
  provisioning; the eight dashboards appear in a `repose` folder.
- `ops/loki/retention.yaml` sets 90 days for component logs and 30 for guest
  console logs, and needs the compactor enabled to do anything.
- The server joins the edge's WireGuard as one more peer:
  `ops/prometheus/wireguard-peer.conf`. Nothing is scraped over the internet.
- Locally, `docker compose -f ops/dev/docker-compose.yml up -d` is the same
  Grafana with the same dashboards and no data.

## Common operations

| Task | Command |
|---|---|
| List hosts with capacity | `repose-admin hosts list` |
| Add a host | `repose-admin hosts add --name host-NN` (prints a join token), then `make -C infra apply ENV=prod` (`infra/README.md` "Adding a host") |
| Drain a host (no new placements) | `repose-admin hosts drain host-NN` |
| Retire a host (after all projects moved) | `repose-admin hosts retire host-NN` |
| Move a project to another host | `repose-admin projects move <id> --to host-NN` (stop, snapshot, restore, start) |
| Suspend a user | `repose-admin users suspend <handle> --reason "..."` (stops guests, freezes billing, audit row) |
| Unsuspend | `repose-admin users unsuspend <handle>` |
| See the api's automatic abuse stops | `repose-admin abuse list [--all]` (project, owner, process name, hold) |
| Lift a project's abuse hold | `repose-admin abuse clear <id or slug.handle>` (clears its stops and strikes, audited as `abuse_clear`) |
| Run a command in a guest (audited) | `repose-admin exec <project-id> -- <argv>` |
| Force a snapshot | `repose-admin projects snapshot <id>` |
| Restore a snapshot | `repose-admin projects restore <id> --snapshot <sid> [--to host-NN]` |
| Revoke all certs for a user | `repose-admin certs revoke --user <handle>` |
| Rotate a host's mTLS cert | `repose-admin hosts rotate-cert host-NN` (the old certificate works until the new one connects; refused for a `lost` or `retired` host, I-432) |
| Rotate the Key Vault wrapping key | `az keyvault key rotate` then `repose-admin secrets rewrap` |
| Know when the name-only secret read path can go (I-433 step 3) | `secrets_name_only_none` in the api log of a release with I-474 (both apps, after a deploy); until it appears, `secrets_reseal` and `secrets_reseal_fail` show each pass; `secrets_reseal_incomplete` names how many rows Key Vault would not unwrap, which have to be repaired or deleted first (I-474) |
| Query audit log | `repose-admin audit --user <handle> --since 24h` |
| See the seats and the waitlist | `repose-admin seats` (total, held, free, waiting, source), `repose-admin waitlist list` (position, handle, joined, invited, hold, converted, expired, by) |
| Invite someone ahead of the queue | `repose-admin waitlist admit <handle>`, or `repose-admin waitlist admit --next N` for the next N; a 72-hour seat hold and one email each, audited `waitlist_admit` (I-269, I-290) |
| Publish a base version | `repose-admin base publish --rev <full 40-hex sha on main> --changelog "..." [--security]`; a short, unknown or off-main sha is refused (I-173); `--unverified-rev` skips only the GitHub check, for when GitHub is down |
| Smoke-test a host | `repose-admin hosts smoke host-NN` (create, snapshot, stop, start, destroy a throwaway guest) |
| Initialise the CAs (once) | `repose-admin ca init`; then `repose-admin ca sign-client --name gateway --out <dir>` for the edge |
| Record the edge WireGuard hub | `repose-admin edge init --endpoint <ip>:51820 --pubkey <wg pub> [--out <dir>]` |
| Re-run the hourly rollup | `repose-admin billing rollup --hour 2026-09-17T14` |
| Database migration | `repose-admin db migrate` / `repose-admin db rollback --to NNNN` |

## HostMemory80

Reserved memory on a host is above 80 percent.

1. `repose-admin hosts list`: confirm which host and how many guests are
   `stopped` (stopped guests hold no memory reservation; if the number is
   wrong, the api's reservation accounting drifted, see "Reservation
   drift").
2. Add a host (workstream 11 §5). Until it is `ready`, the scheduler still
   places on the full host; drain it if placements must stop now.
3. Checkouts are already being held once every 8 GB seat of the fleet is
   taken ("Waitlist growing"); memory at 80 percent on one host says
   nothing about seats, which count the whole fleet.

## Waitlist growing

`repose_api_waitlist_waiting` above zero, or `repose-admin waitlist list`
shows people waiting (DECISIONS I-269, I-290). A checkout needs the plan's
seats free; a seat is 8 GB running at once, the fleet has as many as its
`ready`, undrained hosts have usable 8 GB blocks (or `SEATS_TOTAL`), and
live subscriptions and unexpired invitations hold them. `repose-admin
seats` prints total, held, free, waiting and where the total came from.

1. Add a host (workstream 11 §5), or raise `SEATS_TOTAL` on both api apps
   and redeploy if the number was the limit rather than the hosts. Nothing
   else: once a seat is free, the api's minute tick invites the oldest
   waiting user, holds the seat 72 hours for them and emails them to
   choose a plan; `waitlist_invite` in the api log (grpc app) carries the
   count. A hold that runs out moves the user to the back
   (`waitlist_expire`) and the seat goes to the next one.
2. To see the queue: `repose-admin waitlist list` (position, handle,
   joined, invited, hold, converted, expired invites, by). `GET
   /public/seats` is the same count the landing page shows.
3. To let one person in now (a tester, someone who wrote in):
   `repose-admin waitlist admit <handle>` (or `admit --next N`). They get
   the invitation email and a 72-hour hold like an automatic invitation,
   which holds a seat even when none is free; the next automatic
   invitation waits for it.
4. There is no off switch. A launch with hosts to spare sets `SEATS_TOTAL`
   high enough that nobody is refused; `0` derives the count from the
   hosts. With no ready host at all the count is zero and every checkout
   waitlists: that is an outage, fix the hosts.

## Temporary machine not destroyed

A project made with `repose run --temp` is past its `expires_at` and
still there (DECISIONS I-347, I-350). The api's minute tick (grpc app,
under advisory lock 1007) destroys it; `temp_reap` in the api log carries
the counts, `temp_expire` names each project it enqueued, and
`temp_reap_fail` is a failed run.

1. `select id, slug, state, expires_at from projects where expires_at <
   now() and destroyed_at is null;` lists the overdue ones.
2. Waiting is normal for up to 24 hours past `expires_at` while the
   newest meter sample (under 10 minutes old) shows an ssh session, a tmux
   client or an agent working. From then it goes regardless.
3. A destroy that failed leaves the project in `error` with
   `destroy_failed`; the reaper tries again 10 minutes after each failure.
   Fix the host cause as in "Snapshot, stop or destroy slow" below.
4. Nothing enqueued at all and no `temp_reap_fail`: no replica holds lock
   1007 (`select * from pg_locks where locktype = 'advisory' and objid =
   1007;`), which means the grpc app is not running its loops.
5. The owner can always `repose rm NAME` or `repose keep NAME` it.

## HostUnreachable

No heartbeat for 90 seconds.

1. From the operator machine: `ping 10.255.0.<host>` over WireGuard. If
   that fails too, check the Azure portal for the VM state; a platform
   repair event shows there.
2. If the VM is up: `ssh root@10.255.0.<host>`, `systemctl status hostd`,
   `journalctl -u hostd -n 200`. A `stream_disconnect` loop with TLS errors
   means the host cert expired: `repose-admin hosts rotate-cert` from the
   api side writes a new one via the edge jump. `PermissionDenied` with
   "not the host's current one" in hostd's log means the host holds a
   certificate the api has superseded (I-432); the same command fixes it.
3. If the VM is gone: "Host loss" below.
4. Guests keep running through an api outage; the only thing lost is
   metering samples for the window, which the rollup marks as `gap` rather
   than zero.

## SnapshotStale

A running project's newest snapshot is older than 36 hours.

1. `repose-admin projects show <id>`: last snapshot, last error.
2. `journalctl -u hostd | grep snapshot_fail` on the host. Common causes:
   `freeze_timeout` (guestd hung; see "Guest unresponsive"), Blob auth
   (managed identity lost its role: `az role assignment list`), pool out of
   metadata space (`lvs -a`; extend the pool metadata).
3. `repose-admin projects snapshot <id>` after fixing; confirm the age
   gauge drops.

## Snapshot, stop or destroy slow

A stop, destroy or nightly snapshot takes tens of seconds for a volume
that holds little. On the host, `journalctl -t hostd | grep '"snapshot
done"'`: the line carries `format`, `raw_reason`, `used_bytes`,
`volume_bytes` and `duration_ms` (DECISIONS I-164).

- `format: extents`: time should follow `used_bytes` (reading) and
  `bytes` (the upload). Slow with little data means the disk or Blob is
  slow; `iostat -x 1` during the next one. `read_wait_ms` is how long the
  stream waited for the device and `freeze_ms` the freeze, LVM snapshot
  and thaw (I-571): with `read_wait_ms` close to `duration_ms` the disk set the pace (another
  guest's writes share it); well under it, zstd or the upload did.
  I-571's probe read and compressed 900 MB/s on host-01; before it,
  stops ran at 370 to 530 MB/s of `used_bytes`.
- A stop also logs `"guest stopped"` (`power_off_ms`, `escalated`:
  `none`, `hypervisor` when the guest ignored the shutdown for the
  timeout, `kill`) and, with a snapshot, `"stop timings"` (`down_ms` from
  the freeze to the guest being down, `total_ms`). A `power_off_ms` past
  10 s is the guest's user manager waiting on a pane process (I-572);
  the guest's `console.log` shows `A stop job is running for User
  Manager for UID 1000`.
- `format: raw` reads the whole volume (about 16 s per 20 GB on host-01).
  `raw_reason` says why: `journal needs recovery` (the guest was killed,
  not shut down, or, for a running guest, its freeze did not hold until
  the LVM snapshot: guestd's 10 s watchdog thawed first; the next
  snapshot goes back to extents; I-171), `dumpe2fs
  failed (not ext4?)` (someone reformatted the volume), `N of M groups
  listed` (dumpe2fs output cut short: check the host's e2fsprogs). A
  hostd older than I-164 has no `format` field and always reads raw.

A destroy the user no longer waits for (I-166) that fails leaves the
project in `error` with a `destroy_failed` event; `repose-admin projects
show <id>` has the op error, and `repose rm` (or the admin's
destroy) resumes it.

## BuildQueueStuck

Two builds running on a host with no completion for 45 minutes.

1. `repose-admin ops list --host host-NN --state running`.
2. On the host: `ps aux | grep nix-build`; `journalctl -u hostd | grep
   build_`. A build past its 30-minute cap should have been killed by
   hostd; if it is still running, hostd's limiter failed: `kill` it,
   restart hostd, open an issue.
3. If both builds are legitimately slow (big closures, cold cache), wait;
   the alert clears on completion. Consider the central builder (deferred).

## GatewayAuthSpike

More than one auth failure per second at the gateway.

1. Grafana gateway dashboard: failures by `reason`. `no_cert` or `bad_ca`
   in volume from one source is a scan; the gateway rate-limits per source
   IP after 20 failures (fail2ban-style, built in) and refuses further auth
   for 10 minutes. Nothing to do unless it persists for hours; then add the
   source to the edge NSG deny list. `route_error` in volume means the
   gateway cannot reach the api (see "Gateway relay failures").
2. `expired` in volume means the CLI's silent refresh is broken for many
   users: check `repose_api_certs_issued_total` fell off a cliff, and the
   Logto token endpoint.
3. `wrong_principal` from one user repeatedly is someone probing other
   projects; `repose-admin audit --user`.

## Gateway relay failures

The failure modes of the SSH gateway (docs/workstreams/06-gateway-edge.md
§6), and the message the user sees for each. The gateway is on the edge;
reach it with `ssh -p <edge_operator_ssh_port> root@<edge ip>` and read
`journalctl -u gateway` (events are structured JSON: `auth_fail`,
`route_fail`, `dial_fail`, `session_open`, `session_close`).

| Symptom / user message | Cause | What to do |
|---|---|---|
| `gateway cannot reach control plane; try again shortly` | the api is down or the CA/revocation caches aged past 1 h | `journalctl -u gateway \| grep route_fail`; check the api and `api-grpc` app; existing sessions keep working, new ones resume when a refresh succeeds |
| `environment is not accepting connections yet` | the guest's sshd is not up yet, or dialed a throwaway host key | the CLI retries 60 s after a `start`; if it persists, `repose-admin projects show` for the guest state, then "Guest not ready" |
| `cannot reach environment: no route to host` | no WireGuard peer or route for the guest's host | on the edge `wg show wg0` and `ip route \| grep <guest cidr>`; `wgsync` adds them from `/internal/hosts` within 30 s — see "HostWgDown" |
| `permission denied (certificate expired)` / `... not yet valid` | the user's certificate is outside its 24 h validity | the CLI refreshes and retries once; a spike of `expired` is "GatewayAuthSpike" step 2 |
| `permission denied (certificate revoked)` | logout or a revoked serial | expected; takes effect within 30 s of `repose logout` |
| `certificate not valid for this project` | the certificate's principals do not contain the resolved project id, or the login names another user's handle (no lookup is made, so this says nothing about whether that project exists; I-437) | the CLI re-requests a cert for the project; repeated from one user is probing ("GatewayAuthSpike" step 3) |
| `<slug> is stopped; run \`repose start\`` | the project is stopped | expected; the user starts it |
| `gateway busy` | the 200-relay cap is reached (on stderr, exit 255), or 512 connections are already in the handshake (one plain line before the handshake; `ssh -v` shows it) | alert on `repose_gateway_sessions`; if legitimate, the edge is undersized |
| `too many open connections for your account; close some and try again` | one user holds 32 relays | usually a script that leaks connections; the user closes them |
| `too many authentication attempts from your address; try again later` | 4 connections in the handshake or 20 failures from one source (an IPv6 /64 counts as one); sent as one plain line before the handshake, so the user sees `kex_exchange_identification: Connection closed by remote host` | a scan; the ban clears in 10 min. The edge's nftables also caps a source at 64 open and 20 new connections a second on 22 (I-435) |
| `login name must be <project>.<user>` | a malformed SSH login name | the user's SSH config is wrong; `repose run` rewrites it |

A relay ends when its certificate is revoked or expires (I-436):
`session_close` with `reason` `revoked` or `cert_expired` is expected
after a `repose logout` or a day-old certificate.

A switch hands over without dropping relays; a gateway restart or an
edge reboot drops every relay, the guest's tmux session survives, and
`run`, `attach` and `open` reconnect on their own ("Switch the edge").
`nixos-rebuild switch --rollback` on the edge restores the previous
gateway in seconds, and `wgsync` rebuilds the peer set within 30 s.

## EgressHigh

A project moved more than 1 TB in 24 hours.

1. Abuse dashboard: the project's `proc_samples` top `comm`. A torrent
   client, a scraper, or a miner's pool traffic looks different from
   `docker pull`.
2. If legitimate, nothing (it is metered and billed). If not:
   `repose-admin users suspend`.

## EgressBlocked

A guest keeps sending into one of the host's egress blocks (DECISIONS
I-238..I-240); the `reason` label says which: `smtp` (over 100 packets to
tcp 25 in ten minutes), `stratum` (over 30 to a mining-pool port) or
`flows` (over 1000 new flows past the per-guest rate). The traffic is
already dropped; nothing is stopped.

1. Which guest: `{component="hostd", host_id="<host>"} | json |
   event="egress_blocked"` in Loki names `guest_id` and `reason`;
   `repose-admin projects show <guest id's project>` (or `projects list
   --host`) gives the project and owner. On the host, `nft list counters
   table inet repose | grep -A1 <reason>-<guest id>` is the running count.
2. What it runs: the Abuse dashboard's top process names, then
   `repose-admin exec <id> -- ps -o comm,pcpu --sort -pcpu` (audited).
   An app retrying a mail send to port 25 is a user to tell about 587;
   `stratum` with a CPU-bound process is a miner under another name;
   `flows` with a scanner (masscan, zmap, nmap) is a scan.
3. Decide on the account: `repose-admin users suspend <handle> --reason
   "..."` stops every guest with a snapshot. Nothing here is automatic.

## SamplesFailing

The api could not store a guest's sample row as hostd sent it (DECISIONS
I-446). `guest_fields`: the guest-reported part (agents, process names)
was refused and the row was stored with the host-measured fields only, so
metering is intact. `insert`: nothing was stored for that guest's minute,
which the rollup records as a gap.

1. Loki `{component="api"} | json | event="samples_fail"` gives the host,
   the project and the Postgres error (an SQLSTATE such as 22021 is a
   value Postgres refused).
2. `guest_fields` from one project only: hostd and the api both clean these
   fields, so a value that still fails is a cleaning gap to fix in
   `internal/hostd/guest/guestinput.go` and `internal/api/meter`.
3. `insert` across many projects: Postgres itself (partitions, disk,
   connections); see the api's other errors at the same time.

## HostReportsRefused

The api dropped a host's report (DECISIONS I-447). `foreign_guest`: an
event, a `Hello` entry or a sample named a guest whose project is placed on
another host or on none. `bad_snapshot`: a `snapshot_done` path outside the
project's prefix. hostd sends neither.

1. Loki `{component="api"} | json | event="foreign_guest"` gives `host_id`
   and `guest_id`; `repose-admin projects list --host <host>` and the
   project's `host_id` show where the api thinks it is.
2. One line right after a failed placement or a move is a late report from
   the old host for a guest it has just let go; nothing to do.
3. A steady stream from one host is a host to take out of placement
   (`repose-admin hosts drain`) and look at, starting with its hostd
   version and `journalctl -u hostd`.

## MinerStopped

The api stopped a guest because its process sample named a cryptocurrency
miner (I-239): the stop op took a snapshot, the user got an
`abuse_stopped` notification and `repose status` says why.

1. `repose-admin abuse list`: project, owner, process name, and `hold`
   when this was the third stop in 24 hours (starts are refused).
2. A false positive (a tool that happens to share a miner's name) is
   `repose-admin abuse clear <project>` and a note to the user; add the
   name out of `internal/abuse` (or make it exact-only) if it is a real tool.
3. A real miner: `repose-admin users suspend <handle> --reason "mining"`.
   The api never suspends by itself.

## BusyUnattended

A project ran every vCPU at 90 percent or more for six hours with no SSH
session, no tmux client and no agent in any sample (I-239). Nothing is
stopped: a runaway build or test loop looks the same, and a miner under
another name does too.

1. The Abuse dashboard's "Guests at full CPU" panel names it.
2. `repose-admin exec <id> -- ps -o comm,pcpu --sort -pcpu | head`
   (audited). A compiler or test runner that never ends is the user's to
   hear about; an unknown name using every core is a miner.
3. A miner: `repose-admin projects stop <id>`, then decide on
   `repose-admin users suspend`.

## PoolFull

Thin pool under 10 percent free.

1. `lvs vg-guests`: data percent and metadata percent. Metadata full is
   worse (writes fail across every volume): `lvextend --poolmetadatasize`.
2. Extend the data disk in Azure (`tofu apply` with a larger
   `data_disk_gb`, online for Premium SSD v2), then `pvresize` and
   `lvextend -l +100%FREE vg-guests/thin`.
3. Find who: `repose-admin projects list --host host-NN --sort disk`.
   Over-allocated thin volumes are fine; used space is what matters.

## StoreFull

Host root filesystem over 85 percent, almost always `/nix/store`.

1. `nix path-info -S --all | sort -k2 -n | tail`: biggest closures.
   hostd keeps a GC root per live guest closure; everything else is
   collectable.
2. `nix-collect-garbage` (hostd runs it weekly; run it now). If still
   full, a tenant's fragment pulled something huge: `repose-admin
   projects list --host host-NN --sort closure` and talk to them, or
   lower the closure cap.

## GuestdLost

hostd cannot talk to a guest's guestd for 5 minutes.

1. `repose-admin projects show <id>`: is the guest `running`? If the
   guest crashed, the transient unit is gone: `repose-admin projects
   start <id>`.
2. If running: the guest's console log at
   `/var/lib/repose/guests/<id>/console.log`. An OOM in the guest (the
   tenant filled memory) usually killed guestd; the kernel line names it.
   `repose-admin projects restart <id>` (stop without snapshot, since
   freeze needs guestd, then start).
3. Sampling for that guest is missing for the window; billing uses the
   last known state, so a running guest is still billed.

## HostScrapeDown

Prometheus cannot scrape a host: `up{job=~"hosts|hostd"} == 0` for 5 minutes,
and that host's panels on Host capacity go blank. Metering is *not* affected:
hostd sends samples to the api over its own gRPC stream, so billing data
keeps arriving (docs/workstreams/10-observability.md §6).

1. From the monitoring server: `curl -s http://<host wg addr>:9101/metrics |
   head -1`. A timeout is the tunnel, a connection refused is hostd.
2. Tunnel: "HostWgDown" above. The scrape and the logs use the same path, so
   a FluentBitStuck alert for the same host confirms it.
3. hostd itself: on the host, `systemctl status hostd` and
   `ss -tlnp | grep 9101`. hostd binds the WireGuard address, so a hostd that
   started before wg0 existed still listens (`ip_nonlocal_bind`); if it does
   not, `systemctl restart hostd`.
4. nftables: `nft list chain inet repose input` must admit 9100, 9101 and the
   Fluent Bit metrics port from `wg0`.
5. Nothing to do about the gap: Prometheus has no backfill. Say so in the
   incident note rather than wondering later why a graph has a hole.

If *every* target is down at once, including the edge's and the api's, it
is not the hosts: see "Every Prometheus target is down and WireGuard looks
fine" below.

## Every Prometheus target is down and WireGuard looks fine

`up == 0` for every job the moment monitoring is first wired up, or right
after the edge is rebuilt. `wg show` on the monitoring server shows a
recent handshake with the edge, `ping 10.255.0.1` answers, and every
scrape still times out.

The edge is a hub, not a bridge. Its `forward` chain is policy drop with
`ct state established,related accept` and nothing else, on purpose: hosts
must not reach one another or the api's gRPC listener through it. A scrape
from the monitoring peer to a host, or to the control plane, is exactly
that forwarded traffic, and so is Fluent Bit's push to Loki in the other
direction (DECISIONS I-94).

1. The ping answering proves nothing: the edge's own address is `input`,
   not `forward`. The test that distinguishes them is a scrape of the edge
   itself against a scrape of anything behind it —
   `curl http://10.255.0.1:9102/metrics` working while
   `curl http://<host wg addr>:9101/metrics` hangs is this entry.
2. On the edge, `nft list chain inet repose-edge forward`. Two rules
   naming the monitoring peer's address should be there; if the chain is
   just the `ct state` accept and a drop, the edge was built without
   `repose.edge.monitoring.peerCIDRs` set.
3. Fix it in `nix/edge/edge-01.nix`, not with `nft add rule`: a live rule
   is gone at the next `nixos-rebuild`, and the peer itself must be in
   `staticPeers` too or `wgsync` removes it within 30 seconds.
   `ops/prometheus/wireguard-peer.conf` has all three parts.
4. Until the rebuild, an operator with the edge's SSH key can see the same
   numbers through SSH forwards: `ops/dev/tunnel-prod.sh <edge> <control>`
   plus the local stack with `ops/dev/prometheus-prod.yml`.

## FluentBitLogShipperDown

`up{job="fluent-bit"} == 0` for a host, for 15 minutes: Prometheus cannot
reach that host's Fluent Bit at all. Its journald and every guest's
console log are going nowhere, and unlike "FluentBitStuck" nothing is
buffering them for later — a shipper that is not running is not
collecting either.

This alert exists because the other one cannot see this case.
`FluentBitStuck` reads `fluentbit_output_retries_failed_total`, which a
process that is not running does not publish, so the loudest failure was
the quietest signal.

1. **Has a Loki been recorded at all?** `docker exec <api container>
   /usr/local/bin/repose-admin edge loki`. "no Loki recorded" is the
   answer on a fresh platform, and it is the cause: the unit has an
   `ExecCondition` that refuses to start it with an empty `LOKI_HOST`
   rather than retrying a connection to nothing for ever (DECISIONS
   I-95). Fix it centrally — `repose-admin edge loki http://<loki>:3100`
   — and the host picks it up at its next `Rotate`, or immediately with
   an edit of `host.json` and `systemctl restart repose-host-net`.
2. If a Loki *is* recorded, the host has not received it: `grep LOKI
   /run/repose/host.env` on the host. Empty means the value post-dates
   its registration; same fix as above, the immediate half.
3. With `LOKI_HOST` set and the unit still down, it is the unit:
   `systemctl status fluent-bit` and `journalctl -u fluent-bit -n 50`.
   `ConditionPathExists=/run/repose/host.env` unmet means the host never
   registered ("HostUnregistered").
4. If the unit is running and only the *scrape* fails, this is the
   tunnel rather than the shipper: "HostScrapeDown", and check the
   edge's forward chain ("Every Prometheus target is down and WireGuard
   looks fine").

Nothing in a guest waits on log shipping, so tenants are unaffected
throughout; what is lost is the operator's view, and the logs for the
window are lost rather than delayed.

**The reference case**, host-01, 2026-09-21, which is what step 1 looks
like when it is the answer:

```
systemctl status fluent-bit
  inactive (dead) (Result: exec-condition)
  ExecCondition=/nix/store/…-repose-fluent-bit-has-loki (code=exited, status=1)
  "Started with unmet condition" / "Skipped due to 'exec-condition'"
grep LOKI /run/repose/host.env
  LOKI_HOST=          # present and empty
```

The journal is the useful half. Fluent Bit **ran for 6h 1min before the
host switch at 00:16:43Z** and was refused on the restart at 00:16:44Z
— because those six hours were spent retrying an empty target, shipping
nothing, and looking in the journal exactly like a Loki that was down.
That is the failure I-95 exists to convert into a refusal with a reason,
and `FluentBitLogShipperDown` exists so the refusal is not itself
silent. A host in this state is correct, not broken; it is waiting for
`repose-admin edge loki <url>`, which is the owner's step because the
Loki is theirs.

## FluentBitStuck

A host's Fluent Bit has been failing to ship to Loki for 30 minutes
(`increase(fluentbit_output_retries_failed_total[30m]) > 0`). It buffers to
disk and retries forever, so nothing is lost yet; at 1 GB the oldest chunks
are dropped.

1. Is Loki up? `curl -s http://<loki>:3100/ready` from the monitoring server.
   If Loki is the problem, every host alerts at once. Every host alerting at
   once *and* every Prometheus target down is the edge's forward chain
   instead: "Every Prometheus target is down and WireGuard looks fine".
2. On the host: `systemctl status fluent-bit`, `journalctl -u fluent-bit -n
   50`. `ConditionPathExists=/run/repose/host.env` unmet means the host never
   registered ("HostUnregistered"); the unit is `partOf`
   `repose-host-net.service`, so `systemctl restart repose-host-net` restarts
   it with freshly rendered addresses.
3. Buffer size: `du -sh /var/lib/fluent-bit/storage`. Approaching 1 GB is the
   deadline for fixing Loki before lines are dropped.
4. Wrong or missing Loki address: `grep LOKI /run/repose/host.env`. It
   comes from `loki_url` in `host.json`, which the api sends at
   registration from the `loki_url` setting. An **empty** `LOKI_HOST` is
   not a failure of this alert but of its precondition: the unit refuses
   to start and says so (`systemctl status fluent-bit`), because no Loki
   has been recorded. Fix it centrally with `repose-admin edge loki
   http://<loki>:3100` — a host picks it up at its next `Rotate`, or
   immediately by editing `host.json` and `systemctl restart
   repose-host-net` (DECISIONS I-97).
5. Guests are unaffected throughout: nothing in a guest waits on log
   shipping.

## Guestd not ready

A guest is `starting` and never reaches `running`, or the api shows
`guestd_ok=false` from the first sample. guestd sends `Ready` only once sshd
is listening, so "not ready" means guestd did not start, or sshd did not.

1. Console log at `/var/lib/repose/guests/<id>/console.log`. The line to look
   for is guestd's own: `{"event":"ready","msg":"listening",...}` with
   `"transport":"vsock"`. If it is absent, guestd did not start; the lines
   above it say why (a missing `/run/repose`, a vsock device the runner did
   not attach).
2. If guestd is listening but no `Ready` followed, sshd is the one that did
   not come up: the same console log has sshd's error. The usual cause is
   sshd material that never arrived, so `/run/repose/ssh_host_ed25519_key` is
   missing; `repose-admin projects restart <id>` re-sends `CreateGuest`'s
   secrets.
3. From the host, talk to the guest directly:
   `repose-admin exec <id> -- guestd call ping --cid <vsock cid>`. A response
   means guestd is fine and the problem is on hostd's side of the vsock; no
   response with guestd listening means the CID is wrong in hostd's state.
4. The guest keeps running through all of this. A tenant with a certificate
   can still SSH in; what is lost is sampling, secrets delivery and config
   apply.

## Freeze timeout

A snapshot failed with `freeze_timeout`, or the alert fired from a
`host_warning` of that kind.

1. This is guestd's watchdog doing its job: it froze the root filesystem for a
   snapshot, no `Thaw` arrived within 10 seconds, and it thawed itself. The
   guest is *not* wedged; nothing needs to be unfrozen by hand.
2. The cause is on the host side: hostd died mid-snapshot, or the LVM
   snapshot took longer than the window. `journalctl -u hostd | grep
   snapshot` on the host gives which.
3. If the thin pool is near full, the LVM snapshot is what was slow: see
   "PoolFull" above, then `repose-admin projects snapshot <id>` again.
4. If it repeats for one project only, the guest's root filesystem has a
   writer that will not quiesce (a database in a container). Stop the guest
   and snapshot from stopped: `repose-admin projects restart <id>` takes the
   snapshot on the way through.
5. To confirm the guest is healthy afterwards:
   `repose-admin exec <id> -- guestd call ping` and check `df` inside.

## herdr_down

A `host_warning{kind="herdr_down"}` (DECISIONS I-507) names a guest whose
`project.json` says herdr, and whose herdr socket
(`/home/dev/.config/herdr/herdr.sock`) refused guestd, or answered a
protocol below 22, on two 5 s refreshes in a row. The machine runs; its
agents drop out of `signals.agents` until herdr answers again.

1. `repose-admin exec <id> -- systemctl --machine=dev@ --user is-active
   repose-herdr-server` says whether the server unit runs. Failed: the
   unit's journal (`journalctl --machine=dev@ --user -u
   repose-herdr-server`) has the reason.
2. Active, yet the warning repeats: the server answering is not the
   base's pinned herdr (a tenant's `herdr update` in the guest is the known
   way). The tenant fixes it inside the guest; nothing on the host needs
   changing.
3. A host on a hostd from before I-507 reports this kind as `guest_other`.

## Switch a host to main

Done from a clean checkout of the pushed main commit, with the owner's word
for this switch (`docs/ops/RELEASE.md`). The host only trusts signed paths,
so the derivation is copied and built there:

1. `drv=$(nix eval --raw ./nix#nixosConfigurations.host-01.config.system.build.toplevel.drvPath)`
2. `nix copy --derivation --to ssh-ng://host-01 "$drv"`
3. `out=$(ssh host-01 "nix build --no-link --print-out-paths '$drv^*'")`
4. Read `ssh host-01 nix store diff-closures /run/current-system $out` and
   `ssh host-01 $out/bin/switch-to-configuration dry-activate`: what
   restarts. A change to hostd alone restarts hostd; guests keep running.
5. `ssh host-01 "nix-env -p /nix/var/nix/profiles/system --set $out && $out/bin/switch-to-configuration switch"`
6. Guests whose virtiofsd shares the whole host store (started before
   DECISIONS I-463) are restarted at the switch, after telling their users:
   `ssh host-01 hostd guests` lists them with `whole-store` in the STORE
   column, and each one gets `repose-admin projects restart <project id>`.
   Run `hostd guests` again until no line says `whole-store`; a reconcile
   logs `store_view_restart_needed` for any that was missed. The same
   restart moves a user's login share onto its own volume (I-464).

A push to main redeploys the api, which drops hostd's stream for a few
seconds; a stop or restore timed across a push looks slow.

## Switch the edge

With the owner's word for this switch (`docs/ops/RELEASE.md`), from a
clean checkout of the pushed main commit:

1. `nix build ./nix#nixosConfigurations.edge.config.system.build.toplevel`
   builds here; then `NIX_SSHOPTS="-p 2222" nixos-rebuild dry-activate
   --flake ./nix#edge --target-host root@<edge ip>` says what it would
   restart, reload and start.
2. The same with `switch`.

What a switch does to users (DECISIONS I-470..I-472): `gateway` and
`wireguard-wg0` are reloaded, not restarted. The reload of `gateway` is a
handover: the new build takes port 22 and every new connection, and the
old process serves its open relays until each ends (at most 24 hours).
Nobody's terminal, editor or forward drops. Check it:

- `journalctl -u gateway -n 30` shows `handover starting`, `handed over`
  (with the new `pid`) and `gateway draining` with the old process's
  `relays`; `systemctl show -p MainPID gateway` is the new pid.
  `systemctl status gateway` lists both processes while the old one
  drains. systemd's line `Supervising process N which is not our child`
  is expected; it does notice that process ending (I-471).
- `wg show wg0 peers | wc -l` is the same before and after.
- `ssh -p 22 probe.nobody@ssh.repose.herakraft.co` answers `certificate
  required`.

A failed handover fails the switch with the gateway's reason (`the
running gateway refused the handover and serves on: ...`); the old
gateway still serves. Fix the build and switch again, or restart (below).

When sessions must end, which drops every one of them (users' `run`,
`attach`, `open` and editors reconnect, I-469):

- **The first switch onto I-471.** The running gateway has no control
  socket, so the reload fails (`no gateway answers on
  /run/repose-gateway/control.sock`), and the new `gateway-ssh.socket`
  cannot bind 22 while the old gateway holds it. Announce it, then
  `systemctl stop gateway && systemctl start gateway-ssh.socket gateway`.
  Every later switch is a handover.
- **A fix in relay code that open connections must get.** A handover
  leaves them on the old code. `systemctl restart gateway`; connections
  made during the restart wait in the socket and are served.
- **A change to `gateway-ssh.socket`** (its port, backlog). A switch never
  restarts a socket unit: `systemctl restart gateway-ssh.socket gateway`.
- **A change to the gateway's sandbox** (`DynamicUser`, capabilities,
  paths): a handover starts the new build inside the old process's
  sandbox. `systemctl restart gateway`.
- **wg0's address or key file path removed or changed.** The reload adds
  a new address beside the old and never removes one:
  `systemctl restart wireguard-wg0 wgsync` (wgsync re-adds host peers at
  once on its start, otherwise within 30 s).
- **A kernel update** needs a reboot, which ends every connection until
  the edge is back (about a minute). Announce it and pick a quiet hour;
  the edge is one VM (I-473).

## Switch failed

`repose config apply` reported a failed revision, or a base bump left a
project on the old system.

1. The Nix output is stored with the revision: `repose-admin ops list
   --project <id>` then `ops log <op id>`. The tail of it is the reason; it
   is `switch-to-configuration`'s own output, verbatim.
2. The old system is still active and the guest is still running. This is
   switch-to-configuration's semantics and guestd relies on it: a failed
   switch changes nothing.
3. The common causes, in order: a systemd unit in the user's fragment that
   fails to start (the output names it), a store path missing from the share
   (guestd also sends `store_path_missing`; see "StoreFull" for why a path
   disappears, then `repose-admin projects restart <id>` to rebuild and
   re-register the GC root), and a closure that needs a reboot
   (`needs_reboot=true` is not a failure: the user is told to run `repose
   config apply --reboot` when their agent is idle).
4. To retry by hand once the fragment is fixed: `repose config apply` again.
   Nothing needs cleaning up first.

## RollupLag

The hourly usage rollup is more than 2 hours behind.

1. `journalctl` on the api container for `rollup_` events. A failing hour
   is retried; a poisoned row (a sample with a negative delta from a
   hostd restart) is skipped and logged with the project id.
2. `repose-admin billing rollup --hour <hour>` to re-run one hour.

## PartitionDropFail

The hourly `repose_partitions_maintain()` is failing: `meter_samples` and
`proc_samples` keep partitions past their 90 and 30 day retention, so
Postgres grows. Nothing else breaks and no data is lost
(docs/workstreams/10-observability.md §6).

1. The api's log says why: `{component="api"} | json | event="partition_drop_fail"`.
2. By hand, as the api's role: `select * from repose_partitions_maintain();`
   It prints one row per create and drop. A permission error means the role
   cannot `drop table`; a lock timeout means something is reading a partition
   it wants to drop, and the next hour will get it.
3. Space now, if that is the pressure:
   `select relname, pg_size_pretty(pg_total_relation_size(oid)) from pg_class
   where relname like 'proc_samples_%' order by relname;` then
   `drop table proc_samples_YYYYMM` for a month wholly past retention.
4. If the *create* half failed, inserts for the new month will fail at 00:00
   on the first: `select repose_partition_create('meter_samples',
   date_trunc('month', now())::date);` is the fix, and hostd's sample buffer
   holds what did not land (workstream 03).

## HostUnregistered (host never registered)

A new host has been up for more than five minutes and is not in `hosts
list`.

1. `ssh -J root@<edge ip>:2222 root@<private ip>`: `journalctl -u hostd`.
   `token_expired` or `token_used`: mint a new one (`repose-admin hosts
   add --name <name> --reissue`), put it in `infra/azure/prod/prod.local.tfvars` and
   `make -C infra apply ENV=prod` (only the token-delivery step re-runs), or
   by hand `install -d -m 0755 /run/repose && umask 077 && cat >
   /run/repose/join-token` and `systemctl restart hostd`. The token is never
   passed as a command-line argument, so it does not land in a shell history
   or an apply log.
2. `kvm_missing`: the VM was created without `security_type = Standard`.
   Destroy and recreate; there is no in-place fix. The apply should not have
   got this far: the host module's post-install check fails when `/dev/kvm`
   is missing, and the tfsec rule REPOSE-VM-001 fails the build when Secure
   Boot is set.
3. `pool_missing`: the data disk is not attached or `disko` did not run.
   `lsblk` and `ls -l /dev/disk/azure/scsi1/lun10`; re-run nixos-anywhere if
   the layout is missing.
4. Nothing in the journal at all and SSH refused: the install may have
   stopped mid-kexec. `az vm boot-diagnostics get-boot-log --name <host> -g
   repose-prod` shows the serial console; `make -C infra apply ENV=prod`
   after `tofu -chdir=infra/azure/prod taint
   'module.environment.module.host["<host>"].module.install.terraform_data.install'`
   retries the install from the image. The data disk is a separate resource
   with `prevent_destroy`, so it is not touched.

## PoolHigh (thin pool at 80 percent)

`host_warning{kind="pool_high"}` from hostd, or
`repose_lvm_pool_data_percent > 80` from the host's textfile collector.
lvm.conf autoextends the pool by 10 percent of its size into the 5 percent
VG headroom at this threshold, once; after that the pool fills for real.

1. `lvs vg-guests` on the host: data and metadata percent, and whether
   `thin` still has room to grow (`vgs vg-guests` free space). Metadata
   over 80 percent is worse; see "PoolFull".
2. Plan the disk grow now: `tofu apply` with a larger `data_disk_gb`
   (online for Premium SSD v2), then on the host `pvresize <pv>` and
   `lvextend -l +95%FREE vg-guests/thin`. At 90 percent hostd refuses
   `CreateGuest` and `Restore` with `insufficient_capacity`; existing
   guests keep running.
3. `repose-admin projects list --host host-NN --sort disk` for who is
   using it; `repose-admin hosts drain host-NN` if the grow cannot happen
   before it fills.

## StoreHigh (store at 80 percent)

`host_warning{kind="store_high"}` from hostd; the root filesystem is over
80 percent, almost always `/nix/store`. `nix.settings.min-free` (50 GB)
already triggers GC of unrooted paths during builds and hostd refuses
`Build` with `insufficient_capacity: host store full` at this point.

1. `nix-collect-garbage` on the host now (the weekly timer keeps 14 days).
   hostd's GC roots under `/nix/var/nix/gcroots/repose/` protect every
   guest's closure and the last three revisions per project; everything
   else goes.
2. Still high: `nix path-info -S --all | sort -k2 -n | tail` for the
   biggest closures and `repose-admin projects list --host host-NN --sort
   closure`; a tenant near the 20 GB closure cap on a small host is the
   usual cause. See "StoreFull" for the 85 percent alert.

## Host never configured its bridge (registration ran, br-guests has no address)

`hostd status` says registered and `stream_connected`, but `ip addr show
br-guests` has no `10.64.x.1` address, `/run/repose/host.env` is missing,
and node_exporter or Fluent Bit are inactive. `repose-host-net` renders
those from `host.json` and is restarted by `repose-register.service`'s
ExecStartPost; when the unit failed for any reason other than the token
(seen 2026-09-20: it exited 2 on an unknown flag, DECISIONS I-40) hostd
registered by itself and nothing restarted the renderer.

1. `journalctl -u repose-register -u repose-host-net` for the cause.
2. `systemctl restart repose-host-net.service`; then `ip -br addr show
   br-guests` shows the `.1/22` address and `cat /run/repose/host.env` has
   `HOST_ID` and `GUEST_CIDR`.
3. hostd restarts `repose-host-net` itself after a self-registration since
   I-40, so on a current host this entry means the renderer itself failed.

## Store writes fail with `Read-only file system` on `/nix/store/.links`

`nix copy` to the host, hostd's `Build`, or `nix-store --optimise` fail with
`creating hard link ... /nix/store/.links/...: Read-only file system`, while
`/nix/store` itself is the usual read-only bind. `findmnt /nix/store/.links`
shows the `repose-links-mask` tmpfs: the mask `repose-store-export.service`
puts over `/run/repose/store-export/.links` propagated back to the store
because the bind shared `/`'s peer group (DECISIONS I-61, fixed by making
the export mount private). On a host built before the fix:
`umount /nix/store/.links`, and confirm
`ls -A /run/repose/store-export/.links` is still empty.

## HostWgDown

The edge cannot reach a host's guests: `wg show` on the edge shows no
recent handshake for the host's peer, `dial_fail` in gateway logs for its
guests, alert `host_wg_down`. hostd's gRPC stream goes over the provider
NIC, so the api still sees the host as `ready`.

1. On the host (via the Azure serial console or the bootstrap key if the
   tunnel is the only way in): `systemctl status wg-quick-wg0`,
   `wg show wg0`. `ConditionPathExists=/run/repose/wg0.conf` unmet means
   the host never registered: "HostUnregistered".
2. `journalctl -u repose-host-net`: the render from `host.json` failed
   (malformed `host.json` after a bad rotation) or the endpoint did not
   resolve at boot. `systemctl restart repose-host-net` re-renders and
   restarts wg0, sshd, node_exporter and Fluent Bit.
3. Keys do not match: `repose-admin hosts rotate-wg host-NN` issues a new
   pair, hostd rewrites `host.json` and restarts `repose-host-net`;
   confirm the edge's `wgsync` picked up the new public key within 30 s.
4. Handshake fine but no route: on the edge `ip route | grep <guest cidr>`;
   `wgsync` adds it from `/internal/hosts`.

## Host rebooted

`Hello` after boot reports every guest `stopped`; the api restarts those
that were `running` and notifies their users (workstream 03). Kernel
panics are not auto-rebooted (`panic=` is unset on purpose); a stuck host
is restarted from the Azure portal.

1. `journalctl -b -1 -p err` on the host for why. An OOM in the host
   itself means the `guests.slice` cap was wrong for the RAM: check
   `systemctl show guests.slice -p MemoryMax` against `free -b`.
2. Check that everything came back: `systemctl --failed`, `nft list table
   inet repose`, `ip addr show br-guests`, `wg show wg0`, `lvs vg-guests`,
   `systemctl list-units 'guest@*'`.
3. If the reboot was for a kernel update it should have been a drain
   (`repose-admin hosts drain`, `nixos-rebuild boot`, reboot, undrain);
   an unplanned reboot with tenants on the host is an incident line in
   `docs/incidents/`.

## Browser tools on a machine refuse every connection

A tenant reports that `playwright` and `chrome-devtools` fail with a
connection error on every call, while `repose open --desktop` shows the
browser fine or an empty desktop.

1. The machine's DevTools endpoint may be left bridged to a laptop that is
   gone (`repose browser bridge`, I-296): on the guest,
   `repose-guest-profile browser bridge status` prints `on` and `ss -ltn
   sport = :9226` shows no listener. The desktop idle check switches it
   back within a minute on its own; `repose-browser-bridge off` does it
   now. Both are logged in `journalctl -u repose-desktop-idle-check`.
2. If it prints `off` and 9224 still refuses, `systemctl status
   repose-browser.socket repose-browser-proxy.service repose-browser.service`:
   the socket must be listening; the browser's own failures are in
   `journalctl -u repose-browser.service` (an OOM kill of the whole
   browser rather than a renderer means the slice limit, I-246).

## Guest unresponsive

A tenant reports `repose attach` hangs, or `Freeze` times out.

1. `repose-admin projects show`: state and last signals. `guestd_ok=false`
   means "GuestdLost" above.
2. `repose-admin exec <id> -- uptime` (goes through vsock; if it works,
   the guest is fine and the problem is the gateway or the tenant's
   certificate). From the host itself, `guestd call ping --cid <vsock cid>`
   asks guestd directly, without the api in the path.
3. Console log for kernel panics or OOM. A panic leaves CH running with a
   dead guest: `repose-admin projects restart`.

## Guest not ready

hostd reports `guest did not become ready` (no `Ready` from guestd within
120 s of start), or the api shows a project `starting` for minutes.

1. Console log: `/var/lib/repose/guests/<id>/console.log` on the host.
   The last lines say where boot stopped. A kernel panic or `init=` not
   found means the runner's closure is gone from the host store ("Store
   mount missing" below covers the guest side; on the host, `ls -l
   /nix/var/nix/gcroots/repose/<id>`).
2. `systemctl status guest@<id> virtiofsd@<id>` on the host. If virtiofsd
   is not running, the guest is stuck in the initrd waiting for the
   `ro-store` tag: start it and restart the guest. `guest@<id>` runs as
   the `hostd` user (I-51): `Permission denied` on `/dev/kvm`, the tap or
   `/dev/vg-guests/g-<id>` in `journalctl -u guest@<id>` means the host
   lost `hostd`'s `kvm` membership, the tap's owner, or the udev rule
   that makes `g-*` volumes group `hostd` (`ls -l /dev/mapper/vg--guests-g--*`
   should say `root hostd`); a create that fails at step 5 naming a user
   means the `hostd` or `virtiofsd` account is missing.
3. If boot completed (`multi-user.target` in the console) but no `Ready`:
   guestd crashed. The console carries guestd's own stderr (it logs to the
   console so a frozen root never blocks it); `repose-admin exec <id> --
   systemctl status guestd` works only once it is up, so read the console.
   A crash loop in guestd is a base bug: `hold_base_updates` the project,
   roll the base back (`repose-admin base rollback`), open an issue.
4. sshd refusing the certificate after `Ready` is a principals or CA
   problem, not readiness: `repose-admin exec <id> -- cat
   /etc/ssh/principals/dev /etc/ssh/user_ca.pub` must show the project id
   and the User CA; `SetPrincipals` and `UpdateSecrets` from the api
   rewrite them and reload sshd.

## Store mount missing

A guest logs `mount: /nix/.ro-store: wrong fs type` or `virtiofs: tag
ro-store not found` in its console, commands fail with `No such file or
directory` for store paths, or guestd sends `Warning{store_path_missing}`.

1. On the host: `systemctl status virtiofsd@<id>`; the unit must be running
   on `/var/lib/repose/guests/<id>/virtiofsd/virtiofsd.sock` with
   `--shared-dir /run/repose/store-view` (DECISIONS I-463). If it exited,
   `journalctl -u virtiofsd@<id>` says why (usually the socket directory or
   the `virtiofsd` user's permissions). A guest cannot re-mount the share
   on its own, and a restarted unit starts with an empty view: restart the
   guest with `repose-admin projects restart <id>`, which binds its closure
   again.
   - What the guest's virtiofsd serves:
     `ls /proc/$(systemctl show -P MainPID virtiofsd@<id>)/root | wc -l`
     on the host is the number of paths in its view; it must be at least
     `nix-store -qR $(readlink /nix/var/nix/gcroots/repose/<id>) | wc -l`.
     A create or start that fails at the virtiofsd step with `store view:`
     names why the closure could not be bound.
   - Rolling back to the whole-store export (I-463, for one release):
     run hostd with `--store-export /run/repose/store-export` and restart
     the guests that need it. Every new start then shares the whole store
     as before; write down why.
2. `store_path_missing` with virtiofsd healthy means the host garbage
   collected a path the guest's system uses: the GC root under
   `/nix/var/nix/gcroots/repose/` is gone. `nix build` the guest's
   revision again on the host (deterministic), re-root, restart the guest.
   That is a hostd bug; record it.
3. Paths a user installed in the guest are never affected: they live in
   the guest's overlay upper dir (`/nix/.rw-store`), and
   `repose-pin-profile` copies shared paths of the profile there whenever
   the profile changes (`journalctl -u repose-pin-profile` in the guest
   shows `pinned N store paths`).

## Docker driver wrong

`docker info` in a guest shows a storage driver other than `overlay2`, or
`docker run` fails with `overlay2 not supported`.

1. `mount | grep ' / '` in the guest must show ext4 on `/dev/vda`. Anything
   else means the thin volume was created without `mkfs.ext4` or the
   runner booted the wrong disk: check `lvs vg-guests` and the guest's
   `bin/run --volume` argument in `systemctl cat guest@<id>`.
2. `lsmod | grep overlay` must list the module; the base loads it in the
   initrd. A base whose kernel dropped it is caught by the `guest-docker`
   VM test before publish; if a published base has it, `repose-admin base
   rollback` and hold the affected projects.
3. `/var/lib/docker` full: the guest's volume is at capacity; `repose
   resize` (the user) or `repose-admin projects resize` (operator).

## Desktop not starting

`repose browser` fails at "start the desktop in the guest" or "reach the
desktop through the forward", or the viewer page says the machine's
desktop is off.

1. In the guest: `systemctl status repose-novnc.socket repose-xvnc
   repose-openbox repose-novnc`. The socket must be `listening`; a
   connection to 127.0.0.1:6080 starts the proxy, which requires the whole
   chain. `journalctl -u repose-xvnc` failing at `ExecStartPre` means the
   password step could not write `/run/repose/desktop` (must be 0700 dev;
   tmpfiles recreates it at boot). `journalctl -u repose-novnc` failing at
   `ExecStartPre` means the web root under `/run/repose/desktop/web`
   could not be built (same directory).
2. `Xvnc` failing with `Cannot establish any listening sockets` means a
   stale `/tmp/.X11-unix/X99` lock from a killed server: remove
   `/tmp/.X99-lock` and `/tmp/.X11-unix/X99`, then reconnect. `Xvnc`
   failing to read `vnc-passwd`: remove both password files in
   `/run/repose/desktop` and start again; the next start writes a new
   pair (the user's link then needs `repose browser` again).
3. The chain stopped by itself: that is the 30-minute idle stop
   (`journalctl -u repose-desktop-idle`); the page reconnects by itself
   and the link keeps working, since the password is the boot's.
4. The page connects but the screen stays 1440x900 or the browser window
   does not fill it: `xrandr -display :99` in the guest shows the screen
   Xvnc has; `xdotool getwindowgeometry` on the Chromium window shows
   whether openbox re-maximised it. A window that did not follow is an
   openbox restart away (`systemctl restart repose-openbox`).
5. The laptop side: `repose browser` reuses a recorded forward only while
   its port answers `/healthz`; `~/.config/repose/browser-forwards/` holds
   the record, and `repose browser --stop` clears it.
6. A headed browser shows nothing on the desktop: the shell that launched
   it had no `DISPLAY` because it started before Xvnc. New shells export
   `DISPLAY=:99` while the X socket exists; open a new tmux window.

## No session on a machine (tmux or herdr)

`repose attach` finds no tmux session, or a herdr project's machine has
no herdr server, after a start.

1. In the guest, as dev: `jq .multiplexer ~/.repose/project.json` names
   the multiplexer this boot chose (no key is tmux). `systemctl --user
   status repose-tmux-session repose-herdr-server` shows which unit ran.
   Nothing starts either unit but guestd's SetupProject (DECISIONS
   I-503).
2. `Skipped due to 'exec-condition'` on a unit means
   `repose-multiplexer-is` refused it: the file names the other
   multiplexer, or the other unit was already active. A change of
   multiplexer applies at the next start (I-502), so a machine that
   switched while running keeps the old unit until `repose stop` and
   `repose start`. `repose-multiplexer-is tmux; echo $?` (or `herdr`)
   gives the answer the unit got.
3. tmux, on a machine that had a session: the tmux server exited (`exit`
   in the last window, `tmux kill-server`, an OOM kill). The unit starts
   it again after 5 s (I-551); `systemctl --user show -p
   NRestarts,ActiveState repose-tmux-session` shows the count, and
   `activating` during the wait. A unit that stays inactive was stopped
   by hand. A failed unit whose journal says the session "is on a tmux
   server started outside this unit" found the slug's session on a
   server someone started over ssh (I-560): that session works, but
   nothing restarts it; `tmux kill-server` and then `systemctl --user
   start repose-tmux-session` put it back in the unit.
4. herdr: `journalctl --user -u repose-herdr-server` and
   `~/.config/herdr/herdr-server.log`. A server that started but has no
   workspace in the checkout: run `repose-herdr-workspace` by hand. It
   exits 0 without a word when the herdr unit is not active, and names
   any other reason on stderr. A user's `herdr update` puts a newer
   binary in `~/.local/bin`, which the unit runs from the server's next
   start (`herdr update --handoff` moves the running server to it and
   keeps the panes); removing it goes back to the base's release. `repose-herdr-watch: no herdr server left` in the journal
   means the server ended (a crash, `herdr server stop`) and the unit
   restarted it; `start-limit-hit` means it failed five times in a
   minute, and `systemctl --user start repose-herdr-server` tries again
   (I-560).

## Prisma, Playwright or a Python wheel fails in a guest

The base's compat layer (DECISIONS I-228, `nix/guest/base/compat.nix`).

1. Prisma: `Failed to fetch ... linux-nixos ... 404`, or `connect
   ECONNREFUSED 127.0.0.1:850`. In the guest: `echo
   $PRISMA_ENGINES_MIRROR` must be `http://127.0.0.1:850` (a project
   `.env` or shell that sets `PRISMA_ENGINES_MIRROR` or
   `PRISMA_BINARIES_MIRROR` wins over it); `systemctl status
   repose-prisma-engines.socket` must be `listening`; `curl -sI
   http://127.0.0.1:850/all_commits/x/linux-nixos/schema-engine.gz` must
   answer 302 to `.../debian-openssl-3.0.x/...`. The line "Precompiled
   engine files are not available for nixos" is printed on every command
   and is expected.
2. Playwright: `Executable doesn't exist` means the project's version
   wants a browser revision nobody installed: `npx playwright install
   chromium` (downloads into `~/.cache/ms-playwright`). If that directory
   lost the base's links, `sudo systemctl restart repose-playwright-seed`.
3. Python: `libstdc++.so.6: cannot open shared object file` from a venv
   whose `bin/python` links straight into `/nix/store` (made with a
   python from `nix shell` or an older base): recreate the venv with the
   system `python3` or `uv venv`.

## Reservation drift

`hosts list` free memory does not match the sum of running guests.

`repose-admin hosts reconcile host-NN` asks hostd for its `Hello` state
and recomputes reservations. Happens after an api crash mid-command; the
reconcile is safe to run any time.

## api cannot resolve repose-postgres

`api` or `api-grpc` logs a DNS failure for the `DATABASE_URL` host at
start. The Postgres compose file joins the shared `coolify` network itself,
which is what registers `repose-postgres` there; "Connect to predefined
network" would register only the container name (`coolify.md`, fact 11).

1. On the VM: `docker inspect $(docker ps -qf name=repose-postgres)
   --format '{{json (index .NetworkSettings.Networks "coolify").Aliases}}'`
   must list `repose-postgres`. Only the container name there means the
   deployed compose lacks the `networks: coolify:` block: paste the current
   `ops/coolify/postgres/docker-compose.yml` again and redeploy the
   Service by hand. (The Service is pasted, not watched, so a push to
   `main` does not update it the way it updates the applications —
   `coolify.md` fact 16.) No `coolify` key at all means the file was pasted without its
   top-level `networks:` section.
2. From the api container's network: `docker run --rm --network coolify
   busybox:1.36 nc -z -w 3 repose-postgres 5432` exits 0 once 1 is right.

## api-grpc stopped and did not come back

Symptom: `repose-admin ops list --state running` grows, hostd's journal
repeats `stream_disconnect` with `retry_ms` climbing to 30000, `docker ps`
on the control VM shows the api-grpc container `Exited (137)` (or absent
from `docker ps`), and the api itself is healthy.

Cause: the container was stopped by an operator action, `docker kill`
or `docker stop`, and Docker's `restart: unless-stopped` treats that as a
stop, never restarting it (m3 integration, 2026-09-21: three minutes of
no ops). A crash of the process (SIGKILL to its pid, an OOM, a panic) is
restarted within seconds; a stopped container is not.

Fix: `docker start <api-grpc container>` on the control VM (same
container, same image), or a redeploy from Coolify. hostd reconnects
within its 30 s backoff, sends `Hello` with its guest list, and the api
re-sends every unfinished command with its `command_id`; guests are
untouched throughout.

## guestd_lost after a base switch

Symptom: hostd logs `guestd_lost` for a guest seconds after an
`ApplyConfig` that ended `guest_unresponsive` with `guestd Switch:
vsockrpc: EOF`; the guest's unit is `active`, SSH through the gateway
works, `systemctl is-active guestd` inside says `inactive`,
`/run/current-system` still names the previous system, and the revision
row is `built`. Hooks, `repose-admin exec`, snapshots and applies fail for
that guest until guestd runs again.

Cause: the new system carried another guestd binary and its activation
stopped guestd, whose child the activation was, so the switch died before
its start step (base 2026.09.21.3 on host-01, 2026-09-21 02:59Z: three
guests). Bases from I-143 on do not restart guestd from the activation;
guestd restarts itself 3 s after answering, and hostd then logs
nothing at all when guestd is back within a minute, or `guestd_lost`
followed by `guestd_regained` when it is not; both are the healthy shape
of a switch onto a new guestd (2026.09.21.5 on m3-held, 05:00Z: op
`done` at 05:00:22, "guestd restart scheduled", guestd active again at
05:00:27 on the new binary, no `guestd_lost`).

Recovery, in this order:

1. Inside the guest, as the owner over SSH (`ssh <slug>.repose 'sudo
   systemctl start guestd'`): guestd comes back on the old binary, hostd
   logs `guestd_regained` within a minute (m3-held, 03:02Z), and the
   project keeps working on its previous system. `repose-admin exec`
   cannot do this: it goes through guestd.
2. Do not stop/start the project while its pending revision is a base
   from before I-143: the start applies it and strands guestd again
   (m3-iso-c, 03:05Z). Wait for a base with I-143 to be published; the
   sweep builds a new revision on it, and the next start or the sweep's
   own switch applies that one cleanly.
3. A guest with no SSH path (a second tenant's) stays as in 2 until that
   base: `repose-admin projects restart` after it (m3-iso-c, 04:36Z:
   stop 60 s, start 17 s, the built revision applied at boot, running).
4. From I-147 on, `repose-admin projects restart` is the operator
   recovery for any stranded guest whose newest built revision is newer
   than its current system: the start applies that revision and nothing
   older. Before I-147 a start applied the newest `built` row even when
   an applied newer one existed (m3-held, 04:38Z: the 2026.09.21.3 row
   over the applied .4, that older base's activation stopped guestd
   again). A guest whose newest revision is already applied starts with
   no apply at all.
5. From I-157 on, the owner's own `repose start` does the same: on a
   project in `error`, or running with `guestd_ok = false` in its newest
   sample, the start op is stop (no snapshot), apply the pending revision
   to the stopped guest, boot. The op's params carry `restart: true`;
   a start whose ApplyConfig lost guestd carries `recovered:
   apply_config:guest_unresponsive` and did the same. A destroy of such a
   guest finishes (I-156): look for `op_recover` in the api log with the
   op id, then the stop, the snapshot of the stopped volume and
   DestroyGuest.

## Coolify deploy failed

The api, api-grpc or web app's rolling deploy did not go green.

1. Coolify's deployment log. A health check timeout with the container
   alive is usually a migration running long at `api` start (the api
   applies pending migrations itself, I-90): wait, the old container is
   still serving.
2. A migration failure leaves the new container crash-looping and the old
   one serving. Fix forward — a corrected commit on `main` redeploys
   itself (fact 16) — or, if the fix will take a while,
   `repose-admin db rollback --to <previous>` from the operator machine
   (it connects to Postgres over the control VM's WireGuard address) and
   roll the app back to the previous image from Coolify's deployment
   history. A rollback is the one deploy nobody gets automatically.
3. If the deploy fell back to stop-then-start (Coolify does this silently
   when it has no health signal), check that Coolify's own health check is
   off on `api` and `api-grpc` and the image's `HEALTHCHECK` is intact
   (I-87); on `web` that the path is `/healthz` on 3000.
4. `api-grpc` unhealthy with `api` healthy: its log. "GRPC_SERVER_NAMES is
   required" means the variable was cleared; the certificate is issued
   from the CA for those names at start, and `repose-admin ca init` must
   have run once (a fresh database has no CA yet).
5. A user reporting "it hung for five seconds while you deployed", or a
   single 502 at the same moment, is not a failed deploy. Every
   switchover loses one or two requests per client, at a delay that is
   the same every time for a given app (`web` +11 s, `api` +26 s, which
   is each image's health-check start period) — measured seven times out
   of seven (`docs/ops/coolify.md` fact 13). `ops/deploy-probe.sh` against
   the domain during a deploy is how to tell that from a real outage: a
   failure or two and then 200s is the known drain gap, a run of them is
   not.
6. Evidence after a rollout: Coolify removes the replaced container, and
   `docker logs` of the old api or api-grpc goes with it (the notify_send
   lines of a delivery made minutes before a deploy were gone on
   2026-09-21). Loki has them (Coolify's log drain ships the api and web
   containers' stdout, `docs/ops/OBSERVABILITY.md`); on the VM itself only
   the live container's log is readable, so read it before pushing to
   `main`, or query Loki by container name.

## Coolify cannot validate the control VM

"Validate & configure" on the server hangs or fails. Nothing of Coolify runs
on the VM (I-83); this is Coolify on the owner's server failing to SSH in.

1. A hang is the network. Over Tailscale (I-86): `tailscale status` on the
   VM, and the server's address in Coolify must be the tailnet IP. Over the
   public IP: the instance's address must be in `coolify_manager_cidrs`
   (`prod.local.tfvars`), applied. `ssh root@<control ip>` from an operator
   address still works while either is wrong.
2. "Permission denied" is the key: the private key picked in Coolify must be
   the one whose public half is `coolify_public_key` in `prod.tfvars`.
   `grep -c ssh-ed25519 /root/.ssh/authorized_keys` on the VM should count
   the operator keys plus one.
3. A Docker complaint means cloud-init did not finish:
   `/var/log/cloud-init-output.log` on the VM. A fresh apply would have
   failed at `terraform_data.ready` rather than returning, so this is a
   machine that was ready and stopped being one.

There is no `http://<control ip>:8000`; the control subnet NSG does not open
it and `infra/azure/modules/network/main.tf` has a postcondition that fails
the plan if somebody adds it. The dashboard is the owner's instance.

## ssh.repose.herakraft.co resolves to Cloudflare

`dig +short ssh.repose.herakraft.co` returns `104.21.x.x` or `172.67.x.x`
instead of the edge's address, and SSH hangs or is refused. `herakraft.co`
answers every name under it from a **proxied wildcard record**, so a missing
`ssh.repose` record does not fail, it resolves to Cloudflare's proxy, which
carries neither SSH nor WireGuard. Every host configured with that hostname as
its WireGuard endpoint fails the same way.

1. `make -C infra plan ENV=prod` says so too, as the `dns_is_managed_or_manual`
   check warning, whenever `manage_dns` is false.
2. Fix: create `ssh.repose` as an **unproxied** A record pointing at the
   `edge_public_ip` output, or set `manage_dns = true` with a
   `CLOUDFLARE_API_TOKEN` and let OpenTofu own it. `infra/README.md`,
   "DNS while manage_dns is false", has the full record table.
3. Until then the edge is reachable at its literal address, which is what
   every `ssh_jump` output already prints.

## Host loss

A host is gone (Azure repair, disk lost, or a deliberate retirement without
drain).

1. `repose-admin hosts mark-lost host-NN`: every project on it goes to
   `error` with reason `host_lost`, tenants get an email. Its certificate
   stops working at once (I-432); if the machine comes back, it joins
   again with `hosts add --name host-NN --reissue`.
2. For each project: `repose-admin projects restore <id> --latest --to
   <other host>`. Data since the last snapshot (up to 24 hours, or since
   the last `stop`) is lost; the email says so. Tenants' git remotes hold
   whatever their agents pushed.
3. `repose-admin hosts retire host-NN`; `tofu apply` with it removed.

## Suspected cross-tenant access

1. `repose-admin hosts drain --all` (no new placements anywhere).
2. Snapshot the guests involved (`projects snapshot`); do not stop them
   yet, memory state may matter.
3. Capture: `journalctl -u hostd --since <window>` on the host, gateway
   logs from Loki for the window, `audit_log` for the window, nftables
   counters (`nft list ruleset`), `tcpdump` on the involved taps for the
   next hour.
4. Confirm or refute with the `test/isolation` suite against that host.
5. If confirmed: stop the offending guest, suspend the user, notify the
   affected tenant within 72 hours with what was reachable, write
   `docs/incidents/<date>.md`, fix, re-run isolation tests fleet-wide
   before undraining.

Who is told, what is captured, how a tenant hears (workstream 14 §5):

- **Told, in this order:** the owner (ntfy, then the incident file); the
  affected tenants; a third party only if their credentials inside the
  guest could have been read (GitHub, OpenAI, Anthropic) so they can
  rotate on their side. Nobody else until the timeline is written.
- **Captured before anything is stopped**, into
  `/var/lib/repose/incident-<date>/` on the host (root, 0700), then copied
  off with `scp` through the edge:
  ```
  journalctl -u hostd --since '<window start>' -o json > hostd.json
  journalctl -u sshd --since '<window start>' -o json > sshd.json
  journalctl -t hostd-audit --since '<window start>' -o json > audit-login.json
  nft list ruleset > nft.txt; bridge fdb show br br-guests > fdb.txt
  bridge -d link show > taps.txt; ip -s link > ifstats.txt
  hostd guests > guests.txt; hostd state export > state.json
  timeout 3600 tcpdump -nn -e -i tap-<8hex> -w tap-<8hex>.pcap &
  ```
  On the edge: gateway journal for the window; Loki
  `{component="gateway"}` and `{component="hostd", host="<id>"}` exports;
  `audit_log` rows for the window (`repose-admin audit --since`). Never
  capture guest disks or terminal contents beyond what the boundary test
  needs: an incident does not suspend the privacy policy.
- **Confirm or refute** with the suite, from the operator machine:
  ```
  REPOSE_ISOLATION_HOST_ID=<id> REPOSE_ISOLATION_EXEC_A='ssh -J root@<edge>,root@<host> dev@<A ip>' \
  REPOSE_ISOLATION_EXEC_B='ssh -J root@<edge>,root@<host> dev@<B ip>' \
  REPOSE_ISOLATION_HOST_EXEC='ssh -J root@<edge> root@<host>' \
  REPOSE_ISOLATION_A_IP=<A ip> REPOSE_ISOLATION_B_IP=<B ip> REPOSE_ISOLATION_A_MAC=<A mac> \
  REPOSE_ISOLATION_B_TAP=tap-<8hex> REPOSE_ISOLATION_HOST_IP=<.1> REPOSE_ISOLATION_OTHER_GUEST_IP=<other /22> \
  REPOSE_ISOLATION_A_GUEST_ID=<A guest id> REPOSE_ISOLATION_A_SLUG=<A slug> \
    go test ./test/isolation/ -run . -v -count=1 2>&1 | tee isolation-<date>.txt
  ```
  The output (host id and date on every test) goes into the incident
  file verbatim.
- **Tenant notice**, within 72 hours of confirmation, by email from the
  owner's address (the notification pipeline is for agent events, not
  incidents), one message per affected user, plain text: what boundary
  failed, the window, what was reachable in their environment (network
  ports, files, secrets by name only), what we did, what they should
  rotate, and a contact. Keep a copy in the incident file. A user whose
  data was *not* reached is not written to; say so in the file.
- **Incident file** `docs/incidents/YYYY-MM-DD.md`: timeline (UTC),
  boundary and mechanism, hosts and guests involved by id, what was
  captured and where it is, tenants notified and when, the fix, the
  re-run's `isolation-<date>.txt`, and the decision entry if a contract
  changed.

## OpenTofu state lock stuck

`make plan` or `make apply` reports `Error acquiring the state lock` with an
ID. An apply was killed and its blob lease survives it.

1. Confirm nobody is running an apply: ask, and check the CI run list.
2. `make -C infra force-unlock ENV=prod LOCK_ID=<id from the message>`.
3. If the state itself is wrong rather than locked, the container has blob
   versioning: `az storage blob list --account-name reposetfstate3912
   --container-name tfstate --include v` lists the versions and
   `az storage blob copy start` from one restores it.

A lease expires on its own after fifteen minutes; force-unlock is for when
waiting is not acceptable.

## An apply wants to replace a host data disk or a static IP

The plan shows `# forces replacement` on `azurerm_managed_disk.data`,
`azurerm_virtual_machine_data_disk_attachment.data`, or any
`azurerm_public_ip`. It cannot: those carry `prevent_destroy` and the plan
fails instead.

That is the intended outcome. A replaced data disk loses every tenant volume
on that host; a replaced static IP breaks every host's WireGuard endpoint and
the DNS that users type. Find what changed (usually `zone`, `disk_size_gb`
shrinking, or `storage_account_type`) and change it back. If the replacement
really is wanted, drain the host first
(`infra/README.md`, "Draining and destroying a host").

## Control plane cannot reach the edge network

The gateway logs `route_fail` against `https://10.255.255.1:8444`, `wgsync`
cannot fetch `/internal/hosts`, or Prometheus cannot scrape.

1. `ssh root@<control ip> wg show`. No `wg0`: the peer was never written
   (cloud-init only writes it when the VM is created with
   `edge_wireguard_public_key` set; `custom_data` is ignored afterwards).
   Write `/etc/wireguard/wg0.conf` by hand, `infra/README.md` "Wiring the
   control plane to the edge".
2. `wg0` up but no handshake: the edge side. `ssh -p 2222 root@<edge ip>
   wg show` must list the VM's public key (`cat /etc/wireguard/publickey`
   on the VM) as a peer; it comes from `repose.edge.staticPeers` in
   `nix/edge/edge-01.nix`, and `wgsync` never removes it. Missing: add it
   and rebuild the edge. Present but removed every 30 s: `wgsync` is
   running without `WG_STATIC_PEERS` (an edge older than I-92).
3. Handshake fine, `curl` from the edge to `https://10.255.255.1:8444`
   refused: the `api-grpc` app has no port mappings (`ops/coolify/README.md`).

The control plane's WireGuard private key is generated on the machine and
never leaves it, which is why this is two moves rather than one apply.

## Retiring a host's bootstrap key

`repose.host.bootstrap.enable` puts the operator's plain key on the host
so the installer and the join-token delivery can reach it (I-92). With
`repose.host.bootstrap.keyUntilHostCA = true` (DECISIONS I-177) the key
is accepted only while `/run/repose/host_ca.pub` is empty, so a host
that knows the Host CA takes certificates only (14 §9) and a host that
lost its `host.json` takes the key again. Turning it on ends plain-key
logins the moment the CA is present, so, in order, per host:

1. The host has the Host CA: `wc -c /run/repose/host_ca.pub` is non-zero
   (else "Operator certificate refused by a host" below).
2. From the operator machine: `ops/dev/operator-cert.sh` (an 8 h
   certificate next to `~/.ssh/id_ed25519`, signed inside the api
   container; the api must be at I-177 or newer for `--pubkey -`), then
   `ssh -o HostKeyAlias=10.200.1.4 -J root@20.102.98.254:2222
   root@10.255.0.2 true` and, on the host, `journalctl -t sshd-session -g
   'ID operator:'` (or `-t sshd`) shows the certificate login.
3. Set `bootstrap.keyUntilHostCA = true` in `nix/hosts/host-NN.nix` and
   switch the host. `cat /run/repose/bootstrap_authorized_keys` is empty
   and the log line `Host CA present; bootstrap key not accepted` is in
   `journalctl -u repose-host-net`.
4. Every later session runs `ops/dev/operator-cert.sh` first (the
   certificate lasts 8 h). Break-glass if the CA is lost: the host takes
   the bootstrap key again as soon as `host_ca.pub` is empty, and a
   `host.json` without `host_ca_pub` empties it.

## Operator certificate refused by a host

Symptom: `ssh root@10.255.0.x` with a certificate from `repose-admin
operator-cert` is refused (`Permission denied (publickey)`) while the
bootstrap key still works; on the host `wc -c /run/repose/host_ca.pub`
is 0 and `sshd -T | grep trustedusercakeys` names that file.

Cause: the host registered before the api sent the Host CA
(`RegisterResponse.host_ca_pub`, DECISIONS I-139). It learns it at its
first `Rotate` (30 days after registration), when hostd rewrites
`host.json` and restarts `repose-host-net`.

Fix now, on the host, from the api's public line
(`repose-admin ca show`, the `# host ca:` line; also the `@cert-authority`
line the CLI writes to `~/.ssh/repose/known_hosts`):

```
jq --arg ca 'ssh-ed25519 AAAA... repose-host-ca' '. + {host_ca_pub: $ca}' \
  /var/lib/repose/hostd/host.json > /var/lib/repose/hostd/host.json.new
mv /var/lib/repose/hostd/host.json.new /var/lib/repose/hostd/host.json
chmod 0600 /var/lib/repose/hostd/host.json
systemctl restart repose-host-net
wc -c /run/repose/host_ca.pub          # non-zero
```

sshd reads `TrustedUserCAKeys` at each authentication, so no sshd
restart. Then log in with the certificate and read the journal line:
`Accepted publickey for root ... ID operator:<name> (serial N)`.

## hostd: join token already used

`systemctl status hostd` shows `status=3` and the unit is not retrying
(`RestartPreventExitStatus=3`); the journal says `register: join token
already used`.

1. The token was consumed by an earlier registration attempt that did not
   finish writing `/var/lib/repose/hostd/{cert,key}.pem`, or the host was
   re-imaged with the same cloud-init payload.
2. Mint a new token: `repose-admin hosts add --name <host> --reissue`, write it to
   `/run/repose/join-token`, `systemctl restart hostd`.

## hostd: api unreachable

`repose_host_stream_connected` is 0 and the journal loops on
`stream_disconnect`. Guests keep running; nightly snapshots still run
locally from `repose-snapshot.timer`.

1. `hostd status` (control socket) shows `stream_connected: false`.
2. From the host: `curl -sv https://api.repose.herakraft.co:443`. A TLS error naming the client certificate means the
   host certificate expired without rotation: `journalctl -u hostd | grep
   rotate`; rotation needs the stream, so if the certificate is past
   expiry re-register with a new token (previous entry) after moving the
   old `cert.pem` aside.
3. Results, events and up to 60 minutes of samples are buffered and sent
   on reconnect; longer outages lose samples (`repose_host_samples_dropped_total`).

## hostd: thin pool over 90 percent

`host_warning{pool_high}` at 80 percent, and CreateGuest and Restore return
`insufficient_capacity: thin pool 9x% full` from 90. See "PoolFull" above
for extending the pool; existing guests keep running throughout.

## hostd: store over 80 percent

`host_warning{store_high}` and Build refuses with `insufficient_capacity:
host store full`. See "StoreFull" above.

## Caches: npm installs or docker pulls slow or failing in guests

The host's caches (`host-conventions.md` "Caches", I-202). What guests see
never depends on them working: the npm front falls back to the registry,
dockerd falls back to Docker Hub.

1. On the host: `systemctl status nginx repose-npm-cache docker-registry
   repose-cache-volume`. `journalctl --no-pager | grep repose_npm_fallback
   | tail` shows whether the front is serving around the cache.
2. The cache down: `systemctl restart repose-npm-cache`. Its volume
   missing (`mountpoint /var/cache/repose` fails): `systemctl restart
   repose-cache-volume` and read its log; the caches do not start without
   it.
3. The volume full (`df -h /var/cache/repose`): the npm cache evicts at
   40 GB; the Docker mirror expires blobs after 168 h, so a full volume is
   the mirror's. `systemctl stop docker-registry && rm -rf
   /var/cache/repose/docker/* && systemctl start docker-registry` empties
   it; guests pull from Docker Hub meanwhile.
4. `docker-registry` restarting every 10 s: it cannot reach Docker Hub at
   start (`journalctl -u docker-registry` shows the panic). Guests pull
   directly until it can.

## hostd: guest never sends Ready

Create or Start fails with `guest_unresponsive: create: step 10 (ready)
failed: guest did not become ready` after 60 s; the guest is in `error`
and its tap, tc, nft membership and units are gone; the volume stays.

1. `/var/lib/repose/guests/<id>/console.log` holds the boot output. No
   output at all: the kernel or initrd path in `ch.args` is wrong (the
   closure is not a bootable system) or KVM is missing.
2. A boot that stops at mounting `/nix/.ro-store`: virtiofsd died; `journalctl
   -u virtiofsd@<id>`.
3. A boot that reaches login but never Ready: guestd is not running in the
   guest; the base image is at fault (workstream 02).
4. Fix, then `repose-admin projects start <id>`.

Since I-62 a virtiofsd that exits before creating its socket fails the
create at step 8 instead; on an older hostd, `systemctl status
virtiofsd@<guest id>` and `journalctl -u guest@<guest id>` (Cloud
Hypervisor "Failed connecting the backend ... virtiofsd.sock") are the
first things to read when this message appears at once after a create.

## hostd: virtiofsd exited under a running guest

The guest is in `error` with reason `virtiofsd exited`; hostd stopped the
hypervisor cleanly because the guest would see I/O errors on every store
path. `journalctl -u virtiofsd@<id>` for the cause (usually OOM against
its 1 GB MemoryMax, or a chroot problem after a store bind-mount change).
`projects start` brings it back; the api restarts once on its own.

## hostd: hypervisor exited unexpectedly

Reason `hypervisor exited <code>` on the guest; tap and tc are torn down.
`journalctl -u guest@<id>` and the tail of `console.log`. A unit that
ends `Failed with result 'oom-kill'` (hostd may still report the exit as
0 or 137) is the unit's `MemoryMax` (class RAM plus 512 MB) killing CH.
The guest's RAM is shmem and fills most of that; read `journalctl -k`
for the `Memory cgroup stats for /guests.slice/guest@<id>` block: `shmem`
near the class size and `file_writeback` or `file` far above `shmem` is
host page cache from the disk, which I-230 (`direct=on`) removed; check
`cat /var/lib/repose/guests/<id>/ch.args` has `direct=on` (a guest last
started by an older hostd does not until it is stopped and started). With
`direct=on` and still killed, `kernel`/`anon` in that block is the
hypervisor itself: a CH bug or leak, not the tenant. The api restarts once
automatically, then leaves it in error with an event.

## hostd: freeze without thaw

`Warning{freeze_timeout}` from the guest and a failed snapshot: hostd lost
the vsock connection or crashed between `Freeze` and `Thaw`; guestd thawed
itself after 10 s so the tenant saw at most a 10 s pause. The snapshot
is marked failed and retried by the api's timer; nothing else to do
unless it repeats, in which case check `repose_host_snapshot_freeze_seconds`
for a slow `lvcreate -s` (pool metadata nearly full).

## hostd: snapshot upload failed

Result `internal: snapshot upload failed: <err>`; the LVM snapshot was
removed regardless. A 403 means the managed identity lost its role on the
`repose-snapshots` container (`az role assignment list`); a timeout means
egress from the host is broken (NAT gateway). The api retries once from
its timer; `SnapshotStale` fires if a running project stays without one.

## hostd: snapshot checksum mismatch

Restore result `internal: snapshot checksum mismatch` (DECISIONS I-462):
the blob in the store is not the one hostd uploaded, by the SHA-256 the
api recorded in `snapshots.sha256`. Nothing was written: the check runs
before any volume or guest state exists ("changed during the restore"
means the second check failed and the new volume was removed). Treat it
as a possible tampering incident, not a retry: do not restore that
snapshot, keep the blob, read the storage account's access logs for
writes to its path after `taken_at`, and record what you find in
`docs/security/`. The project restores from an older snapshot whose
digest matches. A restore that fails this way leaves the project in
`error` with `start` refused (`restore_unfinished`, I-461) until a
restore succeeds.

## api: project in error after a failed restore

`start` answers `409 conflict` `restore_unfinished` (DECISIONS I-461):
the project's newest restore failed in its build or restore phase, after
the old guest was destroyed; the old volume is gone and the new one was
removed. A restore that failed in destroy_guest is not refused: the old
guest is intact. The user, or an operator with
`repose-admin projects restore`, runs the restore again; the op's `error` says why
the first one failed. hostd removing the failed restore's volume and the
api clearing the old guest's address are both automatic.

## hostd: build exceeds time

`build_timeout: build timed out after 30 minutes while building <name>`
and the CLI exits 10. The named derivation is what was compiling when
`timeout` fired (the scope's `RuntimeMaxSec` is the backstop 30 s later);
the tenant either pulls a cached variant or accepts the cap. "Build
stuck" and `BuildQueueStuck` above cover a build that ignored the cap.

## Build stuck (no BuildLog line for 10 minutes)

A `Build` op is `running`, its SSE log has not moved, and hostd's
`builds_running` gauge holds. Distinguish a slow build from a wedged one:

1. On the host: `systemctl list-units 'repose-build-*'` shows the scope
   (`repose-build-<revision>` for the build, `-eval` for the evaluation)
   with its `RuntimeMaxSec`; `systemctl status <scope>` shows the `nix`
   process tree under user `nixbuild`. A build is alive when `nix log
   --follow` on its derivation (`journalctl -u hostd | grep build_start`
   names the revision; `nix log <drv>`) still grows.
2. A build compiling something big (a browser, CUDA) is slow, not stuck;
   the 30-minute cap ends it as `build_timeout` naming the derivation and
   the tenant reads it in the CLI. Nothing to do.
3. If the scope is past `RuntimeMaxSec` and still present, systemd's kill
   failed: `systemctl kill --signal=KILL <scope>`; hostd reports
   `build_timeout`. If the `nix` client is gone but `nix-daemon` still runs
   the builder (`ps -o pid,user,etime,args -C nix-daemon`), the daemon lost
   the client's cancellation: `nix build --no-link <drv>` from a root shell
   attaches to the running build so you can watch it, or `systemctl restart
   nix-daemon` kills every build on the host (both hostd builds restart
   from their last substituted path; nothing tenant-visible is lost).
4. `BuildQueueStuck` above covers the metric-level alert.

## Build: cache unreachable

`host_warning{kind: cache_unreachable}` and BuildLog lines `warning:
unable to download 'https://<cache>/...'`. Builds go on from
cache.nixos.org or source; only speed is lost.

1. `curl -sI https://repose.cachix.org/nix-cache-info` from the host. A
   DNS or TLS failure from the host and not from your laptop is the host's
   egress (NAT gateway, `HostWgDown` is unrelated). A 5xx is Cachix; check
   status.cachix.org and wait.
2. If the cache is fine but every host warns, the public key changed:
   `repose.host.overlayCache.publicKey` must equal the key on the cache's
   page, else Nix refuses its narinfos with `signature ... invalid` and
   falls back to building the agents from their release tarballs
   (downloads, not compiles; minutes).
3. `nix store info --store https://repose.cachix.org` proves the host
   reaches it after the fix. The warning is per build and stops by itself.

## Build: base unavailable

`Build` fails `internal: base <rev> unavailable: ...` for every project;
no tenant config changes.

1. `ls /var/lib/repose/base/` on the host. Each directory is a git
   checkout of this repository at a `base_versions.nix_rev`.
2. `clone failed`: hostd's `--base-repo-url` is empty or the repository
   refused it. A private repository needs `repose.host.baseRepo.sshKeyFile`
   pointing at a deploy key delivered like the join token (never in the
   store); `journalctl -u hostd | grep base` has git's stderr. Place the
   checkout by hand from a machine that can: `git clone --no-checkout
   <url> /var/lib/repose/base/<rev> && git -C /var/lib/repose/base/<rev>
   checkout <rev>`, then resend the build (`repose-admin projects
   rebuild <id>`, or `repose config apply` as the user).
3. `checkout failed`: the revision is not in the repository (a
   `base publish` of a rev that was never pushed). Publish a rev that
   exists.
4. `flake.lock` errors: the checkout is not a complete repository
   (interrupted clone). Remove the directory and let hostd clone again.
5. `repository path '/var/lib/repose/base/<rev>' is not owned by current
   user (libgit2 error code = 7)` in the build log: the checkout belongs
   to root and the evaluation runs as `nixbuild`. hostd hands every
   checkout to the build user before evaluating (`chown -R nixbuild:`,
   DECISIONS I-93); seeing this means a hostd older than that, and the
   fix by hand is the same chown.

## Base bump failures

`repose-admin base status <version>` lists projects `failed` with the
error's first line; each has a `base_update_failed` event the user saw.

1. One or two projects failing with `eval_failed` or `build_failed` is a
   fragment that stopped building on the new base (a renamed attribute, a
   removed package). The project keeps its old base and is skipped by
   later bumps until the user's next successful `repose config apply`;
   nothing to do on the platform side, but the error text tells you which
   nixpkgs change caused it.
2. Many projects failing the same way is the base's bug: `repose-admin
   base rollback <previous version>` re-applies the previous closure to
   every project the bump reached (still rooted, so no rebuild), then fix
   the base and publish again.
3. `needs_reboot` is not a failure: the kernel changed, the guest was
   built and the user decides when to reboot (`repose config apply
   --reboot`); `repose status` says `base X (Y ready; reboot when
   convenient)`.
4. A bump that never started: the daily job runs at 04:00 UTC (05); a
   `--security` publish runs at once. `repose-admin ops list --kind build`
   shows the queued builds and their `not_before` times, spread over 24 h
   (2 h security), two per host at a time.

## Closure collected under a running guest

`Warning{kind: store_path_missing}` from a guest, or a tenant's shell
saying `No such file or directory` for a store path. The host garbage
collected a path the guest's running system references, which means its
GC root was missing (a hostd bug) or was removed by hand.

1. `ls -l /nix/var/nix/gcroots/repose/` on the host: the guest's root is
   `<guest_id>` and the newest three revisions are
   `rev-<project_id>-<revision_id>`; `nix-store --gc --print-dead | grep
   <closure>` is empty when the closure is protected.
2. Rebuild the revision (deterministic): `repose-admin projects rebuild
   <id>` sends `Build` with the same fragment and base; hostd re-roots the
   result. A guest that only lost a leaf package keeps running from page
   cache and picks the path up at its next `switch`; one that lost its
   `init`, kernel or a service binary is wedged: `repose-admin projects
   restart <id>` (stop with a snapshot, start) boots it from the rebuilt
   closure.
3. Find why the root was gone: `journalctl -u hostd | grep gcroot`
   (hostd logs every root it sets and removes) and the weekly
   `nix-gc.service` run time. A root removed by hand is an audit finding.

## hostd: command for unknown guest

`not_found: guest <id> not on this host`. The api's placement and the host
disagree, usually after a restore onto another host that was not recorded.
`repose-admin hosts reconcile <host>` reads the host's Hello and fixes the
api's view.

## hostd: state.db corrupt or lost

hostd refuses to start and logs the path. Move the file aside and run
`hostd reconcile --rebuild` (alias `--from-api`): it rebuilds the guest
table from `/var/lib/repose/guests/*/guest.json`, `lvs` and
`systemctl list-units 'guest@*'`, then the next Hello lets the api
reconcile its own view. `hostd guests` must match `lvs vg-guests` and the
unit list afterwards. Command results are lost, so the api may re-send
commands; every command but Exec re-executes safely.

## hostd: two hostd processes

The second prints `hostd already running` and exits 1: bbolt holds a
file lock on `state.db`. Nothing to do; `systemctl status hostd` shows
the real one.

## api: login service unavailable

The CLI prints `login service unavailable, retry in a minute`; the api
logs `request` lines with status 401 and the message `identity provider
unavailable`. Logto's JWKS could not be fetched and the cached copy is
older than 24 hours (fresh copies are served for up to an hour without a
fetch).

1. `curl -s https://accounts.herakraft.co/oidc/jwks` from the control VM.
   Logto down: it is the owner's instance on their personal server (I-84).
   A 200 here means the api container cannot reach it (DNS or egress from
   the Coolify network).
2. The api recovers on the next request once the fetch succeeds; nothing
   to restart.

## api: could not create your account

A first sign-in failed with `internal: identity provider unavailable`
and no user row exists. The Management API call
(`GET <issuer>/api/users/<sub>` with the M2M client) failed.

1. Check `LOGTO_M2M_CLIENT_ID/SECRET` on the api app and that the M2M app
   in Logto holds the Management API `all` scope.
2. The user retries; the api creates the row on the first successful
   lookup. Nothing is half-created.

## api: no capacity right now

An op ended `capacity`; `repose_api_schedule_total{result="capacity"}`
rose; the CLI printed `no capacity right now; you have not been charged`.

1. `repose-admin hosts list`: no host is `ready` with free memory above
   the reserve for the class and pool room for the volume. Draining,
   unreachable and stale-heartbeat hosts do not count.
2. Add a host (11 §5) or undrain one. The user runs `repose run` again;
   the project is in `error` with `last_error = capacity` until then.

## dashboard: up but the "Cannot reach the API" bar is showing

The dashboard itself is fine (its `/healthz` is separate from the api's);
the bar (08-dashboard.md 5.5/6) means the browser's last request to
`PUBLIC_API_URL` either failed at the network level or came back 5xx.

1. Check the api from outside the browser: `curl -i
   https://api.repose.herakraft.co/v1/healthz`. A non-200 or a hang points
   at the api's own runbook rows above (Postgres down, Key Vault
   unavailable) rather than the dashboard.
2. If that curl is fine, it's CORS or the wrong origin: open the browser
   console for a `blocked by CORS policy` message, and check
   `PUBLIC_API_URL` in the dashboard's Coolify environment matches the
   api's real origin exactly (scheme and host). The api sends
   `Access-Control-Allow-Origin: *` on every `/v1` route (I-79); a proxy
   or CDN in front of it that strips that header reproduces this exact
   symptom.
3. The bar clears on its own once a poll succeeds; polling backs off to
   60 s while it thinks the api is unreachable (5.3), so a fixed api can
   take up to a minute to clear the bar, or reload the page to force an
   immediate recheck.

## dashboard: sign-in loop

The browser bounces `/` → `/callback` → `/` without ever reaching
`/projects`, or keeps landing back on `/` after visiting a page while
signed in.

1. Open the browser console during the loop. `handleSignInCallback` failing
   (auth.svelte.ts) means either the code was already consumed (a page
   refresh on `/callback`, or a browser prefetch hitting it twice — see
   `data-sveltekit-preload-data="hover"` in app.html) or `PUBLIC_LOGTO_APP_ID`
   / `PUBLIC_LOGTO_ENDPOINT` don't match the Logto application the CLI and
   dashboard were both registered under.
2. If it loops without ever reaching `/callback` at all, `signIn()` itself
   threw — almost always `PUBLIC_LOGTO_ENDPOINT` unset or unreachable from
   the browser (check it the same way as the api origin above; Logto needs
   the same cross-origin discovery fetch the api does).
3. A signed-in user may stay on `/`: the landing page no longer redirects
   (DECISIONS I-330) and shows **Dashboard** instead of **Sign in**. If a
   signed-in user still sees **Sign in** there, or a private page never
   renders, confirm `authState.authenticated` actually resolves (it stays
   `undefined` forever if `initAuth()` threw), not a Logto problem.

## api: op stuck waiting for host

`GET /ops/:id` stays `running` and the CLI shows `waiting for host`. The
host's stream dropped mid-command.

1. `repose-admin ops list --state running` and `hosts list`: the host's
   heartbeat age. Under 90 s: hostd reconnects with backoff and the api
   re-sends the command on `Hello` with the same command_id
   (`command_resend` in the log); nothing to do.
2. Unreachable for more than 10 minutes: the op fails
   `host_unreachable`, the project goes to `error`. See
   "HostUnreachable"; `repose-admin projects start` once the host is back.

## api: build failed

The revision is `failed` with the Nix error and `fragment_line`; the CLI
exits 10. The guest is untouched and the previous revision stays applied.
`repose-admin ops log <op>` has the full output (`build_logs`, secret
values already redacted). A fragment that contains a current secret value
is refused before the build with `invalid: fragment contains the value of
secret NAME`.

## No notifications arriving

A user reports nothing on their phone or in their inbox for an agent that
clearly finished.

1. `repose status` / `GET /projects/:id/events` first: if the event is not
   there at all, the problem is upstream of the outbox (the hook never
   fired, or the guest never reached the api). Check `guestd_ok` in the
   project's signals (`GuestdLost` if it is false) and, on the guest,
   whether `/run/repose/hooks.sock` exists and the agent's wrapper actually
   ran `repose-agent-setup` (`grep repose-hook` in the agent's own hook
   config file, `guest-conventions.md` "Agent wrappers").
2. If the event is there but `delivered` has no key for the channel: the
   outbox has not picked it up yet, or the channel is disabled
   (`notify_email` false, `ntfy_url` null) or over the 30/hour rate cap
   (`kind = 'notifications_paused'` events on the project in the last
   hour). `repose_api_outbox_depth` and `repose_api_outbox_lag_seconds`
   rising together mean the worker itself is stuck: it holds
   `db.LockOutbox`, so `select pg_advisory_lock_...` on the wrong replica
   or a stuck transaction is what to look for; only one replica runs it
   (`api: rollup or expiry not running on one replica` is the same shape
   for a different job).
3. If `delivered[channel]` says `"error: ..."` or `"failed: ..."`, the
   channel-specific entries below have the fix. Nothing to do if it says a
   timestamp: delivery succeeded and the miss is client-side (a stale
   ntfy subscription, a spam folder).

## repose-ask never gets its answer

A user answered an agent's question (ntfy button, email link, dashboard,
`repose reply`) and the agent is still waiting, or `repose-ask` exited 5
(DECISIONS I-244, I-245). Every step logs ids and states only, never the
question or the answer; grep by `question_id`.

1. `select state, answered_via, delivery, delivered_at, deliver_attempts,
   deliver_next_at from questions where id = '<id>'`. `pending` means the
   answer never landed: the reply link said why (expired, already
   answered), or the dashboard/CLI got a `409`.
2. `answered` with `delivered_at` null and `deliver_attempts` rising: the
   worker (api grpc process, advisory lock 1010) sends `AnswerQuestion`
   every 30 s. `command_send` lines with `kind=AnswerQuestion` and no
   `command_result` mean the host is not connected or hostd is older than
   I-244 (it answers `invalid_argument: unknown command`, which the api
   logs as the result and retries; switch the host). `result=guest_unresponsive`
   means guestd is not answering (`GuestdLost`).
3. `delivery = gone`: the guest no longer knew the question: it rebooted
   or stopped, and the ask ended with it. `given_up`: 25 hours without an
   acknowledgement. `guest`: the guest closed it itself (timeout or
   Ctrl-C) before the answer came.
4. On the guest, `ls /run/repose/questions` lists what guestd holds (root
   only), and `journalctl -u guestd | grep agent_question` shows it opened
   and closed. `guestd call answer-question '{"questionId":"<id>","status":"answered","answer":"..."}'`
   delivers by hand; the user's text is theirs, so ask before typing it.

## ntfy failing

`delivered.ntfy` carries `"error: ..."` (retrying) or `"failed: 404"` (not
retried, DECISIONS §5's 4xx rule) and the settings page shows a warning.

1. A 4xx (`400`, `404`) means the URL is wrong or the topic does not exist
   on that server any more: the user re-pastes it from
   `repose notify set ntfy <url>`, which sends a test push
   (`POST /me/notify-test`) so a wrong URL is caught immediately rather
   than on the next real event.
2. A 5xx or timeout retries on the schedule in `13-notifications.md` §5.5
   (7 attempts over roughly 24 h) before `failed`; a self-hosted ntfy
   server that is down for longer than that needs the user to re-set the
   URL once it is back, which requeues nothing retroactively — only new
   events are affected.
3. `repose_api_notify_total{channel="ntfy",result="failed"}` rising across
   many users means a widely-used relay (`ntfy.sh`) is down, not a
   per-user URL problem; check its status page before debugging further.

## Resend failing

Every email `delivered.email` value is `"error: ..."` or `"failed: ..."`
across many users at once (a per-user failure is `email: "user has no
address"`, permanent, and not a Resend outage).

1. Resend's status page and `RESEND_API_KEY`'s validity
   (`repose-admin` has no direct check; a `401` in the api's logs under
   `event=notify_fail` for the email channel is the tell — Resend's API key
   is a Coolify secret, `ops/coolify/api.env.example`).
2. A `429` retries like any 5xx (the sender treats both as retryable); a
   sustained `429` means the account's Resend rate limit needs raising.
3. `repose_api_notify_total{channel="email",result="failed"}` over 5
   percent in 10 minutes is `13-notifications.md` §6's alert threshold;
   page on it rather than waiting for a user to report silence.

## api: secret service unavailable

`PUT /secrets` returned `internal: key service unavailable`;
`repose_api_keyvault_errors_total` rose. Guest starts retry for 30
minutes (`op_retry` in the log) before failing with the same message.

1. `az keyvault key show --vault-name <kv> --name repose-dek-kek` with the
   api's identity; a 403 means the access policy lost the api's object id
   (11 §Key Vault); a timeout means egress from the Coolify VM.
2. Reads keep working from the 10-minute DEK cache; writes and cold starts
   do not. Nothing to restart once Key Vault answers.

## api: Postgres down

`/healthz` returns 503 (`db unreachable` or `migrations pending`) and
Coolify stops routing; host streams drop and hostd buffers samples for 60
minutes. Fix the database (Coolify's Postgres resource, disk, or run
`repose-admin db migrate` for the pending case); the api needs no restart.

## api: rollup or expiry not running on one replica

`rollup: not leader` (`rollup_skip`) means another replica holds the
advisory lock; only one runs the hourly rollup, the outbox, snapshot
expiry and the ops driver. Expected with two replicas. If no replica logs
`rollup_done` for two hours, `RollupLag` fires: `repose-admin billing
rollup` runs it by hand.

## api: snapshot deleted under a restore

Cannot happen by construction: the restore op marks
`snapshots.restoring_op_id` inside the same row lock the expiry job takes
with `for update skip locked`, and the job skips rows a restore holds. If
a restore fails with `not_found: snapshot was deleted`, the snapshot had
expired before the restore was enqueued; pick a newer one.

## api: duplicate results or events

Ignored by design: `ops.command_id` is unique and a result for a finished
command logs `duplicate or unknown result ignored`; `events.host_event_id`
is unique and hook events collapse on `(project, agent, kind, second)`.
Nothing to do.

## api: user reports "account suspended"

`forbidden: account suspended` on every route but `GET /me` and the
billing portal. `repose-admin users show <handle>` for the reason;
`users unsuspend` reverses it.

## Suspend a user

`repose-admin users suspend <handle> --reason "..."`: stops all their
guests (with snapshot), sets `billing_status = suspended`, revokes their
certificates, and writes an audit row. Their data is retained on the normal
30-day schedule from the moment of suspension unless `--retain` is passed.

## CLI: user cannot log in

`repose login` never completes: the browser flow times out after 5
minutes, or device code polling reports `authorization_pending` forever
and then `device code expired`.

1. Ask which path they used. Loopback (PKCE): a corporate firewall or a
   browser extension can block `http://127.0.0.1:<port>/callback`;
   `--no-browser` (or `REPOSE_NO_BROWSER=1`) forces device code, which
   only needs outbound HTTPS.
2. `curl -s https://accounts.herakraft.co/oidc/.well-known/openid-configuration`
   from the user's machine: a failure here means the CLI cannot even
   start (`Cannot reach <issuer>`, exit 1), independent of the flow.
3. Confirm the `repose-cli` Native application in Logto still has the
   loopback redirect (`http://127.0.0.1:*/callback`) and device flow
   enabled (`ops/AZURE-SETUP.md` step 12); a removed or misconfigured app
   answers `invalid_client` on the token exchange.
4. `repose logout --purge` clears any half-written `credentials.json` or
   `~/.ssh/repose/` state before retrying.

## CLI: certificate rejected

`repose run`/`attach`/`open` gets a gateway banner (`certificate not
valid for this project`, or a stopped-project banner) instead of a shell,
or `POST /certs` itself fails.

1. `repose certs`... there is no such command; the certificate lives at
   `~/.ssh/repose/id_ed25519-cert.pub`. `ssh-keygen -L -f
   ~/.ssh/repose/id_ed25519-cert.pub` shows its principals and expiry —
   confirm the failing project's id is in `Principals` and `Valid` has
   not passed (12h lifetime, R3-9).
2. A missing or expired principal means the cert predates the project
   (created on another machine, or before this project existed locally):
   delete the cert file and re-run; `ensureCert` reissues one covering
   every project the account has.
3. `POST /certs` answering `rate_limited` (10/min) is expected under
   rapid repeated runs; the CLI reuses the certificate on disk if it still
   has validity and only warns. If none is on disk yet, wait a minute.
4. A banner that is not one of the two above (gateway-side rejection, not
   yet built as of 07-cli.md's landing — see 06-gateway-edge) surfaces
   verbatim with exit 1; file it against the gateway, not the CLI. Since
   DECISIONS I-149 the CLI re-issues the certificate once on its own when
   the gateway answers `Permission denied` before giving up.
5. `Enter passphrase for key '~/.ssh/id_ed25519'` on every command is a
   CLI from v0.1.4 or earlier, which certified the user's own key; a
   current CLI uses `~/.ssh/repose/id_ed25519` (no passphrase) and moves
   over by itself on its next command. Upgrade with `install.sh`.
6. `ssh <slug>.repose` from a plain terminal does not resolve: the CLI
   prints why after every `run` (the `Include` line is after a `Host`
   line, or `~/.ssh/config` is a read-only link, I-151) with the exact
   line to add; `ssh -G <slug>.repose | grep -i hostname` must say
   `ssh.repose.herakraft.co`.

## CLI: SSH timeout after running

`Guest is running but SSH did not answer in 60s.` after the API already
reports the project `running`.

1. `repose logs --kind console` — sshd not started yet (guest still
   booting past `running`), or a boot failure, both show here.
2. `repose status` for `sessions`/`tmux clients`: if the API's state is
   stale (host lost contact), `hosts` on the host-01 side and
   `repose-admin hosts show` tell you whether the host itself is
   reachable; a `sessions` count that never reflects change means the
   API's view of the guest is the stale part, not SSH.
3. Retry `repose run`/`attach` once more before escalating: the 60s
   window is deliberately short so an agent's user is not left staring at
   a hang; the guest is very likely still coming up.

## CLI: a laptop tool is missing in the guest

The user has a tool on the laptop (or the project's scripts run one) and
the guest says `command not found`, or the run said `Could not install
<tool>: ...` (DECISIONS I-221, I-222).

1. `repose scan` in the checkout, on the laptop: is the tool in the list?
   If not, it was not found where its manager installs (a tool installed
   from a laptop checkout, a `(devel)` Go build or a cargo path install
   is left out on purpose) or the project's dependencies provide it.
2. In the guest (`repose attach`): `cat ~/.repose/tools-install.log`
   holds each install's command and output; `~/.repose/tools/failed`
   lists what will not be tried again until the laptop's entry changes.
   Deleting that line and `~/.repose/carry/tools` makes the next `repose
   run` try again.
3. `systemctl --user status repose-tools-carry` shows whether a pass is
   still running (installs are in the background; `nix profile add` of a
   large package can take minutes). `$XDG_RUNTIME_DIR/repose-installing`
   lists what it is still installing.
4. A Go or cargo fallback install lands in `~/.local/bin`, which is on
   the login PATH; a shell started before the install needs `hash -r`.

## PaddleWebhookRejected

Five or more deliveries in ten minutes failed the `Paddle-Signature`
check (`webhook_received` lines with `result=bad_signature`; the body is
never logged). Two causes, in order of likelihood:

1. **`PADDLE_WEBHOOK_SECRET` is not this destination's endpoint secret.**
   Paddle's dashboard > Developer tools > Notifications shows the
   destination and its secret; a sandbox secret on a live deployment, or a
   second destination, is the usual story. Paste the right one into the
   Coolify environment of `api` and `api-grpc`. During a rotation the api
   accepts the old and the new secret side by side (`billing.NewWebhooks`
   takes extra secrets), so no delivery is lost.
2. **The clock.** A `ts` more than five minutes from the api's clock is
   refused. Check NTP on the control VM before anything else.

While it fires, no subscription or payment event is applied: accounts do
not move to `past_due`, back to `active`, or to `trial` after a checkout.
Paddle retries failed deliveries for three days, so fixing the secret
inside that window replays everything; past it, the destination's page in
the dashboard resends the missed events, and the `paddle_events` primary
key drops the ones that did arrive.

If neither is true, someone is posting at the endpoint. It is
unauthenticated by design (the signature is the authentication) and a
forged body cannot pass, so this is noise rather than an incident; the
rate limit in front of the api is the answer if it becomes constant.

## OverageChargeFailed

`repose_api_billing_overage_charges_total{result="error"}` moved: the
hourly job computed a period's egress overage, recorded it in
`overage_charges`, and Paddle refused the one-time charge
(`overage_charged` lines with `result=error` carry Paddle's code). The row
stays without `paddle_transaction_id`, the period is not marked charged,
and nothing sends it again by itself, because a second attempt could
double a line Paddle did accept after answering an error.

```
repose-admin billing show <handle>              # "overage lines": the period, the GB, the cents, "transaction none yet"
repose-admin billing overage-now <handle>       # sends this period's line if it is not on record; says so if it is
```

Read Paddle's error first: `subscription_locked_processing` means Paddle
is billing the subscription right now (wait ten minutes and retry);
`entity_not_found` means the subscription id in our table is not
Paddle's (compare with the dashboard). Then, if the period has not
billed yet, delete the row and run `overage-now`, which records and sends
it again:

```
delete from overage_charges where subscription_id = 'sub_...' and period_start = '2026-10-01';
```

If the invoice already went out without the line, add the charge in
Paddle's dashboard (Subscriptions > the subscription > Charge one-time)
for the recorded cents and put the transaction id on the row, so `show`
and `explain` agree with what was charged. Paddle locks the invoice about
thirty minutes before `next_billed_at`, which is why the job runs three
hours ahead.

## BillingStopped

`repose_api_billing_stops_total` moved: the api stopped a tenant's running
machines by itself, with a snapshot, for one of three reasons (the label):

- `past_due`: three days after a failed payment (PRICING.md "Failed
  payments"); the account is `suspended` with `suspended_reason =
  billing`, `billing_stopped` went to the user, and a payment lifts it by
  itself (`transaction.completed`).
- `ended`: the subscription reached its end (cancelled at period end, or
  Paddle cancelled it); `subscription_ended` went to the user; the account
  is `none` and a new checkout is the way back.
- `egress`: the period's egress passed four times the plan's allowance;
  `egress_stopped` went to the user; starts are refused with
  `egress_limit` until `period_end`, or sooner on an upgrade.

Nothing is deleted for 30 days. `repose-admin billing show <handle>` shows
which; the `events` table has the email that went out. There is nothing to
fix unless the reason is wrong: an `egress` stop for a tenant with a
legitimate workload is a conversation about a bigger plan, not a bug.

## Customer disputes a charge

`repose-admin billing show <handle>` first: the subscription, its plan and
status, the period, what ran (hours per class), the disk allocated, the
period's egress and the overage arithmetic
(`ceil(egress GB - included) x 5 cents`), and the overage lines recorded
with their Paddle transaction ids. Then Paddle's dashboard for the
transaction itself: its lines are the plan's monthly price, tax for the
buyer's country (Paddle's, as merchant of record), and at most one
`Egress overage` line whose description names the GB and the period.

The three that come up:

- **"I was charged after I cancelled."** Cancelling ends the plan at
  `period_end`; the charge on the day of cancelling is that period's
  renewal if it fell on the same day. `show` prints `cancels at`; Paddle's
  transaction list shows the timing. A refund within 14 days of the first
  charge is policy (PRICING.md "Refunds"); a renewal is not refunded for a
  part period.
- **"What is this egress line?"** `repose-admin billing explain <project>
  <hour>` for any hour of the period prints the period egress over every
  project of the account and the arithmetic to the cent; the Abuse
  dashboard's per-project egress panel shows which project sent it. The
  line is one per period and matches `overage_charges` exactly.
- **"I did not use it."** A plan is not metered: the month costs the same
  with the machine stopped. `show` prints the hours anyway; a user who
  wants to stop paying cancels, and the plan runs to the period's end.

A refund is made in Paddle's dashboard (Transactions > the transaction >
Refund), where it lands on the same card; record the reason in
the refund's note in Paddle, which is the trail.
Nothing in `usage_hours` or `overage_charges` is edited: they are the
record of what was used and what was sent.

## Move a user between plans by hand

The dashboard's plan page (`POST /billing/plan`) is the normal path: an
upgrade takes effect at once, prorated by Paddle; a downgrade is scheduled
for `period_end` and refused while the account would not fit. By hand,
when the user cannot reach the dashboard or Paddle refused the change:

1. In Paddle's dashboard, Subscriptions > the subscription > Change
   items: replace the price with the other plan's (`PADDLE_PRICE_SOLO`,
   `PADDLE_PRICE_PLUS` and `PADDLE_PRICE_PRO` in the api's environment
   name them), proration
   "prorated immediately" for an upgrade and "prorated next billing
   period" for a downgrade.
2. Paddle sends `subscription.updated`; the webhook writes the new plan
   and seats on the `subscriptions` row and emails `plan_changed`.
   `repose-admin billing show <handle>` confirms the plan within a minute.
3. If the webhook is down (PaddleWebhookRejected), the row lags Paddle.
   Do not edit `subscriptions.plan` by hand: fix the webhook and resend
   the event from the destination's page; the row follows.

A downgrade that Paddle accepts while the user runs more than the smaller
plan allows is not a problem for the api: the next start is refused with
`plan_limit` naming the machines, and running ones keep running until
stopped. `SEATS_TOTAL` bounds upgrades: with no free seat the api refuses
`no_seat`, and by hand you would be overselling the host.

## Claude asks for a login in every project (login share, I-278)

A tenant logged in to Claude Code in one machine and another still asks,
or all of them ask again at once.

1. In the machine that asks: `repose-admin exec <id> -- findmnt
   /home/dev/.claude/.credentials.json`. No output means the share is not
   mounted: `journalctl -u repose-claude-auth -b` in the guest says why
   ("no Claude login share on this host" = no `claude-auth` tag).
2. On the host: `systemctl status virtiofsd-auth@<guest id>` and
   `grep claude-auth /var/lib/repose/guests/<guest id>/ch.args`. The tag is
   only attached when that unit came up before the hypervisor; hostd logs
   `auth_share` with the reason when it did not (a guest with no user id
   has no share by design). A restart of the project (`repose stop`, then
   `start`) retries.
3. All machines asking at once after they worked: the file was emptied or
   corrupted (an agent, or a refresh cut off by a host crash mid-write).
   `ls -l /var/lib/repose/users/<user_id>/claude-auth/` shows its size; do
   not open it. The tenant runs `/login` once in any machine and every
   machine is signed in again.
4. Every refresh failing right after a base publish with a new Claude
   Code: the in-place fallback may be gone. `ops/dev/claude-auth-trace.sh`
   in an e2e machine shows it; roll the base back (DECISIONS I-278).
5. The share is the user's own 16 MiB volume (DECISIONS I-464):
   `findmnt /var/lib/repose/users/<user_id>/claude-auth` on the host must
   show `/dev/mapper/vg--guests-auth--<user_id>`. `df -h` and `df -i` on
   that path show a full volume (a guest wrote junk there); it fills only
   that user's share. `auth_share` with `no space` or a mount error means
   the guest booted without the share. After the release that brought the
   volume, each user signs in once more per host: the old share was
   renamed `claude-auth.legacy/` and is swept once none of their guests
   runs.

A user's directory is removed by hostd 30 days after their last guest on
that host (`auth_share_sweep`). To turn the share off host-wide, set
`repose.host.claudeLoginShare = false` and switch the host (hostd then
runs with `--claude-login-share=false`): guests started after that boot
without it and keep a login of their own.
