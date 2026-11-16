#!/usr/bin/env bash
# Counts what a user would see while something is redeployed.
#
#   ops/deploy-probe.sh https://repose.run/healthz --seconds 600
#
# 08-dashboard.md §9 and 05-control-plane-api.md ask for the same evidence in
# the same words: "deploy twice while curl loops against the domain, zero
# non-200 responses". This is that loop, with the counting done for you, so
# the evidence is a summary somebody can paste rather than 3000 lines of
# curl output that nobody reads to the end.
#
# It writes one line per request to the log file and prints a summary on
# exit (also on Ctrl-C, which is how you stop it when the deploys are done).
# A non-200, a timeout and a connection refusal are all failures and all
# keep their timestamp, because "which second did it break in" is the only
# question worth asking afterwards.
set -uo pipefail

usage() {
	cat >&2 <<'EOF'
usage: ops/deploy-probe.sh <url> [--seconds N] [--interval S] [--timeout S] [--log PATH]

  --seconds   stop after this long (default 900; Ctrl-C stops sooner)
  --interval  seconds between requests (default 0.2, so five a second)
  --timeout   per-request timeout in seconds (default 5)
  --log       where the per-request lines go (default a temp file, printed)
EOF
	exit 2
}

[ $# -ge 1 ] || usage
URL=$1
shift
SECONDS_TOTAL=900
INTERVAL=0.2
TIMEOUT=5
LOG=""

while [ $# -gt 0 ]; do
	case $1 in
	--seconds) SECONDS_TOTAL=$2 ;;
	--interval) INTERVAL=$2 ;;
	--timeout) TIMEOUT=$2 ;;
	--log) LOG=$2 ;;
	*) usage ;;
	esac
	shift 2
done

[ -n "$LOG" ] || LOG=$(mktemp -t deploy-probe-XXXXXX.log)

total=0
ok=0
bad=0
worst=0

summary() {
	echo
	echo "probe of $URL"
	echo "  requests   : $total over $((SECONDS - start_s))s (one every ${INTERVAL}s, ${TIMEOUT}s timeout)"
	echo "  200        : $ok"
	echo "  not 200    : $bad"
	echo "  slowest    : ${worst}s"
	if [ "$bad" -gt 0 ]; then
		echo "  failures:"
		grep -v ' 200 ' "$LOG" | sed 's/^/    /'
	fi
	echo "  log        : $LOG"
	# Non-zero when anything failed, so a caller can branch on it.
	[ "$bad" -eq 0 ]
	exit $?
}
trap summary INT TERM

start_s=$SECONDS
while [ $((SECONDS - start_s)) -lt "$SECONDS_TOTAL" ]; do
	# %{http_code} is 000 when the connection never produced a response,
	# which is the case the rolling deploy is supposed to make impossible.
	read -r code secs < <(curl -sS -o /dev/null --max-time "$TIMEOUT" \
		-w '%{http_code} %{time_total}\n' "$URL" 2>/dev/null || echo "000 0")
	ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	echo "$ts $code ${secs}s" >>"$LOG"
	total=$((total + 1))
	if [ "$code" = "200" ]; then
		ok=$((ok + 1))
	else
		bad=$((bad + 1))
		echo "$ts $code ${secs}s" >&2
	fi
	# Keep the slowest as a plain string compare on a fixed-width number.
	if [ "$(printf '%s\n%s\n' "$secs" "$worst" | sort -g | tail -1)" = "$secs" ]; then
		worst=$secs
	fi
	sleep "$INTERVAL"
done

summary
