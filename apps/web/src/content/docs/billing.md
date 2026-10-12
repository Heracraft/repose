---
title: Pricing
description: The three plans, what each one buys, the free week, and where to see how much of yours is in use.
section: Account
order: 30
---

A plan buys memory that runs at once, disk for what your projects hold, and egress a month. The first week is free; the card goes in at checkout, before your first machine starts.

<!-- plans: the page draws lib/plans.ts here; this table is for /docs/billing.md -->

| Plan | A month                     | Running at once                        | Disk   | Egress a month |
| ---- | --------------------------- | -------------------------------------- | ------ | -------------- |
| Solo | $39 for 12 months, then $49 | 8 GB: one `large`, or two `small`      | 100 GB | 250 GB         |
| Plus | $89                         | 16 GB: one `xl`, two `large`, any mix  | 250 GB | 500 GB         |
| Pro  | $169                        | 32 GB: two `xl`, four `large`, any mix | 500 GB | 1 TB           |

<!-- /plans -->

The $39 is the founding price: the first 30 subscribers to Solo pay it for Solo's first 12 months, then $49, on their first subscription only. The 12 months start after the free week. The pricing page shows it while places are left. If you had a subscription before, or you upgrade to Plus or Pro, you pay the list price.

These are launch prices. A change reaches you by email 30 days ahead, and your subscription keeps the list price it started on for 12 months from its start.

Prices are in USD and exclude tax, which Polar adds at checkout for your country. A machine's size is its memory: `small` is 2 vCPU and 4 GB, `large` 4 vCPU and 8 GB, `xl` 8 vCPU and 16 GB. A plan's memory is how much runs at once, around the clock: the month costs the same however many hours your machines run. Every plan keeps up to 100 projects, and a stopped one costs nothing.

An agent session needs a `large` to finish its work: a coding agent with its language servers, builds and browser fills 8 GB, and a `small` runs out of memory partway through. Pick by how many agents you want working at the same time: one on Solo, two on Plus, four on Pro.

## The free week

Seven days on any plan, once per email and card: if either has had a free week before, the checkout has none and charges at once. Checkout is on Polar's page, which takes your card and first charges it on day eight, unless you cancel before then; nothing stops on day eight. Cancelling during the week ends the plan at the week's end.

## What a plan means

- **Memory.** Starting a machine that would put your running machines past the plan is refused, and the message names the machine using the memory: `Your Solo plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at https://repose.herakraft.co/billing.` An `xl` needs Plus or Pro. From a terminal, `repose run`, `start` and `attach` ask instead, as `repose stop` does: `Stop todo-app to start api? [y/N]`. A yes stops it, fetching its commits first, and starts yours; a no, or no terminal, exits 7 with the message.
- **Disk.** Counts the data your projects hold, running or stopped. A new `large` has a 40 GB disk and counts the gigabyte or so it holds at first; a disk's size is only how far that project can grow, and one project's disk can be at most the plan's whole disk. While your projects hold more than the plan's disk, creating, restoring and forking projects, and growing a disk, are refused, with one email; your machines keep running and starting. A file you delete stops counting within a day, or when its machine stops. Snapshots are free.
- **Egress.** Data your machines send to the internet, over the month. Incoming data and your own SSH traffic, port forwards included, don't count. Past the allowance, $0.05 per GB is added to your next invoice as one line. At four times the allowance (1 TB on Solo, 2 TB on Plus, 4 TB on Pro) your machines stop until the month turns, and you get an email.
- **Projects.** A plan doesn't count them: stop one and start another within the plan's memory. A stopped project uses only the disk it holds.

Example: a Solo user with a `large` running all month, a 40 GB disk and 20 GB of egress pays $49, or $39 at the founding price. The same user with 300 GB of egress pays $49 plus $2.50.

## Seeing your plan

`repose ls` ends with your plan and how much of it is in use:

```text
Solo: 8 of 8 GB running, 41.3 of 100 GB disk, 212 of 250 GB egress
this month
```

`repose ls` and `repose status` add a line when your projects hold more than the plan's disk, when egress is past the allowance, and when a payment failed. The dashboard's **Billing** page shows the same figures with the machines running, the projects holding the most disk and sending the most egress, the next charge, the overage so far and your invoices. Egress is totalled a few minutes past each hour, so it can trail by up to an hour.

## Changing and cancelling

Upgrading to a bigger plan takes effect at once, and Polar charges the prorated difference that day. Downgrading takes effect at your next renewal, and is refused while your running machines or the disk your projects hold would not fit the smaller plan; stop machines, or destroy projects or delete files in them, first. Cancelling ends the plan at the end of the month you have paid for: machines run until then, stop then, and their snapshots stay 30 days. You can undo a cancellation until it takes effect.

The card, the billing address and your receipts are in Polar's customer portal, reached from the Billing page.

## If a payment fails

Polar retries the card 2, 7, 14 and 21 days after the first failure. Meanwhile:

| When   | What happens                                                           |
| ------ | ---------------------------------------------------------------------- |
| day 0  | your machines keep running; starting one is refused; you get an email  |
| day 2  | a second email                                                         |
| day 3  | every running machine is snapshotted and stopped, and you get an email |
| day 33 | the snapshots are deleted                                              |

Paying at any point before day 33 unblocks starts. It does not start your machines again; that is yours to do with `repose start`.

## Refunds

Under the [refund policy](/refunds), the first charge after the free week is refunded on request within 14 days of it. Renewals are not refunded for a part month; cancelling stops the next one. Refunds go back to the same card.

## Deleting your account

The dashboard's **Settings** page shows your handle, email and GitHub login, with **Sign out**, and has **Delete account** at the bottom. Any egress overage for the current month is charged on a final invoice, the plan is not renewed, every machine stops, and everything, snapshots included, is deleted 30 days later.
