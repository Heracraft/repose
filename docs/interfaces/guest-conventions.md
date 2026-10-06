# Guest conventions

What every guest guarantees, so the CLI, hooks and guestd can rely on it.
Produced by `nix/guest/base/` (workstream 02); every path, name and variable
here exists in that module under exactly this name.

## The checkout

Every reader of the checkout's location applies one rule (DECISIONS
I-368): the CLI's `checkoutVar`, guestd's `findCheckout`, and the guest
command `repose-checkout` (prints the absolute path), which the tmux
session unit and `repose-guest-profile` use.

1. `/home/dev/.repose/checkout` names a directory of `/home/dev` that
   exists (and, for guestd, resolves inside the home): that directory.
2. Else `/home/dev/<slug>` exists: that directory (a machine set up
   before I-368).
3. Else there is no checkout, and the session, agents, `repose exec`,
   `repose ssh` and relative `repose cp` paths work in `/home/dev`.

Only the CLI's sync makes a checkout, in its probe: when the rule finds
none, it makes `/home/dev/<name>`, where `<name>` is the laptop
checkout's folder made safe as a project name is (`job search` is
`job-search`), or the slug when that is empty or `/home/dev/<name>` is a
non-empty directory or not a directory; it writes the name to
`/home/dev/.repose/checkout` and `git init`s it. guestd never makes one:
`SetupProject` git-inits and points `origin` at the remote only in a
checkout the rule finds. The sync that made the checkout replaces the
session's `shell` window (`tmux respawn-pane -k -c <checkout>`) when it
is an idle shell in `/home/dev`, and every attach passes `-c <checkout>`
to `tmux attach`, so new windows open there.

### Other checkouts

A machine can hold other checkouts beside the checkout (DECISIONS
I-480): folders `repose run --on` added, each a directory of
`/home/dev` listed by name, one per line, in
`/home/dev/.repose/checkouts`. The checkout itself is never listed, and
"the checkout" everywhere else in this document (guestd, the tmux
session's default directory, `repose-checkout`, the login profile) still
means the one the rule above finds. Only the CLI makes another checkout:
it picks the laptop folder's safe name, else `<name>-2`, `<name>-3`, ...,
skipping the checkout, its `-worktree-N` directories, names already
listed and non-empty directories, makes the directory and appends the
name. The CLI's scripts for such a folder use `/home/dev/<name>` instead
of the rule. Agent windows there are `<name>/<agent>` and
`<name>/<agent>-N` (a `.` in the name becomes `-`), and a shell window
the attach opens is `<name>`. guestd reads none of this.

## Filesystem

| Path | What |
|---|---|
| `/home/dev/<checkout>` | the project checkout, found by the rule in "The checkout" below; the tmux session's default directory. Made by the CLI's first sync, named after the laptop folder (DECISIONS I-368). On a machine set up before I-368 it is `/home/dev/<slug>`, which guestd's `SetupProject` made at every start; on such a volume restored under another slug (a fork, a restore `--as-new`) with no `/home/dev/.repose/checkout`, `SetupProject` makes `/home/dev/<slug>` a relative symlink to the checkout of the slug `project.json` named before (DECISIONS I-255) |
| `/home/dev/.repose/checkout` | one line, the checkout's directory name under `/home/dev` (`[A-Za-z0-9._-]`, no leading dot, no `/`), written by the CLI's first sync (I-368); absent on a machine with no checkout and on one set up before I-368 |
| `/home/dev/<checkout>-worktree-<N>` | a git worktree of the checkout on branch `worktree-<N>`, made by `repose run --worktree` (DECISIONS I-253, I-342); see "tmux". Worktrees made before I-342 are `/home/dev/<slug>-<window>` on `repose/<window>` and stay as they are |
| `/home/dev/.repose/project.json` | `{project_id, slug, name, remote_url, user_handle, class, tz}` written by guestd at SetupProject |
| `/etc/repose/env` | `TZ=` and `REPOSE_PROJECT=` lines written by guestd at SetupProject, sourced by every shell; the CLI replaces the `TZ=` line (through `sudo`, root 0644, by rename) on `run` and `attach` when the laptop's zone differs (I-198) |
| `/etc/repose/base-version` | the platform base version string (same as `nixos-version`'s label) |
| `/etc/repose/claude-settings.json` | the platform hooks (`Notification`, `Stop` → `repose-hook`) the Claude wrapper merges into `~/.claude/settings.json`, and the default `permissions.defaultMode: "bypassPermissions"` with `skipDangerousModePermissionPrompt: true` it adds where the user's file has no `defaultMode` (I-250), and `tui: "fullscreen"` it adds where the user's file has no `tui` (I-425) |
| `/etc/repose/mcp.json` | the platform MCP servers (`playwright --cdp-endpoint http://127.0.0.1:9224`, `chrome-devtools --browserUrl http://127.0.0.1:9224`, DECISIONS I-246) the Claude wrapper merges into `~/.claude.json` `mcpServers`, and `repose_retired`: the entries earlier bases registered (both `--headless`), which the merge replaces when a user's entry is exactly one of them |
| `/etc/repose/agent-guide.md` | the machine guide agents read, rendered from `nix/guest/base/agent-guide.md` (DECISIONS I-243); the same text is `/etc/claude-code/CLAUDE.md` (Claude Code's managed memory), `developer_instructions` in `/etc/codex/config.toml`, the file named by `instructions` in `/etc/opencode/opencode.json`, and `GEMINI.md` in `/etc/repose/gemini-extension/`; `/etc/repose/pi-extension.js` reads it at each pi run |
| `/etc/repose/agents.json` | `{<agent>: {binary, version, hook}}` for every shipped agent, for `repose status --verbose` |
| `/etc/profile.d/repose.sh` | sources `/etc/repose/env`, then the named secrets through `/etc/repose/bash-env.sh` (or `/run/repose/secrets.env` when no `secrets.refresh` exists yet), exports `DISPLAY=:99` while the X display `:99` is up (the agents' browser or the desktop viewer runs), prepends the user bin dirs to `PATH` |
| `/etc/ssh/principals/dev` | the accepted certificate principals (the project id), written by guestd `SetPrincipals` |
| `/etc/ssh/ssh_host_ed25519_key`, `ssh_host_ed25519_key-cert.pub`, `user_ca.pub` | symlinks to the reserved secrets below (DECISIONS I-35) |
| `/run/repose/` | tmpfs (part of `/run`), 0755 root |
| `/run/repose/ssh_host_ed25519_key`, `/run/repose/ssh_host_ed25519_key-cert.pub`, `/run/repose/user_ca.pub` | the reserved secrets, root 0600 / 0644, written by guestd `WriteSecrets`; a throwaway key is generated at first sshd start when none was delivered yet |
| `/run/repose/secrets/<NAME>` | named secret values, tmpfs, 0400 dev, directory 0700 dev |
| `/run/repose/secrets.env` | first line `export REPOSE_ENV_GEN=<16 hex>` (changes when the set of exported secrets does, and only then), then one `export NAME='...'` line per current secret; 0400 dev (DECISIONS I-475) |
| `/run/repose/secrets.refresh` | first line `# repose-env-gen <16 hex>`, then one POSIX sh line per name guestd exports or exported before: `case ${NAME+s$NAME} in ''\|s'earlier1'\|...) export NAME='current' ;; esac` for a current secret, `case ${NAME+s$NAME} in s'earlier1'\|...) unset NAME ;; esac` for a removed one, and last `export REPOSE_ENV_GEN=<gen>`; 0400 dev; holds up to 16 earlier values per name, a removed name's included, until the guest restarts (I-475, I-476) |
| `/run/repose/secrets.state` | JSON `{"gen", "current": {NAME: base64}, "earlier": {NAME: [base64, ...]}}`: the current generation and values, and per name the values exported before, distinct, oldest first, at most 16; root 0600; guestd reads it back after a restart (I-475) |
| `/etc/repose/bash-env.sh` | `BASH_ENV` of every bash, and sourced by `/etc/profile.d/repose.sh`: sources `/run/repose/secrets.refresh` when its first line names another generation than the process's `REPOSE_ENV_GEN`; POSIX sh, silent, runs no other program, keeps `$?`, `$_`, the positional parameters and the shell options, leaves the unexported `__repose_bash_env_u`, a no-op when the file is missing or unreadable (I-475) |
| `/run/repose/hooks.sock` | hook ingest, HTTP over unix, 0660 root:dev, created by guestd |
| `/run/repose/guestd.sock` | dev-only stand-in for vsock (absent in real guests) |
| `/run/repose/paths-registered` | written by guestd after the first `RegisterPaths`; `repose-paths.service` waits for it (up to 180 s) and `home-manager-dev.service` runs after that (DECISIONS I-67) |
| `/var/lib/repose/paths-loaded` | sha256 of the last registration `nix-store --load-db` took, on the volume; a `RegisterPaths` with the same bytes and `/nix/var/nix/db/db.sqlite` present only writes the stamp above (DECISIONS I-225) |
| `/run/repose/desktop/vnc-password` | the viewer's VNC password, generated once per boot the first time the display starts, 0600 dev (DECISIONS I-33, I-292); `vnc-passwd` beside it is TigerVNC's obfuscated form Xvnc reads |
| `/run/repose/desktop/web` | the viewer's web root, rebuilt at every viewer start: links to the shipped page (`index.html`, `viewer.js`, `viewer.css`, `healthz`, noVNC's `core/` and `vendor/`) and `project.json` (`{name, idle_minutes}`) for the page's bar (I-292) |
| `/run/repose/desktop/last-client` | mtime of the last observed desktop client; the idle stop reads it |
| `/run/repose/desktop/last-cdp` | mtime of the last observed DevTools client of the agents' browser (I-246); the idle stop reads it |
| `/home/dev/.local/share/repose/browser` | the agents' browser's Chromium profile (I-246) |
| `/nix/.ro-store` | read-only virtio-fs mount of the host store (tag `ro-store`) |
| `/nix/.rw-store` | the guest's writable store overlay (upper dir `store/`, work dir `work/`), on the thin volume |
| `/nix/store` | overlay of the two: what `nix profile install` in the guest writes lands in `/nix/.rw-store` |
| `/home/dev/.local/state/nix/profiles/profile` | dev's nix profile; `repose-pin-profile` copies its closure into the overlay whenever it changes |
| `/var/log/repose/console.log` | not used; console goes to the serial device and hostd captures it |

## tmux

- Server socket is the default for user `dev`.
- Session name = project slug, created by the user unit
  `repose-tmux-session.service` (started by a path unit once
  `/home/dev/.repose/project.json` exists, and by guestd at SetupProject)
  with window `shell` in the checkout (`repose-checkout`; `/home/dev`
  when there is none, I-368). Running it again is a no-op.
- Agent windows are named after the agent: `claude`, `opencode`, `codex`,
  `gemini`, `pi`. Further instances get the lowest free `claude-N`, N >= 2,
  with no upper limit (DECISIONS I-253); anything reading window names
  accepts any number of digits (guestd's `sample.AgentOf` always did).
  A window with any other name counts as an agent window while an agent's
  program is its foreground process (`sample.AgentByCommand`, I-421:
  `claude` typed in the `shell` window), except gemini, whose process is
  `node`; hooks from it carry that window's name.
- `repose run --worktree "prompt"` (I-253, I-342) first runs `git -C
  /home/dev/<checkout> worktree add -b worktree-<N>
  /home/dev/<checkout>-worktree-<N> <HEAD>`, copies the checkout's
  gitignored `.env` and `.env.*` files into it (I-343), and opens the
  agent's window there (`-c /home/dev/<checkout>-worktree-<N>`). N is the
  lowest number, from 1, whose `/home/dev/<checkout>-worktree-<N>` path
  and `worktree-<N>` branch are both
  free, so a worktree is never reused. The window is named as any other
  (`<agent>` or `<agent>-N`), apart from the worktree's number. The branch
  has no `repose/` prefix: the laptop's `git fetch repose` files it as
  `repose/worktree-<N>`. Worktrees live beside the checkout, never in
  it, so the sync's status, stash, fingerprint and tar never see them;
  nothing removes them but the user (`git worktree remove`).
- `repose attach` = `tmux attach -t <slug> -c <checkout>`; `repose run
  "prompt"` = `tmux new-window -t <slug> -n <agent> -c <checkout> '<agent> ...'`
  then `tmux send-keys -t <slug>:<agent> '<prompt>' Enter` after the TUI is
  up (guestd waits for the pane to be idle 1 s).
- Claude Code's folder trust (DECISIONS I-486): in the same SSH command,
  before `tmux new-window` starts `claude` in a folder (the checkout, a
  worktree, another checkout), the CLI sets
  `projects["<folder, symlinks resolved>"].hasTrustDialogAccepted` to
  `true` in `~/.claude.json` when it is not already `true`, keeping every
  other key, writing atomically with mode 0600, and leaving a file that
  is not valid JSON alone. It needs `jq` on the guest's PATH and is best
  effort; the window starts either way. A pane that settles on Claude
  Code's trust dialog anyway gets no prompt: `run` says so and attaches
  (with `--no-attach`, exits 1). Nothing else writes that key, and the
  laptop's `~/.claude.json` is never carried.
- `/etc/tmux.conf`: `set -g set-clipboard on`, `set -g mouse off` (DECISIONS I-364;
  `~/.tmux.conf` may turn it on), `set -g
  history-limit 50000`, `set -g default-terminal tmux-256color`, `set -ga
  terminal-overrides ",*:Tc"`, `set -s escape-time 10`, `set -g
  focus-events on`, `set -g update-environment "DISPLAY SSH_AUTH_SOCK
  SSH_CONNECTION LANG COLORTERM"` (no `TZ`: an attach from a terminal
  without one would clear the session's zone), and since DECISIONS I-264
  `set -g extended-keys on`, `set -g extended-keys-format csi-u`, `set -as
  terminal-features ",*:extkeys"`, `set -as terminal-features
  ",*:hyperlinks"`, `set -g allow-passthrough on` (tmux 3.7: tmux asks
  every client terminal for modifyOtherKeys, `\E[>4;2m`, and a program
  in a pane that asks for extended keys gets Shift+Enter as `\E[13;2u`;
  OSC 8 links reach the client; `DCS tmux;` passthrough from a visible
  pane). What types into a pane (`send-keys`, guestd's prompt delivery)
  is unaffected: text and an unmodified Enter (`\r`) are the same bytes
  in every mode.
- `TZ` in tmux: the global environment's `TZ` is the project's zone, set
  by `repose-tmux-session` from `/etc/repose/env` when it creates the
  session, and set again (global and every session) by the CLI on each
  `run` and `attach` from a laptop in another zone (I-198), so every new
  window and agent starts in the laptop's zone. Shells already running
  keep theirs. The CLI's attach also sends `TZ` on the SSH session
  (`SendEnv`), for bases older than this rule whose `update-environment`
  still lists it.

## Agent wrappers

Each agent binary is wrapped (`nix/overlay/agents/wrap.nix`) to:

1. Run `repose-agent-setup <agent>`, which makes the agent's hook config
   point at `repose-hook` without touching anything the user configured:
   - `claude`: `~/.claude/settings.json` gains the entries from
     `/etc/repose/claude-settings.json` under `hooks.Notification` and
     `hooks.Stop` unless an entry whose command contains `repose-hook`
     already exists under that event, and its `permissions.defaultMode`
     and `skipDangerousModePermissionPrompt` only when the user's file has
     no `permissions.defaultMode` (I-250), and its `tui` only when the
     user's file has no `tui` (I-425); `~/.claude.json` `mcpServers` gains
     the servers from `/etc/repose/mcp.json`, user entries winning on a
     name clash.
   - `codex`: `~/.codex/config.toml` gains `notify = ["repose-hook"]`
     unless a `notify` key exists.
   - `opencode`: `~/.config/opencode/plugins/repose.js` is installed if
     absent, and replaced only while its sha256 is one an earlier base
     installed (I-481); the `repose-agent-hooks` user unit also runs this
     at login, for an OpenCode 2 started outside the wrapper.
   - `gemini`, `pi`: no hook (guestd's pane-idle heuristic reports for
     them); the machine guide is linked in as an extension (I-243):
     `~/.gemini/extensions/repose-machine-guide` →
     `/etc/repose/gemini-extension`, and
     `${PI_CODING_AGENT_DIR:-~/.pi/agent}/extensions/repose-machine-guide.js`
     → `/etc/repose/pi-extension.js`. A stale link of that name is
     repointed; anything else at the path is left alone.
2. Set `TERM=tmux-256color` (when inside tmux), `COLORTERM=truecolor`, and
   `REPOSE_HOOK_AGENT=<binary>` so `repose-hook` knows who called it.
3. Load the checkout's dev environment into its own process
   (`nix/overlay/agents/devshell.sh`, DECISIONS I-259), from the working
   directory up: the `.envrc` direnv finds (running `direnv allow` on it
   when it was never allowed on this guest, leaving it out when denied),
   else a `flake.nix` that mentions `devShell` below `$HOME`, loaded
   through a generated `~/.cache/repose/devshell/<hash>/.envrc` holding
   `use flake <dir>` (plus `--reference-lock-file` and `--output-lock-file`
   naming `flake.lock` in that directory when `<dir>` has no `flake.lock`,
   so Nix never writes one into the checkout, I-483), else nothing. A failed load prints a `repose:` line
   and the agent starts without it. While it loads inside tmux, the pane
   option `@repose-devshell` is `loading`; `repose run` waits (up to 30
   minutes) while it is set before typing the prompt. The option is new;
   a CLI that does not read it waits 30 s as before. The same loader is
   installed as `/etc/repose/devshell.sh` (I-275): sourcing it defines
   `_repose_devshell NAME`, which `repose exec` calls in the checkout
   before it execs the user's command, NAME being the command's name in
   the `repose:` messages. A base without the file gets `direnv export
   bash` from the CLI instead. Every interactive bash also sources it and
   redefines direnv's `_direnv_hook` to call `_repose_devshell_prompt`
   (I-488): where direnv finds an `.envrc`, or no such `flake.nix` is
   found, that is direnv's own export (nothing is allowed for the user,
   a denied file stays out); otherwise it exports from the same generated
   directory, printing `repose: loading the dev shell from <dir>/flake.nix`
   when it enters one, so the user's shell gets the agent's dev shell and
   leaving the folder unloads it. A cold load blocks the prompt; Ctrl-C
   ends it and the shell stays without it until it leaves the folder or
   `flake.nix` changes.
4. Exec the real binary with `"$@"`.

`repose-hook` takes the agent from `REPOSE_HOOK_AGENT` or `--agent`
(`REPOSE_AGENT` is accepted for one release, DECISIONS I-58) and the socket
from `REPOSE_HOOK_SOCKET`, `REPOSE_HOOKS_SOCKET` or `--socket`, defaulting to
`/run/repose/hooks.sock`. It reads the hook JSON from stdin (or from
`argv[1]`, which is how Codex's `notify` passes it), maps it to `{agent, kind,
summary, window?}`, POSTs it to the socket, and exits 0 always so a hook
failure
never blocks an agent. Mapping:

| Agent | Payload | kind | summary |
|---|---|---|---|
| claude | `hook_event_name=Stop` | `completed` | last assistant text from the transcript tail (200 chars), else `claude finished` |
| claude | `Notification` with `notification_type` in `permission_prompt`, `agent_needs_input` (`idle_prompt` is no event since DECISIONS I-418) | `needs_input` | `message` |
| claude | `StopFailure` | `error` | `error` or `message` |
| codex | `type=agent-turn-complete` | `completed` | `last-assistant-message` |
| opencode | plugin runs `repose-hook --agent opencode` with `{agent, kind, summary}` already mapped. Version 1: `session.idle` → `completed`, `session.error` → `error`, `permission.updated` or `permission.asked` → `needs_input`. OpenCode 2 (I-481): `session.execution.succeeded` → `completed` (summary: the turn's last text), `session.execution.failed` → `error`, `permission.asked` → `needs_input` | | |
| any | a payload that already has `agent` and `kind` | passed through | |

Anything else is dropped silently. `window` is the tmux window name of the
caller's `$TMUX_PANE` when set.

## Messages and questions (DECISIONS I-244)

`repose-notify` and `repose-ask` are `repose-hook` under two more names
(symlinks in the same package; `repose-hook notify` and `repose-hook ask`
are the same commands). Any process in the guest may run them: an agent's
shell tool, a script, the user.

```
repose-notify [--agent NAME] MESSAGE...        # or the message on stdin
repose-ask [--options A,B,C] [--timeout 30m] [--agent NAME] QUESTION...
```

- The agent is `--agent`, else `REPOSE_HOOK_AGENT` (the wrappers export it,
  so every shell an agent starts inherits it), else the first of the five
  agents found by process name (`/proc/<pid>/comm`, never arguments) among
  the caller's ancestors, else `shell`. The window is
  `REPOSE_AGENT_WINDOW`, else guestd's `$TMUX_PANE` lookup.
- `repose-notify` POSTs `/notify` and returns at once. The message (1 KB)
  goes to the owner's channels as kind `agent_message` and counts against
  the project's notification cap (30 an hour, `features/notifications.md`).
  Exit 0 sent, 1 guestd unreachable or refused, 2 usage.
- `repose-ask` POSTs `/ask`, then long-polls `GET /ask/<id>` and prints the
  answer on stdout. `--options` (at most 3, comma-separated, 64 bytes each)
  makes the answer one of them and gives ntfy and email one-click buttons;
  `--timeout` is a duration or seconds, default 30 minutes, at most 24
  hours. Flags may come before or after the question. While guestd is
  unreachable (a restart) it retries for 2 minutes. SIGINT, SIGTERM or
  SIGHUP cancel the question (`DELETE /ask/<id>`) and exit 130.

| Exit | Meaning |
|---|---|
| 0 | answered; the answer is on stdout |
| 1 | guestd unreachable, refused the question, or lost for 2 minutes |
| 2 | usage: no question, more than 3 options, a bad `--timeout` |
| 3 | no answer before the timeout |
| 4 | no notification channel is on for the owner (email off and no ntfy) |
| 5 | cancelled: the owner dismissed it, the guest stopped, or the question is gone (the guest rebooted) |
| 130 | interrupted |

## Credentials the CLI syncs into the guest

| Laptop path | Guest path | Owner/mode |
|---|---|---|
| `~/.config/gh/hosts.yml` | `/home/dev/.config/gh/hosts.yml` | dev 0600 |
| `~/.codex/auth.json` | `/home/dev/.codex/auth.json` | dev 0600 |
| `~/.local/share/opencode/auth.json` | `/home/dev/.local/share/opencode/auth.json` | dev 0600 |
| `git config user.name/email` | inside the carried git config below (DECISIONS I-195). The old shape, the two keys written straight into `/home/dev/.gitconfig` with `git config --global`, is what a CLI before I-195 still does, and stays accepted: the first carry removes them from `~/.gitconfig` when they equal the carried values, so they cannot shadow later changes | dev 0644 |
| (when gh travelled, whatever the project's remote) | `/home/dev/.gitconfig`: `url.https://github.com/.insteadOf` with the values `git@github.com:` and `ssh://git@github.com/` (each set by value with `--replace-all`, so other values under the key stay), and `credential.https://github.com.helper` and `credential.https://gist.github.com.helper` = `!gh auth git-credential`, so the SSH `origin` guestd sets, and any other github SSH URL, is pushed and fetched over HTTPS with gh's login: the laptop's agent is not forwarded (DECISIONS I-150, I-247). The old shape, only `git@github.com:` and only for a github.com remote, is what a CLI before I-247 writes and stays valid; so is the github.com helper alone, before I-526. The base's `/etc/gitconfig` sets both helpers too, by command name, whether or not gh travelled, with `init.defaultBranch=main`, `push.autoSetupRemote=true`, `pull.rebase=false` and git-lfs's `filter.lfs` (DECISIONS I-525..I-527); system scope, so the carried config and `~/.gitconfig` override any of it. At login and at each base switch, dev's user activation runs `repose-gh-helper-cleanup`, which removes from `~/.gitconfig` the helpers for those two hosts whose value is a `/nix/store` path to gh (what `gh auth setup-git` writes), with the empty `helper =` before them | dev 0644 |

When the laptop's gh keeps its token in the system keyring (gh 2.40+),
the `hosts.yml` that travels carries that token as `oauth_token` under
`github.com:`; the laptop's own file is not changed. All of this is one
ssh, before the git steps of the sync.

The Vercel CLI's login is not copied (DECISIONS I-298; a CLI before it
copied `~/Library/Application Support/com.vercel.cli/auth.json` on macOS,
`~/.local/share/com.vercel.cli/auth.json` on Linux, to
`/home/dev/.local/share/com.vercel.cli/auth.json`). When the laptop has
that file, the same ssh sends its SHA-256, never its bytes, and the guest
removes `/home/dev/.local/share/com.vercel.cli/auth.json` only if its
SHA-256 is the same, printing `#warn` once; any other file there (a
`vercel login` made in the guest) is left alone. The creds marker's
version moved to `creds-2` so every guest takes this part once.

A row the laptop's config.toml skips (DECISIONS I-422; `gh`, `codex`,
`opencode` by their labels) is not sent, and for `gh` the `.gitconfig`
lines above are not written. When the laptop has the file, the same ssh
sends the SHA-256 of what would have travelled (for `gh`, `hosts.yml` with
the keyring token written in), never its bytes, and the guest removes its
copy only while its SHA-256 is the same, printing `#warn` once; a login
made in the guest stays. The skip lines are in the creds marker's hash as
`skip <label> <sha>`. With `env` skipped, the sync's apply writes no `.env`
file; when the guest has an earlier `.env` carry (the `env` marker, or a
probe that said `#envmissing`), the apply tar holds `env/rm` (one
`<sha256> <path>` line per laptop file), the guest removes each file whose
SHA-256 matches and prints `#envremoved <n>` and `#envleft <path>` for the
ones that differ, then deletes `~/.repose/env-paths` and the `env` marker.

Never `~/.claude/.credentials.json` (it is the login share's, below),
never `~/.gemini/oauth_creds.json`
(OAuth over SSH is unreliable; Gemini uses `GEMINI_API_KEY` as a named
secret), never SSH private keys.

## Laptop config the CLI carries (DECISIONS I-195..I-198, I-206)

In the same ssh as the credentials on `run`, and from the session helper
beside the attach on `attach`. Each item is applied by its own `sh -e`
and ends by writing its marker; a failed item leaves the guest's previous
state and is named once.

| What | Guest path | Notes |
|---|---|---|
| the laptop's `git config --global --list --includes`, run in the checkout, minus the I-195 denylist and the keys that hold a secret (I-211), plus the checkout's own `user.name`/`user.email` | `/home/dev/.config/git/repose-carried` (dev 0644, replaced whole by rename) | `/home/dev/.gitconfig` starts with `[include] path = ~/.config/git/repose-carried`, added once, so the guest's own keys after it win. Path values missing in the guest and a command not on PATH are removed from the file and named once. The commands checked: `core.pager`, `core.editor`, `sequence.editor`, `interactive.diffFilter`, `diff.external`, `diff.<driver>.textconv`/`.command`, `merge.<driver>.driver`, and `pager.<cmd>` when it is not a boolean (I-195 as amended 2026-10-05). `filter.<x>.*` is not checked: a missing filter fails closed. When `~/.gitconfig` is a symlink (home-manager) nothing is added and the CLI says what to add |
| `core.excludesFile`'s contents | `/home/dev/.config/git/ignore` | git's default excludes file |
| the laptop's zone | `TZ=` in `/etc/repose/env`, tmux global and per-session `TZ` | see "tmux" |
| the laptop CLI's version (DECISIONS I-412) | `/home/dev/.repose/cli-version`, one line, as `repose --version` names it | marker `cli-version`; the machine guide tells agents to read it; absent until a CLI with I-412 has run or attached |
| `~/.claude/CLAUDE.md`, `keybindings.json`, `skills/`, `agents/`, `commands/`, `output-styles/`, and the scripts under `~/.claude` that `settings.json` runs (DECISIONS I-196) | the same paths under `/home/dev/.claude/`, copied onto what is there (`cp -R`, modes kept) | one marker per file or directory (`claude-claude-md`, `claude-skills`, `claude-scripts`, ...) |
| `~/.claude/settings.json` | `/home/dev/.claude/settings.json`, merged by `internal/cli/claude_merge.jq` with the base's `jq`: guest file as the base, laptop's on top (without `env`, `apiKeyHelper`, `aws*`/`gcp*`, `otelHeadersHelper`, `forceLoginMethod`, removed on the laptop, I-211) with its home rewritten to `/home/dev`, `permissions.allow/deny/ask` unioned, `repose-hook` entries stripped from both and `/etc/repose/claude-settings.json`'s appended, hooks and `statusLine` whose command does not resolve dropped. Written as `settings.json.tmp`, checked with `jq empty`, the old file kept as `settings.json.repose-prev`, renamed into place. An invalid guest file is left alone | marker `claude-settings` |
| `enabledPlugins` from a marketplace | installed by `~/.repose/claude-plugins.sh` (started with `setsid -f`, reads `~/.repose/claude-plugins.json`) with `claude plugin marketplace add` / `claude plugin install`, reporting through `tmux display-message` | marker `claude-plugins`, written only when every install worked |
| gitignored `.env` / `.env.*` files in the checkout, outside dependency directories, up to 1 MB (DECISIONS I-197; `run` only) | the same relative path under `/home/dev/<slug>/`, dev 0600, mtime kept; a guest file with a newer mtime is kept (`#kept <path>`) | written at the end of the sync's apply script, after the checkout; marker `env` |
| the laptop's global tools and the project's commands (DECISIONS I-221, I-222; `run` only) | `/home/dev/.repose/tools-wanted.json`, then `repose-tools-install plan` (base, `nix/guest/base/tools-carry.nix`) | see "Tools carry"; marker `tools`, written by the installer when a pass over the list ends |
| markers | `/home/dev/.repose/carry/<item>` | the hash of the laptop input last applied; the probe prints them as `#marker <item> <hash>`, and `#marker tools-notices waiting` while `~/.repose/tools-notices` is non-empty |
| the tool logins' files (DECISIONS I-224) | `/home/dev/.repose/creds-paths`, one expanded path per line | written with marker `creds` after the logins; the probe prints `#credsmissing` when one of them is gone, and the logins are sent again |
| the last completed sync's key (DECISIONS I-224) | `.git/repose-synced-key` in the checkout | hex sha256 of what the laptop sent; emptied before the apply touches the checkout, written at its end; the probe prints `#synckey`, `#head` and `#headref` |

### Tools carry (DECISIONS I-221, I-222)

`~/.repose/tools-wanted.json`, written by the CLI:
`{"v":1, "hash":"<32 hex>", "items":[{"name", "bins":[...], "manager",
"pkg", "version", "from":"laptop"|"project"}], "node":"<major>",
"ruby":"<x.y>", "java":"<major>"}`.
`manager` is `npm`, `pnpm`, `bun`, `go`, `cargo`, `uv`, `pipx` or absent
(nixpkgs only); `pkg` is the manager's name (the Go package path for
`go`); names, versions and commands are restricted to
`[A-Za-z0-9@/._+-]` by the CLI. `node` is absent when the project pins no
single major. `ruby` and `java` (DECISIONS I-265) are absent when the
project pins none; when present they are already resolved by the CLI to
a version in `nix/guest/base/runtime-versions.json` (the same pinned
series or major, else the oldest newer one, else the newest), so the
guest never picks. A base older than I-265 ignores both keys; the
installer ignores a value that is not digits and dots.

The runtimes and their attributes: `node` → `nodejs_<major>`, `ruby` →
`ruby_<x>_<y>`, `java` → `jdk<major>_headless`. The version found on the
login PATH is `node --version`'s major, `ruby --version`'s `x.y`, and
the major of `java -version`'s quoted version (`1.8.0_x` is 8).

`repose-tools-install plan` (dev, in the carry's ssh, milliseconds):
prints `#installing <name> ...` for the items none of whose `bins` is on
the login PATH and that did not fail before, plus each runtime's
attribute when the guest's version is another and dev's nix profile
comes first on PATH; prints `#warn ...` naming `repose config add
<attribute>` when it does not; writes the missing commands, one per line, to
`$XDG_RUNTIME_DIR/repose-installing`; starts the user unit
`repose-tools-carry` (`--no-block`). A base without the command leaves
the file in place and writes no marker, so the list is sent again after
the base is upgraded.

`repose-tools-carry.service` (user unit of dev, `Nice=10`, idle I/O, also
wanted by `default.target` so a cut-short pass finishes at the next
boot): runs `repose-tools-install run` in a login shell. For each missing
item: the nixpkgs attribute with `bin/<first bin>` (`nix-locate
--minimal --no-group --type x --type s --whole-name --at-root
/bin/<command>`, each line `<attr>.<output>`; the attribute named like the
command first, then one outside a package set, then the shortest; without
nix-locate, the attribute named like the command)
via `nix profile add nixpkgs#<attr>`; else the manager, into the login
PATH (`npm install -g` into `~/.npm-global`, `go install` with
`GOBIN=~/.local/bin`, `cargo install --root ~/.local`, `uv tool install`
for uv and pipx). Each command leaves `repose-installing` when its tool
is done; the file is removed when the pass ends. Output goes to
`~/.repose/tools-install.log` (trimmed at 1 MB); a failure appends
`Could not install <name>: <last output line>` to `~/.repose/tools-notices`,
which the next carry prints as `#warn` lines and deletes, and records the
item in `~/.repose/tools/failed`, so it is not tried again until its
entry changes. Before the items, each runtime step (node, ruby, java)
records the attribute it added in `~/.repose/tools/<runtime>` and
replaces it when the project asks for another version; it removes what it
added, and writes `Could not make <runtime> <version> the guest's
<runtime>: ...` to the notices, when a new login shell does not then
report the version.

## Caches (DECISIONS I-202, I-208)

- Docker: `/etc/docker/daemon.json` `registry-mirrors: ["http://10.63.255.254:5000"]`
  and that address in `insecure-registries`. dockerd falls back to
  Docker Hub when the mirror fails; a login or another registry goes
  direct.
- npm, pnpm, yarn (classic): `repose-npm-registry.service`, a user unit of
  `dev` at boot, appends once to `/home/dev/.npmrc`
  `registry=http://10.63.255.254:4873/` (with a comment line naming
  I-202) when the front answers, and records it in
  `/home/dev/.repose/npm-registry` (`added`). A `~/.npmrc` that already
  sets `registry=` or holds a `registry.npmjs.org` token is never
  changed (`own`). With no answer it changes nothing and tries at the next
  boot. A deleted line stays deleted. A project's own `.npmrc` still
  wins, as `~/.npmrc` is below it for npm and pnpm alike.
  `npm_config_registry` is **not** set: npm lets it beat a project's
  `.npmrc`.

## Memory pressure (DECISIONS I-200)

guestd owns `oom_score_adj` for `dev`'s processes and re-applies it every
5 s: -800 for the tmux server (`tmux: server`) and for each agent
window's agent process (the shallowest process in the window's tree whose
name or executable is the agent's binary), and 0 for any other `dev`
process holding a negative value (it inherited the agent's or the tmux
server's on fork: a dev server an agent started, a pane's shell). A
positive value the user set is left alone, and nothing is ever killed or
stopped by guestd. `dev` cannot lower its own value, which is why root
owns this. When the kernel does kill something, guestd's `oom` warning
names it.

## CPU weights (DECISIONS I-494)

Each SSH connection (its sshd and the `tmux attach` client, a logind
`session-N.scope`) and the tmux server (`repose-tmux-session.service` in
`dev`'s user manager) run at `CPUWeight=1000`. Each pane is a
`tmux-spawn-*.scope` of its own at the default 100, so with a build in
every pane holding every vCPU, keystrokes and screen updates still get
the CPU first. Nothing is capped: a weight only matters while the
machine is full. A process that wants to stay out of the way can still
use `nice`.

## Users and privileges

`dev` uid 1000, gid 1000 (group `dev`), groups `wheel docker kvm`, `sudo`
without password, linger enabled (its user manager runs from boot). `root`
has no password and no SSH (`PermitRootLogin no`, `AllowUsers dev`).
`guestd` runs as root. The `virtiofs` mount is owned by root, read-only.
`users.mutableUsers = false`: there is no other account.

## Environment

`TZ` and `REPOSE_PROJECT=<slug>` from `/etc/repose/env`, `LANG=C.UTF-8`,
`EDITOR=nvim` (present), `REPOSE=1` (so scripts can detect they are in a
guest), `COLORTERM=truecolor`, `NPM_CONFIG_PREFIX=/home/dev/.npm-global`
(the nodejs store path is read-only, so `npm i -g` needs a writable
prefix), `PNPM_HOME=/home/dev/.local/share/pnpm`,
`PLAYWRIGHT_BROWSERS_PATH=/home/dev/.cache/ms-playwright` (writable;
`repose-playwright-seed.service` links the base's packaged browser
revisions into it at boot, never over a real directory; until I-228 it
was the read-only store path), `PRISMA_ENGINES_MIRROR=http://127.0.0.1:850`
(I-228), `PKG_CONFIG_PATH` naming openssl, zlib, sqlite and libffi (I-228) and libyaml, libpq, libxml2, libxslt and libmysqlclient, with `pg_config` and `mysql_config` on PATH (I-265),
`PLAYWRIGHT_SKIP_VALIDATE_HOST_REQUIREMENTS=1`, `PUPPETEER_SKIP_DOWNLOAD=1`,
`PUPPETEER_EXECUTABLE_PATH` and `CHROME_BIN` (the guest's chromium).
`GOPATH=/home/dev/go`, `CARGO_HOME=/home/dev/.cargo`,
`RUSTUP_HOME=/home/dev/.rustup`, `BUN_INSTALL=/home/dev/.bun`,
`DENO_INSTALL_ROOT=/home/dev/.deno`,
`COMPOSER_HOME=/home/dev/.config/composer`,
`GEM_HOME=/home/dev/.local/share/gem` (I-227). `PATH` starts with every
package manager's user bin dir, listed in `nix/guest/base/user-bin-dirs.nix`
(`/home/dev/.local/bin`, `/home/dev/.local/share/pnpm`,
`/home/dev/.npm-global/bin`, `/home/dev/go/bin`, `/home/dev/.cargo/bin`,
`/home/dev/.bun/bin`, `/home/dev/.deno/bin` and the rest), in login and
non-login shells, tmux windows, and dev's systemd user units (I-227). `python`, `python3` and `python3.12` in
`/run/current-system/sw/bin` are a wrapper that adds nix-ld's library
directory to `LD_LIBRARY_PATH` for manylinux wheels and keeps its own
path as `sys.executable` (I-228). `DISPLAY=:99` only while the X server
socket `/tmp/.X11-unix/X99` exists (checked at every shell start); it
exists while the agents' browser or the desktop viewer runs (I-246).
The project fragment's `home.sessionVariables` and `home.sessionPath`
(and those a home-manager module it enables sets) reach the same places
as the static values: NixOS `environment.sessionVariables` (a fragment
value over the base's, sessionPath entries first on `PATH`), plus
`/etc/repose/session-vars.sh`, sourced by `/etc/profile.d/repose.sh`, so
every agent wrapper and `repose exec` sets them with shell expansion,
and dev's tmux server and user manager after a switch, with the names
the new configuration dropped unset (`/etc/repose/session-vars.names`,
last pushed set in `/run/repose-session-vars.names`). PAM expands only
`$HOME` and `$USER` in them, so a user unit started at boot sees other
`$NAME` references unexpanded. `PATH`, `BASH_ENV`, `ENV`,
`REPOSE_ENV_GEN` and `REPOSE` as session variables, and a double quote
in a value, fail evaluation with the reason (I-488).
`BASH_ENV=/etc/repose/bash-env.sh` everywhere the static values reach,
and in dev's tmux server and user manager after a base switch, so a
non-interactive bash (each command an agent runs) loads the secrets guestd
wrote after its parent started, and drops a removed one. It sets a
secret the process lacks and replaces or unsets a variable only when the
process holds one of the last 16 values guestd delivered for that name, so
a different value the process or the project's `.envrc` set stays. guestd also sets each secret, the removals and
`REPOSE_ENV_GEN` in dev's tmux global environment on every `WriteSecrets`,
through `tmux source-file -` on stdin (I-475, I-476). A secret named
`BASH_ENV`, `ENV`, `REPOSE_ENV_GEN` or starting `__repose_` is written as
a file and never exported.

## Ports

Anything the user binds on `0.0.0.0` or `127.0.0.1` (or `::`, `::1`)
inside the guest is reachable through `repose open <port>` (SSH `-L`), and
is forwarded automatically while a CLI is attached (DECISIONS I-199), read
with `ss -Hltn` (iproute2, in the base). Platform-owned ports, never
auto-forwarded: 6080, 6081, 5900, 9224, 9225 (I-246), 9226 (I-296). Each attached CLI records its forwarded
ports in `/home/dev/.repose/forwards/<id>`; the project session's
`status-right` is set from their union and unset when none is left.
Nothing is exposed otherwise. The desktop listens only on `127.0.0.1`: the viewer on 6080 (the
socket-activated entry point), websockify on 6081, Xvnc's VNC on 5900 (up
whenever the display is); the agents' browser's DevTools on 9224
(`repose-browser.socket`, the entry point) and 9225 (Chromium behind it);
9226 is where the guest's sshd listens for the reverse forward of `repose
browser bridge` (I-296), and while that bridge is on, 9224 is
`repose-browser-bridge.socket`, whose proxy goes to 9226 instead of 9225.
`repose-prisma-engines.socket` listens on `127.0.0.1:850` (under 1024, so
never forwarded) and answers every GET with a redirect to
binaries.prisma.sh, a `linux-nixos` engine path rewritten to
`debian-openssl-3.0.x` (I-228).

## Desktop (DECISIONS I-33, I-246, I-292)

The display: `repose-xvnc.service`, TigerVNC's Xvnc as X display `:99`
and the VNC server on 127.0.0.1:5900 in one process (`-geometry 1440x900`
until a viewer asks for another size, `-AcceptSetDesktopSize`,
`-SecurityTypes VncAuth -PasswordFile /run/repose/desktop/vnc-passwd`,
`-AlwaysShared`, `-FrameRate 60`, `-ac -nolisten tcp -noreset`), and
`repose-openbox.service` (every window maximised, undecorated, so the
browser follows the screen's size), both `StopWhenUnneeded`, so they run
exactly while one of their two users does. Xvnc's `ExecStartPre` writes
the password once per boot (`vnc-password`, `vnc-passwd`) when the files
are missing; a later start keeps them.

The agents' browser: `repose-browser.service`, headed Chromium on `:99` as
dev, profile `/home/dev/.local/share/repose/browser`, DevTools on
127.0.0.1:9225, in the system slice `repose-browser.slice`, `OOMPolicy=continue`.
`repose-browser.socket` on 127.0.0.1:9224 starts `repose-browser-proxy.service`
(systemd-socket-proxyd to 9225), which pulls in the browser and the display
on the first connection. Both platform MCP servers attach there. A browser
that exits or is killed takes the proxy with it (`BindsTo`); the socket
starts both again on the next connection.

The viewer: `repose-novnc.service` (websockify on 127.0.0.1:6081 to 5900,
`--web /run/repose/desktop/web --file-only`, serving the page repose ships
from `nix/guest/base/desktop/viewer/`; it wants the browser, so the
desktop always shows it, and `repose-vncconfig.service`, `vncconfig
-nowin`, the clipboard helper), and `repose-novnc.socket` on
127.0.0.1:6080 whose proxy service pulls the chain in on the first
connection. `GET /healthz` on 6080 answers `repose desktop viewer ok`;
the CLI probes it to tell a live forward from a dead one. `GET /` is the
viewer page; stock noVNC's `vnc.html` is not served.

`repose-desktop-idle-check.timer` runs every minute. After 30 minutes
without a client on 6081 or 5900 it starts `repose-desktop-idle.service`,
which stops the viewer (`systemctl start repose-desktop-idle` stops it
now); after 30 minutes with no client on 9225 either, it stops the browser.
The MCP servers and any Chromium the user starts run in the user slice
`repose-browser.slice`. Both slices have `MemoryMax` 1.5 GB small, 3 GB
large, 6 GB xl.

## `repose-guest-profile`

The script the CLI's `open`, `sync` and the hooks rely on:

- `repose-guest-profile` prints `{project_id, slug, name, dir, tz, class,
  base_version, desktop: {running, display, novnc_port, password_file}}`;
  `desktop.running` is the viewer (`repose-novnc.service`), not the
  display, which may be up for the agents' browser alone (I-246).
- `repose-guest-profile desktop start` starts the viewer and the agents'
  browser and prints the boot's password (the same at every start until
  a reboot, I-292); `desktop stop` stops the viewer; `desktop status`
  prints `running` or `stopped`.

## Runner contract (hostd ⇄ `mkGuestRunner`, DECISIONS I-34)

`nix/flake.nix` exposes `lib.mkGuestRunner { fragmentModule, class,
baseVersion, ... }`, which evaluates the base plus home-manager plus the
fragment for Cloud Hypervisor and returns a package with:

| Path | What |
|---|---|
| `bin/run` | starts cloud-hypervisor for one guest; every per-guest value is an argument |
| `bin/virtiofsd` | starts virtiofsd for the store share with the flags the guest expects |
| `bin/shutdown <api socket>` | presses the virtual power button through the CH API |
| `share/repose/system` | the NixOS toplevel (`system_closure`); `kernel` and `initrd` inside it are what `kernel_changed` compares |
| `share/repose/kernel`, `initrd`, `cmdline`, `base-version`, `class` | what `bin/run` boots, for inspection |

`bin/run --guest-id ID --ip A --gateway A --cid N --volume DEV --tap NAME
--mac MAC [--netmask M] [--vcpu N] [--mem MiB] [--hostname NAME]
[--state-dir DIR] [--api-socket P] [--serial tty|socket|PATH]
[--virtiofs-socket P] [--vsock-socket P] [--extra-cmdline "..."] [-- CH
args...]`. Defaults under `--state-dir` (`/var/lib/repose/guests/<id>`):
`ch.sock` (API), `console.sock` (serial), `virtiofsd.sock` (store share,
must be listening before start), `vsock.sock`. The vsock socket is a unix
socket speaking Cloud Hypervisor's handshake: connect, write `CONNECT
5000\n`, read `OK <port>\n`, then the stream is guestd's. The tap must
exist with `vnet_hdr` (`ip tuntap add NAME mode tap user hostd vnet_hdr`);
the runner uses one queue pair. Memory is `shared=on` (virtio-fs needs it);
the volume is opened `direct=on` (O_DIRECT, DECISIONS I-230).
The kernel line gets `ip=<ip>::<gateway>:<netmask>:<hostname>:eth0:off`,
which the guest turns into its static network configuration.

## The Claude login share (DECISIONS I-278)

`repose-claude-auth.service` (oneshot, before `systemd-user-sessions` and
`user@1000`) mounts the virtio-fs tag `claude-auth` at
`/run/repose/claude-auth` (`nosuid,nodev,noexec`), creates
`/run/repose/claude-auth/.credentials.json` and
`/home/dev/.claude/.credentials.json` empty, as `dev`, mode 0600, when
missing, and bind-mounts the first over the second. The share appears
owned by `dev`: the host maps uid and gid 1000 to its own account. Nothing
else under `~/.claude` is shared. With no tag (a guest with no user id, an
older hostd, a share that failed to start) the unit logs "no Claude login
share on this host" and exits 0, and the file stays the guest's own. A
symlink at `~/.claude/.credentials.json` is left alone and not shared.
Re-running the unit changes nothing.

`repose-agent-setup claude` sets `hasCompletedOnboarding: true` in
`~/.claude.json` when `~/.claude/.credentials.json` is a mount point and
the key is absent, since Claude Code's first-run onboarding asks for a
login method even when the shared file holds one.

