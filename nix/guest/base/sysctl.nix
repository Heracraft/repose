# Kernel tunables for agents that watch large trees and builds that spike.
# zram gives a build that briefly exceeds RAM somewhere to go other than the
# OOM killer picking the agent.
{ config, lib, pkgs, ... }:
{
  boot.kernel.sysctl = {
    "fs.inotify.max_user_watches" = 1048576;
    "fs.inotify.max_user_instances" = 1024;
    "fs.file-max" = 2097152;
    "net.core.somaxconn" = 4096;
    "vm.swappiness" = 10;
    # Any process of dev's may attach to another of dev's (`strace -p`,
    # `gdb -p`, py-spy). The guest has one user, who has passwordless
    # sudo, and the VM is the boundary; Landlock (Codex) and the bwrap PID
    # namespaces still confine the agents. yama stays loaded (boot.nix,
    # I-231) so the knob exists (DECISIONS I-539).
    "kernel.yama.ptrace_scope" = 0;
  };

  # Open files: 524288 soft as well as hard for the tmux session, its panes
  # and every user service (the user manager's soft default was 1024, and
  # Python in a pane failed with EMFILE near 1021 while the agents' own
  # shells had 524288), and for SSH, `repose exec` and `repose code`
  # sessions through PAM. System daemons keep systemd's defaults
  # (DECISIONS I-538).
  systemd.user.settings.Manager.DefaultLimitNOFILE = "524288:524288";
  security.pam.loginLimits = [
    { domain = "dev"; type = "soft"; item = "nofile"; value = "524288"; }
  ];

  # The same swap zramSwap made (zstd, half the RAM up to 2 GiB, priority 5),
  # set up by a unit of ours instead of zram-generator's (DECISIONS I-231).
  # The generator's swap is part of swap.target, every tmpfs mount is
  # ordered after swap.target, and so /run/wrappers, local-fs.target,
  # sysinit.target and everything after them waited for the zram device to
  # appear, be formatted and be swapped on: 0.6 s of every boot on the dev
  # box. Nothing needs swap in the first seconds of a boot.
  systemd.services.repose-zram-swap = {
    description = "repose: compressed swap on zram";
    wantedBy = [ "multi-user.target" ];
    unitConfig.DefaultDependencies = false;
    after = [ "local-fs.target" ];
    before = [ "shutdown.target" ];
    conflicts = [ "shutdown.target" ];
    path = [ pkgs.util-linux pkgs.kmod pkgs.gawk ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
    };
    script = ''
      if grep -q '^/dev/zram0 ' /proc/swaps; then
        exit 0
      fi
      modprobe zram
      ram=$(awk '/^MemTotal:/ { print $2 * 1024 }' /proc/meminfo)
      size=$((ram / 2))
      max=$((2 * 1024 * 1024 * 1024))
      [ "$size" -le "$max" ] || size=$max
      zramctl --algorithm zstd --size "$size" /dev/zram0
      mkswap -L zram0 /dev/zram0 >/dev/null
      swapon --priority 5 --discard /dev/zram0
    '';
    preStop = ''
      swapoff /dev/zram0 || true
    '';
  };
}
