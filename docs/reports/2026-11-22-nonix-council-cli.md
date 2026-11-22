# No-Nix guests, council first pass: the CLI

Reviewer: council `nonix-cli`, 2026-11-22. Tree under test: `main` at
43906faa, CLI and fake api built from it (`go build ./cmd/repose`,
`./cmd/fakeapi`). Customer: an account whose new machines run the Ubuntu
base (`guest_runtime: erofs`), with one older NixOS machine beside them.

## What would make a user scream

No P0. Nothing I reached loses work or charges wrongly. Ranks follow
docs/REVIEW.md: P1 is fixed before the plan is sold, P2 after.

| # | Rank | What the customer sees |
|---|------|------------------------|
| C1 | P1 | `repose config --global add htop` says "Saved machine.nix to your account", exits 0, and no Ubuntu machine ever gets the package. The file it writes says "every repose machine of your account gets" it. |
| C2 | P1 | `repose scan` on an Ubuntu machine's checkout says the laptop's tools are skipped because of machine.nix, the scripts' commands are skipped because of repose.nix, and node goes "into the machine's nix profile". The Ubuntu machine uses none of these. |
| C3 | P1 (process) | The fake cannot model this program's main states, so the CLI and the dashboard cannot be reviewed or tested against them: no switch for the default runtime, a restore of an Ubuntu machine comes back NixOS, machine.nix fans out to Ubuntu machines, no pending base. |
| C4 | P2 | A failed `repose config add a b c` prints "Added a, b and c" and "✓ a ready", then exit 10; afterwards none of the three is in the menu, and `c` was never tried. Nothing says so. |
| C5 | P2 | `repose config rm redis` on a new Ubuntu machine says "app's repose.nix was written by hand; remove the package with `repose config edit`", and `config edit` is refused on that machine. |
| C6 | P2 | No CLI command says a base update is waiting (`status`, `ls`). The docs send the user to the dashboard's Config tab. |
| C7 | P2 | Nothing in `ls` or `status` says which machine is Ubuntu and which is NixOS, while docs and help branch on "an older machine" and "(Nix machines)". |
| C8 | P2 | Help text still describes Nix behaviour as universal: `--global` on every `config` subcommand ("which every machine of your account gets"), `run --no-personal`, and the menu descriptions of `postgresql` and `vercel`, which contradict config.md. |
| C9 | P2 | Error text: the exit-10 line reads `error: menu_failed: ...`, and config.md shows `error: NAME failed: ...`. A 129-character package name is refused with the character-set rule, and the name has only lower-case letters. `config show` on an empty Ubuntu menu prints nothing. |
| C10 | P2 | `repose host add` on Linux prints only the Mac form of the step. Run on an x86 box, that step says "The appliance runs on a Mac with Apple silicon" and does not name the installer. The installer URL in own-hardware.md on main answers 404 (it is unpublished on purpose; see the gate note). |
| C11 | P2 | Smaller lines: `repose run NAME --temp` twice makes `NAME-2` without asking (undocumented); `repose snapshots app` exits 2 and prints the right command; snapshot ids are short in `list` and full in `create`; `REPOSE_API_URL` without `/v1` says only "Route not found (not_found)"; an unreachable api prints nothing for 30 s; config.md says `stop && start` "does the same" as Restart, but `stop` takes a snapshot. |

Gate note on C10. The x86 installer and the Mac image are absent on
purpose (brief, 2026-11-22, PR 6 still building). But own-hardware.md on
`main` already tells a customer to `curl -fsSL
https://repose.run/install-host.sh | sudo bash`, and that URL answers
`HTTP/2 404` (curl -sI, 11:29 UTC). The live docs page does not carry the
line yet (`curl -s https://repose.run/docs/own-hardware | grep -c
install-host.sh` = 0). So the hold depends on release order: if `main`
ships before PR 6 publishes, the docs point at a 404. Check this before the
next release, not after.

## Findings

Every command below ran with the wrapper `r`: `HOME` and
`XDG_CONFIG_HOME` under `/tmp/rt/council-cli/home`, `REPOSE_API_URL` =
the fake's `.../v1`, `REPOSE_NO_SPINNER=1`, a `credentials.json` with a
12-hour access token (the fake takes any non-empty bearer). "Ubuntu
machine" is `app` (`guest_runtime: erofs` in `status --json`); "Nix
machine" is `legacy` (`guest_runtime: nix`). How I reached each state is
in Method. Times are `/usr/bin/time -f %e` against the fake on localhost,
so they measure the CLI's own overhead and nothing about a host.

### C1 (P1). `config --global add` on an Ubuntu account succeeds and does nothing

Reproduction (fresh fake, default runtime erofs, no projects; this is
what the real api answers for an account whose projects are all erofs,
see "fake fidelity" below):

```
$ r config --global add ripgrep
Added ripgrep to ~/.config/repose/machine.nix.
Saved machine.nix to your account.
exit 0
$ r config --global | head -3
# machine.nix: a home-manager module every repose machine of your account
# gets, beside each project's own configuration.
# https://repose.run/docs/config#your-machine-nix
```

- Expected: a refusal or a one-line warning that no Ubuntu machine gets
  machine.nix, as config.md says ("the account-wide file `repose config
  --global` writes never reaches it").
- Happened: success, exit 0, and a file header that says every machine
  gets it. The link `/docs/config#your-machine-nix` has no heading on
  config.md (grep of `apps/web/src/content/docs/*.md` for that heading:
  none), so it is a dead anchor.
- `config add --help` says "With --global, the names go into the
  home.packages list of your machine.nix, every machine of your account
  gets them"; every `config` subcommand's `--global` says "which every
  machine of your account gets".
- Seconds: 0.00 (fake).
- Instrument: CLI output and exit code. Fidelity: on an account that has
  Ubuntu machines, the fake fans the file out to them too ("app and legacy
  switch in place", and a new revision on `app`). The real api does not:
  its fan-out selects `guest_runtime = 'nix'` only
  (`internal/api/ops/personal.go:91`). I read that line only to decide
  which fake reading to believe. On an Ubuntu-only account the real api
  answers with no projects, and that is the branch reproduced above.
- Falsifier: the real api's PUT /me/config answers a project list that
  includes an erofs project, or the CLI prints a warning when it does not.
  Neither is the case in the tree under test.
- Why P1: the customer is told the package is on every machine. They find
  out it is not when an agent fails, on a machine they believed was set
  up. A customer with plain SSH would at least know they had not installed
  it.

### C2 (P1). `repose scan` describes a Nix machine for an Ubuntu one

Reproduction: checkout of `app` (Ubuntu), with `package.json` pinning
`engines.node` 18. The account has a machine.nix (pushed by an earlier
`run`). The checkout has a committed `repose.nix`.

```
$ r scan
Your laptop's tools (0):
  skipped: your account has a machine.nix, which describes your tools, so none of these is installed
This project (/tmp/rt/council-cli/work/app):
  skipped: repose.nix at the checkout root describes this project's tools, so the commands its scripts run are not installed
  node 18 (package.json engines.node): nodejs_18 goes into the machine's nix profile when its node is another major
```

With `repose.nix` removed, the first `skipped:` line and the nix-profile
line stay. `scan -v` makes one api call, `GET /v1/me/config`, and never
reads the project, so it cannot know the runtime.

- Expected (cli.md): "On an older machine, its configuration files take
  over a half". On an Ubuntu machine, neither file applies, so neither
  half is skipped, and node comes from mise.
- Happened: both halves are skipped, for files the Ubuntu machine never
  uses.
- Seconds: 0.00. Instrument: CLI output, `-v` request log.
- Falsifier: `scan` reads the project and changes its lines for erofs.
  It does not (the `-v` log has no project call).
- Unverified part: scan says it prints "what the next `repose run` sends
  to the machine's tool installer". If `run` follows the same rule, a
  customer with an account machine.nix (every existing customer who used
  `config --global`) gets none of their laptop's tools on a new Ubuntu
  machine, and machine.nix does not reach it either. I could not run
  `run` to the guest (no ssh against the fake; see Method). What stands
  without that: the scan output is false for an Ubuntu machine and
  contradicts cli.md.

### C3 (P1, process). The fake cannot reach the states this program adds

The brief's rule: before you trust the fake, make it produce the state
the finding needs; if it cannot, that is a finding. Four states needed
code to reach, or were wrong when reached:

1. **No Ubuntu machine for a customer.** `cmd/fakeapi` has no flag and no
   admin route for `SetDefaultGuestRuntime`, and the CLI has no runtime
   flag. Every machine `repose run` makes on the stock fake is NixOS. The
   only erofs path is `POST /v1/projects {"guest_runtime":"erofs"}` on
   an exempt account (billing off); with `-billing` it is refused ("this
   account cannot choose the guest runtime"). I reached a billed Ubuntu
   customer only with an uncommitted copy of `cmd/fakeapi/main.go` that
   calls `f.SetDefaultGuestRuntime(os.Getenv("FAKE_RUNTIME"))` and adds
   `/runtime` and `/pending-base` admin routes.
2. **A restored Ubuntu machine comes back NixOS.** `r snapshots restore
   app 0011 --as app-fork` and `r rm app -y; r restore app` both produce
   `"guest_runtime": "nix"` (`status --json`), from an `erofs` source.
   The real api copies the source's runtime
   (`internal/api/http/restore.go:300`, `store.NormalizeRuntime(src.GuestRuntime)`).
   So restore, fork and undo of an Ubuntu machine are untestable on the
   fake, and I did not review them.
3. **machine.nix fans out to Ubuntu machines** (C1): the fake says yes
   where the api says no. This is the "fakes must mirror refusals" failure
   again.
4. **No pending base.** `SetPendingBase` has no admin route. C6 was
   reachable only through my harness.

Also: the fake never answers `pushed: true` on a secret, so "the running
machine has it now" cannot be reached. Its ssh CA is
`@cert-authority ssh.repose.run`, so every ssh step the CLI takes against
the fake dials the **production gateway** (`20.102.98.254:22`, which
resets it: "kex_exchange_identification: read: Connection reset by
peer"). A harness that sends test traffic to production by default is
worth fixing even though no harm came of it here.

- Instrument: `status --json` `guest_runtime`; the fake's admin routes
  (`cmd/fakeapi/main.go` header); CLI `-v` logs.
- Falsifier for 2: a restore on the stock fake that keeps `erofs`. Ran it
  twice (`--as`, and `restore` after `rm`): `nix` both times.
- Why P1: the second pass is the gate, and it reviews against this fake.
  If the fake cannot produce a restored Ubuntu machine, the second pass
  cannot test one either, and every test passes anyway.

### C4 (P2). A failed `config add` says "Added" and "ready" for items it did not keep

Reproduction (fake with `-config-delay 3s`; `repose-fake-missing` is the
fake's failing package; the real machine's equivalent is any name apt
lacks):

```
$ r config add --project app redis repose-fake-missing zig
Added redis, repose-fake-missing and zig to app.
...
✓ redis ready  0.3s
installing repose-fake-missing...
repose-fake-missing failed: E: Unable to locate package repose-fake-missing
error: menu_failed: repose-fake-missing failed: E: Unable to locate package repose-fake-missing
exit 10    (3.02 s)
$ r config show --project app
bun
postgresql
ffmpeg            <- redis is not there; zig is not there
```

- Expected: the first line tells the truth after a failure, and the output
  says which items were not tried. config.md documents the same first
  line ("Added bun and ffmpeg-typo to todo-app."), so this is the
  documented behaviour, but the documented behaviour misleads.
- Unverified consequence: redis was installed on the machine and is not
  in the menu. Whether it comes back after a base update depends on the
  machine's apt record (machine.md: "What you install with `apt-get` is
  recorded"), which I cannot see from the CLI. If the record misses it,
  the customer loses a service they saw "ready". I keep this at P2 until
  someone with a guest checks it.
- Ctrl-C is right: `kill -INT` mid-install exits 130 and prints the
  documented "Interrupted. The install keeps going on the machine.";
  the revision applied 3 s later (`config revisions`).
- Instrument: CLI output, `config show`, `config revisions`. Falsifier:
  `config show` lists redis after the failure. It does not.

### C5 (P2). `config rm` on a new Ubuntu machine points at a refused command

```
$ r config rm --project app redis      # app is new; nothing added yet
redis is not in app's menu.
app's repose.nix was written by hand; remove the package with `repose config edit`.
exit 2
$ EDITOR=true r config edit --project app
This machine runs the Ubuntu base; custom Nix files are not used, and `repose config add <item>` installs packages and services.
exit 1
```

The second line appears only while the menu is empty. After one `config
add`, the same command prints only the first line. The real api also
answers no `menu` for a new erofs project (`internal/api/http/projects.go`
creates it with an empty fragment and no menu), so this is not a fake
artefact. Instrument: CLI output. Falsifier: the line is absent on a new
Ubuntu machine. It is present.

### C6 (P2). The CLI never says a base update is waiting

With `pending_base_version: "2026.11.22"` set on `app` (harness
`/pending-base`), `status --json` carries the field. `r status app` and
`r ls` show nothing about it. config.md says only the Config tab shows
"Base 2026.11.30 is ready". A customer who lives in the terminal does not
learn that a security base waits for a restart. The parity rule is
written for the dashboard; this is the same gap the other way round.
Instrument: CLI output with and without the field.

### C7 (P2). Ubuntu and NixOS machines look the same in the CLI

`r ls` with `app` (erofs) and `legacy` (nix):

```
PROJECT   SIZE   STATE    UP  AGENTS
app       large  running  0m  -
legacy *  large  running  0m  -
```

`status` has no runtime or base line either; only `--json` has
`guest_runtime`. Docs (cli.md, config.md) and help branch on "an older
machine" and "(Nix machines)", so a customer with both cannot tell which
sentence applies. The refusals tell them after the fact. P2 because only
customers from before the cutover have both kinds.

### C8 (P2). Help texts that are false on an Ubuntu machine

Sweep: every command's and subcommand's `--help`, grepped for
`nix|home-manager|machine.nix|flake|closure|build`.

- `--global` on `config show|revisions|edit|apply|add|remove|on|off`:
  "act on your machine.nix, which every machine of your account gets".
  False for Ubuntu machines (C1).
- `run --no-personal`: "keep your machine.nix off this machine", with no
  "(Nix machines)", unlike `config on|off`.
- `config add` with no names, the menu's own descriptions:
  `postgresql` "data lives on the volume under /var/lib/postgresql"
  (config.md: `/var/lib/repose/services/postgresql/16`); `vercel` "from
  npm into ~/.npm-global at the first boot after it is chosen" (config.md:
  the machine installs into what is running).
- `logs --kind build` on the Ubuntu machine prints `build evaluating
  configuration / building / built`. That text comes from the fake, so I
  do not claim the real api does the same. cli.md's "`build` is the last
  configuration build's log" does not say what an Ubuntu machine shows.
- `scan --help`: "the closest nixpkgs has", "A .nix file takes precedence
  over the scan" (C2).

The rest of the `config` help is careful ("(Nix machines)", "on an
Ubuntu machine an apt package").

### C9 (P2). Error lines

- Exit 10 prints `error: menu_failed: NAME failed: E: ...` on a terminal
  and without one. config.md shows `error: ffmpeg-typo failed: E: ...`.
  The `menu_failed:` prefix is a code, not words. The real engine stores
  revision errors as `code + ": " + msg` (`internal/api/ops/phases.go`),
  so the real line likely has the prefix too.
- A 129-character name of only `a`s: `"aaa…" is not an apt package name
  (lower case letters, digits, + - and dots, like ffmpeg or libpq-dev).`
  The reader is told the rule they followed. 128 characters is accepted.
  config.md does not state the length.
- `config show --project app` on a new Ubuntu machine prints nothing and
  exits 0. The help says it prints "its menu on an Ubuntu one".
- Clear refusals, measured: `apt:ffmpeg`, `FFmpeg`, `nodejs_22`,
  `python312Packages.black`, `lib;rm`, `-foo`, `postgresql@15` and `""`
  all exit 2 with the rule and two examples. `pkgs.gcc` is quietly
  accepted as `gcc`, which helps someone with a Nix habit. No `apt:`
  prefix exists anywhere a customer reads; only the recipes file uses it.

### C10 (P2). `host add` on Linux, for an x86 box

```
$ r host add
repose host add --join eyJ2Ijox...
host-1: run this on the machine you are adding. It works once, before Mon 23 Nov 11:29.
$ r host add --join <that argument>      # on this linux/amd64 box
The appliance runs on a Mac with Apple silicon; this is linux/amd64.
exit 1
```

own-hardware.md says the same argument goes to the x86 installer, run as
root. The CLI prints only the Mac form, and the refusal does not name the
installer. A second `host add` while the first host waits says "Your plan
takes one host"; the docs say to `host rm` and add again, so that is
documented. See the gate note at the top about the 404.

### C11 (P2). Smaller lines

- `r run spike --temp 3h -d` twice: the second prints `"spike" is taken;
  trying "spike-2"` and creates a second temporary machine. Not in cli.md.
  A customer who repeats the command to get back to `spike` pays for two.
  Not specific to Ubuntu.
- `r snapshots app` → `app is a project: \`repose snapshots list app\`.`,
  exit 2. The CLI knows what was meant. `snapshots list` shows id
  `00000011`, `snapshots create` prints `Snapshot 01900000-…-000000000020`.
- `REPOSE_API_URL=http://127.0.0.1:PORT` (no `/v1`): `Route not found
  (not_found).` exit 1. Nothing says the path is wrong.
- An api address that drops packets: nothing printed for 30.00 s, then
  `Could not reach …: timed out.`, exit 1.
- config.md: "**Restart** there stops the machine, with no snapshot …;
  `repose stop todo-app && repose start todo-app` does the same." After
  `r stop app`, `status` says "snapshot just now". The two are not the
  same.

## The agent guide

The brief names `images/guest/agent-guide.md`. That path does not exist on
`main`: the guest image branch `ws/nonix-guest-image` (PR 2) is not
merged (`git merge-base --is-ancestor` fails). The Ubuntu guide on `main`
is `internal/cli/testdata/agent-guide.md`. It is byte-identical to the
branch's `images/guest/agent-guide.md`, so I reviewed that, as the agent
that lands on the machine.

- Every `repose` command it names exists in the built CLI: `attach`,
  `browser`, `browser bridge`, `code`, `config add`, `cp`, `exec`, `mcp
  forward`, `reply`, `resize`, `resize --size`, `run`, `secrets import
  --mcp`, `secrets set`, `sync`, `version update` (each `--help` read).
- No sentence I found is false of an Ubuntu machine from what the CLI can
  show. One is Nix advice: "The system `python3` has no Tk; for tkinter,
  use a Python from uv". On Ubuntu, `sudo apt-get install python3-tk` is
  the plain answer. P2.
- The disk rate-limit line lists small, large and xl and omits tiny. Not
  specific to this program. P2.
- Unverified: lines marked `needs: repose-guest-profile` and `needs:
  repose-checkout` drop out when the guest lacks the command. On the image
  branch those names appear only in the guide (`git grep` over
  `ws/nonix-guest-image:images/guest`). If the image does not ship them,
  the agent silently loses the bridge-status and checkout-path lines.
  Someone with a booted image should run `command -v repose-checkout
  repose-guest-profile`.

## What is right, measured

- The Nix-file refusals: `config edit`, `config apply x.nix`, `config on`
  and `config off` on the Ubuntu machine each print config.md's sentence
  word for word and exit 1. `config --global … --project app` exits 2 and
  says why.
- `config add` on a terminal (pseudo-tty via `script`) draws one spinner
  line per item and a `✓ NAME ready  0.4s` line, as config.md shows.
  Exit 10 on a failed install, 130 on Ctrl-C with the documented line.
- The menu: `config add` with no names lists 27 entries in four groups,
  matching config.md's table (10 + 7 + 4 + 6).
- Bad names (C9 list): exit 2 with the rule. Billed customer: `run --size
  xl` and `resize --size xl` on Solo exit 7 with "Your Solo plan runs
  8 GB at once and an xl machine needs 16 GB. Upgrade to Plus at …".
- Tokens: a `read` token doing `config add` gets "This token's scopes are
  read; this call needs config (forbidden)." exit 1; `stop` names
  `projects`. `REPOSE_TOKEN` on `tokens ls` and `run` gets one plain
  sentence each. An unknown token exits 3. An expired login whose refresh
  server is unreachable says "Could not reach the login server
  (127.0.0.1:9): connection refused." `secrets set FOO=bar` explains why
  it refuses (shell history).
- Repeats: `stop` on a stopped machine says "app is already stopped.",
  exit 0; `start` on a running one, likewise. `resize 20G` on an 80G disk:
  "app's disk is 80G and can only grow.", exit 2. `--temp 5m`, `--temp=25h`,
  `--size medium` and `--size` with no value each exit 2 naming the
  range or the four sizes.
- Speed: against the fake, every api-only command took 0.00 to 0.07 s,
  and `config add` with the fake's 3 s delay took 3.02 s. The CLI adds
  about 0.02 s. This says nothing about the 12 s hosted start or an
  install's real time, which need a host.

## Method

- Built: `go build -o /tmp/rt/council-cli/{repose,fakeapi}` from 43906faa
  (5.7 s). The scratch harness `council-scratch/fakeerofs` in the
  worktree is a copy of `cmd/fakeapi/main.go` plus the runtime and
  pending-base switches. It is excluded from git and not committed.
- States: an Ubuntu default with a NixOS machine beside it (harness
  `/runtime` flip around a second `run -d`); billing off (exempt) and
  `-billing` (Solo); `-config-delay 3s` for streamed installs; a pending
  base via the harness.
- Could not run: everything past ssh. `run`/`attach`/`exec`/`ssh`/`cp`
  and `sync` stop at the ssh step against the fake, so I could not check
  sync with an old `repose.nix` in the checkout (STATUS says PR 7 made
  run/sync leave it unsent on erofs; not verified here), a dirty checkout,
  the laptop-tool carry, or what an agent on the machine sees. Real
  guestd install lines, an install on a stopped Ubuntu machine (the fake
  answers "Applied" at once; config.md says it installs at the next
  start), a refresh rejected by the login server, and an expired API
  token (the shortest is 1 day) were not reachable. These need staging,
  which this pass may not use, or a guest.
- Evidence rules that bit: "before you trust the fake, make it produce
  the state" (C3, four times). "Eliminate with evidence from the tree
  under test": I read three api lines on `main` (personal.go:91,
  restore.go:300, projects.go create) only to decide whether a fake
  reading matched the real api, never to explain a CLI fault. "Check
  whether an absence is on purpose": the installer 404 is deliberate, and
  its date matters for release order (gate note).
- My own mistakes, declared: ssh attempts from `run -d`, `sync` and
  `status` dialled the production gateway, because the fake's CA names
  `ssh.repose.run`; about eight attempts, all reset by the gateway. One
  "no network" probe sent a SYN to `10.255.255.1:9`, which is the control
  VM's WireGuard address on this box; I should have used a TEST-NET
  address. Neither changed anything.
- Single readings: every time above is one run, except the config-delay
  figure (3.02 s, twice).

## Second pass (2026-11-22, afternoon)

Tree with the fixes: `main` at 424cafcd, merged into `council/nonix-cli`.
The CLI and the fake api were built from it. The fake ran with
`-default-runtime erofs -config-delay 3s` and the new `/runtime` and
`/pending-base` switches, and the harness from the first pass was not
used. Real machines ran on staging (control plane, host, gateway and CLI
wrapper `~/repose-ws/repose-stg` all at c460b3a2; Ubuntu base
2026.11.22.8; account `stgdev`, exempt), from
`/mnt/nixstore/tmp/council-cli/`.

**Gate: clean. No P0 is open.** Two new P1s are open (N1, N2).

### Verification

| First pass | Now | New behaviour, quoted | Where |
|---|---|---|---|
| C1 `config --global` on Ubuntu | Fixed | Ubuntu-only: "Your machines run the Ubuntu base and don't use machine.nix; `repose config add NAME` installs a package on one." exit 1. Mixed: "…legacy switches in place. … app runs the Ubuntu base and doesn't use machine.nix." Help: "every NixOS machine of your account gets". Template header: "Ubuntu machines don't use it", link `/docs/machine#older-machines`. | fake; staging (refusal) |
| C2 `scan` | Fixed (scan); `run`'s carry not re-run | `scan -v` now reads `GET /v1/projects/{id}`; "node 18 through mise when the machine's node is another version"; no machine.nix or repose.nix skip lines. | fake |
| C3 fake fidelity | Fixed | Ubuntu by flag; restore `--as` and `restore` after `rm` keep `"guest_runtime": "erofs"`; machine.nix no longer fans out to erofs; pending base settable; the CA now names `[127.0.0.1]:1`, so no ssh leaves the box. New gap: N1. | fake |
| C4 failed install | Fixed on the CLI | No "Added …" line on failure; ends "app's menu is unchanged.", exit 10. Residual: the item before the failure is installed and not in the menu (staging: `redis-server` present, `repose-svc-redis` inactive, `redis` absent from `config show`); items after it are not named. | fake; staging |
| C5 `config rm` on empty menu | Fixed | "redis is not in cli-r's menu." exit 2, nothing more. | fake; staging |
| C6 waiting base | Fixed | status: "base 2026.11.30 is ready; app uses it at its next restart" plus a `base 2026.09.15, Ubuntu` row; ls: "app: base 2026.11.30 is ready; …". | fake (staging has no pending base) |
| C7 kind of machine | Partly fixed | status row `base 2026.11.22.8, Ubuntu` (staging) and `…, NixOS` (fake). `ls` with an Ubuntu and a NixOS machine still shows no kind. | fake; staging |
| C8 help texts | Fixed, except `logs --kind build` | `--no-personal`: "on a NixOS machine, keep your machine.nix off it"; catalog: postgresql "under /var/lib/repose/services/postgresql, one folder per major", vercel "Vercel's CLI, from npm." cli.md now says `build` holds "the install lines on an Ubuntu machine"; the real log holds only "build switching the machine" (N1). | fake; staging |
| C9 error lines | Fixed (empty `show` closed by documentation) | "error: repose-fake-missing failed: E: …" (no `menu_failed:`); 129 characters: "is longer than an apt package name can be (128 characters at most)."; cli.md now says `show` prints "nothing when it is empty". | fake |
| C10 `host add` on Linux | Not fixed (CLI half) | Still only `repose host add --join …`; on linux/amd64 the step answers "The appliance runs on a Mac with Apple silicon; this is linux/amd64." with no installer. | fake |
| C11 smaller lines | /v1 hint and restart line fixed; rest not fixed | "http://127.0.0.1:38275 has no /v1 at its end; the api answers at …/v1. Check --api-url or $REPOSE_API_URL." config.md: "`repose stop --no-snapshot todo-app && repose start todo-app` does the same." Still: `--temp` twice → "spk" is taken; trying "spk-2"; `snapshots app` exit 2; 30.03 s of silence before "timed out" (192.0.2.1). | fake |
| Guide | Fixed in source, not in the published base | `internal/cli/testdata` and `ws/nonix-guest-image` say "For tkinter, `sudo apt-get install -y python3-tk`"; the guide on a staging machine (base 2026.11.22.8) still says "The system `python3` has no Tk". The `needs:` commands are all on the machine (`repose-checkout`, `repose-guest-profile`, `repose-presence`, `repose-notify`, `repose-ask`, `repose-mcp`, `mise`, `apt-get`), so that first-pass doubt is closed. | staging `exec` |

No finding regressed.

### New findings

**N1 (P1). A real install says nothing until it ends, and a failure gives
no reason.** Both reviewers saw it on two machines (cli-a here, tmp-vffw
for the dashboard reviewer).

```
$ repose-stg config add --project cli-a postgresql bun ffmpeg
Added postgresql, bun and ffmpeg to cli-a.
Applied revision 38933074 in 1m10s.          (70.03 s, nothing in between)
$ repose-stg logs cli-a --kind build
2026-11-22T13:32:21Z build switching the machine
$ repose-stg config add --project cli-a redis ffmpeg-typo zig
error: ffmpeg-typo failed: apt-get exited 100
cli-a's menu is unchanged.                    (15.71 s, exit 10)
```

On a pseudo-terminal (`script`), `config add deno` drew no spinner and no
item line in its 8.0 s. config.md shows "✓ postgresql 16 ready on
127.0.0.1:5432 14s" and "E: Unable to locate package ffmpeg-typo", and
troubleshooting.md tells the reader to look for that apt line. The fake
streams "installing X / X ready" and the apt line, so every test that uses
it passes while a customer stares at nothing for a minute. The installs
themselves are right: PostgreSQL 16.16 answers `psql -h 127.0.0.1 -U dev
-d dev`, `repose-svc-postgresql` is active, `/var/lib/repose/services/postgresql/16`
exists, and bun, deno and ffmpeg are on the path. Falsifier: one real
install that prints a per-item line. None of four did (two here, two for
the dashboard reviewer). Against plain SSH, where `apt-get` shows its own
progress, a silent minute is worse.

**N2 (P1). `repose sync` after `run --temp` makes a second, permanent,
large machine without asking.**

```
$ cd repo          # git checkout, committed repose.nix, no remote
$ repose-stg run cli-r --temp --size small -d      # 6.78 s, fine
$ (edit two files) ; repose-stg sync
Creating repo...
✓ Created repo (large)  5.5s
...
Synced: 2 modified, 1 untracked (1 new commit)  0.3s
```

The temporary machine is never written to the laptop's project cache
(documented), so the next `sync` in that folder finds no project and
creates one named after the folder, at the default size, not temporary.
On a terminal it does not ask either (fake, under `script`: "✓ Created
fresh (large)"). The customer's changes went to `repo`, not to `cli-r`
where they were working. `sync --help` says "creating or starting the
machine if needed", and lifecycle.md says `--temp` "never uses the
checkout's project". Each sentence is documented, but nothing warns about
what they do together. I reached it on an exempt account. On Solo, with
the 4 GB temporary machine running, an 8 GB large one would be refused
with exit 7, which is a confusing answer to `sync`. On Plus it would be
made silently. Not specific to Ubuntu. Falsifier: `sync` asks before
creating, or picks the temporary machine. It did neither.

**N3 (P2). A snapshot id as shown is refused when typed by its start.**
`snapshots list cli-a` shows `8b287420`, the last 8 characters of
`01a20377-…-0cd38b287420`. `snapshots restore cli-a 8b28 --as cli-b` →
"cli-a has no snapshot 8b28." exit 2, and `8b2874` is refused too, while
`7420` and `8b287420` work. The help says "4 or more of its first or
last characters", and the customer sees neither end of the real id.

**N4 (P2). Restoring a temporary machine's snapshot makes a permanent
machine.** `snapshots restore cli-a 8b287420 --as cli-b` (cli-a temporary)
made `cli-b` with no `LEFT`, and nothing said so. That is a second way to
end up with a machine that keeps running.

**N5 (P2). Nix-era words on a real Ubuntu machine's operations.**
`logs --kind ops`: "build done 1m10s"; `config revisions`: the first
revision's status is `built`; the build log: "switching the machine".
`sync` on an all-Ubuntu account still prints "Not pushing
~/.config/repose/machine.nix: the copy on your account was saved
elsewhere …" (fake), about a file no machine of theirs uses.

Real timings, for the record (one reading each unless stated): create
6.0 s ("Created … 6.0s"), `run -d` total 6.40 s and 6.78 s, against the 6 s
reference. Start after a stop: boot 5.3 s, 12.97 s with the wait for the
stop. `resize --size tiny`: 11.36 s, boot 8.8 s. `restore --as`: 12.76 s
and 14 s; the first pass had no figure for this, and the reference is
about 8 s, so it is 5 to 6 s slower. Menu installs: 70.03 s
(postgresql+bun+ffmpeg on a fresh machine), 8.0 s (deno), 15.71 s (failed).

### Method, second pass

- Ran: every CLI row above on the fake. On staging: create, `status`,
  `config add` (success, failure, on a pseudo-terminal), `config rm`,
  `config edit`, `config --global`, `logs`, stop/start, `snapshots
  restore --as`, `resize --size`, `run --temp` in a git checkout with
  `repose.nix`, `sync` with a dirty checkout, and `exec` checks on the
  machine.
- Could not run: `run`'s laptop-tool carry with an account machine.nix
  (an Ubuntu-only account now refuses to make one, which is the C1 fix);
  a pending base on staging; the dashboard against staging (the partner
  found the staging login 404s).
- Rules that bit: "fakes must mirror refusals" again, the other way round.
  The fake now models states it did not, but it invents install output the
  real system does not produce (N1). "Do not rank from the only state you
  could reach": N2 was measured on an exempt account, and its Solo and
  Plus outcomes are reasoned, not run.
- My breaches: staging held four machines twice, for about a minute after
  `restore --as cli-x` (I ran a second restore to test a suffix id) and
  for about 30 s after N2's unexpected create. I destroyed them at once.
  All my machines (cli-a, cli-b, cli-x, cli-r, repo) are destroyed. Their
  final snapshots stay in `ls --destroyed` for 30 days, and the CLI cannot
  purge them.
