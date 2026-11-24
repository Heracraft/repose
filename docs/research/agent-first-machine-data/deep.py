# Counts only. Never prints command text, prompts or output content;
# prints category names, first words of commands, missing-command names and numbers.
import json, glob, os, re, sys, collections, datetime, statistics as st, pickle

FILES = glob.glob(os.path.expanduser("~/.claude/projects/**/*.jsonl"), recursive=True)
SELF = sys.argv[1] if len(sys.argv) > 1 else ""

ANSI = re.compile(r'\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07')
def ts(s):
    try: return datetime.datetime.fromisoformat(s.replace("Z", "+00:00"))
    except Exception: return None

def bash_cat(c):
    s = c.strip()
    if re.search(r'\bsleep\b|until |while .*sleep|--watch|gh run watch|\bwait\b|inotifywait|tail -f', s): return "wait/poll"
    if re.search(r'\b(go test|pytest|vitest|playwright|pnpm (test|check|run test)|npm (test|run test)|cargo test|just )', s): return "test"
    if re.search(r'\b(go build|go vet|golangci|nix (build|flake|develop|eval)|pnpm (build|run build)|npm run build|cargo build|tsc|make\b|svelte-check)', s): return "build"
    if re.search(r'\bssh\b|docker exec|repose exec|\bscp\b', s): return "remote exec"
    if re.search(r'^\s*(cd [^&;]+(&&|;)\s*)?git\b', s): return "git"
    if re.search(r'\b(rg|grep|find|fd)\b', s): return "search"
    if re.search(r'\b(cat|head|tail|sed -n|wc|ls|tree|jq|less|bat)\b', s): return "read"
    if re.search(r'\b(gh|curl|aws|az)\b', s): return "api call"
    if re.search(r'\b(ps|pgrep|systemctl|journalctl|df|free|top|ss|lsof|dmesg)\b', s): return "inspect system"
    if re.search(r'\b(python3?|node|bash|sh)\b', s): return "script"
    return "other"

def wait_target(c):
    for k, p in [("CI", r"gh run|gh pr checks|actions"), ("remote machine/guest", r"ssh|repose |docker exec|curl"),
                 ("build/test", r"nix |go test|go build|playwright|pnpm|just "), ("background job/log", r"tail|\.log|\.output|pgrep|ps |kill -0|/proc/"),
                 ("deploy", r"coolify|deploy")]:
        if re.search(p, c): return k
    return "bare sleep/other"

def wait_shape(c):
    if re.search(r'\buntil\b|\bwhile\b|--watch|gh run watch|inotifywait|tail -f|\bwait\b', c): return "condition loop"
    if re.search(r'\bsleep\s+\d', c): return "fixed sleep"
    return "other"

def sleep_secs(c):
    t = 0
    for m in re.finditer(r'\bsleep\s+(\d+(?:\.\d+)?)([smh]?)', c):
        v = float(m.group(1)); u = m.group(2)
        t += v * (60 if u == "m" else 3600 if u == "h" else 1)
    return t

ERRK = [("timeout", r"timed out|timeout"), ("command not found", r"command not found|not found in path|no such command"),
        ("no space", r"no space left"), ("killed/oom", r"\bkilled\b|out of memory|oom"),
        ("interactive prompt", r"\(y/n\)|\[y/n\]|ok to proceed|password:|press any key|are you sure|passphrase"),
        ("permission", r"permission denied|operation not permitted"),
        ("edit mismatch (harness)", r"string to replace not found|old_string|found \d+ matches"),
        ("file not read first (harness)", r"has not been read|read it first"),
        ("file not found", r"no such file|does not exist|enoent"),
        ("interrupted", r"interrupt"), ]

calls = []        # dicts per tool call
sessions = []     # per-session timing
missing = collections.Counter()
for f in FILES:
    if SELF and SELF in f: continue
    sub = "/subagents/" in f
    start = len(calls)
    pending = {}; seen_msg = set(); msg_cost = {}
    last_result_t = None; tool_t = 0.0; model_t = 0.0; idle_t = 0.0; first = last = None
    try: fh = open(f, errors="ignore")
    except Exception: continue
    for ln in fh:
        try: d = json.loads(ln)
        except Exception: continue
        t = ts(d.get("timestamp") or "")
        if t:
            first = first or t; last = t
        m = d.get("message") or {}
        if not isinstance(m, dict): continue
        cont = m.get("content")
        if d.get("type") == "assistant":
            mid = m.get("id"); u = m.get("usage") or {}
            if mid and mid not in seen_msg:
                seen_msg.add(mid)
                msg_cost[mid] = (u.get("input_tokens", 0) + u.get("cache_read_input_tokens", 0) + u.get("cache_creation_input_tokens", 0), u.get("output_tokens", 0))
                if last_result_t and t:
                    g = (t - last_result_t).total_seconds()
                    if 0 <= g < 600: model_t += g
                    elif g >= 600: idle_t += g
                    last_result_t = None
        if not isinstance(cont, list): continue
        for b in cont:
            if not isinstance(b, dict): continue
            if b.get("type") == "tool_use":
                n = b.get("name", "?"); inp = b.get("input") or {}
                rec = {"tool": n, "sub": sub, "file": f, "t0": t, "mid": m.get("id"), "bg": bool(inp.get("run_in_background"))}
                if n == "Bash":
                    c = inp.get("command", "") or ""
                    rec["cat"] = bash_cat(c)
                    w = re.sub(r'^(cd \S+\s*(&&|;)\s*)', '', c.strip()).split()
                    rec["first"] = w[0] if w else ""
                    rec["timeout_ms"] = inp.get("timeout")
                    if rec["cat"] == "wait/poll":
                        rec["wtarget"] = wait_target(c); rec["wshape"] = wait_shape(c); rec["sleep"] = sleep_secs(c)
                    rec["git_pager_risk"] = bool(re.search(r'^\s*git (log|diff|show)\b', c)) and "--no-pager" not in c and "|" not in c
                elif n in ("Read",):
                    rec["cat"] = "Read tool"; rec["path"] = inp.get("file_path")
                elif n in ("Grep", "Glob"):
                    rec["cat"] = "Grep/Glob tool"
                elif n in ("Edit", "Write", "MultiEdit", "NotebookEdit"):
                    rec["cat"] = "edit"; rec["path"] = inp.get("file_path")
                else:
                    rec["cat"] = "tool:" + n
                pending[b.get("id")] = rec
            elif b.get("type") == "tool_result":
                rec = pending.pop(b.get("tool_use_id"), None)
                if not rec: continue
                r = b.get("content")
                s = r if isinstance(r, str) else "".join(x.get("text", "") for x in r if isinstance(x, dict)) if isinstance(r, list) else ""
                rec["out"] = len(s)
                rec["ansi"] = sum(len(x) for x in ANSI.findall(s))
                rec["cr"] = s.count("\r")
                rec["trunc"] = ("truncated" in s[-400:].lower()) or ("output too large" in s.lower()) or ("persisted-output" in s[:200])
                rec["err"] = bool(b.get("is_error"))
                low = s.lower()
                if rec["err"]:
                    for k, p in ERRK:
                        if re.search(p, low): rec["ek"] = k; break
                    else: rec["ek"] = "other"
                for mm in re.finditer(r'(?:bash: |^|\n)([A-Za-z0-9_.+-]{1,40}): command not found', s):
                    missing[mm.group(1)] += 1
                if rec["tool"] == "Bash" and re.search(r'command timed out|timed out after', low): rec["harness_timeout"] = True
                if t and rec["t0"]:
                    dt = (t - rec["t0"]).total_seconds()
                    if 0 <= dt < 7200: rec["dt"] = dt; tool_t += dt
                last_result_t = t
                rec.pop("t0", None)
                calls.append(rec)
    for rec in calls[start:]:
        rec["turn"] = msg_cost.get(rec.get("mid"), (0, 0))
    if first and last:
        sessions.append({"file": f, "sub": sub, "span": (last - first).total_seconds(), "tool": tool_t, "model": model_t, "idle": idle_t})

pickle.dump({"calls": calls, "sessions": sessions, "missing": missing}, open(os.path.join(os.path.dirname(__file__), "calls.pkl"), "wb"))
print("files", len(FILES), "calls", len(calls), "sessions", len(sessions))
