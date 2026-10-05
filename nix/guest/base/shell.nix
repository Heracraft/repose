# The interactive shell around the tools in tools.nix: terminfo for the
# laptop terminals people attach from, ~/.bashrc in login shells, history,
# fzf's keys and starship's settings. guest-conventions.md "Shell" lists
# what this guarantees.
{ config, lib, pkgs, ... }:
let
  # The file the starship module points STARSHIP_CONFIG at when the user
  # has none, computed the way the module does (no presets are set), so it
  # is the same store path.
  starshipToml = (pkgs.formats.toml { }).generate "starship.toml" config.programs.starship.settings;
in
{
  # TERM passes from the laptop to the guest unchanged (internal/cli/run.go
  # attachTmux). ncurses has no entry for Ghostty's or kitty's own TERM, so
  # tmux refused to attach from them and `clear` failed (DECISIONS I-512).
  # Two outputs, not enableAllTerminfo, which grows with nixpkgs. The
  # other terminals I-264 names (alacritty, wezterm, foot) are in ncurses.
  environment.systemPackages = [ pkgs.ghostty.terminfo pkgs.kitty.terminfo ];

  # Ctrl-R and Ctrl-T through fzf, and `**<Tab>` completion (I-514). fzf's
  # own walker already skips .git and node_modules.
  programs.fzf = {
    keybindings = true;
    fuzzyCompletion = true;
  };

  # A cold `starship prompt` in a large checkout timed out on the default
  # 500 ms and printed a warning above the prompt (I-514). A user's
  # ~/.config/starship.toml replaces this file.
  programs.starship.settings = {
    command_timeout = 2000;
  };

  # First in /etc/bashrc's interactive part, before starship's init and
  # before a user's ~/.bashrc (below), so either may change these (I-514).
  #
  # History: long-lived machines kept 500 lines, and each pane's exit
  # rewrote the file with its own.
  #
  # STARSHIP_CONFIG: the starship module exports its store file when the
  # user has no ~/.config/starship.toml at the shell's start, and a nested
  # shell (exec bash, nix develop, an editor's terminal) inherits that and
  # keeps ignoring a file written since. Unset here only when it is still
  # the system file and a user file now exists.
  programs.bash.interactiveShellInit = lib.mkMerge [
    (lib.mkBefore ''
      shopt -s histappend
      : "''${HISTSIZE:=100000}"
      : "''${HISTFILESIZE:=200000}"
      : "''${HISTCONTROL:=ignoredups}"
      if [[ "''${STARSHIP_CONFIG:-}" == ${starshipToml} && -f "$HOME/.config/starship.toml" ]]; then
        unset STARSHIP_CONFIG
      fi
    '')
    # Last in /etc/bashrc's interactive part, after the base's aliases,
    # prompt, zoxide, direnv and the dev shell hook (I-488), so what the
    # user's file sets wins. A login bash (every tmux pane, ssh shell and
    # editor terminal) reads ~/.bash_profile, ~/.bash_login or ~/.profile
    # and never ~/.bashrc, and dev has none of the three, so a ~/.bashrc
    # written by nvm, sdkman, conda or an installer was never read
    # (DECISIONS I-513). A user who has one of them already decides.
    (lib.mkOrder 2000 ''
      if shopt -q login_shell && [ -r "$HOME/.bashrc" ] && [ ! -e "$HOME/.bash_profile" ] && [ ! -e "$HOME/.bash_login" ] && ! grep -qs bashrc "$HOME/.profile"; then
        . "$HOME/.bashrc"
      fi
    '')
  ];
}
