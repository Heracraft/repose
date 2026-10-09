#!/usr/bin/env bash
# startup-bench.sh: measure `repose run` / `repose attach` startup against the
# live service, per phase (DECISIONS I-223). Rerun it after a deploy.
#
#   ops/dev/startup-bench.sh [-b BIN] [-d DIR] [-p PROJECT] [-n N] [-r RTT_MS] [-o OUT] SCENARIO...
#
# SCENARIO is one of
#   setup    create PROJECT from DIR (a first run, kept for the others)
#   first    create + build + boot + first sync + attach (a fresh e2e- project
#            each time, destroyed afterwards; N defaults to 3)
#   warm     run on a running guest, nothing changed since the last run
#            (the checkout's uncommitted changes, if any, as they were)
#   clean    warm, from a checkout with no uncommitted changes (the first
#            run resets DIR's tracked files)
#   dirty    run with three newly modified files
#   stopped  stop, then run (start + sync + attach; N defaults to 3)
#   attach   `repose attach` alone
#   cold     warm, but with no ssh master left from an earlier command
#            (ControlPersist expired)
#
# DIR is a git checkout (default: a clone of gin-gonic/gin under
# /mnt/nixstore/repose-ws/e2e-startup). PROJECT must start with e2e-; the
# script refuses anything else. Each run is driven through a pty: the clock
# stops when tmux switches the terminal to its alternate screen (the moment
# the user sees the session), then the script detaches (C-b d). BIN runs
# with REPOSE_TIMING=1, and its per-phase lines are kept in
# $OUT/<scenario>-<i>.log.timing; the summary is the median of each line.
#
# -r RTT_MS adds a laptop's round trip to every api and ssh connection of
# BIN (ops/dev/latency-proxy.py: HTTPS_PROXY for the api, an ssh wrapper
# with a ProxyCommand for the gateway), so a box next to the service
# measures what a laptop far from it sees. The machine's network is not
# touched.
set -euo pipefail

BIN=${REPOSE_BIN:-repose}
DIR=/mnt/nixstore/repose-ws/e2e-startup/gin
PROJECT=e2e-startup
N=""
RTT=0
OUT=${OUT:-/mnt/nixstore/repose-ws/e2e-startup/out/$(date -u +%Y%m%dT%H%M%SZ)}
while getopts "b:d:p:n:o:r:" o; do
  case $o in
    b) BIN=$OPTARG ;; d) DIR=$OPTARG ;; p) PROJECT=$OPTARG ;; n) N=$OPTARG ;; o) OUT=$OPTARG ;; r) RTT=$OPTARG ;;
    *) sed -n '2,30p' "$0"; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[ $# -gt 0 ] || { sed -n '2,30p' "$0"; exit 2; }
case $PROJECT in e2e-*) ;; *) echo "refusing: PROJECT must start with e2e-" >&2; exit 2 ;; esac
if [ ! -d "$DIR/.git" ]; then
  git clone -q https://github.com/gin-gonic/gin.git "$DIR"
fi
mkdir -p "$OUT"; OUT=$(cd "$OUT" && pwd)
BIN=$(realpath "$(command -v "$BIN")")
HERE=$(cd "$(dirname "$0")" && pwd)
echo "bin: $BIN ($("$BIN" --version))  dir: $DIR  project: $PROJECT  rtt: ${RTT}ms  out: $OUT"

closemaster() { ssh -F ~/.ssh/repose/config -O exit "$1.repose" >/dev/null 2>&1 || true; }

if [ "$RTT" != 0 ]; then
  REAL_SSH=$(command -v ssh)
  mkdir -p "$OUT/bin"
  printf '#!/bin/sh\nexec %s -o "ProxyCommand=python3 %s/latency-proxy.py stdio %s %%h %%p" "$@"\n' \
    "$REAL_SSH" "$HERE" "$RTT" >"$OUT/bin/ssh"
  chmod +x "$OUT/bin/ssh"
  export PATH="$OUT/bin:$PATH"
  port=$((20000 + RANDOM % 20000))
  python3 "$HERE/latency-proxy.py" http "$RTT" "$port" &
  PROXY_PID=$!
  trap 'kill $PROXY_PID 2>/dev/null' EXIT
  export HTTPS_PROXY=http://127.0.0.1:$port
  sleep 0.3
fi
# A master left by an earlier command may have been made at another RTT
# (through the proxy, or not): every bench starts without one.
closemaster "$PROJECT"

# drive CMD... in a pty; print "<ms to tmux attach> <exit>" and save the output.
drive() {
  local log=$1; shift
  python3 - "$log" "$@" <<'PY'
import os, pty, re, select, signal, sys, time
log, argv = sys.argv[1], sys.argv[2:]
env = dict(os.environ, REPOSE_TIMING="1", TERM=os.environ.get("TERM", "xterm-256color"))
t0 = time.monotonic()
pid, fd = pty.fork()
if pid == 0:
    # Be the interactive shell: run the command as its own foreground job
    # and keep the terminal after it ends, so an ssh ControlPersist master
    # it forked survives the way it does under bash or zsh (a session
    # leader exiting would SIGHUP the job's process group).
    signal.signal(signal.SIGTTOU, signal.SIG_IGN)
    job = os.fork()
    if job == 0:
        os.setpgid(0, 0)
        os.tcsetpgrp(0, os.getpgrp())
        signal.signal(signal.SIGTTOU, signal.SIG_DFL)
        os.execvpe(argv[0], argv, env)
    try:
        os.setpgid(job, job)
    except OSError:
        pass
    _, st = os.waitpid(job, 0)
    os.tcsetpgrp(0, os.getpgrp())
    os._exit(os.waitstatus_to_exitcode(st) & 0xFF)
buf, attached, sent = b"", None, None
while True:
    r, _, _ = select.select([fd], [], [], 0.05)
    if r:
        try:
            d = os.read(fd, 65536)
        except OSError:
            break
        if not d:
            break
        buf += d
        if attached is None and b"\x1b[?1049h" in buf:
            attached = (time.monotonic() - t0) * 1000
    if attached is not None and sent is None and (time.monotonic() - t0) * 1000 > attached + 300:
        os.write(fd, b"\x02d")  # tmux detach
        sent = time.monotonic()
    if sent and time.monotonic() - sent > 10:
        os.kill(pid, signal.SIGTERM)
    if time.monotonic() - t0 > 900:
        os.kill(pid, signal.SIGTERM)
_, st = os.waitpid(pid, 0)
open(log, "wb").write(buf)
lines = re.findall(r"repose-timing \+\d+ms [^\r\n\x1b]*", buf.decode("utf-8", "replace"))
with open(log + ".timing", "w") as f:
    f.write("\n".join(lines) + "\n")
    f.write("repose-timing +%dms attached(tmux screen) 0ms\n" % (attached or -1))
print("%d %d" % (attached or -1, os.waitstatus_to_exitcode(st)))
PY
}

modify() { # three tracked files changed
  local i=$1 f
  for f in $(git -C "$DIR" ls-files '*.go' | head -3); do
    printf '\n// startup-bench %s %s\n' "$i" "$(date +%s%N)" >>"$DIR/$f"
  done
}

run_one() { # scenario i
  local s=$1 i=$2 log="$OUT/$1-$2.log" r
  case $s in
    setup) r=$(cd "$DIR" && drive "$log" "$BIN" run "$PROJECT"); echo "$PROJECT" >>"$OUT/created" ;;
    warm) r=$(cd "$DIR" && drive "$log" "$BIN" run) ;;
    clean) [ "$i" != 1 ] || git -C "$DIR" checkout -q -- .; r=$(cd "$DIR" && drive "$log" "$BIN" run) ;;
    dirty) modify "$i"; r=$(cd "$DIR" && drive "$log" "$BIN" run) ;;
    attach) r=$(cd "$DIR" && drive "$log" "$BIN" attach) ;;
    cold) closemaster "$PROJECT"; r=$(cd "$DIR" && drive "$log" "$BIN" run) ;;
    stopped)
      (cd "$DIR" && "$BIN" stop "$PROJECT" >/dev/null 2>&1) || true
      r=$(cd "$DIR" && drive "$log" "$BIN" run) ;;
    first)
      local name="$PROJECT-f$i-$RANDOM" d="$OUT/first-$i"
      git clone -q "$DIR" "$d"
      git -C "$d" remote set-url origin "https://github.com/gin-gonic/$name"
      r=$(cd "$d" && drive "$log" "$BIN" run "$name")
      echo "$name" >>"$OUT/created"
      (cd "$d" && "$BIN" destroy -y --wait "$name" >/dev/null 2>&1) || echo "destroy $name failed" >&2
      ;;
  esac
  echo "$s #$i: attached after ${r% *} ms (exit ${r#* })"
  echo "$s $i $r" >>"$OUT/results"
}

for s in "$@"; do
  n=${N:-5}
  if [ "$s" = first ] || [ "$s" = stopped ]; then n=${N:-3}; fi
  if [ "$s" = setup ]; then n=1; fi
  for i in $(seq 1 "$n"); do run_one "$s" "$i"; done
done

# summary: median ms per scenario for the total and each timed line
python3 - "$OUT" <<'PY'
import glob, os, re, statistics, sys, collections
out = sys.argv[1]
by = collections.defaultdict(lambda: collections.defaultdict(list))
for f in sorted(glob.glob(os.path.join(out, "*.log.timing"))):
    scen = os.path.basename(f).split("-")[0]
    seen = collections.Counter()
    for l in open(f):
        m = re.match(r"repose-timing \+(\d+)ms (.*?) (\d+)ms$", l.strip())
        if not m:
            continue
        key = re.sub(r" (out=\d+B|exit=-?\d+)", "", m.group(2))
        key = re.sub(r"script\(\d+B\)", "script", key)
        seen[key] += 1
        k = key if seen[key] == 1 else "%s #%d" % (key, seen[key])
        if key == "attached(tmux screen)":
            by[scen]["TOTAL to tmux screen"].append(int(m.group(1)))
        else:
            by[scen][k].append(int(m.group(3)))
for scen, rows in by.items():
    print("\n== %s (%d runs)" % (scen, max(len(v) for v in rows.values())))
    for k, v in rows.items():
        print("  %-48s median %6d ms  (n=%d, min %d, max %d)" % (k, statistics.median(v), len(v), min(v), max(v)))
PY
