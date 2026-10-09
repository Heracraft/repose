#!/usr/bin/env bash
# Shows a Polar sandbox subscription, or ends its trial now so Polar
# charges the first period with the card on file (docs/ops/M4-GATE.md).
# With Stripe's test card 4000 0000 0000 0341 on the subscription the
# charge fails and Polar sends subscription.past_due: the gate's failed
# payment, made for real rather than simulated (DECISIONS I-604).
#
#   POLAR_ACCESS_TOKEN=polar_oat_... ops/polar/subscription.sh show SUB_ID
#   POLAR_ACCESS_TOKEN=polar_oat_... ops/polar/subscription.sh end-trial SUB_ID
#
# Sandbox only: a production environment is refused. The token is read
# from the environment, never from the command line.
set -euo pipefail

usage() {
	echo "usage: ops/polar/subscription.sh show|end-trial SUB_ID" >&2
	exit 2
}
[[ $# -eq 2 ]] || usage
cmd=$1 sub=$2
[[ -n "${POLAR_ACCESS_TOKEN:-}" ]] || { echo "POLAR_ACCESS_TOKEN is not set" >&2; exit 2; }
if [[ "${POLAR_ENVIRONMENT:-sandbox}" != sandbox ]]; then
	echo "POLAR_ENVIRONMENT is ${POLAR_ENVIRONMENT}; this helper runs against the sandbox only" >&2
	exit 2
fi

api() {
	curl -sS --fail-with-body -H "Authorization: Bearer ${POLAR_ACCESS_TOKEN}" -H "Polar-Version: 2026-10" \
		-H "Content-Type: application/json" "$@"
}
base="https://sandbox-api.polar.sh/v1/subscriptions/${sub}"
fields='{id, status, product: .product.name, current_period_start, current_period_end, trial_end, cancel_at_period_end, ends_at, past_due_at, discount: .discount.name, pending_update: .pending_update.product_id}'

case "$cmd" in
show) api "$base" | jq "$fields" ;;
end-trial) api -X PATCH "$base" -d '{"trial_end": "now"}' | jq "$fields" ;;
*) usage ;;
esac
