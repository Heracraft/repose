# Where every package manager puts what dev installs, relative to
# /home/dev (DECISIONS I-227). env.nix puts them first on PATH everywhere;
# the guest-devtools VM test drops a program into each and checks it
# resolves. A directory that does not exist costs nothing.
[
  ".local/bin" # uv tool, pipx (if installed), stack, cabal, claude's own installer
  ".local/share/pnpm/bin" # pnpm 11+ add -g (I-520)
  ".local/share/pnpm" # pnpm 10 add -g (PNPM_HOME), a corepack pin
  ".npm-global/bin" # npm i -g, yarn v1 global (NPM_CONFIG_PREFIX)
  "go/bin" # go install (GOPATH)
  ".cargo/bin" # cargo install, rustup's proxies (CARGO_HOME)
  ".bun/bin" # bun add -g, bun's installer (BUN_INSTALL)
  ".deno/bin" # deno install (DENO_INSTALL_ROOT)
  ".yarn/bin" # yarn v1 global without a prefix
  ".local/share/gem/bin" # gem install (GEM_HOME)
  ".config/composer/vendor/bin" # composer global require (COMPOSER_HOME)
  ".dotnet/tools" # dotnet tool install -g
  ".ghcup/bin" # ghcup
  ".cabal/bin" # cabal install, pre-XDG layout
  ".opam/default/bin" # opam's default switch
  ".luarocks/bin" # luarocks --local
  ".mix/escripts" # mix escript.install
  ".nimble/bin" # nimble install
  ".juliaup/bin" # juliaup
  ".julia/bin" # julia apps
  ".krew/bin" # kubectl krew
  ".volta/bin" # volta
]
