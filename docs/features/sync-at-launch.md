# Sync at launch

Git is the exchange channel between the laptop and the guest. The first
`repose run` for a machine adds one thing on top: the laptop's tree,
uncommitted work included, is carried over into the new machine, so the
agent starts from what the user actually sees. Every later run attaches to
the machine as it is; the laptop's later work goes over only with `repose
sync` (DECISIONS I-367). Work comes back only through git (`git fetch
repose`, I-272). Nothing syncs continuously.

## What the user sees

The first run, into a new machine:

```
$ repose run
Connected to todo-app (large)
Synced: 3 modified, 1 untracked
```

A later run, with work on the laptop the machine never took (I-367); the
checkout is left alone and the run attaches:

```
$ repose run
Connected to todo-app (large)
Not synced: your laptop has work the machine doesn't (2 modified, 1 commit). `repose sync` sends it.
```

`repose sync` sends it. Local commits not on the remote yet travel anyway,
and nothing is pushed:

```
$ repose sync
Connected to todo-app (large)
Synced: 0 modified, 0 untracked (2 new commits)
```

An agent committed on the guest's branch, left two files uncommitted,
and the laptop has not pulled (DECISIONS I-573, I-574):

```
$ repose sync
Connected to todo-app (large)
Synced: 1 modified, 0 untracked (1 new commit), merged with the machine's main; kept the machine's changes to 2 files
```

The same, when the two sides' commits conflict:

```
$ repose sync
Connected to todo-app (large)
Synced: 1 modified, 0 untracked (1 new commit)
The machine's main has commits that could not be merged with yours, so it was left as it is and the machine is on 4f2a9c1, detached. `git fetch repose` brings that branch here.
```

The laptop checkout has a fetch-only `repose` remote for the guest's
checkout, so that fetch is plain git with no push to origin (DECISIONS
I-272; /docs `sync.md` "Getting work back").

Guest changed since the last sync, laptop has nothing new (DECISIONS
I-248):

```
$ repose sync
Nothing new to sync. The machine has changes your laptop doesn't have (27 files).
```

Guest changed files that the laptop's new work changes too (DECISIONS
I-573):

```
$ repose sync
Not synced: the machine changed 11 files that your laptop changed too:
  src/auth.ts
  src/routes/login.ts
  ...(eight names in all)
  and 3 more
`repose sync --stash-remote` moves the machine's changes to its git stash first.
```

The guest's checkout has a git operation of its own in progress
(DECISIONS I-573):

```
$ repose sync
Not synced: the machine's checkout is in the middle of a git rebase. Finish or abort it there, or run `repose sync --discard-remote` to end it and move the machine's changes to its git stash.
```

A Ctrl-C after the sync created the project and before it connected
(DECISIONS I-575); a Ctrl-C with the create request still out looks the
name up for two seconds and prints the same line, or ``Interrupted.
job-search may have been created; `repose ls` shows it.``:

```
$ repose sync
✓ Created job-search (large)  0.6s
^C
Interrupted. job-search was created and stays on your account; `repose rm job-search` removes it.
```

## Behaviour that must hold

- The checkout syncs on `repose sync` (I-302), and on `repose run` only
  when the guest's checkout has no commit yet: a new machine, or one made
  with `--no-sync` (I-367). A run into a guest with a commit leaves the
  checkout alone whatever either side has, sends the logins and carry,
  and, when the laptop has work the guest never took (a sync key that
  differs, with a modified or untracked file or a commit the guest lacks
  to show for it), prints the "Not synced" line above. Never on
  `attach`, `start`, or any other command. `run --stash-remote` and
  `--discard-remote` exit 2 naming `repose sync` with the same flag.
  Everything below describes `repose sync` and a run's first sync.
- The guest's own changes refuse a sync only where the sync would write
  (DECISIONS I-573, which replaces the whole-tree refusal of R3-12 and
  I-248). When the laptop has nothing new since the sync the guest last
  took (its sync key equals the guest's `.git/repose-synced-key` and the
  guest has every commit it would send, which the guest shows by still
  having the commits that sync recorded under its key, DECISIONS I-284,
  even after it pulled past every commit the laptop knows), there is
  nothing to write: the checkout is left alone, only the logins and carry
  go, and the sync ends with the one-line notice above, whose count is
  the guest's own changed files (every untracked file on its own) less
  the paths the last sync's per-path record still matches; with none of
  those and the guest's `HEAD` and branch as that sync left them, the
  line is "Nothing new to sync: the machine already has this checkout."
  The same holds
  when the guest's tree is clean but it moved on (an agent's commits,
  another branch): no detached checkout of an older laptop commit.
  Otherwise the apply, before it touches anything (and before the logins
  and carry), lists the paths the sync writes: the ones the checkout to
  its target (the laptop's commit, or the merge tree below) changes
  against the guest's `HEAD` (`git ls-tree` of the target in an empty
  repository), both sides of the laptop's staged and unstaged diffs, its
  untracked files and each bundled submodule. It lists the guest's own
  changes with `git status --porcelain=v1 -z -uall`: every dirty or
  untracked file (both paths of a rename), leaving out the last sync's own
  (below). A guest path that equals a written path, sits under one, or
  holds one below it is an overlap: the apply exits 3 naming each, and the
  CLI prints the refusal above (eight names, then a count) and exits 6.
  With no overlap the sync goes on, the guest's other changes stay as
  they are (git's checkout and merge carry unrelated edits), and the
  summary line ends "kept the machine's changes to N files".
  Before the overlap check, a merge, rebase, `git am`, cherry-pick,
  revert or bisect in progress in the guest's checkout refuses the sync
  (exit 6, the refusal above naming the operation): a checkout would drop
  its state and a stash cannot hold it, so `--stash-remote` refuses too.
  `--stash-remote` runs `git stash push -u -m "repose sync --stash-remote"`
  in the guest first; `--discard-remote` ends any such operation where
  `HEAD` is, then stashes the same way under `repose sync
  --discard-remote` (DECISIONS I-618); with either there is no overlap
  check, and the summary ends "stashed the machine's changes to N files
  (git stash <commit>)". Neither asks for confirmation; the
  flag itself is the confirmation.
- A run with nothing new applies nothing (DECISIONS I-224): when the
  laptop would send exactly what the last completed sync sent (the same
  commit, branch, diff and untracked files) and the guest's tree is still
  what that sync left, the apply is skipped, nothing is stashed, and a
  run that attaches prints no summary line; `repose sync` and
  `run --no-attach` print "Nothing new to sync: the machine already has
  this checkout." (DECISIONS I-303).
- The laptop's own changes are not an agent's (DECISIONS I-210, I-573).
  A sync that carried a modified or untracked file leaves the guest's tree
  dirty by construction, so the apply records what it left twice: per
  path, in `.git/repose-synced-paths` (the `HEAD` it left, then each of
  the laptop's paths with a hash of its content: a blob hash, a link
  target, a submodule's `HEAD` and tree, or none), and, when no guest
  change was kept, as a fingerprint of the whole tree (`HEAD` plus the
  tree `git add -A` would write, in the checkout's `.git/repose-synced`).
  A path whose content still matches the record under the same `HEAD` is
  the last sync's own: it never overlaps, and the apply stashes it alone
  (`git stash push -u -m "repose run: last sync" --pathspec-from-file`)
  before laying the laptop's current work down. When the next probe finds
  the tree dirty and the fingerprint unchanged, the run goes on: the apply
  stashes those changes (`git stash push -u -m "repose run: last sync"`, so nothing
  misjudged is lost; only the newest 10 such stashes are kept, and the
  user's own stashes and `--stash-remote`'s are never dropped), lays down the laptop's current ones, and the summary
  line ends "the last sync's changes stashed in the guest". An edit to a
  synced file, a new file, a commit, or a change inside a submodule
  (each checked-out submodule adds its own `HEAD` and tree to the
  fingerprint, DECISIONS I-263) changes the fingerprint; the apply then stashes only the paths the
  record still matches, and an edited synced file the laptop still sends
  refuses as above. The overlap check runs in the apply itself, so a
  change made between the probe and the apply counts. A tracked file an
  agent edits after the check makes the checkout or `git apply` fail, so
  it is not overwritten; a new file an agent writes at one of the
  laptop's untracked paths after the check (the logins are copied in
  between) is overwritten by the untracked tar. `git stash push -u` cleans
  the untracked files after recording them, so a file written in that
  instant is lost (git's own behaviour). The checks force
  `status.showUntrackedFiles=normal` and `submodule.recurse=false`, so a
  carried laptop setting cannot hide a file or reach into a submodule.
  Both ride the two existing ssh round trips. A run from a second laptop
  sees the first laptop's synced changes as the last sync's own too, and
  stashes them in the guest the same way before laying down its own.
- The guest never fetches from origin during a sync: it has no
  credentials for a private repository, nor for a public one behind the
  SSH `origin` guestd sets (DECISIONS I-150). The laptop sends the commits
  itself. The first ssh (the same one that reads the dirty list) reports
  every commit a ref in the guest points at; the laptop `git bundle
  create`s `HEAD` and its own `origin/<branch>` minus what the guest has
  (the whole history the first time, nothing when the guest is current),
  and the second ssh fetches from that bundle. Nothing is pushed, and the
  old "Push now?" prompt is gone: an unpushed commit simply travels.
- The guest's `origin/<branch>` is moved to where the laptop last saw
  origin (forward only), and `origin` is added if missing, so the agent's
  `git status` and `git push` behave as they would on the laptop.
- Checkout: the laptop's branch is created in the guest, or
  fast-forwarded when the guest's copy is behind. When the guest's branch
  has commits the laptop does not (an agent committed and nobody pulled),
  the laptop's commit is merged into it, after switching the checkout to
  it when the guest is on another branch or detached (the overlap check
  counts the paths that switch changes; a switch git still refuses falls
  back to the detached checkout; the other branch keeps its commits)
  (DECISIONS I-574) when `git merge-tree --write-tree` finds no conflict,
  the guest's commits since the merge base leave every one of the
  laptop's own paths alone, and git has a committer identity, the
  laptop's `user.name` and `user.email` (sent in the apply's tar, set
  for the identity check and the merge alone): `git merge
  --no-ff --no-edit --no-verify --no-autostash --no-verify-signatures
  --no-gpg-sign -m "Merge the laptop's <branch> (repose sync)"`, and the
  summary line says "merged with the machine's <branch>". Otherwise the
  branch is left exactly where it is, the laptop's commit is checked out
  detached, and a warning says so; an agent's work is never moved off its
  branch, and the agent guide tells the agent what a detached checkout
  after a sync means. A detached `HEAD` on the laptop is checked out
  detached.
- Two diffs, not a tar, carry tracked changes: `git diff --cached
  --binary` (what is staged) applied with `git apply --index`, then
  `git diff --binary` (what is not) applied with `git apply`, so the
  guest's `git status` shows the same staged and unstaged files as the
  laptop's (I-258). Only
  the untracked files (`git ls-files --others --exclude-standard`,
  filtered by `sync.exclude` in `config.toml`) travel as a tar, extracted
  in the checkout. Bundle, diff and untracked tar go as one payload in one
  ssh; nothing writes a custom sync helper into the guest, and every
  command there is stock git and tar.
- Submodules travel like the superproject (DECISIONS I-263). Every
  submodule the laptop has checked out, nested ones included, arrives
  checked out at the laptop's submodule `HEAD` (which may differ from the
  commit the superproject records, and then `git status` says so on both
  sides). Its commits go as a bundle of what the guest's copy lacks, so a
  submodule commit only the laptop has travels and a private submodule
  remote needs nothing on the guest; its staged and unstaged diffs,
  untracked files (inside the same size limits and skipped directories)
  and gitignored `.env` files follow the superproject's rules. A staged
  submodule change, addition or removal is staged in the guest too. The
  guest registers each one (`git submodule init` and `sync`), so its
  `origin` is the URL `.gitmodules` names. `--stash-remote`,
  `--discard-remote` and the last-sync stash act inside every submodule
  as well. A submodule the laptop never checked out stays empty in the
  guest. A shallow submodule cannot be bundled: the guest runs `git
  submodule update --init` for it itself (gh's login covers github.com);
  when that fails the run goes on and says `Submodule <path> is empty on
  the machine: ...`, and edits inside a shallow submodule are not sent,
  with a warning to `git -C <path> fetch --unshallow`.
- An empty guest checkout (a bare `git init`, or no directory at all) is
  filled the same way: the first bundle is the full history.
- A laptop directory that is not a git repository, has no commit yet, or
  is a shallow clone gets one sentence saying what to run
  (`git init && git add -A && git commit -m init`, `git fetch
  --unshallow`) or `--no-sync`, exit 2, before anything is created or
  started (DECISIONS I-353): `run` and `sync` check the checkout beside
  the api's first read. For `run`, a directory that is not a git
  repository skips the sync instead (`Not a git repository, so nothing was
  synced.`), temporary or not (I-358); `sync` there still refuses.
- Size: a file over 100 MB is skipped with a warning rather than sent,
  because an accidental video is the usual cause and the user wants to
  know; past 500 MB of untracked files in one sync the rest is skipped
  with one warning (DECISIONS I-194).
- Dependency and cache directories never travel, gitignored or not:
  `node_modules`, `.pnpm-store`, `.venv`, `venv`, `__pycache__`, `.next`,
  `.turbo`, `.svelte-kit` and the like, wherever they sit in the tree. The
  sync names each one it left behind once (`Not sent: cms/node_modules`);
  the agent installs dependencies in the guest, where they are built for
  the guest's platform. A `sync.exclude` pattern excludes a directory at
  any depth (`dist` covers `web/dist/...`).
- Symlinks travel as symlinks, never followed; directories and special
  files in the untracked list are skipped, and one unreadable file does
  not stop the rest.
- Files ignored by gitignore do not travel, with one exception (DECISIONS
  I-197, which reverses this document's earlier "never"): gitignored
  `.env` and `.env.*` files at any depth outside the dependency
  directories above, up to 1 MB each, go laptop to guest in the sync's
  own apply ssh, after the checkout (so the guest's `.gitignore` already
  covers them), mode 0600, never through the api. A guest copy newer than
  the laptop's is kept, and the CLI says so once: `Kept the guest's
  apps/web/.env: it is newer than the laptop's.` An unchanged set is not
  sent again (marker `env`, I-206). They sit on the guest disk and so are
  in snapshots, like the gh token and the code. Named secrets
  (secrets.md) remain the path for values that must change without a
  laptop. Ignored directories are listed collapsed (`git ls-files
  --others --ignored --exclude-standard --directory`), so a `.env`
  inside a wholly ignored directory does not travel. Contents never reach
  a log line.
- The summary line prints whenever the apply ran (I-303),
  `Synced: <n> modified, <m> untracked`, even when both are zero, followed by `, <e> env files` when .env files
  were written and `(<k> new commits)` when commits travelled.
- The guest's checkout is found by the rule in guest-conventions.md "The
  checkout". A machine with none gets it from the sync's probe, at
  `/home/dev/<laptop folder>` (made safe; the slug when that name is
  taken), recorded in `~/.repose/checkout`, `git init`ed, with `origin`
  added by the apply (DECISIONS I-368). The run then prints `Checkout:
  ~/<name> on the machine`. A machine set up before I-368 keeps
  `/home/dev/<slug>`.
- The first sync of a large GitHub repository clones in the guest
  (DECISIONS I-203). When the guest has no commits yet, the remote is on
  github.com and the laptop's `git count-objects -v` reports a
  `size-pack` of 20 MB or more (the starting threshold, to be set from
  measurement), the guest fetches every branch and tag from
  `https://github.com/<owner>/<repo>.git` itself, after the credentials
  step (so a private repository uses gh's login when it travelled; a
  public one needs none). The laptop then bundles only the commits GitHub
  lacks, and the diff, untracked and `.env` files follow as always. The
  summary line ends `, history cloned from github.com`. A clone that
  fails for any reason falls back to the full bundle, with `The guest
  could not clone from GitHub (<git's reason>), so the history was sent
  from your laptop instead.`; the run never fails for it. A full fetch,
  never a partial clone: lazily fetched blobs fail later once a token
  expires. Later syncs never clone.
- A project made with `repose run NAME` in a directory with no git remote syncs the
  same way, without an `origin` or remote-tracking refs: its real commits
  travel, so a file deleted and committed on the laptop is deleted in the
  guest too (DECISIONS I-150, replacing I-138's whole-tree commit).
- Credentials (secrets.md) are copied before the git steps, in one ssh.
  When gh's login travelled and the remote is on github.com, the guest's
  git is told to push to github over HTTPS with gh as the credential
  helper (`guest-conventions.md`), so an agent's `git push` works without
  the laptop's SSH keys.
- All of this rides the command's one multiplexed SSH connection (I-149):
  three round trips in all: the probe (which also returns the carry's
  markers), the credentials and carry, and the apply (with the .env
  files).

Tools (DECISIONS I-221, I-222):

- `repose run` makes the guest have the tools the laptop has and the
  project runs. It lists the laptop's global tools by reading the
  managers' install directories, never by running them: npm's global
  prefix (`$NPM_CONFIG_PREFIX`, `prefix=` in `~/.npmrc`, else the
  directory above `node`'s), pnpm's (`$PNPM_HOME/global`), bun's
  (`~/.bun/install/global`), Go binaries in `$GOBIN`, `$GOPATH/bin` or
  `~/go/bin` (package path and version from their build info), cargo's
  `~/.cargo/.crates2.json` (registry crates only), `uv tool` and `pipx`
  venvs. It adds the commands the checkout's own scripts run: package.json
  scripts at the root and in every workspace package, Makefile and
  justfile recipes, Procfile, `.air.toml` (air) and compose files
  (docker). A command is left out when the guest base has it, when a
  dependency of that workspace or the root provides it (by name, a known
  package-to-command table, or `node_modules/.bin`), when a workspace
  package's `bin` or a pyproject script defines it, or when it is the
  name of another script. `npx`, `pnpm dlx` and `uv run` fetch what they
  run and name nothing.
- What travels is names, managers, versions and Go package paths, nothing
  else: no laptop path, no config value. It rides the carry (marker
  `tools`), so an unchanged list costs nothing, and the laptop's reading
  runs while the probe's ssh is in flight (about 0.5 ms measured on the dev box
  against nuru-playground; the budget is 20 ms).
- The guest answers in milliseconds with what it lacks, and the CLI says
  it once: `Installing 3 of your tools in the background: air, portless,
  typescript`. The installs run after that in a low-priority user unit
  (`repose-tools-carry`); nothing in `run` waits for them. Each tool is
  installed from nixpkgs when a package there has `bin/<command>` (a
  prebuilt binary, pinned into the store overlay with the rest of dev's
  profile), else with the laptop's manager into a directory on the login
  PATH (`npm i -g`, `go install` with `GOBIN=~/.local/bin`, `cargo
  install --root ~/.local`, `uv tool install`). A command only a project
  script names is installed from nixpkgs or, for the few npm-only ones
  (`portless`), from npm. While a tool installs, its commands are listed
  in `$XDG_RUNTIME_DIR/repose-installing`, so a shell that runs one early
  says it is on its way instead of "command not found".
- A project that pins a node major (`.nvmrc`, `.node-version`,
  `.tool-versions`, `volta.node`, or an `engines.node` of one major such
  as `22`, `22.x`, `^22.1`) the guest does not have gets `nodejs_<major>`
  in dev's nix profile, but only when that makes it the `node` of new
  shells, which it checks. When a `node` earlier on PATH would still win,
  nothing is installed and the CLI says to run `repose config add
  nodejs_<major>`. Go, Rust and Python version files are left to
  `GOTOOLCHAIN`, rustup and uv, which fetch the version themselves.
- A tool that could not be installed is named once, at the next `run`:
  `Could not install air: <the installer's last line>`. The full output
  is in the guest's `~/.repose/tools-install.log`. A failed tool is not
  retried until the laptop's entry for it changes.
- `repose scan [DIR]` prints the same list and why, without installing
  anything or contacting the guest, to check a project before a run.

Back to the laptop:

- `repose cp` copies a file either way when a log or a trace is needed
  for triage (DECISIONS I-201): `repose cp :logs/x.log .` from the
  checkout, `repose cp izma:/tmp/trace.json .` from anywhere, `repose cp
  -r ./fixtures :test/fixtures` the other way. It is scp over the
  project's connection with the project resolved as every other command
  resolves it, and guest paths relative to the checkout.
- There is no reverse sync. The user pulls. `repose status` shows the
  guest's branch, HEAD, and whether the tree is dirty so the user knows
  something is waiting to be committed.

## Depends on

Workstreams 07 (cli: the diff/tar builder and the git commands, all run
over the SSH session, never vsock), 04/02 (guestd's `SetupProject`
guarantees the guest checkout exists before the CLI's first sync).

## Deferred

`repose sync --watch` continuous sync (DECISIONS R1-3). Reverse sync of the
guest's uncommitted changes to the laptop. Syncing other gitignored files
by explicit allowlist.
