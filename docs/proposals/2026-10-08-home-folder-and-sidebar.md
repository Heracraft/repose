# The home folder, loose machines and herdr's sidebar (proposal, 2026-10-08)

**Status: proposal. Nothing here is decided and no code is written.** It
changes behaviour only through `DECISIONS.md` entries that amend I-152,
I-358, I-575 and I-510. The public docs change in the same commit as the
code.

## The owner's notes

1. herdr has no way to take a running machine out of the laptop's
   sidebar for a while. With tmux you detach; with herdr every machine
   stays connected.
2. `repose run` in the home folder linked the folder to the first
   project made there (`obsidian`). Every later plain command in `~` now
   means `obsidian`: `run` lands on it, and `repose rm` would destroy it.
   The owner wants `~` to be a place to start and attach loose machines
   from: `repose run` with a name, no sync, several of them, and no
   project quietly tied to the folder the terminal opens in.

## What the code does today

From `internal/cli` at c6c04167.

- **How `~` got linked.** A plain `run` outside a git repository creates
  a project named after the folder and writes `by_dir[~]` (I-358). A
  `run --name obsidian` there writes it too: the run's first sync into a
  named project with no remote, from a folder with no entry, links the
  folder (I-575, `linkExplicitSync`, `run.go:567`). Either way `~` ends up
  in `projects.json` as `obsidian`'s folder.
- **What the link drives.** `resolveProject` (`project.go:222`) reads
  `by_dir[git root or cwd]` for every command without an explicit
  project: `run`, `attach`, `sync`, `rm`, `stop`, `start`, `status`,
  `logs`, `secrets`, `config`, `exec`, `ssh`, `code`, `cp`, `fork`,
  `resize`, `snapshots`, `keep`. Only `run` and `sync` say why (`Using
  obsidian, the machine last made in this directory.`). The rest act on
  it in silence.
- **`repose rm` in `~`** asks `Destroy obsidian? A final snapshot is kept
  for 30 days. [y/N]`. It names the project, never the reason it picked
  it. `-y` skips even that.
- **`rm` leaves the link.** It removes the herdr entry and the `repose`
  git remote, and leaves `by_dir`. While the destroy runs, a plain `run`
  in `~` lands on the dying project and recreates it under the same name
  (`startOver`, I-301).
- **No way to unlink.** No command forgets a `by_dir` entry; editing
  `~/.config/repose/projects.json` is the only route.
- **Nothing treats `~` as special.** `os.UserHomeDir` appears only for
  the config directory and the carry. A home folder that is a git
  repository (a dotfiles repo) takes the checkout path: `run` would sync
  it and add a `repose` remote to it.
- **`run NAME` does not exist.** Every positional word of `run` is the
  prompt; a one-word prompt equal to a slug is refused with a hint
  (`run.go:708`). The name goes through `--name`.
- **`--temp` already ignores the folder**: always a new `tmp-xxxx`, no
  cache entry, and outside a repository no sync. It always runs tmux, so
  a herdr user's temporary machines never reach the sidebar (I-510).
- **Doc drift found on the way.** I-358 says `repose sync` outside a
  repository still refuses; the code skips the sync and goes on
  (`run.go:149`), for `sync` as for `run`.

## Part 1: the home folder

### What counts as the home folder

`os.UserHomeDir()` with symlinks resolved, and every folder above it
(`/Users`, `/`, `C:\Users`). Subfolders are ordinary folders:
`~/Downloads/job search` keeps I-358's behaviour, which the owner asked
for. Whether `~` is a git repository does not matter; a dotfiles repo in
`~` is still the home folder.

Rejected: a list of "not a project" folders (`~/Desktop`, `~/Downloads`).
A list guesses, and I-358 shows a real project living in `~/Downloads`.

Open: a config key for more such folders (`loose_dirs = ["~/scratch"]`).
Not proposed until someone asks.

### What changes in it

1. **The folder is never linked.** No `by_dir` entry is written for it,
   by `run` (I-358) or by a sync into a named project (I-575).
2. **The folder resolves to nothing.** `resolveProject` skips `by_dir`
   and the remote lookup there. An entry an older CLI wrote for it is
   ignored and deleted from `projects.json`, with no line: the error the
   next command prints says what to do.
3. **Nothing is synced from it.** A `run` there skips the sync, as
   outside a repository today, and carries the logins, settings and
   tools as `--no-sync` does. No `repose` git remote is added. A `repose
   sync` there exits 2. `run --on PROJECT` there exits 2.
4. **Commands that need a project ask for one by name.** `rm`, `stop`,
   `status` and the rest exit 2 with the error they print today when no
   project matches, so `repose rm` in `~` can no longer destroy anything
   without a name.
5. **`repose run --name NAME` in `~`** attaches to NAME, or creates it
   with no checkout and attaches. Run it again with another name for a
   second machine. Nothing is written for the folder, so `~` stays free.
   The machine works in `/home/dev` (I-368) until something syncs a
   checkout into it.
6. **A plain `repose run` in `~`** has no name to go on. See decision A.

### Decisions for the owner

**A. Plain `repose run` in the home folder.**

- A1 (recommended): exit 2 with one line, `Your home folder is not a
  checkout. Name the machine: repose run --name NAME, or repose run
  --temp.` Cheap, scriptable, and the line teaches the two forms once.
- A2: in a terminal, ask `Name for a new machine:` and go on with it;
  without a terminal, A1's exit. Closest to the owner's "are you sure",
  and one more prompt shape in the CLI.
- A3: make a temporary machine, as `--temp` would. Fast, and surprising
  the day the owner wanted to keep the work.

**B. A positional name for `run`.** `repose run obsidian` reads better
than `repose run --name obsidian`, but `run`'s positional words are the
prompt (`repose run fix the tests`). Recommended: keep `--name` for
creating. For a machine that exists, `repose attach NAME` already works
from anywhere and starts it when stopped (`attach` goes through
`runRun`). Rejected: a positional name only in `~`, since the same words would
mean a prompt one folder down.

**C. `repose rm` elsewhere.** Point 4 fixes `~`. In other folders the
question still names the project without the reason. Recommended:
when the project came from the folder (no PROJECT, `--project` or
`REPOSE_PROJECT`), the question says so: `Destroy job-search, this
folder's machine? A final snapshot is kept for 30 days. [y/N]`. And
`-y` with a project found only through the folder exits 2 asking for the
name, so a script cannot destroy the wrong machine from the wrong
directory. That second half changes a documented contract and needs its
own call.

**D. Unlinking any folder.** With point 1 nobody needs to unlink `~`.
Other folders can still be linked by mistake (the I-575 case).
Recommended: `repose rm` removes every `by_dir` entry for the destroyed
project (a bug fix today), and no new command. A `repose unlink` would
be a new top-level noun for a rare need ([[cli-surface-taste]]: fold
first).

### Edge cases this has to get right

- `~` is a dotfiles git repository: no sync, no `repose` remote, no
  `by_dir`, no lookup by its remote.
- The terminal opens somewhere other than `~` (some setups open in `/`
  or a workspace folder): `/` is covered as an ancestor; a workspace
  folder is not, and is a normal folder.
- `REPOSE_PROJECT` set in `~`: explicit, works as today.
- `cp :path` (this checkout's machine) in `~`: exits 2 like the rest;
  `cp NAME:path` works.
- `exec` in `~`: `repose exec obsidian -- ls` already takes a slug as the
  first word.
- The early probe and the fast attach (`cachedGuess`) read the cache:
  both must skip the home folder, or a fast attach still lands on the old
  link before the slow path's check.
- The laptop is Windows: `%USERPROFILE%` and `C:\`.
- The `Using X, the machine last made in this directory.` line never
  prints in `~`, since nothing there is linked.
- `repose scan` (reads a folder) is unaffected.

### Where it lands

`internal/cli/project.go` (resolution), `run.go` (create, link, sync
skip, `--on`), `fastpath.go`, `lifecycle.go` (`rm` question, link
cleanup), `config.go` (drop the home entry). Contract:
`docs/interfaces/cli-config.md` (`by_dir`), `docs/workstreams/07-cli.md`
§5.3, `docs/features/projects.md`. Public: `cli.md` "Which project", the
`run` and `rm` sections, `run-and-attach.md`, `sync.md`. One decision
entry amending I-152, I-358 and I-575, and fixing I-358's line about
`sync` to match the code.

## Part 2: taking a machine out of herdr's sidebar

### What exists

herdr 0.9.3 has `herdr machine disable <profile-id>` and `herdr machine
enable <profile-id>`. Disabling closes that machine's connection and
leaves its sessions and agents running, which is tmux's detach for one
machine. repose already leaves a disabled entry disabled (I-510), and
`run-and-attach.md` says to disable an entry when you are done for the
day. `herdr machine remove` works too, but the next `run`, `attach` or
`sync` puts the entry back while the machine runs with herdr.

The gaps:

- herdr's UI has no disable action; the command needs the profile id
  from `herdr machine list`, not the label.
- `repose attach NAME` on a disabled entry does not enable it; it opens
  `herdr --remote` or `ssh` in a separate client (`chooseHerdrPath`,
  `mux_herdr.go:495`). Turning it back on is another herdr command.
- A connected entry counts as someone at the machine (I-511), so a
  machine left in the sidebar never gets the idle notice. Disconnecting
  is how you let it go idle.

### Decision E

- E1 (recommended): `repose attach NAME` from a laptop herdr pane
  enables a disabled entry for NAME and goes to it in the sidebar
  (attaching is asking for it back). Disconnecting stays herdr's: the docs
  give the one line that disables by label, `herdr machine disable
  "$(herdr machine list --json | jq -r '.[] | select(.label=="NAME") |
  .id')"`, and repose asks herdr upstream for a sidebar action.
- E2: E1, plus a repose verb for disconnecting. No placement reads
  well: `stop` means the machine stops, and `attach --off` or `repose
  attach detach` contradict themselves. Listed so the owner can name one
  if the herdr line is not enough.
- E3: a repose herdr plugin with a "Disconnect" action in herdr's command
  palette. herdr plugins can add actions; it is the right place in the
  UI, and one more thing repose ships and keeps working across herdr
  releases.

## Not in this proposal

- Temporary machines on herdr (`--temp` runs tmux only, I-510). The
  owner's "just get a machine" case may want them in the sidebar; that
  reverses a recorded choice and gets its own entry.
- `--temp` decoupled from the folder inside a checkout (the 2026-09-29
  proposal). The home-folder rules above do not depend on it.
