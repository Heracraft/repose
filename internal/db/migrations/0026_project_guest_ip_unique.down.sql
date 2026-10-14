-- Reverts 0026 (DECISIONS I-723): two live projects may hold one guest address again.
drop index projects_guest_ip_live;
