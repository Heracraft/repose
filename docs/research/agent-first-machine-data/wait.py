import json,glob,os,re,collections,datetime
files=glob.glob(os.path.expanduser("~/.claude/projects/**/*.jsonl"),recursive=True)
tgt=collections.Counter(); th=collections.Counter(); P=[("CI / GitHub run","gh run|gh pr checks|actions"),("remote machine / guest","ssh|repose |docker exec|curl"),("build / nix / test","nix |go test|go build|playwright|pnpm|just "),("background job / log file","tail|\\.log|\\.output|pgrep|ps "),("deploy","coolify|deploy"),("plain sleep","^sleep|; sleep|&& sleep")]
for f in files:
    pend={}
    for ln in open(f,errors="ignore"):
        try:d=json.loads(ln)
        except:continue
        m=d.get("message") or {}; c=m.get("content") if isinstance(m,dict) else None
        if not isinstance(c,list):continue
        for b in c:
            if not isinstance(b,dict):continue
            if b.get("type")=="tool_use" and b.get("name")=="Bash":
                cmd=(b.get("input") or {}).get("command","")
                if re.search(r'\bsleep\b|until |--watch|gh run watch|\bwait\b',cmd):
                    k=next((n for n,p in P if re.search(p,cmd)),"other"); pend[b["id"]]=(k,d.get("timestamp"))
            elif b.get("type")=="tool_result" and b.get("tool_use_id") in pend:
                k,t0=pend.pop(b["tool_use_id"])
                try:
                    dt=(datetime.datetime.fromisoformat(d["timestamp"].replace("Z","+00:00"))-datetime.datetime.fromisoformat(t0.replace("Z","+00:00"))).total_seconds()
                    if 0<=dt<7200: tgt[k]+=1; th[k]+=dt
                except:pass
for k,n in tgt.most_common(): print(f"{k:28s} {n:6d} calls {th[k]/3600:6.1f} h")
