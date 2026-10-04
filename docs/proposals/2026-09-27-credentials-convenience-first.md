# Credentials: convenience by default, scoped keys by choice (proposal, 2026-09-27)

**Status: proposal. Nothing here is decided except I-298 (the Vercel login
stays on the laptop), which is committed on branch
`worktree-agent-a93d6b299344278b5` and waiting to be merged.** Every item
below that changes behaviour needs its own `DECISIONS.md` entry and ships
with its public docs. Where this file and a decision entry disagree, the
entry wins.

Sources: the owner's direction in the session of 2026-09-27, and three
research reports:

- `reports/Repose credentials without a firewall.md` (the main one, with a
  per-provider minting table and a user study plan)
- `reports/Repose credential proxy research.md` (placeholder proxies, what
  developers say about agent firewalls, incidents)
- `reports/Repose agent apps and Docker research.md` (Docker Cloud
  Sandboxes)

Notes behind them are in `research_notes/Repose credentials without a
firewall/` and `research_notes/Repose credential proxy research/`. Nothing
in them was tested against a live guest.

## The position

Developers already take the risk of running agents with full permissions
on their laptops, with every login they own sitting next to the agent.
The need for a machine that keeps running is real, but people are managing
without one. Repose wins on convenience, not on safety they didn't ask
for. So:

- **The default is convenient, and the risk is the user's to take.** Repose
  copies what makes the machine feel like the laptop, and says plainly
  what it copied.
- **Safety is opt-in.** A user who wants it sets up scoped keys once, and
  from then on every machine gets its own short-lived keys. Repose never
  pushes anyone into it.
- **No firewall by default.** The agent reaches the whole internet.

The default is still better than the laptop, and the docs can say so: SSH
keys, other checkouts and the rest of the home directory never reach the
machine. What travels is the project, its `.env` files and the logins
listed below.

## Why no firewall

A firewall acts on where the agent can connect. In every documented
incident, the damage came from what a credential was allowed to do:

- **Nx s1ngularity (Aug 2025) and Shai-Hulud 1 and 2 (Sep and Nov 2025)**
  stole GitHub, npm and cloud tokens and published them through
  `api.github.com`, using the victim's own `gh` token to create public
  repos. s1ngularity alone made more than 6,700 private repos public. Every
  dev machine has to reach GitHub, so an allowlist lets this through.
- **PocketOS (Apr 2026):** a Cursor agent found a root-scoped Railway token
  in an unrelated file and deleted the production database and its backups
  in one API call. Nothing left the machine.
- **Replit (Jul 2025):** an agent destroyed a production database in place.

Meanwhile a firewall blocks what agents are used for. Browser automation
and docs lookup are the most used MCP servers, and agents trigger between
30% and more than half of Vercel's deployments (sources disagree). The
complaints about Claude Code on the web, Codex, the Copilot agent and
Docker Sandboxes are about allowlists that miss the CDN, toolchain and
deploy hosts real work needs.

So protection goes on the credentials. Egress stays open, as R2-10 already
says. A locked mode is built only if a user asks for it.

## Default mode

What `repose run` does for a user who changes nothing:

| What | Default | Opt-out | Status |
|---|---|---|---|
| Gitignored `.env` files | Copied (I-197) | **New:** a way to not copy them, per project | Opt-out to build |
| `gh` login | Copied, full scopes (I-150) | Secure mode replaces it (below) | As today |
| Codex and opencode logins | Copied | — | As today |
| Vercel login | **Not copied.** Use `vercel login` on the machine or `repose secrets set VERCEL_TOKEN` with a scoped token | — | I-298, on a branch |
| Claude login | Never copied; one login share per user (I-278) | — | As today |
| Named secrets | tmpfs in the guest | — | As today |
| Egress | Open, shaped | Locked mode, if ever asked for | As today |

Two additions make the default honest without taking anything away:

1. **The sync names what it found.** `repose run` prints the names (never
   the values) of variables that look like live keys (`sk_live_`,
   `rk_live_`, known provider prefixes) or remote databases (a
   `DATABASE_URL` whose host is not localhost, a private address, `.local`
   or a Compose service name). One line, and nothing is changed. This also
   warns when `localhost:5432` will find nothing in the machine because
   Postgres runs on the laptop, not in the guest.
2. **The docs state the norm.** A `.env` is for local things. The research
   found no written standard for this, so the docs give it as advice
   backed by the incidents above, not as a rule. Text ready for the
   secrets page:

   > Keep production credentials out of your checkout. The machine gets a
   > copy of your gitignored `.env` files, and an agent there can read
   > every value and use it for anything that key allows. That is fine for
   > values that only work in development: a database in Docker Compose (a
   > snapshot restores it with the machine), a Stripe test key, a random
   > `JWT_SECRET`. It is not fine for a production database URL, a live
   > payment key or a token that can delete things, because an agent that
   > makes a mistake then makes it in production. Use a test key, a dev
   > database, or a key limited to sending, one project or a spending cap,
   > that you can revoke.

A database in Docker Compose inside the machine is already covered: wreck
it, restore the snapshot, and it comes back. That is true today and worth
saying on the site.

## Secure mode (opt-in)

One idea: **every key the agent gets from Repose reaches only this project
and dies with the machine.** Repose does not claim more than that. With open
egress the agent can still send anything it can read, including the
project's code and any key left plain. Scoping limits what a stolen key
can do. It does not stop the theft.

Secure mode has three parts. A user can take any of them alone.

### 1. GitHub: a token for this repo only, minted at each run

The user installs Repose's GitHub App once, choosing **All repositories**.
From then on, every `repose run` asks GitHub for an installation token
narrowed to the repo being run and to Contents and Pull requests only:
`POST /app/installations/{id}/access_tokens` with a `repositories` list and
a `permissions` object. The token expires after an hour.

- **New repo tomorrow:** covered, because "All repositories" includes repos
  created later. Nothing to click.
- **An old repo the user no longer wants reachable:** nothing is reachable
  between runs. A repo only gets a token while a machine for it is running.
- **New org:** the App has to be installed on each org once, and some orgs
  need an admin to approve it. This is GitHub's rule and the one extra
  click in the design.
- **Someone else's repo:** not reachable; work in a fork.

What the token cannot do: create repos, change visibility, write gists, or
touch any other repo. Those need the Repository creation, Administration
and Gists permissions, which the token does not get. That closes the
channel s1ngularity and Shai-Hulud used.

What it can still do: push anything the agent reads into this repo. If the
repo is public, that is publication. A ruleset on the default branch stops
force-pushes, and that is the user's GitHub setting, not Repose's.

Design points:

- **Secure mode stops copying the laptop's `gh` login.** Otherwise the broad
  token rides along and the scoped one protects nothing.
- **The token must refresh while the laptop is closed.** The api mints a new
  one before the hour is up for every running guest in secure mode and
  pushes it through the existing named-secret path to the tmpfs. `gh`
  reads its config file on every call, so a `hosts.yml` that points at the
  tmpfs copy stays current. A `GH_TOKEN` environment variable would not,
  because long-running processes keep the value they started with. To
  verify on a live guest.
- **The App's private key is the new risk.** With "All repositories", that
  one key can mint a token for any repo of any user who opted in. It lives
  in a KMS, not a file, and the docs say what it can reach.
- **Pull requests show as `repose[bot]`.** Commits keep the user's git
  identity. Acting as the user would need GitHub's user-to-server tokens,
  which means an OAuth device flow. Not proposed.
- **Without the App,** the zero-build path is a fine-grained token the user
  makes by hand for one repo and stores with `repose secrets set GH_TOKEN`.
  It gives the same limits and costs only a docs section. It can ship first.

### 2. The credential manager: parent keys in, per-machine keys out

The user stores a parent key once, a key that can create keys. When a
machine starts, Repose mints a child for that machine, scoped and expiring
where the provider allows, and writes it into the machine's copy of `.env`
in place of the laptop's value. The laptop's file is never touched. When
the machine stops or is destroyed, the child is revoked.

Providers fall into three groups (full table with endpoints in the main
report, section 6):

| Group | Providers | What secure mode does |
|---|---|---|
| Mints a scoped key that expires | OpenAI, AWS (STS), GCP, GitHub App, Cloudflare, Vercel, Mailgun, Docker Hub, PlanetScale, Turso, MongoDB Atlas database users, Neon branches | Mint per machine; expiry is the backstop |
| Mints a scoped key with no expiry | Resend (sending-only, one domain), SendGrid, Twilio, Pinecone, Gemini/Google API keys, OpenRouter (credit cap), Supabase secret keys | Mint per machine; Repose must delete it on stop and sweep orphans |
| Can't mint | Stripe (restricted keys are made in the dashboard only), Anthropic (its Admin API cannot create keys), Groq, Replicate, Clerk, Sentry, PostHog, Railway, Render | The user makes one restricted or test key by hand; every machine gets that same key |

By `.env.example` counts on GitHub, providers that can mint cover roughly
three quarters of provider-key mentions.

The cost is the parent key itself. To mint, Repose needs a key more
powerful than anything in the user's `.env`: an OpenAI Admin key, a
full-account Vercel token, a full-access Resend key. That is a setup step,
and it makes Repose's Postgres a store of admin keys, which is a target.
That is why this is opt-in and not the default.

Design points:

- **Secret homes.** The parent is a named secret (ciphertext in Postgres) and
  the child lands in the guest like any secret. No fourth home. A child
  written into the checkout's `.env` is on the guest disk and so in
  snapshots. That is harmless for expiring children, and revoke-on-stop
  covers the rest. Restoring a snapshot brings back revoked children, so
  restore mints fresh ones.
- **Idempotency.** Provider mint calls are not idempotent, and hostd or the
  api may restart. Every child is named `repose-<guest id>`, and its handle
  is recorded before the value leaves the api. A resent command revokes
  the recorded child and mints a new one rather than returning a stored
  value, since storing it would make a new home. At start, and in a daily
  sweep, list each provider's `repose-*` keys and delete those whose guest
  is gone. The sweep is required anyway: SendGrid allows 100 keys per
  account and Atlas 100 users per project.
- **Keys that sign inside the machine.** AWS request signing happens in the
  guest, so a one-hour STS credential has to be refreshed without
  restarting the dev server. The AWS SDKs read a local credentials
  endpoint (`AWS_CONTAINER_CREDENTIALS_FULL_URI`) that the guest agent can
  serve. To verify.
- **Anthropic stays plain.** It can't mint, and Repose never intercepts
  `api.anthropic.com`, because the Claude login share (I-278) talks to it
  and Repose never opens that login. The docs advise a Console key with an
  expiry, in a workspace of its own. Workload identity federation (which
  AWS, GCP, OpenAI and Anthropic support) would remove the parent key
  entirely. It needs a spike before anyone promises it.
- **Build small.** Infisical, Vault, OpenBao and Doppler don't cover Stripe,
  Resend, SendGrid, Twilio, Vercel, Anthropic or Cloudflare. Each provider
  Repose needs is a few dozen lines against its API: create with scope and
  expiry, revoke by handle, list by name. Start with the two providers the
  user study names, not twenty-five.

### 3. A database branch per machine

`DATABASE_URL` is the most common value in `.env.example` files and the one
agents destroy. For Neon, Supabase and PlanetScale, secure mode can give
each machine its own branch, deleted with the machine, so "restore from
snapshot" extends to a hosted database.

A branch copies data. A branch of production hands the agent every
customer row, and open egress lets them out. Branch from a dev or staging
parent, or schema-only where the provider allows it. Branching protects
against destruction, not against theft.

This only matters for users whose `DATABASE_URL` points at a hosted
database. Compose users already have it through the snapshot. The study
decides whether it earns a place in the pitch.

## Logging in to websites

The owner's idea was a browser with something like Bitwarden, where the
agent logs in as the user without being able to read the password.
Hiding the password is easy. The session is the problem:

- A filled password sits in the page, where an agent with root can read it.
  1Password's agent autofill, Bitwarden's Agent Access SDK, Steel, Kernel
  and Browser Use all keep it out of the transcript, not away from the
  agent.
- The session cookie that comes back after login is as good as the
  password.
- A logged-in agent can create an API key and read it off the page, add its
  own SSH key or passkey, or change the recovery email.

Proposed, in order of cost:

1. **Document the takeover login (works today).** The user opens
   `repose open --desktop`, types the password once, and the machine's
   browser keeps the login. The docs say the trade plainly: the agent can
   use that session, and it is in snapshots. Never put a TOTP seed in the
   machine.
2. **Document the agent's own identity for new services.** When the agent
   needs a new service, it creates one without the user's account and the
   user claims it later: Neon Claimable Postgres (no account, 72 hours
   unless claimed), Vercel claimable deployments, Clerk keyless dev apps.
   This covers "let the agent sign up for things" without risking anything
   of the user's. The research found vendors pushing this, and no users yet
   asking for it.
3. **Not now: a logged-in browser outside the machine.** Chromium runs under
   hostd, and the agent drives it through navigate, click, type, screenshot
   and read-text, with no script access. It is the only design where the
   agent uses a login without being able to take it. It still needs a
   per-site blocklist of pages that create credentials, it would be a
   fourth secret home, and it is the largest build in the research. Wait
   until users ask for agents in their existing dashboards.

Device Bound Session Credentials would solve this cleanly, but in 2026
they run only in Chrome on Windows with a TPM, and almost no site besides
Google uses them.

## `.env` files: any `.env` is valid

The TypeScript rule applies: every plain `.env` works unchanged, Repose
tightens only where the user opts in, and it never rewrites the laptop's
file.

| Layer | What happens | When |
|---|---|---|
| 0. Plain | Synced exactly as today (I-197) | Today |
| 1. Named | The sync prints names of live-looking keys and remote databases | Build now |
| 2. Upgraded | In secure mode, the machine's copy gets minted children or a branch URL | With secure mode |
| 3. Annotated | Full-line comments above a key override the guess ("leave plain", "local") | Only when there is something to override |

Rules the research settled:

- **Annotations are full-line `#` comments only.** It is the one form that
  Node dotenv, Next.js, python-dotenv, godotenv, phpdotenv and Docker
  Compose all ignore the same way. Inline comments and new `REPOSE_*` keys
  both break something. Reuse varlock's `# @` decorator syntax rather than
  invent a dialect.
- **Detect in tiers.** Act only on a known vendor prefix. Report a
  secret-looking name with an unknown value, and leave it alone. Classify
  URLs by host. Call a remote database "remote", never "production". Never
  test a key against its provider.
- **Leave public keys alone:** `pk_*`, `sb_publishable_*`, Sentry DSNs,
  `NEXT_PUBLIC_*`, `VITE_*`. Clerk decodes its publishable key, so
  changing it breaks the app and hides nothing.
- **Keep the format.** Replace only the value's bytes, inside the original
  quotes. A minted child already has the right shape.
- **Signing secrets can't be protected.** `JWT_SECRET`, `AUTH_SECRET` and
  `NEXTAUTH_SECRET` never leave the app, so there is nothing to mint or
  proxy. A dev value is harmless. A production value is protected only by
  the norm. The docs say so.
- **Pull-back rules wait.** Once the machine's copy differs from the
  laptop's, a future `repose pull` must never write a child back over a
  real value, and rewriting the machine's copy must not make it look
  newer than a real change on the laptop. Not needed until layer 2 ships.

## Detection and cleanup, not blocking

- **Revoke on destroy, three tiers:** delete tmpfs files and named secrets;
  revoke every child Repose minted; show a "rotate these" list with links
  for the plain keys and copied logins Repose can't revoke. Restore
  follows the same steps.
- **Later, if the pitch needs it: a domain log.** A per-guest list of the
  registrable domains the machine contacted (`webhook.site`, not the full
  hostname, because DNS labels can carry stolen data), shown to the tenant
  and not sent to operator logs. Today's `egress_blocked` line records "never
  an address", so this is a new kind of data: it needs a DECISIONS entry
  and a privacy-policy line.
- **A notice on first contact with a short fixed list** of drop and paste
  hosts, not "new since last week", because a dev machine has no stable
  baseline. It catches nothing sent through `api.github.com`; only the
  scoped token does.
- **No canary tokens yet.** The agent itself will find and try a decoy key.
- **Locked mode on request only,** with every blocked host shown and
  allowable in one command.

## What to tell users

Until secure mode ships, the quickstart line
(`apps/web/src/content/docs/index.md:8`, "your other projects are out of
its reach") is not true, because the full `gh` login is copied. The
owner has deferred the doc fixes; when they are made, the line becomes:

> Your laptop and SSH keys are out of its reach. The logins and keys you
> bring are not: the agent can use them for anything they allow, so bring
> test keys and a dev database.

The secrets page's example `repose secrets import .env.production` with a
live-style `STRIPE_SECRET_KEY` contradicts the norm and becomes `.env` with
test keys in the same change.

Once secure mode ships, for users who turn it on:

> Keys the agent gets from us reach only this project and die with the
> machine. For the rest, we tell you what it can do.

## Open questions for the user study

A one-week study (six to eight interviews, a short survey) runs alongside
the Docker `sbx --cloud` head-to-head. The script and thresholds are in
the main report, section 2. The questions that decide this proposal:

| Question | Decides |
|---|---|
| Is GitHub the only credential the agent uses weekly? | Whether the GitHub part is all of secure mode for now |
| Do you have a production deploy token or database URL where the agent runs? | Whether the credential manager is needed beyond GitHub |
| Would you hand Repose an admin key so it can mint per-machine keys? | Whether the credential manager is worth building at all |
| Would you install a GitHub App to get one-repo tokens? | Whether the App is the secure-mode default or the hand-made token is |
| Does your `DATABASE_URL` point at Compose or a hosted database? | Whether branching belongs in the pitch |
| Has your agent created an account or resource, and did you want it to? | How much to say about claim flows |

## Build order

| When | Item | DECISIONS entry |
|---|---|---|
| Done, awaiting merge | Stop copying the Vercel login (I-298) | I-298 |
| Now | Sync names live-looking keys and remote databases; names only | Yes, new CLI output |
| Now | Opt-out for copying `.env` files | Yes, amends I-197 |
| Now | Docs: the `.env` norm, takeover login, claim flows, the hand-made fine-grained GitHub token | No |
| Deferred by the owner | Fix `index.md:8` and the `.env.production` example | No |
| Now | User study and Docker head-to-head | — |
| After a "continue" | GitHub App, per-run one-repo tokens, secure mode stops the `gh` copy | Yes, amends I-150 |
| If the study says users hand over parent keys | Credential manager with the two providers they name, revoke on stop, orphan sweep | Yes |
| If hosted databases are common | Branch per machine, Neon first | Yes |
| If the pitch needs it | Domain log and the fixed alert list | Yes, plus privacy policy |
| On request only | Locked egress mode, placeholder proxy (never `api.anthropic.com`), federation, `.env` annotations, host-side browser | Yes, each |

## Not proposed

- A deny-by-default firewall as the default.
- A placeholder proxy now. It holds real values in hostd memory (a fourth
  secret home), breaks on certificate trust per runtime, and doesn't cover
  database URLs, request signing or signing secrets. Minting covers more
  with no new home.
- Vault-style autofill in the machine's browser. It hides the password from
  the transcript and nothing else.
- Canary tokens, audit-then-lock egress, live key checks against providers.
- A 25-provider minter, OpenBao, or a general policy engine before the
  study.
- Anything that sells notifications, phones or team use
  (`repose-positioning`).

## Names

"Secure mode" is this document's working name. Before anything ships, it
gets one name used everywhere (CLI flag, config key, docs), and no second
name for the same thing.
