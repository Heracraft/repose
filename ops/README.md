# ops

Everything the owner's personal server needs in order to watch repose, and a
local stack that looks the same with no data in it. The design is
`docs/workstreams/10-observability.md`; how to use it in an incident is
`docs/ops/OBSERVABILITY.md` and `docs/ops/RUNBOOK.md`.

Nothing here runs on a host or on the control plane. Hosts run Fluent Bit,
node_exporter and hostd's own `/metrics` (`nix/hosts/`); this directory is the
other end of those.

| Path | What it is |
|---|---|
| `alerts.yaml` | The 54 alert rules, as Prometheus rules. The source of truth for both Prometheus and Grafana. Every name is a heading in `docs/ops/RUNBOOK.md`. |
| `alerts_test.yaml` | `promtool test rules` cases: each alert fires on the series it was written for and stays quiet just below it. |
| `grafana/alerts-gen.py` | Turns `alerts.yaml` into Grafana's own alert rules. `--check` fails if the YAML is stale (DECISIONS I-942). |
| `grafana/provisioning/alerting/repose-rules.yaml` | The 53 rules as Grafana holds them (54 alerts less `Watchdog`). Generated: edit `alerts.yaml` and re-run the generator. Not mounted anywhere. |
| `grafana/push.py` | The one command that puts all of it into a running Grafana over the HTTP API: datasources, alert rules, dashboards. `--dry-run` says what would change (DECISIONS I-944). |
| `grafana/verify.py` | The drift check: what a Grafana holds, against what the generator produces, rule by rule. Nothing reconciles at boot, so this is the only thing that notices. |
| `alertmanager/repose-route.yaml` | Kept, not deployed. The same routing as an Alertmanager configuration, and the only written record of the seven inhibition rules Grafana cannot express (DECISIONS I-648, I-848, I-942). |
| `prometheus/prometheus.yml` | Scrape config: hosts (`:9100`, `:9101`, Fluent Bit `:2021`), edge (`:9102`), api (`:9103`), all over WireGuard. |
| `prometheus/targets/hosts.yml` | The host list, re-read every minute. Adding a host is one edit here. |
| `prometheus/wireguard-peer.conf` | How the monitoring server joins the edge's WireGuard hub, and how to check it. |
| `loki/retention.yaml` | 90 days for component logs, 30 for guest console logs, with the compactor that makes it happen. |
| `grafana/provisioning/` | Datasources (three fixed uids), the dashboard provider, and the alert rules. Grafana's provisioning format, because it is the readable shape; nothing here is mounted into the owner's Grafana (DECISIONS I-944). |
| `dashboards/*.json` | The eight dashboards. Generated: edit `dashboards/gen.py` and re-run it. |
| `dashboards/gen.py` | The dashboards' source of truth. `--check` fails if the JSON is stale. |
| `dashboards/push.py` | Imports every dashboard into a real Grafana, in one folder, rewriting the dev datasource uids to the target's. Idempotent; the token comes from `GRAFANA_TOKEN`. |
| `dashboards/validate.py` | Structure, datasource uids, forbidden Prometheus labels, SQL against `db-schema.md`, and `--query` to see which panels have data. |
| `sql/partitions.sql` | The operator's copy of the partition maintenance the api does in Go: create this month and next, drop what is past retention. |
| `check.sh` | Everything above that can be checked without a host. `--grafana` also loads the dashboards into a real Grafana. |
| `dev/` | Prometheus, Loki, Grafana and Postgres in Docker, plus `seedmetrics`, `seedlogs.sh` and `pgcheck.sh`. |

## Installing it on the personal server

1. **WireGuard.** Add the monitoring server as a peer of the edge
   (`prometheus/wireguard-peer.conf`, both halves). Until this works nothing
   else can: hosts have no inbound and no public address.
2. **Prometheus.** Copy `prometheus/prometheus.yml` to
   `/etc/prometheus/prometheus.yml` and `prometheus/targets/` to
   `/etc/prometheus/repose/`, and run it with
   `--storage.tsdb.retention.time=90d`.

   **Prometheus needs no `rule_files:` and no `alerting:` block**, and the
   config here has neither (DECISIONS I-944). Grafana evaluates repose's
   alert rules itself, by querying this Prometheus, and notifies through its
   own contact point; Prometheus has nothing to evaluate and nowhere to send.
   An `alerting:` block would point at an Alertmanager this server does not
   run. `alerts.yaml` is still the source of truth -- promtool tests it and
   `grafana/alerts-gen.py` generates Grafana's rules from it -- it is simply
   not loaded here.

   Before the alert rules go in, `up{job=~"hosts|hostd|api|api-grpc|gateway"}`
   must be 1 for every target: HostdDown, ApiDown and GatewayDown are pages.
   `api` passes through Traefik's allow-list (`coolify/README.md` "The api's
   metrics").
3. **Alerting** (DECISIONS I-942, I-944). Grafana evaluates the rules and
   sends the notifications. There is no Alertmanager on this server and this
   adds none.

   Nothing is mounted. `grafana/push.py` puts the datasources, the 53 alert
   rules and the eight dashboards into a running Grafana over its HTTP API,
   and is safe to re-run:

   ```
   export GRAFANA_URL=https://grafana.example GRAFANA_TOKEN=glsa_...
   python3 ops/grafana/push.py --dry-run     # what it would change
   python3 ops/grafana/push.py
   ```

   **Those two variables are the whole list.** `GRAFANA_TOKEN` is a
   service-account token with the Admin role (Grafana: Administration ->
   Users and access -> Service accounts). There is no password to set
   anywhere: this tooling holds no secret, asks for none, and refuses for
   want of none.

   **One contact point, set up by hand in the UI. That is the whole of it,
   and without it nothing is delivered.** Two steps, both in Grafana:

   1. Alerting -> Contact points -> **Add contact point**. Email, Google
      Chat, anything Grafana supports; repose has no opinion. A fresh
      Grafana has none at all.
   2. Alerting -> Notification policies -> **Default policy** -> Edit, and
      set its contact point to the one just made. A fresh Grafana 12.4 ships
      a default policy whose receiver is `empty`, so step 1 alone delivers
      nothing.

   Grafana's default policy then carries every repose alert to it. Nothing
   here provisions a policy or a contact point, which is why this repository
   holds no address and no SMTP password: if the channel is email, Grafana's
   own `[smtp]` settings (`GF_SMTP_*` on the service) are the owner's.

   Then check it: **Test** on the contact point arrives; Alerting -> Alert
   rules lists 53 rules in a `repose alerts` folder; and
   `python3 ops/grafana/verify.py` exits 0. The alerts have a folder of their
   own rather than the dashboards' `repose`, so there is never a second
   folder of one name (I-942).

   Four things to know, none of them hidden:

   - **21 of the 83 dashboard panels will read "no data", and that is
     correct.** They are the per-account and per-project figures, which read
     the control plane's Postgres rather than Prometheus -- `project_id` and
     `user_id` are refused as metric labels, so those numbers cannot be
     Prometheus series at all (`docs/ops/OBSERVABILITY.md`). Reaching that
     database needs a password, and the push takes none, so it does not
     create that datasource. The dashboards are not broken. Two ways to get
     those figures when they are wanted: `psql "$DATABASE_URL" -f
     ops/sql/launch.sql`, which needs no datasource; or add the datasource by
     hand in Grafana (Connections -> Add new connection -> PostgreSQL) with
     the uid set to exactly `repose-postgres`, so the committed panels find
     it. `grafana/provisioning/datasources/repose.yaml` is the reference for
     what to put in it, including the read-only role to create first. Doing
     it by hand is the point: no database password passes through this
     repository's tooling, now or later.

   - **A rebuilt Grafana comes back with no alert rules until someone runs
     the push again, and no alert rules is silent.** With a mounted
     provisioning directory Grafana re-provisions itself at boot; over the
     API nothing does. Grafana's state is in Postgres
     (`GF_DATABASE_TYPE=postgres`), so recreating the container keeps the
     rules; replacing that database loses them. Nothing watches for it.
     `ops/grafana/verify.py` is the check, and it only runs when someone
     runs it.
   - **There is no heartbeat.** `Watchdog` is in `alerts.yaml` and is the one
     alert Grafana does not get. A dead-man's switch is worth having only
     where the watcher sits somewhere the failure cannot reach, and this
     server's Uptime Kuma is on this server. So a Prometheus or a Grafana
     that is wholly down says nothing; Azure's own alert email about the VM
     is what is left (RUNBOOK "Azure alert").
   - **One outage can send several notifications.** Grafana's default policy
     groups by `grafana_folder` and `alertname`, and the seven Alertmanager
     `inhibit_rules` that kept one page per outage have no equivalent in
     Grafana. A host that goes away trips HostdDown, HostUnreachable and
     MonitoringPathDown, and each is its own notification. If that becomes
     annoying it is one field in the same dialog as step 2 above: group by
     `stack` instead of `alertname`, and they arrive as one message. That is
     the owner's call, which is why it is not done here.
4. **Loki.** Merge `loki/retention.yaml`. Retention does nothing without the
   compactor, so check `curl -s localhost:3100/config | grep retention`.
5. **Grafana.** Nothing to mount: step 3's `grafana/push.py` puts the two
   datasources, the alert rules and the dashboards in over the API, and
   `grafana-data` stays the service's only volume. This Grafana evaluates the
   alert rules, so it is in the path of every notification: a Grafana that is
   down tells nobody anything, and nothing outside it notices (step 3). The
   21 Postgres-backed panels stay empty unless the owner adds that datasource
   themselves, which step 3 explains.
6. **Postgres.** Nothing to install: the api creates and drops the sample
   tables' partitions itself. `sql/partitions.sql` is the same maintenance as
   SQL functions, for an operator whose api is down (`psql -f
   sql/partitions.sql`, then `select * from repose_partitions_maintain();`).
   `sql/launch.sql` is the launch window's twelve per-account questions
   (DECISIONS I-804); run it as `grafana_ro`, the same role step 5 creates:
   `psql "$DATABASE_URL" -f sql/launch.sql`.
7. **The daily digest** (DECISIONS I-810). One Chat message at 08:00 with
   the launch numbers, from the box that can reach Prometheus and Postgres
   at once.

   ```
   install -m 0755 -D digest/digest.py /opt/repose/ops/digest/digest.py
   install -m 0644 digest/repose-digest.service digest/repose-digest.timer        /etc/systemd/system/
   printf 'DATABASE_URL=postgres://grafana_ro:...@.../repose
'        >/etc/repose-digest.env && chmod 0600 /etc/repose-digest.env
   systemctl daemon-reload && systemctl enable --now repose-digest.timer
   ```

   It needs `psql` on PATH and an SMTP password of its own in
   `SMTP_PASSWORD_FILE` (the unit reads it from a file). repose's alerting
   holds no SMTP password any more, so this is the only place one is needed. Put `DIGEST_TO` in `/etc/repose-digest.env`.
   Check it before enabling the timer: `DATABASE_URL=... /opt/repose/ops/digest/digest.py --dry-run`
   prints the message and sends nothing. A source it cannot reach prints
   `?` for its own numbers and the rest still goes, so question marks in
   the first message are an exporter to fix, not a broken digest.

## Locally

```
docker compose -f ops/dev/docker-compose.yml up -d
go run ./ops/dev/seedmetrics          # plausible values for every family
ops/dev/seedlogs.sh                   # a few lines of the documented shape into Loki
ops/dev/pgcheck.sh                    # schema, synthetic samples, every panel's SQL
```

Grafana is then on `http://<this box's Tailscale address>:3000` (anonymous
admin, folder `repose`); Prometheus on `:9090`. `ops/check.sh --grafana` does
the same thing non-interactively and asserts that all eight dashboards
loaded (the overview was missing from the list until I-800).

`docker compose -f ops/dev/docker-compose.yml down -v` removes it, volumes
included.

## What is checked, and what only a host can tell you

`ops/check.sh` covers: the rules parse; each alert fires on its own fixture;
every alert has a runbook heading; the Grafana alert rules and the dashboards
are both what their generators produce; every
panel has a title, a description, a provisioned datasource and a query; no
Prometheus query selects on `project_id`, `guest_id` or `user_id`; every SQL
query names tables and columns that `db-schema.md` has; every Prometheus
expression parses. With `--grafana`, that Grafana loads all eight
dashboards, that `grafana/push.py` puts all 53 rules into a real Grafana,
that a second push changes nothing, and that `grafana/verify.py` finds no
difference between the target and the generator.

`ops/dev/pgcheck.sh` runs every Postgres panel query against a real
Postgres and makes one partition drop happen.

None of that shows whether a real host's values are sensible, whether
Fluent Bit reaches the real Loki, or whether the alert thresholds are the
right ones for this fleet. Those are the real-host items in
`docs/workstreams/10-observability.md` §9.
