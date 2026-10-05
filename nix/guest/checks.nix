# Checks for the fragment pipeline (docs/workstreams/12-nix-config-pipeline.md
# §7): every example fragment in docs/features/config-examples composes and
# builds, and the contract's refusals refuse with the documented message.
# Both go through composeGuest, the same path hostd's `guestSystem` takes.
{ pkgs, lib, composeGuest, guestd, hook, baseVersion, examplesDir }:
let
  compose = args: composeGuest ({ inherit guestd hook baseVersion; } // args);

  exampleFiles = lib.filter (n: lib.hasSuffix ".nix" n) (builtins.attrNames (builtins.readDir examplesDir));
  examples = lib.listToAttrs (map
    (n: lib.nameValuePair (lib.removeSuffix ".nix" n) (compose { fragmentPath = examplesDir + "/${n}"; }).toplevel)
    exampleFiles)
  # Menu output with nixpkgs packages by attribute path (DECISIONS I-220),
  # rendered by internal/menu (TestMenuFixturesAreCurrent keeps it current).
  // { menu-packages = (compose { fragmentPath = ./menu-fixtures/packages.nix; }).toplevel; };

  # A fragment that must fail evaluation, and the text its error must carry.
  # The module system reports its errors with `throw`, which tryEval sees;
  # the message check happens outside tryEval by re-evaluating the drvPath
  # under `builtins.seq` guarded by the expectation.
  refusal = name: fragment: expect:
    let
      drv = (compose { inherit fragment; }).toplevel.drvPath;
      r = builtins.tryEval (builtins.deepSeq drv drv);
    in
    if r.success then throw "fragment-contract: ${name}: evaluation succeeded; it must fail with: ${expect}"
    else { inherit name expect; };

  refusals = [
    (refusal "system-outside-allowlist"
      { repose.system = [ { networking.firewall.enable = false; } ]; }
      "repose.system: option 'networking.firewall' is not allowed in a fragment")
    (refusal "system-openssh"
      { repose.system = [ { services.openssh.settings.PermitRootLogin = "yes"; } ]; }
      "repose.system: option 'services.openssh' is not allowed in a fragment")
    (refusal "hm-nixpkgs-overlays"
      { nixpkgs.overlays = [ (f: p: { }) ]; }
      "use repose.overlays")
    (refusal "nixos-option-in-fragment"
      { services.postgresql.enable = true; }
      "does not exist")
    # home.sessionVariables reach NixOS (I-488); PATH and the loader's
    # names do not, and PAM cannot hold a double quote.
    (refusal "session-variable-path"
      { home.sessionVariables.PATH = "$HOME/bin:$PATH"; }
      "home.sessionVariables.PATH: not allowed in a fragment; add directories with home.sessionPath")
    (refusal "session-variable-bash-env"
      { home.sessionVariables.BASH_ENV = "/tmp/x"; }
      "home.sessionVariables.BASH_ENV: not allowed in a fragment")
    (refusal "session-variable-quote"
      { home.sessionVariables.GREETING = "say \"hi\""; }
      "home.sessionVariables.GREETING: a value may not contain a double quote")
    # The message itself is asserted by internal/menu's
    # TestRealNixMissingPackage (tryEval cannot see it).
    (refusal "menu-missing-package"
      (import ./menu-fixtures/missing-package.nix)
      "nixpkgs has no package \"no-such-package-repose\"; search https://search.nixos.org/packages")
  ];
in
{
  # Forces every example's system closure to build; the output lists them.
  fragment-examples = pkgs.runCommand "fragment-examples" { passthru = examples; } ''
    mkdir -p $out
    ${lib.concatStringsSep "\n" (lib.mapAttrsToList (n: t: "ln -s ${t} $out/${n}") examples)}
    ls -l $out
  '';

  # Pure evaluation: each refusal above threw. The expected texts are also
  # what internal/hostd/nixbuild's error mapping and the CLI show.
  fragment-contract = pkgs.writeText "fragment-contract"
    (lib.concatMapStringsSep "\n" (r: "${r.name}: refused (${r.expect})") refusals);
}
