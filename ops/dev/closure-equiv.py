#!/usr/bin/env python3
"""Do two revisions build the same system, ignoring the version stamp?

    closure-equiv.py <target> <revA> <revB>      # evaluates both sides
    closure-equiv.py A.json B.json <stampA> <stampB>   # pre-built closures

<target> is a flake attribute under nix/, for example
`nixosConfigurations.host-01.config.system.build.toplevel`,
`nixosConfigurations.edge.config.system.build.toplevel` or
`packages.x86_64-linux.guest-system`. About three seconds a side.

Why this is needed. `self.shortRev` is baked in as the version of hostd and
guestd and as the guest's base stamp, so every commit changes the drvPath of
every system and a plain comparison always answers "changed". Measured on
host-01 for a docs-only branch: 4844 derivations a side, of which 4826 are
byte-identical and the 18 that differ are the stamp and what is downstream
of it. So the question "does this branch actually change the host" is
answerable, but not by comparing drvPaths.

How. A structural hash per derivation, bottom-up: the builder, args, system,
output *names*, sources, and the structural hashes of its input derivations,
with the stamp text replaced and every store path that an input produces
replaced by that input's structural hash. Two systems are the same iff their
roots hash the same. Store paths that nothing in the closure produces
(fetched sources, fixed-output derivations) are content-addressed already and
compare directly.
"""
import hashlib, json, re, sys

# Only the store path itself, never the path inside the package: a unit file
# carries ".../hostd-@STAMP@/bin/hostd", and matching the whole thing means
# the lookup below never finds the producer (found by diffing
# unit-hostd.service.drv across two revisions).
STORE = re.compile(r"/nix/store/[a-z0-9]{32}-[^/\"'\s,:;)}\]]*")


def load(path, stamp):
    d = json.load(open(path))["derivations"]
    # map an output store path to the derivation that produces it
    producer = {}
    for k, v in d.items():
        env = v.get("env") or {}
        for name, o in ((v.get("outputs") or {})).items():
            # A fixed-output derivation (a vendored Go module set, a fetch)
            # carries only `hash` and `method` under outputs; its store path
            # is in env under the output's name. Missing those cost an hour:
            # hostd's env.goModules then matched no producer and the two
            # closures never converged.
            p = o.get("path") or env.get(name)
            if not p:
                continue
            p = p if p.startswith("/nix/store/") else "/nix/store/" + p
            # keyed with the stamp already replaced: norm() substitutes store
            # paths only after replacing the stamp text, so a key carrying the
            # raw stamp would never match.
            producer[p.replace(stamp, "@STAMP@")] = k
    return d, producer, stamp


def structural(drvs, producer, stamp):
    """A hash per derivation that does not depend on traversal order.

    Store paths in a derivation's text are replaced by the *name* of the
    derivation that produces them, never by that derivation's hash: a unit
    file can name a path belonging to something that depends on it, and
    resolving those by hash made the result depend on which node the walk
    started from (the same pair reported 18 changed derivations when hashed
    bottom-up and 3975 when hashed from the root). Discrimination comes from
    the formal input graph below, which is acyclic.
    """
    memo = {}

    def name_of(key):
        # the stamp lives in the key too (hostd-<rev>-go-modules.drv), and the
        # key is not normalised at load time, so normalise it here or the two
        # sides disagree on every name that embeds a revision
        b = key.rsplit("/", 1)[-1].replace(stamp, "@STAMP@")
        return b.split("-", 1)[1] if "-" in b else b

    def h(key):
        if key in memo:
            return memo[key]
        d = drvs[key]
        outs = sorted((d.get("outputs") or {}).keys())
        env = d.get("env") or {}
        own = set()
        for nm, o in ((d.get("outputs") or {})).items():
            p = o.get("path") or env.get(nm)
            if p:
                p = p if p.startswith("/nix/store/") else "/nix/store/" + p
                own.add(p.replace(stamp, "@STAMP@"))

        def nrm(text):
            text = text.replace(stamp, "@STAMP@")

            def sub(m):
                q = m.group(0)
                r = producer.get(q)
                if q in own or r == key:
                    return "@SELF@"
                if r is None:
                    # nothing in the closure builds it: a fetched source or a
                    # fixed-output path, content-addressed and comparable
                    return q
                return "@P:" + name_of(r) + "@"

            return STORE.sub(sub, text)

        memo[key] = "pending"           # formal inputs are acyclic
        ins = sorted(h(k) for k in (d.get("inputs", {}).get("drvs") or {}))
        body = {
            "name": nrm(d.get("name", "")),
            "builder": nrm(d.get("builder", "")),
            "system": d.get("system", ""),
            "args": [nrm(a) for a in (d.get("args") or [])],
            "env": {k: nrm(v) for k, v in sorted(env.items())
                    if k not in outs and k != "outputs"},
            "outs": outs,
            "inputs": ins,
            "srcs": sorted(nrm(x) for x in (d.get("inputs", {}).get("srcs") or [])),
        }
        memo[key] = hashlib.sha256(
            json.dumps(body, sort_keys=True).encode()).hexdigest()[:24]
        return memo[key]

    return h


def roots(drvs):
    """Derivations nothing else in the closure depends on."""
    used = set()
    for v in drvs.values():
        used |= set((v.get("inputs", {}).get("drvs") or {}).keys())
    return sorted(set(drvs) - used)


def main(fa, fb, sa, sb):
    A, pa, _ = load(fa, sa)
    B, pb, _ = load(fb, sb)
    ha = structural(A, pa, sa)
    hb = structural(B, pb, sb)
    ra, rb = roots(A), roots(B)
    print(f"derivations: A {len(A)}  B {len(B)}   roots: {len(ra)} / {len(rb)}")
    same = True
    for x, y in zip(ra, rb):
        da, db = ha(x), hb(y)
        mark = "same" if da == db else "DIFFERS"
        print(f"  {mark:8} {x.rsplit('/',1)[-1][:58]}")
        same &= da == db
    if len(ra) != len(rb):
        same = False
    # What actually differs. Report distinct structural hashes, not store
    # paths: 417 derivations in a host closure are named "source.drv" and
    # many paths share one structural hash, so listing paths made a 17
    # derivation difference look like 4000 (while validating this tool).
    if not same:
        sa_ = {ha(k) for k in A}
        sb_ = {hb(k) for k in B}
        names = sorted({k.rsplit('/', 1)[-1].split('-', 1)[-1]
                        for k in A if ha(k) not in sb_})
        print(f"\nstructural hashes: A {len(sa_)}  B {len(sb_)}  "
              f"shared {len(sa_ & sb_)}  changed {len(sa_ - sb_)}")
        print(f"what changes ({len(names)} derivations):")
        for n in names[:25]:
            print(f"   {n[:66]}")
        if len(names) > 25:
            print(f"   ... and {len(names) - 25} more")
    print()
    print("VERDICT:", "the same system; only the version stamp differs" if same
          else "the system really changes")
    return 0 if same else 1


def drive(target, ra, rb):
    """Evaluate the target at two revisions here, then compare."""
    import subprocess, tempfile, os
    top = subprocess.run(["git", "rev-parse", "--show-toplevel"],
                         capture_output=True, text=True, check=True).stdout.strip()
    out = []
    for r in (ra, rb):
        sha = subprocess.run(["git", "rev-parse", r], capture_output=True,
                             text=True, check=True).stdout.strip()
        short = subprocess.run(["git", "rev-parse", "--short=7", sha],
                               capture_output=True, text=True, check=True).stdout.strip()
        fd, path = tempfile.mkstemp(suffix=".json", prefix=f"closure-{short}-")
        os.close(fd)
        url = f"git+file://{top}?rev={sha}&dir=nix#{target}"
        print(f"evaluating {short} ...", file=sys.stderr)
        with open(path, "w") as f:
            p = subprocess.run(["nix", "derivation", "show", "-r", url],
                               stdout=f, stderr=subprocess.PIPE, text=True)
        if p.returncode != 0:
            print(p.stderr.strip()[-400:], file=sys.stderr)
            return 2
        out.append((path, short))
    try:
        return main(out[0][0], out[1][0], out[0][1], out[1][1])
    finally:
        for path, _ in out:
            os.unlink(path)


if __name__ == "__main__":
    a = sys.argv[1:]
    if len(a) == 3:
        sys.exit(drive(*a))
    if len(a) == 4:
        sys.exit(main(*a))
    print(__doc__, file=sys.stderr)
    sys.exit(2)
