# OpenAI Codex CLI from its GitHub release: the static musl binary, so no
# patching. Pinned in versions.json.
#
# $out is laid out as a complete Codex package, the shape of upstream's
# codex-package-<target> asset: codex-package.json, bin/codex,
# bin/codex-code-mode-host, codex-path/rg and codex-resources/bwrap.
# Since 0.157 the interactive `codex` starts a background app-server
# daemon, which copies the package that holds the running executable into
# ~/.codex/packages and refuses to start without every one of those files
# ("this CLI has no complete local package", DECISIONS I-487). So bin/codex
# is the real binary, never a wrapper script beside a renamed one, and rg
# is a copy (the daemon rejects a link out of the package). Codex puts
# codex-path on its PATH. codex-code-mode-host runs every shell command
# (I-426).
#
# bwrap is upstream's own release asset, byte for byte: with no bwrap on
# PATH (a guest has none) Codex runs its bundled one only after checking
# it against the sha256 compiled into the binary, and refuses any other
# build ("bundled bubblewrap digest mismatch", DECISIONS I-495).
{ lib, stdenvNoCC, fetchurl, ripgrep, procps, coreutils }:
let
  v = (builtins.fromJSON (builtins.readFile ./versions.json)).codex;
  host = fetchurl { inherit (v.code-mode-host) url hash; };
  bwrap = fetchurl { inherit (v.bwrap) url hash; };
  target = "x86_64-unknown-linux-musl";
  manifest = builtins.toJSON {
    layoutVersion = 1;
    inherit (v) version;
    inherit target;
    variant = "codex";
    entrypoint = "bin/codex";
    resourcesDir = "codex-resources";
    pathDir = "codex-path";
  };
in
stdenvNoCC.mkDerivation {
  pname = "codex";
  inherit (v) version;
  src = fetchurl { inherit (v.x86_64-linux) url hash; };
  sourceRoot = ".";
  dontBuild = true;
  # The binaries are static and already stripped; patchelf has nothing to do.
  dontFixup = true;
  installPhase = ''
    runHook preInstall
    install -Dm755 codex-${target} $out/bin/codex
    tar xzf ${host} codex-code-mode-host-${target}
    install -Dm755 codex-code-mode-host-${target} $out/bin/codex-code-mode-host
    install -Dm755 ${ripgrep}/bin/rg $out/codex-path/rg
    tar xzf ${bwrap} bwrap-${target}
    install -Dm755 bwrap-${target} $out/codex-resources/bwrap
    printf '%s\n' ${lib.escapeShellArg manifest} > $out/codex-package.json
    runHook postInstall
  '';

  # Two of Codex's own checks, run for real, neither needing a login or
  # the network:
  # - `codex sandbox` runs a command the way an agent turn does, through
  #   the bundled bwrap, so a bwrap Codex rejects fails here. PATH holds
  #   coreutils only: a bwrap on PATH would be used instead and hide it
  #   (I-495). A builder that forbids bwrap's namespaces (GitHub's
  #   runners) passes once Codex has accepted the bwrap; a digest mismatch
  #   always fails.
  # - `codex app-server daemon start` installs this package into a
  #   scratch CODEX_HOME and starts the app server, which fails on any
  #   missing piece of the layout (I-487). The updater it also starts is
  #   stopped before it matters.
  doInstallCheck = true;
  # The daemon records its app server's start time with ps.
  nativeInstallCheckInputs = [ procps ];
  installCheckPhase = ''
    runHook preInstallCheck
    $out/bin/codex --version
    export HOME=$(mktemp -d) CODEX_HOME=$(mktemp -d)
    echo sandboxed > probe.txt
    if got=$(env PATH=${coreutils}/bin $out/bin/codex sandbox cat probe.txt 2>sandbox.err); then
      test "$got" = sandboxed
    elif grep -q 'digest mismatch' sandbox.err; then
      cat sandbox.err >&2
      exit 1
    elif grep -q '^bwrap: .*Operation not permitted' sandbox.err; then
      # Codex accepted the bundled bwrap; this builder forbids what bwrap
      # then sets up (GitHub's runners refuse its loopback address). The
      # digest is what I-495 fixed, and it passed.
      echo "codex sandbox: bwrap accepted, namespaces not permitted here:" >&2
      cat sandbox.err >&2
    else
      cat sandbox.err >&2
      exit 1
    fi
    $out/bin/codex app-server daemon start | tee start.json
    grep -q '"status":"started"' start.json
    $out/bin/codex app-server daemon stop || true
    for f in "$CODEX_HOME"/app-server-daemon/*.pid; do
      [ -f "$f" ] && kill "$(cat "$f")" 2>/dev/null || true
    done
    runHook postInstallCheck
  '';

  meta = {
    description = "OpenAI Codex CLI";
    homepage = "https://github.com/openai/codex";
    license = lib.licenses.asl20;
    mainProgram = "codex";
    platforms = [ "x86_64-linux" ];
  };
}
