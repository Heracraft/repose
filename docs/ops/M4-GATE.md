# M4 gate runbook: the Paddle sandbox

The gate (`docs/MILESTONES.md` M4, DECISIONS I-289): in Paddle's sandbox, a
checkout with a test card creates a `trialing` subscription and a seat and
the webhook makes the account `trial`; a simulated `transaction.completed`
makes it `active`; a simulated `transaction.payment_failed` makes it
`past_due` and the 3-day tick stops the machine; an egress overage for a
known number of GB appears on the next transaction to the cent. This page
is the whole path from "here is a sandbox key" to the evidence for the
open rows of `docs/workstreams/09-billing.md` §9, in one sitting.

What the owner provides, exactly:

1. **A Paddle sandbox account** (sandbox-vendors.paddle.com) with an **API
   key** (Developer tools > Authentication > API keys; `pdl_sdbx_…`, with
   read and write on customers, transactions, subscriptions, products,
   prices and notification settings) and a **client-side token**
   (`test_…`, same page). Nothing else: the price ids and the webhook
   secret come out of the bootstrap.
2. For step 5 only: **the live account** (Paddle's domain review, which
   asks for `/terms`, `/privacy` and `/refunds` on the site), its key and
   token, and the owner's own card, on a day they choose. Anything that
   costs money is announced to the conductor first.

Conventions: `ra` below is `docker exec <api container> repose-admin` on
the control VM (the container has `DATABASE_URL` and, after step 1, every
`PADDLE_*` variable). Commands under "dev box" run from a checkout of
`main`. `psql` is `docker exec -it <postgres container> psql -U repose`.
Read the key into the shell without echoing it, never on a command line:

```
read -rs PADDLE_API_KEY && export PADDLE_API_KEY    # paste pdl_sdbx_..., Enter
```

## 0. Before: `main` is deployed

The api and web must run the code that has `billing paddle-bootstrap`,
`billing show`, `billing overage-now`, the plan page and the Paddle.js
checkout. `curl -s -o /dev/null -w '%{http_code}\n' -X POST
https://api.repose.herakraft.co/v1/billing/webhook` answers `503` until
step 1 is done.

## 1. Paddle objects and the api's environment (5 minutes)

Dev box:

```
ops/paddle/bootstrap.sh > /tmp/paddle.env
```

Stderr lists each object as `created` (first run) or `found` (any rerun):
`product solo`, `price solo`, `product plus`, `price plus`, `product
pro`, `price pro`, `discount intro`, `product overage`, `webhook`. Stdout, in
`/tmp/paddle.env`, is the block: `PADDLE_PRICE_SOLO`, `PADDLE_PRICE_PLUS`,
`PADDLE_PRICE_PRO`, `PADDLE_PRODUCT_OVERAGE`, `PADDLE_DISCOUNT_INTRO`,
`PADDLE_WEBHOOK_SECRET`. Paste it into the Coolify environment of **both**
`api` and `api-grpc` with `PADDLE_API_KEY` and `PADDLE_CLIENT_TOKEN` beside
it (both read billing: the webhook is served by `api`, the hourly jobs run
under the leader lock in whichever holds it), save, and let Coolify
restart them (`docs/ops/coolify.md` fact 16). Then `shred -u
/tmp/paddle.env`.

Check:

```
curl -s -o /dev/null -w '%{http_code}\n' -X POST https://api.repose.herakraft.co/v1/billing/webhook   # 400: signature required, billing is on
ra billing show <your handle>     # "subscription  - (no plan chosen)", not "billing is not configured"
```

In Paddle's dashboard, Developer tools > Notifications, the destination
`https://api.repose.herakraft.co/v1/billing/webhook` is listed with the
ten events. In the sandbox its traffic source is "all", so it also
receives the simulated events step 3 sends; Paddle refuses a simulation
for a "platform" destination, and a rerun of the bootstrap switches an
older one (I-600).

Checkout settings > Default payment link must be set to
`https://repose.herakraft.co/billing` (LAUNCH.md step 4) before anything
opens a checkout: without it Paddle refuses every checkout transaction
with `transaction_default_checkout_url_not_set`, and `TestPaddleSandbox`
below fails with that code.

Evidence for AZURE-SETUP step 17: the stderr of the bootstrap (object ids,
no secrets). Offline, the same code path is `TestBootstrapIsIdempotent`;
`REPOSE_PADDLE_SANDBOX_KEY=$PADDLE_API_KEY go test -run TestPaddleSandbox
-v ./internal/billing/` runs it against the real sandbox and creates a
customer and a checkout transaction as a smoke test.

## 2. Checkout to `trial` (10 minutes)

Use a non-exempt account (a second GitHub login, or an email sign-in at
`accounts.herakraft.co` with an inbox you can read: Logto sends a one-time
code; exempt accounts pass the gate and never reach checkout, I-16).

1. Sign in at `https://repose.herakraft.co`. `ra billing show <handle>`:
   `billing none`, `subscription - (no plan chosen)`. `repose run` in any
   checkout prints `Choose a plan at https://repose.herakraft.co/billing
   first.` and exits 7.
2. `/billing` shows Solo, Plus and Pro with the seats left. Choose **Solo**.
   Paddle's checkout opens in the page; the test card is `4242 4242 4242
   4242`, any future expiry, any CVC, a real-looking address (Paddle's
   sandbox cards are listed under Developer tools > Test cards). It
   completes with a $0 first transaction (the trial).
3. Within a few seconds the webhook delivers `subscription.created` and
   `transaction.completed`. `ra billing show <handle>`: `billing trial`,
   `subscription sub_… solo trialing (1 seat(s))`, `trial ends` seven days
   out, `next billed` the same instant. In psql:

   ```
   select id, type, processed_at, error from paddle_events order by received_at desc limit 5;
   select id, plan, status, seats, period_start, period_end, next_billed_at, trial_end from subscriptions;
   select handle, billing_status, has_card, paddle_customer_id from users where handle = '<handle>';
   ```

   Every event has `processed_at` and no `error`; the user row is `trial`,
   `has_card true`, with a `ctm_…` id.
4. `repose run` now creates and starts a `large`. A second `repose run
   --name other` in another checkout is refused: `Your Solo plan runs 8 GB
   at once and <slug> is using it. Stop it, or upgrade at …`, exit 7.
   `ra billing show` prints `running memory 8 of 8 GB: <slug> (large)`.

Evidence: the `show` output, the three psql results, the two CLI lines.

## 3. Payment events: `active`, `past_due`, the 3-day stop (15 minutes)

Paddle's sandbox does not advance time, so the payment events are
simulated. `ops/paddle/simulate.py activated|completed|failed <sub_…>`
(dev box, the sandbox key in the environment) sends one about the
account's own subscription, built from the subscription or its newest
transaction as Paddle has it, so the ids already match. By hand, from the
dashboard (Developer tools > Simulations), a simulated event carries
Paddle's example ids, so the api must be able to find the account: edit
the payload's `data.customer_id` to the account's `ctm_…` and
`data.subscription_id` to its `sub_…` (both in `ra billing show`), or add
`"custom_data": {"user_id": "<uuid>"}` from `select id from users where
handle = …`. An event for an unknown customer is recorded with an `error`
and changes nothing, which is itself a check.

A simulation changes the api's rows, not Paddle's subscription. The next
real event about it carries Paddle's own status again: `overage-now`
(step 4) and a cancel each make Paddle send `subscription.updated`, which
puts a simulated `past_due` back to `trial`. Run step 4 after this one.

1. **`subscription.activated`, then `transaction.completed`**
   (`simulate.py activated`, then `completed`), the order Paddle sends at
   a trial's end: the account moves `trial` → `active` (`ra billing show`:
   `billing active`, `solo active`). A `transaction.completed` alone
   leaves a `trialing` subscription's account in `trial`: the webhook
   reads it as the checkout's $0 transaction. Evidence: `show` and
   `paddle_events`.
2. **`transaction.payment_failed`** with the `subscription_id`: `billing
   past_due`, `past_due since` set, and the `payment_failed` email in the
   inbox (the `events` table: `select kind, ts, summary from events where
   user_id = … order by ts`). `repose start <slug>` is refused `Your last
   payment failed. Update your card at …`, exit 7; the running machine
   keeps running. A second simulated failure adds no second email.
3. **The 3-day stop.** The tick reads `past_due_since`; move it back
   rather than waiting: `update users set past_due_since = now() -
   interval '49 hours' where handle = '…'`, then the next hourly tick (or
   restart `api-grpc`: its first tick runs about a minute after it starts,
   when that is at :05 or later) sends day 2's `payment_failed`; then
   `… - interval '73 hours'` and the next tick snapshots and stops the
   machine (an `ops` row of kind `stop` with `params.reason = billing` and
   `snapshot: true`), writes the `billing_stopped` event, and `ra billing
   show` says `billing suspended`. `repose status` shows the project
   stopped; `repose start` is refused `Your account is suspended…`, exit 7.
4. **Paying.** Simulate `transaction.completed` again: `billing active`,
   `suspended -`, the machine still stopped (`repose status`), and
   `repose start <slug>` goes through. That is R4-11: a payment unblocks,
   the user restarts.

Evidence: the `show` outputs after each event, the `events` rows, the
`ops` row of the stop.

## 4. The overage line to the cent (15 minutes)

The line is computed from `usage_hours.egress_bytes` over the hours of the
period (`hour` from `period_start`). Give the account a known egress
rather than moving a terabyte: insert one hour 10 GB over the allowance
for its project. A first Solo checkout carries the introductory offer, so
the allowance is 100 GB, not 250 (`ra billing show` prints it on the
`limits` line). Put the row at an hour inside the period that the rollup
has not reached: the rollup rewrites the hour it rolls up, and the
checkout's own hour is before `period_start`. A day before `period_end`
does both.

```
insert into usage_hours (project_id, hour, class, running_seconds, gb_alloc, egress_bytes, cost_cents, period_start, period_end, price_version)
select p.id, date_trunc('hour', s.period_end) - interval '1 day', p.class, 0, 0, 110::bigint * 1073741824, 0, s.period_start, s.period_end, 'plan-v1'
from projects p join subscriptions s on s.user_id = p.user_id where p.slug = '<slug>' and s.status in ('trialing','active','past_due');
```

then `ra billing show <handle>`: `egress 110.00 GB of 100 GB included`,
`overage ceil(110.00 - 100) = 10 GB x 5 cents = 50 cents`. `ra billing
explain <slug> <that hour>` prints the same arithmetic for the hour. The
tick sends the line within three hours of `next_billed_at`; send it now:

```
ra billing overage-now <handle>     # "sent 10 GB over = 50 cents to Paddle for the period from …"
ra billing overage-now <handle>     # "already has its line …; nothing sent"
```

In Paddle's dashboard, Subscriptions > the subscription: the next
transaction preview carries one line `Egress overage: 10 GB over the Solo
plan's 100 GB (…) at $0.05/GB`, $0.50 before tax. `select * from overage_charges;`
has one row, 10 GB, 50 cents, `paddle_transaction_id` null until the
transaction is billed (the `transaction.completed` for it stamps the id).
To see the charge on a billed transaction in the sandbox, cancel the
account's trial in Paddle's dashboard with "bill immediately", or wait for
the trial to end: the invoice's total is $29.00 less the $9.00
introductory discount, plus $0.50, plus the sandbox's tax for the address
($22.31 for a New York ZIP, 2026-10-08).

The hard stop: `insert` another hour of 290 GB (400 in all, four times the
introductory 100), run the tick (or restart `api-grpc`): the machine stops
with reason `billing`, the `egress_stopped` email goes out, `repose start`
is refused `Your machines are stopped until <period end>: this period's
egress passed 400 GB…`, exit 7, and
a second tick does nothing more. Delete the two `usage_hours` rows
afterwards.

Evidence: the `show` and `explain` outputs, the `overage-now` lines, the
`overage_charges` row, a screenshot of the transaction preview with the
line, the `ops` row of the egress stop.

## 5. Live: one charge of the owner's own card (the owner's call)

Announce it to the conductor first. Dev box, with the live key read the
same way:

```
ops/paddle/bootstrap.sh --live > /tmp/paddle-live.env
```

Replace the sandbox block in both `api` and `api-grpc` with it (the live
`PADDLE_API_KEY` and `PADDLE_CLIENT_TOKEN` beside it). Live and sandbox
objects are separate: the owner signs in, chooses Solo at `/billing` with
their own card, and `ra billing show` says `trialing`. The first charge is
seven days later, or at once if the owner cancels the trial in Paddle's
dashboard with "bill immediately". The transaction id, `billing show`'s
line and the receipt Paddle emails are the evidence. A refund is the
owner's decision, made in Paddle's dashboard; nothing in the api changes
for it.

## If something is off

- The api will not start after the paste: the log names the missing
  variable (`PADDLE_API_KEY is set but PADDLE_PRICE_PRO … is not`); the
  block was cut short. Rerun the bootstrap and paste again.
- Webhooks rejected (`PaddleWebhookRejected`, `webhook_received
  result=bad_signature`): the secret in the environment is not the
  destination's (RUNBOOK). During a rotation both are accepted.
- A `paddle_events` row with an `error` naming `ctm_…`: the event was for
  a customer the api does not know; a simulated event whose ids were not
  edited (step 3), or another product on the same Paddle account.
- `overage-now` says `no billing period yet`: Paddle has not sent
  `current_billing_period` (a subscription made by hand in the dashboard
  before its first bill); the next `subscription.updated` fills it.
- A refused charge (`OverageChargeFailed`): RUNBOOK "OverageChargeFailed";
  the row waits, nothing is sent twice.
