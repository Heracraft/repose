# Run and attach

`repose run` is the whole product in one command. With no arguments it puts
you inside your project's tmux session. With a prompt it starts an agent in a
new tmux window, types the prompt, and leaves it running whether or not you
stay attached.

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
