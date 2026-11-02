#!/usr/bin/env python3
"""Generate the repose Grafana-managed alert rules.

The rules of `ops/alerts.yaml`, turned into the provisioning file Grafana's
own alerting engine reads
(`ops/grafana/provisioning/alerting/repose-rules.yaml`). 54 alerts in, 53
out: `Watchdog` is left behind on purpose, see EXCLUDED.

Why a generator and not 53 hand-written Grafana rules: a Grafana rule is 40
lines of query, reduce and threshold nodes of which the name, the expression
and one number matter, and the alert itself is already written down once in
`ops/alerts.yaml` -- which promtool parses, which `promtool test rules`
exercises per rule, and whose names `ops/check.sh` matches against the
headings of `docs/ops/RUNBOOK.md`. Two hand-kept copies would drift, and the
copy that drifts is the one that pages. So `ops/alerts.yaml` stays the single
source of truth and this file is the only thing that knows Grafana's shape.
The YAML next to it is generated and committed, because Grafana's
provisioning reads files, not scripts, and `ops/check.sh` fails if the two
disagree -- the same arrangement as `ops/dashboards/gen.py` and as the
generated protobuf stubs of DECISIONS I-38.

    python3 ops/grafana/alerts-gen.py          # rewrite the YAML
    python3 ops/grafana/alerts-gen.py --check  # fail if it is out of date

Rules, and nothing else. No contact point and no notification policy are
generated or pushed: with no `policies:` of ours, Grafana's own default
policy sends every alert to whatever default contact point that Grafana has,
which the owner sets up in the UI as email, Google Chat or anything else
(DECISIONS I-944). The `severity` labels are still on every rule, so a route
can be added later by hand, but routing is not this repository's business.

How a Prometheus rule becomes a Grafana rule (DECISIONS I-942)
--------------------------------------------------------------
Prometheus needs no condition: a rule fires for every series its expression
returns. Grafana needs one named node that comes out non-zero. Each rule
therefore becomes three nodes:

    A  the Prometheus query, instant
    B  reduce(last, A)        -- the number the annotations quote
    C  threshold(B)           -- the condition

and `condition: C`.

What goes in A depends on the shape of the expression:

* A *threshold* expression -- anything ending in a top-level comparison
  against a number, which is 48 of the 53 -- is split. `a / b > 0.8` becomes
  A = `a / b` and C = `is above 0.8`. Splitting, rather than passing the
  whole expression and asking for "above 0", is not tidiness: the value of
  `up{job="hostd"} == 0` *is* zero, so "above 0" would never fire for the
  eight `== 0` rules, and `free / total < 0.1` is zero exactly when the pool
  is completely full. Splitting also puts the measured quantity in B, which
  is what Prometheus's `$value` was, so the annotations keep their numbers.
* A *boolean* expression -- one whose top level is `and`, `or` or `unless`,
  or which has no comparison at all -- cannot be split. The whole expression
  goes in A and C is "above 0". That is only sound where the expression's
  firing value is always above zero, which is true of all six but is not
  something a generator can prove, so they are named in BOOLEAN_EXPRS with
  the reason. A new rule of that shape fails this generator rather than
  getting a condition that silently never fires. Five of them reach Grafana;
  the sixth was `Watchdog`.

`noDataState: OK` matches Prometheus, where a rule whose series are absent
does not fire -- absence has its own rules, `ApiHostsAbsent` and
`HostNotInTargets`.

`execErrState` is `OK` on every rule but one. `Error` on all of them would
send a DatasourceError notification per rule on every Prometheus restart, 53
for one planned event; `OK` on all of them is worse, because a datasource
that answers nothing then leaves 53 rules green and silent, which is measured
behaviour and not a guess. So exactly one rule carries `Error` --
EXEC_ERR_ALERTING, and `ops/grafana/verify.py` fails if it does not.
"""

from __future__ import annotations

import argparse
import ast
import json
import pathlib
import re
import sys

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent.parent
SOURCE = ROOT / "ops" / "alerts.yaml"
OUT = HERE / "provisioning" / "alerting" / "repose-rules.yaml"

# The uid of ops/grafana/provisioning/datasources/repose.yaml. Fixed: every
# dashboard panel and now every alert rule names it.
PROM_UID = "repose-prometheus"

# The folder the rules go in, by TITLE: the alerting provisioning file has no
# field for a folder uid, and Grafana 12.4 refuses to start without `folder`
# (checked: "rule group has no folder set").
#
# Not `repose`, which is the dashboards' folder, because Grafana provisions
# alerting BEFORE dashboards. On a Grafana with no state yet -- the owner's,
# the first time -- the alert rules are loaded while the dashboards' folder
# does not exist, so matching by title finds nothing and Grafana makes a
# second folder, also titled `repose`, with a uid of its own. That leaves two
# folders with one name, the alerts in one and the dashboards in the other,
# and a restart does not heal it. A title of its own is unambiguous whichever
# order they load in. It is also the `grafana_folder` label on every alert.
FOLDER = "repose alerts"

# Prometheus adds these to every series it sends (ops/prometheus/prometheus.yml
# `external_labels`), and the route matches on `stack`. A Grafana-managed rule
# is evaluated by Grafana, not by Prometheus, so nothing adds them for us.
EXTERNAL_LABELS = {"stack": "repose"}

# Prometheus comparison operator -> Grafana threshold evaluator. All six are
# evaluated correctly by Grafana 12.4 (checked against
# /api/v1/rule/test/grafana, which returns the condition's own value).
EVALUATOR = {">": "gt", ">=": "gte", "<": "lt", "<=": "lte", "==": "eq", "!=": "ne"}

# Expressions that cannot be split into a query and a threshold, with the
# reason their firing value is always above zero. Keep this list honest: it is
# the only thing standing between a new rule of this shape and a condition
# that never fires.
BOOLEAN_EXPRS = {
    "ApiHostsAbsent": "absent() returns 1",
    "ApiMetricsAbsent": "absent() returns 1",
    "BuildQueueStuck": "the value is repose_host_builds_running, and the rule needs it >= 2",
    "BootSlow": "the value is the slow-boot share, and the rule needs it > 0.5",
    "HostNotInTargets": "the value is the ready-state gauge, which is 1",
    "NotifyFailing": "both sides of the `or` are counts the rule needs above 0",
}

# The one rule that is told to shout when its query cannot run at all.
#
# `execErrState` decides what a rule does when Grafana cannot evaluate it --
# a Prometheus that is down, a datasource pointing somewhere that answers
# nothing. `OK`, which every other rule here uses, means the rule reads
# Normal and its health stays "ok": measured on Grafana 12.4, a datasource
# returning "connection refused" on every query left all 53 rules green and
# silent, with nothing in the API to distinguish them from a quiet fleet.
# That is the failure this whole arrangement exists to prevent, so one rule
# does not take `OK`.
#
# `Error` on all 53 instead would send one DatasourceError notification per
# rule every time Prometheus restarts. One is the right number, and
# MonitoringPathDown is the one, because it is already the single alert for
# "the monitoring path is broken" (DECISIONS I-800): Grafana being unable to
# query Prometheus is that same failure, one hop further back. It is also
# what is left of the dead-man's switch after I-944 dropped the heartbeat --
# narrower, since it needs Grafana itself to be alive, but it is inside the
# system rather than resting on a watcher that was not.
EXEC_ERR_ALERTING = {
    "MonitoringPathDown": "the one alert for a broken monitoring path, so it is the "
                          "one that must not go quiet when the query itself fails",
}

# Alerts of ops/alerts.yaml that Grafana does not get, with the reason.
#
# `Watchdog` is a dead-man's switch: it always fires, and the point of it is
# the Uptime Kuma push monitor going red when the pings stop. Kuma has to be
# somewhere the failure it watches for cannot reach it, and the owner's runs
# on the same server as Grafana and Prometheus, so the one outage it exists
# to catch would take Kuma with it. Rather than ship a heartbeat that proves
# nothing, there is none: no rule here, no webhook contact point, no route
# (DECISIONS I-944). `ops/alerts.yaml` keeps the rule anyway, as the written
# record of a design that is deliberately not deployed: if a watcher ever
# lives somewhere this server's failure cannot reach, the expression and its
# runbook entry are already there. That is the only reason it is still in
# that file; the Alertmanager definition it used to share the file with is
# deleted (I-956).
EXCLUDED = {
    "Watchdog": "a heartbeat is only worth having where the watcher is "
                "somewhere else, and the owner's Uptime Kuma is on this server",
}

# Annotation templates are Go templates in both systems, but the value is
# reached differently. Prometheus binds `$value` to the one number the rule
# computed; Grafana binds `$values` to every node of the pipeline, keyed by
# refId, so the number lives at `$values.B` -- B being the reduce node above.
# `$values.B` renders the number on its own; a function has to be given the
# float, `$values.B.Value`. `$labels` is the same in both.
#
# The `if` is not decoration. Grafana expands these on an evaluation that
# returned nothing as well, where `$values` has no B at all: `{{ $values.B }}`
# then renders the words "[no value]" into the sentence and
# `{{ $values.B.Value | humanizePercentage }}` fails to expand, which leaves
# its own source text in the annotation and an error in Grafana's log every
# minute. Guarded, it renders nothing, which is what an alert with no number
# should say. (`$values.B` is a struct, so a value of 0 still passes the
# `if` -- checked on a running Grafana, both ways.)
VALUE_REWRITES = [
    (
        re.compile(r"\{\{ \$value \| ([^}]+?) \}\}"),
        r"{{ if $values.B }}{{ $values.B.Value | \1 }}{{ end }}",
    ),
    (re.compile(r"\{\{ \$value \}\}"), "{{ if $values.B }}{{ $values.B }}{{ end }}"),
]


# --- reading ops/alerts.yaml ----------------------------------------------
#
# A deliberately small YAML reader, because the dev shell's python3 is the
# plain interpreter (nix/flake.nix) and CI runs ops/check.sh on a runner with
# no PyYAML either. It understands the shape a Prometheus rules file has --
# nested mappings, one sequence per level, `|` block scalars, quoted and plain
# scalars -- and raises on anything else rather than guessing. A rules file
# that grows a construct it does not know fails loudly here.


class YamlError(Exception):
    pass


def _scalar(text: str, where: int) -> str:
    text = text.strip()
    if text.startswith('"'):
        if not text.endswith('"') or len(text) < 2:
            raise YamlError(f"line {where}: unterminated double-quoted scalar")
        # A double-quoted YAML scalar escapes the way a JSON string does, for
        # everything these annotations use (\" and \\).
        return json.loads(text)
    if text.startswith("'"):
        if not text.endswith("'") or len(text) < 2:
            raise YamlError(f"line {where}: unterminated single-quoted scalar")
        return text[1:-1].replace("''", "'")
    if text.startswith("[") and text.endswith("]"):
        # A flow sequence of scalars, which is how the notification policy
        # writes `group_by` and its matchers. Nothing nests inside one here.
        inner = text[1:-1].strip()
        if not inner:
            return []
        if "[" in inner or "{" in inner:
            raise YamlError(f"line {where}: a nested flow sequence is not read here")
        return [_scalar(part, where) for part in inner.split(",")]
    if text.startswith(("{", "&", "*", "!", ">")):
        raise YamlError(f"line {where}: {text[0]!r} is not a construct this reader knows")
    # A plain scalar carries its type, because ops/grafana/push.py reads the
    # datasource file with this same reader and sends it as JSON, where
    # `isDefault: "true"` and `maxOpenConns: "4"` are the wrong types. Nothing
    # in ops/alerts.yaml is affected: `5m`, `warn` and `1m` are not numbers.
    if text in ("true", "false"):
        return text == "true"
    if text in ("null", "~"):
        return None
    if re.fullmatch(r"-?\d+", text):
        return int(text)
    if re.fullmatch(r"-?(\d+\.\d*|\.\d+)([eE][-+]?\d+)?|-?\d+[eE][-+]?\d+", text):
        return float(text)
    return text


def _parse(lines: list[tuple[int, str]], start: int, indent: int):
    """Parse one block at `indent`. Returns (value, next index)."""
    i = start
    out = None
    while i < len(lines):
        no, raw = lines[i]
        if not raw.strip() or raw.lstrip().startswith("#"):
            i += 1
            continue
        col = len(raw) - len(raw.lstrip())
        if col < indent:
            break
        if col > indent:
            raise YamlError(f"line {no}: unexpected indent")
        body = raw.strip()
        if not body or body.startswith("#"):
            i += 1
            continue
        if body.startswith("- "):
            if out is None:
                out = []
            if not isinstance(out, list):
                raise YamlError(f"line {no}: sequence item inside a mapping")
            rest = body[2:]
            if not re.match(r"[A-Za-z_][A-Za-z0-9_]*:(\s|$)", rest):
                # A sequence of scalars, `- [stack, =, repose]` included.
                out.append(_scalar(rest, no))
                i += 1
                continue
            # `- key: value` opens a mapping whose first key sits at col + 2.
            lines[i] = (no, " " * (col + 2) + rest)
            item, i = _parse(lines, i, col + 2)
            out.append(item)
            continue
        if out is None:
            out = {}
        if not isinstance(out, dict):
            raise YamlError(f"line {no}: mapping key inside a sequence")
        m = re.match(r"([A-Za-z_][A-Za-z0-9_]*):(?:\s+(.*))?$", body)
        if not m:
            raise YamlError(f"line {no}: not `key:` or `key: value`: {body!r}")
        key, val = m.group(1), m.group(2)
        if val is None:
            out[key], i = _parse(lines, i + 1, _next_indent(lines, i + 1, col, no))
        elif val == "|":
            out[key], i = _block_scalar(lines, i + 1, col)
        else:
            out[key] = _scalar(val, no)
            i += 1
    if out is None:
        raise YamlError(f"line {lines[start][0] if start < len(lines) else '?'}: empty block")
    return out, i


def _next_indent(lines, i, parent_col, no) -> int:
    while i < len(lines) and (not lines[i][1].strip() or lines[i][1].lstrip().startswith("#")):
        i += 1
    if i >= len(lines):
        raise YamlError(f"line {no}: key with nothing under it")
    col = len(lines[i][1]) - len(lines[i][1].lstrip())
    if col <= parent_col:
        raise YamlError(f"line {no}: key with nothing under it")
    return col


def _block_scalar(lines, i, parent_col) -> tuple[str, int]:
    """A literal `|` block: newlines kept, which is what `|` means.

    They are kept because one of these is an email body, where the blank
    lines between the summary, the description and the runbook are the
    message's shape. A PromQL expression broken over lines is folded back to
    one by its own caller, not here."""
    body = []
    while i < len(lines):
        no, raw = lines[i]
        if raw.strip():
            col = len(raw) - len(raw.lstrip())
            if col <= parent_col:
                break
        body.append(raw)
        i += 1
    while body and not body[-1].strip():
        body.pop()
    if not body:
        raise YamlError("empty block scalar")
    indent = min(len(l) - len(l.lstrip()) for l in body if l.strip())
    return "\n".join(l[indent:] if l.strip() else "" for l in body) + "\n", i


def read_yaml(path: pathlib.Path) -> dict:
    """One YAML document of the subset above. `ops/grafana/push.py` reads the
    datasource and notification files with this too, so there is one parser in
    ops/grafana and one place where an unknown construct is refused."""
    lines = []
    for n, raw in enumerate(path.read_text().splitlines(), 1):
        if raw.rstrip() != raw:
            raise YamlError(f"{path.name} line {n}: trailing whitespace")
        # Blank and comment lines are kept, because inside a `|` block they
        # are content: dropping them turned an email body into one line.
        # _parse skips them everywhere else.
        lines.append((n, raw))
    doc, end = _parse(lines, 0, 0)
    if end != len(lines):
        raise YamlError(f"{path.name} line {lines[end][0]}: trailing content")
    return doc


def read_source(path: pathlib.Path) -> list[dict]:
    doc = read_yaml(path)
    if set(doc) != {"groups"}:
        raise YamlError(f"expected one top-level key `groups`, got {sorted(doc)}")
    return doc["groups"]


# --- splitting the expression ---------------------------------------------


def _top_level(expr: str):
    """Yield (position, token) for every operator at bracket depth 0."""
    depth = 0
    quote = None
    i = 0
    while i < len(expr):
        c = expr[i]
        if quote:
            if c == "\\":
                i += 2
                continue
            if c == quote:
                quote = None
            i += 1
            continue
        if c in "\"'`":
            quote = c
            i += 1
            continue
        if c in "([{":
            depth += 1
        elif c in ")]}":
            depth -= 1
            if depth < 0:
                raise YamlError(f"unbalanced brackets in {expr!r}")
        elif depth == 0:
            two = expr[i : i + 2]
            if two in ("==", "!=", ">=", "<="):
                yield i, two
                i += 2
                continue
            if c in "<>":
                yield i, c
            else:
                m = re.match(r"\b(and|or|unless)\b", expr[i:])
                if m and (i == 0 or not (expr[i - 1].isalnum() or expr[i - 1] == "_")):
                    yield i, m.group(1)
                    i += len(m.group(1))
                    continue
        i += 1
    if depth or quote:
        raise YamlError(f"unbalanced brackets or quotes in {expr!r}")


def _number(text: str):
    """A right-hand side that is arithmetic over literals, as a float.

    `repose_api_snapshot_age_seconds > 36 * 3600` is a threshold of 129600;
    Grafana's threshold takes a number, not an expression.
    """
    if not re.fullmatch(r"[0-9eE+\-*/.() ]+", text.strip()):
        return None
    try:
        tree = ast.parse(text.strip(), mode="eval").body
    except SyntaxError:
        return None

    def walk(node):
        if isinstance(node, ast.Constant) and isinstance(node.value, (int, float)):
            return float(node.value)
        if isinstance(node, ast.UnaryOp) and isinstance(node.op, (ast.UAdd, ast.USub)):
            v = walk(node.operand)
            return v if isinstance(node.op, ast.UAdd) else -v
        if isinstance(node, ast.BinOp) and isinstance(
            node.op, (ast.Add, ast.Sub, ast.Mult, ast.Div)
        ):
            a, b = walk(node.left), walk(node.right)
            return {ast.Add: a + b, ast.Sub: a - b, ast.Mult: a * b, ast.Div: a / b}[
                type(node.op)
            ]
        raise ValueError("not arithmetic over literals")

    try:
        return walk(tree)
    except (ValueError, ZeroDivisionError):
        return None


def split(name: str, expr: str) -> tuple[str, str, float]:
    """(query, threshold evaluator, threshold) for one Prometheus expression."""
    # An expression broken over several lines for reading is one expression.
    expr = " ".join(expr.split())
    tokens = list(_top_level(expr))
    logical = [t for _, t in tokens if t in ("and", "or", "unless")]
    comparisons = [(p, t) for p, t in tokens if t in EVALUATOR]
    if not logical and len(comparisons) == 1:
        pos, op = comparisons[0]
        rhs = _number(expr[pos + len(op) :])
        if rhs is not None:
            if name in BOOLEAN_EXPRS:
                raise YamlError(
                    f"{name} splits into a query and a threshold, so take it out of "
                    "BOOLEAN_EXPRS"
                )
            return expr[:pos].strip(), EVALUATOR[op], rhs
    if name not in BOOLEAN_EXPRS:
        raise YamlError(
            f"{name}: this expression has no top-level comparison against a number, so "
            "the whole of it becomes the query and the condition becomes `above 0`. "
            "That is only right if the expression's firing value is always above zero "
            "-- `up == 0` fires with the value 0 and would never alert. Check it, then "
            f"add {name!r} to BOOLEAN_EXPRS with the reason."
        )
    return expr, "gt", 0.0


# --- the Grafana rule -----------------------------------------------------

_UNIT = {"s": 1, "m": 60, "h": 3600, "d": 86400, "w": 604800}


def lookback(expr: str) -> int:
    """Seconds the rule's data reaches back, from its longest range selector.

    An instant query is evaluated at the end of the window, so this only has
    to cover the ranges inside the expression -- it is what the rule's own
    panel in Grafana shows, and what a `[6h]` increase needs to be there.
    """
    longest = 0
    for n, unit in re.findall(r"\[(\d+)([smhdw])\]", expr):
        longest = max(longest, int(n) * _UNIT[unit])
    return max(600, longest)


def uid(name: str) -> str:
    """A uid fixed by the rule's name.

    Grafana keeps a rule's state, its silences and its annotation history
    against the uid, so a uid that moved would read as a new rule with no
    history. Derived from the name, which `ops/check.sh` already holds fixed
    against the RUNBOOK headings, and lowercased so it reads like the
    runbook anchor.
    """
    out = "repose-" + re.sub(r"[^a-z0-9]+", "-", name.lower())
    if len(out) > 40:
        raise YamlError(f"{name}: uid {out!r} is over Grafana's 40 characters")
    return out


def annotations(rule: dict) -> dict:
    out = {}
    for key in ("summary", "description", "runbook"):
        text = rule["annotations"][key]
        for pattern, repl in VALUE_REWRITES:
            text = pattern.sub(repl, text)
        # Grafana leaves an unknown template alone, so a `$value` that got
        # past the rewrites would reach the owner's inbox as its own source.
        if re.search(r"\$value\b", text):
            raise YamlError(
                f"{rule['alert']}: {key} still uses $value, which Grafana does not bind "
                f"the way Prometheus does: {text!r}"
            )
        out[key] = text
    return out


def grafana_rule(rule: dict) -> dict:
    name = rule["alert"]
    query, evaluator, threshold = split(name, rule["expr"])
    window = {"from": lookback(rule["expr"]), "to": 0}
    expr_ds = {"type": "__expr__", "uid": "__expr__"}
    return {
        "uid": uid(name),
        "title": name,
        "condition": "C",
        "for": rule.get("for", "0m"),
        "noDataState": "OK",
        "execErrState": "Error" if name in EXEC_ERR_ALERTING else "OK",
        "isPaused": False,
        "labels": {**rule.get("labels", {}), **EXTERNAL_LABELS},
        "annotations": annotations(rule),
        "data": [
            {
                "refId": "A",
                "relativeTimeRange": window,
                "datasourceUid": PROM_UID,
                "model": {
                    "refId": "A",
                    "datasource": {"type": "prometheus", "uid": PROM_UID},
                    "editorMode": "code",
                    "expr": query,
                    "instant": True,
                    "range": False,
                    "intervalMs": 1000,
                    "maxDataPoints": 43200,
                },
            },
            {
                "refId": "B",
                "relativeTimeRange": window,
                "datasourceUid": "__expr__",
                "model": {
                    "refId": "B",
                    "datasource": expr_ds,
                    "type": "reduce",
                    "reducer": "last",
                    "expression": "A",
                    # A series with no number is not a series Prometheus would
                    # have returned, so it is dropped rather than compared.
                    "settings": {"mode": "dropNN"},
                },
            },
            {
                "refId": "C",
                "relativeTimeRange": window,
                "datasourceUid": "__expr__",
                "model": {
                    "refId": "C",
                    "datasource": expr_ds,
                    "type": "threshold",
                    "expression": "B",
                    "conditions": [
                        {
                            "type": "query",
                            "evaluator": {"type": evaluator, "params": [threshold]},
                            "operator": {"type": "and"},
                            "query": {"params": ["B"]},
                            "reducer": {"type": "last", "params": []},
                        }
                    ],
                },
            },
        ],
    }


# --- emitting -------------------------------------------------------------

HEADER = """\
# Grafana-managed alert rules for repose. GENERATED -- do not edit.
#
#   python3 ops/grafana/alerts-gen.py
#
# The source is ops/alerts.yaml, which promtool parses and `promtool test
# rules ops/alerts_test.yaml` exercises rule by rule. Change an alert there
# and re-run the generator; `ops/check.sh` fails if this file is stale
# (DECISIONS I-942).
#
# NOT MOUNTED. This is the readable definition; ops/grafana/push.py puts it
# into a Grafana over the HTTP API (DECISIONS I-944). The shape is still
# Grafana's provisioning format, so a Grafana that does mount it loads the
# same rules.
#
# Each rule is a Prometheus query (A), a reduce to one number (B) and a
# threshold (C), because Grafana needs a named condition where Prometheus
# needs none. The number the annotations quote is B, so `{{ $values.B }}` is
# what Prometheus wrote as `{{ $value }}`.
#
# Who gets told is not here and is not generated: Grafana's own default
# notification policy sends these to its default contact point, which the
# owner sets up in the UI. The `severity` label is on every rule for whoever
# wants to add a route.
#
# 53 rules for 54 alerts: `Watchdog` is excluded (EXCLUDED in the generator).
"""

_BARE = re.compile(r"[A-Za-z0-9_.:/@-]+")
_RESERVED = {"true", "false", "null", "yes", "no", "on", "off", "~", ""}


def scalar(value) -> str:
    if value is True:
        return "true"
    if value is False:
        return "false"
    if value is None:
        return "null"
    if isinstance(value, float):
        return str(int(value)) if value.is_integer() else repr(value)
    if isinstance(value, int):
        return str(value)
    if isinstance(value, str):
        if _BARE.fullmatch(value) and value.lower() not in _RESERVED and not _number(value):
            return value
        return json.dumps(value)
    raise YamlError(f"cannot emit {value!r}")


def emit(value, indent: int = 0) -> list[str]:
    pad = "  " * indent
    lines = []
    if isinstance(value, dict):
        for key, inner in value.items():
            if isinstance(inner, (dict, list)) and inner:
                lines.append(f"{pad}{key}:")
                lines += emit(inner, indent + 1)
            elif isinstance(inner, (dict, list)):
                lines.append(f"{pad}{key}: {'{}' if isinstance(inner, dict) else '[]'}")
            else:
                lines.append(f"{pad}{key}: {scalar(inner)}")
    elif isinstance(value, list):
        for item in value:
            if isinstance(item, dict):
                body = emit(item, indent + 1)
                lines.append(f"{pad}- {body[0].lstrip()}")
                lines += body[1:]
            elif isinstance(item, list):
                raise YamlError("a sequence of sequences is not emitted")
            else:
                lines.append(f"{pad}- {scalar(item)}")
    else:
        raise YamlError(f"cannot emit {value!r} at the top of a block")
    return lines


def groups() -> list[dict]:
    """Every rule group, in the provisioning file's own shape.

    `ops/grafana/push.py` and `ops/grafana/verify.py` call this rather than
    re-reading the YAML, so there is one definition of a Grafana rule in this
    repository and the pusher cannot drift from the generator. They check the
    committed file against `render()` before trusting either.
    """
    out = []
    seen = {}
    names = set()
    for group in read_source(SOURCE):
        rules = []
        for rule in group["rules"]:
            names.add(rule["alert"])
            if rule["alert"] in EXCLUDED:
                continue
            built = grafana_rule(rule)
            if built["uid"] in seen:
                raise YamlError(f"{rule['alert']} and {seen[built['uid']]} share a uid")
            seen[built["uid"]] = rule["alert"]
            rules.append(built)
        # A group whose every rule is excluded is left out rather than emitted
        # empty: `repose-meta` held only Watchdog.
        if rules:
            out.append(
                {
                    "orgId": 1,
                    "name": group["name"],
                    "folder": FOLDER,
                    "interval": group["interval"],
                    "rules": rules,
                }
            )
    for name in sorted(set(EXCLUDED) | set(BOOLEAN_EXPRS) | set(EXEC_ERR_ALERTING)):
        if name not in names:
            raise YamlError(
                f"{name} is named in this generator and is not an alert of "
                "ops/alerts.yaml any more; take it out"
            )
    return out


def render() -> str:
    return HEADER + "\n".join(emit({"apiVersion": 1, "groups": groups()})) + "\n"


def stale() -> str | None:
    """The reason the committed file cannot be trusted, or None."""
    if not OUT.exists():
        return f"{OUT.relative_to(ROOT)} does not exist"
    if OUT.read_text() != render():
        return (
            f"{OUT.relative_to(ROOT)} is not what ops/alerts.yaml produces; "
            "run python3 ops/grafana/alerts-gen.py"
        )
    return None


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--check", action="store_true", help="fail if the committed YAML is out of date")
    args = ap.parse_args()
    try:
        want = render()
    except YamlError as exc:
        print(f"ops/grafana/alerts-gen.py: {exc}", file=sys.stderr)
        return 1
    if args.check:
        why = stale()
        if why:
            print(why, file=sys.stderr)
            return 1
        rules = want.count("\n      - uid: ")
        print(f"up to date, {rules} rules")
        return 0
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(want)
    print(f"wrote {OUT.relative_to(ROOT)} ({want.count(chr(10) + '      - uid: ')} rules)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
