package cli

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// TestMain points HOME (and XDG_CONFIG_HOME) at a throwaway directory for
// the whole package, so no test can write the developer's real
// ~/.ssh/repose or ~/.config/repose. A restore test that renewed the
// certificate (I-188) overwrote the dev box's real certificate, config and
// known_hosts with the fake CA's before this existed. Tests that need
// their own home still call withHome.
// realHome is the developer's HOME, for the one test that runs `go build`
// (whose module and build caches live under it).
var realHome string

func TestMain(m *testing.M) {
	realHome = os.Getenv("HOME")
	home, err := os.MkdirTemp("", "repose-cli-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("XDG_CONFIG_HOME", home+"/.config")
	// A developer who moved their Claude config would otherwise have the
	// carry tests read it instead of the test home's ~/.claude.
	_ = os.Unsetenv("CLAUDE_CONFIG_DIR")
	// A test run from a herdr pane would pick herdr for new projects,
	// and the developer's own herdr must never see a test's reconcile
	// (I-510): tests that want a laptop herdr set lookHerdr themselves.
	_ = os.Unsetenv("HERDR_ENV")
	lookHerdr = func(string) (string, error) { return "", exec.ErrNotFound }
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
