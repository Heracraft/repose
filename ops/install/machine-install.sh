#!/usr/bin/env bash
# machine-install.sh: installs repose on a machine the user owns
# (docs/workstreams/SPINE.md "Contract 5", DECISIONS I-812, I-814).
# `repose machine add` copies this script to the box and runs it over an
# ssh session; a self-hoster runs the same file by hand.
#
#   machine-install.sh --name NAME [--undo-gb N] [--device PATH]
#                      [--agent-version TAG]
#                      [--agent PATH | --agent-url URL] [--agent-sha256 HEX]
#                      [--versions PATH]
#
# The seven steps of the contract, in order, each idempotent: a second run
# against the same box changes nothing and says so. Each step prints one
# line to stderr (step 5 one per agent, since it is the minute-long one);
# the last line on stdout is the JSON result, and nothing else goes there.
# An error is one sentence naming what is missing, in install.sh's voice.
#
# It runs as the box's own login user and reaches for `sudo -n` where it
# needs root, so a password prompt never waits on a session with no
# terminal. That login user owns the store, which together with
# user_subvol_rm_allowed is what lets the agent delete its own points
# without sudo (docs/research/wrapper-undo.md).
set -euo pipefail

# The GitHub repository the releases live in (DECISIONS I-98, I-286).
REPO="heracraft/repose"

# The release signing key (DECISIONS I-430), as install.sh carries it:
# release.yml signs each release's checksums.txt with its private half,
# and nothing here installs a release whose checksums.txt.sig does not
# verify against this key. Every release that carries a repose-agent is a
# signed one, so there is no table of pre-signing checksums beside it.
RELEASE_PUBKEY='-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEq1BgpU75F46qQ3iMHL1E0VOqye+4
3/u1m873IxGrYRM2AZyYkK6QqN92G2qBuMvUi+qb4OYeF7KRF1MQ5oiIdw==
-----END PUBLIC KEY-----'

prefix=/opt/repose
store=/var/lib/repose/undo
image=/var/lib/repose/undo.img
etcdir=/etc/repose

# The five agents of nix/overlay/agents/default.nix, in its order, with
# the binary each installs. An agent with no x86_64-linux entry in the
# pins below is named and skipped; the rest still go on.
AGENTS="claude-code opencode codex gemini-cli pi-coding-agent"
binary_of() {
  case $1 in
    claude-code) echo claude ;;
    opencode) echo opencode ;;
    codex) echo codex ;;
    gemini-cli) echo gemini ;;
    pi-coding-agent) echo pi ;;
  esac
}

name=""
undo_gb=40
device=""
agent_version="latest"
agent_path=""
agent_url=""
agent_sha=""
versions=""

usage() {
  echo "usage: machine-install.sh --name NAME [--undo-gb N] [--device PATH] [--agent-version TAG] [--agent PATH | --agent-url URL] [--agent-sha256 HEX] [--versions PATH]" >&2
  exit 2
}

# install.sh's flag style: --flag value, and --flag=value for the same flag.
while [ $# -gt 0 ]; do
  case "$1" in
    --name) name="${2:-}"; shift ;;
    --name=*) name="${1#--name=}" ;;
    --undo-gb) undo_gb="${2:-}"; shift ;;
    --undo-gb=*) undo_gb="${1#--undo-gb=}" ;;
    --device) device="${2:-}"; shift ;;
    --device=*) device="${1#--device=}" ;;
    --agent-version) agent_version="${2:-}"; shift ;;
    --agent-version=*) agent_version="${1#--agent-version=}" ;;
    --agent) agent_path="${2:-}"; shift ;;
    --agent=*) agent_path="${1#--agent=}" ;;
    --agent-url) agent_url="${2:-}"; shift ;;
    --agent-url=*) agent_url="${1#--agent-url=}" ;;
    --agent-sha256) agent_sha="${2:-}"; shift ;;
    --agent-sha256=*) agent_sha="${1#--agent-sha256=}" ;;
    --versions) versions="${2:-}"; shift ;;
    --versions=*) versions="${1#--versions=}" ;;
    *) usage ;;
  esac
  shift
done

die() {
  echo "repose: $1" >&2
  exit 1
}

step() {
  echo "$1" >&2
}

# The same name internal/cli's machineNameRE takes: it is the Host alias
# the laptop reaches the box by.
case "$name" in
  "") usage ;;
  [!a-z0-9]* | *[!a-z0-9-]*) die "the machine name \"$name\" is not lower-case letters, digits and dashes" ;;
esac
case "$undo_gb" in
  "" | *[!0-9]*) die "--undo-gb takes a whole number of gigabytes, not \"$undo_gb\"" ;;
esac
if [ -n "$agent_path" ] && [ -n "$agent_url" ]; then
  die "--agent and --agent-url name two different binaries; pass one"
fi
# A repose-agent that does not come from a signed release is installed
# only against a hash the caller already holds, never against one fetched
# beside the file (DECISIONS I-430).
if [ -n "$agent_path" ] || [ -n "$agent_url" ]; then
  if [ -z "$agent_sha" ]; then
    die "--agent and --agent-url need --agent-sha256 HEX; without it nothing is verified"
  fi
fi
if [ -n "$agent_sha" ]; then
  case "$agent_sha" in
    *[!0-9a-f]*) die "--agent-sha256 is not a sha256: \"$agent_sha\"" ;;
  esac
  if [ ${#agent_sha} -ne 64 ]; then
    die "--agent-sha256 is not a sha256: \"$agent_sha\""
  fi
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

am_root=0
if [ "$(id -u)" = 0 ]; then
  am_root=1
fi
login=${SUDO_USER:-$(id -un)}

root_run() {
  if [ "$am_root" = 1 ]; then
    "$@"
  else
    sudo -n "$@"
  fi
}

# write_root <mode> <path>: the file on stdin, installed there as root.
write_root() {
  cat > "$tmp/staged"
  root_run install -D -m "$1" -o root -g root "$tmp/staged" "$2"
  rm -f "$tmp/staged"
}

sha256_of() {
  sha256sum "$1" | cut -d' ' -f1
}

# The hashes in versions.json are nix's SRI form: base64 of the digest.
sri_hex() {
  printf '%s' "${1#sha256-}" | base64 -d | od -An -vtx1 | tr -d ' \n'
}

# fetch <url> <sri hash> <destination>: download and verify, or stop.
fetch() {
  local want
  curl -fsSL "$1" -o "$3" || die "could not download $1"
  want=$(sri_hex "$2")
  if [ "$(sha256_of "$3")" != "$want" ]; then
    die "$1 does not match the sha256 pinned for it; refusing to install"
  fi
}

changed=0

# ---------------------------------------------------------------- step 1
# Check the box: the architecture the agent pins are built for, systemd,
# sudo without a password, and a kernel with btrfs.
arch=$(uname -m)
if [ "$arch" != x86_64 ]; then
  die "this box is $arch; repose installs on x86_64 only"
fi
if [ ! -d /run/systemd/system ]; then
  die "this box is not running systemd; repose mounts the undo store with a systemd unit"
fi
if [ "$am_root" != 1 ]; then
  command -v sudo >/dev/null 2>&1 ||
    die "there is no sudo on this box; repose needs root to mount the undo store"
  sudo -n true 2>/dev/null ||
    die "$login cannot run sudo without a password here; repose installs over a session with no terminal.
Allow it with: echo '$login ALL=(ALL) NOPASSWD:ALL' | sudo tee /etc/sudoers.d/repose-$login && sudo chmod 0440 /etc/sudoers.d/repose-$login"
fi
if ! grep -qw btrfs /proc/filesystems 2>/dev/null; then
  root_run modprobe btrfs 2>/dev/null || true
  grep -qw btrfs /proc/filesystems 2>/dev/null ||
    die "this kernel has no btrfs; a rewind point is a btrfs subvolume (docs/research/wrapper-undo.md)"
fi

# discard=async is what gives a deleted point's blocks back to the box:
# without it btrfs frees them inside the store and the sparse image stays
# as large as it ever grew. btrfs has had it since Linux 5.6, and an
# older kernel refuses the whole mount rather than the one option, so it
# goes on only where it is understood. internal/undo makes the same
# choice for the mount it does itself; the two must move together.
discard_opt=""
kver=$(uname -r)
kmaj=${kver%%.*}
krest=${kver#*.}
kmin=${krest%%.*}
case "$kmaj:$kmin" in
  *[!0-9]*:* | *:*[!0-9]*) ;;
  *) if [ "$kmaj" -gt 5 ] || { [ "$kmaj" -eq 5 ] && [ "$kmin" -ge 6 ]; }; then
       discard_opt=",discard=async"
     fi ;;
esac

# The store must not promise the box space the box has not got. The image
# is sparse, so truncate succeeds at any size and btrfs then reports the
# whole of it as free; the first point past the real free space fails with
# ENOSPC and nothing above it knows why. So the size is capped at what the
# backing filesystem has, less a margin for the box's own work, and a box
# with less than the floor is refused outright rather than given an undo
# store that cannot hold a rewind.
undo_margin_gb=2
undo_floor_gb=5
if [ -z "$device" ] && [ ! -e "$image" ]; then
  probe=$(dirname "$image")
  while [ ! -d "$probe" ] && [ "$probe" != / ]; do
    probe=$(dirname "$probe")
  done
  avail_gb=$(df -Pk "$probe" | awk 'NR==2 {printf "%d", $4 / 1048576}')
  case "$avail_gb" in
    "" | *[!0-9]*) avail_gb="" ;;  # a df this script cannot read is not a refusal
  esac
  if [ -n "$avail_gb" ]; then
    usable_gb=$((avail_gb - undo_margin_gb))
    if [ "$usable_gb" -lt "$undo_floor_gb" ]; then
      die "$probe has $avail_gb GB free and the undo store needs at least $((undo_floor_gb + undo_margin_gb)) GB; free some space or pass --device PATH"
    fi
    if [ "$undo_gb" -gt "$usable_gb" ]; then
      undo_cap_note="Capped the undo store at $usable_gb GB, not $undo_gb: $probe has $avail_gb GB free"
      undo_gb=$usable_gb
    fi
  fi
fi
os=$arch
if [ -r /etc/os-release ]; then
  # shellcheck disable=SC1091
  os=$(. /etc/os-release && printf '%s' "${PRETTY_NAME:-${NAME:-$arch}}")
fi
step "Checked the box ($os, $arch, systemd, btrfs)"
if [ -n "${undo_cap_note:-}" ]; then
  step "$undo_cap_note"
fi

# ---------------------------------------------------------------- step 2
# What the agent and the wrappers need from the distribution. ripgrep is
# in the list because the agent packages put it on PATH and Codex refuses
# a package without codex-path/rg (DECISIONS I-487); gawk because the
# sync helpers use GNU extensions mawk has not got, and Debian and Ubuntu
# ship mawk as `awk`.
#
# The manager is only detected here, not demanded: a box that already has
# everything needs none, and refusing such a box for the want of apt was
# a refusal with nothing behind it.
pm=""
if command -v apt-get >/dev/null 2>&1; then
  pm=apt
elif command -v dnf >/dev/null 2>&1; then
  pm=dnf
elif command -v pacman >/dev/null 2>&1; then
  pm=pacman
fi

NEEDED="mkfs.btrfs tmux git curl jq rsync rg gawk python3 tar openssl"

# gh is wanted, not needed: it is how an agent on the box pushes and
# opens a pull request, and Debian 12 has no gh package at all. So it is
# installed where the distribution has it and named where it does not,
# rather than failing an install over it.
WANTED="gh"

# link_on_path <name>: a symlink in /usr/local/bin, which is on the
# default PATH of a login shell, a non-interactive ssh command and a tmux
# window alike. /etc/profile.d/repose.sh puts $prefix/bin on PATH for a
# shell the user logs in with, but tmux gives a new window the
# environment its *server* started with, so an agent started in a window
# would not be found (measured on a Debian 12 box, 2026-10-19). A
# standard place on the standard PATH is what makes the agents reachable
# everywhere, and it is what any installed program does.
link_on_path() {
  _name=$1
  _want=$prefix/bin/$_name
  if [ "$(readlink "/usr/local/bin/$_name" 2>/dev/null || true)" = "$_want" ]; then
    return 1
  fi
  root_run ln -sfn "$_want" "/usr/local/bin/$_name"
  return 0
}

# have_cmd <command>: on PATH, or in a sbin directory a login shell leaves
# out. This script runs as the box's login user, and Debian keeps
# mkfs.btrfs in /usr/sbin, so `command -v` alone reports a package that is
# installed as missing and then reports it again as not on PATH. root_run
# finds it: sudo's secure_path carries the sbin directories.
have_cmd() {
  if command -v "$1" >/dev/null 2>&1; then
    return 0
  fi
  for d in /usr/local/sbin /usr/sbin /sbin; do
    if [ -x "$d/$1" ]; then
      return 0
    fi
  done
  return 1
}

# package_of <command>: the package that carries it, for this manager.
package_of() {
  case "$1" in
    mkfs.btrfs) echo btrfs-progs ;;
    rg) echo ripgrep ;;
    python3) if [ "$pm" = pacman ]; then echo python; else echo python3; fi ;;
    *) echo "$1" ;;
  esac
}

# pm_install <package>...: install them with this box's manager.
pm_install() {
  case "$pm" in
    apt) root_run env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "$@" ;;
    dnf) root_run dnf install -y -q "$@" ;;
    pacman) root_run pacman -S --needed --noconfirm "$@" ;;
  esac
}

wanted=""
for cmd in $NEEDED; do
  if have_cmd "$cmd"; then
    continue
  fi
  pkg=$(package_of "$cmd")
  case " $wanted " in
    *" $pkg "*) ;;
    *) wanted="${wanted:+$wanted }$pkg" ;;
  esac
done
if [ -n "$wanted" ] && [ -z "$pm" ]; then
  die "this box has no apt, dnf or pacman, and it is missing $wanted; install those yourself and run this again"
fi
missing_wanted=""
for cmd in $WANTED; do
  have_cmd "$cmd" || missing_wanted="${missing_wanted:+$missing_wanted }$(package_of "$cmd")"
done

# claude-code is a dynamically linked bun binary that needs
# libasound.so.2, which its nix package carries as alsa-lib. The library's
# package name moved with Debian's 64-bit time transition, so the
# candidates are tried in turn and a box that has none of them is told
# what is missing rather than left with a claude that will not start.
case "$pm" in
  apt) alsa_pkgs="libasound2t64 libasound2" ;;
  *) alsa_pkgs="alsa-lib" ;;
esac
# ldconfig lives in /usr/sbin too, so ask for it the way have_cmd does.
# Without this the check fails on every run, the install below is a no-op
# that apt still exits 0 on, and the result claims the box changed.
ldconfig_bin=$(command -v ldconfig 2>/dev/null || true)
if [ -z "$ldconfig_bin" ]; then
  for d in /usr/local/sbin /usr/sbin /sbin; do
    if [ -x "$d/ldconfig" ]; then
      ldconfig_bin=$d/ldconfig
      break
    fi
  done
fi
has_alsa() {
  [ -n "$ldconfig_bin" ] || return 0
  "$ldconfig_bin" -p 2>/dev/null | grep -q 'libasound\.so\.2'
}
have_alsa=1
has_alsa || have_alsa=0

if [ "$pm" = apt ] && { [ -n "$wanted" ] || [ -n "$missing_wanted" ] || [ "$have_alsa" = 0 ]; }; then
  root_run env DEBIAN_FRONTEND=noninteractive apt-get update -qq ||
    die "apt-get update failed; this box cannot reach its package mirrors"
fi
if [ -n "$wanted" ]; then
  # shellcheck disable=SC2086
  pm_install $wanted || die "$pm could not install $wanted"
  for cmd in $NEEDED; do
    have_cmd "$cmd" ||
      die "$(package_of "$cmd") is installed but $cmd is not on PATH"
  done
  changed=1
  step "Installed $wanted"
else
  step "The packages are installed"
fi
# The wanted-but-not-needed packages, each on its own: a distribution
# without one must not take the others down with it. The package's name
# is also the command's for everything in WANTED, so the command is what
# decides whether the install worked — a manager that exits 0 on a
# package it did not place is otherwise reported as a success.
for pkg in $missing_wanted; do
  if [ -n "$pm" ] && pm_install "$pkg" >/dev/null 2>&1 && have_cmd "$pkg"; then
    changed=1
    step "Installed $pkg"
  else
    step "This box has no $pkg package; the agents work without it"
  fi
done

if [ "$have_alsa" = 0 ]; then
  for pkg in $alsa_pkgs; do
    pm_install "$pkg" >/dev/null 2>&1 || continue
    # The library, not the package manager's exit status: installing an
    # already-installed package succeeds and changes nothing.
    if has_alsa; then
      have_alsa=1
      changed=1
      break
    fi
  done
  if [ "$have_alsa" = 0 ]; then
    step "No package here provides libasound.so.2, which claude needs"
  fi
fi

# ---------------------------------------------------------------- step 3
# The undo store: btrfs in a sparse file, or on a device the user named,
# mounted by a unit so it comes back after a restart. The loopback file is
# the decision of I-814: it costs 3 percent on a sequential write, and it
# is what makes the ask a login rather than a disk.
store_changed=0
root_run mkdir -p /var/lib/repose "$store"
unit=$(systemd-escape -p --suffix=mount "$store")
if [ -n "$device" ]; then
  if [ ! -b "$device" ]; then
    die "$device is not a block device"
  fi
  store_source=$device
  options="noatime,compress=zstd:1,user_subvol_rm_allowed$discard_opt"
else
  store_source=$image
  options="loop,noatime,compress=zstd:1,user_subvol_rm_allowed$discard_opt"
  if [ ! -e "$image" ]; then
    root_run truncate -s "${undo_gb}G" "$image"
    root_run chmod 0600 "$image"
    store_changed=1
  fi
fi

if ! mountpoint -q "$store"; then
  # mkfs.btrfs without -f refuses a source that already holds a
  # filesystem, so a wrong answer from the probe cannot take the points
  # with it.
  fstype=$(root_run blkid -p -s TYPE -o value "$store_source" 2>/dev/null || true)
  case "$fstype" in
    btrfs) ;;
    "")
      # >&2: stdout carries the one result object and nothing else, and
      # mkfs.btrfs prints its layout however quiet it is asked to be.
      root_run mkfs.btrfs -q -L repose-undo "$store_source" >&2 ||
        die "mkfs.btrfs refused $store_source; it is not empty"
      store_changed=1
      ;;
    *) die "$store_source already holds a $fstype filesystem; repose will not overwrite it" ;;
  esac
fi

unit_text="[Unit]
Description=repose undo store
RequiresMountsFor=$(dirname "$store_source")

[Mount]
What=$store_source
Where=$store
Type=btrfs
Options=$options

[Install]
WantedBy=local-fs.target
"
if [ "$(cat "/etc/systemd/system/$unit" 2>/dev/null || true)" != "$(printf '%s' "$unit_text")" ]; then
  printf '%s' "$unit_text" | write_root 0644 "/etc/systemd/system/$unit"
  root_run systemctl daemon-reload
  store_changed=1
fi
if ! root_run systemctl is-enabled --quiet "$unit" 2>/dev/null; then
  root_run systemctl enable "$unit" >/dev/null 2>&1 ||
    die "systemctl could not enable $unit"
  store_changed=1
fi
if ! root_run systemctl is-active --quiet "$unit" 2>/dev/null; then
  root_run systemctl start "$unit" ||
    die "$unit could not mount $store_source on $store; journalctl -u $unit says why"
  store_changed=1
fi

# The layout internal/undo works in: one subvolume per project's live area
# under projects/, the read-only points under points/.
for sub in projects points; do
  if ! root_run btrfs subvolume show "$store/$sub" >/dev/null 2>&1; then
    root_run btrfs subvolume create "$store/$sub" >/dev/null ||
      die "could not create the $sub subvolume in $store"
    store_changed=1
  fi
done
group=$(id -gn "$login")
for path in "$store" "$store/projects" "$store/points"; do
  if [ "$(stat -c '%U' "$path")" != "$login" ]; then
    root_run chown "$login:$group" "$path"
    store_changed=1
  fi
done

size=$(df -h --output=size "$store" | tail -1 | tr -d ' ')
if [ "$store_changed" = 1 ]; then
  changed=1
  step "Provisioned $size of undo space (btrfs at $store)"
else
  step "The undo space is provisioned ($size of btrfs at $store)"
fi

# ---------------------------------------------------------------- step 4
# repose-agent, the command the CLI runs over ssh (SPINE.md contract 3),
# and the PATH a login shell and a tmux window get: the guest convention
# of /etc/profile.d/repose.sh on a box with no guest. The binary comes
# from the signed release, or from --agent / --agent-url against a sha256
# the caller gives; either way nothing unverified lands in
# /opt/repose/bin.
if [ -n "$agent_url" ]; then
  curl -fsSL "$agent_url" -o "$tmp/repose-agent" || die "could not download $agent_url"
  agent_path="$tmp/repose-agent"
fi
if [ -n "$agent_path" ]; then
  if [ ! -f "$agent_path" ]; then
    die "there is no repose-agent at $agent_path"
  fi
  if [ "$(sha256_of "$agent_path")" != "$agent_sha" ]; then
    die "the repose-agent does not match --agent-sha256; refusing to install"
  fi
else
  # The release path, verified the way install.sh verifies the CLI: the
  # archive against checksums.txt, and checksums.txt against the release
  # key's signature. REPOSE_INSTALL_BASE_URL and REPOSE_INSTALL_PUBKEY are
  # install.sh's own overrides for a test release; a real install sets
  # neither.
  if [ "$agent_version" = latest ] && [ -z "${REPOSE_INSTALL_BASE_URL:-}" ]; then
    agent_version=$(curl -fsSL -o /dev/null -w '%{url_effective}' \
      "https://github.com/$REPO/releases/latest" | sed 's#.*/tag/##')
    if [ -z "$agent_version" ]; then
      die "could not resolve the latest release; pass --agent-version TAG"
    fi
  fi
  base=${REPOSE_INSTALL_BASE_URL:-https://github.com/$REPO/releases/download/$agent_version}
  archive="repose-agent_${agent_version}_linux_amd64.tar.gz"
  curl -fsSL "$base/$archive" -o "$tmp/$archive" ||
    die "release $agent_version has no $archive; pass --agent-url URL with --agent-sha256 HEX"
  curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" ||
    die "release $agent_version has no checksums.txt; refusing to install"
  if [ -n "${REPOSE_INSTALL_BASE_URL:-}" ] && [ -n "${REPOSE_INSTALL_PUBKEY:-}" ]; then
    cp "$REPOSE_INSTALL_PUBKEY" "$tmp/release.pub.pem"
  else
    printf '%s\n' "$RELEASE_PUBKEY" > "$tmp/release.pub.pem"
  fi
  curl -fsSL "$base/checksums.txt.sig" -o "$tmp/checksums.txt.sig" ||
    die "release $agent_version has no signature (checksums.txt.sig); refusing to install"
  openssl dgst -sha256 -verify "$tmp/release.pub.pem" \
    -signature "$tmp/checksums.txt.sig" "$tmp/checksums.txt" >/dev/null 2>&1 ||
    die "the signature on checksums.txt of $agent_version does not verify; refusing to install"
  want=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1 || true)
  if [ -z "$want" ] || [ "$(sha256_of "$tmp/$archive")" != "$want" ]; then
    die "checksum verification failed for $archive"
  fi
  tar -xzf "$tmp/$archive" -C "$tmp" repose-agent ||
    die "$archive holds no repose-agent"
  agent_path=$tmp/repose-agent
fi
agent_changed=0
want_sha=$(sha256_of "$agent_path")
if [ ! -x "$prefix/bin/repose-agent" ] ||
  [ "$(sha256_of "$prefix/bin/repose-agent")" != "$want_sha" ]; then
  root_run install -D -m 0755 "$agent_path" "$prefix/bin/repose-agent"
  agent_changed=1
  agent_changed=1
fi

profile_text='# repose (ops/install/machine-install.sh): the project environment, then
# the repose bin directory on PATH, as the guest convention of
# docs/interfaces/guest-conventions.md has it.
if [ -r /etc/repose/env ]; then
  set -a
  . /etc/repose/env
  set +a
fi
case ":$PATH:" in
  *:/opt/repose/bin:*) ;;
  *) PATH="/opt/repose/bin:$PATH" ;;
esac
export PATH
'
if [ "$(cat /etc/profile.d/repose.sh 2>/dev/null || true)" != "$(printf '%s' "$profile_text")" ]; then
  printf '%s' "$profile_text" | write_root 0644 /etc/profile.d/repose.sh
  agent_changed=1
fi
if link_on_path repose-agent; then
  agent_changed=1
fi
if [ "$agent_changed" = 1 ]; then
  changed=1
  step "Installed $prefix/bin/repose-agent"
else
  step "$prefix/bin/repose-agent is installed"
fi

# ---------------------------------------------------------------- step 5
# The agents, from the same pins and the same sha256 values as the nix
# overlay, without nix. The block below is a copy of
# nix/overlay/agents/versions.json; scripts/bump-agents.sh rewrites that
# file, so a bump lands here in the same commit, and --versions PATH
# reads the tree's copy instead of this one.
if [ -n "$versions" ]; then
  if [ ! -r "$versions" ]; then
    die "there is no versions.json at $versions"
  fi
  pins=$versions
else
  pins=$tmp/versions.json
  cat > "$pins" <<'VERSIONS'
{
  "claude-code": {
    "version": "2.1.283",
    "x86_64-linux": {
      "url": "https://downloads.claude.ai/claude-code-releases/2.1.283/linux-x64/claude",
      "hash": "sha256-GFlYPOMpIFlcYe+Gi+5S4bFZT3SG2yCZNeAfHl6ASuI="
    }
  },
  "opencode": {
    "version": "1.18.34",
    "x86_64-linux": {
      "url": "https://github.com/anomalyco/opencode/releases/download/v1.18.34/opencode-linux-x64.tar.gz",
      "hash": "sha256-DyJHlkcibR0t2ZWV0gCC7nvaOHC2LcapC0Hvwacdfpo="
    }
  },
  "codex": {
    "version": "0.157.1",
    "x86_64-linux": {
      "url": "https://github.com/openai/codex/releases/download/rust-v0.157.1/codex-x86_64-unknown-linux-musl.tar.gz",
      "hash": "sha256-6YwejgKOgTf6LSQVyC7Fjns3AaYn41VKrOWzyjFFSvI="
    },
    "code-mode-host": {
      "url": "https://github.com/openai/codex/releases/download/rust-v0.157.1/codex-code-mode-host-x86_64-unknown-linux-musl.tar.gz",
      "hash": "sha256-NRb5uLvmvAbue9uSspOhfqsZSz8QubnqEMW4Oely1/w="
    },
    "bwrap": {
      "url": "https://github.com/openai/codex/releases/download/rust-v0.157.1/bwrap-x86_64-unknown-linux-musl.tar.gz",
      "hash": "sha256-x20yV1JzGeF92KhSHVVMSNSx0+FLJtQmam6KwlfO+68="
    }
  },
  "gemini-cli": {
    "version": "0.61.0",
    "npm": {
      "url": "https://registry.npmjs.org/@google/gemini-cli/-/gemini-cli-0.61.0.tgz",
      "hash": "sha256-vE76XJJcRDAQW1UoIO0xZLvv+p3CJ5kPySL5VHM7zX0="
    }
  },
  "pi-coding-agent": {
    "version": "1.0.4",
    "x86_64-linux": {
      "url": "https://github.com/earendil-works/pi/releases/download/v1.0.4/pi-linux-x64.tar.gz",
      "hash": "sha256-KExF3SjPl1oTz/avNHQd0KDNymY06L38AIOufUUuhtY="
    }
  },
  "chrome-devtools-mcp": {
    "version": "1.10.1",
    "url": "https://registry.npmjs.org/chrome-devtools-mcp/-/chrome-devtools-mcp-1.10.1.tgz",
    "hash": "sha256-ASy89ugy1PZwna0MIde+8XCJ6Ure6c8zF50V6gqa3ys="
  },
  "herdr": {
    "version": "0.9.3",
    "x86_64-linux": {
      "url": "https://github.com/herdrdev/herdr/releases/download/v0.9.3/herdr-linux-x86_64",
      "hash": "sha256-GKjcZfHC+khYhDRDVt6hz9kRxvBs9G+njhk/QIf026c="
    }
  }
}
VERSIONS
fi

# unpack_root <tarball>: extracts it into a fresh directory and prints the
# directory the files are in, which for an archive with a single top-level
# directory is that directory (what nix's unpackPhase takes as sourceRoot).
unpack_root() {
  local dir entries
  dir=$tmp/unpack
  rm -rf "$dir"
  mkdir -p "$dir"
  tar -xzf "$1" -C "$dir" || die "$1 is not a tar.gz this installer can read"
  entries=$(find "$dir" -mindepth 1 -maxdepth 1)
  if [ "$(printf '%s\n' "$entries" | wc -l)" = 1 ] && [ -d "$entries" ]; then
    printf '%s' "$entries"
  else
    printf '%s' "$dir"
  fi
}

# wrap.nix's codexDaemon, as it is there but for the store paths: an
# interactive Codex whose dev environment differs from the running
# app-server daemon's gets --no-daemon, so its turns do not run with the
# first checkout's PATH (DECISIONS I-547).
codex_daemon() {
  cat <<'CODEX'
repose_mode=
if [ $# -eq 0 ]; then
  repose_mode=top
else
  case "$1" in
    resume|fork) repose_mode=sub ;;
    agents|exec|e|review|login|logout|mcp|mcp-server|plugin|app-server|remote-control|completion|update|doctor|sandbox|debug|apply|a|queue|archive|delete|migrate-rollouts|unarchive|cloud|exec-server|features|help) ;;
    *) repose_mode=top ;;
  esac
fi
for repose_a in "$@"; do [ "$repose_a" = --no-daemon ] && repose_mode=; done
if [ -n "$repose_mode" ]; then
  repose_cd="${CODEX_HOME:-$HOME/.codex}/app-server-daemon"
  repose_key=$(printf '%s\n%s\n' "${DIRENV_DIR-}" "$PATH" | sha256sum)
  repose_key=${repose_key%% *}
  repose_pid=$(sed -n 's/^{"pid":\([0-9][0-9]*\).*/\1/p' "$repose_cd/daemon.pid" 2>/dev/null) || repose_pid=
  if [ -n "$repose_pid" ] && grep -qa app-server "/proc/$repose_pid/cmdline" 2>/dev/null; then
    if [ "$(cat "$repose_cd/repose-env" 2>/dev/null)" != "$repose_key" ]; then
      if [ "$repose_mode" = top ]; then
        set -- --no-daemon "$@"
      else
        repose_a=$1; shift; set -- "$repose_a" --no-daemon "$@"
      fi
    fi
  else
    { mkdir -p "$repose_cd" && printf '%s\n' "$repose_key" > "$repose_cd/repose-env"; } 2>/dev/null || true
  fi
  unset repose_cd repose_key repose_pid
fi
unset repose_mode repose_a
CODEX
}

# The agent wrapper of nix/overlay/agents/wrap.nix as a plain script: the
# same binary name, the same terminal environment, the same project
# environment, then the real binary with "$@". Two parts of the nix
# wrapper are absent and cannot be stood in for here: repose-agent-setup,
# which registers the hooks and MCP servers of a guest, and the dev shell
# loader of DECISIONS I-259, which needs direnv and nix-direnv. A box the
# user owns has neither in this pass.
#
# write_wrapper <agent> <binary> <real binary> <extra lines>
write_wrapper() {
  local agent=$1 bin=$2 real=$3 extra=$4
  {
    echo '#!/usr/bin/env bash'
    echo "# repose's wrapper for $bin, from nix/overlay/agents/wrap.nix."
    echo '# No set -e: a line that fails here must never block an agent.'
    echo 'if [ -n "${TMUX:-}" ]; then'
    echo '  export TERM=tmux-256color'
    echo 'fi'
    echo 'export COLORTERM=truecolor'
    echo 'if [ -r /etc/profile.d/repose.sh ]; then'
    echo '  . /etc/profile.d/repose.sh'
    echo 'fi'
    echo "export REPOSE_HOOK_AGENT=$bin"
    echo '# herdr detects the agent in a pane from this, whatever the argv0'
    echo '# (DECISIONS I-501).'
    echo "export HERDR_AGENT=$bin"
    if [ -n "$extra" ]; then
      printf '%s\n' "$extra"
    fi
    if [ "$agent" = codex ]; then
      codex_daemon
    fi
    echo "exec $real \"\$@\""
  } > "$tmp/wrapper"
  root_run install -D -m 0755 "$tmp/wrapper" "$prefix/bin/$bin"
  rm -f "$tmp/wrapper"
}

# install_agent <agent> <version>: lays the agent out under
# $prefix/libexec/<agent> the way its nix package does, and writes the
# wrapper. The environment each one sets is that package's: the updaters
# are off, because a binary this script installed cannot replace itself
# and the banner points at a command that fails (DECISIONS I-543).
install_agent() {
  local agent=$1 version=$2 url hash root
  local target=x86_64-unknown-linux-musl host_url host_hash bwrap_url bwrap_hash
  url=$(jq -r --arg a "$agent" '.[$a]["x86_64-linux"].url' "$pins")
  hash=$(jq -r --arg a "$agent" '.[$a]["x86_64-linux"].hash' "$pins")
  root_run rm -rf "$prefix/libexec/$agent"
  case "$agent" in
    claude-code)
      fetch "$url" "$hash" "$tmp/claude"
      root_run install -D -m 0755 "$tmp/claude" "$prefix/libexec/$agent/bin/claude"
      write_wrapper "$agent" claude "$prefix/libexec/$agent/bin/claude" \
        'export DISABLE_AUTOUPDATER=1
export DISABLE_INSTALLATION_CHECKS=1'
      ;;
    opencode)
      fetch "$url" "$hash" "$tmp/opencode.tar.gz"
      root=$(unpack_root "$tmp/opencode.tar.gz")
      [ -f "$root/opencode" ] || die "the opencode $version release holds no opencode binary"
      root_run install -D -m 0755 "$root/opencode" "$prefix/libexec/$agent/bin/opencode"
      write_wrapper "$agent" opencode "$prefix/libexec/$agent/bin/opencode" \
        'export OPENCODE_DISABLE_AUTOUPDATE=1'
      ;;
    codex)
      # $prefix/libexec/codex is a complete Codex package, the shape of
      # upstream's codex-package-<target> asset: the app-server daemon
      # copies the package holding the running executable and refuses to
      # start without every file of it (DECISIONS I-487). bwrap is
      # upstream's own asset byte for byte, because Codex checks it
      # against the sha256 compiled into the binary (I-495), and rg is a
      # copy, since the daemon rejects a link out of the package.
      host_url=$(jq -r '.codex["code-mode-host"].url' "$pins")
      host_hash=$(jq -r '.codex["code-mode-host"].hash' "$pins")
      bwrap_url=$(jq -r '.codex.bwrap.url' "$pins")
      bwrap_hash=$(jq -r '.codex.bwrap.hash' "$pins")
      fetch "$url" "$hash" "$tmp/codex.tar.gz"
      root=$(unpack_root "$tmp/codex.tar.gz")
      [ -f "$root/codex-$target" ] || die "the codex $version release holds no codex-$target"
      root_run install -D -m 0755 "$root/codex-$target" "$prefix/libexec/$agent/bin/codex"
      fetch "$host_url" "$host_hash" "$tmp/codex-host.tar.gz"
      root=$(unpack_root "$tmp/codex-host.tar.gz")
      [ -f "$root/codex-code-mode-host-$target" ] ||
        die "the codex $version release holds no codex-code-mode-host-$target"
      root_run install -D -m 0755 "$root/codex-code-mode-host-$target" \
        "$prefix/libexec/$agent/bin/codex-code-mode-host"
      fetch "$bwrap_url" "$bwrap_hash" "$tmp/bwrap.tar.gz"
      root=$(unpack_root "$tmp/bwrap.tar.gz")
      [ -f "$root/bwrap-$target" ] || die "the codex $version release holds no bwrap-$target"
      root_run install -D -m 0755 "$root/bwrap-$target" \
        "$prefix/libexec/$agent/codex-resources/bwrap"
      root_run install -D -m 0755 "$(command -v rg)" "$prefix/libexec/$agent/codex-path/rg"
      jq -nc --arg v "$version" --arg t "$target" '{
        layoutVersion: 1, version: $v, target: $t, variant: "codex",
        entrypoint: "bin/codex", resourcesDir: "codex-resources",
        pathDir: "codex-path"
      }' > "$tmp/codex-package.json"
      root_run install -D -m 0644 "$tmp/codex-package.json" \
        "$prefix/libexec/$agent/codex-package.json"
      write_wrapper "$agent" codex "$prefix/libexec/$agent/bin/codex" ""
      ;;
    pi-coding-agent)
      # pi reads its package.json, themes, docs and a wasm module from the
      # directory it lives in, so the whole tree is installed.
      fetch "$url" "$hash" "$tmp/pi.tar.gz"
      root=$(unpack_root "$tmp/pi.tar.gz")
      [ -f "$root/pi" ] || die "the pi $version release holds no pi binary"
      root_run mkdir -p "$prefix/libexec/$agent/lib/pi"
      root_run cp -a "$root/." "$prefix/libexec/$agent/lib/pi/"
      root_run chmod 0755 "$prefix/libexec/$agent/lib/pi/pi"
      write_wrapper "$agent" pi "$prefix/libexec/$agent/lib/pi/pi" \
        'export PI_SKIP_VERSION_CHECK=1'
      ;;
    *) die "this installer has no layout for $agent" ;;
  esac
  rm -rf "$tmp/unpack"
}

installed_json=$etcdir/agents.json
: > "$tmp/installed.jsonl"
: > "$tmp/skipped"
agents_changed=0
for agent in $AGENTS; do
  version=$(jq -r --arg a "$agent" '.[$a].version' "$pins")
  bin=$(binary_of "$agent")
  url=$(jq -r --arg a "$agent" '.[$a]["x86_64-linux"].url // empty' "$pins")
  if [ -z "$url" ]; then
    # gemini-cli ships an npm tarball and no linux x86_64 binary, so this
    # installer cannot put it on the box.
    printf '%s\n' "$agent" >> "$tmp/skipped"
    step "$agent $version has no x86_64-linux release and is not installed"
    continue
  fi
  have=""
  if [ -r "$installed_json" ] && [ -x "$prefix/bin/$bin" ]; then
    have=$(jq -r --arg a "$agent" '.[$a].version // empty' "$installed_json" 2>/dev/null || true)
  fi
  if [ "$have" = "$version" ]; then
    step "$agent $version is installed"
  else
    install_agent "$agent" "$version"
    agents_changed=1
    step "Installed $agent $version"
  fi
  # Every run, not only an install: the symlink is what puts the agent on
  # the PATH a tmux window gets, and an install that predates it has none.
  if link_on_path "$bin"; then
    agents_changed=1
  fi
  jq -nc --arg a "$agent" --arg b "$bin" --arg v "$version" \
    '{agent: $a, binary: $b, version: $v}' >> "$tmp/installed.jsonl"
done

# /etc/repose/agents.json, the guest convention of
# docs/interfaces/guest-conventions.md without its `hook` field: there is
# no repose-hook on a machine the user owns in this pass.
agents_text=$(jq -s '
  map({key: .agent, value: {binary: .binary, version: .version}}) | from_entries
' "$tmp/installed.jsonl")
if [ "$(cat "$installed_json" 2>/dev/null || true)" != "$agents_text" ]; then
  printf '%s' "$agents_text" | write_root 0644 "$installed_json"
  agents_changed=1
fi
if [ "$agents_changed" = 1 ]; then
  changed=1
fi

# The login user's Claude Code settings, which is what decides whether an
# agent on this box can act or stops to ask. The three keys are
# nix/guest/base/agents.nix's, for the same reason: a repose machine is
# one an agent may wreck and a rewind puts back, so a permission prompt
# buys nothing and costs the whole claim (DECISIONS I-250, I-425).
#
# User settings, never managed settings, so the user wins: a file that
# already names a defaultMode keeps it, a file this script cannot read as
# an object is left alone, and the comparison is of normalised JSON so a
# second run against the same box changes nothing.
claude_home=$(getent passwd "$login" 2>/dev/null | cut -d: -f6 || true)
if [ -z "$claude_home" ]; then
  claude_home=${HOME:-}
fi
if [ -z "$claude_home" ]; then
  step "The login user has no home directory, so claude keeps its own settings"
else
  claude_settings=$claude_home/.claude/settings.json
  claude_current='{}'
  if [ -r "$claude_settings" ]; then
    claude_current=$(cat "$claude_settings")
  fi
  # An object whose permissions is an object or absent is the only shape
  # the merge below is safe on.
  if ! claude_now=$(printf '%s' "$claude_current" |
    jq -S -e 'if (type == "object") and (((.permissions // {}) | type) == "object")
              then . else null | halt_error(1) end' 2>/dev/null); then
    step "~/.claude/settings.json is not a shape repose will edit and was left alone"
  else
    claude_want=$(printf '%s' "$claude_now" | jq -S '
      (.permissions // {}) as $perm
      | (if ($perm.defaultMode // null) == null then
           .permissions = ($perm + {defaultMode: "bypassPermissions"})
           | .skipDangerousModePermissionPrompt = true
         else . end)
      | (if .tui == null then .tui = "fullscreen" else . end)
    ')
    if [ "$claude_now" = "$claude_want" ]; then
      step "claude starts without permission prompts"
    else
      printf '%s\n' "$claude_want" > "$tmp/claude-settings.json"
      root_run install -D -m 0644 -o "$login" -g "$group" \
        "$tmp/claude-settings.json" "$claude_settings"
      root_run chown "$login:$group" "$(dirname "$claude_settings")"
      rm -f "$tmp/claude-settings.json"
      changed=1
      step "Set claude to start without permission prompts"
    fi
  fi
fi

# ---------------------------------------------------------------- step 6
# The machine's record on the box. The id is a UUIDv7
# (docs/interfaces/README.md) and is written once: a second run keeps it,
# so the laptop's registry and the box agree after a reinstall.
machine_json=$etcdir/machine.json
id=""
if [ -r "$machine_json" ]; then
  id=$(jq -r '.id // empty' "$machine_json" 2>/dev/null || true)
fi
if [ -z "$id" ]; then
  id=$(python3 - <<'PY'
import os, time, uuid
ms = int(time.time() * 1000)
b = bytearray(os.urandom(16))
b[0:6] = ms.to_bytes(6, "big")
b[6] = 0x70 | (b[6] & 0x0F)
b[8] = 0x80 | (b[8] & 0x3F)
print(uuid.UUID(bytes=bytes(b)))
PY
  )
fi
machine_text=$(jq -n --arg n "$name" --arg i "$id" --arg s "$store" \
  '{name: $n, id: $i, store: $s}')
if [ "$(cat "$machine_json" 2>/dev/null || true)" != "$machine_text" ]; then
  printf '%s' "$machine_text" | write_root 0644 "$machine_json"
  changed=1
  step "Named the machine $name in $machine_json"
else
  step "The machine is named $name in $machine_json"
fi

# ------------------------------------------------------------- step 6.5
# Collect the undo store. A point is taken on every `repose run`, so
# without this the store fills and `point take` starts answering
# no_space. The timer runs as the login user, which is who owns the
# points (the store is mounted user_subvol_rm_allowed); `gc` never
# deletes a project's newest point.
gc_service="[Unit]
Description=repose undo store collection
ConditionPathIsMountPoint=$store

[Service]
# No --store: the agent's default is this script's $store (cmd/repose-agent
# DefaultStore). They are one value in two places and must move together.
Type=oneshot
User=$login
ExecStart=$prefix/bin/repose-agent gc --high-water 85
"
gc_timer="[Unit]
Description=repose undo store collection

[Timer]
OnCalendar=hourly
RandomizedDelaySec=600
Persistent=true

[Install]
WantedBy=timers.target
"
gc_changed=0
if [ "$(cat /etc/systemd/system/repose-undo-gc.service 2>/dev/null || true)" != "$(printf '%s' "$gc_service")" ]; then
  printf '%s' "$gc_service" | write_root 0644 /etc/systemd/system/repose-undo-gc.service
  gc_changed=1
fi
if [ "$(cat /etc/systemd/system/repose-undo-gc.timer 2>/dev/null || true)" != "$(printf '%s' "$gc_timer")" ]; then
  printf '%s' "$gc_timer" | write_root 0644 /etc/systemd/system/repose-undo-gc.timer
  gc_changed=1
fi
if [ "$gc_changed" = 1 ]; then
  root_run systemctl daemon-reload
fi
if ! root_run systemctl is-enabled --quiet repose-undo-gc.timer 2>/dev/null; then
  root_run systemctl enable --now repose-undo-gc.timer >/dev/null 2>&1 ||
    die "systemctl could not enable repose-undo-gc.timer"
  gc_changed=1
fi
if [ "$gc_changed" = 1 ]; then
  changed=1
  step "Set the undo store to collect hourly above 85 percent"
else
  step "The undo store collects hourly above 85 percent"
fi

# ------------------------------------------------------------- step 6.6
# A point every half hour, of every project in the store. Without it the
# only point a project has is the one `repose run` took, so a six-hour
# agent session has exactly one place to go back to and an afternoon of
# work is all-or-nothing. A point is 8 ms and 32 KiB (I-814), so the cost
# is store pressure, which `gc` bounds and whose floors keep these from
# eating each other.
point_service="[Unit]
Description=repose rewind point
ConditionPathIsMountPoint=$store

[Service]
# No --project: every project in the store, each with its own name. No
# --store either, as above.
Type=oneshot
User=$login
ExecStart=$prefix/bin/repose-agent point take --reason scheduled
"
# Monotonic, not OnCalendar: half an hour after the timer starts and
# half an hour after each run finishes, so a box that sleeps does not
# come back to a queue of missed points. Persistent= means nothing to a
# monotonic timer and is left out rather than written and ignored.
point_timer="[Unit]
Description=repose rewind point

[Timer]
OnActiveSec=30min
OnUnitInactiveSec=30min
RandomizedDelaySec=60

[Install]
WantedBy=timers.target
"
point_changed=0
if [ "$(cat /etc/systemd/system/repose-undo-point.service 2>/dev/null || true)" != "$(printf '%s' "$point_service")" ]; then
  printf '%s' "$point_service" | write_root 0644 /etc/systemd/system/repose-undo-point.service
  point_changed=1
fi
if [ "$(cat /etc/systemd/system/repose-undo-point.timer 2>/dev/null || true)" != "$(printf '%s' "$point_timer")" ]; then
  printf '%s' "$point_timer" | write_root 0644 /etc/systemd/system/repose-undo-point.timer
  point_changed=1
fi
if [ "$point_changed" = 1 ]; then
  root_run systemctl daemon-reload
fi
if ! root_run systemctl is-enabled --quiet repose-undo-point.timer 2>/dev/null; then
  root_run systemctl enable --now repose-undo-point.timer >/dev/null 2>&1 ||
    die "systemctl could not enable repose-undo-point.timer"
  point_changed=1
fi
if [ "$point_changed" = 1 ]; then
  changed=1
  step "Set the store to take a point of every project every 30 minutes"
else
  step "The store takes a point of every project every 30 minutes"
fi

# ---------------------------------------------------------------- step 7
# The one line on stdout. `changed` is false on a second run against a box
# this has installed already, which is what makes a repeated add safe.
if [ "$changed" = 1 ]; then
  changed_json=true
else
  changed_json=false
fi
jq -nc \
  --arg store "$store" \
  --arg name "$name" \
  --arg id "$id" \
  --arg agent "$prefix/bin/repose-agent" \
  --argjson agents "$agents_text" \
  --argjson changed "$changed_json" \
  --rawfile skipped "$tmp/skipped" \
  '{ok: true, store: $store, fs: "btrfs", machine: {name: $name, id: $id},
    agent: $agent, agents: ($agents | map_values(.version)),
    not_installed: ($skipped | split("\n") | map(select(length > 0))),
    changed: $changed}'
