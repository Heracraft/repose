---
title: Your own hardware
description: Run your projects on a Mac mini or an x86 box you own, on the Own hardware plan.
section: Account
order: 29
---

Repose normally runs your machines on its own servers. On the [Own hardware plan](/docs/billing#own-hardware) ($10 a month) it runs them on hardware you own instead: a Mac mini on your desk, or an x86 box in a cupboard. Everything else is unchanged. You still `repose run`, still reach your machines over SSH through repose's gateway, still get the notifications and the [triggers](/docs/triggers). The difference is whose hardware your agents burn.

Your hardware takes only your projects. Nobody else's machine is ever placed on it, and once you have added one, your new projects go there rather than on repose's servers. `repose run --host repose` makes one on repose's servers instead, `repose run --host mac-mini` names your hardware, and a project made through the [API](/docs/api#where-a-project-runs) says the same with `placement` and `host`. With more than one host, a new machine goes to the one with the most room, and `--host box-b` puts it on box-b or refuses with box-b's state and exit 8; it never puts it on another host. The create line names the host a machine went to: `✓ Created todo-app on box-a`. Repose's servers need a plan that buys memory there. A machine stays on its host for life. It does not move to repose's servers or to another host, and a machine on repose's servers does not move to your hardware: a snapshot is only readable where it was taken.

## What a Mac needs

- Apple silicon: any M-series chip. Each machine is a virtual machine of its own under macOS's Virtualization framework, so no nested virtualization is needed.
- macOS 14 or later.
- 8 GB of memory or more. macOS keeps 4 GB, and your machines share the rest ([What fits](#what-fits-on-a-mac)).
- At least 10 GB of free disk: repose itself is about 50 MB, the first machine fetches its base image, about 3 GB, and from there a machine costs what it actually writes, up to the size of its disk (40 GB to begin with), and its snapshots are kept here too.
- A network connection and no sleep. While a machine runs or rests, repose keeps the Mac from sleeping on its own; the display still sleeps. Closing a MacBook's lid puts it to sleep all the same. A Mac that sleeps or loses Wi-Fi goes [offline](#when-your-hardware-is-offline) and your machines are out of reach until it is back.
- No inbound ports, no public address, nothing to forward on your router. The Mac dials out to repose.

Nothing of yours is touched: no disk is wiped, no system setting is changed, and your Mac stays a Mac. Repose lives in `/Library/Application Support/repose`, owned by root, and runs from two launchd daemons, which is why `repose host add` asks for your password once. Remove the host and all of it goes.

## What fits on a Mac

Each machine on a Mac is a virtual machine of its own. When it starts it gets half the Mac's cores, at least two, and half its memory, at most 8 GB: 5 cores and 8 GB on a ten-core Mac mini with 24 GB. macOS keeps 4 GB for itself, and a machine starts only while macOS reports at least 1 GB free, as on any host of your own. The running machines together hold at most two cores more than the Mac has, and their memory and macOS's 4 GB never more than the Mac has, so three machines at once on that Mac mini never touched swap.

A machine that starts while the others hold most of that gets what is left, down to two cores and 2 GB, and says so: `repose start`, `repose ls`, `repose status` and the machine's page on the dashboard print `runs with 2 vCPUs and 3.9 GB of its 5 and 8.0 GB: the machines running beside it held the rest when it started. A start after one of them stops gives it all.` A virtual machine's size cannot change while it runs, so that is the way to give it more. With less than 2 GB left, the start or the create is refused with exit code 8 and nothing changes: `the machines running on this host hold the memory it gives its machines; stopping a running machine frees room`. A refused start leaves the machine stopped, as it was.

A running machine keeps the memory it has used until it stops or rests: macOS does not take it back from a virtual machine while it runs, and a machine woken from a rest holds its whole size until its next rest. A stopped machine gives all of it back at once. A machine nobody uses rests ([frozen](/docs/lifecycle)), first with its memory held, so the next connection wakes it in under a second. After 30 minutes of rest, or at once when macOS has less than a quarter of its memory free, and while the Mac's screen is unlocked, repose saves its memory to a file that macOS encrypts with a key only your own unlocked session on that Mac can use, and ends its virtual machine, so macOS has all of that memory back. The first connection after that reads the file back: on an M4 Mac mini, 3.3 to 3.7 seconds for an idle 8 GB machine, and about 10 to 15 seconds for one that had been running builds (its restore took 9.8 to 14.2 seconds), since the time grows with the memory the machine has used; the connection waits for it. When the screen locks, repose reads resting machines back while they fit with room left for one more machine, because a locked Mac cannot read the file; a machine read back holds its whole size, and rests with its memory held until a minute after you unlock the Mac. The ones that do not fit stay saved, holding nothing: `repose ls` says `resting; it wakes once you unlock mac-mini`, and a connection to one says the same at once. macOS locks the screen by itself 20 to 25 minutes after the last use of a Mac left at its settings. A resting file is never kept longer than six hours: past that the machine is read back and rests with its memory held, or, on a locked Mac, it stops, `repose ls` says why, and `repose start` starts it fresh.

On an M4 Mac mini, Go builds in three machines at once took 39 to 69 seconds each (the slowest in a machine that started with two cores), and node builds 50 to 63 seconds, with nothing stopped and no swap. Measured on 2026-11-26.

If memory runs out even so, repose stops one machine, the one whose memory grew most in the last half minute, or the one started most recently when none grew, and never its own services: when the host has had under 0.5 GB free for five seconds, so the machines never hang waiting for memory. On an x86 box that is not itself a virtual machine, the least used memory is compressed first, and the machines slow down before one is stopped. Its disk is kept. `repose ls`, `repose status` and the project's page say which machine stopped and why (`stopped: your host mac-mini ran out of memory, so repose stopped todo-app to keep the others running`), you get a notification, and `repose start` starts it again. A machine that stops answering for any other reason says so too ([Not answering](/docs/troubleshooting#machine-state)).

A new machine's disk is 40 GB. On a Mac it grows only while the machine is stopped: `repose resize 80G` on a running machine is refused with `a machine on this host gets a bigger disk while it is stopped: stop it, resize, and start it again`, and the next start fills the bigger disk out.

`repose ls` and `repose status` show what each machine uses, under MEMORY (on a Mac, what macOS gives up for it: `0 B held` while it rests as a save), and the dashboard shows the same. Repose updates itself within about half an hour, a Mac and an x86 machine alike: nothing to reinstall or run again, and the machines keep running through the update. `--size` on `repose run`, `repose sync` and `repose resize` is accepted and changes nothing on your hardware: the command says so in one line, and no line about the machine names a size.

## What an x86 box needs

- x86_64 with hardware virtualization and `/dev/kvm`. Bare metal, Intel or AMD. A virtual machine works only if its own host passes virtualization through, which on Proxmox or libvirt is one setting; most cheap VPSes do not.
- Ubuntu 24.04, or Debian 12 or later, and room for your machines' disks: 40 GB free on the system disk, or a whole empty disk.
- A kernel with the fix for CVE-2026-99999, through which code in one of your machines could take over the box. Ubuntu 24.04's kernels carry it from 6.8.0-149 (`sudo apt-get update && sudo apt-get install linux-image-generic`, then reboot and check that `uname -r` shows 6.8.0-149 or later) and its HWE 7.0 kernels from 7.0.0-43; Debian 13's from 6.12.113-1; Debian 12's did not on 2026-11-23. The installer knows these versions, and for any other kernel it reads the package changelog for the CVE or `ipv4: validate checksum_start before completing checksum`. When the kernel lacks the fix, or the installer could not check, it says which at the end and installs anyway, since the only machines on the box are yours; run it again after the reboot to check.
- 16 GB of memory or more. Repose takes the whole box. Your machines share its memory: a machine has no size and may use all of it, and a new one starts while the box has at least 1 GB free. Otherwise the start or the create is refused with exit code 8, naming the host: `Your host box is full.` and, on the next line, `Stop a machine there.` Each machine has every core of the box, and the number of cores does not limit how many run. A box that is itself a virtual machine (a cloud VM) gives each machine two cores, and runs machines at once while they hold at least two fewer cores than it has; a start past that is refused with exit code 8, naming the machines that hold the cores. Compressing memory needs the kernel's zram module, which Ubuntu's and Debian's own kernels have; without it the installer warns `no compressed swap (zram)`, and running out of memory stops a machine at once instead of slowing them first.
- Outbound UDP 51820 and TCP 443. No inbound port.

## Adding it

On the Mac mini, with the [CLI installed](/docs/cli) and logged in:

```shell
repose host add
```

It says what it is about to do, then does it: fetches repose for macOS (hostd, which runs each of your machines as a virtual machine of its own, the program that holds each one, and the tools for their disks), checks it against repose's release key, puts it in `/Library/Application Support/repose`, owned by root, and starts hostd from a launchd daemon that starts it again whenever the Mac boots, before anyone logs in. sudo asks for your password once, for that copy and the daemons; your machines run as you, never as root. Then it waits until the Mac has joined your account, and says how long that took. macOS lists the daemons under System Settings → General → Login Items, as allowed in the background; turning them off there keeps your machines off.

A download that fails is tried twice more, continuing from the bytes already on disk, and one that goes quiet for a minute is given up rather than held open. If the install stops, a download or sudo, `repose host add` says what failed. The host is in your account by then, so the command that finishes the install is the one the failure prints, `repose host add --join-file <path>`. The install keeps the argument in that file so you do not have to paste it again, and so it stays off the command line: anything on the Mac can read a command line while it runs. To start over instead, `repose host rm` takes the host off your account and whatever the install left on the Mac with it, and `repose host rm --local` clears the Mac without touching your account.

From any other machine, `repose host add` prints one step and nothing else, so you can pipe it: `repose host add --join <argument>`, to run on the Mac. It carries a token that works once, within 24 hours, and the machine's place on repose's network. Treat it as a password until the machine has joined: anyone who runs it adds a machine to your account, and anything running on the Mac can read it from the command line while the install does. To keep it off there, put it in a file and use `repose host add --join-file <path>`, or pipe it to `repose host add` and give `--join` a lone dash, which means standard input. If you lose it, or let it go stale, `repose host rm` and `repose host add` again; the old step then opens nothing. `repose host add --json` never prints the argument, so a script or an agent that reads the output never holds the key: on the Mac it installs, and elsewhere it puts the argument in `~/.config/repose/join-NAME`, readable by you alone, and names that file instead.

On an x86 box, run `repose host add` on the box itself. It puts the argument in `~/.config/repose/join-NAME`, readable by you alone, and prints the installer, to run there as root:

```shell
curl -fsSL https://repose.run/install-host.sh \
  | sudo bash -s -- --join-file '/home/you/.config/repose/join-box'
```

It sets repose up and joins your account. The argument stays in the file so that it is on no screen, no command line and not in the copy of the command sudo logs, and an agent you ask to run the line never holds the key; the installer deletes the file once it has read it. To add another x86 box from this one, copy the file there and give its path. It keeps your machines' disks in `/var/lib/repose/guests.img`, 100 GB or the free space less 20 GB, whichever is smaller; with `--device /dev/sdb` it takes over that whole disk instead, and it refuses a disk that holds anything. The dashboard's **Add a host** shows the line with the argument in it, `printf '%s\n' <argument> > repose-join && curl -fsSL https://repose.run/install-host.sh | sudo bash -s -- --join-file repose-join`, for you to paste into the box's terminal; don't paste it into an agent's conversation.

Each machine's disk is a file of its own in `/Library/Application Support/repose/volumes`, and takes space only as the machine fills it. hostd's log is `/Library/Logs/repose/hostd.log`, and the updater's is beside it; they hold nothing of yours beyond the ids repose logs. Repose updates itself every 30 minutes: hostd restarts and your machines keep running through it.

Once it has joined:

```shell
repose host ls
```

```
HOST      STATE   MEMORY  MACHINES  RUNNING  LAST SEEN
mac-mini  online  24 GB   0         0        just now
```

`waiting` means it has not joined yet, and a new machine is refused until it has: `Your Mac mac-mini has not joined your account yet.`, exit 8, and nothing is created. `repose run --host repose` makes the machine on repose's servers meanwhile on a hosted plan, or [remove the host](#taking-it-off) and add it again if the install stalled ([troubleshooting](/docs/troubleshooting#your-own-hardware)). From `online`, `repose run` in any project puts it on your hardware.

`repose ls` then says where each machine runs:

```
$ repose ls
PROJECT     MEMORY       STATE    UP     AGENTS           HOST
todo-app *  1.4 GB used  running  2h14m  claude: working  mac-mini
api         small        running  3d     -                repose
```

`repose status` says it in a `host` row, and a project's `--json` carries `host_name` beside `host_id`.

To name it yourself, `repose host add mac-mini`. A name is 1 to 32 characters of `a-z`, `0-9` and `-`. It is yours alone: another account may use the same one.

On the dashboard, **New project** has **Runs on** under **Options** once you have a host: your host, or repose's servers, the same choice as `--host`. The line under the name says where the machine will run before you open it. With more than one host it lists each with its state, after **Any of your hosts**, the default, which takes the one with the most room. For one host, the line also says what it has free and how many machines run there (`3.2 GB free · 2 running`); when it is full the form says so, with a **Stop** for each machine running there and **Use repose's servers**, and **Create** waits until there is room or **Runs on** is repose's servers. While your host is waiting or offline the form says so in the same words and creates nothing, and repose's servers is the way out. Settings → Hosts adds, lists and removes hosts, and restarts a Mac ([through repose](#restart-your-mac-through-repose-and-your-machines-keep-their-memory)). **Add a host** takes a name, or none, and shows the step to run on the machine once, with a copy button; it is not shown again, and a host that is still waiting can be removed and added again for a fresh one. The list shows each host's state, its memory and cores, how many of your machines are on it and how many are running (on a Mac, with what they hold together), and when it last answered.

## Starting a machine takes a few seconds

On an M4 Mac mini, `repose start` of a stopped machine returned in 3.8 to 5.4 seconds, and a new machine from a repository was ready in 12 to 34 seconds, 16 at the median, once the base image was on the Mac. The first machine also downloads that image, about 3 GB. Measured on 2026-11-26 and 2026-11-27.

Once it is running it is a machine like any other: Claude Code showed its first screen in 0.4 seconds and opencode in 1.4 to 1.5, and `repose run -d --agent opencode -p` returned in about a second. Measured on 2026-11-26.

## When your hardware is offline

A Mac that is asleep, rebooting, off, or off the network stops answering repose. After 90 seconds `repose host ls` says `offline` and names how long it has been quiet:

```
HOST      STATE    MEMORY  MACHINES  RUNNING  LAST SEEN
mac-mini  offline  24 GB   2         1        14m ago
```

`repose ls` says the same under each machine on it, and so does the dashboard:

```
PROJECT     MEMORY  STATE    UP     AGENTS           HOST
todo-app *  -       running  2h14m  claude: working  mac-mini
todo-app: your host mac-mini has not answered for 14m.
```

`repose run` and `repose attach` wait up to 10 minutes for the host and then give up; `repose start` is refused while it is quiet, because a stopped machine's disk is on that host and nowhere else. A new machine is refused with the same sentence, and `repose run --host repose` makes it on repose's servers instead.

A Mac that is only off the network keeps running your machines, and they are reachable again, with whatever the agents did meanwhile, as soon as it answers. A Mac you restart, shut down or lose power on stops them, and repose starts the ones that were running again once it is back; what they held in memory is gone, what they wrote to disk is not, unless you [restart it through repose](#restart-your-mac-through-repose-and-your-machines-keep-their-memory). If the hardware is gone for good, [retire the offline host](#taking-it-off) with `repose host rm <name>` to free your host slot and clear its stranded machines with `repose rm`.

## Restart your Mac through repose and your machines keep their memory

`repose host restart` on the Mac, or **Restart…** for that host on the dashboard's Settings → Hosts from any computer, saves every running machine's memory to a file on the Mac (it parks them), then restarts the Mac. When you log in again, each machine comes back where it was: its programs, its tmux windows, an agent mid-task. On an M4 Mac mini with automatic login, three machines were parked in 8 to 11 seconds and were running again, memory and all, 54 to 70 seconds after the command, three times in a row. Measured on 2026-11-27.

A machine that cannot be parked stops the restart: the answer names it and why, nothing restarts, and the machines already parked run again. `repose host restart --force`, or **Restart anyway** on the dashboard, restarts all the same, and those machines start fresh. macOS saves a machine's memory only while the Mac's screen is unlocked, so on a locked Mac the restart is refused and nothing changes: `This Mac's screen is locked, and a locked Mac cannot save a machine's memory. Nothing restarted; unlock it and restart again.` Save your work in the Mac's own apps first: repose may restart the Mac without waiting for them. If the Mac has not restarted three minutes later (an app cancelled it), repose runs the parked machines again.

While the Mac restarts, `repose ls`, `repose status` and the dashboard show its machines `parked; mac-mini is restarting, and its memory is kept`. Until you log in, a parked machine waits, with its memory kept. `repose ls` shows it `running parked`, `repose status` and the dashboard say `parked; it waits for you to log in to mac-mini, and its memory is kept`, and `repose start` starts nothing. With automatic login on (System Settings → Users & Groups), the Mac logs you in itself and the machines come back without you, unless its screen was locked when it went down: macOS then logs in with the screen locked, and the machines wait for you to unlock it (`parked; it waits for you to unlock mac-mini`). A memory file nobody logs in to read is deleted six hours after the park, and its machine stops, to start fresh at the next `repose start`: a machine's memory is never kept on the disk longer than that.

**Any other restart loses the machines' memory**: Restart or Shut Down from the Apple menu, a macOS update that restarts on its own, a power cut or a crash. macOS ends every virtual machine the moment a restart begins, before anything can save it, measured on both restart paths. When the Mac is back, repose starts the machines that were running again, cold: their disks are as they were, and what they held in memory is gone. Stop or park anything you would miss before you restart the Mac yourself.

**FileVault.** With FileVault on, macOS waits at its unlock screen after a restart or a power cut, before it starts anything, so your machines wait too. A restart you begin with `sudo fdesetup authrestart` unlocks itself once.

Machines on repose's servers keep their memory across a server restart too ([when a machine's server restarts](/docs/lifecycle#when-a-machines-server-restarts)).

## Snapshots stay on its disk

Snapshots of machines on repose's servers go to cloud storage. **Snapshots of machines on your hardware are written to that machine's own disk, and repose keeps no copy.** If the disk dies, every snapshot of every machine on it dies with it, and so do the machines. Back the disk up yourself, or keep in your git remote anything you would miss. They share the space the machines' disks use: a volume of half of it, which costs what the snapshots hold. They are deleted on the host by the same rule as anywhere: after seven days, keeping a project's newest, at most seven a project, and a destroyed machine's last one for 30 days.

This is on purpose. The alternative is the keys to a storage bucket sitting on a machine in your house, which is a kind of credential repose does not keep anywhere ([secrets](/docs/secrets)).

So a destroyed machine's final snapshot is kept on the host too, for 30 days while the host is there: `repose rm` says `Its final snapshot is kept on your host mac-mini; removing the host deletes it.` [Taking the host off](#taking-it-off) deletes every snapshot on it. After that `repose ls --destroyed` and the dashboard show `gone with removed host mac-mini` in place of a date, and a restore is refused with `That snapshot was kept on your host mac-mini, which was removed, so it is gone; repose had no copy` and creates nothing.

## What runs on it, and what it reaches

Only your own projects run on your hardware; nobody else's is ever placed there. On repose's network it reaches the control plane and nothing else: not another host, not the gateway's own services. Repose reaches it over the tunnel the install sets up, the same way it reaches its own servers, with no port open on your side.

## Taking it off

```shell
repose host rm mac-mini
```

Repose asks once, naming how many snapshots the host keeps (`This deletes the 2 snapshots kept on it; repose has no copy.`), then stops placing projects there and retires the host. Run on the Mac that is the host, the command also removes repose from it: hostd and its updater, every machine with its disk and snapshots, and repose's files and logs, so the memory and the disk are the Mac's again; sudo asks for your password for it. `repose host add` installs it again afterwards. Run anywhere else, the Mac is untouched and keeps running until you turn it off, and `repose host rm --local` on the Mac removes it.

A host that is answering and still holds machines is not removed. A machine does not move to other hardware, so `repose rm` each of them first (keep in your git remote anything you would miss), and `repose host rm` then tells you which are in the way.

A host that has **stopped** answering is removed, machines and all, because the alternative is a dead end: the machines on it cannot be destroyed while repose still believes the host may come back, so refusing to remove the host would leave you unable to do either, or to add a replacement. The question says how many machines go with it and that their disks were on that host; `--yes` skips it, as everywhere. Afterwards the machines are still listed and `repose rm` finishes each of them without the host. If the Mac was only asleep, turn it on and wait for `online` instead: once a host is removed it cannot rejoin, and whatever was on its disk is gone.

Removing it from the Mac first is the other order, and it takes one more step. `repose host rm --local` works with no session and with repose unreachable, because it is your Mac and removing repose from it needs nobody's permission. The question it asks says how many machines go with it when your account can be read, and says that it could not be read when it cannot. The host stays on your account either way, holding machines that no longer have anywhere to run: wait until `repose host ls` shows it `offline` (about 90 seconds), run `repose host rm <name>` to retire it, and then `repose rm` each stranded machine.

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
