# Claude Code from Anthropic's release bucket, the binary the npm
# installer downloads (a bun-compiled, dynamically linked ELF), pinned in
# versions.json (docs/workstreams/12-nix-config-pipeline.md "Agent
# overlay", DECISIONS R3-19). Same environment as nixpkgs's package: no
# self-update, no installation checks, the store's ripgrep, and bubblewrap
# and socat on PATH for its sandbox. versions.json holds one slot per
# architecture (x86_64-linux, aarch64-linux), and the derivation takes
# the one it is built for (DECISIONS I-1374).
{ lib, stdenvNoCC, fetchurl, autoPatchelfHook, makeBinaryWrapper, alsa-lib, procps, ripgrep, bubblewrap, socat }:
let
  v = (builtins.fromJSON (builtins.readFile ./versions.json))."claude-code";
in
stdenvNoCC.mkDerivation {
  pname = "claude-code";
  inherit (v) version;
  src = fetchurl { inherit (v.${stdenvNoCC.hostPlatform.system}) url hash; };
  dontUnpack = true;
  dontBuild = true;
  dontStrip = true; # stripping a bun binary breaks it
  nativeBuildInputs = [ autoPatchelfHook makeBinaryWrapper ];
  buildInputs = [ alsa-lib ];
  installPhase = ''
    runHook preInstall
    install -Dm755 $src $out/bin/claude
    wrapProgram $out/bin/claude \
      --set DISABLE_AUTOUPDATER 1 \
      --set DISABLE_INSTALLATION_CHECKS 1 \
      --set USE_BUILTIN_RIPGREP 0 \
      --prefix LD_LIBRARY_PATH : ${lib.makeLibraryPath [ alsa-lib ]} \
      --prefix PATH : ${lib.makeBinPath [ procps ripgrep bubblewrap socat ]}
    runHook postInstall
  '';
  meta = {
    description = "Claude Code";
    homepage = "https://code.claude.com";
    license = lib.licenses.unfree;
    mainProgram = "claude";
    platforms = [ "x86_64-linux" "aarch64-linux" ];
  };
}
