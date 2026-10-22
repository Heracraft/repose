# host-01: the first production host (DECISIONS I-14, I-39), registered with
# the api since M2 (I-92). This file holds only what makes host-01 differ
# from the generic `host` configuration, and none of it is secret: the
# control plane's VNet address, the platform CA certificate, an operator
# public key, the platform repository and the Blob endpoint and identity the
# host snapshots with.
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

    # Guests are frozen to an encrypted image of their memory when this host
    # restarts and restored where they were (DECISIONS I-652, I-653); the
    # The idle pause is on here (DECISIONS I-846): a machine nobody is
    # using has its vCPUs stopped and its memory left resident, and
    # anything that reaches it wakes it. This is what puts `pause` in this
    # host's Hello, so the api pauses nothing here without it. The owner
    # asked for it on 2026-10-21, with the mechanism measured on
    # host-stg-01 the same day and the rule's own judgement on this fleet
    # readable first through I-844's counters.
    #
    # keep_awake is the per-project way out and takes effect at once
    # (I-820): set it on any project that must not be frozen before this
    # host is SWITCHED, from its Config page or `PATCH /projects/:id`. A
    # merge is not a switch -- this line does nothing until host-01 takes
    # a new configuration.
    pauseIdle = true;

    # owner signed off I-653's exception to the secrets rule on 2026-10-12
    # and host-01 is the first host with it on (I-686). The vault is prod's
    # `freeze_keyvault_name` (`make output ENV=prod`, freeze_keyvault); the
    # host identity below holds Get and UnwrapKey on it, and nothing else.
    # Azure Scheduled Events stay off: turning that listener on holds every
    # restart of this VM for approval, so it is its own decision (I-652).
    freeze = {
      enable = true;
      keyVaultUrl = "https://repose-frz-3912.vault.azure.net/";
      keyName = "freeze-wrap";
      # Measured on the disk the images are written to, this host's data
      # disk (nvme1n1, the PV of vg-guests), on 2026-10-18 with two
      # guests running: a scratch thin volume written 2 GiB at a time
      # with `dd oflag=direct` took 450 and 460 MB/s, 8 GiB in one go
      # took 456 MB/s (so it is not a burst), four at once took 197 MB/s
      # each (786 MB/s together), and it reads back at 484 MB/s. The
      # disk takes no host cache (Premium SSD v2) and prod.tfvars
      # provisions it at 600 MB/s. 400 is the single-stream figure
      # rounded down (DECISIONS I-796).
      #
      # The root disk, where the earlier figure came from, does
      # 126 MB/s: under a third of this, and not where any image goes.
      # Re-measure on the pool whenever a disk changes (RUNBOOK "Guest
      # freeze").
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
