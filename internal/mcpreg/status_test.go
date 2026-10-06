package mcpreg

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStatus(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	co := filepath.Join(home, "app")
	p := Paths{Home: home, Platform: filepath.Join(root, "etc/repose/mcp.json"), SecretsDir: filepath.Join(root, "secrets"), SocketDir: filepath.Join(root, "sock"), Etc: root}
	writeFile(t, p.Platform, `{"mcpServers":{"playwright":{"type":"stdio","command":"playwright-mcp","args":[]}}}`)
	// Unit A's system layers.
	writeFile(t, filepath.Join(root, "etc/gemini-cli/system-defaults.json"), `{"mcpServers":{"playwright":{"command":"playwright-mcp","args":[]}}}`)
	writeFile(t, filepath.Join(root, "etc/opencode/opencode.json"), `{"mcp":{"playwright":{"type":"local","command":["playwright-mcp"]}}}`)
	writeFile(t, filepath.Join(root, "etc/repose/pi-extension.js"), `pi.registerMcpServer`)
	writeFile(t, filepath.Join(home, ".repose/checkout"), "app\n")
	writeFile(t, filepath.Join(co, ".mcp.json"), `{"mcpServers":{"notes-db":{"command":"sqlite-mcp"}}}`)
	writeFile(t, filepath.Join(home, ".repose/mcp/laptop.json"), `{"version":1,
	  "user":{"linear":{"type":"stdio","command":"sh","args":[],"env":{"LINEAR_TOKEN":"${LINEAR_TOKEN_T}"}},
	          "sentry":{"type":"sse","url":"https://sentry.example/sse"},
	          "gone":{"command":"no-such-command-repose"}},
	  "skipped":[{"name":"xcode","reason":"an Apple app"}]}`)
	writeFile(t, filepath.Join(home, ".repose/mcp/forward/apple-notes.json"), `{"version":1,"name":"apple-notes"}`)
	os.Unsetenv("LINEAR_TOKEN_T")

	var stderr bytes.Buffer
	Sync(p, Agents, &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("sync: %s", stderr.String())
	}
	// The user's own: a server added on the machine, and Codex's playwright
	// turned off.
	b, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	var cj map[string]any
	if err := json.Unmarshal(b, &cj); err != nil {
		t.Fatal(err)
	}
	cj["mcpServers"].(map[string]any)["mine"] = map[string]any{"command": "my-mcp"}
	nb, _ := json.Marshal(cj)
	writeFile(t, filepath.Join(home, ".claude.json"), string(nb))
	codex, _ := os.ReadFile(filepath.Join(home, ".codex/config.toml"))
	out, ok := replaceTable(string(codex), "playwright", "[mcp_servers.playwright]\ncommand = \"playwright-mcp\"\nargs = []\nenabled = false\n")
	if !ok {
		t.Fatal("no playwright table")
	}
	writeFile(t, filepath.Join(home, ".codex/config.toml"), out)

	st, err := ReadStatus(p)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ServerStatus{}
	for _, s := range st.Servers {
		got[s.Name+"/"+s.From] = s
	}
	j, _ := json.MarshalIndent(st, "", "  ")
	t.Logf("%s", j)
	check := func(key string, agents []string, state string) {
		t.Helper()
		s, ok := got[key]
		if !ok {
			t.Errorf("no row %s", key)
			return
		}
		if !reflect.DeepEqual(s.Agents, agents) {
			t.Errorf("%s agents = %v, want %v", key, s.Agents, agents)
		}
		if s.State != state {
			t.Errorf("%s state = %q, want %q", key, s.State, state)
		}
	}
	check("playwright/machine", []string{"claude", "gemini", "opencode", "pi"}, "")
	check("linear/laptop", []string{"claude", "codex", "gemini", "opencode", "pi"}, "needs LINEAR_TOKEN_T")
	check("sentry/laptop", []string{"claude", "gemini", "opencode", "pi"}, "codex: an SSE server, which Codex does not take")
	check("gone/laptop", []string{"claude", "codex", "gemini", "opencode", "pi"}, "no-such-command-repose missing")
	check("xcode/laptop", []string{}, "an Apple app")
	check("apple-notes/forward", []string{"claude", "codex", "gemini", "opencode", "pi"}, "laptop not connected")
	check("mine/yours", []string{"claude"}, "")
	check("notes-db/project", []string{"claude"}, "")
	if got["notes-db/project"].Checkout != co {
		t.Errorf("checkout = %q", got["notes-db/project"].Checkout)
	}
	if got["linear/laptop"].Needs[0] != "LINEAR_TOKEN_T" {
		t.Errorf("needs = %v", got["linear/laptop"].Needs)
	}
	// Once the secret is set, linear needs nothing.
	writeFile(t, filepath.Join(p.SecretsDir, "LINEAR_TOKEN_T"), "x")
	st, _ = ReadStatus(p)
	for _, s := range st.Servers {
		if s.Name == "linear" && (s.State != "" || len(s.Needs) != 0) {
			t.Errorf("linear after the secret: %+v", s)
		}
	}
}

func TestPrepare(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	p := Paths{Home: home, Platform: filepath.Join(dir, "none.json"), SecretsDir: dir}
	writeFile(t, filepath.Join(dir, "PROBE_TOKEN"), "s3cret")
	writeFile(t, filepath.Join(home, ".repose/mcp/laptop.json"), `{"version":1,"user":{
	  "probe":{"command":"sh","args":["-c","${PROBE_TOKEN}"],"env":{"T":"${PROBE_TOKEN}","R":"${PROBE_REGION:-eu}"}},
	  "remote":{"type":"http","url":"https://x"},
	  "absent":{"command":"no-such-command-repose"}}}`)
	path, argv, env, err := Prepare(p, "probe")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "sh" || !reflect.DeepEqual(argv, []string{"sh", "-c", "s3cret"}) {
		t.Errorf("path %q argv %v", path, argv)
	}
	tail := env[len(env)-2:]
	if !reflect.DeepEqual(tail, []string{"R=eu", "T=s3cret"}) {
		t.Errorf("env tail %v", tail)
	}
	for name, code := range map[string]int{"missing": 127, "absent": 127, "remote": 1} {
		_, _, _, err := Prepare(p, name)
		le, ok := err.(*LaunchError)
		if !ok || le.Code != code {
			t.Errorf("%s: %v, want exit %d", name, err, code)
		}
	}
	_, _, _, err = Prepare(p, "missing")
	if err.Error() != "repose-mcp: missing is not in ~/.repose/mcp/laptop.json" {
		t.Errorf("message %q", err)
	}
}

func TestReplaceTable(t *testing.T) {
	in := "a = 1\n\n[mcp_servers.x]\ncommand = \"c\"\n\n[mcp_servers.x.env]\nK = \"v\"\n# about y\n[mcp_servers.y]\ncommand = \"d\"\n"
	out, ok := replaceTable(in, "x", "")
	if !ok || out != "a = 1\n# about y\n[mcp_servers.y]\ncommand = \"d\"\n" {
		t.Errorf("remove x: %v %q", ok, out)
	}
	out, ok = replaceTable(in, "y", "[mcp_servers.y]\ncommand = \"e\"\n")
	if !ok || out != "a = 1\n\n[mcp_servers.x]\ncommand = \"c\"\n\n[mcp_servers.x.env]\nK = \"v\"\n# about y\n[mcp_servers.y]\ncommand = \"e\"\n" {
		t.Errorf("replace y: %v %q", ok, out)
	}
	if _, ok := replaceTable("mcp_servers.z.command = \"c\"\n", "z", ""); ok {
		t.Errorf("dotted form edited")
	}
	out, ok = replaceTable("[mcp_servers.\"q\"] # quoted\ncommand = \"c\"\n", "q", "")
	if !ok || out != "" {
		t.Errorf("quoted: %v %q", ok, out)
	}
}
