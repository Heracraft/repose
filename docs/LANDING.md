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

Don't show or mention notifications, answering from your phone, "check in
from anywhere", remote control, always-on, collaboration or a software
factory.

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
- Show tools the way people use them: Neovim means a real,
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
  things people have (`.ssh`, a cat photo, tax documents).
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
- Snapshots are small, contents hidden. The hero and "Let it break the
  whole machine" draw them alike (`SnapshotTile.svelte`, I-666): the
  pinwheel and the time in a head row, the newest with the machine's rows
  in miniature under it, each older one its head alone, stacked under
  the newest. A mark at the head's right end says why it
  was taken: a moon for the nightly one, a prompt for one you asked for,
  the wreck's cross for the one a stop takes.
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

Amended 2026-10-11 (I-667), after the landing audit the owner approved:

- Each snapshot has its true cause. The older one is the nightly 03:00
  (a moon), already on the rail at first paint; the newer one is yours,
  21:25, taken when a `repose snapshots create` chip fires on the
  laptop's side. The times are illustrative, on one clock. Before the
  restore the machine stops (its dot greys, the agent fades), and the
  stop takes a third snapshot of the wreck (the cross) as a real stop
  does; the restore comes from 21:25, and the agent comes back (I-636).
  No timing label.
- The agent's work rows are OneCommand's uncommitted set
  (`.env.example`, `poller.ts`, the untracked `timeout.test.ts`) with
  git's letters (M, M, U), as OneCommand draws them. Their line counts
  were never recorded, so the rows carry no counts until someone takes
  `git diff --numstat HEAD` on that checkout.
- Red means destroyed and nothing else: the laptop's rows are never
  tinted. The skill's run at your laptop is a red dashed trail from the
  skill to the machine's wall, level with your private rows, where the
  knock and the cross land.
- The stage is balanced on one floor: the laptop stands on the
  machine's bottom line, and the right column is exactly as tall as the
  machine, the snapshots panel over the globe on its floor. Permanent
  `--pic-dim` hairlines with a dot at each end wire the machine to the
  snapshots and to the globe; motion only lights them. Below 1024px,
  where the machine's agent row cannot hold its mode line and the skill
  side by side, the picture stacks as on a phone: the laptop, the two
  commands, the machine, then the snapshots and the globe in one row
  under it, each wired up to it. At 480px and under the globe hangs on
  its wire at the machine's right corner and the snapshots panel takes
  the full width under it (I-677).
- The machine is never empty. Server-rendered, it has its header,
  postgres "job listings 3,532 rows", node v24.20.0 and the agent's row;
  only the project rows wait, dimmed, for `repose run`.

## "Your working state, in one command"

Titled "Your laptop's setup, in one command" since I-477, with one
sentence for what the picture does not show: the globally installed CLIs,
the copied logins, and the SSH keys staying home. This file keeps the old
name for the section (`OneCommand.svelte`).

The owner chose the animated version (anime.js): the laptop's commits and
changed files copy across into the cloud machine panel when `repose run`
fires; `node_modules/` stays behind, struck. Keep it animated.

How it moves (I-668):

- The panels wear the feature grid's window head and chip (I-669): a
  28px sunken head with the 16px mark and the place's name at 12px, and
  the 12px accent chip that fills as it fires. They stand side by side
  from 1024px; below that they stack, where a row has the width to show
  its message.
- The two commands stand in the middle of the gap between the panels,
  halfway down them (owner, I-684; landing-revamp put them on the title
  bars and heads): side by side, `repose run` with its arrow to the
  machine over `git fetch repose` with its arrow to the laptop, each on
  a wire from wall to wall; stacked, the two side by side. The chips and
  wires are above every copy, and no copy is seen in the gap. The machine says "Ready in 16s"
  (the docs quickstart's time, `content/docs/index.md`, held there by
  `OneCommand.test.ts`) in its own head, after its green dot.
- A row that travels: its source greys to `--pic-faint`, and a copy of
  the row it lands as, on the accent's tint (9% into `--surface`) with a
  2px accent edge at its left (main's travelling chip, which the owner
  kept, I-684), sets down on its exact slot; the real row comes up under
  it as it fades. At most two copies are in flight, each in its own
  row's band. At every width a copy is seen only inside the panel it
  lands in, within its walls (I-675): a copy in the gap was cut to
  fragments by the walls. It crosses the gap unseen and slides in
  through the panel's wall. Side by side, the panels' rows share bands
  and a copy comes in through the machine's left wall; coming back,
  through the laptop's right wall into its top slot. Stacked, a copy
  slides in under
  the machine's head and eases down onto its slot, the lower rows first,
  so it crosses only slots still empty; coming back, it rises into the
  laptop's top slot. A group's heading on the machine shows once its
  first row lands and no copy is still to cross it.
- The machine is never an empty box: its rows from the last loop stay at
  0.22 opacity, as the hero's do, until the new ones land over them.
  `repose run` fires 500ms into the loop. Each window is as tall as its
  rows; the machine's is a row shorter, with no node_modules.
- `repose run` stands alone in the middle of the gap until `git fetch
  repose` arrives; it moves aside (stacked) or up (side by side) to make
  the pair.
- The coda: the agent commits on the machine. Its rows move down a row
  and the commit drops into the top slot. The `git fetch repose` chip
  arrives and fires, the laptop's rows make room as it fires, and the
  commit travels back the same way, landing as the newest row with a
  `repose/fetch-timeout` ref in the chips' accent (docs `sync.md`
  "Getting work back"; `repose stop` makes the same fetch). The commit,
  e41b9c2, is illustrative and touches none of the three changed files,
  so both Changes lists stand.
- The still frame, with reduced motion and with scripts off, is the end
  state: both panels holding the agent's commit, both chips, and Ready.
  Until the coda both panels' rows sit a row up, so the slots show no
  gap.

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

- Short is not the point; plain is (owner, 2026-10-17, I-772). "Punchy"
  lines that leave the reader working out what the product does cost
  more than the words they save: "if one wrecks it, a snapshot puts it
  back" was read as "pure AI slop, corpo LLM speak". Say the thing a
  developer already feels, in the words they would use. The chore repose
  removes is a machine you set up for tens of minutes to hours, the
  GitHub login, the CLI to install, the `.env` to paste, before an agent
  can start; the hero and the setup head say that, and are a paragraph
  where a paragraph says it better than three fragments. "No
  hand-holding" above still rules out explaining the picture or the
  screen; it does not license a line nobody can parse.
- The lead is one paragraph on its measure, never one line per
  sentence (I-772): three sentences each held whole read as a staircase
  of fragments under a headline twice as wide. The hero's lead and a
  section head's sentence take 48rem, about 70 characters; every other
  lead keeps 36rem.
- The shapes that read as generated, and are banned on this page
  (owner, 2026-10-17, I-772, from `hardikpandya/stop-slop`):
  - **x-not-y.** "costs you minutes, not your setup", "the machine is
    disposable, not precious". Say the thing you mean and stop. "Your
    SSH keys stay home" went for this reason.
  - **Triplets.** Three sentences or three clauses in a row beat like a
    speech. Prefer two.
  - **Choppy fragments** standing in for sentences in prose: a lead or
    a card's line reads as sentences. A deliberate label is not prose
    and may be two words ("Launch prices. Seven days free."), as the
    pricing head is.
  - **Punchy closers.** A last line written to be quoted.
  - **No actor.** Prefer a person doing something ("You spend an hour
    setting up a cloud box") over a state of the world.
  - **Em-dashes**, filler adverbs, and "every/always/never" as
    emphasis rather than fact.
  Plain beats clever, and a sentence that explains the product beats a
  sentence that performs. The first rewrite under I-772 traded a wall of
  text for a different wall of text; the owner's word for it was "bro you
  just added verbosity".
- Never write as if the product were Claude-only: "the agent" drives the
  browser, reads the console; not "Claude Code does X".
- No empty phrasing that sounds generated ("tests in a real browser": as
  opposed to what?). Say what the viewer gains.
- A card title says what you can do, not what you can see (I-772):
  "Watch the agent's browser, and take over", since taking over was in
  the card's line and not its title.
- Never promise recovery the product cannot give (I-772). A snapshot
  restores a machine only if one was taken, so the page says repose
  snapshots the machine nightly and that you can take one yourself any
  time; it never implies a wreck undoes itself.

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
- Snapshots are about the machine. Git already brings back deleted source
  files; a snapshot is the whole disk (the root overlay's
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
  Since I-675 the machine is a closed window inset alike on all four
  sides, its six rows sharing the frame's height, beside a snapshot
  column up to 120px wide; under 410px of picture the snapshots stand in
  a row under the machine, so no row's label is cut. Taking the grid's
  turn from its still frame, it starts at its own seam, so it leaves the
  restored machine through the loop's exit, never by a cut.
- "Five agents and a full toolchain on first boot" is clean; leave it.
  2026-09-27: the agents' row shows the marks alone, no name or command
  under them. The title went with I-772, after the owner asked for "all
  the agents and all the tools they could need for the task": it reads
  "The agents you use, and the tools they need, on first boot". The
  picture is unchanged.

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
    before "The machine's ports on your localhost" (I-772; it was "Your
    dev server on your localhost").
  - *ring* (a lens: one disc, a paper ring, an ink pupil; the quartered
    ring was redrawn 2026-09-27, its four tones did not work small) is
    watching the agent's browser, before "Watch the agent's browser, and
    take over" (I-772; it was "Watch the agent use the browser").
  - *arch* (a door in) is the editor over SSH, before "Open it in your
    editor".
  - *asterisk* (a wildcard) is the toolchain and anything installable,
    before "The agents you use, and the tools they need, on first boot"
    (I-772).
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
  drawing) is a copy being made, caught just before it finishes (DECISIONS
  I-779): a disc in the text's ink, and in `--sh-accent` the part of a
  second, slightly smaller disc that has cleared it, with the seam still
  open. It is drawn with this page's sphere, and its curve is the one the
  system allows the mark. One drawing serves the header and the 16px tab;
  the favicon is not a heavier cut. It encloses nothing, on purpose: a
  ring or a box with a block in it reads as opencode. It replaced the
  owner's traced cross-and-blocks sketch (I-363), which turned to mush
  under 24px, and that had replaced the r. The mark is in every page's
  header, not only this one's (I-381); where it appears and its minimum
  size are in `DESIGN-LANGUAGE.md`, "Logo".
- The shapes live in `landing/Shape.svelte` and draw only from the `--sh-*`
  tokens. The app's own pages never use them, with one exception: the
  logo's block is `--sh-accent`, and the logo is in every header
  (I-381, I-393, I-779).

## Since the owner's notes (implementation, awaiting the owner)

Changes made in the 2026-10-01 repair round on top of the owner's
sections above, recorded here and not in them, since the owner has not
reviewed them yet (STATUS.md). Each holds until the owner says otherwise.

- **"Your working state", the figures** (I-398). The machine shows no
  `node_modules/`, since sync leaves dependency directories behind
  (`content/docs/sync.md`), and the machine reads "Ready in 16s" (14s
  until I-668), the docs quickstart's figure: since I-367 `repose run`
  syncs only into a new machine, so the captured "Ready in 0.6s" of a
  re-sync no longer happens. The picture is that first run: the machine
  starts with no commit, and every commit travels into it, master's
  `7ea43a8` with the branch's two (I-400). Where the panels stack, a copy
  is seen only inside the panel it lands in (I-668).
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
  OneCommand's "Ready in 16s" in the machine's head, is the Foundation's `--ink-muted`. A mark
  laid over a capture follows the capture's ground, not the page's
  scheme: the Browser picture's ring, on the white page, is blue-600 in
  both schemes (`--chip`), and the bar on its dark log is blue-400
  (`--on-log`). The Browser's captures exist in the light scheme only,
  so in the dark scheme its toolbar and page are dimmed (brightness
  0.86) to sit beside the dark windows; the marks over them are not
  (I-676). The green running dot in the drawn window heads and
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
  was kept whole on one line in a nowrap span (`.whole`); both went with
  I-772, which made the snapshot card "repose snapshots the machine
  nightly, and you can take one yourself any time. Restoring it brings
  back the databases, logins and uncommitted work in minutes." Held
  nowrap, that sentence ran 275px past a 320px phone.
- **The grid's four pictures share one frame** (I-669): 17rem tall, the
  windows inset 12px top and sides (10px on a phone) and run off the
  frame's bottom edge, each under the same 28px head naming where it
  runs ("your cloud machine", "your laptop"), Browser and Editor
  included, since the head says where the app runs. One chip style for
  `⇄ 5173` and `repose browser`. ComesBack draws its snapshots with
  `SnapshotTile`: 03:00 nightly (moon) and 21:25 taken by you (prompt).
  Localhost starts on Chromium's real "This site can't be reached"
  page, dimmed, with localhost:5173 in the bar, and the dev server's
  Local line on the machine throughout; each of its pages is cropped to
  end in white space between two lines at any width, and its chip
  starts under the tmux bar, never over it (I-676). The Editor's
  statusline keeps its right end (position, line and column, clock)
  on a phone; the crop takes the file path. The Editor fits its 78
  columns from 640 wide, so no line is cut by the crop.
- **Every step's command has a Copy button** (I-398), as the install
  command beside the hero's button does. "Copy" says a step is its title
  and its command; the button is the command row's, not a third part.
  The last step shows `cd ~/code/your-project && repose run` and its button
  copies `repose run` alone, since the path is an example that a pasted
  command would fail on (I-401).
- **Pricing's sentence and spec line** (I-397, recorded in I-401). The
  head sentence is "Seven days free, card at checkout. Prices in USD,
  before tax. A plan's memory is shared by the machines you have
  running; a stopped machine uses none. A plan's disk counts the data
  your projects hold." (it was "Three plans. Seven days free, card at
  checkout."; the disk sentence came with I-585, so a buyer reads 100 GB
  as room for many projects, most of them stopped): the three cards show there are three,
  and the currency, tax and how memory is counted are
  `docs/PRICING.md`'s, facts the cards do not show. Each card's spec
  reads "N GB of memory" (it was "N GB running at once", which did not
  say it was memory), naming what the units count (I-402).
  Cut back with I-772: the memory, stopped-project and disk sentences
  were the docs on the page ("Verbosity for the sake of verbosity...
  this is not the docs"), and the cards already carry those figures. The
  head is "Launch prices. Seven days free." (owner, 2026-10-17: "shorty
  as fuck"). The currency, the tax line and "card at checkout" are in
  `billing.md`, on the billing page and at checkout, and off the
  landing.; the price-change promise under the cards went to `terms.md`,
  `billing.md` and PRICING.md alone, and Solo's founding price is `$49`
  struck before `$39` with "First 12 months, first 30 subscribers" under
  it, not a sentence that wrapped to two lines.
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
- **The hero's laptop panel hugs its rows** (I-398) and stands on the
  machine's floor (I-667): from 1024 wide it ends under its last row
  instead of stretching to the machine's height, and its bottom edge is
  the machine's (y=814 at 1440x900; I-677).
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
- Where motion could help, you may offer an animated and a static
  version for the owner to choose (anime.js is allowed).

## The page's grammar (design system, 2026-09-27)

The pictures draw a machine as a panel with hairline edges, and the hero
shows those edges as the wall the attack stops at. The page takes that
drawing as its own grammar, in `apps/web/src/routes/landing.css` (imported
by `+page.svelte` alone; the house tokens stay in `layout.css`):

- **Rails.** The content stands between two hairlines (`.rails`) that run
  from the site header's rule to the footer, on the edges of the frame
  every page shares (`--frame-w`, 1200px: x=120 and 1320 at 1440), with
  the text inset by the frame's gutter (`--land-x` is `--frame-x`), so
  the headline starts under the logo (I-656). They and their ticks are
  hidden below 1240px, where a rail would stand on the window's edge.
  On load they draw themselves from the top down, once, and
  the ticks fade in after them ("Motion").
- **Rules run wall to wall, ticked.** Every section (`.sec`) opens with a
  rule across the full width, and a small cross (`::before`/`::after`)
  marks where it meets each rail, as a drawing marks an intersection. The
  first section draws no rule: the site header's hairline is that rule,
  and the first section's ticks mark where it meets the rails.
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
  stack as tall as the machine: the snapshots panel (titled, the
  miniatures shrink into it) over the internet as a bare 40px globe on
  the stack's floor, no window, since the internet is not a machine of
  anyone's. The snapshots panel's foot is the rule over the machine's
  agent row, and the globe's row is that row (I-677). At 480px and
  under, the globe hangs on its wire at the machine's right corner and
  the snapshots panel takes the full width under it. No snapshot hangs below the machine and the globe does not
  float at the edge. Two permanent hairlines cross the gap between the
  machine and that stack, each with a dot at both ends (I-667). The rogue agent is the red mark alone, no
  halo, background or border around it.
- **Commands are rows.** The install command and each step's command are
  one `.cmd`: a mono row on a sunken ground with a hairline, the way a
  picture shows a row of a terminal.
- **The pictures' grammar** (I-673). Every picture is drawn from the
  same parts, in `landing.css` and three components:
  - *Stage*: `--stage`, 40px (`--stage-y`) above and below the picture
    at every width; the hero's top on a phone is 20px, so the machine's
    head is in the first screen (F04), and 28px from 768px wide on a
    screen 840px tall or less, so the agent's row ends inside 1280x800
    (I-677). The bottom 40px is the floor the pause button stands on.
  - *Window*: a hairline at `--rule-strong` on `--surface`, its head
    `WinHead.svelte`: `--shot-head` (28px), `--sunken`, 10px side
    padding, a 16px mark, the name at 12px. Every "your cloud machine"
    window takes `--pic-wall` instead, the hero's ink edge.
  - *Chip*: `.chip`, 12px mono 600 in the accent, filled while it fires;
    only the ground fades (180ms), the text turns at once.
  - *Snapshot tile*: `SnapshotTile.svelte`, its head 27px plus its rule,
    a window head's height.
  - *Radius*: `--radius` (3px) for frames, windows, chips, terminals,
    the command row, Copy and the boxes inside a picture.
- **The sign-off.** The page's shape set stands one to a cell between
  the rails (`.frieze`); the site footer (`SiteFooter`, the same on every
  page) follows under the rails.
  Eight cells in one row at every width, a phone included.
- **Type.** Display `clamp(2.75rem, 6.6vw, 5.25rem)`; section titles
  `clamp(1.9rem, 3.4vw, 2.5rem)`; cell and step titles 1.125rem serif
  600; a cell's sentence 1rem (a step has none, "Copy"); lead
  `clamp(1rem, 1.3vw, 1.125rem)`, the hero's 0.9375rem on a phone so the
  machine's head is in the first screen (I-667); a command row (`.cmd`) `--text-compact`
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

