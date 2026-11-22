# UI council round 2

Council score: 7.03 (target 9.5). Seats judge screenshots only.

| Seat | Score | Findings | High |
|---|---|---|---|
| art | 6.8 | 17 | 1 |
| customer | 6.9 | 17 | 1 |
| copy | 7.4 | 13 | 2 |

## art (6.8)

The palette is disciplined, light and dark both hold up, and the page avoids every loud mistake the owner listed. But it does not yet read as editorial. Every figure is a small hairline card, often holding a second boxed card, set in a large empty stage. The wheel is big and quiet, yet it looks the same at every step. Pricing and Start are tables with no figure, and the same "Mac mini or Linux server" and snapshot facts come back three times.

Flyer: Not yet: the owner would be comfortable with it, not proud of it, because the figures are timid widgets in oversized stages and the page repeats itself instead of landing one image per idea.

Directives:
- 1 Three-second read: partly met. The hero h1 and figure show a sandbox, a laptop-to-VM copy and agent icons. The undo idea is a tiny grey timeline, and where it runs is carried only by the lede copy (hero, 1440 and 390).
- 2 Show, don't tell: partly met. The hero, wheel and safety sections carry figures, but the hero lede runs three lines, the wheel-tools step adds a lede, and pricing and start have no figure (pricing, start-cloud).
- 3 Each fact once: not met. 'Your Mac mini or Linux server' appears in the hero lede, the wheel-where card, the pricing-hardware row and the start toggle. The snapshot undo is in the hero figure and the safety section. Agent icons are in the hero and wheel-agents.
- 4 Brand colours only: met. Zinc and slate only, red only for 'wrecked', green only as 6px dots, no gradients, and no dark slabs in light mode (all light shots). The logo's royal-blue dot is a minor deviation.
- 5 Subtle over loud: partly met. The palette and type are calm, but the figures are boxes in boxes with inner dividers (hero, safety-figure, wheel-where).
- 6 Mac mini named, never first-and-only, never 'own hardware': met (hero lede: 'our cloud or your Mac mini or Linux server'; pricing-hardware).
- 7 Desktop wheel large, quiet, part of the background, turns with scroll: large, quiet and part of the background are met (wheel-where 1440). The turn is not verifiable from stills, and the ticks sit at identical positions in all three steps, so no rotation is visible.
- 8 No notifications pitch, no 'No token markup', no agent names in the hero, no 'Full permissions' pill: met (hero shows icons only; safety shows 'bypass permissions on' as an agent status line, not a pill).

Screens: hero 7, wheel-where 6.7, wheel-agents 6.9, wheel-tools 6.2, safety 7, safety-figure 6.7, pricing 7, pricing-hardware 6.2, start-cloud 7.1, start-machine 6.9

Systemic:
- Boxes in boxes everywhere: each figure is a 1px-bordered card split by internal hairlines into sub-panels (header row, body, footer row), and these sit inside a dotted or grey stage panel. That gives three nested frames per figure in the hero, safety and wheel-where.
- The figures are small next to their stages. At 1440 the hero figure fills about 560x210 of a 590x680 stage, and the safety cards fill about 900x275 of a 1150x380 stage. Above and below each figure the stage is empty, so it reads as a widget, not an illustration.
- Mono is used for prose labels that are not code: 'Claude subscription', 'ChatGPT login', 'billing-api'/'web-app' rows, 'snapshots' times with words. Keep Geist Mono for commands, paths and env vars only.
- Each fact appears several times. 'Your Mac mini or Linux server' appears in the hero lede, as the wheel-where card header, as the pricing row title and as the 'Your machine' toggle. The undo by snapshot appears in the hero figure (snapshot timeline with a restore arc) and again as the whole safety section. Agent icons appear in the hero and again in wheel-agents.
- The logo's second dot is a saturated royal blue (about #1d4ed8). The page accent is slate (#4b6496 / #93a7d0), so the brand shows two blues side by side in the header, the footer and the favicon area.
- Green status dots repeat 9 to 10 times on the page (hero, four in wheel-where, wheel-tools, safety). Each one is legal, but together they become a pattern of green that reads as decoration.

High findings:
- wheel-where / wheel-agents / wheel-tools @1440: The wheel does not visibly turn. The tick marks, the dashed inner ring and the arc sit at identical pixel positions in all three steps (for example, a major tick at about x=120,y=135-160 and another at x=340,y=170-190 in every shot). The only change is an extra slate dot near the top-left, and the dots bunch within 80px of each other. Directive 7 asks for a wheel that turns with scroll. These stills show a static dial with a growing row of dots. FIX: Rotate the whole wheel group (ticks, dashed ring, arc) by a fixed step per section, about 18-24 degrees, so the active tick moves to a fixed 'noon' index point aligned with the step title. Keep one active dot at that index point instead of adding dots. Space the three step markers at least 120px apart along the arc.

## customer (6.9)

The page is calm, on-brand and honest, and dark mode is as polished as light. But I still learned the three facts from the hero paragraph, not from the picture. The hero figure shows "copies your repo but not ~/.ssh" and a 40px undo arc. It never shows cloud or Mac mini or Linux server. The facts the figure should carry are told in copy and then told again lower down. The wheel section reads as three list cards with a tiny decorative arc, and in the stills the wheel does not visibly turn.

Flyer: Not yet: a sceptical HN reader gets "another agent sandbox" from the headline and has to read a 30-word paragraph to find the two things that are different (a sandbox per repo that undoes a wreck, and $10 to use your own box).

Directives:
- 1 (three-second value): partly met. The hero paragraph states all three facts. The hero figure shows the per-repo VM and a tiny undo arc but not 'like your laptop' or cloud versus Mac mini versus Linux server (hero, 1440 and 390).
- 2 (show, don't tell): not met in the hero. Its copy is three lines and about 30 words. The wheel steps meet it (a title and at most one line), and so do the safety and start sections.
- 3 (each fact once): not met. 'Mac mini or Linux server' appears in the hero copy, the wheel-where card, the pricing row and the start tab. 'Full permissions with undo' appears in the hero copy, the hero figure and section 02.
- 4 (brand colours): met. Only slate-blue accent, red only for 'wrecked', no gradients, and light sections stay light. The safety stage is the allowed #f4f4f5. Green is limited to dots, but there are eleven of them (wheel-where has eight).
- 5 (subtle, fewer boxes and labels): partly met. The page is calm, but the wheel stacks an eyebrow, a three-item sub-nav and boxed list cards, and every figure is a bordered card.
- 6 (Mac mini named, never first-and-only, never 'own hardware'): met. It is always 'Mac mini or Linux server' or 'a machine you already have' (hero, wheel-where, pricing-hardware).
- 7 (wheel large, quiet, background, turns with scroll): large, quiet and part of the background is met (wheel-where, 1440). Turning is not met in the stills: the ticks are identical across the three wheel screenshots, and only the progress dots advance.
- 8 (no notifications, no 'No token markup', no agent names in hero, no 'Full permissions' pill): met. The hero uses icons only, and 'bypass permissions on' sits in the safety figure as product UI, not as a pill.

Screens: hero 6.9, wheel-where 6.5, wheel-agents 6.6, wheel-tools 6, safety 7.2, safety-figure 7, pricing 7.3, pricing-hardware 7.4, start-cloud 7, start-machine 7

Systemic:
- Facts repeat. 'Mac mini or Linux server' appears in the hero paragraph, as the wheel-where card heading, as the pricing row title and as the 'Your machine' tab. 'Full permissions plus snapshot undo' appears in the hero paragraph, in the hero figure and in section 02. The hero copy restates what the figure and the later sections already show.
- Figures are mostly list cards: a rounded box with mono rows and hairline dividers (wheel-where, wheel-agents, safety snapshots, start table). The page reads as tables, not pictures, so the 3-second read depends on the copy.
- Mono is used for labels that are not code: 'Claude subscription', 'ChatGPT login', 'billing-api' rows, 'restore 21:25' is fine but 'nightly/restored/wrecked' sit beside mono times. Keep mono for commands, paths, env vars and times only.
- Green status dots multiply. One in the hero, eight in wheel-where, one in wheel-tools, one in the safety figure. Each is allowed alone, but together they add a green speckle the owner said to keep minimal.
- At 390 the header shows only the two-dot mark. The word 'repose' appears nowhere above the fold on a phone.

High findings:
- hero @1440: The figure does not carry directive 1. It shows the repo copied with .ssh/ and Documents/ struck out, the five agent icons, and a 40px-wide undo arc under a lowercase 'snapshots' label. Nothing shows 'set up like your laptop' (it shows what is NOT copied), and nothing shows cloud versus Mac mini versus Linux server. I got all three facts from the paragraph. FIX: Make the figure carry the three facts and cut the paragraph to one line. (1) In the laptop card, list what comes over (for example 'your-project/', '.gitconfig', 'node 24', 'gh login') with one struck '.ssh/' at most. (2) Put a three-position chip row in the target card header: 'repose cloud | Mac mini | Linux server', with one chip active in the accent. (3) Make the snapshot strip span the full card width at 64px tall: a red 'wrecked' tick on the right and an accent arc back to the previous dot. Then set the subcopy to one line, e.g. 'A Linux VM per repo that agents can wreck and you can roll back.'

## copy (7.4)

The copy is mostly clean. I found no em dashes, no buzzwords, no "own hardware" and no claim that contradicts A6. The biggest failure is repetition: "your Mac mini or Linux server" is said three times, the undo story is drawn twice, and pricing gives three overlapping scarcity lines. Smaller problems: some titles narrate what the figure already shows ("Three commands"), and some lines lack a referent ("don't count" toward what, "Let it" who).

Flyer: Nearly: a developer would not wince at any one sentence, but the same where-it-runs line comes back three times and the pricing header stacks three scarcity notes, so the page still reads like it is repeating itself.

Directives:
- A5.1 three-second value: met on hero 1440 and 390. The subline names the sandbox per repo set up like your laptop, full permissions plus snapshot undo, and cloud or Mac mini or Linux server.
- A5.2 show, don't tell: mostly met. Every section has a figure and at most one line. The exceptions are the 'Three/Four commands' title narrating the rows and the wheel-tools line restating its figure.
- A5.3 each fact once: not met. 'Mac mini or Linux server' appears in the hero, wheel-where and pricing-hardware. The snapshot undo appears in the hero figure and the safety figure. The pricing header has three scarcity lines.
- A5.4 brand colours: met in copy terms. Green appears only as status dots, red only as 'wrecked'. 390 pricing renders '18 cloud seats left' in accent blue, a questionable use of the one accent.
- A5.5 subtle and elegant: partly met. The pricing header and the wheel cards are calm. The extra labels ('snapshots', '01 HOW IT WORKS', '18 cloud seats left') add noise.
- A5.6 Mac mini named, never first-and-only, never 'own hardware': met (hero, wheel-where, pricing-hardware). 'own' survives only in 'Use your own browser and editor' (wheel-tools), which I flag for cutting.
- A5.7 desktop wheel large and quiet: visible on wheel-where, wheel-agents and wheel-tools 1440. I cannot verify scroll rotation from stills, though the advancing dots suggest it.
- A5.8 no notifications pitch, no 'No token markup', no agent names in hero, no 'Full permissions' pill: met on all screens. The hero shows icons only.

Screens: hero 7.8, wheel-where 6.8, wheel-agents 8, wheel-tools 7.4, safety 7.8, safety-figure 8, pricing 6.9, pricing-hardware 7.2, start-cloud 7.2, start-machine 7.2, full 7.3

Systemic:
- The fact 'cloud or a Mac mini / Linux server you have' appears three times: in the hero subline ('on our cloud or your Mac mini or Linux server'), as the wheel-where title plus its card header 'Your Mac mini or Linux server', and as the pricing row title 'Your Mac mini or Linux server'. This breaks A5.3. The hero keeps the full wording. Everywhere else, use the short name 'Your machine', which the Start tab already uses.
- The undo idea is drawn twice: once as the hero figure's 'snapshots' timeline with its loop-back arrow, and again as the whole safety figure. The hero copy also says 'a snapshot undoes what they break'. Keep the hero sentence and the safety figure. Remove the snapshots strip from the hero card.
- Several titles count or narrate what sits right under them ('Three commands' / 'Four commands', '01 HOW IT WORKS'). A title should say what the reader gets.
- Names drift. The rail says 'Logins' while the figure shows an API key. The wheel card says 'repose cloud', the hero says 'our cloud', and pricing says 'cloud seats'. Pick one name for each thing: 'repose cloud' and 'Your machine'.

High findings:
- wheel-where @1440: The title 'Our cloud, or a machine you already have' and the card header 'Your Mac mini or Linux server' repeat the hero subline word for word in meaning. This is the second of three statements of the same fact (A5.3), and the owner named this exact repetition in A4. FIX: Change the title to 'Same sandbox, either machine'. Change the card's second header from 'Your Mac mini or Linux server' to 'Your machine'. Keep 'repose cloud' as the first header.
- pricing-hardware @1440: The row title 'Your Mac mini or Linux server' is the third time this phrase appears on the page (A5.3). FIX: Change the row title to 'Your machine'. Change the line to 'Per machine. Run as many sandboxes as its memory and disk hold.' Apply the same change at 390.

## Out of scope (for a later lane)

- art: The footer theme switcher and the llms.txt link are page chrome shared with /docs. If they change, the change belongs to the site shell, not the landing figures.
- art: Whether '18 cloud seats left' is live data or a constant needs an API check before any redesign leans on it.
- customer: Whether '18 cloud seats left' is a live count from the API or a fixed string. If it is hardcoded, it breaks the 'Real, or not at all' rule; checking this needs the data source.
- customer: Whether the 7-day trial applies to the $10 Mac mini or Linux server plan, and what the default sandbox memory size is for showing sandboxes per tier. Both must come from billing and the public docs before the landing can state them.
- copy: Confirm against billing that the Solo founding offer's '30 subscribers' and the '18 cloud seats left' counter measure the same pool. The copy fix depends on it, and the counter comes from live data.
- copy: Confirm that 'Stopped sandboxes don't count against your memory' matches the docs' wording for frozen versus stopped machines in apps/web/src/content/docs/.

