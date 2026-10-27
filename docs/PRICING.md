# Pricing

A monthly plan through Polar (DECISIONS I-604, which moved billing off
Paddle), chosen before the first machine starts, with a card at checkout
and a week free. A plan buys memory that may run at once,
disk for the bytes its projects hold, and egress for the month; a plan
sells no project count, and a stopped project costs only the disk it holds
(DECISIONS I-569, I-585). The shape is flat because the pitch is "the agent
keeps working after the laptop closes", and an hourly meter told people to
stop machines at night (DECISIONS I-289, superseding R2-12, R4-7, R4-8,
I-77, I-179 to I-185 and I-205; the hourly design is kept in
`workstreams/09-billing.md` §5 as history).

## Plans

| Plan | Price | Memory for running machines | Disk | Egress a month | Seats |
|---|---|---|---|---|---|
| Solo | $49 a month | 8 GB: one `large`, or two `small` | 100 GB | 250 GB | 1 |
| Plus | $89 a month | 16 GB: one `xl`, two `large`, any mix | 250 GB | 500 GB | 2 |
| Pro | $169 a month | 32 GB: two `xl`, four `large`, any mix | 500 GB | 1000 GB | 4 |

The prices are ladder C of `proposals/2026-10-11-price-level.md`,
chosen for launch (DECISIONS I-672, superseding I-362's $29, $59 and $99).
An always-on agent costs about $263 a month on GitHub Codespaces and about
$161 on Vercel Sandbox, and the buyer already pays Anthropic $100 to $200
a month; $49 is a fifth to a third of the first two.

Solo costs $39 a month for its first 12 months, then $49: the founding
price, for the first 30 subscribers, on an account's first subscription
only (DECISIONS I-672, superseding I-497's $20 for 3 months). The week
free comes first, so the first twelve charges after it are $39 and the
thirteenth is $49. Polar charges it as a fixed discount of $10, repeating
for 12 months and restricted to Solo's product, which the api attaches
to a first Solo checkout while fewer than 30 subscriptions carry it
(I-604); Polar counts the months from the first charged period, the
trial's end. The count is the api's: subscriptions in any status that
carry the discount, so a cancelled founding subscription keeps its place
and an upgrade gives its place back. `GET /public/seats` reports the
places left as `founding_left`, and the landing's Solo card shows $49
once none are. The discount applies to the whole order, so the overage
line on a $39 renewal is billed in full; a user who had any
subscription before, or who checks out Plus or Pro, pays the table's
price. An upgrade from Solo during the twelve months ends the founding
price: the api removes the discount from the subscription at Polar with
the change, since the offer is Solo's only and Polar would otherwise
carry the $10 onto Plus or Pro (DECISIONS I-497, I-604). The founding
price keeps Solo's 250 GB of egress.

Three plans because one agent needs a `large`: a harness with its
language servers, builds and browser fills 8 GB, and a smaller machine
gets its session OOM-killed partway through. So Solo is one agent working,
Plus two, Pro four. Pro is for the user who runs several agents at once
across repositories, who already pays Anthropic $100 to $200 a month, and
who is the product's best source of feedback; it is not capped beyond the
seat count (DECISIONS I-362).

Prices are in USD and exclude tax; Polar adds and remits the tax for the
buyer's country as merchant of record, which is why billing goes through a
merchant of record (DECISIONS I-289, I-604). Size classes keep their memory
and disk; their vCPUs halved with I-784, to 4 GB of memory per guest vCPU
(`small` 1 vCPU 4 GB, `large` 2 vCPU 8 GB, `xl` 4 vCPU 16 GB, `DESIGN.md`
§5). No price moved with them. A plan says how much of them may run at the
same time.

A plan's rules:

- **Memory.** The sum of the classes of a user's `running` (and starting,
  restoring) machines never exceeds the plan's memory. Starting one more is
  refused with `payment_required`, `detail.reason = plan_limit`, and the
  message names the machines using the memory: stop one or upgrade. An
  `xl` needs Plus or Pro, and the refusal names Plus.
- **Disk.** The bytes a user's live projects' volumes hold, running or
  stopped, against the plan's disk (DECISIONS I-585). A volume's figure
  is its thin volume's allocated blocks, which is what the host's disk
  pays for; the guest trims its free blocks daily and at every stop, so a
  deleted file stops counting within a day. A volume's size (20, 40, 80 GB
  by class, or what a resize set) is only the ceiling one project can grow
  to, and may be at most the plan's whole disk. While the projects hold
  more than the plan's disk, a create, a restore or fork as a new project,
  and growing a volume are refused with `detail.reason = disk_limit`; a
  create counts a new volume's first 1 GB, a restore or fork its source's
  figure per copy. Nothing stops: running machines keep running, stopped
  ones start (starting one is how its files are deleted), and one
  `disk_over_plan` email a period says so. It is a cap, with no overage
  price. Snapshots are blob storage, included and not counted.
- **Egress.** Bytes leaving the user's machines for the internet, summed
  over the billing period. Traffic to the gateway (SSH, the browser view,
  hooks) is not counted; ingress is free. Past the allowance, $0.05 a GB is
  added to the next invoice as one overage line (one event on Polar's
  "Egress overage" meter, sent within three hours of the period's end,
  which Polar bills on the renewal order through the plan's metered
  price; I-604). At four times the
  allowance (1 TB on Solo, 2 TB on Plus, 4 TB on Pro) the user's machines are stopped
  for the rest of the period with `detail.reason = egress_limit` and an
  `egress_stopped` email; that is the stolen-card ceiling, not a price.
- **Projects.** No count on any plan: memory caps what runs and disk caps
  what is kept, so a user stops one project and starts another within the
  plan, and keeps many stopped ones: Solo's 100 GB holds a dozen projects
  of 8 GB each, whatever their disk sizes. One abuse bound, 100 projects per account (running or stopped,
  the same on every plan and without one), stops a runaway script; it is
  in the Limits doc, not on the pricing page or the plan cards, and an
  operator can raise it for one account (DECISIONS I-569). Destroyed
  projects and their 30-day snapshots are free.

Example: a Solo user with a `large` running all month, a 40 GB disk, and
20 GB of egress pays $49, or $39 in the founding months. The same user
with 300 GB of egress pays $49 plus $2.50. Two `large` machines at once need Plus; three or four need Pro.

## The trial

Seven days free on any plan, card at checkout, first charge on day eight
unless cancelled. The seven-day trial on each plan's Polar product does
this, and Polar's trial abuse prevention gives an email or card one trial
only; the
account is `trial` until the first payment and `active` after. Nothing
stops on day eight; the card is charged and the plan continues. There is no
credit balance and no per-hour arithmetic any more; the trial is the same
plan, with the first charge a week out. Cancelling during the trial ends
the plan at the trial's end: machines stop then, snapshots stay 30 days.

The trial is the only free thing. No launch account gets a free plan
(owner, 2026-09-27: "I actually don't have enough money"); a discount code
made in Polar's dashboard is how a promotion works, and none is required
by the code. The checkout turns codes off today (I-604), so a promotion
needs that changed first.

## Seats and the waitlist

A seat is 8 GB of memory that may run at once on the fleet: Solo holds one,
Plus two, Pro four. A seat is also 2 guest vCPUs, since every class is 4 GB
per vCPU (I-784), so a host delivers the smaller of two ceilings: the usable
8 GB blocks its memory divides into (RAM minus the host reserve) and the
seats its physical cores can serve while typing still feels immediate. The
fleet has as many seats as its `ready`, undrained hosts deliver, counted per
host and summed, or `SEATS_TOTAL` when the operator sets it (DECISIONS
I-739, I-784). Hosts are sized one by one, so the hosts in a fleet differ:
host-01, a `D16s_v7`, is 4 seats, host-02, the `D64s_v7` the launch adds
beside it, is 24, and the fleet is **28** while both are `ready`. Seats are
held by every subscription that is `trialing`, `active` or `past_due`, and by
every waitlist invitation that has not expired. Checkout for a plan needs
that many free seats; without
them the user joins the waitlist, and the waitlist invites the oldest
waiting user whenever a seat is free, holding it 72 hours for them
(DECISIONS I-290, amending I-269). The landing page and the dashboard show
the seats left and the number waiting, because those two numbers measure
interest at launch.

## Failed payments

Polar retries the card 2, 7, 14 and 21 days after the first failure, then
revokes the subscription; from the first failure it is `past_due` (I-604).
Day 0: an email, starting a new machine is refused (`detail.reason =
past_due`), running ones keep running. Day 2: a second email. Day 3: every
running machine is snapshotted and stopped, a `billing_stopped` email goes
out and the account is `suspended`. Day 33: the 30-day retention that
started at suspension runs out and the snapshots go. A payment at any point
returns the account to `active` and unblocks starts; it does not start the
machines again (DECISIONS R4-11 stands).

## Cancelling and changing plans

A plan is cancelled from the dashboard or Polar's customer portal and ends at the
period's end; machines run until then and stop at it, with the 30-day
retention from that day. Upgrading (to a plan with more seats) takes effect at once,
with Polar charging the prorated difference that day. Downgrading takes effect at the next renewal and is
refused while the user's running memory or the disk their projects hold
would not fit the smaller plan. Deleting the account sends the period's egress overage first; with none
the subscription is revoked at once, and with some it is cancelled at the
period's end so Polar bills the overage on the final order, because a
revoke ends the period without billing metered usage. No new period is
charged either way (I-604).

## Refunds

The first charge after the trial is refunded on request within 14 days of
it. Renewals are not refunded for a part period; cancelling stops the next
one. Refunds are made through Polar and appear on the same card. Polar
may also refund a charge within 60 days on its own, to head off a
chargeback, which cancels the subscription. The public statement is
`apps/web/src/content/legal/refunds.md`.

## The cost floor

**Every figure in this section is owed a re-derivation and is wrong until
it gets one** (I-739, `proposals/2026-10-14-host-cpu-capacity.md` §7 item
6). It is computed at 30 seats a host; the seat derivation puts a
`D64s_v7` at 24 (11 between I-739 and I-784), so a seat costs about $129
rather than $100. I-784 recovered most of the gap and none of the
arithmetic below: the prices themselves are the owner's call, so nothing
below has been changed.

The plans have to cover an always-on seat on the host it runs on. Memory is
the binding constraint at 30 seats per host (256 GB minus 16 GB reserve, no
memory oversubscription; CPU is oversubscribed at most 1.5 committed
guest vCPUs per physical core, I-734). The seats the launch sells are on
host-02, a `D64s_v7` (DECISIONS I-39) at about $3,000 a month on demand, so a seat
that runs around the clock costs about $100 in compute before storage, the
control plane and the edge, and Solo at $49 loses about $54 a month on such
a user ($63 at the founding $39); a Pro user running four seats all month
costs about $400 and loses about $240. That is accepted for the launch (owner, 2026-09-27: "use Azure as a
cash sink for now"): the Azure credit pays for it while the product is
learned, the seat count caps the loss at what the fleet's hosts deliver, and
the prices are set for the host the business ends up on.

That host is no longer cheap. Hetzner's 15 June 2026 repricing (DECISIONS
I-642) took the 256 GB AX162 from about €242 to €842.30 a month ($992.10,
plus a €419 setup fee), so a seat on it costs about $33 a month, not the
€8 this section first said. A plan earns per seat, after Polar's fee,
at I-672's prices:

| Plan | Per seat a month | Against an AX162-2 seat ($33.07) | Against an Azure seat (~$100) |
|---|---|---|---|
| Solo | $46.05 | +$12.98 | −$53.95 |
| Solo, founding $39 | $36.55 | +$3.48 | −$63.45 |
| Plus | $42.03 | +$8.96 | −$57.97 |
| Pro | $40.01 | +$6.94 | −$59.99 |

Hetzner's dedicated servers, per seat, in USD for new orders, excluding
IPv4 (seats are RAM minus the 16 GB reserve, in 8 GB blocks):

| Server | CPU | RAM | $ a month | Seats | $ a seat |
|---|---|---|---|---|---|
| AX162-2 | EPYC 9454P, 48 cores | 256 GB | 992.10 | 30 | 33.07 |
| AX162-3 | EPYC 9454P, 48 cores | 512 GB | 1,897.10 | 62 | 30.60 |
| EX131-2 | Xeon Gold 6731P | 256 GB | 1,297.10 | 30 | 43.24 |
| AX102-2 | Ryzen 9 7950X3D, 16 cores | 192 GB | 642.10 | 22 | 29.19 |
| EX63-2 | Core Ultra 7 265 | 192 GB | 632.10 | 22 | 28.73 |
| AX162-1-LTD | EPYC 9454P, 48 cores | 128 GB | 372.10 | 14 | 26.58 |
| EX131-2-LTD | Xeon Gold 6731P | 256 GB | 602.10 | 30 | 20.07 |

At I-362's $29, $59 and $99 a seat ran on a regular Hetzner server at a
loss of about $5 to $10 a month on every plan. At I-672's prices every
plan clears an AX162-2 seat, by $3.48 (founding Solo) to $12.98 (Solo),
before disk, egress, snapshots, the edge and the control plane, and only
on a full host: a subscription holds its seats whether its machines run
or not ("Seats and the waitlist"), so the fleet pays for every seat sold
around the clock, and an empty seat earns nothing. Ladder C clears the
AX162-2's $992.10 once 22 Solo seats are sold (75% fill with the setup
fee spread over 12 months; `proposals/2026-10-11-price-level.md` §4). The
server auction was not priced: its data no longer loads without
JavaScript. On Azure every plan still loses $54 to $63 a seat, which the
credit pays.

Polar takes 5% plus 50¢ a transaction on its Starter rate, the same as
Paddle did (I-604; Stripe alone would take about 2.9% plus
30¢ and leave the tax filing to us): $2.95 of a Solo month ($2.45 of a
founding one), $4.95 of a Plus one, $8.95 of a Pro one.

## What is deliberately not priced

- Snapshots: included. They are small (used blocks, compressed) and the
  retention is fixed.
- Builds: included, capped by time and cores instead.
- Notifications, the gateway, the browser view, the dashboard: included.
- Support: email only.
- Teams, seats for other people, annual plans, currencies other than USD:
  deferred (R5-6).

## Changing prices

Prices live in one place in the API's configuration (`internal/billing/plans.go`,
the Polar product ids in the environment) and in this doc. A change is a
`DECISIONS.md` entry, a 30-day notice to users by email, and a new Polar
price. A subscription keeps the list price it started on for 12 months
from its start (DECISIONS I-672, which cut "until moved"); a founding
price is a discount with its own end, stated at checkout. The pricing
page says so in one line: "Launch prices. A change reaches you by email
30 days ahead. Your subscription keeps the list price it started on for
12 months from its start."

The code cannot keep that hold yet. A new `PriceCents` makes a new Polar
product, the config maps one product per plan, and the webhook refuses a
subscription on an old product. Before the next price change ships, the
api needs old product ids mapped to their plan, the subscription's own
price stored from the webhook, and `ChargeCents` reading it
(`proposals/2026-10-11-price-level.md` §6). I-672 shipped without them
because no subscription was live.
