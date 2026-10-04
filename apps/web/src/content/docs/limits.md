---
title: Limits and acceptable use
description: What your plan allows at once, how many projects you can have, what the network allows, and what gets a machine stopped.
section: Account
order: 31
---

## Your plan

A plan buys memory for running machines, disk that may be allocated, and egress for the month ([Pricing](/docs/billing)). Solo gives running machines 8 GB of memory (one `large`, or two `small`), Plus 16 GB (one `xl`, two `large`, any mix), Pro 32 GB (two `xl`, four `large`, any mix). Starting a machine that would pass it is refused with exit code 7 and a message that names the machine using the memory; stop it, or upgrade. An `xl` needs Plus or Pro.

## Projects

Solo allows 10 projects, Plus 25 and Pro 50, running or stopped. Destroyed projects don't count, and neither does one still being destroyed, for projects or for disk. A project whose destroy failed still counts until `repose rm` succeeds. Each copy [`repose fork`](/docs/lifecycle#fork-a-project) makes is a project, and so is a [temporary machine](/docs/lifecycle#temporary-machines) until it's destroyed. A temporary machine lives from 10 minutes to 24 hours, and waits at most a day past that while someone is attached. Disk bounds it anyway: Solo allocates up to 100 GB across its projects, Plus 250 GB, Pro 500 GB.

## When repose is full

Machines never share memory, so there's room for a fixed number of them. A seat is 8 GB of memory for running machines: Solo takes one, Plus two, Pro four. When no seat is free, choosing a plan puts you on the waitlist instead: `repose is full right now. You're number 3 on the waitlist; we'll email you@example.com when there's a seat.` The Billing page and the landing page show the seats left and the number waiting.

We let people in, in the order they joined, as seats free up or we add a server. You get one email when it's your turn, sent even if you've turned notification emails off, and the seat is held for you for 72 hours. Choose your plan within them; a hold that runs out moves you to the back of the queue, and the email says so.

## Egress

Data your machines send to the internet is counted against the month's allowance (250 GB on Solo, 500 GB on Plus, 1 TB on Pro), then $0.05 per GB. At four times the allowance your machines are stopped until the month turns, with an email. Incoming data, and your own SSH traffic through the gateway, don't count.

## Network

- Outbound traffic is limited to 200 Mbit/s per machine. Downloads into the machine aren't limited.
- Outbound connections to port 25 are blocked, so a machine can't send mail directly. Use your email provider's API, or its submission port (587 or 465) with a login.
- Outbound connections to the ports mining pools use (3333, 5555, 7777, 14433 and 14444) are blocked.
- A machine can open 200 new outbound connections a second, in bursts of up to 2000. Installing packages, running test suites and crawling your own app stay well under it.
- Nothing on the internet can connect to the machine. Reach your own servers on it through [port forwarding](/docs/machine#ports).

## SSH connections

- Your account can hold 32 SSH connections through the gateway at once, across all your projects. `repose` commands share one connection per command, and an editor opens a few.
- One address can have 4 connections logging in at the same moment, 64 open and 20 new a second. Past that, a new connection is closed before it logs in.
- A connection ends when the certificate it logged in with expires, or within 30 seconds of `repose logout` on any device. The CLI renews a certificate once it has less than 12 hours left, so a connection a `repose` command or your `ssh` opens lasts at least 12 hours. Editors reconnect by themselves; a `repose attach` that ends this way can be run again, and the tmux session is where you left it.

## What isn't allowed

The [terms](/terms) have the full wording. In short, don't use a machine to:

- mine cryptocurrency;
- send spam or bulk mail;
- attack, scan or flood other systems;
- host or run anything illegal.

<!-- Hidden for now (owner, 2026-09-28); the terms still carry it:
- host a live product for other people (showing someone work in progress is fine);
-->

A machine running a known miner is stopped, with a snapshot. You get a notification, and `repose status` and the dashboard say why. After three stops in a day the project can't be started until we've looked at it. Other abuse found by monitoring or reported to us gets the machine stopped and the account reviewed. Monitoring looks at process names and resource use, never at your files or terminal; see [what repose stores](/docs/secrets#what-repose-stores).
