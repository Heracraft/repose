Archived 2026-09-26; superseded by [../STATUS.md](../STATUS.md) and [../../ops/ORCHESTRATION.md](../../ops/ORCHESTRATION.md); open items moved to [../CHECKLIST-AUDIT.md](../CHECKLIST-AUDIT.md) ("Waits on the owner").

# Update 2026-09-23 (conductor, Opus 5.5)

Read this section first; the 2026-09-21 text below is history where it
disagrees.

- **Owner's laptop session (2026-09-23 00:01Z) fixed.** CLI v0.1.5 is
  released (tag on 69b3229): own passphrase-less key and one multiplexed
  connection per command (I-149), the laptop sends its commits as a git
  bundle so the guest needs no GitHub access (I-150), Include verified with
  `ssh -G` (I-151), dir cache only trusted when remotes match (I-152),
  honest states and a `[y/N]` destroy that waits for the op (I-153), live
  phases with elapsed time (I-154), `repose attach izma` positional (I-155),
  "Your login has expired" instead of "Not logged in".
- **Server (api, live):** destroy always finishes on a dead guestd, proven
  live by destroying age-calculator at the owner's request (01:48Z);
  `repose start` on a project in error restarts it; human op errors
  (I-156..I-159). Build reuse for identical closures and NOTIFY-driven ops
  (I-160, I-163) are live: a second create on the same base took 11 s end
  to end from repose-admin (was 22 s host side alone).
- **Base 2026.09.23** (abdc969) published with the boot trims (I-161): a
  small guest boots in 10 s (was 16 s for izma's large). Unheld projects
  rebuild at the 04:00Z sweep.
- **Edge and host-01 switched to main** at ~02:05Z (edge carries I-123,
  I-133; host-01 carries I-148's retry and I-162's lazy mkfs). All guests
  survived the hostd restart.
- **Live projects:** `izma` (owner, email row) running; `m3-iso-c`
  (second tenant for the isolation runner). m3-check, m3-held and
  age-calculator are destroyed; nuru-playground was destroyed by the owner.
- **Still owed:** a real-laptop run of v0.1.5 (the dev box's Logto refresh
  token is revoked, so the conductor could not drive the live gateway):
  `repose login`, `repose run` in a private-repo checkout with a
  passphrase-protected `~/.ssh/id_ed25519` should prompt zero times;
  `repose open` over its own connection and Windows are untested. The
  owner's list below is unchanged apart from items that the switches closed.
  `api.md`'s Project shape lacks `last_error`, `host_unreachable` and
  `signals.guestd_ok`, which the api already returns (05 owns it).

- **Owner's monitoring server joined (I-170):** peer 10.255.0.3
  (`wg-repose`) on the edge; Loki `http://10.255.0.3:3100` recorded; the
  edge and host-01 ship logs (host-01 via its `host.json`, backup at
  `host.json.bak-loki`). Owner items 3 and 4 are done. Still owed: the
  api `/metrics` router labels (item 5). Since then: the owner's
  Prometheus scrapes hosts, hostd, fluent-bit, gateway and api-grpc (all
  `up`), log streams carry `service_name`, and the eight dashboards
  (including the new `repose / Overview`) are in the owner's Grafana,
  folder "Repose", via `ops/dashboards/push.py` (Postgres panels read no
  data there: no datasource).
- **Todo: front-end analytics with self-hosted Umami** (owner's choice,
  2026-09-23; Google Analytics rejected: a third party the privacy policy
  would have to name, cookies and a consent banner, blocked by most
  developers' ad blockers). Owner: deploy Umami from Coolify's template
  on the homeserver (e.g. `stats.herakraft.co`) and hand over its URL
  and website id. Then, in apps/web: (1) load the script first-party,
  proxied through the dashboard's own domain so blockers do not drop it;
  (2) on public pages only (landing, install, pricing, docs), never the
  signed-in dashboard, whose paths carry project names; (3) two events,
  `install_copied` and `signup`; (4) one sentence in
  `apps/web/src/content/legal/privacy.md` "Where your data lives": page
  visits are counted with Umami, self-hosted, no cookies, no IP stored;
  (5) a DECISIONS entry recording the above and why the dashboard is
  excluded.
- **Destroy/restore (I-164..I-168), CLI v0.1.6:** background destroy,
  `repose restore NAME`, `repose projects --destroyed`, dashboard "Recently
  destroyed"; snapshot of a clean volume reads only used blocks (40 GB:
  33 s → 1.3 s).

# Conductor handoff, 2026-09-21 (after M2 and most of M3, M5 step 1)

Read this before `STATUS.md` when picking the project up. It says where
every machine, session and milestone stood when the conductor stopped at
about 05:00 UTC on 2026-09-21, what the owner still owes, and what the
next session must do first. Facts here are as of that time; the live
state is `repose-admin projects list`, `hosts list` and `base list`.

## Where the milestones stand

- **M1, M2: closed.** M2 closed on the owner's call with one person plus a
  second account (DECISIONS I-129); a second human on their own laptop is
  still required at M5 step 3.
- **M3: api side done and proven on host-01, dashboard side owner-gated.** Every 05/12/13/14 row
  that one host can prove is closed with evidence (`12 §9`, `05 §9`, the
  security review). Open: the four owner values below and the last
  kernel-row proof (I-147/I-148, see "Live state").
- **M4: not started.** Needs the Stripe test-mode objects in the api's
  Coolify environment (`PROMPTS.md` M4 step 1), then `/ws m4` (Opus 5)
  in `../repose-ws/m4-billing`.
- **M5: steps 1 and 4 done as far as one host allows** (deployed-state
  review `docs/security/review-2026-09-21.md`, "At a glance" section).
  Steps 2 (second host, host loss) and 3 (second human) need the owner.

## Live environment

| Thing | State |
|---|---|
| main | see `git log`; every push tonight passed CI (a `-race` flake in hostmgr fixed at a5ca589) |
| Coolify apps (api, api-grpc, web) | redeploy on every push to main; api container prefix `8kpqxzfejbbsgwjhooep2ymc`, api-grpc `v2feqwjqar150xgjqed1wpzz`, web `kw2gthh3iy0rwovbfraahlth` on the control VM `20.121.138.150` (`root@`, operator key) |
| edge `20.102.98.254` | main 65d336e (I-110); **not yet switched** for I-123 (session reports) and I-133's `scrapePorts` change; `NIX_SSHOPTS="-p 2222" nixos-rebuild switch --flake ./nix#edge --target-host root@20.102.98.254` |
| host-01 (`10.255.0.2` via `-J root@20.102.98.254:2222`) | switched by the owner at 04:30Z to main 73491f2 (I-137, I-139, I-140, I-142..I-146 hostd halves); **not** I-148's hostd half (retry the Switch once after guestd is regained), which needs the next host switch |
| bases | latest `2026.09.21.5` (e1d7984, security; carries I-143, I-147, I-148's guestd half); proven at 05:00Z with a second switch on a guest already on the fixed guestd: op `done`, guestd restarted itself from the transient unit, no guestd_lost |
| Fluent Bit on host-01 | inactive by design (I-95, no Loki URL); alert `FluentBitLogShipperDown` and its runbook entry explain it |
| public `/metrics` | 404 since 02:30Z (I-136); the Traefik router labels for the scraper are the owner's paste (`ops/coolify/README.md`, I-133) |
| Blob role for the api | applied by the owner 01:52Z (I-131); expiry proven deleting real blobs |

## Live projects on host-01 and what each needs

- `nuru-playground`, `age-calculator` (email row `user-c7fh26yzrl93`):
  the owner's. nuru-playground is on 2026.09.21.4 and fine. age-calculator
  is running with **guestd dead** since the 02:59Z sweep (RUNBOOK "guestd_lost
  after a base switch"); its newest built revision is 2026.09.21.4. Recovery
  is either the owner's `ssh age-calculator.repose 'sudo systemctl start
  guestd'` **with the email account's certificate** (their laptop is now
  logged in as `heracraft`, whose certificate the gateway rightly refuses
  for this project), or `repose-admin projects restart` (I-147 is
  deployed, so the start applies its newest built revision, 2026.09.21.5,
  cleanly; it is a reboot and agents inside end). Do nothing to it
  without the owner.
- `m3-check`, `m3-held` (row `heracraft`) and `m3-iso-c` (row
  `repose-m3-b`): m3's test projects, left alive for the next session's
  checks (m3-check on the 7.2.6 test kernel with .5 built and a reboot
  pending; m3-held and m3-iso-c on .5). Destroy them when no longer
  useful; m3-iso-c is the second tenant the isolation runner needs.

## What the owner still owes (each unblocks named rows)

1. Stripe test-mode key, webhook secret, three prices and meter ids in the
   api's Coolify env: M4 entirely, plus two dashboard panels.
2. Dashboard session capture (`pnpm --filter web run live:auth` from
   `apps/web`): 08's seven live Logto tests.
3. Monitoring server's WireGuard public key and address: I-94's edge rules,
   10's scrape row (then an edge rebuild).
4. Loki push URL (`repose-admin edge loki <url>`): Fluent Bit on every
   host, 10's shipping row, the log-shipper alert's quiet state.
5. Paste the api's `/metrics` router labels in Coolify (I-133).
6. ~~Decide b1a5915~~ settled: the key was rotated, history stays (I-193). Was: rewrite and force
   push, or make the repo private before sharing. Recorded as a DECISIONS
   entry once chosen.
7. Coolify watch paths on the three apps, so docs-only pushes stop rolling
   them (optional; they roll cleanly).
8. Account: the owner now has two rows (`heracraft` by GitHub, the email
   row) and projects under both. There is no transfer command; the choice
   is to log in as the email account for those two projects or destroy and
   recreate them under `heracraft`. A `repose-admin projects transfer` is a
   small feature worth adding before there are more users.

## The local observability stack (ephemeral, and how to get it back)

Not on the list above because nothing depends on it, but it existed and
will not when this session's shell ends, so nobody wonders later.

The owner's Prometheus is not a WireGuard peer yet (item 3), so the
m3-web session stood one up here instead: SSH forwards from the dev box
to the real endpoints, and the local dev stack scraping through them.
That is how "38 of 43 panels" and then "41 of 43" were measured, and how
`repose_host_guests` was found empty and then found fixed. It proves the
exporters and the panel queries; it proves **nothing** about the
WireGuard path, because the edge originates a forwarded connection
itself and so never crosses the `forward` chain a real scrape would
(DECISIONS I-94). Do not read a working tunnel as a working peer.

```
ops/dev/tunnel-prod.sh --stop            # if any are lingering
BIND_ADDR=172.17.0.1 ops/dev/tunnel-prod.sh 20.102.98.254 20.121.138.150 &
docker compose -f ops/dev/docker-compose.yml \
               -f ops/dev/docker-compose.prod-scrape.yml up -d
python3 ops/dashboards/validate.py --query http://127.0.0.1:9090 --host-id host-01
```

Grafana answers on this box's Tailscale address, port 3000, folder
"repose" (the dev box is remote; localhost is no use to the owner). The
forwards are children of whatever shell started them and die with it;
the docker stack survives and shows a dead target until they are
restarted. One forward targets the api's *container* address, so it
breaks on every api redeploy and the script re-resolves it on restart.
That stops mattering once item 5's Traefik route exists.

## Findings filed, not fixed

- 07: the `Include ~/.ssh/repose/config` line the CLI writes was not
  effective on the owner's laptop (`ssh age-calculator.repose` did not
  resolve); the CLI should verify the alias resolves after writing it and
  say what to fix.
- 07: no certificate re-issue on a gateway "certificate revoked" (STATUS
  2026-09-21 00:16Z finding).
- 05/12: `base publish --rev` accepts unresolvable or short shas.
- 05: the `gateway_sessions` key is (project, cert_serial); interface
  change later.
- 14: M-1's other half (bootstrap key stays while `bootstrap.enable`),
  api-grpc's 9104 reachable in-VNet over plain HTTP, Traefik 8080 mapped
  but not listening; all in the review's open list with owners.

## How the sessions were run (so the next conductor can repeat it)

- One tmux window per session (`0:m2`, `0:m3`, `0:m3-web`, `0:m5`), each
  `claude --model <id> --permission-mode auto` in its own worktree under
  `../repose-ws/<name>` on branch `ws/<name>`, started with `/ws m<n>`;
  they message the conductor by session name and never push to main.
- The conductor trial-merges each batch in a detached scratch worktree,
  runs the touched packages' tests, fast-forwards main, pushes (Coolify
  redeploys), watches the roll with a `Monitor` and CI with `gh run watch`.
- Decision numbers collide across parallel sessions; the conductor assigns
  the next free number in each message and renumbers on merge.
- `tofu apply` and `nixos-rebuild switch` to a host with tenant guests
  are the owner's (the conductor's permission classifier refuses them);
  queue the exact command in a tmux buffer (`apply`, `switch`) and ask.
- Rules every session followed: never touch the owner's projects, at most
  two test projects alive per row, announce anything host-affecting to the
  conductor first, no Coolify UI, no force-unlock.

## First things for the next session

1. Read the last five lines of `STATUS.md` (m3, m3-web, m5, conductor).
2. If I-147/I-148 are on main but no base carries them, publish
   `2026.09.21.5` from main's sha and run the second-switch proof on a
   test guest (m3's RUNBOOK entry says how).
3. Switch the edge to main (I-123, I-133 scrape ports) and host-01 again
   (I-148's hostd retry) at a quiet moment; both are the owner's to run
   (`switch` buffer shape in "How the sessions were run"); the edge one
   drops live gateway sessions for a second.
4. Then the owner's list above, in the order their values arrive. Items
   3 and 4 are one value each and each closes a whole row; item 4 (the
   Loki URL) also retires the six-hour silent-failure mode recorded in
   `RUNBOOK.md` "FluentBitLogShipperDown".
