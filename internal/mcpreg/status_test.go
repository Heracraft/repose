package mcpreg

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	_ = os.Unsetenv("LINEAR_TOKEN_T")

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
	check("playwright/repose", []string{"claude", "gemini", "opencode", "pi"}, "")
	check("linear/laptop", []string{"claude", "codex", "gemini", "opencode", "pi"}, "needs LINEAR_TOKEN_T")
	check("sentry/laptop", []string{"claude", "gemini", "opencode", "pi"}, "codex: an SSE server, which Codex does not take")
	check("gone/laptop", []string{"claude", "codex", "gemini", "opencode", "pi"}, "no-such-command-repose missing")
	check("xcode/laptop", []string{}, "an Apple app")
	check("apple-notes/forward", []string{"claude", "codex", "gemini", "opencode", "pi"}, "laptop not connected")
	check("mine/machine", []string{"claude"}, "")
	check("notes-db/project", []string{"claude"}, "")
	if got["notes-db/project"].Checkout != co {
		t.Errorf("checkout = %q", got["notes-db/project"].Checkout)
	}
	if got["linear/laptop"].Needs[0] != "LINEAR_TOKEN_T" {
		t.Errorf("needs = %v", got["linear/laptop"].Needs)
	}
	// A forward the hold holds right now has nothing to do; a carry skip
	// of a forwarded name gives way to the forward's row; a forward no
	// agent was synced for yet still has a row.
	writeFile(t, filepath.Join(p.SocketDir, "apple-notes.sock"), "")
	writeFile(t, filepath.Join(home, ".repose/mcp/forward/xcode.json"), `{"version":1,"name":"xcode"}`)
	st, _ = ReadStatus(p)
	got = map[string]ServerStatus{}
	for _, s := range st.Servers {
		got[s.Name+"/"+s.From] = s
	}
	check("apple-notes/forward", []string{"claude", "codex", "gemini", "opencode", "pi"}, "")
	check("xcode/forward", []string{}, "laptop not connected")
	if _, ok := got["xcode/laptop"]; ok {
		t.Errorf("xcode kept its carry row beside the forward: %+v", got["xcode/laptop"])
	}
	_ = os.Remove(filepath.Join(home, ".repose/mcp/forward/xcode.json"))
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
	  "absent":{"command":"no-such-command-repose"},
	  "hidden":{"command":"${PROBE_TOKEN}/bin/mcp"},
	  "override":{"command":"sh","env":{"REPOSE_DUP_T":"server"}},
	  "linear":{"command":"sh","args":["--token","${PROBE_LINEAR_T}"],"env":{"R":"${PROBE_REGION:-eu}"}},
	  "two":{"command":"sh","env":{"A":"${PROBE_B_T}","B":"${PROBE_A_T}","C":"${PROBE_TOKEN}"}}}}`)
	t.Setenv("REPOSE_DUP_T", "agent")
	_ = os.Unsetenv("PROBE_LINEAR_T")
	_ = os.Unsetenv("PROBE_A_T")
	_ = os.Unsetenv("PROBE_B_T")
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
	// A command missing after expansion is named as written, never with
	// the secret's value in it.
	_, _, _, err = Prepare(p, "hidden")
	if err == nil || err.Error() != "repose-mcp: hidden needs ${PROBE_TOKEN}/bin/mcp, which the machine lacks" {
		t.Errorf("hidden: %v", err)
	}
	st, serr := ReadStatus(p)
	if serr != nil {
		t.Fatal(serr)
	}
	for _, s := range st.Servers {
		if s.Name == "hidden" && (!reflect.DeepEqual(s.Missing, []string{"${PROBE_TOKEN}/bin/mcp"}) || strings.Contains(s.State, "s3cret")) {
			t.Errorf("hidden status %+v", s)
		}
	}
	// A secret the machine lacks stops the start: the server would send
	// the literal ${NAME} to its service as a token. ${X:-d} is not a need.
	_, _, _, err = Prepare(p, "linear")
	if le, ok := err.(*LaunchError); !ok || le.Code != 1 || le.Msg != "repose-mcp: linear needs the secret PROBE_LINEAR_T; set it with `repose secrets set PROBE_LINEAR_T` on your laptop, then restart the agent." {
		t.Errorf("linear without its secret: %v", err)
	}
	_, _, _, err = Prepare(p, "two")
	if err == nil || err.Error() != "repose-mcp: two needs the secrets PROBE_A_T, PROBE_B_T; set each with `repose secrets set NAME` on your laptop, then restart the agent." {
		t.Errorf("two without their secrets: %v", err)
	}
	// Set in the environment (an agent started with it) is enough, and an
	// empty secret file is not.
	t.Setenv("PROBE_LINEAR_T", "tok")
	if _, argv, _, err := Prepare(p, "linear"); err != nil || !reflect.DeepEqual(argv, []string{"sh", "--token", "tok"}) {
		t.Errorf("linear from the environment: %v %v", argv, err)
	}
	writeFile(t, filepath.Join(dir, "PROBE_A_T"), "")
	writeFile(t, filepath.Join(dir, "PROBE_B_T"), "b")
	if _, _, _, err := Prepare(p, "two"); err == nil || !strings.Contains(err.Error(), "needs the secret PROBE_A_T;") {
		t.Errorf("two with an empty secret: %v", err)
	}
	// The server's env replaces an inherited name: one copy, the server's.
	_, _, env, err = Prepare(p, "override")
	if err != nil {
		t.Fatal(err)
	}
	var dup []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "REPOSE_DUP_T=") {
			dup = append(dup, kv)
		}
	}
	if !reflect.DeepEqual(dup, []string{"REPOSE_DUP_T=server"}) {
		t.Errorf("REPOSE_DUP_T copies %v", dup)
	}
}

// TestPrepareIn: run NAME CHECKOUT starts that checkout's server, so two
// checkouts' Claude Code entries of one name each start their own.
func TestPrepareIn(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	p := Paths{Home: home, Platform: filepath.Join(dir, "none.json"), SecretsDir: dir}
	writeFile(t, filepath.Join(dir, "DB_T"), "s3cret")
	writeFile(t, filepath.Join(home, ".repose/mcp/laptop.json"), `{"version":1,
	  "user":{"top":{"command":"sh","args":["user"]}},
	  "projects":{"/home/dev/app":{"db":{"command":"sh","args":["app","${DB_T}"]},"top":{"command":"sh","args":["app"]}},
	              "/home/dev/lib":{"db":{"command":"sh","args":["lib","${DB_T}"]}}}}`)
	for _, c := range []struct {
		name, checkout string
		argv           []string
	}{
		{"db", "", []string{"sh", "app", "s3cret"}},
		{"db", "/home/dev/app", []string{"sh", "app", "s3cret"}},
		{"db", "/home/dev/lib", []string{"sh", "lib", "s3cret"}},
		{"db", "/home/dev/none", []string{"sh", "app", "s3cret"}},
		{"top", "", []string{"sh", "user"}},
		{"top", "/home/dev/app", []string{"sh", "app"}},
		{"top", "/home/dev/lib", []string{"sh", "user"}},
	} {
		_, argv, _, err := PrepareIn(p, c.name, c.checkout)
		if err != nil || !reflect.DeepEqual(argv, c.argv) {
			t.Errorf("run %s %s: %v %v, want %v", c.name, c.checkout, argv, err, c.argv)
		}
	}
}

// TestRemoteNeedsSecret: a carried remote server whose secret is missing
// reaches no agent, and status says what it needs once; set, it reaches
// them at the next sync.
func TestRemoteNeedsSecret(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	p := Paths{Home: home, Platform: filepath.Join(root, "none.json"), SecretsDir: filepath.Join(root, "secrets"), SocketDir: filepath.Join(root, "sock"), Etc: root}
	writeFile(t, filepath.Join(root, "etc/repose/pi-extension.js"), `pi.registerMcpServer`)
	writeFile(t, filepath.Join(home, ".repose/mcp/laptop.json"), `{"version":1,"user":{
	  "notion":{"type":"http","url":"https://mcp.notion.com/mcp","headers":{"Authorization":"Bearer ${NOTION_T}"}}}}`)
	_ = os.Unsetenv("NOTION_T")
	var stderr bytes.Buffer
	Sync(p, Agents, &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("sync: %s", stderr.String())
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".claude.json")); strings.Contains(string(b), "notion") {
		t.Errorf("claude got notion without its secret:\n%s", b)
	}
	st, _ := ReadStatus(p)
	if len(st.Servers) != 1 || st.Servers[0].State != "needs NOTION_T" || len(st.Servers[0].Agents) != 0 {
		t.Errorf("status %+v", st.Servers)
	}
	writeFile(t, filepath.Join(p.SecretsDir, "NOTION_T"), "x")
	Sync(p, Agents, &stderr)
	st, _ = ReadStatus(p)
	if len(st.Servers) != 1 || st.Servers[0].State != "" || !reflect.DeepEqual(st.Servers[0].Agents, Agents) {
		t.Errorf("status after the secret %+v", st.Servers)
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

// A laptop.json or forward file that does not parse, or that a newer CLI
// wrote, costs only its own servers: the platform servers still reach
// every agent, sync says what it left out, and status still answers.
func TestBrokenSourceCostsOnlyItsOwn(t *testing.T) {
	for _, c := range []struct{ laptop, forward, rendered string }{
		{`{"version":1,"user":[]}`, `{"version":1,"name":"notes","tools":"x"}`, ""},
		{`{"version":1,"user":{"x":"a string"}}`, `{"version":2,"name":"notes"}`, `{"version":2,"agents":{"claude":{"user":{"playwright":{"command":"old"}}}}}`},
		{`{"version":2,"user":{"linear":{"command":"sh"}}}`, `not json`, ""},
	} {
		home, root := t.TempDir(), t.TempDir()
		p := Paths{Home: home, Platform: filepath.Join(root, "etc/repose/mcp.json"), SecretsDir: filepath.Join(root, "secrets"), SocketDir: filepath.Join(root, "sock"), Etc: root}
		writeFile(t, p.Platform, `{"mcpServers":{"playwright":{"type":"stdio","command":"playwright-mcp","args":[]}}}`)
		writeFile(t, filepath.Join(home, ".repose/mcp/laptop.json"), c.laptop)
		writeFile(t, filepath.Join(home, ".repose/mcp/forward/notes.json"), c.forward)
		writeFile(t, filepath.Join(home, ".repose/mcp/forward/good.json"), `{"version":1,"name":"good","tools":[]}`)
		if c.rendered != "" {
			writeFile(t, filepath.Join(home, ".repose/mcp/rendered.json"), c.rendered)
		}
		var stderr bytes.Buffer
		Sync(p, Agents, &stderr)
		b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
		if err != nil {
			t.Fatalf("%s: no ~/.claude.json: %v (%s)", c.laptop, err, stderr.String())
		}
		var cj struct {
			MCPServers map[string]any `json:"mcpServers"`
		}
		_ = json.Unmarshal(b, &cj)
		if cj.MCPServers["playwright"] == nil || cj.MCPServers["good"] == nil || cj.MCPServers["notes"] != nil || cj.MCPServers["linear"] != nil {
			t.Errorf("%s: claude has %v", c.laptop, cj.MCPServers)
		}
		codex, _ := os.ReadFile(filepath.Join(home, ".codex/config.toml"))
		if !strings.Contains(string(codex), "[mcp_servers.playwright]") {
			t.Errorf("%s: codex lacks playwright:\n%s", c.laptop, codex)
		}
		if !strings.Contains(stderr.String(), "laptop.json") || !strings.Contains(stderr.String(), "forward/notes.json") || strings.Count(stderr.String(), "\n") > 3 {
			t.Errorf("%s: stderr %q", c.laptop, stderr.String())
		}
		st, err := ReadStatus(p)
		if err != nil || len(st.Problems) < 2 {
			t.Errorf("%s: status %v %+v", c.laptop, err, st)
		}
	}
}
