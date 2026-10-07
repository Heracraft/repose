# git, gh and gpg set up for a fresh HOME. Everything here is system
# scope (/etc/gitconfig, /etc/gnupg), below the laptop's carried git config
# and the user's own files, so their values win (DECISIONS I-195, I-525..I-528).
{ pkgs, lib, ... }:
let
  # `gh auth setup-git` writes the /nix/store path of one gh build into
  # ~/.gitconfig, which a base update and the store's garbage collection
  # remove; the system config names gh by command instead (I-526). The
  # empty `helper =` gh writes before its own line goes too, or it would
  # reset the system helper.
  ghHelperCleanup = pkgs.writeShellApplication {
    name = "repose-gh-helper-cleanup";
    runtimeInputs = [ pkgs.git pkgs.gnugrep ];
    text = ''
      g="$HOME/.gitconfig"
      [ -f "$g" ] && [ ! -L "$g" ] || exit 0
      store='^!/nix/store/[^ ]*gh[^ ]* auth git-credential$'
      for h in https://github.com https://gist.github.com; do
        k="credential.$h.helper"
        v=$(git config --file "$g" --get-all "$k" 2>/dev/null) || continue
        grep -qE "$store" <<<"$v" || continue
        git config --file "$g" --unset-all "$k" "$store"
        git config --file "$g" --unset-all "$k" '^$' || true
      done
    '';
  };
in
{
  # git-lfs, with its filter in /etc/gitconfig, so a carried
  # filter.lfs.required=true has a filter to run (I-525).
  programs.git.lfs.enable = true;

  # mkDefault, so a module that sets one of these keys replaces it.
  programs.git.config = {
    # gh as the helper for GitHub over HTTPS, by command name, so it goes
    # through the wrapper and survives a base update. With no gh login it
    # answers nothing and git prompts (I-526).
    credential."https://github.com".helper = lib.mkDefault "!gh auth git-credential";
    credential."https://gist.github.com".helper = lib.mkDefault "!gh auth git-credential";
    # I-527. No rerere, zdiff3 or fetch.prune: those are taste.
    init.defaultBranch = lib.mkDefault "main";
    push.autoSetupRemote = lib.mkDefault true;
    pull.rebase = lib.mkDefault false;
  };

  # In dev's user activation, which runs at login and again at every
  # base switch: dev's user manager lingers, so a unit wanted by
  # default.target would first run at the next boot (I-526).
  system.userActivationScripts.repose-gh-helper-cleanup.text = ''
    ${ghHelperCleanup}/bin/repose-gh-helper-cleanup || true
  '';
  # A gpg-agent started before /etc/gnupg/gpg-agent.conf named a pinentry
  # keeps "No pinentry" until it rereads its config (I-528). Only a
  # running agent, found by its socket, is told; none is started, and a
  # failure or a hang never holds the activation up.
  system.userActivationScripts.repose-gpg-agent-reload.text = ''
    if repose_sock=$(${pkgs.gnupg}/bin/gpgconf --list-dirs agent-socket 2>/dev/null) && [ -S "$repose_sock" ]; then
      ${pkgs.coreutils}/bin/timeout 10 ${pkgs.gnupg}/bin/gpgconf --reload gpg-agent >/dev/null 2>&1 || true
    fi
    unset repose_sock
  '';

  # gnupg's built-in pinentry path does not exist in its store output, so
  # every passphrase prompt failed with "No pinentry". gpg-agent reads
  # /etc/gnupg/gpg-agent.conf (its sysconfdir) before the user's own (I-528).
  environment.etc."gnupg/gpg-agent.conf".text = ''
    pinentry-program ${pkgs.pinentry-curses}/bin/pinentry-curses
  '';
  # pinentry-curses draws on the terminal gpg-agent is told about.
  programs.bash.interactiveShellInit = ''
    if repose_tty=$(tty 2>/dev/null); then export GPG_TTY=$repose_tty; fi
    unset repose_tty
  '';
}
