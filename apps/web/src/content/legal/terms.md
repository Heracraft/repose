---
title: Terms of service
effective: 2026-10-08
status: these terms are under review before launch.
---

# Terms of service

These terms govern your use of repose, a service that runs a persistent
Linux environment for each of your projects on shared servers we operate,
reachable over SSH, where coding agents keep working after your laptop
closes. By creating an account you agree to them.

## The service

Each project gets one environment: a virtual machine of the size class you
choose, a disk of the size you choose, a network connection to the
internet, and the tools and agents we ship. Environments run until you
stop them. We build the environment's configuration from the description
you give us and apply it in place; when a change needs a reboot we ask
first. We snapshot every environment nightly and when you stop it, and you
can restore any snapshot we still hold.

We operate on infrastructure we rent; we will tell you in advance when we
move it and we will move your data with it.

## Your account

You sign in with a GitHub account. One person, one account; you are
responsible for what happens under it, including what your agents do. Keep
your laptop's SSH key and your CLI login private; if you lose a laptop,
`repose logout` from another device revokes its certificates, and every
certificate expires on its own within twenty-four hours.

## Coding agents and their providers

Coding agents such as Claude Code run inside your environment under your
own account with that agent's provider. repose does not hold, proxy, or
resell those credentials. You are responsible for complying with each
provider's terms for hosted use.

In particular: the platform never copies your existing Claude Code
credentials from your computer; you authenticate inside your environment,
through Anthropic's own sign-in, and the only alternative we offer
is a token you generate yourself and store as a named secret of your own
project. The agent binaries we ship are the providers' own releases,
unmodified.

## Acceptable use

Your environment is for software development and the work that comes with
it: builds, tests, containers, browsers, dev servers, agents. You may not
use it to:

- mine cryptocurrency, or run anything that does;
- send spam or bulk email nobody asked for;
- scan, probe or attack networks or systems you do not own or are not
  allowed to test, or flood anyone with traffic;
- run a proxy, VPN exit or relay that other people use to reach the
  internet;
- serve production traffic to other people: host your product elsewhere
  and use your environment to build it (showing work in progress to
  someone is fine);
- host or share content that is illegal where we or you are;
- try to reach other tenants' environments, our servers, or the cloud
  provider's metadata services.

Some of this is blocked at the network. Environments cannot connect out to
port 25, which is how servers hand mail to each other and how spam leaves
rented machines; send mail through a provider such as Amazon SES, Postmark
or Resend on its submission ports (465 or 587), which stay open. The usual
ports of cryptocurrency mining pools are blocked too. New outbound
connections are limited to a rate that development work does not come
near (a cold package install opens a few dozen); connections past it are
dropped, and connections already open are never cut.

If your environment runs a program named as a cryptocurrency miner, such
as xmrig, we stop it automatically, with a snapshot first so nothing on
its disk is lost, and tell you why. The third such stop within 24 hours
puts the project on hold: it cannot be started until we have looked at
it. Anything else, and any action on your account, is decided by a
person.

We record process names and network volumes to notice these things; the
privacy policy says exactly what we record and what we never record.

Each project has a bandwidth ceiling and build limits; the plan sets the
egress allowance, the disk, the memory running machines may use and the number of
projects. When every seat on our servers is taken, new plans wait on a
waitlist and are offered in order as seats free.

## Billing

repose is sold as a monthly plan, Solo, Plus or Pro, through Polar, which is
the merchant of record: Polar takes the payment, adds and remits the tax
for your country, and issues the receipt, under its
[buyer terms](https://polar.sh/legal/checkout-buyer-terms). Prices are published on the site
in US dollars before tax. A plan sets how much memory may run at once,
how much disk may be allocated and how much traffic may leave your
environments in a month; egress past the allowance is added to the next
invoice at the published rate, and at four times the allowance your
environments are stopped for the rest of the period.

Every plan starts with seven days free. You provide a payment card at
checkout and the first charge is made on the eighth day unless you cancel
before then. The plan renews monthly on the same card until you cancel.
Cancelling ends the plan at the end of the period you have paid for; your
environments run until then, stop at that point, and their snapshots are
kept for 30 days. Upgrading takes effect at once, prorated; downgrading
takes effect at the next renewal.

If a payment fails we tell you the same day and stop starting new
environments; the ones running keep running. Polar retries the card. If
the payment has not gone through after three days, we snapshot and stop
every environment and suspend the account; thirty days after that the
snapshots are deleted. A successful payment at any point restores the
account. Refunds are described in the [refund policy](/refunds).

We may change prices with at least 30 days' notice by email; an existing
plan keeps its price until that date. A plan is for one person's own use;
seats for other people are not sold yet.

## Your data

Your environments, their disks and snapshots, and the secrets you store
are yours. We access them only to operate the service, to investigate an
incident or an abuse report, or at your request, and every such access is
logged. We may suspend an account that breaks these terms; suspension
stops environments and preserves data on the normal retention schedule.
Destroying a project deletes its disk at once and its last snapshot after
thirty days. Cancelling your account stops everything at once and deletes
all data after thirty days.

## Availability and liability

We aim for the service to be available and we tell you when it is not, but
we make no guarantee of uptime, and an agent that was running when a
server failed may need to be started again from a snapshot. To the extent
the law allows, our liability to you is limited to the fees you paid us in
the three months before the claim, and we are not liable for indirect or
consequential loss, including work an agent did not finish. Nothing here
limits liability that cannot be limited by law.

## Changes

We may change these terms; we publish changes with their effective date
and notify you by email at least fourteen days before a change that
reduces what you get or increases what you pay. Continuing to use the
service after that date is acceptance.

## Contact

Questions, notices and security reports go to the contact address
published on repose.herakraft.co.
