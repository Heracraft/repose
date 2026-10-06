# repose-herdr-watch (herdr.nix): run in the background by
# repose-herdr-server.service. Kills the unit's cgroup once no herdr
# server runs in it (DECISIONS I-560).
cg="/sys/fs/cgroup$(sed -n 's/^0:://p' /proc/self/cgroup)"
manager="${MANAGERPID:-}"
if [ -z "$manager" ] || [ ! -w "$cg/cgroup.kill" ]; then
  echo "repose-herdr-watch: no service manager or cgroup.kill; not watching" >&2
  exit 0
fi
# Prints the name of each process in the cgroup, other than this one,
# whose parent is the service manager: the unit's main process (bash
# before it execs herdr, then herdr) and a handed-off server.
from_manager() {
  local pid stat ppid
  while read -r pid; do
    [ "$pid" != "$$" ] || continue
    stat=$(cat "/proc/$pid/stat" 2>/dev/null) || continue
    # Field 4, the parent, after the ")" that ends the name.
    stat=${stat##*) }
    read -r _ ppid _ <<<"$stat"
    [ "$ppid" = "$manager" ] || continue
    cat "/proc/$pid/comm" 2>/dev/null || true
  done < "$cg/cgroup.procs"
}
interval=0.5
while :; do
  procs=$(from_manager)
  if grep -qx herdr <<<"$procs"; then
    interval=5
  elif [ "$interval" = 5 ] || [ -z "$procs" ]; then
    break
  fi
  sleep "$interval"
done
echo "repose-herdr-watch: no herdr server left; ending the unit" >&2
echo 1 > "$cg/cgroup.kill"
