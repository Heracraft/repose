# The Mac mini appliance: a repose host as one aarch64 NixOS VM on Apple's
# Virtualization framework, launched by vfkit from a launchd agent on the
# user's Mac (DECISIONS I-1376, I-1380; docs/workstreams/19-mac-mini.md
# item 19f). Active when `repose.host.provider == "mac"`. Everything the
# generic host has, the appliance has, and this file holds only what a
# Mac changes:
#
#   one disk       The launcher attaches one raw image: ESP, a fixed root
#                  and a third partition that is the PV of vg-guests
#                  (disko-layout.nix `oneDisk`). The image ships small and
#                  the partition, the PV and the pool grow to the disk the
#                  user gave it at first boot (repose-mac-grow).
#   no cloud-init  The join token and the host's place on the hub arrive
#                  from a file the launcher shares into the VM over
#                  virtio-fs (repose-launcher-join), never from a provider.
#   the hub        A Mac has no route to the api's VNet address
#                  (prod.nix), so the api is dialled at the control
#                  plane's WireGuard address through the one path the edge
#                  forwards a host (I-1382), over a tunnel that exists
#                  before the first Register (I-1380, network.nix hub.json).
#   owned          The user's machine: snapshots stay on its disk (I-1372),
#                  no operator bootstrap key, no park vault, no Blob.
#   the kernel     The same pin as every host, and here it is a product
#                  constraint, not a detail: see the note at `boot.kernelParams`.
#
# Sizing for the two Macs repose names (docs page "What a Mac mini
# needs"): the launcher gives the VM three quarters of the Mac's memory,
# 12 GiB of 16 and 18 GiB of 24, and macOS keeps the rest. Inside, the
# host reserve is the fleet rule (I-724: an eighth, floor 2 GiB), so a 16
# GB Mac places 10 GiB of guests (one large and one tiny, or two small
# and a tiny) and a 24 GB Mac 15.75 GiB (one xl, or a large and a small
# and a tiny). `repose host ls` shows the VM's memory, which is the
# figure the plan caps on.
{ config, lib, pkgs, ... }:
let
  cfg = config.repose.host;
  mac = cfg.mac;
  platform = import ./platform.nix;
  launcherDir = "/run/repose/launcher";
  joinFile = "${launcherDir}/join";
  runDir = "/run/repose";
  lvm = lib.getBin config.services.lvm.package;

  # The join bundle (hostmgr.JoinBundle, docs/interfaces/api.md): one
  # base64url string carrying `{"v":1,"token":...,"wg":{...}}`. Split into
  # the two files the generic host already reads: /run/repose/join-token
  # for repose-register (unchanged, the pinned host-services check) and
  # /run/repose/hub.json for repose-host-net's bootstrap tunnel. Logs name
  # events and sizes only; the token and the key never reach a line.
  launcherJoin = pkgs.writeShellApplication {
    name = "repose-launcher-join";
    runtimeInputs = [ pkgs.coreutils pkgs.jq ];
    text = ''
      join=${joinFile}
      run=${runDir}
      stateJson=/var/lib/repose/hostd/host.json
      umask 077
      mkdir -p "$run"
      chmod 0755 "$run"
      if [ ! -s "$join" ]; then
        echo "repose-launcher-join: event=no_join_file"
        exit 0
      fi
      if [ -s "$stateJson" ]; then
        # Registered already: the launcher left the bundle from the first
        # boot, or the user ran the install twice. A used bundle is dead,
        # and a new one for a registered host is `repose host rm` and
        # `host add` again, which replaces host.json by a new registration.
        echo "repose-launcher-join: event=join_ignored reason=registered"
        rm -f "$join" || true
        exit 0
      fi
      raw=$(tr -d '[:space:]' < "$join")
      # base64url without padding: GNU basenc wants it padded.
      pad=$(( (4 - ''${#raw} % 4) % 4 ))
      padded="$raw$(printf '%*s' "$pad" "" | tr ' ' '=')"
      if ! decoded=$(printf '%s' "$padded" | basenc --base64url -d 2>/dev/null); then
        echo "repose-launcher-join: event=join_invalid reason=encoding" >&2
        exit 1
      fi
      if [ "$(printf '%s' "$decoded" | jq -r '.v // 0')" != "1" ]; then
        echo "repose-launcher-join: event=join_invalid reason=version" >&2
        exit 1
      fi
      token=$(printf '%s' "$decoded" | jq -r '.token // empty')
      if [ -z "$token" ]; then
        echo "repose-launcher-join: event=join_invalid reason=no_token" >&2
        exit 1
      fi
      if [ ! -e "$run/join-token" ]; then
        printf '%s\n' "$token" > "$run/join-token.tmp"
        chmod 0600 "$run/join-token.tmp"
        mv "$run/join-token.tmp" "$run/join-token"
        echo "repose-launcher-join: event=join_token_written"
      fi
      if printf '%s' "$decoded" | jq -e '.wg.private_key and .wg.address and .wg.edge_pubkey and .wg.edge_endpoint' >/dev/null; then
        printf '%s' "$decoded" | jq '.wg' > "$run/hub.json.tmp"
        chmod 0600 "$run/hub.json.tmp"
        mv "$run/hub.json.tmp" "$run/hub.json"
        echo "repose-launcher-join: event=hub_bootstrap_written"
      else
        echo "repose-launcher-join: event=no_hub_in_join"
      fi
      # Single use: the file is gone from the Mac's share too, so a second
      # boot finds nothing to read and nothing of the bundle outlives the
      # registration it was for.
      rm -f "$join" || echo "repose-launcher-join: event=join_file_kept reason=share_read_only"
    '';
  };

  # First boot on a disk larger than the image: the last partition takes
  # the rest of the disk, the PV follows, the pool's metadata is sized for
  # the new PV and the pool itself grows to 95 percent of the VG, the same
  # shape disko-layout.nix gives a data disk at install. Every step is a
  # no-op the second time, so the unit runs at every boot and a user who
  # grows the image file later (the launcher's `grow`) gets the room at
  # the next boot. Arguments: the disk and the partition number; the unit
  # passes the layout's. Logs sizes and names only.
  grow = pkgs.writeShellApplication {
    name = "repose-mac-grow";
    runtimeInputs = [ pkgs.coreutils pkgs.util-linux pkgs.gptfdisk pkgs.cloud-utils pkgs.gawk lvm ];
    text = ''
      disk=''${1:-${mac.disk}}
      part=''${2:-${toString mac.guestsPartition}}
      pv="''${disk}''${part}"
      case "$disk" in /dev/nvme*|/dev/loop*|/dev/mmcblk*) pv="''${disk}p''${part}" ;; esac
      vg=vg-guests
      gib=$((1024 * 1024 * 1024))

      # 1. The partition, to the end of the disk. growpart exits 1 and
      # says NOCHANGE when it already is; that is the common case.
      out=$(growpart "$disk" "$part" 2>&1) && rc=0 || rc=$?
      case "$rc" in
        0) echo "repose-mac-grow: event=partition_grown disk=$disk part=$part" ;;
        1) echo "repose-mac-grow: event=partition_unchanged disk=$disk part=$part" ;;
        *) echo "repose-mac-grow: event=partition_grow_failed disk=$disk part=$part rc=$rc" >&2; echo "$out" >&2; exit 1 ;;
      esac
      udevadm settle --timeout 30 || true

      # 2. The PV, to the partition.
      if ! pvs "$pv" >/dev/null 2>&1; then
        echo "repose-mac-grow: event=no_pv device=$pv" >&2
        exit 1
      fi
      before=$(pvs --noheadings --units b --nosuffix -o pv_size "$pv" | tr -d ' ')
      pvresize "$pv" >/dev/null
      after=$(pvs --noheadings --units b --nosuffix -o pv_size "$pv" | tr -d ' ')
      if [ "$before" != "$after" ]; then
        echo "repose-mac-grow: event=pv_grown from=$before to=$after"
      fi

      # 3. The pool's metadata: one percent of the PV, clamped to 1 to 16
      # GiB, as disko-layout.nix computes it at install.
      want_meta=$((after / 100))
      [ "$want_meta" -lt $gib ] && want_meta=$gib
      [ "$want_meta" -gt $((16 * gib)) ] && want_meta=$((16 * gib))
      have_meta=$(lvs --noheadings --units b --nosuffix -o lv_metadata_size "$vg/thin" | tr -d ' ')
      if [ "$have_meta" -lt "$want_meta" ]; then
        lvextend --poolmetadatasize "''${want_meta}b" "$vg/thin" >/dev/null
        echo "repose-mac-grow: event=pool_metadata_grown from=$have_meta to=$want_meta"
      fi

      # 4. The pool, to 95 percent of the VG; the rest is the headroom
      # lvm's autoextend grows into (storage.nix). In whole extents, so a
      # second run computes the same size and does nothing.
      vg_size=$(vgs --noheadings --units b --nosuffix -o vg_size "$vg" | tr -d ' ')
      extent=$(vgs --noheadings --units b --nosuffix -o vg_extent_size "$vg" | tr -d ' ')
      want=$(( vg_size / 100 * 95 / extent * extent ))
      have=$(lvs --noheadings --units b --nosuffix -o lv_size "$vg/thin" | tr -d ' ')
      if [ "$have" -lt "$want" ]; then
        lvextend -L "''${want}b" "$vg/thin" >/dev/null
        echo "repose-mac-grow: event=pool_grown from=$have to=$want vg=$vg_size"
      else
        echo "repose-mac-grow: event=pool_unchanged size=$have vg=$vg_size"
      fi
    '';
  };
in
{
  options.repose.host.mac = {
    disk = lib.mkOption {
      type = lib.types.str;
      default = "/dev/vda";
      description = "The one virtio-blk disk the launcher attaches: the image, grown to the size the user chose.";
    };
    guestsPartition = lib.mkOption {
      type = lib.types.int;
      default = 3;
      readOnly = true;
      description = "The partition of `disk` that is vg-guests' PV: the third, after the ESP and the root (disko-layout.nix `oneDisk`).";
    };
    rootSize = lib.mkOption {
      type = lib.types.str;
      default = "32G";
      description = ''
        The root partition, fixed at install: it holds the host system,
        the guest base closure (about 6 GiB), the base checkouts and the
        builds, and nothing of the guests' volumes or the caches, which
        are on vg-guests. 32 GiB is twice what host-01 used after two
        months; nix's free-space floors below keep it from filling.
      '';
    };
    imageSize = lib.mkOption {
      type = lib.types.str;
      default = "37G";
      description = ''
        The size of the raw image as built: the ESP, `rootSize`, and a PV
        partition just big enough for the pool's 1 GiB metadata floor.
        Sparse while it is built and compressed before it ships, so the
        download is the closure and not the zeros; the launcher grows the
        file to the size the user chose, and `repose-mac-grow` gives the
        pool the difference at the next boot.
      '';
    };
    launcherShareTag = lib.mkOption {
      type = lib.types.str;
      default = "repose-launcher";
      description = "The virtio-fs mount tag of the directory the launcher shares in; the join bundle is `join` in it.";
    };
    launcherMount = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Mount the launcher's share at ${launcherDir}. The VM test turns it off and puts the file there itself.";
    };
    growDisk = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Run repose-mac-grow at boot (the VM test runs the script on a disk of its own instead).";
    };
    renameUplink = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Name the framework's virtio NIC `uplinkInterface` by driver (the VM test, whose NICs are all virtio, turns it off).";
    };
  };

  config = lib.mkIf (cfg.provider == "mac") {
    assertions = [
      {
        assertion = cfg.osDevice == mac.disk;
        message = "repose.host.mac.disk and repose.host.osDevice differ; the appliance has one disk and both name it.";
      }
      {
        assertion = !cfg.bootstrap.enable;
        message = "repose.host.bootstrap on a Mac appliance: there is no operator on the user's LAN, and the plain key would be the one way in from it.";
      }
      {
        assertion = !cfg.park.enable;
        message = "repose.host.park on a Mac appliance: the park wraps its image keys with an Azure Key Vault the Mac has no identity for (I-653); gated on gate 0 step 5 (I-1376).";
      }
    ];

    repose.host = {
      owned = true;
      osDevice = lib.mkDefault mac.disk;
      # The one path a host outside the VNet reaches the api (I-1382): the
      # api's gRPC at the control plane's hub address, over wg0. The
      # certificate carries that IP as a SAN and the internal name, so
      # the server name is the fleet's (I-92).
      apiAddr = lib.mkDefault "10.255.255.1:8443";
      apiServerName = lib.mkDefault platform.apiServerName;
      apiCA = lib.mkDefault platform.apiCA;
      baseRepo.url = lib.mkDefault platform.baseRepoUrl;
      # The NIC vfkit's NAT gives the VM is virtio; udev names it below.
      uplinkInterface = lib.mkDefault "wan0";
    };

    # ---- boot ------------------------------------------------------------
    # Apple's framework presents virtio over PCI: the disk, the NIC, the
    # console, the entropy source and the launcher's share. All in the
    # initrd so the root is found whatever order they enumerate in.
    boot.initrd.availableKernelModules = [ "virtio_pci" "virtio_blk" "virtio_mmio" "virtio_net" "virtio_console" "virtiofs" ];
    boot.initrd.kernelModules = [ "virtio_pci" "virtio_blk" ];
    boot.kernelModules = [ "virtiofs" ];
    # The framework's console is a virtio console, hvc0; the launcher
    # writes it to a log file beside the image (I-1380). No `rootdelay`:
    # the disk is there from the first microsecond.
    #
    # THE KERNEL IS A PRODUCT CONSTRAINT HERE. Under Apple's nested
    # virtualization (macOS 15.8.2, M4, 2026-11-19), a Cloud Hypervisor
    # guest inside this VM boots on 6.8.0-147 and on 6.18.57 and hangs at
    # about two seconds on 7.0.0-43 (`rcu_preempt kthread timer wakeup
    # didn't happen`, docs/research/mac-mini-nested-kvm.md §7), and
    # kernel.nix asserts 6.18.57 or newer for CVE-2026-99999. So the pin
    # in nix/flake.nix (`nixpkgs-host-kernel`) is the one version known
    # to satisfy both; the bisect window for whoever bumps next is
    # 6.18.57 to 7.0.0-43, and a bump is proven on Apple silicon with
    # `ops/dev/macmini-l2-try.sh` before it ships (I-1376).
    boot.kernelParams = [ "console=hvc0" ];

    # ---- the disk --------------------------------------------------------
    disko.devices.disk.os = {
      imageName = "repose-appliance";
      imageSize = mac.imageSize;
    };
    # The image is built inside a VM (disko's image builder) on the host
    # kernel pin, with the whole closure copied in. 4 GiB: the copy of a
    # host closure with the guest base in its store ran out at the default.
    disko.memSize = 4096;
    disko.imageBuilder = {
      name = "repose-appliance-image";
      # The raw file is sparse until it leaves the builder; a NAR has no
      # holes, so it ships compressed and the launcher inflates it.
      extraPostVM = ''
        ${pkgs.gzip}/bin/gzip -1 --rsyncable "$out/repose-appliance.raw"
      '';
    };

    systemd.services.repose-mac-grow = lib.mkIf mac.growDisk {
      description = "Grow the guest partition, PV and pool to the disk at boot (I-1376)";
      wantedBy = [ "multi-user.target" ];
      after = [ "repose-guest-pool.service" ];
      wants = [ "repose-guest-pool.service" ];
      before = [ "hostd.service" ];
      unitConfig.ConditionPathExists = "!${runDir}/pool-hold";
      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
        ExecStart = "${grow}/bin/repose-mac-grow ${mac.disk} ${toString mac.guestsPartition}";
      };
    };
    environment.systemPackages = [ grow launcherJoin ];

    # A 32 GiB root cannot keep gc.nix's fleet floors (50 and 100 GiB:
    # the store would be "below the floor" from the first boot and every
    # build would run a GC first). Four and eight: a guest base closure's
    # worth of room before a build, and twice that freed when it runs.
    nix.settings.min-free = lib.mkForce (4 * 1024 * 1024 * 1024);
    nix.settings.max-free = lib.mkForce (8 * 1024 * 1024 * 1024);

    # ---- the NIC ---------------------------------------------------------
    # The framework's NAT NIC is the only virtio_net device; named so the
    # uplink unit, the NAT rules and the input chain can name it, and so
    # the name does not depend on which PCI slot the framework picks.
    systemd.network.links."10-uplink" = lib.mkIf mac.renameUplink {
      matchConfig.Driver = "virtio_net";
      linkConfig.Name = cfg.uplinkInterface;
    };

    # ---- the launcher's share and the join --------------------------------
    fileSystems.${launcherDir} = lib.mkIf mac.launcherMount {
      device = mac.launcherShareTag;
      fsType = "virtiofs";
      options = [ "nofail" "x-systemd.device-timeout=5s" ];
    };

    systemd.services.repose-launcher-join = {
      description = "Take the join bundle from the launcher's share (I-1380)";
      wantedBy = [ "multi-user.target" ];
      unitConfig.RequiresMountsFor = lib.optional mac.launcherMount launcherDir;
      after = [ "systemd-tmpfiles-setup.service" ];
      before = [ "repose-host-net.service" "repose-register.service" "hostd.service" ];
      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
        ExecStart = "${launcherJoin}/bin/repose-launcher-join";
      };
    };

    # Register travels over the bootstrap tunnel, so it waits for it; a
    # tunnel the edge has not peered yet is the 30 s retry in
    # registration.nix, not a failure.
    systemd.services.repose-register = {
      after = [ "repose-launcher-join.service" "wg-quick-wg0.service" ];
      wants = [ "repose-launcher-join.service" ];
    };
  };
}
