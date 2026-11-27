# Pull requests: the shape, the stack, the record

A change reaches `main` as a pull request against `integrated`, cut and
merged by the conductor. The PR is the queue entry: its body carries the
decisions it adds and the check to run live, and the next PR stacks on
it. The agent opens it; `docs/ops/RELEASE.md` is the procedure; this file
is the shape every PR has. DECISIONS I-1352, I-1714.

## Opening it

```
git push -u origin HEAD
gh pr create --base integrated --label queued --title "<what the change does for its user>" --body-file pr.md
```

`--base <branch>` stacks it on a queued PR. `ops/dev/release-queue add
--title ...` does the same for one release and then goes.

## The title

The title is one line that says what the change does for its user. It never starts with STATUS, Merge or WIP.

"repose ls shows each machine's size" is a title; "STATUS: ls-size
queued", "Merge main" and "WIP size column" are not. The `pr-record`
check on the PR refuses a title that breaks the rule and quotes it.

## The stack

- The base of a PR is `integrated`: the newest integrated commit, which
  is the last merged PR. A PR that builds on a PR not yet merged uses
  that PR's branch as its base; GitHub moves the base to `integrated`
  when the earlier one merges. `gh pr create --base BRANCH` does this.
- A PR is in the queue when it carries the label `queued`. The agent
  sets it with `gh pr create --label queued`; the conductor's `cut` takes
  every `queued` PR in order; `done`
  pushes `integrated`, which closes each PR as merged.
- The conductor merges. The owner is never asked to merge a PR unless
  the owner says so for that PR. A PR the conductor sends back loses
  `queued`, with the reason as a comment.
- `main` moves only when the owner asks for a release, by a push from
  the conductor; a merge into `main` on GitHub deploys the api and the
  site at once, so no PR targets `main`.

## Labels

| Label | Meaning |
|---|---|
| `queued` | ready for the conductor; the agent sets it, `cut` keeps it, `done` and a send-back remove it |
| `ships:api` `ships:web` `ships:cli` `ships:base` `ships:host` `ships:edge` `ships:infra` | what a merge of this PR deploys, as the compatibility `release-queue add` computes it; an agent sets none, and a PR without them is read from its diff (RELEASE.md "What a branch ships as") |

Nothing else goes on labels. A fact a tool reads lives in the record.

## The body

Four parts, in this order. Plain words, present tense, no line wraps
inside a paragraph (one paragraph is one line). `.github/pull_request_template.md`
holds the skeleton; `ops/dev/pr-record render` fills the record (below).

1. **What changes**: at most five sentences. What a user, or the next
   agent, gets that they did not have. No "this PR", no adjectives.
2. **Why**: at most three lines. The problem, or the owner's word, that
   this answers.
3. `<details><summary>In depth</summary>`: the longer account in plain
   language. What moved, what it replaces, what it costs, what is not
   done and why. Closed by default; a reader opens it.
4. `<details><summary>For agents</summary>`: what an agent building on
   this needs. Files and packages touched, interfaces changed (name the
   `docs/interfaces/` file), new commands or flags, the tests that hold
   it, the worktree and branch.
5. `<details><summary>Record</summary>` with one fenced block whose
   info string is `record`. Tools read this and nothing else:

   ```record
   branch: carry-lite
   commit: b8eeef575
   stack: integrated
   decisions: [I-1346, I-1348]
   migrations: []
   ships: [base, cli, web]
   live: after the deploy, repose run --temp --size tiny prints no Installing line
   ```

   `decisions` is every `I-<n>` the branch adds to `docs/DECISIONS.md`
   (and `R` ids, if any). `migrations` is the numbers under
   `internal/db/migrations/`. `stack` is `integrated` or the branch this
   PR stacks on. `ships` is what the change deploys as the author reads
   it, or `[]`; the conductor reads the diff. `live` is the check the
   conductor runs on repose.run after the deploy, one line. A field
   with nothing is `[]` or empty, never missing.

## Reading a PR with a tool

`ops/dev/pr-record` reads and writes the record; the GitHub CLI brings
the body as JSON, so no agent reads a PR into its context to find an id.

```
ops/dev/pr-record parse 123             # the record of PR 123 as key=value lines
ops/dev/pr-record decisions             # every decision id in every open PR, one a line
ops/dev/pr-record check 123             # refuses a body that misses a part or the record
ops/dev/pr-record render --branch X --commit SHA --decisions I-1,I-2 --ships web --live "..." < body.md
```

`release-queue id` reads `pr-record decisions`, so an id reserved on
another machine and written into a PR there is never handed out here.
That is what makes two machines one queue: the PR, not a file in one
clone.

## Checks

The `pr-record` workflow (`.github/workflows/pr-record.yml`) runs
`ops/dev/pr-record check --title` on every PR not against `main`, when it
opens, when its body or title is edited and on each push. It fails on a
part missing, a record field missing, a `decisions` entry that is not an
`I-<n>`, a what-changes part over five sentences, or a title that breaks
the rule above, and prints the rule. Run it before opening:

```
ops/dev/pr-record check --title "<the title>" pr.md
```
