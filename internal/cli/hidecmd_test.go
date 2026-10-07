package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hideCommand makes name missing for the rest of the test, as on a base
// that predates it: each PATH directory that has it is replaced by a copy
// of links to everything else there, and the carry script's extra tool
// directories are emptied (REPOSE_TOOL_DIRS). So a host that has name
// (this box once it runs a base with repose-mcp) tests the same.
func hideCommand(t *testing.T, name string) {
	t.Helper()
	var dirs []string
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(d, name)); err != nil {
			dirs = append(dirs, d)
			continue
		}
		shadow := t.TempDir()
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if e.Name() == name {
				continue
			}
			_ = os.Symlink(filepath.Join(d, e.Name()), filepath.Join(shadow, e.Name()))
		}
		dirs = append(dirs, shadow)
	}
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
	t.Setenv("REPOSE_TOOL_DIRS", "")
}
