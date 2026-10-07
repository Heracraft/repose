#!/usr/bin/env bash
# docs/CHECKLIST.md: every command, flag, endpoint, message, table and field
# named in docs/workstreams/09-billing.md §5.11 (DECISIONS I-289) exists
# with that exact name.
set -u
cd "$(dirname "$0")/.."
miss=0
row() {
  local what="$1" pat="$2" where="$3"
  local hit
  hit=$(rg -n --no-heading -m1 -- "$pat" $where 2>/dev/null | head -1)
  if [ -z "$hit" ]; then printf '  MISSING  %-46s %s\n' "$what" "$pat"; miss=1
  else printf '  ok       %-46s %s\n' "$what" "$hit"; fi
}
echo "== tables and columns (migration 0008) =="
for t in subscriptions paddle_events overage_charges; do row "table $t" "create table $t" internal/db/migrations/0008_plans.up.sql; done
for c in plan status seats period_start period_end next_billed_at trial_end cancel_at scheduled_plan overage_charged_for; do row "subscriptions.$c" "^  $c " internal/db/migrations/0008_plans.up.sql; done
row "users.paddle_customer_id" "rename column stripe_customer_id to paddle_customer_id" internal/db/migrations/0008_plans.up.sql
row "users.billing_status none" "'none','trial','active','past_due','suspended','exempt'" internal/db/migrations/0008_plans.up.sql
row "usage_hours plan-v1" "set default 'plan-v1'" internal/db/migrations/0008_plans.up.sql

echo "== routes (docs/interfaces/api.md) =="
for r in '/v1/usage' '/v1/billing"' '/v1/billing/checkout' '/v1/billing/plan' '/v1/billing/cancel' '/v1/billing/resume' '/v1/billing/portal' '/v1/billing/invoices' '/v1/billing/webhook'; do row "route $r" "$r" internal/api/http/routes.go; done

echo "== repose-admin billing subcommands =="
for c in Rollup Explain Show OverageNow PaddleBootstrap; do row "billing${c}" "^func \(e \*Env\) billing${c}\(" internal/admin; done
row "billing suspend|unsuspend (alias of users)" "the same action as \`users" internal/admin/cmds.go
row "usage line in repose-admin help" "^  billing " internal/admin/admin.go
row "ops/paddle/bootstrap.sh" "billing paddle-bootstrap" ops/paddle/bootstrap.sh

echo "== plans (internal/billing/plans.go) =="
for k in Solo Plus Pro EgressHardStopMultiplier OveragePerGBCents SeatGB; do row "$k" "^\s*$k\b" internal/billing/plans.go; done
row "Plans" "^var Plans\b" internal/billing/plans.go
row "PriceVersion plan-v1" '^const PriceVersion = "plan-v1"' internal/billing/plans.go
for f in PlanByID ClassMemoryGB OverageCents Price; do row "func $f" "^func $f\(" internal/billing/plans.go; done
for f in NewPaddle NewWebhooks NewGate NewOverage NewDunning NewRollup NewService Bootstrap AccountEvent LiveSubscription LimitsFor WaitlistPlace Sign LoadAccount Explain RecordEnforcement; do row "func $f" "^func $f\(" internal/billing; done
for f in EnsureCustomer CreateCheckoutTransaction GetSubscription UpdateSubscriptionItems CancelSubscription ResumeScheduledChange CreateOneTimeCharge PortalSession ListTransactions InvoicePDF; do row "Paddle.$f" "^func \(p \*Paddle\) $f\(" internal/billing/paddle.go; done

echo "== gate reasons (api.md payment_required) =="
for k in subscription_required plan_limit disk_limit egress_limit past_due suspended; do row "reason $k" "\"$k\"" internal/billing/gate.go; done

echo "== webhook events (5.11) =="
for k in subscription.created subscription.activated subscription.trialing subscription.updated subscription.past_due subscription.paused subscription.resumed subscription.canceled transaction.completed transaction.payment_failed; do row "$k" "\"$k\"" internal/billing/webhook.go; done

echo "== account event kinds (I-291) =="
for k in trial_ending payment_failed subscription_cancelled subscription_ended plan_changed egress_stopped billing_stopped; do row "$k" "\"$k\"" internal/billing/events.go; done

echo "== metrics =="
for k in repose_api_billing_webhook_total repose_api_billing_overage_charges_total repose_api_billing_gate_refused_total repose_api_billing_subscriptions_total repose_api_billing_stops_total repose_api_rollup_duration_seconds repose_api_billing_gap_minutes_total; do row "$k" "$k" internal/api/metrics/metrics.go; done
for k in webhook_received overage_charged gate_refused billing_stopped billing_gap; do row "log event $k" "\"$k\"" internal/obs/events.go; done

echo "== alerts and runbook headings =="
for a in PaddleWebhookRejected OverageChargeFailed BillingStopped; do row "alert $a" "alert: $a" ops/alerts.yaml; row "alert test $a" "alertname: $a" ops/alerts_test.yaml; done
for h in PaddleWebhookRejected OverageChargeFailed BillingStopped "Customer disputes a charge" "Move a user between plans by hand"; do row "runbook ## $h" "^## $h" docs/ops/RUNBOOK.md; done
row "dashboard billing" '"repose-billing"' ops/dashboards/gen.py

echo "== env (ops/coolify/api.env.example) =="
for v in PADDLE_API_KEY PADDLE_WEBHOOK_SECRET PADDLE_CLIENT_TOKEN PADDLE_PRICE_SOLO PADDLE_PRICE_PLUS PADDLE_PRICE_PRO PADDLE_PRODUCT_OVERAGE PADDLE_DISCOUNT_INTRO PADDLE_PORTAL_RETURN_URL SEATS_TOTAL BILLING_ENFORCE; do row "$v" "^$v" ops/coolify/api.env.example; done
for v in PADDLE_API_KEY PADDLE_WEBHOOK_SECRET PADDLE_CLIENT_TOKEN PADDLE_PRICE_SOLO PADDLE_PRICE_PLUS PADDLE_PRICE_PRO PADDLE_PRODUCT_OVERAGE PADDLE_DISCOUNT_INTRO BILLING_ENFORCE; do row "$v read" "\"$v\"" internal/billing/config.go; done

echo "== nothing of Stripe left in code =="
# internal/cli/scan.go and its testdata name "stripe" as an npm script in a
# scanned package.json (a tenant's word, not ours); the 0003 migration and
# its columns are history db-schema.md names as unused.
# internal/fakes/api is the web workstream's and moves to Paddle with it.
globs=(-g '!internal/cli/scan*' -g '!internal/cli/testdata/**' -g '!internal/db/migrations/**' -g '!internal/fakes/**' -g '!*09-billing-names.sh')
if rg -n -i "stripe" "${globs[@]}" internal cmd ops/coolify ops/alerts.yaml ops/dashboards/gen.py scripts go.mod >/dev/null 2>&1; then
  printf '  MISSING  %-46s %s\n' "no stripe in code" "$(rg -n -i stripe "${globs[@]}" internal cmd ops/coolify ops/alerts.yaml ops/dashboards/gen.py scripts go.mod | head -1)"; miss=1
else
  printf '  ok       %-46s\n' "no stripe in code"
fi

exit $miss
