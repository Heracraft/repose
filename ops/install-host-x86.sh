#!/usr/bin/env bash
# Make this Linux x86_64 machine a repose host (DECISIONS I-1498): hostd
# without Nix, Cloud Hypervisor, the guest pool, the guest isolation
# ruleset and the units, the same as the Mac appliance's
# (images/appliance-arm64/), then register it with the join argument
# `repose host add` printed. Served as https://repose.run/install-host.sh:
#
#   printf '%s\n' <argument> > repose-join && curl -fsSL https://repose.run/install-host.sh | sudo bash -s -- --join-file repose-join
#   curl -fsSL https://repose.run/install-host.sh | sudo bash -s -- --uninstall
#
# Ubuntu 24.04 or Debian 12 and later, x86_64, systemd, /dev/kvm. Run as
# root. Every step is printed before it runs; a second run changes only
# what differs and re-registers nothing.
#
#   --join-file PATH       a file holding the argument `repose host add`
#                          printed; deleted once the install has read it
#   --join ARGUMENT        the argument itself, which sudo's log then keeps
#   --device DEV           a whole empty block device for the guests
#   --loop-file PATH       a file on this machine's disk for the guests
#                          instead (the default: /var/lib/repose/guests.img)
#   --size GIB             the file's size (default: 100, or the free space
#                          less 20, never under 20)
#   --api HOST:PORT        the api's gRPC address (default: the control
#                          plane's hub address, 10.255.255.1:8443)
#   --api-server-name NAME, --api-ca FILE   the api's TLS name and CA,
#                          with --api (default: the platform's)
#   --uplink IFACE         the interface guests reach the internet through
#                          (default: the one the default route uses)
#   --hostd-binary PATH    install this hostd instead of the one the
#                          newest hostd release names
#   --manifest-url URL     a hostd manifest to install and update from,
#                          instead of the newest hostd-<version> release's
#   --uninstall            take repose off this machine: hostd, the guests
#                          and their pool (the loop file, or --device's
#                          disk left with no signature), wg0, the nftables
#                          tables, the units, binaries and state. Refused
#                          while the host is still on the account
#   --force                with --uninstall, do it although it is
#   --dry-run              print every step and change nothing
set -euo pipefail

# Cloud Hypervisor: the hosts' version, the upstream static build.
CH_VERSION=53.0
CH_SHA256=448af3d4e59b22c2987f7df94c213ad40fb53a10d437e42b5ee6c4fce7c29ecc
CHR_SHA256=13f32ba952e6791fd901f2279be2055fbacc64005f96c42a8e90d58860df84a7
CH_BASE=https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/v$CH_VERSION
# virtiofsd: the version the NixOS hosts run, upstream's static musl
# build from its release (DECISIONS I-1540). The distributions' are too
# old or absent: Ubuntu 24.04's 1.10.0 has no --translate-uid, which the
# Claude login share needs (I-278), and Debian 12 ships none.
VIRTIOFSD_VERSION=1.14.0
VIRTIOFSD_ZIP=https://gitlab.com/-/project/21523468/uploads/f505704014ae7a816e515f2a05a93d8b/virtiofsd-v1.14.0.zip
VIRTIOFSD_ZIP_SHA256=2e4fe9571f492b00baa34bc4e708e950039c5da05b830b31a8d179cb6ac8978e
VIRTIOFSD_SHA256=15b2e72a78cc08a9bd8a6943e89fb69c88cb3cbeb63069efceade835342ac7d4
# The api over the hub (DECISIONS I-1382), as the Mac appliance dials it:
# the control plane's WireGuard address, its internal name and the
# platform host CA (nix/hosts/platform.nix). Public material.
API_ADDR=10.255.255.1:8443
API_SERVER_NAME=api.repose.herakraft.co
API_CA='-----BEGIN CERTIFICATE-----
MIIBYzCCAQmgAwIBAgIBATAKBggqhkjOPQQDAjAZMRcwFQYDVQQDEw5yZXBvc2Ug
aG9zdCBjYTAeFw0yNjA5MjAxNDM1NDRaFw0zNjA5MTcxNTM1NDRaMBkxFzAVBgNV
BAMTDnJlcG9zZSBob3N0IGNhMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEEf0Z
a+ShTl8C17TyIihGfxGA8/+LPSztZO7U+1jkulN/0PyP+u0mFtoz1No7eNIsLQdI
4XqvAoE1SQ5NJMb9+6NCMEAwDgYDVR0PAQH/BAQDAgKEMA8GA1UdEwEB/wQFMAMB
Af8wHQYDVR0OBBYEFJ3mh/uToG0kXZreInW+yRCV7Vv0MAoGCCqGSM49BAMCA0gA
MEUCIQD4CrivKkoYjKlkHwnK7Ors2lohLv4cFQ+yUU5IhKmIUgIgRiWHt/xsWPzy
pgFYxi/RgOzA9GSronavaaOscYi+Vng=
-----END CERTIFICATE-----'
# The release key (install.sh RELEASE_PUBKEY, DECISIONS I-430): the hostd
# manifest is signed with its private half.
RELEASE_PUBKEY='-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEq1BgpU75F46qQ3iMHL1E0VOqye+4
3/u1m873IxGrYRM2AZyYkK6QqN92G2qBuMvUi+qb4OYeF7KRF1MQ5oiIdw==
-----END PUBLIC KEY-----'

join="" join_file="" device="" loop_file="" size="" uplink=""
api="" api_name="" api_ca_file="" hostd_binary="" manifest_url="" dry=0
doc_dir=/usr/share/doc
uninstall=0 force=0
while [ $# -gt 0 ]; do
  case $1 in
    --join) join=${2:?--join needs the argument}; shift 2 ;;
    --join-file) join_file=${2:?--join-file needs a path}; shift 2 ;;
    --device) device=${2:?--device needs a block device}; shift 2 ;;
    --loop-file) loop_file=${2:?--loop-file needs a path}; shift 2 ;;
    --size) size=${2:?--size needs a number of GiB}; shift 2 ;;
    --api) api=${2:?--api needs HOST:PORT}; shift 2 ;;
    --api-server-name) api_name=${2:?}; shift 2 ;;
    --api-ca) api_ca_file=${2:?}; shift 2 ;;
    --uplink) uplink=${2:?}; shift 2 ;;
    --hostd-binary) hostd_binary=${2:?}; shift 2 ;;
    --manifest-url) manifest_url=${2:?}; shift 2 ;;
    --dry-run) dry=1; shift ;;
    --uninstall) uninstall=1; shift ;;
    --force) force=1; shift ;;
    -h | --help) sed -n '2,/^set -euo/p' "$0" | sed '$d; s/^# \{0,1\}//'; exit 0 ;;
    *) echo "install-host: unknown argument $1" >&2; exit 2 ;;
  esac
done

die() { echo "install-host: $*" >&2; exit 1; }
say() { echo "install-host: $*"; }
# step DESCRIPTION -- COMMAND...: printed, then run unless --dry-run.
step() {
  local what=$1; shift
  [ "${1:-}" = -- ] && shift
  if [ "$dry" = 1 ]; then
    echo "would: $what"
    return 0
  fi
  echo "==> $what"
  "$@"
}

# ---- kernel check (ops/install-host-x86-test sources this block) ---------
# kernel_fix_status KVER OSID: prints fixed, unfixed or unknown: whether
# this kernel carries the fix for CVE-2026-99999
# (docs/security/cve-2026-99999.md, DECISIONS I-778, I-1588, I-1600).
#
# First the version, which needs nothing but the version:
# - a 6.18 from 6.18.57 on, the first 6.18 with the backport of
#   6785011f8b16;
# - Ubuntu's generic and lowlatency kernels, whose ABI number in `uname -r`
#   only grows within a series: 6.8.0 has it from ABI 149 and not below
#   (6.8.0-147 lacks it), the HWE 7.0.0 from ABI 43. Other Ubuntu flavours
#   (azure, aws, ...) number their ABIs their own way (7.0.0-1014-azure
#   lacks it, 7.0.0-1020-azure has it), so they fall through;
# - Debian's 6.12, from package version 6.12.113-1.
# Then the package changelog, which is how any other distribution kernel
# that took the backport says so: the CVE or the fix's subject in the
# changelog of the modules or unsigned image package, read from
# $doc_dir and else from `apt-get changelog`, which needs the apt lists
# (the installer fills them before it asks). A full changelog without
# either is unfixed. The signed image package's changelog may be only a
# version bump, so it can show the fix and never its absence. Nothing
# readable is unknown, which the installer says as "could not check".
kernel_fix_status() {
  local kver=$1 osid=$2 v maj rest min pat abi series pkg f log pver full_read=0
  local fix='validate checksum_start before completing checksum'
  v=${kver%%-*}
  maj=${v%%.*} rest=${v#*.}
  min=${rest%%.*}
  pat=0
  [ "$rest" != "$min" ] && pat=${rest#*.}
  pat=${pat%%[!0-9]*}
  if [ "$maj" = 6 ] && [ "$min" = 18 ] && [ "${pat:-0}" -ge 57 ]; then
    echo fixed; return
  fi
  if [ "$osid" = ubuntu ] && [[ $kver =~ ^(6\.8\.0|7\.0\.0)-([0-9]{1,3})-(generic|lowlatency)$ ]]; then
    abi=${BASH_REMATCH[2]} series=${BASH_REMATCH[1]}
    case $series in
      6.8.0) if [ "$abi" -ge 149 ]; then echo fixed; else echo unfixed; fi; return ;;
      7.0.0) if [ "$abi" -ge 43 ]; then echo fixed; return; fi ;;
    esac
  fi
  if [ "$osid" = debian ] && [ "$maj.$min" = 6.12 ] && command -v dpkg-query >/dev/null; then
    pver=$(dpkg-query -W -f='${Version}' "linux-image-$kver" 2>/dev/null || true)
    if [ -n "$pver" ]; then
      if dpkg --compare-versions "$pver" ge 6.12.113-1; then echo fixed; else echo unfixed; fi
      return
    fi
  fi
  for pkg in "linux-modules-$kver" "linux-image-unsigned-$kver" "linux-image-$kver-unsigned" "linux-image-$kver"; do
    for f in "$doc_dir/$pkg/changelog.Debian.gz" "$doc_dir/$pkg/changelog.gz"; do
      [ -r "$f" ] || continue
      if zgrep -q -F -e CVE-2026-99999 -e "$fix" "$f" 2>/dev/null; then
        echo fixed; return
      fi
      [ "$pkg" = "linux-image-$kver" ] || full_read=1
    done
  done
  if command -v apt-get >/dev/null; then
    for pkg in "linux-modules-$kver" "linux-image-unsigned-$kver" "linux-image-$kver-unsigned" "linux-image-$kver"; do
      log=$(timeout 60 apt-get changelog -qq "$pkg" 2>/dev/null || true)
      [ -n "$log" ] || continue
      if grep -q -F -e CVE-2026-99999 -e "$fix" <<< "$log"; then
        echo fixed; return
      fi
      [ "$pkg" = "linux-image-$kver" ] || full_read=1
    done
  fi
  if [ "$full_read" = 1 ]; then echo unfixed; else echo unknown; fi
}

# kernel_note KVER OSID STATUS: the line the installer ends with, or
# nothing for a fixed kernel (DECISIONS I-1588, I-1600). The Ubuntu remedy
# installs the newest kernel of the series the box runs, not a full
# upgrade: an image can carry a second series (Latitude's Ubuntu 24.04
# carries the HWE 7.0 beside the 6.8 it boots), a full upgrade moves that
# one too, and repose's x86 hosts were measured on 6.8 only.
kernel_note() {
  local kver=$1 osid=$2 status=$3 note remedy
  local what="through which code in one of your machines could take over this box"
  case $status in
    fixed) return ;;
    unfixed) note="warning: kernel $kver does not carry the fix for CVE-2026-99999, $what." ;;
    *) note="warning: could not check whether kernel $kver carries the fix for CVE-2026-99999, $what: its version is not one this installer knows and its package changelog could not be read." ;;
  esac
  case $osid in
    ubuntu)
      case $kver in
        6.8.0-*-generic | 6.8.0-*-lowlatency) remedy="Ubuntu 24.04's kernels carry it from 6.8.0-149: apt-get update && apt-get install linux-image-generic, reboot, check that uname -r shows 6.8.0-149 or later" ;;
        7.0.0-*-generic | 7.0.0-*-lowlatency) remedy="Ubuntu 24.04's HWE 7.0 kernels carry it from 7.0.0-43: apt-get update && apt-get install linux-image-generic-hwe-24.04, reboot, check that uname -r shows 7.0.0-43 or later" ;;
        *) remedy="Ubuntu 24.04's generic kernels carry it from 6.8.0-149 and its HWE 7.0 kernels from 7.0.0-43: install one, or a kernel whose changelog names the CVE, reboot into it" ;;
      esac
      note+=" $remedy, and run this installer again to check." ;;
    debian) note+=" Debian 13's kernel carries it from 6.12.113-1, and Debian 12's did not on 2026-11-23; with a kernel that does, reboot and run this installer again to check." ;;
  esac
  echo "$note"
}
# ---- end of kernel check -------------------------------------------------

prepare_install() {
# ---- what this machine is ------------------------------------------------
[ "$(uname -m)" = x86_64 ] || die "this installer is for x86_64; this machine is $(uname -m). A Mac runs \`repose host add --join\`."
[ "$dry" = 1 ] || [ "$(id -u)" = 0 ] || die "run it as root (sudo bash -s -- ...)."
[ -d /run/systemd/system ] || die "systemd is not running here; repose's units need it."
[ -c /dev/kvm ] || die "/dev/kvm is missing: enable virtualization (VT-x or AMD-V) in the firmware, or use a bare-metal or nested-virtualization instance."
# shellcheck source=/dev/null
. /etc/os-release
case "$ID:${VERSION_ID%%.*}" in
  ubuntu:2[4-9] | ubuntu:[3-9][0-9] | debian:1[2-9]) ;;
  *) die "this is $PRETTY_NAME; the installer supports Ubuntu 24.04 and Debian 12 or later." ;;
esac
# The kernel. Below 6.8, the kernel Ubuntu 24.04 shipped with, the parts
# hostd relies on (nftables' per-address connection count, erofs with
# file-backed images, vhost-vsock as Cloud Hypervisor 53 uses it) are not
# what repose is tested on: refused. A kernel not shown to carry the fix
# for CVE-2026-99999, a guest-to-host escape through a tap's checksum
# metadata, is warned about at the end, where the output is read, and
# installed on: the only guests on this machine are its owner's
# (DECISIONS I-1588).
kver=$(uname -r)
kmaj=${kver%%.*} krest=${kver#*.} kmin=${krest%%[!0-9]*}
if [ "$kmaj" -lt 6 ] || { [ "$kmaj" = 6 ] && [ "$kmin" -lt 8 ]; }; then
  die "kernel $kver is older than 6.8; upgrade the kernel first."
fi
if [ -n "$join_file" ]; then
  [ -r "$join_file" ] || die "cannot read $join_file"
  join=$(tr -d '[:space:]' < "$join_file")
fi
registered=0
[ -s /var/lib/repose/hostd/host.json ] && registered=1
if [ -z "$join" ] && [ "$registered" = 0 ]; then
  die "--join is missing: pass the argument \`repose host add\` printed."
fi
if [ -n "$device" ] && [ -n "$loop_file" ]; then
  die "--device and --loop-file are two places for the same pool; pass one."
fi
if [ -n "$api" ] && [ -z "$api_ca_file" ] && [ -z "$api_name" ]; then
  say "--api without --api-ca or --api-server-name: the api's certificate is checked against the platform host CA as $API_SERVER_NAME."
fi
if [ -z "$uplink" ]; then
  uplink=$(ip -o route show default 2>/dev/null | awk '{ for (i = 1; i < NF; i++) if ($i == "dev") { print $(i + 1); exit } }')
  [ -n "$uplink" ] || die "no default route; pass --uplink with the interface guests reach the internet through."
fi
if [ -e /etc/wireguard/wg0.conf ] && [ "$(readlink /etc/wireguard/wg0.conf || true)" != /run/repose/wg0.conf ]; then
  die "/etc/wireguard/wg0.conf is this machine's own; repose's tunnel is wg0 too. Move it to another name first."
fi
if [ -z "$device" ]; then
  loop_file=${loop_file:-/var/lib/repose/guests.img}
  if [ -z "$size" ] && [ ! -e "$loop_file" ]; then
    mkdir -p "$(dirname "$loop_file")" 2>/dev/null || true
    free=$(df -BG --output=avail "$(dirname "$loop_file")" 2>/dev/null | tail -1 | tr -dc 0-9)
    size=100
    [ -n "$free" ] && [ $((free - 20)) -lt "$size" ] && size=$((free - 20))
  fi
  if [ ! -e "$loop_file" ] && [ "${size:-0}" -lt 20 ]; then
    die "the guest pool needs 20 GiB and this disk has under 40 GiB free (20 stay free for the machine); free up space, or pass --device."
  fi
else
  [ -b "$device" ] || die "$device is not a block device."
fi
say "machine: $PRETTY_NAME, kernel $kver, uplink $uplink, guests on ${device:-$loop_file${size:+ ($size GiB)}}"

# ---- packages ------------------------------------------------------------
packages=(lvm2 thin-provisioning-tools nftables wireguard-tools iproute2 util-linux
  e2fsprogs zstd curl ca-certificates jq openssl unzip)
missing=()
for p in "${packages[@]}"; do
  dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q 'install ok installed' || missing+=("$p")
done
if [ "${#missing[@]}" -gt 0 ]; then
  step "apt-get install ${missing[*]}" -- bash -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "$@" >/dev/null' _ "${missing[@]}"
else
  say "packages: all present"
fi
# The kernel's fix, checked after the package step, because a fresh
# image can have no apt lists, without which `apt-get changelog` cannot
# fetch anything (x86 pass 2, I-1600). The update failing (no route to
# the archive) only leaves the check to the kernel's version.
if [ "$dry" = 0 ] && ! compgen -G "/var/lib/apt/lists/*Release" >/dev/null; then
  step "apt-get update" -- bash -c 'apt-get update -qq || true'
fi
kernel_note=$(kernel_note "$kver" "$ID" "$(kernel_fix_status "$kver" "$ID")")
}

kernel_note=""
[ "$uninstall" = 1 ] || prepare_install

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# install_file MODE DEST: stdin to DEST when it differs; prints which.
changed=()
install_file() {
  local mode=$1 dest=$2 new=$tmp/new
  cat > "$new"
  if [ -f "$dest" ] && cmp -s "$new" "$dest" && [ "$(stat -c %a "$dest")" = "${mode#0}" ]; then
    return 0
  fi
  changed+=("$dest")
  if [ "$dry" = 1 ]; then
    echo "would: write $dest"
    return 0
  fi
  echo "==> write $dest"
  mkdir -p "$(dirname "$dest")"
  install -m "$mode" "$new" "$dest.repose-new"
  mv -f "$dest.repose-new" "$dest"
}
# fetch_verified URL SHA256 DEST MODE
fetch_verified() {
  local url=$1 sum=$2 dest=$3 mode=$4
  if [ -f "$dest" ] && echo "$sum  $dest" | sha256sum -c --quiet - 2>/dev/null; then
    return 0
  fi
  changed+=("$dest")
  if [ "$dry" = 1 ]; then
    echo "would: fetch $url to $dest (sha256 $sum)"
    return 0
  fi
  echo "==> fetch $url"
  curl -fsSL --retry 3 -o "$tmp/fetch" "$url"
  echo "$sum  $tmp/fetch" | sha256sum -c --quiet - || die "$url does not match its pinned sha256"
  install -m "$mode" "$tmp/fetch" "$dest.repose-new"
  mv -f "$dest.repose-new" "$dest"
}

# BEGIN GENERATED HOST FILES (images/appliance-arm64/embed.sh)
# Do not edit by hand: the sources are under images/appliance-arm64/.
write_host_files() {
  install_file 0755 /usr/local/lib/repose/repose-guest-clean-stop <<'REPOSE_HOST_FILE'
#!/bin/bash
# At a host shutdown, power a guest off through ACPI rather than letting
# systemd's SIGTERM end Cloud Hypervisor without telling the guest, which
# loses what its page cache held (DECISIONS I-650). Only at a system
# shutdown: hostd's own stops ask guestd first. Ported from
# nix/hosts/hostd.nix (I-1494).
set -uo pipefail
id=$1
case "$(systemctl is-system-running 2>/dev/null || true)" in
  running | degraded | starting | initializing | maintenance) exit 0 ;;
esac
[ -n "${MAINPID:-}" ] || exit 0
sock=/var/lib/repose/guests/$id/ch.sock
# A guest the idle pause left resting cannot service the ACPI event
# (I-818, I-824): resume it first; on a running VM this is refused and
# harmless.
curl -s -m 2 --unix-socket "$sock" -X PUT http://localhost/api/v1/vm.resume >/dev/null || true
curl -s -m 2 --unix-socket "$sock" -X PUT http://localhost/api/v1/vm.power-button >/dev/null || exit 0
echo "repose-guest-clean-stop: event=guest_power_button guest_id=$id"
start=$(date +%s)
while kill -0 "$MAINPID" 2>/dev/null; do
  if [ $(( $(date +%s) - start )) -ge 20 ]; then
    echo "repose-guest-clean-stop: event=guest_power_button_timeout guest_id=$id"
    exit 0
  fi
  sleep 0.2
done
echo "repose-guest-clean-stop: event=guest_powered_off guest_id=$id seconds=$(( $(date +%s) - start ))"
REPOSE_HOST_FILE
  install_file 0755 /usr/local/lib/repose/repose-guest-pool <<'REPOSE_HOST_FILE'
#!/bin/bash
# repose-guest-pool (DECISIONS I-643), ported from nix/hosts/storage.nix
# (I-1494): the one thing that activates vg-guests/thin after boot.
# Exit 0: pool active. Exit 1: the VG never appeared, or three attempts
# failed. Exit 2: thin_check says the metadata is damaged; never retried
# and never repaired. Every failure leaves /run/repose/pool-hold, which
# the unit's ExecCondition reads, so a failed pool is not retried at
# every hostd restart. Log lines carry VG, volume and dm names and exit
# codes only.
set -euo pipefail
vg="vg-guests"
pool=thin
# dm names double the dashes of the VG and LV names.
dmpool="vg--guests-thin"
hold_file=/run/repose/pool-hold
vg_wait=${REPOSE_POOL_VG_WAIT:-300}
say() { echo "repose-guest-pool: $*"; }
hold() {
  mkdir -p "$(dirname "$hold_file")"
  : > "$hold_file"
  exit "$1"
}

if [ "${1:-}" = --held ]; then
  # The unit's ExecCondition: skipped, not failed, while held.
  if [ -e "$hold_file" ]; then
    say "event=pool_held marker=$hold_file"
    exit 1
  fi
  exit 0
fi
rm -f "$hold_file"

udevadm settle --timeout=60 || true
waited=0
until vgs "$vg" >/dev/null 2>&1; do
  if [ "$waited" -ge "$vg_wait" ]; then
    say "event=pool_vg_missing vg=$vg waited_s=$waited"
    hold 1
  fi
  sleep 1
  waited=$((waited + 1))
done

if [ "$(vgs --noheadings -o autoactivation "$vg" | tr -d ' ')" = enabled ]; then
  vgchange --setautoactivation n "$vg"
  say "event=pool_autoactivation_off vg=$vg"
fi

lvm_says_active() {
  [ "$(lvs --noheadings -o lv_active "$vg/$pool" 2>/dev/null | tr -d ' ')" = active ]
}
pool_active() {
  local table
  lvm_says_active || return 1
  table=$(dmsetup table "$dmpool-tpool" 2>/dev/null || dmsetup table "$dmpool" 2>/dev/null || true)
  grep -q ' thin-pool ' <<< "$table"
}
open_count() { dmsetup info -c --noheadings -o open "$1" 2>/dev/null | tr -d ' '; }

clear_leftovers() {
  local d n names
  if lvm_says_active; then
    say "event=pool_leftover_held reason=pool_reported_active"
    return 1
  fi
  names=$(dmsetup info -c --noheadings -o name 2>/dev/null || true)
  if grep -qE "^vg--guests-(g--|auth--|repose--cache)" <<< "$names"; then
    say "event=pool_leftover_held reason=thin_volume_device_present"
    return 1
  fi
  for d in "$dmpool" "$dmpool-tpool" "$dmpool"_tdata "$dmpool"_tmeta; do
    dmsetup info "$d" >/dev/null 2>&1 || continue
    n=$(open_count "$d")
    if [ "$n" != 0 ]; then
      say "event=pool_leftover_held device=$d open_count=$n"
      return 1
    fi
    dmsetup remove "$d"
    say "event=pool_leftover_removed device=$d"
  done
}

attempt() {
  local err rc status
  if pool_active; then
    say "event=pool_already_active vg=$vg"
  else
    clear_leftovers || return 1
    err=$(mktemp)
    rc=0
    lvchange -ay "$vg/$pool" 2>"$err" || rc=$?
    if [ "$rc" != 0 ]; then
      status=$(grep -oE 'Check of pool .* failed \(status:-?[0-9]+\)' "$err" | grep -oE -- '-?[0-9]+\)$' | tr -d ')' || true)
      rm -f "$err"
      if [ -n "$status" ] && [ "$status" -gt 0 ]; then
        say "event=pool_check_failed vg=$vg pool=$pool status=$status"
        hold 2
      fi
      say "event=pool_activate_failed vg=$vg pool=$pool exit=$rc"
      return 1
    fi
    rm -f "$err"
    say "event=pool_activated vg=$vg pool=$pool"
  fi
  vgchange -ay "$vg" >/dev/null || say "event=pool_volumes_partial vg=$vg"
  pool_active || return 1
  return 0
}

delays=(10 30 60)
ok=0
for i in 1 2 3; do
  if attempt; then ok=1; break; fi
  [ "$i" = 3 ] && break
  sleep "${REPOSE_POOL_RETRY_DELAY:-${delays[$((i - 1))]}}"
done
if [ "$ok" != 1 ]; then
  say "event=pool_unavailable vg=$vg attempts=3"
  hold 1
fi

inactive=$(lvs --noheadings -o lv_name,lv_active "$vg" | awk '$1 ~ /^(g|auth)-/ && $2 != "active" { n++ } END { print n + 0 }')
if [ "$inactive" != 0 ]; then
  say "event=pool_volumes_inactive vg=$vg count=$inactive"
fi

# dmeventd: autoextend acts only on a monitored pool.
systemctl start dm-event.service || true
lvchange --monitor y "$vg/$pool" || true
if [ "$(lvs --noheadings -o seg_monitor "$vg/$pool" | tr -d ' ')" = monitored ]; then
  say "event=pool_monitored vg=$vg pool=$pool"
else
  say "event=pool_unmonitored vg=$vg pool=$pool"
fi
REPOSE_HOST_FILE
  install_file 0755 /usr/local/lib/repose/repose-guests-slice <<'REPOSE_HOST_FILE'
#!/bin/bash
# guests.slice gets everything but the host reserve: an eighth of RAM,
# floor 2 GiB, ceiling 16 GiB, the rule of the api's scheduler.HostReserve
# and hostd's HostReserveBytes (DECISIONS I-724). Ported from
# nix/hosts/hostd.nix (I-1494). On a host its user owns, hostd
# --owned-host sets it again at every start to all but the owned reserve
# (DECISIONS I-1628), before any guest starts; this boot figure stands
# only for a hostd without the flag.
set -euo pipefail
total=$(awk '/^MemTotal:/ { print $2 * 1024 }' /proc/meminfo)
gib=$((1024 * 1024 * 1024))
reserve=$((total / 8))
if [ "$reserve" -lt $((2 * gib)) ]; then reserve=$((2 * gib)); fi
if [ "$reserve" -gt $((16 * gib)) ]; then reserve=$((16 * gib)); fi
max=$((total - reserve))
if [ "$max" -le 0 ]; then max=$((total / 2)); fi
systemctl set-property --runtime guests.slice MemoryMax="$max"
echo "guests.slice MemoryMax=$max (total=$total reserve=$reserve)"
REPOSE_HOST_FILE
  install_file 0755 /usr/local/lib/repose/repose-host-net <<'REPOSE_HOST_FILE'
#!/bin/bash
# Configure br-guests and wg0 from host.json (nix/hosts/network.nix,
# ported for the appliance and the x86 installer, DECISIONS I-1494).
# Renders /run/repose/host.env, /run/repose/wg0.conf (0600) and the
# bridge's address drop-in, then restarts the units that read them.
# Without host.json the bridge stays unaddressed and, when hub.json is
# there (the join bundle's place on the hub, I-1380), wg0 is rendered
# from it so `hostd register` reaches the control plane's hub address.
# There is no sshd on an owned host, so nothing here renders its files.
set -euo pipefail
hostJson=${REPOSE_HOST_JSON:-/var/lib/repose/hostd/host.json}
run=${REPOSE_RUN_DIR:-/run/repose}
hubJson="$run/hub.json"
netd=/run/systemd/network/20-br-guests.network.d
mkdir -p "$run"
# 0755 whatever created it first (DECISIONS I-97).
chmod 0755 "$run"
umask 022

write_wg0() {
  # $1: a JSON file with private_key, address, edge_pubkey and
  # edge_endpoint under the jq prefix $2. Prints 1 when it wrote the file,
  # 0 when the material is incomplete and the file was removed.
  local key address edge_pubkey endpoint
  key=$(jq -r "${2}.private_key // empty" "$1")
  address=$(jq -r "${2}.address // empty" "$1")
  edge_pubkey=$(jq -r "${2}.edge_pubkey // empty" "$1")
  endpoint=$(jq -r "${2}.edge_endpoint // empty" "$1")
  if [ -n "$key" ] && [ -n "$address" ] && [ -n "$edge_pubkey" ] && [ -n "$endpoint" ]; then
    (
      umask 077
      {
        echo "[Interface]"
        echo "PrivateKey = $key"
        echo "Address = $address"
        echo
        echo "[Peer]"
        echo "PublicKey = $edge_pubkey"
        echo "Endpoint = $endpoint"
        echo "AllowedIPs = 10.255.0.0/16"
        echo "PersistentKeepalive = 25"
      } > "$run/wg0.conf.tmp"
      mv "$run/wg0.conf.tmp" "$run/wg0.conf"
    )
    echo 1
  else
    rm -f "$run/wg0.conf"
    echo 0
  fi
}

if [ ! -s "$hostJson" ]; then
  echo "no host.json; bridge not configured"
  rm -f "$run/host.env"
  rm -rf "$netd"
  networkctl reload || true
  if [ -s "$hubJson" ] && [ "$(write_wg0 "$hubJson" "")" = 1 ]; then
    echo "hub.json applied: wg0 configured for registration"
    systemctl --no-block restart wg-quick@wg0.service
  else
    rm -f "$run/wg0.conf"
    systemctl --no-block stop wg-quick@wg0.service
  fi
  exit 0
fi
# Registered: the bootstrap material is spent (I-1380).
rm -f "$hubJson"

host_id=$(jq -er .host_id "$hostJson")
guest_cidr=$(jq -er .guest_cidr "$hostJson")
wg_address=$(jq -r '.wg.address // empty' "$hostJson")
wg_ip=${wg_address%/*}

IFS='./' read -r a b c d prefix <<< "$guest_cidr"
bridge_ip="$a.$b.$c.$((d + 1))"
bridge_address="$bridge_ip/$prefix"

cat > "$run/host.env.tmp" <<ENV
HOST_ID=$host_id
GUEST_CIDR=$guest_cidr
BRIDGE_ADDR=$bridge_ip
WG_ADDR=$wg_ip
ENV
mv "$run/host.env.tmp" "$run/host.env"

if [ "$(write_wg0 "$hostJson" ".wg")" != 1 ]; then
  echo "no WireGuard material in host.json; wg0 not configured"
fi

mkdir -p "$netd"
printf '[Network]\nAddress=%s\n' "$bridge_address" > "$netd/10-address.conf"
networkctl reload
networkctl reconfigure br-guests || true

echo "host.json applied: host_id=$host_id guest_cidr=$guest_cidr bridge=$bridge_address"
if [ -s "$run/wg0.conf" ]; then
  systemctl --no-block restart wg-quick@wg0.service
else
  systemctl --no-block stop wg-quick@wg0.service
fi
REPOSE_HOST_FILE
  install_file 0755 /usr/local/lib/repose/repose-hostd-update <<'REPOSE_HOST_FILE'
#!/bin/bash
# hostd's unattended update on a host repose does not rebuild: the Mac
# appliance and a machine ops/install-host-x86.sh set up (DECISIONS
# I-1496). repose-hostd-update.timer runs it every 30 minutes.
#
# The channel is the newest release `hostd-<version>` that is not a draft
# and carries a `manifest.json`, found through GitHub's releases API,
# anonymously, in version order (sort -V: 2026.11.23 after 2026.11.22.10).
# The repository's releases are immutable, so there is no moving channel
# release; `hostd-stable` is a dead name and is passed over. The lookup is
# not the trust; the manifest's signature is:
#   {"version": "...",
#    "arches": {"amd64": {"url": "...", "sha256": "..."},
#               "arm64": {"url": "...", "sha256": "..."}},
#    "signature": "<base64>"}
# signed with the release key (install.sh RELEASE_PUBKEY) over this text,
# one line each, the arches in that order:
#   repose-hostd-manifest v1
#   version <version>
#   amd64 <sha256> <url>
#   arm64 <sha256> <url>
# Each verified manifest is kept in the state directory; a lookup the API
# refuses (403 or 429: its anonymous rate limit) uses the kept one, which
# for a host already on it is nothing to do, and exits 0. A manifest that
# does not verify, a version older than the running one, or a binary whose
# sha256 differs changes nothing. Otherwise the binary is written beside
# /usr/local/bin/hostd, asked its version, renamed over the old one and
# hostd restarted; hostd.service has KillMode=process, so the guests keep
# running and hostd reconciles them at its start.
#
#   repose-hostd-update [--manifest-url URL] [--force]
#
# Environment (tests): REPOSE_HOSTD_RELEASES_URL, REPOSE_HOSTD_MANIFEST_URL,
# REPOSE_HOSTD_BIN, REPOSE_HOSTD_STATE_DIR, REPOSE_RELEASE_PUBKEY_FILE,
# REPOSE_HOSTD_RESTART (the command that restarts hostd).
set -euo pipefail

releases="https://api.github.com/repos/Heracraft/repose/releases?per_page=30"
url=""
# shellcheck source=/dev/null
[ -r /etc/repose/hostd-update.env ] && . /etc/repose/hostd-update.env
releases=${REPOSE_HOSTD_RELEASES_URL:-$releases}
url=${REPOSE_HOSTD_MANIFEST_URL:-$url}
force=0
while [ $# -gt 0 ]; do
  case $1 in
    --manifest-url) url=${2:?}; shift 2 ;;
    --force) force=1; shift ;;
    *) echo "repose-hostd-update: unknown argument $1" >&2; exit 2 ;;
  esac
done
bin=${REPOSE_HOSTD_BIN:-/usr/local/bin/hostd}
state=${REPOSE_HOSTD_STATE_DIR:-/var/lib/repose/hostd-update}
pubkey=${REPOSE_RELEASE_PUBKEY_FILE:-/etc/repose/release.pub}
restart=${REPOSE_HOSTD_RESTART:-systemctl restart hostd.service}
say() { echo "repose-hostd-update: $*"; }
start=$(date +%s.%N)

case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64) arch=arm64 ;;
  *) say "event=update_refused reason=arch arch=$(uname -m)" >&2; exit 1 ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
m=$tmp/manifest.json
cached=$state/manifest.json
from_cache=0

if [ -z "$url" ]; then
  code=$(curl -sS --max-time 60 -H 'Accept: application/vnd.github+json' -o "$tmp/releases.json" -w '%{http_code}' "$releases" 2>/dev/null) || code=000
  if [ "$code" != 200 ]; then
    say "event=lookup_failed status=$code"
    case $code in
      403 | 429) ;;
      *) [ -s "$cached" ] || exit 1 ;;
    esac
    if [ ! -s "$cached" ]; then
      say "event=nothing_to_do reason=no_cached_manifest"
      exit 0
    fi
    cp "$cached" "$m"
    from_cache=1
  else
    tag=$(jq -r '.[] | select((.draft | not) and (.tag_name | test("^hostd-[0-9]")) and any(.assets[]?; .name == "manifest.json")) | .tag_name' "$tmp/releases.json" |
      sed 's/^hostd-//' | sort -V | tail -1)
    if [ -z "$tag" ]; then
      say "event=no_release"
      exit 0
    fi
    tag=hostd-$tag
    url=$(jq -r --arg t "$tag" '.[] | select(.tag_name == $t) | .assets[] | select(.name == "manifest.json") | .browser_download_url' "$tmp/releases.json" | head -1)
    say "event=release_found tag=$tag"
  fi
fi
if [ "$from_cache" = 0 ] && ! curl -fsSL --max-time 60 -o "$m" "$url"; then
  say "event=manifest_fetch_failed" >&2
  exit 1
fi
field() { jq -er "$1" "$m" 2>/dev/null; }
if ! version=$(field .version) || ! sig=$(field .signature) ||
  ! a_url=$(field .arches.amd64.url) || ! a_sum=$(field .arches.amd64.sha256) ||
  ! r_url=$(field .arches.arm64.url) || ! r_sum=$(field .arches.arm64.sha256); then
  say "event=update_refused reason=manifest_shape" >&2
  exit 1
fi
printf 'repose-hostd-manifest v1\nversion %s\namd64 %s %s\narm64 %s %s\n' \
  "$version" "$a_sum" "$a_url" "$r_sum" "$r_url" > "$tmp/signed"
if ! printf '%s' "$sig" | base64 -d > "$tmp/sig" 2>/dev/null ||
  ! openssl dgst -sha256 -verify "$pubkey" -signature "$tmp/sig" "$tmp/signed" >/dev/null 2>&1; then
  say "event=update_refused reason=signature version=$version" >&2
  exit 1
fi
say "event=manifest_verified version=$version"
if [ "$from_cache" = 0 ]; then
  mkdir -p "$state"
  cp "$m" "$cached.tmp" && mv -f "$cached.tmp" "$cached"
fi

current=$("$bin" version 2>/dev/null | awk '{ print $2 }') || current=""
if [ "$current" = "$version" ] && [ "$force" = 0 ]; then
  say "event=up_to_date version=$version"
  exit 0
fi
if [ -n "$current" ] && [ "$current" != dev ] && [ "$force" = 0 ] &&
  [ "$(printf '%s\n%s\n' "$current" "$version" | sort -V | tail -1)" = "$current" ]; then
  say "event=update_refused reason=older version=$version running=$current" >&2
  exit 1
fi

if [ "$arch" = amd64 ]; then want_url=$a_url want_sum=$a_sum; else want_url=$r_url want_sum=$r_sum; fi
# Beside the binary, so the rename below is one filesystem's and atomic.
new="$(dirname "$bin")/.hostd.new"
rm -f "$new"
if ! curl -fsSL --max-time 300 -o "$new" "$want_url"; then
  rm -f "$new"
  say "event=binary_fetch_failed version=$version" >&2
  exit 1
fi
got=$(sha256sum "$new" | awk '{ print $1 }')
if [ "$got" != "$want_sum" ]; then
  rm -f "$new"
  say "event=update_refused reason=sha256 version=$version" >&2
  exit 1
fi
say "event=sha256_verified version=$version"
chmod 0755 "$new"
said=$("$new" version 2>/dev/null | awk '{ print $2 }') || said=""
if [ "$said" != "$version" ]; then
  rm -f "$new"
  say "event=update_refused reason=version_mismatch version=$version binary=$said" >&2
  exit 1
fi
mv -f "$new" "$bin"
$restart
end=$(date +%s.%N)
say "event=updated from=${current:-none} to=$version seconds=$(awk -v a="$start" -v b="$end" 'BEGIN { printf "%.2f", b - a }')"
REPOSE_HOST_FILE
  install_file 0755 /usr/local/lib/repose/repose-images-volume <<'REPOSE_HOST_FILE'
#!/bin/bash
# The base image cache on its own thin volume (DECISIONS I-1494): hostd's
# --images-dir, /var/lib/repose/images, holds up to two bundles and a
# download in progress (I-1478's rotation), about 3 GB each for arm64, and
# the appliance's 2 GiB root cannot hold one (the first create on a Mac
# failed with "no space left on device"). After the pool is active and
# before hostd: make vg-guests/images once, 16 GiB thin, ext4, and mount
# it. Every step is skipped when already done.
set -euo pipefail
vg="vg-guests"
lv=images
size=${REPOSE_IMAGES_SIZE:-16G}
dir=/var/lib/repose/images
say() { echo "repose-images-volume: $*"; }

if ! lvs "$vg/$lv" >/dev/null 2>&1; then
  lvcreate -q -y -V "$size" -T "$vg/thin" -n "$lv"
  udevadm settle --timeout 30 || true
  mkfs.ext4 -q -L repose-images -E lazy_itable_init=1 "/dev/$vg/$lv"
  say "event=volume_created size=$size"
fi
if [ "$(lvs --noheadings -o lv_active "$vg/$lv" | tr -d ' ')" != active ]; then
  lvchange -ay "$vg/$lv"
fi
mkdir -p "$dir"
if mountpoint -q "$dir"; then
  say "event=volume_mounted_already"
else
  # A cache written to the root before this volume existed is moved in,
  # not hidden under the mount.
  if [ -n "$(ls -A "$dir" 2>/dev/null)" ]; then
    tmp=$(mktemp -d /run/repose-images.XXXXXX)
    mount -o noatime,discard "/dev/$vg/$lv" "$tmp"
    cp -a "$dir"/. "$tmp"/
    umount "$tmp"
    rmdir "$tmp"
    find "$dir" -mindepth 1 -delete
    say "event=cache_moved"
  fi
  mount -o noatime,discard "/dev/$vg/$lv" "$dir"
  say "event=volume_mounted"
fi
chmod 0755 "$dir"
REPOSE_HOST_FILE
  install_file 0755 /usr/local/lib/repose/repose-launcher-join <<'REPOSE_HOST_FILE'
#!/bin/bash
# The join bundle (hostmgr.JoinBundle, docs/interfaces/api.md): one
# base64url string carrying {"v":1,"token":...,"wg":{...}}. Split into the
# two files the host reads: /run/repose/join-token for repose-register and
# /run/repose/hub.json for repose-host-net's bootstrap tunnel. On the Mac
# appliance the launcher shares the bundle in over virtio-fs; on an x86
# machine ops/install-host-x86.sh writes it to the same path. Ported from
# nix/hosts/mac.nix (DECISIONS I-1380, I-1494). Logs name events only; the
# token and the key never reach a line.
set -euo pipefail
join=${REPOSE_JOIN_FILE:-/run/repose/launcher/join}
run=${REPOSE_RUN_DIR:-/run/repose}
stateJson=${REPOSE_HOST_JSON:-/var/lib/repose/hostd/host.json}
umask 077
mkdir -p "$run"
chmod 0755 "$run"
if [ ! -s "$join" ]; then
  echo "repose-launcher-join: event=no_join_file"
  exit 0
fi
if [ -s "$stateJson" ]; then
  # Registered already: a used bundle is dead, and a new one for a
  # registered host is `repose host rm` and `host add` again.
  echo "repose-launcher-join: event=join_ignored reason=registered"
  rm -f "$join" || true
  exit 0
fi
raw=$(tr -d '[:space:]' < "$join")
# base64url without padding: GNU basenc wants it padded.
pad=$(( (4 - ${#raw} % 4) % 4 ))
padded="$raw$(printf '%*s' "$pad" "" | tr ' ' '=')"
if ! decoded=$(printf '%s' "$padded" | basenc --base64url -d 2>/dev/null); then
  echo "repose-launcher-join: event=join_invalid reason=encoding" >&2
  exit 1
fi
if [ "$(printf '%s' "$decoded" | jq -r '.v // 0')" != "1" ]; then
  echo "repose-launcher-join: event=join_invalid reason=version" >&2
  exit 1
fi
token=$(printf '%s' "$decoded" | jq -r '.token // empty')
if [ -z "$token" ]; then
  echo "repose-launcher-join: event=join_invalid reason=no_token" >&2
  exit 1
fi
if [ ! -e "$run/join-token" ]; then
  printf '%s\n' "$token" > "$run/join-token.tmp"
  chmod 0600 "$run/join-token.tmp"
  mv "$run/join-token.tmp" "$run/join-token"
  echo "repose-launcher-join: event=join_token_written"
fi
if printf '%s' "$decoded" | jq -e '.wg.private_key and .wg.address and .wg.edge_pubkey and .wg.edge_endpoint' >/dev/null; then
  printf '%s' "$decoded" | jq '.wg' > "$run/hub.json.tmp"
  chmod 0600 "$run/hub.json.tmp"
  mv "$run/hub.json.tmp" "$run/hub.json"
  echo "repose-launcher-join: event=hub_bootstrap_written"
else
  echo "repose-launcher-join: event=no_hub_in_join"
fi
# Single use: nothing of the bundle outlives the registration it was for.
rm -f "$join" || echo "repose-launcher-join: event=join_file_kept reason=share_read_only"
REPOSE_HOST_FILE
  install_file 0755 /usr/local/lib/repose/repose-pool-create <<'REPOSE_HOST_FILE'
#!/bin/bash
# Make vg-guests and its thin pool on one empty block device, the shape
# disko-layout.nix gives a data disk at install: the pool at 95 percent of
# the VG, its metadata one percent of the PV clamped to 1..16 GiB,
# discards passed down, new blocks zeroed (DECISIONS I-1494). The Mac
# appliance runs it at first boot on its third partition
# (repose-guest-vg-init.service) and ops/install-host-x86.sh on --device
# or the loop device of --loop-file.
#
# A device that already carries vg-guests is left alone and is success. A
# device with any other signature is refused: this never formats
# something it did not make.
#
#   repose-pool-create <device>
set -euo pipefail
dev=${1:?usage: repose-pool-create <device>}
vg="vg-guests"
gib=$((1024 * 1024 * 1024))
say() { echo "repose-pool-create: $*"; }

if pvs --noheadings -o vg_name "$dev" 2>/dev/null | grep -qx "[[:space:]]*$vg"; then
  if lvs "$vg/thin" >/dev/null 2>&1; then
    say "event=pool_present device=$dev"
    exit 0
  fi
elif sig=$(blkid -p -o value -s TYPE "$dev" 2>/dev/null) && [ -n "$sig" ]; then
  say "event=device_refused device=$dev reason=signature_$sig" >&2
  exit 1
else
  pvcreate -q "$dev"
  vgcreate -q "$vg" "$dev"
  say "event=vg_created device=$dev"
fi

pv=$(blockdev --getsize64 "$dev")
meta=$((pv / 100))
[ "$meta" -lt $gib ] && meta=$gib
[ "$meta" -gt $((16 * gib)) ] && meta=$((16 * gib))
# 95 percent of the VG, and never past what it holds beside the metadata
# and its spare (on a small disk those two are more than 5 percent), in
# whole extents: the same sum repose-mac-grow makes, so its first run
# after this changes nothing.
vg_size=$(vgs --noheadings --units b --nosuffix -o vg_size "$vg" | tr -d ' ')
extent=$(vgs --noheadings --units b --nosuffix -o vg_extent_size "$vg" | tr -d ' ')
meta=$(( (meta + extent - 1) / extent * extent ))
want=$(( vg_size * 95 / 100 ))
cap=$(( vg_size - 2 * meta - 4 * extent ))
[ "$want" -gt "$cap" ] && want=$cap
want=$(( want / extent * extent ))
if [ "$want" -le 0 ]; then
  say "event=device_too_small device=$dev pv_bytes=$pv" >&2
  exit 1
fi
lvcreate -q -y --type thin-pool -L "${want}b" --poolmetadatasize "${meta}b" \
  --discards passdown --zero y -n thin "$vg"
# repose-guest-pool owns activation (I-643).
vgchange -q --setautoactivation n "$vg"
# The pool's autoextend (I-643), as a profile so the machine's own VGs keep theirs.
vgchange -q --metadataprofile repose-thin "$vg"
say "event=pool_created device=$dev pv_bytes=$pv metadata_bytes=$meta"
REPOSE_HOST_FILE
  install_file 0755 /usr/local/lib/repose/repose-snapshots-volume <<'REPOSE_HOST_FILE'
#!/bin/bash
# The snapshot store on its own thin volume (DECISIONS I-1636): hostd's
# --snapshot-dir, /var/lib/repose/snapshots, sat on the appliance's 2 GiB
# root, and one large stop (1.3 GB) and four tiny ones filled it on the
# lab Mac, after which every stop failed. After the pool is active and
# before hostd: make vg-guests/snapshots once, thin, ext4, and mount it.
#
# Its size is half the volume group's at creation, rounded down to a GiB.
# It is thin, so it costs the pool what the snapshots hold and no more,
# and the pool's own thresholds (I-586) count it with the guests'
# volumes. Every step is skipped when already done, and snapshots
# already on the root are moved in, not hidden under the mount.
set -euo pipefail
vg=${REPOSE_SNAPSHOTS_VG:-vg-guests}
lv=snapshots
dir=${REPOSE_SNAPSHOTS_DIR:-/var/lib/repose/snapshots}
say() { echo "repose-snapshots-volume: $*"; }

if ! lvs "$vg/$lv" >/dev/null 2>&1; then
  vg_bytes=$(vgs --noheadings --units b --nosuffix -o vg_size "$vg" | tr -d ' ')
  gib=$((vg_bytes / 2 / 1073741824))
  if [ "$gib" -lt 4 ]; then gib=4; fi
  lvcreate -q -y -V "${gib}G" -T "$vg/thin" -n "$lv"
  udevadm settle --timeout 30 || true
  mkfs.ext4 -q -L repose-snapshots -E lazy_itable_init=1 "/dev/$vg/$lv"
  say "event=volume_created size_gib=$gib"
fi
if [ "$(lvs --noheadings -o lv_active "$vg/$lv" | tr -d ' ')" != active ]; then
  lvchange -ay "$vg/$lv"
fi
mkdir -p "$dir"
if mountpoint -q "$dir"; then
  say "event=volume_mounted_already"
else
  if [ -n "$(ls -A "$dir" 2>/dev/null)" ]; then
    tmp=$(mktemp -d "${REPOSE_SNAPSHOTS_TMP:-/run}/repose-snapshots.XXXXXX")
    mount -o noatime,discard "/dev/$vg/$lv" "$tmp"
    # Copied, then checked, then deleted: a snapshot is never in neither.
    # A copy that fails or differs leaves the root as it was, and hostd
    # keeps writing there until the cause is fixed.
    if ! cp -a "$dir"/. "$tmp"/ || ! diff -rq "$dir" "$tmp" >/dev/null; then
      umount "$tmp"
      rmdir "$tmp"
      say "event=snapshots_not_moved reason=copy_failed"
      exit 1
    fi
    n=$(find "$dir" -type f | wc -l)
    umount "$tmp"
    rmdir "$tmp"
    find "$dir" -mindepth 1 -delete
    say "event=snapshots_moved files=$n"
  fi
  mount -o noatime,discard "/dev/$vg/$lv" "$dir"
  say "event=volume_mounted"
fi
chmod 0755 "$dir"
REPOSE_HOST_FILE
  install_file 0755 /usr/local/lib/repose/repose-zram <<'REPOSE_HOST_FILE'
#!/bin/bash
# Compressed swap in memory for a host its user owns (DECISIONS I-1626):
# when the guests together want more than the host has, the coldest guest
# pages are compressed into RAM instead of the OOM killer ending a guest,
# so overflow is slowness first. Guest RAM is a shared memfd, which the
# kernel can swap like any anonymous page.
#
# One zram device of half the host's memory, compressed with zstd: guest
# memory compresses about three to one, so a full device holds about a
# sixth of the host. Highest priority, so a host that also has a swap
# file or partition fills this first. A kernel without zram leaves the
# host as it was and says so; nothing else depends on this unit. There is
# no stop: a swapoff at shutdown would page every guest's swapped memory
# back in while guests may still run, and power-off ends the device.
set -euo pipefail
dev=/dev/zram0
sys=/sys/block/zram0
say() { echo "repose-zram: $*"; }

if grep -q "^$dev " /proc/swaps; then
  say "event=zram_ready already=1"
  exit 0
fi
modprobe zram num_devices=1 2>/dev/null || true
if [ ! -d "$sys" ]; then
  say "event=zram_unavailable reason=no_module"
  exit 0
fi
total=$(awk '/^MemTotal:/ { print $2 * 1024 }' /proc/meminfo)
size=$((total / 2))
echo 1 > "$sys/reset" 2>/dev/null || true
if ! echo zstd > "$sys/comp_algorithm" 2>/dev/null; then
  say "event=zram_algorithm algorithm=$(sed 's/.*\[\([^]]*\)\].*/\1/' "$sys/comp_algorithm")"
fi
echo "$size" > "$sys/disksize"
mkswap "$dev" >/dev/null
swapon -p 100 "$dev"
# For swap in memory the kernel's documentation suggests a swappiness
# over 100 and no read-ahead: a page in zram costs a decompression, not a
# disk seek, so there is nothing to gain from reading its neighbours.
echo 150 > /proc/sys/vm/swappiness
echo 0 > /proc/sys/vm/page-cluster
say "event=zram_ready disksize_bytes=$size algorithm=$(sed 's/.*\[\([^]]*\)\].*/\1/' "$sys/comp_algorithm")"
REPOSE_HOST_FILE
  install_file 0755 /usr/local/lib/repose/repose-guest-loop <<'REPOSE_HOST_FILE'
#!/bin/bash
# Attach the file that holds vg-guests on a machine with one disk
# (ops/install-host-x86.sh --loop-file) to a loop device, at every boot
# before repose-guest-pool. Attached already is success.
#
#   repose-guest-loop <file>
set -euo pipefail
file=${1:?usage: repose-guest-loop <file>}
if [ -n "$(losetup -j "$file" -O NAME -n)" ]; then
  echo "repose-guest-loop: event=loop_present device=$(losetup -j "$file" -O NAME -n)"
  exit 0
fi
dev=$(losetup --find --show --direct-io=on "$file")
echo "repose-guest-loop: event=loop_attached device=$dev"
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/guest@.service.d/10-repose.conf <<'REPOSE_HOST_FILE'
# Every guest@ unit, running ones included: a shutdown stops the guest
# before its shares and powers it off through ACPI (DECISIONS I-650), and
# parks it first where the park is on (I-652). The guest binds its base
# bundle from the image cache volume, so a shutdown stops it before that
# volume goes: before its mount unit stops, and before
# blk-availability.service's ExecStop (`blkdeactivate -u -l wholevg`,
# enabled on Ubuntu and Debian), which unmounts every filesystem on a
# device-mapper device and deactivates its volume group at the start of
# a shutdown. Otherwise the clean stop's ExecStop cannot set up the
# unit's mount namespace (226/NAMESPACE) and Cloud Hypervisor gets a
# plain SIGTERM (DECISIONS I-1550).
[Unit]
After=virtiofsd@%i.service virtiofsd-auth@%i.service repose-images-volume.service blk-availability.service
RequiresMountsFor=/var/lib/repose/images
Before=repose-guest-park.service

[Service]
ExecStop=/usr/local/lib/repose/repose-guest-clean-stop %i
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/guests.slice <<'REPOSE_HOST_FILE'
[Unit]
Description=Tenant guests (Cloud Hypervisor and virtiofsd transient units)

[Slice]
CPUWeight=100
MemoryAccounting=yes
CPUAccounting=yes
IOAccounting=yes
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/hostd.service <<'REPOSE_HOST_FILE'
# hostd on a host repose does not build with Nix: the Mac appliance and an
# x86 machine set up by ops/install-host-x86.sh (DECISIONS I-1494). The
# flags are /etc/repose/hostd.env's. Guests are transient units in
# guests.slice, not children (KillMode=process): a restart of hostd, an
# update included, does not stop them, and hostd reconciles them at start.
[Unit]
Description=repose hostd: runs tenants' guests on this host
Wants=network-online.target repose-host-net.service repose-register.service repose-guests-slice.service repose-guest-pool.service repose-images-volume.service repose-snapshots-volume.service repose-nftables.service
After=network-online.target repose-nftables.service repose-host-net.service repose-register.service repose-guests-slice.service repose-guest-pool.service repose-images-volume.service repose-snapshots-volume.service lvm2-monitor.service

[Service]
EnvironmentFile=/etc/repose/hostd.env
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ExecStart=/usr/local/bin/hostd --state /var/lib/repose/hostd --images-dir /var/lib/repose/images --nix-mode off $HOSTD_API_ARGS $HOSTD_ARGS
Restart=always
RestartSec=2
# A join token that has been used is not retried (03-hostd §6).
RestartPreventExitStatus=3
KillMode=process
LimitNOFILE=1048576
StateDirectory=repose/hostd
StateDirectoryMode=0700
LogsDirectory=repose
OOMScoreAdjust=-900
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-fault-around.service <<'REPOSE_HOST_FILE'
# Fault-around off for the shmem hole-punch race (DECISIONS I-1040,
# I-1042; nix/hosts/kernel.nix). No condition on the file, deliberately:
# a kernel without the knob fails this unit, which is how a host finds
# out the mitigation no longer applies.
[Unit]
Description=Switch fault-around off for the shmem hole-punch race (I-1040)
After=sys-kernel-debug.mount
Requires=sys-kernel-debug.mount
Before=hostd.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'echo 4096 > /sys/kernel/debug/fault_around_bytes'

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-guest-park.service <<'REPOSE_HOST_FILE'
# Park running guests before the host shuts down (DECISIONS I-652, I-1386;
# nix/hosts/park.nix). Enabled only where hostd.env turns the park on: the
# Mac appliance, whose launcher wraps the image keys. Its ExecStop runs at
# shutdown before any guest stops (the guest@, virtiofsd@ and
# virtiofsd-auth@ drop-ins order them before it). TimeoutStopSec is the
# park deadline plus 150 s (park.nix).
[Unit]
Description=Park running guests before the host shuts down
Wants=hostd.service
After=hostd.service repose-guest-pool.service local-fs.target network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/bin/true
ExecStop=/usr/local/bin/hostd park --shutdown
TimeoutStopSec=270

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-guest-pool.service <<'REPOSE_HOST_FILE'
[Unit]
Description=Activate the guest pool vg-guests/thin (DECISIONS I-643)
After=systemd-udevd.service dm-event.socket repose-guest-vg-init.service repose-guest-loop.service
Wants=dm-event.socket
Before=hostd.service
StartLimitIntervalSec=0

[Service]
Type=oneshot
RemainAfterExit=yes
# thin_check of a large pool after a crash takes minutes.
TimeoutStartSec=30min
# A failed run held the unit: skipped, not failed.
ExecCondition=/usr/local/lib/repose/repose-guest-pool --held
ExecStart=/usr/local/lib/repose/repose-guest-pool

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-guests-slice.service <<'REPOSE_HOST_FILE'
[Unit]
Description=Set guests.slice memory cap from the host's RAM
Before=hostd.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/lib/repose/repose-guests-slice

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-host-net.service <<'REPOSE_HOST_FILE'
[Unit]
Description=Configure br-guests, wg0 and per-host files from host.json
After=systemd-networkd.service repose-nftables.service repose-launcher-join.service
Wants=systemd-networkd.service
Before=hostd.service wg-quick@wg0.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/lib/repose/repose-host-net

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-hostd-update.service <<'REPOSE_HOST_FILE'
[Unit]
Description=Update hostd from the signed release manifest (DECISIONS I-1496)
Wants=network-online.target
After=network-online.target hostd.service

[Service]
Type=oneshot
ExecStart=/usr/local/lib/repose/repose-hostd-update
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-hostd-update.timer <<'REPOSE_HOST_FILE'
[Unit]
Description=Check for a new hostd every 30 minutes

[Timer]
OnBootSec=5min
OnUnitActiveSec=30min
RandomizedDelaySec=5min
Unit=repose-hostd-update.service

[Install]
WantedBy=timers.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-images-volume.service <<'REPOSE_HOST_FILE'
# hostd's base image cache on vg-guests/images (DECISIONS I-1494).
[Unit]
Description=Mount the base image cache volume on /var/lib/repose/images
After=repose-guest-pool.service repose-mac-grow.service
Wants=repose-guest-pool.service
Before=hostd.service
ConditionPathExists=!/run/repose/pool-hold

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/lib/repose/repose-images-volume

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-launcher-join.service <<'REPOSE_HOST_FILE'
[Unit]
Description=Take the join bundle from /run/repose/launcher (DECISIONS I-1380)
RequiresMountsFor=/run/repose/launcher
After=systemd-tmpfiles-setup.service
Before=repose-host-net.service repose-register.service hostd.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/lib/repose/repose-launcher-join

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-nftables.service <<'REPOSE_HOST_FILE'
# The `repose` nftables tables (docs/interfaces/host-conventions.md,
# nix/hosts/nftables.nix). Its own unit, not the distribution's
# nftables.service, whose /etc/nftables.conf flushes the whole ruleset:
# this one declares and flushes only its own chains, so hostd's chains,
# sets and counters survive a reload.
[Unit]
Description=repose guest isolation ruleset
DefaultDependencies=no
Before=network-pre.target hostd.service
Wants=network-pre.target
After=systemd-modules-load.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f /etc/repose/nftables.nft
ExecReload=/usr/sbin/nft -f /etc/repose/nftables.nft

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-register.service <<'REPOSE_HOST_FILE'
# One-shot registration before hostd: trades /run/repose/join-token for
# host.json, cert.pem and key.pem through `hostd register`. Skipped once
# host.json exists or with no token. A token the api rejects makes hostd
# exit 3 and the unit stops retrying; a network error retries every 30 s.
[Unit]
Description=Register this host with the api using the join token
Wants=network-online.target repose-launcher-join.service
After=network-online.target repose-host-net.service wg-quick@wg0.service repose-launcher-join.service
Before=hostd.service
ConditionPathExists=!/var/lib/repose/hostd/host.json
ConditionPathExists=/run/repose/join-token

[Service]
Type=oneshot
EnvironmentFile=/etc/repose/hostd.env
ExecStart=/usr/local/bin/hostd --state /var/lib/repose/hostd --join-token /run/repose/join-token $HOSTD_API_ARGS register
# The bridge and wg0 read host.json; apply it now.
ExecStartPost=/usr/bin/systemctl --no-block restart repose-host-net.service
Restart=on-failure
RestartSec=30
RestartPreventExitStatus=3
StateDirectory=repose/hostd
StateDirectoryMode=0700

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-snapshot.service <<'REPOSE_HOST_FILE'
[Unit]
Description=Nightly snapshot of every running guest

[Service]
Type=oneshot
ExecStart=/usr/local/bin/hostd snapshot-all
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-snapshot.timer <<'REPOSE_HOST_FILE'
[Unit]
Description=Nightly snapshot at 03:00 local time

[Timer]
OnCalendar=*-*-* 03:00:00
Persistent=false
RandomizedDelaySec=10min
Unit=repose-snapshot.service

[Install]
WantedBy=timers.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-snapshots-volume.service <<'REPOSE_HOST_FILE'
# hostd's snapshot store on vg-guests/snapshots (DECISIONS I-1636).
[Unit]
Description=Mount the snapshot store volume on /var/lib/repose/snapshots
After=repose-guest-pool.service repose-mac-grow.service
Wants=repose-guest-pool.service
Before=hostd.service
ConditionPathExists=!/run/repose/pool-hold

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/lib/repose/repose-snapshots-volume

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-zram.service <<'REPOSE_HOST_FILE'
# Compressed swap in memory for a host its user owns (DECISIONS I-1626):
# guests share the host's memory, and overflow is compressed into RAM
# before the OOM killer ends a guest.
[Unit]
Description=Compressed swap in memory for guests that share the host
After=systemd-modules-load.service
Before=hostd.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/lib/repose/repose-zram

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/virtiofsd-auth@.service.d/10-repose.conf <<'REPOSE_HOST_FILE'
[Unit]
Before=repose-guest-park.service
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/virtiofsd@.service.d/10-repose.conf <<'REPOSE_HOST_FILE'
[Unit]
Before=repose-guest-park.service
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/wg-quick@wg0.service.d/10-repose.conf <<'REPOSE_HOST_FILE'
# /etc/wireguard/wg0.conf is a link to /run/repose/wg0.conf, which
# repose-host-net renders from host.json or the join's hub.json.
[Unit]
ConditionPathExists=/run/repose/wg0.conf
After=repose-host-net.service
PartOf=repose-host-net.service
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/system/repose-guest-loop.service <<'REPOSE_HOST_FILE'
# vg-guests' PV is a file on this machine's one disk (DECISIONS I-1498).
[Unit]
Description=Attach the guest pool's file to a loop device
DefaultDependencies=no
After=local-fs.target
Before=repose-guest-pool.service

[Service]
Type=oneshot
RemainAfterExit=yes
EnvironmentFile=/etc/repose/pool.env
ExecStart=/usr/local/lib/repose/repose-guest-loop $REPOSE_POOL_FILE

[Install]
WantedBy=multi-user.target
REPOSE_HOST_FILE
  install_file 0644 /etc/lvm/profile/repose-thin.profile <<'REPOSE_HOST_FILE'
# vg-guests' metadata profile (vgchange --metadataprofile repose-thin):
# grow the pool by 5 percent into the VG headroom when it passes 80
# percent full (DECISIONS I-643, storage.nix). A profile and not lvm.conf,
# so a machine's own volume groups keep their settings.
activation {
  thin_pool_autoextend_threshold = 80
  thin_pool_autoextend_percent = 5
}
REPOSE_HOST_FILE
  install_file 0644 /etc/modules-load.d/repose.conf <<'REPOSE_HOST_FILE'
# nix/hosts/kernel.nix's boot.kernelModules, less kvm-intel (the kernel
# loads its KVM module for the CPU it finds).
vhost_vsock
vhost_net
tun
nf_tables
nf_conntrack
nft_connlimit
dm_thin_pool
dm_snapshot
overlay
bridge
sch_htb
sch_fq_codel
sch_ingress
cls_flower
act_police
act_gact
erofs
REPOSE_HOST_FILE
  install_file 0644 /etc/repose/nftables.nft <<'REPOSE_HOST_FILE'
#!/usr/sbin/nft -f
# The `repose` nftables tables (docs/interfaces/host-conventions.md),
# ported from nix/hosts/nftables.nix for hosts without Nix (DECISIONS
# I-1494). That file's comment explains every chain; the differences here:
# no build_out (no Nix builders run on these hosts), no cache rules (no
# npm or Docker Hub cache runs on them), no bootstrap ssh, and the provider
# NIC's name comes from /etc/repose/uplink.nft, and what reaches or
# crosses the host that is neither a guest's nor the mesh's is decided by
# /etc/repose/host-chains.nft: dropped on the Mac appliance, as on every
# repose host, and accepted on an x86 machine, whose own services and
# firewall are its owner's.
#
# Loaded by repose-nftables.service. The first part declares and flushes
# the chains this file owns; hostd's chains (guest_dyn, guest_smtp,
# guest_stratum, guest_flows), its sets' elements and its counters are
# never flushed by a reload.
include "/etc/repose/uplink.nft"

table inet repose {
  chain input { type filter hook input priority filter; policy drop; }
  chain output { type filter hook output priority filter; policy accept; }
  chain guest_fwd { type filter hook forward priority filter; policy drop; }
  chain guest_meter { type filter hook forward priority filter + 10; policy accept; }
  chain nat { type nat hook postrouting priority srcnat; policy accept; }
  chain guest_in { }
  chain host_input { }
  chain host_forward { }
  chain smtp_drop { }
  chain stratum_drop { }
  chain flows_drop { }
}
flush chain inet repose input
flush chain inet repose output
flush chain inet repose guest_fwd
flush chain inet repose guest_meter
flush chain inet repose nat
flush chain inet repose guest_in
flush chain inet repose smtp_drop
flush chain inet repose stratum_drop
flush chain inet repose flows_drop
flush chain inet repose host_input
flush chain inet repose host_forward
table bridge repose {
  chain forward { type filter hook forward priority filter; policy accept; }
  chain input { type filter hook input priority filter; policy drop; }
}
flush chain bridge repose forward
flush chain bridge repose input
include "/etc/repose/host-chains.nft"

table inet repose {
  chain guest_dyn {
  }
  chain guest_smtp {
  }
  chain guest_stratum {
  }
  chain guest_flows {
  }

  # One token bucket per guest address for new outbound flows (I-240).
  set guest_flow_rate {
    type ipv4_addr
    size 65535
    flags dynamic,timeout
    timeout 1m
  }
  # Each guest address's tracked connections (I-453).
  set guest_conns {
    type ipv4_addr
    size 65535
    flags dynamic
  }

  chain smtp_drop {
    jump guest_smtp
    counter drop
  }
  chain stratum_drop {
    jump guest_stratum
    counter drop
  }
  chain flows_drop {
    jump guest_flows
    counter drop
  }

  chain input {
    type filter hook input priority filter; policy drop;
    iifname "lo" accept
    iifname "br-guests" jump guest_in
    ct state established,related accept
    ct state invalid drop

    # The exporters and hostd's metrics, over WireGuard only.
    iifname "wg0" tcp dport { 9100, 9101 } accept
    iifname "wg0" icmp type echo-request accept

    # What else reaches the host is host_input's: closed on the Mac
    # appliance, the machine's own business on an x86 host.
    jump host_input
    counter drop
  }

  chain guest_in {
    ct direction reply ct state established,related accept
    icmp type echo-request limit rate 5/second accept
    counter drop
  }

  chain guest_fwd {
    type filter hook forward priority filter; policy drop;
    ct state established,related accept
    ct state invalid drop

    # The edge gateway relays SSH to guests over WireGuard.
    iifname "wg0" oifname "br-guests" ip daddr 10.64.0.0/12 tcp dport 22 accept

    iifname "br-guests" meta nfproto ipv6 counter drop
    iifname "br-guests" tcp dport 25 goto smtp_drop
    iifname "br-guests" tcp dport { 3333, 5555, 7777, 14433, 14444 } goto stratum_drop
    iifname "br-guests" ct state new add @guest_conns { ip saddr ct count over 16384 } goto flows_drop
    iifname "br-guests" ct state new update @guest_flow_rate { ip saddr limit rate over 200/second burst 2000 packets } goto flows_drop

    # Cloud instance metadata and the Azure wire server: never from a guest.
    iifname "br-guests" ip daddr { 169.254.169.254, 168.63.129.16 } counter drop
    # Other guests, on this host or any other.
    iifname "br-guests" ip daddr 10.64.0.0/12 counter drop
    # The edge: hook ingest, and nothing else on the mesh.
    iifname "br-guests" oifname "wg0" ip daddr 10.255.0.1 tcp dport 8443 accept
    # Everything private: the owner's LAN, the mesh, link-local.
    iifname "br-guests" ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, 169.254.0.0/16 } counter drop
    iifname "br-guests" oifname $uplink counter accept
    iifname "br-guests" counter drop
    oifname "br-guests" counter drop
    # Forwarding that is not a guest's is host_forward's (an x86 host's
    # own containers, say).
    jump host_forward
    counter drop
  }

  chain guest_meter {
    type filter hook forward priority filter + 10; policy accept;
    iifname "br-guests" oifname $uplink jump guest_dyn
  }

  chain nat {
    type nat hook postrouting priority srcnat; policy accept;
    oifname $uplink ip saddr 10.64.0.0/12 masquerade
  }

  chain output {
    type filter hook output priority filter; policy accept;
  }
}

table bridge repose {
  # hostd-owned: { <mac> . <ip> . tap-<8hex> } per running guest.
  set guests {
    type ether_addr . ipv4_addr . ifname
  }

  # No frame is ever switched between two guests. Only br-guests': an
  # x86 host's other bridges (Docker's, say) are not this table's.
  chain forward {
    type filter hook forward priority filter; policy accept;
    meta ibrname "br-guests" counter drop
  }

  chain input {
    type filter hook input priority filter; policy drop;
    iifname != "tap-*" accept
    ether type arp ether saddr . arp saddr ip . iifname @guests accept
    ether type ip ether saddr . ip saddr . iifname @guests accept
    counter drop
  }
}
REPOSE_HOST_FILE
  install_file 0644 /etc/sysctl.d/90-repose.conf <<'REPOSE_HOST_FILE'
# nix/hosts/kernel.nix's sysctls (DECISIONS I-1494); that file says why
# each is set. A leading - ignores a key whose module is not loaded yet.
kernel.panic_on_oops = 1
net.ipv4.ip_forward = 1
fs.inotify.max_user_instances = 8192
vm.overcommit_memory = 1
net.ipv4.ip_nonlocal_bind = 1
-net.netfilter.nf_conntrack_max = 1048576
-net.netfilter.nf_conntrack_tcp_timeout_established = 86400
-net.bridge.bridge-nf-call-iptables = 0
-net.bridge.bridge-nf-call-ip6tables = 0
-net.bridge.bridge-nf-call-arptables = 0
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/network/20-br-guests.netdev <<'REPOSE_HOST_FILE'
# No STP: taps never form loops (nix/hosts/network.nix).
[NetDev]
Kind=bridge
Name=br-guests

[Bridge]
STP=false
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/network/20-br-guests.network <<'REPOSE_HOST_FILE'
# The address is repose-host-net's drop-in, from host.json.
[Match]
Name=br-guests

[Network]
ConfigureWithoutCarrier=yes
DHCP=no
LinkLocalAddressing=no
IPv6AcceptRA=no

[Link]
RequiredForOnline=no
REPOSE_HOST_FILE
  install_file 0644 /etc/systemd/network/30-repose-unmanaged.network <<'REPOSE_HOST_FILE'
# hostd creates the taps and attaches them; wg-quick owns wg0.
[Match]
Name=tap-* wg0

[Link]
Unmanaged=yes
REPOSE_HOST_FILE
  install_file 0644 /etc/sysusers.d/repose.conf <<'REPOSE_HOST_FILE'
# nix/hosts/virt.nix's accounts: hostd runs the guest hypervisors and owns
# the taps; virtiofsd and repose-auth serve the shares and are in group
# hostd to reach their sockets (DECISIONS I-51, I-69, I-278).
g hostd -
u hostd -:hostd "repose guest hypervisors" /var/empty
m hostd kvm
u virtiofsd - "repose virtiofsd" /var/empty
m virtiofsd hostd
u repose-auth - "repose Claude login share" /var/empty
m repose-auth hostd
REPOSE_HOST_FILE
  install_file 0644 /etc/tmpfiles.d/repose.conf <<'REPOSE_HOST_FILE'
# The state directories of docs/interfaces/host-conventions.md.
d /run/repose 0755 root root -
d /run/repose/launcher 0700 root root -
d /run/repose/store-view 0755 root root -
d /var/lib/repose 0755 root root -
d /var/lib/repose/guests 0711 root root -
d /var/lib/repose/users 0711 root root -
d /var/lib/repose/images 0755 root root -
d /var/lib/repose/snapshots 0700 root root -
d /var/log/repose 0750 root root -
L /etc/wireguard/wg0.conf - - - - /run/repose/wg0.conf
d /var/lib/repose/hostd-update 0700 root root -
REPOSE_HOST_FILE
  install_file 0644 /etc/udev/rules.d/90-repose.rules <<'REPOSE_HOST_FILE'
# nix/hosts/virt.nix: the hostd account runs guest hypervisors.
KERNEL=="kvm", GROUP="kvm", MODE="0660"
KERNEL=="vhost-vsock", GROUP="kvm", MODE="0660"
KERNEL=="vhost-net", GROUP="kvm", MODE="0660"
# Guest volumes g-<id> in vg-guests are group hostd so an unprivileged
# guest@<id> can open its disk; snapshots and the pool stay root:disk.
SUBSYSTEM=="block", KERNEL=="dm-*", ENV{DM_VG_NAME}=="vg-guests", ENV{DM_LV_NAME}=="g-*", GROUP="hostd", MODE="0660"
REPOSE_HOST_FILE
  install_file 0644 /etc/repose/host-chains.nft <<'REPOSE_HOST_FILE'
# An x86 machine of the user's own (ops/install-host-x86.sh): what reaches
# the machine or crosses it that is not a guest's is its owner's (their
# ssh, their services, their containers), under whatever firewall they
# run. The repose tables close the guest bridge and the mesh only.
table inet repose {
  chain host_input {
    accept
  }
  chain host_forward {
    accept
  }
}
REPOSE_HOST_FILE
}
# END GENERATED HOST FILES

# ---- --uninstall ---------------------------------------------------------------
# Take repose off this machine (ENG-204, DECISIONS I-1590): the reverse of
# everything below, run as root, each step only where there is something
# to undo, so a second run changes nothing. The files the generated block
# writes are listed by running it with install_file recording instead of
# writing, so the two lists cannot drift apart.
#
# Refused while this machine is still a host on the account: hostd logs
# the api's "host is retired" (or "lost") at its first heartbeat after
# `repose host rm`, which is how this machine knows. --force skips that.
uninstall_host() {
  [ "$dry" = 1 ] || [ "$(id -u)" = 0 ] || die "run it as root (sudo bash -s -- --uninstall)."
  local hj=/var/lib/repose/hostd/host.json
  if [ -s "$hj" ] && [ "$force" = 0 ] &&
    ! journalctl -u hostd.service -o cat --no-pager 2>/dev/null | grep -E 'host is (retired|lost)' >/dev/null; then
    die "this machine is still a host on your account. Remove it there first with \`repose host rm <name>\` (the name \`repose host ls\` shows), wait 15 seconds for this machine to hear, and run this again. --force removes repose from here without that, and the host stays on your account, offline, until you remove it there."
  fi

  local files=() units=() f u pool_file="" pvs_list="" dev
  install_file() { cat > /dev/null; files+=("$2"); }
  write_host_files
  files+=(/etc/repose/release.pub /etc/repose/hostd.env /etc/repose/api-ca.pem
    /etc/repose/uplink.nft /etc/repose/hostd-update.env /etc/repose/pool.env
    /etc/apparmor.d/repose-virtiofsd /usr/local/bin/hostd /usr/local/bin/.hostd.new)
  for f in "${files[@]}"; do
    case $f in
      /etc/systemd/system/*.service | /etc/systemd/system/*.timer)
        [ -e "$f" ] && units+=("$(basename "$f")") ;;
    esac
  done
  pool_file=$(sed -n 's/^REPOSE_POOL_FILE=//p' /etc/repose/pool.env 2>/dev/null || true)
  if command -v pvs >/dev/null; then
    pvs_list=$(pvs --noheadings -o pv_name -S vg_name=vg-guests 2>/dev/null | tr -d ' ' || true)
  fi

  if [ "${#units[@]}" = 0 ] && [ ! -e /etc/repose ] && [ ! -e /var/lib/repose ] && [ -z "$pvs_list" ] &&
    [ ! -e /usr/local/bin/hostd ]; then
    say "repose is not on this machine."
    return 0
  fi

  # hostd and its timers first, so nothing starts a guest again; then the
  # guests, which with their host gone have nowhere else to be.
  # shellcheck disable=SC2329 # run through step
  quiet() { "$@" >/dev/null 2>&1 || true; }
  step "stop hostd and its timers" -- quiet systemctl stop repose-hostd-update.timer repose-snapshot.timer \
    repose-hostd-update.service repose-snapshot.service repose-register.service hostd.service
  local guests
  guests=$(systemctl list-units --all --plain --no-legend 'guest@*' 'virtiofsd@*' 'virtiofsd-auth@*' 2>/dev/null | awk '{ print $1 }')
  if [ -n "$guests" ]; then
    # shellcheck disable=SC2086 # one unit name a word
    step "stop the machines on this host" -- quiet systemctl stop $guests
  fi
  local mounts
  mounts=$(findmnt -rn -o TARGET 2>/dev/null | grep -E '^(/var/lib/repose|/run/repose)(/|$)' | sort -r || true)
  for f in $mounts; do
    # shellcheck disable=SC2016 # expanded by the inner bash
    step "unmount $f" -- bash -c 'umount "$1" 2>/dev/null || umount -l "$1"' _ "$f"
  done
  if [ "${#units[@]}" -gt 0 ]; then
    step "stop and disable ${units[*]}" -- bash -c 'systemctl disable --now -q "$@" 2>/dev/null || true' _ "${units[@]}"
  fi

  # The network: wg0 (only when its config is repose's link), the taps on
  # br-guests and the bridge, and the two repose tables.
  if [ "$(readlink /etc/wireguard/wg0.conf 2>/dev/null || true)" = /run/repose/wg0.conf ]; then
    step "stop wg0" -- bash -c 'systemctl stop wg-quick@wg0.service 2>/dev/null || true
      ip link show wg0 >/dev/null 2>&1 && ip link del wg0
      rm -f /etc/wireguard/wg0.conf'
  fi
  if ip link show br-guests >/dev/null 2>&1; then
    # shellcheck disable=SC2016 # expanded by the inner bash
    step "remove br-guests and its taps" -- bash -c '
      for t in $(ip -o link show master br-guests 2>/dev/null | awk -F": " "{ print \$2 }" | cut -d@ -f1); do
        case $t in tap-*) ip link del "$t" 2>/dev/null || true ;; esac
      done
      ip link del br-guests'
  fi
  for f in "inet repose" "bridge repose"; do
    # shellcheck disable=SC2086 # family and name, two words
    if command -v nft >/dev/null && nft list table $f >/dev/null 2>&1; then
      # shellcheck disable=SC2086
      step "delete the nftables table $f" -- nft delete table $f
    fi
  done

  # The pool: its volumes, the volume group, the physical volume's label,
  # then the loop device and its file. A --device disk keeps its bytes and
  # loses its signature, so the machine sees it as an empty disk.
  if [ -n "$pvs_list" ]; then
    step "remove vg-guests and its volumes" -- bash -c 'vgchange -q -an vg-guests >/dev/null 2>&1 || true; vgremove -q -ff -y vg-guests'
    for dev in $pvs_list; do
      # shellcheck disable=SC2016 # expanded by the inner bash
      step "release $dev" -- bash -c 'pvremove -q -ff -y "$1"; lvmdevices --deldev "$1" >/dev/null 2>&1 || true' _ "$dev"
    done
  fi
  if [ -n "$pool_file" ]; then
    for dev in $(losetup -j "$pool_file" -O NAME -n 2>/dev/null || true); do
      step "detach $dev" -- losetup -d "$dev"
    done
    if [ -e "$pool_file" ]; then
      step "delete $pool_file" -- rm -f -- "$pool_file"
    fi
  fi

  # The files, the binaries the installer fetched (by their pinned sha256,
  # so one the machine's owner put there is left), the state, the accounts.
  if [ -e /etc/apparmor.d/repose-virtiofsd ] && command -v apparmor_parser >/dev/null; then
    step "unload /etc/apparmor.d/repose-virtiofsd" -- quiet apparmor_parser -R /etc/apparmor.d/repose-virtiofsd
  fi
  local gone=()
  for f in "${files[@]}"; do
    [ -e "$f" ] && gone+=("$f")
  done
  local path sum
  for f in "/usr/local/bin/cloud-hypervisor $CH_SHA256" "/usr/local/bin/ch-remote $CHR_SHA256" "/usr/local/bin/virtiofsd $VIRTIOFSD_SHA256"; do
    read -r path sum <<< "$f"
    if [ -f "$path" ] && echo "$sum  $path" | sha256sum -c --quiet - >/dev/null 2>&1; then
      gone+=("$path")
    fi
  done
  if [ "${#gone[@]}" -gt 0 ]; then
    step "delete ${#gone[@]} files of repose's" -- rm -f -- "${gone[@]}"
  fi
  # The drop-in directories only when nothing of anyone else's is in them.
  for f in /etc/systemd/system/guest@.service.d /etc/systemd/system/virtiofsd@.service.d \
    /etc/systemd/system/virtiofsd-auth@.service.d /etc/systemd/system/wg-quick@wg0.service.d; do
    if [ -d "$f" ] && [ -z "$(ls -A "$f")" ]; then
      step "delete $f" -- rmdir -- "$f"
    fi
  done
  for f in /usr/local/lib/repose /etc/repose /run/systemd/network/20-br-guests.network.d \
    /var/lib/repose /run/repose /var/log/repose; do
    if [ -e "$f" ]; then
      step "delete $f" -- rm -rf --one-file-system -- "$f"
    fi
  done
  for u in repose-auth virtiofsd hostd; do
    if getent passwd "$u" >/dev/null; then
      step "delete the account $u" -- userdel "$u"
    fi
  done
  if getent group hostd >/dev/null; then
    step "delete the group hostd" -- groupdel hostd
  fi
  if [ "$dry" = 1 ]; then
    echo "would: reload systemd, udev and networkd"
  else
    echo "==> reload systemd, udev and networkd"
    systemctl daemon-reload
    systemctl reset-failed >/dev/null 2>&1 || true
    udevadm control --reload >/dev/null 2>&1 || true
    networkctl reload >/dev/null 2>&1 || true
  fi
  say "repose is off this machine. The apt packages it installed stay, and its kernel settings (IP forwarding among them) last until the next boot."
}

if [ "$uninstall" = 1 ]; then
  uninstall_host
  exit 0
fi

# ---- the host's files -------------------------------------------------------
write_host_files

# ---- Cloud Hypervisor and hostd -------------------------------------------
fetch_verified "$CH_BASE/cloud-hypervisor-static" "$CH_SHA256" /usr/local/bin/cloud-hypervisor 0755
fetch_verified "$CH_BASE/ch-remote-static" "$CHR_SHA256" /usr/local/bin/ch-remote 0755
if [ -f /usr/local/bin/virtiofsd ] && [ ! -L /usr/local/bin/virtiofsd ] &&
  echo "$VIRTIOFSD_SHA256  /usr/local/bin/virtiofsd" | sha256sum -c --quiet - 2>/dev/null; then
  :
elif [ "$dry" = 1 ]; then
  echo "would: fetch virtiofsd $VIRTIOFSD_VERSION ($VIRTIOFSD_ZIP, sha256 $VIRTIOFSD_SHA256) to /usr/local/bin/virtiofsd"
else
  echo "==> fetch virtiofsd $VIRTIOFSD_VERSION"
  curl -fsSL --retry 3 -o "$tmp/virtiofsd.zip" "$VIRTIOFSD_ZIP"
  echo "$VIRTIOFSD_ZIP_SHA256  $tmp/virtiofsd.zip" | sha256sum -c --quiet - || die "$VIRTIOFSD_ZIP does not match its pinned sha256"
  unzip -q -o -j "$tmp/virtiofsd.zip" target/x86_64-unknown-linux-musl/release/virtiofsd -d "$tmp/virtiofsd"
  echo "$VIRTIOFSD_SHA256  $tmp/virtiofsd/virtiofsd" | sha256sum -c --quiet - || die "virtiofsd in $VIRTIOFSD_ZIP does not match its pinned sha256"
  # Before an installer that linked the distribution's, this was a link.
  rm -f /usr/local/bin/virtiofsd
  install -m 0755 "$tmp/virtiofsd/virtiofsd" /usr/local/bin/virtiofsd
  changed+=(/usr/local/bin/virtiofsd)
fi
# virtiofsd sandboxes itself in an unprivileged user namespace
# (--sandbox namespace). Ubuntu 24.04 lets only a binary with an AppArmor
# profile that says `userns` make one, so without this every share fails
# with "Error entering sandbox: CleanMount(... Permission denied)". The
# profile is Ubuntu's own shape for such binaries (crun's): unconfined
# otherwise, so it names the binary and changes nothing else. Only where
# the restriction exists: an older AppArmor does not parse the rule.
if [ "$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null || echo 0)" = 1 ]; then
  cat <<'PROFILE' | install_file 0644 /etc/apparmor.d/repose-virtiofsd
# repose (ops/install-host-x86.sh, DECISIONS I-1540): virtiofsd may make
# the user namespace its sandbox needs; nothing else is confined.
abi <abi/4.0>,
include <tunables/global>

profile repose-virtiofsd /usr/local/bin/virtiofsd flags=(unconfined) {
  userns,

  include if exists <local/repose-virtiofsd>
}
PROFILE
  if [ "$dry" = 1 ]; then
    echo "would: load /etc/apparmor.d/repose-virtiofsd"
  else
    step "load /etc/apparmor.d/repose-virtiofsd" -- apparmor_parser -r -W /etc/apparmor.d/repose-virtiofsd
  fi
fi
# The login share's unit passes --translate-uid (internal/hostd/virtiofs);
# a virtiofsd without it exits 2 at every guest start and the guest boots
# without the share, which nothing else would say.
if [ "$dry" = 0 ] && ! /usr/local/bin/virtiofsd --help 2>&1 | grep -q -- '--translate-uid'; then
  die "/usr/local/bin/virtiofsd has no --translate-uid; the Claude login share needs it. Nothing is enabled."
fi
printf '%s\n' "$RELEASE_PUBKEY" > "$tmp/release.pub"
install_file 0644 /etc/repose/release.pub < "$tmp/release.pub"
if [ -n "$hostd_binary" ]; then
  install_file 0755 /usr/local/bin/hostd < "$hostd_binary"
elif [ "$dry" = 1 ]; then
  echo "would: fetch hostd from ${manifest_url:-the newest hostd-<version> release}, check its signature and sha256, and install it as /usr/local/bin/hostd"
else
  echo "==> fetch hostd from ${manifest_url:-the newest hostd-<version> release}"
  # The update script does the fetch and both checks; it is the same one
  # the timer runs every 30 minutes.
  install -d /usr/local/bin
  REPOSE_RELEASE_PUBKEY_FILE=$tmp/release.pub REPOSE_HOSTD_BIN=/usr/local/bin/hostd REPOSE_HOSTD_RESTART=true \
    REPOSE_HOSTD_MANIFEST_URL=$manifest_url /usr/local/lib/repose/repose-hostd-update ||
    die "hostd did not install; nothing is registered yet, and running this again continues."
  # A lookup GitHub refused (its anonymous rate limit) installs nothing
  # and is not an error for the timer; here it is.
  [ -x /usr/local/bin/hostd ] ||
    die "hostd did not install: GitHub's releases API refused the lookup. Nothing is registered yet; run this again in an hour, or pass --manifest-url."
fi

# A hostd from before DECISIONS I-1626 refuses a flag it does not know, so
# the flag goes in only when the hostd just installed takes it.
owned_flag=""
if /usr/local/bin/hostd --help 2>&1 | grep -q -- '-owned-host'; then
  owned_flag="--owned-host "
fi
{
  echo "# hostd's flags on an x86 host (ops/install-host-x86.sh, DECISIONS I-1498)."
  if [ -n "$api" ]; then
    echo "HOSTD_API_ARGS=--api-addr $api${api_name:+ --api-server-name $api_name} --api-ca /etc/repose/api-ca.pem"
  else
    echo "HOSTD_API_ARGS=--api-addr $API_ADDR --api-server-name $API_SERVER_NAME --api-ca /etc/repose/api-ca.pem"
  fi
  echo "# Snapshots on its own disk (DECISIONS I-1372); no park: an x86 host has"
  echo "# no launcher and no Key Vault to wrap the image key with. The machine is"
  echo "# its user's own: guests share its memory and every vCPU (I-1626, I-1628)."
  echo "HOSTD_ARGS=${owned_flag}--snapshot-dir /var/lib/repose/snapshots"
} | install_file 0644 /etc/repose/hostd.env
if [ -n "$api_ca_file" ]; then
  install_file 0644 /etc/repose/api-ca.pem < "$api_ca_file"
else
  printf '%s\n' "$API_CA" | install_file 0644 /etc/repose/api-ca.pem
fi
printf 'define uplink = "%s"\n' "$uplink" | install_file 0644 /etc/repose/uplink.nft
if [ -n "$manifest_url" ]; then
  printf 'url=%s\n' "$manifest_url" | install_file 0644 /etc/repose/hostd-update.env
fi

if [ "$dry" = 0 ]; then
  step "create the accounts, directories and device rules" -- bash -c '
    systemd-sysusers /etc/sysusers.d/repose.conf
    systemd-tmpfiles --create /etc/tmpfiles.d/repose.conf
    udevadm control --reload && udevadm trigger --subsystem-match=misc --subsystem-match=block --action=change
    systemctl restart systemd-modules-load.service
    sysctl -q --system'
  step "reload systemd" -- systemctl daemon-reload
else
  echo "would: create the accounts, directories and device rules; load the modules and sysctls; reload systemd"
fi

# ---- the guest pool ----------------------------------------------------------
if vgs vg-guests >/dev/null 2>&1 && lvs vg-guests/thin >/dev/null 2>&1; then
  say "guest pool: vg-guests/thin present"
  if [ -n "$loop_file" ] && [ -e "$loop_file" ]; then
    printf 'REPOSE_POOL_FILE=%s\n' "$loop_file" | install_file 0644 /etc/repose/pool.env
    step "enable the pool's loop device at boot" -- systemctl enable repose-guest-loop.service
  fi
elif [ -n "$device" ]; then
  step "make vg-guests and its thin pool on $device" -- /usr/local/lib/repose/repose-pool-create "$device"
else
  if [ ! -e "$loop_file" ]; then
    # shellcheck disable=SC2016 # expanded by the inner bash
    step "make $loop_file, ${size} GiB, sparse" -- bash -c 'mkdir -p "$(dirname "$1")" && truncate -s "$2G" "$1"' _ "$loop_file" "$size"
  fi
  printf 'REPOSE_POOL_FILE=%s\n' "$loop_file" | install_file 0644 /etc/repose/pool.env
  step "attach $loop_file at every boot" -- systemctl enable --now repose-guest-loop.service
  if [ "$dry" = 1 ]; then
    echo "would: make vg-guests and its thin pool on the loop device of $loop_file"
  else
    dev=$(losetup -j "$loop_file" -O NAME -n | head -1)
    [ -n "$dev" ] || die "$loop_file did not attach to a loop device"
    step "make vg-guests and its thin pool on $dev" -- /usr/local/lib/repose/repose-pool-create "$dev"
  fi
fi

# ---- the join ---------------------------------------------------------------
if [ "$registered" = 1 ]; then
  say "registered already (/var/lib/repose/hostd/host.json); the join argument is not used again"
elif [ "$dry" = 1 ]; then
  echo "would: write the join argument to /run/repose/launcher/join (0600) for repose-launcher-join"
else
  echo "==> write the join argument to /run/repose/launcher/join"
  install -d -m 0700 /run/repose/launcher
  (umask 077 && printf '%s\n' "$join" > /run/repose/launcher/join)
  # The file the argument came in is spent now: the copy above is the one
  # registration reads, and the token works once (ENG-206).
  if [ -n "$join_file" ]; then
    rm -f -- "$join_file" || say "could not delete $join_file; the argument in it works only for this install."
  fi
fi

# ---- the units -----------------------------------------------------------------
units=(systemd-networkd.service repose-nftables.service repose-fault-around.service repose-zram.service
  repose-guests-slice.service repose-guest-pool.service repose-images-volume.service repose-snapshots-volume.service repose-launcher-join.service
  repose-host-net.service repose-register.service hostd.service
  repose-snapshot.timer repose-hostd-update.timer)
if [ "$dry" = 1 ]; then
  echo "would: systemctl enable --now ${units[*]}"
else
  echo "==> systemctl enable --now ${units[*]}"
  systemctl enable -q systemd-networkd.service "${units[@]:1}"
  systemctl start systemd-networkd.service repose-nftables.service
  # A changed ruleset, script or unit takes effect now; hostd's restart
  # keeps its guests (KillMode=process).
  for f in "${changed[@]}"; do
    case $f in
      /etc/repose/nftables.nft | /etc/repose/host-chains.nft | /etc/repose/uplink.nft) systemctl reload repose-nftables.service ;;
    esac
  done
  systemctl start repose-fault-around.service || say "warning: fault-around could not be set (no debugfs?); see docs/ops for I-1040"
  systemctl start repose-zram.service || say "warning: no compressed swap (zram): guests that overflow memory are stopped rather than slowed"

  systemctl start repose-guests-slice.service repose-guest-pool.service repose-images-volume.service repose-snapshots-volume.service repose-launcher-join.service repose-host-net.service
  systemctl start --no-block repose-register.service
  restart_hostd=0
  for f in "${changed[@]}"; do
    case $f in /usr/local/bin/hostd | /etc/repose/hostd.env | /etc/repose/api-ca.pem | /etc/systemd/system/hostd.service) restart_hostd=1 ;; esac
  done
  if [ "$restart_hostd" = 1 ] && systemctl is-active -q hostd.service; then
    systemctl restart hostd.service
  else
    systemctl start hostd.service
  fi
  systemctl start repose-snapshot.timer repose-hostd-update.timer
  if [ "$registered" = 1 ]; then
    say "hostd is $(systemctl is-active hostd.service)."
  else
    say "hostd is $(systemctl is-active hostd.service); it registers through the hub and joins your account in a minute or two."
  fi
fi
if [ -n "$kernel_note" ]; then
  say "$kernel_note"
fi
