# The control plane on Coolify

`docs/workstreams/11-infra-opentofu.md` §3 draws a line: OpenTofu creates the
VM, and Coolify keeps its application definitions in its own database, so
everything on the far side of that line is a click path. This file is that
click path, in the order it works.

The Coolify is the one the owner already runs on their personal server. The
control VM is a **server** of that instance (`DECISIONS.md` I-83): Coolify
connects to it over SSH as root, installs its proxy, and deploys the api, the
dashboard, Logto and the platform Postgres onto it. Nothing Coolify runs on
the VM itself, so there is no admin account to create there, no port 8000, no
`APP_KEY` to copy off it, and no Coolify upgrade to schedule for it.

The VM itself is `infra/azure/modules/coolify`: an Ubuntu 24.04
`Standard_D4s_v7` with a 256 GB Premium OS disk and a static public IP, in the
`control` subnet, whose NSG opens 80 and 443 to `control_web_cidrs`, and 22 to
`operator_cidrs` plus `coolify_manager_cidrs` (the instance's address), and
nothing else. It stays Ubuntu because Coolify rejects NixOS as a managed
server (`DECISIONS.md` R4-2). cloud-init leaves it exactly as Coolify's
server validation wants to find it: root login by key, with the operator keys
and the instance's key (`coolify_public_key` in `prod.tfvars`) in
`/root/.ssh/authorized_keys`; Docker Engine and the compose plugin from
Docker's apt repository; Tailscale installed but not joined;
`wireguard-tools` and the WireGuard peer script; and nothing listening but
sshd. Nothing for backups or restores is installed: those are Coolify's
(`DECISIONS.md` I-112). `terraform_data.ready` fails the apply if cloud-init
did not finish, Docker or its compose plugin is missing or stopped,
Tailscale is missing, or the instance's key is not in `authorized_keys`.

## Adding the server, in order

1. **Join the tailnet.** Coolify reaches the VM over Tailscale, so no
   address of the owner's server goes in any file and nothing breaks when
   that server moves (`DECISIONS.md` I-86). Once, over SSH:

   ```bash
   ssh root@<control ip> tailscale up          # prints a login URL; approve it
   ssh root@<control ip> tailscale ip -4       # the address Coolify will use
   ```

   A tagged node (`--advertise-tags=tag:repose`) keeps the machine out of a
   personal account; the tag must exist in the tailnet ACL first. Without
   Tailscale, the fallback is `coolify_manager_cidrs` in `prod.local.tfvars`
   (the instance's public `/32`) and an apply; that rule goes stale when the
   owner's server moves, and the failure is a silent hang in step 2.
2. **Add the server** in Coolify: Servers, Add. Name it after the VM
   (`coolify-01`), address the tailnet IP from step 1, user `root`, port
   `22`, and pick the private key whose public half is `coolify_public_key`.
   Then **Validate & configure**. Coolify checks SSH, finds Docker already
   installed, writes its `/etc/docker/daemon.json`, and starts its proxy
   (Traefik) on 80 and 443. A validation that fails on "permission denied"
   is the key: compare `Keys & Tokens` with `prod.tfvars`. One that hangs is
   the network: `tailscale status` on the VM, or the NSG if using the
   fallback.
3. **Names and proxy.** The proxy on this server is what serves
   `repose.herakraft.co` and `api.repose.herakraft.co`; both A records point
   at the VM's public IP (`infra/README.md`, "DNS"). 80 and 443 are open to
   the internet (`control_web_cidrs = ["0.0.0.0/0"]`, owner's call
   2026-09-20), so Let's Encrypt HTTP-01 works as it does everywhere.
4. **Postgres, as a file.** Project -> Add resource -> Services -> Docker
   Compose Empty, paste `ops/coolify/postgres/docker-compose.yml`, server
   the one just added (`DECISIONS.md` I-87). A Service rather than a git
   application because Coolify gives the postgres image inside a Service
   its Backups tab. Leave "Connect to predefined network" off: the file
   joins the shared `coolify` network itself, which registers the service
   name `repose-postgres` there for the applications (fact 11, I-89).
   `POSTGRES_PASSWORD`
   is a project-level shared variable so the api applications reference
   the same one. Its backups are set up on that tab and are the owner's
   own concern (fact 15, `DECISIONS.md` I-112).
5. **Logto** is the owner's existing instance, `https://accounts.herakraft.co`
   (`DECISIONS.md` I-84); nothing is deployed for it. In that Logto: the API
   resource `https://api.repose.herakraft.co`, two applications,
   `repose-cli` (Native, device flow on, redirect
   `http://127.0.0.1:*/callback`) and `repose-web` (SPA, redirect
   `https://repose.herakraft.co/callback`), and the M2M application
   `repose-api` with a role granting the Management API, whose id and
   secret are `LOGTO_M2M_CLIENT_ID/SECRET`. The api and CLI take the
   endpoint without `/oidc` and append it.
6. **api, api-grpc, web**: three Coolify "Dockerfile" applications from the
   repository (`cmd/api/Dockerfile` twice, `apps/web/Dockerfile`), base
   directory `/`, each with its env file from `ops/coolify/` pasted in.
   Domains
   `https://api.repose.herakraft.co:8080` and
   `https://repose.herakraft.co:3000`; port mappings and health-check
   settings are in `ops/coolify/README.md`. Secrets (Logto M2M, the Entra
   client, later Polar and Resend) go in each app's Environment tab,
   nowhere else. Billing (I-604) is `POLAR_ACCESS_TOKEN` (an organization
   access token), `POLAR_ENVIRONMENT` (`sandbox` or `production`, required
   with the token), `POLAR_WEBHOOK_SECRET`, `POLAR_PRODUCT_SOLO`,
   `POLAR_PRODUCT_PLUS`, `POLAR_PRODUCT_PRO`, `POLAR_DISCOUNT_INTRO` and
   optionally `POLAR_PORTAL_RETURN_URL` (default `DASHBOARD_URL/billing`),
   the same block on api and api-grpc, printed by `repose-admin billing
   polar-bootstrap`; web needs none. Without `POLAR_ACCESS_TOKEN` billing
   answers `503 billing_disabled`. No pre-deploy command on either: the api applies its own
   migrations at start and generates the platform CA the first time it
   finds none, both idempotent (fact 12, I-90). `api-grpc` deploys after
   `api`.
7. **The WireGuard peer to the edge**: `infra/README.md`, "Wiring the
   control plane to the edge". Two moves, because the private key never
   leaves the VM. The applications deploy themselves on the first push to
   `main` (fact 16); there is no deploy step here.

A health check on every application is not optional: without one Coolify
silently falls back to stop-then-start instead of a rolling deploy, and the
first anybody hears of it is downtime during a routine deploy
(`RUNBOOK.md` "Coolify deploy failed"). On `api` and `api-grpc` the check
is the image's own `HEALTHCHECK` (`api -healthcheck`), because Coolify's
curl-based one cannot run in a distroless image; Coolify's is turned off
there so the image's drives the deploy (I-87).

## Coolify facts that cost a round trip each (2026-09-20)

Verified in Coolify's source (`bootstrap/helpers/parsers.php`) or against
the live instance, not taken from the docs. Each one changed a file here.

1. **`container_name` in a compose file is overwritten** with
   `<service>-<uuid>`, and `networks: aliases:` are dropped: the parser
   `<service>-<uuid>`. `networks: aliases:` are **not** dropped for a
   service: the parser's "ignore aliases" applies to top-level network
   definitions, and a service's own `networks:` map is passed through
   (serviceParser, 4.3.23). Docker registers the service name on every
   network the file joins, so `repose-postgres` (not `postgres`, which a
   second such service on the shared network would round-robin with) is
   the hostname.
2. **Compose resources get their own network; applications sit on the
   server's `coolify` network.** A compose Service reaches the applications,
   and they reach it, only with **Connect to predefined network** on for
   the Service (Configuration -> Advanced). The applications need no
   toggle. An open Coolify issue reports the toggle sometimes not attaching
   the network for Services: `docker inspect` the container for the
   `coolify` network before debugging anything else (`RUNBOOK.md`
   "api cannot resolve repose-postgres").
3. **The Backups tab exists for a postgres image inside a Service**
   (pasted compose), not for a compose added as a git application. That is
   why `ops/coolify/postgres/docker-compose.yml` is pasted rather than
   pulled from the repository; the file in the repository stays the source
   of truth, and a change to it is a paste.
4. **Coolify's own health check runs curl or wget inside the container.**
   The api image is distroless and has neither, so it carries
   `HEALTHCHECK CMD api -healthcheck` and Coolify's check is turned off on
   `api` and `api-grpc`; with no health signal Coolify silently falls back
   to stop-then-start, which is the downtime the rolling deploy exists to
   avoid.
5. **One resource per thing.** A compose resource redeploys as a unit and
   is not a rolling deploy. Postgres has nothing to roll, so it is the one
   compose file; `api`, `api-grpc` and `web` are three Dockerfile
   applications, each redeployed only for its own change (I-87).
6. **Coolify does what Coolify does.** Backups, health checks, the proxy,
   TLS: configured, not reimplemented. Sidecars that duplicated the backup
   were written and removed the same day (I-87).
7. **Coolify SSHes to a server from inside its own container.** The VM
   answering `tailscale ping` from the Coolify host proves the tailnet, not
   that the `coolify` container can route to it; sshd's journal on the VM
   (`journalctl -u ssh`) shows whether any connection arrived at all.
   `docker exec coolify ssh root@<tailnet ip> true` on the Coolify host is
   the test that matches what Coolify does.
8. **Magic variables and required values.** `${VAR:?}` in a compose file
   makes Coolify refuse to deploy until the value is set, which is how the
   file declares its secrets without holding them; `SERVICE_*` names are
   generated by Coolify. A project-level shared variable
   (`{{project.NAME}}`) is how four resources agree on one password.
9. **A shared-variable reference resolves only as a whole value.**
   `DATABASE_URL=postgres://repose:{{project.POSTGRES_PASSWORD}}@...` reached
   the container with the braces intact (2026-09-20): Coolify's environment
   variable model looks a reference up only when the value starts with
   `{{` and ends with `}}`, never inside a longer string. So the password is
   its own variable, `PGPASSWORD={{project.POSTGRES_PASSWORD}}`, and
   `DATABASE_URL` carries no password; pgx reads `PGPASSWORD` (libpq's
   convention) when the URL omits one, verified against pgx v5.
10. **DNS validation compares a domain with the server's address as
   entered, not with where the proxy listens.** The server is registered by
   its tailnet IP (I-86), so Coolify refused `api.repose.herakraft.co`
   because the record points at the public IP (2026-09-20). The check has
   no notion of a management address distinct from a served one; turn it
   off under Settings -> Advanced, "DNS validation". The records stay as
   `infra/README.md` "DNS" lists them.
11. **"Connect to predefined network" registers only the container name.**
   The toggle does not add `coolify` to the generated compose (its
   `networks:` lists only the per-resource network,
   `/data/coolify/services/<uuid>/docker-compose.yml` on the VM); Coolify
   runs `docker network connect` afterwards, and Docker registers the
   container name there and nothing else, so the first api deploy failed
   with `repose-postgres` NXDOMAIN (2026-09-20). The fix is in the file:
   the service lists `coolify` (declared `external: true`) under its own
   `networks:`, and Docker then registers the service name and any
   `aliases` there. Verified on the VM with a throwaway compose: aliases on
   `coolify` came out as the container name, `repose-postgres` and the
   explicit alias, and a busybox reached 5432 by name. The toggle stays
   off for this Service.
12. **A pre-deployment command runs in the previous container, or not at
   all.** `ApplicationDeploymentJob::run_pre_deployment_command` (4.3.23)
   does `docker exec` into a currently running container of the app and
   logs "No running containers found. Skipping." when there is none. So
   `repose-admin db migrate` as a pre-deploy command never ran on the first
   deploy, and on later ones would have run the old image's migrations;
   `repose-admin ca init` "after the first deploy" had no container to run
   in, because an api that exits on a missing CA never goes healthy and
   Coolify removes it. The api now does both itself at start (I-90).

13. **Every rolling deploy loses a request or two per client at the
   switchover, and how long after depends on the app.** Measured on
   2026-09-20 with `ops/deploy-probe.sh`: three loops at five requests a
   second, two against the dashboard and one against the api, **29,841
   responses, 14 failures, 7 switchovers**.

   | deploy | new container started | failures | after |
   |---|---|---|---|
   | `web` → `e6dd4fa` | 18:05:04.0Z | 18:05:15Z, both loops | +11 s |
   | `web` → `4bcc94b` | 18:22:22.6Z | 18:22:34Z, both loops | +12 s |
   | `api` → `4bcc94b` | 18:23:02.2Z | 18:23:28Z | +26 s |
   | `web` → `cde2b05` | 18:25:24.2Z | 18:25:35Z, both loops | +11 s |
   | `web` → `cc916eb` | 18:28:24.1Z | 18:28:30Z **502**, 18:28:35Z both | +6 s, +11 s |
   | `api` → `faeaefc` | 18:30:20.3Z | 18:30:46Z | +26 s |
   | `web` → `faeaefc` | 18:30:28.9Z | 18:30:40Z, both loops | +11 s |

   Seven for seven, and the delay is the same each time *per
   application*: `web` at +11 or +12 seconds, `api` at +26, both times.
   That tracks each image's `HEALTHCHECK` start period — `web` 5 s,
   `api` 20 s — which is what Coolify waits on before it removes the old
   container, and the failure lands at the removal, not at the start. So
   this is not the health check failing; it is the absence of a drain
   after it passes. Traefik keeps the outgoing container in its pool for
   a moment: a request that picks it then either waits out the client's
   timeout (almost all of them, at 5 s) or gets a clean 502 once the
   container is actually gone (once, at +6 s).

   "Rolling" therefore means no outage, not no dropped request. On `web`
   that is a page that takes five seconds; on `api` it is a CLI command
   or a dashboard poll that fails, which is the one worth deciding
   about. The lever, if it is worth pulling, is the Coolify proxy's
   graceful-shutdown/drain setting, not anything in these images.

   Reading a probe afterwards: a failure within thirty seconds of a
   container start is this, and one on its own with no deploy near it is
   not — a single unexplained 000 appeared at 18:20:39Z on one loop, and
   folding it into the pattern would have made the pattern wrong.

14. **A port mapping and a rolling deploy are mutually exclusive.** A
   published host port means the old and the new container cannot both be
   up, so Coolify falls back to stop-then-start — which is exactly why
   `api-grpc` is a separate application from `api` (I-2), not a
   convenience. So the HTTP `api` publishes **nothing**: an earlier
   version of `ops/coolify/README.md` told an operator to add
   `9103:9103` to it for metrics, and following that would have quietly
   cost the api its rolling deploys, which is the one property the split
   exists to protect. `api-grpc` carries `8443`, `8444` and
   `10.255.255.1:9104:9103` because it is the app that accepts the restart.
   **The WireGuard address `10.255.255.1` is the only interface api-grpc's
   metrics port 9103 is published on** (as host port 9104): it is plain
   HTTP, so it must never be reachable from the VNet (`10.200.3.4`), the
   public IP or `0.0.0.0`; the only client is the monitoring peer over the
   tunnel (DECISIONS I-174, review M5-4). 8443 and 8444 are mTLS and stay
   on every address. Where the api's own
   metrics go instead is an open choice, written up with both options
   and a ready-to-paste label block in `ops/coolify/README.md`, "The
   api's metrics".

15. **Backups are Coolify's, and nothing here touches them.** The
   schedule and the destination live on the Postgres service's Backups
   tab, against storage the owner configures in their own Coolify. No
   bucket, no token, no on-VM check, no rehearsal script and no alert
   exist on this side any more (`DECISIONS.md` I-112), and `infra/r2` is
   gone. If a backup question arises, the answer is in Coolify, not in
   this repository.

16. **Every push to `main` redeploys.** Coolify watches the repository,
   so `api`, `api-grpc` and `web` rebuild and roll on their own; the
   deploy is a consequence of merging, not a step anybody performs.
   Two things follow. A documented procedure must never say "then
   redeploy X" — if it needs new code it needs a merge, and if it needs
   only an environment change, that is a Coolify field and the redeploy
   comes with it. And the drop of fact 13 happens on **every merge**,
   not only on deliberate deploys, which is what makes it worth a
   decision rather than a shrug.

## The instance's .env is half any backup

Backups are Coolify's and the destination is the owner's (`DECISIONS.md`
I-112), so this is a note for whoever owns them rather than a step here,
and it is the one that ruins a restore if it is learned late.

Coolify encrypts the credentials it holds — every application's secrets,
the database passwords it generated — with `APP_KEY` from
`/data/coolify/source/.env` **on the owner's Coolify host**, not on the
control VM, which runs no Coolify (I-83). **A Postgres dump restored into
a Coolify without that key is a database of ciphertext nobody can read.**
The dump is therefore not a complete backup of the control plane on its
own; the owner's backup of their Coolify host is the other half, and it
was already their problem before this project. Check that it exists.

## Upgrading Coolify

Not a step here. The instance is the owner's; its upgrade routine is
whatever it already was. Nothing the api, the dashboard or Logto rely on is
specific to a Coolify version (`DESIGN.md` §Risks).

## What is still a human step

- Joining the tailnet and adding the server (above).
- The Logto applications, the `repose-api` M2M application and the API
  resource in the owner's Logto (above).
- The values under each resource's Environment tab and the two Domains
  fields (`ops/coolify/README.md`).
- Backups: the schedule and the destination on the Postgres service's
  Backups tab, entirely in the owner's Coolify (`DECISIONS.md` I-112).
- The api's Entra app registration and its client certificate, whose object id
  becomes `api_identity_object_id` and turns on the Key Vault wrap/unwrap
  policy (`DECISIONS.md` I-21).
- Every click path in this file. Coolify's API could automate some of it; that
  is not built, and `DECISIONS.md` R3-1 keeps provisioning in OpenTofu, which
  cannot reach into Coolify's database.
