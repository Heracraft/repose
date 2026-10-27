# host-02: the second production host, a Standard_D64s_v7 with a 2048 GB
# data disk, installed beside host-01 rather than resizing host-01 in
# place: a resize stops every guest on that host, and host-01 carries live
# tenant machines including the conductor's own. host-01 is retired
# afterwards by moving its projects here (docs/ops/LAUNCH.md section 3).
# It registers with the api the way host-01 did (DECISIONS I-92).
#
# Everything it shares with the fleet is `prod.nix`; what is left here is
# the one value that is this host's own, and it is not measured yet.
{ ... }:
{
  imports = [ ./prod.nix ];

  # INHERITED AND UNMEASURED: 400 is host-01's figure (DECISIONS I-796),
  # and host-02's data disk is four times the size at a different
  # provisioned throughput, so nothing about host-01's disk predicts this
  # one. Measure it on host-02's own pool device (the PV of vg-guests, the
  # data disk) by the method host-01.nix records -- a scratch thin volume
  # written with `dd oflag=direct`, 2 GiB and 8 GiB single-stream to rule
  # out a burst, four at once, and a read back -- and put the single-stream
  # figure, rounded down, here (RUNBOOK "Guest freeze" step 3).
  #
  # Too high is a loss, not a slow freeze: the deadline is a hard cap,
  # snapshots are admitted against the memory this rate says fits inside
  # it, so a rate the disk cannot hold has the freeze start more than it
  # can finish, and the guest memory still unwritten when the deadline
  # passes is gone on the restart.
  repose.host.freeze.writeRateMBs = 400;
}
