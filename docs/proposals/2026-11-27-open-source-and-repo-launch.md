# Open-sourcing repose under Apache 2.0: repository split, self-hosting readiness, security hardening, and launch cutover

Status: planning specification from owner design session (2026-11-27). Not built yet. No `DECISIONS.md` ids are reserved until each stage is implemented.

Related documents and board items:
- [2026-11-27-own-hardware-free.md](2026-11-27-own-hardware-free.md) — Free bring-your-own-hardware tier (`repose.run` funnel companion to self-hosting)
- [2026-11-26-integrations.md](2026-11-26-integrations.md) — Data-only integration YAML templates and `internal/integrations` engine (§7.7 open source)
- Board items: `ENG-5` (git identity history), `ENG-17` (release asset home across open-source split), `ENG-53` (sign-in dark mode & branding on first CLI login), `ENG-78` (device-code join without private key in user hands), `ENG-208` (bare-metal Linux first, Mac mini second across docs/CLI/UI), `ENG-215` (1,000-host deployment address ceiling), `ENG-236` (auth issuer decoupling and self-hosted sign-in), `ENG-271` (pre-launch `host-02` scale-up), `ENG-282` (notification noise and channel architecture), `ENG-283`–`ENG-289`

---

## 1. Strategic decision (owner, 2026-11-27)

1. **Model: Option 1 — Full self-hostable stack open source (Coolify model), live `repose.run` fleet operations private.**
   - Coding agents make closed-source feature secrecy temporary; anyone motivated can replicate surface behaviour with agents in days.
   - Open-sourcing the full self-hostable product builds immediate trust for software that runs a root daemon (`hostd`) on a developer's bare-metal Linux box or Mac mini, removes the incentive to build a clean-room clone, and turns integrations into community pull requests.
2. **License: Apache License 2.0 (`Apache-2.0`) with a `NOTICE` file.**
   - Permissive for individuals and enterprises (no AGPL laptop-binary bans), includes explicit patent grant and retaliation clauses, and explicitly excludes trademark rights (§6) so the `repose` name and dividing-cell mark remain Herakraft's.
3. **Commercial pairing: Free BYOH on `repose.run` keeps self-hosters in the hosted funnel.**
   - Offering the full single-host experience for free on `repose.run` with user-owned hardware ([2026-11-27-own-hardware-free.md](2026-11-27-own-hardware-free.md)) means 95% of developers who value the Apache 2.0 no-lock-in guarantee will still connect their Linux box or Mac mini to `repose.run` in 60 seconds rather than operating Postgres, OIDC, TLS certificates, and a WireGuard hub themselves.

---

## 2. Audit of `Heracraft/repose` today (what must not ship in the public tree)

Today `Heracraft/repose` on GitHub is `PUBLIC` with no `LICENSE` file (`licenseInfo: null`, defaulting to all rights reserved) and ~3,700 commits. The Go, Nix, and SvelteKit product code is clean, tested, and production-grade; what creates operational, competitive, and security exposure is non-product state living in the same tree and git history:

1. **Live fleet sizing, cloud identifiers, and runway notes**
   - `infra/azure/prod/prod.tfvars` and `infra/bootstrap/prod.tfvars` carry the production Azure `subscription_id` field, Key Vault names, snapshot/tfstate storage account names, Coolify SSH public key, and dated runway comments describing exact VM SKU downscaling and daily spend.
   - `docs/PRICING.md` records internal margin arithmetic (per-guest `OverheadMiB` cost on `tiny`, founding-seat discount mechanics, and unlaunched burst allowances).
2. **Operator network topology and internal peers**
   - `nix/edge/edge-01.nix` records the dev box public IP in `operatorCIDRs` (admitted to operator SSH `:2222`) and the static WireGuard public keys and internal IPs of the control VM, the personal monitoring peer, and the `kanali` coordinator guest.
   - `nix/hosts/host-01.nix`, `nix/hosts/host-02.nix`, and `nix/hosts/host-stg-01.nix` record per-host production disk and burst settings.
3. **Internal agent orchestration logs, incident timelines, and unmitigated risk notes**
   - `docs/DECISIONS.md` (~39,900 lines), `docs/ops/RESTART.md`, `docs/ops/CONDUCTOR-HANDOFF.md`, `docs/ops/RUNBOOK.md`, `docs/ops/LAUNCH.md`, `docs/workstreams/`, `docs/reports/`, and `docs/proposals/` narrate past incidents (`I-643`, `I-920`, `I-954`), open P0/P1 defects, and internal owner transcripts.
   - `docs/SECURITY.md` ("Not mitigated in the first release") enumerates known fleet trade-offs (shared `hostd` UID across taps, shared snapshot Blob identity across hosts, single edge VM carrying `:22` and `:443` without upstream DDoS protection).
4. **Git history hygiene, author identity, and SaaS anti-abuse constants**
   - Commit `b1a5915e` (`I-193`) committed a `.env` key (since rotated) alongside the owner's private email in git metadata; `ENG-5` tracks 28 commits on `main` with an unverified git identity; `I-194` names unrelated repositories; `context/portless` holds early scratch files.
   - At the squashed root commit for the public repository (and across the owner's git config going forward), switch `user.email` off the private email address to either GitHub's anonymized `users.noreply.github.com` address or `nehemia@repose.run`, and add the old private email to the public export's CI deny-grep.
   - `internal/guestd/sample/watch.go` and `internal/abuse` hardcode exact miner binary names in source, allowing an abuser who reads the repo to rename binaries around static checks.

---

## 3. Repository split and day-to-day workflow

### 3.1 What lives where

| Surface | Public `Heracraft/repose` (`Apache-2.0`) | Private `Heracraft/repose-internal` |
|---|---|---|
| **Binaries & packages** | `cmd/repose`, `cmd/hostd`, `cmd/guestd`, `cmd/gateway`, `cmd/api`, `cmd/repose-hook`, `cmd/repose-admin`, test fakes; `internal/*`; `proto/` | Same source (if using Workflow A below) plus any SaaS-only operational scripts |
| **Guest & host images** | `images/guest/`, `images/appliance-arm64/`, generic Nix modules in `nix/` (`guest/`, `overlay/`, generic `hosts/` and `edge/` modules), `ops/install-host-x86.sh` | `nix/hosts/host-01.nix`, `host-02.nix`, `host-stg-01.nix`, `nix/edge/edge-01.nix` |
| **Web & docs** | `apps/web/` (dashboard + user docs under `src/content/docs/`, with GTM `I-1214`, Polar IDs, and domain strings strictly env-gated) | SaaS-specific legal/marketing experiments if held prior to launch |
| **Infrastructure & ops** | `docker-compose.yml` + `.env.example` for self-hosting | `infra/` (Azure, Cloudflare, R2 OpenTofu), `ops/` (Grafana, Prometheus, Loki, Alloy, Polar, Coolify, SQL, conductor tooling) |
| **Documentation** | Clean `README.md`, `SELF-HOSTING.md`, `CONTRIBUTING.md`, `SECURITY.md` (architecture boundaries + private disclosure), `LICENSE`, `NOTICE` | `docs/DECISIONS.md`, `DECISIONS-INDEX.md`, `PRICING.md`, `docs/ops/*`, `docs/workstreams/*`, `docs/proposals/*`, `docs/reports/*`, `docs/security/*` |
| **Git history & releases** | Squashed clean initial `Apache-2.0` commit onward; **existing GitHub Release tags and binary assets kept intact** | Full 3,700+ commit history preserved permanently |

### 3.2 Two workflow options for ongoing agent waves

- **Workflow A (Recommended while agent velocity is high): Private working monorepo → automated filtered export to public `Heracraft/repose` on release.**
  - Agents and the conductor continue working in private `Heracraft/repose-internal` (`woker-1`, worktrees, `DECISIONS.md`, `PR.md` records, `ENG-*` references) with zero risk of an agent writing internal fleet or runway notes onto a public PR.
  - Coolify (`api`, `api-grpc`, `web`) deploys `repose.run` from private `Heracraft/repose-internal` `main`.
  - At each release (`v0.1.N`), a deterministic export script (`ops/dev/public-export`) strips the excluded paths above, runs a leak-gate check, and pushes one clean release commit + tag to public `Heracraft/repose`. External community PRs opened on `Heracraft/repose` are imported into a worktree, verified, merged, and flow back out in the next export.
- **Workflow B (Post-stabilization option): Develop product code directly in public `Heracraft/repose`; keep private `Heracraft/repose-ops` for `infra/`, `ops/`, fleet Nix, and internal docs.**
  - Gives public contributors live daily commits and public CI runs, at the cost of enforcing strict commit/PR hygiene on every agent session so internal operational details never enter `Heracraft/repose`.

---

## 4. Self-hosting turnkey stack (separate post-launch project, ~1 week after launch)

**Sequencing ruling (owner, 2026-11-27):** Do **not** block the `repose.run` launch or the Apache 2.0 repo cutover on swapping out Azure Key Vault or Logto. Launch first with the clean Apache 2.0 repository and Free 1-host BYOH on `repose.run`, gauge real self-hosting demand, and bring in turnkey control-plane self-hosting as its own project roughly one week after launch. Until then, the public `README.md` can state plainly that full control-plane self-hosting packaging is coming shortly and point users to the free 1-host BYOH tier on `repose.run`.

Most of `repose` already works without SaaS services: `API_MIGRATE=1` bootstraps the Postgres schema and platform CAs automatically (`I-90`), `BILLING_ENFORCE=false` disables Polar gates cleanly (`I-1396`), and user-owned Linux/Mac hosts store snapshots locally without Azure Blob (`I-1372`, `I-1636`).

When the post-launch self-hosting project runs, three concrete gaps close a non-Azure `docker compose up`:

1. **Azure Key Vault hard requirement (`internal/api/app/config.go:160`)**
   - *Today:* `api` refuses to start unless `KEYVAULT_URL` is set or `REPOSE_DEV=1` is passed. `REPOSE_DEV=1` uses an in-memory key vault that loses every wrapped secret DEK when the container restarts.
   - *What to build:* Add a `LocalKeyVault` (`SECRET_MASTER_KEY` env var or `SECRET_MASTER_KEY_FILE` mode `0600`, 32-byte base64/hex key using AES-256-GCM key wrapping) implementing the 3-method `secrets.KeyVault` interface (`CurrentVersion`, `Wrap`, `Unwrap` in `internal/api/secrets/secrets.go:33`). Keep `KEYVAULT_URL` on `repose.run`; mutually exclude `KEYVAULT_URL` and `SECRET_MASTER_KEY` at config validation so production never falls back silently.
2. **Hardcoded Logto / Herakraft OIDC defaults (`internal/api/app/config.go:164`, `internal/cli/config.go`, `ENG-236`)**
   - *Today:* `api` requires `LOGTO_ISSUER`, `LOGTO_M2M_CLIENT_ID`, and `LOGTO_M2M_CLIENT_SECRET`, and the CLI defaults its OAuth audience/issuer to `repose.run` / Herakraft values (`I-99`, `I-102`, `I-1156`). Manual Logto setup is heavy friction for self-hosters.
   - *What to build:*
     - Ensure `repose login --api-url https://repose.example.com` discovers the self-hosted instance's OIDC issuer, client ID, audience, and SSH gateway (`POST /certs` already returns gateway host/port per `I-1502`; add unauthenticated `GET /public/auth-config` so the CLI needs zero client-side OAuth flags).
     - Ship either a pre-seeded OIDC container in `docker-compose.yml` or a lightweight built-in GitHub OAuth / single-admin token mode for self-hosted deployments.
3. **Turnkey self-hosted `docker-compose.yml` and `SELF-HOSTING.md`**
   - Compose stack running `postgres`, `api` (`http`), `api-grpc` (`grpc`), `gateway` (with WireGuard UDP `:51820`, SSH `:2222`/`:22`, webhook/view TLS), and `web`.
   - Verified end-to-end on a clean Ubuntu VM with no Azure credentials: `docker compose up -d` → `repose login --api-url ...` → `repose host add` → `repose run` → browser view and snapshots working.

---

## 5. Community contribution rails

With `repose config add` and the dashboard's package menu removed on Ubuntu guests in favour of `apt-get` (`I-1698`), `CONTRIBUTING.md` should steer external contributors toward four clean, low-blast-radius extension points:

1. **Integration templates and engine ([2026-11-26-integrations.md](2026-11-26-integrations.md))**
   - `integrations/templates/<provider>.yaml` (Coolify, Grafana, Linear, Kaneo, Supabase, Datadog, AWS, Azure, GitHub). Pure declarative YAML validated by JSON Schema and a CI checklist linter—no arbitrary code execution.
2. **Notification providers (`internal/api` outbox senders; related to `ENG-282`)**
   - Extract a small `Notifier` interface (`Send(ctx, Notification) error` + URL/credential validator using `I-444`'s public-address SSRF dialer) so community PRs can add Slack, Discord, Telegram, Linear, PagerDuty, or generic signed webhooks in one isolated package per provider.
3. **Agent wrappers, state hooks, and machine guides (`cmd/repose-hook`, `internal/guestd`, `images/guest/`)**
   - Adding new coding agents (e.g. `ENG-161` / `ENG-261` Antigravity CLI), improving `working` / `idle` / `needs_input` state accuracy across tmux, herdr, and windowless desktop sessions (`I-1676`), and keeping `versions.json` current.
4. **CLI and host substrate (`cmd/repose`, `cmd/hostd`)**
   - Editor integrations (`repose code`), Linux distro compatibility in `ops/install-host-x86.sh`, and macOS `Virtualization.framework` (`Design 2`) polish.

---

## 6. Security hardening before announcing the repo

Kernel/KVM boundaries, nftables bridge drop rules, 24-hour Ed25519 SSH certificates, Key Vault envelope encryption, and `sha256` token storage do not weaken when attackers read the source (`docs/SECURITY.md`). What *does* change when agents scan a public repo is speed at finding **logic bugs on public/tenant boundaries** and **bypassing hardcoded abuse heuristics**:

1. **Externalize SaaS abuse watchlists (`internal/guestd/sample/watch.go`, `internal/abuse`)**
   - Keep a minimal baseline in open source and load additional watched/auto-stopped process patterns on `repose.run` from operator configuration so cryptocurrency miners cannot trivially rename binaries around the public list. Keep `ProcSample` fields unchanged so `test/isolation/policy_test.go` and the privacy policy promise hold.
2. **Automated leak gate on the public tree**
   - Run `gitleaks` / `trufflehog` plus a repository-specific deny-grep in CI that fails if any commit on the public branch contains `infra/`, `docs/DECISIONS.md`, `docs/ops/`, `subscription_id`, `repose-kv`, `reposesnapshots`, `20.102.`, `10.255.254.`, or personal email addresses.
3. **Adversarial review of every untrusted boundary before promotion**
   - Run a dedicated adversarial review pass (with reproduction tests) across:
     - `internal/gateway` SSH handshake limits (`I-435`) and `:443` webhook path/header/redirect rules (`I-1110`, `I-1116`).
     - `internal/api/http` JWT/API-token scope table (`I-1316`) and cross-tenant `404` scoping.
     - `internal/api` outbound HTTP dialers (`ntfy`, Google Chat, and future notification/integration providers) against DNS rebinding and private/link-local SSRF (`I-444`).
     - `internal/api` and `internal/hostd` foreign-host / foreign-guest / foreign-command checks (`I-1368`) and `ApplyMenu` legacy input validation while the one-release compatibility route remains (`I-1698`).
4. **Disclosure and dependency gates**
   - Enable **GitHub Private Vulnerability Reporting** on `Heracraft/repose` and reference it in the public `SECURITY.md`.
   - Enable `govulncheck` and CodeQL on public PRs.
   - Revisit `ENG-78` (device-code host join where the WireGuard private key never leaves the host) so join-argument handling is eliminated as a secret-transport surface.

---

## 7. How things could go wrong, and where each failure is caught

| Failure mode | Consequence if missed | Prevention & automated gate that catches it |
|---|---|---|
| **1. Deleting and recreating `Heracraft/repose` on GitHub to wipe history (`ENG-17`)** | Wipes existing GitHub Releases (`v0.1.69`, `guest-*`, `hostd-*`, `appliance-*`). Every `install.sh` run, `repose version update`, Linux host image pull (`I-1478`), and Mac 30-minute `repose-hostd-update` (`I-1686`) immediately fails with `404`. | **Never delete `Heracraft/repose`.** Push the full mirror to private `Heracraft/repose-internal` first, then update `Heracraft/repose` branches in place while keeping existing Releases untouched. Gate: run `ops/dev/release-assets` (`I-1398`) immediately after cutover and alert via `ENG-99`. |
| **2. Resetting `Heracraft/repose` `main` while Coolify still auto-deploys from it (`I-112`)** | Force-pushing a stripped public tree triggers Coolify to redeploy `api`, `api-grpc`, and `web` from the stripped repo or breaks webhook triggers. | Repoint all Coolify apps (`api`, `api-grpc`, `web`) to private `Heracraft/repose-internal` `main` **before** touching public `Heracraft/repose`, and verify one clean Coolify deploy + `ops/deploy-probe.sh` first. |
| **3. Old commits (`b1a5915e`, `I-193`) remain reachable via old release tags on GitHub** | Even after force-pushing `main` to an orphan commit, old `v0.1.*` git tags still point into the old commit DAG, so `git fetch origin tag v0.1.50` still downloads the old history (`prod.tfvars`, `RESTART.md`, `b1a5915e`). | Before announcing: re-point historical release tags onto synthetic tag-only marker commits (preserving the attached GitHub Release binary assets, checksums, and signatures!), delete merged PR refs/branches if needed or contact GitHub Support to GC unreachable objects, then verify in a fresh `git clone --mirror` that `git log --all` contains **only** the new root commit onward. Re-run `ops/dev/release-assets` to verify no Release asset or signature broke when moving a tag ref. |
| **4. `LocalKeyVault` (`SECRET_MASTER_KEY`) accidentally used in production, or lost by self-hosters** | If `repose.run` boots without `KEYVAULT_URL` and silently generates a random key, secrets break across restarts/replicas. If a self-hoster loses `SECRET_MASTER_KEY`, encrypted DB secrets are unrecoverable. | `config.Validate()` refuses startup unless **exactly one** of `KEYVAULT_URL`, `SECRET_MASTER_KEY`/`_FILE`, or `REPOSE_DEV=1` is set (`TestKeyVaultConfigMutuallyExclusive`). `docker-compose.yml` generates and persists the key into a named volume or `.env` on first setup and documents backup in `SELF-HOSTING.md`. |
| **5. Whole-tree tests break across the split (`ENG-142`)** | `internal/cli` docs tests (`I-242`) read `apps/web/src/content/docs/`, while `test/domain` and `ops/dev/decisions-index.py` read `docs/DECISIONS.md` and `ops/`. A partial export breaks `go test ./...` on the public repo. | Keep `apps/web/src/content/docs/` and `images/guest/agent-guide.md` in the public tree so `internal/cli` docs/guide tests pass publicly; scope internal-only checks (`decisions-index.py`, `ops/check.sh`, `test/domain` allow-list rows for `infra/`/`ops/`) to internal CI, and run `go test ./...` inside a clean checkout of the exported public tree in CI before pushing any public commit. |
| **6. Free tier + open source traffic spike exhausts the 1,000-host hub ceiling (`ENG-215`)** | An HN/GitHub launch drives >1,000 `repose host add` calls; `hostmgr` runs out of `/22` subnets and `POST /hosts` returns `500` for everyone. | Treat `ENG-215` + [2026-11-27-own-hardware-free.md](2026-11-27-own-hardware-free.md) as a hard blocker before the open-source announcement. |

---

## 8. Ordered launch waves (coordinated with active conductor work)

| Wave | Board items | Why this exact order |
|---|---|---|
| **Wave 0 (In flight now)** | Conductor batch `r20261127-7` (`msgs-plain` `I-1724`, `a11y-config`), `msgs-plain-2` (`I-1728`), `d2-core-6` (`I-1726`, `ENG-279` option a), `d2-surface-4`, and `[sweep-cli]` cards | Finishes Mac Design 2 pass-2 fixes and establishes the 2-line plain-words refusal architecture (`I-1724`) across `internal/api/ops/`, `internal/cli/`, and `apps/web/src/lib/refusal.ts`. |
| **Wave 1 (Build now — disjoint from Wave 0)** | `ENG-215` (1,000-host address ceiling), `ENG-284` (security hardening + externalized abuse watchlists), `ENG-99` (alert on red `release-assets.yml`), plus owner call on `ENG-282` (default-off email) | Touches `internal/api/hostmgr/`, `internal/abuse`, `watch.go`, and `.github/workflows/` — zero file overlap with Wave 0, and closes every safety/capacity prerequisite before Free BYOH or public repo promotion. |
| **Wave 2 (Unblocks after Wave 0 + `ENG-215` + `ENG-282`)** | `ENG-283` (Free BYOH: `FreeOwnHosts = 1`, no card, remote browser unlocked; `$10/month` `Own hardware` at 2+ hosts), `ENG-289` (CLI first-run sign-up & Cloud vs Own-hardware prompt), `ENG-208` (bare-metal Linux first, Mac mini second across CLI/UI/docs), `ENG-53` (sign-in dark mode & branding), `ENG-253` (landing hardware visuals, Linux-first) | Building `ENG-283` and `ENG-289` *after* `msgs-plain` (`I-1724`) avoids merge conflicts in `ops/`, `cli/run.go`, and `refusal.ts`, and aligns every surface on bare-metal Linux first, Mac mini second. |
| **Wave 3 (Clean queue boundary after `ENG-284` + `ENG-99` + `ENG-283`)** | `ENG-285` (Mirror `Heracraft/repose-internal`, repoint Coolify, switch git author `user.email` to GitHub noreply or `nehemia@repose.run`, build `ops/dev/public-export` with leak-gate CI, push squashed `Apache-2.0` commit & re-point tags without losing Releases; closes `ENG-5`) | **Must run at a clean conductor wave boundary (`release-queue ls` empty):** active worktrees on `woker-1` currently point `origin` at `Heracraft/repose`, so repointing `woker-1` + Coolify to `repose-internal` and rewriting public `Heracraft/repose` mid-wave would break open `gh pr create --base integrated` branches. Verify `ops/dev/release-assets` (`I-1398`) immediately after cutover. |
| **Wave 4 (Final pre-announcement switch)** | `ENG-271` (Resize `host-02` back to `Standard_D64s_v7`, reissue & undrain) | Kept last so `host-02` keeps saving ~$51/day on `Standard_D32s_v7` throughout Waves 0–3 until launch day. |
| **Wave 5 (~1 week post-launch)** | `ENG-286` (Turnkey self-hosting: `SECRET_MASTER_KEY` local `KeyVault`, Logto/OIDC decoupling, `docker-compose.yml`), `ENG-287` + `ENG-288` (Integrations engine & YAML templates, stages 1–5) | Deferred past launch on purpose: launch first, measure real self-hosting demand, and unlock community YAML integration PRs. |
