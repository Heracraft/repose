#!/usr/bin/env bash
# Publish an erofs base version on staging and, optionally, make erofs the
# default guest runtime. Staging only (stg-cp, DECISIONS I-1462, I-1464).
#
#   RELEASE_URL=https://.../v1/  VERSION=2026.11.22-1  KERNEL_VERSION=6.18.57 \
#   CHANGELOG="first erofs base"  publish-erofs.sh [--default]
#
# File names inside RELEASE_URL (defaults are guesses; set them from the
# release's manifest): AMD64_IMAGE, AMD64_KERNEL, AMD64_INITRAMFS, and the
# optional ARM64_IMAGE, ARM64_KERNEL, ARM64_INITRAMFS. Each *_SHA256 may be
# given; otherwise it is read from RELEASE_URL/SHA256SUMS. MANIFEST_URL
# defaults to RELEASE_URL/manifest.json. --default also runs
# `settings set default_guest_runtime erofs`. --dry-run prints the command.
set -euo pipefail
: "${RELEASE_URL:?set RELEASE_URL (the directory holding the release files, ending in /)}"
: "${VERSION:?set VERSION (the base_versions label)}"
: "${KERNEL_VERSION:?set KERNEL_VERSION}"
: "${CHANGELOG:?set CHANGELOG}"
RELEASE_URL="${RELEASE_URL%/}/"
MANIFEST_URL="${MANIFEST_URL:-${RELEASE_URL}manifest.json}"
AMD64_IMAGE="${AMD64_IMAGE:-rootfs-amd64.erofs}"
AMD64_KERNEL="${AMD64_KERNEL:-vmlinuz-amd64}"
AMD64_INITRAMFS="${AMD64_INITRAMFS:-initramfs-amd64.img}"
SSHCFG=/home/azureuser/repose-ws/staging-ssh-config
default=0 dry=0
for a in "$@"; do
	case $a in
	--default) default=1 ;;
	--dry-run) dry=1 ;;
	*) echo "unknown argument $a" >&2; exit 2 ;;
	esac
done

sums=$(curl -fsSL "${RELEASE_URL}SHA256SUMS" 2>/dev/null || true)
sum_of() { # <file> [given]
	if [ -n "${2:-}" ]; then echo "$2"; return; fi
	local s
	s=$(printf '%s\n' "$sums" | awk -v f="$1" '{n=$2; sub(/^\*/,"",n)} n==f{print $1; exit}')
	[ -n "$s" ] || { echo "no SHA256 for $1: set it or publish SHA256SUMS beside it" >&2; exit 1; }
	echo "$s"
}

args=(base publish --format erofs --version "$VERSION" --kernel-version "$KERNEL_VERSION"
	--manifest-url "$MANIFEST_URL" --changelog "$CHANGELOG"
	--amd64-url "${RELEASE_URL}${AMD64_IMAGE}" --amd64-sha256 "$(sum_of "$AMD64_IMAGE" "${AMD64_IMAGE_SHA256:-}")"
	--amd64-kernel-url "${RELEASE_URL}${AMD64_KERNEL}" --amd64-kernel-sha256 "$(sum_of "$AMD64_KERNEL" "${AMD64_KERNEL_SHA256:-}")"
	--amd64-initramfs-url "${RELEASE_URL}${AMD64_INITRAMFS}" --amd64-initramfs-sha256 "$(sum_of "$AMD64_INITRAMFS" "${AMD64_INITRAMFS_SHA256:-}")")
if [ -n "${ARM64_IMAGE:-}" ]; then
	args+=(--arm64-url "${RELEASE_URL}${ARM64_IMAGE}" --arm64-sha256 "$(sum_of "$ARM64_IMAGE" "${ARM64_IMAGE_SHA256:-}")"
		--arm64-kernel-url "${RELEASE_URL}${ARM64_KERNEL}" --arm64-kernel-sha256 "$(sum_of "$ARM64_KERNEL" "${ARM64_KERNEL_SHA256:-}")"
		--arm64-initramfs-url "${RELEASE_URL}${ARM64_INITRAMFS}" --arm64-initramfs-sha256 "$(sum_of "$ARM64_INITRAMFS" "${ARM64_INITRAMFS_SHA256:-}")")
fi
[ "${SECURITY:-0}" = 1 ] && args+=(--security)

q=""
for a in "${args[@]}"; do q="$q $(printf '%q' "$a")"; done
echo "+ stg-admin$q"
[ $dry = 1 ] && exit 0
ssh -F "$SSHCFG" stg-edge "/var/lib/repose-staging/admin$q"
ssh -F "$SSHCFG" stg-edge "/var/lib/repose-staging/admin base list"
if [ $default = 1 ]; then
	ssh -F "$SSHCFG" stg-edge "/var/lib/repose-staging/admin settings set default_guest_runtime erofs && /var/lib/repose-staging/admin settings get default_guest_runtime"
fi
