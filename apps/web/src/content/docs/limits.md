---
title: Limits and acceptable use
description: What your plan allows at once, how many projects an account can have, what the network allows, and what gets a machine stopped.
section: Account
order: 31
---

## Your plan

Solo runs 8 GB of machines at once, Plus 16 GB, Pro 32 GB. The limit is the total, not the number of machines. An `xl` takes 16 GB on its own, so it needs Plus or Pro.

Starting a machine, or growing one, that would pass the total is refused with exit code 7, naming the projects using the memory. On a terminal `repose run`, `start` and `attach` [ask to stop one](/docs/billing#what-a-plan-means) instead.

## Projects

An account can have 100 projects, running or stopped, on every plan. Past that, creating, restoring or forking a project is refused with exit code 7: ``You have 100 of the 100 projects an account can have, running or stopped. Destroy one first with `repose rm PROJECT`.``

Destroyed projects don't count, for the 100 or for disk, and neither does one still being destroyed. A project whose destroy failed counts until `repose rm` succeeds. Each copy [`repose fork`](/docs/lifecycle#fork-a-project) makes is a project, and so is a [temporary machine](/docs/lifecycle#temporary-machines) until it's destroyed.

Your plan's disk counts the data your projects hold, running or stopped ([what a plan has](/docs/billing#what-a-plan-means)). A project's own [disk](/docs/machine) is how far that project can grow, not a slice of the plan's.

While your projects hold more than the plan's disk, creating, restoring or forking a project, and growing a disk, are refused with exit code 7: `Your projects hold 104 GB and your Solo plan has 100 GB of disk. Destroy a project, or delete files in one (they stop counting within a day, or when it stops), or upgrade at https://repose.herakraft.co/billing.` Nothing stops. Your machines keep running, and a stopped one still starts, since starting it is how you delete the files that put you over. `repose ls`, `repose status` and the Billing page say you're over before anything refuses you.

## When repose is full

A seat is 8 GB of memory for running machines, so a plan takes one for each 8 GB it runs. Choosing a plan whose seats aren't free puts you on the waitlist instead: `repose is full right now. You're number 3 on the waitlist; we'll email you@example.com when there's a seat.` The Billing page shows your place and how many are waiting.

Seats free as plans end, and a new server adds more. We invite in the order people joined, one seat each. You get one [email](/docs/notifications#emails-about-your-account) when it's your turn, and the seat is held for 72 hours. A hold that runs out hands the seat to the next person and puts you at the back of the list, with an email saying what number you are now.

## Egress

Data your machines send to the internet counts against the plan's allowance for the billing period ([what counts](/docs/billing#what-a-plan-means)). Every GB past it is $0.05 on your next invoice. At four times the allowance your machines are stopped for the rest of the period, and starting one is refused with exit code 7: `Your machines are stopped until 1 November: this period's egress passed 1000 GB, four times the Solo plan's 250 GB allowance. Upgrade at https://repose.herakraft.co/billing, or wait for the period to end.` An upgrade raises that ceiling to four times the new plan's allowance, and the refused start goes through.

## Network

- Outbound traffic is limited to 200 Mbit/s per machine, and downloads into it to 1 Gbit/s. Neither limit applies to the [caches on the server](/docs/machine#network).
- Outbound connections to port 25 are blocked. Use your email provider's API, or its submission port (587 or 465) with a login.
- Outbound connections to the ports mining pools use (3333, 5555, 7777, 14433 and 14444) are blocked.
- A machine can open 200 new outbound connections a second, in bursts of up to 2,000, and hold 16,384 open at once. Past that, new connections are dropped and open ones are never cut. Connections to the caches count toward the 16,384 and have their own 200 a second.
- Ping to the internet gets no reply. Check connectivity with `curl -sI https://example.com`.

## SSH connections

- Your account can hold 32 SSH connections through the gateway at once, across all your projects.
- One address can have 4 connections logging in at the same moment, 64 open, and 20 new a second in bursts of up to 40. Past that, a new connection is closed before it logs in.
- A connection ends when the certificate it logged in with expires. The CLI keeps at least 12 hours of validity on the certificate it connects with, so a `repose` command or an `ssh` you start holds its connection that long.
- `repose logout` revokes your certificates, and the connections they opened close within 30 seconds, on any device.
- An update to the SSH gateway leaves open connections running. A restart ends them all at once, and `run`, `attach` and `open` [come back on their own](/docs/run-and-attach#detach-and-come-back).

## Disk and console

- Disk reads and writes together are limited by size: `small` 2,000 operations a second and 80 MB/s, `large` 3,000 and 120 MB/s, `xl` 4,000 and 150 MB/s. A machine takes a new limit when it next starts.
- A disk can grow only as far as the server it runs on has room, even when your plan's disk has plenty. A [resize](/docs/machine#memory-and-disk) past that is refused with exit code 8 and `the host has no room for this project right now; try again in a few minutes`, and so is starting a stopped project on a server whose disk is nearly full.
- The machine's serial console log ([`repose logs --kind console`](/docs/cli#repose-logs-project)) takes 1 MB at once and then 2 KB a second. Output past that is dropped, and the log gets a line saying how much went: `[repose: N bytes of console output dropped, over the limit of 2048 bytes a second]`. Your programs' own output in a terminal or a log file isn't affected.

## What isn't allowed

A machine is for development work. Don't use one to:

- mine cryptocurrency;
- send spam or bulk mail nobody asked for;
- scan, attack or flood systems you aren't allowed to test;
- host or run anything illegal.

<!-- Hidden for now (owner, 2026-09-28); the terms still carry it:
- host a live product for other people (showing someone work in progress is fine);
-->

The [terms](/terms) have the full list.

A machine running a process named as a known miner is stopped, with a snapshot first, so nothing on the disk is lost. The match is the process name and nothing else; repose doesn't read your files or your terminal ([what repose stores](/docs/secrets#what-repose-stores)). `repose status` and the dashboard say why: `stopped: a cryptocurrency miner (xmrig) was running; mining is not allowed on repose, see the terms at https://repose.herakraft.co/terms`. The third stop within 24 hours puts the project on hold: it can't be started, forked or restored into a new project until we've reviewed it; reply to the notification or write to the contact address on the site. No other abuse is acted on automatically; a person decides on it.
