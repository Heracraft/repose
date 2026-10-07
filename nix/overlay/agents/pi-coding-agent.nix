# pi from its GitHub release: a bun-compiled, dynamically linked binary
# that reads package.json, its themes, docs and a wasm module from the
# directory it lives in, so the whole tree is installed and bin/pi is a
# wrapper into it. Pinned in versions.json. PI_SKIP_VERSION_CHECK, as
# claude-code.nix and opencode.nix turn their updaters off: a package in
# /nix/store cannot update itself, so pi's "New version available. Run pi
# update" banner at every start pointed at a command that fails ("pi
# cannot self-update this installation"); new versions arrive with
# platform updates (DECISIONS I-543).
{ lib, stdenvNoCC, fetchurl, autoPatchelfHook, makeBinaryWrapper, ripgrep, fd, xorg }:
let
  v = (builtins.fromJSON (builtins.readFile ./versions.json))."pi-coding-agent";
in
stdenvNoCC.mkDerivation {
  pname = "pi-coding-agent";
  inherit (v) version;
  src = fetchurl { inherit (v.x86_64-linux) url hash; };
  dontBuild = true;
  dontStrip = true;
  nativeBuildInputs = [ autoPatchelfHook makeBinaryWrapper ];
  # A prebuilt X11 clipboard helper ships in the tree; the desktop's X
  # server is the only time it loads, but the library must resolve.
  buildInputs = [ xorg.libxcb ];
  installPhase = ''
    runHook preInstall
    mkdir -p $out/lib $out/bin
    cp -r . $out/lib/pi
    chmod 755 $out/lib/pi/pi
    makeBinaryWrapper $out/lib/pi/pi $out/bin/pi \
      --set PI_SKIP_VERSION_CHECK 1 \
      --prefix PATH : ${lib.makeBinPath [ ripgrep fd ]}
    runHook postInstall
  '';
  meta = {
    description = "pi, the terminal coding agent";
    homepage = "https://pi.dev";
    license = lib.licenses.mit;
    mainProgram = "pi";
    platforms = [ "x86_64-linux" ];
  };
}
