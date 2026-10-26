# host-01: the first production host (DECISIONS I-14, I-39), registered with
# the api since M2 (I-92), carrying live tenant machines. Everything it has
# in common with the rest of the fleet is `prod.nix`; what is left here is
# what host-01 measured on its own disk.
{ ... }:
{
  imports = [ ./prod.nix ];

  # Measured on the disk the images are written to, this host's data disk
  # (nvme1n1, the PV of vg-guests), on 2026-10-18 with two guests running:
  # a scratch thin volume written 2 GiB at a time with `dd oflag=direct`
  # took 450 and 460 MB/s, 8 GiB in one go took 456 MB/s (so it is not a
  # burst), four at once took 197 MB/s each (786 MB/s together), and it
  # reads back at 484 MB/s. The disk takes no host cache (Premium SSD v2)
  # and prod.tfvars provisions it at 600 MB/s. 400 is the single-stream
  # figure rounded down (DECISIONS I-796).
  #
  # The root disk, where the earlier figure came from, does 126 MB/s: under
  # a third of this, and not where any image goes. Re-measure on the pool
  # whenever a disk changes (RUNBOOK "Guest freeze").
  repose.host.freeze.writeRateMBs = 400;
}
