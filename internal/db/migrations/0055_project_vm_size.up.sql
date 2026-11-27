-- What a running machine's VM was booted with, and what it gets with its
-- host to itself, on a host that sizes each start to what its running
-- machines leave (a Mac without the appliance, DECISIONS I-1680):
-- {"vcpus", "mem_mib", "full_vcpus", "full_mem_mib"}. Null on every other
-- host, where a machine is its class, and on every row before this.
alter table projects add column if not exists vm_size jsonb;

-- What a machine's memory, in a save file on a Mac, waits for (DECISIONS
-- I-1682): 'unlock' (the Mac's user is logged in, its screen locked) or
-- 'login' (nobody is logged in); null otherwise, on every fleet row and on
-- every row before this.
alter table meter_samples add column if not exists waits_for text;
