import pickle, collections, statistics as st, os
D = pickle.load(open(os.path.join(os.path.dirname(__file__), "calls.pkl"), "rb"))
C = D["calls"]; S = D["sessions"]
def h(x): return x / 3600
print("calls", len(C), "sessions", len(S), "subagent files", sum(s["sub"] for s in S))
span = sum(s["span"] for s in S); tool = sum(s["tool"] for s in S); model = sum(s["model"] for s in S); idle = sum(s["idle"] for s in S)
print(f"session span {h(span):.0f} h; tool exec {h(tool):.0f} h; model think (gaps<10min) {h(model):.0f} h; gaps>=10min {h(idle):.0f} h")

print("\nCATEGORY calls err% exec_h out_Mchars ctx_Gtok(turn re-read, split) trunc ansi_kchars")
agg = collections.defaultdict(lambda: collections.Counter())
mids = collections.Counter(c.get("mid") for c in C)
for c in C:
    k = c["cat"]; a = agg[k]
    a["n"] += 1; a["err"] += c.get("err", False); a["dt"] += c.get("dt", 0); a["out"] += c.get("out", 0)
    a["ctx"] += c["turn"][0] / max(1, mids[c.get("mid")]); a["otok"] += c["turn"][1] / max(1, mids[c.get("mid")])
    a["trunc"] += c.get("trunc", False); a["ansi"] += c.get("ansi", 0); a["cr"] += c.get("cr", 0)
tot = collections.Counter()
for a in agg.values(): tot.update(a)
for k, a in sorted(agg.items(), key=lambda x: -x[1]["dt"])[:22]:
    print(f"{k:22s} {a['n']:6d} {100*a['err']/a['n']:5.1f} {h(a['dt']):6.1f} {a['out']/1e6:6.2f} {a['ctx']/1e9:6.2f} {a['trunc']:4d} {a['ansi']/1e3:7.1f}")
print(f"{'TOTAL':22s} {tot['n']:6d} {100*tot['err']/tot['n']:5.1f} {h(tot['dt']):6.1f} {tot['out']/1e6:6.2f} {tot['ctx']/1e9:6.2f} {tot['trunc']:4d} {tot['ansi']/1e3:7.1f}")
print("ansi share of all output chars: %.3f%%; outputs with any ansi: %d; with \\r: %d" % (100*tot['ansi']/tot['out'], sum(1 for c in C if c.get('ansi')), sum(1 for c in C if c.get('cr'))))

B = [c for c in C if c["tool"] == "Bash"]
print("\nBash calls", len(B), "run_in_background", sum(c["bg"] for c in B), "explicit timeout set", sum(1 for c in B if c.get("timeout_ms")))
W = [c for c in B if c["cat"] == "wait/poll"]
print("\nWAIT: calls", len(W), "exec_h %.1f" % h(sum(c.get("dt", 0) for c in W)))
for key in ("wtarget", "wshape"):
    g = collections.defaultdict(list)
    for c in W: g[c[key]].append(c)
    for k, L in sorted(g.items(), key=lambda x: -sum(c.get('dt', 0) for c in x[1])):
        dts = [c.get('dt', 0) for c in L]
        print(f"  {key}={k:22s} n={len(L):5d} h={h(sum(dts)):5.1f} median_s={st.median(dts):6.1f} err%={100*sum(c.get('err',0) for c in L)/len(L):4.1f} ctx_Gtok={sum(c['turn'][0] for c in L)/1e9:5.2f}")
fs = [c for c in W if c["wshape"] == "fixed sleep"]
print("  fixed sleep: declared sleep seconds total h %.1f, measured h %.1f" % (h(sum(c["sleep"] for c in fs)), h(sum(c.get('dt', 0) for c in fs))))
# poll episodes: consecutive wait calls in one file
eps = []; cur = 0; prevf = None
for c in B:
    if c["file"] != prevf:
        if cur: eps.append(cur)
        cur = 0; prevf = c["file"]
    if c["cat"] == "wait/poll": cur += 1
    elif cur: eps.append(cur); cur = 0
if cur: eps.append(cur)
eps.sort()
print("  wait episodes (runs of consecutive wait calls)", len(eps), "median len", st.median(eps), "p90", eps[int(.9*len(eps))], "max", eps[-1], "calls in runs>=3:", sum(e for e in eps if e >= 3))
print("  wait calls per turn ctx tokens median: %d" % st.median([c['turn'][0] for c in W if c['turn'][0]]))

print("\nERRORS by kind (all tools)")
ek = collections.Counter(c.get("ek") for c in C if c.get("err"))
for k, n in ek.most_common(): print(f"  {k:32s} {n}")
print("  harness timeouts in Bash:", sum(1 for c in B if c.get("harness_timeout")))
print("missing commands (command not found):", D["missing"].most_common(30))
print("git log/diff/show without pager guard:", sum(1 for c in B if c.get("git_pager_risk")))

print("\nOUTPUT size: share of all output chars from outputs >20k chars")
outs = sorted(c.get("out", 0) for c in C); big = sum(o for o in outs if o > 20000)
print("  p50 %d p90 %d p99 %d; >20k outputs %d carry %.1f%% of chars" % (outs[len(outs)//2], outs[int(.9*len(outs))], outs[int(.99*len(outs))], sum(1 for o in outs if o > 20000), 100*big/sum(outs)))
bc = collections.Counter(); bo = collections.Counter()
for c in C:
    if c.get("out", 0) > 20000: bc[c["cat"]] += 1; bo[c["cat"]] += c["out"]
print("  big outputs by category:", [(k, bc[k], round(bo[k]/1e6, 2)) for k, _ in bo.most_common(8)])

print("\nREAD: Read tool re-reads of a path already read in the session with no edit to it in between")
rr = 0; nr = 0
byf = collections.defaultdict(list)
for c in C: byf[c["file"]].append(c)
for f, L in byf.items():
    seen = set()
    for c in L:
        if c["tool"] == "Read" and c.get("path"):
            nr += 1
            if c["path"] in seen: rr += 1
            seen.add(c["path"])
        elif c["cat"] == "edit" and c.get("path"): seen.discard(c["path"])
print("  Read calls", nr, "re-reads", rr)
print("\nTOP first words of Bash:", collections.Counter(c.get("first") for c in B).most_common(30))
print("\nsubagent share: calls", sum(c['sub'] for c in C), "exec_h %.1f" % h(sum(c.get('dt',0) for c in C if c['sub'])))
