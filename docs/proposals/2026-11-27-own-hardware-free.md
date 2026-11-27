# Own hardware is free for up to two hosts; the third host is the paid line

Ticket, high priority, next up (owner, 2026-11-27). Not built.

Owner's ruling, 2026-11-27:
- Bringing your own hardware costs $0. The $10 a month Own hardware plan goes away.
- Free covers up to two hosts of your own. Adding a third host is where payment starts.
- No card at sign-up for the free tier. Nobody is on Own hardware today, so no refunds or migration are needed.
- Hosted plans (Solo, Plus, Pro) stay as they are and keep including hosts of your own (I-1704). Running on repose's servers is the other paid path.
- No per-person pricing yet: accounts have no teammates, and teams are not planned for the next month unless users ask.

Why: let people get the product's value before asking for money, and charge when they hit a natural limit (the Linear model). A free account costs repose little: its compute, disk and egress run on the user's own hardware and connection.

Open questions for the builder to bring back:
- What the third host costs: the old $10, a hosted plan, or a price per extra host.
- What happens at the limit: `repose host add` and the dashboard's Add a host refuse the third with a sentence and a link to upgrade.
- A fair-use ceiling on edge relay traffic (SSH, attach, browser view, port forwards) for free accounts, as an abuse guard, not a paywall.
- Sign-up without a card: what the account is before any checkout, and what the dashboard shows.

Done when:
- A decision entry superseding the Own hardware plan's $10.
- api: a free account adds and uses up to two hosts with no subscription; a third is refused with a plain sentence; tests for both.
- Polar: the Own hardware product removed or archived; the checkout no longer offers it.
- CLI and dashboard: Billing and Add a host say free up to two hosts, and the refusal on the third.
- Public docs: billing.md (Own hardware section, the free week), own-hardware.md, limits.md; the landing's pricing band.
- Live check on repose.run: a new account with no card adds a host and runs a machine on it.
