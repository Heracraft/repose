#!/bin/sh
# Builds repose-vm on a Mac (Xcode command line tools: swiftc, codesign) and
# signs it ad hoc with the virtualization entitlement, without which VZ
# refuses to make a VM. Usage: build.sh [OUT] (default ./repose-vm).
# REPOSE_VM_PREFIX_MAP=OLD=NEW maps the source path out of the binary, so
# the release bundle does not depend on where the checkout is
# (ops/mac/build-bundle.sh).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=${1:-./repose-vm}
map=""
if [ -n "${REPOSE_VM_PREFIX_MAP:-}" ]; then
  map="-file-prefix-map $REPOSE_VM_PREFIX_MAP"
fi
# shellcheck disable=SC2086 # map is two words or none
swiftc -O -swift-version 5 -target arm64-apple-macos14 $map -o "$out" "$here/main.swift"
codesign --force --sign - --identifier run.repose.repose-vm --entitlements "$here/entitlements.plist" "$out"
