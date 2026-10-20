#!/usr/bin/env bash
# A loopback btrfs store for internal/undo's integration test
# (docs/workstreams/SPINE.md, DECISIONS I-814). The test skips unless
# REPOSE_UNDO_TESTFS names a prepared btrfs mountpoint:
#
#   sudo ops/dev/undo-testfs.sh --up            make and mount it, print the path
#   REPOSE_UNDO_TESTFS=/mnt/nixstore/repose-undo-testfs go test ./internal/undo/...
#   sudo ops/dev/undo-testfs.sh --down          unmount, detach, remove the image
#
# SIZE_GB (default 20) and ROOT (default /mnt/nixstore) set the image. The
# root disk of the dev box has about 3 GB free, so the image goes on the
# data disk by default.
#
# The mount options are the installer's (SPINE.md contract 5):
# user_subvol_rm_allowed lets the login user delete its own points without
# sudo. The loop device is whatever losetup hands out — several sessions
# share this box, so a fixed /dev/loop0 and a sweeping `losetup -D` both
# take someone else's device with them.
set -uo pipefail

root=${ROOT:-/mnt/nixstore}
size_gb=${SIZE_GB:-20}
img=$root/repose-undo-testfs.img
mnt=$root/repose-undo-testfs

usage() { echo "usage: undo-testfs.sh --up | --down" >&2; exit 2; }

if [ "$(id -u)" != 0 ]; then
  echo "undo-testfs: needs root (mkfs, mount, losetup)" >&2
  exit 1
fi

# loopdev prints the loop device backed by $img, empty when there is none.
loopdev() { losetup -j "$img" -nO NAME 2>/dev/null | head -1; }

case "${1:-}" in
--up)
  if mountpoint -q "$mnt"; then
    echo "$mnt"
    exit 0
  fi
  mkdir -p "$mnt"
  if [ ! -f "$img" ]; then
    truncate -s "${size_gb}G" "$img"
    mkfs.btrfs -q -L repose-undo "$img" || exit 1
  fi
  dev=$(loopdev)
  if [ -z "$dev" ]; then
    dev=$(losetup --find --show "$img") || exit 1
  fi
  mount -o noatime,compress=zstd:1,user_subvol_rm_allowed "$dev" "$mnt" || exit 1
  # The test provisions the layout itself, as the agent does, and it runs
  # as the invoking user: a root-owned mountpoint refuses `btrfs
  # subvolume create` to anyone else.
  chown "${SUDO_UID:-0}:${SUDO_GID:-0}" "$mnt"
  echo "$mnt"
  ;;
--down)
  if mountpoint -q "$mnt"; then
    # Only an open file holds the mount; a test that left one running is
    # worth naming rather than forcing.
    umount "$mnt" || { echo "undo-testfs: $mnt is busy" >&2; exit 1; }
  fi
  dev=$(loopdev)
  if [ -n "$dev" ]; then
    losetup -d "$dev" || exit 1
  fi
  rm -f "$img"
  rmdir "$mnt" 2>/dev/null
  ;;
*) usage ;;
esac
