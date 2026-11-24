import json, glob, os, re, sys, collections
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from deepcat import bash_cat
FILES = [f for f in glob.glob(os.path.expanduser("~/.claude/projects/**/*.jsonl"), recursive=True) if sys.argv[1] not in f]
P = {"port in use": r"address already in use|eaddrinuse|port is already allocated|port \d+ is (already )?in use",
     "git index.lock": r"index\.lock",
     "docker name conflict": r"conflict\. the container name",
     "lock wait/flock": r"waiting for (the )?lock|could not acquire lock|resource temporarily unavailable.*lock|lock is held",
     "no space": r"no space left on device",
     "oom/killed": r"out of memory|oom-kill|killed process|signal: killed",
     "text file busy": r"text file busy",
     "timeout exceeded (test)": r"panic: test timed out|test timed out after",
     "harness bash timeout": r"command timed out after",
     "too many open files": r"too many open files",
     "network/dns": r"could not resolve host|temporary failure in name resolution|connection refused|connection reset",
     "rate limit/429": r"\b429\b|rate limit",
     "playwright browser missing": r"executable doesn't exist|browsertype\.launch",
     }
hits = collections.Counter(); sess = collections.defaultdict(set)
for f in FILES:
    pend = set()
    for ln in open(f, errors="ignore"):
        try: d = json.loads(ln)
        except Exception: continue
        m = d.get("message") or {}; cont = m.get("content") if isinstance(m, dict) else None
        if not isinstance(cont, list): continue
        for b in cont:
            if not isinstance(b, dict): continue
            if b.get("type") == "tool_use" and b.get("name") == "Bash" and bash_cat((b.get("input") or {}).get("command","") or "") in ("test","build","script","other","remote exec","wait/poll"): pend.add(b["id"])
            elif b.get("type") == "tool_result" and b.get("tool_use_id") in pend:
                r = b.get("content"); s = (r if isinstance(r, str) else json.dumps(r)).lower()
                for k, p in P.items():
                    if re.search(p, s): hits[k] += 1; sess[k].add(f)
for k, n in hits.most_common(): print(f"{k:28s} outputs={n:5d} sessions={len(sess[k])}")
