#!/usr/bin/env python3
"""Generate the repose Grafana dashboards.

The dashboards of docs/workstreams/10-observability.md §5, one JSON file
each, provisioned by ops/grafana/provisioning/dashboards/repose.yaml.

Why a generator and not seven hand-written files: a dashboard is 400 lines of
JSON of which four matter, and the same panel (a byte-valued timeseries over a
Prometheus query, with a legend and a unit) appears thirty times. Editing that
by hand is how panels drift apart. This file is the source of truth; the JSON
next to it is generated and committed, because Grafana's provisioning reads
files, not scripts, and `ops/check.sh` fails if the two disagree — the same
arrangement as the generated protobuf stubs of DECISIONS I-38.

    python3 ops/dashboards/gen.py          # rewrite the JSON files
    python3 ops/dashboards/gen.py --check  # fail if they are out of date
"""

from __future__ import annotations

import argparse
import json
import pathlib
import sys

HERE = pathlib.Path(__file__).resolve().parent

PROM = {"type": "prometheus", "uid": "repose-prometheus"}
LOKI = {"type": "loki", "uid": "repose-loki"}
PG = {"type": "grafana-postgresql-datasource", "uid": "repose-postgres"}

# Every dashboard is tagged so the provisioned folder can be found at a glance
# and so `/api/search?tag=repose` lists exactly ours.
TAGS = ["repose"]


# --- targets ---------------------------------------------------------------


def q(expr: str, legend: str = "", instant: bool = False, fmt: str = "time_series") -> dict:
    """A Prometheus query. fmt is the query editor's Format: a heatmap panel
    needs "heatmap" or it receives series instead of buckets."""
    t = {
        "datasource": PROM,
        "editorMode": "code",
        "expr": expr,
        "legendFormat": legend or "__auto",
        "range": not instant,
        "instant": instant,
        "refId": "A",
    }
    if fmt != "time_series":
        t["format"] = fmt
    return t


def logq(expr: str, legend: str = "") -> dict:
    """A Loki query."""
    return {
        "datasource": LOKI,
        "editorMode": "code",
        "expr": expr,
        "legendFormat": legend or "__auto",
        "queryType": "range",
        "refId": "A",
    }


def sql(raw: str, fmt: str = "time_series") -> dict:
    """A Postgres query. Per-project figures live here, never in Prometheus."""
    return {
        "datasource": PG,
        "editorMode": "code",
        "format": fmt,
        "rawQuery": True,
        "rawSql": " ".join(raw.split()),
        "refId": "A",
    }


def refids(targets: list[dict]) -> list[dict]:
    out = []
    for i, t in enumerate(targets):
        t = dict(t)
        t["refId"] = chr(ord("A") + i)
        out.append(t)
    return out


# --- panels ----------------------------------------------------------------


def panel(
    kind: str,
    title: str,
    targets: list[dict],
    *,
    desc: str = "",
    unit: str | None = None,
    w: int = 12,
    h: int = 8,
    min_: float | None = None,
    max_: float | None = None,
    thresholds: list[tuple[str, float | None]] | None = None,
    options: dict | None = None,
    stack: bool = False,
    decimals: int | None = None,
    overrides: list[dict] | None = None,
) -> dict:
    targets = refids(targets)
    ds = targets[0]["datasource"] if targets else PROM
    defaults: dict = {"custom": {}}
    if unit:
        defaults["unit"] = unit
    if min_ is not None:
        defaults["min"] = min_
    if max_ is not None:
        defaults["max"] = max_
    if decimals is not None:
        defaults["decimals"] = decimals
    if thresholds:
        defaults["thresholds"] = {
            "mode": "absolute",
            "steps": [{"color": c, "value": v} for c, v in thresholds],
        }
    else:
        defaults["thresholds"] = {"mode": "absolute", "steps": [{"color": "text", "value": None}]}
    if kind == "timeseries":
        defaults["custom"] = {
            "drawStyle": "line",
            "lineWidth": 1,
            "fillOpacity": 10 if not stack else 40,
            "showPoints": "never",
            "stacking": {"mode": "normal" if stack else "none", "group": "A"},
            "axisSoftMin": 0,
        }
    opts = {
        "legend": {"displayMode": "table", "placement": "bottom", "showLegend": True, "calcs": ["lastNotNull", "max"]},
        "tooltip": {"mode": "multi", "sort": "desc"},
    }
    if kind in ("stat", "gauge"):
        opts = {
            "colorMode": "value",
            "graphMode": "area",
            "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
            "textMode": "auto",
        }
    if kind == "table":
        opts = {"showHeader": True, "cellHeight": "sm", "footer": {"show": False}}
    if kind == "state-timeline":
        opts = {"legend": {"displayMode": "list", "placement": "bottom", "showLegend": True}, "mergeValues": True, "showValue": "auto"}
    if kind == "heatmap":
        opts = {
            "calculate": False,
            "color": {"mode": "scheme", "scheme": "Turbo", "steps": 64},
            "yAxis": {"unit": unit or "s"},
            "cellGap": 1,
        }
    if options:
        opts.update(options)
    return {
        "type": kind,
        "title": title,
        "description": desc,
        "datasource": ds,
        "targets": targets,
        "fieldConfig": {"defaults": defaults, "overrides": overrides or []},
        "options": opts,
        "gridPos": {"h": h, "w": w, "x": 0, "y": 0},
    }


def row(title: str) -> dict:
    return {"type": "row", "title": title, "collapsed": False, "panels": [], "gridPos": {"h": 1, "w": 24, "x": 0, "y": 0}}


def layout(panels: list[dict]) -> list[dict]:
    """Place panels left to right, wrapping at 24 columns, numbering them."""
    x = y = 0
    rowh = 0
    out = []
    for i, p in enumerate(panels, start=1):
        p = dict(p)
        w = p["gridPos"]["w"]
        h = p["gridPos"]["h"]
        if p["type"] == "row":
            if x:
                y += rowh
            x, rowh = 0, 0
            p["gridPos"] = {"h": 1, "w": 24, "x": 0, "y": y}
            y += 1
            p["id"] = i
            out.append(p)
            continue
        if x + w > 24:
            y += rowh
            x, rowh = 0, 0
        p["gridPos"] = {"h": h, "w": w, "x": x, "y": y}
        x += w
        rowh = max(rowh, h)
        p["id"] = i
        out.append(p)
    return out


def dashboard(uid: str, title: str, panels: list[dict], *, variables: list[dict] | None = None,
              time_from: str = "now-6h", refresh: str = "1m", desc: str = "") -> dict:
    return {
        "uid": uid,
        "title": title,
        "description": desc,
        "tags": TAGS,
        "timezone": "utc",
        "editable": True,
        "graphTooltip": 1,
        "schemaVersion": 39,
        "refresh": refresh,
        "time": {"from": time_from, "to": "now"},
        "templating": {"list": variables or []},
        "panels": layout(panels),
    }


def var_query(name: str, label: str, query, *, ds=PROM, multi=True, include_all=True, regex="") -> dict:
    return {
        "name": name,
        "label": label,
        "type": "query",
        "datasource": ds,
        "query": query,
        "refresh": 1,
        "includeAll": include_all,
        "multi": multi,
        "allValue": ".*" if include_all and multi else None,
        "regex": regex,
        "current": {},
        "options": [],
        "sort": 1,
    }


# --- 1. host capacity ------------------------------------------------------

HOST_VAR = var_query("host_id", "host", {"query": "label_values(repose_host_mem_free_bytes, host_id)", "refId": "hosts"})


def host_capacity() -> dict:
    return dashboard(
        "repose-host-capacity",
        "repose / Host capacity",
        desc="Whether the fleet can take another guest, and whether each host is healthy. Capacity is added by hand at 80 percent reserved memory (DECISIONS R3-5).",
        variables=[HOST_VAR],
        panels=[
            panel(
                "stat", "Reserved memory",
                [q('repose_host_mem_reserved_bytes{host_id=~"$host_id"} / (repose_host_mem_reserved_bytes{host_id=~"$host_id"} + repose_host_mem_free_bytes{host_id=~"$host_id"})',
                   "{{host_id}}", instant=True)],
                desc="The HostMemory80 expression. Memory is never oversubscribed, so this is the capacity figure.",
                unit="percentunit", w=6, h=6, min_=0, max_=1,
                thresholds=[("green", None), ("yellow", 0.7), ("red", 0.8)],
            ),
            panel(
                "stat", "Stream connected",
                [q('min by (host_id) (repose_host_stream_connected{host_id=~"$host_id"})', "{{host_id}}", instant=True)],
                desc="1 while hostd holds its Session stream to the api. A zero here and a HostUnreachable alert are the same incident.",
                w=6, h=6, min_=0, max_=1,
                thresholds=[("red", None), ("green", 1)],
                options={"textMode": "value_and_name"},
            ),
            panel(
                "stat", "Builds running",
                [q('sum by (host_id) (repose_host_builds_running{host_id=~"$host_id"})', "{{host_id}}", instant=True)],
                desc="At most two per host (DECISIONS R5-4). Two for 45 minutes with nothing finishing is BuildQueueStuck.",
                w=6, h=6, thresholds=[("green", None), ("yellow", 2)],
            ),
            panel(
                "stat", "Guests with guestd lost",
                [q('sum by (host_id) (repose_host_guestd_lost{host_id=~"$host_id"})', "{{host_id}}", instant=True)],
                desc="Guests that cannot be frozen, resized or switched. Which guest it is comes from the guestd_lost line in Loki.",
                w=6, h=6, thresholds=[("green", None), ("red", 1)],
            ),
            panel(
                "timeseries", "Memory: reserved and free",
                [q('repose_host_mem_reserved_bytes{host_id=~"$host_id"}', "reserved {{host_id}}"),
                 q('repose_host_mem_free_bytes{host_id=~"$host_id"}', "free {{host_id}}")],
                desc="Reserved is the sum of running guests' RAM; free is what is left after the host reserve (8 GiB below 128 GiB of RAM, 16 above).",
                unit="bytes",
            ),
            panel(
                "timeseries", "Guests by state",
                [q('sum by (state) (repose_host_guests{host_id=~"$host_id"})', "{{state}}")],
                desc="The guest state enum of docs/interfaces/README.md. Anything stuck in creating, building or stopping is worth a look in Loki.",
                stack=True,
            ),
            panel(
                "timeseries", "Thin pool",
                [q('repose_host_pool_free_bytes{host_id=~"$host_id"}', "free {{host_id}}"),
                 q('repose_host_pool_bytes{host_id=~"$host_id"}', "size {{host_id}}")],
                desc="vg-guests/thin. Under 10 percent free pages (PoolFull): every guest's writes fail at once.",
                unit="bytes",
            ),
            panel(
                "timeseries", "Store and root filesystem",
                [q('repose_host_store_bytes{host_id=~"$host_id"}', "store {{host_id}}"),
                 q('node_filesystem_size_bytes{job="hosts",mountpoint="/",fstype!~"tmpfs|ramfs",host_id=~"$host_id"} - node_filesystem_avail_bytes{job="hosts",mountpoint="/",fstype!~"tmpfs|ramfs",host_id=~"$host_id"}', "root used {{host_id}}")],
                desc="Every guest reads /nix/store over virtio-fs, so the store is on the host root filesystem. Over 85 percent used is StoreFull.",
                unit="bytes",
            ),
            panel(
                "timeseries", "Guest CPU seconds by class",
                [q('sum by (class) (rate(repose_host_guest_cpu_seconds_total{host_id=~"$host_id"}[5m]))', "{{class}}")],
                desc="CPU is oversubscribed 2:1 against the host's vCPU (DESIGN §4); this is what is actually being used, by size class.",
                unit="none",
            ),
            panel(
                "timeseries", "Guest network bytes",
                [q('sum by (direction) (rate(repose_host_guest_net_bytes_total{host_id=~"$host_id"}[5m]))', "{{direction}}")],
                desc="From the per-guest nftables counters. Egress is shaped at 200 Mbit/s per guest (DECISIONS R5-5); the per-project view is on the Abuse dashboard.",
                unit="Bps",
            ),
            panel(
                "timeseries", "Commands by result",
                [q('sum by (kind, result) (rate(repose_host_commands_total{host_id=~"$host_id"}[15m]))', "{{kind}} {{result}}")],
                desc="Every command of docs/interfaces/grpc-hostd.md. A repeated error on one kind is the first sign of a host-level failure.",
                unit="none",
            ),
            panel(
                "timeseries", "Version",
                [q('repose_build_info{component="hostd"}', "{{version}}")],
                desc="Which hostd is running where, so a rollout can be seen finishing.",
                w=12, h=6,
            ),
        ],
    )


# --- 2. per-guest resources ------------------------------------------------

PROJECT_VAR = var_query(
    "project_id", "project",
    "select p.slug || ' (' || u.handle || ')' as __text, p.id::text as __value from projects p join users u on u.id = p.user_id where p.destroyed_at is null order by 1",
    ds=PG, multi=False, include_all=False,
)


def per_guest() -> dict:
    where = "where project_id = '$project_id'::uuid and $__timeFilter(ts)"
    return dashboard(
        "repose-per-guest",
        "repose / Per-guest resources",
        desc="One project over time, from meter_samples in Postgres. Prometheus carries no per-project series on purpose (§5); this is where a support question is answered.",
        variables=[PROJECT_VAR],
        time_from="now-24h",
        panels=[
            panel(
                "timeseries", "CPU busy fraction",
                [sql(f"select ts as time, cpu_ns / 60e9 as cpu from meter_samples {where} order by ts")],
                desc="cpu_ns is the delta since the previous sample, 60 seconds apart, so this is cores busy. A small guest has 2.",
                unit="none",
            ),
            panel(
                "timeseries", "Memory RSS",
                [sql(f"select ts as time, mem_rss as rss from meter_samples {where} order by ts")],
                desc="Resident memory of the guest. It cannot exceed the class (4, 8 or 16 GB) because memory is never oversubscribed.",
                unit="bytes",
            ),
            panel(
                "timeseries", "Network",
                [sql(f"select ts as time, net_tx / 60.0 as tx, net_rx / 60.0 as rx from meter_samples {where} order by ts")],
                desc="Bytes a second, from the per-guest nftables counters. Egress is what is billed past 500 GB a month.",
                unit="Bps",
            ),
            panel(
                "timeseries", "Disk",
                [sql(f"select ts as time, disk_alloc as allocated, disk_used as used from meter_samples {where} order by ts")],
                desc="Allocated is what is billed (thin volumes bill on allocated size); used is what the guest has written.",
                unit="bytes",
            ),
            panel(
                "timeseries", "Signals: sessions, tmux clients, containers",
                [sql(f"""select ts as time, ssh_sessions as "ssh sessions", tmux_clients as "tmux clients",
                         docker_containers as "docker containers" from meter_samples {where} order by ts""")],
                desc="The signals DESIGN §15 records from day one so that an idle policy can be designed from data rather than guessed.",
                unit="none",
            ),
            panel(
                "state-timeline", "Agent state",
                [sql(f"""select ts as time, coalesce(a->>'agent', 'none') || ': ' || coalesce(a->>'state', 'unknown') as agent
                         from meter_samples left join lateral jsonb_array_elements(agents) a on true {where} order by ts""")],
                desc="working, idle, needs_input or unknown, per agent process, from guestd's tmux window state. This is the series an idle policy would be built on.",
            ),
            panel(
                "state-timeline", "Guest state and guestd",
                [sql(f"""select ts as time,
                         state || (case when guestd_ok is false then ' (guestd lost)'
                                        when guestd_ok is null then ' (guestd unknown)' else '' end) as state
                         from meter_samples {where} order by ts""")],
                desc="The guest's own state, with the gaps where guestd was unreachable. Samples continue during those gaps; the signals inside them do not. A null guestd_ok is a sample from before the column existed, which is unknown rather than lost (DECISIONS I-54).",
            ),
            panel(
                "table", "Top processes in the window",
                [sql("""select comm, sum(cpu_ns) / 1e9 as cpu_seconds, max(rss) as max_rss
                        from proc_samples where project_id = '$project_id'::uuid and $__timeFilter(ts)
                        group by comm order by cpu_seconds desc limit 20""", fmt="table")],
                desc="Process names only: no arguments, no environment, no paths (DECISIONS R5-3, and the privacy policy in the same words).",
                w=24,
            ),
        ],
    )


# --- 3. builds -------------------------------------------------------------


def builds() -> dict:
    return dashboard(
        "repose-builds",
        "repose / Builds",
        desc="Every guest system closure is built on the project's host (DECISIONS R4-4). A build that fails is a user watching a stream of Nix output.",
        variables=[HOST_VAR],
        time_from="now-24h",
        panels=[
            panel(
                "stat", "Builds in the window",
                [q('sum(increase(repose_host_build_duration_seconds_count{host_id=~"$host_id"}[$__range]))', "builds", instant=True)],
                w=6, h=6, desc="Completed builds, successful or not.",
            ),
            panel(
                "stat", "Failure rate",
                [q('sum(increase(repose_host_build_duration_seconds_count{host_id=~"$host_id",result!="ok"}[$__range])) / clamp_min(sum(increase(repose_host_build_duration_seconds_count{host_id=~"$host_id"}[$__range])), 1)',
                   "failed", instant=True)],
                unit="percentunit", w=6, h=6, min_=0, max_=1,
                thresholds=[("green", None), ("yellow", 0.1), ("red", 0.3)],
                desc="A fragment that does not evaluate is a user error and normal; a rate that climbs across projects is ours.",
            ),
            panel(
                "stat", "Queue depth",
                [q('sum by (host_id) (repose_host_build_queue_depth{host_id=~"$host_id"})', "{{host_id}}", instant=True)],
                w=6, h=6, thresholds=[("green", None), ("yellow", 3)],
                desc="Builds waiting for one of the two slots.",
            ),
            panel(
                "stat", "p90 build duration",
                [q('histogram_quantile(0.9, sum by (le) (rate(repose_host_build_duration_seconds_bucket{host_id=~"$host_id"}[$__range])))',
                   "p90", instant=True)],
                unit="s", w=6, h=6,
                thresholds=[("green", None), ("yellow", 300), ("red", 1500)],
                desc="The cap is 30 minutes (DECISIONS R5-4); p90 near it means users are waiting.",
            ),
            panel(
                "heatmap", "Build duration distribution",
                [q('sum by (le) (increase(repose_host_build_duration_seconds_bucket{host_id=~"$host_id"}[$__interval]))', "{{le}}", fmt="heatmap")],
                desc="The histogram §5 asks for: most builds are substituted and fast, and the tail is source builds.",
                unit="s", w=12, h=9,
                options={"calculate": False},
            ),
            panel(
                "timeseries", "Eval versus build time",
                [q('histogram_quantile(0.9, sum by (le) (rate(repose_host_build_phase_duration_seconds_bucket{host_id=~"$host_id",phase="eval"}[15m])))', "eval p90"),
                 q('histogram_quantile(0.9, sum by (le) (rate(repose_host_build_phase_duration_seconds_bucket{host_id=~"$host_id",phase="build"}[15m])))', "build p90")],
                desc="Evaluation is capped at 60 s and the build at 30 minutes. A slow eval is the fragment; a slow build is a substituter or a source build.",
                unit="s", w=12, h=9,
            ),
            panel(
                "timeseries", "Failures by error code",
                [q('sum by (result) (increase(repose_host_build_duration_seconds_count{host_id=~"$host_id",result!="ok"}[15m]))', "{{result}}")],
                desc="The result label carries the error code of docs/interfaces/nix-build-contract.md: eval_failed, build_failed, closure_too_large, eval_timeout, build_timeout.",
                stack=True,
            ),
            panel(
                "table", "Recent build failures",
                [logq('{component="hostd"} | json | event="build_fail" | line_format "{{.code}} project={{.project_id}} revision={{.revision_id}} {{.duration_ms}}ms"')],
                desc="From Loki. The full Nix output of any of them is in build_logs, not here: `repose-admin ops log <op id>`.",
                w=24, h=9,
            ),
        ],
    )


# --- 4. gateway ------------------------------------------------------------


def gateway() -> dict:
    return dashboard(
        "repose-gateway",
        "repose / Gateway",
        desc="The SSH relay on the edge. Every user session passes through it, so it is the first place a 'cannot connect' report is checked.",
        time_from="now-12h",
        panels=[
            panel(
                "stat", "Sessions now",
                [q("sum(repose_gateway_sessions)", "sessions", instant=True)],
                w=6, h=6, desc="Relayed SSH connections open right now. The api's /internal/sessions cross-checks this against the ssh_sessions signal.",
            ),
            panel(
                "stat", "Sessions in the window",
                [q("sum(increase(repose_gateway_sessions_total[$__range]))", "sessions", instant=True)],
                desc="Connections accepted and relayed over the dashboard's time range, successful ones only.",
                w=6, h=6,
            ),
            panel(
                "stat", "Auth failures a second",
                [q("sum(rate(repose_gateway_auth_fail_total[5m]))", "per second", instant=True)],
                w=6, h=6, decimals=2,
                thresholds=[("green", None), ("yellow", 0.5), ("red", 1)],
                desc="Over one a second for five minutes is GatewayAuthSpike: a scan, or the CLI's refresh path broken.",
            ),
            panel(
                "stat", "p90 route lookup",
                [q("histogram_quantile(0.9, sum by (le) (rate(repose_gateway_route_duration_seconds_bucket[$__range])))", "p90", instant=True)],
                unit="s", w=6, h=6, thresholds=[("green", None), ("yellow", 0.25), ("red", 1)],
                desc="Login name to guest address, which is an api call. It is in the path of every connection.",
            ),
            panel(
                "timeseries", "Auth failures by reason",
                [q("sum by (reason) (rate(repose_gateway_auth_fail_total[5m]))", "{{reason}}")],
                desc="bad_cert and not_found are a scan or a wrong login name; expired and revoked are the CLI's certificate refresh; wrong_principal is a certificate for another project; stopped is a guest that is not running.",
                stack=True,
            ),
            panel(
                "timeseries", "Sessions and dial failures",
                [q("sum(repose_gateway_sessions)", "open sessions"),
                 q("sum(rate(repose_gateway_dial_fail_total[5m])) * 300", "dial failures / 5m")],
                desc="A dial failure is the gateway reaching a guest's sshd and failing: the guest is down, or WireGuard to its host is.",
                unit="none",
            ),
            panel(
                "timeseries", "Route lookup latency",
                [q("histogram_quantile(0.5, sum by (le) (rate(repose_gateway_route_duration_seconds_bucket[5m])))", "p50"),
                 q("histogram_quantile(0.9, sum by (le) (rate(repose_gateway_route_duration_seconds_bucket[5m])))", "p90"),
                 q("histogram_quantile(0.99, sum by (le) (rate(repose_gateway_route_duration_seconds_bucket[5m])))", "p99")],
                desc="The api call the gateway makes on every connection. A rising p99 here is the api, not the edge.",
                unit="s",
            ),
            panel(
                "table", "Recent authentication failures",
                [logq('{component="gateway"} | json | event="auth_fail" | line_format "{{.reason}} project={{.project_id}} serial={{.cert_serial}}"')],
                desc="No IP address and no user agent: docs/ops/OBSERVABILITY.md forbids both. The gateway keeps a per-IP counter in memory for rate limiting and logs none of it.",
                w=24, h=8,
            ),
        ],
    )


# --- 5. snapshots ----------------------------------------------------------


def snapshots() -> dict:
    return dashboard(
        "repose-snapshots",
        "repose / Snapshots",
        desc="Nightly at 03:00 in the host's timezone and on every stop, kept 7 days (DESIGN §6). A snapshot is the only thing between a host loss and a tenant losing work.",
        time_from="now-7d",
        panels=[
            panel(
                "stat", "Oldest snapshot among running projects",
                [q("max(repose_api_snapshot_age_seconds)", "age", instant=True)],
                unit="s", w=8, h=6,
                thresholds=[("green", None), ("yellow", 86400), ("red", 129600)],
                desc="The SnapshotStale input. Over 36 hours means two schedules have been missed.",
            ),
            panel(
                "stat", "Snapshot failures today",
                [q("sum(increase(repose_host_snapshot_duration_seconds_count{result!=\"ok\"}[24h]))", "failures", instant=True)],
                desc="A failed snapshot leaves the guest thawed and the previous snapshot in place; the cause is in the snapshot_fail line in Loki.",
                w=8, h=6, thresholds=[("green", None), ("red", 1)],
            ),
            panel(
                "stat", "p99 freeze window",
                [q("histogram_quantile(0.99, sum by (le) (rate(repose_host_snapshot_freeze_seconds_bucket[$__range])))", "p99", instant=True)],
                unit="s", w=8, h=6, decimals=3,
                thresholds=[("green", None), ("yellow", 1), ("red", 5)],
                desc="DESIGN §6 promises under one second. The guest is frozen for this long; every write in it blocks.",
            ),
            panel(
                "table", "Age per running project",
                [sql("""select p.slug as project, u.handle as owner, p.class,
                        max(s.taken_at) as last_snapshot,
                        extract(epoch from (now() - max(s.taken_at))) as age_seconds,
                        max(s.bytes) as last_bytes
                        from projects p join users u on u.id = p.user_id
                        left join snapshots s on s.project_id = p.id and s.deleted_at is null
                        where p.state = 'running' group by 1,2,3 order by age_seconds desc nulls first""", fmt="table")],
                desc="The table §5 asks for. A null age is a running project that has never been snapshotted, which is worse than a stale one.",
                w=24, h=9,
                overrides=[{"matcher": {"id": "byName", "options": "age_seconds"},
                            "properties": [{"id": "unit", "value": "s"},
                                           {"id": "custom.cellOptions", "value": {"type": "color-text"}},
                                           {"id": "thresholds", "value": {"mode": "absolute", "steps": [
                                               {"color": "green", "value": None},
                                               {"color": "yellow", "value": 86400},
                                               {"color": "red", "value": 129600}]}}]},
                           {"matcher": {"id": "byName", "options": "last_bytes"},
                            "properties": [{"id": "unit", "value": "bytes"}]}],
            ),
            panel(
                "timeseries", "Bytes per day",
                [sql("""select date_trunc('day', taken_at) as time, sum(bytes) as bytes
                        from snapshots where $__timeFilter(taken_at) and deleted_at is null
                        group by 1 order by 1""")],
                desc="What is going into Blob, zstd-compressed. Retention is 7 daily per project, 30 days after a destroy.",
                unit="bytes",
            ),
            panel(
                "timeseries", "Snapshot duration by reason",
                [q("histogram_quantile(0.9, sum by (le, reason) (rate(repose_host_snapshot_duration_seconds_bucket[1h])))", "{{reason}} p90")],
                desc="scheduled is the nightly timer, stop is on a `repose stop`, manual is an operator. The upload to Blob dominates.",
                unit="s",
            ),
        ],
    )


# --- 6. billing ------------------------------------------------------------


def billing() -> dict:
    return dashboard(
        "repose-billing",
        "repose / Billing",
        desc="Plans through Polar (DECISIONS I-289, I-604): the subscription mix, the webhook, the gate's refusals, the egress overage line, and the hours and egress usage_hours records. Polar holds the money; usage_hours holds what was used.",
        time_from="now-2d",
        panels=[
            panel(
                "stat", "Rollup lag",
                [q("max(repose_api_rollup_lag_seconds)", "lag", instant=True)],
                unit="s", w=6, h=6,
                thresholds=[("green", None), ("yellow", 3600), ("red", 7200)],
                desc="Now minus the last hour rolled into usage_hours. Past two hours is RollupLag.",
            ),
            panel(
                "stat", "Webhooks rejected in 10 minutes",
                [q('sum(increase(repose_api_billing_webhook_total{result="bad_signature"}[10m]))', "rejected", instant=True)],
                w=6, h=6, thresholds=[("green", None), ("red", 5)],
                desc="Webhook signature failures. Five in ten minutes is BillingWebhookRejected: the secret and the endpoint have drifted, and no subscription event is applied.",
            ),
            panel(
                "stat", "Overage charges failed in the last hour",
                [q('sum(increase(repose_api_billing_overage_charges_total{result="error"}[1h]))', "failed", instant=True)],
                w=6, h=6, thresholds=[("green", None), ("red", 1)],
                desc="An egress line Polar refused. The next hourly tick sends it again under the same external id (OverageChargeFailed).",
            ),
            panel(
                "stat", "Live subscriptions",
                [sql("""select count(*) as live from subscriptions where status in ('trialing','active','past_due')""", fmt="table")],
                w=6, h=6,
                desc="Subscriptions that hold a seat and buy compute (trialing, active, past_due). The seats the fleet sells are SEATS_TOTAL (I-290).",
            ),
            panel(
                "timeseries", "Subscription webhooks by plan and status",
                [q("sum by (plan, status) (increase(repose_api_billing_subscriptions_total[1h]))", "{{plan}} {{status}}")],
                desc="Every subscription.* event applied, by the plan it names and its status. A rising past_due series is the dunning job's next three days.",
                unit="none", stack=True,
            ),
            panel(
                "timeseries", "Gate refusals by reason",
                [q("sum by (reason) (increase(repose_api_billing_gate_refused_total[1h]))", "{{reason}}")],
                desc="payment_required answers: subscription_required (no plan), plan_limit (memory), disk_limit, egress_limit, past_due, suspended. plan_limit is the upgrade signal.",
                unit="none", stack=True,
            ),
            panel(
                "table", "Accounts by plan and status",
                [sql("""select plan, status, count(*) as accounts, sum(seats) as seats
                        from subscriptions group by 1, 2 order by 1, 2""", fmt="table")],
                desc="The subscriptions table as it stands: what Polar told the webhook last.",
                w=12, h=8,
            ),
            panel(
                "table", "Egress this period per account",
                [sql("""select u.handle as owner, s.plan, sum(h.egress_bytes) as egress,
                        (case s.plan when 'pro' then 1000 when 'plus' then 500 else 250 end) as included_gb,
                        greatest(0, ceil(sum(h.egress_bytes) / 1073741824.0 - (case s.plan when 'pro' then 1000 when 'plus' then 500 else 250 end))) * 5 / 100.0 as overage_usd
                        from subscriptions s join users u on u.id = s.user_id
                        join projects p on p.user_id = s.user_id join usage_hours h on h.project_id = p.id
                        where s.status in ('trialing','active','past_due') and h.hour >= s.period_start and h.hour < s.period_end
                        group by 1, 2 order by egress desc limit 50""", fmt="table")],
                desc="What the overage line will say (PRICING.md: $0.05 a GB past the plan's allowance), and who is near the hard stop at four times it.",
                w=12, h=8,
                overrides=[{"matcher": {"id": "byName", "options": "egress"}, "properties": [{"id": "unit", "value": "bytes"}]},
                           {"matcher": {"id": "byName", "options": "overage_usd"}, "properties": [{"id": "unit", "value": "currencyUSD"}]}],
            ),
            panel(
                "timeseries", "Guest hours per hour by class",
                [sql("""select hour as time, class, sum(running_seconds) / 3600.0 as guest_hours
                        from usage_hours where $__timeFilter(hour) group by 1, 2 order by 1""", fmt="time_series")],
                desc="A running machine accrues, a stopped one does not. One machine running the whole hour is 1.0. Not a price since plan-v1; `repose status` shows the same hours.",
                unit="none", stack=True,
            ),
            panel(
                "timeseries", "Machines stopped for billing",
                [q("sum by (reason) (increase(repose_api_billing_stops_total[1h]))", "{{reason}}")],
                desc="Stops the api made itself: past_due (day 3), ended (a cancelled subscription reached its end), egress (four times the allowance). BillingStopped alerts on any.",
                unit="none",
            ),
            panel(
                "timeseries", "Egress per hour",
                [sql("""select hour as time, sum(egress_bytes) as egress from usage_hours
                        where $__timeFilter(hour) group by 1 order by 1""")],
                desc="250 GB a month is included on Solo, 500 on Plus, 1000 on Pro, then $0.05 a GB; machines stop at four times the allowance. The per-project view is on the Abuse dashboard.",
                unit="bytes", w=24,
            ),
        ],
    )


# --- 7. abuse --------------------------------------------------------------


def abuse() -> dict:
    return dashboard(
        "repose-abuse",
        "repose / Abuse",
        desc="What abuse looks like from outside a guest: a process name nobody recognises at the top of the CPU list, egress in the terabytes, a guest at full CPU with nobody attached. Confirm with `repose-admin exec` (audited), then the runbook's Suspend a user.",
        time_from="now-24h",
        refresh="5m",
        panels=[
            panel(
                "stat", "Projects over 1 TB in 24 hours",
                [q("max(repose_api_egress_alert_projects)", "projects", instant=True)],
                w=8, h=6, thresholds=[("green", None), ("red", 1)],
                desc="The EgressHigh input, recomputed hourly by the api from meter_samples.",
            ),
            panel(
                "stat", "Fleet egress in the window",
                [sql("""select coalesce(sum(net_tx), 0) as bytes from meter_samples where $__timeFilter(ts)""", fmt="table")],
                desc="Everything every guest sent, for scale: one project's terabyte is obvious against it.",
                unit="bytes", w=8, h=6,
            ),
            panel(
                "stat", "Guests busy with nobody attached",
                [sql("""select count(*) from (
                          select project_id from meter_samples
                          where ts > now() - interval '24 hours'
                          group by project_id
                          having avg(cpu_ns) / 60e9 > 0.9 and max(ssh_sessions) = 0 and max(tmux_clients) = 0
                        ) s""", fmt="table")],
                w=8, h=6, thresholds=[("green", None), ("yellow", 1)],
                desc="Over 90 percent of a core for a day with no session and no tmux client. An unattended agent looks like this too, which is exactly why the signals are recorded before any policy is written (DECISIONS R1-5).",
            ),
            panel(
                "table", "Top 20 process names by CPU, fleet-wide, 24 hours",
                [sql("""select comm, sum(cpu_ns) / 1e9 as cpu_seconds,
                        count(distinct project_id) as projects, max(rss) as max_rss
                        from proc_samples where ts > now() - interval '24 hours'
                        group by comm order by cpu_seconds desc limit 20""", fmt="table")],
                desc="A miner is a name you do not recognise at the top of this list. Process names only, never arguments (DECISIONS R5-3).",
                w=12, h=10,
                overrides=[{"matcher": {"id": "byName", "options": "max_rss"}, "properties": [{"id": "unit", "value": "bytes"}]}],
            ),
            panel(
                "table", "Top projects by egress, 24 hours",
                [sql("""select p.slug as project, u.handle as owner, p.class,
                        sum(m.net_tx) as egress, sum(m.net_rx) as ingress,
                        max(m.ssh_sessions) as max_sessions
                        from meter_samples m join projects p on p.id = m.project_id join users u on u.id = p.user_id
                        where m.ts > now() - interval '24 hours'
                        group by 1,2,3 order by egress desc limit 20""", fmt="table")],
                desc="Egress is shaped at 200 Mbit/s per guest, which is 2 TB a day if it runs flat out; a terabyte is not a build cache.",
                w=12, h=10,
                overrides=[{"matcher": {"id": "byName", "options": "egress"}, "properties": [{"id": "unit", "value": "bytes"}]},
                           {"matcher": {"id": "byName", "options": "ingress"}, "properties": [{"id": "unit", "value": "bytes"}]}],
            ),
            panel(
                "table", "Guests at full CPU with no session for over 24 hours",
                [sql("""select p.slug as project, u.handle as owner, p.class,
                        avg(m.cpu_ns) / 60e9 as avg_cores_busy,
                        max(m.ssh_sessions) as max_sessions, max(m.tmux_clients) as max_tmux,
                        max(m.docker_containers) as max_containers,
                        sum(m.net_tx) as egress
                        from meter_samples m join projects p on p.id = m.project_id join users u on u.id = p.user_id
                        where m.ts > now() - interval '24 hours' and m.state = 'running'
                        group by 1,2,3
                        having avg(m.cpu_ns) / 60e9 > 0.9 and max(m.ssh_sessions) = 0 and max(m.tmux_clients) = 0
                        order by avg_cores_busy desc""", fmt="table")],
                desc="The third panel §5 asks for, as a list. Check the agent state on Per-guest resources before acting: an agent working alone for a day is the product working.",
                w=24, h=9,
                overrides=[{"matcher": {"id": "byName", "options": "egress"}, "properties": [{"id": "unit", "value": "bytes"}]}],
            ),
            panel(
                "timeseries", "Blocked outbound attempts by reason",
                [q("sum by (reason) (rate(repose_host_egress_blocked_total[5m]))", "{{reason}}")],
                desc="Packets guests sent into the host's blocks (DECISIONS I-238..I-240): smtp is tcp 25, stratum the mining-pool ports, flows new connections over the per-guest rate. Which guest: hostd's egress_blocked log line (the EgressBlocked alert).",
                unit="pps", w=12, h=8,
            ),
            panel(
                "table", "Automatic stops, 7 days",
                [sql("""select a.ts as time, p.slug as project, u.handle as owner, a.kind,
                        a.detail->>'process' as process, a.hold, a.cleared_at, a.cleared_by
                        from abuse_events a join projects p on p.id = a.project_id join users u on u.id = a.user_id
                        where a.ts > now() - interval '7 days'
                        order by a.ts desc limit 50""", fmt="table")],
                desc="Guests the api stopped because a miner's name was in their process samples (I-239). hold means starts are refused until `repose-admin abuse clear`; the account is untouched until someone runs `repose-admin users suspend`.",
                w=12, h=8,
            ),
        ],
    )


# --- 0. overview -----------------------------------------------------------


def overview() -> dict:
    """One screen that answers "is anything wrong?", every panel from
    Prometheus or Loki so it works on a Grafana that cannot reach the api's
    database. Each stat turns red on the same condition as its alert."""
    d = dashboard(
        "repose-overview",
        "repose / Overview",
        desc="A quick look: are the parts up, what is running, is anything failing. Links to the detailed dashboards at the top.",
        time_from="now-6h",
        refresh="30s",
        panels=[
            row("Is everything up"),
            panel("stat", "Scrape targets down",
                  [q('count(up{job=~"hosts|hostd|fluent-bit|gateway|api-grpc|api"} == 0) or vector(0)', "down", instant=True)],
                  w=4, h=5, thresholds=[("green", None), ("red", 1)],
                  desc="Prometheus targets not answering (host exporters, hostd, Fluent Bit, gateway, api). Any is a page."),
            panel("stat", "Hosts ready",
                  [q('sum(repose_api_hosts{state="ready"})', "ready", instant=True),
                   q('sum(repose_api_hosts{state=~"unreachable|lost"})', "unreachable or lost", instant=True)],
                  w=4, h=5, thresholds=[("green", None)],
                  overrides=[{"matcher": {"id": "byName", "options": "unreachable or lost"},
                              "properties": [{"id": "thresholds", "value": {"mode": "absolute", "steps": [{"color": "green", "value": None}, {"color": "red", "value": 1}]}}]}],
                  desc="Hosts by the api's view of them."),
            panel("stat", "hostd streams connected",
                  [q("sum(repose_host_stream_connected)", "connected", instant=True)],
                  w=4, h=5, thresholds=[("red", None), ("green", 1)],
                  desc="Hosts whose hostd holds its command stream to the api. Zero means nothing can be created, started or stopped."),
            panel("stat", "Guests with guestd lost",
                  [q("sum(repose_host_guestd_lost)", "lost", instant=True)],
                  w=4, h=5, thresholds=[("green", None), ("red", 1)],
                  desc="Guests whose agent stopped answering. `repose start` on the project restarts it."),
            panel("stat", "Log shipping failures (1h)",
                  [q("sum(increase(fluentbit_output_retries_failed_total[1h])) or vector(0)", "failed", instant=True)],
                  w=4, h=5, thresholds=[("green", None), ("yellow", 1), ("red", 50)],
                  desc="Chunks Fluent Bit gave up on in the last hour. Non-zero means log lines were lost."),
            panel("stat", "Newest snapshot age",
                  [q("max(repose_api_snapshot_age_seconds)", "age", instant=True)],
                  w=4, h=5, unit="s", thresholds=[("green", None), ("yellow", 26 * 3600), ("red", 48 * 3600)],
                  desc="Seconds since the newest successful snapshot anywhere; nightly snapshots keep it under a day."),

            row("What is running"),
            panel("stat", "Guests running",
                  [q('sum(repose_host_guests{state="running"})', "running", instant=True)],
                  w=4, h=5, desc="Across every host."),
            panel("stat", "Guests in error",
                  [q('sum(repose_host_guests{state="error"})', "error", instant=True)],
                  w=4, h=5, thresholds=[("green", None), ("red", 1)],
                  desc="Guests a host reports in error. `repose start` restarts one; `repose-admin ops list --state error` says why."),
            panel("stat", "SSH sessions open",
                  [q("sum(repose_gateway_sessions)", "sessions", instant=True)],
                  w=4, h=5, desc="Relayed connections through the gateway right now."),
            panel("stat", "Builds running / queued",
                  [q("sum(repose_host_builds_running)", "running", instant=True),
                   q("sum(repose_host_build_queue_depth)", "queued", instant=True)],
                  w=4, h=5, desc="Nix builds of guest configurations in progress and waiting, across hosts."),
            panel("gauge", "Host memory reserved",
                  [q("sum(repose_host_mem_reserved_bytes) / sum(node_memory_MemTotal_bytes)", "reserved", instant=True)],
                  w=4, h=5, unit="percentunit", min_=0, max_=1,
                  thresholds=[("green", None), ("yellow", 0.7), ("red", 0.8)],
                  desc="Guest memory promised as a share of host RAM; HostMemory80 fires at 0.8."),
            panel("gauge", "Thin pool used",
                  [q("max(repose_lvm_pool_data_percent) / 100", "data", instant=True)],
                  w=4, h=5, unit="percentunit", min_=0, max_=1,
                  thresholds=[("green", None), ("yellow", 0.7), ("red", 0.85)],
                  desc="The fullest host's guest volume pool."),

            row("Trends"),
            panel("timeseries", "Guests by state",
                  [q('sum by (state) (repose_host_guests) > 0', "{{state}}")],
                  stack=True, w=12, h=8, desc="Every guest on every host, by the state hostd reports."),
            panel("timeseries", "Gateway sessions and auth failures",
                  [q("sum(repose_gateway_sessions)", "open sessions"),
                   q("sum(rate(repose_gateway_auth_fail_total[5m])) * 60", "auth failures / min")],
                  w=12, h=8, desc="Open relayed SSH connections, and refused logins a minute (the Gateway dashboard splits them by reason)."),
            panel("timeseries", "Host load and free memory",
                  [q("node_load5", "load5 {{host_id}}"),
                   q("node_memory_MemAvailable_bytes / 1024^3", "GiB free {{host_id}}")],
                  w=12, h=8, desc="Five-minute load average and available RAM per host, from node_exporter."),
            panel("timeseries", "API requests by status",
                  [q("sum by (status) (rate(repose_api_requests_total[5m]))", "{{status}}")],
                  w=12, h=8, stack=True, desc="Per second, both api processes."),

            row("Recent errors"),
            panel("logs", "Warnings and errors from hosts and the edge",
                  [logq('{service_name=~"hostd|gateway|wgsync|fluent-bit|repose-host-net"} |~ `(?i)level\\W+(warn|error)|\\[ ?(warn|error)\\]`')],
                  w=24, h=10, options={"showTime": True, "wrapLogMessage": True, "sortOrder": "Descending", "enableLogDetails": True},
                  desc="The newest WARN and ERROR lines from the platform's own services. Tenant output is never here."),
        ],
    )
    d["links"] = [{"title": "repose dashboards", "type": "dashboards", "tags": TAGS, "asDropdown": True, "includeVars": False, "keepTime": True}]
    return d


DASHBOARDS = {
    "overview.json": overview,
    "host-capacity.json": host_capacity,
    "per-guest-resources.json": per_guest,
    "builds.json": builds,
    "gateway.json": gateway,
    "snapshots.json": snapshots,
    "billing.json": billing,
    "abuse.json": abuse,
}


def render(fn) -> str:
    return json.dumps(fn(), indent=2, sort_keys=False) + "\n"


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--check", action="store_true", help="fail if the committed JSON is out of date")
    args = ap.parse_args()
    stale = []
    for name, fn in DASHBOARDS.items():
        path = HERE / name
        want = render(fn)
        if args.check:
            have = path.read_text() if path.exists() else ""
            if have != want:
                stale.append(name)
            continue
        path.write_text(want)
        print(f"wrote {path.relative_to(HERE.parent.parent)}")
    if stale:
        print("out of date, run python3 ops/dashboards/gen.py: " + " ".join(stale), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
