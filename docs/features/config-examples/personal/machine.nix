# A personal machine.nix (DECISIONS I-490): every machine of the account
# gets it, beside the project's own fragment. Same contract as a
# fragment; checks.fragment-examples composes it with
# ../packages-and-dotfiles.nix, so the two must merge.
{ pkgs, ... }:
{
  home.packages = with pkgs; [
    ripgrep
    fd
    jq
  ];

  programs.git = {
    enable = true;
    settings.push.autoSetupRemote = true;
  };

  # Aliases reach bash only with home-manager writing ~/.bashrc.
  programs.bash.enable = true;
  home.shellAliases = {
    gs = "git status --short";
    ll = "ls -la";
  };

  xdg.configFile."starship.toml".text = ''
    add_newline = false
  '';
}
