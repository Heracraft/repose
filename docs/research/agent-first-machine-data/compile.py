# Build/test calls whose output carries compiler/type-checker errors; counts only.
import json, glob, os, re, sys, datetime, collections
FILES = [f for f in glob.glob(os.path.expanduser("~/.claude/projects/**/*.jsonl"), recursive=True) if sys.argv[1] not in f]
HEAVY = re.compile(r'\b(go test|go build|go vet|golangci|playwright|vitest|pnpm (test|check|build)|nix (build|flake check)|svelte-check|tsc)\b')
CE = re.compile(r'(\.go:\d+:\d+: (undefined|cannot use|syntax error|declared and not used|too many|not enough|missing return|imported and not used|undefined:)|error TS\d+|\[build failed\]|# [a-z0-9/._-]+\n.*\.go:\d+:\d+|svelte-check found [1-9]\d* error)')
def ts(s):
    try: return datetime.datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()
    except Exception: return None
n = ce = 0; ce_t = 0.0; tot_t = 0.0; kinds = collections.Counter(); ce_ctx = 0
for f in FILES:
    pend = {}; usage = {}
    for ln in open(f, errors="ignore"):
        try: d = json.loads(ln)
        except Exception: continue
        m = d.get("message") or {}; cont = m.get("content") if isinstance(m, dict) else None
        if d.get("type") == "assistant" and isinstance(m, dict) and m.get("usage"):
            u = m["usage"]; usage[m.get("id")] = u.get("input_tokens", 0) + u.get("cache_read_input_tokens", 0) + u.get("cache_creation_input_tokens", 0)
        if not isinstance(cont, list): continue
        for b in cont:
            if not isinstance(b, dict): continue
            if b.get("type") == "tool_use" and b.get("name") == "Bash":
                c = (b.get("input") or {}).get("command", "") or ""
                if HEAVY.search(c) and not re.search(r'\bsleep\b|until ', c): pend[b["id"]] = (ts(d.get("timestamp")), HEAVY.search(c).group(1), m.get("id"))
            elif b.get("type") == "tool_result" and b.get("tool_use_id") in pend:
                t0, k, mid = pend.pop(b["tool_use_id"]); t1 = ts(d.get("timestamp"))
                r = b.get("content"); s = r if isinstance(r, str) else json.dumps(r)
                dt = (t1 - t0) if t0 and t1 and 0 < t1 - t0 < 7200 else 0
                n += 1; tot_t += dt
                if CE.search(s.replace("\\n", "\n")):
                    ce += 1; ce_t += dt; kinds[k] += 1; ce_ctx += usage.get(mid, 0)
print("build/test calls %d (%.1f h); with compile/type errors in output %d (%.1f%%), %.2f h; by runner %s; ctx tokens of those turns %.2f G" % (n, tot_t/3600, ce, 100*ce/n, ce_t/3600, kinds.most_common(), ce_ctx/1e9))
