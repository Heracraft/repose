# Concurrency of build/test calls across all sessions on this box; counts only.
import json, glob, os, re, sys, collections, datetime, statistics as st, hashlib, bisect
FILES = [f for f in glob.glob(os.path.expanduser("~/.claude/projects/**/*.jsonl"), recursive=True) if sys.argv[1] not in f]
def ts(s):
    try: return datetime.datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()
    except Exception: return None
HEAVY = re.compile(r'\b(go test|go build|go vet|golangci|playwright|vitest|pnpm (test|check|build|install)|nix (build|flake check)|svelte-check|tsc)\b')
def key(c):
    m = HEAVY.search(c); tail = c[m.start():]
    tail = re.split(r'\||2>&1|;|&&|>', tail)[0]
    tail = re.sub(r'/home/azureuser/[^ ]*?/(kanali-[^/ ]+|factory)/', '', tail)
    tail = re.sub(r'-count=\d+|-timeout[= ]\S+', '', tail)
    return re.sub(r'\s+', ' ', tail).strip()
iv = []
for f in FILES:
    pend = {}
    for ln in open(f, errors="ignore"):
        try: d = json.loads(ln)
        except Exception: continue
        m = d.get("message") or {}; cont = m.get("content") if isinstance(m, dict) else None
        if not isinstance(cont, list): continue
        for b in cont:
            if not isinstance(b, dict): continue
            if b.get("type") == "tool_use" and b.get("name") == "Bash":
                c = (b.get("input") or {}).get("command", "") or ""
                if HEAVY.search(c) and not re.search(r'\bsleep\b|until ', c) and not (b.get("input") or {}).get("run_in_background"):
                    pend[b["id"]] = (ts(d.get("timestamp")), key(c), HEAVY.search(c).group(1))
            elif b.get("type") == "tool_result" and b.get("tool_use_id") in pend:
                t0, hsh, kind = pend.pop(b["tool_use_id"]); t1 = ts(d.get("timestamp"))
                if t0 and t1 and 0 < t1 - t0 < 7200 and not b.get("is_error"):
                    iv.append((t0, t1, hsh, kind))
iv.sort()
starts = [x[0] for x in iv]
# overlap: number of other heavy calls overlapping >=50% of this call's span
def overlap(i):
    t0, t1 = iv[i][0], iv[i][1]; n = 0; busy = 0.0
    j = bisect.bisect_left(starts, t0 - 7200)
    for k in range(j, len(iv)):
        if k == i: continue
        a, b = iv[k][0], iv[k][1]
        if a >= t1: break
        o = min(b, t1) - max(a, t0)
        if o > 0: busy += o
    return busy / (t1 - t0)
by = collections.defaultdict(list)
for i, x in enumerate(iv):
    by[x[2]].append((overlap(i), x[1] - x[0], x[3]))
print("heavy calls", len(iv), "distinct commands", len(by))
allo = [overlap(i) for i in range(len(iv))]
print("share of heavy calls with >=1 other heavy call in flight on average (overlap>=1.0): %.1f%%; any overlap: %.1f%%" % (100*sum(o >= 1 for o in allo)/len(allo), 100*sum(o > 0 for o in allo)/len(allo)))
print("heavy-call hours %.1f; of which run with others in flight (overlap>0) %.1f" % (sum(x[1]-x[0] for x in iv)/3600, sum(iv[i][1]-iv[i][0] for i in range(len(iv)) if allo[i] > 0)/3600))
ratios = []; kinds = collections.defaultdict(list)
for h, L in by.items():
    alone = [d for o, d, k in L if o == 0]; busy = [d for o, d, k in L if o >= 1]
    if alone and busy and st.median(alone) >= 5:
        r = st.median(busy) / st.median(alone); ratios.append(r); kinds[L[0][2]].append(r)
ratios.sort()
if ratios:
    print("paired same-command comparisons (median alone >=5s): n=%d, slowdown busy/alone median %.2f p25 %.2f p75 %.2f" % (len(ratios), st.median(ratios), ratios[len(ratios)//4], ratios[3*len(ratios)//4]))
    for k, L in kinds.items(): print("   ", k, len(L), round(st.median(L), 2))
