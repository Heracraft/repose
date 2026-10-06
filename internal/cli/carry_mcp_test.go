package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// The MCP carry (DECISIONS I-556). Every credential in the fixtures holds
// NEVER-, which must not reach the payload, the hash, a line or a guest
// file.

func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("%v: %s", err, s)
	}
	return m
}

// One row per classifier row of the design (3.2) and the critic's
// additions (item 6).
func TestClassifyMCPServer(t *testing.T) {
	const home, repo = "/Users/me", "/Users/me/code/app"
	for _, tc := range []struct {
		desc, name, server string
		skip               string // "" carried
		drop               bool
		want               string // a JSON subset the carried server must hold
		secrets            []string
		cmd                string
	}{
		{desc: "name", name: "bad name!", server: `{"command":"npx","args":["x"]}`, skip: "its name has characters other than letters, digits, - and _"},
		{desc: "type", name: "proxy", server: `{"type":"claudeai-proxy","url":"https://x"}`, skip: "type claudeai-proxy, which repose does not copy"},
		{desc: "platform name", name: "playwright", server: `{"command":"npx","args":["-y","something-else"]}`, drop: true},
		{desc: "platform package", name: "pw", server: `{"command":"npx","args":["-y","@playwright/mcp@latest"]}`, drop: true},
		{desc: "platform command", name: "cdt", server: `{"command":"/opt/homebrew/bin/chrome-devtools-mcp"}`, drop: true},
		{desc: "apple", name: "notes", server: `{"command":"npx","args":["-y","mcp-server-apple-notes"]}`, skip: "an Apple app"},
		{desc: "xcode", name: "xc", server: `{"command":"xcodebuildmcp"}`, skip: "an Apple app"},
		{desc: "xcrun", name: "xcode", server: `{"command":"xcrun","args":["mcpbridge"]}`, skip: "an Apple app"},
		{desc: "localhost", name: "figma", server: `{"type":"http","url":"http://127.0.0.1:3845/mcp"}`, skip: "runs on your laptop"},
		{desc: "lan", name: "nas", server: `{"type":"sse","url":"http://192.168.1.4/sse"}`, skip: "runs on your laptop"},
		{desc: "tailnet ip", name: "tail", server: `{"type":"http","url":"http://100.101.102.103:8080/mcp"}`, skip: "runs on your laptop"},
		{desc: "tailnet name", name: "tail2", server: `{"type":"http","url":"https://box.tail1234.ts.net/mcp"}`, skip: "runs on your laptop"},
		{desc: "headersHelper", name: "hh", server: `{"type":"http","url":"https://api.example.com/mcp","headersHelper":"~/bin/get-token"}`, skip: "gets its headers from a laptop command"},
		{desc: "oauth helper", name: "oh", server: `{"type":"http","url":"https://api.example.com/mcp","oauth":{"clientId":"c","clientSecretHelper":"op read x"}}`, skip: "gets its OAuth secret from a laptop command"},
		{desc: "oauth carried", name: "linear", server: `{"type":"http","url":"https://mcp.linear.app/mcp","oauth":{"clientId":"abc","callbackPort":8080}}`,
			want: `{"type":"http","url":"https://mcp.linear.app/mcp","oauth":{"clientId":"abc","callbackPort":8080}}`},
		{desc: "oauth client secret", name: "acme", server: `{"type":"http","url":"https://acme.example/mcp","oauth":{"clientId":"abc","clientSecret":"NEVER-OAUTH-SECRET"}}`,
			want: `{"oauth":{"clientId":"abc","clientSecret":"${ACME_CLIENT_SECRET}"}}`, secrets: []string{"ACME_CLIENT_SECRET"}},
		{desc: "bearer", name: "sentry", server: `{"type":"http","url":"https://mcp.sentry.dev/mcp","headers":{"Authorization":"Bearer NEVER-BEARER"}}`,
			want: `{"headers":{"Authorization":"Bearer ${SENTRY_TOKEN}"}}`, secrets: []string{"SENTRY_TOKEN"}},
		{desc: "secret header name", name: "notion", server: `{"type":"http","url":"https://n.example/mcp","headers":{"X-Api-Key":"NEVER-HEADER","Accept":"application/json"}}`,
			want: `{"headers":{"X-Api-Key":"${NOTION_X_API_KEY}","Accept":"application/json"}}`, secrets: []string{"NOTION_X_API_KEY"}},
		{desc: "long header value", name: "z", server: `{"type":"http","url":"https://z.example/mcp","headers":{"X-Thing":"NEVERabcdefghijklmnopqrstu"}}`,
			want: `{"headers":{"X-Thing":"${Z_X_THING}"}}`, secrets: []string{"Z_X_THING"}},
		{desc: "url user info and query", name: "q", server: `{"type":"http","url":"https://u:NEVER-PASS@q.example/mcp?api_key=NEVER-QUERY&x=1"}`,
			want: `{"url":"https://u:${Q_PASSWORD}@q.example/mcp?api_key=${Q_API_KEY}&x=1"}`, secrets: []string{"Q_API_KEY", "Q_PASSWORD"}},
		{desc: "nvm npx", name: "everything", server: `{"command":"/Users/me/.nvm/versions/node/v22.1.0/bin/npx","args":["-y","@modelcontextprotocol/server-everything"]}`,
			want: `{"command":"npx","args":["-y","@modelcontextprotocol/server-everything"]}`},
		{desc: "windows npx", name: "win", server: `{"command":"C:\\Program Files\\nodejs\\npx.cmd","args":["-y","x-mcp"]}`, want: `{"command":"npx"}`},
		{desc: "laptop program", name: "mine", server: `{"command":"/Users/me/bin/mytool"}`, skip: "a program on your laptop"},
		{desc: "repo path", name: "local", server: `{"command":"node","args":["/Users/me/code/app/scripts/mcp.js","--root=/Users/me/code/app"]}`,
			want: `{"command":"node","args":["@@REPOSE_CHECKOUT@@/scripts/mcp.js","--root=@@REPOSE_CHECKOUT@@"]}`},
		{desc: "repo command", name: "local2", server: `{"command":"/Users/me/code/app/bin/server"}`, want: `{"command":"@@REPOSE_CHECKOUT@@/bin/server"}`},
		{desc: "docker -v laptop", name: "dock", server: `{"command":"docker","args":["run","-i","-v","/Users/me/data:/data","img"]}`, skip: "files on your laptop"},
		{desc: "home in arg", name: "fs", server: `{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","${HOME}/Documents"]}`, skip: "files on your laptop"},
		{desc: "env laptop path", name: "gcp", server: `{"command":"npx","args":["gcp-mcp"],"env":{"CREDS_FILE":"/Users/me/gcp.json"}}`, skip: "reads CREDS_FILE from your laptop"},
		{desc: "flag=value", name: "tok", server: `{"command":"npx","args":["-y","tok-mcp","--token=NEVER-FLAG-EQ"]}`,
			want: `{"args":["-y","tok-mcp","--token=${TOK_TOKEN}"]}`, secrets: []string{"TOK_TOKEN"}},
		{desc: "flag value", name: "k2", server: `{"command":"npx","args":["-y","k2-mcp","--api-key","NEVER-FLAG-SPACE"]}`,
			want: `{"args":["-y","k2-mcp","--api-key","${K2_API_KEY}"]}`, secrets: []string{"K2_API_KEY"}},
		{desc: "secretIn arg", name: "gh", server: `{"command":"npx","args":["-y","gh-mcp","ghp_NEVERsecret0123456789"]}`,
			want: `{"args":["-y","gh-mcp","${GH_TOKEN}"]}`, secrets: []string{"GH_TOKEN"}},
		{desc: "env secret own name", name: "lin", server: `{"command":"npx","args":["-y","linear-mcp"],"env":{"LINEAR_API_KEY":"NEVER-ENV-LINEAR","DEBUG":"1"}}`,
			want: `{"env":{"LINEAR_API_KEY":"${LINEAR_API_KEY}","DEBUG":"1"}}`, secrets: []string{"LINEAR_API_KEY"}},
		{desc: "env agent login name", name: "ai", server: `{"command":"npx","args":["-y","ai-mcp"],"env":{"ANTHROPIC_API_KEY":"NEVER-ENV-ANTHROPIC","GITHUB_TOKEN":"NEVER-ENV-GH"}}`,
			want: `{"env":{"ANTHROPIC_API_KEY":"${AI_ANTHROPIC_API_KEY}","GITHUB_TOKEN":"${AI_GITHUB_TOKEN}"}}`, secrets: []string{"AI_ANTHROPIC_API_KEY", "AI_GITHUB_TOKEN"}},
		{desc: "env lower-case key", name: "1pw", server: `{"command":"npx","args":["-y","x"],"env":{"api-token":"NEVER-ENV-LOWER"}}`,
			want: `{"env":{"api-token":"${MCP_1PW_API_TOKEN}"}}`, secrets: []string{"MCP_1PW_API_TOKEN"}},
		{desc: "env url credential", name: "db", server: `{"command":"npx","args":["-y","pg-mcp"],"env":{"DATABASE_URL":"postgres://u:NEVERp@db.example.com/x"}}`,
			want: `{"env":{"DATABASE_URL":"${DATABASE_URL}"}}`, secrets: []string{"DATABASE_URL"}},
		{desc: "existing refs kept", name: "refs", server: `{"command":"npx","args":["-y","r","${A:-b}"],"env":{"T":"${LINEAR}","U":"${USER}"}}`,
			want: `{"args":["-y","r","${A:-b}"],"env":{"T":"${LINEAR}","U":"${USER}"}}`},
		{desc: "missing command", name: "foo", server: `{"command":"fooctl","args":["serve"]}`, want: `{"command":"fooctl"}`, cmd: "fooctl"},
		{desc: "header flag", name: "mr", server: `{"command":"npx","args":["-y","mcp-remote","https://api.example.com/mcp","--header","X-API-Key: NEVER3f9a8b7c6d5e4f3a2b"]}`,
			want: `{"args":["-y","mcp-remote","https://api.example.com/mcp","--header","X-API-Key: ${MR_X_API_KEY}"]}`, secrets: []string{"MR_X_API_KEY"}},
		{desc: "header flag=value", name: "mr2", server: `{"command":"npx","args":["mcp-remote","https://a.example/mcp","--header=Authorization: Bearer NEVERtok"]}`,
			want: `{"args":["mcp-remote","https://a.example/mcp","--header=Authorization: Bearer ${MR2_TOKEN}"]}`, secrets: []string{"MR2_TOKEN"}},
		{desc: "token in url path", name: "zap", server: `{"type":"http","url":"https://mcp.zapier.com/api/mcp/s/NEVERZjM4NjQ0MzYtZDQ2Ny00/mcp"}`,
			want: `{"url":"https://mcp.zapier.com/api/mcp/s/${ZAP_TOKEN}/mcp"}`, secrets: []string{"ZAP_TOKEN"}},
		{desc: "token in arg url path", name: "zap2", server: `{"command":"npx","args":["mcp-remote","https://mcp.zapier.com/api/mcp/s/NEVERZjM4NjQ0MzYtZDQ2Ny00/sse"]}`,
			want: `{"args":["mcp-remote","https://mcp.zapier.com/api/mcp/s/${ZAP2_TOKEN}/sse"]}`, secrets: []string{"ZAP2_TOKEN"}},
		{desc: "positional provider key", name: "oai", server: `{"command":"npx","args":["-y","@x/server","sk-proj-NEVERabcdefghij0123"]}`,
			want: `{"args":["-y","@x/server","${OAI_TOKEN}"]}`, secrets: []string{"OAI_TOKEN"}},
		{desc: "positional hex key", name: "hex", server: `{"command":"npx","args":["-y","hex-mcp","NEVER3f9a8b7c6d5e4f3a2b1c0d"]}`,
			want: `{"args":["-y","hex-mcp","${HEX_TOKEN}"]}`, secrets: []string{"HEX_TOKEN"}},
		{desc: "short header value", name: "h", server: `{"type":"http","url":"https://h.example/mcp","headers":{"X-Api":"NEVERabc1","Content-Type":"application/json"}}`,
			want: `{"headers":{"X-Api":"${H_X_API}","Content-Type":"application/json"}}`, secrets: []string{"H_X_API"}},
		{desc: "plain flags", name: "fp", server: `{"command":"npx","args":["-y","@modelcontextprotocol/server-everything","--auth-type","oauth","--session-name","foo","--private-mode","true","--keyboard","us","--transport=stdio"]}`,
			want: `{"args":["-y","@modelcontextprotocol/server-everything","--auth-type","oauth","--session-name","foo","--private-mode","true","--keyboard","us","--transport=stdio"]}`},
		{desc: "docker -e NAME=VALUE", name: "pg", server: `{"command":"docker","args":["run","-i","--rm","-e","POSTGRES_PASSWORD=NEVERsupersecret","-e","DEBUG=1","mcp/postgres"]}`,
			want: `{"args":["run","-i","--rm","-e","POSTGRES_PASSWORD=${POSTGRES_PASSWORD}","-e","DEBUG=1","mcp/postgres"]}`, secrets: []string{"POSTGRES_PASSWORD"}},
		{desc: "--env=NAME=VALUE", name: "envflag", server: `{"command":"docker","args":["run","--env=API_KEY=NEVERabc123","x"]}`,
			want: `{"args":["run","--env=API_KEY=${API_KEY}","x"]}`, secrets: []string{"API_KEY"}},
		{desc: "jwt positional", name: "jw", server: `{"command":"npx","args":["-y","srv","eyJNEVERhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcDEF123"]}`,
			want: `{"args":["-y","srv","${JW_TOKEN}"]}`, secrets: []string{"JW_TOKEN"}},
		{desc: "jwt in url path", name: "jw2", server: `{"type":"http","url":"https://h.example.com/mcp/eyJNEVERhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcDEF123"}`,
			want: `{"url":"https://h.example.com/mcp/${JW2_TOKEN}"}`, secrets: []string{"JW2_TOKEN"}},
		{desc: "jwt flag", name: "jw3", server: `{"command":"npx","args":["srv","--jwt=NEVERshort"]}`,
			want: `{"args":["srv","--jwt=${JW3_JWT}"]}`, secrets: []string{"JW3_JWT"}},
		{desc: "windows cmd /c", name: "linear", server: `{"command":"cmd","args":["/c","npx","-y","@linear/mcp"]}`,
			want: `{"command":"npx","args":["-y","@linear/mcp"]}`},
		{desc: "absolute system command", name: "sh", server: `{"command":"/bin/bash","args":["-c","npx -y some-mcp"]}`,
			want: `{"command":"bash","args":["-c","npx -y some-mcp"]}`},
		{desc: "absolute command the machine may lack", name: "rb", server: `{"command":"/usr/bin/ruby","args":["server.rb"]}`,
			want: `{"command":"ruby"}`, cmd: "ruby"},
		{desc: "env key with a newline", name: "nl", server: `{"command":"npx","args":["x"],"env":{"A\n#mcpold":"/Users/me/x"}}`, skip: "reads A#mcpold from your laptop"},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			v, values := classifyMCPServer(home, repo, tc.name, mustJSON(t, tc.server))
			if v.drop != tc.drop || v.skip != tc.skip {
				t.Fatalf("verdict drop=%v skip=%q, want drop=%v skip=%q", v.drop, v.skip, tc.drop, tc.skip)
			}
			if tc.skip != "" || tc.drop {
				return
			}
			b, _ := json.Marshal(v.server)
			if bytes.Contains(b, []byte("NEVER")) {
				t.Fatalf("a credential is in the carried server: %s", b)
			}
			if tc.want != "" && !jsonSubset(mustJSON(t, tc.want), v.server) {
				t.Fatalf("server = %s\nwant it to hold %s", b, tc.want)
			}
			var got []string
			for n := range values {
				got = append(got, n)
				if !strings.Contains(values[n], "NEVER") {
					t.Errorf("secret %s holds a value that is not the laptop's credential", n)
				}
				if !secretNameRe.MatchString(n) {
					t.Errorf("%s is not a valid secret name", n)
				}
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, tc.secrets) && !(len(got) == 0 && len(tc.secrets) == 0) {
				t.Fatalf("secrets = %v, want %v", got, tc.secrets)
			}
			if v.cmd != tc.cmd {
				t.Fatalf("cmd = %q, want %q", v.cmd, tc.cmd)
			}
		})
	}
}

// A Windows laptop writes repository paths with backslashes; git gives
// the root with slashes. Both are the checkout.
func TestClassifyMCPWindowsRepoPath(t *testing.T) {
	v, _ := classifyMCPServer(`C:\Users\me`, "C:/Users/me/code/repo", "local", mustJSON(t, `{"command":"node","args":["C:\\Users\\me\\code\\repo\\server.js"]}`))
	if v.skip != "" || v.drop {
		t.Fatalf("skip=%q drop=%v, want it carried", v.skip, v.drop)
	}
	if b, _ := json.Marshal(v.server["args"]); string(b) != `["@@REPOSE_CHECKOUT@@/server.js"]` {
		t.Fatalf("args = %s", b)
	}
}

// The guest's laptop.jq builds "secrets": `${A:-b}` is not a secret the
// machine needs, `${LINEAR}` is, and ambient names never are; local
// scope goes under the checkout's real path with the placeholder filled.
func TestMCPLaptopJQSecrets(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	dir := t.TempDir()
	in := `{"user":{"a":{"command":"x","args":["${A:-b}","${LINEAR}","@@REPOSE_CHECKOUT@@/f"],"env":{"H":"${HOME}","X":"${XDG_CONFIG_HOME}","P":"${PATH}"}}},` +
		`"project":{"p":{"url":"https://h/${PROJ_TOKEN}"}},"skipped":[]}`
	for n, b := range map[string]string{"in.json": in, "old.json": `{"projects":{"/other":{"o":{"url":"https://o"}}}}`, "laptop.jq": string(mcpLaptopJQ)} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command("jq", "-S", "-n", "--slurpfile", "n", filepath.Join(dir, "in.json"), "--slurpfile", "o", filepath.Join(dir, "old.json"), "--arg", "co", "/home/dev/co", "-f", filepath.Join(dir, "laptop.jq")).Output()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		User     map[string]map[string]any `json:"user"`
		Projects map[string]map[string]any `json:"projects"`
		Secrets  map[string][]string       `json:"secrets"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if want := map[string][]string{"LINEAR": {"a"}, "PROJ_TOKEN": {"p"}}; !reflect.DeepEqual(got.Secrets, want) {
		t.Errorf("secrets = %v, want %v", got.Secrets, want)
	}
	if got.Projects["/home/dev/co"]["p"] == nil || got.Projects["/other"]["o"] == nil {
		t.Errorf("projects = %v", got.Projects)
	}
	if args := got.User["a"]["args"].([]any); args[2] != "/home/dev/co/f" {
		t.Errorf("args = %v", args)
	}
}

// Two long names that meet after the cut to 64 characters still get
// two secrets, and the carry finishes.
func TestMCPAssignLongNames(t *testing.T) {
	c := newMCPClassifier(t.TempDir(), nil)
	done := make(chan []string, 1)
	go func() {
		var got []string
		for i, n := range []string{strings.Repeat("a", 62) + "-x", strings.Repeat("a", 62) + "_x", strings.Repeat("a", 62) + ".x"} {
			got = append(got, c.assign(n, "TOKEN", "", fmt.Sprintf("NEVER-%d", i)))
		}
		done <- got
	}()
	select {
	case got := <-done:
		seen := map[string]bool{}
		for _, n := range got {
			if seen[n] || !secretNameRe.MatchString(n) || len(n) > 64 {
				t.Fatalf("names = %v", got)
			}
			seen[n] = true
		}
	case <-time.After(3 * time.Second):
		t.Fatal("assign loops")
	}
}

// run passes the tools carry's bins and attach may pass others: the hash
// is the same, and only the guest's command list leaves them out.
func TestMCPHashIgnoresToolBins(t *testing.T) {
	home := t.TempDir()
	writeClaudeJSON(t, home, map[string]any{"mcpServers": map[string]any{"tf": map[string]any{"command": "terraform-mcp-server"}}})
	a, _ := buildMCPCarry(home, "", testSlug, "", map[string]bool{"terraform-mcp-server": true})
	b, _ := buildMCPCarry(home, "", testSlug, "", nil)
	if a.HashUser != b.HashUser || a.HashProject != b.HashProject {
		t.Fatal("the tools carry's bins change the MCP hash")
	}
	p := newGuestPayload()
	if _, err := addMCPParts(p, a, carryOptions{}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(p.buf.Bytes(), []byte("tf terraform-mcp-server")) {
		t.Fatal("the guest is asked to check a bin the tools carry installs")
	}
}

func execBash(script, home string) *exec.Cmd {
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	return cmd
}

// jsonSubset reports whether every key of want is in got with the same
// value (objects compared recursively, everything else exactly).
func jsonSubset(want, got map[string]any) bool {
	for k, wv := range want {
		gv, ok := got[k]
		if !ok {
			return false
		}
		wm, wok := wv.(map[string]any)
		gm, gok := gv.(map[string]any)
		if wok && gok {
			if !jsonSubset(wm, gm) {
				return false
			}
			continue
		}
		wb, _ := json.Marshal(wv)
		gb, _ := json.Marshal(gv)
		if !bytes.Equal(wb, gb) {
			return false
		}
	}
	return true
}

// writeClaudeJSON writes the laptop's ~/.claude.json.
func writeClaudeJSON(t *testing.T, home string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// No credential reaches the templated list or the hash; a rotated value
// changes neither; two servers holding different values under one name
// get the second its own prefix.
func TestTemplateMCPSecrets(t *testing.T) {
	home := t.TempDir()
	cfg := func(linear, sentry string) map[string]any {
		return map[string]any{
			"oauthAccount": map[string]any{"emailAddress": "NEVER-OAUTH-ACCOUNT"},
			"mcpServers": map[string]any{
				"linear": map[string]any{"command": "npx", "args": []any{"-y", "linear-mcp"}, "env": map[string]any{"API_TOKEN": linear}},
				"sentry": map[string]any{"command": "npx", "args": []any{"-y", "sentry-mcp"}, "env": map[string]any{"API_TOKEN": sentry}},
				"same":   map[string]any{"command": "npx", "args": []any{"-y", "same-mcp"}, "env": map[string]any{"API_TOKEN": linear}},
			},
		}
	}
	writeClaudeJSON(t, home, cfg("NEVER-LINEAR-1", "NEVER-SENTRY-1"))
	mc, notes := buildMCPCarry(home, "", testSlug, "", nil)
	if mc == nil || len(notes) != 0 {
		t.Fatalf("carry = %+v, notes %v", mc, notes)
	}
	b, _ := json.Marshal(mc)
	if bytes.Contains(b, []byte("NEVER")) || strings.Contains(mc.HashUser+mc.HashProject, "NEVER") {
		t.Fatalf("a credential is in the carry: %s", b)
	}
	if got := mc.User["linear"]["env"].(map[string]any)["API_TOKEN"]; got != "${API_TOKEN}" {
		t.Errorf("linear API_TOKEN = %v", got)
	}
	if got := mc.User["sentry"]["env"].(map[string]any)["API_TOKEN"]; got != "${SENTRY_API_TOKEN}" {
		t.Errorf("sentry API_TOKEN = %v (a clash takes the server prefix)", got)
	}
	if got := mc.User["same"]["env"].(map[string]any)["API_TOKEN"]; got != "${API_TOKEN}" {
		t.Errorf("same API_TOKEN = %v (the same value shares the name)", got)
	}
	if !reflect.DeepEqual(mc.Templated, []string{"API_TOKEN", "SENTRY_API_TOKEN"}) {
		t.Errorf("templated = %v", mc.Templated)
	}
	h := mc.HashUser
	writeClaudeJSON(t, home, cfg("NEVER-LINEAR-2", "NEVER-SENTRY-2"))
	mc2, _ := buildMCPCarry(home, "", testSlug, "", nil)
	if mc2.HashUser != h {
		t.Errorf("a rotated token changed the hash")
	}
	p := newGuestPayload()
	if _, err := addMCPParts(p, mc2, carryOptions{}); err != nil {
		t.Fatal(err)
	}
	_ = p.tw.Close()
	if bytes.Contains(p.buf.Bytes(), []byte("NEVER")) || strings.Contains(p.script.String(), "NEVER") {
		t.Fatal("a credential is in the payload")
	}
	// Only the import, which asks for them, sees the values.
	values := map[string]string{}
	collectMCP(home, "", nil, values)
	if values["API_TOKEN"] != "NEVER-LINEAR-2" || values["SENTRY_API_TOKEN"] != "NEVER-SENTRY-2" || len(values) != 2 {
		t.Errorf("values = %d entries", len(values))
	}
}

// Local scope is keyed on the main worktree's root, from a subdirectory,
// a linked worktree and a symlinked root, and ~/.claude.json moves with
// CLAUDE_CONFIG_DIR.
func TestReadClaudeMCPProjectKey(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, root, "git", "init", "-q", "-b", "main")
	mustRun(t, root, "git", "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(base, "app-wt")
	mustRun(t, root, "git", "worktree", "add", "-q", wt)
	link := filepath.Join(base, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(root)
	home := t.TempDir()
	writeClaudeJSON(t, home, map[string]any{"projects": map[string]any{real: map[string]any{
		"mcpServers":            map[string]any{"proj": map[string]any{"command": "npx", "args": []any{"proj-mcp"}}},
		"enabledMcpjsonServers": []any{"notes-db"},
	}}})
	for desc, dir := range map[string]string{"root": root, "subdirectory": filepath.Join(root, "sub"), "linked worktree": wt, "symlinked root": link} {
		_, project, appr, err := readClaudeMCP(home, gitRepoRoot(dir))
		if err != nil || project["proj"] == nil {
			t.Errorf("%s: project = %v, err %v", desc, project, err)
		}
		if !reflect.DeepEqual(appr.Enabled, []string{"notes-db"}) {
			t.Errorf("%s: approvals = %+v", desc, appr)
		}
	}
	cfgDir := t.TempDir()
	t.Setenv(envClaudeConfigDir, cfgDir)
	writeClaudeJSON(t, cfgDir, map[string]any{"mcpServers": map[string]any{"moved": map[string]any{"command": "npx"}}})
	user, _, _, err := readClaudeMCP(home, "")
	if err != nil || user["moved"] == nil {
		t.Errorf("CLAUDE_CONFIG_DIR: user = %v, err %v", user, err)
	}
}

// A 5 MB ~/.claude.json (long transcripts' project entries) is read and
// classified in under 50 ms.
func TestBuildMCPCarryIsFast(t *testing.T) {
	home := t.TempDir()
	projects := map[string]any{}
	pad := strings.Repeat("x", 1000)
	for i := 0; len(projects) < 5000; i++ {
		projects[fmt.Sprintf("/Users/me/p%d", i)] = map[string]any{"history": []any{pad}, "mcpServers": map[string]any{}}
	}
	servers := map[string]any{}
	for i := 0; i < 20; i++ {
		servers[fmt.Sprintf("s%d", i)] = map[string]any{"command": "npx", "args": []any{"-y", "x"}, "env": map[string]any{"API_KEY": "NEVER-FAST"}}
	}
	writeClaudeJSON(t, home, map[string]any{"projects": projects, "mcpServers": servers})
	if info, _ := os.Stat(filepath.Join(home, ".claude.json")); info.Size() < 5<<20 {
		t.Fatalf("fixture is %d bytes", info.Size())
	}
	best := time.Hour
	for i := 0; i < 3; i++ {
		start := time.Now()
		mc, _ := buildMCPCarry(home, "", testSlug, "", nil)
		if d := time.Since(start); d < best {
			best = d
		}
		if mc == nil || len(mc.User) != 20 {
			t.Fatalf("carry = %+v", mc)
		}
	}
	t.Logf("buildMCPCarry on a 5 MB ~/.claude.json: %v", best)
	if best > 50*time.Millisecond {
		t.Fatalf("buildMCPCarry took %v, want under 50ms", best)
	}
}

// The carry end to end against a test guest: nothing secret in the
// stream, laptop.json holds ${NAME} and the checkout's real path, the
// lines name what is missing once, an unchanged run sends nothing, a
// removal empties the entry, logins.skip sends the empty set, and a base
// without repose-mcp prints its line once.
func TestCarryMCPServers(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	hideCommand(t, "repose-mcp") // the base line below is an old base's
	home := t.TempDir()
	secrets := t.TempDir()
	if err := os.WriteFile(filepath.Join(secrets, "SET_ALREADY"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REPOSE_SECRETS_DIR", secrets)
	var stream bytes.Buffer
	observePayload = func(script string, tarball []byte) { stream.WriteString(script); stream.Write(tarball) }
	t.Cleanup(func() { observePayload = nil })

	local, _ := filepath.EvalSymlinks(f.local)
	user := map[string]any{
		"linear":      map[string]any{"type": "http", "url": "https://mcp.linear.app/mcp", "headers": map[string]any{"Authorization": "Bearer NEVER-LINEAR-BEARER"}},
		"done":        map[string]any{"command": "npx", "args": []any{"-y", "done-mcp"}, "env": map[string]any{"T": "${SET_ALREADY}"}},
		"foo":         map[string]any{"command": "fooctl-not-here", "args": []any{"serve"}},
		"apple-notes": map[string]any{"command": "npx", "args": []any{"-y", "mcp-apple-notes"}},
		"figma":       map[string]any{"type": "http", "url": "http://127.0.0.1:3845/mcp"},
		"playwright":  map[string]any{"command": "npx", "args": []any{"@playwright/mcp"}},
	}
	project := map[string]any{
		"proj": map[string]any{"command": "node", "args": []any{local + "/tools/mcp.js"}, "env": map[string]any{"PROJ_SECRET": "NEVER-PROJ-SECRET"}},
	}
	write := func() {
		writeClaudeJSON(t, home, map[string]any{
			"oauthAccount": map[string]any{"emailAddress": "NEVER-OAUTH-ACCOUNT"},
			"userID":       "NEVER-USER-ID",
			"mcpServers":   user,
			"projects":     map[string]any{local: map[string]any{"mcpServers": project, "history": []any{"NEVER-HISTORY"}}},
		})
	}
	write()
	carry := func(skip map[string]bool) *carryOutcome {
		t.Helper()
		out, err := runSSH(ctx, f.target, markerScript(), nil)
		if err != nil {
			t.Fatal(err)
		}
		mc, notes := buildMCPCarry(home, f.local, testSlug, "", nil)
		if len(notes) > 0 {
			t.Fatalf("notes = %v", notes)
		}
		_, o, err := syncCredentialsAndCarry(ctx, f.target, home, f.local, credSyncOptions{Skip: skip}, carryOptions{MCP: mc, Markers: parseMarkers(string(out))})
		if err != nil {
			t.Fatal(err)
		}
		if len(o.Failed) > 0 {
			t.Fatalf("failed: %v", o.Failed)
		}
		return o
	}
	readLaptop := func() map[string]any {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(f.guestHome, ".repose/mcp/laptop.json"))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%v: %s", err, b)
		}
		return m
	}

	o := carry(nil)
	lines := strings.Join(o.Lines(), "\n")
	t.Logf("first carry lines:\n%s", lines)
	for _, want := range []string{
		"Left on your laptop: MCP servers apple-notes (an Apple app), figma (runs on your laptop). repose mcp forward apple-notes runs it from here.",
		"MCP servers need secrets the machine lacks: LINEAR_TOKEN (linear), PROJ_SECRET (proj). Set them from your laptop's values with repose secrets import --mcp.",
		"MCP server foo needs fooctl-not-here, which the machine lacks. repose config add PACKAGE adds the package that has it.",
		"This machine's base predates MCP servers from your laptop; they arrive after its next update.",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("lines lack %q", want)
		}
	}
	if strings.Contains(lines, "SET_ALREADY") || strings.Contains(lines, "playwright") {
		t.Errorf("lines name what they should not:\n%s", lines)
	}
	if bytes.Contains(stream.Bytes(), []byte("NEVER-")) {
		t.Fatalf("a NEVER- marker is in the carry stream:\n%s", stream.String())
	}
	lj := readLaptop()
	guestCo, _ := filepath.EvalSymlinks(filepath.Join(f.guestHome, testSlug))
	b, _ := json.Marshal(lj)
	if bytes.Contains(b, []byte("NEVER")) || bytes.Contains(b, []byte("@@REPOSE_CHECKOUT@@")) {
		t.Fatalf("laptop.json: %s", b)
	}
	if lj["version"] != float64(1) {
		t.Errorf("version = %v", lj["version"])
	}
	u := lj["user"].(map[string]any)
	if u["linear"].(map[string]any)["headers"].(map[string]any)["Authorization"] != "Bearer ${LINEAR_TOKEN}" || u["playwright"] != nil || u["figma"] != nil {
		t.Errorf("user = %v", u)
	}
	pr := lj["projects"].(map[string]any)[guestCo].(map[string]any)["proj"].(map[string]any)
	if pr["args"].([]any)[0] != guestCo+"/tools/mcp.js" || pr["env"].(map[string]any)["PROJ_SECRET"] != "${PROJ_SECRET}" {
		t.Errorf("project entry = %v", pr)
	}
	sec := lj["secrets"].(map[string]any)
	if !reflect.DeepEqual(sec["LINEAR_TOKEN"], []any{"linear"}) || !reflect.DeepEqual(sec["SET_ALREADY"], []any{"done"}) {
		t.Errorf("secrets = %v", sec)
	}
	if sk := lj["skipped"].([]any); len(sk) != 2 {
		t.Errorf("skipped = %v", sk)
	}
	if info, err := os.Stat(filepath.Join(f.guestHome, ".repose/mcp/laptop.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("laptop.json mode: %v %v", info, err)
	}
	if fileExists(filepath.Join(f.guestHome, ".claude.json")) {
		t.Error("the carry wrote ~/.claude.json")
	}
	if m, _ := os.ReadFile(filepath.Join(f.guestHome, ".repose/carry/claude-mcp")); !strings.HasSuffix(strings.TrimSpace(string(m)), ":old") {
		t.Errorf("marker on an old base = %q", m)
	}

	// Unchanged: nothing goes, nothing prints, even on the old base.
	stream.Reset()
	if o := carry(nil); len(o.Sent) != 0 || len(o.Lines()) != 0 {
		t.Fatalf("unchanged run: sent %v, lines %v", o.Sent, o.Lines())
	}

	// A base with repose-mcp: the next change prints no base line.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "repose-mcp"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	delete(user, "foo")
	write()
	o = carry(nil)
	if o.MCPOld || len(o.Sent) == 0 {
		t.Errorf("after the base update: %+v", o)
	}
	if u := readLaptop()["user"].(map[string]any); u["foo"] != nil || u["linear"] == nil {
		t.Errorf("a removal on the laptop left %v", u)
	}

	// logins.skip = ["mcp"]: the empty set travels.
	o = carry(map[string]bool{mcpLogin: true})
	if len(o.Sent) == 0 {
		t.Fatal("turning mcp off sent nothing")
	}
	lj = readLaptop()
	if len(lj["user"].(map[string]any)) != 0 || len(lj["projects"].(map[string]any)) != 0 || len(lj["secrets"].(map[string]any)) != 0 {
		t.Errorf("off: laptop.json = %v", lj)
	}
	if bytes.Contains(stream.Bytes(), []byte("NEVER-")) {
		t.Fatal("a NEVER- marker is in the carry stream")
	}
}

// A laptop with no MCP servers on a base without repose-mcp: the part
// still travels, but no line says servers are waiting.
func TestCarryMCPNoServersOldBase(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	hideCommand(t, "repose-mcp")
	home := t.TempDir() // no ~/.claude.json
	t.Setenv("REPOSE_SECRETS_DIR", t.TempDir())
	mc, notes := buildMCPCarry(home, f.local, testSlug, "", nil)
	if mc == nil || len(notes) > 0 {
		t.Fatalf("carry = %+v, notes %v", mc, notes)
	}
	_, o, err := syncCredentialsAndCarry(ctx, f.target, home, f.local, credSyncOptions{}, carryOptions{MCP: mc})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Sent) == 0 {
		t.Fatal("nothing sent")
	}
	if o.MCPOld || len(o.mcpLines()) != 0 {
		t.Fatalf("lines for a laptop with no servers: %v", o.mcpLines())
	}
}

// The attach's helper honours the off switch and files local scope under
// the checkout it was given.
func TestSessionHelperCarriesMCP(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("REPOSE_SECRETS_DIR", t.TempDir())
	writeClaudeJSON(t, home, map[string]any{
		"mcpServers": map[string]any{"s": map[string]any{"command": "npx", "args": []any{"-y", "s"}}},
		"projects":   map[string]any{f.local: map[string]any{"mcpServers": map[string]any{"loc": map[string]any{"command": "npx", "args": []any{"-y", "loc"}}}}},
	})
	// An extra checkout (I-480): local scope goes under it, not under the
	// machine's own checkout.
	other := filepath.Join(f.guestHome, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	otherReal, _ := filepath.EvalSymlinks(other)
	if err := runSession(ctx, sessionOptions{Slug: testSlug, Target: f.target.Args, Carry: true, HomeDir: home, RepoDir: f.local, Checkout: "other"}, func() bool { return false }); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(f.guestHome, ".repose/mcp/laptop.json"))
	if err != nil || !strings.Contains(string(b), `"s"`) {
		t.Fatalf("laptop.json after attach: %s %v", b, err)
	}
	var lj struct {
		Projects map[string]map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(b, &lj); err != nil || len(lj.Projects) != 1 || lj.Projects[otherReal]["loc"] == nil {
		t.Fatalf("local scope not under %s: %v %v", otherReal, lj.Projects, err)
	}
	if err := runSession(ctx, sessionOptions{Slug: testSlug, Target: f.target.Args, Carry: true, HomeDir: home, RepoDir: f.local, MCPOff: true}, func() bool { return false }); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(f.guestHome, ".repose/mcp/laptop.json"))
	if strings.Contains(string(b), `"s"`) {
		t.Fatalf("attach with mcp off kept the server: %s", b)
	}
}

// `repose secrets import --mcp` sets the templated names from the
// laptop's values, prints only names, refuses a FILE, and --dry-run sends
// nothing.
func TestSecretsImportMCP(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	puts := map[string]string{}
	var mu sync.Mutex
	target, _ := url.Parse(fake.URL())
	proxy := httputil.NewSingleHostReverseProxy(target)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/secrets/") {
			body, _ := io.ReadAll(r.Body)
			var v struct{ Value string }
			_ = json.Unmarshal(body, &v)
			raw, _ := base64.StdEncoding.DecodeString(v.Value)
			mu.Lock()
			puts[path.Base(r.URL.Path)] = string(raw)
			mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		proxy.ServeHTTP(w, r)
	}))
	defer srv.Close()
	e.Client = newClient(srv.URL+"/v1", staticToken("tok"))
	var out, errOut strings.Builder
	e.Out, e.ErrOut = &out, &errOut
	ctx := context.Background()
	if _, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "large"}); err != nil {
		t.Fatal(err)
	}
	e.HomeDir = t.TempDir()
	writeClaudeJSON(t, e.HomeDir, map[string]any{"mcpServers": map[string]any{
		"linear": map[string]any{"type": "http", "url": "https://mcp.linear.app/mcp", "headers": map[string]any{"Authorization": "Bearer NEVER-IMPORT-1"}},
		"db":     map[string]any{"command": "npx", "args": []any{"-y", "pg"}, "env": map[string]any{"DATABASE_URL": "postgres://u:NEVER-IMPORT-2@db/x", "X": "${ALREADY}"}},
	}})
	if err := SecretsImportMCPCmd(ctx, e, SecretsImportOptions{ProjectArg: "izma", MCP: true, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(puts) != 0 || !strings.Contains(out.String(), "Would set 2 on izma from your laptop's MCP servers: DATABASE_URL, LINEAR_TOKEN") {
		t.Fatalf("dry run: puts %d, said:\n%s", len(puts), out.String())
	}
	out.Reset()
	if err := SecretsImportMCPCmd(ctx, e, SecretsImportOptions{ProjectArg: "izma", MCP: true}); err != nil {
		t.Fatal(err)
	}
	if puts["LINEAR_TOKEN"] != "NEVER-IMPORT-1" || puts["DATABASE_URL"] != "postgres://u:NEVER-IMPORT-2@db/x" || len(puts) != 2 {
		t.Fatalf("puts = %d entries", len(puts))
	}
	if strings.Contains(out.String()+errOut.String(), "NEVER") || !strings.Contains(out.String(), "Set 2 on izma from your laptop's MCP servers") {
		t.Fatalf("said:\n%s%s", out.String(), errOut.String())
	}
	// Both names exist now: one question, and a no replaces nothing.
	var asked []string
	no := func(p string) (bool, error) { asked = append(asked, p); return false, nil }
	for n := range puts {
		delete(puts, n)
	}
	out.Reset()
	if err := SecretsImportMCPCmd(ctx, e, SecretsImportOptions{ProjectArg: "izma", MCP: true, Confirm: no}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "izma already has DATABASE_URL, LINEAR_TOKEN.") || len(puts) != 0 || out.String() != "Nothing imported.\n" {
		t.Fatalf("declined: asked %q, puts %d, said %q", asked, len(puts), out.String())
	}
	// --yes replaces without asking.
	asked = nil
	if err := SecretsImportMCPCmd(ctx, e, SecretsImportOptions{ProjectArg: "izma", MCP: true, Yes: true, Confirm: no}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 0 || len(puts) != 2 {
		t.Fatalf("--yes: asked %q, puts %d", asked, len(puts))
	}
	// A FILE with --mcp is refused before anything is read.
	cmd := newSecretsImportCmd(func() (*Env, error) { return e, nil }, &globalFlags{})
	cmd.SetArgs([]string{"--mcp", ".env"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(ctx); err == nil || !strings.Contains(err.Error(), "--mcp reads your laptop's Claude Code config") {
		t.Fatalf("--mcp with FILE: %v", err)
	}
	cmd = newSecretsImportCmd(func() (*Env, error) { return e, nil }, &globalFlags{})
	cmd.SetArgs([]string{"--yes", ".env"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(ctx); err == nil || !strings.Contains(err.Error(), "--yes goes with --mcp") {
		t.Fatalf("--yes without --mcp: %v", err)
	}
}

// The agent window's trust write copies the laptop's .mcp.json answers
// where the guest has none, and the .mcp.json dialog stops the typing.
func TestClaudeTrustCopiesMCPApprovals(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	// The empty enabled list is what Claude Code writes into every
	// project it opens: no answer. "mine" was answered on the machine.
	writeClaudeJSON(t, home, map[string]any{"projects": map[string]any{real: map[string]any{"hasTrustDialogAccepted": true, "enabledMcpjsonServers": []any{}, "disabledMcpjsonServers": []any{"mine"}}}})
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	script := claudeTrustScript(dir, mcpApprovals{Enabled: []string{"notes-db", "mine"}, Disabled: []string{"other"}})
	for i := 0; i < 2; i++ {
		if out, err := execBash(script, home).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	p := readClaudeJSON(t, home)["projects"].(map[string]any)[real].(map[string]any)
	if !reflect.DeepEqual(p["enabledMcpjsonServers"], []any{"notes-db"}) || !reflect.DeepEqual(p["disabledMcpjsonServers"], []any{"mine", "other"}) || p["hasTrustDialogAccepted"] != true {
		t.Fatalf("project = %v", p)
	}
	if !paneShowsDialog("╭─\n New MCP server found in this project: notes-db\n") {
		t.Fatal("the .mcp.json dialog is not a dialog")
	}
	if e := dialogError("New MCP server found in this project"); !e.MCP || !strings.Contains(e.Error(), ".mcp.json") {
		t.Fatalf("dialog error = %v", e)
	}
}

// The forward clause names only servers forward can run: one that starts
// with a command and has a name forward takes. An HTTP server on the
// laptop, a headers helper or a bad name gets no clause.
func TestMCPLeftLineForwardClause(t *testing.T) {
	lines := func(replies ...string) string {
		o := &carryOutcome{}
		for _, r := range replies {
			tag, rest, _ := strings.Cut(r, " ")
			if !o.parseMCP(tag, rest) {
				t.Fatalf("not an MCP reply: %q", r)
			}
		}
		return strings.Join(o.mcpLines(), "\n")
	}
	cases := []struct {
		replies []string
		want    string
	}{
		{[]string{"#mcpleft 1 xcode (an Apple app)"}, "Left on your laptop: MCP server xcode (an Apple app). repose mcp forward NAME runs one from here."},
		{[]string{"#mcpleft 0 figma (runs on your laptop)"}, "Left on your laptop: MCP server figma (runs on your laptop)."},
		{[]string{"#mcpleft 0 bad name! (its name has characters other than letters, digits, - and _)"}, "Left on your laptop: MCP server bad name! (its name has characters other than letters, digits, - and _)."},
		{[]string{"#mcpleft 1 xcode (an Apple app)", "#mcpleft 0 figma (runs on your laptop)"}, "Left on your laptop: MCP servers xcode (an Apple app), figma (runs on your laptop). repose mcp forward xcode runs it from here."},
		{[]string{"#mcpleft 1 xcode (an Apple app)", "#mcpleft 1 notes (files on your laptop)", "#mcpleft 0 sentry (gets its headers from a laptop command)"}, "Left on your laptop: MCP servers xcode (an Apple app), notes (files on your laptop), sentry (gets its headers from a laptop command). repose mcp forward NAME runs xcode, notes from here."},
	}
	for _, c := range cases {
		if got := lines(c.replies...); got != c.want {
			t.Errorf("%v:\n got %q\nwant %q", c.replies, got, c.want)
		}
	}
	// What the classifier marks: stdio skips are forwardable, the rest not.
	home, repo := t.TempDir(), t.TempDir()
	for _, c := range []struct {
		name, server string
		forward      bool
	}{
		{"xcode", `{"command":"xcrun","args":["mcpbridge"]}`, true},
		{"local", `{"command":"` + home + `/bin/tool"}`, true},
		{"figma", `{"type":"http","url":"http://127.0.0.1:3845/mcp"}`, false},
		{"helper", `{"type":"http","url":"https://x.example","headersHelper":"get-token"}`, false},
		{"bad name!", `{"command":"npx"}`, false},
		{"proxy", `{"type":"claudeai-proxy","url":"https://x"}`, false},
		{"empty", `{"type":"stdio"}`, false},
	} {
		var s map[string]any
		if err := json.Unmarshal([]byte(c.server), &s); err != nil {
			t.Fatal(err)
		}
		v, _ := classifyMCPServer(home, repo, c.name, s)
		if v.skip == "" || v.forward != c.forward {
			t.Errorf("%s: skip %q forward %v, want forward %v", c.name, v.skip, v.forward, c.forward)
		}
	}
}

// A long unicode name is cut on a rune boundary, and its reason survives
// beside it.
func TestPrintableLongUnicodeName(t *testing.T) {
	name := strings.Repeat("服", 22) // 66 bytes
	got := printable(name)
	if !utf8.ValidString(got) || len(got) > 64 || got != strings.Repeat("服", 21) {
		t.Errorf("printable = %q (%d bytes)", got, len(got))
	}
	left := mcpLeftLines([]mcpSkip{{Name: name + "\x1b[2J", Reason: "its name has characters other than letters, digits, - and _"}, {Name: "xcode", Reason: "an Apple app", Forward: true}})
	want := []string{"0 " + strings.Repeat("服", 21) + " (its name has characters other than letters, digits, - and _)", "1 xcode (an Apple app)"}
	if !reflect.DeepEqual(left, want) {
		t.Errorf("left = %q, want %q", left, want)
	}
}

// On a base with repose-mcp the carry prints no base line and its marker
// has no ":old", whatever the host running the test has.
func TestCarryMCPNewBase(t *testing.T) {
	f := newSyncFixture(t)
	hideCommand(t, "repose-mcp")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "repose-mcp"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REPOSE_TOOL_DIRS", bin)
	home := t.TempDir()
	t.Setenv("REPOSE_SECRETS_DIR", t.TempDir())
	writeClaudeJSON(t, home, map[string]any{"mcpServers": map[string]any{"s": map[string]any{"command": "npx", "args": []any{"-y", "s"}}}})
	mc, _ := buildMCPCarry(home, f.local, testSlug, "", nil)
	_, o, err := syncCredentialsAndCarry(context.Background(), f.target, home, f.local, credSyncOptions{}, carryOptions{MCP: mc})
	if err != nil {
		t.Fatal(err)
	}
	if o.MCPOld {
		t.Errorf("base line on a base with repose-mcp: %v", o.mcpLines())
	}
	if m, _ := os.ReadFile(filepath.Join(f.guestHome, ".repose/carry/claude-mcp")); strings.HasSuffix(strings.TrimSpace(string(m)), ":old") || len(m) == 0 {
		t.Errorf("marker %q", m)
	}
}

// [mcp] forward on Windows, where attach runs no helper, says so once per
// attach instead of doing nothing quietly; elsewhere it says nothing.
func TestWindowsMCPForwardLine(t *testing.T) {
	for goosName, want := range map[string]string{"windows": windowsMCPForwardLine + "\n", "linux": ""} {
		t.Setenv("REPOSE_TEST_GOOS", goosName)
		var errb bytes.Buffer
		e := &Env{ErrOut: &errb, TargetFor: func(string) sshTarget { return sshTarget{} }}
		startSessionHelper(e, sessionOptions{MCP: []string{"notes"}})
		if errb.String() != want {
			t.Errorf("%s: %q, want %q", goosName, errb.String(), want)
		}
		errb.Reset()
		startSessionHelper(e, sessionOptions{})
		if errb.Len() != 0 {
			t.Errorf("%s with no [mcp] forward: %q", goosName, errb.String())
		}
	}
}
