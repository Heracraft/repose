# Resources for the keystroke path (DECISIONS I-576; I-200, I-494).
#
# What a user needs to type into a machine whose memory or CPU is full:
# sshd and the SSH session (sshd-session, the `tmux attach` client), the
# tmux server, dev's user manager (its stop ends the tmux server and every
# pane), guestd, journald, logind and dbus. On kanali, 2026-10-05,
# page-cache thrash with no OOM kill hung journald for 3 minutes and
# guestd's tmux calls for 15, and the OOM kills that followed took dev's
# user manager (oom_score_adj 100) ahead of a 319 MB claude (0), which ended
# the tmux server and every pane.
#
# Four parts:
#   - CPU (I-494). The SSH session scopes and the tmux server run at
#     CPUWeight=1000 against a pane's 100. Nothing is capped or reserved.
#   - oom_score_adj. Upstream runs the user manager at 100 and its units
#     at 200 (manager + 100), which the kernel scores as 1 and 2 GB of extra
#     memory on a large machine: a 128 kB shell at 200 went before a 300 MB
#     process at 0. The user manager runs at -900, its units at 0, sshd's
#     sessions at -800 (sshd gives its sessions the unit's value). guestd
#     keeps the tmux server, the attach clients and dev's sshd-session at
#     -800 and puts every other process of dev's that inherited a negative
#     value back to 0, so panes and what they start are ranked by size
#     (internal/guestd/sample/oom.go).
#   - MemoryLow. The kernel reclaims these cgroups' pages last, so typing
#     does not wait on the disk to read tmux, sshd or bash back in. The
#     budget is the same on every size: these processes use about the
#     same memory on 4 GB as on 16 GB (kanali: tmux server 4 MB, sshd 6,
#     guestd 21, journald 43). It is soft protection, so it never causes
#     an OOM kill by itself, and only memory a cgroup uses counts. The
#     cgroup tree is mounted with memory_recursiveprot and a child's
#     protection is capped by its parent's, so each slice carries the sum
#     of what is protected below it. 304 MB in all, under 8 percent of
#     small.
#   - Thrash prevention. MGLRU's min_ttl_ms makes the kernel run its OOM
#     killer when it cannot keep the pages used in the last second in
#     memory, instead of evicting them and thrashing for minutes. The
#     kernel's choice follows the oom_score_adj above.
{ config, lib, pkgs, ... }:
let
  # MemoryLow budgets (I-576).
  low = {
    sshd = "16M";
    guestd = "64M";
    journald = "64M";
    logind = "16M";
    dbus = "16M";
    system = "176M"; # sshd + guestd + journald + logind + dbus
    # Per SSH connection (session-N.scope): sshd-session twice, the
    # attach client and a shell.
    session = "32M";
    tmux = "48M"; # the tmux server: its scrollback is its own memory
    userManager = "64M"; # user@1000.service: the tmux server and the manager
    userSlice = "128M"; # user@1000.service + two SSH connections
  };
in
{
  # dev's user manager: the last OOM victim. Its stop ends every unit and
  # scope under it, the tmux server and every pane with them. NixOS
  # already gives user@.service restartIfChanged = false, so a switch
  # never restarts it; its oom_score_adj comes from this at the machine's
  # next start, and guestd writes it to the running manager meanwhile.
  systemd.services."user@".serviceConfig = {
    OOMScoreAdjust = -900;
    MemoryLow = low.userManager;
  };
  # Upstream's default for a user manager's units is its own value plus
  # 100, which with the manager at -900 would protect every user unit and
  # everything it forks. 0: user units are ranked by size, like panes.
  systemd.user.settings.Manager.DefaultOOMScoreAdjust = 0;

  # sshd hands its own unit's value to every session it forks (OpenSSH's
  # oom_adjust_restore; the listener itself runs at -1000). Shells and
  # commands in the session inherit it, and guestd puts them back to 0.
  systemd.services.sshd.serviceConfig = {
    OOMScoreAdjust = -800;
    MemoryLow = low.sshd;
  };

  # Each SSH connection, with its sshd and the `tmux attach` client, is a
  # logind session scope; the CPU weight keeps the keystrokes moving while
  # user@1000.service's panes hold every core (DECISIONS I-494). A prefix
  # drop-in, so it reaches the transient session-N.scope units.
  systemd.units."session-.scope" = {
    overrideStrategy = "asDropin";
    text = ''
      [Scope]
      CPUWeight=1000
      MemoryLow=${low.session}
    '';
  };

  # journald hung for 3 minutes under thrash and was killed by its
  # watchdog, twice on 2026-10-05 ("19.8G read from disk" over 23 hours
  # with a 40 MB peak); its journal files are page cache in its own
  # cgroup. Its oom_score_adj stays upstream's -250. guestd's own is in
  # guestd.nix.
  systemd.services.systemd-journald.serviceConfig.MemoryLow = low.journald;
  systemd.services.systemd-logind.serviceConfig.MemoryLow = low.logind;
  systemd.services.dbus-broker.serviceConfig.MemoryLow =
    lib.mkIf (config.services.dbus.implementation == "broker") low.dbus;
  systemd.services.guestd.serviceConfig.MemoryLow = low.guestd;

  systemd.slices.system.sliceConfig.MemoryLow = low.system;
  systemd.slices.user.sliceConfig.MemoryLow = low.userSlice;
  # Every user-UID.slice; dev's is the only one with a login.
  systemd.units."user-.slice" = {
    overrideStrategy = "asDropin";
    text = ''
      [Slice]
      MemoryLow=${low.userSlice}
    '';
  };

  # The tmux server, in app.slice of dev's user manager. herdr's server
  # unit gets no MemoryLow: herdr's panes share its cgroup, so the
  # protection would cover their builds.
  systemd.user.units."app.slice" = {
    overrideStrategy = "asDropin";
    text = ''
      [Slice]
      MemoryLow=${low.tmux}
    '';
  };
  systemd.user.services.repose-tmux-session.serviceConfig = {
    MemoryLow = low.tmux;
    # The unit's cgroup holds the server and its #() and run-shell jobs.
    # An OOM kill of a job must not stop the unit, which ends the server
    # and every pane (KillMode=control-group); the default is stop.
    OOMPolicy = "continue";
  };

  # Thrash prevention (Documentation/admin-guide/mm/multigen_lru.rst,
  # "Thrashing prevention"): 1000 ms is the value the kernel docs name for
  # removing intolerable lag. A kernel without MGLRU has no such file and
  # the line does nothing. systemd-tmpfiles-resetup applies it on a live
  # switch.
  systemd.tmpfiles.rules = [
    "w- /sys/kernel/mm/lru_gen/min_ttl_ms - - - - 1000"
  ];
}
