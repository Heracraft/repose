# Browser for agents

Every guest has one Chromium that agents drive, headed on a virtual
display, and a desktop viewer that can be switched on when a human needs to
look at or take over that same browser (DECISIONS I-246, I-292). Claude in
Chrome is not available in a guest, and the doc says why; `repose browser
bridge` (I-296) lends the guest's browser tools the laptop's own Chrome
instead.

## What the user sees

Agents use it:

```
$ repose run -p "log into the staging site and screenshot the dashboard"
```

Watching or taking over is a project's **Browser** tab on the dashboard,
or one command on a laptop with the CLI (DECISIONS I-292, I-1216). The
tab needs no forward, no password and nothing installed: open it and the
machine's screen is there.

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
the same link; `--no-browser` prints the link without opening a browser (`--no-open` is its old name, I-619).

```
$ repose browser stop
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
- Playwright MCP registered for every agent as `playwright`
  (`--cdp-endpoint http://127.0.0.1:9224`) and chrome-devtools-mcp as
  `chrome-devtools` (`--browserUrl http://127.0.0.1:9224`), through each
  agent's own layer (DECISIONS I-553, I-554; the table is in agents.md
  "MCP"). Both attach to the agents' browser, so they see the same tabs,
  and Playwright works in its default context: the window and the logins
  the user sees. A guest's earlier `--headless` Claude Code entries are
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
  page scales it). Any fraction left over is scaled
  (`scaleViewport`); nothing is clipped. A Retina tab is shown at 1x
  pixels: the desktop is the tab's CSS size, the page reports "at 1x" in
  the bar. Sharper than the old 1440x900 stretched to fit, and Chromium
  cannot change its scale factor while running, so 2x waits.
- Quality 9 and compression 1 by default (`?q=0` to `?q=9` and `?c=` in
  the query string change them: the screen is mostly text, and CPU is
  better spent by the machine's browser than its encoder).
- A slim bar: the project's name (from the guest's `project.json`, else
  its hostname), "the agent's browser", the connection state, the remote
  size, a "paste" button, a "copy link" button that copies the page's
  address with the fragment, and a full-screen button. Full screen hides
  the bar and the screen takes the whole display.
- Keyboard goes to the machine (Meta and Alt combinations included, as far
  as the browser lets them through); the page focuses the screen on load
  and on click.
- Clipboard both ways (DECISIONS I-726). Ctrl+V on the screen (Cmd+V on
  a Mac, Ctrl+Shift+V too) pastes the laptop's clipboard on the machine:
  the page holds the key back from noVNC, takes the text from the
  browser's paste event (no permission needed, Chromium and Firefox
  alike), sends it as the VNC clipboard and then presses Ctrl+V on the
  machine over the same connection; Cmd becomes Ctrl there. A browser
  that fires no paste event within 300 ms gets its key through as before.
  The "paste" button does the same through `navigator.clipboard.readText`
  (Chromium asks once; Firefox shows its own Paste button at each click),
  and presses nothing when the laptop's clipboard holds no text.
  When the tab comes to the front, Chromium with that permission also
  sends the clipboard. Text copied on the machine lands in the laptop's
  clipboard.
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

## The dashboard's view (DECISIONS I-1216)

- The same desktop, over a different path: the dashboard page draws
  noVNC itself on the dashboard's own origin, over a WebSocket to the
  api, which pipes it to a relay on the edge (`:6081`, mTLS with the
  client name `api`), which holds the SSH connection to the guest. No
  forward, so nothing has to exist on the watcher's machine first.
- **The password never leaves the edge on this path.** The relay reads
  the guest's per-boot password over the SSH connection it already has,
  does the VncAuth handshake itself, and offers the page security type
  None. The CLI's view is the other arrangement: there the password
  rides in the link's fragment, which the browser never sends with a
  request, and the page authenticates. Both keep it out of a log and off
  the wire to repose; the dashboard's also keeps it out of the browser.
- Each connect is one mint: `POST /v1/projects/{id}/browser/view` sets a
  15-minute `HttpOnly; Secure; SameSite=Strict` cookie scoped to
  `/v1/browser/stream`, and the stream route reads the project from the
  ticket's row and not from any path. The page mints again on every
  reconnect, so an expired ticket is a mint and not an error the reader
  sees.
- An open view holds the machine out of its freeze while the screen is
  moving, and the frames are not the project's egress: they never cross
  a guest's uplink, so nothing meters them (I-1218). They are capped
  instead: 2 GiB per ticket, 120 s with no frame, 4 hours, one view per
  project.
- A close says why, and the page acts on it: `closed` and a transport
  error are reconnected to, three times at 1, 2 and 4 seconds, each with
  a fresh mint; `idle`, `cap` and `budget` stop and show **Resume view**,
  because a reconnect mints and a mint is where a frozen machine wakes.
- **The clipboard is a panel, not a bridge**, which is the one place the
  two viewers differ in how they work. Under the screen, "Paste into the
  machine" is a field and a **Send**: the text reaches the machine's
  clipboard when Send is pressed. "Copied in the machine" appears when
  the machine copies something and holds it in the page, with the site's
  own Copy beside it. Nothing moves on its own in either direction, and
  the page never reads or writes the watcher's own clipboard except
  through that Copy.

  The guest's own viewer page does bridge both ways automatically
  (`viewer.js`): it writes the machine's cut-text straight into the
  watcher's clipboard, and pushes the watcher's clipboard into the
  machine on `focus` and `visibilitychange`. That is right for a person
  at their own laptop behind their own SSH forward, and wrong here: a
  guest the tenant has root on would read whatever was on the watcher's
  clipboard the moment the tab was focused, and choose what their next
  paste anywhere else said. The panel's text is a tenant's own text in
  both directions, so neither half is ever logged
  (`docs/ops/OBSERVABILITY.md`). `apps/web/src/lib/browserView.test.ts`
  pins both rules against the source, because the panel needs frames and
  frames need a guest.
- The two viewers share no code. The guest's page uses the `core/` and
  `vendor/` modules copied out of `pkgs.novnc` at build time; the
  dashboard's uses `@novnc/novnc` pinned to the exact version nixpkgs
  gives the guest (1.7.0 on both today, an exact pin in
  `apps/web/package.json` rather than a range, so an update is an edit a
  reviewer sees).

## Behaviour that must hold

- A fresh guest can run a Playwright script that opens a page and takes a
  screenshot with no install step. A test in the guest base does that.
- Claude Code in a fresh guest lists `playwright` and `chrome-devtools` in
  `claude mcp list`.
- `repose browser [PROJECT] [--no-browser]` (`stop` as its own subcommand since I-619): runs
  `repose-guest-profile desktop start` over SSH (starts the viewer, and
  with it the agents' browser if it is not running; prints the password),
  then reaches the viewer through a forward of 6080 to laptop port 6080 or
  a free one with a message (I-261). The forward is an `ssh -N` child in
  its own session with `ExitOnForwardFailure`, `ServerAliveInterval 15`,
  `ServerAliveCountMax 3`, off the ControlMaster (I-149), recorded as port
  and pid in `~/.config/repose/browser-forwards/<slug>.json`; it outlives
  the CLI. A second run probes the recorded port for the viewer's
  `/healthz` (`repose desktop viewer ok`) and its `/project.json`, whose
  `name` the guest writes at every viewer start, and reuses it only when
  that name is this project's (I-1238): every viewer answers `/healthz`
  the same way, and a port another project's forward took meanwhile
  showed that machine's page, which refused this one's password. That
  forward is left running and this project gets a port of its own; a
  dead or hijacked one is replaced. The command prints one line with the URL, whose
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
- `repose browser stop` stops the viewer. The agents' browser keeps
  running for the agent; with it gone, Xvnc stops and `DISPLAY` is no
  longer exported.
- The viewer stops after 30 minutes with no client. The agents' browser
  stops after **10** minutes with no DevTools command and no viewer
  client once every agent is `idle` or `needs_input`, and keeps 30
  minutes while an agent works (I-1122). The freeze cannot start while
  an agent works, so a browser stopped mid-turn saves nothing and costs
  the agent its page; once every agent is idle the browser is the only
  thing between the machine and its freeze. guestd stamps
  `/run/repose/desktop/last-agent-working` on each refresh that sees an
  agent working, and the idle check reads its age to pick the threshold;
  no stamp means no agent has worked since the boot, which is the short
  threshold.
- A **command**, not traffic and not a connection. An MCP server keeps
  its CDP connection open for the agent's whole session and sends
  nothing between calls, so counting open sockets held the browser alive
  for ever and the machine could never freeze -- Chromium alone burns
  about 15 s of CPU a minute against the rest rule's 6 s (I-930). And
  `bytes_sent` on the browser's side is its replies *and* its events, so
  a page with a live-reload socket or a poll kept the browser up with no
  tool call at all. The check sums `bytes_received` on the CDP sockets
  each minute and treats a changed total as use (I-1122).
- A DevTools request that opens and closes between two checks takes its
  bytes with it, which used to stop the browser of a machine reached
  only by short requests (I-948). `repose-browser-proxy.service` stamps
  `last-cdp` when it starts and exits ten seconds after its last
  connection closes, so each such request stamps the clock itself.
  `systemd-socket-proxyd` supports `Accept=no` alone, so the stamp is
  per proxy start and not per connection: a short request that arrives
  while another connection is held open is still unseen, and that
  connection's own bytes answer for it.
- The browser comes back with the tabs it had. `repose-browser-start`
  sets the profile's `session.restore_on_startup` to 1, so the start
  after an idle stop opens the pages the agent had; each page reloads,
  so form input and in-page state are lost and the profile's logins are
  not. A profile that has never run is started on `about:blank` instead,
  since a URL on the command line would be opened beside the restored
  tabs. Chromium writes its session a couple of seconds after a
  navigation commits, so a browser stopped in the same breath as a
  navigation comes back on the page before it.
- A **stray headless browser** is stopped by guestd (I-1124, which
  amends I-200's "nothing is killed on a timer"). All five must hold,
  for ten one-minute passes in a row: the process runs as `dev` and is
  the top of a browser tree (`chrome-headless`, `chromium`, `chrome`,
  `headless_shell`, whose parent is none of those); its cgroup is a
  `.scope` under dev's `user-<uid>.slice` or `repose-herdr-server.service`,
  never a unit of its own and never a container; its parent is pid 1 or
  dev's user manager, so its launcher has exited; no TCP connection is
  established to a port it listens on; and in each of those minutes the
  machine had no SSH session, no multiplexer client and every agent
  `idle` or `needs_input`. A process listening on 9225 is never a stray
  whatever its cgroup says: that is the agents' browser, and I-1122's
  rule governs it. guestd then opens a pidfd, checks the start time
  again so a reused pid is never hit, sends SIGTERM and SIGKILL ten
  seconds later. It logs one line with the process name, the pid, how
  long the process ran and its CPU seconds, and nothing else.
- **What keeps a browser.** Its launcher still running, or something
  connected to it. A unit of its own does not, for Chromium: as soon as
  Chromium can reach dev's user bus it moves itself into
  `app-org.chromium.Chromium-<pid>.scope` under dev's `app.slice`,
  whatever unit or scope it was started in, so its cgroup says nothing
  about where the user put it. The `guest-stray-browser` VM test prints
  that cgroup (`MEASURE real chromium cgroup`); the run of 2026-11-11
  that found it had started the browser with
  `systemd-run --user --unit keepc`, which the proposal had offered as
  the way to keep one. Chromium with no user bus to reach, which is the
  agents' browser in its system unit, stays where it is put -- and
  because that is an accident of its environment, a process listening on
  9225 is excluded by name above. Playwright's `chrome-headless-shell`,
  which is the 7.4 hours this rule is for, has no such integration and
  stays in the unit it is given, which is why the `.service` condition
  is kept. The docs promise only the two conditions that hold for every
  browser.
- A crashed or killed browser starts again on the next DevTools
  connection with the same profile, and both MCP servers reconnect on
  their next call.
- The desktop is never reachable except through the SSH forward. The
  viewer and Xvnc bind `127.0.0.1` in the guest; the guest has no inbound
  anyway. Xvnc's VNC port is up whenever the display is (the browser
  alone keeps it up), password-protected and on loopback, and the CLI
  never auto-forwards 5900, 6080 or 6081.
- The browser is limited to 739 MB on a tiny guest, 1.5 GB on small, 3 GB on large, 6 GB
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
  survives a stop and start of the display, and the idle stops hold --
  both thresholds, the short request that stamps the clock, the tabs
  that come back with no warming request, and the viewer that keeps the
  browser up while nothing drives it. The guest-stray-browser VM test
  runs the real guestd against four browsers in one twelve-minute quiet
  window and stops the one orphan.
- `golang.org/x/sys/unix` pidfds make the stray's signal safe against a
  reused pid; `internal/guestd/sample/stray_test.go` drives each of the
  five conditions on a fixture `/proc`.
- A dashboard ticket reaches only its own project's machine, whatever
  else is asked for: `TestStreamReachesOnlyTheTicketsProject` registers
  no route with a project id and proves A's ticket reaches A's login and
  address. A guest answering the relay's dial with a host certificate
  that names only the address is refused:
  `internal/gateway/view_crosstenant_test.go`.
- No byte of a dashboard view's stream, and no ticket, reaches a log:
  `TestBrowserViewLogs` plants markers in a framebuffer rectangle, in a
  cut-text message and in the ticket, and finds none of them in the
  captured logs, while the same logs carry `browser_view_close`, its
  outcome, `bytes_out` and an unredacted `ticket_id`.
- The panel's look is reviewed at real size: the fake api's static
  desktop (`internal/fakes/api/desktop.go`, the server half of RFB 3.8 as
  far as ServerInit, off unless a test turns it on) puts the real noVNC
  in its watching state against a real server, so `browser-view.spec.ts`
  captures the panel at 1x at 1440 and 390 in both schemes. Frames from a
  real guest are still the release's live check; the panel's layout is
  not.
- The dashboard's view moves the clipboard only on a click:
  `BrowserView.test.ts` drives the panel against a stub RFB -- typing in
  the field hands the machine nothing, **Send** hands it the text once,
  the machine's own cut-text lands in the page with
  `navigator.clipboard.writeText` never called -- and pins against the
  source that the component reaches `navigator.clipboard` nowhere itself
  and ports neither the `focus` nor the `visibilitychange` listener the
  guest's page has. The view listens for `connect`, `clipboard` and
  `disconnect`, and nothing else.

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
sites named, and the docs say both. I-311, I-467 and the user page list
what never passes and the gaps `--allow` leaves.

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
bridge`), 04 (start/stop units, DISPLAY export via guestd Exec), 06 (the
edge's view relay on `:6081`) and 08 (the dashboard's Browser tab).

## Deferred

A 2x desktop for Retina tabs (needs Chromium's scale factor to follow the
viewer, which means a restart). A link from the attached session to a
view (it would need the forward to exist before the click; the command
is the forward; the dashboard's own view, I-1216, needs no forward and
shipped instead). Cut text between the dashboard's view and the
watcher's own machine (I-1216 says why it is not a feature to add
later). Browserbase or another hosted browser as an
option. GPU-accelerated rendering (no GPU guests). Bridging Claude in
Chrome itself (the extension speaks to Anthropic's relay, not to a port).
