# Design language

repose's web app reads as one Herakraft product: Noto Serif 400 for display
only, Inter 15px for everything else, Geist Mono for commands, zinc neutrals
with one blue for links, current items, eyebrows, focus and chart emphasis,
hairline rows on the page colour, and near-black code panes in both themes.

Herakraft UI (Guide B, `branding/docs/ui-guide/GUIDE-B-herakraft-ui.md`) is
the parent spec. This file records what repose took from it, the numbers it
ships with, and where it departs and why (DECISIONS I-837). The code is in
`apps/web/src/lib/hk/` and `apps/web/src/routes/layout.css`; when this file
and the code disagree, one of them is wrong, and a change to the rules goes
through a DECISIONS entry. `CHECKLIST.md` "For every change to the web
app's look" holds the greps that catch drift. The gallery at `/design` on a
`vite dev` server draws every component below on one page (it is a 404 in a
build).

# Foundation

## Tokens and scope

`lib/hk/tokens.css` is the kit's file, copied byte for byte; a kit update is
a file copy. `lib/hk/repose.css` holds every repose addition, each rule with
its reason. `routes/layout.css` imports both into Tailwind's components
layer, so a utility on the same element still wins (`class="hk-btn h-10"` is
40px), and a component's own `<style>`, which is unlayered, beats both.
`<body class="hk">` puts every page inside the kit's scope.

Colour in markup comes from three places only: an `hk-*` class, a token in a
component `<style>` (`var(--hk-text-3)`), or one of the theme utilities below.
No Tailwind palette utility (`text-zinc-500`), no hex outside `lib/hk/` and
the first-paint pair in `layout.css`, and no `prefers-color-scheme` block in
a component: the tokens already follow the theme.

| Utility suffix | Token | Light | Dark |
|---|---|---|---|
| `page` | `--hk-bg` | `#fafafa` | `#09090b` |
| `surface` | `--hk-surface` | `#ffffff` | `#111113` |
| `surface-2` | `--hk-surface-2` | `#f4f4f5` | `#18181b` |
| `hairline` | `--hk-hairline` | `#ececee` | `#1c1c1f` |
| `line` | `--hk-border` | `#e4e4e7` | `#27272a` |
| `line-strong` | `--hk-border-strong` | `#d4d4d8` | `#3f3f46` |
| `ink` | `--hk-text` | `#18181b` | `#f4f4f5` |
| `ink-2` | `--hk-text-2` | `#52525b` | `#a1a1aa` |
| `ink-3` | `--hk-text-3` | `#636369` | `#8b8b94` |
| `control` | `--hk-control-edge` | `#8b8b94` | `#71717a` |
| `accent` | `--hk-accent` | `#1d4ed8` | `#60a5fa` |
| `ok`, `warn`, `err` | `--hk-ok`, `--hk-warn`, `--hk-err` | 700 to 800 steps | 300 to 400 steps |

`--hk-text-4` (`#a1a1aa`, dark `#5f5f68`) is for placeholders and list
markers and is never readable text. The status utilities colour text on
their own washes (banners, badges) and nothing else.

Tokens repose adds in `repose.css`: `--hk-container` 75rem, `--hk-gutter`
`clamp(1.25rem, 3vw, 2.5rem)`, `--hk-header-h` 57px at every width,
`--hk-control-edge`, the three font stacks with their fallback faces,
`--hk-shadow-terminal`, `--hk-busy` and `--hk-busy-halo` for the busy dot,
and `--hk-on-err` for text on the armed red. `layout.css` keeps three
variables on `:root` for code outside `.hk`: `--frame-w`, `--frame-x`,
`--header-h`, with the same numbers.

## The accent

Blue has five jobs: a link, the current item (header, docs sidebar, rail),
an eyebrow, the focus ring, and the emphasis in a chart or a meter. The
logo's square is the sixth, and the one brand use. A primary button is
inverted ink, so blue on a page always means "this navigates" or "you are
here". Text selection is the kit's blue wash.

## Status colour

Amber marks what needs the user, red what went wrong (Guide B 5.3). Both
appear only in a badge, a dot, a banner, the outage bar and the error toast.
A line that reports a status is `--hk-text-2` text with a 7px dot before it;
a line that states a refusal is a banner. A running machine has no colour:
its dot is filled ink. The kit's green `--ok` dot is not used for a machine
state; `--hk-ok` appears only on the success banner (`seat-held`, the
billing-exempt line).

## Contrast floor

Text is 4.5:1 on its ground and a control's edge or a state mark is 3:1
(WCAG 1.4.3, 1.4.11). `--hk-text-3` is AA at 11px on the page in both themes.
The kit's input edge, `--hk-border`, is 1.27:1, so every input, select and
textarea takes `--hk-control-edge`, 3:1 in both themes. The selected item of
a joined control carries a 1px `--hk-control-edge` ring in light, so
selection is never a 1.3:1 fill alone. A meter's fill can sit under 3:1
(amber is 2.06:1 on the light page); its value text and `aria-valuetext`
carry the number. `pnpm a11y` audits 13 pages in both themes at 1440 and 390.

## Touch

Below 640px, or under a coarse pointer, every control a thumb hits is at
least 44px tall: buttons of every kind and size, inputs, selects and
textareas (with 16px text, so iOS does not zoom on focus), every item of a
joined control, checkbox rows, page tabs, crumb links, copy buttons, and the
header's links, which take the full 56px row. A ghost action in a table row
keeps the row's height by a negative block margin (`hk-btn-ghost--touch`).
Links inside running text are exempt (WCAG 2.5.8's inline exception).
`design.spec.ts` "touch targets" measures `header` and `main` of nine pages
at 390 with touch.

## Type

Three families, all self-hosted in `apps/web/static/fonts/` with their OFL
licences, so the CSP's `font-src 'self'` holds (I-371):

- **Inter**, the fontsource variable build (opsz 14 to 32, wght 100 to 900;
  400, 500 and 600 are used). It has no `cv11` or `ss01`, so the kit's
  `font-feature-settings` line is inert, as the reference renders it.
- **Geist Mono** 400, 500 and a real 400 italic for code comments. Contextual
  alternates are off on `html`, so `://` in an install URL is never spaced.
- **Noto Serif** 400 for every heading, 600 for the wordmark only.

Each webfont has a local fallback face (`Inter Fallback` over Arial, `Geist
Mono Fallback` over Courier New, `Noto Serif Fallback` over Georgia) scaled
with capsize metrics, so text painted before the font arrives wraps where it
will after. `app.html` preloads Inter latin, Noto Serif 400 latin and Geist
Mono 400 latin; latin-ext loads only for a page that has such a character.

Every `h1`, `h2` and `h3` is the serif at 400 (the kit's `.hk :where(h1, h2,
h3)`). Weight stops at 600, and 600 is only for small labels (eyebrows,
footer heads, docs group titles). A label that must be a heading element for
a test (a plan name, an empty row's title) sets `font-family:
var(--hk-font-sans)` on its own class, or uses `hk-empty-title`.

| Role | Class | Size / line | Notes |
|---|---|---|---|
| Landing headline | `hk-h1` | 40 to 56px / 1.05 | -0.025em, `max-width: 16ch`, balanced |
| Docs, legal and error titles, landing section heads | `hk-h2` | 28 to 36px / 1.12 | -0.02em |
| Landing feature cells | `hk-h3` | 24px / 1.3 | |
| Dashboard page title | `hk-h1-app` | 24px / 32px, 30px / 36px from 640px | PageShell's `h1` |
| Dashboard section head | `hk-h2-app` | 20px / 28px | -0.01em |
| Docs section head | `.doc h2` | 24px / 31px | |
| Lead | `hk-lead` | 17px / 1.6, `--hk-text-3` | `<strong>` is the claim in ink at 500 |
| Body | | 15px / 1.6 | Inter, the kit's base |
| UI text, buttons | | 14px, 13px in a small button | 500 on buttons |
| Caption, label | `hk-label` | 12px / 16px, `--hk-text-3` | |
| Eyebrow | `hk-eyebrow` | 11px, 600, 0.16em, uppercase, accent | sans even on an `h2` |
| Code in panes | | Geist Mono 13px / 20px | 12px / 20px in the hero terminal |
| Inline code | `.hk code` | 0.85em Geist Mono on `--hk-surface-2` | 5px radius, no border |

Figures that line up use `hk-num` (tabular). No Tailwind `text-[…]` sizes in
markup.

## Corners

| Radius | Token | Used by |
|---|---|---|
| 4px | `--hk-radius-xs` | focus outline, kbd, skeleton bars |
| 5px | `--hk-radius-sm` | badges, inline code |
| 6px | `--hk-radius-6` | segmented thumb and items, docs sidebar items |
| 7px | `--hk-radius-7` | small buttons, the Account pill |
| 8px | `--hk-radius` | buttons, inputs, banners, toasts, menus, the inline confirm, command lines |
| 9px | `--hk-radius-9` | segmented track |
| 12px | `--hk-radius-lg` | code panes, the terminal, plan cards, panels |
| 14px | `--hk-radius-xl` | kit modals (unused) |
| pill | `--hk-radius-pill` | dots, meter track |

Tailwind's `rounded-*` steps map onto these values (`rounded-lg` is 8px), but
markup uses the tokens. Square corners belong to memory squares and the
outage bar.

## Elevation

Three levels. Flat on the page colour, for nearly everything. A 1px ring or
inset edge, for code panes, the selected item of a joined control and the
info banner. A shadow (`--hk-shadow-pop`) for the Account menu, toasts and a
chart's tooltip; in the dark it is a 1px `#27272a` ring plus a deeper drop,
since a shadow alone is invisible on `#09090b`. The hero terminal adds
`--hk-shadow-terminal`, the one drop shadow on a pane. Nothing else casts
one, and no surface has a gradient.

## Spacing

Guide B 10.1 on repose's frame:

| Distance | Value |
|---|---|
| Page top to title | 1.75rem, 2.25rem from 640px (PageShell) |
| Title block to content | 1.75rem |
| Between dashboard sections | 2.75rem |
| Section head to its content | 0.875rem |
| Row padding | 0.75rem (facts), 0.875rem (kit rows, questions) |
| Table row, skeleton row | 56px |
| Landing sections | 4.5rem from 1024px, 3.5rem below |
| Page bottom | 6rem |
| Banner to what follows | 1.5rem |

## Motion

One curve, `--hk-ease` (`cubic-bezier(0.2, 0, 0, 1)`), and the kit's named
durations: 150ms for colour, border and background; 160ms for the copy icon;
200ms for a chevron; 220ms for the segmented thumb; 240ms for a terminal line;
260ms for the drawn check; 400ms for a meter's fill. A transition names a
token, never a number. Nothing moves on hover or press: buttons change colour
only. Nothing fades in on scroll. The busy dot pulses at 1.6s. The hero
terminal plays once and stops on a still caret, so it needs no pause control
(WCAG 2.2.2). Under `prefers-reduced-motion: reduce` the busy dot holds
still, the terminal shows every line, the thumb jumps, and
`document.getAnimations()` is empty on `/`, `/projects` and a project page
(design.spec, landing-motion.spec).

## Focus

`:focus-visible` is a 2px `--hk-accent` outline 2px out, 4px corners.
Where a container would clip an outset ring the ring goes inside: header
links, page tabs, a scrolling table wrapper and a joined control's items
take `outline-offset: -2px`. A table row's link draws its ring on the row's
`::after`, which covers the row. A code pane or the Nix editor shows a 2px
inset `--hk-code-focus` ring. `<main id="main" tabindex="-1">` is the skip
link's target and draws no ring. The skip link, `hk-skip`, is the first tab
stop on every page.

## Icons

Inline 16x16 SVG, `stroke: currentColor`, 1.4 stroke, round caps, `aria-hidden`
beside a word. No icon library. The snowflake (`Snowflake.svelte`) and the
copy and check glyphs follow the same rule.

## Logo

The cross-and-blocks mark, its strokes in `--hk-text` and its large square in
`--hk-accent`, then "repose" in Noto Serif 600 18px at -0.01em (16px in the
footer). The link's name is "repose, home". Below 640px the header shows the
mark alone.

## Frame and header

Every page is built in one frame (I-656): a 1200px box, its gutter included,
centred, with a gutter of 20px on a phone growing to 40px. The `frame`
utility applies it to the header row, the footer, every `main`, the docs and
the landing. At 1440 with a classic scrollbar the content box is 1120px wide.
The kit's 76rem container and 2rem gutter are not used: the docs columns and
the tests' geometry depend on 1200.

The header is one 56px row and a 1px `--hk-hairline` rule, 57px, sticky at
the top at every width, on `--hk-bg` at 88% with a 10px blur. The kit's
stacked, non-sticky phone header is not used: the docs sub-bar is pinned to
end at 101px, and the item counts design.spec reads depend on one row.

- Signed out: the logo, then Docs, Pricing (hidden below 400px), Feedback,
  GitHub (from 640px) and Sign in. Signed in on a public page, the slot is a
  Dashboard link in the same small secondary look.
- Signed in on a dashboard page: the logo and the Dashboard nav (Projects;
  Billing, hidden below 360px; Settings, hidden below 640px), then Docs,
  Feedback (from 640px), GitHub (from 640px) and Account.
- Header links (`hk-navlink`) are 14px `--hk-text-2`. The current one is the
  accent with a 1px accent edge lying on the header's rule, weight unchanged,
  `aria-current="page"`; the edge is the cue that does not depend on colour.
- The Projects link carries a `hk-badge--warn` count of waiting questions,
  and its name says it ("Projects, 2 questions waiting").
- The account slot keeps one width for its three forms. Sign in is a small
  secondary button. Account is a 32px pill with a chevron that opens a menu
  on `--hk-surface` with the pop shadow: the email on its first line, then
  the places the header hides at that width, Settings and Sign out.

There is no left rail on the dashboard. Three places do not need a column,
and a rail with its gap takes 248px from the projects table and the project
page's two-column rows. On a project page the header marks the place
(Projects, in the accent) and the page tabs mark the view (in ink).

## Footer and the theme switch

The full footer (landing, docs, legal) sits on a hairline, `3rem 0 2rem`:
the logo, then two columns, **Product** (Docs, Pricing, llms.txt, GitHub,
Feedback) and **Policies** (Terms, Privacy, Refunds), with 12px 600 heads
and 13px `--hk-text-3` links. Below them, on a hairline, the theme switch.
The compact footer (dashboard pages, and error pages for a signed-in user)
is one row, `1.25rem 0`: Docs, Feedback, Terms, Privacy and Refunds left,
the theme switch right. Both put their links in `nav[aria-label="Footer"]`.

The theme switch (`ThemeSwitch.svelte`) is a small segmented radio group
named "Theme": System, Light, Dark. It lives in the footer and nowhere else.
Light or Dark is stored in this browser (`localStorage` `hk-theme`) and set
as `data-theme` on `<html>`; System removes both and follows the OS.
`lib/theme.svelte.ts` reads nothing at import, so prerendered pages render
System checked and show the stored choice once they run, at the same size.

## First paint

`app.html` paints the page before any CSS arrives: `html` gets `#fafafa` and
`#18181b`, or `#09090b` and `#f4f4f5` in the dark by media query or by
`data-theme`, with `color-scheme` set so the scrollbar and native controls
match. Its one inline script reads `hk-theme` and sets `data-theme` and both
`theme-color` metas before the body paints. The body stays hidden until
`layout.css` applies (its last, unlayered rule), so no page paints unstyled
(I-331). The script's sha256 sits in `svelte.config.js` `script-src`;
`app-html.test.ts` fails when the script changes and the hash does not, and
when the first-paint pair drifts from `--hk-bg` and `--hk-text`.

# Components

Every class here is `hk-` prefixed. The kit's come from `tokens.css`;
those marked (repose) are in `repose.css`. Svelte components are in
`lib/hk/` and `lib/components/`.

## Buttons

- `hk-btn`: the primary, inverted ink (`#18181b` with `#fafafa` text; the
  reverse in the dark). One per screen.
- `hk-btn-secondary`: surface, 1px `--hk-border`, ink text.
- `hk-btn-ghost`: no fill, `--hk-text-2`, a wash on hover. Row actions and
  "Keep it".
- `hk-btn-danger`: transparent with a red edge at 35% and red text. For an
  action that cannot be undone. Weight follows consequence, so it is never
  ghost.
- Sizes: default 36px / 14px / 8px corners; `hk-btn-sm` 30px / 13px / 7px
  corners; `hk-btn-lg` 40px for the hero and pricing calls to action only.
  All are 14px Inter 500 or smaller and 44px on touch.
- Hover and press change colour only, 150ms. Pressed is one step past hover.

**Waiting** (repose). A button whose request is in flight keeps focus and
says so in its label ("Starting…", "Keeping…"): `aria-disabled="true"`,
never `disabled`, which drops focus to `<body>` (I-766). The primary takes
`--hk-disabled-bg` and `--hk-disabled-text`; the others turn `--hk-text-3`
at full opacity. The face text equals the accessible name.

**Disabled** (repose). A true `disabled` is only for Save with nothing to
save, ConfirmType before the name matches, and a pager at its end. It is
flat `--hk-text-3`, never faded (settings-account.spec reads its colour).

**Armed** (repose). `hk-btn-danger--armed` is ConfirmType's button once the
typed name matches: a solid `--hk-err` fill with `--hk-on-err` text, so the
moment the action becomes possible is visible (I-661).

**Danger text** (repose). `hk-btn-text-danger` has ghost geometry and red
text, for one reversible destructive action at a page's end ("Cancel plan",
undone by Resume). Never in a table row (Guide B anti-pattern 19).

## Badges

`hk-badge`, 20px, 11px 500, 5px corners, with `--warn`, `--err`, `--ok`,
`--info`, `--mono` and `--outline`. A badge marks an exception: a count of
waiting questions (`--warn`), "temporary", the "Current" plan and a
"refunded" invoice (`--outline`). Normal states (paid, applied, manual) are
plain words.

## Dots and states

`hk-dot` is 7px, always beside a word, `aria-hidden`. `StateDot.svelte` maps
a guest state to it:

| State | Dot | Class |
|---|---|---|
| running | filled ink | `hk-dot--on` (repose) |
| creating, building, starting, stopping, restoring, destroying | `--hk-busy` with a 3px halo, pulsing | `hk-dot--busy` (repose) |
| stopped, destroyed, unused | hollow 1.5px `--hk-control-edge` ring | `hk-dot--idle` (repose) |
| needs input, host unreachable | amber | `hk-dot--warn` |
| error | red | `hk-dot--err` |
| frozen | the 12px snowflake and "frozen" in `--hk-text-2` in place of dot and word | `data-frozen` |

A frozen machine has no band, no cyan and no row fill; frozen is a normal
state of a running machine. A dot keeps `data-tone` and its parent
`data-state` for tests and stylesheets.

## Fields

`hk-input`, `hk-select` and `hk-textarea` are 36px, 8px corners, 14px text,
with the 3:1 `--hk-control-edge` border (repose). Focus is a 55% accent
border and a 3px `--hk-ring` halo. `aria-invalid="true"` turns the edge red
and `hk-field-error` says why under the field. A disabled field takes the
plain border. `hk-input--search` (repose) adds a magnifier on the left in
`#71717a`, which reads on both themes. Labels are `hk-field-label`; fields
stack in `hk-fields`. Inputs are `width: 100%`, so markup sizes them
(`max-w-80`).

## Checkboxes

`hk-check` is a label around a native 16px box with `accent-color` the
accent, 14px text, 24px tall and 44px on touch (repose). Save-on-change
settings toast their result; fields with a Save button say "Not saved yet."
while dirty (I-332).

## Joined controls

All share one look: a `--hk-surface-2` track with 3px padding and a 1px
inset hairline, items 30px (24px with `hk-seg--sm`) at 13px 500
`--hk-text-3`, and the selected item on `--hk-surface` in ink with the 3:1
ring. In the dark the selected item is `#3f3f46` with a `#a1a1aa` inset ring
and `#fafafa` text. Focus is a 2px accent outline inside the item, so the
track never clips it. In forced colours the selected item takes a 2px
Highlight outline.

- `hk-seg` with `Segmented.svelte`: native radios under a sliding thumb
  (`hk-seg--slide`, `hk-seg-thumb`, repose). For a choice of four or fewer
  short options: the theme switch. Before it runs, the checked label is
  raised and no thumb shows.
- `hk-seg--links` (repose): a `<nav>` of links with `aria-current="page"`,
  for filters that live in the URL: the projects filter and the event kinds.
- `hk-seg--buttons` (repose): `button[aria-pressed]`, for the Usage range
  (Hour, Day, Week).
- `hk-tablist` (repose): `role="tablist"` drawn as a small track, so it never
  reads as the page tabs above it. Config's Menu and Nix. One tab stop, the
  selected tab; the arrow keys move between tabs. In forced colours the
  selected tab has a 3px CanvasText bottom edge and the others a 3px Canvas
  edge, so nothing shifts.

## Page tabs

`hk-tabs` (repose): a `<nav>` of links on the full-width hairline PageShell
draws under its `tabs` snippet, 1.5rem apart, 40px tall (44px on touch), 14px
`--hk-text-2`. The current tab is ink with a 1px ink edge lying on the rule,
weight unchanged. A project's Overview, Config and Secrets.

## Tables

`hk-table`: 13px, a 12px `--hk-text-3` header over `--hk-border`, hairline
rules between rows, outer cells flush with the frame, numbers right-aligned
and tabular (`hk-num-col`). `hk-table--hover` washes the row under the
pointer, and a row's main link covers the row with its `::after`; any action
in the row sits above it (`position: relative; z-index: 1`). A table that
may scroll sideways sits in `hk-table-wrap` (repose) with `role="region"`, a
name, and a tab stop while it overflows (`tabStopWhenScrolls` in
`lib/scroller.ts`); its 4px inline padding, pulled back by a negative margin,
keeps a focus ring on the first or last column visible while the text edge
stays put. Below 640px a table with row actions stacks each row into two
lines, its action on the right of line 1.

## Facts

`hk-facts` (repose) is a `<dl>` of read-back facts: each `div` a hairline
row, `dt` 13px `--hk-text-3` in a 9rem column, `dd` 14px ink (add `hk-mono`
for an id). Below 640px `dt` sits over `dd`. Inside `hk-section` the first
row drops its top rule, so one rule sits where one belongs. `hk-facts--row`
sets the facts side by side between two hairlines, `dt` 12px over `dd` 15px:
Billing's current plan (price, status, next charge).

## Stat strip

`hk-stats` with `hk-stat`, `hk-stat-label`, `hk-stat-value` and
`hk-stat-unit`: hairlines above and below, no tiles, three or four columns
from 900px, labels that wrap and reserve two lines. repose shows a stat strip
only for a figure it can read at render time; the dashboard has none today,
and the landing has none because no public figure is both live and
reproducible.

## Meters and memory squares

`Meter.svelte` on the kit's `hk-meter`: the label left, the value right in
500 tabular ink, a 6px pill track (`hk-meter-track`) and its fill
(`hk-meter-fill`) under them. `role="meter"` is named by the label, with
`aria-valuetext` the value text ("24 GB of 32 GB", "112.4 GB of 100 GB ·
over"). The fill is the accent while comfortable, amber under 20% left, red
under 5% left or past the limit, and `--hk-text-4` with a quiet value when
nothing is counted yet, so a first run raises no alarm. The fill's width
moves in 400ms and holds still under reduced motion. In forced colours the
track draws a GrayText edge and the fill CanvasText (repose).

Memory is squares, one per GB (`MemoryBlocks.svelte`), passed into a Meter
through its `track` snippet so every plan figure shares one anatomy. Squares
are 10px on /billing and 8px in the projects plan row, 2px apart, square
cornered. One machine's run is the accent; two are the accent and
`--hk-text-2`; three or more take `--hk-series-1` to `-3`, then `--hk-text-2`,
with a 12px legend naming each machine. Frozen GB are hatched in
`--hk-text-3`, GB past the plan are `--hk-warn-dot`, and a free GB is a 1px
`--hk-control-edge` outline. In forced colours running GB are CanvasText,
frozen GrayText, free an outline.

## Code pane

`CodeBlock.svelte`: near-black in both themes (`--hk-code-bg` `#0c0c0e`,
`#0e0e10` on a dark page, where a 1px outer ring separates it), 12px
corners, an inset `--hk-code-border`, a 42px bar when it has a title or tabs,
Geist Mono 13px / 20px. Shell blocks show `$ ` and output in
`--hk-code-muted`, commands in `--hk-code-text`, `# ` comments in italic.
Copy copies the commands only. Line numbers appear past eight lines, never in
docs panes, so the 70-column rule holds there. A pane that scrolls takes a
tab stop and shows a 2px inset `--hk-code-focus` ring when focused. Strings
are blue-300 and numbers amber-300; every other token is a zinc step, so the
pane is never the loudest thing on the page.

## Command list

`CommandList.svelte`: two to four commands in one near-black pane, a 40px row
each: a muted `$ `, the command in `--hk-code-text`, and its own dark small
Copy named "Copy {what}". A `# note` above a command is 12px italic and wraps
on a phone while the command scrolls sideways; Copy lines up with the command
line. A row takes a tab stop only while it overflows. Used where a page
offers several commands together: Connect on a project page, the empty
/projects state.

## Command line

`CodeCopy.svelte`: one lone command on a light page, in a `--hk-surface-2`
box with a 1px border and 8px corners, Geist Mono 13px ink, an optional muted
`$ `, and a page-tone small Copy at the end. On touch the box is 46px tall,
so the 44px Copy sits inside it edge to edge. Long commands scroll sideways
inside the box. A page keeps one dark anchor, so a lone command is light.

## Copy button

`CopyButton.svelte`: sizes sm, md and lg (24, 28, 40px; all reach 44px on
touch), tones `dark` for a pane and `page` for the page. The face says what
it does ("Copy", "Copy install command"), and its accessible name starts with
the face ("Copy the install command"). On copy the icon becomes a check drawn
in 260ms and a polite live region says "Copied"; the width never changes. A
copy button's text is exactly what the pane shows and runs where the reader
pastes it (Guide B 1.10).

## Terminal

`landing/Terminal.svelte` is the landing's one terminal, described under The
landing. It is the only pane with a drop shadow.

## Banners

`hk-banner`: 8px corners, `0.75rem 1rem`, 14px, one sentence and at most one
action, 1.5rem above what follows. The base is amber (warn) for something
that needs you or a refusal; `hk-banner--err` for something that went wrong,
`role="alert"`; `hk-banner--ok` for a confirmed good state; `hk-banner--info`
(repose) for a neutral fact (`--hk-surface-2`, ink, an inset hairline). Links
inside are underlined at 35%. `hk-banner-action` (repose) draws a `<button>`
as that inline link where the action must be a button ("Update card" opens
the payment portal). One banner per fact, and the page does not repeat it in
a status line. In forced colours a banner draws a CanvasText edge.

## The outage bar

`#outage` in `routes/+layout.svelte`: a full-bleed strip above the header,
`--hk-err-wash` with `--hk-err` text at 13px 500, centred in the frame, square
cornered. It says the API cannot be reached. A page's failed-load banner
shows under it too, since the bar is about the API and the banner about what
this page could not load (I-390, I-393).

## Empty row

`hk-empty-row`: one line of 14px `--hk-text-3` on the left and the one action
(a guide link or the primary) on the right, between hairlines. The section
head's own button hides while the empty row offers it. A heading a test pins
goes inside as `h2.hk-empty-title` (repose, sans 14px 500 ink).

## Loading and failure

`LoadState.svelte`. Loading is a still skeleton (`hk-skel` bars on
`hk-skel-row` rows, repose): `shape="page"` draws a title bar in a box the
height of the h1, so the swap to the page moves nothing, then four 56px rows;
`content` four rows; `rows` three. A screen reader hears the label once
(`role="status"`).

### Failure

A load that fails becomes `hk-banner--err` with `role="alert"`, the reason,
and a small secondary "Retry" that reads "Retrying…" with `aria-disabled`
while it runs, focus kept. A poll that fails says so once: the toast comes on
its first failure and not again until it has recovered (`lib/api/toast.ts`,
I-393).

## Toasts

svelte-sonner, mounted with the resolved theme and no `richColors`. Every toast
is neutral (`--hk-surface`, 1px `--hk-border`, the pop shadow, 8px corners,
13px ink) except an error, which takes the err wash and text like its banner.
A page with a bottom bar lifts the toasts above it.

## Bottom bar

`hk-bottom-bar` (repose): a sticky foot with a `--hk-border` top edge, the
page colour at 90% with an 8px blur, and the safe-area inset. Config's Apply
lives there.

## Inline confirmation

A confirmation opens in place, under the control that asked for it, and
focus moves into it and back when it closes (`lib/focus.ts`, I-386, I-391).
No modal dialog and no `confirm()`.

- `hk-confirm` (repose) with `hk-confirm-actions`: `--hk-surface-2`, 8px
  corners, one sentence naming the target and the consequence, then the
  danger verb (`hk-btn-danger hk-btn-sm`) and a ghost "Keep it" or "Keep it
  running" on one row. Stop with an agent mid-turn, Delete a secret, Cancel
  plan.
- `ConfirmType.svelte` for what cannot be undone: the label "Type `slug` to
  confirm", a mono input, and a danger button that stays `disabled` until the
  exact name, then arms. Enter confirms. `cancelLabel` names the ghost
  ("Keep it"). One row from 24rem of its own width.
- Reveal, then type: Destroy and Delete account first show one danger button
  ("Destroy job-board…", "Delete account…"); pressing it puts ConfirmType in
  its place with focus in the field. A page never carries an open
  destroy field on every visit.

## Error pages

`ErrorView.svelte` owns the page's one `main`: the status as a Geist Mono
eyebrow ("404"), `hk-h2` "Not found" or "Something went wrong", an optional
line or form, then "Back to Projects" for a signed-in user or "Docs" and
"Home" otherwise. No lead sentence and never a framework message. The root
`+error.svelte`, the layout's error boundary (with Reload) and a missing
project all use it, inside the site's header and footer. An unknown path
renders it for everyone; only `/projects`, `/billing` and `/settings` send a
signed-out visitor to `/`.

## Forced colours

Every mark that carries state in a fill or a coloured edge has a rule in the
`forced-colors` block of `layout.css` or `repose.css`: dots in CanvasText with
the idle one an outline, the current nav link and page tab underlined, the
selected joined-control item in Highlight, the tablist's 3px edges, meters,
banners, disabled buttons in GrayText, the armed danger button's 2px edge,
badges and kbd edged. A new stateful mark adds its own rule.

# Dashboard

## Page head

`PageShell.svelte`: `<main id="main" tabindex="-1" class="frame">`, crumbs
13px `--hk-text-3` with middots (44px tall on touch), `h1.hk-h1-app` with a
`<wbr>` after each `-`, `_` and `.` so a slug breaks at its separators, the
page's one action on the title's baseline from 640px and under it on a
phone, and a 14px `--hk-text-3` lede (a string, or a snippet). With `tabs`
the page tabs sit 1.25rem under the title on a full-width hairline.
`width="form"` caps the content at 42rem.

## Sections

A dashboard page is sections on the page colour: `hk-section` (repose),
2.75rem apart, a `hk-section-head` row with an `h2.hk-h2-app` and, on the
right, a 13px `--hk-text-3` caption or one small button. No border, no fill,
no card. Rows inside are hairlines. A panel (`hk-panel`, 12px corners, a
1px border on the surface) is only for plan cards; panels are never nested.

## The pages

Order at 1440, top to bottom. A phone shows the same order in one column.

- **/projects**: the billing banner when a payment failed or the account is
  suspended; Waiting on you (questions as hairline rows, the section's badge
  carrying the count); the projects table, with search and the four filters
  past 8 projects, Start or Stop as a ghost button always shown on each row,
  and stop's inline confirm under its row; the plan row (Memory running as
  squares, Disk held and Egress this period as meters, then "Billing"), one
  row between two hairlines; Recent activity, five rows with repeats of one
  kind on one project folded and questions and failures never folded;
  Recently destroyed with "Kept until" and "Restore…". An empty account sees
  its billing line, "No projects yet." with the Quickstart link, and one
  command pane with the install, `repose login` and `repose run` commands.
- **A project**: the exception banners (last error, refusal, host
  unreachable, abuse stop, boot fallback), which stay first because each one
  changes what Connect will do; Connect, one command pane of `repose run`,
  `ssh <name>.repose` and `repose code <name>`; Waiting on you; then from
  1024px two columns (`minmax(0, 1fr) 21rem`): Events beside Agents (while
  it runs and reports one) and Machine, Snapshots beside Details; Usage, four
  80px charts across; Destroy, behind its button. The header holds the
  remote in Geist Mono as the lede and a status line (StateDot, size, cores,
  memory, uptime, or the frozen sentence); its action is Stop with
  "Snapshot on stop", Start or Restart. On a phone the action sits one row
  under the status line in every state. One "Restore…" per snapshot opens a
  panel offering the restore over the project (typed name) or, with
  "Restore as new", into a new project.
- **Config**: the project's name as the title and Config as the current tab.
  From 1024px the editor column and an 18rem rail. The editor column holds
  the Menu and Nix tablist with the applied status beside it; Menu is the
  hand-edit banner, a search and `hk-check` rows in two columns; Nix is the
  Nix editor as a near-black code surface, at least 28rem tall. The rail
  holds Base updates, machine.nix and Revisions. Apply sits in the bottom
  bar. Leaving with an unapplied edit asks first (Stay, Leave).
- **Secrets**: the project's name as the title. From 1024px the list and a
  21rem form. Each secret is a hairline row with a ghost "Delete…" that opens
  the inline confirm; the account's secrets follow, struck where the project
  has its own.
- **/billing**: the payment banners; the current plan (its name in serif,
  then price, status and next charge as one row of facts, then Change plan
  and Card and receipts); This month (Running now as squares, Disk held and
  Egress this period as meters, between two hairlines); Change plan, opened
  in place with focus on its heading; Invoices; Cancel plan, last, as danger
  text. An account with no plan sees the plan cards with a button each; an
  exempt account sees its banner and the meters and no plan to buy.
- **/settings**: Account, Notifications, Timezone, Install, machine.nix,
  Secrets, Delete account. From 1024px each section is a 15rem head column and
  a 40rem control column, sections on hairlines 2.75rem apart; the
  machine.nix editor stays in the 40rem column with a 10rem minimum height.

## Long lists

A list that grows is capped and offers the rest: projects 25 ("Show all
N"), snapshots 8, config revisions 8, Recently destroyed 10 ("Show more"),
Recent activity 5, waiting questions 3. Events and invoices page with a
"Page 1 of 5" or "1–6 of 12" position at the foot of the list.

## One primary

A screen has one `hk-btn` at most. Row actions are ghost buttons; a section's
own action is a small secondary. The primary is what the page is for:
Start on a stopped project, Apply on config, Choose a plan on an empty
account.

## Titles

`<title>` is the page, then the project where there is one, then "repose",
joined by middots: "Billing · repose", "Config · job-board · repose".

# Docs and legal pages

The docs frame sits inside the shared frame (I-656, I-660). From 768px a
220px sidebar sticks under the header at 57px, then 48px, then the article.
From 1280px the article is 624px (about 42rem at 15px), then 48px and a
180px "On this page" rail at the frame's right edge; at 1440 the sidebar
starts at x=160, the text at 428 and the rail at 1100. Below 1280 the rail
folds into an "On this page" disclosure above the body, phones included.
Below 768px a 44px sub-bar sticks under the header, ending at 101px, with
"Open the docs menu" (the current page's title) on the left and Pricing on
the right; the menu opens as an opaque panel with search first.

- **Sidebar**: a "Search the docs" field that filters in place, group titles
  12px 600 ink, items 13px `--hk-text-2` with 6px corners, each reserving a
  2px left rule. The current item is the accent on `--hk-accent-wash` with
  that rule in the accent, `aria-current="page"`. Tutorials folds. From 768px
  it draws no scrollbar and fits 1440x900.
- **Article head**: the section as an eyebrow, `h1.hk-h2`, the page's
  description as `hk-lead`, then "View as Markdown" (a small ghost link to
  `/docs/{slug}.md`), then a hairline.
- **Prose** (`lib/components/doc.css`, scoped `.hk .doc`): 15px / 1.6
  `--hk-text-2`; h2 serif 24px with a "#" self-link on hover; h3 Inter 15px
  600 ink; lists with `--hk-text-4` markers; links underlined at 35%. A span
  of inline code with no space in it, 30 characters or fewer, never breaks
  (I-393); `docs.test.ts` holds the limit.
- **Code**: the code pane look, `max-height: 30rem`, never wrapping, the
  70-column rule, no line numbers. Copy is a dark small button 8px from the
  top right; when the text column is under 624px it takes its own strip
  above the code, so it hides no line. A pane or table that overflows on
  either axis takes a tab stop, a region name from its heading, and a 2px
  `--hk-code-dim` bar on the hidden edge.
- **Tables**: 13px, the table header look, the term column on one line from
  640px. Below 40rem a table of two or three columns stacks each row.
- **Notes**: a 2px accent left rule on `--hk-accent-wash`.
- **Rail**: "On this page" 12px 600, items 13px `--hk-text-3` on a 1px track;
  the section being read is ink with a 1px accent rule, `aria-current="true"`,
  and its h3s show one step in.
- **Docs 404**: inside the docs frame, the "404" eyebrow, "Not found", and up
  to six closest pages.
- **Legal pages**: the same tracks, with a 220px "Policies" column (Terms,
  Privacy, Refunds) in the sidebar's look, the effective date under the
  title, and the draft notice as a warn banner.

# The landing

The landing (`routes/+page.svelte`, `routes/landing.css`,
`lib/components/landing/`) uses this system with no exception: the same
tokens, type and components. `LANDING.md` covers what it says and shows.

- **Width**: the full 1120px frame. Two-column sections from 1024px,
  `minmax(0, 0.8fr) minmax(0, 1.2fr)` with a 3.5rem gap, the heading column
  sticky under the header; one column below.
- **Hero**: from 1024px `minmax(0, 1fr) minmax(0, 1.05fr)`, 4rem apart,
  centred. Left: the eyebrow, `h1.hk-h1`, the two-tone lead at 34rem, "Get
  started" (`hk-btn-lg`) beside a large page-tone "Copy install command",
  and the install command in Geist Mono 13px under them, exactly what the
  button copies. Right: the terminal.
- **The terminal** replays the Quickstart's `repose run -p` session, parsed
  at build time from `content/docs/index.md`, so the page cannot drift from
  the docs. A near-black pane with 12px corners, a 1px outer ring, an inset
  border and `--hk-shadow-terminal`; a 38px bar titled
  `~/code/your-project`; Geist Mono 12px / 20px. Commands are
  `--hk-code-text`, `$ ` and output `--hk-code-muted`, ticks `--hk-code-ok`.
  When 40% in view it plays once, one line every 450ms, each rising 4px and
  fading in over 240ms, and ends on "Ready in 16s." with a still caret. It
  has no copy button, no minimum height and no status dot. Without scripts
  and under reduced motion every line shows at once.
- **Each command once**: the install line in the hero only; `repose login`
  and `repose run` as light command lines in "Your laptop's setup"; the
  feature cells give their command as one mono line with a muted `$` and no
  copy button, since a visitor without the CLI cannot run it.
- **Sections**: the hero; "Your laptop's setup, in one command"; "On every
  machine", four cells in a 2x2 grid from 768px, each with an `hk-h3`, a
  paragraph and its command; "Agents on first boot", a hairline list read
  from `content/docs/agents.md`; "Pricing"; "Questions", three `details`
  rows between hairlines; the full footer.
- **Pricing**: `PlanCells` in its public variant, three cards from 900px on
  `hk-panel`-like frames, the founding price struck when it applies, then
  "Start a free week" and the seats line. Plan figures come from
  `lib/plans.ts`, never typed into markup.
- **Never on the landing**: isometric or anatomy figures, drawn pictures of
  the product, invented sample output, a stat strip without a source, motion
  on scroll, or a second dark pane in the first two screens.

# Copy

Guide B 1.1 and 1.2 and `CLAUDE.md` "Trust the reader": every line says what
is true, once, and stops. No sentence without a verb, no em dash, no
reassurance, no next step after a success, no caption that restates what the
screen shows. One term per thing (a project, its machine, a snapshot). Dates
use the CLI's format (`2026-10-20 23:06`, "just now"), which the tests pin.
Prices, sizes and limits come from `lib/plans.ts` and the API, never typed.
`copy.test.ts` and `docs.test.ts` scan routes, components and the docs for
the patterns `lib/unasked.ts` lists.

# Where it goes

| What | File |
|---|---|
| The kit's tokens and primitives (never edited) | `apps/web/src/lib/hk/tokens.css` |
| repose's additions, each with its reason | `apps/web/src/lib/hk/repose.css` |
| Imports, `@theme`, `frame`, `@font-face`, first-paint pair, toasts, forced colours | `apps/web/src/routes/layout.css` |
| First paint, theme script, preloads | `apps/web/src/app.html` (hash in `svelte.config.js`) |
| Theme state | `lib/theme.svelte.ts`, `lib/components/ThemeSwitch.svelte` |
| Shared components | `lib/hk/` (CopyButton, CodeBlock, CommandList, Segmented), `lib/components/` (PageShell, LoadState, ErrorView, Meter, StateDot, Snowflake, CodeCopy, ConfirmType, SiteHeader, SiteFooter, DashNav, AccountMenu, Logo, PlanCells, MemoryBlocks) |
| Docs prose and code | `lib/components/doc.css` (unlayered) |
| The Nix editor | `lib/components/nix-editor.css` (unlayered: CodeMirror's theme is) |
| The landing | `routes/+page.svelte`, `routes/landing.css`, `lib/components/landing/` |
| The gallery | `routes/design/+page.svelte` (dev only) |

A new shared class goes in `repose.css`, `hk-` prefixed and named in
Components above, with a comment that says why. A page styles itself in its
component's `<style>`.
