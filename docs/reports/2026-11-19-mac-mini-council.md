# Bring your own hardware, reviewed as customers — the conferred report

19 November 2026. The council of `docs/REVIEW.md`, run before the Mac mini
work goes on sale: three sessions reviewed three surfaces as three customers
who had bought a Mac mini, and this file is what they confer to. It ranks
every finding by how it reaches a customer, not by what it costs to repair.

The three reports are the evidence and this file never replaces them:

| Surface | Report | Head |
|---|---|---|
| The command line | `docs/reports/2026-11-19-mac-mini-council-cli.md` | `council/cli` at `a057f6c1` |
| The dashboard | `docs/reports/2026-11-19-mac-mini-council-dashboard.md` | `council/dash` at `beaa0245` |
| The Mac itself | in progress, `council/mac` | one P0 filed before the review could start |

Counts, conferred: **3 P0, 8 P1**, with the Mac surface still being reviewed. The
command line filed eighteen findings and ranked one P0 and seven P1; the
dashboard filed fifteen and ranked two P0, three P1 and ten P2. Where the two
disagreed on a rank, both ranks are shown below with what separates them.

## What has never been checked, said out loud

A builder asked for this to be stated rather than assumed, and it is right:
**the full `nix flake check` has never passed on this branch, anywhere, at
any point.** It was abandoned deliberately — a whole-flake evaluation takes
8.5 to 12 GB on the 16 GB machine this program ran on, and it was OOM-killed
twice — so the checks were built one at a time under a lock instead. That is
weaker evidence than a full check and it is what we have. **An idle box is
not evidence that a check passed.** Whoever cuts the queue should say this
out loud rather than let a reader assume the usual gate ran.

Also never exercised by a customer path: the disk-floor refusal (ENG-41),
because sizing happens after the image is unpacked and until 22:28 UTC
nothing unpacked. Its fix is merged at `38f5b797` and its behaviour is
unverified by anyone.

## The gate, and where it stands

The review's gate is a second pass that finds no P0. **It has not been run and
cannot pass today**: two P0 are open, and a third of the product has not been
looked at. Nothing in this report clears the branch to ship.

## The three P0

### 0. No customer on any Mac can finish the first command

Measured on a real M4 Mac at 21:38:44 UTC with **published v0.1.51** and a
join argument minted 13 minutes earlier. The whole customer path, with no
unusual step in it: buy a Mac mini, pay for Own hardware, install the
published CLI from `install.sh`, mint a credential, run `repose host add`.

```
fetching vfkit 0.6.4
fetching the appliance image 2026.11.19 (about 2 GB)
the image's checksum: .../appliance-2026.11.19/repose-appliance-aarch64.raw.gz.sha256: the server answered 404
exit 1
```

Verified three ways so it is not a mirror fault: `curl -sIL` answers
`HTTP/2 404`, `gh release list --limit 30` shows no `appliance-*` release,
and `git ls-remote --tags origin` shows no appliance tag. Still 404 at 22:25.

**This is upstream of the other two and independent of them.** Both of those
describe a customer who has a working install and then cannot steer or is
measured wrongly; this one means they never reach that state. Publishing the
image fixes nothing about placement or billing, and fixing those publishes no
image.

**The cause is a process gap, not a mistake in the code** (ENG-62): a CLI
release shipped pointing at an artefact that was never published, and nothing
in the release machinery checks that a release's assets exist. The next
release can do the same thing on any Monday.

**And fixing it broke the installer for every platform, which is the same
gap from the other side.** Publishing the appliance release made it GitHub's
`latest`, and `install.sh` resolves `latest` by following that redirect and
building the archive name from the tag — so every install on every platform
asked for a CLI archive under the appliance tag, which has none. 404 from
22:28 to 22:54 UTC, found and reverted by the session building the check,
and verified afterwards (`releases/latest` → `v0.1.51`, all six assets 200
anonymously). The conductor's publish instruction omitted `--latest=false`.
Two live breaks of one class in one evening, neither announced by any alert,
test or customer, each found only because somebody happened to look — which
is why the check runs periodically and not only at release time. The image was rebuilt tonight
and **published at 22:28:37 UTC** — `appliance-2026.11.19`, tag `a3ecdc45`,
both assets answering 200 anonymously, sha256
`fce986ce2215326863ae233dfda84b4a4b840715516da5ede2bec296a0ed567b` identical
on the builder, the Mac and this box, and the identity check passed
(`hostd-a3ecdc4`, `--api-addr 10.255.255.1:8443`, the production CA, the lab
address nowhere in the image). **The defect is fixed; the gap it came through
is still open**, and the reviewer closes the finding on its own anonymous
reading, not on this report's word.

A second consequence worth naming: **the ENG-41 disk refusal has never been
exercised by a customer path**, because sizing happens after the image is
unpacked and nothing unpacks. Its fix is merged at `38f5b797` and its
behaviour is unverified by anyone.

ENG-58, cause ENG-62.

## The other two P0

### 1. Nothing lets a customer say where a project runs, and the documented way out does not exist

A customer buys a Mac, adds it, and the install does not finish — the host
sits `waiting`. From that moment the account cannot create a working project
**anywhere**, on their hardware or ours, by any route either client offers.

What each surface measured, each on its own evidence:

- **The command line** (`council/cli`): no `placement`, `hosted` or `fleet`
  flag on `run`, `start`, `resize`, `restore` or `snapshots restore`. Every
  `repose run` is refused while an owned host is `waiting`, reproduced four
  times. The one escape the docs name — `placement: "hosted"` through the
  API — points at `/docs/api.md`, which answers 200 with 11,553 bytes and
  contains the word `placement` **zero times**. So the documented escape is an
  API field the API reference does not document and the CLI cannot send.
- **The dashboard** (`council/dash`): the New project form is byte-identical
  with and without a host — name, size, keep-for, repository, both `<select>`
  option lists diffed — with no placement control and no sentence matching
  *fleet*, *placement*, *host* or *where it runs*. The request body carries no
  such field: `{"name":"fleetcheck-1","class":"large","expires_in_s":3600,"tz":"UTC"}`.
  The server answers **201** with `placement: "own"` and `host: null`, and
  **8.0 s later the project is `error`** with **Restart** as the only control,
  which cannot work while the host waits.
- **The recovery exists and is unreachable.** With the host removed, the same
  stranded project started and ran in **17.0 s**. So nothing underneath is
  broken: the entire defect is a question nobody is asked and an answer nobody
  can see. Nothing on either page says that the fix for "your host has not
  registered yet" is to delete the host you bought the Mac for.
- **The cause is separate from the symptom and is filed apart** (ENG-66): the
  project row is inserted as `creating` in the request's transaction, while
  placement runs afterwards in the op, so a refusal has a row to leave behind
  and every retry strands another project.

Ranks: the command line ranked it **P1** on its own evidence, because
`repose start` recovers in 16–37 s and the product names that command. The
dashboard ranked it **P0** after measuring that its surface offers no
equivalent. **Conferred P0**, on the dashboard's measurement: a paid account
with no working machine by any route the product offers is the rank.

Cards: ENG-67 (no way to say where), ENG-28 (the refusals), ENG-33 and ENG-65
(what the customer is told), ENG-66 (the stranded row).

### 2. Billing measures the account against the wrong plan's limits

The billing page measures against limits the account is not on, prints
**over** in red, states that creating is refused — and then creates a project
in **6.0 s** with the banner still on screen. A customer who believes the page
deletes work to get under a limit that was never theirs. Irreversible, acted
on by the customer, and caused by the product's own sentence. ENG-34.

## The P1, in order

3. **Adding your own hardware never says what it costs.** `repose host add`
   takes a Mac into a paid plan and names no price. The command line's own
   pick of its eighteen findings, because it is a missing sentence rather than
   a bug and the same silence is what a paying customer meets. The price is
   not in question: **repose Own hardware is live in the payment catalogue at
   $10.00 a month with no overage charge**, verified against the live
   catalogue at 22:08 UTC. ENG-39.
4. **Six restore points labelled `kept`, discarded at destroy.** Six stop/start
   cycles with **Snapshot on stop** at its default produced six snapshots, each
   shown with `Expires: kept` and a Restore button, on a machine that keeps
   none. Confirmed P1 rather than P0 because the destroy confirmation states
   the truth plainly in the sentence directly above the type-to-confirm box, so
   a customer must read past an explicit warning. The half worth promoting is
   the reviewer's secondary finding: **the box is on by default on a machine
   that cannot keep a snapshot**, so the product manufactured six dead restore
   points and labelled them kept. **Split out as ENG-69**; ENG-50 keeps the
   display half.
5. **A trigger takes over a live webhook URL** and the sender's deliveries stop
   arriving where they did. ENG-32.
6. **The CLI says the account has no plan while the dashboard says it is
   billed.** Two clients, one account, opposite answers. ENG-40.
7. **A first `repose run` prints the build log over its own summary** — seventy
   lines of build output where the result should be. ENG-27.
8. **`repose host ls` says you own no hardware and its own `--json` says you
   own four.** ENG-37.
9. **The Disk column is clipped at 1440** to `15....`, so a customer cannot
   read how much disk a machine uses at the width most of them browse at.
   ENG-24.
10. **An expired session drops you on the marketing page**, with no message and
    no way back to the page you asked for. ENG-44.

## The finding that is not a defect but a decision

`internal/cli/status.go:454` carries its reasoning in the code: the host and
its address "are platform internals nobody acts on, so they print only under
`-v` (I-616, which amends I-192)". That was **true when it was written** — a
customer could not act on `host-02` and had no reason to know it existed.

**I-1366 ended the premise.** A host with an owning account is a Mac on the
customer's desk that they bought, named, plugged in and can unplug: the
opposite of an internal nobody acts on, and the thing they act on most. The
decision that changed the world and the decision that encodes the old one sit
in different entries, and only the code holds both.

So the fix is not a column; it is an entry that **amends I-616**. No id is
reserved, deliberately: whoever builds it reserves one as their first act.
The related half needs no decision at all — `ls --json` carries `host_id` as
an opaque uuid with **no host name field**, so a script gets an id and never
the name the customer gave their Mac. A hidden flag can be found by someone
determined; a missing field cannot be worked around at all.

The proposal that found this is `docs/proposals/2026-11-19-where-a-project-runs.md`
(PR #82). It researched twelve existing tools rather than inventing a pattern,
and every one that works has the same three parts: a named default, a
per-command override, and a way to see which is active.

## What the review disproved

Five claims died during the review. They are recorded so a second pass does
not rediscover them, and because a council that can only add findings inflates.

- **The dashboard does show every plan's price.** The reviewer who filed "no
  surface tells a customer what it costs" re-measured with a non-exempt
  account and withdrew the dashboard half: `/billing` renders all four plans,
  Own hardware at **$10 a month** with its own description and a Choose
  button. The earlier reading was the `exempt` branch, and that reviewer's only
  account was exempt. Provenance, which must travel with the claim: that render
  is the real dashboard build against the **fake** api. On production, only
  `GET /v1/billing` returning `own` with `available: true` is established.
  **Nobody has seen a live non-exempt account.** What survives is P2 and is the
  docs alone: `/docs/own-hardware`'s "What it costs" names no number, and its
  own link on "the Own hardware plan" goes to a page titled Pricing that opens
  "The three plans" and names $49, $39, $89, $169 and $0, never $10. ENG-47.
- **The Hosts dialog does not choose the wrong field.** It reads
  `install.command` and always did. What survives is smaller: when the api
  returns an empty command the dialog falls back silently to a value the
  install step cannot take. ENG-29, whose card body still describes the
  superseded diagnosis and carries a note saying so.
- **"There is no indicator anywhere of which machine a project is on" is too
  strong.** `repose status -v` prints `host  host-02, ip 10.64.4.10`, and
  `cli.md` documents it. The honest claim is narrower and harder to answer: the
  readout exists where nobody looks, behind a flag that also writes a line per
  api request to stderr.
- **An exempt account is not a hole in the paywall.** Withdrawn on a design
  fact plus the dashboard's reading of `/billing`. ENG-36.
- **`repose run` in the wrong folder does not silently build a large machine.**
  Retested under a pty and demoted. ENG-26 → ENG-30.

The command line dropped eight findings in total: **five by its own
re-measurement and three on a fact another session held.** That ratio is the
empirical argument for conferring rather than reviewing once.

## What this review added to the method

Each of these came from a mistake made tonight, and they are now in
`docs/REVIEW.md`:

- **Locate the hit; do not count it.** A grep that counts matches does not say
  the page a customer opens carries one.
- **Check whether an absence is on purpose.** A missing price, a missing flag
  and a missing line read the same from outside. One of tonight's was withheld
  deliberately — by the conductor — and three reviewers hunted it.
- **Check a timing window twice before filing it**, on a clock, at named
  intervals, with the readings in the report.
- **Name the measurement a shortfall is computed against.** "Six seconds
  unaccounted for" was arithmetic against a number nobody had measured.
- **When two sessions review the same surface, the merge carries each one's
  evidence or it carries neither.** Two true sentences about different
  evidence become one false sentence the moment the evidence is dropped. This
  happened three times in one night, every time in the conductor's summary and
  never in a reviewer's report.
- **Ask which surfaces a claim was measured on, not whether the claim is
  true.** A claim can arrive already general, with its citation left behind in
  the section it came from; checking it cannot catch that, and asking what was
  run can.
- **A measurement earns a rank; agreement does not.** In one reviewer's own
  words: *"I had agreed to your P0 before measuring it, which was the wrong
  order."*
- **The report carries a method section** — what was run, what could not be,
  and which of these rules bit.

## The surface still being reviewed {#held}

The Mac itself. A customer's first hour — unbox, install, add the host, start
a project, reboot — cannot be reviewed from this machine, and most of it is
held behind the appliance image, which was rebuilt tonight and is in
verification. The reviewer got far enough to find P0 zero above and no
further, because the first command is where it stops.

What nobody has measured and only that surface can: the timed first hour, and
what a Mac restart does to a running guest. The figures to confirm are a cold
park **6.5 s** a guest, an unpark **3.8 s** and the appliance back at its
login in **21 s** — all measured on an ordinary guest with no experiment, and
freeze-and-wake is the only fast path that currently exists.

**A correction the conductor owes this report.** A cold guest start on the
published image is about **289 s**, not the 57.9 s quoted elsewhere tonight.
The 57.9 s was a hand-built experimental guest: `storeOnDisk` defaults to
false, **no caller in the tree passes it true**, the mechanism exists only on
the program branch, the published image is built from `main` which has none of
it, and the research section says the product shape "is not designed here".
The cause of the four minutes is found; the fix is not shipped. ENG-70 is two
pieces of work — make the measured path the product's, then make it faster —
and the owner's instruction that this is far too slow stands against 289 s.

**The review is not finished and the gate is not passed.**
