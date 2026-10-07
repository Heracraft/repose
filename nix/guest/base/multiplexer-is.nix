# repose-multiplexer-is, a function of pkgs shared by tmux.nix and
# herdr.nix: both session units run it as their ExecCondition (DECISIONS
# I-503; guest-conventions.md "Session units").
{ pkgs }:
# repose-multiplexer-is <tmux|herdr>: exit 0 when project.json names
# that multiplexer (no key, or any value but "herdr", is tmux) and the
# other session unit is not active; 1 otherwise, and with no
# project.json for both. systemd skips a start whose ExecCondition
# exits 1 to 254.
pkgs.writeShellApplication {
  name = "repose-multiplexer-is";
  runtimeInputs = [ pkgs.jq pkgs.systemd pkgs.coreutils ];
  text = ''
    want="''${1:-}"
    case "$want" in
      tmux) other=repose-herdr-server.service ;;
      herdr) other=repose-tmux-session.service ;;
      *) echo "usage: repose-multiplexer-is tmux|herdr" >&2; exit 2 ;;
    esac
    project="''${HOME:-/home/dev}/.repose/project.json"
    [ -s "$project" ] || exit 1
    have=$(jq -r '.multiplexer // "tmux"' "$project" 2>/dev/null || echo tmux)
    [ "$have" = herdr ] || have=tmux
    [ "$have" = "$want" ] || exit 1
    if systemctl --user -q is-active "$other" 2>/dev/null; then
      exit 1
    fi
    exit 0
  '';
}
