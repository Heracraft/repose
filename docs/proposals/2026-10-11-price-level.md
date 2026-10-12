# Charging what it is worth (proposal, 2026-10-11)

**Status: ladder C chosen and shipped at launch, with the founding $39 for 12 months for the first 30 Solo subscribers (DECISIONS I-672); the launch trigger below was not taken. The rest is analysis.** The question: repose may
be priced too low for the buyer it names. I-362 defines that buyer as "the
heavy Claude Code user already paying Anthropic $100 to $200 a month and
running several agents across repositories". The plans today are Solo
$29, Plus $59 and Pro $99, with a first Solo subscription at $20 for 3
months (I-289, I-362, I-497). Any of this becomes real through
`DECISIONS.md` entries that amend those.

Outside prices were checked on 2026-10-11 and carry their URL in
"Sources". Figures marked *derived* are arithmetic on a vendor's published
unit prices; figures marked *unverified* could not be confirmed. A month
is 730 h; a "6 h/day month" is 180 h. The comparison shape is 8 GB and
4 vCPU, repose's `large`.

## Summary

| Question | Answer |
|---|---|
| Is $29 low for the market? | Yes. Metered always-on 8 GB / 4 vCPU runs $132 (Fly performance-4x) to $392 (E2B Pro) a month. Three offers sit near $29 to $51: exe.dev Personal (about $30, *unverified* at this size), Replit Reserved VM 2 vCPU / 8 GB ($50, a deployment with no workspace) and Fly shared-cpu ($50.76, *derived*, CPU-throttled) |
| Is $29 low for the buyer? | Yes. The tools this buyer pays for run $10 to $20 entry, $39 to $100 middle and $100 to $200 heavy (section 2). Solo sits beside Copilot Pro+ ($39) |
| Does $29 cover cost? | No host covers it. Hetzner AX162-2 is $33.07 a seat on a full host (I-642), $34.44 with its setup fee spread over 12 months; Solo nets $27.05 after Polar. Azure is about $100 a seat |
| Is the waitlist binding? | No. On 2026-10-11 `GET /v1/public/seats` returned 6 seats, 6 free, 0 waiting; `waitlist` has 0 rows and prod has 9 users, none paying |
| Cash effect of the price | Little at low fill. With 5 to 15 Solo seats on one Hetzner host, ladder C instead of today's price moves the owner's monthly result by $95 to $285; the $992.10 server bill dominates |
| Proposed list price | Ladder C: Solo $49, Plus $89, Pro $169, published once the waitlist binds (someone waiting on 14 days in a row) or the first Hetzner host takes seats, whichever comes first. Until then $29 / $59 / $99 stand |
| Proposed launch offer | Keep $20 x 3 until the switch (I-497 stands). At the switch, a founding Solo price of $39 for 12 months for the first 30 accounts (one AX162-2). It clears Hetzner on a full host (91% fill on Solo alone) and loses $29.59 a seat a month at half fill |
| Reach without seats | Option E: price the bring-your-own-host plan (I-647, shape A) as the cheap entry, free or $5 a host, so the hosted price can rise without shrinking the count of people using repose |
| Honesty line | "Launch prices. A change reaches you by email 30 days ahead. Your subscription keeps the list price it started on for 12 months from its start." Founding and introductory prices end on their stated date |
| Main risk | Claude Code on the web gives the same buyer a VM that pauses with its files saved, at $0 extra |
| Prerequisite | Whatever the ladder, a subscription on an old Polar product breaks the webhook today (`webhook.go:328`). Fix that before the live key |

---

## 1. One agent around the clock, priced elsewhere

Normalised to 8 GB and 4 vCPU. "Always-on" is 730 h of reservation;
"6 h/day" is 180 h with perfect auto-stop.

| Vendor | Unit price | Always-on month | 6 h/day month | Limit on always-on |
|---|---|---|---|---|
| GitHub Codespaces 4-core | $0.36/h, storage $0.07/GB-month | $262.80 *derived*, unbuyable | $64.80 + storage | Idle timeout 30 min default, 240 max; 12 h maximum lifetime "regardless of your idle timeout policy" |
| Vercel Sandbox Pro | $0.0212/GB-h, $0.128/active CPU-h, $20 credit | $161.19 at 10% CPU ($141.19 after credit); $217.25 at 25% | $39.74 at 10% CPU | 24 h per session; the limit "resets every time a sandbox stops and resumes" |
| exe.dev Work | $75 incl. 2 vCPU / 4 GiB, then $0.105 per unit-hour; intro $37.50 for 3 months | $151.65 | $37.80 standalone | None; pool reserved |
| exe.dev Personal | $15 for a 2 vCPU / 4 GiB pool | about $30, *unverified* | | None |
| Daytona | $0.0504/vCPU-h, $0.0162/GiB-h | $241.78 *derived* | $59.62 | Auto-stop 15 min default, 0 disables; default org ceiling 4 vCPU / 8 GB |
| E2B Pro | $0.000014/vCPU-s, $0.0000045/GiB-s, plus $150/month | $391.78 | $209.62 | "Up to 24 hours per sandbox session" on Pro, 1 h on Hobby; pause resets |
| Modal Sandbox | $0.1419/physical core-h, $0.024/GiB-h | $347.33 *derived*, upper bound | $85.64 | 5 min default lifetime, 24 h maximum |
| Fly Machines performance-4x | $33 per performance-1x 2 GB, linear | $132 *derived* | $32.55 | None |
| Fly Machines shared-cpu-4x 8 GB | RAM $6.00/GB-month; shared vCPU about $0.69 ($2.19 for shared-cpu-1x 256 MB minus 0.25 GB x $6) | $50.76 *derived* (4 x $0.69 + 8 x $6) | $12.52 | Metered per second; throttled to a baseline CPU share, so it does not match 4 dedicated vCPU |
| Fly Sprites | $0.0385/CPU-h, $0.021875/GB-h, bills running time only | $240.17 upper bound | $59.22 upper bound | None; idle cost *unverified* |
| Railway | $20/vCPU-month, $10/GB-month, Pro $20 incl. $20 usage | $160 | $39.45 | None |
| Ona (Gitpod) | from $20; 4 vCPU / 16 GB at 1 OCU/h; $10 per 40 OCU | $182.50 *derived* | $45 | 7-day inactive delete, *unverified*; now part of OpenAI (closed 2026-08-10) |
| Replit Reserved VM | 2 vCPU / 8 GB $50; 4 vCPU / 16 GB $130 | $50 to $130 | | Deployment product, no workspace |
| Claude Code on the web | included in Pro, Max, Team; 4 vCPU / 16 GB / 30 GB disk | $0 extra | $0 extra | An idle session's VM "pauses with its files saved"; a paused VM "can later be reclaimed", timing unpublished; Claude Code only |
| OpenAI Codex cloud | included in ChatGPT plans | $0 extra | $0 extra | Runtime limits *unverified* |
| Cursor cloud agents | model API price, no VM charge documented | $0 compute | $0 compute | Runtime limit *unverified* |

Metered always-on runs from $132 (Fly performance-4x) to $392 (E2B Pro).
Three offers sit between $29 and $51, each with a catch: exe.dev Personal
is *unverified* at 4 vCPU, Replit's $50 VM is a deployment with no
workspace, and Fly shared-cpu is metered per second and throttled. The lab
agents (Anthropic, OpenAI, Cursor) put the VM inside the model plan and
charge nothing for it. Claude Code on the web restores the same VM on the
next message after a pause, so its disk lasts until Anthropic reclaims the
paused VM; the doc gives no reclaim timing, the VM pauses once the session
goes idle, there is no tmux to attach to, and the agent has to be Claude
Code.

Codespaces cannot keep one process running past 12 h without a restart,
and Vercel, E2B and Modal stop one at 24 h. Vercel's counter resets on each
stop and resume, so a Vercel sandbox lives on in 24 h pieces; the
always-on figures for these four assume restarts at those limits, which
few agents survive with their session intact.

One always-on machine busy 47.5% of the time comes to about 350 busy
hours a month. The ratio comes from the owner's account: 436 busy of 918
running hours in the last 14 days, counting any use (attached, or an
agent working). That account is an operator's, exempt from billing, and
ran about 2.7 machines at once, about 947 busy machine-hours a month, so
it describes the ratio and stands in for no customer. Paying for 350 busy
hours with perfect auto-stop costs $126 on Codespaces and $115.92 on
Daytona. On Vercel it is $77.28 at 10% CPU during those hours, or $104.16
at 25%. Solo charges $29 for that machine's whole month.

## 2. The buyer's current tool bill

| Tool | Entry | Middle | Heavy |
|---|---|---|---|
| Claude | Pro $20 | Max 5x $100 | Max 20x $200; Team Premium $125/seat |
| ChatGPT / Codex | Plus $20 | Pro $100 | Pro $200, $500 |
| Cursor | Pro $20 | Pro Plus $60 | Ultra $200; Teams $40/user |
| GitHub Copilot | Pro $10 | Pro+ $39 | Max $100 |
| Warp | Build $20 | Business $50/user | Max $200 |
| Devin | Pro $20 | Teams $80 + $40/seat | Max $200 |

The middle column runs $39 to $100; this document uses that band
throughout. Anthropic's cost page puts enterprise Claude Code at "around
$13 per developer per active day and $150-250 per developer per month".
Solo at $29 is 12% to 19% of that range, and $49 is 20% to 33%. Against a
Max 20x bill of $200, Solo at $29 adds 14.5% and at $49 adds 24.5%; a Pro
at $169 lands between Max 5x and Max 20x.

## 3. Price as a signal, and the reach it costs

- Patrick McKenzie argues that value comes from what the customer gets
  and cites one product that "would see higher sales if it were priced at
  triple". His Server Density write-up found tiered plans earned $0.41 per
  visitor against $0.19 for usage pricing, from 18 cards against 23. That
  compares price shapes on one product; it says nothing about how many
  developers a $49 seat turns away against $29.
- I-497 calls $20 "the cheapest always-on 8 GB in the market". A buyer
  comparing it with the $132 to $160 buyable range elsewhere can read that
  as a service that will not last. `solo-at-20.md` cites 6.1% monthly
  churn under $25 ARPA from ChartMogul, *unverified*.

The owner's goal is public recognition leading to a hire, acqui-hire or
adoption, and the owner already worries that a launch capped near 100 seats is too small to get
noticed (memory "owner-goal-recognition", 2026-10-10). Signups are that
goal's metric, and a higher price lowers them by an amount nobody here can
estimate: prod has 9 users, 0 waiting and 0 paying.

An argument that a higher price changes who gets a seat, and leaves the
count sold alone, holds only while the waitlist binds (I-290). It does not
bind today: the fleet reports 6 seats, 6 free and 0 waiting
(`GET /v1/public/seats`, 2026-10-11). Two conditions would justify ladder C.
Someone waiting on 14 days in a row means every seat sells at either
price, so C costs no reach. The first Hetzner host means the owner pays
$992.10 a month in cash instead of credit. Section 4's cash table shows ladder C adding $95 a month at
5 seats sold, too little to justify the signups it may cost before either
condition holds.

## 4. Ladders

Net per seat = price x 0.95 − $0.50 (Polar, `PRICING.md`). Seat cost:
Hetzner AX162-2 $992.10 for 30 seats, $33.07 a seat full (I-642, on
branch `hetzner-repricing`); Azure D64s_v7 about $100. The Hetzner figures
below leave out the one-time EUR 419 setup fee (`hetzner-repricing:
docs/PRICING.md:183`), about $493.50 at the same exchange rate; spread
over a 12-month host life it adds $41.13 a month, $1.37 a seat-month on a
full host, for a seat cost of $34.44.

Seats are held per subscription whether machines run or not (I-290,
`internal/billing/seats.go:37`), and idle machines are announced, never
stopped (I-262, R1-5). The 48% busy share therefore sets the price per busy
hour and leaves every cost in these tables where it is. A margin built on
releasing idle seats needs suspend-when-idle and amendments to R1-5,
I-262 and I-290; this proposal does not lean on it.

| Ladder | Solo / Plus / Pro | Net per seat | Margin per seat, Hetzner full | Margin per seat, Azure | Solo per busy hour (350 h) |
|---|---|---|---|---|---|
| A, today | $29 / $59 / $99 | $27.05 / $27.78 / $23.39 | −$6.02 / −$5.30 / −$9.68 | −$72.95 / −$72.23 / −$76.61 | $0.083 |
| B, Hetzner floor | $39 / $75 / $145 | $36.55 / $35.38 / $34.31 | +$3.48 / +$2.31 / +$1.24 | −$63.45 / −$64.63 / −$65.69 | $0.111 |
| C, AI middle tier | $49 / $89 / $169 | $46.05 / $42.03 / $40.01 | +$12.98 / +$8.96 / +$6.94 | −$53.95 / −$57.98 / −$59.99 | $0.140 |
| D, Max anchor | $59 / $109 / $199 | $55.55 / $51.53 / $47.14 | +$22.48 / +$18.46 / +$14.07 | −$44.45 / −$48.48 / −$52.86 | $0.169 |

With setup spread over 12 months, ladder B's margins fall to +$2.11 /
+$0.94 / −$0.13, and Pro on B loses money even on a full host.

Hetzner's $33.07 assumes all 30 seats are sold. The fill each ladder
needs to pay for the server, on Solo alone:

| Ladder | Solo seats to cover $992.10 | Fill | Fill with setup ($1,033.23) | Solo margin at half full ($66.14 a sold seat) |
|---|---|---|---|---|
| A | 36.7 | never | never | −$39.09 |
| B | 27.1 | 91% | 94% | −$29.59 |
| C | 21.5 | 72% | 75% | −$20.09 |
| D | 17.9 | 60% | 62% | −$10.59 |

The owner pays a Hetzner host in cash every month, sold or not. Monthly
result for one AX162-2 by Solo seats sold, setup excluded:

| Solo seats sold | A ($29) | $20 intro | B ($39) | C ($49) | D ($59) |
|---|---|---|---|---|---|
| 5 | −$856.85 | −$899.60 | −$809.35 | −$761.85 | −$714.35 |
| 10 | −$721.60 | −$807.10 | −$626.60 | −$531.60 | −$436.60 |
| 15 | −$586.35 | −$714.60 | −$443.85 | −$301.35 | −$158.85 |
| 30 | −$180.60 | −$437.10 | +$104.40 | +$389.40 | +$674.40 |

Ladder C instead of A changes the result by $19 a seat: $95 a month at 5
seats, $285 at 15, $570 at 30. Below about 20 seats every ladder leaves
the owner paying most of the server. The cash case for repricing is
strongest at a full host, which is also the waitlist-binding case from
section 3.

On Azure, Solo breaks even at $105.79, a price no ladder a $20-to-$200
buyer recognises reaches; every row there is a loss paid in credit. On a
full 30-seat Azure host of Solo seats, ladder C cuts the monthly credit
burn from $2,188.50 to $1,618.50. The remaining Azure credit and the month
it runs out are *unverified*: neither appears in the repo, and the owner
holds them.

Break-even list prices on a full Hetzner host are Solo $35.34, Plus
$70.15 and Pro $139.77, or Solo $36.78 with setup spread over 12 months.

Each ladder keeps I-362's rule that Pro costs less than two Plus. B, C and
D fall per GB at each step; A rises from Solo to Plus.

| Ladder | Solo $/GB | Plus $/GB | Pro $/GB | 2 x Plus |
|---|---|---|---|---|
| A | 3.63 | 3.69 | 3.09 | $118 |
| B | 4.88 | 4.69 | 4.53 | $150 |
| C | 6.13 | 5.56 | 5.28 | $178 |
| D | 7.38 | 6.81 | 6.22 | $218 |

**Option E: a hosted ladder plus a bring-your-own-host plan.** I-647 chose
shape A of `byo-host-shape-a:docs/proposals/2026-10-10-bring-your-own-host.md`:
users attach their own KVM boxes to the hosted control plane. A user on
their own host holds no seat, so their cost to the owner is the edge
relaying their SSH traffic (Azure egress at about $0.087 a GB, bytes per
user unmeasured) and a share of a control plane that is already paid. That
proposal names two plan shapes, free with an account or about $5 a host,
of which Polar keeps 75 cents. Paired with ladder C for hosted seats, E
gives a $0 or $5 entry for anyone with a box, and the hosted price stops
being the number that decides reach. The catch is reach of a different
kind: the box must be x86_64 with `/dev/kvm` and becomes NixOS, a much
bigger ask than Coolify's "any Linux with Docker", and shape A is not built
yet.

**Ladder C is the proposal for hosted seats, on the trigger in section 3,
with E as the entry once it ships.** Solo at $49 costs $11 less than Cursor
Pro Plus ($60), 37% of Fly performance-4x ($132) and under a third of
Railway ($160). It clears Hetzner at 72% fill (75% with setup), leaving
room for a host that is not sold out. Pro at $169 is four agents for less
than one Max 20x seat, which keeps I-362's argument in a new form. Ladder B
needs 91% fill (94% with setup), and its Pro loses money once setup is
counted. Ladder D's Solo at $59 matches Cursor Pro Plus ($60) and costs
$20 more than Copilot Pro+ ($39), and its Pro at $199 sits a dollar under
Max 20x, so the four-agents argument stops reading as a saving.

## 5. The introductory offer, and the people who saw $20

I-497 is the owner's call from 2026-10-05: "lets make the $20 now $29 in 3
months a reality". The landing has shown "$20, First 3 months for new
subscribers, then $29" since then (`apps/web/src/routes/+page.svelte:116`,
deployed on push). Replacing that offer undoes I-497. Anyone who saw $20
and returns to find $39 or $49 meets the rug pull the owner said they
fear. A change before the live key grandfathers no Polar subscription,
but it still reaches everyone who read the landing.

Counted read-only on prod on 2026-10-11: 9 users, 0 rows in `waitlist`,
2 cancelled sandbox subscriptions and no active one. The people exposed so
far are few and known by account.

Today $20 nets $18.50 and loses $14.57 a month on a full Hetzner host
($43.71 per new user over 3 months) and $81.50 on Azure ($244.50). I-497
accepted that as acquisition cost while Azure credit pays.

| Option | Code | Cost per new user, Hetzner | Effect |
|---|---|---|---|
| Keep $20 x 3 before $49 | `IntroCents: 2000` | −$43.71 on a full host | A 59% discount reads as the real price |
| Follow the list: $29 x 3 | `IntroCents: 2900` | −$18.06 on a full host | Keeps the shape, smaller gap |
| Founding: $39 x 12 | `IntroCents: 3900, IntroMonths: 12` | +$41.76 over 12 months on a full host; −$355.08 at half fill | Clears Hetzner on a full host (91% fill on Solo alone); loses $29.59 a seat a month at half fill |
| Drop it | `IntroCents: 0` | $0 | The free week is the only offer |

**The proposal keeps $20 x 3 while the list stays $29**, so I-497 stands
until the trigger. At the switch to ladder C:

- New accounts get the founding $39 x 12, capped at 30 redemptions, one
  AX162-2's worth. On a full host of founding seats that is +$1,252.80
  over 12 months (+$759.60 with setup); against ladder C's list it gives
  up $3,420. On Azure the same 30 seats burn $22,842 of credit in 12
  months. Under a binding waitlist the host is full by definition, so the
  full-host row is the expected one.
- Accounts created before the switch date keep $20 x 3 then $29 on their
  first Solo subscription, held 12 months from its start. Today that is 9
  accounts. It needs a created-before check beside `EverSubscribed`
  (`subscriptions.go:109`), the old `intro-solo-2000-3` discount left
  live, and the legacy product mapping in section 6.

The founding offer fits the existing machinery: `plans.go` already models
a per-plan intro as a fixed Polar discount repeating for `IntroMonths`
(`polar.go`, key `intro-solo-3900-12`), and `ChargeCents` already quotes
the discounted amount. The cap needs `max_redemptions` on the Polar
discount, which `CreateIntroDiscount` does not send today; Polar's
discount API supports it.

`IntroEgressGB` can go back to zero on Hetzner, where egress at this
volume is included in the server (*unverified* for the AX162-2). I-497
cut it to 100 GB because 250 GB of Azure egress ($21.75 at $0.087 a GB)
exceeded a $20 month. On Azure, 250 GB adds $21.75 to a $39 seat already
$63.45 under cost, $85.20 a month in all.

## 6. Grandfathering and the honesty line

Polar locks a subscription to the price it started on; a new price
reaches new subscriptions alone. The repo makes three different promises
about it:

- `docs/PRICING.md`, "Changing prices": "existing subscriptions keep
  their price until moved".
- `apps/web/src/content/legal/terms.md:119`: "an existing plan keeps its
  price until that date", the end of a 30-day notice.
- The launch line suggested to the owner: early subscribers keep their
  price "for a while".

The code keeps none of them past one price change. A new `PriceCents`
makes a new Polar product (`bootstrap.go`, key `plan-<id>-<cents>`), the
config maps one product per plan, and a webhook for a subscription on the
old product fails with "subscription %s has no repose plan product"
(`webhook.go:328`). `ChargeCents` quotes the current list price, so
emails and `GET /billing` would show the grandfathered user the wrong
amount. This breaks today's "until moved" promise as much as any new one,
so section 7 lists the fix as a prerequisite for the live key, whatever
the ladder: legacy product ids mapped to a plan (`POLAR_PRODUCT_LEGACY`),
a `subscriptions.price_cents` column filled from the webhook, and
`ChargeCents` reading it.

Others' history argues for a dated hold said out loud. Cursor changed Pro
from 500 requests to a $20 credit pool in June 2025 without clear notice,
apologised, and refunded surprise charges from 16 June to 4 July.
Heroku gave 3 months' notice before ending its free tier in November 2022
and still met heavy backlash.

Proposed line, under the pricing cards and in the same words in
`PRICING.md` and `terms.md`:

> Launch prices. A change reaches you by email 30 days ahead. Your
> subscription keeps the list price it started on for 12 months from its
> start.

"The list price it started on" means the plan's full monthly price on the
subscription's start date. A founding or introductory price is a discount
with its own end date, which the checkout and the first email state: a
founding Solo pays $39 for its first 12 months, then $49, and a list
change announced during those 12 months reaches it no sooner than month
13 and 30 days after the email.

The line cuts `PRICING.md`'s "until moved" to 12 months, a reduction of
the strongest promise in the repo. The reason: "until moved" has no end,
so a seat sold at $29 on Azure would lose $72.95 a month for as long as
its owner stays, and the code cannot keep it today anyway. Twelve months
is long enough to plan a year around and caps how long a seat sold below
Azure cost ($63.45 a month at $39) can last. Decision 3 asks the owner to
accept that cut explicitly.

The line passes I-484 and I-485 because it prevents a loss the reader
cannot undo: a renewal at a price they did not choose.

## 7. Changes in the repo and at Polar

Prerequisite, independent of the ladder and due before the live key:

- `POLAR_PRODUCT_LEGACY` mapping old product ids to a plan, a migration
  adding `subscriptions.price_cents` filled from the webhook,
  `ChargeCents` reading it, and a `price_notice` email template (none
  exists; `plan_changed` is the nearest).

At the switch to ladder C:

- `internal/billing/plans.go:41-43`: `PriceCents` 4900 / 8900 / 16900;
  Solo `IntroCents: 3900`, `IntroMonths: 12`, `IntroEgressGB: 0`; the
  comment names the new entry.
- `docs/PRICING.md`: the table (the `TestPlansMatchPricingDoc` regex
  shape), the intro paragraph, the Pro rationale, the cost floor (stale
  "€8 a seat" text until I-642 merges), and "Changing prices".
- `apps/web/src/content/docs/billing.md`: table, footnote, worked
  example. `limits.md:30` and `nix/guest/base/agent-guide.md:102` lose
  the 100 GB introductory egress.
- `apps/web/src/routes/+page.svelte`: the `plans` array (Solo `price`
  and `then`) and the honesty line in the pricing section.
- `apps/web/src/content/legal/terms.md:119-120`: the price-change clause.
- Tests: `plans_test.go`, `emails_test.go`, `dunning_test.go`,
  `polar_sandbox_test.go`, `internal/api/notify` testdata,
  `apps/web/tests/landing.spec.ts`, `billing.spec.ts`, and
  `apps/web/tests-live/public.spec.ts:50-54`, which is already stale.
- Other docs quoting prices: `docs/DESIGN.md`, `docs/features/pricing.md`,
  `docs/ops/LAUNCH.md`, `docs/workstreams/09-billing.md`,
  `docs/interfaces/guest-conventions.md`.
- Polar: rerun `repose-admin billing polar-bootstrap`, which creates new
  products and the new discount; set `POLAR_PRODUCT_*` and
  `POLAR_DISCOUNT_INTRO` in Coolify; keep the old products active for
  grandfathered subscriptions and stop selling them.

## 8. Risks

- **Claude Code on the web costs $0 and survives a closed laptop.** Its
  VM pauses with its files saved and restores on the next message
  (cloud-environments). repose's case at $49 rests on a machine that keeps
  running while idle, a disk that is never reclaimed, any agent, tmux you
  attach to and reserved memory. The landing has to make that case at $49
  in a way it never had to at $20.
- **The buyer's model plan can throttle an always-on agent.** Anthropic
  added weekly limits to Pro and Max from 28 August 2025, citing users
  running Claude Code "24/7". An always-on seat is worth less to a buyer
  whose agent stops at a weekly cap, whatever repose charges, and the
  owner hopes Anthropic will notice the product.
- **Reach lost to a higher price.** Unmeasured. The trigger in section 3
  and option E exist to keep this small.
- **People who saw $20.** Section 5 honours them; skipping that turns a
  pricing change into the rug pull the owner named.
- **The VM anchor is falling.** OpenAI owns Ona; Codex, Cursor and
  Copilot bundle compute. The $132 to $392 metered range may not stay the
  buyer's reference.
- **Thin evidence.** Three accounts used machines in 14 days. Account 1
  is the owner's and account 2, which ran 299 h with zero use, is also
  `exempt` from billing (read-only check, 2026-10-11), so neither says
  anything about how price filters buyers. The 48% is any-use on the
  owner's account; across all three it is 35.8%, and `solo-at-20.md`
  measured 3.7% agent-working on a different definition.
- **Azure stays a loss.** Ladder C still loses $53.95 a Solo seat a month
  on Azure, and the credit balance is *unverified*.
- **Hetzner fill.** Ladder C loses money below 72% Solo fill (75% with
  setup).
- **A promise the code cannot keep.** Shipping any hold without the
  legacy mapping breaks webhooks for the first grandfathered user.
- **An acquirer reading price as a subsidy.** This draft first argued
  that a $29 seat losing $73 on Azure reads to an acquirer as a subsidy.
  No named acquisition supports it, so it plays no part in the
  recommendation.

## Suggested order

1. Build the legacy product mapping and `subscriptions.price_cents`
   (section 7, prerequisite).
2. Merge I-642 from `hetzner-repricing`, so a price entry can cite it.
3. Reserve ids with `ops/dev/release-queue id`: one entry for the
   price-change promise, one recording ladder C and its trigger as an
   amendment to I-289, I-362 and I-497.
4. Ship the honesty line with the live key.
5. On the trigger, change plans, docs, landing and tests in one commit,
   rerun the Polar bootstrap, and start the founding offer.
6. Price option E once shape A ships and the edge's bytes per user are
   measured.

## Decisions for the owner

1. The hosted ladder: A (today), B ($39 / $75 / $145), C ($49 / $89 /
   $169, proposed) or D ($59 / $109 / $199).
2. The trigger: reprice now, when the waitlist binds (someone waiting on
   14 days in a row, proposed), or when the first Hetzner host takes
   seats.
3. The grandfather promise: 30 days' notice only (no hold), a 12-month
   hold (proposed; a cut from today's "until moved"), or "until moved" as
   `PRICING.md` says today.
4. The launch offer at the switch: $20 x 3, $29 x 3, a founding $39 x 12
   capped at 30 accounts (proposed), or none; and honouring $20 x 3 for
   accounts created before the switch (proposed, 9 today).
5. Launch-price label on the landing: yes or no.
6. Option E's plan: free with an account, or about $5 a host.
7. The remaining Azure credit and the month it runs out, which this
   document could not find.

## Sources

Checked 2026-10-11 unless stated.

- GitHub Codespaces billing: https://docs.github.com/en/billing/concepts/product-billing/github-codespaces; lifecycle (12 h, idle timeout): https://docs.github.com/en/codespaces/about-codespaces/understanding-the-codespace-lifecycle. 16 GB on 4-core *unverified*.
- Vercel Sandbox pricing (page dated 2026-09-10): https://vercel.com/docs/vercel-sandbox/pricing. Pro seat fee *unverified*.
- exe.dev: https://exe.dev/pricing, https://exe.dev/docs/pools. Personal at 4 vCPU *unverified*.
- Daytona: https://www.daytona.io/pricing, https://www.daytona.io/docs/sandbox-management
- E2B: https://e2b.dev/pricing, https://docs.e2b.dev/sandbox/persistence
- Modal: https://modal.com/pricing, https://modal.com/docs/guide/sandbox
- Fly Machines: https://docs.fly.io/about/pricing (4x presets and shared vCPU price *derived*); Sprites: https://fly.io/sprites
- Railway: https://railway.com/pricing
- Replit: https://docs.replit.com/billing/deployment-pricing, https://replit.com/pricing
- Ona: https://ona.com/docs/ona/billing/usage, https://ona.com/stories/ona-joins-openai. Tiers and 7-day delete *unverified*.
- Claude Code on the web: https://code.claude.com/docs/en/claude-code-on-the-web; pause, reclaim and VM size: https://code.claude.com/docs/en/cloud-environments; costs: https://code.claude.com/docs/en/costs
- Claude plans: https://claude.com/pricing/max, https://claude.com/pricing
- ChatGPT and Codex: https://learn.chatgpt.com/docs/pricing
- Cursor: https://cursor.com/docs/cloud-agent; tier prices: https://cursor.com/docs/account/pricing
- GitHub Copilot: https://github.com/features/copilot/plans
- Devin: https://devin.ai/pricing
- Warp: https://www.warp.dev/pricing
- Polar subscriptions: https://polar.sh/docs/features/subscriptions/introduction; discounts: https://polar.sh/docs/features/discounts.md
- McKenzie: https://www.kalzumeus.com/2006/08/14/you-can-probably-stand-to-charge-more/, https://www.kalzumeus.com/2012/08/13/doubling-saas-revenue/
- Cursor 2025: https://techcrunch.com/2025/07/07/cursor-apologizes-for-unclear-pricing-changes-that-upset-users/, https://cursor.com/blog/june-2025-pricing
- Anthropic weekly limits: https://simonwillison.net/2025/Jul/28/anthropic/, https://gigazine.net/gsc_news/en/20250729-anthropic-calude-code-rate-limits
- Heroku 2022: https://techcrunch.com/2022/08/25/heroku-announces-plans-to-eliminate-free-plans-blaming-fraud-and-abuse, https://devclass.com/2022/09/27/after-axing-free-plans-heroku-introduces-low-cost-tier-including-scale-to-zero
- Repo: `docs/PRICING.md`, `docs/DECISIONS.md` (I-262, I-289, I-290, I-362, I-497; I-642 on `hetzner-repricing`; I-647 on `byo-host-shape-a`), `docs/proposals/2026-10-04-solo-at-20.md`, `docs/proposals/2026-10-10-bring-your-own-host.md` on `byo-host-shape-a`, `internal/billing/{plans,polar,bootstrap,subscriptions,webhook,seats}.go`, `apps/web/src/routes/+page.svelte`.
- Prod, read-only on 2026-10-11: `meter_samples` for the last 14 days, `users` (count, `billing_status`), `waitlist`, `subscriptions`, and https://api.repose.herakraft.co/v1/public/seats.
