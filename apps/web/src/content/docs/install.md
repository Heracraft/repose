---
title: Install
description: Install the CLI, log in, update and uninstall.
section: Start here
order: 2
---

The CLI runs on Linux and macOS, x86-64 or ARM64. On Windows, install it inside WSL.

## Install

```
curl -fsSL https://repose.herakraft.co/install.sh | sh
```

The script downloads the latest release, checks it and installs `~/.local/bin/repose`. If `~/.local/bin` isn't on your `PATH`, it adds it to your shell's startup file and prints that to the terminal. Open a new terminal afterwards.

The script needs `curl`, `tar` and `openssl`; macOS and most Linux distributions include all three. 

Other options:

```
# for every user, in /usr/local/bin
curl -fsSL https://repose.herakraft.co/install.sh | sh -s -- --system

# a particular release
curl -fsSL https://repose.herakraft.co/install.sh \
  | sh -s -- --version v0.1.11
```

Arch Linux has an unrelated package that also installs a `repose` command. If the script finds another `repose` earlier on your `PATH`, it prints both locations.

## How the download is checked

Each release's `checksums.txt` is signed with the repose release key. %%The script carries the public half of that key, verifies the signature with `openssl`, and then checks the archive against `checksums.txt`. It stops without installing anything if the signature is missing or wrong, or if the archive doesn't match. Releases up to v0.1.27 came out before signing began; for those, the script compares `checksums.txt` with a copy of its hash that it carries.%%

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

 `repose login --status` shows the account you're logged in as. logout with: `repose logout`.

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

Run the install command again. Update command coming soon

## Uninstall

```
repose logout --purge
rm ~/.local/bin/repose
```

`--purge` also deletes [the CLI's files](/docs/cli#files-on-your-laptop): `~/.config/repose/`, `~/.ssh/repose/` and the `Include` line the CLI added to `~/.ssh/config`. Your projects keep running on the server, so stop them, or remove them with `repose rm`, first.