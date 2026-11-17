-- Reverts 0045. The hold goes back to reading session counts (I-689) and
-- every user back to the one built-in hold.
alter table users drop column notify_hold_seconds;
alter table meter_samples drop column last_input_at;
