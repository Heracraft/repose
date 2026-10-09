# The control plane on Coolify, as files

Four Coolify resources on the control VM, one per thing, so each deploys on
its own and the three applications get rolling deploys (DECISIONS I-87):

| resource | type | from | env |
| --- | --- | --- | --- |
| `repose-postgres` | Service (Docker Compose, pasted) | `postgres/docker-compose.yml` | `POSTGRES_PASSWORD`; backups are the owner's, on its Backups tab |
| `api` | Dockerfile application, `cmd/api/Dockerfile`, context `/` | this repo, `main` | `api.env.example` |
| `api-grpc` | Dockerfile application, same Dockerfile | this repo, `main` | `api-grpc.env.example` |
| `web` | Dockerfile application, `apps/web/Dockerfile`, context `/` | this repo, `main` | `web.env.example` |

`docs/ops/coolify.md` is the order of operations around them.

## Postgres

The compose file is Postgres alone. Add it as a Coolify **Service** (Add
resource -> Docker Compose Empty, paste the file): Coolify recognises the
postgres image inside a Service and gives it a Backups tab, which is
where the owner sets the schedule and destination (I-112). Turn **Connect to
predefined network** **off** for it (Configuration -> Advanced): the file
joins the shared `coolify` network itself, so the service name
`repose-postgres` is registered there and is the `DATABASE_URL` host. The
toggle instead connects the container after `compose up`, which registers
only the container name (`docs/ops/coolify.md`, fact 11). Do not rename the
service. Its one value is `POSTGRES_PASSWORD`,
a project-level shared variable, so the api apps reference the same one as
`PGPASSWORD={{project.POSTGRES_PASSWORD}}`, on its own line: Coolify only
resolves a reference that is a variable's whole value, so the password is
not embedded in `DATABASE_URL`; pgx takes it from `PGPASSWORD`.

Coolify's DNS validation must be off (Settings -> Advanced): it compares each
domain with the server's tailnet address, not the public IP the proxy serves
on (`docs/ops/coolify.md`, fact 10).

## The three applications

Each is Coolify "Dockerfile" build pack from this repository with base
directory `/` and the Dockerfile path above. Paste the matching env file
into the app's Environment (developer view) and fill the blanks.
Applications sit on the server's `coolify` network by default, where
`repose-postgres` answers because the compose file joins that network; if
the `DATABASE_URL` host lookup fails, `docker inspect` the Postgres
container's aliases on `coolify` (`RUNBOOK.md` "api cannot resolve
repose-postgres").

- `api`: domain `https://api.repose.herakraft.co:8080`; **no port
  mappings at all**, and see "The api's metrics" below for why its 9103
  is not published; **no pre-deploy command**
  (the api migrates itself at start and creates the CA on first start,
  I-90); Coolify's own health check **off** so the
  image's `HEALTHCHECK` (`api -healthcheck`) drives the rolling deploy (the
  image is distroless, so Coolify's curl-based check cannot run in it).
- `api-grpc`: no domain; port mappings `8443:8443`, `8444:8444`,
  `10.255.255.1:9104:9103` (Configuration -> Network, "Ports Mappings"; a
  Dockerfile application publishes nothing until they are set); Coolify's
  health check **off** (same reason). The control NSG never opens
  these ports on the public IP. Docker publishes 8443 and 8444 on every
  address, so they are reached on two private ones (DECISIONS I-92): hosts
  dial the VNet address (`control_private_ip`, `10.200.3.4`) because a host
  registers before it has a tunnel, and the edge dials the WireGuard address
  `10.255.255.1`. Both are mTLS. The metrics port is plain HTTP, so it is
  published on the WireGuard address alone, where only the monitoring peer
  scrapes it (DECISIONS I-174; the 2026-09-21 review's M5-4 found
  `0.0.0.0:9104` open to the edge and hosts). **The WireGuard address is
  the only interface 9103 is ever published on**: a mapping without the
  `10.255.255.1:` prefix is a regression, whatever else changes. Docker cannot publish on an
  address that does not exist yet, so the VM starts Docker after
  `wg-quick@wg0` (`/etc/systemd/system/docker.service.d/10-repose-wg.conf`,
  written by cloud-init; on a VM older than that, write it by hand, below). `GRPC_SERVER_NAMES` therefore lists all three names,
  `api.repose.herakraft.co,10.255.255.1,10.200.3.4` (the IPs become IP
  SANs), so the gateway and hostd verify the certificate the app issues
  from the api's CA at start without a server-name override; hosts also
  pass `apiServerName = api.repose.herakraft.co`.
- `web`: domain `https://repose.herakraft.co:3000`; health check path
  `/healthz`, port 3000.

Required before anything serves: `LOGTO_M2M_CLIENT_ID/SECRET` (the
`repose-api` M2M application in Logto, Management API role),
`PUBLIC_LOGTO_APP_ID` (the `repose-web` SPA), `AZURE_TENANT_ID`,
`AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET` (the api's Entra app registration,
`docs/ops/AZURE-SETUP.md`). Empty until turned on: `RESEND_API_KEY`,
`POLAR_*` (the block `ops/polar/bootstrap.sh` prints, DECISIONS I-604),
`OTEL_EXPORTER_OTLP_ENDPOINT`. The web application needs no Polar value:
the api answers the checkout's URL (I-182, I-604).

## The api's metrics

`API_METRICS_LISTEN=:9103` serves 57 `repose_*` series inside the `api`
container. The application publishes no host port and cannot — a
published port stops the old and the new container coexisting, so
Coolify could not roll the app, which is the constraint that made
`api-grpc` a separate application in the first place (DECISIONS I-2).
`api-grpc` carries `9104:9103` precisely because it is the app that
accepts stop-then-start.

So the proxy already in front of the api serves `/metrics` from the same
container port. **Paste this into the `api` app's Configuration ->
Advanced -> Custom labels** (DECISIONS I-133):

```
traefik.enable=true
traefik.http.routers.api-metrics.rule=Host(`api.repose.herakraft.co`) && Path(`/metrics`)
traefik.http.routers.api-metrics.entryPoints=https
traefik.http.routers.api-metrics.tls=true
traefik.http.routers.api-metrics.tls.certresolver=letsencrypt
traefik.http.routers.api-metrics.service=api-metrics
traefik.http.routers.api-metrics.middlewares=api-metrics-allow
traefik.http.services.api-metrics.loadbalancer.server.port=9103
traefik.http.middlewares.api-metrics-allow.ipallowlist.sourcerange=10.255.0.0/16,10.200.0.0/16
```

Three things about it that are easy to get wrong:

1. **The router matches on the `Host` header**, so a scrape aimed at
   `10.255.255.1:443` does not match it — the header would be the
   address. The monitoring server resolves `api.repose.herakraft.co` to
   the control VM's WireGuard address with one `/etc/hosts` line and
   scrapes the name, which keeps the header, the SNI and the
   certificate all correct
   (`ops/prometheus/wireguard-peer.conf`, step 4).
2. **The allow-list is the only thing keeping `/metrics` off the
   internet**, since the router is on the public entry point, and it
   matches the source address *Traefik sees*. Over WireGuard that should
   be the monitoring peer's `10.255.0.x`, but it is worth one check with
   a real request rather than an assumption: the failure is silent and
   open, not loud and closed. `curl https://api.repose.herakraft.co/metrics`
   from anywhere else must answer 403.
3. **It is a label edited in a UI**, which is the class of thing I-87
   otherwise keeps in files. It lives here so the file is still the
   source of truth even though the paste is manual; a label that drifts
   from this block is a bug in the deployment, not a local improvement.

## Two owner steps on the control VM (I-174)

Both are Coolify- or VM-side and cannot be applied from this repository.

1. **api-grpc's metrics port on the tunnel only.** On a VM that predates
   the cloud-init drop-in, first make Docker wait for the tunnel, or a
   reboot that starts Docker before `wg0` leaves api-grpc (and with it
   8443) unable to start:

   ```
   mkdir -p /etc/systemd/system/docker.service.d
   printf '[Unit]\nAfter=wg-quick@wg0.service\nWants=wg-quick@wg0.service\n' \
     > /etc/systemd/system/docker.service.d/10-repose-wg.conf
   systemctl daemon-reload
   ```

   Then change api-grpc's mapping `9104:9103` to `10.255.255.1:9104:9103`
   and redeploy it. Check: `ss -tlnp | grep 9104` shows
   `10.255.255.1:9104` only; `curl -m3 http://10.200.3.4:9104/metrics`
   from the edge fails; the monitoring server's `repose-api-grpc` target
   stays `up`.

2. **Traefik's `8080` mapping.** `coolify-proxy` publishes `0.0.0.0:8080`
   with nothing listening behind it (`--api.insecure=false`; review M5-5).
   In Coolify: Servers -> the server -> Proxy -> Configuration, delete the
   line `- '8080:8080'` under `ports`, save, and restart the proxy. The
   dashboard, if it is ever wanted, goes behind a router on 443 with an
   allow-list, never a published port. Check: `ss -tlnp | grep ':8080 '`
   prints nothing.

## Backups

Coolify's, on the Postgres service's Backups tab, against a destination
the owner configures in their own Coolify. Nothing on this side is
involved: no bucket, no token, no check, no rehearsal script and no alert
(`docs/DECISIONS.md` I-112). A backup or restore question is answered in
Coolify.

One thing worth knowing before a restore rather than during one:
`docs/ops/coolify.md`, "The instance's .env is half any backup".
