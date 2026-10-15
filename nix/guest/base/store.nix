# The guest's own nix: a daemon that installs into the writable overlay
# (/nix/.rw-store) with cache.nixos.org as the only substituter, plus
# repose-pin-profile, which copies the closure of dev's profile up into the
# overlay so a host garbage collection of a path the profile shares with an
# old base can never break the guest (docs/workstreams/02-guest-base.md
# "The shared store and the overlay"), and repose-store-gc, which deletes,
# weekly, the unused paths that exist only in the overlay (I-529).
#
# What a binary built in the guest links against is no longer copied up.
# That copy (I-533) cost 31 to 87 s of every first boot and 75 MB of every
# volume, and it protected nothing: the host roots the closure each guest
# runs, so a collection cannot take a library of a live base, and a path
# the current closure really lost would fail the boot at virtiofsd before
# any copy could help. The libraries are still named by a gc root each, so
# the guest knows which eleven matter, and the one reachable loss - an old
# base's path, after a nixpkgs bump moved it and the host collected the
# closure - is fetched back from cache.nixos.org by repose-store-repair in
# 0.3 s a path (DECISIONS I-718, I-719, amending I-533 and I-706).
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
  # crates and Python extensions link them). Nothing is copied for these
  # any more (I-718); each gets a gc root naming it, which keeps the
  # guest's own collection off it, records for the next base which paths
  # a checkout's binaries may still name - roots from earlier bases stay -
  # and leaves a dangling symlink to read when one is really gone.
  linkTargets = map lib.getLib ([ pkgs.gcc.libc pkgs.gcc.cc ]
    ++ (with pkgs; [ openssl zlib sqlite libffi libyaml libpq libxml2 libxslt libmysqlclient ]));
  linkTargetsRoots = "/nix/var/nix/gcroots/repose-link-targets";

  # Whether a store path is really here. Not `test -e`: a collection on the
  # host empties the directory under a running guest's virtiofs mount, so
  # the path is still there and holds nothing (the class of problem I-652
  # found with nix-store --optimise). An empty directory is as broken as a
  # missing one, and tells the repair to ask for a restart rather than try
  # (nix refuses to repair a path that exists). A store path that is a
  # single file is itself the content.
  storePathHere = ''
    storePathHere() {
      [ -e "$1" ] || return 1
      [ -d "$1" ] || return 0
      [ -n "$(${pkgs.findutils}/bin/find "$1" -mindepth 1 -maxdepth 2 \( -type f -o -type l \) -print -quit)" ]
    }
  '';

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
      # A gc root for each library a guest-built binary links against, kept
      # under its own name so neither repose-store-gc nor a later base
      # forgets one a checkout still names. Roots from earlier bases stay.
      # Nothing is copied: 11 symlinks, no bytes (I-718, amending I-533).
      mkdir -p ${linkTargetsRoots}
      for t in ${lib.concatStringsSep " " linkTargets}; do
        ln -sfn "$t" "${linkTargetsRoots}/$(basename "$t")"
      done
      targets=()
      for profile in \
        /home/dev/.local/state/nix/profiles/profile \
        /nix/var/nix/profiles/per-user/dev/profile \
        /home/dev/.nix-profile; do
        [ -e "$profile" ] || continue
        target=$(readlink -f "$profile") || continue
        targets+=("$target")
      done
      # A fresh guest has no profile at all, which is why this unit cost
      # nothing before I-533 and costs nothing again (DECISIONS I-718).
      if [ "''${#targets[@]}" -eq 0 ]; then
        echo "repose-pin-profile: dev has no nix profile; nothing to pin"
        exit 0
      fi
      pinned=0
      todo=$(mktemp)
      trap 'rm -f "$todo"' EXIT
      # Every path in these closures that is not yet in the upper dir is
      # copied up. Paths already there (installed by the guest's own
      # daemon, or pinned before) are skipped by the -e test, so an
      # unchanged profile costs nothing. One nix-store call for every
      # target: one call per target cost 8.50 s of a boot against 1.78 s
      # for all of them together (I-718).
      for p in $(nix-store -qR "''${targets[@]}" 2>/dev/null | sort -u); do
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

  # Fetching back a library a program in the checkout names and this
  # machine no longer has (DECISIONS I-719). It happens when a base moved
  # a library's store path, the machine has been on a newer base long
  # enough for the host to collect the old one, and a binary compiled
  # before that still names it: the program fails with "error while
  # loading shared libraries: libpq.so.5: cannot open shared object file".
  #
  # `nix-store --realise` is not the fix and validity is not the test: the
  # path stays valid in the guest's database, so realise prints it and
  # exits 0 having fetched nothing. Only `nix-store --repair-path` fetches
  # it, the nix daemon refuses that operation ("operation 'repairPath' is
  # not supported by store 'daemon'"), and the store is bind-mounted
  # read-only, so it runs as root with NIX_REMOTE unset and /nix/store
  # remounted writable in a private mount namespace - the same dance the
  # pin's copy-up does. Measured in a guest with the bytes really gone:
  # 315 ms a library cold, 230 ms warm; 8.3 s to fail with no egress.
  repairOne = pkgs.writeShellScript "repose-store-repair-one" ''
    set -eu
    ${pkgs.util-linux}/bin/mount -o remount,bind,rw /nix/store
    NIX_REMOTE= exec ${pkgs.nix}/bin/nix-store --repair-path "$1"
  '';

  repair = pkgs.writeShellApplication {
    name = "repose-store-repair";
    runtimeInputs = [ pkgs.nix pkgs.coreutils pkgs.findutils pkgs.gnugrep pkgs.gnused pkgs.binutils pkgs.util-linux ];
    text = ''
      ${storePathHere}
      if [ "$#" -eq 0 ]; then
        echo "usage: repose-store-repair PROGRAM | /nix/store/PATH ... | --link-targets" >&2
        echo "downloads again the store paths a program needs and this machine has lost" >&2
        exit 2
      fi
      # Root, before any of the work: the daemon refuses the repair.
      if [ "$(id -u)" -ne 0 ]; then
        exec sudo -n -- "$0" "$@"
      fi
      want=$(mktemp)
      trap 'rm -f "$want"' EXIT
      # The top level of a store path is what nix repairs.
      top() { echo "$1" | cut -d/ -f1-4; }
      for arg in "$@"; do
        case "$arg" in
          # Every library a binary built here can link against: the gc
          # roots the pin writes, including the ones earlier bases left,
          # which is the set a checkout's older binaries may still name.
          --link-targets)
            for l in ${linkTargetsRoots}/*; do
              [ -L "$l" ] || continue
              top "$(readlink "$l")" >> "$want"
            done
            continue
            ;;
          /nix/store/*)
            top "$arg" >> "$want"
            continue
            ;;
        esac
        if [ ! -e "$arg" ]; then
          echo "repose-store-repair: $arg: no such file" >&2
          exit 1
        fi
        # What the program names: its ELF interpreter and the store paths
        # on its RUNPATH, which is where a guest-built binary's libraries
        # are. readelf, not ldd: ldd needs the libraries it reports on.
        interp=$(readelf -l "$arg" 2>/dev/null | sed -n 's/.*interpreter: \(.*\)]/\1/p')
        runpath=$(readelf -d "$arg" 2>/dev/null | sed -n 's/.*R\(UN\)*PATH.*\[\(.*\)\]/\2/p' | tr ':' ' ')
        for p in $interp $runpath; do
          case "$p" in
            /nix/store/*) top "$p" >> "$want" ;;
          esac
        done
      done
      missing=0
      failed=0
      while IFS= read -r p; do
        storePathHere "$p" && continue
        # There but empty: the host collected it while this machine was
        # running and the directory went out from under the mount. nix
        # will not repair a path that exists, and a stop and start brings
        # the path back cleanly absent, so ask for that rather than fail.
        # `repose stop` then `repose start`: repose has no restart verb.
        if [ -e "$p" ]; then
          missing=$((missing + 1))
          failed=$((failed + 1))
          # Name the machine, which is what the user types from their
          # laptop: hostd sets the guest's hostname to the project slug
          # (ch.Hostname, create.go). It falls back to the base's own
          # "repose-guest" for a slug that is not a DNS label, which names
          # nothing, so say the commands bare in that case.
          case "''${HOSTNAME:-}" in
            "" | repose-guest) m="" ;;
            *) m=" $HOSTNAME" ;;
          esac
          echo "repose-store-repair: $p is empty: it was collected while this machine was running. Run repose stop$m then repose start$m from your laptop, and run this again." >&2
          continue
        fi
        missing=$((missing + 1))
        echo "repose-store-repair: downloading $p"
        if out=$(unshare -m --propagation private ${repairOne} "$p" 2>&1); then
          continue
        fi
        failed=$((failed + 1))
        echo "$out" >&2
        if echo "$out" | grep -qiE "could not connect|unable to download|resolve host|timeout was reached|SSL connect error"; then
          echo "repose-store-repair: $p: this machine cannot reach https://cache.nixos.org right now. Run this again once it can." >&2
        else
          echo "repose-store-repair: $p could not be repaired. Building the program again gives it the libraries this base has." >&2
        fi
      done < <(sort -u "$want")
      if [ "$missing" -eq 0 ]; then
        echo "repose-store-repair: nothing to repair; this machine has every library these name"
      fi
      [ "$failed" -eq 0 ]
    '';
  };

  # The guest's own garbage collection (DECISIONS I-529). Deleting a path
  # that is also in the lower layer (/nix/.ro-store, the host's copy)
  # leaves an overlayfs whiteout in the upper dir, which hides the host's
  # copy. guestd roots every such path the database lists (I-588) and a
  # start removes whiteouts (I-587, boot.nix), so a whole-store GC is safe
  # now; this one still deletes only dead paths that exist in the upper
  # dir alone, and leaves the system's generations to hostd.
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
    # No scheduled whole-store GC (nix.gc, min-free): repose-store-gc
    # below deletes upper-only paths. One a user or agent runs is safe:
    # guestd roots the paths the host shares (I-588).
    gc.automatic = false;
    optimise.automatic = false;
  };

  environment.systemPackages = [ pin repair ];

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
    # (I-67); before that `nix-store -qR` of a profile path finds nothing.
    after = [ "repose-paths.service" ];
    wants = [ "repose-paths.service" ];
    serviceConfig = {
      Type = "oneshot";
      # A fresh guest has no profile, so this exits at once and needs no
      # deferral; I-706's wait for guestd's set-up stamp went with the
      # link-target copy it was there for (DECISIONS I-718). What is left
      # is dev's own profile, whose closure the guest's daemon has already
      # written to the upper dir in all but the offline-install case.
      ExecStart = "${pin}/bin/repose-pin-profile";
      Nice = 10;
      IOSchedulingClass = "idle";
    };
  };

  # Once a boot, four minutes in, check the eleven libraries a binary
  # compiled here links against and fetch back any the host has collected
  # (DECISIONS I-719). The common case is eleven stats and no work. The
  # four minutes are I-706's reason - nothing on the create path, where a
  # user is waiting, and least of all a network call - and are past the
  # 3 min 15 s hostd allows SetupProject, so a create is always finished
  # first. It is a timer and not a wait inside the unit, because such a
  # wait would hold the boot on every ordinary start: hostd sends
  # SetupProject only on a create (create.go is its one caller), so the
  # stamp never arrives on a start and the wait would run to its 180 s
  # fallback, and these oneshots are in multi-user.target's path, not
  # beside it - in the guest-base run the target was reached 2 ms after
  # the last of them finished. A failure is logged and ignored - no egress
  # must never cost a boot - and the machine is usable either way: a
  # rebuild in the checkout gets the base's own libraries.
  systemd.services.repose-store-repair = {
    description = "repose: fetch back store paths this machine has lost";
    after = [ "repose-paths.service" "nix-daemon.socket" ];
    wants = [ "repose-paths.service" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "-${repair}/bin/repose-store-repair --link-targets";
      Nice = 10;
      IOSchedulingClass = "idle";
    };
  };

  systemd.timers.repose-store-repair = {
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnBootSec = "4min";
      AccuracySec = "1min";
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
