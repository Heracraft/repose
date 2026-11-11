# Only a choice puts an account on the waitlist (proposal, 2026-11-10)

**Status: proposal; nothing here is decided.** The product value council
(`reports/2026-11-04-product-value-council.md`, "waitlist-seats", all
four members) asked for a fix of the bug where loading the Billing page
puts a plan-less account on the waitlist. The owner found the bug on
2026-10-17. This file checks main as of `a408bf2f` (2026-11-06) and
proposes what is left.

## Summary

| Question | Answer |
|---|---|
| Does `GET /billing` still join the waitlist? | No. `adffcbd7` (I-1033, 2026-11-04) moved `Overview` from `Seats.Reserve` to a read-only `Seats.Fits`. It is in tag `v0.1.44` and on `origin/main`, so prod has it |
| Is there a test? | Yes. `TestAnExpiredHoldEndsTheInvitations` reads `GET /billing` as three accounts while full and checks that nobody joined and no lapse ended. The commit says a mutation that puts `Overview` back on `Reserve` fails it |
| What joins today? | `POST /billing/checkout` when the plan does not fit, and `POST /billing/waitlist`. Nothing else writes a `waitlist` row |
| What is left | (1) An exempt account can still join through the two POST routes, and the inviter then holds a seat for it. (2) Rows that page views made before `v0.1.44` are still in the queue. (3) `docs/features/projects.md` describes `POST /billing/waitlist`'s answer wrongly. (4) `api.md` does not say that `GET /billing` writes nothing |
| Size | S. One refusal in the api, one filter in the inviter, three doc lines, one read-only query |

## 1. The repo today

**The read path.** `Service.Overview` (`internal/billing/service.go:88`)
is `GET /v1/billing` (`internal/api/http/routes.go:69`,
`billingOverview` at `internal/api/http/billing.go:60`). For an account
with no live subscription it asks, for each plan, whether the plan
fits (`service.go:116-125`):

```go
// Fits, never Reserve: reading the page puts nobody on the
// waitlist and undoes nobody's lapse (I-1033).
ok, err := s.seats.Fits(ctx, u.ID.String(), p.Seats)
```

`waitlist.Service.Fits` (`internal/api/waitlist/waitlist.go:228-240`)
counts the free seats plus the user's own unexpired hold, with no lock
and no write. The `Seats` interface says the same contract in its
comment (`internal/api/waitlist/seats.go:37-40`). The stand-in
`SubscriptionSeats.Fits` never waitlists anyone
(`internal/billing/seats.go:31-35`). `GET /me` reads the place with
`billing.WaitlistPlace` (`internal/api/http/users.go:33`,
`internal/billing/gate.go:305`), which is a `select`. The billing gate's
`subscription_required` refusal reads the same place and writes nothing
(`internal/billing/gate.go:140-151`).

**The write paths.** One statement inserts a `waitlist` row:
`join` (`internal/api/waitlist/waitlist.go:299-300`). Two functions call
it:

- `Reserve` (`waitlist.go:245`), whose only caller is
  `Service.Checkout` (`internal/billing/service.go:217`), which is
  `POST /v1/billing/checkout`. The dashboard calls it only from the
  **Choose** button (`apps/web/src/routes/billing/+page.svelte:258-278`,
  button at `:444-452`).
- `Join` (`waitlist.go:280`), which is `POST /v1/billing/waitlist`
  (`internal/api/http/seats.go:15-25`). The dashboard calls it only from
  **Join the waitlist** (`+page.svelte:280-291`, button at `:676-683`).

The `?plan=` link from the landing page only moves the focus to that
plan's button (`+page.svelte:247-256`). The CLI never calls either POST
route: a plan-less `repose run` opens the Billing page and waits
(`internal/cli/payment.go:100-121`).

**When the bug lived.** `Overview` called `Reserve` from `1315d5ea`
(2026-09-27, I-289) until `adffcbd7`. Each plan-less visit while every
seat was taken added the visitor to the queue and sent a
`waitlist_joined` email. The memory note of 2026-10-17 records one
exempt account that joined this way; the owner deleted its hold on prod
on 2026-10-18.

**What the fix did not cover.**

1. *Exempt accounts.* The dashboard shows an exempt account no plan
   cards and no waitlist button (`+page.svelte:627-633`). The api does
   not refuse it: `Checkout` checks only for a live subscription
   (`service.go:211-216`), and `billingWaitlist` checks nothing
   (`seats.go:15-25`). `store.ListWaiting`
   (`internal/api/store/store.go:760-762`) does not filter exempt
   accounts, so the inviter (`waitlist.go:424-465`) can give one a
   72-hour hold that nobody uses. The gate never needs a plan for an
   exempt account (`internal/billing/gate.go:101`), so a place in the
   queue does nothing for it.
2. *Old rows.* Rows that page views made before `v0.1.44` look the same
   as rows from a real choice. Under I-1033 such a user is invited once,
   holds a seat for 72 hours, and then lapses. Each one costs a seat for
   three days and adds one to the "waiting" count on the landing page
   and on `/billing` until then.
3. *A wrong line in the feature doc.* `docs/features/projects.md:166-168`
   says that on `POST /billing/waitlist` the user "gets `503 waitlisted`".
   That route answers `200` with `{position, joined_at}`
   (`seats.go:24`, `docs/interfaces/api.md:354`).
4. *The contract.* `api.md:352` says what `plans[].available` means, but
   does not say that the route writes nothing. The next person to touch
   `Overview` has only a code comment to stop them.

## 2. Proposed behaviour

**2.1 An exempt account cannot join.** `POST /billing/checkout` and
`POST /billing/waitlist` answer an exempt account with `409 conflict`,
`detail.reason = "exempt"`, and the message:

```
This account is billing-exempt, so it has no plan to choose and no place to wait for.
```

The dashboard never sends either request for an exempt account, so the
user sees nothing new. The check goes before `Reserve` and `Join`, in
`Service.Checkout` and in `billingWaitlist`.

**2.2 The inviter skips exempt accounts.** `store.ListWaiting` and the
`Waiting` count (`waitlist.go:180`) leave out rows whose user has
`billing_status = 'exempt'`, as they already leave out suspended,
cancelled and deleted accounts. A row that exists keeps its data, so a
change of status back to `none` puts the user back in line at the same
place.

**2.3 The rows the bug made.** Run a read-only query first, then let
the owner choose (decision 2):

```sql
-- Waiting rows that joined before v0.1.44 was deployed, with whether
-- the user has a subscription now.
select w.user_id, w.joined_at, u.billing_status,
       exists (select 1 from subscriptions s where s.user_id = w.user_id) as ever_subscribed
  from waitlist w join users u on u.id = w.user_id
 where w.invited_at is null and w.converted_at is null and w.lapsed_at is null
   and w.joined_at < '2026-11-06T02:00:00Z'
 order by w.joined_at;
```

The api's request log has `route` and `user_id` on each line
(`internal/api/http/server.go:399-401`). Where Loki still holds the day,
a row with a `POST /v1/billing/checkout` or `POST /v1/billing/waitlist`
from that user at its `joined_at` came from a choice. A row with only
`GET /v1/billing` came from the bug.

**2.4 No change to the choice paths.** **Choose** on a plan whose seats
went while the page was open still joins, as I-290 says, and the page
then shows the place (`+page.svelte:268-272`). That is an explicit
choice. **Join the waitlist** stays as it is.

## 3. Docs that change

- `docs/interfaces/api.md:352`: add to `GET /billing` "It writes
  nothing: reading it never joins the waitlist or ends a lapse (I-1033)."
  Add `409 conflict` `detail.reason = exempt` to `:353` and `:354`.
  This is an additive contract change, so it ships in the same commit as
  2.1.
- `docs/features/projects.md:166-168`: `POST /billing/waitlist` answers
  `{position, joined_at}`; only a checkout that does not fit answers
  `503 waitlisted`. Add the exempt refusal.
- `docs/features/pricing.md:40-50`: one sentence that an exempt account
  has nothing to choose.
- Public docs: no change. `apps/web/src/content/docs/limits.md:26-28`
  already says that choosing a plan or joining puts you on the list, and
  says nothing about a page view.

## 4. Tests and the evidence that closes each item

| Item | Test | Evidence |
|---|---|---|
| `GET /billing` joins nobody (done) | `TestAnExpiredHoldEndsTheInvitations`, `internal/api/http/waitlist_test.go:455-465` | Already green on main; paste the run with `-run TestAnExpiredHoldEndsTheInvitations -v` in `nix develop ./nix` |
| 2.1 | New `TestAnExemptAccountCannotJoin`: as an exempt account while full, `POST /billing/checkout` and `POST /billing/waitlist` both answer `409` with `reason = exempt`, and `select count(*) from waitlist` is still 0 | The test output, plus a mutation (drop the check in `billingWaitlist`) that fails it |
| 2.2 | New case in the inviter test: one exempt row first in line and one plain row second; one free seat; one tick invites the plain row | The test output, plus `repose-admin waitlist list` on a dev api that shows the exempt row with no position |
| 2.3 | None (data) | The query output pasted in the decision entry, with the count of rows the owner chose to lapse |
| 3 | `go test ./internal/cli -run TestDocs` and the web docs tests | Pasted runs |
| Dashboard | `apps/web/tests/billing.spec.ts` gains one case: load `/billing` while full and assert that no `POST` request left the page | The Playwright run |

## 5. Risks

- A status change to exempt while the user holds an invitation: 2.2
  hides the row from the queue, but the hold still counts as held until
  it expires. Expire it in the same statement that sets the status, or
  accept up to 72 hours of one held seat.
- Lapsing old rows (decision 2) must not go through `Inviter.expire`
  (`waitlist.go:473`), which sends the `waitlist_expired` email; a plain
  update of `lapsed_at` sends none. A row that a person made by choice,
  on a day the logs no longer cover, would lose its place. The query
  shows how many rows are at stake before anything changes.
- The fix in `adffcbd7` lives in two places that must agree: `Fits` and
  `Reserve` share `fits` (`waitlist.go:208`). A later change to one
  without the other would show a plan as available that a checkout then
  refuses, or the other way round. The shared helper is the guard.

## Decisions for the owner

1. **Exempt accounts.** Refuse the two POST routes with `409 exempt`
   and skip exempt rows in the inviter (recommended), or leave the api
   open and rely on the dashboard hiding the buttons.
2. **Rows the bug made.** Lapse the waiting rows that joined before
   `v0.1.44` with no POST in the logs, with no email; or leave them, so
   that each gets one invitation and lapses on its own under I-1033.
3. **The 2026-10-17 UI notes.** They are separate work (branch
   `waitlist-feedback`, `docs/proposals/2026-10-17-waitlist-feedback.md`
   there): for example, the page still prints "Every seat is taken and
   N people are waiting" (`+page.svelte:672-673`), which the owner's
   "no queue length shown" note would remove. Confirm they stay out of
   this change.
