#!/usr/bin/env bash
# Before-and-after evidence for the guest pool coming back across a reboot
# (workstream 17; DECISIONS I-643, I-644). Runs on a staging host, driven
# from the dev box:
#
#   scp ops/dev/staging-pool-repro.sh root@host:/root/ && ssh root@host \
#     /root/staging-pool-repro.sh show | tee ~/repose-ws/logs/<case>.txt
#
#   state     make a guest-shaped thin volume with a file in it
#   show      what the pool, the device maps and the dependent units are
#   leftover  the incident's shape: pool inactive, its components active
#   corrupt   damaged pool metadata, so thin_check fails for real
#   rebuild   an empty healthy pool again, the way disko makes it
#
# Reboots are not in here: `az vm restart`, `systemctl reboot` or a
# power-off/on from outside, then `show` again.
#
# It refuses to run anywhere but a staging host, because `leftover`
# deactivates the volume group every tenant volume lives in.
set -uo pipefail

case "$(hostname)" in
  host-stg-*) ;;
  *) echo "staging-pool-repro: refusing to run on $(hostname); staging hosts only" >&2; exit 1 ;;
esac

vg=vg-guests
lv=g-repro
mnt=/mnt/repro

case "${1:-show}" in
state)
  set -x
  lvcreate -V 8G -T "$vg/thin" -n "$lv"
  mkfs.ext4 -q "/dev/$vg/$lv"
  mkdir -p "$mnt"
  mount "/dev/$vg/$lv" "$mnt"
  date -Is > "$mnt/written-before-reboot"
  sync
  umount "$mnt"
  # Not auth-*: hostd's login-share sweep removes an auth- volume with no
  # user directory as an orphan, by design, within a few minutes and without
  # a log line on success (internal/hostd/guest/claudeauth.go, DECISIONS
  # I-278). A test volume named that way vanishes mid-exercise.
  lvcreate -V 1G -T "$vg/thin" -n g-repro-aux
  ;;

leftover)
  set -x
  # The caches hold repose-cache open, so the group will not deactivate
  # while they are up. Stopping the volume unit stops the two that want it
  # but leaves the mount: the unit has no ExecStop, which is deliberate, so
  # the umount is ours.
  systemctl stop docker-registry repose-npm-cache repose-cache-volume
  umount /var/cache/repose
  vgchange -an "$vg"
  # tmeta active with the pool inactive is what the incident left behind:
  # LVM refuses the pool while a component of it is active, and nothing
  # retries. --yes answers "activate component LV in read-only mode?",
  # which it asks because a pool's metadata volume is hidden.
  lvchange -ay --yes "$vg/thin_tmeta"
  lvchange -ay "$vg/thin" || echo "expected: activating the pool failed"
  ;;

corrupt)
  # A genuinely damaged pool: thin_check must then fail, and the fix must
  # leave the pool alone and say so rather than repair it (the plan's
  # "a failing check is never repaired"). Destroys the pool's contents;
  # `rebuild` puts an empty one back.
  set -x
  systemctl stop docker-registry repose-npm-cache repose-cache-volume
  umount /var/cache/repose
  vgchange -an "$vg"
  # LVM activates a pool's metadata volume read-only however you ask, so
  # the write goes through a second device-mapper device over the same
  # extents, which is writable.
  lvchange -ay --yes "$vg/thin_tmeta"
  dmsetup table vg--guests-thin_tmeta > /tmp/tmeta.table
  dmsetup create stg-tmeta-rw --table "$(cat /tmp/tmeta.table)"
  dd if=/dev/urandom of=/dev/mapper/stg-tmeta-rw bs=1M count=8 conv=fsync
  udevadm settle
  dmsetup remove --retry stg-tmeta-rw
  lvchange -an "$vg/thin_tmeta"
  lvchange -ay "$vg/thin" || echo "expected: thin_check refused the pool"
  ;;

rebuild)
  # Back to a healthy empty pool, the way disko makes it.
  set -x
  systemctl stop docker-registry repose-npm-cache repose-cache-volume
  umount /var/cache/repose 2>/dev/null || true
  vgchange -an "$vg" || true
  lvremove -y "$vg" || true
  lvcreate --yes -l 95%FREE -n thin --type=thin-pool \
    --poolmetadatasize 1024m --discards passdown --zero y "$vg"
  systemctl start repose-cache-volume
  lvs "$vg"
  ;;

show)
  for c in \
    "uname -r" \
    "uptime" \
    "vgs -o vg_name,pv_count,vg_size,autoactivation --noheadings $vg" \
    "lvs -o lv_name,lv_attr,lv_active,lv_health_status,data_percent,metadata_percent $vg" \
    "dmsetup ls --tree" \
    "ls -l /dev/$vg/" \
    "lvs -o lv_name,seg_monitor --noheadings $vg" \
    "systemctl is-active repose-guest-pool repose-cache-volume repose-npm-cache docker-registry hostd" \
    "systemctl is-active lvm2-monitor dm-event.service dm-event.socket" \
    "command -v lvm" \
    "systemctl --failed --no-legend --plain" \
    "grep -c . /var/lib/node_exporter/textfile/repose_lvm.prom" \
    "cat /var/lib/node_exporter/textfile/repose_lvm.prom" \
    "journalctl -b -u 'lvm-activate-*' --no-pager -o cat" \
    "journalctl -b -g 'thin_check|Manual repair|repair required|vg-guests' --no-pager -o short-monotonic" \
  ; do
    printf '\n=== %s\n' "$c"
    eval "$c" 2>&1 | head -60
  done
  printf '\n=== file written before the reboot\n'
  if mkdir -p "$mnt" && mount "/dev/$vg/$lv" "$mnt" 2>/dev/null; then
    cat "$mnt/written-before-reboot" 2>&1
    umount "$mnt"
  else
    echo "could not mount /dev/$vg/$lv"
  fi
  ;;

*)
  echo "usage: staging-pool-repro.sh [state|show|leftover|corrupt|rebuild]" >&2
  exit 2
  ;;
esac
