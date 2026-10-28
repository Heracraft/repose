-- A cooldown after an expired waitlist hold (DECISIONS I-902).
--
-- I-290 moves a hold that runs out unconverted to the back of the list
-- (`joined_at = now`, `expired_invites + 1`) so a ghost cannot block the
-- queue, and I-294 (4) runs expiries before invitations in the same
-- tick. With one person waiting, the back of the list is also its front:
-- the tick that expired the hold freed the seat, counted the same user
-- as waiting, and invited them again, so the owner got the "your hold
-- ran out" and "a seat is yours" emails in the same minute, every 72
-- hours, for ever. I-290 rejected holding the seat (it blocks the queue)
-- and dropping the user (a launch weekend is not a reason to lose
-- them); a cooldown is neither.
--
-- The row keeps its place so `position` can still tell the user where
-- they stand; it is only not a candidate until the cooldown passes.
alter table waitlist add column cooldown_until timestamptz;

-- The waiting index is the candidate list, so it carries the column it
-- is now filtered by.
create index waitlist_cooldown on waitlist(cooldown_until) where cooldown_until is not null;
