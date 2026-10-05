-- CPU pressure and guest memory per sample (DECISIONS I-493): microseconds in the sample in
-- which a task in the guest waited for a vCPU (guest-written, from the
-- guest's /proc/pressure/cpu), and in which the guest's hypervisor threads
-- waited for a host CPU (host-measured, the unit cgroup's cpu.pressure).
-- Zero for rows written before I-493 and for a guest without PSI.
alter table meter_samples add column cpu_pressure_us bigint not null default 0;
alter table meter_samples add column host_cpu_wait_us bigint not null default 0;
-- Memory in use as the guest sees it (MemTotal less MemAvailable),
-- guest-written; mem_rss is what the host backs and never shrinks.
alter table meter_samples add column mem_used bigint not null default 0;
