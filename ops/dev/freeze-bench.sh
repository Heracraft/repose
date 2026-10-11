#!/usr/bin/env bash
# Guest freeze experiments on a KVM box (docs/research/guest-freeze.md,
# DECISIONS I-652). Boots the repose guest runner under Cloud Hypervisor the
# way hostd does (ext4 volume, virtiofsd store share, tap, vsock), and
# snapshots, kills and restores it. Run as root. Not a host tool: hostd's
# freeze lives in internal/hostd/guest/freeze.go.
#
#   freeze-bench.sh up ID CLASS          volume, tap, virtiofsd, boot, setup
#   freeze-bench.sh freeze ID            pause, snapshot, kill CH and virtiofsd, drop tap
#                                        (ENC=1 dm-crypt image volume, DROP=1 guest drops caches first)
#   freeze-bench.sh thaw ID [MODE]       tap, virtiofsd, restore (copy|ondemand), resume, set the clock
#                                        (COLD=1 drops the host page cache first)
#   freeze-bench.sh down ID              stop everything, keep the volume
#   freeze-bench.sh rm ID                remove everything
#   freeze-bench.sh x ID USER CMD...     run a command in the guest
#   freeze-bench.sh hfreeze|hthaw ID     the same through hostd's code (KEY, REC, VG)
#
# BALLOON=1 on up adds a virtio-balloon with free page reporting.
# Environment: RUNNER (guest runner, default /tmp/gf/runner), FB (freezebench
# binary), ROOT (work dir, default /mnt/nixstore/gf).
set -euo pipefail
RUNNER=${RUNNER:-/tmp/gf/runner}
FB=${FB:-/tmp/gf/freezebench}
ROOT=${ROOT:-/mnt/nixstore/gf}
CH=${CH:-}; [ -n "$CH" ] || CH=$(grep -o '/nix/store/[^ ]*cloud-hypervisor[^/]*/bin/cloud-hypervisor' "$(readlink -f "$RUNNER/bin/run")" | head -1)

now_ms() { date +%s%3N; }
api() { # api ID METHOD PATH [JSON]
  local sock=$ROOT/$1/ch.sock
  if [ -n "${4:-}" ]; then
    curl -sS --fail-with-body --unix-socket "$sock" -X "$2" -H 'Content-Type: application/json' -d "$4" "http://localhost/api/v1/$3"
  else
    curl -sS --fail-with-body --unix-socket "$sock" -X "$2" "http://localhost/api/v1/$3"
  fi
}
num() { echo "$1" | tr -dc 0-9 | sed 's/^0*//;s/^$/0/'; }
mem_of() { case "$1" in small) echo 4096 ;; large) echo 8192 ;; xl) echo 16384 ;; esac; }
cpu_of() { case "$1" in small) echo 2 ;; large) echo 4 ;; xl) echo 8 ;; esac; }
tap_of() { echo "gf$1"; }

net_up() {
  local id=$1 n; n=$(num "$id")
  local tap; tap=$(tap_of "$id")
  ip link show "$tap" >/dev/null 2>&1 || ip tuntap add "$tap" mode tap
  ip addr replace "10.99.$n.1/24" dev "$tap"
  ip link set "$tap" up
}
net_down() { ip link del "$(tap_of "$1")" 2>/dev/null || true; }

vfs_up() {
  local d=$ROOT/$1
  mkdir -p "$d"
  rm -f "$d/virtiofsd.sock"
  setsid "$RUNNER/bin/virtiofsd" --socket "$d/virtiofsd.sock" --sandbox none >>"$d/virtiofsd.log" 2>&1 < /dev/null &
  echo $! > "$d/virtiofsd.pid"
  for _ in $(seq 200); do [ -S "$d/virtiofsd.sock" ] && return 0; sleep 0.01; done
  echo "virtiofsd socket missing" >&2; return 1
}

kill_pidfile() {
  local f=$1
  [ -f "$f" ] || return 0
  local p; p=$(cat "$f")
  kill -9 "$p" 2>/dev/null || true
  while kill -0 "$p" 2>/dev/null; do sleep 0.01; done
  rm -f "$f"
}

up() {
  local id=$1 class=$2 d=$ROOT/$1 n; n=$(num "$id")
  mkdir -p "$d"
  echo "$class" > "$d/class"
  if [ ! -f "$d/vol.img" ]; then
    truncate -s 20G "$d/vol.img"
    mkfs.ext4 -q -L guest -E lazy_itable_init=1 "$d/vol.img"
  fi
  net_up "$id"
  vfs_up "$id"
  rm -f "$d/ch.sock" "$d/vsock.sock"
  local t0; t0=$(now_ms)
  setsid "$RUNNER/bin/run" --guest-id "gf$id" --ip "10.99.$n.2" --gateway "10.99.$n.1" --netmask 255.255.255.0 \
    --cid $((1000 + n)) --volume "$d/vol.img" --tap "$(tap_of "$id")" --mac "52:54:00:99:00:$(printf %02x "$n")" \
    --vcpu "$(cpu_of "$class")" --mem "$(mem_of "$class")" --hostname "gf$id" --state-dir "$d" \
    --serial "$d/console.log" ${BALLOON:+-- --balloon size=0,free_page_reporting=on} >>"$d/ch.log" 2>&1 < /dev/null &
  echo $! > "$d/ch.pid"
  local ms; ms=$("$FB" wait "$d" 120s)
  "$FB" register "$d" "${REG:-/tmp/gf/reg.db}"
  "$FB" setup "$d" "gf$id"
  echo "boot_ms=$ms ready_total_ms=$(( $(now_ms) - t0 ))"
}

# ENC=1 puts the image on a dm-crypt (plain, aes-xts-plain64, 512-bit
# random key) ext4 volume, as hostd does on a thin volume (I-653); here the
# volume is a loop device over a sparse file and the key sits in /dev/shm.
enc_open() { # enc_open ID new|old
  local id=$1 d=$ROOT/$1 mem
  mem=$(mem_of "$(cat "$d/class")")
  if [ "$2" = new ]; then
    rm -f "$d/frz.img"
    truncate -s $(( (mem + 256) * 1024 * 1024 )) "$d/frz.img"
    head -c 64 /dev/urandom > "/dev/shm/gf$id.key"
  fi
  losetup -f --show "$d/frz.img" > "$d/frz.loop"
  cryptsetup open --type plain --cipher aes-xts-plain64 --key-size 512 --key-file "/dev/shm/gf$id.key" "$(cat "$d/frz.loop")" "gffrz$id"
  if [ "$2" = new ]; then
    mkfs.ext4 -q -E nodiscard,lazy_itable_init=1,lazy_journal_init=1 -m 0 "/dev/mapper/gffrz$id"
  fi
  mkdir -p "$d/freeze"
  mount -o noatime "/dev/mapper/gffrz$id" "$d/freeze"
}
enc_close() {
  local id=$1 d=$ROOT/$1
  umount "$d/freeze"
  cryptsetup close "gffrz$id"
  losetup -d "$(cat "$d/frz.loop")"
}

freeze() {
  local id=$1 d=$ROOT/$1
  local s="$d/snap" t0 te t1 t2 t3 tc
  t0=$(now_ms)
  if [ "${ENC:-0}" = 1 ]; then
    enc_open "$id" new
    s="$d/freeze/snap"
  fi
  te=$(now_ms)
  rm -rf "$s"; mkdir -p "$s"
  if [ "${DROP:-0}" = 1 ]; then
    "$FB" exec "$d" root sh -c 'sync; echo 3 > /proc/sys/vm/drop_caches' || true
  fi
  tc=$(now_ms)
  api "$id" PUT vm.pause >/dev/null
  t1=$(now_ms)
  api "$id" PUT vm.snapshot "{\"destination_url\":\"file://$s\"}" >/dev/null
  t2=$(now_ms)
  local apparent real
  apparent=$(du -sb --apparent-size "$s" 2>/dev/null | awk '{print $1}' || true)
  real=$(du -sB1 "$s" 2>/dev/null | awk '{print $1}' || true)
  api "$id" PUT vmm.shutdown >/dev/null 2>&1 || true
  kill_pidfile "$d/ch.pid"
  kill_pidfile "$d/virtiofsd.pid"
  net_down "$id"
  if [ "${ENC:-0}" = 1 ]; then
    sync -f "$s/memory-ranges" 2>/dev/null || true
    enc_close "$id"
  else
    sync -f "$s/memory-ranges" 2>/dev/null || true
  fi
  t3=$(now_ms)
  echo "prep_ms=$((te - t0)) drop_ms=$((tc - te)) pause_ms=$((t1 - tc)) snapshot_ms=$((t2 - t1)) total_ms=$((t3 - t0)) image_apparent=$apparent image_disk=$real"
}

thaw() {
  local id=$1 mode=${2:-copy} d=$ROOT/$1
  local t0 t1 t2 t3 t4; t0=$(now_ms)
  local s="$d/snap"
  if [ "${ENC:-0}" = 1 ]; then
    enc_open "$id" old
    s="$d/freeze/snap"
  fi
  if [ "${COLD:-0}" = 1 ]; then
    sync; echo 3 > /proc/sys/vm/drop_caches
  fi
  t1=$(now_ms)
  net_up "$id"
  vfs_up "$id"
  rm -f "$d/ch.sock" "$d/vsock.sock"
  setsid "$CH" --api-socket "$d/ch.sock" --restore "source_url=file://$s,memory_restore_mode=$mode" >>"$d/ch.log" 2>&1 < /dev/null &
  echo $! > "$d/ch.pid"
  for _ in $(seq 3000); do
    if api "$id" GET vm.info >/dev/null 2>&1; then break; fi
    kill -0 "$(cat "$d/ch.pid")" 2>/dev/null || { echo "restore: cloud-hypervisor exited" >&2; tail -5 "$d/ch.log" >&2; return 1; }
    sleep 0.005
  done
  t2=$(now_ms)
  api "$id" PUT vm.resume >/dev/null
  t3=$(now_ms)
  local ms; ms=$("$FB" wait "$d" 30s)
  t4=$(now_ms)
  local gt behind
  gt=$("$FB" exec "$d" root date +%s%3N)
  behind=$(( $(now_ms) - gt ))
  "$FB" settime "$d"
  if [ "${ENC:-0}" = 1 ] && [ "$mode" = copy ]; then
    enc_close "$id"
    rm -f "$d/frz.img" "/dev/shm/gf$id.key"
  fi
  echo "decrypt_ms=$((t1 - t0)) restore_ms=$((t2 - t1)) resume_ms=$((t3 - t2)) guestd_ms=$ms total_ms=$((t4 - t0)) clock_behind_ms=$behind"
}

down() {
  local id=$1 d=$ROOT/$1
  api "$id" PUT vmm.shutdown >/dev/null 2>&1 || true
  kill_pidfile "$d/ch.pid"
  kill_pidfile "$d/virtiofsd.pid"
  net_down "$id"
}

case "${1:-}" in
  up) up "$2" "${3:-large}" ;;
  freeze) freeze "$2" ;;
  thaw) thaw "$2" "${3:-copy}" ;;
  down) down "$2" ;;
  rm) down "$2"; rm -rf "${ROOT:?}/$2" ;;
  x) id=$2 user=$3; shift 3; "$FB" exec "$ROOT/$id" "$user" "$@" ;;
  # The same freeze and restore through hostd's own code (freeze.Image on a
  # thin volume of $VG, dm-crypt, Key check, fingerprint, ch.Freezer,
  # ch.RestoreArgs); the key is an RSA PEM ($KEY), records in $REC.
  hfreeze)
    id=$2; d=$ROOT/$2
    "$FB" hostd-freeze "$d" "$(cat "$d/class")" "${KEY:?}" "${REC:?}" "${VG:?}"
    kill_pidfile "$d/ch.pid"; kill_pidfile "$d/virtiofsd.pid"; net_down "$id" ;;
  hthaw)
    id=$2; d=$ROOT/$2
    net_up "$id"; vfs_up "$id"
    CH=$CH "$FB" hostd-thaw "$d" "${KEY:?}" "${REC:?}" "${VG:?}" ;;
  *) sed -n '2,20p' "$0"; exit 64 ;;
esac
