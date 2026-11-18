# API tokens

A user mints a token that lets an agent or a script call the api as
them, for the areas its scopes name, for one project or all, until it
expires. The api stores the token's sha256 and shows the token once. The
decisions are I-1314, I-1316, I-1318, I-1320, I-1322 and I-1324.

User-facing documentation is `apps/web/src/content/docs/api.md` (the
page a token's holder reads) and the `API tokens` section of `cli.md`.

## 1. The shape

```
rpt_<43 characters of base64url>        47 characters, 256 random bits
```

`rpt_` is what `authed` looks for: a bearer with it is looked up here,
without it at Logto's JWKS. It is also what a secret scanner matches.

A row (`api_tokens`, 0046): `id`, `user_id`, `name`
(`[a-z0-9][a-z0-9-]{0,31}`, free among the account's live tokens),
`prefix` (the first 12 characters, shown in lists), `token_sha256`,
`scopes` (text[]), `project_id` (null for the account), `expires_at`,
`created_at`, `last_used_at` (written at most once a minute per token),
`revoked_at` (the row stays 30 days so a list can say "revoked").

## 2. The CLI

```
$ repose tokens create ci --scope read,triggers
rpt_…                                  ← stdout, the token alone
ci: read, triggers, every project, until 2026-12-18. Not shown again.   ← stderr
$ repose tokens create deploy --scope read,projects --project todo-app --expires 90
$ repose tokens                        ← the list; ls, --json, -q
NAME    SCOPES          PROJECT   EXPIRES     LAST USED
deploy  read,projects   todo-app  2027-02-16  never
ci      read,triggers   all       2026-12-18  3m ago
$ repose tokens rm ci
```

`REPOSE_TOKEN` makes the CLI a token's caller: the token is sent as the
bearer, never refreshed, and a 401 prints `REPOSE_TOKEN was refused:
<the api's reason>.` with exit 3. `repose login --status` prints `you
with token ci (read, triggers, every project, until 2026-12-18) on
api.repose.run`. The three `tokens` commands refuse a token caller with
`Tokens are made with a login, not with REPOSE_TOKEN.` Commands that
mint an SSH certificate (`run`, `attach`, `exec`, `ssh`, `cp`) meet the
api's `token_never` and print its sentence.

## 3. The dashboard

Settings → API tokens, the fifth tab (I-1322, amending I-841's four).
A list (name and prefix, scopes, project, expires, last used, Revoke…
with the two-step confirm the Secrets tab uses; a revoked row stays,
greyed, with "Revoked 3m ago") and a Create token dialog: name, the six
scopes as checkboxes each with its one-line meaning, a project select
(All projects, or one), an expiry select (7, 30, 90 days, a year). On
create the dialog turns into the one view of the token, selectable,
with Copy focused and "Copy it now. It is not shown again." Nothing on
the page holds the token after Done.

## 4. The routes

`GET /me/tokens`, `POST /me/tokens`, `DELETE /me/tokens/:name` (api.md
"Users"). A login's routes: `tokenNever` lists them, so a token cannot
mint tokens.

## 5. The policy

`internal/api/http/tokens.go` holds one map from mux pattern to scope
(`tokenRouteScope`), one from pattern to the reason no token opens it
(`tokenNever`), and the three account-wide patterns a project-bound
token may still call (`tokenAccountWide`: `GET /me`, `GET /catalog`,
`GET /projects`, the last narrowed to the one project in `listProjects`).
`TestEveryUserRouteHasATokenPolicy` fails when a user route behind
`authed` is in neither map, so a new route is closed to tokens until it
is placed. `tokenCheck` runs inside `authed` after the row is loaded and
before the rate limit, which stays per user.

| Scope | Opens |
|---|---|
| `read` | every GET except billing, usage and the token list |
| `projects` | create, restore, patch, destroy, start, stop, resize, snapshot, snapshot restore |
| `config` | `PUT /me/config`, a project's config put and revision apply |
| `secrets` | put and delete, account and project (never a value: no route answers one) |
| `triggers` | triggers put, delete, fire; webhooks put, delete, rotate |
| `agents` | say; a question's answer, cancel, hold |
| never | `PATCH/DELETE /me`, notify-test, certs, browser view, billing, usage, tokens |

Refusals (api.md): `401 unauthenticated` with `detail.reason`
`token_expired` or `token_revoked`; `403 forbidden` with `token_scope`
(`detail.needs`, `detail.scopes`), `token_project`, or `token_never`.
The message names the token's scopes and the one the call needs.

## 6. What a token is not

- Not a login: no SSH certificate, no browser view, no money, no
  account changes. An agent that needs a machine's shell runs on the
  machine; one that needs a certificate is a person with `repose login`.
- Not readable: no route answers a token or a secret's value, and the
  logs carry `token_id`, never the token.
- Not a Logto client. Logto's M2M apps are the api's own identity to
  Logto's management API; a user's tokens are rows the api owns, so a
  revoke is one UPDATE and an expiry is one comparison.
- Not rotated by name. A new token is `create`; the old one is `rm`.
  Rejected for this version: a `rotate` that keeps the name, because a
  script that holds the token has to be told the new one either way.

## 7. Limits

20 live tokens an account. 30 days unless asked, 365 at most. A
revoked row is listed for 30 days. Rate limits are the user's, whichever
bearer a call carries.

## 8. Checklist

- [ ] `TestAPITokens`, `TestAPITokenExpires`, `TestEveryUserRouteHasATokenPolicy` (internal/api/http) pass in `nix develop`.
- [ ] `TestTokensCreateListRm` (internal/cli) passes; `TestDocsNameEveryCommandAndFlag` and `TestDocsNameEveryEnvVar` pass with the `tokens` noun and `REPOSE_TOKEN`.
- [ ] `tokens.spec.ts` passes against `cmd/fakeapi`, and its shots at 1440 and 390 in both schemes were looked at.
- [ ] `TestPublicAPIDocNamesRealRoutes` (internal/api/apidoc) passes: every route on `/docs/api` is in `docs/interfaces/api.md`.
- [ ] Live: a token made on the dashboard lists a machine with `curl`, `REPOSE_TOKEN=… repose ls` prints it, a `projects` call with a read-only token answers 403 `token_scope`, and `repose tokens rm` makes the next call 401 `token_revoked`.
