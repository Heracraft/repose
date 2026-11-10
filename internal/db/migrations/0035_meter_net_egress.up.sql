-- What a guest sent the internet, apart from everything it sent
-- (DECISIONS I-1063). net_tx is the guest's tap: SSH output to the edge,
-- port forwards, hooks and the browser view included, which the idle
-- rule and the abuse checks read as activity. net_egress is the host's
-- nft counter of what left on the uplink, which is what the plan bills.
-- Null on a sample from a hostd older than I-1063, where billing reads
-- net_tx as before.
alter table meter_samples add column net_egress bigint;
