#!/bin/sh
# Builds repose-vm on a Mac (Xcode command line tools: swiftc, codesign) and
# signs it ad hoc with the virtualization entitlement, without which VZ
# refuses to make a VM. Usage: build.sh [OUT] (default ./repose-vm).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=${1:-./repose-vm}
swiftc -O -swift-version 5 -target arm64-apple-macos14 -o "$out" "$here/main.swift"
codesign --force --sign - --entitlements "$here/entitlements.plist" "$out"
