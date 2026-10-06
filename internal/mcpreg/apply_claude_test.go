package mcpreg

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func claudeFixture(t *testing.T, claudeJSON string) (Paths, string) {
	t.Helper()
	home := t.TempDir()
	dir := t.TempDir()
	plat := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(plat, []byte(`{"mcpServers":{"playwright":{"type":"stdio","command":"playwright-mcp","args":["--cdp-endpoint","http://127.0.0.1:9224"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".repose", "mcp"), 0o700); err != nil {
		t.Fatal(err)
	}
	laptop := `{"version":1,"projects":{"/home/dev/app":{"db":{"command":"pg-mcp"}}}}`
	if err := os.WriteFile(filepath.Join(home, ".repose", "mcp", "laptop.json"), []byte(laptop), 0o600); err != nil {
		t.Fatal(err)
	}
	cj := filepath.Join(home, ".claude.json")
	if claudeJSON != "" {
		if err := os.WriteFile(cj, []byte(claudeJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Paths{Home: home, Platform: plat, SecretsDir: filepath.Join(dir, "secrets"), Etc: dir}, cj
}

func TestClaudeLockHeldGivesWarningAndNoWrite(t *testing.T) {
	defer func(w time.Duration) { claudeLockWait = w }(claudeLockWait)
	claudeLockWait = 300 * time.Millisecond // stands in for the 5 s limit
	p, cj := claudeFixture(t, `{"mcpServers":{}}`)
	if err := os.Mkdir(cj+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	start := time.Now()
	Sync(p, []string{"claude"}, &stderr)
	if time.Since(start) < claudeLockWait {
		t.Errorf("sync returned before the lock wait ended")
	}
	if !strings.Contains(stderr.String(), "~/.claude.json stayed locked") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if b, _ := os.ReadFile(cj); string(b) != `{"mcpServers":{}}` {
		t.Errorf("~/.claude.json changed under a held lock: %s", b)
	}
	if _, err := os.Stat(cj + ".lock"); err != nil {
		t.Errorf("sync removed a lock it did not take: %v", err)
	}
}

func TestClaudeLockReleasedAndStaleTakenOver(t *testing.T) {
	p, cj := claudeFixture(t, `{"mcpServers":{}}`)
	// A lock a crashed Claude Code left, older than proper-lockfile's 10 s.
	if err := os.Mkdir(cj+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(cj+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	Sync(p, []string{"claude"}, &stderr)
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
	var doc map[string]any
	b, _ := os.ReadFile(cj)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["mcpServers"].(map[string]any)["playwright"]; !ok {
		t.Errorf("platform server missing after a stale lock: %s", b)
	}
	if _, err := os.Stat(cj + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock left behind: %v", err)
	}
}

func TestClaudeInvalidJSONLeftAlone(t *testing.T) {
	p, cj := claudeFixture(t, `{"mcpServers": {`)
	var stderr bytes.Buffer
	Sync(p, []string{"claude"}, &stderr)
	if b, _ := os.ReadFile(cj); string(b) != `{"mcpServers": {` {
		t.Errorf("invalid ~/.claude.json was rewritten: %s", b)
	}
	if !strings.Contains(stderr.String(), "~/.claude.json is not valid JSON") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(cj + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock left behind: %v", err)
	}
}

func TestClaudeProjectKeysSurvive(t *testing.T) {
	p, cj := claudeFixture(t, `{"projects":{"/home/dev/app":{"hasTrustDialogAccepted":true,"allowedTools":["Bash"],"mcpServers":{"mine":{"command":"my-mcp"}}}},"userID":"x"}`)
	var stderr bytes.Buffer
	Sync(p, []string{"claude"}, &stderr)
	var doc struct {
		Projects map[string]map[string]any `json:"projects"`
		UserID   string                    `json:"userID"`
	}
	b, _ := os.ReadFile(cj)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	app := doc.Projects["/home/dev/app"]
	if app["hasTrustDialogAccepted"] != true || doc.UserID != "x" {
		t.Errorf("keys lost: %s", b)
	}
	servers := app["mcpServers"].(map[string]any)
	if _, ok := servers["db"]; !ok {
		t.Errorf("local-scope server not added: %s", b)
	}
	if _, ok := servers["mine"]; !ok {
		t.Errorf("user's local-scope server removed: %s", b)
	}
	st, _ := os.Stat(cj)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
}

// Agents start in parallel and hold syncs too; concurrent syncs must leave
// one table per server in Codex's file.
func TestConcurrentSyncs(t *testing.T) {
	p, _ := claudeFixture(t, "")
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			var b bytes.Buffer
			Sync(p, Agents, &b)
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	b, err := os.ReadFile(filepath.Join(p.Home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "[mcp_servers.playwright]"); n != 1 {
		t.Errorf("%d playwright tables:\n%s", n, b)
	}
	if _, err := codexServers(string(b)); err != nil {
		t.Errorf("config does not parse: %v", err)
	}
}
