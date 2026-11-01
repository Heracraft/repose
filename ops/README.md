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
| `grafana/provisioning/alerting/repose-rules.yaml` | The 54 rules as Grafana reads them. Generated: edit `alerts.yaml` and re-run the generator. |
| `grafana/provisioning/alerting/repose-notifications.yaml` | Contact points and the notification policy: email for everything, `page` on a faster timer, `Watchdog` only to the Uptime Kuma push monitor. Hand-kept, and it says what the inhibition rules became. |
| `grafana/verify.py` | Asks a running Grafana what it made of the two files above: a rule for every alert under its own name, and the seven routing cases. |
| `alertmanager/repose-route.yaml` | Kept, not deployed. The same routing as an Alertmanager configuration, and the only written record of the seven inhibition rules Grafana cannot express (DECISIONS I-648, I-848, I-942). |
| `prometheus/prometheus.yml` | Scrape config: hosts (`:9100`, `:9101`, Fluent Bit `:2021`), edge (`:9102`), api (`:9103`), all over WireGuard. |
| `prometheus/targets/hosts.yml` | The host list, re-read every minute. Adding a host is one edit here. |
| `prometheus/wireguard-peer.conf` | How the monitoring server joins the edge's WireGuard hub, and how to check it. |
| `loki/retention.yaml` | 90 days for component logs, 30 for guest console logs, with the compactor that makes it happen. |
| `grafana/provisioning/` | Datasources (three fixed uids), the dashboard provider, and the alert rules, contact points and notification policy. |
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
   `/etc/prometheus/prometheus.yml`, `alerts.yaml` and `prometheus/targets/`
   to `/etc/prometheus/repose/`, and run it with
   `--storage.tsdb.retention.time=90d`. Before `alerts.yaml` is loaded,
   `up{job=~"hosts|hostd|api|api-grpc|gateway"}` must be 1 for every
   target: HostdDown, ApiDown and GatewayDown page on any that is 0 and
   repeat hourly. `api` passes through Traefik's allow-list
   (`coolify/README.md` "The api's metrics"). After loading it,
   `curl -s localhost:9090/api/v1/alerts` lists only `Watchdog` firing.
3. **Alerting: email and the heartbeat** (DECISIONS I-648, I-848, I-942).
   Grafana sends it, not an Alertmanager; there is none on this server and
   this adds none. The rules, the contact points and the notification policy
   are files under `grafana/provisioning/alerting/`, loaded by step 5. Three
   things are not in them, because none belongs in a repository:

   - **SMTP.** Grafana's own `[smtp]`. In this stack that is environment
     variables on the Grafana service, because Coolify owns the compose file
     and there is no grafana.ini to edit:

     ```
     GF_SMTP_ENABLED=true
     GF_SMTP_HOST=smtp.resend.com:587
     GF_SMTP_USER=resend
     GF_SMTP_PASSWORD__FILE=/etc/grafana/secrets/repose-smtp-password
     GF_SMTP_FROM_ADDRESS=alerts@repose.herakraft.co
     GF_SMTP_FROM_NAME=repose alerts
     GF_SMTP_STARTTLS_POLICY=MandatoryStartTLS
     ```

     The `__FILE` suffix is Grafana's own: it reads the value out of the
     file, so the Resend API key is not in Coolify's environment. The domain
     is verified already (LAUNCH.md step 2) and the username is the literal
     `resend`. Put the key where Grafana can read it:

     ```
     read -rsp 'Resend API key: ' k; install -d -m 0750 /etc/grafana/secrets; (umask 077; printf '%s' "$k" > /etc/grafana/secrets/repose-smtp-password); unset k
     ```

     Readable by the user Grafana runs as (uid 472 in the official image),
     and mounted into the container at that same path.
   - **The inbox.** `REPOSE_ALERT_EMAIL` on the Grafana service, the address
     Azure's alerts already use (`alert_email` in `prod.local.tfvars`); one
     inbox for both is the point. The provisioned contact points expand it
     as `$__env{REPOSE_ALERT_EMAIL}` when they load.
   - **Uptime Kuma.** Add a monitor of type Push, heartbeat interval 180 s,
     retries 1, with the Chat space as its notification. Kuma must be 2.0 or
     later (1.x accepts only GET on push URLs; Grafana POSTs), and should
     run somewhere other than this server, or a dead server goes unheard.
     Put its push URL in the file the policy reads as
     `$__file{/etc/grafana/secrets/repose-watchdog-url}`, because a URL
     anyone can push to is a way to hide an outage:
     `read -rsp 'Kuma push URL: ' u; install -d -m 0750 /etc/grafana/secrets; (umask 077; printf '%s' "$u" > /etc/grafana/secrets/repose-watchdog-url); unset u`

   Then check it in Grafana: Alerting -> Contact points lists `repose-email`,
   `repose-email-page` and `repose-watchdog`, all three provisioned, and
   **Test** on `repose-email` arrives in the inbox; Alerting -> Alert rules
   lists 54 rules in a `repose alerts` folder with `Watchdog` firing and
   nothing else; Kuma's monitor goes green within a minute. The alerts have a
   folder of their own, not the dashboards' `repose`, because Grafana
   provisions alerting before dashboards and would otherwise make a second
   folder of the same name (I-942).
   `GRAFANA_TOKEN=... python3 grafana/verify.py --url https://<grafana>`
   asserts the same without a browser.

   Two things to know before loading it. **The notification policy is the
   org's whole tree**: this file replaces whatever policy that Grafana had,
   and the UI refuses to edit it afterwards. Its root route sends to the same
   inbox, so an alert from anything else in that Grafana is still delivered.
   And **there are no inhibitions**: Grafana cannot express the seven in
   `alertmanager/repose-route.yaml`, so an outage that trips three pages
   sends one email naming all three rather than one email for the cause. The
   policy file explains it in full.
4. **Loki.** Merge `loki/retention.yaml`. Retention does nothing without the
   compactor, so check `curl -s localhost:3100/config | grep retention`.
5. **Grafana.** Provisioning from `grafana/provisioning/` -- datasources,
   dashboards and alerting, all three -- with dashboards from `dashboards/`
   mounted at `/etc/grafana/dashboards/repose` and step 3's secrets at
   `/etc/grafana/secrets`. Create the read-only Postgres role in the
   datasource file's comment first, or the four Postgres-backed dashboards
   are empty. This Grafana evaluates the alert rules, so it is in the path of
   every page: a Grafana that is down tells nobody anything except by
   `Watchdog` stopping, which is what Kuma watches.
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

   It needs `psql` on PATH and reads the SMTP password from the file Grafana
   already holds (`SMTP_PASSWORD_FILE` in the unit, pointed at
   `/etc/grafana/secrets/repose-smtp-password`), so it adds no secret. Put `DIGEST_TO` in `/etc/repose-digest.env`.
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
dashboards, that it holds a rule for each of the 54 alerts under its own
name, and that the seven routing cases land on the contact point they
should.

`ops/dev/pgcheck.sh` runs every Postgres panel query against a real
Postgres and makes one partition drop happen.

None of that shows whether a real host's values are sensible, whether
Fluent Bit reaches the real Loki, or whether the alert thresholds are the
right ones for this fleet. Those are the real-host items in
`docs/workstreams/10-observability.md` §9.
