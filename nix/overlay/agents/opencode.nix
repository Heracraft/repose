# opencode from the npm registry: version 2's channel is a per-platform
# package (@opencode/cli-linux-x64, @opencode/cli-linux-arm64) holding one
# bun-compiled, dynamically linked binary, pinned in versions.json under
# the architecture's slot (DECISIONS I-1374). Version 2 is the machine's
# opencode since DECISIONS I-1050; version 1 came from the GitHub release
# asset (I-481).
{ lib, stdenvNoCC, fetchurl, autoPatchelfHook, makeBinaryWrapper, ripgrep }:
let
  v = (builtins.fromJSON (builtins.readFile ./versions.json)).opencode;
in
# The platform owns the version (R3-19), so a release that moves the major
# line is a decision, not a bump: the plugin's event mapping, the hook
# contract and /docs/agents are written against version 2 (I-1050).
assert lib.assertMsg (lib.versions.major v.version == "2")
  "opencode: versions.json left version 2's line (DECISIONS I-1050)";
stdenvNoCC.mkDerivation {
  pname = "opencode";
  inherit (v) version;
  src = fetchurl { inherit (v.${stdenvNoCC.hostPlatform.system}) url hash; };
  sourceRoot = "package";
  dontBuild = true;
  dontStrip = true;
  nativeBuildInputs = [ autoPatchelfHook makeBinaryWrapper ];
  installPhase = ''
    runHook preInstall
    install -Dm755 bin/opencode $out/bin/opencode
    wrapProgram $out/bin/opencode \
      --set OPENCODE_DISABLE_AUTOUPDATE 1 \
      --prefix PATH : ${lib.makeBinPath [ ripgrep ]}
    runHook postInstall
  '';
  meta = {
    description = "opencode, the terminal coding agent";
    homepage = "https://opencode.ai";
    license = lib.licenses.mit;
    mainProgram = "opencode";
    platforms = [ "x86_64-linux" "aarch64-linux" ];
  };
}
