# The user who does not live in a terminal (ENG-222)

2026-11-24. Research only. No product change. Brief:
`~/briefs/frontend-users-2026-11-24.md` (owner, 2026-11-23 and 24). Outside
facts were read from the page or the API on 2026-11-24 and carry their URL.
A fact that could not be read from a primary page is marked *unverified*.

## Verdict

**Do not build a front end, and do not serve one.** The front ends that
users choose now open a remote machine over the user's own SSH. Claude
Desktop, the Codex app, T3 Code's desktop app, Antigravity, Zed, VS Code and
Cursor all do this. Every repose project is already an SSH host,
`<project>.repose`, in the user's `~/.ssh/config`. So the gap is smaller and
different from what the brief assumed. Users can get to the machine. What is
missing is the last step:

- nobody has tested a repose machine in these apps;
- nothing points the user there;
- a stopped machine fails in the app with no explanation.

**Build first: "Open in Claude".** A project page button and a CLI verb open
the machine as a Claude Desktop SSH environment, through Claude's own
documented deep link
`claude://code/new?ssh_host=<project>.repose&ssh_folder=<checkout>`. The
button starts a stopped machine first. The work includes three more parts:

- a guest-side check that Desktop's remote requirements hold;
- one public page, "Desktop apps", with a tested row for each app that
  speaks SSH;
- a rewrite of the T3 Code tutorial from "forward port 3773 and keep a
  terminal open" to "add `<project>.repose` as an SSH environment in T3
  Code".

**Follow-up (at most one): the phone.** Desktop SSH sessions do not reach the
phone. Claude's docs also warn that such a task can stop overnight. The phone
path stays Claude's Remote Control. The follow-up gives it a dashboard switch
and a measured idle cost. Do not build a repose session list.

**The owner's hunch is half right.** For Codex, a GUI leads on OpenAI's own
word. For Claude Code, no surface split exists in public, and every survey
still calls it terminal-first. The demand for a GUI on an always-on machine
is real and large in absolute terms, as section 1 shows. The hunch's
corollary, "T3 Code on repose means a port to remember", was true of our own
tutorial, not of T3 Code.

---

## 1. Who these users are: the evidence

### 1.1 The surface split

| Measure | Value | Date | Source |
|---|---|---|---|
| Codex's most-used surface | "The Codex App is now our most used surface, ahead of the VS Code extension and the CLI." (Tibo, OpenAI) | 2026-04-03 | https://x.com/thsottiaux/status/2039901964679688437 (seen as a search-result title, page not fetched) |
| Claude Code for VS Code installs (`anthropic.claude-code`) | 30,905,414 installs, rated 3.71 by 864 people | 2026-11-24 | VS Code Marketplace API |
| Codex VS Code extension installs (`openai.chatgpt`) | 17,118,594 installs, rated 2.63 by 959 people | 2026-11-24 | VS Code Marketplace API |
| npm `@anthropic-ai/claude-code` | 20.7 M a week (2026-11-17 to 23) | 2026-11-23 | api.npmjs.org |
| npm `@openai/codex` | 27.7 M a week | 2026-11-23 | api.npmjs.org |
| Pragmatic Engineer survey (900+ people) | Claude Code is the most-used tool and is called "terminal-first". No split by surface | 2026-02 | https://newsletter.pragmaticengineer.com/p/ai-tooling-2026 |
| T3 Code | 77,156 stars, 3,639 open issues | 2026-11-24 | `gh api repos/pingdotgg/t3code` |
| Happy (phone client for Claude Code and Codex) | 24.8 k stars | 2026-11-24 | github.com/slopus/happy |

These units do not compare. npm counts installs, CI runs included.
Marketplace counts are cumulative. Stars are not users. No Anthropic number
for its desktop, web or mobile share was found. The issue tracker leans
terminal: the most-reacted claude-code issues include terminal scroll and
flicker bugs (#3648, #826, #769).

**What this supports.** A large GUI population exists:

- the VS Code extension, with 30.9 M installs;
- the Codex app;
- T3 Code, with 77 k stars;
- Happy, with 25 k stars.

Anthropic itself has shipped Remote Control, a Code tab on mobile, Desktop
SSH and a web product. "Average user" is unproven. "A segment we do not
serve well, and a large one" is proven.

**The segment the brief missed.** The biggest GUI population for Claude Code
is probably the **VS Code extension**. repose already serves it with
`repose code`, which uses the extension in a Remote-SSH window. The Claude
Code changelog for 2.1.331 lists "[VSCode] Improved speed in remote windows
(SSH, WSL, containers)". Our public docs never say that the Claude Code
extension works in that window.

### 1.2 What hurts, in users' words

The full list is 36 quotes, collected on 2026-11-24. Reddit refused every
automated fetch, so Reddit is absent; the rest is HN and GitHub. Counts by
theme:

| Theme | Quotes | Example |
|---|---|---|
| A. A do-it-yourself box with tmux, Tailscale and a phone terminal: it works, but it is clumsy on a phone | 13 | "I tried Termius on my phone but it was quite painful the moment I tried to paste screenshots or record voice notes." ([HN](https://news.ycombinator.com/item?id=49097326), 2026-07-29); "People tried reinventing terminals, SSH, and tmux for phones. It's a pretty terrible experience using your thumbs. And it takes significant know-how to set up." ([HN](https://news.ycombinator.com/item?id=47152678), 2026-02-25) |
| B. Remote Control is unreliable | 10 | "All Remote Control sessions silently die after ~5-30 minutes of idle time" ([#32982](https://github.com/anthropics/claude-code/issues/32982), 88 reactions); "silently drops the connection to the Claude iOS app" ([#34255](https://github.com/anthropics/claude-code/issues/34255), 112 reactions) |
| C. Sessions are hard to find and tell apart | 6 | "the session does not appear in the session list ... there is no way to reconnect to it" ([#28402](https://github.com/anthropics/claude-code/issues/28402), open, 38 reactions); "With dozens of sessions on the main computer, the few sessions on the second computer get buried" ([#111153](https://github.com/anthropics/claude-code/issues/111153), open, 2026-11-19); "same-repo multi-machine fleets are indistinguishable" ([#76898](https://github.com/anthropics/claude-code/issues/76898), closed) |
| D. People want the GUI to drive their own always-on machine | 7 | "always-on, remotely-accessible Claude Code instances on headless servers — no terminal multiplexer required" ([#30447](https://github.com/anthropics/claude-code/issues/30447), open, 54 reactions); "the Claude Code process on the remote server terminates with it. There's no way to reconnect" ([#49790](https://github.com/anthropics/claude-code/issues/49790), Desktop SSH, open, 48 reactions); "All my work runs on an always-on, headless Mac mini ... the conversations are tied to the one [client]" ([#104393](https://github.com/anthropics/claude-code/issues/104393), open) |
| E. Third-party front ends: pairing, auth and trust in the relay | about 8 | T3 Code [#1928](https://github.com/pingdotgg/t3code/issues/1928) "Failed to fetch remote auth endpoint" on a headless VPS; [#5093](https://github.com/pingdotgg/t3code/issues/5093) mobile pairs, then the websocket fails; Omnara's founder: "the messages flow through our server ... for a tradeoff of the data not being local" ([HN](https://news.ycombinator.com/item?id=44879061)) |
| F. Ports and previews on a remote box | about 3 | Thin evidence. No complaint found that is about port forwarding itself. Desktop SSH sessions lack the in-app browser ([#97729](https://github.com/anthropics/claude-code/issues/97729), 12 reactions) |

I checked these issue numbers myself with `gh issue view` (state, date,
reactions): #49790, #28402, #30447, #104393, #96867, #76898 and #111153.

**What the themes say.**

- **A and D are repose's market.** People rent a box so that the agent keeps
  going. Then they suffer the terminal to reach it.
- **B and C are Anthropic's bugs in Anthropic's list.** repose cannot fix
  them by building a list beside Claude's.
- **F is the owner's T3 pain.** It appears in our tutorial, not in the
  field.

Two HN threads show the size of the interest: Remote Control's launch had
544 points and 313 comments
([47148454](https://news.ycombinator.com/item?id=47148454)), and Claude Code
on the web had 578 points and 390 comments
([45647166](https://news.ycombinator.com/item?id=45647166)).

---

## 2. What each front end connects to today

All facts were read on 2026-11-24.

### 2.1 Claude Desktop (the Code tab): SSH to any host in `~/.ssh/config`

Source: https://code.claude.com/docs/en/desktop and
https://code.claude.com/docs/en/deep-links.

- **Environments.** Local, Cloud, **SSH** ("runs on a remote machine you
  connect to over SSH, such as your own servers, cloud VMs, or dev
  containers") and WSL.
- **It uses the user's own OpenSSH.** "Desktop connects by running the
  OpenSSH client on your computer, so it uses your `~/.ssh/config`." It
  honours "`HostName`, `User`, `IdentityFile`, `CertificateFile`,
  `ProxyJump`, and `ProxyCommand`". OpenSSH certificates were fixed on Mac
  from 2.110.0 ([#91790](https://github.com/anthropics/claude-code/issues/91790),
  closed 2026-11-18).
- **Client versions.** macOS and Linux need v1.37937.0 or later with OpenSSH
  7.6 or later. Windows needs v2.2553.0 or later with Microsoft OpenSSH 9.4
  or later, and not the Store build.
- **What Desktop installs on the remote.** "Desktop installs a helper program
  and Claude Code under `~/.claude/remote/` ... Allow about 1 GB of disk
  space in the home directory of each user who connects."
- **Remote requirements:**
  - "an SSH server with SFTP";
  - an install folder not mounted `noexec`;
  - a shell that runs commands sent over SSH;
  - glibc 2.17 or later;
  - network access.
- **Version pin.** The Desktop changelog of v7.37095.0 (2026-11-24): "Changed
  the Claude Code version that SSH hosts need to 2.1.331."
  (https://claude.com/docs/cowork/claude-desktop-changelog). The repose base
  pins 2.1.330 (`nix/overlay/agents/versions.json`). Desktop installs its own
  copy, so this does not block Desktop. It does mean two Claude Code versions
  on one machine.
- **Persistence.** "Claude Code runs as a process on the remote machine,
  under a background helper ... so it doesn't need your SSH connection to
  keep running." But: "If you sign in to Desktop with a Claude account, don't
  count on a task to run overnight. Desktop renews that sign-in for the task
  only while Desktop is running, connected, and signed in ... it can be only
  a few minutes." Open issue
  [#49790](https://github.com/anthropics/claude-code/issues/49790)
  (48 reactions) is about this.
- **The deep link** (v2.110.0 or later):
  - The form is
    `claude://code/new?ssh_host=...&ssh_port=...&ssh_folder=...&q=...`.
  - `ssh_host` is required. `ssh_folder` starts with `/` or `~/`.
  - "Desktop asks you to confirm before it selects the connection."
  - "A link can't carry a key file, a password, or a command."
  - The prompt is never sent automatically.
  - No link opens an existing session.
- **`sshHostCommands`** (v3.6556.0 or later on macOS and Linux) in the
  laptop's `~/.claude/settings.json`:
  - Each entry has `id`, `name` and `command`. It adds an item to the
    environment menu.
  - The command "has to exit with code 0 and print only the host", either as
    a line or as JSON `{"sshHost","sshPort","startDirectory"}`.
  - The time limit is 10 minutes.
  - "Desktop doesn't stop or delete machines."
- **`sshConfigs`.** An admin pre-fills connections. A user may also put them
  in their own settings.
- **Limits in an SSH session:**
  - no Browser pane tools;
  - the file pane is view-only;
  - Merge when ready is disabled;
  - the session can't be moved to the cloud.
- **Not found on the page.** The page does not say whether Remote Control
  ("Connect new sessions to Remote Control") applies to SSH sessions. The
  phone question in section 4 starts here.

### 2.2 claude.ai/code on the web

Source: https://code.claude.com/docs/en/claude-code-on-the-web and
https://code.claude.com/docs/en/self-hosted-environments.

- Sessions run on Anthropic's sandboxes or on **self-hosted environments**.
  Self-hosted environments are a public beta on Team and Enterprise only and
  are off by default. A `claude self-hosted-runner` polls the API, clones the
  repo and runs one child Claude Code process per session.
- The model is one task per clone, not one persistent machine. The page sends
  always-on users elsewhere: "use Remote Control, which is also available on
  Pro, Max, and Ultra plans". Pro and Max users ask for self-hosted
  environments in
  [#91841](https://github.com/anthropics/claude-code/issues/91841).
- **For an individual's own machine, Remote Control is the only door from
  the web and the phone.**

### 2.3 Remote Control: naming and finding

Source: https://code.claude.com/docs/en/remote-control.

- **Naming.** Names come from `--name`, else from `/rename`, else from the
  last message, else `<hostname>-<two words>`. The prefix comes from
  `--remote-control-session-name-prefix` or the environment variable
  `CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX`.
- **Finding.** The user finds a session by name in the list, from the printed
  URL, or with the QR code. The list has no grouping or filter by machine.
  [#111153](https://github.com/anthropics/claude-code/issues/111153) is open;
  [#76898](https://github.com/anthropics/claude-code/issues/76898) and #73343
  are closed with no feature documented.
- **repose already names everything by the project.** The server window runs
  `--name <slug>` (I-1132), and a guest's hostname is its slug (I-550), so
  auto-connected windows show as `<slug>-<two words>`. The owner's pain about
  finding sessions is Claude's list, not our names.
- **What still blocks a front-end-only user** (I-1132, I-1134):
  - the one-time `Enable Remote Control? (y/n)` must be answered in a
    terminal;
  - the switch is a line in `config.toml`;
  - the default is off, because the idle cost was never measured.

### 2.4 Other front ends

| Product | How it reaches a remote box | Works with `<project>.repose` today? | Source |
|---|---|---|---|
| **T3 Code** desktop (v0.1.11, 2026-11-20) | Settings → Environments → Add environment → SSH, "a host or SSH alias". "T3 Code starts or reuses a server there and opens the port forward for you." Downloads its server to `~/.t3/runtime`. Agent CLIs must be on the PATH of a non-interactive login shell | Expected; our tutorial says "We've tested the browser route above and not the desktop app's" | github.com/pingdotgg/t3code `docs/user/remote-access.md` |
| T3 Code phone and web (app.t3.codes) | T3 Connect: a relay on Clerk identity, "the relay Worker is not in the data path"; 3 machines free, 10 on T3 Chat Pro | Through T3's own relay, which is T3's to run | `docs/internals/06_t3-connect.md` |
| **Codex app** | "Codex reads concrete host aliases from `~/.ssh/config`, resolves them with OpenSSH, and ignores pattern-only hosts"; starts `codex app-server` over SSH | Expected. **Unverified whether it reads hosts inside an `Include`d file**, which is where repose writes them | https://learn.chatgpt.com/docs/remote-connections |
| Codex phone | Pairs with "hosts running the ChatGPT desktop app on macOS and Windows"; `codex remote-control pair` exists in the CLI source, undocumented | No (a Linux host is undocumented) | https://learn.chatgpt.com/docs/mobile |
| **Antigravity** (App v2.33.0+) | SSH by config alias; server in `~/.antigravity-hub-server/`; needs `AllowTcpForwarding yes` (the guest has it) | Expected | https://antigravity.google/docs/ssh/ |
| **Zed** | Local `ssh` and ssh config; `zed://ssh/host/path` | Yes: `repose code --editor zed` | zed.dev/docs/remote-development |
| **VS Code, Cursor** | Remote-SSH; `vscode://vscode-remote/ssh-remote+HOST/path` | Yes: `repose code` | code.visualstudio.com/docs/configure/command-line |
| Cursor My Machines | `agent worker start`, outbound HTTPS | Not built (I-1138, base closure) | cursor.com/docs/cloud-agent/bring-your-own-machine |
| Conductor (v0.99.0) | Mac only; "own machines: Not yet" | No | conductor.build/docs/cloud/faq |
| Happy | SSH only to install, then its own WireGuard ("Tailcat") and an end-to-end encrypted relay | Possible, its own path | happy.engineering/guides/remote-server |
| CloudCLI (claudecodeui), Vibe Kanban, `opencode web` | A web server you host on the box; CloudCLI "does not include built-in authentication" | Only behind a port forward or a tunnel | cloudcli.ai/docs; opencode.ai/docs/web |

**"Delight".** The only product under that name is
[bhandras/delight](https://github.com/bhandras/delight): 4 stars, a
self-hosted mobile remote control for Codex and Claude Code over a "blind
mailbox server". It is probably not what the owner meant. The owner should
name it if it matters.

### 2.5 What the repo says a front end needs from a machine

| Need | State in the repo |
|---|---|
| Resolve `<project>.repose` with the user's OpenSSH | `repose login` puts `Include ~/.ssh/repose/config` at the top of `~/.ssh/config`. A `Match originalhost "*.repose" exec "repose ssh-prepare %n"` line renews the certificate. Concrete `Host` blocks carry `CertificateFile` (`internal/cli/sshprepare.go`, `cert.go`) |
| SFTP, exec, subsystems | The gateway relays `exec`, `shell` and `subsystem` (`internal/gateway/relay.go:401`). The guest is NixOS openssh with the default `allowSFTP`; no line in `nix/guest/base/ssh.nix` turns it off. *Unverified on a live guest*, and on the Ubuntu (no-Nix) base |
| Port forward (T3 Code, Antigravity) | `AllowTcpForwarding = true` in the guest |
| Exec in the home folder | `/home/dev` is not `noexec`; only the login share is (`claude-auth.nix`) |
| The Claude login | The host's login share is bind-mounted over `~/.claude/.credentials.json` (I-278), so any `claude` on the machine, Desktop's copy included, finds the user's login there |
| A stopped machine | "a plain `ssh todo-app.repose` never starts a stopped machine" (`ssh-and-editors.md`). Every app above then fails with an SSH error |
| A frozen machine | "Anything that reaches the machine wakes it first" (`lifecycle.md`), SSH included |
| Starting with no terminal at all | Impossible today: the certificate and the `Host` blocks come from the CLI. A dashboard-made project is an empty machine (I-1354) |

---

## 3. Candidate pathways

**How the steps are counted.** One step is one thing the user does: a
command typed, a click that changes state, a field filled, or a choice made
in a list. A first visit means a user who has repose's CLI and a project
already moved with `repose run`. The first move stays in the terminal,
because the brief keeps it there. A later visit means coming back the next
day.

**Today, Claude Desktop, web or phone, through Remote Control** (from
`agents.md`):

1. edit `~/.config/repose/config.toml` (`remote_control = true`);
2. `repose attach`;
3. switch to the `remote-control` window;
4. answer `y`;
5. detach;
6. open claude.ai/code or the app;
7. find the session in a list that has no machine grouping;
8. open it.

That is 8 steps, 5 of them in a terminal, plus the one-time `/login` in
`claude` (3 more). A later visit takes 3 steps (open, find, open) while the
machine runs. While it is stopped, the user needs a terminal (`repose start`)
and then the same 3.

**Today, T3 Code** (from `tutorial-t3-code.md`):

1. `ssh … 'npx -y t3@latest service install'`;
2. `repose open 3773`, which must stay running;
3. `ssh … 't3 pair'`;
4. open the pairing URL before it expires;
5. add `/home/dev/<folder>` as a project;
6. start a thread.

That is 6 steps, 3 of them in a terminal. A later visit takes 2 terminal
steps (`repose open 3773`, open the browser) for every session, because the
forward dies with the terminal.

### P1. Open the machine in Claude Desktop, and in every app that speaks SSH: RECOMMENDED

**What it is.** No new protocol. repose makes the documented SSH-environment
path of these apps work on a repose machine, proves it, and puts one button
on it:

1. **Guest check and fixes.** A VM check and a staging run prove each of
   Desktop's requirements on both bases: SFTP; non-interactive exec; a
   helper that runs from `~/.claude/remote/` and survives the SSH session's
   end; the login share read by Desktop's Claude Code; repose's hooks firing
   for a Desktop session; 1 GB of home disk on a `tiny`. Fix what fails.
2. **The dashboard button.** The project page's Connect card gets **Open in
   Claude**, a link to
   `claude://code/new?ssh_host=<slug>.repose&ssh_folder=/home/dev/<checkout>`.
   - A stopped machine gets `POST /projects/:id/start` first, and the link
     opens when the project reads `running`.
   - A frozen machine needs nothing, because SSH wakes it.
   - The link never carries `q`, a token or a user. The host's `User` comes
     from the user's ssh config.
3. **CLI parity.** `repose code --editor claude [PROJECT]` opens the same
   link. `repose code` is already the verb that opens an app on a machine.
4. **Docs.** A new public page, "Desktop apps", has one tested row for each
   of these apps: Claude Desktop, T3 Code desktop, Codex app, Antigravity,
   Zed, and VS Code and Cursor with the Claude Code extension. Each row says
   what to type in the app, `<project>.repose`. The T3 Code tutorial moves
   to T3's SSH environment. `ssh-and-editors.md` says the Claude Code
   extension runs in the `repose code` window. The overnight limit is said
   once: a Desktop task can stop when Desktop's sign-in lapses, so start
   overnight work with `repose run -p` or from the phone (P2).

**Evidence it will be used.**

- Desktop SSH exists and is documented, and Anthropic still invests in it:
  v7.37095.0 is from today.
- Its open issues are people already using it on their own remote boxes:
  [#49790](https://github.com/anthropics/claude-code/issues/49790) (48),
  [#104393](https://github.com/anthropics/claude-code/issues/104393)
  ("always-on, headless Mac mini") and #97729 (12).
- The Codex app's SSH projects, T3 Code's SSH environments and
  Antigravity's SSH show three more vendors betting on the same door.
- Theme D (7 quotes) is this user.

**Steps after, for Claude Desktop:**

- First visit: click **Open in Claude** on the project page (the machine
  starts if stopped), then confirm the connection in Desktop. That is 2
  steps, 0 in a terminal, against 8 (5 in a terminal) for Remote Control.
  The `/login` in a terminal is also likely gone, because the login share is
  read by Desktop's copy. *Unverified*; it is acceptance step B4.
- Later visits: pick the saved environment in Desktop, 1 step, or click the
  button, 2 steps. A stopped machine costs no terminal step when the user
  comes through the button.

**Steps after, for T3 Code:** add environment, then SSH `<project>.repose`,
then add the project folder, then start a thread. That is 4 steps, 0 in a
terminal, with no port and no pairing link, against 6 (3 in a terminal) and
a terminal held open forever. A later visit takes 1 step. A stopped machine
still needs the button or `repose start`; T3 has no deep link to a host
that we found.

**The gimmick tests.**

1. *Can the user already do it with their tools?* Partly. The user can type
   `todo-app.repose` into Desktop today. Nobody has checked that it works,
   nothing tells the user, and a stopped machine breaks it. What we add is
   the proof, the start, and one click. This is integration, not a feature.
2. *Does it depend on intent or self-grading?* No.
3. *Does a vendor ship it?* Yes, and that is the point: we use the vendors'
   own door and build no UI.
4. *Does it move the user off their harness?* No. It is the user's own
   Claude Desktop.

**Build cost.**

- Small for the button and CLI verb: one Svelte change, one `repose code`
  editor entry, Playwright and unit tests.
- Small to medium for the guest checks; this depends on what fails.
- Medium for the docs, because every row needs a real run in the real app,
  on a Mac or Linux client.
- No api, contract or schema change. The start call exists.

**Risks.**

- **Security.** No new port, hostname or listener. The path is the same
  certificate SSH path the editors already use. Desktop's link "can't carry
  a key file, a password, or a command" and asks before it connects.
- **Secrets' three homes.** Desktop's task runs under a sign-in that
  Desktop "renews for the task". Where that lives on the guest is
  *unverified*. If it is a file outside the login share, a Claude
  credential sits on the machine's own disk, and so in snapshots. That would
  be the user's app writing it, not repose copying it (I-278). The
  CLAUDE.md promise is about repose. Step B4 finds the file by name, never
  opening it. A decision entry records the answer before the docs say
  anything about it.
- **Overnight.** Desktop says its tasks may stop after a disconnect. The
  docs must say so plainly, and must not sell Desktop sessions as "keeps
  running with the laptop closed".
- **Disk.** Desktop writes about 1 GB in `/home/dev`, which counts against
  the plan's disk.
- **Drift.** Desktop pins a Claude Code version (2.1.331 today) and
  downloads it. If a machine cannot download it, the session fails.
- **Windows.** The repose SSH config lives in WSL's `~/.ssh`, so Desktop on
  Windows cannot reach a machine. This is the same limit `repose code`
  already states.

### P2. The phone: Remote Control without a terminal: FOLLOW-UP

**What it is.**

- A **Remote Control** switch on the project page. I-1136 named a dashboard
  checkbox "the alternative if the owner later wants the switch reachable
  from a phone".
- The idle measurement I-1134 still owes, so that the switch can default on
  for Claude users.
- A finding on the consent question. Server mode asks
  `Enable Remote Control? (y/n)` on a terminal; a comment on #30447 claims
  "Ours has run without a TTY since 2026-08" (*unverified*). If the consent
  can be given from the app or from Desktop, the last terminal step goes.

**Evidence.**

- Remote Control is the only door from the web and the phone to an
  individual's own machine (section 2.2).
- It has 1,986 titled issues, and its top requests are about this very use
  (#29006 with 165 reactions, #30447 with 54, #96867 with 18).

**Steps.**

- First visit: from 8 steps (5 in a terminal) to about 3: tick the switch,
  answer the consent wherever Claude allows, open the app.
- Later visit: unchanged at 3. The list is Anthropic's.

**Gimmick tests.** It integrates Claude's own feature, and it builds no list
or client. It passes.

**Cost.** Small to medium: an api column, a contract field and a guestd
path, as I-1136 said, plus a half-day measurement on a host with a
subscription login.

**Risks.** The ones I-1132 already records. The claude.ai login becomes a
second way to run commands as `dev`, so the docs recommend Trusted Devices.

### P3. A per-machine HTTPS URL that serves a web agent UI (T3 Code web, CloudCLI, `opencode web`): KILLED

- **The need is already met.** T3 Code's own desktop app reaches the machine
  over SSH and forwards its port itself (P1). The owner's "biggest gap" was
  the route our tutorial chose, not a missing feature.
- **For the phone, T3 runs its own relay.** T3 Connect has the user's tunnel
  hostname "not in the data path". Building ours competes with it, which
  fails gimmick test 3.
- **No new hostname.**
  - A subdomain per project needs a wildcard certificate. That needs DNS-01
    and the Cloudflare token that has blocked the preview certificate since
    2026-09-20 (I-1110). Per-name certificates hit Let's Encrypt's 50 a week
    on a domain the dashboard shares.
  - Paths on one hostname put every tenant's agent UI on one origin, so one
    tenant's page can read another's storage in the same browser.
  - None of T3 Code, opencode or CloudCLI document a path prefix.
- **It is the page-serving door I-1116 kept shut.** A web agent UI is an
  HTML app under repose's name. It is also a shell over HTTPS: CloudCLI has
  no auth at all, and Codex's app-server says non-loopback listeners "allow
  unauthenticated connections by default".
- **It is the one option with a new attack surface.** Every other pathway
  here adds none.
- *Revisit if* the P1 run shows T3 Code desktop cannot use a repose host,
  or if users without a desktop app (a Chromebook, a locked-down work
  laptop) show up in support.

### P4. A dashboard-native thread with the agent as the front end: KILLED as a front end

- It rebuilds claude.ai/code, Remote Control, T3 Code and Happy for one
  harness at a time. That is gimmick test 3 by definition, and the user
  would still use Claude's own app for everything else.
- The "say" redesign (memory say-ux-is-ugly) belongs to the dashboard
  builder. This research says: keep it to what repose alone knows (events,
  states, `repose-ask` questions and their answers), and do not render a
  transcript.

### P5. A mobile-first project page as the phone front end: KILLED as a pathway

- The phone user drives the agent in the Claude app, Happy or T3 mobile.
  repose's page on a phone is status, the `repose-ask` answer and the start
  button.
- Those belong in the dashboard builder's 390-wide pass, not in a program
  of their own.

### P6. Claude self-hosted environments on repose (`claude self-hosted-runner`): KILLED

- Team and Enterprise only. It clones the repo once per task, which throws
  away the persistent checkout that repose sells.
- The docs themselves send always-on users to Remote Control.

### P7. Our own phone app or relay (Happy-shaped): KILLED

- It competes with Happy (25 k stars), T3 Connect and Claude's own app.
  That is gimmick test 3.

### P8. "Fix" Remote Control's session list on our side: KILLED

- repose already names sessions by the project (I-1132 `--name <slug>`,
  I-550).
- The list, its grouping (#111153) and its bugs (#28402, #32982, #34255)
  are Anthropic's.
- A session URL on the dashboard is refused by I-1132's logging rule ("no
  log line, event or sample carries a session URL").

### P9. Cursor My Machines, the Codex phone on a Linux host, VS Code tunnels: KILLED for now

- **Cursor.** I-1138 stands, because the CLI does not fit the base closure.
- **Codex phone.** The phone path for a Linux host is undocumented and
  experimental.
- **VS Code tunnels.** `code tunnel` needs a GitHub or Microsoft account for
  each tunnel and a third relay, while `repose code` already opens VS Code.

### P10. `sshHostCommands` written into the laptop's Claude settings: DEFERRED, not killed

- An entry like `{"id":"repose-todo-app","name":"todo-app (repose)","command":"<abs>/repose code --print-host todo-app"}`
  would put every project in Desktop's environment menu, and starting a
  stopped machine would come with it. That is the one gap P1's saved
  environment leaves.
- It writes into a file the user owns on the laptop, which repose has never
  done.
- Do it after P1 shows that users open Desktop through repose, with an
  opt-in and a decision entry.

---

## 4. Recommendation

**Build P1 first.** It is the only pathway that:

- removes every terminal step from the daily path for the largest front-end
  population (Claude Desktop, then T3 Code, the Codex app and Antigravity);
- adds no attack surface and no home for a secret;
- costs days, not weeks;
- moves nobody off their harness: the user is in Claude's app, running
  Claude's agent, on repose's machine.

**Then P2.** P1 cannot reach the phone, and Desktop's own docs say its
sessions are not for overnight.

### Acceptance script

Run this on a preview, not production. Part A runs on woker-1 against the
fake api. Part B runs on staging with an `e2e-*` project, after the
conductor approves touching staging. Part C runs on a real Mac client: the
owner's Mac, or the AWS Mac lab (memory aws-mac-lab-layout).

**A. The dashboard and the CLI (preview at `http://100.89.156.32:<port>`)**

1. Build `apps/web`. Start the fake api with a running project `todo-app`
   (checkout folder `todo-app`), a stopped project `old-app` and a frozen
   project `cold-app`. Serve the web app bound to `0.0.0.0` and open
   `http://100.89.156.32:<port>/projects/<todo-app id>`.
2. The Connect card shows **Open in Claude**. Its `href` is exactly
   `claude://code/new?ssh_host=todo-app.repose&ssh_folder=%2Fhome%2Fdev%2Ftodo-app`,
   or the unencoded `/` if Desktop requires it (check in C1). It has no `q`,
   no `@`, no token and no project id. A Playwright spec asserts this.
3. On `old-app`, the click sends one `POST /projects/<id>/start` (the fake
   records it). The page shows the start, and `location` becomes the
   `claude://` link only after the project reads `running`. A failed start
   shows the start's own error and no link.
4. On `cold-app`, the click opens the link at once, with no start call.
5. Capture the Connect card at 1440 and 390, light and dark, at 1x
   (CLAUDE.md "Judge visuals at real size").
6. `repose code --editor claude todo-app` against the fake: it prints or
   opens the same URL, and starts `old-app` first as `repose code` does
   today. The CLI docs test (`internal/cli/docs_test.go`) passes with
   `cli.md` updated.

**B. The machine (staging, `e2e-desktop`)**

1. In a `mktemp -d` folder with one commit, run
   `repose run -d -p "say hi"` with the staging CLI. The project is
   `e2e-desktop`.
2. Run the checks that stand in for Desktop's requirements, from the laptop:
   - `ssh -G e2e-desktop.repose | grep -E '^(hostname|certificatefile|user) '`
     shows the gateway, the certificate and `e2e-desktop.<handle>`;
   - `ssh e2e-desktop.repose true` exits 0 with no prompt;
   - `echo ls | sftp -b - e2e-desktop.repose` exits 0;
   - `ssh e2e-desktop.repose 'mkdir -p ~/.claude/remote && cp "$(command -v true)" ~/.claude/remote/t && ~/.claude/remote/t && rm ~/.claude/remote/t && echo exec-ok'`
     prints `exec-ok`.
3. Repeat B2 on a machine on the Ubuntu (no-Nix) base.
4. After C1, with Desktop's session finished, check the login by file test
   only. Never `cat`, never read a value:
   `ssh e2e-desktop.repose 'findmnt -n ~/.claude/.credentials.json; find ~/.claude/remote -maxdepth 3 -iname "*cred*" -printf "%p %s\n"'`.
   Record where Desktop's sign-in lives, and write a decision entry before
   any docs claim.
5. `repose events e2e-desktop` shows the Desktop session's turn as
   `working` and then `finished`, which proves that repose's hooks run for
   Desktop's copy of Claude Code.

**C. The real apps (Mac client)**

1. **Claude Desktop** (at least v7.37095.0):
   - Open **Open in Claude** from the A-preview's real build pointed at
     staging, or paste the link. Confirm the connection. Desktop installs
     its helper.
   - Prompt: `create hello.txt containing hi`. Then
     `ssh e2e-desktop.repose cat todo/hello.txt`, or the checkout path,
     prints `hi`.
   - Quit Desktop. Wait 20 minutes. `repose ls` shows `e2e-desktop` frozen.
   - Reopen the saved environment: the machine wakes and the session is
     there.
2. **Stopped machine.**
   - Run `repose stop e2e-desktop`. Pick the saved environment in Desktop.
     Record the exact error text for the docs.
   - Then click **Open in Claude** on the dashboard. The machine starts and
     Desktop connects.
3. **T3 Code desktop:** add environment, then SSH, then `e2e-desktop.repose`.
   Add `/home/dev/<folder>`. A thread with Claude Code edits a file. No
   `repose open` runs at any point.
4. **Codex app:** in Settings > Connections, check that `e2e-desktop.repose`
   is listed. This is the `Include` question. If it is not listed, record
   it and document the one-line workaround the Codex docs allow. Run one
   task.
5. **Antigravity and Zed:** one connection each, and one agent turn where
   the app has an agent.
6. Each app's row on the "Desktop apps" page states only what C1 to C5
   showed.
7. `repose rm e2e-desktop`.

**Close.**

- The PR body (shape `docs/PR.md`) pastes the A2 Playwright output, the A5
  captures, the B2 to B5 command output, and a screenshot or one line per C
  step.
- The decision entry covers the button, the CLI verb and the B4 finding.
- The docs ship in the same commit (I-242).

## Sources

All were read on 2026-11-24.

- Claude Desktop: https://code.claude.com/docs/en/desktop ; deep links https://code.claude.com/docs/en/deep-links ; Desktop changelog https://claude.com/docs/cowork/claude-desktop-changelog
- Claude Code on the web: https://code.claude.com/docs/en/claude-code-on-the-web ; self-hosted environments https://code.claude.com/docs/en/self-hosted-environments ; Remote Control https://code.claude.com/docs/en/remote-control ; mobile https://code.claude.com/docs/en/mobile ; VS Code https://code.claude.com/docs/en/vs-code
- Codex: https://learn.chatgpt.com/docs/remote-connections , /docs/mobile , /docs/app-server
- T3 Code: https://github.com/pingdotgg/t3code (`docs/user/remote-access.md`, `docs/internals/06_t3-connect.md`, releases)
- Antigravity https://antigravity.google/docs/ssh/ ; Cursor https://cursor.com/docs/cloud-agent/bring-your-own-machine , https://cursor.com/changelog/self-hosted-machines ; Conductor https://conductor.build/docs/cloud/faq ; Happy https://happy.engineering/guides/remote-server ; CloudCLI https://cloudcli.ai/docs/installation/remote-server ; opencode https://opencode.ai/docs/web ; Zed https://zed.dev/docs/remote-development ; VS Code https://code.visualstudio.com/docs/configure/command-line
- Usage: https://x.com/thsottiaux/status/2039901964679688437 ; https://openai.com/index/introducing-the-codex-app/ ; https://newsletter.pragmaticengineer.com/p/ai-tooling-2026 ; https://survey.stackoverflow.co/2025/ ; VS Code Marketplace API ; api.npmjs.org
- Issues: anthropics/claude-code #49790, #28402, #30447, #104393, #96867, #76898, #111153, #91790, #91841, #32982, #34255, #29006, #97729 ; pingdotgg/t3code #1928, #5093
- HN: 47148454, 45647166, 49097326, 47152678, 44879061
- Repo: DECISIONS I-278, I-550, I-1110, I-1116, I-1132, I-1134, I-1136, I-1138, I-1216, I-1354 ; `internal/cli/sshprepare.go`, `internal/cli/cert.go`, `internal/cli/code.go`, `internal/gateway/relay.go`, `nix/guest/base/ssh.nix`, `nix/guest/base/claude-auth.nix`, `nix/overlay/agents/versions.json` ; public docs `agents.md`, `ssh-and-editors.md`, `machine.md`, `lifecycle.md`, `tutorial-t3-code.md`
- Not reached: Reddit (every automated fetch refused), X (one post seen as a search-result title only)
