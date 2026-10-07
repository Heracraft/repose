# 07 · cli

## 1. Goal

`repose` is the one static Go binary a developer installs. It turns "I am in
a project directory" into "I am attached to tmux inside that project's guest",
and everything else (start, stop, secrets, config, snapshots) is a thin, exact
mirror of the HTTP API. It never holds platform secrets and never talks to a
host directly; it talks to the API over HTTPS and to guests over SSH through
the gateway.

## 2. Scope: builds

- `cmd/repose` and `internal/cli/` (Go 1.23+, cobra for commands, no
  framework beyond that).
- Every command in `DESIGN.md` §10 with the flags, output and exit codes in
  §5 below.
- Logto login: authorization code with PKCE and a loopback redirect, device
  code fallback, token storage per `interfaces/cli-config.md`.
- Project resolution from the git remote with normalisation and the
  `projects.json` cache.
- SSH certificate issue and silent refresh, `~/.ssh/repose/` files, the one
  `Include` line in `~/.ssh/config`.
- The `run` sequence: create if new, start if stopped, stream the build,
  certificate, git-based sync with dirty-tree refusal, credential file sync,
  tmux attach or prompt send.
- `open <port>` (SSH `-L`) and `browser` (the machine's desktop viewer on
  6080 through a background forward, opens the browser on the viewer page
  with the password in the URL fragment, I-292).
- `status` table and `--json` for every read command.
- SSE build log rendering with the Nix error and fragment line highlighted.
- `install.sh` and release builds for darwin/arm64, darwin/amd64,
  linux/amd64, linux/arm64, plus `nix run`.
- Shell completion for bash, zsh, fish (`repose completion <shell>`).
- `internal/fakes/api` if it does not exist yet (the consumer writes the
  fake).

### Added by I-8: `events` and `notify`

- `repose events [--since 24h] [--follow]`: prints the project's events from
  `GET /projects/:id/events` as `ts  agent  kind  summary`, one per line;
  `--follow` polls every 10 s. Exit 4 if the project does not exist.
- `repose notify set --email on|off --ntfy <url>|none`: `PATCH /me` with the
  notify block; prints the resulting settings.
- `repose notify test`: `POST /me/notify-test`; prints per-channel `ok` or
  the error string. Exit 1 if every channel failed.

### Binary name collision (DECISIONS I-15)

Arch Linux ships an unrelated `repose` binary in its official repos. The
installer puts ours in `~/.local/bin` and prepends that to PATH in the shell
rc if it is not already first; when `command -v repose` resolves to another
file after install, the installer prints `another repose is on your PATH at
<path>; ours is at ~/.local/bin/repose` and exits 0. `repose version` prints
`repose <version> (herakraft)` so a user can tell which one answered.

## 3. Scope: does not build

- The API itself, the CA, project routing (05-control-plane-api).
- The gateway (06-gateway-edge). The CLI only writes SSH config that points
  at it.
- Anything inside the guest: tmux session creation, hook wiring, secret
  files (02-guest-base, 04-guestd). The CLI sends `tmux` commands over SSH
  and relies on `interfaces/guest-conventions.md`.
- The dashboard (08-dashboard).
- `repose mcp forward` (deferred, see `DECISIONS.md` R2-11 and
  `DESIGN.md` §18). The command tree reserves the name and prints "not
  available yet" with a link. `repose browser bridge` was reserved the
  same way until I-296 built it (features/browser.md).
- `repose-admin` (operator tool, lives with 05).

## 4. Interfaces

Owns: `interfaces/cli-config.md`.

Consumes: `interfaces/api.md` (every route), `interfaces/ssh-gateway.md`
(login name, certificate file, known_hosts CA line, ssh config block),
`interfaces/guest-conventions.md` (tmux session and window names, credential
paths, `REPOSE=1`).

## 5. Design detail

### 5.1 Command tree

```
repose login [--no-browser]
repose logout
repose run [PROMPT...] [--agent claude|opencode|codex|gemini|pi] [--size small|large|xl]
            [--name NAME] [--temp [DURATION]] [--no-sync] [--no-attach] [--worktree]
            # --stash-remote, --discard-remote: hidden, exit 2 naming `repose sync` (I-367)
repose attach [PROJECT]
repose start [PROJECT]
repose stop [PROJECT] [--no-snapshot]
repose status [PROJECT] [--json] [--watch]
repose open PORT [--local-port N] [--no-browser]
repose browser [PROJECT] [--stop] [--no-open]   # I-292; `open --desktop [--stop] [--no-browser]` is the hidden old name
repose cp [-r] SRC DST        # PROJECT:PATH, or :PATH for this checkout's (I-201)
repose paste [PROJECT] [--window NAME] [--print]   # clipboard image to the guest (I-252)
repose ps [PROJECT] [-q|--quiet] [--json]      # the tmux windows (I-274)
repose exec [-i] [-t] [PROJECT] [--] CMD [ARG...]  # one command in the checkout, its exit code (I-275, I-411)
repose ssh [PROJECT]           # login shell in the checkout, outside tmux (I-275)
repose secrets set NAME [--from-file PATH] [--from-env]
repose secrets import [FILE|-] [--dry-run]   # dotenv, default ./.env (I-277)
repose secrets list            # alias ls
repose secrets rm NAME
repose config show [--revisions]
repose config edit
repose config apply [PATH]      # PATH defaults to ./repose.nix if present, else opens editor
repose config add NAME...       # catalog id, else any nixpkgs attribute path (I-220)
repose config remove NAME...    # alias rm
repose snapshots list [-q|--quiet] [--json]   # alias ls
repose snapshots create
repose snapshots restore SNAPSHOT_ID [--as-new NAME]
repose rm [PROJECT] [--yes|-y] [--wait]   # alias destroy, the old name (I-273)
repose restore [NAME] [--as NEW-NAME] [--snapshot ID]   # no NAME: the checkout's remote finds it
repose logs [PROJECT] [--kind console|build|ops] [--since 1h] [--follow|-f]
repose events [PROJECT] [--since 24h] [--follow|-f]
repose ls [--destroyed [--all]] [-q|--quiet]  # list all, ignores cwd; --destroyed: what can be restored; alias projects, the old name (I-273)
repose fork [PROJECT] [-n N] [--name NAME] [--size S] [--snapshot ID] [--prompt TEXT [--agent A]] [--json]   # I-254
repose resize [PROJECT] [DISK] [--size small|large|xl] [--yes|-y]   # grow the disk (e.g. 80G); --size changes the class (I-260); PROJECT positional (I-268)
repose scan [DIR] [--json]     # dry run of what run installs (I-222)
repose questions [PROJECT] [--json]            # I-245
repose reply [PROJECT] [ANSWER...] [--question ID] [--json]
repose notify set [--email on|off] [--ntfy URL|none]
repose notify test
repose version
repose completion bash|zsh|fish
repose mcp forward ...           # reserved, prints not-available message
repose browser bridge [PROJECT] [--cdp URL] [--user-data-dir DIR] [--no-browser]   # I-296
```

Global flags: `--project ID|SLUG` (or `REPOSE_PROJECT`), `--api-url` (or
`REPOSE_API_URL`), `--json` on read commands, `-v` for debug logging to
stderr.

PROJECT (DECISIONS I-155): the commands whose object is a project take it
as their one argument, docker-style; `--project` and `REPOSE_PROJECT` keep
working, the same project named both ways is fine, two different ones is
exit 2. `run`'s argument stays the prompt (all remaining words, joined),
and a one-word prompt equal to one of the user's slugs is refused with
exit 2 pointing at `--project`/`attach` (`--agent` sends it anyway).
`open`, `secrets`, `config`, `snapshots` keep `--project` because their
argument is something else. A command given an argument it does not take
exits 2; so do unknown commands and flags. Completion offers the
account's slugs for PROJECT and `--project`.

### 5.2 Login

`repose login`:

1. Discover Logto's OIDC config from `config.toml` `logto_issuer` (default
   `https://auth.herakraft.co`, overridable). Cache the discovery document
   for 24 hours.
2. Only with `--browser` (since v0.1.2, DECISIONS I-101: the Logto Native
   application has no registered redirect URIs, so this flow cannot
   succeed against it) and a browser available (`REPOSE_NO_BROWSER`
   unset, `DISPLAY` or macOS, not inside a guest (`REPOSE=1`)): start a
   listener on `127.0.0.1:0`, build the authorization URL with
   `code_challenge` (S256), `scope=openid offline_access profile email`,
   `resource=https://api.repose.herakraft.co`, open the browser, wait up to
   5 minutes for the callback, exchange the code. Print `Logged in as
   <handle> (<email>)`.
3. Otherwise, and by default, device code: `POST /oidc/device/auth`, print

   ```
   Open https://accounts.herakraft.co/device?user_code=ABCD-EFGH
   The page shows code ABCD-EFGH; check it matches, then sign in.
   Waiting...
   ```

   and poll at the returned interval.
4. Store per `interfaces/cli-config.md`: macOS keychain for the refresh
   token (service `repose`, account `<issuer>`), else
   `credentials.json` 0600.
5. Call `GET /me`. If `billing.has_card` is false, print

   ```
   No card on file. Add one at https://repose.herakraft.co/billing before
   the first `repose run`.
   ```

   Exit 0; the run command will fail with `payment_required` (exit 7) until
   a card exists.

Access token refresh happens transparently in the API client: on 401 with
`unauthenticated`, refresh once, retry once. If the refresh fails, exit 3
with `Not logged in. Run \`repose login\`.`

`repose logout` revokes the refresh token at Logto, deletes the stored
tokens, calls `POST /certs/revoke {all:true}`, removes
`~/.ssh/repose/id_ed25519-cert.pub`. It leaves `projects.json` and the ssh
config in place.

### 5.3 Project resolution

Order:

1. PROJECT / `--project` / `REPOSE_PROJECT`: id or slug, resolved via `GET
   /projects`. Writes nothing to the cache (DECISIONS I-152).
2. `projects.json` `by_dir[<repo root, else cwd>]` (set when `run`
   created a project with no remote there, or when a sync into a named
   project with no remote came from a directory with no remote and no
   entry, DECISIONS I-575), used only when that project's
   `remote_url` equals the directory's normalised remote (both empty for
   such a project); a mismatched or 404 entry is deleted.
3. `git remote get-url origin` in the cwd's repo root, normalised
   (`interfaces/cli-config.md`), then `projects.json[<remote>]` (checked the
   same way), then `GET /projects` filtered by `remote_url`.
4. Nothing found and the command is `run`: create (see 5.5). Nothing found
   and the command is anything else: exit 4 with

   ```
   No repose project for github.com/a/b. Run `repose run` here to create one,
   or name one: `repose attach PROJECT`.
   ```

No git remote and no `--name` on `run`: create a project named after the
repository root, else the cwd, with characters outside `[A-Za-z0-9._-]`
replaced by `-`, and write `by_dir` for it (DECISIONS I-358). A Ctrl-C
after the create and before the connect removes that entry and exits 130
with ``Interrupted. <slug> was created and stays on your account;
`repose rm <slug>` removes it.`` (DECISIONS I-575).

### 5.4 Certificates and SSH files

`ensureCert(projectIDs)`:

- Ensure the CLI's own key `~/.ssh/repose/id_ed25519` exists: generated in
  process (ed25519, no passphrase, 0600) the first time. The user's
  `~/.ssh/id_*` keys are never created, read, certified or offered
  (DECISIONS I-149).
- Read `~/.ssh/repose/id_ed25519-cert.pub`; if it certifies that key, is
  valid for more than 30 minutes, its principals cover the requested
  project ids, and `known_hosts` exists, reuse it. A certificate for any
  other key (v0.1.4 certified `~/.ssh/id_ed25519`) is re-issued.
- Else `POST /certs` with the public key and the project ids the user has
  (all of them, so one certificate covers every project) and write the
  certificate 0600. Nothing is `ssh-add`ed: the config names the key and
  certificate, and the key has no passphrase to cache.
- Write `~/.ssh/repose/known_hosts` with `@cert-authority
  ssh.repose.herakraft.co,10.64.* <host_ca_pub>` from the response.
- Rewrite `~/.ssh/repose/hosts` with one `Host <slug>.repose` block per
  project (the block in `interfaces/ssh-gateway.md`, with `ControlMaster
  auto`, `ControlPath ~/.ssh/repose/cm-%C`, `ControlPersist 10m` except on
  Windows, so every ssh of a command shares one connection), and
  `~/.ssh/repose/config`, whose Match line runs `repose ssh-prepare` before
  ssh reads the blocks (I-281; ssh-gateway.md "CLI side"). Ensure
  `~/.ssh/config` has `Include ~/.ssh/repose/config` before its first
  `Host` or `Match` line (inserted once as its first line with a comment
  `# added by repose`; one that exists only after a `Host` line does not
  count). Never rewrite any other line; a symlinked config is written at
  its target, never replaced, and a read-only one is left alone.
- Verify with `ssh -G <slug>.repose` that the alias resolves to the
  gateway host and the `<slug>.<handle>` user. If not, warn with the
  reason and the exact line to add (for a read-only link, where), and use
  `ssh -F ~/.ssh/repose/config` for the CLI's own connections (I-151).

The refresh is silent: every command that opens SSH calls `ensureCert`
first. If `POST /certs` returns `rate_limited`, use the existing cert if it
has any validity left and warn. If the gateway refuses the connection
(`Permission denied`), re-issue once and keep waiting.

### 5.5 The run sequence

```
$ repose run
```

1. Resolve the project. If none: `POST /projects {name: <repo basename or
   --name>, remote_url, class: --size or config default_class or large, tz:
   local zone}`. On `payment_required` exit 7 with the billing URL. On
   `conflict` for the name, append `-2`.., ask.
2. `GET /projects/:id`. A create or start in flight is waited on (its op,
   else the state); `stopped` or `error` gets `POST /start` (the api
   restarts an `error` project, `{restart: true}`, I-157) and the op is
   waited on, polling every 500 ms. If the op is a build, open the SSE log
   and render it (5.8). An op that fails prints `Could not start <slug>:
   <reason> (<code>). <next step>` and exits 1; a build's own failure
   prints the Nix block and exits 10. Every wait shows its phase on stderr
   (5.12).
3. `ensureCert`.
4. Wait until `ssh <slug>.repose true` succeeds, up to 60 seconds after the
   API says `running`, polling every second, then print `Connected to
   <slug> (<class>)`. This first ssh becomes the ControlMaster every later
   one in the command shares.
5. Credential sync, first in step 6d's ssh, before its git steps (a
   first sync that clones in the guest, I-203, sends it on its own before
   the clone; DECISIONS I-224). With `--no-sync`, or outside a
   repository, it goes in two ssh commands of its own, the guest's
   markers and then what changed, together with the carry (I-366):
   for each row of the table in `interfaces/guest-conventions.md`, if the
   laptop file exists, copy it to the guest path with its mode; set the
   git identity with `git config --global`; when gh travelled and the
   remote is on github.com, set the HTTPS `insteadOf` and gh credential
   helper (I-150). A gh token kept in the laptop keyring is written into
   the copy of `hosts.yml` that travels. Never the Claude file, never
   Gemini's, never SSH keys.
6. Sync (unless `--no-sync`), two ssh round trips (DECISIONS I-150). On
   `run` it is a first sync only (I-367): after step b, a guest whose
   checkout has any commit is left alone, steps c and d send only the
   logins and carry, and the run prints `Not synced: your laptop has work
   the machine doesn't (<counts>). \`repose sync\` sends it.` when the
   laptop's sync key differs and it has a modified or untracked file or a
   commit the guest lacks. `repose sync` runs every step:

   a. Local: `git rev-parse HEAD` → `H`; `git status --porcelain` → dirty
      list; `git ls-files --others --exclude-standard` → untracked list. A
      directory that is not a repository, has no commit, or is a shallow
      clone exits 2 with the command that fixes it (or `--no-sync`).
   b. Remote, one ssh: find the checkout (guest-conventions "The
      checkout"); with none, make `~/<laptop folder>` (the slug when that
      name is taken), record it in `~/.repose/checkout` and `git init` it
      (I-368); then report `git status --porcelain`, every commit a ref or `HEAD`
      points at, and whether `origin` exists. With a non-empty status and
      nothing new on the laptop since the guest's last sync, the checkout
      is left alone (DECISIONS I-248). Otherwise the overlap check of step
      d decides: exit 6 with the refusal in `features/sync-at-launch.md`
      only when the guest changed a path the sync writes (DECISIONS
      I-573).

      `--stash-remote` runs `git stash push -u -m "repose run"`,
      `--discard-remote` runs `git reset --hard && git clean -fd`; both run
      at the start of step d's script. A non-empty status whose
      fingerprint (`HEAD` and the `git add -A` tree, built in a copy of the
      index) equals `.git/repose-synced` is the previous sync's own diff
      and untracked files, not an agent's: no exit 6, and step d stashes it
      first (`git stash push -u -m "repose run: last sync"`, said in the
      summary line; stashes with exactly that message past the newest 10
      are dropped, others never), after checking the fingerprint again (a
      change in between stashes only the paths the per-path record of
      step d still matches, I-573). Each checked-out submodule, nested ones too,
      adds its own `HEAD` and `git add -A` tree to the fingerprint, and
      the stash, discard and last-sync stash run in each of them as well
      (I-263).
      Every status here runs with `-c status.showUntrackedFiles=normal -c
      submodule.recurse=false --ignore-submodules=none`. Step d ends by
      writing the fingerprint when it leaves the tree dirty with nothing
      of the guest's kept, and removing the file otherwise (DECISIONS
      I-210), and by writing `.git/repose-synced-paths` (I-573).
   c. Local: of the guest's commits, keep the ones this checkout has; `git
      bundle create` `HEAD` and `refs/remotes/origin/<branch>` excluding
      them (`--stdin`, so the list never hits the command line). Nothing is
      bundled when the guest already has everything; the first sync of an
      empty guest carries the whole history. Nothing is pushed and nothing
      is asked: an unpushed commit travels like any other.
   d. Remote, one ssh whose stdin is one tar (the bundle, `git diff
      --cached --binary` and `git diff --binary`, a tar of the untracked
      list, and `paths`: every path the laptop's own work writes, NUL
      separated): `git fetch` from the bundle; when the guest is on
      `<branch>` and it has commits the laptop lacks, decide on a merge
      (DECISIONS I-574: `git merge-tree --write-tree` clean, the guest's
      commits since the merge base leave `paths` alone, a committer
      identity); without `--stash-remote` or `--discard-remote`, the
      overlap check of `features/sync-at-launch.md` (I-573: the guest's
      dirty and untracked files against `paths` plus what the checkout to
      `H` or the merge tree changes; any overlap exits 3 naming them,
      before anything else runs); then the logins and carry; set aside the
      last sync's own paths (or everything, with a flag);
      move `origin/<branch>` forward to the laptop's value; add `origin`
      (guestd's `git@host:owner/repo.git`) if missing; check out `H` on
      `<branch>`, creating it or fast-forwarding it, or, when the guest's
      branch has commits the laptop lacks, merge `H` into it as decided,
      else leave the branch alone and check `H` out detached with a
      warning; set the branch's upstream; apply
      the staged diff with `git apply --index` and the unstaged one with
      `git apply` (I-258); extract the untracked tar. Skip files over
      100 MB with a warning, dependency/cache directories at any depth
      (named once), and anything past 500 MB in total; symlinks travel as
      symlinks. Respect `sync.exclude`, matched at any depth (I-194).
      Submodules the laptop has checked out go the same way, parents
      first (I-263): a bundle of what the guest's copy lacks (its refs come
      back from the probe), checkout of the laptop's submodule `HEAD`, a
      ref `refs/repose/laptop-head`, the two diffs, the index's submodule
      entries set with `git update-index`, `git submodule init` and
      `sync`; their untracked files ride the one untracked tar. The
      superproject's diffs pass `--ignore-submodules=all`. A shallow
      submodule is fetched by the guest itself, a failure a warning.
   e. Print `Synced: 4 modified, 2 untracked`, plus `(3 new commits)` when
      commits travelled, `, merged with the machine's <branch>` after a
      merge, and `; kept the machine's changes to N files` when the guest
      had changes elsewhere.
   f. A project created with `--name` in a directory that has no git
      remote takes the same path without `origin` or remote-tracking refs
      (this replaced I-138's whole-tree commit): its commits travel, and a
      deletion committed on the laptop is a deletion in the guest.
   Print one line `Credentials: gh, opencode` naming what step 5 copied.
7. If PROMPT given: agent = `--agent` or project `agent_default`. Over SSH:
   `tmux new-window -t <slug> -n <agent> -c <checkout> -d '<agent>'` (name
   becomes the lowest free `<agent>-N`, N >= 2, if the window exists,
   DECISIONS I-253; with `--worktree`, which needs a PROMPT, the directory
   is a new `git worktree add -b worktree-<N> ~/<checkout>-worktree-<N> HEAD`
   and the name also skips any N whose worktree path or branch exists; a
   checkout with no `.git` or no commit is refused with exit 2), wait until the pane has been
   idle 1 second (`tmux display -p '#{pane_current_command}'` is the agent
   and no output for 1 s), then `tmux send-keys -t <slug>:<window> -l
   '<prompt>'` and `send-keys Enter`. If the agent is `claude` and
   `~/.claude/.credentials.json` is missing or empty in the guest (`test -s`;
   the login share keeps an empty file there until the first login,
   DECISIONS I-278) and no
   `CLAUDE_CODE_OAUTH_TOKEN` secret is set, attach instead of sending so the
   user can complete the login, and print

   ```
   Claude Code is not logged in on this guest yet. Finish the login in the
   window that opens, then re-run with your prompt.
   ```
8. Unless `--no-attach`: `ssh -t <slug>.repose tmux attach -t <slug>`
   (attaches to the window just created when there was a prompt). On
   macOS and Linux ssh runs on a pty of the CLI's, which passes the
   terminal through and copies dropped files and Ctrl+V clipboard images
   to the guest (the input proxy, DECISIONS I-280); the exit status is
   ssh's. With `REPOSE_INPUT_PROXY=0`, on Windows, or when stdin or stdout
   is not a terminal, the CLI process replaces itself with ssh as before.
   Under the input proxy an ssh that ends with 255 attaches again once
   the machine answers, for up to 2 minutes (DECISIONS I-469;
   `features/run-and-attach.md` "A dropped connection attaches again").

`repose attach [PROJECT]` is steps 1 (resolve, no create), 3, 4, 8. A
project that is not running exits 5 with its true state, the reason
the api recorded for an `error` (`last_error`), and the command that fits
(DECISIONS I-153).

`run` ends with `Ready in <time>.` on stderr before attaching.

Startup fast paths (DECISIONS I-223..I-225). The steps above are what
happens; these decide how many round trips they cost, and `REPOSE_TIMING=1`
prints one stderr line per phase, api call and ssh (`ops/dev/startup-bench.sh`
measures them):

- Step 2 does not read the project again when step 1 just did. The op
  wait polls every 500 ms for its first 30 s, then 1 s, then 2 s.
- Steps 3 and 4 need no api call when `~/.ssh/repose` already covers the
  project (a certificate for the CLI's key with the project's id and 30
  minutes left, `known_hosts`, its `Host` block, the alias resolving), and
  no ssh when a ControlPersist master for it is up (`ssh -O check`): the
  gateway ends a client connection when its guest connection ends, so a
  live master is a guest that answered. Otherwise `GET /me` and `GET
  /projects` go in parallel, as before, with `ensureCert`.
- `run` starts step 6b's probe before step 1's api call when the projects
  cache names the project and the files cover it; with no master up, the
  probe's connection becomes the master. Its answer is used only when the
  project resolves to that id and was running before the command (a start
  makes a new guest); otherwise it is discarded, its master closed, and
  the steps run as written.
- `attach` with the cache naming the project and a master up makes no api
  call: it prints `Connected to <slug>` and execs step 8.
- Step 6d is skipped when the guest is exactly as the last completed sync
  left it and the laptop would send the same thing: the apply records a
  key (the hash of `H`, the branch, the tracked ref, the diff and the
  untracked tar) in `.git/repose-synced-key`, cleared before it touches
  the checkout; the probe returns it with `HEAD` and `.git/HEAD`, and the
  summary line ends `the guest already had them`. Step 5 is skipped when
  the guest's `creds` marker holds the hash of the same login files
  (content and mtime) and every file it wrote is still there
  (`~/.repose/creds-paths`); an unchanged `run` then makes no ssh after
  the probe.

### 5.6 stop, start, destroy, resize

- `stop [PROJECT]`: `POST /stop {snapshot: !--no-snapshot}`, phase on the
  op, then `Stopped <slug> in <time>. Snapshot <id> (1.2 GB). Disk is
  still billed.` Already stopped: says so, exit 0. Success lines say what
  happened and stop; a next command is for failures and refusals
  (DECISIONS I-484).
- `start [PROJECT]`: `POST /start`, wait, print `<slug> is running
  (<class>), ready in <time>.` Does not
  sync. When the api answers `restart: true` (a project in `error`, or a
  running one whose guestd stopped answering, I-157) the phase reads
  `Restarting <slug> (its agent stopped answering)`. Already running and
  healthy: says so, exit 0.
- `destroy [PROJECT]`: asks `Destroy <slug>? A final snapshot is kept for
  30 days. [y/N]` unless `--yes`/`-y` (no terminal and no `--yes`: exit 2).
  Then `DELETE /projects/:id` → `202 {op_id}` (api.md, I-156) and, by
  default, returns at once (DECISIONS I-166): `Destroying <slug>. Its final
  snapshot is kept for 30 days.` The project reads
  `destroying` in `repose ls` from then on; a destroy that fails
  shows there as `error` with the reason and `repose rm <slug>` as
  the retry, in `repose status`, and as a `destroy_failed` notification
  (I-165). `--wait` keeps the old behaviour for scripts: wait on the op,
  then on `GET` answering 404, and only then print `Destroyed <slug> in
  <time>. Its last snapshot is kept until <date>.`; an op in `error` prints `Could not destroy
  <slug>: <reason> (<code>). <slug> is still there, <state>. \`repose
  destroy <slug>\` tries again.` and exits 1. An api that answers without
  an op id is waited on by polling the project (DECISIONS I-153).
- `restore NAME [--as NEW-NAME] [--snapshot ID]` (I-167): `POST
  /projects/restore {slug (or project_id when NAME is an id), name?,
  snapshot_id?}`, then waits with the phases of a create (building,
  restoring, booting) and prints `Restored <slug> from its snapshot of
  <time> in <elapsed>; it is running (<class>).` NAME is resolved by the api: the live project with that
  slug, else the destroyed ones, newest snapshot first. A name in use
  (`409` with `detail.reason = "name_taken"`) asks for another name on a
  terminal (empty cancels) and otherwise exits 2 naming `--as`; nothing to
  restore exits 3 with the api's sentence and `repose ls
  --destroyed`. Completion offers the destroyed projects' slugs.
  `snapshots restore` is unchanged, for restoring in place.
- Resize is `repose config apply` with `volume_bytes` in the fragment
  header? No: it is its own route, so `repose resize 80G` exists as a
  command for `POST /resize`, visible and in the public CLI reference
  (DECISIONS I-242; it was hidden before).

### 5.7 status

```
$ repose status
todo-app   large   running   2h14m   claude: working   today $0.31   month $12.40
  host eastus/h-01   ip 10.64.0.7   disk 8.1/40 GB   snapshot 6h ago
  sessions 1   tmux clients 1   docker 2
  last event 12m ago: claude completed "ran tests, 3 failures fixed"
```

`--watch` refreshes every 5 seconds. `--json` prints the `Project` object.
A project in `error` gets an `error: <reason>` line under the first.
`repose ls` prints a table with a header row (`PROJECT CLASS STATE
UP AGENTS TODAY MONTH`, `-` where a column does not apply, uptime only
while running, and `LEFT`, a temporary machine's time left, only while
one is listed), then one line per project in `error` with its reason and
the command that fixes it; with no projects it says `No projects yet.`
(I-484).
`--json` is the api's list, unchanged (DECISIONS I-153).
`repose ls --destroyed` lists `GET /projects/destroyed` one row per
name, the one `repose restore NAME` restores (that name's newest
snapshot): `PROJECT CLASS DESTROYED SNAPSHOT SIZE RESTORABLE UNTIL
EARLIER`, EARLIER counting older destroyed projects of the name,
`(name in use)` after a slug a live project holds with a line giving
`repose restore <id> --as NEW-NAME` for it, and a last line naming
`repose restore NAME`; `--all` lists every row with its `ID`, earlier
ones marked `(earlier)`; `--json` is the api's list (I-167, I-192). `repose restore` with no NAME inside a
checkout restores the destroyed project whose `remote_url` is the
checkout's normalised remote (the newest destroy when one name was
destroyed several times); two or more names are asked about on a
terminal and listed with exit 2 otherwise; no match exits 4 naming the
remote (I-172).

### 5.8 Build log rendering

`GET /projects/:id/ops/:op/log` is SSE. Lines are printed as received with
a dim `nix ›` prefix. On the `done` event with an error, print the error
block from the op (`build_failed` or `eval_failed`) verbatim, then if
`fragment_line` is set, print the fragment with that line marked:

```
error: attribute 'nodejs_25' missing
   at repose.nix:12:5
      11 |   home.packages = with pkgs; [
      12 |     nodejs_25
         |     ^
```

Exit 10. `eval_failed` and `build_failed` are the only errors that print
Nix output; every other API error prints `{code}: {message}`.

### 5.9 open

`repose open 3000`: `ensureCert`, then `ssh -o ControlPath=none -N -L
127.0.0.1:<local>:127.0.0.1:3000 <slug>.repose` in the foreground (its own
connection, not the shared ControlMaster, so the forward ends with
Ctrl-C rather than living on in the master, I-149), print
`http://localhost:3000 → todo-app:3000 (Ctrl-C to stop)`, and open the
browser unless `--no-browser`. `--local-port` defaults to the same port,
falling back to a free port with a message if taken.

`repose browser [PROJECT] [--stop] [--no-open]` (I-292): over SSH
`repose-guest-profile desktop start` (starts Xvnc, the window manager,
the viewer and the agents' browser per 02-guest-base, prints the boot's
password), then a background forward of 6080 (laptop 6080 or a free port,
I-261): `ssh -o ControlPath=none -N -o ExitOnForwardFailure=yes -o
ServerAliveInterval=15 -o ServerAliveCountMax=3 -L
127.0.0.1:<local>:127.0.0.1:6080 <slug>.repose` started in its own session,
its port and pid in `~/.config/repose/browser-forwards/<slug>.json`; a
recorded forward whose port answers `GET /healthz` with `repose desktop
viewer ok` is reused. Print `Watching <slug>'s browser at
http://localhost:<local>/#p=<password> (the view sleeps after 30 idle
minutes).` and open the URL unless
`--no-open`. `--stop`: `desktop stop` in the guest when it runs, kill the
recorded forward (only while its port still answers as our viewer), forget
it. `repose open --desktop [--stop] [--no-browser]` is the same command
under its old name, hidden from help, with one stderr line saying so.

### 5.10 secrets, config, snapshots, logs

- `secrets set NAME`: value from `--from-file`, `--from-env` (reads
  `$NAME`), or a hidden prompt. Validates the name regex client-side.
  `PUT /projects/:id/secrets/NAME`. Prints `Set NAME (pushed to running
  guest)` or `(will be delivered at next start)`.
- `config show`: prints the fragment; `--revisions` lists them with status.
- `config edit`: fetches, opens `$EDITOR` on a temp file, on save `PUT` and
  render the build log, then `Applied revision <id>` or the error block. If
  the build reports `kernel_changed`, print `This change needs a reboot;
  run \`repose stop && repose start\` when the agent is idle.`
- `config apply [PATH]`: same with a file; `./repose.nix` default. The file
  is the fragment and is *not* required to be committed; recommend adding
  it to the repo in the message.
- `config add NAME...` / `config remove NAME...` (DECISIONS I-220): read
  `GET /catalog` and the current `menu`, add or drop items (a catalog id is
  `{id}`, any other name matching the attribute-path pattern is
  `{package}`; `pkgs.` and `nixpkgs#` prefixes are dropped; a bad name is
  exit 2 before any request), `PUT {menu}`, print `Added gcc and air to
  izma. Building revision 1a2b3c4d ...`, stream the log, then `Applied
  revision 1a2b3c4d.` A failed build prints the summary line without the
  generated fragment's location, then `Nothing changed in izma; the
  previous revision is still active.` (exit 10). Names already present
  are said once and not rebuilt; a hand-written fragment is exit 2 pointing
  at `config edit`.
- `snapshots list/create/restore` mirror the API. `restore` without
  `--as-new` requires the project stopped and asks for confirmation.
- `logs`: `GET /logs`, `--follow` polls every 2 s with `since`.

### 5.11 Install and release

`install.sh` at `https://repose.herakraft.co/install.sh`: detects
`uname -sm`, downloads `repose_<version>_<os>_<arch>.tar.gz` from the
GitHub release, verifies the sha256 from `checksums.txt`, installs to
`~/.local/bin` (or `/usr/local/bin` with sudo when `--system`), prints the
PATH hint if needed. GoReleaser config in `.goreleaser.yaml`, CGO off, `-s
-w`, version from the tag into `internal/version`. `nix run
github:heracraft/repose#repose` works via the flake's `packages.repose`.

### 5.12 Output rules

Human output to stdout, progress and warnings to stderr, so `--json` and
pipes are clean. Colours only when stdout is a TTY. No spinner when not a
TTY. All timestamps local.

Progress (DECISIONS I-154): every command that waits (run, start, stop,
destroy, resize, snapshot create and restore) shows its phase on stderr.
On a terminal: one spinner line with the phase and its elapsed time,
redrawn every 100 ms, replaced by `✓ <done>  <time>` for phases that
have no result line of their own (Created, Built the environment,
Booted); streamed build-log lines pass through without tearing it.
Otherwise (`TERM=dumb`, `REPOSE_NO_SPINNER=1`, or stderr not a
terminal): one `<phase>...` line per phase. `--json` commands show none.
Ctrl-C clears the line and exits 130.

Errors are one sentence and the next step (I-153): `Could not <step>:
<why> (<code or ssh's last line>). <next>`. They never include a remote
command line, a guest id, or the host's own wording (that is the op's
`detail`, printed only under `-v`).

## 6. Failure modes

| Situation | Outcome |
|---|---|
| Not logged in or refresh failed | exit 3, `Not logged in. Run \`repose login\`.` |
| No card on file on create/start | exit 7, `Add a card at https://repose.herakraft.co/billing first.` |
| `capacity` from create/start | exit 8, `No capacity right now; try again in a few minutes. (We have been alerted.)` |
| Guest not running on `attach`/`open` | exit 5, the true state and its fix, e.g. `todo-app is stopped. Start it with \`repose start todo-app\`, or \`repose run\` in its checkout to start, sync and attach.` or `todo-app is in an error state: <reason>. …` (I-153) |
| Op fails (start, stop, destroy, resize, snapshot) | exit 1, `Could not <verb> <slug>: <reason> (<code>). <next step>`; destroy never prints `Destroyed` unless the op is done and the project is gone |
| Dirty remote tree | exit 6, message in 5.5 |
| Build or eval error | exit 10, Nix error block, fragment line marked |
| SSH cannot connect within 60 s after `running` | exit 1, `Guest is running but SSH did not answer in 60s. \`repose logs --kind console\` may show why.` |
| Gateway rejects certificate | re-issue once; if still rejected exit 1 with the gateway banner verbatim |
| `~/.ssh/config` unwritable, a read-only link, or its Include not effective | warning naming the file and the exact line to add (for a link, where); the command goes on with `ssh -F ~/.ssh/repose/config`; nothing partial written (write temp + rename), a link never replaced (I-151) |
| Browser cannot open | print the URL and continue |
| API unreachable | exit 1, `Cannot reach api.repose.herakraft.co: <err>`; never retried more than 3 times with backoff |
| Rate limited on `/certs` | reuse existing cert if valid, warn on stderr |
| Prompt given but agent not installed in guest | exit 1, `Agent 'pi' is not in this guest's config. Add it with \`repose config edit\`.` |
| Second prompt while agent window exists | new window `<agent>-2` (then `-3`, ..., the lowest free, I-253), stderr warning `Another claude window is open; two agents share one working tree. \`repose run --worktree\` gives the next one its own.` (no warning with `--worktree`) |

## 7. Testing

- Unit: remote normalisation table, project resolution order, ssh config
  rendering (golden files), certificate validity logic, exit codes, SSE
  parser, error block rendering.
- Integration against `internal/fakes/api`: full `run` sequence with a
  local sshd in a container standing in for the guest (Docker test fixture
  `test/guest-sshd/` with tmux and git, trusting the test CA). Covers dirty
  tree refusal, stash and discard, an unpushed commit arriving without a
  push, an empty guest checkout, a diverged guest branch left alone, the
  whole sync over one multiplexed connection, credential copy, prompt
  send, second window naming.
- Real: on the M2 host, a second person's laptop, every command, recorded
  in `docs/workstreams/STATUS.md`.
- Cross-platform: the GoReleaser matrix builds all four targets in CI;
  `install.sh` is run in CI on ubuntu and macos runners.

## 8. Rollback

The CLI is stateless on the platform. Rolling back a release is publishing
the previous tag; `install.sh --version X` installs a specific version. The
only files it owns on a laptop are under `~/.config/repose/` and
`~/.ssh/repose/` plus the one `Include` line; `repose logout --purge`
removes all of them including the `Include` line.

## 9. Checklist

- [ ] Every command and flag in 5.1 exists with that name. Evidence: `repose
      --help` tree pasted, diffed against 5.1. — open: STATUS 2026-09-20
      07-cli done-local line says the tree was diffed, but the `repose
      --help` tree is pasted nowhere; paste it (all subcommands) with the
      diff against §5.1, which has grown since (restore, cp, projects
      --destroyed)
- [x] Login works against the real Logto: device code is the default
      (DECISIONS I-101; the `repose-cli` Native application has no redirect
      URI, so the PKCE loopback of `--browser` cannot match it), with the
      application's App ID as client id (I-99). Evidence: the owner's
      transcript of `repose login` at the M2 gate (2026-09-20); the first
      three attempts failed with `oidc.invalid_client` (the application
      name was sent as client id), `oidc.invalid_redirect_uri` (PKCE
      against a device-flow application) and the api's `unauthenticated:
      invalid token` (the device-code and refresh grants carried no
      `resource`, so Logto minted an opaque token without the api audience,
      I-102), each fixed on main before the next. — closed: STATUS
      2026-09-20 "m2 e2e from the dev box" done line (device login on a
      build of main after I-99, I-101, I-102) and STATUS 2026-09-21
      m2-integration gate-done line (owner's laptop, released v0.1.4,
      device-code login; DECISIONS I-129)
- [ ] Tokens stored per `interfaces/cli-config.md`; on macOS the refresh
      token is in the keychain and absent from disk. Evidence: `cat
      credentials.json` on macOS shows no refresh token. — waits on: owner
      (a macOS laptop: `cat credentials.json` after `repose login` showing
      no refresh token; STATUS 2026-09-21 m2 gate-done line lists macOS
      keychain as not done)
- [x] Remote normalisation passes the table test with at least: ssh, https,
      https with `.git`, uppercase host, trailing slash, `ssh://git@host/`.
      Evidence: test file. — closed: `TestNormalizeRemote` in
      internal/cli/remote_test.go (ssh, https, .git, uppercase host,
      trailing slash, ssh://git@host/)
- [x] `ensureCert` reuses a valid certificate, refreshes an expiring one,
      re-issues when a new project is added. Evidence: unit test with a
      clock. — closed: internal/cli/cert_test.go
      `TestEnsureCertReusesValidCertificate` (no api call),
      `TestCertUsableFor` (clock at now+50m, inside the 30 m margin,
      refuses), `TestEnsureCertReissuesWhenProjectAdded`
- [x] `~/.ssh/config` gets exactly one `Include` line, first line, and no
      other line changes on repeated runs. Evidence: golden test with a
      pre-existing config. — closed: `TestEnsureIncludeLineOnceAndFirst` in
      internal/cli/cert_test.go (pre-existing `Host example.com` config,
      repeated runs, one first-line Include, content kept)
- [x] `ssh <slug>.repose` works from a plain terminal with no CLI involved
      after one `repose run`. Evidence: transcript. — closed: STATUS
      2026-09-20 "m2 e2e from the dev box" progress and done lines (`ssh
      <slug>.repose` from a plain terminal after one run, I-108); predates
      I-149's own key, whose laptop re-run is the I-149 row below
- [ ] Sync: dirty remote refused with exit 6; `--stash-remote` stashes and
      the stash is listed; `--discard-remote` discards; a commit not on
      origin arrives in the guest without a push or a prompt (I-150); an
      empty guest checkout gets the whole history; a diverged guest branch
      is left alone; untracked files respecting gitignore arrive; binary
      diff applies. Evidence: integration test output. — open: internal/cli
      has `TestSyncDirtyRemoteRefused` (exit 6), `TestSyncStashRemote`,
      `TestSyncDiscardRemote`, `TestSyncSendsAnUnpushedCommit`,
      `TestSyncIntoAnEmptyGuestRepo`,
      `TestSyncLeavesADivergedGuestBranchAlone`,
      `TestSyncAppliesDiffAndUntracked` (commit 7a8af85 output names the
      bundle ones); missing: a test that a gitignored file stays on the
      laptop and one that a binary diff applies (the untracked test is text
      only)
- [ ] One `repose run` makes one SSH connection and prompts for nothing;
      the user's `~/.ssh/id_*` are untouched (I-149). Evidence:
      `TestSyncOverAMultiplexedConnection` (the fake guest counts one
      connection), `TestEnsureCertMovesOffTheUsersKey`, and a transcript
      on a laptop whose `~/.ssh/id_ed25519` has a passphrase. — waits on:
      owner (laptop transcript with a passphrase-protected
      `~/.ssh/id_ed25519`, zero prompts; CHECKLIST-AUDIT.md "Waits on the owner");
      the two tests pass in commit 7a8af85's output
- [x] The owner's 2026-09-23 findings each have a test: destroy reports a
      failed op, `[y/N]` prompt, positional PROJECT, dir-cache poisoning,
      true state on attach, projects header and reasons, sentences for ssh
      errors (I-151..I-155). Evidence: `go test ./internal/cli/` output
      naming them. — closed: commit 7a8af85 message, `go test
      ./internal/cli/...` output naming TestDestroyReportsAFailedOp,
      TestDestroyConfirmationIsYesNo, TestPositionalProject,
      TestResolveProjectOrder, TestAttachToAnErroredGuestSaysError,
      TestNotRunningMessagesSayTheTruth, TestProjectsTable,
      TestSSHErrorsAreSentences
- [ ] `repose rm` returns within 2 s of the `[y/N]` with the restore
      command, `repose ls` shows `destroying`, and `repose restore
      NAME` brings the project back under its name (I-166, I-167).
      Evidence: `TestDestroyThenRestoreByName`,
      `TestProjectsShowAFailedDestroy`, and a laptop transcript with
      timings against the real api. — open: the tests exist
      (internal/cli/restore_test.go); STATUS 2026-09-23 03:40Z conductor
      line records live timings (destroy accepted 0.28 s, restore 12 s) but
      no CLI transcript showing the `[y/N]`, the printed restore command,
      `projects` showing `destroying` and `repose restore NAME`
- [x] Credential sync copies exactly the four rows and never the Claude,
      Gemini or SSH key files, even if present. Evidence: integration test
      that plants all of them and asserts. — closed:
      `TestSyncCredentialsCopiesExactlyTheFourRows` in
      internal/cli/run_integration_test.go (plants Claude, Gemini and SSH
      key files, asserts absent on the guest); STATUS 2026-09-20 14-security
      review-07 line
- [x] Prompt send: window named after the agent, second one `-2`, prompt
      arrives after the TUI is idle (not typed into a shell). Evidence:
      `tmux capture-pane` in the integration test shows the prompt inside
      the agent UI. — closed: `TestPromptSendAndSecondWindowNaming`
      (capture-pane shows each prompt in `cat` and `cat-2`) and
      `TestRunWithPromptSendsIntoTmuxWindow` in internal/cli; live prompt
      sends in the "Real-API evidence" below. I-253: the same test now
      opens `cat`, `cat-2`, `cat-3` and hands out a closed `cat-2` again;
      `TestPickWindowHasNoCap` (claude-10), `TestRunWorktree`,
      `TestRunWorktreeRefusals`, `TestRunWorktreeThenPlainRun`
- [x] Claude not-logged-in path attaches instead of sending. Evidence:
      integration test. — closed: `TestRunClaudeNotLoggedInAttachesInstead`
      in internal/cli/run_e2e_test.go (commit d61bf56)
- [ ] `attach` runs ssh under the input proxy on macOS and Linux, and
      execs ssh (the CLI process is replaced) with `REPOSE_INPUT_PROXY=0`
      (DECISIONS I-280, which replaces the old "no `repose` parent" item).
      Evidence: `ps -o pid,ppid,comm` from a second terminal during a
      session shows `ssh` under `repose`, and under the shell with
      `REPOSE_INPUT_PROXY=0`; a file dropped on a Mac terminal shows as
      `[Image #1]` in Claude Code. — open: needs a real laptop.
      `TestInputProxyDropReachesTheSession` covers the proxy against the
      fake guest
- [x] Build log SSE renders, error block matches 5.8 with the marked line.
      Evidence: golden test with a fake eval error. — closed:
      `TestRenderBuildErrorGolden` (fake `nodejs_25` eval error, marked line
      with caret) and `TestStreamBuildLogRendersLinesAndDoneState` in
      internal/cli/buildlog_test.go
- [ ] `open PORT` and `repose browser` work, browser opens, Ctrl-C leaves
      the desktop running (`open`), the forward outlives the CLI and
      `repose browser --stop` ends it. Evidence: transcript on a real guest;
      `TestBrowserCmdWatchesReusesAndStops` covers the fake guest. — waits on:
      owner (a real laptop with a browser; STATUS 2026-09-21 m2 gate-done
      line and archive/HANDOFF-2026-09.md list both as untested;
      CHECKLIST-AUDIT.md "Waits on the owner")
- [ ] `status`, `projects`, `secrets`, `config`, `snapshots`, `logs`,
      `destroy` each round-trip against the real API. Evidence: transcript.
      — open: status/projects/destroy are in the STATUS 2026-09-20 m2 e2e
      lines, secrets in 05 §9 (`ops/checks/secrets.sh`, 2026-09-20 23:50Z),
      config apply in 12 §9 (`ops/checks/menu.sh`), snapshots in
      `ops/checks/resilience.sh`; no `repose logs` run against the real api
      is recorded, and no one transcript covers the list
- [ ] Every row of the failure table in §6 is triggered and prints the
      exact message and exit code. Evidence: table of outputs in the PR. —
      open: no table of outputs exists; trigger each §6 row (tests like
      TestSSHErrorsAreSentences, TestExitCodeForLoginFailures cover some)
      and paste message plus exit code per row
- [ ] `--json` output on every read command is valid JSON with nothing else
      on stdout. Evidence: `repose status --json | jq .` in CI. — open: no
      CI job runs `repose status --json | jq .` (.github/workflows/ci.yml
      has no jq step); `TestStatusAndProjectsRoundTrip` checks status --json
      against the fake only
- [x] GoReleaser builds four targets; `install.sh` installs on ubuntu and
      macos CI runners and `repose version` prints the tag. Evidence: CI
      run link. — closed: CI run 35875626476 on 264e6c2
      (https://github.com/Heracraft/factory/actions/runs/35875626476) jobs
      "cli GoReleaser matrix build" (asserts the four dist binaries),
      "install.sh (ubuntu-latest)", "install.sh (macos-latest)" (`repose
      version` greps the tag)
- [x] `nix run .#repose` works. Evidence: CI job. — closed: CI run
      35875626476 on 264e6c2
      (https://github.com/Heracraft/factory/actions/runs/35875626476) job
      "nix run .#repose"
- [x] Completion scripts generate and load without errors in bash, zsh,
      fish. Evidence: CI job sourcing each. — closed: CI run 35875626476 on
      264e6c2
      (https://github.com/Heracraft/factory/actions/runs/35875626476) job
      "shell completions load in bash, zsh, fish"
- [x] `internal/fakes/api` covers every route the CLI calls. Evidence: the
      fake's route table diffed against `interfaces/api.md`. — closed:
      `TestRoutesMatchDoc` in internal/fakes/api/api_test.go (fake route
      table against the table parsed from api.md, both directions)
- [ ] No secret, token, certificate or prompt text is ever logged at `-v`.
      Evidence: reviewer grepped log calls; test asserts on captured
      output. — open: no reviewer grep of log calls and no test capturing
      `-v` output is recorded; `-v` now prints op error detail
      (internal/cli/run.go `opFailed`)
- [x] `features/run-and-attach.md`, `features/sync-at-launch.md`,
      `features/ports-and-previews.md` match the built behaviour. Evidence:
      re-read and diffed by the implementer. — closed: commit d61bf56
      (implementer re-read the three and removed what was never built);
      later changes updated them in their own commits (e.g. 7a8af85 for
      I-149/I-150)
- [x] `ops/RUNBOOK.md` has entries for: user cannot log in, certificate
      rejected, SSH timeout after running. Evidence: entries exist. —
      closed: ops/RUNBOOK.md "CLI: user cannot log in", "CLI: certificate
      rejected", "CLI: SSH timeout after running" (commit d61bf56)
- [x] A dropped attach attaches again and `repose open` reconnects
      (I-469). Evidence: `TestAttachLoopReattachesAfterADrop` (real ssh on
      a pty, a proxy cuts the connection and refuses for 2 s, the program
      in tmux gets the keys typed before and after), `TestReattach*`,
      `TestForwardOverTheControlMaster` (forward back on a new master, no
      second message). — closed 2026-10-04 (edge-zero-downtime)

### Real-API evidence (M2, 2026-09-20/21)

- Login against the real Logto: device code (I-101) with the App ID
  (I-99) and the api audience on the device grant (I-102); the owner's
  laptop and the conductor's box both logged in on v0.1.3/v0.1.4.
- `install.sh`: the owner installed v0.1.4 with it on 2026-09-21 (I-98
  release).
- `repose run` end to end on the real api and host-01: the conductor's
  runs (login, notify, create, build, boot, sync over HTTPS and over the
  forwarded agent with an SSH origin, stop with snapshot 22 s, start, the
  `<slug>.repose` alias from a plain terminal, sessions) and the owner's
  two projects (`nuru-playground`, `age-calculator`: create op to running
  in about 60 s each with the base closure cached).
- Dirty-tree refusal, credential sync (`Credentials: gh, git` printed),
  prompt send and the not-logged-in Claude path: the conductor's runs.
- Gate findings fixed on the day: I-99, I-101, I-102, I-104, I-106, I-107,
  I-108, I-109, I-111, I-114, I-127, I-128.
- Not done on a real laptop: `open PORT`, `open --desktop`, macOS keychain
  storage, the second person's run (waived for M2 by I-129).
