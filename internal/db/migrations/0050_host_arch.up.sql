-- CPU architecture the host reported ("x86_64", "aarch64"), separate from
-- nixos_system, which holds the /nix/store/... closure path from
-- readlink /run/current-system (ENG-89, DECISIONS I-1448).
alter table hosts add column if not exists arch text;
