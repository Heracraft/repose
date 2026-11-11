-- Reverts 0040_says_handoff. A row still in the hand-off is failed
-- rather than dropped: the old constraint has no state for it.
drop index says_pending;
update says set state = 'failed', error = 'the machine did not answer' where state in ('pending','sending');
alter table says drop column refusal;
alter table says drop column replaced;
alter table says drop constraint says_state;
alter table says add constraint says_state
  check (state in ('delivered','waiting','replaced','failed'));
