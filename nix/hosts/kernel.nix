# The host kernel: which one, and the modules, parameters and sysctls a host
# needs before hostd starts.
# The module list is fixed here instead of locking modules (hardening.nix)
# because hostd creates taps and Cloud Hypervisor opens /dev/kvm after boot.
{ config, lib, ... }:
let
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
  ];

  boot.kernel.sysctl = {
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

  # virtiofsd runs unprivileged in a user namespace (`--sandbox namespace`).
  # The workstream doc names the Debian-only `kernel.unprivileged_userns_clone`
  # sysctl; on the NixOS kernel the equivalent is this option.
  security.allowUserNamespaces = true;
}
