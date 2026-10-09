# Feature research backlog (proposal, 2026-09-26)

**Status: open.** The research, with its fact-check, is in
`research_notes/Repose feature research/feature_research.md`; read the
Errata box at its top first. On 2026-09-26 the owner had the items in the first rows built
(table below) and asked for the rest to be left documented here. Nothing
below is built. If this file and a decision entry disagree, the entry
wins.

| Item | Outcome |
|---|---|
| Agents start in the repo's dev shell | built this round (I-259) |
| Change size class from the CLI | built this round; hostd now applies the class at every start (I-260) |
| `repose open` on `::1`, `--desktop` port check | built this round (I-261) |
| Idle-cost warning (no idle stop) | built this round (I-262) |
| Submodules carried by the sync | built this round (I-263) |
| tmux extended keys (Shift+Enter) | built this round (I-264) |
| Ruby/Java version pins, Rails native libs | built this round (I-265) |
| mosh | not offered: needs an edge UDP relay and outlives certificate revocation (I-266) |
| Certificates last 24 hours | built this round (I-267) |
| 1. Limit what a compromised guest can do | open, below |
| 2. Automatic rewind point per `run "prompt"` | open, below |
| 3. `repose pull` / `repose diff` | open, below |
| 4. Smaller items | open, below |

## 1. Limit what a compromised guest can do

**Problem.** `apps/web/src/content/docs/index.md:8` says "The worst it can
do is wreck that one machine, and a snapshot puts it back." That is not
true today. `repose run` copies the laptop's gh login (whatever scopes the
laptop's token has; gh's default login has `repo`, `read:org`, `gist`),
and the Codex, opencode and Vercel logins (`internal/cli/creds.go:23-28`,
`131-133`, `290-305`), plus every `.env` file under 1 MB with no check of
its values (I-197, `internal/cli/carry_env.go:44-60`). Egress is open
(R2-10). A prompt injection or a malicious postinstall script on the
machine can push to or delete any repo the user can write to, deploy with
the Vercel login, or use a production key in `.env`. A snapshot undoes
none of that. `docs/SECURITY.md:249-251` calls this "the same exposure as
on the tenant's laptop", which no longer holds once the pitch is agents
with full permissions left unattended.

**Smallest version (S, 1-2 days).**
- A switch that skips the login copies without skipping the git sync
  (today only `--no-sync` skips them, and it skips the sync too). Per
  tool: `carry_gh`, `carry_vercel`, and so on in config, or one
  `--no-logins` flag. The Vercel carry has no decision entry of its own
  (`creds.go:26` cites I-205, which is the trial-credit decision); give it
  one, and consider making it opt-in.
- At `run`, check carried `.env` values against production-looking shapes
  (`sk_live_`, `rk_live_`, `AKIA`, `SUPABASE_SERVICE_ROLE`, a
  `DATABASE_URL` whose host isn't localhost or a compose service). Reuse
  `secretPattern` (`internal/cli/carry.go:257`). Print the file and key,
  never the value. Warn; don't block.
- Apply the same shape check to the login files: a classic `ghp_` token
  inside `hosts.yml` is copied today although the git-config carry drops
  that shape (I-211).
- Document the narrower GitHub setup: create a fine-grained token for one
  repo, `repose secrets set GH_TOKEN`. Named secrets reach gh and git in
  shells and agents (`nix/guest/base/env.nix:47-60`,
  `nix/overlay/agents/wrap.nix:17-22`), and gh prefers `GH_TOKEN` over
  `hosts.yml`. It hides the full token without removing it, so pair it
  with the login-skip switch.
- Fix the copy in `index.md:8`, `apps/web/src/routes/+page.svelte:125-126`
  and `SECURITY.md:249-251` to say what a snapshot does and doesn't undo.

**Later (needs decisions).** Keeping the gh token out of the guest
entirely (exe.dev does this with a GitHub App and an HTTP proxy). I-204
rejects GitHub App tokens (they act as `repose[bot]`) but has a revisit
condition; an edge proxy that injects tokens would be a fourth home for
secrets and puts tenant git traffic through the edge. Decide the git proxy
and a general secret proxy together.

**Signal.** How often the `.env` warning fires on real runs (a count
only), and whether users then use the login-skip switch or `GH_TOKEN`.

## 2. Automatic rewind point per `run "prompt"`

**Problem.** "An agent can wreck it and it comes back" needs a fast,
automatic undo. Today snapshots are nightly (03:00), on stop and manual;
restore needs a stop and uploads/downloads through Blob storage (the one
example in the docs took 31 s). Fly Sprites checkpoints automatically in
about 300 ms and restores in under a second. Demand: openai/codex#9203
("Please make /undo back", 513 reactions); anthropics/claude-code#87575
(`/rewind` misses Bash edits); HN 45505879 ("Claude has dropped my dev
database about three times").

**Smallest version (M, 4-6 days after decisions).** At each
`repose run "prompt"`, take a local-only thin snapshot of the guest volume
(freeze under 1 s, `lvcreate -s`, no upload), keep the last three as
`rew-*` outside the 7-day retention, and add `repose rewind` (stop, merge
the snapshot, start, restart the agent with its conversation): about
10-15 s. It restores the database, Docker volumes and untracked files
along with the code, which file checkpoints can't.

**Hard parts.** A thin-pool guard that drops the oldest rewind points
first (a full pool breaks every tenant on the host); reconcile must adopt
`rew-*` and keep them out of the I-68 sweep; idempotent by command_id;
rewind points are lost with the host (R2-3).

**Decisions needed.** Amend R3-6 (snapshots streamed to Blob) and I-68 for
local-only volumes; bump `grpc-hostd.md` with the old shape accepted.

**Signal.** Rewinds per active project per week.

## 3. `repose pull` / `repose diff`

**Problem.** Work comes back through `git push` or `repose cp` (I-201).
There is no command that brings the machine's branches and uncommitted
work back into the laptop checkout for review.

**Smallest version (S-M).** `git fetch` over the existing
`Host <slug>.repose` block into `refs/remotes/repose/*`, plus the
machine's uncommitted work as a `git stash create` ref; `repose diff`
shows it; never auto-merge; `status` shows drift ("laptop is N commits
ahead"). Its value is pairing with item 1: a machine with no write
token whose work you review and push from the laptop. Needs an entry
amending the "no reverse sync" line of `features/sync-at-launch.md`; it
keeps git as the exchange channel (R1-3).

## 4. Smaller items

- **SSH `ProxyCommand` that renews the certificate on connect** and
  starts a stopped machine. Only matters to people who connect with an
  editor and never run a `repose` command; the 24-hour certificate
  (I-267) covers most of it. Auto-start is visible on the bill and needs
  its own entry.
- **Idle stop.** I-262 only warns. An opt-in `idle_stop` that never fires
  while an agent process runs would revisit R1-5; decide once the
  recorded idle signals show what unattended agents look like.
- **Fork pricing.** A fork costs what any project costs, and a new
  account can fork at most twice (3-project limit, `limits.md:10`), so
  parallel agents in separate machines are expensive. `--worktree` covers
  the cheap case.
- **Region.** One region. Users far from it type with 200 ms of latency.
- **Documented this round, not built:** LFS files arrive as pointers
  (`/docs/sync`, with the `repose config add git-lfs` + `git lfs pull`
  recipe); user-scope MCP servers live in `~/.claude.json`, which isn't
  copied (`/docs/agents`). Company registry tokens in `~/.npmrc` aren't
  copied (use a named secret); not yet on /docs.
- **Dropped after fact-checking:** a browser shim for CLI logins
  (loopback callbacks are already forwarded while attached; device-code
  and paste-code logins work); a NixOS hint for agents (the I-243 machine
  guide already says it); `/login` on every fork (a fork copies the disk,
  logins included).
