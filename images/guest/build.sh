#!/usr/bin/env bash
# Builds the erofs guest bundle (DECISIONS I-1466; guest-conventions.md
# "The erofs guest") for one or both architectures:
#
#   <out>/<version>/<arch>/vmlinuz        bzImage (amd64), plain Image (arm64)
#   <out>/<version>/<arch>/initramfs.img  BusyBox init that composes the root
#   <out>/<version>/<arch>/rootfs.erofs   the Dockerfile's root, lz4hc level 9
#   <out>/<version>/<arch>/manifest.json  version, arch, kernel_version,
#                                         git_sha, sizes, sha256 of each file
#   <out>/<version>/<arch>/SHA256SUMS
#
# guestd and repose-hook are built from this checkout (CGO_ENABLED=0) and
# stamped with its git SHA. The root is exported with buildx's tar output,
# so no image is loaded into the Docker daemon.
#
# usage: build.sh [--arch amd64|arm64]... [--version YYYY.MM.DD.N] [--out DIR]
#                 [--builder NAME]
#   --arch     repeat for both; default both
#   --version  default today's date, .1
#   --out      default /mnt/nixstore/tmp/nonix-guest/out when that exists,
#              else images/guest/out
#   --builder  a buildx builder (a container builder whose state lives off
#              the root disk, on a small dev box); default the current one
# Needs: docker with buildx (binfmt for a foreign arch), go, mkfs.erofs and
# dump.erofs (erofs-utils), python3, jq, sha256sum.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(git -C "$here" rev-parse --show-toplevel)
archs=()
version=$(date -u +%Y.%m.%d).1
out=""
builder=()
while [ $# -gt 0 ]; do
  case "$1" in
    --arch) archs+=("$2"); shift ;;
    --version) version=$2; shift ;;
    --out) out=$2; shift ;;
    --builder) builder=(--builder "$2"); shift ;;
    -h|--help) sed -n '2,26p' "$0"; exit 0 ;;
    *) echo "build.sh: unknown argument $1" >&2; exit 64 ;;
  esac
  shift
done
[ ${#archs[@]} -gt 0 ] || archs=(amd64 arm64)
if [ -z "$out" ]; then
  if [ -d /mnt/nixstore/tmp ]; then out=/mnt/nixstore/tmp/nonix-guest/out; else out=$here/out; fi
fi
if ! [[ $version =~ ^[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9]+$ ]]; then
  echo "build.sh: --version must be YYYY.MM.DD.N, not $version" >&2
  exit 64
fi
for a in "${archs[@]}"; do
  case "$a" in amd64|arm64) ;; *) echo "build.sh: --arch is amd64 or arm64, not $a" >&2; exit 64 ;; esac
done
command -v mkfs.erofs >/dev/null || { echo "build.sh: mkfs.erofs (erofs-utils) is not on PATH" >&2; exit 1; }

# COPY keeps the context's modes, directories included, and git keeps no
# directory modes: a checkout made under umask 002 gave the image a
# group-writable /etc, which sshd refuses for its CA and principals files.
find "$here/rootfs" "$here/rootfs-late" -perm /022 ! -type l -exec chmod go-w {} +

sha=$(git -C "$repo" rev-parse HEAD)
if [ -n "$(git -C "$repo" status --porcelain -- cmd internal proto go.mod go.sum images/guest)" ]; then
  sha="$sha-dirty"
fi

for arch in "${archs[@]}"; do
  start=$(date +%s)
  dest=$out/$version/$arch
  work=$out/.work/$version-$arch
  rm -rf "$work" "$dest"
  mkdir -p "$work/bin" "$dest"

  echo "== $arch: guestd and repose-hook ($sha)"
  for b in guestd repose-hook; do
    (cd "$repo" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch \
      go build -trimpath -ldflags "-s -w -X main.version=$version+$sha" -o "$work/bin/$b" "./cmd/$b")
  done

  common=(
    "${builder[@]}"
    --platform "linux/$arch"
    --build-context "bin=$work/bin"
    --build-context "viewer=$repo/nix/guest/base/desktop/viewer"
    --build-arg "BASE_VERSION=$version"
    --build-arg "GIT_SHA=$sha"
    --file "$here/Dockerfile"
    --progress plain
  )

  echo "== $arch: kernel and initramfs"
  docker buildx build "${common[@]}" --target bundle --output "type=local,dest=$work/bundle" "$here"
  echo "== $arch: root filesystem"
  docker buildx build "${common[@]}" --target rootfs --output "type=tar,dest=$work/rootfs.tar" "$here"

  echo "== $arch: rootfs.erofs"
  # BuildKit's tar has no entry for the root directory, and mkfs.erofs then
  # gives / the caller's uid and mode 0777: a "./" entry, root 0755, goes
  # first.
  mkdir -m 0755 "$work/empty"
  tar -C "$work/empty" --owner=0 --group=0 --numeric-owner -cf "$work/root.tar" .
  tar --concatenate -f "$work/root.tar" "$work/rootfs.tar"
  mv "$work/root.tar" "$work/rootfs.tar"
  # --tar=f reads the tar straight into the image; the uid, gid and modes
  # are the tar's, and every file's time is the build's (-T0, reproducible).
  mkfs.erofs -zlz4hc,9 -T0 --tar=f "$dest/rootfs.erofs" "$work/rootfs.tar" >/dev/null
  # The artifact gate: the modes and owners sshd, sudo and every user's
  # temp files depend on, read from the produced image's inodes.
  python3 "$here/scripts/erofs-gate.py" "$(command -v dump.erofs)" "$dest/rootfs.erofs"

  install -m 0644 "$work/bundle/vmlinuz" "$work/bundle/initramfs.img" "$dest/"
  kv=$(cat "$work/bundle/kernel-version")
  (cd "$dest" && sha256sum vmlinuz initramfs.img rootfs.erofs > SHA256SUMS)
  jq -n --arg version "$version" --arg arch "$arch" --arg kv "$kv" --arg sha "$sha" \
    --rawfile sums "$dest/SHA256SUMS" \
    --argjson sizes "$(cd "$dest" && for f in vmlinuz initramfs.img rootfs.erofs; do printf '{"%s":%s}\n' "$f" "$(stat -c %s "$f")"; done | jq -s add)" \
    '{version: $version, arch: $arch, kernel_version: $kv, git_sha: $sha,
      files: ($sums | split("\n") | map(select(length > 0) | split("  ") | {key: .[1], value: {sha256: .[0]}}) | from_entries
              | with_entries(.value.size = $sizes[.key]))}' > "$dest/manifest.json"
  (cd "$dest" && sha256sum manifest.json >> SHA256SUMS)
  rm -rf "$work"
  echo "== $arch: done in $(( $(date +%s) - start )) s"
  jq -c '{arch, kernel_version, files: (.files | map_values(.size))}' "$dest/manifest.json"
done
