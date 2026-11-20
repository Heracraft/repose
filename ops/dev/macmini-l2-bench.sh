#!/bin/bash
# One guest boot inside L1 for the nesting-tax number, with no console login:
# cloud-init runs the one-thread openssl sha256 benchmark, writes it to
# /bench.txt on the guest's disk and powers the guest off; the file is then
# read by mounting the disk image. Prints BOOT_MS (login prompt) and the
# benchmark lines.
set -u
cd "$HOME/l2" || exit 1
for p in $(pgrep -f '^\./cloud-hypervisor'); do kill -9 "$p"; done; sleep 2; rm -f /tmp/ch-bench.sock
printf 'instance-id: l2-bench-%s\nlocal-hostname: l2\n' "$(date +%s)" > meta-data
cat > user-data <<'UD'
#cloud-config
runcmd:
  - [ sh, -c, "{ uname -r; nproc; openssl speed -seconds 5 sha256; } > /bench.txt 2>&1; sync; poweroff" ]
UD
rm -f seed-bench.img; cloud-localds seed-bench.img user-data meta-data
expect <<'EXP' 2>&1 | tee "$HOME/results/l2-bench.log" | grep -aE '^(BOOT_MS|BENCH_)'
set timeout 300
set t0 [clock milliseconds]
spawn ./cloud-hypervisor --api-socket /tmp/ch-bench.sock --kernel Image --initramfs initrd.img --disk path=root.raw,image_type=raw path=seed-bench.img,image_type=raw --cmdline "console=ttyAMA0 root=/dev/vda1 rw" --cpus boot=2 --memory size=2048M --serial tty --console off
expect {
  -re "login:" { puts "BOOT_MS [expr [clock milliseconds] - $t0]" }
  -re "reboot: Power down" { puts "BENCH_POWEROFF_BEFORE_PROMPT" }
}
expect {
  -re "reboot: Power down" { puts "BENCH_POWEROFF at [expr [clock milliseconds] - $t0] ms" }
  eof { puts "BENCH_EOF" }
  timeout { puts "BENCH_TIMEOUT" }
}
EXP
for p in $(pgrep -f '^\./cloud-hypervisor'); do kill -9 "$p"; done; sleep 1
# read /bench.txt from the first partition of the raw image
SUDO_PASS=${SUDO_PASS:-admin}
sudo_() { echo "$SUDO_PASS" | sudo -S -p "" "$@"; }
dev=$(sudo_ losetup --find --show -P root.raw) && sleep 1 && mkdir -p /tmp/l2root \
  && sudo_ mount -o ro "${dev}p1" /tmp/l2root && sudo_ cat /tmp/l2root/bench.txt | tee "$HOME/results/openssl-l2-1thread.txt"
sudo_ umount /tmp/l2root 2>/dev/null; sudo_ losetup -d "$dev" 2>/dev/null; true
