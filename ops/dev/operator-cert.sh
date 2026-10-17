#!/usr/bin/env bash
# An 8-hour Host CA certificate for this machine's operator key, written
# next to the key so plain `ssh` offers it with no flag (DECISIONS I-177):
#
#   ops/dev/operator-cert.sh [--if-stale] [control-ip] [key]   # defaults below
#   ssh -o HostKeyAlias=10.200.1.4 -J root@20.102.98.254:2222 root@10.255.0.2
#
# The key never leaves this machine: its public half goes to
# `repose-admin operator-cert --pubkey -` inside the api container over the
# operator SSH the control VM already accepts, and the certificate comes
# back on stdout. Hosts accept it once they know the Host CA
# (`wc -c /run/repose/host_ca.pub` non-zero; RUNBOOK "Operator certificate
# refused by a host"), which is what lets `repose.host.bootstrap
# .keyUntilHostCA` retire the plain key without locking the operator out.
# Run it again when the certificate expires; `ssh-keygen -L -f
# <key>-cert.pub` shows its validity.
set -euo pipefail

# --if-stale: renew only when the certificate on disk has under an hour
# left, so this can be the first line of anything that reaches a host or a
# guest. An expired certificate is why woker-1 was banned from the gateway
# three times in one night: a refusal of the user's own current certificate
# does not count towards the ban (DECISIONS I-599), but an expired one
# does, and the ttl here is 8 hours, so a retry loop is 20 counted
# failures in minutes (I-435: 20 in 10 minutes, then 10 minutes refused).
# The failures stop at the source; a fresh certificate does not fail a
# handshake.
if_stale=0
if [ "${1:-}" = --if-stale ]; then
	if_stale=1
	shift
fi

CONTROL=${1:-20.121.138.150}
KEY=${2:-$HOME/.ssh/id_ed25519}
# The `api` Coolify application's container name prefix (docs/workstreams/
# archive/HANDOFF-2026-09.md "Live environment"); Coolify appends a deploy
# suffix.
API_PREFIX=${REPOSE_API_CONTAINER_PREFIX:-8kpqxzfejbbsgwjhooep2ymc}

if [ "$if_stale" = 1 ] && [ -s "$KEY-cert.pub" ]; then
	# ssh-keygen -L prints "Valid: from <from> to <to>"; compare the end
	until_s=$(ssh-keygen -L -f "$KEY-cert.pub" 2>/dev/null |
		sed -n 's/.*Valid: from .* to \(.*\)$/\1/p' | head -n1)
	if [ -n "$until_s" ]; then
		left=$(( $(date -d "$until_s" +%s 2>/dev/null || echo 0) - $(date +%s) ))
		if [ "$left" -gt 3600 ]; then
			printf 'operator certificate valid for %d more minutes; not renewed\n' \
				$((left / 60)) >&2
			exit 0
		fi
	fi
fi

if [ ! -s "$KEY.pub" ]; then
	echo "no public key at $KEY.pub" >&2
	exit 1
fi

cert=$(ssh -o BatchMode=yes -o ConnectTimeout=15 "root@$CONTROL" \
	"c=\$(docker ps --filter name=$API_PREFIX --format '{{.Names}}' | head -1)
	 [ -n \"\$c\" ] || { echo 'no api container named $API_PREFIX*' >&2; exit 1; }
	 docker exec -i \"\$c\" repose-admin operator-cert --pubkey - --name \"${REPOSE_OPERATOR_NAME:-$(whoami)}\" --ttl 8h" <"$KEY.pub")

case "$cert" in
ssh-ed25519-cert-v01@openssh.com\ * | ecdsa-sha2-*-cert-v01@openssh.com\ * | ssh-rsa-cert-v01@openssh.com\ *) ;;
*)
	echo "repose-admin did not return a certificate: $cert" >&2
	exit 1
	;;
esac

umask 022
printf '%s\n' "$cert" >"$KEY-cert.pub.tmp"
mv "$KEY-cert.pub.tmp" "$KEY-cert.pub"
ssh-keygen -L -f "$KEY-cert.pub" | grep -E 'Key ID|Valid:'
