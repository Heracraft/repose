# host-02: the second production host, a Standard_D64s_v7 with a 2048 GB data
# disk, installed beside host-01 rather than resizing host-01 in place: a
# resize stops every guest on that host, and host-01 carries live tenant
# machines including the conductor's own. host-01 is retired afterwards by
# moving its projects here. It registers with the api the way host-01 did
# (DECISIONS I-92).
#
# Like host-01.nix this file holds only what differs from the generic `host`
# configuration, and none of it is secret. Every value below is host-01's,
# because the control plane, the platform CA, the base repository, the
# operator key, the idle pause and the freeze vault are the fleet's, not one
# host's. The exception is the freeze write rate, which is inherited
# unmeasured; the comment there says what to do about that.
{ ... }:
{
  repose.host = {
    # The api's gRPC listener on the control VM's VNet address (infra output
    # control_private_ip): a host registers before it has a WireGuard tunnel
    # (DECISIONS I-92). The certificate is issued from the platform CA for
    # api.repose.herakraft.co and this address (GRPC_SERVER_NAMES).
    apiAddr = "10.200.3.4:8443";
    apiServerName = "api.repose.herakraft.co";
    # The platform x509 host CA (`repose-admin ca show`); public material.
    apiCA = ''
      -----BEGIN CERTIFICATE-----
      MIIBYzCCAQmgAwIBAgIBATAKBggqhkjOPQQDAjAZMRcwFQYDVQQDEw5yZXBvc2Ug
      aG9zdCBjYTAeFw0yNjA5MjAxNDM1NDRaFw0zNjA5MTcxNTM1NDRaMBkxFzAVBgNV
      BAMTDnJlcG9zZSBob3N0IGNhMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEEf0Z
      a+ShTl8C17TyIihGfxGA8/+LPSztZO7U+1jkulN/0PyP+u0mFtoz1No7eNIsLQdI
      4XqvAoE1SQ5NJMb9+6NCMEAwDgYDVR0PAQH/BAQDAgKEMA8GA1UdEwEB/wQFMAMB
      Af8wHQYDVR0OBBYEFJ3mh/uToG0kXZreInW+yRCV7Vv0MAoGCCqGSM49BAMCA0gA
      MEUCIQD4CrivKkoYjKlkHwnK7Ors2lohLv4cFQ+yUU5IhKmIUgIgRiWHt/xsWPzy
      pgFYxi/RgOzA9GSronavaaOscYi+Vng=
      -----END CERTIFICATE-----
    '';

    # hostd clones the base checkout it builds fragments from (I-28); the
    # repository is public.
    baseRepo.url = "https://github.com/Heracraft/repose.git";

    # Operator SSH on the provider NIC with a plain key until the host is
    # registered: the installer's checks and the join-token delivery reach
    # the host over the VNet through the edge; once host.json carries a
    # WireGuard address sshd binds that alone (I-92).
    bootstrap.enable = true;
    bootstrap.authorizedKeys = [
      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEnlaWLqhKDXwKRp74FWyHFmrK5eZXh7U3VL3a3J/EO0 operator"
    ];

    # The idle pause (DECISIONS I-846): a machine nobody is using has its
    # vCPUs stopped and its memory left resident, and anything that reaches
    # it wakes it. This is what puts `pause` in this host's Hello, so the
    # api pauses nothing here without it. It is on from the first boot,
    # because the install and the switch are the same event on a new host.
    #
    # keep_awake is the per-project way out and takes effect at once
    # (I-820): a project moved here from host-01 that must not be paused
    # carries it from its Config page or `PATCH /projects/:id`.
    pauseIdle = true;

    # Guests are frozen to an encrypted image of their memory when this host
    # restarts and restored where they were (DECISIONS I-652, I-653, the
    # owner's exception to the secrets rule). The vault is prod's
    # `freeze_keyvault_name` (`make output ENV=prod`, freeze_keyvault), the
    # same one host-01 uses: the key wraps per-image keys only, and the
    # user-assigned host identity below, which every prod host shares,
    # already holds Get and UnwrapKey on it and nothing else. Azure
    # Scheduled Events stay off here too (I-652).
    freeze = {
      enable = true;
      keyVaultUrl = "https://repose-frz-3912.vault.azure.net/";
      keyName = "freeze-wrap";
      # 400 is host-01's measured figure (DECISIONS I-796), carried here
      # INHERITED AND UNMEASURED: host-02 does not exist yet, and its data
      # disk is a different size and a different provisioned throughput
      # from host-01's, so nothing about host-01's disk predicts this one.
      # Measure it on host-02's own pool device (the PV of vg-guests, the
      # data disk) by the method host-01.nix records -- a scratch thin
      # volume written with `dd oflag=direct`, 2 GiB and 8 GiB
      # single-stream to rule out a burst, four at once, and a read back --
      # and put the single-stream figure, rounded down, here (RUNBOOK
      # "Guest freeze").
      #
      # Too high is a loss, not a slow freeze: the deadline is a hard cap,
      # snapshots are admitted against the memory this rate says fits
      # inside it, so a rate the disk cannot hold has the freeze start more
      # than it can finish, and the guest memory still unwritten when the
      # deadline passes is gone on the restart.
      writeRateMBs = 400;
    };

    # infra outputs `snapshots.blob_endpoint` and `snapshots.identity_client_id`
    # (the user-assigned identity id-repose-host, Storage Blob Data
    # Contributor on the container).
    snapshots = {
      blobUrl = "https://reposesnapshots3912.blob.core.windows.net";
      container = "repose-snapshots";
      identityClientId = "072397e1-f6f0-4d0f-a3f0-f8d35be6d2f3";
    };
  };
}
