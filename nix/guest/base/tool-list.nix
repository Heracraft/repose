# The tool list, as a function of pkgs so nix/flake.nix's devShell and the
# guest base share it. Moved from packages/core/flake.nix (curl, wget, jq,
# ripgrep, node 24, pnpm, python 3.12, uv, go, rustup, just) plus the
# additions docs/workstreams/02-guest-base.md names, plus the C toolchain
# and the everyday CLIs of DECISIONS I-218.
pkgs:
let
  # psql and the dump tools, without the server binaries on PATH: a
  # database is a menu service (`repose.system`), not a base tool.
  postgresqlClient = pkgs.runCommand "postgresql-client-${pkgs.postgresql.version}" { } ''
    mkdir -p $out/bin
    for b in psql pg_dump pg_dumpall pg_restore pg_isready; do
      ln -s ${pkgs.postgresql}/bin/$b $out/bin/$b
    done
  '';
  # yarn and yarnpkg from the corepack the system's `corepack` is (I-520):
  # `corepack enable` cannot write its shims next to node in the store.
  # Each runs the version a project's packageManager pins, else yarn 1.
  # pnpm stays nixpkgs's.
  yarnCorepack = pkgs.runCommand "yarn-corepack" { } ''
    mkdir -p $out/bin
    dist=${pkgs.lib.getOutput "corepack" pkgs.nodejs-slim_24}/lib/node_modules/corepack/dist
    for b in yarn yarnpkg; do
      test -x $dist/$b.js
      ln -s $dist/$b.js $out/bin/$b
    done
  '';
in
with pkgs; [
  curl wget jq ripgrep fd bat fzf tree unzip zstd htop
  git gh just
  nodejs_24 pnpm yarnCorepack python312 uv go rustup
  tmux openssh
  eza zoxide starship direnv nix-direnv
  neovim
  docker-compose
  # C/C++ toolchain (I-218): cgo, node-gyp, rustup's linker, Python
  # extensions and `make` in any repository. `gcc` is the wrapper that
  # provides cc, gcc, g++ and c++ against the guest's glibc.
  gcc binutils gnumake pkg-config cmake
  # Everyday CLIs a developer expects on a Linux box (I-218).
  file lsof zip dnsutils sqlite postgresqlClient openssl gnupg psmisc
]
