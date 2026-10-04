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

- Outbound traffic is limited to 200 Mbit/s per machine. Downloads into the machine are limited to 1 Gbit/s; downloads from the npm and Docker Hub caches on the server aren't limited.
- Outbound connections to port 25 are blocked, so a machine can't send mail directly. Use your email provider's API, or its submission port (587 or 465) with a login.
- Outbound connections to the ports mining pools use (3333, 5555, 7777, 14433 and 14444) are blocked.
- A machine can open 200 new outbound connections a second, in bursts of up to 2000, and hold 16,384 open at once. Connections to the caches count toward the 16,384 and have their own 200 a second. Installing packages, running test suites and crawling your own app stay well under both.
- Nothing on the internet can connect to the machine. Reach your own servers on it through [port forwarding](/docs/machine#ports).

## Disk and console

- Disk reads and writes together are limited by size: `small` 2,000 operations a second and 80 MB/s, `large` 3,000 and 120 MB/s, `xl` 4,000 and 150 MB/s. A machine takes a new limit when it next starts.
- A disk can grow only as far as the server it runs on has room. A [resize](/docs/machine#memory-and-disk) past that fails with `the host has no room for this project right now`, even within your plan's disk.
- The boot and console log that `repose logs --kind console` shows keeps up to 2 KB a second from the machine's serial console, after the first 1 MB. Output past that is dropped, and the log has a line saying how many bytes went. Your programs' own output in a terminal or a log file isn't affected.

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
