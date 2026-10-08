# Kernel and boot for a Cloud Hypervisor direct-boot guest. No bootloader:
# the runner passes the kernel and initrd to the hypervisor. The module list
# is the one docs/workstreams/02-guest-base.md names; the microvm.nix module
# adds its own initrd modules on top when the runner composes this base.
{ config, lib, pkgs, ... }:
let
  # DECISIONS I-231: the setuid wrappers, restored from a copy made at the
  # first boot of these exact wrappers. The key is the NixOS script, which
  # names every wrapper's program, owner, mode and capabilities.
  wrappersCache = "/var/lib/repose/wrappers";
  wrappersKey = builtins.substring 0 32 (builtins.hashString "sha256" config.systemd.services.suid-sgid-wrappers.script);
  # ExecCondition: exit 0 runs NixOS's script, exit 1 skips it.
  wrappersRestore = pkgs.writeShellScript "repose-wrappers-restore" ''
    c=${wrappersCache}/${wrappersKey}
    if [ -e /run/wrappers/bin ] || [ ! -f "$c/.complete" ]; then
      exit 0
    fi
    ${pkgs.coreutils}/bin/cp -a "$c" /run/wrappers/wrappers.${wrappersKey} || exit 0
    ${pkgs.coreutils}/bin/ln -s /run/wrappers/wrappers.${wrappersKey} /run/wrappers/bin || exit 0
    exit 1
  '';
  wrappersSave = pkgs.writeShellScript "repose-wrappers-save" ''
    set -eu
    c=${wrappersCache}/${wrappersKey}
    [ ! -f "$c/.complete" ] || exit 0
    ${pkgs.coreutils}/bin/rm -rf ${wrappersCache}
    ${pkgs.coreutils}/bin/mkdir -p -m 0700 ${wrappersCache}
    t=$(${pkgs.coreutils}/bin/mktemp --directory --tmpdir=${wrappersCache} tmp.XXXXXXXXXX)
    ${pkgs.coreutils}/bin/cp -a /run/wrappers/bin/. "$t/"
    ${pkgs.coreutils}/bin/chmod 0755 "$t"
    ${pkgs.coreutils}/bin/touch "$t/.complete"
    ${pkgs.coreutils}/bin/mv "$t" "$c"
  '';
in
{
  # Latest LTS from nixpkgs (linuxPackages is the LTS default).
  boot.kernelPackages = lib.mkDefault pkgs.linuxPackages;

  boot.kernelModules = [
    "overlay"
    "br_netfilter"
    "nf_tables"
    "vsock"
    "vmw_vsock_virtio_transport"
    "virtiofs"
  ];
  boot.initrd.availableKernelModules = [
    "virtio_pci"
    "virtio_blk"
    "virtio_net"
    "virtiofs"
    "overlay"
  ];
  # The scripted stage 1, not systemd's initrd (DECISIONS I-231). The guest's
  # stage 1 only loads the virtio modules and mounts the volume, the store
  # share and its overlay; the systemd initrd took 2.1 s of a 9 s boot on the
  # dev box for it (unit machinery, the mount-monitor rate limit below
  # holding the store mounts back 0.55 s, a switch-root) and its image was
  # 27 MB against 11. The scripted one does it in 0.6 s. microvm.nix sets
  # the systemd initrd with mkDefault for Cloud Hypervisor, hence mkForce.
  # The activation script now runs in stage 2's init, as before 24.11.
  boot.initrd.systemd.enable = lib.mkForce false;

  # DECISIONS I-587: before stage 2, remove the overlayfs whiteouts in the
  # store's upper dir. A nix command in the guest that deletes a path the
  # host shares (the lower layer, /nix/.ro-store) leaves one there (a 0:0
  # character device) and it hides the host's copy from then on, the
  # system the guest is about to boot included (kanali, 2026-10-06:
  # "stage 2 init script ... not found"). guestd roots every shared path
  # so nix no longer deletes one (I-588); this repairs what was done
  # before, or by a deletion that ignores roots. Every whiteout directly
  # in the upper store dir goes, hiding a shared path or not: one with no
  # lower counterpart hides nothing today but would hide the path the
  # first time a later start or switch shares it again. Nothing else is
  # touched, and nothing at all when there is no whiteout. The upper dir
  # is changed only while the overlay is unmounted, as overlayfs
  # requires; the remount takes the line stage 1 wrote to its fstab. The
  # upper root's opaque xattr needs no handling: overlayfs never reads it
  # on the root of a mount ("root is always merge"), and only a write
  # straight into the upper dir could set it there. No overlay (another
  # store layout): nothing happens.
  # The repair is a scripted-initrd hook, which nixpkgs plans to remove in
  # 26.11; a move to systemd's initrd must bring it along, so this fails
  # the evaluation rather than drop it quietly.
  assertions = [{
    assertion = !config.boot.initrd.systemd.enable;
    message = "repose: the store whiteout repair (DECISIONS I-587) runs in the scripted initrd; port it to the systemd initrd before enabling it";
  }];
  boot.initrd.postMountCommands = lib.mkAfter ''
    # On the console and in the kernel log, which stage 2's journal and
    # `dmesg` keep.
    reposeSay() {
      echo "$1"
      echo "<5>$1" > /dev/kmsg 2>/dev/null || true
    }
    reposeStoreRepair() {
      local opts upper lower list total shared f name l
      opts=$(awk '$2 == "/mnt-root/nix/store" && $3 == "overlay" { print $4; exit }' /proc/mounts)
      [ -n "$opts" ] || return 0
      upper=$(echo "$opts" | tr ',' '\n' | sed -n 's/^upperdir=//p')
      lower=$(echo "$opts" | tr ',' '\n' | sed -n 's/^lowerdir=//p')
      [ -n "$upper" ] && [ -d "$upper" ] || return 0
      list=/repose-store-whiteouts
      : > "$list"
      find "$upper" -mindepth 1 -maxdepth 1 -type c > "$list.c" 2>/dev/null || true
      while IFS= read -r f; do
        [ "$(stat -c '%t:%T' "$f" 2>/dev/null)" = "0:0" ] && echo "$f" >> "$list"
      done < "$list.c"
      total=$(wc -l < "$list")
      if [ "$total" -eq 0 ]; then
        reposeSay "repose: store overlay: 0 whiteouts removed"
        return 0
      fi
      if ! umount /mnt-root/nix/store; then
        reposeSay "repose: store overlay busy; $total whiteouts left in place"
        return 0
      fi
      shared=0
      while IFS= read -r f; do
        name=''${f##*/}
        for l in $(echo "$lower" | tr ':' ' '); do
          if [ -e "$l/$name" ] || [ -L "$l/$name" ]; then
            shared=$((shared + 1))
            break
          fi
        done
        rm -f "$f"
      done < "$list"
      if ! mount /mnt-root/nix/store; then
        reposeSay "repose: store overlay remount failed; retrying"
        mount /mnt-root/nix/store || fail
      fi
      reposeSay "repose: store overlay: $total whiteouts removed, $shared of them over shared store paths"
    }
    reposeStoreRepair
  '';

  boot.loader.grub.enable = false;
  boot.loader.systemd-boot.enable = false;
  boot.loader.efi.canTouchEfiVariables = false;

  # The runner adds console=ttyS0 and the ip= line; these are the same for
  # every guest so they live here.
  boot.kernelParams = [
    "panic=-1"
    "reboot=t"
    # The serial console answers no terminal queries, so systemd waited out
    # its size and terminfo timeouts on every boot: about 0.67 s in the
    # initrd and 0.33 s in stage 2. Saying what the console is skips the
    # queries; all three are needed (DECISIONS I-161).
    "systemd.tty.term.console=vt220"
    "systemd.tty.rows.console=24"
    "systemd.tty.columns.console=80"
  ];

  # The initrd's services each mount a credentials directory; those early
  # mounts tripped systemd's mount-monitor rate limit, which then held
  # sysroot.mount back for about 0.7 s on every boot. A guest passes no
  # credentials, so the imports go (DECISIONS I-161). systemd-fsck-root and
  # the sysroot tmpfiles unit are upstream units and take a drop-in. Inert
  # with the scripted stage 1 (I-231); kept for a return to systemd's.
  boot.initrd.systemd.services =
    lib.genAttrs [
      "systemd-journald"
      "systemd-tmpfiles-setup-dev-early"
      "systemd-tmpfiles-setup-dev"
      "systemd-tmpfiles-setup"
      "systemd-sysctl"
      "systemd-vconsole-setup"
    ]
      (_: { serviceConfig.ImportCredential = ""; })
    // lib.genAttrs [ "systemd-fsck-root" "systemd-tmpfiles-setup-sysroot" ]
      (_: { overrideStrategy = "asDropin"; serviceConfig.ImportCredential = ""; });

  # The same in stage 2 (DECISIONS I-231). Every unit that imports
  # credentials gets its own tmpfs on /run/credentials/<unit>, mounted when
  # it starts and, for the oneshots, unmounted when it ends; with the API
  # mounts that systemd starts beside them, that was more than five mount
  # table changes in a second right after the switch to the real root, so
  # systemd's mount monitor held every later mount back until the second
  # was over: /run/wrappers, and so local-fs.target, sysinit.target and
  # guestd, started 0.6 s late on every boot. The guest gets no credentials.
  systemd.services = lib.genAttrs [
    "systemd-journald"
    "systemd-tmpfiles-setup-dev-early"
    "systemd-tmpfiles-setup-dev"
    "systemd-tmpfiles-setup"
    "systemd-tmpfiles-clean"
    "systemd-sysctl"
    "systemd-vconsole-setup"
    "systemd-network-generator"
    "systemd-networkd"
    "systemd-resolved"
    "getty@"
    "serial-getty@"
  ]
    (_: { serviceConfig.ImportCredential = ""; })
  // {
    # Also off the critical chain (DECISIONS I-231): copying and chmodding
    # the setuid wrappers (sudo, mount, newuidmap...) is about 45 processes,
    # 0.3 s on host-01, and NixOS orders it before sysinit.target, which
    # everything waits for. Nothing a boot starts needs a wrapper except
    # PAM (unix_chkpwd), for dev's lingering user manager, which logind
    # starts, and for logins; so it comes before logind and user sessions.
    suid-sgid-wrappers.before = lib.mkForce [ "systemd-logind.service" "systemd-user-sessions.service" "shutdown.target" ];
    # Even off sysinit, making them took 1.2-1.5 s of a boot on the dev box
    # (45 short processes against everything else starting), and logind
    # waits for it. The finished directory for these exact wrappers is kept
    # on the volume, root-only, and a boot copies it back in one cp -a
    # (modes, owners and file capabilities included), skipping NixOS's
    # script. Different wrappers (a new base, a fragment adding one) are a
    # different key: the script runs as before and the copy is replaced.
    suid-sgid-wrappers.serviceConfig = {
      ExecCondition = "${wrappersRestore}";
      ExecStartPost = "-${wrappersSave}";
    };
  };

  # The rest of that burst is systemd's API mounts. These five serve nothing
  # a guest runs at boot (debugfs and tracefs are for tracing tools, configfs
  # for kernel targets, fusectl for aborting FUSE connections, hugetlbfs for
  # explicit huge pages); a user who wants one mounts it with sudo.
  systemd.suppressedSystemUnits = [
    "sys-kernel-debug.mount"
    "sys-kernel-tracing.mount"
    "sys-kernel-config.mount"
    "sys-fs-fuse-connections.mount"
    "dev-hugepages.mount"
  ];

  # No BPF LSM (DECISIONS I-231): with it, systemd loads its restrict-fs
  # program at startup, 0.07 s in the initrd and 0.27 s after the switch to
  # the real root on the dev box. It only enforces RestrictFileSystems=, and
  # the guest's boundary is the VM.
  security.lsm = lib.mkForce [ "landlock" "yama" ];

  # The guest's whole state is on its thin volume (/), which the kernel sees
  # as the first virtio disk; the shared store is a virtio-fs tag. Both are
  # declared by nix/guest/microvm.nix; the VM tests use the test framework's
  # own layout, which has the same /nix/.ro-store + /nix/.rw-store shape.
  # /tmp is on that volume too; ./tmp.nix empties it at each boot.
}
