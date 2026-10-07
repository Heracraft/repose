# The Go binaries that ship inside a guest, built from the repository root.
#
# The generated protobuf code is tracked in git, but it is regenerated here
# by `buf` with local plugins so a stale checkout cannot ship stale stubs. That keeps the build
# offline and reproducible: buf compiles the .proto files itself, so no
# network and no remote plugin is involved.
#
# Builds every Go binary in the repo: guestd and repose-hook (04), hostd and
# hostdev (03), api and repose-admin (05). Used by the flake's packages,
# nix/guest/base (02) and nix/hosts (01).
{ pkgs, lib, version ? "dev" }:

let
  root = ../.;

  # Only what the Go build reads, so an edit to docs or apps/ does not rebuild
  # the guest.
  src = lib.cleanSourceWith {
    name = "repose-go-src";
    src = root;
    filter = path: type:
      let rel = lib.removePrefix (toString root + "/") (toString path);
      # An entry names a file or a directory; a directory's ancestors are
      # kept too, or cleanSourceWith never descends to it. test/fake-logto
      # is imported by cmd/fake-logto (the dashboard's dev login), so the
      # vendor step needs it without the rest of test/.
      in lib.any (p: rel == p || lib.hasPrefix (p + "/") rel || lib.hasPrefix (rel + "/") p)
        [ "go.mod" "go.sum" "cmd" "internal" "proto" "buf.yaml" "test/fake-logto" ];
  };

  bufTemplate = pkgs.writeText "buf.gen.nix.yaml" (builtins.toJSON {
    version = "v2";
    plugins = [
      {
        local = "protoc-gen-go";
        out = ".";
        opt = "module=github.com/heracraft/repose/internal/gen";
      }
      {
        local = "protoc-gen-go-grpc";
        out = ".";
        opt = "module=github.com/heracraft/repose/internal/gen";
      }
    ];
  });

  generated = pkgs.runCommand "repose-proto-go"
    {
      nativeBuildInputs = [ pkgs.buf pkgs.protoc-gen-go pkgs.protoc-gen-go-grpc ];
    } ''
    # buf writes a cache under $HOME, which the sandbox does not provide.
    export HOME="$NIX_BUILD_TOP/home"
    mkdir -p "$HOME" "$out"
    cd "$out"
    buf generate ${src} --template ${bufTemplate}
  '';

  # One derivation per binary so `${pkg}/bin/<name>` and meta.mainProgram
  # are right for each; they share src and vendorHash.
  mkBin = name: pkgs.buildGoModule {
    pname = name;
    inherit version src;

    # `nix build ./nix#guestd` prints the expected value when a dependency
    # changes and this no longer matches.
    vendorHash = "sha256-s20s9wRZutz/jtol/LG25vnT2O0YGKLQvoHBuh8Chbg=";

    postPatch = ''
      mkdir -p internal/gen
      cp -r ${generated}/* internal/gen/
      chmod -R u+w internal/gen
    '';

    subPackages = [ "cmd/${name}" ];

    ldflags = [ "-s" "-w" "-X main.version=${version}" ];

    # The unit tests need a writable /proc fixture tree and fork commands; they
    # run in CI, not in the sandbox.
    doCheck = false;

    meta = {
      description = "repose ${name} (see docs/workstreams/)";
      mainProgram = name;
    };
  };
in
{
  inherit generated;
  guestd = mkBin "guestd";
  # repose-notify and repose-ask are the same binary under the names agents
  # call (DECISIONS I-244), and repose-mcp the guest's MCP registry command
  # (I-555); it picks the command from argv[0].
  repose-hook = (mkBin "repose-hook").overrideAttrs (old: {
    postInstall = (old.postInstall or "") + ''
      ln -s repose-hook $out/bin/repose-notify
      ln -s repose-hook $out/bin/repose-ask
      ln -s repose-hook $out/bin/repose-mcp
    '';
  });
  hostd = mkBin "hostd";
  hostdev = mkBin "hostdev";
  api = mkBin "api";
  repose-admin = mkBin "repose-admin";
  repose = mkBin "repose";
  gateway = mkBin "gateway";
}
