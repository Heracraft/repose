# repose

repose puts the project you are working on, uncommitted edits and untracked
files included, on a Linux machine, starts a coding agent in it, and undoes
everything that agent wrote to the files with one command. The agent keeps
working after your laptop closes.

Apache-2.0, the whole repository. Everything about what this is and how it is
built lives in [`docs/`](docs/README.md). Contributors and agents also read
[`CLAUDE.md`](CLAUDE.md) (`AGENTS.md` is the same file).

## Two shapes

**A machine you own** is a Linux box you can already `ssh` into: the desktop at
home, a VPS, the box under the stairs. `repose machine add` installs repose on
it over that same `ssh`; the project's files live in a btrfs subvolume there, so
`repose rewind` puts the code, the untracked files and the dev database back
together. No account, and no server of ours in the path. Run it yourself for
nothing.

**A hosted machine** is a NixOS microVM repose creates for the project at
`repose.herakraft.co`, with its own kernel, disk and Docker, snapshots to Blob
storage, an SSH gateway, a dashboard and billing by the month.

Same binaries either way, and no feature is held back from the free half. What
a hosted account buys is the half of the product that is a service: a machine
reachable wherever you are, one project registry across every laptop you log in
on, notification delivery, and the agents and the machine's base kept current.

## A machine you own, from nothing

Install the CLI:

```
curl -fsSL https://repose.herakraft.co/install.sh | sh
```

Add the box. The login is your own account on it, and its `sudo` has to work
without a password:

```
$ repose machine add homebox --ssh me@192.168.1.40
Installing on me@192.168.1.40 ...
✓ Checked the box (Debian GNU/Linux 12 (bookworm), x86_64, systemd, btrfs)  0.2s
✓ Installed ripgrep gawk  0.7s
✓ Installed Nix 2.34.9 (single-user, store at /nix)  48.1s
✓ Provisioned 40G of undo space (btrfs at /var/lib/repose/undo)  1.4s
✓ Installed /opt/repose/bin/repose-agent  0.1s
✓ Installed claude-code 2.1.283  1.3s
✓ gemini-cli 0.61.0 has no x86_64-linux release and is not installed  0.0s
✓ claude starts without permission prompts  0.1s
✓ The store takes a point of every project every 30 minutes  0.1s
Added homebox.
```

Hand the project to an agent. The first run in a checkout binds it to that
machine, so later runs need no `--machine`:

```
$ cd ~/code/todo-app
$ repose run --machine homebox -p "finish the auth flow"
✓ Prepared todo-app on homebox  0.4s
✓ Synced 5 files and 1 commit  1.1s
Logins copied: gh, git
✓ Installed nodejs_22  0.2s
✓ Took a rewind point  0.0s
Added the git remote repose: `git fetch repose` brings the machine's commits to this checkout.
claude is running in todo-app on homebox.
```

That attaches you to the agent's tmux window; `Ctrl-b` then `d` leaves it
running. `repose attach todo-app` comes back to it, and `repose ps` says what is
in there.

The project's environment is its `repose.nix` at the checkout root, applied on
the machine by Nix in single-user mode, as a profile inside the project's own
subvolume. A run applies that file before it takes the rewind point, and
installs what the project needs that the box has not got. `repose config add
bun` puts a package in that profile, and `repose config show` lists what is in
it. The tools your laptop has globally and the commands the project's own
scripts call go into that profile too; a system service or a home-manager
module cannot go in one, and the run says so. A checkout that brings its own
environment, an `.envrc` or a `flake.nix` naming a dev shell, gets that loaded
on top, as it is on a hosted machine.

When the agent wrecks the project:

```
$ repose rewind
Rewind todo-app to the point taken 14 minutes ago? Everything written since is lost. [y/N] y
Stopped 1 process. Rewound todo-app (0.0s). The state it replaced is the point r-01a1575d85617c25830ac225.
```

A point is taken on every run, every sync and every 30 minutes. One costs 8 ms,
and a rewind of a 30,000-file project with a 32 MB database took 158 ms.

`docs/workstreams/SPINE.md` has the measurements. The pages a user reads are
`apps/web/src/content/docs/own-machine.md` and
`apps/web/src/content/docs/hosted-or-your-own.md`.

## What this does not do yet

- **A box behind NAT is yours to make reachable**, with Tailscale or your own
  forwarding. The relay a hosted account is meant to sell is not built.
- **No account, billing or dashboard on this path.** A machine you own is
  invisible to the dashboard, and `repose snapshots`, `resize`, `stop`,
  `start`, `rm`, `secrets`, `browser`, `logs` and `reply` act on a machine
  repose creates, so none of them reaches such a box. `run`, `attach`, `sync`,
  `ps`, `ls`, `status`, `config`, `scan`, `rewind` and `machine` do.
- **`repose config` there adds and lists; it does not remove.** The project's
  profile on a box you own only grows, so `repose rewind` is the way back to an
  environment without a package, and the revisions a hosted machine keeps have
  no equivalent on it.
- **`repose attach` on an owned machine is held by tests, not by a session**,
  because attaching needs a terminal. `run -d`, `sync`, `ps` and `rewind` were
  driven end to end against a real box (`ops/dev/spine-e2e.sh`).
- **No tagged release carries `repose-agent` yet**, so a box is installed with
  `--agent-url` and `--agent-sha256` until one does.
- **A database is still yours to run**, and anything installed outside the
  project's subvolume (`apt` packages, edits to `/etc`, Docker's volumes) is
  outside what a rewind undoes.
- **One tenant, one trust boundary.** There is no hypervisor between the agent
  and a box you own: an agent with full permissions reaches everything that
  login reaches, the rewind points included.
- **Rewind points are not a backup.** They live on that machine's disk. git is
  the copy that leaves the box.
- **macOS is not a box.** It has neither btrfs nor systemd, and the install
  refuses it.

## Layout

```
cmd/         api, hostd, guestd, gateway, repose (CLI), repose-agent (on a
             machine you own), repose-admin, repose-hook; hostdev (M1
             stand-in), fakeapi and fake-logto (test fakes)
internal/    shared Go; undo (btrfs rewind points), machine (the on-laptop
             registry of machines you own); fakes for every interface;
             generated protobuf under gen/
proto/       gRPC and vsock contracts (docs/interfaces/ is the prose)
nix/         one flake: hosts, edge, guest base, agent overlay, dev shell
infra/       OpenTofu for the Azure pieces and DNS
ops/install/ machine-install.sh, what `repose machine add` runs over ssh
ops/dev/     spine-e2e.sh and the rest of the operator scripts
apps/web/    SvelteKit dashboard and the public docs
packages/    shared TypeScript config and UI packages
docs/        the spec
```

## Developing

```
nix develop ./nix     # Go, buf, opentofu, az, just, nixos-anywhere ...
just build
just test
just proto            # regenerate internal/gen from proto/
```

Do not install anything from `nix/` on this machine; hosts and guests are
remote (see `CLAUDE.md`).

## Licence

Apache License 2.0. See [`LICENSE`](LICENSE).
