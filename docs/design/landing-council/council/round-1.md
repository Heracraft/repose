# UI council round 1

Council score: 6.43 (target 9.5). Seats judge screenshots only.

| Seat | Score | Findings | High |
|---|---|---|---|
| art | 6.6 | 18 | 2 |
| customer | 6.4 | 15 | 2 |
| copy | 6.3 | 19 | 4 |

## art (6.6)

The system holds: serif titles, slate accent, ruled sheet, quiet dark mode. But the figures carry too many labels, and several of them nest boxes three deep. Wheel-tools reads like a generated dashboard mock, not an editorial figure. The page repeats itself: the install command, $10, 7 days free, `repose run` and `restore 21:25` each appear two or three times. The hero still shows a rejected spec label (Ubuntu 24.04).

Flyer: Not yet. The hero and pricing are close to proud, but wheel-tools and the safety snapshot card look generated and need decoding, and that undercuts the elegance the owner asked for.

Directives:
- 1 Three-second value: partly met. The hero headline and figure land sandbox, full permissions and undo. Cloud or your Mac mini or Linux server appears only in the small meta line, not in the figure (hero 1440/390).
- 2 Show, don't tell: partly met. Copy is a title plus one line everywhere, but wheel-tools and safety-figure need label reading. Pricing has no figure, which is acceptable.
- 3 Each fact once: not met. The curl install appears in the hero and Start, $10 and Mac mini or server in the hero, wheel-where, pricing and Start, 7 days free in the hero and pricing, 'Start a free week' x3, and the restore at 21:25 in the hero and safety.
- 4 Brand colours: met. Slate is the only accent, green appears only as small status dots (hero, wheel-where rows, safety), red only for the wreck (full-1440), and there are no gradients. The light grey #f4f4f5 bands in safety and pricing-hardware are borderline but not dark.
- 5 Subtle over loud: not met. Boxes in boxes in wheel-where, wheel-tools and the safety snapshots, and heavy mono label clutter.
- 6 Mac mini named, never first-and-only, never 'own hardware': met. It is always 'Mac mini or Linux server'. 'x86 box you own' in pricing-hardware edges close to the rejected wording.
- 7 Desktop wheel large, quiet, background, turns with scroll: large and quiet is met (wheel-where/agents/tools 1440). Rotation can't be verified from stills; tick positions look identical across the three step shots. On mobile a stray arc fragment shows behind the heading.
- 8 No notifications pitch, no 'No token markup', no agent names in the hero, no 'Full permissions' pill: met (hero shows icons only).

Screens: hero 6, wheel-where 6.6, wheel-agents 7, wheel-tools 5.5, safety 7, safety-figure 6.3, pricing 7.3, pricing-hardware 6.8, start-cloud 7.2, start-machine 7.4

Systemic:
- Boxes in boxes: figure cards have a header bar, then inner bordered rows or panels (wheel-where rows, wheel-tools sandbox and laptop panels inside an outer card, the safety snapshot card inside a card). One hairline container per figure, rows split by a single rule, nothing deeper.
- Label clutter in figures: mono micro-labels (Ubuntu 24.04, listening :5173, 5173 forwarded, nightly, last night, job listings, v24.20.0, 3,532 rows, apps/worker/src/ingest) compete with the one point each figure makes. Keep at most one secondary label per card.
- Facts repeat across sections: the curl install (hero and Start), $10/mo and 'Mac mini or server' (hero line, wheel-where card, pricing row, Start toggle), 7 days free (hero and pricing), 'Start a free week' (hero, pricing, Start), `repose run` and the restore at 21:25 (hero figure and safety figure).
- The hero figure already tells the whole safety story (snapshot timeline with an undo arc), so section 02 repeats it. Give the hero sandbox plus agents only, and leave undo to section 02, or the reverse.
- Header pills and chips ('repose run', 'snapshot', 'restore 21:25', '5173 forwarded', 'localhost:5173') use a slate outline pill style. It is on-brand, but there are too many: one accent pill per figure at most.
- Mobile drops the wheel but leaves a stray arc fragment at the top right of section 01, behind the heading.
- Dark mode is on par with light everywhere: surfaces #18181b, hairlines and accent shift work. No dark-specific failures found.

High findings:
- hero @1440: The sandbox card header shows 'Ubuntu 24.04' in mono at the top right. This is the spec the owner named as a cut ('Every machine runs Ubuntu 24.04...'). It is a rejected A4 item on screen and caps the hero at 6. It shows at 390 too. FIX: Delete the 'Ubuntu 24.04' label at both widths. Leave the header as the green dot and 'your-project' only.
- wheel-tools @1440: The figure nests three deep (outer card > 'your sandbox' panel > 'your laptop' panel > a Job Alerts app mock with its own nav). It carries a VITE log line, 'listening :5173', a '5173 forwarded' pill and a 'localhost:5173' outlined chip, which says the port four times. A second bordered strip below lists 'ssh your-project.repose' and four editor names. A visitor has to decode a terminal line, which the owner rejected, and it looks generated. FIX: Remove the outer card and the ssh strip. Draw two plain frames side by side, joined by one hairline arrow: left, a borderless mono line 'vite  →  localhost:5173' under the label 'sandbox'; right, a browser frame (6px radius, 1px #e4e4e7, URL bar reading 'localhost:5173') with a 3-line greyed page skeleton, no fake job data. Drop 'listening', 'forwarded', the VITE version and 'ready in 1.4s'. Put the editor point into the subtitle: 'Its dev server opens on your localhost; VS Code, Cursor or Zed connect over SSH.'

## customer (6.4)

The hero headline and the laptop-to-sandbox-to-snapshot figure get me most of the value in three seconds, and pricing and start are clean. The middle of the page goes back to what the owner rejected. "Your tools" is a Vite log and a fake app nested three boxes deep. The safety figure is a wall of mono labels that only makes sense in its wrecked frame. "Mac mini or server", the install command, $39, $10 and "7 days free" each appear two to six times.

Flyer: Not yet: the hero and pricing would hold up on Hacker News, but a sceptical reader scrolls into a terminal log and a safety diagram they have to decode, and sees "Mac mini or server" so often that it reads like padding.

Directives:
- 1 (three-second value): partly met. The hero headline, subline and laptop-to-VM-with-snapshots figure carry the sandbox per repo and the undo (hero-1440). Cloud versus a Mac mini or server shows only in a small price line, and the figure does not show it.
- 2 (show, don't tell): not met. wheel-tools-1440 relies on a terminal log, and safety-figure-1440 relies on mono labels and pills. The mobile safety captions (safety-figure-390) explain what the figure should show.
- 3 (each fact once): not met. 'Mac mini or server' appears about 7 times (hero, wheel-where subline and card, pricing-hardware title and description, the start toggle, step 4.3). The curl command, $39, $10 and '7 days free' are each repeated between the hero and pricing or start.
- 4 (brand colours, slate accent, green dot only, no gradients, light stays light): met. Green appears only as small status dots (hero, wheel-where), red only in the wreck state (full-1440). No gradients. The stage tint stays light in light mode.
- 5 (subtle and elegant, fewer boxes): not met in the middle of the page. Nested cards in wheel-where, wheel-tools and the safety-figure snapshots card. Pricing and start are clean.
- 6 (Mac mini named, never first-and-only, never 'own hardware'): mostly met. It is always paired with 'Linux server' and comes after 'repose cloud' (wheel-where, pricing-hardware). The phrase 'own hardware' does not appear, but 'an x86 box you own' in pricing-hardware is close to it.
- 7 (desktop wheel large, quiet, background, turns with scroll): large and quiet are met (wheel-where-1440, wheel-agents-1440), but the arc collides with card corners. Rotation with scroll cannot be verified from stills; the tick positions are identical across the three wheel stops.
- 8 (no notifications pitch, no 'No token markup', no agent names in hero, no 'Full permissions' pill): met (hero-1440 shows icons only; no such items appear anywhere).

Screens: hero 7, wheel-where 6, wheel-agents 6.5, wheel-tools 5, safety 6, safety-figure 5.5, pricing 7.5, pricing-hardware 6.5, start-cloud 7.5, start-machine 7.5

Systemic:
- Repetition across the page breaks directive 3. 'Mac mini or (Linux) server' appears in the hero price line, the 01 title subline, the wheel card, the pricing row title, the pricing row description, the start toggle and step 4.3. The install command appears in the hero and in step 4.1. '7 days free', '$39' and '$10' appear in the hero line and again in pricing. The laptop-to-sandbox 'repose run' arrow and the '21:25' restore appear in both the hero figure and the safety figure.
- Boxes in boxes: the figures stack outer panel, header strip, inner card, outlined chip and pill (wheel-where cards around row cards; tools panel around two windows around a pill and a chip; snapshots card around a 03:00 card). This works against directive 5.
- Mono labels carry meaning the figures should show: 'bypass permissions on', 'listening :5173', 'VITE v8.0.1 ready in 1.4s', '3,532 rows', 'v24.20.0', 'nightly', 'last night'. Each one is a small thing to decode.
- Section numbering is inconsistent. Desktop uses '01 HOW IT WORKS' with no sub-numbers. Mobile adds 1.1/1.2/1.3 and 2.1/2.2/2.3 captions. Start uses 4.1-4.4 on both widths. Pick one: keep the 01-04 section labels and drop the sub-numbers.
- Mobile captions do not match desktop. The safety captions ('Laptop stays walled off', 'Agent runs freely inside VM', 'Snapshots undo the whole disk') exist only at 390. The wheel sublines ('A Mac mini, or any Linux server that runs VMs', 'Mix and match subscriptions...') are dropped at 390. The two widths tell different stories.
- Dark theme holds up everywhere: no slabs, the accent is consistent and the hairlines stay visible. Light sections stay light. The pricing hardware row and the safety stage use the #f4f4f5 stage tint, which reads as light.

High findings:
- wheel-tools @1440: The figure is a terminal log ('VITE v8.0.1 ready in 1.4s → http://localhost:5173/', 'listening :5173') above a made-up 'Job Alerts' app with job rows, inside a panel holding two windows, a '5173 forwarded' pill and an outlined 'localhost:5173' chip. That is four nested boxes and a log to decode, which A4 rejects. 'Job Alerts / Staff Systems Engineer' also pulls attention to an app that has nothing to do with repose. FIX: Replace it with two quiet elements joined by a single 1px accent line. Left: a minimal browser frame whose address bar reads 'localhost:5173' in accent, with three grey placeholder bars and no fake content. Right: a row of four editor icons (VS Code, Cursor, Zed, Neovim) at 24px, monochrome zinc-500. Drop the sandbox window, the Vite line, the pill and the chip. Keep 'ssh your-project.repose' only if it is the single line of mono on the screen.
- safety-figure @1440: In the captured frame nothing is broken: no red, no wreck. The figure is a dense diagram of laptop, sandbox and snapshots with five mono pills and labels ('repose run', 'snapshot', 'restore 21:25', 'bypass permissions on', '03:00 nightly', 'last night'), plus a globe on a line that explains nothing. The point 'the agent wrecks it, you roll back' lands only in the animation's wreck frame (visible in the full-page capture). A visitor who glances at the wrong moment sees an architecture chart. The snapshots card shows a '03:00' card with about 80px of empty space while the arrow says 'restore 21:25'. FIX: Make the resting state the story. Show the sandbox in two columns: 21:31 with files struck through and '0 rows' in the wreck red, and 21:25 restored in normal ink, joined by one curved accent arrow labelled 'restore'. Remove the laptop card (the hero already shows the laptop), the 'repose run' and 'snapshot' pills and the globe line. Keep 'bypass permissions on' as the agent icon plus that single line. Run the motion from restored to wrecked to restored, but make the static and reduced-motion frame the split view.

## copy (6.3)

The voice is mostly clean: no em dashes, no buzzwords, no x-not-y, no punchy closers. The page breaks the owner's "mention it once" rule all over. "7 days free", the $39 and $10 prices, the install command, "Start a free week" and "Mac mini or server" each come back two to seven times. Small leftovers of telling remain: mobile captions that narrate the safety figure, "Scroll to turn", "Ubuntu 24.04", the port number shown four times, and an "x86 box you own" line that echoes the banned "own hardware".

Flyer: Almost: a developer would get it from the hero, but by pricing they have read "Mac mini or server" five times and "7 days free" three times, which reads as padding, so the owner would not be proud yet.

Directives:
- A5.1 Three-second read: met. The hero h1, subline and figure (laptop, repose run, sandbox with five agent icons, snapshot timeline) carry 'sandbox per repo like your laptop', 'full permissions plus undo' and the cloud or Mac mini option. The cloud-or-machine part leans on the meta row I want cut, so the wheel's first panel must carry it (it does at 1440; at 390 it sits just below the hero).
- A5.2 Show, don't tell: mostly met. Each section has a figure. Not met on the safety section at 390 (narrating captions 2.1-2.3) and on the wheel-agents and wheel-tools subtitles, which restate the figure.
- A5.3 Each fact once: NOT met. '7 days free' appears in the hero meta, the pricing subtitle and four buttons. $39 and $10 appear in the hero and pricing. The curl line appears in the hero and Start 4.1. 'Mac mini or server' appears about 7 times.
- A5.4 Brand colours: met in every screenshot I read. Slate accent only, green only as small status dots, no gradients, no dark slabs in light mode (the pricing hardware row is a light stage grey).
- A5.5 Subtle over loud: partly met. Labels pile up (kicker plus n.n numbers plus title plus subtitle), and the hero and wheel-tools figures carry extra mono labels.
- A5.6 Mac mini named, never first-and-only, never 'own hardware': partly met. It always sits beside Linux server and after the cloud, but the pricing row says 'an x86 box you own', the banned wording in other words.
- A5.7 Desktop wheel large, quiet, turning with scroll: large and quiet (wheel-where, wheel-agents and wheel-tools at 1440, light and dark). The turn with scroll can't be checked from stills.
- A5.8 No notifications pitch, no 'No token markup', no agent names in the hero, no 'Full permissions' pill: met (hero shows icons only; 'full permissions' is in running text, not a pill).

Screens: hero 6, wheel-where 6.6, wheel-agents 6.5, wheel-tools 6, safety 6.8, safety-figure 7, pricing 6, pricing-hardware 5.8, start-cloud 6.8, start-machine 6.8

Systemic:
- The same facts come back across the page (A5.3, A4 'Repetition'). '7 days free': hero meta row, pricing subtitle and four 'Start a free week' buttons. '$39' and '$10': hero meta row and pricing. The curl install line: hero and step 4.1. 'Mac mini or server': hero meta, wheel subtitle, wheel card, pricing row title, pricing row body, start toggle and step 4.3. Each fact needs one home: prices in pricing, install in Start, the trial in the button.
- The machine has three names: 'Linux server that runs VMs' (wheel), 'server' (hero, pricing, start) and 'x86 box' (pricing). Use 'Mac mini or Linux server' everywhere, and say it at most twice: once in the wheel figure and once on the pricing row.
- Casing in the figures is inconsistent. The hero says 'Your laptop'. The safety figure says 'your laptop' and 'your sandbox'. The wheel says 'repose cloud' and 'your Mac mini'. Pick sentence case for all figure window titles ('Your laptop', 'Your sandbox', 'Snapshots').
- Labels pile up: section kickers ('01 HOW IT WORKS'), then sub-numbers (1.1, 2.1, 4.1), then a title and a subtitle. The decimal numbers add a label to every block and carry no meaning for a visitor. Drop the n.n numbers and keep the 01-04 kickers.
- 'Start a free week' appears four times, twice within one screen at 1440 (the pricing header and the Start header, about 550px apart). Keep it in the hero, the sticky nav or 'Sign in', and the Start section. Cut the one in the pricing header.

High findings:
- hero @1440: The meta row '7 days free · Cloud from $39/mo · Your Mac mini or server $10/mo' repeats the pricing section word for word and the button's 'free week'. This is the A4 repetition the owner rejected. At 390 it also wraps to a stray trailing '·' on line one. FIX: Cut the whole meta row at both widths. Pricing carries the prices and the button carries the free week. If one link to pricing must stay, use a single plain link 'Pricing' after 'Read docs'.
- hero @390: The curl install box repeats step 4.1 of the Start section. At 390 it wraps in the middle of the URL ('https://repose.run/' / 'install.sh | sh'), which reads as broken. FIX: Cut the curl box from the hero. Step 4.1 in Start is its one home. The hero keeps 'Start a free week' and 'Read docs →'.
- pricing @1440: The subtitle '7 days free. Stopped sandboxes don't count.' says '7 days free' again. It is already in the hero and in every 'Start a free week' button. FIX: Change the subtitle to 'Stopped sandboxes don't count.'
- pricing-hardware @1440: The body 'A Mac mini or an x86 box you own, in your account. One host; its memory and disk are the limits.' repeats 'Mac mini' from the row title. 'x86 box you own' is the banned 'own hardware' wording in other words, and 'x86' contradicts the wheel's 'any Linux server'. 'in your account' is vague, and the semicolon sentence reads as legal text. FIX: Title: 'Your Mac mini or Linux server'. Body: 'Per machine. Run as many sandboxes as its memory and disk hold.' Cut 'x86', 'box you own' and 'in your account'.

## Out of scope (for a later lane)

- art: Footer links llms.txt, Terms, Privacy and Refunds point at pages outside / and were not reviewed.
- art: Whether '18 cloud seats left' and 'first 30 subscribers' are live data needs an API check; if the number is static, the page is outside the 'Real, or not at all' rule.
- customer: Whether repose copies ~/.ssh or tool logins into the guest decides what the hero's lock icons should show. That needs product confirmation against docs/features/secrets.md before the figure can state it.
- customer: '18 cloud seats left' must come from live data. Whether that number is real or hard-coded cannot be judged from screenshots.
- copy: Can't confirm from the page whether 'ssh your-project.repose', 'repose host add' and 'First 12 months, first 30 subscribers' match apps/web/src/content/docs; the docs seat or a docs check should verify them.
- copy: Whether the $10 Mac mini or Linux server plan includes the 7-day trial is not stated on the page. That is a billing and plan question, outside the landing scope.

