package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/heracraft/repose/internal/mcpreg"
)

// TestCarryMCPFeedsRegistry is the seam between the carry (I-556) and the
// guest registry (I-555): the laptop.json the carry's guest script writes
// is what mcpreg.Load reads, and sync renders it into each agent's config
// with the secret references intact and nothing from the laptop's
// credentials.
func TestCarryMCPFeedsRegistry(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	home := t.TempDir()
	secrets := t.TempDir()
	t.Setenv("REPOSE_SECRETS_DIR", secrets)

	local, _ := filepath.EvalSymlinks(f.local)
	writeClaudeJSON(t, home, map[string]any{
		"oauthAccount": map[string]any{"emailAddress": "NEVER-OAUTH-ACCOUNT"},
		"mcpServers": map[string]any{
			"linear": map[string]any{"type": "http", "url": "https://mcp.linear.app/mcp", "headers": map[string]any{"Authorization": "Bearer NEVER-LINEAR-BEARER"}},
			"gh":     map[string]any{"command": "npx", "args": []any{"-y", "gh-mcp", "--token", "NEVER-GH-TOKEN-0123456789abcdef"}},
			"notion": map[string]any{"command": "npx", "args": []any{"-y", "notion-mcp"}, "env": map[string]any{"NOTION_API_KEY": "NEVER-NOTION-KEY"}},
		},
		"projects": map[string]any{local: map[string]any{"mcpServers": map[string]any{
			"proj": map[string]any{"command": "node", "args": []any{local + "/tools/mcp.js"}},
		}}},
	})
	out, err := runSSH(ctx, f.target, markerScript(), nil)
	if err != nil {
		t.Fatal(err)
	}
	mc, notes := buildMCPCarry(home, f.local, testSlug, "", nil)
	if len(notes) > 0 {
		t.Fatalf("notes = %v", notes)
	}
	if _, o, err := syncCredentialsAndCarry(ctx, f.target, home, f.local, credSyncOptions{}, carryOptions{MCP: mc, Markers: parseMarkers(string(out))}); err != nil || len(o.Failed) > 0 {
		t.Fatalf("carry: %v %v", err, o)
	}

	p := mcpreg.Paths{Home: f.guestHome, Platform: filepath.Join(t.TempDir(), "none.json"), SecretsDir: secrets, SocketDir: t.TempDir(), Etc: t.TempDir()}
	reg := mcpreg.Load(p)
	if len(reg.Problems) > 0 {
		t.Fatalf("mcpreg.Load of the carry's laptop.json: %v", reg.Problems)
	}
	if got := slices.Sorted(maps.Keys(reg.Laptop.User)); !reflect.DeepEqual(got, []string{"gh", "linear", "notion"}) {
		t.Fatalf("registry user servers = %v", got)
	}
	guestCo, _ := filepath.EvalSymlinks(filepath.Join(f.guestHome, testSlug))
	if reg.Laptop.Projects[guestCo]["proj"] == nil {
		t.Fatalf("registry projects = %v", reg.Laptop.Projects)
	}
	if len(reg.Laptop.Secrets) == 0 {
		t.Fatal("registry has no secrets from the carry")
	}

	// Render, as sync does for each agent.
	cl := reg.Render("claude")
	if h := cl.User["linear"].(map[string]any)["headers"].(map[string]any)["Authorization"]; h != "Bearer ${LINEAR_TOKEN}" {
		t.Errorf("claude linear Authorization = %v", h)
	}
	if cl.Projects[guestCo]["proj"] == nil {
		t.Errorf("claude projects = %v", cl.Projects)
	}
	cx := reg.Render("codex")
	if v := cx.User["linear"].(map[string]any)["bearer_token_env_var"]; v != "LINEAR_TOKEN" {
		t.Errorf("codex linear = %v", cx.User["linear"])
	}
	// A reference in args, and Codex's fixed environment: the launcher.
	if gh := cx.User["gh"].(map[string]any); gh["command"] != mcpreg.Launcher || !reflect.DeepEqual(gh["args"], []any{"run", "gh"}) {
		t.Errorf("codex gh = %v", gh)
	}
	if cx.User["proj"] == nil {
		t.Errorf("codex lacks the checkout's server: %v", cx.User)
	}

	// The whole sync into the guest home: every agent file parses, holds
	// the references, and no laptop credential.
	var stderr bytes.Buffer
	mcpreg.Sync(p, mcpreg.Agents, &stderr)
	if stderr.Len() > 0 {
		t.Errorf("sync stderr: %s", stderr.String())
	}
	files := map[string]string{
		"claude":   ".claude.json",
		"codex":    ".codex/config.toml",
		"gemini":   ".gemini/extensions/repose-mcp/gemini-extension.json",
		"opencode": ".config/opencode/config.json",
		"pi":       ".repose/mcp/agents/pi.json",
	}
	for agent, rel := range files {
		b, err := os.ReadFile(filepath.Join(f.guestHome, rel))
		if err != nil {
			t.Errorf("%s: %v", agent, err)
			continue
		}
		if bytes.Contains(b, []byte("NEVER-")) {
			t.Errorf("%s: a laptop credential reached %s:\n%s", agent, rel, b)
		}
		if !bytes.Contains(b, []byte("linear")) {
			t.Errorf("%s: %s lacks linear:\n%s", agent, rel, b)
		}
		var v any
		if strings.HasSuffix(rel, ".toml") {
			_, err = toml.Decode(string(b), &v)
		} else {
			err = json.Unmarshal(b, &v)
		}
		if err != nil {
			t.Errorf("%s: %s does not parse: %v", agent, rel, err)
		}
	}
}
