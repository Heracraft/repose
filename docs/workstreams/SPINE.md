# The wrapper-only spine (workstream, 2026-10-19)

The vertical slice of `../proposals/2026-10-19-wrapper-only.md`: a project
on a plain Linux machine the user owns, reached by ordinary SSH, with undo
on btrfs subvolumes. Ids reserved: **I-812** (the shape), **I-814** (the
undo mechanism), **I-816** (the commercial model).

## What this builds, and what it deliberately does not

A user can do exactly this when the spine is done:

```
$ repose machine add homebox --ssh me@192.168.1.40
Installing on me@192.168.1.40 ...
✓ Checked the box (Ubuntu 24.04, x86_64, systemd)    1.2s
✓ Provisioned 40 GB of undo space (btrfs)            0.4s
✓ Installed the agent and 5 coding agents            48s
Added homebox.

$ cd ~/code/todo-app && repose run -p "finish the auth flow"
...                                   # syncs the dirty checkout, starts the agent in tmux

$ repose rewind
Stopped todo-app's agents. Rewound to the point taken 14 minutes ago (2.1s).
```

**Not in this pass, and why.** The api, billing, the `small|large|xl`
class enum, the scheduler and the plan gates are untouched. §6 of the
proposal puts self-host first — no billing, no seats, no relay, no card —
and the subsystem map found the api edit is the widest blast radius in the
repo: the class enum is pinned by migrations, protos, the dashboard and
the landing; `billing/gate.go:163-197` is the whole of what a
subscription currently buys; and `projects_guest_ip_live` was added after
an incident. None of that should move before a user has walked this path
once. **So the spine has no control plane at all**: the machine registry
is a file on the laptop, and the CLI talks straight to the box over SSH.

**hostd, the guests, the gateway and guestd are not edited.** The hosted
product keeps working byte-identically; this is new code beside it. That
is also what keeps the tree compiling while five agents work at once.

## The pieces

| Package | New? | What it owns |
|---|---|---|
| `internal/undo` | new | the undo backend interface and its btrfs implementation |
| `internal/machine` | new | the machine record, its on-laptop registry, SSH config rendering |
| `cmd/repose-agent` | new | the on-box command the CLI invokes over SSH, JSON on stdout |
| `internal/cli` (spine files only) | edits | `repose machine`, `repose rewind`, and run/attach against a machine |
| `ops/install/machine-install.sh` | new | the one-command install over SSH |

## Contract 1: the undo backend

Derived from the existing call sites in `internal/hostd/guest/snapshot.go`
and `internal/api/ops/phases.go`, so a later pass can put the hosted path
behind the same interface. In `internal/undo`:

```go
// Point is one rewind point: a fixed moment of a project's files.
type Point struct {
	Name      string // stable, unique, derived from the caller's id — never a wall clock
	Project   string // project slug
	Reason    string // manual | run | stop | scheduled | rewind
	TakenAt   time.Time
}
// No size field: btrfs reports a snapshot's bytes only through qgroups,
// which means quotas on, and quotas on a store of thousands of
// snapshots cost every write — so the figure was 0 on every real box.

type Space struct{ Total, Used, Free uint64 }

// Backend is the undo mechanism for one machine.
// Every operation is idempotent: a repeat of one that already happened
// succeeds without changing anything.
type Backend interface {
	// Provision prepares the store. Safe to call on an already-prepared one.
	Provision(ctx context.Context, sizeBytes uint64) error
	// Init creates the project's live area. Returns its path.
	Init(ctx context.Context, project string) (string, error)
	// Take fixes the project's current state as a point.
	Take(ctx context.Context, project, name, reason string) (Point, error)
	// List returns the project's points, newest first.
	List(ctx context.Context, project string) ([]Point, error)
	// Rewind replaces the live area with the point and returns the name
	// of the point it took of the state it replaced, whose reason is
	// "rewind", so a rewind is undoable and nothing selects that point
	// as a target. It refuses with ErrBusy unless the live area is
	// quiesced.
	Rewind(ctx context.Context, project, name string) (string, error)
	// Fork copies a point into a new project's live area.
	Fork(ctx context.Context, srcProject, name, newProject string) error
	// Delete removes a point. Absence is success.
	Delete(ctx context.Context, project, name string) error
	// Space reports the store's bytes.
	Space(ctx context.Context) (Space, error)
	// GC deletes the oldest points until the store is under the high-water
	// mark, within three floors: a project's newest MinPointsPerProject
	// points stay, a point inside MinPointAge stays, and a delete that
	// frees no bytes stops the loop. Returns what went and what it would
	// not touch.
	GC(ctx context.Context, highWaterPercent int) (GCResult, error)
}

const (
	MinPointAge         = 6 * time.Hour
	MinPointsPerProject = 3
)

type GCResult struct {
	Deleted []Point // oldest first
	Young   int     // left because they are inside MinPointAge
	Kept    int     // left because their project is at its floor
	Stalled bool    // a delete freed nothing, so the loop stopped
}

var (
	ErrNotFound = errors.New("no such point")
	ErrBusy     = errors.New("the project is not quiesced")
	ErrNoSpace  = errors.New("not enough space for a point")
	ErrExists   = errors.New("the target project already exists")
)
```

Rules the implementation must hold, each measured in
`../research/wrapper-undo.md`:

- A point is a read-only subvolume snapshot: 0.13 s and 32 KiB, and
  afterwards it costs only the blocks that change.
- `Rewind` must stop nothing itself and refuse when the live area is
  busy. A writer with its cwd inside the subvolume survives the swap and
  goes on writing where nothing can reach it; quiescing is the caller's
  job and the backend verifies it (no open fds under the subvolume, via
  `/proc/*/fd` or `fuser`).
- `Rewind` takes a point of the current state before swapping, and swaps
  by rename, so a failure leaves the pre-rewind state live.
- `GC` works to the floors above: without them an agent that fills the
  store in ten minutes takes the morning's points with it, which is the
  one way a collection turns a full store into a full store with no undo.
  btrfs has no honest free figure from `df`; use `btrfs filesystem usage
  --raw` and treat an allocation failure as `ErrNoSpace`. On an
  image-backed store the figures are bounded by the filesystem the image
  lives on: a sparse file promises space the box may not have.
- `Fork` is a writable snapshot of a point: 0 s and no copied bytes.

## Contract 2: the machine record

In `internal/machine`. The registry is `~/.config/repose/machines.toml`
(0600), beside the existing config. No api, no database.

```go
type Machine struct {
	Name     string // the user's name for it, [a-z0-9-], unique
	Address  string // host or IP as ssh reaches it, resolved when it was added
	Port     int    // 0 means 22
	Login    string // the ssh user: the box's own owner, not "dev"
	KeyPath  string // the laptop key authorised on the box
	// From the user's own ~/.ssh/config, read once with `ssh -G` when the
	// machine is added: the blocks are read with -F, which replaces that
	// config, so the hops have to be spelled out here or they are lost.
	ProxyJump    string
	ProxyCommand string
	Spelled      string // the --ssh value as the user wrote it, when it was an alias
	UndoPath     string // the store's mountpoint on the box
	AddedAt      time.Time
}

type Registry interface {
	List() ([]Machine, error)
	Get(name string) (Machine, error)
	Add(m Machine) error      // refuses a duplicate name
	Update(m Machine) error   // the same name at a new address moves it
	Remove(name string) error // absence is success
	// SSHConfig renders ~/.ssh/repose/machines, one Host block per machine,
	// in name order. Included by ~/.ssh/repose/config like the gateway blocks.
	SSHConfig(prefix string) string
}
```

The SSH block is an ordinary one — no certificate, no gateway:

```
Host homebox.repose
  HostName 192.168.1.40
  User me
  IdentityFile ~/.ssh/id_ed25519
  IdentitiesOnly yes
  ForwardAgent no
  ServerAliveInterval 30
  ControlMaster auto
  ControlPath ~/.ssh/repose/cm-%C
  ControlPersist 10m
```

`gatewayHost` and `renderSSHConfig` in `internal/cli/cert.go` are **not
touched**: a machine's blocks are a separate file with a separate Include,
so a laptop can hold hosted projects and owned machines at once.

**The login user is the box's own user, not `dev`.** CLAUDE.md fixes
`dev` as the guest user; a machine the user owns has no guest and already
has an owner. This is a deliberate difference and I-812 records it.

## Contract 3: the on-box command

`cmd/repose-agent`, installed at `/opt/repose/bin/repose-agent`. The CLI
invokes it over SSH and parses JSON from stdout; human-readable text never
goes to stdout. Every subcommand prints one JSON object and exits 0, or
prints `{"error":{"code":"...","message":"..."}}` and exits non-zero.

```
repose-agent provision --size-gb N        {"store":"/var/lib/repose/undo","fs":"btrfs","created":true}
repose-agent project init  --project SLUG {"path":"/var/lib/repose/undo/projects/SLUG","created":true}
repose-agent point take    --project SLUG --name N --reason R   {"point":{...}}
repose-agent point take    --reason R          # every project  {"points":[{...}]}
repose-agent point list    --project SLUG {"points":[{...}]}
repose-agent point rewind  --project SLUG --name N              {"rewound":true,"safety_point":"..."}
repose-agent point fork    --project SLUG --name N --as SLUG2    {"path":"..."}
repose-agent point delete  --project SLUG --name N              {"deleted":true}
repose-agent gc            --high-water 85                      {"deleted":[{...}],"young":N,"kept":N,"stalled":false,"space":{...}}
repose-agent space                        {"total":...,"used":...,"free":...}
repose-agent quiesce       --project SLUG {"stopped":["tmux:todo-app","claude (pid 4242)"],"busy":[]}
repose-agent holders       --root PATH    {"holders":[{"pid":4242,"comm":"claude"}]}
repose-agent status                       {"machine":{...},"projects":[...],"space":{...}}
```

Error codes, reusing the vocabulary of `docs/interfaces/README.md`:
`invalid_argument`, `not_found`, `busy`, `no_space`, `exists`, `internal`.

`quiesce` is what makes a rewind safe: it kills the project's tmux session
and any process whose cwd is under the live area, then reports what it
stopped. `point rewind` calls the same check and refuses with `busy` if
anything is left.

## Contract 4: the CLI surface

New nouns only; nothing existing changes its output.

```
repose machine add NAME --ssh [user@]host[:port] [--key PATH] [--undo-gb N]
repose machine ls
repose machine rm NAME
repose rewind [PROJECT] [--to POINT] [--list] [--rm POINT] [--yes]
```

Output, in the house voice — what is true, once, and nothing else
("Trust the reader"; no next-command lines, no footers):

```
$ repose machine ls
NAME     ADDRESS           UNDO
homebox  me@192.168.1.40   12.4 GB used of 40 GB

$ repose rewind --list
POINT             TAKEN             REASON
r-0199a1c2        14 minutes ago    run
r-0199a0f1        2 hours ago       manual

$ repose rewind
Rewind todo-app to the point taken 14 minutes ago? Everything written since is lost. [y/N] y
Stopped 2 processes. Rewound todo-app (2.1s). The state it replaced is the point r-0199a1ff.
```

A `no` exits 1. `--yes` skips the question. `--to` names a point, and
`--rm` deletes one and rewinds nothing. With no points, `rewind` exits 5
and says so. Exit codes follow the existing
table: 1 refused, 5 wrong state, 7 payment (unused here).

Where a project lives is recorded on the laptop beside the machine
registry: `~/.config/repose/projects.toml` maps a checkout's remote (or
directory) to `{machine, slug}`, which is the same resolution order
`internal/cli/project.go:244` already uses, minus the api.

## Contract 5: the installer

`ops/install/machine-install.sh`, run by `repose machine add` over SSH
with the box's package manager detected. In order, and idempotent at every
step (a second run changes nothing):

1. Check the box: `x86_64`, systemd present, a login user with sudo, and
   a kernel with btrfs (`modprobe btrfs`). Refuse with one sentence
   naming what is missing.
2. Install from the distro: `btrfs-progs`, `tmux`, `git`, `curl`, `jq`,
   `rsync`, `python3`.
3. Provision the undo store: a sparse file at `/var/lib/repose/undo.img`
   (or `--device`), `mkfs.btrfs`, a mount unit with
   `noatime,compress=zstd:1,user_subvol_rm_allowed` so the login user can
   delete its own points without sudo, and `projects/` and `points/`
   subvolumes.
4. Install `repose-agent` at `/opt/repose/bin/repose-agent`, verified
   against a pinned sha256.
5. Install the five coding agents from `nix/overlay/agents/versions.json`
   — the same pins, the same sha256 checks, no nix — into
   `/opt/repose/bin`, with the wrapper body from
   `nix/overlay/agents/wrap.nix` extracted to a plain script.
6. Write `/etc/repose/machine.json`: the machine's name, its id and the
   store's path.
7. Print one line per step on stderr, `{"ok":true,...}` on stdout.

A box behind NAT is the user's own problem in this pass: the installer
takes whatever address ssh takes, Tailscale included. The proposal's §6
says brokering that is the only thing a subscription will sell, and it is
not built here.

## Checklist

Closed 2026-10-19/20, except where the box is empty. Each line is the
evidence, not a claim.

- [x] **`internal/undo` against a real btrfs store.** `REPOSE_UNDO_TESTFS=/mnt/nixstore/repose-undo-testfs
      go test ./internal/undo/` green, on a 30,002-file tree with a 32 MB
      database: **point 8 ms, fork 3 ms, rewind of 30,000 files 158 ms**,
      against the checklist's bounds of 0.5 s, 0.5 s and 5 s. The store's
      figures come from `btrfs filesystem usage --raw`
      (4294967296 total, 207233024 used).
- [x] **`Rewind` refuses a busy project, and a failed rewind leaves the
      pre-rewind state live.** `TestRewind*` in `internal/undo/btrfs_test.go`
      hold the ordering (safety point before the swap) and `ErrBusy`; the
      integration test wrecks 498 files and a database and asserts all
      30,000 back with the database byte-identical.
- [x] **`GC` never deletes a project's newest point.** Table test, plus the
      integration test's `gc --high-water 1`: one point deleted, the newest
      left.
- [x] **Two real bugs the run found, both fixed with the measurement in
      I-814.** A read-only point cannot be deleted by the login user until
      its `ro` flag is cleared, which broke all retention without root; and
      a second-truncated timestamp made `List` return points in name order
      rather than newest first.
- [x] **`internal/machine`.** Golden files `testdata/machines` and
      `machines-home` regenerated and green; the blocks carry
      `UserKnownHostsFile` and `StrictHostKeyChecking accept-new` (I-812).
      `ssh -F ~/.ssh/repose/machines spinebox.repose` is the transport every
      command below used.
- [x] **`cmd/repose-agent`.** Golden test per subcommand's JSON shape and per
      error code; `go test ./cmd/repose-agent/` green.
- [x] **The installer runs four times against a box and changes nothing after
      the first.** On Debian 12 (bookworm) over ssh: run 1
      `"changed":true`, runs 3 and 4 `"changed":false`, with every step's
      line in the "already done" wording. `systemctl is-enabled
      var-lib-repose-undo.mount repose-undo-gc.timer` → `enabled enabled`;
      `findmnt` → `btrfs rw,noatime,compress=zstd:1,...,user_subvol_rm_allowed`.
      Four of the five agents installed from `versions.json`'s own pins and
      hashes (claude-code 2.1.283, opencode 1.18.34, codex 0.157.1,
      pi-coding-agent 1.0.4); gemini-cli has no x86-64 Linux build, is named
      and skipped.
- [x] **End to end on a real box.** `repose machine add spinebox --ssh
      me@127.0.0.1:2299` → `Added spinebox.`; `repose machine ls` → `352 KB
      used of 4 GB`, read from the box. In a checkout with an uncommitted
      edit, an untracked file and an untracked database: `repose run
      --machine spinebox -d -p "say hello"` → `✓ Synced 3 files`, `✓ Took a
      rewind point`, `claude is running in todo-app on spinebox.` The box's
      copy then held the uncommitted edit, both untracked files and a
      `git status` identical to the laptop's. An agent then deleted `src`,
      overwrote the database and removed `.gitignore` (4 files to 1);
      `repose rewind --yes` → `Stopped 1 process. Rewound todo-app (0.0s).`
      and all four files were back with the database's contents intact.
      Without a terminal `repose rewind` exits 2 naming `--yes`.
- [x] **User docs ship in the same commit.** `apps/web/src/content/docs/own-machine.md`
      and the `machine`/`rewind`/`--machine`/`--agent-url` entries in
      `cli.md`. `go test ./internal/cli -run TestDocs` green — it is the
      test that refused `--machine`, `--agent-url` and `--agent-sha256`
      until they were documented.
- [ ] **The docs say what this pass does, not what the first draft did.**
      `own-machine.md` corrects the two claims the 2026-10-20 audit found
      false — the sync carries the gitignored `.env` and `.env.*` files, not
      "the files git ignores", and a `~/.ssh/config` host name is resolved
      when the machine is added, so a later edit to that entry does not
      follow — and adds "Which commands work on it", "What the box does not
      get", "What an agent on it can reach", the `/etc/sudoers.d/repose`
      drop-in line, the point cadence (every run, every sync, every 30
      minutes) and the hourly collection at 85 percent. `cli.md` gains
      `--rm` on `rewind` and the owned-machine clause on `attach`, `sync`
      and `ps`, and loses the per-point size claim with the SIZE column.
      `SECURITY.md` has the no-hypervisor entry. Closes with
      `go test ./internal/cli -run TestDocs` green, which is also what
      proves every flag these lines name landed.
- [x] **Decisions written.** I-812, I-814 and I-816 in `DECISIONS.md`, each
      amended with what building it taught, and `DECISIONS-INDEX.md`
      regenerated.
- [x] **Build, lint and the checklist greps.** `go build ./...` green;
      `golangci-lint run` on the new and changed packages → `0 issues`;
      `gofmt -l` empty; no `TODO`, `FIXME`, `not implemented`, `_ = err` or
      bare `panic` in the new code; both nouns answer `--help`.

## Second pass: the audit, and what it closed (2026-10-20)

An audit asked whether this shape delivers what it is sold on — one command
turns your laptop's working state into a machine an agent can wreck safely —
and rated 27 of its findings as losing the user. The verdict was that the two
differentiators worked and almost nothing around them did. What closed, with
the evidence from a real Debian 12 box over ssh:

- [x] **The agent can work.** The run passed no carry at all, so no login, no
      git identity and no `.env` reached the box. Measured on the box after a
      run: `.env arrived: DATABASE_URL=postgres://localhost/dev`; `git
      identity on the box: a@b`; the gh login with
      `url.https://github.com/.insteadof git@github.com:` and
      `credential.https://github.com.helper !gh auth git-credential`; and an
      agent commit that succeeded (`a9c4a41 the agent commits`). The MCP
      servers are deliberately not carried — the part that renders them into
      an agent's config does not exist on such a box.
- [x] **A missing Claude Code login is caught before the prompt is typed.**
      Seen on the box: `Claude Code is not logged in on todo-app, so your
      prompt was not typed. Log in with \`repose attach todo-app -w claude\`,
      then run your prompt again.`
- [x] **There is a way back.** `attach`, `sync`, `ps`, `ls` and `status` reach
      an owned machine; the fourteen nouns that cannot say so in one sentence
      and exit 5. `repose ps` against the box prints its tmux listing
      (`0:bash* bash - /var/lib/repose/undo/projects/e2e-033148 now`).
- [x] **A way home for the work.** `Added the git remote repose: \`git fetch
      repose\` brings the machine's commits to this checkout.`
- [x] **A rewind is reversible and says so.** `Stopped 1 process. Rewound
      e2e-033148 (0.0s). The state it replaced is the point r-01a1575d85617c25830ac225.`
      The safety point carries reason `rewind`, so a second rewind cannot
      restore the wreck the first one escaped — asserted in the harness.
- [x] **A point is taken by `sync` too, and every 30 minutes** from a timer
      the installer enables, where before a six-hour session had one point.
- [x] **The store cannot eat itself or the disk.** `gc` stops when a delete
      frees nothing, never collects a point younger than 6 hours and keeps 3
      per project; the installer refuses an `--undo-gb` the backing
      filesystem cannot hold; `discard=async` returns a deleted point's
      blocks to the sparse image.
- [x] **No column of zeroes.** `rewind --list` no longer prints a SIZE it
      cannot know (btrfs quotas are off, and `qgroup show` needs root).
- [x] **An address can change**, a `~/.ssh/config` alias and a `ProxyJump`
      work as the address, and a failure says which it was rather than
      blaming a gateway that does not exist on this path.
- [x] **A box's tmux is not the guest's.** tmux 3.3a prints `_` for a tab in
      a format string, so the listing came back as one field per row. The
      spine asks for `|~|`; the hosted format is untouched. Found by running
      it, recorded in I-812.
- [x] **The harness refuses rather than filling a shared disk.**
      `ops/dev/spine-e2e.sh` builds a throwaway Debian box, installs, runs,
      wrecks and rewinds in one command, and it now checks free space first
      (it filled this machine's root disk once) and can point at a box you
      already have instead of making one.

Build, lint and tests after the pass: `go build ./...` green, `golangci-lint
run` on every new and changed package → `0 issues`, `gofmt -l` empty, every
package's tests green except a pre-existing `internal/guestd` failure that
also fails on untouched `main` when this box's root disk is above 90 percent
(`TestHerdrProjectWarnsHerdrDown`, "warning \"disk_high\" before
SetupProject").

## Third pass: the toolchain, and both options (2026-10-20)

The owner: "we need to make sure its a good experience to bring your own
hardware + repose having a hand ... the promise of the repose product must be
fulfilled otherwise the product is useless." The promise broke at the
toolchain: the box had no node and no python, so `repose run` synced the code,
started the agent, and `npm run dev` failed. What closed, with the evidence
from the same Debian 12 box (`ops/dev/spine-e2e.sh`, exit 0):

- [x] **The project runs.** The run resolves the project's own pin and installs
      it on the box: `✓ Installed nodejs_22  0.2s`, and then on the box
      `node --version` answers **v22.23.2** and `node -e` prints. Nix in
      single-user mode, a shared store at `/nix`, and the project's profile
      inside its own btrfs subvolume so a rewind takes an added package back
      out (I-832). `repose config add|show` work there;
      `repose config remove` does not, and says the rewind is the way back.
- [x] **The repository is licensed.** It was public with no LICENSE at all,
      which is all rights reserved: there was no free option in the only sense
      that matters. Apache-2.0, verified byte-identical to a pristine copy
      apart from the copyright line (I-830).
- [x] **Both options are written down.** `apps/web/src/content/docs/hosted-or-your-own.md`
      says what the free self-hosted option is and what a hosted account adds,
      as services and not gates, with no price (I-836: the price waits on
      I-834's measurement). The README is the funnel, with the real transcripts
      and a "What this does not do yet" section.
- [x] **The hosted option's reachability is designed, not built.** I-834: a
      byte-splice relay on the edge, the box dialling out with a certificate it
      traded a join token for, direct-first connection selection, and the
      WireGuard hub deliberately refused. The price waits on
      `relay_bytes_total`, which the gateway has been counting since it
      shipped and nothing has ever read.
- [x] **The scan stops lying about an owned box.** `baseCommands` promised the
      hosted guest's base (make, gcc, psql, docker) on a box that has none of
      it; it is split, and an owned machine's listing is that box's own answer
      to a `command -v` sweep. A test now ties the hosted half to
      `nix/guest/base/tool-list.nix`, which is how it drifted (it had lost
      `yarn` and `yarnpkg`).
- [x] **repose's own records cannot be committed by accident.** The records
      live in the project's subvolume so a rewind takes them, and that
      subvolume is the user's checkout, so `.repose/` showed as untracked in
      their repository. `project init` and every `point take` add it to
      `.git/info/exclude` — the repository's own private ignore list, which is
      not a tracked file and never travels. Found by reading the e2e's own
      `git status` output.
- [x] **Two bugs the tests caught in the new code.** A failed tool install was
      recorded but invisible, because `plan` short-circuited on the marker a
      pass writes; and an absent `nix` read as "nix is on the box but exited
      1", because a shell reports a missing command as exit 127 with "command
      not found" and the check matched neither.
- [x] **The harness refuses to wedge or to fill the disk.** It filled this
      machine's root disk once and later hung `up` by backgrounding a file
      server inside a pipeline; it now checks free space first, detaches the
      server with `setsid`, binds `/nix` to the data disk, and can point at a
      box you already have (`SPINE_E2E_SSH`).

Still true, and said in the docs rather than implied: a database is the user's
to run; anything installed outside the project's subvolume is outside what a
rewind undoes; and repose does not run the project's own `npm ci` or `uv sync`,
because that executes the project's lockfile scripts on a box where the agent
already has root.

## What is not done

Named here rather than left for someone to discover:

- **No api, no billing, no dashboard.** Deliberate
  (§"What this builds"), and it is what §6's self-host-first order asks
  for. A machine the user owns is invisible to the dashboard, and
  `repose snapshots`, `resize`, `stop`, `start`, `rm`, `config`,
  `secrets`, `browser`, `logs` and `reply` are a hosted machine's
  lifecycle and its server-side state, so none of them reaches such a
  box. The public docs say which commands do (own-machine.md, "Which
  commands work on it").
- **`attach` was not exercised end to end**, because it needs a terminal;
  `run -d` and `rewind` were. The attach path is `attachTmux`, the hosted
  path's own function, with the machine's target.
- **The release path for `repose-agent` is untested.** `.goreleaser.yaml`
  now builds and archives it, but no tagged release carries the asset yet,
  so every run above used `--agent-url` with `--agent-sha256`. The first
  release that publishes it proves the default path.
- **Agent logins on the box are each agent's own.** The `.env` files a
  checkout ignores travel with a sync; nothing carries a Claude login or a
  `gh` login to a machine the user owns, and `repose secrets` was not
  wired into this path.
- **The project's own toolchain is the user's to install.** The installer
  brings what the agents need and no language runtime, package manager or
  database. §7 item 6 of the proposal — Nix single-user with a profile per
  project — is the answer and is not built. The docs page says so
  ("What the box does not get") rather than leaving a first-timer to find
  out on the box.
- **One tenant, one trust boundary.** There is no hypervisor between the
  agent and the box, by design (I-812): an agent running with
  `--dangerously-skip-permissions` on a machine the user owns reaches
  everything that login reaches, the rewind points included. Recorded,
  not mitigated: `SECURITY.md`'s entry under "Not mitigated in the first
  release", and own-machine.md's "What an agent on it can reach".
- **macOS**, which has neither btrfs nor the systemd units the installer
  writes. The installer refuses it by its box check.
