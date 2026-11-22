delete from settings where key = 'default_guest_runtime';
alter table snapshots drop column if exists base_version;
alter table projects drop constraint if exists projects_guest_runtime_check;
alter table projects drop column if exists pending_base_version, drop column if exists guest_runtime;
drop index if exists base_versions_format_released;
-- An erofs row has no nix_rev a Nix host can build; a down that kept it
-- would hand every Nix project a base it cannot use.
delete from base_versions where format = 'erofs';
alter table base_versions drop constraint if exists base_versions_arm64_check;
alter table base_versions drop constraint if exists base_versions_erofs_amd64_check;
alter table base_versions drop constraint if exists base_versions_format_check;
alter table base_versions
  drop column if exists manifest_url,
  drop column if exists kernel_version,
  drop column if exists initramfs_arm64_sha256,
  drop column if exists initramfs_arm64_url,
  drop column if exists kernel_arm64_sha256,
  drop column if exists kernel_arm64_url,
  drop column if exists initramfs_amd64_sha256,
  drop column if exists initramfs_amd64_url,
  drop column if exists kernel_amd64_sha256,
  drop column if exists kernel_amd64_url,
  drop column if exists image_arm64_sha256,
  drop column if exists image_arm64_url,
  drop column if exists image_amd64_sha256,
  drop column if exists image_amd64_url,
  drop column if exists format;
