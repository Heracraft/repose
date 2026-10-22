#!/usr/bin/env bash
# Everything in ops/ that can be checked without a host, in one command.
# `just obs-check` runs it; CI runs the same thing.
#
#   ops/check.sh              rules, dashboards, generator freshness
#   ops/check.sh --grafana    also bring the dev stack up and load the
#                             dashboards into a real Grafana
#
# What each step catches:
#   promtool check rules   a rule that does not parse (a typo in a metric name
#                          is not caught here; the tests below are)
#   promtool test rules    a rule whose expression does not fire on the series
#                          it was written for, which is indistinguishable from
#                          a quiet system until the incident
#   dashboard exprs        a PromQL expression in a panel that does not parse,
#                          which renders as an empty panel with an error a
#                          human has to click to see
#   amtool                 a route that does not parse, or an alert that lands
#                          on the wrong receiver (a page that waits 12 h, a
#                          Watchdog that posts to Chat every minute)
#   gen.py --check         committed JSON that no longer matches the generator
#   grafana                a provisioning file or a dashboard Grafana refuses
set -euo pipefail

cd "$(dirname "$0")/.."
ops=ops
fail=0

say() { printf '\n== %s ==\n' "$1"; }

# promtool comes from nixpkgs#prometheus.cli; the dev shell has it.
promtool() {
  # type -P, not command -v: the latter reports this very function, so on a
  # machine without the binary the wrapper called itself and failed.
  if type -P promtool >/dev/null 2>&1; then
    command promtool "$@"
  else
    nix shell nixpkgs#prometheus.cli -c promtool "$@"
  fi
}

# amtool likewise, from nixpkgs#prometheus-alertmanager.
amtool() {
  if type -P amtool >/dev/null 2>&1; then
    command amtool "$@"
  else
    nix shell nixpkgs#prometheus-alertmanager -c amtool "$@"
  fi
}

say "alert rules parse"
promtool check rules "$ops/alerts.yaml" || fail=1

say "alert rule tests"
(cd "$ops" && promtool test rules alerts_test.yaml) || fail=1

say "every alert's runbook anchor resolves"
# A heading that exists is not the same as the anchor the rule points at:
# FluentBitStuck pointed at #observability, a heading this file has never
# had, so the one link an operator follows mid-incident went nowhere and
# the heading check below still passed (I-800).
python3 - <<'ANCHORS' || fail=1
import re, sys
rules = open("ops/alerts.yaml").read()
anchors = {re.sub(r"[^a-z0-9 -]", "", line[3:].strip().lower()).replace(" ", "-")
           for line in open("docs/ops/RUNBOOK.md").read().splitlines() if line.startswith("## ")}
bad, cur = [], None
for line in rules.splitlines():
    m = re.match(r"\s*- alert: (\w+)", line)
    if m:
        cur = m.group(1)
    m = re.search(r'runbook: "docs/ops/RUNBOOK\.md#([^"]+)"', line)
    if m and m.group(1) not in anchors:
        bad.append(f"{cur} -> #{m.group(1)}")
if bad:
    print("runbook anchor goes nowhere: " + ", ".join(bad), file=sys.stderr)
    sys.exit(1)
print("all anchors resolve")
ANCHORS

say "every alert has a runbook heading"
# docs/CHECKLIST.md: an alert with no runbook entry is a notification nobody
# knows what to do with. The heading is the alert name, lowercased by
# GitHub-style anchors but written out in full in the file.
missing=""
for name in $(grep -oE "^[[:space:]]+- alert: [A-Za-z0-9]+" "$ops/alerts.yaml" | awk '{print $3}'); do
  grep -qi "^## $name" docs/ops/RUNBOOK.md || missing="$missing $name"
done
if [ -n "$missing" ]; then
  echo "no RUNBOOK heading for:$missing" >&2
  fail=1
else
  echo "all alerts have one"
fi

say "alertmanager route"
# DECISIONS I-648, amended by I-848: Watchdog only to the Kuma push, pages
# to the page receiver, the rest to email on the slow timer.
route=$ops/alertmanager/repose-route.yaml
amtool check-config "$route" >/dev/null || fail=1
while read -r want labels; do
  # shellcheck disable=SC2086
  amtool config routes test --config.file="$route" --verify.receivers="$want" $labels >/dev/null \
    || { echo "$labels should route to $want" >&2; fail=1; }
done <<'ROUTES'
repose-watchdog stack=repose alertname=Watchdog severity=heartbeat
repose-email-page stack=repose alertname=HostdDown severity=page host_id=host-01
repose-email-page stack=repose alertname=ApiDown severity=page job=api-grpc
repose-email-page stack=repose alertname=MonitoringPathDown severity=page
repose-email stack=repose alertname=HostServiceDown severity=warn host_id=host-01
repose-email stack=repose alertname=GuestWakeFailed severity=warn host_id=host-01 reason=resume
repose-email stack=repose alertname=BillingStopped severity=info
ROUTES
echo "routes as intended"

say "dashboards are what the generator produces"
python3 "$ops/dashboards/gen.py" --check || fail=1

say "dashboard structure and queries"
python3 "$ops/dashboards/validate.py" || fail=1

say "dashboard PromQL parses"
# Every Prometheus expression in every panel, turned into a rules file so
# promtool's parser can judge it. Grafana's own variables ($host_id,
# $__range, $__interval) are substituted with values that parse.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
python3 "$ops/dashboards/validate.py" --emit-rules >"$tmp/dashboard-rules.yaml"
promtool check rules "$tmp/dashboard-rules.yaml" || fail=1

if [ "${1:-}" = "--grafana" ]; then
  say "dashboards load in Grafana"
  docker compose -f "$ops/dev/docker-compose.yml" up -d
  for i in $(seq 1 60); do
    if curl -sf http://127.0.0.1:3000/api/health >/dev/null; then break; fi
    sleep 2
  done
  curl -sf http://127.0.0.1:3000/api/health | python3 -m json.tool
  # Provisioning is asynchronous; the provider polls every 30 s but loads
  # once at start.
  sleep 5
  for uid in repose-overview repose-host-capacity repose-per-guest repose-builds repose-gateway \
             repose-snapshots repose-billing repose-abuse; do
    if out=$(curl -sf "http://127.0.0.1:3000/api/dashboards/uid/$uid"); then
      title=$(printf '%s' "$out" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dashboard"]["title"])')
      panels=$(printf '%s' "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["dashboard"]["panels"]))')
      echo "  $uid: $title ($panels panels)"
    else
      echo "  $uid: NOT LOADED" >&2
      fail=1
    fi
  done
  say "Grafana logged no provisioning error"
  if docker compose -f "$ops/dev/docker-compose.yml" logs grafana 2>&1 | grep -iE 'level=error|provisioning.*error'; then
    fail=1
  else
    echo "clean"
  fi
fi

if [ "$fail" -ne 0 ]; then
  printf '\nFAILED\n' >&2
  exit 1
fi
printf '\nok\n'
