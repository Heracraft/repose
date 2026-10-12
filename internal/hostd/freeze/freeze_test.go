package freeze

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A key wrapped with the file key's public half unwraps to the same key;
// what sits on disk is not the key.
func TestFileKeyWrapUnwrap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.pem")
	if err := GenerateFileKey(p); err != nil {
		t.Fatal(err)
	}
	s := &FileKey{Path: p}
	if err := s.Ready(); err != nil {
		t.Fatal(err)
	}
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	w, id, err := s.Wrap(k)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(w, k) || len(w) != 384 {
		t.Fatalf("wrapped key of %d bytes carries the key", len(w))
	}
	got, err := s.Unwrap(context.Background(), w, id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, k) || KeyCheck(got) != KeyCheck(k) {
		t.Fatal("unwrapped key differs")
	}
	w[10] ^= 1
	if _, err := s.Unwrap(context.Background(), w, id); err == nil {
		t.Fatal("a damaged wrapped key unwrapped")
	}
}

func TestKeyCheckDiffersPerKey(t *testing.T) {
	a, _ := NewKey()
	b, _ := NewKey()
	if KeyCheck(a) == KeyCheck(b) || len(a) != KeyBytes {
		t.Fatal("key checks collide or key size wrong")
	}
	Wipe(a)
	if !bytes.Equal(a, make([]byte, KeyBytes)) {
		t.Fatal("Wipe left key bytes")
	}
}

// Records round-trip, are root-only, and List skips the temporary files.
func TestRecordStore(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "freeze")}
	r := &Record{GuestID: "g1", State: StateWriting, CreatedAt: time.Now().UTC().Truncate(time.Second), WrappedKey: []byte{1, 2, 3}, KeyID: "v1"}
	if err := s.Put(r); err != nil {
		t.Fatal(err)
	}
	r.State = StateComplete
	if err := s.Put(r); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("g1")
	if err != nil || got == nil || got.State != StateComplete || !bytes.Equal(got.WrappedKey, []byte{1, 2, 3}) {
		t.Fatalf("got %+v, %v", got, err)
	}
	fi, err := os.Stat(s.Dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("record dir mode %v", fi.Mode())
	}
	_ = os.WriteFile(filepath.Join(s.Dir, ".rec-123"), []byte("x"), 0o600)
	ids, err := s.List()
	if err != nil || len(ids) != 1 || ids[0] != "g1" {
		t.Fatalf("list %v %v", ids, err)
	}
	if err := s.Put(&Record{GuestID: "../x"}); err == nil {
		t.Fatal("a guest id with a slash was accepted")
	}
	if err := s.Delete("g1"); err != nil {
		t.Fatal(err)
	}
	if r, err := s.Get("g1"); r != nil || err != nil {
		t.Fatalf("after delete: %v %v", r, err)
	}
	if err := s.Delete("g1"); err != nil {
		t.Fatal("deleting an absent record failed")
	}
}

// The fingerprint reads past the page cache and changes with the
// superblock's bytes.
func TestFingerprint(t *testing.T) {
	p := filepath.Join(t.TempDir(), "vol")
	b := make([]byte, 8192)
	b[1024+44] = 7 // s_mtime
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := Fingerprint(p)
	if err != nil {
		t.Skipf("O_DIRECT not supported here: %v", err)
	}
	b[1024+52] = 1 // s_mnt_count
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Fingerprint(p)
	if err != nil {
		t.Fatal(err)
	}
	if a == c {
		t.Fatal("fingerprint did not change with the superblock")
	}
}
