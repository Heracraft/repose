-- No-Nix guests (DECISIONS I-1462, proposal 2026-11-22 Surface 5). A base
-- row is now one of two formats: 'nix' (a nix/ git revision every host
-- builds from, as before) or 'erofs' (an Ubuntu image release: a
-- rootfs.erofs, a kernel and an initramfs per architecture, each with its
-- SHA-256). An erofs row needs all three amd64 files; arm64 is all three
-- or none, because the Mac path publishes it later.
alter table base_versions
  add column format text not null default 'nix',
  add column image_amd64_url text,
  add column image_amd64_sha256 text,
  add column image_arm64_url text,
  add column image_arm64_sha256 text,
  add column kernel_amd64_url text,
  add column kernel_amd64_sha256 text,
  add column initramfs_amd64_url text,
  add column initramfs_amd64_sha256 text,
  add column kernel_arm64_url text,
  add column kernel_arm64_sha256 text,
  add column initramfs_arm64_url text,
  add column initramfs_arm64_sha256 text,
  add column kernel_version text,
  add column manifest_url text;
alter table base_versions add constraint base_versions_format_check check (format in ('nix', 'erofs'));
alter table base_versions add constraint base_versions_erofs_amd64_check check (
  format <> 'erofs' or (image_amd64_url is not null and image_amd64_sha256 is not null
                        and kernel_amd64_url is not null and kernel_amd64_sha256 is not null
                        and initramfs_amd64_url is not null and initramfs_amd64_sha256 is not null));
alter table base_versions add constraint base_versions_arm64_check check (
  (image_arm64_url is null and image_arm64_sha256 is null and kernel_arm64_url is null
   and kernel_arm64_sha256 is null and initramfs_arm64_url is null and initramfs_arm64_sha256 is null)
  or (image_arm64_url is not null and image_arm64_sha256 is not null and kernel_arm64_url is not null
      and kernel_arm64_sha256 is not null and initramfs_arm64_url is not null and initramfs_arm64_sha256 is not null));
create index base_versions_format_released on base_versions (format, released_at desc);

-- What a project's guest boots, fixed at create (a restore copies its
-- source's): 'nix' as every project before this, or 'erofs'. A project's
-- runtime never changes in place; the volume layouts differ.
-- pending_base_version is the erofs base the next start boots, set by the
-- base bump and cleared when a start boots it; null when there is none.
alter table projects
  add column guest_runtime text not null default 'nix',
  add column pending_base_version text;
alter table projects add constraint projects_guest_runtime_check check (guest_runtime in ('nix', 'erofs'));

-- The base the guest ran when the snapshot was taken, so a restore knows
-- which vda produced it. Null for every snapshot from before this.
alter table snapshots add column base_version text;

-- settings.default_guest_runtime is read at project create; absent means
-- 'nix'. No row is written here: the flip to erofs is an operator's
-- `repose-admin settings set default_guest_runtime erofs` on the target
-- environment, not a code default.
