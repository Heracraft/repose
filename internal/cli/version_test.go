package cli

import (
	"io"
	"os"
	"testing"
)

// TestVersionLine: `repose version` and `repose --version` print
// "repose <version>" and nothing after it (DECISIONS I-1624).
func TestVersionLine(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		old := os.Stdout
		os.Stdout = w
		root := newRootCmd("0.1.65")
		root.SetOut(w)
		root.SetArgs(args)
		runErr := root.Execute()
		os.Stdout = old
		_ = w.Close()
		got, _ := io.ReadAll(r)
		_ = r.Close()
		if runErr != nil {
			t.Fatalf("%v: %v", args, runErr)
		}
		if string(got) != "repose 0.1.65\n" {
			t.Errorf("%v printed %q, want %q", args, got, "repose 0.1.65\n")
		}
	}
}
