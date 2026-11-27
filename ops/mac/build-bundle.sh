#!/bin/bash
# Build the Mac host bundle, repose-host-darwin-arm64.tar.gz (DECISIONS
# I-1684, I-1686), on macOS on Apple silicon with Xcode's command line
# tools (swiftc, clang, codesign) and Go. It holds, under bin/:
#
#   hostd, repose-hostd-update   Go, cross-built (the same bytes from any machine)
#   repose-vm                    the VM launcher, Swift, signed ad hoc with
#                                com.apple.security.virtualization (cmd/repose-vm)
#   mke2fs e2fsck dumpe2fs debugfs   e2fsprogs, from its pinned release
#   zstd                         zstd, from its pinned release
#
# Every source is pinned by SHA-256, paths are mapped out of the
# binaries, and the tar is packed by ops/mac/bundle with one owner, mode
# and mtime, so two runs with the same toolchain give the same bytes
# (ops/mac/build-bundle.sh --twice checks that). The release workflow runs
# it on a GitHub macOS runner (.github/workflows/appliance-image.yml, job
# mac); anyone can run it on a Mac.
#
#   ops/mac/build-bundle.sh VERSION OUT          build OUT (a .tar.gz)
#   ops/mac/build-bundle.sh --twice VERSION OUT  build twice in two directories, compare, keep the first
set -euo pipefail

E2FS_VERSION=1.47.3
E2FS_URL=https://mirrors.edge.kernel.org/pub/linux/kernel/people/tytso/e2fsprogs/v$E2FS_VERSION/e2fsprogs-$E2FS_VERSION.tar.xz
E2FS_SHA256=857e6ef800feaa2bb4578fbc810214be5d3c88b072ea53c5384733a965737329
ZSTD_VERSION=1.5.7
ZSTD_URL=https://github.com/facebook/zstd/releases/download/v$ZSTD_VERSION/zstd-$ZSTD_VERSION.tar.gz
ZSTD_SHA256=eb33e51f49a15e023950cd7825ca74a4a2b43db8354825ac24fc1b7ee09e6fa3

twice=0
if [ "${1:-}" = --twice ]; then twice=1; shift; fi
[ $# -eq 2 ] || { sed -n '2,/^set -euo/p' "$0" | sed '$d; s/^# \{0,1\}//' >&2; exit 2; }
version=$1 out=$2
[ "$(uname -s)/$(uname -m)" = Darwin/arm64 ] || { echo "build-bundle: this runs on macOS on Apple silicon" >&2; exit 1; }
top=$(cd "$(dirname "$0")/../.." && pwd)
case $out in /*) ;; *) out=$PWD/$out ;; esac
ncpu=$(sysctl -n hw.ncpu)

fetch() { # url sha256 dest
  curl -fsSL --retry 3 -o "$3" "$1"
  echo "$2  $3" | shasum -a 256 -c - >/dev/null || { echo "build-bundle: $1 does not match its pinned checksum" >&2; exit 1; }
}

build() { # workdir dest
  local w=$1 dest=$2
  mkdir -p "$w/bin" "$w/src"
  # Go: no cgo, so these are cross-builds and match a Linux build byte for byte.
  (cd "$top" && GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=$version -buildid=" -o "$w/bin/hostd" ./cmd/hostd)
  (cd "$top" && GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -buildid=" -o "$w/bin/repose-hostd-update" ./cmd/repose-hostd-update)
  # The launcher, from a copy at a fixed path, so no checkout path is in it.
  REPOSE_VM_PREFIX_MAP="$top=/repose" "$top/cmd/repose-vm/build.sh" "$w/bin/repose-vm"

  local flags="-O2 -ffile-prefix-map=$w=/build -mmacosx-version-min=14.0"
  fetch "$E2FS_URL" "$E2FS_SHA256" "$w/src/e2fsprogs.tar.xz"
  tar -xf "$w/src/e2fsprogs.tar.xz" -C "$w/src"
  (cd "$w/src/e2fsprogs-$E2FS_VERSION" &&
    ./configure -q --disable-nls --disable-fuse2fs --disable-uuidd --disable-e2initrd-helper \
      --disable-defrag --without-libarchive MKDIR_P="mkdir -p" CFLAGS="$flags" >/dev/null &&
    make -s -j"$ncpu" libs >/dev/null &&
    make -s -j"$ncpu" -C misc mke2fs dumpe2fs >/dev/null &&
    make -s -j"$ncpu" -C e2fsck e2fsck >/dev/null &&
    make -s -j"$ncpu" -C debugfs debugfs >/dev/null)
  local e=$w/src/e2fsprogs-$E2FS_VERSION
  cp "$e/misc/mke2fs" "$e/misc/dumpe2fs" "$e/e2fsck/e2fsck" "$e/debugfs/debugfs" "$w/bin/"

  fetch "$ZSTD_URL" "$ZSTD_SHA256" "$w/src/zstd.tar.gz"
  tar -xzf "$w/src/zstd.tar.gz" -C "$w/src"
  # libSystem only: no zlib, lzma or lz4 formats (hostd writes and reads zstd).
  make -s -j"$ncpu" -C "$w/src/zstd-$ZSTD_VERSION/programs" zstd-release \
    HAVE_ZLIB=0 HAVE_LZMA=0 HAVE_LZ4=0 MOREFLAGS="$flags" >/dev/null
  cp "$w/src/zstd-$ZSTD_VERSION/programs/zstd" "$w/bin/"

  # The C tools: symbols out, then signed ad hoc again (strip voids the
  # linker's signature, and macOS on arm64 runs nothing unsigned).
  for b in mke2fs dumpe2fs e2fsck debugfs zstd; do
    strip -x "$w/bin/$b"
    codesign --force --sign - --identifier "run.repose.$b" "$w/bin/$b"
  done
  "$w/bin/mke2fs" -V >/dev/null 2>&1
  "$w/bin/zstd" -V >/dev/null
  [ "$("$w/bin/hostd" version)" = "hostd $version" ]
  codesign -d --entitlements - "$w/bin/repose-vm" 2>/dev/null | grep -q com.apple.security.virtualization ||
    { echo "build-bundle: repose-vm lacks the virtualization entitlement" >&2; exit 1; }
  (cd "$top" && go run ./ops/mac/bundle pack --dir "$w" --version "$version" --out "$dest")
}

work=$(mktemp -d "${TMPDIR:-/tmp}/repose-bundle.XXXXXX")
trap 'rm -rf "$work"' EXIT
build "$work/a" "$out"
sum=$(shasum -a 256 "$out" | awk '{ print $1 }')
if [ "$twice" = 1 ]; then
  build "$work/b" "$work/b.tar.gz"
  sum2=$(shasum -a 256 "$work/b.tar.gz" | awk '{ print $1 }')
  if [ "$sum" != "$sum2" ]; then
    echo "build-bundle: not reproducible: $sum against $sum2" >&2
    for f in "$work"/a/bin/*; do
      cmp -s "$f" "$work/b/bin/$(basename "$f")" || echo "build-bundle: bin/$(basename "$f") differs" >&2
    done
    exit 1
  fi
  echo "build-bundle: reproducible, two builds in two directories"
fi
echo "build-bundle: $(basename "$out") sha256=$sum bytes=$(stat -f %z "$out") version=$version"
