# Someone else pays (proposal, 2026-10-11)

**Status: analysis only. Nothing here is decided.** Direction 3 of the
pricing directions: money for repose's hosts comes from someone other
than the individual developer. That someone can be a cloud's credit
program, a model lab, a grant fund, a sponsor, a donor, or a company
paying for its team or bringing its own host. Any of it becomes real
only through `DECISIONS.md` entries that supersede the ones named below.

Today the Azure credit pays (owner, 2026-09-27: "use Azure as a cash
sink for now", `docs/PRICING.md`). A seat costs about $100 a month on
an Azure D64s_v7 and $33.07 on a Hetzner AX162-2 at EUR 842.30
($992.10 / 30), both compute only, before storage, the control plane
and the edge (I-642, on branch `hetzner-repricing`, not yet on main;
`hetzner-repricing:docs/PRICING.md`, "The cost floor"). The fleet runs
on Azure today and nothing schedules a move to Hetzner. After Polar's
5% + 50c, a seat earns $27.05 on Solo ($18.50 in the intro months),
$27.78 on Plus and $23.39 on Pro. The owner has little cash ("I
actually don't have enough money", 2026-09-27, `PRICING.md`) and wants
public recognition that leads to a hire, an acqui-hire or a cloud
adopting the product. The repo records no owner country and no
registered business; the owner has said no entity exists, but no dated
quote of that is in the repo (*unverified*).

Every outside figure was checked on 2026-10-11 unless another date is
given, and each source page's own date sits next to its figure where
the page has one. Anything marked *unverified* could not be confirmed
on a primary page. Re-check every amount before it goes in an
application.

## Summary

| Question | Answer |
|---|---|
| Biggest single amount reachable | A named compute sponsorship: one host is 30 seats, worth about $12k a year on Hetzner or $36k on Azure. Long odds, months away |
| Biggest amount on a known date | NLnet Restack or CodeSupply, EUR 5,000 to 50,000 for labour, next deadline 2026-11-03 12:00 CET. Individuals may apply. The ask fits the guest image builder and the BYO-host agent as general KVM tools; the hosted service targets coding agents, and the owner judges whether Restack's AI exclusion covers it |
| Soonest money for hosts | More Microsoft for Startups credit. The owner's portal offered up to $25,000 (`solo-at-20.md`), which conflicts with having no entity |
| Cheapest way to cut the per-seat loss | A company brings its own host under shape A (I-647): repose's host cost for those users is zero |
| Best for the recognition goal | Per the ranking in section 10: a compute sponsorship, then an NLnet grant page, then Claude Startups (office hours with Anthropic's Applied AI team, no stated hiring path) |
| Licence | `heracraft/repose` is public with no LICENSE file. A licence unlocks NLnet now and four smaller or closed programs later. It cannot be withdrawn from versions already published |
| Does a company paying per seat help? | It adds revenue at the same per-seat loss. About $35 a seat clears a $33.07 Hetzner seat, compute only; about $106 clears a $100 Azure seat, where the fleet runs today. No company has asked for one bill |
| Is an entity worth forming now? | Only once a deal or the Microsoft investor path needs a signature. The owner's country decides the form; a US LLC carries a $25,000 Form 5472 penalty for a foreign owner who misses the filing |
| Decide first | The owner's country and business status, then the licence, then whether to pitch a sponsor |

---

## 1. The licence unlocks NLnet now and a few programs later

`gh repo view heracraft/repose` reports a public repository with
`licenseInfo: null` and 0 stars. The tree has no LICENSE file and
`DECISIONS.md` has no licence entry. Without a licence the code is
"all rights reserved" even though anyone can read it.

Five programs below require an open licence: NLnet, Sovereign Tech
Fund, Open Source Collective, the Vercel Open Source Program and the
GitHub Secure Open Source Fund. In practice a licence unlocks NLnet
before 2026-11-03. STF does not fund user-facing applications, Vercel
OSS is closed, the GitHub fund's last round closed, and Open Source
Collective needs an organization repository. Claude for Open Source
also needs reach (500+ dependents, 100+ merged PRs and similar) that
no licence gives. Adding a licence takes one commit. Removing it later
does not reach copies already published under it.

| Licence | Effect on repose |
|---|---|
| AGPL-3.0 | A cloud that hosts a modified repose must publish its changes, which the owner can merge back. Some large companies ban AGPL code: Google's policy reads "Code licensed under the GNU Affero General Public License (AGPL) MUST NOT be used at Google". That makes an acqui-hire or cloud adoption harder. The policy at other companies on the acquirer list is *unverified* |
| Apache-2.0 | A cloud can host repose without the owner. That is one of the outcomes the owner listed as good ("a cloud adopting the product"), and it is the easiest licence for a company lawyer to approve |

The BYO host proposal (branch `byo-host-shape-a`, "Two shapes") already
calls fully self-hosted shape B "what earns GitHub stars". A licence
plus shape A (I-647, on branch `byo-host-shape-a`, not yet on main)
gives a grant fund a self-hostable infrastructure piece to pay for.

## 2. Cloud credit programs

The owner's profile as the repo records it: an individual, no entity
on record, selling to individual developers. Each program below says
whether that profile qualifies.

### Microsoft for Startups (Azure)

| Item | Detail |
|---|---|
| Start | Two paths (application page, ms.date 2026-10-08). Without an investor code: $200 after personal LinkedIn verification. With an investor code: $1,000 at Azure sign-up. Business verification of a registered legal entity brings the total to $5,000 (getting-started page) |
| Tiers (getting-started page, ms.date 2026-05-27, updated 2026-08-05) | $25,000 at 5 workloads for about 60 days; $50,000 at 7; $150,000 at 10 workloads and about $3,000 a month of use. Each workload needs at least $1 of spend on all 60 days |
| Most unlockable | $150,000 (overview and getting-started pages). Eligibility bar: under $350,000 of lifetime free Azure credit received (overview, updated 2026-08-05) |
| Expiry | Activate within 90 days. The two pages disagree: the getting-started page says the $25,000 milestone gives "a one-time extension of two years", and the FAQ (updated 2026-08-05) says credits are "valid for up to two years. Extensions are not available". The extension is *unverified*. No transfer between accounts. Converts to pay-as-you-go when spent or expired |
| Buys | "Eligible Azure services". Reservations cannot be bought with credits on most sponsorship SKUs (`docs/workstreams/11-infra-opentofu.md`). The excluded list for offer 0036P is not public, so whether D64s_v7 counts is *unverified* |
| Eligibility | Pre-seed to Series B, privately held, "B2B startups building software, AI, or technology solutions", a personal Microsoft account "with no prior Azure account" (application page) |
| Owner's profile | Qualifies for the $200 path. The $5,000 step and above need a registered legal entity. repose sells to individuals, so the B2B line does not describe it; reviewer enforcement of that line is *unverified* |

**A contradiction the owner has to settle.** `docs/proposals/2026-10-04-solo-at-20.md`
records the owner's portal showing "maximum credit offer of $25,000"
and concludes that business verification, which needs a registered
entity, is "probably complete". The owner has said no entity exists
(*unverified*, see the introduction). Both cannot be true. If
verification passed, every tier up to $150,000 is a matter of workloads
and time; if it did not, the ceiling today is $200, and $5,000 waits on
an entity.

**Climbing the tiers.** `infra/azure` bills six services today, and Key
Vault probably spends under the $1 a day a workload needs. The 60 days
a tier takes are the existing host spend at about $3,000 a month, so
the climb costs no extra credit beyond the added services. Three
settled entries limit which services can be added. R3-4 keeps the
control plane portable (`solo-at-20.md`); R4-2 + R5-11 rejected Azure
Database for PostgreSQL as "an Azure dependency in the state store";
R4-14 sends Postgres backups to Cloudflare R2. Postgres Flexible and
Azure Backup are therefore out. Services that count without becoming
dependencies: Azure Monitor, Azure Container Registry as a pull-through
mirror, and ACS Email as a second sender. That reaches 7 workloads at
best, enough for $50,000. Ten needs services that pull the control
plane onto Azure. At $3,000 a month, $25,000 lasts about 8 months and
$50,000 about 17.

**Azure OSS credits** are closed: "Microsoft is not accepting new
applications from projects directly". The closing date is *unverified*.

### AWS Activate

| Item | Detail |
|---|---|
| Founders | $1,000, up to $5,000, self-funded, by direct application |
| Portfolio | Up to $200,000, needs an Activate Provider's Organization ID |
| Requirements | Pre-Series B, founded in the last 10 years, a paid-tier AWS account. Secondary sources add a company website and a business-domain email. *Unverified* on an AWS page |
| Excludes | Marketplace, upfront Reserved Instance and Savings Plan fees, Enterprise support, Professional Services, Route 53 domains, mining |
| Expiry | Set on the code; 1 to 2 years (`docs/RESEARCH.md` §3) |
| Owner's profile | Founders without an entity is *unverified* |

The owner applied for $10,000 (`solo-at-20.md`). Founders tops out at
$5,000, so $10,000 needs a Portfolio code or another offer, and the
repo does not say which. Nested virtualization runs on C8i, M8i, R8i,
C7i, R7i, M7i and other Intel families (AWS EC2 nested virtualization
docs), so AWS can meet DESIGN §4. Using it needs an S3 `Blob` in
`internal/hostd/snapshot/blob.go`, an OpenTofu AWS host module, a
WireGuard peer, and amendments to R2-3 and R3-20. At about $1,545 a
month for an `r7i.8xlarge` (about $2.1168 an hour on demand in
us-east-1, *unverified*), $5,000 runs 30 seats for about 3.2 months and
$10,000 for about 6.5.

### Google for Startups Cloud

The Start tier gives $2,000 to startups with no institutional funding,
founded in the last five years. repose has no GCP code, and whether
Start needs an entity is *unverified* (the apply page needs a login).
The Scale tier needs equity funding.

## 3. Anthropic

| Program | Gives (2026-10-11) | Eligibility | Fit |
|---|---|---|---|
| Claude Startups | The program page (no date) says "we're currently over capacity on the Claude Team and $1,000 API credit offers". It still lists partner deals "worth up to $45,000", Applied AI office hours ("45-minute live sessions for members, held every other week") and events. Up to $100K of API credit comes only through a partner VC. The expansion article (2026-10-06) lists the Team seats and credit; the program page is newer on capacity and wins | "Founded in the last five years or funded in the last two"; bootstrapped accepted. Eligibility of an individual without an entity is *unverified* | Pays for no host. Gives office hours with the Applied AI team and a chance to ask about a Claude Marketplace listing. No hiring path is stated |
| Claude for Open Source | 6 months of Claude Max 20x | 500+ dependent repos, 200,000+ monthly downloads, 100+ merged PRs in others' repos in 12 months, 20+ external contributors, or OpenSSF criticality 0.4+. "Don't quite fit? ... apply anyway" | repose does not qualify. The owner might on personal contributions, *unverified* |

**Anthropic Fellows** pays the owner, never a host: 4 months, $3,850 a
week stipend and about $15k a month of compute. The greenhouse board
lists three tracks: "ML Systems & Reinforcement Learning", "The
Anthropic Institute (Economics & Policy)" and "AI Safety & Security".
An infrastructure engineer fits ML Systems best. The last confirmed
cohort started 2026-07-20 and closed applications on 2026-04-26
(opportunitydesk.org, secondary); the next deadline is *unverified*.
It is a hire-track channel and sits outside the ranking.

## 4. Vercel, Hetzner and compute sponsorship

**Vercel.** The Open Source Program gives "$3,600 in platform credits"
over 3 years. It needs an OSS project "hosted on or planned for Vercel"
with a Code of Conduct. Applications are "Currently closed"; cohorts run
four times a year. The Spring 2026 cohort post names 34 projects and
the program page shows 29. The credits could host only the landing and
docs site, so their cash value toward hosts is near zero. Vercel for
Startups offers up to $30,000, and a secondary source says it needs a
Startup Partner affiliation, *unverified*. A cohort listing is a public
mention from a company on the owner's acquirer list.

**Hetzner.** No sponsorship page exists (`hetzner.com/sponsoring`
returns 404). Past sponsorships (JOSM in 2018, Gentoo per search
results) came by direct contact and went to community projects. Hetzner
is not on the Nix Community sponsor list. An ask for one AX162 is worth
$992.10 a month. Hetzner's willingness to sponsor a paid service is
*unverified*.

**A "seats run on X" deal.** The sponsor provides N hosts; repose names
the sponsor on the landing and dashboard and writes a case study.

| Target | Ask | Why them |
|---|---|---|
| Microsoft Azure | More credit, or hosts, for a public "runs on Azure" case | Already the host; on the owner's acquirer list |
| Hetzner | One AX162 host, about $12k a year | Cheapest seat (I-642) |
| DigitalOcean | A host or credits | Precedent: $3M over 3 years in funding, tokens and compute to Omarchy, with DigitalOcean as Founding Corporate Patron of the Omacom Foundation and its "agentic cloud" (cloudnews.tech, linuxiac.com). The date is *unverified* until a DigitalOcean or Omacom page gives it. Its general OSS credits ended (reported 2026-10-05) |
| Anthropic | Compute or credits for a Claude Code-centric product | The users are Claude Code users |

This ranks first for recognition, since the sponsor's name is the
endorsement. It is also the least certain: no program exists to apply
to, a contract likely needs an entity, and one sponsored host is a
single point of failure for 30 seats.

## 5. A company brings its host

Shape A (I-647; `byo-host-shape-a:docs/proposals/2026-10-10-bring-your-own-host.md`)
lets a user attach their own x86_64 KVM box to the hosted control
plane. The box joins the WireGuard hub and runs that user's projects;
`repose run`, `attach` and the dashboard work unchanged. repose pays
nothing for that host. Its remaining costs are the edge relaying SSH
traffic (unmeasured) and a share of the already-paid control plane. The
proposal suggests either no charge or a fee per host; at $5 a month,
Polar keeps 75 cents.

A company that wants repose for 30 people can compare two bills:

| Option | Company pays | repose nets a month | repose's host cost |
|---|---|---|---|
| 30 seats on a per-seat plan at $35 (section 6) | $1,050 a month to repose | $997.00 | $992.10 on Hetzner, about $3,000 on Azure |
| Its own AX162-class box under shape A, $5 fee | EUR 842.30 to Hetzner, $5 to repose | $4.25 | $0 |

Shape A clears every floor in section 6, and the company keeps its
machines on hardware it controls. The gap: shape A gives a host to one
user's projects. A host shared by a team's accounts needs host
ownership at the account-group level, which the BYO proposal does not
design (*unverified* how much work). A company with one heavy user
fits today's design.

## 6. Grants and donations

| Source | Amount | Entity | Notes |
|---|---|---|---|
| NLnet Restack | EUR 5,000 to 50,000, can scale; EUR 7M to 2030 | No; "Individual" is an applicant type | Covers "core internet infrastructure ... reproducibility and trust enhancing technologies". Excludes AI projects under 1M active users. Pays labour per milestone; hosting is outside its scope. Funded by the European Commission through Horizon Europe. NLnet asks applicants "not from a Horizon Europe-eligible country" to "clarify how your project might satisfy the 'European dimension' requirement" (nlnet.nl/propose). "We are not interested in AI-generated projects or proposals" |
| NLnet CodeSupply | Same range; EUR 400k total | No | Supply-chain security and software metadata |
| GitHub Secure OSS Fund | $10,000 per project, paid by milestone via GitHub Sponsors | No | Needs "a valid open source license". The last round took applications on a rolling basis until a set close date; a new round is *unverified* |
| GitHub Sponsors | Tens of dollars a month at 0 stars (estimate) | No; personal accounts pay 0% | Matching Fund closed |
| Polar | 5% + 50c, plus 1.5% on non-US cards; payouts $2 a month plus 0.25% + $0.25 | No; the owner's account verified without one (I-604) | The donations docs page returns 404, *unverified* whether donations survive. A product sold as a subscription works either way |
| Open Source Collective | 10% fee | No; it holds funds instead | Needs an open licence and an organization repository. *Unverified* whether it takes a project that sells a hosted service |

Closed or misfit, with links under Sources: NGI Zero Commons (final
call closed 2026-06-01), Equinix Metal (sunset 2026-06-30), Sovereign
Tech Fund ("does not fund prototypes or user-facing applications"),
Anthropic's AI for Science (research institutions only) and Google's
Scale tier (equity-funded startups).

**NLnet has a date and a scope that stands without the agent product.**
Two pieces work for anyone running KVM fleets: the NixOS microVM guest
image builder and the BYO-host agent (I-647) as a general tool for
attaching KVM boxes to a control plane. An ask for EUR 30,000 to 50,000
to release both openly can say plainly that the hosted service built on
them targets coding agents. A reviewer who opens the landing sees that
anyway, and the owner's name is on the application. The owner decides
whether Restack's AI exclusion covers that, or whether CodeSupply fits
better. NLnet bans AI-generated proposals, so the owner writes it in
their own words. Most of this repository was written by agents; the effect of
that on the deliverable code's eligibility is *unverified*.
Decision timelines are *unverified*, and the European dimension depends
on the owner's country.

## 7. A company pays: the minimum per-seat plan

Here "seat" keeps its I-290 meaning, 8 GB running at once. Polar's
seat-based pricing calls each person who claims a benefit a seat; this
section calls that person a **member**.

Teams are deferred (R5-6), and I-362 rejected "Team" as a plan name for
that reason. Plans that already hold several seats set the bar a
company compares against:

| Plan | Price per 8 GB seat | Net per seat after Polar |
|---|---|---|
| Solo, $29 for 1 | $29.00 | $27.05 |
| Plus, $59 for 2 | $29.50 | $27.78 |
| Pro, $99 for 4 | $24.75 | $23.39 |
| Per-seat company plan, 3 seats at $29 | $29.00 | $27.38 |
| Per-seat company plan, 3 seats at $35 | $35.00 | $33.08 |
| Per-seat company plan, 3 seats at $39 | $39.00 | $36.88 |

A company can already buy N Solo subscriptions at $29 each, or Pro at
$24.75 a seat for one person. A company plan adds one thing: a single
bill for several people, with an invoice from Polar as merchant of
record. A VAT ID on that invoice is
*unverified*. No company has asked for it, so demand is unproven until
a named company does.

The smallest shape that could ship:

- A Polar product with a fixed price per 8 GB seat (`SeatGB` in
  `internal/billing/plans.go`), 2-seat minimum, monthly, one bill.
- The buyer is a billing manager on an ordinary repose account. Polar
  sends `seat.claimed` with an email; repose maps the member to a user
  who gets one seat's 8 GB as their limits. Polar's "Billing manager
  does not receive product benefits", so a manager who wants to run
  machines assigns a seat to themselves as a member.
- Projects stay per user. `projects.user_id` and every path keyed on it
  (certs, gateway, sync) stay untouched.

Polar's guide allows fixed, graduated and volume pricing, up to 1,000
members per subscription. Counts rise freely and drop only after
revocation. The first purchase turns the customer into `type: "team"`
for good. Polar's handbook calls the feature beta behind Settings,
General, Features; the guide does not.

**Code.** A `team_members` table (subscription, member, claimed_at,
revoked_at); `seat.*` handling in `internal/billing/webhook.go`;
`LimitsFor` in `gate.go` reading a claimed seat; `polar-bootstrap`
creating the product; `update_seats` turned on in `polar.go` (I-604
set it false); a dashboard page to invite and revoke; the `/billing`
contract in `docs/interfaces/api.md`; `apps/web/src/content/docs/billing.md`;
and the terms line "One person, one account". The held-seat SQL in
`internal/api/waitlist/waitlist.go` may need no change if the row
stores the bought quantity. About one workstream.

On a 3-seat bill at 5% + 50c, the net per seat is 0.95 x price minus
$0.167.

| Price per seat | Net, Polar 5% + 50c | Net, Polar Startup Program (Scale plan, 3.4% + 30c, free for 12 months) | Net, 5% + 50c on a non-US card (+1.5%) | Against a $33.07 Hetzner seat | Against a $100 Azure seat |
|---|---|---|---|---|---|
| $29 | $27.38 | $27.91 | $26.95 | -$5.69 | -$72.62 |
| $35 | $33.08 | $33.71 | $32.56 | +$0.01 | -$66.92 |
| $39 | $36.88 | $37.57 | $36.30 | +$3.81 | -$63.12 |

Break-even is about $35 against Hetzner and about $106 against Azure.
Both are compute only, before storage, the control plane and the edge
(`hetzner-repricing:docs/PRICING.md`, "The cost floor"), so neither
price is shown to clear the full cost. Outside the Startup Program the
Scale plan costs $400 a month (polar.sh pricing), far more than its
lower rate saves on a small bill. Startup Program eligibility without
an entity is *unverified*. A company logo on the landing helps the
recognition goal more than the seat revenue does.

## 8. Sponsor-a-seat

A Polar product "Sponsor a seat" at $29 a month, or $87 once for 3
months, funds one 8 GB seat for a nominee: an OSS maintainer, a student,
or the next person on the waitlist. Monthly it nets $27.05; the one-off
payment spreads a single 50c fee over three months and nets $27.38 a
month. Against a $100 Azure seat that covers about 27%, leaving a loss
of about $73 a month per sponsored seat; against a $33.07 Hetzner seat
it covers 82%. Sponsored seats count against the same 30 seats a host
holds and add the same per-seat loss. The money is small. The story
("N maintainers run on seats paid for by M sponsors") is public.

**Idle risk.** Guests run until stopped, and an idle machine is
announced, never stopped (R1-5, I-262, I-617). In the last 14 days of
prod `meter_samples`, account 2 ran 299 hours with zero use. A free
seat given to a nominee can sit like that while a paying user waits on
the waitlist. Since R1-5 rules out stopping idle machines, any use
condition has to act on the grant itself: for example, a grant renews
each month only if the account showed use (attached, or an agent
working) in the prior month, and otherwise ends at `ends_at`.

**The code has a hole.** Billing-exempt accounts (I-16,
`repose-admin users exempt`) are today the only way to give someone
free compute. `gate.go` does not gate them on memory or disk at all:
`Check` returns early for exempt (line 89), and `LimitsFor` (line 258)
puts Pro's 32 GB, 500 GB disk and xl in the `/me` limits block only for
display ("An exempt account is not gated on memory or disk; its limits
block says what the biggest plan would allow"). The held-seat SQL in
`waitlist.go` sums live `subscriptions.seats` and unexpired invitations
and leaves exempt accounts out. One exempt account can therefore hold
any amount of memory, past its displayed 32 GB, with no seat counted.
That breaks I-290's promise that memory is never oversubscribed, by an
unbounded amount. Exempt also has no expiry, no sponsor and no size.

Before any sponsored seat ships, add a `grants` table (seats,
starts_at, ends_at, sponsor) counted in the held-seat SQL, and have
`LimitsFor` give a granted account a real memory limit of its granted
seats. A grant ends the way a cancelled plan does (`PRICING.md`,
"Cancelling and changing plans"): machines run until `ends_at`, stop
then, and keep the 30-day retention from that day. The nominee gets a
new `grant_ending` email 7 days before `ends_at`. Exempt accounts
bypassing the gate is a separate decision. Discount codes are off since
I-604, so a coupon-style gift needs them back on in the checkout
session.

## 9. Forming an entity

The owner's country is not recorded in the repo, so pick a form once it
is known. Two facts bear on the choice now:

- A foreign-owned single-member US LLC files Form 5472 with a pro forma
  1120 every year, revenue or not. The penalty for a missed or late
  form is $25,000 (IRS Form 5472 instructions, IRC 6038A(d)). That
  makes a Delaware LLC a bad default unless the owner is a US person or
  pays an accountant. Delaware's LLC tax is $400 a year, due June 1
  (corp.delaware.gov/alt-entitytaxinstructions).
- A UK private limited company costs GBP 100 to incorporate online and
  GBP 50 per confirmation statement from 2026-02-01 (GOV.UK). A local
  sole-proprietor registration is often near zero, *unverified* per
  country, and may satisfy "registered business" checks.

An entity unlocks Microsoft's $5,000 business-verification step and the
investor-code path, possibly AWS Founders, and a signature on a
sponsorship contract. Polar sales, NLnet and GitHub Sponsors need none.
Forming one also means naming the operator in `terms.md`, `privacy.md`
and the refund policy, which today name only a contact address, and
possibly moving the Polar payout owner. The pricing promise in
`PRICING.md` ("existing subscriptions keep their price until moved")
binds whoever operates the service and moves with it.

## 10. Ranking

Scores 1 to 5, higher is better; for effort, higher means easier.

| Option | Money | Effort | Time to cash | Recognition | Entity |
|---|---|---|---|---|---|
| Compute sponsorship (Azure, Hetzner, DO, Anthropic) | 5 | 1 | 1 | 5 | Likely, for a contract |
| NLnet Restack or CodeSupply | 4 | 3 | 2 | 4 | No; licence needed |
| Company brings its host (shape A) | 4 (host cost to repose is zero) | 2 (shape A work) | 2 | 3 | No |
| Microsoft for Startups tier climb | 4 if verified, 1 if not | 3 | 3 (60 days per tier) | 2 | Business verification above $200 |
| Claude Startups | 1 (Team seats and credit over capacity) | 5 | 4 | 3 | *Unverified* |
| Per-seat company plan on Polar | 2 | 2 | 3 | 3 | No |
| Sponsor-a-seat | 1 | 3 (grants table first) | 4 | 3 | No |
| AWS Activate Founders | 2 | 2 (S3 Blob, host module) | 4 | 2 | *Unverified*; website and domain email per secondary sources |
| GitHub Sponsors | 1 | 5 | 4 | 2 | No |
| Vercel OSS | 1 (Vercel-only) | 4 | 2 (closed) | 3 | No; licence needed |
| Open Source Collective | 1 | 3 | 3 | 2 | No; org repo needed |

## 11. Risks

- **Credit runs out.** Azure credit lasts two years, or four if the
  $25,000 extension applies (*unverified*, section 2), cannot move, and
  turns into pay-as-you-go. `PRICING.md`, "Changing prices", promises
  that existing subscriptions keep their price until moved. New prices
  can apply to new subscriptions with 30 days notice; raising an
  existing subscriber's price breaks that published promise. Keeping
  the right to raise existing subscribers needs a `DECISIONS.md` entry
  amending "Changing prices" and a matching change to `billing.md`
  before launch.
- **Programs end.** Equinix Metal closed 2026-06-30, the NGI Zero
  Commons Fund 2026-06-01, and DigitalOcean's OSS credits were reported
  ended 2026-10-05. Azure OSS credits are closed, date *unverified*.
- **Eligibility claims.** Microsoft asks for a B2B startup with a
  registered name, Claude Startups and AWS ask for a founded company.
  An individual who is not a startup attesting otherwise risks a public
  clawback or account closure under the owner's name.
- **NLnet reads the product as an AI project** and declines or pulls
  the grant (section 6).
- **Sponsored and exempt accounts break I-290** with no upper bound
  until they are counted (section 8).
- **A US LLC** risks $25,000 a year for a foreign owner who misses Form
  5472.
- **Lock-in.** Azure workloads added for tier counts work against R3-4,
  R4-2 + R5-11 and R4-14; an AWS host amends R2-3 and R3-20.
- **A permissive licence** lets a cloud host repose without the owner.
  The owner may count that as success. AGPL keeps changes public and
  bars repose from use inside Google.

## 12. Suggested order

1. Owner records their country and any registered business. NLnet's
   European dimension, Microsoft verification and the entity form all
   depend on it.
2. Owner picks a licence and adds LICENSE. One commit, no code.
3. Owner settles the Microsoft contradiction: does the portal's $25,000
   offer mean business verification passed, and what is the credit
   balance and expiry? Check whether "no prior Azure account" blocks a
   second Microsoft for Startups account
   (https://www.microsoft.com/en-us/startups).
4. Owner writes the NLnet proposal before 2026-11-03 12:00 CET
   (https://nlnet.nl/propose/): guest image builder plus BYO-host agent
   as general KVM tools, EUR 30,000 to 50,000, saying plainly that the
   hosted service targets coding agents.
5. Build the `grants` table, count it in the held-seat SQL and give it
   a real memory limit, before granting any more exempt accounts.
6. Draft one-page asks for Hetzner, Microsoft and DigitalOcean, each
   offering landing and dashboard credit plus a case study.
7. Apply to Claude Startups (https://claude.com/programs/startups, form
   at https://platform.claude.com/offers/startups-application) for the
   office hours, if the owner's profile qualifies. Ask about a
   Marketplace listing.
8. Try Polar seat-based pricing in the sandbox (I-604) only after a
   named company asks for one bill.
9. If the owner qualifies on personal contributions, apply to Claude
   for Open Source (https://claude.com/contact-sales/claude-for-oss).

## Decisions for the owner

1. **Country and business status.** Name your country of residence and
   any registered business. Did Microsoft business
   verification pass (`solo-at-20.md`), and with what entity?
2. **Licence.** AGPL-3.0, Apache-2.0, or keep the repo unlicensed and
   give up NLnet, STF, Vercel OSS, Open Source Collective and the
   GitHub fund. Before 2026-11-03 if NLnet is wanted.
3. **NLnet scope.** Does the hosted coding-agent service fall under
   Restack's AI exclusion, and is CodeSupply the better fund?
4. **Sponsorship.** Pitch Microsoft, Hetzner, DigitalOcean or Anthropic
   for named compute, and with what public credit?
5. **Company buyers.** Point companies at shape A plus a host fee, or
   lift R5-6 and I-362 for a per-seat company plan at $35 or $39 once a
   named company asks.
6. **Sponsored seats.** Replace open-ended exempt accounts with
   counted, expiring grants (amends I-16, I-289, I-290)? Name the
   people who receive them, and does a grant need a use condition? Should exempt keep
   bypassing the gate?
7. **Azure workloads.** Stop at the portable services ($50,000 at 7
   workloads), or accept R3-4, R4-2 + R5-11 and R4-14 lock-in to reach
   $150,000?
8. **Price promise.** Option A: keep `PRICING.md` as written; new
   prices reach new subscriptions only, and existing ones keep theirs
   until moved. Option B: replace it before launch, through a
   `DECISIONS.md` entry and `billing.md`, with "30 days notice; early
   subscribers keep their price for N months". Either way, decide if
   the launch pricing names the Azure credit as the reason prices sit
   below cost.

## Sources

Checked 2026-10-11 unless noted.

- Microsoft for Startups application: https://learn.microsoft.com/en-us/startups/microsoft-for-startups/application (ms.date 2026-10-08)
- Microsoft for Startups FAQ: https://learn.microsoft.com/en-us/startups/microsoft-for-startups/mfs-faqs (updated 2026-08-05)
- Microsoft for Startups overview: https://learn.microsoft.com/en-us/startups/microsoft-for-startups/overview (ms.date 2026-05-22, updated 2026-08-05)
- Microsoft credit milestones: https://learn.microsoft.com/en-us/startups/microsoft-for-startups/getting-started-mfs#credit-milestones-and-requirements (ms.date 2026-05-27, updated 2026-08-05)
- Azure OSS credits: https://opensource.microsoft.com/azure-credits
- AWS Activate: https://aws.amazon.com/startups/credits
- AWS credit terms: https://aws.amazon.com/awscredits/
- AWS credits and reservations: https://repost.aws/knowledge-center/credits-ri
- AWS nested virtualization: https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html
- AWS Founders requirements (secondary): https://guptadeepak.com/startup-offers/programs/aws-activate, https://www.bluematador.com/blog/do-i-qualify-for-aws-activate
- Google for Startups Start tier: https://cloud.google.com/startup/pre-funded
- Google Scale tier (secondary): https://guptadeepak.com/startup-offers/programs/google-for-startups-cloud
- Google AGPL policy: https://opensource.google/documentation/reference/using/agpl-policy
- Claude Startups program page (no date): https://claude.com/programs/startups; expansion article (2026-10-06): https://claude.com/resources/articles/were-expanding-the-claude-startups-program-to-help-founders-build
- Claude for Open Source: https://claude.com/contact-sales/claude-for-oss
- Anthropic Fellows tracks: https://job-boards.greenhouse.io/anthropic/jobs/5023394008, https://www.anthropic.com/careers/jobs/5023394008; 2026 cohort dates (secondary): https://opportunitydesk.org/2026/04/15/anthropic-fellows-program-2026/
- AI for Science: https://support.claude.com/en/articles/11199177
- Vercel Open Source Program: https://vercel.com/open-source-program, https://vercel.com/blog/vercel-open-source-program-spring-2026-cohort
- Vercel for Startups: https://vercel.com/startups; partner requirement (secondary): https://hub.causo.ai/guides/how-to-apply-to-vercel-for-startups-2026
- Hetzner sponsorship page (404): https://www.hetzner.com/sponsoring; JOSM: https://lists.openstreetmap.org/pipermail/josm-dev/2018-February/007971.html
- Nix Community sponsors: https://nix-community.org/sponsors
- NLnet: https://nlnet.nl/propose/, https://nlnet.nl/restack/, https://nlnet.nl/codesupply/, https://www.nlnet.nl/commonsfund
- Sovereign Tech Fund: https://www.sovereign.tech/programs/fund
- GitHub Secure Open Source Fund: https://github.blog/news-insights/company-news/announcing-github-secure-open-source-fund/
- Equinix Metal sunset: https://deploy.equinix.com/blog/sunsetting-equinix-metal
- GitHub Sponsors: https://docs.github.com/en/sponsors/getting-started-with-github-sponsors/about-github-sponsors
- Polar pricing (Scale $400 a month, +1.5% non-US cards): https://polar.sh/resources/pricing; accounts: https://polar.sh/docs/features/finance/accounts; donations (404): https://polar.sh/docs/features/donations
- Polar seat-based pricing: https://polar.sh/docs/guides/seat-based-pricing, https://polar.sh/docs/features/seat-based-pricing, https://handbook.polar.sh/engineering/design-documents/seat-based-pricing
- Polar Startup Program: https://polar.sh/startup-program; tax: https://polar.sh/docs/merchant-of-record/tax
- Open Source Collective: https://opencollective.com/opensource/apply/intro, https://docs.oscollective.org/getting-started/acceptance-criteria
- DigitalOcean and Omarchy (date *unverified*): https://cloudnews.tech/digitalocean-commits-3-million-to-omarchy-becomes-its-agentic-cloud/, https://linuxiac.com/omarchy-funding-surges-to-18-5m-with-digitaloceans-3m-pledge/
- DigitalOcean OSS credits end: https://itsfoss.com/news/digitalocean-open-source-credits-end/
- Delaware taxes: https://corp.delaware.gov/paytaxes/, https://corp.delaware.gov/alt-entitytaxinstructions/
- Form 5472: https://www.irs.gov/forms-pubs/about-form-5472, instructions https://www.irs.gov/instructions/i5472
- Companies House fees: https://www.gov.uk/government/news/companies-house-fees-are-changing-from-1-february-2026
- Repo: `docs/PRICING.md`, `docs/DECISIONS.md` (R1-5, R2-3, R3-4, R3-20, R4-2 + R5-11, R4-14, R5-6, I-16, I-262, I-289, I-290, I-362, I-604, I-617), `docs/proposals/2026-10-04-solo-at-20.md`, `docs/workstreams/11-infra-opentofu.md`, `internal/api/waitlist/waitlist.go`, `internal/billing/gate.go`, `internal/billing/polar.go`, `hetzner-repricing:docs/PRICING.md` and `hetzner-repricing:docs/DECISIONS.md` (I-642), `byo-host-shape-a:docs/proposals/2026-10-10-bring-your-own-host.md` and `byo-host-shape-a:docs/DECISIONS.md` (I-647); prod `meter_samples`, read-only, 14 days to 2026-10-11
