#!/usr/bin/env python3
"""Ask a running Grafana what it made of the provisioned alerting files.

`ops/check.sh --grafana` runs this against the dev stack
(`ops/dev/docker-compose.yml`). What it catches is the failure the whole
generator exists to prevent: a rule Grafana accepts the file for and then
silently drops, or keeps under a different name, so the alert is in git and
not in the alerting engine.

    python3 ops/grafana/verify.py [--url http://127.0.0.1:3000]

It reads `ops/alerts.yaml` for the list of alerts that ought to be there and
then uses Grafana's own provisioning API:

    GET /api/v1/provisioning/alert-rules
    GET /api/v1/provisioning/contact-points
    GET /api/v1/provisioning/policies

Nothing here needs a token against the dev stack, which is anonymous admin.
Against a real Grafana, put a service-account token in GRAFANA_TOKEN, the
same variable `ops/dashboards/push.py` uses.
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import re
import sys
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent.parent.parent

# Where each kind of alert has to land, and the matching labels that take it
# there. The same seven cases ops/check.sh used to put through `amtool config
# routes test` against the Alertmanager route, so a page that waits 12 hours
# or a Watchdog that emails is a failure here as it was there.
ROUTES = [
    ("repose-watchdog", {"stack": "repose", "alertname": "Watchdog", "severity": "heartbeat"}),
    ("repose-email-page", {"stack": "repose", "alertname": "HostdDown", "severity": "page",
                           "host_id": "host-01"}),
    ("repose-email-page", {"stack": "repose", "alertname": "ApiDown", "severity": "page",
                           "job": "api-grpc"}),
    ("repose-email-page", {"stack": "repose", "alertname": "MonitoringPathDown",
                           "severity": "page"}),
    ("repose-email", {"stack": "repose", "alertname": "HostServiceDown", "severity": "warn",
                      "host_id": "host-01"}),
    ("repose-email", {"stack": "repose", "alertname": "GuestWakeFailed", "severity": "warn",
                      "host_id": "host-01", "reason": "resume"}),
    ("repose-email", {"stack": "repose", "alertname": "BillingStopped", "severity": "info"}),
]

# The folder title ops/grafana/alerts-gen.py puts every group in.
FOLDER_TITLE = "repose alerts"

CONTACT_POINTS = {
    "repose-email": "email",
    "repose-email-page": "email",
    "repose-watchdog": "webhook",
}


def get(url: str, path: str):
    req = urllib.request.Request(url + path)
    token = os.environ.get("GRAFANA_TOKEN")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(req, timeout=20) as resp:
        return json.loads(resp.read())


def source_alerts() -> list[str]:
    text = (ROOT / "ops" / "alerts.yaml").read_text()
    return [m.group(1) for m in re.finditer(r"^\s+- alert: (\w+)$", text, re.M)]


def matches(matchers, labels) -> bool:
    for name, op, value in matchers or []:
        have = labels.get(name, "")
        if op == "=" and have != value:
            return False
        if op == "!=" and have == value:
            return False
        if op == "=~" and not re.fullmatch(value, have):
            return False
        if op == "!~" and re.fullmatch(value, have):
            return False
    return True


def route(node, labels) -> str:
    """The receiver Alertmanager's first-match-wins walk ends at."""
    for child in node.get("routes") or []:
        if matches(child.get("object_matchers"), labels):
            found = route(child, labels)
            if not child.get("continue"):
                return found
    return node["receiver"]


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--url", default="http://127.0.0.1:3000")
    args = ap.parse_args()
    url = args.url.rstrip("/")
    bad = []

    try:
        rules = get(url, "/api/v1/provisioning/alert-rules")
        points = get(url, "/api/v1/provisioning/contact-points")
        policy = get(url, "/api/v1/provisioning/policies")
    except (urllib.error.URLError, urllib.error.HTTPError) as exc:
        print(f"cannot read Grafana at {url}: {exc}", file=sys.stderr)
        return 1

    want = source_alerts()
    titles = {r["title"]: r for r in rules}
    # One folder for all of them, and only one folder by that name. Grafana
    # provisions alerting before dashboards and matches a rule group's folder
    # by title, so a title shared with the dashboards' folder makes a second
    # folder of the same name on a Grafana with no state (I-942).
    folders = get(url, "/api/folders")
    named = [f for f in folders if f["title"] == FOLDER_TITLE]
    if len(named) != 1:
        bad.append(f"{len(named)} folders titled {FOLDER_TITLE!r}, expected one")
    folder_uid = named[0]["uid"] if named else None
    print(f"  {len(want)} alerts in ops/alerts.yaml, {len(rules)} rules in Grafana")
    for name in want:
        if name not in titles:
            bad.append(f"{name}: no rule in Grafana")
    for title in sorted(set(titles) - set(want)):
        bad.append(f"{title}: a rule Grafana has and ops/alerts.yaml does not")
    if len(rules) != len(titles):
        bad.append(f"{len(rules)} rules but {len(titles)} titles: two rules share a name")

    for name, rule in sorted(titles.items()):
        uid = "repose-" + re.sub(r"[^a-z0-9]+", "-", name.lower())
        if rule["uid"] != uid:
            bad.append(f"{name}: uid is {rule['uid']}, not {uid}")
        if rule["folderUID"] != folder_uid:
            bad.append(f"{name}: folder {rule['folderUID']}, not the one the rest are in")
        if rule["labels"].get("stack") != "repose":
            bad.append(f"{name}: no stack=repose label, so the policy cannot route it")
        if not rule["labels"].get("severity"):
            bad.append(f"{name}: no severity label")
        for key in ("summary", "description", "runbook"):
            if not rule["annotations"].get(key):
                bad.append(f"{name}: no {key} annotation")
        if "$value" in json.dumps(rule["annotations"]).replace("$values", ""):
            bad.append(f"{name}: an annotation still uses Prometheus's $value")
        refs = [node["refId"] for node in rule["data"]]
        if refs != ["A", "B", "C"] or rule["condition"] != "C":
            bad.append(f"{name}: data is {refs} with condition {rule['condition']}, not A/B/C")

    names = {p["name"]: p["type"] for p in points}
    for name, kind in CONTACT_POINTS.items():
        if names.get(name) != kind:
            bad.append(f"contact point {name}: {names.get(name)!r}, not a {kind} one")
    print(f"  contact points: {', '.join(f'{n} ({t})' for n, t in sorted(names.items()))}")

    for want_receiver, labels in ROUTES:
        got = route(policy, labels)
        if got != want_receiver:
            bad.append(f"{labels} routes to {got}, not {want_receiver}")
    # The one that matters most: a heartbeat every 30 seconds in the inbox
    # buries every alert that is not one.
    if route(policy, {"stack": "repose", "alertname": "Watchdog",
                      "severity": "heartbeat"}) != "repose-watchdog":
        bad.append("Watchdog does not go to the webhook")
    print(f"  {len(ROUTES)} routes land where they should")

    if bad:
        print("\n".join("  " + line for line in bad), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
