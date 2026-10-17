#!/usr/bin/env python3
"""Generate docs/DECISIONS-INDEX.md from docs/DECISIONS.md.

One line per entry: id, title, date (when the entry's opening parenthetical
carries one), status (superseded / amended / reversed, when DECISIONS.md says
so, either in the entry itself or in a later entry that names it), and the
line the entry starts on, so a reader opens DECISIONS.md at that line.

    python3 ops/dev/decisions-index.py            # write the index
    python3 ops/dev/decisions-index.py --check    # exit 1 if the index is stale

Either way it exits 1 when two entries declare the same id, which is how
one of them stays invisible: the index keeps the first and the second's
title, date and status are never rendered, and a reader who follows a
cross-reference lands on the wrong entry.

Standard library only; run from anywhere.
"""
import collections
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SRC = ROOT / "docs" / "DECISIONS.md"
OUT = ROOT / "docs" / "DECISIONS-INDEX.md"

ID = r"(?:R\d+|I)-\d+[a-z]?"
# An entry may open with a range ("I-195..I-205") or several ids joined by
# " + " ("R2-12 + R3-16 + R4-7 + R4-8"); every id in the group names it.
ID_GROUP = ID + r"(?:\.\." + ID + r")?(?: \+ " + ID + r")*"
ENTRY_START = re.compile(r"^\*\*(" + ID_GROUP + r")\b")
DATE = re.compile(r"\b(\d{4}-\d{2}-\d{2})\b")

# The ids after a verb may be a list: "amends I-370, I-376, I-381, I-384"
# or "amends I-5 and I-6". Only the first id used to be read, so an entry
# that amended four others marked one of them (I-391).
LIST_TAIL = r"((?:(?:,? and |, )" + ID + r"(?:'s)?)*)"
# Statements an entry makes about an earlier entry: (pattern, verb for target).
BACKREFS = [
    (re.compile(r"\bSupersedes (?:what is left of )?(" + ID + r")('s)?" + LIST_TAIL, re.I), "superseded"),
    (re.compile(r"\breplaces (" + ID + r")('s)?" + LIST_TAIL, re.I), "superseded"),
    (re.compile(r"\bamends (" + ID + r")('s)?" + LIST_TAIL, re.I), "amended"),
    (re.compile(r"\breverses (" + ID + r")('s)?" + LIST_TAIL, re.I), "reversed"),
    (re.compile(r"(" + ID + r")('s)? text; superseded here", re.I), "superseded"),
]
TAIL_ID = re.compile(r"(" + ID + r")('s)?")
# Statements an entry makes about itself.
SELF = [
    (re.compile(r"\*?(?:Replaced|Superseded) by:?\*?:? ?(" + ID + r"(?: \([^)]*\))?(?: and " + ID + r")?)", re.I), "superseded"),
    (re.compile(r"\*?Reversed by:?\*?:? ?(" + ID + r")", re.I), "reversed"),
]
AMENDED_SELF = re.compile(r"^\*\*" + ID + r"(?: \(amended ([^)]*)\)| amended)", re.I)


def parse(text):
    lines = text.split("\n")
    entries, section = [], ""
    cur = None
    for n, line in enumerate(lines, 1):
        if line.startswith("## "):
            section = line[3:].strip()
            if cur:
                entries.append(cur)
                cur = None
            continue
        m = ENTRY_START.match(line)
        if m:
            if cur:
                entries.append(cur)
            cur = {"id": m.group(1), "line": n, "section": section, "text": [line]}
        elif cur is not None:
            cur["text"].append(line)
    if cur:
        entries.append(cur)
    for e in entries:
        flat = " ".join(" ".join(e["text"]).split())
        e["flat"] = flat
        e["aliases"] = re.findall(ID, e["id"]) if ".." not in e["id"] else [e["id"]]
        m = re.match(r"^\*\*(" + ID_GROUP + r")(.*?)\*\*", flat)
        title = m.group(2) if m else flat[len(e["id"]) + 2 :]
        title = re.sub(r"^(?:amended)?[.:]\s*", "", title.strip()).strip()
        e["title"] = title.rstrip(".") if title else ""
        rest = flat[m.end() :].lstrip() if m else ""
        paren = re.match(r"^\(([^)]*)\)", rest)
        d = DATE.search(paren.group(1)) if paren else None
        e["date"] = d.group(1) if d else ""
        e["status"] = []
    return entries


def statuses(entries):
    by_id = {}
    for e in entries:
        for a in e["aliases"]:
            by_id.setdefault(a, e)
    for e in entries:
        if AMENDED_SELF.match(e["flat"]):
            e["status"].append("amended")
        for pat, verb in SELF:
            for m in pat.finditer(e["flat"]):
                note = f"{verb} by {re.sub(r' [(][^)]*[)]', '', m.group(1))}"
                if note not in e["status"]:
                    e["status"].append(note)
    for src in entries:
        for pat, verb in BACKREFS:
            for m in pat.finditer(src["flat"]):
                named = [(m.group(1), m.group(2))]
                if m.lastindex and m.lastindex >= 3 and m.group(3):
                    named += [(t.group(1), t.group(2)) for t in TAIL_ID.finditer(m.group(3))]
                for ref, possessive in named:
                    tgt = by_id.get(ref)
                    if not tgt or tgt is src:
                        continue
                    # "amends R4-8's $10" or an id that is one of several in a
                    # grouped entry touches part of that entry, not all of it.
                    partly = "partly " if (possessive or len(tgt["aliases"]) > 1) else ""
                    note = f"{partly}{verb} by {src['id']}"
                    if f"{verb} by {src['id']}" in tgt["status"] or note in tgt["status"]:
                        continue
                    tgt["status"].append(note)


def render(entries):
    out = [
        "# Decisions index",
        "",
        "Generated from [DECISIONS.md](DECISIONS.md) by `ops/dev/decisions-index.py`;",
        "do not edit by hand. Run the script after adding an entry, and",
        "`python3 ops/dev/decisions-index.py --check` to see whether this file is stale.",
        "",
        "Read this first, then open DECISIONS.md at the entries you need (`L` is the",
        "line the entry starts on). Status is only what DECISIONS.md itself says: an",
        "entry marked superseded, amended or reversed has a later entry that says so,",
        "and the later entry wins. The entry text is the decision; a title here is a",
        "pointer, not a summary.",
        "",
        f"{len(entries)} entries.",
    ]
    section = None
    for e in entries:
        if e["section"] != section:
            section = e["section"]
            out += ["", f"## {section}", ""]
        parts = [f"- **{e['id']}** {e['title']}"]
        meta = []
        if e["date"]:
            meta.append(e["date"])
        meta += e["status"]
        meta.append(f"L{e['line']}")
        out.append(parts[0] + " — " + "; ".join(meta))
    return "\n".join(out) + "\n"


# KNOWN_DUPLICATES are the two collisions that were already in
# DECISIONS.md when this check was added (2026-10-15): the staging-host
# lane and the pricing lane each took I-642 and I-647 while working at the
# same time. They are left as they are on purpose, since renumbering them
# now would move the cross-references in four documents and in the code
# that cites them; a new collision is what this check is for.
KNOWN_DUPLICATES = {"I-642", "I-647"}


def duplicates(entries):
    """ids declared by more than one entry, as {id: [line, ...]}."""
    where = collections.defaultdict(list)
    for e in entries:
        for a in e["aliases"]:
            where[a].append(e["line"])
    return {i: ls for i, ls in where.items() if len(ls) > 1 and i not in KNOWN_DUPLICATES}


def main(argv):
    check = "--check" in argv
    entries = parse(SRC.read_text())
    dups = duplicates(entries)
    if dups:
        src = SRC.relative_to(ROOT)
        for i, lines in sorted(dups.items()):
            at = ", ".join(str(n) for n in lines)
            print(f"{src}: {i} is the id of {len(lines)} entries, at lines {at}", file=sys.stderr)
        print(
            "reserve ids with `ops/dev/release-queue id` and renumber the later entry; "
            "the index would keep the first and hide the rest",
            file=sys.stderr,
        )
        return 1
    statuses(entries)
    text = render(entries)
    if check:
        cur = OUT.read_text() if OUT.exists() else ""
        if cur != text:
            print(f"{OUT.relative_to(ROOT)} is stale; run ops/dev/decisions-index.py", file=sys.stderr)
            return 1
        print(f"{OUT.relative_to(ROOT)} is current ({len(entries)} entries)")
        return 0
    OUT.write_text(text)
    print(f"wrote {OUT.relative_to(ROOT)} ({len(entries)} entries, {len(text)} bytes)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
