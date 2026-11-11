# Webhooks

A project can have named webhook routes on one public hostname. Each
route has its own unguessable token and forwards to a port and path
inside the machine. The edge passes body bytes and signature headers
through unchanged, so the app's own provider check passes.

This is the one public path into a guest. It is not a preview: a route
cannot serve a page (§5). The design and the alternatives that lost are
in `docs/proposals/2026-11-10-webhooks.md`; the decisions are I-1110,
I-1112, I-1114, I-1116, I-1118 and I-1120.

User-facing documentation is `apps/web/src/content/docs/webhooks.md`.

## 1. The URL

```
https://hooks.repose.herakraft.co/<project_id>/<route>/<token>[/rest][?query]
```

- `project_id` is the project's UUIDv7. It is stable: a handle can
  change and a destroyed project's slug can be reused, and a URL pasted
  into a provider's dashboard has to outlive both.
- `route` is the name the user chose, `[a-z0-9][a-z0-9-]{0,31}`.
- `token` is 22 characters of base64url, 128 random bits.
- Anything after the token is appended to the target path, and the
  query string passes as it came.

One hostname, routed by path, on one certificate from TLS-ALPN-01 on the
edge's own :443 (I-1112). A subdomain per project would need a wildcard,
which needs DNS-01 and the Cloudflare token that has blocked the preview
certificate since 2026-09-20.

The hostname is kept for good, whatever happens to the product's domain
(R4-12): the URLs live in third parties' dashboards.

## 2. The CLI

`repose webhooks`, with `add`, `list` (and the bare noun), `url`,
`rotate` and `rm`. The noun is `webhooks` and not `hooks`: agent hooks
own that word (`repose-hook`, the edge's `/hooks`, Claude Code's `hooks`
key).

```
$ repose webhooks add stripe 3000/api/webhooks/stripe
https://hooks.repose.herakraft.co/<project_id>/stripe/<token>

$ repose webhooks
NAME     TARGET                      START  LAST DELIVERY  STATUS
stripe   :3000/api/webhooks/stripe   off    2m ago         200
github   :3000/api/webhooks/github   on     1h04m ago      503 stopped
twilio   :4000/sms                   off    never
```

`add` on a name that exists changes the target and `--start` and keeps
the token, so a URL already in a provider's dashboard goes on working.
`rotate` replaces the token at once; the old one is a tombstone. The
output of `add`, `url` and `rotate` is the URL and nothing else, so it
pipes.

`repose status` gains a `webhooks` row for the routes a provider has
called.

## 3. Where a route lives

In the api: `webhook_routes`, `webhook_tombstones`,
`webhook_deliveries`, `webhook_egress` and `webhook_starts`
(`docs/interfaces/db-schema.md`).
I-820's reasoning for `keep_awake` applies in full: a route is
per-project server state, the edge must know it whether or not a laptop
is open, and the api never reads a file from the checkout.

The token is held twice: `sha256(token)` for the edge's lookup, and the
token itself as ciphertext under the account's data key, with the same
envelope as a named secret, so `repose webhooks url` can print it again.
It therefore lives in the two homes CLAUDE.md allows a secret: ciphertext
in Postgres, and the URL on the user's laptop where they put it. A copy
of the edge's memory gives no working URL.

## 4. The delivery path

1. The sender POSTs to the URL. nftables accepts :443 under the
   per-source connection limits (256 open, 100 a second).
2. The gateway's :443 completes TLS. SNI `hooks.repose.herakraft.co`
   selects the `autocert` certificate; any other name falls through to
   the preview stub.
3. The handler parses the path. A malformed one is 404.
4. It looks up `sha256(token)` in the table it refreshes from
   `GET /internal/webhook-routes?since=` every 5 s. No row: 410 if the
   hash is a tombstone, else 404. Nothing has called the api or touched
   a host.
5. Route and project rate limits, then the in-flight count. A
   `Content-Length` over 1 MiB is 413, before any wake.
6. The project's state from the cached `GET /internal/route?project_id=`
   (§6).
7. The gateway's own SSH connection to `guest_ip:22`, with the
   five-minute gateway certificate, and a `direct-tcpip` channel to
   `127.0.0.1:<port>` or `::1:<port>`. No new inbound port on the guest,
   no change to the host's nftables. A connection a delivery opened is
   reused for 30 s.
8. The request is written (§4.1) and the body streamed with a counter.
9. The reply is read under a 30 s timeout, the reply rules applied
   (§4.2), and the status, headers and allowed body streamed back.
10. The delivery goes into the next batch for the api and one line into
    the log (§7).

### 4.1 Request, edge to app

Method as sent; every method passes. Path is the route's target path then
anything after the token; the query unchanged. Body streamed, never
decoded or re-encoded; `Content-Encoding` passes.

Headers pass with their values byte for byte and duplicates of one name
in order, except:

- hop-by-hop fields, which RFC 9110 §7.6.1 requires a proxy to drop;
- `Host`, set to `localhost:<port>`. Vite answers only `localhost`,
  `.localhost` and IP addresses by default, so a dev server would refuse
  the public name. No provider signs `Host` except Twilio, which signs
  the whole URL and is covered below;
- any `Repose-*` header the sender set, replaced by the edge's.

Order *across different field names* is not kept: Go's server parses a
request into a map, so the arrival order is gone before the handler
runs. RFC 9110 §5.3 makes it insignificant, and no provider signs it.

Added: `X-Forwarded-For`, `X-Forwarded-Proto: https`,
`X-Forwarded-Host`, `Repose-Webhook: <route>`, `Repose-Delivery: <uuid>`
and `Repose-Webhook-URL: <the full public URL with query>`. Twilio's
helpers take the URL to check as an argument; the app passes that last
header, which holds the token and reaches only the app, inside the
user's own machine.

A body with no `Content-Length` (chunked) is read into memory up to the
cap and sent with one, because `Transfer-Encoding` is hop-by-hop.

### 4.2 Reply, app to provider

- Status passes through, except 3xx, which becomes 502 with no
  `Location`. Providers count a 3xx as a failure anyway, and a passed
  redirect would make the hostname an open redirect.
- The body passes only for `application/json`, `text/plain`,
  `application/xml`, `text/xml` or
  `application/x-www-form-urlencoded`, and only up to 64 KiB. Anything
  else is dropped with `Repose-Body-Dropped: type`.
- `Set-Cookie` removed. `Content-Security-Policy: sandbox;
  default-src 'none'`, `X-Content-Type-Options: nosniff` and
  `Cache-Control: no-store` added.
- 30 s from the last request byte to the first reply byte.

## 5. Why this is not the preview case

Public previews were not approved because they would serve pages:
phishing kits, malware downloads and live products under repose's name.
A route serves no page. No HTML, no script, no redirect, no cookie;
64 KiB of reply; unguessable URLs with a per-source cap on 404s; rates
sized for events; counted egress; and the edge stores nothing, so a
takedown has no content to remove — the operator removes the route.

What stays open is narrower than `cloudflared`, which is a menu entry
(I-251) and gives any guest a full public URL with HTML, files and no
rate limit.

## 6. Machine states

| State | Answer |
|---|---|
| `running` | Deliver |
| `running`, frozen | `POST /internal/wake` with reason `webhook`, then deliver. Outcome `woke` |
| `stopped`, route `--start` off | 503, `Retry-After: 3600`, outcome `stopped` |
| `stopped`, route `--start` on | The api starts it, at most once per project per 10 minutes; 503, `Retry-After: 60`, outcome `started` or `start_refused` |
| `creating`, `building`, `starting`, `stopping`, `restoring` | 503, `Retry-After: 30` |
| `error`, host unreachable, no address | 503, `Retry-After: 300` |
| destroyed, route removed, token rotated | 410 for 30 days, then 404 |

The start is not held open: a start takes 12 to 14 s, longer than
GitHub's 10 s timeout and far beyond Slack's 3 s, so a held request
would fail at the provider and tie up an edge connection.

A delivery is not a gateway session: nothing is posted to
`/internal/sessions`, so a run of deliveries never holds a machine awake
past the 24 h session window. A woken machine still stays up 15 minutes,
because the samples taken while it rested are not quiet.

Deliveries are not use for the unused notice (I-262), so a machine that
only receives webhooks is still announced as unused.

No store-and-forward. Most signature checks reject a delivery more than
five minutes old, so a stored body is useless when it arrives late, and
a queue on the edge would hold exactly what the logging rule says never
to keep.

## 7. Limits, logging and egress

| Limit | Value |
|---|---|
| Request body | 1 MiB; 413 on a larger `Content-Length`, before any wake |
| Reply body | 64 KiB, the listed types only |
| Reply time | 30 s |
| Per route | 10 a second, burst 50 |
| Per project | 30 a second, 8 at a time |
| Unknown tokens per source /24 | 20 a second, burst 40 |
| New connections on :443 per source | 256 open, 100 a second (nftables) |
| Routes per project | 20 |

Rates are per route and per project, not per source: providers send from
a few shared addresses, so a per-source cap would let one tenant's
Stripe burst throttle every other tenant's. The per-source limits apply
only to unknown tokens and to raw connection counts.

The api keeps the last 20 deliveries per route: `(at, status, outcome,
duration_ms, bytes_in, bytes_out)`. `outcome` is one of `delivered`,
`woke`, `stopped`, `started`, `start_refused`, `wake_failed`,
`no_listener`, `timeout`, `too_large`, `rate_limited`, `body_dropped`,
`unavailable`, `not_found`, `gone`. The api refuses any other word, so
nothing a sender chose reaches a row. The edge batches them to
`POST /internal/webhook-deliveries` every 10 s.

The edge logs one line per delivery, `event=webhook_delivery`, with
`project_id`, `route_id`, `status`, `outcome`, `ms`, `bytes_in`,
`bytes_out` and `source_prefix`. Never the path, which holds the token;
never the query, a header value or a body byte.

Metrics: `repose_gateway_webhook_requests_total{result}`,
`repose_gateway_webhook_wake_seconds`,
`repose_gateway_webhook_inflight` and `repose_gateway_webhook_routes`.

Reply bytes are the project's egress (I-1120). I-1063's `guest_meter`
cannot see them: they leave the guest on port 22 to the edge, where they
look like a terminal's output. The edge counts them and reports them
with each delivery; the api banks them per project per hour in
`webhook_egress`, and the billing rollup adds that hour's bytes to
`coalesce(net_egress, net_tx)`. They are not read off
`webhook_deliveries`, which keeps the newest 20 a route for the user to
look at and would bill twenty of a thousand.

## 8. Not in this version

- `Repose-Signature`. An HMAC key on the edge would be a third holder of
  a tenant-scoped secret, which "Secrets have three homes" forbids. The
  compliant shape, if the owner wants it, is an Ed25519 platform key
  held on the edge as its TLS and SSH host keys are, with the public
  half at `/etc/repose/webhook.pub` in the guest.
- Store-and-forward (§6).
- The route list in the guest's secrets tmpfs.
