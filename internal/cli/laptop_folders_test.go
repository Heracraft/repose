package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A laptop folder in the `Fetched ... in X.` line reads as the user
// spells it. tildePath, which this used, is the guest's spelling and
// takes a name relative to the guest's home, so a laptop absolute path
// came out as `~//home/agustus/Documents/...` (I-892).
func TestLaptopPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home folder")
	}
	for _, c := range []struct{ in, want string }{
		{filepath.Join(home, "code", "api"), "~/code/api"},
		{home, "~"},
		{"/var/tmp/elsewhere", "/var/tmp/elsewhere"},
	} {
		if got := laptopPath(c.in); got != c.want {
			t.Errorf("laptopPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := laptopPath("/home/agustus/Documents/projects/nuru-wasm"); strings.Contains(got, "//") {
		t.Errorf("laptopPath kept a doubled slash: %q", got)
	}
}
