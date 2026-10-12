# Design language

One system for every page: neutral paper and ink, hairline rules, corners
of 2 to 4px, no shadows or gradients, one accent colour. The source is
`apps/web/src/routes/layout.css`; read the CSS, not the recruiting app it
began as a copy of (DECISIONS I-369 retired that).

The Foundation below is shared by the dashboard, the docs, the legal pages
and the landing. The Dashboard part adds the patterns of the signed-in
pages. The landing adds its own grammar in `LANDING.md` on top of the
Foundation, and nothing in the Foundation is restated there.

# Foundation

## Tokens

Scheme colours are CSS variables on `:root` in `layout.css`, redefined
under `prefers-color-scheme: dark`, and exposed as Tailwind utilities
through `@theme inline` (`text-ink`, `border-control`, `bg-sunken`), so
each utility follows the scheme. Light and dark come from the OS only: no
theme toggle, no `class="dark"`, no `[data-theme]`.

| Token | Light | Dark | Use |
|---|---|---|---|
| `--page` | `#fbfbfa` | `#111110` | The page ground (`bg-page`). `app.html` paints it before the CSS loads (I-331). |
| `--surface` | `#ffffff` | `#161615` | Fields, `.btn-quiet`, `.banner`, `.card`, `.stat-row`, the Account panel, toasts, the Nix editor's current line in the light. |
| `--sunken` | `#f4f4f2` | `#1b1b1a` | Code blocks, the Nix editor, the landing's stages, kbd. |
| `--rule` | `#e6e6e3` | `#31312e` | Hairlines between sections, rows and cards (dark stepped up from `#2a2a28` by I-659). |
| `--rule-strong` | `#cfcfcb` | `#3b3b38` | Badges, table heads, banners. Never the only edge of a control. |
| `--control-edge` | `#888883` | `#6e6e6a` | The edge of anything you type into or press: fields, `.btn-quiet`, the hollow state dot, the meter track (`border-control`). |

Three layout tokens sit beside them, the same in both schemes: `--frame-w`
(75rem), `--frame-x` (`clamp(1.25rem, 3vw, 2.5rem)`) and `--header-h`
(57px). See "Page frame and header".
| `--ink` | zinc-900 | zinc-100 | Primary text (`text-ink`). |
| `--ink-muted` | zinc-600 | zinc-400 | Secondary text: metadata, ledes, table heads, the inactive nav link. |
| `--ink-faint` | `#6b6b66` | `#8f8f8a` | Tertiary text: placeholders, code comments, timestamps. |
| `--accent` | blue-700 | blue-300 | Link text (`text-accent`: `.link`, the docs' links). |
| `--accent-strong` | blue-900 | blue-200 | A link under the pointer (`hover:text-accent-strong`). |
| `--selection` | blue-200 | blue-800 | The text selection's ground. |
| `--focus` | blue-600 | blue-400 | The focus ring. |

Pages use `text-ink*`, never a hand-paired `text-zinc-500
dark:text-zinc-400`. That pairing is how the dashboard and the landing
ended up with two different "muted" greys (I-370). The accent is the same:
a page names `--accent`, never a blue step (I-392). `app.html` writes
`--page` and `--ink` out by hand for the first paint;
`src/lib/app-html.test.ts` fails when the two files disagree.

## Palette

Tailwind's zinc, blue, emerald, amber and red are re-toned once in
`@theme`, so a stock utility cannot bring in Tailwind's defaults. Each
colour has one meaning:

- **Zinc** is a neutral grey: text, rules, the primary button.
- **Blue is the accent**, for links (`.link`, the docs' links), the focus
  ring and text selection. It may also draw a measure: a meter's fill
  under its limit and the usage chart's line over a 12% accent area
  (I-659). Nothing else is blue in the dashboard, the docs or the legal
  pages except the logo's big square (see Logo); a busy state, a current
  tab and code syntax are not (I-373, I-374, I-388). The landing's shapes
  and bar use it as `LANDING.md` says, and so do the plan cells on
  `/billing` (I-663).
- **Emerald** is running and success, **amber** attention: a warning
  banner, a meter's overage, something that waits on you
  (`.badge--warn`). **Red** is failure and destruction.

No purple, pink, orange or other hue in anything the site draws for
itself: text, controls, rules, state, the landing's shapes and bar. A new
colour there needs a DECISIONS entry.

One layer is exempt: a picture of a real tool on the landing (a terminal,
Claude Code, an editor, a browser) keeps that tool's own colours, because
it shows what you will see (`LANDING.md`, "The hero"). Claude Code's orange
mascot, its pink `⏵⏵ bypass permissions on` line, a terminal's ANSI
colours and an editor theme live inside those pictures and nowhere else
(I-392).

## Contrast floor

- Text: 4.5:1 or better on `--page`, `--surface` and `--sunken` in both
  schemes. Every `--ink*` token holds it. zinc-500 (`#70706b`) holds it in
  the light scheme only (4.52:1 on `--sunken`); in the dark it is 3.5 to
  3.8:1, so it is never dark-scheme text. zinc-400 and zinc-300 are never
  light-scheme text. A page that needs a grey for text uses `text-ink-muted`
  or `text-ink-faint`, which switch with the scheme.
- Control edges, focus rings, state dots and meter tracks: 3:1 or better
  against what they sit on (WCAG 1.4.11). `--control-edge` holds 3.2:1 or
  better on `--page`, `--surface` and `--sunken` in both schemes; a control
  whose only edge is `--rule` or `--rule-strong` fails this (the Nix
  editor and the docs' copy button did, I-391).
- State never rides on colour alone: a dot has its word, a meter says
  "over", a current tab has its underline.
- `tests-a11y/a11y.spec.ts` fails on any Lighthouse binary audit that
  scores 0, on every page in both schemes at 1440 and 390 (I-389).

## Corners

The whole radius scale runs from 2px to 4px (`--radius-xs` and
`--radius-sm` 2px, `--radius-md` 3px, `--radius-lg` and up 4px).
`rounded-sm` on fields, buttons, cards, banners and toasts; `rounded-xs`
on badges, dots and keys. No shadows. No gradients, except the
one-colour `linear-gradient(c, c)` that draws a rule or a bar, and the
landing's sphere.

## Type

Two webfonts, self-hosted from `static/fonts` with their OFL texts, so no
page asks a third party for a font (I-371):

- **Noto Serif** (400, 600, 700) for every heading, `h1` to `h6`, and
  `.font-display`. The base style applies it, so a heading needs no
  `font-display` class.
- **JetBrains Mono** (400, 600, 400 italic) is the one monospace: code,
  commands, ids, sizes, badges and the landing's pictures and labels.
  `--font-mono` leads with it; a component never names its own mono stack.
- **Body text is the system sans** (`--font-sans`, Tailwind's default
  stack written out).

Serif titles over a plain sans page are the identity. Each webfont has a
metric-matched local fallback, so nothing reflows when it arrives.
Contextual ligatures are off for the whole page
(`font-variant-ligatures: no-contextual` on `html`): JetBrains Mono draws
its code ligatures that way, and one of them spaced `://` apart in every
URL (I-392).

| Step | Size | Use |
|---|---|---|
| `text-2xs` | 11px | Badges, the landing's uppercase labels. The floor: no text is smaller. |
| `text-xs` | 12px | Table heads, notes under a meter. |
| `text-compact` | 13px | Mono readings beside sans text, code blocks, kbd, the dashboard nav on a phone. |
| `text-sm` | 14px | Dashboard body, fields, buttons, banners. |
| `text-base` | 16px | Body text outside the prose. |
| `text-prose` | 17px, 1.7 | Docs and legal prose (`.doc`, I-660). Code blocks stay 13px. |
| `text-xl` | 20px | Every dashboard h2, semibold. |
| `text-2xl` | 24px | Docs and legal h2; a stat's figure (`.stat-value`, serif). |
| `text-4xl` | 36px | The page title everywhere: `PageShell`, `DocPage`, a policy. Serif 600 at `-0.01em` (I-660). |

The landing sets its own display sizes (`LANDING.md`, "Type"). Mono under
13px is a badge and nothing else: no label, no running text. The
landing's uppercase labels are its own (I-395). A picture of a real tool
on the landing (the Editor's LazyVim screen, the Browser's log, Ready's
terminal, Localhost's tmux bar) is exempt from the 11px floor and the
13px mono rule, as it is from the palette: it is drawn at the scale that
fits the whole capture in its frame, and on a phone the Editor's screen
draws at 8.4px so 69 columns fit (I-397). A picture the landing draws
for itself keeps the 11px floor, and its rows (a file name, a diff
stat, a time) are mono at 12px to 12.5px, 11px on a phone: a picture's
labels at the picture's scale, as "Your working state" drew them when
the owner chose it (I-399).
Pages write no `text-[13px]`; a size that is missing becomes a step here.
Figures that change or line up in columns (sizes, counts, times, prices)
use `tabular-nums`. Page markup uses no `font-bold`; emphasis in body text
is `font-medium` or `font-semibold`.

## Spacing

Tailwind's 4px steps, used the same way everywhere: the frame's gutter
(`--frame-x`, 20px on a phone to 40px); 40px between the last rule
above a page's title and the title (`pt-10`), 32px from the title rule to the content (`mt-8`), 40px between
sections with 24px under each section's rule (`.form-section`); 20px
inside a card (`p-5`); 12px for a row (`.row`); 96px under the last
section (`pb-24`).

## Motion

Motion is for state, and only on colour, opacity and transform.

| Where | What | Duration, easing | Reduced motion |
|---|---|---|---|
| Buttons, links, nav | Colour on hover and press | 150ms, Tailwind's default ease | Stays (colour only) |
| `.dot--busy` | Opacity pulse | 2s, `cubic-bezier(0.4, 0, 0.6, 1)`, looping | Still (`motion-safe`) |
| Docs drawer | Slides in from the left | 200ms ease-out | None (`motion-reduce:transition-none`) |
| Docs heading anchor | Fades in on hover | 150ms | Stays (opacity only) |
| Docs "On this page" fold | Chevron turns half a turn | 150ms | None (`motion-reduce:transition-none`) |
| Docs "On this page" rail | A link scrolls the page to its section | The browser's smooth scroll | Jumps (`prefers-reduced-motion: reduce` checked on click) |
| Landing | Rails, ticks, the hero's bar, shapes landing, the snapshot mark, the pictures | `LANDING.md`, "Motion" | Every shape still and whole |

Anything that travels, turns or loops runs only under
`prefers-reduced-motion: no-preference`. A colour or opacity change of
200ms or less may stay for everyone. A transition names its properties:
Tailwind's `transition-colors` includes `outline-color`, and the focus
ring faded in with it.

## Icons

Inline SVG, no icon library: 16px on a 16 or 20 grid, 1.5 stroke, square
caps, no fill, in `currentColor` or zinc-500. Drawn ones today are the
select chevron, the search glass and the docs' fold chevron. A background
image icon disappears in forced colours; the forced-colours block drops
it or swaps in the native control.

## Logo

`Logo.svelte` is the I-363 mark (a thin cross in ink, three blocks in
grey, light grey and the accent) followed by "repose" in Noto Serif 600.
The mark is in every header (I-381):

- It links to `/` from every page, the dashboard included, named
  "repose, home", with a 40px hit area (I-655).
- 28px tall with the word from `sm` up. The word sits 1px under centre,
  so its baseline is the header links' baseline.
- Below `sm`: the 24px `sm` cut alone, so the header row fits 320px. The
  link carries the name. The footer shows the `sm` mark with the word at
  every width.
- Never under 24px. At 16px (the tab) use the favicon, a heavier cut of
  the same drawing (`static/favicon.svg`, `favicon.png`).
- The mark's grey blocks have their own greys so they hold 3:1 at header
  size: zinc-500 and `--control-edge` in the light, zinc-400 and
  `--control-edge` in the dark. The landing's large shapes keep
  `--sh-grey` and `--sh-light`. The big square is `--sh-accent`, the one
  `--sh-*` token that appears off `/` (I-363, I-393).

## Page frame and header

**The frame** (I-656). Every page is built in one column: the `frame`
utility in `layout.css` centres a box of `--frame-w` (1200px, gutter
included) and pads it by `--frame-x` (20px on a phone, 40px from 1334px
up). The header's row, the footer, `PageShell`'s main, the docs' frame,
the legal pages' main and the landing's rails use it, so the content
column is 1120px from 1280 up and the logo, the page's title and the
docs' sidebar all start at x=160 at 1440 and x=20 at 390 (7.5px less
where a classic scrollbar takes its gutter). No page sets its own
`max-w-*` frame or side padding. `html` keeps `scrollbar-gutter: stable`
and `scroll-padding-top: calc(var(--header-h) + 1rem)`, so a jump to a
heading lands 16px under the sticky header; the docs add their sub-bar's
44px below `lg`.

**The header** is `SiteHeader.svelte`, the one header (I-655). The root
layout draws it on every route at the first paint, before the sign-in
check answers. It is sticky on every page: 56px over a full-bleed
`--rule` hairline, its row on the frame. Left, the logo (see Logo).
Right, a `nav` named "Site" with, in this order on every page: Docs
(current on `/docs` and below), Pricing (`/#pricing`, hidden below
400px), Feedback (`https://repose.fider.io`), GitHub (from `sm`), and one
account slot. The links are `text-sm` in `--ink-muted`, ink under the
pointer, 16px apart below `sm` and 24px from it; the current page is ink
with a 1px ink edge lying on the header's rule, no bold shift and no
accent, and its focus ring is drawn inside the link. The account slot
is always a `.btn-quiet .btn--sm` of one minimum width: "Sign in" while
signed out or unknown (the prerendered HTML), "Dashboard" signed in on a
public page, and on a signed-in page the "Account" disclosure
(`AccountMenu.svelte`): a 16px chevron, and a panel on `--surface` with a
`--rule-strong` edge and no shadow holding Settings and Sign out in 36px
rows. It is a disclosure, not an ARIA menu: focus moves to its first
item, the arrows move between them, Escape, a click outside or a
navigation closes it. The URLs are in `lib/links.ts`.

**Under the header.** On signed-in pages `DashNav.svelte` is a 44px row,
not sticky, on the frame over a `--rule` hairline: Projects, Billing and
Settings in the header's link style. Projects carries a `.badge--warn`
with the number of questions waiting across every project, and its name
says it ("Projects, 1 question waiting"). A project's pages put
`ProjectTabs.svelte` (Overview, Config, Secrets) under the title on the
title's rule, through `PageShell`'s `tabs` snippet: links with
`aria-current`, drawn like the header's. On the docs below `lg` a sticky
44px sub-bar sits under the header: the Menu button and the open page's
title. No hamburger anywhere else.

## Footer

`SiteFooter.svelte`, on every page and under the landing's frieze on
`/` (I-655): a `--rule` hairline, the frame, 28px above and below, the
`sm` mark with the word on the left and, on the right, Docs, Pricing,
GitHub, Feedback, then Terms, Privacy, Refunds, in `text-sm`
`--ink-muted`. Below 768px the two groups are two columns, so the lines
align. The root layout keeps it at the window's foot on a short page.

# Dashboard

The signed-in pages: projects, a project and its config and secrets,
billing, and settings (which holds the account, I-578).

## Frame

`PageShell.svelte` wraps every page in a `<main>` on the frame, `pt-10
pb-24`. It spans the frame by default (`width="full"`, with `list` kept
as an alias); `width="form"` holds the content to `max-w-2xl`, flush
left, and a form page may put a second column beside it (I-664). It
draws the breadcrumb (`text-sm text-ink-muted`, `·` separators), the h1
(`text-4xl font-semibold tracking-[-0.01em]`), an optional lede, an
action (right of the title from `sm`, under it below), an optional
`tabs` row, then a `--rule` hairline. A page's h2s are `text-xl
font-semibold`, card titles included; no dashboard page uses h3 (I-375).

## The pages

What each signed-in page leads with (I-664). A change to a page's order
or its first screen goes through a DECISIONS entry.

- **Projects**: the questions agents wait on, as one accent-ruled
  callout; a stat strip (`PlanStrip`) of memory running, disk held and
  egress against the plan, or, with no plan limits, four facts from the
  list (memory, machines running, agents needing input, disk in use);
  the table; Recent activity; Recently destroyed. The table's name link
  covers the row, and its Start or Stop answers in place. Below `sm`
  rows stack, never scroll sideways.
- **A project**: two columns from `lg`, the work (Questions, Events,
  Usage) in the main one and the machine (Connect, Machine, Agents,
  Details, Snapshots) in a side one; Destroy last. Below `lg` one
  column in the order Questions, Connect, Events, Snapshots, Usage,
  Machine, Agents, Details, Destroy. Nothing mounts above painted
  content: what the first screen needs is fetched together. The
  Machine card is memory in use and disk as ring rows (the size's vCPUs
  and memory are on the status line); Usage's busiest processes are a
  full-width table folded under a `<details>` until opened (I-674).
- **Events** come in numbered pages, not an endless "Show older": 10
  rows under day headings, one pager (Newest, Newer, "Page n of m",
  Older) above the list (I-674), the place in the URL, and the
  filters as links with `aria-current`, drawn like the project's tabs.
  A disabled pager button keeps its place and says why in its title.
- **Billing**: the plan, its price once and its status, then memory
  as squares beside disk and egress as ring rows, then one band of
  facts (period, next charge, latest invoice) (I-674); plans as
  `PlanCells`. On a phone the plan's two actions share a row at equal
  width.
  Paged tables (invoices) use the same Newer and Older.
- **Settings, Config, Secrets**: a 280px label column beside the
  controls from `lg` (Settings), or the work in two thirds and its
  context in one (Config, Secrets). An action that saves sits in the
  footer of what it saves.

## Hairlines, not boxes

Borders use `--rule` for sections, rows and cards, `--rule-strong` for
badges, table heads and banners, and `--control-edge` for controls.
Sections separate with a top rule and spacing (`.form-section`, whose
first one on a page draws no rule under the title's; `.row`). `.card` is a
`--rule` box filled with `--surface`, one step off the page, with no
shadow: unfilled, a page of cards read as a wireframe (I-659). `.btn-quiet`, `.banner` and fields
keep a `--surface` fill, one step off `--page`, so a control reads as
something you can use. The Nix editor is the one field on `--sunken`: it
holds code, and code sits on `--sunken` everywhere; its `--control-edge`
border still marks it as a control. Its current line is one step lighter
than the fill: `--surface` in the light, and in the dark, where
`--surface` is the darker, `--sunken` with 5% `--ink` mixed in (I-393,
I-395).

## Buttons

- `.btn`: the inverted zinc primary, one per view.
- `.btn-quiet`: `--control-edge` border on `--surface`, for a secondary
  action that still needs a button's weight.
- `.btn-danger`: bordered red, for an action that cannot be undone.
- `.btn-ghost`: a muted word with a hairline underline in
  `--control-edge`, which turns ink under the pointer, for a secondary
  action. The resting underline says it can be pressed; grey and
  underlined, it cannot be taken for a link, which is blue (I-392).
- `.btn-ghost-danger`: the same in red, for a destructive action that can
  be reversed or that opens a confirmation.

Visual weight tracks consequence. Choices of equal weight (a question's
answers) are all `.btn-quiet`, never a row of primaries. Sizes (I-376):
the default suits a form; `.btn--sm` (`px-3 py-1.5`, at least 40px tall
below 640px, I-657) is for rows, toolbars and header bars; `.btn--lg` (`px-5 py-2.5`) is for a call to action that
stands alone on its row, the landing's "Get started" and "Start a free
week" and nothing else (I-397). No `!py-*` or `!px-*` overrides. Hover and press
(`active:`, one step darker than hover) apply to every button and to a
link drawn as one; a disabled button does not answer them. Never gate
them with `enabled:`, which no `<a>` matches (I-393). A
disabled `.btn`, `.btn-quiet` or `.btn-danger` has one look whatever its
kind: a `--rule-strong` outline on `--surface` with `--ink-faint` text
(I-391); `aria-disabled="true"` takes the same look, for a button that
must keep focus while it waits (LoadState's Retry). A ghost button at the
end of a row takes `-mr-2` (or `-mx-2`) so its word lines up with the
content edge, as on secrets, config revisions and snapshots. A word that
does something in a dense row takes the `hit` utility, an 8px margin that
takes the pointer without moving the layout (I-657).

## Fields

- `.field`: `rounded-sm`, `--control-edge` border on `--surface`,
  placeholder in `--ink-faint`. Focus is the house `:focus-visible` ring,
  2px `--focus`, offset 2px (I-372); a field does not restyle its border
  on focus.
- Every field has a visible `<label for>`, or an `aria-label` when the
  row's heading already names it (a search box). A placeholder is an
  example, never the label.
- An error is a `.field-error` paragraph under the field, with an id; the
  field carries `aria-invalid` and `aria-describedby` pointing at it.
- Selects are `select.field` with the drawn chevron.
- A boolean is a native checkbox (`accent-zinc-900`, dark zinc-100) and a
  text label on one line (`.check-row`). A pick-several list is a
  searchable list of `.check-list-row`s with the description in muted
  text.

## Badges, dots, tables, meters

- `.badge`: 11px mono on a `--rule-strong` edge, neutral. `.badge--new`
  emerald, `.badge--error` red, and `.badge--warn` amber for what waits
  on you or is over a limit (a question count, I-659). Nothing else; a
  tag like "temporary" is a plain `.badge`.
- `StateDot.svelte`: an 8px square before the state's word, which is
  always printed. Filled for running (emerald), error (red) and busy
  (ink, pulsing under `motion-safe`); hollow, edged in `--control-edge`,
  for stopped and destroyed (I-373).
- `.table`: hairlines, no fills, heads in `text-xs text-ink-muted`. A
  table that scrolls sideways on a phone sits in a `role=region` with an
  `aria-label` and `tabindex=0`, so a keyboard can scroll it.
- `Meter.svelte` (I-659): one series as a 6px bar on `--sunken` with a
  `--control-edge` edge, filled in the accent. Past the limit the bar's
  scale becomes the used figure: the share up to the limit stays blue,
  the overage is amber, and a 1px ink tick marks the limit, so "over" is
  an amount; the reading and `aria-valuetext` say "over" too. An optional
  `marker` draws a 1px `--ink-muted` tick (the period's pace on egress),
  described by `markerLabel`; `compact` puts the bar and the reading on
  one line for a table cell; `href` makes the label a link. It draws the
  project page's memory and disk. Charts beyond a meter need a DECISIONS
  entry.
- Plan limits (I-674): memory running is `MemoryBlocks.svelte`, the
  plan's squares (one per GB, 32 to a row, 16 below 640px) filled by
  each running machine in the accent, alternate machines at 55%, free
  squares outlined, past the limit amber. Disk held and egress are
  `UsageRing.svelte`, an 18px ring beside the "X of Y" figure. A page
  shows each limit once and draws no bar for it.
- Stat strips (I-659): `.stat-row` is a `--surface` box whose `.stat`
  cells are split by `--rule` hairlines (stacked, with horizontal ones,
  below `sm`). A cell is a `.stat-label` (12px, muted), a `.stat-value`
  (24px serif 600, tabular) and an optional `.stat-sub` or meter. A page
  leads with the figures its user came for.
- `UsageChart.svelte` (I-492, I-659): one measure over time as a 2px
  accent line over a 12% accent area, dashed hairlines at the top and middle, the top's
  value in `text-2xs` faint, "N hours ago" and "now" under it. One series
  per chart, so no legend; the label and an "now X, peak Y" reading in
  mono carry the numbers. Hover or the arrow keys show one point. A gap
  is a time with no data. No explanation under a chart: the public docs
  say what each measure means.
- A reading of a share is "X of Y" ("4 GB of 20 GB"), on a meter or in a
  card, never "X / Y".
- `CodeCopy.svelte`: one command on a `.codeblock` line with its own
  Copy button in the corner, "Copied" for 1.5s and in its own live
  region; a failure toasts.
- `.codeblock`: a command block on a dashboard page, 13px mono on
  `--sunken`. Its lines scroll sideways, as the docs' blocks do; a
  wrapped install line left "| sh" alone on the last line. As the docs'
  blocks do, it shows a 2px `--ink-faint` bar at an edge where a line
  runs on, and a block wider than its box takes a tab stop
  (`tabStopWhenScrolls`, I-401).

## States

Every view that loads or acts has each of these:

- **Loading**: `LoadState` draws a still skeleton in `--sunken` blocks
  (the title's bar where no title is on screen yet, a stat strip, four
  rows), no shimmer, and says "Loading…" to a screen reader in a
  `role=status` (I-659). The root layout draws the page-shaped one on a
  private page while the sign-in check runs. The h1 names the page
  ("Project"), never "Loading…".
- **Failed first load**: `LoadState.svelte` shows the error in a
  `.banner--error` (`role=alert`) with a Retry (`.btn-quiet .btn--sm`,
  "Retrying…" while it runs) that re-runs the same load. Only the first
  load goes there; a refresh that fails later keeps the content on screen
  and raises a toast (I-385), once, when the poll starts failing
  (`PollFailure` in `lib/api/toast.ts`, I-393), or, if the load banner or
  the outage bar was saying it then, on the first failed tick after it
  goes (I-394). A page keeps its load error until a load
  succeeds, so the banner and its button stay through a retry and keep
  the keyboard's focus; a breadcrumb waiting on the failed load's name
  reads "Project", not "…".
- **Empty**: an h2 that says so ("No projects yet"). If the screen has
  no way to add one, one sentence on how, with the command when the CLI
  is the way; if it has one (a form below), at most one sentence of
  fact the user needs, never instructions for the form (I-485).
- **Disabled**: the one disabled look (see Buttons; a ghost button fades
  to `opacity-50`) and `cursor-not-allowed`, and a sentence
  that says why when the reason is not obvious ("Stop todo-app first",
  "320 GB is the largest size."). If no choice is valid, the control is
  replaced by that sentence.
- **Pending**: while an action runs, its button is disabled and its label
  becomes the verb with an ellipsis ("Destroying…", "Re-applying…").
  One action at a time per page section.
- **Danger zone**: last on the page, an h2 in red at `text-xl`
  ("Destroy", "Delete account"), a sentence on what is lost, and
  `ConfirmType`.

## Confirmation

One pattern per consequence, and never the browser's `confirm()` (I-386):

- **Cannot be undone** (destroy a project, delete the account, restore a
  snapshot over the disk): `ConfirmType`. Its field has a visible label,
  "Type `slug` to confirm", with the word in mono; the `.btn-danger` stays
  disabled until it matches exactly, then turns the filled red of
  `.btn-danger--armed` (I-661); Enter confirms. The panel says what
  is lost.
- **A single deletion whose cost is recoverable** (a secret, cancelling a
  plan): an inline two-step in the row. The first button turns into a
  sentence naming the effect, a `.btn-danger .btn--sm` that does it and a
  `.btn-ghost` "Keep it".
- **Focus follows the panel** (WCAG 2.4.3, I-391). A panel that opens in
  place of the button that asked for it takes focus: "Keep it" in a
  two-step, the field in `ConfirmType` or `RestoreNameForm`, the size
  select of the Disk card's resize panel. Cancel or Keep it puts focus
  back on that button; a row that goes away passes it to the next row's
  button. A panel that closes on its own after a wait (a grow that
  finished) gives focus back only if focus was still inside it.
  `lib/focus.ts` has the two helpers.
- **Leaving unsaved edits**: the SvelteKit navigation is cancelled and a
  `.banner--warn` asks in place, with Stay (focused) and Leave. Stay puts
  focus back on the link that asked to leave, or on the unsaved field
  when the browser's Back asked (I-393).

No modals: the inline panel keeps what is being confirmed on screen.

## Tabs

A switch that swaps a panel in place without changing the URL is ARIA
tabs: `tablist`, `tab` with `aria-selected` and a roving tabindex,
`tabpanel` with `aria-labelledby`; arrows, Home and End move between
tabs. The keys are handled on the tabs, so the tablist needs no
tabindex. If only the current panel is rendered, only the current tab
carries `aria-controls`; an id that names nothing is a broken reference. The current tab looks like the header's current page: ink with a
1px underline, no weight change, no accent (I-388). A switch that changes
the URL is links with `aria-current`.

## Toasts

`svelte-sonner`, `theme="system"`, bottom-right, without `richColors`
(I-374). From 600px up the toast's right edge is the frame's content
edge, not the window's; on a phone it spans the page's 20px gutters
(`mobileOffset`), so its edges are the column's. A toast is a banner of its kind: `--surface` and `--ink` for
neutral, the `.banner--ok`, `--error` and `--warn` colours for typed
ones, 2px corner, a hairline, no shadow. Toasts report the result of
something the user did, and a failed refresh. A failure that leaves the
page with nothing to show is a banner, not a toast, and not both: one
failure is said once, in `errorText`'s words (I-390). A poll says it once
too: a toast on the first failed tick, none while the load banner or the
outage bar shows, and the toast dismissed when a tick gets through
(`PollFailure`, I-393). Only a toast counts as said: a tick that failed
quietly under the banner or the bar does not stop the next one, after
they have gone, from toasting (I-394).

## The outage bar

A strip in the `.banner--error` colours across the top of every page
while the api is down (08-dashboard.md 6): "Cannot reach the API.
Retrying…" when a request got no answer, "The API is failing right now.
Retrying…" on a 5xx. A 503 carrying one of the codes api.md lists as
answers ("Errors"; `ANSWER_503` in `lib/api/errors.ts`) is not an outage
and raises no bar (I-390, I-393). The bar is said in a `role=status`
region (`#outage`) that is always in the page and empty while the api
answers; only its contents change, so a screen reader announces the bar
when it comes (I-393).

## Keyboard

The first tab stop on every page is "Skip to content" (`routes/+layout.svelte`),
hidden until focused, which moves focus to the page's `<main>` (WCAG
2.4.1). `<main>` carries `id="main"` and draws no ring when the link
focuses it. `tests/design.spec.ts` drives it, the resize panel's focus,
Retry, forced colours and reduced motion (I-392).

## Forced colours

An unlayered `@media (forced-colors: active)` block in `layout.css` puts
back whatever carries a state in a fill or a coloured border (I-377):
state dots, the current nav item, the current tab (a 3px `Highlight`
edge, I-393), select arrows, button, badge and key
edges, disabled buttons in `GrayText`, and the meter fill
(`Meter.svelte`). A new component that shows state that way adds its rule
there and is checked with Chromium's `forcedColors: 'active'`.

## Controls

For boolean and pick-one settings (notification channels on or off, hold
base updates, default agent, size class) use plain controls in the same
palette, not chips or segmented groups:

- A boolean is a native checkbox and a label on one line (`.check-row`).
  One per line, aligned left.
- A pick-one with a handful of options is a `select.field`. A pick-one
  that needs a sentence per option is a vertical list of native radios
  with the help text under each; no card borders around options. No page
  needs one today, and the old `.radio-row` classes were deleted (I-378);
  build it again in `layout.css`, with forced-colour states, when one
  does.
- Pick-several (the config menu's package list) is a searchable list with
  a native checkbox per row and the description in muted text, not a wall
  of pills.

Chip-wrapped checkboxes, segmented pick-ones and card-sized radio options
stay rejected. So does a toggle switch until a page needs one (the unused
`.switch` was deleted with the rest).

# Docs and legal pages

They use the Foundation and the shared header. Prose is `.doc` in
`layout.css`: the site's palette over `@tailwindcss/typography`, with the
plugin's `--tw-prose-*` colours mapped to the ink and rule tokens and no
`prose-zinc` or `prose-invert`, whose cool greys and white headings are
off the palette. Every heading is 600, as on the dashboard, and the h1 is
the page title's `text-4xl` at every width. Inline code is a quiet chip at
body weight with no backticks; a span with no space in it (a command, a
flag, a path) of 30 characters or fewer never breaks across lines, a
span with spaces breaks only at them (`--api-url URL`, I-393), and a
longer word (a URL) wraps, so no chip pushes a phone's page sideways
(`docs.test.ts` holds the limit). The legal pages render inline code
with the same renderer (`lib/codespan.ts`, I-394). A command used as a heading is the
heading's own mono text, not a chip. Shell blocks are ink with muted
prompts and output, and no token hue: in the docs blue is a link (I-388).
A block or table wider than its box shows a 2px `--ink-faint` bar at the
edge where text is hidden, and takes a tab stop so a keyboard can scroll
it; one that fits has neither (I-394, I-395). The bar is a one-colour
layer. On a phone a command in a table wraps at its spaces,
so the description beside it keeps its width.
The copy button is 28px tall and says "Copied" to a screen reader through
a live region as well as on its face. Links are in
the accent; h2 sections are separated by a rule. Prose is `text-prose`, 17px at 1.7, and the title 36px, 40px under the
last rule above it (I-660). Prose, code blocks, tables and the h2 rules
share one right edge: one measure. The docs frame is inside the shared
frame (I-656, I-660): from `lg` a 220px sidebar, 48px, then the page; from
`xl` the text, 48px and a 180px "On this page" rail at the frame's right
edge, so at 1440 the sidebar is at x=160, the 624px text at 428 and the
rail at 1100. Below `xl` the text holds to 42rem. The sidebar lists pages
and nothing else, under a search that sticks to its top. From `lg` up it
draws no scrollbar (one flashed over it on the first visit, I-660) and
fits 1440x900 whole: 26px rows, labels 16px apart, the Tutorials group
folded except on a tutorial's page, a rule under the search once the
list scrolls, and a 24px fade at its foot while more is below. When it
must scroll to show the open page, the page's group label comes to the
top. The rail lists the page's h2s and, under each, its h3s, one step
in. On a phone the drawer, opened from the sub-bar's Menu, opens
scrolled to show the current page's link, and the list scrolls under the
search (I-400).
The rail's column is drawn on every page, empty when a page has one h2
or none, so the sidebar, the text and the rail sit at the same x on
every page. The rail and the sidebar stick at 57px, under the header and
its hairline, and a long rail scrolls on its own. The section you are
reading, the last h2 above a line a quarter of the way down the window,
is ink with a 1px ink edge on the rail's hairline (an underline in
forced colours), `aria-current="true"`; the others are `--ink-muted`.
Above the first h2 the first link is marked, so the rail always says
where you are (amends I-400). A section's h3s show under it, 13px and
one step in, while it is the one being read; on the CLI reference all
30 show, by command name ("repose run").
A rail link scrolls smoothly, or jumps under reduced motion. Its links
are 28px tall, over the 24px target size (I-391). Below `xl` the
sections fold under the description in "On this page".
Legal pages (I-658) sit on the docs' tracks, so a policy's text is
where a docs page's text is: from `lg` a 220px "Policies" column
(Terms, Privacy, Refunds, in the sidebar's look, sticky at 57px), then
the text; from `xl` the 180px "On this page" rail at the frame's right
edge, in the rail's type and 28px rows, marking the section being read.
Below `xl` the text holds to 42rem and the sections fold into "On this
page" under the effective date; below `lg` the three policies are one
line of links there.

# The landing's exception

The landing adds a shape set in the `--sh-*` tokens (zinc greys, ink and
the blue accent), a palette for the pictures it draws in the `--pic-*`
tokens (the blue, three greys, and a diff stat's red and green; I-397),
a blue bar under its headline and prices, a sphere drawn as a flat disc
with a globe's meridians over it where it stands for the internet, its
own display sizes, and its own page grammar (rails, ticked rules, stages, cells) in
`apps/web/src/routes/landing.css`. Those exist only on `/`, save the
logo's `--sh-accent` square in every header (see Logo) and the plan
cells (`lib/plans.ts`, `PlanCells.svelte`), which draw the Units
picture and the blue price rule on `/billing` as on `/#pricing`, so the
two cannot disagree (I-663). They are described in `LANDING.md`. Every other page follows this document with no
exception.

# Where it goes

`apps/web/src/routes/layout.css` for tokens and classes; `app.html` for
the font preloads and the pre-CSS colours, which must match `--page` and
`--ink`. A new class goes in `layout.css` with a comment that says why,
in full sentences, like the rest of the file. `CHECKLIST.md`, "Design",
has the greps that catch drift from this document.
