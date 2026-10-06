# repose-tmux-reload: bring dev's running tmux server in line with its
# config files after home-manager switches (DECISIONS I-552).
#
# tmux reads /etc/tmux.conf and the user's files once, when the server
# starts, and the server lives as long as the machine's session
# (restartIfChanged = false, I-496). A fragment or machine.nix that adds,
# changes or removes ~/.config/tmux/tmux.conf therefore changed nothing on
# a running machine, and a user who sourced their file into the server by
# hand kept its options after the file was gone: removing a machine.nix
# left its status bar behind. Sourcing the files again is not enough
# either, since it only adds (and appends to array options such as
# terminal-features each time). So when the files' contents changed since
# the last run, this puts every global option and key binding back to
# tmux's defaults and loads the files again, in tmux's own order. Options
# a session sets for itself (the CLI's port forward line) and every
# window and pane are left as they are.
#
# It runs as dev after each home-manager activation (microvm.nix). With no
# server running, or on the first run, it only records the files' digest.
{ writeShellApplication, tmux, coreutils, gawk }:
writeShellApplication {
  name = "repose-tmux-reload";
  runtimeInputs = [ tmux coreutils gawk ];
  text = ''
    state="''${XDG_STATE_HOME:-$HOME/.local/state}/repose/tmux-conf.sha256"
    config="''${XDG_CONFIG_HOME:-$HOME/.config}"

    # tmux's own list, in its order, each file once.
    files=()
    for f in /etc/tmux.conf "$HOME/.tmux.conf" "$config/tmux/tmux.conf" "$HOME/.config/tmux/tmux.conf"; do
      dup=
      for g in "''${files[@]}"; do [ "$g" = "$f" ] && dup=1; done
      [ -n "$dup" ] || files+=("$f")
    done

    sum=$(for f in "''${files[@]}"; do printf '== %s\n' "$f"; cat "$f" 2>/dev/null || true; done | sha256sum | cut -d' ' -f1)
    mkdir -p "$(dirname "$state")"
    prev=$(cat "$state" 2>/dev/null || true)
    printf '%s\n' "$sum" > "$state"

    if [ -z "$prev" ] || [ "$prev" = "$sum" ] || ! tmux list-sessions >/dev/null 2>&1; then
      exit 0
    fi

    tmp=$(mktemp)
    trap 'rm -f "$tmp"' EXIT

    # tmux's default key bindings, from a server that read no file.
    probe="repose-reload-$$"
    tmux -L "$probe" -f /dev/null start-server \; list-keys > "$tmp"
    tmux -L "$probe" kill-server 2>/dev/null || true

    names() { awk '{ sub(/\[.*/, "", $1); print $1 }' | sort -u; }
    tmux show-options -s | names | while read -r o; do tmux set-option -su "$o" 2>/dev/null || true; done
    tmux show-options -g | names | while read -r o; do tmux set-option -gu "$o" 2>/dev/null || true; done
    tmux show-options -gw | names | while read -r o; do tmux set-option -gwu "$o" 2>/dev/null || true; done
    tmux list-keys | awk '$2 == "-T" { print $3 } $2 != "-T" { print $4 }' | sort -u \
      | while read -r t; do tmux unbind-key -a -T "$t" 2>/dev/null || true; done
    tmux source-file "$tmp" || true

    # A file with an error still loads its other lines, as at server start.
    rc=0
    for f in "''${files[@]}"; do
      if [ -r "$f" ]; then tmux source-file "$f" || rc=1; fi
    done
    echo "repose-tmux-reload: reloaded tmux's configuration"
    exit "$rc"
  '';
}
