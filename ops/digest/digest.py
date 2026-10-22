#!/usr/bin/env python3
"""The launch digest: one email a day with the numbers that say whether
the launch is working (DECISIONS I-810, delivery amended by I-848).

Why this and not alerts: the launch numbers are not alert-shaped. Seat
fill, MRR, trial conversion and activation are things to read once a day
and act on over a week, and an alert that fires on them either repeats
every 12 hours or never clears. Prometheus annotations cannot carry them
either -- a rule's template sees only its own `$value`, so one alert can
carry exactly one number (ops/alerts.yaml).

Why on the personal server: it is the one place that can reach both
Prometheus and Postgres. It emails through the same SMTP credentials
Alertmanager uses, so it adds no secret and no service.

    ops/digest/digest.py --dry-run     print the message, send nothing
    ops/digest/digest.py               send it

Environment:
    PROM_URL            default http://127.0.0.1:9090
    DATABASE_URL        the api's Postgres, as a read-only role
    SMTP_HOST           default smtp.resend.com:587
    SMTP_USER           default resend
    SMTP_PASSWORD_FILE  the file holding the SMTP password
    DIGEST_FROM         default repose alerts <alerts@repose.herakraft.co>
    DIGEST_TO           where to send it

Every number is optional. A source that is down prints `?` for its own
numbers and the rest of the message still goes: a digest that fails
whenever one exporter is down is a digest nobody trusts. The exit code is
0 whenever the message was sent.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import smtplib
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from email.message import EmailMessage

TIMEOUT = 10


def prom(expr: str) -> float | None:
    """One instant query. None when Prometheus or the series is absent."""
    url = os.environ.get("PROM_URL", "http://127.0.0.1:9090").rstrip("/")
    q = urllib.parse.urlencode({"query": expr})
    try:
        with urllib.request.urlopen(f"{url}/api/v1/query?{q}", timeout=TIMEOUT) as r:
            body = json.load(r)
    except (urllib.error.URLError, OSError, ValueError, TimeoutError):
        return None
    if body.get("status") != "success":
        return None
    result = body.get("data", {}).get("result", [])
    if not result:
        return None
    try:
        return float(result[0]["value"][1])
    except (KeyError, IndexError, TypeError, ValueError):
        return None


def row(sql: str) -> list[str] | None:
    """One single-row query against Postgres, through psql.

    Through psql and not a driver on purpose: this runs on the personal
    server beside Prometheus and calert, and a digest that needs a Python
    package installed there is a digest that stops working after the next
    rebuild. psql is already how every runbook step reads this database.
    None when psql is absent or the query failed.
    """
    dsn = os.environ.get("DATABASE_URL")
    if not dsn or not shutil.which("psql"):
        return None
    try:
        out = subprocess.run(
            ["psql", dsn, "-tAF|", "-v", "ON_ERROR_STOP=1", "-c", sql],
            capture_output=True, text=True, timeout=TIMEOUT, check=True,
        )
    except (subprocess.SubprocessError, OSError):
        return None
    line = out.stdout.strip().splitlines()
    return line[0].split("|") if line else None


def n(v: float | None, fmt: str = "{:.0f}") -> str:
    return "?" if v is None else fmt.format(v)


def pct(num: float | None, den: float | None) -> str:
    if num is None or den in (None, 0):
        return "?"
    return f"{100.0 * num / den:.0f}%"


RUNNING_GB = (
    '((4 * sum(repose_host_guests{state="running",class="small"})) or vector(0))'
    ' + ((8 * sum(repose_host_guests{state="running",class="large"})) or vector(0))'
    ' + ((16 * sum(repose_host_guests{state="running",class="xl"})) or vector(0))'
)


def message() -> str:
    seats_total = prom("max(repose_api_seats_total)")
    seats_held = prom("max(repose_api_seats_held)")
    running_gb = prom(RUNNING_GB)
    mrr = prom('sum(max by (status) (repose_api_mrr_cents{status=~"active|past_due"}))')
    trial_mrr = prom('max(repose_api_mrr_cents{status="trialing"})')
    waiting = prom("max(repose_api_waitlist_waiting)")
    refused = prom("sum(increase(repose_api_schedule_total"
                   '{result=~"no_capacity|no_cpu"}[24h]))')
    gate = prom('sum(increase(repose_api_billing_gate_refused_total{reason="plan_limit"}[24h]))')

    # The two questions Prometheus cannot answer, because neither may be
    # labelled by user (internal/obs/metrics refuses it): who is paying,
    # and whether an agent ran for them with nobody watching.
    funnel = row("""select
        count(*) filter (where status = 'trialing'),
        count(*) filter (where status in ('active','past_due')),
        count(*) filter (where status = 'canceled' and cancel_at >= now() - interval '24 hours')
      from subscriptions""")
    activation = row("""select
        coalesce(round(sum(unattended_agent_seconds) / 3600.0, 1), 0),
        count(distinct project_id) filter (where unattended_agent_seconds > 0)
      from usage_hours where hour >= now() - interval '24 hours'""")

    trialing, paying, lost = (funnel + ["?"] * 3)[:3] if funnel else ("?", "?", "?")
    agent_hours, agent_projects = (activation + ["?"] * 2)[:2] if activation else ("?", "?")

    running_seats = None if running_gb is None else running_gb / 8.0
    lines = [
        "repose, last 24 hours",
        f"seats {n(seats_held)}/{n(seats_total)} sold ({pct(seats_held, seats_total)} fill), "
        f"{n(running_seats, '{:.1f}')} running ({pct(running_gb, None if seats_held is None else seats_held * 8)} of sold)",
        f"MRR ${n(None if mrr is None else mrr / 100)}, in trial ${n(None if trial_mrr is None else trial_mrr / 100)}",
        f"subscriptions {paying} paying, {trialing} in trial, {lost} cancelled yesterday",
        f"agents alone {agent_hours} h across {agent_projects} machine(s)",
        f"refused: {n(refused)} placement(s) for capacity, {n(gate)} start(s) at the plan limit",
        f"waitlist {n(waiting)} waiting",
    ]
    return "\n".join(lines)


def send(subject: str, body: str) -> str | None:
    """Email the digest. Returns an error string, or None on success."""
    to = os.environ.get("DIGEST_TO")
    path = os.environ.get("SMTP_PASSWORD_FILE")
    if not to or not path:
        return "set DIGEST_TO and SMTP_PASSWORD_FILE"
    try:
        with open(path) as f:
            password = f.read().strip()
    except OSError as e:
        return f"{path}: {e}"
    msg = EmailMessage()
    msg["Subject"] = subject
    msg["From"] = os.environ.get("DIGEST_FROM", "repose alerts <alerts@repose.herakraft.co>")
    msg["To"] = to
    msg.set_content(body)
    host, _, port = os.environ.get("SMTP_HOST", "smtp.resend.com:587").partition(":")
    try:
        with smtplib.SMTP(host, int(port or 587), timeout=TIMEOUT) as smtp:
            smtp.starttls()
            smtp.login(os.environ.get("SMTP_USER", "resend"), password)
            smtp.send_message(msg)
    except (smtplib.SMTPException, OSError, ValueError) as e:
        return str(e)
    return None


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--dry-run", action="store_true", help="print the message, send nothing")
    args = ap.parse_args()
    text = message()
    if args.dry_run:
        print(text)
        return 0
    if err := send("repose: the last 24 hours", text):
        print(f"digest: {err}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
