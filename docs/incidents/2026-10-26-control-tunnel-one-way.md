# 2026-10-26: the control VM restarted, its tunnel came back one way, and nobody could reach a machine for 9 hours

Azure stopped the control VM at 07:46:55Z and it was back at 07:50:43Z.
Its WireGuard tunnel to the edge never re-established: packets left the VM
every 5 s and none arrived. The gateway therefore could not refresh its CA
or revocation list, and once they passed `StaleAfter` (1 h) it refused
every new connection with `gateway cannot reach control plane; try again
shortly`. From about 08:50Z to 17:09Z no `repose ssh`, `run`, `attach` or
`open` worked, for anybody. Guests kept running throughout; the agents
inside them kept working, and tmux sessions survived.

No alert fired in those nine hours. The owner found it by running `repose
attach` and reading the refusal.

Decisions: I-866, I-868, I-870.

## Timeline (UTC)

| Time | What |
|---|---|
| 2026-10-25 23:28 | v0.1.41 pushed; Coolify deploys api, api-grpc and web by 23:41. Unrelated, but it is the release the outage ran on. |
| 00:14 | A `tofu apply` for host-02 starts from another session and takes the prod state lock. Its install fails from 00:16 and it retries for 17 h. Unrelated to this outage, and it is why no apply was possible during it. |
| 07:46:55 | The control VM's journal stops mid-run. No shutdown sequence: Azure stopped the VM. |
| 07:50:43 | The VM boots (new boot id; waagent logs the change). `wg-quick@wg0` comes up on ephemeral port 51275, because `wg0.conf` had no `ListenPort`. |
| 07:49 to 07:50 | The gateway's last successful refresh of the revocation list and the CA. |
| from 07:50 | Handshake initiations leave `eth0` every 5.6 s and never arrive at the edge. The edge keeps answering 20.121.138.150:**49007**, the port the VM used before the restart, which is now closed. Both directions are dead and neither end can repair it. |
| 08:50 | The caches pass `StaleAfter`. Every new connection is refused with `gateway cannot reach control plane`. |
| 08:50 to 17:09 | `route_fail` every 10 s in the gateway's journal. `ApiDown` should have paged at 07:53 — Prometheus scrapes the api over that same tunnel. Whether it fired and was not delivered is still open. |
| 16:59:45 | The owner's laptop retries `repose attach` enough times to ban its own address for 10 minutes: each refusal counted towards the ban. The error it reads is `kex_exchange_identification: read: Connection reset by peer`, which says nothing about the cause. |
| 17:09:4x | `wg set wg0 listen-port 51820` on the control VM. Handshake within 12 s, 1 ms RTT, `/internal/ca` in 5.7 ms, no refresh failure since. |
| 17:12 | `ListenPort = 51820` written into the live `/etc/wireguard/wg0.conf` (backup `wg0.conf.bak-20261026`), so the next restart cannot repeat it. |

## Why the tunnel stayed down

The edge's static peer for the control plane carries no endpoint
(`infra/README.md`): the edge learns the VM's address and port from the
packets it receives, and WireGuard updates a peer's endpoint only on a
packet that authenticates. So a restart on a new source port can only be
repaired from the VM's side, and only if its packets arrive.

They did not arrive. The flow 10.200.3.4:51275 → 20.102.98.254:51820 was
blackholed in both directions, while the path itself was healthy:

- `tcpdump -ni eth0 "udp port 51820"` on the VM: a 148-byte initiation
  every 5.6 s, correct destination.
- `grep dport=51820 /proc/net/nf_conntrack` on the edge: three flows (the
  host, the monitoring peer, and the control plane's **stale** 49007
  entry, kept alive by the edge's own retries). Nothing from port 51275.
- The edge's `wg show` for that peer: `latest handshake` 9 h old, rx
  frozen, tx climbing.
- `nc -u -p 40001 <edge ip> 51820` from the same VM, at the same moment:
  arrives, and appears in the edge's conntrack at once. So not the NSG
  (which admits 51820 from `Internet`), not nftables (which accepts
  51820 from any source), not routing, not the clock (both ends in sync,
  which rules out a replayed handshake timestamp), and not the keys.

Rebinding the socket to 51820 brought the handshake up immediately. Why
that one 5-tuple was dropped is Azure's side and is not established here.
The VM runs MANA accelerated networking and had just been moved, which is
the same family as the host-01 live migration of 2026-10-10
(`2026-10-10-host-01-live-migration.md`), but there is no evidence tying
the two beyond the shape: Azure reports the VM healthy, the network says
otherwise.

## Why it took 9 hours

1. The port was ephemeral, so a restart was a coin toss and the edge's
   stored endpoint was wrong the moment the VM came back. Fixed:
   `ListenPort = 51820` in the cloud-init template, in `infra/README.md`
   and on the live VM (I-866).
2. Nothing watched the one signal that was true the whole time. The
   gateway was up and scrapeable, so `GatewayDown` and
   `MonitoringPathDown` stayed quiet by design; `ApiDown` reads the api
   through the same dead tunnel, so it is not an independent witness.
   Fixed: `GatewayCachesStale` pages on
   `repose_gateway_revocation_cache_age_seconds > 900`, 45 minutes before
   the gateway starts refusing users (I-870).
3. The refusal counted towards the per-source ban, so retrying made it
   worse and the message the user finally saw named neither cause. Fixed:
   a refusal the gateway makes because its own caches are stale does not
   count (I-868).
4. The runbook's "Control plane cannot reach the edge network" had no case
   for a tunnel that is dead one way. Fixed: step 4 there, with the
   two-sided capture that tells a blackholed flow from a blocked path.

## Open

- Whether `ApiDown` fired at 07:53 and was not delivered, or did not fire.
  Until that is answered, assume paging is unproven for this class of
  outage and `GatewayCachesStale` is the only new witness.
- A one-way tunnel still needs no human: neither end rebinds its socket
  when it has sent for minutes and received nothing. `GatewayCachesStale`
  pages a person to run one command. Worth a watchdog on the control VM;
  not built here.
