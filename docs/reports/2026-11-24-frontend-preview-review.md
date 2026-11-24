# Front-end preview: customer review (ENG-222, ENG-223)

2026-11-24. One reviewer, the interface surface. Subject: the combined
preview of PR #202 (New machine, Messages) and PR #203 (Open in Claude),
`https://azurewoker-01.tail05d19d.ts.net:4722`, and the same build on a
private copy of the fake api. Brief: `~/briefs/frontend-users-2026-11-24.md`.
Promise under test: `docs/research/frontend-users-2026-11-24.md` P1.

Two customers:

- **Dana**, who never opens a terminal. She uses Claude Desktop, claude.ai
  and her phone. She just paid for repose and wants a machine her agent
  can work on.
- **The owner**, who lives in the terminal and called the old dashboard
  clunky and ugly.

## What would make a user scream

1. **P1. Dana clicks Open in Claude and lands in a dead end, and nothing
   told her why.** The button links Claude Desktop to `<project>.repose`.
   That name exists only in the ssh config that `repose login` writes, from
   a terminal. Without it, `ssh` answers `Could not resolve hostname
   todo-app.repose: Name or service not known`. The project page shows the
   button to everyone, and the docs' Claude Desktop section and the T3 Code
   tutorial never say that `repose login` must run on that computer first
   (F1).
2. **P2. The button shows on a Windows PC and on a phone, where it cannot
   work** (F2).
3. **P2. A machine made on the dashboard has nothing to talk to.** No code,
   no agent and no Messages section. The browser has no way to start an
   agent (F3).
4. **P2. A Solo customer's second machine is refused with the defaults.**
   The form defaults to `large`, which is the whole plan (F4).
5. **P2. The T3 Code tutorial promises "with the laptop closed" for a route
   that the same page says nobody has tested** (F5).

No P0. Nothing here loses a tenant's work or charges them wrongly. The
owner's brief keeps the terminal for the first move. So F1 is a P1 for its
silence, not a P0 for the terminal step.

## Verdict

**Would the non-terminal user stay? No.** What stops Dana is one thing:
after she pays, every door from her apps to the machine needs two terminal
commands first (`curl -fsSL https://repose.run/install.sh | sh`, then
`repose login`), and the screen that sends her to Claude Desktop does not
say so. She makes a machine in one keystroke. Then she clicks Open in
Claude, Desktop cannot resolve the host, and she has no message that names
the fix. A user who will run two commands once, if told, can stay. The
product's job is to tell her, on the button and in the Desktop section. The
research's step count ("2 steps, 0 in a terminal") is true only for a laptop
that already ran `repose login`.

**The owner's view: is the dashboard still clunky or ugly? No, for what
these two PRs touched.** New machine is one field, with the name already
chosen and selected. Enter makes the machine. Messages reads as a plain
thread, with Noto Serif headings, no gradients, no glows and no eyebrows in
the new parts. One clunk remains, on the Connect card. It leads with three
terminal commands in a box and puts the one action that needs no terminal
under them as a secondary button. For the owner that order is right. For
Dana it is backwards (F1).

## Findings

| # | Rank | What the customer sees | Evidence | Card |
|---|---|---|---|---|
| F1 | P1 | Open in Claude fails on a computer that never ran `repose login`, and nothing on the page or in the Desktop docs says so | below | ENG-224 |
| F2 | P2 | Open in Claude on Windows and on a phone | below | ENG-225 |
| F3 | P2 | A dashboard-made machine has no code, no agent and no Messages | below | ENG-226 |
| F4 | P2 | Solo plan: the second machine with defaults is refused | below | ENG-227 |
| F5 | P2 | T3 Code tutorial: an untested route is sold as "with the laptop closed"; the Codex row hints at a failure and gives no fix | below | ENG-228 |
| F6 | P3, unverified | After a start of more than a few seconds, the app opens with no fresh click; a browser may block that, and then nothing says "click again" | below | none |
| F7 | P3 | The tutorial's link "Desktop apps" goes to a section called "Other apps"; no page has that name | below | none |
| F8 | P3 | PR #203's body ends with a second, empty Why / In depth / For agents / Record set | `gh pr view 203`, after the Record block | none |
| F9 | P3, owner's call | One screen says "Projects", "No projects yet." and "New machine" | `10-first-visit-1440-light.png` | none |

Screenshots: all 68 are 1x viewport captures in
`~/previews/frontend-review/shots/`. The ones cited here are also in
`.dev/shots/frontend-review/` on this branch.

### F1. P1. Open in Claude needs `repose login`, and nobody says so

**Steps.** Sign in as a paying account with no machine. Press Enter (the
New machine form is open, with the name selected). The project page shows
Open in Claude. Its `href` is
`claude://code/new?ssh_host=slow-spruce.repose&ssh_folder=~`. On a project
with a known checkout it is
`claude://code/new?ssh_host=todo-app.repose&ssh_folder=/home/dev/todo-app`.

**What Desktop does with that host, from Claude's docs** (Desktop page,
"Open an SSH session from a link", read 2026-11-24): "An alias from
`~/.ssh/config` works as `ssh_host` only for people who have that entry ...
otherwise Desktop opens the dialog for adding an SSH connection." The host
is not a DNS name. On this box, with no repose ssh config:
`getent hosts todo-app.repose` exits 2, and `ssh -F /dev/null todo-app.repose`
prints `ssh: Could not resolve hostname todo-app.repose: Name or service not known`.

**What tells Dana.**

- The project page: nothing. The Connect card is three terminal commands
  and the button (`12-new-project-page-1440-light.png`,
  `33-proj-dash-made-top-390-light.png`).
- The empty account's page lists `curl ... | sh`, `repose login` and
  `repose run`, as a "Quickstart", with no link to the button
  (`10-first-visit-1440-light.png`). After the first machine exists, that
  block is gone.
- Docs, ssh-and-editors, "Claude Desktop": "On the project's page, Open in
  Claude opens a new session in Claude Desktop's Code tab ..." It has no
  word on `repose login`. Only the page's first section says "After repose
  login, every one of your projects is an SSH host".
- Docs, T3 Code tutorial: "Type the host todo-app.repose. T3 Code connects
  with your own ssh ..." It has no word on `repose login` either.

**Expected.** The button, or a line beside it, says once that this computer
needs `repose login` first, with the two commands. The Desktop section and
the T3 tutorial say the same. A better fix lets the page know whether this
browser's computer has logged in. That needs a decision and is not mine to
propose.

**Instrument.** Playwright reading the `href`. `getent` and `ssh` on this
box. Claude's docs, quoted. **Falsifier:** Desktop resolves
`<project>.repose` with no repose ssh config. It cannot, because the name
has no DNS. A real Desktop was not run here (no Mac), so Desktop's exact
error text is not recorded.

### F2. P2. Open in Claude on Windows and on a phone

- With a Windows Chrome user agent, the Connect card and the link are
  identical: `claude://code/new?ssh_host=todo-app.repose&ssh_folder=/home/dev/todo-app`.
  The same docs page says: "An editor or app installed on Windows, Claude
  Desktop included, can't reach a project yet." The Claude Desktop section
  says "on macOS or Linux". The button says nothing.
- With an iPhone user agent at 390, the button shows
  (`34-proj-old-app-connect-390-dark.png`). Claude Desktop does not run on
  a phone. What iOS does with `claude://code/new` is unverified: either
  nothing happens, or the Claude app opens on a page with no SSH.
- Dana's phone path is Remote Control (research P2, not built). The phone
  page shows her three terminal commands and a button for a desktop app.

**Instrument.** Playwright with `userAgent` set. **Falsifier:** the page
hides or words the button differently for those agents. It does not.

### F3. P2. A machine made on the dashboard has nothing to talk to

- New machine, Options, Repository: under the field the form says "Not
  cloned. `repose run` in that checkout uses this machine."
  (`32-new-options-390-dark.png`). This is honest. But for Dana, the field
  then does nothing she can use.
- The new machine's Overview has no Messages section, because no agent runs
  (`12-new-project-page-1440-light.png`, `dash-made` captures). The browser
  cannot start an agent. PR #202 lists that as "Not done" and hands it to
  ENG-222. The research killed a dashboard chat as a front end (P4), but
  not "start an agent with this prompt".
- The owner's parity rule (2026-11-17): a CLI-only feature is not done.
  `repose run -p` has no dashboard twin.
- What Dana can do: Open in Claude at `~` (after F1), then ask Claude to
  clone. For a private repository, the machine has no GitHub login unless
  `repose run` copied it from a laptop.

This needs the owner's decision (start an agent from the dashboard, or say
plainly that the dashboard does not), so its card is in Needs you.

### F4. P2. Solo plan: the second machine with defaults is refused

On the paying fake (`fakeapi -billing`, Solo, 8 GB), the first Enter makes
`large · 2 cores · 8 GB`. The sidebar then reads "8 GB of 8 GB running".
Press `n` and Enter: `402`, and the form says "Your Solo plan runs 8 GB at
once and slow-spruce is using it. Stop it. See your plan", with Options
opened (`14-second-machine-plan-full-1440-light.png`). The refusal is clear.
But the default size can never fit beside a running machine on Solo, and
the form does not offer `small`. PR #202 says "Size default stays `large`,
the api's".

### F5. P2. T3 Code tutorial and the Other apps table

- The tutorial's lead: "drive Claude Code and Codex threads on it, with the
  laptop closed." Its body: "We haven't tested this route in the app yet."
  The research said not to sell Desktop-style sessions as "keeps running
  with the laptop closed" until it was measured.
- Other apps, "Tested in the app": "Not yet" on all three rows (T3 Code, the
  Codex app, Antigravity). This is honest, and the brief knows it (no Mac
  here).
- The Codex row: "Not yet. Codex lists the hosts in ~/.ssh/config; repose's
  are in a file that ~/.ssh/config includes". It hints at a failure and does
  not say what to do. The research named a one-line workaround to document
  (acceptance C4).

### F6. P3, unverified. The link after a long start

A stopped machine: click sends one `POST /projects/:id/start`. The button
and the header read "Starting…", disabled
(`24-oic-old-app-starting-6s-1440-light.png`). With the start op held 12 s
(a Playwright route, the hosted start time in `REVIEW.md`), the page asks
for `claude://...` at 12.0 s (CDP `Page.frameRequestedNavigation`, reason
`anchorClick`). Chrome and Safari may block an app launch 12 s after the
click. If they do, the button is the link again, but no text asks for a
second click. Headless Chromium with no `claude://` handler cannot show
this. Run it on the owner's Mac (owner-mac-script, section C2).

### F7. P3. "Desktop apps" link

`tutorial-t3-code`: "The machine side is tested: Desktop apps." links to
`/docs/ssh-and-editors#other-apps`. That section is called "Other apps". PR
#203 explains why the page became two sections. The link text did not
follow.

## What works, measured

- **First visit to a running machine.** Signed in with no machine: 0 clicks,
  1 keystroke (Enter). The page opens on the form with the name selected
  (focus on the input, value `snug-river`). 47 words in `main`. From the
  landing's Sign in: 2 clicks and 1 keystroke, 14.5 s end to end on the fake
  (which boots at once, so the time measures the page, not a machine).
  Before (PR #202's own count): 2 clicks and a name to invent.
- **Open in Claude, the link.** Exactly
  `claude://code/new?ssh_host=todo-app.repose&ssh_folder=/home/dev/todo-app`.
  No `q`, no user, no token, no project id. `ssh_folder=~` with no checkout
  is allowed by Claude's docs ("Start it with `/` or `~/`, or use `~`").
- **Open in Claude, the states.** Running: the app is asked for at 0.1 s.
  Frozen: at 0.0 s, with no start call. Stopped: one start, "Starting…",
  then the link (F6). Refused start: the api's sentence as a toast ("Your
  Solo plan runs 8 GB at once and todo-app is using it."), and no app is
  asked for (`22-oic-docs-site-refuse-2s-1440-light.png`).
- **Stopped Connect card.** It shows `repose ssh old-app` instead of
  `ssh old-app.repose`, because plain ssh does not start a machine. This is
  correct.
- **Messages.** Enter sends. Shift+Enter makes a new line. The text is
  posted as one message with `\n`. "Delivered to claude" shows at 0.5 s, the
  agent's reply at 8 s at the latest (the fake answers at 4 s), and both
  survive a reload (`40-msg-sent-reply-1440-light.png`). Enter in an empty
  box sends nothing. With two agents, the one that needs input is chosen
  first (`codex-2 · needs input`). A working agent shows "claude is working"
  under the delivered message. On a stopped machine the box is disabled and
  says "The machine is not running."
- **Layout.** No horizontal scroll at 390 on four project pages
  (`scrollWidth` 390). At 390 the tab strip cuts "Snapshots" to "Sn" and
  scrolls. This is not new.
- **CLI.** `repose code --editor claud` answers `--editor must be one of
  code, cursor, zed, claude, got "claud".`, exit 2.
- **Docs that are honest about the terminal.** Overnight ("Start work that
  must run overnight with repose run -p or in repose attach"), a stopped
  machine in a saved Desktop environment ("Use Open in Claude or repose
  start"), Windows, and the 1 GB Desktop install.

## Where the path still needs a terminal

| Step | Terminal? | Do the docs say so? |
|---|---|---|
| Install the CLI and `repose login` (writes `<project>.repose`, renews the certificate) | Yes, once per computer | Only on ssh-and-editors' first section. Not on the button, not in the Desktop section, not in the T3 tutorial (F1) |
| Move an existing project (`repose run`) | Yes | Yes. The owner keeps this. |
| Start an agent with no desktop app | Yes (`repose run -p`, `repose attach`) | Not on the dashboard (F3) |
| Overnight work | Yes | Yes, in the Desktop section |
| T3 Code from a browser | Yes (`ssh ...`, `repose open 3773`) | Yes |
| Phone (Remote Control) | Yes (config.toml, consent `y`) | Yes, in agents.md. Research P2, not built |

## Method

- **Ran.** The preview at `:4722` (seeded, billing off, exempt): sign-in and
  a read of the list. A private copy of the same build
  (`~/previews/open-in-claude/build`, `fakeapi`, `fake-logto`) on
  127.0.0.1:4793-4795, started from `~/previews/frontend-review/run.sh` in
  `tmux -L fr-review`. First with `-billing` (a paying Solo account, empty)
  for the first visit and F4. Then without billing, seeded as
  `~/previews/open-in-claude/seed.sh` does, for the Open in Claude states
  and Messages. I did not change the shared preview's state. Playwright
  with nixpkgs Chromium, 1x, 1440x900 and 390x844 (iPhone user agent,
  touch), light and dark. CDP `Page.frameRequestedNavigation` stands in for
  "Desktop opened". The branch CLI (`go build ./cmd/repose` from
  `open-in-claude`) for the refusal.
- **Could not run.**
  - Claude Desktop, T3 Code, the Codex app and Antigravity: no Mac or
    Desktop client here. The owner's Mac script covers them.
  - `repose code --editor claude` past its ssh check: the fake has no ssh
    gateway, so it stops at "did not answer ssh in 15 s".
  - A real 12 s start: the fake boots at once. I held the op with a
    Playwright route (F6).
  - Where Desktop keeps its sign-in on the machine (research B4): needs a
    real Desktop session.
- **Rules that bit.**
  - *Fakes must mirror refusals.* My first slow-start simulation answered
    "op done" while the project still read `starting`. The page then never
    opened the link. The api contract gives every op a `project_state`, so
    that pair is probably not real. I dropped it and held the op itself
    instead, and then the link opened.
  - *Locate the hit.* At 1x, "Usage" looked brighter than the other tabs.
    The computed colours are the same, and a 2x crop is even. Not filed.
  - *Check whether an absence is on purpose.* The frost strip on a frozen
    row is a gradient. It is on `main` since 44e70933 (I-892, I-898).
    Not filed.
  - *Do not rank from the only state you could reach.* The shared preview
    is billing-exempt. F4 exists only on a paying account, which is why I
    ran `-billing`.
