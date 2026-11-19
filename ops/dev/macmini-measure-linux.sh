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
  if command -v apt-get >/dev/null; then export DEBIAN_FRONTEND=noninteractive; sudo_ apt-get -qq update >/dev/null; sudo_ apt-get -qq install -y cpu-checker qemu-utils cloud-image-utils expect openssl file >/dev/null
  elif command -v dnf >/dev/null; then sudo_ dnf -q -y install qemu-img cloud-utils expect openssl file >/dev/null; fi
fi
{ echo "kernel $(uname -r)"; nproc; free -g; ls -l /dev/kvm; } > ../results/linux-facts.txt 2>&1
[ -e /dev/kvm ] || { echo "no /dev/kvm"; exit 1; }
CH=https://github.com/cloud-hypervisor/cloud-hypervisor/releases/latest/download
[ -x cloud-hypervisor ] || { curl -fsSL -o cloud-hypervisor $CH/cloud-hypervisor-static-aarch64; chmod +x cloud-hypervisor; }
[ -x ch-remote ]        || { curl -fsSL -o ch-remote $CH/ch-remote-static-aarch64; chmod +x ch-remote; }
./cloud-hypervisor --version | tee ../results/ch-version.txt
# the L1 kernel boots L2: arm64 vmlinuz is a gzip'd Image, CH wants it uncompressed
if [ ! -s Image ]; then
  k=/boot/vmlinuz-$(uname -r)
  if file -L "$k" | grep -q gzip; then sudo_ zcat "$k" > Image; else sudo_ cp "$k" Image; fi
  sudo_ cp "/boot/initrd.img-$(uname -r)" initrd.img
  sudo_ chown "$USER" Image initrd.img
fi
[ -s root.raw ] || {
  curl -fsSL -o noble.img https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-arm64.img
  qemu-img convert -O raw noble.img root.raw && rm noble.img
  qemu-img resize -f raw root.raw 8G
}
[ -s seed.img ] || {
  printf 'instance-id: l2\nlocal-hostname: l2\n' > meta-data
  printf '#cloud-config\npassword: l2pass\nchpasswd: { expire: false }\nssh_pwauth: true\n' > user-data
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
  --disk path=root.raw path=seed.img \
  --cmdline "console=ttyAMA0 root=/dev/vda1 rw" \
  --cpus boot=2 --memory size=2048M --serial tty --console off
expect -re "login:" { puts "BOOT_MS [expr [clock milliseconds] - \$t0]" }
send "ubuntu\r"
expect -re "(?i)password:"
send "l2pass\r"
expect -re "\\\\$ $"
send "openssl speed -multi 2 -seconds 5 sha256 2>&1 | tail -n 4; echo OPENSSL_DONE\r"
expect "OPENSSL_DONE"
send "sudo poweroff\r"
expect eof
EXP
  grep BOOT_MS "$log" | awk '{print $2}'
}
: > ../results/l2-boot-ms.txt
for n in 1 2 3 4 5; do
  ms=$(run_l2 $n); echo "$ms" | tee -a ../results/l2-boot-ms.txt
done
sed -n '/sha256/,/OPENSSL_DONE/p' ../results/l2-boot-5.log > ../results/openssl-l2.txt
openssl speed -multi 2 -seconds 5 sha256 2>&1 | tail -n 4 > ../results/openssl-l1-2threads.txt
openssl speed -multi "$(nproc)" -seconds 5 sha256 2>&1 | tail -n 4 > ../results/openssl-l1-all.txt
echo "measure: done"
