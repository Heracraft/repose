# Customer review, the command line — 2026-11-19

One session of the three-session council (`docs/REVIEW.md`), reviewing the
**command line** as a customer with a stopwatch against production
(`repose.run`, `api.repose.run`, release r20261119-6). The dashboard and the
Mac mini path are other sessions' surfaces and are not judged here.

**Which account did what.** Everything in this report was run as the owner's
account, because that is the only login this box holds:
`repose login --status` → `user-c7fh26yzrl93 (…) on api.repose.run, no plan`.
I did not use `ops/dev/dashboard-session`; it mints a browser session for the
dashboard and the CLI needed no such thing. Nothing was run as a separate
identity of mine, so read every step below as the owner's account. I added no
host except the two I report and removed each within a minute, completed no
payment, ran no `repose-admin`, touched no host, edge, control VM, Azure or
tofu, and installed nothing from `nix/`.

**What I destroyed.** Only projects I created: `plainfolder`, `council-tiny`,
`council-t2`, `council-t3`, `council-plain`, `notarepo2`. The owner's twenty
projects were never stopped, started, resized or removed.

---

## What would make a user scream

1. **"The docs told me to run a command my repose does not have."** For at
   least 21 minutes this evening the published install path served a CLI with
   no `host` command while the live docs documented four of them in full.
   Closed at 21:00 UTC. **F1, P0, ENG-23.**
2. **"I bought a Mac for this and now every project goes to it whether I
   want it to or not."** No CLI command can choose where a project runs, and
   the one escape the docs name is an API field the API reference does not
   document. **F17, P1 here, P0 on the dashboard session's end-to-end
   measurement, ENG-67.**
3. **"I handed my Mac to repose and it never told me what it costs."** The
   command that commits hardware names no plan, no price, no trial and no
   charge, and no command anywhere in the CLI names a price. **F2, P1,
   ENG-39.**
4. **"Adding a trigger took over my Stripe URL and my app stopped getting
   deliveries."** Proven with the same URL answering 502 before and 202
   after. **F5, P1, ENG-32.**
5. **"My first `repose run` printed 70 lines of nix and I thought it had
   broken."** And so does every `repose config add` after it. **F4, P1,
   ENG-27.**
6. **"`repose host ls` says I own nothing and its own `--json` says I own
   four."** **F6, P1, ENG-37.**

---

## The numbers

Measured with wall clock around each command, released CLI, hosted fleet on
host-02, one command at a time. Each figure says what produced it.

| What | Measured | The number to beat | Verdict |
|---|---|---|---|
| `curl … install.sh \| sh` | **1.77 s** | — | fine |
| `repose version update` (0.1.49→0.1.50) | **0.52 s** | — | fine |
| First `repose run` in a new project (`large`, with the owner's `machine.nix`) | **45.5 s** — config eval 9.0 s, 20 derivations built 12 s, boot 19 s | — | see F4 |
| `repose start`, clean, nothing pending (`large`) | **11.07 s** | about 12 s, `docs/index.md` | **the published promise holds** |
| `repose start` of a project in `error` | 31 s, 16 s, 37 s, 16 s | — | recovers |
| `repose stop` (returns; machine reaches `stopped`) | 0.5–0.8 s; stopped in ~5 s | — | fine |
| `repose snapshots create`, 93 MB | 2.0–2.4 s | — | fine |
| `repose snapshots restore`, `large`, 93 MB snapshot | **12.59, 12.48, 12.61 s** | — | see note |
| `repose restore` of a destroyed `large`, 895 KB snapshot | **14.91 s** (CLI attributes 1.3 s to the restore, the rest to the boot) | — | see note |
| `repose rm -y --wait` | 3.6–7.0 s | — | fine |
| `repose resize --size tiny→small` (restart) | 25.9 s | — | fine |
| `repose resize` disk 10G→20G, live | **1.5 s**, filesystem online to 20 G, no restart | — | better than expected |
| `repose sync`, 1 modified + 1 untracked | 0.9 s | — | fine |
| Webhook POST → `202` → agent window | 0.33 s → window within 6 s | — | fine |
| `repose ls`, `status`, `events`, `secrets ls`, `tokens` | 42–565 ms | — | fine |
| Blackholed api address before giving up | 30.0 s, then `Could not reach …: timed out.` | — | fine |

**Note on the restore figures, and a retraction.** I first offered these four
readings against the "a restore is about 8 s" figure in my brief and in
`docs/REVIEW.md`, and that comparison was wrong: the 8 s is the park and
restore of a frozen guest's **live memory** on a host restart, which has no
command behind it and is invisible from a customer's seat. These four are two
different, disk-snapshot commands. I retracted the readings from ENG-13 so
whoever fixes the park path is not sent measuring the wrong thing. Kept here
because they are the numbers a customer actually waits through, and because
the public docs make **no** restore claim at all — so there is no broken
promise here, only an internal figure that does not describe these commands.

**Sizes.** `tiny` gives 1968 MB and a 9.8 G disk, `small` 3915 MB and
"1 core, 4 GB memory; fits the Solo plan" in the CLI's own words. Both match
what is sold. `large` and `xl` were exercised through existing projects;
I created no `xl`, so its memory is unverified by me. A guest sees `nproc` = 2
on a `tiny`, which sells 0.5 core — correct for a quota-based class, but a
customer sizing `make -j$(nproc)` will oversubscribe.

---

## Findings

### F1 — The docs documented four commands the released CLI did not have (P0, ENG-23)

**Reproduction**, 20:39 UTC, exactly what `https://repose.run/docs/install`
tells a new user to run:

    $ curl -fsSL https://repose.run/install.sh | sh
    Downloading repose_v0.1.49_linux_amd64.tar.gz (v0.1.49)...
    Installed /home/azureuser/.local/bin/repose
    $ repose host add
    unknown command "host" for "repose"
    `repose --help` shows its usage.          # exit 2

At that same moment `https://repose.run/docs/own-hardware` and
`https://repose.run/docs/cli` were live (HTTP 200) and documented
`repose host add`, `repose host run`, `repose host list` and `repose host rm`
in full, flags included — `own-hardware` says plainly "On the Mac mini, with
the CLI installed and logged in: `repose host add`". `repose --help` listed no
`host` command at all, so nothing pointed the customer anywhere.

**Window.** v0.1.50 was published at 21:00:03 UTC. The documented update path
`repose version update` took 0.52 s and `repose host --help` then works. So
the window ran at least 20:39→21:00 UTC and opened whenever the docs were
pushed. **The window is closed; the ordering that opened it is not.** Docs
ship on a push, the CLI ships on a tag, and nothing holds the first for the
second. Own hardware is the thing being launched, so this is the one page
where that ordering costs a customer the whole feature.

This is the first thing every customer does, and the install itself is good:
1.77 s, it checks a signature, and when it cannot write `~/.bashrc` it says
so and prints the exact line to add.

### F2 — `repose host add` never says what it costs (P1, ENG-39)

    $ repose host add
    <join token on stdout>
    host-2: this is its join token. It works once, before Fri 20 Nov 21:03.
    What to do with it: https://repose.run/docs/own-hardware

That is the whole of it. No plan, no price, no trial, no charge, at the moment
a customer commits their own hardware to the product. The plan is $10 a month
with a 7-day trial; the customer learns neither here nor anywhere else on the
command line. `repose --help` has no plan, billing, price or pay command, and
`repose login --status` prints a plan name with no price and no trial end.

Billing belongs to the dashboard and the CLI cannot take a card — but naming
the price and the trial's end is not taking a card. The same silence is what a
paying customer sees.

**I walked no checkout and completed no payment.** There is no CLI path to one
to walk.

### F3 — `login --status` says "no plan" for an account that is billing-exempt (P1, ENG-40)

    $ repose login --status
    user-c7fh26yzrl93 (nehemiahelibariki@gmail.com) on api.repose.run, no plan

The dashboard's Billing page reports this same account as billing-exempt. Two
surfaces, one account, two different answers, and "no plan" reads as "nothing
is checked" rather than "limits do not apply here".

**This cost a reviewer a P0.** On that line I concluded the Own hardware plan
was unenforced, from `repose host add` succeeding on an account the CLI told me
had no plan. It is correct by design — a billing-exempt account is deliberately
allowed one host whatever the plans say, so the owner can add a Mac without
buying from themselves. The claim is withdrawn and the card parked (ENG-36).
A line that misleads the people who read it most is worth fixing on that
evidence alone.

### F4 — The nix build log is printed over the summary that already says it (P1, ENG-27)

A first `repose run` printed ~70 lines like these, between its clean progress
lines:

    nix › warning: ignoring the client-specified setting 'sandbox', because it is a restricted setting and you are not a trusted user
    nix › these 20 derivations will be built:
    nix ›   /nix/store/6y8s3bxgkx13c983cfbp66dw99sjasn6-guestd-dirty.drv
    nix › system-path> pkgs.buildEnv warning: colliding subpath (ignored): …
    nix › system-path> created 16838 symlinks in user environment
    nix › …> structuredAttrs is enabled

**This is not only a first run.** `repose config add ripgrep` on an existing
machine printed **73 lines, 62 of them `nix ›`**, to add one package in 15.9 s.
Adding a package is how a customer installs software, so this is the normal
path, not a one-off.

The lines the customer needs are all already there and get buried:
`✓ Evaluated your config 6.0s`, `✓ Built 18 derivations 2.0s`,
`✓ Switched the machine 5.5s`, `Applied revision 27fbde20 in 16s`. A customer's
first impression of repose is "you are not a trusted user" and a wall of store
paths.

### F5 — Adding a trigger silently took over a live webhook URL (P1, ENG-32)

    $ repose webhooks add council 3000/hook
    https://hooks.repose.run/…/council/<token>
    $ curl -X POST … <that URL>
    HTTP 502 — repose: nothing is listening on port 3000      # the app answers

    $ repose triggers add onhook --route council -p 'read the payload'
    https://hooks.repose.run/…/council/<token>
    No gate: anyone with that URL can start an agent run. `--gate CMD` checks the provider's signature.

    $ curl -X POST … <the same URL>
    HTTP 202                                                   # an agent answers
    $ repose ps
    1:t-onhook   claude   …

`repose webhooks list`'s TARGET went from `:3000/hook` to `trigger onhook`.
A customer whose provider is already POSTing to that route has their app's
deliveries diverted to an agent, and the only output is the URL they already
had and a warning about a different thing.

One door with two receivers is the settled design (I-1360) and the row does
say who answers. What is missing is any line **at the moment the receiver
changes**. The no-gate warning proves the command knows how to warn.

Nearby and **documented, so not a fault**: `--route` on a name that does not
exist creates the route ("created and aimed at the machine if it isn't
there"). It is still a trap — `repose triggers add bad --route nosuchroute`
exited 0 and minted a second public URL where `repose webhooks url
nosuchroute` would have said "no such route" — but it is intended, and it is
recorded here so the next review does not re-raise it.

### F6 — `host ls` and `host ls --json` disagree about what I own (P1, ENG-37)

    $ repose host ls
    You have no hardware of your own.
    $ repose host ls --json
    [ {"name":"e2e-mac","state":"retired"}, {"name":"host-1","state":"retired"},
      {"name":"mac-mini","state":"retired"}, {"name":"host-2","state":"retired"} ]

The docs promise "a host you have removed is not listed", so the table is
right and the JSON is wrong. A script reading `--json` to decide whether to
add a host concludes it already has four — and `repose host rm` on any of
those names answers `You have no host named mac-mini.` (exit 1), so the api
agrees with the table and not with its own JSON.

The JSON also returns a state the docs do not list: a host the table showed as
`waiting` came back as `registering`. The documented set is `waiting`,
`online`, `offline`, `removing`.

### F7 — Nothing warns that a project is being committed to a host that has never registered (P1, ENG-28)

With one owned host on the account in `waiting`, every new project is refused:

    $ repose run -d council-plain
    Creating council-plain...
    Could not start council-plain: your host host-1 has not registered yet (capacity).   # exit 8, ~600 ms

Reproduced four times — no flags, `--size tiny`, `--size large` — and with a
second host name (`mac-mini`) later in the evening. The project record is
created and left in `error`.

**The placement itself is intended** (I-1366: a host a customer owns takes
only that account's projects, and placement defaults to `own`), so a customer
with one Mac would see this and it would be correct. Two things are not:

- **Nothing is said at creation.** The project is committed to a host that has
  never registered with no mention of it, and the refusal arrives at placement.
- **Exit 8 does not mean this.** `repose help exit-codes` defines 8 as free
  memory, the host's disk pool, its build queue, its store, or a host being
  emptied. A host that never registered is none of those.

**It is not a dead end, and I checked before saying so.** `repose start`
recovered all four of my `error` projects once the waiting host was gone —
31 s, 16 s, 37 s, 16 s, exit 0 — and the product names that command itself,
unprompted, once per affected project, in `repose ls`'s footer and again on
`repose status`'s `error:` row:

    council-tiny: your host host-1 has not registered yet. `repose start council-tiny` restarts it.

No `repose rm` is needed. This correction matters because the review was at one
point about to rank a dead end that does not exist.

### F8 — `host add --cpus 0` adds a host instead of refusing (P2, ENG-38)

    $ repose host add --cpus 0
    <join token>                      # exit 0, host-3 created
    $ repose host add --disk banana
    invalid argument "banana" for "--disk" flag: strconv.ParseInt: parsing "banana": invalid syntax

`--cpus 0` is accepted and mints a host, where the same CLI refuses
`--expires 0` with `a number of days, 1 to 365`. And `--disk banana` is
refused with a raw Go error, where every other validator in this CLI writes
prose — `"banana" is not a size like 80G` is this CLI's own wording for the
same mistake, from `repose resize`. host-3 was removed within the minute.

### F9 — The help says "take" and `take` is refused (P2)

`repose --help` and `repose snapshots --help` both say "List, **take** or
restore snapshots". The subcommand is `create`, and there is no alias:

    $ repose snapshots take
    unknown command "take" for "repose snapshots"      # exit 2

### F10 — A scripted `run` in the wrong folder builds a machine without asking (P2, ENG-30)

Interactively this is right — `repose run` in a folder that is not a git
repository asks `… isn't a git repository. Make it one and sync it? [Y/n]`.
With `-d`, or any non-TTY (a script, CI, a pipe), the question is skipped, the
default is to create, and the customer is told
`Not a git repository, so nothing was synced.` after 45 s, with a `large`
machine running. My own first filing of this overstated it as a silent charge;
corrected after retesting under a pty, which is why ENG-26 is parked and
ENG-30 replaces it.

### F11 — `repose say` leaks an internal call name (P2)

    $ repose say "hello are you there"
    Guestd SayToAgent: no agent is running on this machine.      # exit 1

Every other error in this CLI is prose. The same command on a **stopped**
machine gets the good line (`plainfolder is stopped. `repose start
plainfolder` starts it.`), so the leak is specific to this path.

### F12 — Raw ssh and scp output reaches the customer (P2)

- `repose attach` with no terminal prints ssh's own
  `Pseudo-terminal will not be allocated because stdin is not a terminal.`
  before its own clean line, `Attaching needs a terminal, and this command has
  none.`
- `repose cp` of a missing local file: `scp: stat local "…": No such file or
  directory`, **exit 255** — a code `repose help exit-codes` does not list,
  so a customer's script cannot interpret it.
- `repose open` on a machine that goes away under it ends with
  `Connection to ssh.repose.run closed by remote host.`

### F13 — Some lists are tables and some are not (P2)

`ls`, `status`, `tokens`, `github`, `snapshots list`, `webhooks list`,
`triggers list` and `host ls` are aligned tables with headers.
`secrets ls` and `mcp` are headerless tab-separated lines, and `mcp` has a
trailing tab, an empty column with no name. `repose secrets ls` on a project
with no secrets prints **zero bytes** and exits 0, where `repose questions`
says "No questions are waiting in any of your projects." and `repose webhooks`
says "plainfolder has no webhook routes." A customer cannot tell the empty
answer from a broken one.

Related: `repose secrets ls` lists only the project's secrets while the
machine also carries the account's. The machine held six files under
`/run/repose/secrets`; `secrets ls` named one. Answering "which secrets does
my machine have" takes two commands and a union.

### F14 — Revoked tokens stay in the list for good (P2)

    NAME         SCOPES  PROJECT  EXPIRES             LAST USED
    council-ro2  read    all      revoked just now    never
    e2e-check    read    all      revoked 37h02m ago  37h02m ago

The EXPIRES column carries two different meanings, and after a year of CI
tokens the list is mostly dead rows. A revoke prints nothing at all on
success, which is this house's style and defensible — a typo'd name does
exit 1 with `You have no token named council-r0.`, so silence really does mean
it worked.

### F15 — "Interrupted." does not say the machine is still starting (P2)

Ctrl-C during `repose start` exits 130 as documented and prints `Interrupted.`
The start continues server-side and the machine was running 12 s later, which
is correct — but the customer is not told, and may reasonably believe they
cancelled it.

### F16 — A bad `--api-url` leaks a Go error (P2)

    $ repose --api-url notaurl ls
    Could not reach the api: unsupported protocol scheme ""

### F17 — Nothing lets a customer say where a project runs (P1 here, P0 on the dashboard's evidence, ENG-67)

No command in the CLI can choose placement. `repose run --help` has no
`placement`, `hosted` or `fleet` flag, and neither do `start`, `resize`,
`restore` or `snapshots restore`. Once an account owns a host, every new
project goes there (intended, I-1366) and there is no way to ask for repose's
servers.

The docs do offer one escape, at `/docs/own-hardware` line 7:

> A project made through the [API](https://repose.run/docs/api.md) can ask for
> repose's servers instead, with `placement: "hosted"`, if your plan buys
> memory there.

`https://repose.run/docs/api.md` returns HTTP 200 and 11,553 bytes, and
contains the word `placement` **zero times**. So the one documented way out is
an API field that the API reference does not document and that the CLI cannot
send.

**Rank, and whose evidence it rests on.** On my surface this is P1: it costs
the customer a choice, not their work. The `19-council-dash` session measured
the same dead end end to end — identical New project form with and without a
host, no placement field in the request body, the server defaulting to the
waiting host, failure to `error` in 8.0 s with Restart the only control, and a
working recovery at 17.0 s reachable only by deleting the host the customer
bought the Mac for — and ranks it P0. **That measurement is theirs, not mine,
and I have not repeated it.** I did not check whether the absent CLI flag is
deliberate; `own-hardware` implies the API is the intended route, so it may
be. The api.md omission is not deliberate either way.

### F18 — A customer cannot tell which machines are on their own Mac (P2, ENG-68)

A separate research session reported that `repose ls` names the host only
while it is **not** answering, so an online Mac is invisible. **That finding
and that mechanism are theirs.** My own measurement of the CLI half refines
it rather than repeating it:

- plain `repose status` on a running project does not name the host at all;
- `repose status -v` does — `host       host-02, ip 10.64.4.10` — and cli.md
  documents that row ("`-v` adds a `host` row with the server and the
  machine's address"). But `-v` also logs every api request to stderr, so the
  only readout is behind a debugging flag;
- `repose ls --json` carries `host_id`, an opaque uuid, and **no host name
  field**, so a script gets an id and never the name the customer gave their
  Mac.

So there is an indicator, in an awkward place — not "nothing anywhere".

---

## A note on absences

Three of this review's findings are things that are not there, and an absence
is only a finding once you have asked whether it is on purpose. Which ones I
checked:

| Absence | Checked? | Answer |
|---|---|---|
| the `host` command (F1) | yes | not deliberate — v0.1.50 shipped it 21 minutes later |
| a CLI billing or price command (F2) | yes | plausibly deliberate: billing is the dashboard's surface and the CLI cannot take a card. So the finding is narrowed to **naming** the price, not taking payment |
| any price in `host add`'s own output (F2) | **no** | I do not know whether that silence is deliberate. Stated here rather than assumed either way |
| a placement flag (F17) | **no** | `own-hardware` implies the API is the intended route, so the flag may be deliberately absent. `api.md` documenting the field nowhere is not deliberate either way |
| `snapshots take` (F9) | yes | not deliberate — the CLI's own help uses the word "take" |
| the Own hardware plan on the pricing page | yes | **deliberately held** out of the release until this review passes, per the conductor. Excluded, and not counted as a finding |

---

## Which error lines tell a customer what to do, and which leave them guessing

Most of this CLI is in the first group, and it is the best thing about it.
These tell you the rule, what you typed, and the way out:

    NAME must match [A-Z][A-Z0-9_]{0,63}, got "MY SECRET"
    --size must be tiny, small, large or xl, got "medium"
    --temp must be from 10m to 24h, got 5m
    not a schedule: next is not a day; write mon, tue, wed, thu, fri, sat or sun
    plainfolder is stopped. `repose start plainfolder` starts it.
    No repose project is called nope. `repose ls` lists yours.
    This token's scopes are read; this call needs projects (forbidden).
    REPOSE_TOKEN was refused: this API token was revoked.
    council-t2's disk is 20G and can only grow.
    No editor found: repose code opens VS Code (`code`), Cursor (`cursor`) or Zed (`zed`), and none is on your PATH. Other editors: https://…
    repose paste reads the clipboard of a desktop session, and neither WAYLAND_DISPLAY nor DISPLAY is set (over ssh, run it on the computer you copied the image on).
    Could not reach no-such-host.invalid: the name no-such-host.invalid does not resolve.
    repose: nothing is listening on port 3000            (in a 502 body, to the provider)
    Nothing on plainfolder is listening on port 3000 yet; the forward reaches it once it listens on 127.0.0.1 or 0.0.0.0.
    Destroy council-t3? A final snapshot is kept for 30 days. [y/N] → Nothing destroyed.
    Destroyed council-plain in 5.2s. Its final snapshot is kept until 2026-12-19.
    /home/azureuser/.bashrc is not writable, so …/.local/bin is not on your PATH. Add it where that file is generated (home-manager: home.sessionPath = [ … ];), or run: export PATH=…

The read-only-token refusals deserve singling out: they name the scopes the
token has **and** the scope the call needs, which is the one thing a customer
needs to fix it.

These leave them guessing:

| Line | What it does not say |
|---|---|
| `Could not start X: your host host-1 has not registered yet (capacity).` | whether to wait, finish the install, or remove the host; and `(capacity)` is an exit-code family that does not fit (F7) |
| `Guestd SayToAgent: no agent is running on this machine.` | what Guestd SayToAgent is; and how to start an agent (F11) |
| `Removing mac-mini needs a confirmation; pass --yes to skip it.` | that there is no host of that name — the confirmation gate fires before the existence check, so the advice is to re-run a destructive flag for a no-op |
| `Interrupted.` | that the machine is still starting (F15) |
| `Name must match [A-Za-z0-9._-]{1,64} (invalid).` | what `(invalid)` is — an api error code in the middle of prose; and it exits 1 where every sibling validator exits 2 |
| `run takes one PROJECT; put the prompt after -p, quoted` | the real problem, when the one argument given is a name with a space in it. The emoji case gets the right message; the space case is told about `-p`, which it never used |
| `invalid argument "banana" for "--disk" flag: strconv.ParseInt…` | anything, in this CLI's own voice (F8) |
| `scp: stat local "…"` / exit 255 | which exit code family this is (F12) |
| `(blank)` from `repose secrets ls` | whether it worked (F13) |

---

## What held up, and is worth keeping

- **The published start promise.** "about 12 seconds" measured **11.07 s**.
- **Restores are correct.** I thought I had found a restore that lost a file
  and chased it three ways before finding the fault was my own harness: a
  subshell without `~/.local/bin` on `PATH` had silently not taken the
  snapshot I then restored. Verified afterwards with a flushed write and a
  10 s gap (`CONTENT-ONE` came back), and with an unflushed write snapshotted
  immediately (`NOSYNC-THREE` came back). No data was lost in any path I
  tried.
- **Live disk growth.** 10 G → 20 G in 1.5 s, filesystem online, no restart.
- **Idempotence.** `stop` on a stopping machine, `start` on a running one,
  `run` on an existing project, `webhooks add` twice — each answers plainly
  and changes nothing.
- **Triggers end to end.** POST → `202` in 0.33 s → a `claude` window within
  6 s, and the no-gate warning on the way in.
- **Tokens.** `tokens create` puts the token on stdout alone and the human
  line on stderr, so it pipes; scopes are enforced per call with a message
  naming both sides.
- **It names the command that fixes your situation**, once per affected
  thing, without being asked — the `error`-project footers in F7. That is the
  behaviour the trust-the-reader rule is meant to produce.
- **Refusals cost nothing.** Every validation refusal I tried exited before
  creating anything, with two exceptions, both in F8.

---

## What I could not test, and where it does test

- **Whether the Own hardware plan is enforced for an account that is not
  billing-exempt.** The only login on this box is the owner's, which is
  exempt, and the decision is made in `internal/billing/ownhardware.go`'s
  `HostsAllowed`, which returns the exempt allowance before it reaches the
  plan's. Neither reviewer can test the paying path without a second account
  on production, which I did not create. **Recorded as a gap, not a claim in
  either direction.**
- **Any billing figure.** Every project on this account reports
  `cost_today_cents: 0` and `cost_month_cents: 0`, so I could not check that
  anything is charged correctly, including a project left in `error` with a
  disk allocated. The dashboard session sees the Billing page; this one
  cannot.
- **`repose host add` on a Mac**, and therefore the appliance install, the
  four-minute start, the launchd agent, `host run` and `host rm --local`.
  Those need Apple silicon; this box answers
  `The appliance runs on a Mac with Apple silicon; this is linux/amd64.`
  (exit 1) and that is the right answer. It is `19-council-mac`'s surface.
  **Known and unreproduced here, from 19f on the Mac:** v0.1.50 sizes the
  appliance disk to free space minus 20 GB with a 20 GB floor, below the
  image's 37 GiB, so a Mac with under ~57 GB free gets a dead appliance; fixed
  on a branch, not in the released binary. I did not attempt it.
- **`xl`**, and `attach`/`code` as a human uses them — `attach` needs a
  terminal and correctly refuses without one; I drove it under a pty only far
  enough to read its refusals.
- **An expired token.** `--expires` has a one-day floor, so I revoked one
  instead and used it: `REPOSE_TOKEN was refused: this API token was
  revoked.`, exit 3. A token expired by time is untested.
- **A second concurrent customer.** The account was shared with the dashboard
  reviewer throughout, and twice their test hosts and twice mine captured
  new-project placement for under a minute each. Where that interfered I have
  said so.

---

---

## Final ranking, and what moved

The order I stand behind, most severe first. Where a rank changed during the
review, the thing that changed it is named — in three cases it was evidence
that made a finding *smaller*.

**P0**

1. **ENG-23 — the docs promised four commands the released CLI did not have.**
   Found P0, and **closed during the review**: v0.1.50 at 21:00:03 UTC, close
   verified from a customer's seat, card `Done` with that evidence. Its root
   cause is open as **ENG-43** (docs ship on a push, the CLI ships on a tag).
   F1.

**P1**, in order

2. **ENG-39 — `host add` never says what it costs.** My pick of the lot: the
   one to fix before the plan goes on sale, because it is a missing sentence
   rather than a bug, and the same silence is what a paying customer sees. F2.
3. **ENG-67 — nothing lets a customer say where a project runs**, and the one
   documented escape is an API field the API reference does not document. P1
   on my evidence; **P0 on the dashboard session's**, which measured the path
   end to end. F17.
4. **ENG-32 — adding a trigger takes over a live webhook URL.** The only P1
   that silently breaks something a customer already had working. F5.
5. **ENG-40 — "no plan" for a billing-exempt account.** Ranked here and not
   lower because it cost a reviewer a false P0 tonight; a line that misleads
   the people who read it most. F3.
6. **ENG-27 — the nix build log over the summary that already says it.**
   Every `config add`, not just a first run. F4.
7. **ENG-37 — `host ls` says I own nothing, its own `--json` says four.** F6.
8. **ENG-28 — nothing warns at creation, and exit 8 does not mean this.**
   Last of the P1s on my own measurement of the recovery. F7.

**P2**: ENG-68 the host readout only under `-v` (F18) · ENG-38 `--cpus 0`
adds a host (F8) · F9 `snapshots take` refused though the help says take ·
ENG-30 a scripted `run` in the wrong folder (F10) · F11 `Guestd SayToAgent`
leaked · F12 raw ssh/scp output and exit 255 · F13 lists that are not tables,
and a blank `secrets ls` · F14 revoked tokens listed for good · F15
"Interrupted." does not say the machine is still starting · F16 a bad
`--api-url` leaks a Go error.

### Dropped, and what dropped each one

Kept here because a finding dropped in silence is one the next review
rediscovers.

1. **"A restore lost a file that was in the snapshot."** Dropped: **my own
   harness**, not the product. A subshell without `~/.local/bin` on `PATH`
   had silently failed to take the snapshot I then restored. Verified correct
   afterwards with a flushed write and a 10 s gap, and with an unflushed write
   snapshotted immediately. No finding.
2. **"`repose host add` is not gated by the plan, so a customer can use the
   product free."** Filed P0, **withdrawn and parked (ENG-36)**. The owner's
   account is billing-exempt and an exempt account is deliberately allowed one
   host whatever the plans say. The design fact came from the mac-mini
   conductor (`internal/billing/ownhardware.go`, `HostsAllowed`) and the
   exempt status from the dashboard session's reading of `/billing` — **both
   theirs, neither mine**. What survived the withdrawal is ENG-39 and ENG-40.
3. **"`repose run` in a non-repo folder silently builds and charges for a
   `large` machine."** Filed P1, **dropped to P2 as ENG-30** by my own retest
   under a pty: interactively it asks. ENG-26 parked as wrongly titled.
4. **The four restore readings against "about 8 s."** Withdrawn from ENG-13:
   the 8 s is the park and restore of a guest's live memory on a host restart,
   a different operation with no command behind it. The conductor supplied
   that distinction. Numbers kept in "The numbers" as what they actually are.
5. **"`repose open` prints nothing and hangs."** Dropped: it prints three good
   lines. My 5-second timeout raced its output.
6. **"`triggers add --route <typo>` mints a public URL instead of erroring."**
   Not filed: documented in cli.md ("created and aimed at the machine if it
   isn't there"). Still a trap, recorded so the next review does not re-raise
   it.
7. **"`sync` starts a stopped machine without asking."** Not filed:
   documented in sync.md ("It creates or starts the machine first if it needs
   to"). It is the one command that bills you without asking while three
   siblings refuse, which is worth knowing and is not a defect.
8. **"The Own hardware plan is missing from the pricing page."** Not filed:
   deliberately held out of the release until this review passes.

Of eight dropped or demoted items, five were dropped by my own re-measurement
and three by a fact another session held. That ratio is the argument for
conferring.

## Verdict

**The command line is in good shape and should not be what holds the launch.**
Eighteen findings, one P0 and seven P1, and the P0 closed itself at 21:00 UTC
while I was writing. Nothing I did lost a customer's work; the paths that
could — snapshot, restore, stop, resize, destroy — are correct, fast and
honest about what they keep and for how long. The error lines are, with the
eight exceptions tabled above, better than most products ship.

What is not ready is the **seam around the thing being launched**. Four of
the seven P1s are in `repose host` and its billing: the command names no
price, nothing lets a customer place a project anywhere else, the CLI calls an
exempt account "no plan", and `host ls` contradicts its own `--json`.
`repose host` had its first customer contact 21 minutes before I tested it.
It shows.

**My rank, if one P1 is fixed before the plan goes on sale, it is F2:** a
customer should not be able to hand over their hardware without being told
what it costs. That one is not a bug in anything — it is a missing sentence.

**Signed:** the CLI session, `19-council-cli`, branch `council/cli`. I hold
the measurements in "The numbers" and wrote the sentences about them. The
confer is another session's to write; where it restates anything here, this
file is the provenance.
