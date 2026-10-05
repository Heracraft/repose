# herdr, the second multiplexer (DECISIONS I-501, I-503): the package in
# every guest, and the session unit a project that chooses herdr runs in
# place of repose-tmux-session. guest-conventions.md "Session units" and
# "herdr" are the contract.
#
# Neither session unit is wanted by anything. guestd's SetupProject
# writes ~/.repose/project.json for this boot and starts the unit its
# `multiplexer` names (I-503); each unit's ExecCondition runs
# repose-multiplexer-is, so a start of the wrong one (an older guestd
# starting the tmux unit by name, a hand-typed start) is skipped without
# failing, and the two never run together.
{ config, lib, pkgs, ... }:
let
  checkout = import ./checkout.nix { inherit pkgs; };
  herdr = pkgs.reposeHerdr;

  multiplexerIs = import ./multiplexer-is.nix { inherit pkgs; };

  # repose-herdr-workspace: the herdr counterpart of tmux's `shell`
  # window. When the running herdr server has no workspace labelled with
  # the checkout's directory name (`home` on a machine with no checkout),
  # it creates one there without taking focus. The server unit runs it
  # after start; the CLI runs it after the first sync. Exits 0 when herdr
  # is not running, and is safe to run again.
  workspace = pkgs.writeShellApplication {
    name = "repose-herdr-workspace";
    runtimeInputs = [ herdr checkout pkgs.jq pkgs.systemd pkgs.coreutils ];
    text = ''
      state=$(systemctl --user show -p ActiveState --value repose-herdr-server.service 2>/dev/null || true)
      case "$state" in
        active|activating|reloading) ;;
        *) exit 0 ;;
      esac
      list=
      for _ in $(seq 40); do
        if list=$(timeout 5 herdr workspace list 2>/dev/null); then
          break
        fi
        list=
        sleep 0.25
      done
      if [ -z "$list" ]; then
        echo "repose-herdr-workspace: herdr did not answer within 10 s" >&2
        exit 0
      fi
      dir=$(repose-checkout)
      if [ "$dir" = "''${HOME:-/home/dev}" ]; then
        label=home
      else
        label=$(basename "$dir")
      fi
      if printf '%s' "$list" | jq -e --arg l "$label" \
        '[.result.workspaces[]? | select(.label == $l)] | length > 0' >/dev/null 2>&1; then
        exit 0
      fi
      if ! timeout 10 herdr workspace create --cwd "$dir" --label "$label" --no-focus >/dev/null; then
        echo "repose-herdr-workspace: herdr could not create the workspace $label" >&2
        exit 1
      fi
    '';
  };

  # The seeded ~/.config/herdr/config.toml, written only when no file (or
  # link) is there: panes start login shells as tmux's do (herdr's Linux
  # default is non-login), and herdr does not look for releases, since
  # the base's pinned one is the supported one. A file that exists is
  # never changed; herdr itself edits it (onboarding), so it is dev's
  # and writable.
  seedConfig = pkgs.writeText "herdr-config.toml" ''
    [terminal]
    shell_mode = "login"

    [update]
    version_check = false
  '';
  seed = pkgs.writeShellApplication {
    name = "repose-herdr-config";
    runtimeInputs = [ pkgs.coreutils ];
    text = ''
      dir="''${HOME:-/home/dev}/.config/herdr"
      cfg="$dir/config.toml"
      if [ -e "$cfg" ] || [ -L "$cfg" ]; then
        exit 0
      fi
      mkdir -p "$dir"
      tmp=$(mktemp -p "$dir" .config.toml.XXXXXX)
      cat ${seedConfig} > "$tmp"
      chmod 0644 "$tmp"
      mv -n "$tmp" "$cfg"
      rm -f "$tmp"
    '';
  };
in
{
  # /run/current-system/sw/bin/herdr resolves: a laptop herdr's remote
  # discovery looks there.
  environment.systemPackages = [ herdr multiplexerIs workspace ];

  systemd.user.services.repose-herdr-server = {
    description = "repose: project herdr server";
    after = [ "default.target" ];
    # Never restarted by a switch: the unit's cgroup holds the herdr
    # server and every pane (I-496). A changed unit takes effect at the
    # server's next start.
    restartIfChanged = false;
    serviceConfig = {
      Type = "simple";
      ExecCondition = "${multiplexerIs}/bin/repose-multiplexer-is herdr";
      ExecStartPre = "${seed}/bin/repose-herdr-config";
      # A login shell, so the server and every pane get the login PATH
      # with each user bin dir (I-227); `herdr update` in the guest lands
      # in ~/.local/bin, which comes first.
      ExecStart = "${pkgs.bash}/bin/bash -lc 'exec herdr server'";
      # A failed workspace step never stops the server.
      ExecStartPost = "-${workspace}/bin/repose-herdr-workspace";
      # The project's zone (TZ) and REPOSE_PROJECT.
      EnvironmentFile = "-/etc/repose/env";
      KillMode = "control-group";
      # A restarted server restores session.json and resumes its agents.
      Restart = "on-failure";
      TasksMax = "infinity";
      # No CPUWeight: herdr's panes live in this unit's cgroup, so a
      # weight would lift every build with them. guestd renices the
      # server's threads instead (I-505).
    };
  };
}
