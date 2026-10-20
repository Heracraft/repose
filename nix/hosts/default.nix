# NixOS configuration for a repose host: a machine that runs tenants' guests
# under hostd and nothing else. docs/workstreams/01-host-nixos.md is the
# spec; docs/interfaces/host-conventions.md lists every path, device and
# unit name, and each of them is created by one of the modules imported
# here. Built here, deployed with nixos-anywhere; never installed on the
# dev box.
{ config, lib, pkgs, ... }:
let
  cfg = config.repose.host;
in
{
  imports = [
    ./disko.nix
    ./kernel.nix
    ./network.nix
    ./nftables.nix
    ./storage.nix
    ./virt.nix
    ./hostd.nix
    ./gc.nix
    ./observability.nix
    ./fluent-bit.nix
    ./registration.nix
    ./ssh.nix
    ./hardening.nix
    ./azure.nix
    ./caches.nix
    ./vm-hardening.nix
    ./freeze.nix
  ];

  options.repose.host = {
    hostName = lib.mkOption {
      type = lib.types.str;
      default = "repose-host";
      description = "Machine hostname. The host id used everywhere else comes from registration, not from this.";
    };

    pauseIdle = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Rest a guest nobody is using: its vCPUs stop and its memory stays
        resident on this host, every process and socket survives, and
        anything that reaches the machine wakes it (DECISIONS I-818).
        This is what puts `pause` in the host's Hello, so an api asks a
        host without it for no pause and no wake.

        Off by default, and a host opts in the way it opts into the
        freeze: a plain vm.pause and vm.resume of a guest with two
        vhost-user devices is measured on no repose host yet, and I-652
        records that a *snapshotted* VM must not be resumed at all
        because its virtio-fs stops answering.
      '';
    };

    provider = lib.mkOption {
      type = lib.types.enum [ "azure" "none" ];
      default = "azure";
      description = ''
        Where the host runs. `azure` enables waagent, cloud-init for the join
        token, and the Hyper-V drivers. `none` is for the VM tests and for a
        future provider module (DECISIONS R3-20: Hetzner is a second module,
        added when it exists).
      '';
    };

    uplinkInterface = lib.mkOption {
      type = lib.types.str;
      default = "eth0";
      description = "The provider NIC: DHCP, default route, NAT egress for guests, and nothing inbound.";
    };

    osDevice = lib.mkOption {
      type = lib.types.str;
      # Every size this subscription may use (Intel v7) exposes disks over
      # NVMe only, and which name the OS disk takes depends on how many
      # controllers the size uses, so it is resolved at install time from
      # the namespace ids like the data disk (DECISIONS I-647, amending
      # I-39; I-41). A SCSI size would set this to /dev/sda, and any real
      # path here is used as it stands.
      default = "/dev/disk/repose/os";
      description = "OS disk for the disko layout (GPT, ESP, ext4 root).";
    };

    dataDevice = lib.mkOption {
      type = lib.types.str;
      default = "/dev/disk/repose/data";
      description = ''
        Data disk that becomes the one PV of `vg-guests`. The default is a
        symlink the disko layout's Azure hook creates at install time from
        whatever the size exposes: the SCSI by-LUN path, or the one NVMe
        disk that is not the OS disk (DECISIONS I-41). Set it to a real
        device to bypass that.
      '';
    };

    edgeWireGuardAddress = lib.mkOption {
      type = lib.types.str;
      default = "10.255.0.1";
      description = "The edge's WireGuard address (docs/workstreams/06-gateway-edge.md owns the 10.255.0.0/16 plan). Guests may reach it on the hook-ingest and noVNC relay ports only.";
    };

    apiAddr = lib.mkOption {
      type = lib.types.str;
      default = "api.repose.herakraft.co:443";
      description = ''
        Where hostd registers and holds its gRPC stream. Production is the
        api; before it exists this is the `hostdev` stand-in (DECISIONS
        I-17), for example the edge's public address on 443.
      '';
    };

    apiServerName = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = "TLS server name when it differs from apiAddr's host part (hostd --api-server-name). Empty means the address itself.";
    };

    apiCA = lib.mkOption {
      type = lib.types.lines;
      default = "";
      description = ''
        PEM certificate bundle hostd trusts for the api instead of the
        system roots (hostd --api-ca). Empty means the system roots, which
        is what the real api behind a public certificate needs. The
        `hostdev` stand-in (DECISIONS I-17) issues its own CA, and a host it
        drives names that certificate here. Public material only, never a
        key: it lands in the store.
      '';
    };

    apiCAFile = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = ''
        Path on the host to a PEM bundle hostd trusts for the api
        (hostd --api-ca), for a CA that is not known when the host is built:
        the VM tests' `hostdev`, or a CA delivered beside the join token.
        `apiCA` is the usual way; setting both is an error (DECISIONS I-75).
      '';
    };

    snapshots = {
      blobUrl = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = ''
          Azure Blob service URL snapshots stream to (hostd --blob-url),
          for example `https://<account>.blob.core.windows.net`. Empty
          keeps snapshots under `localDir`, which is not a backup: DESIGN
          §6 says Blob, and a production host must set this.
        '';
      };
      container = lib.mkOption {
        type = lib.types.str;
        default = "repose-snapshots";
        description = "Blob container snapshots are written to (hostd --blob-container).";
      };
      identityClientId = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "Client id of the user-assigned managed identity hostd authenticates to Blob with (hostd --blob-identity); empty uses the VM's default credential chain.";
      };
      localDir = lib.mkOption {
        type = lib.types.str;
        default = "/var/lib/repose/snapshots";
        description = "Where snapshots land while blobUrl is empty (hostd --snapshot-dir): the host's own disk, for VM tests and a host without a Blob account. A snapshot here does not survive the host.";
      };
    };

    hostdPackage = lib.mkOption {
      type = lib.types.package;
      default = pkgs.callPackage ./hostd-stub.nix { };
      defaultText = lib.literalExpression "pkgs.callPackage ./hostd-stub.nix { }";
      description = ''
        The hostd binary. Workstream 03 provides the real one as the flake's
        `packages.hostd`; until it is merged this is a stub that logs, sleeps,
        and implements only what the host units call (`register`,
        `audit-login`, `snapshot-all`, `version`).
      '';
    };

    kernelPackages = lib.mkOption {
      type = lib.types.raw;
      default = pkgs.linuxPackages;
      defaultText = lib.literalExpression "pkgs.linuxPackages";
      description = ''
        The host kernel. The flake sets it from its own nixpkgs pin while
        `nixpkgs` carries no host kernel with the CVE-2026-99999 fix
        (DECISIONS I-778). kernel.nix asserts the version either way, so this
        default is a build failure until that channel catches up rather than
        a host that boots the vulnerable kernel.
      '';
    };

    baseRepo = {
      url = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = ''
          Git URL hostd clones a missing base checkout from
          (`/var/lib/repose/base/<base_ref>`, hostd --base-repo-url). Empty
          means the operator places checkouts by hand (ops/RUNBOOK.md
          "Build: base unavailable").
        '';
      };
      sshKeyFile = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "Private key file for a private baseRepo.url over SSH (hostd --base-repo-ssh-key); delivered like the join token, never in the store.";
      };
    };

    claudeLoginShare = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = ''
        Share each user's Claude Code login file into all of that user's
        guests (DECISIONS I-278). false starts no `virtiofsd-auth@` at the
        next guest start: guests boot without the share and keep a login of
        their own; existing share directories stay until hostd's sweep.
      '';
    };

    buildUser = lib.mkOption {
      type = lib.types.str;
      default = "nixbuild";
      description = "The unprivileged account fragment evaluation and builds run as (docs/workstreams/12-nix-config-pipeline.md \"Flags and limits\"); created here, allowed to talk to nix-daemon.";
    };

    overlayCache = {
      url = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "Platform overlay binary cache URL (DECISIONS I-46: the Cachix cache `repose`, https://repose.cachix.org), added to the daemon's substituters and passed to hostd's builds. Empty until the owner creates the cache (ops/AZURE-SETUP.md).";
      };
      publicKey = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "Signing key of the overlay cache (`name:base64`). Required when `url` is set.";
      };
    };

    bootstrap = {
      enable = lib.mkEnableOption "operator SSH on the provider NIC with a plain public key, until the host is registered: the installer's checks and the join-token delivery reach a host over the VNet through the edge, and once host.json carries a WireGuard address sshd binds that alone, so the rule admits nothing afterwards (DECISIONS I-92)";
      authorizedKeys = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "Operator public keys accepted for root while bootstrap is enabled.";
      };
      keyUntilHostCA = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = ''
          Accept the bootstrap keys only while the host has no Host CA
          (/run/repose/host_ca.pub empty): repose-host-net renders them into
          /run/repose/bootstrap_authorized_keys when the CA file is empty and
          empties that file when it is not, so a host that knows the Host CA
          takes operator certificates only (14 §9, review M5-6) and a host that
          lost it (no host.json, a CA that never arrived) takes the key again.
          Off by default: turning it on ends plain-key logins the moment the
          CA arrives, so it is turned on per host once an operator has logged
          in with a certificate there (RUNBOOK "Retiring a host's bootstrap
          key", DECISIONS I-177).
        '';
      };
    };

    timeZone = lib.mkOption {
      type = lib.types.str;
      default = "UTC";
      description = "The host's timezone; `repose-snapshot.timer` fires at 03:00 in it (DESIGN §6).";
    };
  };

  config = {
    assertions = [
      {
        assertion = cfg.overlayCache.url == "" || cfg.overlayCache.publicKey != "";
        message = "repose.host.overlayCache.publicKey must be set when overlayCache.url is set; an unsigned substituter is a supply-chain hole.";
      }
      {
        assertion = !cfg.bootstrap.enable || cfg.bootstrap.authorizedKeys != [ ];
        message = "repose.host.bootstrap.enable without authorizedKeys opens sshd on the provider NIC for nobody.";
      }
      {
        assertion = cfg.apiCA == "" || cfg.apiCAFile == "";
        message = "repose.host.apiCA and repose.host.apiCAFile both set: hostd takes one --api-ca, so name the CA once.";
      }
    ];

    warnings = lib.optional (cfg.provider == "azure" && cfg.snapshots.blobUrl == "")
      "repose.host.snapshots.blobUrl is empty: snapshots of ${cfg.hostName} stay on its own disk under ${cfg.snapshots.localDir} and do not survive the host (DESIGN §6).";

    networking.hostName = cfg.hostName;
    time.timeZone = cfg.timeZone;
    i18n.defaultLocale = "C.UTF-8";

    boot.loader.systemd-boot.enable = true;
    boot.loader.systemd-boot.configurationLimit = 5;
    boot.loader.efi.canTouchEfiVariables = true;

    # The operator toolbox named in docs/ops/RUNBOOK.md and the checklists.
    environment.systemPackages = with pkgs; [
      jq
      iproute2
      nftables
      wireguard-tools
      curl
      htop
      tmux
      lsof
      zstd
    ];

    system.stateVersion = "26.11";
  };
}
