# host-stg-01: the staging host (infra/README.md, "Staging"). It exists to
# prove host-level changes — pool activation, reboot recovery, kernel
# hardening — on a machine whose guests nobody paid for, before host-01 is
# switched. Like host-01.nix this file holds only what differs from the
# generic `host`, and none of it is secret.
{ ... }:
{
  repose.host = {
    # `hostdev`, not the api (DECISIONS I-642). Staging has no control plane
    # of its own (`coolify_count = 0`), and it cannot borrow production's:
    # the api listens on 10.200.3.4:8443 inside prod's VNet, nsg-control
    # opens no 8443 to the internet, and both environments use
    # 10.200.0.0/16, so the two VNets cannot be peered. So the api side of
    # grpc-hostd.md is played by `hostdev` (DECISIONS I-17) on the staging
    # edge, at its address in snet-edge, reached privately inside staging's
    # own VNet. 8444 rather than 8443 because the edge's gateway already
    # listens on 8443 for hook ingest.
    apiAddr = "10.200.2.4:8444";

    # The CA `hostdev init` generated, kept in ~/repose-ws/staging-hostdev on
    # the dev box so a re-created environment reuses it and this file stays
    # true. Public material; its key never leaves the edge.
    apiCA = ''
      -----BEGIN CERTIFICATE-----
      MIIBaTCCAQ+gAwIBAgIBATAKBggqhkjOPQQDAjAcMRowGAYDVQQDExFyZXBvc2Ug
      aG9zdGRldiBDQTAeFw0yNjEwMTExNjQ0MzNaFw0zNjEwMDgxNzQ0MzNaMBwxGjAY
      BgNVBAMTEXJlcG9zZSBob3N0ZGV2IENBMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcD
      QgAERdYW+SktkneQG7U4NQxROsi1kbDK66cazb5Va9+gIoOfr1o8bJlZp4AFchvO
      UCsUJGaXfkIyHU5L+V5geGWpzaNCMEAwDgYDVR0PAQH/BAQDAgKEMA8GA1UdEwEB
      /wQFMAMBAf8wHQYDVR0OBBYEFJlszoi3Vr+HsVVyCNeonk1PKsbCMAoGCCqGSM49
      BAMCA0gAMEUCIBkmx5ZR8ZagjgO5sfvYlLQ8xn+xDlao6fDyJmz8ZsMoAiEA0qWR
      qmTWEImd0c2ImKPppVcil5zZvWJYxSINlccCF6k=
      -----END CERTIFICATE-----
    '';

    # The same public repository hostd clones base checkouts from (I-28).
    baseRepo.url = "https://github.com/Heracraft/repose.git";

    # Operator SSH on the provider NIC: the installer's checks and the
    # join-token delivery reach the host through the edge over the VNet, and
    # a host registered by hostdev has no WireGuard address to bind instead
    # (DECISIONS I-40), so this stays on for the life of the environment.
    bootstrap.enable = true;
    bootstrap.authorizedKeys = [
      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEnlaWLqhKDXwKRp74FWyHFmrK5eZXh7U3VL3a3J/EO0 operator"
    ];

    # Staging's own account and identity (`make output ENV=staging`), so a
    # snapshot taken here can never land beside a tenant's.
    snapshots = {
      blobUrl = "https://reposesnapstaging3912.blob.core.windows.net";
      container = "repose-snapshots";
      identityClientId = "0fc60aaf-0f6d-40c9-baa9-f58d20612580";
    };
  };
}
