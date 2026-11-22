#!/bin/bash
# ops/install-host-x86.sh carries the host's files inside itself, because
# it is fetched alone (curl ... | sudo bash). This writes them into it
# between its GENERATED HOST FILES markers, from the files the Mac
# appliance is built from (host/) and the x86 machine's own (x86/), so
# the two hosts cannot drift apart (DECISIONS I-1498).
#
#   embed.sh           rewrite the installer
#   embed.sh --check   exit 1 when the installer is not what embed.sh writes
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
installer=$here/../../ops/install-host-x86.sh
begin='# BEGIN GENERATED HOST FILES (images/appliance-arm64/embed.sh)'
end='# END GENERATED HOST FILES'
marker=REPOSE_HOST_FILE

emit() { # source dest mode
  if grep -q "^$marker\$" "$1"; then
    echo "embed.sh: $1 has a line that is the heredoc marker" >&2
    exit 1
  fi
  printf "  install_file %s %s <<'%s'\n" "$3" "$2" "$marker"
  cat "$1"
  printf '%s\n' "$marker"
}

body() {
  echo "$begin"
  echo "# Do not edit by hand: the sources are under images/appliance-arm64/."
  echo "write_host_files() {"
  for f in "$here"/host/bin/* "$here"/x86/bin/*; do
    # The Mac's disk is the one that grows under the appliance.
    [ "$(basename "$f")" = repose-mac-grow ] && continue
    emit "$f" "/usr/local/lib/repose/$(basename "$f")" 0755
  done
  (cd "$here/host/systemd" && find . -type f | LC_ALL=C sort) | while read -r f; do
    emit "$here/host/systemd/$f" "/etc/systemd/system/${f#./}" 0644
  done
  for f in "$here"/x86/systemd/*; do
    emit "$f" "/etc/systemd/system/$(basename "$f")" 0644
  done
  (cd "$here/host/etc" && find . -type f | LC_ALL=C sort) | while read -r f; do
    case $f in ./repose/host-chains.nft | ./repose/uplink.nft) continue ;; esac
    emit "$here/host/etc/$f" "/etc/${f#./}" 0644
  done
  emit "$here/x86/etc/repose/host-chains.nft" /etc/repose/host-chains.nft 0644
  echo "}"
  echo "$end"
}

if ! grep -qxF "$begin" "$installer" || ! grep -qxF "$end" "$installer"; then
  echo "embed.sh: $installer has lost its GENERATED HOST FILES markers" >&2
  exit 1
fi
new=$(mktemp) gen=$(mktemp)
trap 'rm -f "$new" "$gen"' EXIT
body > "$gen"
awk -v b="$begin" -v e="$end" -v gen="$gen" '
  $0 == b { while ((getline line < gen) > 0) print line; skip = 1; next }
  $0 == e { skip = 0; next }
  !skip { print }
' "$installer" > "$new"
if [ "${1:-}" = --check ]; then
  if ! cmp -s "$new" "$installer"; then
    echo "embed.sh: ops/install-host-x86.sh is not what images/appliance-arm64/embed.sh writes; run it" >&2
    diff -u "$installer" "$new" | head -40 >&2 || true
    exit 1
  fi
  exit 0
fi
cat "$new" > "$installer"
