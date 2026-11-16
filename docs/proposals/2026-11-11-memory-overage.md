# Memory above the plan, metered by the day (proposal, 2026-11-11)

**Status: parked for later. Nothing here is decided; nothing is to be
built now.** This records the owner's thinking of 2026-11-10 and
2026-11-11 so that it is not lost. No DECISIONS id is reserved, no code
is written, and no shipped behaviour changes because of this file.

Repo facts carry a `file:line` from `main` at `4e0b926c`. Every money
figure is an illustration and is marked *estimate*; none is measured and
none is a price.

## Summary

| Question | Where this lands |
|---|---|
| What is a plan? | A floor: flat, monthly, reserved. The plan's memory is a seat the host holds, running or stopped (I-289, I-569, I-585) |
| What is sold above it? | Memory, metered in units of 8 GB for each day the extra runs. Not per hour |
| Why a day and not an hour? | I-289 dropped the hourly meter because it taught users to stop machines at night |
| What does it cost? | More, over a steady month, than the next plan tier. The number is open |
| Is overage memory reserved? | No. It sells only unsold seats. A full host sells none |
| What does a full host answer? | The `plan_limit` refusal says the fleet has no spare memory, and the waitlist (I-290, I-1033) is the existing answer |
| Is a machine ever stopped for it? | No. An overage machine holds its seat until the user stops it |
| Does the idle pause add stock? | No. The pause frees cores, not memory (I-824) |
| How does the user turn it on? | One overage switch for every metered resource, off by default, with one monthly dollar cap |
| What is open? | The price, the reserve size, placement, and the refusal text. Section 7 |

## 1. The repo today

**A plan is a floor, not a meter.** A plan buys memory that may run at
once, disk for the bytes its projects hold, and egress for the month
(`docs/PRICING.md:5-8`). Solo is $49 for 8 GB, Plus $89 for 16 GB, Pro
$169 for 32 GB (`docs/PRICING.md:16-20`, `internal/billing/plans.go:45-47`).
The shape is flat because the pitch is "the agent keeps working after the
laptop closes", and an hourly meter argued with it (I-289,
`docs/DECISIONS.md:7610`).

**The memory rule.** The sum of the classes of a user's `running`,
`starting` and `restoring` machines never passes the plan's memory. One
more start is refused with `payment_required` and
`detail.reason = plan_limit`, which carries `plan`, `limit_gb`, `used_gb`
and the slugs using the memory (`docs/PRICING.md:67-71`,
`docs/interfaces/api.md:370`). The user reads: `Your Solo plan runs 8 GB
at once and todo-app is using it. Stop it, or upgrade at
https://repose.herakraft.co/billing.`
(`apps/web/src/content/docs/billing.md:28`).

**A seat is held, not used.** A seat is 8 GB of memory that may run at
once on the fleet (`docs/PRICING.md:129`). Seats are held by every
subscription that is `trialing`, `active` or `past_due`, and by every
unexpired waitlist invitation (`docs/PRICING.md:138-143`, I-290). "The
fleet pays for every seat sold around the clock, and an empty seat earns
nothing" (`docs/PRICING.md:236-237`). Checkout without a free seat puts
the user on the waitlist (I-290, amended by I-1033,
`docs/DECISIONS.md:29751`).

**Seats are a sales unit; placement is a second, smaller ceiling.** A
host's seats are the smaller of its memory ceiling and its CPU ceiling
(I-739, `docs/DECISIONS.md:20939`, amended by I-784). host-01 sells 4
seats, host-02 sells 24, and the fleet is 28 while both are `ready`
(I-786, `docs/DECISIONS.md:25841-25852`). Placement is a separate test:
committed guest vCPUs at most 150 percent of physical cores
(`internal/api/scheduler/scheduler.go:328`), so host-01 places 12 small
guests on the 8 cores that sell it 4 seats. Memory is never
oversubscribed; the host reserve is taken off the top first
(`internal/api/scheduler/scheduler.go:105-125`).

**Egress is the one metered resource today.** Past the allowance, $0.05 a
GB is added to the next invoice as one overage line. At four times the
allowance the user's machines stop for the rest of the period
(`docs/PRICING.md:86-95`, `apps/web/src/content/docs/limits.md:34`). This
overage is **on by default** and has no switch and no dollar cap.

**Burst cores is the second metered resource, designed and not built.**
`docs/proposals/2026-11-10-burst-cores.md` §8 and §9 carry the owner's
2026-11-04 decision: CPU time above a machine's base cores is metered
against a plan allowance, and overage is off by default with a monthly
dollar cap.

## 2. The shape

Memory above the plan is sold, metered, in **one unit per 8 GB per day
the extra runs**.

- A day, not an hour. I-289 removed hourly metering because it taught
  users to stop machines at night, and that is the behaviour the product
  exists to end. A day is coarse enough that nobody watches a clock.
- 8 GB, because that is already the unit of a seat
  (`docs/PRICING.md:129`) and of a `large` (`docs/DESIGN.md` §5). An `xl`
  above the plan is two units a day.
- A day is counted when the extra memory runs at any point in it. A
  machine started for ten minutes costs a unit. This is the coarse rule;
  a part-day rule is a price question, not a design one.

**The price is set against the next tier, not against cost.** Overage is
the way out of a day's crunch, never the cheap way to live above the
plan. Solo to Plus is $40 a month for the same 8 GB
(`internal/billing/plans.go:45-46`), so an 8 GB-day must cost more than
$40 divided by about 30. At $2.50 a day (*estimate*, an illustration and
not a price) a steady month of one extra 8 GB costs about $75, which is
1.9 times the tier step, and a two-day crunch costs $5.

## 3. Stock: only unsold seats

**Memory above the plan is not reserved.** This is the difference between
the plan and the overage, and it is the whole of the risk model. The
plan's memory is held for the user whether the machine runs or not. The
overage's memory is held for nobody.

- The stock is **physical seats minus sold seats**: the seats a `ready`,
  undrained host delivers, less the seats held by subscriptions and
  unexpired invitations, whether a sold seat's machine runs or is
  stopped. A sold seat is never lent out, because its owner may start
  their machine at any minute.
- **A full host sells no overage.** 30 users holding 30 seats leaves
  nothing. The refusal says so, and the waitlist is the existing answer
  for a fleet with no room (I-290, I-1033).
- **An overage machine holds its seat until it stops.** Nobody is stopped
  mid-work to free memory for a signup. The seat comes back when the user
  stops the machine, as any other seat does.
- **A reserve of N unsold seats is never sold as overage.** N is the
  owner's number. The arithmetic behind it: a signup pays $49 a month for
  a seat it holds every day, which is about $1.61 a day (*estimate*); an
  overage day on the same 8 GB earns about $2.50 (*estimate*) and earns
  it only on the days the machine runs. Overage earns more per busy day
  and far less per year, so overage must never be the reason a signup
  goes on the waitlist.

Because the stock is per host and a project is pinned to its host for
life (`docs/DESIGN.md` §3), the stock a user can reach is their own
host's, not the fleet's. Two hosts with one spare seat each cannot run
one `xl` of overage.

## 4. The idle pause adds no stock

The idle pause (I-818, I-844, I-846, I-886) pauses a quiet machine's
cores. It does not take its memory back: I-824 is titled "The pause must
never cost a machine its memory" (`docs/DECISIONS.md:25209`), and the
public text says the cores are paused
(`apps/web/src/content/docs/lifecycle.md:111`).

The memory half of `docs/proposals/2026-10-11-pause-idle-machines.md` §9
— pushing a paused guest's RAM to encrypted host swap and selling the
freed blocks — was **not** taken. Nothing in the fleet turns resting time
into memory stock. A proposal that counts resting machines as overage
supply is counting memory that is still resident.

## 5. One switch for every metered resource

Repose will have three metered resources: burst core-hours above the
allowance, memory above the plan, and egress above the allowance. They
get **one** overage switch, not three.

- **Off by default.** No user is charged for something they did not
  choose.
- **One monthly dollar cap**, over all three meters together. A user sets
  one number and knows the worst case.
- At the cap, each meter falls back to its own plan behaviour: burst
  drops to base cores, memory above the plan is refused as `plan_limit`,
  egress keeps its own stop at four times the allowance.

**Egress is the exception that needs its own decision line.** Egress
overage ships today on by default, with no cap
(`docs/PRICING.md:86-95`). Folding it under an off-by-default switch
changes behaviour a user already has: an account that passes its egress
allowance would stop being billed and start being refused. That is a
shipped-behaviour change, with a 30-day notice
(`docs/PRICING.md:259-269`), and it belongs in its own `DECISIONS.md`
entry rather than inside this one.

## 6. What a user reads

The shape follows `docs/proposals/2026-11-10-burst-cores.md` §9. Every
line below is a sketch, not approved copy.

| Place | Text |
|---|---|
| `repose ls`, the plan line | today `Solo: 8 of 8 GB running, 41.3 of 100 GB disk, 212 of 250 GB egress this month` (`internal/cli/status.go:204`). With overage on and in use, it appends `, 8 GB over your plan today: $2.50 of your $20 cap` |
| `repose run` on a full plan, overage off | the `plan_limit` sentence as today, and one line: `or turn on overage at https://repose.herakraft.co/billing` |
| `repose run` on a full plan, overage on, stock free | from a terminal, `Start api with 8 GB above your Solo plan, $2.50 for today? [y/N]`, beside the existing `Stop todo-app to start api?` (`apps/web/src/content/docs/billing.md:28`) |
| `repose run` on a full plan, overage on, no stock | `Your Solo plan runs 8 GB at once, and the server has no spare memory today. Stop a machine, or upgrade at https://repose.herakraft.co/billing.` |
| Billing page | a Memory row beside Egress and Burst: units used this period, the money so far, the Overage switch and the one cap field |
| Notification at the cap | one a period, as burst's cap email does |
| `/docs/billing` | a Memory bullet in "What a plan means": what a unit is, the price, off by default, the cap, and that the server may have no spare memory |
| `/docs/limits` | the `plan_limit` section gains the two refusals: plan full, and server full |

## 7. Open questions, not answered here

1. **The price of an 8 GB-day.** Constrained from below by the tier step
   ($40 a month between Solo and Plus) and from above by nothing yet
   measured.
2. **The reserve size N.** How many unsold seats a host keeps back from
   overage so that a signup is never refused.
3. **Placement.** May an overage machine be placed on a host whose unsold
   seats a later signup needs? A project is pinned for life
   (`docs/DESIGN.md` §3), so an overage machine placed today constrains
   that host for as long as the project lives, not for as long as the
   overage runs.
4. **How `plan_limit` changes.** Today one refusal means one thing. With
   overage there are three states: plan full with overage off, plan full
   with overage on and stock free, plan full with no stock. `detail`
   needs a field for them, and the CLI needs a sentence each
   (`docs/interfaces/api.md:370`).
5. **Part days.** Whether a machine that runs ten minutes costs a whole
   unit, and what the docs say about a machine started at 23:55.
6. **Whether the switch is one or three.** Section 5 proposes one; the
   burst proposal reserved its own `burst_cap_cents`
   (`docs/proposals/2026-11-10-burst-cores.md` §8), and the two must not
   ship as separate switches by accident.

## Sources

- Repo: `docs/PRICING.md`, `docs/features/pricing.md`,
  `docs/DESIGN.md` §3 and §5, `docs/interfaces/api.md:370`,
  `internal/billing/plans.go:45-47`,
  `internal/api/scheduler/scheduler.go:105-125`, `:328`,
  `internal/api/waitlist/hostseats.go:94-110`,
  `internal/cli/status.go:204`,
  `apps/web/src/content/docs/billing.md:28,30`,
  `apps/web/src/content/docs/limits.md:10,26,34`,
  `apps/web/src/content/docs/lifecycle.md:111`.
- DECISIONS: I-289 (L7610), I-290 (L7688), I-569 (L15657), I-585
  (L15894), I-734 (L20663), I-739 (L20939), I-784 (L23674), I-786
  (L25764), I-818 (L25008), I-824 (L25209), I-844 (L25327), I-846
  (L25369), I-886 (L26220), I-1033 (L29751).
- Proposals: `2026-10-11-pause-idle-machines.md` §9,
  `2026-11-10-burst-cores.md` §8 and §9, `2026-10-11-price-level.md` §4.
