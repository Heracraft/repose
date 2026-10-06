# mkGuestRunner: compose the platform base, home-manager and a user fragment
# into one NixOS system for Cloud Hypervisor, and produce the runner package
# hostd starts (docs/workstreams/02-guest-base.md, DECISIONS I-34).
#
# The system closure is guest-independent: nothing about a particular guest
# (ip, cid, tap, volume, sockets, vcpu, memory) is baked in. Those are
# arguments of `bin/run`, so Build (per revision) can happen before
# CreateGuest allocates an address, one closure serves a guest across stop,
# start and host moves, and `kernel_changed` compares revisions, not guests.
# The attributes the workstream doc lists (guestId, ip, gatewayIp, cid,
# volumeDevice, vcpu, mem, extraKernelParams) are still accepted: they
# become defaults of the run script, overridable on the command line.
#
# Output layout:
#   bin/run          start cloud-hypervisor for one guest (see --help)
#   bin/virtiofsd    start virtiofsd for the store share with the same flags
#   bin/shutdown     ask a running guest to power off via the API socket
#   share/repose/system   -> the NixOS toplevel (config.system.build.toplevel)
#   share/repose/kernel, initrd, cmdline, base-version, class
{ nixpkgs, home-manager, microvm, system, overlay, self }:
{ fragmentModule ? { }
, personalModule ? null   # the account's machine.nix (DECISIONS I-490)
, extraModules ? [ ]
, class ? "large"
, baseVersion ? (self.shortRev or self.dirtyShortRev or "dirty")
, guestd ? null
, hook ? null
  # Run-time defaults (DECISIONS I-34); every one is overridable by bin/run.
, guestId ? null
, ip ? null
, gatewayIp ? null
, netmask ? "255.255.252.0"
, cid ? null
, volumeDevice ? null
, tap ? null
, mac ? null
, vcpu ? null
, mem ? null
, extraKernelParams ? [ ]
}:
let
  lib = nixpkgs.lib;
  classDefaults = {
    small = { vcpu = 2; mem = 4096; };
    large = { vcpu = 4; mem = 8192; };
    xl = { vcpu = 8; mem = 16384; };
  }.${class};
  defaultVcpu = if vcpu != null then vcpu else classDefaults.vcpu;
  defaultMem = if mem != null then mem else classDefaults.mem;

  # The platform's pkgs without user overlays, for the fragment's
  # repose.overlays pre-pass (nix/guest/contract.nix). Same instantiation
  # as the flake's `pkgs`.
  prePassPkgs = import nixpkgs {
    inherit system;
    overlays = [ overlay ];
    config.allowUnfreePredicate = pkg: builtins.elem (lib.getName pkg) (import ./unfree-allowlist.nix);
  };

  guestSystem = lib.nixosSystem {
    inherit system;
    modules = [
      microvm.nixosModules.microvm
      home-manager.nixosModules.home-manager
      ./base
      # The fragment contract: repose.fragment applied to dev, repose.overlays
      # onto pkgs, repose.system through the allowlist (nix/guest/contract.nix).
      ./contract.nix
      {
        repose.class = class;
        repose.baseVersion = baseVersion;
        repose.fragment = fragmentModule;
        repose.personal = personalModule;
        repose.prePassPkgs = prePassPkgs;
        # command-not-found's and nix-locate's prebuilt index (I-219).
        repose.nixIndexPackage = self.inputs.nix-index-database.packages.${system}.nix-index-with-small-db;
      }
      (lib.mkIf (guestd != null) { repose.guestd.package = guestd; })
      (lib.mkIf (hook != null) { repose.hookPackage = hook; })
      {
        microvm = {
          hypervisor = "cloud-hypervisor";
          vcpu = defaultVcpu;
          mem = defaultMem;
          storeOnDisk = false;
          writableStoreOverlay = "/nix/.rw-store";
          # Guest-side view only: the socket and image paths are placeholders
          # that bin/run replaces with the per-guest ones.
          shares = [{
            tag = "ro-store";
            source = "/nix/store";
            mountPoint = "/nix/.ro-store";
            proto = "virtiofs";
            socket = "virtiofsd.sock";
          }];
          volumes = [{
            image = "volume.img";
            mountPoint = "/";
            fsType = "ext4";
            autoCreate = false;
          }];
          interfaces = [{ type = "tap"; id = "tap-repose"; mac = "52:54:00:00:00:01"; }];
          vsock.cid = 3;
          socket = "ch.sock";
        };
        # The kernel's ip= is handled by systemd-network-generator; the
        # console lives on ttyS0.
        boot.kernelParams = [ "console=ttyS0" ];
      }
      ({ config, pkgs, ... }: {
        home-manager.useGlobalPkgs = true;
        home-manager.useUserPackages = true;
        home-manager.backupFileExtension = "repose-bak";
        home-manager.users.dev = {
          home.username = "dev";
          home.homeDirectory = "/home/dev";
          home.stateVersion = "26.11";
        };
        # home-manager-dev runs before systemd-user-sessions, so every boot's
        # first login (hostd's SetupProject, the user's ssh) waited for its
        # activation: 1.2 s on host-01 for a generation that the volume already
        # had in place. When the generation dev's home last activated is this
        # one, there is nothing to link, so the unit ends at once; a changed
        # fragment or base is a new generation and activates as before
        # (DECISIONS I-231). The fallback is home-manager's own hm-setup-env,
        # line for line.
        systemd.services.home-manager-dev.serviceConfig.ExecStart =
          let
            hmStart = pkgs.writeScript "repose-hm-start" ''
              #! ${pkgs.bash}/bin/bash -el
              gen=$1
              current=$(readlink -e "$HOME/.local/state/home-manager/gcroots/current-home" || true)
              if [ "$current" = "$gen" ]; then
                echo "home-manager generation already active; nothing to do"
                exit 0
              fi
              eval "$(
                XDG_RUNTIME_DIR=''${XDG_RUNTIME_DIR:-/run/user/$UID} systemctl --user show-environment 2> /dev/null \
                | ${pkgs.gnused}/bin/sed -En '/^(DBUS_SESSION_BUS_ADDRESS|DISPLAY|WAYLAND_DISPLAY|XAUTHORITY|XDG_RUNTIME_DIR)=/s/^/export /p'
              )"
              exec "$gen/activate" --driver-version 1
            '';
          in
          lib.mkForce "${hmStart} ${config.home-manager.users.dev.home.activationPackage}";
      })
    ] ++ extraModules;
  };

  cfg = guestSystem.config;
  pkgs = guestSystem.pkgs;
  toplevel = cfg.system.build.toplevel;
  kernel = "${cfg.microvm.kernel.dev}/vmlinux";
  initrd = cfg.microvm.initrdPath;
  ch = cfg.microvm.cloud-hypervisor.package;
  baseCmdline = lib.concatStringsSep " " (cfg.microvm.kernelParams ++ extraKernelParams);
  opt = v: if v == null then "" else toString v;

  run = pkgs.writeShellScript "repose-guest-run" ''
    set -eu
    guest_id='${opt guestId}'
    ip='${opt ip}'
    gateway='${opt gatewayIp}'
    netmask='${netmask}'
    cid='${opt cid}'
    volume='${opt volumeDevice}'
    tap='${opt tap}'
    mac='${opt mac}'
    vcpu='${toString defaultVcpu}'
    mem='${toString defaultMem}'
    state_dir=""
    api_socket=""
    serial=""
    virtiofs_socket=""
    vsock_socket=""
    extra_cmdline=""
    hostname=""

    usage() {
      cat <<USAGE
    usage: run --guest-id ID --ip ADDR --gateway ADDR --cid N --volume DEV --tap NAME --mac MAC
               [--netmask MASK] [--vcpu N] [--mem MiB] [--hostname NAME]
               [--state-dir DIR]              default /var/lib/repose/guests/ID
               [--api-socket P]               default DIR/ch.sock
               [--serial tty|socket|P]        default socket=DIR/console.sock
               [--virtiofs-socket P]          default DIR/virtiofsd.sock
               [--vsock-socket P]             default DIR/vsock.sock
               [--extra-cmdline "..."]        appended to the kernel command line
               [-- CLOUD_HYPERVISOR_ARGS...]  appended verbatim
    The store share must be served on the virtiofs socket before start
    (bin/virtiofsd does that). The vsock socket is a unix socket that speaks
    Cloud Hypervisor's "CONNECT <port>" handshake; guestd listens on port 5000.
    USAGE
    }

    while [ "$#" -gt 0 ]; do
      case "$1" in
        --guest-id) guest_id="$2"; shift 2 ;;
        --ip) ip="$2"; shift 2 ;;
        --gateway) gateway="$2"; shift 2 ;;
        --netmask) netmask="$2"; shift 2 ;;
        --cid) cid="$2"; shift 2 ;;
        --volume) volume="$2"; shift 2 ;;
        --tap) tap="$2"; shift 2 ;;
        --mac) mac="$2"; shift 2 ;;
        --vcpu) vcpu="$2"; shift 2 ;;
        --mem) mem="$2"; shift 2 ;;
        --hostname) hostname="$2"; shift 2 ;;
        --state-dir) state_dir="$2"; shift 2 ;;
        --api-socket) api_socket="$2"; shift 2 ;;
        --serial) serial="$2"; shift 2 ;;
        --virtiofs-socket) virtiofs_socket="$2"; shift 2 ;;
        --vsock-socket) vsock_socket="$2"; shift 2 ;;
        --extra-cmdline) extra_cmdline="$2"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        --) shift; break ;;
        *) echo "run: unknown argument $1" >&2; usage >&2; exit 64 ;;
      esac
    done

    for v in guest_id ip gateway cid volume tap mac; do
      eval "val=\$$v"
      if [ -z "$val" ]; then
        echo "run: --$(echo "$v" | tr _ -) is required" >&2
        exit 64
      fi
    done
    [ -n "$state_dir" ] || state_dir="/var/lib/repose/guests/$guest_id"
    [ -n "$api_socket" ] || api_socket="$state_dir/ch.sock"
    [ -n "$virtiofs_socket" ] || virtiofs_socket="$state_dir/virtiofsd.sock"
    [ -n "$vsock_socket" ] || vsock_socket="$state_dir/vsock.sock"
    [ -n "$hostname" ] || hostname="repose-guest"
    case "$serial" in
      "") serial="socket=$state_dir/console.sock" ;;
      tty|null|off) ;;
      socket) serial="socket=$state_dir/console.sock" ;;
      *=*) ;;
      *) serial="file=$serial" ;;
    esac
    if [ ! -S "$virtiofs_socket" ]; then
      echo "run: virtiofs socket $virtiofs_socket is not listening; start bin/virtiofsd first" >&2
      exit 66
    fi
    if [ ! -e "$volume" ]; then
      echo "run: volume $volume does not exist" >&2
      exit 66
    fi
    mkdir -p "$state_dir"
    rm -f "$api_socket" "$vsock_socket"

    cmdline="console=ttyS0 earlyprintk=ttyS0 ${baseCmdline} ip=$ip::$gateway:$netmask:$hostname:eth0:off systemd.hostname=$hostname"
    if [ -n "$extra_cmdline" ]; then
      cmdline="$cmdline $extra_cmdline"
    fi

    exec ${ch}/bin/cloud-hypervisor \
      --cpus "boot=$vcpu" \
      --memory "size=''${mem}M,shared=on" \
      --kernel ${kernel} \
      --initramfs ${initrd} \
      --cmdline "$cmdline" \
      --seccomp true \
      --console null \
      --serial "$serial" \
      --vsock "cid=$cid,socket=$vsock_socket" \
      --disk "path=$volume,image_type=raw,direct=on,num_queues=$vcpu" \
      --fs "tag=ro-store,socket=$virtiofs_socket,num_queues=1,queue_size=1024" \
      --net "tap=$tap,mac=$mac" \
      --api-socket "$api_socket" \
      "$@"
  '';

  virtiofsdRun = pkgs.writeShellScript "repose-guest-virtiofsd" ''
    set -eu
    socket=""
    shared="/nix/store"
    sandbox="namespace"
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --socket) socket="$2"; shift 2 ;;
        --shared-dir) shared="$2"; shift 2 ;;
        --sandbox) sandbox="$2"; shift 2 ;;
        --) shift; break ;;
        *) echo "virtiofsd: unknown argument $1" >&2; exit 64 ;;
      esac
    done
    if [ -z "$socket" ]; then
      echo "usage: virtiofsd --socket PATH [--shared-dir DIR] [--sandbox namespace|chroot|none] [-- EXTRA...]" >&2
      exit 64
    fi
    rm -f "$socket"
    # Read-only is enforced by the guest mounting the tag ro and by the
    # host running this as a user with no write access to the store.
    exec ${pkgs.virtiofsd}/bin/virtiofsd \
      --socket-path "$socket" \
      --shared-dir "$shared" \
      --sandbox "$sandbox" \
      --cache auto \
      --inode-file-handles=never \
      --announce-submounts \
      "$@"
  '';

  shutdown = pkgs.writeShellScript "repose-guest-shutdown" ''
    set -eu
    api="''${1:?usage: shutdown API_SOCKET}"
    exec ${pkgs.curl}/bin/curl -s --unix-socket "$api" -X PUT http://localhost/api/v1/vm.power-button
  '';
in
pkgs.runCommand "repose-guest-runner-${baseVersion}-${class}"
{
  passthru = {
    inherit toplevel guestSystem class baseVersion;
    config = cfg;
    kernel = kernel;
    initrd = initrd;
  };
  meta.mainProgram = "run";
} ''
  mkdir -p $out/bin $out/share/repose
  ln -s ${run} $out/bin/run
  ln -s ${virtiofsdRun} $out/bin/virtiofsd
  ln -s ${shutdown} $out/bin/shutdown
  ln -s ${toplevel} $out/share/repose/system
  ln -s ${kernel} $out/share/repose/kernel
  ln -s ${initrd} $out/share/repose/initrd
  echo '${baseCmdline}' > $out/share/repose/cmdline
  echo '${baseVersion}' > $out/share/repose/base-version
  echo '${class}' > $out/share/repose/class
''
