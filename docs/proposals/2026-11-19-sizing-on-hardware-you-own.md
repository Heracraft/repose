# Sizing on hardware you own (proposal, 2026-11-19)

**Status: research and design, saved. Nothing here is decided, no id is
reserved, and nothing is to be built from this file until a council pass
has judged the work items it names.** The owner called this "the real
item" on 2026-11-19: the size ladder (`tiny`, `small`, `large`, `xl`, 4 GB
per vCPU) is an artefact of our hosted economics, where a seat is a sales
unit. On a Mac the customer owns, the customer owns the cores and the
memory, so a 24 GB Mac mini should give one machine most of the appliance
instead of making its owner pick a rung from a ladder built for our fleet.
The owner's rider is the harder half and the reason this file starts with
research: "We don't want the user to have terrible UX simply because now
they have to figure out a combination of disk and cores. What if they're
new, what if they don't know? ... Nice defaults. How do you make sure they
already know, by default, that they can choose whatever they want?" And the
standing instruction, given three times that day: do not invent a pattern;
find what already works in products people choose to use, and emulate it.

One thing is settled and was not researched: **burst is on by default on
owned hosts, with no gate** (the owner: "just straight up do it"), because a
guest alone on the Mac should use every core the appliance has. Everything
below is consistent with that, and it means what the owner guessed it
would: the default is about memory, and the cores follow.

Repo facts carry a `file:line` from branch `owned-sizing` at `069e32d3`
(from the program branch `mac-mini`). The research is the four reports
this session commissioned on 2026-11-19, twenty-seven products, each
claim with its URL; §8 lists the sources that matter and the rest is in
the reports' text, quoted here where a quote carries the point.

## Summary

| Question | Answer |
|---|---|
| What is wrong today | A new project on a Mac gets `large`: 2 base cores, 8 GB, a 40 GB disk ceiling, whatever the Mac has. A 24 GB mini places 15.75 GiB and strands 7 of them. Its `xl` does not fit at all (§1), so the two sentences the landing wants to say are false |
| What the research found | Twenty-seven products, zero that ask a newcomer to size anything on day one. Every default that survived its users is a fraction of the host, floored and capped (Docker 50%, WSL 50%, Rancher 25% to 6 GB, Lima half to 4 GB, Parallels N/2, Apple's own sample `cores - 1`). Every small constant (Docker 2 GB, Fly 256 MB, Codespaces 2 cores 4 GB, Podman 2 GB, multipass 1 GB) was raised by its vendor after complaints. Nobody lowered one |
| The pattern to emulate | A host-derived default behind a word (Parallels "Automatic", UTM "Default", Docker's slider position), **with the number printed where the user acts** (Fly's `shared-cpu-1x, 1GB RAM (most apps need about 1GB of RAM)`), a per-command override, a way to see which is active, and a resize that exists from day one (§3) |
| Recommendation | On an owned host a size is a memory figure, not a class. The default is the whole placeable share of the appliance; cores are always all of the appliance's (burst, settled); disk is a separate ceiling that defaults to the pool. One function gives the figure from the Mac's memory (§4, §5). A second machine while the first holds the box is refused with both ways out named (§4.3) |
| What it means on the two Macs | 16 GB: one machine, all but two of the chip's cores, 9 GB. 24 GB: 15 GB. 32 GB: 20 GB. Two machines at once on a 24 GB mini means one of them says `--memory` (§5) |
| The counter-evidence | Fixed grants are the pattern with the most "second VM cannot start" complaints; the products users praise most (OrbStack, WSL2, Railway) hand out ceilings, not grants. Why this file still recommends grants on a one-VM-per-project product, and what would change the answer, is §6 |
| Not in this file | Any code, any decision id, the fleet's classes (unchanged), the plan (unchanged, I-1364), the appliance's own share of the Mac (unchanged, I-1376) |

---

## 1. The repo today, and the arithmetic the landing wants to quote

**The ladder.** `tiny` 0.5 vCPU 2 GiB 10 GB, `small` 1/4/20, `large`
2/8/40, `xl` 4/16/80 (`internal/hostd/guest/manager.go:131-136`,
`internal/api/scheduler/scheduler.go:37`), every class 4 GB of memory per
vCPU (I-784), with burst ceilings 2, 4, 6, 8 (I-1126). A running guest
holds its class RAM plus 512 MiB of overhead on the host
(`manager.go:923`, `unit.go:29`), and the api's placement term asks for
the class RAM against `least(free_mem_bytes, mem_bytes - reserve -
reserved)` (`scheduler.go:288-300`). The class reaches hostd **by name
only**: `CreateGuest`, `StartGuest` and `Restore` carry `class`
(`docs/interfaces/grpc-hostd.md:185-193`) and hostd's table turns it into
`--cpus boot=N` and `--memory size=NM,shared=on` (`ch.go:123,143`).
Nothing on the wire carries a number. Cloud Hypervisor gets one memory
size and one vCPU count; there is no hotplug and the balloon is
free-page reporting only (I-652). Memory is never oversubscribed (R3-2),
in four places (I-724).

**The default.** `default_size` in `config.toml`, `large` when unset
(`internal/cli/config.go:156`, `cli.md:579`); the dashboard's form offers
the four names (`NewProjectForm.svelte:40`). `repose run` on a Mac
therefore makes a `large`, and says `Created todo-app (large)`.

**The Mac.** The launcher gives the appliance three quarters of the Mac's
memory, never under 8 GiB, and all but two cores, never under four
(`internal/cli/appliance.go:104-116`); the disk is a 100 GB sparse file
unless the volume has less (`:59-67`, `--disk`). Inside, the reserve is the
fleet's eighth with a 2 GiB floor (I-724). A 12 GiB appliance reports
11.6 GiB in its Hello (`docs/features/own-hardware.md`), so the measured
figures sit about 0.4 GiB under the ideal ones.

**What fits, by the repo's own rules.** The hold per running machine is
class RAM plus 0.5 GiB, and nothing bursts memory.

| Mac | appliance | reserve | placeable | fits today | biggest single machine today | stranded |
|---|---|---|---|---|---|---|
| 16 GB | 12 GiB | 2 | 10 | one `large` (8.5); or two `small` (9) | `large`: 2 cores (up to 6), 8 GB | 1.5 GiB |
| 24 GB | 18 GiB | 2.25 | 15.75 | `large` + `small` + `tiny` (15.5) | `large`: 2 cores (up to 6), 8 GB | 7.25 GiB |
| 32 GB | 24 GiB | 3 | 21 | one `xl` (16.5), or `xl` + `small` (21), or two `large` (17) | `xl`: 4 cores (up to 8), 16 GB | 4.5 GiB |
| 48 GB | 36 GiB | 4.5 | 31.5 | `xl` + `large` + `small` (29.5) | `xl` | 15 GiB |
| 64 GB | 48 GiB | 6 | 42 | two `xl` (33), or `xl` + two `large` (33.5) | `xl` | 9 GiB |

So **a 24 GB Mac cannot run an `xl`**: it needs 16 GiB plus the 512 MiB
hold against 15.75 GiB placeable, short by 768 MiB; two `large` are
short by 1,280 MiB. The one combination that does work, a `large`, a
`small` and a `tiny`, leaves 256 MiB spare, less than one more guest's
overhead. `nix/hosts/mac.nix:30-32` says "a 24 GB Mac [places] 15.75 GiB
(one xl, or a large and a small and a tiny)", and the landing's
placeholder says "24 GB: one xl or two large"; both are wrong by the
arithmetic the same files define, and the customer who buys the bigger
Mac and tries the obvious thing (one top-class machine, or two of the
next) is refused twice. The ladder and the appliance's share were
designed apart and nobody multiplied them out; the landing session
counted the overhead first and checked the figures above (its proposal
§5, commit `066484a2` on `landing-overhaul`), and holds them off the
page. The figures' single source is ENG-84; the `mac.nix` comment is
corrected by whoever next edits that file, which tonight is the park
session, not this one. The default this file proposes for a 24 GB Mac,
about 15 GB to one machine, is larger than any class we sell, so it
could not be a class even if the ladder were kept.

**Cores.** The appliance on a 10-core M4 has 8 vCPUs. A `large` boots with
6 and is held to 2 until the controller raises it, and `mac.nix` sets no
`burst` line, so the appliance ships with burst **off**: a `large` on a
Mac runs on 2 of the Mac's 10 cores at all times. Gate 0 measured no
nesting tax (ratio 1.07, research §7); the 2-core cap is why the boot
path is "two to three times slower than on x86" (`features/own-hardware.md`
§4, load average 7 on 2 vCPUs). The owner's "3 to 5 times slower builds"
is this cap, not the hypervisor.

**Disk.** Volumes are thin and the pool is overcommitted by design
(I-586: placement wants 30 percent of the pool free after the new
volume's first bytes, `scheduler.go:108-116`). A machine costs about
0.3 GB at creation and then what it writes (`features/own-hardware.md`).
The appliance's 100 GB file holds an ESP, a 32 GiB root and the rest as
`vg-guests` (I-1376), so its pool is about 57 GiB. "A 256 GB disk fits
about two projects" is not a rule the code has; the true sentence is
that machines share the appliance's disk and cost what they write.

**What a user can change.** `--size` on `run`, `sync` and `resize`
(classes only), `resize DISK` (grow only, I-613), `repose host add
--memory --cpus --disk` for the appliance itself.

## 2. The research: twenty-seven products, five questions each

The four reports (homelab and desktop hypervisors; laptop container
runtimes; Lima, Colima, multipass and Apple's own tools; cloud and PaaS
pickers) asked the same five questions of every product: the default
with no input, where the choice is shown, whether it is required, what a
newcomer sees on day one, and what people complain about, with issues
and forum threads preferred over vendor docs for any claim about a
mistake people make.

| Product | Default with no input | Where the choice is | Required? | Day-one message about size | Top complaint |
|---|---|---|---|---|---|
| **Docker Desktop** | memory 50% of host (2 GB constant until 4.6.0, 2022-03; then half, min 2 max 8; cap since dropped); swap 1 GB; disk ceiling ~64 GB (unverified) | Settings > Resources sliders; `.wslconfig` on Windows | No | None: "No further configuration is needed" | idle VM holds GBs on Apple VZ and never gives them back (for-mac #5812, #6120); `Docker.raw` only grows |
| **OrbStack** | memory ceiling "no more than 8 GB", released when unused (1.7.0, 2024-08); CPU "not a reservation"; disk unlimited sparse; per-machine limits since 2.2.0 (2026-06) | Preferences > System, two controls; `orb config set` | No | None; "starts low and grows as needed" | helper exceeds its own limit and swaps (#378, #2251, #2742 fixed 2.2.4); disk not reclaimed (#2030) |
| **WSL2** | memory 50% of host (80% before build 20175, 2020), all logical processors, swap 25%, 1 TB VHD, `autoMemoryReclaim=dropCache` (default since 2024-05) | `.wslconfig` (absent by default) or the WSL Settings app | No | None; `wsl --install` asks for a username | vmmem eats RAM and will not return it (#4166, open since 2019-06, "we have a fix in the works"; #8725); `.vhdx` never shrinks (#4699) |
| **Podman / Podman Desktop** | CLI: CPUs host/2, 2048 MiB, 100 GiB; GUI: host/2, 4 GB, 100 GB; fixed per VM | flags, `containers.conf`, GUI form with sliders | No | CLI: "Machine init complete"; GUI shows the numbers once | 2 GB "too small for Kind" (#2145, #3047); 4 GB "often too low" (#17961, 2026) |
| **Rancher Desktop** | memory min(6 GB, 25% of host), 2 CPUs, disk grows to 100 GB | Preferences > Hardware sliders with a red "may affect system services" zone | No | Welcome asks Kubernetes, engine, PATH; nothing about size | formula had no floor so small hosts could not boot (#7499); balloon hides but never releases (lima #2720) |
| **Lima** | min(4, host cores); min(4 GiB, half of host); 100 GiB sparse | `--cpus/--memory/--disk`; "Open an editor" menu item; `limactl edit` | No | a 4-line menu; numbers only in `limactl list` | 4 GiB OOMs under two coding agents (eggie #73, 2026-10); refuses more vCPUs than cores (#1621) |
| **Colima** | 2 CPU, 2 GiB, 100 GiB, constant | `--cpu/--memory/--disk`; `colima start --edit` | No | None | build OOM hangs the VM (#1158); a refused shrink is still saved to the config (#1661) |
| **multipass** | 1 CPU, 1 GiB, 5 GiB, constant | `launch --cpus/--memory/--disk`; `multipass set` (stopped only) | No | `Launched: <name>` | CPU and memory could not be changed at all from 2019-10 to 2022-04 (#1158); "Not enough memory in the system" at start, not launch (#3345) |
| **Tart** | 2 CPU, 4 GB, constant; Linux disk 20 GB | `tart set` after clone | No | None | two macOS guests max; no CPU pinning (#609) |
| **vfkit** | 1 vCPU, 512 MiB, no disk | flags only | n/a | None | wrapper defaults "will not be enough" |
| **Apple Virtualization sample** | CPUs = host cores minus one; 4 GiB flat, clamped; memory "reserves but doesn't allocate immediately" | in code | set by the sample | n/a | more vCPUs than physical cores is "devastating" (Oakley); memory ceiling misread as a reservation |
| **Apple `container`** | 4 CPU, 1 GB per container, lazy ceiling | `--cpus/--memory`; `container system property set` | No | None | "1 GB is not enough for real workloads" (workmux #77, 2026-03) |
| **Xcode simulators** | no sizing exists; shares the host fully | nowhere | no choice | None | runaway CPU; ~4 GB each, thrash at five on 16 GB |
| **Proxmox VE** | 1 core, 1 socket, 2048 MiB, 32 GiB, balloon on; constants | eight wizard tabs; balloon min behind "Advanced" | No | eight tabs of hardware vocabulary | "why are the default VM settings so bad" (2022); staff: a *profile mechanism*, not bigger defaults; second VM "cannot allocate memory" (#66114, #76089); users ask for a refusal instead of overcommit (#115875) |
| **Unraid** | 1 vCPU, 1 GiB initial = max; Linux disk size blank | one form; Max memory in Advanced view | disk size, yes | "Refer to your guest OS documentation" | Initial/Max memory is not dynamic ("KVM memory balloon is not dynamic"); `qemu` OOM-killed on an 8 GB box |
| **UTM** | 4096 MB; cores "Default" = host performance cores; 64 GB | wizard Hardware and Storage steps | No | "CPU Cores: Default" with no number | disk cannot grow after install (#6043, open since 2024-01); Apple VZ never returns memory (#8211) |
| **Parallels Desktop** | "Automatic": a profile rule over host cores (Productivity 2 if N<8 else 4; Development the even number <= N/2) and "maximum recommended memory"; Win11 disk 256 GB expanding | a plain-language question ("I will primarily use Windows for: ...") then Hardware > CPU & Memory with Automatic/Manual and a green zone | No | the purpose question | Automatic is distrusted when the Mac slows; 256 GB default disk alarms people; refuses to start a VM when the Mac is short rather than squeezing |
| **VMware Fusion** | 1 vCPU, 2 GB (Win10), constants | summary card with "Customize Settings" beside Finish | No | the numbers on the summary | "Not enough physical memory is available to power on this virtual machine" |
| **Vagrant** | whatever the box ships (typically 1 GB, 1-2 CPU); `vb.memory = "1024"` commented out | a commented block in the Vagrantfile | No | nothing; the size is in a comment | ENOMEM on builds at 1 GB; "10 GB is never enough"; VMDK cannot be resized |
| **Fly.io** | `shared-cpu-1x, 1GB` (256 MB until 2024) | the `fly launch` summary with a yes/no; `[[vm]]`; `fly scale` | No | `App Machines: shared-cpu-1x, 1GB RAM (most apps need about 1GB of RAM)` | 256 MB OOMed simple apps (staff: "kinda hard to run stuff in under 256mb"); a size set outside `fly.toml` reverts on deploy (#1310, #4544) |
| **Railway** | no size; elastic up to the plan's ceiling (Hobby 48 vCPU, 48 GB per service) | Replica Limits sliders (a max only), `memoryLimit` | No | nothing; Metrics shows use | surprise bills ("$5 plan but I pay $10": "allocation is not the same as usage"); until 2024-07 "you can't" cap a service |
| **Hetzner** | a preselected card (which one: unverified); bundles of vCPU + RAM + disk | one wizard step, Shared / Dedicated tabs | step required, pre-filled | the card grid | shared vCPU throttled at a 20-33% baseline (HN 2024-09); a disk upgrade blocks every downgrade |
| **DigitalOcean** | "most popular defaults are pre-selected" (which size: unverified); $4 to $18 bundles | one long page of cards | pre-filled | price per month and hour on each card | disk tied to size and cannot shrink ("reason seems to be fake", 2020); small plan runs out of disk before CPU |
| **Coolify** | the app gets the whole server (limit 0) | App > Advanced > Resource Limits | No | server validated; metrics off until enabled | "one unbounded service exhausted host memory and froze the whole server" (#12414, 2026-11) |
| **Dokku, CapRover, Kamal** | unlimited, the whole box | per app / per role, opt in | No | nothing | same shape as Coolify; no product has a per-host reserve |
| **GitHub Codespaces** | the smallest type the repo allows: 2 cores, 8 GB, 32 GB; `hostRequirements` raises the floor | "New with options"; `devcontainer.json`; change later | No | the green button says nothing; the list page shows cores and RAM | smallest default too slow; GitHub raised it twice (2020: 4 cores 8 GB; 2023: 2 cores to 8 GB); cannot change type across storage sizes (#7087) |
| **Gitpod / Ona** | Standard "up to 4 cores, up to 8 GB, 30 GB", hard-coded server-side (#18939) | the New Workspace dialog; repo default | No | class name and credits per hour | cannot change class after creation without losing the workspace (#11042) |
| **Render** | Starter 0.5 CPU 512 MB when a blueprint omits it | form radio; `plan:`; Settings later | form, yes | plans with RAM, CPU, price; the OOM text names the 512 MB | 512 MB too small for Node and LLM apps (2026) |

**Six things the table says, each with the complaint that proves it.**

1. **Nobody asks.** Twenty-seven products, no sizing prompt on day one.
   The choice is always one level down, in a flag, a settings pane or a
   file, and it is found after the first failure. That is settled
   practice, not a gap to fill.
2. **A default that scales with the host survives; a constant does not.**
   Docker's 2 GB became "half of physical memory (min 2 GB, max 8 GB)"
   in 4.6.0 "for better out-of-the-box performances" and the cap later
   went; WSL's 80 percent became 50; Fly's 256 MB became 1 GB after
   "it's actually kinda hard to run stuff in under 256mb"; Codespaces'
   2-core 4 GB was raised twice; Podman Desktop's 2 GB became 4 and a
   2026 ticket asks for "25 to 30% of host RAM ... never below 4 GB".
   The Proxmox thread "why are the Proxmox 7 default VM settings so
   bad" (1 core, 2 GB, 32 GB on hosts with 64 cores) is the canonical
   complaint, and the vendor's answer was that diverse use cases want
   "a profile mechanism", not bigger universal defaults. No vendor in
   the four reports ever lowered a default after "too big" complaints,
   except WSL's 80 to 50 percent, which was the host being starved, not
   the guest being large.
3. **The number must be visible where the user acts, or the default is
   distrusted.** UTM's "CPU Cores: Default" shows no number and users
   "notice that the 'default' setting for the CPU only has a clock speed
   of 1 GHz"; Parallels users with "Automatic" blame it when the Mac is
   slow ("I had it as 'Automatic,' which should be the best option").
   Fly prints `shared-cpu-1x, 1GB RAM (most apps need about 1GB of RAM)`
   in the launch summary with a yes/no, and the Fly complaints are not
   about the default being unknown; they are about a size set outside
   `fly.toml` reverting. Rancher's slider has a red zone that says where
   "may affect system services" starts, the one piece of guidance in
   the whole set that tells a newcomer what too much is.
4. **Never more vCPUs than physical cores, and every tool on Apple
   silicon learned it the hard way.** Lima clamps and warns (#1621,
   "devastating performance"); Docker's slider offered 10 cores on an
   8P+2E M1 Max and the VM boot-looped (#6090); Apple's own sample code
   takes `processorCount - 1`; Oakley's measurement puts the useful
   range at "2 to (n - 1)". The P-versus-E-core question never surfaces
   as a user complaint in any product that exposes a count or a
   percentage.
5. **A size that cannot be changed later is the complaint that lasts
   longest.** multipass could not change CPU or memory from 2019-10 to
   2022-04 (#1158, 30 months, with users hand-editing the daemon's
   database and bricking instances); Gitpod cannot change class without
   losing the workspace (#11042); Codespaces cannot switch types across
   storage sizes (#7087); Colima refused a shrink and saved the asked
   size anyway (#1661), so the resize must say what it did. Every tool
   that got this right needs the VM stopped for the change, which is
   what `repose resize --size` already requires.
6. **Disk is grow-only everywhere, and tying it to the size is the
   top "cannot undo" complaint.** DigitalOcean ("You can make a Droplet's
   CPU and RAM smaller, but not its disk", which users call "fake"),
   Hetzner (a disk upgrade blocks every later downgrade), Codespaces,
   Gitpod. The tools that avoid the "too small" disk complaint chose one
   large sparse ceiling once (Lima and Colima 100 GiB, WSL 1 TB,
   OrbStack 8 TB) and took the "grows forever" complaint instead
   (WSL #4699 since 2019, OrbStack #2030). Repose's thin volumes with a
   per-machine ceiling and a trimmed pool already sit on the right side
   of this; what is wrong today is only that the ceiling comes from the
   class.

**And the one split that decides the shape.** Products that are a single
VM shared by everything (Docker Desktop, OrbStack, WSL2, Rancher) can
hand out a *ceiling* and let the kernel share; their praise ("starts low
and grows", "returns idle memory to the host") and their complaints
(memory not released: WSL #4166 open since 2019, OrbStack #2742, Docker
#6120) are both about that one VM's accounting. Products that run **one
VM per thing** (Proxmox, Unraid, UTM, Parallels, Fusion, multipass, Lima,
Podman, Tart, Apple's `container`) all hand out *grants*, and their
complaint is the second VM: "ioctl(KVM_CREATE_VM) failed: 12 Cannot
allocate memory" (Proxmox), "Out of memory: Killed process 1356
(qemu-system-x86)" (Unraid), "Not enough memory in the system to start
the virtual machine" (multipass), "Suspend or stop one or more of the
virtual machines currently running on your Mac and try again"
(Parallels), "Not enough physical memory is available to power on this
virtual machine" (Fusion). Proxmox is the one multi-VM product that
overcommits memory with no guard, and its users ask for the guard: "I
would like Proxmox to fail to start a new VM if there is insufficient
RAM" (#115875). Coolify, which lets every app take the whole server, has
"one unbounded service exhausted host memory and froze the whole server"
(#12414, 2026-11-01). Repose is one VM per project. The grant-and-refuse
shape is what every product of that shape does, and the research says
how to do it without the complaints: scale the grant with the host, say
the number, refuse early and name the way out, and let the size change.

## 3. The pattern, in parts

Every product in the table that a newcomer gets through without a
decision has the same four parts, and the two that are missing a part
are the ones with the complaint:

| Part | Who does it well | Who is missing it, and what it cost |
|---|---|---|
| **A default derived from the host**, floored and capped, behind a word | Parallels "Automatic" (N/2 by profile), UTM "Default" (performance cores), Docker and WSL (50%), Apple's sample (`cores - 1`), Lima (half, to 4 GiB) | Proxmox, Colima, multipass, Fly before 2024, Render: constants, raised after OOM complaints |
| **The number printed where the user acts**, with the reason in a few words | Fly's launch summary line; Podman Desktop's create form; Fusion's summary card; Rancher's red zone | UTM ("Default", no number), Parallels ("Automatic", no number), Docker, WSL, OrbStack, Lima, Colima, multipass (nothing at all on day one) |
| **A per-command override and a persistent default** | Lima `--memory` and `_config/default.yaml`; multipass `launch --memory`; Fly `--vm-memory` and `[[vm]]`; Apple `container --memory` and `container system property set container.memory` | Railway: a max only, and until 2024-07 not even that |
| **A readout and a resize** that exist from day one and say what they did | `limactl list` and `limactl edit`; `multipass info` and `set` (stopped only); `fly status` and `fly scale`; `orb config show` | multipass 2019 to 2022; Gitpod; Codespaces across storage sizes; Colima's dishonest shrink |

This is the same four-part shape `docs/proposals/2026-11-19-where-a-project-runs.md`
found for the host choice (a named default, a per-command override, a
readout), which is why the two proposals should ship as one shape: the
`--host` flag and the HOST column there, the memory figure and its
column here.

The one part the owner asked about that no product provides is the last
sentence of the rider: "how do you make sure they already know, by
default, that they can choose whatever they want?" Nobody tells a
newcomer on day one that a size exists. The closest is Fly, whose
summary line plus "Do you want to tweak these settings before
proceeding?" is the only day-one sentence in the set that both states
the number and implies it can be changed, and the Fly complaints are the
mildest in the set. That is the line to emulate, in repose's words and
without the question.

## 4. Recommendation

**On a host the user owns, a size is a memory figure. Its default is the
whole placeable share of the appliance, computed by one rule from the
Mac's memory; the cores are always all of the appliance's; the disk is a
separate ceiling. The figure is printed when the machine is made, changed
with `--memory` on the commands that take `--size` today, shown in the
same column, and a second machine the box cannot hold is refused with
both ways out named.** The four class names stay the fleet's and are not
offered on an owned host. Nothing about the fleet changes.

### 4.1 What a size means on an owned host

- **Memory** is the size. A project on an owned host carries a memory
  figure in whole GiB, and that is what `--memory`, the dashboard's
  field, the `ls` column and the samples endpoint speak. The four class
  names are not offered there: `large` on a Mac is a rung of a ladder the
  user did not buy. (`--size large` from an old script or a `default_size`
  in `config.toml` is read as that class's memory, 8 GB, for one release,
  so nothing breaks; the docs do not mention it.)
- **Cores** are not a size. Every machine on an owned host boots with
  every vCPU the appliance has (8 on a 10-core M4, all but two of the
  chip) and `nproc` says so; the burst controller runs with no gate, so a
  machine alone on the Mac uses all of them and two machines share them
  by weight (settled by the owner; I-1126's controller with `gate = 1`
  and the ceiling equal to the host's cores). Never more vCPUs than the
  appliance has, which is never more than the chip has less two
  (research 4). The cgroup weight follows the memory figure, as it
  follows the class today (I-737), so the bigger machine wins a
  contended core.
- **Disk** is a separate ceiling, grow-only as today, and defaults to the
  appliance's pool size rather than to a class table. Volumes stay thin;
  a machine still costs what it writes. (Research 6: no product that
  survived ties disk to size, and the complaint about one large ceiling
  is "it grows", which the pool's trim and the 85/90/95 percent refusals
  already answer.)

### 4.2 The default, per Mac

One function, from the Mac's memory, in the same place the share rule and
the reserve rule already live, and exposed to every client (the figures'
single source, ENG-84):

```
appliance  = max(8 GiB, 3/4 of the Mac)                 (I-1376, unchanged)
reserve    = clamp(appliance / 8, 2 GiB, 16 GiB)         (I-724, unchanged)
placeable  = appliance - reserve
default    = floor_GiB(placeable - 512 MiB)              (the hold a machine takes beyond its RAM)
```

| Mac | appliance | placeable | **default machine** | cores (M4 10-core) | second machine while it runs |
|---|---|---|---|---|---|
| 16 GB | 12 GiB | 10 | **9 GB** | 8 | none |
| 24 GB | 18 GiB | 15.75 | **15 GB** | 8 (M4 Pro: 10 or 12) | none |
| 32 GB | 24 GiB | 21 | **20 GB** | 8 | none |
| 48 GB | 36 GiB | 31.5 | **31 GB** | 10 or 12 | none |
| 64 GB | 48 GiB | 42 | **41 GB** | 10 or 12 | none |

"None" is the honest column, and it is the owner's framing: one machine
gets most of the appliance. The measured Hello sits about 0.4 GiB under
the ideal, so the function should read the host's reported memory, not
the Mac's nominal one, and the table above is what the docs quote with
"about".

A machine whose figure is not the default (a `--memory 7G`) is a grant of
that much, held while it runs, as a class is today. The reserve, the
3/4 share and the 512 MiB hold are unchanged; this file moves nothing
that I-724 and I-1376 settled.

### 4.3 What the user reads

**Creation.** The one line a newcomer sees, in the shape of Fly's summary
line, after the `Created` line that exists today:

```
$ repose run
✓ Created todo-app on mac-mini: 8 cores, 15 GB memory, 57 GB disk  4s
```

That is the whole of "how do they know they can choose": the number is
on the line they read, and the flag is in `--help` and the docs where
they look when they want to change it. The line says what is true, once,
and stops (I-485). Fly adds a reason in parentheses; this file does not
propose one, and the council should judge whether `(all of mac-mini)`
after the figures earns its place under I-484: it is the one phrase that
would tell the user *why* a second `repose run` will be refused before
it is. The dashboard's row and the project page say the same three
numbers.

**The second machine.** The refusal repose already has for a full host
(I-724: a machine holds its memory while it runs, so stopping one frees
room) gains the other way out, in the shape Heroku uses when it refuses
to guess:

```
$ repose run
api needs 15 GB and mac-mini has 0 GB free while todo-app runs.
  repose stop todo-app
  repose run --memory 7G
```

The dashboard's form shows the same two choices where it shows the
refusal today. Nothing is created in the refused case (that is the
separate fix ENG-66 and ENG-28 own: the row must not exist before
placement answers).

**The override and the readout.** `--memory 8G` on `run`, `sync` and
`resize`, beside `--size` (which stays the fleet's); `resize --memory`
needs the machine stopped and asks, as `resize --size` does, and ends
with what it did (`Changed todo-app to 8 GB memory. Running again in
58s.`). `repose ls` and `status` show the figure in the SIZE column where
the class is today (`15 GB` beside `large`), and `--json` carries
`memory_gib` beside `class`. The dashboard's form pre-fills the default
and shows the appliance's placeable figure beside the field, which is
UTM's "host memory beside the field" and Rancher's red zone in one: the
field is a number input with the host's figure as its max, and a value
past what is free while other machines run is refused where the form
refuses today, not silently clamped (Colima's lesson, #1661).

**`repose host ls`** already shows the appliance's memory in whole GB.
It gains nothing from this file; the per-machine readout is on the
machine.

### 4.4 What changes under the surface

- **The wire.** `CreateGuest`, `StartGuest` and `Restore` gain
  `memory_mib` and `vcpus` beside `class`; hostd uses the numbers when
  they are present and the class table when they are not, and an api
  that sends only `class` is accepted for one release
  (`docs/interfaces/grpc-hostd.md`, same commit). `GuestSample.class`
  stays for the fleet; the samples endpoint's `vcpus` becomes the
  appliance's cores on an owned host, so the Usage chart stops clipping.
- **The row.** `projects.memory_mib` nullable: null is a class
  (`projects.class`, unchanged); set means an owned-host figure and the
  class column is ignored. `POST /projects` takes `memory_gib` (owned
  hosts only; `400 invalid` with a class, or on the fleet); `PATCH`
  takes it under the stopped rule `class` has.
- **Placement.** `PickHost`'s memory term reads the figure where the
  class is read; the CPU term, the seat count, the 1.5 rule and the pool
  gauges already leave owned hosts out (I-1366), so the thousandths
  stay the fleet's and nothing is oversubscribed anywhere. The unit's
  `MemoryMax` and `MemoryHigh` follow the figure; `guests.slice`'s
  ceiling is unchanged.
- **The host.** `mac.nix` turns burst on with `gate = 1` and the
  appliance's cores as every guest's ceiling (the settled part); the
  controller's "never below the base" keeps the weight rule meaningful
  when two machines share the box.
- **Restart.** A machine keeps its figure on the row, so I-645's restart
  after a Mac reboot starts it at the same size, as it starts a class
  today. If the appliance's memory shrank between boots (the user ran
  `repose host add --memory` smaller, or moved the disk to a smaller
  Mac), the start is refused with the capacity sentence and
  `repose resize --memory`, and the api does not silently shrink a
  machine (the Fly complaint, #1310: a size the user set reverting on
  its own). A machine made before this change keeps its class until
  its owner resizes it, the rule I-1326 used for burst ("a guest running
  since before it keeps its count until its next stop"), and its `ls`
  row shows the class name until then.

### 4.5 What this does not change

The fleet's four classes, their prices, the seat arithmetic (I-784,
I-1168) and the burst gate on host-02 (I-1326). The Own hardware plan
(I-1364): it sold no memory of ours before and sells none now. The
appliance's own share of the Mac and the reserve inside it (I-1376,
I-724). Snapshots on the host's disk (I-1372). The four-minute start,
which is ENG-6's and ENG-12's, not this file's; the cores part of this
file makes it shorter, since the boot path ran on 2 vCPUs at load
average 7.

## 5. The two Macs, in page-safe sentences (if the recommendation stands)

For the landing (ENG-84) and `/docs/own-hardware`, with "about" where the
measured Hello sits under the ideal:

- **16 GB Mac mini.** Repose keeps 12 GB of it for your machines. One
  machine gets all but two of the chip's cores and about 9 GB of memory.
- **24 GB Mac mini.** Repose keeps 18 GB. One machine gets all but two of
  the chip's cores and about 15 GB.
- **32 GB Mac mini.** Repose keeps 24 GB. One machine gets all but two of
  the chip's cores and about 20 GB.
- **Two machines at once** share the memory between them: `--memory`
  sizes each one, and a machine keeps its memory while it runs.
- **256 GB or 512 GB disk.** Repose keeps a 100 GB file that takes space
  only as your machines write to it, and your machines share it. A
  machine costs what it writes; a checkout with its dependencies is a
  few gigabytes. `repose host add --disk` sets the file's size.

The figures come from the function in §4.2 and nothing else; the page
must not carry a number the function did not produce (the drift test the
landing session designed is the right shape).

## 6. The counter-evidence against this recommendation

**Grants are the complaint-heavy pattern, and the products people praise
hand out ceilings.** OrbStack ("Memory is released when it's no longer
used", "This is not a reservation"), WSL2 (`autoMemoryReclaim`, the VM
stops 60 s after the last distro), Docker's own VMM ("Returns idle memory
to the host") and Railway (no size at all) are the four products in the
set whose sizing nobody has to think about, and every one of them is
elastic. The recommendation gives a machine a grant of 15 GB that it
holds whether it uses it or not, which is the thing Docker on Apple's
framework is hated for (#5812: 5.2 GB "with zero running or stopped
containers"). Two answers, and the second is the real one. First, on a
Mac mini used as an appliance the quarter macOS keeps is fixed by the
launcher, so a grant inside the appliance starves nothing of the user's;
the "idle VM eats my laptop" complaint is a laptop complaint. Second,
those four products are one VM shared by every workload, and repose is
one VM per project: the elastic products with several VMs (Proxmox,
Coolify, Unraid) are the ones whose users ask for a refusal (#115875) or
get an OOM-killed hypervisor (Coolify #12414, Unraid #169610), and an
OOM-killed hypervisor on repose is an agent session gone, which is the
one thing the product promises not to do. Cloud Hypervisor has no memory
hotplug and the balloon only reports free pages, so a ceiling model
would also be new engineering with no measurement behind it. **What
would change the answer:** a measurement, on the lab Mac, of two
machines each granted 15 GB on an 18 GiB appliance with free-page
reporting, a zram swap in the appliance and the page cache dropped
under pressure (WSL's `dropCache`, eggie #73's proposed swapfile), with
the latency and OOM figures recorded. If two agents at full tilt fit,
the "none" column in §4.2 becomes "shares", and ownership of that
measurement is a card, not this file.

**The default leaves no room for a second machine, and a user with two
projects meets a refusal on day two.** True, and it is the owner's
framing ("one machine, most of the appliance"). The alternative the
research supports is a fraction: Docker's and WSL's 50 percent, or two
thirds, which on a 24 GB mini gives one machine 10 GB and leaves 4.75
GiB for a browser-driving second one (the `tiny`-shaped agent of I-1168
at 2 GB fits twice). It makes day two silent at the cost of day one:
the first machine gets 10 GB where it could have 15, and a 16 GB Mac's
machine gets 6. The research's strongest finding is that small defaults
get raised and large ones do not get lowered; the owner's instinct is on
the side of the evidence, and the refusal with two ways out is what
every multi-VM product does. The council should still try the day-two
path as a customer and say whether the refusal reads as a trap.

**A memory figure in a column that holds class names elsewhere is two
vocabularies.** `repose ls` would show `large` for a fleet project and
`15 GB` for a Mac one in the same column, and `--size` and `--memory`
are two flags for one idea. The alternative is one flag taking both
spellings (Lima's `--memory 4` and multipass's `--memory 1G` are one
flag each because they have no classes). The reason to keep two is the
fleet: `--size` sells a class with a price and a seat, `--memory` asks
the user's own hardware for a number, and a flag that means a price on
one host and a number on another is the kind of synonym CLAUDE.md
forbids. Fly keeps `--vm-size` and `--vm-memory` apart for the same
reason.

**The printed line is help nobody asked for.** `8 cores, 15 GB memory,
57 GB disk` on the `Created` line is a fact the user did not ask for,
and I-485 cuts those. The defence is research 3: every product that hid
the number (UTM, Parallels) is distrusted, and the one that prints it
(Fly) is not; and the line prevents a loss the user cannot undo, since a
project is pinned to its host and its figure is what the second
`repose run` is refused against. The council judges it against the
fiftieth visit.

**The research is of single-user desktop tools and PaaS, none of which
promises that an agent keeps running after the laptop closes.** So the
cost of a wrong size is higher here than in any product in the table, in
both directions: too small and the agent's build dies, too generous and a
second agent cannot start. That argues for the grant (the agent's memory
is its own) and for the early refusal (the second agent is told before
it starts, not killed after), and against the elastic shape; it does
not argue for the ladder.

**Nothing in this file was run on a Mac.** The figures are arithmetic on
rules the repo defines; the 0.4 GiB Hello gap is one measurement on one
appliance size. Before the docs quote 9, 15 and 20 GB, the function must
be run against a real Hello on the lab Mac at 16 and 24 GB appliance
sizes, and that is in the default-per-Mac work item.

## 7. The work items

One card per surface on the board, each in Open, each naming the section
it comes from, none implementing anything until the council has judged
them. Whoever builds each reserves its own decision id; this file
reserves none. The cards, in the order they can ship:

1. **ENG-90, what a size means on an owned host** (§4.1): the decision
   entry, the vocabulary, the one-release reading of class names.
2. **ENG-91, the default per Mac memory, and the figures' single source**
   (§4.2, §5): the function, the generated table, the drift test, the
   measurement against a real Hello; shares ENG-84's mechanism; carries
   the `mac.nix` comment's correction.
3. **ENG-92, cores: burst on with no gate on owned hosts** (§4.1, settled
   by the owner): `mac.nix`, the controller's ceiling, `nproc`. Not for
   the council to re-judge; listed so it is built.
4. **ENG-93, the api, the scheduler and the wire** (§4.4): the column,
   the routes, `PickHost`, `memory_mib` and `vcpus` on the three RPCs,
   the samples endpoint.
5. **ENG-94, the CLI** (§4.3): `--memory` on `run`, `sync`, `resize`; the
   `Created` line; the refusal with two ways out; the column; `--json`;
   `cli.md` and `docs_test.go`.
6. **ENG-95, the dashboard** (§4.3): the field with the host's figure
   beside it, the refusal, the row and the project page, parity with
   every CLI action.
7. **ENG-96, the docs** (§5): `/docs/own-hardware` says what a size means
   on your hardware and quotes the function's figures; `/docs/machine`
   and `/docs/limits` point there; the landing's figures component reads
   the same source.
8. **ENG-97, restart and the machines that predate this** (§4.4): I-645
   with the figure, the refusal when the appliance shrank, the
   class-until-resized rule.
9. **ENG-98, the measurement that would change the answer** (§6): two
   machines sharing the appliance's memory as ceilings, on the lab Mac,
   only if the council or a user asks for two machines at once by
   default.

## 8. Sources

The four research reports, each claim with its URL, are the evidence;
the ones that carry the argument above:

- Docker Desktop release notes 4.6.0 (half of physical memory, min 2 max
  8), 4.24.0 (Resource Saver), settings reference ("Defaults to 50% of
  your host's memory"); docker/for-mac #5812, #6120, #6090.
- OrbStack settings and efficiency pages, release notes 1.7.0 and 2.2.0;
  orbstack #378, #2251, #2742, #2030.
- WSL `.wslconfig` reference (50 percent, all processors, 1 TB VHD,
  `autoMemoryReclaim`), release notes build 20175, WSL 2.0.0 release;
  microsoft/WSL #4166, #8725, #4699.
- Podman `containers/common` `default.go` (host/2, 2048, 100);
  podman-desktop #2145, #3047, #17961.
- Rancher Desktop `settingsImpl.ts` (min(6, total/4)), hardware
  preferences page (the red zone); rancher-desktop #7499; lima #2720.
- Lima `templates/default.yaml` (min(4, cores), min(4 GiB, half)); lima
  #1621, #4220; eggie #73.
- Colima README; colima #1158, #1661. multipass CLI reference; multipass
  #1158, #3345, #4385. Tart quick start; tart #609, #1011. vfkit usage.
- Apple `VZVirtualMachineConfiguration` `cpuCount` and `memorySize`
  pages; the sample's `computeCPUCount` / `computeMemorySize`; Oakley,
  "Virtualisation on Apple silicon Macs: core allocation in VMs";
  apple/container `resource-usage.md`; workmux #77.
- Proxmox `ProcessorEdit.js`, `MemoryEdit.js`, `DiskStorageSelector.js`,
  `qm.conf(5)`; forum threads 112704 ("default VM settings so bad"),
  66114, 76089, 115875 ("disable RAM overprovisioning"), 186249.
- Unraid `Custom.form.php`, `libvirt_helpers.php`; forum threads 120530,
  169610, 40526. UTM `VMWizardState.swift`, QEMU settings doc; utm
  #2557, #6043, #8211, discussion 3610. Parallels CPU and memory settings
  and vCPU allocation pages, KB 124264, KB 122771; forum threads 355582,
  360824, 368697. VMware Fusion processor and memory pages. Vagrant
  `Vagrantfile.erb`, base box guide; vagrant #8343; slate #515.
- Fly `fly launch` docs ("most apps need about 1GB of RAM"), pricing;
  community 21164 (staff on 256 MB), 22284; flyctl #1310, #4544.
  Railway scaling reference, plans, changelog 2023-09-15, station
  threads 75445eb8 ("you can't"), 909fafa3, 2f42cbb2. Hetzner FAQ; HN
  41517880. DigitalOcean create page, "can I make my droplet smaller",
  community question 2020-07-31. Coolify resource limits page; coolify
  #12414, #10676, #873. Dokku resource management; caprover #1068; kamal
  discussion 370. Codespaces machine-type docs; community discussions
  26377, 7087, 44714; github/docs #28019. Gitpod workspace classes;
  gitpod #18939, #11042, #8261. Render blueprint spec, compute plans;
  openclaw #5966.
- This repo: `docs/proposals/2026-11-19-where-a-project-runs.md` (the
  four-part shape for the host choice), DECISIONS I-724, I-784, I-1126,
  I-1168, I-1326, I-1364, I-1366, I-1376, I-645, I-485, I-484;
  `docs/research/mac-mini-nested-kvm.md` §7, §9, §10;
  `docs/features/own-hardware.md`; `docs/reports/2026-11-19-mac-mini-council.md`.
