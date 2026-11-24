#!/bin/bash
# One run: go test compile of ./internal/guestd/... (no tests executed), cold GOCACHE then
# one-file-touched warm rerun. Args: label cpuquota iolimit(yes|no) cachedir
set -u
label=$1; quota=$2; io=$3; cache=$4
W=/home/azureuser/kanali-agent-first-research
GO=$(readlink -f $(which go))
props=(-p MemoryMax=4G -p CPUQuota=$quota)
if [ "$io" = yes ]; then
  for k in IOReadIOPSMax IOWriteIOPSMax; do props+=(-p "$k=/dev/nvme0n1 2000"); done
  for k in IOReadBandwidthMax IOWriteBandwidthMax; do props+=(-p "$k=/dev/nvme0n1 80M"); done
fi
rm -rf "$cache"; mkdir -p "$cache"
run() { sudo systemd-run --scope -q --uid=azureuser "${props[@]}" -E HOME=/home/azureuser -E GOCACHE="$cache" -E GOMODCACHE=/home/azureuser/go/pkg/mod -E GOFLAGS=-mod=readonly -E GOTOOLCHAIN=local -E PATH="$(dirname $GO):/usr/bin:/bin" \
  bash -c "cd $W && /usr/bin/time -f %e $GO test -count=1 -run '^\$' ./internal/guestd/... >/dev/null 2>/tmp/claude-1000/iob.err; tail -1 /tmp/claude-1000/iob.err"; }
cold=$(run)
echo "// bench" >> $W/internal/guestd/guestd.go
warm=$(run)
(cd $W && git checkout -q internal/guestd/guestd.go)
echo "$label cold_s=$cold warm_after_touch_s=$warm cache_MB=$(du -sm $cache | cut -f1)"
rm -rf "$cache"
