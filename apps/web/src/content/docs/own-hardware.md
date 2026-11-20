---
title: Your own hardware
description: Run your projects on a Mac mini or an x86 box you own, on the Own hardware plan.
section: Account
order: 29
---

Repose normally runs your machines on its own servers. On the [Own hardware plan](/docs/billing) it runs them on hardware you own instead: a Mac mini on your desk, or an x86 box in a cupboard. Everything else is unchanged. You still `repose run`, still reach your machines over SSH through repose's gateway, still get the notifications and the [triggers](/docs/triggers). The difference is whose hardware your agents burn.

Your hardware takes only your projects. Nobody else's machine is ever placed on it, and once you have added one, your new projects go there rather than on repose's servers. `repose run --host repose` makes one on repose's servers instead, `repose run --host mac-mini` names your hardware, and a project made through the [API](/docs/api#where-a-project-runs) says the same with `placement`. Repose's servers need a plan that buys memory there. A machine stays on its host for life; it moves only by [restoring a snapshot](/docs/lifecycle#destroy-and-restore).

## What a Mac mini needs

- An M3 chip or newer. Nested virtualization, which repose needs to run your machines, arrived with M3; an M1 or M2 cannot do it.
- macOS 15 or later.
- 16 GB of memory or more. Your machines run inside repose's virtual machine on the Mac, which takes a share of the memory and leaves macOS the rest. On a 16 GB Mac that share is about 12 GB, of which about 9.6 GB is what your machines may hold at once; repose keeps the rest for itself. `repose host ls` shows the share.
- About 60 GB of free disk to begin with. The appliance's disk is a 40 GB file at least (100 GB when the volume has the room) that takes space only as your machines fill it, and 20 GB of the volume stays free for you; with less, `repose host add` says how much to free. Installing repose peaks at about 6.6 GB while the download is still on disk, and settles under 6 GB. From there a machine costs what it actually writes, up to the size of its disk (40 GB for a `large`, [the sizes](/docs/machine)), and its snapshots are kept here too.
- A network connection and no sleep. Repose keeps the Mac awake while it holds your machines; a Mac that sleeps or loses Wi-Fi goes [offline](#when-your-hardware-is-offline) and your machines are out of reach until it is back.
- No inbound ports, no public address, nothing to forward on your router. The Mac dials out to repose.

Repose runs on your Mac as one virtual machine, and nothing of yours is touched: no disk is wiped, no system setting is changed, nothing needs your password, and your Mac stays a Mac. Remove the host and the virtual machine goes with it.

## What an x86 box needs

- x86_64 with hardware virtualization and `/dev/kvm`. Bare metal, Intel or AMD. A virtual machine works only if its own host passes virtualization through, which on Proxmox or libvirt is one setting; most cheap VPSes do not.
- A machine you are willing to give away whole. Repose installs NixOS over what is there and wipes the disks it is given.
- Two disks: one for the system, one for your machines' volumes.
- 16 GB of memory or more. Repose takes the whole box and keeps an eighth of its memory, never less than 2 GB, for itself; the rest is what your machines may hold at once.
- Outbound UDP 51820 and TCP 443. No inbound port.

## Adding it

On the Mac mini, with the [CLI installed](/docs/cli) and logged in:

```shell
repose host add
```

It says what it is about to do, then does it: fetches [vfkit](https://github.com/crc-org/vfkit) and repose's virtual machine image (about 2 GB), puts them under `~/Library/Application Support/repose`, and starts the machine from a launchd agent that starts it again at every login. While the machine runs, the Mac does not sleep. Nothing needs your password.

A download that fails is tried twice more, continuing from the bytes already on disk rather than fetching the 2 GB again, and one that goes quiet for a minute is given up rather than held open. If all three attempts fail, `repose host add` says what failed and stops. The host is in your account by then, so the command that finishes the install is the one the failure prints, `repose host add --join-file <path>`, which continues from the part of the image already here. The install keeps the argument in that file so you do not have to paste it again, and so it stays off the command line: anything on the Mac can read a command line while it runs. Nothing is running and nothing of the Mac's has changed; to start over instead, `repose host rm` takes the host off your account and the part of the image on disk with it, and `repose host rm --local` clears that part without touching your account.

From any other machine, `repose host add` prints one step and nothing else, so you can pipe it: `repose host add --join <argument>`, to run on the Mac. It carries a token that works once, within 24 hours, and the machine's place on repose's network. Treat it as a password until the machine has joined: anyone who runs it adds a machine to your account, and anything running on the Mac can read it from the command line while the install does. To keep it off there, put it in a file and use `repose host add --join-file <path>`, or pipe it to `repose host add --join -`. If you lose it, or let it go stale, `repose host rm` and `repose host add` again; the old step then opens nothing.

The machine's disk is one file, `disk.raw`, 100 GB unless the volume has less free, never under 40 GB and never closer than 20 GB to what the volume has (`--disk` sets it); it takes space only as your machines fill it. The machine gets three quarters of the Mac's memory, 12 GB of 16 and 18 GB of 24, and all but two cores (`--memory`, `--cpus`). Its logs are in `~/Library/Logs/repose` and hold nothing of yours beyond the ids repose logs.

The machine sets repose up and joins your account:

```shell
repose host ls
```

```
HOST      STATE   MEMORY  MACHINES  RUNNING  LAST SEEN
mac-mini  online  24 GB   0         0        just now
```

`waiting` means it has not joined yet, and a new machine is refused until it has: `your host mac-mini has not registered yet`, exit 8, and nothing is created. `repose run --host repose` makes the machine on repose's servers meanwhile. From `online`, `repose run` in any project puts it on your hardware.

`repose ls` then says where each machine runs:

```
$ repose ls
PROJECT     SIZE   STATE    UP     AGENTS           HOST
todo-app *  large  running  2h14m  claude: working  mac-mini
api         small  running  3d     -                repose
```

`repose status` says it in a `host` row, and a project's `--json` carries `host_name` beside `host_id`.

To name it yourself, `repose host add mac-mini`. A name is 1 to 32 characters of `a-z`, `0-9` and `-`. It is yours alone: another account may use the same one.

On the dashboard, **New project** has **Runs on** once you have a host: your host, or repose's servers, the same choice as `--host`. While your host is waiting or offline the form says so in the same words and creates nothing, and repose's servers is the way out. Settings → Hosts does the same three things. **Add a host** takes a name, or none, and shows the step to run on the machine once, with a copy button; it is not shown again, and a host that is still waiting can be removed and added again for a fresh one. The list shows each host's state, its memory and cores, how many of your machines are on it and how many are running, and when it last answered.

## Starting a machine takes about four minutes

A machine on your Mac takes about four minutes to start, the first time and
every time after. On repose's own servers the same machine starts in about
[12 seconds](/docs#6-come-back-and-stop). The difference is work repose redoes on the
Mac at every start, not something the machine carries once it is up.

Once it is running it is a machine like any other. The agents, the builds and
the tests go at the Mac's own speed: the virtual machine around them costs
nothing we can measure.

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

A Mac that is only off the network keeps running your machines, and they are reachable again, with whatever the agents did meanwhile, as soon as it answers. A Mac you restart, shut down or lose power on stops them, and repose starts the ones that were running again once it is back; what they held in memory is gone, what they wrote to disk is not.

## Restart your Mac and your machines start fresh

When you restart or shut down your Mac, your machines stop and start again at your next login, cold: their disks are as they were, and what they held in memory is gone: programs, tmux windows, an agent mid-task. Stop anything you would miss before you restart, the way you would on a laptop.

Keeping a machine's memory across a restart is built and it does not work yet, so this page does not promise it: macOS gives repose about a second when the Mac goes down, measured, and writing a machine's memory takes longer than that. Machines on repose's servers do keep their memory across a server restart ([when a machine's server restarts](/docs/lifecycle#when-a-machines-server-restarts)); your own Mac does not, today.

## Snapshots stay on its disk

Snapshots of machines on repose's servers go to cloud storage. **Snapshots of machines on your hardware are written to that machine's own disk, and repose keeps no copy.** If the disk dies, every snapshot of every machine on it dies with it, and so do the machines. Back the disk up yourself, or keep in your git remote anything you would miss.

This is on purpose. The alternative is the keys to a storage bucket sitting on a machine in your house, which is a kind of credential repose does not keep anywhere ([secrets](/docs/secrets)).

## What runs on it, and what it reaches

Only your own projects run on your hardware; nobody else's is ever placed there. On repose's network it reaches the control plane and nothing else: not another host, not the gateway's own services. Repose reaches it over the tunnel the install sets up, the same way it reaches its own servers, with no port open on your side.

## Taking it off

```shell
repose host rm mac-mini
```

Repose asks once, then stops placing projects there and retires the host. Run on the Mac that runs it, the command also stops the machine and removes it, disk and snapshots included; run anywhere else, the machine itself is untouched and keeps running until you turn it off, and `repose host rm --local` on the Mac removes it.

A host that is answering and still holds machines is not removed. A machine moves hardware only by [restoring a snapshot](/docs/lifecycle#destroy-and-restore) elsewhere, so `repose rm` each of them first, and `repose host rm` then tells you which are in the way.

A host that has **stopped** answering is removed, machines and all, because the alternative is a dead end: the machines on it cannot be destroyed while repose still believes the host may come back, so refusing to remove the host would leave you unable to do either, or to add a replacement. The question says how many machines go with it and that their disks were on that host; `--yes` skips it, as everywhere. Afterwards the machines are still listed and `repose rm` finishes each of them without the host. If the Mac was only asleep, turn it on and wait for `online` instead: once a host is removed it cannot rejoin, and whatever was on its disk is gone.

Settings → Hosts has the same Remove, with the same question and the same machines named.

Once a host is off, your new projects go to repose's servers again if your plan buys memory there.

## What it costs

The [Own hardware plan](/docs/billing) is one host per account. It buys no memory and no disk of repose's: your hardware's memory is the only cap on what runs at once, and your disk the only cap on what you keep. Repose still runs the gateway your SSH goes through, the control plane, the notifications and the triggers, which is what the plan pays for.
