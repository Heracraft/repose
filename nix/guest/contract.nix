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
#   home.sessionVariables = { NAME = "value"; };  home.sessionPath = [ ... ];
#     home-manager writes these to hm-session-vars.sh, which only a shell
#     home-manager manages sources; nothing on the guest does. They are
#     carried to NixOS's environment.sessionVariables (PAM and
#     /etc/set-environment, like the base's own, env.nix), with the
#     fragment's value winning over the base's and sessionPath entries put
#     ahead of PATH, and to /etc/repose/session-vars.sh, which
#     /etc/profile.d/repose.sh sources, so an agent's wrapper sets them
#     with shell expansion of $HOME or $OTHER, as home-manager would
#     (DECISIONS I-488). PATH and the names the base's loader owns are
#     refused with the reason.
#
#   home.shellAliases = { gs = "git status --short"; };
#     carried to NixOS's programs.{bash,zsh,fish}.shellAliases at priority
#     90, so an alias of the user's replaces the base's alias of the same
#     name (ll) and every shell on the guest has it (DECISIONS I-519).
#     home-manager's programs.bash stays off: it would take over ~/.bashrc.
#     programs.bash.initExtra (and the other shells' init options) is not
#     carried: the base already initialises starship, zoxide, direnv and
#     fzf, and the copies from home-manager's modules would run a second
#     time. Shell code goes in ~/.bashrc, which every bash on the guest
#     reads (I-513).
#
# The fragment itself is `repose.fragment`; nix/guest/compose.nix and
# nix/guest/microvm.nix set it. The account's personal layer
# (`machine.nix`, DECISIONS I-490) is `repose.personal`: the same contract,
# imported into the same home-manager configuration before the fragment,
# so lists merge and two definitions of one single-valued option are an
# error naming both files. Null (the default) imports nothing. The values are read back from
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

  # home.sessionVariables and home.sessionPath (header; DECISIONS I-488).
  # home-manager's own i18n module sets LOCALE_ARCHIVE_2_27 to the archive
  # NixOS already names in LOCALE_ARCHIVE; that one stays out, so a guest
  # whose fragment sets nothing has the environment it had before.
  localeArchive = "${config.i18n.glibcLocales}/lib/locale/locale-archive";
  sessionVars = lib.mapAttrs (_: toString) (lib.filterAttrs
    (n: v: v != null && !(n == "LOCALE_ARCHIVE_2_27" && toString v == localeArchive))
    hm.home.sessionVariables);
  sessionPath = hm.home.sessionPath;
  reservedVars = {
    PATH = "add directories with home.sessionPath = [ ... ] instead";
    BASH_ENV = "the guest's environment loader uses it";
    ENV = "the guest's environment loader uses it";
    REPOSE_ENV_GEN = "the guest's environment loader uses it";
    REPOSE = "the guest sets it";
  };
  sessionRefusals =
    lib.mapAttrsToList (n: _: "home.sessionVariables.${n}: not allowed in a fragment; ${reservedVars.${n}}")
      (lib.filterAttrs (n: _: reservedVars ? ${n}) sessionVars)
    ++ lib.mapAttrsToList (n: _: "home.sessionVariables: '${n}' is not a valid variable name (letters, digits and _, not starting with a digit)")
      (lib.filterAttrs (n: _: builtins.match "[A-Za-z_][A-Za-z0-9_]*" n == null) sessionVars)
    ++ lib.mapAttrsToList (n: _: "home.sessionVariables.${n}: a value may not contain a double quote (\")")
      (lib.filterAttrs (_: v: lib.hasInfix "\"" v) sessionVars)
    ++ map (p: "home.sessionPath: '${p}' may not contain a double quote (\")")
      (lib.filter (p: lib.hasInfix "\"" p) sessionPath);
  carriedVars = lib.filterAttrs (n: _: !(reservedVars ? ${n})) sessionVars;

  # A user unit home-manager writes to ~/.config/systemd/user wins over the
  # base's unit of the same name in /etc/systemd/user, so a fragment could
  # replace repose-tmux-session or repose-herdr-server, the units every
  # agent runs in. The repose- prefix is the base's (DECISIONS I-563).
  # The menu's npm installs (internal/menu/catalog.yaml) write
  # repose-npm-<tool> units into the fragment, and saved project files
  # carry those names, so a repose-npm- name the base does not define
  # itself is the fragment's to write.
  menuUnit = n: lib.hasPrefix "repose-npm-" n && !(config.systemd.user.services ? ${n});
  unitRefusals = map
    (n: "systemd.user.services.${n}: not allowed in a fragment; names starting with repose- belong to the machine")
    (lib.filter (n: lib.hasPrefix "repose-" n && !menuUnit n) (builtins.attrNames hm.systemd.user.services));

  # Sourced by /etc/profile.d/repose.sh (env.nix): every login and
  # interactive shell, and every agent wrapper, whose tmux server may have
  # started before this configuration was applied. Same quoting as
  # home-manager's hm-session-vars.sh, so `$HOME/x` expands. A sessionPath
  # entry already on PATH stays where it is; one missing is put first.
  sessionVarsScript = ''
    # repose: home.sessionVariables and home.sessionPath from this machine's
    # configuration (nix/guest/contract.nix, DECISIONS I-488). Generated.
  '' + lib.concatStrings (lib.mapAttrsToList (n: v: "export ${n}=\"${v}\"\n") carriedVars)
  + lib.optionalString (sessionPath != [ ]) ''
    for __repose_sv_p in ${lib.concatMapStringsSep " " (p: "\"${p}\"") (lib.reverseList sessionPath)}; do
      case ":''${PATH-}:" in
        *":$__repose_sv_p:"*) ;;
        *) PATH="$__repose_sv_p''${PATH:+:$PATH}" ;;
      esac
    done
    unset __repose_sv_p
    export PATH
  '';

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

  # The account's personal layer, or null.
  personalModule = config.repose.personal;

  # The pre-pass for repose.overlays (see the header). A function fragment
  # gets exactly the arguments it names; anything but lib and pkgs is a
  # throw with the rule in it, so a fragment that computes its overlays
  # from config fails with that sentence rather than with a recursion.
  prePass = m:
    let
      f = if builtins.isPath m || builtins.isString m then import m else m;
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

  # Personal overlays first, then the project's.
  prePassValue =
    (if personalModule != null then prePass personalModule else [ ]) ++ prePass fragmentModule;
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

  options.repose.personal = lib.mkOption {
    type = lib.types.nullOr lib.types.deferredModule;
    default = null;
    description = ''
      The account's personal home-manager layer (machine.nix), applied to
      user dev before the fragment under the same contract. The flake's
      guestSystem points it at "''${fragment}/personal.nix" when hostd wrote
      one (DECISIONS I-490); null imports nothing.
    '';
  };

  config = {
    home-manager.users.dev = {
      imports = [ fragmentOptions ] ++ lib.optional (personalModule != null) personalModule ++ [ fragmentModule ];
    };

    # The fragment's session variables over the base's (env.nix sets its
    # own at the default priority), its sessionPath ahead of the base's
    # PATH entries (header; DECISIONS I-488).
    environment.sessionVariables =
      lib.mapAttrs (_: v: lib.mkOverride 90 v) carriedVars
      // { PATH = lib.mkBefore sessionPath; };
    environment.etc."repose/session-vars.sh".text = sessionVarsScript;
    # home.shellAliases into every shell (header; DECISIONS I-519).
    programs.bash.shellAliases = lib.mapAttrs (_: lib.mkOverride 90) hm.home.shellAliases;
    programs.zsh.shellAliases = lib.mapAttrs (_: lib.mkOverride 90) hm.home.shellAliases;
    programs.fish.shellAliases = lib.mapAttrs (_: lib.mkOverride 90) hm.home.shellAliases;
    # The names, one per line, for env.nix's activation script, which
    # gives a running tmux server and user manager the new values and
    # unsets the names a new configuration dropped.
    environment.etc."repose/session-vars.names".text =
      lib.concatMapStrings (n: "${n}\n") (builtins.attrNames carriedVars);

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
      {
        assertion = sessionRefusals == [ ];
        message = lib.concatStringsSep "\n" sessionRefusals;
      }
      {
        assertion = unitRefusals == [ ];
        message = lib.concatStringsSep "\n" unitRefusals;
      }
    ];
  } // allowed;
}
