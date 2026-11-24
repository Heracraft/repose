# The agent-first machine: what the corpus says the machine can remove (ENG-199)

2026-11-24. Research only. No product change. Brief: owner, 2026-11-23
("computers are not built around agents; they should be"), with the
gimmick tests below. Scripts and count outputs are in
[agent-first-machine-data/](agent-first-machine-data/).

## Verdict

Most of the measured cost of an agent's work does not sit in the machine.
It sits in the model's turns (96 h of 202 h), in waits on other systems
(CI and remote machines, 18.5 h), and in work that takes the time it takes
(builds and tests). The harness already owns waiting: Claude Code refused
a foreground `sleep` 78 times in this corpus and offers background tasks
and Monitor.

One candidate survives with measured value: **code intelligence that
works on an unmodified harness** (language servers on the agents' PATH and
the harnesses' own LSP switches on). Its measured value is small but real.
A second item survives as a prerequisite, not a primitive: **a guest-side
measurement**, because this corpus is one owner's dev box, not a guest.

Everything else in ENG-199's first pass, and in my own list, is killed
below with the number that kills it. The first pass's headline, "wait/poll
is 46 % of Bash wall time", is true and is not a machine problem.

## The gimmick tests, as applied here

From the brief. An idea dies if any answer is yes.

1. **Tools on the box already do it.** The agent can do the same with
   tools that are on the machine (worktrees instead of forks).
2. **It needs the user to define intent, or trusts the agent to grade
   itself** (receipts, acceptance passes).
3. **A harness vendor ships it, or obviously will** (remote control,
   background tasks).
4. **It needs a modified harness.** It must live in the kernel, the file
   system, processes, network, image, daemons or the in-machine guide, and
   the agent finds it with ordinary tools.

I add one test of my own, from the brief's "research, not guesswork":

5. **The corpus does not show the cost.** No measured cost, no candidate.

## Corpus and method

- `~/.claude/projects` on woker-1: 756 transcript files, 695 with a time
  span, 595 of them subagents. This session is excluded.
  58,307 tool calls with a matched result.
- Counts only. The scripts print categories, numbers, first words of
  commands and names of missing commands. They print no prompt, command
  text or output.
- Categories are regular expressions on the command (`deepcat.py`). A
  compound command goes to its first matching category, so some
  "search" and "read" calls are really scripts (see the slow-call check
  below).
- Turn cost: each assistant message carries `usage`. I count input,
  cache-read and cache-creation tokens once per message and split them
  over the tool calls that message made. This is the context the model
  re-reads to make that call. It is a proxy for token cost, not a bill.
- Wall time of a call: result timestamp minus call timestamp, 0 to 2 h.
  Parallel calls overlap, so the hours add up to more than wall time.
- Model time: the gap from a tool result to the next assistant message,
  when under 10 minutes.

**Caveats.** One owner, one repository (Go, Svelte, Nix), one harness
(Claude Code; the box holds no usable Codex, opencode or pi transcripts),
one machine that the owner has tuned for months. That last one matters
most: a fresh guest will show failures (missing tools, wrong PATH) that
this box no longer has. Terminal-Bench 2.0 finds "executable not installed
or not in PATH" is the largest class of command failures, 24.1 % of 3,800
sampled ([arXiv 2601.11868](https://arxiv.org/html/2601.11868v1), §4.5);
this corpus has 10 "command not found" results in 58,307 calls.

## Where the time and the tokens go

From `report1.txt`, `report2.txt`. Tool exec 106 h; model 96 h; gaps over
10 minutes 4 h.

| Category | Calls | Exec h | Output M chars | Context G tokens | Who could remove it |
| --- | ---: | ---: | ---: | ---: | --- |
| wait/poll | 3,606 | 37.3 | 1.6 | 1.27 | mostly nobody (inherent); polling turns: harness |
| test | 3,944 | 26.1 | 2.1 | 1.45 | machine (speed), inherent |
| TaskOutput (harness wait) | 109 | 10.5 | 0.1 | 0.04 | harness |
| build | 2,574 | 7.5 | 1.2 | 0.87 | machine (speed), inherent |
| search (Bash) | 18,104 | 6.4 | 30.3 | 3.81 | model, harness; a small part machine (code intelligence) |
| read (Bash) | 15,046 | 6.3 | 47.2 | 2.99 | model, harness |
| remote exec | 2,005 | 3.4 | 2.4 | 0.66 | off-machine |
| git | 2,908 | 0.9 | 7.0 | 0.71 | model |
| everything else | 9,011 | 8.1 | 9.4 | 2.61 | — |
| **total** | **58,307** | **106.5** | **101.2** | **14.41** | |

What the table says:

- **Tokens follow turns, not output.** Read and search make 57 % of calls
  and 47 % of the context the model re-reads. The cost is the number of
  turns times the context size (median 343 k tokens per wait turn). Fewer
  turns to understand code is the lever. That is mostly the model and the
  harness.
- **Read and search are fast.** Of 12.7 h in these two categories, 10.4 h
  is in 637 calls over 10 s, and those are compound commands (scripts,
  release-queue calls) that my regex put there. Real reads take about
  2.3 h for 32,500 calls. No search index would buy time.
- **Output size is not the problem.** p50 554 chars, p90 4,505, p99
  16,084. Outputs over 20 k chars are 356 calls and 9.4 % of output chars,
  and Claude Code already moves output over 30 k chars to a file with a
  2 k preview.
- **ANSI colour and progress bars are noise here.** Escape sequences are
  0.095 % of output chars; 363 outputs hold a carriage return.

### Waiting, in detail

From `report1.txt` and `report2.txt`:

| Waiting on | Calls | Hours |
| --- | ---: | ---: |
| background job or log | 1,213 | 14.5 |
| remote machine or guest | 1,338 | 12.5 |
| CI | 150 | 6.0 |
| build or test | 561 | 3.0 |
| bare sleep, other | 344 | 1.4 |

| Duration of one wait call | Calls | Hours |
| --- | ---: | ---: |
| under 10 s | 2,588 | 0.9 |
| 10 to 60 s | 529 | 3.7 |
| 1 to 2 min | 134 | 3.1 |
| 2 to 10 min | 191 | 14.7 |
| over 10 min | 69 | 14.6 |

- 29.3 h of 37.3 h is in 260 calls that block for over 2 minutes on a real
  thing: a guest booting, a CI run, a build. An event instead of a poll
  ends those waits at the same moment. It saves no wall time.
- Polling is not a loop of many turns. Runs of consecutive wait calls have
  a median length of 1 and a p90 of 2; 685 calls sit in runs of 3 or more.
- 18.5 h (remote and CI) is off this machine. A guest cannot make GitHub
  Actions finish sooner.
- Fixed sleeps fail 15.5 % of the time. Of 258 failed wait calls, 170 are
  the check after the sleep saying "not yet", and 78 are Claude Code
  refusing a foreground `sleep` and pointing at Monitor or a background
  task. The harness already teaches the event-driven form.

### Build and test

From `report2.txt`, `report3b.txt`, `report4.txt`:

- 3,623 test runs, 24.4 h. `go test` 2,335 runs and 15.9 h; Playwright 849
  and 6.4 h; vitest 329 and 1.1 h. Median 3 s, p90 42 s.
- Identical reruns in one session: 24. Go's test cache already covers
  reruns; a "warm test daemon" has nothing left to save here.
- Build or test output with a compiler or type error: 148 of 5,350 calls
  (2.8 %), 0.16 h. Diagnostics at edit time would save at most those
  round trips.
- Test output is 2.1 M of 101 M output chars. Structured test results
  would save about 2 % of output.
- 32 % of heavy calls (build, test, check) ran while another was running,
  15.2 of 29.6 h. Whether that slowed them I could not measure: 55
  same-command pairs give a busy-to-alone ratio with median 1.02 and
  quartiles 0.51 to 1.70, which is cache noise, not a finding.

### Failures the environment causes

From `report6b.txt`, on run commands only (build, test, script, remote,
wait), and the box's journal since 2026-11-11:

| Signature | Outputs | Sessions |
| --- | ---: | ---: |
| HTTP 429 or "rate limit" | 45 | 36 |
| network or DNS | 40 | 30 |
| lock wait | 14 | 1 |
| out of memory or killed | 12 | 10 |
| Playwright browser missing | 8 | 8 |
| port already in use | 8 | 7 |
| disk full | 2 | 2 |

The kernel journal holds 7 OOM kills since 2026-11-11, all of `nix`.
A session that died does not log its own death, so this undercounts.
Still, nothing here is large.

## Experiments

### 1. Find the uses of a symbol: grep against gopls

`lsp_exp.py`, on this repository (1,073 Go files, 300 k lines), 30
exported functions and methods from `internal/`, sampled with seed 199.
For each: `grep -rn --include=*.go '\bNAME\b' .`, against
`gopls references -d FILE:LINE:COL` (gopls 0.23.0, cold, no daemon).

| | grep | gopls |
| --- | ---: | ---: |
| median output, chars | 4,779 | 164 |
| total output, chars (30 symbols) | 1,112,638 | 13,863 |
| median lines | 47 | 2 |
| median time | 0.02 s | 0.72 s (max 4.93 s) |
| outputs over 30 k chars | 4 | 0 |

- Share of grep hits that are real references (precision): median 0.06.
  For the 25 methods, 0.05 (`Start`, `Close` and the like collide); for
  the 5 plain functions, 0.67. grep missed nothing (recall median 1.00).
- gopls peak resident memory: 1.36 GB on this repository. On a `tiny`
  (2 GB) that is too much; on a `small` (4 GB) it is a third.
- This is the naive grep. Agents in the corpus grep more narrowly: 1,308
  greps look like a symbol lookup (a definition pattern or one bare
  identifier), with a median output of about 1,150 chars and 2.0 M chars
  in total, 2 % of all output (`report5.txt`). So the realistic gain is a
  few percent of read-and-search output and some of the `sed -n` turns
  that follow, not the 29-fold drop of the table.

**The surprise.** The owner turned on Claude Code's `gopls-lsp` plugin on
this box. The corpus holds 0 calls of the LSP tool in 58,307. gopls is in
the repository's dev shell, but Claude Code starts outside it, and the
plugin needs the binary on the PATH of the shell that starts `claude`
([code intelligence docs](https://code.claude.com/docs/en/plugins/code-intelligence)).
`which gopls` in this session's shell finds nothing. So the switch was on
and the tool never ran, and nothing told anyone. That is the machine's
part of the gap: the harness ships the tool, the vendor says "you install
the server binary yourself", and the machine did not.

### 2. Does a `small` guest's disk limit slow an agent's compile?

`iobench.sh`, in a throwaway worktree on woker-1: `go test -count=1 -run '^$'
./internal/guestd/...` (compile 15 packages and their tests, run none),
first with an empty `GOCACHE` (cold), then after one changed line (warm).
Each run in a cgroup with `MemoryMax=4G`, a CPU quota, and, where marked,
the `small` disk limit as cgroup `io.max` (2,000 operations a second and
80 MB/s, each for reads and for writes).

| Cores | Disk limit | Cache on | Cold s | Warm s |
| --- | --- | --- | ---: | ---: |
| 1 | small | disk | 54.9 | 4.22 |
| 1 | none | disk | 48.0 | 4.18 |
| 1 | small | tmpfs | 58.3 | 4.84 |
| 4 | small | disk | 17.7 | 1.39 |
| 4 | none | disk | 20.7 | 2.15 |
| 4 | small | tmpfs | 22.7 | 2.27 |
| 8 | none | disk | 15.9 | — |

- Cores decide it: 1 core is 3.1 times slower than 4 cold and 3.0 times
  warm. The disk limit costs at most 14 % cold on 1 core and nothing
  measurable on 4 or warm. A tmpfs cache wins nothing.
- That is what burst ("Up to" cores, already shipped) addresses. Nothing
  new for the machine to build here.
- Limits of the emulation: woker-1 is AMD with 15 GB of page cache, and
  cgroup `io.max` limits reads and writes apart, while Cloud Hypervisor
  limits them together. One workload (Go compile); no `pnpm install`,
  which is the file-heavy one, because the disk was at 81 %.

## Prior art

Collected by a research subagent from the sources linked; I checked the
Claude Code and opencode LSP pages myself.

- **SWE-agent** (Yang et al. 2024, [arXiv 2405.15793](https://arxiv.org/html/2405.15793v3)):
  the interface changes the score with the same model. On SWE-bench Lite
  with GPT-4 Turbo, an edit command that lints and reverts a bad edit
  resolves 18.0 % against 15.0 % without the lint; summarised search 18.0
  against 12.0 for iterative search; a 100-line viewer 18.0 against 12.7
  for whole files. Every one of these is a tool inside the harness. The
  machine-side analogue of the lint is a language server's diagnostics,
  which Claude Code reports after each edit when the server is there.
- **Terminal-Bench 2.0** ([arXiv 2601.11868](https://arxiv.org/html/2601.11868v1)):
  command error rates 9.2 to 26.7 % by model; the largest class is
  "executables not installed or not in PATH" (24.1 %). Both repose guests
  already print the package for a missing command (I-219, I-516) and the
  guide says how to install. Already done.
- **Aegis** ([arXiv 2508.19504](https://arxiv.org/html/2508.19504)):
  environment-side changes with agent and model unmodified raised success
  6.7 to 12.5 % and cut turns up to 16.8 %, on non-coding benchmarks. The
  changes were richer tool responses, which on a coding machine are the
  harness's tools.
- **Vendors.** Claude Code cloud: Ubuntu VM, setup-script cache, pauses
  when idle, and "does not start plugin language servers" in cloud
  sessions ([cloud environments](https://code.claude.com/docs/en/cloud-environments)).
  Codex cloud: setup script, 12 h container cache, network off. Codex CLI
  sets `NO_COLOR=1`, `TERM=dumb`, `PAGER=cat`, `GIT_PAGER=cat` in its own
  exec (source, `unified_exec/process_manager.rs`). Daytona exposes an LSP
  API to the caller, not to the agent inside. E2B offers file watching to
  the caller. Morph and boxd sell fast branching (a gimmick by test 1).
  None documents language servers that an agent inside the box uses.
- **Harness LSP support.** Claude Code: the LSP tool is off until a
  code-intelligence plugin is installed; the plugin does not include the
  server; the binary must be on PATH (`gopls`, `typescript-language-server`,
  `pyright-langserver`, `rust-analyzer`, and others); when a binary is on
  PATH, Claude Code offers the matching plugin once a session.
  opencode: "LSP is disabled by default"; once on, gopls needs `go`, the
  TypeScript server needs `typescript` in the project, pyright needs
  `pyright` installed, Rust needs `rust-analyzer`
  ([opencode LSP docs](https://opencode.ai/docs/lsp/)). Codex and Gemini
  CLI: no LSP tool found.

## Candidates that survive

### 1. Code intelligence that works with no setup (ENG-212)

**What.** Put language-server binaries on the agents' PATH in both guest
bases: `gopls`, `typescript-language-server` with `typescript`, `pyright`,
and `rust-analyzer` through the rustup component the guide already names.
Turn on each harness's own switch where the machine already writes that
harness's lowest config layer (agent-guide.nix lists them): Claude Code's
official code-intelligence plugins, opencode's `lsp`. Codex and Gemini
CLI have nothing to turn on; they lose nothing.

**Measured cost it removes.**
- Symbol lookups by grep: 1,308 calls, 2.0 M chars, about 2 % of calls
  and of output, plus some of the reads that follow (corpus).
- Per lookup: median 164 against 4,779 chars, precision 0.06 for method
  names (experiment 1). On a repository larger than repose's, grep grows
  with the repository and gopls with the number of real uses.
- Compile and type errors found by a build: 148 round trips, 0.16 h
  (corpus). Small with today's models; SWE-agent's 3-point lint gain was
  measured on a 2024 model.
- A switch that is on and silently does nothing (the `gopls-lsp`
  surprise). Users bring their Claude plugins to the machine; the machine
  should make them work.

**How an unmodified agent finds it.** Claude Code finds the binary on
PATH and offers or uses the plugin; opencode starts the server when it
opens a matching file. Nothing in the guide is needed beyond, at most,
one line that the servers are there.

**Why it is not a gimmick.**
1. Tools on the box: no. The agent could install gopls, but in 58,307
   calls on a box where the owner even switched the plugin on, no agent
   did. The PATH problem is invisible from inside the agent.
2. User intent: none needed.
3. Harness vendor: the vendor ships the tool and leaves the binary to
   the machine in writing; its own cloud runs none.
4. Unmodified harness: yes, binaries and config layers repose already
   owns.
5. Measured: yes, small.

**What it costs us.**
- Image size: gopls 70 MiB closure (31 MiB itself); the TypeScript server
  about 25 MiB beyond Node; rust-analyzer about 52 MiB; pyright about
  252 MiB unpacked (nixpkgs pin b1b8759, `nix build --dry-run`). The
  NixOS guest closure has about 1 GiB of room under its 7 GiB limit
  (memory note). Pyright is the expensive one; leave it out first, or
  take basedpyright, and measure.
- Memory at run time: gopls held 1.36 GB on repose. A server starts only
  when the harness opens a file of its language, but on `tiny` (2 GB) it
  must stay off, and `tiny` already gets no Claude plugins (cli.md).
- Upkeep: versions in `images/guest/versions.json` and `bump-agents.sh`,
  one more thing per base bump. The agent-guide test must learn the new
  commands.

**How to measure the win.** Replay the corpus's task shapes on a `small`
guest with and without the servers: count lookup turns, output chars and
context tokens per task, and time to the first correct edit. Then a
Terminal-Bench subset with Go and TypeScript tasks, same model, both
ways. Kill it if the turns do not drop.

### 2. A guest-side measurement, before any other primitive (ENG-213)

**What.** The same counts as this report, from agents running inside
repose guests: the owner's and the e2e projects first, never a tenant's
content. guestd already samples process names, CPU and memory each minute
and keeps them to names and numbers (machine.md, "what repose stores").
Aggregate those samples across opted-in machines: which runners (`go`,
`node`, `pytest`, `playwright`), how long, how often a machine was at its
core limit while an agent was working, how often a process was killed for
memory, how often `repose-command-not-found` ran.

**Why it survives.** Every number above comes from a tuned dev box. The
two failures the literature says matter most on a fresh machine (missing
executables, interactive hangs) cannot appear in this corpus by
construction. Without a guest-side count, the next proposal is guesswork
too.

**Cost.** A query over data the api already stores, plus a counter in
`repose-command-not-found`. No new field may carry what a tenant typed
(CLAUDE.md, OBSERVABILITY.md).

**Measure.** It is the measurement.

## Killed

Each with the number that kills it.

| Idea | Test | Why |
| --- | --- | --- |
| Wait on an event, not a poll (ENG-199 hypothesis 1) | 1, 3, 5 | 79 % of wait time is 260 calls blocking on a real thing; an event ends them at the same moment. 18.5 h is CI and remote. Runs of polls: median 1. Claude Code ships background tasks and Monitor and refused 78 foreground sleeps. `timeout`, `until`, `inotifywait`, `tail --pid`, `systemd-run --wait` are on the box. |
| A `repose wait` command or wait daemon | 1, 3 | Same evidence; a wrapper command is what ENG-198 removes. |
| Outputs paged or summarised for a context window (hypothesis 2b) | 3, 5 | p90 output 4.5 k chars; Claude Code already persists outputs over 30 k with a preview; Codex caps at 10 k tokens. |
| Always-warm code search index (hypothesis 2a) | 1, 5 | Real reads and searches take 2.3 h for 32,500 calls. ripgrep is installed. The useful part is semantic lookup, which is candidate 1. |
| Build and test results as structure (hypothesis 3a) | 2, 5 | Test output is 2 % of output chars. Which test failed is already in the output; parsing it is the harness's job. |
| Warm build or test daemon (hypothesis 3b) | 1, 5 | 24 identical reruns in 3,623 test runs; Go's caches already hold the rest. Experiment 2: the compile is bound by cores, which burst already lends. |
| tmpfs build caches, a higher disk limit | 5 | Experiment 2: the `small` disk limit costs at most 14 % cold on 1 core and nothing warm or on 4 cores; tmpfs wins nothing. |
| `NO_COLOR`, `PAGER=cat`, `CI=1` for agent shells | 5 | ANSI is 0.095 % of output; no interactive-prompt hang found in run commands; Claude Code's Bash has no TTY, so git does not page. Codex sets these itself. |
| Diagnostics on edit as a reason on its own | 5 | 148 compile-error round trips, 0.16 h. It rides along with candidate 1 at no extra cost, but does not justify it. |
| More tools in the image | 5, already done | 10 "command not found" in 58,307 calls; both guests already name the package for a missing command. |
| Fair CPU between agents (a cgroup per agent session) | 5 | 32 % of heavy calls overlapped another, but the slowdown is not measurable from this corpus (n=55, median 1.02, quartiles 0.51 to 1.70). Revisit with candidate 2's data. |
| Port, lock and OOM isolation between parallel agents | 5 | 8 port collisions, 14 lock waits in one session, 12 OOM or kill outputs; guests already kill test runs before agents. |
| Shell state that persists between commands | 3, 4, already done | 60 % of Bash calls start with `cd`, `export` or `VAR=`; that is the harness resetting the shell. Guests already give every agent bash the dev shell and secrets through `BASH_ENV`. |
| A shorter machine guide | 5 | About 6 k tokens, cached, against a median context of 343 k. Not a measured cost; keep it accurate instead. |
| Fork or branch the machine; acceptance receipts; phone reach | 1, 2, 3 | Killed by the owner on 2026-11-23; nothing in the corpus revives them. |

## What I could not measure

- Any guest. My `repose` login on woker-1 has expired, so no e2e machine;
  experiment 2 emulates a `small` with cgroups on the dev box.
- Any harness but Claude Code.
- Overshoot of fixed sleeps: the corpus does not record when the awaited
  thing became ready.
- The slowdown from concurrent builds (above).
- Sessions that died: an OOM-killed session does not log its end.
- Whether Claude Code starts a plugin's server at session start or at
  first use of a file; this decides candidate 1's memory cost on `small`.

## Reproduce

All scripts are in [agent-first-machine-data/](agent-first-machine-data/).
Run each inside `systemd-run --user --scope -p MemoryMax=6G choom -n 500 --`.
`deep.py <exclude-session-id>` then `report.py`; `deep2.py`, `conc2.py`,
`compile.py`, `symgrep.py`, `collide2.py` take the same argument.
`lsp_exp.py <gopls> cold` runs in a worktree. `iobench.sh` needs sudo and
a throwaway worktree, and restores the one file it changes.
