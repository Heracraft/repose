# vg-guests' metadata profile (vgchange --metadataprofile repose-thin):
# grow the pool by 5 percent into the VG headroom when it passes 80
# percent full (DECISIONS I-643, storage.nix). A profile and not lvm.conf,
# so a machine's own volume groups keep their settings.
activation {
  thin_pool_autoextend_threshold = 80
  thin_pool_autoextend_percent = 5
}
