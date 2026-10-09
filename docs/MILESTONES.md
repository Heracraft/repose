# Milestones

Each milestone is usable on its own and has a gate. Workstreams (see
`workstreams/README.md`) map onto milestones; several workstreams run in
parallel inside one milestone, and some start early because nothing blocks
them. "Done" for a milestone means every listed workstream's checklist is
closed and the gate is demonstrated, not that code exists.

## M0. Benchmark gate (deferred, DECISIONS I-12)

Skipped by the owner's choice on 2026-09-17. The first M1 host records real
timings instead; the procedure below remains for when a comparison is needed.

Workstreams: `00-benchmark`.

Provision one `D64s_v5` host with `--security-type Standard` by hand (az CLI
is fine here), install NixOS with nixos-anywhere from a throwaway host config,
boot one microvm.nix Cloud Hypervisor guest with the shared store, Docker
inside, 4 vCPU 8 GB. Measure against a plain `D4s_v5` Azure VM:

- CPU: `nix build` of a fixed derivation set, and a Go test suite, wall time.
- Disk: `docker pull` and extract of a fixed 2 GB image; `fio` 4k random rw.
- Network: `git clone` of a fixed 1 GB repo from GitHub; `iperf3` to the edge.

Gate: penalty under ~20 percent on each axis. Record all numbers, SKUs, kernel
and CH versions in `RESEARCH.md`. If the gate fails: repeat once on
`D64s_v6`; if that fails, open a decision to move hosts to Hetzner and
continue with M1 unchanged in design.

M0 can run in parallel with every M1 workstream that does not need a host.

## M1. Hosts, guests, and the daemon pair

Workstreams: `01-host-nixos`, `02-guest-base`, `03-hostd`, `04-guestd`,
`12-nix-config-pipeline`, `10-observability` (host and guest parts).

Gate: on one real host, a developer's own projects run as guests created
through `hostdev` (DECISIONS I-17), with the shared store, Docker inside,
config apply in place, snapshot and restore round-trip, process samples and
metrics visible in Grafana. Reached over WireGuard from the laptop with a
manually issued certificate.

## M2. Edge, identity, CLI

Workstreams: `06-gateway-edge`, `05-control-plane-api` (auth, CA, projects,
routing), `07-cli`, `11-infra-opentofu` (edge, hosts, blob, kv).

Gate: a second person with a GitHub account runs `repose login` and `repose
run` on their laptop and lands in tmux in their own guest on the shared host,
cannot reach the first person's guest, and gets a notification when their
agent finishes. *Closed 2026-09-21 on the owner's call with one person plus
a second account (DECISIONS I-129): the owner's laptop run, the isolation
suite on host-01 under two accounts, and the notification deliveries stood
in for the second human, who is still required at M5.*

## M3. Control plane on Coolify, dashboard, secrets, config menu

Workstreams: `05-control-plane-api` (rest), `08-dashboard`, `13-notifications`,
`11-infra-opentofu` (Coolify VM), `14-security` (policy text, review).

Gate: the API and dashboard run on Coolify with rolling deploys, Postgres is
backed up on a schedule, secrets set in the dashboard appear in a guest, a
non-Nix user adds a package from the menu and sees it in their guest without
a reboot.

*The backup clause used to read "Postgres backs up to R2 nightly and a
restore has been rehearsed". `DECISIONS.md` I-112 withdrew all of that from
this side: backups and their restore are configured on the Postgres
service's Backups tab in the owner's own Coolify, against a destination no
credential for which exists in this repository, so there is no R2 bucket, no
on-VM check and no rehearsal script left to gate on. What remains gateable
here is that the schedule exists, which is a look at that tab.*

*Where it stands, 2026-09-26: the api side is proven on host-01 (05, 12,
13, 14 rows closed with evidence). Open, all on the owner: the dashboard's
signed-in live tests (`pnpm --filter web run live:auth`), the Postgres
backup schedule, and the Traefik drain for rolling deploys
(`workstreams/CHECKLIST-AUDIT.md` "Waits on the owner").*

## M4. Billing

Workstreams: `09-billing`.

Gate (DECISIONS I-289, I-604): in Polar's sandbox, a checkout with a test
card creates a `trialing` subscription and a seat and the webhook makes
the account `trial`; ending the trial charges the first period and makes
it `active`; a renewal on a card that fails makes it `past_due` and the
3-day tick stops the machine; an egress overage for a known number of GB
appears on the renewal order to the cent.

*Where it stands, 2026-10-08: moved from Paddle to Polar (I-604, ws/polar);
`ops/M4-GATE.md` is the runbook, against the sandbox organization
production runs on.*

## M5. Public

Workstreams: `14-security` (final review), `ops` docs, launch checklist in
`CHECKLIST.md`.

Gate: `CHECKLIST.md` closed. Landing page at `repose.herakraft.co`.

*Where it stands, 2026-09-26: the deployed-state review
(`security/review-2026-09-21.md`), tenant isolation on host-01, dashboards,
alerts, runbook and the published privacy policy are closed. Open in
`CHECKLIST.md` "Release (M5)": a second human on their own laptop, the
installer on real macOS and Linux arm64 machines, the Postgres backup
schedule, restore onto a different host and host loss (both need a second
host), and the Polar sandbox gate (M4). The b1a5915 row is settled by I-193
(key rotated, history kept) but not yet ticked.*

## Later, in order of likely demand

Preview URLs. Idle auto-stop (needs M1 signals for a month). `repose mcp
forward`. Telegram and Discord. Hetzner host module. Per-project LUKS. Teams.
Central builder. Automatic capacity.
