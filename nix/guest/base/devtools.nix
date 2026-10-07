# What makes code that was not written for NixOS run in the guest:
#
# - nix-ld (DECISIONS I-218): a prebuilt, dynamically linked binary whose
#   interpreter is /lib64/ld-linux-x86-64.so.2 (prisma engines, biome,
#   esbuild's and turbo's npm binaries, playwright's own browsers, anything
#   a curl | sh installer drops) runs with the libraries below.
# - The pinned nixpkgs registry (I-218, I-531): `nixpkgs` in the flake
#   registries and NIX_PATH is the base's own nixpkgs source, which is in
#   the closure, so `nix profile add nixpkgs#air` evaluates offline and
#   substitutes from cache.nixos.org at the base's revision, and a flake
#   input that names `nixpkgs` locks offline to the same revision.
#   `nixpkgs.flake.source` is set by the flake (lib.nixosSystem does it
#   for the runner; the guestBase module does it for the VM tests); this
#   module only refuses a base without it.
# - command-not-found (I-219): an unknown command names the nixpkgs
#   package that has it and the two ways to add it, from a prebuilt
#   nix-index database (nix-index-database's small, /bin-only index), so a
#   lookup never touches the network. `nix-locate` is on PATH for other
#   tooling (the guest-side installer maps a binary to an attribute with
#   `nix-locate --minimal --no-group --type x --type s --whole-name --at-root
#   /bin/<cmd>`; there is no --top-level flag, and only top-level attributes
#   are listed unless --all is given).
{ config, lib, pkgs, ... }:
let
  cfg = config.repose;

  # Written by the guest-side installer while it installs a command in the
  # background: one command name per line. The handler answers "still
  # being installed" for a listed name instead of offering to install it.
  installingFile = "/run/user/$(id -u)/repose-installing";

  notFound = pkgs.writeShellApplication {
    name = "repose-command-not-found";
    runtimeInputs = [ pkgs.coreutils pkgs.gnugrep pkgs.gnused pkgs.gawk ]
      ++ lib.optional (cfg.nixIndexPackage != null) cfg.nixIndexPackage;
    text = ''
      cmd="''${1:-}"
      if [ -z "$cmd" ]; then
        exit 127
      fi
      installing="${installingFile}"
      if [ -r "$installing" ] && grep -qxF -- "$cmd" "$installing"; then
        printf '%s is still being installed; try again in a moment\n' "$cmd" >&2
        exit 127
      fi
      # Commands whose package is the wrong answer here (DECISIONS I-517).
      case "$cmd" in
        apt | apt-get | aptitude | dpkg | yum | dnf | apk | pacman | zypper | brew | port | snap)
          now="nix profile add nixpkgs#NAME"
          {
            printf '%s: command not found\n' "$cmd"
            printf '  %-*s  %s\n' "''${#now}" "$now" "install a package on this machine"
            printf '  %-*s  %s\n' "''${#now}" "repose config add NAME" "keep it on every rebuild (run this on your laptop)"
          } >&2
          exit 127
          ;;
        pip | pip3)
          venv="python3 -m venv .venv && . .venv/bin/activate"
          {
            printf '%s: command not found\n' "$cmd"
            printf '  %-*s  %s\n' "''${#venv}" "$venv" "a virtual environment, with pip in it"
            printf '  %-*s  %s\n' "''${#venv}" "uv tool install NAME" "install a Python command-line tool"
          } >&2
          exit 127
          ;;
        crontab | cron | crond | at)
          {
            printf '%s: command not found\n' "$cmd"
            printf 'This machine has no cron; a systemd user timer runs a job on a schedule: https://repose.herakraft.co/docs/machine#scheduled-jobs\n'
          } >&2
          exit 127
          ;;
      esac
      attrs=""
      case "$cmd" in
        */*) ;;
        *)
          if command -v nix-locate >/dev/null 2>&1; then
            # "air.out" -> "air"; one attribute per line, first seen first.
            # nixpkgs' test attributes are dropped, and top-level
            # attributes go before nested ones (python314Packages.pip).
            attrs=$(nix-locate --minimal --no-group --type x --type s --whole-name --at-root "/bin/$cmd" 2>/dev/null \
              | sed 's/\.[^.]*$//' | awk '!/^tests\./ && !seen[$0]++ { if (index($0, ".")) nested = nested $0 "\n"; else print } END { printf "%s", nested }' || true)
          fi
          ;;
      esac
      if [ -z "$attrs" ]; then
        printf '%s: command not found\n' "$cmd" >&2
        exit 127
      fi
      # The attribute named like the command first, else the first found.
      if printf '%s\n' "$attrs" | grep -qxF -- "$cmd"; then
        attr="$cmd"
      else
        attr=$(printf '%s\n' "$attrs" | head -n1)
      fi
      # grep -v finds nothing when one package has the command, and under
      # pipefail that would end the script before it printed a word (I-410).
      others=$(printf '%s\n' "$attrs" | { grep -vxF -- "$attr" || true; } | head -n3 | paste -sd, - | sed 's/,/, /g')
      # Plain and aligned (I-249): the not-found line bash users know,
      # then each command with what it does beside it.
      now="nix profile add nixpkgs#$attr"
      keep="repose config add $attr"
      {
        printf '%s: command not found\n' "$cmd"
        printf '  %-*s  %s\n' "''${#now}" "$now" "install it on this machine"
        printf '  %-*s  %s\n' "''${#now}" "$keep" "keep it on every rebuild (run this on your laptop)"
        if [ -n "$others" ]; then
          printf 'Other packages with %s: %s\n' "$cmd" "$others"
        fi
      } >&2
      exit 127
    '';
  };
in
{
  options.repose.nixpkgsLocked = lib.mkOption {
    type = lib.types.nullOr (lib.types.submodule {
      options = {
        rev = lib.mkOption { type = lib.types.str; };
        narHash = lib.mkOption { type = lib.types.str; };
        lastModified = lib.mkOption { type = lib.types.int; };
      };
    });
    default = null;
    description = ''
      The base's nixpkgs as its flake input locked it (rev, narHash,
      lastModified of github:NixOS/nixpkgs). The flake sets it; with null,
      the global registry points `nixpkgs` at the source path instead,
      which locks too but as a path a laptop cannot fetch.
    '';
  };

  options.repose.nixIndexPackage = lib.mkOption {
    type = lib.types.nullOr lib.types.package;
    default = null;
    description = ''
      nix-index with a prebuilt database (nix-index-database's
      `nix-index-with-small-db`); provides `nix-locate` for the
      command-not-found handler and other guest tooling. The flake sets it;
      null leaves unknown commands with the plain not-found message.
    '';
  };

  config = {
    assertions = [
      {
        assertion = config.nixpkgs.flake.source != null;
        message = "the guest base pins `nixpkgs` in the flake registry to its own nixpkgs: set nixpkgs.flake.source (DECISIONS I-218)";
      }
    ];

    # The base's nixpkgs for `nixpkgs#...` and <nixpkgs>, from the store
    # path already in the closure (nixos/modules/misc/nixpkgs-flake.nix).
    nixpkgs.flake.setFlakeRegistry = true;
    nixpkgs.flake.setNixPath = true;
    # No channels: NIX_PATH is `nixpkgs=flake:nixpkgs` alone, so
    # `nix-shell -p` stops warning that root's channels directory does not
    # exist, and `nix-channel` is gone (I-532).
    nix.channel.enable = false;
    # A global registry of one entry, written here: the default one is
    # downloaded from channels.nixos.org before resolving any `nixpkgs#`,
    # and without network that hung about a minute (I-218). Nix 2.34 locks
    # a flake's indirect inputs (`inputs.nixpkgs.url = "nixpkgs"`, or
    # `outputs = { self, nixpkgs }` with no inputs) against the global
    # registry only, never the system one, so an empty global registry
    # failed those with "cannot find flake 'flake:nixpkgs'" (I-531). The
    # entry is the base's own revision with its narHash, which nix finds
    # in the store, so the lock is made offline and names a github: URL
    # that works on a laptop and in CI. Other indirect names
    # (`templates#`, `home-manager#`) still need a `github:` URL.
    nix.settings.flake-registry = pkgs.writeText "flake-registry.json" (builtins.toJSON {
      version = 2;
      flakes = [{
        from = { type = "indirect"; id = "nixpkgs"; };
        to =
          if cfg.nixpkgsLocked != null then {
            type = "github";
            owner = "NixOS";
            repo = "nixpkgs";
            inherit (cfg.nixpkgsLocked) rev narHash lastModified;
          } else {
            type = "path";
            path = config.nixpkgs.flake.source;
          };
      }];
    });

    environment.systemPackages = [ notFound ]
      ++ lib.optional (cfg.nixIndexPackage != null) cfg.nixIndexPackage;

    # The handlers call the store path: a name looked up on PATH would,
    # with a PATH that lacks it, call the handler again for itself, one
    # process deeper each time (DECISIONS I-577).
    programs.bash.interactiveShellInit = ''
      command_not_found_handle() {
        ${notFound}/bin/repose-command-not-found "$1"
      }
    '';
    programs.zsh.interactiveShellInit = ''
      command_not_found_handler() {
        ${notFound}/bin/repose-command-not-found "$1"
      }
    '';
    programs.fish.interactiveShellInit = ''
      function fish_command_not_found
        ${notFound}/bin/repose-command-not-found $argv[1]
      end
    '';

    programs.nix-ld = {
      enable = true;
      # Added to the module's own set (zlib, zstd, libstdc++/libgcc_s, curl,
      # openssl, attr, libssh, bzip2, libxml2, acl, libsodium, util-linux
      # for libuuid and libmount, xz, systemd). The additions are what
      # common prebuilt binaries link: icu (dotnet, some language servers),
      # expat, libffi, ncurses and readline (interpreters and REPLs), krb5
      # (database drivers), and the shared libraries Chromium's headless
      # shell and Playwright's downloaded browsers load. All but a few are
      # in the closure already through chromium.
      libraries = with pkgs; [
        icu expat libffi ncurses readline krb5 e2fsprogs libgcc
        glib nss nspr dbus atk at-spi2-atk at-spi2-core cups libdrm libgbm libglvnd
        libxkbcommon pango cairo alsa-lib freetype fontconfig gtk3
        libx11 libxcb libxcomposite libxdamage libxext libxfixes libxrandr libxshmfence
      ];
    };
  };
}
