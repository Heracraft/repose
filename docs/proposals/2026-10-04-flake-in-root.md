# Flake in the root: what is left after I-483

Status: proposal, nothing built. Source: the live test of 2026-10-04
(`reports/Flake in root DevX.md` in the main checkout, untracked), on
base 2026.10.04.1 with CLI v0.1.29.

I-483 fixed the small things: the dev shell no longer writes `flake.lock`
into the checkout, `repose config apply flake.nix` says the file is a
flake, the unknown-option hint no longer names a `repose config menu`
command, and `/docs/machine` says what a first flake gets wrong. The
items below are larger, change the base or add user-visible behaviour, so
each needs the owner's choice first. They are ordered by how much they
hurt a new user.

## 1. Say what repose did with the flake

Today `repose run` and `repose sync` say nothing about a `flake.nix`. A
flake that repose ignores (only `nixosConfigurations`, `darwinConfigurations`
or `packages`), an untracked one, or one with a dev shell only for
`aarch64-darwin` is found out later, in an agent's scrollback, or never.

Proposal: the CLI already reads the checkout to sync it. When the
repository root has a `flake.nix`, `run` and `sync` print one line, only
when something is wrong, from cheap text checks on the laptop (no Nix
needed there):

```text
flake.nix is untracked; Nix on the machine will not see it. `git add flake.nix`.
flake.nix has no dev shell, so agents start without it. Machine-wide packages and databases: repose config.
flake.nix defines a dev shell only for aarch64-darwin; the machine is x86_64-linux.
```

The third check is a heuristic (the text names a darwin system and
neither `x86_64-linux`, `eachDefaultSystem`, `eachSystem` nor
`systems.url`). No new command or flag. `repose scan`, which already
reports what the machine will get from the laptop, could print the same
lines.

## 2. The user's own shells get the dev shell too

Agents and `repose exec` start in a flake's dev shell; `repose ssh`,
`ssh <slug>.repose`, the tmux shell window and an editor's terminal do
not, unless the repository has an `.envrc` with `use flake`. On the test
machine `which ruff` found the flake's ruff in the agent and not in the
shell next to it, and the command-not-found handler then suggests
`nix profile add nixpkgs#ruff`, which is the wrong fix.

Proposal: an interactive-bash hook in the base (next to direnv's) that,
in a checkout with a `flake.nix` and no `.envrc`, loads the same shadow
`.envrc` the agent wrapper writes (`~/.cache/repose/devshell/<hash>`),
from the cache, so it costs about 0.4 s once built. Alternative: leave it,
and have the command-not-found handler say "this checkout's flake.nix has
ruff: agents get it; in your shell, add `use flake` to `.envrc`" when the
missing command is in the cached dev shell's PATH.

## 3. `home.sessionVariables` in repose.nix does nothing

`/docs/config` shows `home.sessionVariables.DENO_NO_UPDATE_CHECK = "1"`.
On the test machine a fragment with `home.sessionVariables.TALLY_ENV =
"dev"` built and switched; the value is in
`/etc/profiles/per-user/dev/etc/profile.d/hm-session-vars.sh`, and nothing
sources that file (`sudo grep -rl hm-session-vars /etc` is empty, and dev
has no `~/.bashrc` or `~/.profile`). `bash -ic 'echo $TALLY_ENV'` prints
nothing. home-manager only sources it when it manages the shell.

Proposal: in `nix/guest/contract.nix`, copy the fragment's
`home.sessionVariables` into NixOS `environment.sessionVariables` and
`home.sessionPath` onto its `PATH`, so they reach
`/etc/set-environment` and PAM, and therefore agents, tmux and user units,
like the base's own variables (env.nix). Needs a VM test on the dev box
(`guest-base` asserting a fragment variable in a non-interactive bash and
in an agent window). Until then the docs example is wrong.

## 4. A prompt that produces something repose can use

The owner's line, "tell your agent to represent your machine as a Nix
flake, put it in the root of your project", produced two different
results in the test. Asked on a repose machine, Claude wrote a NixOS
`nixosConfigurations` for that machine (hardware, sshd, docker), which
repose does not read. Asked with a Mac's Homebrew list as context, it
wrote a nix-darwin config plus a `devShells` for three systems, which
does load on repose (38 s cold) and gives agents the tools, but its
PostgreSQL is a client only: nothing starts the server, and the user's
"everything comes along" is false for every service.

Proposal: `/docs/machine` gets a short "Ask your agent" block with a
prompt that names the two files repose reads, for example:

```text
Write a flake.nix at the root of this repository with a devShells.default
for x86_64-linux and aarch64-darwin (use flake-utils) holding the
languages and CLI tools this project needs. Put databases and other
services in repose.nix as a home-manager module: repose config add
postgresql (or the matching menu entry) for each service, home.packages
for anything every shell on the machine needs. Commit flake.nix and run
nix flake lock if Nix is installed.
```

and the video line becomes the paragraph at the end of the report.
Larger alternative, not recommended yet: let `repose config apply` read a
flake output (`reposeModules.default`, a home-manager module) so one
`flake.nix` carries both the dev shell and the machine configuration.

## 5. Quieter dev shell output in agent windows

Every agent window starts with ten lines of `direnv: export +AR +AS +CC
+CONFIG_SHELL ...` (mkShell's whole stdenv). The wrapper could set
`DIRENV_LOG_FORMAT` for the load and print its own one line ("loaded the
dev shell from flake.nix: ruff, hyperfine, node ...", or just the count),
keeping direnv's output for a failed load, as the quiet mode for `repose
exec` already does.

## 6. Say whether the fallback had anything

A failed load prints "starting claude with the last one that did, if
any". nix-direnv sets `NIX_DIRENV_DID_FALLBACK` whether or not a previous
profile existed, so the user cannot tell whether the agent has the old
tools or none. The wrapper can check for a `flake-profile-*` link in the
layout directory before the load and say "with the dev shell from
before this change" or "without a dev shell".
