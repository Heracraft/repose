import json, glob, os, re, collections, datetime, statistics as st
files=glob.glob(os.path.expanduser("~/.claude/projects/**/*.jsonl"),recursive=True)
tool=collections.Counter(); err=collections.Counter(); bashfirst=collections.Counter(); basherr=collections.Counter()
outlen=[]; waits=collections.defaultdict(list); cats=collections.Counter(); caterr=collections.Counter(); catwait=collections.defaultdict(float)
errkinds=collections.Counter(); repeats=0; total=0; sessions=0; trunc=0
def cat(cmd):
    c=cmd
    if re.search(r'\bsleep\b|until |while .*sleep|--watch|gh run watch|wait\b',c): return "wait/poll"
    if re.search(r'\bssh\b|docker exec|repose exec|scp ',c): return "remote exec"
    if re.search(r'\b(go test|pytest|vitest|playwright|pnpm (test|check)|just |nix (build|flake)|go build|go vet|golangci)',c): return "build/test"
    if re.search(r'^\s*(cd [^&;]+(&&|;)\s*)?git\b',c): return "git"
    if re.search(r'\b(grep|rg|find|ls|cat|head|tail|sed -n|wc|jq)\b',c): return "read/search"
    if re.search(r'\b(gh|curl|aws)\b',c): return "api call"
    if re.search(r'\b(ps|pgrep|systemctl|journalctl|df|free|top)\b',c): return "inspect system"
    return "other"
for f in files:
    sessions+=1; pending={}; lastcmds=collections.deque(maxlen=5)
    try: lines=open(f,errors="ignore").read().splitlines()
    except: continue
    for ln in lines:
        try: d=json.loads(ln)
        except: continue
        ts=d.get("timestamp"); m=d.get("message") or {}
        cont=m.get("content") if isinstance(m,dict) else None
        if not isinstance(cont,list): continue
        for b in cont:
            if not isinstance(b,dict): continue
            if b.get("type")=="tool_use":
                n=b.get("name","?"); tool[n]+=1; total+=1
                inp=b.get("input") or {}
                c=None
                if n=="Bash":
                    cmd=inp.get("command","") or ""
                    c=cat(cmd); cats[c]+=1
                    w=re.sub(r'^(cd \S+\s*&&\s*)','',cmd.strip()).split()
                    bashfirst[w[0] if w else ""]+=1
                    if cmd in lastcmds: repeats+=1
                    lastcmds.append(cmd)
                pending[b.get("id")]=(n,c,ts)
            elif b.get("type")=="tool_result":
                p=pending.pop(b.get("tool_use_id"),None)
                if not p: continue
                n,c,t0=p
                r=b.get("content"); s=r if isinstance(r,str) else json.dumps(r)
                outlen.append(len(s))
                if "truncated" in s[-400:].lower() or "output too large" in s.lower(): trunc+=1
                if b.get("is_error"):
                    err[n]+=1
                    if c: caterr[c]+=1
                    low=s.lower()
                    for k,pat in [("permission/denied","permission denied|denied"),("not found","not found|no such file"),("timeout","timed out|timeout"),("string not found (edit)","string to replace not found|old_string"),("file not read first","read it first|has not been read"),("exit code","exit code"),("interrupted","interrupt")]:
                        if re.search(pat,low): errkinds[k]+=1; break
                    else: errkinds["other"]+=1
                try:
                    a=datetime.datetime.fromisoformat(t0.replace("Z","+00:00")); z=datetime.datetime.fromisoformat(ts.replace("Z","+00:00"))
                    dt=(z-a).total_seconds()
                    if 0<=dt<7200:
                        waits[n].append(dt)
                        if c: catwait[c]+=dt
                except: pass
print("sessions",sessions,"tool calls",total)
print("\nTOOL  calls  err%  median_s  total_h")
for n,k in tool.most_common(12):
    w=waits.get(n,[0]); print(f"{n:14s} {k:7d} {100*err[n]/k:5.1f} {st.median(w) if w else 0:8.1f} {sum(w)/3600:8.1f}")
print("\nBASH CATEGORY  calls  err%  total_h")
for c,k in cats.most_common(): print(f"{c:15s} {k:7d} {100*caterr[c]/k:5.1f} {catwait[c]/3600:8.1f}")
print("\nerror kinds",errkinds.most_common())
print("bash repeats of one of last 5 cmds:",repeats, "of", tool['Bash'])
o=sorted(outlen); print("tool output chars p50/p90/p99:",o[len(o)//2],o[int(len(o)*.9)],o[int(len(o)*.99)],"truncated:",trunc)
print("top bash first words",bashfirst.most_common(25))
