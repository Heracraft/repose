# Ports and previews

A dev server running in the guest reaches the user's browser through an SSH
port forward the CLI manages. Public preview URLs per project have a
design below; they are not built, and were not approved on 2026-09-25
(`docs/proposals/2026-09-24-backlog-triage.md`).

## What the user sees

While `repose run` or `repose attach` is attached, every port the guest
starts listening on is on the laptop's localhost too, with no command
(DECISIONS I-199). Start `pnpm dev` in the guest and, inside tmux:

```
⇄ localhost:5173 → :5173                          (a message for 4 s)
... ⇄ 3000 5173 │ "dev@izma" 14:02 23-Sep-26         (the status bar's right side)
```

`http://localhost:5173` on the laptop is the guest's vite, a secure
context, an OAuth-friendly `localhost` redirect URI and the same cookies
as local development, which a preview hostname could not give.

The explicit, foreground form stays for one port, for a laptop without
the session helper (Windows), or with auto-forward off:

```
$ repose open 3000
http://localhost:3000 → todo-app:3000 (Ctrl-C to stop)
```

```
$ repose browser
Watching todo-app's browser at http://localhost:6080/#p=5m2k8Q1p (the view sleeps after 30 idle minutes; repose browser --stop ends it).
```

(browser.md covers the desktop.)

## Behaviour that must hold

Auto-forward (I-199), run by the session helper (run-and-attach.md):

- The helper reads the guest's listeners with `ss -Hltn` over the
  command's multiplexed connection every second. It forwards listeners
  on `127.0.0.1`, `0.0.0.0`, `::1`, `::` and `*`, ports 1024 and up,
  except the guest's own (`forwardPlatformPorts` in
  `internal/cli/forward.go`): 6080, 6081, 5900 (the desktop, which `repose
  browser` forwards), 9224, 9225 (the agents' browser's DevTools
  endpoint, I-246) and 5353, 5355 (mDNS, LLMNR, I-215). A listener only on another address (a
  Docker bridge, the guest's own IP) is not forwarded, and nor is a port
  bound only inside a Docker network.
- A forward is `ssh -O forward -L <laptop>:127.0.0.1:<port>` on the
  existing ControlMaster: no new connection, no process per port. It is
  cancelled (`ssh -O cancel`) within two seconds of the listener closing,
  and every forward is cancelled when the attach ends.
- The laptop port is the guest's port when it is free; otherwise the next
  free one, and the message says so: `⇄ localhost:3001 → :3000 (3000 is
  taken on your laptop)`. Portless's proxy port 1355 is never remapped
  silently, since the URLs portless prints name it: `1355 is taken on
  your laptop (portless?); izma's portless is on localhost:1356`. A port
  no laptop port could be found for is tried again after 30 seconds.
- Output is only ever inside tmux: `display-message` per new forward, and
  the session's `status-right` set to `⇄ <ports> │ <the global
  status-right>` while any attached CLI forwards. Each CLI keeps its
  ports in `~/.repose/forwards/<id>` in the guest (re-stamped every 20 s,
  ignored after 60 s), so two laptops attached to one project each forward
  on their own laptop and the bar shows the union; the session's own
  `status-right` is unset again when the last one leaves.
- `REPOSE_NO_FORWARD=1` turns it off, for a laptop whose ports must stay
  free. It is the only knob.

`repose open`:

- `repose open PORT [--local-port N] [--no-browser]` reads the guest's
  listeners once (`ss -Hltn` over SSH, 5 s bound) and runs `ssh -N -L
  127.0.0.1:<local>:<host>:<port> <slug>.repose`, where host is `::1` when
  the port listens only on `::1` and `127.0.0.1` otherwise, including
  when nothing listens yet, which it says on stderr (DECISIONS I-261).
  It uses the CLI's SSH config, so
  anything the CLI can reach, a plain `ssh -L` can reach too, and opens
  the URL in the default browser unless `--no-browser`. One port per
  invocation; there is no multi-port, `--background` or `--list` form
  (that needs a forwards registry nobody built). `--desktop` and its
  `--stop` are the hidden old name of `repose browser` (I-292).
- The local port defaults to the port number. If it is taken (on the
  laptop's 127.0.0.1, ::1 or wildcard, as auto-forward checks), the CLI
  picks a free one and forwards to that instead, with a message saying
  so, rather than failing. `repose browser` does the same for 6080
  (I-261).
- `open PORT`'s forward runs in the foreground and dies with the CLI
  (Ctrl-C, or the parent process exiting). `repose browser`'s is the one
  exception: an `ssh -N` child in its own session, recorded under
  `~/.config/repose/browser-forwards/`, reused by the next `repose
  browser` and ended by `repose browser --stop` (I-292).
- Anything bound on `0.0.0.0`, `::`, `127.0.0.1` or `::1` in the guest is
  reachable this way. Nothing in the guest is reachable any other way; the guest has no
  inbound path except through the gateway.
- `repose browser` starts the guest's desktop chain over SSH
  (`repose-guest-profile desktop start`, which starts the socket-activated
  system units; there is no user unit, I-241) and forwards 6080 in the
  background, then opens the viewer page with the password in the URL
  fragment (browser.md).

## Outbound connections

A guest reaches the internet through its host's NAT, with the private
ranges, the cloud's metadata services and other guests blocked
(SECURITY.md). Three more limits keep one guest from getting the shared
address blocklisted (DECISIONS I-238..I-240), and the terms' acceptable
use names them:

- **Port 25 is blocked.** Servers hand mail to each other on 25, and it is
  how spam leaves rented machines. Send mail through a provider on its
  submission port, which stays open: `smtp.resend.com:465`,
  `email-smtp.<region>.amazonaws.com:587`, `smtp.postmarkapp.com:587`, or
  its HTTP API. `nc -zv smtp.gmail.com 25` from a guest times out;
  `nc -zv smtp.gmail.com 587` connects.
- **Mining pools' default ports are blocked:** 3333, 5555, 7777, 14433
  and 14444. Nothing a developer commonly reaches remotely listens there
  (4444, Selenium Grid's port, and 8888, Jupyter's, stay open).
- **New connections are rate-limited per guest**, at 200 new outbound
  flows a second with a burst of 2000. A cold package install opens a few
  dozen and a headless browser loading six news sites at once about 1700
  over twenty seconds, so development does not reach it; a scan does.
  Connections past the limit are dropped (the program sees a timeout),
  connections already open are never touched, and traffic to the host's
  npm and Docker caches does not count.

Each blocked attempt is counted for the guest, as a number, and the
operator is alerted when a guest keeps trying; nothing is stopped for
it. A guest running a known cryptocurrency miner is stopped (with a
snapshot); that is in stop-start-destroy.md.

## Not built; not approved on 2026-09-25: preview URLs

`https://3000-todo-app.repose.herakraft.co` reaching port 3000 in the guest,
so a teammate or a phone can see a running dev server and a browser-driving
agent on the laptop can hit it.

Design (DECISIONS R2-6 chose to document it now):

- DNS: wildcard `*.repose.herakraft.co` to the edge. TLS: a wildcard
  certificate from Let's Encrypt via DNS-01, renewed on the edge.
- The gateway process on the edge gains an HTTPS listener. It parses
  `<port>-<slug>.repose.herakraft.co`, resolves `(slug, owner)` through the
  API (`GET /internal/route` extended to accept a slug without a handle and
  return the owner; slugs are unique per user, not globally, so the URL
  form must carry the handle: `3000-todo-app-heracraft.repose.herakraft.co`,
  with the handle as the last segment before the domain). DECISIONS
  I-438: that name cannot be split back when a slug or handle has a dash,
  so the label after the port must be a per-project preview name the API
  keeps unique, chosen when this is built.
- Authentication: a session cookie issued by the dashboard after Logto
  login, scoped to `.repose.herakraft.co`. A request without it redirects
  to the dashboard login with a return URL. The cookie identifies the user;
  the gateway checks that user owns the project. A project can be marked
  `preview: public` by its owner to skip the check for read-only sharing;
  the default is owner-only.
- Proxying: HTTP/1.1 and WebSocket to `guest_ip:<port>` over WireGuard.
  Server-sent events and long polls pass through. Timeouts: 60 seconds idle.
  Response bodies are not buffered.
- Rate limits and abuse: a preview marked public is a way to serve content
  from a guest to the internet, so public previews count against egress and
  are capped at 100 requests per second per project, and the terms say what
  they may not be used for.
- The guest sees requests from the edge's WireGuard address with
  `X-Forwarded-For`, `X-Forwarded-Proto: https`, and `Host` rewritten to
  the preview hostname so frameworks' host checks pass. The guest base sets
  `VITE_ALLOWED_HOSTS` and the Next.js equivalent for `*.repose.herakraft.co`.
- The dashboard shows a project's open ports with a preview link each.

What is *not* in that design: custom domains per project, HTTP basic auth
as an alternative to the cookie, and non-HTTP protocols. Those wait for
demand.

## Depends on

Workstreams 07 (`open`, auto-forward), 04 (listening ports in signals).
Preview URLs would need 06 (proxy), 11 (wildcard DNS and certificate), 05
(route by slug and owner, `preview` flag) and 08 (preview links).

## Deferred

Preview URLs as designed above (not approved). Custom domains. Tunnelling arbitrary TCP.
