-- A host whose thin pool cannot serve guest volumes (DECISIONS I-644):
-- inactive after a reboot, missing, or failed. hostd sends it in Hello and
-- every Heartbeat; the scheduler places nothing there while it is set, and
-- a start that fails with volume_unavailable leaves its project stopped.
alter table hosts add column pool_unavailable boolean not null default false;
