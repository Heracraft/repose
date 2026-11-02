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

## The page (I-837, I-843)

Guide B 13.1, on the 1200px frame every page shares (I-656), the
headline starting under the logo, laid out as one ruled sheet on a
12-column grid (I-961, option D; see "The sheet" below). In order:

1. **Hero.** Eyebrow "For solo founders and their agents" in the page's
   one label (12px Geist Mono, upper case, tracked 0.08em; the step
   numbers take it too, and nothing else on the page is numbered); the
   serif headline "Your dev environment, replicated in the cloud"; main's
   lead ("Your code, tools and logins sit on a machine of its own, ...").
   From 1200px the headline (seven columns) and the lead (five) stand
   side by side on one baseline, and "Get started", "Copy install
   command" and the install line share one row under both; from 1024 to
   1199px the headline runs across, and the buttons stand in the right
   half with the install line on the lead's last line. The install line
   is mono, exactly what the button copies, on one line, scrolling under
   a fade where its column is narrower. Under the copy, the hero picture
   (`landing/Hero.svelte`) on the field: the one stage that bleeds to the
   window's edges, a dark plate in both schemes (in the dark scheme a
   step lighter than the page, with a lit top edge) carrying a 16px dot
   grid that fades with its sheen, the windows on the frame. The picture
   draws in its dark palette there. It is the page's one moment of
   contrast; the rest of the page stays on the page's tones. The agent wrecks the
   cloud machine and a snapshot restores it (I-843, I-961). The still
   frame (reduced motion, no script) has no globe, since it has no skill
   to come from it; in the loop the globe is drawn in line.
2. **Setup. "Your laptop's setup, in one command"** (the owner's I-772
   heading and paragraph, the paragraph on the heading's last line), then
   the OneCommand picture on its field: `repose run` copying the laptop's
   source control, changes and ignored files to the machine.
3. **Machine. "Work on the machine as you would on your laptop"**
   (`#features`, I-961), with "Each project gets a machine of its own,
   and you reach it from the tools already on your laptop." on the
   heading's last line. Four cells, each its picture (ComesBack,
   Localhost, Browser, Editor) over a serif claim, one paragraph with a
   fact the picture cannot carry, and
   the command the picture shows, under the paragraph, set as the hero's
   install line is (13px mono, the second grey) with no `$` and no copy
   button, since a visitor without the CLI cannot run it: the page has
   two command treatments, runnable (the steps, with Copy) and reference
   (the hero's line, the cells). The cells are flush on the sheet, split
   by inner rules with no gap: from 1024px a 2x2 whose centre hairline
   stands half a gap left of the column line, so the right cells' text
   starts on it, and runs unbroken from the head's foot to the next
   band's rule. Below 1024px they are one column; a cell and its stage
   run the window's width, and the picture starts on the text line and
   stops at 34rem. The snapshot cell says what a snapshot holds and that
   `--as` restores it to a new machine, which the hero does not show; the
   Browser cell says what `repose browser` is for (watching, a login or a
   captcha), and the bridge stays in the docs.
4. **Agents. "The agents you use, and the tools they need, on first
   boot"**, with "All agents" and "Everything installed" (grey, with the
   kit's arrow) on the heading's last line in the right half (under it
   below 1024px), then the Ready picture, from rail to rail: the five
   agents' marks alone (I-287) on the stage's tone, each naming itself
   under the pointer; an inner rule; then the tools on the first third,
   on the stage's tone too, the everyday tools' line under them, and the
   machine's shell flush on the other two thirds, on the terminals'
   ground.
5. **Install. "Start in three commands"**, with "Install and sign in"
   and "Run and attach" (the docs pages, as the Agents links) on the
   heading's last line, then ruled rows (I-961): the
   step's number (the page's label) and serif name in the left half, its
   command filling the right half flush on the stage's tone from the
   centre hairline to the rail, Copy at the cell's right end. On a phone
   the number and name share a baseline over the command's cell, which
   runs the window's width under its own rule. Install the CLI, Sign in
   (`repose login`) and Run in any checkout (`repose run`). Each row
   shows exactly what its Copy copies, with no `$`, on one line at every
   width (under 640px the install line scrolls under the copy box's
   fade), and it runs on a visitor's laptop.
6. **Pricing. "Launch prices"** (`#pricing`). On the heading's last
   line, "Start a free week" and the seats line beside it from
   `GET /public/seats` ("18 seats left"; the waitlist and seat copy stays
   as it is until the owner decides the 2026-10-17 notes). The heading
   says the launch prices and the button the free week, so no line
   repeats either (I-961). Under the head, `PlanCells` in its public
   variant (the one drawing of the plans, I-663), as flush cells split by
   inner rules with no card edge: three across on the thirds lines from
   1024px, stacked below.
7. **Questions. "Before you try it"**, with "Limits" and "Billing" on
   the heading's last line. Five a solo founder asks before trying it,
   each one a reason to: why not skip permissions on the laptop (what the
   agent can reach there and here), closing the laptop while the agent
   works (it keeps working; you attach again; no notifications, per "What
   we sell"), how long snapshots are kept (seven days and the newest, and
   when repose takes them: the wreck itself is the hero's), three agents
   on three approaches (`repose fork`), and why not set up a server
   yourself. Every answer is from the docs (secrets, lifecycle,
   sync). No setup trivia (where a
   login lives, Windows, how commits come back, what a stopped machine
   costs): those belong in the docs and give a visitor no reason to try
   it (owner, 2026-10-21).

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

Every band after the hero opens with the same head: the h2, and from
1024px the band's one side item in the right half on the h2's last
baseline (under the h2 below 1024px). Every band has one: a paragraph
(setup, machine), links to the docs (agents, install, questions) or the
button (pricing). The head's foot is an inner rule across the sheet. No
band carries a numbered label: no sentence cites one, and the h2 and
the header's links already name each band.

Two rule weights: band rules and rails in `--hk-border`, every rule
inside a band in `--hk-hairline`. The page has no gap between bands. Each
band after the hero opens with a rule the window's width and ends on the
next one: a band that ends on a picture or a ruled list (the steps, the
plans, the questions) has no bottom padding, and its last stage or row
ends on that rule; the questions end on the footer's. Empty space is
padding inside a band, never a strip between two.

No stat strip: repose has no public figure the page can read at render
time and a reader can reproduce. No isometric or "anatomy" figure, flat or
3D, anywhere on the page (owner, 2026-10-04 and 2026-10-19).

## The pictures (I-843)

The drawn pictures are main's (`components/landing`: Hero, OneCommand,
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
or number over it. The stage is the cell itself, flush from rule to
rule, with no edge or radius of its own; its windows stand in it closed,
on the text lines at the sides. A band picture (the hero, the setup)
stands in a field `--field-y` deep above and below its windows (half a
band, never under 48px), and ends on the next band's rule. A grid cell's
stage is 20rem tall, ruled at its foot, its windows 24px (20 from 640 to
1023, 12 on a phone) from its top and foot, so no window is cut at the
stage's edge: the Editor's statusline keeps its right segment whole and
the Browser's agent log shows whole. The four grid pictures share one
type scale: window chrome and terminals at 12px to 13px mono, 11px on a
phone, and the Browser's captured page at 13px body type: one capture
pixel is drawn 0.72px, the page stands at the left of its window on the
app's ground, and the toolbar's address bar is drawn stretched to the
window's width. A capture that is wider than its window (the Editor's
78 columns) is cropped on the right into a fade, as a terminal is, never
shrunk to fit; a captured page is cropped in its white space, never
inside a sentence, at every beat of its loop. On a phone the Localhost
and Browser captures keep a size their text reads at and crop on the
right into a fade as well; the Browser's own scrollbar is cut off. The
Localhost app page ends under its nav's rule: drawn at its window's
width, the headline under it outsized the cell's title, and the ground
above the headline read as a page still loading; the machine's window
shows Vite's ready line over the Local one. ComesBack's snapshots stand
in a window as tall as their two tiles, on the machine's top line; on a
phone both tiles stand whole at every beat. The
Browser and the Editor have no window head of their own, since their
captured toolbar and nvim's tab bar are their chrome; "your cloud machine"
heads the hero's, the setup picture's, ComesBack's, Localhost's and
Ready's windows, where it tells the machine from the laptop or a snapshot.
Only the hero's machine carries the running dot, since its stop greys it.

The terminals (Ready, Localhost's tmux, the Browser's agent log, the
Editor's nvim) draw from one palette, `--t-*` in `pictures.css`, light on
the light page and dark on the dark one. Their captured colours are mapped
by role (one-dark and xterm-256 by their ANSI tables, Tokyo Night through
a role map), never their text. The Browser's captures exist in the light
scheme only; in the dark scheme they are drawn inverted with their hues
turned back (`invert(0.9) hue-rotate(180deg)`), so the app reads as a
dark page beside the dark windows (I-961).

Each looping picture has a pause button (WCAG 2.2.2), shown at rest in
the pictures' quiet grey, with its chip's right edge on the text line,
named after its picture ("Pause the ports picture"). A band picture's
stands in its field's top right corner; a cell's at the right end of its
title's line, which keeps clear of it. Ready and the Editor do not loop,
so they have none, and nothing holds a place for one. On a touch screen
the button is a 44px target with the 28px chip drawn in its middle.

`landing/Terminal.svelte` is no longer on the landing; `/design` still
shows it.

## Each fact once

The install command shows twice, under the hero's buttons and as the
first of the three steps: the owner's call (I-843), so a reader who
scrolled past the hero starts from one list. `repose run` shows in the
pictures and in the run step; the cells carry other commands. The
snapshot story has one telling per place: the hero shows the wreck and
the restore, the ComesBack cell says how a restore runs (on the
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
agents picture (I-843, restoring main's rule). The hero and the setup picture
draw windows, not terminal output; the commands section uses light
command rows.

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
motion (I-400). The hero's wires are drawn in CSS, so the no-JS frame
has them; only the skill's trail is measured by the script, and only the
loop draws it.

The pictures move only by `translate`, `scale`, `opacity`, `clip-path`,
colour and a wire's `stroke-dashoffset` (the hero's connectors draw
along their length): nothing they animate changes layout, so a loop adds
no layout shift (the Browser picture's ring is four edges scaled to size,
and the snapshot tile folds by a clip, its box keeping its height). The
hero's rail and its tiles are laid out in CSS, the newest tile filling
what the stubs under it leave, so hydration resizes nothing (I-399,
I-667).

One controller decides when each looping picture plays
(`landing/motion.svelte.ts`, I-666). Each of the five (the hero,
OneCommand and the three moving grid pictures) registers with it in
place of an observer of its own:

- **Pause.** Each picture has an icon-only pause button
  (`PauseButton.svelte`, WCAG 2.2.2): a 28px picture chip with the
  windows' hairline and `--radius` corners, `aria-pressed` while paused, in the
  tab order throughout, and always shown. It never stands over a head or
  content (I-673, I-961): a band picture's in its field's top right
  corner, a cell's at the end of its title's line, its chip's right edge
  on the text line for all five loops. Its
  name says which picture it pauses. Its hit area is 44px. A paused picture
  stays on its frame through scrolling out and back until play is
  pressed. A pointer moving inside a picture, a press on it, or focus
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
  crossfade. The hero and OneCommand are in no group.
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
  rebuilt hero seeks back to where it was. A phone's address bar
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
    shows both. A row that drops in as it appears (the hero's
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
    tile clips up to its head in 550ms `inOutCubic` as the newer one lands,
    and at the loop's seam unfolds back to the top the same way (550ms
    clip, 700ms travel, `inOutCubic`) as the newer one fades (I-669).
  - *The restore's wipe* in "Break it and roll it back": each row the
    snapshot holds wipes in over its wrecked twin by clip-path, from the
    snapshot's side (the right beside the machine, the left where the
    snapshots stand under it), 450ms `inOutCubic`, 90ms apart (I-669,
    I-675).
  - *Struck names greying* in the hero's wreck: a 200ms ease-out colour
    transition in CSS, to `--pic-faint`.
  - *The snapshot mark* is above: a quarter turn in 420ms `outQuad`, a
    full turn back in 1s `inOutCubic`.
  - *The shutter* in the hero, as a snapshot is taken: an accent wash
    over the machine rises to 0.9 and clears in 460ms, `outQuad`.
  - *The knock* in the hero: the malicious skill bounces 8px toward
    your laptop and back, 380ms `outQuad`, while its trail draws: a red
    dashed line from the skill to the machine's wall, level with your
    private rows, 700ms `inOutCubic` by `stroke-dashoffset`. The wall's
    red edge (200ms `outQuad`) and the cross (300ms `outBack`) land at its
    tip; the laptop is never tinted (I-667). Below 1024px the trail runs
    up the machine's right edge to the rule under its head, and the cross
    sits over the status dot, inside the wall (I-677).
  - *A wire lighting* in the hero: the permanent hairline's lit copy
    grows from one end, 380ms `outCubic`, and fades, 300ms `inQuad`; the
    snapshot wire in the accent as a snapshot is taken or restored, the
    globe's in red as the skill comes in.
  - *The stop* in the hero: the title's dot greys (a 200ms colour
    change), and the agent and its mode line dim to 0.35, 400ms
    `inQuad`. The skill, its trail, the wall's red edge and the cross
    stay until the restore begins and fade together then, 400ms
    `inQuad`. After the restore the agent and its mode line return,
    400ms `outQuad` (I-677).
  - *The hero's snapshot copy*: a copy of the machine leaves along the
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
  - *The hero's repo row* on `repose run`: the laptop's row greys to
    `--pic-faint` while its copy is away (a 200ms colour change). The
    copy flies under the laptop's window; wide, it rises into the band
    over the `repose run` chip in 300ms `outQuad`, crosses the gap
    (900ms `inOutCubic`) and settles onto the machine's row (600ms
    `inOutQuad`); wide, it also fades in over 250ms `outQuad` from 300ms
    into its flight, once its name is clearing the laptop's wall, with
    its right 40 percent masked, so its bare fill never shows alone at
    the wall (I-961); stacked, it appears 10px over the machine's row and
    settles, 600ms `outCubic` (I-677).
  - *The hero's wreck values*: postgres counts down from 3,532 to 0 in
    its one value, 600ms `outQuad`, as ComesBack's counts do; node's
    version fades out, 300ms `inQuad`, before "not found" fades in
    (I-677).
  - *The hero's seam*: the project rows dim to 0.22 and the 21:25 stub
    goes, 450ms `inQuad`; the nightly tile, folded at the rail's foot,
    rises and unfolds behind the stop tile, 550ms `inOutCubic`, and the
    stop tile then rolls up off it from the bottom, a 500ms
    `inOutCubic` clip, so the rail always shows one whole drawing
    (I-677). The hero's beats
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
    end: to 0.22 in 450ms `inQuad`, the hero's seam, and up from there
    as each new row lands.
  - *Rows making room* in "Your working state": a panel's rows move down
    one row, 450ms `inOutCubic`, as a commit takes the top slot, and both
    panels' rows back up at the loop's end.
  - *The snapshot copy* in "Break it and roll it back": the machine's
    rows travel into the new tile, 850ms `inOutCubic`, reaching the
    tile's scale in the first 510ms (`outCubic`); the copy fades in its
    last 150ms (`inQuad`), and the tile comes in (250ms) as it is gone
    (I-675).
  - *The pointer's press* in the Browser picture: it scales to 0.86 and
    back in 220ms, `outQuad`, as your click lands.
  - *The pause button under the pointer*: its icon and edge darken in
    150ms, the CSS `ease` (I-666).
  - *Colour as a class changes* (CSS transitions, the CSS `ease` unless
    named): the `repose run` chip filling as it fires, 180ms (Hero,
    OneCommand, Localhost, Browser), OneCommand's `git fetch repose`
    chip and the hero's `repose snapshots create` chip the same way; a
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
