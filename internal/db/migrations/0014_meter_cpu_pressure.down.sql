-- Reverts 0014: samples carry no CPU pressure and no guest memory figure.
alter table meter_samples drop column mem_used;
alter table meter_samples drop column host_cpu_wait_us;
alter table meter_samples drop column cpu_pressure_us;
