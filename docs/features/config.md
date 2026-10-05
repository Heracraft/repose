# Config

A project's environment is the platform base plus the user's own Nix
fragment. People who write Nix write the fragment; everyone else picks from a
menu that generates the same fragment. Either way the result is a NixOS
closure built on the host and switched into the running guest without a
reboot.

## What the user sees

The menu, from the dashboard or the CLI:

```
$ repose config add bun postgresql gcc air
Added bun, postgresql, gcc and air to todo-app. Building revision 4f1c2a9e ...
✓ Evaluated your config  7.9s
✓ Fetched 38 paths (112.4 MiB)  21s
✓ Built 14 derivations  12s
✓ Switched the machine  3.1s
Applied revision 4f1c2a9e in 45s.

$ repose config add gcc-typo
Added gcc-typo to todo-app. Building revision 7d03b1c5 ...
config error: nixpkgs has no package "gcc-typo"; search https://search.nixos.org/packages
Nothing changed in todo-app; the previous revision is still active.

$ repose config remove air
Removed air from todo-app. Building revision 9a8e77d0 ...
```

A name the catalog has is that catalog entry (services included); any
other name is a nixpkgs attribute path, such as `gcc`, `nodejs_22`,
`python312Packages.black` or `nodePackages.typescript` (DECISIONS I-220).

The fragment:

```
$ repose config show              # print it; --revisions lists revisions
$ repose config edit              # opens $EDITOR on the fragment, applies on save
$ repose config apply ./repose.nix
$ repose config apply              # no ./repose.nix: apply the current configuration again (I-321)
```

The steps are read off the build log (DECISIONS I-320); `-v` adds Nix's
own lines. Ctrl-C stops only the CLI; the build goes on.

A failed apply prints the error's first line, then the fragment line it
points at (`internal/cli/buildlog.go` `RenderBuildError`), and exits 10;
the previous revision stays active.

A change that needs a reboot is built but not switched in; the CLI prints
`This change needs a reboot; run `repose stop && repose start` when the
agent is idle.` and the next `start` boots it. The dashboard can also
re-apply it with a reboot (`POST .../config/revisions/:rid/apply?reboot=true`).

Holding a project on its base is the **Hold base updates** checkbox on the
dashboard's Config page (`hold_base_updates` on the project). There is no
CLI command for it.

## Menu versus fragment

The menu is a catalog (`GET /catalog`) of packages and services with labels
and groups: languages, databases, browsers, tools, and deploy CLIs
(wrangler, the Supabase CLI, flyctl and cloudflared from nixpkgs; the Vercel CLI and
portless, which nixpkgs does not carry, installed once with `npm i -g`
into `~/.npm-global` by a user unit after the network is up). None of
these needs the menu: `npm i -g <cli>` in the guest works and persists
too (DECISIONS I-36), and so does `nix profile add nixpkgs#<attr>`,
which resolves `nixpkgs` to the base's own pinned nixpkgs, needs no
download of it, and fetches the package from cache.nixos.org (I-218).
Typing a command the guest does not have names the nixpkgs package that
has it and both ways to add it (I-219; plain layout I-249):

```
$ air
air: command not found
  nix profile add nixpkgs#air  install it on this machine
  repose config add air        keep it on every rebuild (run this on your laptop)
Other packages with air: air-formatter
```

The first is immediate and survives restarts; the second puts it in the
fragment, so it survives a rebuild from scratch and a move to another
host. The base also has a C toolchain (`cc`, `gcc`, `g++`, `make`,
`cmake`, `pkg-config`) for cgo, node-gyp and rustup, and runs prebuilt
Linux binaries downloaded by npm, pip or an install script (nix-ld):
Prisma engines, Playwright and Puppeteer browsers, rustup toolchains,
uv-managed Pythons and manylinux wheels, bun, deno and the usual
single-binary CLIs work with their stock commands (I-228). A selection is stored as
JSON and rendered by the API into a home-manager module. Editing the
fragment directly turns the menu off for that project (the menu cannot
round-trip arbitrary Nix); the CLI and dashboard say so and offer to keep a
copy of the generated fragment as the starting point.

The fragment is a home-manager module, evaluated under the platform's
nixpkgs and home-manager inputs. The user does not choose inputs; the base
version pins them. A fragment may add packages, enable `programs.*`
modules, set session variables and dotfiles, and declare user systemd
services. It cannot touch NixOS system options; those are the base's,
except the allowlisted services the menu renders into `repose.system`
(below).

The catalog is `internal/menu/catalog.yaml`; `GET /catalog` returns each
entry's `{id, label, group, kind, description, options}`. A selection is
`[{id, options: {name: value}} | {package: "<nixpkgs attribute path>"}]`
(`interfaces/api.md` "MenuSelection"); the api validates it (an unknown id
or value, or a package name that is not a plain attribute path, is
`invalid` naming it) and renders it into a fragment whose first two lines
are `# generated by repose from your menu selection; edit with repose
config edit to take over` and `# repose-menu: <the selection as JSON>`,
one `imports` module per catalog entry and one more holding every package
as `home.packages`. A package name only ever reaches Nix as a string
looked up in `pkgs` (`lib.attrByPath`), so a missing one fails the build
with `nixpkgs has no package "<name>"`, and an unfree one outside the
allowlist below with nixpkgs' own refusal. The api keeps a project in
menu mode while its fragment still starts with those lines, an empty
selection included. `repose config add <name>...` and `repose config
remove <name>...` edit the selection from the CLI; the dashboard's menu
tab lists the packages as "Extra packages".

## Writing a fragment

A fragment is one Nix file whose value is a home-manager module: an
attribute set, or a function `{ config, pkgs, lib, ... }: { ... }`. It is
applied to user `dev` on top of the platform base. Three complete examples
are in [config-examples/](config-examples/); `nix flake check` builds them,
so they are always current.

A fragment may:

- add `home.packages` from `pkgs`, the platform's pinned nixpkgs with the
  agents overlay applied. Unfree packages are allowed for a fixed list:
  `claude-code`, `codex`, `gemini-cli`, `vscode`, `cursor`, `terraform`,
  `ngrok`, `google-chrome`;
- configure any `programs.*` and `services.*` home-manager module (user
  services, not system ones);
- write dotfiles with `home.file` and `xdg.configFile`;
- set `home.sessionVariables` and `home.sessionPath`. They reach every
  process (PAM, `/etc/set-environment`, agent wrappers, and dev's tmux
  server and user manager after a switch), the fragment's value over the
  base's and sessionPath first on `PATH`. `PATH`, `BASH_ENV`, `ENV`,
  `REPOSE_ENV_GEN` and `REPOSE`, and a double quote in a value, are
  refused (DECISIONS I-488);
- apply overlays through `repose.overlays = [ (final: prev: { ... }) ]`.
  They are applied to the guest's `pkgs` before anything is evaluated,
  after the platform's own overlay. home-manager's `nixpkgs.overlays` is
  refused, because with the platform's shared `pkgs` it would be silently
  ignored. The overlay list is read before `pkgs` exists, so its value may
  use `lib` but not `pkgs` or `config` (the overlay functions themselves
  see the real `final` and `prev`);
- fetch sources with `pkgs.fetchurl`, `pkgs.fetchFromGitHub`,
  `pkgs.fetchgit` and friends, always with a hash: fixed-output fetches run
  in the build sandbox with network, which is Nix's normal model, and reach
  the public internet only (the host's firewall drops the build accounts'
  traffic to private, loopback and link-local addresses, DECISIONS I-439);
- build things with `pkgs.writeShellScriptBin`, `pkgs.buildNpmPackage`,
  `pkgs.buildGoModule`, `pkgs.rustPlatform.buildRustPackage`, any nixpkgs
  builder;
- enable a system service from the allowlist through `repose.system`,
  which is what the menu generates: `repose.system = [ {
  services.postgresql = { enable = true; }; } ]`. Each entry is a plain
  attribute set whose first two levels must be on the list in
  `nix/guest/system-allowlist.json` (today: `services.postgresql`,
  `services.redis`, `services.mysql`, `services.memcached`,
  `services.rabbitmq`, `services.meilisearch`, `services.nats`). Anything
  else is refused at evaluation with the option named. The menu is the
  supported way to get a service; this is the door it goes through.

A fragment may not:

- use `builtins.fetchurl`, `builtins.fetchTarball`, `builtins.fetchGit` or
  `builtins.fetchTree`: evaluation is pure and restricted, so an
  evaluation-time fetch fails with `eval-time fetch not allowed at
  fragment.nix:L:C; use pkgs.fetchurl { url = ...; hash = ...; }`;
- read any path outside its own file (`builtins.readFile "/etc/passwd"`
  fails with `access to absolute path '/etc/passwd' is forbidden`), or
  `import <nixpkgs>` (there is no search path; use the `pkgs` argument);
- import from derivation (`allow-import-from-derivation` is off);
- set NixOS options: it is a home-manager module, so
  `services.postgresql.enable = true` at the top level fails with `option
  'services.postgresql' does not exist in a fragment; a fragment is a
  home-manager module: packages go in home.packages, databases come from
  `repose config add` or repose.system`, a whole `flake.nix` fails with
  `this file is a Nix flake; ...` (DECISIONS I-483), and `networking.*`, `users.*`,
  `boot.*`, `services.openssh` and `virtualisation.*` are outside the
  `repose.system` allowlist whatever the file says.

Limits: evaluation 60 seconds; build 30 minutes wall time on 8 cores and
16 GB of memory, sandboxed, as an unprivileged user; closure 20 GB over
the base. The exact messages for each failure are listed in
[`../interfaces/nix-build-contract.md`](../interfaces/nix-build-contract.md)
"What the user reads".

## Behaviour that must hold

Building (limits from DECISIONS R5-4, pipeline in
`workstreams/12-nix-config-pipeline.md`):

- `PUT /config` stores a revision, starts a build on the project's host, and
  returns an op id. The CLI streams the build log. The dashboard shows it
  live.
- Evaluation is pure and restricted, capped at 60 seconds. Builds are
  sandboxed, capped at 30 minutes wall time and 8 cores, with substitutes
  from cache.nixos.org and the platform overlay cache. A project's closure
  may not exceed 20 GB.
- Every failure names itself. Syntax and evaluation errors show the Nix
  message and the fragment line. A timeout says which derivation was
  building when time ran out. A closure over the cap lists the largest
  store paths. A fragment using a forbidden builtin (`fetchurl`,
  `fetchTarball`, `fetchGit` outside pinned inputs, import-from-derivation)
  is refused at evaluation with the builtin named. A fragment containing a
  current secret value of the project is refused before evaluation.
- A failed build changes nothing. The previous revision stays `applied` and
  the guest is untouched. The failed revision is kept with its error so the
  dashboard can show it.
- A fragment identical to the applied one is a no-op and says so in one
  line.

Applying (DECISIONS R3-3):

- After a successful build, hostd sends `Switch` to guestd, which runs the
  new system's `switch-to-configuration switch`. Running processes, tmux,
  and agents are untouched. A test starts an agent, applies a package
  addition, and asserts the agent's PID and tmux window survive and the new
  binary is on `PATH` in a new shell.
- If the kernel, initrd, or virtio-fs share layout changed, guestd reports
  a reboot is required and switches nothing. The revision stays `built`
  with `reboot_required`; the CLI says to `repose stop && repose start`,
  and a start boots the newest built revision. Applying it to a running
  guest from the API needs `?reboot=true` (409 `conflict` without it).
- On a stopped guest, apply happens at next start.
- Switching back: `repose config show --revisions` lists revisions; the
  dashboard's Revisions list re-applies an earlier one, which rebuilds
  nothing while its closure is still a GC root.

Base bumps (DECISIONS R4-5):

- The platform releases a new base version weekly, sooner for security
  fixes. The api sweeps daily at 04:00 UTC, and within ten minutes of a
  security release (`internal/api/basebump`). Every project not holding
  gets its fragment rebuilt against the new base and switched in place.
  If that needs a reboot, the revision stays `built` and takes effect at
  the project's next `stop`/`start`; the `base_updated` event says so. No
  forced overnight reboot is built.
- A rebuild against a new base that fails does not change the project; it
  raises a `base_update_failed` event with the error. A base that breaks
  a fragment is usually the platform's bug.
- The dashboard's **Hold base updates** checkbox (`hold_base_updates`)
  pins the base; unticking it releases the project to the next rollout.
  The Config page shows the base the project is on.

## The personal layer (DECISIONS I-490)

- An account has one machine.nix, a home-manager module under the same
  contract as a fragment, stored with revisions (`GET/PUT /me/config`).
  Every machine of the account that has not opted out gets it beside the
  project's fragment; temporary machines too.
- A save rebuilds every such project: a running machine switches in
  place, a stopped one keeps the revision built for its next start. A
  failure leaves each machine on its revision and raises
  `personal_failed`.
- A new machine never waits for it: when the host holds no closure of the
  combination, the machine is created on the project layer and the
  combined revision is built and applied right after.
- The CLI keeps `~/.config/repose/machine.nix` and pushes it on `run`
  when it changed since its last push, refusing when the account's copy
  changed since too; `repose config --global` shows, edits, adds,
  removes and applies; `repose run --no-personal` and the Config page's
  switch opt a machine out.
- With a machine.nix on the account the tool carry skips the laptop's
  global tools; with a `repose.nix` at the checkout root it skips the
  scripts' commands.

## Depends on

Workstreams 12 (evaluation, build, limits, errors), 03 (Build and
ApplyConfig commands), 04 (Switch), 05 (config routes, catalog, revisions,
base rollout scheduling), 07 (`config` commands, log streaming), 08
(menu UI, revision list, build log view), 13 (base bump events).

## Deferred

Templates other than the single platform base (DECISIONS R4-1 mentions
future templates). Central builder and binary cache across hosts (R4-4).
Fragments that reference private git repositories.
