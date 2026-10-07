package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsMCP(t *testing.T) {
	cases := []struct {
		argv []string
		ok   bool
		rest []string
	}{
		{[]string{"/run/current-system/sw/bin/repose-mcp", "sync", "claude"}, true, []string{"sync", "claude"}},
		{[]string{"repose-hook", "mcp", "status", "--json"}, true, []string{"status", "--json"}},
		{[]string{"repose-hook", "--agent", "claude"}, false, nil},
		{[]string{"repose-notify", "hi"}, false, nil},
	}
	for _, c := range cases {
		rest, ok := isMCP(c.argv)
		if ok != c.ok || strings.Join(rest, " ") != strings.Join(c.rest, " ") {
			t.Errorf("%v: %v %v", c.argv, rest, ok)
		}
	}
}

func TestRunMCPDispatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := runMCP(args, strings.NewReader(""), &out, &errb)
		return code, out.String(), errb.String()
	}
	if code, _, _ := run(); code != 2 {
		t.Errorf("no args: %d", code)
	}
	if code, out, _ := run("--help"); code != 0 || !strings.Contains(out, "repose-mcp sync") {
		t.Errorf("help: %d %q", code, out)
	}
	// The forward's two ends (internal/mcpshim has their tests): the shim
	// ends with its agent's stdin, and hold wants names.
	if code, out, _ := run("notes"); code != 0 || out != "" {
		t.Errorf("shim with no agent: %d %q", code, out)
	}
	if code, _, _ := run("hold"); code != 2 {
		t.Errorf("hold with no names: %d", code)
	}
	if code, out, _ := run("hold", "--remove", "notes"); code != 0 || out != "notes\n" {
		t.Errorf("hold --remove of a name never forwarded: %d %q", code, out)
	}
	if code, _, _ := run("bad name!"); code != 2 {
		t.Errorf("bad name: %d", code)
	}
	if code, _, errs := run("run", "nothing"); code != 127 || !strings.Contains(errs, "nothing is not in ~/.repose/mcp/laptop.json") {
		t.Errorf("run nothing: %d %q", code, errs)
	}
	// sync exits 0 even on a broken registry.
	if err := os.MkdirAll(filepath.Join(home, ".repose", "mcp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".repose", "mcp", "laptop.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	// sync exits 0 on a broken laptop.json and names it; the file's own
	// servers are left out (internal/mcpreg TestBrokenSourceCostsOnlyItsOwn).
	if code, _, errs := run("sync", "claude"); code != 0 || !strings.Contains(errs, "laptop.json does not parse") {
		t.Errorf("sync broken: %d %q", code, errs)
	}
	if code, _, _ := run("sync", "nosuchagent"); code != 0 {
		t.Errorf("sync unknown agent: %d", code)
	}
	// run refuses a server whose secret is missing, before starting it.
	if err := os.WriteFile(filepath.Join(home, ".repose", "mcp", "laptop.json"), []byte(`{"version":1,"user":{"linear":{"command":"sh","args":["-c","echo started"],"env":{"T":"${REPOSE_TEST_LINEAR_T}"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Unsetenv("REPOSE_TEST_LINEAR_T")
	if code, out, errs := run("run", "linear"); code != 1 || out != "" || errs != "repose-mcp: linear needs the secret REPOSE_TEST_LINEAR_T; set it with `repose secrets set REPOSE_TEST_LINEAR_T` on your laptop, then restart the agent.\n" {
		t.Errorf("run without its secret: %d %q %q", code, out, errs)
	}
	// run NAME CHECKOUT takes that checkout's server; run NAME the first
	// checkout's, as before.
	if err := os.WriteFile(filepath.Join(home, ".repose", "mcp", "laptop.json"), []byte(`{"version":1,"projects":{
	  "/home/dev/app":{"db":{"command":"sh","env":{"U":"${REPOSE_TEST_APP_T}"}}},
	  "/home/dev/lib":{"db":{"command":"sh","env":{"U":"${REPOSE_TEST_LIB_T}"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Unsetenv("REPOSE_TEST_APP_T")
	_ = os.Unsetenv("REPOSE_TEST_LIB_T")
	if code, _, errs := run("run", "db", "/home/dev/lib"); code != 1 || !strings.Contains(errs, "needs the secret REPOSE_TEST_LIB_T;") {
		t.Errorf("run db lib: %d %q", code, errs)
	}
	if code, _, errs := run("run", "db"); code != 1 || !strings.Contains(errs, "needs the secret REPOSE_TEST_APP_T;") {
		t.Errorf("run db: %d %q", code, errs)
	}
	if code, _, errs := run("run", "db", "/home/dev/other"); code != 1 || !strings.Contains(errs, "needs the secret REPOSE_TEST_APP_T;") {
		t.Errorf("run db in a checkout without it: %d %q", code, errs)
	}
	if code, _, _ := run("run", "db", "/home/dev/lib", "extra"); code != 2 {
		t.Errorf("run with three words: %d", code)
	}
	if err := os.WriteFile(filepath.Join(home, ".repose", "mcp", "laptop.json"), []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run("status", "--json")
	var st struct {
		Version int `json:"version"`
		Servers []any
	}
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || st.Version != 1 {
		t.Errorf("status: %d %q", code, out)
	}
	if code, _, _ := run("status"); code != 2 {
		t.Errorf("status without --json: %d", code)
	}
}
