#!/usr/bin/env python3
"""The erofs feature gate (images/guest/build.sh, DECISIONS I-1556): fails
unless the guest kernel (Ubuntu 6.8) can read every inode of rootfs.erofs.

It checks what that kernel checks when it opens an inode, so an image it
would refuse fails the build instead of the boot:
  - the superblock's incompatible features are ones 6.8 knows (0x7f:
    lz4_0padding, compr_cfgs/big_pcluster, chunked_file, device_table/
    compr_head2, ztailpacking, fragments/dedupe, xattr_prefixes), the
    block size is 4096, and the advertised algorithms are lz4, lzma or
    deflate;
  - every inode reached from the root has a data layout 6.8 knows (0-4);
  - every compressed inode's map header names algorithms the superblock
    advertises (HEAD2 only with compr_head2), uses big pclusters only when the superblock has the
    feature (6.8: "per-inode big pcluster without sb feature"), sets both
    or neither big pcluster bit when compact ("head1/2 ... should be
    consistent"), and uses ztailpacking or fragments only when the
    superblock has them.
erofs-utils 1.7.1 wrote small files of a tar as compressed inodes with no
data and a garbage map header, and the guest froze at boot (guest
2026.11.22.11); this gate and fsck.erofs --extract both fail that image.

Layout (linux include/erofs_fs.h): superblock at byte 1024; blkszbits
+12, root_nid +14 (u16), meta_blkaddr +40, feature_incompat +80,
available_compr_algs +84 (u16). Inode at (meta_blkaddr << blkszbits) +
nid * 32: i_format +0 (bit 0 extended, bits 1-3 layout), i_xattr_icount
+2, i_mode +4, i_size +8 (u32 compact, u64 extended), i_u +16 (raw
block address for flat layouts); 32 bytes compact, 64 extended. The map
header of a compressed inode follows the inode and its xattrs, aligned
to 8: h_advise +4 (u16), h_algorithmtype +6 (head1 low nibble, head2 high
nibble), h_clusterbits +7 (bit 7: the whole file is a fragment).

usage: erofs-features-gate.py IMAGE
"""
import stat, struct, sys

KNOWN_INCOMPAT = 0x7F  # what linux 6.8 EROFS_ALL_FEATURE_INCOMPAT accepts
F_COMPR_CFGS = F_BIG_PCLUSTER = 0x2
F_COMPR_HEAD2 = 0x8
F_ZTAILPACKING = 0x10
F_FRAGMENTS = 0x20
ALGS_KNOWN = 0x7  # lz4, lzma, deflate; zstd (3) is 6.10
A_BIG1, A_BIG2, A_INLINE, A_FRAGMENT = 0x2, 0x4, 0x8, 0x20
FLAT_PLAIN, COMP_FULL, FLAT_INLINE, COMP_COMPACT, CHUNK = range(5)
DIR_TYPE = 2


def main(image):
    bad = []
    with open(image, "rb") as f:
        sb = f.read(2048)[1024:]
        if struct.unpack_from("<I", sb, 0)[0] != 0xE0F5E1E2:
            sys.exit(f"erofs-features-gate: {image} is not erofs")
        blkszbits = sb[12]
        root_nid, = struct.unpack_from("<H", sb, 14)
        meta_blkaddr, = struct.unpack_from("<I", sb, 40)
        incompat, = struct.unpack_from("<I", sb, 80)
        u1, = struct.unpack_from("<H", sb, 84)
        bs = 1 << blkszbits
        algs = u1 if incompat & F_COMPR_CFGS else 0x1  # no cfgs: lz4 only
        if blkszbits != 12:
            bad.append(f"block size {bs}, the guest kernel reads 4096")
        if incompat & ~KNOWN_INCOMPAT:
            bad.append(f"superblock incompatible features {incompat:#x}, "
                       f"linux 6.8 knows {KNOWN_INCOMPAT:#x}")
        if algs & ~ALGS_KNOWN:
            bad.append(f"superblock algorithms {algs:#x}, linux 6.8 has {ALGS_KNOWN:#x}")
        meta = meta_blkaddr << blkszbits

        def inode(nid):
            f.seek(meta + nid * 32)
            raw = f.read(64)
            fmt, icount, mode = struct.unpack_from("<HHH", raw, 0)
            ext = fmt & 1
            size = struct.unpack_from("<Q" if ext else "<I", raw, 8)[0]
            raw_blk, = struct.unpack_from("<I", raw, 16)
            xsize = 12 + (icount - 1) * 4 if icount else 0
            return (fmt >> 1) & 0x7, mode, size, raw_blk, (64 if ext else 32) + xsize

        def check_compressed(nid, layout, isize):
            f.seek(meta + nid * 32 + ((isize + 7) & ~7))
            h = f.read(8)
            advise, = struct.unpack_from("<H", h, 4)
            head1, head2, clusterbits = h[6] & 0xF, h[6] >> 4, h[7]
            why = []
            if clusterbits & 0x80:  # whole-file fragment: no map to check
                if not incompat & F_FRAGMENTS:
                    why.append("whole-file fragment without the sb feature")
                return why
            heads = [("HEAD1", head1)] + ([("HEAD2", head2)] if incompat & F_COMPR_HEAD2 else [])
            for name, a in heads:
                if a > 2 or not algs >> a & 1:
                    why.append(f"{name} algorithm {a} not in the sb's {algs:#x}")
            if advise & (A_BIG1 | A_BIG2) and not incompat & F_BIG_PCLUSTER:
                why.append("per-inode big pcluster without sb feature")
            if layout == COMP_COMPACT and bool(advise & A_BIG1) != bool(advise & A_BIG2):
                why.append("big pcluster head1/2 of compact indexes inconsistent")
            if advise & A_INLINE and not incompat & F_ZTAILPACKING:
                why.append("ztailpacking without the sb feature")
            if advise & A_FRAGMENT and not incompat & F_FRAGMENTS:
                why.append("fragment without the sb feature")
            return why

        def read_dir(nid, layout, size, raw_blk, isize):
            if layout == FLAT_PLAIN:
                f.seek(raw_blk << blkszbits)
                data = f.read(size)
            elif layout == FLAT_INLINE:
                full = size // bs * bs
                f.seek(raw_blk << blkszbits)
                data = f.read(full)
                f.seek(meta + nid * 32 + isize)
                data += f.read(size - full)
            else:
                return None
            out = []
            for off in range(0, len(data), bs):
                blk = data[off:off + bs]
                n = struct.unpack_from("<H", blk, 8)[0] // 12
                for i in range(n):
                    child, _, ftype = struct.unpack_from("<QHB", blk, i * 12)
                    out.append((child, ftype))
            return out

        seen, stack, n_inodes, n_comp, n_bad = {root_nid}, [root_nid], 0, 0, 0
        while stack:
            nid = stack.pop()
            n_inodes += 1
            layout, mode, size, raw_blk, isize = inode(nid)
            if layout > CHUNK:
                bad.append(f"nid {nid}: data layout {layout}, linux 6.8 knows 0-4")
                continue
            if layout in (COMP_FULL, COMP_COMPACT):
                n_comp += 1
                why = check_compressed(nid, layout, isize)
                n_bad += bool(why)
                bad += [f"nid {nid}: {w}" for w in why]
            if stat.S_ISDIR(mode):
                entries = read_dir(nid, layout, size, raw_blk, isize)
                if entries is None:
                    bad.append(f"nid {nid}: directory layout {layout}, the gate reads 0 or 2 only")
                    continue
                for child, ftype in entries:
                    if child not in seen:
                        seen.add(child)
                        stack.append(child)
    for b in bad[:20]:
        print(f"erofs-features-gate: {b}")
    if len(bad) > 20:
        print(f"erofs-features-gate: ... and {len(bad) - 20} more")
    print(f"erofs-features-gate: {n_inodes} inodes, {n_comp} compressed ({n_bad} unreadable), "
          f"incompat {incompat:#x}, algorithms {algs:#x}: "
          f"{'FAIL' if bad else 'ok'}")
    return 1 if bad else 0


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__.rsplit("usage: ", 1)[1].strip())
    sys.exit(main(sys.argv[1]))
