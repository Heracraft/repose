# Secrets

Three kinds of secret, three treatments, and no fourth place. Each treatment
exists because the other two would be wrong for that kind.

## What the user sees

```
$ repose secrets set DATABASE_URL
Value for DATABASE_URL: **************************************
Set DATABASE_URL (pushed to running guest)

$ repose secrets list
DATABASE_URL	2026-09-17 14:02
GEMINI_API_KEY	2026-09-15 09:41

$ repose secrets rm GEMINI_API_KEY
Removed GEMINI_API_KEY
```

On a stopped guest `set` prints `Set NAME (will be delivered at next
start)`. `--from-file PATH` and `--from-env` read the value without a
prompt. `list` prints name and last update, tab-separated, no header.

`repose secrets import [FILE]` (I-277) sets every `NAME=VALUE` of a dotenv
file (default `./.env`, `-` for stdin) through the same PUT, one per name,
replacing existing ones like `set`; the summary names names only
(`OLD (replaced)`), `--dry-run` sends nothing, and a file with any invalid
name or oversize value is refused whole before anything is sent. The file
is read, never written; the values go to the one home named secrets have.

Synced logins happen inside `repose run` with no output of their own.

## Kind 1: tool logins the laptop already has

Copied at every `repose run` over the SSH session into the guest, owned by
`dev`, mode 0600. The list is exact and lives in
`interfaces/guest-conventions.md`: gh's `hosts.yml`, Codex's `auth.json`,
opencode's `auth.json`, and the git identity (inside the carried git
config below, since I-195); when gh's login
travels, whatever the project's remote, the guest's git also gets gh as
its credential helper for github and rewrites `git@github.com:` and
`ssh://git@github.com/` to HTTPS, so an agent can push to an SSH remote (a gh
token the laptop keeps in its keyring is written into the copy of
`hosts.yml` that travels; DECISIONS I-150). All of it is one ssh, before
the checkout is synced. The platform never sees these; they travel laptop
to guest inside SSH.

Why copy rather than store: the user already has them, they rotate on the
laptop, and holding a copy of a GitHub token for every user in a database is
a liability with no benefit. Why copy at all: the agent must push while the
laptop is closed, and the laptop's ssh-agent is never forwarded
(DECISIONS I-247): with it, any process in the guest (an agent running
with every permission, a package's install script) could sign with the
laptop's keys while the user is attached, which is the exposure the
machine exists to remove. The generated `~/.ssh/repose/hosts` says
`ForwardAgent no` and the gateway refuses agent forwarding from any
client.

Other git hosts (GitLab, Bitbucket, self-hosted) have no copied login.
Two options, both in the public docs' secrets page ("Other git hosts"):
an HTTPS token stored as a named secret (kind 3) plus a per-host
`credential.<url>.helper` that echoes it, with an `insteadOf` for the
host's SSH URLs; or a deploy key generated in the guest and added to that
one repository with write access. The deploy key is the user's own file
on the guest disk (in snapshots), not a repose secret, the same as any
other file the user writes there.

The user chooses which of these travel, on the laptop (DECISIONS I-422):
`repose secrets choose` toggles `gh`, `codex`, `opencode` and `env` (the
gitignored `.env` files, I-197) and saves the list in the laptop's
`config.toml`, for every project or, with `--project`, for one. The api
never holds the choice, for the same reason it never holds the logins.
Everything is copied until the user chooses. A login turned off is not
sent, and the copy an earlier run left in the guest is removed while it is
still byte for byte the laptop's; one made in the guest stays.

The Vercel CLI's login is not copied (DECISIONS I-298): its `auth.json`
holds a token for the whole Vercel account, every team and project, where
an agent with full permissions could deploy, delete or read env vars of
projects that are not this one, and could take the token off the machine.
Two paths, both in the public docs' secrets page: `vercel login` on the
machine (the file lands on the guest disk, in snapshots, like the deploy
key above), or a token the user scopes to one team and gives an expiry,
stored as a named secret `VERCEL_TOKEN` (kind 3), which the Vercel CLI
reads from the environment. A copy an earlier CLI made is removed at the
next `run` while it is still byte for byte the laptop's file.

Rules that must hold:

- A file missing on the laptop is skipped silently; a file present is copied
  every run, so a rotated token reaches the guest on the next run.
- The copy never overwrites a guest file that is newer than the laptop's,
  because a login done inside the guest (Codex device auth, say) would be
  clobbered. Mtime decides; the CLI prints which side won when it skips.
- `~/.claude/.credentials.json`, `~/.gemini/oauth_creds.json`, and any SSH
  private key are never copied. A test feeds a laptop home containing all
  of them and asserts the tar stream contains none.

### Laptop config that travels the same way

Not secrets, but carried over the same SSH, laptop to guest, never through
the api (DECISIONS I-195..I-198; `interfaces/guest-conventions.md` "Laptop
config the CLI carries" has the exact paths):

- **git config** (I-195): the laptop's effective global config for the
  checkout (`git config --global --list --includes`, so an `includeIf`
  that picks a work email is flattened in), minus `credential.*`,
  `core.sshCommand`, `ssh.*`, `url.*`, signing (`user.signingkey`,
  `gpg.*`, `commit.gpgsign`, `tag.gpgsign`), proxies and TLS client
  settings, `core.hooksPath`, `init.templateDir`, `safe.directory`,
  includes, and diff and merge tools; and every key that holds a secret
  (DECISIONS I-211): `http.extraHeader` (also per URL, where PATs sit),
  `http.cookieFile`, `sendemail.smtpPass`, `github.token`,
  `hub.oauthtoken`, `core.gitProxy`, and any key whose name ends in
  `token`, `pass`, `password` or `secret`, or whose key or value holds a
  URL password or a known token prefix (counted in one line, never
  shown; I-211). A value that is a laptop path
  missing in the guest, and a `core.pager` or `core.editor` whose command
  is not on the guest's PATH, are dropped and named once. It lands whole
  in `~/.config/git/repose-carried`, included first from `~/.gitconfig`,
  so a key the user sets in the guest's `~/.gitconfig` wins; a key
  removed on the laptop disappears from the guest. `core.excludesFile`
  travels as its contents, to `~/.config/git/ignore`. Carried on `run`,
  and on `attach` when it is run from the project's checkout (the only
  place its `includeIf` rules resolve the way they do for this project).

- **Claude Code config** (I-196): `CLAUDE.md`, the merged
  `settings.json`, `skills/`, `agents/`, `commands/`, `output-styles/`,
  `keybindings.json` and the scripts `settings.json` runs; never
  `.credentials.json`, transcripts, history or any other state
  (`agents.md` has the list and the merge). `settings.json` leaves the
  laptop without `env` (API keys, MCP tokens), `apiKeyHelper`, the `aws*`
  and `gcp*` auth helpers, `otelHeadersHelper` and `forceLoginMethod`
  (DECISIONS I-211), and without any entry whose strings hold a URL
  password or a known token prefix; files named `.env*`, SSH identities
  (`id_ed25519`, `id_rsa.pub`, …),
  `*credentials*`, `*.pem`, `*.key`, `*.p12` never travel from the
  carried directories. `TestCarryClaudeNeverCarriesSecrets`
  plants `.credentials.json` (also inside `skills/`), `projects/`,
  `history.jsonl`, `todos/`, `shell-snapshots/`, `file-history/`,
  `plugins/`, `statsig/`, `~/.claude.json`, an SSH key and Gemini's
  OAuth file in the laptop home, and those settings keys in its
  `settings.json`, and asserts none of them is in the carry stream.

- **`.env` files** (I-197): gitignored `.env` and `.env.*` files in the
  checkout, laptop to guest over the sync's SSH, mode 0600, newer side
  wins by mtime (`sync-at-launch.md`). This generalises the "copied over
  SSH" home to files the laptop holds; it is still three homes, not four.

A part that has not changed on the laptop since the guest last applied it
is not sent: the guest keeps one marker per carried item under
`~/.repose/carry/` (DECISIONS I-206), returned in the sync's first SSH.
The tool logins are one such item (`creds`, I-224): they go again when a
laptop file's content or mtime changes, or when a file they wrote in the
guest is gone (`~/.repose/creds-paths`, checked by the same SSH). A login
done inside the guest is still never overwritten; with nothing sent, it
is simply kept.

## Kind 2: Claude Code

Never copied, never read by repose. The user logs in inside any one of
their guests, with Claude Code's own `/login`; the file Claude Code writes
is on the user's login share, one directory per user on the host that
every guest of that user mounts (DECISIONS I-278), so every other project
of that user is signed in too. It is the same kind of secret in the same
treatment, the user's own login in the user's own machines; the share is
where those machines keep it, not a place repose puts it:

- Where: `/var/lib/repose/users/<user_id>/claude-auth/.credentials.json`
  on the host, bind-mounted over `~/.claude/.credentials.json` in each
  guest. Only that file: `settings.json` and hooks stay per guest, so an
  agent in one project cannot run commands in another.
- Why not in the guest, as before: users open many projects and should
  log in once. Why not copied between guests: copying a Claude credential
  is what Anthropic's terms forbid a platform, and a copy does not follow
  the original's refresh.
- Who can read it: the user's guests, root and `repose-auth` on the host
  (SECURITY.md). Not in snapshots, not in any log, never sent to the api.
- How long: until 30 days after the user's last machine on that host is
  gone. `/logout` in any machine is Claude Code's own.
- One per host: machines of one user on two hosts need one login each.

The fallback is a setup token stored as a named secret. agents.md carries
the reasoning and the Anthropic policy behind it.

## Kind 3: named secrets

`repose secrets set NAME` and the dashboard's secrets page. Stored by the
API as ciphertext in Postgres, encrypted with a per-user data key that is
itself wrapped by an Azure Key Vault key (DECISIONS R3-10). Delivered to the
guest at start and on every change as `/run/repose/secrets/NAME` on a
tmpfs, mode 0400, owner `dev`, and exported into login shells through
`/run/repose/secrets.env`.

Why central: an unattended agent needs them when no laptop is connected, and
a stopped guest that restarts at 03:00 for a base bump needs them too. Why
envelope encryption in Postgres rather than one Key Vault secret per value:
Key Vault is priced and rate-limited per operation, and a guest start would
need one call per secret.

Rules that must hold:

- Names match `[A-Z][A-Z0-9_]{0,63}`. Values up to 64 KB. Binary values are
  base64 on the wire and raw in the file.
- The API never returns a value. `GET /secrets` lists names and timestamps
  only. The dashboard has no "reveal".
- A `set` on a running guest pushes `UpdateSecrets` and the file is updated
  within 5 seconds; `secrets.env` is regenerated. Already running processes
  are not restarted; the CLI says so.
- A `rm` deletes the ciphertext row and the guest file. The audit log
  records the action, the name, and never the value.
- Secrets are per project. The same name in two projects is two secrets.
  Each ciphertext is bound to its project id and its name (AES-GCM
  additional data, DECISIONS I-433), so a row copied into another project
  or renamed does not decrypt. Rows written before I-474 were bound to
  the name alone; each api process rewrites them in the bound form in the
  background at start, retrying while Key Vault is unreachable, and the
  api reads the old form for one more release.
  `repose fork` copies the source's values into
  each copy in the same transaction that creates it, opened and sealed
  again for the copy under the same user key and wrapped key, which is the
  same home (DECISIONS I-254); a later change in one project does not
  reach the others. Copying between arbitrary projects is a later feature.
- Secrets appear in no log, no build log, no event summary, no error
  message. Build logs are scanned for every current secret value of that
  project before storage and matching substrings are replaced with
  `[redacted]`, because a Nix build that echoes an environment variable is a
  common way a token leaks into a log that lives 90 days. The scan looks for
  each value whole, its standard and URL-safe base64, and, for a value of
  several lines (a PEM key, a JSON credential), each line on its own except
  PEM armour lines, since logs are stored a line at a time. The error a
  failed build stores (it carries the builder's log tail) goes through the
  same scan, and a fragment holding any of those strings is refused before
  evaluation. Values under 4 bytes are not matched: they would turn
  ordinary words into `[redacted]` (DECISIONS I-440).
- Rotating the Key Vault key re-wraps every DEK without touching
  ciphertext; `repose-admin secrets rewrap` does it and is rehearsed before
  launch.

## Where secrets are not

Not in the repo. Gitignored `.env` files do travel, over the sync's SSH and
never through the API (Kind 1 above, I-197; sync-at-launch.md). Not in the fragment (a Nix expression is a build input,
lands in the world-readable store, and shows in build logs; the pipeline
rejects a fragment containing a string that matches a current secret value
of that project). Not in tmux history the platform can see; the platform
cannot see tmux history.

## Depends on

Workstreams 05 (secrets routes, envelope encryption, Key Vault), 03
(`UpdateSecrets` delivery), 04 (`WriteSecrets`), 07 (`secrets` commands,
login sync), 08 (dashboard page), 12 (fragment scan, build log redaction).

## Deferred

Per-project LUKS so an operator with root on a host cannot read a stopped
tenant's disk (R3-10 revisit). User-level secrets shared across projects.
Copy between projects. Secret versions and rollback.
