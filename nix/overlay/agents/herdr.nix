# herdr from its GitHub release: a static-pie x86-64 binary, so nothing is
# patched. Pinned in versions.json and moved by scripts/bump-agents.sh like
# the agents (R3-19), though herdr is a multiplexer, not an agent
# (DECISIONS I-501): it is not wrapped and has no /etc/repose/agents.json
# entry.
#
# The install check refuses a release whose socket protocol guestd and the
# laptop's herdr cannot speak: `herdr status client --json` must report
# endpoint_protocol_generation 1 and protocol 22 or later. The numbers are
# internal/multiplexer's HerdrEndpointGeneration and HerdrMinProtocol; a
# bump that fails here fails before any guest gets it (the I-487 lesson).
# `passthru.protocolCheck` is the same script, which nix/flake.nix's
# herdr-protocol-check runs against fake binaries.
{ lib, stdenvNoCC, fetchurl, writeShellScript, jq }:
let
  v = (builtins.fromJSON (builtins.readFile ./versions.json)).herdr;
  protocolCheck = writeShellScript "herdr-protocol-check" ''
    # herdr-protocol-check <herdr binary>
    set -u
    bin="$1"
    if ! out=$(HOME="''${TMPDIR:-/tmp}" "$bin" status client --json 2>&1); then
      echo "herdr-protocol-check: '$bin status client --json' failed: $out" >&2
      exit 1
    fi
    gen=$(printf '%s' "$out" | ${jq}/bin/jq -r '.endpoint_protocol_generation // "missing"' 2>/dev/null || echo unreadable)
    proto=$(printf '%s' "$out" | ${jq}/bin/jq -r '.protocol // "missing"' 2>/dev/null || echo unreadable)
    case "$proto" in
      ""|*[!0-9]*) proto_ok=0 ;;
      *) if [ "$proto" -ge 22 ]; then proto_ok=1; else proto_ok=0; fi ;;
    esac
    if [ "$gen" != 1 ] || [ "$proto_ok" != 1 ]; then
      echo "herdr-protocol-check: endpoint_protocol_generation is $gen and protocol is $proto; repose needs generation 1 and protocol 22 or later (DECISIONS I-501)" >&2
      exit 1
    fi
    echo "herdr-protocol-check: endpoint_protocol_generation $gen, protocol $proto"
  '';
in
stdenvNoCC.mkDerivation {
  pname = "herdr";
  inherit (v) version;
  src = fetchurl { inherit (v.x86_64-linux) url hash; };
  dontUnpack = true;
  dontBuild = true;
  dontStrip = true;
  dontPatchELF = true;
  installPhase = ''
    runHook preInstall
    install -Dm755 $src $out/bin/herdr
    runHook postInstall
  '';
  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    ${protocolCheck} $out/bin/herdr
    runHook postInstallCheck
  '';
  passthru = { inherit protocolCheck; };
  meta = {
    description = "herdr, the terminal workspace manager for coding agents";
    homepage = "https://herdr.dev";
    license = lib.licenses.asl20;
    mainProgram = "herdr";
    platforms = [ "x86_64-linux" ];
  };
}
