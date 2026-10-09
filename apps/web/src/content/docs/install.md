---
title: Install
description: Install the CLI, log in, update and uninstall.
section: Start here
order: 2
---

The CLI runs on macOS and Linux, x86-64 or ARM64. On Windows, install it inside WSL. It needs `git` and the OpenSSH client. You don't need tmux, Nix or Docker on your laptop; those run on the machine.

## Install

```
curl -fsSL https://repose.herakraft.co/install.sh | sh
```

The script downloads the latest release, checks it and installs `~/.local/bin/repose`. If `~/.local/bin` isn't on your `PATH`, it adds it to your shell's startup file and tells you. Open a new terminal afterwards.

The script needs `curl`, `tar` and `openssl`; macOS and most Linux distributions include all three. A slim container image may lack `openssl`, so install it first.

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

Each release's `checksums.txt` is signed with the repose release key. The script carries the public half of that key, verifies the signature with `openssl`, and then checks the archive against `checksums.txt`. It stops without installing anything if the signature is missing or wrong, or if the archive doesn't match. Releases up to v0.1.27 came out before signing began; for those, the script compares `checksums.txt` with a copy of its hash that it carries.

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

The CLI prints a link and a short code, and opens the link in this computer's browser when it has one. Or open it in any browser, on any device: check the code on the page matches the one in your terminal, then sign in with your email or with GitHub. This works over SSH too. `repose login --status` shows the account you're logged in as.

The first `repose run` on a computer that has never logged in logs in this way first.

You stay logged in until you run `repose logout`.

## Choose a plan

A machine starts only on an account with a plan. Choose one at [repose.herakraft.co/billing](https://repose.herakraft.co/billing); the first week is free. Solo runs 8 GB at once: one `large`, or two `small`. [Pricing](/docs/billing) compares the plans.

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

Run the install command again. It replaces the binary and leaves your login and settings alone. When a newer release is out, the CLI says so once.

## Uninstall

```
repose logout --purge
rm ~/.local/bin/repose
```

`--purge` also deletes `~/.config/repose/`, `~/.ssh/repose/` and the `Include` line the CLI added to `~/.ssh/config`. Your projects keep running on the server, so stop them, or remove them with `repose rm`, first. [Files on your laptop](/docs/cli#files-on-your-laptop) lists everything the CLI writes.

The CLI never reads or changes your own keys in `~/.ssh`, and it writes nothing into your repositories.
