# Browser for agents

Every guest has one Chromium that agents drive, headed on a virtual
display, and a desktop viewer that can be switched on when a human needs to
look at or take over that same browser (DECISIONS I-246, I-292). Claude in
Chrome is not available in a guest, and the doc says why; `repose browser
bridge` (I-296) lends the guest's browser tools the laptop's own Chrome
instead.

## What the user sees

Agents just use it:

```
$ repose run "log into the staging site and screenshot the dashboard"
```

Watching or taking over is one command:

```
$ repose browser
Watching todo-app's browser at http://localhost:6080/#p=5m2k8Q1p (the view sleeps after 30 idle minutes).
```

The laptop's browser opens on that link and shows the agent's browser,
live, the size of the tab. Nothing to type: the password is the part after
`#`, which the page reads and the browser never sends anywhere. Click and
type in the page to log in, solve a captcha or approve a passkey; the
agent's next tool call sees the result. The command returns at once; the
forward it started stays up in the background. Run it again and it prints
the same link; `--no-open` prints the link without opening a browser.

```
$ repose browser --stop
Stopped watching todo-app's browser.
```

The view also stops itself after 30 minutes with no client, and opening the
page again wakes it. `repose open --desktop [--stop] [--no-browser]` is the
old name and still works; it says so once and does the same thing.

Lending the agents the laptop's Chrome:

```
$ repose browser bridge
Chrome 144 → todo-app: the agents there browse in your Chrome now, with your logins. Ctrl-C hands them back the machine's browser.
Chrome asks you to allow each new connection.
Pages the agents open are listed below (host and path only).
An agent on todo-app is in your Chrome.
14:03:21  admin.internal.example/signups
^C
Bridge closed. The agents on todo-app are back on the machine's browser.
```

`repose run --bridge` and `repose attach --bridge` keep the same bridge
up beside the attach, reporting through tmux messages. `--allow HOST`
(`--bridge-allow HOST` on run and attach) keeps the agents to those sites
(I-311). The bridge needs a running machine and never starts one (I-312).
User docs: `apps/web/src/content/docs/your-chrome.md`.

## What is in the guest

- `chromium` from nixpkgs and `playwright-driver.browsers` built with
  Chromium only, linked into the usual `~/.cache/ms-playwright` at boot,
  so a project on the base's Playwright version finds Chromium without
  `npx playwright install`; any other version, or Firefox, downloads its
  own there, and the downloaded browsers run through nix-ld (DECISIONS
  I-228). WebKit does not run (I-548).
- The display and the VNC server in one process, `repose-xvnc.service`:
  TigerVNC's Xvnc as X display `:99`, 1440x900 until a viewer connects,
  VNC on 127.0.0.1:5900 with the boot's password, `-AcceptSetDesktopSize`
  so a viewer can give the screen its own size (I-292). openbox maximises
  every window (no decorations), so the browser is the screen's size and
  follows it.
- The agents' browser, `repose-browser.service`: Chromium, headed, on
  `:99`, its profile in `~/.local/share/repose/browser` so cookies and
  logins survive restarts, DevTools on 127.0.0.1:9225 behind the
  socket-activated endpoint `http://127.0.0.1:9224`. The first connection
  to 9224 starts Xvnc, the window manager and the browser; nothing runs
  before that.
- Playwright MCP registered in Claude Code's user-scope MCP config as
  `playwright` (`--cdp-endpoint http://127.0.0.1:9224`) and
  chrome-devtools-mcp as `chrome-devtools` (`--browserUrl
  http://127.0.0.1:9224`). Both attach to the agents' browser, so they see
  the same tabs, and Playwright works in its default context: the window
  and the logins the user sees. A guest's earlier `--headless` entries are
  replaced by `repose-agent-setup` at the next agent start; an entry the
  user changed is left alone.
- The viewer, `repose-novnc.service`: websockify on 127.0.0.1:6081
  bridging the page's WebSocket to Xvnc and serving the viewer page
  repose ships (`nix/guest/base/desktop/viewer/`: `index.html`,
  `viewer.js`, `viewer.css`, `healthz`, with noVNC 1.7's `core/` and
  `vendor/` modules copied beside them, no build step), behind the socket-activated entry
  point 127.0.0.1:6080. Off until asked; it costs nothing idle.
  `repose-vncconfig.service` runs with it and carries the X selections to
  the VNC clipboard and back.
- Fonts (Noto Sans, Noto Sans Mono, Noto Serif, Liberation, colour emoji)
  with grayscale antialiasing and slight hinting, so pages render as text
  and travel to the viewer without colour fringes.
- The sandbox: Chromium runs as `dev` inside a microVM, so it keeps its own
  sandbox on; nothing needs `--no-sandbox`.

## The viewer page

- Connects at once with the password from the URL fragment (`#p=`). A
  link without one shows a password field and says where the link with
  one comes from (`repose browser`).
- The remote screen takes the size of the tab: noVNC's `resizeSession`
  sends SetDesktopSize, Xvnc resizes the X screen, openbox re-maximises
  the browser to it. A 2560x1440 tab gets a 2560x1440 desktop; a narrow
  tab gets a narrow one (Chromium's own minimum window width, about 500
  px, is the floor: below it the window is wider than the screen and the
  page scales it). Whatever fraction is left over is scaled
  (`scaleViewport`); nothing is clipped. A Retina tab is shown at 1x
  pixels: the desktop is the tab's CSS size, the page reports "at 1x" in
  the bar. Sharper than the old 1440x900 stretched to fit, and Chromium
  cannot change its scale factor while running, so 2x waits.
- Quality 9 and compression 1 by default (`?q=0` to `?q=9` and `?c=` in
  the query string change them: the screen is mostly text, and CPU is
  better spent by the machine's browser than its encoder).
- A slim bar: the project's name (from the guest's `project.json`, else
  its hostname), "the agent's browser", the connection state, the remote
  size, a full-screen button and a "copy link" button that copies the
  page's address with the fragment. Full screen hides the bar and the
  screen takes the whole display.
- Keyboard goes to the machine (Meta and Alt combinations included, as far
  as the browser lets them through); the page focuses the screen on load
  and on click.
- Clipboard both ways where the browser allows: when the tab comes to the
  front, the laptop's clipboard is sent to the machine (Chromium asks the
  user once; Firefox has no such read); text copied on the machine lands
  in the laptop's clipboard.
- Reconnects with backoff (1, 2, 4, 8, 16 s, then every 15 s) when the
  connection drops: the guest's socket activation brings the chain back
  when the viewer idled out, so a tab left open comes back by itself.
  After three failed attempts the page says the machine's desktop is off
  and to start it with `repose browser`, and keeps trying.
- A refused password (the machine rebooted since the link was made) stops
  the retries and shows the field.

Shipping: the viewer page, Xvnc and the per-boot password need the base
after 2026.09.27.2; `repose browser`, the background forward and the
fragment need the CLI after v0.1.20. An older CLI against a new base
prints the password with `repose open --desktop` and the new page shows
its password field; a new CLI against an older base opens stock noVNC's
`vnc.html`, which asks for the password the CLI did not print (run
`repose open --desktop` on that base, or wait for its update).

## Behaviour that must hold

- A fresh guest can run a Playwright script that opens a page and takes a
  screenshot with no install step. A test in the guest base does exactly
  that.
- Claude Code in a fresh guest lists `playwright` and `chrome-devtools` in
  `claude mcp list`.
- `repose browser [PROJECT] [--stop] [--no-open]`: runs
  `repose-guest-profile desktop start` over SSH (starts the viewer, and
  with it the agents' browser if it is not running; prints the password),
  then reaches the viewer through a forward of 6080 to laptop port 6080 or
  a free one with a message (I-261). The forward is an `ssh -N` child in
  its own session with `ExitOnForwardFailure`, `ServerAliveInterval 15`,
  `ServerAliveCountMax 3`, off the ControlMaster (I-149), recorded as port
  and pid in `~/.config/repose/browser-forwards/<slug>.json`; it outlives
  the CLI. A second run probes the recorded port for the viewer's
  `/healthz` (`repose desktop viewer ok`) and reuses it; a dead or hijacked
  one is replaced. The command prints one line with the URL, whose
  fragment carries the password; the password is never printed on its
  own and never logged. `--stop` runs `desktop stop` in the guest (when the
  project runs) and ends the forward (the pid is killed only while the
  port still answers as our viewer). (DECISIONS I-33, I-292.)
- The password is generated once per boot into
  `/run/repose/desktop/vnc-password` (0600 dev) the first time the display
  starts, so a viewer that idled out or was stopped reconnects with the
  link it has; `repose-guest-profile desktop start` prints that same
  password every time until the machine reboots.
- While the display is up (the agents' browser or the viewer is running),
  `DISPLAY=:99` is exported into new shells, so a headed browser an agent
  or the user starts appears on the desktop too. Playwright, Puppeteer and
  Cypress default to headless whatever `DISPLAY` says, so a project's test
  suite stays headless unless its config asks otherwise (I-246).
- `repose browser --stop` stops the viewer. The agents' browser keeps
  running for the agent; with it gone, Xvnc stops and `DISPLAY` is no
  longer exported.
- The viewer stops after 30 minutes with no client. The agents' browser
  stops after 30 minutes with no DevTools client (an MCP server keeps its
  connection for the agent's whole session) and no viewer client.
- A crashed or killed browser starts again on the next DevTools
  connection with the same profile, and both MCP servers reconnect on
  their next call.
- The desktop is never reachable except through the SSH forward. The
  viewer and Xvnc bind `127.0.0.1` in the guest; the guest has no inbound
  anyway. Xvnc's VNC port is up whenever the display is (the browser
  alone keeps it up), password-protected and on loopback, and the CLI
  never auto-forwards 5900, 6080 or 6081.
- The browser is limited to 1.5 GB on a small guest, 3 GB on large, 6 GB
  on xl (the slice `repose-browser.slice`, system level for the agents'
  browser, user level for the MCP servers and any Chromium a user starts),
  because a runaway page in a 4 GB guest takes the agent down with it and
  the user only sees an unexplained stall. A renderer killed at the limit
  is one crashed tab; the browser stays up.
- The DevTools ports 9224 and 9225 are never auto-forwarded to the laptop.
- The guest-desktop VM test proves the chain: the page and `healthz` are
  served on 6080, a real RFB client authenticates with the boot's
  password and receives the framebuffer, its SetDesktopSize changes the X
  screen and the browser's window to the requested size, the password
  survives a stop and start of the display, and the idle stops hold.

## Why Claude in Chrome cannot work here

Claude in Chrome needs a visible Chrome with the extension installed, a
subscription login, and connects through Anthropic's relay from that
browser. The guest has no such Chrome and the extension cannot be bridged
from the laptop today. Agents that need browsing use Playwright MCP or
chrome-devtools-mcp instead, which cover navigation, forms, screenshots,
console and network capture; `repose browser bridge` points those two at
the laptop's Chrome when a job needs the user's own logins.

## `repose browser bridge` (DECISIONS I-296)

The laptop's Chrome, driven by the agents on a guest, for as long as the
command runs. Three parts, none of which the agent sees:

1. **The laptop's Chrome.** Chrome 144 and later has its own switch,
   `chrome://inspect/#remote-debugging`: on, Chrome starts a DevTools
   server on a random port and writes the port and the browser's
   websocket path (an unguessable id) to `DevToolsActivePort` in the
   profile directory (`~/Library/Application Support/Google/Chrome` on
   macOS, `~/.config/google-chrome` on Linux, `%LOCALAPPDATA%\Google\Chrome\User
   Data` on Windows; `--user-data-dir` names another). That server speaks
   websocket only (every HTTP request gets 404) and Chrome asks the user
   to allow each connection. The CLI reads the file and checks the port
   answers; when the switch is off it opens the page in Chrome and polls
   for five minutes. `--cdp URL` bridges any DevTools server as is (a
   browser started with `--remote-debugging-port`), read through its
   `/json/version`.
2. **The front**, a listener on the laptop's loopback that the tunnel
   reaches. It answers `/json/version` itself with the websocket URL at
   the address the request came to (the guest's `127.0.0.1:9224`),
   because that is how Playwright MCP (`--cdp-endpoint`) and
   chrome-devtools-mcp (`--browserUrl`) discover the websocket and
   Chrome's own server does not answer it. The browser's websocket goes
   to Chrome message by message through the bridge's policy (I-311:
   refusals, hidden targets, no cookies, the navigation log; I-467: no
   credential headers, request or response bodies, WebSocket or
   server-sent event payloads, or other sites' stored data); every
   other request, the other `/json` endpoints and per-page websockets
   included, is 404. It counts the upgrades: each is an MCP server
   attaching, and the CLI says so. With `--allow`, a second CDP
   connection of the CLI's own (the warden) intercepts document requests
   in the agents' tabs and fails those off the list.
3. **The tunnel**: `ssh -o ExitOnForwardFailure=yes -R
   127.0.0.1:9226:127.0.0.1:<front> <slug>.repose repose-guest-profile
   browser bridge hold`, on its own connection (not the ControlMaster,
   whose forwards outlive the command). The hold runs
   `repose-browser-bridge on`, which stops `repose-browser.socket` and its
   proxy and starts `repose-browser-bridge.socket` on the same 9224, whose
   proxy goes to 9226; prints `on`; then waits for its stdin to close
   (the CLI's pipe, closed on Ctrl-C; or the session sshd gives up on, two
   minutes after a laptop sleeps) or for the 9226 listener to vanish,
   checked every five seconds; and switches back on exit. Stopping a proxy
   ends the connections the MCP servers hold through it, so their next
   call reconnects through 9224 to whichever browser is there now (I-246's
   reconnect). The CLI runs `bridge release` first (kills the sshd session
   holding 9226 from an earlier bridge, so a new bridge takes over from a
   sleeping laptop's) and `bridge stop` after, belt and braces, in one
   ssh bounded at 4 s that also shows the closing tmux line only if a
   client is attached (I-313).

The guest side is `repose-browser-bridge on|off|status|tunnel|release`
(browser.nix), and `repose-guest-profile browser bridge
start|stop|status|release|hold` for the CLI. The desktop idle check also
switches back a bridge whose tunnel listener is gone, so a guest is never
left with an endpoint that refuses every connection. 9226 is a platform
port, never auto-forwarded.

What it is not: the guest's browser profile is untouched, nothing from the
laptop's Chrome is stored on the guest or the api, and the tunnel is
loopback to loopback at both ends. What the user lends: while the bridge
is up, any process on the guest can drive that Chrome; Chrome's own
per-connection dialog is the check on that, `--allow` narrows it to the
sites named, and the docs say both. What never passes, and the gaps
`--allow` leaves, are in I-311, I-467 and the user page.

Behaviour that must hold (guest-desktop VM test, `TestBridgeEndToEnd`):

- `bridge start` makes both MCP servers' next call land in the browser
  at 9226 without a restart; `bridge stop` brings the next call back to
  the guest's browser, which kept its tabs.
- A running MCP server follows the switch on its next call, both ways.
- `hold` lasts exactly as long as its stdin, or until the 9226 listener
  is gone, and switches back either way.
- The idle check switches back a bridge with no listener.
- Through a real ssh with `-R` against a fake guest, a client on the
  guest's side sees `/json/version` name `ws://127.0.0.1:9224/...` and
  its upgrade reach the laptop's server; ending the command ends the hold
  and the listener. A held port is reported as another bridge, not as an
  ssh warning.
- With `--allow`, against a real Chromium (`TestBridgeAgainstChromium`,
  `TestBridgeWithPlaywright`, `TestBridgeWithChromeDevtoolsMCP`, run when
  `BRIDGE_TEST_CHROMIUM` and friends are set): no document off the list
  loads in the agents' tab, whether by navigate, script, redirect, popup,
  form or frame; the user's tab stays hidden and free; both MCP servers
  work through the front.
- Against a real Chromium (`TestBridgeCredentialsAgainstChromium`): a
  page's request carries its Authorization header to the server, and the
  tool watching the network sees neither that header, the request body,
  a rejected Set-Cookie line, WebSocket frame payloads nor server-sent
  event data; a body read is refused (I-467).

## Depends on

Workstreams 02 (packages, MCP registration, units, slice limits, the
viewer page), 07 (`repose browser`, the background forward, `browser
bridge`), 04 (start/stop units, DISPLAY export via guestd Exec).

## Deferred

A 2x desktop for Retina tabs (needs Chromium's scale factor to follow the
viewer, which means a restart). A link from the dashboard or the attached
session (both would need the forward to exist before the click; the
command is the forward). Browserbase or another hosted browser as an
option. GPU-accelerated rendering (no GPU guests). Bridging Claude in
Chrome itself (the extension speaks to Anthropic's relay, not to a port).
