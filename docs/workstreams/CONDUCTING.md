# Conducting a program of parallel sessions

Written at the end of the Mac mini program (workstream 19, bring your own
hardware), 19 November 2026, by the session that conducted it, because the
next one starts with none of what this one learned. It records the roles, how
they were actually orchestrated, every mistake worth repeating back, and the
inefficiencies that are worth fixing before the next program rather than
after it.

`docs/REVIEW.md` is the review half of this and is not repeated here.

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
8. **Small environment traps** cost an hour each and are each one line:
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
