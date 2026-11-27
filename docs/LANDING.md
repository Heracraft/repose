# Landing page: rules for visuals and copy

The owner's rules for `apps/web/src/routes/+page.svelte`,
`apps/web/src/routes/landing.css` and `apps/web/src/lib/components/landing/`,
collected from their reviews since September 2026. Read this before touching
the landing page; each rule exists because a version broke it and was sent
back. The look (tokens, type, buttons, the code pane, the plan cards) is
Herakraft UI, recorded in `DESIGN-LANGUAGE.md` "The landing" and DECISIONS
I-837; this file covers what the landing shows and says.

## What we sell

A platform for running your own always-on agents, composed in layers,
with any agent and any model (DECISIONS I-1169, amending I-1167 and
I-477). Two ideas, the first one leading, and every section serves one
of them:

1. You build an always-on agent in layers: a machine sized to the job,
   walls that keep it off your laptop and the internet, your dev
   environment copied in (or left out), secrets by name, triggers (a
   schedule or a webhook starting a session) and any agent on top,
   which asks you by email, ntfy or Google Chat.
2. The agent runs there with full permissions. It reaches that machine
   and what the logins on it reach, and a snapshot brings the machine
   back.

The reader is a developer who runs coding agents. The page carries no
eyebrow (owner, 2026-11-15): the headline opens it. Since I-1446 the
headline sells the machine rather than the platform, and names both
homes: our servers, or a Mac mini the reader owns. The hero's picture is
the Quickstart's real `repose run` session (`landing/Terminal.svelte`,
I-837), so the page leads with the product running; the Orbit it
replaced drew the always-on pitch the headline no longer makes and is a
`?hero=` candidate now ("The hero's picture" below).

Two lines are written ahead of shipping on the owner's word (I-1169):
triggers that start a session, and a tiny machine of 2 GB. They name no
command or class that does not exist. Everything else is what the
public docs say a user can do. Never on the page: burst cores, bare
metal or your own hardware, user or customer counts, a rival's name,
collaboration or a software factory.

## The page (I-1452, amending I-1446, I-1212, I-961, I-843, I-287)

**Superseded by I-1552 (2026-11-22).** The page is now: the hero ("A sandbox per repo, with undo", `HeroVisual.svelte`, the install command and the price line), 01 a scroll-driven quarter wheel of three beats (`ScrollWheel.svelte`), 02 Safety (`Wreck.svelte` on a light stage), 03 Your hardware (`YourHardware.svelte`, and `MacFigures.svelte` behind its switch; I-1674, below), 04 Pricing, 05 Start (Cloud, or your machine with `repose login` before `repose host add`). The owner's rules behind it are in `docs/design/landing-council/direction.md` (Part A), which wins where it and this file disagree; the list below is the I-1452 page, kept for its reasons.

Guide B 13.1, on the 1200px frame every page shares (I-656), laid out as one
ruled sheet on a 12-column grid with no full-bleed dark slabs interrupting the
sheet. Total desktop height is ~4,400px (down from 6,837px). Every band after
the hero opens with the same head (`01` to `06`) and carries zero looping
`anime.js` animations on `/`. In order:

1. **Hero.** No eyebrow and no number: the serif headline "Your coding agents,
   on a machine of their own", then a tight two-sentence lead naming
   `repose run`, isolated Linux VMs, Claude Code/Codex and other agents working
   after the laptop closes, and both homes (our servers, or a Mac mini or x86
   box you own). Under the copy, one centred row from 1024px (left-aligned
   below): `Start a free week` (`hk-btn-lg`, returning to `/billing`) beside a
   single `CodeCopy` install bar (`curl -fsSL https://repose.run/install.sh |
   sh`; single-line from 640px, wrapping after `/` below 640px so `| sh` is
   never clipped on phones). Under that row, one factual meta line reading
   `trialDays`, the live Solo price and `OWN_HARDWARE.priceCents` from
   `lib/plans.ts`, stating BYO agent subscriptions/API keys and linking to
   `#pricing`. Under the copy, the Quickstart's real `repose run` session
   (`landing/Terminal.svelte`, centred at `max-width: 46rem`) inside the 1200px
   sheet on `--stage` with its 16px dot grid. No `PauseButton` appears on `/`
   because nothing loops; looping `?hero=` gallery candidates still mount
   `PauseButton` automatically when `motion.live > 0`.
2. **01 How it works** (`#how`). "What repose handles for you", with "One
   command replaces manual server setup and ad-hoc SSH tunnels." on the right.
   Three flush columns on the thirds (`1.1 One-command sync`, `1.2 Full
   permissions`, `1.3 Snapshots`), each with a 1-line serif title, two factual
   sentences, and a unique reference command (`repose run`, `repose run -p
   "..."`, `repose snapshots restore`). Replaces the redundant `Wreck`,
   `OneCommand` and `ComesBack` animated cartoons.
3. **02 Workflow** (`#features`). "Feels like localhost, runs while you're
   away", with a 2x2 flush hairline grid:
   - Row 1 carries the two genuinely non-obvious real captures rendered
     **statically** (`animated={false}`, zero loops): `2.1 Ports` (`Localhost`,
     `repose attach`) and `2.2 Browser` (`Browser`, `repose browser`).
   - Row 2 carries two compact text-only hairline cells: `2.3 Editor`
     (`ssh your-project.repose`) and `2.4 Away & idle` (`repose ls`, surfacing
     phone/email notifications and hosted 15-minute auto-freeze/wake directly on
     the page).
4. **03 Agents & tools** (`#agents`). "Preinstalled on first boot", with "All
   agents" and "Everything installed" links on the right, then `Ready.svelte`:
   the five preinstalled agents drawn with BOTH their marks and visible names
   (`Claude Code`, `Codex`, `opencode`, `Gemini CLI`, `pi`, amending I-287),
   over a two-column flush strip listing preinstalled runtimes/everyday tools
   and background installation of your laptop's global tools plus `repose
   config add`.
5. **04 Pricing** (`#pricing`). "Launch prices", with `Start a free week` and
   the live `seats-line` from `GET /public/seats` on the heading's last line.
   A flush sub-bar states the 7-day free trial, concurrent-memory billing, and
   BYO agent subscriptions/API keys. Under it: `PlanCells` (`Solo`, `Plus`,
   `Pro` from `lib/plans.ts`), a one-line hosted size guide (`tiny` 2 GB to `xl`
   16 GB, up to 100 projects, cloud snapshots), `OwnHardwarePlan` (`$10 a month`
   from `OWN_HARDWARE`) styled as a true flush hairline row whose description
   aligns with the `Plus` column at 1024px+, and a compact `#parts` note stating
   Mac/x86 requirements with links to `/docs/own-hardware` and `/docs/billing`.
   **Neither the pricing section nor `#parts` names a start time or a Mac memory
   sizing claim** (holding I-1446 while ENG-70 and ENG-91 remain open).
6. **05 Questions** (`#questions`). "Before you try it", with "Limits" and
   "Billing" links and five `<details>` rows answering the real buyer
   objections: BYO Claude/Codex/Gemini accounts, running on a Mac mini or x86
   box, Mac sleep/restart behaviour (explicitly stating restarts keep disk and
   clear memory), what an agent can reach, and hosted idle freeze billing.
7. **06 Start** (`#install`). "Start in three commands", with a closing
   `Start a free week` CTA on the heading's last baseline, three light ruled
   `CodeCopy` rows (`6.1 Install the CLI`, `6.2 Sign in`, `6.3 Run in any
   checkout`, wrapping below 640px), and a flush footer strip linking "Install
   and sign in" and "Run and attach".

## 03 Your hardware (I-1674)

The band says what ships today for a box the reader owns, an x86 box or a
Mac, between Safety and Pricing, so it stands right before the price that
carries "Your machine". The core is the hardware; a Mac is a plus (owner,
2026-11-26: "Let's not lead with Mac please. That's a plus. The core is on
your hardware."). It replaces no band.

- **Head.** "03 Your hardware", the h2 "Run it on hardware you own", and
  one docs link on the right, "What it needs" (`/docs/own-hardware`).
- **Three cells on the thirds** (`YourHardware.svelte`), each its label,
  a serif title, one paragraph and a small drawn figure, every sentence
  true of an x86 box and a Mac alike (`own-hardware.md`,
  `ssh-and-editors.md`): 3.1 "Add a box you already have" ("your new
  sandboxes go there"; the figure: `repose host add` on the box, `repose
  run` in any repo), 3.2 "Sandboxes share its memory" (no size; `repose ls` shows what
  each uses; one bar of the box's memory shared by three sandboxes and the
  free rest), 3.3 "Reach it from anywhere" (the box dials out; after
  `repose login` on any laptop, `ssh your-project.repose` reaches it). Cut
  because not true of both without a condition: "one command" (an x86 box
  also runs the installer `repose host add` prints), "every new sandbox"
  (`--host repose` places one on our servers), "starts while 1 GB is free"
  (a Mac also limits by cores), memory "handed back" (an idle sandbox
  returns part of it after five minutes). The figures are diagrams of
  shipped rules and carry no number.
- **The Mac part, behind one switch.** `MAC_DESIGN2_LIVE` in
  `apps/web/src/lib/data/mac-figures.ts`. Off (today), the band ends on
  the three cells. On, it adds `MacFigures.svelte` under an inner rule: an
  h3 "And on a Mac, each sandbox is its own VM" with the legend (the accent
  swatch "Each sandbox its own VM", the grey one "Every sandbox inside one
  Linux VM (repose 0.1.66)") and where and when it was measured, then four
  compact charts, 3.4 to 3.7: start, snapshots, memory once idle, a
  restart through `repose host restart`. Four across from 1280px, two from
  1024px, one below; the cells of a row share their rows (subgrid). Turn
  it on the day design 2 ships and its public docs say what the charts
  say; nothing else changes.
- **One bar idiom** (the Mac charts). A bar is solid to the fastest run and
  lighter to the slowest, and the Mac head says so; its value, in mono,
  stands in one column to the right, with words that go with it ("memory
  kept") under it; the axis is a hairline under the bars' column, labelled
  at its ends only. Accent for design 2, `--hk-text-4` for the nested
  layout; no other colour.
- **Every Mac figure comes from the data file**: the bar's length, its
  value, the screen reader's sentence (one `aria-label` per chart, the bars
  hidden) and the notes. A figure there is a measured range with its n,
  and its comment names who measured it, when and where the raw data is. A
  comparison that is not like for like says what differs under its chart;
  a condition (an unlocked Mac for the rest, a restart started by repose
  for the memory) is said on the page, from the same file.
- **When a number moves**, edit it in that file and its comment, and
  nothing else; change an axis's `max` only when a bar would overrun it
  (`mac-figures.test.ts` fails then, and when a title's claim, "under a
  second", "in seconds", stops being true of the numbers). Drop a
  condition the day the shipped design no longer has it.
- **No motion.** Everything is drawn in its final state; nothing is added
  to "Motion".
- **Height.** With the switch on, the desktop page under reduced motion
  stays under `landing-hero.spec.ts`'s 4,500px (4,348px measured); that is
  why the Mac charts are compact and four across.

## Earlier page structure (I-837, I-843, I-1446 — superseded by I-1452)

Guide B 13.1, on the 1200px frame every page shares (I-656), the
headline centred over the hero's picture from 1024px and starting
under the logo below it, laid out as one ruled sheet on a 12-column
grid (I-961, option D; see "The sheet" below). In order:

1. **Hero.** No eyebrow and no number: the serif headline "A platform
   for running your own always-on agents" (the owner's words, I-1169),
   then the lead, two sentences: the machine and what you add to it
   ("Give your agent a machine of its own, then add what it needs: your
   dev environment, secrets, a schedule or a webhook that wakes it.")
   and the laptop ("It keeps working with your laptop shut."). From
   1024px the copy stands centred over the picture (I-1212): the
   headline balanced on 26ch, the lead under it on 36rem, and "Get
   started", "Copy install command" and the install line on one centred
   row under both. The headline, the lead and that row share the
   picture's centre line; below 1024px all three start on the text
   line, under the logo. The buttons never stand beside the lead at any
   width (owner, 2026-11-02). The install line is mono, exactly what the
   button copies, on one line, scrolling under a fade where its column
   is narrower. Under the copy, the Quickstart's real `repose run`
   session (`landing/Terminal.svelte`, I-1446) on the hero's dark field,
   which runs to the window's edges and ends on the next band's rule.
   **No motion control stands there**: the session reveals its lines
   once and stops, so there is no loop to pause (I-666 asks for the
   control on a looping picture, not on every picture). A looping
   candidate loaded with `?hero=` keeps its control in the field's top
   right corner, its chip's right edge on the text line.
2. **01 Where it runs** (`#parts`), with "Your own hardware" and
   "Pricing" (the docs pages, set as the Agents links) on the heading's
   last line, then **two cells on the halves** (`.cols--halves`), split
   by one inner rule with no gap as the plans are: 1.1 On our servers,
   1.2 On a Mac mini you own. Each is its numbered label, a serif
   claim, one paragraph and its command in the reference treatment
   (`repose run`, `repose host add`). Below 1024px they stack, each
   under its rule. The titles wrap balanced.

   **Neither cell names a start time, and neither names a memory
   figure** (I-1446). The Mac's start is on the board as far too slow
   (ENG-70) and what a size means on a host the user owns is unsettled
   (ENG-91); two cells under one heading, one carrying a number and one
   silent, is an implication rather than an omission, so while either is
   true of one home the page gives it for neither and the docs carry
   both. `landing.spec.ts` holds that: the band must not match
   "seconds", "minutes" or a memory figure. This band replaced **01
   Layers**, whose six cells built the platform bottom to top; that
   band argued the framework pitch the page no longer makes, and its
   subjects live in the docs the heading links.
3. **02 Permissions. "Let it run with full permissions"**, with one
   paragraph on the heading's last line (you don't approve its commands
   one by one; it can touch its machine and anything the logins on it
   can; restoring a snapshot brings the machine back), then the wreck
   picture (`landing/Wreck.svelte`, the hero's until I-1167) on its
   field: the one stage that bleeds to the window's edges, a dark plate
   in both schemes (in the dark scheme a step lighter than the page,
   with a lit top edge) carrying a 16px dot grid that fades with its
   sheen, the windows on the frame, opening on its own edge under the
   head. The picture draws in its dark palette there. It is the page's
   one moment of contrast; the rest of the page stays on the page's
   tones. The agent wrecks the cloud machine and a snapshot restores it
   (I-843, I-961); it is the demo the summit script points at. The still
   frame (reduced motion, no script) has no globe, since it has no skill
   to come from it; in the loop the globe is drawn in line.
4. **03 Setup. "Your laptop's setup, in one command"** (the owner's
   I-772 heading and paragraph, the paragraph on the heading's last
   line), then the OneCommand picture on its field: `repose run` copying
   the laptop's source control, changes and ignored files to the
   machine.
5. **04 Machine. "Work on the machine as you would on your laptop"**
   (`#features`, I-961), with "Each project gets a machine of its own,
   and you reach it from the tools already on your laptop." on the
   heading's last line. Four cells, each its numbered label (4.1
   Snapshots, 4.2 Ports, 4.3 Browser, 4.4 Editor), a serif claim, one
   paragraph with a fact the picture cannot carry, the command the
   picture shows under the paragraph, and under them its picture
   (ComesBack, Localhost, Browser, Editor): the words first and the
   stage under them, ruled at its top (hero loop round 4, owner: the
   reader meets the claim before the drawing), set as the hero's
   install line is (13px mono, the second grey) with no `$` and no
   copy button, since a visitor without the CLI cannot run it: the
   page has two command treatments, runnable (the steps, with Copy)
   and reference (the hero's line, the cells). The cells are flush on
   the sheet, split by inner rules with no gap: from 1024px a 2x2 whose centre hairline stands
   half a gap left of the column line, so the right cells' text starts
   on it, and runs unbroken from the head's foot to the next band's
   rule. Below 1024px they are one column; a cell and its stage run the
   window's width, and the picture's windows run between the text lines,
   the same inset at both sides at every width. The snapshot cell says
   what a snapshot holds and that `--as` restores it to a new machine,
   which the permissions band's picture does not show; the Browser
   cell says what `repose browser` is for (watching, a login or a
   captcha), and the bridge stays in the docs.
6. **05 Agents. "The agents you use, and the tools they need, on first
   boot"**, with "All agents" and "Everything installed" (grey, with the
   kit's arrow, 16px apart) on the heading's last line, ending on the
   right text line (under the heading below 1024px), then the Ready
   picture, from rail to rail: the five agents' marks alone (I-287) on
   the stage's tone, each naming itself under the pointer; an inner
   rule; then the tools on the first third, on the stage's tone too, the
   everyday tools' line under them, and the machine's shell on the other
   two thirds: a closed window under the "your cloud machine" head on
   the terminals' ground, inset on the stage as the grid's windows are
   (24px above and below, the text lines at the sides).
7. **06 Install. "Start in three commands"**, with "Install and sign in"
   and "Run and attach" (the docs pages, set as the Agents links) on the
   heading's last line, then ruled rows (I-961): the step's number (6.1
   to 6.3, in the label's number grey and fixed width) and serif name in
   the left half, its command filling the right half flush on the
   stage's tone from the centre hairline to the rail, Copy at the cell's
   right end, its label ending on the right text line as the head's
   links do. On a phone the number and name share a baseline over the
   command's cell, which runs the window's width under its own rule.
   Install the CLI, Sign in (`repose login`) and Run in any checkout
   (`repose run`). Each row shows exactly what its Copy copies, with no
   `$`, on one line at every width (under 640px the install line scrolls
   under the copy box's fade), and it runs on a visitor's laptop.
8. **07 Pricing. "Launch prices"** (`#pricing`). On the heading's last
   line, "Start a free week" ending on the right text line as the link
   pairs do, and the seats line before it on the same baseline, from
   `GET /public/seats` ("18 seats left"; the waitlist and seat copy
   stays as it is until the owner decides the 2026-10-17 notes). The
   heading says the launch prices and the button the free week, so no
   line repeats either (I-961). Under the head, `PlanCells` in its
   public variant (the one drawing of the plans, I-663), as flush cells
   split by inner rules with no card edge: three across on the thirds
   lines from 1024px, stacked below. Under them, one more flush cell for
   the Own hardware plan (`OwnHardwarePlan`, I-1364): the name and
   `$10 a month` at the left, and one line saying what it is ("A Mac
   mini or an x86 box you own, in your account. One host; its memory and
   disk are the limits."). It is a row and not a fourth card because it
   sells none of the three figures a card carries, and it has no action
   of its own: the section's one button is "Start a free week".
9. **08 Questions. "Before you try it"**, with "Limits" and "Billing"
   on the heading's last line, set as the Agents links. Six questions,
   each one a reason to try it, overhauled on the owner's ask to show
   the primitives and what is special (2026-11-12, I-1169): what you
   can build (any agent you would keep running: a responder your alerts
   wake, which reads your metrics and logs and asks before it changes
   anything; nothing a developer already does by hand with tmux and
   an agent's CLI, such as several agents on worktrees, owner,
   2026-11-12); how it differs from your own server (it freezes and
   wakes for the next webhook, and a server restart leaves its programs
   and agents running, `lifecycle.md`); closing the laptop (it keeps
   working, and you hear by email or on your phone); what the agent can
   reach (`secrets.md` "What an agent on the machine can reach": `dev`
   with sudo, the logins copied and the secrets set, no other project,
   the laptop only through a port or a Chrome you lend it); what
   happens when it breaks something (a snapshot restores the whole
   disk, Claude Code's login aside; nightly, at each stop and your own,
   kept seven days and the newest); what an agent costs while it waits
   (it freezes after about 15 minutes of quiet and wakes in a second or
   two; you pay for the memory your plan runs at once). No answer
   repeats a layer cell: the setup, the secrets, the triggers, the
   agents, the tiny machine and the notifications are 01's. No setup
   trivia (where a login lives, Windows, how commits come back, what a
   stopped machine costs): those belong in the docs and give a visitor
   no reason to try it (owner, 2026-10-21).

### The sheet (I-961, option D)

From 1024px the page is a 12-column grid on the frame's content box,
32px gaps, and every placement spans six columns (halves), four (thirds)
or three (quarters). The halves line is the footer's (Product), the
quarters its Policies; the thirds hold the plans and Ready's lower row.
Below 1024px every section is one column.

The sheet is the content box outset by `--edge`: the frame's gutter below
1200px, so the sheet is the window, and 16px from 1200px, where the frame
stops growing and page shows beside it. A sheet element (a figure, the
cells, the steps, the plans, the questions) runs the sheet's width and
pads its text back onto the content line, and 16px off any rule inside
it, so every vertical rule stands 16px from the text beside it, and so
does every picture's window: a picture's windows stand on the text
lines at every width. From 1200px two rails (`--hk-border`) draw the
sheet's edges from under the header to the footer's last rule (the one
over the theme switch, which runs rail to rail and closes them), and a
9px cross in the fourth grey marks each band rule, the footer's top rule
and that last rule where they meet a rail. The sticky header covers the
first band's top, so the rails start under it with no cross. The hero
field breaks the sheet: the rails stop at its top edge, which carries
its own pair of crosses.

Every band after the hero opens with the same head: its numbered label
on the text line ("01 Layers" to "08 Questions"), the h2 16px under it,
and from 1024px the band's one side item in the right half on the h2's
last baseline (under the h2 below 1024px). Under 640px each link is a
44px target, and a pair that wraps (the install pair at 320px) stands
its second line 32px under the first, the targets overlapping. Every
band has one: a paragraph (permissions, setup, machine), starting on
the column line, or an action ending on the right text line: a pair of
links to the docs (agents, install, questions), 16px apart, or the pricing button with the seats
line before it. Every side action ends on that one right edge. The h2's
last line stands 43px over what it heads (44 at 1024) whether its side
item is a paragraph or links; pricing's is 8px more, the button's box
reaching under the baseline, so the button itself stands the head gap
over the plans. Its foot is an inner rule across the sheet.

The page has one numbering (I-961, the owner's pick, from alternative
C): the bands' labels, the cells' labels (1.1 to 1.6 and 4.1 to 4.4,
8px over the cell's title) and the install rows (6.1 to 6.3). A label is the page's label
type with its number in the second grey at a fixed 2.75em, so the names
after the numbers start on one line, and a step's name starts there
too. Nothing else carries a number: no figure bar, no "Fig.". A band's
or cell's label is read out and copied as "01 Layers": the space after
the number is text inside the fixed width; a step's number is
aria-hidden, since its row is the band's list. The hero has no label
of its own: its headline opens the page (I-1212).

Two rule weights: band rules and rails in `--hk-border`, every rule
inside a band in `--hk-hairline`. The page has no gap between bands. Each
band after the hero opens with a rule the window's width and ends on the
next one: a band that ends on a picture or a ruled list (the steps, the
plans, the questions) has no bottom padding, and its last stage or row
ends on that rule; the questions end on the footer's. Empty space is
padding inside a band, never a strip between two.

No stat strip: repose has no public figure the page can read at render
time and a reader can reproduce. No isometric figure, and no anatomy
figure of physical equipment (a shelf, a rack, brackets and screws),
anywhere on the page (owner, 2026-10-04 and 2026-10-19), with one
exception the owner asked for: the Orbit's ring (I-1212).

## The hero's picture (I-1212, replaced by I-1446)

**The hero is the Quickstart's real `repose run` session**,
`landing/Terminal.svelte` (I-837's hero, restored by I-1446): the pane
shows what the CLI shows, read from the docs at build time, so nothing
on it is invented. It reveals its lines once and stops, so it needs no
pause control, and without scripts or under reduced motion every line
is there from the first paint. The page leads with the product running,
which is what every researched page that leads with a picture does.

The Orbit below is what it replaced, kept because `?hero=` still draws
it and because the reasoning is worth having when the next round reads
this file. It drew one always-on machine, the agents on a ring, and
what wakes it: the pitch the headline stopped making at I-1446.

The Orbit was, `landing/HeroOrbitV1.svelte`: repose in the
middle on its tile ("repose", "always on"), the five agents as bubbles
on a ring around it, each its mark and its name; out on the left, in
plain words, what wakes it (every night at 2, an alert goes off, a
webhook arrives), on the right what it carries (your code, tools,
logins; your secrets) and the phone it asks you on ("Roll back the last
deploy?", Yes or No). Hairline traces join the chips to the tile, the
alert's in the accent. It stands on the hero's dark field, the plate the
wreck picture used to carry, in both schemes.

It is named for a screen reader by one `aria-label` on the picture
(`role="img"`), everything inside it `aria-hidden`, and it names no
command and no variable: the page's words, not the CLI's.

The loop: an alert goes off, a pulse runs along the blue trace into the
machine, the machine and Claude Code light, a pulse runs out to the
phone and the phone asks; then it clears. It moves by class changes and
the pulses' travel, timed by anime.js, and starts only once the page has
been still (see "Motion"). The still frame, which is what reduced motion
and a page with no scripts get, is every part drawn with the phone
asking. At 1024px and up the parts stand around the tile; below, they
stack: what wakes it, the ring, then what it carries beside the phone.

The rounds behind it (hero loop, 2026-11-12 to 2026-11-15): the hero was
judged in nineteen rounds against voidzero.dev's landing (read at 1440
and 390), which is where the page gets one object under the copy, the
parts named by small chips on short lines, and the type carrying the
hero. Each direction was drawn and iterated as a whole picture
(`HeroOrbit*`, `HeroSteps`, `HeroBento`, `HeroDepth`, and `Compose`, the
three lanes of rounds 1 to 8); the Orbit won. They stay in the tree and
draw in the hero's place with `?hero=<name>` in the browser
(`landing/heroes.ts`), so a later round can read them again; the page
itself ships one hero and loads no other. `landing/Stack.svelte`
(I-1169's stack of plates) and `landing/Parts.svelte` are in the tree
unused, and branch `landing-centrepiece-2d` has the page with Parts.

## The pictures (I-843)

The drawn pictures are main's (`components/landing`: Wreck, OneCommand,
ComesBack, Localhost, Browser, Editor, Ready), restored under Herakraft UI.
They keep their own names for colours (`--pic-*`, `--sh-*`, `--ink`,
`--surface`, `--sunken`, `--rule`); `routes/landing.css` maps each onto a
kit token or a zinc step on `.landing`, so they follow the theme switch
and draw in the kit's zinc and a muted slate blue (`#4b6496` light,
`#93a7d0` dark), quieter than the kit's link blue: at the kit's blue the
pictures' chips and wires were too strong (owner, 2026-10-21). The page's
own text uses no blue; blue is the focus ring's and the pictures'.
Windows and chips take a 6px radius, the kit's buttons their own;
nothing else is rounded. One ladder in both schemes: the page, the stage
a step off it (with a sheen at its top-left and a highlight on its top
edge), a window's head a step up and its body on top.

Every picture stands on one stage (I-961, option D), with no figure bar
over it. The stage is the cell itself, flush from rule to
rule, with no edge or radius of its own; its windows stand in it closed,
on the text lines at the sides. A band picture (the wreck, the setup)
stands in a field `--field-y` deep above and below its windows (half a
band, never under 48px), and ends on the next band's rule. A grid cell's
stage is 22rem tall, ruled at its foot, its windows 24px (20 from 640 to
1023, 12 on a phone) from its top and foot, so no window is cut at the
stage's edge: the Editor's statusline keeps its right segment whole and
the Browser's agent log shows whole. The four grid pictures share one
type scale: window chrome and terminals at 12px to 13px mono, 11px on a
phone, and the Browser's captured page at 13px body type: one capture
pixel is drawn 0.72px, the page stands at the left of its window on the
app's ground, and the toolbar's address bar is drawn stretched to the
window's width. A capture that is wider than its window (the Editor's
78 columns) is cropped on the right into a fade, as a terminal is, never
shrunk to fit; on a phone the Editor's explorer (its 20 columns and the
split) is cropped off whole, so the rows start at the tabline and the
code gets the window; a captured page is cropped in its white space, never
inside a sentence, at every beat of its loop. On a phone the Localhost
and Browser captures keep a size their text reads at and crop on the
right into a fade as well; the Browser's own scrollbar is cut off. The
Localhost app page ends under its nav's rule: drawn at its window's
width, the headline under it outsized the cell's title, and the ground
above the headline read as a page still loading; the machine's window
shows Vite's ready line over the Local one. ComesBack's snapshots stand
in a window as tall as the machine's, so both end on the stage's
inset, the two tiles at its top; on a phone both tiles stand whole at
every beat. The four grid pictures
share one treatment (I-961, the owner's pick, from alternative C): each
is a closed window under a head, inset on its stage, never a raw capture
flush to the cell, and Ready's shell under 03 Agents takes the same
window. "Your cloud machine" heads the Browser's and the
Editor's windows over their own chrome (the captured toolbar, nvim's tab
bar), as it heads the wreck picture's, the setup picture's, ComesBack's,
Localhost's and Ready's, where it tells the machine from the laptop or a
snapshot.
Only the wreck picture's machine carries the running dot, since its stop greys it.

The terminals (Ready, Localhost's tmux, the Browser's agent log, the
Editor's nvim) draw from one palette, `--t-*` in `pictures.css`, light on
the light page and dark on the dark one. Their captured colours are mapped
by role (one-dark and xterm-256 by their ANSI tables, Tokyo Night through
two maps, one for grounds and one for text, since one hex is a ground in
one run and a text colour in another), never their text. The Editor's
code and tabs sit on `--t-bg`, its explorer, tabline and statusline on
`--t-bar`, and its accents (the explorer's frame, the tree, the current
line number, the filled mode and clock) on the palette's muted hues. The Browser's captures exist in the light
scheme only; in the dark scheme they are drawn inverted with their hues
turned back (`invert(0.9) hue-rotate(180deg)`), so the app reads as a
dark page beside the dark windows (I-961).

The page has one motion control, not one per picture (WCAG 2.2.2;
owner, 2026-11-12): "Pause motion" in the hero field's top right
corner, its chip's right edge on the text line, shown at rest in the
pictures' quiet grey while any loop runs. Pressing it holds every
picture. On a touch screen it is a 44px target with the chip drawn in
its middle.

`landing/Terminal.svelte` is no longer on the landing; `/design` still
shows it.

## Each fact once

The install command shows twice, under the hero's buttons and as the
first of the three steps: the owner's call (I-843), so a reader who
scrolled past the hero starts from one list. `repose run` shows in the
pictures and in the run step; the cells carry other commands. The
snapshot story has one telling per place: the permissions band's picture shows the wreck
and the restore, the ComesBack cell says how a restore runs (on the
stopped machine, or `--as` a new one), and FAQ 3 gives the schedule
(each night the machine runs, at each stop, your own). Plan
figures come from `lib/plans.ts`, seat counts from the api; none is typed
into the page.

## Names a stranger understands

- A visitor doesn't know what `recruiting` or `wira` is, or that it's a
  repose project. Never show a bare internal name as if it explains itself.
  The pictures' checkout is `your-project`, the docs' own example.
- No product jargon without meaning (`large`, a list like
  `large · sudo · docker` after a name).
- "Project" is ambiguous (a production deployment? the repo on your
  laptop?). Avoid the word where its meaning is not obvious.

## Real, or not at all

- Every string on the page is true: from the docs, the CLI or the api,
  or, in a picture, from a capture or the CLI source. Crop, never retype
  or invent. A capture's colours may be mapped by role onto the
  terminals' palette, and the Browser's inverted in the dark scheme
  (I-961); its text and layout never change, with one exception: an
  internal checkout or session name in a capture reads `your-project`,
  the pictures' checkout, at the same column width (I-961).
- Real apps and real projects, captured from real runs. No invented
  sample apps (`todo-app`), no drawn fake UIs standing in for real ones.
- A partial imitation of a real tool is worse than none: show the real
  thing in full (a real LazyVim screen) or a different representation (a
  mark, a diagram). No fake window chrome (traffic-light dots).
- Never promise recovery the product cannot give (I-772). A snapshot
  restores a machine only if one was taken, so the page says repose
  snapshots the machine nightly and that you can take one yourself any
  time; it never implies a wreck undoes itself, and never "a minute".

## Copy

- No hand-holding (owner, 2026-09-27; CLAUDE.md "Trust the reader"): a
  section head is its title, with a sentence only where the content
  cannot carry a fact (pricing's rule). A cell gets one paragraph of
  facts, a step is its title and its command.
- Short is not the point; plain is (owner, 2026-10-17, I-772). Say the
  thing a developer already feels, in the words they would use. The chore
  repose removes is a machine you set up for tens of minutes to hours, the
  GitHub login, the CLI to install, the `.env` to paste, before an agent
  can start.
- The lead is one paragraph on its 34rem measure, never one line per
  sentence (I-772, measure amended by I-837).
- The shapes that read as generated, and are banned on this page
  (owner, 2026-10-17, I-772, from `hardikpandya/stop-slop`):
  - **x-not-y.** "costs you minutes, not your setup". Say the thing you
    mean and stop.
  - **Triplets.** Three sentences or three clauses in a row beat like a
    speech. Prefer two.
  - **Choppy fragments** standing in for sentences in prose. A deliberate
    label is not prose and may be two words ("Launch prices").
  - **Punchy closers.** A last line written to be quoted.
  - **No actor.** Prefer a person doing something ("You spend an hour
    setting up a cloud machine") over a state of the world.
  - **Em dashes**, filler adverbs, and "every/always/never" as emphasis
    rather than fact.
- Never write as if the product were Claude-only: "the agent" drives the
  browser; not "Claude Code does X".
- A cell's title says what you can do, not what you can see (I-772):
  "Watch the agent's browser, and take over".
- Plan memory is "running at once", never a cap.

## Where terminals are allowed

Terminal UIs appear only in the feature grid (`#features`) and in the
agents picture (I-843, restoring main's rule). The Orbit, the wreck picture and the setup picture
draw windows and chips, not terminal output; the commands section uses light
command rows.

## Motion

Everything that moves on the landing. Each runs only under
`prefers-reduced-motion: no-preference`, except the two colour changes at
the end of the list (a class change's colour, hover and press), which are
plain CSS transitions and move nothing; with reduced motion the page is
drawn in its final state, so no class changes in a picture either. The setting is followed while the page is open:
a visitor who turns reduced motion on mid-loop sees every picture stop on
its final frame and every waiting shape group land at once
(`watchReducedMotion` in `landing/inview.ts`, I-397). The wreck picture's still
frame, with reduced motion and with scripts off, is its end state: the
machine restored with the good work on it, the stop tile, 21:25 lit
under it and the nightly one, and the permanent wires, with no label; the
skill chip is not in it, since inside a restored machine it would read as
still infected, and neither is the cross, which goes when the restore
begins (I-667). With scripts on and motion allowed, the first paint is
the frame the loop starts from, drawn in CSS under `html.js` (set by
`app.html`'s one inline script): the machine with its header, postgres,
node and the agent, the project rows dimmed, and the nightly tile alone
on the rail, so the wreck never flashes before the story (I-398, I-667).
If the app's script never mounts the picture (a bundle that fails to
load or throws), the still frame replaces it after 4s, a cut with no
motion (I-400). The wreck picture's wires are drawn in CSS, so the no-JS frame
has them; only the skill's trail is measured by the script, and only the
loop draws it.

The pictures move only by `translate`, `scale`, `opacity`, `clip-path`,
colour and a wire's `stroke-dashoffset` (the wreck picture's connectors draw
along their length): nothing they animate changes layout, so a loop adds
no layout shift (the Browser picture's ring is four edges scaled to size,
and the snapshot tile folds by a clip, its box keeping its height). The
wreck picture's rail and its tiles are laid out in CSS, the newest tile filling
what the stubs under it leave, so hydration resizes nothing (I-399,
I-667).

One controller decides when each looping picture plays
(`landing/motion.svelte.ts`, I-666). Each of the six (the Orbit, the wreck picture,
OneCommand and the three moving grid pictures) registers with it in
place of an observer of its own:

- **Still first.** A loop starts only once its picture has been in view
  with the page still for a second (`QUIET_MS`, hero loop round 6): a
  reader scrolling past sees the still frame, never a move cut off
  mid-flight by the scroll. Scrolling again holds every picture it
  reaches.

- **Pause.** The page's one control (`PauseButton.svelte`, WCAG 2.2.2):
  the picture chip with the windows' hairline and `--radius` corners,
  "Pause motion" beside it, `aria-pressed` while paused, in the tab
  order throughout, and shown while any loop runs. It never stands over
  a head or content (I-673, I-961): it stands in the hero field's top
  right corner, its chip's right edge on the text line. Its hit area is
  44px. Paused, every picture stays on its frame through scrolling out
  and back until play is pressed. A pointer moving inside a picture, a press on it, or focus
  inside it holds the frame too, and the loop resumes 2s after they
  leave; a pointer the page scrolls under, with no movement of its own,
  holds nothing. There is no button with reduced motion or without
  scripts, since nothing moves then.
- **One grid picture at a time.** In the feature grid, only one
  picture plays: the one under the pointer or focus, else the one last
  pointed at while it stays in view, else the in-view one nearest the
  viewport's middle (a new one takes the turn only when 24px nearer, so
  two cells of a row do not trade it back and forth). The others show
  their still frame, the one drawn with reduced motion: a cut, with no
  crossfade. The Orbit, the wreck picture and OneCommand are in no
  group.
- **Turns.** A grid picture's still frame is its loop's last beat, the
  frame at its seam (Localhost 9900ms, Browser 17200ms). Each cycle the
  picture reports that beat (`picture.looped()`); the leader nobody
  points at then hands the turn to the next in-view grid picture in DOM
  order, so two cells of a row take turns, a loop each. A picture that
  gains the turn from its still frame starts its first cycle at its
  seam, so the still leaves through the loop's own exit (`onMode`'s
  `fromStill`), never by a cut to the first frame (I-676).
- **Width, not height.** A picture rebuilds its loop only when its own
  width changes (a ResizeObserver on the picture, `watchWidth`), and the
  rebuilt wreck picture seeks back to where it was. A phone's address bar
  collapsing changes the window's height alone and restarts nothing.

- **The snapshot mark** turns a quarter when a snapshot is taken and a
  full turn back when one is restored. Between loops it keeps the angle
  it was left at, so it never cuts to another angle (I-401).
- **The pictures** loop in anime.js, the calm motion of "Your working
  state" as the top of this file defines it: stagger, travel, settle, rest,
  loop, each move 120ms to 1.1s. By kind:
  - *Travel* (a row, a chip, a snapshot miniature, the malicious skill,
    the Browser picture's pan and pointer): 520ms to 1s, `inOutCubic`.
  - *Reveals* (a row, a count, a time, a page fading in): 200ms to 500ms,
    `outQuad`, anime.js's default, so a reveal that names no ease is one.
    The Localhost picture's app page is one of these: the refused page,
    dimmed from the first frame, fades out first (200ms `inQuad`), and
    the app fades in after it (300ms `outQuad`), so the two pages never
    show at once; the seam mirrors it (I-669, I-676). Captures that do
    not line up (the Browser's pages, scrolled between captures, and its
    toolbar's two URLs) swap in 16ms instead: a crossfade of two texts
    shows both. A row that drops in as it appears (the wreck picture's
    work rows, OneCommand's agent commit) takes 320ms `outCubic`, opacity
    with a 6px drop.
  - *Strikes and wires drawing*: 260ms to 380ms, `outCubic`.
  - *Exits* (a loop's last frame fading before the rest, a wire or a
    mark going once its part is played): 300ms to 700ms, `inQuad`. Every
    fade to 0 in the pictures names `inQuad` (I-400).
  - *Dimming to rest*: the Browser picture's older log rows fall to 0.7
    as a newer call lands, 400ms or 500ms, `outQuad`.
  - *A terminal scrolling*: the Browser picture's log moves a whole row
    at a time as a call lands, two rows in 240ms (anime.js `steps(2)`),
    so no row ever shows cut in half (I-676).
  - *A file being written* in the wreck picture: the row's light rises and falls
    once in 1s, `inOutCubic`, as its diff stat pops in.
  - *Arrivals* (a chip, a cross, a tick landing): 250ms to 380ms,
    `outBack`, the one overshoot.
  - *The agent's bob* in the wreck picture, while it works: two 2px lifts in
    1.1s, `inOutCubic`.
  - *The rogue agent's shake* in the wreck picture, when it turns red: 2px either
    way in 480ms, four moves of 120ms, `inOutCubic`.
  - *The crosses' pulse* in "Break it and roll it back", on what git
    cannot bring back: each grows to 1.35 and back in 420ms, `inOutCubic`,
    60ms apart.
  - *The snapshot tile's fold* in "Break it and roll it back": the older
    tile clips up to its head in 550ms `inOutCubic` as the newer one lands,
    and at the loop's seam unfolds back to the top the same way (550ms
    clip, 700ms travel, `inOutCubic`) as the newer one fades (I-669).
  - *The restore's wipe* in "Break it and roll it back": each row the
    snapshot holds wipes in over its wrecked twin by clip-path, from the
    snapshot's side (the right beside the machine, the left where the
    snapshots stand under it), 450ms `inOutCubic`, 90ms apart (I-669,
    I-675).
  - *Struck names greying* in the wreck picture: a 200ms ease-out colour
    transition in CSS, to `--pic-faint`.
  - *The snapshot mark* is above: a quarter turn in 420ms `outQuad`, a
    full turn back in 1s `inOutCubic`.
  - *The shutter* in the wreck picture, as a snapshot is taken: an accent wash
    over the machine rises to 0.9 and clears in 460ms, `outQuad`.
  - *The knock* in the wreck picture: the malicious skill bounces 8px toward
    your laptop and back, 380ms `outQuad`, while its trail draws: a red
    dashed line from the skill to the machine's wall, level with your
    private rows, 700ms `inOutCubic` by `stroke-dashoffset`. The wall's
    red edge (200ms `outQuad`) and the cross (300ms `outBack`) land at its
    tip; the laptop is never tinted (I-667). Below 1024px the trail runs
    up the machine's right edge to the rule under its head, and the cross
    sits over the status dot, inside the wall (I-677).
  - *A wire lighting* in the wreck picture: the permanent hairline's lit copy
    grows from one end, 380ms `outCubic`, and fades, 300ms `inQuad`; the
    snapshot wire in the accent as a snapshot is taken or restored, the
    globe's in red as the skill comes in.
  - *The stop* in the wreck picture: the title's dot greys (a 200ms colour
    change), and the agent and its mode line dim to 0.35, 400ms
    `inQuad`. The skill, its trail, the wall's red edge and the cross
    stay until the restore begins and fade together then, 400ms
    `inQuad`. After the restore the agent and its mode line return,
    400ms `outQuad` (I-677).
  - *The wreck picture's snapshot copy*: a copy of the machine leaves along the
    snapshot wire and lands in its tile's place, drawn only inside the
    rail's body, so it never covers the machine, the gap or a head. It
    shrinks early (scale and fold 620ms `outCubic`) and travels late
    (620ms `inOutCubic`). The tile it lands over folds to its head under
    it, 220ms `inOutCubic`; the copy fades in 150ms `inQuad`, gone 750ms
    after it left, and only then the new tile fades in, 250ms `outQuad`,
    so two drawings never show at once. The restore runs the other way:
    the 21:25 snapshot grows into the machine from the wire's end, drawn
    only inside the machine's walls, over 800ms (travel `inOutCubic`,
    growth `inCubic`), and fades off the restored machine in 500ms
    `inQuad` (I-677).
  - *The wreck picture's repo row* on `repose run`: the laptop's row greys to
    `--pic-faint` while its copy is away (a 200ms colour change). The
    copy flies under the laptop's window; wide, it rises into the band
    over the `repose run` chip in 300ms `outQuad`, crosses the gap
    (900ms `inOutCubic`) and settles onto the machine's row (600ms
    `inOutQuad`); wide, it also fades in over 250ms `outQuad` from 300ms
    into its flight, once its name is clearing the laptop's wall, with
    its right 40 percent masked, so its bare fill never shows alone at
    the wall (I-961); stacked, it appears 10px over the machine's row and
    settles, 600ms `outCubic` (I-677).
  - *The wreck picture's wreck values*: postgres counts down from 3,532 to 0 in
    its one value, 600ms `outQuad`, as ComesBack's counts do; node's
    version fades out, 300ms `inQuad`, before "not found" fades in
    (I-677).
  - *The wreck picture's seam*: the project rows dim to 0.22 and the 21:25 stub
    goes, 450ms `inQuad`; the nightly tile, folded at the rail's foot,
    rises and unfolds behind the stop tile, 550ms `inOutCubic`, and the
    stop tile then rolls up off it from the bottom, a 500ms
    `inOutCubic` clip, so the rail always shows one whole drawing
    (I-677). The wreck picture's beats
    (ms): `repose run` 250, the repo row's travel 400, the rows land
    1300, the work 1800, your snapshot command 3700, the shot 4000, the
    reach 5500, the skill 5850, rogue 7300, the wreck 7800, the knock
    8800, the cross 9500, the stop 10000, the stop's snapshot 10350, the
    restore 11450, restored 12350, the seam 17350, the end 18400 (I-667).
  - *A copy's travel* in "Your working state" (I-668, I-675): two legs
    of one 820ms move along the axis it crosses: across the gap unseen
    to the wall of the panel it lands in (205ms, linear), then in through
    that wall onto its slot (615ms `outCubic`); on the other axis it
    moves in one 820ms `inOutCubic`. It shows in 16ms. The real row
    fades up under it (from 0.22 on the machine) in 160ms as the copy
    fades off in 160ms.
  - *The run chip making room* in "Your working state": it slides from
    the middle to its place beside (stacked) or over (side by side) `git
    fetch repose` as that chip arrives, 450ms `inOutCubic`, and back at the loop's end.
  - *The machine's rows dimming* in "Your working state", at the loop's
    end: to 0.22 in 450ms `inQuad`, the wreck picture's seam, and up from there
    as each new row lands.
  - *Rows making room* in "Your working state": a panel's rows move down
    one row, 450ms `inOutCubic`, as a commit takes the top slot, and both
    panels' rows back up at the loop's end.
  - *The snapshot copy* in "Break it and roll it back": the machine's
    rows travel into the new tile, 850ms `inOutCubic`, reaching the
    tile's scale in the first 510ms (`outCubic`); the copy fades in its
    last 150ms (`inQuad`), and the tile comes in (250ms) as it is gone
    (I-675).
  - *The Orbit* (I-1212): the alert chip lights at 900ms and a pulse
    runs along the trace into the machine (1100ms `inOutSine`, falling
    below 1024px, 900ms); the machine and Claude Code light at 2100ms;
    a pulse runs out to the phone at 3300ms and the phone asks at
    4300ms; the alert clears at 5200ms, the machine at 6200ms and the
    phone at 8600ms. A 9.4s round, ending on the still frame. Each
    change is a class the CSS transitions (colour, opacity, scale);
    only the pulses travel.
  - *The pointer's press* in the Browser picture: it scales to 0.86 and
    back in 220ms, `outQuad`, as your click lands.
  - *The motion control under the pointer*: its icon and edge darken in
    150ms, the CSS `ease` (I-666).
  - *Colour as a class changes* (CSS transitions, the CSS `ease` unless
    named): the `repose run` chip filling as it fires, 180ms (Wreck,
    OneCommand, Localhost, Browser), OneCommand's `git fetch repose`
    chip and the wreck picture's `repose snapshots create` chip the same way; a
    row whose copy is in flight greying to `--pic-faint` and back, 200ms
    (OneCommand); the Localhost address bar's edge turning blue as the
    forward lands, 180ms; the Browser log's bar on the
    acting call, 300ms opacity; in "Break it and roll it back", the
    machine's and a tile's edge turning blue (300ms), a row's tint (250ms),
    a value's colour (250ms), git's mark and the snapshot mark (200ms) and
    the older tile's time greying (400ms).
  - *Hover and press* on the page's links and buttons: the Foundation's
    150ms colour change (`DESIGN-LANGUAGE.md`, "Motion"). A question's
    row takes `--hk-hover` and its plus turns to the first ink, and a
    step's command cell darkens by the kit's `--hk-active` wash, each in
    150ms, the kit's ease,
    with no transition under reduced motion (I-961). An opened question's
    plus turns 45 degrees in the same 150ms.

Nothing else moves. A new motion is added to this list with its duration
and easing, or it does not ship.

## Process

- Judge the assembled page, not a component in isolation: screenshot it at
  1440×900 and 390px, light and dark, and look before calling anything done.
  Check 1280, 1240, 1024, 768 and 360 for the column changes.
- Look at captures at their real size. A full-page capture read after
  being scaled to a fifth hides what a visitor sees at once (a lopsided
  hero got through that way, 2026-09-27). Capture the viewport, or crop
  the region, at 1x and judge that; use a full-page capture only for the
  page's rhythm, never for a section's layout.
