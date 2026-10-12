-- Reverts 0022: nothing restarts machines after a host reboot.
drop table resume_notices;
drop table guest_resumes;
