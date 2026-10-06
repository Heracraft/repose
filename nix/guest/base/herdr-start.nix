# repose-herdr-start: the unit's main process, which execs the server.
# The server is never started through a login shell (DECISIONS I-563):
# - PATH is the login PATH from repose-login-path, so every user bin
#   dir is there as in a login shell (I-227) and `herdr update` in the
#   guest, which lands in ~/.local/bin, comes first; the user's profile
#   cannot print into it or replace the shell before herdr starts.
# - The guards /etc/profile, /etc/set-environment and home-manager's
#   session file export are cleared, so each pane's shell (login or
#   not: /etc/bashrc reads /etc/profile when the guard is unset) runs
#   /etc/profile.d/repose.sh and gets the current session variables,
#   zone and secrets, as a new tmux window does (I-508).
# - The configuration's session variables (/etc/repose/session-vars.names)
#   are left out of the server's environment: herdr has no
#   set-environment, so a name a later switch dropped would otherwise
#   stay in every new pane; the panes' shells and the agent wrappers set
#   the current ones.
#
# namesFile is a parameter for the flake check only.
{ pkgs, herdr, namesFile ? "/etc/repose/session-vars.names" }:
let
  loginPath = import ./login-path.nix { inherit pkgs; };
in
pkgs.writeShellApplication {
  name = "repose-herdr-start";
  runtimeInputs = [ loginPath pkgs.coreutils ];
  text = ''
    login_path=$(repose-login-path || true)
    if [ -n "$login_path" ]; then
      export PATH="$login_path"
    fi
    unset __NIXOS_SET_ENVIRONMENT_DONE __ETC_PROFILE_DONE __ETC_PROFILE_SOURCED \
      __ETC_BASHRC_SOURCED __HM_SESS_VARS_SOURCED __HM_ZSH_SESS_VARS_SOURCED
    if [ -r ${namesFile} ]; then
      for n in $(< ${namesFile}); do
        unset "$n" 2>/dev/null || true
      done
    fi
    if command -v herdr >/dev/null 2>&1; then
      exec herdr server
    fi
    exec ${herdr}/bin/herdr server
  '';
}
