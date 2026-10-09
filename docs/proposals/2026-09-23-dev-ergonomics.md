# Dev ergonomics: the post-M2 list (proposal, 2026-09-23)

**Status: the agreed items are decided as DECISIONS I-195..I-205
(2026-09-23), and the build is workstream 15
(`../workstreams/15-dev-ergonomics.md`).** This file is the reasoning
record: the owner's words, the options that lost, and the items still open
or deferred. If this file and a decision entry disagree, the entry wins.

The source is the owner's list after using repose on real projects
(nuru-wasm, izma, age-calculator) and a `script` recording of a session.
Each item quotes the owner's note, then the direction agreed on 2026-09-23,
the design sketch, and what is still open. "Agreed" means the owner chose
the direction in conversation; it is still not a decision until recorded.

| # | Item | State |
|---|---|---|
| 1 | Carry laptop config (git, Claude) at `run`/`attach` | agreed: git denylist, Claude merge; measure; `config.toml` parked |
| 2 | Timezone follows the laptop on every run | agreed (small) |
| 3 | Extra CLIs (vercel, portless) | agreed: no base change (recorded as I-256) |
| 4 | Shared package store per host | agreed: download cache for npm/pnpm and Docker on each host |
| 5 | Ports: auto-forward while attached | agreed (option A, printed in tmux) |
| 6 | Process lifetime and stale dev servers | agreed (OOMScoreAdjust, visibility) |
| 7 | Copying out: `repose cp`, clipboard shims, last-output bindings | decided: `cp` (I-201); shims and bindings not agreed |
| 8 | Image paste and voice | later: paste built as `repose paste` (I-252), voice dropped (I-256) |
| 9 | Claude login across N projects | open: continued in `2026-09-24-claude-login-shared-folder.md` |
| 9b | Secrets and `.env` | decided: copy `.env` over SSH (I-197) |
| 10 | Hybrid first sync (clone from GitHub, bundle the rest) | agreed |
| 11 | GitHub access without platform branding | connector dropped for now |
| 12 | Trial credit | decided: one day of compute, $3.36 credit (I-205) |
| 13 | Quick path to production | deferred (recorded as I-256) |
| 14 | Observability with Alloy | declined |

---

## 1. Carry laptop config at `run` and `attach`

> git global config should transfer over. .claude settings such as their
> skills, settings.json should transfer over.

**Agreed:** carry on `run` and `attach`, **but only if it adds no visible
delay and no per-OS special cases.** macOS and Linux laptops; Windows is not
supported and not planned. git carries *everything except a denylist*.
Claude's `settings.json` is merged, never overwritten wholesale, and the result
is never invalid JSON.

### Staying off the critical path

- `run` already does one ssh round trip for credentials (I-149). Carried
  files go in that same tar. No new round trip.
- `attach` does no sync today. There, the carry runs *concurrently* over
  the mux connection while tmux attaches, so the user's first keystroke
  waits for nothing. If it fails, the failure is reported inside tmux
  (`tmux display-message`), never by blocking.
- Skip what has not changed. The first sync ssh already returns the guest's
  refs; it also returns a hash per carried file, and the laptop sends only
  the ones that differ. On `attach` the laptop keeps the last hashes it
  sent, per project, in `projects.json`.
- Size, measured on the dev box 2026-09-23: `~/.claude/CLAUDE.md` +
  `settings.json` + `.gitconfig` compress to 839 bytes. `skills/` is
  4.3 MB uncompressed, which is the outlier; at 16 Mbit/s up it is about
  half a second the first time and nothing after that, because of the hash
  skip. `git config --global --list --includes` takes 7 ms.
- **Measure before shipping:** time `repose run` and `attach` on a real
  guest with and without carry, from a macOS laptop on home wifi, and put
  the numbers in the decision entry. The rule is "no visible delay"; the
  number that closes it is under 100 ms added to the p50 of a warm `run`.

### git: everything except a denylist

Read the effective config **for this repository**:
`git -C <repo> config --global --list --includes` (in practice, the
global file plus the includes that apply here). This flattens
`include.path` and `includeIf "gitdir:~/work/"`, so a work email picked by
`includeIf` on the laptop is the email in the guest.

Write it in the guest to `~/.config/git/repose-carried`, included from the
guest's `~/.gitconfig`. Each run replaces that file as a whole, so a key
removed on the laptop disappears from the guest, and anything set inside the
guest by hand (in `~/.gitconfig` itself) is never touched.

Denied, with the reason for each:

| Key | Why |
|---|---|
| `credential.*` | laptop keychains (`osxkeychain`, `libsecret`); the guest has gh as its helper (I-150) |
| `core.sshCommand`, `ssh.*` | laptop ssh binaries and keys |
| `url.*.insteadOf`, `url.*.pushInsteadOf` | an https-to-ssh rewrite for github.com breaks the guest's HTTPS push |
| `user.signingkey`, `gpg.*`, `commit.gpgsign`, `tag.gpgsign` | signing needs the forwarded agent, which is gone when the laptop closes, and that is when agents commit |
| `http.proxy`, `https.proxy`, `http.*.proxy`, `http.sslCAInfo`, `http.sslCert*` | laptop network |
| `core.hooksPath`, `init.templateDir` | laptop paths |
| `safe.directory` | laptop paths |
| `include.*`, `includeIf.*` | already flattened |
| `diff.tool`, `merge.tool`, `difftool.*`, `mergetool.*` | GUI tools |

Also dropped: any remaining value that is an absolute path or starts with `~/`
where that path does not exist in the guest, and any `core.pager` or
`core.editor` whose first word is not on the guest's `PATH` (checked
guest-side with `command -v` in the same ssh). A dropped key is named once
in the summary line, not on every run.

`core.excludesFile`: carry the file's contents to
`~/.config/git/ignore` instead of the path.

### Claude Code: `~/.claude`

Credentials are never carried (unchanged: `features/agents.md`). Config is.

Carried: `CLAUDE.md`, `settings.json` (merged, below), `skills/`,
`agents/`, `commands/`, `output-styles/`, `keybindings.json`, and any
script under `~/.claude/` that `settings.json` points to (hooks,
`statusLine`). Never carried: `.credentials.json`, `projects/`
(transcripts: 466 MB on the dev box, and tenant content), `history.jsonl`,
`todos/`, `shell-snapshots/`, `file-history/`, `paste-cache/`,
`sessions/`, `plugins/` caches, `statsig/`, anything else not on the list.
`~/.claude.json` stays excluded (account state mixed with MCP config;
already decided). Plugins: `settings.json` records only names
(`enabledPlugins`, `extraKnownMarketplaces`), and per the docs check
Claude Code does **not** reinstall them on a fresh machine. The carry step
therefore installs marketplace plugins named in `enabledPlugins` that are
missing in the guest, in the background after the attach, reported through
tmux like other carry output. Local plugins (a laptop path) are skipped and
named once.

**Merging `settings.json`.** The goals: never produce invalid JSON, never
lose the platform's hooks, never lose what the user set inside the guest.

- The repose agent hooks stay in `~/.claude/settings.json`, marked by
  their command (`repose-hook`). The merge strips every hook entry whose
  command contains `repose-hook` from both sides, merges, and appends the
  platform's entries last, so running it twice changes nothing. Moving the
  platform hooks into managed settings was considered and **not relied
  on**. A docs check on 2026-09-23 contradicted itself: it gave the path as
  `/etc/anthropic/managed-settings.json` where earlier material says
  `/etc/claude-code/`, and it said managed hooks both do and don't combine
  with user hooks. Revisit only after testing on a guest.
- The merge: guest file as the base, laptop file on top (the laptop wins on
  any key it sets). Keys only the guest has are kept. `permissions.allow`,
  `permissions.deny` and `permissions.ask` are unioned and deduplicated,
  never replaced. `jq` does this in the guest; it is already in the base,
  and it keeps to the rule that the sync uses only stock tools.
- The union matters: per the same docs check, "Yes, and don't ask again",
  `/model` and `/config` inside the guest write to the *user*
  `settings.json`, the file being carried. With a union, permissions
  granted in the guest survive. `model` and other scalars chosen in the
  guest are overwritten by the laptop's value on the next run if the
  laptop sets them. Accepted: the user configures on the laptop.
- `$HOME` rewriting: string values starting with the laptop's home
  (`/Users/x/`, `/home/x/`) are rewritten to `/home/dev/`. A hook or
  `statusLine` whose command does not exist in the guest after rewriting is
  dropped and named once.
- Safety: the laptop checks its file is valid JSON before sending (an
  invalid file is skipped with one warning, and the guest keeps what it
  has). The guest writes to a temp file, checks it with `jq empty`, keeps
  the previous file as `settings.json.repose-prev`, and renames into place.
  A crash in the middle leaves either the old file or the new one, never
  half of either.
- Accepted: a permission removed on the laptop stays in the guest (union).
  Removing it needs doing in the guest too.

### A standard `config.toml` format (parked 2026-09-23)

Parked by the owner; kept for when it comes back.

`~/.config/repose/config.toml` exists (`interfaces/cli-config.md`) with
flat keys and `sync.exclude`. Proposal: every feature gets one table named
after the feature, and a per-project override is the same table under
`[projects."<normalised remote>"]` (the normalisation `projects.json`
already uses). Nothing is ever written into the user's repository.

```toml
api_url = "https://api.repose.herakraft.co"
default_class = "large"
default_agent = "claude"

[sync]
exclude = ["dist", "*.mp4"]

[carry]
git = true                       # everything minus the denylist
claude = true                    # the list above
extra = ["~/.config/starship.toml", "~/.config/nvim"]
deny_git = ["alias.deploy"]      # added to the built-in denylist

[forward]
auto = true
ignore = [5432]                  # never auto-forward these guest ports

[projects."github.com/heracraft/izma".carry]
extra = ["~/.config/tekid/dev.json"]
```

Rules for the format: keys are `snake_case`; each table is owned by one
feature doc; unknown keys produce one warning, not an error (an older
CLI reading a newer file); `extra` files still go through the
never-carried list (no SSH keys, no Claude or Gemini credentials).

---

## 2. Timezone

> claude rn reads my time in either us east or utc when working inside
> azure worker-1, we dont want that for the real thing.

Already built: I-104 has the CLI send the laptop's IANA zone at create.
worker-1 is the dev box, not a guest. Left to do:

- The zone is set only at create. Send `tz` on every `run` and `attach`,
  and update `/etc/repose/env` when it has changed (someone who travels).
- Running shells and the tmux server keep the old zone. Also run
  `tmux set-environment -g TZ <zone>` so new windows and agents get it.
- Check it on a real guest: `date` in the izma guest should show the
  laptop's zone.

---

## 3. Extra CLIs (vercel, portless)

No base change. `NPM_CONFIG_PREFIX=/home/dev/.npm-global` is already on
PATH (`nix/guest/base/env.nix`), so `npm i -g vercel` works and persists on
the volume. Popular CLIs (vercel, wrangler, supabase, flyctl, portless)
become menu entries in the catalog. vercel's login file joins the carried
tool-login list the way gh's did (a kind 1 secret in `features/secrets.md`).

---

## 4. A shared package store per host

> Global pnpm packages store? ... just do a global ie per host, shared store
> of the packages. so all users can just insta copy, but then ig that would
> mean each microvm needs to be able to symlink into a shared thing.

pnpm's store: files are content-addressed in a store and
*hard-linked* (or reflinked or copied) into `node_modules`. Hard links
need the same filesystem. A store shared across guests can only reach them
over virtiofs, which is a different filesystem from the guest's ext4
volume. So:

| Approach | Saves | Problem |
|---|---|---|
| Shared store over virtiofs, writable by guests | download, disk | **Cross-tenant poisoning.** Tenant A writes a tampered package index into the shared store and tenant B installs it. A non-starter, for the same reason `/nix/store` is shared read-only and only the host writes it |
| Shared store, read-only, written by the host | download, disk | Guests cannot add packages the store lacks; they would have to ask the host to fetch them (a new service and protocol). pnpm cannot hard-link across filesystems, so it copies, and the disk saving disappears. pnpm 10's global virtual store *symlinks* into the store, but that puts node's module resolution on virtiofs, which is slow for metadata-heavy work: every `require` walks the shared filesystem |
| **Pull-through registry cache on the host** (an HTTP cache in front of `registry.npmjs.org`, guests' `npm_config_registry` pointed at it over WireGuard) | download | Saves no disk or extraction time. Private packages and auth tokens must bypass it |

**Agreed (2026-09-23): the registry cache**, for both npm/pnpm and Docker Hub. Reasoning: it's safe because it has no isolation problem and tenant
code never touches it. The same pattern works for Docker Hub (a registry
mirror). **Measure first:** time a cold `pnpm install` on a large repo in
a guest and split download from linking. If the download isn't most of it,
build nothing.

---

## 5. Ports: auto-forward while attached

> A big disadvantage of the current system is port access for dev. We need
> a really strong proxy ... portless both as an impediment since people do
> use it and a potential solution. Traefik?

**Agreed: option A.** Preview URLs later. Output is printed inside tmux.

Localhost matters because `http://localhost:<port>` is a secure context
(service workers, `crypto.subtle`, clipboard APIs), OAuth providers
accept `localhost` redirect URIs (izma's tekid migration is that
case), and cookies behave as they do in local dev. A preview hostname
breaks the redirect URIs and the cookie domains.

Design:

- guestd already reports listening ports in signals (workstream 04). While
  an `attach` or `run` session is open, the CLI subscribes to them (poll
  over the mux at 1 s or less, or a push from guestd) and adds each new one
  with `ssh -O forward -L <port>:127.0.0.1:<port>` on the existing
  ControlMaster. A closed port gets `ssh -O cancel`. There is one
  connection, no new process per port.
- Same local port as the guest port. If the laptop already uses it, take the
  next free one and say so. Ports bound only inside docker networks and
  never published are not seen, as now.
- Output inside tmux: a new forward shows as `tmux display-message -d 4000
  "⇄ localhost:5173 → vite"`, and the status bar right side lists the live
  ones (`⇄ 3000 5173 1355`). Both are set over the mux; nothing is printed
  over the pane.
- `[forward] ignore` in `config.toml` for ports never to forward (a guest
  Postgres the user doesn't want colliding with a laptop one).
- **Portless:** it serves every app on one proxy port (1355) and routes by
  `Host: <name>.localhost`. Browsers resolve `*.localhost` to loopback, so
  forwarding 1355 to 1355 makes every portless app in the guest open on the
  laptop under the name portless printed. If the laptop runs portless too,
  1355 collides; do not silently remap it (the printed URL would be wrong).
  Say instead: `1355 is taken on your laptop (portless?); izma's portless is
  on localhost:1356`.
- `repose open PORT` stays as the explicit, foreground form.
- Traefik: no. The edge gateway is Go and is already meant to be the
  preview proxy; its per-user cookie-auth routing would need Traefik's
  forward-auth plus a service anyway.

Later: preview URLs as designed in `features/ports-and-previews.md`, for
laptop-closed, phone and teammates. Tailscale in the guest as a menu item
(the user's own authkey as a named secret) for people who want every port
and protocol on their tailnet.

This also carries the `BROWSER` shim of item 9.

---

## 6. Process lifetime and stale dev servers

> Preventing user processes from dying (so tmux + claude makes sense),
> could lead to a bunch of zombie processes like throwaway dev servers that
> dont need to stay running. The issue is sometimes you need them to stay
> running.

Already true: `dev` has linger and the tmux session starts at boot, so
nothing dies on detach. The remaining problem is a stale dev server using
the memory an agent needs.

- **Never kill automatically.** Any idle rule eventually kills the
  server someone needed.
- **Protect the agents, not the dev servers.** If a Linux machine runs
  out of memory, the kernel picks one process to kill by a score (mostly
  "who uses the most memory"). `OOMScoreAdjust` is a per-process nudge to
  that score, from -1000 (never pick me) to +1000 (pick me first). Start
  agent windows and the tmux server with a strongly negative value, and
  if memory runs out the three-day-old vite is killed instead of claude.
  The user never sees this unless it fires.
- **Visibility.** guestd already samples processes every 60 s, and process
  names are allowed in logs. `repose status` gains a list: `vite :5173
  up 3d 410 MB`. The existing `oom` warning (I-29) names the process that
  was killed, and the notification says so.

---

## 7. Copying content out

> I like to copy content from files inside the vm so I scp stuff for triage
> ... Might be nice to have this integrated/easy/intuitive.
>
> `fc -ln -1 | xargs -I {} tmux capture-pane -p -S - | sed -n "/{}/,\$p" |
> tail -n +2 | tmux load-buffer -` copies last command into tmux buffer.

**Agreed: a thin `repose cp`.** `scp izma.repose:~/izma/x.log .` works
today, because the generated ssh config makes `<slug>.repose` a host.
`repose cp izma:path/in/guest ./local` and the reverse wrap scp with the
project resolved as other commands resolve it (so `repose cp :logs/x.log .`
in the project directory means the current project), with relative paths
resolved from `~/<slug>`.

Proposed, cheap:

- **Clipboard shims.** `pbcopy`, `wl-copy` and `xclip -i` in the guest write
  OSC52 to tmux, which already has `set-clipboard on`, so `cat x | pbcopy`
  or an agent's own copy tool lands on the laptop clipboard. Terminal
  support: kitty, WezTerm, iTerm2, Alacritty, foot yes; GNOME Terminal no.
  Large payloads should point to `repose cp`.
- **Last-output bindings, built on OSC 133 prompt marks** rather than text
  matching (the `sed "/{}/"` version breaks on a repeated command, a
  multi-line command, or fish). The base shell and starship emit OSC 133,
  and tmux 3.4+ jumps between prompts in copy mode:
  - `prefix y`: copy the last command's output (to the tmux buffer and
    OSC52)
  - `prefix Y`: copy the command and its output
  - `prefix a`: paste the last output into the agent's window, for the
    "look at this error" case. This also avoids the two-pane selection
    problem.

---

## 8. Image paste and voice (deferred; later I-252 and I-256)

> Voice mode and image pasting gotta work somehow

They fail because Claude Code reads a pasted image from the clipboard of the
machine it runs on, and dragging a file into a terminal types a laptop
path that doesn't exist in the guest. Voice mode records from the local
microphone of the machine running claude.

Image options:

| Approach | For | Against |
|---|---|---|
| Forward a clipboard socket into the guest and shim `xclip -o` / `wl-paste` | Ctrl+V works unchanged | **Anything in the guest can read the laptop clipboard while attached.** A malicious postinstall script gets the user's passwords. Rejected |
| **The CLI watches the input stream.** On Ctrl+V with an image on the laptop clipboard, or a bracketed paste that is an existing local file path (a drag-and-drop), upload the file to `/tmp/repose-paste/<ts>.png` over the mux and type the guest path instead | Explicit, one direction, works for every agent, drag and drop works | The CLI has to become a pty proxy instead of exec'ing ssh. Contained, but real work |

To test before building the second row: whether Claude Code attaches an
image when its path is typed or pasted. The docs check said a pasted path
stays text and only drag-and-drop attaches. In a terminal, though, a drop
*is* a pasted path, so the claim doesn't hold together. If a typed path
doesn't attach, the CLI would type Claude's own `@path` reference instead.

Voice (the docs check confirms it needs a microphone on the machine running
claude and does not work over SSH): build nothing. OS dictation (macOS dictation, Superwhisper, Wispr
Flow) types into any terminal. Remote Control from the Claude app may cover
voice and images natively; *verify*. It's a reason to prefer a full login
over a setup token (item 9).

---

## 9. Claude login across N projects (open: needs its own session)

> If we are saying let your agents run in the background and each gets a
> box, then having them log into claude code for each `repose run` might
> not be the best way to do it.

**Owner, 2026-09-23:** users logging in N times is not acceptable, and
Remote Control is how voice gets through (item 8), so losing it is probably
a deal breaker. This needs a dedicated, in-depth session. The points below
frame that session and settle nothing.

### What is fixed

- Credentials are on the persistent volume, so today it's one login per
  **project**, not per run (if it shows up on every run, that's a bug).
- `claude setup-token`: lasts a year, one per user, and would work for every
  box if stored as a user-level secret. Per the docs check on 2026-09-23,
  it lacks **Remote Control**, claude.ai connectors and scheduled tasks.
  With RC as the voice and phone path, it isn't enough alone.
- `docs/features/agents.md` and DESIGN §11 today: the platform never
  stores, proxies or copies Claude credentials, and each user authenticates
  themselves.

### What the session has to settle

1. **Is copying a full login between one user's own boxes technically
   viable?** It depends on whether Claude's OAuth refresh tokens rotate. If
   they do, two boxes holding the same refresh token log each other out on
   the first refresh. Experiment: log in on guest A, copy
   `.credentials.json` to guest B, use both past a few access-token
   lifetimes (over 24 h), and record whether either is logged out. This
   comes first, because if it fails, options 2 and 3 below are dead
   whatever the policy says.
2. **Is it allowed?** The current rule reads Anthropic's terms as "no
   copying". The terms' concern is each *user* authenticating with *their
   own* credentials on a hosted platform; copying one user's own login to
   that same user's other machines, never across users, may be within them.
   Re-read the current terms and, if unclear, ask Anthropic. Changing it
   reverses a design rule, so it needs a `DECISIONS.md` entry and a
   `SECURITY.md` note.
3. **If copying is viable and allowed, who copies?**
   - *Laptop to guest:* the laptop is already logged in (Keychain on macOS,
     `.credentials.json` on Linux). The same rotation question applies,
     now with the laptop as a third holder.
   - *Guest to guest via the laptop:* log in once in any guest, and the CLI
     copies that login to the user's other guests on `run`. The platform
     never sees it.
   - *Via the platform:* a fourth secret home and exactly what the rule
     forbids. Unlikely.
4. **If copying is out:** the best remaining experience is one click per
   *new* project: the `BROWSER` shim plus auto-forward (item 5) opens the
   authorize page on the laptop, the user clicks once, and the box keeps
   the login for life. Whether Claude Code honours `BROWSER` is not
   documented; test it. Voice without RC is OS dictation on the laptop, and
   on a phone it's an SSH app plus the phone keyboard's dictation, which
   works with any login.
5. **What "each agent gets a box" means.** Several agents in one project's
   box (windows in one tmux session) need one login. N logins only hurts
   when N is projects, so the product question is how many boxes a typical
   user has.

`ANTHROPIC_API_KEY` as a user-level secret stays an option for
pay-per-token fleets.

---

## 9b. Secrets and `.env`: how it works today

`repose secrets set NAME` prompts for a value, the API stores it encrypted,
and the guest gets it as the file `/run/repose/secrets/NAME` (tmpfs) and
as `$NAME` in every login shell. So code reading `process.env.DATABASE_URL`
works. **Every secret belongs to one project.** There is no user-level
scope; `features/secrets.md` lists it as deferred.

`.env` files **do not travel** today. They are gitignored, and gitignored
files never sync (`features/sync-at-launch.md`: "deliberate and
documented, named secrets are the supported path"). So someone with a
20-line `.env` has to run `secrets set` 20 times, and anything that reads
the *file* rather than the environment (`docker compose env_file`, a
script that runs `source .env`) finds nothing.

Options:

| Option | For | Against |
|---|---|---|
| A. **Copy `.env*` over SSH on `run`**, as tool logins are (laptop to guest, never through the API; the newer side wins by mtime) | The laptop's behaviour, nothing to learn, survives laptop-closed and reboots | Lives on the guest disk, so it's in snapshots. That's already true of gh's token and the code itself. Changing a value needs a laptop `run` |
| B. Copy to tmpfs with a symlink in the checkout | Not in snapshots | Gone after a guest reboot (the 03:00 base bump) until the next `run`; the agent breaks overnight |
| C. **Import into named secrets** on first run (`Found .env with 14 values. Store as izma secrets? [Y/n]`) and have guestd write the `.env` file back from them at start | Rotation from the dashboard or a phone, not in snapshots, survives reboot | Two sources of truth (the laptop's file and the stored copy); a guest-side edit to `.env` is lost at the next start |

**Decided (owner, 2026-09-23): A** (I-197), with named secrets kept for values that must change without a
laptop. A generalises the "copied over SSH" home to laptop-held `.env`
files, so it's still three homes, but it reverses the sync doc's rule and
needs a decision entry. `repose secrets import .env` stays useful for C
users either way.

User-level secrets, if item 9 needs them:

```
$ repose secrets set ANTHROPIC_API_KEY --all-projects
Stored for all your projects. Updated in 3 running guests.

$ repose secrets list
NAME                SCOPE          UPDATED
ANTHROPIC_API_KEY   all projects   2026-09-23 10:02
DATABASE_URL        todo-app       2026-09-17 14:02
```

The same table with the project nullable, the same encryption. A
project-level secret shadows a user-level one of the same name. A set is
pushed to every running guest. Build-log redaction covers these values
too.

---

## 10. Hybrid first sync

> if we are copying over gh creds, why not clone on the other side? ...
> the issue is untracked, uncommited, unpushed work. plus secrets (.env).
> do we transfer the .git dir too?

**Agreed (2026-09-23).** Clone from GitHub for the heavy first sync,
bundle the rest. Only the **first** sync is affected (guest has no
commits); later syncs are already deltas of a few KB.

- Eligible when the remote is on github.com and either the repo is public
  (no credentials needed; nuru is) or gh's login travelled.
- **Judge the size first; it's free.** `git count-objects -v` gives
  `size-pack` in milliseconds (7 ms here; this repo is 28.5 MB). Rule: if
  `size-pack` is over a threshold, clone in the guest and then bundle only
  what GitHub lacks (unpushed commits). Otherwise bundle as today.
- Threshold: at a typical 10 to 20 Mbit/s home upload, 20 MB is about
  10 s over the laptop link, while GitHub to Azure is seconds for hundreds
  of MB. Start at 20 MB and set it from a measurement: time both paths on
  this repo and on a large one (a monorepo over 500 MB).
- A full clone, not `--filter=blob:none`: a partial clone fetches blobs
  lazily later (blame, log -p, checkout of an old commit), which fails
  confusingly once the gh token expires or the repo goes private.
- The cost is one extra round trip on the first run (clone, then report
  refs, then bundle), and a second code path to test.
- Sending `.git`: the working tree is the code already, and history adds
  little exposure beyond secrets committed in the past. The promise is
  isolation and never logging tenant content, not zero-knowledge;
  per-project LUKS (deferred) is the answer for people who want more.

---

## 11. GitHub access without platform branding

> set up github connector

**Owner, 2026-09-23:** nothing in GitHub may say "repose" or "repose[bot]".
It's the user's dev machine, and everything done from it is done by the
user.

That rules out an **installation token**, since its actions come from
`repose[bot]`. It probably rules out a GitHub App **user token** too:
actions are done as the user, but GitHub attributes some of them to the
App, and "Repose" appears in the user's authorised-apps list. *Verify*
where GitHub shows the attribution before closing this.

Two options meet the rule:

- **Today's copy of gh's own login** (`hosts.yml`, from the laptop).
  Actions are the user's, through the "GitHub CLI" OAuth app the user
  already authorised. Nothing names repose. The cost is breadth: that
  token can reach every repo the user can.
- **`gh auth login` inside the guest** via the `BROWSER` shim (item 9),
  for people without gh on the laptop. It's gh's own OAuth app again, one
  click.

The connector is dropped for now. Its gains (per-repo scope, starting a
project with no laptop) come back only if a way is found that keeps actions
branded as the user's.

---

## 12. Trial credit

> NO FREE TIER actually

There's already no free tier: a card is required before the first guest,
and new accounts get 3 projects (DECISIONS R4-8). The $10 credit is about
71 large-hours or 142 small-hours, **about three days of large**.

**Agreed:** credit one day of compute, and the copy says the time, not the
amount: "Your first day of compute is on us." One day on large is $3.36
(and 48 hours on small). Open: whether that's a $3.36 credit that also
stretches to two days on small, or a credit of 24 guest-hours on any class
(simpler to explain, but a second meter unit). Decided (I-205): the dollar
credit with day-worded copy, since it keeps one meter. The reason R4-8 gives for a
credit (it tests the meters) holds either way. It changes `PRICING.md`,
`features/pricing.md` and the dashboard copy, and needs an entry amending
R4-8.

---

## 13. Quick path to production (deferred)

> we might want -- in the future -- to add a quick path to production.
> Like a built in skill for agents ... deploy the thing immediately no git
> needed no other subscription until they are done verifying their idea.

Repose should not host production. Hosting is the abuse vector item 12
guards against, and it's a second product. The path is a skill in the
base that deploys to *the user's* Vercel, Coolify or Fly with carried
credentials (item 3), plus `preview: public` URLs (item 5, later) for
"show someone now".

---

## 14. Observability with Alloy (declined)

> Deep obs with alloy shipping off to prometheus and loki inside my local
> machine (solus)?

Declined on 2026-09-23. Fluent Bit and node_exporter on hosts already ship
to the monitoring server; swapping in Alloy is churn with no gain, and a
home machine as the sink means gaps whenever it's offline. For observing
one's own apps inside a guest, Alloy can be a menu entry with the
endpoint as a named secret.
