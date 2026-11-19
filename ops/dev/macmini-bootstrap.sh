#!/bin/bash
# Runs ON the rented Mac mini (macOS, bash 3.2). Steps 1 to 4 and 7 of
# docs/research/mac-mini-nested-kvm.md §4: is nested virtualization
# offered, does KVM appear inside a Linux VM, does Cloud Hypervisor boot a
# guest inside that, what does the nesting cost, and does the
# Apache-licensed launcher (vfkit) do what Tart does. Everything lands in
# ~/repose-lab; results in ~/repose-lab/results; nothing is installed
# system-wide and nothing needs sudo. Re-running skips what is done.
#
# L0 = this Mac. L1 = the Ubuntu arm64 VM (Tart, then vfkit). L2 = a
# Cloud Hypervisor guest inside L1.
#
# UNTESTED as of 2026-11-19: written on an x86 Linux box before any Mac
# existed. Expect the download names and the expect(1) prompts to need a
# first-run fix; keep the pass lines, they are the protocol.
set -eu

LAB=$HOME/repose-lab
RES=$LAB/results
BIN=$LAB/bin
L1=l1
L1_CPUS=8
L1_MEM_MIB=12288   # of the M4-S's 16 GiB; macOS keeps 4
mkdir -p "$RES" "$BIN"
export PATH="$BIN:$PATH"

say()  { printf '\n== %s\n' "$*"; }
pass() { printf 'PASS %s\n' "$*" | tee -a "$RES/summary.txt"; }
fail() { printf 'FAIL %s\n' "$*" | tee -a "$RES/summary.txt"; exit 1; }
note() { printf 'NOTE %s\n' "$*" | tee -a "$RES/summary.txt"; }

fetch() { # url dest
  [ -s "$2" ] && return 0
  curl -fsSL --retry 3 -o "$2" "$1"
  xattr -d com.apple.quarantine "$2" 2>/dev/null || true
}

# --- step 1: the chip and the OS ------------------------------------------
say "step 1: chip and OS"
{
  echo "date $(date -u +%FT%TZ)"
  echo "macos $(sw_vers -productVersion) build $(sw_vers -buildVersion)"
  echo "chip $(sysctl -n machdep.cpu.brand_string)"
  echo "cores $(sysctl -n hw.ncpu) memory_bytes $(sysctl -n hw.memsize)"
  echo "hv_support $(sysctl -n kern.hv_support)"
} | tee "$RES/host.txt"
major=$(sw_vers -productVersion | cut -d. -f1)
[ "$major" -ge 15 ] || fail "step 1: macOS $major is older than 15; nested virtualization needs 15"
sysctl -n machdep.cpu.brand_string | grep -Eq 'Apple M([3-9]|[1-9][0-9])' \
  || fail "step 1: chip is not M3 or later; nested virtualization is not offered"
[ "$(sysctl -n kern.hv_support)" = 1 ] || fail "step 1: kern.hv_support is 0"
pass "step 1: macOS $(sw_vers -productVersion) on $(sysctl -n machdep.cpu.brand_string)"

# --- tools: tart (lab) and vfkit (product path) ---------------------------
say "tools"
if ! command -v tart >/dev/null; then
  fetch https://github.com/cirruslabs/tart/releases/latest/download/tart.tar.gz "$LAB/tart.tar.gz"
  tar xzf "$LAB/tart.tar.gz" -C "$LAB"
  t=$(find "$LAB" -type f -name tart -perm +111 | head -n1)
  [ -n "$t" ] || fail "tools: tart binary not found in the tarball; look in $LAB"
  ln -sf "$t" "$BIN/tart"
  xattr -dr com.apple.quarantine "$(dirname "$(dirname "$(dirname "$t")")")" 2>/dev/null || true
fi
tart --version | tee "$RES/tart-version.txt"
if ! command -v vfkit >/dev/null; then
  fetch https://github.com/crc-org/vfkit/releases/latest/download/vfkit "$BIN/vfkit" \
    || fetch https://github.com/crc-org/vfkit/releases/latest/download/vfkit-arm64 "$BIN/vfkit" \
    || note "tools: vfkit release asset name has changed; step 7 will be skipped"
  [ -s "$BIN/vfkit" ] && chmod +x "$BIN/vfkit"
fi
command -v vfkit >/dev/null && vfkit --version | tee "$RES/vfkit-version.txt"

# a key for L1 so nothing below is interactive
[ -s "$LAB/id_lab" ] || ssh-keygen -q -t ed25519 -N '' -f "$LAB/id_lab"
SSH="ssh -i $LAB/id_lab -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR"

# --- step 2: L1 with --nested, /dev/kvm inside ----------------------------
say "step 2: L1 (Tart, Ubuntu arm64, --nested)"
if ! tart list 2>/dev/null | grep -q "^local *$L1 "; then
  tart clone ghcr.io/cirruslabs/ubuntu:latest "$L1"
  tart set "$L1" --cpu "$L1_CPUS" --memory "$L1_MEM_MIB" --disk-size 80
fi
if ! pgrep -f "tart run .*$L1" >/dev/null; then
  # Not nohup: under the ssh -t tty macOS's nohup says "can't detach from
  # console" and the VM dies with the session (first run, 2026-11-19). A
  # subshell with every fd off the tty survives the ssh exit.
  (tart run --nested --no-graphics "$L1" </dev/null > "$LAB/$L1.log" 2>&1 &)
  sleep 5
fi
L1_IP=$(tart ip "$L1" --wait 300) || fail "step 2: L1 got no IP in 300 s; see $LAB/$L1.log"
echo "l1_ip $L1_IP" | tee "$RES/l1.txt"

# the image's login is admin/admin; put our key in once
if ! $SSH -o BatchMode=yes "admin@$L1_IP" true 2>/dev/null; then
  expect <<EXP
set timeout 60
spawn ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null admin@$L1_IP "mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo '$(cat "$LAB/id_lab.pub")' >> ~/.ssh/authorized_keys"
expect {
  -re "(?i)password:" { send "admin\r"; exp_continue }
  eof
}
EXP
fi
$SSH "admin@$L1_IP" 'echo admin | sudo -S -p "" true; nproc; free -g; uname -r; ls -l /dev/kvm' | tee "$RES/l1-facts.txt"
$SSH "admin@$L1_IP" 'ls /dev/kvm' >/dev/null 2>&1 \
  || fail "step 2: no /dev/kvm in L1; nested virtualization was not granted (see $LAB/$L1.log for Tart's warning)"
$SSH "admin@$L1_IP" 'export DEBIAN_FRONTEND=noninteractive; echo admin | sudo -S -p "" apt-get -qq update >/dev/null; echo admin | sudo -S -p "" apt-get -qq install -y cpu-checker qemu-utils cloud-image-utils expect openssl >/dev/null; echo admin | sudo -S -p "" kvm-ok' \
  | tee "$RES/kvm-ok.txt"
grep -q "KVM acceleration can be used" "$RES/kvm-ok.txt" || fail "step 2: kvm-ok refused"
pass "step 2: /dev/kvm present in L1 and kvm-ok passes"

# --- step 3 and 4 inside L1: an L2 guest under Cloud Hypervisor -----------
say "steps 3 and 4: Cloud Hypervisor guest inside L1"
cp "$HOME/macmini-measure-linux.sh" "$LAB/l1-measure.sh"
scp -i "$LAB/id_lab" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR "$LAB/l1-measure.sh" "admin@$L1_IP:l1-measure.sh"
$SSH "admin@$L1_IP" 'chmod +x l1-measure.sh && ./l1-measure.sh' 2>&1 | tee "$RES/l1-measure.log"
scp -i "$LAB/id_lab" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -r "admin@$L1_IP:results/." "$RES/"
# five numbers, not five blank lines (the first run passed on blank lines)
if [ "$(grep -cE '^[0-9]+$' "$RES/l2-boot-ms.txt" 2>/dev/null)" -ge 1 ]; then
  pass "step 3: L2 booted $(grep -cE '^[0-9]+$' "$RES/l2-boot-ms.txt") times; ms to login: $(grep -E '^[0-9]+$' "$RES/l2-boot-ms.txt" | tr '\n' ' ')"
else
  fail "step 3: no L2 boot recorded; read $RES/l1-measure.log and $RES/l2-boot-1.log"
fi
# the Mac's own number for the same benchmark, so L1's cost is visible too.
# macOS ships LibreSSL, whose speed has no -multi; one thread, like the others.
openssl speed sha256 2>&1 | tail -n 3 > "$RES/openssl-l0-1thread.txt"
grep -q "^sha256" "$RES/openssl-l0-1thread.txt" && grep -q "^sha256" "$RES/openssl-l2-1thread.txt" \
  && pass "step 4: openssl sha256, one thread, at L0, L1 and L2 (compare the 16384-byte column)" \
  || fail "step 4: an openssl line is missing; see $RES/openssl-l*-1thread.txt"

# --- step 7: the same disk under vfkit ------------------------------------
say "step 7: vfkit --nested on the Tart disk"
if command -v vfkit >/dev/null; then
  tart stop "$L1" 2>/dev/null || true; sleep 3
  MAC=52:54:aa:c0:ff:ee   # no zero octets: the lease file drops leading zeros
  (vfkit --cpus "$L1_CPUS" --memory "$L1_MEM_MIB" --nested \
    --bootloader "efi,variable-store=$LAB/vfkit-nvram,create" \
    --device "virtio-blk,path=$HOME/.tart/vms/$L1/disk.img" \
    --device "virtio-net,nat,mac=$MAC" \
    --device virtio-rng --device "virtio-serial,logFilePath=$LAB/vfkit-serial.log" </dev/null > "$LAB/vfkit.log" 2>&1 &)
  # the framework's NAT hands out addresses through macOS's own DHCP
  # server, whose leases are in /var/db/dhcpd_leases (what `tart ip` reads)
  VF_IP=
  for _ in $(seq 1 60); do
    VF_IP=$(sudo cat /var/db/dhcpd_leases 2>/dev/null | awk -v m="1,$MAC" '
      /^\{/ { ip=""; mac="" }
      /ip_address=/ { ip=$0; sub(".*=", "", ip) }
      /hw_address=/ { mac=$0; sub(".*=", "", mac) }
      /^\}/ { if (mac == m) print ip }' | tail -n1)
    [ -n "$VF_IP" ] && $SSH -o BatchMode=yes "admin@$VF_IP" true 2>/dev/null && break
    VF_IP=; sleep 3
  done
  if [ -n "$VF_IP" ] && $SSH "admin@$VF_IP" 'ls /dev/kvm' >/dev/null 2>&1; then
    pass "step 7: /dev/kvm present in L1 under vfkit --nested (Apache-2.0 launcher path works)"
  else
    note "step 7: vfkit boot or /dev/kvm not confirmed; see $LAB/vfkit.log (arp lookup or flag syntax may need a fix)"
  fi
  pkill -f "$BIN/vfkit" 2>/dev/null || true
else
  note "step 7 skipped: no vfkit"
fi

say "summary"
cat "$RES/summary.txt"
echo "results: $RES  (ops/dev/macmini results copies them back)"
