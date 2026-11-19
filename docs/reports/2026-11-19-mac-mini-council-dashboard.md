# The dashboard, reviewed as a customer — 19 November 2026

The dashboard half of the 19-council review (`docs/REVIEW.md`). Every page and
every action of the **live** dashboard at `https://repose.run`, release
r20261119-6, at 1440 and 390 wide, in both colour schemes, judged at 1x on the
viewport. Driven with Playwright against production, not against a local build.

The command-line half and the Mac mini half are separate sessions; this file is
one of the three the council confers from.

## What would make a user scream

1. ~~**"The code it gave me for my Mac doesn't work."**~~ **Fixed on production
   at about 21:50 UTC, while this review was being written, by recording one api
   setting.** For the hours I measured it, Settings → Hosts handed out a
   43-character token that `repose host add --join` cannot take. The dialog was
   falling back silently; see the re-check under [P0-1](#p0-1). What is left is
   the silent fallback itself, not a P0.
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
6. **"I read the page about the plan, clicked the link to the price, and the
   price page says there are only three plans."** No price for the Own hardware
   plan on the landing page, on Pricing, or anywhere in the dashboard.
   [P1-5](#p1-5)

Counts at the close of the review: **2 P0, 4 P1, 9 P2** — the ranking and what
moved are in [Final ranking](#final-ranking-and-what-moved). P0-1 was a P0 when
measured and was fixed on production before this report was filed; the silent
fallback behind it is still open.

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

#### Re-checked at 21:50 UTC: the symptom is gone from production, and my diagnosis was wrong

The release conductor recorded `host_install_command` on the live api as
`repose host add --join %s`. I added a host again and read the dialog:

```
recheck-2
Run this on the machine. It is not shown again.
repose host add --join <340-char base64 argument>        [Copy]
                                                          [Done]
```

The copy around it changed too — "Copy this token now. It is not shown again,
and it is used once." became "Run this on the machine. It is not shown again.",
and the "What to do with it" link is gone. **Copy puts the whole 363-character
command on the clipboard, byte-identical to `install.command`.** Verified by
reading `navigator.clipboard` after pressing it.

So the deployed dashboard was reading `install.command` **all along**, and fell
through to `join_token` only because the api had no command recorded. Nothing
was deployed between my two measurements; one setting was recorded.

**That makes the diagnosis above wrong, and the rank wrong with it.** I wrote
that the dialog "chooses the wrong field". It does not; it has a fallback, and
the fallback fired. The fault was never in the field the dashboard reads.

**What survives, and it is a real card rather than a P0:** the fallback is
silent and its product is unusable. With `host_install_command` unset the dialog
hands out a bare 43-character token under confident copy telling the customer to
copy it now because it is used once — and nothing can be done with it. A
fallback that produces an unusable value and says nothing is how a configuration
gap became, for the hours I measured it, a product that could not be bought and
used. If the setting is ever lost — a new environment, a fresh api, a bad
deploy — it returns in silence. That is the thing to fix, and it is what 19a's
PR #78 should be judged against.

The earlier measurement stands as a measurement: at 20:49 UTC on production the
dialog showed a 43-character `join_token` and `install.command` was the empty
string. Both are true; the cause was not what I said it was.

One residue from the re-check, P2: the `<pre>` holding the command is
`clientWidth` 362 against `scrollWidth` **2831** with `overflow: auto`, so a
customer sees roughly an eighth of the line. Copy works, so this is cosmetic —
but anyone who selects it by hand, or reads it to check what they are about to
run on their Mac, gets a sliver.

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

**A prediction I made here was wrong, and I am striking it rather than hiding
it.** I first wrote that an Own-hardware customer, whose plan carries
`memory_gb: 0` and `disk_gb: 0`, would be measured the same way and would read
"X GB of 0 GB · over" permanently. **That does not happen.** The conductor
checked the page against the code: `billing/+page.svelte` derives
`noFleetLimits` from `limits.memory === 0` and takes a different branch
entirely, and `diskOver` requires `limits.disk > 0`, so the red bar cannot fire
on a plan whose disk limit is zero (I-1364). I did not read that code myself —
I am reporting the conductor's check, not my own.

I am naming the mistake because it is the inverse of the trap this review is
built to avoid: a reviewer *not seeing* something is a gap, and belongs in
"what I could not run"; a reviewer *predicting* something is a claim, and this
one was checkable and false. Left in, it would have sent whoever fixes this
hunting a bug the code already prevents, and the card would have read as
evidence that the plan is unusable when it is not.

**What survives is the whole of the P0 and none of the prediction:** on *this*
account `/billing` measures against one set of numbers while `GET /v1/me`
reports another, prints "over" in red, says creating is refused, and then
creates in 6.0 s. A real customer can be billing-exempt — the owner is — so a
real customer can see exactly this.

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

#### Re-measured at 22:00 UTC, because I doubted my own finding

While measuring the fleet question below I saw a stranded project whose row
carried no host line, and I was no longer sure the stale naming was real rather
than a propagation window I had caught at 20:52. So I built the controlled
version: add `stalehost`, create one project on it, remove the host, then read
the `/projects` row at fixed intervals.

| When | The row says |
|---|---|
| host present | `your host stalehost has not registered yet` |
| Hosts tab, after removal | **"No hosts of your own."** |
| removal + 0 s | `your host stalehost has not registered yet` |
| removal + 30 s | `your host stalehost has not registered yet` |
| removal + 60 s | `your host stalehost has not registered yet` |

**It stands, and it is not a propagation window.** My doubt came from looking in
the wrong place: the reason is carried on the `/projects` row and **not** on the
machine's own page, which shows `error` and a Restart button and never says why.
That is a second, smaller finding — a customer who clicks into the broken
project to find out what is wrong is told less than the list they came from.

#### Measured at 21:55 UTC: the dashboard cannot send a project to the fleet while a host waits

The conductor asked me to measure this rather than agree to a rank, and it
decides whether ENG-28 is a P0 or a P1. It is a P0, and as a description rather
than a judgement.

I added a host, left it in `waiting`, and created a project from the dashboard.

- **The New project form is identical with and without a host.** Name, Size,
  Keep it, Repository. I diffed the fields and the `<select>` options in both
  states: the size list and the keep-for list, nothing else. No placement
  control, no host picker, and no sentence anywhere on the form saying where the
  machine will run — I searched the form text for *fleet*, *placement*, *host*,
  *where it runs* and *repose's servers* and matched none of them.
- **The dashboard does not send the field.** The request body, verbatim:
  `{"name":"fleetcheck-1","class":"large","expires_in_s":3600,"tz":"UTC"}`.
- **The server defaults it to the waiting host.** `POST /v1/projects` → **201**
  with `placement: "own"`, `host: null`, `state: "creating"`.
- **8.0 s later the project is in `error`**, and the only control on it is
  **Restart**, which cannot work while the host is waiting.

`own-hardware.md` says a project can ask for repose's servers with
`placement: "hosted"` — and names the API as the way to do it. There is no such
control on the dashboard, so a customer whose install never completes has a paid
account and no way to make a working machine by any route the dashboard offers.

**The recovery exists, and only in a state the customer has not reached.** With
the host removed, I pressed **Restart** on the same stranded project:
`POST …/start` → 202, and it reached `running` in **17.0 s**. So the project is
recoverable — but only after the customer works out, unaided, that the fix for
"your host has not registered yet" is to delete the host they just bought a Mac
for. Nothing on either page says so.

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

**And the public side is no better.** I checked the live pages signed out, as
myself:

| Page | Says |
|---|---|
| the landing page | no "own hardware", no "mac mini", no "$10" |
| `/docs/billing` — **titled "Pricing"** | subtitle: *"**The three plans**, what each one buys, the free week…"*. Prices named in the body: **$49, $39, $89, $169, $39, $49, $0**. No $10. |
| `/docs/own-hardware` | links the words **"Own hardware plan"** to `/docs/billing` — the page that says there are three |

So a customer who reads the one page that documents the product, and follows
its own link to find the price, lands on a page that tells them the plan they
just read about does not exist. There is no route to the price: not the
landing page, not Pricing, not the dashboard. (The Mac reviewer found the same
on the served HTML and across all 26 live docs pages; the table above is my own
measurement of the rendered pages.)

**One caveat I want kept with this.** The plan surface in the dashboard is very
likely gated on `status: "exempt"` and a normal account probably sees a picker.
I could not test that; I had one account and it is exempt. What is certain is
that the account I had was given no way to see a plan, a price or a change
control, and that no public page carries the price at all.

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

## Final ranking, and what moved

At the close of the review, on the dashboard surface: **2 P0, 4 P1, 9 P2.**
Three findings changed rank during the review and every one of them moved
because of a measurement, not an argument.

### P0

| # | Finding | Card | Moved? |
|---|---|---|---|
| 1 | Billing measures the account against the wrong plan's limits, prints "over" in red, says creating is refused, then creates in 6.0 s | ENG-34 | no |
| 2 | A host in `waiting` leaves the account with no way to make a working machine by any route the dashboard offers | ENG-33, ENG-28, ENG-65 | **P1 → P0** |

**What moved #2:** I wrote it as P1-3 and agreed to the conductor's P0 without
measuring, which was the wrong order. The 21:55 measurement settled it: the New
project form is identical with and without a host, the request body carries no
placement field, and the server silently defaults to the host that is not there.
A paid account with no working machine by any route is a description of P0, not
a judgement about it.

### P1

| # | Finding | Card | Moved? |
|---|---|---|---|
| 3 | A temporary machine lists six `kept` restore points and discards all of them | ENG-50 | no — argued P1, flagged that the council may read it as P0 |
| 4 | The Projects table clips the Disk column at 1440 | ENG-24 | no |
| 5 | An expired session drops you on the marketing page with no word and no return | ENG-44 | no |
| 6 | No route anywhere — dashboard, landing or Pricing — tells a customer what the plan costs | ENG-51 | no (overlaps the Mac reviewer's ENG-47; merge them) |

### P2

| # | Finding | Card | Moved? |
|---|---|---|---|
| 7 | The host dialog's fallback is silent and its product is unusable | ENG-29 | **P0 → P2** |
| 8 | A refusal raised while a dialog is open is drawn under its backdrop | ENG-25 | **P1 → P2** |
| 9 | A refused destroy's reason lives 4.1 s while the panel stays unchanged | ENG-52 | **P1 → P2** |
| 10 | The sign-in page is light-only in dark mode and says "Powered by Logto" | ENG-53 | no |
| 11 | Questions clip to one line; unreadable on a phone | ENG-54 | no |
| 12 | No standalone webhook view; one delivery shown instead of twenty | ENG-56 | no |
| 13 | The join command is about one eighth visible | ENG-63 | no |
| 14 | The machine's own page says less than the list it came from | ENG-65 | no |
| 15 | A grey smear under the frozen project row | ENG-48 | no |

**What moved #7:** the symptom was fixed on production at 21:50 by recording
`host_install_command`, with nothing deployed, which proved my diagnosis wrong —
the dialog reads `install.command` and fell back, it does not choose the wrong
field. **What moved #8:** I measured that the toast is drawn rather than absent.
**What moved #9:** I first recorded "nothing happens and nothing is said" after
checking at +12 s, then measured 0.29 s and 4.1 s.

### Dropped, and why

Recorded here so a second pass does not rediscover them and the owner does not
read them twice.

- **"An Own-hardware customer would see 'X GB of 0 GB · over' permanently."**
  Struck. The code prevents it (`noFleetLimits`, `diskOver`, I-1364). It was a
  prediction, not an observation, and it was checkable and false.
- **The CSP console error** on every page — the Cloudflare RUM beacon is blocked
  by the site's own `script-src`. Not filed: no customer sees it, and the only
  cost is that the analytics collect nothing.
- **`repose-billing-mode` surviving sign-out.** Not filed: a display hint,
  overwritten at the next sign-in.
- **The Resize panel opening below the fold**, far from the button that opens
  it. Not filed; it sits in the copy notes.
- **The copy nits** in [P2-7](#p2-7) — the space in "Remove `host-1` ?", 39.1 GB
  against 40 GB on one card, the sentence-case inconsistency across refusals,
  the missing actions-column header. Not filed as cards. One card per typo is
  how a board stops being read; they are a single editing pass and the report is
  where that pass should start.

## Verdict

The dashboard is in better shape than this list suggests. The flows are
well-built, the irreversible actions are guarded, the copy is mostly in the
house voice, dark mode is honoured everywhere the dashboard itself renders, and
the measured timings match the documented ones exactly.

One thing blocks the launch. [P0-2](#p0-2) is one wrong source for three
denominators, and until it is fixed the billing page tells customers a false
thing about their own account in red, on an account type a real customer can
have.

[P0-1](#p0-1) was the other, and it was fixed under me — by recording one api
setting, not by changing the dashboard. I was wrong about why it happened, and
the report says so where the finding is rather than in a footnote. The silent
fallback that produced it is still there and is what PR #78 should be judged
against.

**I do not sign the gate on the dashboard surface.** Fix P0-2, and the five P1s
are an afternoon.
