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
#   alerts-gen.py --check  committed Grafana rules that no longer match
#                          ops/alerts.yaml or ops/alerts-loki.yaml, which are
#                          the one source of both (DECISIONS I-942, I-980)
#                          as written. Nothing runs it: the personal server's
#                          alerting is Grafana's own, and the live routing
#                          check is verify.py under --grafana
#   gen.py --check         committed JSON that no longer matches the generator
#   grafana                a provisioning file, a dashboard or an alert rule
#                          Grafana refuses; an alert that lands on the wrong
#                          contact point (a page that waits 12 h, a Watchdog
#                          that emails every 30 s); and, since the alerting
#                          goes in over the API rather than a mount
#                          (DECISIONS I-944), that a push followed by a
#                          second push changes nothing
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
# Both sources: the log rules of ops/alerts-loki.yaml carry a runbook
# annotation like any other and promtool never reads them (I-980).
rules = open("ops/alerts.yaml").read() + open("ops/alerts-loki.yaml").read()
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
for name in $(grep -hoE "^[[:space:]]+- alert: [A-Za-z0-9]+" "$ops/alerts.yaml" "$ops/alerts-loki.yaml" | awk '{print $3}'); do
  grep -qi "^## $name" docs/ops/RUNBOOK.md || missing="$missing $name"
done
if [ -n "$missing" ]; then
  echo "no RUNBOOK heading for:$missing" >&2
  fail=1
else
  echo "all alerts have one"
fi

say "every alert-shaped runbook heading is a real alert"
# The reverse of the check above, and the one that was missing. A heading
# written as a bare CamelCase word reads as an alert name, so an operator
# or an agent who finds it believes an alert exists for that failure. On
# 2026-11-10 "## HostWgDown" had been in the runbook for months, said
# "alert `host_wg_down`", and no such rule existed: the section for the
# failure in front of the diagnosing agent looked like it was for some
# other, alerted condition, and 42 minutes went elsewhere (I-1074).
#
# Sections deliberately named after something other than a Prometheus
# alert go in this list with the reason. Keep it honest: it is the only
# thing between a new phantom alert and a reader who trusts the name.
allowed_non_alerts="StoreHigh"
# StoreHigh: a hostd host_warning kind (`kind="store_high"`), not a
# Prometheus rule. The Prometheus alert for the root filesystem is
# StoreFull, at 85 percent, and that section names it.
phantom=""
for name in $(grep -oE "^## [A-Z][A-Za-z0-9]+$" docs/ops/RUNBOOK.md | awk '{print $2}'); do
  case " $allowed_non_alerts " in *" $name "*) continue ;; esac
  grep -qE "^[[:space:]]+- alert: $name$" "$ops/alerts.yaml" "$ops/alerts-loki.yaml" ||
    phantom="$phantom $name"
done
if [ -n "$phantom" ]; then
  echo "runbook headings that read as alert names with no such alert:$phantom" >&2
  echo "add the alert, rename the heading to a symptom, or name it in allowed_non_alerts with the reason" >&2
  fail=1
else
  echo "no phantom alerts"
fi

say "grafana alert rules are what the generator produces"
python3 "$ops/grafana/alerts-gen.py" --check || fail=1

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
  say "the alerting pushes in over the API, as it does on the server"
  # The alerting is not mounted (DECISIONS I-944), so this is the real path:
  # ops/grafana/push.py against a running Grafana, then the drift check.
  # GRAFANA_URL is the whole of what it needs here: dev's Grafana is
  # anonymous admin, so no token, and the push takes no other value at all
  # (DECISIONS I-944). Dev's datasources come from a file of its own (the dev
  # URLs differ), so they are reported as left alone rather than written.
  export GRAFANA_URL=http://127.0.0.1:3000
  unset GRAFANA_TOKEN
  python3 "$ops/grafana/push.py" --skip-dashboards 2>&1 | sed 's/^/  /' || fail=1

  say "a second push changes nothing"
  # Idempotence, said by the thing itself: --dry-run reports what it would
  # change, and after a push that has to be nothing.
  out=$(python3 "$ops/grafana/push.py" --skip-dashboards --dry-run 2>&1) || fail=1
  printf '%s\n' "$out" | sed 's/^/  /'
  if printf '%s' "$out" | grep -qE ': (created|deleted)|[1-9][0-9]* (new|changed)'; then
    echo "a second push would still change something" >&2
    fail=1
  fi

  say "a log rule fires on a pushed line"
  # The one thing promtool cannot do (DECISIONS I-980): ops/alerts-loki.yaml
  # is LogQL, so there is no `promtool test rules` case for it and its
  # firing is otherwise unproven -- which is the failure this very rule was
  # written about, a condition that reads as a quiet system. So: push one
  # real kernel-oops line into the dev Loki with the labels
  # nix/hosts/fluent-bit.nix gives them, ask Grafana's own Loki datasource
  # the rule's query, and then wait for the rule's own state to turn.
  python3 - <<'LOGRULE' || fail=1
import json, sys, time, urllib.error, urllib.request

GRAFANA = "http://127.0.0.1:3000"
LOKI = "http://127.0.0.1:3100"
# The line host-02 wrote at 13:51:07 on 2026-11-02, labelled the way a host
# ships it: `host` is the host_id, `component` is `kernel` for a journal
# record with no unit.
HOST_ID = "01a1758b-961b-7d57-a378-f0f38a97c139"
LINE = "BUG: unable to handle page fault for address: 0000790c8a01b51c"


def post(url, payload, headers=None):
    req = urllib.request.Request(
        url, data=json.dumps(payload).encode(), method="POST",
        headers={"Content-Type": "application/json", **(headers or {})})
    with urllib.request.urlopen(req, timeout=30) as r:
        body = r.read().decode()
    return json.loads(body) if body.strip() else {}


def get(url):
    with urllib.request.urlopen(url, timeout=30) as r:
        return json.loads(r.read().decode())


ds = next((d for d in get(f"{GRAFANA}/api/datasources") if d["type"] == "loki"), None)
if ds is None:
    print("dev Grafana has no Loki datasource", file=sys.stderr)
    sys.exit(1)

post(f"{LOKI}/loki/api/v1/push", {"streams": [{
    "stream": {"component": "kernel", "host": HOST_ID, "service_name": "kernel"},
    "values": [[str(time.time_ns()), LINE]]}]})

# The rule's own A query, read out of the generator rather than retyped.
sys.path.insert(0, "ops/grafana")
import importlib.util
spec = importlib.util.spec_from_file_location("gen", "ops/grafana/alerts-gen.py")
gen = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gen)
rules = {r["title"]: r for g in gen.groups() for r in g["rules"]}
log_rules = [r["alert"] for g in gen.read_source(gen.SOURCE_LOKI) for r in g["rules"]]
if not log_rules:
    print("no rules in ops/alerts-loki.yaml to check", file=sys.stderr)
    sys.exit(1)
name = log_rules[0]
query = rules[name]["data"][0]["model"]["expr"]

# Through Grafana, not straight to Loki: this is the datasource the rule
# uses, and a datasource that answers nothing is what leaves a rule green.
answer = post(f"{GRAFANA}/api/ds/query", {"queries": [{
    "refId": "A", "datasource": {"type": "loki", "uid": ds["uid"]},
    "expr": query, "queryType": "instant", "maxDataPoints": 43200}],
    "from": "now-10m", "to": "now"})
frames = answer.get("results", {}).get("A", {}).get("frames", [])
values = [v for f in frames for col in f.get("data", {}).get("values", [])[1:] for v in col]
if not any(isinstance(v, (int, float)) and v > 0 for v in values):
    print(f"{name}: the query returned nothing for a pushed oops line: {query}", file=sys.stderr)
    print(json.dumps(answer)[:800], file=sys.stderr)
    sys.exit(1)
print(f"  {name}: the query counts the pushed line")

# And the rule itself. The group's interval is a minute, so this is two
# evaluations plus slack; a rule that never turns is the whole point.
deadline = time.time() + 150
state = None
while time.time() < deadline:
    groups = get(f"{GRAFANA}/api/prometheus/grafana/api/v1/rules")["data"]["groups"]
    for g in groups:
        for r in g.get("rules", []):
            if r.get("name") == name:
                state = r.get("state")
    if state in ("firing", "pending"):
        break
    time.sleep(5)
if state != "firing":
    print(f"{name}: state is {state!r} after a pushed oops line, not firing", file=sys.stderr)
    sys.exit(1)
print(f"  {name}: firing")
LOGRULE

  say "Grafana holds what the generator produces"
  # The count against ops/alerts.yaml, rule by rule, not just "no error": a
  # rule that was pushed once and then edited away in the UI is the drift
  # nothing else catches now.
  python3 "$ops/grafana/verify.py" || fail=1

  say "Grafana logged no provisioning or template error"
  # The dev stack has nowhere to deliver: no GF_SMTP_* and no contact point
  # of its own, so a firing alert reaching Grafana's default email receiver
  # fails. That failure is the alerting path working, and is the only error
  # allowed through. An annotation that will not expand is NOT one of them:
  # it reaches the reader as its own template source, and is how
  # `{{ $values.B.Value | ... }}` without its `if` guard was found (I-942).
  if docker compose -f "$ops/dev/docker-compose.yml" logs grafana 2>&1 \
     | grep -iE 'level=error|provisioning.*error|alerting.*(failed|invalid)' \
     | grep -vE 'SMTP not configured'; then
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
