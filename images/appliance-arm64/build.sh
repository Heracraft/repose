#!/bin/bash
# Build the Mac mini appliance image (DECISIONS I-1494): Debian 13
# (trixie) arm64, made with mmdebstrap, written into a raw GPT disk with no
# loop device and no root on the machine that builds it:
#
#   1  esp     64 MiB  vfat: systemd-boot and the kernel (vmlinuz.efi)
#   2  root     2 GiB  ext4 (mkfs.ext4 -d): the system; nothing writes it
#                      but the system's own state under /var
#   3  guests   2 MiB  empty: at the first boot it takes the rest of the
#                      disk the launcher grew the file to and becomes
#                      vg-guests' PV (repose-guest-vg-init, then
#                      repose-mac-grow at every boot, as nix/hosts/mac.nix)
#
# There is no initramfs: the kernel (kernel.sh) has the disk, the console
# and ext4 built in and finds the root by its partition GUID.
#
# Runs as root in a Debian trixie container (CI: an arm64 runner; this
# box: an amd64 container, arm64 code through binfmt). Needs git, mmdebstrap
# (and arch-test on a foreign architecture),
# e2fsprogs, dosfstools, mtools, gdisk, zstd, curl, jq.
#
#   build.sh --version V --kernel kernel-*.tar.zst --hostd hostd-arm64
#            --hostd-commit SHA --out DIR
#            [--ch DIR]    a directory holding cloud-hypervisor-static-aarch64
#                          and ch-remote-static-aarch64 (default: fetched, pinned)
#            [--allow-branch]  build from a tree that is not main's head
#
# The image ships what main ships: the tree must be a clean checkout of
# main's head, and --hostd-commit, the commit the hostd binary was built
# from, must be that commit. appliance-2026.11.22's first upload carried a
# branch's hostd without main's split-parts fetch (I-1504) and no first
# create on a Mac could start. The commit is written to
# /etc/repose/hostd-version and printed.
#
# Output: DIR/repose-appliance-aarch64.raw.zst and its .sha256.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
top=$(cd "$here/../.." && pwd)
version="" kernel="" hostd="" hostd_commit="" out="" chdir="" allow_branch=0
while [ $# -gt 0 ]; do
  case $1 in
    --version) version=${2:?}; shift 2 ;;
    --kernel) kernel=${2:?}; shift 2 ;;
    --hostd) hostd=${2:?}; shift 2 ;;
    --out) out=${2:?}; shift 2 ;;
    --ch) chdir=${2:?}; shift 2 ;;
    --hostd-commit) hostd_commit=${2:?}; shift 2 ;;
    --allow-branch) allow_branch=1; shift ;;
    *) echo "build.sh: unknown argument $1" >&2; exit 2 ;;
  esac
done
if [ -z "$version" ] || [ ! -f "$kernel" ] || [ ! -f "$hostd" ] || [ -z "$hostd_commit" ] || [ -z "$out" ]; then
  sed -n '/^#   build.sh/,/^# Output/p' "$0" >&2
  exit 2
fi
mkdir -p "$out"
out=$(cd "$out" && pwd)

git() { command git -c safe.directory='*' -C "$top" "$@"; }
head=$(git rev-parse HEAD)
main=$(git rev-parse --verify -q refs/heads/main || git rev-parse --verify -q refs/remotes/origin/main || true)
if [ "$allow_branch" = 0 ]; then
  if [ "$head" != "$main" ]; then
    echo "build.sh: the tree is at $head, not main's head ${main:-(no main ref)}; build from main, or pass --allow-branch" >&2
    exit 1
  fi
  if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
    echo "build.sh: the tree has uncommitted changes; build from a clean checkout of main, or pass --allow-branch" >&2
    exit 1
  fi
  case "$head" in "$hostd_commit"*) ;; *)
    echo "build.sh: hostd was built from $hostd_commit, the tree is at $head; build hostd from this tree, or pass --allow-branch" >&2
    exit 1 ;;
  esac
fi
echo "build.sh: tree $head, hostd built from $hostd_commit, main ${main:-unknown}"

# Cloud Hypervisor: the hosts' version, the upstream static build.
CH_VERSION=53.0
CH_SHA256=f192b510eea1c710cbc439d716bb0573c223fc463dbe3e6523788a2b7ef62850
CHR_SHA256=ade26617f74264467e1381f146fd1face6b8b0fb13c5ec84f4acedd72f972596
# The three partitions' GUIDs are fixed, so the kernel command line and
# fstab can name them on every image.
ESP_GUID=7a1e0c4b-5e2f-4c1d-9b3a-1d6f0e2c8a01
ROOT_GUID=7a1e0c4b-5e2f-4c1d-9b3a-1d6f0e2c8a02
GUESTS_GUID=7a1e0c4b-5e2f-4c1d-9b3a-1d6f0e2c8a03
DISK_GUID=7a1e0c4b-5e2f-4c1d-9b3a-1d6f0e2c8a00
MIB=$((1024 * 1024))
ESP_MIB=64
ROOT_MIB=2048
GUESTS_MIB=1

work=$(mktemp -d "${TMPDIR:-/tmp}/repose-appliance.XXXXXX")
trap 'rm -rf "$work"' EXIT
root=$work/root
started=$(date +%s)

packages=(
  systemd systemd-sysv systemd-resolved systemd-timesyncd udev dbus kmod
  iproute2 nftables wireguard-tools lvm2 thin-provisioning-tools
  util-linux e2fsprogs cloud-guest-utils fdisk cryptsetup-bin btrfs-progs
  virtiofsd zstd curl ca-certificates jq openssl systemd-boot-efi
)
# No documentation, manuals or translations: nobody reads them here.
mmdebstrap --variant=minbase --architectures=arm64 --include="$(IFS=,; echo "${packages[*]}")" \
  --dpkgopt='path-exclude=/usr/share/doc/*' --dpkgopt='path-include=/usr/share/doc/*/copyright' \
  --dpkgopt='path-exclude=/usr/share/man/*' --dpkgopt='path-exclude=/usr/share/locale/*' \
  --dpkgopt='path-exclude=/usr/share/info/*' \
  trixie "$root" http://deb.debian.org/debian

# ---- the kernel ------------------------------------------------------
zstd -dc "$kernel" | tar -C "$root" -xf - --keep-directory-symlink
release=$(ls "$root/usr/lib/modules")
chroot "$root" depmod -a "$release"

# ---- repose ------------------------------------------------------------
lib=$root/usr/local/lib/repose
install -d "$lib" "$root/usr/local/bin" "$root/etc/repose"
install -m 0755 "$here"/host/bin/* "$lib/"
install -m 0755 "$hostd" "$root/usr/local/bin/hostd"
if [ -z "$chdir" ]; then
  chdir=$work/ch && mkdir -p "$chdir"
  base=https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/v$CH_VERSION
  curl -fsSL -o "$chdir/cloud-hypervisor-static-aarch64" "$base/cloud-hypervisor-static-aarch64"
  curl -fsSL -o "$chdir/ch-remote-static-aarch64" "$base/ch-remote-static-aarch64"
fi
echo "$CH_SHA256  $chdir/cloud-hypervisor-static-aarch64" | sha256sum -c --quiet -
echo "$CHR_SHA256  $chdir/ch-remote-static-aarch64" | sha256sum -c --quiet -
install -m 0755 "$chdir/cloud-hypervisor-static-aarch64" "$root/usr/local/bin/cloud-hypervisor"
install -m 0755 "$chdir/ch-remote-static-aarch64" "$root/usr/local/bin/ch-remote"
# hostd and systemd-run name it bare; Debian keeps it off PATH. The
# Claude login share's unit passes --translate-uid (I-278), which
# virtiofsd 1.10 lacks and trixie's 1.13.2 has. An image whose
# virtiofsd lacks it would boot every guest without the share (I-1540).
ln -sf /usr/libexec/virtiofsd "$root/usr/local/bin/virtiofsd"
if ! chroot "$root" /usr/libexec/virtiofsd --help 2>&1 | grep -q -- '--translate-uid'; then
  echo "build.sh: the image's virtiofsd has no --translate-uid" >&2
  exit 1
fi

cp -r "$here"/host/etc/. "$root/etc/"
cp -r "$here"/mac/etc/. "$root/etc/"
install -d "$root/etc/systemd/system"
cp -r "$here"/host/systemd/. "$root/etc/systemd/system/"
cp -r "$here"/mac/systemd/. "$root/etc/systemd/system/"
printf 'vmw_vsock_virtio_transport\nbtrfs\n' > "$root/etc/modules-load.d/repose-mac.conf"
# The release key hostd updates are signed with, and the platform host CA
# the api's certificate chains to: public material, read from the files
# that already carry it (install.sh, nix/hosts/platform.nix).
sed -n "/^RELEASE_PUBKEY='/,/END PUBLIC KEY/p" "$top/install.sh" |
  sed "s/^RELEASE_PUBKEY='//; s/'\$//" > "$root/etc/repose/release.pub"
sed -n '/BEGIN CERTIFICATE/,/END CERTIFICATE/p' "$top/nix/hosts/platform.nix" |
  sed 's/^[[:space:]]*//' > "$root/etc/repose/api-ca.pem"
grep -q 'END PUBLIC KEY' "$root/etc/repose/release.pub"
grep -q 'END CERTIFICATE' "$root/etc/repose/api-ca.pem"
printf '%s\n' "$version" > "$root/etc/repose/appliance-version"
printf '%s %s\n' "$("$root/usr/local/bin/hostd" version 2>/dev/null | awk '{ print $2 }' || true)" "$hostd_commit" > "$root/etc/repose/hostd-version"

# ---- the system ---------------------------------------------------------
echo repose-appliance > "$root/etc/hostname"
: > "$root/etc/machine-id"
chroot "$root" passwd -l root >/dev/null
# The launcher writes the console to console.log on the Mac: the journal
# goes there, ids and states only.
install -d "$root/etc/systemd/journald.conf.d"
printf '[Journal]\nForwardToConsole=yes\nTTYPath=/dev/hvc0\nMaxLevelConsole=info\nSystemMaxUse=200M\n' \
  > "$root/etc/systemd/journald.conf.d/10-repose.conf"
install -d "$root/etc/systemd/system/systemd-networkd-wait-online.service.d"
printf '[Service]\nExecStart=\nExecStart=/usr/lib/systemd/systemd-networkd-wait-online --any\n' \
  > "$root/etc/systemd/system/systemd-networkd-wait-online.service.d/10-any.conf"
sed -i "s/PARTLABEL=root /PARTUUID=$ROOT_GUID /; s/PARTLABEL=esp /PARTUUID=$ESP_GUID /" "$root/etc/fstab"
ln -sf ../run/systemd/resolve/stub-resolv.conf "$root/etc/resolv.conf"
chroot "$root" systemctl enable systemd-networkd.service systemd-resolved.service systemd-timesyncd.service \
  hostd.service repose-guests-slice.service repose-guest-pool.service repose-host-net.service \
  repose-launcher-join.service repose-register.service repose-guest-park.service \
  repose-snapshot.timer repose-hostd-update.timer repose-fault-around.service repose-nftables.service \
  repose-guest-vg-init.service repose-mac-grow.service repose-mac-trim.service repose-images-volume.service \
  fstrim.timer >/dev/null
chroot "$root" systemctl disable nftables.service >/dev/null 2>&1 || true
# hostd's image cache is never on the 2 GiB root (I-1494): the volume's
# unit is enabled, mounts the cache directory, and hostd waits for it.
units=$root/etc/systemd/system
if ! { [ -L "$units/multi-user.target.wants/repose-images-volume.service" ] &&
  grep -q '^dir=/var/lib/repose/images$' "$root/usr/local/lib/repose/repose-images-volume" &&
  grep -q '^After=.*repose-images-volume.service' "$units/hostd.service" &&
  grep -q '^Wants=.*repose-images-volume.service' "$units/hostd.service" &&
  grep -q -- '--images-dir /var/lib/repose/images' "$units/hostd.service" &&
  grep -q '^After=.*repose-images-volume.service.*blk-availability.service' "$units/guest@.service.d/10-repose.conf" &&
  grep -q '^RequiresMountsFor=/var/lib/repose/images$' "$units/guest@.service.d/10-repose.conf"; }; then
  echo "build.sh: hostd's --images-dir would be on the root partition, or a shutdown would take it from under a running guest: repose-images-volume is not enabled, not mounting /var/lib/repose/images, not before hostd, or not after every guest@ (I-1550)" >&2
  exit 1
fi
rm -rf "$root"/var/lib/apt/lists/* "$root"/var/cache/apt/* "$root"/var/log/*.log "$root"/tmp/*

# ---- the ESP --------------------------------------------------------------
esp=$work/esp
install -d "$esp/EFI/BOOT" "$esp/loader/entries"
cp "$root/usr/lib/systemd/boot/efi/systemd-bootaa64.efi" "$esp/EFI/BOOT/BOOTAA64.EFI"
cp "$root/boot/vmlinuz.efi" "$esp/vmlinuz.efi"
printf 'default repose.conf\ntimeout 0\n' > "$esp/loader/loader.conf"
# The kernel parameters nix/hosts/kernel.nix sets on every host, the
# framework's virtio console, and the root by GUID.
cat > "$esp/loader/entries/repose.conf" <<ENTRY
title repose appliance $version
linux /vmlinuz.efi
options root=PARTUUID=$ROOT_GUID rootfstype=ext4 rootwait rw console=hvc0 transparent_hugepage=madvise hashdist=0 panic=30
ENTRY
rm -f "$root/boot/vmlinuz.efi"

# ---- the disk ---------------------------------------------------------------
mkfs.vfat -C -n ESP "$work/esp.img" $((ESP_MIB * 1024)) >/dev/null
mcopy -s -i "$work/esp.img" "$esp"/* ::
truncate -s $((ROOT_MIB * MIB)) "$work/root.img"
mkfs.ext4 -q -L root -d "$root" -E lazy_itable_init=0,lazy_journal_init=0 "$work/root.img"
disk=$work/repose-appliance-aarch64.raw
truncate -s $(( (1 + ESP_MIB + ROOT_MIB + GUESTS_MIB + 1) * MIB )) "$disk"
# sgdisk's -q writes nothing and says nothing (2026-11-22: an image with
# no partition table at all), and -U wants a table that exists: the
# table first, its GUID second, and both read back.
sgdisk -o \
  -n 1:1M:+${ESP_MIB}M -t 1:ef00 -c 1:esp -u 1:"$ESP_GUID" \
  -n 2:0:+${ROOT_MIB}M -t 2:8305 -c 2:root -u 2:"$ROOT_GUID" \
  -n 3:0:0 -t 3:8e00 -c 3:guests -u 3:"$GUESTS_GUID" "$disk" >/dev/null
sgdisk -U "$DISK_GUID" "$disk" >/dev/null
for n in 1 2 3; do
  guid=$(case $n in 1) echo "$ESP_GUID" ;; 2) echo "$ROOT_GUID" ;; 3) echo "$GUESTS_GUID" ;; esac)
  sgdisk -i "$n" "$disk" | grep -qi "unique GUID: $guid" || { echo "build.sh: partition $n is not in the table" >&2; exit 1; }
done
dd if="$work/esp.img" of="$disk" bs=1M seek=1 conv=notrunc,sparse status=none
dd if="$work/root.img" of="$disk" bs=1M seek=$((1 + ESP_MIB)) conv=notrunc,sparse status=none

apparent=$(stat -c %s "$disk")
zstd -q -19 -T0 -f "$disk" -o "$out/repose-appliance-aarch64.raw.zst"
(cd "$out" && sha256sum repose-appliance-aarch64.raw.zst > repose-appliance-aarch64.raw.zst.sha256)
compressed=$(stat -c %s "$out/repose-appliance-aarch64.raw.zst")
used=$(du -B1 "$disk" | awk '{ print $1 }')
echo "build.sh: version=$version kernel=$release hostd_commit=$hostd_commit apparent_bytes=$apparent allocated_bytes=$used compressed_bytes=$compressed seconds=$(( $(date +%s) - started ))"
