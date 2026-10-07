# Landing page: rules for visuals and copy

The owner's rules for `apps/web/src/routes/+page.svelte` and
`apps/web/src/lib/components/landing/`, collected from their reviews in
September 2026. Read this before touching the landing page; each rule exists
because a version broke it and was sent back. `DESIGN-LANGUAGE.md`,
"Foundation", holds what the landing shares with every page (the tokens
including `--ink*`, the palette, the contrast floor, the faces and the one
mono, the logo); this file covers what the landing page shows and says, and
the grammar it adds.

## What we sell

Your laptop's dev environment, replicated in the cloud in one command,
for solo founders and their agents (DECISIONS I-477). Two ideas, the
first one leading, and every visual serves one of them:

1. `repose run` in a checkout replicates your laptop's dev environment
   (your code with its unpushed and uncommitted work, your tools, your
   logins) on a cloud machine that feels like your own box. Your private
   things stay home.
2. The agent runs there with full permissions; the damage stays on that
   machine, and it comes back.

The reader is one person shipping alone, with agents doing the work. The
page says so once, in the label above the headline.

Not notifications, not answering from your phone, not "check in from
anywhere", not remote control, not always-on, not collaboration, not a
software factory. Don't show or mention them.

## The reference: "Your working state, in one command"

The owner: "that one is beautiful. It's made with anime.js, and the colour
palette, the design, it's beautiful... we should maintain that aesthetic.
It has to be like that." Every other animated visual (the hero, "Break it
and roll it back") matches it: the same light panels with hairline
borders, the same row and chip styling, the same blue accent for what
moves, the same anime.js motion: stagger, travel, settle, rest, loop.
Calm means no move is shorter than 120ms or longer than 1.1s; travel
eases in and out (`inOutCubic`), a reveal eases out (`outQuad`,
`outCubic`) and an exit eases in (`inQuad`); only a chip or a mark
arriving overshoots (`outBack`); and nothing moves during the rest before
the loop starts again.
Frames of it for reference: record them from the live page before starting.

## Less is more

- Show only what carries the point. A list of fifteen files where two
  matter is noise: the viewer can't find the important ones fast. Show the
  few things that matter (a small group reads instantly, like `.ssh`
  and a cat photo), and cut the rest. What must stay for context but does
  not matter is muted: its text in `--ink-faint`, the faintest grey that
  still holds 4.5:1, its icon in the same grey, never a colour.
- Transfers are quick: one folder, or at most three items, moving across;
  not every file.

## Show, don't tell

- The picture carries the explanation. A visitor should get the point in two
  seconds without reading a word.
- No captions or labels that explain the picture ("copied by repose run",
  "not sent", "installed here, for Linux", "never reach the machine",
  "outbound", "another project"). Say it with the visual itself: mute,
  strike, cross out, fade, animate, move.
- No hand-holding text and no walls of text. Headline, one short sentence,
  the picture. Explanatory paragraphs go (the pricing notes went).
- A box that holds only small plain text is not a visual. Give it a mark,
  an icon, real content, or remove it.

## Real, and whole, or not at all

- Real apps and real projects, captured from real runs. No invented sample
  apps (`todo-app`), no drawn fake UIs standing in for real ones.
- A partial imitation of a real tool is worse than none. Either show the
  real thing in full (a real capture of Claude Code, a real LazyVim screen)
  or use a different representation (a mark, a diagram).
- Show tools the way people actually use them: Neovim means a real,
  popular config (LazyVim), not bare stock Neovim.
- No fake window chrome (traffic-light dots, close/minimise/maximise) unless
  it adds meaning. A full-screen app needs no frame.
- Every string in a visual is true: from a capture, the docs, or the CLI
  source. Crop, never retype or invent.

## Names a stranger understands

- A visitor doesn't know what `recruiting` or `wira` is, or that it's a
  repose project. Never show a bare internal name as if it explains itself;
  let the picture say what it is (your repo, your app, your machine).
- Don't repeat the same name (`recruiting` … `/home/dev/recruiting`).
- No product jargon without meaning (`large`, a list like
  `large · sudo · docker` after a name).
- "Project" is ambiguous (a production deployment? the repo on your
  laptop?). Avoid the word in visuals, or make its meaning obvious from the
  picture.
- Every cross, arrow or block must show what it prevents or allows; a red
  cross on "another project" means nothing if the viewer can't tell what
  that is.

## The hero (owner's direction, 2026-09-26)

- Same aesthetic as the working-state section, built with anime.js.
- Don't force a laptop drawing on the left; the form factor didn't work.
- The stakes must be obvious (what could be lost), and snapshots must make
  sense without text: the viewer sees a snapshot being taken and the
  machine coming back from it.
- The poisoned thing is "malicious skill", not "npx malicious-skill".
- No `passwords.csv`: nobody has one, it reads as silly. Private items are
  things people really have (`.ssh`, a cat photo, tax documents).
- Order of the story: the agent first does real, good work (file rows with
  green `+` and red `-` line counts, the way a diff stat looks), running in
  `--dangerously-skip-permissions` mode; snapshots are taken along the way
  (at least two); only then does it go out to the internet and the
  malicious skill comes in. The skill is not on screen from the start. The
  wreck follows, and the machine comes back from the most recent snapshot,
  good work included.
- Show the agent's permission mode the way Claude Code itself does: the
  pink `⏵⏵ bypass permissions on` line at the bottom left of the machine,
  not a `--dangerously-skip-permissions` flag chip.
- No connector lines from the agent to the files it edits; the edits show
  on the file rows themselves (counts appear, rows change).
- Anything that moves exists once: the malicious skill travels from the
  internet (globe) into the machine; no second, faded copy left behind.
- Snapshots are small. The newest shows its time; older ones stack behind
  it as just an icon and a timestamp, contents hidden.
- No chapter labels ("repose run", "The agent wrecks it", "A snapshot puts
  it back"). The three beats must be understood from the motion alone.
- The laptop is full, not empty: several things live on it (your repo, and
  the private rest of your life: SSH keys, tax documents, cute cat photos).
  The repo visibly opens and its contents copy into the cloud machine; the
  private things stay behind.
- The agent is Claude Code's mascot, orange, turning red when it goes
  rogue: it deletes files on the machine, then reaches out along a visible
  path to the internet, picks up something poisoned (a prompt injection, a
  malicious `npx` skill or package, in the spirit of the Shai-Hulud npm
  worm), and that tries to reach your private files on the laptop and is
  stopped at the machine's wall. Then a snapshot puts the machine back.
- Truth limit: the machine's own contents (the checkout, its `.env`, the
  logins copied to it) are reachable by anything running there
  (`apps/web/src/content/docs/secrets.md`, "What an agent on the machine can
  reach"). Show the attack failing against the laptop; never imply the
  machine's own secrets are out of reach. Don't use a real package name for
  the malicious one.

## "Your working state, in one command"

Titled "Your laptop's setup, in one command" since I-477, with one
sentence for what the picture does not show: the globally installed CLIs,
the copied logins, and the SSH keys staying home. This file keeps the old
name for the section (`OneCommand.svelte`).

The owner chose the animated version (anime.js): the laptop's commits and
changed files copy across into the cloud machine panel when `repose run`
fires; `node_modules/` stays behind, struck. Keep it animated.

## Copy

- No hand-holding (owner, 2026-09-27): a section head is its title, with
  a sentence only where the picture cannot carry a fact (pricing's rule).
  A feature card gets one line of facts the picture does not show, no
  explanation of the picture. A step is its title and its command. The
  hero's lead is three short sentences: the machine, the wreck, the
  snapshot. Since I-477 the headline is "Your dev environment,
  replicated in the cloud", the label above it "For solo founders and
  their agents", and "full permissions" sits in the lead's second
  sentence.

- Never write as if the product were Claude-only: "the agent" drives the
  browser, reads the console; not "Claude Code does X".
- No empty phrasing that sounds generated ("tests in a real browser": as
  opposed to what?). Say what the viewer gains.

## The grid cards (owner's notes, 2026-09-26)

- Browser: show the agent's browser-tool log lines and what the browser is
  doing at the same time; the point is that the agent drives the browser
  and reads the console, and you can watch from your laptop and take over
  to nudge it.
- Localhost: keep the tmux green status bar with the forwarded ports and
  the `localhost:5173` address bar; make it obvious that what you see on
  your laptop is forwarded from the cloud machine. The job listings
  (Boeing, RTX) don't fit the page's vibe.
- Editor: the explorer sidebar must be narrow relative to the code.
- Snapshots are about the machine, not the source. Git already brings back
  deleted source files; a snapshot is the whole disk (the root overlay's
  writable layer and /home, DESIGN.md §6): databases and Docker volumes,
  installed tools and PATH, logins made on the machine, uncommitted work.
  Show damage git can't undo, and the machine back in minutes (a restore
  took about two minutes in real runs; never claim "a minute"). The hero's
  wreck and the snapshot card tell this same story.
- Order of "On every machine": snapshots, localhost, the agent's browser,
  your editor.
- "Break it and roll it back": same aesthetic as the working-state section,
  animated with anime.js. At least two snapshots: one is taken, some edits
  happen, a second is taken and stacks on top, shown only as an icon and a
  timestamp with its contents hidden.
- "Five agents and a full toolchain on first boot" is super clean; leave it.
  2026-09-27: the agents' row shows the marks alone, no name or command
  under them.

## Shape language (owner's direction, 2026-09-27)

The owner brought in a slide template's look (flat geometric shapes in
saturated colours, heavy headings over a thick bar) and asked for it on top
of the house style, not in place of it. Two attempts were sent back: one let
the shapes take over the page, the next added them at the top and bottom
and underlined every heading. The page is one system:

- **Every section is built the same way**: a header (the bold serif
  heading, with a sentence only where the picture cannot carry a fact; see
  "Copy") and then its picture on a stage, the sunken hairline panel
  the grid cards already use. The hero picture and "Your working state"
  sit on a stage too, which keeps the headline apart from the picture.
- **A shape encodes something, or it isn't there.** Owner, 2026-09-27:
  shapes placed "just to have shapes" read as gimmicks, however well they
  are coloured or anchored. The model is Isotype: a shape stands for a
  thing, and its fill or count carries a quantity. Two shapes are
  measures:
  - *Progress* (`Gauge.svelte`): each of the three steps is a circle
    filled a third, two thirds, then whole; grey, and the blue accent for
    the last, "done".
  - *Capacity* (`Units.svelte`): each pricing card counts the plan's
    memory in small squares, one per GB (8, 16, 32, in rows
    of eight), Isotype's own form for a quantity, so the plans compare at
    a glance.
  The rest each name one feature and appear where that feature is, so
  the footer's row is the page's own symbols and none "spawns from
  heaven" (owner, 2026-09-27). The mapping, in page order:
  - *pinwheel* (two quarters of a circle: the state before and after) is
    a snapshot. It is the snapshot mark in the hero's snapshots panel and
    on each miniature, in the "Let it break" card, and before that card's
    title.
  - *sphere* is the internet, in the hero, with a globe's meridians drawn
    over it in paper.
  - *pill* is the sync, before "Your working state, in one command".
  - *halves* (the same thing above and below) is the localhost forward,
    before "Your dev server on your localhost".
  - *ring* (a lens: one disc, a paper ring, an ink pupil; the quartered
    ring was redrawn 2026-09-27, its four tones did not work small) is
    watching the agent's browser, before "Watch the agent use the
    browser".
  - *arch* (a door in) is the editor over SSH, before "Open it in your
    editor".
  - *asterisk* (a wildcard) is the toolchain and anything installable,
    before "Five agents and a full toolchain on first boot".
  Sun, moon and leaf name nothing on the page and are not shown. A shape
  on a head or a card title sits inline before the words, one em tall, so
  it matches the title's letters (`.head-mark`, `.cell-mark`; owner,
  2026-09-27). The hero's headline carries none.
  A column of agent logos beside the headline was tried and dropped
  (owner, 2026-09-27): a list of logos is a gimmick, and repose is a
  machine for any work, not only AI.
  The star (Gemini's sparkle) was dropped (owner, 2026-10-05, I-498): it
  read as Gemini's logo, not as repose's. Gemini CLI keeps its own mark in
  the toolchain box.
  The footer's row is these seven, in the order the page used them, one
  to a cell between the rails, no pie (it would read as a gauge).
- **Anchored, never floating.** The gauges and counts sit in their line
  or card, the footer's shapes stand on its rule.
- **The blue bar marks the prices, nothing else.** It marked "full
  permissions" until I-477 and "replicated" in the headline until I-498.
  The headline and section headings are bold serif with no bar.
- **Shapes move one way**: a group lands in place when it comes into
  view. The one other shape motion carries meaning (owner, 2026-09-27):
  the snapshot mark clicks a quarter turn when a snapshot is taken and
  rewinds a full turn when one is restored, in the hero and in the "Let it
  break" card. No labels on any of it. Every motion on the page is listed
  under "Motion" below; under `prefers-reduced-motion` every shape is
  still and whole.
- **The palette the landing had before the shapes, and nothing else, and
  no agent's brand colour.** The shapes, the bar and the price rules use
  the neutrals and the one blue accent (`--sh-*` in `layout.css`; blue-500
  for fills, blue-400 in the dark). No Claude Code orange (owner,
  2026-09-27): the landing's own design is not any AI vendor's. No green,
  amber, pink or purple either; the slide template's hues were tried and
  dropped, and a new colour needs a reason recorded here. This rule is for
  what the landing draws for itself. A picture of a real tool keeps that
  tool's colours, because it shows what you will see: Claude Code's orange
  mascot and pink bypass line (above, "The hero"), a terminal's ANSI
  colours, an editor's theme. Those stay inside the picture's frame
  (DESIGN-LANGUAGE.md, "Palette"; DECISIONS I-392).
- **Mostly grey, a spot of colour, even weight.** Each shape has one
  main tone (`tone`: neutral or accent; Shape.svelte); a group carries a
  spot of blue and the rest grey, as the pictures are mostly grey with a
  blue chip. Fills sit
  at mid values so nothing vanishes or shouts: zinc-400 and zinc-300 on
  paper, zinc-500 and zinc-600 in the dark; the blue fill is blue-500
  (a step lighter than the pictures' blue-600 lines) so it doesn't
  outweigh the greys; `--sh-ink` only for small details (a hole, a
  diamond's top), never a whole shape. In the light scheme those details
  are holes in zinc-700; in the dark they are lit marks in zinc-400, since
  a dark detail on the dark paper ring vanished (DECISIONS I-379). Judge
  the balance on viewport captures at 1x in both themes, one per section,
  as "Process" says; a full-page capture only shows the page's rhythm.
- **The logo** (`Logo.svelte`, and `static/favicon.png` from the same
  drawing) is the owner's notebook sketch, traced (DECISIONS I-363): a
  thin cross in the text's ink, its crossing left of centre and low, and
  three flat blocks hugging the crossing: a skinny one in grey above-left,
  a middle square in a fainter grey above-right on the arm, and the big
  square in `--sh-accent` below-right. The two greys are the mark's own,
  not `--sh-grey` and `--sh-light`, which vanish at header size; their
  values are in `DESIGN-LANGUAGE.md`, "Logo" (I-393). The favicon
  (`favicon.svg`, `favicon.png`) is a heavier cut of it, stems 10 instead
  of 4, so it holds at 16px. It replaced the r (a stem and a blue quarter
  disc), which had replaced the quartered ring. The mark is in every
  page's header, not only this one's (I-381); where it appears and its
  minimum size are in `DESIGN-LANGUAGE.md`, "Logo".
- The shapes live in `landing/Shape.svelte` and draw only from the `--sh-*`
  tokens. The app's own pages never use them, with one exception: the
  logo's big square is `--sh-accent`, and the logo is in every header
  (I-363, I-381, I-393).

## Since the owner's notes (implementation, awaiting the owner)

Changes made in the 2026-10-01 repair round on top of the owner's
sections above, recorded here and not in them, since the owner has not
reviewed them yet (STATUS.md). Each holds until the owner says otherwise.

- **"Your working state", the figures** (I-398). The machine shows no
  `node_modules/`, since sync leaves dependency directories behind
  (`content/docs/sync.md`), and the chip reads "Ready in 14s", the docs
  quickstart's figure: since I-367 `repose run` syncs only into a new
  machine, so the captured "Ready in 0.6s" of a re-sync no longer happens.
  The picture is that first run: the machine starts with no commit, and
  every commit travels into it, master's `7ea43a8` with the branch's two
  (I-400). On a phone, where the panels stack, the copies travel only through the
  gap between them.
- **The snapshot card's logins** (I-398). The Claude Code login lives on
  the user's login share, which a snapshot does not hold (I-278), so the
  card's logins row is gh and Codex.
- **The drawn pictures have one palette**, beside the shapes' `--sh-*` in
  `layout.css` (I-397). The pictures the page draws for itself (the hero,
  "Your working state", "Break it and roll it back", and the rows, chips
  and wires around the captures) take their lines, text and marks from
  six `--pic-*` tokens, and none picks its own step: `--pic-accent`
  (blue-600, blue-400 in the dark) for what moves and the chips;
  `--pic-ink` (zinc-800, zinc-200) for a row's name; `--pic-dim`
  (zinc-500, zinc-400) for a line, an icon or a letter at rest;
  `--pic-faint`, which is `--ink-faint` itself, for the muted rows "Less
  is more" asks for; `--pic-stop` (red-600, red-400) for what is blocked
  or deleted; `--pic-add` (emerald-600, emerald-400) for a line count
  added or a new file, and nothing else: a row "Break it and roll it
  back" restores takes the accent's tint and tick, not green (I-400).
  The last two are a diff stat's own red and green,
  the hues a drawn picture uses besides the blue; amber, a source
  control panel's colour for a changed file, is not one of them
  (OneCommand's `M` is `--pic-dim`). Text beside a picture, like
  OneCommand's "Ready in 14s", is the Foundation's `--ink-muted`. A mark
  laid over a capture follows the capture's ground, not the page's
  scheme: the Browser picture's ring, on the white page, is blue-600 in
  both schemes (`--chip`), and the bar on its dark log is blue-400
  (`--on-log`). The green running dot in the drawn title bars and
  beside OneCommand's "Ready" (emerald-500) is the one hue still open,
  waiting on the owner (STATUS.md). The captures keep their tool's
  colours and do not use these.
- **The drawn pictures' rows are 12px to 12.5px mono** (I-399): a file
  name, a diff stat, a time, a git letter, as "Your working state" drew
  them when the owner chose it, and 11px on a phone. They are a
  picture's labels, drawn at the picture's scale; the Foundation's 13px
  mono rule is for the page's own text, and the 11px floor holds.
- **The sphere is flat** (I-398), like every other shape: the noise and
  radial-gradient texture it had is gone. The footer's sphere carries the
  hero globe's meridians in paper (`meridians` on `Shape.svelte`), so the
  two are one drawing, as "Shape language" describes the hero's.
- **The grid cards' lines** (I-397) say what the product does, in the
  docs' terms: Localhost's is "Ports from 1024 up, while `repose run` is
  open. Cookies and OAuth redirects behave as they do locally." (it was
  "Every port the machine listens on, on your laptop. Cookies and OAuth
  redirects included.", and ports below 1024 are not forwarded,
  `content/docs/machine.md`; I-401), and Browser's is "`repose browser` shows
  the agent's Chromium on your laptop; click in it to take over" (it was
  "puts you in the same window. Take over any time."). "Back in minutes"
  is kept whole on one line.
- **Every step's command has a Copy button** (I-398), as the install
  command beside the hero's button does. "Copy" says a step is its title
  and its command; the button is the command row's, not a third part.
  The last step shows `cd ~/code/job-alerts && repose run` and its button
  copies `repose run` alone, since the path is an example that a pasted
  command would fail on (I-401).
- **Pricing's sentence and spec line** (I-397, recorded in I-401). The
  head sentence is "Seven days free, card at checkout. Prices in USD,
  before tax. A plan's memory is shared by the machines you have
  running; a stopped machine uses none." (it was "Three plans. Seven
  days free, card at checkout."): the three cards show there are three,
  and the currency, tax and how memory is counted are
  `docs/PRICING.md`'s, facts the cards do not show. Each card's spec
  reads "N GB of memory" (it was "N GB running at once", which did not
  say it was memory), naming what the units count (I-402).
- **The meta description** (I-397, recorded in I-401): "A cloud dev
  machine for your repo in one command, with your code, tools and logins
  on it, so coding agents can run with full permissions and your laptop
  stays out of reach." It was "A disposable dev machine per project with
  your code, tools and secrets on it in 15 seconds, ...": the machine
  is persistent, not disposable, and 15 seconds was not the docs
  quickstart's 14s (I-398).
- **Ready drops the first line of each two-line prompt** (I-398), as
  whole rows, so the capture does not show the machine's old name
  (`recruiting`); nothing is retyped. On a phone Ready's rows crop on the
  right instead of re-flowing, as a terminal would, and the hint's last
  words ("install it on this machine", "keep it on every rebuild") are
  cut: open for the owner (STATUS.md).
- **The pricing cards count no agents** (I-402, reversing I-397's
  caption): "One agent at a time", "Two agents at once" read as a
  ceiling on the product, so the cards have no caption. Memory, disk and
  egress are the plan; the head sentence says the memory is shared by
  whatever is running.
- **The hero's laptop panel hugs its rows** (I-398): from 768 wide it
  ends under its last row (y=748 at 1440) instead of stretching to the
  machine's height (y=867). Before, both panels ended at y=870.
- **The hero's lead keeps each sentence whole** (I-400): the line
  breaks between sentences, never after a sentence's first word. A
  sentence wider than the column wraps inside its own box (I-401), so
  320px with WCAG 1.4.12 text spacing does not scroll sideways.

## Motion

Everything that moves on the landing. Each runs only under
`prefers-reduced-motion: no-preference`, except the two colour changes at
the end of the list (a class change's colour, hover and press), which are
plain CSS transitions and move nothing; with reduced motion the page is
drawn in its final state, so no class changes in a picture either. The setting is followed while the page is open:
a visitor who turns reduced motion on mid-loop sees every picture stop on
its final frame and every waiting shape group land at once
(`watchReducedMotion` in `landing/inview.ts`, I-397). The hero's still
frame, with reduced motion and with scripts off, is its end state: the
machine restored with the good work on it, and the stop cross with the lit
wall where the skill was turned back; the skill chip itself is not in it,
since inside a restored machine it would read as still infected. With
scripts on and motion allowed, the first paint is the empty machine the
loop starts from, drawn in CSS under `html.js` (set by `app.html`'s one
inline script), so the wreck never flashes before the story (I-398). If
the app's script never mounts the picture (a bundle that fails to load
or throws), the still frame replaces the empty machine after 4s, a cut
with no motion (I-400). The connectors, the newest snapshot's arrow back
into the machine among them, are measured by the script, so the no-JS
frame has none.

The pictures move only by `translate`, `scale`, `opacity`, `clip-path`,
colour and a wire's `stroke-dashoffset` (the hero's connectors draw
along their length): nothing they animate changes layout, so a loop adds
no layout shift (the Browser picture's ring is four edges scaled to size,
and the snapshot tile folds by a clip, its box keeping its height). The
hero's snapshot slot is server-rendered at the size the script measures
at 1280 to 1920 wide, so hydration does not resize it either (I-399).

- **The chrome, once on load**: the rails draw from the top down (1.1s,
  `--land-ease`, `cubic-bezier(0.65, 0, 0.35, 1)`); the ticks fade in
  after them (0.4s ease-out, from 0.9s). The prices' bar does not move.
- **Shapes landing**: a group lands in place when it comes into view, one
  shape after another (`.land` in `layout.css`: opacity 0.3s ease-out,
  transform 0.7s `cubic-bezier(0.34, 1.56, 0.64, 1)`, a small overshoot,
  each shape delayed by its `--d`).
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
    The Localhost picture's URL in the address bar is one of these, a
    300ms `outQuad` fade. A row that drops in as it appears (the hero's
    work rows) takes 320ms `outCubic`, opacity with a 6px drop.
  - *Strikes and wires drawing*: 260ms to 380ms, `outCubic`.
  - *Exits* (a loop's last frame fading before the rest, a wire or a
    mark going once its part is played): 300ms to 700ms, `inQuad`. Every
    fade to 0 in the pictures names `inQuad` (I-400).
  - *Dimming to rest*: the Browser picture's older log rows fall to 0.7
    as a newer call lands, 400ms or 500ms, `outQuad`.
  - *A file being written* in the hero: the row's light rises and falls
    once in 1s, `inOutCubic`, as its diff stat pops in.
  - *Arrivals* (a chip, a cross, a tick landing): 250ms to 380ms,
    `outBack`, the one overshoot.
  - *The agent's bob* in the hero, while it works: two 2px lifts in
    1.1s, `inOutCubic`.
  - *The rogue agent's shake* in the hero, when it turns red: 2px either
    way in 480ms, four moves of 120ms, `inOutCubic`.
  - *The crosses' pulse* in "Break it and roll it back", on what git
    cannot bring back: each grows to 1.35 and back in 420ms, `inOutCubic`,
    60ms apart.
  - *The snapshot tile's fold* in "Break it and roll it back": the older
    tile clips up to its head in 550ms `inOutCubic` as the newer one lands.
  - *Struck names greying* in the hero's wreck: a 200ms ease-out colour
    transition in CSS, to `--pic-faint`.
  - *The snapshot mark* is above: a quarter turn in 420ms `outQuad`, a
    full turn back in 1s `inOutCubic`.
  - *The shutter* in the hero, as a snapshot is taken: an accent wash
    over the machine rises to 0.9 and clears in 460ms, `outQuad`.
  - *The knock* in the hero: the malicious skill bounces 7px back off the
    wall and returns, 380ms `outQuad`, as the stop cross lands.
  - *The pointer's press* in the Browser picture: it scales to 0.86 and
    back in 220ms, `outQuad`, as your click lands.
  - *Colour as a class changes* (CSS transitions, the CSS `ease` unless
    named): the `repose run` chip filling as it fires, 180ms (Hero,
    OneCommand, Localhost, Browser); the Localhost address bar's edge
    turning blue as the URL is typed, 180ms; the Browser log's bar on the
    acting call, 300ms opacity; in "Break it and roll it back", the
    machine's and a tile's edge turning blue (300ms), a row's tint (250ms),
    a value's colour (250ms), git's mark and the snapshot mark (200ms) and
    the older tile's time greying (400ms).
  - *Hover and press* on the page's links and buttons: the Foundation's
    150ms colour change (`DESIGN-LANGUAGE.md`, "Motion").

Nothing else moves. A new motion is added to this list with its duration
and easing, or it does not ship.

## Where terminals are allowed

Terminal UIs appear only in the "On every machine" grid and in "Five agents
and a full toolchain on first boot". Everywhere else (hero, "Your working
state", pricing): realistic GUI or clean illustration, no terminal windows,
no CLI output blocks.

## Pricing

The size table and the call to action. No dashboard screenshot, no notes
paragraphs.

## Process

- Judge the assembled page, not a component in isolation: screenshot it at
  1440×900 and 390px, light and dark, and look before calling anything done.
- Look at captures at their real size. A full-page capture read after
  being scaled to a fifth hides what a visitor sees at once (a lopsided
  hero got through that way, 2026-09-27). Capture the viewport, or crop
  the region, at 1x and judge that; use a full-page capture only for the
  page's rhythm, never for a section's layout.
- When motion could help, it's fine to offer an animated and a static
  version for the owner to choose (anime.js is allowed).

## The page's grammar (design system, 2026-09-27)

The pictures draw a machine as a panel with hairline edges, and the hero
shows those edges as the wall the attack stops at. The page takes that
drawing as its own grammar, in `apps/web/src/routes/landing.css` (imported
by `+page.svelte` alone; the house tokens stay in `layout.css`):

- **Rails.** The content stands between two hairlines (`.rails`) that run
  from the top bar to the footer, 1120px apart at most. They are hidden
  below 768px. On load they draw themselves from the top down, once, and
  the ticks fade in after them ("Motion").
- **Rules run wall to wall, ticked.** Every section (`.sec`) opens with a
  rule across the full width, and a small cross (`::before`/`::after`)
  marks where it meets each rail, as a drawing marks an intersection. The
  top bar's rule is ticked the same way.
- **A section is a head, then a stage.** The head (`SectionHead.svelte`)
  is the bold serif title, and a sentence only where the picture cannot
  carry a fact ("Copy"), inset from the rails by
  `--land-x`; no number and no running label (a "01 Run" label was tried
  and dropped, owner, 2026-09-27: generic). The stage (`.landing-stage`)
  is sunken and fills the width between the rails, so a picture reads as
  a bay inside the walls. The hero's picture sits on a stage the same way.
- **Cells, not cards.** The features (`.cells`/`.cell`), the steps
  (`.step`) and the sizes (`.tier`) are cut by the same hairlines, and the
  dividers cross the full width. Each feature picture is cropped to one
  `.shot` frame; the title and sentence under it belong to the page, not to
  the picture's component.
- **The hero.** One stack on the left edge: the headline, the lead under
  it, then the button and the install command on one row. Nothing is
  pushed to the right; the picture fills the width. The headline has
  no bar (I-498). The picture (owner,
  2026-09-27) is your laptop, your cloud machine, and on the right a
  stack: the snapshots panel (titled, the miniatures shrink into it) over
  the internet as a bare 52px globe, no window, since the internet is not
  a machine of anyone's. No snapshot hangs below the machine and the
  globe does not float at the edge. The connectors cross the gap between
  the machine and that stack. The rogue agent is the red mark alone, no
  halo, background or border around it.
- **Commands are rows.** The install command and each step's command are
  one `.cmd`: a mono row on a sunken ground with a hairline, the way a
  picture shows a row of a terminal.
- **The sign-off.** The footer's shape set stands one to a cell between
  the rails (`.frieze`), the rule under it, then the wordmark and links.
  Eight cells in one row at every width, a phone included.
- **Type.** Display `clamp(2.75rem, 6.6vw, 5.25rem)`; section titles
  `clamp(1.9rem, 3.4vw, 2.5rem)`; cell and step titles 1.125rem serif
  600; a cell's sentence 1rem (a step has none, "Copy"); lead
  `clamp(1rem, 1.3vw, 1.125rem)`; a command row (`.cmd`) `--text-compact`
  mono; a size's name 2.25rem serif 700 uppercase and its price 2.75rem
  serif 700; labels 11px JetBrains Mono, 0.12em tracking, uppercase (the
  price's "a month"); a drawn picture's rows 12px to 12.5px mono, 11px on
  a phone (I-399). A captured picture draws at the scale that fits the
  capture in its frame, under the 11px floor where it must (the Editor's
  screen at 8.4px on a phone), as it keeps its tool's colours
  (`DESIGN-LANGUAGE.md`, "Type", I-397). Text takes the
  Foundation's three ink steps (`--ink`, `--ink-muted`, `--ink-faint`,
  `DESIGN-LANGUAGE.md`, "Tokens"), set in `layout.css` so they apply
  before `landing.css` arrives (I-331).

Everything in "Shape language" still holds: shapes encode or are absent,
the palette is the neutrals and the one blue, the pictures are untouched.

