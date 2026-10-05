# Ecosystem tools that download their own prebuilt binaries, working in the
# guest with no per-project setup (DECISIONS I-228). nix-ld (devtools.nix,
# I-218) makes the binaries run; this module fixes the tools that decide
# what to download, or where, wrongly on NixOS:
#
# - Prisma reads ID=nixos from /etc/os-release and asks for "linux-nixos"
#   engines, which binaries.prisma.sh does not have. Its platform detection
#   takes no override (@prisma/get-platform: the target comes from
#   os-release alone; PRISMA_CLI_BINARY_TARGETS only adds downloads), but
#   every engine URL is built from PRISMA_ENGINES_MIRROR. The mirror here is
#   a redirect on the guest's loopback that sends linux-nixos paths to the
#   debian-openssl-3.0.x build of the same commit and every other path to
#   binaries.prisma.sh unchanged, so every Prisma version gets engines that
#   run through nix-ld, stored under its linux-nixos file names.
# - Playwright's browsers directory was the read-only store path, so
#   `npx playwright install` of any version hung ten minutes on a lock it
#   could not create. It is now the usual writable ~/.cache/ms-playwright,
#   seeded at boot with links to the base's packaged revisions, so the
#   version the base carries needs no download and any other downloads
#   like on a laptop. The MCP server sets its own path in its wrapper.
# - The nixpkgs python finds no libstdc++ for a manylinux wheel (numpy,
#   pandas, grpcio): the system python3 runs with nix-ld's library set on
#   LD_LIBRARY_PATH, named by its own path (exec -a), so a venv made from
#   it (python3 -m venv, uv venv) links back to the wrapper and keeps it.
# - Build scripts that ask pkg-config for a system library (openssl-sys in
#   every reqwest/native-tls crate, cgo `#cgo pkg-config:`, meson) find
#   openssl, zlib, sqlite and libffi, and, for Rails' native gems (I-265),
#   libyaml, libpq, libxml2, libxslt and the MySQL client, whose
#   pg_config and mysql_config are on PATH.
{ config, lib, pkgs, ... }:
let
  # Loopback and under 1024: never auto-forwarded by the CLI (I-199), and
  # not a port a user's dev server takes.
  prismaMirrorAddress = "127.0.0.1:850";
  prismaUpstream = "https://binaries.prisma.sh";
  # The build that runs on the guest: glibc, OpenSSL 3 (nix-ld's openssl).
  prismaTarget = "debian-openssl-3.0.x";

  # One connection per instance (Accept=yes): read the request line, answer
  # a redirect, close. Nothing is proxied, cached or logged.
  prismaRedirect = pkgs.writeShellScript "repose-prisma-engines" ''
    set -u
    line=""
    IFS= read -r -t 10 line || true
    line=''${line%$'\r'}
    # Drain the headers so the client sees a clean close.
    while IFS= read -r -t 2 h; do
      h=''${h%$'\r'}
      [ -z "$h" ] && break
    done
    method=''${line%% *}
    rest=''${line#* }
    path=''${rest%% *}
    reply() {
      printf 'HTTP/1.1 %s\r\nContent-Length: 0\r\nConnection: close\r\n%s\r\n' "$1" "$2"
      exit 0
    }
    case "$method" in
      GET|HEAD) ;;
      *) reply "405 Method Not Allowed" "" ;;
    esac
    case "$path" in
      /*/../*|*/..|*//*|*[!A-Za-z0-9._/-]*|"") reply "400 Bad Request" "" ;;
      /*) ;;
      *) reply "400 Bad Request" "" ;;
    esac
    # /<channel>/<commit>/linux-nixos/<file> -> the debian build.
    if [[ "$path" =~ ^/([^/]+)/([^/]+)/linux-nixos/(.+)$ ]]; then
      path="/''${BASH_REMATCH[1]}/''${BASH_REMATCH[2]}/${prismaTarget}/''${BASH_REMATCH[3]}"
    fi
    reply "302 Found" "Location: ${prismaUpstream}$path"$'\r\n'
  '';

  playwrightBrowsers = pkgs.reposePlaywrightBrowsers;
  playwrightDir = "/home/dev/.cache/ms-playwright";

  # Links each packaged revision into the writable directory unless the user
  # has a real one there, repoints links left by an older base, and removes
  # links whose store path is gone. Idempotent; runs at every boot.
  playwrightSeed = pkgs.writeShellScript "repose-playwright-seed" ''
    set -eu
    dir=${playwrightDir}
    mkdir -p "$dir"
    for src in ${playwrightBrowsers}/*; do
      name=''${src##*/}
      dst="$dir/$name"
      if [ -L "$dst" ]; then
        case "$(readlink "$dst")" in
          /nix/store/*) ln -sfn "$src" "$dst" ;;
        esac
      elif [ ! -e "$dst" ]; then
        ln -s "$src" "$dst"
      fi
    done
    for l in "$dir"/*; do
      if [ -L "$l" ] && [ ! -e "$l" ]; then
        case "$(readlink "$l")" in
          /nix/store/*) rm -f "$l" ;;
        esac
      fi
    done
  '';

  nixLdLib = "/run/current-system/sw/share/nix-ld/lib";
  python = pkgs.python312;
  pythonVersioned = "python${python.pythonVersion}";
  pythonWrapper = pkgs.writeShellScript "repose-python" ''
    lib="''${NIX_LD_LIBRARY_PATH:-${nixLdLib}}"
    case ":''${LD_LIBRARY_PATH:-}:" in
      *":$lib:"*) ;;
      *) export LD_LIBRARY_PATH="''${LD_LIBRARY_PATH:+$LD_LIBRARY_PATH:}$lib" ;;
    esac
    exec -a "$0" ${python}/bin/${pythonVersioned} "$@"
  '';
  # Wins over python312's own bin/python* in the system path (hiPrio).
  pythonCompat = pkgs.runCommand "repose-python-compat" { } ''
    mkdir -p $out/bin
    for n in python python3 ${pythonVersioned}; do
      ln -s ${pythonWrapper} $out/bin/$n
    done
  '';

  # zlib installs its .pc under share/, the others under lib/. libyaml,
  # libpq, libxml2, libxslt and the MySQL client are what Rails' native
  # gems ask for (psych, pg, nokogiri with system libraries, mysql2;
  # DECISIONS I-265).
  pkgConfigPath = lib.concatStringsSep ":" (lib.concatMap
    (p: [ "${lib.getDev p}/lib/pkgconfig" "${lib.getDev p}/share/pkgconfig" ])
    (with pkgs; [ openssl zlib sqlite libffi libyaml libpq libxml2 libxslt libmysqlclient ]));

  # pg and mysql2 look for pg_config and mysql_config before pkg-config
  # (mysql2 never asks pkg-config), so both are on PATH, each naming its
  # library's own directories. Only these two commands: the rest of
  # libmysqlclient's dev output stays off PATH.
  nativeGemConfigs = pkgs.runCommand "repose-native-gem-configs" { } ''
    mkdir -p $out/bin
    ln -s ${pkgs.libpq.pg_config}/bin/pg_config $out/bin/pg_config
    ln -s ${lib.getDev pkgs.libmysqlclient}/bin/mysql_config $out/bin/mysql_config
    ln -s ${lib.getDev pkgs.libmysqlclient}/bin/mariadb_config $out/bin/mariadb_config
  '';
in
{
  options.repose.compat.prismaMirror = lib.mkOption {
    type = lib.types.str;
    readOnly = true;
    default = "http://${prismaMirrorAddress}";
    description = "The Prisma engines redirect every shell's PRISMA_ENGINES_MIRROR names (I-228).";
  };

  config = {
    environment.systemPackages = [ (lib.hiPrio pythonCompat) nativeGemConfigs ];

    environment.variables = {
      PRISMA_ENGINES_MIRROR = config.repose.compat.prismaMirror;
      PLAYWRIGHT_BROWSERS_PATH = playwrightDir;
      PKG_CONFIG_PATH = pkgConfigPath;
    };

    # The interpreter paths scripts and Makefiles written on other Linux
    # systems name (I-521): `#!/bin/bash`, `#!/usr/bin/python3`,
    # `SHELL := /bin/bash`. Links into the system profile, so they follow
    # a base switch; python3 there is the nix-ld wrapper above. /bin/sh
    # and /usr/bin/env stay NixOS's. Not envfs, which puts a FUSE mount
    # on the boot path (I-231).
    systemd.tmpfiles.rules = [
      "L+ /bin/bash - - - - /run/current-system/sw/bin/bash"
      "L+ /usr/bin/bash - - - - /run/current-system/sw/bin/bash"
      "L+ /usr/bin/python3 - - - - /run/current-system/sw/bin/python3"
      "L+ /usr/bin/python - - - - /run/current-system/sw/bin/python3"
      "L+ /usr/bin/perl - - - - /run/current-system/sw/bin/perl"
    ];

    # python-build-standalone, the CPython uv downloads, has
    # openssl_cafile=/etc/ssl/cert.pem compiled in; without the file every
    # https request from it fails certificate checks (I-521).
    environment.etc."ssl/cert.pem".source = config.environment.etc."ssl/certs/ca-certificates.crt".source;

    systemd.sockets.repose-prisma-engines = {
      description = "repose: Prisma engines redirect on ${prismaMirrorAddress}";
      wantedBy = [ "sockets.target" ];
      socketConfig = {
        ListenStream = prismaMirrorAddress;
        Accept = true;
        MaxConnections = 64;
      };
    };

    systemd.services."repose-prisma-engines@" = {
      description = "repose: Prisma engines redirect (one connection)";
      serviceConfig = {
        ExecStart = prismaRedirect;
        StandardInput = "socket";
        StandardOutput = "socket";
        StandardError = "null";
        DynamicUser = true;
        PrivateTmp = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        NoNewPrivileges = true;
        RuntimeMaxSec = 30;
      };
    };

    systemd.services.repose-playwright-seed = {
      description = "repose: link the base's Playwright browsers into ~/.cache/ms-playwright";
      wantedBy = [ "multi-user.target" ];
      after = [ "local-fs.target" ];
      serviceConfig = {
        Type = "oneshot";
        User = "dev";
        Group = "dev";
        ExecStart = playwrightSeed;
      };
    };
  };
}
