# Mac mini council: the Own hardware path, reviewed as a customer

2026-11-19. Session `19-council-mac`, one of the three in `docs/REVIEW.md`.
The other two are the CLI (`19-council-cli`) and the dashboard
(`19-council-dash`); this one is the path the owner cannot test, because
they do not own a Mac.

I am a developer who bought an M4 Mac mini and heard about repose. I have a
stopwatch. I did not read the code to explain anything here: every finding
below is a sequence with what I expected and what happened, and the two I
could not reproduce say so in their own words.

**What I was reviewing.** repose.run and api.repose.run in production,
release r20261119-6. The CLI changed under me mid-review: I installed
**v0.1.50** at 21:24 UTC and **v0.1.51** at 21:27:52 UTC, two minutes after
it was published. Both numbers are stated wherever they matter.

---

## What would make a user scream

Ranked by how it reaches a customer, not by how hard it is to fix.

| # | Rank | What the customer says | Card | Section |
|---|---|---|---|---|
| 1 | **P0** | "Both machines on my Mac died: one went unreachable after saying Ready, the other never started." | ENG-80 | [F7](#f7-both-machines-on-the-broken-appliance-died) |
| 2 | **P0** | "My agent asked me a question, I answered it, and the agent never heard the answer." | ENG-105 | [F13](#f13-the-ask-reaches-me-the-answer-never-reaches-the-agent) |
| 3 | **P0** | "It refused my Mac for lack of disk, then installed anyway when I ran it again — and what it installed was broken." | ENG-71 | [F1](#f1-p0-candidate-the-disk-guard-refuses-once-then-lets-you-past) |
| 4 | **P0** ✅ | "I ran the install on my Mac mini and it stopped with a 404." — **closed 22:28:08** | ENG-58 | [E1](#e1-p0-the-appliance-image-has-never-been-published) |
| 5 | P1 | "Starting my first machine took seventeen minutes, not the four the page promises." | ENG-103 | [F4](#f4-the-first-machine-on-owned-hardware) |
| 6 | P1 | "My Mac says it is online while the thing running on it booted with a failed service." | ENG-79 | [F6](#f6-the-appliance-boots-with-a-failed-unit-and-the-account-is-never-told) |
| 7 | P1 | "It said my machine did not answer within a minute, after I watched it try for six and a half." | ENG-100 | [F7](#f7-both-machines-on-the-broken-appliance-died) |
| 8 | P1 | "My failed run left a broken project behind, and the fix it told me to run can never work." | ENG-59 | [E2](#e2-a-refused-run-leaves-a-project-in-error-and-prints-advice-that-cannot-work) |
| 9 | P1 | "My Mac install died and repose just says waiting, for ever." | ENG-60 | [E3](#e3-a-dead-install-is-indistinguishable-from-a-slow-one-for-ever) |
| 10 | P1 | "My Mac is listed with gibberish under its name, and on my phone the Remove button is off the screen." | ENG-88 / ENG-89 | [F11](#f11-a-build-hash-under-my-macs-name-and-no-remove-button-on-a-phone) |
| 11 | P1 | "My first run printed three thousand lines of build log over its own summary." | ENG-27 | [F5](#f5-3664-lines-of-nix-and-42-lines-for-the-customer) |
| 12 | P1 | "Am I being charged for every gigabyte my agent downloads?" (unreproduced) | ENG-55 | [B2](#b2-the-own-plan-is-zero-memory-zero-disk-zero-egress) |
| 13 | P1 | "I pressed Add and nothing happened." | ENG-25 | [D1](#d1-the-refusal-renders-behind-the-dialog-that-caused-it) |
| 14 | P2 | "I freed the 60 GB the docs asked for and the install still refused me." | ENG-87 | [F8](#f8-the-cli-counts-in-gib-and-says-gb) |
| 15 | P2 | "Your Troubleshooting page has nothing about the thing I'm troubleshooting." | ENG-57 | [D2](#d2-troubleshooting-does-not-know-this-feature-exists) |
| 16 | P2 | "The one way out of this is documented nowhere I can find." | ENG-64 | [B3](#b3-the-one-way-out-is-named-on-one-page-and-documented-nowhere) |
| 17 | P2 | "The price is one scroll below three plans I can't use." | ENG-47 | [A2](#a2-the-price-is-on-no-page-a-buyer-reads-first) |
| 18 | P2 | "Four of my documentation files were left behind for having 'credentials' in the name." | ENG-104 | [F14](#f14-four-documentation-files-left-behind-for-having-credentials-in-the-name) |
| 19 | P2 | "I named my Mac MacMini and got a regular expression." | ENG-61 | [E4](#e4-the-refusals-are-good-and-one-of-them-is-a-character-class) |

**Three open P0 on this surface, and a fourth already closed.**

**ENG-80 is first because it is the feature.** Across five start attempts on
this Mac with the published build, **two succeeded and three failed**, in two
distinct failure modes. A clean appliance failed as readily as a broken one.

**ENG-105 is second because it is the reason the product exists.** The agent
asks, the owner is interrupted and answers, and the agent — which gave up
after 6.1 seconds against a requested 3 minutes — never hears it.

**ENG-71 is third because it survives everything else.** It is also the one
finding here whose cause I proved and then had to retract as the cause of
the others.

**ENG-58 closed at 22:28:08** when the appliance release was published; I
verified it anonymously. Its cause, **ENG-62**, does not close: nothing in
the release machinery checks that a release's assets exist.

---

## Method, and one thing the next review should copy

Two reviewers scanned the same page tonight with different tools and got
different answers about whether it mentions the Own hardware plan. I had
grepped the `.md` source; the dashboard reviewer read the rendered text.
Both are wrong in the same way, and the resolution is narrower than "use the
rendered text":

> **Locate the hit, not just count it.** A string in a sidebar, a
> previous/next pager or a hidden search palette is site chrome. The page
> has not said anything.

The method that settled it is in [A2](#a2-the-price-is-on-no-page-a-buyer-reads-first):
walk every text node and record the element path of each match.

A second rule earned the same way: **the first reading is not the finding.**
At 21:29 the live docs contradicted the CLI that had shipped two minutes
earlier. At 21:31 they did not. A deploy lag filed as a defect is a defect
the owner reads and cannot reproduce. See [A3](#a3-what-i-did-not-file).

A third, which cost me a retraction: **a finding carries the version it was
measured on and the state of the deployment it was measured against.** Two
releases *and* a live settings change happened during this review, and I
credited a fix to the one I could see. See
[C1](#c1-the-command-printed-the-argument-and-called-it-the-step--fixed-under-me).

And a fourth, which is about the product rather than the method, but was
learned the same way and belongs where it will be read:

> **A warning on a web page does not warn anybody.** The person who needs
> *"Treat it as a password until the machine has joined"* is looking at a
> terminal, has already run the command, and will never open that page
> again. See the engineer's verdict.

---

## A. Finding repose, and reading it as a buyer

### A1. A stranger does not find repose

I searched for exactly what the product does — a persistent machine for
coding agents on a Mac mini. Ten results came back and none of them was
repose: The New Stack, `techpunch/agent-machine`, hyperbox.sh, paradev,
prommer.net and others, several of them selling the same thing. I reached
the product because I was told the URL.

Not filed. Nothing is broken and nothing has been marketed yet. It belongs
in the report because the rest of this section assumes a buyer who arrives
at repose.run already interested, and that buyer does not exist yet.

**Stopwatch.** repose.run answers in 0.20 s to first byte and 0.24 s total
(three runs: 0.24, 0.41, 0.39 s), 183 KB of HTML. Rendered to network-idle
in Chromium: 1.29 s at 1440, 1.24 s at 390. `/docs/own-hardware`: 0.17 s.
Nothing on this site is slow.

### A2. The price is on no page a buyer *reads first*

**What I expected.** To find out what the Own hardware plan costs before
deciding to buy a Mac mini's worth of commitment.

**What happened.** The landing page does not mention own hardware, a Mac
mini, or a price for one. Its served HTML contains zero occurrences of "own
hardware", "mac mini" or "$10". Its pricing block, captured at 1440 and
read at 1x, is three columns — Solo $49/$39, Plus $89, Pro $169 — under the
heading "Launch prices" with "19 seats left" beside it. There is no fourth
column.

The feature is reachable from `/docs`, where the sidebar carries **Account →
Your own hardware**. That page is good and I say so in
[A4](#a4-what-the-own-hardware-page-gets-right). Its last section is called
**What it costs**, and it names no number. It links the words "the Own
hardware plan" to `/docs/billing` — twice, once in the opening paragraph and
once from "What it costs".

`/docs/billing` is titled **Pricing** and subtitled *"The three plans, what
each one buys, the free week, and what happens when you change, cancel or
miss a payment."* It prices Solo, Plus and Pro. It does not price the plan
that sent me there.

So the customer journey is: the one page that documents the product sends
you, on the words "the Own hardware plan", to a page telling you there are
three plans.

**Reproduction.** Fetch all 26 docs pages as markdown from `llms.txt`:

```
grep -ril "own hardware" md/     # own-hardware.md and cli.md, nothing else
grep -rn  '\$10\b'      md/     # no match
grep -rho '\$[0-9][0-9.]*' md/ | sort | uniq -c
      2 $49   2 $39   1 $89   1 $169   1 $0.05
```

**Where my first measurement was wrong, and the one that replaced it.** The
grep above is of the markdown source, which has no navigation. The dashboard
reviewer checked the rendered page and found "Your own hardware" present,
which would have sunk the card on the first Ctrl-F. Walking every text node
in the rendered DOM and recording each match's element path settles it —
three matches, all chrome:

| Match | Element path |
|---|---|
| "Your own hardware" | `aside.sidebar > nav > ul.items > li > a` |
| "Your own hardware" | `main > nav.pager > a > span.pager-title` |
| "Your own hardware" | `dialog.pal > ul.pal-list > li > span.pal-label` |

A scan of `.doc`, the article body, returns false. A scan of `main` returns
true, and the only reason is the "Previous" pager — the link the reader just
came from. Ctrl-F finds three hits and all three are the way back.

**And then the correction that matters more than the finding.** The absence
is **deliberate**. Commit `7db572b5` on `ws/19d-plan` is titled *"ws 19d
plan: offer Own hardware publicly (drop this commit to ship the plan without
selling it)"* — the held commit ENG-16 tracks. It adds an `## Own hardware`
section to `/docs/billing` reading *"the Own hardware plan is **$10 a
month** for one host you own"*, adds *"If the machine is yours, Own hardware
is $10 a month instead"* to that page's opening line, adds the plan to the
landing, and ships tests asserting the price matches `plans.go`. Production
reads the way it does because someone chose it, and the fix is written.

So I spent an hour measuring, three ways, the absence of a price that a
teammate had deliberately removed — and one `git log` would have told me.
The method line this earns is the second half of the one above, and it is
the more expensive half:

> **Check whether the absence is on purpose.** A reviewer who only looks at
> production cannot tell a dormant feature from a broken one, and will
> reliably file the dormancy — the most expensive false finding there is,
> because it looks like diligence.

**What actually survives, and is small and real:** the held commit does not
touch `own-hardware.md`. Its two links on the words "Own hardware plan"
(lines 8 and 120) point at `/docs/billing` with **no fragment**, while the
held commit puts the price at the anchor `#own-hardware`, *below* Solo, Plus
and Pro. So after it lands, a buyer following either link still arrives at
the top of a page about three plans they cannot buy, and must scroll past
them. Two characters each, and none of the held commit's tests would catch
it, because they assert the price matches `plans.go` rather than that anyone
can reach it.

**And a second correction, from the confer, narrowing it again.** I wrote
that no surface tells a customer the price. The command-line reviewer
re-measured `/billing` with a **non-exempt** account and withdrew that half:
the dashboard renders **all four** plans, Own hardware at **$10 a month**
with its own description and a Choose button. My reading — and theirs — was
the `exempt` branch, which is the only account either of us had.

Provenance, which has to travel with that: the render was the real dashboard
build against the **fake** api. On production only `GET /v1/billing`
returning `own` with `available: true` is established, and **nobody has seen
a live non-exempt account**. The plan itself is live in the payment
catalogue at **$10.00 a month with no overage charge**, verified against the
live catalogue at 22:08 UTC by another session.

So what survives is the **docs alone**, and it is the two un-anchored links
above. **Rank: P2**, down from the P1 I first filed. **Card ENG-47**,
re-ranked and corrected on the board, and matching the conferred report.

### A3. What I did not file

v0.1.51 was tagged at 21:27:10 UTC, and its `--disk` help reads *"default
100, at least 40, bounded by the volume's free space less 20"* — the
ENG-41 fix, which needs about 60 GB free. At **21:29:15** the live
`/docs/own-hardware.md` still read *"About 10 GB of free disk to begin
with"*, so the requirements page was wrong by a factor of six against the
CLI a customer would install that minute. At **21:31:29** the same URL read:

> About 60 GB of free disk to begin with. The appliance's disk is a 40 GB
> file at least (100 GB when the volume has the room) that takes space only
> as your machines fill it, and 20 GB of the volume stays free for you; with
> less, `repose host add` says how much to free.

The docs trailed the tag by between 2 and 4 minutes. Not filed as a defect:
it closed on its own, and it is the same seam as ENG-43, in the opposite
direction and far inside that card's measured 20-minute window. Recorded on
ENG-43 as a second measurement.

### A4. What the Own hardware page gets right

Said plainly because a review that only lists faults mis-ranks them. This
page is the best writing on the site and it does the hard thing: it tells a
buyer the bad news before they buy.

- **"Starting a machine takes about four minutes."** Its own heading. Then:
  *"On repose's own servers the same machine starts in about 12 seconds."*
  A page that puts its feature's worst number in a heading, next to the
  competitor it loses to, has earned the rest of its claims.
- **"Snapshots stay on its disk."** *"If the disk dies, every snapshot of
  every machine on it dies with it... Back the disk up yourself."* Then it
  says why, which is a credential repose will not hold.
- **"Nothing of yours is touched: no disk is wiped, no system setting is
  changed, nothing needs your password, and your Mac stays a Mac."** This is
  the sentence that decides it for a Mac mini owner, and it is in the
  requirements list where they will read it.

---

## B. Billing, and what I could not do

### B1. I could not walk the checkout, not even to its edge

**Declared, not worked around.** The brief asked me to walk the checkout to
the edge without completing it. I cannot reach the edge. The owner's account
is billing-exempt, so `/billing` renders *"This account is billing-exempt."*
and no plan picker at all — no Own hardware card, no price, no button. The
whole page is the exemption notice and three usage meters.

So **nobody has yet seen the Own hardware plan on a live non-exempt
account**: not its card, not its price, not its checkout, not the Billing
page of an account on it. The command-line session has since rendered all
four plans — Own hardware at $10 with a Choose button — against the **fake**
api, and the plan is live in the payment catalogue at **$10.00 a month with
no overage charge**. Neither is the same as a customer's own screen. That is a gap in this review, and it is the gap that matters
most, because [B2](#b2-the-own-plan-is-zero-memory-zero-disk-zero-egress) is
about what that page would say.

Measured anyway: `/billing` renders in 0.97–1.13 s signed in, at both widths
and both schemes.

### B2. The own plan is zero memory, zero disk, zero egress

ENG-34 (dashboard session) ends with *"Unverified and worth checking: the
own plan carries memory_gb 0 / disk_gb 0, so a bring-your-own-hardware
customer measured the same way would read 'over' and see that banner
permanently."* That is my surface, so I checked the half that can be
checked.

**Reproduction.** `GET https://api.repose.run/v1/billing`, production,
21:28 UTC, with the dashboard's own access token:

```
{id: solo, name: Solo,          memory_gb: 8,  disk_gb: 100, egress_gb: 250,  hosts: 0}
{id: plus, name: Plus,          memory_gb: 16, disk_gb: 250, egress_gb: 500,  hosts: 0}
{id: pro,  name: Pro,           memory_gb: 32, disk_gb: 500, egress_gb: 1000, hosts: 0}
{id: own,  name: Own hardware,  memory_gb: 0,  disk_gb: 0,   egress_gb: 0,    hosts: 1}
```

The zeroes are real. The conductor has since reported that the dashboard
handles them deliberately — `noFleetLimits` in `billing/+page.svelte`, and
`diskOver` requiring a non-zero limit — so the memory and disk halves do not
reach a customer's screen. I have not seen that page myself and say so.

**What remains, and is unreproduced.** `egress_gb: 0` against the published
rule in `/docs/billing`:

> Past the allowance, $0.05 per GB is added to your next invoice. ... At
> four times the allowance ... your machines stop until the period ends.

Read against zero, that is: every gigabyte is an overage at $0.05, and four
times zero is zero, so the machines stop. Machines on a customer's own Mac
egress over the customer's own internet, so repose has no cost to recover
here and the metering most likely should not apply at all — but **no account
has been put on the own plan, so nobody knows.** Anything that charges a
customer wrongly is P0 whatever its size, which is why this is kept rather
than dropped.

**The check to run**, which needs one test account and ten minutes: put it
on the own plan, add a host, move a few GB, read the Billing page and the
invoice.

**Rank: P1, unreproduced.** **Card ENG-55**, parked and marked.

### B3. The one way out is named on one page and documented nowhere

When an owned host is on the account, every new project goes to it — which
is what makes [E3](#e3-a-dead-install-is-indistinguishable-from-a-slow-one-for-ever)
and ENG-28 as bad as they are. The docs name exactly one escape, in one
sentence, on `/docs/own-hardware`:

> "A project made through the [API] can ask for repose's servers instead,
> with `placement: "hosted"`, if your plan buys memory there."

Measured across the 26 live docs pages: **`placement` appears in
`own-hardware.md` and nowhere else**, `api.md` included. And `/docs/api`
does not merely omit it — line 114 says:

> "Request and answer bodies are not listed here. **They are what the CLI
> sends and reads**: `--json` on the matching command prints the answer, and
> `-v` prints each request it makes."

The CLI never sends `placement`; `repose run --help` has no such flag. So
the API reference's own rule tells a reader the field does not exist, and
there is no way to learn its shape from the documentation.

A customer whose install died, following the only pointer they were given,
reaches an API page that documents no bodies and a CLI that never sends this
one.

**Independently confirmed by the command-line session**, which measured the
page as served rather than as markdown: `/docs/api.md` answers 200 with
11,553 bytes and contains the word `placement` **zero times**. Two different
instruments, same answer — which is the one kind of agreement worth having.

**Rank: P2** on its own. **Card ENG-64.** In the conferred report this is
part of P0 #1: a paid account with no working machine by any route the
product offers.

---

## C. Minting the credential

I minted with the CLI rather than the dashboard, on the brief's instruction
(ENG-29: the dialog showed the 43-character token rather than the install
argument).

**Stopwatch.** The whole CLI path is fast enough to disappear:

| Step | Time |
|---|---|
| `curl -fsSL https://repose.run/install.sh \| sh` | **1.6 s** |
| `repose host add mac-council` | **61 ms**, exit 0 |
| `repose host ls` | **49 ms** |

The installer prints four lines and all four are useful. When
`~/.bashrc` was not writable it said so, named the consequence, and gave
both the home-manager setting and the `export` line. That is a good error.

### C1. The command printed the argument and called it the step — fixed under me

**Kept because it is the clearest example in this review of why a finding
needs its version**, not because it is open. It is closed.

On **v0.1.50**, 21:25 UTC, `repose host add mac-council` printed on stdout
one line of **339 characters** beginning `eyJ2Ijox`, which did **not**
contain the string `repose host add --join`. stderr was:

> `mac-council: this is its join token. It works once, before Fri 20 Nov 21:25. What to do with it: https://repose.run/docs/own-hardware`

So the whole output was a blob and a link to go and read, while the docs and
the command's own `--help` both promise *"the one step to run on the machine
being added"*. I filed it as P1.

**v0.1.51 was tagged at 21:27:10 — two minutes after I measured it.**
Re-measured at 21:46:33 on the published v0.1.51 binary, same account, same
command shape:

| | v0.1.50 | v0.1.51 |
|---|---|---|
| stdout length | 339 | 363 |
| stdout begins | `eyJ2IjoxLCJ0b2tlbiI6IlpB` | `repose host add --join e` |
| contains `repose host add --join` | **no** | **yes** |
| stderr | "this is its join token… What to do with it: `<docs link>`" | "run this on the machine you are adding. It works once, before …" |

It is the runnable step, it pipes, and the instruction a customer needed is
now the first clause of the message.

**And then the correction, which is the whole point of keeping this
section.** v0.1.51 did **not** fix it. At about 21:40 the release conductor
recorded `host_install_command` on the live api as `repose host add --join
%s`, and `repose host add` prints `install.command` **verbatim** whenever
the api returns one — 19c's rule, deliberately, so the CLI can never compose
an install line that differs from the operator's. That rule was in v0.1.50.
Before 21:40 the setting was empty, which is production's default, so the
CLI fell through to printing the bare argument. **v0.1.50 would print the
correct line today.**

Two things changed between my two measurements and I credited the one I
could see. So the rule from the method section goes one layer deeper:

> A finding carries **the version it was measured on** *and* **the state of
> the deployment it was measured against.**

I could not have seen that setting from where I stood. I could have noticed
I had no way to rule it out, and said so instead of concluding.

**Card ENG-49, closed** — no work owed, but resolved by a recorded setting
rather than a release, so nobody should hunt for a CLI change in v0.1.51's
notes.

One thing survives and gets better for the correction: the line no longer
carries the `/docs/own-hardware` link. If that is because the operator's
template is `repose host add --join %s` and nothing more, then the missing
link is **a one-line settings fix rather than a code regression** — and it
is currently the only thing standing between ENG-39 (this output names no
plan, price or trial) and a customer having anywhere to go. See also ENG-8,
which says this value should be a setting rather than a constant.

### C2. What is in the argument, and the safety note

Decoded to its shape only, never its values:

```
{v: int, token: str len=43, wg: {private_key: str len=44,
 address: str len=13, edge_pubkey: str len=44, edge_endpoint: str len=19}}
```

The 44-character WireGuard private key is confirmed present, which is
ENG-42's claim. **No raw value from this argument appears in this report, in
any card, in any log or in any message I sent.** The file holding it is mode
0600 in the session scratchpad and dies with the session. Every capture in
this review passed through a redactor before it was read.

### C3. `host ls` and its own `--json` disagree about the state name

A second instance of ENG-37, not a new card. Re-confirmed on **v0.1.51** at
21:47–21:48, so it needs no version caveat. In the same second, for the
same host: `repose host ls` prints STATE `waiting`; `repose host ls --json`
prints `"state": "registering"`. `/docs/own-hardware` defines the column as
`waiting`, `online`, `offline`, `removing`. The word `registering` is
defined in no documentation, and `--json` is the documented scripting
surface. Noted on ENG-37.

Also reproduced on that card's main claim, on v0.1.51: `repose host ls`
prints *"You have no hardware of your own."* while `repose host ls --json`
returns **eight** hosts, every one `retired`.

---

## D. The dashboard, from a Mac owner's seat

The dashboard is `19-council-dash`'s surface. These two are here because
they are on the Own hardware path and I hit them while walking it.

### D1. The refusal renders behind the dialog that caused it

**Reproduction**, 21:34 UTC, 1440×900: with one host on the account
(`mac-council`, waiting), **Settings → Hosts → Add a host**, Name
`second-host`, **Add**.

`POST https://api.repose.run/v1/hosts` returns **409**. The dialog stays
open, the field still filled, the Add button still live. The only
explanation — *"your plan includes 1 host, remove one before you add
another"* — renders as a toast in the **bottom-right corner, behind the
modal's dim overlay**, greyed out, while the customer is looking at the
middle of the screen.

No second host was created; the refusal is correct. It is the delivery that
fails. A Mac mini buyer who has already added a host hits this on their
first wrong press, and what they learn is that the button does nothing.

This is ENG-25, filed by the dashboard session. Noted there with this
reproduction rather than filed again, because it is the same defect on the
launch feature's own dialog. **Rank: P1.**

(The dialog also offers **Add a host** with no hint that the plan takes one
and one is already there. That is the same finding from the other end.)

### D2. Troubleshooting does not know this feature exists

`/docs/troubleshooting` has nine sections — Logging in and connecting,
Machine state, Sync, On the machine, Agents, Ports, Notifications, Updating
the CLI, Something else — and **zero** occurrences of "own hardware",
"appliance", "host add", "launchd" or "vfkit".

Own hardware is the launch feature and the only one running on a machine the
customer owns, where a disk, a network, a launchd agent and a 2 GB download
can each fail. `/docs/own-hardware` describes the happy path and names
`~/Library/Logs/repose`; no page tells a customer what to do when the host
never leaves `waiting`. ENG-41 is proof that these installs do fail in the
field.

**Rank: P2**, climbing to P1 if the second pass finds an install failure
that exits with no message. **Card ENG-57.**

---

## E. The Mac

The Mac is an AWS `mac-m4.metal`: **macOS 15.8.2 (24H232), Apple M4, 24 GB,
10 cores, 150 GiB APFS with 88 GiB free**, handed over clean at 21:35 UTC —
no `repose` on PATH, no appliance directory, no launchd label.

**One way it is not a retail Mac mini, declared because it touches the
installer:** its `~/.zshrc` is owned by `root:wheel`. So the installer took
its unwritable-rc branch, which a retail Mac would not. Everything else
about it — chip, memory, macOS, free space — is a Mac mini owner's machine
or better.

**Stopwatch, the Mac half.**

| Step | Time |
|---|---|
| `curl -fsSL https://repose.run/install.sh \| sh` on macOS | **2.1 s**, served `repose_v0.1.51_darwin_arm64.tar.gz` |
| `repose host add --join <argument>` | **failed**, exit 1, at the image fetch |
| `mkfile 34g ~/ballast` (my own setup, not the product) | 81 s |

### E1 (P0). The appliance image has never been published

**What I expected.** The install the docs describe: *"it fetches vfkit and
repose's virtual machine image (about 2 GB), puts them under ~/Library/
Application Support/repose, and starts the machine from a launchd agent."*

**What happened**, 21:38:44 UTC, published CLI v0.1.51, join argument minted
by `repose host add` on another machine 13 minutes earlier:

```
$ repose host add --join <argument>
fetching vfkit 0.6.4
fetching the appliance image 2026.11.19 (about 2 GB)
the image's checksum: https://github.com/Heracraft/repose/releases/download/appliance-2026.11.19/repose-appliance-aarch64.raw.gz.sha256: the server answered 404
$ echo $?
1
```

stdout was empty; those three lines are the whole of stderr.

**Verified three ways**, because a 404 can be a mirror hiccup and this one
is not:

- `curl -sIL` on that exact URL → `HTTP/2 404`
- `gh release list --repo heracraft/repose --limit 30` → no release tagged
  `appliance-*`
- `git ls-remote --tags origin` → no appliance tag at all

And the artefact is not merely mis-tagged: `find / -xdev -name
"repose-appliance*"` on the Mac returns nothing, and a size sweep for any
file between 1800 MB and 2400 MB returns nothing. The only image on the
machine is the builder's `lab6.raw.gz` (6,720,794,082 bytes), which dials a
development api and must never be published.

**So no customer on any Mac can complete `repose host add` today.** Not an
aarch64 gap, not transient: the release the shipped CLI names does not
exist.

**Two things to fix, not one.** The missing release is the cause. The
*message* is the symptom and outlives it: a bare `404` against a
`github.com` URL reads to a Mac mini owner as something **they** did wrong,
and the line names nothing to do next — no "this is ours", no support
address, no retry, no suggestion to try again later.

**A third consequence, for the gate:** the v0.1.51 disk-sizing refusal —
ENG-41's fix, the thing this review was explicitly asked to capture — is
**unreachable**, because the sizing happens after the image is unpacked and
nothing is unpacked. I had the volume filled to 54 GiB free specifically to
trigger it. That rule has never been exercised by a customer path and is
still unverified by anyone.

**Card ENG-58. P0, and in the conferred report (`1fd3b535`) it is ranked
P0 zero — ahead of the other two**, on the argument that both of those
describe a customer who has a working install and then cannot steer or is
measured wrongly, while this one means they never reach that state.
Independent in both directions: publishing the image fixes nothing about
placement or billing, and fixing those publishes no image.

**The cause outlives the fix, and is the thing to carry out of tonight.**
This P0 closes by a publish rather than a code change — the image was
rebuilt from main's head and the owner has approved releasing it. What does
not close is **ENG-62**: a CLI release went out naming an artefact that was
never published, and nothing in the release machinery checks that a
release's assets exist. That can happen again on any Monday, and no amount
of individual care prevents it.

### E2. A refused run leaves a project in `error`, and prints advice that cannot work

With the host sitting in `waiting`, 21:40:55 UTC, in a fresh git checkout:

```
$ repose run --no-attach
Creating proj-test...
Could not start proj-test: your host mac-council has not registered yet (capacity).
$ echo $?
8
```

1.27 s. The refusal is correct and ENG-28 already covers the blocking. What
it does not cover is what the refusal **leaves**:

```
$ repose ls
proj-test         large  error           -      -
proj-test: your host mac-council has not registered yet. `repose start proj-test` restarts it.
```

A run that said it could not start **did create a project**. It sits in
`error` and counts against the 100 an account may hold.

Then I followed the CLI's own advice, 21:41:18:

```
$ repose start proj-test
Creating proj-test...
Could not start proj-test: your host mac-council has not registered yet (capacity).
$ echo $?
8
```

0.65 s, and note the first word: **`start` prints "Creating"**. The advice
cannot work — the host cannot register while the appliance release is
absent — so "has not registered **yet**" and "**restarts** it" both promise
an arrival that will not come, and a customer can repeat this until they
hold a hundred projects in `error`.

Three lines to fix: the project left behind, the word *Creating* in `start`,
and the two words that promise it will come good. **Card ENG-59. P1.**

### E3. A dead install is indistinguishable from a slow one, for ever

`mac-council` was created at 21:25, its install died at 21:38:44, and at
21:42 **both** surfaces read exactly what they read one second after it was
minted:

```
HOST         STATE    MEMORY  MACHINES  RUNNING  LAST SEEN
mac-council  waiting  -       0         0        -
```

and the dashboard's Settings → Hosts: `mac-council  waiting  —  None  —`.

`/docs/own-hardware` defines `waiting` as *"it has not joined yet"*, which
is indistinguishable from *"it is still downloading 2 GB"*. There is no
timeout, no error state, no last-attempt time and no message anywhere.

The customer's experience is not an error. It is **silence**: they wait,
then they wonder, then they email support. And it outlives ENG-58, because
it is true whatever killed the install — a 404, a bad checksum, a full disk,
a closed laptop, a terminal shut mid-download. The install runs on a machine
the control plane has never heard from, so the only end that can report
anything is the one that cannot see what happened.

Pair it with ENG-28: while that host waits, **every** new project on the
account is refused, hosted or owned. I hit that as a teammate problem, not
a hypothetical — two other reviewers share this account, and my dead host
was blocking both of them until I removed it at 21:43.

**Card ENG-60. P1.**

### E4. The refusals are good, and one of them is a character class

Every `repose host` refusal I could reach, with its exit code. All of them
validate **before** minting anything — `repose host ls` was still empty
afterwards:

| Command | Answer | Exit |
|---|---|---|
| `repose host add MacMini` | `NAME must match [a-z0-9][a-z0-9-]{0,31}, got "MacMini"` | 2 |
| `repose host add "my mac"` | same | 2 |
| `repose host rm nonexistent-host --yes` | `You have no host named nonexistent-host.` | 1 |
| `repose host rm --yes` | `rm takes one NAME, or --local` | 2 |
| `repose host run` (on Linux) | `The appliance runs on a Mac with Apple silicon; this is linux/amd64.` | 1 |
| `repose host add --join notvalidbase64` (on Linux) | same | 1 |
| `repose host add --disk` | `flag needs an argument: --disk` + `` `repose host add --help` shows its usage.`` | 2 |

These are the good kind: short, exact, and they name the next move. The one
wart is the first row — the first name a Mac mini owner types is `MacMini`,
and the answer is a character class, when `/docs/own-hardware` already has
the sentence in words: *"A name is 1 to 32 characters of a-z, 0-9 and -."*
**Card ENG-61, parked at P2** so a second pass does not rediscover it.

**And one thing designed right, which deserves saying.** After the install
failed, `~/Library/Application Support/repose` held 63 MB: `bin/vfkit`
fetched, `share/` empty, **no launchd agent, no plist, no partial image, no
half-made disk**. A retry reuses vfkit and starts clean. A failed install
that leaves a clean retry is not an accident — someone designed that.
`~/Library/Logs/repose` survived the previous `repose host rm --local` with
its logs, which also looks deliberate.

### E5. What this review could not reach

Declared rather than retried into silence, because a gap named is worth more
than a gap hidden:

| Asked for | Status |
|---|---|
| The host appearing, `online` | **not reached** — ENG-58 |
| `repose run` on owned hardware, first machine timed against 289 s | **not reached** |
| How a second machine feels | **not reached** |
| The phone ask | **not reached** |
| Laptop closed and reopened | **not reached** |
| A Mac reboot, and a parked guest against 3.8 s | **not reached** |
| `repose host rm` on the Mac that runs the appliance | **not reached** (`host rm` from elsewhere: exercised, 21:43) |
| The ENG-41 disk refusal, verbatim | **not reached** — unpacking happens after the fetch |
| The checkout, even to its edge | **not reachable on this account** — see [B1](#b1-i-could-not-walk-the-checkout-not-even-to-its-edge) |

That is most of the feature. **Everything downstream of a 2 GB download is
unreviewed**, and the second pass must cover it before the gate opens.

I was offered the builder's lab image to get past it and **declined**: every
number after it would measure something no customer can get, which is
exactly what the P0 says is wrong. The conductor is pursuing a published
release; if one lands I will install from it like a customer, and the whole
sequence including the download becomes real.

## F. The install, once the image was published

The appliance release `appliance-2026.11.19` was published at **22:28:08
UTC**, forty-nine minutes after my P0. I verified it the way a customer's
CLI does, anonymously, before trusting it: `isDraft: false`, tag
`appliance-2026.11.19` on the remote at `a3ecdc45`, both assets HTTP 200,
image `content-length 2081526806`, sha256
`fce986ce…a0ed567b`.

**A note on the one guard I was given.** I was told to stop if the published
image was not **2,081,798,905** bytes. It was **2,081,526,806** — 272,099
bytes smaller. I stopped and asked rather than assuming a rebuild explained
it. It did: the guard figure was an earlier build from a branch tree, and
this one is `main` at `a3ecdc45`. The spark confirmed the sha256 matches in
three places and read the identity out of the mounted image — the hostd unit
runs `--api-addr 10.255.255.1:8443 --api-server-name api.repose.herakraft.co`
with the production CA, and the lab twin's `192.168.64.1` appears nowhere.
Only then did I let an appliance register.

### F1 (P0 candidate). The disk guard refuses once, then lets you past

**This is ENG-41's fix defeated by the most likely thing a customer does
after a refusal: running the command again.**

Volume at **46.67 GiB / 50.11 GB free**, unchanged throughout.

**22:32:45** — run the printed install line:

```
fetching the appliance image 2026.11.19 (about 2 GB)
unpacking the image
this volume has 46 GB free; the appliance needs a 40 GB disk and leaves 20 GB free, so free up 14 GB first
exit 2
```

A good refusal: it names the shortfall, the arithmetic is right
(40 + 20 − 46 = 14), and it tells the customer exactly what to do. **This is
the sentence the release conductor asked for and it is correct.**

**22:34** — run the *same* line again, having freed nothing, `df` identical:

```
launchd took no agent for this session; the appliance starts detached and the agent takes over at the next login
exit 0
```

in **under a second**, and the appliance installs and starts.

**Why.** The unpack writes straight into `disk.raw`, and the refusal **leaves
that file behind**: 39,728,447,488 bytes, which is exactly **37.00 GiB** —
the image's own size. The retry finds it present, skips both the download
and the sizing rule, writes the config and the launchd plist, and launches
vfkit.

So the installed disk is sized to **the image**, not to the 40 GB floor and
not to free-space-less-20, on a volume the product had just said could not
hold it. If that guest fills its disk, the Mac's boot volume reaches 100%,
which is the outcome the guard exists to prevent.

**Two defects in one sequence**: the refusal is not atomic — it leaves the
artefact it refused to create — and the guard is not re-evaluated on the path
that finds that artefact. **Card ENG-71**, observed once, with a clean
reproduction owed before the rank is confirmed.

### F2. What the install got right

Measured from the running process and the api, not from the page:

| `/docs/own-hardware` promises | What I measured |
|---|---|
| "three quarters of the Mac's memory … 18 GB of 24" | `vfkit --memory 18432`; `host ls` prints **18 GB**; api `mem_bytes 18796412928` |
| "all but two cores" | `vfkit --cpus 8` on a 10-core M4; api `vcpus 8` |
| "`repose host ls` shows the share" | it does |
| "While the machine runs, the Mac does not sleep" | the appliance runs under `caffeinate -s -i` |
| "a launchd agent that starts it again at every login" | `~/Library/LaunchAgents/run.repose.appliance.plist` written |
| "Nothing needs your password" | nothing did |

**The host registered 61 seconds after `repose host add` was run**, install
and 2 GB download included — exactly, from the api's own record:
`created_at 23:00:52.612`, `registered_at 23:01:53.025`. (The first
install, measured more loosely from file stamps, agreed at about 60 s.)

**And one good message I did not expect.** Installing over SSH with no GUI
session, the CLI said: *"launchd took no agent for this session; the
appliance starts detached and the agent takes over at the next login."* It
noticed the situation, did the right thing anyway, and said so in one line.

### F3. A third text-versus-JSON state split

Same family as ENG-37, now across every state I have seen:

| `repose host ls` | `repose host ls --json` |
|---|---|
| `waiting` | `registering` |
| `online` | `ready` |

`/docs/own-hardware` defines the column as `waiting`, `online`, `offline`,
`removing`. Neither `registering` nor `ready` is defined anywhere, and
`--json` is the documented scripting surface.

### F4. The first machine on owned hardware

The headline number nobody had. `repose run --no-attach --size small`, in a
fresh one-commit checkout, against a host that had been `online` for about
a minute.

**Boot alone: 7 min 05 s.** The CLI counted up in 30-second steps the whole
way — `Booting firstmachine… 3m00s`, `… 4m00s`, `… 7m00s`, `✓ Booted
firstmachine 7m05s` — which is the right way to spend seven minutes of a
customer's attention: never silent, never a spinner with no number.

Against the comparisons I was given: `/docs/own-hardware` promises **"about
four minutes"**, and the phases measured on the program branch totalled
**289 s**. The published build took **425 s to boot** — **77% longer than
the page promises.**

### F5. 3,664 lines of nix, and 42 lines for the customer

The first `repose run` on owned hardware wrote **3,706 lines to stderr**, of
which **3,664 — 98.9% — begin `nix ›`**: 655 `copying`, 322 `building`, and
the rest per-package build logs (176 `udev-rules`, 153 `jwcrypto`, 130
`websockify`, 106 `linux`, 84 `fc-cache`, 83 `pi-coding-agent`).

The summary the customer is meant to read is **42 lines**, and it is good:

```
✓ Created clean1 (small)  0.5s
✓ Fetched the base  4.0s
✓ Evaluated your config  15s
✓ Fetched 655 paths (1.7 GiB)  60s
✓ Built 323 derivations  57s
✓ Booted firstmachine  7m05s
Ready in 12m15s.
```

It is buried under the other 3,664. ENG-27 records this at **70 lines** on
the hosted fleet; on owned hardware it is **fifty times worse**, because an
owned host has no warm store, so a first run fetches 1.7 GiB and builds 323
derivations on the Mac itself and every line reaches the terminal. Own
hardware is the launch feature, so this is the shape most new customers will
meet. Noted on **ENG-27** rather than filed again.

### F6. The appliance boots with a failed unit and the account is never told

The appliance installed by the [F1](#f1-p0-candidate-the-disk-guard-refuses-once-then-lets-you-past)
retry path booted with exactly one failure, in its own console log at the
path `/docs/own-hardware` names:

```
         Starting Grow the guest partition,…ool to the disk at boot (I-1376)...
[FAILED] Failed to start Grow the guest par… pool to the disk at boot (I-1376).
See 'systemctl status repose-mac-grow.service' for details.
```

Everything else reached `OK`, the appliance registered, and from the
account's side it looked perfect: `repose host ls` said `online  18 GB  …
just now`, and so did the dashboard. **Nothing anywhere told the customer a
unit had failed.**

And the one diagnostic offered — `systemctl status repose-mac-grow.service`
— runs **inside a virtual machine the customer has no shell on and no
documented way to reach**. It is advice that cannot be followed by the
person who owns the Mac.

**The cause is F1**, established by controlled comparison on the same Mac
with the same CLI and the same image, differing only in the disk:

| install | `disk.raw` | the grow unit |
|---|---|---|
| retry path, 22:34 | 39,728,447,488 B = **37.00 GiB** (= image size) | `[FAILED]` |
| clean, 23:00:53 | 55,834,574,848 B = **52.00 GiB** | `[ OK ] Finished`, **no FAILED line in the boot** |

The image is 37 GiB with "root partition to 32 GiB, pool partition after"
(ENG-41's own words), so a disk file of exactly image size leaves nothing
after the pool for a boot-time grow to claim.

Two things still want fixing whatever happens to F1: **a failed unit on an
owned host should reach the account**, not only a local log file; and a
message should not tell a customer to run a command they cannot run.
**Card ENG-79, P1.**

### F7. Both machines on the broken appliance died

On the appliance of [F6](#f6-the-appliance-boots-with-a-failed-unit-and-the-account-is-never-told).

**Machine 1 worked, then stopped.** `repose run --no-attach --size small`
completed: `Ready in 12m15s`, `Checkout: ~/firstmachine on the machine`,
`Logins copied: gh, git`. Then from 22:50 to 22:57 — **eighteen SSH attempts
over 355 s, plus three more after** — every one answered:

```
environment is not accepting connections yet
```

Throughout, `repose ls` said `running` and `repose status` said `running
12m` with `checkout unknown (no ssh answer in 4 s)`. And the customer's one
diagnostic is closed to them:

```
$ repose logs --kind console
firstmachine has no console output: repose keeps a machine's console only from a boot that failed.
```

**So for a machine that booted and then died, there is nothing to read.**

**Machine 2 never started.** `Creating secondmachine… 5m00s`, then exit 1:
*"the machine did not answer within a minute of starting … (guest_unresponsive)"*.
Its console does exist, and shows a **healthy** boot — modules loaded, `fsck`
clean, overlay mounted, `0 whiteouts removed`, systemd starting — with the
first systemd line stamped **`[ 196.433235]`**. The guest was alive and 196
seconds into its own boot when the control plane gave up after *a minute*.

**Ruled out by measurement, not assumed:** memory. `/docs/machine.md` gives
`small` = 4 GB, so two of them are 8 GB of an 18 GB appliance, and the Mac's
load average was 3.72 across 10 cores. I had assumed OOM and was wrong.

**And then the clean appliance failed too, which is the decisive result and
goes against my own hypothesis.**

I expected a healthy appliance to fix this. At 23:02:20, on the appliance
installed cleanly at 23:00:53 — **no FAILED line in its boot, grow unit
finished `OK`, `disk.raw` 52.00 GiB, host `online 18 GB`** — a first
`repose run --no-attach --size small` in a one-file checkout ran **567 s
(9m27s)** and exited 1:

```
Could not start clean1: the machine did not answer within a minute of starting … (guest_unresponsive)
```

Every phase before the failure was normal and fast:

```
✓ Created clean1 (small)  0.5s
✓ Fetched the base  4.0s
✓ Evaluated your config  15s
✓ Fetched 655 paths (1.7 GiB)  60s
✓ Built 323 derivations  57s
Booting clean1... 30s … 6m30s
```

So **ENG-80 is not caused by ENG-71.** A guest fails to come up on a healthy
appliance as readily as on a broken one, and the two differ in the only way
that mattered for [F6](#f6-the-appliance-boots-with-a-failed-unit-and-the-account-is-never-told).
I had a tidy causal chain and the control killed it.

**The one success, kept because it bounds the claim**: the very first
machine, on the *broken* appliance, booted in **7m05s** and synced. So the
failure is **not deterministic**, and 6m30s against 7m05s suggests the guest
is close to whatever deadline is applied rather than dead — the
`[196.433235]` systemd stamp from
[the second machine](#f7-both-machines-on-the-broken-appliance-died) says
the same thing.

**Raised from candidate to confirmed P0: machines do not reliably start on
owned hardware**, which is the whole feature. **Card ENG-80.**

**Then it got worse, and the shape of it only became clear from the event
log.**

A machine that was **working** — ssh in 11.5 s at 23:18, checkout present,
logins copied — was stopped normally at 23:25, exactly as the quickstart
instructs ("Stop the machine when you're done"). **It never came back.**

| # | What | Result |
|---|---|---|
| 1 | `firstmachine`, first run (broken appliance) | **ok** 7m05s, then unreachable |
| 2 | `secondmachine`, first run (broken appliance) | fail, `guest_unresponsive` |
| 3 | `clean1`, first run (clean appliance) | fail, 567 s |
| 4 | `clean1`, `repose start` | **ok** 311 s, genuinely usable |
| 5 | `clean1`, `start` after a clean `stop` | fail, 332 s |
| 6 | `clean1`, `start` again | fail, 307 s |
| 7 | `clean1`, `start` again | fail, 283 s |
| 8 | `clean1`, `snapshots restore` → start | fail, 253 s |

**Two successes in eight guest starts.** Sharper and more testable than the
raw tally: **after a clean `repose stop`, four attempts have failed and none
has succeeded**, while both successes were the first start of a fresh
machine.

**The event log shows every failure is the same step.** For the restore:

```
23:47:32  machine  destroying
23:47:33  machine  restoring
23:47:33  machine  destroyed
23:47:35  machine  stopped
23:47:36  machine  starting
23:51:44  machine  error
```

The snapshot, the destroy and the disk restore all succeed **in three
seconds**. Only the start fails, four minutes later. The snapshot survives
intact and is still listed.

**So what a customer loses is not their data.** It is safe in a snapshot
that demonstrably restores. What they lose is the ability to run anything on
the hardware they bought — **and on the Own hardware plan there is nowhere
else to run it**: the plan buys no fleet memory
([B2](#b2-the-own-plan-is-zero-memory-zero-disk-zero-egress): `memory_gb 0`)
and the CLI offers no placement flag
([B3](#b3-the-one-way-out-is-named-on-one-page-and-documented-nowhere)). A
Mac that will not start guests leaves the account with **no working machine
anywhere**.

**Two lines a customer reads at that moment, both wrong** (card ENG-100, P1):

- `repose run` counted **"Booting clean1… 6m30s"** and then refused with
  *"did not answer **within a minute** of starting"*. Whichever number is
  true, the other is on screen.
- The same message says *"`repose logs --kind console` shows what it
  printed"*. Running exactly that returns **one line** — the error restated
  — and none of the machine's console. For an earlier failed machine the
  same command returned a full NixOS stage 1 and 2 boot log, so the path is
  **unreliable rather than absent**, and a customer cannot tell which case
  they are in.

### F8. The CLI counts in GiB and says GB

Two consistent measurements:

- `df` reporting **46.67 GiB = 50.11 GB** free → the CLI printed *"this
  volume has **46 GB** free"*. It said the GiB figure.
- On a volume with **72.65 GiB** free after unpacking → `disk.raw` was
  **exactly 52.00 GiB**, which is `72.65 − 20` floored **in GiB**. In GB the
  same rule gives about 58 GiB.

So the rule is `free_GiB − 20 GiB`, floored, with a "40 GB" floor that is
really 40 GiB — every disk figure the CLI states is about **7% larger than
the unit it names**.

**What it costs a customer:** `/docs/own-hardware` says *"About 60 GB of free
disk to begin with"*. Someone who reads that, frees **62 GB**, and runs the
install has 57.7 GiB; `57.7 − 20 = 37.7`, below the 40 floor, so they are
**refused after downloading 2 GB**, having done exactly what the page asked.

Derived from two measurements rather than triggered deliberately, and the
card says so. **Card ENG-87, P2.**

### F9. Taking it off works, and quickly

The one part of the lifecycle that behaved exactly as documented:

| | |
|---|---|
| `repose host rm mac-council --yes` from the laptop | exit 0, **under a second**, silent; `host ls` then "You have no hardware of your own." |
| `repose host rm --local --yes` on the Mac | exit 0, **3 s** |
| after it | appliance directory **gone**, no `vfkit`, no launchd plist, volume back to **78.75 GiB** free |

`/docs/own-hardware` promises *"Remove the host and the virtual machine goes
with it"*, and it does, including the disk. No finding.

### F11. A build hash under my Mac's name, and no Remove button on a phone

Settings → Hosts, with a host **actually online** — a state no earlier review
could reach, because until 22:28 no host ever got past `waiting`.

The row renders the host's name and then, directly beneath it in grey
monospace:

```
mac-clean
/nix/store/b9khbvcckcvs6mmjrb77kp0yhznh41s1
```

A customer sees a 32-character build hash captioned under the name they
chose for their Mac mini.

**Two symptoms, one cause.** At 1440 it is a cosmetic leak of an internal
identifier. At 390 it is **functional**: the string does not wrap, so the row
is wider than the viewport — `scrollWidth` **441** against `clientWidth`
**390** — the page scrolls sideways, and the only control in the row,
`Remove…`, is pushed off the right edge and renders as a single letter
**`R`**.

So **on a phone a customer cannot take their own hardware off their
account** — and removing the host is the one action the product most needs
them to find: it is the documented escape from a stuck host
([B3](#b3-the-one-way-out-is-named-on-one-page-and-documented-nowhere),
ENG-28) and the fix for a failed install. The repo's own rule is no
horizontal page scroll at 390.

**And the cause is not the dashboard.** `repose host ls --json` for the same
host returns, verbatim:

```
arch: /nix/store/b9khbvcckcvs6mmjrb77kp0yhznh41s1
```

The field named `arch` carries a 32-character store hash rather than an
architecture. The dashboard renders faithfully what it is given, and **a
script reading `--json` for an aarch64 Mac gets a store path too**. So
wrapping the text in the dashboard alone would leave the api answering a
store path to every caller.

Filed apart so closing one does not lose the other: **ENG-88** (the 390
overflow and the unreachable Remove) and **ENG-89** (the field), both P1.

The rest of that record is right and worth saying: `mem_bytes
18796412928`, `vcpus 8`, `cores 8`, `state ready`, and `created_at
23:00:52.612` against `registered_at 23:01:53.025` — **the host registered
61 seconds after it was minted**, install included.

**One unit disagreement noticed in passing**, belonging with
[F8](#f8-the-cli-counts-in-gib-and-says-gb): for the same host, the CLI says
**18 GB** and the dashboard says **17.5 GB**. The true figure is
18,796,412,928 bytes — 17.5 GiB, 18.8 GB. Both are rounding the same number
in different units, and neither says which.

### F12. The questions view works, and I left it alone

Opening Settings showed a **Questions** badge of 1, and `repose questions`
listed a real question: an agent on the account's `job` project, asked 59
minutes earlier, expiring in 3h01m, with a four-part prompt and instructions
for replying.

That is the owner's own workflow, so I did not answer it. It is in this
report because it is **evidence the ask path works end to end in
production** — an agent asked, the question reached both the CLI and the
dashboard with its age and expiry, and the badge found me without being
looked for. I will exercise the path myself with my own question on the
clean machine, inside the **2-minute notification hold** that `repose
notify` reports, so that no real person is paged by a test.

### F13. The ask reaches me; the answer never reaches the agent

The product's pitch is that an agent asks you for a yes and carries on when
you answer. On a Mac mini the asking half fails while the owner is still
interrupted.

On a **working** machine — `guestd.service` active, api reporting
`guestd_ok: true` three seconds earlier — run as an agent would:

```
$ repose-ask --options yes,no --timeout 3m "Council test question, please ignore"
repose-ask: guestd is not answering on /run/repose/hooks.sock: Post "http://guestd/ask": context deadline exceeded
real 0m6.115s
```

**It asked for three minutes and gave up in six seconds.** Reproduced twice,
6.1 s both times.

**And the question still reaches the owner.** Both times it appeared in
`repose questions` within 12–20 s, complete:

```
clean1  shell asked just now, expires in 5m  (id 6a1821c5)
  Council test question, please ignore: is this reaching the owner?
  options: yes|no
```

and `repose reply --question <id> yes` answered it — `Answered shell in
clean1: yes`.

So the loop is **one-way**: the owner is interrupted, answers, and nothing
is waiting for the reply. An agent written the documented way gets an error
instead of an answer and carries on without one, while the owner's attention
has been spent on a question that can no longer change anything.

**Not established:** whether this is specific to owned hardware. I could not
run the hosted control, because every hosted project on this account is the
owner's live work — one of them had a real question pending while I tested.
If it is product-wide it is worse, not better. **Card ENG-105, P0.**

**Two things that worked, said because they did.** The notification **hold**
is real: `repose notify` reports `hold: 2m0s`, and by answering within 36
seconds I exercised the whole path without paging a human — which is also
how I tested this without spending the owner's attention. And `repose reply`
refused my first attempt correctly: I typed `repose reply <id> yes`, it
answered *"2 questions are waiting; say which with the project or --question
ID"* and listed them. The syntax is documented, the error named the fix, and
I had simply not read it. **Not a finding.**

### F14. Four documentation files left behind for having "credentials" in the name

On the sync:

```
Not carried (the names look like credentials): ~/.claude/skills/amazon-bedrock/references/agentcore-credentials-and-security.md, ~/.claude/skills/aws-cdk/references/troubleshooting-credentials.md, ~/.claude/skills/aws-sdk-js-v3-usage/references/credentials.md, ~/.claude/skills/aws-sdk-python-usage/references/credentials.md
```

All four are public AWS documentation *about* credentials. The filter
matches the filename.

**Parked at P2 rather than opened**, because it is wrong in the safe
direction and the message **names every file it skipped**, so a customer can
copy one by hand. Recorded so a second pass does not rediscover it.
**Card ENG-104.**

The same sync also printed `Not carried (would not work on the machine):
Claude hook "…"` with its reason, and `Installing 3 of your tools in the
background: cptr, golangci-lint, portless`. Those are good lines.

### F10. What is still unmeasured

Held open honestly rather than filled with impressions. These need a working
machine on the clean appliance, which is the run in progress as this section
is written:

| Asked for | Status |
|---|---|
| The phone ask | **done** — [F13](#f13-the-ask-reaches-me-the-answer-never-reaches-the-agent) |
| Laptop closed and reopened | **not yet run** |
| A Mac reboot, and the appliance back at its login against 21 s | **delegated, with cause** — see below |
| A parked guest restored, against **3.8 s** | **not yet run** |
| A guest's cold start against **289 s** on a healthy appliance | **running** |
| Whether [F7](#f7-both-machines-on-the-broken-appliance-died) survives a healthy appliance | **running** — decides ENG-80's rank |

**A limit on what my reboot can prove, found while answering another
session.** Every install here ends with:

> `launchd took no agent for this session; the appliance starts detached and the agent takes over at the next login`

and `launchctl list | grep repose` then returns **nothing**, though
`~/Library/LaunchAgents/run.repose.appliance.plist` is on disk. An ssh
session has no GUI login and so no `gui/$(id -u)` domain to bootstrap into.
The CLI handles this well — it notices, does the right thing anyway, and
says so in one line.

But it means **this review cannot verify `/docs/own-hardware`'s promise of
"a launchd agent that starts it again at every login"**, because the agent
never loads for me. A Mac mini owner installing from a Terminal window in
their own GUI session would get it; I never do. Anything I measure across a
restart is therefore a measurement of the **detached** appliance, not the
agent — which also bounds what a park-and-restart test run from here could
honestly claim, and I have said so to the session building that fix
(ENG-2).

**A correction I was given and am recording rather than quietly using.** I
was first told to measure a cold guest start against **57.9 s**. That figure
came from an experimental store-on-disk path which is a parameter defaulting
to `false`, which nothing in the repository sets to `true`, and which exists
only on branches — the published image is built from `main`, which does not
contain the mechanism at all. So **no product guest on any branch gets that
path today**, and reporting 289 s or 425 s against 58 s would have made my
measurement look like a failure of something that was never shipped. The
honest comparison is `/docs/own-hardware`'s own promise of **"about four
minutes"**, and the 289 s of measured phases.

---

## The four hats

Each written before they confer, as the brief asks.

### The daily driver — does it work, is it pleasant, what broke

**It does not work yet, and I want to be precise about what that means,
because "it does not work" is too blunt for what I saw.**

Everything around the machine is good. The CLI installs in 2 seconds and
prints four useful lines. `repose host add` registers a Mac in **61
seconds**, 2 GB download included. Removal takes **3 seconds** and leaves
nothing behind. Every refusal I provoked was correct, fast and exited with a
code I could script against. The appliance takes exactly the memory and
cores the page promises. When a machine does come up it is a real machine:
`aarch64`, 1 core, 3890 MB, a 20 G disk, the checkout in place, the commit
present, `gh` and `git` logged in.

**The machine itself is the part that does not work.** Five start attempts,
two successes. The failures take six to nine minutes before telling you, and
then the thing they tell you to run — `repose logs --kind console` — hands
back the error you already had. One machine said `Ready in 12m15s`, synced
my checkout, copied my logins, and then refused SSH for eight minutes while
the dashboard said `running`.

**What I would have tweeted** is not any of that. It is this: my agent asked
me a question, I answered it on my laptop in 36 seconds, and the agent never
heard me — it had given up six seconds after asking. I would have spent an
hour assuming I had misconfigured something.

**And the honest counterweight:** nothing was ever lost. Every failure left
the disk intact, `repose stop` took its snapshot, and the retry that worked
produced a machine with all my work on it. This is a product that is
unreliable, not one that is dangerous.

### The economist — is it worth $10

**At $10 a month, yes, and the arithmetic is not close.** Own hardware buys
the gateway, the control plane, triggers and notifications; the memory and
disk are mine. Against Solo at $39 for 8 GB, a 24 GB Mac mini I already own
pays for itself in the second month. If it worked, I would buy it.

**What the measurements do to that answer:** the page sells "about four
minutes" against 12 seconds hosted, and the number is really **5m11s at
best, 7m05s typical, and 17m 8s** for my first machine end to end with one
failure. Four minutes is a trade a developer makes happily. **Seventeen
minutes, twice, is a trade they stop making** — and the third time they go
back to the hosted plan, which is the one outcome that costs repose both the
$10 and the goodwill.

**What I could not cost**, and would not guess at: the `own` plan carries
`memory_gb 0, disk_gb 0, egress_gb 0`, and the published egress rule is
$0.05/GB past the allowance with a stop at four times it. Against zero that
reads as a charge on every gigabyte. The conductor reports the dashboard
handles the zeros deliberately. **Nobody has put an account on the plan to
see what the invoice does**, and until someone has, that is an open question
and not a finding.

**One real cost nobody is counting:** every first run on an owned host
fetches **1.7 GiB** and builds **323 derivations** on the customer's own
Mac, because an owned host has no warm store. That is the customer's
electricity, their thermal budget and their seven minutes — and it is a
recurring cost per project, not a one-off per Mac.

### The brand skeptic — does any line oversell, does it read like repose

**The writing is the best thing here and I want that on the record before
the complaints.** `/docs/own-hardware` puts its feature's worst number —
four minutes against twelve seconds — **in a heading**, next to the
competitor it loses to. It tells you snapshots die with your disk, and then
says why. It promises "nothing of yours is touched… your Mac stays a Mac"
in the requirements list, where the person deciding will actually read it.
That page is honest in a way most product pages are not.

**What oversells is a single number**: "about four minutes… the first time
and every time after." Three measured boots: 7m05s, 6m30s, 5m11s. None of
them four.

**What reads like it was written for someone else:**

- `NAME must match [a-z0-9][a-z0-9-]{0,31}` — a character class, shown to a
  Mac owner naming their Mac, when the docs have the sentence in words.
- `/nix/store/b9khbvcckcvs6mmjrb77kp0yhznh41s1`, captioned under the name
  they chose, on the dashboard.
- "did not answer **within a minute** of starting", after the screen has
  just counted to **6m30s**.
- "`repose start proj-test` restarts it" and "has not registered **yet**" —
  two promises the product cannot keep in the state that prints them.
- `the server answered 404` — accurate, and it tells a customer nothing
  about whose fault it is.

Each is one line. Each lands in the launch feature's first twenty minutes.

### The engineer — a stranger's host, idempotency, restart, failure

**The failure paths are better than the success paths, which is the right
way round and is not an accident.** A failed install leaves cached `vfkit`,
no launchd agent, no plist, no partial image and no half-made disk; a retry
starts clean. Name validation happens before anything is minted. `repose
stop` snapshots before stopping even when it cannot reach the guest. The
join argument is single-use, expires in 24 hours, and `host rm` + `host add`
invalidates it.

**The one place idempotency fails is the one that matters most**, and it is
ENG-71: the disk-sizing guard is evaluated on the download path and skipped
on the path that finds an already-unpacked `disk.raw`. A refusal that leaves
the artefact it refused to create is a refusal that teaches the next run to
ignore it. The result registers, reports healthy, and has a guest pool that
was never grown.

**What I could not test, and said so rather than guessing:** a stranger's
host joining, host-to-host isolation, sleep, Wi-Fi drop, a full disk, and a
real restart. The restart is **delegated with cause**: an ssh session has no
GUI login, so the launchd agent never loads here and the appliance runs
detached. Anything I measured across a reboot would have been the detached
appliance, not the agent — a result that would look like an answer and be
none.

**Two numbers an engineer should see.** The guest that was declared
`guest_unresponsive` after "a minute" shows its first systemd line at
`[196.433235]` — it was alive and 196 seconds into a healthy boot when the
control plane gave up. And `guestd` on an idle 1-vCPU guest had burned
**1 min 21 s of CPU in 9 minutes**.

**On the credential, including my own failure with it.** The join argument
decodes to `{v, token(43), wg:{private_key(44), …}}` — a live WireGuard
private key, confirming ENG-42. At 21:48 I printed one into my own
transcript by dropping my redactor to read an exit code; the host was
retired seconds later and the single-use token is spent. The argument it
supports is stronger than any design critique I could write: **I was
instructed in writing, I redacted every capture for ninety minutes, and I
leaked it on the first command where I wanted an exit code rather than a
value.** A customer has neither the instruction nor the habit. ENG-42's own
fix — take the argument on stdin or from a file — wants a second half: say,
at the moment of printing, that it is a credential.

## Signatures

One session writes the confer; this file is `19-council-mac`'s half, with
its findings already on the board as cards. The CLI and dashboard sessions
read and sign it, or record disagreement here.

- `19-council-mac` — written from the measurements above.
- `19-council-cli` — _unsigned_
- `19-council-dash` — _unsigned_
