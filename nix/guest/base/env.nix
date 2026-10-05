# Environment every process of dev's sees (guest-conventions.md
# "Environment"). Static values are NixOS environment.sessionVariables,
# which reach both /etc/set-environment (every bash, login or not) and
# /etc/pam/environment (the SSH session and dev's systemd user manager, so
# user units and the tmux server it starts). TZ and REPOSE_PROJECT come
# from /etc/repose/env (written by guestd at SetupProject) and named
# secrets from /run/repose/secrets.refresh, both sourced by
# /etc/profile.d/repose.sh, which also exports DISPLAY while the desktop
# is up. That file is sourced from every shell through extraInit so login
# and interactive shells behave the same. A non-interactive bash (how an
# agent runs each command) reads none of these, so BASH_ENV points it at
# /etc/repose/bash-env.sh, which brings the named secrets up to date when
# guestd has rewritten them since the process started (DECISIONS I-475).
{ config, lib, pkgs, ... }:
let
  home = "/home/dev";
  # Every package manager's user bin dir (DECISIONS I-227), ahead of the
  # system profile so a user's install wins over the base's copy.
  userBinDirs = map (d: "${home}/${d}") (import ./user-bin-dirs.nix);
in
{
  environment.sessionVariables = {
    LANG = "C.UTF-8";
    EDITOR = "nvim";
    REPOSE = "1";
    COLORTERM = "truecolor";
    # The nodejs store path is read-only; global installs need a prefix.
    NPM_CONFIG_PREFIX = "${home}/.npm-global";
    PNPM_HOME = "${home}/.local/share/pnpm";
    # The tools' own defaults, set so every process (user units too)
    # agrees on them.
    GOPATH = "${home}/go";
    CARGO_HOME = "${home}/.cargo";
    RUSTUP_HOME = "${home}/.rustup";
    BUN_INSTALL = "${home}/.bun";
    DENO_INSTALL_ROOT = "${home}/.deno";
    COMPOSER_HOME = "${home}/.config/composer";
    # Not a default: without it `gem install` writes to ruby's store path.
    GEM_HOME = "${home}/.local/share/gem";
    PATH = userBinDirs;
    # A stable /etc path rather than a store path, so a process that kept
    # an older BASH_ENV across a base switch still finds the file.
    BASH_ENV = "/etc/repose/bash-env.sh";
  };

  environment.etc."repose/bash-env.sh".source = ./bash-env.sh;

  environment.etc."profile.d/repose.sh".text = ''
    # repose guest profile: sourced by every shell (see nix/guest/base/env.nix).
    # The configuration's home.sessionVariables and home.sessionPath
    # (nix/guest/contract.nix, DECISIONS I-488), here as well as in PAM and
    # /etc/set-environment so an agent started by a tmux server older than
    # the configuration gets them, expanded by the shell.
    if [ -r /etc/repose/session-vars.sh ]; then
      . /etc/repose/session-vars.sh
    fi
    if [ -r /etc/repose/env ]; then
      set -a
      . /etc/repose/env
      set +a
    fi
    # Secrets through the same guarded refresh as BASH_ENV (I-475): a
    # shell with no secrets gets them all, and a value its parent set on
    # purpose stays. secrets.env alone is a guest whose secrets an older
    # guestd wrote.
    if [ -r /run/repose/secrets.refresh ]; then
      . /etc/repose/bash-env.sh
    elif [ -r /run/repose/secrets.env ]; then
      . /run/repose/secrets.env
    fi
    # DISPLAY only while the on-demand desktop's X server is up.
    if [ -S /tmp/.X11-unix/X99 ]; then
      export DISPLAY=:99
    else
      unset DISPLAY
    fi
  '';

  environment.extraInit = ''
    if [ -r /etc/profile.d/repose.sh ]; then
      . /etc/profile.d/repose.sh
    fi
  '';

  # A base applied without a reboot changes /etc, but two long-lived
  # processes keep the PATH they started with: dev's tmux server (a
  # `tmux new-window <cmd>`, how `repose run` starts an agent, runs `bash
  # -c` with the server's environment) and dev's user manager (what a user
  # unit starts with). Give both the new login PATH and BASH_ENV, so an
  # agent started after the switch gets current secrets in each command
  # (I-475), and the configuration's session variables, unsetting the
  # ones the previous configuration had and this one dropped (I-488;
  # /run/repose-session-vars.names keeps the names last pushed).
  # Idempotent; nothing to push on first boot, when neither is running
  # yet, but the names are recorded.
  system.activationScripts.repose-user-path = {
    deps = [ "etc" "users" ];
    text = ''
      repose_old_names=$(${pkgs.coreutils}/bin/cat /run/repose-session-vars.names 2>/dev/null || true)
      if [ -S /tmp/tmux-1000/default ] || [ -S /run/user/1000/bus ]; then
        ${pkgs.util-linux}/bin/runuser -u dev -- ${pkgs.coreutils}/bin/env -i \
          HOME=/home/dev USER=dev LOGNAME=dev XDG_RUNTIME_DIR=/run/user/1000 \
          REPOSE_OLD_NAMES="$repose_old_names" \
          ${pkgs.bash}/bin/bash -lc '
            old=$REPOSE_OLD_NAMES
            unset REPOSE_OLD_NAMES
            new=
            [ -r /etc/repose/session-vars.names ] && new=$(< /etc/repose/session-vars.names)
            tmux_args=(set-environment -g PATH "$PATH" \; set-environment -g BASH_ENV "$BASH_ENV")
            set_args=(PATH="$PATH" BASH_ENV="$BASH_ENV")
            unset_args=()
            kept=" "
            for n in $new; do
              kept="$kept$n "
              tmux_args+=(\; set-environment -g "$n" "''${!n-}")
              set_args+=("$n=''${!n-}")
            done
            for n in $old; do
              case $kept in
                *" $n "*) ;;
                *)
                  tmux_args+=(\; set-environment -g -u "$n")
                  unset_args+=("$n")
                  ;;
              esac
            done
            if [ -S /tmp/tmux-1000/default ]; then
              ${pkgs.tmux}/bin/tmux -S /tmp/tmux-1000/default "''${tmux_args[@]}" 2>/dev/null || true
            fi
            if [ -S /run/user/1000/bus ]; then
              ${pkgs.systemd}/bin/systemctl --user set-environment "''${set_args[@]}" 2>/dev/null || true
              if [ "''${#unset_args[@]}" -gt 0 ]; then
                ${pkgs.systemd}/bin/systemctl --user unset-environment "''${unset_args[@]}" 2>/dev/null || true
              fi
            fi
          ' || true
      fi
      if [ -r /etc/repose/session-vars.names ]; then
        ${pkgs.coreutils}/bin/cp /etc/repose/session-vars.names /run/repose-session-vars.names || true
      else
        ${pkgs.coreutils}/bin/rm -f /run/repose-session-vars.names
      fi
      unset repose_old_names
    '';
  };

  systemd.tmpfiles.rules = [
    "d /etc/repose 0755 root root -"
    "d /home/dev/.local 0755 dev dev -"
    "d /home/dev/.local/bin 0755 dev dev -"
    "d /home/dev/.local/share 0755 dev dev -"
    "d /home/dev/.local/share/pnpm 0755 dev dev -"
    "d /home/dev/.npm-global 0755 dev dev -"
    "d /home/dev/.npm-global/bin 0755 dev dev -"
  ];
}
