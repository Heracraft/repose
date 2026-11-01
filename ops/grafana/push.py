#!/usr/bin/env python3
"""Put repose's observability config into a running Grafana.

One command, over Grafana's HTTP API: two datasources, the 53 alert rules and
the eight dashboards. Nothing is mounted into the container, and the only
things it needs are GRAFANA_URL and GRAFANA_TOKEN (DECISIONS I-944).

    GRAFANA_URL=https://grafana.example GRAFANA_TOKEN=glsa_... \\
        python3 ops/grafana/push.py [--dry-run]

    python3 ops/grafana/push.py --dry-run    # says what it would change

Idempotent, and meant to be re-run: every object has a uid fixed by this
repository, so a second run updates in place and the next says nothing
changed. `--dry-run` reads the target and prints the difference without
writing.

NO CONTACT POINT AND NO NOTIFICATION POLICY. This pushes rules. With no
`policies:` of ours, Grafana's own default policy sends every alert to
whatever default contact point that Grafana has, which the owner sets up in
the UI -- email, Google Chat, anything. That is why there is no SMTP setting
and no address anywhere in this repository, and why pushing cannot overwrite
the notification tree of the Grafana it is pushed into.

What it reads, and nothing else:

  ops/grafana/provisioning/datasources/repose.yaml   two of its three
  ops/grafana/provisioning/alerting/repose-rules.yaml      GENERATED
  ops/dashboards/*.json                                    GENERATED

Neither generated file is re-derived here. The rules come from
`ops/grafana/alerts-gen.py` itself, and this refuses to push at all if the
committed YAML is not what the generator produces -- pushing a definition
nobody has read is how a fixed alert goes live as the broken one. The
dashboards are pushed by `ops/dashboards/push.py`, which this calls; there is
one implementation of each.

NO SECRETS AT ALL. Two variables, GRAFANA_URL and GRAFANA_TOKEN, and nothing
else: this never asks for a password and never refuses for want of one. The
Prometheus and Loki datasources need no credentials, and the third one, the
control plane's Postgres, is not created here -- see SKIP_DATASOURCES in
verify.py for why and for what it costs. A datasource entry that still
carries a `$...` placeholder is refused rather than sent as its own source
text, because nothing expands them any more.

THE RISK THIS DESIGN CARRIES. A mounted provisioning directory makes a
rebuilt Grafana re-provision itself. Over the API it does not: a Grafana that
comes back with an empty database has no alert rules until a human runs this,
and no alert rules is silent. Grafana's own state is in Postgres
(GF_DATABASE_TYPE=postgres) so a container recreation keeps it, which is why
this is a tolerable trade; losing that database is not, and nothing watches
for it -- there is no heartbeat, because the only place to watch from was the
same server (DECISIONS I-944). `ops/grafana/verify.py` is what says, at any
time, whether the target still holds what this repository says it should.
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


# alerts-gen.py is not importable by name (the hyphen), and it is the one
# definition of a Grafana rule in this repository, so it is loaded rather than
# copied. verify.py holds the comparison both this and the drift check use.
GEN = _load("alerts_gen", HERE / "alerts-gen.py")
VERIFY = _load("verify", HERE / "verify.py")

DATASOURCES = HERE / "provisioning" / "datasources" / "repose.yaml"

# The folder the rules go in, with a uid of this repository's choosing. Over
# the API the uid is ours to set, which file provisioning did not allow: there
# the folder was matched by title and a Grafana with no state made a second
# one of the same name (I-942). Here there is one folder, found by uid, and a
# folder of that title under any other uid is refused rather than added to.
# It lives in verify.py so the drift check and the push cannot disagree.
FOLDER_UID = VERIFY.FOLDER_UID


# --- the API --------------------------------------------------------------


class ApiError(Exception):
    pass


class Grafana:
    def __init__(self, base: str, token: str | None, dry_run: bool):
        self.base = base.rstrip("/")
        self.token = token
        self.dry_run = dry_run
        self.writes = 0

    def call(self, method: str, path: str, body=None, allow=()):
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json",
            # Cloudflare (error 1010) refuses Python's default User-Agent.
            "User-Agent": "repose-grafana-push/1",
        }
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        req = urllib.request.Request(
            self.base + path,
            method=method,
            data=None if body is None else json.dumps(body).encode(),
            headers=headers,
        )
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                raw = resp.read()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as exc:
            if exc.code in allow:
                return None
            detail = exc.read()[:400].decode(errors="replace")
            raise ApiError(f"{method} {path}: HTTP {exc.code}: {detail}") from None
        except urllib.error.URLError as exc:
            raise ApiError(f"{method} {path}: {exc}") from None

    def write(self, method: str, path: str, body=None, allow=()):
        if self.dry_run:
            return None
        self.writes += 1
        return self.call(method, path, body, allow)


# --- placeholders, which are no longer expanded ---------------------------

PLACEHOLDER = re.compile(r"\$__env\{[A-Z_][A-Z0-9_]*\}|\$__file\{[^}]*\}|\$\{?[A-Z_][A-Z0-9_]*\}?")


def placeholders(node, found=None) -> list[str]:
    """Every `$__env{...}`, `$__file{...}` or `$VAR` left in a parsed object.

    Grafana expands those when it reads a provisioning file off disk. Over the
    API nothing does, and this pusher deliberately holds no values to expand
    them with, so one that reaches here would be sent as its own source text
    -- a datasource whose password is the literal string `$REPOSE_PG_PASSWORD`
    connects to nothing and says nothing about why. So it is refused instead.
    """
    found = [] if found is None else found
    if isinstance(node, dict):
        for value in node.values():
            placeholders(value, found)
    elif isinstance(node, list):
        for value in node:
            placeholders(value, found)
    elif isinstance(node, str):
        found += PLACEHOLDER.findall(node)
    return sorted(set(found))


# --- the three things it pushes -------------------------------------------


def push_datasources(gf: Grafana, doc: dict) -> list[str]:
    """Prometheus and Loki, of the three in the datasource file.

    By uid, so the dashboards' and the alert rules' `repose-prometheus` means
    this one. Each is created once and updated in place after that. The third,
    `repose-postgres`, is skipped: see SKIP_DATASOURCES in verify.py.
    """
    have = {d["uid"]: d for d in gf.call("GET", "/api/datasources")}
    lines = []
    for ds in doc["datasources"]:
        uid = ds["uid"]
        if uid in VERIFY.SKIP_DATASOURCES:
            # Said once, here, so nobody meets the empty panels without an
            # explanation: this is the designed state, not a failure.
            lines.append(
                f"datasource {uid}: not created, so the panels that read it say "
                "\"no data\" -- the per-project figures, which are in Postgres because "
                "project_id is not a metric label. By design (ops/README.md step 3)."
            )
            continue
        left = placeholders(ds)
        if left:
            raise ApiError(
                f"datasource {uid} still carries {', '.join(left)}. Placeholders are "
                "Grafana's file-provisioning syntax and nothing expands them over the "
                "API; this push takes GRAFANA_URL and GRAFANA_TOKEN and no values of "
                "its own (DECISIONS I-944)."
            )
        body = dict(ds, access=ds.get("access", "proxy"))
        if uid in have:
            if have[uid].get("readOnly"):
                # That Grafana provisions its datasources from a file of its
                # own, as ops/dev does. The API cannot write over one, and
                # does not need to: it is already there. Reported, not failed.
                lines.append(f"datasource {uid} ({ds['type']}): "
                             "file-provisioned on the target, left alone")
                continue
            body["id"] = have[uid]["id"]
            # Neither of these carries a credential, so everything about them
            # can be read back and compared, and one that already matches is
            # not rewritten. That is what lets --dry-run say truthfully that a
            # second push would change nothing.
            drift = sorted(k for k, v in body.items()
                           if k not in ("id", "version")
                           and not VERIFY.same(v, have[uid].get(k)))
            if not drift:
                lines.append(f"datasource {uid} ({ds['type']}): as written")
                continue
            gf.write("PUT", f"/api/datasources/uid/{uid}", body)
            lines.append(f"datasource {uid} ({ds['type']}): rewritten, the target's "
                         + ", ".join(drift) + " did not match")
        else:
            gf.write("POST", "/api/datasources", body)
            lines.append(f"datasource {uid} ({ds['type']}): created")
    return lines


def push_folder(gf: Grafana) -> list[str]:
    """One folder for the alert rules, by uid, and never a second by title."""
    title = GEN.FOLDER
    folders = gf.call("GET", "/api/folders?limit=1000")
    by_uid = {f["uid"]: f for f in folders}
    same_title = [f for f in folders if f["title"] == title and f["uid"] != FOLDER_UID]
    if same_title:
        raise ApiError(
            f"a folder titled {title!r} already exists under uid "
            f"{same_title[0]['uid']!r}, which is not {FOLDER_UID!r}. Two folders of "
            "one name is the state I-942 was written to avoid. Move the rules out of "
            "it and delete it, or rename it, then run this again."
        )
    if FOLDER_UID in by_uid:
        if by_uid[FOLDER_UID]["title"] != title:
            gf.write("PUT", f"/api/folders/{FOLDER_UID}",
                     {"title": title, "version": by_uid[FOLDER_UID].get("version", 1),
                      "overwrite": True})
            return [f"folder {FOLDER_UID}: renamed to {title!r}"]
        return [f"folder {FOLDER_UID} ({title!r}): already there"]
    gf.write("POST", "/api/folders", {"uid": FOLDER_UID, "title": title})
    return [f"folder {FOLDER_UID} ({title!r}): created"]


def push_rules(gf: Grafana) -> list[str]:
    """The 54 rules, one PUT per group.

    `PUT /api/v1/provisioning/folder/{uid}/rule-groups/{group}` replaces a
    whole group: it sets the group's evaluation interval and its rules in one
    request, and a rule that left ops/alerts.yaml leaves the group with it. A
    rule per request would have left the deleted ones behind and the interval
    unset.
    """
    why = GEN.stale()
    if why:
        raise ApiError(why)
    before = {r["uid"]: r for r in gf.call("GET", "/api/v1/provisioning/alert-rules")}
    lines = []
    pushed = set()
    for group in GEN.groups():
        rules = []
        for rule in group["rules"]:
            body = {k: v for k, v in rule.items() if k != "uid"}
            body.update(uid=rule["uid"], orgID=1, folderUID=FOLDER_UID,
                        ruleGroup=group["name"])
            rules.append(body)
            pushed.add(rule["uid"])
        payload = {
            "title": group["name"],
            "folderUid": FOLDER_UID,
            "interval": VERIFY.seconds(group["interval"]),
            "rules": rules,
        }
        gf.write("PUT",
                 f"/api/v1/provisioning/folder/{FOLDER_UID}/rule-groups/{group['name']}",
                 payload)
        new = [r["uid"] for r in rules if r["uid"] not in before]
        changed = [r["uid"] for r in rules
                   if r["uid"] in before and VERIFY.rule_differences(before[r["uid"]], r)]
        lines.append(
            f"group {group['name']}: {len(rules)} rules "
            f"({len(new)} new, {len(changed)} changed, "
            f"{len(rules) - len(new) - len(changed)} unchanged), interval {group['interval']}"
        )
    stale_rules = [uid for uid, r in before.items()
                   if uid.startswith("repose-") and uid not in pushed
                   and r.get("folderUID") == FOLDER_UID]
    for uid in sorted(stale_rules):
        gf.write("DELETE", f"/api/v1/provisioning/alert-rules/{uid}", allow=(404,))
        lines.append(f"rule {uid}: deleted, ops/alerts.yaml no longer has it")
    return lines


def push_dashboards(gf: Grafana, extra: list[str]) -> list[str]:
    """ops/dashboards/push.py, which already does these."""
    dash = _load("dash_push", ROOT / "ops" / "dashboards" / "push.py")
    argv = sys.argv
    skipped = [arg for uid in VERIFY.SKIP_DATASOURCES for arg in ("--expect-missing", uid)]
    sys.argv = ["push.py", "--folder", "repose",
                "--prometheus", "repose-prometheus",
                "--loki", "repose-loki",
                "--postgres", "repose-postgres", *skipped, *extra]
    if gf.dry_run:
        sys.argv.append("--dry-run")
    try:
        dash.main()
    finally:
        sys.argv = argv
    return []


# --- the command ----------------------------------------------------------


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--dry-run", action="store_true",
                    help="read the target and print the difference; write nothing")
    ap.add_argument("--url", default=os.environ.get("GRAFANA_URL"),
                    help="default: $GRAFANA_URL")
    ap.add_argument("--skip-dashboards", action="store_true",
                    help="alerting and datasources only")
    ap.add_argument("--no-verify", action="store_true",
                    help="do not run ops/grafana/verify.py afterwards")
    args = ap.parse_args()
    if not args.url:
        print("set GRAFANA_URL, or pass --url", file=sys.stderr)
        return 2
    token = os.environ.get("GRAFANA_TOKEN")
    if not token:
        print("no GRAFANA_TOKEN: sending unauthenticated requests, which only an "
              "anonymous-admin Grafana accepts (ops/dev)", file=sys.stderr)
    os.environ["GRAFANA_URL"] = args.url

    gf = Grafana(args.url, token, args.dry_run)
    try:
        health = gf.call("GET", "/api/health")
        print(f"{args.url}: Grafana {health.get('version')}, database {health.get('database')}")
        out = push_datasources(gf, GEN.read_yaml(DATASOURCES))
        out += push_folder(gf)
        out += push_rules(gf)
    except (ApiError, GEN.YamlError) as exc:
        print(f"ops/grafana/push.py: {exc}", file=sys.stderr)
        return 1
    for line in out:
        print("  " + line)
    if not args.skip_dashboards:
        try:
            push_dashboards(gf, [])
        except SystemExit as exc:
            if exc.code:
                print(f"dashboards: {exc}", file=sys.stderr)
                return 1
    if args.dry_run:
        print("--dry-run: nothing was written")
        return 0
    print(f"{gf.writes} write requests")
    if args.no_verify:
        return 0
    print("verifying:")
    return VERIFY.run(args.url, token)


if __name__ == "__main__":
    sys.exit(main())
