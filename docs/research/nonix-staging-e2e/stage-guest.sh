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
EU=/nix/store/r80xfvq46alb3fqbka1v0c0h1mphlah4-erofs-utils-1.9.4
[ -x $EU/bin/dump.erofs ] || EU=$(nix build --no-link --print-out-paths nixpkgs#erofs-utils | tail -1)
bad=0
for pm in /:0755 /etc:0755 /etc/ssh:0755 /usr:0755 /tmp:1777; do
	p=${pm%%:*} want=${pm##*:}
	got=$($EU/bin/dump.erofs --path="$p" "$src/rootfs.erofs" 2>&1 | sed -n 's/.*Access: \([0-7]*\)\/.*/\1/p')
	echo "mode $p $got (want $want)"
	[ "$got" = "$want" ] || bad=1
done
[ $bad = 0 ] || { [ "${FORCE:-0}" = 1 ] || { echo "refusing to stage: wrong modes (FORCE=1 to stage anyway)" >&2; exit 1; }; }
ssh $S stg-edge "mkdir -p $dst"
for f in vmlinuz initramfs.img manifest.json rootfs.erofs; do scp $S -q "$src/$f" "stg-edge:$dst/amd64-$f"; done
ssh $S stg-edge "cd $dst && chmod 644 * && sha256sum amd64-vmlinuz amd64-initramfs.img amd64-rootfs.erofs amd64-manifest.json > SHA256SUMS && cat SHA256SUMS"
echo "-- builder's own SHA256SUMS:"; cat "$src/SHA256SUMS"
ssh $S stg-host-wg "curl -sI -m 10 http://10.200.2.4:8082/guest-$v/SHA256SUMS | head -1; curl -sI -m 10 http://10.200.2.4:8082/guest-$v/amd64-rootfs.erofs | grep -i '^HTTP\|length'"
