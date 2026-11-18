# Moving the queue onto pull requests

Done on 2026-11-18 (DECISIONS I-1352). What changes for a session that
already knows the old way, and nothing else.

1. **Write the PR body before you queue.** The shape is `docs/PR.md`: five
   sentences on what changes, three lines of why, an "In depth" fold, a
   "For agents" fold, and a record block the tool fills. Put it in a
   file and pass it: `ops/dev/release-queue add --body pr.md --live "..."`.
   The old `add --live "..."` alone fails on a branch with no PR yet.
2. **`add` pushes and opens the PR.** You no longer push anything
   yourself, and you never open a PR against `main`; `add` opens it
   against `integrated`. A second `add` updates the same PR.
3. **Stack when you build on a queued branch**: `add --on <branch>`.
4. **Ids come from the PRs too.** `release-queue id` reads every open
   PR's record, so a number reserved on the other machine is taken here.
   The band (`release-queue band`) stays.
5. **The conductor merges.** Never ask the owner to merge a PR. A branch
   sent back loses `queued` and gets a comment; fix, `add` again.
6. **Entries already in this clone's queue** keep working: `ls` lists them
   beside the PRs, and a branch queued on a box with no `gh` (a repose
   guest) is still a local entry the conductor fetches by hand.

Labels on GitHub: `queued`, `ships:api`, `ships:web`, `ships:cli`,
`ships:base`, `ships:host`, `ships:edge`, `ships:infra`, made on
2026-11-18. The PR template is `.github/pull_request_template.md`.
