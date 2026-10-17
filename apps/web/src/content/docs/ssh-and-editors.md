---
title: SSH and editors
description: Reach every project with ssh, scp, rsync and git, and open it in VS Code, Cursor, Zed or JetBrains Gateway.
section: Using repose
order: 10.5
---

## Every project is an SSH host

After `repose login`, every one of your projects is an SSH host called `<project>.repose`, including ones you created on another laptop and have never run from this one. Anything that uses your own `ssh` reaches it.

```
ssh todo-app.repose
ssh todo-app.repose 'cd todo-app && git log --oneline -3'
scp todo-app.repose:todo-app/report.html .
rsync -a todo-app.repose:todo-app/dist/ ./dist/
ssh -L 9229:localhost:9229 todo-app.repose
git clone todo-app.repose:todo-app todo-app-from-machine
```

A path with no leading slash is under `/home/dev`, in a folder that may not be the project's name ([Where the checkout is](/docs/sync#where-the-checkout-is)).

Plain `ssh` lands in a login shell outside tmux, so the agents' windows aren't there and what you start in it dies with the connection. `repose attach` from your laptop, or `tmux attach` in that shell, puts you in the session ([Detach and come back](/docs/run-and-attach#detach-and-come-back)).

In a checkout you've run `repose run` in, skip the clone: the `repose` remote already points at the machine ([Getting work back](/docs/sync#getting-work-back)).

## VS Code and Cursor

```
repose code todo-app
```

```text
Opening todo-app.repose:/home/dev/todo-app in VS Code
```

To pin one editor instead of the first `repose code` finds, see [`repose code`](/docs/cli#repose-code-project-checkout).

To connect by hand, point **Remote - SSH** at `todo-app.repose`; Cursor has its own. From a terminal:

```
code --remote ssh-remote+todo-app.repose /home/dev/todo-app
```

If your shell has no `code`, run **Shell Command: Install 'code' command in PATH** from VS Code's Command Palette.

## Zed

`repose code --editor zed` opens the checkout in Zed. By hand, `zed ssh://todo-app.repose/home/dev/todo-app`.

## JetBrains Gateway

Gateway brings its own SSH client instead of yours, so it renews no certificate and writes no Host block. `ssh todo-app.repose true` does both: run it before you connect, and again each time Gateway drops the connection. Point it at host `todo-app.repose` and tell it to authenticate from your OpenSSH config, where repose's key and certificate are named and nowhere else. We haven't tested Gateway yet.

## How it works

`repose login` puts one `Include ~/.ssh/repose/config` line at the top of `~/.ssh/config`. The file it includes has your `ssh` run `repose ssh-prepare` before every connection to a `.repose` host. With the certificate and the project's `Host` block already on disk the prepare reads a few files and returns without touching the network. Otherwise it renews the certificate ([SSH connections](/docs/limits#ssh-connections)) or writes the missing block, and ssh goes on with those.

The prepare never prompts, so an editor can't hang on it. An editor that reconnects runs it again, so the certificate is renewed without you. A renewal that fails doesn't end the connection as long as the certificate on disk is good for another minute: ssh connects on that one, and the prepare prints `repose: could not renew the SSH certificate (...); using the one on disk.`

It only writes those files, so a plain `ssh todo-app.repose` never starts a stopped machine, while `repose ssh` [does](/docs/lifecycle#stop-and-start).

## When it doesn't connect

`Could not resolve hostname todo-app.repose` with no other message means ssh isn't reading the file repose writes: no entry gave ssh the name, so it asked DNS. `Include ~/.ssh/repose/config` has to sit above the first `Host` or `Match` line of `~/.ssh/config`, because below one it applies to that block alone. `repose login` puts it at the top, and leaves a line of your own further down where you put it.

When `~/.ssh/config` is a symlink, the line goes in at the far end of it, your dotfiles repository included. When that target is one repose can't write, home-manager's in the Nix store for instance, it prints the path and the line to add there yourself, and every command that connects says so until the line is in place.

repose's own commands connect anyway: they hand ssh the file with `-F`. The editor `repose code` opens does not, and nor do plain `ssh`, `scp` and `git`.

The line that runs the prepare names the `repose` binary by absolute path. Move that binary and ssh prints your shell's `not found` for the old path, then connects on the certificate and Host blocks already on disk, which holds until the certificate expires. The next repose command that connects writes the current path in.

## Your SSH keys stay on your laptop

Your laptop's ssh-agent is never forwarded to the machine, and `ssh -A` is refused. Nothing running there, an agent or a package's install script, can use your keys, even while you're attached. Three layers say no: the generated config sets `ForwardAgent no`, the gateway answers the forwarding request with a failure, and the guest's sshd has `AllowAgentForwarding no`.

repose connects with a key it generated for itself and never reads or certifies your own keys in `~/.ssh`, so connecting never asks for your key's passphrase.

So the machine has no key of yours to push with. [Pushing from the machine](/docs/sync#pushing-from-the-machine) has what git uses instead.

## Windows

Inside WSL, `ssh`, `scp`, `rsync` and `git` reach a project as above, with the CLI [installed inside WSL](/docs/install).

An editor installed on Windows can't reach a project yet. It connects with Windows' own `ssh`, which doesn't read the `~/.ssh` in WSL, where repose writes the Host block and the certificate. `repose code` refuses that editor: `VS Code here is the Windows app, which connects with Windows' ssh and cannot reach a repose machine yet. Use repose ssh, or an editor installed inside WSL.`
