# Billing: the plan

What a user sees about money: the plan they choose before the first
machine starts, the week that is free, the number in `repose status`, the
invoice each month, and what happens when a payment fails. The prices and
rules are in [`../PRICING.md`](../PRICING.md); the implementation is
[`../workstreams/09-billing.md`](../workstreams/09-billing.md) §5.11
(DECISIONS I-289, I-290, I-291).

## Choosing a plan

```
$ repose run
Choose a plan at https://repose.herakraft.co/billing first.
```

The CLI prints the api's sentence for every `payment_required` and exits
7 (`internal/cli/payment.go`). A new account has no plan
(`billing.status = none`): `repose run`, `repose start`, creating,
restoring and forking a project all answer `payment_required` with
`detail.reason = subscription_required` until one is chosen.

The dashboard's billing page shows the three plans, Solo at $29 (shown as
$20 "for 3 months, then $29 and 250 GB egress" with 100 GB of egress to
an account that never had a subscription, DECISIONS I-497), Plus at
$59 and Pro at $99 a month, and how many seats are left. "Choose" opens Paddle's checkout
in the page (Paddle.js with a transaction the api made, so the seat is
held and the account is stamped before the card form appears); the card
and the billing address go to Paddle, never to the platform, and Paddle
adds the tax for the buyer's country as merchant of record. The first
week is free: the card is taken at checkout and first charged on day
eight. When the webhook arrives the account is `trial`, and from the first
real charge `active`.

When the fleet has no free seat, "Choose" answers `503 waitlisted`:
`repose is full right now. You're number 3 on the waitlist; we'll email
you@example.com when there's a seat.` The user is on the waitlist from
then on and gets one email when a seat is theirs, held for 72 hours
(DECISIONS I-290).

## What the plan buys

| Plan | Running at once | Disk | Egress a month |
|---|---|---|---|
| Solo | 8 GB: one `large`, or two `small` | 100 GB | 250 GB |
| Plus | 16 GB: one `xl`, two `large`, any mix | 250 GB | 500 GB |
| Pro | 32 GB: two `xl`, four `large`, any mix | 500 GB | 1000 GB |

A plan sells no project count: projects cost nothing but their disk
while stopped, and every account may have 100, running or stopped
(`limits.md`, DECISIONS I-569). The month costs the same however
much runs, and nothing is metered by the hour. The other reasons
`payment_required` carries, each with the whole sentence as `message`:

| `detail.reason` | When | What the user reads |
|---|---|---|
| `plan_limit` | the running memory plus this machine's class would pass the plan's | `Your Solo plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at https://repose.herakraft.co/billing.` (an `xl` on Solo: `an xl machine needs 16 GB. Upgrade to Plus`) |
| `disk_limit` | the allocated disk plus this volume would pass the plan's | `Your Solo plan allocates up to 100 GB of disk and your projects use 70 GB; this needs 40 GB more. Destroy a project, or upgrade at …` |
| `egress_limit` | this period's egress passed four times the allowance | `Your machines are stopped until 1 November: this period's egress passed 1000 GB, four times the Solo plan's 250 GB allowance. Upgrade at …, or wait for the period to end.` |
| `past_due` | the last payment failed | `Your last payment failed. Update your card at … to start machines again.` |
| `suspended` | three days past due, or an operator suspension | `Your account is suspended. Pay at … to lift it, or email support.` |

`detail` also carries `plan`, `limit_gb`, `used_gb` and, for
`plan_limit`, `projects` (the slugs using the memory), so a dashboard can
draw the sentence itself.

Egress past the allowance is not a refusal: $0.05 a GB is added to the
next invoice as one line, `Egress overage: 50 GB over the Solo plan's 250
GB (1 Oct to 1 Nov 2026) at $0.05/GB`. The billing page shows the period's
egress and the overage so far; `repose-admin billing show` prints the
arithmetic to the cent.

An account an operator has marked billing-exempt (`repose-admin users
exempt`) passes all of these; it still records hours and egress, so the
numbers below are still real for it (DECISIONS I-16).

## Seeing the hours

```
$ repose status
todo-app   large  running   2h14m   claude: working      today 2h14m  month 41h
```

`running_seconds_today` and `running_seconds_month` on a project come from
the same `usage_hours` rows the overage line is computed from. Usage is
rolled up once an hour, at five past, so the figure lags by up to an hour
and a bit. Hours are information, not a bill: the plan is what is charged.

A gap in the host's samples under-counts that hour: the minutes it did not
hear about are not counted and are never estimated. That makes a host
outage visible as a short figure rather than an invented one, and the
egress it did not see is egress the user is not charged for.

## The invoice

Monthly, through Paddle, charged to the card given at checkout: the plan's
price, tax for the buyer's country, and at most one egress overage line.
`GET /billing/invoices` and the dashboard list them with the PDF; the
portal (`POST /billing/portal`) is where a user changes the card or the
address and downloads receipts. Paddle sends its own receipt emails.

## Changing and cancelling

Upgrading to a bigger plan takes effect at once, prorated by Paddle on the
next invoice, and needs the extra seats free. Downgrading takes effect at
the next renewal and is refused (`409 conflict`, `detail.reason =
over_plan`) while the running memory or allocated disk would not fit the
smaller plan; stop or
destroy first. Cancelling ends the plan at the period's end (during the
trial, at the trial's end): machines run until then, stop at it, and the
snapshots stay 30 days. A cancellation can be undone until it takes
effect. Every change sends one email (`plan_changed`,
`subscription_cancelled`, `subscription_ended`).

## A failed payment

Paddle retries on its own schedule. Meanwhile:

| When | What happens |
|---|---|
| day 0 | the payment fails; the account is `past_due`; machines keep running; starting one is refused; one `payment_failed` email |
| day 2 | a second `payment_failed` email |
| day 3 | every running machine is snapshotted and stopped, a `billing_stopped` notification goes out, the account becomes `suspended` |
| day 33 | the 30-day retention that started at suspension runs out and the snapshots go |

Nothing is deleted before day 33 (DECISIONS R4-11). Paying at any point
before then returns the account to `active` and unblocks starts; it does
not start the machines again. That is the user's call, made with `repose
start`, so nobody is surprised by machines that restarted themselves.

## "I dispute this charge"

`repose-admin billing show <handle>` prints the subscription, the period,
what ran, the egress and the overage arithmetic; `repose-admin billing
explain <project> <hour>` prints one hour's inputs and the period's line.
A refund is made in Paddle's dashboard and lands on the same card
(PRICING.md "Refunds"); nothing in `usage_hours` or `overage_charges` is
edited. `ops/RUNBOOK.md` "Customer disputes a charge" has the procedure.

## Deferred

- Per-seat or team pricing (no teams in the first release, DECISIONS R5-6).
- Currencies other than USD, annual plans, promotions beyond Paddle's own
  discount codes.
- Idle auto-stop (R1-5); an idle machine holds its share of the plan's
  memory and the CLI says so.
