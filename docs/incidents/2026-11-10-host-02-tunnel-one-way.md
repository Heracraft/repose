# 2026-11-10: host-02's tunnel came back one way after a live migration, and the only repair left was a restart

Azure live-migrated host-02 at about 01:40:50Z. Its WireGuard tunnel to
the edge came back one way: the host's handshake initiations reached the
edge and authenticated, and the edge's answers never arrived. From
01:41:20Z nothing reached the host or its guests — no `repose ssh`,
`run`, `attach` or `open` for the three machines on it, no metrics, no
logs, and no operator ssh, because the host's sshd binds the WireGuard
address and nothing else. The three guests kept running throughout and
the api kept seeing the host `ready`, correctly, because hostd's
heartbeat travels the provider NIC and not the tunnel.

The outage was found by the owner, who said "gateway might be down". The
gateway was up and had been for a month. What fired was `HostdDown` and
`HostScrapeDown`, both of which name hostd, which was healthy the whole
time; `HostWgDown`, the alert whose RUNBOOK section describes exactly
this, had never been written as a rule.

Neither end repaired itself, and no way into the host existed: ssh went
through the dead tunnel, Azure Run Command is disabled on hosts, and the
serial console needs a root password whose hash had never been placed on
host-02. So the repair was `az vm restart`, which the owner authorised as
the guest park's first production test. The park saved all three
machines' memory and they came back with their agents running.

Decisions: I-1068, I-1070.

## Timeline (UTC)

| Time | What |
|---|---|
| 01:40:49 | host-02's kernel logs the live-migration signature: `hv_utils: TimeSync IC version 4.0` and `Heartbeat IC version 3.0` re-initialising, `sched: DL replenish lagged too much`. |
| 01:40:51 | `systemd-resolved: Clock change detected. Flushing caches.` — the resumed VM. |
| 01:40:53 | `hv_storvsc ... status: scsi 0x2 srb 0x6 host 0xc0000001`, then `hv_balloon: Max. dynamic memory size: 262144 MB`. No reboot: PID 1 and the per-minute `repose-kernel-check` run straight through. |
| 01:40:55 to 01:40:57 | guest `01a1bdec` logs `the docker socket did not answer` and `list tmux windows: setpriv: context deadline exceeded` — the guest noticing the freeze, five seconds after it, and the reason the owner first suspected a 500 MB paste. |
| 01:41:20 | The last completed handshake for host-02's peer on the edge. Nothing after it. |
| 01:41:24 | host-02's last line in Loki. Fluent Bit ships over the tunnel. |
| 01:42:13 | The gateway's first `dial_fail` with `reason: timeout`, then one per attempt for three projects. |
| 01:43:10 to 01:43:12 | Two relays close on `keepalive` after 63 and 67 minutes. |
| ~01:44 | `HostdDown` and `HostScrapeDown` become due. Neither names the tunnel. |
| 02:18 | Diagnosis complete: the host answers ICMP on its VNet address from both the edge and the control VM, hostd's heartbeat is current, 3 guests running, 186 GB of 252 GB free. Alive; only the tunnel is dead. |
| 02:20:56 | `az vm restart -g repose-prod -n host-02`, the owner's authorisation, taken as the park's prod test. |
| 02:21:21 to 02:22:57 | The park holds the shutdown for 96 s and parks all three guests. |
| 02:22:57 | A clean systemd shutdown: `Reached target System Power Off`, filesystems synced. |
| 02:23:27 | Boot. |
| 02:24:02 to 02:24:44 | hostd restores all three from their images: `restored=3 images=3 duration_ms=60263`. `image key fetched; guests can be parked`. |
| 02:23:47 | The tunnel's first handshake since 01:41:20. A fresh source port is all it took. |
| 02:25 to 02:26 | All three reachable through the gateway, `guestd_ok=true`, agents back. |

## What broke, exactly

The tunnel leaves the VNet at all only because the endpoint hosts dial is
the edge's **public** address. host-02 has no public IP, so its packets
left through `natgw-hosts` and came back to the edge's public address, to
reach a machine 2 ms away in the same VNet. The migration lost the NAT
gateway's mapping for that flow in the return direction.

The evidence that it was one-way rather than dead, which is what made the
repair obvious once it was in hand:

- The edge received 148-byte initiations continuously (`rx` +592 B per 20 s)
  and sent answers (`tx` rising) with `latest handshake` frozen.
- Removing and re-adding the peer on the edge changed nothing, and the
  edge **re-learned the host's endpoint from its initiations** — which
  WireGuard only does for a packet that authenticates. So the host's
  packets were arriving and verifying, and only the answers were lost.
- Both WireGuard peers that egress through `74.235.105.146` stopped in the
  same second, and `nic-host-02` is the only prod NIC behind it. The two
  peers on other addresses (control plane, monitoring) kept handshaking
  every two minutes throughout.
- `natgw-hosts` reported `DatapathAvailability` 100 and `PacketDropCount`
  0 across the window. As on 2026-10-26, why one five-tuple stopped
  passing is Azure's side and is not established here.
- `nsg-hosts` carries no custom rules, so inbound from the internet falls
  to `DenyAllInBound` and the return leg is allowed only by flow state. A
  NAT gateway does no inbound translation either way, so no rule change
  could have reopened the path; only a new mapping could.

## Why it took 42 minutes, and why the repair was so heavy

1. **Nothing rebinds a socket.** Both ends retried the same dead
   five-tuple for as long as it took a person to ask. This is the second
   occurrence — the control VM on 2026-10-26 ran nine hours the same way
   — and that incident file closed on this exact gap. Fixed: I-1068.
2. **The alert for it did not exist.** RUNBOOK's `HostWgDown` section
   named an alert `host_wg_down` that was never a rule, and
   `HostScrapeDown`'s own runbook link already pointed at that section.
   What paged said hostd was down while hostd was fine. Fixed: I-1070.
3. **No out-of-band access.** host-02 had no console password hash, so
   the serial console could not be logged into; Run Command is disabled;
   ssh runs over the broken tunnel. One command on the host would have
   fixed this in seconds. Not fixed in code: `docs/CHECKLIST.md`'s
   console row now names every machine and why, and placing the hash is
   still a by-hand step at provisioning.
4. **The api's `nixos_system` is written at registration and never
   updated.** Mid-incident it reported host-02's Nov 2 system, which
   still had the pre-rename `--freeze` flags, and I used it to confirm
   the park was armed. The conclusion happened to be right and the
   evidence was eight days stale; the running system was the Nov 3 one
   with `--park`. Not fixed.

## The park's first production run

Authorised as the test. All three guests parked and all three restored.

| Guest | Project | Park outcome | Park ms | Image | Unpark ms |
|---|---|---|---|---|---|
| 01a19a11 | kanali | parked | 42,839 | 7.4 GiB | 18,317 |
| 01a1938f | verista | parked | 55,999 | 13.0 GiB | 22,459 |
| 01a1bdec | shule-id | parked | 80,398 | 15.1 GiB | 60,230 |

36 GiB written inside a 96 s hold against the 240 s deadline, on a budget
of 240 s × 400 MB/s. `shule-id`'s restore was dominated by
`image_read_ms` 56,088. Afterwards each guest's `uptime` spanned the
restart (3 days, 7 days 4:49, 23:35), each had its tmux session, and
`shule-id`'s nine agent windows were back with a load average of 16.6.
`park_discard` removed each image after its restore, as designed.

`I-645` was **not** exercised: the park pre-empted it, so no guest was
marked interrupted and the api's resume path never ran.

## Open

- The private-endpoint fix for the hairpin is not made. It needs a
  per-host endpoint or a fallback, because the endpoint is one global
  setting, WireGuard holds one endpoint per peer with no failover, and a
  host outside the edge's VNet would then never connect at all. I-1068
  records the shape; the decision is the owner's.
- The watchdog is on hosts only. The 2026-10-26 occurrence was the
  control VM, whose wg-quick is not managed by `nix/hosts`, and the edge
  uses `networking.wireguard`. Both still need a person.
- Nothing publishes the absence of a console hash, so the next host added
  without one is silent again in exactly the way host-02 was.
- `ListenPort` is still unpinned on hosts (I-866 pinned it for the
  control VM only). It is what lets the watchdog's rebind work at all, so
  pinning it and the watchdog cannot both be the contract.
- The edge's WireGuard private key was printed into this session's output
  by a `wg show wg0 dump`, whose first field is the private key, and read
  as the public key. It should be rotated at a window when every host is
  reachable: a new `/var/lib/repose/edge/wg.key`, `repose-admin edge init
  --pubkey`, and each host's `host.json`. The watchdog's script carries a
  comment never to run `dump` for this reason.
