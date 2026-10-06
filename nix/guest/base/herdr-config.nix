# The seeded ~/.config/herdr/config.toml and the reload after a
# home-manager switch (DECISIONS I-501, I-563). herdr.nix runs the seed
# before the server starts; microvm.nix runs the reload after each
# home-manager activation, where repose-tmux-reload does the same for tmux
# (I-552).
{ pkgs, herdr }:
let
  # Written only when no file (or link) is there: panes start login
  # shells as tmux's do (herdr's Linux default is non-login), herdr
  # does not look for releases, since the base's pinned one is the
  # supported one, and herdr shows notifications, which carry repose's
  # messages (copied files, forwards, the time zone; DECISIONS I-564). A file that exists is never changed; herdr itself edits
  # it (onboarding), so it is dev's and writable.
  seedConfig = pkgs.writeText "herdr-config.toml" ''
    [terminal]
    shell_mode = "login"

    [update]
    version_check = false

    [ui.toast]
    delivery = "herdr"
  '';
  seed = pkgs.writeShellApplication {
    name = "repose-herdr-config";
    runtimeInputs = [ pkgs.coreutils ];
    text = ''
      dir="''${XDG_CONFIG_HOME:-''${HOME:-/home/dev}/.config}/herdr"
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

  # repose-herdr-reload: herdr reads config.toml when its server starts,
  # and the server lives as long as the machine's session (I-496). A
  # machine.nix or fragment that adds, changes or removes
  # ~/.config/herdr/config.toml would reach a running machine only at its
  # next start. When a herdr server's socket is there, this puts the seed
  # back if the file went away, and when the file's contents changed since
  # its last run it asks the server to reload (`herdr server reload-config`;
  # a missing file is herdr's defaults). With no server it records the
  # digest only. Never fails the switch: every error is ignored.
  reload = pkgs.writeShellApplication {
    name = "repose-herdr-reload";
    runtimeInputs = [ herdr seed pkgs.coreutils ];
    text = ''
      config="''${XDG_CONFIG_HOME:-$HOME/.config}/herdr"
      state="''${XDG_STATE_HOME:-$HOME/.local/state}/repose/herdr-conf.sha256"
      running=
      if [ -S "$config/herdr.sock" ]; then
        running=1
        repose-herdr-config || true
      fi
      sum=none
      if [ -r "$config/config.toml" ]; then
        sum=$(sha256sum < "$config/config.toml" | cut -d' ' -f1)
      fi
      old=$(cat "$state" 2>/dev/null || true)
      mkdir -p "$(dirname "$state")"
      printf '%s\n' "$sum" > "$state"
      if [ -z "$running" ] || [ -z "$old" ] || [ "$old" = "$sum" ]; then
        exit 0
      fi
      timeout 10 herdr server reload-config >/dev/null 2>&1 || true
    '';
  };
in
{ inherit seed reload; }
