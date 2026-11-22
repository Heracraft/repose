#!/usr/bin/env bash
# Copy one guest release (amd64) from the builder's output to the staging
# edge server under its version, with the D-11 names and a regenerated
# SHA256SUMS, and check the host can read it.
#   stage-guest.sh 2026.11.22.6
set -euo pipefail
v=${1:?version}
src=/mnt/nixstore/tmp/nonix-guest/out/$v/amd64
S="-F /home/azureuser/repose-ws/staging-ssh-config"
dst=/var/lib/repose-staging/guest/guest-$v
for f in vmlinuz initramfs.img rootfs.erofs manifest.json; do test -s "$src/$f" || { echo "missing $src/$f" >&2; exit 1; }; done
# The modes sshd's StrictModes and a multi-user guest need, read from the
# artifact itself (2026.11.22.5 and .6 shipped /etc 0775).
# erofs-mode.py reads the raw inode mode: dump.erofs masks Access to 0777
# and cannot show a sticky or setuid bit.
bad=0
while read -r p got own; do
	case $p in
	/tmp|/var/tmp) want=1777 ;;
	/usr/bin/sudo) want=4755 ;;
	*) want=0755 ;;
	esac
	echo "mode $p $got owner $own (want $want 0:0)"
	[ "$got" = "$want" ] && [ "$own" = "0:0" ] || bad=1
done < <(python3 /mnt/nixstore/tmp/stg-cp/erofs-mode.py "$src/rootfs.erofs" / /etc /etc/ssh /usr /tmp /var/tmp /usr/bin/sudo)
[ $bad = 0 ] || { [ "${FORCE:-0}" = 1 ] || { echo "refusing to stage: wrong modes (FORCE=1 to stage anyway)" >&2; exit 1; }; }
ssh $S stg-edge "mkdir -p $dst"
for f in vmlinuz initramfs.img manifest.json rootfs.erofs; do scp $S -q "$src/$f" "stg-edge:$dst/amd64-$f"; done
ssh $S stg-edge "cd $dst && chmod 644 * && sha256sum amd64-vmlinuz amd64-initramfs.img amd64-rootfs.erofs amd64-manifest.json > SHA256SUMS && cat SHA256SUMS"
echo "-- builder's own SHA256SUMS:"; cat "$src/SHA256SUMS"
ssh $S stg-host-wg "curl -sI -m 10 http://10.200.2.4:8082/guest-$v/SHA256SUMS | head -1; curl -sI -m 10 http://10.200.2.4:8082/guest-$v/amd64-rootfs.erofs | grep -i '^HTTP\|length'"
