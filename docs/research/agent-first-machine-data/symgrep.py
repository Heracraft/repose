import json, glob, os, re, sys, collections, statistics as st
FILES = [f for f in glob.glob(os.path.expanduser("~/.claude/projects/**/*.jsonl"), recursive=True) if sys.argv[1] not in f]
DEF = re.compile(r'''^\s*(cd \S+\s*&&\s*)?(grep|rg)\b[^|]*['"]\^?\s*(func|type|def|class|interface|const|var|function|struct|fn)\b''')
# single identifier pattern, e.g. grep -rn "FooBar" or grep -rn 'foo_bar' path
SYM = re.compile(r'''^\s*(cd \S+\s*&&\s*)?(grep|rg)\s+(-[A-Za-z]+\s+)*['"]?\\?b?[A-Za-z_][A-Za-z0-9_]{3,}(\\b)?['"]?\s+[^|]*$''')
tot = collections.Counter(); outs = collections.defaultdict(list)
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
                if re.search(r'^\s*(cd \S+\s*&&\s*)?(grep|rg)\b', c):
                    k = "def-like" if DEF.search(c) else "single-symbol" if SYM.search(c) else "other grep"
                    pend[b["id"]] = k
            elif b.get("type") == "tool_result" and b.get("tool_use_id") in pend:
                k = pend.pop(b["tool_use_id"]); r = b.get("content"); s = r if isinstance(r, str) else json.dumps(r)
                outs[k].append(len(s))
for k, L in outs.items():
    print(f"{k:14s} n={len(L):6d} chars={sum(L)/1e6:6.2f}M median={st.median(L):6.0f} p90={sorted(L)[int(.9*len(L))]:7d}")
