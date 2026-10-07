# 08 · dashboard

## 0. Design language

Read `../DESIGN-LANGUAGE.md` before writing any markup. It says what to copy
from the recruiting app and which of its controls are rejected.

## 1. Goal

The web dashboard at `repose.herakraft.co` is where users do the things that
are awkward in a terminal: pick packages from a menu without writing Nix,
manage secrets, look at cost, put a card on file, and see what an agent did
overnight. It shows nothing the CLI cannot also do, and it does nothing the
API does not do for it.

## 2. Scope: builds

- `apps/web` (existing SvelteKit 5 app, Svelte 5 runes, Vite, adapter-node).
- Logto login as a single-page app (authorization code with PKCE in the
  browser via `@logto/browser`), access tokens for the API resource, refresh
  in the client. No server-side session, no server-side secrets.
- Pages: sign-in, projects list, project detail (status, signals, events,
  cost, snapshots), config (menu and raw fragment editor with build log and
  errors), secrets, billing (card setup, portal link, invoices, usage),
  settings (timezone, notification channels), account (cancel).
- A thin typed API client generated from `interfaces/api.md` shapes in
  `apps/web/src/lib/api/`.
- Dockerfile with a `HEALTHCHECK` and a `/healthz` route so Coolify does a
  rolling deploy.
- Landing page at `/` for every visitor (I-330): what it is, pricing, install
  command.
- Playwright tests against `internal/fakes/api` served over HTTP.

## 3. Scope: does not build

- The API, the catalog contents, menu-to-fragment rendering (all
  05-control-plane-api; the dashboard sends `{menu}` and shows what comes
  back).
- Web terminal to the guest (DECISIONS R4-18, not built).
- Preview URLs (DESIGN §7, later).
- Teams, org switching (R5-6).
- Any card form of its own (09-billing owns the Paddle side; the dashboard
  opens Paddle.js with the transaction `POST /billing/checkout` returns and
  links to Paddle's portal, DECISIONS I-289).
- Admin or operator views (`repose-admin`, 05).

## 4. Interfaces

Owns: none.

Consumes: `interfaces/api.md` (all user routes), `interfaces/cli-config.md`
(only to show the same project slugs and names the CLI shows).

## 5. Design detail

### 5.1 Auth

`@logto/browser` `LogtoClient` with `appId` for the dashboard's SPA app,
`resources: ["https://api.repose.herakraft.co"]`, `scopes: ["openid",
"profile", "email", "offline_access"]`. `+layout.ts` checks
`isAuthenticated()`; unauthenticated users see the landing page, whose "Get
started", "Start a free week" and header "Sign in" buttons all call
`signIn(callbackUrl)`; Logto's page offers email or GitHub (I-340). `/callback` handles
`handleSignInCallback`. Every API call does `getAccessToken(resource)` and
sends it as a bearer. Tokens live in memory plus Logto's default storage
(localStorage) for the refresh token; this is the accepted SPA pattern and
the API's audience check is the control.

The dashboard has no `+server.ts` routes except `/healthz`. Nothing in
`apps/web` reads an environment secret; the only build-time env values are
`PUBLIC_API_URL`, `PUBLIC_LOGTO_ENDPOINT`, `PUBLIC_LOGTO_APP_ID`
(the Paddle client token comes from `GET /billing`, not the build,
DECISIONS I-289).

### 5.2 Routes

| Route | Content |
|---|---|
| `/` | landing, signed in or out; signed in, its header and calls to action offer the dashboard instead of sign-in (DECISIONS I-330). Sign-in itself still lands on `/projects`. |
| `/callback` | Logto callback |
| `/projects` | table: name, class, state (dot + word), uptime, agent state, cost today, cost month. Row click → detail. `New project` explains that projects are created from the CLI and shows the install command; there is no create form because a project needs a git remote and a laptop-side sync. A project in `error` shows its `last_error` sentence under the state. Below the table, **Recently destroyed** (`GET /projects/destroyed`, DECISIONS I-168): each destroyed project that still has a snapshot, when it was destroyed, the snapshot's time and size, "restorable until <date> (N days left)", a `name in use` badge when a live project has the slug, and `Restore…`, which opens a name field (the old name, or `<slug>-restored` when taken), posts `POST /projects/restore {project_id, name}` and goes to the new project; a taken name shows under the field. The newest 10 rows show first, and "Show N more" adds 20 at a time (I-333). Hidden when the list is empty or the route answers an error. |
| `/projects/[id]` | header with state and actions (Start, Stop, Destroy with confirm typing the slug); cards: connect (`repose run` and `ssh <slug>.repose`), signals (ssh sessions, tmux clients, agents and their state, docker containers, updated N s ago), cost (today, month, projected month at current run rate, using `GET /usage`), disk (used / allocated, Resize with a size picker), events (list from `GET /events`, newest first, agent icon, summary), snapshots (list, Create, Restore with confirm, restore-as-new with a name field), last build (status, link to config) |
| `/projects/[id]/config` | two tabs: **Menu** and **Nix**. Menu: groups from `GET /catalog` rendered as checkbox lists with descriptions and a search box, plus a "Services" group for things like Postgres and Redis if the catalog has them; Apply sends `{menu}`. Nix: CodeMirror 6 editor with Nix syntax, Apply sends `{fragment}`. Both then open the build log panel (SSE from `/ops/:op/log`), auto-scrolled, and on failure show the error block with the fragment line highlighted in the editor. Revisions list with Re-apply. A `Hold base updates` toggle (PATCH `hold_base_updates`) with the current base version and its changelog. |
| `/projects/[id]/secrets` | list of names with dates; Add (name, value textarea or file upload, client validates the name regex); Delete with confirm. Values are never displayed after save. |
| `/billing` | the plan page (§5.8, DECISIONS I-289, I-290): plan cards and Paddle's checkout while there is no subscription and a seat is free, the waitlist while there is none, and with a subscription the plan, its status, usage of the plan, plan changes, cancellation, Paddle's portal and the invoices. |
| `/settings` | timezone (auto-detected default, select; saved on change, and the detected zone is stored when the account has none), email notifications toggle (saved on change), ntfy URL field with its own Save and a "Send test" button (I-332) (calls `POST /me/notify-test`, added to `interfaces/api.md` by this workstream if missing: see §6), install command, SSH config hint. Since I-578 it also carries what `/account` had: the account section first (handle, email, GitHub login, `id="account"`), machine.nix (`#machine-nix`), and Delete account last (`#delete-account`). |
| `/account` | a permanent redirect (308) to `/settings`, whose sections took its content: handle, email, GitHub login, Delete account (types handle, calls `DELETE /me`, explains 30-day retention) (I-578). |
| `/healthz` | `200 ok` |

### 5.3 State and polling

Svelte 5 runes stores in `src/lib/state/`. The projects list and project
detail poll `GET /projects` / `GET /projects/:id` every 10 seconds while the
tab is visible (`document.visibilityState`), and every 60 seconds otherwise.
Ops (start, stop, build) poll `GET /ops/:op` every 2 seconds until done.
Build logs use `EventSource` with the bearer token passed as
`?access_token=` (the API accepts it on the SSE route only; recorded in
`interfaces/api.md` by 05).

### 5.4 Menu → fragment

The dashboard never renders Nix from the menu. It sends `{menu:
MenuSelection}` where `MenuSelection = {packages: [catalog id], services:
[catalog id], options: {[catalog id]: value}}`, and displays the fragment the
API stored (`GET /config` returns both). Switching from Menu to Nix tab after
a menu apply shows the generated fragment read-only with an "Edit as Nix"
button that copies it into the editor and marks the project as
hand-edited (the API sets `menu = null` on a fragment apply, and the Menu tab
then shows "This project's config was edited by hand; applying from the menu
will replace it").

### 5.5 Errors

One failure is said once, in the words `errorText` (`lib/api/errors.ts`)
gives it: the api's `message`, except for `internal`, whose bare "internal
error" becomes the caller's sentence plus "The API failed on its side; try
again shortly." (I-390). Where it is said depends on what the page still
has:

- A page's first load that fails leaves nothing to show, so the page shows
  that sentence in a `.banner--error` with a Retry (`LoadState.svelte`,
  I-385) instead of a toast. Retry re-runs the same load and reads
  "Retrying…" while it does.
- A later refresh that fails, and any action that fails, keeps the page
  as it is and raises a toast with the sentence. A poll raises it once,
  when it starts failing, and not while the load banner or the outage bar
  already says it; the first tick that gets through dismisses it (I-393).
- `payment_required` on Start (and on a resize) renders an inline banner
  with the api's sentence, a link to `/billing` named for `detail.reason`
  (§5.8), and for `plan_limit` a Stop for each machine `detail.projects`
  names that is this user's; `capacity` renders "No capacity right now, try
  again in a few minutes"; `rate_limited` waits and retries once.

A request that gets no answer raises a persistent bar, "Cannot reach the
API. Retrying…"; a 5xx raises it as "The API is failing right now.
Retrying…". A 503 with one of the codes api.md lists as answers
("Errors") raises no bar. Any other answer clears it (§6, I-390, I-393).

### 5.6 Deploy

`apps/web/Dockerfile`: multi-stage, `node:24-alpine`, `pnpm install
--frozen-lockfile`, `pnpm --filter web build`, runtime image runs `node
build` on port 3000 as a non-root user, `HEALTHCHECK CMD wget -qO-
http://127.0.0.1:3000/healthz || exit 1`. Coolify application type "Dockerfile",
health check path `/healthz`, no host port mapping (Traefik routes
`repose.herakraft.co` → 3000), so deploys are rolling. Build args carry
the four `PUBLIC_*` values.

### 5.7 Landing page

One screen: the one-idea sentence, the four-line terminal example from
`DESIGN.md` §2, the two plans from `PRICING.md` (name, price a month, the
memory that runs at once with the Units squares counting its GB, disk,
egress; head "Two plans. Seven days free, card at checkout."), and beside
the "Start with GitHub" button one line from `GET /public/seats`
("12 seats left" while `free > 0`; "Full for now. 41 waiting; join
the list and you're emailed when a seat frees." at 0; nothing when the
fetch fails), install command, "Get started" (I-340). Links to terms,
privacy and refunds (static markdown from `src/content/legal/`,
prerendered; the frontmatter's `status` marks a draft). The rules are
`../LANDING.md`.

### 5.8 The plan page (`/billing`, DECISIONS I-289, I-290)

One `GET /me` and one `GET /billing` on load, invoices only with a
subscription. States, by what `GET /billing` answers, with the element
names the tests use (`data-testid`):

| State | When | What the page shows |
|---|---|---|
| billing off | `503 billing_disabled` | one line, `billing-disabled`: "Billing is not switched on yet." |
| exempt | `me.billing.status = exempt`, no subscription | an ok banner "This account is billing-exempt. No plan is needed." over the plan cards |
| plan cards | no subscription, some `plans[].available` | `seats-line` ("18 seats left."), then `plan-solo` and `plan-pro` from `plans`: name, `price_cents` a month, "Running at once" (`memory_gb`: one large, or two small / one xl, two large, or any mix), disk, egress a month (no project count, I-569), "7 days free, card at checkout, cancel any time.", a "Choose Solo/Pro" button; an unavailable plan's button is disabled with "Needs N seats; M free." |
| held seat | `waitlist.hold_until` in the future | `seat-held` ("Your seat is held until <time> (<n> left). Choose a plan before then.") over the plan cards |
| full | no subscription, no plan available, no hold | `full`: "repose is full", "Every seat is taken and W people are waiting.", `join-waitlist` (`POST /billing/waitlist`); with a place, `waitlist-place`: "You're number N on the waitlist. We'll email <email> when a seat frees; you'll have 72 hours to choose a plan." |
| checkout | "Choose" pressed | `POST /billing/checkout {plan}`; Paddle.js (`https://cdn.paddle.com/paddle/v2/paddle.js`, loaded here only) `Environment.set('sandbox')` when sandbox, `Initialize({token, eventCallback})`, `Checkout.open({transactionId, settings: {displayMode: overlay, theme: the page's scheme, successUrl: /billing?checkout=done}})`; `environment: fake` calls `window.__reposePaddleStub.open` instead (`src/lib/paddle.ts`); `503 waitlisted` reloads into the full state with the place |
| setting up | `checkout.completed`, or `?checkout=done` with no subscription | `setting-up` ("Setting up your plan"), `GET /billing` every 2 s up to 60 s until `subscription` is set, then the plan; after 60 s a warn banner and the cards again |
| plan | `subscription` set | `plan`: name, price a month, `plan-status` ("Trial. First charge of $29 on <date>." / "Active. Renews <date>." / "Payment past due since <date>." / "Cancelled. Ends <date>; machines stop then and snapshots stay 30 days." plus "Changes to Solo on <date>." with `scheduled_plan`); three `Meter` bars `meter-running-now` (`running_gb` of `memory_gb`), `meter-disk-held` (`disk_held_gb`, else `disk_allocated_gb`, of `disk_gb`; over it, "Creating, restoring and forking projects, and growing a disk, are refused until your projects hold less. A deleted file stops counting within a day, or when its machine stops.", I-585), `meter-egress-this-period` (with "Over by N GB: $x on the next invoice at $0.05 a GB." when `overage_cents > 0`); no project count (I-569); buttons Change plan, Cancel plan, "Manage card and receipts" (`POST /billing/portal`); Resume plan instead of the first two while `cancel_at` is set |
| past due | `subscription.status = past_due` | `status-past-due` banner with "Update card" (`POST /billing/portal {"for":"payment_method"}`) over the plan |
| suspended | `me.billing.status = suspended` | `status-suspended` banner with "Update card and pay" over the plan |
| change plan | Change plan pressed | `change-plan`: the other plan's numbers; "Upgrade to Pro" (at once, prorated) or "Downgrade to Solo" (at the renewal; what runs and what the projects hold has to fit); with a `scheduled_plan`, "Keep <current>" (`POST /billing/plan {plan: current}`); a `409 conflict` (`over_plan`, `no_seat`) shows its message in `change-error` |
| cancel | Cancel plan pressed | `confirm-cancel`: the end date (the trial's end while trialing), "Cancel plan" (`POST /billing/cancel`), "Keep it" |
| invoices | with a subscription | the list from `GET /billing/invoices`: date, number, status badge, amount (tax in brackets), a PDF link (`pdf_url`, else `hosted_url` as View); "No invoices yet. The first comes with the first charge." |

Every state ends with the tax line and a link to `/refunds`. The page is
one column at 390 wide (the two cards stack under 480px) and `max-w-2xl`
at 1440; every state was captured at both widths in both colour schemes
before it was called done (2026-09-27).

Elsewhere: the project page's `refusal` banner (§5.5) and its Plan card
(the class's memory of the plan's `limits.memory_gb`, a link to Billing);
the projects list's empty state shows `seat-held`, `waitlist-place` or
`no-plan` from `GET /me`; an idle project's note says "idle <time> · still
running"; the list has no cost columns.

## 6. Failure modes

| Situation | Outcome |
|---|---|
| Logto sign-in fails or is cancelled | back to landing with a toast `Sign-in was cancelled or failed; try again.` |
| Access token refresh fails | sign out, redirect to landing, toast `Session expired, sign in again.` |
| API 5xx or unreachable | persistent bar, polling continues with backoff to 60 s; a 503 with a code api.md lists as an answer is not an outage, and a 5xx says the api is failing rather than unreachable (I-390) |
| Build fails | error block under the editor, fragment line highlighted, revision marked failed, Apply re-enabled |
| Start refused for a billing reason | inline banner with the api's sentence and the link the reason wants (§5.8); button stays enabled |
| Destroy typed wrong | button disabled until the slug matches exactly |
| Secret value over 64 KB | client-side error before sending |
| `POST /me/notify-test` missing on the API | button shows `Test not available yet`; this workstream files the route in `interfaces/api.md` and DECISIONS I-n |
| `/healthz` fails in the container | Coolify does not switch traffic; old container keeps serving |

## 7. Testing

- Unit (Vitest): API client error mapping, menu selection state, remote
  normalisation display, cost projection math.
- Component tests for the config editor error highlighting and the destroy
  confirm.
- Playwright end to end against `internal/fakes/api` (Go, run as a test
  fixture on a port) with a fake Logto (a tiny OIDC stub in
  `test/fake-logto/` that issues signed tokens the fake API accepts): sign
  in, see projects, open detail, start and stop, apply menu, apply broken
  fragment and see the error, add and delete a secret, set ntfy URL.
- Docker build in CI and a `HEALTHCHECK` probe.
- Real: after M3, walk every page against production with a real account
  and record it in `STATUS.md`.

## 8. Rollback

Coolify keeps previous images; roll back to the previous one from its
deployment history. That is the one deploy that is not automatic — a push
to `main` redeploys forward on its own (`docs/ops/coolify.md` fact 16),
so a rollback is always a deliberate act. The dashboard holds no state,
so there is nothing to migrate. If a new dashboard depends on
an API route the API does not have yet, the page must degrade (see the
notify-test row), never break the whole app.

## 9. Checklist

Closed 2026-09-20/21 by the m3-web session unless marked otherwise. Two
suites back most of it: `apps/web/tests/` against `internal/fakes/api`
(28 tests), and `apps/web/tests-live/` against the deployed dashboard
(14, `playwright.live.config.ts`).

- [x] Every route in 5.2 exists and renders with the fake API. Evidence:
      `tests/routes.spec.ts`, one test per route, in a 28/28 run.
- [ ] Sign-in, callback, token refresh and sign-out work against the real
      Logto with the GitHub connector. Evidence: recording or transcript.
      **Open, and the only 08 row that needs a person.** The handover to
      the real Logto is closed against production (`tests-live/
      public.spec.ts`, the sign-in experience offering GitHub, a foreign
      `redirect_uri` refused); the GitHub click is not, because this
      session cannot sign in. The remaining seven tests are written and
      skip with the command that arms them:
      `pnpm --filter web run live:auth` once, then `run live`. — waits on:
      owner (the GitHub sign-in in a browser: `pnpm --filter web run
      live:auth` from apps/web, then `run live`; CHECKLIST-AUDIT.md "Waits on
      the owner").
- [x] No server routes other than `/healthz`; no `$env/static/private` or
      `$env/dynamic/private` imports anywhere. Evidence: `rg 'env/static/private|env/dynamic/private|\+server\.ts' apps/web/src`
      returns only `src/routes/healthz/+server.ts`, and
      `tests-live/public.spec.ts` "the deploy serves no api of its own"
      asserts the same of production.
- [x] Projects list and detail poll at 10 s visible / 60 s hidden and stop
      when the tab is closed. Evidence: `src/lib/poll.test.ts`, 8 tests on
      fake timers — the 10 s and 60 s intervals, the 60 s backoff while the
      api is unreachable (§6), a visibility change rescheduling at once
      rather than waiting out the old interval, `stop()` ending it, and a
      slow poll not overlapping itself.
- [x] Start, Stop, Destroy, Resize call the right routes and show op
      progress. Evidence: `tests/project-lifecycle.spec.ts`.
- [x] A destroyed project is listed under "Recently destroyed" with its
      expiry, and Restore brings it back as a new project (I-168).
      Evidence: `tests/project-lifecycle.spec.ts` "a destroyed project can
      be restored from the projects list", `src/lib/destroyed.test.ts`
      (5 tests); 31 of 31 Playwright tests pass on 2026-09-23. Lighthouse
      on the new section not run (no `lighthouse` binary on the dev box).
- [x] Menu tab renders every catalog group and search filters it; Apply
      sends `{menu}` and shows the generated fragment. Evidence:
      `tests/config.spec.ts`, two tests.
- [x] Nix tab: editor with Nix highlighting, Apply, build log streams, a
      failing fragment shows the error block with the line highlighted.
      Evidence: `tests/config.spec.ts` against the fake's
      `repose-force-eval-error` marker (I-80).
- [x] Hold base updates toggle round-trips. Evidence:
      `tests/config.spec.ts`.
- [x] Secrets: add via text and via file, list, delete; values never appear
      in the DOM after save. Evidence: `tests/secrets.spec.ts`, five tests.
      The file path is its own: it stores a deliberately non-UTF-8 byte
      sequence, asserts the base64 never reaches the DOM, reads the name
      back from the api, and refuses a file over 64 KB.
- [x] Billing (§5.8, superseding the Stripe row with DECISIONS I-289):
      every state of the plan page against the fake. Evidence:
      `tests/billing.spec.ts` 13/13 on 2026-09-27 (billing off; the plan
      cards from `GET /billing`; one seat free; a checkout through the
      Paddle stub to a trialing plan; `?checkout=done`; full, the waitlist
      and the place; the held seat; trial with the overage line; active
      with Paddle invoices and the portal; past due and suspended; upgrade
      and the `over_plan` conflict; a scheduled downgrade and its undo;
      cancel and resume), `tests/landing.spec.ts` 3/3 (the plans, the seats
      line free and full, the Refunds link), `tests/routes.spec.ts`
      "/terms, /privacy and /refunds render". Captures of each state at
      390 and 1440, light and dark, were looked at at 1x before closing.
- [ ] Billing against Paddle's sandbox: a checkout with a test card through
      the real overlay makes the account `trial`; the CSP admits Paddle.js
      and its frame. Evidence: a recording against the deployed dashboard
      with `PADDLE_*` set (`docs/ops/M4-GATE.md`). Open, waits on the owner
      for the sandbox keys.
- [x] The six `payment_required` reasons on the project page (§5.5).
      Evidence: `tests/failure-modes.spec.ts` 7/7 on 2026-09-27
      (`subscription_required` with "Choose a plan", no reason with
      "Billing", `plan_limit` naming the machine with a Stop that works,
      `disk_limit` on a resize, `egress_limit`, `past_due`, `suspended`).
- [x] Settings: timezone, email toggle, ntfy URL, test button. Evidence:
      `tests/settings-account.spec.ts`.
- [x] Account deletion flow requires typing the handle and explains
      retention. Evidence: `tests/settings-account.spec.ts`; the live
      suite asserts the same guard against the real account without
      pressing it.
- [ ] Every failure row in §6 is exercised. Evidence: Playwright tests
      named after the rows. **Six of eight.** Covered: a cancelled or
      failed sign-in (`tests/auth.spec.ts` and, against the real Logto,
      `tests-live/public.spec.ts` for both the bounce-back and the bad
      callback), api 5xx and the persistent bar with its backoff
      (`tests/failure-modes.spec.ts` plus `poll.test.ts`), start with no
      card, capacity, destroy typed wrong, and a secret over 64 KB on
      both input paths. Not covered: **access-token refresh failure**,
      which the fake Logto cannot be made to refuse, and **`POST
      /me/notify-test` missing**, which the fake always answers — both
      need a fake that can fail on demand, not a new page behaviour. — open:
      tests for access-token refresh failure and a missing `POST
      /me/notify-test`, which need the fake Logto and `internal/fakes/api` to
      fail on demand.
- [x] Dockerfile builds in CI, runs as non-root, `HEALTHCHECK` passes, image
      under 200 MB. Evidence: the `web` CI job builds it, checks the size
      and probes `/healthz`; and on the running production container,
      `uid=100(repose) gid=101(repose)`, image **170.8 MB**, health
      `healthy`.
- [x] Coolify deploy is rolling: deploy twice while `curl` loops against
      the domain, zero non-200 responses. Evidence: **closed as measured,
      and it is not zero.** `ops/deploy-probe.sh`, three loops at 5/s,
      29,841 responses, 7 switchovers (5 `web`, 2 `api`): every one loses
      one or two requests per client, at a delay fixed per application
      (`web` +11/+12 s, `api` +26 s — each image's `HEALTHCHECK` start
      period, so the loss is at the *removal* of the old container, not a
      health-check failure). Almost all are 5 s hangs; one was a 502. The
      table and the cause are `docs/ops/coolify.md` fact 13; the drain is
      a proxy-side question with the owner.
- [x] Landing page contains the install command, pricing table matching
      `PRICING.md`, and links to terms and privacy. Evidence:
      `tests-live/public.spec.ts` against production — the three tier rows
      checked cell by cell against `docs/PRICING.md`, the storage, egress
      and trial lines, both legal links followed, and a full-page
      screenshot attached to the run. `/install.sh` is asserted to return
      the real script (I-98).
- [x] Lighthouse accessibility score 90 or higher on `/projects` and
      `/projects/[id]/config`. Evidence (re-run 2026-09-30 under I-389; the
      earlier "100 on both" came from the signed-out landing page the
      dashboard redirected Lighthouse to, so it proved nothing about these
      two pages): `pnpm a11y` with Lighthouse 13.5.0, in a Playwright
      persistent context that is signed in, asserting each audited path:
      **52 passed** (13 pages x light and dark x 1440 and 390). `/projects`
      and `/projects/[id]/config` score **100** in all four runs each, with
      no failed binary audit; the config runs first had to find the Menu
      tab (I-388) and then an unnamed CodeMirror textbox, both fixed
      (I-391). The one tolerated failure was colour contrast on `/` at 390
      (score 96), named in `KNOWN_FAILURES`; the Editor capture's rows
      are inert since I-400, and the list is empty. The audit runs in CI with its reports uploaded, because
      a score checked once by hand drifts.
- [x] `features/config.md`, `features/secrets.md`, `features/snapshots.md`
      match what the pages do. Evidence: re-read; and two *other* feature
      docs did not match and were corrected rather than left
      (`status-and-logs.md` and `notifications.md` promised a dashboard
      nobody specified — DECISIONS I-96).
- [x] `ops/RUNBOOK.md` has: dashboard up but API bar showing, sign-in loop.
      Evidence: both entries exist under those names.
