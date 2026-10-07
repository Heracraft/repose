# 15 · dev-ergonomics (laptop parity)

## 1. Goal

A guest should feel like the user's own laptop: their git config, their
Claude Code setup, their `.env` files, their clock, and their dev servers
on `localhost`, with no extra step and no visible delay. Decided with the
owner on 2026-09-23 as DECISIONS I-195..I-205. The reasoning, the options
that lost and the owner's words are in
`../proposals/2026-09-23-dev-ergonomics.md`; read it before starting.

The rule over everything here: **carrying and forwarding never make `run`
or `attach` slower in a way the user can see**, and nothing needs a
per-OS special case beyond macOS and Linux laptops (Windows is not
supported).

## 2. Scope: builds

In the order to build them (each part is mergeable on its own):

1. **Timezone on every run and attach** (I-198).
2. **git config carry** (I-195).
3. **Claude Code config carry and `settings.json` merge** (I-196).
4. **`.env` carry** (I-197).
5. **Auto-forward while attached, printed in tmux** (I-199).
6. **`repose cp`** (I-201).
7. **Agent OOM priority and process visibility** (I-200).
8. **Hybrid first sync** (I-203).
9. **npm registry and Docker Hub caches on each host** (I-202).
10. **Trial credit of one day** (I-205).
11. Catalog menu entries for vercel, wrangler, supabase, flyctl, portless
    (proposal item 3), and vercel's login file added to the carried
    tool-login list (reversed by DECISIONS I-298).

## 3. Scope: does not build

The rest of the proposal is **not** in this workstream. Do not start any of
these; they are open or deferred with the owner:

- Claude login across N projects (proposal item 9): it needs its own design
  session. Carry config only; never touch `.credentials.json`.
- Image paste and voice (item 8), the pty proxy it would need, and the
  `BROWSER` shim.
- `config.toml` tables for any of this (parked by the owner). Behaviour is
  fixed defaults with no knobs, except the environment variable in §5.5.
- A GitHub App or any GitHub integration that shows the platform's name
  (I-204).
- Clipboard shims and OSC 133 last-output bindings (proposed, not agreed).
- Preview URLs, a production deploy path, Alloy.
- User-level secrets and `repose secrets import`.
- Any `apps/web` landing-page copy beyond the trial string. Another session
  is redesigning that page; coordinate through the owner.

## 4. Interfaces

Changes, each in the commit that changes the code, with the old shape
accepted for one release:

- `interfaces/guest-conventions.md`: the `.gitconfig` row becomes the
  `repose-carried` include (I-195); new rows for the carried `~/.claude`
  files and `.env` files (I-196, I-197); `TZ` update on attach (I-198);
  the registry and Docker mirror settings (I-202); oom_score_adj ownership
  (I-200).
- `interfaces/cli-config.md`: `repose cp`; the per-project carry hashes in
  `projects.json`; `REPOSE_NO_FORWARD`.
- `interfaces/host-conventions.md`: the two caches (units, ports on the
  WireGuard address, disk).
- `interfaces/api.md` only if `tz` needs to be accepted on start/attach
  routes it isn't on today.

Feature docs to update with the code: `sync-at-launch.md` (I-197 reverses
its `.env` rule; I-203), `secrets.md` (the carried-files list),
`ports-and-previews.md` (I-199 replaces "one port per invocation" as the
main path), `agents.md` (carried config), `run-and-attach.md` (tmux
status bar), `status-and-logs.md` (process list), `pricing.md` and
`../PRICING.md` (I-205).

## 5. Design detail

The proposal doc has the full sketches; the points that are easy to get
wrong are these.

### 5.1 Staying off the critical path

- `run`: carried files ride the existing credentials ssh (I-149); no new
  round trip. The first sync ssh, which already reads the dirty list and
  refs, also returns a hash and mtime per carried file and per guest
  `.env`, so the laptop sends only what differs and can apply the
  mtime rule.
- `attach`: the carry runs concurrently over the mux while tmux attaches.
  Its result and any failure are shown with `tmux display-message`, never
  by delaying the attach.
- Measure before calling it done (checklist).

### 5.2 git (I-195)

Read the config with `git -C <repo> config --global --list --includes -z`
(`-z`, because values can contain newlines). Apply the denylist on the
laptop, then run the path and command checks in the guest (`test -e`,
`command -v`) inside the same ssh, as a shell snippet using only stock
tools. Write the result to `~/.config/git/repose-carried` with `git config
--file` so quoting is right, then atomically rename it into place. The
guest's `~/.gitconfig` gets `[include] path = ~/.config/git/repose-carried`
once, before its own keys, so a key set by hand in the guest wins.

### 5.3 Claude settings merge (I-196)

This runs in the guest with `jq` (already in the base). Pseudocode:

```
strip(x)  = x | del(.hooks[]?[]?.hooks[]? | select(.command|test("repose-hook")))   # and empty groups
merged    = strip(guest) * strip(laptop_rewritten)
merged.hooks[ev] = (guest.hooks[ev] - prev_laptop.hooks[ev]) + laptop.hooks[ev]   # identical groups once (I-499)
merged.permissions.{allow,deny,ask} = (guest + laptop) | unique
merged    = add_platform_hooks(merged)
```

`*` is jq's recursive merge; arrays are replaced by the right side, which is
why permissions and hook events are unioned explicitly. `prev_laptop` is
`~/.repose/claude-laptop-hooks.json`, the hooks the previous laptop file
added, so a hook deleted on the laptop leaves the guest (DECISIONS I-499). Hooks and `statusLine` commands
that don't resolve in the guest are dropped *after* the home-path rewrite.
Write to `settings.json.tmp`, check it with `jq empty`, copy the old file to
`settings.json.repose-prev`, `mv` into place. Running the merge twice with
the same input must give byte-identical output (test it).

Plugins: for each `enabledPlugins` entry from a marketplace, install it in
the guest if missing, in the background after the attach, using Claude
Code's own plugin command. Check the exact CLI syntax against the installed
`claude-code` version, and don't guess.

### 5.4 `.env` (I-197)

Candidates: `git ls-files --others --ignored --exclude-standard` filtered to
basenames matching `.env` or `.env.*`, outside the dependency directories
I-194 skips, at most 1 MB each. `.env.example` and friends are usually
tracked and already travel with the diff. They're written in the guest
with mode 0600. When the guest copy is newer, it's kept and the CLI prints
one line naming the file. The summary line gains `, 2 env files` when
any travelled. File contents never go in a log line (CLAUDE.md "never log
what a tenant typed"): log counts only.

### 5.5 Auto-forward (I-199)

- Detect ports in the CLI itself: `ss -Hltn` over the mux every second
  while a session is attached (stock tool, no guestd change, no API).
  Forward listeners on `127.0.0.1`, `0.0.0.0`, `::1` and `::` with ports
  of 1024 and up, except the guest's own platform ports (6080 unless the
  desktop was asked for, and anything guest-conventions lists as
  platform-owned).
- Add a forward with `ssh -O forward -L <local>:127.0.0.1:<port>` on the
  ControlMaster; cancel it with `-O cancel` when the port closes or the
  session ends. If the local port is taken, bind the next free one. 1355
  (portless) is never remapped silently; the message names the port used.
- Print only inside tmux: `display-message -d 4000 "⇄ localhost:5173 →
  :5173"` per new forward, and the current list in the project session's
  `status-right`, restored when the last attached CLI goes away.
- `REPOSE_NO_FORWARD=1` turns it off (for users whose laptop ports must
  stay free). This is the only knob.
- `repose open` is unchanged.

### 5.6 OOM priority (I-200)

`oom_score_adj` is **inherited on fork**, so setting it once on an agent
also protects every dev server the agent starts. The unprivileged `dev`
user can raise it but never lower it. So guestd (root) owns it and
re-applies it on every process sample: `-800` for the tmux server and for
processes whose name matches an agent binary, `0` for everything else
under `dev` that isn't one of those. A dev server an agent started is
reset to `0` within one sample interval. Name matching uses process names
only; arguments are never read, per the never-log list.

### 5.7 Hybrid first sync (I-203)

Only when the guest has no commits. Size comes from `git count-objects -v`
(`size-pack`, in KiB) on the laptop. The guest attempts `git clone` of the
https GitHub URL: no credentials for a public repo, the gh credential
helper otherwise. If the clone fails for any reason, fall back to today's
full bundle with one line of explanation; never fail the run for it.
Then the usual bundle of what the guest lacks, diff and untracked files.
Set the 20 MB threshold from the measurement in the checklist.

### 5.8 Caches (I-202)

A pull-through npm registry cache and a Docker Hub registry mirror as
NixOS services on each host, listening only on the host's WireGuard or
guest-bridge address, with a size cap and LRU eviction on the host disk.
The guest base points `npm_config_registry` (pnpm, npm and yarn all read
it) and the Docker daemon's `registry-mirrors` at them. If the cache is
down, the fallback must be automatic (Docker mirrors fall back natively;
for npm, check the tool actually falls back, and if it doesn't, cache at
the HTTP layer with the upstream as a failover). Scoped or authenticated
registries in a user's `.npmrc` go direct.

### 5.9 Trial (I-205)

`billing.TrialCreditCents` becomes 336. Every user-visible string (CLI,
dashboard, emails, `features/pricing.md`, `PRICING.md`) says "your first
day of compute" and never the amount. The fake api's `trial_credit_cents`
values follow.

## 6. Failure modes

| Failure | Required behaviour |
|---|---|
| Laptop `settings.json` is invalid JSON | skip Claude settings, one warning, guest file untouched |
| `jq` merge fails in the guest | previous file stays; warning in tmux |
| Carry ssh fails on `attach` | attach still works; one tmux message |
| A carried hook script is missing in the guest | hook dropped, named once per change, not every run |
| Laptop port taken | next free port, message says so |
| Guest listener disappears | forward cancelled within 2 s |
| Two CLIs attached to one project | each forwards on its own laptop; the status bar shows the union |
| Clone from GitHub fails | full bundle, one line saying why |
| npm cache down | installs still work (fallback), logged on the host |
| `.env` newer in the guest | guest copy kept, one line |

## 7. Testing

- The existing local-sshd harness (I-149's) runs carry, `.env`, `cp` and
  forward end to end against a scratch `$HOME`.
- The jq merge gets golden tests: permissions union, idempotence,
  repose-hook strip and re-add, path rewrite, invalid input.
- A guest NixOS test covers the git include precedence (a key set by hand
  in the guest beats a carried one), the oom_score_adj values under a
  forked child, and the registry settings.
- A test feeds a laptop `$HOME` holding `.credentials.json`,
  `~/.claude/projects/`, SSH keys and Gemini creds, and asserts none of
  them is in the carry stream (extends the existing secrets test).

## 8. Rollback

Each part is independent. Turning off carry or forward in the CLI is a
release; the guest-side pieces are a base bump; the caches are removed by
a host switch, and guests fall back to upstream.

## 9. Checklist

Each item closes with the evidence it names, pasted in the commit or the
final report. `just done-check` and `just lint` pass.

- [ ] **Timing:** `repose run` (warm, no changes) and `repose attach` p50
      over 10 runs each, from a macOS laptop, with and without carry and
      forward. Added time under 100 ms at p50, pasted as a table. — waits on:
      owner (a macOS laptop): STATUS 2026-09-23 conductor 10:00Z records
      +19 ms run and +12 ms attach at p50 (client not named, not macOS),
      but no table is pasted.
- [ ] **TZ:** `date` in a guest after `TZ=Asia/Tokyo repose attach` shows
      JST in a new tmux window; transcript pasted. — open: STATUS 2026-09-23
      conductor 10:00Z says the live check ran; the transcript is not pasted
      (commit 75e740b has the guest-parity VM test reporting JST only).
- [ ] **git:** a laptop config with `includeIf`, `credential.helper
      osxkeychain`, `url.*.insteadOf`, `gpg.format ssh`, `core.pager
      delta` and an alias; the guest's `git config --list --show-origin`
      pasted, showing the alias and the includeIf email and none of the
      denied keys. — open: the live guest's `git config --list --show-origin`
      is not pasted (STATUS 2026-09-23 conductor 10:00Z says the carry was
      checked live with fake secrets absent).
- [ ] **Claude merge:** golden test names and output; plus a live guest
      where a permission granted in the guest survives the next `run`, and
      a laptop skill appears in `/skills` in the guest. — open: goldens exist;
      the live guest proof (a guest-granted permission surviving the next
      `run`, a laptop skill in `/skills`) is not pasted.
- [x] **Never carried:** the extended secrets test name and output.
- [ ] **`.env`:** a nested `apps/web/.env` travels with mode 0600; a guest
      copy newer than the laptop one is kept; `ls -l` pasted. — open: the `ls
      -l` from a live guest is not pasted (STATUS 2026-09-23 conductor 10:00Z
      lists .env as checked live).
- [ ] **Forward:** `vite` started in the guest is reachable at
      `localhost:5173` on the laptop within 2 s without any command; a
      screenshot of the tmux status bar; port-taken and portless-collision
      messages pasted. — open: the tmux status bar screenshot and the
      port-taken and portless-collision messages from a live guest are not
      pasted.
- [ ] **OOM:** a guest under a memory hog started from a shell that an
      agent spawned; the journal shows the hog killed and the agent alive. —
      open: the live run found I-213 (protection never reached
      `.claude-wrapped`); the journal showing the hog killed and the agent
      alive on a guest with the I-213 base is not pasted.
- [ ] **cp:** `repose cp :logs/x.log .` and the reverse, transcript pasted. —
      open: the live `repose cp` transcript both ways is not pasted.
- [ ] **Hybrid:** first-run times for this repo and for a repo over
      500 MB, bundle vs clone, pasted, and the threshold set from them in
      the decision. — open: bundle-vs-clone first-run times for this repo and
      a repo over 500 MB are not pasted, and I-203 still says the 20 MB
      `size-pack` threshold is "to be set from measurement".
- [ ] **Caches:** a cold `pnpm install` on a large repo in two guests on one
      host, the second measurably faster, with the times pasted; `docker
      pull` twice likewise; the fallback when the cache is stopped, shown. —
      open: two-guest cold `pnpm install` and `docker pull` times and the
      cache-stopped fallback are not pasted (I-214 records only the live
      495-miss, 0-hit finding before the fix).
- [x] **Trial:** a new account's billing view shows 336 cents, and every
      string grep for `\$10` and "10 of credit" in docs, cmd and apps comes
      back empty.
- [x] **Docs:** every doc in §4 updated in the commit that changed the
      code; `git log --stat` pasted.

Status on 2026-09-23 (branch `ws/15-dev-ergonomics`, see STATUS.md for
the evidence): every part of §2 is built. Closed with local evidence:
never carried, trial, docs. Closed locally and owed on a real guest (the
row's own evidence names a guest or a macOS laptop): TZ (guest NixOS test
shows JST in a new window), git (guest NixOS test's `git config --list
--show-origin`), Claude merge (goldens; live `/skills` and a permission
across a real run owed), `.env` (sshd harness), forward (real
ControlMaster to the harness; screenshot owed), OOM (guestd NixOS test:
hog killed, agent alive), cp (harness), caches (host NixOS test; the
timings on a host owed). Open: timing from a macOS laptop (local harness
numbers in the commits), hybrid threshold from measurement.

