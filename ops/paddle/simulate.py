#!/usr/bin/env python3
"""Sends one simulated Paddle event about a real sandbox subscription to the
api's webhook destination (docs/ops/M4-GATE.md step 3, DECISIONS I-600).

    PADDLE_API_KEY=pdl_sdbx_... ops/paddle/simulate.py activated sub_...
    PADDLE_API_KEY=pdl_sdbx_... ops/paddle/simulate.py completed sub_...
    PADDLE_API_KEY=pdl_sdbx_... ops/paddle/simulate.py failed    sub_...

activated is subscription.activated (a trial's end); completed is
transaction.completed for a renewal; failed is transaction.payment_failed.
The payload is the subscription, or its newest transaction, as Paddle has it
now, so the ids, customer and custom_data are the account's own and the api
finds it. Paddle's own state does not change: the next real event about the
subscription (a charge, a cancel) carries its real status again.

The destination must accept simulations (traffic_source all), which
paddle-bootstrap sets in the sandbox. A live key is refused. The key is read
from the environment, never from the command line.
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

BASE = "https://sandbox-api.paddle.com"
HOOK = "/v1/billing/webhook"


def call(key, method, path, body=None):
    req = urllib.request.Request(
        BASE + path,
        method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req) as r:
            return json.load(r)["data"]
    except urllib.error.HTTPError as e:
        sys.exit(f"paddle: {method} {path}: {e.code} {e.read().decode()[:400]}")


def main():
    if len(sys.argv) != 3 or sys.argv[1] not in ("activated", "completed", "failed"):
        sys.exit("usage: simulate.py activated|completed|failed SUBSCRIPTION_ID")
    kind, sub_id = sys.argv[1], sys.argv[2]
    key = os.environ.get("PADDLE_API_KEY", "")
    if not key.startswith("pdl_sdbx_"):
        sys.exit("PADDLE_API_KEY is not a sandbox key (pdl_sdbx_...)")
    dests = [n for n in call(key, "GET", "/notification-settings") if n["destination"].endswith(HOOK)]
    if not dests:
        sys.exit("no notification destination ends with " + HOOK + "; run paddle-bootstrap")
    if kind == "activated":
        payload = call(key, "GET", "/subscriptions/" + sub_id)
        payload["status"] = "active"
        event = "subscription.activated"
    else:
        txns = call(key, "GET", "/transactions?subscription_id=" + sub_id + "&order_by=created_at[DESC]")
        if not txns:
            sys.exit("the subscription has no transaction to base the event on")
        payload = txns[0]
        payload["origin"] = "subscription_recurring"
        payload["status"] = "completed" if kind == "completed" else "past_due"
        event = "transaction.completed" if kind == "completed" else "transaction.payment_failed"
    sim = call(key, "POST", "/simulations", {
        "notification_setting_id": dests[0]["id"], "name": "repose " + kind, "type": event, "payload": payload,
    })
    run = call(key, "POST", f"/simulations/{sim['id']}/runs")
    for _ in range(30):
        time.sleep(2)
        evs = call(key, "GET", f"/simulations/{sim['id']}/runs/{run['id']}/events")
        if evs and all(e["status"] != "pending" for e in evs):
            for e in evs:
                print(event, e["status"], (e.get("response") or {}).get("status_code"))
            return
    sys.exit(event + ": still pending after a minute")


if __name__ == "__main__":
    main()
