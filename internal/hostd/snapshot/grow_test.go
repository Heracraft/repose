package snapshot

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// A Mac volume grown while its machine ran was snapshotted at its new
// size; a restore into the size the api asks for refused every extent past
// it ("extent 42949672960+65536 does not fit the 42949672960-byte device",
// e2e-pocketmail, 2026-11-26). With GrowToSnapshot the file grows to the
// snapshot's size first, and the header is not consumed.
func TestGrowToSnapshot(t *testing.T) {
	dev := filepath.Join(t.TempDir(), "vol.img")
	if err := os.WriteFile(dev, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(dev, 40<<20); err != nil {
		t.Fatal(err)
	}
	var hdr [16]byte
	copy(hdr[:8], extentMagic)
	binary.LittleEndian.PutUint64(hdr[8:], 120<<20)
	br := bufio.NewReader(bytes.NewReader(append(hdr[:], 1, 2, 3)))
	if err := growToSnapshot(dev, br); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dev); fi.Size() != 120<<20 {
		t.Fatalf("size %d", fi.Size())
	}
	if b, _ := br.Peek(8); string(b) != extentMagic {
		t.Fatal("the header was consumed")
	}
	// Never shrinks.
	binary.LittleEndian.PutUint64(hdr[8:], 10<<20)
	if err := growToSnapshot(dev, bufio.NewReader(bytes.NewReader(hdr[:]))); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dev); fi.Size() != 120<<20 {
		t.Fatalf("shrunk to %d", fi.Size())
	}
}
