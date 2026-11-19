---
title: API
description: Call repose from an agent or a script with an API token, what each scope opens, and the routes.
section: Reference
order: 40.5
---

Everything the CLI and the dashboard do goes through one HTTP API, and an API token lets an agent or a script call it as you. An agent on your laptop can make a project, add a trigger and read what last night's runs did; a CI job can stop a machine; a script can set a secret on every project.

```
curl https://api.repose.run/v1/projects \
  -H "Authorization: Bearer $REPOSE_TOKEN"
```

## Make a token

```
$ repose tokens create ci --scope read,triggers
rpt_9qK2…
ci: read, triggers, all projects, until 2026-12-18. Not shown again.
```

Or on the dashboard, Settings → API tokens → Create token. The token is 47 characters and starts with `rpt_`, so a secret scanner can match it.

A token has a name, one or more scopes, a project or all of them, and an end: 30 days unless you say otherwise, a year at most. An account holds 20 live tokens. [`repose tokens list`](/docs/cli#api-tokens) shows each one's scopes, project, end and last use; [`repose tokens rm`](/docs/cli#api-tokens) revokes one, and every call with it is refused from that moment. A revoked token stays in the list for 30 days.

## Scopes

| Scope      | What it opens                                                      |
| ---------- | ------------------------------------------------------------------ |
| `read`     | every project's status, events, questions, triggers and settings   |
| `projects` | create, start, stop, resize, snapshot, restore and remove projects |
| `config`   | change machine.nix and a project's config                          |
| `secrets`  | set and remove secrets (never read a value)                        |
| `triggers` | make, change, fire and remove triggers and webhook routes          |
| `agents`   | message an agent and answer its questions                          |

A token bound to one project sees that project alone: `GET /projects` lists only it, and a call on another project, or on an account route other than `GET /me`, `GET /catalog` and `GET /projects`, is refused.

No token reaches your account, billing, SSH, the browser view, other tokens, or adding and removing a host of your own. Those take a login.

## Call the API

JSON in, JSON out. Ids are UUIDs; times are RFC 3339 in UTC. A refusal is `{"error": {"code", "message", "detail"}}`: `401 unauthenticated` for a token that expired or was revoked (`detail.reason` is `token_expired` or `token_revoked`), `403 forbidden` for a route the token's scopes do not open (`token_scope`, with `detail.needs`), another project (`token_project`) or the account's own routes (`token_never`). Every answer carries `X-Request-Id`. Reads are limited to 600 a minute per account and writes to 60; a 429 says `Retry-After`.

The CLI is a client of the same API. Set `REPOSE_TOKEN` and it uses the token in place of your login:

```
$ export REPOSE_TOKEN=rpt_9qK2…
$ repose triggers add nightly --every "daily 02:00" -p "Run the tests"
```

`repose login --status` then names the token the api sees, with its scopes, project and end. Commands that open SSH (`run`, `attach`, `exec`, `ssh`, `cp`) need a login.

## Routes

The scope each route needs. `:id` is a project's id, which `GET /projects` lists.

| Route                                            | Scope                                                                         |
| ------------------------------------------------ | ----------------------------------------------------------------------------- |
| `GET /me`                                        | any; with a token it adds `token`, the row `repose tokens list --json` prints |
| `GET /projects`                                  | `read`                                                                        |
| `GET /projects/:id`                              | `read`                                                                        |
| `GET /projects/destroyed`                        | `read`                                                                        |
| `GET /projects/:id/events`                       | `read`                                                                        |
| `GET /projects/:id/logs`                         | `read`                                                                        |
| `GET /projects/:id/samples`                      | `read`                                                                        |
| `GET /projects/:id/ops/:op_id`                   | `read`                                                                        |
| `GET /projects/:id/ops/:op_id/log`               | `read`                                                                        |
| `GET /projects/:id/config`                       | `read`                                                                        |
| `GET /projects/:id/config/revisions`             | `read`                                                                        |
| `GET /me/config`                                 | `read`                                                                        |
| `GET /me/config/revisions`                       | `read`                                                                        |
| `GET /projects/:id/secrets`                      | `read` (names, never values)                                                  |
| `GET /me/secrets`                                | `read` (names, never values)                                                  |
| `GET /projects/:id/webhooks`                     | `read`                                                                        |
| `GET /projects/:id/webhooks/:name`               | `read`                                                                        |
| `GET /projects/:id/triggers`                     | `read`                                                                        |
| `GET /projects/:id/triggers/:name`               | `read`                                                                        |
| `GET /projects/:id/trigger-runs`                 | `read`                                                                        |
| `GET /projects/:id/trigger-runs/:run_id`         | `read`                                                                        |
| `GET /trigger-runs`                              | `read`                                                                        |
| `GET /projects/:id/snapshots`                    | `read`                                                                        |
| `GET /snapshots`                                 | `read`                                                                        |
| `GET /projects/:id/questions`                    | `read`                                                                        |
| `GET /questions`                                 | `read`                                                                        |
| `GET /projects/:id/route`                        | `read`                                                                        |
| `GET /catalog`                                   | `read`                                                                        |
| `GET /hosts`                                     | `read` (the hosts you own)                                                    |
| `POST /projects`                                 | `projects`                                                                    |
| `PATCH /projects/:id`                            | `projects`                                                                    |
| `DELETE /projects/:id`                           | `projects`                                                                    |
| `POST /projects/:id/start`                       | `projects`                                                                    |
| `POST /projects/:id/stop`                        | `projects`                                                                    |
| `POST /projects/:id/resize`                      | `projects`                                                                    |
| `POST /projects/restore`                         | `projects`                                                                    |
| `POST /projects/:id/snapshots`                   | `projects`                                                                    |
| `POST /projects/:id/snapshots/:sid/restore`      | `projects`                                                                    |
| `PUT /projects/:id/config`                       | `config`                                                                      |
| `POST /projects/:id/config/revisions/:rev/apply` | `config`                                                                      |
| `PUT /me/config`                                 | `config`                                                                      |
| `PUT /projects/:id/secrets/:name`                | `secrets`                                                                     |
| `DELETE /projects/:id/secrets/:name`             | `secrets`                                                                     |
| `PUT /me/secrets/:name`                          | `secrets`                                                                     |
| `DELETE /me/secrets/:name`                       | `secrets`                                                                     |
| `POST /projects/:id/triggers`                    | `triggers`                                                                    |
| `DELETE /projects/:id/triggers/:name`            | `triggers`                                                                    |
| `POST /projects/:id/triggers/:name/fire`         | `triggers`                                                                    |
| `POST /projects/:id/webhooks`                    | `triggers`                                                                    |
| `DELETE /projects/:id/webhooks/:name`            | `triggers`                                                                    |
| `POST /projects/:id/webhooks/:name/rotate`       | `triggers`                                                                    |
| `POST /projects/:id/say`                         | `agents`                                                                      |
| `POST /projects/:id/questions/:qid/answer`       | `agents`                                                                      |
| `POST /projects/:id/questions/:qid/cancel`       | `agents`                                                                      |
| `POST /projects/:id/questions/:qid/hold`         | `agents`                                                                      |

Request and answer bodies are not listed here. They are what the CLI sends and reads: `--json` on the matching command prints the answer, and `-v` prints each request it makes, with its status.

## Give an agent a token

An agent on your laptop reads `REPOSE_TOKEN` from its environment like any other variable. An agent on a repose machine has no `repose` CLI; give it the token as a [secret](/docs/secrets) of that project (`repose secrets set REPOSE_TOKEN --project NAME`) and it calls the api with `curl`. A token bound to that project, with `read` and `triggers`, lets the agent read its own runs and add a trigger.
