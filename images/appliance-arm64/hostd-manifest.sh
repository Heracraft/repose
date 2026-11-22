#!/bin/bash
# Write and sign the hostd channel manifest repose-hostd-update reads
# (host/bin/repose-hostd-update has the format; DECISIONS I-1496).
#
#   hostd-manifest.sh <version> <amd64-binary> <amd64-url> <arm64-binary> <arm64-url> <signing-key.pem> > manifest.json
#
# The key is the release key's private half (release.yml's
# REPOSE_RELEASE_SIGNING_KEY); the result is checked against the public
# half install.sh carries before it is printed, so a manifest every host
# would refuse is never published.
set -euo pipefail
[ $# -eq 6 ] || { sed -n '4,5p' "$0" >&2; exit 2; }
version=$1 a_bin=$2 a_url=$3 r_bin=$4 r_url=$5 key=$6
here=$(cd "$(dirname "$0")" && pwd)
pub=${REPOSE_RELEASE_PUBKEY_FILE:-}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
if [ -z "$pub" ]; then
  pub=$tmp/release.pub
  sed -n "/^RELEASE_PUBKEY='/,/END PUBLIC KEY/p" "$here/../../install.sh" |
    sed "s/^RELEASE_PUBKEY='//; s/'\$//" > "$pub"
fi
a_sum=$(sha256sum "$a_bin" | awk '{ print $1 }')
r_sum=$(sha256sum "$r_bin" | awk '{ print $1 }')
printf 'repose-hostd-manifest v1\nversion %s\namd64 %s %s\narm64 %s %s\n' \
  "$version" "$a_sum" "$a_url" "$r_sum" "$r_url" > "$tmp/signed"
openssl dgst -sha256 -sign "$key" -out "$tmp/sig" "$tmp/signed"
openssl dgst -sha256 -verify "$pub" -signature "$tmp/sig" "$tmp/signed" >/dev/null
jq -n --arg v "$version" --arg au "$a_url" --arg as "$a_sum" --arg ru "$r_url" --arg rs "$r_sum" \
  --arg sig "$(base64 -w0 < "$tmp/sig")" \
  '{version: $v, arches: {amd64: {url: $au, sha256: $as}, arm64: {url: $ru, sha256: $rs}}, signature: $sig}'
