# Forwards both ways: laptop ports on the machine, and forwards you can see (proposal, 2026-11-10)

**Status: proposal; nothing here is decided.** The owner chose two rows
of the build list in `reports/2026-11-04-product-value-council.md`,
"Ports: is forwarding enough?": reverse forwards (`[forward] reverse` in
`config.toml`, row 2 there, item 8 on the owner's list) and visible
forwards (row 3 there, item 9). This document turns both into behaviour,
config keys, user text and tests. Two neighbours are out of scope: the
background forward daemon with pinned ports (row 1, item 7) and
owner-only preview links (row 4, item 13). Where this design touches
them, it says so and stops.

All four council members named the same gap: "the agent cannot reach
your laptop's Android emulator, Ollama or local database". The economist
card adds that adb to the laptop's emulator "answers most of the Android
question at $0 host cost". The second gap is that the payoff of
auto-forward is hard to see: the tmux message names a port pair, a dev
server's `--open` prints a URL the user must copy, and nothing outside
tmux lists the forwards.

Any of this becomes real only through new `DECISIONS.md` entries. They
amend I-199 and I-324 (the forward messages), I-634 (which links open on
the laptop) and I-616 (the `status` rows), and they add to `SECURITY.md`
boundary 13. No decision ids are reserved here.

## Summary

| Question | Answer |
|---|---|
| What the user sets | `[forward] reverse = [5037, 11434]` in `~/.config/repose/config.toml`, and `[projects.NAME.forward] reverse` for one project, as `[mcp] forward` works today |
| When a reverse forward exists | Only while `run` or `attach` is attached, from this laptop, with the session helper running. It ends with the attach |
| Where it listens on the machine | `127.0.0.1` and `::1` only. The guest's sshd already enforces this (`GatewayPorts no`) |
| How it travels | A second SSH connection the session helper holds, with `-R` and `ExitOnForwardFailure=yes`, as `repose browser bridge` does. The gateway already relays it |
| Ports allowed | 1024 to 65535, at most 16, minus the ports the machine keeps for itself and four laptop ports that would go round a repose control (9222, 9229, 2375, 2376) |
| New forward message | `⇄ localhost:5173 → your laptop` |
| `http://localhost` links | A link the machine hands to `$BROWSER` opens on the laptop when its port is forwarded, on the laptop port the forward uses |
| `repose status` | The `listening` rows say which ports are on your laptop; a new `laptop` row lists the laptop's ports on the machine, with process names |
| Freeze | No change to the rest rule. A reverse forward lives inside an attach, and an attach already keeps the machine awake |
| Biggest risk | A reverse forward is a raw TCP pipe. Everything on the machine, agents included, gets what a local program on the laptop gets from that service. For a Postgres with `trust` auth, that is code on the laptop |
| Size | S. *Estimate* 4 to 5 days with docs and the live test |
| Decide first | Decision 1: accept a laptop port that the CLI passes through without inspecting it |

---

## 1. The repo today

**Auto-forward (I-199).** The session helper runs beside every attach
(`internal/cli/session.go:17-30`). Its options carry `Forward`,
`OpenURLs` and `MCP` (`session.go:41-75`). With `Forward`, it builds a
`forwarder` and runs `runForwards` until the attach ends
(`session.go:191-199`). The forwarder reads `ss -Hltn` over the
ControlMaster every second (`internal/cli/forward.go:26-27,165-172`),
keeps listeners on loopback and wildcard addresses from port 1024 up
(`forward.go:83-97`), and skips the machine's own ports, 9226 among
them because "the tunnel end leads back to the laptop, I-296"
(`forward.go:56-68`). Each forward is `ssh -O forward -L` on the master
(`forward.go:368-381`). A taken laptop port moves to the next free one,
up to 20 tries (`forward.go:336-353`).

**The messages today.** One port: `⇄ localhost:5173 → :5173`
(`forward.go:319`). Several, coalesced by I-324: `⇄ 6 ports on
localhost: 3000, 3001, ...` (`forward.go:329`). A moved port:
`⇄ localhost:3001 → :3000 (3000 is taken on your laptop)`
(`forward.go:294`). Portless keeps its own line (`forward.go:291-292`).
The message names two ports and no place. A user who did not read the
docs cannot tell which side is the laptop.

**The forward registry.** Each attached CLI writes its guest ports, as
one line of numbers, to `~/.repose/forwards/<id>` on the machine and
re-stamps it every 20 s. The status bar shows the union of fresh
entries (`forward.go:401-439`). The reader keeps only bare numbers:
`tr ' ' '\n' | grep -E '^[0-9]+$'` (`forward.go:425`). Section 4 uses
that to extend the format without breaking older CLIs.

**`repose open`.** `OpenPortCmd` (`internal/cli/open.go:22-67`) runs
one `ssh -N -L` in the foreground on a connection of its own
(`ownConnection`, `open.go:143-152`, I-149), reconnects for up to 2
minutes (`keepForward`, `open.go:74-99`, I-469), and prints
`http://localhost:3000 → todo-app:3000 (Ctrl-C to stop)` (`open.go:62`).
It writes nothing to the registry, so nothing lists it. Item 7 reworks
this command; this proposal leaves it alone.

**Links that open on the laptop exist, for `https` only (I-634, amending
I-541).** The machine's `BROWSER` is `repose-print-url`
(`nix/guest/base/browser.nix:217-226`). It prints the URL and appends
each `https://` link to `~/.cache/repose/open-urls`; other schemes are
not recorded (`browser.nix:222`). The helper takes that file in the same
SSH call as `ss` (`internal/cli/openurl.go:30-33`, `forward.go:165-169`)
and opens links younger than 2 minutes (`openurl.go:24-25`).
`openableURL` accepts any `https` link with a host (`openurl.go:65-73`).
The public text is `apps/web/src/content/docs/run-and-attach.md:199-203`,
"Links the machine opens".

*A gap in it today.* `https://localhost:8443/` passes `openableURL` and
opens on the laptop as written. If the forward for 8443 moved to 8444,
or nothing forwards 8443, the laptop opens its own server on 8443, or
nothing. Section 5 fixes this with the `http` case.

**Reverse forwards already work, in one place.** `repose browser bridge`
holds `ssh -o ExitOnForwardFailure=yes -R 127.0.0.1:9226:127.0.0.1:PORT`
on its own connection, with a remote command that holds the guest end
(`internal/cli/bridge.go:459-468`, hold command `bridge.go:66`). I-149
says why it is not on the ControlMaster: "a reverse forward through the
ControlMaster would outlive the command". The gateway relays
`tcpip-forward` and its cancel to the guest (`internal/gateway/relay.go:679-697`)
and accepts `forwarded-tcpip` channels from the guest
(`relay.go:68-69`). The guest's sshd has `AllowTcpForwarding = true`
and `GatewayPorts = "no"` (`nix/guest/base/ssh.nix:58-59`), so a `-R`
listener binds to the guest's loopback only. The test guest answers
`tcpip-forward` and opens `forwarded-tcpip` back
(`internal/testguest/testguest.go:200-260`), so a Go test can drive a
reverse forward without a VM. `repose mcp forward` does not use `-R`;
it carries frames over a hold's stdin and stdout
(`internal/cli/mcpforward.go:24-37`).

**Config.** `[mcp] forward` and `[projects.NAME.mcp] forward` are the
model for a laptop-side list that the session helper acts on beside
every attach (`internal/cli/config.go:59-67,70-73`; the merge is
`mcpForward`, `config.go:88-101`). On Windows the helper does not run,
and `[mcp] forward` prints a line that says so (`session.go:77-91`).

**`repose status`.** It reads `ss -Hltnp`, `ps`, `df` and the
multiplexer from the guest in one SSH call (`internal/cli/status_procs.go:46`)
and prints a `listening` row per process: `node :5173 up 3d 410.0 MB`
(`status_procs.go:165-179`). The `attached` row counts SSH sessions and
tmux clients (`internal/cli/status.go:462,493-506`). Nothing in it says
which ports are forwarded.

**What the agents are told.** The guide says "You cannot reach the
laptop or its files from here" (`nix/guest/base/agent-guide.md:11`) and
describes forwards in one direction only (`agent-guide.md:18-23`). The
public page says "**Not your laptop.** Nothing on the machine can open a
connection to it", with the bridge and `repose mcp forward` as the
exceptions (`apps/web/src/content/docs/secrets.md:179`).

**The freeze.** I-818's rule rests a running machine only when, among
other terms, no gateway session is open and each of the last 15 minutes
had `ssh_sessions = 0` and `tmux_clients = 0`. I-930 adds the principle
that an open connection is not use; only bytes moving on it are.

## 2. Reverse forwards: what the user sets

```toml
[forward]
reverse = [5037, 11434]

[projects.todo-app.forward]
reverse = ["15432:5432"]
```

- `reverse` lists laptop ports that appear on the machine's `localhost`
  while you are attached. A number means the same port on both sides.
- `"MACHINE:LAPTOP"` puts the laptop's port on another port on the
  machine. The port that listens comes first, as in `repose open
  LOCAL:PORT` (I-619) and in ssh's own `-L` and `-R`. The example above
  is the common case: the machine's own Postgres from the menu holds
  5432, so the laptop's Postgres goes to 15432.
- `[projects.NAME.forward] reverse` adds to `[forward] reverse` for that
  project, each port once, as `[projects.NAME.mcp] forward` adds to
  `[mcp] forward`. Decision 2 asks whether the global table should exist
  at all.
- `REPOSE_NO_FORWARD=1` turns off both directions. It stays the one knob
  (I-199).

**Refused values.** A bad entry fails the config load and every command
exits 2 naming the file, as an unknown `default_multiplexer` does today.
A forward dropped in silence would look like a broken forward, which is
the reason `session.go:86-88` gives for the Windows line. The texts:

```
~/.config/repose/config.toml: forward.reverse: 80 is below 1024. Ports below 1024 can't be lent to the machine.
~/.config/repose/config.toml: forward.reverse: 6080 is a port the machine keeps for itself.
~/.config/repose/config.toml: forward.reverse: 9222 is Chrome's DevTools port. To lend the agents your Chrome, use repose browser bridge.
~/.config/repose/config.toml: forward.reverse: 2375 is Docker's API port, which runs anything on your laptop.
~/.config/repose/config.toml: forward.reverse: 17 ports; at most 16.
```

The machine's own ports are `forwardPlatformPorts` (5353, 5355, 5900,
6080, 6081, 9224, 9225, 9226). The laptop deny list is 9222 (a raw
DevTools port goes round the bridge's policy, I-311), 9229 (Node's
inspector runs code), 2375 and 2376 (Docker's API). Decision 3 asks
whether to keep this list.

**No new command.** The owner asked for a config key, and
`cli-surface-taste` asks for no new top-level command without a preview.
A one-off form would fold under `repose open` (for example `repose open
--reverse 11434`); decision 6 leaves it for later.

## 3. Reverse forwards: what happens

**When.** The session helper starts the reverse forwards beside the
attach, after its first `ss` poll, and ends them when the attach ends.
A dropped attach that reconnects (I-469) gets them back on the same
ports.

**How.** One extra SSH connection per attach, held by the helper:

```
ssh -o ControlPath=none -o ExitOnForwardFailure=yes \
    -R 127.0.0.1:5037:localhost:5037 -R [::1]:5037:localhost:5037 \
    -R 127.0.0.1:11434:localhost:11434 -R [::1]:11434:localhost:11434 \
    todo-app.repose 'exec cat >/dev/null'
```

- *Its own connection, not the ControlMaster.* I-149's reason holds: a
  `-R` on the master lives as long as the master, up to `ControlPersist
  10m` after the last client (`internal/cli/cert.go:369-375`). A laptop
  port must not stay open on the machine ten minutes after you detach.
  A separate connection also means an agent that kills the process
  holding 11434 ends the reverse forward and leaves the attach alone.
- *The remote command holds it.* `cat` reads the helper's pipe. When
  the helper exits, for any reason, the pipe closes, `cat` exits, and
  the connection and its listeners go. This needs no base change, so it
  works on machines that are running now.
- *Both loopbacks.* Some clients on the machine try `::1` first for
  `localhost`. The laptop end is `localhost`, so ssh reaches a laptop
  service on either `127.0.0.1` or `::1`.
- *A port the machine already uses* is left out before the connection
  starts, from the same `ss` poll, and named. A race that still fails
  ends the connection (`ExitOnForwardFailure`); the helper drops the
  port ssh names and tries once more.
- *Windows.* No helper runs there (`session.go:85-91`). A set
  `[forward] reverse` prints, on stderr before the attach:
  `[forward] reverse in config.toml does nothing on Windows.`

**The loop to prevent.** The reverse listener on the machine is a
listener like any other. Without a change, the forwarder would see
`sshd-session` on `127.0.0.1:5037`, find 5037 taken on the laptop by
adb, and forward the machine's 5037 back to the laptop's 5038: a loop
back to the laptop. The forwarder skips every port in its own reverse
set, and every port another laptop's registry entry names as reverse
(section 4), the same way it skips 9226 today.

**Messages,** through the multiplexer as today:

```
⇄ localhost:5037 on the machine → adb on your laptop
⇄ localhost:5037, 11434 on the machine → your laptop
⇄ localhost:11434 on the machine → your laptop (nothing listens on 11434 there yet)
Could not lend your laptop's 5432: postgres on the machine listens there. Use "15432:5432" in [forward] reverse.
```

The laptop process name comes from the laptop's own tools when it asks
(`lsof -nP -iTCP:PORT -sTCP:LISTEN` on macOS, `ss -Hltnp` on Linux) and
is left out when they do not answer. With nothing listening on the
laptop, the forward still starts; a connection from the machine is then
refused, which is what a program would see on the laptop too.

**Two laptops.** The first laptop to lend 11434 holds it on the machine.
The second gets the "Could not lend" line, naming `sshd-session`. Section
4's registry lets it say `another laptop lends 11434` instead.

## 4. Visible forwards

**The message names the place.** I-199 and I-324's messages become:

```
⇄ localhost:5173 → your laptop
⇄ localhost:3000, 3001, 5173 → your laptop
⇄ 12 ports → your laptop: 3000, 3001, 3002, 3003, 3004, 3005, 3006, 3007, …
⇄ :3000 → your laptop's localhost:3001 (3000 is taken there)
```

The portless line (`forward.go:292`) is unchanged. The status bar keeps
`⇄ 3000 5173 │ ...` for laptop-bound forwards; reverse ports are not in
the bar, because the bar answers "what can I open on my laptop".

**The registry gains detail without breaking older CLIs.** Line 1 of
`~/.repose/forwards/<id>` stays the guest ports, as numbers. New lines
follow, one per forward, with fields joined by colons so the old reader
(`forward.go:425`, bare numbers only) ignores them:

```
3000 5173
out:3000:3001
out:5173:5173
in:5037:5037:adb
in:11434:11434:ollama
```

`out` is a machine port forwarded to a laptop port. `in` is a laptop
port lent to the machine, with the laptop's process name, cut to
`[A-Za-z0-9._-]`. Entries keep the 20 s heartbeat and the 60 s age-out.
`docs/interfaces/guest-conventions.md` "Ports" names the format; both
shapes are read for one release, as `CLAUDE.md` asks for a contract
change.

**`repose status`** adds the fresh registry entries to the SSH call it
makes already (`status_procs.go:46`, a `#fwd` section). Rows:

```
$ repose status
todo-app  running 2h14m  large
  agents     claude working
  checkout   main: 3 commits not on this laptop, 2 files not committed
  attached   2 SSH sessions, 1 tmux client
  listening  node :5173 up 3d 410.0 MB, on your laptop
             node :3000 up 2h 120.4 MB, on your laptop at :3001
             postgres :5432 up 3d 40.1 MB
  laptop     :5037 adb, :11434 ollama
  disk       6.2 GB of 39.0 GB (40G disk), snapshot 11h08m ago
```

- `on your laptop` marks a listening port that an attached laptop
  forwards; `at :3001` appears only when the laptop port differs.
- The `laptop` row lists laptop ports lent to the machine, with the
  laptop's process name when it was known. The reverse listeners
  themselves (`sshd-session`) leave the `listening` rows, since the
  `laptop` row says what they are.
- `attached` counts the reverse connection as an SSH session, because
  it is one. Decision 7 asks whether to hide it.
- `--json` gains `forwards`: `[{"port": 3000, "laptop_port": 3001,
  "direction": "to_laptop", "process": "node"}, {"port": 11434,
  "laptop_port": 11434, "direction": "from_laptop", "process":
  "ollama"}]`.
- The rows list the attach's forwards only. `repose open` and `repose
  browser` forwards are not in the registry; item 7 is where they would
  join it.

## 5. `http://localhost` links open on the laptop

**The machine side.** `repose-print-url` also records `http://` and
`https://` links whose host is `localhost`, `127.0.0.1` or `[::1]`, in
the same file, in the same format. Other `http://` links stay printed
only. This is a base change: a machine on an older base keeps recording
`https` links only.

**The laptop side.** For a loopback link, the helper:

1. waits up to 5 s (five polls) for a forward of that port, because a
   dev server may hand over its URL a moment before `ss` shows it;
2. opens it with the host and port rewritten to the laptop's end of the
   forward (`http://localhost:3001/` for the machine's 3000);
3. without a forward after 5 s, opens nothing and says
   `A program on the machine opened localhost:3000, and no forward reaches it.`

A loopback link never opens on the laptop as written. Without a forward
it would reach whatever the laptop runs on that port. With
`REPOSE_NO_FORWARD=1`, loopback links are not opened and the line above
says so once. The same link opens at most once a minute, so a dev
server that restarts with `--open` in a loop does not fill the laptop
with tabs. The rule covers `https://localhost` too, which closes the gap
in section 1.

**What this reaches, and what not.** It reaches programs that hand a link
to `$BROWSER`: Python's `webbrowser` (I-541 names it) and tools that
read `BROWSER` for their open option. A URL a dev server only prints to
its terminal is out of reach; the forward message covers that case.
Which dev servers honour `BROWSER` is checked in the live test, not
claimed here.

## 6. Security

**What changes.** Today nothing on the machine can open a connection to
the laptop, except through the bridge, which the CLI polices message by
message (I-311), and `repose mcp forward`, which runs one named server.
A reverse forward is a byte pipe the CLI does not read. For as long as
you are attached, every process on the machine reaches that laptop
service as a local program on the laptop would: every agent, any
package install script, a page an agent reads that carries a prompt
injection. `SECURITY.md` counts "a malicious or compromised agent" as an
actor, bounded by "what the guest can reach". This feature widens that
bound, by the user's choice, port by port.

**What a lent port gives, by service.** These are known behaviours of
the tools, to confirm in the live test:

- *adb (5037).* The laptop's adb server serves every device it has: the
  emulator, and also a phone on USB with debugging allowed. `adb -s` with
  any serial installs apps, runs a shell and reads files on it. `adb
  connect HOST:PORT` makes the laptop's adb server open connections to
  hosts on the laptop's own network.
- *Ollama (11434).* No authentication. Anything on the machine can pull
  models to the laptop's disk and delete the user's models.
- *A local database.* The connection arrives from the laptop's
  `127.0.0.1`, so `pg_hba.conf`'s localhost line applies. Homebrew's and
  Postgres.app's clusters commonly use `trust` there, and their default
  role is a superuser, which `COPY ... FROM PROGRAM` turns into commands
  run on the laptop. A Redis with no password gives every key to the
  machine.

**The design's answers.**

1. *Opt-in by port, in a file on the laptop.* Nothing on the machine can
   add a port. The machine cannot write the laptop's `config.toml`.
2. *Loopback only on the machine.* `GatewayPorts no` in the guest's sshd
   (`ssh.nix:59`), plus an explicit `127.0.0.1` and `[::1]` bind. Docker
   containers on a bridge network do not reach it; a container with
   `--network host` does, as it reaches every loopback port.
3. *Attached only.* The forward lives inside the attach and ends with
   it (section 3). A laptop that sleeps ends it within about 90 s: the
   gateway sends a keepalive every 30 s (`internal/gateway/server.go:178-179`)
   and ends a session after three with no answer.
4. *The deny list* (section 2) keeps four ports whose raw form goes
   round a repose control or runs code by design.
5. *The words where the user sets it.* `cli.md`'s `forward.reverse` row
   and `machine.md` say, once: "Everything on the machine, agents
   included, reaches a lent port as a program on your laptop would. Lend
   a database only with a password, and adb only with no phone plugged
   in." This line prevents a loss the user cannot undo, so I-485 keeps
   it.
6. *`SECURITY.md` boundary 13* gains: "A reverse forward the user lists
   in `config.toml` is a TCP pipe the CLI does not inspect. It listens on
   the guest's loopback, only while that laptop is attached, and refuses
   ports below 1024, the guest's own ports and 9222, 9229, 2375 and
   2376. Stops: a port the user did not list, and a lent port outliving
   the attach." `secrets.md` "What an agent on the machine can reach"
   gains a third exception to "Not your laptop".

**What the agent guide must say.** Line 11 ("You cannot reach the laptop
or its files from here") becomes true only with an exception, and
"Servers and ports" gains two lines:

```
- The user may lend you ports from their laptop, such as adb on 5037 or Ollama on 11434. While they're attached, these listen here on localhost and `ss -Hltnp` shows `sshd-session` holding them. A port held that way is the laptop's: use it, or pick another port for your own server, and never kill that process. A refused connection means the user isn't attached or the service isn't running on their laptop. <!-- /docs/machine#your-laptops-ports-on-the-machine -->
- What you do through a lent port happens on the user's laptop. Don't delete models, drop tables or uninstall apps there unless the user asks. adb reaches every device on their laptop: pass `-s emulator-5554` (or the serial they name), and never `adb kill-server`. <!-- /docs/machine#your-laptops-ports-on-the-machine -->
```

`agent_guide_test.go` needs the new `machine.md` heading to exist, which
section 9 adds.

**Logging.** Nothing new is logged. The helper runs on the laptop. The
registry lines carry ports and process names, which the logging rule
allows, and they live in the user's own guest.

## 7. Interaction with the freeze

**No new term in the rest rule.** A reverse forward exists only while
an attach does, and an attach already fails I-818's rule twice:
`tmux_clients > 0` and a gateway session open. The extra connection
changes nothing while the attach lasts and is gone when it ends.

**I-930 is the line this design must not cross.** A held reverse
connection that outlived its attach would be a gateway session with no
bytes on it, and the rule would read it as use for ever: the failure
I-930 names, "presence is not activity". Three things keep it from
happening: the `cat` hold ends on the helper's exit, the helper ends
when its parent changes (`session.go:118-119`), and a live test kills
the helper with `SIGKILL` and checks that the gateway session ends
(section 10).

**If item 7 is built later.** A reverse forward held by a background
daemon, outside any attach, would need I-930's rule written into the
rest rule: such a connection counts as use only in a minute when bytes
moved on it, and closes after 30 minutes with none, as the council asked
for forward ones. This proposal does not build that.

**Waking.** A frozen machine has no attach, so it has no reverse
forward to wake for. Nothing in this design wakes a machine that would
not wake today.

## 8. Effort

*Estimate*, one engineer:

| Part | Days |
|---|---|
| Config keys, merge, validation, `help config-file` table | 0.5 |
| Reverse hold in the session helper, retry, Windows line | 1 |
| Loop exclusion, registry lines, old and new readers | 0.5 |
| Messages (both directions) | 0.25 |
| Loopback links: base script, helper rewrite and wait, once-a-minute | 0.5 |
| `status` rows and `--json` | 0.5 |
| Docs, agent guide, `SECURITY.md`, `guest-conventions.md` | 0.75 |
| Live test (section 10) | 0.5 |

## 9. Docs to change

Public (`apps/web/src/content/docs/`, same commit as the code, I-242):

- `machine.md` "Ports": the new message text; a new subsection "Your
  laptop's ports on the machine" with the config, the remap form, the
  two-laptop rule and the warning line from section 6.
- `cli.md` "config.toml": the example and a `forward.reverse` row;
  `projects` row gains `[projects.NAME.forward]`. "`repose status`": the
  `on your laptop` mark, the `laptop` row and `forwards` in `--json`.
- `run-and-attach.md` "Links the machine opens": loopback links and the
  forward rule.
- `machine.md` "Browser" (line 202): `localhost` links now also open on
  the laptop while attached.
- `secrets.md` "What an agent on the machine can reach": the third
  exception.
- `internal/cli/helptopics_data.go`: the `config-file` table row, which
  `docs_test.go` holds equal to `cli.md`.

Internal:

- `docs/features/ports-and-previews.md`: both directions, the registry
  lines, the loop rule; and 9226 in the platform port list, which the
  doc misses today.
- `docs/features/run-and-attach.md`: the helper's new duties.
- `docs/interfaces/cli-config.md`: `forward.reverse` and
  `[projects.NAME.forward]`.
- `docs/interfaces/guest-conventions.md` "Ports": the reverse listeners
  and the registry format.
- `docs/SECURITY.md` boundary 13 (section 6).
- `nix/guest/base/agent-guide.md` (section 6) and
  `nix/guest/base/browser.nix` (the script and its comment).
- `docs/DECISIONS.md`: entries for the reverse forward, the message and
  link changes, and the status rows, under ids reserved with
  `ops/dev/release-queue id` when the work starts.

## 10. Tests and closing evidence

Each item closes with the evidence it names, pasted in the commit
message.

| Item | Evidence |
|---|---|
| Config parses and refuses | `go test ./internal/cli -run TestForwardReverseConfig -v` output: numbers, `"15432:5432"`, the project table merge, and each refusal text from section 2 with exit 2 |
| Reverse forward works end to end | A test against `internal/testguest` (it answers `tcpip-forward`): a listener on the test laptop's port, a dial from the guest side, bytes back. `-v` output pasted |
| No loop | A forwarder test whose `ss` output has `sshd-session` on 127.0.0.1:5037 and a registry line `in:5037:5037:adb` from another id: no `-O forward` for 5037. Test output pasted |
| Old registry readers | The publish script's union from a file with the new lines equals the union from line 1 alone; test output |
| Loopback links | `parseOpenURLs` and the rewrite: `http://localhost:3000/x` opens `http://localhost:3001/x` when 3000 maps to 3001; nothing opens with no forward; `http://example.com` never opens; `https://localhost:8443` follows the same rule; the once-a-minute cap |
| Status rows | Golden output of `writeListening` and the `laptop` row; `--json` with `forwards` |
| Docs agree | `go test ./internal/cli -run 'TestDocs|TestAgentGuide'` passing, and the `docs/CHECKLIST.md` greps run with their output |
| Live, macOS laptop | On a staging machine: `adb devices` on the machine lists `emulator-5554` from the laptop's emulator; `curl -s localhost:11434/api/tags` on the machine returns the laptop's models; the tmux messages from sections 3 and 4 as screenshots at 1x; `repose status` output pasted |
| Live, the attach ends it | After detach, `ss -Hltn` on the machine shows no 5037 within 5 s; after `kill -9` of the helper, the same, and the gateway session count for the project drops (`repose status` `attached`) |
| Live, the freeze still comes | 15 minutes after detach, `repose status` shows `frozen` on a machine that had reverse forwards |
| Live, adb version | The machine's `adb` against a laptop adb of another release: record whether the laptop's adb server is killed, and put the result in the docs |
| Live, `BROWSER` | Vite with `--open` and `python3 -m webbrowser http://localhost:8000` on the machine: the laptop opens the tab. Record which ones do |

VM tests cannot boot on kanali, so the live rows run against a staging
host.

## 11. Risks

- **A lent port is more power than it looks** (section 6). The warning
  line and the deny list reduce it. They do not remove it, and a user who
  lends a `trust` Postgres has given the machine their laptop.
- **adb version skew.** An adb client that finds a server of another
  version may kill it and start its own. On the machine, that kills the
  laptop's adb server through the forward. The live test settles it;
  until then, the guide says never `adb kill-server` and the docs say to
  match versions.
- **The loop.** If the exclusion misses a case (an old CLI on a second
  laptop that does not read `in:` lines), that laptop forwards the
  machine's 5037 back to itself at 5038. It is harmless to data and
  confusing to see. The one-release overlap is the window.
- **Port clashes on the machine.** Menu services hold common ports
  (Postgres 5432, Redis 6379). The refusal text names the remap.
- **More SSH sessions.** Each attach with reverse forwards holds two
  gateway sessions. The `attached` row shows it. The gateway caps relays
  at 32 per user (`DefaultMaxConnsPerUser`,
  `internal/gateway/limits.go:24`); a user attached to many machines at
  once, each with reverse forwards and its ControlMaster, reaches that
  cap sooner.
- **Tabs the user did not ask for.** An agent that starts servers with
  `--open` opens tabs on the laptop. The once-a-minute cap limits it;
  `REPOSE_NO_BROWSER=1` turns it off.
- **Two laptops.** `status` says `on your laptop` for a forward another
  laptop holds. It is rare, and the row stays true for one of them.

## Decisions for the owner, in order

1. **Accept a laptop port that the CLI passes through without reading
   it.** The bridge set the rule that the CLI decides what a guest
   reaches on the laptop (boundary 13). A reverse forward decides only
   the port. Recommendation: yes, with the warning line, the deny list
   and the `SECURITY.md` text in section 6.
2. **Global `[forward] reverse`, or per project only?** Global matches
   `[mcp] forward` and the council's spelling, and lends adb to every
   project, a throwaway one with a cloned stranger's repo included.
   Recommendation: both tables, as `[mcp]` has; the docs example uses
   the project table.
3. **Keep the laptop deny list** (9222, 9229, 2375, 2376)? It refuses
   ports a user might want for a reason. Recommendation: keep it; 9222
   has the bridge, and the other three run code by design.
4. **The remap spelling** `"MACHINE:LAPTOP"`, listening port first.
   Recommendation: yes, it matches `repose open LOCAL:PORT` and ssh.
5. **`REPOSE_NO_FORWARD=1` turns off both directions.** Recommendation:
   yes, to keep one knob.
6. **No CLI command in this round.** A one-off reverse forward would be
   `repose open --reverse PORT`, under the existing noun.
   Recommendation: config only now; revisit with item 7, which reworks
   `repose open`.
7. **New message text and status rows** (`⇄ localhost:5173 → your
   laptop`, the `on your laptop` mark, the `laptop` row, the reverse
   connection counted in `attached`). Recommendation: yes as written;
   say if the extra SSH session in `attached` should be hidden.
8. **Loopback links open only through a forward, on the forwarded
   port**, which also changes how `https://localhost` opens today.
   Recommendation: yes.

Out of scope here, by the owner's choice: the background forward daemon
with pinned ports (item 7) and owner-only preview links (item 13).
