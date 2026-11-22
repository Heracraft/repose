# No-Nix guests, council first pass: the confer

Reviewers: `nonix-cli` (CLI, writes this file) and `nonix-dash` (dashboard,
agent a1be50b621bfb24d9, signs below). Tree: `main` at 43906faa, against the
fake api. Surface reports: `docs/reports/2026-11-22-nonix-council-cli.md`
and the dashboard reviewer's `docs/reports/2026-11-22-nonix-council-dash.md`.
Each finding names whose evidence it rests on. Where both measured it, both
are named. A claim here is no stronger than the surface report it cites.

## What would make a user scream

No P0 from either surface. Nothing either reviewer reached loses work or
charges wrongly.

| # | Rank | Finding | Evidence |
|---|------|---------|----------|
| 1 | P1 | The dashboard cannot add an apt package. Config > Packages search "ffmpeg" says `No package matches "ffmpeg"`. The CLI's `repose config add ffmpeg` works (3.0 s with the fake's 3 s delay). config.md says packages are added "from the CLI or the dashboard". Breaks the parity rule. | dash (Playwright); CLI half: cli (3.02 s, C4 setup) |
| 2 | P1 | `repose config --global add X` on an Ubuntu account: "Saved machine.nix to your account", exit 0, and no Ubuntu machine gets X. The file it writes says every machine gets it. | cli C1 |
| 3 | P1 | `repose scan` on an Ubuntu checkout skips both halves for machine.nix and repose.nix and sends node "into the machine's nix profile". It never reads the project's runtime. Whether `run` follows it is unverified (no ssh). | cli C2 |
| 4 | P1 | A failed install is out of sight on the dashboard: after Apply the footer returns to "1 selected", the `menu_failed` line is 746 px below the fold at 1440x900 and 1332 px at 390x844, and the ticked item unticks. On the CLI the same failure prints "Added a, b and c" and "✓ a ready", and afterwards none of them is in the menu (P2 on its own; one fault on two surfaces). | dash D2 (boundingBox); cli C4 |
| 5 | P1 | Reload or back/forward during an install drops the progress: the page offers "2 selected / Apply" while the op still runs. | dash D3 (fake `-config-delay 3s`) |
| 6 | P1 | Two tabs: a stale tab's Apply during another tab's install sends a menu without the item being installed ("removing nats"). The first tab ends "Applied." with NATS still ticked. No conflict check in the fake; the real api is unconfirmed. | dash D4 |
| 7 | P1 | The dashboard's Resize only grows the disk; it cannot change size class (`repose resize --size`). Parity. | dash D5 |
| 8 | P1 (process) | The fake cannot reach this program's states: no runtime switch (every CLI- or dashboard-made machine is NixOS; erofs only via `POST /v1/projects` on an exempt account); restoring an Ubuntu machine yields NixOS (real api copies the runtime, restore.go:300); machine.nix fans out to Ubuntu machines (real api excludes them, personal.go:91); no pending-base route; start never moves `base_version` to the pending one. The second pass cannot test what the fake cannot produce. | cli C3 (restore: CLI `--as` and `restore`, twice); dash confirms restore from the Snapshots tab and the no-runtime-choice create |
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

## Disagreements and how they were settled

- None on substance. The dashboard reviewer did not reproduce the CLI's
  machine.nix fan-out (cli C3 item 3) or the production-gateway ssh
  (cli C3, last paragraph). Both are CLI-only paths, and the dashboard
  reviewer chose not to touch ssh. They stay on the CLI's evidence alone.
- The CLI's C4 is P2 on its own; merged with the dashboard's D2 it is
  P1, because the dashboard hides the failure and the CLI misstates it.
  Both reviewers' readings are kept above.
- D7 was offered as "P1?". Both reviewers rank the installer 404 as
  deliberate. The sharper claim is release order, so it is P2 now and
  becomes P1 if main ships before PR 6 publishes the installer.

## Unreproduced, kept

- Whether `run` on an Ubuntu machine skips the laptop-tool carry when the
  account has a machine.nix (cli C2). Needs ssh to a guest.
- Whether the real api refuses a concurrent config PUT (dash D4).
- Whether an item installed before a failed `config add` survives a base
  update (cli C4).

## Signatures

- nonix-cli: written 2026-11-22, from the two surface reports.
- nonix-dash: (to sign, or to record disagreement here)
