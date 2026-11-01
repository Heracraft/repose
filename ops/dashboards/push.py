#!/usr/bin/env python3
"""Import every dashboard in this directory into a Grafana, in one folder.

    GRAFANA_URL=https://grafana.example GRAFANA_TOKEN=glsa_... \
        python3 ops/dashboards/push.py [--folder Repose] [--dry-run]

The dashboards name their datasources by the uids of the dev stack
(repose-prometheus, repose-loki, repose-postgres; ops/dev). A real Grafana
has its own uids, so each reference is rewritten to the target's
datasource of the same type: the only one of that type, or the one whose
name matches --prometheus / --loki / --postgres when there are several. A
type the target lacks is left as it is and reported; those panels read "no
data" until the datasource exists (Postgres is the api's database, which a
personal Grafana normally cannot reach).

Idempotent: the folder is found or created by title, and every dashboard is
posted with overwrite, keeping its uid, so running it again updates in
place. The token is read from the environment and never written anywhere.
"""
import argparse
import json
import os
import pathlib
import sys
import urllib.error
import urllib.request

DEV_UIDS = {"repose-prometheus": "prometheus", "repose-loki": "loki", "repose-postgres": "grafana-postgresql-datasource"}
TYPE_ALIASES = {"grafana-postgresql-datasource": {"grafana-postgresql-datasource", "postgres"}}


def api(base, token, method, path, body=None):
    headers = {"Content-Type": "application/json", "Accept": "application/json",
               # Cloudflare (error 1010) refuses Python's default User-Agent.
               "User-Agent": "repose-dashboards-push/1"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(
        base.rstrip("/") + path,
        method=method,
        data=None if body is None else json.dumps(body).encode(),
        headers=headers,
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        raise SystemExit(f"{method} {path}: HTTP {e.code}: {e.read()[:300].decode(errors='replace')}")


def pick(datasources, kind, want_name):
    types = TYPE_ALIASES.get(kind, {kind})
    cands = [d for d in datasources if d["type"] in types]
    if want_name:
        cands = [d for d in cands if d["name"] == want_name]
    if len(cands) == 1:
        return cands[0]
    if not cands:
        return None
    names = ", ".join(d["name"] for d in cands)
    flag = "postgres" if "postgres" in kind else kind
    raise SystemExit(f"several {kind} datasources ({names}); choose one with --{flag} NAME")


def rewrite(node, mapping):
    if isinstance(node, dict):
        ds = node.get("datasource")
        if isinstance(ds, dict) and ds.get("uid") in mapping:
            new = mapping[ds["uid"]]
            node["datasource"] = {"type": new["type"], "uid": new["uid"]}
        for v in node.values():
            rewrite(v, mapping)
    elif isinstance(node, list):
        for v in node:
            rewrite(v, mapping)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--folder", default="Repose")
    ap.add_argument("--prometheus", help="datasource name, when the target has several")
    ap.add_argument("--loki")
    ap.add_argument("--postgres")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--expect-missing", action="append", default=[], metavar="UID",
                    help="a dev uid whose datasource the target is not meant to have; "
                         "its panels are left pointing at it and nothing is reported. "
                         "ops/grafana/push.py passes repose-postgres (DECISIONS I-944)")
    a = ap.parse_args()
    base, token = os.environ.get("GRAFANA_URL"), os.environ.get("GRAFANA_TOKEN")
    if not base:
        raise SystemExit("set GRAFANA_URL")
    # No token means no Authorization header, which only an anonymous-admin
    # Grafana accepts (ops/dev). A real one answers 401 and says so.
    token = token or ""

    datasources = api(base, token, "GET", "/api/datasources")
    wanted = {"prometheus": a.prometheus, "loki": a.loki, "grafana-postgresql-datasource": a.postgres}
    mapping, missing = {}, []
    for uid, kind in DEV_UIDS.items():
        ds = pick(datasources, kind, wanted[kind])
        if ds is None:
            # --expect-missing silences the report, not the lookup: a target
            # that does have one still gets its panels pointed at it.
            if uid not in a.expect_missing:
                missing.append(kind)
        else:
            mapping[uid] = ds
            print(f"datasource {uid} -> {ds['name']} ({ds['uid']})")

    folder = next((f for f in api(base, token, "GET", "/api/folders?limit=1000") if f["title"] == a.folder), None)
    if folder is None and not a.dry_run:
        folder = api(base, token, "POST", "/api/folders", {"title": a.folder})
        print(f"created folder {a.folder}")
    folder_uid = folder["uid"] if folder else None

    here = pathlib.Path(__file__).resolve().parent
    for path in sorted(here.glob("*.json")):
        dash = json.loads(path.read_text())
        dash.pop("id", None)
        rewrite(dash, mapping)
        if a.dry_run:
            print(f"would import {path.name} ({dash.get('title')})")
            continue
        try:
            res = api(base, token, "POST", "/api/dashboards/db",
                      {"dashboard": dash, "folderUid": folder_uid, "overwrite": True, "message": "ops/dashboards/push.py"})
        except SystemExit as exc:
            # That Grafana provisions its dashboards from files of its own,
            # as ops/dev does, and `allowUiUpdates: false` makes the API
            # refuse to write over one. It is already there, so this is
            # reported and not an error.
            if "provisioned" in str(exc):
                print(f"{path.name}: provisioned from a file on the target, left alone")
                continue
            raise
        print(f"imported {path.name}: {base.rstrip('/')}{res.get('url', '')}")
    if missing:
        print(f"no {', '.join(missing)} datasource on the target; those panels read 'no data'", file=sys.stderr)


if __name__ == "__main__":
    main()
