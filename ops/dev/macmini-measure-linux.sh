#!/bin/bash
# Runs on the Linux side: inside L1 (the Ubuntu VM on the Mac) or natively
# on an Asahi Linux Mac mini, where KVM is the real thing and the numbers
# are aarch64-without-nesting. Boots an Ubuntu arm64 cloud image as a
# Cloud Hypervisor guest five times, timing exec to the login prompt, then
# logs in over the serial console and runs the same openssl benchmark the
# host runs. Needs: curl, file, qemu-img, cloud-localds, expect, openssl,
# and `sudo` that takes the password admin on stdin or asks nothing
# (SUDO_PASS overrides). docs/research/mac-mini-nested-kvm.md §4 steps 3-4.
set -eu
SUDO_PASS=${SUDO_PASS:-admin}
sudo_() { echo "$SUDO_PASS" | sudo -S -p "" "$@"; }
cd "$HOME"; mkdir -p l2 results; cd l2
# tools, on Ubuntu (L1) or Fedora (Asahi)
if ! command -v cloud-localds >/dev/null || ! command -v qemu-img >/dev/null || ! command -v expect >/dev/null; then
  if command -v apt-get >/dev/null; then export DEBIAN_FRONTEND=noninteractive; sudo_ apt-get -qq update >/dev/null; sudo_ apt-get -qq install -y cpu-checker qemu-utils cloud-image-utils expect openssl file zstd python3 >/dev/null
  elif command -v dnf >/dev/null; then sudo_ dnf -q -y install qemu-img cloud-utils expect openssl file zstd python3 >/dev/null; fi
fi
{ echo "kernel $(uname -r)"; nproc; free -g; ls -l /dev/kvm; } > ../results/linux-facts.txt 2>&1
[ -e /dev/kvm ] || { echo "no /dev/kvm"; exit 1; }
# /dev/kvm is root:kvm 0660 and this user is not in kvm (Cloud Hypervisor:
# "CreateHypervisor(VmCreate(Permission denied))", 2026-11-19); a lab box.
[ -w /dev/kvm ] || sudo_ chmod a+rw /dev/kvm
CH=https://github.com/cloud-hypervisor/cloud-hypervisor/releases/latest/download
[ -x cloud-hypervisor ] || { curl -fsSL -o cloud-hypervisor $CH/cloud-hypervisor-static-aarch64; chmod +x cloud-hypervisor; }
[ -x ch-remote ]        || { curl -fsSL -o ch-remote $CH/ch-remote-static-aarch64; chmod +x ch-remote; }
./cloud-hypervisor --version | tee ../results/ch-version.txt
# the L1 kernel boots L2: arm64 vmlinuz is a gzip'd Image, CH wants it uncompressed
if [ ! -s Image ]; then
  k=/boot/vmlinuz-$(uname -r)
  # Cloud Hypervisor wants the uncompressed arm64 Image (magic "ARM\x64" at
  # byte 56). Ubuntu ships vmlinuz as an EFI "zboot" PE with the Image
  # compressed inside (header: "zimg" at 4, payload offset and size at 8
  # and 12, the compressor's name at 24); older releases ship a plain gzip.
  sudo_ cp "$k" vmlinuz.copy; sudo_ chown "$USER" vmlinuz.copy
  python3 - vmlinuz.copy Image <<'PYX'
import sys, gzip, lzma, subprocess
src, dst = sys.argv[1], sys.argv[2]
d = open(src, 'rb').read()
def decomp(payload, comp):
    if comp.startswith('gzip'): return gzip.decompress(payload)
    if comp.startswith('xz') or comp.startswith('lzma'): return lzma.decompress(payload)
    if comp.startswith('zstd'):
        return subprocess.run(['zstd', '-dc'], input=payload, capture_output=True, check=True).stdout
    raise SystemExit('unknown compressor ' + comp)
if d[56:60] == b'ARM\x64':
    out = d; how = 'already an Image'
elif d[4:8] == b'zimg':
    off = int.from_bytes(d[8:12], 'little'); size = int.from_bytes(d[12:16], 'little')
    comp = d[24:60].split(b'\0')[0].decode()
    out = decomp(d[off:off + size], comp); how = 'zboot ' + comp
elif d[:2] == b'\x1f\x8b':
    out = gzip.decompress(d); how = 'gzip'
else:
    raise SystemExit('vmlinuz is neither Image, zboot nor gzip')
assert out[56:60] == b'ARM\x64', 'extracted payload has no ARM64 magic'
open(dst, 'wb').write(out); print('kernel: %s, Image %d bytes' % (how, len(out)))
PYX
  rm -f vmlinuz.copy
  echo "Image magic $(od -A n -t x1 -j 56 -N 4 Image | tr -d ' ') (41524d64 is ARM64)" | tee -a ../results/linux-facts.txt
  sudo_ cp "/boot/initrd.img-$(uname -r)" initrd.img
  sudo_ chown "$USER" Image initrd.img
fi
[ -s root.raw ] || {
  curl -fsSL -o noble.img https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-arm64.img
  qemu-img convert -O raw noble.img root.raw && rm noble.img
  qemu-img resize -f raw root.raw 8G
}
# the benchmark runs from cloud-init and prints to the serial console, so
# no serial login is needed (the login dance lost two runs to echo races
# and to cloud-init setting the password late)
[ -s seed.img ] || {
  printf 'instance-id: l2-bench-%s\nlocal-hostname: l2\n' "$(date +%s)" > meta-data
  cat > user-data <<'UD'
#cloud-config
password: l2pass
chpasswd: { expire: false }
runcmd:
  - [ sh, -c, "openssl speed -seconds 5 sha256 2>&1 | tail -n 3 > /dev/ttyAMA0; echo OPENSSL_DONE > /dev/ttyAMA0" ]
UD
  cloud-localds seed.img user-data meta-data
}
run_l2() { # $1 = run number; prints seconds to login prompt and keeps the openssl output on the last run
  local n=$1
  local sock=/tmp/ch-$n.sock log=../results/l2-boot-$n.log
  rm -f "$sock"
  expect <<EXP > "$log" 2>&1
set timeout 180
set t0 [clock milliseconds]
spawn ./cloud-hypervisor --api-socket $sock --kernel Image --initramfs initrd.img \
  --disk path=root.raw,image_type=raw path=seed.img,image_type=raw \
  --cmdline "console=ttyAMA0 root=/dev/vda1 rw" \
  --cpus boot=2 --memory size=2048M --serial tty --console off
expect {
  -re "login:" { puts "BOOT_MS [expr [clock milliseconds] - \$t0]" }
  -re "OPENSSL_DONE" { puts "BOOT_MS [expr [clock milliseconds] - \$t0] (benchmark finished before the prompt)" }
}
set timeout 300
expect "OPENSSL_DONE"
EXP
  for p in $(pgrep -f "^\./cloud-hypervisor"); do kill -9 "$p"; done
  grep BOOT_MS "$log" | awk '{print $2}'
}
: > ../results/l2-boot-ms.txt
for n in $(seq 1 "${RUNS:-5}"); do
  ms=$(run_l2 $n); echo "$ms" | tee -a ../results/l2-boot-ms.txt
done
sed -n '/sha256/,/OPENSSL_DONE/p' "../results/l2-boot-${RUNS:-5}.log" > ../results/openssl-l2-1thread.txt
# one thread at every level is the like-for-like number; all threads shows the VM's ceiling
openssl speed -seconds 5 sha256 2>&1 | tail -n 3 > ../results/openssl-l1-1thread.txt
openssl speed -multi "$(nproc)" -seconds 5 sha256 2>&1 | tail -n 4 > ../results/openssl-l1-all.txt
echo "measure: done"
