# No-Nix guests, council first pass: the dashboard and public docs

Reviewer: `council nonix-dash`. Tree: `main` at 43906faa, worktree
`council/nonix-dash`. Persona: a customer with one Ubuntu (erofs) machine,
`ubu`, size small, on an exempt account (fake api with billing off).
Surface: every dashboard page and action at 1440 and 390 px wide, light and
dark, and the public docs and landing page as rendered by the built site.
Partner: `council nonix-cli`, who owns the confer file
`docs/reports/2026-11-22-nonix-council.md` on branch `council/nonix-cli`.

## What would make a user scream

No P0. Nothing I reached loses a tenant's files or charges wrongly. The
worst case found: a second browser tab silently stops a service that the
first tab just installed. The service's data stays (config.md line 39).

| # | Rank | Finding |
|---|------|---------|
| D1 | P1 | The dashboard cannot add an apt package. The CLI can. |
| D2 | P1 | A failed install happens out of sight, and the item you ticked unticks itself. |
| D3 | P1 | If you reload, or go back and forward, during an install, the progress is gone and Apply is enabled again. |
| D4 | P1 | A stale second tab removes the services and packages the first tab added, and says nothing. |
| D5 | P1 | The dashboard cannot change a machine's size (tiny, small, large or xl). |
| L1 | P1 | The landing page says every machine has Nix and that `repose config add` adds "any Nix package to every rebuild". |
| D7 | P2, P1 if main ships before PR 6 | The x86 line in "Add a host" points at a URL that answers 404, and it does not say that it wipes a disk. |
| D6 | P2 | The Revisions list offers Re-apply on a failed revision, and clicking it shows a raw API refusal. |
| D8 | P2 | The Config page marks a failed revision "failed" and does not say why. |
| D9 | P2 | Only the Config tab shows that a base update is waiting. |
| D10 | P2 | Catalog copy on the Config page disagrees with the docs: the PostgreSQL data path, and Vercel "at the first boot". |
| D11 | P2 | Refusals show as a toast with the API's own sentence. On the real API, the busy refusal says "a build is in progress" on a machine that builds nothing. |
| D12 | P2 | One Escape key throws away the join step in "Add a host", which is "not shown again". |
| D13 | P2 | own-hardware.md mixes the Mac's paragraphs into the x86 section. It has no x86 reboot or removal steps. The landing page's x86 requirements are less than the docs' requirements. |
| D14 | P2 | At 390 px the project tabs hide Snapshots and Config off the right edge, even when Config is the open page. |
| D15 | P2 | An Ubuntu-only account can still open and save `/settings/machine-nix`. |
| D16 | P2 | machine.md says older machines keep working "until they are migrated", and gives no way to migrate. |
| D17 | P2 | The Hosts table says `waiting`. The create refusal says `has not registered yet`. These are two names for one state. |
| F1-F5 | process | The fake cannot produce the states that this program needs (see "The fake"). |

## Findings

Unless a finding says otherwise, the instrument is a Playwright 1.58 script
that drives nix-store Chromium 157 against the built dashboard (`node build`),
`cmd/fakeapi -config-delay 3s` and `cmd/fake-logto`. Times come from
`Date.now()` around the action. They are local and against a fake, so they
measure the page and not the product's back end.

### D1 (P1, parity): you cannot add an apt package from the dashboard

Reproduction: open Config > Packages on `ubu`, and type `ffmpeg` in "Search
packages and services…".
Expected: a way to add `ffmpeg`. config.md line 3 says software is added
"from the CLI or the dashboard". cli.md line 518 says `config add` takes
"menu entries or apt packages".
Happened: `No package matches "ffmpeg".` The same happens for any name that
is not a menu entry. On the same fake, `repose config add ffmpeg --project ubu`
printed `✓ ffmpeg installed` and `Applied revision 00000010 in 3.0s`
(real 3.03 s). The dashboard then lists it under EXTRA PACKAGES, with Remove
only. You can remove an apt package in the dashboard but you cannot add one.
Falsifier: a control anywhere on Config that adds a free-form package name.
I searched the whole page and found none.
Captures: `06-search-ffmpeg-*`, `07-config-with-apt-pkg-*`.

### D2 (P1): a failed install happens out of sight

Reproduction: on Config, tick Redis and click Apply. To make the install fail
I used Playwright route interception: it adds `{"package":"repose-fake-missing"}`
to the PUT body. This is the fake's failing name, and the dashboard cannot
send it because of D1. The other way in is a CLI-added failing package
followed by Re-apply, which D6 refuses.
Expected: the place where you clicked shows that the install failed.
Happened: the sticky footer went from "2 selected · Installing…" back to
"1 selected". The `menu_failed: repose-fake-missing failed: E: Unable to
locate package repose-fake-missing` line has `role=alert`. Its top edge was
746 px below the bottom of the viewport at 1440x900 and 1332 px below it at
390x844 (`boundingBox` after 5.5 s, measured once at each width). Redis
unticked itself. The log above the error says `redis ready`, so the page
shows as not installed a service that the log says is ready. config.md
line 37 says "The items before it stay installed", and that agrees with the
log, not with the checkbox. A successful install has the same problem:
"Applied." is just as far out of view, and the footer goes back to "N
selected". Failure took 5.9 s end to end, with the fake's 3 s delay. The
message is the raw API string, with its `menu_failed:` code prefix.
Falsifier: an error or success state in the footer or in view after Apply.
I took the reading once at each width, with screenshots.
Captures: `15-failing-install-running-*`, `16-failing-install-done-viewport-*`,
`18-failure-at-click-1440.png`, `18-failure-at-click-390.png`. On the CLI
side, the partner's C4 shows that the CLI says "Added" and "ready" for
these items.

### D3 (P1): reload, or back and forward, during an install drops the progress

Reproduction: tick Memcached and click Apply. After 1.0 s, reload the page.
For the second case, tick Deno and click Apply. After 0.8 s, go back, then
forward.
Expected: the page shows that an install is running, with its log, as it
does before the reload.
Happened: 0.4 s and 3.4 s after the reload, the page shows "2 selected" and
an enabled Apply, with no log and no "Installing…", while the op is still
running (the fake holds it for 3 s). Back then forward gives the same result:
"4 selected / Apply", and nothing changes after 4 s more. A customer who
reloads to check on the install sees an idle page and may click Apply again.
On the real API that second Apply is refused with a 409 (see D11).
Falsifier: "Installing…" or the log after the reload. I read the page twice,
at 0.4 s and 3.4 s.
Capture: `19-reload-midflow-*`.

### D4 (P1): a stale second tab removes what the first tab installed

Reproduction, in sequence and not concurrent: open Config for `ubu` in tab A
and tab B. In tab A, tick RabbitMQ, click Apply, and wait for "Applied.".
Wait 10 s. Tab B still shows RabbitMQ unticked (`isChecked` false). In tab B,
tick Ruby and click Apply.
Expected: tab B picks up the new menu, or the save is refused because the
menu changed under it.
Happened: tab B's log says `removing rabbitmq` / `rabbitmq removed`, then
installs the rest and says "Applied.". The API menu changed from
`[bun, deno, memcached, rabbitmq, ffmpeg]` to `[bun, deno, ruby, memcached,
ffmpeg]`. Tab A still shows RabbitMQ ticked. The service's data stays
(config.md line 39), so nothing is lost for good, but the service stops and
neither tab says so.
Concurrent variant: tab B applies while tab A's install is still running. On
the fake this also removed NATS, and tab A ended with "Applied." and NATS
still ticked. The real API refuses a concurrent PUT with 409 `a build is in
progress` (I located this at `internal/api/http/config.go:157` and did not run
it). So the concurrent variant is a fake divergence (F5). The sequential
variant above does not depend on concurrency. I found no revision
precondition in the PUT handler, so I expect it to reproduce on the real API,
but I did not run it there.
Falsifier: tab B refreshes the menu within 10 s, or the API refuses the stale
menu. Neither happened.
Capture: `20-two-tabs-tab2-*` (concurrent variant).

### D5 (P1, parity): the dashboard cannot change a machine's size

Reproduction: Overview > Machine > Resize… on `ubu`.
Happened: an inline "Grow to [40 GB] Grow Cancel", which changes the disk
only (20 to 320 GB). No page or action I found changes the size class. The
CLI does it with `repose resize --size xl`. machine.md line 230 sends people
who run out of memory to the CLI command.
Falsifier: a size control on Overview, the project list or Config. I found
none, and a source grep for one found none.
Capture: `26-resize-dialog-*`.

### L1 (P1): the landing page claims Nix

Reproduction: open `/` and find "Everything installed" in section 03.
Happened: the list includes `Nix`, and the text under it says "Add any Nix
package to every rebuild with repose config add." machine.md says that every
machine runs Ubuntu 24.04, and config.md says that `config add` installs menu
entries and apt packages. This is the first page a customer reads, and both
sentences are false for every new machine.
Falsifier: the landing page not rendering these strings. It renders them
(page text).
Capture: `40-landing-installed-*`.

### D7 (P2, P1 if main ships before PR 6): the x86 line in "Add a host"

Reproduction: Settings > Hosts > Add a host > Add.
Happened: the dialog shows "On an x86 box with Ubuntu 24.04" with the line
`curl -fsSL https://repose.run/install-host.sh | sudo bash -s -- --join …`.
That URL answers 404 locally and on production (`curl -w %{http_code}`,
2026-11-22), and no file named `install-host*` exists in the repo. This is
withheld on purpose (PR 6, per the brief). But production still serves the
NixOS docs, so the next release of main makes this line live, unless PR 6
lands first. own-hardware.md line 26 says the installer wipes a second disk.
The dialog does not say so, and this is a loss the customer cannot undo
(CLAUDE.md "Trust the reader" allows a line for that). The installer may ask
before it wipes, but I could not check, because the installer does not exist.
Captures: `34-add-host-steps-*`, `34-add-host-dialog-full.png`.

### D6 (P2): Re-apply on a failed revision

Reproduction: make a failed revision, for example with `repose config add
repose-fake-missing`. Then on Config > Revisions, click its Re-apply.
Happened: `POST …/revisions/{id}/apply` returns 400, and the toast says
"revision: only a successful revision can be re-applied". The page offers a
button that always fails. config.md line 72 says the Config tab "re-applies
one from its Revisions list".
Capture: `14-reapply-click-0-*`.

### D8 (P2): a failed revision does not say why

The Revisions row shows a date and "failed" and nothing more. The reason
(`menu_failed: … Unable to locate package …`) appears only in Overview >
Latest events and in the project list's Recent activity. Two rows with the
same minute (11:24, 11:24) cannot be told apart. Capture: `10-config-after-failed-cli-*`.

### D9 (P2): a waiting base update shows on the Config tab only

To reach the state, I used route interception that adds
`pending_base_version: "2026.11.30"` to GET /projects/{id}, as the repo's own
`tests/nonix.spec.ts` does. Config shows "Base 2026.11.30 is ready. It is
used at the next restart." and Restart, which is right (see below). Overview's
Details shows `Base 2026.09.15` with no mention of the waiting base, and so
does the project list. This agrees with the partner's C6 on the CLI. The docs
point to the Config tab, so this is a discoverability issue, not a false
statement. Captures: `22-pending-base-*`, `23-overview-pending-*`.

### D10 (P2): catalog copy that disagrees with the docs

The Config page says PostgreSQL's "data lives on the volume under
/var/lib/postgresql". config.md lines 39 and 58 say
`/var/lib/repose/services/postgresql/16`. Vercel CLI says it installs "at the
first boot after it is chosen". On an Ubuntu machine the menu installs into
the running machine (config.md line 25, and the log I watched). The partner
found the same strings on the CLI (C8).

### D11 (P2): refusals are raw API sentences

Every refusal I triggered appeared as a toast with the API message verbatim:
"revision: only a successful revision can be re-applied", "your host host-1
has not registered yet", and a fail-next 409 showed "forced by the test's
error switch". The real API's busy refusal for a config PUT is "a build is in
progress" (located at config.go:157, not run). On an Ubuntu machine nothing
is built. Capture: `38-conflict-refusal-*`.

### D12 (P2): one Escape loses the join step

After Add, the dialog says the step "is not shown again, and it works once".
Escape closed it at once, with no question. The host stays `waiting`, and the
only way back is Remove… and Add again. own-hardware.md line 81 documents
this, but it costs one stray key. A reload costs the same.

### D13 (P2): own-hardware.md for a Linux customer

- Lines 53 to 55 sit in the flow after the x86 installer (line 44 onwards),
  but they describe the Mac: `disk.raw`, "three quarters of the Mac's memory",
  `~/Library/Logs/repose`, and "The machine sets repose up and joins". A Linux
  reader cannot tell which applies to them.
- There is no x86 counterpart to "Restart your Mac and your machines start
  fresh" or to "Taking it off". `repose host rm --local` is described as run
  "on the Mac". Nothing says how to take the installer off an x86 box, or what
  happens to the disk it wiped.
- The landing page and its FAQ say "an x86 box with Linux and KVM". The docs
  require Ubuntu 24.04 and a second disk that the installer wipes.
- "About four minutes" to start is stated for the Mac only. The x86 start
  time is not stated. (The appliance numbers are provisional per the brief.)

### D14 (P2): the project tabs at 390 px

On `/projects/ubu/config` at 390 px, the tab strip has scrollWidth 469 and
clientWidth 335. The Config link's left edge is at 445 px, beyond the 390 px
viewport, and the strip has scrollLeft 0. The open tab is not visible and
nothing shows that the strip scrolls. Capture: `24-restart-confirm-390-dark.png`
(top of the page).

### D15 (P2): machine.nix settings for an Ubuntu-only account

With only erofs projects, the Settings nav correctly hides machine.nix. But
`/settings/machine-nix` still opens, with an editor and Save, and no line that
says Ubuntu machines never read the file. Capture: `32-machine-nix-direct-*`.

### D16 (P2): "until they are migrated"

machine.md line 277 says older machines keep their Nix files "until they are
migrated". It does not say who migrates them, when, or how a customer moves a
machine to Ubuntu.

### D17 (P2): two words for one host state

The Hosts table and the "Runs on" option say `waiting`. The create refusal
says `your host host-1 has not registered yet`. The docs use both.

## The fake (process findings)

These limit what this pass and the second pass can test. The partner's C3
covers the same ground from the CLI.

- F1: no admin switch for `default_guest_runtime`. Every project the
  dashboard creates is Nix: "dash-made" came back `guest_runtime: nix`. The
  dashboard's create form has no runtime choice, which is correct. I made the
  Ubuntu machine with `POST /v1/projects {"guest_runtime":"erofs"}` on the
  exempt account.
- F2: no admin route for `pending_base_version`. The banner is reachable only
  by route interception.
- F3: after Restart, `start` never moves `base_version` to the pending
  version, so the after-restart state cannot be seen. The fake also stops and
  starts instantly, so the page's "restarting" state cannot be seen either.
- F4: Snapshots > Restore… > Restore as new on the Ubuntu `ubu` (menu:
  memcached, bun, ffmpeg) made `ubu-restored` with `guest_runtime: nix` and
  an empty Nix fragment as its config. The real API copies the runtime
  (`restore.go:300`, located). So on the fake, the dashboard shows Nix UI for
  a restored Ubuntu machine.
- F5: the fake accepts a config PUT while a config op is running. The real
  API refuses it with 409 (D4).
- The erofs project's config response also carries a Nix `fragment` and the
  same base version (2026.09.15) as Nix projects.

## What is right, measured

- The erofs Config page: no Nix editor, no machine.nix toggle. Packages is the
  menu, grouped as Runtimes 10, Services 7, Tools 4 and Deploy 6. Version
  choices appear when you tick an entry: Node.js 20/22 (default 22) and
  PostgreSQL 15/16/17 (default 16), as config.md says. Captures `05-ubu-config-*`
  and `21-version-choice-*`.
- The install log streams line by line: the first line ("installing zig") at
  1.2 s, and "Applied." at 3.3 s against the fake's 3 s delay. A dashboard
  apply kept the CLI-added `ffmpeg`. The menu afterwards was
  `[{"id":"zig"},{"package":"ffmpeg"}]`.
- The waiting-base line uses exactly the docs' words. Restart opens an inline
  "Restart ubu? Its processes end; its files and services' data stay." Restart
  now sends `POST /stop {"snapshot":false}` and then `POST /start`, as
  config.md line 80 says. Ticking "Hold base updates" hides the line and sends
  `PATCH {"hold_base_updates":true}`.
- The Settings nav hides machine.nix when the account has no Nix project.
- With a waiting host, New project shows "Runs on: host-1 · waiting /
  repose's servers". Create is refused in the form ("your host host-1 has not
  registered yet"), and nothing is created, as own-hardware.md line 81 says.
- Add a host shows both steps with Copy buttons, and the join string wraps at
  390 px.
- The public docs `machine.md` and `config.md` are written for Ubuntu. A grep
  for Nix terms across `content/docs` found only lines labelled for older
  machines and laptop-side mentions.
- Local page loads (fake API, built site, one reading each): sign-in 1.15 s,
  project Overview 0.57 s, Config 0.58 s, Settings 0.57 s, Hosts 0.56 s,
  landing 0.67 s, and docs config, own-hardware and machine 0.70 to 0.71 s.
  Create from the dashboard reached the project page in 0.07 s.

## Brief and tree disagree (not filed as a defect)

The brief lists "own-hardware prices are not on the public page" as
deliberately absent. In the tree, own-hardware.md (lines 8 and 151),
billing.md (line 28) and the landing pricing section all say "$10 a month"
with seven days free. Commit 45772b28 added this on 2026-11-21 ("docs: own
hardware price", ENG-47). Production still serves the older docs. The
conductor should say which statement is current. I did not rank this.

## Method

What I ran: built `cmd/fakeapi`, `cmd/fake-logto` and `cmd/repose` with
`go build`, ran them in `tmux -L council-dash`, built the dashboard with
`pnpm build`, and served it with `node build` on port 5177, with
`PUBLIC_API_URL` and `PUBLIC_LOGTO_ENDPOINT` set to the fakes. Ran 24
Playwright scripts (in the session scratchpad). Each one captures the viewport
at 1x at 1440x900 and 390x844, light and dark. The CLI ran against the same
fake with a scratch `XDG_CONFIG_HOME`. I read every capture cited here at 1x,
as the viewport or a cropped region.

What I could not run: anything on a real guest (no ssh, by choice: the
partner found that the fake's ssh CA points at the production gateway); the
real API (D4 and D11 rest on located code, not runs); the production billing
catalogue (it needs a login, which this pass forbids); the x86 installer and
the Mac appliance (not published); an owned host reaching `online` with
erofs machines on arm64 (the fake has an `arch` switch, which I did not
explore).

Evidence rules that applied:
- "Before you trust a fake, make it produce the state": F1 to F5. D2 and D9
  needed route interception to reach their states.
- "Fakes must mirror refusals": I first wrote D4 as a concurrent bug. The real
  API refuses that case, so I narrowed D4 to the sequential case, which does
  not depend on the fake.
- "Check whether an absence is on purpose": D7, and the price item above.
- "One number is a margin": the D2 offsets are one reading at each width,
  shown with screenshots and not repeated.

Side effects on the fake during the pass: I applied revision `…0010` once
directly with the API while probing D6. I destroyed `dash-made` and
`ubu-restored` to reach the Ubuntu-only account. One host `host-1` was left
`waiting`.

## Captures

The captures are 172 PNGs, 13 MB, under `docs/reports/captures/` in this
worktree. They are untracked and not committed, because they exceed 1 MB.
Names are `<step>-<width>-<scheme>.png`: 01-projects-empty, 02-new-project,
03-machine-page-nix, 04-ubu-overview, 05-ubu-config (and -full),
06-search-ffmpeg, 07-config-with-apt-pkg, 08-apply-running, 09-apply-done,
10-config-after-failed-cli, 12/13-failed-apply, 14-reapply-click-0,
15-failing-install-running, 16-failing-install-done-viewport,
17-failing-install-done-log, 18-failure-at-click-{1440,390}, 19-reload-midflow,
20-two-tabs-tab2, 21-version-choice, 22-pending-base, 23-overview-pending,
24-restart-confirm, 25-restarting, 26-resize-dialog, 27-snapshots,
28-restore-panel, 30-settings, 31-hosts, 32-machine-nix-direct, 33-add-host,
34-add-host-steps (and -dialog-full), 35-hosts-after-add,
36-new-project-waiting-host, 37-create-refused-waiting, 38-conflict-refusal,
39-landing, 40-landing-installed, 41-held-and-pending,
42-docs-{config,own-hardware,machine}.

## Second pass (2026-11-22, main at 424cafcd)

Gate: **clean. No P0 is open.** No fixed finding has regressed. New
findings: one P1, which the CLI reviewer and I share (N1), and P2s.

The staging browser login does not complete. `/signin` redirects to
`http://172.206.225.242:8081/oidc/auth?...`, which answers `404 page not
found`. The stand-in's discovery document lists only `device_code`,
`refresh_token` and `client_credentials` grants. So every dashboard reading
below is from `cmd/fakeapi -default-runtime erofs -config-delay 3s`.
Real-machine readings come from the staging CLI (`~/repose-ws/repose-stg`,
build `stg-c460b3a2`, which contains 424cafcd) on one machine, `tmp-vffw`,
which I destroyed afterwards. `ls` then said "No projects for stgdev".

### Verification

| Joint row | Finding | Result | New behaviour (quoted) |
|---|---|---|---|
| 1 | D1, add an apt package | fixed | Searching "ffmpeg" shows `No package matches "ffmpeg".` followed by `An apt package: ffmpeg` with an Add button (accessible name "Add package ffmpeg"). After Apply the footer says `1 selected · Applied`. Menu: `[{"package":"ffmpeg"}]`. |
| 4 | D2, a failed install out of sight | fixed | The footer next to Apply says `Not installed: repose-fake-missing failed: E: Unable to locate package repose-fake-missing`, in view at 1440 and at 390 (`v-d2-failure-at-click-*.png`). Residual: Redis, ticked in the same Apply, unticks itself while the log says "redis ready". The footer names only the failed package. config.md:37 documents this. |
| 5 | D3, reload during an install | fixed | Reload 1.0 s into an install. At +0.4 s the footer says `Installing… / Applying…`. At +3.4 s it says `Applied`, with the log. Back then forward shows `Installing…` and the log line `installing deno`. |
| 6 | D4, stale tab | fixed | Tab B's Apply now says `The menu changed elsewhere since you loaded it, and is reloaded.` RabbitMQ is kept and shows ticked in B. B's own new tick (Ruby) is dropped and must be ticked again. The concurrent case gets the same refusal. |
| 7 | D5, size class | fixed | Resize… has a Size select and the line `Changing dash-ubu from large to small stops it, which ends every process on it, agents included, then starts it again.` with the buttons [Stop, change and start] and [Keep it running]. It sends `stop {"snapshot":false}`, `PATCH {"class":"small"}`, then `start`. The header says small 4.7 s later. |
| 8 | Fake F1, F2, F3, F5 | fixed | `-default-runtime erofs`: a machine created in the dashboard is `erofs`. `POST /pending-base` works. Restart moves `base_version` to 2026.11.30 and clears pending. A concurrent PUT is refused. |
| 8 | Fake F4 | partly fixed | Restore as new now keeps `erofs`. But the fake drops the menu: GET config has no `menu` and does carry a Nix `fragment`, and Config shows `0 selected` for a source machine with 6 items. The real API copies `cur.Menu` (`insertRestored`, restore.go; located, not run). |
| 8a | L1, landing page | fixed | `Every machine runs Ubuntu 24.04. ... Add a database from the menu, or any apt package, with repose config add.` The word Nix no longer appears on the page. |
| 9 | D7, x86 line | copy fixed | The dialog says `It keeps your machines' disks in /var/lib/repose/guests.img, or with --device takes over a whole empty disk.` The URL still answers 404 locally and on production, and no `install-host` file exists on main. The script is on `release/r20261122-10` (`apps/web/static/install-host.sh`), which contains 424cafcd, so it ships with that batch. The brief's statement that the installer "fails at its host daemon download step" is true of r10, not of main. |
| 10 | D9, waiting base | fixed | Overview Details: `Base 2026.09.15 · 2026.11.30 at the next restart`. The project list row says `new base at next restart`. |
| 13 | D10, catalog copy | fixed | PostgreSQL: `data lives under /var/lib/repose/services/postgresql, one folder per major.` Vercel: `Vercel's CLI, from npm.` On staging, `/var/lib/repose/services/postgresql/16` exists. |
| 16 | D6, Re-apply on a failed revision | not fixed | The failed row still offers Re-apply. Clicking it gets a 400 and the toast `revision: only a successful revision can be re-applied`. |
| 19 | D8, failed revision with no reason | not fixed | The row still says only `failed`. |
| 20 | D11, raw refusals | not fixed | The stale case now has its own sentence. Other refusals are still the API's text. |
| 21 | D12, Escape loses the join step | not fixed | After Escape, 0 dialogs are open. |
| 22 | D13, own-hardware.md layout | not fixed | The x86 requirements were rewritten. The disk.raw and ~/Library/Logs paragraph still follows the x86 installer paragraph. There is still no x86 reboot or removal text. |
| 23 | D14, tabs at 390 | not fixed | Config link left edge at 445, viewport 390, scrollLeft 0. |
| 24 | D15, machine.nix page | not fixed | `/settings/machine-nix` still opens, with Save. |
| 25 | D16, "until they are migrated" | not fixed | machine.md:277 is unchanged. |
| 26 | D17, two names for one state | not fixed | The list says `waiting`; the refusal says `your host host-1 has not registered yet`. |

STATUS.md line 775 lists rows 16 and 19 to 26 as not done, so these
results are expected.

### New findings, ranked

**N1 (P1, shared with the CLI reviewer): a real install says nothing while
it runs, and a failure gives no reason.** On staging, `config add postgresql
ffmpeg` printed `Added postgresql and ffmpeg to tmp-vffw.` and then
`Applied revision d7ae3dda in 1m07s`, with nothing between them (66.5 s,
`/usr/bin/time`). `config add redis` did the same in 20.5 s. Afterwards,
`repose logs tmp-vffw --kind build` held one line: `build switching the
machine`. config.md:16-20 says each line is "the machine saying what it
installed" and shows `✓ postgresql 16 ready ...`. The fake streams
`installing X / X ready`, so the fake hides this. The dashboard's Install
log prints op lines raw and has no case for "switching the machine" (grep of
apps/web; located, but I did not render it against staging, because the
login fails). A dashboard user would see that Nix-era line alone for a
minute. `config add ffmpeg-typo` exits 10 with `error: ffmpeg-typo failed:
apt-get exited 100`. config.md:33-34 promises `E: Unable to locate package
ffmpeg-typo`. On the dashboard this becomes `Not installed: ffmpeg-typo
failed: apt-get exited 100`. The services themselves are correct:
`repose-svc-postgresql` and `repose-svc-redis` are active, `psql
postgres://localhost/dev` answers, and `/usr/bin/ffmpeg` is present.
Falsifier: a second apply printing item lines. Neither of the two did.

**N6 (P2): three different x86 requirements.** Add a host says "On an x86 box
with Ubuntu 24.04". own-hardware.md now says "Ubuntu 24.04, or Debian 12 or
later". The landing page and its FAQ say "an x86 box with Linux and KVM".

**N7 (P2): an always-on line in Resize.** The Resize panel shows "320 GB is
the largest size." even while 80, 160 and 320 GB are still on offer.

**N8 (P2): searching for a docs name finds nothing.** The menu search does
not match the entry names that config.md's table lists and that `repose config
add` takes. `dotnet`, `awscli`, `go-tools`, `shell-extras` and `nodejs` each
give `No package matches` and no Add row: I-1508 hides Add for a menu id.
`jdk` matches only because the description says OpenJDK. A customer who
copies a name from the docs reaches a dead end. Capture: `h-search-dotnet-*`.

**N9 (P2): an invalid name is refused without a reason.** `FFmpeg`, `Bad
Name!`, `ffmpeg libpq-dev` (two names) and a 130-character name each give
`No package matches "…"` with no Add row and no reason. config.md:23 gives
the rule (lower case, digits, `+`, `-`, dots). The page never says it.

**N10 (P2): "Applied" on a stopped machine means "applied at next start".**
On staging, with the machine stopped, `config add zig` printed `Applied
revision d5662d1d in 0.5s`. Nothing was installed until `start`, which took
16.1 s against the 6 s reference; zig was present after the start. config.md:25
says this ("installs at its next start"), but neither surface says so at the
moment it happens. The fake streams install lines and `Applied` for a
stopped machine (fake divergence), so the dashboard's real wording for this
case cannot be seen on the fake.

**N11 (P2): the Snapshots tab doesn't say where snapshots go on owned
hardware.** For a machine on an owned host, Snapshots shows "No snapshots
yet. / Create snapshot". own-hardware.md puts in bold that these snapshots
stay on that host's disk and that repose keeps no copy. Nothing on the tab
says so.

**N12 (unverified, fake only): Apply on an offline owned host says
`Applied`.** The fake applied a menu change to a machine whose host had not
answered for 14 minutes. I have not checked what the real API does.

**Right, measured:** staging create took 6.0 s (`Created tmp-vffw ... 6.0s`)
against the 6 s reference. The machine runs `Ubuntu 24.04.5 LTS`. `df -h
/home/dev` shows `/dev/vdb 20G`. cloudflared, mise and psql are present, and
rustup has no toolchain, as machine.md says. The agent guide starts "an Ubuntu
24.04 virtual machine". The Own hardware purchase on the fake: Billing lists
Own hardware at $10. Before purchase, Add a host is refused in the dialog
with "Your plan does not include a host of your own. The Own hardware plan is
$10 a month for one host: choose it at https://repose.run/billing." After
checkout the plan shows "Trial. First charge of $10 on 2026-11-29." An arm64
host shows `online 24 GB · 8 cores`. New project offers `mini` by default,
and the machine created there is `erofs`. With the host quiet, Overview and
the list say `your host mini has not answered for 14m`.

### Method

Merged main 424cafcd into this branch and rebuilt the fakes, the CLI and the
dashboard. I made one attempt at a staging browser login, which failed as
described above. I then re-ran every dashboard row against the fake with
Playwright and nix Chromium at 1440 and 390, using the new switches
(`-default-runtime erofs`, `POST /pending-base`). The free hunt was about 30
minutes: one staging machine for the real install output and the docs
claims, and the fake for the menu search, the Own hardware flow and owned
hosts. Rules that applied: "make the fake produce the state" (F4, N10 and
N12 are where it still cannot); "fakes must mirror refusals" (N1 is the
fake being kinder than the machine). The captures from this pass are in
`docs/reports/captures/p2/` (untracked).
