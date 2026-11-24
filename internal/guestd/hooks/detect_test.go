package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetect names the agent of every recorded Claude and Codex payload
// (I-1608), and no agent for what could be either or is not JSON.
func TestDetect(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		files, err := filepath.Glob(filepath.Join("testdata", agent, "*.json"))
		if err != nil || len(files) == 0 {
			t.Fatalf("%s testdata: %v %v", agent, files, err)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			// The placeholder mapper_test replaces with a temp file; a
			// real Stop names a file under ~/.claude/projects.
			b = []byte(strings.ReplaceAll(string(b), `"TRANSCRIPT"`, `"/home/dev/.claude/projects/x.jsonl"`))
			got := Detect(b)
			if strings.HasSuffix(f, "other-event.json") {
				if got != "" && got != agent {
					t.Errorf("%s: Detect = %q", f, got)
				}
				continue
			}
			if got != agent {
				t.Errorf("%s: Detect = %q, want %q", f, got, agent)
			}
		}
	}
	for _, p := range []string{``, `not json`, `{}`, `{"hook_event_name":"UserPromptSubmit"}`, `{"hook_event_name":"AfterAgent"}`} {
		if got := Detect([]byte(p)); got != "" {
			t.Errorf("Detect(%q) = %q, want \"\"", p, got)
		}
	}
}
