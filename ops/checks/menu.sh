#!/usr/bin/env bash
# M3 step 3 (docs/workstreams/PROMPTS.md "M3 bring-up / m3"): the menu and
# the Nix pipeline on the real host through the api.
#
#   1. a catalog entry added through PUT /config {menu}, the build log
#      streamed over SSE, the package on PATH in the guest with no reboot
#      (boot_id and the tmux session unchanged);
#   2. GET /config shows the selection and the generated header;
#   3. takeover: the fragment edited and applied, then a menu PUT answers
#      409 "project uses a custom fragment";
#   4. the restricted-eval refusals on the real host, each with the exact
#      first line of interfaces/nix-build-contract.md: syntax error, missing
#      attribute, builtins.fetchurl, readFile /etc/passwd, import <nixpkgs>;
#   5. a fixed-output fetch with a hash builds (sandbox network);
#   6. GC roots after a build, the running closure live under
#      `nix-store --gc --print-dead`, and with --with-destroy a throwaway
#      project's roots gone after destroy;
#   7. with --with-closure-cap, a 21 GB fragment answers closure_too_large
#      with ten paths;
#   8. eval and build timings from hostd's build_done lines, for
#      docs/RESEARCH.md.
#
# kernel_changed for a base kernel bump is resilience.sh's base-bump section
# (it needs a published base whose kernel differs; README.md).
#
#   ops/checks/menu.sh [--with-destroy] [--with-closure-cap] [--with-build-timeout] [--only-closure-cap]
#   (PROJECT running; --with-build-timeout takes 30 minutes by design)
set -euo pipefail
check=menu
# shellcheck source-path=SCRIPTDIR
. "$(dirname "$0")/lib.sh"
frags="$checks_dir/fragments"
with_destroy=0
with_cap=0
with_timeout=0
only_cap=0
for a in "$@"; do
	case $a in
	--with-destroy) with_destroy=1 ;;
	--with-closure-cap) with_cap=1 ;;
	--with-build-timeout) with_timeout=1 ;;
	--only-closure-cap) with_cap=1; only_cap=1 ;;
	*) echo "usage: $0 [--with-destroy] [--with-closure-cap] [--with-build-timeout]" >&2; exit 2 ;;
	esac
done

pid=$(project_id)
[ "$(project_state)" = running ] || fail "$PROJECT is $(project_state), not running"
gid=$(guest_id)
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
log "project $pid guest $gid"

closure_cap_row() {
	set +e
	out=$("$REPOSE" config apply "$frags/closure-cap.nix" --project "$PROJECT" 2>&1)
	rc=$?
	set -e
	printf '%s\n' "$out" | grep -A12 'config too large' | evidence "closure cap (exit $rc): the first line and the ten largest paths"
	[ $rc -ne 0 ] || fail "the 21 GB fragment was accepted"
	printf '%s' "$out" | grep -Eq 'config too large: closure is [0-9.]+ GB, limit is 20 GB; largest paths:' || fail "closure_too_large message is not the documented one"
	assert_eq "ten paths listed" "$(printf '%s\n' "$out" | grep -A12 'largest paths:' | grep -c '/nix/store/')" "10"
	host_sh "ls /nix/var/nix/gcroots/repose/ | grep -c m3-twenty-one-gb || true; nix-collect-garbage >/dev/null 2>&1; ls /nix/store | grep -c m3-twenty-one-gb || true" | evidence "no GC root for the oversized result; store after nix-collect-garbage"
}
if [ $only_cap = 1 ]; then
	closure_cap_row
	log "closure cap row finished; evidence in $report"
	exit 0
fi

# A project that went through the takeover below is in fragment mode and
# refuses a menu PUT (409); the api's default fragment, applied verbatim,
# is what puts it back (internal/api/http DefaultFragment). Idempotent
# on a fresh project ("configuration unchanged").
"$REPOSE" config apply "$frags/default.nix" --project "$PROJECT" 2>&1 | tail -1 | evidence "reset to the default fragment (menu mode)"
before=$(guest_sh "$PROJECT" 'cat /proc/sys/kernel/random/boot_id; tmux list-sessions -F "#{session_name}" 2>/dev/null | head -1; command -v bun || echo no-bun')
boot0=$(printf '%s\n' "$before" | sed -n 1p)
printf '%s\n' "$before" | evidence "guest before: boot_id, tmux session, bun on PATH"

# 1. The catalog, and one package through the menu route.
cat=$(api_body GET /catalog)
printf '%s' "$cat" | python3 -c 'import json,sys; [print(e["id"], e["kind"], e["group"]) for e in json.load(sys.stdin)]' | evidence "GET /catalog (id kind group)"
resp=$(api_body PUT "/projects/$pid/config" '{"menu":[{"id":"bun"}]}')
printf '%s\n' "$resp" | evidence "PUT /projects/:id/config {menu:[{id:bun}]}"
op=$(printf '%s' "$resp" | jsonq 'd["op_id"]')
rev=$(printf '%s' "$resp" | jsonq 'd["revision_id"]')
log "menu revision $rev, op $op"
logf="$OUT/menu-buildlog-$stamp.sse"
stream_op_log "$pid" "$op" "$logf" &
sse=$!
# While it runs: the eval and build scopes and the user they run as (12 §9
# "The build runs as nixbuild, not root, inside a scope with the documented
# CPUQuota, MemoryMax, RuntimeMaxSec").
sleep 6
host_sh "for u in \$(systemctl list-units --type=scope --plain --no-legend 'repose-build-*' | awk '{print \$1}'); do echo \"== \$u\"; systemctl show \"\$u\" -p CPUQuotaPerSecUSec,MemoryMax,RuntimeMaxUSec,ControlGroup; done; echo '== nix processes (user pid comm)'; ps -eo user=,pid=,comm= | grep -E ' nix( |\$)|nix-build|nix eval' | head -5" | evidence "build scopes and processes on the host during op $op"
wait_op "$pid" "$op" 1800 || fail "the menu build failed"
sleep 2
kill $sse 2>/dev/null || true
wait $sse 2>/dev/null || true
{
	echo "lines: $(grep -c '^data:' "$logf")"
	echo "first:"; grep '^data:' "$logf" | head -3
	echo "last:"; grep '^data:' "$logf" | tail -3
	grep '^event: done' -A1 "$logf" | tail -2
} | evidence "SSE build log of op $op ($logf)"
after=$(guest_sh "$PROJECT" 'cat /proc/sys/kernel/random/boot_id; tmux list-sessions -F "#{session_name}" 2>/dev/null | head -1; bash -lc "command -v bun && bun --version"')
printf '%s\n' "$after" | evidence "guest after: boot_id, tmux session, bun on PATH and its version"
assert_eq "boot_id unchanged (no reboot)" "$(printf '%s\n' "$after" | sed -n 1p)" "$boot0"
assert_eq "tmux session kept" "$(printf '%s\n' "$after" | sed -n 2p)" "$(printf '%s\n' "$before" | sed -n 2p)"
printf '%s' "$after" | sed -n 3p | grep -q '/bun$' || fail "bun is not on PATH in a new login shell"

# 2. The stored config: the selection and the generated header.
cfg=$(api_body GET "/projects/$pid/config")
printf '%s' "$cfg" | python3 -c 'import json,sys; d=json.load(sys.stdin); print("revision", d["revision_id"]); print("menu", json.dumps(d.get("menu"))); print("base", d.get("base_version")); print("fragment head:"); print("\n".join(d["fragment"].splitlines()[:4]))' | evidence "GET /projects/:id/config after the menu apply"
printf '%s' "$cfg" | jsonq 'd["fragment"].splitlines()[0]' | grep -q '^# generated by repose from your menu selection' || fail "the fragment does not start with the generated header"
printf '%s' "$cfg" | jsonq 'd["fragment"].splitlines()[1]' | grep -q '^# repose-menu: ' || fail "the fragment's second line is not the selection"

# 3. Takeover: edit the generated fragment (a comment before the header ends
#    menu mode, features/config.md "Menu versus fragment"), apply it, then a
#    menu PUT is refused with the documented conflict.
gen=$(mktemp --suffix=.nix)
{ printf '# taken over by ops/checks/menu.sh on %s\n' "$stamp"; printf '%s' "$cfg" | jsonq 'd["fragment"]'; } >"$gen"
"$REPOSE" config apply "$gen" --project "$PROJECT" 2>&1 | tail -5 | evidence "repose config apply of the edited fragment (takeover)"
rm -f "$gen"
conflict=$(api PUT "/projects/$pid/config" '{"menu":[{"id":"bun"},{"id":"deno"}]}')
printf '%s\n' "$conflict" | evidence "PUT {menu} after the takeover"
assert_eq "takeover conflict status" "$(printf '%s\n' "$conflict" | tail -1)" "409"
printf '%s' "$conflict" | grep -q 'project uses a custom fragment; use fragment mode or reset' || fail "the conflict message is not the documented one"

# 4. The refusals, each with the contract's first line.
refuse() { # <fragment file> <expected first-line regex>
	local f=$1 want=$2 out rc first
	set +e
	out=$("$REPOSE" config apply "$f" --project "$PROJECT" 2>&1)
	rc=$?
	set -e
	first=$(printf '%s\n' "$out" | grep -m1 'config error:' || printf '%s\n' "$out" | head -1)
	{ echo "fragment: $(cat "$f")"; echo "exit: $rc"; echo "first line: $first"; printf '%s\n' "$out" | sed -n 2,8p; } | evidence "refusal: $(basename "$f")"
	[ $rc -ne 0 ] || fail "$(basename "$f") was accepted"
	printf '%s' "$first" | grep -Eq -- "$want" || fail "$(basename "$f"): first line '$first' does not match '$want'"
}
# The CLI prints the local file's name in place of fragment.nix (07).
if [ "${SKIP_SYNTAX_ROW:-0}" = 1 ]; then
	echo "syntax.nix: SKIPPED by SKIP_SYNTAX_ROW (the deployed api predates I-126 and words the parse-time error differently)" | evidence "refusal: syntax.nix"
else
	refuse "$frags/syntax.nix" "config error: syntax error at syntax.nix:1:[0-9]+, unexpected ';'"
fi
# Nix's suggestion text is its own ("did you mean ripgrep?" or "did you
# mean one of ripgrep, ipgrep or repgrep?"); the contract pins the shape.
refuse "$frags/missing.nix" "config error: attribute 'ripgrepp' missing at missing.nix:1:[0-9]+ \(did you mean .*ripgrep.*\?\)"
refuse "$frags/fetch.nix" "config error: eval-time fetch not allowed at fetch.nix:1:[0-9]+; use pkgs.fetchurl \{ url = ...; hash = ...; \}"
refuse "$frags/abspath.nix" "config error: access to absolute path '/etc/passwd' is forbidden in pure evaluation mode .* at abspath.nix:1:[0-9]+; a fragment may only read files it carries"
refuse "$frags/nixpath.nix" "config error: <nixpkgs> is not available at nixpath.nix:1:[0-9]+; use the pkgs argument, which is the platform's pinned nixpkgs"

# 5. A fixed-output fetch with a hash builds and lands in the guest.
"$REPOSE" config apply "$frags/fetchurl-ok.nix" --project "$PROJECT" 2>&1 | tail -3 | evidence "repose config apply fetchurl-ok.nix"
guest_sh "$PROJECT" 'head -c 60 ~/.m3-copying; echo' | evidence "the fetched file in the guest"

# 6. GC roots: the project's revisions are rooted and the running closure is
#    live; the newest three revisions per project are kept (contract step 6).
closure=$(psql_q "select system_closure from config_revisions where project_id='$pid' and status='applied' order by applied_at desc limit 1")
host_sh "ls -l /nix/var/nix/gcroots/repose/ | grep -E '$gid|rev-$pid' ; echo dead-count: \$(nix-store --gc --print-dead 2>/dev/null | grep -c '$closure' || true)" | evidence "GC roots on the host for guest $gid and project $pid; the applied closure $closure in --print-dead (must be 0)"

# 7. Timings from hostd's build_done lines since the start (I-57), for
#    docs/RESEARCH.md.
host_sh "journalctl -u hostd --since '$since' --no-pager -o cat | grep '\"event\":\"build_done\"' | sed -E 's/.*\"revision_id\":\"([0-9a-f]{8})[^\"]*\".*\"eval_ms\":([0-9]+).*\"build_ms\":([0-9]+).*\"closure_bytes\":([0-9]+).*/revision \\1 eval_ms \\2 build_ms \\3 closure_bytes \\4/'" | evidence "hostd build_done timings since $since"

# 8. Optional: a throwaway project's roots and volume are gone after destroy.
if [ $with_destroy = 1 ]; then
	tp="m3-throwaway-$(date -u +%H%M%S)"
	d=$(mktemp -d)
	(cd "$d" && "$REPOSE" run "$tp" --no-attach --no-sync 2>&1 | tail -3) | evidence "repose run $tp (throwaway)"
	tpid=$("$REPOSE" status --json --project "$tp" | jsonq 'd["id"]')
	tgid=$(admin projects show "$tp" | awk '$1=="guest_id"{print $2}')
	host_sh "ls /nix/var/nix/gcroots/repose/ | grep -E '$tgid|rev-$tpid'; lvs --noheadings -o lv_name vg-guests | grep -c 'g-$tgid' || true" | evidence "before destroy: roots and volume of $tp"
	"$REPOSE" destroy --yes --project "$tp" 2>&1 | tail -3 | evidence "repose destroy $tp"
	sleep 20
	host_sh "echo roots: \$(ls /nix/var/nix/gcroots/repose/ | grep -cE '$tgid|rev-$tpid' || true); echo volumes: \$(lvs --noheadings -o lv_name vg-guests | grep -c 'g-$tgid' || true); systemctl list-units 'guest@*' --no-pager --plain | grep -c '$tgid' || true" | evidence "after destroy: roots, volumes and units of $tp (all 0)"
	rm -rf "$d"
fi

# 9a. Optional: the 30-minute build cap on the real host (case (c)).
if [ $with_timeout = 1 ]; then
	set +e
	out=$("$REPOSE" config apply "$frags/build-timeout.nix" --project "$PROJECT" 2>&1)
	rc=$?
	set -e
	printf '%s\n' "$out" | grep -B1 -A3 'timed out' | evidence "build timeout (exit $rc)"
	[ $rc -ne 0 ] || fail "the sleeping derivation was accepted"
	printf '%s' "$out" | grep -q 'build timed out after 30 minutes while building sleep-forever-1.0' || fail "build_timeout message is not the documented one"
fi

# 9. Optional: the closure cap on the real host.
if [ $with_cap = 1 ]; then
	closure_cap_row
fi

# Leave the project on a plain package fragment so later checks start clean.
"$REPOSE" config apply "$frags/package.nix" --project "$PROJECT" 2>&1 | tail -2 | evidence "reset to fragments/package.nix"
log "menu check passed; evidence in $report"
