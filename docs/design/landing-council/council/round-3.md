# UI council round 3

Council score: 7.03 (target 9.5). Seats judge screenshots only.

| Seat | Score | Findings | High |
|---|---|---|---|
| art | 7.2 | 15 | 1 |
| customer | 6.9 | 15 | 2 |
| copy | 7 | 17 | 2 |

## art (7.2)

The page now has a real editorial frame. The serif scale is calm, the 1200px ruled sheet holds, dark mode is as finished as light, and nothing off-brand is loud. It still falls short of Linear or Stripe level in three places. First, the wheel stages put small figures in the top-right corner of a huge quiet canvas, so the bottom half of every wheel viewport is empty. Second, the undo timeline and the 'running at once' idea each appear more than once. Third, at 390 the install command is cut off mid-word.

Flyer: Nearly: the owner would put up the hero and the safety section with pride. They would wince at the empty lower half of the wheel stages, the generic Todos/billing-api mock-ups, and an install command cut off at 'install.s' on a phone.

Directives:
- 1. Three-second understanding: met, mostly through the hero. The headline 'A sandbox per repo, with undo', the laptop-to-sandbox figure and the agent icons carry it. 'Mac mini or Linux server' is carried only by the copy line, not by a figure (hero 1440/390).
- 2. Show, don't tell: mostly met. Every section has a figure and a title with at most one line. The pricing cells drift into captions (pricing 1440).
- 3. Each fact once: not met. The snapshot and undo timeline is in both the hero and section 02, and 'running at once' is stated about four times in pricing (hero 1440, safety-figure 1440, pricing 1440).
- 4. Brand colours: met with a caveat. Slate is the only accent, red appears only in the safety timeline, green only as dots, there are no gradients, and light sections stay light. The logo's royal-blue dot and the halo band under the wheel arc push the edge (hero, wheel-where 1440).
- 5. Subtle and elegant: partly met. The type and spacing are calm, but header-bar cards and box-in-box figures recur (hero, wheel-tools, safety-figure, start 390).
- 6. Mac mini named, never first-only, never 'own hardware': met. The hero line reads 'on our cloud or your Mac mini or Linux server', and 'own hardware' does not appear anywhere (hero 1440/390).
- 7. Desktop wheel large, quiet, part of the background, turning with scroll: large and quiet, met. The active arc changes position across wheel-where, wheel-agents and wheel-tools, which suggests rotation, but scroll-driven motion cannot be verified from stills. The figures sit outside the wheel rather than with it (wheel-* 1440).
- 8. No notifications pitch, no 'No token markup', no agent names in the hero, no 'Full permissions' pill: met. The hero shows icons only, and nothing banned is visible on any screen.

Screens: hero 1440 7.8, hero 390 7.4, wheel-where 1440 7, wheel-where 390 7.2, wheel-agents 1440 7.8, wheel-agents 390 7.6, wheel-tools 1440 6.8, wheel-tools 390 6.9, safety 1440 7.4, safety 390 7.3, safety-figure 1440 7, safety-figure 390 7, pricing 1440 7.2, pricing 390 7, pricing-hardware 1440 6.8, pricing-hardware 390 6, start-cloud 1440 7.8, start-cloud 390 6, start-machine 1440 7.8, start-machine 390 6, full (rhythm) 7

Systemic:
- The card with a header bar (a title strip, a 1px rule, then a body) is used everywhere: both hero cards, the localhost and SSH cards in wheel-tools, and the safety 'Your sandbox' card. It reads as a template. Each figure stacks two boxes, which goes against 'fewer borders, fewer boxes'.
- The snapshot timeline with an undo arc appears twice. The hero card shows a small one ('snapshots', 'undo') and section 02 shows the full one ('Snapshots', 'restore 21:25'). This breaks directive 3, and the label casing differs between the two.
- 'Running at once' is said four times in pricing: the title, three '8/16/32 GB running at once' lines, and the segment bars plus the 'Two/Four/Eight 4 GB sandboxes' captions say it again.
- The sandbox gets a different name in each section: 'your-project' with a green dot in the hero and wheel-tools, and 'Your sandbox' with a cloud icon and a green dot in safety. Pick one name and one header style.
- The green status dot appears three times (hero, wheel-tools, safety). Each use is within the rule, but at three per page it starts to read as decoration.
- The logo's second dot is a saturated royal blue (about #1d4ed8 light, #60a5fa dark). It is the only non-slate blue on the page, so it competes with the #4b6496 / #93a7d0 accent in the nav and footer on every screen.
- Commands are set in ink, not the slate accent, in the start section. Part B reserves the accent for commands, and the hero's 'repose run' already uses it. One of the two is inconsistent.

High findings:
- start-cloud / start-machine @390: The Install row clips the command to '$ curl -fsSL https://repose.run/install.s'. In light it is a hard cut against the copy icon; in dark the last characters fade out. The clipped text caps the screen at 6. This is also the line a visitor copies first. FIX: Let the command wrap at 390. Set white-space: pre-wrap on .cmd with a deliberate break before '| sh', so it renders as '$ curl -fsSL https://repose.run/install.sh' on line 1 and '  | sh' on line 2. Keep 13px Geist Mono. Remove the fade mask in dark.

## customer (6.9)

The hero now works: from the laptop-to-sandbox picture, the agent icons and the undo arc I understood "my dotfiles in a VM per repo, agents run loose, rewind" in about three seconds. Below the hero the page gets weaker. The wheel is big and quiet, but its figures sit small in the top-right corner with a lot of empty canvas under them. The safety section shows the same snapshot-and-undo timeline the hero already showed, and its still never shows anything breaking. Pricing says each memory figure three times. On a phone the install command is cut off at "install.s", and that is the line I came to copy.

Flyer: Almost: the owner would be proud of the hero, but not yet of the repeated undo figure, the pricing that says each figure three times, or a phone install line that cuts off mid-URL.

Directives:
- 1 Three-second value: partly met. The hero at 1440 shows a laptop-to-sandbox copy, the agent icons and an undo arc, so the sandbox, the agents and the undo land. The cloud-or-Mac-mini-or-Linux-server part sits only in the subline, and on 390 the figure is below the fold (hero-390).
- 2 Show, don't tell: mostly met. Every section has a figure and a title. Pricing still leans on captions (pricing-1440), and the wheel adds a text sub-nav (wheel-where-1440).
- 3 Each fact once: not met. Undo/snapshot appears in both the hero and safety-figure. Pricing gives each GB figure three times (pricing-1440), and the 390 repeats the tier bars (pricing-hardware-390).
- 4 Brand colours: mostly met. Slate accent, red only on the wreck time, green only as small dots, no gradients, light sections stay light (safety-1440 stage is the light stage grey). The logo's royal-blue dot is off-palette (hero-1440).
- 5 Subtle and elegant: partly met. The hero and the wheel are quiet. Chips, nested cards, the grey code cells and the boxed toggle add borders (wheel-tools-1440, start-cloud-1440).
- 6 Mac mini named, never first-and-only, no 'own hardware': met (hero subline 'your Mac mini or Linux server'; 'Your machine' elsewhere).
- 7 Desktop wheel large, quiet, part of the background, turns with scroll: met for size and quietness (wheel-where/agents/tools-1440, the arc advances between stills). Its figures are small and pushed to a corner.
- 8 No notifications pitch, no 'No token markup', no agent names in hero, no 'Full permissions' pill: met (hero-1440 uses icons only; no pill anywhere).

Screens: hero 7.8, wheel-where 7, wheel-agents 7.4, wheel-tools 7, safety 6.8, safety-figure 6.3, pricing 6.9, pricing-hardware 6.7, start-cloud 6, start-machine 6

Systemic:
- Undo appears twice: the hero's snapshots strip with its 'undo' arc and the 02 Safety timeline with its 'restore 21:25' arc are the same figure. Each fact once (A5.3) is broken at the page's core claim.
- Pricing repeats each number: '8 GB running at once', the 2-of-8 bar and 'Two 4 GB sandboxes' say one thing three times. On 390 the bars also come back as a separate Solo/Plus/Pro block under the cards.
- The logo's second dot is a saturated royal blue (about #2563eb) in the header and the footer. The design system has one slate-blue accent (#4b6496 / #93a7d0), and the logo dot is the most visible off-palette colour on the page.
- Boxes inside boxes come back below the hero: bordered chips (billing-api, web-app, ml-notebook, your-project), bordered browser and SSH cards in a bordered tree, the bordered sandbox window on a grey stage, and code cells on a grey fill inside a ruled table. A5.5 asks for fewer borders.
- Section labels and copy weight: '01 SETUP', '02 SAFETY' and the others are fine, but the wheel section adds a three-item sub-nav ('Where it runs / Accounts / Your tools') that repeats the titles above it.

High findings:
- start-cloud @390: The install command is clipped mid-URL: '$ curl -fsSL https://repose.run/install.s' with nothing visible after it. There is no fade or scroll hint, so the visitor cannot read the command they are about to pipe to sh. Clipped text caps this screen at 6. The same happens on start-machine 390. FIX: Let the command wrap. Use white-space: pre-wrap with overflow-wrap: anywhere on the code cell, and indent the wrapped part 2ch after the '$'. Or drop the font to 12.5px and put a 24px fade on the right edge with overflow-x: auto. Wrapping is better because the whole command stays visible.
- safety-figure @1440: It repeats the hero. The hero figure already shows snapshots on a timeline with an 'undo' arc, and this figure shows the same timeline with a 'restore 21:25' arc. The still also never shows a wreck: the sandbox window is intact, so the only sign of breakage is a red '21:31' label. 'Let the agent break the whole machine' is not shown. It breaks A5.3. FIX: Remove the snapshots strip from the hero card. Keep the agent icons there and let the section 02 figure own undo. In the static/reduced-motion frame of the safety figure, show the broken state: files struck through in red ('rm -rf your-project/', 0 files), then the restored tree beside it, so the still tells the whole story.

## copy (7)

The copy is mostly clean. I found no em dashes, buzzwords, "own hardware", notification pitch or applause filler, and the hero line is plain developer English. What drags the page down is repetition: where a sandbox runs is said four times, the snapshot-and-undo figure appears twice, the wheel's side list repeats each slide title, and on phone the tiers are listed twice. The install command is also clipped at 390, which caps both start screens at 6.

Flyer: Close: the owner would put the hero in front of users today, but not a phone page whose first command is cut off at "install.s", or a page that says "cloud or your machine" four times.

Directives:
- A5.1 three-second read: met. The hero at 1440 and 390 shows the H1 "A sandbox per repo, with undo", the laptop-to-sandbox figure, the agent icons and the sub-line naming our cloud, Mac mini and Linux server.
- A5.2 show, don't tell: mostly met. Every section has a figure and a title, with at most one line. It is weakened by the wheel side-list labels and the pricing captions ("Two 4 GB sandboxes") that restate the figures.
- A5.3 each fact once: not met. Where it runs appears four times (hero, wheel-where, pricing-hardware, start tabs). Undo appears three times, with the timeline figure duplicated (hero card and safety-figure). The tiers appear twice on 390 (pricing-hardware 390).
- A5.4 brand colours: met. Slate accent only, green only as the status dot, red only on the 21:31 wreck point. No gradients. Light sections stay light (safety stage is pale zinc in light mode).
- A5.5 subtle and elegant: partly met. The wheel and pricing are calm, but the wheel side list and the pricing captions add labels the figures already carry.
- A5.6 Mac mini named, never first-and-only, never "own hardware": met. The hero names "Mac mini or Linux server" and the rest of the page says "Your machine". "Own hardware" appears nowhere.
- A5.7 desktop wheel large and quiet and turning with scroll: the size and quietness are met on wheel-where, wheel-agents and wheel-tools at 1440. The rotation is not verifiable from stills, though the arc progress differs between the three captures.
- A5.8 no notifications pitch, no "No token markup", no agent names in the hero, no "Full permissions" pill: met. The hero shows icons only, and "bypass permissions on" is a status line in the safety figure, not a pill.

Screens: hero 8, wheel-where 6.8, wheel-agents 7.5, wheel-tools 7.4, safety 7.6, safety-figure 7.6, pricing 7.2, pricing-hardware 6.8, start-cloud 6, start-machine 6, full 7

Systemic:
- Where a sandbox runs is said four times: in the hero sub-line ("on our cloud or your Mac mini or Linux server"), in wheel slide 1 ("Pick where each sandbox runs" with repose cloud / Your machine), in the pricing row "Your machine $10" and in the start tabs "Cloud / Your machine". Directive 3 allows each fact once. The hero and pricing must keep it, so wheel slide 1 has to say something new: mixing both places in one account.
- One product carries three names: "sandbox" (H1, wheel, pricing), "Linux VM" (hero sub-line) and "Your sandbox" / "your-project" (figures). The cloud also has three names: "our cloud" (hero), "repose cloud" (wheel) and "Cloud" (start tab). Pick one per noun: "sandbox" for the product, "repose cloud" for the cloud, with "Cloud" kept as the short tab label.
- Labels repeat the headings next to them. The wheel side list (Where it runs / Accounts / Your tools) restates each slide title. The start rows ("Run in a repo") repeat the title "Run it in your repo". The pricing tiers say "8 GB running at once" and then "Two 4 GB sandboxes" under a bar that already shows it.
- The snapshot timeline with an undo arc appears twice: in the hero card and in the safety figure. The second is the full version, so the hero one makes the reader parse the same picture twice.

High findings:
- start-cloud @390: The Install command is clipped at the right edge and reads "$ curl -fsSL https://repose.run/install.s". The visible text is a broken command, and a reader who types it by hand gets a 404. Clipped text caps the screen at 6. The same clip appears on pricing-hardware 390. FIX: Let the command wrap inside its row: white-space: pre-wrap; overflow-wrap: anywhere; on the code element at <=480px, with the copy button pinned to the top right (align-self: start). The line breaks before "| sh" and the whole command stays visible. Do not shrink the mono below 13px.
- start-machine @390: The Install command is clipped the same way ("...repose.run/install.s"). FIX: Same fix as start-cloud: wrap the command at <=480px with pre-wrap and overflow-wrap: anywhere.

## Out of scope (for a later lane)

- art: The pricing tiers show disk and egress figures (100 GB / 250 GB, 250 GB / 500 GB, 500 GB / 1 TB) that direction.md A6 does not list. Check them against apps/web/src/content/docs pricing before shipping.
- art: The install URL https://repose.run/install.sh, and whether a shorter alias exists for mobile, is a docs and CLI distribution question.
- customer: I could not check whether 'repose host add' is the documented command for adding a Mac mini or Linux server, and whether 'repose run' is right for the cloud path, because I judged from screenshots only. Someone should check apps/web/src/content/docs/cli.md.
- customer: The 'First 12 months, first 30 subscribers' founding-price terms need checking against the billing docs; direction.md A6 gives only '$39 founding then $49'.
- copy: The fact "First 12 months, first 30 subscribers" for Solo founding pricing is not in direction.md A6. Confirm it against apps/web/src/content/docs pricing before it ships.
- copy: The hostname "your-project.repose" in wheel-tools and the command "repose host add" in start-machine need to match the CLI and docs (cli.md). This cannot be verified from stills.
- copy: The egress figures (250 GB / 500 GB / 1 TB) come from the billing plan definitions, which are outside the landing scope.
