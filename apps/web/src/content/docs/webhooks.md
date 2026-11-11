---
title: Webhooks
description: Public HTTPS URLs that deliver a provider's events to a port inside your machine, with the body and the signature headers unchanged.
section: Using repose
order: 12.5
---

A webhook route is a public HTTPS URL that forwards to a port and path inside your machine. Stripe, GitHub, Clerk, Twilio and the rest POST to it; repose passes the body and the headers through unchanged, so the app's own signature check passes.

```
$ repose webhooks add stripe 3000/api/webhooks/stripe
https://hooks.repose.herakraft.co/<project-id>/stripe/<token>
```

The URL is about 110 characters: the hostname, your project's id, the route's name and a token.

Paste that into the provider's dashboard. The command prints the URL and nothing else, so it pipes: `repose webhooks add stripe 3000 | pbcopy`.

The token is 128 random bits, which makes the URL a credential: anyone who has it can deliver to that route, so it belongs in the provider's dashboard and nowhere public. A wrong token is refused at the edge and never reaches your machine.

## The routes you have

```
$ repose webhooks
NAME     TARGET                      START  LAST DELIVERY  STATUS
stripe   :3000/api/webhooks/stripe   off    2m ago         200
github   :3000/api/webhooks/github   on     1h04m ago      503 stopped
twilio   :4000/sms                   off    never
```

`repose webhooks url stripe` prints one URL again. `--json` adds the last 20 deliveries, each with its status, outcome, duration and byte counts. The dashboard has the same list under **Webhooks** on a project's Config tab, with Copy, Rotate and Remove.

A project can have 20 routes.

## Rotating and removing

`repose webhooks rotate stripe` mints a new token at once. The old URL answers 410 from that moment, for 30 days, and 404 after that.

For a change with no gap, add a second route to the same target, paste its URL into the provider, then remove the first:

```
repose webhooks add stripe-2 3000/api/webhooks/stripe
repose webhooks rm stripe
```

`repose webhooks rm` removes a route; its URL answers 410 the same way. Destroying a project removes its routes, and [`repose restore`](/docs/lifecycle#destroy-and-restore) does not bring them back: restore the project, add the routes again, and paste the new URLs.

## What your app receives

The method, the query string and the body arrive as the provider sent them, byte for byte, including a compressed body. The headers arrive too, except the ones HTTP requires a proxy to drop, and:

| Header | What it carries |
| ------ | --------------- |
| `Host` | `localhost:<port>`, so a Vite dev server answers (it refuses hosts it doesn't know). |
| `X-Forwarded-Host` | The public hostname. |
| `X-Forwarded-For` | The sender's address. |
| `Repose-Webhook` | The route's name. |
| `Repose-Delivery` | A new id for this delivery. |
| `Repose-Webhook-URL` | The full public URL, query included. |

A `Repose-*` header a sender set is replaced.

Twilio signs the full URL rather than the body, and its helpers take that URL as an argument. Use `Repose-Webhook-URL`:

```js
const url = req.get('Repose-Webhook-URL');
const sig = req.get('X-Twilio-Signature');
twilio.validateRequest(authToken, sig, url, req.body);
```

```python
url = request.headers['Repose-Webhook-URL']
sig = request.headers['X-Twilio-Signature']
validator.validate(url, request.form, sig)
```

## What your app may answer

A reply is for a provider, not a browser. The status passes through and so does the body, up to 64 KB, when its `Content-Type` is JSON, XML, plain text or form data. Slack's `url_verification` challenge, Discord's interaction answers and TwiML all fit.

Anything else has its body dropped, with `Repose-Body-Dropped: type` on the reply. A redirect becomes a 502, which is what every provider would make of it anyway. `Set-Cookie` is removed.

## Frozen and stopped machines

A [frozen](/docs/lifecycle#frozen-machines) machine wakes and takes the delivery.

A stopped one answers 503 with `Retry-After: 3600`. Most providers retry for hours or days and the delivery lands when you start the machine again. `--start` lets a delivery start the machine instead, at most once every 10 minutes; that delivery still gets a 503, and the provider's retry finds the machine running.

```
repose webhooks add github 3000/api/webhooks/github --start
```

`--start` is off by default: `repose stop` is a deliberate act that frees [plan memory](/docs/limits#your-plan), and a route that undid it on every delivery would surprise you.

What it costs to leave it off, by provider:

| Provider | While your machine is stopped |
| -------- | ----------------------------- |
| GitHub | Never retries. Use its Redeliver button when you're back. |
| Slack | Can disable the subscription after an hour of failures. |
| Shopify | Removes the subscription after 8 failures in 4 hours. |
| Polar | Disables the endpoint after 10 failures in a row. |
| Stripe, Svix (Clerk, Resend), Linear | Retry for hours or days; the delivery lands. |

Deliveries don't count as using a machine, so one that only receives webhooks is still announced as [unused](/docs/lifecycle#unused-machines).

## Limits

| | |
| --- | --- |
| Request body | 1 MB. A larger one gets 413 and never reaches the machine. |
| Reply body | 64 KB, JSON, XML, plain text or form data. |
| Reply time | 30 seconds from the last request byte. Past it, 504. |
| Rate | 10 deliveries a second per route, 30 per project, 8 at once. Past them, 429. |
| Routes | 20 per project. |

Reply bytes count against your plan's [egress](/docs/limits#egress).

## When something goes wrong

The status column of `repose webhooks` is what the provider saw.

| What you see | What happened |
| ------------ | ------------- |
| `502 no_listener` | Nothing is listening on that port inside the machine. |
| `504 timeout` | The app took longer than 30 seconds. |
| `503 stopped` | The machine is stopped. |
| `503 wake_failed` | The machine is frozen and did not wake. |
| `503 start_refused` | `--start` fired and the start was refused, usually for plan memory. |
| `429 rate_limited` | Past a rate above. |
| `200 body_dropped` | The app answered with a type a route may not pass back. |

repose records a delivery's route, status, outcome, duration and byte counts. It never stores or logs the URL, the query, a header or the body, and it keeps nothing when a delivery fails: there is no queue to replay from, and the provider's own retry is what delivers it.

A route is a door into your machine for a provider's events, not a way to serve a site, and what you may run on a machine is unchanged: see [acceptable use](/docs/limits#what-isnt-allowed).
