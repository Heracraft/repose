#!/usr/bin/env python3
"""The unit-path gate (DECISIONS I-1554): fails the image build when a
systemd unit names a /usr/local path the image does not have.

Every unit file and drop-in under ROOT/etc/systemd/system,
ROOT/etc/systemd/user and ROOT/etc/xdg/systemd is read. Each Exec* line
(ExecStart, ExecStartPre, ExecStartPost, ExecCondition, ExecReload,
ExecStop, ExecStopPost) is searched for absolute paths under /usr/local/,
the program and its arguments both, so `/bin/bash -c '/usr/local/bin/x &'`
counts. A path under /usr/local/share/ must exist; any other must be an
executable regular file. Symbolic links are followed inside ROOT, so the
gate gives the same answer in the image (ROOT=/) and on an extracted root.

Image 2026.11.22.10 shipped without the 23 scripts in /usr/local/bin,
which .gitignore's `bin/` had kept out of git; repose-tmux-session was
skipped on its ExecCondition and `repose run` found no tmux session.

usage: unit-paths-gate.py ROOT
"""
import os, re, stat, sys

UNIT_DIRS = ("etc/systemd/system", "etc/systemd/user", "etc/xdg/systemd")
EXEC = re.compile(r"^\s*(Exec[A-Za-z]+)\s*=\s*(.*)$")
PATH = re.compile(r"/usr/local/[A-Za-z0-9_.+@-][A-Za-z0-9_./+@-]*")


def resolve(root, path, hops=0):
    """The real path inside root of an absolute path, or None at a loop."""
    if hops > 40:
        return None
    parts = [p for p in path.split("/") if p]
    cur = root
    for i, part in enumerate(parts):
        nxt = os.path.join(cur, part)
        if os.path.islink(nxt):
            target = os.readlink(nxt)
            if not target.startswith("/"):
                target = os.path.join("/" + os.path.relpath(cur, root), target)
            rest = "/".join(parts[i + 1:])
            return resolve(root, os.path.normpath(target) + ("/" + rest if rest else ""), hops + 1)
        cur = nxt
    return cur


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: unit-paths-gate.py ROOT")
    root = os.path.realpath(sys.argv[1])
    checked, bad = {}, []
    for d in UNIT_DIRS:
        base = os.path.join(root, d)
        for dirpath, _, files in os.walk(base):
            for name in sorted(files):
                unit = os.path.join(dirpath, name)
                if os.path.islink(unit) or not os.path.isfile(unit):
                    continue
                with open(unit, errors="replace") as f:
                    for n, line in enumerate(f, 1):
                        m = EXEC.match(line)
                        if not m:
                            continue
                        for p in PATH.findall(m.group(2)):
                            p = p.rstrip(".")
                            where = f"/{os.path.relpath(unit, root)}:{n} {m.group(1)}"
                            if p not in checked:
                                real = resolve(root, p)
                                if real is None or not os.path.exists(real):
                                    checked[p] = "missing"
                                elif p.startswith("/usr/local/share/"):
                                    checked[p] = ""
                                else:
                                    st = os.stat(real)
                                    if not stat.S_ISREG(st.st_mode):
                                        checked[p] = "not a regular file"
                                    elif not st.st_mode & 0o111:
                                        checked[p] = f"not executable (mode {stat.S_IMODE(st.st_mode):o})"
                                    else:
                                        checked[p] = ""
                            if checked[p]:
                                bad.append(f"unit-paths-gate: {p}: {checked[p]}, named by {where}")
    for line in bad:
        print(line)
    print(f"unit-paths-gate: {len(checked)} /usr/local paths named by units, {len(bad)} bad")
    sys.exit(1 if bad else 0)


main()
