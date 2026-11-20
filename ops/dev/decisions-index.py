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
import subprocess
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


# KNOWN_ORPHANS are ids that code or docs cite with no entry behind them, each
# waived with the reason it cannot simply be written. A fifth line here is a
# confession, not a configuration: the check exists because I-904 was cited in
# eight shipped places -- docs/interfaces/api.md among them, a contract doc --
# for two days with no entry at all, and was found by accident while someone
# asked an unrelated question about reservations (DECISIONS I-994).
KNOWN_ORPHANS = {
    # internal/ca/sshca/sshca.go:18 and docs/ops/RUNBOOK.md:3790. An
    # interview-round id used as a cross-reference and never written as an
    # entry, like the R-series ids DECISIONS.md records only in prose.
    "R3-9": "interview-round id used as a cross-reference, never an entry",
    "R4-1": "same, docs/features/config.md",
    "R4-18": "same, docs/features/run-and-attach.md",
}

# Citations are matched with their own pattern, not the heading one. ID above
# allows a trailing letter because a heading may read "I-12a"; no entry does,
# and allowing it here would let "I-7xx" in a comment read as a citation of
# I-7. The negative lookahead is what rejects it, and it is PCRE -- `git grep
# -E` matches nothing at all with it, silently, which is the same class of
# fault this check is for.
CITE = r"(?:R[0-9]+|I)-[0-9]+(?![0-9A-Za-z_-])"

# Where a bare id is a claim about an entry. Deliberately out: DECISIONS.md
# itself, which records reserved-and-unused ids in prose (two of them at
# DECISIONS.md:21249 and :22637 -- named by line and not by number, because a
# number written here would itself be a citation in an enforced path, which is
# how this comment first failed its own check); and STATUS.md,
# status-archive/, releases/ and research/,
# which are logs of claimed, abandoned and superseded work where a dead id is
# the history rather than a mistake.
ENFORCED = [
    "internal/", "cmd/", "apps/", "nix/", "ops/", "scripts/", "test/",
    ".github/", "docs/interfaces/", "docs/features/", "docs/ops/",
    "docs/DESIGN.md", "docs/ARCHITECTURE.md", "docs/CHECKLIST.md",
    "docs/MILESTONES.md", "docs/README.md", "install.sh", "justfile",
    # Added 2026-11-20 after an entry cited in docs/REVIEW.md turned out to
    # be an orphan nothing looked for, because that file was outside this
    # list. These five
    # are normative documents in the same class as DESIGN.md and
    # CHECKLIST.md: a reader acts on them, so a dead id in one points at
    # nothing. All five were clean when they were added, 114 citations
    # between them and no orphan, so this widened the net without moving a
    # single existing line.
    "docs/REVIEW.md", "docs/SECURITY.md", "docs/PRICING.md",
    "docs/LANDING.md", "docs/GLOSSARY.md",
]


# An entry heading this file cannot parse makes the entry ABSENT rather
# than wrong: the index is "current" without it, and a citation of it is an
# orphan only where citations are enforced. On 2026-11-20 an entry was
# written with a Markdown heading instead of the bold form (the mac-mini
# branch; its number is deliberately not repeated here, because a number
# in this file is itself a citation in an enforced path). No tool could
# see it for twenty minutes while a commit and docs/REVIEW.md cited it,
# and --check printed "is current" with the same entry count before and
# after. These two shapes
# are what that mistake looks like; both match nothing in DECISIONS.md as
# it stands, which is what makes them safe to fail on. A bare id at the
# start of a line is deliberately not here: six of those are prose wrapped
# mid-sentence ("Amends\nI-269. With plans ..."), so it cannot tell a
# malformed heading from a paragraph.
MISHEADINGS = [
    (re.compile(r"^#{1,6}[ \t]*\*{0,2}(" + ID_GROUP + r")\b", re.M),
     "a Markdown heading"),
    (re.compile(r"^\*(?!\*)[ \t]*(" + ID_GROUP + r")\.", re.M),
     "one asterisk, not two"),
]


def misheadings(text):
    """Lines that look like an entry heading in a shape parse() ignores."""
    out = []
    for pat, shape in MISHEADINGS:
        for m in pat.finditer(text):
            out.append((text[: m.start()].count("\n") + 1, m.group(1), shape))
    return sorted(out)


def declared(entries):
    """Every id an entry declares, with `..` ranges expanded numerically."""
    out = set()
    for e in entries:
        for a in e["aliases"]:
            if ".." not in a:
                out.add(a)
                continue
            lo, hi = a.split("..", 1)
            m, n = re.match(r"(.*?)(\d+)[a-z]?$", lo), re.match(r"(.*?)(\d+)[a-z]?$", hi)
            if not m or not n or m.group(1) != n.group(1):
                out.add(a)
                continue
            for i in range(int(m.group(2)), int(n.group(2)) + 1):
                out.add(f"{m.group(1)}{i}")
    return out


def orphans(entries):
    """Cited ids with no entry: an existence check on the number, and nothing
    more -- it cannot tell whether the id cited was the one meant."""
    have = declared(entries)
    out = subprocess.run(
        ["git", "grep", "-nI", "-P", "-o", CITE, "--", *ENFORCED],
        cwd=ROOT, capture_output=True, text=True,
    ).stdout
    hits, files = [], set()
    for line in out.splitlines():
        path, _, rest = line.partition(":")
        lineno, _, cited = rest.partition(":")
        files.add(path)
        if cited not in have and cited not in KNOWN_ORPHANS:
            hits.append((path, lineno, cited))
    # A scan that matched no FILES is a broken scan, not a clean tree: run
    # outside a work tree, or with a bad pathspec, git grep says nothing and
    # silence would read as success. That is the fault this check is for.
    if not files:
        return None, hits
    for i in sorted(KNOWN_ORPHANS):
        if i in have:
            hits.append(("KNOWN_ORPHANS", "-", i))
    return len(files), hits


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
    bad = misheadings(SRC.read_text())
    if bad:
        src = SRC.relative_to(ROOT)
        for lineno, cited, shape in bad:
            print(f"{src}:{lineno}: {cited} looks like an entry heading written as "
                  f"{shape}; parse() ignores it, so the entry is absent and the "
                  f"index is 'current' without it", file=sys.stderr)
        print("entries start with `**I-<n>.` at the start of a line; a heading in "
              "any other shape is invisible to every tool that reads DECISIONS.md",
              file=sys.stderr)
        if check:
            return 1

    # Only under --check. Regenerating the index is a formatter and must
    # always succeed: `cut` regenerates it to settle an index conflict, and
    # ops/dev/release-queue-test drives that path in a scratch repository
    # whose tree is the real one's shape with a fixture DECISIONS.md of two
    # entries -- there every real citation is an orphan, correctly and
    # uselessly. The gate is --check, which is what CI runs and what
    # docs/ops/RELEASE.md's integrate step runs (DECISIONS I-994).
    nfiles, ghosts = orphans(entries) if check else (1, [])
    if nfiles is None:
        print("decisions-index: git grep matched no files at all; the orphan scan "
              "cannot run here, and silence is not a clean tree", file=sys.stderr)
        return 1
    if ghosts:
        src = SRC.relative_to(ROOT)
        for path, lineno, cited in ghosts:
            if path == "KNOWN_ORPHANS":
                print(f"KNOWN_ORPHANS: {cited} now has an entry in {src}; "
                      f"remove its waiver", file=sys.stderr)
            else:
                print(f"{path}:{lineno}: {cited} is cited and has no entry in {src}",
                      file=sys.stderr)
        print("reserve ids with `ops/dev/release-queue id` and write the entry before "
              "citing it; a citation with no entry points a reader at nothing "
              "(DECISIONS I-994)", file=sys.stderr)
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
