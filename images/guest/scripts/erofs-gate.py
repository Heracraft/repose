#!/usr/bin/env python3
"""The artifact gate (images/guest/build.sh, DECISIONS I-1466): reads the
raw inode of each path in rootfs.erofs and fails unless its mode, uid and
gid are the expected ones. dump.erofs finds each path's NID; its Access
field is masked to 0777, so the mode (setuid and sticky bits included) is
read from the inode itself: superblock at byte 1024 (blkszbits at +12,
meta_blkaddr at +40), inode at (meta_blkaddr << blkszbits) + nid * 32,
i_format at +0 (bit 0: extended), i_mode at +4, uid/gid at +24/+26
(compact) or +24/+28 (extended).

usage: erofs-gate.py DUMP_EROFS IMAGE
"""
import re, struct, subprocess, sys

WANT = {
    "/": 0o40755, "/etc": 0o40755, "/etc/ssh": 0o40755, "/usr": 0o40755,
    "/tmp": 0o41777, "/var/tmp": 0o41777, "/usr/bin/sudo": 0o104755,
}

dump, image = sys.argv[1], sys.argv[2]
with open(image, "rb") as f:
    sb = f.read(2048)[1024:]
    magic, = struct.unpack_from("<I", sb, 0)
    if magic != 0xE0F5E1E2:
        sys.exit(f"erofs-gate: {image} is not erofs")
    blkszbits = sb[12]
    meta_blkaddr, = struct.unpack_from("<I", sb, 40)
    bad = 0
    for path, want in WANT.items():
        out = subprocess.run([dump, f"--path={path}", image], capture_output=True, text=True).stdout
        m = re.search(r"NID:\s*(\d+)", out)
        if not m:
            print(f"erofs-gate: {path}: not in the image"); bad += 1; continue
        f.seek((meta_blkaddr << blkszbits) + int(m.group(1)) * 32)
        ino = f.read(64)
        fmt, mode = struct.unpack_from("<H", ino, 0)[0], struct.unpack_from("<H", ino, 4)[0]
        uid, gid = struct.unpack_from("<II", ino, 24) if fmt & 1 else struct.unpack_from("<HH", ino, 24)
        ok = mode == want and uid == 0 and gid == 0
        print(f"erofs-gate: {path} {mode:o} {uid}:{gid} {'ok' if ok else f'WANT {want:o} 0:0'}")
        bad += not ok
sys.exit(1 if bad else 0)
