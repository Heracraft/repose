# Launch runbook (owner)

What the owner does between this round's merge and the first tweet. The
code is on `main` and deploys itself when pushed (Coolify redeploys `api`,
`api-grpc` and `web`, `coolify.md` fact 16); the steps below are the ones
that need an account, a key or money, and so cannot be done by an agent.
Each step names the check that proves it. Dated 2026-09-27; the round is
DECISIONS I-289 to I-292.

## 0. Right after this push (five minutes)

An api with no `POLAR_ACCESS_TOKEN` has billing disabled: the
plan page says so, and the compute gate refuses every account that is not
`exempt` with `subscription_required`. Until Polar is configured, either
set `BILLING_ENFORCE=false` on both api apps (starts go through, nothing
is charged) or mark the accounts that must keep working exempt:
`repose-admin users exempt <handle>`. Remove `WAITLIST_PERCENT` from both
apps at the same time; the api ignores it and logs a warning (I-290).

## 1. Polar (about an hour)

Steps 2 to 5 are done in the sandbox (2026-10-08, DECISIONS I-604): prod's
api runs Polar's sandbox organization `repose` (sandbox.polar.sh), so
checkout takes Stripe's test card until step 6 replaces it.

1. Create a Polar account at https://polar.sh and an organization for
   repose (a sandbox organization is made separately at
   https://sandbox.polar.sh). Polar is the merchant of record: it reviews
   the organization before payouts and wants pricing, terms, privacy and
   a refund policy on the domain. All four are live after the web deploy:
   `/#pricing`, `/terms`, `/privacy`, `/refunds`.
2. In the sandbox first: Settings, Developers, create an organization
   access token (`polar_oat_...`). Paste it into the api's Coolify
   environment as `POLAR_ACCESS_TOKEN` with `POLAR_ENVIRONMENT=sandbox`
   (both `api` and `api-grpc` apps; the two must match, the grpc app runs
   the ticks).
3. Run the bootstrap from this checkout with the token in the environment:

   ```
   POLAR_ACCESS_TOKEN=... POLAR_ENVIRONMENT=sandbox ops/polar/bootstrap.sh > /tmp/polar.env
   ```

   It creates the meter "Egress overage", the three products (Solo $29,
   Plus $59, Pro $99, each with the metered overage price at 5 cents a GB
   and a seven-day trial), Solo's introductory discount ($9 off the first
   three charges, I-497), and the webhook endpoint at
   `https://api.repose.herakraft.co/v1/billing/webhook`; it sets the
   organization (one subscription per customer, trial abuse prevention,
   plan changes off in the customer portal, prices exclusive of tax) and
   prints the env block: `POLAR_ENVIRONMENT`, `POLAR_PRODUCT_SOLO`,
   `POLAR_PRODUCT_PLUS`, `POLAR_PRODUCT_PRO`, `POLAR_DISCOUNT_INTRO`,
   `POLAR_WEBHOOK_SECRET`. Paste it into both api apps and redeploy, then
   delete the file. It is idempotent: run it again and it creates nothing
   and prints the same ids.
4. Check Settings > Notifications in Polar: the bootstrap turned off the
   customer emails repose sends itself (trial ending, payment failed,
   cancellation, revoked, plan updated; I-291, I-604) and left the
   receipts, the subscription confirmation and the card-expiring reminder
   on. Leave it that way.
5. Prove the gate in the sandbox with `docs/ops/M4-GATE.md`: a checkout
   with Stripe's test card `4242 4242 4242 4242` on a throwaway account,
   the subscription appearing in `repose-admin billing show <handle>`, the
   trial ended at once so Polar charges the first period, a renewal on
   the failing card `4000 0000 0000 0341`, and an overage line with
   `repose-admin billing overage-now <handle>`.
6. Going live: create the production organization at https://polar.sh
   and its access token, then
   `POLAR_ACCESS_TOKEN=... POLAR_ENVIRONMENT=production ops/polar/bootstrap.sh --production --webhook-url https://api.repose.herakraft.co/v1/billing/webhook > /tmp/polar.env`,
   and paste the block with the token and `POLAR_ENVIRONMENT=production`
   into Coolify for `api` and `api-grpc`. Tests refuse a production
   environment.

A promotion needs a code change: the checkout turns Polar's discount
code field off (I-604). With it turned back on, a code made in Polar's
dashboard (Products, Discounts; for example 50% off the first three
months, limited to 20 redemptions) can go in the tweet.

## 2. Resend (ten minutes)

1. https://resend.com, add and verify the domain `repose.herakraft.co`
   (the DNS records are in `AZURE-SETUP.md` "Resend"), create an API key.
2. Set `RESEND_API_KEY` on both api apps and redeploy. `NOTIFY_FROM` stays
   `repose <notify@repose.herakraft.co>`.
3. Check: sign in with a fresh GitHub account; the `welcome` email arrives.
   The dashboard's Settings page has **Send test**.

## 3. Seats and the host (money)

0. Before the launch host registers, give it a free WireGuard address.
   The api numbers hosts on the edge's WireGuard network by themselves:
   the nth host registered gets 10.255.0.(n+1) (`hostmgr.allocate`), so
   host-01 has .2 and the next host gets .3. Your monitoring server
   already has .3 (`wg-repose`, I-170); it was picked by hand because it
   was free then. When the new host registers, the edge hands .3 to it:
   Grafana loses every scrape, and the hosts' logs to Loki on
   `10.255.0.3:3100` reach the new host instead. The next edge rebuild
   takes .3 back, and the new host loses its metrics and operator SSH
   (DECISIONS I-359). Do one of these first:
   - Preferred, nothing on your server changes: make `hostmgr.allocate`
     skip 10.255.0.3, so the new host gets .4. host-01 keeps .2.
   - Or move the monitoring server to 10.255.254.2, in the operator
     range: the `Address` in its `wg-repose.conf`, its entry in
     `nix/edge/edge-01.nix` (`staticPeers`, `monitoring.peerCIDRs`,
     `lokiUrl`), `repose-admin edge loki http://10.255.254.2:3100`, then
     an edge switch.

   Check after the host registers: `wg show wg0 allowed-ips` on the edge
   lists no address twice, and Grafana still shows host-01 `up`.
1. The launch host: `infra/azure/prod/prod.tfvars` `host_size =
   "Standard_D64s_v7"` and the 2048 GB data disk (I-14, I-39), then
   `make -C infra apply ENV=prod`. About $3,000 a month while it runs
   (`AZURE-SETUP.md`). Existing guests move with `repose-admin projects
   move`.
2. Until the host is up, or if you want to sell fewer seats than the fleet
   has, set `SEATS_TOTAL` on both api apps (30 for one `D64s_v7`; `0`
   derives it from the ready hosts, which is 6 on the `D16s_v7` today: its usable memory is a little under 64 GB minus the reserve). Check:
   `repose-admin seats` prints total, held, free and the source; the
   landing page's pricing section shows "N seats left".
3. Your own account and any tester's: `repose-admin users exempt <handle>`
   passes the gate without a plan and holds no seat.

## 4. Deploy order

1. Push `main`; watch CI and the three Coolify deploys. The api migrates
   itself (`0008_plans`, and any later one on `main`).
2. Publish the base for `repose browser` (Xvnc, the viewer page):
   `repose-admin base publish --rev <sha of main> --changelog "repose
   browser: one command, the screen follows your tab"`. Machines pick it
   up at their next start or the 04:00 UTC rebuild.
3. Tag the CLI: `git tag v0.1.21 && git push origin v0.1.21`; the release
   workflow builds the binaries and `repose` self-updates. The new plan
   sentences, hours in `repose status` and `repose browser` need it.
4. Try it end to end on a throwaway account: sign in, choose Solo in the
   sandbox, `repose run` on a small repo, `repose browser`, stop, `repose
   rm`, cancel the plan from the dashboard.

## 5. The tweet

The link is `https://repose.herakraft.co`. The landing page shows the two
plans and the live seat count, "Start with GitHub" leads to the dashboard,
the dashboard's Billing page to checkout or the waitlist. Watch:

- `repose-admin seats` and `repose-admin waitlist list` for the reading.
- Grafana's billing board: subscriptions by plan and status, gate refusals
  by reason, webhook results, overage charges.
- `repose-admin abuse list` for the first miners.

## What is not in this round

- The edge relay for the browser view and a dashboard button for it: the
  view still needs the CLI (`repose browser`), by design (I-292).
- Teams, annual plans, currencies other than USD (R5-6).
- Hetzner hosts (`proposals/2026-09-26-subscription-paddle-hetzner.md`);
  the prices are set for them, the hosts are not there yet.
