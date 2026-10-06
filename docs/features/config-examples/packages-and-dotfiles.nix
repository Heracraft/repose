# Packages, a dotfile, session variables and a home-manager program module.
{ config, pkgs, lib, ... }:
{
  home.packages = with pkgs; [
    deno
    shellcheck
    hyperfine
  ];

  programs.git = {
    enable = true;
    settings = {
      pull.rebase = true;
      init.defaultBranch = "main";
    };
  };

  # Written to ~/.config/htop/htoprc on every apply.
  xdg.configFile."htop/htoprc".text = ''
    tree_view=1
    hide_kernel_threads=1
  '';

  home.sessionVariables = {
    DENO_NO_UPDATE_CHECK = "1";
  };
  home.sessionPath = [ "$HOME/.deno/bin" ];
}
