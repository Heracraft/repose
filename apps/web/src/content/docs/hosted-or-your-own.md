---
title: Hosted or your own
description: The two ways to run repose: free on a Linux box you own, or a machine repose runs for you, and what each one is.
section: Start here
order: 3
---

repose comes in two shapes, and the software is the same in both: one CLI, the same [agents](/docs/agents), and the same `repose.nix` for the project's tools. What differs is whose machine the project runs on, and so what undoes an agent's work on it: a [rewind point](/docs/own-machine#what-a-rewind-brings-back) on a box you own, a [snapshot](/docs/lifecycle#snapshots) on a machine repose runs.

## A box you own

[A Linux box you can `ssh` into](/docs/own-machine) runs your projects once `repose machine add` has installed repose on it. Nothing of ours is in the path: the laptop reaches the box over your own `ssh`, the machines you have added are a file on the laptop, and there is no account to make and nothing to log into.

This half is free, and its source is yours: repose is Apache-2.0 at [github.com/heracraft/repose](https://github.com/heracraft/repose), server included. A box you own, and your own disk and network, are what it costs to run.

What owning the box means is that the box is yours to keep: reachable, over Tailscale or your own forwarding when it sits behind NAT; on, and patched, because you keep it that way; and holding the rewind points on its own disk, which [is not a backup](/docs/own-machine#points-are-not-a-backup).

## A machine repose runs

A [hosted machine](/docs/machine) is a NixOS microVM repose creates for the project, with its own kernel, disk and Docker, on hardware of ours. It needs no box of yours, and with it comes the half of the product that is a server:

- **Reachable from wherever you are**, with no port to forward and no VPN of yours: [every project is an SSH host](/docs/ssh-and-editors) through repose's gateway, whichever network the machine and the laptop are on.
- **One list of your projects**, on every laptop you are logged in on: [`repose ls`](/docs/cli#repose-ls) and the dashboard, rather than a file on one machine.
- **[Notifications](/docs/notifications) delivered** by email, to your phone or into Google Chat when an agent finishes or asks you something, and your answer sent back to it.
- **The agents and the machine's base kept current**, as five agents change week by week, and [installed software](/docs/config) built and applied for you.
- **[Snapshots](/docs/lifecycle#snapshots) that outlive the machine**, and the rest of a project's lifecycle: stop, start, resize, fork, restore.

A hosted machine is paid for by the month ([Pricing](/docs/billing)).

## Nothing is held back

The free half is the whole product on your own hardware, not a trial of it: no feature is kept for paying accounts, and no command checks a plan before it works on a machine you own. What an account buys is the server-side half above, which is work someone has to do and hardware someone has to rent.
