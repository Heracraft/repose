package cli

import (
	"fmt"
	"os"
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
	// The test binary doubles as a process the tests start: the stdio MCP
	// server and repose-mcp of the forward's tests (mcpforward_test.go).
	if h := os.Getenv(testHelperEnv); h != "" {
		os.Exit(runTestHelper(h, os.Args[1:]))
	}
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
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
