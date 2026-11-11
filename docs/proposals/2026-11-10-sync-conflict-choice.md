# A choice when the sync and the machine changed the same files (proposal, 2026-11-10)

**Status: proposal; nothing here is decided.** The product value council
(`reports/2026-11-04-product-value-council.md`, "repose-sync", all four
members) asked that `repose sync`, on exit 6, list the files in conflict
and offer a one-key choice: keep the machine's, keep the laptop's, or
stash. This file reads main as of `a408bf2f` (2026-11-06) and branch
`run-sync-fixes` at `48c22c8c` (2026-11-10, I-1063, I-1065, I-1067),
which is not merged yet.

## Summary

| Question | Answer |
|---|---|
| Does exit 6 list the files today? | Yes, since I-573 (2026-10-07): up to 8 names, then a count, and one line naming `--stash-machine` |
| Can the user act on it today? | Only by running the command again with `--stash-machine` (all of the machine's changes to its git stash) or `--discard-machine` (the same, after ending a merge or rebase). There is no per-file choice, and no way to keep the machine's copy and send the rest |
| What this adds | On a terminal, after the list: `l` uses the laptop's copy of those files and stashes the machine's; `m` keeps the machine's copy and syncs the rest; `s` is `--stash-machine`; Enter stops. One key, no Enter needed |
| Stash naming | `l` stashes only the overlapping files under the name `repose sync: the machine's copy`; the summary prints the stash commit for `git stash apply <commit>`. Nothing uses `git stash pop` |
| Without a terminal, or with `--json` | No change: the same text and exit 6 |
| Other refusals | The mid-merge or mid-rebase refusal (also exit 6) is unchanged (decision 3) |
| Size | M. Most of the work is in the apply script and its tests |

## 1. The repo today

**The refusal.** The apply script's guard (`repose_guard`,
`internal/cli/sync.go:1664-1693`) collects every path the sync writes:
the laptop's staged and unstaged diffs, its untracked files, its bundled
submodules (`$t/paths`), and the paths the checkout to the target commit
changes (`git diff --name-only HEAD <target>`, `:1666-1671`). It lists
the machine's dirty and untracked files with `git status --porcelain=v1
-z -uall`. A machine path that is the last sync's own goes to the stash
(`$t/ownd`). Any other that equals a written path, sits under one, or
holds one below it is an overlap: it is printed after `#overlap ` on
stderr and the script exits 3 before it touches anything (`:1688-1691`).
The rest are the machine's changes elsewhere, kept and counted
(`#guestkept`). The CLI turns exit 3 into exit 6 with
`dirtyTreeError` (`sync.go:1039-1044`), whose text is
(`sync.go:256-279`):

```
Not synced: the machine changed 2 files that your laptop changed too:
  src/session.ts
  notes.md
`repose sync --stash-machine` moves the machine's changes to its git stash first.
```

`dirtyListMax` is 8 (`:260`); past it the list ends with `and N more`.

**The flags.** `--stash-machine` and `--discard-machine`
(`internal/cli/cli.go:680-685`, I-618, renamed by I-633) skip the guard
and stash everything under `repose sync --stash-machine` or `repose sync
--discard-machine` (`sync.go:1409-1421,1497-1512`). The summary ends
`stashed the machine's changes to N files (git stash <commit>)`
(`sync.go:2081-2083`), and `sync.md` tells the user to run `git stash
apply <commit>` on the machine. A short id of digits alone is printed in
full, so `git stash apply` cannot read it as `stash@{N}`
(`sync.go:1504-1509`).

**Stashing some paths.** The apply already stashes a list of paths:
`repose_stash_own` (`sync.go:1694-1701`) runs `git stash push -q -u -m
'repose run: last sync' --pathspec-from-file="$t/ownd"
--pathspec-file-nul`, and stashes inside each submodule path first. This
proposal reuses that pattern.

**The record.** `.git/repose-synced-paths` holds the hash of each path
the last sync wrote (`recordSyncedScript`, `sync.go:1763-1776`;
`docs/interfaces/guest-conventions.md:949`). A machine file whose hash
still matches is the last sync's own and is stashed without a refusal.

**Where exit 6 comes from.** Only `repose sync`. `repose run` syncs only
into a machine with no commit yet (I-367), so it has nothing to overlap,
and it refuses `--stash-machine` with exit 2 (`cli.md:78`).

**Prompts.** I-614: a confirmation is asked only on a terminal, a no
exits 1, and Ctrl-C ends the command with 130. `askYesNo`
(`internal/cli/cli.go:2285-2306`) reads a line. A one-key reader in raw
mode exists for the logins list (`internal/cli/logins.go:604-633`).
Branch `run-sync-fixes` adds the first prompt to `repose sync` (I-1065:
"Make it one and sync it? [Y/n]" for a folder that is not a repository),
asked only when stdin and stderr are terminals
(`internal/cli/makerepo.go:37` on that branch). Neither branch changes
the overlap path: `48c22c8c` touches `run.go`, `makerepo.go` and the
docs around them, and leaves `sync.go` alone.

**The spec.** R3-12 said the sync "refuses if the guest tree is dirty and
offers stash or discard". I-573 narrowed the refusal to the overlapping
paths. The offer became a flag to type again.

## 2. Proposed behaviour

**2.1 When the menu shows.** All of these hold: stdin and stderr are
terminals (as I-1065), `--json` is not set, `--stash-machine` and
`--discard-machine` are not set, and the refusal is the overlap refusal.
Otherwise the output and exit 6 stay as in section 1.

**2.2 The text.** The list keeps its form. The flag line is replaced by
the menu:

```
Not synced: the machine changed 2 files that your laptop changed too:
  src/session.ts
  notes.md
  l  use the laptop's copy; the machine's goes to its git stash
  m  keep the machine's copy; sync the rest
  s  move all of the machine's changes to its git stash, then sync
Choose l, m or s, or Enter to stop: 
```

The key is read in raw mode and echoed. `l`, `m` and `s` (either case)
act at once. Enter, `q` or Esc stops: the command exits 6 and prints
nothing more, since the refusal is already on the screen. Ctrl-C exits
130 with `Interrupted.` Any other key is ignored.

When an overlap is a submodule path, `m` is left out of the menu (2.4
explains why), and the prompt reads `Choose l or s, or Enter to stop: `.

**2.3 `l`: the laptop's copy.** The CLI runs the apply again with the
list the user saw. The guard runs as before. If the overlap is a subset
of that list, the script stashes exactly those paths, as
`repose_stash_own` does, under the message `repose sync: the machine's
copy`, prints `#stashedcopy <n> <commit>`, and the sync goes on. If the
agent changed another overlapping file in the meantime, the guard exits
3 again and the CLI shows the new list and the menu again, so no file is
stashed that the user did not see. The summary:

```
Synced: 3 modified, 0 untracked; stashed the machine's copy of 2 files (git stash 1a2b3c4)  1.4s
```

`git stash apply 1a2b3c4` on the machine brings them back, as with
`--stash-machine`.

**2.4 `m`: the machine's copy.** The apply again carries the list. The
script stashes those paths as in 2.3, under `repose sync: kept for the
machine`. The sync then runs as usual: the checkout to the target, the
laptop's diffs, its untracked files. The record
(`.git/repose-synced-paths`) is written next, without those paths.
Last, the script puts the machine's copy back from that stash with `git
restore --source=<stash> --worktree -- <tracked paths>` and `git restore
--source=<stash>^3 --worktree -- <untracked paths>`, removes a path that
the machine had deleted, and drops that one stash entry by its commit id
(found with `git stash list --format='%H %gd'`), never by position.

Two details matter here:

- The record leaves the paths out. If it kept them, their hash would
  match the machine's copy, and the next sync would take the machine's
  work for the last sync's own and stash it without asking.
- The laptop still has its change, so the next `repose sync` finds the
  same overlap and asks again until the laptop commits, reverts or
  stashes it. That is the true state of the two copies.

A submodule path cannot be restored this way without its own commit, so
`m` is not offered when one of the overlaps is a submodule.

The summary:

```
Synced: 3 modified, 0 untracked; kept the machine's copy of 2 files  1.3s
```

**2.5 `s`: stash everything.** The same as `repose sync --stash-machine`
(I-618), with the same summary.

**2.6 More than 8 files.** The list stays at 8 names and a count. `l`,
`m` and `s` act on every overlapping file, so the summary's count can be
larger than the names shown (decision 2).

**2.7 Nothing new without a terminal.** A laptop agent that runs `repose
sync` from its shell has no terminal on stdin, so it gets today's text
and exit 6. Scripts keep `--stash-machine`. No new flags (decision 1).

## 3. Contracts and docs that change

- `docs/interfaces/guest-conventions.md:949`: the record leaves out the
  paths a sync kept for the machine (2.4). The two stash names join
  `repose sync --stash-machine` and `repose run: last sync` wherever the
  guide or the conventions list them. Same commit as the code.
- `apps/web/src/content/docs/sync.md:95-131` ("When the machine has
  changes of its own"): the menu with its exact text, what each key does,
  the stash names, `git stash apply <commit>`, that `m` asks again on the
  next sync while the laptop still has its change, that `m` is not
  offered for a submodule, and that a script or a run without a terminal
  gets the old text.
- `apps/web/src/content/docs/cli.md`: the `repose sync` section and the
  exit 6 row (`:555`) say "from a terminal, the sync asks which copy to
  keep".
- `nix/guest/base/agent-guide.md:82`: today it says the sync "stops
  before writing over one you changed". New text: "...stops before
  writing over one you changed, unless the user chooses their copy: then
  yours is in `git stash list` as `repose sync: the machine's copy`."
  Reaches machines with the next base.
- `docs/features/sync-at-launch.md:68-80,134-155`: the refusal, the menu,
  and the two new apply paths.
- `docs/workstreams/07-cli.md:351-358`: the same, beside the flags.

## 4. Tests and the evidence that closes each item

All of these run against a real git checkout on both sides, as the
tests in `internal/cli/sync_overlap_test.go` do (for example
`TestSyncRefusesNamingTheOverlap`, `:93`).

| Item | Test | Evidence |
|---|---|---|
| Menu shown only on a terminal | `TestOverlapMenuOnlyOnATerminal`: a pipe on stdin, and `--json`, give today's text byte for byte and exit 6 | Test output |
| `l` | `TestOverlapKeepLaptop`: the machine changed `README.md` and `agent.txt`, the laptop changed `README.md`; after `l`, `README.md` is the laptop's, `agent.txt` is still the machine's, and `git stash show -p <commit>` holds only the machine's `README.md` | Test output |
| `l` race | `TestOverlapKeepLaptopRefusesAFileNotShown`: a second overlapping file appears between the refusal and the key; the apply exits 3 again and stashes nothing | Test output |
| `m` | `TestOverlapKeepMachine`: after `m`, `README.md` is the machine's, the laptop's other files arrived, `git stash list` has no `kept for the machine` entry, and a second sync with no change on either side refuses again over `README.md` (the record left it out) | Test output, and a mutation that writes the record with the path, which the second sync must fail |
| `m` over a commit | The laptop's commit changes `src/a.ts`, the machine has it dirty; after `m` the checkout is at the laptop's commit and `src/a.ts` holds the machine's copy as an uncommitted change | Test output |
| `m` with a submodule | The menu has no `m` line | Test output |
| `s` | Same assertions as `TestSyncRemoteFlagsStashAndSaySo` | Test output |
| Keys | `TestOverlapMenuKeys` over a fake terminal: `L`, `m`, Enter, `q`, Esc, Ctrl-C, `x` then `l` | Test output |
| Real binaries | A built CLI against a dev guest: an agent edits a file, the laptop edits it too, `repose sync`, press `l`; then the same with `m` | Pasted terminal session, and `git stash list` on the machine afterwards |
| Docs | `go test ./internal/cli -run 'TestDocs|TestAgentGuide'` | Pasted run |

## 5. Risks

- **A key pressed by accident.** `l` writes over the agent's copy. The
  agent's copy is in a named stash, and the summary prints its commit,
  so nothing is lost. Enter, the most likely accident, stops.
- **The agent writes while the user decides.** 2.3 and 2.4 refuse again
  if the overlap grew. A file the agent changes at a path the sync
  writes after the guard has run is overwritten, as today (`sync.md`
  "A file the agent creates while the sync runs").
- **Stashes pile up.** `l` adds one per choice and nothing prunes it;
  only `repose run: last sync` stashes are pruned to 10
  (`sync.go:1726-1756`). That matches `--stash-machine` today.
- **`git restore` and pathspecs** need git 2.23 and 2.26; the base has a
  newer git and `repose_stash_own` already relies on
  `--pathspec-from-file`.
- **`m` asks every time** while the laptop keeps its change. A user who
  wants the machine's copy on both sides must copy it back
  (`repose cp :src/session.ts src/session.ts`) or wait for reverse sync.

## Decisions for the owner

1. **Flags for scripts.** Add `--keep-laptop` and `--keep-machine` with
   the same meaning as `l` and `m`, or keep the menu for people and
   `--stash-machine` for scripts (recommended for now).
2. **Long lists.** On a terminal with the menu, list every overlapping
   file up to some limit (for example 20) so the user sees what `l` and
   `m` act on, or keep 8 names and a count.
3. **The mid-operation refusal.** Offer `d` (the same as
   `--discard-machine`) when the machine is in the middle of a merge or
   rebase, or leave that refusal as it is.
4. **The keys.** `l`, `m`, `s` as drafted, or `1`, `2`, `3`.
5. **Reverse sync.** The council's next step was `repose sync --pull`
   for the machine's uncommitted changes. With it, `m` could also copy
   the machine's version to the laptop so the menu stops recurring.
   Confirm it stays a separate proposal.
