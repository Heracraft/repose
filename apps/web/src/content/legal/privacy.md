---
title: Privacy policy
effective: 2026-10-08
status: this policy is under review before launch. The two sentences in bold under "Process samples" already bind the service today.
---

# Privacy policy

repose gives each of your projects a persistent Linux environment on a
shared server where coding agents keep working after your laptop closes.
This policy says what we record about you and your environments, what we
never record, where it lives, how long we keep it, and who can see it. It
is written to be checked against the software; the two sentences in bold
under "Process samples" are enforced by tests in the repository, and a
change to them is a change to the product.

## What we record

**Your account.** You sign in through our identity provider with your
email address or with GitHub. We store your email address, your GitHub
login if you use GitHub, a handle derived from your GitHub login or, without
one, from the part of your email address before the @, your time zone, and
your notification settings (an email address and, if you set one, an ntfy
endpoint).

**Your projects.** For each project: its name, the git remote URL you
created it from, its size class, its state, which server it runs on, its
configuration (the Nix fragment or menu choices you gave us), the build
logs of that configuration, and the events your agents report through the
notification hooks (agent name, kind of event, and a short summary the
agent produced, capped at 1 KB), and the messages and questions sent from
the environment with `repose-notify` and `repose-ask`, with your answers
(each capped at 1 KB).

**Metering.** Once a minute, for every environment: whether it is running,
its size class, CPU time, memory in use, how long its processes waited
for a CPU (inside the environment and on the server), bytes sent and received, disk
allocated and used, the number of open SSH sessions and attached tmux
clients, the number of Docker containers running, and which agents are
running in which tmux windows and whether they are working, idle, or
waiting for input. Bytes sent count against your plan's egress allowance;
all of these numbers are the signals we watch for abuse.

**Process samples.** **We sample the processes running in your
environment once a minute and record their names, CPU time, memory use and
network bytes. We never record command-line arguments, environment
variables, file paths, file contents, terminal contents, or the prompts
you give to any agent.** The sampler reads only the process name, CPU and
memory counters of each process; a test runs it under system-call tracing
and fails if it ever opens a process's command line or environment.

**Console logs.** The serial console of your environment, which carries the
kernel's and the init system's messages, is captured and kept for 30 days
so we can diagnose an environment that will not boot. Your shells run on
terminals, not on the console; text you type into a terminal or an agent
does not reach the console log, and a test checks that.

**Access records.** Every SSH session opened to an environment through our
gateway is recorded as an open and close event with the project and the
certificate serial used. Every operator login to a server and every
command an operator runs inside an environment is written to an audit log
that we keep indefinitely.

**Snapshots.** Nightly, and whenever you stop a project, we take a
snapshot of the environment's disk and store it compressed in Azure Blob
Storage with server-side encryption. Snapshots are yours to restore from
and are the only way a project moves between servers.

## What we never record

The process-sample boundary above is the important one. Beyond it:

- We never copy your existing Claude Code credentials from your computer.
  You log in to Claude inside your environment with your own account.
- We never store the tool logins the repose CLI copies from your laptop
  (GitHub CLI, Codex, opencode, your git identity). They travel from your
  laptop to your environment inside your own SSH session and are not
  visible to our servers or our staff except by reading your environment's
  disk, which is covered under "Who can see your data".
- Named secrets you store with `repose secrets set` are encrypted before
  they reach our database, with a key we cannot export. We never show a
  secret's value back to you or to anyone; our API only lists names.
- Our logs and metrics never carry the contents of a terminal, an agent's
  prompts or output, command-line arguments, environment variables, file
  paths inside your environment, secret values, tokens, or your email
  address. The full list is published in the repository's observability
  policy and each log line is reviewed against it.

## Where your data lives

Environments and their disks run on virtual machines in Microsoft Azure
(East US). Snapshots are in Azure Blob Storage in the same region. Our
database runs on a virtual machine in the same region and is backed up
nightly to Cloudflare R2. Identity is handled by a Logto instance we run
ourselves; GitHub sees only the sign-in. Payments are handled by Polar,
the merchant of record: at checkout Polar receives your email address,
your country (for tax) and your card, which it processes through Stripe,
and holds them under
[Polar's privacy policy](https://polar.sh/legal/privacy-policy). We hold
Polar's identifiers for your customer and subscription, your plan and
its status, the dates of the period and the invoice totals, never card
numbers. Email notifications are sent through Resend;
push notifications go to the ntfy endpoint you configure, which may be a
third party of your choosing. Our feedback board at
[repose.fider.io](https://repose.fider.io) is hosted by Fider: when you
sign in there with your repose account, Fider receives your name and
email address, and what you post there is public and held under
[Fider's privacy policy](https://fider.io/privacy).

Coding agents such as Claude Code, Codex, opencode, Gemini CLI and pi run
inside your environment under your own account with each agent's provider.
What those agents send to their providers is governed by the provider's
own terms and privacy policy, not by this one.

## How long we keep it

| What                                       | Kept for                                                                |
| ------------------------------------------ | ----------------------------------------------------------------------- |
| Metering samples                           | 90 days                                                                 |
| Process samples                            | 30 days                                                                 |
| Console logs                               | 30 days                                                                 |
| Service logs                               | 90 days                                                                 |
| Usage records, invoices, events, audit log | indefinitely                                                            |
| Build logs                                 | the last 20 builds per project                                          |
| Snapshots                                  | 7 daily; the last one 30 days after a project is destroyed              |
| Environment disks                          | until you destroy the project                                           |
| Everything, after you cancel               | environments stop at once; snapshots and data are deleted 30 days later |

## Who can see your data

Our operators can log in to the servers that run your environment. Today
an operator with root on a server can read any environment's disk on that
server; we have not yet enabled per-project disk encryption and we do not
claim otherwise. Every such login and every command run inside an
environment is written to the audit log. We look at your environment only
to investigate an incident, an abuse report, or a support request you
made, and we record that we did.

Other tenants share the same servers. Each environment is a separate
virtual machine with its own disk and its own network interface; our
network rules stop environments from reaching each other or the server,
and a test suite exercises those rules on every release. The threat model
and what it does and does not cover are published in the repository.

## Abuse detection

The process names and network volumes we record are what we look at when
we suspect abuse: a cryptocurrency miner, a port scanner, sustained full
CPU for hours with nobody attached, egress measured in terabytes. A short
list of process names always appears in samples so that a program hiding
below the top of the list is still visible.

Two things happen without a person. An environment whose process samples
name a known cryptocurrency miner is stopped, with a snapshot first, and
the stop and that process name are recorded against the project. And the
network blocks the terms describe (outbound port 25, mining-pool ports,
new connections over the rate limit) count how many attempts each
environment made, a number only: never the address it tried, and never
what it sent. Any action on your account is decided by a person; nothing is
suspended automatically.

## Your choices

You can destroy a project, which deletes its disk at once and its last
snapshot after 30 days. You can delete your account, which stops every
environment and deletes everything 30 days later. You can turn each
notification channel off. You can read every line of the software that
handles your data; the repository is the authoritative description of what
it does.

## Changes and contact

Changes to this policy are published with the date they take effect, and
the two quoted boundaries cannot be weakened without the code and its
tests changing first. Questions and security reports go to the contact
address published on repose.herakraft.co.
