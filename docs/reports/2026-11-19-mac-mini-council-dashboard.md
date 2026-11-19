# The dashboard, reviewed as a customer — 19 November 2026

The dashboard half of the 19-council review (`docs/REVIEW.md`). Every page and
every action of the **live** dashboard at `https://repose.run`, release
r20261119-6, at 1440 and 390 wide, in both colour schemes, judged at 1x on the
viewport. Driven with Playwright against production, not against a local build.

The command-line half and the Mac mini half are separate sessions; this file is
one of the three the council confers from.

## What would make a user scream

1. **"The code it gave me for my Mac doesn't work."** Settings → Hosts → Add a
   host shows a 43-character token. `repose host add --join` takes a 339-character
   envelope that the same API response also returns and the dialog never shows.
   The value is one-shot, so the only recovery is remove-and-re-add, which hands
   them the same wrong value again. [P0-1](#p0-1)
2. **"It told me I was over my limit, so I deleted three projects. I wasn't."**
   Billing measures the account against the wrong plan's limits, prints "over" in
   red, says creating is refused — and then creates a project in 6.0 s with that
   banner still on screen. [P0-2](#p0-2)
3. **"I had six restore points and now they're gone."** The Snapshots tab listed
   six snapshots marked `kept`, each with a Restore button, on a machine that
   discards all of them at destroy. [P1-2](#p1-2)
4. **"I can't read how much disk I'm using."** At 1440 the Projects table clips
   the Disk column to `15....`. [P1-1](#p1-1)
5. **"I was signed in a minute ago and now I'm on the marketing page."** An
   expired session drops you on `/` with no message and no way back to the page
   you asked for. [P1-4](#p1-4)
6. **"I can see the plan exists but not what it costs."** No plan name, no price,
   no change control anywhere on `/billing`. [P1-5](#p1-5)

Counts: **2 P0, 5 P1, 7 P2.**

## Method, and who I was

| Step | As whom |
|---|---|
| Signed-out pages, deep links, the sign-in page, sign-up entry | myself, no session |
| Everything signed in — Projects, machine pages, Billing, Settings and every tab, Hosts, Triggers, Secrets, Tokens, Questions | the **owner's account**, through `ops/dev/dashboard-session` |
| Creating, stopping, starting, snapshotting and destroying test projects | the owner's account |
| Adding and removing hosts | the owner's account |
| Sign out | the owner's account |

The owner's session mints an hour at a time and cannot renew; I re-minted at
20:35, 20:55 and 21:16 UTC rather than wait to be surprised by an expiry.

**The host in this report was mine, not a customer's.** I added `host-1` at
about 20:46 UTC and `mac-mini` at 20:49, and removed both by 20:53. Production
held no customer host at any point. Everything in [P1-3](#p1-3) follows from my
test host, and the capture itself — an account that owns a host sending its new
projects there — is intended under I-1366 and must not be "fixed".

### What I could not run, and why

- **I did not run `repose host add --join <bare token>` to watch the CLI reject
  it.** On x86 Linux that command installs NixOS over the box
  (`own-hardware.md`, "What an x86 box needs"), and this is the shared dev box.
  [P0-1](#p0-1) rests on the two values and the doc, not on a failed install.
- **I did not walk a plan change or a checkout.** The owner's account is
  `billing.status: "exempt"` and `/billing` renders no plan surface at all, so
  there was nothing to walk. No money moved and no purchase was begun. Whether a
  non-exempt account sees a plan picker is **untested** — I had one account.
- **I could not see a pending question.** "Waiting on you" was empty for the
  whole review, so [P2-5](#p2-5) is measured on answered questions and the claim
  that a *pending* one clips the same way is **unverified**.
- **I did not confirm whether signing in returns you to the page you asked for**
  ([P1-4](#p1-4)); completing a real Logto sign-in was out of reach.
- Readings of project counts and fleet memory move during the review because the
  command-line session was working in the same account. No finding here rests on
  a count.

---

## P0

### P0-1 — The code the dashboard gives you for your Mac is not the one the install step takes {#p0-1}

**Reproduce.** Settings → Hosts → **Add a host** → name it → **Add**. A dialog
appears: *"Copy this token now. It is not shown again, and it is used once."*
with a 43-character value and a **Copy** button.

**Measured.** In the same page load I read the `POST /v1/hosts` 2xx body:

| field | value |
|---|---|
| what the dialog shows | 43 characters |
| `join_token` | 43 characters — **identical to what the dialog shows** |
| `install.argument` | **339 characters** of base64 |
| `install.command` | `""` — the empty string |
| `install.url` | `https://repose.run/docs/own-hardware` |
| decoded `install.argument` top-level keys | `v`, `token`, `wg` |
| its `wg` keys | `private_key`, `address`, `edge_pubkey`, `edge_endpoint` |
| `envelope.token === join_token` | **true** |

`repose host add --join` takes the **argument**, which carries the token *and the
machine's place on repose's network* (`own-hardware.md`, "Adding it"). The dialog
shows the inner token alone. The WireGuard placement is dropped on the floor.

**Why it is P0.** This is the only thing the dashboard does for the Own hardware
product, and it cannot complete. The value is one-shot — "not shown again" — so a
customer's only recovery is **Remove** and **Add a host** again, which hands them
the same wrong value. They will do this twice and then write to support.

**The docs and the code disagree, and the code is wrong.**
`apps/web/src/content/docs/own-hardware.md` says: *"On the dashboard, Settings →
Hosts does the same three things. **Add a host** takes a name, or none, and shows
**the step to run on the machine** once, with a copy button."* It does not show
the step. `install.command` arriving empty says the server has a slot for that
step and is sending nothing into it.

**One warning for whoever fixes this.** `install.argument` carries a live
WireGuard **private key**. "Show `install.argument` instead of `join_token`" reads
as harmless and is not: no raw argument should reach a log, a card, a PR, or a
screenshot of the dashboard.

Measured 20:49 UTC, reproduced twice. Card: ENG-29.

### P0-2 — Billing says you are over your limit and that new projects are refused, then makes one {#p0-2}

**Reproduce.** Open `/billing`, read the meters and the banner. Then open
`/projects` → **New project** → name it → **Create**.

**What the page says.**

```
Memory running     24 GB of 8 GB · over
Disk held       359.9 GB of 100 GB · over      (red bar, full width)
Egress this period 65.1 GB of 250 GB

Creating and restoring projects, and growing a disk, are refused
until your projects hold less.
```

**What the account actually is.** `GET /v1/me`, same page load:

```
"limits": { "memory_gb": 32, "disk_gb": 500, "egress_gb": 1000,
            "hosts": 1, "projects": 100, "xl": 1 }
"billing": { "plan": null, "status": "exempt" }
```

By its own limits the account is **under** on all three: 24 of 32, 359.9 of 500,
65.1 of 1000.

**Where 8 / 100 / 250 come from.** `GET /v1/billing`, same page load, lists four
plans. The first, `solo`, carries `memory_gb: 8, disk_gb: 100, egress_gb: 250` —
exactly the three denominators the page prints. The page is measuring the account
against a plan it is not on.

**And the consequence line is false.** With that banner on screen I created
`dashreview-t1` through the dashboard. `POST /v1/projects` → **201**, machine
running, **6.0 s** from pressing Create to the machine page.

**Why it is P0.** A customer told in red that their disk is 3.5× over and that
creating is blocked will delete projects to get under a line they were never
near. That is a page causing a customer to destroy their own work. Size is
irrelevant; the brief's rule decides it.

**Unverified, and worth checking before launch:** the `own` plan carries
`memory_gb: 0` and `disk_gb: 0` by design. An Own-hardware customer measured the
same way would read "X GB of 0 GB · over" and carry that red banner permanently.
I could not test this — I had one account, and it is exempt.

Measured 20:54–21:00 UTC. Card: ENG-34.

---

## P1

### P1-1 — The Projects table clips the Disk column at 1440 {#p1-1}

**Reproduce.** Open `/projects` at 1440 × 900. Read the Disk column.

It shows `2.3 …`, `15....`, `14....`. The Agents cell shows `claude` where the
value is `claude, idle` — the agent's state is cut off too. Measured on the
cells: `clientWidth` 61 against `scrollWidth` 66 for `2.3 GB`, 83 against 93 for
`Restore…`. The table is 872 px wide inside a 1440 viewport with 168 px of empty
margin to its right, so nothing forced this.

The **phone** layout at 390 shows the same rows stacked and prints every value in
full — `2.1 GB`, `15.0 GB`, `14.8 GB`. The desktop view carries less information
than the phone one.

Disk is a limit a customer is held to and, on three of the four plans, a thing
they pay for. Card: ENG-24.

### P1-2 — Six restore points that are discarded the moment you would want them {#p1-2}

**Reproduce.** Create a temporary project. Stop and start it a few times with the
**Snapshot on stop** box left at its default (checked). Open the **Snapshots**
tab.

I measured six stop/start cycles between 20:56 and 21:06 UTC. The tab then read:

```
6 snapshots                                    [Create snapshot]
Taken               Size     Reason   Expires
2026-11-19 21:06    0.0 GB   stop     kept      Restore…
2026-11-19 21:05    0.0 GB   stop     kept      Restore…
… four more, all "kept", all with Restore…
```

Meanwhile the **Overview** of the same machine says, in the Destroy panel and
again in the destroy confirmation:

> Deletes the disk. It is temporary: destroying it keeps no snapshot and it
> cannot be restored.

**Which is true?** I destroyed it and looked. `dashreview-t1` does **not** appear
under "Recently destroyed" on `/projects`, and left no snapshot and no Restore.
The Overview was right. The Snapshots tab was wrong for the whole hour, in the
one column — `Expires: kept` — that answers "will this still be there".

**Rank.** I put this at P1 rather than P0 because the irreversible action's own
confirmation states the truth plainly, in the sentence directly above the
type-to-confirm box, so a customer must read past an explicit warning to lose
work. If the council reads the Snapshots tab as the surface a customer checks
*before* they ever reach that confirmation — which is how I would use it — it
belongs at P0.

Secondary: **Snapshot on stop** was on by default on a machine that cannot keep
one, so six stops produced six dead snapshots and nothing said so.

### P1-3 — Projects tell you to fix a host the Hosts tab says you do not have {#p1-3}

**Reproduce.** Add a host. Do not register it. Create a project. Remove the host.
Look at `/projects`.

Five projects sat in `error` reading **"your host host-1 has not registered yet"**
and **"your host mac-mini has not registered yet"**, while Settings → Hosts read
**"No hosts of your own."** The only control on each row is **Start**, which
cannot succeed. Nothing links the error to the Hosts tab, and nothing says the
way out is destroy-and-recreate.

**The capture is intended.** Under I-1366 an account that owns a host has its new
projects placed there; a customer with a teammate would see the same and it would
be correct. What is not designed is the dead end around it:

- nothing warns at creation time that the project is being committed to a host
  that has never registered;
- the resulting state offers only an action that cannot work;
- removing the host does not clear the rows that name it.

Those five projects were the command-line session's, landing on **my** test host
at 20:46–20:53 UTC. Nothing here was a production fault. Card: ENG-33.

### P1-4 — An expired session drops you on the marketing page without a word {#p1-4}

**Reproduce.** Take a signed-in browser state, expire its access token by 60
seconds, open `https://repose.run/projects`.

Measured: the dashboard makes **no API call** but `GET /v1/public/seats`, rewrites
the URL from `/projects` to `/`, and renders the landing page — hero, install
command and all. No "your session has ended", no sign-in prompt, no return path.
Because the URL is rewritten, reload and Back do not recover it.

The same happens to a signed-out visitor opening a bookmarked deep link:
`/projects`, `/billing`, `/settings` and `/questions` all return 200, rewrite to
`/`, and show the landing page.

This is the single most common thing that will happen to every signed-in
customer. The owner's own minted session lasts an hour. Card: ENG-44.

### P1-5 — The dashboard never says what the plan costs or what it gives {#p1-5}

On the owner's account `/billing` contains, in `main`, **zero buttons and zero
links**. I searched every leaf element on the page for a plan name or a price:
no `Solo`, no `Plus`, no `Pro`, no `Own hardware`, no `$`, no "per month". The
page is a green banner reading "This account is billing-exempt." and three usage
meters.

In the same page load, `GET /v1/billing` returned all four plans with their
prices and `"available": true`, including `own` with `hosts: 1`.

The Own hardware plan is live, is deliberately held off the public Pricing page
until this review passes, and is also absent from the dashboard. **On 19 November
2026 there is no surface anywhere that tells a customer what it costs.** If the
dashboard is meant to be the door while Pricing is held, the door is not there.

**This is the weakest-evidenced item in the report and I want that said.**
The plan surface is very likely gated on `status: "exempt"` and a normal account
probably sees a picker. I could not test that; I had one account and it is
exempt. What is certain is that the account I had was given no way to see a plan,
a price or a change control.

---

## P2

### P2-1 — A refusal raised while a dialog is open is drawn under the dialog's backdrop {#p2-1}

**Reproduce.** With one host already added, Settings → Hosts → **Add a host** →
any name → **Add**.

`POST /v1/hosts` returns **409** `{"code":"conflict","message":"your plan includes
1 host; remove one before you add another"}`. A toast carrying that sentence
appears bottom-right — and `document.elementFromPoint` at the toast's own centre
returns `DIALOG.hk-modal`. The toast is `z-index: 999999999`, but a native
`<dialog>` opened with `showModal()` lives in the top layer, above any z-index,
so the toast renders under the backdrop. At 1x the crop is washed out and barely
legible.

The dialog meanwhile keeps its neutral hint, "Leave it empty and it is named for
you", so the thing the customer is looking at says nothing happened.

**The inconsistency is the real finding:** the *name taken* 409 from the same
endpoint **is** shown inline, in the dialog, as "You already have a host called
host-1." One class of refusal reaches the customer where they are looking and the
other does not. Card: ENG-25 — filed at `high` on first reading, downgraded here
after measuring that the toast is drawn rather than absent.

### P2-2 — A refused destroy says "an operation is in progress" for 4.1 seconds {#p2-2}

**Reproduce.** Create a project. Four seconds later, while it still reads
`starting`, open Destroy, type the name, press the red **Destroy**.

`DELETE /v1/projects/<id>` returns **409** `{"code":"conflict","message":"an
operation is in progress"}`. Measured: the toast appears at **0.29 s** at (956,
848), 144 px below and 148 px left of the Destroy button at (1104, 704) — inside
the viewport — and is **gone by 4.38 s**, a lifetime of 4.1 s. The destroy panel
stays open with the name still typed and the button still enabled, so the control
the customer is staring at does not change at all.

The line itself is a server code phrase. It does not say *which* operation, that
the machine is still starting, or to wait and try again — the test DECISIONS
I-484/I-485 set for a line a customer reads.

I first recorded this as "nothing happens and nothing is said" after checking at
+12 s. That was wrong and the measurement corrected it.

### P2-3 — A grey smear under the frozen project row {#p2-3}

The `frozen` state draws a soft blurred gradient band under the row on
`/projects`. At 1x, 1440 wide, it reads as a smudge or a half-loaded image rather
than frost. In dark it is a grey-white streak with no frost association at all.
Against the house taste — clean, sharp, no gradients or glows — it is the one
thing on the page that looks like a rendering fault. Card: ENG-48.

### P2-4 — The sign-in page is Logto's, not repose's {#p2-4}

Every repose page honours the colour scheme. The sign-in page at
`accounts.herakraft.co` does not: `getComputedStyle(document.body).backgroundColor`
is `rgb(251, 251, 250)` at 1440 **and** at 390, in dark **and** in light. A
dark-mode customer gets a full white page.

It also carries a generic blue primary button where the dashboard uses near-black,
and a **"Powered by Logto"** footer. This is the first page a new customer sees
and the page every expired session lands on.

### P2-5 — Questions are clipped to one line with no way to read the rest {#p2-5}

On `/questions`, each row's text is clipped: measured `clientWidth` 672 against
`scrollWidth` **3412** on a 523-character question. The full text is in a `title`
attribute, so a mouse user gets a browser tooltip. There is no expand, no detail
page, and no clickable ancestor — I checked.

**On a phone there is no hover at all**, so a 523-character question renders as
`Airbnb Software Engineering Intern (BS/MS), SF: th…` and the rest is
unreachable. The project names in the same row are clipped to `recrui…` and
`noteb…` with room to spare.

Unverified: whether a question in **"Waiting on you"** — the one a customer must
read before answering — clips the same way. None was pending during the review.

### P2-6 — Parity: no webhook-route view, and one delivery instead of twenty

The dashboard covers the CLI well (see below). Two gaps:

- `repose webhooks list` / `url` / `rotate` / `rm` have no standalone surface.
  Routes are reachable only through a trigger's row on a project's Triggers tab,
  which does carry **Copy**, **Edit**, **Rotate…** and **Remove…**. `/webhooks`,
  `/settings/webhooks` and `/projects/<id>/webhooks` all 404.
- `repose webhooks url NAME` prints **the last 20 deliveries**. The dashboard row
  shows one: `your app :4854/ 200 body_dropped 14h ago`. A customer debugging a
  webhook has one data point on the dashboard and twenty on the command line.

### P2-7 — Copy, line by line

- **Sentence style is not consistent across refusals of the same kind.** "You
  already have a host called host-1." (capital, full stop) against "a project
  named dashreview-t1 already exists" (lower case, none) and "your plan includes
  1 host; remove one before you add another" (lower case, none).
- **"Remove `host-1` ?"** — a space before the question mark, because the code
  span is a separate element. Visible at 1x.
- **Two numbers for one disk on one card**: "Disk 2.0 GB **of 39.1 GB**" with
  "**40 GB** disk" directly beneath it.
- **"Egress this period"** under a heading that says **"This month"**.
- **"Snapshot on stop"** is a bare checkbox beside the Stop button with no
  explanation of what it does or costs, and it is checked by default on temporary
  machines that cannot keep one.
- **"Never freeze this machine"** on the Config tab assumes the customer knows
  what freezing is and what it costs them.
- On the Projects table the actions column has **no header** while every other
  column has one.
- **Good lines, for the record:** "Remove host-1? It stops taking your projects,
  and it joins again only with a new install." — states the consequence and
  stops. "Destroying dashreview-t2. It was temporary, so there is no snapshot to
  restore." — answers the question the customer was about to ask. "Grow to" on
  the resize control, which states the constraint in two words. "Waiting for a
  core" and "Waiting for the server" on the Usage tab, which are CPU steal and IO
  wait said in English.

---

## What is right, measured

These are not findings. They are the numbers and behaviours I checked and found
sound, and the review is worth less without them.

| Thing | Measured |
|---|---|
| Stop, from the dashboard | **4.3 s** (`running` → `stopped`), two readings, identical |
| Start, from the dashboard | **12.3 s** (`stopped` → `starting` at 1.4 s / 1.8 s → `running`), two readings, identical — against the ~12 s in `features/stop-start-destroy.md` |
| Create a project | **6.0 s** from Create to a running machine page |
| Destroy a running machine | **0.8 s** to the confirmation and back to `/projects` |
| Horizontal overflow | none, on any page I checked, at 390 or 1440, in either scheme |

- **Destroy is guarded properly.** The button is `disabled` until the project's
  name is typed, and the panel states the consequence first.
- **Duplicate names are refused well.** Both the host dialog and the new-project
  form show the reason inline, where the customer is looking.
- **A revoked token shows `Revoked 1d ago` and offers no Revoke button** — an
  action on a thing already in that state is simply not offered.
- **Sign out is clean**: 6.0 s to `/`, tokens cleared, a protected page no longer
  opens. One key survives, `repose-billing-mode: "exempt"`, which is a display
  hint and is overwritten at the next sign-in; on a shared computer it briefly
  carries the previous user's billing mode. Not worth a card, worth knowing.
- **404s are proper 404s** on `/webhooks` and the like, with a page offering Docs
  and Home.
- The **Add trigger** flow carries the whole model — *Fires on*: A webhook / A
  schedule; *Then*: Start an agent / Forward to your app — which is one door and
  two receivers, in the dashboard, in the customer's words.
- **Dark mode is correct** on every signed-in page I captured.

## The parity rule

| CLI | Dashboard |
|---|---|
| `run` (create) | New project — name, size, keep-for, repository ✓ |
| `ls`, `status` | Projects, machine header ✓ |
| `stop`, `start` | ✓ |
| `rm` (destroy) | ✓, with type-to-confirm |
| `snapshots list/create/restore` | Snapshots tab ✓ |
| `logs` | Events tab, filtered All / Agents / Errors / Machine ✓ |
| `attach`, `ssh`, `code` | Connect — the three commands, each with Copy ✓ |
| `browser` | Browser tab ✓ |
| `config` (menu, nix, revisions) | Config tab ✓ |
| `secrets` (account and project) | Settings → Secrets, Config tab ✓ |
| `tokens` | Settings → API tokens ✓ |
| `triggers` add/list/on/off/rm | Project → Triggers ✓ |
| `notify` | Settings → Notifications ✓ |
| `host add` | Settings → Hosts — **present and broken**, [P0-1](#p0-1) |
| `host ls`, `host rm` | ✓ |
| `webhooks` list/url/rotate/rm | only through a trigger; no standalone view, one delivery not twenty — [P2-6](#p2-6) |
| `triggers runs` | a trigger row carries its last run; I could not reach a runs listing |
| `github setup/status` | Settings → General ✓ |
| `secrets choose`, `version update`, `completion`, `login` | laptop-side by nature; no dashboard equivalent is wanted |

No rename exists on either side, so that is not a gap.

The rule holds except for hosts and webhooks. Hosts is the one that matters:
the dashboard has the control and the control does not work.

## Combinations I tried

| Combination | What happened |
|---|---|
| Same host name twice | refused inline in the dialog, clearly ✓ |
| A second host over the plan's one-host limit | refused; the reason is drawn under the modal's backdrop — [P2-1](#p2-1) |
| Same project name twice | refused inline in the form, clearly ✓ |
| Destroy a machine that is still starting | 409; a 4.1 s toast, panel unchanged — [P2-2](#p2-2) |
| Reload after the one-shot host token dialog | the token is gone for good; the row sits at `waiting` with no way to re-issue, and nothing says remove-and-re-add is the way out |
| Cancel mid-flow (Add a host, New project, Resize) | all clean ✓ |
| Two tabs, destroy in one | the second tab's page 404s while its own sidebar still lists the project, until it refreshes |
| Signed-out deep link to `/projects` | 200, URL rewritten to `/`, landing page — [P1-4](#p1-4) |
| Expired token mid-session | same — [P1-4](#p1-4) |
| `/design` in production | proper 404 page ✓ |

## Two smaller things, recorded so the second pass does not rediscover them

- Every page logs a CSP console error: the Cloudflare RUM beacon
  (`static.cloudflareinsights.com/beacon.min.js/...`) is blocked by the site's own
  `script-src`, and `POST /cdn-cgi/rum` aborts. Nothing a customer sees unless
  they open devtools, and the real cost is that the analytics collect nothing.
  Not filed as a card.
- The Resize control opens at the **bottom of the page**, far below the
  **Resize…** button that opens it and partly below the fold at 1440 × 900, with
  no scroll to it. Not filed; folded into [P2-7](#p2-7)'s neighbourhood as a
  layout nit.

## Verdict

The dashboard is in better shape than this list suggests. The flows are
well-built, the irreversible actions are guarded, the copy is mostly in the
house voice, dark mode is honoured everywhere the dashboard itself renders, and
the measured timings match the documented ones exactly.

Two things block the launch, and they are specific rather than structural.
[P0-1](#p0-1) is one field in one dialog, and until it is fixed the Own hardware
plan has no working door on the dashboard. [P0-2](#p0-2) is one wrong source for
three denominators, and until it is fixed the billing page tells customers a
false thing about their own account in red.

**I do not sign the gate on the dashboard surface.** Fix those two, and the
remaining five P1s are an afternoon.
