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

The CLI prints a link and a short code. Open the link in any browser, on any device: the page already has the code in it, so check it matches the one in your terminal, then sign in with your email or with GitHub. Because the browser doesn't have to be on the same computer, this also works over SSH.

You stay logged in until you run `repose logout`.

## Update

Run the install command again. It replaces the binary and leaves your login and settings alone.

## Uninstall

```
repose logout --purge
rm ~/.local/bin/repose
```

`--purge` also deletes `~/.config/repose/`, `~/.ssh/repose/` and the `Include` line the CLI added to `~/.ssh/config`. Your projects keep running on the server, so stop them, or remove them with `repose rm`, first. [Files on your laptop](/docs/cli#files-on-your-laptop) lists everything the CLI writes.

The CLI never reads or changes your own keys in `~/.ssh`, and it writes nothing into your repositories.
