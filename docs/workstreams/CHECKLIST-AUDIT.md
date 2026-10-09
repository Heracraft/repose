# Checklist audit, 2026-09-23

Every §9 checklist row in `docs/workstreams/NN-*.md` that was not ticked at
d3b72d3 was checked against the evidence already recorded: STATUS lines
(most now in [status-archive/2026-09.md](status-archive/2026-09.md)),
`docs/RESEARCH.md`, DECISIONS entries, `docs/security/` reviews, feature docs,
`ops/checks/`, test names, CI runs and commit messages. Each row now ends in
one of:

- `— closed: <citation>` and ticked `[x]`, when the evidence closes the row as
  the row itself specifies;
- `— open: <what is missing>`, when the evidence was never recorded;
- `— waits on: owner (<what>)` or `— waits on: second host`;
- `— superseded: DECISIONS I-NN (<why>)`, when a decision made the row not
  apply (all eight are 00-benchmark, deferred by I-12).

`[~]` rows (local half closed, real-path half not) were checked the same way;
the ones that stay `[~]` also carry an open or waits note.

No row was ticked on "tests pass" or "CI green" alone. A test was accepted
where the row asks for a test; where a row asks for pasted output, the output
had to exist somewhere cited. Six 03-hostd rows (freeze window, snapshot
under 5 s, two hostd refused, reconcile `--from-api`, `/metrics`,
registration) are ticked on a STATUS or RESEARCH record of the result on
host-01 rather than the literal paste the row names; a reviewer who wants
strict pastes should reopen those six.

Refreshed on 2026-09-26: the 04 Ready, 07 help-tree and 12 bump-agents
items below, and the owner items moved from the archived handoff. The
counts table is still as of 2026-09-23.

## Counts per workstream

"Before" is `[x]` / `[~]` / `[ ]` at d3b72d3. "Closed" is rows ticked by this
audit. The last four columns count the rows still not `[x]`.

| WS | Before x/~/[ ] | Closed now | After [x] | After [~] | Open (evidence missing) | Waits owner | Waits second host | Superseded |
|---|---|---|---|---|---|---|---|---|
| 00 benchmark | 0/0/8 | 0 | 0 | 0 | 0 | 0 | 0 | 8 |
| 01 host-nixos | 0/0/16 | 12 | 12 | 0 | 3 | 1 | 0 | 0 |
| 02 guest-base | 0/0/21 | 4 | 4 | 0 | 17 | 0 | 0 | 0 |
| 03 hostd | 0/0/27 | 18 | 18 | 0 | 9 | 0 | 0 | 0 |
| 04 guestd | 0/0/16 | 5 | 5 | 0 | 11 | 0 | 0 | 0 |
| 05 control-plane-api | 20/7/3 | 4 | 24 | 4 | 3 | 3 | 0 | 0 |
| 06 gateway-edge | 0/0/21 | 13 | 13 | 0 | 5 | 2 | 1 | 0 |
| 07 cli | 0/0/27 | 16 | 16 | 0 | 8 | 3 | 0 | 0 |
| 08 dashboard | 17/0/3 | 0 | 17 | 0 | 1 | 2 | 0 | 0 |
| 09 billing | 15/0/6 | 0 | 15 | 0 | 0 | 6 | 0 | 0 |
| 10 observability | 8/0/2 | 0 | 8 | 0 | 1 | 1 | 0 | 0 |
| 11 infra-opentofu | 8/0/5 | 1 | 9 | 0 | 3 | 1 | 0 | 0 |
| 12 nix-config-pipeline | 9/6/2 | 6 | 15 | 0 | 1 | 1 | 0 | 0 |
| 13 notifications | 15/2/0 | 0 | 15 | 2 | 2 | 0 | 0 | 0 |
| 14 security | 3/6/0 | 2 | 5 | 4 | 2 | 1 | 1 | 0 |
| 15 dev-ergonomics | 3/0/10 | 0 | 3 | 0 | 9 | 1 | 0 | 0 |
| **Total** | **98/21/167** | **81** | **179** | **10** | **75** | **22** | **2** | **8** |

## Genuinely missing, and how to close each

Most of these are one command on host-01 or in a guest whose output nobody
pasted; a few need a CI job. The row in the workstream doc carries the detail.

**CI jobs (one KVM-capable runner closes several):**
- 01: build `nixosConfigurations.host-bench` toplevel in CI, or paste a build log.
- 01: run the `nix/hosts/tests` VM tests in CI, or record a DECISIONS entry that they run locally.
- 02: build the guest-base, guest-docker, guest-desktop and guestd VM checks in CI.
- 04: install strace on the runner and run `go test -run TestStrace -v` so the log shows the test name.
- 07: a CI step running `repose status --json | jq .`.
- 12: `bump-agents.yml` failed daily 2026-09-21..23 (`lock file contains unlocked input ./guest/fragment-placeholder`); fixed by 01b6243 (install-nix-action v31). Scheduled runs pass since 2026-09-24 (runs 35984425847, 36122532223); PRs #1 and #3 merged, #4 open. The row's evidence now exists; tick it in 12 §9 with those links and the built agents' versions.

**Grep lists and tables nobody wrote:**
- 01: grep `interfaces/host-conventions.md` paths and unit names against `nix/hosts`, paste the list.
- 02: the same for `interfaces/guest-conventions.md` against `nix/guest/base`.
- 04: a request-to-handler table from `internal/guestd/dispatch.go` against `interfaces/vsock-guestd.md`.
- 07: `repose --help` tree pasted and diffed against §5.1. The CLI has grown since the row was written: restore, projects --destroyed, run --worktree are in §5.1; cp, fork, paste, scan, resize, questions and reply are not (they are in `apps/web/src/content/docs/cli.md`, which `internal/cli/docs_test.go` checks against the CLI).
- 07: a reviewer grep of log calls plus a test capturing `-v` output for secrets, tokens, certs, prompts.

**Output from a real guest (one session on host-01 closes most of 02 and 04):**
- 02: `nix path-info -S` for guest-system (under 6 GB).
- 02: `id dev`, `sudo -n true`, `passwd -S root`.
- 02: ssh with the right and the wrong principal, both outputs.
- 02: the `mount` ro-store/rw-store line and the `nix profile install` store path.
- 02: `docker run hello-world`, `docker info` overlay2, `docker network inspect` showing 172.20.0.0/14.
- 02: `tmux ls` as dev.
- 02: the five agents' `--version`.
- 02: `~/.claude/settings.json` before and after the first `claude` run (hook merge).
- 02: the hook-socket `curl` answering 200 plus the hostd AgentEvent line (hook path from a real guest).
- 02: chromium headless screenshot: file size and a look at the PNG.
- 02: Playwright and chrome-devtools MCP `--version` offline.
- 02: desktop chain start on 6080 and idle stop.
- 02: `sysctl fs.inotify.max_user_watches`.
- 02: `echo $TZ $LANG $REPOSE $REPOSE_PROJECT` in a login shell.
- 02: `/etc/repose/base-version` next to the api's `base_version` (not pasted since I-118 fixed `dirty`).
- 04: guestd VM-test subtests (freeze/thaw, Switch, GrowFs, WriteSecrets, SetPrincipals, Sample): paste the lines from `nix build ./nix#checks.x86_64-linux.guestd -L`; six rows.
- 04: `listening on vsock port 5000` and hostd's `Ready`. Ready was 11.7 s on host-01 (RESEARCH §11). The boot work since (I-161, I-231..I-234) cut a stopped guest's start: live on host-01 on 2026-09-24, ensure-running median 5.37 s and `repose run` to the tmux screen median 8.9 s (conductor line 2026-09-24 02:10Z in the archive). Nobody has pasted a Ready time itself, and nothing shows it under 5 s, so the row is still open as written.
- 04: an `"event":"sample"` line with `duration_ms` under 20.
- 04: guestd memory size: `ls -l` of the binary and `ps -o rss=` idle.
- 13: `repose status` showing `last event` and agent state after a hook fires.
- 13: native Gemini CLI and pi hook payloads captured, with fixtures and mapping tests.

**Host and edge runs:**
- 03: reused join token: `systemctl status hostd` showing status=3 and no restart.
- 03: `kill -9` hostd mid-Create and mid-Build, replay shown.
- 03: a real guest's `tc class show` (200 Mbit/s) and nft counter.
- 03: `--fail-at-step N` on host-01, then `lvs`, `ip link`, `nft list`, `systemctl`.
- 03: `diff -r` of a restored filesystem against the original.
- 03: BuildLog latency: timestamp of a Nix line against the SSE event.
- 03: api-side sample log for a busy guest (non-zero deltas) and with guestd stopped (`guestd_ok=false`).
- 03: nightly snapshot timer fired with the api down and a guest present, blob shown.
- 03: console rotation at 64 MB: `yes` in a guest past 64 MB, the new file's lines seen in Loki.
- 05: `az keyvault key rotate` then `repose-admin secrets rewrap` against production, key versions and a secret read back.
- 05 and 07: a destroy/restore CLI transcript: `[y/N]`, printed restore command, `projects` reading `destroying`, the snapshot's `stop` reason and 30-day expiry, `repose restore NAME` (timings exist in the 2026-09-23 03:40Z conductor line).
- 05: a production user row from a real GitHub first sign-in (trial 336 cents per I-205, not 1000); comes with the M5 second human.
- 06: OpenSSH relay of pty, `-L`, `-R`, `-A` in CI or on the real edge (`-R` never run with OpenSSH).
- 06: `git push` over the forwarded agent from an attached guest (only `git fetch` was run).
- 06: production `GET /projects/:id` showing the session while attached, with the api log line.
- 06: 100-connection soak with heap at start and end (goroutines already logged).
- 06: hook ingest on 8443: `curl` from a real guest and from host-01.
- 07: `ps -o pid,ppid,comm` during `repose attach` (no `repose` parent).
- 07: `repose logs` against the real api (the other read commands are recorded).
- 07: every §6 failure row triggered, message and exit code pasted.
- 07: sync: tests that a gitignored file stays on the laptop and that a binary diff applies.
- 08: Playwright tests for access-token refresh failure and a missing `/me/notify-test`.
- 10: a Loki query for one guest's console stream showing the documented labels.
- 11: the IMDS curl from host-01 (guest half closed by `TestGuestCannotReachIMDS`).
- 11: a plan against prod state changing `host_data_disk_gb`, failing on `prevent_destroy`.
- 11: `dig +short` for the edge and control-plane names.
- 14: an `audit_log` row for the user-route restore (L-13, no audit call today) and a production `operator_login` row.
- 14: run the "Suspected cross-tenant access" capture commands once on host-01.
- 15: live transcripts for TZ, git carry, Claude merge, `.env`, forward, OOM on the I-213 base, `cp`; bundle-vs-clone timings to set I-203's threshold; two-guest cache timings. The conductor's 10:00Z line says these ran live but pasted nothing; nine rows.

## Waits on the owner

- Polar sandbox token in the api's env: 08 billing screenshot, 09 checkout to trial, payment events, the 3-day stop, the overage line to the cent (M4-GATE.md). Live key and card: 09 live charge.
- GitHub sign-in in a browser: 08 `live:auth`.
- Staging, or permission to pause production Postgres: 05 `/healthz`.
- Traefik drain on the Coolify proxy: 05 rolling deploy (14 failures in 29,841 responses).
- Cachix cache and token: 12 overlay cache.
- Loki and monitoring peer view in Grafana: 01 host label row. api `/metrics` Traefik labels in Coolify (I-133): 10 Prometheus `up` series.
- Cloudflare token for the wildcard certificate: 06 preview stub.
- A laptop: 06 VS Code Remote-SSH screenshot; 07 macOS keychain, zero-prompt run with a passphrase key, `open PORT`/`--desktop`; 15 macOS timing table.
- Someone who did not write it follows `infra/README.md`: 11.
- Bootstrap-key retirement (I-177): 14 operator access.

Moved here on 2026-09-26 from the archived conductor handoff
([archive/HANDOFF-2026-09.md](archive/HANDOFF-2026-09.md)); no workstream row
tracks them:

- Front-end analytics with self-hosted Umami (owner's choice, 2026-09-23;
  Google Analytics rejected: a third party the privacy policy would have to
  name, cookies and a consent banner, dropped by most ad blockers). Owner:
  deploy Umami from Coolify's template on the homeserver (for example
  `stats.herakraft.co`) and hand over its URL and website id. Then, in
  `apps/web`: load the script first-party, proxied through the dashboard's
  own domain; only on public pages (landing, install, pricing, docs), never
  the signed-in dashboard, whose paths carry project names; two events,
  `install_copied` and `signup`; one sentence in
  `apps/web/src/content/legal/privacy.md` "Where your data lives" (page
  visits counted with Umami, self-hosted, no cookies, no IP stored); a
  DECISIONS entry recording this and why the dashboard is excluded. Nothing
  of it is built.
- Coolify watch paths on the api, api-grpc and web apps, so docs-only pushes
  stop redeploying them (optional; they roll cleanly).
- The owner has two account rows (`heracraft` by GitHub and an email row)
  with projects under both. There is no transfer command: log in as the
  email account for those projects, or destroy and recreate them under
  `heracraft`. A `repose-admin projects transfer` is worth adding before
  there are more users.

## Waits on a second host

- 06: wgsync removes a retired peer.
- 14: hostd for host X cannot act on host Y.
