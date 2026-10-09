#!/usr/bin/env bash
# Creates, or finds, every Polar object the api needs and prints the
# POLAR_* block to paste into the api's Coolify environment (DECISIONS
# I-604, docs/ops/M4-GATE.md).
#
#   ops/polar/bootstrap.sh                          # prompts for the token, sandbox
#   POLAR_ACCESS_TOKEN=polar_oat_... ops/polar/bootstrap.sh > /tmp/polar.env
#   ops/polar/bootstrap.sh --no-webhook             # catalog only
#   POLAR_ENVIRONMENT=production ops/polar/bootstrap.sh --production
#
# It is `repose-admin billing polar-bootstrap`, run from this checkout with
# `go run` when no repose-admin is on PATH. Progress goes to stderr and the
# block alone to stdout. Rerunning is safe: the meter, the three products,
# the discount and the webhook endpoint are each found (by metadata.repose
# and by endpoint URL) before anything is created, and a second run
# creates nothing. The token is taken from the environment or a silent
# prompt, never from the command line, where it would land in shell
# history and `ps`. POLAR_ENVIRONMENT defaults to sandbox here.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"

if [[ -z "${POLAR_ACCESS_TOKEN:-}" ]]; then
	if [[ -t 0 ]]; then
		read -r -s -p "Polar organization access token (polar_oat_...): " POLAR_ACCESS_TOKEN
		echo >&2
	else
		echo "POLAR_ACCESS_TOKEN is not set and there is no terminal to ask on" >&2
		exit 2
	fi
fi
export POLAR_ACCESS_TOKEN
export POLAR_ENVIRONMENT="${POLAR_ENVIRONMENT:-sandbox}"

if command -v repose-admin >/dev/null 2>&1; then
	exec repose-admin billing polar-bootstrap "$@"
fi
cd "$root"
exec go run ./cmd/repose-admin billing polar-bootstrap "$@"
