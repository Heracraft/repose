# Counts only; prints no command text.
import json, glob, os, re, sys, collections, datetime, statistics as st, hashlib
FILES = [f for f in glob.glob(os.path.expanduser("~/.claude/projects/**/*.jsonl"), recursive=True) if sys.argv[1] not in f]
def ts(s):
    try: return datetime.datetime.fromisoformat(s.replace("Z", "+00:00"))
    except Exception: return None
DEF = re.compile(r'''(grep|rg)\b[^|]*['"]\^?\s*(func|type|def|class|interface|const|var|let|function|struct|impl|fn|export (const|function|class|type|interface))\b''')
WORDID = re.compile(r'''(grep|rg)\b[^|]*(-w|\\b)''')
RECURSIVE = re.compile(r'''\b(grep -[a-zA-Z]*r|rg)\b''')
INSTALL = re.compile(r'\b(pnpm (install|i)\b|npm (ci|install|i)\b|yarn install|go mod (download|tidy)|pip install|uv (sync|pip)|cargo fetch|bundle install)')
ENVPFX = re.compile(r'^\s*(export |[A-Z_]+=\S+\s|source |\. |cd \S+\s*(&&|;))')
NIXDEV = re.compile(r'nix develop')
TESTRUN = re.compile(r'\b(go test|playwright|vitest|pytest|pnpm (test|check)|cargo test)')
stats = collections.Counter(); dts = collections.defaultdict(float)
chainlens = []; wait_err_first = collections.Counter(); wait_dur = collections.Counter(); wait_dur_h = collections.Counter()
test_rerun = 0; test_n = 0; test_dt = []; test_runner = collections.Counter(); test_runner_h = collections.Counter()
for f in FILES:
    pend = {}; chain = 0; seen_tests = set()
    for ln in open(f, errors="ignore"):
        try: d = json.loads(ln)
        except Exception: continue
        m = d.get("message") or {}; cont = m.get("content") if isinstance(m, dict) else None
        if not isinstance(cont, list): continue
        for b in cont:
            if not isinstance(b, dict): continue
            if b.get("type") == "tool_use":
                n = b.get("name"); inp = b.get("input") or {}
                if n == "Bash":
                    c = inp.get("command", "") or ""
                    isread = bool(re.search(r'^\s*(cd \S+\s*&&\s*)?(grep|rg|sed -n|cat|head|tail|ls|find|wc|awk)\b', c))
                    if isread: chain += 1
                    elif chain: chainlens.append(chain); chain = 0
                    if re.search(r'^\s*(cd \S+\s*&&\s*)?(grep|rg)\b', c):
                        stats["grep"] += 1
                        if DEF.search(c): stats["grep_def"] += 1
                        if WORDID.search(c): stats["grep_word"] += 1
                        if RECURSIVE.search(c): stats["grep_recursive"] += 1
                    if INSTALL.search(c): stats["install"] += 1; pend[b["id"]] = ("install", ts(d.get("timestamp")), c)
                    elif TESTRUN.search(c) and not re.search(r'\bsleep\b|until ', c):
                        test_n += 1
                        hsh = hashlib.sha1(c.encode()).hexdigest()
                        if hsh in seen_tests: test_rerun += 1
                        seen_tests.add(hsh)
                        r = TESTRUN.search(c).group(1).split()[0] if TESTRUN.search(c).group(1).startswith(("go","pnpm","cargo")) else TESTRUN.search(c).group(1)
                        r = TESTRUN.search(c).group(1)
                        pend[b["id"]] = ("test:" + r, ts(d.get("timestamp")), c)
                    elif re.search(r'\bsleep\b|until |while .*sleep|--watch|gh run watch|\bwait\b', c):
                        pend[b["id"]] = ("wait", ts(d.get("timestamp")), c)
                    else:
                        pend[b["id"]] = ("other", ts(d.get("timestamp")), c)
                    if ENVPFX.search(c): stats["env_prefix"] += 1
                    if NIXDEV.search(c): stats["nix_develop"] += 1
                    stats["bash"] += 1
                elif n in ("Read", "Grep", "Glob"):
                    chain += 1
                else:
                    if chain: chainlens.append(chain); chain = 0
            elif b.get("type") == "tool_result" and b.get("tool_use_id") in pend:
                k, t0, c = pend.pop(b["tool_use_id"]); t = ts(d.get("timestamp"))
                dt = (t - t0).total_seconds() if t and t0 else 0
                if not (0 <= dt < 7200): dt = 0
                dts[k.split(":")[0]] += dt
                if k.startswith("test:"):
                    test_runner[k[5:]] += 1; test_runner_h[k[5:]] += dt; test_dt.append(dt)
                if k == "wait":
                    bucket = "<10s" if dt < 10 else "10-60s" if dt < 60 else "1-2m" if dt < 120 else "2-10m" if dt < 600 else ">10m"
                    wait_dur[bucket] += 1; wait_dur_h[bucket] += dt
                    if b.get("is_error"):
                        r = b.get("content"); s = r if isinstance(r, str) else json.dumps(r)
                        low = s.lower()
                        kk = "exit code (check not yet true or cmd failed)" if "exit code" in low else "timeout" if "timed out" in low or "timeout" in low else "interrupted" if "interrupt" in low else "blocked sleep (harness refused)" if "sleep" in low and ("block" in low or "not allowed" in low or "monitor" in low) else "other"
                        wait_err_first[kk] += 1
    if chain: chainlens.append(chain)
chainlens.sort()
print(stats)
print("grep that look like definition lookups: %d of %d (%.1f%%); word/identifier greps %d; recursive %d" % (stats["grep_def"], stats["grep"], 100*stats["grep_def"]/stats["grep"], stats["grep_word"], stats["grep_recursive"]))
print("read/search chains between other actions: n=%d median %d p90 %d p99 %d; calls in chains>=5: %d" % (len(chainlens), st.median(chainlens), chainlens[int(.9*len(chainlens))], chainlens[int(.99*len(chainlens))], sum(c for c in chainlens if c >= 5)))
print("dep install calls %d, %.1f h" % (stats["install"], dts["install"]/3600))
print("bash calls with env/cd prefix %d of %d; nix develop %d" % (stats["env_prefix"], stats["bash"], stats["nix_develop"]))
print("test runs %d, identical reruns in same session %d, total %.1f h, median %.0fs p90 %.0fs" % (test_n, test_rerun, sum(test_dt)/3600, st.median(test_dt), sorted(test_dt)[int(.9*len(test_dt))]))
print("test runners:", [(k, v, round(test_runner_h[k]/3600, 1)) for k, v in test_runner.most_common()])
print("wait duration buckets:", [(k, wait_dur[k], round(wait_dur_h[k]/3600, 1)) for k in ["<10s", "10-60s", "1-2m", "2-10m", ">10m"]])
print("wait error kinds:", wait_err_first.most_common())
