# Shareable overnight recap page (proposal, 2026-11-10)

**Status: parked for later. Documented only; nothing here is decided and
nothing is to be built now.** This is future item 12 in
`reports/2026-11-04-product-value-council.md`: an opt-in page for one
task or one night of agent work. It shows the commits, a diff stat, PR
links, the questions the user answered, screenshots the agent chose from
the shared browser, the hours the machine worked with the lid closed,
and a signup link. The council scored it pull 7.3, retention 3.7,
novelty 7.5, size M, with 1 build-next and 3 experiment votes: "Each
share advertises repose. Under the logging rule, it may use only
commits, links and screenshots the agent chose."

## Summary

| Question | Short answer |
|---|---|
| What data exists today? | Events with agent summaries and `--done` links, questions and answers, per-minute samples (sessions, clients, agent states). The api holds no commits, diffs or screenshots. |
| What must be gathered new? | Commits, diff stat and PR links, from the guest's git. Screenshots, from files the agent names. |
| Logging rule | The page is published tenant content that the user chose. No log line, metric or trace may carry any of it. Terminal contents and prompts never go on the page. |
| Private repositories | Commit messages, branch names and file names can leak plans or customers. Each share needs an explicit consent step and a preview of the exact page. |
| Hosting | A page on `repose.herakraft.co` (the dashboard app), content in Postgres, images in Blob, behind an unguessable link the user can revoke. |
| Growth case | Real, and unmeasured. It is the only item that puts repose in front of people who did not search for it. |
| Biggest conflict | The council told preview URLs "no public mode". A recap meant for sharing is a public page. |

## 1. Data that exists

| Recap item | Source today | Notes |
|---|---|---|
| What the agent says it did | `events.summary` of `agent_done` (1 KB), from `repose-notify --done` (I-688) | Written by the agent for the user; tenant content (`docs/interfaces/db-schema.md:113-119`) |
| PR links, "look here" links | `events.link_url`, `link_label` (I-931, migration 0033) | One link per event. Tenant content. |
| Questions answered | `questions.text`, `options`, `answer`, `answered_via`, `answered_at` (I-244, I-690; `db-schema.md:196-208`) | "tenant content stored like events.summary: plain, shown to the owner, never logged". A question that asked for a secret has `secret_name` and no value. |
| Hours with the lid closed | `meter_samples`: per minute, `state`, `ssh_sessions`, `tmux_clients`, `agents` (`db-schema.md:126-136`), 90-day retention | A minute with no SSH session, no client and an agent `working` is a lid-closed working minute. Derivable today with one query. |
| Commits, diff stat | None in the api. `repose status` reads git over SSH from the laptop (`internal/cli/status_procs.go:205-250`), and the api stores nothing of it | Needs a new read in the guest |
| Screenshots | The shared browser (`docs/features/browser.md`) writes screenshots to the guest's disk when an agent asks for one (the playwright MCP saves them under the working directory, in `.playwright-mcp/`) | Nothing leaves the guest today. No notification carries an image (`docs/features/notifications.md:228`: "no images") |
| Machine, size, time span | `projects`, `ops`, events timestamps | Plain facts |

## 2. The logging rule and the privacy policy

CLAUDE.md, "Never log what a tenant typed", and
`docs/ops/OBSERVABILITY.md:285-300` forbid in any log field, metric label,
trace attribute or stored build log: prompts, agent output, terminal
contents, process arguments, environment variables, file paths inside a
guest, secrets, tokens, git remote URLs and user email. The privacy policy
promises the same in plain words (`apps/web/src/content/legal/privacy.md:47-49`
and `:84-88`), and lists what repose does keep: agent summaries,
`repose-notify` and `repose-ask` text and answers, each capped at 1 KB
(`privacy.md:28-34`).

A recap page fits these rules only under three conditions:

1. **It is content the user published, stored as tenant content.** It
   lives in its own table and blob container, like `events.summary`. No
   log line names a commit subject, file name, branch or image name; logs
   carry the recap id, the project id, counts and byte sizes.
2. **It never reads the terminal or a prompt.** The recap is built from
   git, from what the agent wrote for the user (`--done` summaries,
   questions), and from files the agent names. It never scrapes a tmux
   pane and never includes the prompt that started the work. A prompt the
   user wants on the page they type into the preview themselves.
3. **The privacy policy gains a section** for published recaps: what a
   recap holds, that the user chose each item, who can open it, how long
   it is kept, and how to delete it. The list of data repose keeps today
   names commit messages and file names nowhere, so publishing them
   without that change breaks a promise.

Screenshots carry a risk the other items do not: a screenshot of a
staging dashboard can show a customer's name, a token in a URL bar, or a
secret in a form. The agent chooses them, so the preview must show each
image at full size, and the user removes any of them before publishing.

## 3. Private repositories

Most repose projects are private. On a private repository these items
can be sensitive:

- commit subjects ("Fix billing for Acme's SSO contract"),
- branch names (`acme-migration`),
- file names and paths in the diff stat (`src/customers/acme/...`),
- PR titles and URLs (the URL names the organization and repository),
- question texts and answers,
- screenshots.

Options, from least to most work:

1. **Counts only by default.** The page shows numbers (12 commits, +840
   -210 in 31 files, 2 PRs, 3 questions answered, 6.5 hours with the lid
   closed) and the agent's own `--done` summary. Names, subjects and
   links are off until the user turns each on in the preview.
2. **Per-item consent.** The preview lists every commit subject, file
   path, link and image with a checkbox, all unchecked on a private
   repository and all checked on a public one. GitHub's API, through the
   copied `gh` login, can tell public from private.
3. **A redaction pass.** The user can edit any line before publishing.

Each share is its own consent: no account-wide "always publish", and no
automatic recap after each night. A published page is a snapshot; it
does not update when more commits arrive.

## 4. How the data is gathered

- **Git.** A guest helper (named here only to describe it, for example
  `repose-recap`) runs `git log` and `git diff --stat` over a range: since
  the user's last attach, since a given commit, or for one `--worktree`
  branch. It writes a JSON draft on the machine. The CLI (`repose recap`
  or a flag on an existing command, see question 6) reads it over SSH, as
  `status` reads git today, and sends it to the api only after the
  preview.
- **Screenshots.** The agent names the files it chose (for example
  `repose-notify --done "..." --image shot.png`, or a line in the draft).
  The CLI uploads them with the draft. A cap on count and size (for
  example 6 images, 2 MB each) keeps Blob and egress small.
- **Events, questions and hours.** The api adds these from its own tables
  for the project and the time range at publish time.

Gathering from the laptop CLI keeps the api's role small: it stores what
the user approved and serves it. A guest-to-api upload path through
guestd and hostd would let an agent publish without the laptop, which is
the wrong default for a page that needs consent.

## 5. Where it is hosted

- **URL.** `https://repose.herakraft.co/r/<id>`, served by the dashboard
  app (SvelteKit on the Coolify VM, `docs/ARCHITECTURE.md:18`). The id is
  a random 128-bit token, so the page is unlisted.
- **Storage.** The recap's text in Postgres; images in Blob, in a
  container apart from snapshots, served through the app or a signed URL.
- **Access.** Anyone with the link can read it. The owner can revoke it
  from the dashboard or the CLI, and it expires after a set time (for
  example 90 days) unless the owner keeps it. `noindex` by default, and
  an Open Graph image built from the counts so a share on X or Slack
  shows a card.
- **The council's conflict.** The council said preview URLs get "no
  public mode" and that public paths start with a signed webhook later.
  A recap differs from a preview: it is a static page of approved text and
  images, with no route into a guest. It still makes repose the host of
  public content that tenants write, so it needs the abuse rules a public
  page needs (a report link, takedown by the operator, a size cap).

## 6. The growth case

- **For.** The page is the proof the landing cannot show: a real night of
  work by an agent on a real VM, with the hours the laptop was closed.
  Each share reaches people who never searched for repose. The signup
  link ("Made with repose") can carry the sharer's id, which also gives
  repose its first measure of word of mouth. The owner's goal is
  recognition and reach (`reports/2026-11-04-product-value-council.md`,
  "What repose should be known for"). Its pull score, 7.3, is the
  second highest of the future items, after the taste tier (8.2).
- **Against.** Retention scored 3.7: users share once or twice, then
  stop. Three of four members voted "experiment". The value depends on
  the share rate, which nobody has measured. A minimal test needs no new
  infrastructure: the owner writes three recaps by hand from his own
  nights and posts them, and the click-through decides whether to build.

## 7. Open questions

1. Is a public, unlisted page acceptable, given the council's "no public
   mode" for previews? If not, the recap is owner-only and loses the
   growth case.
2. Default content on a private repository: counts only, or per-item
   consent with everything unchecked?
3. One recap per task (a `--done` and the commits before it) or per night
   (a time range)? Per task is easier to name; per night matches the
   "lid closed" story.
4. May an agent draft a recap, with the user publishing it, or only the
   user? Publishing always needs the user.
5. Screenshots: allowed at all in the first version, and with what caps?
6. CLI placement under the owner's rule (fold into an existing noun):
   a flag on `repose events`, a subcommand of an existing noun, or a
   dashboard action only.
7. Retention and deletion: default expiry, and whether a destroyed
   project's recaps go with it.
8. Privacy policy and terms wording, and the abuse path for public
   tenant content. These need a DECISIONS entry that cites I-688, I-931,
   I-244 and the observability rules.
9. The signup link: a plain link, or one that credits the sharer (no
   referral programme exists today).
10. Run the hand-made test first?

## Sources

- `reports/2026-11-04-product-value-council.md`, future item 12.
- Repo: CLAUDE.md "Never log what a tenant typed";
  `docs/ops/OBSERVABILITY.md`; `apps/web/src/content/legal/privacy.md`;
  `docs/interfaces/db-schema.md`; DECISIONS I-244, I-688, I-690, I-931.
