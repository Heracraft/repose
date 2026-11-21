# Conducting a program of parallel sessions

Written at the end of the Mac mini program (workstream 19, bring your own
hardware), 19 November 2026, by the session that conducted it, because the
next one starts with none of what this one learned. It records the roles, how
they were actually orchestrated, every mistake worth repeating back, and the
inefficiencies that are worth fixing before the next program rather than
after it.

`docs/REVIEW.md` is the review half of this and is not repeated here.

## The loop a program runs on

The owner's method, stated at the end of the Mac mini program and close to
their words:

> Come up with a problem. Choose solutions — but usually those solutions are
> not good enough. Do research, find good ones, critique them, experience
> them as real users, fail, fix, and then ship. **Ship might happen three or
> four times in the middle, and that is okay.**

Seven steps, and the three a conductor is tempted to skip are the middle
three. Taken in order:

1. **Name the problem**, in the words a customer would use.
2. **Choose solutions.** The first ones will be the obvious ones.
3. **Assume they are not good enough**, because they usually are not. This is
   the step that makes the rest happen; a conductor who believes its own
   first answer skips straight to building it.
4. **Research** what already works, in products people choose to use. See the
   next section: do not invent a pattern.
5. **Critique** the researched options, including the one you like, and write
   the counter-evidence down in the proposal itself.
6. **Experience it as a real user, and expect to fail.** This is the review
   method in `docs/REVIEW.md`, and failing here is the point of it.
7. **Fix, and ship** — several times along the way, not once at the end.

The practical consequence for sequencing: **a program is not a line of
builders followed by a review.** Research and design run while fixes ship;
the review runs on each piece as it lands; and a program may ship three or
four times before it is finished. Holding everything for one grand release is
how a review of two thirds of a product gets called a review.

## The roles

Seven roles ran this program. The point of naming them is that **a role is
not a session**: sessions end, are killed, run out of context and lose their
names, and agent identity and agent timestamps are both unreliable. Address
work to a role and record it against a role.

| Role | Owns | Never does |
|---|---|---|
| **The owner** | Every decision only they can make: money, releases, what goes on sale, what a customer is promised, and any question whose answer is a preference rather than a measurement. | Merge a pull request. Be asked the same settled question twice. |
| **The release conductor** | `main`. Merges the queue in verified batches, on one machine, and pushes and ships when the owner asks. | Build items. Merge a program branch that has not passed its own gate. |
| **The mini-conductor** | One program and one branch. Launches builders, merges their branches, runs the checks on the *combination*, runs the review, queues the branch. | **Build any item itself.** Decide anything the owner reserved. Push `main`. |
| **A builder** | One slice of the program doc, in its own worktree, on its own branch, with its own checklist and pre-reserved decision ids. | Touch another builder's files. Push `main`. Queue without its checks. |
| **The spark** | Hands on hardware nobody else can reach — a rented machine, a device, a lab. Owns the money clock on it and the artifacts built there. | Decide what is published. Reason about a number instead of reporting it. |
| **A reviewer** | One customer-facing surface, reviewed as a customer. Files its own cards. Confers. | Read the code to excuse a defect. Rank a finding it did not measure. |
| **A research session** | One bounded question, answered from existing prior art, ending in a proposal. | Touch the product. Invent a pattern when a real one exists. |

The mini-conductor role is the one that needs stating most plainly, because
it is the one that drifts: **you are a merge point and a gate, not a pair of
hands.** Every hour this session spent building was an hour it was not
checking somebody else's combination, and every claim it restated in its own
words was a chance to get it wrong — which it took three times in one night.

## How it was actually orchestrated

The mechanics that worked, in the order a program uses them.

1. **One worktree per session**, `git worktree add ../<checkout>-<slug> -b <slug> main`, as `~/kanali-<slug>` on this box. Nobody edits the main checkout. New worktrees need `pnpm install` before any web test.
2. **Claim before you build, record when you stop**, in `docs/workstreams/STATUS.md`. Sequential sessions read that file first. A `Not done:` clause in it is the handoff.
3. **Decision ids reserved up front** with `ops/dev/release-queue id <count>`, from this machine's band. A reservation never leaves the machine, so **do not reserve ids for work nobody has claimed** — it parks numbers against nothing and leaves gaps for everyone else.
4. **One build lock, and one `nix` evaluation at a time.** A whole-flake evaluation takes 8.5 to 12 GB on a 16 GB box. Three sessions were killed by the kernel in one day because this rule was in the conductor's head and not in the briefs it wrote. Build checks one at a time under `flock`, with `choom -n 1000` on `nix`, and detach long jobs with `setsid` rather than `nohup ... &`, which dies with the tool call.
5. **Cross-session messages are the channel**, and they carry evidence, not conclusions. Address a role, not a session name.
6. **The board carries what is unfinished**, written at claim and at stop. Cards are where work becomes visible; they are never where a decision lives.
7. **The program branch is merged by the mini-conductor and checked as a combination.** A clean merge is not evidence the tree builds. Build and test after every merge, not at the end.
8. **The review runs before the branch is queued**, three reviewers by surface, conferring into one ranked report, with a second pass that must find no P0.

Two mechanical traps that cost real time: `/rc` must be **typed into a pane**
(`tmux send-keys -t <session>:<window> -l "/rc"` then `C-m`), because the
skill tool refuses it; and never `tmux kill-server`, because every session
lives on the default server.

## The mistakes, in the order they cost the most

These are this conductor's, except where named. They are written as what
went wrong and what prevents it, because a list of apologies teaches nothing.

1. **An artifact was built, left unrooted, and garbage-collected.** The only
   copy of the appliance image the program existed to ship was deleted and
   collected, and the published URL answered 404, which made it the program's
   one P0 and cost the evening. **Root a build with `nix build -o` and copy it
   off the machine before anything else**, including before checking it.
2. **Three claims were misattributed, all three in this session's summaries.**
   One session's observation credited to another; one builder's request
   credited to a different builder; a number this session invented and then
   defended as a builder's own ("18 GiB — it told me so"). Nothing like it
   happened in any reviewer's report. **Summarising is where evidence gets
   detached from claims.** A merge carries each session's evidence or it
   carries neither, and the question to ask is not "is this claim true" but
   **"which surfaces was it measured on"** — a claim can arrive already
   general, having lost its citation in the section it came from.
3. **Numbers were relayed without being verified.** A docs page said nothing
   about start time while this session relayed that it said "a few minutes";
   a storage figure that did not reconcile turned out to be two readings
   either side of a trim; a restore's "unaccounted seconds" were arithmetic
   against a figure nobody had measured; and "removing the appliance removes
   the nesting cause structurally" was reasoning that a measurement then
   contradicted (ratio 1.07, identical nested and native boots). **The person
   holding the measurement writes the sentence.**
4. **Three reviewers were sent to hunt a price this session had withheld.**
   A commit putting the plan on sale was deliberately held, and nobody told
   the reviewers, so a deliberate absence was reported as a defect by all
   three. **Tell reviewers what is held, or expect to pay for it three times.**
5. **A byte comparison was demanded that could not exist.** The conductor
   asked whether a rebuilt image matched a recorded byte count across a
   different source revision. A builder replaced the question with two lines
   read out of the image — a version stamp and the address it phones home to
   — which answer *identity* where size cannot. **Ask what a thing is, not
   what size it is.**
6. **An exit code was read through a pipe** and came back 0 while the script
   returned 1; the same trap caught a builder with `| head`. **Read the
   script's own exit code**, and `--rebuild` on a never-built derivation
   returns 1 meaning "cannot check", not "failed".
7. **A destructive command was nearly run on production** from a settings
   value that looked stale and was correct. **Read what the runtime uses
   before changing it.**
8. **A publish instruction that omitted one flag took the installer down for
   every platform.** The conductor told the spark to publish an artefact
   release and did not say `--latest=false`; that release became the
   repository's `latest`, the installer resolves `latest` by following that
   redirect and building an archive name from the tag, and so every install on
   every platform asked for a file under a tag that has none. 404 for 26
   minutes, immediately after fixing a 404 that had lasted 47. **When you
   instruct an action on something live, name the invariants it must not
   change, not only the action**; and notice that the first fix created the
   second break, which is the signature of a missing invariant rather than
   carelessness. Neither break was announced by an alert, a test or a
   customer: both were found because somebody happened to look.
9. **A check that is always red is as useless as one that is always green,
   and more expensive.** Every pull request run in this repository was red
   for a week — always and only on one job, which passed its three commands
   and then exited 1 trying to post a comment its token could not post. So a
   red run meant nothing, nobody could tell a real failure from the
   permanent one, and the place real failures appear had been trained out of
   everyone's attention. **Nobody asked why every run was red**, which makes
   it a conducting failure rather than a code one. Put beside the two above
   it is a pattern worth naming: the first two were checks that did not
   exist, this was a check that existed, ran, and was ignored, which is
   worse, because somebody believed it was covered. **Ask of every green
   tick and every red cross what it last told you that you did not already
   know.**
10. **Small environment traps** cost an hour each and are each one line:
   `TMPDIR` longer than 108 bytes breaks unix sockets (`/tmp/r`, not
   `/tmp/rt`); the harness kills background tool jobs under memory pressure.

## The inefficiencies worth fixing before the next program

- **Standalone reporting is right and expensive.** The owner asked for every
  update to stand alone, because they arrive cold and may not have read the
  one before — which is correct and made the updates far better. It also
  loses fidelity to condensation and costs tokens to rebuild context every
  time. The owner's own suggestion, parked and still unbuilt: make it a
  **trigger** rather than a habit, so a full standalone briefing is produced
  when asked for and short answers otherwise.
- **The conductor was a relay and should have been a switchboard.** Findings
  passed reviewer → conductor → reviewer, re-worded each hop. Every
  misattribution above happened in that re-wording. Sessions should message
  **each other** with the conductor copied, and the conductor should merge
  only what it has read at a commit.
- **Status was polled instead of reported.** Asking "where is the build"
  three times costs more than a session saying "built, here is the number".
  Tell each session to report at named events — start, finish, and any
  surprise — and then stop asking.
- **Reviewers were given no list of what was deliberately absent.** Cheap to
  write, expensive to omit (mistake 4).
- **The briefs this conductor wrote were missing the memory rule**, which is
  why three sessions died. A brief should be generated from one place, so a
  rule learned once appears in every brief after it.
- **A third of the review could not run** because it depended on an artifact
  that was not published. Sequence the thing everyone depends on first, and
  treat "the artifact exists and is reachable" as a gate of its own.

## Research the pattern before you design one

A standing instruction from the owner, given twice in one day and worth
obeying literally: **do not invent a pattern, because what you invent will be
unintuitive.** Somebody has almost certainly solved the shape of the problem
already, in a product people use by choice, and a pattern a customer has met
before needs no explaining.

So when a problem is about **how a thing should work** rather than whether it
works, the first move is a bounded research session: name eight to fifteen
real systems to study, require what the human types and what actually goes
wrong in practice, and **prefer real complaints — issues, forum threads,
postmortems — over vendor documentation wherever the claim is about a mistake
people make.** Documentation says what a tool intends; a complaint thread says
what it does to people.

Two results from running this twice in one night, both of which beat what the
conductor would have designed:

- The placement question returned the same answer from all twelve systems
  studied — a named default, a per-command override, and a way to **see**
  which is active — and none of the four options the brief had offered. It
  also killed a proposed naming scheme on the product's own facts, and found a
  recorded decision whose reasoning had quietly stopped being true.
- The counter-evidence is as valuable as the pattern: `gh repo set-default`
  refuses even when only one answer is possible and is widely resented for it,
  which is what stops a researched "refuse when ambiguous" recommendation from
  shipping as the hated version.

Require the proposal to argue against itself in its own words, and to say
where its evidence is a vendor doc rather than a measurement.

## What a mini-conductor should do, in order

1. Read the program doc, `docs/REVIEW.md`, and this file.
2. Write the briefs from one source, including the memory and lock rules.
3. Reserve each builder's decision ids and name them in its brief.
4. Launch builders in worktrees; require a `STATUS.md` claim before work.
5. Sequence any shared artifact first, rooted and copied, before anything that depends on it.
6. Merge each branch as it lands and build and test the **combination** every time.
7. Keep a written list of what is deliberately held or withheld, and give it to the reviewers.
8. Run the review by surface; confer; fix every P0 and P1; run a second pass that must find no P0.
9. Queue the branch with `ops/dev/release-queue add`, and let the release conductor merge it.
10. Record the decisions in `docs/DECISIONS.md`, close the cards with evidence, and write what is **not** done into `STATUS.md` before stopping.

## Writing someone else's claim down makes it yours

The method doc's rule is *ask which surfaces a claim was measured on, not
whether the claim is true.* Tonight added its companion, and it is the
conductor's own failure mode rather than a builder's:

**When you write another session's claim into a document, you inherit its
evidence — including the parts it inferred.** A claim in a message is
attributable and perishable. The same claim in a checklist row has your
authority and no author, and the next reader cites it rather than checking
it.

Three instances in one night, each caught by the session that made the
claim rather than by the conductor who recorded it:

- A per-package timing difference was measured and its **cause** inferred
  from a vendor's documented runner spec that never appeared in the job
  log. It travelled from a message into a checklist row, as fact, in under
  an hour.
- A single 600-second test failure was read as a hang and written up as
  one; five further measurements made it a package at 89 percent of an
  inherited deadline.
- A speculation that a slower runner would exceed that deadline reached a
  card in stronger words than the evidence deserved, and the one
  measurement that existed pointed the other way.

So: **when you record someone else's finding, record which part they
measured and which part they reasoned**, in the document, not in the
message. And expect the correction to come from them, which is itself the
argument for telling them what you wrote down.

The rule has a mirror image, which is inferring another session's **process**
from your own observation: **a red in your tree is not evidence it went
unchecked upstream.** A break reaching you proves only that it reached you.
On 2026-11-20 a builder found a merged commit's test red in its worktree and
reported that the commit "was merged without the package being run"; the
package had been run, the red had been caught, and the fix was already on the
program branch. Ask, or read the log, before describing someone else's
diligence.

The sharpest form of the rule, from the session that caught two of the three:
**the words that survive the inheritance are the measured ones.** "Ten times
slower per package" survives. "Four cores" does not. Neither does "the whole
flake checked" when what ran was `nix flake check --no-build`, which
evaluates every output and builds none — a job name is a claim like any
other, and this one differs from the thing people assume it says by every
VM test and every closure check.

## Look for the guard before you write the rule

Everything above was derived over one night from mistakes. Then
`release-queue cut` refused a cut with the sentence that states the whole
theme better than anything here:

> **Not knowing yet is not a pass. Wait for it, then cut.**

`ops/dev/release-queue:1088`. It was written before any of tonight, it is
enforced by the tool rather than by anyone's discipline, and neither
conductor knew it was there. Every lesson on this page is a variant of it:
an always-red check, a truncated log, a filtered test that matched nothing,
a job named "flake check" that builds nothing, a single measurement read as
a defect — each is **not knowing, recorded as knowing.**

So before authoring a rule, **grep for it.** A guard already in the tooling
beats a paragraph in a document, because it fires without being read. And
when a tool refuses, read its sentence: somebody put the reason there.

## A card is a report from a moment, not a statement about now

Reports age in one direction. A review files twenty findings in an
afternoon, builders fix some of them that night, and the board still
reads as it did when the report was written. So **before a conductor
sends a finding back to a builder, and before a builder fixes one, the
card is checked against the head of the branch** — and the check is
mechanical, not a reading: is the fix commit an ancestor of head
(`git merge-base --is-ancestor <sha> HEAD`), and does the behaviour the
card describes still happen there.

What it cost not to do this, 2026-11-20: ENG-41 sat in `Open` at
`urgent` while its fix had been merged for hours, which made the open-P0
count wrong in the one direction that matters — it was the number the
conductor was about to hand a release conductor as the reason a branch
could not be cut. The card was closed with the ancestor check as its
evidence, and with a pointer to the separate card for the *bypass* of
that fix rather than a clean bill, because the guard was real and was
being skipped on retry.

Two corollaries, both from the same night:

- **Close what is already fixed, with evidence, rather than fixing it
  twice.** Three builders were told to verify before building and one of
  them found a card describing a published build whose shape at head was
  different; saying "the head has a different shape" is as useful as a
  fix and costs an hour less.
- **A rank that the report has changed is changed on the card too.** The
  report decides the rank; a board left at `urgent` while the report
  says P1 wins the argument by being what the owner opens first.
