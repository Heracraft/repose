#!/usr/bin/env bash
# The appliance's kernel: kernel.org 6.18.57 with Debian trixie's
# cloud-arm64 configuration and the few changes below (DECISIONS I-1494).
#
# Why not Debian's own package: trixie ships 6.12, which has no fix for
# CVE-2026-99999 (nix/hosts/kernel.nix asserts 6.18.57 or newer), and
# trixie-backports ships 7.3, and under Apple's nested virtualization
# every guest hangs two seconds into boot on 7.0 (docs/research/
# mac-mini-nested-kvm.md §7). 6.18.57 is the one version proven on the M4
# for both; a bump is proven with ops/dev/macmini-l2-try.sh first.
#
# Output: $OUT/kernel-<release>-arm64.tar.zst holding boot/vmlinuz.efi
# (an EFI zboot image systemd-boot loads), boot/config, boot/System.map
# and usr/lib/modules/<release>. Runs natively on arm64 or cross-compiles on
# x86_64 (gcc-aarch64-linux-gnu); zboot needs hexdump (bsdextrautils). build.sh unpacks the tarball into the
# root.
#
#   kernel.sh <out-dir>
set -euo pipefail

KVER=6.18.57
KSHA256=5bd9acdc82ec5efc3790ae840e5aaa40dfde7efff261cf6dde902dc570acf98c
# Debian's configuration, read from the package it ships in.
DEB=linux-image-6.12.117+deb13-cloud-arm64_6.12.117-1_arm64.deb
DEB_URL=https://deb.debian.org/debian/pool/main/l/linux-signed-arm64/$DEB
DEB_SHA256=cc33e288505c3271539beab498aeb4f25e27b8f7c0c49ddb6c506b6d3c50c22f
LOCALVERSION=-repose

out=${1:?usage: kernel.sh <out-dir>}
mkdir -p "$out"
out=$(cd "$out" && pwd)
work=${KERNEL_WORK:-$out/kernel-work}
mkdir -p "$work"
cd "$work"

fetch() { # url sha256 file
  if [ ! -s "$3" ] || ! echo "$2  $3" | sha256sum -c --quiet - 2>/dev/null; then
    curl -fsSL -o "$3.part" "$1"
    mv "$3.part" "$3"
  fi
  echo "$2  $3" | sha256sum -c --quiet -
}
fetch "https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-$KVER.tar.xz" "$KSHA256" "linux-$KVER.tar.xz"
fetch "$DEB_URL" "$DEB_SHA256" "$DEB"

rm -rf deb && mkdir deb
(cd deb && ar x "../$DEB" && tar -xf data.tar.* ./boot)
[ -d "linux-$KVER" ] || tar -xf "linux-$KVER.tar.xz"
cd "linux-$KVER"
cp ../deb/boot/config-* .config

make_args=(ARCH=arm64)
if [ "$(uname -m)" != aarch64 ]; then
  make_args+=(CROSS_COMPILE=aarch64-linux-gnu-)
fi

c=scripts/config
$c --set-str LOCALVERSION "$LOCALVERSION" --disable LOCALVERSION_AUTO
# systemd-boot loads a PE image; zboot is the compressed one.
$c --enable EFI_ZBOOT --enable EFI_STUB
# No initramfs: the disk, the console and the root filesystem are built
# in, so the root is found by PARTLABEL from the first instruction.
$c --enable VIRTIO --enable VIRTIO_PCI --enable VIRTIO_BLK --enable VIRTIO_CONSOLE \
   --enable EXT4_FS --enable VIRTIO_MMIO
# Debian signs modules with a key this build does not have, and its
# trusted-key lists name files in Debian's tree.
$c --disable MODULE_SIG --disable MODULE_SIG_ALL --set-str MODULE_SIG_KEY "" \
   --set-str SYSTEM_TRUSTED_KEYS "" --set-str SYSTEM_REVOCATION_KEYS ""
# No debug info: it is most of the build's time and of the modules' size.
$c --disable DEBUG_INFO --enable DEBUG_INFO_NONE --disable DEBUG_INFO_DWARF5 \
   --disable DEBUG_INFO_DWARF_TOOLCHAIN_DEFAULT --disable DEBUG_INFO_BTF
make "${make_args[@]}" olddefconfig >/dev/null

# The changes above must have taken: olddefconfig drops what it cannot
# satisfy without a word.
for want in EFI_ZBOOT=y VIRTIO_PCI=y VIRTIO_BLK=y VIRTIO_CONSOLE=y EXT4_FS=y KVM=y \
  VHOST_VSOCK=m VIRTIO_VSOCKETS=m DM_THIN_PROVISIONING=m DM_CRYPT=m BTRFS_FS=m WIREGUARD=m \
  NF_TABLES=m NFT_CONNLIMIT=m BRIDGE=m TUN=m EROFS_FS=m OVERLAY_FS=m VIRTIO_FS=y; do
  grep -qx "CONFIG_$want" .config || { echo "kernel.sh: CONFIG_$want did not take" >&2; exit 1; }
done

jobs=${KERNEL_JOBS:-$(nproc)}
make "${make_args[@]}" -j"$jobs" vmlinuz.efi modules
release=$(make "${make_args[@]}" -s kernelrelease)

stage=$work/stage
rm -rf "$stage" && mkdir -p "$stage/boot"
# /usr/lib/modules: Debian 13 has a merged /usr, and lib is a symlink.
make "${make_args[@]}" -s INSTALL_MOD_PATH="$stage/usr" INSTALL_MOD_STRIP=1 modules_install
rm -f "$stage/usr/lib/modules/$release/build" "$stage/usr/lib/modules/$release/source"
cp arch/arm64/boot/vmlinuz.efi "$stage/boot/vmlinuz.efi"
cp .config "$stage/boot/config"
cp System.map "$stage/boot/System.map"
tar -C "$stage" -cf - boot usr | zstd -q -19 -T0 -o "$out/kernel-$release-arm64.tar.zst" -f
echo "kernel.sh: $out/kernel-$release-arm64.tar.zst ($release)"
