#!/usr/bin/env python3
# erofs-mode.py IMAGE PATH...: the full i_mode (special bits included) and
# owner of each PATH, read from the inode itself. dump.erofs 1.9.4 masks
# its "Access:" to 0777, so it cannot show a sticky or setuid bit.
import re, struct, subprocess, sys
img, paths = sys.argv[1], sys.argv[2:]
EU = "/nix/store/r80xfvq46alb3fqbka1v0c0h1mphlah4-erofs-utils-1.9.4/bin/dump.erofs"
with open(img, "rb") as f:
    f.seek(1024); sb = f.read(128)
    if struct.unpack_from("<I", sb, 0)[0] != 0xE0F5E1E2: sys.exit("not erofs")
    blkbits = sb[12]; meta = struct.unpack_from("<I", sb, 40)[0]
    for p in paths:
        out = subprocess.run([EU, "--path=" + p, img], capture_output=True, text=True).stdout
        m = re.search(r"NID: (\d+)", out); o = re.search(r"Uid: *(\d+) *Gid: *(\d+)", out)
        if not m: print(f"{p} missing"); continue
        f.seek((meta << blkbits) + int(m.group(1)) * 32 + 4)
        mode = struct.unpack("<H", f.read(2))[0] & 0o7777
        print(f"{p} {mode:04o} {o.group(1)}:{o.group(2)}")
