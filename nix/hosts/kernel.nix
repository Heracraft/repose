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

  boot.kernelParams = [ "transparent_hugepage=madvise" ];

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
