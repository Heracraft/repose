# User docs audit (DECISIONS I-242, 2026-09-24)

> One-off audit of 2026-09-24 (I-242); `internal/cli/docs_test.go` now enforces the CLI, config.toml, environment-variable and exit-code parts, and `docs/CHECKLIST.md` the rest. Not maintained.

Every user-visible behaviour on main at a2aad99, checked against the 14
pages of `apps/web/src/content/docs/` (served at `/docs/<page>`). Status:
**ok** was already documented and right, **added** was missing, **fixed**
was wrong, **code** changed code instead. Anchors are the page's heading
slugs. Items that were ok and are plainly in `cli.md` (each command's own
heading or row) are listed once as a group; `internal/cli/docs_test.go`
keeps that group honest from now on.

## CLI (`internal/cli`, cobra tree walked by `TestDocsNameEveryCommandAndFlag`)

| Item | Status | Where |
|---|---|---|
| run, attach, open, cp, scan, projects, status, start, stop, destroy, restore, logs, events, snapshots list/create/restore, secrets set/list/rm, config add/remove(rm)/show/edit/apply, login, logout, notify set/test, version, completion and all their flags except those below | ok | cli (each command's heading or row) |
| `repose resize SIZE` (was hidden) | code + added | code: unhidden. cli#repose-resize-size, machine#memory-and-disk |
| `repose __session` (hidden) | stays hidden | internal helper the CLI starts beside an attach; allowlisted with that reason in docs_test.go |
| `repose help [COMMAND]` | added | cli#account, cli intro |
| `repose mcp forward`, `repose browser bridge` (reserved) | fixed | were one prose line; now rows in cli#account |
| `--api-url` (global) | added | cli#which-project, cli#other-servers |
| `--verbose` long form of `-v` | added | cli#which-project |
| `cp --recursive`, `logs --follow`, `events --follow` long forms | added | cli |
| `login --browser` (loopback PKCE, needs a Logto app with loopback URIs; the hosted one has none, I-101) | added | cli#account, cli#other-servers |
| `login --no-browser` (the default since v0.1.2, kept for scripts) | added | cli#account |

## config.toml (`Config` struct, `TestDocsNameEveryConfigKey`)

| Key | Status | Where |
|---|---|---|
| `default_class`, `default_agent`, `sync.exclude` | ok | cli#configtoml (now also a key table) |
| `api_url`, `logto_issuer`, `logto_client_id` | added | cli#configtoml, cli#other-servers |
| `gateway` | code | decoded, never read: removed; old files still load |

## Environment variables (`userEnvVars` in env.go, `TestDocsNameEveryEnvVar`)

| Variable | Status | Where |
|---|---|---|
| `REPOSE_PROJECT`, `REPOSE_NO_FORWARD`, `REPOSE_TIMING`, `REPOSE_NO_SPINNER` | ok | cli#environment-variables |
| `REPOSE_API_URL`, `REPOSE_NO_FASTPATH`, `REPOSE_NO_BROWSER` | added | cli#environment-variables |
| `REPOSE=1` (set in every guest; login never opens a browser there) | added | cli#environment-variables |
| `XDG_CONFIG_HOME`, `CLAUDE_CONFIG_DIR`, `VISUAL`/`EDITOR`, `TERM=dumb` | added | cli#environment-variables |
| `REPOSE_SESSION`, `REPOSE_TEST_GOOS`, `REPOSE_CLAUDE_PLATFORM` | internal | allowlisted in docs_test.go with reasons |

## Exit codes (`Exit*` in exitcode.go, `TestDocsListEveryExitCode`)

| Item | Status | Where |
|---|---|---|
| 0-8, 10 | ok | cli#exit-codes |
| 130 on Ctrl-C | code + added | `ExitInterrupted`; cli#exit-codes |
| ssh's code after run/attach connect; scp's for cp | added | cli#exit-codes |

## Dashboard (`apps/web/src/routes/**`)

| Item | Status | Where |
|---|---|---|
| Projects table, Connect card, cost card, events, config menu, hold base updates, revisions re-apply, secrets page, notification settings, install snippet | ok | lifecycle, billing, config, secrets, notifications, install |
| Project page "shows the same" as `repose status` (it has no ports) | fixed | lifecycle#see-whats-running |
| Signals card (SSH sessions, tmux clients, agents), last build | added | lifecycle#see-whats-running |
| Start only for a stopped project (error needs `repose start`) | added | lifecycle#stop-and-start |
| Snapshot on stop checkbox | added | lifecycle#stop-and-start |
| Snapshots Create / Restore / Restore as new | added | lifecycle#snapshots |
| Destroy (type the name), Recently destroyed with Restore | added | lifecycle#destroy-and-restore |
| Disk Resize (20 to 320 GB) | fixed | machine#memory-and-disk |
| Config: Extra packages Remove, live build log, Nix tab / Edit as Nix | added | config#the-menu, config#write-it-in-nix |
| Settings: time zone | added | run-and-attach#time-zone |
| Settings: Send test, Save | added | notifications#your-phone-with-ntfy |
| Billing page: hours per day, invoices, card | added | billing#seeing-what-youve-used (no "add a card" step: billing is not enforced) |
| Account page, Delete account (type handle, stops all, deleted in 30 days) | added | billing#deleting-your-account |
| Past-due / suspended banners | not documented | only reachable with BILLING_ENFORCE on, which production doesn't set |

## Guest and platform behaviour

| Item | Status | Where |
|---|---|---|
| Tools carry, `repose scan`, tools-install.log, command-not-found, `nix profile add`, nix-ld compat (Prisma, Playwright, wheels, openssl-sys), desktop/VNC, port auto-forward, secrets delivery, OOM priority, Chromium caps, time zone, git/Claude/.env carry, hybrid first sync, port 25 block, 200 Mbit egress, direnv/flake | ok | machine, sync, secrets, agents, limits, run-and-attach |
| Tools come from nixpkgs first, so versions can differ | added | machine#your-laptops-tools-come-along |
| Node pin also from `.tool-versions`, `volta.node` | added | machine#your-laptops-tools-come-along |
| Extra PATH dirs (yarn, dotnet, ghcup, cabal, opam, luarocks, mix, nimble, juliaup, krew, volta) | added | machine#installing-more; gem/composer added in troubleshooting#on-the-machine |
| Installed but unlisted: tmux, zoxide, starship, `ls` is GNU ls and `ll`/`la`/`lt` run eza (I-514), killall, file, zstd, pg_restore; `dev` in docker group | added | machine#whats-installed |
| Ports never forwarded (5353, 5355, 5900, 6080, 6081, 9224 to 9226); fallback within 20 ports | added | machine#ports |
| npm cache: yarn v1 only; `~/.npmrc` lines, opt-out, own-registry left alone | fixed | machine#network |
| Snapshot retention "seven newest" (code: 7 days, newest always kept) | fixed | lifecycle#snapshots |
| Mining pool ports named; flow limit 200/s burst 2000 | added | limits#network |
| Miner stop sends a notification; dashboard shows the reason | added | limits#what-isnt-allowed |
| Project limit rises to 10 after the first paid invoice (I-184) | fixed | limits#projects (said "not self-serve") |
| `.env` files are on disk and in snapshots, unlike secrets | added | sync#what-travels |
| `repose run: last sync` stash (I-210), `repose run` stash name | added | sync#when-the-machine-has-changes-of-its-own |
| Hybrid sync threshold (~20 MB) and fallback | added | sync#repositories-the-sync-cant-handle |
| Git config: proxies, sshCommand, hooksPath, diff/merge tools, missing pager/editor dropped | added | secrets#git-and-claude-code-settings |
| Secret names start with a letter, up to 64 characters | fixed | secrets#store-an-api-key |
| Pricing examples (free first day; $58.80 is about $59) | fixed | billing#examples |

## Notifications (`internal/api/events` notifyKinds)

| Kind | Status | Where |
|---|---|---|
| completed, needs_input, error, snapshot_failed, base_update_failed | ok | notifications#what-youll-get |
| base_updated, destroy_failed (I-165), host_moved, abuse_stopped (I-239), notifications_paused | added | notifications#what-youll-get |
| gemini/pi body `<agent> went idle` | added | notifications#what-youll-get |
| billing_stopped | not documented | fires only with billing enforced |

## Decisions I-149..I-241

Internal, nothing to document: I-158, I-160..I-164, I-170, I-171, I-173,
I-174, I-176, I-177, I-179, I-180, I-181, I-183, I-185..I-187, I-193,
I-204, I-206, I-208, I-209, I-212, I-214, I-216, I-224..I-226,
I-230..I-237. Documented already: I-149..I-157, I-159, I-166, I-167,
I-172, I-188, I-189, I-192, I-194..I-203, I-207, I-211, I-213, I-215,
I-217..I-223, I-227, I-228, I-238, I-240, I-241. Added or fixed above:
I-155 (one-word prompt that is a project name refused, run-and-attach),
I-165, I-168, I-184, I-190 (restore waits for a running destroy,
lifecycle), I-197, I-205 (free first day in the examples), I-210, I-239.
