# Pricing

A monthly plan through Paddle, chosen before the first machine starts, with
a card at checkout and a week free. A plan buys memory that may run at once,
disk that may be allocated, and egress for the month; a plan sells no
project count, and a stopped project costs only its disk (DECISIONS
I-569). The shape is flat because the pitch is "the agent
keeps working after the laptop closes", and an hourly meter told people to
stop machines at night (DECISIONS I-289, superseding R2-12, R4-7, R4-8,
I-77, I-179 to I-185 and I-205; the hourly design is kept in
`workstreams/09-billing.md` §5 as history).

## Plans

| Plan | Price | Memory for running machines | Disk | Egress a month | Seats |
|---|---|---|---|---|---|
| Solo | $29 a month | 8 GB: one `large`, or two `small` | 100 GB | 250 GB | 1 |
| Plus | $59 a month | 16 GB: one `xl`, two `large`, any mix | 250 GB | 500 GB | 2 |
| Pro | $99 a month | 32 GB: two `xl`, four `large`, any mix | 500 GB | 1000 GB | 4 |

Solo costs $20 a month for its first 3 months, then $29: the
introductory price, for an account's first subscription only. The week
free comes first, so the first three charges after it are $20 and the
fourth is $29. Paddle charges it as a recurring discount of $9 restricted
to Solo's price, which the api attaches to a first Solo checkout; a user
who had any subscription before, or who checks out Plus or Pro, pays the
table's price. An upgrade from Solo during the three months ends the
introductory price, since the discount applies only to Solo's price
(DECISIONS I-497).
The introductory offer also has less egress: 100 GB of egress a period
while it runs (the free week and the three $20 periods), then 250 GB. The
$0.05 overage starts past 100 GB, and the stop at four times the allowance
is at 400 GB. On Azure a GB of egress costs about $0.087, so 250 GB is
$21.75, more than a $20 month; prod measured 28 GB for the whole fleet in
two weeks, so the cut costs a typical user nothing.

Three plans because one agent needs a `large`: a harness with its
language servers, builds and browser fills 8 GB, and a smaller machine
gets its session OOM-killed partway through. So Solo is one agent working,
Plus two, Pro four. Pro is for the user who runs several agents at once
across repositories, who already pays Anthropic $100 to $200 a month, and
who is the product's best source of feedback; it is not capped beyond the
seat count (DECISIONS I-362).

Prices are in USD and exclude tax; Paddle adds and remits the tax for the
buyer's country as merchant of record, which is the whole reason for Paddle
(DECISIONS I-289). Size classes keep their shapes (`small` 2 vCPU 4 GB,
`large` 4 vCPU 8 GB, `xl` 8 vCPU 16 GB, `docs/interfaces/README.md`); a
plan says how much of them may run at the same time.

What a plan means, in rules:

- **Memory.** The sum of the classes of a user's `running` (and starting,
  restoring) machines never exceeds the plan's memory. Starting one more is
  refused with `payment_required`, `detail.reason = plan_limit`, and the
  message names the machines using the memory: stop one or upgrade. An
  `xl` needs Plus or Pro, and the refusal names Plus.
- **Disk.** The sum of the allocated volume sizes of a user's live projects
  never exceeds the plan's disk. Creating a project or growing a volume past
  it is refused with `detail.reason = disk_limit`. Allocated, not used,
  because thin provisioning reserves it and the user chose the size.
  Snapshots are included and not counted.
- **Egress.** Bytes leaving the user's machines for the internet, summed
  over the billing period. Traffic to the gateway (SSH, the browser view,
  hooks) is not counted; ingress is free. Past the allowance, $0.05 a GB is
  added to the next invoice as one overage line (a Paddle one-time charge on
  the subscription, sent before the period locks). At four times the
  allowance (1 TB on Solo, 2 TB on Plus, 4 TB on Pro) the user's machines are stopped
  for the rest of the period with `detail.reason = egress_limit` and an
  `egress_stopped` email; that is the stolen-card ceiling, not a price.
- **Projects.** No count on any plan: memory caps what runs and disk caps
  what is kept, so a user stops one project and starts another within the
  plan. One abuse bound, 100 projects per account (running or stopped,
  the same on every plan and without one), stops a runaway script; it is
  in the Limits doc, not on the pricing page or the plan cards, and an
  operator can raise it for one account (DECISIONS I-569). Destroyed
  projects and their 30-day snapshots are free.

Example: a Solo user with a `large` running all month, a 40 GB disk, and
20 GB of egress pays $29. The same user with 300 GB of egress pays $29 plus
$2.50. Two `large` machines at once need Plus; three or four need Pro.

## The trial

Seven days free on any plan, card at checkout, first charge on day eight
unless cancelled. Paddle's `trial_period` on the price does this; the
account is `trial` until the first payment and `active` after. Nothing
stops on day eight; the card is charged and the plan continues. There is no
credit balance and no per-hour arithmetic any more; the trial is the same
plan, with the first charge a week out. Cancelling during the trial ends
the plan at the trial's end: machines stop then, snapshots stay 30 days.

The trial is the only free thing. No launch account gets a free plan
(owner, 2026-09-27: "I actually don't have enough money"); a discount code
made in Paddle's dashboard is how a promotion works, and none is required
by the code.

## Seats and the waitlist

A seat is 8 GB of memory that may run at once on the fleet: Solo holds one,
Plus two, Pro four. Memory is never oversubscribed, so the fleet has exactly as many
seats as its `ready`, undrained hosts have usable 8 GB blocks (RAM minus the
host reserve), or `SEATS_TOTAL` when the operator sets it (the launch host,
a `D64s_v7`, is 30 seats). Seats are held by every subscription that is
`trialing`, `active` or `past_due`, and by every waitlist invitation that
has not expired. Checkout for a plan needs that many free seats; without
them the user joins the waitlist, and the waitlist invites the oldest
waiting user whenever a seat is free, holding it 72 hours for them
(DECISIONS I-290, amending I-269). The landing page and the dashboard show
the seats left and the number waiting, because the launch is a gauge of
interest and the count is the reading.

## Failed payments

Paddle retries on its own schedule and the subscription is `past_due`.
Day 0: an email, starting a new machine is refused (`detail.reason =
past_due`), running ones keep running. Day 2: a second email. Day 3: every
running machine is snapshotted and stopped, a `billing_stopped` email goes
out and the account is `suspended`. Day 33: the 30-day retention that
started at suspension runs out and the snapshots go. A payment at any point
returns the account to `active` and unblocks starts; it does not start the
machines again (DECISIONS R4-11 stands).

## Cancelling and changing plans

A plan is cancelled from the dashboard or Paddle's portal and ends at the
period's end; machines run until then and stop at it, with the 30-day
retention from that day. Upgrading (to a plan with more seats) takes effect at once,
prorated by Paddle. Downgrading takes effect at the next renewal and is
refused while the user's running memory or allocated disk would not fit the
smaller plan. Deleting the account cancels the subscription at once, after
any pending egress overage is charged, because Paddle drops one-time charges
on a cancelled subscription.

## Refunds

The first charge after the trial is refunded on request within 14 days of
it. Renewals are not refunded for a part period; cancelling stops the next
one. Refunds are made through Paddle and appear on the same card. The
public statement is `apps/web/src/content/legal/refunds.md`, which Paddle's
domain review requires.

## The cost floor

The plans have to cover an always-on seat on the chosen host. Memory is the
binding constraint at 30 seats per host (256 GB minus 16 GB reserve, no
memory oversubscription; CPU is oversubscribed 2:1). The launch host is a
`D64s_v7` (DECISIONS I-39) at about $3,000 a month on demand, so a seat
that runs around the clock costs about $100 in compute before storage, the
control plane and the edge, and Solo at $29 loses about $70 a month on such
a user; a Pro user running four seats all month costs about $400 and loses
about $300. That is accepted for the launch (owner, 2026-09-27: "use Azure as a
cash sink for now"): the Azure credit pays for it while the product is
learned, the seat count caps the loss at the size of one host, and the
prices are set for the host the business ends up on. On a Hetzner AX162-R
(about €242 a month for 256 GB, `proposals/2026-09-26-subscription-paddle-hetzner.md`
§3) a seat costs about €8 and every plan clears its floor several times
over (Pro's four seats cost about €32 against $99). Nothing in the plan prices is tied to Azure; if hosts move, prices
stay and margin changes.

Paddle takes 5% plus 50¢ a transaction (Stripe would take about 2.9% plus
30¢ and leave the tax filing to us): $1.95 of a Solo month, $3.45 of a Plus
one, $5.45 of a Pro one.

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
the Paddle price ids in the environment) and in this doc. A change is a
`DECISIONS.md` entry, a 30-day notice to users by email, and a new Paddle
price; existing subscriptions keep their price until moved.
