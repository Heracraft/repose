#!/bin/sh
# Bring the staging control plane back on stg-edge (after an edge reboot,
# or any unit lost). Idempotent: a running unit is left alone.
# Usage on the edge: /var/lib/repose-staging/up.sh
set -eu
B=/var/lib/repose-staging
PGB=$(readlink $B/pg)/bin
run() { u=$1; shift; systemctl is-active -q "$u" && return 0; systemctl reset-failed "$u" 2>/dev/null || true; systemd-run --unit="$u" "$@"; }
ip addr show dev lo | grep -q 172.206.225.242 || ip addr add 172.206.225.242/32 dev lo
nft list chain inet repose-edge input | grep -q 'stg-cp grpc' || nft insert rule inet repose-edge input ip saddr 10.200.1.0/24 tcp dport 8444 accept comment '"stg-cp grpc"'
nft list chain inet repose-edge input | grep -q 'stg-cp operator' || nft insert rule inet repose-edge input ip saddr 20.102.97.100 tcp dport '{ 8080, 8081 }' accept comment '"stg-cp operator"'
systemctl is-active -q wireguard-wg0 || systemctl start wireguard-wg0
run stg-postgres -p DynamicUser=yes -p StateDirectory=repose-staging-pg -p StateDirectoryMode=0700 -p Restart=always -p RestartSec=2 -p RuntimeDirectory=stg-postgres \
  $PGB/postgres -D /var/lib/repose-staging-pg/data -c listen_addresses=127.0.0.1 -c port=5432 -c unix_socket_directories=/run/stg-postgres
for i in $(seq 1 20); do $PGB/pg_isready -q -h 127.0.0.1 && break; sleep 1; done
run stg-idp -p Restart=always -p RestartSec=2 -p EnvironmentFile=$B/idp.env $B/bin/stg-idp
run stg-api -p Restart=always -p RestartSec=3 -p EnvironmentFile=$B/api.env $B/bin/stg-api
for i in $(seq 1 20); do curl -sf http://127.0.0.1:8080/healthz >/dev/null && break; sleep 1; done
run stg-wgsync -p Restart=always -p RestartSec=5 -p EnvironmentFile=$B/gw.env -p Environment=PATH=/run/current-system/sw/bin $B/bin/gateway wgsync
run stg-gateway -p Restart=always -p RestartSec=1 -p RuntimeDirectory=stg-gateway -p EnvironmentFile=$B/gw.env $B/bin/gateway serve
nft list chain inet repose-edge input | grep -q "stg-cp guest-http" || nft insert rule inet repose-edge input ip saddr 10.200.1.0/24 tcp dport 8082 accept comment "\"stg-cp guest-http\""
run stg-guest-http -p Restart=always -p ProtectSystem=strict -p ProtectHome=yes -p ReadOnlyPaths=$B/guest -p PrivateTmp=yes $(readlink $B/py)/bin/python3 -m http.server 8082 --bind 10.200.2.4 --directory $B/guest
systemctl is-active stg-postgres stg-idp stg-api stg-wgsync stg-gateway stg-guest-http
curl -s http://127.0.0.1:8080/healthz; echo
