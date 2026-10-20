-- Reverts 0029: nothing rests, and no machine is kept awake.
alter table meter_samples drop column resting;
alter table projects drop column keep_awake;
alter table projects drop column resting_since;
