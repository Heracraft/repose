---
title: API
description: Call repose from an agent or a script with an API token, what each scope opens, and the routes.
section: Reference
order: 40.5
---

Everything the CLI and the dashboard do goes through one HTTP API, and an API token lets an agent or a script call it as you. An agent on your laptop can make a project, add a trigger and read what last night's runs did; a CI job can stop a machine; a script can set a secret on every project.

## Make a token

```
$ repose tokens create ci --scope read,triggers
rpt_9qK2…
ci: read, triggers, every project, until 2026-12-18. Not shown again.
```

Or on the dashboard, Settings → API tokens → Create token. Either way the token is shown once. It is 47 characters and starts with `rpt_`, so a secret scanner can match it.

A token has a name, one or more scopes, a project or all of them, and an end: 30 days unless you say otherwise, a year at most. An account holds 20 live tokens. [`repose tokens list`](/docs/cli#api-tokens) shows each one's scopes, project, end and last use; [`repose tokens rm`](/docs/cli#api-tokens) revokes one, and every call with it is refused from that moment.

## Scopes

| Scope      | What it opens                                                            |
| ---------- | ------------------------------------------------------------------------ |
| `read`     | read every project, its status, events, questions, triggers and settings |
| `projects` | create, start, stop, resize, snapshot, restore and remove projects       |
| `config`   | change machine.nix and a project's config                                |
| `secrets`  | set and remove secrets (never read a value)                              |
| `triggers` | make, change, fire and remove triggers and webhook routes                |
| `agents`   | message an agent and answer its questions                                |

A token bound to one project sees that project alone: `GET /projects` lists only it, and a call on another project or on the account is refused.

No token reaches your account's settings, billing, SSH certificates, the browser view or other tokens. Those take a login.

## Call the API

```
curl https://api.repose.run/v1/projects \
  -H "Authorization: Bearer $REPOSE_TOKEN"
```

JSON in, JSON out. Ids are UUIDs; times are RFC 3339 in UTC. A refusal is `{"error": {"code", "message", "detail"}}`: `401 unauthenticated` for a token that expired or was revoked (`detail.reason` is `token_expired` or `token_revoked`), `403 forbidden` for a route the token's scopes do not open (`token_scope`, with `detail.needs`), another project (`token_project`) or the account's own routes (`token_never`). Every answer carries `X-Request-Id`. Reads are limited to 600 a minute per account and writes to 60; a 429 says `Retry-After`.

The CLI is a client of the same API. Set `REPOSE_TOKEN` and it uses the token in place of your login:

```
$ export REPOSE_TOKEN=rpt_9qK2…
$ repose triggers add nightly --every "daily 02:00" -p "Run the tests"
```

`repose login --status` then names the token the api sees, with its scopes, project and end. Commands that open SSH (`run`, `attach`, `exec`, `ssh`, `cp`) need a login.

## Routes

The scope each route needs. `:id` is a project's id, which `GET /projects` lists.

| Scope      | Routes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| any        | `GET /me`, which with a token adds `token: {name, scopes, project, expires_at}`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `read`     | `GET /projects`, `GET /projects/:id`, `GET /projects/destroyed`, `GET /projects/:id/events`, `GET /projects/:id/logs`, `GET /projects/:id/samples`, `GET /projects/:id/ops/:op_id`, `GET /projects/:id/ops/:op_id/log`, `GET /projects/:id/config`, `GET /projects/:id/config/revisions`, `GET /me/config`, `GET /me/config/revisions`, `GET /projects/:id/secrets`, `GET /me/secrets` (names, never values), `GET /projects/:id/webhooks`, `GET /projects/:id/webhooks/:name`, `GET /projects/:id/triggers`, `GET /projects/:id/triggers/:name`, `GET /projects/:id/trigger-runs`, `GET /projects/:id/trigger-runs/:run_id`, `GET /trigger-runs`, `GET /projects/:id/snapshots`, `GET /snapshots`, `GET /projects/:id/questions`, `GET /questions`, `GET /projects/:id/route`, `GET /catalog` |
| `projects` | `POST /projects`, `PATCH /projects/:id`, `DELETE /projects/:id`, `POST /projects/:id/start`, `POST /projects/:id/stop`, `POST /projects/:id/resize`, `POST /projects/restore`, `POST /projects/:id/snapshots`, `POST /projects/:id/snapshots/:sid/restore`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `config`   | `PUT /projects/:id/config`, `POST /projects/:id/config/revisions/:rev/apply`, `PUT /me/config`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `secrets`  | `PUT /projects/:id/secrets/:name`, `DELETE /projects/:id/secrets/:name`, `PUT /me/secrets/:name`, `DELETE /me/secrets/:name`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `triggers` | `POST /projects/:id/triggers`, `DELETE /projects/:id/triggers/:name`, `POST /projects/:id/triggers/:name/fire`, `POST /projects/:id/webhooks`, `DELETE /projects/:id/webhooks/:name`, `POST /projects/:id/webhooks/:name/rotate`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| `agents`   | `POST /projects/:id/say`, `POST /projects/:id/questions/:qid/answer`, `POST /projects/:id/questions/:qid/cancel`, `POST /projects/:id/questions/:qid/hold`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |

The bodies and answers are what the CLI sends and reads: `--json` on the matching command prints the answer's shape, and `-v` prints each request with its status.

## Give an agent a token

An agent on your laptop reads `REPOSE_TOKEN` from its environment like any other variable. An agent on a repose machine has no `repose` CLI; give it the token as a [secret](/docs/secrets) of that project (`repose secrets set REPOSE_TOKEN --project NAME`) and it calls the api with `curl`. A token bound to that project, with `read` and `triggers`, lets the agent read its own runs and add a trigger without reaching the rest of your account.
