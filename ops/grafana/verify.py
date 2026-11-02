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
  * that the datasource the rules query ANSWERS, with Grafana's own
    connection test -- the one check that catches a datasource pointing
    somewhere dead, which rule health cannot (see datasource_answers)
  * that the one rule allowed to shout about a failed query still is

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
import time
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
# ops/dashboards/push.py already solves "which of the target's datasources is
# the Prometheus one", for panels. The rules have exactly the same problem, so
# its `pick` and its uid-to-type table are reused rather than written twice.
DASH = _load("dash_push", ROOT / "ops" / "dashboards" / "push.py")

# The datasource uids the generated rules are written against -- Prometheus
# for all but one, Loki for the log rules of ops/alerts-loki.yaml (I-980).
# They are not uids any real Grafana has: the target's own uids are the
# target's, so the pusher rewrites these to whatever the target calls its
# Prometheus and its Loki, and this compares the same way. Written form in the
# file, resolved form on the wire. Read from the rules rather than listed, so
# a rule pointed at a datasource nothing resolves cannot slip past.
def rule_datasource_uids() -> list[str]:
    out = []
    for group in GEN.groups():
        for rule in group["rules"]:
            for node in rule["data"]:
                uid = node["datasourceUid"]
                if uid != "__expr__" and uid not in out:
                    out.append(uid)
    return out

# Which `--flag NAME` names each written uid, for a target with more than one
# datasource of that type. The same flag names ops/dashboards/push.py takes,
# because it is the same question.
NAME_KEY = {
    "repose-prometheus": "prometheus",
    "repose-loki": "loki",
    "repose-postgres": "postgres",
}

# The folder the alert rules live in. The uid is this repository's, set over
# the API, which is what keeps a second folder of the same title impossible
# (I-942, I-944). ops/grafana/push.py reads this and the next from here, so
# the push and the drift check cannot disagree about either.
FOLDER_UID = "repose-alerts"

# NOTHING here creates a datasource. Only the target knows its own addresses:
# the owner's Grafana reaches its siblings on a Docker network
# (`http://prometheus-<coolify id>:9090`), and inside that container
# `127.0.0.1:9090` -- which is what this repository's reference file says --
# is Grafana itself. A datasource created from that file would reach nothing,
# every rule that named it would fail its query, and with
# `execErrState: OK` all 56 would read OK: fifty-six green, silent alerts,
# the exact failure this tooling exists to prevent (DECISIONS I-944).
#
# So the datasources are the target's, found by type, and the uids in the
# generated rules are rewritten to them on the way in. A target with no
# datasource of a type we need is an error naming what to create, never a
# datasource invented with a guessed URL.
#
# `repose-postgres` is additionally not needed by any rule -- 55 query
# Prometheus and KernelOops queries Loki (I-980) -- and 21 of the 83 dashboard
# panels read it. Those say "no data"
# unless the owner has a Postgres datasource of their own; reaching the
# control plane's database needs a password, and this tooling takes none.
# `ops/sql/launch.sql` gets the same figures through psql with no datasource.
SKIP_DATASOURCES = {
    "repose-postgres": "reaching the control plane's database needs a "
                       "password, and this tooling does not take one",
}


class Unresolved(Exception):
    """The target has no datasource of a type the rules or panels need, or
    has several and has not been told which."""


def resolve(datasources: list, written_uid: str, want_name: str | None) -> dict:
    """The target's datasource for one of the uids this repository writes."""
    kind = DASH.DEV_UIDS[written_uid]
    try:
        found = DASH.pick(datasources, kind, want_name)
    except SystemExit as exc:
        # pick() names the flag to pass; ops/grafana/push.py takes the same
        # flag names as ops/dashboards/push.py, so its message is right here.
        raise Unresolved(str(exc)) from None
    if found is None:
        raise Unresolved(
            f"the target has no {kind} datasource, so the rules have nothing to query. "
            f"Add one in Grafana (Connections -> Add new connection) pointing at the "
            f"Prometheus that scrapes repose, then run this again. Nothing here creates "
            f"it: only the target knows its own address."
        )
    return found


def rewrite_uids(rules: list, mapping: dict) -> list:
    """Every `datasourceUid` in a rule's pipeline, mapped to the target's.

    `__expr__` is Grafana's own server-side expression engine and is the same
    uid everywhere, so reduce and threshold nodes are left alone.
    """
    out = []
    for rule in rules:
        copy = dict(rule, data=[])
        for node in rule["data"]:
            node = json.loads(json.dumps(node))
            uid = mapping.get(node.get("datasourceUid"))
            if uid:
                node["datasourceUid"] = uid
                if isinstance(node.get("model", {}).get("datasource"), dict):
                    node["model"]["datasource"]["uid"] = uid
            copy["data"].append(node)
        out.append(copy)
    return out

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


def get(url: str, token: str | None, path: str, method: str = "GET"):
    req = urllib.request.Request(url.rstrip("/") + path, method=method, headers={
        "Accept": "application/json", "User-Agent": "repose-grafana-verify/1"})
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=30) as resp:
        raw = resp.read()
        return json.loads(raw) if raw else {}


def datasource_answers(url: str, token: str | None, uid: str) -> str | None:
    """Why the target's datasource cannot be queried, or None if it can.

    Grafana's own connection test, which is the only check that catches the
    failure mode this file was rewritten for: a datasource whose address
    answers nothing. Every rule but one is `execErrState: OK`, and measured
    on Grafana 12.4 that means a rule whose query fails keeps health "ok" and
    state Normal -- 55 rules green and silent while every evaluation returned
    "connection refused". Rule health cannot see that. This can.
    """
    try:
        answer = get(url, token, f"/api/datasources/uid/{uid}/health", method="POST")
    except urllib.error.HTTPError as exc:
        body = exc.read()[:200].decode(errors="replace")
        return f"HTTP {exc.code}: {body}"
    except urllib.error.URLError as exc:
        return str(exc)
    if str(answer.get("status", "")).upper() == "OK":
        return None
    return str(answer.get("message", answer))[:300]


def uid_of(name: str) -> str:
    return "repose-" + re.sub(r"[^a-z0-9]+", "-", name.lower())


def source_alerts() -> list[str]:
    """Every alert of both source files (ops/alerts-loki.yaml is I-980)."""
    out = []
    for name in ("alerts.yaml", "alerts-loki.yaml"):
        text = (ROOT / "ops" / name).read_text()
        out += [m.group(1) for m in re.finditer(r"^\s+- alert: (\w+)$", text, re.M)]
    return out


def run(url: str, token: str | None, names: dict | None = None, wait: int = 0) -> int:
    bad = []
    names = names or {}
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

    # --- which of the target's datasources the rules are meant to query
    #
    # Not "is `repose-prometheus` there": it is not, and should not be. The
    # target's Prometheus has the target's uid, the pusher rewrites the rules
    # to it, and this has to resolve it the same way or every rule would read
    # as drifted.
    mapping = {}
    for written in rule_datasource_uids():
        try:
            found = resolve(datasources, written, names.get(NAME_KEY[written]))
            mapping[written] = found["uid"]
            print(f"  {written} -> {found['name']} ({found['uid']}) on the target")
        except Unresolved as exc:
            print(f"  {exc}", file=sys.stderr)
            return 1
        why = datasource_answers(url, token, found["uid"])
        if why:
            bad.append(
                f"datasource {found['name']} ({found['uid']}) at {found.get('url', '?')} "
                f"cannot be queried, so every rule that names it is dead: {why}. "
                "They will not say so: all but one are execErrState: OK, which keeps a "
                "failed evaluation reading OK."
            )
        else:
            print(f"  it answers queries ({found.get('url', '?')})")

    # --- a rule per alert, as the generator writes it, pointed where the
    #     pusher points it
    want = {}
    for group in GEN.groups():
        for rule in rewrite_uids(group["rules"], mapping):
            want[rule["uid"]] = dict(rule, folderUID=FOLDER_UID, ruleGroup=group["name"])
    have = {r["uid"]: r for r in rules}
    names = source_alerts()
    skipped = ", ".join(sorted(GEN.EXCLUDED))
    print(f"  {len(names)} alerts in ops/alerts.yaml and ops/alerts-loki.yaml, "
          f"{len(want)} generated for Grafana "
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
                       "ops/alerts.yaml or ops/alerts-loki.yaml")

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

    # --- and that the engine can actually evaluate them
    #
    # This is the check that matters, and asserting acceptance instead of
    # health is what let a push of 53 rules at a datasource that answered
    # nothing look like a success. A rule whose query fails goes to health
    # "error", and because every rule here is execErrState: OK its STATE
    # stays OK -- so the state says nothing and only the health does.
    #
    # A rule that has not been evaluated yet has no health, so waiting is
    # part of the check: without it this passes on a rule that is about to
    # fail. Running out of time to wait is a failure, not a pass.
    bad += evaluation(url, token, sorted(r["title"] for r in want.values()), wait)

    # The one rule that is allowed to shout when its own query fails. With
    # every rule on execErrState: OK there would be nothing in Grafana at all
    # to notice a dead datasource, which is why the generator keeps exactly
    # one on Error -- and why this fails if that ever stops being true.
    for name in GEN.EXEC_ERR_ALERTING:
        rule = have.get(uid_of(name))
        if rule and rule.get("execErrState") != "Error":
            bad.append(f"{name}: execErrState is {rule.get('execErrState')!r}, not "
                       "'Error'. Nothing would then notify when Grafana cannot query "
                       "Prometheus at all.")

    if bad:
        print("\n".join("  " + line for line in bad), file=sys.stderr)
        return 1
    return 0


def evaluation(url: str, token: str | None, titles: list, wait: int) -> list:
    unknown = {"", "unknown", "nodata", None}
    deadline = time.time() + max(0, wait)
    waited = False
    while True:
        try:
            groups = get(url, token, "/api/prometheus/grafana/api/v1/rules")["data"]["groups"]
        except (urllib.error.URLError, urllib.error.HTTPError, KeyError):
            return ["the scheduler's rule list cannot be read, so no rule's health is known"]
        seen = {r["name"]: r for g in groups for r in g["rules"]}
        pending = [t for t in titles
                   if t not in seen or seen[t].get("health") in unknown]
        if not pending or time.time() >= deadline:
            break
        if not waited:
            print(f"  waiting up to {wait}s for the first evaluation of "
                  f"{len(pending)} rules")
            waited = True
        time.sleep(5)
    bad = []
    for title in titles:
        rule = seen.get(title)
        if rule is None:
            bad.append(f"{title}: the scheduler does not have it")
        elif rule.get("health") == "error":
            bad.append(f"{title}: the engine cannot evaluate it, so it will never fire "
                       f"on what it was written for: {rule.get('lastError', '')[:200]}")
        elif rule.get("health") in unknown:
            bad.append(f"{title}: not evaluated yet, so its health is unknown. Re-run, "
                       "or pass a longer --wait")
    healthy = sum(1 for t in titles
                  if seen.get(t, {}).get("health") not in unknown | {"error"})
    print(f"  {healthy} of {len(titles)} rules evaluated healthy in the scheduler")
    return bad


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--url", default=os.environ.get("GRAFANA_URL", "http://127.0.0.1:3000"))
    ap.add_argument("--prometheus", metavar="NAME",
                    help="which of the target's Prometheus datasources the rules query, "
                         "when it has more than one")
    ap.add_argument("--loki", metavar="NAME")
    ap.add_argument("--wait", type=int, default=0, metavar="SECONDS",
                    help="wait this long for rules that have not been evaluated yet, "
                         "rather than reporting their health as unknown")
    args = ap.parse_args()
    return run(args.url, os.environ.get("GRAFANA_TOKEN"),
               {"prometheus": args.prometheus, "loki": args.loki}, args.wait)


if __name__ == "__main__":
    sys.exit(main())
