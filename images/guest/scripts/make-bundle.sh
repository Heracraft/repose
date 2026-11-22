#!/usr/bin/env bash
# Builds the kernel and initramfs half of the guest bundle from the kernel
# package installed in the image (images/guest/Dockerfile, bundle-build
# stage), so /lib/modules in rootfs.erofs is the kernel's own
# (proposal §2.3 #5).
#
# usage: make-bundle.sh <init script> <out dir>
# writes <out>/vmlinuz, <out>/initramfs.img, <out>/kernel-version
set -euo pipefail

init=$1
out=$2
mkdir -p "$out"

kv=$(ls /lib/modules)
[ "$(printf '%s\n' "$kv" | wc -l)" = 1 ] || { echo "make-bundle: more than one kernel in /lib/modules: $kv" >&2; exit 1; }
printf '%s\n' "$kv" > "$out/kernel-version"

# The kernel. amd64: the bzImage as Ubuntu ships it. arm64: Cloud
# Hypervisor boots only a plain Image; Ubuntu's 6.8 vmlinuz is that Image
# gzipped, and a later one is an EFI zboot PE ("MZ", then "zimg" at offset
# 4) whose payload is the Image, at the offset and size the header names,
# compressed with the method named at 0x18.
k=/boot/vmlinuz-$kv
# bytes FILE OFFSET COUNT: those bytes as text, NULs dropped.
bytes() { dd if="$1" bs=1 skip="$2" count="$3" 2>/dev/null | tr -d '\0'; }
magic=$(bytes "$k" 4 4)
if [ "$(od -An -tx1 -N2 "$k" | tr -d ' ')" = 1f8b ]; then
  # Ubuntu's arm64 vmlinuz up to 6.8: the plain Image, gzip-compressed.
  gzip -dc "$k" > "$out/vmlinuz"
  [ "$(bytes "$out/vmlinuz" 56 4)" = ARMd ] \
    || { echo "make-bundle: the gunzipped kernel has no arm64 ARMd magic" >&2; exit 1; }
elif [ "$magic" = zimg ]; then
  le32() { od -An -tu4 -j "$1" -N4 "$k" | tr -d ' '; }
  off=$(le32 8)
  size=$(le32 12)
  comp=$(bytes "$k" 24 8)
  case "$comp" in
    gzip) dec="gzip -dc" ;;
    zstd) dec="zstd -dc" ;;
    lz4) dec="lz4 -dc" ;;
    xzkern) dec="xz -dc" ;;
    *) echo "make-bundle: zboot payload compressed with '$comp', not handled" >&2; exit 1 ;;
  esac
  tail -c +"$((off + 1))" "$k" | head -c "$size" | $dec > "$out/vmlinuz"
  # A plain arm64 Image has "ARMd" at offset 56.
  [ "$(bytes "$out/vmlinuz" 56 4)" = ARMd ] \
    || { echo "make-bundle: the extracted arm64 Image has no ARMd magic" >&2; exit 1; }
elif [ "$(bytes "$k" 56 4)" = ARMd ]; then
  cp "$k" "$out/vmlinuz"
elif [ "$(bytes "$k" 514 4)" = HdrS ]; then
  cp "$k" "$out/vmlinuz"
else
  echo "make-bundle: $k is neither a bzImage, an arm64 Image nor a zboot image" >&2
  exit 1
fi

# The initramfs: static BusyBox, the init script, and the modules it needs
# that the kernel does not build in, decompressed (BusyBox's insmod does
# not read .ko.zst) and listed in load order in /modules.
root=$(mktemp -d)
mkdir -p "$root"/{bin,dev,proc,sys,mnt/ro,mnt/rw,sysroot,lib/modules}
cp /bin/busybox "$root/bin/busybox"
for applet in sh mount umount mkdir rm mv cp cat echo sleep ls insmod switch_root \
  chown chmod tr cut head tail grep sed test "[" mountpoint stat find touch poweroff reboot; do
  ln -s busybox "$root/bin/$applet"
done
install -m 0755 "$init" "$root/init"

: > "$root/modules"
for m in virtio_pci virtio_blk erofs ext4 overlay; do
  if grep -qE "/$m\.ko" "/lib/modules/$kv/modules.builtin"; then
    echo "make-bundle: $m is built in"
    continue
  fi
  while read -r kind path _; do
    [ "$kind" = insmod ] || continue
    name=$(basename "$path"); name=${name%.zst}; name=${name%.xz}
    [ -e "$root/lib/modules/$name" ] && continue
    case "$path" in
      *.zst) zstd -qdc "$path" > "$root/lib/modules/$name" ;;
      *.xz) xz -dc "$path" > "$root/lib/modules/$name" ;;
      *) cp "$path" "$root/lib/modules/$name" ;;
    esac
    echo "$name" >> "$root/modules"
    echo "make-bundle: $name (for $m)"
  done < <(modprobe -S "$kv" --show-depends "$m")
done

(cd "$root" && find . -print0 | cpio --null -o -H newc --quiet) | gzip -9 > "$out/initramfs.img"
rm -rf "$root"
