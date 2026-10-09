# 04: guestd

Milestone: M1. Owns `interfaces/vsock-guestd.md` jointly with 03-hostd (03
owns the hostd side and the sample shapes, 04 owns the guest side and the
hook socket). Consumes `interfaces/guest-conventions.md`. Consumed by
02-guest-base (installs it), 03-hostd (calls it), 13-notifications (its
events).

## 1. Goal

One small Go daemon inside every guest that gives hostd a way to freeze the
filesystem, switch the system, grow the disk, deliver secrets, set up the
project's tmux session, sample what is running, and relay agent hook events.
It listens on vsock only and has no network presence.

## 2. Scope: builds

- `cmd/guestd/main.go` and `internal/guestd/`: the server, one package per
  request family (`freeze`, `system`, `fs`, `secrets`, `ssh`, `project`,
  `sample`, `hooks`, `exec`).
- `proto/repose/guestd/v1/guestd.proto` and the generated Go, with the
  length-prefixed framing in `internal/vsockrpc/` (shared with hostd's
  client side: a `Conn` that reads a uvarint length then a message, writes
  the same, and multiplexes `request_id`).
- `cmd/repose-hook/main.go`: the tiny binary agent wrappers call from
  their hook config. Reads the agent's hook JSON on stdin, maps to
  `{agent, kind, summary}`, POSTs to `/run/repose/hooks.sock`, always exits
  0.
- A `--dev-socket /run/repose/guestd.sock` mode that serves the same
  protocol on a Unix socket for tests and for developer machines without
  vsock.
- `internal/fakes/guestd`: an in-process implementation of the protocol
  that hostd's tests use (records calls, returns canned samples).
- A NixOS VM test (`nix/guest/tests/guestd.nix`, run by 02's flake check)
  that exercises every request against the real binary inside a real
  guest.

## 3. Scope: does not build

- The NixOS unit that runs it, the tmpfs mounts, the user (02).
- The hostd side: connection management, CID assignment, retries (03).
- Deciding what to do with events (13) or samples (05, 09, 10). guestd
  reports them and decides nothing.
- Agent wrappers (02) and the per-agent state heuristics' tuning
  (`features/agents.md` records accuracy; 04 implements the mechanism).

## 4. Interfaces owned / consumed

Owned: the guest side of `interfaces/vsock-guestd.md`, the hook socket
protocol. Consumed: `interfaces/guest-conventions.md` for every path.

## 5. Design detail

### Process model

Single static binary, runs as root, `GOMAXPROCS=1` (it must not compete with
the agent). Starts before sshd. On start: mounts nothing (02 does the
tmpfs), creates `/run/repose/hooks.sock` (0660 root:dev) and listens on
vsock port 5000 (`AF_VSOCK`, `VMADDR_CID_ANY`), or the dev Unix socket.
Accepts one hostd connection; a second connection replaces the first (the
old one gets `EOF`), so a hostd restart reconnects cleanly.

Every request handler has a deadline (default 30 s, `Switch` 10 min, `Exec`
per request) and runs in its own goroutine; responses are written under a
mutex. `Notify` messages are queued (buffer 256) and dropped with a counter
if hostd is absent for long; samples are pull-based so nothing accumulates.

### Freeze and the watchdog

`Freeze` calls `fsfreeze -f /` via the `FIFREEZE` ioctl on the root mount
(not the binary, to avoid a fork while frozen). It starts a 10 s timer; if
`Thaw` does not arrive, guestd thaws and sends `Warning{kind:
"freeze_timeout"}`. Without the timer, a hostd crash mid-snapshot would leave
every write in the guest blocked forever, and the user would see a hung
agent. During a freeze guestd itself must not write to the root
filesystem, so its logging goes to the serial console (stderr) only, never a
file.

Docker's overlay mounts are on the same filesystem; `FIFREEZE` on `/`
freezes them too, as a consistent snapshot needs.

### Switch

`Switch{system_closure}`: verify the path exists under `/nix/store` (it
arrived through the share); read `<closure>/kernel` and `<closure>/initrd`
symlink targets and compare with `/run/current-system/{kernel,initrd}`; if
they differ, respond `needs_reboot=true, rebooted=false` and do nothing
unless `force_reboot`, in which case run `<closure>/bin/switch-to-
configuration boot` and `systemctl reboot`. Otherwise run `<closure>/bin/
switch-to-configuration switch` with a 10 min deadline, capture stdout and
stderr (32 KB cap, tail kept), set `/nix/var/nix/profiles/system` to the new
closure first (so a reboot lands on it), respond with the output and exit
code. A non-zero exit leaves the old system active; guestd reports it and
hostd surfaces it as `ApplyConfig` failure with the output.

Home-manager activation for `dev` is part of the NixOS switch because 02
uses the NixOS home-manager module, so one switch does both.

### GrowFs

After hostd has run `lvextend`, `GrowFs` runs `resize2fs` on the root block
device (found from `/proc/mounts`), online, and returns the new size from
`statfs`. ext4 online grow is safe on a mounted filesystem.

### Secrets

`WriteSecrets{list}`: for each, write `/run/repose/secrets/<NAME>` with
`O_CREAT|O_TRUNC`, mode 0400, owner `dev`, via a temp file and rename.
Reserved names (`ssh_host_ed25519_key`, `ssh_host_ed25519_key-cert.pub`,
`user_ca.pub`) are written to `/run/repose/` (root, 0600 / 0644) instead and
sshd is reloaded. Then rewrite `/run/repose/secrets.env` with one `export
NAME='value'` per non-reserved secret, single-quoted with `'` escaped as
`'\''`, mode 0400 dev. Secrets are never logged, not even their names at
debug level (names are fine in metrics as a count).

### Principals

`SetPrincipals{list}`: write `/etc/ssh/principals/dev` with one principal
per line (temp file and rename), `systemctl reload sshd`. The file is on the
root filesystem, so it survives restarts; hostd sends it at every start
anyway (idempotent).

### SetupProject

`SetupProject{slug, remote_url, tz, lang}`: write
`/home/dev/.repose/project.json` and `/etc/repose/env` (`TZ`,
`REPOSE_PROJECT`), create `/home/dev/<slug>` if missing (owned dev), `git
init` if it has no `.git`, and start `repose-tmux-session.service` for the
dev user if not running (`systemctl --user -M dev@ start`). That unit,
rather than guestd, creates the tmux session, so the session survives guestd
restarts and belongs to `dev`'s tmux server.

### Sample

`Sample` reads:

- `ssh_sessions`: count of `sshd: dev@pts/*` processes, or `loginctl
  list-sessions` entries for `dev` with class `user`.
- `tmux_clients`: `tmux list-clients -t <slug>` as `dev`, line count.
- `agents`: `tmux list-windows -t <slug> -F '#{window_name} #{pane_pid}
  #{pane_current_command}'`; a window whose name is an agent name (or
  `<agent>-N`) and whose pane's process tree contains that agent's binary is
  an agent; `state` is `working` if the pane's process tree consumed CPU in
  the last 5 s, `needs_input` if the last hook event for that window was
  `needs_input` and no `Stop`/`completed` since, `idle` if the process is
  alive and no CPU for 30 s, `unknown` otherwise. The hook events refine
  this (Claude Code's `Notification` with `permission_prompt` sets
  `needs_input`, and `idle_prompt` is ignored since I-418; `Stop` sets `idle` then `completed`).
- `docker_containers`: `docker ps -q | wc -l` via the socket, 2 s timeout.
- `procs`: from `/proc/*/stat`, keyed by `comm`, summing `utime+stime`
  deltas since the previous sample and RSS; capped at the top 50 by CPU
  plus any process whose name is on a watch list (`xmrig`, `minerd`,
  `kdevtmpfsi`, `kinsing`, etc., list in `internal/guestd/sample/watch.go`
  and mirrored in `SECURITY.md`). `comm` only. `/proc/<pid>/cmdline` is
  never opened by guestd; this is enforced by a test that runs guestd under
  `strace -e openat` and asserts no `cmdline` open.

Sampling costs under 20 ms; the VM test measures it.

### Hooks

The hook socket accepts `POST /` with JSON `{agent, kind, summary,
window?}`. Validation: `agent` in the known list, `kind` in `completed|
needs_input|error`, `summary` truncated to 1 KB, `window` optional (defaults
to the tmux window that owns the calling pid, found from `$TMUX_PANE` in
the caller's environment, read from `/proc/<pid>/environ` of the peer pid
obtained via `SO_PEERCRED`; this is the one environment read, limited to
that variable, and documented in `SECURITY.md`). Relays as `AgentEvent`
and updates the window's last-hook state for `Sample`.

`repose-hook` maps Claude Code's payload: `hook_event_name=Stop` →
`completed` with `summary` = last assistant line if present in
`transcript_path` (read tail 4 KB, first `"type":"assistant"` text, 200
chars); `Notification` with `notification_type=permission_prompt|idle_
prompt|agent_needs_input` → `needs_input` with the notification message;
`StopFailure` → `error`. Other agents' mappings live beside it and are
listed in `features/agents.md`.

### Exec

`Exec{argv, timeout_s, as_user}`: runs with `setpriv` to `dev` or root,
captures 64 KB of each stream, returns exit code. guestd logs `exec
requested` with argv length only; hostd holds the audit record. Used by
`repose-admin exec` and by hostd for two internal purposes: the git
fetch/checkout at `run` (07 drives it through the SSH session instead, so
this is only a fallback) and `docker ps` if the socket path changes.

### Warnings

A ticker every 30 s checks: root filesystem over 90 percent (`Warning{disk_
90}`), inotify instances exhausted (`/proc/sys/fs/inotify` current vs max,
via `lsof`-free counting of `/proc/*/fd` inotify entries, sampled cheaply
every 5 minutes), Docker socket not answering (`docker_down`), and `dmesg`
for `Out of memory` lines since last check (`oom`). Each kind is sent at
most once per 10 minutes.

## 6. Failure modes

| Failure | Outcome |
|---|---|
| hostd never connects | guestd runs, sshd runs, the user can still work over SSH; samples are not collected, hooks queue and drop after 256 with a counter; `Ready` is retried on every new connection. The api shows `guestd_ok=false` after 60 s (03). |
| `Freeze` then hostd dies | Thawed after 10 s, `Warning{freeze_timeout}` on reconnect. Snapshot marked failed by hostd's timeout. |
| `Switch` fails | Old system stays; output returned; api shows the revision `failed` with the tail; user sees it in `repose config apply` and `repose status`. |
| `Switch` needs a reboot and the user did not force | Response `needs_reboot=true`; CLI prints `this change needs a reboot (kernel changed); run \`repose config apply --reboot\` when your agent is idle`. |
| Secret name invalid or value over 64 KB | Rejected upstream by the api; guestd also rejects with `invalid_argument` and writes nothing for the whole batch (atomic per request). |
| Hook JSON malformed | 400 to the wrapper, which ignores it; guestd logs `hook rejected: <reason>` without the body. |
| `Sample` exceeds 1 s | Returns partial data with `partial=true`; hostd logs it; a metric counts it (10). |
| tmux server not running for dev | `SetupProject` starts the unit; `Sample` reports 0 clients and no agents, `Warning{tmux_down}` once. |
| Root fs 100 percent | Writes fail everywhere; guestd's own writes are on tmpfs so it keeps running; `Warning{disk_90}` fired earlier; the user resizes with `repose resize`. |

## 7. Testing

- Unit tests for framing, every handler with a fake filesystem root
  (`--root` flag pointing at a temp dir for paths under `/run/repose`,
  `/etc/ssh`, `/home/dev`), the proc sampler against fixture `/proc` trees,
  the hook mapper against recorded payloads from each agent (fixtures in
  `internal/guestd/hooks/testdata/`).
- The `strace` test asserting no `cmdline` or `environ` opens except the
  peer-pid `TMUX_PANE` read.
- The NixOS VM test in 02 runs the real binary: freeze and thaw with a
  write blocked in between, switch to a second generation, grow after a
  `qemu-img resize`, secrets appear with the right modes, principals reload
  sshd, hooks flow, a sample returns the expected tmux windows.
- On a real host with 03: the same sequence over real vsock; paste the
  hostd log.

## 8. Rollback

guestd is part of the base closure; rolling back the base (02 §8) rolls it
back. The protocol is versioned by `Ping.version`; hostd refuses to send a
request the guest's version does not know and reports `guest_unresponsive`
with the version in the message, so a new hostd with old guests fails only
the requests those guests do not know.

## 9. Checklist

- [x] `go test ./internal/guestd/... ./internal/vsockrpc/...` passes with
      race detector. Evidence: CI. Closed: CI run 35875626476 (commit
      264e6c2, 2026-09-23), step `go test -race ./...`: `ok` for every
      `internal/guestd/...` package and `internal/vsockrpc`.
- [ ] The strace test passes and is in CI (Linux runner). Evidence: CI log
      showing the test name. Open: `TestStraceNeverOpensCmdlineOrEnviron`
      passes locally (commit 3c960c8 message) but CI runs `go test -race
      ./...` without `-v` and does not install strace, so the test may skip
      there and no CI log shows its name; add strace to the runner and a
      `-run TestStrace -v` step.
- [ ] Every request in `interfaces/vsock-guestd.md` has a handler and a
      unit test; every Notify has a producer. Evidence: a table in the PR
      mapping each name to its handler file. Open: no request-to-handler
      table was ever written (commit de67cf0 only says every request is
      covered); write it from `internal/guestd/dispatch.go` against
      `interfaces/vsock-guestd.md`.
- [ ] VM test: freeze blocks a `dd` write, thaw releases it, `Warning{
      freeze_timeout}` arrives when Thaw is withheld 11 s. Evidence: test
      output. Open: the subtest exists in `nix/guest/tests/guestd.nix`
      ("Freeze blocks a write and Thaw releases it", "the freeze watchdog
      thaws and warns") and a passing build of the check is in the dev box
      store (2026-09-23), but no run output is recorded anywhere; close by
      pasting the subtest's lines from `nix build
      ./nix#checks.x86_64-linux.guestd -L`.
- [ ] VM test: `Switch` to a generation that adds a package makes the package
      appear without reboot; `Switch` to a generation with a different kernel
      returns `needs_reboot=true`. Evidence: test output. Open: the subtest
      exists in `nix/guest/tests/guestd.nix` ("Switch applies a new generation
      without a reboot", "Switch refuses a generation whose boot files
      changed") and a passing build of the check is in the dev box store
      (2026-09-23), but no run output is recorded anywhere; close by pasting
      the subtest's lines from `nix build ./nix#checks.x86_64-linux.guestd -L`
      (DECISIONS I-209 records that it ran; the output is not kept).
- [ ] VM test: `GrowFs` after a block device resize reports the new size and
      `df` agrees. Evidence: test output. Open: the subtest exists in
      `nix/guest/tests/guestd.nix` ("GrowFs after a block device resize") and
      a passing build of the check is in the dev box store (2026-09-23), but
      no run output is recorded anywhere; close by pasting the subtest's lines
      from `nix build ./nix#checks.x86_64-linux.guestd -L`.
- [ ] VM test: `WriteSecrets` yields files with mode 0400 owner dev and a
      correctly quoted `secrets.env` for a value containing `'` and newlines.
      Evidence: test output. Open: the subtest exists in
      `nix/guest/tests/guestd.nix` ("WriteSecrets: modes, ownership and shell
      quoting") and a passing build of the check is in the dev box store
      (2026-09-23), but no run output is recorded anywhere; close by pasting
      the subtest's lines from `nix build ./nix#checks.x86_64-linux.guestd
      -L`.
- [ ] VM test: `SetPrincipals` followed by an ssh with a matching certificate
      succeeds. Evidence: test output. Open: the subtest exists in
      `nix/guest/tests/guestd.nix` ("SetPrincipals, then ssh with a matching
      certificate") and a passing build of the check is in the dev box store
      (2026-09-23), but no run output is recorded anywhere; close by pasting
      the subtest's lines from `nix build ./nix#checks.x86_64-linux.guestd
      -L`.
- [ ] VM test: `Sample` reports the tmux windows and agent states for a
      fixture session with a running `sleep` renamed to `claude`. Evidence:
      test output. Open: the subtest exists in `nix/guest/tests/guestd.nix`
      ("Sample reports the tmux windows and agent states") and a passing build
      of the check is in the dev box store (2026-09-23), but no run output is
      recorded anywhere; close by pasting the subtest's lines from `nix build
      ./nix#checks.x86_64-linux.guestd -L`.
- [x] Hook mapper fixtures exist for Claude Code `Stop`, `Notification`
      (three types), `StopFailure`, and for each other agent's mechanism
      named in `features/agents.md`. Evidence: the testdata directory
      listing. Closed: `internal/guestd/hooks/testdata/` has claude
      (stop, stop-no-transcript, stopfailure, subagent-stop, notification
      idle-prompt / permission-prompt / agent-needs-input plus untyped and
      unrelated), codex (agent-turn-complete), opencode (session-idle,
      session-error, permission-asked, message-part-updated), and gemini and
      pi (README plus heuristic fixture, the pane-idle agents).
- [ ] On a real guest: `journalctl -u guestd` shows `listening on vsock
      port 5000` and hostd's log shows `Ready` within 5 s of boot.
      Evidence: both pasted. Open: neither journal line is pasted anywhere
      (the message was renamed for this grep in commit f426661), and Ready
      is not within 5 s: RESEARCH §11 measured 11.7 s on host-01 and
      RESEARCH §14 6.2-6.4 s on the dev box with I-161; paste both lines
      from a real guest booted on a base carrying I-161.
- [ ] Sampling takes under 20 ms (VM test asserts, and real guest `Sample`
      timing logged). Evidence: log line. Open: the VM subtest "a sample
      costs under 20 ms" asserts it, but no `"event":"sample"` line with
      `duration_ms` from the VM test or from a real guest is recorded; paste
      one from `journalctl -u guestd -o cat` on a host-01 guest.
- [ ] `guestd` binary is under 15 MB and uses under 20 MB RSS idle.
      Evidence: `ls -l` and `ps` output. Open: the VM subtest "guestd is
      small and light" asserts both and prints them, but neither number is
      recorded; paste `ls -l $(readlink -f $(which guestd))` and `ps -o rss=`
      from a real guest.
- [x] No log line anywhere in guestd can contain a secret value, a hook
      body, or a cmdline. Evidence: reviewer grep of `log.` calls listed in
      the PR. Closed: security/review-2026-09-20.md boundary row
      "Never-log list in hostd and guestd" (every `log.*` call in
      `internal/guestd` read: ids, states, kinds, counts, durations, error
      codes) and row "Hook socket cannot spoof another project" (kind and
      byte count, never the summary); STATUS 2026-09-20 14-security review
      04-guestd line.
- [x] `ops/RUNBOOK.md` entries: guestd not ready, freeze timeout, switch
      failed. Evidence: the entries. Closed: `docs/ops/RUNBOOK.md`
      headings "Guestd not ready", "Freeze timeout", "Switch failed" (commit
      1283e82).
- [x] `rg 'TODO|FIXME|not implemented' cmd/guestd internal/guestd
      cmd/repose-hook` empty. Evidence: output. Closed: run by the upkeep
      worker 2026-09-23 on d3b72d3, no matches (rg exit 1).
