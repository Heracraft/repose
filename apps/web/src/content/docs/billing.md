---
title: Pricing
description: The three plans, what each one buys, the free week, and where to see your hours.
section: Account
order: 30
---

repose is a monthly plan. You choose one before your first machine starts, with a card, and the first week is free.

| Plan | A month | Running at once                        | Disk   | Egress a month |
| ---- | ------- | -------------------------------------- | ------ | -------------- |
| Solo | $29\*   | 8 GB: one `large`, or two `small`      | 100 GB | 250 GB         |
| Plus | $59     | 16 GB: one `xl`, two `large`, any mix  | 250 GB | 500 GB         |
| Pro  | $99     | 32 GB: two `xl`, four `large`, any mix | 500 GB | 1 TB           |

\*Your first subscription to Solo costs $20 a month for its first 3 months, then $29. The 3 months start after the free week. Until the $29 month starts, the egress allowance is 100 GB a month: $0.05 a GB is charged past 100 GB, and your machines stop at 400 GB. If you had a subscription before, or you upgrade to Plus or Pro, you pay the price in the table.

Prices are in USD and exclude tax, which Paddle adds at checkout for your country. A machine's size is its memory: `small` is 2 vCPU and 4 GB, `large` 4 vCPU and 8 GB, `xl` 8 vCPU and 16 GB. A plan says how much of that may run at the same time; projects cost nothing while stopped, and the month costs the same however many hours run.

An agent session needs a `large` to finish its work: a coding agent with its language servers, builds and browser fills 8 GB, and a `small` runs out of memory partway through. So pick by how many agents you want working at once: one on Solo, two on Plus, four on Pro.

## The free week

Seven days on any plan. Your card is taken at checkout and first charged on day eight, unless you cancel before then; nothing stops on day eight. Cancelling during the week ends the plan at the week's end.

## What a plan means

- **Memory.** Starting a machine that would put your running machines past the plan is refused, and the message names the machine using the memory: `Your Solo plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at https://repose.herakraft.co/billing.` An `xl` needs Plus or Pro.
- **Disk.** Creating a project or growing a disk past the plan's total is refused. Disk counts by the size you chose, running or stopped; snapshots are free.
- **Egress.** Data your machines send to the internet, over the month. Incoming data and your own SSH traffic, port forwards included, don't count. Past the allowance, $0.05 per GB is added to your next invoice as one line. At four times the allowance (1 TB on Solo, 400 GB during its introductory months, 2 TB on Plus, 4 TB on Pro) your machines stop until the month turns, and you get an email.
- **Projects.** A plan doesn't count them: stop one and start another within the plan's memory. A stopped project uses only its disk.

Example: after the first 3 months, a Solo user with a `large` running all month, a 40 GB disk and 20 GB of egress pays $29. The same user with 300 GB of egress pays $29 plus $2.50.

## Seeing your hours

`repose ls` and `repose status` end each project's line with its running time today and this month (the agent column is cut here):

```text
todo-app   large  running   2h14m   …   today 2h14m  month 41h
```

The dashboard's **Billing** page shows the same against your plan: memory running, disk allocated, egress this month and the overage so far, plus your invoices. Hours are totalled a few minutes past each hour, so figures can trail by up to an hour.

## Changing and cancelling

Upgrading to a bigger plan takes effect at once; Paddle prorates the difference on your next invoice. Downgrading takes effect at your next renewal, and is refused while your running machines or allocated disk would not fit the smaller plan; stop or destroy some first. Cancelling ends the plan at the end of the month you have paid for: machines run until then, stop then, and their snapshots stay 30 days. You can undo a cancellation until it takes effect.

The card, the billing address and your receipts are in Paddle's portal, reached from the Billing page.

## If a payment fails

Paddle retries on its own schedule. Meanwhile:

| When   | What happens                                                           |
| ------ | ---------------------------------------------------------------------- |
| day 0  | your machines keep running; starting one is refused; you get an email  |
| day 2  | a second email                                                         |
| day 3  | every running machine is snapshotted and stopped, and you get an email |
| day 33 | the snapshots are deleted                                              |

Paying at any point before day 33 unblocks starts. It does not start your machines again; that is yours to do with `repose start`.

## Refunds

The first charge after the free week is refunded on request within 14 days of it. Renewals are not refunded for a part month; cancelling stops the next one. Refunds go back to the same card. [The full statement](/refunds).

## Deleting your account

The dashboard's **Settings** page shows your handle, email and GitHub login, and has **Delete account** at the bottom. Any egress overage for the current month is charged, the plan is cancelled at once, every machine stops, and everything, snapshots included, is deleted 30 days later.
