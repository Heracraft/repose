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
	Reason    string // manual | run | stop | scheduled
	TakenAt   time.Time
	Bytes     uint64 // exclusive bytes this point holds, 0 when unknown
}

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
	// Rewind replaces the live area with the point.
	// It refuses with ErrBusy unless the live area is quiesced, and it
	// takes a point of the current state first, so a rewind is undoable.
	Rewind(ctx context.Context, project, name string) error
	// Fork copies a point into a new project's live area.
	Fork(ctx context.Context, srcProject, name, newProject string) error
	// Delete removes a point. Absence is success.
	Delete(ctx context.Context, project, name string) error
	// Space reports the store's bytes.
	Space(ctx context.Context) (Space, error)
	// GC deletes the oldest points until the store is under the high-water
	// mark, never deleting the newest point of any project. Returns what went.
	GC(ctx context.Context, highWaterPercent int) ([]Point, error)
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
- `GC` never deletes a project's newest point. btrfs has no honest free
  figure from `df`; use `btrfs filesystem usage --raw` and treat an
  allocation failure as `ErrNoSpace`.
- `Fork` is a writable snapshot of a point: 0 s and no copied bytes.

## Contract 2: the machine record

In `internal/machine`. The registry is `~/.config/repose/machines.toml`
(0600), beside the existing config. No api, no database.

```go
type Machine struct {
	Name     string // the user's name for it, [a-z0-9-], unique
	Address  string // host or IP as ssh reaches it
	Port     int    // 0 means 22
	Login    string // the ssh user: the box's own owner, not "dev"
	KeyPath  string // the laptop key authorised on the box
	UndoPath string // the store's mountpoint on the box
	AddedAt  time.Time
}

type Registry interface {
	List() ([]Machine, error)
	Get(name string) (Machine, error)
	Add(m Machine) error      // refuses a duplicate name
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
repose-agent point list    --project SLUG {"points":[{...}]}
repose-agent point rewind  --project SLUG --name N              {"rewound":true,"safety_point":"..."}
repose-agent point fork    --project SLUG --name N --as SLUG2    {"path":"..."}
repose-agent point delete  --project SLUG --name N              {"deleted":true}
repose-agent gc            --high-water 85                      {"deleted":[{...}],"space":{...}}
repose-agent space                        {"total":...,"used":...,"free":...}
repose-agent quiesce       --project SLUG {"stopped":["tmux:todo-app","claude"],"busy":[]}
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
repose rewind [PROJECT] [--to POINT] [--list] [--yes]
```

Output, in the house voice — what is true, once, and nothing else
("Trust the reader"; no next-command lines, no footers):

```
$ repose machine ls
NAME     ADDRESS           UNDO
homebox  me@192.168.1.40   12.4 GB used of 40 GB

$ repose rewind --list
POINT             TAKEN             REASON   SIZE
r-0199a1c2        14 minutes ago    run      104 MB
r-0199a0f1        2 hours ago       manual   2 MB

$ repose rewind
Rewind todo-app to the point taken 14 minutes ago? Everything written since is lost. [y/N] y
Stopped 2 processes. Rewound todo-app (2.1s).
```

A `no` exits 1. `--yes` skips the question. `--to` names a point. With no
points, `rewind` exits 5 and says so. Exit codes follow the existing
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

Each item names the evidence that closes it. "Tests pass" closes nothing
(`../CHECKLIST.md`).

- [ ] `internal/undo` btrfs backend: `go test ./internal/undo/...` green
      against a **real** btrfs mount, with the measured figures in the
      test output — a point under 0.5 s, a fork under 0.5 s, a rewind of a
      30k-file tree under 5 s. Paste the timings.
- [ ] `Rewind` refuses a busy project: a test that holds an open fd under
      the live area and asserts `ErrBusy`, and a test that proves the
      pre-rewind state is still live after a failed rewind.
- [ ] `GC` never deletes a project's newest point: a test with a full
      store asserting the newest survives and the oldest went.
- [ ] `internal/machine`: `SSHConfig` golden file, and `ssh -G
      homebox.repose` resolving to the machine's address with the
      generated config (not the gateway's).
- [ ] `cmd/repose-agent`: every subcommand's JSON shape in a golden test,
      and the error object for each of the six codes.
- [ ] The installer runs twice against a box and changes nothing the
      second time: paste both runs' stdout.
- [ ] End to end on a real box: `machine add`, `run`, an agent writes
      something, `rewind`, the file is back. Paste the transcript.
- [ ] `apps/web/src/content/docs/` carries `machine` and `rewind` in the
      same commit, and `internal/cli/docs_test.go` is green (it fails when
      the CLI and `cli.md` disagree).
- [ ] `docs/DECISIONS.md` has I-812, I-814 and I-816 written, and
      `ops/dev/decisions-index.py` regenerated.
- [ ] `go build ./...` and `just lint` green, with no `TODO`, no
      `not implemented`, and no unregistered command
      (`../CHECKLIST.md`'s greps).
