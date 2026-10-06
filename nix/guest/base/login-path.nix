# repose-login-path: prints the PATH a login shell of dev's would have,
# for the two session units, which start their servers outside one
# (DECISIONS I-563). A server started through `bash -l` itself would run
# the user's ~/.bash_profile and ~/.profile, which machine.nix can write
# (programs.bash.profileExtra): a profile that prints text would end up in
# the PATH, and one that runs `exec zsh` or `exec tmux` would replace the
# shell before the server ever started. The login shell here runs with an
# empty environment, no stdin and at most 10 s, and prints PATH after a
# marker line, so text a profile prints is ignored and a profile that
# replaces the shell prints nothing. Prints nothing (exit 0) when no PATH
# came back; the caller keeps its own.
{ pkgs }:
pkgs.writeShellApplication {
  name = "repose-login-path";
  runtimeInputs = [ pkgs.coreutils pkgs.gnused ];
  text = ''
    user=$(id -un)
    # shellcheck disable=SC2016 # expanded by the login shell
    out=$(timeout 10 env -i HOME="''${HOME:-/home/$user}" USER="$user" LOGNAME="$user" \
      ${pkgs.bash}/bin/bash -lc 'printf "\n__repose_login_path=%s\n" "$PATH"' </dev/null 2>/dev/null || true)
    path=$(printf '%s\n' "$out" | sed -n 's/^__repose_login_path=//p' | tail -n 1)
    if [ -n "$path" ]; then
      printf '%s\n' "$path"
    fi
  '';
}
