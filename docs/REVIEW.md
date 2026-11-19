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

The reusable form of this, for any project, is the `customer-review` skill.
This file is the repose-specific version: the surfaces, the comparisons and
the gate.

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
the gate**. For a plan, the gate is not the release: repose ships a plan
dormant and the commit that puts it on sale is held until the second pass is
clean, so the release and the launch are separate events.

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
