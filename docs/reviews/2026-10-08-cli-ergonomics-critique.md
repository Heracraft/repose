# repose CLI: developer ergonomics critique (2026-10-08)

The repose CLI has a solid base. Exit codes are typed and documented, errors give the next command, and progress output stays off stdout. The CLI also finds the project from the checkout with no extra input. The defects are in three areas. Scripts cannot trust the confirmations. Some commands do an expensive or permanent action with no message. Some help text disagrees with the code and the docs. These three changes give the most value:

1. Make confirmations safe for scripts. A declined prompt exits nonzero, and only a real TTY can answer a prompt.
2. Stop the silent costly actions:
   - `repose sync` outside a repository creates a machine.
   - `reply` sends a project name as an answer.
   - A temporary machine is destroyed with unfetched commits on it.
   - `--since 7d` is ignored.
3. Make the help true. Fix the `run` Short, the broken flag placeholders and the word "guest".

## Strengths

- Exit codes are short, stable and the same in three places: exitcode.go:6-18, cli.md:404-420 and docs/interfaces/cli-config.md:105-118. Scripts can branch on 2 (usage), 3 (login), 4 (no project), 5 (not running) and 6 (sync refused).
- Errors name the project and the exact next command, with the current verb in it. For example: `No repose project here, and this directory has no git remote. Name one: `repose ps PROJECT` (`repose ls` lists them).` (exit 4, project.go:360-371).
- Usage refusals say what the CLI received. For example: `repose status takes at most one PROJECT, got 2 arguments: todo-app nosuch` and `"b" and --project "a" name two projects; pass one`.
- Typos get a suggestion at both levels. `repose stauts` suggests `status` or `start`, and `repose secrets lsit` suggests `secrets list` (cli.go:29-31, SuggestFor at cli.go:524 and cli.go:1040).
- An unknown flag points to the command's own help: `unknown flag: --bogus (`repose ls --help` lists its flags)` (cli.go:112).
- Renamed commands stay as hidden aliases. `projects`, `destroy` and `run --stash-remote` keep scripts working, and help does not show them (cli.go:351-356, cli.go:523, cli.go:1039).
- The CLI keeps its output streams separate. Progress goes to stderr and results go to stdout. `--json` turns off the spinner (env.go:153). `ls --json` gives `[]` for no projects.
- `ls`, `ps` and `snapshots list` take `-q` and `--json`, and they refuse both together with the same message.
- `exec` follows the docker exec contract. Words pass through unchanged, the exit code is the command's, stdout and stderr stay separate without `-t`, and stdin is read only with `-i`. `exec -- CMD` turns off the project guess (exec.go:149, exec.go:172).
- The attach fast path skips all API calls when ~/.ssh/repose covers the project. It also probes a reused ssh master with a 2 s limit, so a laptop that slept does not cause a 90 s hang (fastpath.go:15-120).
- `syncPrecheck` refuses a shallow clone or an empty repository before it creates anything, and it gives the git command that fixes it (run.go:131-158).
- Ctrl-C during the create in `run` says what exists: "Interrupted. demo was created and stays on your account; `repose rm demo` removes it." (run.go:609).
- The exit 6 sync refusal is a good example for other errors. It lists the files (capped) and gives two exact commands (sync.go:244-259).
- `stop`, `start`, `resize` and `rm` retry a 409 for 10 s (lifecycle.go:21-38). `fork` sends a request_id, so a resend after a 502 does not create more projects.
- Success lines give facts: "Restored demo-fork-1 from its 1.0 GB snapshot of 2026-10-08 17:48 in 0.2s; it is running (small)."
- `repose rm` returns when the API accepts the destroy. A `repose restore` during that destroy waits for the final snapshot (lifecycle.go:241-249, restore.go:305-322).
- docs_test.go keeps cli.md and the CLI in step in both directions for commands, flags, config keys, REPOSE_* variables and exit codes.
- The long help for `exec`, `cp`, `secrets import` and `mcp forward` states the argument rules and gives real examples, for example `op inject -i .env.tpl | repose secrets import -`.
- `--temp`, `--temp 3h` and `--temp=3h` all work.
- `repose ls` adds the LEFT and DISK columns only when a listed project needs them (status.go:103-118).
- `repose-ask` documents its exit codes in its own `--help` (cmd/repose-hook/ask.go:66). `repose paste` names the missing tool and warns WSL users about the separate clipboard (paste.go:138-160).

## Findings

### A. Confirmations and destructive actions

#### A1. A declined or unreadable confirmation exits 0, and one check controls both the spinner and the prompts

**Severity:** high

**Evidence:**
- progress.go:46-55: `isTerminal` checks only ModeCharDevice, plus `REPOSE_NO_SPINNER` and `TERM=dumb`.
- cli.go:1515-1530: `askYesNo` returns `(false, nil)` on EOF. Its own comment promises a usage error.
- lifecycle.go:216-222 prints "Nothing destroyed." to stdout and returns nil.
- snapshots.go:106-112 prints "Not restored." and returns nil. resize_class.go:87 prints "Not changed. %s is still %s." and returns nil.
- Against the fake API, `repose rm other </dev/null` prints the prompt, then "Nothing destroyed.", and exits 0.
- In a pty with `REPOSE_NO_SPINNER=1`, `repose rm` prints "No terminal to confirm destroying on; pass --yes." and exits 2.
- cli.go:754-755: `secrets set` refuses with "No terminal to type %s's value into".

**Problem:** Cron, systemd units, `ssh -n` and many CI runners give stdin from /dev/null. /dev/null is a character device, so the CLI prompts, reads EOF, declines and exits 0. A script continues as if the machine was destroyed or resized. The opposite also happens. A user who sets `REPOSE_NO_SPINNER=1` or works in an Emacs or IDE shell with `TERM=dumb` cannot confirm `rm` and cannot type a secret. The CLI then tells that user to pass `--yes`, which is the less safe path.

**Change:**
- Use two functions. `canDrawSpinner` reads `TERM` and `REPOSE_NO_SPINNER`. `canPrompt` does a real isatty check (golang.org/x/term `IsTerminal`) on stdin.
- If the user declines, gives empty input or EOF, write the decline line to stderr and exit 1, or exit with a new documented code.
- With this change, `repose rm demo && next-step` stops at a "no".

#### A2. A temporary machine is destroyed when its last window exits, with no check for unfetched work

**Severity:** high

**Evidence:**
- temp.go:182-200 `tempSessionEndedWith` prints "%s is temporary and its session has ended; destroying it." and calls `DestroyProject` at once. run.go:299-302 runs this after every attach.
- lifecycle.md:174: "The checkout gets no `repose` git remote".
- lifecycle.md:179: "Exiting the last window of its tmux session destroys it at once".
- lifecycle.md:181: a temporary machine keeps no snapshot.
- temp.go:154-172: `repose keep` can only make the machine permanent.

**Problem:** Ctrl-D or `exit` in the last shell is a habit. On a temporary machine it destroys the agent's commits and uncommitted files, and the CLI does not ask. A temporary machine has no `repose` remote, so its commits exist only on the machine. A first-day user who tries `--temp` for a short test loses the work from that test.

**Change:**
- Before the destroy, run one git check over the existing ssh master. It looks for uncommitted changes and for commits on any branch that the laptop does not have.
- If the check finds either, keep the machine until its expiry and print one line, for example `tmp-k3f9 has 3 commits you have not fetched; it is destroyed at 14:02.`
- Add the `repose` remote for a temporary machine too.
- Let `keep` take a duration, for example `repose keep tmp-k3f9 3h`.

#### A3. `repose stop` ends a working agent without a question, but `resize --size` asks

**Severity:** medium

**Evidence:**
- lifecycle.go:87 computes `busy := busyAgents(project)` before `StopProject`. lifecycle.go:141-142 prints `Interrupted %s.` after the stop.
- `DestroyCmd` (lifecycle.go:203-226) never calls `busyAgents`.
- resize_class.go:45 asks "...ends every process on it, agents included... Go ahead? [y/N]".
- DECISIONS I-500 chose to name interrupted agents after the stop, because "The sample can be up to a minute old".

**Problem:** `resize --size` asks before it ends the agents. `stop` ends them and reports it after the stop. A `repose stop` typed in the wrong checkout ends a turn in progress, and the user cannot undo that. Also, "Interrupted claude (working)." uses the same word as the Ctrl-C message, so it can read as if the CLI was interrupted.

**Change:**
- If `busyAgents` is not empty, ask: `demo has claude (working) and codex-2 (needs input). Stopping ends them. Stop demo? [y/N]`.
- Add `-y/--yes`. Without a TTY and without `--yes`, refuse with exit 2.
- If no agent is busy, stop without a question.
- Add the same clause to the `rm` prompt, and change the after-line to `Ended claude (working).`
- This change revises I-500, so record a decision.

#### A4. The in-place restore prompt names no project and no snapshot

**Severity:** medium

**Evidence:**
- cli.go:981: "Restore over the current volume? Anything since the snapshot is lost. [y/N] ".
- snapshots.go:101-115 checks only `State != "stopped"`.
- snapshots.go:122 labels the phase `"Restoring "+snapshotID`.
- resize_class.go:96 stops without a snapshot.
- lifecycle.md:77 says "Stopping takes its own snapshot, so this can be undone". lifecycle.md:91 says the dashboard asks the user to type the name.

**Problem:** This operation can cause a data loss that the user cannot undo, and it has the least specific prompt in the CLI. "Volume" is a second name for "disk". After `resize --size`, no snapshot holds the current disk, and the prompt does not say so. The dashboard is safer than the CLI for the same action.

**Change:**
- Name both ends: `Replace demo's disk with its snapshot of 2026-10-08 17:47? The stop snapshot of 17:49 keeps the disk as it is now. [y/N]`.
- If no snapshot is newer than the last start, say: `demo has no snapshot since it last ran; changes after 17:47 are lost for good.`
- Label the phase `Restoring demo from 2026-10-08 17:47`.

#### A5. The first Ctrl-C at a y/N prompt does nothing

**Severity:** medium

**Evidence:**
- cli.go:44-46: `signal.NotifyContext` stops after the first signal.
- cli.go:1521-1545: `readLine` blocks on `os.Stdin` and does not watch ctx.
- In a pty, the process ran 1.5 s after the first ^C. The second ^C ended it with SIGINT and printed no decline line.

**Problem:** Users press Ctrl-C when a destroy prompt shows the wrong name. At that moment the CLI looks hung, and it gives no confirmation that nothing happened.

**Change:**
- Read the answer in a goroutine and select on `ctx.Done()`.
- On Ctrl-C, print a newline and the decline line, then exit 130.
- Do the same for the restore prompts at restore.go:328 and restore.go:411.

#### A6. Ctrl-C during a long operation prints only "Interrupted."

**Severity:** medium

**Evidence:**
- cli.go:51-53 prints `Interrupted.`.
- resize_class.go:94-127 runs stop, then `waitOpPhased`, then `PatchProject`, then `ensureRunningFrom`.
- run.go:609 and buildprogress.go:164 have specific lines. `fork`, `stop` and `snapshots restore` have none.

**Problem:** A stop with a snapshot can take 56 s (I-595), and users press Ctrl-C during that wait. The user then does not know if the stop continues. A Ctrl-C during `resize --size` leaves the machine stopped and still small, with no line that says so. After `fork -n 5`, up to five projects exist and the output does not name them.

**Change:** Give each wait its own line in the run.go:609 style. For example:
- `Interrupted. The stop of demo continues; `repose ls` shows when it is stopped.`
- `Interrupted. demo is stopped and still small; `repose start demo` starts it.`
- `Interrupted. demo-fork-1 and demo-fork-2 exist and are starting.`

#### A7. `rm` and `stop` take one project, but `fork` makes up to ten

**Severity:** low

**Evidence:** `repose stop demo other` gives "repose stop takes at most one PROJECT, got 2 arguments: demo other" with exit 2. fork.go:71 sets `const maxForks = 10`.

**Problem:** To clean up after `fork -n 10`, the user runs nine `repose rm` commands and answers nine prompts. The other path is an xargs loop with `-y`, which skips every check.

**Change:**
- Let `rm` and `stop` accept several PROJECTs and ask one question: `Destroy demo-fork-1, demo-fork-2, demo-fork-3? Final snapshots are kept for 30 days. [y/N]`.
- Print one line per project, and exit 1 if one fails.

#### A8. The `rm` success line repeats the prompt and uses a second word for the snapshot

**Severity:** low

**Evidence:**
- lifecycle.go:248: "Destroying %s. Its final snapshot is kept for 30 days."
- lifecycle.go:300-311: "Its last snapshot is kept until %s."

**Problem:** After a "y", the user reads the same sentence that the prompt showed one second before. "Final" and "last" name the same snapshot.

**Change:**
- After an interactive "y", print `Destroying demo.`
- With `-y`, keep the retention sentence with a date: `Its final snapshot is kept until 2026-11-07.`
- Use "final" in both paths.

### B. Commands that do something you did not ask for

#### B1. `repose sync` outside a git repository creates a billable machine

**Severity:** high

**Evidence:**
- run.go:152-158 runs `if gitRepoRoot(e.Cwd) != "" { return err }; skipSync = true`.
- run.go:191-197 then calls `createProjectForRun`.
- sync.md:185 says "`repose sync` there refuses".
- I-358 allows the empty machine only for `repose run`.

**Problem:** A `repose sync` typed in ~/Downloads creates a new machine, then prints "Not a git repository, so nothing was synced." The machine counts against the plan's memory and the project cap. The code also contradicts the public docs.

**Change:**
- If `opts.Sync` is true and the precheck fails outside a repository, return the precheck error before `resolveForRun`.
- Keep I-358 for `repose run` only.
- Add a test that checks that `repose sync` in a folder with no git sends no `POST /projects`.

#### B2. `repose reply PROJECT ANSWER` can send the project name as the answer

**Severity:** high

**Evidence:**
- questions.go:178-186 treats args[0] as PROJECT only if that project has a waiting question.
- exec.go:30-32 checks the first word against all projects (I-411).
- cli.go:1343: `reply [PROJECT] [ANSWER...]`.

**Problem:** If izma has no waiting question, `repose reply izma main` sends "izma main" to the agent in another project. An answer cannot be undone.

**Change:**
- Check the first word against all projects on the account, as `exec` does.
- If the word names a project with no waiting question, exit 1 with `No question is waiting in izma.`
- Accept `--` before ANSWER for an answer that starts with a project name.

#### B3. `repose run` PROMPT is parsed for flags, but the help says "quoting is optional"

**Severity:** high

**Evidence:**
- cli.go:315-317: "PROMPT is everything after the flags, so quoting is optional."
- cli.go:116: the persistent `-v`.
- exec.go:149: `SetInterspersed(false)`, from I-411.
- `repose run add a --dry-run flag` gives "unknown flag: --dry-run (`repose run --help` lists its flags)" with exit 2.

**Problem:** Many prompts name flags, globs and question marks. The help tells users not to quote. The CLI then refuses the prompt, or it takes an unquoted `-v` as a flag and removes it from the prompt.

**Change:**
- Call `cmd.Flags().SetInterspersed(false)` on `run`, as `exec` does.
- Replace the sentence with: `Flags go before PROMPT. Quote PROMPT when it has ?, * or quotes.`

#### B4. The first `repose run PROMPT` on an account loses the prompt, and `--no-attach` then exits 0

**Severity:** high

**Evidence:**
- run.go:503-515: `needsClaudeLogin` sets `attachInstead`, and `StartAgent` runs with `AttachOnly`, so the prompt is not typed.
- run.go:529-531 prints "Claude Code is not logged in on this guest yet. Finish the login in the %s that opens, then re-run with your prompt."
- run.go:534 prints "Ready in". run.go:539-554 returns nil for `--no-attach`.
- run.go:519-521: the dialog case exits `ExitGeneric`.

**Problem:**
- On day one, the user writes a long prompt, waits for the create and logs in. The prompt is then gone.
- A script or conductor agent that runs `repose run --no-attach "fix the flaky test"` gets exit 0 and "Ready in 12s." No agent got the prompt, and no window opened.

**Change:**
- With `--no-attach`, exit nonzero as the dialog case does, with `Claude Code is not logged in on todo-app. Log in with `repose attach todo-app`, then run your prompt again.`
- Attached, save the prompt (for example in ~/.repose/pending-prompt) and type it after the login: `Log in to Claude Code in the window that opens; your prompt is typed after that.`
- If the CLI cannot type it, print the prompt again after the attach returns.

#### B5. `--since` accepts only Go durations, and the API ignores other values

**Severity:** high

**Evidence:**
- logscmd.go:92-97: `sinceArg` sends a value it cannot parse to the API unchanged.
- internal/api/http/misc.go:15-22: `sinceParam` changes a value it cannot parse to the zero time.
- logscmd.go:166: `eventsBackTo` cannot parse the value either.
- cli.go:1284 shows `e.g. 1h`, and cli.go:1314 shows `e.g. 24h`.

**Problem:** `--since 7d` and `--since 2026-10-01` are the usual forms. In production, `logs` then returns all logs, and `events` returns one page. Both exit 0, so the user gets the wrong data with no error.

**Change:**
- Parse `--since` in the CLI. Accept Go durations plus `d` and `w`, a date (local midnight) and RFC 3339.
- Refuse any other value with exit 2: `--since takes 90m, 2d, 2026-10-01 or an RFC 3339 time; got "yesterday"`.
- Make the API return `invalid` for a bad value.
- Give `--since` the same default on `logs` and `events`.

#### B6. `repose run --size xl` does nothing on an existing machine

**Severity:** medium

**Evidence:**
- cli.go:346 shows "small|large|xl". cli.go:429 (sync) shows "small|large|xl, for a machine this creates".
- run.go:1455 reads `opts.Size` only in the create path. run.go:96 checks only the `--on` combinations.

**Problem:** An agent runs out of memory, so the user runs `repose run --size xl`. The CLI attaches to the same small machine and gives no message.

**Change:**
- If the project exists and `--size` differs, exit 2: `widget is small; --size applies only when run creates a machine. `repose resize widget --size xl` changes it (restarts the machine).`
- Use the sync help text on `run`.

#### B7. `PROJECT:CHECKOUT` is accepted everywhere and ignored on most commands

**Severity:** medium

**Evidence:**
- project.go:223-234 splits on `:` for every command.
- `repose status todo-app:api` printed todo-app's status with exit 0.
- `repose rm todo-app-fork-3:api` asked "Destroy todo-app-fork-3? ...".
- checkout.go:29-56 creates ~/<extra> for any name.
- run-and-attach.md:84 describes removal as a hand edit on both sides.
- cp.go:87 says "starts at the project's checkout (~/<slug>)", which is out of date since I-368.

**Problem:** A user who wants to remove an added checkout finds `repose rm`. The `:api` part looks like a limit on the destroy, but it has no effect. A typo in CHECKOUT creates a new empty checkout.

**Change:**
- Refuse `:CHECKOUT` with exit 2 on commands that do not use it.
- Refuse a CHECKOUT that is not in ~/.repose/checkouts. Only `run --on` creates one.
- Add a removal path under an existing noun.
- Show `[PROJECT[:CHECKOUT]]` in the usage of attach, exec, ssh and code, and fix the cp Long.

### C. Help text that is false or garbled

#### C1. The `run` help says it syncs, but it syncs only into a new machine

**Severity:** high

**Evidence:**
- cli.go:313: "Create or start this checkout's machine, sync it and attach; ...".
- cli.go:314: "Create/start this checkout's environment, sync it and attach ... (`repose attach PROJECT` attaches without syncing)".
- run.go:380: `FirstOnly: !opts.Sync`.
- humanize.go:162: "...or `repose run` in its checkout to start, sync and attach."
- I-367. cli.md:27 describes the real behaviour.

**Problem:** Users type this command more than any other. Its help is false for every run after the first. A user edits locally, runs `repose run`, and expects the edit on the machine.

**Change:**
- Short: `Create or start this checkout's machine and attach; with PROMPT, start an agent on it`.
- Long: `A new machine gets a copy of the checkout. To send later work, use `repose sync`.`
- humanize.go:162: `widget is stopped. `repose start widget` starts it; `repose run` starts it and attaches.`

#### C2. Backticks in flag usage give a command as the value name

**Severity:** high

**Evidence:**
- cli.go:1112. `repose restore --help` prints `--snapshot repose snapshots list ID   restore this snapshot instead of the newest (repose snapshots list ID lists them)`.
- cli.go:1355. `repose reply --help` prints `--question repose questions   the question's id ...`.
- `snapshots list` takes `[PROJECT]`, not an ID.

**Problem:** pflag takes the first backticked word as the value name. The user reads three words as the flag's syntax, and the command in the parentheses does not run. These two pages are the undo of a destroy and the reply to a phone notification, which users read under stress.

**Change:**
- Restore: "`ID` of the snapshot to restore (default: the newest; repose snapshots list PROJECT shows them)".
- Reply: "`ID` of the question, or its last characters (repose questions shows them)".
- Add a test that fails if `pflag.UnquoteUsage` returns a name with a space.

#### C3. `--temp` help shows the internal sentinel "bare"

**Severity:** medium

**Evidence:**
- `repose run --help` prints `--temp string[="bare"]`.
- temp.go:30: `const tempBare = "bare"`.
- temp.go:48-51: `looksLikeDuration` uses `time.ParseDuration`.

**Problem:** A user can read `--temp=bare` as a valid value. `sync --temp 1d` gives an error about `--project`, which the user did not type. On `run`, `1d` becomes the first word of the prompt with no message.

**Change:**
- Use a pflag Value whose `Type()` returns `DURATION`, so help prints `--temp[=DURATION]`.
- If the next word matches `^[0-9]+[a-z]+$`, refuse with `--temp takes 10m to 24h; got 1d`.
- Use the `--since` parser here too.

#### C4. Help and messages use "guest", "environment", "guestd" and "fragment" for the machine and its Nix file

**Severity:** medium

**Evidence:**
- sync --help: "--stash-remote  stash the guest's uncommitted changes".
- cp.go:85: "Copy files between the laptop and a guest". cp.go:124 and cp.go:136: "on the guest".
- run.go:530: "on this guest". run.go:988: "Guest is running".
- secrets.go:43: "Set %s (pushed to running guest)".
- status.go:221: "the environment's agent (guestd) is not answering".
- cli.go:314: "environment".
- config --help: "apply  Apply a fragment file".
- Also cli.go:432-433, carry.go:98-113, and sync.go:191 through sync.go:1760.

**Problem:**
- The fixed name is "machine". In one sync, the user reads "the machine changed 3 files" and "Kept the guest's gh login", and a grep for "machine" misses half the messages.
- In `status`, "agent" means guestd. On a product about AI agents, the user reads "Claude stopped answering".
- `--discard-remote` reads like "drop the `repose` git remote".

**Change:**
- Replace "guest" and "environment" with "machine", and "fragment" with "the project's repose.nix", in every user-visible string.
- Rename to `--stash-machine` and `--discard-machine`, and keep the old names hidden for one release.
- status: `repose on todo-app is not answering; `repose start todo-app` restarts it`.
- Add a test that greps Short, Long, flag usage and exitf strings for `\bguest\b`, `environment` and `fragment`.

#### C5. Short help hides rules that only cli.md states

**Severity:** medium

**Evidence:**
- `repose reply --help` has only the Short, and cli.md:283 holds the first-word rule.
- `repose logs --help` shows `--kind string    console|build|ops` with no default. cli.md:269-271 says the default is console and that "A machine that booted cleanly has none".
- Only five commands have `Example:` (cli.go:775, 868, 885; exec.go:113; secrets_import.go:195).

**Problem:**
- An empty `repose logs` after a clean boot looks like a bug.
- `logs --kind ops` and `events` both show project history, and the help gives no way to choose between them.
- `reply todo-app yes` and `reply yes` differ by one word, and `resize 80G` and `resize todo-app` do too. None of these commands has an example.

**Change:**
- Give reply, logs, resize and secrets set a Long of two to four lines with the cli.md facts.
- Set the `--kind` default in StringVar so cobra prints `(default "console")`.
- Shorts: `Show a machine's failed-boot output, last build log or operation history (--kind)` and `Show what agents and repose did on a project: finishes, questions, messages`.
- Add two or three Examples each to run, resize, reply, mcp forward, fork and restore. Move cp's examples from Long into Example.

#### C6. `repose --help` is one flat list of 37 commands

**Severity:** medium

**Evidence:** `repose --help` lists the commands in one alphabetical list. A grep for `AddGroup|GroupID` in internal/cli finds nothing. cli.md groups the commands at :23, :217 and :285.

**Problem:** A first-day user cannot see the six commands that matter. `run`, `attach`, `sync` and `stop` sit between `resize`, `scan` and `completion`.

**Change:** Add cobra groups that match cli.md:
- Work: run, attach, sync, exec, ssh, code, cp, paste, open, browser.
- Agents: ps, questions, reply, mcp.
- Projects: ls, status, start, stop, resize, fork, keep, rm, restore, snapshots, logs, events.
- Setup: config, secrets, scan, notify, login, logout.

Put completion, help and version last.

#### C7. docs_test reads only cli.md, so other pages name commands that do not exist

**Severity:** medium

**Evidence:**
- run-and-attach.md:82 names `repose undo`, which gives `unknown command "undo"`.
- docs_test.go:28: `const cliDocPath = ".../cli.md"`.
- `flagOf` uses one global flag set.

**Problem:** A user who reads run-and-attach.md types `repose undo` and gets an error. A doc span like `repose sync --agent` passes the test, because `run` has `--agent`.

**Change:**
- Run the ghost-command check over every file in apps/web/src/content/docs/*.md.
- Check each `repose X ... --flag` span against the flags of X only.
- Fix run-and-attach.md:82 to name `repose restore` and `repose rm`.

#### C8. Flag help uses implementation words, version history and wrong defaults

**Severity:** low

**Evidence:**
- login --help: "--browser  use the loopback browser flow (PKCE) ...; needs a Logto application with loopback redirect URIs" and "--no-browser  device-code flow (the default since v0.1.2; kept for scripts)".
- open --help: "--local-port int   local port to bind (defaults to PORT)". cli.md:119 adds "or a free one if it's taken".

**Problem:** PKCE, Logto and "loopback" are internal words. The version note is history the user does not need. The `--local-port` text is wrong if the port is in use.

**Change:**
- `--browser  log in through a browser on this computer (only for servers that allow it)`.
- `--no-browser  same as the default; kept for scripts`.
- `--local-port  port on the laptop (default: PORT, or a free one)`.

#### C9. Help lines are too wide, and placeholder style differs

**Severity:** low

**Evidence:**
- The `secrets choose` Long is one 389-character line (cli.go:718).
- The run `--multiplexer` line is about 190 characters.
- cli.go:853 and cli.go:882 use `<package>...`.
- cli.go:1165: `resize [PROJECT] [DISK] [--size small|large|xl]`.
- The `ps` Long starts "Lists".

**Problem:** In an 80-column terminal, the flag names and their text do not line up. Placeholders in help and in cli.md have different shapes.

**Change:**
- Wrap Long text at 80 columns, or use `FlagUsagesWrapped(width)`.
- Cut each flag usage to one clause, for example `--multiplexer tmux|herdr  what runs the terminals, from the next start`.
- Use `PACKAGE...`, and remove `[--size ...]` from the resize Use line.
- Start each Long with the imperative verb.
- Add a test that fails if a Short is over 80 columns or a Long line is over 100.

#### C10. The docs describe a native Windows CLI that does not ship

**Severity:** low

**Evidence:**
- .goreleaser.yaml:13-15 builds only darwin and linux.
- install.md:8 sends Windows users to WSL.
- cli.md:54, cli.md:173 and ssh-and-editors.md:94-96 describe native Windows behaviour.
- code.go has no WSL check.

**Problem:** A WSL user reads "On Windows ..." and applies limits that are false for WSL. `repose code` fails under WSL with no clear message.

**Change:**
- Remove the native-Windows sentences, or change them to "WSL" where they are true.
- In code.go, detect WSL and stop with: `Editors on Windows use Windows' ssh and cannot reach todo-app.repose yet; run `repose ssh` or an editor inside WSL.`

### D. Arguments and flag names

#### D1. PROJECT is an argument on some commands and only a flag on others

**Severity:** medium

**Evidence:**
- cli.go:114: "most commands also take it as their argument".
- `repose open abc` gives "PORT must be a port number (1-65535), got "abc"; the project is --project NAME".
- `secrets list todo-app` works, but `secrets set todo-app FOO` fails.
- cli.go:152: `newRestoreCmd(env)` gets no global flags, so `--project` has no effect there.
- I-155 keeps `--project` on open, secrets, config and snapshots on purpose.

**Problem:** "Most" makes the user guess for each command. `repose run todo-app` gives no error and starts an agent with the prompt "todo-app".

**Change:**
- Respect I-155, but make the rule visible. Replace "most" in the `--project` help with the list of commands that take PROJECT only as a flag, and add the same list to cli.md:19.
- Make `run` refuse a one-word PROMPT that equals a project name.
- Wire the global flags into `restore`, or refuse `--project` there.
- Make `--project` a usage error on commands that do not use it (ls, login, version, completion, notify).

#### D2. Cobra's raw argument errors appear, and they point to the root help

**Severity:** medium

**Evidence:**
- `repose secrets set todo-app FOO` prints "accepts 1 arg(s), received 2" and "Run `repose --help` for the commands." with exit 2.
- `repose open todo-app 80` and `repose config add` give the same kind of output.
- cli.go:59-74 (`isCobraRefusal`, root hint at cli.go:62).
- `grep -n 'cobra\.\(Exact\|Maximum\|Minimum\)' internal/cli` finds 11 uses.

**Problem:** These are the most likely mistakes with these commands. The message does not name the missing NAME or PACKAGE. It does not say that PROJECT goes in `--project`, and it sends the user to the list of 37 commands.

**Change:**
- Replace the 11 validators with `gotArgs` validators, as `snapshotRestoreArgs` (cli.go:993) does.
- Example: `repose secrets set takes one NAME; give the project with --project: repose secrets set FOO --project todo-app`.
- In `isCobraRefusal`, point to `<command path> --help`.

#### D3. Two restore commands do one job with different flags

**Severity:** medium

**Evidence:**
- cli.go:965 `restore [PROJECT] SNAPSHOT_ID`, cli.go:987 `--as-new`, cli.go:988 `--yes` with no `-y`.
- cli.go:1068 `restore [NAME]`, cli.go:1111 `--as`, cli.go:1112 `--snapshot`.
- cli.go:1073-1074: "`repose snapshots restore` still restores...".
- I-167 keeps the in-place form under `snapshots`.

**Problem:**
- `restore NAME --snapshot ID --as X` and `snapshots restore NAME ID --as-new X` both make a new project, with two flag names.
- `restore` starts the result, but `snapshots restore --as-new` prints no state.
- `-y` fails with "unknown shorthand flag: 'y' in -y" on the most destructive command.
- "Still" is history that gives the user nothing.

**Change:**
- Use `--as` on both, and keep `--as-new` hidden for one release.
- Give `snapshots restore` the form `-y/--yes`.
- End both paths with one line format: name, snapshot time and size, state and size class.
- Remove "still" from the Long.

#### D4. One idea has several flag names, and one flag name has several meanings

**Severity:** medium

**Evidence:**
- cli.go:588: `open --no-browser` "print the URL instead of opening a browser".
- cli.go:621: `browser --no-open` "print the link instead of opening a browser".
- cli.go:286: `login --no-browser`.
- cli.go:655: `bridge --no-browser` "don't open chrome://inspect".
- `bridge --allow` against `run`/`attach` `--bridge-allow` (cli.go:363, cli.go:393, cli.go:656).
- `-y` exists at cli.go:1060, cli.go:1207 and secrets_import.go:221, but not at cli.go:988.

**Problem:** `repose browser --no-browser` gives "unknown flag". A flag learned on one command fails on the next.

**Change:**
- Use one name for "print the link" on open and browser, and keep the other name hidden for one release.
- Rename the bridge flag to `--no-inspect`.
- Document `--bridge-allow` as the prefixed form of `--allow`.
- Add a test that checks that every `--yes` has `-y`.

#### D5. The size has four names, and SIZE means the disk in one table

**Severity:** medium

**Evidence:**
- status.go:114 header `CLASS`.
- restore.go:354 and restore.go:380: `CLASS ... SIZE` (SIZE is the disk).
- `--size` flag. JSON `class`. Config `default_class`.

**Problem:** A user who reads `ls` types `--class large` and gets an unknown-flag error. In `ls --destroyed`, SIZE is the disk and sits next to CLASS.

**Change:**
- Use "size" for small, large and xl.
- Rename the column to SIZE, and the disk column to DISK.
- Add `size` beside `class` in JSON for one release.
- Accept `default_size`, and keep reading `default_class` for one release.
- Record the decision.

#### D6. `repose resize DISK` reads `80` as 80 bytes, and a shrink gives a raw API error

**Severity:** medium

**Evidence:**
- cli.go:1585-1606: `parseSize` sets mult 1 if there is no unit, and it echoes `big` as "BI".
- `repose resize demo 1G` gives "invalid: volume_bytes: volumes only grow" with exit 1 (projects.go:802).
- lifecycle.go:391 and lifecycle.go:433 divide by 1024 and print "GB".

**Problem:** `repose resize izma 100` is a normal mistake. The CLI can catch the error with no API call, but it gives an API field name and a second word for "disk".

**Change:**
- Require a unit: `"80" needs a unit, like 80G` (exit 2).
- Compare with the current size before the POST.
  - Same size: `izma's disk is already 80G.` (exit 0).
  - Smaller: `izma's disk is 80G and can only grow.` (exit 2).
- Echo the input as typed.

#### D7. Completion leaves out the hardest arguments

**Severity:** low

**Evidence:** completion.go:12 offers `bash|zsh|fish`. cp.go has no `ValidArgsFunction`.

**Problem:** On `cp`, completion offers the laptop's files where the user types a project. The user must type secret names, snapshot IDs and question IDs by hand.

**Change:**
- Complete `SLUG:` on cp. After `SLUG:`, return `ShellCompDirectiveNoFileComp`.
- Complete `restore` from `ls --destroyed -q`, `secrets rm` from `secrets list`, `--snapshot` and `snapshots restore` from `snapshots list -q`, and `--question` from waiting questions.
- Complete `--temp` with `1h 3h 24h`.
- Add powershell.

#### D8. Snapshot IDs are 36-character UUIDs with no short form

**Severity:** low

**Evidence:**
- A wrong ID gives "not_found: snapshot not found" with exit 1.
- `repose snapshots restore 0190` with no checkout suggests `repose snapshots restore PROJECT`, which drops SNAPSHOT_ID.

**Problem:** Each restore needs a copied UUID. A typo gives an API string with no help.

**Change:**
- Accept a unique prefix, and show 8 characters in the table.
- On not_found, print `demo has no snapshot 0190...001; `repose snapshots list demo` lists them.` with exit 4.
- Build the hint from the real Use line.

#### D9. `repose cp` on Windows reads a drive letter as a project

**Severity:** low

**Evidence:** cp.go:43-52: `parseCpSide` reads a colon before any `/` as a remote side.

**Problem:** `repose cp C:\a.txt izma:/tmp` gives "two projects", or a not-found error for project C.

**Change:** Read `X:\` and `X:/` as a local path, and require at least two characters in PROJECT.

#### D10. The environment variables use two polarities

**Severity:** low

**Evidence:** env.go:28-29: `REPOSE_INPUT_PROXY=0` and `REPOSE_CLIPBOARD_PATH=0`, beside the `REPOSE_NO_*=1` variables in env.go:21-37.

**Problem:** `REPOSE_NO_INPUT_PROXY=1` follows the pattern of the other variables, but it has no effect and gives no error.

**Change:** Accept `REPOSE_NO_INPUT_PROXY=1` and `REPOSE_NO_CLIPBOARD_PATH=1`, and keep the `=0` forms for one release.

### E. Errors and exit codes

#### E1. Offline or an identity-provider outage gives "Not logged in" and exit 3

**Severity:** high

**Evidence:**
- oidc.go:356-362: discover runs before the `refreshing session:` wrap.
- oidc.go:64-66 and oidc.go:72-74: `discovering %s:`.
- oidc.go:50: 24 h cache.
- client.go:117 wraps the error as `notLoggedInError`.
- env.go:194-205 treats only the `refreshing session` prefix as a network problem. Its comment says this outcome is the one to avoid.

**Problem:** The first command on bad wifi tells the user to log in again, and `repose login` then fails too. A script that reads exit 3 as "run login" starts a browser flow.

**Change:**
- Send every transport failure in the token source to `unreachableError`.
- Print `Could not reach the login server (<host>). Check your connection.` with exit 1, or exit with a new documented code.
- Keep exit 3 for a missing or refused login only.

#### E2. A capacity failure from an op exits 1, and the message says "try again" twice

**Severity:** medium

**Evidence:**
- run.go:1171-1180 sends op failures to `opFailed`, and humanize.go:197-207 always returns `ExitGeneric`.
- cli.md:415 documents exit 8.
- recover.go:182 ends "try again in a few minutes". humanize.go:212-215 adds "Try again in a few minutes; we have been alerted."

**Problem:** A script that retries on exit 8 gets exit 1 if the host runs out of room during boot.

**Change:**
- Map op codes to exit codes in one place in `opFailed`: `ExitCapacity` for insufficient_capacity.
- Do not add `next` if the reason already gives a retry.
- Result: `Could not start todo-app: the server has no room for it now (insufficient_capacity). Try again in a few minutes.` with exit 8.

#### E3. API and proxy errors print raw internals

**Severity:** medium

**Evidence:**
- client.go:165-172 turns a body that is not JSON into `Code internal` with the raw body as the message.
- env.go:243-245 prints `%s: %s`.
- fork.go:235-245 is the only retry of a redeploy's 502.

**Problem:**
- During a deploy, each command prints something like `internal: Bad Gateway` with exit 1 and no next step.
- `code: message` shows API field names such as `volume_bytes`.

**Change:**
- In `exitCodeFor`, give each status class one sentence.
  - 5xx: `The repose api answered 502. Try again in a minute.`
  - A 200 that is not JSON: `<url> is not a repose api. Check --api-url or $REPOSE_API_URL.`
- Keep the body for `-v`.
- Retry an idempotent GET once on a 502 or 503.

#### E4. `-v` is documented as debug output but prints almost nothing

**Severity:** medium

**Evidence:**
- cli.go:116: "debug logging to stderr".
- Verbose is read only at buildprogress.go:156 and run.go:1185.
- troubleshooting.md:8 tells users to add `-v`.

**Problem:** The documented next step gives no URL, status or request ID for support.

**Change:**
- Under `-v`, log `GET <path> -> <status> <duration> (request <id>)`, the error body on failure, and the ssh argv without secrets.
- Keep the field rules from CLAUDE.md.

#### E5. ssh errors that never clear cost 60 s, and the advice is to restart the machine

**Severity:** medium

**Evidence:**
- run.go:963-999: `waitForSSH` retries every failure except `ExitCode -1`.
- run.go:988: "Guest is running but SSH did not answer in 60s. `repose stop` and then `repose start` restart it."
- run.go:796: `isCertRefusal`.

**Problem:** A laptop-side error waits a full minute. The suggested fix takes a snapshot and ends every agent, and it does not touch the cause. These errors include "ControlPath too long", a bad config line, "Host key verification failed", "Permission denied (publickey)" and "Could not resolve hostname".

**Change:**
- Classify ssh stderr as `isCertRefusal` does. For laptop-side errors, stop at once and print the ssh line with the local fix.
- Keep the wait for timeouts and refused connections only.
- Say: `todo-app is running but did not answer ssh in 60 s.`

#### E6. Input checks happen in the API, so exit codes differ for the same kind of mistake

**Severity:** medium

**Evidence:**
- misc.go:166: a bad `--kind` gives `invalid: kind must be console, build or ops` with exit 1.
- `--size` is checked in the CLI with exit 2 (resize_class.go:58).

**Problem:** The same kind of mistake gives exit 2 on one flag and exit 1 on the next, with API wording.

**Change:** Check enumerated and duration flags in the CLI with exit 2, in the `--size` message shape. B5 and D6 hold the `--since` and size parts.

#### E7. Fix hints name flags and arguments the command does not have

**Severity:** low

**Evidence:**
- sync.go:508, sync.go:514 and sync.go:520 say "...or pass --no-sync.", but `repose sync` has no `--no-sync` (cli.go:429-434).
- project.go:360-372 builds `<command> PROJECT`, which drops exec's COMMAND.
- run.go:534 prints "Ready in %s." for sync too.

**Problem:** A hint that names a missing flag sends a first-day user into a usage error.

**Change:**
- Make the precheck suffix depend on the command.
- Print `repose exec PROJECT COMMAND`.
- Print one success line on run, for example `Connected to widget (large) in 4.1s.`, and nothing besides the sync line on sync.

#### E8. The project cap exits 1, while other plan limits exit 7

**Severity:** low

**Evidence:**
- env.go:230-236: `project_limit` gives `ExitGeneric`.
- limits.md:10 and limits.md:16 give exit 7. limits.md:18 gives no code.

**Problem:** A script that forks must parse English to tell a limit from a fault.

**Change:**
- Return 7.
- Change the cli.md row to `A plan or account limit refused it (memory, disk, egress, project count); the message names it`.
- Add `Destroy one first with `repose rm PROJECT`.`

#### E9. One failure has several wordings, and messages contain guesses

**Severity:** low

**Evidence:**
- project.go:233, restore.go:195 and sshprepare.go:296 word an unknown project in three ways.
- configcmd.go:112 prints `reading %s: open %s: no such file or directory`.
- env.go:210 prints "Cannot reach the api: <Go error>".
- env.go:241 guesses a cause for a 429 and suggests `repose status`, which is another API call.
- env.go:223 and run.go:1118 add "(We have been alerted.)".
- client.go:69 and client.go:130-139 wait up to 60 s with no output.

**Problem:** A support grep finds one third of the reports. A rate-limited user is told to make another call, and the silent wait looks like a hang.

**Change:**
- Use one helper for project-not-found.
- Print `<path> does not exist.` and `Could not reach <host>: connection refused.`
- For a 429, print `Too many requests from this account in the last minute. Try again in a minute.`
- During the wait, print `Waiting for the api's rate limit...`.
- Remove "(We have been alerted.)".

### F. Output for scripts

#### F1. JSON coverage has gaps, and `--json` streams are not NDJSON

**Severity:** medium

**Evidence:**
- util.go:14-17 uses `SetIndent`.
- logscmd.go:34 and logscmd.go:138 write one indented object per event.
- snapshots.go:83 prints "Snapshot of %s taken in %s." with no ID.
- `secrets list --json` gives "unknown flag".
- cli.md:279: `questions --json` leaves out terminal waits.

**Problem:**
- A script cannot get the ID of the snapshot it just made.
- `repose events -f --json | while read line` breaks, because each event is six lines.
- An "is an agent blocked?" check misses agents at a permission prompt.

**Change:**
- Give `--json` to snapshots create, sync, start, stop and resize. Print the Project or Snapshot object.
- Print compact NDJSON for `-f` streams.
- Add the terminal waits to `questions --json` with kind `terminal`.

#### F2. `status --watch` adds a new copy every 5 s and stops on the first error

**Severity:** medium

**Evidence:**
- cli.go:497-507 runs the loop with no redraw and returns on the first error.
- cli.md:229: "--watch (every 5 seconds)".

**Problem:** In a terminal, the screen scrolls, and the user cannot see the change. A short network error ends the watch. A script cannot wait for a state.

**Change:**
- Redraw in place on a TTY. Print one block per change when stdout is not a TTY. Retry API errors.
- Add `repose status --wait running [--timeout 5m]`, which exits 0 when the machine reaches the state and nonzero at the timeout.

#### F3. Wait and timeout behaviour differs across commands

**Severity:** low

**Evidence:** `repose rm --help` shows `--wait`. `repose stop --help` shows no wait or timeout flag.

**Problem:** A script author must remember which commands block. A stop that stays on the server holds the script with no limit.

**Change:**
- Block by default on all operations, with `--no-wait`.
- Add `--timeout DURATION`, which exits nonzero and prints the op ID.

#### F4. Text output has several shapes

**Severity:** low

**Evidence:**
- mcplist.go:62-73 prints TSV if stdout is not a TTY.
- status.go:79-81 writes the disk note to stdout.
- status.go:347 prints cells such as `claude: working`.

**Problem:** `mcp list` and `secrets list` change to TSV in a pipe, but `ls` and `snapshots list` do not. Notes go to stdout.

**Change:**
- Apply the mcp list rule to all lists: if stdout is not a TTY, print TSV rows with fixed columns and no header, and send notes to stderr.
- Or state in cli.md that only `--json` and `-q` are stable.

#### F5. Tables show times in two zones with no zone marker

**Severity:** low

**Evidence:** secrets.go:79 and configcmd.go:26 have no `.Local()`. snapshots.go:57, personal.go:258, restore.go:348 and fork.go:264 call `.Local()`.

**Problem:** Two tables can show the same minute hours apart.

**Change:** Show local time in tables, and use RFC 3339 with a zone in TSV and JSON. Add `.Local()` at the two call sites.

#### F6. `secrets set` refuses a piped value

**Severity:** low

**Evidence:** cli.go:754-755 refuses stdin that is not a terminal. `secrets import -` reads stdin.

**Problem:** `op read ... | repose secrets set NAME` is the usual script form. The only workarounds are `--from-file /dev/stdin` and `--from-env`.

**Change:** If stdin is not a terminal, read the value from stdin and remove one trailing newline. I-155 keeps `--project` on secrets.

#### F7. `ps --json` has a different shape for each multiplexer

**Severity:** low

**Evidence:** ps.go:23-31 (PsAgent: `focused`) and ps.go:74-82 (PsWindow: `current`, `idle_seconds`). I-509 records the split.

**Problem:** A script that works with both multiplexers needs two jq programs, and the user breaks it with `--multiplexer`.

**Change:** Use one shape: name, agent, command, state (busy, idle, waiting or unknown), focused and idle_seconds. Set a field to null if one side cannot know it.

### G. Daily loop and missing paths

#### G1. The laptop CLI never learns that it is out of date

**Severity:** medium

**Evidence:**
- client.go:113 and client.go:224 send `User-Agent: repose-cli` with no version.
- `repose version` prints `repose dev (herakraft)`.
- The only version check is on the machine: carry.go:220-226 writes ~/.repose/cli-version, and agent-guide.md:13 tells agents to read it.
- run-and-attach.md:59: "Worktrees made before CLI v0.1.22 keep their old names".

**Problem:** An old CLI does not have flags that the docs describe. The user sees `unknown flag` and does not know the version is the cause.

**Change:**
- Send `repose-cli/<version>`. The API returns the latest version and an optional minimum version.
- Print at most once a day: `repose v0.1.20 is older than v0.1.24; run the install command again to update.`
- Refuse below the minimum with its own exit code.
- Keep only one of `version` and `--version`.

#### G2. No repose command can see, enter or remove a `--worktree` tree

**Severity:** medium

**Evidence:**
- run-and-attach.md:46: windows are named `claude`, `claude-2`.
- checkout.go:186-191 prefixes added checkouts only.
- ps.go:77-82 has no directory or branch.
- run-and-attach.md:50-54 describes manual cleanup.

**Problem:** After three `run --worktree` prompts, `repose ps` does not show the window that is in worktree-2. To run tests there, the user needs `repose exec sh -c 'cd ~/todo-app-worktree-2 && npm test'`. Each worktree keeps its own dependencies on the plan's disk.

**Change:**
- Name windows `worktree-2/claude`, and add the directory and branch to `ps` and `ps --json`.
- Let `PROJECT:CHECKOUT` or `--worktree N` address a worktree on exec, code, attach and cp.
- List worktrees in `repose status`.
- Add a removal path that refuses while the branch has commits the laptop has not fetched.

#### G3. Getting a fork's work back is a hand-typed remote, and two pages disagree

**Severity:** medium

**Evidence:**
- lifecycle.md:213: `git remote add fork-2 todo-app-fork-2.repose:~/todo-app`.
- sync.md:160: `...:~/todo-app-fork-2`.
- lifecycle.md:206 says the code is at `~/todo-app` in each copy.
- git_remote.go:34 adds only the `repose` remote.
- The lifecycle.md:198-203 example does not match the real output.

**Problem:** Keeping the best fork is the least supported step of the fork flow. A user who follows sync.md gets a fetch error that does not name the cause.

**Change:**
- In a checkout of the project, have `fork` add one remote per copy, named after the copy.
- Have `repose rm` remove it, with the existing `forgetReposeRemote`.
- Fix sync.md:160 and the lifecycle.md example.

#### G4. The CLI cannot apply an earlier config revision

**Severity:** medium

**Evidence:**
- `config show --help`: "--revisions   list revisions instead".
- `config apply [PATH]` has no revision option.
- configcmd.go:118-121 uses the newest revision that built.
- config.md:178 sends the user to the dashboard.
- api_routes.go:254 has `ApplyRevision`.

**Problem:** After a `config add` breaks the machine, the CLI shows the revision IDs and then sends the user to a browser to use one. `snapshots` uses subcommands, but revisions use a flag on `show`.

**Change:**
- Add `config revisions` with `--json` and `-q`.
- Add `config apply --revision ID`, and accept the short ID from "Building revision 4f1c2a9e".
- Keep `show --revisions` hidden for one release.

#### G5. `repose notify` has no read command, and `notify test` hides failures

**Severity:** medium

**Evidence:**
- Bare `repose notify` prints help. Only `notify set` with no flags prints the settings ("email: on", "ntfy: none").
- cli.go:1363 Short: "Change notification settings".
- notifycmd.go:50-59 exits 1 only if both channels fail.
- notify.go:515-546 leaves out a channel that is off and reduces errors to "error".
- On the machine, `repose-notify` sends a message.

**Problem:** A careful user does not run a command named "set" only to read the values. "email: ok / ntfy: error" exits 0 and gives no reason.

**Change:**
- Make bare `repose notify` print the settings, with `--json`.
- Show `ntfy: off`, or `ntfy: failed: <status or reason>`.
- Exit 1 if any channel that is on fails.
- In `notify --help`, say that agents send with repose-notify.

#### G6. The run warning about new laptop work scrolls behind tmux

**Severity:** low

**Evidence:** run.go:648 and run.go:661 (`laptopAheadLine`). I-367 decides that `run` attaches without a sync.

**Problem:** The morning loop is `repose run`, then Ctrl-b d, `repose sync` and `repose attach`. The CLI prints the warning, and then tmux covers it.

**Change:** Keep I-367. On a TTY, print the line and wait for Enter, or ask `Sync first? [y/N]`. Add no new noun.

#### G7. `attach` refuses a stopped machine

**Severity:** low

**Evidence:** run.go:210-218: `if project.State != "running" { return notRunningError(project) }`. run-and-attach.md:101.

**Problem:** After a night, the machine is stopped, so `repose attach` fails and the user types `repose run`. The two verbs overlap.

**Change:**
- Make `attach` start a stopped machine. It still does not sync.
- Keep exit 5 for states that cannot start.
- Update run-and-attach.md:101 in the same commit.

#### G8. `repose login` prints a URL and opens no browser

**Severity:** low

**Evidence:** login.go:41-45. cli.go:268 and cli.go:285-286. I-101 makes the device code the default.

**Problem:** On the first command, the user must copy the URL by hand.

**Change:**
- With a display or on macOS, open the verification URL with the code in it, and still print it.
- Hide `--browser`.
- If `repose run` finds no login on a TTY, start the device flow inline.

#### G9. Fifteen `repose-*` helpers are on the machine's PATH

**Severity:** low

**Evidence:** `ls /run/current-system/sw/bin | grep ^repose-` lists 15 entries. nix/guest/base/agent-guide.md and your-chrome.md send agents to `repose-guest-profile browser bridge status`.

**Problem:** Four of the helpers are for users. The bridge check sends agents into a script that can also start and stop the desktop.

**Change:**
- Move the plumbing to libexec. Keep repose-ask, repose-notify, repose-checkout and repose-mcp on PATH.
- Show the bridge state through one of them, or through a file such as ~/.repose/bridge.

## Proposed order of work

1. Split `isTerminal` into spinner and prompt checks, and make a declined confirmation exit nonzero (A1).
2. Make `repose sync` refuse outside a git repository before it creates a machine (B1).
3. Check for unfetched work before the CLI destroys a temporary machine, and add its `repose` remote (A2).
4. Make `reply` check the first word against all projects (B2).
5. Parse `--since` in the CLI, and make the API refuse bad values (B5).
6. Map offline and discovery errors to a network message with exit 1, and keep exit 3 for login (E1).
7. Fix the `run` Short and Long and humanize.go:162 (C1).
8. Set `SetInterspersed(false)` on `run`, and remove "quoting is optional" (B3).
9. Exit nonzero for `run --no-attach` without a Claude login, and keep the prompt (B4).
10. Fix the backtick placeholders at cli.go:1112 and cli.go:1355, and add the UnquoteUsage test (C2).
11. Replace "guest", "environment" and "fragment" in user-visible strings, and add the grep test (C4).
12. Replace the 11 cobra validators with `gotArgs` messages that name the command's help (D2).
13. Refuse `run --size` on an existing project (B6).
14. Map op capacity failures to exit 8, and remove the double retry sentence (E2).
15. Unify the restore flags (`--as`, `-y`), and name the project and snapshot in the in-place prompt (D3, A4).
16. Ask before `stop` ends a busy agent, after a decision entry that revises I-500 (A3).
17. Classify ssh errors in `waitForSSH`, and stop at once on laptop-side faults (E5).
18. Make `-v` log requests, statuses and request IDs (E4).
19. Give one sentence per HTTP status class, and retry idempotent GETs once (E3).
20. Handle Ctrl-C at prompts and during long waits with specific lines (A5, A6).
21. Add `--json` to the commands that change state, and print NDJSON for streams (F1).
22. Add a CLI version check against the API (G1).
23. Add command groups to `repose --help` (C6).
24. Run the docs_test ghost check over all docs pages (C7).
25. Do the rest of the medium findings: D1, D4-D6, B7, C3, C5, E6, F2, G2-G5.
26. Do the low findings as polish.