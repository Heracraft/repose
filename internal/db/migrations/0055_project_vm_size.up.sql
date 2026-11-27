-- What a running machine's VM was booted with, and what it gets with its
-- host to itself, on a host that sizes each start to what its running
-- machines leave (a Mac without the appliance, DECISIONS I-1680):
-- {"vcpus", "mem_mib", "full_vcpus", "full_mem_mib"}. Null on every other
-- host, where a machine is its class, and on every row before this.
alter table projects add column if not exists vm_size jsonb;
