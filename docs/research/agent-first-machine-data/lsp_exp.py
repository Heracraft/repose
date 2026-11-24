# grep vs gopls for "where is X used": output size, precision, latency. Symbols are
# sampled from the repo's own source by a fixed seed; nothing from transcripts.
import subprocess, re, random, os, time, glob, json, statistics as st, sys
R = os.path.expanduser("~/kanali-agent-first-research"); G = sys.argv[1]; MODE = sys.argv[2]  # cold|warm
os.chdir(R)
cands = []
for f in sorted(glob.glob("internal/**/*.go", recursive=True)):
    if f.endswith("_test.go"): continue
    for i, line in enumerate(open(f, errors="ignore"), 1):
        m = re.match(r'^func (?:\([^)]*\) )?([A-Z][A-Za-z0-9]+)\(', line)
        if m: cands.append((f, i, line.index(m.group(1)) + 1, m.group(1), line.startswith("func (")))
random.seed(199); S = random.sample(cands, 30)
rows = []
for f, ln, col, name, method in S:
    t = time.time(); g = subprocess.run(["grep", "-rn", "--include=*.go", r"\b%s\b" % name, "."], capture_output=True, text=True); tg = time.time() - t
    glines = [l for l in g.stdout.splitlines() if l]
    args = [G] + (["-remote=auto"] if MODE == "warm" else []) + ["references", "-d", "%s:%d:%d" % (f, ln, col)]
    t = time.time(); r = subprocess.run(args, capture_output=True, text=True, timeout=600); tl = time.time() - t
    rlines = [l for l in r.stdout.splitlines() if l]
    refs = set((os.path.relpath(l.split(":")[0], R), l.split(":")[1]) for l in rlines)
    gset = set((os.path.normpath(l.split(":")[0]), l.split(":")[1]) for l in glines)
    true_in_grep = len(gset & refs)
    rows.append(dict(name_len=len(name), method=method, grep_lines=len(glines), grep_chars=len(g.stdout), grep_s=tg,
                     lsp_lines=len(rlines), lsp_chars=len(r.stdout), lsp_s=tl, lsp_ok=r.returncode == 0, grep_precision=(true_in_grep / len(gset)) if gset else 1.0,
                     grep_recall=(true_in_grep / len(refs)) if refs else 1.0))
json.dump(rows, open("lsp_%s.json" % MODE, "w"))
ok = [r for r in rows if r["lsp_ok"]]
def med(k, L=ok): return st.median([r[k] for r in L])
print(MODE, "symbols", len(rows), "gopls ok", len(ok))
print("grep: median lines %.0f chars %.0f, p90 chars %.0f, total chars %d, median %.2fs" % (med("grep_lines"), med("grep_chars"), sorted(r["grep_chars"] for r in ok)[int(.9*len(ok))], sum(r["grep_chars"] for r in ok), med("grep_s")))
print("gopls: median lines %.0f chars %.0f, total chars %d, median %.2fs max %.2fs" % (med("lsp_lines"), med("lsp_chars"), sum(r["lsp_chars"] for r in ok), med("lsp_s"), max(r["lsp_s"] for r in ok)))
print("grep precision (share of grep hits that are real references) median %.2f, mean %.2f; methods only median %.2f (n=%d); recall median %.2f" % (med("grep_precision"), st.mean(r["grep_precision"] for r in ok), med("grep_precision", [r for r in ok if r["method"]]) if any(r["method"] for r in ok) else -1, sum(r["method"] for r in ok), med("grep_recall")))
