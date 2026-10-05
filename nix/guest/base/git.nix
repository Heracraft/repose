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

  systemd.user.services.repose-gh-helper-cleanup = {
    description = "repose: remove gh credential helpers that name a store path";
    wantedBy = [ "default.target" ];
    unitConfig.ConditionUser = "dev";
    # Outside default.target's ordering, like repose-agent-hooks: the
    # project's tmux session waits for default.target (DECISIONS I-231).
    unitConfig.DefaultDependencies = false;
    conflicts = [ "shutdown.target" ];
    before = [ "shutdown.target" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "${ghHelperCleanup}/bin/repose-gh-helper-cleanup";
    };
  };

  # gnupg's built-in pinentry path does not exist in its store output, so
  # every passphrase prompt failed with "No pinentry". gpg-agent reads
  # /etc/gnupg/gpg-agent.conf (its sysconfdir) before the user's own (I-528).
  environment.etc."gnupg/gpg-agent.conf".text = ''
    pinentry-program ${pkgs.pinentry-curses}/bin/pinentry-curses
  '';
  # pinentry-curses draws on the terminal gpg-agent is told about.
  programs.bash.interactiveShellInit = ''
    GPG_TTY=$(tty)
    export GPG_TTY
  '';
}
