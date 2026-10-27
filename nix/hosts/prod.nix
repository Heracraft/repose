# The production fleet. Every production host imports this file.
#
# host-01.nix and host-02.nix were identical once their comments were
# stripped, and the next host was going to be a third copy of the same
# twelve values. They are the fleet's values, not one machine's: the
# control plane's address, the platform CA, the base repository, the
# operator key, the idle pause, the freeze vault, the Blob account. None of
# it is secret -- the CA and the key are public material and the rest are
# addresses and names -- so all of it can sit in the store.
#
# What stays in a host's own file, and why:
#
#   freeze.writeRateMBs   Measured on that host's own pool device. Disks
#                         differ in size and in provisioned throughput, so
#                         one host's figure predicts nothing about
#                         another's (DECISIONS I-796). Deliberately not set
#                         here: a host that names no figure takes
#                         freeze.nix's floor of 100 and skips guests, which
#                         costs what those guests were running; a host that
#                         inherited a neighbour's 400 would start freezes
#                         its disk cannot finish, and lose the same guests
#                         while believing it saved them. The floor is the
#                         safer default to forget.
#
# What is per host but not in that file: `hostName`, which `nix/flake.nix`
# sets from the attribute name (`lib.mkHost`), and nothing else. A host
# whose file holds only its write rate is the normal shape; a host file
# that would be empty means its write rate is still unmeasured, which is
# `ops/RUNBOOK.md` "Guest freeze" step 3, not a reason to delete the file.
#
# Staging does not import this. host-stg-01 answers to `hostdev` rather
# than the api, on its own address, with its own CA, vault and Blob
# account (DECISIONS I-642), so it shares no line with a production host
# and keeps all of its own in `host-stg-01.nix`.
{ ... }:
{
  repose.host = {
    # The api's gRPC listener on the control VM's VNet address (infra output
    # control_private_ip): a host registers before it has a WireGuard tunnel
    # (DECISIONS I-92). The certificate is issued from the platform CA for
    # api.repose.herakraft.co and this address (GRPC_SERVER_NAMES).
    #
    # This is also the wall a host outside the home VNet runs into: the
    # address is private and Register comes before the tunnel exists, so
    # there is nothing here for a host in another region or at another
    # provider to reach (docs/workstreams/11-infra-opentofu.md, "The
    # portability wall").
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
    # it wakes it. This is what puts `pause` in a host's Hello, so the api
    # pauses nothing on a host without it. The owner asked for it on
    # 2026-10-21, with the mechanism measured on host-stg-01 the same day
    # and the rule's own judgement readable through I-844's counters.
    #
    # It takes effect at a host's next SWITCH, which on a new host is its
    # install; a merge is not a switch. keep_awake is the per-project way
    # out and takes effect at once (I-820), so a project that must never be
    # paused carries it from its Config page or `PATCH /projects/:id`
    # before the host it sits on is switched.
    pauseIdle = true;

    # Guests are frozen to an encrypted image of their memory when a host
    # restarts and restored where they were (DECISIONS I-652, I-653, the
    # owner's exception to the secrets rule, signed off 2026-10-12 and
    # first on for host-01 as I-686). The vault is prod's
    # `freeze_keyvault_name` (`make output ENV=prod`, freeze_keyvault); the
    # key wraps per-image keys only, and the user-assigned host identity
    # below, which every production host shares, holds Get and UnwrapKey on
    # it and nothing else. Azure Scheduled Events stay off: turning that
    # listener on holds every restart of the VM for approval, so it is its
    # own decision per host (I-652).
    #
    # writeRateMBs is per host; see the header.
    freeze = {
      enable = true;
      keyVaultUrl = "https://repose-frz-3912.vault.azure.net/";
      keyName = "freeze-wrap";
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
