# The guest's own nix: a daemon that installs into the writable overlay
# (/nix/.rw-store) with cache.nixos.org as the only substituter, plus
# repose-pin-profile, which copies the closure of dev's profile up into the
# overlay so a host garbage collection of a path the profile shares with an
# old base can never break the guest (docs/workstreams/02-guest-base.md
# "The shared store and the overlay").
#
# The same service also pins the libraries a binary built in the guest
# links against (DECISIONS I-533), and repose-store-gc deletes, weekly,
# the unused paths that exist only in the overlay (I-529).
#
# Copy-up is done by touching every file of every closure path with the
# mtime the store already gives them (epoch 1): overlayfs copies a file up
# on any attribute change, so this duplicates the bytes into the upper
# directory without changing anything visible. The upper directory is read
# from /proc/mounts, so the same script works for the microvm layout
# (/nix/.rw-store/store) and the NixOS test layout (/nix/.rw-store/upper).
{ config, lib, pkgs, ... }:
let
  # The overlay's upper and lower directories, read from /proc/mounts, with
  # the initrd's prefix removed: mounted in the initrd, the options still
  # name its view of the real root (/sysroot with a systemd initrd,
  # /mnt-root with the scripted one the guest boots with since I-231).
  # `overlayDir upperdir` prints the upper directory (nothing when
  # /nix/store is no overlay); `overlayDir lowerdir` the lower ones, one
  # per line.
  overlayDir = ''
    overlayDir() {
      awk '$2 == "/nix/store" && $3 == "overlay" { print $4; exit }' /proc/mounts | tr ',' '\n' \
        | grep "^$1=" | head -n1 | cut -d= -f2- | tr ':' '\n' \
        | sed -e 's|^/sysroot/|/|' -e 's|^/mnt-root/|/|' || true
    }
  '';

  # What a program built in the guest is linked against: the gcc wrapper's
  # glibc (the ELF interpreter and libc.so) and libgcc_s/libstdc++, and the
  # libraries pkg-config finds (compat.nix; cgo, node-gyp, cargo's -sys
  # crates and Python extensions link them). Each new base can bring new
  # store paths for these while the binaries in a checkout keep the old
  # ones; the host keeps only the closures it still roots (I-463), so
  # these are copied into the overlay and rooted there (I-533). Runtime
  # outputs only: headers matter to a compile, which a later build redoes
  # with the base's own wrapper.
  linkTargets = map lib.getLib ([ pkgs.gcc.libc pkgs.gcc.cc ]
    ++ (with pkgs; [ openssl zlib sqlite libffi libyaml libpq libxml2 libxslt libmysqlclient ]));
  linkTargetsRoots = "/nix/var/nix/gcroots/repose-link-targets";

  # The copy-up itself: /nix/store is bind-mounted read-only over the
  # overlay (NixOS does that for every system), so the touches run in a
  # private mount namespace with the store remounted writable, exactly as
  # nix-daemon does for its own writes. Failures here are real errors.
  copyUp = pkgs.writeShellScript "repose-pin-copy-up" ''
    set -eu
    ${pkgs.util-linux}/bin/mount -o remount,bind,rw /nix/store
    while IFS= read -r p; do
      [ -n "$p" ] || continue
      ${pkgs.findutils}/bin/find "$p" -exec ${pkgs.coreutils}/bin/touch -h -d @1 {} +
    done
  '';

  pin = pkgs.writeShellApplication {
    name = "repose-pin-profile";
    runtimeInputs = [ pkgs.nix pkgs.coreutils pkgs.findutils pkgs.gnugrep pkgs.gnused pkgs.gawk pkgs.util-linux ];
    text = ''
      ${overlayDir}
      upper=$(overlayDir upperdir)
      if [ -z "$upper" ]; then
        echo "repose-pin-profile: /nix/store is not an overlay; nothing to pin" >&2
        exit 0
      fi
      pinned=0
      todo=$(mktemp)
      trap 'rm -f "$todo"' EXIT
      # The link targets are rooted under their own names, so neither
      # repose-store-gc nor a later base's pin forgets one a binary in a
      # checkout still names (I-533). Roots from earlier bases stay.
      mkdir -p ${linkTargetsRoots}
      for t in ${lib.concatStringsSep " " linkTargets}; do
        ln -sfn "$t" "${linkTargetsRoots}/$(basename "$t")"
      done
      targets=(${lib.concatStringsSep " " linkTargets})
      for profile in \
        /home/dev/.local/state/nix/profiles/profile \
        /nix/var/nix/profiles/per-user/dev/profile \
        /home/dev/.nix-profile; do
        [ -e "$profile" ] || continue
        target=$(readlink -f "$profile") || continue
        targets+=("$target")
      done
      # Every path in these closures that is not yet in the upper dir is
      # copied up. Paths already there (installed by the guest's own
      # daemon, or pinned before) are skipped by the -e test, so an
      # unchanged glibc costs nothing.
      for p in $(for t in "''${targets[@]}"; do nix-store -qR "$t" 2>/dev/null || true; done | sort -u); do
        name=$(basename "$p")
        if [ -e "$upper/$name" ]; then
          continue
        fi
        echo "$p" >> "$todo"
        pinned=$((pinned + 1))
      done
      if [ "$pinned" -gt 0 ]; then
        unshare -m --propagation private ${copyUp} < "$todo"
      fi
      echo "repose-pin-profile: pinned $pinned store paths into $upper"
    '';
  };

  # The guest's own garbage collection (DECISIONS I-529). A whole-store GC
  # is unsafe here: deleting a path that is also in the lower layer
  # (/nix/.ro-store, the host's copy) leaves an overlayfs whiteout in the
  # upper dir, which hides the host's copy for good, and a later switch or
  # rollback that registers that path again (I-67; I-463 keeps up to 16
  # earlier closures and the rev-* roots in view) finds it empty. So this
  # deletes only dead paths that exist in the upper dir alone.
  gc = pkgs.writeShellApplication {
    name = "repose-store-gc";
    runtimeInputs = [ pkgs.nix pkgs.coreutils pkgs.findutils pkgs.gnugrep pkgs.gnused pkgs.gawk pkgs.util-linux ];
    text = ''
      export LC_ALL=C
      # dev's own profile history first, as dev, so the closures only old
      # generations held become dead. The system's generations are hostd's.
      for profile in /home/dev/.local/state/nix/profiles/profile /nix/var/nix/profiles/per-user/dev/profile; do
        [ -L "$profile" ] || continue
        runuser -u dev -- env HOME=/home/dev nix profile wipe-history --profile "$profile" --older-than 14d \
          || echo "repose-store-gc: could not delete old generations of $profile" >&2
      done

      ${overlayDir}
      upper=$(overlayDir upperdir)
      lowers=$(overlayDir lowerdir)
      if [ -z "$upper" ]; then
        echo "repose-store-gc: /nix/store is not an overlay; nothing to delete" >&2
        exit 0
      fi
      work=$(mktemp -d)
      trap 'rm -rf "$work"' EXIT

      in_lower() {
        local d
        while IFS= read -r d; do
          if [ -n "$d" ] && { [ -e "$d/$1" ] || [ -L "$d/$1" ]; }; then
            return 0
          fi
        done <<< "$lowers"
        return 1
      }

      select_paths() {
        nix-store --gc --print-dead 2>/dev/null | sort -u > "$work/dead"
        : > "$work/candidates"
        while IFS= read -r p; do
          name=''${p##*/}
          # A character device in the upper dir is a whiteout: the path is
          # already gone from this guest's view.
          if [ -c "$upper/$name" ]; then continue; fi
          if ! [ -e "$upper/$name" ] && ! [ -L "$upper/$name" ]; then continue; fi
          if in_lower "$name"; then continue; fi
          echo "$p" >> "$work/candidates"
        done < "$work/dead"
        # nix-store --delete refuses a path a dead path outside the list
        # still refers to, and then deletes nothing, so the closure of the
        # dead paths that stay is left out too.
        comm -23 "$work/dead" "$work/candidates" > "$work/stay"
        if [ -s "$work/stay" ]; then
          xargs -r nix-store --check-validity --print-invalid < "$work/stay" | sort -u > "$work/invalid" || true
          comm -23 "$work/stay" "$work/invalid" | xargs -r nix-store -qR 2>/dev/null | sort -u > "$work/held" || true
          comm -23 "$work/candidates" "$work/held" > "$work/delete"
        else
          cp "$work/candidates" "$work/delete"
        fi
      }

      # One retry: --delete refuses the whole list when a path became live
      # between the listing and the deletion.
      for attempt in 1 2; do
        select_paths
        if ! [ -s "$work/delete" ]; then
          echo "repose-store-gc: nothing to delete"
          exit 0
        fi
        mapfile -t paths < "$work/delete"
        if nix-store --delete "''${paths[@]}"; then
          exit 0
        fi
        echo "repose-store-gc: deletion attempt $attempt refused" >&2
      done
      exit 1
    '';
  };
in
{
  nix = {
    enable = true;
    package = pkgs.nix;
    settings = {
      experimental-features = [ "nix-command" "flakes" ];
      substituters = [ "https://cache.nixos.org" ];
      trusted-public-keys = [ "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=" ];
      sandbox = true;
      auto-optimise-store = false;
      # The guest builds inside its own vcpu/RAM caps; a user's nix build
      # affects only their guest.
      max-jobs = "auto";
      cores = 0;
      # dev already has passwordless sudo (R3-13), so trust gives no new
      # privilege; it lets a flake's nixConfig substituters and `cachix
      # use` work (I-530). cache.nixos.org stays the default.
      trusted-users = [ "root" "dev" ];
      allowed-users = [ "root" "dev" ];
    };
    # Never a whole-store GC inside the guest (nix.gc, min-free,
    # nix-collect-garbage): deleting a path the host also has writes a
    # whiteout into the overlay's upper dir, which keeps hiding the host's
    # copy after a later switch or rollback registers that path again
    # (I-67, I-463). repose-store-gc below deletes upper-only paths.
    gc.automatic = false;
    optimise.automatic = false;
  };

  environment.systemPackages = [ pin ];

  # At every boot and switch, and whenever dev's profile changes, pin, in
  # the background. It used to run inside the activation script, which at
  # boot held the whole start up while it copied: 98 paths after a stop cut
  # the previous pass short took a start from 5 s to 26 s (I-234). Nothing
  # is safer for waiting: a pin protects against a later host garbage
  # collection, never one that already happened. The path unit fires on
  # `nix profile add`.
  system.activationScripts.repose-pin-profile = {
    deps = [ "users" ];
    text = ''
      # During a switch systemd is up; at boot the unit's own WantedBy runs it.
      # The switch runs this before it reloads systemd, so starting the unit
      # here would run the previous base's script, which knows nothing of
      # this base's link targets (I-533). A transient unit runs this base's.
      if [ -d /run/systemd/system ]; then
        ${pkgs.systemd}/bin/systemd-run --no-block --collect --quiet \
          --description="repose: copy dev's profile closure into the store overlay" \
          -p Nice=10 -p IOSchedulingClass=idle \
          ${pin}/bin/repose-pin-profile || true
      fi
    '';
  };

  systemd.services.repose-pin-profile = {
    description = "repose: copy dev's profile closure into the store overlay";
    wantedBy = [ "multi-user.target" ];
    # At boot the system's paths are valid only once hostd registered them
    # (I-67); before that `nix-store -qR` of a link target finds nothing.
    after = [ "repose-paths.service" ];
    wants = [ "repose-paths.service" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "${pin}/bin/repose-pin-profile";
      Nice = 10;
      IOSchedulingClass = "idle";
    };
  };

  systemd.services.repose-store-gc = {
    description = "repose: delete unused nix store paths only the overlay holds";
    after = [ "repose-paths.service" "nix-daemon.socket" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "${gc}/bin/repose-store-gc";
      Nice = 19;
      IOSchedulingClass = "idle";
    };
  };

  systemd.timers.repose-store-gc = {
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnCalendar = "weekly";
      RandomizedDelaySec = "1h";
      Persistent = true;
    };
  };

  systemd.paths.repose-pin-profile = {
    description = "repose: watch dev's nix profile for changes";
    wantedBy = [ "multi-user.target" ];
    pathConfig = {
      PathChanged = [
        "/home/dev/.local/state/nix/profiles"
        "/nix/var/nix/profiles/per-user/dev"
      ];
      Unit = "repose-pin-profile.service";
    };
  };

  systemd.tmpfiles.rules = [
    "d /home/dev/.local/state 0755 dev dev -"
    "d /home/dev/.local/state/nix 0755 dev dev -"
    "d /home/dev/.local/state/nix/profiles 0755 dev dev -"
    "d /nix/var/nix/profiles/per-user/dev 0755 dev dev -"
  ];
}
