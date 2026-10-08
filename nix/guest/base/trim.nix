# The guest's volume gives deleted files' blocks back to the host's thin
# pool at most a day late, and at once when the machine stops (DECISIONS
# I-585). The plan's disk counts the bytes a volume holds, which is the
# thin volume's allocated blocks, and a deleted file keeps its blocks
# until a trim: NixOS's weekly fstrim left a user who deleted files
# counted at the old figure for up to a week (kanali's weekly trim gave
# back 13.4 GiB, I-567). `discard` on the mount was not taken: it trims on
# every delete, which slows the big deletes agents run (node_modules, a
# store GC). A trim of 7.1 GiB freed over three days took 8.3 s on
# kanali; a second run right after, 0.34 s.
{ pkgs, ... }:
{
  services.fstrim.enable = true;
  services.fstrim.interval = "daily";

  # Its stop runs at shutdown, so a stopped project is counted at what it
  # holds. hostd snapshots before the shutdown (I-404), and the snapshot
  # reads the filesystem's used blocks (I-164), so the trim changes
  # neither. hostd waits StopTimeoutS (60 s) for the guest; past 20 s the
  # trim is cut short and the daily one finishes it.
  systemd.services.repose-trim-on-stop = {
    description = "repose: trim the volume when the machine stops";
    wantedBy = [ "multi-user.target" ];
    after = [ "local-fs.target" ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = "${pkgs.coreutils}/bin/true";
      ExecStop = "${pkgs.util-linux}/bin/fstrim /";
      TimeoutStopSec = "20s";
    };
  };
}
