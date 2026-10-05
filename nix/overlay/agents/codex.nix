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
# and bwrap are copies (the daemon rejects a link out of the package).
# Codex puts codex-path on its PATH and finds bwrap in codex-resources
# itself. codex-code-mode-host runs every shell command (I-426).
{ lib, stdenvNoCC, fetchurl, bubblewrap, ripgrep, procps }:
let
  v = (builtins.fromJSON (builtins.readFile ./versions.json)).codex;
  host = fetchurl { inherit (v.code-mode-host) url hash; };
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
    install -Dm755 ${bubblewrap}/bin/bwrap $out/codex-resources/bwrap
    printf '%s\n' ${lib.escapeShellArg manifest} > $out/codex-package.json
    runHook postInstall
  '';

  # The daemon's own check, run for real: with a scratch CODEX_HOME,
  # `codex app-server daemon start` installs this package and starts the
  # app server, which fails on any missing piece of the layout. It needs
  # no network; the updater it also starts is stopped before it matters.
  doInstallCheck = true;
  # The daemon records its app server's start time with ps.
  nativeInstallCheckInputs = [ procps ];
  installCheckPhase = ''
    runHook preInstallCheck
    $out/bin/codex --version
    export HOME=$(mktemp -d) CODEX_HOME=$(mktemp -d)
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
