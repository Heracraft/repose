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
	// The forward entry points are the forward unit's; this base says so.
	for _, args := range [][]string{{"hold", "notes"}, {"notes"}} {
		code, _, errs := run(args...)
		if code != 1 || !strings.Contains(errs, "forwarding is not built in this base") {
			t.Errorf("%v: %d %q", args, code, errs)
		}
	}
	if code, _, _ := run("bad name!"); code != 2 {
		t.Errorf("bad name: %d", code)
	}
	if code, _, errs := run("run", "nothing"); code != 127 || !strings.Contains(errs, "nothing is not in ~/.repose/mcp/laptop.json") {
		t.Errorf("run nothing: %d %q", code, errs)
	}
	// sync exits 0 even on a broken registry, and changes nothing.
	if err := os.MkdirAll(filepath.Join(home, ".repose", "mcp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".repose", "mcp", "laptop.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := run("sync", "claude"); code != 0 || !strings.Contains(errs, "MCP servers not updated") {
		t.Errorf("sync broken: %d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Errorf("sync wrote ~/.claude.json from a broken registry")
	}
	if code, _, _ := run("sync", "nosuchagent"); code != 0 {
		t.Errorf("sync unknown agent: %d", code)
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
