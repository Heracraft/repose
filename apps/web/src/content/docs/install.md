---
title: Install
description: Install the CLI, log in, update and uninstall.
section: Start here
order: 2
---

The CLI runs on Linux and macOS, x86-64 or ARM64. On Windows, install it inside WSL. It needs `git` and the OpenSSH client.

## Install

```
curl -fsSL https://repose.run/install.sh | sh
```

The script downloads the latest release, checks it and installs `~/.local/bin/repose`. If `~/.local/bin` isn't on your `PATH`, the script adds it to your shell's startup file and says so; when that file can't be written, home-manager's read-only `~/.bashrc` for instance, it prints the line to add instead. Open a new terminal afterwards.

The script needs `curl`, `tar` and `openssl`; macOS and most Linux distributions include all three.

Other options:

```
# for every user, in /usr/local/bin
curl -fsSL https://repose.run/install.sh | sh -s -- --system

# a particular release
curl -fsSL https://repose.run/install.sh \
  | sh -s -- --version v0.1.11
```

Arch Linux has an unrelated package that also installs a `repose` command. If the script finds another `repose` earlier on your `PATH`, it prints both locations.

## How the download is checked

Each release's `checksums.txt` is signed with the repose release key. The script carries the public half of that key, verifies the signature with `openssl` and checks the archive against `checksums.txt`. If the signature is missing or wrong, or the archive doesn't match, the script installs nothing. Releases up to v0.1.27 came out before signing began; for those the script compares `checksums.txt` against a hash it carries.

To check a downloaded archive yourself, with the GitHub CLI:

```
gh attestation verify repose_v0.1.28_darwin_arm64.tar.gz \
  --repo heracraft/repose
```

This confirms that the repose release workflow built the archive. It works for v0.1.28 and later.

## Log in

```
repose login
```

[`repose login --status`](/docs/cli#account) shows the account you're logged in as. You stay logged in until `repose logout`. Starting a machine needs a [plan](/docs/billing).

## Set up by an agent

Paste this into the coding agent on the computer you work on, in your project's folder:

```text
Set up repose for me.
Instructions: https://repose.run/docs/install#set-up-by-an-agent

It gives each of my projects its own Linux machine, where coding
agents keep working after I close my laptop. Do this in order, and
stop at any step that fails, telling me what it printed:

1. If `repose --version` fails, install the CLI with
   `curl -fsSL https://repose.run/install.sh | sh`. Until I open a
   new terminal, it is at ~/.local/bin/repose.
2. Run `repose login --status`. If I am not logged in, run
   `repose login` in the background, show me the address and the
   code it prints, and wait for me to say I have signed in. Then run
   `repose login --status` again. If it says I have no plan, give me
   https://repose.run/billing and wait for me.
3. Only if I asked for repose to run my machines on this computer (a
   Mac with an M3 chip or newer, or a Linux x86_64 box with 16 GB of
   memory): run `repose host add` here and do what it prints. If
   sudo asks for my password, give me the command to run in my own
   terminal. Never use `--join`, never print or read the files named
   `join-*` under ~/.config/repose, and never ask me to paste an
   install line from the dashboard: each of those holds a key that
   must stay out of this conversation.
4. In the folder of the project I want on repose (this one, unless I
   named another), run `repose run -d`. It copies the folder,
   uncommitted work included, to a new machine. Never run it in my
   home folder.
5. Tell me to run `repose attach NAME` in a terminal of my own, with
   the name `repose run` printed, to log Claude Code in on the
   machine once. That login is never copied from this computer.

Do not run `repose run` without `-d`: it attaches to a terminal and
waits.
```

Signing in is yours: the agent shows you an address and a code, and you approve it in your browser. The sign-in page is on `accounts.herakraft.co`, repose's login service.

### What stays out of the conversation

Nothing the agent reads holds a key. `repose login` keeps your login in `~/.config/repose`, and the agent only ever sees the code you approve. On a Mac, `repose host add` hands the host's key to the install inside the same process. On an x86 box it writes the key to `~/.config/repose/join-NAME`, readable by you alone, and prints an install line that names the file; [Your own hardware](/docs/own-hardware#adding-it) has the rest.

The install line the dashboard's **Add a host** shows carries the key itself. Paste it into a terminal, not into an agent.

## Shell completion

`repose completion SHELL` prints a script that completes commands, flags and project names. To load it in every new shell:

```
# bash
echo 'source <(repose completion bash)' >> ~/.bashrc

# zsh
repose completion zsh > "${fpath[1]}/_repose"

# fish
repose completion fish > ~/.config/fish/completions/repose.fish
```

## Update

```
repose version update
```

It downloads the latest release, checks it the way the install script does, and replaces the binary. Your login and settings stay. A version goes back to that release: `repose version update v0.1.40`. Releases before v0.1.28 came out unsigned and it refuses them.

A CLI older than the latest release says so on stderr, once per release. One the server no longer answers as these docs describe says so at the end of every `repose run`, `repose attach` and `repose sync`.

If a package manager installed the binary (Homebrew, snap, your distribution's), the command names that manager and changes nothing; update it there. If the binary is somewhere you can't write, `/usr/local/bin` after `install.sh --system` for instance, run `sudo repose version update`.

A CLI too old to have the command updates the old way. Run the install command again:

```
curl -fsSL https://repose.run/install.sh | sh
```

## Uninstall

```
repose logout --purge
rm ~/.local/bin/repose
```

`--purge` also removes [the CLI's files](/docs/cli#files-on-your-laptop). Your projects keep running on the server, so stop them, or remove them with `repose rm`, first.

[Your SSH keys stay on your laptop](/docs/ssh-and-editors#your-ssh-keys-stay-on-your-laptop).
