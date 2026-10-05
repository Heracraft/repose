# Toolchain every guest has. Successor of packages/core/flake.nix (deleted
# in this workstream); the dev shell in nix/flake.nix reuses `toolPackages`
# so the dev box and the guests carry the same tools.
{ pkgs, lib, ... }:
let
  toolPackages = import ./tool-list.nix pkgs;
in
{
  environment.systemPackages = toolPackages;

  programs.direnv = {
    enable = true;
    nix-direnv.enable = true;
  };
  # The user's own shells (repose ssh, ssh <slug>.repose, the tmux shell
  # window, an editor's terminal) load a checkout's flake dev shell as the
  # agents do when the checkout has no .envrc: direnv's prompt hook is
  # replaced by one that, there and only there, exports from the agent
  # wrapper's generated .envrc (devshell.sh, DECISIONS I-488). After the
  # direnv module's own init, which defines _direnv_hook; nothing changes
  # where that init did not run.
  programs.bash.interactiveShellInit = lib.mkAfter ''
    if declare -F _direnv_hook >/dev/null && [ -r /etc/repose/devshell.sh ]; then
      . /etc/repose/devshell.sh
      _direnv_hook() {
        local previous_exit_status=$?
        trap -- ''' SIGINT
        _repose_devshell_prompt
        trap - SIGINT
        return $previous_exit_status
      }
    fi
  '';
  programs.starship.enable = true;
  programs.zoxide.enable = true;
  programs.git.enable = true;
  programs.neovim = {
    enable = true;
    defaultEditor = false;
    # `vi` and `vim` are base commands to the CLI's tool carry
    # (internal/cli/carry_tools.go), so a laptop's vim is never carried
    # and core.editor=vim needs them here (DECISIONS I-514).
    viAlias = true;
    vimAlias = true;
  };
  programs.bash.completion.enable = true;
  # `ls` stays GNU ls (NixOS's `ls --color=tty`): eza with no path reads
  # its file list from a non-tty stdin, and its -t and -r differ, which
  # broke scripts and loops that call ls (DECISIONS I-514).
  programs.bash.shellAliases = {
    la = "eza -a --group-directories-first";
    ll = "eza -al --group-directories-first";
    lt = "eza -aT --group-directories-first";
  };
}
