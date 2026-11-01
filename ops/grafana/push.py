#!/usr/bin/env python3
"""Put repose's observability config into a running Grafana.

One command, over Grafana's HTTP API: the three datasources, the 53 alert
rules and the eight dashboards. Nothing is mounted into the container
(DECISIONS I-944).

    GRAFANA_URL=https://grafana.example GRAFANA_TOKEN=glsa_... \\
    REPOSE_PG_HOST=... REPOSE_PG_PASSWORD_FILE=/path/to/pw \\
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

  ops/grafana/provisioning/datasources/repose.yaml   the three datasources
  ops/grafana/provisioning/alerting/repose-rules.yaml      GENERATED
  ops/dashboards/*.json                                    GENERATED

Neither generated file is re-derived here. The rules come from
`ops/grafana/alerts-gen.py` itself, and this refuses to push at all if the
committed YAML is not what the generator produces -- pushing a definition
nobody has read is how a fixed alert goes live as the broken one. The
dashboards are pushed by `ops/dashboards/push.py`, which this calls; there is
one implementation of each.

THE ONE SECRET LEFT. The datasource file names the control plane's Postgres
with `$REPOSE_PG_HOST` and `$REPOSE_PG_PASSWORD`, Grafana's own
file-provisioning placeholders. Over the API nothing expands them, so this
does, from the environment: the variable NAME, or the file named by NAME_FILE
when the value should not be in an environment at all. A name with neither is
an error that lists every missing one at once. No value is printed, logged,
or written to any file, in --dry-run or out of it.

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


# --- placeholders ---------------------------------------------------------

PLACEHOLDER = re.compile(r"\$__env\{([A-Z_][A-Z0-9_]*)\}|\$\{([A-Z_][A-Z0-9_]*)\}|\$([A-Z_][A-Z0-9_]*)")


class Missing(Exception):
    pass


def resolve(name: str, wanted: set) -> str:
    """The value of one placeholder, from NAME or from the file NAME_FILE."""
    if name in os.environ:
        return os.environ[name]
    path = os.environ.get(name + "_FILE")
    if path:
        try:
            # A secret written with `printf '%s'` has no newline; one written
            # with an editor has. Neither belongs in an address or a URL.
            return pathlib.Path(path).read_text().strip()
        except OSError as exc:
            raise Missing(f"{name}_FILE={path}: {exc.strerror}") from None
    wanted.add(name)
    return ""


def expand(node, wanted: set):
    """Every placeholder in a parsed provisioning file, substituted."""
    if isinstance(node, dict):
        return {k: expand(v, wanted) for k, v in node.items()}
    if isinstance(node, list):
        return [expand(v, wanted) for v in node]
    if isinstance(node, str):
        def one(m):
            return resolve(m.group(1) or m.group(2) or m.group(3), wanted)

        return PLACEHOLDER.sub(one, node)
    return node


# --- the three things it pushes -------------------------------------------


def push_datasources(gf: Grafana, doc: dict) -> list[str]:
    """The three of ops/grafana/provisioning/datasources/repose.yaml.

    By uid, so the dashboards' and the alert rules' `repose-prometheus` means
    this one. A datasource is created once and updated in place after that;
    `secureJsonData` is write-only in Grafana, so the password is sent on
    every run and never read back.
    """
    have = {d["uid"]: d for d in gf.call("GET", "/api/datasources")}
    lines = []
    for ds in doc["datasources"]:
        uid = ds["uid"]
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
            # Its password is write-only: Grafana answers `secureJsonFields:
            # {password: true}` and never the value, so there is no way to
            # tell a matching password from a changed one and it is sent on
            # every run. Everything readable is compared, so --dry-run can
            # still say whether anything else moved.
            readable = {k: v for k, v in body.items()
                        if k not in ("secureJsonData", "id", "version")}
            drift = sorted(k for k, v in readable.items()
                           if not VERIFY.same(v, have[uid].get(k)))
            secret = bool(ds.get("secureJsonData"))
            if not drift and not secret:
                lines.append(f"datasource {uid} ({ds['type']}): as written")
                continue
            gf.write("PUT", f"/api/datasources/uid/{uid}", body)
            if drift:
                lines.append(f"datasource {uid} ({ds['type']}): rewritten, the target's "
                             + ", ".join(drift) + " did not match")
            else:
                lines.append(f"datasource {uid} ({ds['type']}): as written, its password "
                             "sent again")
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
    sys.argv = ["push.py", "--folder", "repose",
                "--prometheus", "repose-prometheus",
                "--loki", "repose-loki",
                "--postgres", "repose-postgres", *extra]
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
    wanted: set = set()
    try:
        health = gf.call("GET", "/api/health")
        print(f"{args.url}: Grafana {health.get('version')}, database {health.get('database')}")
        # Every placeholder is resolved before the first write, so a missing
        # variable leaves the target as it was rather than half pushed.
        datasources = expand(GEN.read_yaml(DATASOURCES), wanted)
        if wanted:
            raise Missing(
                "no value for " + ", ".join(sorted(wanted))
                + ". Set each one, or its _FILE naming a file to read it from "
                  "(ops/README.md step 3)"
            )
        out = push_datasources(gf, datasources)
        out += push_folder(gf)
        out += push_rules(gf)
    except (ApiError, Missing, GEN.YamlError) as exc:
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
