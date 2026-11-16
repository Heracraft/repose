# Stopping browsers nobody uses (proposal, 2026-11-10)

**Status: proposal; nothing here is decided.** The product value council
(`reports/2026-11-04-product-value-council.md`, Fix list, score 6.0) named
the shared browser and stray headless browsers as the second-largest idle
CPU sink. This proposal changes two things. The agents' shared Chromium
stops 10 minutes after its last DevTools command when no agent is working
and nobody watches, and comes back with its tabs on the next call. A
headless browser whose launching process has exited is stopped once the
machine is otherwise quiet. The second part amends I-200, which rejected
idle reaping, so it needs the owner's ruling before anyone builds it.

Prod could not be reached on 2026-11-10 (the edge timed out on SSH), so
every prod figure below comes from the 2026-10-11 sample in
`2026-10-11-pause-idle-machines.md` §5 and from I-930's measurement.
Figures marked *estimate* are arithmetic on those numbers. Figures marked
*unverified* need step 1.

## Summary

| Question | Answer |
|---|---|
| Today's stop rule | The shared browser stops after 30 minutes with no DevTools traffic and no viewer client (`nix/guest/base/desktop.nix:56`, `:168-171`). The council's "30 minutes" is right |
| Today's headless rule | None. A `chrome-headless` or `chromium` that an agent's script started runs until the machine stops |
| How the freeze sees them | Only as CPU. A running Chromium burns about 15 s a minute (I-930) against the rest rule's 6 s (`internal/api/resting/resting.go:62`), so no machine freezes while one runs |
| The 13.6 idle CPU hours | Measured 2026-09-27 to 2026-10-11, before I-930 (2026-11-01). Most of the 6.2 `chromium` hours came from the bug I-930 fixed. The 7.4 `chrome-headless` hours are untouched by any fix since |
| Proposed browser rule | Stop after 10 minutes with no DevTools command and no viewer client while every agent is `idle` or `needs_input`; keep 30 minutes while an agent works. No config key |
| Proposed headless rule | guestd stops a browser whose launcher has exited, that nobody is connected to, after 10 quiet minutes on a machine with no SSH session, no multiplexer client and no working agent |
| Restart | Socket activation already restarts the browser, and both MCP servers already reconnect (VM test, `nix/guest/tests/default.nix:2462-2475`). New: the tabs come back |
| Saving per idle stretch | The earliest freeze moves from about 46 to about 26 minutes after the last browser call (*estimate*). A stray headless browser stops blocking the freeze at all |
| Biggest risk | Stopping a headless browser that a person wanted. The rule only touches orphans with no client, on a quiet machine, outside a unit of their own |
| Decide first | Does the orphan rule amend I-200? Two tiers or a flat 10 minutes? Tabs restored on restart? |

## 1. The repo today

**The shared browser.** `nix/guest/base/browser.nix` defines it.
`repose-browser.socket` listens on 127.0.0.1:9224 (`:327-335`) and starts
`repose-browser-proxy.service`, a `systemd-socket-proxyd` to Chromium's
own DevTools port 9225 (`:362-373`). The proxy `Requires` and `BindsTo`
`repose-browser.service` (`:365-367`), which runs a headed Chromium on
`:99` as `dev` with its profile in `~/.local/share/repose/browser`
(`:158-174`, `:295-325`). The browser `BindsTo` Xvnc (`:299`). Both MCP
servers are registered against 9224: `playwright-mcp --cdp-endpoint` and
`chrome-devtools-mcp --browserUrl` (`:229-232`). The start script resets
the profile's crashed flag so Chromium shows no restore bubble
(`:160-164`). It does not ask Chromium to restore tabs. The system slice
caps the browser at 37.5 percent of guest memory: 1.5 GB small, 3 GB
large, 6 GB xl (`:23-27`, `:283-286`).

**The idle stop.** `repose-desktop-idle-check.timer` runs each minute
(`desktop.nix:312-318`). The check (`:115-172`) sums `bytes_sent` on
established sockets whose source port is 9225 and stamps
`/run/repose/desktop/last-cdp` when the sum changes (`:141-162`, I-930).
It stamps `last-client` while a viewer is connected on 6081 or 5900
(`:139-140`). It stops the proxy and the browser when both stamps are
older than `idleSeconds`, 1800 (`:56`, `:168-171`). The same constant
sets the viewer's own stop (`:165-167`) and the `idle_minutes` the viewer
page shows (`:110-111`). The bridge guard at `:119-126` is not part of
this proposal.

**Two gaps in that check.**

- `bytes_sent` on the 9225 side is the browser talking: command replies
  and events. A tool that enabled the Network or Runtime domain gets an
  event for each request a polling page makes, so a page with a live
  reload socket or a poll can keep the stamp fresh with no tool call at
  all (*unverified* how often this happens; I-930's tenant had 0 bytes in
  15 s, with a page that burned 13.4 s a minute).
- A connection that opens and closes between two checks takes its bytes
  with it. I-948 recorded this and left it open: "The fix that fits is
  for the 9224 proxy to stamp `last-cdp` itself, since it already sees
  every request. That changes a base that is published, so it is the
  owner's call". At 30 minutes the gap is rare. At 10 minutes a script
  that connects for a short job every quarter hour finds a cold browser
  each time.

**Restart and reconnect.** The VM test
`nix/guest/tests/default.nix:2462-2475` kills the browser, lets the
socket start it again, and shows `playwright-mcp` navigating and
`chrome-devtools-mcp` listing pages in the new browser with no restart of
either server. I-246 records why: "both servers reconnect when
`browser.connected` is false". An idle stop closes the same proxy
connection a kill closes, so the same path applies. Two limits:

- The test warms the browser with `curl -m 300` before the MCP call
  (`:2468`), because "the MCP server's own connect timeout is 30 s, and a
  cold Chromium on a loaded 2-vCPU test VM has taken over a minute"
  (`:2378-2382`). Cold start on a real guest is unmeasured.
- After a restart the browser has one `about:blank` tab. The agent's page
  is gone, and the agent must navigate again.

**Browsers outside the shared one.** `PUPPETEER_EXECUTABLE_PATH` and
`CHROME_BIN` point at the wrapped `chromium` (`browser.nix:205-208`),
which runs in a transient scope of the user slice `repose-browser.slice`
(`:94-114`). Playwright from code launches its own
`chrome-headless-shell` from `~/.cache/ms-playwright`; its process name is
`chrome-headless` after the kernel's 15-character cut. Nothing stops
either. `keystroke-path.nix:91-104` records a 1.4 GB headless Chromium a
Playwright test left in a pane on kanali, 2026-10-13.

**guestd never kills.** `internal/guestd/sample/oom.go:22-24`: "Nothing
is ever killed or stopped by guestd; the kernel's OOM killer decides".
I-200: "nothing is killed on a timer ... *Rejected:* idle reaping (it
eventually kills the one server someone needed)". guestd already reads
each process's name, parent and uid (`oom.go:113`, `proc.go:206-239`) and
finds dev's user manager as the `systemd` whose parent is 1
(`oom.go:129`). R5-3 limits what it reads to names, stat fields, the uid
and the exe link's basename, never a command line.

**The freeze.** A machine rests when 15 one-minute samples in a row show
no SSH session, no tmux client, guestd answering, each agent `idle` or
`needs_input` (`internal/api/idle/idle.go:80-82`), guest CPU under 6 s and
network under 64 KiB (`resting.go:57-66`, `:223`; I-818, I-886). The
browser counts only through CPU. I-930 left one question open: "Whether
an agent's browser should count toward the rest rule's CPU at all is a
separate question". This proposal answers it by stopping the browser,
which keeps the rule as it is.

## 2. What the idle CPU was

From the 2026-10-11 sample (3 accounts, 79 projects, 14 days to
2026-10-11), CPU in minutes that `usedSQL` calls idle:

| Process | CPU hours | Status since |
|---|---|---|
| `chrome-headless` | 7.4 | No change. Playwright and other scripts' own browsers |
| `chromium` | 6.2 | I-930 (2026-11-01) fixed the stop that never fired. What is left is up to 30 minutes per idle stretch, plus any `chromium` a script started |

Before I-930 the shared browser ran until the machine stopped, so most of
the 6.2 hours is gone already (*estimate*; the split between the shared
browser and other `chromium` processes is not in `proc_samples`). The
`chrome-headless` hours are now the larger target. How many of them were
orphans and how many had a live parent is *unverified*; step 1 measures
it.

## 3. The shared browser: proposed behaviour

**The rule.** The idle check stops the proxy and the browser when all of
these hold:

1. No DevTools command for the threshold: no growth in `bytes_received`
   on the 9225 side of established sockets, and no proxy connection
   opened or closed (see "Implementation").
2. No viewer client for the threshold (`last-client`, as today).
3. The threshold is 10 minutes when no agent was working in that time,
   and 30 minutes otherwise. An agent is working when its state is
   anything but `idle` or `needs_input`, which is the freeze's own test
   (`idle.go:80-82`).

The viewer's own stop stays at 30 minutes, so `idle_minutes` on the
viewer page, `cli.md:151` and `tutorial-watch-browser.md:29` do not
change.

**Why two tiers.** The freeze cannot start while an agent works, so a
browser stopped during a turn saves CPU and costs the agent its page. An
agent that edits code for 15 minutes between two looks at its page is
common. When every agent is idle or waiting on you, the browser is the
only thing between the machine and its freeze, and 10 minutes is where
the freeze's gain lies. A flat 10 minutes is decision 2.

**Why no config key.** With tabs restored (below) a restart costs a cold
start and the state of the page, and an agent sees the same URL. A key
would carry a docs entry, a `repose config` row and a test for a value
almost nobody changes. A user who wants a browser that never stops can
start their own (`chromium --remote-debugging-port=9222`, which 9222 is
kept free for, `browser.nix:30-32`). `keep_awake` (I-820) does not change
the browser rule: it keeps a machine from freezing, and a browser that
nobody uses still burns that machine's cores. Decision 3.

**Tabs come back.** `repose-browser-start` sets the profile's
`session.restore_on_startup` to 1 before it starts Chromium, next to the
crashed-flag reset at `browser.nix:162-164`. An idle stop sends SIGTERM,
Chromium writes its session, and the next start opens the same URLs. A
page reloads, so form input and in-page state are lost; logins stay in
the profile as today. Whether `playwright-mcp` in CDP mode picks up a
restored tab as its current page is *unverified* (step 2 tests it).
Decision 4.

**The cold start.** The first MCP call after a stop waits for Xvnc,
openbox and Chromium. The MCP servers give up after 30 s. Step 1 times
it on a `large` guest on host-01. If the median is over 10 s, the idle
stop also stops Xvnc and openbox only after the viewer's 30 minutes, so
the restart is Chromium alone.

**What a user sees.** Nothing in the CLI. `machine.md` changes (section
6). An agent sees its next browser call take a few seconds and land on
the page it left. The guest agent guide gets one line (section 6).

## 4. Stray headless browsers: proposed behaviour

guestd checks each minute, on its existing `/proc` scan. A process P is
a **stray browser** when all of these hold:

1. P runs as `dev`, and its name is `chrome-headless`, `chromium`,
   `chrome` or `headless_shell`. Its parent's name is none of those, so P
   is the top of a browser tree and its renderers go with it.
2. P's cgroup is a `.scope` under `dev`'s `user-<uid>.slice`: a tmux pane
   (`tmux-spawn-*.scope`), an SSH session (`session-*.scope`), or the
   wrapper's `run-*.scope` in `repose-browser.slice`. P in herdr's
   `repose-herdr-server.service` counts too, since herdr's panes share
   that unit. A browser in any other `.service` cgroup is never a stray:
   that is how a person runs a browser on purpose (`systemd-run --user`,
   a unit file). The agents' browser lives in the system unit
   `repose-browser.service` and never matches. Docker containers run
   outside `dev`'s slice and never match.
3. P's launcher has exited: its parent is PID 1 or `dev`'s user manager
   (the `systemd` whose parent is 1, `oom.go:129`), which is where the
   kernel moves a child whose parent died.
4. Nobody is connected to P: no established TCP connection to a port P
   listens on. A browser launched with a pipe has no port and passes.
5. Points 1 to 4 have held for 10 checks in a row, and in each of those
   minutes the machine had no SSH session, no tmux or herdr client and
   every agent `idle` or `needs_input`. These are the guest-side terms of
   the freeze rule apart from CPU, which the stray is the cause of.

guestd then opens a pidfd for P, checks that its start time still matches
the one it first saw (so a reused PID is never hit), sends SIGTERM, and
SIGKILL 10 s later if P is still there. It logs one line to its journal
with the process name, the PID, how long the process ran and its CPU
seconds, nothing else (R5-3, `docs/ops/OBSERVABILITY.md`):

```
stopped a stray browser: chrome-headless pid 4182, its launcher exited, running 3h12m, 2214 s of CPU
```

**What it never touches.** A browser whose launcher still runs, such as a
`playwright test` run, a Puppeteer script waiting on a slow page, or a
`node` dev server that holds a browser. A browser started as its own
unit. Any browser while someone is attached or an agent works. A browser
with a client connected.

**Why not a CPU test.** The brief asked about "no CPU-meaningful
activity". CPU does not tell use from waste: I-930's idle page took 13.4 s
a minute, and a test waiting on a server takes almost none. The launcher
being gone is a fact about the process. The quiet machine is the moment
the stray starts to cost the user a freeze. I-930 calls the same mistake
"Presence is not activity".

**Why guestd.** It already scans `/proc` within R5-3, knows agent states,
SSH sessions and multiplexer clients, and runs as root. A guest-side rule
also works on a host a user brings under I-647, which gives the api no
samples to decide on. The api could decide instead and send an `Exec`
through hostd (I-818's split), at the cost of a new `Exec` use in
`vsock-guestd.md:27` and a minute of lag. Decision 6.

**Where a user sees it.** The guestd journal line only. No `Warning` kind
(`vsock-guestd.md:39` and I-11 stay as they are), no notification: a
stray is by definition a process nobody is using. Decision 7.

## 5. Savings

**Per idle stretch with the shared browser up** (*estimate*, at I-930's
15 s a minute):

| | Today | Proposed |
|---|---|---|
| Browser stops after its last call, agents idle | 30 to 31 min | 10 to 11 min |
| Earliest freeze after that call (stop, then 15 quiet samples) | about 46 min | about 26 min |
| Browser CPU in the stretch | 7.5 CPU-min | 2.5 CPU-min |
| Guest memory the browser can hold meanwhile | up to 1.5, 3 or 6 GB by class | the same, for a third of the time |

So each stretch gives about 20 more frozen minutes and 5 CPU-minutes of
browser time. At three such stretches a working day on an active
machine, that is about an hour more frozen and 15 CPU-minutes less a day
(*estimate*; the count of stretches is *unverified*). Freed guest memory
reaches the host only where the balloon's free page reporting is on
(`internal/hostd/ch/ch.go:50`, `:151`).

**Strays.** A stray that burns more than about 4 s a minute blocks the
freeze until the machine stops (*estimate*: the 6 s line less about 2 s
for an agent at its prompt, `resting.go:58-61`). The proposal stops it 10 minutes into the
quiet, and the freeze follows 15 minutes later. The upper bound is the
7.4 `chrome-headless` hours in 14 days on 3 accounts. The part that is
orphans comes from step 1.

**What the saving buys.** On Azure, CPU time is credit and not cash
(`2026-10-11-pause-idle-machines.md` §9). The value is cores: a host
types smoothly while busy guest vCPUs stay at or below its physical cores
(`2026-10-14-host-cpu-capacity.md`), and the owner's 2026-11-04 decision
makes idle cores the stock that automatic burst sells (council report,
"Owner decision: automatic burst by class").

## 6. Docs to change, in the same commit as the code

- `apps/web/src/content/docs/machine.md:196`. Replace the sentence about
  the browser's stop with:

  > The agent's browser stops after 10 minutes with no browser call and
  > nobody watching, once every agent is finished or waiting on you, or
  > after 30 minutes while an agent is working. The next call starts it
  > again with the same tabs and logins; each page reloads.

  Add to the same section:

  > A headless browser that a script or test started is stopped when the
  > program that started it has exited, nothing is connected to it, and
  > the machine has been quiet for 10 minutes: no SSH session, no tmux or
  > herdr client, every agent finished or waiting on you. A browser whose
  > program still runs is never stopped, and neither is one you start as
  > its own unit (`systemd-run --user --unit NAME chromium ...`).
  > `journalctl -u guestd --grep 'stray browser'` lists each one.

- `nix/guest/base/agent-guide.md`, Browser section (`:53-60`), one line:

  > - The machine's browser stops after 10 minutes unused while no agent works; your next browser call starts it again on the same pages. A headless browser whose launching process has exited is stopped once the machine is quiet; start one with `systemd-run --user --unit NAME` to keep it. <!-- /docs/machine#browser -->

- `docs/features/browser.md:193-203`: the two-tier rule, commands in
  place of traffic, tab restore, and the stray rule; the test list at
  `:215-220`.
- `docs/interfaces/guest-conventions.md:103` (`last-cdp` is written on
  each proxy connection's start and end and on command bytes), a new row
  for `/run/repose/desktop/last-agent-working` (mtime of the last guestd
  refresh with an agent working; owned by `dev`, written through
  `repose-desktop-stamp` as I-727 requires), `:1218-1222` (the
  per-connection proxy), `:1234-1237` (the two thresholds; it still says
  "no client on 9225", which I-930 already made wrong), and the process
  section near `:1058` for the stray rule.
- `docs/DECISIONS.md`: one entry that amends I-200 (if decision 1 is
  yes), closes I-948's gap, and answers I-930's open question. Its id is
  reserved when the work starts.
- No change to `docs/interfaces/vsock-guestd.md`, `grpc-hostd.md` or
  `api.md` under the recommended choices. Decision 6's api option would
  change `vsock-guestd.md:27`; decision 7's `Warning` option would change
  `:39`.

## 7. Implementation

- `nix/guest/base/desktop.nix`: split `idleSeconds` into the viewer's
  1800 and the browser's 600 or 1800, chosen by the age of
  `last-agent-working`. Read `bytes_received` in place of `bytes_sent`.
- `nix/guest/base/browser.nix`: `repose-browser.socket` takes
  `Accept=yes`, and `repose-browser-proxy@.service` runs one
  `systemd-socket-proxyd` per connection, with
  `ExecStartPre=+repose-desktop-stamp last-cdp` and the same in
  `ExecStopPost`. Each instance `Requires` and `BindsTo`
  `repose-browser.service`, so the idle stop still ends every
  connection. The bridge socket stays as it is. `repose-browser-start`
  writes `session.restore_on_startup` with `jq` beside the `sed` at
  `:162-164`.
- guestd: stamp `last-agent-working` on each refresh with a working
  agent; a new `internal/guestd/sample/stray.go` for section 4, using the
  `procReader` and `sysdep.Paths` it already has. The comment at
  `oom.go:22-24` changes with it.
- A base switch keeps the running browser (I-536); the new rule takes
  effect at the browser's next start, and the guestd rule with the new
  guestd.

## 8. Tests and closing evidence

**VM test `guest-desktop`** (`nix/guest/tests/default.nix`, after
`:2618`):

- With `last-agent-working` 11 minutes old and `last-cdp`, `last-client`
  11 minutes old, one check stops the browser. With `last-agent-working`
  fresh, it does not; at 31 minutes it does.
- A held connection that receives events but sends no command does not
  stamp `last-cdp` (a page that polls, opened through the MCP server).
- One short `curl http://127.0.0.1:9224/json/version` against a running
  browser stamps `last-cdp` (I-948's gap, closed).
- After an idle stop, a `playwright-mcp` call with **no** warming `curl`
  lands, and `chrome-devtools-mcp` `list_pages` shows the URL the agent
  had open before the stop. Print `MEASURE cold start` with the time.

**VM test, new subtest `stray browsers`:**

- `bash -c 'chromium --headless=new --remote-debugging-port=9333 about:blank &'`
  in a tmux pane, then 10 checks with no client and agents idle: the
  process is gone and the journal has the line.
- The same with a `python3` holding a connection to 9333: still running.
- `systemd-run --user --unit keep chromium --headless=new ...`: still
  running after 10 checks.
- A `node` script that launches Playwright's `chrome-headless-shell` and
  sleeps: still running after 10 checks.
- A `tmux attach` client during the 10 checks resets the count.

**Unit tests** in `internal/guestd/sample` on a fake `/proc`: each of the
five conditions failing on its own keeps P; a PID reused between the
first sighting and the signal is not signalled.

**Closing evidence**, pasted into the commit message:

- The two VM test logs, with the `MEASURE cold start` line.
- On a `large` guest on host-01: `systemctl show -p ActiveExitTimestamp
  repose-browser.service` 10 to 11 minutes after the last MCP call with
  the agent idle; `time curl -s -o /dev/null
  http://127.0.0.1:9224/json/version` from cold, five runs; `repose
  status` showing `frozen` about 26 minutes after the last call.
- The journal line from a real stray on the same guest.
- The prod query below, for 14 days before and 14 days after the deploy.

```sql
-- read-only; run inside a transaction that is rolled back
select p.comm, round(sum(p.cpu_ns) / 3.6e12, 2) as cpu_hours
from proc_samples p
join meter_samples m using (project_id, ts)
where m.ts >= now() - interval '14 days'
  and m.state = 'running'
  and not (m.ssh_sessions > 0 or m.tmux_clients > 0 or not m.guestd_ok
       or exists (select 1 from jsonb_array_elements(coalesce(m.agents, '[]'::jsonb)) a
                  where coalesce(a->>'state', '') not in ('idle', 'needs_input')))
  and p.comm in ('chromium', 'chrome-headless', 'chrome', 'headless_shell')
group by p.comm;
```

## 9. Suggested order

1. **Measure, with no change.** Run the query above for the 14 days since
   I-930. On host-01, through an audited operator `Exec` on guests that
   are idle, list browser processes with `ps -eo ppid,comm,etimes,time`
   (names and stat fields only, R5-3) to count strays against browsers
   with a live parent. Time the cold start on a `large` and an `xl`
   guest.
2. **The shared browser:** commands in place of bytes, the per-connection
   stamp, the two tiers, tab restore. No decision of I-200's kind is
   needed for this half, since the browser is repose's own unit.
3. **Strays,** after decision 1, and only if step 1 shows them to be a
   real share of the 7.4 hours.

## 10. Risks

- A stray rule that hits a browser a person wanted: one started with `&`
  and left, that they reach with short connections less often than every
  10 minutes, on a quiet machine. The docs give the `systemd-run` way to
  keep one. I-200's warning stands for anything wider than section 4.
- Cold start over the MCP servers' 30 s timeout on a busy guest. The
  first call fails and the agent retries; more stops make this more
  frequent. Step 1 measures it before step 2 ships.
- Restored tabs reload. A half-filled form, a payment page or an SPA's
  in-memory state is lost. Chromium does not resend a POST on restore.
- A restored page that burns CPU starts burning again at the next start.
  It then stops again after 10 idle minutes.
- `needs_input` counts as not working, as it does for the freeze. An
  agent that asked you to log in through `repose browser` and waits an
  hour finds its browser stopped; with tab restore you open the same
  login page.
- A browser tool that keeps the browser busy with commands between agent
  turns would hold it up. Neither MCP server does this today (I-930: 0
  bytes in 15 s).
- Every prod share comes from 3 accounts and predates I-930.

## Decisions for the owner

1. **I-200.** May guestd stop a stray browser under section 4's five
   conditions? It amends "nothing is killed on a timer" for browsers whose
   launcher has exited, and the comment at `oom.go:22-24`.
2. **Two tiers or flat.** 10 minutes with every agent idle or waiting and
   30 while one works (recommended), or 10 minutes always.
3. **A config key.** None (recommended), or a per-project key for the
   threshold.
4. **Tab restore.** Restore the browser's tabs on the start after an idle
   stop (recommended), or start on `about:blank` as today.
5. **I-948's gap.** Close it with the per-connection proxy and count
   commands (`bytes_received`) in place of all traffic (recommended; I-948
   left this to the owner because it changes a published base).
6. **Who decides on a stray.** guestd from its own samples (recommended),
   or the api through an `Exec` on hostd.
7. **What a user sees for a stray.** The guestd journal line only
   (recommended), or also a `Warning` kind for the operator.
8. **Order.** The shared browser first, strays after step 1's
   measurement (recommended), or both together.

## Sources

- Council: `reports/2026-11-04-product-value-council.md`, Fix list and
  the daily driver's card (`shared-browser-mcp`).
- Repo: `nix/guest/base/browser.nix`, `nix/guest/base/desktop.nix`,
  `nix/guest/base/keystroke-path.nix:91-104`, `nix/guest/tests/default.nix`,
  `internal/guestd/sample/oom.go`, `internal/guestd/sample/proc.go`,
  `internal/api/resting/resting.go`, `internal/api/idle/idle.go`,
  `docs/interfaces/guest-conventions.md`, `docs/interfaces/vsock-guestd.md`,
  `docs/features/browser.md`, `apps/web/src/content/docs/machine.md`.
- Proposals: `2026-10-11-pause-idle-machines.md` §5,
  `2026-10-14-host-cpu-capacity.md`.
- DECISIONS: R5-3, I-11, I-200, I-246, I-296, I-536, I-647, I-727, I-818,
  I-820, I-886, I-930, I-948.
