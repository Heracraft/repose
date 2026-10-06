# System tmux config and the per-project session unit.
#
# The session is created by a *user* unit of `dev`, not by guestd, so it
# belongs to dev's tmux server and outlives guestd restarts. guestd starts
# the unit at SetupProject, right after it writes this boot's
# /home/dev/.repose/project.json, when that file names tmux (DECISIONS
# I-503; herdr.nix has the other session unit). The unit creates the
# session named after the slug with window `shell` in the checkout
# (repose-checkout: the laptop folder's name since DECISIONS I-368,
# ~/<slug> on an older machine, the home directory when the machine has
# none yet). Creating it twice is a no-op.
#
# Until I-503 a path unit started it as soon as project.json existed,
# which at boot is the previous boot's file: after a change of
# multiplexer made while stopped, the old multiplexer would start before
# SetupProject wrote the new one. The path unit is gone; a guestd from
# before I-503 starts this unit by name, which the ExecCondition allows
# for every tmux project. Restart=always brings the session back after
# the tmux server exits on a running machine, which the path unit used to
# do (DECISIONS I-551).
{ config, lib, pkgs, ... }:
let
  checkout = import ./checkout.nix { inherit pkgs; };
  multiplexerIs = import ./multiplexer-is.nix { inherit pkgs; };
  tmuxSession = pkgs.writeShellApplication {
    name = "repose-tmux-session";
    runtimeInputs = [ pkgs.tmux pkgs.jq pkgs.coreutils pkgs.gnused pkgs.bash checkout ];
    text = ''
      project="$HOME/.repose/project.json"
      if [ ! -s "$project" ]; then
        echo "repose-tmux-session: $project missing; nothing to do" >&2
        exit 0
      fi
      slug=$(jq -r '.slug // empty' "$project")
      if [ -z "$slug" ]; then
        echo "repose-tmux-session: project.json has no slug" >&2
        exit 1
      fi
      dir=$(repose-checkout)
      if tmux has-session -t "=$slug" 2>/dev/null; then
        exit 0
      fi
      # -d: detached. The server keeps running in this unit's cgroup.
      tmux new-session -d -s "$slug" -n shell -c "$dir"
      # The server copied this unit's PATH (NixOS gives every unit its
      # own). What runs with the server's environment (run-shell, #()
      # jobs, a window opened from inside tmux with a command) gets the
      # login PATH instead, with every user bin dir (DECISIONS I-227).
      # shellcheck disable=SC2016 # expanded by the login shell
      login_path=$(env -i HOME="$HOME" USER="$(id -un)" LOGNAME="$(id -un)" bash -lc 'printf %s "$PATH"' 2>/dev/null || true)
      if [ -n "$login_path" ]; then
        tmux set-environment -g PATH "$login_path"
      fi
      # Every window and agent starts with the project's zone; the CLI
      # moves it (and /etc/repose/env) when the laptop's changes (I-198).
      tz=$(sed -n 's/^TZ=//p' /etc/repose/env 2>/dev/null | tail -n 1 || true)
      if [ -n "$tz" ]; then
        tmux set-environment -g TZ "$tz"
      fi
    '';
  };
in
{
  programs.tmux = {
    enable = true;
    # One socket path for everyone: /tmp/tmux-1000/default. With the secure
    # socket the server started by the user unit and a client in an SSH
    # session would look in different places (TMUX_TMPDIR is only set in
    # login shells).
    secureSocket = false;
    # Written to /etc/tmux.conf; guest-conventions.md "tmux" lists these.
    historyLimit = 50000;
    escapeTime = 10;
    terminal = "tmux-256color";
    extraConfig = ''
      set -g set-clipboard on
      # Mouse off (DECISIONS I-364): the laptop terminal's own selection,
      # copy and scrollback work as they do outside tmux. A user who wants
      # tmux's mouse puts `set -g mouse on` in ~/.tmux.conf.
      set -g mouse off
      set -ga terminal-overrides ",*:Tc"
      set -g focus-events on
      # Modified keys reach the program in the pane (DECISIONS I-264):
      # Shift+Enter is a newline in Claude Code, not a submit. tmux asks
      # the laptop's terminal for extended keys (extkeys, for every
      # TERM: a terminal without them ignores the request) and hands them
      # on as CSI u to a program that asks for them.
      set -g extended-keys on
      set -g extended-keys-format csi-u
      set -as terminal-features ",*:extkeys"
      # OSC 8 hyperlinks from a program in the pane are drawn as links on
      # the laptop's terminal; a pane in view may pass other escape
      # sequences through (DCS tmux; ...) to it.
      set -as terminal-features ",*:hyperlinks"
      set -g allow-passthrough on
      set -g default-shell ${pkgs.bash}/bin/bash
      # Env the CLI sets on the SSH session should reach new windows. TZ
      # is not among them: an attach from a terminal without TZ would
      # clear the session's zone, and agents started in new windows would
      # run in UTC. The global TZ (set at session creation and by the
      # CLI on every run and attach, I-198) is what windows get.
      set -g update-environment "DISPLAY SSH_AUTH_SOCK SSH_CONNECTION LANG COLORTERM"
      # The status clock is tmux's default status-right with the time from
      # `date` instead of the server's strftime: the server keeps the zone
      # it started with, while a #() job runs with the global environment,
      # whose TZ the carry moves (I-198, DECISIONS I-215). One fork per
      # status-interval.
      # Messages (the CLI's port forwards and carry notes, I-324) in the
      # status bar's own colours rather than tmux's yellow, which read as
      # a warning each time a dev server started.
      set -g message-style "bg=green,fg=black"
      set -g status-right '#{?window_bigger,[#{window_offset_x}#,#{window_offset_y}] ,}"#{=21:pane_title}" #(date "+%%H:%%M %%d-%%b-%%y")'
    '';
  };

  environment.systemPackages = [ tmuxSession ];

  systemd.user.services.repose-tmux-session = {
    description = "repose: project tmux session";
    after = [ "default.target" ];
    unitConfig.ConditionPathExists = "%h/.repose/project.json";
    # Never restarted by a switch: the unit's cgroup holds the tmux server
    # and, through KillMode=control-group, every pane, so a restart ends
    # every agent and shell on the machine. A base whose change touched
    # this unit (CPUWeight, I-494) did that to every running guest at the
    # 2026-10-05 04:00 sweep (DECISIONS I-496). A changed unit takes
    # effect at the session's next start.
    restartIfChanged = false;
    serviceConfig = {
      Type = "forking";
      # Skipped when project.json names herdr or the herdr server runs
      # (DECISIONS I-503).
      ExecCondition = "${multiplexerIs}/bin/repose-multiplexer-is tmux";
      ExecStart = "${tmuxSession}/bin/repose-tmux-session";
      # The tmux server exits with its last session (`exit` in the last
      # window, `tmux kill-server`); the unit then starts it again, so
      # `repose attach` and `repose run "prompt"` find a session. The path
      # unit did this until I-503 removed it (DECISIONS I-551). The 5 s
      # wait lets the CLI's check after an attach on a temporary machine
      # (I-352) see the session gone before it comes back. A skipped
      # start (ExecCondition) and a `systemctl stop` are never restarted.
      Restart = "always";
      RestartSec = "5s";
      KillMode = "control-group";
      # The tmux server draws every pane, so it gets ten times a pane's
      # share of CPU (each pane is its own tmux-spawn scope at the default
      # 100): with a build on every core, what you type still shows at
      # once (DECISIONS I-494).
      CPUWeight = 1000;
    };
  };

  # Each SSH connection, with its sshd and the `tmux attach` client, is a
  # logind session scope; the same weight keeps the keystrokes moving
  # while user@1000.service's panes hold every core (DECISIONS I-494). A
  # prefix drop-in, so it reaches the transient session-N.scope units.
  systemd.units."session-.scope" = {
    overrideStrategy = "asDropin";
    text = ''
      [Scope]
      CPUWeight=1000
    '';
  };

  systemd.tmpfiles.rules = [
    "d /home/dev/.repose 0700 dev dev -"
  ];
}
