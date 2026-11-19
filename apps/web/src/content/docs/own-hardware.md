---
title: Your own hardware
description: Run your projects on a Mac mini or an x86 box you own, on the Own hardware plan.
section: Account
order: 29
---

Repose normally runs your machines on its own servers. On the [Own hardware plan](/docs/billing) it runs them on hardware you own instead: a Mac mini on your desk, or an x86 box in a cupboard. Everything else is unchanged. You still `repose run`, still reach your machines over SSH through repose's gateway, still get the notifications and the [triggers](/docs/triggers). The difference is whose hardware your agents burn.

Your hardware takes only your projects. Nobody else's machine is ever placed on it, and once you have added one, your new projects go there rather than on repose's servers. A project made through the [API](/docs/api) can ask for repose's servers instead, with `placement: "hosted"`, if your plan buys memory there.

## What a Mac mini needs

- An M3 chip or newer. Nested virtualization, which repose needs to run your machines, arrived with M3; an M1 or M2 cannot do it.
- macOS 15 or later.
- 16 GB of memory or more. Your machines run inside repose's virtual machine on the Mac, which takes a share of the memory and leaves macOS the rest; `repose host ls` shows that share.
- Free disk for your machines' volumes and their snapshots. A `large` machine's disk is 40 GB ([the sizes](/docs/machine)), and repose's own system takes a few gigabytes more.
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

Run this on your laptop, in any folder:

```shell
repose host add
```

It prints one step and nothing else, so you can pipe it. Carry that step to the machine you are adding and run it there. It carries a token that works once, within 24 hours, and the machine's place on repose's network. Treat it as a password until the machine has joined: anyone who runs it adds a machine to your account. If you lose it, or let it go stale, `repose host rm` and `repose host add` again; the old step then opens nothing.

On a Mac mini the step is:

```shell
curl -fsSL https://repose.run/mac | sh -s -- <the argument it printed>
```

It fetches [vfkit](https://github.com/crc-org/vfkit) and repose's virtual machine image (about 1.5 GB), puts them under `~/Library/Application Support/repose`, and starts the machine from a launchd agent that starts it again at every login. While the machine runs, the Mac does not sleep. The machine's disk is one file there, `disk.raw`, 100 GB unless the volume has less free (`REPOSE_DISK_GB` sets it); it takes space only as your machines fill it, and `repose-appliance grow 200` makes it larger later. The machine gets three quarters of the Mac's memory, 12 GB of 16 and 18 GB of 24, and all but two cores (`REPOSE_VM_MEMORY_MIB`, `REPOSE_VM_CPUS`).

`~/Library/Application Support/repose/bin/repose-appliance` is the launcher: `start`, `stop`, `status`, `log` (the machine's console), `grow GB`, and `uninstall`, which removes the machine and its disk, every snapshot on it included, after one question. Its logs are in `~/Library/Logs/repose` and hold nothing of yours beyond the ids repose logs.

The machine sets repose up and joins your account:

```shell
repose host ls
```

```
HOST      STATE   MEMORY  MACHINES  RUNNING  LAST SEEN
mac-mini  online  24 GB   0         0        just now
```

`waiting` means it has not joined yet. From `online`, `repose run` in any project puts it on your hardware.

To name it yourself, `repose host add mac-mini`. A name is 1 to 32 characters of `a-z`, `0-9` and `-`. It is yours alone: another account may use the same one.

On the dashboard, Settings → Hosts does the same three things. **Add a host** takes a name, or none, and shows the step to run on the machine once, with a copy button; it is not shown again, and a host that is still waiting can be removed and added again for a fresh one. The list shows each host's state, its memory and cores, how many of your machines are on it and how many are running, and when it last answered.

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

`repose run` and `repose attach` wait up to 10 minutes for the host and then give up; `repose start` is refused while it is quiet, because a stopped machine's disk is on that host and nowhere else.

A Mac that is only off the network keeps running your machines, and they are reachable again, with whatever the agents did meanwhile, as soon as it answers. A Mac that was switched off or rebooted stops them, and repose starts the ones that were running again once it is back; what they held in memory is gone, what they wrote to disk is not. `repose-appliance stop` and a shutdown of the Mac give your machines 20 seconds to finish writing before they stop.

## Snapshots stay on its disk

Snapshots of machines on repose's servers go to cloud storage. **Snapshots of machines on your hardware are written to that machine's own disk, and repose keeps no copy.** If the disk dies, every snapshot of every machine on it dies with it, and so do the machines. Back the disk up yourself, or keep in your git remote anything you would miss.

This is on purpose. The alternative is the keys to a storage bucket sitting on a machine in your house, which is a kind of credential repose does not keep anywhere ([secrets](/docs/secrets)).

## What runs on it, and what it reaches

Only your own projects run on your hardware; nobody else's is ever placed there. On repose's network it reaches the control plane and nothing else: not another host, not the gateway's own services. Repose reaches it over the tunnel the install sets up, the same way it reaches its own servers, with no port open on your side.

## Taking it off

```shell
repose host rm mac-mini
```

Repose asks once, then stops placing projects there and retires the host. The machine itself is untouched: nothing of yours on it is deleted, and it keeps running until you turn it off. On a Mac, `repose-appliance uninstall` turns it off and removes it, disk included.

A host still holding machines is not removed. A machine moves hardware only by [restoring a snapshot](/docs/lifecycle#destroy-and-restore) elsewhere, so `repose rm` or restore each of them first, and `repose host rm` then tells you which are in the way.

Settings → Hosts has the same Remove, and names the machines that are in the way.

Once a host is off, your new projects go to repose's servers again if your plan buys memory there.

## What it costs

The [Own hardware plan](/docs/billing) is one host per account. It buys no memory and no disk of repose's: your hardware's memory is the only cap on what runs at once, and your disk the only cap on what you keep. Repose still runs the gateway your SSH goes through, the control plane, the notifications and the triggers, which is what the plan pays for.
