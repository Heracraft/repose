# M4 gate runbook: the Polar sandbox

The gate (`docs/MILESTONES.md` M4, DECISIONS I-289, I-604): in Polar's
sandbox, a checkout with a test card creates a `trialing` subscription and
a seat and the webhook makes the account `trial`; ending the trial charges
the first period and makes it `active`; a renewal on a card that fails
makes it `past_due` and the 3-day tick stops the machine; a payment lifts
it; an egress overage for a known number of GB is billed to the cent.
Nothing is simulated: every event is one Polar sent about a real sandbox
subscription. This page is the whole path from "here is a sandbox token"
to the evidence for the open rows of `docs/workstreams/09-billing.md` §9,
in one sitting.

What the owner provides, exactly:

1. **A Polar sandbox organization** (sandbox.polar.sh; production runs
   the organization `repose` there until launch) with an **organization
   access token** (Settings > Developers; `polar_oat_…`). Nothing else:
   the product, discount and meter ids and the webhook secret come out of
   the bootstrap.
2. For step 6 only: **the production organization** at polar.sh, its
   token, and the owner's own card, on a day they choose. Anything that
   costs money is announced to the conductor first.

Conventions: `ra` below is `docker exec <api container> repose-admin` on
the control VM (the container has `DATABASE_URL` and, after step 1, every
`POLAR_*` variable). Commands under "dev box" run from a checkout of
`main`. `psql` is `docker exec -it <postgres container> psql -U repose`.
Read the token into the shell without echoing it, never on a command line:

```
read -rs POLAR_ACCESS_TOKEN && export POLAR_ACCESS_TOKEN POLAR_ENVIRONMENT=sandbox    # paste polar_oat_..., Enter
```

Test cards are Stripe's, any future expiry, any CVC: `4242 4242 4242
4242` succeeds; `4000 0000 0000 0341` attaches but every charge on it
fails. Polar gives an email or a card one trial only (trial abuse
prevention), so each new throwaway account needs a card Polar has not
seen, such as `5555 5555 5555 4444`; a reused one gets the checkout
without the trial.

## 0. Before: `main` is deployed

The api and web must run the code that has `billing polar-bootstrap`,
`billing show`, `billing overage-now`, the plan page and the redirect to
Polar's hosted checkout. `curl -s -o /dev/null -w '%{http_code}\n' -X POST
https://api.repose.herakraft.co/v1/billing/webhook` answers `503` until
step 1 is done.

## 1. Polar objects and the api's environment (5 minutes)

Dev box:

```
ops/polar/bootstrap.sh > /tmp/polar.env
```

Stderr lists each object as `created` (first run) or `found` (any rerun):
`organization`, `meter overage`, `product solo`, `product plus`, `product
pro`, `discount intro`, `webhook`. Stdout, in `/tmp/polar.env`, is the
block: `POLAR_ENVIRONMENT`, `POLAR_PRODUCT_SOLO`, `POLAR_PRODUCT_PLUS`,
`POLAR_PRODUCT_PRO`, `POLAR_DISCOUNT_INTRO`, `POLAR_WEBHOOK_SECRET` (on
the run that creates the endpoint). Paste it into the Coolify environment
of **both** `api` and `api-grpc` with `POLAR_ACCESS_TOKEN` beside it (both
read billing: the webhook is served by `api`, the hourly jobs run under
the leader lock in whichever holds it), save, and let Coolify restart
them (`docs/ops/coolify.md` fact 16). Then `shred -u /tmp/polar.env`.

Check:

```
curl -s -o /dev/null -w '%{http_code}\n' -X POST https://api.repose.herakraft.co/v1/billing/webhook   # 400: signature required, billing is on
ra billing show <your handle>     # "subscription  - (no plan chosen)", not "billing is not configured"
```

In Polar's dashboard, Settings > Webhooks, the endpoint
`https://api.repose.herakraft.co/v1/billing/webhook` is listed, format
raw, with the eight events of 09-billing.md §5.11. Products shows `repose
Solo`, `repose Plus` and `repose Pro`, each with its monthly price, the
metered "Egress overage" price and the seven-day trial. Settings >
Notifications has the trial conversion reminder, past due, cancellation,
revoked and updated customer emails off: repose sends those itself
(I-291, I-604).

Evidence for AZURE-SETUP step 17: the stderr of the bootstrap (object ids,
no secrets) and a rerun's, which creates nothing. Offline, the same code
path is `TestBootstrapIsIdempotent`. Against the real sandbox,
`go test -run TestPolarSandbox -v ./internal/billing/` (dev box, the token
and `POLAR_ENVIRONMENT=sandbox` in the environment, a test Postgres in
`DATABASE_URL`) runs the bootstrap twice and creates a checkout; its doc
comment describes the end-to-end mode, which serves the webhook for
`polar listen` and walks the whole flow once a person pays the checkout.

## 2. Checkout to `trial` (10 minutes)

Use a non-exempt account (a second GitHub login, or an email sign-in at
`accounts.herakraft.co` with an inbox you can read: Logto sends a one-time
code; exempt accounts pass the gate and never reach checkout, I-16).

1. Sign in at `https://repose.herakraft.co`. `ra billing show <handle>`:
   `billing none`, `subscription - (no plan chosen)`. `repose run` in any
   checkout prints `Choose a plan at https://repose.herakraft.co/billing
   first.` and exits 7.
2. `/billing` shows Solo, Plus and Pro with the seats left. Choose **Solo**.
   The browser goes to Polar's hosted checkout (`sandbox.polar.sh`). It
   shows "7 days free", the line "Solo introductory price, until <date>",
   "Additional metered charges may apply", and tax on top for the
   address's country. Pay with `4242 4242 4242 4242`; nothing is charged.
   Polar returns the browser to `/billing?checkout=done`, which waits for
   the subscription and then shows the plan.
3. Within a few seconds the webhook delivers `subscription.created`. `ra
   billing show <handle>`: `billing trial`, `polar customer` set,
   `subscription <id> solo trialing (1 seat(s))`, `trial ends` seven days
   out, `next billed` the same instant. In psql:

   ```
   select id, type, processed_at, error from billing_events order by received_at desc limit 5;
   select id, provider, plan, status, seats, period_start, period_end, next_billed_at, trial_end, intro, intro_until from subscriptions;
   select handle, billing_status, has_card, billing_customer_id from users where handle = '<handle>';
   ```

   Every event has `processed_at` and no `error`; the subscription is
   `polar`, `intro true`; the user row is `trial`, `has_card true`, with
   Polar's customer id.
4. `repose run` now creates and starts a `large`. A second `repose run
   --name other` in another checkout is refused: `Your Solo plan runs 8 GB
   at once and <slug> is using it. Stop it, or upgrade at …`, exit 7.
   `ra billing show` prints `running memory 8 of 8 GB: <slug> (large)`.

Evidence: the `show` output, the three psql results, the two CLI lines, a
screenshot of the checkout page.

## 3. The trial's end: `active` (5 minutes)

Dev box, the subscription id from `ra billing show`:

```
ops/polar/subscription.sh end-trial <subscription id>
```

It sends `PATCH /v1/subscriptions/{id}` with `{"trial_end": "now"}`, and
Polar charges the first period on the card: $29 less the $9 introductory
discount, plus tax. Polar sends `subscription.updated`, `subscription.active`
and `order.paid`. `ra billing show`: `billing active`, `solo active`,
`next billed` a month out. `GET /billing/invoices` (the dashboard's
Billing page) lists the order, `paid`, with a PDF link once Polar has
generated the invoice.

Evidence: `show`, the `billing_events` rows, the order in Polar's
dashboard (Sales > Orders) with its amount.

## 4. A failed payment: `past_due`, the 3-day stop, paying (15 minutes)

Use a second throwaway account and check out Solo with `4000 0000 0000
0341`: the card attaches and the trial starts as in step 2. Start a
`large` on it. Then end its trial:

```
ops/polar/subscription.sh end-trial <its subscription id>
```

1. **The charge fails.** Polar sends `subscription.past_due`: `ra billing
   show` says `billing past_due`, `past due since` set, and the
   `payment_failed` email is in the inbox, with no past-due email from
   Polar beside it (the `events` table: `select
   kind, ts, summary from events where user_id = … order by ts`). `repose
   start <slug>` is refused `Your last payment failed. Update your card at
   …`, exit 7; the running machine keeps running. Polar retries 2, 7, 14
   and 21 days after the failure; another `subscription.past_due` adds no
   second email.
2. **The 3-day stop.** The tick reads `past_due_since`; move it back
   rather than waiting: `update users set past_due_since = now() -
   interval '49 hours' where handle = '…'`, then the next hourly tick (or
   restart `api-grpc`: its first tick runs about a minute after it starts,
   when that is at :05 or later) sends day 2's `payment_failed`; then
   `… - interval '73 hours'` and the next tick snapshots and stops the
   machine (an `ops` row of kind `stop` with `params.reason = billing` and
   `snapshot: true`), writes the `billing_stopped` event, and `ra billing
   show` says `billing suspended`. `repose status` shows the project
   stopped; `repose start` is refused `Your account is suspended…`, exit 7.
3. **Paying.** On `/billing`, open the portal and replace the card with
   `4242 4242 4242 4242`. Polar retries the open order as soon as the card
   is updated and sends `order.paid`: `billing active`,
   `suspended -`, the machine still stopped (`repose status`), and
   `repose start <slug>` goes through. That is R4-11: a payment unblocks,
   the user restarts.

Evidence: the `show` outputs after each event, the `events` rows, the
`ops` row of the stop, the `order.paid` row in `billing_events`.

## 5. The overage line to the cent, and the hard stop (15 minutes)

On the first account. The line is computed from `usage_hours.egress_bytes`
over the hours of the period (`hour` from `period_start`). Give the
account a known egress rather than moving a terabyte: insert one hour 10
GB over the allowance for its project. A first Solo subscription carries
the introductory offer, so the allowance is 100 GB, not 250 (`ra billing
show` prints it on the `limits` line). Put the row at an hour inside the
period that the rollup has not reached: the rollup rewrites the hour it
rolls up. A day before `period_end` does both.

```
insert into usage_hours (project_id, hour, class, running_seconds, gb_alloc, egress_bytes, cost_cents, period_start, period_end, price_version)
select p.id, date_trunc('hour', s.period_end) - interval '1 day', p.class, 0, 0, 110::bigint * 1073741824, 0, s.period_start, s.period_end, 'plan-v1'
from projects p join subscriptions s on s.user_id = p.user_id where p.slug = '<slug>' and s.status in ('trialing','active','past_due');
```

then `ra billing show <handle>`: `egress 110.00 GB of 100 GB included`,
`overage ceil(110.00 - 100) = 10 GB x 5 cents = 50 cents`. `ra billing
explain <slug> <that hour>` prints the same arithmetic for the hour. The
tick sends the line within three hours of `period_end`; send it now:

```
ra billing overage-now <handle>     # "sent 10 GB over = 50 cents to Polar for the period from … (event overage:<id>:<unix>)"
ra billing overage-now <handle>     # "already has its line …; nothing sent"
```

`select * from overage_charges;` has one row, 10 GB, 50 cents, `sent_ref`
the external id. Polar counts it on the meter at once: dev box,

```
curl -s -H "Authorization: Bearer $POLAR_ACCESS_TOKEN" -H "Polar-Version: 2026-10" \
  "https://sandbox-api.polar.sh/v1/meters/<meter id>/quantities?external_customer_id=<user uuid>&start_timestamp=<period start>&end_timestamp=<period end>&interval=day" | jq .total
```

prints `10` (the meter id is in the bootstrap's stderr; the user uuid is
`select id from users where handle = …`), and the customer portal shows
10 units of metered usage. The renewal order a month later carries the
line "Egress overage", 10 units at $0.05, $0.50 before tax, beside $29
less the $9 introductory discount; the discount does not reduce it. That
order is the final evidence; until it exists, the meter's quantity and
the portal are.

The hard stop: `insert` another hour of 290 GB (400 in all, four times the
introductory 100), run the tick (or restart `api-grpc`): the machine stops
with reason `billing`, the `egress_stopped` email goes out, `repose start`
is refused `Your machines are stopped until <period end>: this period's
egress passed 400 GB…`, exit 7, and a second tick does nothing more.
Delete the two `usage_hours` rows afterwards.

Evidence: the `show` and `explain` outputs, the `overage-now` lines, the
`overage_charges` row, the meter quantity, the renewal order with the
line, the `ops` row of the egress stop.

## 6. Live: one charge of the owner's own card (the owner's call)

Announce it to the conductor first. Dev box, with the production token
read the same way:

```
POLAR_ENVIRONMENT=production ops/polar/bootstrap.sh --production --webhook-url https://api.repose.herakraft.co/v1/billing/webhook > /tmp/polar-live.env
```

Replace the sandbox block in both `api` and `api-grpc` with it (the
production `POLAR_ACCESS_TOKEN` beside it, `POLAR_ENVIRONMENT=production`).
Sandbox and production objects are separate: the owner signs in, chooses
Solo at `/billing` with their own card, and `ra billing show` says
`trialing`. The first charge is seven days later, or at once if the owner
ends the trial in Polar's dashboard. The order, `billing show`'s line and
the receipt Polar emails are the evidence. A refund is the owner's
decision, made in Polar's dashboard; it cancels the subscription, and the
webhook follows.

## If something is off

- The api will not start after the paste: the log names the missing
  variable (`POLAR_ACCESS_TOKEN is set but POLAR_PRODUCT_PRO is not`); the
  block was cut short. Rerun the bootstrap and paste again.
- Webhooks rejected (`BillingWebhookRejected`, `webhook_received
  result=bad_signature`): the secret in the environment is not the
  endpoint's (RUNBOOK). During a rotation both are accepted.
- A `billing_events` row with an `error` naming a customer: the event was
  for a customer the api does not know, such as another product on the
  same Polar organization.
- The checkout shows no trial: Polar has seen the email or the card
  before. Use a fresh account and a card Polar has not seen.
- `overage-now` says `no billing period yet`: Polar has not sent the
  subscription's current period; the next `subscription.updated` fills it.
- A refused event (`OverageChargeFailed`): RUNBOOK "OverageChargeFailed";
  the row waits and the next tick resends it under the same external id.
