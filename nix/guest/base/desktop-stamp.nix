# repose-desktop-stamp NAME...: touch the desktop's idle stamps in
# /run/repose/desktop and leave them dev's (DECISIONS I-727).
#
# The stamps (last-client, last-cdp) are touched by the per-minute idle
# check, which runs as root, and by the display's and the browser's own
# units, which run as dev. A stamp root made first could not be touched by
# dev afterwards, so the browser's start failed on its last step and the
# browser never came up. Every writer goes through this, as root (a unit
# runs it with the "+" prefix), so the owner is dev whoever wrote first.
{ writeShellApplication, coreutils }:
writeShellApplication {
  name = "repose-desktop-stamp";
  runtimeInputs = [ coreutils ];
  text = ''
    dir=''${REPOSE_DESKTOP_DIR:-/run/repose/desktop}
    owner=''${REPOSE_DESKTOP_OWNER:-dev:dev}
    for name in "$@"; do
      touch "$dir/$name"
      chown "$owner" "$dir/$name"
    done
  '';
}
