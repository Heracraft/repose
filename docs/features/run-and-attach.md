# Run and attach

`repose run` is the whole product in one command. With no arguments it puts
you inside your project's session: tmux, or herdr on a project that chose
it (DECISIONS I-501, I-502; "herdr projects" below). With a prompt it
starts an agent in a new window (a tab on herdr), types the prompt, and
leaves it running whether or not you stay attached. Everything above
"herdr projects" describes tmux.

## What the user sees

First run on a new project (stderr shows a spinner with the elapsed time
on a terminal; this is what stays on screen, DECISIONS I-154):

```
$ repose run
✓ Created todo-app (large)  0.3s
nix › building '/nix/store/…-repose-guest.drv'...
✓ Built the environment  41s
✓ Booted todo-app  6.2s
Connected to todo-app (large)
Synced: 3 modified, 1 untracked (12 new commits)
Credentials: gh, git
Ready in 49s.
dev@todo-app:~/todo-app$
```

Without a terminal on stderr (CI, a pipe) the phases are plain lines,
`Creating todo-app...`, `Building the environment...`, `Booting
todo-app...`, `Connecting to todo-app...`, `Syncing...`. One `repose run`
makes one SSH connection and asks for nothing: the CLI's own key has no
passphrase (I-149).

Run with a prompt:

```
$ repose run "finish the auth flow, run the tests, commit when green"
Connected to todo-app (large)
```

Run with another agent and an explicit size for a new project:

```
$ repose run --agent codex --size xl "port the build to bun"
```

Attach to an existing session later, from the checkout or from anywhere
by name (the project is the argument, DECISIONS I-155; `--project` works
too):

```
$ repose attach
$ repose attach izma
```

Attach to a project that is not running says which state it is in and
what to do, exit 5:

```
$ repose attach age-calculator
age-calculator is in an error state: the environment's agent (guestd) stopped answering; `repose start` restarts it.
```

`repose run izma` is refused with exit 2 when `izma` is one of your
projects: `run`'s argument is the prompt, so the CLI points at `repose run
--project izma` or `repose attach izma` instead of typing the word into
an agent (`--agent` sends it anyway). The prompt is everything after the
flags, so quoting is optional.

Run a prompt while an agent is already running:

```
$ repose run "also update the README"
Another claude window is open; two agents share one working tree. `repose run --worktree` gives the next one its own.
```

The window is `claude-2`, then `claude-3`, and so on: the lowest free
number, with no limit (DECISIONS I-253).

Run a prompt in its own git worktree (I-253):

```
$ repose run --worktree "try the other approach"
Worktree: ~/todo-app-claude-2 on branch repose/claude-2
The worktree starts at the last commit; the uncommitted changes in ~/todo-app are not in it.
```

The second line appears only when the guest's checkout is dirty.
`--worktree` needs a prompt (exit 2 without one) and works for the first
window too (`~/todo-app-claude`, `repose/claude`). A guest checkout with
no `.git` or no commit is refused with exit 2. Each `--worktree` run makes
a new worktree; a later run never reuses one, and nothing removes them
but the user (`git worktree remove ~/todo-app-claude-2 && git branch -D
repose/claude-2`, documented in /docs/run-and-attach).

Running against a stopped project starts it first (the `Starting
todo-app` phase on stderr) before the usual `Connected to todo-app
(large)` line — there is no separate "stopped" message for `run` (only
`attach` and `open` refuse a guest that is not running, exit 5, since
starting one is not their job). A project in `error` is restarted by the
api on the same start (`Restarting todo-app (its agent stopped
answering)`, I-157).

## Behaviour that must hold

Session and windows (see `interfaces/guest-conventions.md`):

- The tmux session is named after the project slug and exists from guest
  boot, with a window `shell` whose working directory is the checkout
  (`/home/dev/<laptop folder>` since I-368, `/home/dev` while the machine
  has none; guest-conventions "The checkout"). `repose run` with no
  prompt attaches to the session's
  current window.
- A prompt opens a window named after the agent (`claude`, `opencode`,
  `codex`, `gemini`, `pi`). If that window already exists, the new one is
  the lowest free `<agent>-N` (`-2`, `-3`, ... with no limit, I-253). With
  `--worktree` the window opens in `~/<checkout>-worktree-<N>`, a git
  worktree on branch `worktree-<N>` (guest-conventions "tmux"); otherwise in the
  checkout. The agent's interactive TUI runs in that window,
  never a headless or print mode, because the point is that the user can
  attach and see the live session with its history.
- The prompt is typed into the TUI only once the TUI is up. The CLI polls
  `tmux display -p '#{pane_current_command}'` and `tmux capture-pane`
  until the pane's process matches the agent and its content has been
  unchanged for 1 second, then `tmux send-keys -l '<prompt>'` followed by
  a separate `Enter`, exactly as 07-cli.md §5.5 step 7 specifies (a single
  `-l` send, not a bracketed paste).
- After starting the agent the CLI attaches to that window unless
  `--no-attach` was given. Detaching (`C-b d`) never stops anything.
- `repose attach` attaches to the project's current window; there is no
  `--window` flag (07-cli.md's command tree has none). If the session does
  not exist, `repose attach` fails the way any other tmux target failure
  does; recreating a lost session is guestd's job at boot
  (`guest-conventions.md`), not something the CLI drives.
- Two `repose run` invocations on the same project from two terminals both
  attach; tmux handles the multi-client case and the smaller terminal
  constrains the size, as tmux always does. That is documented, not hidden.

The laptop's clock (I-198):

- Every `run` and `attach` moves the guest to the laptop's IANA zone when
  it differs: `/etc/repose/env` (read by every login shell) and tmux's
  global and per-session `TZ`, so a window or agent opened afterwards
  runs in the laptop's time, and the project's stored `tz` (`PATCH
  /projects/:id`), so the next boot starts in it too. Shells already
  running keep the zone they started with. When the zone changed, `run`
  prints `Time zone set to Asia/Tokyo.` on stderr; `attach` shows the same
  line inside tmux.
- Nothing here waits: on `run` it rides the SSH that copies the tool
  logins, on `attach` it runs beside tmux (the session helper, see
  below), and the api update runs beside the connect.
- A laptop whose zone cannot be named (no `TZ`, no zoneinfo link) leaves
  the guest's zone alone.

The session helper: `run` and `attach` start a small background process
(`repose __session`, detached, its options in the environment rather than
on its command line) just before the CLI becomes `ssh`. It does what must
not delay the attach and reports only through `tmux display-message`,
never over the pane, and it ends when the SSH session it was started
beside ends. Windows has no helper. While it runs it also keeps the
guest's listening ports forwarded to the laptop (ports-and-previews.md,
I-199), shown in the session's status bar.

Agent picker:

- `--agent` accepts exactly `claude`, `opencode`, `codex`, `gemini`, `pi`.
  Anything else exits 2 listing the five. The default is the project's
  `agent_default`, set at creation from `default_agent` in the laptop's
  `~/.config/repose/config.toml` (`claude` unless set; I-241). An existing
  project keeps its own; the API accepts `agent_default` on `PATCH
  /projects/:id`, but no CLI command or dashboard control changes it.
- The wrapper for the chosen agent installs its hooks (see agents.md) before
  exec. A prompt for an agent whose login is missing is handled as
  agents.md describes: the TUI's own login prompt appears in the window and
  the CLI tells the user to complete it.

Sequence and idempotency (from DESIGN §10):

1. Resolve or create the project (projects.md).
2. Ensure the guest is running; stream a pending build; start a stopped
   guest. An op that ends in `error` prints it and exits 1 (a build's own
   `eval_failed`/`build_failed` exits 10 instead, per the failure table
   below).
3. Get or refresh the SSH certificate; write the SSH config block and
   check the alias resolves (I-151).
4. Sync credential files (secrets.md), then the checkout
   (sync-at-launch.md), the checkout only into a guest with no commit
   yet (DECISIONS I-367). `--no-sync` skips the checkout only; the
   credential files still go (DECISIONS I-366).
5. Start the agent window if a prompt was given, then attach.

A connection kept from an earlier command (the ControlPersist master) is
reused only after it runs `true` within 2 seconds; one that does not (its
TCP connection died while the laptop slept or changed networks) is stopped
and the command connects afresh (DECISIONS I-491). While attached, the CLI
refreshes its access token 10 minutes before it expires, so the next
command after a long attach does not wait on Logto (I-491).

Running `repose run` twice in a row attaches twice and changes nothing else.
A test asserts that the second run makes no `POST` to the API except the
certificate refresh, if due.

Timing that must hold on a healthy host:

- A stopped guest starts and is attachable in under 10 seconds.
- A new project with an empty fragment is attachable in under 60 seconds,
  because the base closure is already in the host store.

Failure output:

- Not logged in: exit 3, `Not logged in. Run \`repose login\`.`
- No plan, or the plan's memory or disk is used up: exit 7 and the api's
  sentence, e.g. `Choose a plan at https://repose.herakraft.co/billing
  first.` (the reasons are api.md's `payment_required` table, I-289)
- Host capacity exhausted: exit 8, `No capacity right now; try again in a
  few minutes. (We have been alerted.)` The API also raises a capacity
  alert.
- A first project while the fleet is near full: exit 8, `repose is at
  capacity. You're number N on the waitlist; we'll email <address> when
  there's room.` (projects.md, "Limits"; DECISIONS I-269).
- Build failed: exit 10, the Nix error verbatim, the fragment line if known,
  and `edit with \`repose config edit\``.
- SSH does not answer within 60s of the API reporting `running`: exit 1,
  `Guest is running but SSH did not answer in 60s. \`repose logs --kind
  console\` may show why.`, followed by ssh's last error line. A gateway
  refusal (`Permission denied`, a revoked certificate) during that window
  re-issues the certificate once and keeps waiting (07-cli.md §6,
  DECISIONS I-149).
- Any failed step in the guest: exit 1, `Could not <step>: <why> (<ssh's
  last line>).`, never the raw remote command (I-153).

## Paste an image (I-252)

Claude Code reads a pasted image from the clipboard of the machine it
runs on, so `Ctrl-V` in the guest never sees the laptop's screenshot.
`repose paste [PROJECT] [--window NAME] [--print]` carries it across:

- The CLI reads a PNG from the laptop's clipboard: `pngpaste -`, else
  `osascript` writing `«class PNGf»` to a temp file, on macOS;
  `wl-paste --type image/png` when `WAYLAND_DISPLAY` is set, else
  `xclip -selection clipboard -t image/png -o` when `DISPLAY` is, on
  Linux (the offered types are listed first, so "no image" and "tool
  failed" differ). Windows is refused; WSL is Linux. No image, bytes that
  are not a PNG, over 20 MB, or no tool: exit 1 before any api call,
  naming the tool to install.
- One ssh command over the project's multiplexed connection (as `cp`)
  writes stdin to `/tmp/repose-paste/<UTC yyyymmdd-hhmmss-ms>.png` (umask
  077: directory 0700, file 0600, owned by `dev`), refusing a directory
  that is a symlink or not `dev`'s, and first deletes pastes over a day
  old and all but the newest 50.
- In the same command, the path goes into the target pane with `tmux
  set-buffer` and `paste-buffer -p`: a bracketed paste when the program
  asked for one, which is how a terminal delivers a dropped file and what
  Claude Code attaches. No Enter. The target is the session's current
  window's active pane, or `=<slug>:<NAME>` with `--window`. A window
  that does not exist leaves the file saved and exits 1 with its path.
- `--print` saves and prints the guest path only.
- Nothing is logged; the path and the image stay between the laptop and
  the guest.

`repose paste` stays for scripts, other windows and a disabled proxy;
the kitty and WezTerm key bindings that ran it left /docs/run-and-attach
when Ctrl+V did the same through the input proxy (I-280).

## A dropped connection attaches again (I-469)

On the input-proxy path (macOS and Linux with a terminal), an attach whose
ssh ends with 255 does not end the command:

```
Connection to ssh.repose.herakraft.co closed by remote host.

repose: lost the connection to todo-app. Reconnecting; Ctrl-C stops.
```

- The CLI probes with `ssh <alias> true` every second, for up to 2
  minutes, and attaches again to the session (`tmux attach -t <slug>`,
  not the agent window the first attach named), so tmux redraws the
  screen the user left. Keys typed while it waits are dropped; Ctrl-C or
  Ctrl-D stops it.
- A certificate refusal gets a new certificate (a relay ends when its
  certificate expires, I-436); a refusal after a renewal that worked
  ends the wait. A stopped, destroyed, errored or unknown
  project ends the wait at once with the gateway's line.
- An attach that drops again within 5 seconds of a reattach ends the
  command: `the connection to todo-app dropped again at once; giving up.`
- Past 2 minutes: ``could not reach todo-app for 2 minutes. `repose
  attach todo-app` attaches again once it answers.``, exit 255.
- Where the CLI has become ssh (Windows, `REPOSE_INPUT_PROXY=0`, no
  terminal) the command ends with 255 as before.

## Drop a file or paste an image while attached (I-280)

On macOS and Linux, `run` and `attach` run `ssh -t ... tmux attach` on a
pty of the CLI's own (creack/pty; the laptop terminal in raw mode;
SIGWINCH copied to the pty; SIGHUP, SIGTERM, SIGINT, SIGQUIT handed to
ssh; the exit status is ssh's, 128+N for a signal). Input goes through
byte for byte, a lone ESC or a cut escape sequence held at most 30 ms,
with two exceptions:

- A bracketed paste (the guest's tmux 3.7 turns bracketed paste on in the
  laptop terminal whatever the pane runs) whose content is only absolute
  paths of existing regular files on the laptop: backslash-escaped,
  single- or double-quoted, `file://` URIs (empty host or `localhost`),
  separated by spaces or newlines, or one unquoted path with spaces.
  Paths under system directories (`/etc`, `/usr`, `/nix`, ...), paths
  with a hidden component (`~/.ssh/id_ed25519`, `.env`), files named
  like a private key or key store (`id_ed25519`, `*.pem`, `*.p12`, ...),
  files whose first 4 KiB hold a PEM, OpenSSH or PGP private key or start
  an OpenPGP secret-key packet (`.gpg`, `.pgp`), and pastes over 64 KiB
  are text. `.key` is judged by content only (Keynote uses it). The checks apply to the pasted path
  and again to the file it resolves to after every symlink, which is
  what is read (I-468). A read with no paste markers that is only such
  paths is a drop too (a terminal not asked for bracketed paste).
  Each file is copied over the project's multiplexed ssh with
  `repose paste`'s script (same directory, modes, symlink and owner
  refusal, pruning) to `/tmp/repose-paste/<ts>-<n>-<safe name>`, and the
  proxy types a bracketed paste of the guest paths, backslash-escaped and
  space-separated, which Claude Code attaches (checked with Claude Code
  2.1.280). A file inside the checkout the CLI was run from (its git
  toplevel, when that checkout is the project's) whose guest copy under
  `$HOME/<slug>` has the same size is not copied: its guest path is
  typed. Over 20 files or a file over 20 MB: nothing copied, the original
  paste goes through, and `tmux display-message` says why. A copy is
  named there too ("copied report.pdf to the machine"), so no file
  leaves the laptop unseen (I-468).
- Ctrl+V (0x16, CSI u `118;5u`, modifyOtherKeys `27;5;118~`): the
  clipboard is read as `repose paste` reads it, for up to 2 s. A PNG is
  copied to `/tmp/repose-paste/<ts>.png` and its path typed as above;
  anything else sends the key on. A missing clipboard tool on a desktop is
  said once per session on the tmux status line.

Input typed during a copy waits and follows it in order. A copy that
takes over 0.5 s says so on the status line; nothing is ever written
over the pane. `REPOSE_INPUT_PROXY=0`, Windows, or a stdin or stdout that
is not a terminal: the CLI execs ssh as before. Nothing is logged.

## Choosing the multiplexer (I-502)

```
$ repose run --help
...
      --multiplexer string   tmux|herdr: what runs this machine's terminals, from its next start (default: config.toml's default_multiplexer, else tmux)
```

```toml
# ~/.config/repose/config.toml
default_agent = "claude"
default_multiplexer = "herdr"
```

A new project, from a laptop terminal inside herdr:

```
$ cd ~/src/todo-app && repose run
✓ Created todo-app (large, herdr)  0.3s
...
Ready in 38s. todo-app is in herdr's sidebar.
```

Switching a running machine:

```
$ repose run --multiplexer herdr
todo-app uses herdr from its next start; tmux keeps running until then.
```

- `--multiplexer` is a flag of `run` and `sync`. Values `tmux` and
  `herdr`; anything else exits 2 naming both.
- A new project gets, in order: `--multiplexer`; `default_multiplexer`
  in `config.toml`; `herdr` when `HERDR_ENV=1` is in the CLI's
  environment and the project is not temporary; `tmux`. The create line
  names herdr (`Created todo-app (large, herdr)`) and says nothing for
  tmux.
- On an existing project `--multiplexer` with another value than the
  project's sends `PATCH /projects/:id {multiplexer}` before anything
  else, and the value stays with the project. A stopped project then
  starts on it, and nothing more is printed. A running one prints the
  line above (with the values swapped for a switch to tmux) and attaches
  to what runs now. The same value as the project's prints nothing and
  sends nothing.
- The api refuses herdr on a base without it (`409`,
  `base_update_needed`, api.md "The base gate"). For an explicit flag or
  config key the CLI prints the api's message and exits 1. For the
  `HERDR_ENV` pick it creates the project with tmux and says nothing.
- `default_multiplexer` never changes an existing project.
- The dashboard shows `herdr` beside the size on a herdr project's page,
  read only.

## herdr projects (I-509, I-510)

Which path a command takes is decided by what runs: the CLI asks the
guest `systemctl --user -q is-active repose-herdr-server` over the ssh
connection it holds (a few milliseconds; the fast path asks too). A
machine switched while running answers by what it runs until its next
start. A base with no such unit answers tmux. The CLI asks only when a
prompt or an attach needs the answer; a `sync` (or `run --no-attach`
with no prompt) asks nothing and takes the project's stored
`multiplexer` for the sidebar (I-542).

| Command | herdr project |
|---|---|
| `repose run`, `repose attach` | the attach rule below |
| `repose run "prompt"` | a tab in the checkout's workspace, the agent, the prompt, then the attach rule |
| `repose run --worktree "prompt"` | the git worktree as on tmux, `herdr worktree open --path DIR` so herdr groups it under the repository, then the agent in it |
| `repose attach P:CHECKOUT` (I-480) | focuses that checkout's workspace, creating it in `/home/dev/<name>` when missing, then the attach rule |
| `repose ps` | herdr's agents (`herdr agent list` and `herdr workspace list`): `WORKSPACE  AGENT  NAME  STATE`; `--json` gives `{workspace, agent, name, state, focused}`; `-q` the names. No cwd, no title |
| `repose paste` | `herdr pane send-text <pane> <path>` into the focused pane (from `herdr pane list`), or into agent NAME's pane with `--window NAME`; no Enter |
| `repose status` | `herdr` after the size in the header; the sessions line drops `tmux clients` |
| `repose ls` AGENTS, dashboard | herdr's agents and states, gone when the pane closes |
| `repose stop` | as on tmux, the `Interrupted ...` line included (I-500); herdr resumes agents with an integration at the next start |
| `repose rm` | also removes the laptop herdr's entry for the machine |
| temporary machine | destroyed when the attach returns and herdr reports no panes; otherwise at its expiry |

**The attach rule**, first match wins:

1. `HERDR_ENV=1` (a laptop herdr pane), the project is not temporary, and
   the machine is in the laptop herdr's sidebar (the reconcile below has
   just made sure): the CLI prints `<slug> is in herdr's sidebar.` and
   stays in the foreground as the session helper (port forwards, the
   browser bridge, the carry) until Ctrl-C, which exits 0. No client
   opens inside the pane.
2. `herdr` 0.9.0 or newer on the laptop PATH: `herdr --remote
   <slug>.repose --session default` runs as a child of the CLI, never
   through exec, so the session helper lives as long as it does. herdr's
   bridge reconnects by itself, so the input proxy and the reattacher
   (I-280, I-469) stay off on this path. The exit status is herdr's.
3. Otherwise `ssh -t <slug>.repose herdr` under the input proxy and the
   reattacher, as a tmux attach runs.

**A prompt on herdr** is one ssh script:

1. Claude's folder trust, as on tmux (I-486).
2. The workspace for the folder (checkout, worktree or other checkout),
   created when missing.
3. `herdr agent list` (it prints herdr's JSON answer) picks the name, `claude` or the lowest free
   `claude-N`, and counts the same agent in that workspace. With one
   there, `run` prints `Another claude tab is open; two agents share one
   working tree. \`repose run --worktree\` gives the next one its own.`
4. `herdr tab create --workspace W --cwd DIR --label NAME`.
5. `herdr agent start NAME --kind AGENT --pane P --timeout 300000`; on a
   timeout, `herdr agent wait NAME --until idle --until blocked` again
   until 30 minutes have passed (a first dev-shell build).
6. A pane that settles on `blocked` gets no prompt: `run` names the tab
   and attaches, or exits 1 with `--no-attach` (I-486).
7. `herdr agent prompt NAME "<prompt>" --wait --until working --until
   blocked --timeout 10000`.

**The laptop's sidebar** (I-510). With `herdr` 0.9.0 or newer on the
laptop PATH and repose's `Include` in `~/.ssh/config`, `run`, `attach`,
`sync`, `rm` and every certificate refresh reconcile herdr's machine list: a
running herdr project with no entry gets `herdr machine add
<slug>.repose --label <slug> --remote-session default` in the
background (it never delays `Ready in`; a certificate refresh, which
`ssh-prepare` may run in a process that ends at once, only removes,
I-542); an entry whose target is
`<slug>.repose` and whose slug is no live project goes (a destroy from
the dashboard, another laptop or an expiry); anything else, including an
entry you disabled, stays. A stopped machine keeps its entry, and herdr
shows the gateway's "stopped" line until you start it. Temporary
machines are never added. Nothing is printed unless an add fails, once:
`Could not add todo-app to herdr's sidebar: <herdr's last line>`.

**Messages.** What the session helper and the CLI show inside the
session (`Time zone set to ...`, forwards, carry notices) goes through
`herdr notification show repose --body "<text>"` on herdr, where tmux
uses `display-message`; a script that does not know the multiplexer
uses tmux when its server answers, else herdr when its socket exists.
herdr shows them only when the guest's herdr config sets `[ui.toast]
delivery = "herdr"` (its default is off, I-542). In the sidebar path
(rule 1) the helper runs in the CLI's own pane, and its messages print
there. With no herdr client attached nobody sees them, as with tmux.

**Old CLIs.** A CLI from before I-509 runs `tmux attach` on a herdr
machine and gets tmux's `no server running`. The release notes and
/docs/tutorial-herdr name the CLI version herdr needs; the api cannot tell an old
CLI apart.

## ps, exec and ssh (I-274, I-275)

- `repose ps [PROJECT]` is one ssh over the project's connection:
  `date +%s` and `tmux list-windows` with index, name,
  `pane_current_command` (the process name, never its arguments),
  `window_activity` and `window_active`. Idle time is the guest's clock
  minus the activity time, so a skewed laptop clock does not matter.
  `-q` prints names, `--json` the records. Nothing is logged.
- `repose exec [PROJECT] [--] CMD...` runs, over ssh, `cd` into the
  checkout (the home directory when the machine has none, I-368), `/etc/profile.d/repose.sh`, then
  `/etc/repose/devshell.sh` (the agent wrappers' loader, I-259) or, on an
  older base, `direnv export bash`, then `exec` of the arguments, each
  single-quoted. No stdin without `-i`, a remote tty only with `-t`
  (`ssh -tt`). Once the command runs, repose exits with its status; repose's
  own codes only come before, with a message. The `--` is optional
  (I-411): flags stop at the first word, so the command's own flags pass
  through; with no `--` and no `--project`, a first word that is one of
  the account's slugs is PROJECT, and that word alone is refused (exit 2).
  A `--` after one word and `-i`/`-t` only is the old PROJECT separator;
  any other `--` is the command's.
- `repose ssh [PROJECT]` replaces the CLI with `ssh -t <slug>.repose` running
  a login shell in the checkout, outside tmux.
- All three need a running project (exit 5 otherwise) and ensure the
  certificate and config the way `attach` does.

## Depends on

Workstreams 07 (cli), 05 (projects, certs, ops), 04 (guestd SetupProject,
tmux control, send-keys idle wait), 02 (guest base, wrappers), 06 (gateway),
12 (build streaming).

## Deferred

Worktrees by default (DECISIONS R4-10 chose warn-and-proceed; I-253 made
`--worktree` opt-in). A command that lists or removes worktrees. Queueing prompts for
when the current agent finishes. Web terminal in the dashboard (R4-18).
