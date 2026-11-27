#!/bin/bash
# verbs_test.sh: every verb of repose-vm's control socket and its hybrid
# vsock, against a real VM on a Mac (DECISIONS I-1664, I-1682). Run it as
# the logged-in user, in an unlocked session (a save needs both, I-1662):
#
#   IMG=<dir with vmlinuz, initramfs.img, rootfs.erofs> VOL=<ext4 volume> \
#     cmd/repose-vm/verbs_test.sh [path/to/repose-vm]
#
# VOL is copied (an APFS clone) and never written. Prints one line a check
# and exits 1 if any failed.
set -u
VM=${1:-./repose-vm}
: "${IMG:?IMG names the guest base directory}" "${VOL:?VOL names a guest volume}"
X=$(mktemp -d "${TMPDIR:-/tmp}/rvm.XXXXXX")
trap 'pkill -f "api-socket $X/" 2>/dev/null; rm -rf "$X"' EXIT
fails=0
check() { # name want got
  if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: want $2, got $3"; fails=$((fails + 1)); fi
}
has() { # name needle haystack
  case "$3" in *"$2"*) echo "ok   $1" ;; *) echo "FAIL $1: no \"$2\" in \"$3\""; fails=$((fails + 1)) ;; esac
}
api() { # method verb [json] -> "code body"
  local b
  b=$(curl -s --max-time 30 --unix-socket "$1" -X "$2" "http://localhost/api/v1/$3" ${4:+-H 'Content-Type: application/json' -d "$4"} -w ' %{http_code}')
  echo "${b##* } ${b% *}"
}
code() { echo "${1%% *}"; }
waitsock() { for _ in $(seq 1 300); do [ -S "$1" ] && return 0; sleep 0.1; done; return 1; }
gone() { for _ in $(seq 1 100); do pgrep -f "api-socket $1" >/dev/null || return 0; sleep 0.1; done; return 1; }

cp -c "$VOL" "$X/vol.img"
S=$X/a/ch.sock
mkdir -p "$X/a"
( "$VM" --api-socket "$S" --kernel "$IMG/vmlinuz" --initramfs "$IMG/initramfs.img" --cmdline "console=hvc0 panic=-1" \
    --cpus boot=2 --memory size=2048M \
    --disk "path=$IMG/rootfs.erofs,image_type=raw,readonly=on,id=vda" --disk "path=$X/vol.img,image_type=raw,id=vdb" \
    --net tap=t0,mac=52:54:00:aa:bb:10 --vsock "cid=3,socket=$X/a/vsock.sock" --serial "socket=$X/a/console.sock" \
    --park-dir "$X/park" > "$X/a.log" 2>&1 < /dev/null & )
waitsock "$S" || { echo "FAIL boot: no API socket"; cat "$X/a.log"; exit 1; }

r=$(api "$S" GET vmm.ping); check "GET vmm.ping" 200 "$(code "$r")"
for _ in $(seq 1 50); do r=$(api "$S" GET vm.info); case "$r" in *'"state":"Running"'*) break ;; esac; sleep 0.1; done
check "GET vm.info" 200 "$(code "$r")"; has "vm.info running" '"state":"Running"' "$r"
r=$(api "$S" PUT vm.resize-disk '{"desired_size":1}'); check "PUT vm.resize-disk refused" 500 "$(code "$r")"
# hostd gives a Mac VM no balloon (VZ's has no free page reporting), and
# a VM saved with one did not restore ("invalid argument", lab Mac
# 2026-11-27), so the resize is refused here.
r=$(api "$S" PUT vm.resize '{"desired_balloon":536870912}'); check "PUT vm.resize without a balloon refused" 500 "$(code "$r")"
r=$(api "$S" PUT vm.resize '{}'); check "PUT vm.resize without desired_balloon" 400 "$(code "$r")"
r=$(api "$S" PUT vm.net-reconnect); check "PUT vm.net-reconnect without a switch port" 500 "$(code "$r")"
r=$(api "$S" PUT vm.nonsense); check "an unknown verb" 404 "$(code "$r")"
r=$(api "$S" PUT vm.snapshot "{\"destination_url\":\"file://$X/s1\"}"); check "PUT vm.snapshot of a running VM refused" 500 "$(code "$r")"
r=$(api "$S" PUT vm.resume); check "PUT vm.resume of a running VM refused" 500 "$(code "$r")"

# The hybrid vsock: CONNECT to guestd's port once the guest is up.
ok=no
for _ in $(seq 1 60); do
  a=$(printf 'CONNECT 5000\n' | nc -U -w 2 "$X/a/vsock.sock" 2>/dev/null | head -c 64 | head -1)
  case "$a" in OK*) ok=yes; break ;; esac
  sleep 1
done
check "vsock CONNECT 5000 answers OK" yes "$ok"
a=$(printf 'HELLO\n' | nc -U -w 2 "$X/a/vsock.sock" 2>/dev/null | head -c 64)
check "vsock without CONNECT is closed" "" "$a"

r=$(api "$S" PUT vm.pause); check "PUT vm.pause" 204 "$(code "$r")"
r=$(api "$S" GET vm.info); has "vm.info paused" '"state":"Paused"' "$r"
r=$(api "$S" PUT vm.pause); check "PUT vm.pause twice refused" 500 "$(code "$r")"
r=$(api "$S" PUT vm.snapshot '{"destination_url":"http://x/"}'); check "PUT vm.snapshot to a non-file URL" 400 "$(code "$r")"
mkdir -p "$X/s1"
r=$(api "$S" PUT vm.snapshot "{\"destination_url\":\"file://$X/s1\"}"); check "PUT vm.snapshot of a paused VM" 204 "$(code "$r")"
check "the save and its config" yes "$([ -s "$X/s1/vz.state" ] && [ -s "$X/s1/repose-vm.json" ] && echo yes || echo no)"
r=$(api "$S" PUT vm.resume); check "PUT vm.resume" 204 "$(code "$r")"
r=$(api "$S" PUT vm.shutdown); check "PUT vm.shutdown" 204 "$(code "$r")"
r=$(api "$S" GET vm.info); has "vm.info after vm.shutdown" '"state":"Shutdown"' "$r"
r=$(api "$S" PUT vmm.shutdown); check "PUT vmm.shutdown" 204 "$(code "$r")"
gone "$S" && ok=yes || ok=no
check "vmm.shutdown ends the process" yes "$ok"

# A restore comes up paused, as Cloud Hypervisor's does.
mkdir -p "$X/b"
S=$X/b/ch.sock
( "$VM" --api-socket "$S" --restore "source_url=file://$X/s1,memory_restore_mode=copy" > "$X/b.log" 2>&1 < /dev/null & )
waitsock "$S" || { echo "FAIL restore: no API socket"; cat "$X/b.log"; exit 1; }
r=$(api "$S" GET vm.info); has "restored VM is paused" '"state":"Paused"' "$r"
r=$(api "$S" PUT vm.resume); check "PUT vm.resume after the restore" 204 "$(code "$r")"
r=$(api "$S" GET vm.info); has "restored VM runs" '"state":"Running"' "$r"
r=$(api "$S" PUT vmm.shutdown); check "PUT vmm.shutdown" 204 "$(code "$r")"
gone "$S" && ok=yes || ok=no
check "vmm.shutdown ends the process" yes "$ok"

# SIGTERM with --park-dir pauses and saves before the process exits (a
# launcher upgrade, a park repose starts).
mkdir -p "$X/c"
S=$X/c/ch.sock
( "$VM" --api-socket "$S" --restore "source_url=file://$X/s1,memory_restore_mode=copy" --park-dir "$X/park" > "$X/c.log" 2>&1 < /dev/null & )
waitsock "$S" && api "$S" PUT vm.resume > /dev/null
pkill -TERM -f "api-socket $S"
gone "$S" && ok=yes || ok=no
check "SIGTERM ends the process" yes "$ok"
has "SIGTERM parks a running VM" "event=parked" "$(cat "$X/c.log")"
check "the park's save" yes "$([ -s "$X/park/vz.state" ] && echo yes || echo no)"

if [ "$fails" -gt 0 ]; then echo "$fails failed"; exit 1; fi
echo "all passed"
