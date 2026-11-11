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

If a package manager installed the binary (Nix, Homebrew, snap, your distribution), the command names that manager and changes nothing; update it there. If the binary is somewhere you can't write, `/usr/local/bin` after `install.sh --system` for instance, run `sudo repose version update`.

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
