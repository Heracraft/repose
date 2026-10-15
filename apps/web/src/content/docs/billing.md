---
title: Pricing
description: The three plans, what each one buys, the free week, and where to see how much of yours is in use.
section: Account
order: 30
---

Your plan determines concurrent memory, disk and egress a month. The first week is free.

<!-- plans: the page draws lib/plans.ts here; this table is for /docs/billing.md -->

| Plan | A month                     | Running at once                        | Disk   | Egress a month |
| ---- | --------------------------- | -------------------------------------- | ------ | -------------- |
| Solo | $39 for 12 months, then $49 | 8 GB: one `large`, or two `small`      | 100 GB | 250 GB         |
| Plus | $89                         | 16 GB: one `xl`, two `large`, any mix  | 250 GB | 500 GB         |
| Pro  | $169                        | 32 GB: two `xl`, four `large`, any mix | 500 GB | 1 TB           |

<!-- /plans -->

The $39 is the founding price: the first 30 subscribers to Solo pay it for Solo's first 12 months, then $49, on their first subscription only. The 12 months start after the free week. If you had a subscription before, or you upgrade to Plus or Pro, you pay the list price.

Prices are in USD and exclude tax, which Polar adds at checkout for your country.  A machine's size corresponds to allotted memory: 
- `small`2 vCPU and 4 GB, 
- `large` 4 vCPU and 8 GB, 
- `xl` 8 vCPU and 16 GB. 

Your plan dictates how much memory runs at once, around the clock: the month costs the same however many hours your machines run. Every plan keeps up to 100 projects, stopped machines only count towards your disk.

## What a plan means

- **Memory.** %%Starting a machine that would put your running machines past the plan is refused, and the message names the machine using the memory: `Your Solo plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at https://repose.herakraft.co/billing.` An `xl` needs Plus or Pro. From a terminal, `repose run`, `start` and `attach` ask instead, as `repose stop` does: `Stop todo-app to start api? [y/N]`. A yes stops it, fetching its commits first, and starts yours; a no, or no terminal, exits 7 with the message.%%
- %%^^^ this section above is repeated like 3x by now%%
- **Disk.** Counts the data your projects hold, running or stopped. A new `large` has a 40 GB disk and counts the gigabyte or so it holds at first; a disk's size is only how far that project can grow, and one project's disk can be at most the plan's whole disk. While your projects hold more than the plan's disk, creating, restoring and forking projects, and growing a disk, are refused, with one email; your machines keep running and starting. A file you delete stops counting within a day, or when its machine stops. Snapshots are free.
- **Egress.** Data your machines send to the internet, over the month. Incoming data and your own SSH traffic, port forwards included, don't count. Past the allowance, $0.05 per GB is added to your next invoice . At four times the allowance (1 TB on Solo, 2 TB on Plus, 4 TB on Pro) your machines stop for that billing cycle. You will receive an email from us.
- **Projects.** Capped at 100.

## Changing and cancelling

Upgrading to a bigger plan takes effect at once, and Polar charges the prorated difference that day. Downgrading takes effect at your next renewal, and is refused while your running machines or the disk your projects hold would not fit the smaller plan; stop machines, or destroy projects or delete files in them, first. Cancelling ends the plan at the end of the month you have paid for. You can undo a cancellation until it takes effect.

## If a payment fails

Polar retries the card 2, 7, 14 and 21 days after the first failure. Meanwhile:

| When   | What happens                                                           |
| ------ | ---------------------------------------------------------------------- |
| day 0  | your machines keep running; starting one is refused; you get an email  |
| day 2  | a second email                                                         |
| day 3  | every running machine is snapshotted and stopped, and you get an email |
| day 33 | the snapshots are deleted                                              |

Paying at any point before day 33 unblocks starts. 

## Refunds

Under the [refund policy](/refunds), the first charge after the free week is refunded on request within 14 days of it. Renewals are not refunded for a part month; cancelling stops the next billing cycle.

%%## Deleting your account

The dashboard's **Settings** page shows your handle, email and GitHub login, with **Sign out**, and has **Delete account** at the bottom. Any egress overage for the current month is charged on a final invoice, the plan is not renewed, every machine stops, and everything, snapshots included, is deleted 30 days later%%.
