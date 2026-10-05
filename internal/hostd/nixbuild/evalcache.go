package nixbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// The eval cache (DECISIONS I-405). Evaluating a guest system took 5.3 s
// of the 5.7 s Build that a restore of a destroyed project runs, for a
// configuration evaluated before: the evaluation is pure (pure-eval,
// restrict-eval, no import-from-derivation, inputs locked by the base's
// flake.lock), so its derivation path is fixed by what it reads. evalKey
// hashes that; an entry maps it to the derivation path of a build that
// succeeded. A hit whose .drv is gone from the store is a miss
// (keep-derivations holds it while the closure is rooted), and a hit whose
// build fails is forgotten and evaluated afresh (Build).

// evalCacheVersion changes when the key's inputs change.
const evalCacheVersion = "1"

// evalKey is the hash of everything the evaluation reads: the base
// checkout (a git revision, checked out once and never moved), the flake
// the attribute is read from, the fragment and the base-version label,
// the files of the fragment directory. The personal layer (I-490) is
// hashed only when there is one, so a build without it keeps the key it
// had before the layer existed.
func (b *Real) evalKey(req Request) string {
	h := sha256.New()
	for _, part := range []string{evalCacheVersion, req.BaseRef, b.BaseSubdir, b.BaseScheme, b.EvalAttr, req.BaseVersion} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	h.Write(req.Fragment)
	if len(req.Personal) > 0 {
		h.Write([]byte{0})
		h.Write([]byte("personal.nix"))
		h.Write([]byte{0})
		h.Write(req.Personal)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (b *Real) evalCachePath(key string) string {
	return filepath.Join(b.BuildsDir, ".evalcache", key)
}

// cachedDrv is the derivation an earlier successful build evaluated for
// key, when it is still in the store; "" otherwise.
func (b *Real) cachedDrv(ctx context.Context, key string) string {
	raw, err := os.ReadFile(b.evalCachePath(key))
	if err != nil {
		return ""
	}
	drv := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(drv, "/nix/store/") || !strings.HasSuffix(drv, ".drv") || strings.ContainsAny(drv, " \n") {
		return ""
	}
	if ok, err := b.PathExists(ctx, drv); err != nil || !ok {
		return ""
	}
	return drv
}

// rememberDrv records drv for key; a failure only costs the next build
// its evaluation.
func (b *Real) rememberDrv(key, drv string) {
	p := b.evalCachePath(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(drv+"\n"), 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp) // the entry is simply not cached
	}
}

func (b *Real) forgetDrv(key string) { _ = os.Remove(b.evalCachePath(key)) } // a missing entry is a miss already
