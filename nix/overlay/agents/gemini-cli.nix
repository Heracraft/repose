# Gemini CLI from the npm registry: the published package is a single
# bundle (no dependencies), run with the guest's node. Pinned in
# versions.json. Google moved unpaid and AI Pro/Ultra accounts to
# Antigravity CLI (docs/RESEARCH.md §6); API-key use, which is how a guest
# authenticates (GEMINI_API_KEY as a named secret), is unchanged.
{ lib, stdenvNoCC, fetchurl, jq, makeWrapper, nodejs_24, ripgrep }:
let
  v = (builtins.fromJSON (builtins.readFile ./versions.json))."gemini-cli";
in
stdenvNoCC.mkDerivation {
  pname = "gemini-cli";
  inherit (v) version;
  src = fetchurl { inherit (v.npm) url hash; };
  nativeBuildInputs = [ jq makeWrapper ];
  dontBuild = true;
  installPhase = ''
    runHook preInstall
    dest=$out/lib/node_modules/@google/gemini-cli
    mkdir -p $dest $out/bin
    cp -r . $dest/
    # The entry script is read from package.json's bin rather than named
    # here, for the reason chrome-devtools-mcp is: a path written into
    # this file is a daily bump that builds, passes its own `--version`
    # smoke run only while upstream keeps the path, and breaks the bump
    # the day upstream moves it. 0.61.0 says bundle/gemini.js, which is
    # what this read returns today. The build fails here, loudly, if bin
    # names a file the tarball does not ship.
    entry=$(jq -r '.bin | if type == "object" then .gemini else . end' $dest/package.json)
    entry=''${entry#./}
    if [ -z "$entry" ] || [ "$entry" = null ] || [ ! -f "$dest/$entry" ]; then
      echo "gemini-cli ${v.version}: package.json bin names [$entry], which the tarball does not ship" >&2
      exit 1
    fi
    makeWrapper ${nodejs_24}/bin/node $out/bin/gemini \
      --add-flags "$dest/$entry" \
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
