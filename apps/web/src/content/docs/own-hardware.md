---
title: Your own hardware
description: Run your projects on a Mac mini or an x86 box you own, on the Own hardware plan.
section: Account
order: 29
---

Repose normally runs your machines on its own servers. On the [Own hardware plan](/docs/billing#own-hardware) ($10 a month) it runs them on hardware you own instead: a Mac mini on your desk, or an x86 box in a cupboard. Everything else is unchanged. You still `repose run`, still reach your machines over SSH through repose's gateway, still get the notifications and the [triggers](/docs/triggers). The difference is whose hardware your agents burn.

Your hardware takes only your projects. Nobody else's machine is ever placed on it, and once you have added one, your new projects go there rather than on repose's servers. `repose run --host repose` makes one on repose's servers instead, `repose run --host mac-mini` names your hardware, and a project made through the [API](/docs/api#where-a-project-runs) says the same with `placement` and `host`. With more than one host, a new machine goes to the one with the most room, and `--host box-b` puts it on box-b or refuses with box-b's state and exit 8; it never puts it on another host. The create line names the host a machine went to: `✓ Created todo-app on box-a (large)`. Repose's servers need a plan that buys memory there. A machine stays on its host for life. It does not move to repose's servers or to another host, and a machine on repose's servers does not move to your hardware: a snapshot is only readable where it was taken.

## What a Mac mini needs

- An M3 chip or newer. Nested virtualization, which repose needs to run your machines, arrived with M3; an M1 or M2 cannot do it.
- macOS 15 or later.
- 16 GB of memory or more. Your machines run inside repose's virtual machine on the Mac, which takes three quarters of the memory and leaves macOS the rest. [What fits](#what-fits-on-a-mac) says how many machines that holds.
- At least 40 GB of free disk: the machine's disk is 20 GB at least, and 20 GB of the volume stays free for you. The download is about 85 MB; with less free, `repose host add` says how much to free. From there a machine costs what it actually writes, up to the size of its disk (40 GB for a `large`, [the sizes](/docs/machine)), and its snapshots are kept here too.
- A network connection and no sleep. Repose keeps the Mac awake while it holds your machines; a Mac that sleeps or loses Wi-Fi goes [offline](#when-your-hardware-is-offline) and your machines are out of reach until it is back.
- No inbound ports, no public address, nothing to forward on your router. The Mac dials out to repose.

Repose runs on your Mac as one virtual machine, and nothing of yours is touched: no disk is wiped, no system setting is changed, and your Mac stays a Mac. The one thing outside your home folder is the launchd job that starts the virtual machine when the Mac boots, which is why `repose host add` asks for your password once. Remove the host and the virtual machine goes with it.

## What fits on a Mac

Repose's virtual machine takes three quarters of the Mac's memory, and macOS keeps the other quarter: 4 GB of 16, 6 GB of 24. The virtual machine holds its share for as long as it runs, whether machines run in it or not.

Of that share, repose keeps an eighth for itself, never less than 2 GB, and each machine counts its size plus half a gigabyte. So this is what runs at once:

| Mac | Machines at once |
|---|---|
| 16 GB | 1 `large`, or 2 `small`, or 3 `tiny` |
| 24 GB | 1 `large` and 1 `small`, or 3 `small`, or 6 `tiny` |

An `xl` fits on neither; it needs a Mac with 32 GB. The 24 GB row is measured, on an M4 Mac mini on 2026-11-22; the 16 GB row is the same rule worked out. `--memory` at `repose host add` changes the share, and the same rule applies to what you give it. `repose host ls` shows the share under MEMORY. A machine that does not fit is refused with exit code 8, and the refusal names the host, the memory it has free and what the machine needs: `your host mac-mini has 5 GB of memory free for machines, and a large machine needs 8 GB`.

## What an x86 box needs

- x86_64 with hardware virtualization and `/dev/kvm`. Bare metal, Intel or AMD. A virtual machine works only if its own host passes virtualization through, which on Proxmox or libvirt is one setting; most cheap VPSes do not.
- Ubuntu 24.04, or Debian 12 or later, and room for your machines' disks: 40 GB free on the system disk, or a whole empty disk.
- A kernel with the fix for CVE-2026-99999, through which code in one of your machines could take over the box. Ubuntu 24.04's kernels carry it from 6.8.0-149 (`sudo apt-get update && sudo apt-get install linux-image-generic`, then reboot and check that `uname -r` shows 6.8.0-149 or later) and its HWE 7.0 kernels from 7.0.0-43; Debian 13's from 6.12.113-1; Debian 12's did not on 2026-11-23. The installer knows these versions, and for any other kernel it reads the package changelog for the CVE or `ipv4: validate checksum_start before completing checksum`. When the kernel lacks the fix, or the installer could not check, it says which at the end and installs anyway, since the only machines on the box are yours; run it again after the reboot to check.
- 16 GB of memory or more. Repose takes the whole box and keeps an eighth of its memory, never less than 2 GB, for itself; the rest is what your machines may hold at once, each counting its size plus half a gigabyte. The number of cores does not limit how many machines run; memory and disk do.
- Outbound UDP 51820 and TCP 443. No inbound port.

## Adding it

On the Mac mini, with the [CLI installed](/docs/cli) and logged in:

```shell
repose host add
```

It says what it is about to do, then does it: fetches [vfkit](https://github.com/crc-org/vfkit) and repose's virtual machine image (about 85 MB), puts them under `~/Library/Application Support/repose`, and starts the machine from a launchd job that starts it again whenever the Mac boots, before anyone logs in. sudo asks for your password once, to put that job in `/Library/LaunchDaemons`; the job runs as you, not as root. Without sudo, the job runs at your login instead, and `repose host add` says so. macOS lists the job under System Settings → General → Login Items, as allowed in the background; turning it off there keeps the machine off. While the machine runs, the Mac does not sleep.

A download that fails is tried twice more, continuing from the bytes already on disk rather than fetching the image again, and one that goes quiet for a minute is given up rather than held open. If all three attempts fail, `repose host add` says what failed and stops. The host is in your account by then, so the command that finishes the install is the one the failure prints, `repose host add --join-file <path>`, which continues from the part of the image already here. The install keeps the argument in that file so you do not have to paste it again, and so it stays off the command line: anything on the Mac can read a command line while it runs. Nothing is running and nothing of the Mac's has changed; to start over instead, `repose host rm` takes the host off your account and the part of the image on disk with it, and `repose host rm --local` clears that part without touching your account.

From any other machine, `repose host add` prints one step and nothing else, so you can pipe it: `repose host add --join <argument>`, to run on the Mac. It carries a token that works once, within 24 hours, and the machine's place on repose's network. Treat it as a password until the machine has joined: anyone who runs it adds a machine to your account, and anything running on the Mac can read it from the command line while the install does. To keep it off there, put it in a file and use `repose host add --join-file <path>`, or pipe it to `repose host add` and give `--join` a lone dash, which means standard input. If you lose it, or let it go stale, `repose host rm` and `repose host add` again; the old step then opens nothing. `repose host add --json` never prints the argument, so a script or an agent that reads the output never holds the key: on the Mac it installs, and elsewhere it puts the argument in `~/.config/repose/join-NAME`, readable by you alone, and names that file instead.

On an x86 box, run `repose host add` on the box itself. It puts the argument in `~/.config/repose/join-NAME`, readable by you alone, and prints the installer, to run there as root:

```shell
curl -fsSL https://repose.run/install-host.sh \
  | sudo bash -s -- --join-file '/home/you/.config/repose/join-box'
```

It sets repose up and joins your account. The argument stays in the file so that it is on no screen, no command line and not in the copy of the command sudo logs, and an agent you ask to run the line never holds the key; the installer deletes the file once it has read it. To add another x86 box from this one, copy the file there and give its path. It keeps your machines' disks in `/var/lib/repose/guests.img`, 100 GB or the free space less 20 GB, whichever is smaller; with `--device /dev/sdb` it takes over that whole disk instead, and it refuses a disk that holds anything. The dashboard's **Add a host** shows the line with the argument in it, `printf '%s\n' <argument> > repose-join && curl -fsSL https://repose.run/install-host.sh | sudo bash -s -- --join-file repose-join`, for you to paste into the box's terminal; don't paste it into an agent's conversation.

The machine's disk is one file, `disk.raw`, 100 GB unless the volume has less free, never under 20 GB and never closer than 20 GB to what the volume has (`--disk` sets it); it takes space only as your machines fill it. The machine gets three quarters of the Mac's memory, 12 GB of 16 and 18 GB of 24, and all but two cores (`--memory`, `--cpus`). Its logs are in `~/Library/Logs/repose` and hold nothing of yours beyond the ids repose logs.

The machine sets repose up and joins your account:

```shell
repose host ls
```

```
HOST      STATE   MEMORY  MACHINES  RUNNING  LAST SEEN
mac-mini  online  24 GB   0         0        just now
```

`waiting` means it has not joined yet, and a new machine is refused until it has: `your host mac-mini has not registered yet`, exit 8, and nothing is created. `repose run --host repose` makes the machine on repose's servers meanwhile on a hosted plan, or [remove the host](#taking-it-off) and add it again if the install stalled ([troubleshooting](/docs/troubleshooting#your-own-hardware)). From `online`, `repose run` in any project puts it on your hardware.

`repose ls` then says where each machine runs:

```
$ repose ls
PROJECT     SIZE   STATE    UP     AGENTS           HOST
todo-app *  large  running  2h14m  claude: working  mac-mini
api         small  running  3d     -                repose
```

`repose status` says it in a `host` row, and a project's `--json` carries `host_name` beside `host_id`.

To name it yourself, `repose host add mac-mini`. A name is 1 to 32 characters of `a-z`, `0-9` and `-`. It is yours alone: another account may use the same one.

On the dashboard, **New project** has **Runs on** under **Options** once you have a host: your host, or repose's servers, the same choice as `--host`. The line under the name says where the machine will run before you open it. With more than one host it lists each with its state, after **Any of your hosts**, the default, which takes the one with the most room. While your host is waiting or offline the form says so in the same words and creates nothing, and repose's servers is the way out. Settings → Hosts does the same three things. **Add a host** takes a name, or none, and shows the step to run on the machine once, with a copy button; it is not shown again, and a host that is still waiting can be removed and added again for a fresh one. The list shows each host's state, its memory and cores, how many of your machines are on it and how many are running, and when it last answered.

## Starting a machine takes about half a minute

A machine on your Mac starts in about 33 seconds, and a new one is ready in
about 40 seconds once the base image is on the Mac. The first machine also
downloads that image, 2.9 GB, so it takes about two minutes. On repose's own
servers the same machine starts in about
[5 seconds](/docs#6-come-back-and-stop). Measured on an M4 Mac mini on
2026-11-22.

Once it is running it is a machine like any other. Agents start as fast as on
repose's servers: on an M4 Mac mini a small machine showed Claude Code's first
screen in half a second and Gemini CLI's in 1.5 to 2 seconds, and opencode,
started with `repose run -d --agent opencode -p`, wrote a one-line file 31 to
40 seconds after the command. Measured on 2026-11-23.

## When your hardware is offline

A Mac that is asleep, rebooting, off, or off the network stops answering repose. After 90 seconds `repose host ls` says `offline` and names how long it has been quiet:

```
HOST      STATE    MEMORY  MACHINES  RUNNING  LAST SEEN
mac-mini  offline  24 GB   2         1        14m ago
```

`repose ls` says the same under each machine on it, and so does the dashboard:

```
PROJECT     SIZE   STATE    UP     AGENTS
todo-app *  large  running  2h14m  claude: working
todo-app: your host mac-mini has not answered for 14m.
```

`repose run` and `repose attach` wait up to 10 minutes for the host and then give up; `repose start` is refused while it is quiet, because a stopped machine's disk is on that host and nowhere else. A new machine is refused with the same sentence, and `repose run --host repose` makes it on repose's servers instead.

A Mac that is only off the network keeps running your machines, and they are reachable again, with whatever the agents did meanwhile, as soon as it answers. A Mac you restart, shut down or lose power on stops them, and repose starts the ones that were running again once it is back; what they held in memory is gone, what they wrote to disk is not. If the hardware is gone for good, [retire the offline host](#taking-it-off) with `repose host rm <name>` to free your host slot and clear its stranded machines with `repose rm`.

## Restart your Mac and your machines start fresh

When you restart or shut down your Mac, or it loses power, your machines stop. When it is back, the virtual machine starts with the Mac, with nobody logged in, and repose starts the machines that were running, cold: their disks are as they were, and what they held in memory is gone: programs, tmux windows, an agent mid-task. Stop anything you would miss before you restart, the way you would on a laptop.

Two things keep the machines off until someone is at the Mac:

- **FileVault.** With FileVault on, macOS waits at its unlock screen after a power cut, before it starts anything. A restart you begin with `sudo fdesetup authrestart` unlocks itself once and comes back on its own.
- **No sudo at `repose host add`.** The job then starts at your login, and `repose host ls` on the Mac says so. `repose host boot` moves it to boot once you can use sudo; the virtual machine restarts, and the machines on it start fresh.

macOS gives repose about a second when the Mac goes down, measured, and writing a machine's memory takes longer than that. Machines on repose's servers do keep their memory across a server restart ([when a machine's server restarts](/docs/lifecycle#when-a-machines-server-restarts)); your own Mac does not, today.

## Snapshots stay on its disk

Snapshots of machines on repose's servers go to cloud storage. **Snapshots of machines on your hardware are written to that machine's own disk, and repose keeps no copy.** If the disk dies, every snapshot of every machine on it dies with it, and so do the machines. Back the disk up yourself, or keep in your git remote anything you would miss. They share the space the machines' disks use: a volume of half of it, which costs what the snapshots hold.

This is on purpose. The alternative is the keys to a storage bucket sitting on a machine in your house, which is a kind of credential repose does not keep anywhere ([secrets](/docs/secrets)).

So a destroyed machine's final snapshot is kept on the host too, for 30 days while the host is there: `repose rm` says `Its final snapshot is kept on your host mac-mini; removing the host deletes it.` [Taking the host off](#taking-it-off) deletes every snapshot on it. After that `repose ls --destroyed` and the dashboard show `gone with host mac-mini` in place of a date, and a restore is refused with `That snapshot was kept on your host mac-mini, which was removed, so it is gone; repose had no copy` and creates nothing.

## What runs on it, and what it reaches

Only your own projects run on your hardware; nobody else's is ever placed there. On repose's network it reaches the control plane and nothing else: not another host, not the gateway's own services. Repose reaches it over the tunnel the install sets up, the same way it reaches its own servers, with no port open on your side.

## Taking it off

```shell
repose host rm mac-mini
```

Repose asks once, naming how many snapshots the host keeps (`This deletes the 2 snapshots kept on it; repose has no copy.`), then stops placing projects there and retires the host. Run on the Mac that runs it, the command also stops the machine and removes it, disk and snapshots included, and sudo asks for your password to remove the launchd job; run anywhere else, the machine itself is untouched and keeps running until you turn it off, and `repose host rm --local` on the Mac removes it.

A host that is answering and still holds machines is not removed. A machine does not move to other hardware, so `repose rm` each of them first (keep in your git remote anything you would miss), and `repose host rm` then tells you which are in the way.

A host that has **stopped** answering is removed, machines and all, because the alternative is a dead end: the machines on it cannot be destroyed while repose still believes the host may come back, so refusing to remove the host would leave you unable to do either, or to add a replacement. The question says how many machines go with it and that their disks were on that host; `--yes` skips it, as everywhere. Afterwards the machines are still listed and `repose rm` finishes each of them without the host. If the Mac was only asleep, turn it on and wait for `online` instead: once a host is removed it cannot rejoin, and whatever was on its disk is gone.

Removing the appliance first is the other order, and it takes one more step. `repose host rm --local` works with no session and with repose unreachable, because the appliance is on your Mac and removing it needs nobody's permission. The question it asks says how many machines go with it when your account can be read, and says that it could not be read when it cannot. The host stays on your account either way, holding machines that no longer have anywhere to run: wait until `repose host ls` shows it `offline` (about 90 seconds), run `repose host rm <name>` to retire it, and then `repose rm` each stranded machine.

Settings → Hosts has the same Remove, with the same question and the same machines named.

On an x86 box, removing the host leaves repose running there, holding its disk. `repose host rm` prints the line that takes it off, to run on the box as root:

```shell
curl -fsSL https://repose.run/install-host.sh \
  | sudo bash -s -- --uninstall
```

It stops hostd and the machines, and removes the machines' disks with `/var/lib/repose/guests.img`, or releases the `--device` disk with no signature left on it (its bytes are not overwritten). It also removes the tunnel, repose's firewall tables, its services, programs, accounts and files. The packages it installed from apt stay, and the kernel settings it changed, IP forwarding among them, last until the next boot. It refuses while the box is still a host on your account, so `repose host rm` comes first; `--force` skips that, and the host then stays on your account, offline, until you remove it. A second run finds nothing to do. `repose host rm --local` on the box prints the same line rather than running it.

Once a host is off, your new projects go to repose's servers again if your plan buys memory there.

## What it costs

The [Own hardware plan](/docs/billing#own-hardware) is **$10 a month** for any number of hosts, with seven days free first (prices exclude tax). It buys no memory, disk or egress on repose's servers, so you cannot place machines there on this plan: your hardware's memory is the only cap on what runs at once, your disk the only cap on what you keep, and egress is not counted. Repose runs the gateway your SSH goes through, the control plane, the dashboard, notifications and triggers, which is what the plan pays for. There is no seat to wait for while repose is full.
