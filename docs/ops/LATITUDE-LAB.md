# The x86 own-hardware lab on Latitude.sh

A rented bare-metal x86 server stands in for the box a customer brings to
the Own hardware plan: `apps/web/src/content/docs/own-hardware.md`, "What
an x86 box needs" (bare metal, Intel or AMD, `/dev/kvm`, Ubuntu 24.04,
16 GB or more, outbound UDP 51820 and TCP 443, no inbound port). It
replaces AWS for this job: AWS has no cheap hourly bare metal, and the
owner holds a $100 credit at Latitude.sh. The Mac path still needs Apple
hardware, which Latitude does not have (`ops/dev/macmini`).

The helper is `ops/dev/latitude`; its header lists every command.

## What it can and cannot tell you

It answers "does the x86 own-hardware path work, as a customer runs it".
It does not answer "how fast is repose for hosted customers": repose's
servers are Azure Intel with nested virtualization, this is AMD bare metal
with none, so a speed measured here says nothing about host-02 (the same
reason the dev box measures nothing about hosts, CLAUDE.md).

## The key

The owner writes it once, in their own terminal on woker-1 (the `!`
prefix in a Claude session has no keyboard input, so `read` gets nothing):

```bash
mkdir -p ~/.config/repose && install -m 600 /dev/null ~/.config/repose/latitude-api-key && read -rsp 'Latitude API key: ' k && printf '%s' "$k" > ~/.config/repose/latitude-api-key && unset k && echo && echo saved
```

The key is made at Latitude under Settings & Billing, API Keys, with write
access. An agent never reads, prints, copies or checks the file's
contents; the helper sends it as a bearer header from a pipe, so it is
never in argv. `test -s` is the only check an agent runs on it.

## Money

On-demand metal bills by the hour. Their pricing page on 2026-11-23:
m4.metal.small (6 cores, 48 GB, 2 x 960 GB NVMe) $0.41 an hour, the
default and the size of a real home server; m4.metal.medium (16 cores,
128 GB) $0.62. The $100 is about 240 hours of the small one. Their docs
state no minimum charge and say a deletion "may take up to an hour" to
show on the billing summary. Every `create` and `destroy` appends a line
to `~/briefs/latitude-billing.log`. Destroy the server the day it was
made, unless the owner says to keep it.

## A run, as a customer

1. `ops/dev/latitude plans`: check the plan is in stock where you want it
   (default `ASH`, Ashburn). The slugs come from the API spec's enums and
   were not seen live before the first run.
2. `ops/dev/latitude ssh-key` once per project, then
   `ops/dev/latitude create`, then `ops/dev/latitude wait`.
3. On the box, check the customer's preconditions before installing:
   `ls -l /dev/kvm`, `nproc`, `free -g`, `df -h /`, the OS release.
4. From a logged-in CLI here, `repose host add` prints the join argument
   for an x86 box; run the installer on the server as the docs give it
   (`curl -fsSL https://repose.run/install-host.sh | sudo bash -s --
   --join <argument>`). Machines made on it are throwaway `e2e-*`
   projects, never the owner's (docs/ops/RELEASE.md).
5. `repose host ls` until `online`, then `repose run e2e-x86-... --temp`
   and the checks the change under test needs.
6. `repose host rm`, then `ops/dev/latitude destroy`, then `status` to see
   it gone. Read the billing log's last two lines.

## Untested

The helper was written from the API reference on 2026-11-23, before a key
existed. Not confirmed live: the plan, site and OS slugs; whether
`ssh_keys` takes the key's id (assumed) or its name; the project ssh-key
path, which the spec marks deprecated; the user the Ubuntu image logs in
as (`ubuntu` assumed, `LATITUDE_SSH_USER` changes it). The first run is
the test; fix the helper in the same branch as what it finds.
