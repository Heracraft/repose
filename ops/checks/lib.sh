#!/usr/bin/env bash
# Shared helpers for the M3 checks in this directory (README.md). Sourced by
# each check, never run on its own. Every check reads ops/checks/m3.env
# (copy m3.env.example) or the same names from the environment, drives the
# real api through the `repose` CLI and raw HTTP with the CLI's token,
# reaches host-01 through the edge's operator sshd, and reaches the control
# VM's containers over its operator sshd. Nothing here applies infrastructure
# or touches Coolify; the one state-changing thing a check does on the
# control VM is `docker kill`/`docker restart` of an api container, and only
# resilience.sh does that, after the conductor has been told.
#
# Every check appends what it saw to $OUT/<check>-<stamp>.txt with the
# evidence blocks the workstream checklists ask to paste.
set -euo pipefail

checks_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source-path=SCRIPTDIR
[ -f "$checks_dir/m3.env" ] && . "$checks_dir/m3.env"

: "${check:=check}"
: "${REPOSE:=repose}"
: "${API_URL:=https://api.repose.run/v1}"
: "${EDGE:?set EDGE (the edge public address) in ops/checks/m3.env}"
: "${EDGE_SSH_PORT:=2222}"
: "${HOST_WG_IP:?set HOST_WG_IP (host-01 WireGuard address, repose-admin hosts list) in ops/checks/m3.env}"
: "${CONTROL_IP:?set CONTROL_IP (the control VM public address) in ops/checks/m3.env}"
: "${PROJECT:?set PROJECT (the slug of the project the checks use) in ops/checks/m3.env}"
: "${OUT:=$checks_dir/out}"
mkdir -p "$OUT"
stamp=$(date -u +%Y%m%dT%H%M%SZ)
report="$OUT/$check-$stamp.txt"

log() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" | tee -a "$report" >&2; }
fail() { log "FAIL: $*"; exit 1; }
# evidence <heading>: the block on stdin goes to the report and the screen.
evidence() { { printf '\n### %s\n' "$1"; cat; printf '\n'; } | tee -a "$report"; }
# assert_eq <what> <got> <want>
assert_eq() { [ "$2" = "$3" ] || fail "$1: got '$2', want '$3'"; log "ok: $1 = '$3'"; }
# assert_zero <what> <count>: a grep count that must be zero
assert_zero() { [ "$2" = "0" ] || fail "$1: found $2 occurrences, want none"; log "ok: $1: none"; }

ssh_common=(-o BatchMode=yes -o ConnectTimeout=20 -o ServerAliveInterval=30)
edge_jump=(-J "root@$EDGE:$EDGE_SSH_PORT")

# host_sh <script>: run a shell script on host-01 as root, through the edge.
host_sh() { printf '%s\n' "$1" | ssh "${ssh_common[@]}" "${edge_jump[@]}" "root@$HOST_WG_IP" sh -s; }
# guest_sh <slug> <script>: run a script in a guest as dev through the
# gateway, with the certificate `repose run` wrote (~/.ssh/repose/config).
guest_sh() { printf '%s\n' "$2" | ssh "${ssh_common[@]}" "$1.repose" sh -s; }
# control_sh <script>: run a script on the control VM as root.
control_sh() { printf '%s\n' "$1" | ssh "${ssh_common[@]}" "root@$CONTROL_IP" sh -s; }

# The api containers on the control VM. Both applications run the same
# image; api-grpc is the one publishing 8443 (ops/dev/tunnel-prod.sh does
# the same lookup). Postgres is the Service named in its compose file.
containers() {
	[ -n "${API_CTR:-}" ] && [ -n "${API_GRPC_CTR:-}" ] && [ -n "${PG_CTR:-}" ] && return 0
	local found
	found=$(control_sh '
		set -e
		for c in $(docker ps --format "{{.Names}}"); do
			docker inspect -f "{{json .Config.Entrypoint}}" "$c" | grep -q "/usr/local/bin/api" || continue
			if docker port "$c" 8443 >/dev/null 2>&1; then echo "grpc $c"; else echo "http $c"; fi
		done
		echo "pg $(docker ps --format "{{.Names}}" | grep repose-postgres | head -1)"')
	API_CTR=${API_CTR:-$(printf '%s\n' "$found" | awk '$1=="http"{print $2; exit}')}
	API_GRPC_CTR=${API_GRPC_CTR:-$(printf '%s\n' "$found" | awk '$1=="grpc"{print $2; exit}')}
	PG_CTR=${PG_CTR:-$(printf '%s\n' "$found" | awk '$1=="pg"{print $2; exit}')}
	[ -n "$API_CTR" ] && [ -n "$API_GRPC_CTR" ] && [ -n "$PG_CTR" ] || fail "could not find the api, api-grpc and postgres containers on $CONTROL_IP: $found"
	export API_CTR API_GRPC_CTR PG_CTR
	log "containers: api=$API_CTR api-grpc=$API_GRPC_CTR postgres=$PG_CTR"
}

# admin <args...>: repose-admin inside the api container (it talks to
# Postgres directly, DECISIONS I-42). Arguments are shell-quoted for the
# remote shell.
admin() {
	containers
	local q=""
	local a
	for a in "$@"; do q="$q $(printf '%q' "$a")"; done
	ssh "${ssh_common[@]}" "root@$CONTROL_IP" "docker exec -i $API_CTR /usr/local/bin/repose-admin$q"
}
# psql_q <sql>: one query against the platform database, unaligned tuples.
psql_q() {
	containers
	ssh "${ssh_common[@]}" "root@$CONTROL_IP" "docker exec -i $PG_CTR psql -U repose -d repose -At -c $(printf '%q' "$1")"
}
# docker_logs_since <container> <RFC3339 or unix ts>
docker_logs_since() { control_sh "docker logs --since $(printf '%q' "$2") $(printf '%q' "$1") 2>&1"; }

# token: the CLI's current access token. `repose projects` refreshes it
# first, so the file is never stale when it is read.
token() {
	"$REPOSE" projects >/dev/null
	python3 -c 'import json,os; print(json.load(open(os.path.expanduser("~/.config/repose/credentials.json")))["access_token"])'
}
# api <METHOD> <path> [json body]: raw call; prints the body, then the status
# code on its own last line.
api() {
	local m=$1 p=$2 body=${3:-}
	local t
	t=$(token)
	if [ -n "$body" ]; then
		curl -sS -X "$m" -H "Authorization: Bearer $t" -H 'Content-Type: application/json' -d "$body" -w '\n%{http_code}' "$API_URL$p"
	else
		curl -sS -X "$m" -H "Authorization: Bearer $t" -w '\n%{http_code}' "$API_URL$p"
	fi
}
api_body() { api "$@" | sed '$d'; }
api_code() { api "$@" | tail -1; }
jsonq() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))' "$1"; }

project_json() { "$REPOSE" status --json --project "$PROJECT"; }
project_id() { project_json | jsonq 'd["id"]'; }
project_state() { project_json | jsonq 'd["state"]'; }
# guest_id from repose-admin (the Project object has no guest id).
guest_id() { admin projects show "$PROJECT" | awk '$1=="guest_id"{print $2}'; }

# wait_op <project id> <op id> [timeout s]: poll until done or error.
wait_op() {
	local pid=$1 op=$2 t=${3:-1800} s state
	for ((s = 0; s < t; s += 5)); do
		state=$(api_body GET "/projects/$pid/ops/$op" | jsonq 'd["state"]')
		case $state in
		done) log "op $op done after ${s}s"; return 0 ;;
		error) api_body GET "/projects/$pid/ops/$op" | evidence "op $op error"; return 1 ;;
		esac
		sleep 5
	done
	fail "op $op still $state after ${t}s"
}
# wait_state <state> [timeout s]
wait_state() {
	local want=$1 t=${2:-600} s st
	for ((s = 0; s < t; s += 5)); do
		st=$(project_state)
		[ "$st" = "$want" ] && { log "$PROJECT is $want after ${s}s"; return 0; }
		sleep 5
	done
	fail "$PROJECT is $st, not $want, after ${t}s"
}
# stream_op_log <project id> <op id> <file>: the SSE build log to a file
# (the same route the CLI and the dashboard read, 05 §5.4).
stream_op_log() {
	local t
	t=$(token)
	curl -sS -N -H "Authorization: Bearer $t" "$API_URL/projects/$1/ops/$2/log" >"$3" 2>&1 || true
}

# wait_idle <project id> [timeout s]: until the project has no pending or
# running op. A secrets push is an op (DECISIONS I-70), so an exec or a
# lifecycle request right after `repose secrets set|rm` answers "an
# operation is in progress" unless it waits.
wait_idle() {
	local pid=$1 t=${2:-120} s n
	for ((s = 0; s < t; s += 3)); do
		n=$(psql_q "select count(*) from ops where project_id='$pid' and state in ('pending','running')")
		[ "$n" = "0" ] && return 0
		sleep 3
	done
	fail "$pid still has an op in flight after ${t}s"
}

