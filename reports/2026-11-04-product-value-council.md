# repose product value review

Date: 2026-11-04. Method: one agent made a fact base of 58 user-facing features from `docs/features/`, the public docs, `docs/PRICING.md`, `docs/DESIGN.md` §4 and the proposals. Four council members scored each feature in two rounds. Each member had one lens: daily driver (a solo developer who runs agents all day), growth (reach and recognition), economist (cost per seat), and innovation (the market, checked by web search). In round 2, each member read the three other cards, changed scores or kept them with a reason, and voted. The chair (this session) counted the votes and wrote this report. The owner's decision on burst cores (after the council) is in "Owner decision: automatic burst by class". Each member's full round-2 reasoning is in the appendix.

## Executive summary

1. **repose wins on one loop.** You type `repose run` and close the laptop. Your uncommitted work, `.env`, logins, skills and MCP servers are on a real VM in about 14 seconds. The agents keep working, they ask you on your phone when they are stuck, and their work comes back as `localhost` and `git fetch repose`. All four members voted the same four features into the top: auto port forward (8.3), repose-ask and reply (8.2), run and attach (8.2) and sync at launch (8.1). Spend your build time on this loop.

2. **Port forwarding is the best feature, and its gaps are in the hours repose sells.** The forwards stop when you detach. A busy laptop port moves (3000 becomes 3001), and that breaks OAuth callbacks. The agent cannot reach your laptop. Your phone cannot open the `--done` link. All four fixes are size S or M. Details are in "Ports" below.

3. **Your snapshot view is correct, and the council agreed with it unanimously.** Automatic snapshots (nightly and on stop) score 5.9: keep them. Manual create, pick and restore score 2.1, the lowest of all features. Fork scores 3.9. Every member voted "gimmick" on both. Freeze them and build per-turn rewind in their place. Per-turn rewind was the top future feature, with four "build next" votes. One fact to correct: a resize by size class takes no snapshot (I-595). The snapshot you value "on resize" comes from the stop before the resize.

4. **A standing shared power host is the wrong shape. Burst cores on the hosts you already pay for are the right one.** An Android emulator cannot run inside a guest on Azure. The guest is already the second level of virtualization (L2), and an emulator needs a third level (L3), which Microsoft does not support. An idle shared box costs $770 to $992 a month. A pooled free-for-all lets the heaviest user set the typing lag for everyone: one tenant at full load took p90 keystroke latency from 4.3 ms to 44.5 ms. Do this instead, in order: reverse forwards to the laptop's emulator ($0), automatic burst cores from idle host cores (near $0), then a per-job spot VM for a real emulator after a mobile user asks. The council proposed burst as a lease that the user asks for. The owner overruled that after the vote: burst is automatic and has a ceiling per class (small up to 4 cores, large up to 6, xl up to 8).

5. **The biggest risks for a launch are outside the feature list.** The copied `gh` token has full scope, and all four members called it the top launch risk. The waitlist bug lets `GET /billing` join the waitlist. No `repose update` exists. On Azure, every seat loses $54 to $90 a month. A small entry plan at about $22 loses less cash per user than a Solo and answers exe.dev's $20.

6. **Trim herdr, desktop dashboard revamps and further work on fork.** herdr doubles the test surface of attach, and its laptop SSH session stops the machine from freezing. Two or more members voted trim on each of these.

## How the scores work

Each member gave 0 to 10 on six axes: **pull** (makes a stranger sign up or tell a friend), **retention** (makes a user come back daily), **novelty** (new, and not commodity elsewhere), **delight** (feels good to use), **value** (value for money to the user) and **cost** (cheap for repose to run and maintain). The score in the tables is the mean of the four members' round-2 cards. Pull and retention count double: score = (2P + 2R + N + D + V + C) / 8. Votes are in the order daily driver, growth, economist, innovation.

## Current features

### Lean in (the core loop)

| Score | Feature | P | R | N | D | V | C | Votes | Next move |
|---|---|---|---|---|---|---|---|---|---|
| 8.3 | Auto port forward while attached | 8.0 | 9.2 | 6.5 | 9.2 | 9.0 | 7.2 | 4 lean-in | Forward daemon with pinned ports, reverse forwards (see Ports) |
| 8.2 | repose-ask, questions, reply | 7.0 | 9.0 | 7.5 | 8.2 | 9.0 | 9.0 | 4 lean-in | Make it the center of a phone view, with a Stop button |
| 8.2 | run and attach | 9.0 | 9.2 | 6.2 | 8.0 | 8.2 | 6.5 | 4 lean-in | Show the lid closing in the launch video |
| 8.1 | Sync at launch (dirty tree, .env, submodules) | 8.8 | 7.0 | 8.8 | 8.5 | 9.0 | 6.8 | 4 lean-in | Show it as one "launch receipt" with the carry |
| 7.7 | Claude setup and MCP carry | 7.8 | 8.0 | 7.8 | 8.0 | 8.8 | 5.8 | 3 lean-in, 1 keep | Same receipt |
| 7.5 | git fetch repose | 5.2 | 8.5 | 6.8 | 7.5 | 8.5 | 10.0 | 1 lean-in, 3 keep | No change; it costs nothing |
| 7.1 | Agent wrappers (trust, dev shell, resume after stop) | 4.8 | 9.0 | 6.5 | 7.8 | 8.2 | 6.8 | 3 lean-in, 1 keep | Test resume for Claude Code and Codex at each release |

### Keep (it works, users return to it, few headlines)

| Score | Feature | Votes | Note |
|---|---|---|---|
| 7.1 | repose-notify | 4 keep | Part of the ask loop |
| 6.9 | Laptop tools carry | 4 keep | Part of the launch receipt |
| 6.6 | cp and paste (images into the agent) | 4 keep | Delight 9.0, the highest of all features |
| 6.5 | Agent states | 4 keep | Feeds `ls --watch` (future) |
| 6.4 | SSH and editors | 4 keep | Commodity (novelty 2.5), but users expect it |
| 6.3 | Notification channels | 4 keep | |
| 6.3 | Secrets ask flow | 4 keep | |
| 6.1 | Worktree agents | 1 lean-in, 3 keep | Several agents share 2 cores; burst cores help |
| 6.1 | Stop and start | 4 keep | |
| 5.9 | Auto snapshots, nightly and on stop | 4 keep | Snapshot in the background after the stop, so `repose stop` returns in seconds |
| 5.9 | tmux session | 4 keep | |
| 5.8 | Preinstalled base, five agents, guest agent guide | 4 keep each | Test Claude Code and Codex as first class. Mark opencode, Gemini and pi "installed, not tested". |
| 5.6 | Browser viewer, temp machines, links open on laptop | 4 keep each | |
| 5.5 | Multiple checkouts, ps and exec, host-restart freeze | 4 keep each | Host-restart freeze: novelty 8.5, a good engineering post |
| 5.2 | Browser bridge | 3 keep, 1 trim | Freeze its scope and get one external security review |
| 5.1 | status, ls, logs, events | 4 keep | |
| 5.0 | Named secrets | 4 keep | |
| 4.6 | Config menu, second machine from home | 4 keep each | |
| 4.4 | Freeze (core pause) | 1 lean-in, 3 keep | Users cannot see it. Its idle cores are the stock that burst leases sell. |
| 4.3 | MCP forward | 4 keep | |
| 4.1 | Resize | 4 keep | |
| 4.0 | Usage charts, scheduled jobs | 4 keep each | |
| 3.9 | Destroy and restore | 4 keep | Prevents a loss you cannot undo |
| 3.8 | machine.nix | 3 keep, 1 trim | Apply saves at each machine's next start, not as a rebuild of all machines at once |
| 3.7 | keep, base updates | 4 keep each | |
| 3.6 | Timezone carry | 4 keep | |
| 3.0 | Unused notice | 4 keep | |
| 2.8 | Limits and abuse controls | 4 keep | Low scores are correct here: it protects the fleet, not the user |

### Fix (the value is real, but a defect blocks it)

| Score | Feature | Fix (all four voted fix unless noted) |
|---|---|---|
| 7.6 | Tool login copy | Mint a short-lived, repo-scoped GitHub token per project through a repose GitHub App, in place of the full-scope `gh` token. Store the App key as named-secret ciphertext, so no fourth home for secrets appears. |
| 6.0 | Shared browser and MCP | Stop Chromium after 10 minutes with no CDP traffic, and kill orphan `chrome-headless` processes. Together they used 13.6 idle CPU hours in 14 days, the second-largest idle CPU sink. |
| 5.8 | Claude login share | Place all of an account's machines on the host that holds its login share. host-02 is live, so a second `/login` can happen now, and repeat logins drove a user away from Sprites. |
| 5.2 | Preview URLs | Build only the owner-only half: an HTTPS link behind the dashboard login, with a QR code, linked from `--done`. Do not build a public mode. |
| 4.9 | repose sync | On exit 6, list the files in conflict and give a one-key choice: keep machine, keep laptop or stash. |
| 4.8 | Install and CLI update | Ship `repose update` and a version-skew warning on attach. |
| 4.5 | repose open | Merge it into the forward daemon: `repose open -d PORT`, with `--list` and `--close`. |
| 3.2 | Plans and billing | Add a small 4 GB plan at about $22, then move seats to Hetzner (see Money). |
| 2.7 | Waitlist and seats | Fix the bug where `GET /billing` joins the waitlist before any launch post. |

### Trim or freeze

| Score | Feature | Votes | Action |
|---|---|---|---|
| 4.2 | Conductor tutorial | 3 keep, 1 trim | The daily driver says a swarm on 2 cores sets the wrong expectation. Growth wants it as a filmed demo. Keep the doc, and revisit after burst cores. |
| 4.1 | Dashboard (desktop revamps) | 4 trim | Stop full revamps. Keep Projects, Questions, Billing and Settings working, and put design time into the phone view. |
| 3.1 | herdr | 4 trim | Mark it best-effort, remove it from the attach test matrix, and fix the held SSH session that blocks the freeze, or remove the path. |

### Gimmick (unanimous)

| Score | Feature | Action |
|---|---|---|
| 3.9 | fork | Stop extending it. Copies default to `--temp`. Worktrees give parallel attempts inside one machine's memory, and three 8 GB copies fill a Solo plan right after the demo. |
| 2.1 | Manual snapshot create, list, restore, dashboard tab | Freeze the code. Remove the dashboard tab and manual create. Keep `snapshots restore` on the CLI as a support path. A whole-disk rollback is the wrong undo for an agent's mistake, and per-turn rewind is the right one. |

## Ports: is forwarding enough?

At the keyboard, yes. Score 8.3, the top of the list, and the economist measured its cost at about $2.40 of egress in 14 days for the whole fleet. It falls short in five places, and every member named the same five:

1. **It stops at detach.** You often detach, then open the browser to check the agent's work.
2. **A busy laptop port moves.** 3000 becomes 3001, and an OAuth callback registered for 3000 breaks.
3. **It goes one way only.** The agent cannot reach your laptop's Android emulator, Ollama or local database.
4. **A phone cannot reach it.** A `--done` link to `localhost:3000` does not open on the bus.
5. **The launch video does not show it.**

Build these, in order:

| Order | Change | Size |
|---|---|---|
| 1 | A laptop forward daemon: `repose open -d`, `[forward] ports` in `config.toml`. Ports are pinned and fail with an error on a conflict, they survive sleep, and a forward closes after 30 minutes with no bytes, so the freeze still works. | S |
| 2 | Reverse forwards: `[forward] reverse = [5037, 11434]`. adb to the laptop emulator is the main use, and it answers most of the Android question at $0 host cost. | S |
| 3 | Print `localhost:3000 -> your laptop` when a forward opens. `http://localhost` links that the machine opens go to the laptop. `repose status` lists forwards with their process names. | S |
| 4 | Owner-only HTTPS links with a QR code behind the dashboard login, used by `--done` and the browser viewer. No anonymous mode. | M |
| 5 | Later: a signed webhook endpoint per project, as the only public path. Leave public previews to `cloudflared`. | M |

## Cores: the power host question

**The facts.** A seat is 8 GB and 2 vCPUs. Cores are sold at up to 1.5 committed vCPUs per physical core (I-734). The fleet is busy 33 percent of the time, and the freeze gives back 29 to 48 percent of running CPU. Keystrokes stay smooth while busy guest vCPUs stay at or below the number of physical cores.

**The Android emulator.** It needs KVM. Today the stack is Azure Hyper-V (L0), then the NixOS host with KVM (L1), then the Cloud Hypervisor guest (L2), so an emulator in a guest is L3. Microsoft does not support L3. hostd also sets no `nested=on`. If L3 worked, it would still break host-restart freeze, because Cloud Hypervisor cannot save a guest that runs its own VMs. Without KVM, an x86 emulator runs 5 to 10 times slower. Settle the docs with two commands: `cat /sys/module/kvm_intel/parameters/nested` on host-02 and `ls /dev/kvm` in a guest.

**The council's answer, cheapest first:**

| Option | What | Build | Run cost | Votes |
|---|---|---|---|---|
| Laptop emulator bridge | Reverse forwards. The agent drives the emulator on the laptop over adb. Apple Silicon runs the ARM emulator fast. It is also the only option that helps iOS. It works only while attached. | S | $0 | 4 build-next |
| Timed resize | `repose resize --size xl --for 2h` | S | Memory for the time box | Stopgap |
| Burst-core lease (council design, replaced by the owner decision below) | hostd boots guests with Cloud Hypervisor `boot=N,max=M` (today it passes only `boot=N`, `ch.go:123`) and hot-adds 2 vCPUs (4 on xl) for a time box. Lease vCPUs run at low cgroup weight. hostd grants them only while busy vCPUs stay under physical cores, and refuses when the slice is full. A monthly allowance comes with each plan: 20, 50 or 120 core-hours on Solo, Plus or Pro. | M | Near $0: it sells idle time you already pay for. $0.13 per core-hour at Azure list price, $0.03 at Hetzner. | 3 build-next, 1 experiment |
| Redroid test | Android in a container. It needs binder in the guest kernel, not KVM. A one-day test could show whether UI tests run on today's hosts. | S | $0 | Experiment |
| Per-job spot VM | `repose device android`: a single-tenant Azure spot VM where the emulator is L2, joined with `adb connect` and shown in the browser viewer | L | $0.05 to $0.15 an hour, $0 when unused. Price near $1 a device-hour, under Genymotion's $3.60. | Experiment, after a mobile user asks |
| Bare-metal KVM pool | A Hetzner box where guests are L1. An EX44 pilot costs $50 to $60 a month for about 3 sessions. An AX162-2 holds about 11 sessions and breaks even at about 34 subscribers on a $29 add-on. | XL | $50 to $992 a month idle | Build later, with the Hetzner move and BYO host (I-647) |
| Standing shared power host | Your original idea | M | $770 to $992 a month, idle most hours | Rejected |

Your "communism" instinct is correct for idle cores. Memory stays the guarantee. Cores become a bonus with a ceiling, so one heavy user cannot raise everyone's typing lag.

### Owner decision: automatic burst by class

After the council voted, the owner decided (2026-11-04) that burst cores are automatic. A user never asks for them, and no allowance or lease command exists. Each class has a ceiling:

| Class | Base cores (sold) | Burst ceiling |
|---|---|---|
| small | 1 | 4 |
| large | 2 | 6 |
| xl | 4 | 8 |

The chair's notes on how this changes the council's design:

- **Hotplug is not necessary.** hostd can boot each guest with the ceiling as its vCPU count (`boot=4`, `6` or `8`) and keep the sold share with cgroups. `cpu.weight` keeps the class weights (I-737), and the extra vCPUs get CPU time only when the host has idle time. This is simpler than Cloud Hypervisor vCPU hotplug, and the guest kernel does not have to bring CPUs online or offline.
- **The latency gate stays, and it becomes automatic.** Measured on prod: keystrokes stay smooth while busy guest vCPUs stay at or below physical cores, and one tenant at full load took p90 to 44.5 ms. hostd must cap burst time when the host approaches that line. A `cpu.max` on each guest above its base share is one way to do it. The latency check on host-02 that the growth member asked for is now a release gate for this feature.
- **Placement and the 1.5x rule (I-734, I-736) count base vCPUs only.** If placement counts the ceiling, a host holds about half the seats.
- **No request, no lease.** Users never ask for burst. The plan includes burst core-hours, and usage above them is billed only when the user turns overage on (see "Decision: metered burst, overage off by default" below). Plans still sell memory, and the public docs say cores above the base are best-effort.
- **It is visible.** `repose status` shows the cores the machine can use now, so a slow build on a full host has an explanation. The guest agent guide says the same thing, so agents do not plan for 8 cores on a busy host.
- **It works with the freeze.** A frozen guest uses no cores, and the cores it frees are the ones other guests burst into.
- **Open question for the owner:** do small guests on Solo get burst at once, or only after the latency check passes on host-02? The council's risk case was one tenant raising everyone's typing lag, and a burst to 8 cores makes that tenant heavier than today.

This decision needs a DECISIONS.md entry before anyone builds it, because it changes I-734 placement accounting and the cgroup design in I-737.

### Burst and the plans

The council's lease had a revenue hook. The plan included the allowance, and the economist proposed selling more core-hours later as an add-on. The $0.13 and $0.03 per core-hour figures are what idle capacity costs at list price, not a price. Burst sells cores that repose already pays for, so any charge for it is close to pure margin.

On 2026-11-04 the owner first said that both of these directions are acceptable. The owner then chose a metered charge (see the decision below). The two directions were:

1. **Charging for burst.** Automatic burst can still earn money. The council's other priced items show the shape: a per-job Android spot VM at about $1 a device-hour, against a cost of $0.05 to $0.15, and a bare-metal KVM pool as a $29 add-on that breaks even at about 34 subscribers. Any charge has to keep the automatic rule: the user never requests a core, and burst never stops in the middle of a build because a counter ran out.
2. **Priority for higher plans.** When several guests on one host want burst at the same time, hostd gives the burst vCPUs of Pro guests a higher cgroup weight than Plus guests, and Plus guests a higher weight than Solo guests. Nobody asks for anything, and nobody counts anything. On a quiet host, all guests reach their class ceiling. On a busy host, Pro gets the larger share of the idle cores. This gives Plus and Pro a value beyond memory, at no cost to repose. The base share that each class buys (I-737) stays the same on all plans, so a Solo user's sold cores never lose to a Pro user's burst.

Facts that apply to both directions:

- The latency gate comes before priority and before price. Neither a paid add-on nor a Pro weight may push busy guest vCPUs past physical cores. One tenant at full load took p90 keystroke latency to 44.5 ms for all guests (2026-10-14).
- A charge or a tier difference is something a user can see, so it ships with its update to `docs/PRICING.md`, `docs/features/pricing.md` and the public billing docs, under a DECISIONS entry.
- For the owner's goal (reach, low cash burn), the tier priority costs nothing to run and adds a reason to upgrade. A paid add-on brings in cash, but it needs a meter and billing code (Polar usage events), and support has to explain it.


### Decision: metered burst, overage off by default

The owner chose this on 2026-11-04, after comparing it with a flat add-on that raises the ceiling and with plan-set ceilings. It follows the egress model that already ships: automatic use, an allowance in the plan, and a metered charge above it. The owner's condition: **users must be told clearly that they can turn overage on.**

**How it works**

- hostd measures CPU time above each guest's base share from the guest cgroup's `cpu.stat`. Only time above the base counts. The sold cores (1, 2 or 4) are never metered.
- Each plan includes burst core-hours per billing period. The council's 20, 50 and 120 on Solo, Plus and Pro is the starting proposal, with the aim that most users never reach the allowance. The numbers need checking against measured burst on host-02.
- **Overage is off by default.** When the allowance runs out, burst stops and each machine runs on its base cores until the period ends. Nothing stops: builds and agents continue, at base speed.
- **A user can turn overage on**, with a monthly cap in dollars. Burst then continues past the allowance and is billed per core-hour. At the cap, burst stops the same way it stops at the allowance. A proposed starting price is about $0.04 per core-hour: the marginal cost is about $0, and the most it can cost repose is $0.13 per core-hour at Azure list price. At that price, an xl that bursts its 4 extra cores for 4 hours a day for 22 days pays about $14 (352 core-hours).
- Billing uses the same path as egress overage: a metered charge on the Polar subscription, shown on the invoice, with "Additional metered charges may apply" already at checkout.
- The latency gate still comes first. Overage buys burst time, but only while the host has idle cores. Paying never pushes busy guest vCPUs past physical cores.

**Where users learn that they can turn it on**

Each of these is a line that unblocks the user or prevents a surprise, so each one passes the "Trust the reader" rule:

| Place | What the user reads |
|---|---|
| `repose status` | `burst: 14.2 of 20 core-hours this period, overage off`. With overage on: `burst: 26.0 core-hours, 6.0 over, $0.24 of a $10 cap`. |
| Notification at 80 percent of the allowance (once per period) | `Your machines used 16 of this period's 20 burst core-hours. When they are used up, machines run on their base cores until 1 December. You can turn on burst overage, with a cap, at https://repose.herakraft.co/billing.` |
| Notification when the allowance is used up (once per period) | `Your machines used this period's 20 burst core-hours and now run on their base cores until 1 December. To keep bursting, turn on burst overage at https://repose.herakraft.co/billing and set a monthly cap.` |
| Dashboard billing page | A Burst row: a usage bar against the allowance, an Overage switch (off), and a cap field that the switch requires. |
| Guest agent guide | One line: when burst is used up, the machine has its base cores only, and the user can turn on overage on the billing page. An agent that sees a slow build can then tell the user why. |
| Public docs | `billing.md` (what is included, the price, the cap, the default), `limits.md` (best-effort burst ceilings per class) and `machine.md` (cores the machine can use). |

The switch lives on the dashboard billing page, next to the plan. No new CLI command is proposed: `repose status` shows the state, and the notification links to the page.

**Before it is built**

- A DECISIONS entry. It changes I-734 placement accounting (base vCPUs only), the cgroup design in I-737, and adds a metered charge next to egress in `docs/PRICING.md` and `docs/features/pricing.md`.
- The latency check on host-02 with guests booted at their ceiling.
- Allowance numbers and the price, set from measured burst.


## Future features

Mean of the members who scored each one in round 2.

| Rank | Feature | P | R | N | Size | Votes | Why |
|---|---|---|---|---|---|---|---|
| 1 | **Per-turn rewind**: each agent turn writes the tree, untracked files included, to a hidden git ref. `repose rewind` lists the last 20 turns. | 7.0 | 7.0 | 7.2 | S (git-ref version) | 4 build-next | The undo that agent users want. It replaces the snapshot picker. |
| 2 | **Forward daemon with pinned ports** | 4.8 | 8.0 | 4.0 | S | 4 build-next | See Ports |
| 3 | **Reverse forwards and the laptop emulator bridge** | 6.0 | 6.3 | 7.0 | S | 4 build-next | See Ports and Cores |
| 4 | **Burst cores** (council: lease; owner: automatic up to 4, 6 or 8 by class, metered above an allowance, overage off by default) | 6.3 | 5.8 | 6.3 | M | 3 build-next, 1 experiment | See Cores |
| 5 | **Owner-only phone preview link** | 6.8 | 6.0 | 3.3 | M | 4 build-next | Closes the `--done` loop on the phone |
| 6 | **Scheduled agent prompts**: `repose schedule 'weekdays 07:00' -p 'triage new issues'` | 5.2 | 7.0 | 4.8 | S | 3 build-next | Timers, wake and notify exist already. It is a CLI wrapper. |
| 7 | **Prompt queue per agent**: prompts start when the turn ends | 4.5 | 7.0 | 5.2 | S | 3 build-next | A worker does not sit idle overnight |
| 8 | **Repo-scoped GitHub tokens** through a repose GitHub App | 4.0 | 4.5 | 5.0 | M | 4 build-next | Closes the top launch risk |
| 9 | **Card-less taste tier and a small plan**: one small temp machine, 10 hours a month, from a fixed pool of 2 to 4 seats | 8.2 | 4.2 | 2.3 | S | 3 build-next, 1 experiment | The highest pull of any future item. It needs miner checks. |
| 10 | **Vendor remote-control host**: each machine registers with Claude Code Remote Control and Cursor's worker | 7.0 | 8.0 | 7.0 | S | 1 build-next (new in round 2) | The vendors' phone apps then drive your always-on machine, and repose builds no mobile UI. |
| 11 | **Live agent board**: `repose ls --watch` | 3.5 | 7.8 | 4.2 | S | 2 build-next, 2 later | |
| 12 | **Shareable overnight recap page**: commits, PR links, screenshots, hours run with the lid closed | 7.3 | 3.7 | 7.5 | M | 1 build-next, 3 experiment | Each share advertises repose. Under the logging rule, it may use only commits, links and screenshots the agent chose. |
| 13 | Phone console with web push | 5.8 | 8.0 | 4.8 | M | 3 build-next, 1 later | Innovation found that Claude Code Remote Control and Cursor's iOS app own this surface. Item 10 may be the cheaper path. |
| 14 | One-tap stop from notifications | 1.0 | 3.2 | 2.0 | S | 4 build-next | Small and safe |
| Later | Signed webhook endpoint, Telegram and Discord, Tailscale join, memory half of the freeze (encrypted swap) | | | | | build-later or experiment | |

## Money

| Fact | Number | Source |
|---|---|---|
| Azure seat cost on D64s_v7 at 24 seats | about $129 a month | PRICING note |
| Net per Solo seat after Polar | $46.05 | I-604 |
| Loss per Azure seat | $54 to $90 a month, paid from credit | |
| Hetzner AX162-2 seat at 30 seats | $33.07; every plan clears it | I-642 |
| Small 4 GB plan at $22 on Azure | loses about $44 a user, compared with about $83 for a Solo | Economist |
| Competitors, always-on 8 GB | Codespaces about $263, exe.dev $151.65 (2 vCPU and 8 GB for $20 on its entry plan), Fly shared about $51 | proposals/2026-10-11-price-level.md |

For your goal (recognition, low cash burn), the economist and growth members agree on the order. Fix the waitlist and the token first. Then add the small plan and the capped taste tier. Then move seats to Hetzner before credit falls below three months of burn.

## What repose should be known for

The four theses agree. repose is the machine where your exact laptop state lands in 14 seconds, your agents keep working with the lid closed, and they reach you on your phone. The launch should show three moments that the current video leaves out: the lid closing, the phone answer and the `localhost` payoff. For the hire, the members value engineering posts more than features: the machine-level agent guide, the browser bridge security model, and agents that survive a host restart.

## Where the council disagreed

- **Burst cores now or later.** Daily driver and innovation said build next. Growth said experiment, with a latency gate on host-02. The economist moved to build next in round 2. All four members designed it as a lease that the user asks for. The owner chose automatic burst instead, metered above an included allowance with overage off by default, and users told clearly that they can turn it on (see Cores).
- **Browser bridge.** The daily driver voted trim, because it needs the laptop open, which works against the lid-closed loop. The other three voted keep, because it costs no host resources and makes a good security post.
- **Phone surface.** Daily driver and economist want a repose phone console. Innovation lowered it, because Claude Code Remote Control and Cursor's iOS app already exist, and proposed registering with them instead.
- **machine.nix.** The economist voted trim, because of fleet-wide rebuilds on each save. The other three voted keep and agreed on the same fix.

## Owner picks (2026-11-10)

The owner chose these items from the build list for proposals. Each proposal is in `docs/proposals/`:

| Item | Proposal |
|---|---|
| Waitlist joined by `GET /billing` (already fixed by I-1033; the rest) | `2026-11-10-waitlist-billing-get.md` |
| CLI update and version skew | `2026-11-10-cli-update-and-skew.md` |
| Idle Chromium and stray headless browsers | `2026-11-10-idle-browser.md` |
| One-key choice on `repose sync` exit 6 | `2026-11-10-sync-conflict-choice.md` |
| Reverse forwards and visible forwards | `2026-11-10-forwards-both-ways.md` |
| Automatic burst cores and metering (key) | `2026-11-10-burst-cores.md` |
| Claude Remote Control and Cursor host, with an off switch | `2026-11-10-remote-control-host.md` |
| Webhook routes per service (key) | `2026-11-10-webhooks.md` |

Parked and documented only: scheduled agent prompts (`2026-11-10-scheduled-prompts.md`) and the overnight recap page (`2026-11-10-recap-page.md`). The owner decided to remove `repose fork` (`2026-11-10-remove-fork.md`). The other items on the list are not planned now.

## Appendix: each member's round-2 card

These are the members' own words from round 2. In them, burst cores are a lease that the user asks for, because the owner's decision came after the vote.


### Daily driver

**Thesis.** For someone running several agents all day, repose earns its $49 through one move: you type `repose run`, close the lid, and your dirty branch, .env, logins, skills and MCP servers are on a real VM with agents still working, and their commits come back with `git fetch repose`. Port forwarding, image paste and phone answers make that remote machine feel like your laptop, and you return to them daily. Whole-disk restore, fork, herdr and desktop dashboard polish demo well and get little use. Per-turn rewind, forwards that outlive the terminal and run both ways, a phone console and burst cores would earn hourly use. Repose should be known for agents that keep going after you close the laptop and that you answer from your phone.

**Lean in (votes).**
- run-attach together with sync-at-launch and agent-setup-carry: one command that lands your dirty tree, logins, skills and MCP servers on a VM with agents running
- auto-port-forward, extended with a background daemon with pinned ports and reverse forwards
- repose-ask-reply, made the center of a phone console with web push

**Cut (votes).**
- manual-snapshot-restore: freeze it, drop the dashboard tab, build per-turn rewind instead
- herdr: freeze as best-effort; it doubles the attach surface and blocks the freeze
- fork: stop extending; worktrees cover parallel attempts inside one plan's memory

**Ports.** Attached forwarding covers most of my desk hours. It falls short in five places. (1) It ends on detach, and I detach right before I open the browser to check an agent's work. (2) A busy laptop port moves 3000 to 3001, which breaks OAuth callbacks registered for 3000. (3) It runs one way: the agent cannot reach my laptop's Ollama, local database or Android emulator (the economist's point, which I missed in round 1). (4) A phone cannot reach it, so a --done link to localhost is dead on the bus. (5) Nothing outside tmux lists open forwards. Next, in order: (a) reverse forwards in config.toml, S, which also answers most mobile developers' emulator needs at zero host cost; (b) a laptop forward daemon (`repose open -d`, `[forward] ports`) with pinned ports that fail loudly instead of moving, reconnect after sleep, and drop after 30 minutes with no bytes so freeze still works, S; (c) http://localhost links opened on the machine open on the laptop through the forward, S; (d) an owner-only HTTPS link with a QR code behind dashboard login, linked from --done, M, with no public mode. Leave public URLs to cloudflared.

**Cores.** Two problems sit under the request. The common one is bursty CPU on a two-core large: a build, a test suite, four agents running tsc. The rare one is the Android emulator, which needs KVM. Order by cost. (1) Laptop emulator bridge through reverse forwards: S build, $0 host cost. Apple Silicon laptops run the ARM emulator well and the agent drives it over adb; it is the only option that also helps iOS tooling. It works only while attached. (2) Burst cores on existing hosts: start with `resize --size xl --for 2h`, then CH vCPU hotplug (boot=2,max=6) at low cgroup weight, metered against a core-hour allowance (for example Solo 20, Plus 50, Pro 120 a month), granted only while host busy vCPUs stay under physical cores and refused when the slice is full. M build, run cost near zero, because it sells idle time on a 33 percent busy fleet. I keep build-next where the economist said experiment; the lease rule answers his neighbour-latency concern. (3) Power runner: a headless emulator on a per-job Azure spot VM with nested virt (emulator at L2) joined by `adb connect` and shown in the browser viewer, about $0.05-0.15 an hour with nothing held idle. Experiment once a mobile user asks. (4) A standing shared power host or bare-metal KVM pool: build-later. Azure cannot run an emulator inside a guest (L3), and an idle $770-992 box burns cash the owner lacks; route it through BYO host shape A. The pooled free-for-all version is the wrong shape, because without a lease the heaviest user sets everyone's typing lag. Spend one hour checking /dev/kvm with CH nested=on on host-02 so the docs stop guessing.

**What changed from round 1.** claude-login-share keep to fix (economist: host-02 makes a second login real). preview-urls trim to fix, owner-only only (three cards: the phone is where --done lands). scheduled-jobs trim to keep with a helper (growth, innovation). frozen-pause lean-in to keep (invisible to a daily user; its value reaches me as burst). repose-ask-reply retention 8 to 9; browser-viewer pull 3 to 4; five-agents novelty 3 to 4. Added reverse forwards and the laptop emulator bridge as build-next (economist), and folded them into auto-port-forward and the cores answer. Merged web push into innovation's phone console (build-next). Repo-scoped tokens build-later to build-next to match my tool-login-copy fix. Owner-only preview experiment to build-next. Added the shared worktree dependency cache. Kept against the others: conductor-tutorial trim (wrong expectation on two cores), browser-bridge trim (needs the lid open), git-fetch-repose lean-in, burst build-next over the economist's experiment.

**Verdicts and the one change each member asked for.**

| Feature | Verdict | Change | Why |
|---|---|---|---|
| run-attach | lean-in | On `repose attach`, print one line per agent with what it did while you were away (commits, turns ended, questions answered), and reattach on laptop wake with no 2-minute cutoff as the economist proposed. | I type this dozens of times a day, and every other feature hangs off it. The economist's wake-reattach point is right: a lid close past 2 minutes costs me a retyped command every morning. |
| tmux-session | keep | Ship a tested terminal matrix (iTerm2, Ghostty, WezTerm, Kitty, VS Code) for Shift+Enter, mouse and truecolor, and push the `?` marker into the laptop terminal title so I see it without attaching. | The `?` marker is my cheapest attention signal across five agents. Growth and innovation are right that it should travel to the tab title. |
| herdr | trim | Freeze herdr at its current scope as best-effort, drop it from the run/attach/ps/paste test matrix, and fix the laptop SSH session that blocks freeze. | All four cards agree. A second multiplexer doubles the attach surface and its held session defeats the CPU pause. |
| five-agents | keep | Make Claude Code and Codex first class with tested resume and hooks; label opencode, Gemini and pi installed-not-tested so a base update never blocks on them. | I run two of the five. Innovation's point that agent-neutral is a wedge against vendor clouds moves novelty up one, but the upkeep is the same. |
| agent-wrappers-hooks | lean-in | Gate every base bump on a per-agent start, resume and trust smoke test, and print a 'resumed 3 agents' line on attach. | Resume is what makes stop safe to press, and stopping is how I juggle 8 GB. The economist's smoke test is the cheapest way to keep it true. |
| agent-states | keep | Add needs-input for Codex through its notify hook, so both agents I use report it and the 90 s guess goes away for them. | Which of four agents needs me is the question I ask every hour. Half coverage weakens trust in the column. |
| agent-setup-carry | lean-in | Ship `repose mcp login NAME` for OAuth MCP servers from the laptop browser, and print the carry receipt (skills 14, MCP 6 of 7, 1 needs SECRET_X) on first run. | My skills and MCP servers arriving without a dotfiles repo is what separates this from a Hetzner box. OAuth servers are why my carried setup is half broken today. |
| claude-login-share | fix | Place all of an account's projects on the host that holds its login share by default; re-login only on a forced move, with a notice first. | Moved keep to fix after the economist's card: host-02 is live, so a second /login is now a real event, and repeat logins drove a user off Sprites. |
| guest-agent-guide | keep | Run a five-task eval (port, secret, notify, install, fetch) per agent on each base update, and cut guide lines that change no result. | Invisible to me, but it stops agents running apt or hunting for ngrok. Innovation's eval beats my process-name count. |
| worktree-agents | lean-in | Add `repose worktrees` (branch, agent state, unfetched commits, remove) and a shared pnpm store and cargo target so a new worktree does not reinstall from scratch. | Parallel agents on separate branches is how I run several at once; others call it commodity, but it is the reason fork is unnecessary. Two shared cores is where it hurts, which burst cores fix. |
| multi-checkout | keep | Offer `--on` at the refused-start prompt when plan memory is full, and group `ls`/`ps` windows by checkout. | My api and web repos on one 8 GB machine saves real money on Solo. Growth and innovation are right that it needs a prompt at the memory wall to be found. |
| conductor-tutorial | trim | Fold it into the worktree page as one example that states a swarm wants xl, and drop T3 Code until it is tested. | I still disagree with keep: a swarm on two cores saturates the default size, so the tutorial sells a workflow the default cannot carry. Revisit once burst cores exist. |
| ps-exec | keep | `repose ps PROJECT WINDOW -f` to follow a window's output from the laptop without attaching. | Peeking at a window's last lines from a side pane is a quiet daily habit. Cheap SSH reads. |
| sync-at-launch | lean-in | Print a one-line receipt (branch, N uncommitted files, M .env files, skipped over-cap files by name) and lead the landing with that fact. | The only unique thing per research, and all four cards lean in. Codespaces cannot take my uncommitted work. |
| repose-sync | fix | On exit 6, show the overlapping files with a one-key choice (keep machine, keep laptop, stash machine); then add `repose sync --pull` for the machine's uncommitted changes. | I hit it a few times a week and the conflict path bites. Innovation's reverse sync is the second half, but conflicts cost me more today. |
| git-fetch-repose | lean-in | Print unfetched branch counts on `attach`, `ls` and `stop`, and put the exact `git fetch repose && git switch BRANCH` line in each --done notification. | Getting commits back without a GitHub round trip is the cleanest part of my loop, and free to run. I hold lean-in where others say keep: I use it more than any feature but run-attach. |
| ssh-editors | keep | Add mosh or an equivalent roaming transport, and stop an idle VS Code server from blocking freeze. | `project.repose` in VS Code, scp and rsync with no config is how I review diffs. Parity with Codespaces, so no pull. |
| cp-paste | keep | Add a per-terminal paste regression test and a 2-second inline notice with the remote path, so a failed paste is visible. | Pasting a screenshot into remote Claude Code feels local and I do it many times a day. The input proxy is fragile and the fragility is felt. |
| links-open-on-laptop | keep | Extend to http://localhost links so a dev server's printed URL opens on the laptop through the forward. | Claude /login and gh auth just work. Negligible cost. |
| timezone-carry | keep | None; leave it. | Log timestamps and git dates in my zone. Free. |
| auto-port-forward | lean-in | A laptop forward daemon that keeps pinned ports per project up while detached, plus reverse forwards (laptop to machine) for Ollama, a local database or adb. | localhost:3000 appearing with OAuth cookies intact makes the machine feel local. The economist's reverse direction is the gap I missed in round 1. |
| repose-open | fix | Merge it into the forward daemon: `repose open -d PORT`, listed and closed with `repose open --list/--close`. | All four cards say foreground-only is the problem. One terminal tab per port is a chore. |
| preview-urls | fix | Build only the owner-only half: a dashboard-cookie URL with a QR code, linked from --done notifications, no public flag. | Moved trim to fix: the other three showed the phone is where I am when --done fires, and a dead localhost link there breaks the loop. Owner-only keeps the hosting abuse vector shut. |
| shared-browser-mcp | fix | Stop the browser after 5-10 minutes without CDP traffic instead of 30 and kill orphan chrome-headless processes; together 13.6 idle CPU hours in 14 days. | Agents checking their own UI is daily value for frontend work. As shipped it is the second-largest idle CPU sink. |
| browser-viewer | keep | Ship the 2x Retina desktop, then make the viewer reachable on the phone through the owner-only link so a night captcha gets solved from bed. | Pull up one after growth's demo point. I open it about once a week, and it unblocks the agent each time. |
| browser-bridge | trim | Freeze scope, commission one external security review, and instrument monthly use before any extension. | I hold trim where others keep: it needs the laptop open, which cuts against the lid-closed loop, and it is security-sensitive code for an occasional path. |
| mcp-forward | keep | Fix docs/features/README.md and reuse its pattern for the reverse port forward. | Niche and laptop-bound. The economist's note that it seeds reverse forwards is its best use. |
| notifications-channels | keep | Add web push from a dashboard PWA so a phone gets answer buttons without ntfy setup. | Getting pinged lets me leave the desk. Three cards agree web push beats Telegram first. |
| repose-notify | keep | Include machine, window, diff stat and the fetch line in every --done, with a Stop button. | An agent saying 'done, PR at X' on my phone closes the loop. Tiny cost. |
| repose-ask-reply | lean-in | Make it the center of a phone page: open questions, the window's last 30 lines, answer buttons, delivered by web push. | A two-hour stall becomes a 20-second tap. Retention up one after growth and innovation: this is the reason I trust closing the lid. |
| secrets-ask | keep | Offer 'store as global' on the answer page so the next project does not ask again. | Keeps keys out of transcripts a few times per project. Cheap. |
| named-secrets | keep | Show in `secrets list` which running processes started before a value changed. | Plumbing Codespaces also has. The economist's stale-env hint saves me a confused restart. |
| tool-login-copy | fix | Mint a repo-scoped, short-lived GitHub token per project through a repose GitHub App in place of copying the full-scope gh token. | gh and Codex logged in on arrival is the best frame in the video. All four cards rank the full-scope token as the top launch risk. |
| laptop-tools-carry | keep | Cache the resolved tool list per account so a second machine starts with them installed. | 'Installing 12 of your tools' is a screenshot moment. The background install per machine burns minutes of CPU. |
| preinstalled-base | keep | Generate the base contents page from the nix closure each release, and add the top command-not-found names. | Docker, Chromium and languages present means the agent works in second one. Shared store keeps it cheap. |
| config-menu | keep | A central binary cache for menu items so `config add postgresql` takes seconds and stops competing with guests for host cores. | Postgres in one line beats compose. Host builds show up as my typing lag. |
| machine-nix | keep | Apply a save lazily at each machine's next start or attach, never as a fleet-wide rebuild burst. | My aliases everywhere matter to me. The rebuild-all on save is the waste, and three cards name the same fix. |
| base-updates | keep | Build the base once and push closures, gated on a Claude Code and Codex resume smoke test in a canary guest. | Fresh agents weekly without my effort. A bump that breaks resume mid-week would cost me a morning. |
| scheduled-jobs | keep | Add a guest helper `repose-schedule "daily 07:00" -- CMD` that writes the two unit files. | Moved trim to keep: growth and innovation showed a nightly agent run is a real use, and a helper is S cost. Hand-written timers stay a chore. |
| auto-snapshots | keep | Take the stop snapshot in the background after the machine reports stopped, and upload incrementally, so `repose stop` returns in seconds. | Insurance I never think about; the owner calls it fantastic. Sprites checkpoints faster, so it is no headline. |
| manual-snapshot-restore | gimmick | Freeze the code, drop the dashboard tab and manual create, keep CLI restore as a recovery path, and build per-turn rewind. | All four cards agree. A whole-disk rollback after a stop is the wrong undo for agent mistakes. |
| destroy-restore | keep | None; it prevents a loss I cannot undo. | Used once a quarter, and that once saves a week. Low cost. |
| fork | gimmick | Stop extending it and point parallel attempts at --worktree; default fork copies to --temp. | Three 8 GB copies blow a Solo plan, so I use worktrees. Unanimous across cards. |
| second-machine-home | keep | Default `repose run scratch` to --temp unless kept. | A scratch machine from home is how I try tools. Fine as is. |
| temp-machines | keep | Use temp as the default for runs outside a git repo and for fork copies. | Disposable machines clean up without me remembering. |
| keep | keep | None. | Escape hatch that makes temp safe. Free. |
| stop-start | keep | Put a Stop button in the unused notice and --done notifications, fetching commits first. | Stop that fetches commits and resumes agents makes memory juggling painless. One tap from the phone makes me use it more. |
| frozen-pause | keep | Spend the reclaimed cores as burst for active guests, and count only moving bytes on a held forward or editor as use. | Moved lean-in to keep: from my seat it is invisible apart from a 1-2 s wake, and the memory half is an owner ruling. Its value to me arrives through burst cores. |
| unused-notice | keep | Add a one-tap Stop in the notification itself. | Recovers 92 percent of a forgotten machine's hours only if I act. Unanimous fix. |
| host-restart-freeze | keep | Report it after the fact in one event line, and run a monthly restore drill that fails loudly. | I would never know it happened, which is the value. The economist is right that a rare path rots without a drill. |
| resize | keep | Allow `repose resize --size xl --for 2h` that falls back on its own, as the first cheap form of burst. | I resize for a slow build and forget to size back. Commodity. |
| usage-charts | keep | Put 'waiting for a core' as one number in `repose status` and tie it to the burst offer. | I open it when the machine feels slow, about weekly. |
| status-ls-logs-events | keep | Build `repose ls --watch` as a compact live board with a key to attach to the selected window. | `repose ls` is my hourly glance. With five agents a live board would sit in a side pane all day. |
| dashboard | trim | Stop desktop revamps and spend design time on the phone view of Questions and project state. | Three other cards converge on the phone view, which I accept. I hold trim on the desktop surface: I visit it weekly for billing. |
| plans-billing | fix | Add a small 4 GB entry around $19-25; exe.dev sells 2 vCPU and 8 GB for $20 and that number will appear in every thread. | $49 is fine for me against Codespaces, steep against a $5 VPS for a curious reader. Every Azure seat loses money anyway. |
| waitlist-seats | fix | Fix the GET /billing waitlist-join bug now. | Unanimous. A plan-less account landing on the waitlist is a lost signup and a corrupted demand signal. |
| limits-abuse | keep | Alert when one tenant's busy vCPUs exceed their share for 10 minutes, and name the limit in each refusal. | Keeps the bill and my neighbours' typing safe. I notice it only when it bites. |
| install-login-cli | fix | Ship `repose update` and a version-skew warning on attach. | All four cards name the missing update command. Stale CLIs against a weekly server cost me support time. |


### Growth

**Thesis.** Repose gives a heavy Claude Code user an agent that keeps working after the lid closes and asks on their phone when it is stuck. It starts in one command with uncommitted work, .env, logins, skills and tools already on the machine, and the result comes back as localhost and a `git fetch`. It should be known for the 'it texted me, I tapped yes, it finished' story, and the launch has to show three moments the current video omits: the lid closing, the phone answer and the localhost payoff. For reach, a card-less taste tier, a scoped GitHub token and a working waitlist decide whether an HN thread turns into trials or into a security pile-on. For the hire, engineering posts on the machine-level CLAUDE.md, the bridge security model and agents surviving host reboots carry more weight than any feature built on manual restore, fork, herdr or dashboard polish.

**Lean in (votes).**
- repose-ask-reply: the phone answer loop with web push, the 'it texted me, I tapped yes, it finished' story and the demo climax
- sync-at-launch, shown with agent-setup-carry and tool-login-copy as one launch receipt: dirty tree, .env, logins, skills and tools on a real VM in 14 s
- auto-port-forward: put the localhost payoff on screen, then extend it with forwards that outlive the attach and reverse forwards

**Cut (votes).**
- manual-snapshot-restore: drop the dashboard tab and manual create, keep a CLI recovery command, build per-turn rewind
- herdr: freeze as community-supported; it doubles the attach test surface and blocks the freeze
- fork: stop promoting; the wow ends at the plan memory wall, and worktrees cover parallel attempts

**Ports.** At the keyboard, it is enough, and the owner is right to call it amazing. It falls short in the hours repose sells. (1) It ends on detach, and detaching is what you do right before you open the browser to check an agent's work. (2) Busy laptop ports move from 3000 to 3001, which breaks OAuth callbacks registered for 3000. (3) A phone cannot reach it, so a --done message saying 'look at localhost:3000' is a dead link on the bus. (4) Traffic flows one way only; the agent cannot reach the laptop's Android emulator, Ollama or local database. (5) The payoff never appears in the launch video. Next, in order: show it on screen first (a status line 'localhost:3000 is on your laptop' and a browser opening it with OAuth working; costs nothing). Then forwards that outlive the terminal with fixed ports (`repose open -d`, `[forward] ports` in config.toml, dropped after 30 idle minutes so the freeze holds; size S). Then reverse forwards (`[forward] reverse`, size S), whose headline use is adb to the laptop emulator, which also answers most of the Android question for free. Then the owner-only phone preview link with a QR code behind the dashboard cookie, linked from --done (size M, R2-6 minus its public half). Leave public URLs to cloudflared; they are commodity and turn repose into hosting.

**Cores.** A standing shared power host is the wrong shape for this budget: an idle D16s_v7 is about $770 a month and an AX162 about $992, and with 28 seats on a fleet that runs 33 percent busy it would sit idle most hours. On Azure it cannot run the Android emulator anyway, since the guest is already L2 and a third level is unsupported. Split the request in two. For builds, test suites and swarms, the cheap version of 'communism' is burst cores on hosts you already pay for: boot guests with CH max_vcpus above boot, hot-add vCPUs for a time box at low cgroup weight from a monthly core-hour allowance, grant only while busy vCPUs stay under physical cores, and refuse when the slice is full. Run cost is near zero; the risk is the measured 17-45 ms typing lag, so ship it as an experiment with a latency gate on host-02, size M. A timed resize (`--size xl --for 2h`) is the S stopgap. For the Android emulator, build reverse forwards first (S, $0 host cost): the agent drives the emulator on the user's Apple Silicon laptop over adb while attached. After that, a one-box bare-metal KVM pilot (EX44 class, about $50-60 a month, about 3 sessions) is affordable as a visibility stunt ('your agent tests your Android app while you sleep'), gated on real mobile users asking and on Hetzner or BYO-host shape A existing. Spend one hour now checking whether /dev/kvm appears with CH nested=on so the docs stop guessing; until a pilot exists, the docs should say no Android emulator in the machine and point to the laptop bridge. Do not let mobile delay the Claude Code beachhead.

**What changed from round 1.** Moved to fix: claude-login-share (economist: host-02 makes a second /login real, and that story already cost Sprites a user), repose-sync (three cards see exit 6 as the support stream), shared-browser-mcp (13.6 idle CPU hours is a defect, cost_efficiency stays 3). Moved lean-in to keep: repose-notify (folded into the ask loop vote) and laptop-tools-carry (folded into the agent-setup-carry receipt). Raised: agent-setup-carry retention 7 to 8 (daily-driver's OAuth MCP point), auto-port-forward novelty 6 to 7 (innovation: plain-terminal forwarding beats editor-bound), cp-paste delight 8 to 9, frozen-pause cost_efficiency 9 to 10 (burst cores depend on it), status-ls retention 6 to 7. Lowered: fork pull 5 to 4 (wow then memory wall), browser-viewer pull 6 to 5 (1x on Retina spoils the clip), browser-bridge retention 4 to 3, plans-billing cost_efficiency 5 to 3 (corrected $129 seat). Kept against others: conductor-tutorial keep (filmed swarm is HN content), browser-bridge keep (security post value), frozen-pause keep not lean-in (no reach of its own). Future: adopted economist's reverse forwards and laptop emulator bridge as build-next and the cheap Android answer; merged my separate-host burst idea into a bare-metal KVM pool (build-later) and in-host burst cores (experiment); promoted repo-scoped tokens and phone console to build-next; added agent race as one new item.

**Verdicts and the one change each member asked for.**

| Feature | Verdict | Change | Why |
|---|---|---|---|
| run-attach | lean-in | Film the full loop in one take: `repose run -p`, the lid closes, a phone buzzes 40 minutes later with a question, one tap, then `git fetch repose` shows the commits. All four cards name this missing shot. | This is the five-minute trial and the headline demo, and all four members agree. The VM and the attach are commodity, so the video has to sell the carry and the lid close around them. |
| tmux-session | keep | Put the `?` needs-input marker in the laptop terminal title and tab badge, so ten tabs show which machine wants you. | Nobody signs up for tmux. Terminal quirks (Shift+Enter, mouse) are what a stranger hits in minute three, so the daily-driver's terminal matrix belongs in the launch checklist. |
| herdr | trim | Freeze it as community-supported, drop it from the run/attach/ps/paste test matrix, and fix the held laptop SSH session that blocks the freeze or remove the path. | A niche audience, a doubled test surface and a machine that never freezes. No demo or post needs it. |
| five-agents | keep | Make Claude Code and Codex first class with a resume smoke test per base update, and label opencode, Gemini and pi as installed but untested. | Agent-neutral reads well to a hiring cloud and lets a Codex user try it. I took the daily-driver's point: three extra agents are owner time each week, so they should not block a release. |
| agent-wrappers-hooks | lean-in | On attach after a stop or host restart, print one line naming the conversations resumed and how long ago. | Resume is why a user returns the next morning without rework. Users notice it only when it fails, so one visible receipt line turns it into proof. |
| agent-states | keep | Add needs-input for Codex through its notify hook, and mark hookless states as estimated. | The glance a user with three machines makes ten times a day, and it feeds the freeze. Half coverage of needs-input weakens trust in the column. |
| agent-setup-carry | lean-in | Print the carry as part of one launch receipt on first run (skills 14, MCP 6 of 7, 1 needs SECRET_X), and ship `repose mcp login NAME` so OAuth MCP servers do not leave the setup half broken. | Your skills, hooks and MCP servers working on minute one is what Claude Code on the web and Codex cloud do not do. I raised retention after the daily-driver's point that a half-broken OAuth server is the common failure. |
| claude-login-share | fix | Place all of an account's machines on the host that holds their login share by default, and notify before a forced move asks for a second login. | I moved to fix with the economist: host-02 is live, and repeated /login is the exact story that drove an HN user off Sprites. Affinity is a placement rule with no new secret home. |
| guest-agent-guide | keep | Publish it as a public page with a post on writing a CLAUDE.md for a machine, and add the daily-driver's measurement of apt and tunnel attempts before and after each edit. | Users never see the file, but the idea is good writing material for an Anthropic audience. Near-zero cost. |
| worktree-agents | keep | Add `repose worktrees` with branch, agent state and unfetched commits, and warn at launch when running agents outnumber cores. | Parallel agents are what heavy users post screenshots of, but Conductor, Cursor and Codex make it commodity. Two shared cores are where the demo falls apart, which ties it to burst cores. |
| multi-checkout | keep | When a start is refused for plan memory, offer `--on` to add this repo to the running machine. | More work per seat for free. Strangers will not find it without a prompt at the refused start. |
| conductor-tutorial | keep | Record a 90-second run of a conductor and four workers on an xl with branches coming back through `git fetch repose`, and state in the tutorial that a swarm needs xl. | I keep it against the daily-driver's trim because the swarm clip is HN material and the docs cost nothing. I accept that on a large it sells a workflow two cores cannot carry, so the tutorial must name the size. |
| ps-exec | keep | `repose ps PROJECT WINDOW -f` to follow a window from the laptop without attaching. | A quiet daily tool that brings in nobody new. Cheap SSH reads. |
| sync-at-launch | lean-in | Lead the landing with it ('your uncommitted work, .env and logins on a real VM in 14 s') and print a one-line receipt of what went, naming any file skipped over the cap. | All four cards and the research name this the one unique thing. The landing still leads with 'dev environment, replicated', which is Codespaces copy. |
| repose-sync | fix | On exit 6, list the overlapping files and offer a one-key choice (keep machine, keep laptop, stash machine). | I moved from keep to fix: three members see the conflict path as the support stream, and a stranger who meets exit 6 in week one tells that story. Reverse sync can wait. |
| git-fetch-repose | keep | Print unfetched branch counts on attach, stop and ls, and put the exact fetch-and-switch line in each --done notification. | Developers find work returning without GitHub clever, and it costs nothing. I keep it below lean-in because it is a supporting beat in the loop rather than the hook. |
| ssh-editors | keep | Test and document mosh or an equivalent roaming shell; one East US region plus no mosh is a stated reason non-US users leave. | Table stakes against Codespaces and Coder. An open editor also blocks the freeze, a hidden cost. |
| cp-paste | keep | Put a screenshot paste into remote Claude Code in the demo video, and spend support time on zero-setup paste in iTerm2, Ghostty and the VS Code terminal. | Developers who fought SSH image paste will share that clip. I raised delight to the daily-driver's 9; the fragile input proxy keeps it from lean-in. |
| links-open-on-laptop | keep | Extend it to http://localhost links so a dev server's printed URL opens on the laptop through the forward. | Invisible until /login stalls. The localhost extension is cheap and makes the forward payoff visible. |
| timezone-carry | keep | None. | A free nicety nobody mentions. A wrong timestamp would be noticed. |
| auto-port-forward | lean-in | Show the payoff on screen (agent starts a dev server, laptop browser opens localhost:3000 with OAuth working), then extend it with forwards that outlive the attach and reverse forwards for laptop services. | The owner calls it amazing and every card leans in. I raised novelty to 7 with the innovation card: Codespaces forwards only inside its editor, and repose does it in a plain terminal with docker -p. |
| repose-open | fix | Fold it into a background forward (`repose open -d PORT`, listed and closed from one place) so a forward survives the terminal. | All four cards agree that foreground-only makes it a chore. It becomes the second half of port forwarding once it runs in the background. |
| preview-urls | fix | Build only the owner-only half: a dashboard-cookie URL with a QR code, linked from repose-notify --done; leave public sharing to cloudflared. | Checking a build on your phone from bed is the missing step in the lid-closed story, and all four cards land on owner-only. Public previews are commodity and an abuse magnet. |
| shared-browser-mcp | fix | Stop Chromium after 10 minutes with no CDP traffic, kill orphan chrome-headless processes, and verify I-930 lets a machine freeze with Chromium open. | I moved from keep to fix: three members see 13.6 idle CPU hours in 14 days as a defect in the shipped feature. Agents testing their own UI stays a strong frame. |
| browser-viewer | keep | Ship the 2x Retina desktop before filming the captcha clip; at 1x on a MacBook it looks broken in the first five seconds. | A human solving a captcha in the agent's browser demos well. I lowered pull by one because the daily-driver's 1x point would spoil the clip as shipped. |
| browser-bridge | keep | Freeze scope, commission one outside review of bridgewarden, and publish the security model as a post. | I keep it against the daily-driver's trim because the design is new and a careful security write-up is hiring-team reading. Daily use is low since the laptop must be open, so retention drops to 3. |
| mcp-forward | keep | Fix docs/features/README.md, which still says not built, and leave it alone. | Clever but laptop-bound, the opposite of the main promise. Cheap to keep. |
| notifications-channels | keep | Add web push from the dashboard as a PWA before Telegram or Discord; it reaches phones with no ntfy setup. | I moved from Telegram-first to web push with the other three cards: ntfy setup is where most users stop, which leaves only slow email. Channels stay commodity. |
| repose-notify | keep | Attach a diff stat, the branch, the fetch line and the latest browser screenshot to each --done so the phone message works as a review teaser. | I moved from lean-in to keep. It is the outbound half of repose-ask, so my vote goes to the loop as one feature. |
| repose-ask-reply | lean-in | Make it the demo climax and the center of a phone page: open questions, last 30 lines of the window, answer buttons, delivered by web push. | 'It texted me, I tapped yes, it finished' is the word-of-mouth line, and no rival packages it. All four cards lean in. |
| secrets-ask | keep | Show it in the security post and the video: the agent asks for a key from a phone and never sees it in its transcript. | A careful touch security-minded developers share. Meets each user a few times per project. |
| named-secrets | keep | Offer `secrets import` on first run when a .env exists. | Commodity plumbing, invisible when it works. |
| tool-login-copy | fix | Mint a repo-scoped, short-lived GitHub token per project through a repose GitHub App, with the App key kept as an existing named-secret ciphertext, and say so in the launch receipt. | 'Logins copied' is the strongest frame in the video, and the full-scope gh token is the likeliest attack in a launch thread. All four cards say fix. |
| laptop-tools-carry | keep | Keep 'Installing 12 of your tools' in the launch receipt, and cache the resolved list per account so a second machine installs in seconds. | I moved from lean-in to keep and folded it into the agent-setup-carry receipt, since the frame proves one thing with two lines. Per-machine install burns guest CPU, as the daily-driver noted. |
| preinstalled-base | keep | Generate a docs page of base contents and versions from the nix closure each release. | Table stakes every rival ships. The shared store keeps it cheap. |
| config-menu | keep | Have agents run `repose config add` themselves when a tool is missing, and build the shared binary cache so a menu item takes seconds. | Users will ask an agent and never read the menu. Host builds compete with guests for CPU, which every user feels as typing lag. |
| machine-nix | keep | Apply a save lazily at each machine's next start or attach, never as a fleet-wide rebuild. | Nix users like it and strangers never touch it. I lowered retention and cost scores toward the economist; the rebuild burst is real. |
| base-updates | keep | Gate each update on a canary guest that starts and resumes Claude Code and Codex, and post a weekly 'agents updated to X' line. | Nobody praises fresh agents, but a broken resume mid-week gets tweeted. Owner ops time each release. |
| scheduled-jobs | keep | Wrap it as `repose schedule 'weekdays 07:00' -p 'triage new issues'`, which writes the timer and starts an agent. | Hand-written systemd timers are plumbing. A nightly agent prompt is a story people share. |
| auto-snapshots | keep | Take the snapshot in the background after the machine reports stopped, and print its age in one line on stop. | The owner calls it fantastic and it lets users let agents run wild. Sprites checkpoints in 300 ms, so it is insurance and no headline. |
| manual-snapshot-restore | gimmick | Drop the dashboard Snapshots tab and manual create, keep `repose snapshots restore` as a CLI recovery path, and spend the effort on per-turn rewind. | The owner rarely or never uses it, and a stop plus 30-90 s loses to sub-second restores elsewhere. Users want undo of the last agent turn. |
| destroy-restore | keep | None; it prevents a loss the user cannot undo. | Used rarely, and that one use saves a week of work. Small Blob cost. |
| fork | gimmick | Stop promoting and extending it; point parallel attempts at --worktree, and revisit only if burst cores land. | I lowered pull from 5: the wow is followed at once by the plan memory wall, which leaves a worse story than no fork. Worktrees cover the case inside one plan. |
| second-machine-home | keep | Make `repose run scratch` temporary unless kept, and advertise it as the way to try repose without a repo. | A scratch machine is how a stranger tries a tool. Cheap. |
| temp-machines | keep | Default a trial account's first run, home-folder machines and fork copies to --temp. | A try-it-once stranger leaves nothing behind and repose gets the memory back. It brings in few users alone. |
| keep | keep | None. | Necessary partner of temp. Free. |
| stop-start | keep | Put a Stop button in the unused notice and in --done messages, and name the machine to stop in a refused start. | Basic lifecycle done well. Fetch-on-stop is a thoughtful touch nobody sees. |
| frozen-pause | keep | Lend the CPU it frees to busy guests as burst cores, and write the cost post with the measured 29-48 percent. | I keep it at keep against two lean-in votes: users never see it, so it earns no reach on its own. I raised cost efficiency to 10 because burst cores depend on it. |
| unused-notice | keep | A one-tap Stop button in the notification. | Pure cost control that works only if acting takes one tap. |
| host-restart-freeze | keep | Write the post on a live agent surviving a host kernel update with encrypted memory images, and add the economist's monthly restore drill so the path does not rot. | Users never know it happened, which is the point. As a public engineering post it is the strongest cloud-team hiring signal in the codebase. |
| resize | keep | Allow `repose resize --size xl --for 2h` that falls back on its own, the cheapest form of burst. | Commodity, needed. The timed resize links it to the cores question. |
| usage-charts | keep | Surface 'waiting for a core' as one line in `repose status`, and link it to burst cores when that exists. | Few users open the tab. Honest contention numbers protect trust when a neighbour is busy. |
| status-ls-logs-events | keep | Ship `repose ls --watch` as a live agent board for a side pane. | Daily commands for power users, invisible to newcomers. I raised retention with the daily-driver's hourly glance. |
| dashboard | trim | Stop desktop revamps and make the phone view of Questions and project state the one polished surface. | The terminal is the product, and the phone is where a lid-closed user meets repose. Three cards say the same thing about where design time goes. |
| plans-billing | fix | Add a card-less taste tier (small, capped hours, temp by default) or a $19-25 small entry; exe.dev's $20 for 2 vCPU and 8 GB will appear in every HN thread. | A card-gated $49 trial loses most of a launch thread, and every Azure seat loses money anyway. I lowered cost efficiency to 3 with the economist's corrected $129 seat. |
| waitlist-seats | fix | Fix the GET /billing waitlist-join bug before any launch post, and send a 'your seat is ready' email with a one-click start. | Caps cash loss, which the owner needs. A broken waitlist on launch day wastes the traffic spike and corrupts the one demand signal. |
| limits-abuse | keep | Add the economist's per-host alert when one tenant's busy vCPUs exceed their share for 10 minutes, and name the limit in each refusal. | A miner or a hot tenant in launch week costs more than any feature earns. Invisible insurance. |
| install-login-cli | fix | Ship `repose update` with a version-skew warning on attach, and publish a measured install-to-first-agent time under 60 s. | The first 60 seconds decide whether a stranger tries anything else. CLI drift is a support source the agent guide already warns about. |


### Economist

**Thesis.** repose is worth $49 to you if you run Claude Code all day. One command puts your uncommitted work, .env, logins, skills and tools on a real VM, your agents keep going with the lid shut, and they reach your phone when they need you. Spend on that loop: sync-at-launch, phone answers and forwards that outlive the terminal. Each of them costs pennies a month in egress and CPU. Cut the features that cost upkeep or idle CPU without daily use: manual restore, fork, herdr and dashboard revamps. Most of the margin sits outside the feature list. Azure seats lose $54-90 a month each, a small entry plan loses less per user than a Solo, Hetzner clears every plan, and the frozen cores are stock you can sell as boost leases. Answer core-heavy work with leases and a free laptop emulator bridge. Rent KVM per job only after a mobile user asks.

**Lean in (votes).**
- sync-at-launch (with run-attach and agent-setup-carry as one launch receipt)
- repose-ask-reply (grown into a phone console with web push and a Stop button)
- auto-port-forward (extended with pinned background forwards and reverse forwards)

**Cut (votes).**
- manual-snapshot-restore (freeze, drop the dashboard tab, keep a CLI support path, build git-ref per-turn rewind)
- herdr (best-effort only; its held session defeats the freeze)
- fork (stop extending; copies default to --temp; worktrees cover parallel attempts)

**Ports.** For one developer at the laptop, auto-forward is enough, and it costs almost nothing: 28 GB of fleet egress in 14 days is about $2.40. It falls short in five places, and each fix is cheap. (1) Detach kills it. You close the attach and the dev server tab or database GUI dies, and `repose open` holds a terminal tab per port. Fix: a laptop forward daemon (`repose open -d`, `[forward] ports` in config.toml) as a login item. Size S, on the existing reconnect loop. (2) Laptop ports move when busy, so an OAuth callback registered for 3000 breaks on 3001. Pinned ports that fail loudly on conflict fix this inside the same daemon. (3) A held idle forward keeps the machine unfrozen, which costs full CPU overnight. Count only moving bytes as use, as I-930 did for CDP, and drop a forward after 30 idle minutes. (4) Traffic flows one way. Reverse forwards (laptop to machine, size S) let the agent reach the laptop's Android emulator through adb on 5037, plus Ollama or a local database, at zero host cost. (5) Your phone gets the --done ping and cannot open the result. Owner-only preview links behind the dashboard cookie, with a QR code and no public flag, cost M. They keep the abuse vector that stopped I-6 shut, and their egress is a few hundred MB a month counted against the plan. Small extras: print `localhost:3000 -> your laptop` when a forward opens, so the demo shows the payoff, and route http://localhost links the machine opens through the forward. Order: daemon with pinned ports, reverse forwards, owner-only phone links, then a signed webhook endpoint as the only public path worth adding. Leave anonymous public previews to cloudflared, or to `config add tailscale` for users who already run a tailnet.

**Cores.** Two separate problems sit under this request. Bursty CPU (builds, test suites, five agents running tsc) is common and cheap to solve. The Android emulator needs KVM; it is rare and expensive to solve. Nested KVM in today's guests is a no on Azure. The guest is already L2 (Hyper-V L0, NixOS host L1, Cloud Hypervisor L2), an emulator would be L3, which Microsoft does not support, and nothing sets kvm_intel nested=1 or CH `nested=on`. Even a working L3 would break host-restart-freeze, because CH cannot save a guest that runs its own VMs. Two commands settle the docs: `cat /sys/module/kvm_intel/parameters/nested` on host-02 and `ls /dev/kvm` in a guest. Without KVM an x86 emulator falls back to software and runs 5-10x slower. An always-on shared power host is the wrong shape for this owner: a D16s_v7 costs $770 a month idle and an AX162 $992 before one mobile user pays. A pooled free-for-all fails too, because one tenant at full tilt already took every neighbour's p90 keystroke latency from 4.3 to 44.5 ms. Sell cores as revocable time-boxed leases and keep memory as the only guarantee. Options, cheapest first. (a) Laptop emulator bridge through reverse forwards: $0 host cost, size S, build next. It covers most mobile developers, since Apple Silicon runs the ARM emulator fast, and it is the only option that helps iOS. (b) Core boost lease on existing hosts: hostd boots guests with CH boot=N,max=M and hotplugs 2 vCPUs (4 on xl) for a time box. Lease vCPUs run at lower cgroup weight than base vCPUs and tmux, are granted only while host busy vCPUs stay under 75 percent of physical cores, and are capped at about 8 per host on host-02. Placement and the 1.5x rule count base vCPUs only. host-02 commits 48 vCPUs on 32 cores while 24 large guests average about 1.1 busy cores, so leases sell slack the owner already pays for. List-price cost per core-hour is $0.13 on Azure and $0.03 on Hetzner, and the marginal cost is about zero. Include 20/50/120 core-hours on Solo/Plus/Pro and sell more later as an add-on. Size M, build next; it serves builds and swarms but not emulators on Azure. (c) A one-day Redroid test: Android in a container needs binder in the guest kernel repose already builds and no KVM, so it may run UI tests on today's hosts on an xl or a lease. (d) A per-job Azure spot VM for real emulators (`repose device android`). The emulator is the normal L2 case on a single-tenant Dsv5 spot VM, reached with adb over the VNet. It costs about $0.05-0.10 an hour, plus about $0.09 an hour of egress when the screen streams, paid from credits, and nothing when unused. Price it near $1 a device-hour, under Genymotion's $3.60. Size L, run as an experiment once a mobile user asks. (e) A bare-metal KVM pool on Hetzner, where guests are L1. An EX44 pilot costs $50-60 a month for about 3 sessions. An AX162-2 holds about 11 sessions for $992 and breaks even at about 34 subscribers on a $29 add-on. Build it later, with the Hetzner move and BYO host shape A (I-647), not before. (f) Cloud device farms through an MCP tutorial, paid by the user, for CI-style tests. Correction to my round 1 numbers: a 2-core boost-hour on an AX162-2 costs $0.057 at list price, not $0.04.

**What changed from round 1.** Burst cores moved from experiment to build-next. The daily-driver and innovation cards showed that a lease limited by the existing I-738 busy-vCPU rule needs no new hardware. I also corrected my 2-core boost-hour cost on Hetzner to $0.057. For the emulator I added a per-job Azure spot VM, from the innovation card, ahead of the EX44 pilot, because it holds no idle cost and runs on credits. I added Redroid as a new no-KVM experiment. Per-turn rewind moved to build-next because its git-ref version is S. plans-billing now leads with a $22 small entry plan: half a seat loses about $44 a user on Azure against about $83 for a Solo, so the growth card's reach case also holds on cash. Dashboard moved from keep to trim. agent-setup-carry pull went 7 to 8, worktree-agents retention 5 to 7, repose-ask-reply retention 8 to 9, agent-wrappers-hooks retention 8 to 9, and status-ls-logs-events now backs `ls --watch`. frozen-pause retention went from 6 to 4 because users never see it. It stays lean-in on cost. install-login-cli moved to fix. Where I still disagree: claude-login-share stays fix, because host-02 makes a second login likely now. browser-bridge stays keep (the daily-driver card trims it) because it has no host cost. machine-nix stays trim because of its fleet-wide rebuilds.

**Verdicts and the one change each member asked for.**

| Feature | Verdict | Change | Why |
|---|---|---|---|
| run-attach | lean-in | Reattach on laptop wake with no 2-minute cutoff, and print one line per agent on attach saying what it did while you were away (commits, turns ended, questions asked). | Every other feature hangs off this path. The 8 GB and 2 vCPUs a large holds is the price of the product, and freeze already gets back the CPU half. |
| tmux-session | keep | Ship a tested terminal matrix (iTerm2, Ghostty, WezTerm, Terminal.app, VS Code) for Shift+Enter, mouse and truecolor, and fix the defaults that fail. | It costs no host resources and runs in every session. Its one running cost is the terminal-quirk ticket, and a test matrix ends that. |
| herdr | trim | Drop herdr from the run/attach/ps/paste test matrix as best-effort, and fix the held laptop SSH session that blocks freeze, or remove the path. | All four cards agree. A herdr user's machine never freezes, so it pays full CPU around the clock for a niche audience, and it doubles the core path's test surface. |
| five-agents | keep | Make Claude Code and Codex first class with release-gating tests; mark opencode, Gemini and pi as installed-not-tested, and put agents idle at a prompt in a low-weight cgroup. | Preinstall is cheap and widens the funnel, but each agent is weekly upkeep and idle burn (claude at prompt: 12.2 CPU-h in 14 days). The daily-driver and innovation cards' tiering is the right way to cap that upkeep. |
| agent-wrappers-hooks | keep | Gate each base update on a canary guest that starts, trusts and resumes Claude Code and Codex, so an upstream flag change fails the release instead of reaching users. | Resume is what makes stop safe to press, and every stop gives memory back to the pool. I raised retention after the other cards; the verdict stays keep because five upstream CLIs make this the highest-upkeep code per line. |
| agent-states | keep | Add needs-input for Codex through its notify hook, so the two agents most users run both report it and the 90 s heuristic covers only the minor three. | It feeds the freeze rule and the unused notice, so it pays for itself in reclaimed CPU. Half coverage weakens both the glance and the freeze. |
| agent-setup-carry | keep | Before `run` starts, name the MCP servers that lack a secret or need OAuth and offer `secrets import --mcp` once; build `repose mcp login NAME` next, since OAuth servers are the largest ticket source. | I raised pull: three cards found the proof frame stronger than I credited. I keep it out of lean-in because the merge code is large and MCP gaps are recurring support load. |
| claude-login-share | fix | Add account-to-host affinity in placement so all of a user's machines land where their login share already is; notify before a forced move. | The others score it keep, but host-02 went live on 2026-10-27, so a second /login is now a churn trigger that will fire. Affinity is a placement rule and adds no secret home. |
| guest-agent-guide | keep | Add a five-task eval per base update (port, secret, notify, install, fetch), and one line telling agents to size parallel builds to `nproc` on the class. | Plain text that stops apt runs, tunnels and core spikes. The cost to run it is zero. |
| worktree-agents | keep | Share the pnpm store and cargo cache across worktrees, and warn when a new worktree agent starts while 'waiting for a core' is above 20 percent. | I raised retention: the daily-driver card shows two to five worktree agents is the normal day. It is the main source of core demand, and each worktree reinstalls dependencies onto plan disk. |
| multi-checkout | keep | Offer `--on` when a start is refused for plan memory: adding the repo to a running machine turns the refusal into a fit. | It gives more work per GB of plan memory with no host cost. Few users will find it without a prompt at the right moment. |
| conductor-tutorial | keep | State in the tutorial that a swarm needs an xl or a boost lease, and give the core count where it saturates. | Docs only, so the upkeep is near zero, and the swarm content is good for reach. Without a size note it sells a workflow the default large cannot carry. |
| ps-exec | keep | `repose ps PROJECT WINDOW -f` to follow one window from the laptop, documented as waking a frozen machine. | SSH round trips only, and the daily-driver card shows a daily peek habit I had underrated. |
| sync-at-launch | lean-in | Print a one-line receipt of what went (branch, uncommitted files, .env count, skipped over-cap files by name) on every first run, as the video's hero frame. | It is the one unique thing, and ingress is free because repose stores no code. Its cost is edge-case code, and the receipt also turns 'my file is missing' tickets into self-service. |
| repose-sync | fix | On exit 6, list the overlapping paths and offer keep-machine, keep-laptop or stash in one keystroke. | It is the most likely git-conflict ticket source. Continuous sync stays deferred because watch mode would cost more upkeep than it returns. |
| git-fetch-repose | keep | Print unfetched branch counts on `attach` and `stop`, and put the exact `git fetch repose && git switch BRANCH` line in each --done notification. | Work comes back through the gateway with no counted egress and no GitHub round trip. It is daily use for near-zero cost. |
| ssh-editors | keep | Let an editor whose remote server has had no traffic for 30 minutes stop blocking freeze; an editor left open overnight pays full CPU today. | Table stakes against Codespaces. The hidden cost is the editor holding the machine unfrozen, and that cost comes back every night. |
| cp-paste | keep | Freeze the input-proxy scope and add a paste-detection regression test per terminal (iTerm2, Ghostty, VS Code). | Uploads are free ingress, and the others are right that this is a feature people demo. The proxy is intricate and generates terminal-specific tickets, so I would spend money on tests before features. |
| links-open-on-laptop | keep | Extend it to http://localhost links so they open on the laptop through the existing forward. | Invisible until /login stalls without it. Free to run. |
| timezone-carry | keep | None. | Correct timer and log times at no cost. Nothing to build. |
| auto-port-forward | lean-in | Keep pinned ports up after detach with a laptop forward daemon that drops a forward after 30 idle minutes, so freeze still works. | The owner calls it amazing, and fleet egress was 28 GB in 14 days, about $2.40 of Azure egress. It is the cheapest high-delight feature repose has. |
| repose-open | fix | Fold it into the forward daemon: `repose open -d PORT` survives the terminal and is listed and closed from one place. | Holding a foreground terminal tab per port is a chore. The reconnect loop exists, so the background mode is small CLI work. |
| preview-urls | fix | Build only the owner-only half (dashboard cookie, QR code, no public flag) and count its bytes against plan egress; leave public sharing to cloudflared. | Phone checks after a --done ping are the real gap, and owner-only closes the hosting-abuse vector that kept I-6 unapproved. Public mode would add abuse handling and egress the owner cannot afford. |
| shared-browser-mcp | fix | Stop Chromium after 10 minutes with no CDP traffic, kill orphan chrome-headless processes agents spawn, and verify I-930 lets a machine freeze with it open. | Agents checking their own UI is daily value. As shipped it is 13.6 idle CPU-h per 14 days and up to 6 GB of plan memory, the second-largest sink on the fleet. |
| browser-viewer | keep | Ship the 2x Retina desktop, and link the viewer from a repose-ask when an agent hits a captcha. | It unblocks the one step agents cannot do, and Xvnc sleeps after 30 minutes. I raised pull because the growth card makes a fair case for its demo value. |
| browser-bridge | keep | Freeze features and commission one external review of bridgewarden; a hole there exposes the user's real Chrome sessions. | It runs on the laptop, so host cost is zero, which is why I keep it where the daily-driver card trims it. Its cost is security risk, which needs a review and no new scope. |
| mcp-forward | keep | Fix docs/features/README.md, which still lists it as not built. | Laptop-side and cheap, niche by design. Its pattern also carries the adb reverse forward below. |
| notifications-channels | keep | Add web push from the dashboard as a PWA before Telegram or Discord; it reaches phones with no third-party account and costs nothing to send. | Notifications make closing the lid tolerable, and Resend plus webhooks cost pennies. Today most users skip the ntfy setup and end up with email only. |
| repose-notify | keep | Add a Stop button and the fetch line to each --done message, so the user can stop the machine from the same tap. | A done ping brings users back for a reason. With a Stop button it also gives memory back, so it is a cost lever too. |
| repose-ask-reply | lean-in | Center a phone page on it: open questions, the window's last 30 lines, answer buttons, web push. | A blocked agent idles while it holds a full seat, so a 20-second phone answer turns paid-for idle memory into work. Low infra cost, and the other cards rightly scored its retention higher than I did. |
| secrets-ask | keep | Offer 'store as global' on the answer page so the next project does not ask again. | Keeps values out of transcripts at near-zero cost. It is used a few times per project. |
| named-secrets | keep | Show in `secrets list` which long-running processes started before a value changed, which ends 'my dev server has the old key' tickets. | Table stakes, done carefully. Key Vault costs pennies a month. |
| tool-login-copy | fix | Mint a repo-scoped, short-lived GitHub token per project through a repose GitHub App, with the App key stored as an existing named-secret ciphertext so no fourth secret home appears. | All four cards agree. One leaked full-scope token on launch week would cost more reputation than the feature earns. |
| laptop-tools-carry | keep | Cache the resolved install set per account, so the second machine installs from the host store in seconds instead of repeating minutes of guest CPU. | Ingress is free and the frame sells. The cost is repeated per-machine CPU and the nixpkgs mapping upkeep. |
| preinstalled-base | keep | Track top command-not-found names from guests and add the top five each release; publish the base list generated from the closure. | The shared read-only store keeps it cheap per guest. Users would leave if it were missing. |
| config-menu | keep | Build the central binary cache (R4-4) so `config add postgresql` substitutes in seconds instead of a host build that takes up to 8 cores from guests. | Services in one line beat compose for most users. Host builds compete with guests, and every user on the host feels that as typing lag. |
| machine-nix | trim | Debounce saves and apply them lazily at each machine's next start or attach, never as a fleet-wide rebuild burst. | Few users write Nix, and each save rebuilds every machine of the account on host CPU. The others keep it; I trim its rebuild behaviour, not the file. |
| base-updates | keep | Build the base once and push closures to hosts, so a release stops rebuilding per project per host. | Required to keep five agents current. It costs owner time each week and host build CPU per project. |
| scheduled-jobs | keep | Ship a guest helper that writes the two unit files from one line, with an agent prompt as the common case (see scheduled agent prompts). | Plain systemd, nearly free; a due timer blocking freeze is a fair trade. The growth and innovation cards are right that a scheduled prompt is the shape people would use. |
| auto-snapshots | keep | Upload incrementally and in the background after the machine reports stopped, so stop returns in seconds and Blob bytes fall. | The owner calls them fantastic, and they prevent the loss nobody can undo at a few GB of Blob per project. Stop time growing with disk is the cost users feel. |
| manual-snapshot-restore | gimmick | Freeze the code, drop the dashboard tab and manual create, and keep `snapshots restore` as a CLI support path; spend the effort on per-turn rewind. | Unanimous across cards and the owner's own use. The ranged-GET restore code is a large test surface for a path nobody takes. |
| destroy-restore | keep | None. | It prevents an irreversible loss for 30 days of one Blob snapshot. That is cheap insurance. |
| fork | gimmick | Stop extending it and make copies --temp by default; point parallel attempts at --worktree. | Each copy multiplies plan memory, so users hit the wall right after the demo, and the owner does not use it. Worktrees give the same result on one machine's memory. |
| second-machine-home | keep | Default `repose run scratch` to --temp unless named, so scratch machines stop piling up on disk and plan memory. | Useful for trying a tool. Each scratch machine is a full project that holds disk until someone deletes it. |
| temp-machines | keep | Make --temp the default for fork copies, home-folder machines and a trial account's first run. | It reclaims memory and disk with no user action and no snapshot, which makes it the cheapest lifecycle feature repose has. |
| keep | keep | None. | Temp machines need this companion to be safe to use. It costs nothing. |
| stop-start | keep | When a start is refused for plan memory, name the unused machine and stop it on one keystroke. | A stopped machine returns its memory at once. Each stop the product makes easy adds directly to seat supply. |
| frozen-pause | lean-in | Lend the paused cores to active guests as revocable boost leases (see future), and get the three I-828 rulings for the memory half. | I lowered retention because users never see it. It stays lean-in on cost: 29-48 percent of running CPU comes back, and that idle time is the stock any burst offer sells from. |
| unused-notice | keep | Put a one-tap Stop in the notice; a forgotten machine recovers about 92 percent of its hours if stopped at first notice. | It saves money only when the user acts, so make acting take one tap. All four cards reached the same fix. |
| host-restart-freeze | keep | Run a monthly save-and-restore drill on host-01 that pages on failure; this path runs rarely and rots quietly. | Done work that delivers the 'keeps working' promise and good recognition content. It is the riskiest code per line, with the only secrets exception. |
| resize | keep | Show 'waiting for a core' beside the resize choice, and offer a boost lease where the problem is contention, not memory. | Commodity, and the upsell toward xl. A stop-change-start is too heavy as the answer to a slow build. |
| usage-charts | keep | Put 'waiting for a core' as one number in `repose status` and use it to trigger the boost-lease offer. | Billing already collects the samples. The steal number is the honest answer when a neighbour slows a user down. |
| status-ls-logs-events | keep | Build `repose ls --watch` as a compact live board of agents across machines, polling state the api already stores. | These are cheap reads that users run hourly. Two other cards showed the live board would earn a side pane, which changes my 'only if asked'. |
| dashboard | trim | Stop full revamps; keep Projects, Questions, Billing and Settings working and put design time into the phone view of Questions and project state. | I moved from keep to trim: I already named design churn (I-837, I-841) the largest non-infra upkeep line, and two cards trim it. The phone view is the part a lid-closed user meets. |
| plans-billing | fix | Add a small 4 GB entry plan at about $22; on Azure it loses about $44 a user against about $83 for a Solo, and on Hetzner its half seat ($16.50) clears. Then move seats to Hetzner AX162-2 before credits fall under three months of burn. | The other cards showed me a cheaper small entry adds reach, and running the numbers changed my view: half a seat loses less cash per user than a full Solo on Azure. Polar's flat $0.50 makes anything under $15 a poor deal. |
| waitlist-seats | fix | Fix the GET /billing waitlist-join bug before any launch post, and email a one-click start when a seat frees. | Seats cap cash loss at 28, which a low-cash owner needs. The bug corrupts the one demand signal repose has, and on launch day it would drop the visitors the post brings. |
| limits-abuse | keep | Alert when one tenant's busy vCPUs exceed their committed share for 10 minutes, the case that pushed neighbours' p90 typing to 44.5 ms; tighten it for any card-less tier. | One miner or runaway tenant costs more than all features earn. This guard also has to exist before any burst offer ships. |
| install-login-cli | fix | Ship `repose update` and a version-skew warning on attach. | Stale laptop CLIs are a slow, steady ticket source the guest guide already has to warn about. I moved to fix because the update command costs little and that ticket stream is ongoing. |


### Innovation

**Thesis.** Repose is worth paying for because your laptop's exact working state (uncommitted files, .env, gh and Codex logins, Claude skills and MCP servers, your tools) lands on a persistent VM in about 14 seconds, and the agents keep working with the lid closed. The phone side is now partly commodity: Claude Code Remote Control and Cursor's iOS app both drive agents from a phone, so repose should host those apps' sessions and keep repose-ask as the agent-neutral channel. The carry and localhost are the parts repose owns: forwards that outlive the terminal and run both ways, plus per-turn rewind in place of the snapshot picker. Burst cores sold as a revocable lease make swarms and builds fast at near-zero cost, while Android goes through a laptop adb bridge before anyone rents iron. Price and the full-scope gh token are the exposed flanks for a launch, with exe.dev at $20.

**Lean in (votes).**
- sync-at-launch, voted with run-attach and agent-setup-carry as one launch receipt: your dirty checkout, .env, logins, skills and MCP servers on a real VM in about 14 s
- auto-port-forward, extended with forwards that outlive the terminal, pinned ports and reverse forwards
- repose-ask-reply, kept as the agent-neutral phone channel and paired with Remote Control registration instead of a repose phone console

**Cut (votes).**
- manual-snapshot-restore: drop the dashboard tab and manual create, keep one CLI recovery path, build per-turn rewind
- herdr: freeze as best-effort; it doubles the attach surface and blocks the freeze
- fork: stop extending; worktrees cover parallel attempts inside one plan's memory

**Ports.** Forwarding while attached is enough while you sit at the terminal. It falls short in four places. It ends at detach, which is when you close tmux and open the browser. Laptop ports move when busy (3000 becomes 3001), which breaks OAuth callbacks registered for 3000. It runs one way only, so an agent cannot reach your laptop's emulator, Ollama or database. And nothing reaches a phone, so a `--done` saying 'look at localhost:3000' is a dead link on the bus, while exe.dev gives every VM a private-by-default HTTPS URL. Next, in order: (1) S, a laptop forward daemon with pinned ports per project in config.toml that fails loudly on conflict, survives detach and sleep, absorbs `repose open -d`, and drops a forward after 30 minutes with no bytes so the freeze still works. (2) S, reverse forwards (`[forward] reverse = [5037, 11434]`) over the attach session; adb to the laptop's Android emulator is the headline, and it answers most of the cores question at zero host cost. (3) S, http://localhost links the machine opens go to the laptop through the forward, and `repose status` lists forwards with process names. (4) M, owner-only HTTPS links with a QR code behind the dashboard login, used by notify links and the browser viewer, with no anonymous mode. (5) Later, a signed webhook endpoint per project as the one public path. Leave public previews to cloudflared.

**Cores.** Split it in two. Bursty CPU (builds, test suites, five agents running tsc) is the common case and costs almost nothing to serve. The Android emulator is rare, needs KVM, and does not fit inside today's guest. For bursts, build a revocable lease on existing hosts: start with `repose resize --size xl --for 2h`, then hot-add vCPUs with Cloud Hypervisor boot=N,max=M (hostd passes only boot=N today, ch.go:123). Burst vCPUs run at low cgroup weight and are granted only while host busy vCPUs stay below physical cores, so the measured 17-45 ms typing lag does not return. Meter them from a monthly core-hour allowance per plan, show the allowance beside 'waiting for a core', and refuse when the slice is full. The fleet is 33 percent busy and the freeze returns 29-48 percent of running CPU, so a lease sells idle time at about $0.04 per 2-core hour at Hetzner list price. Build cost M, verdict build-next. The owner's 'communism' as a pooled free-for-all is the wrong shape, because the heaviest user then sets everyone's latency; a lease with a ceiling keeps memory as the guarantee and cores as the bonus. A standing shared power host also fails the cash test: about $770 a month on Azure or $992 on Hetzner, idle most hours. For Android, use three steps. First, S and free: reverse forwards so the agent drives the emulator on the laptop over adb, which suits Apple Silicon users. Second, experiment: a per-job Azure spot VM where the emulator runs at L2, joined with `adb connect` and shown in the browser viewer, at about $0.05-0.15 an hour with nothing idle. Third, build later on demand: a bare-metal KVM pool through BYO host shape A, piloted on a $50-60 a month Hetzner box. Spend one hour first checking /dev/kvm in a guest with nested=on on host-02, so the docs stop guessing. In-guest KVM on Azure is L3, unsupported, and breaks host-restart-freeze.

**What changed from round 1.** repose-ask-reply novelty 8 to 6 and phone console PWA from build-next to build-later: searches show Claude Code Remote Control (sessions started from the phone since Aug 2026) and Cursor's iOS app with push already own the phone surface. I added a new 'Vendor remote-control host' future feature to replace it. dashboard keep to trim for the same reason. claude-login-share keep to fix (economist: host-02 makes re-login live). shared-browser-mcp keep to fix and install-login-cli keep to fix, matching the daily-driver and economist cards. frozen-pause cost_efficiency 9 to 10, verdict still keep because users never see it. git-fetch-repose retention 8 to 9 and status-ls retention 7 to 8 after the daily-driver card. auto-port-forward novelty 7 to 6 (editor auto-forward exists). browser-bridge novelty 9 to 8 (Claude in Chrome covers the local case). Reverse forwards and the laptop emulator bridge come in as build-next from the economist card; I had missed them. Repo-scoped GitHub tokens build-later to build-next. Per-turn rewind cost M to S (git-ref version). Burst cores now take the economist's revocable-lease framing and the daily-driver's resize --for as the first step. Unchanged against the growth card: no standing burst host. plans-billing stays fix, now with exe.dev's $20 confirmed.

**Verdicts and the one change each member asked for.**

| Feature | Verdict | Change | Why |
|---|---|---|---|
| run-attach | lean-in | On attach, print one line per agent saying what it did while you were away (commits, turns ended, questions answered), pulled from events; this is the daily-driver card's idea and it shows the lid-close payoff each morning. | One command from a dirty checkout to an attached agent beats Codespaces and Coder setup. The VM and the attach are commodity (exe.dev, Sprites, Cursor's `agent worker start`), so pull comes from the carry around them. |
| tmux-session | keep | Ship a tested terminal matrix (iTerm2, Ghostty, WezTerm, VS Code) for Shift+Enter, mouse and truecolor, and send the `?` marker to the laptop terminal title. | Table stakes; a Hetzner box with tmux has it. The `?` marker is the one part rivals lack, and the terminal quirks cost real week-one time. |
| herdr | trim | Freeze it as best-effort, drop it from the run/attach/ps/paste test matrix, and fix the laptop session that blocks the freeze or drop the path. | A second multiplexer doubles the attach surface for a niche audience. Its held SSH session defeats the CPU pause, which every card flagged. |
| five-agents | keep | Make Claude Code and Codex first class with a resume test on every base bump, label opencode, Gemini and pi installed-not-tested, and add Cursor's CLI agent since Cursor now runs workers on any machine. | Agent-neutral is the wedge against Claude Code on the web, Codex cloud and Cursor cloud agents, which each tie you to one vendor. Five upstream CLIs per weekly base is the tax. |
| agent-wrappers-hooks | lean-in | Resume must survive a base update and a resize too, with a 'resumed 3 agents' receipt on attach, gated by a per-agent smoke test in the base pipeline. | Agents coming back in their conversation, in the right dev shell, make stop safe to press; Sprites and exe.dev hand you a bare shell. Users notice it only when it fails. |
| agent-states | keep | Add needs-input for Codex through its notify hook so both lead agents report it, and mark hookless states as estimated. | Cross-machine state is what Conductor-style apps and Cursor's agents page sell, so novelty is lower than I gave it. It also feeds the freeze, so it pays for itself. |
| agent-setup-carry | lean-in | Ship `repose mcp login NAME` so OAuth MCP servers sign in through the laptop browser, and print the carry as a launch receipt (skills 14, MCP 6 of 7, 1 needs SECRET_X). | Your skills, hooks and MCP servers working on minute one is the line Claude Code on the web and Codex cloud do not cross. OAuth servers are the main reason a carried setup arrives half broken. |
| claude-login-share | fix | Add account-to-host affinity in placement so every machine of a user lands where their login share lives, and notify before a forced move. | The economist card is right that host-02 makes a second /login a live churn risk now, and repeated logins drove an HN user off Sprites. Affinity is a placement rule with no new secret home. |
| guest-agent-guide | keep | Add a five-task eval per agent (port, secret, notify, install, fetch) on every base update, and publish the guide as a post, as the growth card suggests. | A CLAUDE.md written for a machine is an idea worth writing about and it cuts support. Near-zero run cost. |
| worktree-agents | keep | Add `repose worktrees` (branch, ahead count, agent state) and a shared pnpm store and cargo target so a new worktree does not reinstall from scratch. | Parallel worktree agents are commodity (Conductor, Cursor, Codex). Two shared cores are the real limit, which makes burst cores the fix. |
| multi-checkout | keep | Offer `--on` when a start is refused for plan memory, and group `ls`/`ps` by checkout. | More work per plan gigabyte for api-plus-web setups. Few find it without a prompt at the right moment. |
| conductor-tutorial | keep | State that a swarm wants xl, and film one 90-second run of five workers whose branches come back through git fetch. | Docs-only and cheap content. On two cores it mostly demonstrates the core limit, so keep it honest about size. |
| ps-exec | keep | Add `repose ps PROJECT WINDOW -f` to follow one window from the laptop without attaching. | Peeking without attaching is a quiet daily habit for power users. Cheap SSH reads. |
| sync-at-launch | lean-in | Make the landing hero this one fact and print a receipt of what went (branch, N uncommitted files, M .env files, skipped files by name). | Research and all four cards name it the only unique thing. Claude Code Remote Control and Cursor workers still need you to own the always-on box and get your state onto it; repose does that in one command. |
| repose-sync | fix | On exit 6, list the overlapping files with a one-key choice (keep machine, keep laptop, stash), then build `repose sync --pull` for the machine's uncommitted changes. | Half the round trip, and the conflict path is the support stream. Other cards rank the conflict UX first and I now agree it ships before reverse sync. |
| git-fetch-repose | keep | Print unfetched branch counts on attach, ls and stop, and put the exact fetch-and-switch line in each `--done` notification. | Getting work back without GitHub is elegant and free; I moved retention up after the daily-driver card. It depends on agents committing, so the guide must keep nudging that. |
| ssh-editors | keep | Support mosh or an equivalent roaming transport, and let an idle VS Code server stop blocking the freeze. | Parity with Codespaces, Coder and exe.dev. East US only without mosh hurts every non-US user. |
| cp-paste | keep | Show a short inline toast with the remote path for each pasted file, and add a regression test per terminal for paste detection. | Pasting a screenshot into a remote Claude Code is the delight no SSH-box rival matches. The input proxy is fragile, so freeze its scope. |
| links-open-on-laptop | keep | Extend to http://localhost links so a dev server's printed URL opens on the laptop through the forward. | Claude /login and gh auth just work, which you notice only when a rival fails. Free. |
| timezone-carry | keep | None; leave it. | Fit and finish with no cost. Nobody signs up for it. |
| auto-port-forward | lean-in | Keep pinned ports up after detach with a laptop forward daemon, and fail loudly on a busy port instead of moving to 3001, so OAuth callbacks hold. | The best moment in the product, cookie-correct localhost in a plain terminal with docker -p. VS Code and Codespaces auto-forward inside their editor, so I trimmed novelty one point; detach ending it is the main gap. |
| repose-open | fix | Fold it into the forward daemon: `repose open -d PORT`, listed and closed from one place. | A foreground forward needs one terminal tab per port. Background mode makes it the second half of port forwarding. |
| preview-urls | fix | Build only the owner-only half: a login-gated HTTPS link with a QR code, used by notify links. No anonymous mode. | exe.dev gives every VM an HTTPS URL with private-by-default sharing, so the gap shows in every comparison table. Public mode turns repose into a host for abuse. |
| shared-browser-mcp | fix | Stop Chromium after 5-10 minutes with no CDP traffic and kill orphan chrome-headless processes agents spawn; together they are 13.6 idle CPU hours in 14 days. | An agent that checks its own UI in a logged-in browser closes the frontend loop. As shipped it is the second-largest idle sink, so I moved to fix with the daily-driver and economist cards. |
| browser-viewer | keep | Ship the 2x Retina desktop first, then reach it through the owner-only phone link so a night-time captcha can be solved from bed. | Taking over the agent's browser unblocks logins and demos well. At 1x on a MacBook it looks broken in five seconds. |
| browser-bridge | keep | Freeze scope, get one external review of bridgewarden and publish the security model as a post. | A remote agent driving your Chrome with withheld cookies and an allowlist is rare, though Claude in Chrome now covers the local case, so I lowered novelty. It needs the laptop open, against the lid-closed pitch. |
| mcp-forward | keep | Fix docs/features/README.md and reuse its plumbing for reverse port forwards. | Clever and cheap, but laptop-bound. Its pattern is worth more as the base of reverse forwards than as a feature. |
| notifications-channels | keep | Add web push from the dashboard so a phone gets answer buttons with no ntfy setup. | Commodity channels; Cursor's iOS app pushes within seconds. ntfy setup is friction most users skip. |
| repose-notify | keep | Attach the branch, diff stat and a shared-browser screenshot to `--done`. | Done pings make the lid-closed promise felt. Any agent or script can call it, which vendor apps do not offer. |
| repose-ask-reply | lean-in | Keep it as the agent-neutral, script-callable channel with buttons, and register each machine with Claude Code Remote Control so Claude users also get the vendor app; do not build a phone console. | I dropped novelty from 8: Claude Code Remote Control (start sessions from the phone since August 2026) and Cursor's iOS app with push now cover the Claude and Cursor cases. A structured yes/no that any agent or script can raise, plus secrets, stays unique and drives daily return. |
| secrets-ask | keep | Offer 'store as global' on the answer page so the next project does not ask again. | A key pasted from the phone that never enters the transcript is a clean privacy story no cloud agent tells. Near-free. |
| named-secrets | keep | Show which running processes started before a value changed, so 'my dev server has the old key' stops being a ticket. | Table stakes done carefully. Nobody chooses a platform for it. |
| tool-login-copy | fix | Mint a repo-scoped, short-lived token per project through a repose GitHub App in place of the full-scope gh token, before the launch post. | 'Logins copied' is the strongest video frame. A full-scope token beside an unattended agent with open egress is the first HN security comment; all four cards agree. |
| laptop-tools-carry | keep | Cache the resolved tool set per account so the second machine installs from the host store in seconds. | 'Installing 12 of your tools' is novel where Codespaces makes you write a devcontainer. Resolution logic is the upkeep. |
| preinstalled-base | keep | Generate a base contents page from the nix closure each release, and add the top command-not-found hits. | Commodity; the shared read-only store keeps it cheap. Ubuntu binaries running on NixOS removes the Nix tax. |
| config-menu | keep | Add a central binary cache for menu items so `config add postgresql` takes seconds, and let agents run it themselves. | Live NixOS rebuilds with rollback beat devcontainers. Host builds compete with guests for CPU, which every user feels as lag. |
| machine-nix | keep | Apply a save lazily at each machine's next start or attach, never as a fleet-wide rebuild. | A power-user nicety Coder and Codespaces also have. The rebuild-all behaviour is the cost to fix; I keep it over the economist's trim because the fix is small. |
| base-updates | keep | Gate each base update on a canary run of Claude Code and Codex resume, and build once then push closures. | Fresh agents weekly matter in this market. It costs owner ops time each release. |
| scheduled-jobs | keep | Add `repose schedule "weekdays 07:00" -p "triage issues"` that writes the timer and starts an agent. | Codex and Cursor sell automations; here the same thing hides behind systemd unit writing. A helper turns plumbing into a story. |
| auto-snapshots | keep | Take the snapshot in the background after the machine reports stopped, so stop returns in seconds, and print its age in one line. | The owner calls it fantastic and it is the right default. Sprites checkpoints in 300 ms, so it is insurance, not a headline. |
| manual-snapshot-restore | gimmick | Drop the dashboard tab and manual create, keep one CLI restore as a support path, and spend the effort on per-turn rewind. | The owner never uses it, and a stop plus 30-90 s whole-disk rollback trails Sprites' sub-second restore. Users want to undo the last agent turn. |
| destroy-restore | keep | None; it prevents a loss the user cannot undo. | Used once a quarter, and that once saves a week. Small Blob cost. |
| fork | gimmick | Stop extending it and point parallel attempts at --worktree; make fork copies --temp by default. | Three copies demo well and then hit the plan memory wall. Worktrees cover the same case inside one machine. |
| second-machine-home | keep | Default `repose run scratch` to --temp unless named or kept. | A scratch machine is how people try tools, and how exe.dev hooks users. Cheap. |
| temp-machines | keep | Make --temp the default for a trial account's first run, for fork copies and for home-folder machines. | Self-cleaning machines reclaim memory and disk with no effort. The unfetched-work guard is careful. |
| keep | keep | None. | The escape hatch that makes temp safe. Free. |
| stop-start | keep | Put a Stop button in the unused notice and in `--done`, fetching commits first. | Starts in 9-12 s with commit fetch are solid. Commodity against Sprites and exe.dev, which wake on request. |
| frozen-pause | keep | Spend the reclaimed CPU on burst leases for active guests; cores nobody can borrow are half the win. | I raised cost efficiency to 10 with the economist: 29-48 percent of running CPU comes back. I keep keep over lean-in because users never see it and the memory half, the larger lever, needs owner rulings. |
| unused-notice | keep | One-tap Stop in the notification itself. | Recovers about 92 percent of a forgotten machine's hours only if acting is one tap. |
| host-restart-freeze | keep | Publish a post with a live agent surviving a host kernel update, and log one event line to the user afterwards; add a monthly restore drill. | Few rivals claim agents carry on through host maintenance, and it is recognition content for a cloud team. Complex code and a secrets exception for a rare event. |
| resize | keep | Allow `repose resize --size xl --for 2h` that falls back on its own, the cheapest first form of burst. | Commodity. The time-boxed form from the daily-driver card links it to the cores question. |
| usage-charts | keep | Show 'waiting for a core' as one number in `repose status` and offer a burst lease when it stays high. | Honest contention charts are unusual and build trust. Few users open the tab. |
| status-ls-logs-events | keep | Ship `repose ls --watch` as a live board of agents across machines with a key to attach. | `repose ls` is the hourly glance for a five-agent user; I moved retention up after the daily-driver card. Cheap reads. |
| dashboard | trim | Stop revamps; keep Projects, Questions, Billing and Settings working and make the Questions page work on a phone. | I moved from keep: Claude's app and Cursor's iOS app now own the phone control surface, so a repose phone console is a race repose loses. The dashboard's job shrinks to billing and answers. |
| plans-billing | fix | Add a small 4 GB entry near $20, the number exe.dev charges for its pool; move seats to Hetzner as credits run down. | exe.dev sells $20 a month for 50 VMs on pooled resources, confirmed this week, so $49 is the first objection in any thread. Every Azure seat loses $54-90 anyway. |
| waitlist-seats | fix | Fix the GET /billing waitlist-join bug before any launch post and send a one-click 'your seat is ready' email. | Seats cap cash burn, which the owner needs. A broken waitlist wastes the launch spike and corrupts the one demand signal. |
| limits-abuse | keep | Name the limit in each error, and alert when one tenant's busy vCPUs exceed their share for 10 minutes. | Required to survive a public launch on Azure egress pricing. Silent until it fires. |
| install-login-cli | fix | Ship `repose update` and a version-skew warning on attach; time install-to-first-agent for a stranger. | Moved to fix with two other cards: a stale laptop CLI against a weekly server is a support source the guest guide already has to warn about. |

