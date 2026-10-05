package psi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSomeTotal(t *testing.T) {
	got, err := SomeTotal([]byte("some avg10=27.58 avg60=25.09 avg300=25.68 total=7815208307\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"))
	if err != nil || got != 7815208307 {
		t.Fatalf("SomeTotal = %d, %v; want 7815208307", got, err)
	}
	for _, bad := range []string{"", "full avg10=0 total=5\n", "some avg10=1.0\n", "some total=x\n"} {
		if _, err := SomeTotal([]byte(bad)); err == nil {
			t.Errorf("SomeTotal(%q) succeeded", bad)
		}
	}
}

func TestReadSomeTotal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cpu.pressure")
	if err := os.WriteFile(p, []byte("some avg10=0.00 avg60=0.00 avg300=0.00 total=42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadSomeTotal(p); err != nil || got != 42 {
		t.Fatalf("ReadSomeTotal = %d, %v", got, err)
	}
	if _, err := ReadSomeTotal(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file read")
	}
}
