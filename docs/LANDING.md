# Landing page: rules for visuals and copy

The owner's rules for `apps/web/src/routes/+page.svelte`,
`apps/web/src/routes/landing.css` and `apps/web/src/lib/components/landing/`,
collected from their reviews since September 2026. Read this before touching
the landing page; each rule exists because a version broke it and was sent
back. The look (tokens, type, buttons, the code pane, the plan cards) is
Herakraft UI, recorded in `DESIGN-LANGUAGE.md` "The landing" and DECISIONS
I-837; this file covers what the landing shows and says.

## What we sell

Your laptop's dev environment, replicated in the cloud in one command,
for solo founders and their agents (DECISIONS I-477). Two ideas, the
first one leading, and every section serves one of them:

1. `repose run` in a checkout replicates your laptop's dev environment
   (your code with its unpushed and uncommitted work, your tools, your
   logins) on a cloud machine that feels like your own box. Your private
   things stay home.
2. The agent runs there with full permissions; the damage stays on that
   machine, and it comes back.

The reader is one person shipping alone, with agents doing the work. The
page says so once, in the eyebrow above the headline.

Don't show or mention notifications, answering from your phone, "check in
from anywhere", remote control, always-on, collaboration or a software
factory.

## The page (I-837)

Guide B 13.1, on the 1200px frame every page shares (I-656), the
headline starting under the logo. In order:

1. **Hero.** Eyebrow "For solo founders and their agents"; the serif
   headline "Your dev environment, replicated in the cloud"; a two-tone
   lead whose first sentence is the claim ("Each project gets a machine
   of its own, with your code, tools and logins on it.") and whose second
   carries "full permissions"; "Get started" and "Copy install command";
   the install command in mono under them, exactly what the button
   copies. On the right, the terminal (below).
2. **"Your laptop's setup, in one command"** (the owner's I-772 heading
   and paragraph), with two steps on hairline rows: Sign in
   (`repose login`) and Run in any checkout (`repose run`). Each row
   shows exactly what its Copy copies, and it runs on a visitor's laptop.
3. **"On every machine"** (`#features`). Four cells: a serif claim, one
   paragraph, and the command that does it in mono with no copy button,
   since a visitor without the CLI cannot run it.
4. **"Agents on first boot".** The agents and their `--agent` values,
   read at build time from the table at the top of `content/docs/agents.md`,
   with "All agents" and "Everything installed" links.
5. **Pricing** (`#pricing`). "Launch prices. Seven days free.",
   `PlanCells` in its public variant (the one drawing of the plans,
   I-663), then "Start a free week" and the seats line from
   `GET /public/seats` ("18 seats left"; the waitlist and seat copy stays
   as it is until the owner decides the 2026-10-17 notes).
6. **Questions.** Three, each answered from `content/docs/index.md`; the
   restart time is read from it at build time.

Two-column sections from 1024px hold their heading in a sticky left
column; below 1024px every section is one column.

No stat strip: repose has no public figure the page can read at render
time and a reader can reproduce. No isometric or "anatomy" figure, flat or
3D, anywhere on the page (owner, 2026-10-04 and 2026-10-19).

## The terminal

`landing/Terminal.svelte` replays the Quickstart's "4. Let it loose": its
two commands and the six lines `repose run` prints, ending on "Ready in
16s." with a still caret. No shell prompt follows: `repose run -p`
attaches you to the agent's tmux window.
`landing/session.ts` reads them from `content/docs/index.md` at build
time, and `Terminal.test.ts` fails when that section changes shape, so
the pane cannot drift from the docs. It has no copy button: its `cd` into
the example folder fails on a visitor's laptop, and the `repose run -p`
after it would then start an agent wherever they are. The pane is as tall
as its lines, with no empty black under the last one.

It plays once, when 40% of it is in view: each line rises 4px and fades in
over 240ms, one every 450ms, and the caret arrives with the last. It
does not loop or blink, so it needs no pause control. Hidden lines keep
their space, so the pane's height never changes. Without scripts, under
reduced motion, and if the bundle never runs (a CSS fallback 5s in),
every line shows. The component decides from the pane, not the clock:
it replays whenever its lines are still hidden at mount, so a client-side
navigation back to `/` replays too.

## Each fact once

The install command shows in the hero alone. `repose run` shows in the
terminal and in the run step; the cells carry other commands. Plan
figures come from `lib/plans.ts`, seat counts from the api; none is typed
into the page.

## Names a stranger understands

- A visitor doesn't know what `recruiting` or `wira` is, or that it's a
  repose project. Never show a bare internal name as if it explains itself.
  The terminal's checkout is `your-project`, the docs' own example.
- No product jargon without meaning (`large`, a list like
  `large · sudo · docker` after a name).
- "Project" is ambiguous (a production deployment? the repo on your
  laptop?). Avoid the word where its meaning is not obvious.

## Real, or not at all

- Every string on the page is true: from the docs, the CLI or the api.
  The terminal shows what `repose run` prints, read from the Quickstart;
  nothing is retyped or invented.
- No invented sample apps (`todo-app`), no drawn fake UIs, no fake window
  chrome (traffic-light dots).
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
    label is not prose and may be two words ("Launch prices. Seven days
    free.").
  - **Punchy closers.** A last line written to be quoted.
  - **No actor.** Prefer a person doing something ("You spend an hour
    setting up a cloud box") over a state of the world.
  - **Em dashes**, filler adverbs, and "every/always/never" as emphasis
    rather than fact.
- Never write as if the product were Claude-only: "the agent" drives the
  browser; not "Claude Code does X".
- A cell's title says what you can do, not what you can see (I-772):
  "Watch the agent's browser, and take over".
- Plan memory is "running at once", never a cap.

## Where terminals are allowed

The hero's terminal is the page's one near-black pane (I-837, amending
the rule that kept terminals out of the hero). The setup steps are light
command rows, so the hero stays the one dark object in the first two
screens.

## Motion

The terminal's replay is the only motion on the page, besides colour
transitions on hover and the questions' plus turning 45 degrees over
150ms. Nothing moves on scroll. Under reduced motion nothing moves at all
(`landing-motion.spec.ts`).

## Process

- Judge the assembled page, not a component in isolation: screenshot it at
  1440×900 and 390px, light and dark, and look before calling anything done.
  Check 1024, 768 and 360 for the column changes.
- Look at captures at their real size. A full-page capture read after
  being scaled to a fifth hides what a visitor sees at once (a lopsided
  hero got through that way, 2026-09-27). Capture the viewport, or crop
  the region, at 1x and judge that; use a full-page capture only for the
  page's rhythm, never for a section's layout.
