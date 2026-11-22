# No-Nix guests, council first pass: the confer

Reviewers: `nonix-cli` (CLI, writes this file) and `nonix-dash` (dashboard,
agent a1be50b621bfb24d9, signs below). Tree: `main` at 43906faa, against the
fake api. Surface reports: `docs/reports/2026-11-22-nonix-council-cli.md`
and the dashboard reviewer's `docs/reports/2026-11-22-nonix-council-dashboard.md` (branch council/nonix-dash, 8116e6ea).
Each finding names whose evidence it rests on. Where both measured it, both
are named. A claim here is no stronger than the surface report it cites.

## What would make a user scream

No P0 from either surface. Merged: 0 P0, 9 P1, 18 P2. Nothing either reviewer reached loses work or
charges wrongly.

| # | Rank | Finding | Evidence |
|---|------|---------|----------|
| 1 | P1 | The dashboard cannot add an apt package. Config > Packages search "ffmpeg" says `No package matches "ffmpeg"`. The CLI's `repose config add ffmpeg` works (3.0 s with the fake's 3 s delay). config.md says packages are added "from the CLI or the dashboard". Breaks the parity rule. | dash (Playwright); CLI half: cli (3.02 s, C4 setup) |
| 2 | P1 | `repose config --global add X` on an Ubuntu account: "Saved machine.nix to your account", exit 0, and no Ubuntu machine gets X. The file it writes says every machine gets it. | cli C1 |
| 3 | P1 | `repose scan` on an Ubuntu checkout skips both halves for machine.nix and repose.nix and sends node "into the machine's nix profile". It never reads the project's runtime. Whether `run` follows it is unverified (no ssh). | cli C2 |
| 4 | P1 | A failed install is out of sight on the dashboard: after Apply the footer returns to "1 selected", the `menu_failed` line is 746 px below the fold at 1440x900 and 1332 px at 390x844, and the ticked item unticks. On the CLI the same failure prints "Added a, b and c" and "✓ a ready", and afterwards none of them is in the menu (P2 on its own; one fault on two surfaces). | dash D2 (boundingBox); cli C4 |
| 5 | P1 | Reload or back/forward during an install drops the progress: the page offers "2 selected / Apply" while the op still runs. | dash D3 (fake `-config-delay 3s`) |
| 6 | P1 | Two tabs, sequential, independent of the fake: A applies RabbitMQ to "Applied."; 10 s later B still shows RabbitMQ unticked; B ticks Ruby and applies; the log says "removing rabbitmq / rabbitmq removed" and the menu goes from [bun,deno,memcached,rabbitmq,ffmpeg] to [bun,deno,ruby,memcached,ffmpeg]; A still shows it ticked. No revision precondition found in the real PUT handler (located, not run on the real api). The concurrent variant is a fake divergence: the real api answers 409 "a build is in progress" (internal/api/http/config.go:157, located, not run), the fake accepts it (row 8, F5). | dash D4 |
| 7 | P1 | The dashboard's Resize only grows the disk; it cannot change size class (`repose resize --size`). Parity. | dash D5 |
| 8 | P1 (process) | The fake cannot reach this program's states: no runtime switch (every CLI- or dashboard-made machine is NixOS; erofs only via `POST /v1/projects` on an exempt account); restoring an Ubuntu machine yields NixOS (real api copies the runtime, restore.go:300); machine.nix fans out to Ubuntu machines (real api excludes them, personal.go:91); no pending-base route; start never moves `base_version` to the pending one; a config PUT during a running op is accepted where the real api answers 409 (F5). The second pass cannot test what the fake cannot produce. | cli C3 (restore: CLI `--as` and `restore`, twice); dash confirms restore from the Snapshots tab and the no-runtime-choice create |
| 8a | P1 | The landing page's "Everything installed" lists Nix and says "Add any Nix package to every rebuild with repose config add". Both are false for every new machine, on the first page a customer reads. | dash L1 (page text, capture 40-landing-installed) |
| 9 | P2 (P1 if it ships before PR 6) | The x86 installer line (`curl -fsSL https://repose.run/install-host.sh \| sudo bash …`) is on own-hardware.md on main and in the dashboard's Add a host dialog. The URL answers 404. Withheld on purpose (PR 6), but it goes live with the next release of main unless PR 6 lands first. The dialog does not say the installer wipes the second disk. The CLI on Linux prints only the Mac form of the step, and on x86 that step refuses without naming the installer. | dash D7; cli C10 + gate note |
| 10 | P2 | A waiting base update is invisible in `repose status`/`ls` and on the dashboard's overview and project list; only the Config tab shows it. | cli C6; dash (route interception) |
| 11 | P2 | `repose config rm X` on a new Ubuntu machine says to use `repose config edit`, which is refused there. | cli C5 |
| 12 | P2 | Nothing in the CLI says which machine is Ubuntu and which NixOS, while docs and help branch on "older machine". | cli C7 |
| 13 | P2 | PostgreSQL's menu description says data lives under `/var/lib/postgresql`; config.md says `/var/lib/repose/services/postgresql/16`. The same catalog text shows on both surfaces. `vercel`'s says "at the first boot after it is chosen". | cli C8; dash (copy) |
| 14 | P2 | Help text still universal-Nix: `--global` on every config subcommand, `run --no-personal`, `scan --help`; `logs --kind build` shows Nix build lines on the fake. | cli C8 |
| 15 | P2 | Error lines: `error: menu_failed: …` vs config.md's `error: NAME failed: …`; a 129-character name refused with the character rule; empty Ubuntu `config show` prints nothing. | cli C9 |
| 16 | P2 | Revisions offers Re-apply on a failed revision, then a toast "revision: only a successful revision can be re-applied" (API 400). | dash D6 |
| 17 | P2 | Smaller CLI lines: `run NAME --temp` twice makes `NAME-2` without asking; `snapshots app` exits 2; short vs full snapshot ids; no hint for an api URL missing `/v1`; 30 s of silence before a timeout; config.md's "stop && start does the same" (stop snapshots). | cli C11 |
| 18 | P2 | Agent guide: tkinter advice is Nix-era; disk-limit line omits tiny; `needs: repose-checkout` / `repose-guest-profile` lines may drop if the image does not ship those commands (unverified). The guide's brief path `images/guest/` is not on main (PR 2 unmerged). | cli, guide section |
| 19 | P2 | A failed revision on Config shows only "failed"; the reason is only in Overview events. | dash D8 |
| 20 | P2 | Refusals are raw API toasts; the real busy refusal says "a build is in progress" on a machine that builds nothing. | dash D11 |
| 21 | P2 | One Escape closes Add a host and loses the join step it says is "not shown again". | dash D12 |
| 22 | P2 | own-hardware.md lines 53-55 (disk.raw, ~/Library/Logs, "three quarters of the Mac's memory") sit in the x86 flow; no x86 reboot or removal guidance; the landing says "x86 box with Linux and KVM" where the docs need Ubuntu 24.04 and a second disk that is wiped. | dash D13 |
| 23 | P2 | At 390 px the project tab strip puts Config at x=445; the open tab is not visible and nothing shows it scrolls. | dash D14 |
| 24 | P2 | /settings/machine-nix is reachable and has Save on an Ubuntu-only account (the dashboard twin of row 2). | dash D15 |
| 25 | P2 | machine.md:277 "until they are migrated" gives no way to migrate. | dash D16 |
| 26 | P2 | "waiting" and "has not registered yet" are two names for one host state. | dash D17 |

For the conductor, not ranked (dash): the brief says the own-hardware price is not on the public page, but own-hardware.md, billing.md and the landing page say $10/month since 45772b28 (2026-11-21, ENG-47). The brief's list of deliberate absences is out of date here; settle which is current.

## Disagreements and how they were settled

- None on substance. The dashboard reviewer did not reproduce the CLI's
  machine.nix fan-out (cli C3 item 3) or the production-gateway ssh
  (cli C3, last paragraph). Both are CLI-only paths, and the dashboard
  reviewer chose not to touch ssh. They stay on the CLI's evidence alone.
- The CLI's C4 is P2 on its own; merged with the dashboard's D2 it is
  P1, because the dashboard hides the failure and the CLI misstates it.
  Both reviewers' readings are kept above.
- D7 was offered as "P1?"; the dashboard reviewer agreed to P2. Both reviewers rank the installer 404 as
  deliberate. The sharper claim is release order, so it is P2 now and
  becomes P1 if main ships before PR 6 publishes the installer.

## Unreproduced, kept

- Whether `run` on an Ubuntu machine skips the laptop-tool carry when the
  account has a machine.nix (cli C2). Needs ssh to a guest.
- Whether the sequential two-tab overwrite (row 6) happens on the real api; the concurrent case is refused there with 409 (located, not run).
- Whether an item installed before a failed `config add` survives a base
  update (cli C4).

## Signatures

- nonix-cli: written 2026-11-22, from the two surface reports.
- nonix-dash: signed 2026-11-22, with corrections and additions, all applied above: (1) row 6 rests on the sequential case, and the concurrent case is fake divergence F5; (2) the missing P1, L1, is row 8a; (3) P2s D8 and D11 to D17 are rows 19 to 26; (4) agrees with D7 at P2, P1 if main ships before PR 6; (5) the price note goes to the conductor. Dashboard counts: 0 P0, 6 P1 (D1-D5, L1), 11 P2, process findings F1-F5.

## Second pass (2026-11-22, afternoon)

Tree: `main` at 424cafcd for the CLI, the dashboard and the fake. Real
machines on staging at c460b3a2 (Ubuntu base 2026.11.22.8). The staging
dashboard login does not complete (`/oidc/auth` answers 404), so every
dashboard reading is against the fake (`-default-runtime erofs`). Surface
detail: the "Second pass" sections of the CLI and dashboard reports.

**Gate: clean. No P0 is open.** Nothing regressed. Second-pass new findings: 2 P1 (N1, N2), 9 P2 (N3-N11), 1 unverified (N12); the not-fixed P2s below stay open.

### Verification

| Row | Finding | Now | Evidence |
|---|---|---|---|
| 1 | Dashboard adds an apt package | Fixed: "No package matches "ffmpeg". An apt package: ffmpeg [Add]"; Apply gives menu `[{"package":"ffmpeg"}]`. ffmpeg installs on a real machine. | dash (fake); cli (staging) |
| 2 | `config --global` on Ubuntu | Fixed: refused on an Ubuntu-only account, exit 1; on a mixed account it names the Ubuntu machines. | cli (fake, staging) |
| 3 | `scan` | Fixed for scan ("node 18 through mise"). `run`'s carry not re-run. | cli (fake) |
| 4 | Failed install hidden/misstated | Fixed on both surfaces: the dashboard footer "Not installed: …" is in view at 1440 and 390; the CLI ends "menu is unchanged." Residual on both: the item before the failure is installed and unticked (staging: redis-server present, not in menu). | dash; cli |
| 5 | Reload mid-install | Fixed: "Installing… / Applying…" then "Applied" with the log. | dash |
| 6 | Stale tab overwrites | Fixed: "The menu changed elsewhere since you loaded it, and is reloaded." The concurrent case gets the same refusal. B's own tick is dropped and must be redone. | dash |
| 7 | Resize changes the class | Fixed: Size select, confirm, stop{snapshot:false} → PATCH → start; 4.7 s on the fake. | dash |
| 8 | Fake fidelity | F1, F2, F3, F5 and the pending base fixed; gateway now local. F4 partly fixed: restore-as-new keeps erofs, but the fake drops the menu ("0 selected" for a 6-item source; the real api copies it). New gap: the fake streams install lines the real system does not (N1). | cli; dash |
| 8a | Landing page says Nix | Fixed: "Every machine runs Ubuntu 24.04. … any apt package, with repose config add." | dash |
| 9 | x86 installer | Copy fixed on the dashboard and in the docs. The CLI is not fixed: it prints only the Mac step, and on x86 refuses without naming the installer. The URL still 404s on main; it exists on release/r20261122-10 (contains 424cafcd), so it ships with that batch. | dash; cli |
| 10 | Waiting base | Fixed on both: status/ls lines; Overview "Base 2026.09.15 · 2026.11.30 at the next restart", list "new base at next restart". | cli; dash (fake) |
| 11 | `config rm` hint | Fixed. | cli (fake, staging) |
| 12 | Kind of machine | Partly fixed: status `base 2026.11.22.8, Ubuntu`; `ls` still shows no kind. | cli |
| 13 | Catalog text | Fixed on both. | cli; dash |
| 14 | Help texts | Fixed, except `logs --kind build`: the docs now promise install lines, but the real log has only "switching the machine" (N1). | cli |
| 15 | Error lines | Fixed (`menu_failed:` prefix gone, length named); empty `show` closed by documenting it. | cli |
| 16 | Re-apply on a failed revision | Not fixed. | dash |
| 17 | Smaller CLI lines | /v1 hint and restart line fixed. `--temp` twice, `snapshots app`, the 30 s silent timeout: not fixed. | cli |
| 18 | Agent guide | Fixed in source; the published base 2026.11.22.8 still ships the old tkinter line. All `needs:` commands exist on the machine. | cli (staging `exec`) |
| 19-26 | D8, D11-D17 | Not fixed (STATUS line 775 lists them as not done). | dash |

### New findings, ranked

| # | Rank | Finding | Evidence |
|---|---|---|---|
| N1 | P1 | A real `config add` prints nothing between "Added" and "Applied" (cli: 70.03 s, 15.71 s, 8.0 s on a pseudo-terminal; dash: 66.5 s, 20.5 s). `logs --kind build` holds one line, "build switching the machine". A failure says "apt-get exited 100", not the documented "E: Unable to locate package …". The dashboard's install log has no case for that line, so it would show only that line for a minute (located in apps/web, not rendered against staging). The fake streams lines the real system does not. | cli (cli-a), dash (tmp-vffw) |
| N2 | P1 | `repose sync` in a checkout whose machine was made with `run --temp` creates a new, permanent, large machine named after the folder, without asking (on a terminal too), and the work goes there. Measured on an exempt account; on Solo it would refuse with exit 7, on Plus it would succeed silently (reasoned, not run). | cli (staging, fake) |
| N3 | P2 | `snapshots list` shows the last 8 characters of the id; typing the first 4 of what is shown is refused ("has no snapshot 8b28"). | cli (staging) |
| N4 | P2 | Restoring a temporary machine's snapshot `--as` makes a permanent machine, and nothing says so. | cli (staging) |
| N5 | P2 | Nix-era words on real Ubuntu operations: op kind `build`, revision status `built`, "switching the machine"; `sync` warns about pushing machine.nix on an all-Ubuntu account. | cli (staging, fake) |
| N6 | P2 | Add a host says "On an x86 box with Ubuntu 24.04"; own-hardware.md says "Ubuntu 24.04, or Debian 12 or later"; the landing page says "Linux and KVM". | dash |
| N7 | P2 | The Resize panel always says "320 GB is the largest size." while 80, 160 and 320 GB are all on offer. | dash |
| N8 | P2 | The dashboard's menu search does not match the entry names config.md's table lists and `repose config add` takes: `dotnet`, `awscli`, `go-tools`, `shell-extras` and `nodejs` each give `No package matches` and no Add row, because I-1508 hides Add for a menu id. A customer who copies a name from the docs reaches a dead end. | dash (fake, capture h-search-dotnet) |
| N9 | P2 | An invalid apt name is refused without a reason: `FFmpeg`, `Bad Name!`, `ffmpeg libpq-dev` and a 130-character name each give `No package matches "…"`, with no Add row and no rule. config.md:23 states the rule; the page never does. | dash (fake) |
| N10 | P2 | "Applied" on a stopped machine means applied at the next start. On staging, with tmp-vffw stopped, `config add zig` printed `Applied revision d5662d1d in 0.5s`. Nothing was installed until `start` (16.1 s, against 6 s), and after it zig was in mise's installs. config.md:25 documents it, but neither surface says it at the moment. The fake streams install lines and "Applied" for a stopped machine, so the dashboard's real wording here cannot be seen. | dash (staging CLI, fake) |
| N11 | P2 | For a machine on an owned host, the Snapshots tab says only "No snapshots yet. / Create snapshot". own-hardware.md puts in bold that these snapshots stay on that host's disk and repose keeps no copy; the tab says nothing. | dash (fake, arm64 host "mini") |
| N12 | unverified | On the fake, Apply on a machine whose owned host has been quiet for 14 minutes answers "Applied" and streams install lines. The real api's behaviour is not checked. | dash (fake) |

The dashboard reviewer's free-hunt rows are N8 to N12.

### Method

- CLI: fake from 424cafcd, and staging through `repose-stg`, at most two
  machines of mine at a time by agreement (the dashboard reviewer took
  one). That cap was broken twice, briefly; see the CLI report.
  Everything I made is destroyed. Partner's `tmp-vffw` was still running
  at the time of writing; it is now destroyed, and staging `ls` says
  "No projects for stgdev" (dashboard reviewer).
- Dashboard: fake with Playwright and nix Chromium at 1440 and 390; one
  staging machine for the CLI-side install check.
- Both: N1 is two reviewers on two machines, four installs. Every other
  new finding is one reviewer's.

### Signatures

- nonix-cli: written 2026-11-22.
- nonix-dash: signed 2026-11-22. The verification rows marked dash and N1, N6 and N7 match my second-pass report (f40fb3dc). N8 to N12 are mine and are added above. I agree the gate is clean: no P0 is open, and nothing regressed. One note on row 9: the brief's 'the x86 installer fails at its host daemon download step' is true of release/r20261122-10, not of main, where the script itself answers 404.
