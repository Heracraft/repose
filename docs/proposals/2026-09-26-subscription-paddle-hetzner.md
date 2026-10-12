# Subscription pricing, Paddle and Hetzner hosts (proposal, 2026-09-26)

**Status: analysis only. Nothing here is decided.** This writes down a
chat from 2026-09-24, in which the owner asked three questions in order:
"would paddle integration work", then whether a subscription like
exe.dev's with surcharges would be better, then what Hetzner hosts
would cost at repose's scale, auction servers included. That chat wrote
no file, so this one carries it. Any of it becomes real only through
`DECISIONS.md` entries that supersede the ones named below.

What is live today: hourly billing with a monthly cap per project on
Stripe (`docs/PRICING.md`, DECISIONS R2-12, R4-7, R4-8, amended by I-205
for the trial), on Azure hosts that must be Intel (R2-17, I-39).

Numbers marked *unverified* were not confirmed at the time. Re-check
every price before it is used.

**Correction (2026-10-09, DECISIONS I-642).** The Hetzner prices below
predate Hetzner's 15 June 2026 repricing of new orders. The AX162-R is now
the AX162-2 at €842.30 a month (was €242), and the AX102 is the AX102-1
at €257.30 (was €122). 8 GB on Hetzner now costs about €28 a month, not
€7 to 8, so the plans lose money on Hetzner too. `docs/PRICING.md`, "The
cost floor", has the current table.

## Summary

| Question | Answer |
|---|---|
| Can repose sell on Paddle? | Yes. Its prohibited list does not name hosting or compute |
| Why switch? | Paddle is the merchant of record: it collects and remits VAT and sales tax everywhere. Stripe Tax only calculates it |
| What does it cost? | 5% + 50¢ per transaction against about 2.9% + 30¢; about one workstream of billing rework |
| Subscription instead of hourly? | Fits the product and Paddle better, but loses money on Azure at an exe.dev-like price |
| What makes the price work? | Hetzner dedicated hosts: memory at about €1 per GB-month against about $9 on Azure |
| Decide first | Hetzner or Azure (sets the price floor), then what repose sells next to exe.dev |

---

## 1. Paddle

**Is it allowed?**
- Paddle's prohibited list does not name hosting, VPS or compute.
- "VPN and proxies" needs extra review, and a VM with open egress may
  draw questions. "Digital marketplaces" are banned, which repose is
  not.
- Expect a website and domain review. They will want to see pricing,
  terms, privacy and a refund policy on repose.herakraft.co. The first
  three exist; there is no refund policy yet.

**Why it's worth doing:** Paddle is the merchant of record. For a solo
operator selling to developers in every country, it removes tax
registration and filing, which is the unticked "tax" row in M4. That is
the whole argument; Paddle solves no other launch blocker.

**Where it rubs against the current Stripe design:**

| Stripe design today | On Paddle |
|---|---|
| An hourly usage event per project to Stripe meters (I-77, I-179) | No meters. Send one-time charges on a subscription. The `usage_hours` ledger already has each hour's cents, so sum them per period and send three charges (compute, storage, egress) before renewal |
| A card saved before the first guest with no charge (SetupIntent / Checkout setup mode, I-182) | No "save a card" step. The usual pattern is a $0/month subscription created through Paddle checkout |
| Charged in arrears at period end | Paddle locks the invoice about 30 minutes before charging, so usage must be sent before that. One-time charges on a cancelled subscription are dropped, so account deletion must charge first |
| Test clocks prove the M4 gate (I-185) | Redo the proof in Paddle's sandbox. Whether it can fast-forward a billing cycle like Stripe test clocks is *unverified* |
| About 2.9% + 30¢, plus Stripe Tax | 5% + 50¢. The 50¢ hurts on small invoices such as a trial user's first bill |
| Dunning, portal, invoice list | Paddle has its own retries, customer portal and invoice PDFs; its webhooks map onto the existing past_due handling and day-3 stop |

**What changes in the code**
- Already separable: `internal/billing/ports.go` puts the provider behind
  interfaces (`UsagePusher`, `Portal`, the reconciliation summary). A
  Paddle implementation goes there. Rollup, trial credit, class limits and
  the card check before compute stay.
- Rewritten: `internal/billing/{stripe,webhook,bootstrap,reconcile}.go`,
  `internal/admin/billing_stripe.go`, the dashboard billing page. The
  pusher goes from one call per hour to one per period.
- Contracts: a migration turning `stripe_*` columns into
  provider-neutral ones, with `docs/interfaces/db-schema.md` and `api.md`
  in the same commit. DECISIONS entries superseding R2-12, I-77 and
  I-179..I-185.
- Size: about one workstream, most of it redoing the gate proof in the
  sandbox.

**Recommendation from that chat:** decide on the tax question alone. If
tax registration is the worry, switch before the first real invoice;
afterwards it means migrating live customers. If not, stay on Stripe.

## 2. A subscription like exe.dev's

**What exe.dev sells** (https://exe.dev/pricing, read 2026-09-24):
- Personal, $20/month: 2 vCPU and 8 GB RAM shared across up to 50 VMs,
  100 GB disk, 200 GB transfer.
- Overage on disk ($0.08 per GB-month) and transfer ($0.05 per GB) only.
  CPU and memory are a hard ceiling; stay over it and they contact you
  about upgrading.

**Why it suits repose better than hourly**
1. The pitch is "the agent keeps running after the laptop closes".
   Hourly billing tells people to stop guests at night, which works
   against it. A flat price lets them leave things running.
2. Money is taken before compute runs. Today billing is monthly in
   arrears, so a stolen card gets up to a month of compute. Egress past
   the allowance becomes a hard stop or prepaid overage, so the
   $250/day egress risk shrinks to the size of the plan.
3. It fits Paddle: the plan is an ordinary subscription, and disk and
   egress overage are one one-time charge per period.
4. It removes code: the hourly meter events (I-77, I-179), the monthly
   cap per project (R4-7) and the trial credit arithmetic (I-205). The
   `usage_hours` ledger stays as the internal record and computes
   overage.

**A shape that fits the current architecture.** Guests are sized per
project (small, large, xl), so the simplest pool is a limit on how much
memory can run at once, in place of a shared CPU and memory pool:

| Plan | Running at once | Disk | Egress | Projects |
|---|---|---|---|---|
| Solo | 8 GB (1 large or 2 small) | 100 GB | 250 GB | Unlimited while stopped |
| Pro | 16 GB (xl, or any mix) | 250 GB | 500 GB | Unlimited while stopped |

- Overage: disk at $0.10 per GB-month, egress at $0.05 per GB up to a
  cap. CPU and memory are a hard limit.
- Starting a guest over the limit is refused with "stop X or upgrade":
  one check in `billingGate` and placement.
- Price depends on the host decision: $20–29 for Solo on Hetzner, about
  $59 staying on Azure.

**The catch.** Memory is the limiting resource (`docs/PRICING.md`, "The
cost floor"). 8 GB running all month costs:

| Host | 8 GB for a month |
|---|---|
| Azure D64s_v5, on demand | about $70 |
| Azure, 1-year reserved | about $43 |
| Hetzner AX162-R | about €7–8 |

A $20–29 plan for 8 GB loses money on Azure whenever someone runs it
around the clock, which the best users will do. exe.dev can charge $20
because of cheap hardware and oversubscription. The $10k Azure credit
hides this for a few months only.

A price near exe.dev's needs one of: hosts on Hetzner; about $59 or
more for 8 GB running at once on Azure; or stopping idle guests
automatically (on the Later list, no code).

**What changes in the repo:** `docs/PRICING.md` rewritten, with DECISIONS
entries superseding R2-12, R4-7, R4-8, I-205 and I-77. The billing gate
becomes "has an active subscription" plus the running-memory check. The
M4 gate becomes "a plan is charged and overage matches the ledger",
which is simpler to prove than the current 100-hour test.

## 3. Hetzner hosts

**New dedicated servers** (Germany, excluding VAT, after the April 2026
price rise; https://www.hetzner.com/dedicated-rootserver/matrix-ax/).
Superseded on 15 June 2026: see the correction at the top.

| Server | CPU | RAM | Large guests (8 GB) | €/month | Replaces |
|---|---|---|---|---|---|
| AX102 | Ryzen 9 7950X3D, 16 cores | 128 GB ECC | about 15 | €122 | host-01 today (D16s_v7, 7 large, about $770) |
| AX162-R | EPYC 9454P, 48 cores | 256 GB ECC | about 30 | €242 | the launch host (D64s_v7, 30 large, about $2,200) |

**The whole platform**

| | Azure | Hetzner |
|---|---|---|
| Now (1 host, edge, control VM) | about $1,094/month | AX102 plus two small Hetzner Cloud VMs for edge and control: about €140–160/month, with twice the capacity |
| Launch (30 large) | about $2,850/month | AX162-R plus an AX102 kept as a standby host: about €400/month |
| Egress | about $0.087/GB | Unlimited on a 1 Gbit port. The $250/day egress-abuse cost largely goes away; the reputational risk stays |
| Performance | Guests nested inside an Azure VM, 5–15% overhead | Guests directly on KVM |

At these prices a second host is cheap enough to keep permanently, which
also pays for the host-loss drill and cross-host restore that are still
open.

**Auction servers** (https://www.hetzner.com/sb/). Refurbished, no setup
fee, monthly, cancel any time. Prices are *unverified*: the auction page
and trackers load their data with JavaScript and could not be read.
- 128 GB boxes typically about €45–80, mostly older Xeon (E5, W) or
  Ryzen.
- 256 GB boxes now and then at about €90–150 (EPYC 7502P, Xeon W).

Fit for repose:
- Fine as hosts once host loss is rehearsed: guests live on a thin
  volume and are snapshotted, so losing a box should cost at most a
  restore.
- Require ECC memory; some desktop-Ryzen boxes lack it.
- Require NVMe; many auction boxes have SATA SSDs or hard drives, and the
  thin pool wants NVMe.
- Stock varies, so the same box can't be bought again. Ordering through
  the Robot API is *unverified*.
- A sensible mix: one new AX162-R as the main host, auction boxes as
  standbys or overflow.

**What changes in repose**
1. The Intel-only rule exists only because of Azure's nested
   virtualization penalty on AMD (RESEARCH §3). `nix/hosts/kernel.nix:7`
   hardcodes `kvm-intel` and `infra/azure/modules/host/main.tf:181`
   checks Intel's nested setting. A Hetzner host loads `kvm-amd` and
   skips that check. Needs an entry amending R2-17 and I-39.
2. There is no Hetzner host module. R3-20 planned it as a second module;
   `infra/azure/modules/host/main.tf:8` and `variables.tf:14` reserve a
   `hetzner_host` with `hetzner-ax162r`. NixOS from the rescue system
   with nixos-anywhere is the usual install path.
3. Location: dedicated servers are only in Germany and Finland; the US
   sites offer only Cloud VMs. That is about 90–110 ms per keystroke from
   the US, usable in tmux but noticeable. With hosts in Europe the edge
   should move too, or every session crosses the Atlantic twice.
4. Other Azure services: snapshots in Azure Blob could move to R2
   (`infra/r2` exists) or Hetzner Object Storage; Key Vault needs a
   replacement or stays on Azure.
5. Credits: Azure costs no cash while the $10k credit lasts (about 9
   months at today's size, about 3.5 at launch size). Hetzner is real
   money from day one, though only about €150/month at today's scale.

**Suggested order**, each step useful even if repose stays on Azure:
1. Rent one AX102, or a 128 GB ECC NVMe auction box, as host-02.
2. Write the Hetzner host module.
3. Run the host-loss drill and a cross-host restore on host-02.
4. Decide on location and the edge after feeling the latency.

## Decisions for the owner, in order

1. **Hetzner or Azure.** This sets the price floor and whether an
   exe.dev-like price is possible at all.
2. **What repose sells next to exe.dev** (see memory: continue, shrink or
   shelve). The differences are `repose run` syncing the working tree,
   Nix-declared machines with snapshots, agent notifications and
   questions, and now `repose fork`. A subscription sells those.
3. **Hourly or subscription.** Follows from 1 and 2.
4. **Stripe or Paddle.** Decide on tax alone; if Paddle, before the first
   real invoice.

## Sources

- Paddle, what you can't sell:
  https://www.paddle.com/help/start/intro-to-paddle/what-am-i-not-allowed-to-sell-on-paddle
- Paddle, one-time charges on a subscription:
  https://developer.paddle.com/api-reference/subscriptions/create-one-time-charge
  and https://developer.paddle.com/build/subscriptions/bill-add-one-time-charge/
- Phare, metered billing on Paddle:
  https://phare.io/blog/what-paddle-doesnt-tell-you-about-implementing-metered-billing/
- Nadles, usage pricing with Paddle:
  https://www.nadles.com/blog/usage-based-pricing-with-paddle-pay-as-you-go/
- exe.dev pricing: https://exe.dev/pricing
- Hetzner AX servers: https://www.hetzner.com/dedicated-rootserver/matrix-ax/
- Hetzner server auction: https://www.hetzner.com/sb/
- Hetzner 2026 price changes:
  https://webhosting.today/2026/05/29/hetzner-has-now-raised-prices-three-times-in-2026-this-one-is-different/
- Auction trackers: https://radar.iodev.org/, https://gethetzner.com/products/auction/
