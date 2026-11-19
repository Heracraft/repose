# Gemini CLI from the npm registry: the published package is a single
# bundle (no dependencies), run with the guest's node. Pinned in
# versions.json. Google moved unpaid and AI Pro/Ultra accounts to
# Antigravity CLI (docs/RESEARCH.md §6); API-key use, which is how a guest
# authenticates (GEMINI_API_KEY as a named secret), is unchanged.
{ lib, stdenvNoCC, fetchurl, makeWrapper, nodejs_24, ripgrep }:
let
  v = (builtins.fromJSON (builtins.readFile ./versions.json))."gemini-cli";
in
stdenvNoCC.mkDerivation {
  pname = "gemini-cli";
  inherit (v) version;
  src = fetchurl { inherit (v.npm) url hash; };
  nativeBuildInputs = [ makeWrapper ];
  dontBuild = true;
  installPhase = ''
    runHook preInstall
    dest=$out/lib/node_modules/@google/gemini-cli
    mkdir -p $dest $out/bin
    cp -r . $dest/
    makeWrapper ${nodejs_24}/bin/node $out/bin/gemini \
      --add-flags "$dest/bundle/gemini.js" \
      --prefix PATH : ${lib.makeBinPath [ ripgrep ]}
    runHook postInstall
  '';
  meta = {
    description = "Gemini CLI";
    homepage = "https://github.com/google-gemini/gemini-cli";
    license = lib.licenses.asl20;
    mainProgram = "gemini";
    platforms = [ "x86_64-linux" "aarch64-linux" ];
  };
}
