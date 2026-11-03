# The host kernel: which one, and the modules, parameters and sysctls a host
# needs before hostd starts.
# The module list is fixed here instead of locking modules (hardening.nix)
# because hostd creates taps and Cloud Hypervisor opens /dev/kvm after boot.
{ config, lib, pkgs, ... }:
let
  textfileDir = "/var/lib/node_exporter/textfile";

  # repose-kernel-check reads back the boot-time facts about a host that
  # nothing else in this repository reads back at all (DECISIONS I-1010).
  #
  # Two paging checks:
  #
  #   large_hash_linear   the `Dentry cache hash table ... linear` line this
  #                       boot printed. A *positive* read of the allocation
  #                       path that has never faulted, which is what
  #                       docs/CHECKLIST.md already asks for -- not the
  #                       absence of a symbol from /proc/vmallocinfo.
  #   cmdline_recognised  no `Unknown kernel command line parameters` line.
  #                       The general guard for every parameter this file
  #                       ever grows, and the exact line that caught I-988
  #                       after a host had been switched AND rebooted for a
  #                       parameter the kernel never took.
  #
  # and one read that no rule looks at, large_hash_vmalloc_absent, because
  # its positive state cannot be produced anywhere we can test: this is the
  # only fleet and no machine here has more than one NUMA node. Letting an
  # unconfirmed read veto a confirmed one would be I-968's shape inside the
  # guard written against I-968.
  #
  # Not here: panic_on_oops, which hostd exports and HostPanicOnOopsOff
  # reads, and a NUMA node count, which nothing alerts on, no dashboard
  # shows and no code reads.
  kernelCheck = pkgs.writeShellApplication {
    name = "repose-kernel-check";
    runtimeInputs = [ pkgs.coreutils pkgs.gnugrep pkgs.systemd ];
    text = ''
      out=${textfileDir}/repose_kernel.prom
      tmp="$out.tmp"
      state=/var/lib/repose/kernel-check.state

      # Every check starts red. A check that is never computed -- because
      # this script died, or journalctl hung, or a grep was added without
      # its assignment -- publishes 0 and pages, rather than publishing
      # nothing: `noDataState: OK` makes an absent series green, so silence
      # is the one failure a textfile writer must not have.
      large_hash_linear=0
      cmdline_recognised=0
      large_hash_vmalloc_absent=0

      publish() {
        {
          echo "# HELP repose_kernel_check 1 when this boot satisfies the named kernel assertion (DECISIONS I-1010)."
          echo "# TYPE repose_kernel_check gauge"
          echo "repose_kernel_check{check=\"large_hash_linear\"} $large_hash_linear"
          echo "repose_kernel_check{check=\"cmdline_recognised\"} $cmdline_recognised"
          echo "repose_kernel_check{check=\"large_hash_vmalloc_absent\"} $large_hash_vmalloc_absent"
        } > "$tmp"
        mv -f "$tmp" "$out"
      }

      # Reds on disk before anything can fail, and again on the way out
      # whatever happens -- a SIGKILL excepted, which is why the first call
      # is here and not only in the trap.
      publish
      trap publish EXIT

      # The two journal reads latch for the life of the boot. nix/hosts/
      # hardening.nix caps the journal at 4G and these lines are printed in
      # the first second of a boot, so a long chatty boot can lose its own
      # head: without the latch a rotated journal turns a red into a green
      # (the Unknown line scrolled away) and a green into a red (the Dentry
      # line did), and both are wrong. First reading of this boot wins.
      boot=$(cat /proc/sys/kernel/random/boot_id)
      sboot=""
      sa=0
      sb=0
      if [ -r "$state" ]; then
        read -r sboot sa sb < "$state" || true
      fi
      if [ "$sboot" = "$boot" ]; then
        large_hash_linear=$sa
        cmdline_recognised=$sb
      else
        # `|| true` on every count: 0 matches is the healthy answer for
        # cmdline_recognised and `grep -c` exits 1 on it, which under
        # writeShellApplication's `set -e` would kill the script before it
        # ever reached the second check.
        klog=$(journalctl -k -b --no-pager 2>/dev/null || true)

        if [ "$(echo "$klog" | grep -c "Dentry cache hash table.*linear" || true)" != 0 ]; then
          large_hash_linear=1
        fi

        # `grep -v "Command line"` first, because the kernel echoes the
        # whole command line into the same log: on host-02 three of the four
        # oops-pattern matches were the command line quoting the parameter
        # and only the fourth was the kernel refusing it (I-988). A phrase
        # that can appear inside the thing being tested is not a test of it.
        if [ "$(echo "$klog" | grep -v "Command line" | grep -c "Unknown kernel command line parameters" || true)" = 0 ]; then
          cmdline_recognised=1
        fi

        printf '%s %s %s\n' "$boot" "$large_hash_linear" "$cmdline_recognised" > "$state.tmp"
        mv -f "$state.tmp" "$state"
      fi

      # Live state, read every run and never latched: /proc/vmallocinfo is
      # now, not a line from boot.
      if [ "$(grep -c alloc_large_system_hash /proc/vmallocinfo || true)" = 0 ]; then
        large_hash_vmalloc_absent=1
      fi
    '';
  };

  # CVE-2026-99999: the first stable kernel carrying the backport of
  # 6785011f8b16, "ipv4: validate checksum_start before completing checksum".
  patchedKernel = "6.18.57";
in
{
  # Not `pkgs.linuxPackages`: the flake pins the host kernel separately until
  # `nixpkgs` reaches patchedKernel (DECISIONS I-778).
  boot.kernelPackages = config.repose.host.kernelPackages;

  # A host's kernel is the guest-to-host boundary, not a dependency like any
  # other. A guest hands the host kernel its own checksum metadata through
  # its tap's `vnet_hdr` (net.go's AddTap) and the host runs the IPv4 output
  # path on the packet (ip_forward below, the forward and masquerade rules in
  # nftables.nix), so before CVE-2026-99999 was fixed any tenant could write
  # past a validated IPv4 header in the host kernel. None of the containment
  # around the hypervisor applies to that path: it is kernel mode on the
  # host, above the unprivileged `hostd` the guest@ unit runs as. This is an
  # assertion rather than a comment because the fix is a version pin, and a
  # pin is removed by whoever is tidying up months later.
  assertions = [
    {
      assertion = lib.versionAtLeast config.boot.kernelPackages.kernel.version patchedKernel;
      message = ''
        Host kernel ${config.boot.kernelPackages.kernel.version} is older than
        ${patchedKernel} and so has no fix for CVE-2026-99999, a guest-to-host
        escape through the tap's checksum metadata (docs/security/cve-2026-99999.md).
        Repoint `repose.host.kernelPackages` at a patched kernel; do not relax
        this assertion to ship a host.
      '';
    }
  ];

  boot.kernelModules = [
    "kvm-intel"
    "vhost_vsock"
    "vhost_net"
    "tun"
    "nf_tables"
    # The conntrack sysctls below need the module before systemd-sysctl
    # runs; nft_connlimit is the per-guest `ct count` cap (DECISIONS I-453).
    "nf_conntrack"
    "nft_connlimit"
    "dm_thin_pool"
    "dm_snapshot"
    "overlay"
    "bridge"
    # hostd's per-guest egress policer on the tap's ingress (DECISIONS
    # I-217) and the HTB with fq_codel on its root shaping what the guest
    # receives (I-451).
    "sch_htb"
    "sch_fq_codel"
    "sch_ingress"
    "cls_flower"
    "act_police"
    "act_gact"
  ];

  boot.kernelParams = [
    "transparent_hugepage=madvise"
    # hashdist=0 keeps the kernel's large system hash tables -- the dentry
    # and inode caches above all -- in the linear map instead of vmalloc.
    #
    # alloc_large_system_hash() defaults hashdist to 1 on any NUMA machine,
    # which allocates those tables with vmalloc_huge() so they spread over
    # the nodes. On 2026-11-02 that killed host-02 (DECISIONS I-982). It is
    # a D64s_v7 with two NUMA nodes and 251 GB, so its dentry table was
    # "16777216 entries (order: 15, 134217728 bytes, vmalloc hugepage)";
    # host-01, a D16s_v7 with one node, got "8388608 entries (order: 14,
    # 67108864 bytes, linear)" and ran the same 6.18.52 and 6.18.57 for two
    # weeks with no kernel fault at all.
    #
    # A vmalloc mapping is per-page-table; the linear map is in every
    # process's page tables by construction. host-02 took three
    # `__d_lookup`/`__d_lookup_rcu` page faults in three unrelated
    # processes -- a virtiofsd vring worker, systemd-udevd, then systemd
    # itself, which panicked the box -- each one a *read of a not-present
    # page*, each reporting `P4D 0`, each from a different PGD and CR3,
    # all at the same intact table base. Nothing was corrupt: the table
    # was simply not mapped in the faulting process. The third fault read
    # four bytes below a 4 MB-aligned address, i.e. backwards across a
    # hugepage boundary.
    #
    # So this is not a tuning choice. It returns the fleet to the
    # allocation path that has never faulted, and it costs only NUMA
    # locality on a table we read far more than we place.
    "hashdist=0"
    # An oops is not survivable, so stop pretending it is (DECISIONS
    # I-984). The knob is the sysctl below, NOT a kernel parameter:
    # `panic_on_oops=1` on the command line is not a kernel parameter at
    # all, and the kernel says so and hands it to userspace as an
    # environment variable -- "Unknown kernel command line parameters
    # "panic_on_oops=1", will be passed to user space" in host-02's dmesg
    # on 2026-11-02, with `kernel.panic_on_oops` still 0 after the reboot
    # that was supposed to set it (DECISIONS I-988). `panic=30` below IS a
    # kernel parameter and did take.
    #
    # On 2026-11-02 host-02 took its first `__d_lookup_rcu` fault
    # at 13:51:07 and kept running for 3 h 48 m, serving tenant filesystem
    # traffic through virtiofsd with a dentry cache it could not read,
    # before a third fault hit systemd and the box hard-hung with no way
    # back but the Azure portal. Rebooting at the first oops would have
    # cost minutes: hostd marks every interrupted guest on its next start
    # and the api brings them back three per host per minute (I-645).
    # Staying up preserves nothing, because we capture no vmcore -- there
    # is no kdump here and `boot.crashDump` is unset -- so the serial log
    # is all a long tainted run buys, and it is written by the first oops
    # anyway.
    # 30 s, not 0: the oops reaches the serial console and fluent-bit
    # ships it to Loki before the box goes. The panic line itself never
    # ships -- fluent-bit dies with everything else -- which is why the
    # alert matches on oops and not on panic.
    "panic=30"
  ];

  boot.kernel.sysctl = {
    # Panic on an oops rather than running tainted (DECISIONS I-984, and
    # I-988 for why it is here and not in kernelParams). A sysctl also
    # takes effect on a switch, so this needs no reboot of its own, and
    # `sysctl kernel.panic_on_oops` says whether it is really on -- which
    # the command line could not.
    "kernel.panic_on_oops" = 1;
    "net.ipv4.ip_forward" = 1;
    "fs.inotify.max_user_instances" = 8192;
    # Cloud Hypervisor mmaps guest RAM; the host must not refuse it.
    "vm.overcommit_memory" = 1;
    # sshd and node_exporter bind the wg0 address before wg-quick brings
    # the interface up (network.nix renders both from host.json).
    "net.ipv4.ip_nonlocal_bind" = 1;
    # One conntrack table serves every guest (DECISIONS I-453): sized so
    # the most guests a host holds, each at its cap in nftables.nix
    # (guestEgress.maxConnections), still leave room for the host's own.
    # An idle established TCP entry lasts a day instead of five, as
    # kube-proxy sets it; SSH and agents' connections keep alive well
    # inside that.
    "net.netfilter.nf_conntrack_max" = 1048576;
    "net.netfilter.nf_conntrack_tcp_timeout_established" = 86400;
    # Bridged guest frames are filtered by the nftables `bridge` family
    # table, not by iptables hooks; keep the bridge from calling into them.
    "net.bridge.bridge-nf-call-iptables" = 0;
    "net.bridge.bridge-nf-call-ip6tables" = 0;
    "net.bridge.bridge-nf-call-arptables" = 0;
  };

  systemd.tmpfiles.rules = [
    # Both also created elsewhere (storage.nix for the textfile directory,
    # hostd.nix for /var/lib/repose), and repeated here on purpose: this
    # writer must not fail because a host was built without the thin pool
    # or without hostd.
    "d /var/lib/node_exporter 0755 root root -"
    "d ${textfileDir} 0755 root root -"
    "d /var/lib/repose 0755 root root -"
  ];

  systemd.services.repose-kernel-check = {
    description = "Read back this boot's kernel assertions for node_exporter";
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "${kernelCheck}/bin/repose-kernel-check";
    };
  };

  systemd.timers.repose-kernel-check = {
    description = "Read back this boot's kernel assertions every minute";
    wantedBy = [ "timers.target" ];
    timerConfig = {
      # 1 min after boot, then every minute: HostKernelAssertFailed treats a
      # file older than an hour as a failure, so the write has to be far
      # more frequent than that for a stale file to mean the writer stopped
      # rather than that the timer is slow.
      OnBootSec = "1min";
      OnUnitActiveSec = "1min";
      Unit = "repose-kernel-check.service";
    };
  };

  # virtiofsd runs unprivileged in a user namespace (`--sandbox namespace`).
  # The workstream doc names the Debian-only `kernel.unprivileged_userns_clone`
  # sysctl; on the NixOS kernel the equivalent is this option.
  security.allowUserNamespaces = true;
}
