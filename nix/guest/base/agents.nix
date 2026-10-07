# The five agents from the platform overlay, wrapped, plus repose-hook and
# the platform hook and MCP configuration the wrappers merge into the user's
# own files on first run (guest-conventions.md "Agent wrappers").
{ config, lib, pkgs, ... }:
let
  # A guest is a VM an agent may wreck, so Claude Code starts
  # without permission prompts unless the user set another defaultMode;
  # repose-agent-setup adds these two keys only where defaultMode is unset
  # (DECISIONS I-250). User settings, never managed settings, so the user wins.
  # The fullscreen renderer is set, not left to Claude Code's server-side
  # flag, which a new machine has not fetched at its first start, so that
  # start drew inline in the tmux pane; added only where the user's file
  # has no tui (I-425).
  claudeSettings = {
    permissions.defaultMode = "bypassPermissions";
    skipDangerousModePermissionPrompt = true;
    tui = "fullscreen";
    hooks = {
      Notification = [
        { matcher = ""; hooks = [ { type = "command"; command = "repose-hook"; } ]; }
      ];
      Stop = [
        { matcher = ""; hooks = [ { type = "command"; command = "repose-hook"; } ]; }
      ];
    };
  };
in
{
  options.repose.applyOverlay = lib.mkOption {
    type = lib.types.bool;
    default = true;
    description = ''
      Whether this module adds the agents overlay and the unfree allowlist to
      nixpkgs itself. Off when the evaluation supplies a ready pkgs (the
      NixOS test driver does), which must then carry the overlay already.
    '';
  };

  options.repose.hookPackage = lib.mkOption {
    type = lib.types.package;
    default = pkgs.repose-hook-shim;
    defaultText = "pkgs.repose-hook-shim";
    description = ''
      The repose-hook binary agents call from their hooks. Defaults to the
      overlay's shell implementation; nix/flake.nix sets the Go binary from
      cmd/repose-hook (workstream 04) once it exists.
    '';
  };

  config = lib.mkMerge [ (lib.mkIf config.repose.applyOverlay {
    # The base brings its own overlay so nixosModules.guestBase is
    # self-contained; nix/guest/microvm.nix adds the user's overlays after.
    nixpkgs.overlays = [ (import ../../overlay/agents) ];
    nixpkgs.config.allowUnfreePredicate = pkg: builtins.elem (lib.getName pkg) (import ../unfree-allowlist.nix);
  }) {

    environment.systemPackages = (builtins.attrValues pkgs.reposeAgents) ++ [
      config.repose.hookPackage
      pkgs.repose-agent-setup
    ];

    # The opencode plugin at login, not only when the wrapped `opencode`
    # first runs: an OpenCode 2 the user installed (DECISIONS I-481) runs
    # its background service outside the wrapper and would otherwise never
    # get the plugin, nor the replacement of one an earlier base installed.
    # Gemini's at login too: a Gemini CLI that updated itself into
    # ~/.npm-global (I-543) sits ahead of the wrapper on PATH, so the
    # wrapper would never run to remove it.
    systemd.user.services.repose-agent-hooks = {
      description = "repose: install the opencode plugin and the Gemini extension";
      wantedBy = [ "default.target" ];
      unitConfig.ConditionUser = "dev";
      # Outside default.target's ordering, like repose-npm-registry: the
      # project's tmux session waits for default.target (DECISIONS I-231).
      unitConfig.DefaultDependencies = false;
      conflicts = [ "shutdown.target" ];
      before = [ "shutdown.target" ];
      serviceConfig = {
        Type = "oneshot";
        ExecStart = [
          "${pkgs.repose-agent-setup}/bin/repose-agent-setup opencode"
          "${pkgs.repose-agent-setup}/bin/repose-agent-setup gemini"
        ];
      };
    };

    environment.etc."repose/claude-settings.json".text = builtins.toJSON claudeSettings;

    # The agent wrappers' dev environment loader, at a path `repose exec`
    # sources so a command it runs gets what an agent gets (DECISIONS I-275).
    environment.etc."repose/devshell.sh".source = pkgs.reposeDevshell;

    # Every agent that has a hook system is registered here; the rest use
    # guestd's pane-idle heuristic (docs/features/agents.md).
    environment.etc."repose/agents.json".text = builtins.toJSON (lib.mapAttrs (name: pkg: {
      binary = pkg.binary;
      version = pkg.version;
      hook = {
        claude-code = "settings.json hooks Notification+Stop";
        codex = "config.toml notify";
        opencode = "plugin ~/.config/opencode/plugins/repose.js";
        gemini-cli = "heuristic";
        pi-coding-agent = "heuristic";
      }.${name};
    }) pkgs.reposeAgents);
  } ];
}
