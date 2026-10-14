# repose-browser-unlock PROFILE: remove Chromium's profile lock when no
# running process holds it (DECISIONS I-727).
#
# Chromium marks a profile in use with three links in it: SingletonLock
# (to "HOSTNAME-PID"), SingletonSocket and SingletonCookie. A browser that
# is killed (a machine stopped or frozen with the browser up, an OOM kill)
# leaves them behind. Chromium clears its own stale lock only when the
# host name matches and the PID is not a Chromium; a lock from another
# host name (a fork, a snapshot restored under a new name) makes it refuse
# the profile as "in use on another computer", and a PID that a later
# Chromium happens to reuse makes it wait on a socket nobody answers. So
# the agents' browser never started again on such a machine.
#
# The lock is stale unless the process it names is running with this
# profile on its command line; then the links are removed. A lock a live
# browser holds is left alone.
{ writeShellApplication, coreutils, gnugrep }:
writeShellApplication {
  name = "repose-browser-unlock";
  runtimeInputs = [ coreutils gnugrep ];
  text = ''
    if [ "$#" -ne 1 ]; then
      echo "usage: repose-browser-unlock PROFILE" >&2
      exit 64
    fi
    dir=$1
    lock=$dir/SingletonLock
    [ -L "$lock" ] || [ -e "$lock" ] || exit 0
    target=$(readlink "$lock" 2>/dev/null || true)
    pid=''${target##*-}
    case "$pid" in
      "" | *[!0-9]*) ;;
      *)
        if [ -r "/proc/$pid/cmdline" ] \
           && tr '\0' '\n' < "/proc/$pid/cmdline" | grep -qxF -- "--user-data-dir=$dir"; then
          exit 0
        fi
        ;;
    esac
    rm -f "$dir/SingletonLock" "$dir/SingletonSocket" "$dir/SingletonCookie"
    echo "removed a stale profile lock (''${target:-unreadable})"
  '';
}
