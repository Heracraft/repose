# chrome-devtools-mcp from the npm registry, pinned in versions.json. The
# published tarball is fully bundled (no dependencies), so this is an
# install of one tree plus a node wrapper; Chromium is nixpkgs's, passed as
# --executablePath so nothing is downloaded at run time.
{ lib, stdenvNoCC, fetchurl, jq, nodejs_24, chromium, runtimeShell }:
let
  versions = builtins.fromJSON (builtins.readFile ./versions.json);
  v = versions."chrome-devtools-mcp";
in
stdenvNoCC.mkDerivation {
  pname = "chrome-devtools-mcp";
  inherit (v) version;
  src = fetchurl { inherit (v) url hash; };
  dontBuild = true;
  dontConfigure = true;
  nativeBuildInputs = [ jq ];
  installPhase = ''
    runHook preInstall
    dest=$out/lib/node_modules/chrome-devtools-mcp
    mkdir -p $dest $out/bin
    cp -r . $dest/
    # Upstream moves the entry script between releases (build/src/bin in
    # 1.10.1, out/Default/gen/src/bin in 1.13.0), so read it from
    # package.json's bin instead of naming it here; a path written here is
    # a daily bump that builds and then fails its smoke run. The build
    # fails loudly if bin names a file the tarball does not ship.
    entry=$(jq -r '.bin | if type == "object" then ."chrome-devtools-mcp" else . end' $dest/package.json)
    entry=''${entry#./}
    if [ -z "$entry" ] || [ "$entry" = null ] || [ ! -f "$dest/$entry" ]; then
      echo "chrome-devtools-mcp ${v.version}: package.json bin names [$entry], which the tarball does not ship" >&2
      exit 1
    fi
    # --executablePath only when the server launches its own browser:
    # yargs refuses it beside --browserUrl or --wsEndpoint, which is how
    # the guest's registration attaches to the shared browser (I-246).
    # CrUX lookups send the traced page's URL to Google; off unless asked.
    cat > $out/bin/chrome-devtools-mcp <<WRAP
    #!${runtimeShell}
    export CHROME_DEVTOOLS_MCP_NO_USAGE_STATISTICS=\''${CHROME_DEVTOOLS_MCP_NO_USAGE_STATISTICS-1}
    extra=()
    case " \$* " in
      *" --browserUrl"*|*" --browser-url"*|*" -u "*|*" --wsEndpoint"*|*" --ws-endpoint"*|*" -w "*|*" --autoConnect"*|*" --auto-connect"*|*" --executablePath"*|*" --executable-path"*|*" -e "*) ;;
      *) extra+=(--executablePath ${chromium}/bin/chromium) ;;
    esac
    case " \$* " in
      *"erformanceCrux"*|*"erformance-crux"*) ;;
      *) extra+=(--no-performance-crux) ;;
    esac
    exec ${nodejs_24}/bin/node $dest/$entry "\''${extra[@]}" "\$@"
    WRAP
    chmod +x $out/bin/chrome-devtools-mcp
    runHook postInstall
  '';
  meta = {
    description = "Chrome DevTools MCP server";
    homepage = "https://github.com/ChromeDevTools/chrome-devtools-mcp";
    license = lib.licenses.asl20;
    mainProgram = "chrome-devtools-mcp";
    platforms = lib.platforms.linux;
  };
}
