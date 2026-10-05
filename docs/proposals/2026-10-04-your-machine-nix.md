# Your machine in a .nix file (proposal, 2026-10-04)

**Status: proposal.** The owner set the direction on 2026-10-04: a `.nix`
file, personal or per project, configures every new machine without being
asked, it is stored on the account, it is applied automatically, and it
takes precedence over guessing from the laptop. Each stage below ships
through its own `DECISIONS.md` entry. Builds on
`2026-10-04-flake-in-root.md` (items 2 and 3 there are stage 0 here) and
`reports/Flake in root DevX.md`.

## What a user has today

| Mechanism | Scope | Applied |
|---|---|---|
| Laptop tool scan (I-221, I-222) | global npm/go/cargo/uv/pipx tools, commands the scripts call | every `run`, in the background, from nixpkgs or the laptop's package manager |
| `repose config` and `repose.nix` | one project | only on `repose config apply`, `add`, `edit` or the dashboard |
| `flake.nix` or `.envrc` dev shell | one folder | agents and `repose exec`; not the user's own shells |

Gaps against the direction:

1. Nothing personal. A new project starts from `DefaultFragment`
   (`internal/api/http/projects.go`). Shell, aliases, prompt and dotfiles
   never arrive; the tool scan guesses packages only.
2. A `repose.nix` in the repository is not read by `run`. The machine
   boots on the base, and the user has to know to apply it.
3. No precedence. The tool scan runs whatever a `.nix` file declares.
4. The agent's shell and the user's differ: a flake's tools reach agents
   only.
5. `home.sessionVariables` does nothing: nothing on the guest sources
   `hm-session-vars.sh`, and the `/docs/config` example uses it.

## The model

Three layers, each a file the user owns:

1. **Personal**: `machine.nix`, a home-manager module for `dev`, stored
   on the account. Every machine of the account gets it: packages, shell,
   aliases, prompt, dotfiles, environment. On the laptop it lives at
   `~/.config/repose/machine.nix`.
2. **Project**: `repose.nix` at the repository root, the project's
   fragment as today. Services through `repose.system`, project tools.
3. **Folder**: the `flake.nix` or `.envrc` dev shell, loaded for agents,
   `repose exec` and the user's own shells alike.

Rules:

- The personal and project modules are both imported into one
  home-manager configuration. Lists and package sets merge. Two
  definitions of one single-valued option are an error that names both
  files (`machine.nix:4` and `repose.nix:9`), as home-manager reports any
  conflict. A project that needs to win uses `lib.mkForce`. The
  personal layer is the same fragment contract: no secrets, no impure
  reads, `repose.system` through the allowlist.
- Applied without asking. A changed personal file applies to every
  running machine of the account at once, and to a stopped one when it
  next starts (`PlanStart`'s pending revision already does this). A
  changed `repose.nix` applies on the next `run` or `sync` from that
  checkout. Each says so in one line.
- Never blocks. A new machine is created and boots as now; its first
  revision is the combined configuration, applied in place as soon as it
  is built. When the closure is already in the host's store (every guest
  mounts it, DESIGN §4) and the evaluation is cached (I-405), that takes a
  few seconds.
- Precedence over the laptop. With a personal file on the account, `run`
  skips the global-tools half of the scan. With a `repose.nix` in the
  checkout, it skips the project-scripts half too. Logins, Claude Code
  settings, skills and the git identity still travel from the laptop:
  they are not Nix's to describe. `repose scan` says which half was
  skipped and why.
- A project can opt out of the personal layer (dashboard switch and a
  CLI flag), for a machine shared in a demo, say.

## Build time

Measured today (`/docs/config` example, 45 s): evaluate 7.9 s, fetch from
cache.nixos.org 21 s, build the glue derivations 12 s, switch 3 s. A warm
re-apply measured 16 s on 2026-10-04.

What is paid once:

- Evaluation: once per distinct pair of files per base version (I-405's
  eval cache).
- Fetching a package: once per host, into the store every guest shares.
- Building: only the small home-manager and system derivations of a new
  combination; packages come from the cache.

What resets it: the weekly base update (evaluation and glue again, the
packages are still there), and a package not on cache.nixos.org, which
builds from source.

What hides the rest:

1. Never block the boot (above).
2. Build on save: a new personal file, pushed from the laptop or saved on
   the dashboard, is built at once for the account, with a GC root per
   account, so the next machine finds it built.
3. Rebuild ahead of the base: when a base is published, rebuild each
   account's personal file in the background, as projects already are.
4. Later, with a second host: a cache shared between hosts. It holds
   users' own outputs, dotfile contents included, so it is per account or
   private. Not needed with one host.

## Stages

0. **Fixes that help today.** `home.sessionVariables` and
   `home.sessionPath` from the fragment reach `environment.sessionVariables`
   (flake-in-root item 3). The user's interactive shells load the same
   cached dev shell agents get, in a checkout with a `flake.nix` and no
   `.envrc` (flake-in-root item 2). Guest base only.
1. **`repose.nix` from the repository, automatically.** `run` and `sync`
   read `repose.nix` at the repository root; when it differs from the
   project's active revision, they submit it as an apply and do not wait
   (the build goes on server-side, as after Ctrl-C today), printing
   `Applying repose.nix (changed) in the background.` A machine created by
   that run gets it the same way, right after create. The dashboard shows
   such a project's configuration as coming from the repository. CLI
   only, plus docs.
2. **The personal layer.** Storage on the account with revisions; every
   project revision records the personal text it was built with, so a
   rebuild or restore reproduces it. `Build` gains a `personal` field;
   hostd writes `personal.nix` beside `fragment.nix`, and the contract
   imports both. CLI and dashboard to show, edit and apply it. The CLI
   pushes `~/.config/repose/machine.nix` when it changed since its last
   push, and refuses with a clear line when the account's copy changed
   since (edited on the dashboard).
3. **Precedence over the scan**, as above.
4. **Speed**: build on save and rebuild ahead of the base.
5. **Getting a first file.** `repose scan` already maps the laptop's
   tools to nixpkgs names; it can write that as a starting
   `machine.nix`. `/docs/machine` gets a prompt that asks an agent to add
   the user's dotfiles and shell setup to it, naming the two files repose
   reads, so "ask your agent" gives something repose uses.

## Settled by the owner, 2026-10-04

- CLI placement: `repose config --global show|edit|add|remove|apply`, one
  flag on the existing noun. A machine opts out with `repose run
  --no-personal` (and a dashboard switch).
- A temporary machine (`--temp`) gets the personal layer: it is still
  yours.
- Applied automatically, stored on the account.

## Open questions

- Whether a machine whose personal build fails starts on the project
  layer alone (proposed: yes, with a notification naming `machine.nix`).
