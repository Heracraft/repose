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
headline starting under the logo. In order:

1. **Hero.** Eyebrow "For solo founders and their agents"; the serif
   headline "Your dev environment, replicated in the cloud"; main's lead
   ("Your code, tools and logins sit on a machine of its own, ...");
   "Get started" and "Copy install command"; the install command in mono
   under them, exactly what the button copies. Under the copy, the hero
   picture (`landing/Hero.svelte`) on a stage the frame's width: the
   agent wrecks the cloud machine and a snapshot restores it (I-843).
2. **"Your laptop's setup, in one command"** (the owner's I-772 heading
   and paragraph), then the OneCommand picture: `repose run` copying the
   laptop's source control, changes and ignored files to the machine.
3. **"On every machine"** (`#features`). Four cells, each a picture
   (ComesBack, Localhost, Browser, Editor) over a serif claim, one
   paragraph, and the command that does it in mono with no copy button,
   since a visitor without the CLI cannot run it.
4. **"The agents you use, and the tools they need, on first boot"**, with
   "All agents" and "Everything installed" side by side at the right end
   of the headline's row (under it on a phone), then the Ready picture.
5. **"Start in three commands"**, main's rows (owner, 2026-10-21): wall to
   wall, the step's serif name with a gauge counting a third at a time on
   the left, its command filling the right from 768px. Install the CLI,
   Sign in (`repose login`) and Run in any checkout (`repose run`). Each
   row shows exactly what its Copy copies, and it runs on a visitor's
   laptop.
6. **Pricing** (`#pricing`). "Launch prices. Seven days free.",
   `PlanCells` in its public variant (the one drawing of the plans,
   I-663), then "Start a free week" and the seats line from
   `GET /public/seats` ("18 seats left"; the waitlist and seat copy stays
   as it is until the owner decides the 2026-10-17 notes).
7. **Questions.** Five a solo founder asks before trying it, each one a
   reason to: why not skip permissions on the laptop (what the agent can
   reach there and here), closing the laptop while the agent works
   (notifications, answering from the phone), the agent wrecking the
   machine (snapshots), three agents on three approaches (`repose fork`),
   and why not set up a server yourself. Every answer is from the docs
   (secrets, notifications, lifecycle, sync). No setup trivia (where a
   login lives, Windows, how commits come back, what a stopped machine
   costs): those belong in the docs and give a visitor no reason to try
   it (owner, 2026-10-21).

A section with a picture puts its heading over a sunken stage (hairline
edge, the kit's 12px panel radius) the frame's width. The commands
put their heading over their rows. The questions hold their heading in a
sticky left column from 1024px; below 1024px every section is one column.

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
pictures' chips and wires were too strong (owner, 2026-10-21). Windows take a 6px radius,
frames and stages the kit's 12px. In the dark scheme the stage is lowest,
a window's head a step up and its body a step above that.

Each looping picture has a pause button (WCAG 2.2.2) on the floor under
it. On a touch screen the button is a 44px target with the 28px chip
drawn in its middle.

`landing/Terminal.svelte` is no longer on the landing; `/design` still
shows it.

## Each fact once

The install command shows twice, under the hero's buttons and as the
first of the three steps: the owner's call (I-843), so a reader who
scrolled past the hero starts from one list. `repose run` shows in the
pictures and in the run step; the cells carry other commands. Plan
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
  or invent.
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

Terminal UIs appear only in the "On every machine" grid and in the agents
picture (I-843, restoring main's rule). The hero and the setup picture
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
  tab order throughout. It shows on hover or when anything in the frame
  has focus, and stays shown while paused; on a touch screen it is
  always shown. It stands on the floor under its picture at the right
  edge, never over a head or content (I-673): in the stage's 40px
  bottom padding under the hero and OneCommand, in the 40px between a
  grid cell's frame and its title. Its hit area is 44px. A paused picture
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
    `inOutQuad`); stacked, it appears 10px over the machine's row and
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
  - *The pause button showing* on hover or focus: an opacity fade of
    150ms, the CSS `ease`, with its colour change (I-666).
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
    150ms colour change (`DESIGN-LANGUAGE.md`, "Motion").

Nothing else moves. A new motion is added to this list with its duration
and easing, or it does not ship.

On the rest of the page only colours move on hover, and the questions' plus turns 45 degrees over 150ms.

## Process

- Judge the assembled page, not a component in isolation: screenshot it at
  1440×900 and 390px, light and dark, and look before calling anything done.
  Check 1280, 1024, 768 and 360 for the column changes.
- Look at captures at their real size. A full-page capture read after
  being scaled to a fifth hides what a visitor sees at once (a lopsided
  hero got through that way, 2026-09-27). Capture the viewport, or crop
  the region, at 1x and judge that; use a full-page capture only for the
  page's rhythm, never for a section's layout.
