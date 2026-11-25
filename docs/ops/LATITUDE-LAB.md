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

On-demand metal bills by the hour. The API's own hourly prices, read
2026-11-23, are about twice the pricing page's (the page's figures are the
monthly rate, which is 50% off): m4-metal-small (6 cores, 48 GB,
2 x 960 GB NVMe) $0.81 an hour in the US, UK, Netherlands and Canada, the
default and the size of a real home server; m4-metal-medium (16 cores,
128 GB) $1.25. The $100 is about 120 hours of the small one. `plans`
prints the live price per region. Their docs state no minimum charge and
say a deletion "may take up to an hour" to show on the billing summary.
Every `create` and `destroy` appends a line
to `~/briefs/latitude-billing.log`. Destroy the server the day it was
made, unless the owner says to keep it.

## A run, as a customer

1. `ops/dev/latitude plans`: check the plan is in stock where you want it
   (default `NYC`; Ashburn had no m4-metal-small on 2026-11-23). The
   account has two projects; the lab's is `repose-boyh`
   (`proj_ZWr75ZQOvNA91`), the helper's default.
2. `ops/dev/latitude ssh-key` once per project, then
   `ops/dev/latitude create`, then `ops/dev/latitude wait`.
3. On the box, check the customer's preconditions before installing:
   `ls -l /dev/kvm`, `nproc`, `free -g`, `df -h /`, the OS release.
4. From a logged-in CLI here, `repose host add` writes the join argument
   to `~/.config/repose/join-NAME` and prints the installer line that
   reads it (I-1616). Copy the file to the box (`scp` it to
   `repose-join`), then run there `curl -fsSL
   https://repose.run/install-host.sh | sudo bash -s -- --join-file
   repose-join`. Or log a CLI in on the box and run `repose host add`
   there, as a customer would. Machines made on it are throwaway `e2e-*`
   projects, never the owner's (docs/ops/RELEASE.md).
5. `repose host ls` until `online`, then `repose run e2e-x86-... --temp`
   and the checks the change under test needs.
6. `repose host rm`, then on the box `curl -fsSL
   https://repose.run/install-host.sh | sudo bash -s -- --uninstall`
   (I-1590), then `ops/dev/latitude destroy`, then `status` to see
   it gone. Read the billing log's last two lines.

## What the first run confirmed

Run live on 2026-11-23 for the first x86 multi-host review
(`docs/reports/2026-11-23-x86-multihost-1.md`): `ssh-key` on the project
path works and `ssh_keys` takes the key's id; the Ubuntu image logs in as
`ubuntu`; `create`, `wait`, `ssh` and `destroy` work, and `destroy` gives a
404 on the server straight after. Servers came from a ready pool: `wait`
answered 3 seconds after `create`, with the box up for hours already.

Two servers at once: give the second its own state file,
`LATITUDE_STATE=$HOME/.config/repose/latitude-lab-b.state`. The key id is
kept apart from the per-server state (`latitude-lab.ssh-key`), so the
second box needs no second `ssh-key`.

Stock moves by the minute: NYC and CHI ran out of m4-metal-small between
two creates five minutes apart, and DAL had it. A 422
`SERVERS_OUT_OF_STOCK` means another `LATITUDE_SITE` from `plans`.

The provider's power switch, for a "box loses power" test, is the raw
call: `ops/dev/latitude api POST /servers/<id>/actions
'{"data":{"type":"actions","attributes":{"action":"power_off"}}}'`
(`power_on` to bring it back; about 95 seconds to `online` again).
