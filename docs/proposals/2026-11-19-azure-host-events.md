# Three Azure host events in one evening, and nobody was told (proposal, 2026-11-19)

**Status: research, saved. Nothing here is decided and nothing is to be built
from this file.** On 2026-11-19 Azure did host-side maintenance under all three
production VMs in eastus zone 1. host-02 was unreachable for 3 h 15 m with five
tenants' guests on it. No alert reached the owner. This file says why, what
closes each gap, and what each fix costs. The runbook entry it asks for is
already written, in the same branch: `docs/ops/RUNBOOK.md`, "Host or control VM
dead on the network".

Sources: the conductor's own measurements of the evening; read-only Azure
queries run from this branch at 20:30–21:35Z (activity log, Resource Health,
`VmAvailabilityMetric`, instance views); `ops/alerts.yaml`,
`ops/alerts-loki.yaml`, `ops/prometheus/`, `ops/grafana/`,
`infra/azure/modules/environment/alerts.tf`, `internal/api/hostmgr/hostmgr.go`,
`internal/hostd/{app,guest,park,schedev}`, `nix/hosts/`, `nix/edge/`;
`docs/ops/OBSERVABILITY.md`, `docs/ops/RUNBOOK.md`, DECISIONS I-472, I-473,
I-643, I-645, I-648, I-649, I-653, I-848, I-942, I-944, I-956, I-1044, I-1068.

## Summary

| Question | Answer |
|---|---|
| What Azure did | A failed live migration under host-02 (16:20:57Z), a host-failure redeploy of edge-01 (18:21:48Z), and an unannounced pause of coolify-01 (19:10Z). Each left the VM running and its NIC dead. |
| What it cost | host-02 dark 16:21 → 19:36+ (3 h 15 m, five guests); edge-01 down 18:21 → 18:27 (6 m, every session); coolify-01 down 19:10 → 19:37 (27 m, api, grpc, web, Postgres). |
| Why no Azure email | Measured: host-02's Resource Health stayed **Available**, so `repose-prod-vm-health` cannot match it; `VmAvailabilityMetric` stayed **1.0 for the whole three hours** on host-02 and coolify-01, so no availability alert can fire; coolify-01 produced no health event at all; edge-01's did fire, and arrived **59 minutes late**. |
| Why no repose page | Google Chat is **not in the alerting path**. I-848 deleted calert and the Chat route, I-956 deleted the Alertmanager that replaced it. Today delivery is one contact point the owner clicks into Grafana's UI, which this repo neither provisions nor checks (I-944), and a rule in this repo is not a rule in Grafana (I-1044). |
| Who knew first | The api, at 16:22:42Z, 90 s after the last heartbeat (`UnreachableAfter`, `internal/api/hostmgr/hostmgr.go:45`). It wrote a log line, set a column, moved a gauge, and told nobody. |
| Cheapest fix | The api pages the owner's Chat space itself when it marks a host unreachable. It already holds a Google Chat sender. 3 h becomes ~2 min. |
| Second cheapest | One probe outside Azure **and** outside the personal server, reading each VM's guest-agent heartbeat through `az vm get-instance-view`. That is the only Azure signal that was true all evening. |
| Third cheapest | The edge keeps its WireGuard peer set across a reboot. Tonight the edge came back with no peers, because only wgsync restores them and wgsync needs the api, which died 43 minutes later. |
| Can a host save itself | Yes, in part. The park needs no network (the wrap key is cached, refreshed hourly), so a network-dead host can park its guests and reboot. It cannot bring them back: the unpark needs Key Vault, and an unparked guest needs the api to start it. |
| Blast radius | One host, one edge, one control VM, one zone (`zone = "1"`). A second edge is $96 a month plus a load balancer; a second host is $772 to $3,419. Neither shortens a 3-hour detection gap. |

## 1. Detection: why three hours passed in silence

### 1.1 What each layer knew, and when

| Time (UTC) | Who knew | What it did with that |
|---|---|---|
| 16:20:52 | Azure | Posted a Resource Health event, `Live Migration Failed`, `currentHealthStatus: Available`. |
| 16:21:11 | the api | hostd's gRPC stream dropped. |
| 16:22:42 | the api | Marked host-02 `unreachable`, logged `host_unreachable`, set `projects.host_unreachable`, moved `repose_api_host_state`. Notified nobody. |
| ~16:24 | Grafana | `HostUnreachable` (2m, page) and `HostdDown` (3m, page) should both have gone firing. Whether they did, and where they went, is answerable only by asking Grafana. |
| 18:21:48 | Azure | edge-01 Available → Unavailable. |
| 19:10 | nobody | coolify-01's NIC died. Azure never raised a health event for it. |
| 19:21:05 | Azure Monitor | `repose-prod-vm-health` fired — for edge-01's 18:21 event, 59 minutes late, 54 minutes after the edge had recovered on its own. |
| 19:36 | the owner's conductor, by hand | `az vm restart` on coolify-01. Back in seconds. |

The api is the first thing to know, by a margin of about two minutes, and it is
the one component with a working outbound path to the owner. `Sweep`
(`internal/api/hostmgr/hostmgr.go:663`) runs every 15 s in api-grpc
(`internal/api/app/app.go:633`) and marks a host unreachable after
`UnreachableAfter = 90 * time.Second`. Nothing in `internal/api/notify` is
called. `host_unreachable` is not an event kind
(`internal/api/events/events.go`), has no verb in `internal/eventverbs`, and no
rule in `ops/alerts-loki.yaml` matches it — that file holds exactly one rule,
`KernelOops`. The only path from the api's knowledge to a human runs:
api-grpc's gauge → Prometheus over the edge's WireGuard → Grafana →
a contact point → the owner. Six hops, and three of them were in tonight's
blast radius.

### 1.2 The named gap

**Google Chat is not wired to anything.** I-648 paged to Chat through calert.
I-848 deleted calert and the Chat path for Alertmanager email. I-942, I-944 and
I-956 deleted Alertmanager. `ops/grafana/push.py` pushes rules and nothing else,
on purpose: "NO CONTACT POINT AND NO NOTIFICATION POLICY". `ops/README.md` warns
that a fresh Grafana 12.4 ships a default policy whose receiver is `empty`, so
pushing rules alone delivers nothing. `docs/ops/OBSERVABILITY.md` says it
plainly: "A fresh Grafana delivers nothing until both of those are done."

So the owner's paging channel is a thing in a UI that no file in this repository
describes, no test asserts and no check reads. I-944 refused to provision it, for
a good reason (provisioning a notification policy replaces the org's whole
notification tree and makes the UI refuse to edit it). That reason still holds.
What does not hold is leaving the result unchecked.

**And a rule in this repository is not a rule in Grafana** (I-1044, recorded, not
fixed). That decision already names the fix — "a `--verify` that exits non-zero
on drift, run wherever the token lives" — and already names the 18-hour
`KernelOops` window as the cost paid for not having it. Tonight is the second
invoice.

**There is no heartbeat and no external check.** `Watchdog` (`ops/alerts.yaml`,
`repose-meta`) is in the file as the record of a design deliberately not
deployed, excluded by `ops/grafana/alerts-gen.py`, because the owner's Uptime
Kuma runs on the same server as Prometheus and Grafana. Uptime Kuma has **zero**
repose monitors described anywhere in this repository. There is no blackbox
exporter and no synthetic probe of `repose.run`, `api.repose.run` or
`ssh.repose.run`. `ops/deploy-probe.sh` is a hand-run curl loop for a deploy
window, not a monitor.

### 1.3 What closes each gap, where it lands, and what it costs

**(a) A host unreachable for 90 s pages the owner from the api.** The api already
holds a Google Chat sender (`internal/api/notify/gchat.go`, host pinned to
`chat.googleapis.com`). Add an operator space webhook as one api environment
value, and call it from `Sweep` on the transitions in both directions (marked
unreachable; heartbeat back). Files: `internal/api/hostmgr/hostmgr.go` (the
`marked` loop already exists), a small operator sender beside
`internal/api/notify/gchat.go`, `internal/api/app/config.go`,
`ops/coolify/api.env.example`, `docs/ops/OBSERVABILITY.md`,
`docs/ops/RUNBOOK.md`. Cost: a few hours, $0, one more ops secret in the api's
Coolify environment (an ops credential, not a tenant secret, so the three-homes
rule is untouched — say so in the decision entry). Buys: host-02's three hours
becomes about two minutes, for every future dead host, with no dependency on
Prometheus, Grafana, the edge's tunnel or a UI setting. Does not cover: the api
itself dying, which is (b).

**(b) One external probe, outside Azure and outside the personal server.** It
reads, every minute or two:
- each VM's guest-agent heartbeat, `az vm get-instance-view -g repose-prod -n <vm>
  --query "instanceView.vmAgent.statuses[].[displayStatus,time]"` — the one
  signal that was true all evening, on all three VMs, and the one Azure Monitor
  has no rule for;
- `https://api.repose.run/healthz` and a TCP banner on `ssh.repose.run:22`;
- Grafana's `/api/health`, which is what `Watchdog` was for.

It posts to the owner's Google Chat space on any transition. Where it lands: a
scheduled workflow in this repository, `.github/workflows/fleet-probe.yml`. The
repository is public, so Actions minutes are free, and GitHub is a third failure
domain — neither Azure eastus zone 1 nor the owner's server. Cost: $0 of compute;
one Chat webhook and one Azure service principal (OIDC federated credential, no
stored key) as Actions secrets; about half a day. Two honest costs to weigh:
Actions cron is best-effort and can slip past its minute, and a red run on a
public repository tells the world the fleet is down. The alternative home is a
systemd timer on woker-1 beside `ops/digest/`, which is a different resource
group and a different Azure host but the same region and the same subscription.
Buys: the edge's 6 minutes and the control VM's 27 minutes become one probe
interval, and the agent-heartbeat read catches a dead-NIC host too.

**(c) Grafana drift is checked, and the check pages.** `ops/grafana/verify.py`
gains a mode that exits non-zero when the pushed rules differ from
`ops/alerts.yaml`, when the rule count is zero, or when the default notification
policy's receiver is `empty`; a timer on the personal server runs it daily and
sends the result through the same channel as (a) and (b). Files:
`ops/grafana/verify.py`, a unit and timer beside `ops/digest/`,
`docs/ops/OBSERVABILITY.md`, `docs/ops/RUNBOOK.md`. Cost: a day, $0. Buys:
I-1044's unbounded gap becomes a day. It does **not** page on an outage; it pages
on the alerting path being broken, which is the thing nobody could rule out
tonight.

**(d) The edge's wgsync failing is visible.** `repose_gateway_wgsync_peers` and
`repose_gateway_wgsync_errors_total` are declared
(`internal/obs/metrics/families.go`) and incremented in `Sync`, but
`cmd/gateway/wgsync.go` builds its own registry and never starts a listener —
`METRICS_LISTEN` is read only by `gateway serve`, which runs no `WGSync`. So the
two series do not exist in Prometheus, while `GatewayDialFailing`'s description
tells the operator to go and read one of them (`ops/alerts.yaml`). Fix: give
`wgsync` its own metrics listener on the edge's WireGuard address and a scrape
job, then one rule — peers below the api's ready-host count, or errors rising,
for 10 minutes. Files: `cmd/gateway/wgsync.go`, `nix/edge/default.nix`,
`ops/prometheus/prometheus.yml`, `ops/alerts.yaml`, `docs/ops/RUNBOOK.md`
(a heading per alert is enforced by `ops/check.sh`). Cost: half a day, $0.

**(e) A VM agent that stops reporting pages, from Azure's side.** §2.4.

## 2. Azure's own signals: what fired, what could not, and what it costs

`infra/azure/modules/environment/alerts.tf` already declares three, behind
`alerts_enabled = true` in `infra/azure/prod/prod.tfvars`, all **email only** to
`alert_email` in the uncommitted local tfvars. All three are live:
`repose-prod-vm-health`, `repose-prod-service-health`, and one
`repose-prod-<vm>-availability` per VM. The file's own comment says why email:
"Azure's webhook body is its common alert schema, which Google Chat (the owner's
paging channel) does not accept, so the Chat page for the same outage comes from
Prometheus (HostdDown, GatewayDown, ApiDown)." That sentence names a Chat path
that I-848 and I-956 removed. It is now the plan for a channel that does not
exist.

### 2.1 Resource Health: measured

Every `ResourceHealth` row in `repose-prod` for the evening, from the activity
log:

| Time (UTC) | VM | previous → current | Cause | Title |
|---|---|---|---|---|
| 16:20:57 | host-02 | Available → **Available** | PlatformInitiated | **Live Migration Failed** |
| 18:21:48 | edge-01 | Available → Unavailable | Unknown | Unknown |
| 18:26:46 | edge-01 | Unavailable → Unavailable | PlatformInitiated | Redeploying due to host failure |
| 18:26:49 | edge-01 | Unavailable → Available | Unknown | — |

coolify-01 has no row. `repose-prod-vm-health`'s criteria are
`current = ["Unavailable", "Degraded", "Unknown"]`. host-02's event says
`Available`, so the rule cannot match it — **a failed live migration that kills
the NIC is reported as a healthy VM.** The current status still reads
"There aren't any known Azure platform problems affecting this virtual machine."

The rule did fire, twice, for edge-01 — at **19:21:05Z**, 59 minutes after the
18:21:48 event and 54 minutes after the edge had recovered by itself. Resource
Health is a record, not a page.

### 2.2 VmAvailabilityMetric: measured, and worse

`Maximum` below 1 over a 5-minute window, evaluated every minute, is what
`repose-prod-<vm>-availability` watches.

- **host-02**: 1.0 for every 5-minute bucket from 15:30 to 20:20, through the
  entire three hours it was dark. The first 0 is at 20:25 — the conductor's own
  `az vm restart`.
- **coolify-01**: 1.0 through 19:10–19:30. The first 0 is at 19:35 — again the
  conductor's restart.
- **edge-01**: 0 at exactly two one-minute samples, 18:25 and 18:27, with 1 at
  18:24 and 18:28. `Maximum` over any 5-minute window is 1, so the rule cannot
  fire.

So all three availability alerts were correct and all three were silent.
`VmAvailabilityMetric` answers "is Azure running this VM", and Azure was.

### 2.3 Service Health and Scheduled Events

No `ServiceHealth` row for the region all evening, so
`repose-prod-service-health` had nothing to send. Microsoft's own guidance on
this exact question ("Your Azure VM went down and nobody knew why") names only
Resource Health, Service Health, Resource Health alerts and Scheduled Events —
the four that were silent or late tonight — and offers nothing for a platform
that reports healthy while the VM is unreachable.

Scheduled Events would not have helped either, for two reasons that compound.
Azure's documentation is explicit that for a host hardware failure it "bypasses
the minimum notice period and immediately begin[s] the recovery process", and
that a live migration which does not succeed may be Service Healed "prior to
notification". And the endpoint lives on the VM: the poller reads
`http://169.254.169.254/metadata/scheduledevents?api-version=2020-07-01`, which
goes through the same wire server that coolify-01's waagent could not reach at
19:11:30. A dead NIC blinds the poller.

repose already has that poller, built and tested:
`internal/hostd/schedev/schedev.go` (`Listener.Run`, 1-second poll) parks guests
on `Reboot` and `Redeploy`. It is off in production on purpose —
`nix/hosts/prod.nix`: "Azure Scheduled Events stay off: turning that listener on
holds every restart of the VM for approval." That reason is about the *holding*,
not the *reading*. A detect-only mode, which exports the event list as metrics
and log lines and approves nothing, costs one flag and holds nothing. Worth
revisiting on those terms, and only on those terms.

### 2.4 The one Azure signal that was true

The VM agent. Tonight all three VMs showed "VM running" with the agent **Not
Ready**, and that is the reading that matched reality. coolify-01's agent now
reads `Ready`, `Guest Agent is running`, with a fresh timestamp:

```
az vm get-instance-view -g repose-prod -n coolify-01 \
  --query "instanceView.vmAgent.statuses[].[displayStatus,time]"
```

Azure Monitor has **no alert rule for this.** It is a control-plane GET, not a
metric, so it is readable only by polling — which is §1.3(b)'s probe, and the
reason that probe is worth building.

The Azure-native equivalent is the Azure Monitor Agent's own heartbeat into a Log
Analytics workspace, with a log search alert on `Heartbeat` going absent. That
fires on a dead NIC, because the heartbeat is outbound from the VM. Cost: the
agent is free, heartbeat ingestion sits inside the 5 GB of free monthly
ingestion, and a log search alert rule is **$1.50 a month at 5-minute evaluation
or $3.00 at 1-minute**, times one rule. Notifications are effectively free (an
action group includes 1,000 emails and 100,000 webhook pushes a month). Where it
lands: `infra/azure/modules/environment/alerts.tf` beside the three that exist,
plus the workspace and the agent extension per VM.

Two cheaper changes in the same file, neither of which needs a workspace:

- Widen `repose-prod-vm-health` so a `PlatformInitiated` health event matches
  whatever the status says, not only `Unavailable`/`Degraded`/`Unknown`. That
  one line would have emailed "Live Migration Failed" for host-02. $0. Measured
  caveat: delivery took 59 minutes tonight, so it is a record, not a page.
- Give the action group a second receiver that reaches Google Chat. Azure's
  common alert schema is not a Chat card, so it needs a shaper. The honest
  options are a Consumption Logic App (pennies a month at this volume) or
  turning `use_common_alert_schema` off and pointing a webhook at a tiny
  function. Either is a work item in `infra/azure/modules/environment/alerts.tf`
  with its own decision entry, because it adds a service.

## 3. Automatic recovery: what a network-dead VM can do for itself

### 3.1 The park works with no network. That is designed, and it is load-bearing.

`internal/hostd/park/keys.go`: "Wrap must work with no network: a graceful
shutdown may be the network's last minute, so it encrypts with the RSA public
key fetched ahead of time (RSA-OAEP-256)." `parkKeyLoop`
(`internal/hostd/app/park.go`) refreshes that public half at start and hourly,
with retries a minute apart. `KeyVault.Wrap` is a local `rsa.EncryptOAEP`
against the cached key. Nothing on the park path calls the api: `ParkAll` reads
bbolt, asks systemd over dbus, talks to guestd over vsock, creates a thin volume
with dm-crypt, and snapshots through the Cloud Hypervisor API.

And a locally-initiated reboot does park. `repose-guest-park.service`
(`nix/hosts/park.nix`) is a `oneshot` with `RemainAfterExit`, whose `ExecStop`
runs `hostd park --shutdown`; the drop-ins on `guest@.service`,
`virtiofsd@` and `virtiofsd-auth@` put them `Before` it, and it is `After`
hostd, so hostd is still alive to serve the park. `hostd park --shutdown`
self-guards with `systemctl is-system-running` equal to `stopping`
(`internal/hostd/app/app.go`), so a switch that restarts the unit parks nothing.
The VM test asserts it: `nix/hosts/tests/park.nix`, "a graceful reboot runs the
park before any guest unit stops".

**So the answer to the question asked is yes:** a host that is alive inside and
dead on the wire can park its guests' memory and reboot itself, and the park
costs it no network.

### 3.2 Three things it cannot do, and they bound how much a self-reboot buys

- **If hostd restarts while the NIC is dead, the park is skipped entirely.** The
  cached public key is in memory only. `Sealer.Ready()` fails, and
  `internal/hostd/guest/park.go` logs `park skipped: no image key` with reason
  `no_key` for every guest. A host that was up with network and then lost it
  parks fine; a host that *booted* isolated cannot park at all.
- **The unpark needs Key Vault.** The private half never leaves the vault, so
  `UnwrapKey` is a network call, retried every 3 s inside a 90-second
  `UnwrapTimeout`, after which each image is discarded with reason `no_key` and
  that guest starts cold. If the reboot does not fix the network, the park's
  work is thrown away.
- **hostd never starts a guest on its own.** A guest that was not parked, or
  whose image was discarded, is marked `guest_interrupted` and rides out in
  `Hello` for the api to restart, inside I-645's six hours. With the api
  unreachable, those guests stay down.

The honest reading: a self-reboot converts "three hours of guests running but
unreachable" into "a few minutes of guests down, then running again" **only if
the reboot clears the fault**. If it does not, it converts it into "guests down
and cold". That trade needs the owner's call, not an agent's.

### 3.3 What exists today, and what the host does not have

`repose-wg-watchdog` (I-1068, `nix/hosts/wg-watchdog.nix`) is the one piece of
self-healing in the fleet. A timer every minute reads `wg show` handshake ages
and byte counters, and on a one-way tunnel (handshake older than 180 s, tx
rising, rx flat) runs a single `wg set wg0 listen-port 0`, at most once per 600 s.
It is host-only: `nix/edge/default.nix` does not import it, and neither does the
control VM — and the incident that motivated it,
`docs/incidents/2026-10-26-control-tunnel-one-way.md`, was on the control VM.
Its own file names the gap it leaves: the host's heartbeat to the api does not go
over the tunnel, so a dead tunnel leaves a host that still looks `ready` to the
api and is unreachable by every other means. And **no rule reads any of its
metrics**: `repose_host_wg_one_way`, `repose_host_wg_rebinds_total` and
`repose_host_wg_rebind_failed` appear in `HostWgDown`'s description and in no
`expr` anywhere. Worse, they are published through the node_exporter textfile
collector, which serves whatever is on disk, so a stopped watchdog reads exactly
like a healthy tunnel — the script says so at its own lines 163–169.

What the host does not have:

- **No systemd watchdog, by decision.** `nix/hosts/hardening.nix` sets
  `RuntimeWatchdogSec = "off"` and `RebootWatchdogSec = "off"` with the reason in
  the file: "A watchdog reboot is an unattended reboot; both stay at their
  default (off)." Any proposal for a self-reboot has to change that decision in
  the open, not quietly. The argument for revisiting it: an *isolation* watchdog
  is not a blind hardware watchdog. It fires only when the host has proved to
  itself that nothing can reach it, and it parks first.
- **No escalation on a long stream outage.** `internal/hostd/stream/stream.go`
  backs off 1, 2, 4, 8, 16, 30 s and then sits at 30 s plus jitter for ever —
  `attempt` is never reset on a successful session. Three hours of that is 360
  identical retries and no action.
- **No networkd reconnect timer, no NIC reset loop, no tailscale** on hosts or
  the edge.

### 3.4 The ladder a host should climb before it reboots

The runbook already knows the cheap fixes; nothing runs them. In order of cost:

1. **Detect isolation**: the wire server (`168.63.129.16`), IMDS
   (`169.254.169.254`), the api's VNet address and the edge's WireGuard address
   all unreachable for N minutes. Any one of them answering means this is not
   isolation and the watchdog does nothing. N wants to be longer than the longest
   legitimate blind window, and `docs/ops/RUNBOOK.md` "Guest park" records that
   an unpark of host-02 took 3 m 16 s for two guests and grows with memory, so N
   is minutes, not seconds.
2. **Take the MANA virtual function down**: `ip link set dev eth1 down`. This is
   already step 3 of "Host network dead after a live migration", it fixed
   host-01 on 2026-10-10, it is free and it is reversible — Azure re-adds a VF or
   the next boot brings it back.
3. **Rebind WireGuard** (what `repose-wg-watchdog` already does) and restart
   `systemd-networkd`.
4. **Only then** `hostd park` and `systemctl reboot`, with §3.2's three caveats
   on the label.

Files a build of this touches: a new `nix/hosts/isolation-watchdog.nix` beside
`wg-watchdog.nix`, its textfile metrics, one rule in `ops/alerts.yaml`, a heading
in `docs/ops/RUNBOOK.md`, and a decision entry that amends the
`RuntimeWatchdogSec` line in `nix/hosts/hardening.nix`. Steps 1–3 are worth
building before step 4 is decided; they are free and they cannot lose a guest.

### 3.5 Azure's own recovery, and `restart` against `redeploy`

**Azure will not restart a single VM for you.** Automatic instance repair exists
only for Virtual Machine Scale Sets, driven by a health probe or the Application
Health extension. repose's VMs are standalone, by design — each host is its own
`nixosConfigurations` attribute (I-40) with its own data disk — so there is no
auto-repair policy to switch on. The gap is real and it has no Azure-side
answer short of restructuring the fleet as scale sets, which would cost the
per-host identity the whole design rests on.

`az vm restart` against `az vm redeploy`, for tonight's fault:

- **`restart`** keeps the VM on its current Azure host and power-cycles it.
  Measured tonight: coolify-01 back "within seconds" at 19:36. It is the right
  first move and it is what the runbook should say.
- **`redeploy`** moves the VM to a new Azure host. It is slower, it can fail for
  capacity, and it discards the local temp disk. For repose that last point is
  survivable — DESIGN §4 is explicit that guest volumes live on the managed data
  disk and not the local temp disk, "which is why capacity is added as another
  host" — so a redeploy loses nothing a restart would keep. It is the
  escalation when a restart puts the VM back on the same bad host and the fault
  returns.

So: restart first, redeploy if the fault comes back, and record both in the
runbook. Tonight the restart was enough on both VMs that got one.

## 4. Blast radius: one of everything, in one zone

`infra/azure/prod/prod.tfvars` has `zone = "1"` for the whole environment, and
one of each resource: `hosts = ["host-02"]`, one edge, `coolify_count = 1`. All
three VMs saw host-side maintenance within three hours of each other, which is
what one zone's maintenance wave looks like from inside.

Costs, from `infra/README.md` "Cost" (Azure Retail Prices, eastus,
re-checked 2026-09-20, 730 h):

| Change | Monthly cost | What it buys for tonight's fault |
|---|---|---|
| Second edge in zone 2, behind a Standard Load Balancer | $96.36 for a `D2s_v7`, plus the load balancer's hourly charge and per-GB processing | The edge's 6 minutes become invisible. I-473 has the whole design: each host peers with both hubs, a guest route through either, the revocation push to both, and per-edge caps that halve a user's share unless the edges share counts. |
| Second host, `D16s_v7` in zone 2 | $772.34 plus about $147 of disks | A place to move projects to after a host loss. **Nothing for tonight**: a move is stop and restore, not live, so a dead host's guests stay dead until it is back. |
| Second host, `D64s_v7` | $3,089.36 plus about $250 of disks | The same, at the launch host's size. |
| Second control VM | $193.45 for a `D4s_v7` plus $40 of disk | Not a change on its own: the control plane is one Coolify-managed stack with Postgres on the VM's OS disk. Standby control plane is a project, not a line item. |
| Mac mini appliance (`docs/workstreams/19-mac-mini.md`, on main) | **$10 a month per host**, charged to the user, on hardware the user owns | The real change in blast radius: the compute leaves eastus zone 1 entirely. An Azure zone wave cannot touch a user's Mac. The hosted control plane stays single, so coolify-01 is still one VM. |

The arithmetic that matters: the fleet's problem tonight was not redundancy. It
was 3 h 15 m of detection against 1 minute of remedy. The cheapest second edge is
$96 a month and would have saved six minutes. The cheapest alert is $0 and would
have saved three hours. Spend on detection first, and let the second edge wait
for the reason I-473 already names — "the edge needs a reboot that cannot wait
for a quiet hour, uptime is promised in the terms, or one edge's relays near
200".

## 5. The runbook entry

Written in this branch, as `docs/ops/RUNBOOK.md` "Host or control VM dead on the
network", and reached from the symptom index. It holds the diagnosis the
conductor actually ran tonight, in the order that separates the cases fastest —
Azure's power state and agent status, the agent's heartbeat timestamp, the
boot-diagnostics screenshot and serial log, Resource Health through `az rest`,
the health-event history in the activity log, the edge's `wg show` and `nc`
probes, and the VM's own `journalctl -b -1 -k` for `hv_utils: TimeSync` — then
the remedy order: the VF down, then `az vm restart`, then `az vm redeploy`. It
is a procedure, so it ships as the runbook entry and not as a proposal.

Two commands worth having in the muscle memory, both verified from this branch
tonight:

```
az vm get-instance-view -g repose-prod -n <vm> \
  --query "{power:instanceView.statuses[].displayStatus,agent:instanceView.vmAgent.statuses[].[displayStatus,time]}"

az monitor activity-log list -g repose-prod --offset 18h --max-events 400 \
  --query "[?category.value=='ResourceHealth'].{t:eventTimestamp,res:resourceId,cur:properties.currentHealthStatus,title:properties.title}"
```

## Recommend an order

**Before anything else, and it is free.** Ask Grafana what it had and where it
sent it: `python3 ops/grafana/verify.py`, the rule list, the default notification
policy's receiver, and the alert state history for 16:20–19:40Z on
`HostUnreachable`, `HostdDown`, `HostScrapeDown` and `MonitoringPathDown`. Two
answers are possible and they lead to different weeks. If the rules fired and
went nowhere, the week is about the channel. If the rules were not there, the
week is about I-1044. `docs/ops/OBSERVABILITY.md` already warns that "the alert
did not fire" and "the alert did not exist" look identical afterwards, and that
only Grafana can tell them apart. Nothing below should be built before this is
known, and an incident file in `docs/incidents/` should record the answer.

**This week, cheap, in this order.**

1. **The api pages the owner on `host_unreachable`** (§1.3a). Highest value per
   hour of work in this document: three hours to two minutes, no new
   infrastructure, one secret. `internal/api/hostmgr/hostmgr.go` and a sender
   beside `internal/api/notify/gchat.go`.
2. **One external probe, outside both domains** (§1.3b), reading the guest-agent
   heartbeat and the two public endpoints.
   `.github/workflows/fleet-probe.yml`, or a timer beside `ops/digest/` if the
   owner would rather keep the Chat webhook off GitHub.
3. **Widen `repose-prod-vm-health` to match a `PlatformInitiated` event at any
   health status** (§2.4). One line in
   `infra/azure/modules/environment/alerts.tf`, $0, and it turns "Live Migration
   Failed" into an email. A record, not a page.
4. **The edge keeps its peers across a reboot.** Tonight the edge came back at
   18:27 with an empty wg0, because `nix/edge/default.nix` declares only
   `staticPeers` and wgsync is the only thing that adds hosts — and wgsync needs
   `GET /internal/hosts`, which died with coolify-01 at 19:10. The monitoring
   path's recovery depends on the api, and the api is monitored through that
   path. Fix: wgsync writes the peer set it installed to a file, and applies that
   file once at boot before its first successful poll. `internal/gateway/wgsync.go`,
   `cmd/gateway/wgsync.go`, `nix/edge/default.nix`.

**Next, still cheap.**

5. `verify.py --check` on a daily timer that pages on drift (§1.3c): I-1044's own
   named fix.
6. wgsync's metrics actually exported, and one rule on them (§1.3d) — today an
   alert description sends an operator to a series that does not exist.
7. `repose-wg-watchdog` on the edge and the control VM, and alert rules on the
   watchdog's own metrics plus its textfile mtime, so a stopped watchdog stops
   reading as a healthy tunnel (§3.3).
8. Isolation detection on the host, steps 1 to 3 of §3.4 — detect, VF down,
   rebind. Free, reversible, cannot lose a guest.
9. Scheduled Events in detect-only mode (§2.3): the listener exists; export and
   log, approve and hold nothing.

**Needs the owner's call, so it is not "this week".**

10. The self-reboot, step 4 of §3.4, against §3.2's three caveats and against the
    `RuntimeWatchdogSec = "off"` decision in `nix/hosts/hardening.nix`.
11. The Azure Monitor Agent heartbeat alert, $1.50 to $3.00 a month plus a
    workspace (§2.4), and a Chat receiver on the action group, which adds a
    service.
12. A second edge, $96 a month plus the load balancer, on I-473's terms (§4).
