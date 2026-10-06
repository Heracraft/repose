# One interface, eth0, static address from the kernel command line
# (`ip=<guest>::<gateway>:255.255.252.0:<hostname>:eth0:off`, set by the runner from
# hostd's arguments). systemd-network-generator turns that line into a
# .network unit at boot, so nothing per guest is baked into the closure.
# DNS is public resolvers through systemd-resolved: guests cannot reach the
# host, so there is no host resolver to point at. resolved's stub also
# listens on 172.20.0.1, Docker's default bridge, and every container uses
# it there (docker.nix, DECISIONS I-537). No guest firewall: the host enforces policy, and a
# second firewall in the guest would only confuse `repose open`.
{ config, lib, ... }:
{
  networking.useNetworkd = true;
  networking.useDHCP = false;
  networking.usePredictableInterfaceNames = false;
  networking.nameservers = [ "1.1.1.1" "8.8.8.8" ];
  networking.firewall.enable = false;
  networking.nftables.enable = false;
  # No link-local name resolution: a guest has one routed interface and
  # no neighbours to ask, and resolved's LLMNR responder listened on
  # 0.0.0.0:5355, which auto-forward then showed in every status bar
  # (DECISIONS I-215). mDNS likewise: it listened on 0.0.0.0:5353 and
  # answered on docker0 and every container's veth. The CLI also skips
  # 5355 and 5353 for older bases.
  # DNSStubListenerExtra: resolved binds with IP_FREEBIND, so the address
  # needs no docker0 yet; a container on the default bridge reaches it as
  # its gateway, and dockerd's embedded resolver from the guest's side.
  services.resolved.settings.Resolve = {
    LLMNR = "false";
    MulticastDNS = "false";
    DNSStubListenerExtra = "172.20.0.1";
  };

  # systemd-network-generator reads ip= from /proc/cmdline into
  # /run/systemd/network/71-eth0.network. NixOS ships that unit only for the
  # initrd, so stage 2 gets the upstream unit here and pulls it into
  # sysinit.target (as a drop-in on the upstream file, not a replacement).
  systemd.additionalUpstreamSystemUnits = [ "systemd-network-generator.service" ];
  systemd.services.systemd-network-generator.wantedBy = [ "sysinit.target" ];
  systemd.network.wait-online.enable = false;

  # Docker needs forwarding for container egress; the host's nftables decide
  # where guest traffic may go.
  boot.kernel.sysctl."net.ipv4.ip_forward" = 1;
}
