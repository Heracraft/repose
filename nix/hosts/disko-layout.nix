# The disk layout as a pure function of its devices, so the VM test can
# format the data disk alone (`withOs = false`) on a virtual disk while the
# host uses the same code for both disks.
#
# OS disk: GPT, 1 GB ESP, the rest ext4 for `/`.
# Data disk: one PV, VG `vg-guests`, thin pool `thin` on 95 percent of the
# VG. The remaining 5 percent is headroom that lvm.conf's autoextend grows
# the pool into (storage.nix). Pool metadata is 1 percent of the disk,
# clamped to [1 GiB, 16 GiB] (LVM's ceiling), computed in the disko script
# because the disk size is only known at install time.
{
  lib,
  osDevice,
  dataDevice ? null,
  withOs ? true,
  # The edge (nix/edge) has no data disk and reuses only the OS layout.
  withData ? true,
  # Directory of udev rules that create /dev/disk/azure/*; null outside Azure.
  azureUdevRules ? null,
}:
let
  # One percent of the PV, clamped to 1 to 16 GiB, in whole MiB: lvcreate
  # rejects a byte count that is not a multiple of 512 (host-01, 2026-09-20).
  # The clamp is assigned back to m; as a bare comma operand its value was
  # discarded, so a 64 GiB test disk got 0.64 GiB and a disk over 1.6 TiB
  # would have asked for more metadata than a thin pool allows.
  poolMetadataSize = ''"$(( m = $(blockdev --getsize64 "''${lvm_devices[0]}") / 100, m = (m < 1073741824 ? 1073741824 : (m > 17179869184 ? 17179869184 : m)), m / 1048576 ))m"'';

  # A symlink under /dev/disk/repose is this hook's own answer from a
  # previous run. /dev is tmpfs, but an install that failed and is retried
  # has not rebooted, so last run's answer is still there — and if it was
  # wrong, keeping it is how both disks ended up resolving to the same one
  # (host-stg-01, 2026-10-11). Drop ours and resolve again; a path the
  # operator named (a real node, a by-LUN symlink) is never touched.
  dropStale = path: ''
    case "${path}" in
      /dev/disk/repose/*) rm -f "${path}" ;;
    esac
  '';

  # Which NVMe namespace is the OS disk, resolved at install time rather
  # than assumed (DECISIONS I-647). Azure's mapping is by namespace id: the
  # OS disk is nsid 1 and a data disk at LUN n is nsid n+2. Controller order
  # is not: host-01's D16s_v7 put both on one controller, so the OS disk was
  # /dev/nvme0n1 as I-39 recorded, while host-stg-01's D2s_v7 gave the data
  # disk its own controller and enumerated it first — /dev/nvme0n1, nsid 12
  # for LUN 10, with the OS disk second as /dev/nvme1n1, nsid 1. A fixed
  # /dev/nvme0n1 installed the OS onto the data disk there (2026-10-11).
  #
  # Runs from both disks' preCreateHook because attribute order makes disko
  # create `data` before `os`, and the data hook needs the answer to know
  # which disk is not the OS disk. Idempotent, and a no-op when osDevice is
  # a real path (a SCSI size, or a test's virtual disk).
  azureOsResolve = lib.optionalString (azureUdevRules != null) ''
    ${dropStale osDevice}
    if [ ! -e "${osDevice}" ]; then
      # lsblk rather than a glob, for the reason in the data hook below:
      # disko's script runs with globbing off, so /sys/block/nvme*n* stays
      # literal and matches nothing.
      found=""
      for name in $(lsblk -dn -o NAME,TYPE | awk '$2 == "disk" { print $1 }'); do
        case "$name" in nvme*) ;; *) continue ;; esac
        [ -r "/sys/block/$name/nsid" ] || continue
        [ "$(cat "/sys/block/$name/nsid")" = "1" ] || continue
        found="$found /dev/$name"
      done
      set -- $found
      if [ "$#" -ne 1 ]; then
        echo "repose: expected exactly one NVMe namespace with nsid 1 (the Azure OS disk), found:$found" >&2
        lsblk -d -o NAME,SIZE,MODEL >&2 || true
        exit 1
      fi
      mkdir -p "$(dirname "${osDevice}")"
      ln -sfn "$1" "${osDevice}"
      echo "repose: os disk ${osDevice} -> $1"
    fi
  '';

  # nixos-anywhere's kexec installer has no Azure udev rules, and the v7
  # sizes expose disks over NVMe with no by-LUN name at all (DECISIONS I-39,
  # I-41). This runs before disko touches the data disk and makes
  # ${dataDevice} exist: on a SCSI size by loading waagent's rules and
  # retriggering, on an NVMe size by pointing the symlink at the one NVMe
  # disk that is not the OS disk. A no-op wherever the path already exists.
  azureUdevHook = lib.optionalString (azureUdevRules != null) ''
    ${azureOsResolve}
    ${dropStale dataDevice}
    # The SCSI branch is best effort: this waagent build ships no rules at
    # that path, and on host-01 the unguarded cp aborted the whole hook
    # before the NVMe branch below ever ran (2026-09-20).
    if [ ! -e "${dataDevice}" ] && [ -d /run/udev ] && [ -d "${azureUdevRules}" ]; then
      mkdir -p /run/udev/rules.d
      cp "${azureUdevRules}"/*.rules /run/udev/rules.d/ 2>/dev/null || true
      udevadm control --reload || true
      udevadm trigger --subsystem-match=block --action=add || true
      udevadm settle || true
    fi
    if [ ! -e "${dataDevice}" ]; then
      os=$(readlink -f "${osDevice}")
      # lsblk rather than a glob: disko's script runs with globbing off, so
      # /sys/block/nvme*n* stayed literal and no disk was ever seen
      # (host-01, 2026-09-20). Whole disks only; loop devices and the
      # virtual DVD are not disks.
      candidates=""
      for name in $(lsblk -dn -o NAME,TYPE | awk '$2 == "disk" { print $1 }'); do
        dev=/dev/$name
        [ "$dev" = "$os" ] && continue
        case "$name" in nvme*|sd*) ;; *) continue ;; esac
        candidates="$candidates $dev"
      done
      set -- $candidates
      if [ "$#" -ne 1 ]; then
        echo "repose: expected exactly one data disk besides ${osDevice}, found:$candidates" >&2
        lsblk -d -o NAME,SIZE,MODEL >&2 || true
        exit 1
      fi
      mkdir -p "$(dirname "${dataDevice}")"
      ln -sfn "$1" "${dataDevice}"
      echo "repose: data disk ${dataDevice} -> $1"
    fi
  '';

  os = {
    type = "disk";
    device = osDevice;
    preCreateHook = azureOsResolve;
    content = {
      type = "gpt";
      partitions = {
        ESP = {
          size = "1G";
          type = "EF00";
          content = {
            type = "filesystem";
            format = "vfat";
            mountpoint = "/boot";
            mountOptions = [ "umask=0077" ];
          };
        };
        root = {
          size = "100%";
          content = {
            type = "filesystem";
            format = "ext4";
            mountpoint = "/";
          };
        };
      };
    };
  };

  data = {
    type = "disk";
    device = dataDevice;
    preCreateHook = azureUdevHook;
    content = {
      type = "gpt";
      partitions.pv = {
        size = "100%";
        content = {
          type = "lvm_pv";
          vg = "vg-guests";
        };
      };
    };
  };
in
{
  disk = (lib.optionalAttrs withOs { inherit os; }) // (lib.optionalAttrs withData { inherit data; });
}
// lib.optionalAttrs withData {
  lvm_vg.vg-guests = {
    type = "lvm_vg";
    lvs.thin = {
      size = "95%";
      lvm_type = "thin-pool";
      extraArgs = [
        "--poolmetadatasize"
        poolMetadataSize
        # hostd relies on discards reaching the pool so snapshots of trimmed
        # volumes compress to their real contents (03-hostd §5.9).
        "--discards"
        "passdown"
        # Zero new chunks. With zeroing off, a partial first write to a
        # chunk leaves the rest holding whatever the previous owner of that
        # block wrote: a cross-tenant leak through freed blocks.
        "--zero"
        "y"
      ];
    };
  };
}
