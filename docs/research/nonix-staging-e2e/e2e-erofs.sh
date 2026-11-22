#!/usr/bin/env bash
# PR 9 end to end on staging (host-stg-01) for erofs guests, mirroring the
# Nix baseline (briefs/nonix-baseline-nix-staging.md) so the numbers line up.
# Driven by the staging CLI (~/repose-ws/repose-stg) and the staging admin
# tool (stg-admin on stg-edge). Every step prints its wall time and rc, and
# writes its output to $RUN/<step>.log. The run is resumable by step name.
#
#   e2e-erofs.sh --list                 the steps, and which are done
#   e2e-erofs.sh                        every step not yet done, in order
#   e2e-erofs.sh --from 08-docker       from that step on (done ones skipped)
#   e2e-erofs.sh --only 10-config-add   that step alone, done or not
#   e2e-erofs.sh --redo 05-agents       forget that step's done mark, run it
#   RUN_ID=... picks the run directory (default: the latest, else a new one)
#
# Preconditions (step 00 checks them): an erofs base published and
# settings.default_guest_runtime = erofs (publish-erofs.sh --default), host
# features include erofs, no project of stgdev alive. Step 15 needs the
# second release in BASE2_* (see that step). At most two projects are alive
# at any time: A=e2ea, B=e2eb or e2efork.
set -uo pipefail

R=/home/azureuser/repose-ws/repose-stg
SSHCFG=/home/azureuser/repose-ws/staging-ssh-config
SH_HOME=/home/azureuser/.rstg
BASE=/mnt/nixstore/tmp/stg-cp/e2e
SYNC_SRC=/mnt/nixstore/tmp/stg-cp/syncrepo # the same 51-file, 208 KB repo the Nix baseline synced
CHECKS=/mnt/nixstore/tmp/stg-cp/checks     # the isolation check adapted for staging
A=e2ea B=e2eb FORK=e2efork
export REPOSE_NO_SPINNER=1

STEPS=(00-preflight 01-creates 02-stopstart 03-ssh-exec 04-sync 05-agents 06-notify-ask
	07-claude-share 08-docker 09-browser 10-config-add 11-resize 12-snapshot-restore-fork
	13-pause-wake 14-park 15-base-rotate 16-isolation 17-cleanup)

mkdir -p "$BASE"
if [ -z "${RUN_ID:-}" ]; then
	RUN_ID=$(ls -1 "$BASE" 2>/dev/null | sort | tail -1)
	[ -n "$RUN_ID" ] || RUN_ID=$(date -u +%Y%m%dT%H%M%SZ)
fi
RUN=$BASE/$RUN_ID
mkdir -p "$RUN/done" "$RUN/work"
SUMMARY=$RUN/summary.txt

# --- helpers -----------------------------------------------------------
adm() { local q="" a; for a in "$@"; do q="$q $(printf '%q' "$a")"; done; ssh -F "$SSHCFG" stg-edge "/var/lib/repose-staging/admin$q"; }
psql_q() { ssh -F "$SSHCFG" stg-edge "/var/lib/repose-staging/psql -At -F'|' -c $(printf '%q' "$1")"; }
hostsh() { ssh -F "$SSHCFG" stg-host-wg "$@"; }
gx() { local p=$1; shift; "$R" exec "$p" -- sh -c "$*"; } # a shell line in a guest as dev
now() { date +%s.%N; }
since() { echo "$(now) - $1" | bc; }
# t LABEL CMD...: run, print "LABEL wall=S rc=N", and record it.
t() {
	local label=$1; shift
	local s rc
	s=$(now); "$@"; rc=$?
	printf '[%s] %s wall=%.2fs rc=%d\n' "$CUR" "$label" "$(since "$s")" "$rc" | tee -a "$SUMMARY"
	return $rc
}
note() { printf '[%s] %s\n' "$CUR" "$*" | tee -a "$SUMMARY"; }
check() { # check LABEL CONDITION-COMMAND...
	local label=$1; shift
	if "$@"; then note "ok: $label"; else note "FAIL: $label"; FAILED=1; fi
}
close_cm() { for s in "$SH_HOME"/.ssh/repose/cm-*; do [ -S "$s" ] && HOME=$SH_HOME ssh -F "$SH_HOME/.ssh/repose/config" -S "$s" -O exit x >/dev/null 2>&1; done; true; }
alive() { "$R" ls 2>/dev/null | awk 'NR>1{print $1}'; }
wait_gone() { local k; for k in $(seq 1 90); do alive | grep -qx "$1" || return 0; sleep 2; done; return 1; }
status_json() { "$R" status --json --project "$1"; }
jget() { python3 -c 'import json,sys; d=json.load(sys.stdin); v=eval(sys.argv[1]); print("" if v is None else v)' "$1"; }
guest_id() { adm projects show "$1" | awk '$1=="guest_id"{print $2}'; }
host_timings() { # host_timings SINCE-UTC FILE: boot/stop timings lines since then
	hostsh "journalctl -u hostd -o cat --no-pager --since '$1' | grep -E '\"(boot|stop) timings\"|restore_done|snapshot_done|guest_park|guest_unpark|guest_resting|image'" >"$2"
}
ops_rows() { # ops_rows SINCE-UTC FILE
	psql_q "select o.kind,o.state,coalesce(p.slug,'-'),extract(epoch from o.created_at)::numeric(14,3),extract(epoch from o.started_at)::numeric(14,3),extract(epoch from o.finished_at)::numeric(14,3),coalesce(o.error::text,'') from ops o left join projects p on p.id=o.project_id where o.created_at >= '$1' order by o.created_at" >"$2"
}
utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }
stats() { # stats NAME FILE-OF-NUMBERS UNIT
	python3 - "$1" "$2" "$3" <<'EOF' | tee -a "$SUMMARY"
import sys
name, f, unit = sys.argv[1:]
v = sorted(float(x) for x in open(f).read().split())
def pct(p):
    if not v: return float('nan')
    k = (len(v)-1)*p; i = int(k); j = min(i+1, len(v)-1); return v[i]+(v[j]-v[i])*(k-i)
print(f"| {name} | {len(v)} | {pct(.5):.1f} | {pct(.95):.1f} | {v[0] if v else 'nan'} | {v[-1] if v else 'nan'} | {unit} |")
EOF
}
timing_stats() { # timing_stats JSONL OP PREFIX [N]: per-field p50/p95 of hostd boot timings
	python3 - "$1" "$2" "$3" "${4:-0}" <<'EOF' | tee -a "$SUMMARY"
import json, sys
f, op, prefix, n = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
rows = []
for l in open(f):
    try: d = json.loads(l)
    except Exception: continue
    if d.get("msg") == "boot timings" and d.get("op") == op and d.get("result", "running") == "running":
        rows.append(d)
if n: rows = rows[:n]
keys = sorted({k for r in rows for k in r if k.endswith("_ms")})
def pct(v, p):
    v = sorted(v); k = (len(v)-1)*p; i = int(k); j = min(i+1, len(v)-1); return v[i]+(v[j]-v[i])*(k-i)
for k in keys:
    v = [r.get(k, 0) for r in rows]
    print(f"| {prefix} hostd {op} {k} | {len(v)} | {pct(v,.5):.0f} | {pct(v,.95):.0f} | {min(v)} | {max(v)} | ms |")
EOF
}
op_stats() { # op_stats CSV KIND PREFIX [N]
	awk -F'|' -v k="$2" '$1==k && $6!="" {print $6-$4}' "$1" | head -n "${4:-1000}" >"$RUN/work/op-$2.txt"
	stats "$3 op $2 created->finished" "$RUN/work/op-$2.txt" s
}

# --- steps -------------------------------------------------------------
step_00-preflight() {
	t "settings get default_guest_runtime" adm settings get default_guest_runtime | tee "$RUN/work/runtime.txt"
	check "default_guest_runtime is erofs" grep -qx erofs "$RUN/work/runtime.txt"
	t "base list" adm base list | tee "$RUN/work/bases.txt"
	check "an erofs base is published" grep -qw erofs "$RUN/work/bases.txt"
	psql_q "select name, features, arch from hosts" | tee "$RUN/work/hosts.txt"
	check "host-stg-01 reports erofs" grep -q 'erofs' "$RUN/work/hosts.txt"
	hostsh 'hostd status; ls -la /var/lib/repose/images' | tee "$RUN/work/host.txt"
	check "no project alive" test -z "$(alive)"
}

step_01-creates() {
	local from i out rc s slug
	from=$(utc); mkdir -p "$RUN/work/p"; cd "$RUN/work/p" || return 1
	: >"$RUN/work/c-booted.txt"; : >"$RUN/work/c-ready.txt"; : >"$RUN/work/c-wall.txt"
	for i in $(seq 1 10); do
		s=$(now); out=$(timeout 900 "$R" run --temp --size small -d 2>&1); rc=$?
		local w; w=$(since "$s")
		slug=$(printf '%s\n' "$out" | sed -n 's/.*Created \(tmp-[a-z0-9]*\).*/\1/p' | head -1)
		local booted ready
		booted=$(printf '%s\n' "$out" | sed -n 's/.*Booted [^ ]* *\([0-9.]*\)s.*/\1/p' | head -1)
		ready=$(printf '%s\n' "$out" | sed -n 's/^Ready in \([0-9.]*\)s\.$/\1/p' | head -1)
		note "create $i $slug wall=${w}s rc=$rc booted=${booted}s ready=${ready}s"
		[ $rc -eq 0 ] || { printf '%s\n' "$out"; FAILED=1; }
		echo "$booted" >>"$RUN/work/c-booted.txt"; echo "$ready" >>"$RUN/work/c-ready.txt"; echo "$w" >>"$RUN/work/c-wall.txt"
		[ "$i" = 1 ] && [ -n "$slug" ] && gx "$slug" 'cat /etc/os-release | head -2; uname -r' | tee "$RUN/work/c-os.txt"
		[ -n "$slug" ] && "$R" rm -y "$slug" >/dev/null 2>&1 && wait_gone "$slug"
		sleep 5
	done
	host_timings "$from" "$RUN/work/c-host.jsonl"; ops_rows "$from" "$RUN/work/c-ops.csv"
	echo "| measure | n | p50 | p95 | min | max | unit |" | tee -a "$SUMMARY"
	stats "CLI create Booted" "$RUN/work/c-booted.txt" s
	stats "CLI create Ready in" "$RUN/work/c-ready.txt" s
	stats "CLI create wall" "$RUN/work/c-wall.txt" s
	timing_stats "$RUN/work/c-host.jsonl" create creates 10
	op_stats "$RUN/work/c-ops.csv" create creates 10
	op_stats "$RUN/work/c-ops.csv" destroy creates 10
}

step_02-stopstart() {
	local from i s out rc
	mkdir -p "$RUN/work/a"; cd "$RUN/work/a" || return 1
	alive | grep -qx "$A" || t "run $A" "$R" run "$A" --size small -d
	status_json "$A" | jget 'd.get("guest_runtime")' | tee "$RUN/work/a-runtime.txt"
	check "$A is an erofs machine" grep -qx erofs "$RUN/work/a-runtime.txt"
	from=$(utc); : >"$RUN/work/s-booted.txt"; : >"$RUN/work/s-ready.txt"; : >"$RUN/work/s-start.txt"; : >"$RUN/work/s-stop.txt"
	for i in $(seq 1 10); do
		close_cm
		s=$(now); "$R" stop -y --wait "$A" >/dev/null 2>&1; rc=$?; echo "$(since "$s")" >>"$RUN/work/s-stop.txt"
		[ $rc -eq 0 ] || { note "stop $i rc=$rc"; FAILED=1; }
		s=$(now); out=$(timeout 900 "$R" start "$A" 2>&1); rc=$?; echo "$(since "$s")" >>"$RUN/work/s-start.txt"
		printf '%s\n' "$out" | sed -n 's/.*Booted [^ ]* *\([0-9.]*\)s.*/\1/p' | head -1 >>"$RUN/work/s-booted.txt"
		printf '%s\n' "$out" | sed -n 's/.*ready in \([0-9.]*\)s.*/\1/p' | head -1 >>"$RUN/work/s-ready.txt"
		note "cycle $i start rc=$rc $(printf '%s' "$out" | tr '\n' '|' | cut -c1-200)"
		[ $rc -eq 0 ] || FAILED=1
		sleep 5
	done
	host_timings "$from" "$RUN/work/s-host.jsonl"; ops_rows "$from" "$RUN/work/s-ops.csv"
	echo "| measure | n | p50 | p95 | min | max | unit |" | tee -a "$SUMMARY"
	stats "CLI start Booted" "$RUN/work/s-booted.txt" s
	stats "CLI start ready in" "$RUN/work/s-ready.txt" s
	stats "CLI start wall" "$RUN/work/s-start.txt" s
	stats "CLI stop --wait wall" "$RUN/work/s-stop.txt" s
	timing_stats "$RUN/work/s-host.jsonl" start stopstart
	grep '"stop timings"' "$RUN/work/s-host.jsonl" | python3 -c 'import json,sys; [print(json.loads(l)["total_ms"]) for l in sys.stdin]' >"$RUN/work/s-stopms.txt"
	stats "hostd stop timings total_ms" "$RUN/work/s-stopms.txt" ms
	op_stats "$RUN/work/s-ops.csv" start stopstart
	op_stats "$RUN/work/s-ops.csv" stop stopstart
}

step_03-ssh-exec() {
	cd "$RUN/work/a" || return 1
	t "ssh $A" bash -c "echo 'uname -a; head -2 /etc/os-release; exit' | timeout 60 $R ssh $A 2>&1 | grep -v Pseudo | tr -d '\033'"
	t "exec $A uname -a" "$R" exec "$A" -- uname -a
	t "exec $A (warm)" "$R" exec "$A" -- true
	grep HostName "$SH_HOME/.ssh/repose/hosts" | sort -u | tee -a "$SUMMARY"
}

step_04-sync() {
	local d=$RUN/work/syncrepo
	if [ ! -d "$d/.git" ]; then rm -rf "$d"; git clone -q "$SYNC_SRC" "$d" && git -C "$d" remote remove origin; fi
	cd "$d" || return 1
	alive | grep -qx "$B" || t "run $B (first sync of the 51-file repo)" "$R" run "$B" --size small -d
	echo change >>f1.txt; git commit -qam "edit f1"; echo dirty >>f2.txt; echo new >new.txt
	t "sync (1 commit, 1 dirty, 1 untracked)" "$R" sync
	gx "$B" 'git log --oneline | head -2; git status --short; cat new.txt' | tee -a "$RUN/work/sync.txt"
	check "the commit, the dirty file and the untracked file arrived" bash -c "grep -q 'edit f1' $RUN/work/sync.txt && grep -q 'M f2.txt' $RUN/work/sync.txt && grep -q '^new$' $RUN/work/sync.txt"
	t "sync (nothing new)" "$R" sync
}

step_05-agents() {
	local ag session
	cd "$RUN/work/a" || return 1
	session=$A
	for ag in claude codex opencode gemini pi; do
		t "$ag --version" "$R" exec "$A" -- sh -lc "$ag --version 2>&1 | head -3"
		gx "$A" "tmux new-window -d -t '$session' -n '$ag' '$ag'" || note "could not open a window for $ag"
	done
	sleep 20
	gx "$A" "tmux list-windows -t '$session' -F '#{window_name} #{pane_current_command} #{pane_dead}'" | tee "$RUN/work/agents.txt"
	for ag in claude codex opencode gemini pi; do
		check "$ag runs in its window" bash -c "awk '\$1==\"$ag\" && \$3==0' $RUN/work/agents.txt | grep -q ."
		gx "$A" "tmux capture-pane -p -t '$session:$ag' | tail -5" >"$RUN/work/agent-$ag.txt" 2>&1
		gx "$A" "tmux kill-window -t '$session:$ag'" >/dev/null 2>&1
	done
}

step_06-notify-ask() {
	local from pid tag q
	cd "$RUN/work/a" || return 1
	from=$(utc); tag="e2e-notify-$(date +%s)"
	pid=$(status_json "$A" | jget 'd["id"]')
	t "repose-notify in the guest" gx "$A" "repose-notify '$tag'"
	sleep 5
	psql_q "select ts, kind, agent, summary from events where project_id='$pid' and ts >= '$from' order by ts" | tee "$RUN/work/notify-events.txt"
	check "the notify reached the api as an event" grep -q "$tag" "$RUN/work/notify-events.txt"
	"$R" events --since 10m --project "$A" 2>&1 | tail -5 | tee -a "$SUMMARY"
	# repose-ask waits for the answer; it runs in a tmux window and writes it to a file.
	gx "$A" "tmux new-window -d -t '$A' -n ask 'repose-ask --options yes,no \"e2e ask round trip?\" > /tmp/e2e-ask.out 2>&1; sleep 600'"
	local s=$(now) k
	for k in $(seq 1 30); do "$R" questions "$A" 2>&1 | grep -q 'e2e ask round trip' && break; sleep 2; done
	note "question visible after $(since "$s")s"
	"$R" questions "$A" 2>&1 | tee -a "$SUMMARY"
	t "repose reply $A yes" "$R" reply "$A" yes
	for k in $(seq 1 30); do gx "$A" 'test -s /tmp/e2e-ask.out' && break; sleep 1; done
	q=$(gx "$A" 'cat /tmp/e2e-ask.out'); note "repose-ask printed: $q"
	check "repose-ask received yes" bash -c "printf '%s' '$q' | grep -qx yes"
	note "ask round trip $(since "$s")s"
	gx "$A" "tmux kill-window -t '$A:ask'" >/dev/null 2>&1
}

step_07-claude-share() {
	# The share is one file per user per host, ~/.claude/.credentials.json,
	# bind-mounted from /run/repose/claude-auth (nix base: claude-auth.nix;
	# I-278). A and B are both stgdev's. No login exists (it needs the
	# owner), so the file is empty: a marker goes in through A and is read
	# through B, then the file is emptied again.
	cd "$RUN/work/a" || return 1
	alive | grep -qx "$B" || { cd "$RUN/work/syncrepo" && t "run $B" "$R" run "$B" --size small -d; cd "$RUN/work/a" || return 1; }
	local m="e2e-share-$(date +%s)"
	gx "$A" 'findmnt -n ~/.claude/.credentials.json; findmnt -n /run/repose/claude-auth; ls -la ~/.claude/' | tee "$RUN/work/share-a.txt"
	gx "$B" 'findmnt -n ~/.claude/.credentials.json; findmnt -n /run/repose/claude-auth' | tee "$RUN/work/share-b.txt"
	check "the share is mounted in A" grep -q credentials "$RUN/work/share-a.txt"
	check "the share is mounted in B" grep -q credentials "$RUN/work/share-b.txt"
	gx "$A" "test -s ~/.claude/.credentials.json && echo NOT-EMPTY || printf '%s' '$m' > ~/.claude/.credentials.json"
	local got; got=$(gx "$B" 'cat ~/.claude/.credentials.json')
	note "B reads: $got"
	check "a write in A's ~/.claude/.credentials.json is seen in B's" test "$got" = "$m"
	gx "$A" "[ \"\$(cat ~/.claude/.credentials.json)\" = '$m' ] && : > ~/.claude/.credentials.json; wc -c < ~/.claude/.credentials.json"
}

step_08-docker() {
	cd "$RUN/work/a" || return 1
	t "docker run --rm alpine (network)" "$R" exec "$A" -- sh -c 'docker run --rm alpine sh -c "wget -qO- https://example.com | head -c 80; echo"'
	gx "$A" 'mkdir -p ~/e2e-compose && cat > ~/e2e-compose/compose.yaml <<EOF
services:
  web:
    image: nginx:alpine
  client:
    image: alpine
    depends_on: [web]
    command: sh -c "sleep 2; wget -qO- http://web/ | grep -o \"Welcome to nginx\""
EOF'
	t "docker compose up (two services)" "$R" exec "$A" -- sh -c 'cd ~/e2e-compose && docker compose up --abort-on-container-exit --exit-code-from client 2>&1 | tail -5; rc=$?; docker compose down -v >/dev/null 2>&1; exit $rc'
}

step_09-browser() {
	local out port
	cd "$RUN/work/a" || return 1
	t "Chromium CDP /json/version in the guest" "$R" exec "$A" -- sh -c 'curl -s --max-time 30 http://127.0.0.1:9224/json/version | head -c 300; echo'
	out=$(HOME=$SH_HOME timeout 120 "$R" browser "$A" --no-browser 2>&1); note "repose browser: $(printf '%s' "$out" | sed 's/#.*/#<password>/' | tr '\n' '|')"
	port=$(printf '%s' "$out" | grep -o '127.0.0.1:[0-9]*' | head -1 | cut -d: -f2); port=${port:-6080}
	t "viewer /healthz on laptop port $port" curl -s --max-time 10 "http://127.0.0.1:$port/healthz"
	echo | tee -a "$SUMMARY"
	t "repose browser stop" "$R" browser stop "$A"
}

step_10-config-add() {
	cd "$RUN/work/a" || return 1
	t "config add postgresql redis bun awscli" timeout 1800 "$R" config add postgresql redis bun awscli
	t "versions" "$R" exec "$A" -- sh -lc 'psql --version; redis-cli --version; bun --version; aws --version'
	gx "$A" "psql -c 'create table if not exists e2e(v text); insert into e2e values (''kept-across-restarts'');' 2>&1 || sudo -u postgres psql -c 'create table if not exists e2e(v text); insert into e2e values (''kept-across-restarts'');'" | tee -a "$SUMMARY"
	close_cm
	t "stop --wait" "$R" stop -y --wait "$A"
	t "start" "$R" start "$A"
	t "versions after stop/start" "$R" exec "$A" -- sh -lc 'psql --version; redis-cli ping; bun --version; aws --version'
	gx "$A" "psql -Atc 'select v from e2e' 2>/dev/null || sudo -u postgres psql -Atc 'select v from e2e'" | tee "$RUN/work/pg.txt"
	check "PostgreSQL data survived stop/start" grep -q kept-across-restarts "$RUN/work/pg.txt"
	"$R" config show "$A" 2>&1 | tee -a "$SUMMARY"
}

step_11-resize() {
	local cur new
	cd "$RUN/work/a" || return 1
	gx "$A" 'df -h / /home 2>/dev/null' | tee -a "$SUMMARY"
	cur=$(status_json "$A" | jget 'd["volume_bytes"]')
	new=$(( cur / 1073741824 + 10 ))G
	t "resize $A $new (running)" "$R" resize "$A" "$new"
	gx "$A" 'df -h / /home 2>/dev/null' | tee "$RUN/work/df-after.txt"
	cat "$RUN/work/df-after.txt" >>"$SUMMARY"
}

step_12-snapshot-restore-fork() {
	local sid
	cd "$RUN/work/a" || return 1
	# Two alive at most: B goes before the fork.
	alive | grep -qx "$B" && { t "rm $B" "$R" rm -y "$B"; wait_gone "$B"; }
	gx "$A" 'echo e2e-marker-$(date +%s) > ~/marker.txt; head -c 200000000 /dev/urandom > ~/blob.bin; sync; cat ~/marker.txt' | tee "$RUN/work/marker.txt"
	t "snapshots create" "$R" snapshots create "$A"
	sid=$("$R" snapshots list "$A" 2>/dev/null | awk 'NR==2{print $1}')
	t "fork: snapshots restore $sid --as $FORK" "$R" snapshots restore "$A" "$sid" --as "$FORK" -y
	gx "$FORK" 'cat ~/marker.txt; ls -la ~/blob.bin' | tee "$RUN/work/fork.txt"
	check "fork has the marker" grep -qf "$RUN/work/marker.txt" "$RUN/work/fork.txt"
	status_json "$FORK" | jget 'd.get("guest_runtime")' | tee -a "$SUMMARY"
	t "rm $FORK" "$R" rm -y "$FORK"; wait_gone "$FORK"
	t "rm $A" "$R" rm -y "$A"
	local s=$(now); wait_gone "$A"; note "$A gone from ls after $(since "$s")s"
	t "restore $A" "$R" restore "$A"
	gx "$A" 'cat ~/marker.txt; ls -la ~/blob.bin' | tee "$RUN/work/restore.txt"
	check "restore has the marker" grep -qf "$RUN/work/marker.txt" "$RUN/work/restore.txt"
	gx "$A" "psql -Atc 'select v from e2e' 2>/dev/null || sudo -u postgres psql -Atc 'select v from e2e'" | tee "$RUN/work/pg-restore.txt"
	check "restore has the PostgreSQL row" grep -q kept-across-restarts "$RUN/work/pg-restore.txt"
	hostsh "journalctl -u hostd -o cat --no-pager --since -10min | grep -E 'snapshot_done|restore_done' | cut -c1-400" | tee -a "$SUMMARY"
}

step_13-pause-wake() {
	local g s
	cd "$RUN/work/a" || return 1
	close_cm; g=$(guest_id "$A")
	hostsh "for i in 1 2 3 4 5 6; do r=\$(hostd pause $g); echo \"\$(date +%T) \$r\"; [ \"\$r\" = paused ] && break; sleep 10; done; s=\$(date +%s%N); hostd wake $g; echo wake_wall_ms=\$(( (\$(date +%s%N)-s)/1000000 )); hostd pause $g" | tee -a "$SUMMARY"
	s=$(now); "$R" exec "$A" -- true; note "exec on a paused machine rc=$? wall=$(since "$s")s"
	s=$(now); "$R" exec "$A" -- true; note "exec on a running machine wall=$(since "$s")s"
	hostsh "journalctl -u hostd -o cat --no-pager --since -3min | grep -E 'guest_resting' | cut -c1-300" | tee -a "$SUMMARY"
}

step_14-park() {
	cd "$RUN/work/a" || return 1
	gx "$A" 'nohup sleep 100000 >/dev/null 2>&1 & echo $! > ~/sleep.pid; cat ~/sleep.pid'
	close_cm
	hostsh 's=$(date +%s%N); hostd park; echo park_wall_ms=$(( ($(date +%s%N)-s)/1000000 )); sleep 3; s=$(date +%s%N); hostd unpark; echo unpark_wall_ms=$(( ($(date +%s%N)-s)/1000000 )); hostd guests' | tee -a "$SUMMARY"
	gx "$A" 'ps -o pid,etime,cmd -p $(cat ~/sleep.pid); cat ~/marker.txt' | tee "$RUN/work/park.txt"
	check "a process survived the park" grep -q 'sleep 100000' "$RUN/work/park.txt"
	hostsh "journalctl -o cat --no-pager --since -3min | grep -E 'guest_park|guest_unpark' | grep -v command_ | cut -c1-400" | tee -a "$SUMMARY"
}

step_15-base-rotate() {
	# Needs the second release: BASE2_RELEASE_URL, BASE2_VERSION,
	# BASE2_KERNEL_VERSION, BASE2_CHANGELOG (and the file names, as
	# publish-erofs.sh reads them).
	cd "$RUN/work/a" || return 1
	: "${BASE2_VERSION:?set BASE2_VERSION, BASE2_RELEASE_URL, BASE2_KERNEL_VERSION, BASE2_CHANGELOG for the second base}"
	local before
	before=$(status_json "$A" | jget 'd.get("base_version")'); note "base before: $before"
	gx "$A" 'echo home-kept-$(date +%s) > ~/home-marker.txt; cat ~/home-marker.txt' | tee "$RUN/work/home-marker.txt"
	t "publish base $BASE2_VERSION" env RELEASE_URL="$BASE2_RELEASE_URL" VERSION="$BASE2_VERSION" KERNEL_VERSION="$BASE2_KERNEL_VERSION" CHANGELOG="$BASE2_CHANGELOG" \
		/mnt/nixstore/tmp/stg-cp/publish-erofs.sh
	status_json "$A" | jget 'd.get("pending_base_version")' | tee "$RUN/work/pending.txt"
	check "pending_base_version is $BASE2_VERSION" grep -qx "$BASE2_VERSION" "$RUN/work/pending.txt"
	close_cm
	t "stop --wait" "$R" stop -y --wait "$A"
	t "start (boots the new base)" "$R" start "$A"
	status_json "$A" | jget 'd.get("base_version")' | tee "$RUN/work/after.txt"
	check "base_version is now $BASE2_VERSION" grep -qx "$BASE2_VERSION" "$RUN/work/after.txt"
	gx "$A" 'cat ~/home-marker.txt ~/marker.txt; uname -r; head -2 /etc/os-release' | tee "$RUN/work/after-home.txt"
	check "/home/dev intact" grep -qf "$RUN/work/home-marker.txt" "$RUN/work/after-home.txt"
	gx "$A" "psql -Atc 'select v from e2e' 2>/dev/null || sudo -u postgres psql -Atc 'select v from e2e'" | tee "$RUN/work/pg-after.txt"
	check "PostgreSQL data intact" grep -q kept-across-restarts "$RUN/work/pg-after.txt"
	t "menu replayed: versions" "$R" exec "$A" -- sh -lc 'psql --version && redis-cli ping && bun --version && aws --version'
	hostsh "journalctl -u hostd -o cat --no-pager --since -10min | grep -iE 'menu|image|boot timings' | cut -c1-400 | tail -12" | tee -a "$SUMMARY"
}

step_16-isolation() {
	# The adapted copy of ops/checks/isolation-host01.sh ($CHECKS), with
	# A = e2ea (stgdev, CLI) and B = e2eiso (stgb, admin-created; the
	# default runtime makes it erofs too).
	cd "$RUN/work/a" || return 1
	alive | grep -qx "$A" || { note "FAIL: $A is not alive"; FAILED=1; return 1; }
	adm projects list | grep -q ' e2eiso ' || t "admin projects create e2eiso (user stgb)" adm projects create --user stgb --name e2eiso --class small --create-user --wait
	t "isolation-host01.sh (staging copy)" env HOME=$SH_HOME TMPDIR=/mnt/nixstore/tmp/stg-cp GOPATH=/home/azureuser/go GOCACHE=/home/azureuser/.cache/go-build \
		GOMODCACHE=/home/azureuser/go/pkg/mod GOFLAGS=-p=2 PROJECT=$A PROJECT_B=e2eiso LOGIN_B=e2eiso.stgb OUT=$RUN/isolation REVOKE_A=1 \
		timeout 2400 bash "$CHECKS/isolation-host01.sh" >"$RUN/work/isolation.txt" 2>&1
	grep -A30 '### row summary' "$RUN/work/isolation.txt" | head -25 | tee -a "$SUMMARY"
	adm projects destroy e2eiso >/dev/null 2>&1
}

step_17-cleanup() {
	local p
	for p in $(alive); do "$R" rm -y "$p" >/dev/null 2>&1; done
	adm projects list | awk 'NR>1{print $2}' | while read -r p; do adm projects destroy "$p" >/dev/null 2>&1; done
	for p in $(alive); do wait_gone "$p"; done
	adm projects list | tee -a "$SUMMARY"
	hostsh 'hostd guests' | tee -a "$SUMMARY"
	# Snapshots of destroyed projects go through the api's own expiry,
	# which deletes their blobs (as after the Nix baseline).
	psql_q "update snapshots s set expires_at = now() - interval '1 minute' from projects p where p.id=s.project_id and p.destroyed_at is not null and s.deleted_at is null"
	ssh -F "$SSHCFG" stg-edge 'systemctl restart stg-api; sleep 15; /var/lib/repose-staging/psql -Atc "select count(*) filter (where deleted_at is null), count(*) from snapshots"' | tee -a "$SUMMARY"
}

# --- driver ------------------------------------------------------------
list() { local s; for s in "${STEPS[@]}"; do [ -e "$RUN/done/$s" ] && echo "done  $s" || echo "      $s"; done; echo "run: $RUN"; }
runstep() {
	CUR=$1; FAILED=0
	local s rc
	printf '\n== %s %s\n' "$(utc)" "$CUR" | tee -a "$SUMMARY"
	s=$(now)
	local before; before=$(wc -l <"$SUMMARY" 2>/dev/null || echo 0)
	( "step_$CUR" ) 2>&1 | tee "$RUN/$CUR.log"
	rc=${PIPESTATUS[0]}
	# A failed check fails the step even when its last command exited 0.
	if tail -n +"$((before + 1))" "$SUMMARY" | grep -q "^\[$CUR\] FAIL"; then [ "$rc" -eq 0 ] && rc=1; fi
	printf '[%s] STEP wall=%.1fs rc=%d\n' "$CUR" "$(since "$s")" "$rc" | tee -a "$SUMMARY"
	[ "$rc" -eq 0 ] && touch "$RUN/done/$CUR"
	return "$rc"
}
from="" only="" stop_on_fail=1
while [ $# -gt 0 ]; do
	case $1 in
	--list) list; exit 0 ;;
	--from) from=$2; shift ;;
	--only) only=$2; shift ;;
	--redo) rm -f "$RUN/done/$2"; from=$2; shift ;;
	--keep-going) stop_on_fail=0 ;;
	*) echo "unknown argument $1" >&2; exit 2 ;;
	esac
	shift
done
if [ -n "$only" ]; then runstep "$only"; exit $?; fi
started=0; [ -z "$from" ] && started=1
for s in "${STEPS[@]}"; do
	[ "$s" = "$from" ] && started=1
	[ $started = 1 ] || continue
	[ -e "$RUN/done/$s" ] && { echo "skip $s (done)"; continue; }
	runstep "$s" || { [ $stop_on_fail = 1 ] && { echo "stopped at $s; fix and rerun (it resumes here)"; exit 1; }; }
done
echo "summary: $SUMMARY"
