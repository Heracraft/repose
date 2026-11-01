#!/usr/bin/env python3
"""Does a Grafana still hold what this repository says it should?

    python3 ops/grafana/verify.py [--url http://127.0.0.1:3000]
    GRAFANA_URL=... GRAFANA_TOKEN=glsa_... python3 ops/grafana/verify.py

Exit 0 when the target matches, non-zero with the differences listed.

This is the drift check, and since DECISIONS I-944 it is the only one there
is. Nothing is mounted into the owner's Grafana, so nothing reconciles the
alert rules at boot: what the target holds is whatever the last
`ops/grafana/push.py` put there, minus anything a person has changed in the
UI since, minus everything if its database was ever replaced. "No alert
rules" looks exactly like "nothing wrong", which is why this exists and why
it compares rule by rule rather than counting.

What it compares, all of it against `ops/grafana/alerts-gen.py` and the
datasource file, never against a copy kept here:

  * one folder with the rules' title, under the one uid, and no second folder
    carrying that title (the I-942 failure, which cost two folders of one
    name on a Grafana with no state)
  * a rule for every alert the generator emits, under its generated uid, with
    the same title, condition, `for`, labels, annotations, noDataState,
    execErrState and query/reduce/threshold pipeline
  * each group's evaluation interval
  * no rule the repository does not have, and none reporting health "error"
  * the three datasources the rules and the panels name, by uid and type

What it does NOT look at is who gets told. No contact point and no
notification policy is pushed (DECISIONS I-944): Grafana's own default policy
delivers to the default contact point the owner set up in the UI, and that is
theirs to change. A check on it here would be this repository having an
opinion it decided not to have. It does not look for the `repose-postgres`
datasource either, for the reason in SKIP_DATASOURCES.
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import os
import pathlib
import re
import sys
import urllib.error
import urllib.request

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent.parent


def _load(name: str, path: pathlib.Path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


GEN = _load("alerts_gen", HERE / "alerts-gen.py")

# The folder the alert rules live in. The uid is this repository's, set over
# the API, which is what keeps a second folder of the same title impossible
# (I-942, I-944). ops/grafana/push.py reads this and the next from here, so
# the push and the drift check cannot disagree about either.
FOLDER_UID = "repose-alerts"

# Datasources this repository does NOT put into a Grafana, with the reason.
#
# `repose-postgres` is the control plane's own database, and reaching it needs
# a password. Nothing here carries one: the push takes GRAFANA_URL and
# GRAFANA_TOKEN and no third thing (DECISIONS I-944). No alert rule queries
# it -- all 53 query Prometheus -- so no alert is affected. 21 of the 83
# dashboard panels do, and they read "no data" until somebody adds that
# datasource by hand in Grafana, with this exact uid so the committed panels
# find it. `ops/sql/launch.sql` gets the same figures through psql with no
# datasource at all.
SKIP_DATASOURCES = {
    "repose-postgres": "reaching the control plane's database needs a "
                       "password, and this tooling does not take one",
}

_UNIT = {"s": 1, "m": 60, "h": 3600, "d": 86400, "w": 604800}


def seconds(duration) -> int:
    """A Prometheus/Grafana duration as seconds. `1m` and `60s` and 60 are one
    number, which is how a group interval is compared with what the API
    returns for it."""
    if isinstance(duration, (int, float)):
        return int(duration)
    total, text = 0, str(duration).strip()
    if not text:
        return 0
    for amount, unit in re.findall(r"(\d+)([smhdw])", text):
        total += int(amount) * _UNIT[unit]
    return total


# --- comparing ------------------------------------------------------------


def same(want, have) -> bool:
    """Is `have` what `want` asks for?

    A mapping is compared key by key and keys the target added are ignored:
    Grafana fills a rule's model in with `queryType`, `maxDataPoints` and
    others, and a check that demanded an exact match would fail on every
    Grafana version bump instead of on drift. Numbers compare numerically,
    because a threshold of 0.0 comes back as 0.
    """
    if isinstance(want, dict):
        if not isinstance(have, dict):
            return False
        return all(key in have and same(value, have[key]) for key, value in want.items())
    if isinstance(want, list):
        return (isinstance(have, list) and len(want) == len(have)
                and all(same(a, b) for a, b in zip(want, have)))
    if isinstance(want, bool) or isinstance(have, bool):
        return want is have
    if isinstance(want, (int, float)) and isinstance(have, (int, float)):
        return float(want) == float(have)
    return want == have


def rule_differences(have: dict, want: dict) -> list[str]:
    """The fields of one alert rule the target does not match."""
    out = []
    for key in ("title", "condition", "noDataState", "execErrState", "labels",
                "annotations", "folderUID", "ruleGroup"):
        if key in want and not same(want[key], have.get(key)):
            out.append(key)
    if "for" in want and seconds(want["for"]) != seconds(have.get("for", 0)):
        out.append("for")
    if bool(want.get("isPaused")) != bool(have.get("isPaused")):
        out.append("isPaused")
    mine = want.get("data") or []
    theirs = have.get("data") or []
    if len(mine) != len(theirs):
        out.append(f"data ({len(theirs)} nodes, not {len(mine)})")
    else:
        for node, got in zip(mine, theirs):
            if not same(node, got):
                out.append(f"data[{node['refId']}]")
    return out


# --- the target -----------------------------------------------------------


def get(url: str, token: str | None, path: str):
    req = urllib.request.Request(url.rstrip("/") + path, headers={
        "Accept": "application/json", "User-Agent": "repose-grafana-verify/1"})
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=30) as resp:
        raw = resp.read()
        return json.loads(raw) if raw else {}


def source_alerts() -> list[str]:
    text = (ROOT / "ops" / "alerts.yaml").read_text()
    return [m.group(1) for m in re.finditer(r"^\s+- alert: (\w+)$", text, re.M)]


def run(url: str, token: str | None) -> int:
    bad = []
    try:
        rules = get(url, token, "/api/v1/provisioning/alert-rules")
        folders = get(url, token, "/api/folders?limit=1000")
        datasources = get(url, token, "/api/datasources")
    except (urllib.error.URLError, urllib.error.HTTPError) as exc:
        print(f"  cannot read Grafana at {url}: {exc}", file=sys.stderr)
        return 1

    why = GEN.stale()
    if why:
        print(f"  {why}", file=sys.stderr)
        return 1

    # --- the folder, and only one of it
    named = [f for f in folders if f["title"] == GEN.FOLDER]
    if [f["uid"] for f in named] != [FOLDER_UID]:
        bad.append(f"folders titled {GEN.FOLDER!r}: {[f['uid'] for f in named]}, "
                   f"expected exactly [{FOLDER_UID!r}]")

    # --- the datasources the rules and panels name
    have_ds = {d["uid"]: d for d in datasources}
    for ds in GEN.read_yaml(HERE / "provisioning" / "datasources" / "repose.yaml")["datasources"]:
        if ds["uid"] in SKIP_DATASOURCES:
            continue
        got = have_ds.get(ds["uid"])
        if got is None:
            bad.append(f"datasource {ds['uid']}: not on the target")
        elif got["type"] != ds["type"]:
            bad.append(f"datasource {ds['uid']}: type {got['type']}, not {ds['type']}")

    # --- a rule per alert, as the generator writes it
    want = {}
    for group in GEN.groups():
        for rule in group["rules"]:
            want[rule["uid"]] = dict(rule, folderUID=FOLDER_UID, ruleGroup=group["name"])
    have = {r["uid"]: r for r in rules}
    names = source_alerts()
    skipped = ", ".join(sorted(GEN.EXCLUDED))
    print(f"  {len(names)} alerts in ops/alerts.yaml, {len(want)} generated for Grafana "
          f"({skipped} excluded), {len(rules)} on the target")
    for uid, rule in sorted(want.items()):
        got = have.get(uid)
        if got is None:
            bad.append(f"{rule['title']}: no rule {uid} on the target")
            continue
        drift = rule_differences(got, rule)
        if drift:
            bad.append(f"{rule['title']} ({uid}): {', '.join(drift)} differ from the generator")
    for uid in sorted(set(have) - set(want)):
        if have[uid].get("folderUID") == FOLDER_UID or uid.startswith("repose-"):
            bad.append(f"{uid} ({have[uid].get('title')}): on the target and not in "
                       "ops/alerts.yaml")

    # --- each group's evaluation interval
    for group in GEN.groups():
        try:
            got = get(url, token,
                      f"/api/v1/provisioning/folder/{FOLDER_UID}/rule-groups/{group['name']}")
        except (urllib.error.URLError, urllib.error.HTTPError) as exc:
            bad.append(f"group {group['name']}: cannot be read ({exc})")
            continue
        if seconds(got.get("interval", 0)) != seconds(group["interval"]):
            bad.append(f"group {group['name']}: interval {got.get('interval')}s, "
                       f"not {group['interval']}")

    # --- nothing the engine cannot evaluate
    try:
        groups = get(url, token, "/api/prometheus/grafana/api/v1/rules")["data"]["groups"]
    except (urllib.error.URLError, urllib.error.HTTPError, KeyError):
        groups = []
    unhealthy = [(r["name"], r.get("lastError", "")[:120])
                 for g in groups for r in g["rules"] if r.get("health") == "error"]
    for name, err in unhealthy:
        bad.append(f"{name}: the engine cannot evaluate it: {err}")
    if groups:
        evaluated = sum(len(g["rules"]) for g in groups)
        print(f"  {evaluated} rules in the scheduler, {len(unhealthy)} unhealthy")

    if bad:
        print("\n".join("  " + line for line in bad), file=sys.stderr)
        return 1
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--url", default=os.environ.get("GRAFANA_URL", "http://127.0.0.1:3000"))
    args = ap.parse_args()
    return run(args.url, os.environ.get("GRAFANA_TOKEN"))


if __name__ == "__main__":
    sys.exit(main())
