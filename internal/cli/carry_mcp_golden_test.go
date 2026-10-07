package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// mcpCarryGoldenDir holds the MCP part of a carry as the CLI sends it,
// for the guest-base VM subtest that runs it on a real base and then
// repose-agent-setup (design 3.2.5). Regenerate with
// go test ./internal/cli -run TestMCPCarryGoldenPayload -update.
const mcpCarryGoldenDir = "../../nix/guest/tests/mcp-carry"

// The VM subtest's payload is what this CLI sends for the fixture below:
// a server with a token in its env (templated to ${PROBE_TOKEN}), an HTTP
// server with a bearer header, and an Apple app left on the laptop.
func TestMCPCarryGoldenPayload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeClaudeJSON(t, home, map[string]any{
		"mcpServers": map[string]any{
			"probe":  map[string]any{"command": "npx", "args": []any{"-y", "repose-probe-mcp"}, "env": map[string]any{"PROBE_TOKEN": "NEVER-PROBE-TOKEN-VALUE"}},
			"linear": map[string]any{"type": "http", "url": "https://mcp.linear.app/mcp", "headers": map[string]any{"Authorization": "Bearer NEVER-LINEAR-BEARER"}},
			"notes":  map[string]any{"command": "npx", "args": []any{"-y", "mcp-server-apple-notes"}},
		},
	})
	mc, notes := buildMCPCarry(home, "", "todo-app", "", nil)
	if mc == nil || len(notes) > 0 {
		t.Fatalf("carry %+v, notes %v", mc, notes)
	}
	p := newGuestPayload()
	if _, err := addMCPParts(p, mc, carryOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := p.tw.Close(); err != nil {
		t.Fatal(err)
	}
	got := map[string][]byte{"script.sh": []byte(p.script.String()), "payload.tar": p.buf.Bytes()}
	for name, b := range got {
		if bytes.Contains(b, []byte("NEVER-")) {
			t.Fatalf("%s carries a laptop value", name)
		}
		path := filepath.Join(mcpCarryGoldenDir, name)
		if *updateGolden {
			if err := os.WriteFile(path, b, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run with -update to create it)", err)
		}
		if !bytes.Equal(want, b) {
			t.Errorf("%s differs from what the CLI sends now; run go test ./internal/cli -run TestMCPCarryGoldenPayload -update", path)
		}
	}
}
