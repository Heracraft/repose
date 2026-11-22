# repose landing page: design direction

Source of truth for every council seat and builder. Part A is the owner's
intent, in their words where possible (voice notes and messages of
2026-11-22). `docs/LANDING.md` (copy rules, "Real, or not at all", "Names a
stranger understands") and `docs/DESIGN-LANGUAGE.md` (Herakraft UI) still
bind; where they conflict with Part A, Part A wins.

---

# Part A: owner intent

## A1. Baseline

The current page on branch `landing-redesign` (served at
http://127.0.0.1:5185/). Owner, on the latest version: "3 steps forward, one
step back." Improve it; do not restart it. The earlier production page
(repose.run, a terminal-log hero and ~1,000 words) is what this replaces:
"It's not converting users at all, and it's never going to be given how it
is right now."

## A2. The value, as the owner states it

"You come in, you get sandboxes, you get dev environments that are tailored
to your current dev environment, and you can run agents without caring about
permissions, because if they wreck something you can undo. ... At the core
of it you can also bring your own hardware: sandboxes can be expensive; if
you already run your own hardware, bring it."

"You gotta be so understandable the user doesn't need to parse the home page
to get it. They don't have to think to understand why this project is
amazing. We gotta give it to them right there, right then."

## A3. What the owner wants kept

- Herakraft UI: Noto Serif display headings, Inter UI text, Geist Mono for
  commands, zinc neutrals, the one muted slate-blue accent, the 1200px ruled
  sheet with rails and crosses, light and dark both first-class.
- The scroll-driven quarter wheel on desktop: "imagine a wheel of fortune but
  you only see one quarter of it ... the background is 400 times and you're
  seeing 100% of one corner." "It's gotta be huge and integrate really well
  with the background and just look beautiful, beautiful."
- Subtle green is fine for a status dot, such as the dot beside "your
  sandbox". Nothing more.
- Real product truth: every command, price and requirement matches the
  public docs (`apps/web/src/content/docs/`).

## A4. What the owner rejected, so no one repeats it

- **Telling instead of showing.** "Use visuals and figures to make the thing
  click instantly to the user rather than yapping to them."
- **Things a visitor has to decipher.** A terminal or tmux log in the hero,
  status rows like `large · 8 GB · snap 21:25`: "never ... should not have
  that terminal whatever that they have to make sense of and decipher." Same
  for any card that needs context.
- **Too much information.** "We still suffer the curse of too much
  information, a lot of which will not change the customer's mind." Example
  cut: "Every machine runs Ubuntu 24.04 with all 5 agents, Docker, and any
  apt package."
- **Filler that asks for applause.** "No token markup": "like duh, am I (the
  customer) supposed to applaud you? This is filler." Prefer value such as
  "mix and match subscriptions / inference providers".
- **Repetition.** "We need to mention it once. We don't need to repeat it
  again and again." Example: a card reading "Bring your own / $10/mo / Your
  own hardware" says one thing three times, and the same facts came back in
  the hero, the wheel and pricing.
- **Lost subtlety and elegance.** "Subtlety and elegance is something we
  have definitely lost with these new cards."
- **Off-brand colour.** "Can't sneak in colours that are not brand colours
  like that stupid green." No green or pink pills, badges or side borders on
  cards. "The use of green in general is just very ugly."
- **Dark sections in light mode.** "Some sections look dark in light mode."
- **Gradients and glows.** "The new gradient is shit."
- **"Own hardware" as wording.** "Pure slop. No one writes like that."
- **Notifications as a selling point.** "Close the lid, get pinged" is gone:
  "pinging is a gimmick ... users bring Claude Code which has Remote
  Control, Antigravity which has its own dashboard."
- **Agent names spelled out in the hero.** "If you're signing up for a
  sandbox you recognise them from the icons."
- **A "Full permissions" pill.**
- **Mac-mini-first positioning.** "Any hardware ... anything that can
  virtualise. I just don't want it to sound like a Mac-mini-first product,
  but we gotta mention Mac mini because that's what developers bought."
- **LLM speak.** Wikipedia "Signs of AI writing": no buzzwords, no "not X
  but Y", no triplets, no em dashes, no punchy closers, no hand-holding.
- **A setup flow that skips sign-in.** Adding a machine needs `repose login`
  first.

## A5. Binding directives (each must be visible on the page)

1. Within three seconds a developer who runs coding agents knows: a sandbox
   per repo set up like their laptop; agents with full permissions there and
   an undo when they break it; on repose's cloud or on a Mac mini or Linux
   server they already have.
2. Show, don't tell: every section's point is carried by a figure; copy is
   a title and at most one short line.
3. Each fact appears once on the page.
4. Brand colours only; the one accent is slate blue; green only as a small
   status dot; no gradients; light sections stay light in light mode.
5. Subtle and elegant over loud: fewer borders, fewer boxes, fewer labels.
6. Mac mini is named, never first-and-only; never "own hardware".
7. The desktop wheel is large, quiet, part of the background, and turns
   with scroll.
8. No notifications pitch; no "No token markup"; no agent names in the
   hero; no "Full permissions" pill.

## A6. Platform and scope

- Widths: phone 390, desktop 1440. No page wider than its viewport.
- Themes: light and dark, both first-class.
- In scope: the landing page `/` only (`+page.svelte`, `landing.css`,
  `components/landing/*`, `pictures.css`). Out of scope: /docs, /billing,
  the dashboard, the API, plan names in billing.
- Facts seats can rely on: Solo $39 founding then $49, Plus $89, Pro $169,
  "running at once" memory; a Mac mini or Linux server is $10 a month; 7
  days free; frozen machines still count against a plan, stopped ones do
  not; five agents (Claude Code, Codex, opencode, Gemini CLI, pi); nightly
  snapshots plus your own.

---

# Part B: design system (Herakraft UI, summarised)

- **Type:** Noto Serif 400 for h1/h2 and section titles; Inter for UI and
  body; Geist Mono for commands, paths and times. Sentence case.
- **Colour:** zinc neutrals (light canvas #fafafa, surface #fff, stage
  #f4f4f5, rules #e4e4e7; dark canvas #09090b, surface #18181b, rules
  #26262a). Accent slate blue #4b6496 light / #93a7d0 dark: commands,
  links, the one active state, the figures' single highlight. Ink for the
  primary button. Red only for a wreck in the safety figure. Green only as
  a 6px status dot.
- **Shape:** 6px radius on windows and chips, 8px on larger panels, 1px
  hairlines; no shadows beyond a hairline; no gradients.
- **Layout:** the 1200px sheet, 12 columns, rails and crosses; sections
  numbered 01-04 with the mono label.
- **Motion:** only the wheel's scroll rotation and the safety figure's
  wreck-and-restore; none under reduced motion.
