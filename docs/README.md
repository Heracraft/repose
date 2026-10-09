# repose docs

repose is a service where a developer runs `repose run` in a project directory
and gets a persistent remote environment where coding agents keep working after
the laptop closes. Multi-tenant from the first release, billed from the first
hour, hosted at `repose.herakraft.co` until it graduates to its own domain.

These docs are the source of truth. When code and docs disagree, the doc is
wrong only if a `DECISIONS.md` entry says so.

## Map

| Doc | Read it when |
|---|---|
| [DESIGN.md](DESIGN.md) | You need the whole system in one place. Everything below is a slice of it. |
| [ARCHITECTURE.md](ARCHITECTURE.md) | You need the component map, data flows, network diagram, and repo layout. |
| [DECISIONS-INDEX.md](DECISIONS-INDEX.md) | Before DECISIONS.md: read DECISIONS-INDEX.md first; open DECISIONS.md at the entries you need. One generated line per entry (id, title, date, superseded/amended status, line number); `ops/dev/decisions-index.py` regenerates it and `--check` says whether it is stale. |
| [DECISIONS.md](DECISIONS.md) | You want to know *why* something is the way it is, or want to change it. Every settled decision, with the alternatives that lost. |
| [MILESTONES.md](MILESTONES.md) | You want to know what to build next and what "done" means for each stage. |
| [CHECKLIST.md](CHECKLIST.md) | You are about to call something finished. The global definition of done. |
| [LANDING.md](LANDING.md) | You touch the landing page: what it sells, show-don't-tell, real captures, names a stranger understands, where terminals are allowed. |
| [GLOSSARY.md](GLOSSARY.md) | A word is used in a specific way (guest, host, edge, fragment, closure, project). |
| [RESEARCH.md](RESEARCH.md) | You want the facts and citations the decisions rest on (Azure nested virt, pricing, nixpkgs coverage, Coolify limits, Logto flows, what agents lose remotely). |
| [SECURITY.md](SECURITY.md) | You touch anything that crosses a tenant, host, or network boundary. Threat model and the non-negotiables. |
| [security/](security/review-2026-09-20.md) | Dated security reviews: findings with severity and file:line, what was verified, what waits on a stub. |
| [proposals/](proposals/2026-09-23-dev-ergonomics.md) | You want the reasoning behind a batch of decisions made in conversation with the owner, including the options that lost and what was deferred. Not spec: the decisions are in DECISIONS.md. |
| [workstreams/STATUS.md](workstreams/STATUS.md) | You start or stop a session: the newest line per workstream and the last fifteen; older lines are in [workstreams/status-archive/](workstreams/status-archive/2026-09.md). [workstreams/CHECKLIST-AUDIT.md](workstreams/CHECKLIST-AUDIT.md) lists which §9 checklist rows are still open and what closes them. |
| [workstreams/](workstreams/README.md) | You are an agent picking up a chunk of work. Each workstream is self-contained: scope, non-goals, interfaces it owns and consumes, and a checklist. |
| [interfaces/](interfaces/README.md) | Two workstreams meet here. gRPC between API and hostd, vsock between hostd and guestd, the HTTP API, the database schema, the SSH gateway login contract, the CLI config file. |
| [features/](features/README.md) | User-facing behaviour, one feature per file, written as the behaviour a user sees and the edge cases that must hold. |
| [ops/ORCHESTRATION.md](ops/ORCHESTRATION.md) | You are running the waves: conductor and worker roles, the wave cycle, merge rules learned by doing, how applies and stalls are handled. |
| [ops/RELEASE.md](ops/RELEASE.md) | Your branch is done and you want it on main, or you are the conductor cutting a release: the release queue, what a branch ships as, the verify-ship-check-record steps. |
| [ops/LAUNCH.md](ops/LAUNCH.md) | You are the owner and the round is merged: the Polar organization, the Resend key, the seat count, the host, the deploy order and the tweet. |
| [ops/RUNBOOK.md](ops/RUNBOOK.md) | Something is broken in production and you need the symptom-to-fix list. |
| [ops/coolify.md](ops/coolify.md) | You are setting up, backing up, restoring or upgrading the control plane. The click path OpenTofu cannot own, because Coolify keeps it in its own database. |
| [ops/OBSERVABILITY.md](ops/OBSERVABILITY.md) | You are adding a log line, a metric, or a signal that the idle and pricing policies will later depend on. |
| [PRICING.md](PRICING.md) | Tiers, meters, the cost floor per guest, and the trial. What a user sees is [features/pricing.md](features/pricing.md). |
| [DESIGN-LANGUAGE.md](DESIGN-LANGUAGE.md) | You are building any screen. The foundation every page shares (tokens, contrast floor, type, motion, logo, header), then the dashboard's patterns (states, confirmation, fields, toasts). The landing adds [LANDING.md](LANDING.md). |
| [ops/DEV-BOX.md](ops/DEV-BOX.md) | You are on the dev VM and something about disks, Nix or az is odd. |
| [ops/AZURE-SETUP.md](ops/AZURE-SETUP.md) | The one-time human steps in Azure, Cloudflare, Logto, Polar and Resend before agents start. |
| [workstreams/PROMPTS.md](workstreams/PROMPTS.md) | The prompt for launching an agent on a workstream with `/ws` (every worker runs on the current Opus model). |

## How parallel work is organised

The design is split into workstreams that can be built concurrently. Each
workstream document names the interfaces it *owns* (it may change them, and must
update `interfaces/`) and the ones it *consumes* (it codes against the contract
as written and raises a change request in `DECISIONS.md` if the contract is
wrong). The dependency graph and claiming rules are in
[workstreams/README.md](workstreams/README.md).

An agent working a workstream:

1. Reads `DESIGN.md` once, then its workstream doc, then every `interfaces/` doc
   it owns or consumes.
2. Builds the whole workstream, not the parts that are easy. The checklist at the
   end of each workstream doc is the definition of done for that workstream, and
   `CHECKLIST.md` is the definition of done for everything.
3. Records any decision it had to make that the docs did not cover in
   `DECISIONS.md` under "Made during implementation", with the alternatives.
4. Never marks a checklist item done because a build passed. Each item says what
   evidence closes it.

## Conventions

- Prose over bullets where reasoning matters; bullets for parallel items.
- A doc states the failure that a rule prevents. A rule without its failure story
  gets argued with and then deleted.
- Names are fixed: the product, CLI binary, SSH login prefix, config directory and
  Go module are all `repose`. Do not introduce synonyms.
- Dates are absolute (2026-09-17), never "last week".
