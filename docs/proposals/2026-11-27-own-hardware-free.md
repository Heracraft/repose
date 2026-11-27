# Own hardware free tier and paid multi-host line: plan, failure modes, and verification

Ticket and engineering plan, high priority, next up (owner, 2026-11-27; expanded 2026-11-27 with pricing-line options, product invariants, stability guards, and failure-mode checks). Not built.

Related documents and board items:
- [2026-11-27-open-source-and-repo-launch.md](2026-11-27-open-source-and-repo-launch.md) — Apache 2.0 open-source launch plan (pairs with Free BYOH so self-hosters stay in the `repose.run` funnel)
- `ENG-215` — Deployment-wide 1,000-host address ceiling (`POST /hosts` `500` when full; hard blocker before opening no-card `host add`)
- `ENG-282` — Notification noise audit and default-off outbound email (prevents free-tier Resend quota burn)
- `ENG-39`, `ENG-131` — CLI/dashboard billing clarity and installing-host wait path (`I-1700`, `I-1722`)

---

## 1. Owner's rulings and product principles (2026-11-27)

1. **Value first, money after — never cripple a machine.**
   - Bringing your own hardware starts at **$0 with no credit card at sign-up**.
   - Every core capability of a `repose` machine — persistent sandboxes, snapshots, port forwards, webhooks/triggers, `ntfy`/web/CLI notifications, and **remote browser viewing (`repose browser` and the dashboard's Browser tab, `I-292`, `I-1216`)** — works in full on the free tier. Gating remote browser or machine features would break the "value first, money after" rule.
2. **Payment starts only when a user scales out (2+ hosts) or hands off hardware (hosted cloud).**
   - **Free ($0, no card):** **1 user-owned host** (`FreeOwnHosts = 1`, Mac or Linux), unlimited sandboxes bounded only by that host's memory and disk (`I-1626`, `I-1630`), 100-project account abuse cap (`I-569`).
   - **Paid multi-host BYOH ($10/month, confirmed by owner 2026-11-27):** Adding a **2nd host** upgrades to the **$10/month `Own hardware` plan** (`I-1364`, `I-1594`: 2+ / any number of user-owned hosts, no fleet compute; supersedes the earlier 2-free-hosts draft). Nobody is on `Own hardware` today, so changing its free baseline requires no customer migration or refunds.
   - **Hosted cloud plans (`Solo` $49 / $39 intro, `Plus` $89, `Pro` $169):** Unchanged, and continue to include any number of user-owned hosts at no extra cost (`I-1704`).
   - **No per-seat / team pricing yet:** Single-user accounts for launch until users ask for teams (`R5-6`).

### 1.1 Single source of truth for the free-host constant

Define one constant `billing.FreeOwnHosts = 1` (reported in `GET /me` as `limits.hosts` for an account with no subscription, and mirrored in `apps/web/src/lib/plans.ts` via `TestPlansMatchPricingDoc` `I-764`) so CLI, API, fake API, dashboard, and docs read one source rather than scattered literals.

---

## 2. Exact changes required across the stack

1. **Billing & entitlement gate (`internal/api/billing/`)**
   - Today (`I-1364`, `I-1396`, `I-1704`): an account with `sub == nil` (and not `billing_exempt`) has `HostsAllowed == 0` (`own_hardware_plan_required` on `POST /hosts`) and fails `Gate.check` with `subscription_required` on `Create`, `Start`, `Restore`, `Wake`, and `Resize` when `BILLING_ENFORCE=true`.
   - New rule when `sub == nil` and `!exempt`:
     - `HostsAllowed` returns `FreeOwnHosts` (`1` or `2`).
     - `Gate.check` **permits** lifecycle operations (`Create`, `Start`, `Restore`, `Wake`, `Resize`) **when `req.OwnHost == true`** (the exact predicate `I-1704` already added for owned-host placement and operations!), while **continuing to refuse `req.OwnHost == false` (fleet placement)** with `402 payment_required` (`detail.reason = subscription_required`).
     - Past-due or suspended accounts (`sub.Status == past_due` or `suspended`) keep today's refusals and day-3 stop rules (`PRICING.md` "Failed payments") if they hold a past-due paid plan; if an `Own hardware` ($10/month) subscription cancels and ends while the account holds `<= FreeOwnHosts`, its remaining hosts keep working on Free; if it holds `> FreeOwnHosts`, existing hosts are not deleted, `POST /hosts` is refused (`host_limit`), and starting machines on hosts beyond the free allowance is either permitted on the oldest `FreeOwnHosts` hosts or refused cleanly with a message naming which host to remove or `$10/month` to resume (settle explicitly in the decision entry; never leave machines silently wedged).
2. **Polar catalogue (`POLAR_PRODUCT_OWN`)**
   - Keep `POLAR_PRODUCT_OWN` ($10/month, unlimited user-owned hosts), **remove the 7-day free trial from `Own hardware`** (the free tier *is* the unlimited-duration trial; charging $10 only starts when adding host `FreeOwnHosts + 1`). Keep the 7-day card-backed trial on hosted cloud plans (`Solo`, `Plus`, `Pro`).
3. **Host address pool & Sybil limits (`internal/api/hostmgr/`, `ENG-215`)**
   - **Hard prerequisite before shipping Free BYOH:** Fix `ENG-215`'s 1,000-host deployment ceiling and `500` error on exhaustion (see §3.1 below).
   - Lower the per-account unregistered/`waiting` host cap (`I-1598`, currently 10) on free accounts to `FreeOwnHosts` so a scripted free account cannot hold 10 hub peers without ever registering a daemon.
4. **Notification and relay abuse guards (`internal/api/`, `internal/gateway/`, `ENG-282`)**
   - Keep dashboard browser view (`I-1216`) unlocked on Free, bounded by existing protocol limits (`120 s` idle close, `1` active view per project, `2 GiB` per ticket, `10` mints/hour; `SECURITY.md` row 15).
   - Default outbound email notifications off or transactional-only for new free accounts (`ENG-282`), with a monthly/daily per-account email ceiling on Free so an agent hook loop cannot exhaust Resend quota; keep web, CLI (`repose events` / `questions`), and `ntfy` unthrottled beyond `I-445`'s existing guest event rate limits.
   - Add per-account edge relay byte counters (`SSH` + `view` + `webhook` bytes on `gateway`) for observability and an operator alert on extreme outliers (e.g. >100 GB/month relayed through `edge-01` on a free account) as an abuse guard, never a surprise user-facing paywall.
5. **CLI, dashboard, and public docs (`I-242`, `I-1266`)**
   - **Sign-up without a card:** Signing in lands on `/projects` (`I-1716`) with an active free account (`plan: null` / Free tier); `/billing` shows `"Free — up to N hosts of your own"` with two clear upgrade paths: **`Own hardware` ($10/month for unlimited hosts of your own)** and **`Solo` / `Plus` / `Pro` ($49+ for always-on cloud machines + unlimited own hosts)**.
   - **`repose login --status` (`I-1722`):** Prints `Free (up to N hosts of your own)` instead of `no plan`.
   - **At the host limit (`POST /hosts` when `count >= FreeOwnHosts` on Free):** Returns `402 payment_required`, `detail.reason = host_limit`, with one plain sentence in both CLI (`exit 7`, `I-623`) and the dashboard's **Add a host** modal (`I-1450`): *"Free accounts include up to N host(s) of your own. Upgrade to Own hardware ($10/month for any number of hosts) or a cloud plan at https://repose.run/billing."*
   - **Public docs & landing:** Update `apps/web/src/content/docs/billing.md`, `own-hardware.md`, `limits.md`, `pricing.md`, `cli.md`, and the landing pricing band in the same commit (`I-242`), keeping `TestPlansMatchPricingDoc` (`I-764`) and `just done-check` green.

---

## 3. How things could go wrong, and where each failure is caught

### 3.1 Exhausting the 1,000-host WireGuard & guest-CIDR pool (`ENG-215`)
- **How it goes wrong:** Today every `POST /hosts` allocates one `/22` guest range from `10.64.0.0/12` and one hub address from `10.255.0.0/22` (~1,000 hosts across the entire deployment). With cardless free sign-ups, 500–1,000 curious developers running `repose host add` once and closing their laptops permanently consume every slot, after which `POST /hosts` fails with HTTP `500` for paying customers too.
- **How to make it stable:**
  1. Shrink per-host guest CIDR allocation for owned hosts from `/22` (1,024 IPs) to `/26` (62 IPs — no single Mac or desktop runs >60 concurrent VMs) or widen the guest/hub private space (`100.64.0.0/10` or `/16` hub peers, checked against `nix/edge/` and `nix/hosts/nftables.nix` private-range drop rules so guest isolation holds!).
  2. Automatically expire and reclaim hub/CIDR slots for **unregistered** join bundles after 24 hours (`I-1380`) and reclaim or mark `dormant` free-tier hosts that have not connected in 30 days (re-allocating seamlessly on next `Hello` or `host add`).
  3. If the allocator ever reaches capacity, return `503 unavailable` (`detail.reason = host_address_exhausted`) with a plain sentence and fire a high-severity Grafana alert at 70% pool utilization—never HTTP `500`.
- **Where caught:** Unit + Postgres tests in `internal/api/hostmgr` (`TestAllocatePast1000Hosts`, `TestExhaustedPoolReturns503Not500`), edge nftables ruleset snapshot (`just edge-ruleset-golden` + `edge-hub-isolation` VM check if CIDRs change), and Prometheus alert test in `ops/alerts_test.yaml`.

### 3.2 Free account accidentally allowed onto `repose`'s paid Azure fleet (or blocked on its own Mac)
- **How it goes wrong:** Two opposite bugs sit one boolean apart in `billing.Gate`:
  - *Leak bug:* Relaxing `subscription_required` for `sub == nil` without binding every check to `req.OwnHost == true` lets a cardless free user run `repose run --host repose` and get free Azure `D64s_v7` VMs, free Blob snapshots, and unbilled egress.
  - *Wedge bug:* Allowing `POST /hosts` on Free (`HostsAllowed = FreeOwnHosts`) while forgetting one op path (`Wake`, `Restore`, `StartAgent` `I-1702`, or `ResizeVolume`) leaves a free user able to install `hostd` on their Mac and create a VM, only to get `402 subscription_required` the first time the VM wakes from rest or restores a snapshot!
- **Where caught:** Exhaustive table test in `internal/api/billing/gate_test.go` and end-to-end HTTP tests in `internal/api/http/billing_test.go` covering **every** gated action (`Create`, `Start`, `Wake`, `StartAgent`, `Resize`, `Restore`, `SnapshotCopy`) for a `sub == nil` account under `BILLING_ENFORCE=true`:
  - `OwnHost: true` → **allowed** across every lifecycle and wake/restore op.
  - `OwnHost: false` (`--host repose`, or default placement when the account has **zero** owned hosts) → **refused** with `402 subscription_required` (and when the account has zero hosts and no plan, the message names both free `repose host add` and paid cloud plans!).
  - Mirror the exact gate in `internal/fakes/api` (`TestFakeFreeTierMatchesRealGate`, preventing `CHECKLIST.md`'s forgiving-fake trap).

### 3.3 Confusing first-run UX for a brand-new user with no card and no host yet
- **How it goes wrong:** A new user signs in (no card, no plan, 0 owned hosts) and runs `repose run` expecting something to happen. Because they have 0 owned hosts, default placement looks at the cloud fleet (`OwnHost == false`) and refuses with *"An active subscription is required"*—hiding the fact that they can run on their own Mac/Linux machine for **$0 right now**!
- **How to make it stable:** When an account with `sub == nil` and `0` owned hosts hits `subscription_required` on `POST /projects` or `New project` on the dashboard, the refusal and empty-state UI must explicitly offer both paths:
  - CLI (`exit 7`): *"Choose where your machines run: run on this Mac/Linux machine for free with `repose host add`, or start a cloud plan at https://repose.run/billing."*
  - Dashboard (`/projects` empty state / `New project`): Shows **Add your Mac or Linux host (Free)** alongside **Start a cloud plan** instead of a dead-end paywall.
- **Where caught:** CLI test `TestFirstRunWithNoPlanAndNoHostNamesBothPaths` and Playwright spec `tests/free-tier-onboarding.spec.ts` at 1440 and 390 widths in light and dark schemes.

### 3.4 Downgrade / cancellation edge cases (`Own hardware` $10 → Free, or `Solo` → Free)
- **How it goes wrong:** A user pays $10/month (`Own hardware`) or $49/month (`Solo`), adds 3 owned hosts (`mac-1`, `box-2`, `box-3`), and later cancels their subscription. At period end (`sub` becomes `canceled`/`revoked`), the account drops back to Free (`FreeOwnHosts` limit) while owning 3 hosts. If the sweeper deletes hosts, snapshots on those hosts are marked `gone_with_host` (`I-1542`); if nothing happens at all, someone pays $10 for one month, adds 20 hosts, cancels, and runs 20 hosts free forever.
- **How to make it stable:** At subscription end, **never delete a host or its snapshots**. Stop running machines on hosts past the oldest `FreeOwnHosts` active hosts (with a snapshot first, same as day-3 billing stop) and refuse `Start` / `Create` on excess hosts until the user either removes hosts down to `FreeOwnHosts` or resumes a plan.
- **Where caught:** `TestCancelledMultiHostSubscriptionStopsExcessHostsAndKeepsSnapshots` in `internal/api/billing`.

---

## 4. Definition of done

- [ ] `DECISIONS.md` entry (reserved id) recording `FreeOwnHosts = 1`, `$10/month Own hardware` (no trial) starting at the 2nd host for any number of owned hosts, preserving `I-1704` on cloud plans, and recording the cancelled-over-limit rule.
- [ ] `ENG-215` resolved and merged first (address pool widened / `/26` owned-host CIDRs, `503` instead of `500`, 70% utilization alert).
- [ ] `internal/api/billing` & `internal/api/http`: Free (`sub == nil`, `BILLING_ENFORCE=true`) allows up to `FreeOwnHosts` owned hosts and all lifecycle/wake/restore/browser/agent ops on them; refuses `FreeOwnHosts + 1` with `host_limit` and refuses cloud fleet ops with `subscription_required`.
- [ ] `internal/fakes/api` updated in lockstep with parity tests.
- [ ] Outbound email guard on Free accounts (`ENG-282` coordination) and per-account gateway relay byte metric in `internal/obs/metrics`.
- [ ] CLI (`login --status`, `host add` limit refusal, first-run no-plan/no-host guidance) + dashboard (`/billing`, `Settings > Hosts`, `/projects` empty/create state) + Playwright tests at 1440 & 390 in both themes.
- [ ] Public docs (`billing.md`, `own-hardware.md`, `limits.md`, `pricing.md`, `cli.md`) + `docs/PRICING.md` + landing pricing band updated in the same commit (`I-242`, `TestPlansMatchPricingDoc`, `just fast-check`, `just done-check`).
- [ ] Live check on `repose.run`: a brand-new GitHub test account with **no card** adds a host, runs a machine, opens the dashboard remote browser view, wakes from rest, and hits the clear upgrade prompt when attempting host `FreeOwnHosts + 1`.
