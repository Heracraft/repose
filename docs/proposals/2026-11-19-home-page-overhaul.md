# The home page sells the machine again, and the Mac mini joins it (proposal, 2026-11-19)

**Status: research and design. Nothing here is decided; a council pass
judges it before any of it ships, and any of it becomes real only through
`DECISIONS.md` entries.** The owner, late on 2026-11-19, close to verbatim:
"I kind of tried to go in the direction of a framework for always-on agents,
but honestly that ain't gonna pay the bills, that's not the kind of customer
we have. So we need to pull back, refine the home page and add the new stuff
in a way that attracts customers." The new stuff is bring your own hardware
and the Mac mini. The owner also wants the numbers said plainly: what a 16 GB
Mac mini runs, what a 24 GB one runs, what a 256 GB disk fits.

Written by the design session on branch `landing-overhaul` (from `mac-mini`),
from `docs/LANDING.md`, the five research passes in the appendix (every page
read on 2026-11-19), the live page captured at 1440 and 390 in both schemes,
and the owned-sizing session's reading of the share rules. It changes no code.

## Summary

| Question | Answer |
|---|---|
| What the page sells today | "A platform for running your own always-on agents", built in layers, with the Orbit as its picture (I-1169, I-1212). Triggers, the phone ask and "bring any agent" lead; the machine, the carry and the wreck follow |
| Who pays | A developer who runs coding agents all day and wants them off the laptop: the loop the 2026-11-04 council scored highest (`repose run`, close the lid, the work comes back), the pain the 2026-09 research measured (lid-close and price), and the only customer the Own hardware plan can have (I-1364) |
| The sentence | **repose gives your coding agents a machine of their own, on our servers or on a Mac mini you own, and they keep working after you close your laptop.** |
| The pattern, researched | Of eighteen products that sell "your hardware, our software", only Coolify puts both homes in the hero, as a pair of calls to action; Umbrel and Home Assistant sell the box first and "your own hardware" as a column beside it; GitLab makes it a selector on the pricing page at one price; Plausible, Supabase, Sentry, Tailscale and Cal.com keep self-hosting off the home page. Nobody puts "this hardware runs this much" on a marketing page; four put it in docs |
| The page | Eight bands as today, one swapped: the hero (the machine, a real capture), **01 Where it runs** (our servers beside your Mac mini, with the figures) in place of 01 Layers, then Permissions, Setup, Machine, Agents, Install (with the Mac's one extra step), Pricing (with the Own hardware row), Questions rewritten |
| The numbers | One component, one source: a table derived from the share and reserve rules in Go, a drift test as `plans.ts` has, drawn on the landing and in `/docs/own-hardware`. Today's figures are placeholders and **one of the three is false under today's rules** (§5); nothing publishes until the owned-sizing session settles what a size means on an owned host |
| What may go live when | The pullback (hero copy, the cut band, the questions) can ship on the product that is live. Everything that names the Mac mini waits for the mac-mini gate (two P0 open, the park off, a 289 s start) and the sizing decision (§6) |
| Biggest risk | The owner moved to the framework pitch on 2026-11-11 because "runs after the laptop closes" is commodity; pulling back lands the page in that crowded field, and the hardware is the one thing none of the direct rivals sells to a person (§7) |

## 1. What the owner asked, and what the page says today

The live page (captured 2026-11-19 at 1440x900 and 390x844, light and dark;
the captures are in the design session's scratch, not in the tree) opens on
the headline "A platform for running your own always-on agents", a lead
about the machine, secrets, a schedule or a webhook, and the Orbit: repose in
the middle, five agent marks on a ring, "every night at 2", "an alert goes
off", "a webhook arrives" on the left, "your code, tools, logins", "your
secrets" and a phone asking "Roll back the last deploy?" on the right. The
first band after it is "Build it in layers": machine, walls, your setup,
secrets, triggers, agent. The machine, the one-command carry and the wreck
come third, fourth and fifth.

That page is I-1169's: the November repositioning toward primitives for
always-on agents, the job agent as its proof. The owner's reading on
2026-11-19 is that this customer does not pay the bills. The evidence in the
repo agrees with the owner on who does:

- The 2026-11-04 product value council scored 58 features with four lenses.
  The four at the top, voted lean-in by all four members, are one loop: auto
  port forward (8.3), the phone ask and reply (8.2), run and attach (8.2),
  sync at launch (8.1). "You type `repose run` and close the laptop."
  Triggers were not on the list; they shipped a week later.
- The 2026-09-27 demand research: the pain people name is lid-close and
  continuity, and price (at or under $10 a month on Hacker News), not setup
  or safety. The owner's own day on boxd (2026-09-29): the machine boots at
  once, then five to ten minutes to get GitHub working before a clone. That is
  the chore `repose run` removes, and it is the owner's I-772 sentence on
  the page already.
- The economics: every hosted seat loses money (I-642). The Own hardware
  plan (I-1364) is $10 a month for one host the user owns and sells none of
  our compute, which is how reach grows without the hosting bill. Its buyer
  is a developer with a Mac on a desk, which is the same person as the loop
  above.

So the pullback is to the two ideas the page sold from September to
November (I-477, I-772, I-843): one command turns your laptop's working state
into a machine; that machine is one an agent can wreck and that comes back.
What is added is where the machine lives.

## 2. Research: how "your hardware, our software" is sold in one screen

Eighteen products, read on 2026-11-19 by five research passes, each told to
quote the page and say what it could not fetch. The appendix holds the
quotes. The table holds what matters for a page:

| Product | First screen claims | We run it, you run it | Price | What a visitor must already know | Hardware to capacity |
|---|---|---|---|---|---|
| Coolify | "Self-hosting with superpowers." Deploy to "your own server" | **Both in the hero**: "Start with Cloud" / "Self-host for free". Even Cloud needs your server; the $5 is the control panel | Separate page: free self-hosted, $5 a month cloud + $3 a server | What Vercel or Heroku do; that they own or will rent a Linux box | Docs only: 2 cores, 2 GB, 10 GB for Coolify itself; nothing about what a server then runs |
| Umbrel | "Your cloud, in your home." Photo of the box | Hero sells the box (Buy); own hardware is section six, "Plug it in. Go Pro. Or build it.", three columns Home / Pro / DIY | "From" a runtime-filled price on the home page; the OS is free | That they pay for iCloud or Dropbox and resent it | Box specs (16 GB, 4 TB); DIY minimums on GitHub only; never "runs X apps" |
| Home Assistant | "Awaken your home." Local control and privacy | Installation page leads with Green (the box), then Yellow, a Pi, "a computer you already own", a VM. The cloud (Nabu Casa) is optional and sold apart | Green $199 on its page; Link $6.50 a month on nabucasa.com; the software never priced | That they own smart-home devices and have felt a vendor's outage | Green specs; Pi "minimum 2 GB"; "skills required" per install path; never a capacity |
| Unraid | "What will you build?" An OS for self-hosted servers | Own hardware is the premise; "what you bring" (a USB stick, a 64-bit PC) is on Get started, not the home page | **On the home page**: "starting at just $49", one-time | What a NAS or home server is | VM docs only: 8 GB minimum, "add RAM for each active VM", bands per VM kind |
| Proxmox VE | "Simplify your data center" | Bare-metal installer; "install on your hardware" on Get started | Separate page, per socket per year; free is said on Get started, not on pricing | What a hypervisor is and that a whole server is given | Requirements page: "minimum 2 GB for OS and Proxmox VE services. Plus designated memory for guests." The guest budget is left to the reader |
| Synology DSM | "Easy to use and built for everyone" | Never sold alone; "powers every Synology NAS" | Nowhere; the box is the price | That they will buy the box | None |
| GitLab | "Speed you can trust, all the way to production" | Not on the home page. Pricing page: a selector GitLab.com / Self Managed / Dedicated, "We host" / "You install, host GitLab on your own setup", same $0 / $29 under both | Separate page | A Git and CI team | Docs: "8 vCPU, 16 GB" for up to 1,000 users (reference architectures) |
| Mattermost | "Sovereign Operations & AI..." self-hosted and air-gapped in the hero | **In the hero**, twice; a section "You decide where your operations run" | Nowhere; contact sales | Defence or regulated-enterprise procurement | Docs: "1 vCPU, 2 GB RAM" for 1 to 1,000 users. The one product with hardware to users in one line |
| Sentry | "Code breaks, fix it faster" | Not on the home or pricing page at all; develop.sentry.dev: "the Business plan without any software limitations and no paid tier" | Separate page | What error monitoring is | Docs: 4 cores, 16 GB + 16 GB swap, 20 GB |
| Plausible | "Easy to use and privacy-friendly Google Analytics alternative" | Off the home page except a feature-card clause ("run it yourself if you want to"); its own page says "Self-hosting is a real commitment" | **On the home page**, $9 a month; self-hosted unpriced | Why to leave Google Analytics | README: "at least 2 GB of RAM" |
| Supabase | "Build in a weekend / Scale to millions" | Penultimate section, "Open source from day one ... self-host" | Separate page ($0, $25, $599) | What a backend and Postgres are | Hosted compute sizes with prices; self-hosting docs state no requirement |
| Cal.com | "Where people find the time to meet" | Not on the home page; the self-host fork (Cal.diy) is on GitHub with "Use at your own risk" | Home FAQ and a separate page ($12 a user) | What a booking link is | None |
| Tailscale | "Maybe don't put it on the public internet." | Silent; a third-party control plane (Headscale) is named on the open-source page, not sold | Separate page ($0, $8, $18 a user) | What a VPN is | None |
| boxd | "Composable computers for Devs and Agents", "in our cloud or in yours" | **In the hero sub-line** and the nav ("Self-host"); a section "Self-host boxd for free" on AWS, GCP or "any KVM host", free to 16 vCPUs | Separate page, metered per vCPU-hour and GiB-hour; "€0.03/hour for 2 vCPU · 8 GiB (10% active)" | What a sandbox for an agent is | **On the home page**: "1 vCPU / 4 GiB, 2 vCPU / 8 GiB (the default) and 4 vCPU / 16 GiB" |
| exe.dev | "A Private Computer for Your Agents." | Hosted only; "AWS / GCP VPC support" on Enterprise | Separate page, $15 a month flat for 2 vCPU, 4 GiB | SSH and root | On the home page: "2 vCPU · 4 GiB" |
| Fly Sprites | "Sandboxes aren't enough" | Hosted only | **On the home page**, per CPU-hour and GB-hour, with worked examples | Having found sandboxes limiting | 100 GB volume; no per-Sprite size |
| Docker Sandboxes | "Run agents safely, local to cloud." A real terminal of `sbx run claude` | Both, but "your own" means the laptop; a comparison table says where each rival self-hosts | **On the home page**, per hour by size | Running Claude Code and wanting it walled | On the home page: Micro 1 vCPU 2 GB $0.07/h to XL 16/32 |
| Coder | "Your developers have guardrails. Your AI doesn't." | **In the hero sub-line**: "on your own infrastructure"; no hosted option | Separate page; free community, per user per year above | An enterprise with infrastructure and a governance problem | None |
| Ona (Gitpod) | "The Enterprise software factory" | "Runs in your VPC" in the body | Nowhere; contact sales | Enterprise buyer | Docs only |
| Daytona | "Run AI Code." An SDK code block | A body section: "customer-managed compute in your cloud. Daytona provides the control plane" | **On the home page**, per vCPU-hour | Building an agent product with an SDK | Unit prices only |

What survives of this, for a page that sells both homes to one developer:

1. **The choice is a pair, named in the first screen, and the pair is
   calls to action or columns, never a paragraph.** Coolify's hero buttons,
   Umbrel's three columns, Home Assistant's installation list, GitLab's
   selector. Every page that does it well names the two homes with the same
   nouns in both places ("Cloud" / "Self-host"; "Home" / "DIY").
2. **The box is drawn, the software is screenshotted.** Umbrel and Home
   Assistant Green lead with a photograph of the hardware; every software
   product that leads with a picture leads with a real screenshot or a real
   terminal (Coolify, Plausible, Home Assistant, Docker Sandboxes). Nobody
   leads with a diagram of what the product is made of. The Orbit is a
   diagram of what the product is made of.
3. **"You run it" is sold with its cost named.** Home Assistant lists
   "skills required" per path; Plausible says "a real commitment"; Umbrel's
   DIY column says "Install it yourself" and "Community" support; Cal.diy
   says "Use at your own risk". The honest line sells the box or the hosted
   plan to the person who does not want the chore, and keeps the tinkerer.
4. **Prices on the home page are the exception and belong to the
   developer-facing products** (boxd, Sprites, Docker, Daytona, Plausible,
   Unraid). repose already has its pricing band (07) on the page; the
   pattern says keep it there and add the fourth thing for sale as a row.
5. **Hardware-to-capacity is a docs number everywhere.** Mattermost ("1
   vCPU, 2 GB RAM" for 1,000 users) and GitLab ("8 vCPU, 16 GB" for 1,000
   users) say it in one line; Proxmox refuses to ("plus designated memory
   for guests"); nobody says it on the home page. The owner's ask puts
   repose ahead of the field here, and it is also where a wrong figure
   costs the most, because it is the figure a buyer sizes a purchase on.
6. **No direct rival sells to the person who owns a Mac mini.** boxd
   self-hosts on "any KVM host" for a team; Coder and Ona sell to the
   enterprise's cloud; Docker's "your own" is the laptop. Apple silicon is
   named nowhere. That is the open ground, and the one thing the pullback
   can claim that the September page could not.

Two patterns were looked for and are **not** adopted, because the research
did not find them working anywhere: a hero that explains the architecture
(diagram, layers, components), and a hero that names the price.

## 3. The positioning, in one sentence

**repose gives your coding agents a machine of their own, on our servers or
on a Mac mini you own, and they keep working after you close your laptop.**

A stranger repeats it as: "it is a machine for your coding agents that keeps
working when you shut the laptop; you can rent theirs or use your own Mac
mini." Both halves are true today on the product that is live, except the
Mac mini half, which is true on the `mac-mini` branch and is gated (§6).

The two words doing the work: *coding agents*, which names the customer and
drops the job agent and the responder from the pitch (the owner's call); and
*a Mac mini you own*, which is the only noun in this field no rival uses and
the one that makes the price a stranger can repeat ($10 a month, and the
Mac they may already have).

## 4. The page, in order

The sheet, the rails, the one numbering, the two rule weights, the two
command treatments, every rule in `docs/LANDING.md` from "The sheet" down
stays. What changes is what the bands say and one band's subject. Each band
below names what a visitor must understand when they leave it; a band that
does not do that is cut.

**Hero.** *Understand: this is a machine for my coding agents, it carries my
laptop's setup, and it keeps working with the lid shut.* Headline, serif, no
eyebrow, no number: a candidate in the owner's register is "Your coding
agents, on a machine of their own". Lead, one paragraph on its measure: "`repose
run` copies your checkout, tools and logins to a machine where the agents
keep working after you close your laptop." A second sentence, "On our servers,
or on a Mac mini you own.", joins it only when §6's gate passes. Under the
copy, the call-to-action row as today: "Get started", "Copy install
command", the install line. When the Mac ships, the pattern from Coolify is a
second action, "Run it on your Mac mini", to the 01 band's cell; the council
should judge whether two actions split the row (§7, point 5).

The picture: **a real capture, not a drawing.** The research found no
software page that leads with a diagram of its parts, and every one that
leads with a picture leads with the product running. The owner's own reading
of 2026-09-25 was that the real terminal was the branch's strength, and I-837
led the page with "a terminal of a real `repose run`". `landing/Terminal.svelte`
is still in the tree and draws on `/design`. The recommendation is that
hero, or OneCommand moved up, judged at real size in both schemes before the
council sees it; the Orbit joins `HeroOrbit*`, `HeroSteps` and the others in
the `?hero=` gallery, where a later round can read it again. Nothing new is
drawn for this pass.

**01 Where it runs.** *Understand: there are two homes, what each costs,
and what my own Mac would run.* Replaces 01 Layers. Two cells on the halves
line, flush, split by an inner rule, stacked below 1024px: "On our servers"
and "On a Mac mini you own" (the x86 box is a docs link in the cell, not a
third column: the docs say what it needs and it is not a product a stranger
pictures). Each cell is a serif claim, one paragraph of facts from the docs,
and its reference command. Ours: a `large` starts in about 12 seconds, your
plan's memory runs at once, snapshots go to cloud storage; `repose run`.
Yours: an M3 or newer with macOS 15 and 16 GB; `repose host add` puts one
virtual machine on the Mac and changes nothing else on it; your projects go
there and nobody else's; snapshots stay on its disk; `repose host add`. Under
the paragraph, in the Mac cell, the figures component (§5). The band's side
item is the pair of docs links, "Your own hardware" and "Pricing", set as
the Agents links are.

What the band does not say: a start time on the Mac. The docs carry "about
four minutes" (`/docs/own-hardware`) and that figure is on the board as far
too slow (ENG-70). A page that says it repels; a page that omits it keeps the
rule that every line says what is true, once, where they look for it, and
the docs are where a buyer looks for it. The council decides whether an
omission that size is honest. If the band ships before ENG-70 closes, the Mac
cell says nothing about speed and the hosted cell keeps its 12 seconds, which
is itself a comparison a careful reader will make.

**02 Permissions. "Let it run with full permissions."** *Understand: the
agent is not asked command by command, and a snapshot brings the machine
back.* Unchanged: the wreck picture, the one paragraph. This is the September
idea the owner kept through every round and the thing a VPS cannot give.

**03 Setup. "Your laptop's setup, in one command."** *Understand: the hour
I spend setting up a cloud machine is the thing removed.* Unchanged
(I-772's heading and paragraph, OneCommand). If OneCommand becomes the hero's
picture, this band takes Terminal or its paragraph alone over a ruled
command; the council picks; the page never shows the same capture twice.

**04 Machine. "Work on the machine as you would on your laptop."**
*Understand: snapshots, ports on my localhost, the agent's browser, my
editor.* Unchanged, four cells.

**05 Agents. "The agents you use, and the tools they need, on first boot."**
*Understand: Claude Code, Codex, Gemini CLI, opencode and pi are there, with
the tools.* Unchanged: the marks, the tools, the shell. This is where the
five marks live once the Orbit goes.

**06 Install. "Start in three commands."** Unchanged for the hosted path.
When the Mac ships, a fourth ruled row, "On a Mac mini, once", with
`repose host add` and its Copy, under the three; the band's heading does not
change its number. The row runs on a visitor's Mac, so it is the runnable
treatment.

**07 Pricing. "Launch prices."** The three cells and the seats line as
today, and the fourth thing for sale as a row under them: the held commit
`7db572b5` on `ws/19d-plan` (ENG-16) already draws it as `OwnHardwarePlan`,
"$10 a month" with one line saying what it is, in the cells' own faces, no
action of its own, and holds `OWN_HARDWARE` in `lib/plans.ts` against
`plans.go` and `billing.md`. That is the shape; this proposal adds nothing to
it and ships it when ENG-16 unblocks.

**08 Questions. "Before you try it."** Six questions, each a reason to try
it, rewritten for the customer in §1. Kept: closing the laptop; what the
agent can reach; what happens when it breaks something; what an agent costs
while it waits. Cut: "What can I build with it?" (the responder story) and the
server comparison as written, which argue the framework. Added, once §6
passes: "Can I run it on a Mac I already have?" (M3 or newer, macOS 15,
16 GB; one virtual machine on it; `repose host rm` removes it whole) and
"What happens when my Mac sleeps or reboots?" (the docs' offline section,
in its own words: the machines are out of reach until it is back, what they
wrote to disk is kept). Triggers and notifications, which the cut 01 band
carried, get one question only if the council wants one ("Can it start on a
schedule or a webhook?"); otherwise they live in the docs links, which is
where the owner's rule puts help nobody asked for.

Never on the page, as before: burst cores, user counts, a rival's name,
collaboration. Added to that list: a start time on the Mac until ENG-70
closes, and any figure from §5 until the sizing decision lands.

## 5. The numbers: one component, one source, placeholders today

The owner's figures, spoken on 2026-11-19: a 16 GB Mac mini runs one `large`
machine; a 24 GB one runs one `xl` or two `large`; a 256 GB disk fits about
two projects.

**Do not publish any of them.** The owned-sizing session (branch
`owned-sizing`) read the rules the code applies today and sent this, which
this session checked against `scheduler.HostReserve`, `OverheadMiB` (512 MiB,
`internal/hostd/guest/manager.go`) and the launcher's "three quarters of the
Mac's" default (`internal/cli/hostscmd.go`):

| Mac | Appliance VM (3/4) | Placeable (less an eighth, floor 2 GiB) | Runs under today's rules | The owner's figure |
|---|---|---|---|---|
| 16 GB | 12 GiB | 10 GiB | one `large` (8 GiB + 512 MiB), nothing beside it | one `large`: **agrees** |
| 24 GB | 18 GiB | 15.75 GiB | an `xl` needs 16.5 GiB and is **refused**; two `large` need 17 and are refused; what fits is a `large`, a `small` and a `tiny` | one `xl` or two `large`: **false today** |
| 32 GB | 24 GiB | 21 GiB | one `xl`; or an `xl` and a `small`; or two `large` | not given |
| 256 GB disk | a 100 GB sparse file by default | thin volumes, the pool overcommitted by design (I-586) | a project costs about 0.3 GB at creation and then what it writes, up to its class's disk (40 GB for a `large`) | "about two projects": **no rule behind it** |

`nix/hosts/mac.nix`'s own comment says "one xl" for 24 GB and is wrong by
the same arithmetic. None of this is the page's problem to solve: the
sizing session's proposal changes what a size means on an owned host (the
owner's target is one machine getting most of the appliance, about 8 cores
and 14 to 15 GB on a 24 GB mini), and the figures above become different
figures when it lands.

**The design, which does not wait for the figures.** One source and one
drawing, the way the plans are done (I-663):

- **The source is the rules, not hand-written numbers.** The share rule
  and the reserve rule exist as functions (`internal/cli/appliance.go`,
  `internal/api/scheduler`); one Go function composes them with the class
  table and `OverheadMiB` into "for a Mac with N GB: what runs at once", and
  it is exposed as a generated constants file or an endpoint the web reads
  at build time. A drift test holds the web's copy against it, as
  `plans.test.ts` parses `plans.go`. The sizing session named this as a
  work item on the board, not something its branch implements; it is filed
  (§9).
- **One drawing, `HardwareFigures.svelte`**, drawn in the 01 band's Mac
  cell and in `/docs/own-hardware` between `<!-- hardware -->` markers
  that a test holds, as `billing.md` is held to `plans.ts`. The docs page
  is where a buyer sizes a purchase; the landing is where they first see
  that a figure exists. Both say the same thing because both read the same
  table.
- **The shape of the drawing** is three rows at most, each a Mac's memory
  and what runs, and one row for disk, in the cells' own faces: no table
  header a reader has to learn, no footnote. "16 GB: one large machine. 24
  GB: ... 256 GB disk: ..." The exact wording comes from the sizing
  session's page-safe sentences, which it has undertaken to send.
- **Placeholders are marked in code, not on the page.** The component's
  data file carries today's figures under a `PLACEHOLDER` comment naming
  this proposal and the owned-sizing branch, and the band that draws it is
  not in the page until the figures are real. A placeholder that reaches a
  visitor is a promise, and the product rule forbids it.

## 6. The product rule: what may go live when

No page may promise anything that is not shipped. Read against what is live
on 2026-11-19:

| | State | Source |
|---|---|---|
| The hosted product, every band from 02 to 08 as today | Live | repose.run |
| `/docs/own-hardware` | **Live**, including "Starting a machine takes about four minutes" | `curl` 200 on 2026-11-19; the page's own text |
| `repose host add` in the published CLI | Live in v0.1.51 | `gh` latest release; `cli.md` |
| The appliance image | Published 22:28 UTC tonight, `appliance-2026.11.19` | the council report |
| The Own hardware plan, $10 a month | In the payment catalogue; **not offered publicly**: `POLAR_PRODUCT_OWN` is the switch and the public-sale commit is held (ENG-16) | I-1364; `7db572b5` |
| The Mac path through the review gate | **Not passed**: ENG-67 (no way to say where a project runs), ENG-34 (billing measures against the wrong plan), ENG-2 (the park is off, a Mac reboot loses what was in memory), ENG-70 (a 289 s cold start), the Mac surface itself barely reviewed | `docs/reports/2026-11-19-mac-mini-council.md` |
| What a size means on an owned host | Being redesigned | branch `owned-sizing` |

So the overhaul is two shippable halves with different gates:

- **A. The pullback** (hero copy without the Mac sentence, the hero's
  picture, 01 Layers cut, the questions rewritten) describes the live
  product and can ship as soon as the council passes it. It needs a decision
  amending I-1169 ("what we sell") and I-1212 (the hero), reserved through
  `ops/dev/release-queue id` by whoever builds it.
- **B. The Mac mini on the page** (the Mac sentence and action in the hero,
  the 01 Where it runs band with the figures, the Install row, the Pricing
  row, the two questions) ships with or after the `mac-mini` branch, after
  the review gate passes, after the sizing decision, and after the
  figures' source exists. It is one piece, because a page that names the
  Mac in one band and not another sends a visitor looking for the rest.

A page that ships A alone is honest and narrower than the owner asked for.
A page that ships B before its gate breaks the rule the owner called
absolute. The order is A, then B, and the board carries both.

## 7. Counter-evidence against this recommendation

1. **The owner left this pitch for a reason nine days ago.** On
   2026-11-11: "we're not peddling the strongest part of repose ... try to
   show the component nature that makes an always-on agent." The 2026-09-26
   research and the 2026-11-04 council both found "runs after the laptop
   closes" to be commodity (exe.dev at $15, Sprites, boxd, Docker Sandboxes,
   Claude's own cloud). The pullback lands in that field. What this proposal
   answers with is the one noun none of them has (a Mac you own) and the
   two September differentiators (the carry, the wreck); if the council
   finds those three thin, the framework pitch was the stronger page and the
   owner should hear that.
2. **Nobody in eighteen products puts hardware-to-capacity on a home
   page.** Proxmox, the product that knows the most about it, refuses to.
   The figure a buyer sizes a $600 purchase on is the figure most likely to
   be wrong a month later, and one of the owner's three is wrong tonight.
   The one-source design limits the damage; it does not remove the risk
   that a figure true at launch is false after the next class or share
   change, and the page then says a thing the product refuses.
3. **The Mac mini buyer may be the lowest-paying customer, not the
   paying one.** The Hacker News price signal is at or under $10; the plan
   is $10; I-1364 says nobody has measured the relay's bytes per user, so
   whether $10 holds is unknown. Plausible keeps self-hosting off its home
   page and says "a real commitment" because that visitor costs support
   and pays nothing. A band that sells the Mac well may fill the inbox with
   people whose Mac sleeps.
4. **The Mac path is not through its gate, and the band must wait for
   it.** If ENG-70 (289 s) and ENG-2 (the park) take weeks, the overhaul the
   owner asked for tonight is, for weeks, only the pullback. The council
   should say whether A alone is worth shipping or whether the page waits
   for B.
5. **Two actions in the hero split the row.** Coolify does it and is the
   one product that does; its second action is free self-hosting, ours is a
   paid plan either way. The owner ruled on 2026-11-02 that the buttons
   never stand beside the lead; a second button may be one too many on the
   centred row at 1440 and certainly stacks at 390. The alternative is one
   action and the choice made in the 01 band, which is Umbrel's shape.
6. **Dropping the triggers from the page hides a shipped feature.** Triggers,
   webhooks and the phone ask are live and documented, and the council of
   2026-11-04 scored the ask among the top four. The pullback leaves them to
   the docs links and at most one question. A customer who would have bought
   for the schedule does not learn it exists from the home page.
7. **"Coding agents" narrows the sentence.** The job agent and the
   responder are real uses the owner built. The sentence excludes them on
   purpose; if the owner wants them back, the sentence is "your agents" and
   the hero loses a word that tells a stranger who it is for.

## 8. What was not checked

- **No visitor data.** Nothing in the repo or the research says who visits
  repose.run, what they click, or where they leave. The customer in §1 is
  argued from the council, the research and the economics, not measured.
- **The rival pages were read as text, not rendered.** First-screen visuals
  are described from alt text, file names and markup; Umbrel's price is
  filled by a script and was not read on the page; Ona's prices are from a
  2025 blog post; Coder's Premium price was not on its page. Blacksmith and
  Depot were not looked at. Nothing was captured at 1440 or 390.
- **The Mac path end to end.** This session ran nothing on a Mac. The gate
  state in §6 is the council report's, as of tonight.
- **A live non-exempt account.** Whether the dashboard offers the Own
  hardware plan on production is established only by `GET /v1/billing`
  returning it with `available: true` (the council's note); no reviewer has
  seen a live non-exempt checkout.
- **The relay's cost per owned host**, which decides whether $10 holds
  (I-1364).
- **The sizing decision**, which decides every figure in §5.
- **The hero's picture at real size.** The recommendation names existing
  captures; none was placed in the new hero and judged at 1440 and 390 in
  both schemes, because this pass changes no code. That judgement is the
  first thing piece A's builder does, before the council sees a page.
- **A decision id.** None reserved; this pass decides nothing.

## 9. Shippable pieces, on the board

One card per piece, in Open unless gated, each pointing here:

1. **ENG-82, piece A, the pullback:** the hero's copy, 01 Layers cut, the
   questions rewritten; a decision amending I-1169 and I-1212. Open.
2. **ENG-83, piece A's picture:** a real capture in the hero in place of
   the Orbit, judged at 1440 and 390 in both schemes before the council.
   Open, separate from the copy so the council can take one without the
   other.
3. **ENG-84, the figures' source:** one Go function from the share, reserve
   and class rules, a generated table the web reads, a drift test, one
   component drawn on the landing and in `/docs/own-hardware`. Open; its
   figures come from the owned-sizing decision, its mechanism does not.
4. **ENG-85, piece B, the Mac mini on the page:** the hero's second
   sentence and action, 01 Where it runs, the Install row, the Pricing row
   (ENG-16), the two questions. Blocked on the mac-mini gate, ENG-16, the
   sizing decision and ENG-84.
5. **ENG-86, the council pass on this proposal**, as `docs/REVIEW.md` runs
   one: three readers as customers, by surface. Open.

The proposal itself is ENG-77.

ENG-47 (the docs name no price for the plan) and ENG-16 (the held public
sale commit) exist and are not duplicated.

## Appendix: the research notes

Five passes on 2026-11-19, each told to fetch the live page, quote it, and
say what it could not fetch. Condensed; the quotes are the pages' words.

**Tailscale.** h1 "Maybe don't put it on the public internet." CTAs "Start
connecting devices" / "Contact sales"; a laptop screenshot. Home and pricing
silent on self-hosting; `/opensource` names Headscale as "developed
independently and separately from Tailscale". Pricing $0 / $8 / $18 a user.
No hardware figures anywhere.

**Coolify.** h1 "Self-hosting with superpowers." Sub "...to your own
server." CTAs "Start with Cloud" / "Self-host for free"; a product
screenshot. Pricing page: Self-hosted "Free forever"; Cloud "$5 /month base
price (connect 2 servers)", "$3 /month per additional server", "You need to
bring your own servers from any cloud provider". Docs: "Coolify needs 2 CPU
cores, 2 GB RAM, and 10 GB disk space to run." Home h2s: "Push your code.
Coolify does the rest."; sponsors; "Everything you need to ship".

**Plausible.** h1 "Easy to use and privacy-friendly Google Analytics
alternative". CTAs "Start free trial" / "View live demo"; a dashboard
screenshot. Home never says self-host; one feature card: "run it yourself if
you want to." Pricing on the home page: $9 / $14 / $19 a month. The
self-hosted page: "Self-hosting is a real commitment"; CE "You do it all
yourself". README: "At least 2 GB of RAM is recommended."

**Sentry.** h1 "Code breaks, fix it faster". CTAs "Get Started" / "Get
Demo"; a Rive animation. Home and pricing have zero hits for self-host or
open source. develop.sentry.dev: "consider self-hosted as the Business plan
without any software limitations and no paid tier"; "4 CPU Cores, 16 GB RAM
+ 16 GB swap, 20 GB Free Disk Space". Pricing $26 / $80 a month annual.

**GitLab.** h1 "Speed you can trust, all the way to production", sub
"Agentic software engineering for your team". CTA "Try for free"; logos, no
picture. Home never says self-managed. Pricing page selector "GitLab.com |
Self Managed | Dedicated": "cloud-hosted by us" / "An instance of GitLab
managed by you"; $0 / $29 under both. Install page: "We host. No technical
setup required." / "You install, host GitLab on your own setup." Docs: "8
vCPU, 16 GB memory" for "up to 1,000 users".

**Mattermost.** h1 "Sovereign Operations & AI for Critical Infrastructure
and National Security", sub ends "Runs self-hosted, private cloud, or fully
air-gapped." CTAs "Talk to an expert" / "Explore the platform"; a product
screenshot. Section "You decide where your operations run" with five
deployment rows. No price anywhere; "Contact Sales". Docs: "1 - 1,000 users
- 1 vCPU/cores, 2 GB RAM".

**Supabase.** h1 "Build in a weekend / Scale to millions". CTAs "Start your
project" / "Request a demo"; no hero picture, seven product cards. Self-host
appears in the penultimate h2 "Open source from day one". Pricing $0 / from
$25 / from $599; "Can I self-host Supabase for free?" "Yes". Self-hosting
docs state no hardware requirement.

**Cal.com.** h1 "Where people find the time to meet". Sign-up CTAs; a
mocked Safari window with a booking page beside a ChatGPT window. Home
mentions self-hosting only inside a quoted review. The open-source fork
Cal.diy: "Use at your own risk ... intended for users who want to self-host";
"There is no hosted/managed version." Pricing $0 / $12 / $28 a user.

**Home Assistant.** h1 "Awaken your home", sub "Open source home automation
that puts local control and privacy first." CTA "Get started"; a dashboard
screenshot. Installation page: "Home Assistant runs on your own hardware
... The quickest way to start is with Home Assistant Green ... You can also
build your own hub with Home Assistant Yellow, install on a Raspberry Pi or a
computer you already own, or run Home Assistant in a container or virtual
machine." Each path lists "SKILLS REQUIRED". Green: "$199 / 179€", "Plug it
in", "4 GB LPDDR4X", "32 GB eMMC". Nabu Casa Link $6.50 a month. The cloud
is "optional"; "Home Assistant works fully without it."

**Unraid.** h1 "What will you build?", sub "...Make the most out of your
hardware—no matter what kind of drive you have on hand." CTAs "Explore
Unraid" / "Get Started". Home page pricing section: "starting at just $49";
$49 / $109 / $249 one-time. Get started: "A high-quality, named-brand USB
flash device that is at least 4 GB". VM docs: 8 GB minimum, "16 GB or more
(add RAM for each active VM)", bands per VM kind.

**Proxmox VE.** Corporate h1 "Simplify your data center"; VE page "a
complete, open-source server management platform for enterprise
virtualization", CTAs "Get started" / "Download"; a screenshot carousel.
Get started: "Just download the ISO image and install Proxmox VE on your
hardware." Pricing per socket per year, €120 to €1,100; free is on Get
started, not pricing. Requirements: "Memory, minimum 2 GB for OS and Proxmox
VE services. Plus designated memory for guests."

**Umbrel.** h1 "Your cloud, in your home.", sub "The cloud is just someone
else's computer. Umbrel is yours." CTAs "Explore" / "Buy" for Home and Pro;
a photograph of the boxes. Section six "Plug it in. Go Pro. Or build it.":
Home ("Plug in and go", "Priority support"), Pro, DIY Hardware ("Your own
drives", "Install it yourself", "Community", "Best for people who enjoy the
DIY setup as much as the result"). Price "From" a runtime-filled number; the
OS "Free for personal use". Home specs: Intel N150, "16 GB DDR5", up to
4 TB. DIY minimums on GitHub: 4 GB RAM, 32 GB drive.

**Synology DSM.** h1 "Easy to use and built for everyone", "powers every
Synology NAS". Never sold alone; no price, no download; a model page lists
"4 GB DDR4 pre-installed; maximum capacity of 32 GB".

**boxd.** h1 "Composable computers for Devs and Agents", sub "...in our
cloud or in yours." CTA "Get started" beside the install line; no picture.
Nav "Self-host"; h2 "Self-host boxd for free.": "in your own AWS or Google
Cloud account, or on any KVM host. Free up to 16 vCPUs". Home: "Machines
come in 1 vCPU / 4 GiB, 2 vCPU / 8 GiB (the default) and 4 vCPU / 16 GiB".
Pricing per vCPU-hour and GiB-hour, "€0.03/hour for 2 vCPU · 8 GiB machine
(10% active)". No Mac or Apple silicon anywhere.

**exe.dev.** h1 "A Private Computer for Your Agents." CTAs "Try free with
ChatGPT" / `ssh exe.dev`; a decorative illustration. Hosted only. Pricing
$15 a month flat for "2 vCPUs, 4 GiB memory"; closing h2 "Friends Don't Make
Agents Use AWS."

**Fly Sprites.** h1 "Sandboxes aren't enough". CTAs "Try Sprites" / "API
Reference"; an install code block. Hosted only. Pricing on the home page per
CPU-hour and GB-hour with worked examples; "Nothing is charged per sprite."

**Docker Sandboxes.** h1 "Sandboxes", sub "Run agents safely, local to
cloud." CTA "Get $250 in cloud credits"; a real terminal of `sbx run claude`.
"Self-hosting means ... Your laptop, today". Pricing on the home page: Micro
1 vCPU 2 GB $0.07/hour to XL 16 vCPU 32 GB $1.12.

**Coder.** h1 "Your developers have guardrails. Your AI doesn't.", sub
"Safely run your favorite AI, agents, and enterprise development on your own
infrastructure." CTAs "Install Coder" / "Start a trial"; a video. No hosted
option; Community free, Premium "per user" with no figure.

**Ona (Gitpod).** h1 "The Enterprise software factory", sub "Run background
agents in the cloud." CTA "Contact sales"; a video. "Runs in your VPC" in
the body. No price on the pricing page.

**Daytona.** h1 "Run AI Code." CTAs "Start for Free" / "View Docs"; an SDK
code block. "Customer-Managed Compute ... Daytona provides the control
plane." Pricing on the home page per vCPU-hour; "$200 in free compute".
