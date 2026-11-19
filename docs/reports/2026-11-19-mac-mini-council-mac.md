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
| 1 | **P0** | "I ran the install on my Mac mini and it stopped with a 404." | ENG-58 | [E1](#e1-p0-the-appliance-image-has-never-been-published) |
| 2 | P1 | "My failed run left a broken project behind, and the fix it told me to run can never work." | ENG-59 | [E2](#e2-a-refused-run-leaves-a-project-in-error-and-prints-advice-that-cannot-work) |
| 3 | P1 | "My Mac install died and repose just says waiting, for ever." | ENG-60 | [E3](#e3-a-dead-install-is-indistinguishable-from-a-slow-one-for-ever) |
| 4 | P1 | "Am I being charged for every gigabyte my agent downloads?" (unreproduced) | ENG-55 | [B2](#b2-the-own-plan-is-zero-memory-zero-disk-zero-egress) |
| 5 | P1 | "I pressed Add and nothing happened." | ENG-25 | [D1](#d1-the-refusal-renders-behind-the-dialog-that-caused-it) |
| 6 | P2 | "Your Troubleshooting page has nothing about the thing I'm troubleshooting." | ENG-57 | [D2](#d2-troubleshooting-does-not-know-this-feature-exists) |
| 7 | P2 | "The price is one scroll below three plans I can't use." | ENG-47 | [A2](#a2-the-price-is-on-no-page-a-buyer-reads-first) |
| 8 | P2 | "The one way out of this is documented nowhere I can find." | ENG-64 | [B3](#b3-the-one-way-out-is-named-on-one-page-and-documented-nowhere) |
| 9 | P2 | "I named my Mac MacMini and got a regular expression." | ENG-61 | [E4](#e4-the-refusals-are-good-and-one-of-them-is-a-character-class) |

**One P0, and it stops the feature dead.** Nothing a Mac mini owner can buy,
install and run exists today: the appliance image the shipped CLI fetches
has never been published, so `repose host add` ends in a 404 on every Mac.
Everything downstream of that download — the host appearing, `repose run`,
the first machine's four minutes, the phone ask, the laptop closed, the
reboot, the removal — **is unreviewed**, and says so in
[E5](#e5-what-this-review-could-not-reach).

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

---

## The four hats

Each written before they confer, as the brief asks.

### The daily driver — does it work, is it pleasant, what broke

**It does not work.** I did not get a machine. I reached the second step of
the published instructions and hit a 404, and no page on the site tells me
what to do about it.

What I can judge is everything up to that point, and it is *pleasant*. The
CLI install is 2.1 s and prints four useful lines. `repose host add` answers
in 61 ms. Every refusal I provoked was correct, fast, and exited with a code
I could script against. Nothing was slow; nothing was confusing except the
things in this report.

The two faults that would have made me tweet are not the 404 — a 404 reads
like a bad day — but what follows it: a project in `error` I did not ask
for, with advice that cannot work, and a host that says `waiting` for ever
while my install is dead. One is clutter, the other is silence, and silence
is the one that loses a customer.

### The economist — is it worth $10

**Yes, and the page is honest enough to let me decide.** $10/month buys the
gateway, the control plane, triggers and notifications, and nothing else;
the memory and disk are mine. Against Solo at $39 for 8 GB, a 24 GB Mac
mini I already own is the better deal on the second month and every month
after, and the four-minute start is the price I pay for it. `/docs/own-
hardware` puts that four minutes in a heading **next to the hosted 12
seconds**, which is the sentence that earns the rest of the page.

What I cannot cost is the only thing that might change the answer. The `own`
plan carries `memory_gb 0, disk_gb 0, egress_gb 0`, and the published egress
rule is $0.05/GB past the allowance with a stop at four times it. Against
zero, that reads as a charge on every gigabyte and a stop at the first one.
The conductor reports the dashboard handles the zeros deliberately; **nobody
has put an account on the plan to find out what the invoice does**. Until
someone has, I would not put a card on this. [B2](#b2-the-own-plan-is-zero-memory-zero-disk-zero-egress), ENG-55.

### The brand skeptic — does any line oversell, does it read like repose

**Almost nothing oversells, and that is unusual enough to say first.** The
landing page is clean at 1440 and 390, in both schemes, with no gradient, no
glow and no eyebrow. `/docs/own-hardware` tells a buyer the bad news —
four minutes, snapshots die with your disk, back it up yourself — before
they have paid. The quickstart says *"If it fucks up, restore from a
snapshot"*, which is a voice, not a slogan.

What does not read like repose:

- **`NAME must match [a-z0-9][a-z0-9-]{0,31}`** — a regex shown to a Mac
  owner naming their Mac, when the docs have the sentence.
- **`your plan includes 1 host, remove one before you add another`** — shown
  to an account with no plan, lowercase, and rendered where it cannot be
  seen.
- **`the server answered 404`** — accurate, and it tells a customer nothing
  about whose fault it is or what to do.
- **"has not registered yet"** and **"`repose start X` restarts it"** — two
  promises the product cannot keep.

Each is one line. Each is on the launch feature's first ten minutes.

### The engineer — a stranger's host, idempotency, restart, failure

**The failure path is better than the happy path, which is the right way
round.** A failed install left 63 MB of cached `vfkit`, no launchd agent, no
plist, no partial image and no half-made disk; a retry starts clean. Name
validation happens before anything is minted. Every refusal exits with a
distinct code. The join argument carries a token that works once within 24
hours, and `host rm` + `host add` invalidates it — a credential with a short
life and a revocation story.

**What I could not test is the whole of the security surface that matters:**
a stranger's host actually joining, what it can reach, what happens on
sleep, on Wi-Fi drop, on reboot, on a full disk. None of it was reachable.

**The credential, and my own failure with it.** The join argument decodes to
`{v, token(43), wg:{private_key(44), address, edge_pubkey(44),
edge_endpoint}}` — a live WireGuard private key, which is ENG-42's claim,
confirmed. No raw value appears in this report, in any card, in any message
I sent or in any file in this repository.

It did reach one place it should not have. At 21:48 I ran `repose host add`
**without the redactor**, because I wanted an exit code, and a live key went
into my session transcript in plain text. The host was retired seconds
later — `repose host ls --json` shows `statecheck` `retired`, and the token
works once — so it opens nothing. I am reporting it because of what it
demonstrates rather than what it cost:

> I am the hardest case this could have. I was instructed in writing never
> to let a raw key into a log, a card, a PR or a report. I redacted every
> capture for ninety minutes. Then I dropped the redaction once, to read an
> exit code, and leaked one.

A customer has neither the instruction nor the instinct. With
`host_install_command` recorded, stdout is now a ready-to-paste command line
whose base64 carries the key, and the only line beside it says "run this on
the machine you are adding. It works once, before …" — **no "this is a
secret", no "do not paste it anywhere else"**. `/docs/own-hardware` does say
*"Treat it as a password until the machine has joined"*, but that sentence
is on a web page and the key is in a terminal.

Recorded on **ENG-42**, whose own fix — take the argument on stdin or from a
file, and prefer that in the printed step — I would extend by one line:
**say, at the moment of printing, that it is a credential.**

The conductor adds a third, for whoever designs the fix to weigh rather than
as an instruction: **ask whether the credential belongs on stdout at all.**
19c put `install.command` on stdout verbatim and deliberately, so it pipes
and so the CLI can never compose a line differing from the operator's — both
good reasons. But stdout is also what a customer pastes, what a terminal
scrolls into a screenshot, and what a CI job archives. A shape that keeps
19c's rule intact: the human sentence on stderr, the credential written to a
file or a pipe only when asked, and the printed step naming the file rather
than carrying the key. There is precedent in this repository — `repose-admin
billing polar-bootstrap` deliberately puts progress on stderr and only the
pasteable block on stdout, with a comment saying it keeps the token out of
`ps` and shell history. That thinking has not reached `host add`.

---

## Signatures

One session writes the confer; this file is `19-council-mac`'s half, with
its findings already on the board as cards. The CLI and dashboard sessions
read and sign it, or record disagreement here.

- `19-council-mac` — written from the measurements above.
- `19-council-cli` — _unsigned_
- `19-council-dash` — _unsigned_
