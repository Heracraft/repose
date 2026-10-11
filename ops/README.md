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
| `alerts.yaml` | The 30 Prometheus alert rules. Every name is a heading in `docs/ops/RUNBOOK.md`. |
| `alerts_test.yaml` | `promtool test rules` cases: each alert fires on the series it was written for and stays quiet just below it. |
| `alertmanager/repose-route.yaml` | Route and inhibitions: everything to Google Chat through calert, `page` on a faster timer, `Watchdog` only to the Uptime Kuma push monitor (DECISIONS I-648). |
| `alertmanager/calert/` | calert, the Alertmanager-to-Google-Chat adapter: its config, the message template and a compose file. |
| `prometheus/prometheus.yml` | Scrape config: hosts (`:9100`, `:9101`, Fluent Bit `:2021`), edge (`:9102`), api (`:9103`), all over WireGuard. |
| `prometheus/targets/hosts.yml` | The host list, re-read every minute. Adding a host is one edit here. |
| `prometheus/wireguard-peer.conf` | How the monitoring server joins the edge's WireGuard hub, and how to check it. |
| `loki/retention.yaml` | 90 days for component logs, 30 for guest console logs, with the compactor that makes it happen. |
| `grafana/provisioning/` | Datasources (three fixed uids) and the dashboard provider. |
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
   `--storage.tsdb.retention.time=90d`. Check `up{job=~"hosts|hostd"}`.
3. **Alertmanager, calert and the heartbeat** (DECISIONS I-648).
   - calert: copy `alertmanager/calert/config.toml` and `message.tmpl` to
     `/etc/calert/`, then write the Google Chat space's webhook URL into
     calert's environment without it reaching the shell history, the
     screen or a log:

     ```
     read -rsp 'Chat webhook URL: ' u; (umask 077; printf 'CALERT_PROVIDERS__REPOSE__ENDPOINT=%s\n' "$u" > /etc/calert/calert.env); unset u
     docker compose -f alertmanager/calert/docker-compose.yml up -d
     ```

     In Coolify instead, paste the compose file as a service and put
     `CALERT_PROVIDERS__REPOSE__ENDPOINT` in its Environment tab. calert
     listens on `127.0.0.1:6000`; if Alertmanager runs in a container
     without the host network, point the two webhook URLs at calert's
     service name instead.
   - Uptime Kuma: add a monitor of type Push, heartbeat interval 180 s,
     retries 1, with the Chat space as its notification. Kuma must be 2.0
     or later (1.x accepts only GET on push URLs; Alertmanager POSTs), and
     should run somewhere other than this server, or a dead server goes
     unheard. Put its push URL in Alertmanager's secrets:
     `read -rsp 'Kuma push URL: ' u; install -d -m 0750 /etc/alertmanager/secrets; (umask 077; printf '%s' "$u" > /etc/alertmanager/secrets/repose-watchdog-url); unset u`,
     readable by the user Alertmanager runs as.
   - Merge `alertmanager/repose-route.yaml` into `alertmanager.yml` and
     reload (`curl -X POST localhost:9093/-/reload`). Check each route with
     `amtool config routes test --config.file=alertmanager.yml stack=repose
     alertname=HostdDown severity=page` (and `Watchdog`, `HostServiceDown`),
     then send one through to Chat: `amtool alert add alertname=ReposeTest
     severity=warn stack=repose --annotation=summary="test"`.
4. **Loki.** Merge `loki/retention.yaml`. Retention does nothing without the
   compactor, so check `curl -s localhost:3100/config | grep retention`.
5. **Grafana.** Provisioning from `grafana/provisioning/`, dashboards from
   `dashboards/` mounted at `/etc/grafana/dashboards/repose`. Create the
   read-only Postgres role in the datasource file's comment first, or the four
   Postgres-backed dashboards are empty.
6. **Postgres.** Nothing to install: the api creates and drops the sample
   tables' partitions itself. `sql/partitions.sql` is the same maintenance as
   SQL functions, for an operator whose api is down (`psql -f
   sql/partitions.sql`, then `select * from repose_partitions_maintain();`).

## Locally

```
docker compose -f ops/dev/docker-compose.yml up -d
go run ./ops/dev/seedmetrics          # plausible values for every family
ops/dev/seedlogs.sh                   # a few lines of the documented shape into Loki
ops/dev/pgcheck.sh                    # schema, synthetic samples, every panel's SQL
```

Grafana is then on `http://<this box's Tailscale address>:3000` (anonymous
admin, folder `repose`); Prometheus on `:9090`. `ops/check.sh --grafana` does
the same thing non-interactively and asserts that seven of the eight
dashboards loaded (it does not check the overview).

`docker compose -f ops/dev/docker-compose.yml down -v` removes it, volumes
included.

## What is checked, and what only a host can tell you

`ops/check.sh` covers: the rules parse; each alert fires on its own fixture;
every alert has a runbook heading; the dashboards match the generator; every
panel has a title, a description, a provisioned datasource and a query; no
Prometheus query selects on `project_id`, `guest_id` or `user_id`; every SQL
query names tables and columns that `db-schema.md` has; every Prometheus
expression parses. With `--grafana`, that Grafana accepts seven of them (not the overview).

`ops/dev/pgcheck.sh` runs every Postgres panel query against a real
Postgres and makes one partition drop happen.

None of that shows whether a real host's values are sensible, whether
Fluent Bit reaches the real Loki, or whether the alert thresholds are the
right ones for this fleet. Those are the real-host items in
`docs/workstreams/10-observability.md` §9.
