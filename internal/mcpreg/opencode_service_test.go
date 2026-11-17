package mcpreg

import (
	"bytes"
	"path/filepath"
	"testing"
)

// The tests never reach a real opencode: a box running them may have one on
// PATH, and its service is not the test's to restart.
func init() {
	restartOpencode = func(func(string)) {}
}

// TestOpencodeServiceRestartsOnChange: sync restarts opencode's background
// service exactly when it changed ~/.config/opencode/opencode.json
// (DECISIONS I-1050): on the first render, not on the second, and again
// when a server arrives.
func TestOpencodeServiceRestartsOnChange(t *testing.T) {
	calls := 0
	old := restartOpencode
	restartOpencode = func(func(string)) { calls++ }
	defer func() { restartOpencode = old }()
	home := t.TempDir()
	p := testPaths(t, filepath.Join("testdata", "render", "platform-only"), home)
	var b bytes.Buffer
	Sync(p, []string{"opencode"}, &b)
	if calls != 1 || b.Len() != 0 {
		t.Fatalf("first sync: %d restarts, stderr %q", calls, b.String())
	}
	Sync(p, []string{"opencode"}, &b)
	if calls != 1 {
		t.Fatalf("a sync that changed nothing restarted the service (%d)", calls)
	}
	writeFile(t, filepath.Join(home, ".repose/mcp/forward/fw.json"), `{"version":1,"name":"fw"}`)
	Sync(p, []string{"opencode"}, &b)
	if calls != 2 || b.Len() != 0 {
		t.Fatalf("after a forward: %d restarts, stderr %q", calls, b.String())
	}
}

// TestOpencodeDisabledEntryStays: the platform servers are in the reader's
// own opencode.json, so a `disabled: true` the reader adds to one makes
// the entry theirs (DECISIONS I-1050): the next sync leaves it, restarts
// nothing, and status no longer counts opencode among that server's agents.
func TestOpencodeDisabledEntryStays(t *testing.T) {
	calls := 0
	old := restartOpencode
	restartOpencode = func(func(string)) { calls++ }
	defer func() { restartOpencode = old }()
	home := t.TempDir()
	p := testPaths(t, filepath.Join("testdata", "render", "platform-only"), home)
	var b bytes.Buffer
	Sync(p, []string{"opencode"}, &b)
	cfg := opencodeConfig(home)
	doc, _, _ := readFile(cfg)
	edited := bytes.Replace(doc, []byte(`"playwright": {`), []byte(`"playwright": {"disabled": true,`), 1)
	if bytes.Equal(doc, edited) {
		t.Fatalf("no playwright entry in %s", doc)
	}
	writeFile(t, cfg, string(edited))
	Sync(p, []string{"opencode"}, &b)
	if got, _, _ := readFile(cfg); !bytes.Equal(got, edited) || b.Len() != 0 || calls != 1 {
		t.Fatalf("after the reader's disabled: true:\n%s\nstderr %q, %d restarts", got, b.String(), calls)
	}
	st, err := ReadStatus(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st.Servers {
		switch s.Name {
		case "playwright":
			for _, a := range s.Agents {
				if a == "opencode" {
					t.Fatalf("playwright still counted for opencode: %+v", s)
				}
			}
		case "chrome-devtools":
			if !contains(s.Agents, "opencode") {
				t.Fatalf("chrome-devtools lost opencode: %+v", s)
			}
		}
	}
}
