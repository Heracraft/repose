# Projects

A project is one guest, its volume, its snapshots, its config, and its meter
rows, owned by one user. The CLI decides which project a command means from
the directory it runs in.

## What the user sees

```
$ cd ~/code/todo-app
$ repose run
✓ Created todo-app (large)  4s
...
```

```
$ cd "~/Downloads/job search"
$ repose run
✓ Created job-search (large)  4s
Not a git repository, so nothing was synced.
...
```

```
$ repose run todo-app-experiment
✓ Created todo-app-experiment (large)  4s
...
```

(The class is `--size`, or `default_class` from `config.toml`, `large`
unless set. On a terminal the phase line is a spinner that becomes the ✓
line; elsewhere it prints `Creating todo-app...`.)

## Behaviour that must hold

Identity:

- The CLI reads `git remote get-url origin` and normalises it as
  `interfaces/cli-config.md` says. `git@github.com:A/B.git` and
  `https://github.com/a/b` resolve to the same project.
- A directory with a remote and no PROJECT maps to the project keyed on
  `(user, remote)`. The first `run` creates it; every later `run` finds it.
- A project named on `run` in a directory with no remote remembers the
  repository (or directory) it was created from (`projects.json` `by_dir`),
  so a later `run` there without a name finds it. That memory is only
  trusted while the project's remote matches the directory's (both empty
  for such a project), and naming a project explicitly (`repose attach
  izma`, `--project`) never writes it, so one checkout can never be sent
  to another checkout's guest (DECISIONS I-152). One exception: a sync
  (`repose sync PROJECT`, `--project`, or a run's first sync) into a
  project with no remote, from a directory with no remote and no `by_dir`
  entry, writes `by_dir` for that project, since the directory's work is
  now in it (DECISIONS I-575).
- `repose run NAME` and `repose sync NAME` mean the project called NAME
  (`--name NAME` before I-603, hidden for a release; by name,
  or by the slug NAME gets) wherever the command runs, and creates it when
  there is none; it never lands on a project of another name (DECISIONS
  I-348). A NAME that is another repository's project exits 2 instead of
  syncing one repository into the other's machine: by its remote, or,
  when either side has none, by history (I-631). When the laptop knows
  none of the commits the machine's checkout has refs to, one more ssh
  asks the checkout for the laptop's root commits; a checkout with none
  of them is another repository's (`job's checkout shares no commit with
  this one, so ...`), and nothing is written. A shallow repository on
  either side is not checked. A new NAME takes the
  checkout's remote only when no project has that remote yet; otherwise
  it is a second project for the repository, with no remote (as a fork's
  copies, I-254), reached by name, and the checkout's own project is still
  what a plain `run` there means. Its first sync sends the whole history.
- A plain `repose run` in a directory that is not a git repository and
  has a `by_dir` entry lands on that project, the last one `run` made
  there, and says so on stderr (`Using boxd, the machine last made in
  this directory.`). `repose run --temp` there always makes a new
  machine.
- `repose run` in a directory with no remote, no PROJECT and no
  `by_dir` entry creates a project named after the directory (the
  repository root's name inside a repository), characters outside
  `[A-Za-z0-9._-]` replaced by `-` (`job search` is `job-search`), with the
  usual `-2` retry on a taken name, and writes `by_dir` so the next plain
  `run` there finds it (DECISIONS I-358, replacing the exit 2 that asked
  for `--name`). A Ctrl-C after that create and before the run connects
  removes the `by_dir` entry again and names the project, which stays on
  the account (DECISIONS I-575). Any other command that finds no project exits 4 (`No
  repose project here, and this directory has no git remote. Name one:
  ...`).
- The home folder (the laptop's home directory, a folder above it, or a
  folder in a repository rooted at one of those) is no project's
  (DECISIONS I-601). Without an explicit project nothing resolves there:
  no `by_dir`, no `checkouts`, no remote lookup, and an old `by_dir`
  entry for it is deleted when read. A plain `run` exits 2 (`Your home
  folder is not a project. cd into one, or run `repose run NAME` or
  `repose run --temp`.`); other commands exit 4 asking for the name. `run NAME` and
  `run --temp` there sync nothing, write no `by_dir`, send no
  `remote_url` and add no `repose` git remote; `sync` and `run --on`
  exit 2. Its subfolders are ordinary directories.
- `repose rm` deletes every `by_dir` and `checkouts` entry naming the
  destroyed project (DECISIONS I-601).
- The project name becomes the slug: lowercase, `[a-z0-9-]`, other characters
  replaced by `-`, runs collapsed, 1 to 40 characters. `Todo App` and
  `todo-app` collide, and the CLI says so with the existing project's name.
- Nothing is ever written into the user's repository. No `.repose` file, no
  git config key, no hook. Evidence for a test: `git status` before and after
  `repose run` shows the same tree.
- `REPOSE_PROJECT` or `--project <id or slug>` overrides directory
  resolution for every command, so scripts can run `repose stop --project
  todo-app` from anywhere.

Limits:

- The plan's memory limits what runs at once and its disk what is kept
  (pricing.md). A plan sells no project count: an account may have 100
  projects, running or stopped, on every plan and without one, and an
  operator can raise that for one account with `repose-admin users limits
  HANDLE --projects N` (DECISIONS I-569). `POST /projects`, an as-new
  restore and a fork past it return `400 invalid` with `detail: {reason:
  project_limit, limit, projects, requested?}`, and every command prints
  `You have 100 of the 100 projects an account can have, running or
  stopped. Destroy one first.`
- `repose fork` makes N projects at once and is refused whole, before any
  is created, when N more would pass the cap (snapshots.md, "Forking";
  DECISIONS I-254).
- No seats gate on `POST /projects` since DECISIONS I-290: creating a
  project needs a plan (`payment_required`, `detail.reason =
  subscription_required`), and the seats question is answered at checkout.
  See "Seats and the waitlist" below.
- A user without a card on file cannot start a guest at all
  (`payment_required`, exit 7). Creating the project row is allowed so the
  dashboard can show it, but nothing boots.
- `repose resize [PROJECT] [DISK]` grows the disk (`80G`; disks never shrink).
  PROJECT is positional like every other command's (I-155); a lone
  argument that parses as a size is DISK (I-268). DISK needs a unit (M,
  G or T, with an optional B or iB); a bare number exits 2, and the CLI
  compares with `volume_bytes` before the POST: the same size is a no-op,
  a smaller one exits 2 (I-613).
  `repose resize --size small|large|xl` changes the class (DECISIONS
  I-260): the API accepts `class` on `PATCH /projects/:id` only while the
  project is stopped (`conflict` otherwise), so a running project is
  stopped without a snapshot (I-595), patched and started again after a y/N question
  (`--yes` skips it; no terminal and no `--yes` is exit 2). The same class
  is a no-op. A larger class is first asked of the plan from `/me` and
  the project list, before the question, and a size the plan can't run
  beside what runs now exits 7 with nothing stopped (I-610); a PATCH the
  gate still refuses starts the project again at its old class.
  `run --size` and `sync --size` on a stopped project of another size
  PATCH the class before starting it; on a running one they exit 2
  naming `repose resize` (I-611). StartGuest carries the class on every start, so the
  guest boots at the new vCPUs and memory and its samples, and so its
  billing, report the new class. The dashboard does not change a class.

Ownership:

- A project belongs to exactly one user. There is no sharing, no transfer, no
  team in the first release.
- Destroying a project keeps its row for usage history and keeps the last
  snapshot 30 days (see stop-start-destroy.md).

## Seats and the waitlist

A seat is 8 GB of memory that may run at once on the fleet; Solo holds one,
Plus two, Pro four (`docs/PRICING.md`, DECISIONS I-290, I-362). The fleet has as many seats
as its `ready`, undrained hosts have usable 8 GB blocks, or `SEATS_TOTAL`
when the operator set it. Seats are held by every subscription that is
`trialing`, `active` or `past_due` and by every waitlist invitation whose
72-hour hold has not run out. `POST /billing/checkout` needs the plan's
seats free; otherwise, and on `POST /billing/waitlist`, the user joins the
waitlist and gets `503 waitlisted` with `{position, joined_at, email}`. The
CLI prints the api's sentence as it is, `repose is full right now. You're
number N on the waitlist; we'll email you@example.com when there's a
seat.`, and exits 8. `GET /me` and `GET /billing` carry `waitlist:
{position, joined_at, invited_at, hold_until}` while the user holds a
place; `GET /public/seats` gives the landing page `{total, free,
waiting}`. Every minute the api invites the oldest waiting user while a
seat is free, one seat each and strictly in order; an invited user has 72
hours to choose a plan, after which the seat goes to the next person and
the user is back on the list, at the back, told by email. Every waitlist
email is transactional: sent whatever notify email says
(`features/notifications.md`, "Account emails"). Suspended, cancelled and
deleted accounts hold no place. `repose-admin waitlist list | admit HANDLE
| admit --next N` and `repose-admin seats` let an operator see and move
the queue (`ops/RUNBOOK.md`, "Waitlist growing").

## Depends on

Workstreams 05 (api projects routes, limits), 07 (cli resolution), 09 (limit
changes on first paid invoice).

## Deferred

Teams and shared projects (DECISIONS R5-6). Project transfer between users.
Renaming a project (the slug is baked into the tmux session name and the
SSH login; a rename is a destroy-and-restore until someone designs it).
