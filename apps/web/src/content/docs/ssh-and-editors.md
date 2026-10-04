---
title: SSH and editors
description: Reach every project with ssh, scp, rsync and git, and open it in VS Code, Cursor, Zed or JetBrains Gateway.
section: Using repose
order: 10.5
---

## Every project is an SSH host

Once you've run `repose login`, each of your projects is an SSH host called `<project>.repose`. Anything that uses your `ssh` can reach it: projects you created on another laptop, and projects you've never run from this one, included. You don't need a `repose` command first.

```
ssh todo-app.repose
ssh todo-app.repose 'cd todo-app && git log --oneline -3'
scp todo-app.repose:todo-app/report.html .
rsync -a todo-app.repose:todo-app/dist/ ./dist/
ssh -L 9229:localhost:9229 todo-app.repose
```

You log in as `dev`. Paths are relative to `/home/dev`, and the project's checkout is `/home/dev/<folder>`, named after the laptop folder it was first synced from: run `todo-app` from `~/code/todo-app` and it is `/home/dev/todo-app`, run it from `~/code/factory` and it is `/home/dev/factory` ([Where the checkout is](/docs/sync#where-the-checkout-is)). `repose exec pwd` prints it. Plain `ssh` doesn't attach to tmux; run `tmux attach` for that, or use `repose attach`.

git works over the same host:

```
git clone todo-app.repose:todo-app todo-app-from-machine
git ls-remote todo-app.repose:todo-app
```

In a checkout you've used `repose run` in, you don't need these: the `repose` remote already points at the machine, and `git fetch repose` brings the agent's commits ([Getting work back](/docs/sync#getting-work-back)). To run one command, `repose exec npm test` is shorter than `ssh` with a `cd` ([See what's running, run one command](/docs/run-and-attach#see-whats-running-run-one-command)).

## VS Code and Cursor

```
repose code todo-app
```

```text
Opening todo-app.repose:/home/dev/todo-app in VS Code
```

`repose code` checks the machine is running and that SSH to it works, then opens the checkout. It uses VS Code if `code` is installed, else Cursor, else Zed. Pick one with `--editor code`, `--editor cursor` or `--editor zed`, or set `REPOSE_EDITOR` to one of those.

To connect by hand, install the **Remote - SSH** extension (Cursor has its own), run **Remote-SSH: Connect to Host…** from the Command Palette, type `todo-app.repose`, pick **Linux** if it asks, and open `/home/dev/todo-app`. From a terminal:

```
code --remote ssh-remote+todo-app.repose /home/dev/todo-app
```

No VS Code settings are needed. If `code` isn't found on a Mac, run **Shell Command: Install 'code' command in PATH** in VS Code; `repose code` also finds VS Code, Cursor and Zed in `/Applications` and `~/Applications` without it.

## Zed

```
repose code --editor zed
```

or from a terminal, `zed ssh://todo-app.repose/home/dev/todo-app`. In Zed itself, open a remote project, add a server with `ssh todo-app.repose`, and open `/home/dev/todo-app`.

## JetBrains Gateway

Gateway uses its own SSH client instead of your `ssh`, so it can't renew the certificate for you. Run `ssh todo-app.repose true` before connecting, and again when Gateway loses the connection: a connection ends when its certificate expires. Then in Gateway, choose **SSH**, add a connection to host `todo-app.repose`, choose **OpenSSH config and authentication agent** for authentication, and open `/home/dev/todo-app`. We haven't tested Gateway yet.

## How it works

`repose login` adds one line to `~/.ssh/config`, `Include ~/.ssh/repose/config`, and writes that file. Before every `ssh` to a `.repose` host, the file has ssh run `repose ssh-prepare`, which checks the host's entry and your certificate:

- When both are in place, it returns at once. It reads a few files and makes no network call.
- When the certificate has less than 12 hours left (they last 24 hours) or the project is new to this laptop, it gets a new certificate and writes the project's entry, then ssh goes on with them.

A connection ends when the certificate it logged in with expires. Commands share one connection per machine; when a new certificate is issued, that shared connection stops taking new commands (the ones already on it carry on), and the next command opens a fresh one with the new certificate. So an `ssh` you start keeps its connection for at least 12 hours. Editors reconnect on their own, and the reconnect renews the certificate.

It never asks you anything, so an editor can't hang on it. The entries themselves are in `~/.ssh/repose/hosts`, one per project.

Connecting never starts a stopped machine. An editor that reconnects in the background would otherwise start a machine you stopped on purpose. `repose start todo-app` starts it, and so does `repose run` in its checkout.

## When it doesn't connect

**``Not logged in. Run `repose login`.``** followed by `Could not resolve hostname todo-app.repose`: log in, then connect again.

**``repose: you have no project called todo-ap (`repose ls` lists them).``** The name is wrong, or the project was destroyed.

**``todo-app is stopped; run `repose start todo-app` ``**: the machine is stopped. Start it and connect again.

**`Could not resolve hostname todo-app.repose` with no other message.** ssh isn't reading `~/.ssh/repose/config`. The `Include ~/.ssh/repose/config` line must come before the first `Host` or `Match` line of `~/.ssh/config`. If your `~/.ssh/config` is read-only (managed by Nix or a dotfiles tool), add the line where it's generated; `repose login` prints what to add when it can't.

**You moved or reinstalled the `repose` binary** and ssh says `not found` before connecting: run `repose login` or `repose attach` once, and the path is written again.

## Your SSH keys stay on your laptop

Your laptop's ssh-agent is never forwarded to the machine, and `ssh -A` is refused. Nothing running there, an agent or a package's install script, can use your keys, even while you're attached.

Pushes to GitHub still work. When your `gh` login is copied over, git on the machine sends `git@github.com:` and `ssh://git@github.com/` URLs over HTTPS with that login, so `git push` works without changing the remote. For other git hosts, see [Other git hosts](/docs/secrets#other-git-hosts).

## Windows

Install the CLI inside WSL. `ssh`, `scp`, `rsync` and `git` inside WSL work as above. VS Code, Cursor and Zed running on Windows use Windows' own `ssh`, which doesn't read the `~/.ssh` in WSL, so they can't connect to a project yet.
