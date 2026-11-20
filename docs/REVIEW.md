# Reviewing as a customer

A feature's builders cannot review it. They know which paths work, so they
walk those; they explain faults instead of reproducing them; and they rank
findings by how hard each is to fix. This is how repose reviews a thing
before it meets users, and it is not a substitute for tests: it finds what
tests cannot, and tests find what it will not.

The owner asked for it in these words on 2026-11-19, for the Mac mini
program and every program after it: "The last review must be super ruthless,
like going through every combination, possible combination. Every feature,
try it out, find errors, be a real customer. CLI and dashboard."

The bar the review measures against, in the owner's words at the end of that
same day: **"Gotta be something the user wants to use and pay for. Gotta be
the best experience because otherwise, what's the flipping point? They might
as well just use SSH."** That is the comparison every finding is ranked
against. A customer who could do this with `ssh` and a terminal multiplexer
is the competition, they already have it, and it costs them nothing. So a
rough edge is not a cosmetic matter here: it is the reason someone closes the
tab and goes back to what they had.

The reusable form of this, for any project, is the `customer-review` skill.
This file is the repose-specific version: the surfaces, the comparisons and
the gate.

## Two sizes of review, and they are not interchangeable

The full review below is for a thing that is **built** and about to meet
users. The owner asked for a second, lighter pass on 2026-11-19, for work
that is **designed and not yet built**, and it is a different job:

> Not a full usability review — a customer-perspective critique and a
> user-experience review that finds gaps, then re-implement them.

**The design critique.** Its subject is a proposal and a set of work items,
not a running product. It has no measurements to take, so its evidence is
different: it walks the proposed experience as a named person with named
ignorance, and it reports what that person cannot do, cannot find, or would
get wrong. It may not say "this is fine"; it either names gaps or says
plainly that it looked for them and found none, with what it tried.

**Its pass test is written by whoever commissions it, in one sentence, before
it starts.** For the owned-hardware sizing work that sentence is the owner's:
*a newcomer who knows nothing about cores or disks gets a good machine
without deciding anything, and finds the freedom when they want it.* A
critique without such a sentence turns into taste, and taste cannot be
re-implemented against.

**Why it comes before the build and not after.** The findings are meant to be
re-implemented, which is cheap while the work is a proposal and expensive
once it is merged. This is the "critique them, experience them as real users,
fail, fix" middle of the loop in `docs/workstreams/CONDUCTING.md`, and
skipping it is how a design that reads well ships as a product nobody can
use.

## Split by surface

One session per surface a customer touches, each in its own worktree and
branch, launched together. For repose that is three:

| Session | Surface |
|---|---|
| `19-council-cli` | every command, every flag, every refusal |
| `19-council-dash` | every page and action, at 1440 and 390, both schemes |
| `19-council-mac` | the Mac mini path, which the owner cannot test at all |

Three is usually right. One session cannot hold the product with a
stopwatch; five spend longer conferring than reviewing. The launch briefs
are in `workstreams/PROMPTS.md` under `19-council`.

## What every session is told

- **You are a customer, not a builder.** Do not read the code to explain a
  fault. Reproduce it. A finding is a sequence someone else can repeat, with
  what you expected and what happened. An unreproduced finding is kept and
  marked, never dropped.
- **Try the combinations a customer reaches by accident**: the same name
  twice, an action on a machine already in that state, a flag with no value,
  a dirty checkout, no network, an expired token, a read-only token doing a
  write, going back mid-flow, reloading mid-flow, two tabs at once.
- **Time everything in seconds.** The comparisons that mean something here:
  a hosted machine starts in about 12 s (`features/stop-start-destroy.md`),
  a machine on an owned Mac takes about four minutes, a restore is about
  8 s. Measure against those, not against your patience.
- **Every line a customer reads is the product** (DECISIONS I-484, I-485).
  Say which errors tell them what to do next and which leave them guessing.
- **The dashboard must do what the CLI does** (the parity rule): a
  CLI-only feature is not finished.
- **Rank by how it reaches a customer, not by how hard it is to fix.** P0
  blocks launch, P1 is fixed before the plan goes on sale, P2 after.
  Anything that loses a tenant's work or charges them wrongly is P0 whatever
  its size. Without this rule the expensive faults get quietly downgraded,
  which is how a known problem ships.

## The confer and the gate

One file, `reports/<date>-<program>-council.md`, opening with **"What would
make a user scream"**, ranked, before anything else. One session writes it
from the three; the others read and sign it, or record where they disagree
in the same file. A finding one session cannot reproduce goes to another
before it is dropped.

Then: fix every P0 and P1 by sending the finding back to the session that
built it, run the review again, and **only a second pass with no P0 opens
the gate**. For a plan, the gate is not the release *while the plan is
actually dormant*: repose ships a plan unlisted and the commit that puts it
on sale is held until the second pass is clean, so the release and the
launch are separate events.

**That separation holds only as long as nobody can buy the thing being
shipped, and that is a fact to read rather than a state to assume**
(DECISIONS I-1426). It failed the first time it was relied on. On
2026-11-20 a conductor argued that the Own hardware machinery could ship
ahead of its review because the commit putting the plan on sale was held —
and `POLAR_PRODUCT_OWN` was set on the production api, so `GET /v1/billing`
answered `available: true`, `price_cents: 1000`, `trial_days: 7`. The plan
was purchasable, with a free week, while its price appeared on no public
page. So: before shipping behind a held commit, **read the catalogue the
customer reads**. If the plan answers `available: true`, the gate is the
release, because a customer who finds it gets the code in the batch.

## The second pass, and why it is briefed differently

A second pass is **verification, not discovery**, and briefing it like a
first pass wastes it. Written after the first review of 2026-11-19 by the
session that ran its hardest surface; each rule here is a thing that
review got wrong or nearly did.

- **Give each session the fixed list with its reproduction, and require
  every one to be re-run**, then a time-boxed free hunt with the time that
  is left. A pass against the build that was reviewed re-finds what is
  already fixed and proves nothing: the second pass runs against the tree
  that holds the fixes.
- **For a surface a fresh session reviews, that order inverts, in two
  phases.** The fresh session walks the surface as a customer first, with
  no report and no card list, and files what it finds; *then* it is given
  the fixed list and verifies each one. Unprimed first costs nothing and
  buys a reading that cannot be bought later -- once a session has read
  what to look for, it cannot un-read it -- and the verification half is
  mechanical enough to keep. For a surface its own first reviewer returns
  to, the plain order stands: it is primed already.
- **A finding that was fixed and regressed outranks a new P2.** The plain
  gate does not say this and a reviewer ranking by customer impact alone
  will bury it.
- **The brief owes each session the list of what is deliberately absent,
  each with the date it was decided.** The date is the load-bearing part;
  see the gate above for what happens without it.
- **Reviewers talk to each other directly, and settle contradictions
  between themselves.** Four claims got stronger passing through the
  conductor in one night, which is the relay amplifying rather than
  anyone lying. Give the sessions each other's names at launch. A
  contradiction they resolve still goes in the report, because "we
  disagreed and this is why one of us was wrong" is evidence and the
  conclusion alone is not. **And a contradiction they cannot settle goes
  to the conductor unresolved, carrying both pieces of evidence, rather
  than to whichever reviewer is more confident.**
- **Finding something on another session's surface is a success, not a
  trespass.** The best evidence of that night crossed the split: a
  dashboard defect found by the only reviewer with a host that reached
  `online`.
- **Each finding names its instrument, and the reading that would have
  falsified it.** Two short fields, and the second does the work. The
  instrument alone becomes a box to tick, and a form that launders weak
  evidence is worse than no form. Worked, from that night: the 37.00
  against 52.00 GiB disk comparison, falsifier "the clean install's grow
  unit fails too", run and it did not, so the finding stands. The guest
  starts finding, falsifier "a clean appliance still fails to start
  guests", which is exactly what happened — the causal chain died and the
  finding got stronger, legible only because the falsifier was named in
  advance. And the one the field would have caught: "6.1 s is
  reproducible" had one timed run and one untimed observation, was
  written as "twice", and the falsifier field could not have been filled
  honestly for it.
- **A session that reviewed a surface before is a worse customer on it
  than a fresh session**, and knows where the bodies are. For a long
  install path on scarce hardware that trade is worth taking. Everywhere
  else, take fresh sessions and **do not give them the first report** — if
  they re-find what was found, that is signal; primed, it is nothing.
  Withhold the priming, not the truth: a session that asks what it is
  working on is told.

## The report is a document; its findings are cards

A review produces a ranked list all at once, which is the shape of a work
board and so the place a second source of truth gets born. The division,
settled before the findings land rather than after:

- **The report stays the document**: `reports/<date>-<program>-council.md`,
  with the reproductions, the measured numbers and the hats' verdicts. The
  owner reads it once; it is the record of what the review found.
- **The cards are the work**: one per finding someone will act on, linking
  to its section of the report rather than restating it. A finding that
  needs a decision goes to the owner's column; one that needs a fix goes to
  the open one.
- **A finding nobody will act on still gets a card**, parked, with the
  reason where it is read first — otherwise the second pass rediscovers it
  and the owner reads it twice.
- **A card's title is the symptom a customer would say**, not the cause a
  builder would name. The cause belongs in the report section it links to.
- **An unreproduced finding is kept and marked unreproduced**, parked, never
  dropped.

**And the gate is never the board.** A review passes when its own rule says
so — a second pass with no P0 — not when a column is empty. Closing a card
is not evidence a finding is fixed, where a checklist item closed with its
named evidence is.

- **File the cause separately from the symptom and link them.** Closing the
  symptom's card must not lose what caused it. The worked example is the
  2026-11-19 review: the released CLI had no `host` command for twenty-one
  minutes while the docs described it in full, and when the tag landed the
  symptom's card closed itself — so the reviewer filed the push-versus-tag
  ordering that caused it as its own card first.
- **A card's rank follows the report when the two disagree.** A card left at
  `urgent` while the report says P1 wins the argument by being what the
  owner looks at first, and the report loses it by being correct. So a
  re-rank is carried to the card with its reason, not left in the document.

## The evidence rules a review rests on

These were learned in one day of the Mac mini program, each from a mistake,
and they apply to whoever runs the review as much as to the builders:

- **Measure rather than assume.** "Nested virtualisation must be slow" was
  reasoned confidently and was wrong; twenty minutes of `fio` found the real
  cause in a different device.
- **Verify before relaying.** Whoever summarises a number is where the error
  enters. Three numbers reached the owner wrong that day and two of them
  passed through the conductor unchecked.
- **The person holding the measurement writes the sentence.** Not one person
  measuring and another describing.
- **Declare what you could not run**, with where it does run, rather than
  retrying it until it passes or leaving it out.
- **A number needs its provenance.** Two readings either side of a trim, or
  of a cache, are not comparable; say what produced each.
- **Hold a result rather than report a bad run.** A figure measured on a
  starved machine is worse than none, because it will be believed.
- **Show a test red before green**, or say plainly that you did not. A test
  that has never failed has not been shown to test anything.
- **Locate the hit; do not count it.** A grep that counts matches says a
  thing exists somewhere. It does not say the page a customer opens carries
  it. One finding that night rested on a count and died on the page.
- **Check whether an absence is on purpose.** A missing price, a missing
  flag and a missing line read the same from the outside. One of that
  night's three was held back deliberately, and three reviewers hunted it
  as a defect because nobody asked whether it was withheld.
- **And an absence being on purpose does not mean the state around it
  still is.** A decision to withhold has a date, and the world moves after
  it. The same night, the reviewer who established that the Own hardware
  copy was deliberately held then downgraded the finding on that ground —
  while `POLAR_PRODUCT_OWN` had since been set on the production api, so
  `GET /v1/billing` answered `available: true`, `price_cents: 1000`,
  `trial_days: 7`. The plan was purchasable, with a free week, and its
  price appeared on no public page. The hold was real; what nobody
  re-examined was whether it still described the product. Finding the
  intent behind an absence is the start of the check, not the end of it:
  ask when it was decided, and what has changed since.
- **A billing claim measured on an exempt account, or against a fake, is
  not measured.** The owner's own account is billing-exempt, so plans,
  limits, seats and gates take paths no customer meets; the fake api is
  the alternative and it has to be checked before it is trusted. On
  2026-11-20 both lied, in opposite directions, within an hour: the fake
  handed a host to every account whatever its billing mode, and the live
  exempt account allowed a host because an exempt account genuinely may
  have one -- which produced a P0-ranked card ("I added my own Mac
  without ever paying") for behaviour that was the gate working. Re-run
  any such finding against `setBilling` or a non-exempt account before
  believing it.
- **Eliminate an explanation with evidence from the tree under test, not
  from your branch.** The same card survived an hour because the
  elimination step was "`login --status` prints `billing-exempt`, not
  'no plan', for an exempt account" -- true on an unmerged branch, false
  of the CLI built from `main`, which printed "no plan" for that very
  account. Careful reasoning from the wrong tree eliminated the correct
  explanation.
- **Check a timing window twice before you file it.** A state read once,
  at one moment, cannot tell a propagation delay from a permanent wrong
  answer. Read it again on a clock, at a named interval, and put the
  readings in the report.
- **Name the measurement a shortfall is computed against.** "Six seconds
  unaccounted for" was arithmetic against a number that was never
  measured; the six seconds did not exist.
- **When two sessions review the same surface, the merge carries each
  one's evidence or it carries neither.** Two true sentences about the
  same screen can contradict each other, because each is true of its own
  evidence. A merge that keeps the sentences and drops whose evidence they
  were produces a confident claim that nobody measured. This happened
  three times in one night, every time in the conductor's summary and
  never in a reviewer's report.
- **The report carries a method section.** What you ran, what you could
  not run, and which of these rules bit you. A review whose method is
  invisible cannot be checked, and the next reviewer repeats its
  mistakes.
- **Do not rank a finding from the only state you could reach.** An account,
  a plan, a device or a window you happen to have is not the state a customer
  is in. One of this review's P1 findings was the behaviour of an exempt
  account's billing page, filed by the only reviewer with an account, and it
  disappeared when a normal account was reached.
- **When a reviewer's finding and the product's behaviour disagree, reach the
  state the reviewer could not.** It is almost always cheaper than the
  argument about whether they needed to.
- **Report what is right, measured.** A review that lists only damage
  misleads the owner about where the product stands. The one screen this
  review found good reached the report only because a wrong finding was
  challenged, which is how a defect-only review hides the product's health.
- **One number is not a finding; it is a margin until a second agrees with
  it.** This fired twice in one night, in the same direction. A reviewer's
  6.115 s measurement of a command that should have waited three minutes
  was a single timed run, and the session fixing it accounted for 3.0 s and
  refused to claim the rest. A 600.023 s test failure read as a hang was the
  same package's honest 461 to 496 s crossing an inherited 600 s deadline on
  a loaded box — two earlier runs turned one outlier from a defect into a
  margin. Before filing a number as a defect, ask what the same measurement
  reads when nothing is wrong, and say how many times you took it.
- **A finding that overstates is one the next reader discounts entirely**, so
  cut the sentence that outruns the evidence even when it is the sentence
  carrying the force. The hard revision is not the one that improves your own
  claim; it is the one that costs your card its punch. On 2026-11-20 a
  speculation of the conductor's — that a package taking 485 s here would
  likely exceed 600 s on a slower runner, and that this explained CI's
  unreliability — was written into a card in stronger words than the evidence
  deserved, and the single CI measurement that existed came back at 461 s,
  faster than four of five local passes. **Mark the unverified part
  unverified and say what stands without it**; here the measured case was
  untouched.
