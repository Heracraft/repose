# The fragment contract, enforced at evaluation. A fragment is one Nix file
# whose value is a home-manager module (an attribute set or a function of
# { config, pkgs, lib, ... }) applied to user dev. Two things a home-manager
# module cannot normally do are given a named door here, and nothing else
# reaches NixOS from a fragment (docs/workstreams/12-nix-config-pipeline.md
# §5, docs/features/config.md "Writing a fragment", DECISIONS I-43):
#
#   repose.overlays = [ (final: prev: { ... }) ];
#     applied to the guest's pkgs before anything is evaluated, after the
#     platform's own agents overlay. useGlobalPkgs is on, so home-manager's
#     nixpkgs.overlays would be silently ignored; setting it is an error.
#     The list is read off the fragment before pkgs exists (home-manager's
#     module list needs pkgs, so it cannot be read back from the evaluated
#     configuration without recursion): a function fragment is called once
#     with the platform's own pkgs and lib, and its `repose.overlays` may
#     use lib but not pkgs or config.
#
#   repose.system = [ { services.postgresql = { enable = true; }; } ];
#     plain attribute sets of NixOS options, each first two levels checked
#     against system-allowlist.json. This is what the menu renderer emits
#     for catalog services; a hand-written fragment may use it under the
#     same allowlist. Anything outside the list fails evaluation with the
#     option named, so `services.openssh`, `networking.*`, `users.*` and
#     `boot.*` cannot come from a tenant whatever produced the file.
#
# The fragment itself is `repose.fragment`; nix/guest/compose.nix and
# nix/guest/microvm.nix set it. The values are read back from
# home-manager's evaluated configuration, which is the same shape
# home-manager's own NixOS module uses for `users.users.<n>.packages`.
{ config, lib, ... }:
let
  allowlist = (builtins.fromJSON (builtins.readFile ./system-allowlist.json)).allowed;
  hm = config.home-manager.users.dev;

  overlayType = lib.mkOptionType {
    name = "repose-overlay";
    description = "nixpkgs overlay (final: prev: { ... })";
    check = builtins.isFunction;
    merge = lib.mergeOneOption;
  };

  # home-manager side: the two options a fragment may set.
  fragmentOptions = { lib, ... }: {
    options.repose = {
      overlays = lib.mkOption {
        type = lib.types.listOf overlayType;
        default = [ ];
        description = "Overlays applied to the guest's pkgs before evaluation.";
      };
      system = lib.mkOption {
        type = lib.types.listOf lib.types.attrs;
        default = [ ];
        description = "Allowlisted NixOS option sets (the menu's services).";
      };
    };
  };

  # One repose.system entry: the option paths it sets (first two levels), or
  # a message saying why it is refused. The refusal is a string, not a
  # throw, so it can sit in an assertion: the module system needs this
  # module's attribute names before any evaluation, and a config-dependent
  # mkMerge at the top level would recurse.
  pathsOf = m:
    if !(builtins.isAttrs m) then { bad = "an entry is not an attribute set"; }
    else if m ? _type then { bad = "an entry uses ${m._type} at the first level"; }
    else
      let
        per = map
          (top:
            if !(builtins.isAttrs m.${top}) then { bad = "'${top}' is not an attribute set"; }
            else if m.${top} ? _type then { bad = "'${top}' uses ${m.${top}._type} at the second level"; }
            else { paths = map (name: "${top}.${name}") (builtins.attrNames m.${top}); })
          (builtins.attrNames m);
        bads = lib.filter (r: r ? bad) per;
      in
      if bads != [ ] then builtins.head bads
      else { paths = lib.concatMap (r: r.paths) per; };

  refusal = m:
    let r = pathsOf m; in
    if r ? bad then
      "repose.system: ${r.bad}; each entry must be a plain attribute set such as { services.postgresql = { enable = true; }; }"
    else
      let bad = lib.filter (p: !(lib.elem p allowlist)) r.paths; in
      if bad == [ ] then null
      else "repose.system: option '${builtins.head bad}' is not allowed in a fragment; system services come from `repose config add` or the dashboard's Config menu (allowed: ${lib.concatStringsSep ", " allowlist})";

  refusals = lib.filter (r: r != null) (map refusal hm.repose.system);

  # The allowlisted option paths, each defined statically as the merge of
  # what every entry says for it. An entry outside the list never reaches
  # NixOS: nothing here reads it, and the assertion below names it.
  allowed = lib.foldl' lib.recursiveUpdate { } (map
    (p:
      let parts = lib.splitString "." p; in
      lib.setAttrByPath parts (lib.mkMerge (map (m: lib.attrByPath parts { } m) hm.repose.system)))
    allowlist);

  # The fragment may be a path, an attribute set or a function; home-manager
  # takes all three as modules. `repose` attributes stay in the module: the
  # options above declare them.
  fragmentModule = config.repose.fragment;

  # The pre-pass for repose.overlays (see the header). A function fragment
  # gets exactly the arguments it names; anything but lib and pkgs is a
  # throw with the rule in it, so a fragment that computes its overlays
  # from config fails with that sentence rather than with a recursion.
  prePassValue =
    let
      f = if builtins.isPath fragmentModule || builtins.isString fragmentModule then import fragmentModule else fragmentModule;
      arg = name:
        if name == "lib" then lib
        else if name == "pkgs" then config.repose.prePassPkgs
        else throw "repose.overlays is read before the guest's pkgs exist and may use lib and pkgs but not `${name}`";
      called =
        if builtins.isFunction f then f (lib.mapAttrs (name: _: arg name) (builtins.functionArgs f))
        else f;
      r = builtins.tryEval (called.repose.overlays or [ ]);
    in
    if config.repose.prePassPkgs == null && builtins.isFunction f then [ ]
    else if r.success then r.value else [ ];
in
{
  options.repose.prePassPkgs = lib.mkOption {
    type = lib.types.nullOr lib.types.raw;
    default = null;
    description = ''
      The platform's pkgs (agents overlay, no user overlays) used to call a
      function fragment once for its repose.overlays. mkGuestRunner sets it;
      null disables overlays for function fragments (attribute-set fragments
      still work).
    '';
  };

  options.repose.fragment = lib.mkOption {
    type = lib.types.deferredModule;
    default = { };
    description = ''
      The user's home-manager fragment, applied to user dev. Set by
      composeGuest / mkGuestRunner; the flake's guestSystem points it at
      "''${fragment}/fragment.nix" (docs/interfaces/nix-build-contract.md).
    '';
  };

  config = {
    home-manager.users.dev = {
      imports = [ fragmentOptions fragmentModule ];
    };

    # Platform overlay first (the base adds it), then the fragment's.
    nixpkgs.overlays = lib.mkAfter prePassValue;

    assertions = [
      {
        assertion = hm.nixpkgs.overlays == null;
        message = "fragment: nixpkgs.overlays is ignored with useGlobalPkgs; use repose.overlays = [ ... ] instead";
      }
      {
        assertion = refusals == [ ];
        message = lib.concatStringsSep "\n" refusals;
      }
    ];
  } // allowed;
}
