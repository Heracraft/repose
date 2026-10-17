---
title: Pricing
description: The three plans, what each one buys, the free week, and what happens when you change, cancel or miss a payment.
section: Account
order: 30
---

Your plan buys memory for running machines, disk for what your projects hold, and egress for the month. The first week is free.

<!-- plans: the page draws lib/plans.ts here; this table is for /docs/billing.md -->

| Plan | A month                     | Running at once                        | Disk   | Egress a month |
| ---- | --------------------------- | -------------------------------------- | ------ | -------------- |
| Solo | $39 for 12 months, then $49 | 8 GB: one `large`, or two `small`      | 100 GB | 250 GB         |
| Plus | $89                         | 16 GB: one `xl`, two `large`, any mix  | 250 GB | 500 GB         |
| Pro  | $169                        | 32 GB: two `xl`, four `large`, any mix | 500 GB | 1 TB           |

<!-- /plans -->

The $39 is the founding price: the first 30 subscribers to Solo pay it for Solo's first 12 months, then $49, on their first subscription only. The 12 months start after the free week, and the pricing page shows the founding price while places are left. If you had a subscription before, or you upgrade to Plus or Pro, you pay the list price. A price change reaches you by email at least 30 days ahead. A subscription keeps the list price it started on for 12 months ([terms](/terms)).

Prices are in USD and exclude tax, which Polar adds at checkout for your country. A machine's size is its [memory and vCPU](/docs/machine).

Your plan's memory is how much runs at once, around the clock: the month costs the same however many hours your machines run. An account can have [100 projects](/docs/limits#projects) on every plan, and a stopped one counts toward your disk. [`repose ls`](/docs/cli#repose-ls) ends with your plan and how much of it is in use, as the dashboard's **Billing** page does.

## What a plan means

- **Memory.** Starting a machine that would put your running machines past the plan is refused: `Your Solo plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at https://repose.herakraft.co/billing.` From a terminal, `repose run`, `start` and `attach` ask instead: `Stop todo-app to start api? [y/N]`. A yes stops it, fetching its commits first, and starts yours; a no, or no terminal, exits 7 with that refusal.
- **Disk.** Counts the data your projects hold, running or stopped. A new `large` has a 40 GB disk and counts the gigabyte or so it holds at first; a disk's size is only how far that project can grow, and one project's disk can be at most the plan's whole disk. Past the plan's disk, [anything that would add data is refused](/docs/limits#projects). You get one email about it. A file you delete stops counting within a day, or when its machine stops. Snapshots are free.
- **Egress.** Data your machines send to the internet, over the month. Incoming data and your own SSH traffic, port forwards included, don't count. Past the allowance, $0.05 per GB is added to your next invoice. At four times the allowance (1 TB on Solo, 2 TB on Plus, 4 TB on Pro) your machines stop until the period ends, and you get an email. The total is rolled up a few minutes past each hour, so it can trail by up to an hour.

## The free week

Seven days free on any plan, once per email and card: if either has had a free week before, the checkout has none and charges at once. Your card goes in at checkout and is first charged on day eight, unless you cancel before then. Nothing stops on day eight. Cancelling during the week ends the plan at the week's end.

## Changing and cancelling

Upgrading to a bigger plan takes effect at once, and Polar charges the prorated difference that day. It needs the extra [seats](/docs/limits#when-repose-is-full) free and never goes on the waitlist: without them it's refused with `no seat is free for the upgrade right now; try again later`, and you keep the plan you have. Downgrading takes effect at your next renewal. It's refused while your running machines or the disk your projects hold wouldn't fit the smaller plan: stop machines, or destroy projects or delete files in them, first. Cancelling ends the plan at the end of the month you have paid for: machines run until then, stop then, and their snapshots stay 30 days. You can undo a cancellation until it takes effect.

## If a payment fails

Polar retries the card 2, 7, 14 and 21 days after the first failure. Meanwhile:

| When   | What happens                                                           |
| ------ | ---------------------------------------------------------------------- |
| day 0  | your machines keep running; starting one is refused; you get an email   |
| day 2  | a second email                                                         |
| day 3  | every running machine is snapshotted and stopped, and you get an email  |
| day 33 | the snapshots are deleted                                              |

The day-3 email's subject is `Your machines were stopped: a payment failed`.

A payment unblocks starts. It doesn't start your machines again; `repose start` does. Your card, billing address and receipts are in Polar's customer portal, behind **Manage card and receipts** on the **Billing** page.

## Refunds

Under the [refund policy](/refunds), the first charge after the free week is refunded on request within 14 days of it. Renewals aren't refunded for a part month; cancelling stops the next renewal.

## Deleting your account

The dashboard's **Settings** page has **Delete account** at the bottom. Any egress overage for the month is charged on a final invoice, the plan isn't renewed, every machine stops, and everything, snapshots included, is deleted 30 days later.
