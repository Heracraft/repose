#!/bin/bash
# One Cloud Hypervisor boot of the lab guest inside L1, for experiments:
#   macmini-l2-try.sh <vcpus> [extra kernel cmdline]
# Prints BOOT_MS <ms> on a login prompt, RCU_STALL on a stall, TIMEOUT
# after 240 s, then kills the guest. Expects ~/l2 as macmini-measure-linux.sh
# leaves it (Image, initrd.img, root.raw, seed.img, cloud-hypervisor).
set -u
cpus=${1:-2}; extra=${2:-}
cd "$HOME/l2" || exit 1
for p in $(pgrep -f '^\./cloud-hypervisor'); do kill -9 "$p"; done; sleep 2; rm -f /tmp/ch-try.sock
exp=$(mktemp)
cat > "$exp" <<EXP
set timeout 240
set t0 [clock milliseconds]
spawn ./cloud-hypervisor --api-socket /tmp/ch-try.sock --kernel Image --initramfs initrd.img \
  --disk path=root.raw,image_type=raw path=seed.img,image_type=raw \
  --cmdline "console=ttyAMA0 root=/dev/vda1 rw $extra" \
  --cpus boot=$cpus --memory size=2048M --serial tty --console off
expect {
  -re {login:} { puts "BOOT_MS [expr [clock milliseconds] - \$t0] cpus=$cpus extra={$extra}" }
  -re {rcu: INFO} { puts "RCU_STALL after [expr [clock milliseconds] - \$t0] ms cpus=$cpus extra={$extra}" }
  timeout { puts "TIMEOUT cpus=$cpus extra={$extra}" }
}
EXP
expect -f "$exp" 2>&1 | tee "$HOME/results/l2-try-${cpus}.log" | grep -aE '^(BOOT_MS|RCU_STALL|TIMEOUT)'
rm -f "$exp"; for p in $(pgrep -f '^\./cloud-hypervisor'); do kill -9 "$p"; done; true
