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
# for every tmux project. The unit brings the session back after
# the tmux server exits on a running machine, which the path unit used to
# do (DECISIONS I-551).
{ config, lib, pkgs, ... }:
let
  checkout = import ./checkout.nix { inherit pkgs; };
  multiplexerIs = import ./multiplexer-is.nix { inherit pkgs; };
  loginPath = import ./login-path.nix { inherit pkgs; };
  tmuxSession = pkgs.writeShellApplication {
    name = "repose-tmux-session";
    runtimeInputs = [ pkgs.tmux pkgs.jq pkgs.coreutils pkgs.gnused pkgs.bash pkgs.util-linux checkout loginPath ];
    text = ''
      project="$HOME/.repose/project.json"
      # Exit 3: nothing for the unit to keep running. A failed start is
      # never restarted (Restart=on-success), so a start that leaves no
      # tmux server in the unit's cgroup ends there instead of starting
      # again every 5 s (DECISIONS I-560).
      if [ ! -s "$project" ]; then
        echo "repose-tmux-session: $project missing; nothing to do" >&2
        exit 3
      fi
      slug=$(jq -r '.slug // empty' "$project")
      if [ -z "$slug" ]; then
        echo "repose-tmux-session: project.json has no slug" >&2
        exit 1
      fi
      dir=$(repose-checkout)
      created=""
      if ! tmux has-session -t "=$slug" 2>/dev/null; then
        # -d: detached. A server this starts runs in the unit's cgroup.
        tmux new-session -d -s "$slug" -n shell -c "$dir"
        created=1
      fi
      # Run by the unit, the session must belong to a server in the
      # unit's cgroup. A server someone started outside it (`tmux` in an
      # ssh terminal while the unit waited to restart) answers on the same
      # socket: has-session finds the session there, or new-session adds
      # it there, and the unit would be left with nothing to supervise.
      # Run by hand (from a pane, say), the script skips the check.
      self_cg=$(cat /proc/self/cgroup)
      if [ "''${self_cg%/repose-tmux-session.service}" != "$self_cg" ]; then
        server=$(tmux display-message -p -t "=$slug:" '#{pid}' 2>/dev/null || true)
        if [ -z "$server" ] || [ "$(cat "/proc/$server/cgroup" 2>/dev/null)" != "$self_cg" ]; then
          echo "repose-tmux-session: session $slug is on a tmux server started outside this unit; leaving it" >&2
          exit 3
        fi
      fi
      # The server copied this unit's PATH (NixOS gives every unit its
      # own). What runs with the server's environment (run-shell, #()
      # jobs, a window opened from inside tmux with a command) gets the
      # login PATH instead, with every user bin dir (DECISIONS I-227).
      # repose-login-path reads it without letting the user's profile
      # print into it or replace the shell (DECISIONS I-563).
      login_path=$(repose-login-path || true)
      if [ -n "$login_path" ]; then
        tmux set-environment -g PATH "$login_path"
      fi
      # Every window and agent starts with the project's zone; the CLI
      # moves it (and /etc/repose/env) when the laptop's changes (I-198).
      tz=$(sed -n 's/^TZ=//p' /etc/repose/env 2>/dev/null | tail -n 1 || true)
      if [ -n "$tz" ]; then
        tmux set-environment -g TZ "$tz"
      fi
      # The agents' windows come back under the same names, each agent on
      # its conversation (DECISIONS I-636, I-646). Only in a session this
      # run created, and only from a record of an earlier boot:
      #   1. agent-windows, written at a stop, wins;
      #   2. agent-windows.live, the minute record of an earlier boot,
      #      with no stop marker for that boot: the machine ended without
      #      a stop (a host crash or restart);
      #   3. the same with a stop marker: the stop's save failed or the
      #      machine was killed after it began; the windows reopen as in 2;
      #   4. a minute record of this boot: tmux exited and the unit
      #      started it again, which reopens nothing (I-551).
      # The record is renamed before it is read, so a server that exits
      # later in the boot starts with `shell` alone. The lock keeps the
      # minute save from writing while this runs (repose-tmux-save).
      if [ -n "$created" ]; then
        exec 9>>"$HOME/.repose/agent-windows.lock"
        flock -w 10 9 || true
        saved="$HOME/.repose/agent-windows"
        live="$saved.live"
        boot=$(cat /proc/sys/kernel/random/boot_id 2>/dev/null || true)
        from=""
        if [ -s "$saved" ] && mv -f "$saved" "$saved.restoring"; then
          from=stop
          rm -f "$live"
        elif [ -s "$live" ]; then
          live_boot=$(sed -n '1s/^#boot //p' "$live")
          if [ -n "$live_boot" ] && [ "$live_boot" != "$boot" ] && mv -f "$live" "$saved.restoring"; then
            stopped=$(cat "$HOME/.repose/stopped" 2>/dev/null || true)
            if [ "$stopped" = "$live_boot" ]; then
              from=stop-unsaved
            else
              from=minute
            fi
          fi
        fi
        if [ -n "$from" ]; then
          echo "repose-tmux-session: reopening agent windows from the $from record" >&2
          tmux_bin=$(command -v tmux)
          window_path=''${login_path:-/run/current-system/sw/bin:$PATH}
          seen=" "
          # The stop record has 4 fields, the minute record 5 (the
          # agent's state, unused here) after a `#boot` line.
          while IFS="$(printf '\037')" read -r name agent cwd sid _; do
            case "$name" in "#"*) continue ;; esac
            [ -n "$name" ] && [ -d "$cwd" ] || continue
            case "$sid" in *[!A-Za-z0-9_-]*) sid="" ;; esac
            if [ -z "$sid" ]; then
              # Without its own id an agent continues the folder's newest
              # conversation; a second such window there would continue
              # the same one, so it is not reopened.
              key="$agent:$cwd"
              case "$seen" in *" $key "*) continue ;; esac
              seen="$seen$key "
            fi
            case "$agent" in
              claude) if [ -n "$sid" ]; then cmd="claude --resume $sid"; else cmd="claude --continue"; fi ;;
              codex) if [ -n "$sid" ]; then cmd="codex resume $sid"; else cmd="codex resume --last"; fi ;;
              opencode) cmd="opencode --continue" ;;
              pi) cmd="pi --continue" ;;
              gemini) cmd="gemini --resume latest" ;;
              *) continue ;;
            esac
            # The window closes when the agent exits, as one `repose run`
            # opened does, except when the agent refuses to resume (a
            # conversation the crash cut mid-line, one that is gone): it
            # exits non-zero within seconds, and the window keeps a login
            # shell under a line that says so instead of vanishing.
            # `set -m` gives the agent its own process group, the
            # terminal's foreground one, so tmux names the agent as the
            # pane's command, which the save reads.
            wrapped="set -m; s=\$SECONDS; $cmd; rc=\$?; set +m; if [ \$rc -ne 0 ] && [ \$((SECONDS - s)) -lt 15 ]; then echo \"$agent could not continue its conversation (exit \$rc).\"; exec bash -l; fi; exit \$rc"
            # A client that is not attached gives the new window its own
            # PATH, not the server's: this unit's PATH has no agent on it
            # (the restored window exited 127, I-640). The login PATH it is.
            PATH="$window_path" "$tmux_bin" new-window -d -t "=$slug:" -n "$name" -c "$cwd" "$wrapped" || true
          done < "$saved.restoring"
          rm -f "$saved.restoring"
        fi
        flock -u 9 || true
        exec 9>&-
      fi
    '';
  };
  # repose-tmux-save records the session's agent windows (DECISIONS
  # I-636, I-646). One line per window that runs an agent: name, agent,
  # folder, and the conversation id the agent's hook put in the pane's
  # @repose-session, separated by \037.
  #
  # With no argument it is the stop's save: guestd runs it as dev when
  # hostd asks for the shutdown, while every agent still runs, after it
  # wrote ~/.repose/stopped with this boot's id. It writes agent-windows
  # (4 fields, which a restore script from before I-646 reads) and removes
  # the minute record. No agent window removes agent-windows; a tmux that
  # does not answer removes agent-windows and leaves the minute record,
  # which the next start then reopens.
  #
  # With --periodic it is the minute save (repose-tmux-save.timer):
  # agent-windows.live, a `#boot <boot id>` line, then a 5th field per
  # row, the window's @repose-state. Written only when it changed, by
  # temp file, sync, rename and a sync of the folder, so a host crash
  # leaves the old record or the new one. A tmux that does not answer
  # changes nothing; no agent window writes the header alone. A record
  # of another boot is replaced only once the session is two minutes
  # old: the restore consumes it. Both modes and the restore hold one
  # lock.
  tmuxSave = pkgs.writeShellApplication {
    name = "repose-tmux-save";
    runtimeInputs = [ pkgs.tmux pkgs.jq pkgs.coreutils pkgs.gnused pkgs.util-linux ];
    text = ''
      # guestd runs this with no locale (POSIX), where tmux prints the
      # \037 below as `_` and no line splits (I-640). Any UTF-8 locale
      # passes it through; C.UTF-8 is built into glibc.
      export LC_ALL=C.UTF-8
      periodic=""
      if [ "''${1:-}" = "--periodic" ]; then
        periodic=1
      fi
      dir="$HOME/.repose"
      out="$dir/agent-windows"
      live="$out.live"
      slug=$(jq -r '.slug // empty' "$dir/project.json" 2>/dev/null || true)
      [ -n "$slug" ] || exit 0
      exec 9>>"$dir/agent-windows.lock"
      if [ -n "$periodic" ]; then
        # The restore or the stop's save has it: this minute is skipped.
        flock -n 9 || exit 0
      else
        flock -w 3 9 || true
      fi
      boot=$(cat /proc/sys/kernel/random/boot_id 2>/dev/null || true)
      # \037, not a tab: read collapses empty fields between tabs.
      us=$(printf '\037')
      if ! rows=$(tmux list-windows -t "=$slug" -F "#{window_name}$us#{pane_current_command}$us#{@repose-state}$us#{pane_current_path}$us#{@repose-session}" 2>/dev/null); then
        [ -n "$periodic" ] || rm -f "$out"
        exit 0
      fi
      if [ -n "$periodic" ]; then
        [ -n "$boot" ] || exit 0
        if [ -e "$live" ]; then
          live_boot=$(sed -n '1s/^#boot //p' "$live" 2>/dev/null || true)
          if [ "$live_boot" != "$boot" ]; then
            # Another boot's record is the restore's to consume. A session
            # that has lived two minutes without consuming it was not
            # created by the session unit (a tmux started over ssh, I-560),
            # and then this boot's record replaces it.
            created=$(tmux display-message -p -t "=$slug:" '#{session_created}' 2>/dev/null || echo 0)
            case "$created" in ""|*[!0-9]*) created=0 ;; esac
            [ $(( $(date +%s) - created )) -ge 120 ] || exit 0
          fi
        fi
      fi
      body=""
      while IFS="$us" read -r name command state cwd sid; do
        base=''${name##*/}
        [[ $base =~ ^(claude|codex|opencode|gemini|pi)(-[0-9]+)?$ ]] || continue
        agent=''${base%-[0-9]*}
        # A window whose agent exited holds a shell: nothing to resume.
        if [ -z "$state" ]; then
          case "$command" in bash|sh|zsh|fish|"") continue ;; esac
        fi
        if [ -n "$periodic" ]; then
          body+=$(printf '%s\037%s\037%s\037%s\037%s' "$name" "$agent" "$cwd" "$sid" "$state")$'\n'
        else
          body+=$(printf '%s\037%s\037%s\037%s' "$name" "$agent" "$cwd" "$sid")$'\n'
        fi
      done <<< "$rows"
      if [ -n "$periodic" ]; then
        target="$live"
        body="#boot $boot"$'\n'"$body"
        if [ -e "$live" ] && [ "$(cat "$live")"$'\n' = "$body" ]; then
          exit 0
        fi
      else
        target="$out"
        if [ -z "$body" ]; then
          rm -f "$out" "$live"
          sync "$dir" || true
          exit 0
        fi
      fi
      tmp=$(mktemp "$target.XXXXXX")
      printf '%s' "$body" > "$tmp"
      sync "$tmp"
      mv -f "$tmp" "$target"
      [ -n "$periodic" ] || rm -f "$live"
      sync "$dir"
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
      # 24-bit colour only to terminals that have it (DECISIONS I-515): the
      # TERMs below, whose terminfo the base ships (I-512), and any client
      # started with COLORTERM=truecolor, which tmux 3.7 takes as RGB for
      # that client. PAM gives every ssh session truecolor; the CLI's
      # attach unsets it when the laptop's COLORTERM does not say so.
      # Others, Apple's Terminal before macOS 26 among them, get 256
      # colours.
      set -as terminal-features ",xterm-ghostty:RGB,xterm-kitty:RGB,alacritty:RGB,wezterm:RGB,foot*:RGB,*-direct:RGB"
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
      # The laptop's tab or window title names the machine and session.
      set -g set-titles on
      set -g set-titles-string "#h: #S"
      # A window whose agent waits for input (guestd's @repose-state,
      # DECISIONS I-606) shows `?` after its name, so an attached user
      # sees which other agent needs them. tmux's default formats
      # otherwise.
      set -g window-status-format '#I:#W#{?#{==:#{@repose-state},needs_input},?,}#{?window_flags,#{window_flags}, }'
      set -g window-status-current-format '#I:#W#{?#{==:#{@repose-state},needs_input},?,}#{?window_flags,#{window_flags}, }'
      set -g status-right '#{?window_bigger,[#{window_offset_x}#,#{window_offset_y}] ,}"#{=21:pane_title}" #(date "+%%H:%%M %%d-%%b-%%y")'
    '';
  };

  environment.systemPackages = [ tmuxSession tmuxSave ];

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
      # `repose attach` and `repose run -p PROMPT` find a session. The path
      # unit did this until I-503 removed it (DECISIONS I-551). The 5 s
      # wait lets the CLI's check after an attach on a temporary machine
      # (I-352) see the session gone before it comes back. A server that
      # crashed or was killed (SIGKILL is also the OOM kill) comes back
      # the same way. A skipped start (ExecCondition), a `systemctl stop`
      # and a failed start are never restarted: a start that leaves no
      # server in the unit (exit 3) or finds no slug (exit 1) would fail
      # the same way every 5 s, below the start limit. Restart=always
      # restarts a failed start too, and RestartPreventExitStatus reads
      # the main process, which a forking unit's start is not (I-560).
      Restart = "on-success";
      RestartForceExitStatus = "SIGKILL SIGSEGV SIGABRT SIGBUS";
      RestartSec = "5s";
      KillMode = "control-group";
      # The tmux server draws every pane, so it gets ten times a pane's
      # share of CPU (each pane is its own tmux-spawn scope at the default
      # 100): with a build on every core, what you type still shows at
      # once (DECISIONS I-494).
      CPUWeight = 1000;
    };
  };

  # The minute record of the agent windows, which a machine that ends
  # without a stop (a host crash or restart) reopens at its next start
  # (DECISIONS I-646). System units, so a switch starts the timer on a
  # running machine; run as dev, whose tmux socket it reads. Skipped on
  # a herdr project, whose own server records its tabs.
  systemd.services.repose-tmux-save = {
    description = "repose: record the agent windows";
    # No ConditionPathExists: a start skipped by a condition never
    # leaves `inactive`, and OnUnitActiveSec counts from that. The
    # ExecCondition (no project.json, a herdr project) skips the run
    # after the unit has started, and the script exits 0 with no slug.
    # No start limit: a burst of starts beside the timer (the VM test
    # makes one) must not fail any of them (DECISIONS I-646).
    unitConfig.StartLimitIntervalSec = 0;
    environment = {
      HOME = "/home/dev";
      XDG_RUNTIME_DIR = "/run/user/1000";
    };
    serviceConfig = {
      Type = "oneshot";
      User = "dev";
      ExecCondition = "${multiplexerIs}/bin/repose-multiplexer-is tmux";
      ExecStart = "${tmuxSave}/bin/repose-tmux-save --periodic";
      Nice = 10;
      IOSchedulingClass = "idle";
      TimeoutStartSec = 30;
    };
  };
  systemd.timers.repose-tmux-save = {
    description = "repose: record the agent windows every minute";
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnBootSec = 60;
      OnUnitActiveSec = 60;
      AccuracySec = "5s";
    };
  };

  # The SSH session scopes' CPU weight, and this unit's MemoryLow and
  # OOMPolicy, are in keystroke-path.nix (DECISIONS I-494, I-576).

  systemd.tmpfiles.rules = [
    "d /home/dev/.repose 0700 dev dev -"
  ];
}
