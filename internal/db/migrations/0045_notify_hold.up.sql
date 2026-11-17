-- 0045_notify_hold (DECISIONS I-1272, I-1282): what the presence hold
-- reads, and the hold the user chose.
--
-- meter_samples.last_input_at is when somebody last typed in the guest's
-- tmux, as the guest saw it. The hold asked "is a session open", which an
-- attached laptop answers yes to all day; it asks "did anyone type" now.
-- Null from a guest or a host from before I-1272, where the hold falls
-- back to tmux_clients + ssh_sessions. It is a time, not a count, so no
-- backfill is possible or wanted.
alter table meter_samples add column last_input_at timestamptz;

-- How long a report waits while the owner is at the machine, in seconds;
-- 0 turns the hold off. The default is notify.DefaultHold (I-1274), and
-- the column is the only place a user's own choice lives.
alter table users add column notify_hold_seconds int not null default 120
  check (notify_hold_seconds >= 0 and notify_hold_seconds <= 3600);
