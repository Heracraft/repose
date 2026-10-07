package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/claude-merge/*/want.json")

// runClaudeMerge runs the guest half of the settings merge exactly as the
// carry does (claudeSettingsScript with the embedded jq program), with
// home as the guest's $HOME, and returns its output lines.
func runClaudeMerge(t *testing.T, home string, laptop []byte, laptopHome string, cfg ...string) string {
	t.Helper()
	dir := t.TempDir()
	c := filepath.Join(dir, "claude")
	if err := os.MkdirAll(c, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"settings.json": laptop, "home": []byte(laptopHome), "cfg": []byte(strings.Join(cfg, "")), "merge.jq": claudeMergeJQ} {
		if err := os.WriteFile(filepath.Join(c, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "part.sh")
	if err := os.WriteFile(script, []byte(claudeSettingsScript()), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-e", script, dir)
	cmd.Env = append(filterTestEnv(os.Environ(), "HOME"), "HOME="+home, "REPOSE_CLAUDE_PLATFORM="+filepath.Join("testdata", "claude-merge", "platform.json"))
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("merge script: %v\n%s%s", err, out.String(), stderr.String())
	}
	return out.String()
}

// The golden cases: permissions union (union), the laptop-home rewrite
// with hooks the guest cannot run dropped (rewrite), repose-hook entries
// stripped from both sides and the platform's added once, last (hooks),
// a guest with no settings.json yet (fresh), the guest's
// bypassPermissions default kept when the laptop sets no defaultMode
// (mode-kept) and the laptop's own defaultMode winning over it
// (mode-laptop-wins, DECISIONS I-250). Each is merged twice;
// the second run must not change a byte.
func TestClaudeSettingsMergeGolden(t *testing.T) {
	cases, err := os.ReadDir(filepath.Join("testdata", "claude-merge"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if !c.IsDir() {
			continue
		}
		t.Run(c.Name(), func(t *testing.T) {
			src := filepath.Join("testdata", "claude-merge", c.Name())
			home := t.TempDir()
			settings := filepath.Join(home, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(filepath.Join(src, "guest.json")); err == nil {
				if err := os.WriteFile(settings, b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// What the scripts part carried before the merge runs.
			for _, s := range []string{".claude/statusline.sh", ".claude/hooks/fmt.sh"} {
				p := filepath.Join(home, s)
				_ = os.MkdirAll(filepath.Dir(p), 0o700)
				if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			laptop, err := os.ReadFile(filepath.Join(src, "laptop.json"))
			if err != nil {
				t.Fatal(err)
			}
			lh, _ := os.ReadFile(filepath.Join(src, "home"))
			out := runClaudeMerge(t, home, laptop, strings.TrimSpace(string(lh)))
			first, err := os.ReadFile(settings)
			if err != nil {
				t.Fatal(err)
			}
			// The guest's home is /home/dev; this one is a temporary
			// directory, so the goldens are written with it put back.
			first = bytes.ReplaceAll(first, []byte(home), []byte("/home/dev"))
			out = strings.ReplaceAll(out, home, "/home/dev")
			if !json.Valid(first) {
				t.Fatalf("merge wrote invalid JSON:\n%s", first)
			}
			golden := filepath.Join(src, "want.json")
			if *updateGolden {
				if err := os.WriteFile(golden, first, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(src, "want.out"), []byte(out), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if !bytes.Equal(first, want) {
				t.Errorf("merged settings.json differs from %s:\n%s", golden, first)
			}
			wantOut, _ := os.ReadFile(filepath.Join(src, "want.out"))
			if out != string(wantOut) {
				t.Errorf("merge output = %q, want %q", out, wantOut)
			}
			if _, err := os.Stat(settings + ".repose-prev"); (err == nil) != fileExists(filepath.Join(src, "guest.json")) {
				t.Errorf("settings.json.repose-prev present = %v, want it exactly when there was a guest file", err == nil)
			}

			// Idempotent: the same laptop file again changes nothing.
			runClaudeMerge(t, home, laptop, strings.TrimSpace(string(lh)))
			second, _ := os.ReadFile(settings)
			second = bytes.ReplaceAll(second, []byte(home), []byte("/home/dev"))
			if !bytes.Equal(first, second) {
				t.Errorf("second merge changed the file:\nfirst:\n%s\nsecond:\n%s", first, second)
			}
		})
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// The laptop's home is rewritten wherever it sits in a string, not only
// at the start ("sh /Users/lap/.claude/hooks/a.sh", "Read(/Users/lap/...)"),
// so a hook whose script was carried is kept; and a config directory
// moved by CLAUDE_CONFIG_DIR maps to the guest's ~/.claude in its
// absolute and ~/ forms.
func TestClaudeSettingsRewriteHomeAnywhere(t *testing.T) {
	home := t.TempDir()
	for _, s := range []string{".claude/hooks/a.sh", ".claude/hooks/b.sh"} {
		p := filepath.Join(home, s)
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	laptop := []byte(`{
  "permissions": {"allow": ["Read(/Users/lap/src/**)", "Read(/Users/lapx/other)"]},
  "hooks": {"Stop": [{"matcher": "", "hooks": [
    {"type": "command", "command": "sh /Users/lap/.config/claude/hooks/a.sh --quiet"},
    {"type": "command", "command": "sh ~/.config/claude/hooks/b.sh"}
  ]}]}
}`)
	// The laptop half: CLAUDE_CONFIG_DIR is read, and sent only when it
	// is not ~/.claude.
	lh := t.TempDir()
	moved := filepath.Join(lh, ".config", "claude")
	_ = os.MkdirAll(moved, 0o700)
	if err := os.WriteFile(filepath.Join(moved, "CLAUDE.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", moved)
	if cc, err := buildClaudeCarry(lh); err != nil || cc == nil || cc.CfgDir != moved {
		t.Fatalf("CfgDir with CLAUDE_CONFIG_DIR set: %+v %v", cc, err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(lh, ".claude"))
	_ = os.MkdirAll(filepath.Join(lh, ".claude"), 0o700)
	_ = os.WriteFile(filepath.Join(lh, ".claude", "CLAUDE.md"), []byte("x\n"), 0o600)
	if cc, err := buildClaudeCarry(lh); err != nil || cc == nil || cc.CfgDir != "" {
		t.Fatalf("CfgDir for ~/.claude: %+v %v", cc, err)
	}

	out := runClaudeMerge(t, home, laptop, "/Users/lap", "/Users/lap/.config/claude")
	if strings.Contains(out, "#dropped") {
		t.Errorf("a hook whose script was carried was dropped: %q", out)
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"sh " + home + "/.claude/hooks/a.sh --quiet",
		"sh ~/.claude/hooks/b.sh",
		"Read(" + home + "/src/**)",
		"Read(/Users/lapx/other)", // another user's home is not this one
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("merged settings lack %q:\n%s", want, b)
		}
	}
}

// A laptop SessionStart hook joins the guest's instead of replacing them,
// so herdr's resume hook (written to the guest's settings.json by `herdr
// integration install claude`) survives every run; a hook removed from
// the laptop leaves the guest on the next run, and a hook both sides have
// is kept once (DECISIONS I-499).
func TestClaudeSettingsMergeKeepsGuestHooks(t *testing.T) {
	home := t.TempDir()
	herdrHook := filepath.Join(home, ".claude", "hooks", "herdr-agent-state.sh")
	_ = os.MkdirAll(filepath.Dir(herdrHook), 0o700)
	if err := os.WriteFile(herdrHook, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	herdrCmd := "bash '" + herdrHook + "' session"
	settings := filepath.Join(home, ".claude", "settings.json")
	guest := `{"hooks": {"SessionStart": [
  {"matcher": "^(startup|resume|clear|compact|fork)$", "hooks": [{"type": "command", "command": "` + herdrCmd + `", "timeout": 10}]},
  {"matcher": "", "hooks": [{"type": "command", "command": "echo both"}]}
]}}`
	if err := os.WriteFile(settings, []byte(guest), 0o600); err != nil {
		t.Fatal(err)
	}
	laptop := func(cmd string) []byte {
		return []byte(`{"hooks": {"SessionStart": [
  {"matcher": "", "hooks": [{"type": "command", "command": "` + cmd + `"}]},
  {"matcher": "", "hooks": [{"type": "command", "command": "echo both"}]}
]}}`)
	}
	sessionStart := func() []string {
		t.Helper()
		b, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Hooks map[string][]struct {
				Hooks []struct{ Command string } `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("%v\n%s", err, b)
		}
		var cmds []string
		for _, g := range v.Hooks["SessionStart"] {
			for _, h := range g.Hooks {
				cmds = append(cmds, h.Command)
			}
		}
		return cmds
	}

	runClaudeMerge(t, home, laptop("echo laptop-one"), "/Users/lap")
	if got, want := strings.Join(sessionStart(), "|"), herdrCmd+"|echo laptop-one|echo both"; got != want {
		t.Fatalf("SessionStart after the first run = %q, want %q", got, want)
	}
	first, _ := os.ReadFile(settings)
	runClaudeMerge(t, home, laptop("echo laptop-one"), "/Users/lap")
	if again, _ := os.ReadFile(settings); !bytes.Equal(first, again) {
		t.Errorf("the same laptop file again changed settings.json:\n%s\n%s", first, again)
	}

	// The laptop's hook changes: the old one goes, herdr's stays.
	runClaudeMerge(t, home, laptop("echo laptop-two"), "/Users/lap")
	if got, want := strings.Join(sessionStart(), "|"), herdrCmd+"|echo laptop-two|echo both"; got != want {
		t.Errorf("SessionStart after the laptop changed = %q, want %q", got, want)
	}

	// A laptop file with no hooks at all takes its own hooks out and
	// leaves herdr's.
	runClaudeMerge(t, home, []byte(`{"model": "opus"}`), "/Users/lap")
	if got, want := strings.Join(sessionStart(), "|"), herdrCmd; got != want {
		t.Errorf("SessionStart after the laptop dropped its hooks = %q, want %q", got, want)
	}
}

// A guest settings.json that is not JSON is left exactly as it is, with
// one warning; nothing half-written appears beside it.
func TestClaudeSettingsMergeLeavesAnInvalidGuestFile(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(settings), 0o700)
	bad := []byte(`{"model": "opus",`)
	if err := os.WriteFile(settings, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	out := runClaudeMerge(t, home, []byte(`{"model":"sonnet"}`), "/Users/lap")
	if !strings.Contains(out, "#warn The guest's ~/.claude/settings.json is not valid JSON") {
		t.Errorf("output = %q", out)
	}
	if b, _ := os.ReadFile(settings); !bytes.Equal(b, bad) {
		t.Errorf("invalid guest file changed to %q", b)
	}
	for _, leftover := range []string{".tmp", ".repose-prev"} {
		if fileExists(settings + leftover) {
			t.Errorf("%s left behind", leftover)
		}
	}
}

// An invalid laptop settings.json never leaves the laptop, and says so.
func TestClaudeCarrySkipsAnInvalidLaptopFile(t *testing.T) {
	home := t.TempDir()
	d := filepath.Join(home, ".claude")
	_ = os.MkdirAll(d, 0o700)
	if err := os.WriteFile(filepath.Join(d, "settings.json"), []byte(`{nope`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "CLAUDE.md"), []byte("be terse\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cc, err := buildClaudeCarry(home)
	if err != nil {
		t.Fatal(err)
	}
	if cc.Settings != nil {
		t.Fatal("an invalid settings.json would be sent")
	}
	if len(cc.Notes) != 1 || !strings.Contains(cc.Notes[0], "not valid JSON") {
		t.Errorf("notes = %v", cc.Notes)
	}
	if len(cc.Items) != 1 || cc.Items[0].Marker != "claude-claude-md" {
		t.Errorf("items = %+v", cc.Items)
	}
}

// claudeLaptopHome is a laptop $HOME with the Claude config I-196 carries
// and, beside it, everything it must never carry.
func claudeLaptopHome(t *testing.T, plugins bool) string {
	t.Helper()
	home := t.TempDir()
	enabled := `"mine@laptop-dir":true,"p@privmkt":true`
	if plugins {
		enabled = `"gopls-lsp@claude-plugins-official":true,` + enabled
	}
	files := map[string]string{
		".claude/CLAUDE.md":              "Prefer small commits.\n",
		".claude/keybindings.json":       `{"bindings":[]}`,
		".claude/skills/deploy/SKILL.md": "---\nname: deploy\ndescription: ship it\n---\nRun the deploy.\n",
		".claude/agents/reviewer.md":     "---\nname: reviewer\n---\nReview.\n",
		".claude/commands/fix.md":        "Fix the build.\n",
		".claude/hooks/notify.sh":        "#!/bin/sh\necho done\n",
		".claude/settings.json": `{"model":"opus","permissions":{"allow":["Bash(go test:*)","Bash(curl -H 'Authorization: Bearer NEVER-PERM-BEARER-0123456789':*)","Bash(grep -rn Bearer src:*)"]},"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"` + home + `/.claude/hooks/notify.sh"}]}],"PostToolUse":[{"matcher":"","hooks":[{"type":"command","command":"curl https://u:NEVER-HOOK-PASS@hooks.example/x"}]}],"Notification":[{"matcher":"","hooks":[{"type":"command","command":"curl -H \"Authorization: Bearer $NTFY_TOKEN\" -d done ntfy.example/t"}]}]},"enabledPlugins":{` + enabled + `},"extraKnownMarketplaces":{"laptop-dir":{"source":{"source":"directory","path":"/Users/lap/mkt"}},"privmkt":{"source":{"source":"git","url":"https://tok:NEVER-MKT-TOKEN@git.example/m.git"}}},` +
			// Credentials inside values (second review, item 4).
			`"statusLine":{"type":"command","command":"echo sk-ant-NEVER-STATUSLINE"},` +
			// Settings keys that hold or run a credential (I-211).
			`"env":{"ANTHROPIC_API_KEY":"NEVER-ENV-API-KEY","GITHUB_MCP_TOKEN":"NEVER-ENV-MCP"},"apiKeyHelper":"~/.claude/NEVER-API-KEY-HELPER.sh",` +
			`"awsAuthRefresh":"NEVER-AWS-REFRESH","awsCredentialExport":"NEVER-AWS-EXPORT","otelHeadersHelper":"NEVER-OTEL-HELPER","forceLoginMethod":"NEVER-FORCE-LOGIN"}`,
		// Never carried, whatever else happens:
		".claude/.credentials.json":               `{"claudeAiOauth":{"accessToken":"NEVER-CLAUDE-CREDS"}}`,
		".claude/skills/deploy/.credentials.json": `NEVER-NESTED-CREDS`,
		".claude/projects/-home-x/abc.jsonl":      `{"NEVER-TRANSCRIPT":1}`,
		".claude/history.jsonl":                   `{"display":"NEVER-HISTORY"}`,
		".claude/todos/t.json":                    `NEVER-TASK-LIST`,
		".claude/shell-snapshots/s.sh":            `NEVER-SNAPSHOT`,
		".claude/file-history/f":                  `NEVER-FILE-HISTORY`,
		".claude/plugins/cache/x":                 `NEVER-PLUGIN-CACHE`,
		".claude/statsig/s":                       `NEVER-STATSIG`,
		// Secret-looking files inside carried directories (item 5).
		".claude/skills/deploy/.env":            `NEVER-SKILL-ENV`,
		".claude/skills/deploy/.env.local":      `NEVER-SKILL-ENV-LOCAL`,
		".claude/agents/id_ed25519":             `NEVER-AGENT-KEY`,
		".claude/agents/id_generator.py":        "print(\"GENERATOR-OK\")\n",
		".claude/commands/server.pem":           `NEVER-PEM`,
		".claude/commands/tls.key":              `NEVER-DOT-KEY`,
		".claude/skills/deploy/aws-credentials": `NEVER-SKILL-CREDENTIALS`,
		".claude/output-styles/cert.p12":        `NEVER-P12`,
		".claude.json": `{"oauthAccount":"NEVER-CLAUDE-JSON","userID":"NEVER-CLAUDE-USER-ID","projects":{"/x":{"history":["NEVER-CLAUDE-PROJECT-HISTORY"]}},` +
			// MCP servers travel templated (I-556); their credentials never.
			`"mcpServers":{"lin":{"type":"http","url":"https://mcp.linear.app/mcp","headers":{"Authorization":"Bearer NEVER-MCP-BEARER"}},"pg":{"command":"npx","args":["-y","pg"],"env":{"DATABASE_URL":"postgres://u:NEVER-MCP-PASS@db/x"}}}}`,
		".ssh/id_ed25519":          "NEVER-SSH-KEY",
		".gemini/oauth_creds.json": `NEVER-GEMINI`,
	}
	for rel, body := range files {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// I-196 and the secrets rule: the whole stream of a run's credentials ssh
// with every carry part in it holds none of the never-carried files,
// while the config does arrive, the hook script runs from the guest, a
// permission granted in the guest survives the next carry, and the
// platform hooks are there once.
func TestCarryClaudeNeverCarriesSecrets(t *testing.T) {
	f := newSyncFixture(t)
	// No plugin the guest could install: the installer runs claude, which
	// TestClaudePluginsScript covers with a stand-in.
	home := claudeLaptopHome(t, false)
	ctx := context.Background()
	var stream bytes.Buffer
	observePayload = func(script string, tarball []byte) { stream.WriteString(script); stream.Write(tarball) }
	t.Cleanup(func() { observePayload = nil })

	carry := func() *carryOutcome {
		t.Helper()
		cc, err := buildClaudeCarry(home)
		if err != nil {
			t.Fatal(err)
		}
		out, err := runSSH(ctx, f.target, markerScript(), nil)
		if err != nil {
			t.Fatal(err)
		}
		mc, _ := buildMCPCarry(home, "", testSlug, "", nil)
		_, o, err := syncCredentialsAndCarry(ctx, f.target, home, f.local, credSyncOptions{}, carryOptions{TZ: "UTC", Claude: cc, MCP: mc, Markers: parseMarkers(string(out))})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	o := carry()
	if len(o.Failed) != 0 {
		t.Fatalf("outcome = %+v", o)
	}
	// The files left behind for their names are named (never their
	// contents); an SSH key's name is, a script called id_generator.py
	// is carried.
	warned := strings.Join(o.Warnings, "\n")
	for _, name := range []string{"~/.claude/agents/id_ed25519", "~/.claude/skills/deploy/.env", "~/.claude/commands/server.pem"} {
		if !strings.Contains(warned, name) {
			t.Errorf("warnings do not name %s:\n%s", name, warned)
		}
	}
	if strings.Contains(warned, "NEVER-") || strings.Contains(warned, "id_generator") {
		t.Errorf("warnings = %s", warned)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestHome, ".claude/agents/id_generator.py")); !strings.Contains(string(b), "GENERATOR-OK") {
		t.Errorf("id_generator.py did not arrive: %q", b)
	}
	// A hook that names its token through the environment is not a
	// credential, and neither is a grep for the word.
	if b, _ := os.ReadFile(filepath.Join(f.guestHome, ".claude/settings.json")); !strings.Contains(string(b), "Bearer $NTFY_TOKEN") || !strings.Contains(string(b), "grep -rn Bearer src") {
		t.Errorf("safe Bearer entries dropped:\n%s", b)
	}
	for _, never := range []string{"NEVER-CLAUDE-CREDS", "NEVER-NESTED-CREDS", "NEVER-TRANSCRIPT", "NEVER-HISTORY", "NEVER-TASK-LIST", "NEVER-SNAPSHOT", "NEVER-FILE-HISTORY", "NEVER-PLUGIN-CACHE", "NEVER-STATSIG", "NEVER-CLAUDE-JSON", "NEVER-SSH-KEY", "NEVER-GEMINI",
		"NEVER-ENV-API-KEY", "NEVER-ENV-MCP", "NEVER-API-KEY-HELPER", "NEVER-AWS-REFRESH", "NEVER-AWS-EXPORT", "NEVER-OTEL-HELPER", "NEVER-FORCE-LOGIN",
		"NEVER-MKT-TOKEN", "NEVER-STATUSLINE", "NEVER-PERM-BEARER", "NEVER-HOOK-PASS",
		"NEVER-SKILL-ENV", "NEVER-AGENT-KEY", "NEVER-PEM", "NEVER-DOT-KEY", "NEVER-SKILL-CREDENTIALS", "NEVER-P12",
		"NEVER-CLAUDE-USER-ID", "NEVER-CLAUDE-PROJECT-HISTORY", "NEVER-MCP-BEARER", "NEVER-MCP-PASS"} {
		if bytes.Contains(stream.Bytes(), []byte(never)) {
			t.Errorf("%s is in the carry stream", never)
		}
	}
	t.Logf("carry stream: %d bytes, none of the never-carried markers in it; sent %v", stream.Len(), o.Sent)
	// The MCP servers arrived, as references to secrets (I-556).
	if b, _ := os.ReadFile(filepath.Join(f.guestHome, ".repose/mcp/laptop.json")); !strings.Contains(string(b), "Bearer ${LIN_TOKEN}") || !strings.Contains(string(b), "${DATABASE_URL}") {
		t.Errorf("laptop.json:\n%s", b)
	}
	for _, rel := range []string{".claude/CLAUDE.md", ".claude/keybindings.json", ".claude/skills/deploy/SKILL.md", ".claude/agents/reviewer.md", ".claude/commands/fix.md", ".claude/hooks/notify.sh"} {
		if !fileExists(filepath.Join(f.guestHome, rel)) {
			t.Errorf("%s did not arrive", rel)
		}
	}
	// Never in the guest, whatever the carry sent.
	for _, rel := range []string{".claude/.credentials.json", ".claude/projects", ".claude/history.jsonl", ".claude.json"} {
		if fileExists(filepath.Join(f.guestHome, rel)) {
			t.Errorf("%s is in the guest", rel)
		}
	}
	if info, err := os.Stat(filepath.Join(f.guestHome, ".claude/hooks/notify.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("hook script not executable in the guest: %v %v", info, err)
	}
	var s map[string]any
	b, _ := os.ReadFile(filepath.Join(f.guestHome, ".claude/settings.json"))
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("guest settings: %v\n%s", err, b)
	}
	if !strings.Contains(string(b), f.guestHome+"/.claude/hooks/notify.sh") || strings.Contains(string(b), home) {
		t.Errorf("hook path not rewritten to the guest's home:\n%s", b)
	}

	// A permission granted inside the guest ("Yes, and don't ask again").
	s["permissions"].(map[string]any)["allow"] = append(s["permissions"].(map[string]any)["allow"].([]any), "Bash(make:*)")
	nb, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(filepath.Join(f.guestHome, ".claude/settings.json"), nb, 0o600); err != nil {
		t.Fatal(err)
	}
	// Nothing changed on the laptop: nothing is sent.
	if o := carry(); len(o.Sent) != 0 {
		t.Fatalf("unchanged carry sent %v", o.Sent)
	}
	// The laptop changes its settings: the merge runs, the guest's
	// permission stays.
	sp := filepath.Join(home, ".claude/settings.json")
	lb, _ := os.ReadFile(sp)
	if err := os.WriteFile(sp, bytes.Replace(lb, []byte(`"opus"`), []byte(`"sonnet"`), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	carry()
	b, _ = os.ReadFile(filepath.Join(f.guestHome, ".claude/settings.json"))
	if !strings.Contains(string(b), "Bash(make:*)") || !strings.Contains(string(b), `"sonnet"`) {
		t.Errorf("after the laptop changed:\n%s", b)
	}
	if !fileExists(filepath.Join(f.guestHome, ".claude/settings.json.repose-prev")) {
		t.Error("no settings.json.repose-prev")
	}
}

// 15-dev-ergonomics §6: a jq merge that fails in the guest leaves the
// previous settings.json exactly as it was, says so, and writes no marker
// (so the next carry tries again); a hook whose script the guest lacks is
// named on the carry that changed it and not on the next, unchanged one.
func TestClaudeSettingsFailureAndDroppedHookOncePerChange(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	home := t.TempDir()
	sp := filepath.Join(home, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(sp), 0o700)
	laptop := `{"model":"opus","hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"` + home + `/.claude/hooks/gone.sh"}]}]}}`
	if err := os.WriteFile(sp, []byte(laptop), 0o600); err != nil {
		t.Fatal(err)
	}
	carry := func() *carryOutcome {
		t.Helper()
		cc, err := buildClaudeCarry(home)
		if err != nil {
			t.Fatal(err)
		}
		out, err := runSSH(ctx, f.target, markerScript(), nil)
		if err != nil {
			t.Fatal(err)
		}
		mc, _ := buildMCPCarry(home, "", testSlug, "", nil)
		_, o, err := syncCredentialsAndCarry(ctx, f.target, home, f.local, credSyncOptions{}, carryOptions{TZ: "UTC", Claude: cc, MCP: mc, Markers: parseMarkers(string(out))})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}

	// Valid JSON the merge cannot use (an array): the merge fails.
	guest := filepath.Join(f.guestHome, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(guest), 0o700)
	if err := os.WriteFile(guest, []byte("[1]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := carry()
	if len(o.Failed) != 1 || o.Failed[0] != "Claude settings" {
		t.Fatalf("failed = %v, want [Claude settings]", o.Failed)
	}
	if !strings.Contains(strings.Join(o.Lines(), "\n"), "the guest keeps its previous one") {
		t.Errorf("lines = %v", o.Lines())
	}
	if b, _ := os.ReadFile(guest); string(b) != "[1]\n" {
		t.Errorf("guest settings.json changed by a failed merge: %q", b)
	}
	if fileExists(guest+".tmp") || fileExists(filepath.Join(f.guestHome, ".repose", "carry", "claude-settings")) {
		t.Error("a failed merge left settings.json.tmp or wrote its marker")
	}

	// A mergeable guest file: the hook is dropped and named once.
	if err := os.WriteFile(guest, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o = carry()
	if len(o.Failed) != 0 || len(o.Dropped) != 1 || !strings.HasPrefix(o.Dropped[0], "Claude hook \"") {
		t.Fatalf("outcome = %+v, want the hook dropped and named", o)
	}
	if o = carry(); len(o.Dropped) != 0 || len(o.Lines()) != 0 {
		t.Errorf("unchanged carry spoke again: %v", o.Lines())
	}
	// A change on the laptop names it again.
	if err := os.WriteFile(sp, []byte(strings.Replace(laptop, "opus", "sonnet", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if o = carry(); len(o.Dropped) != 1 {
		t.Errorf("after a laptop change, dropped = %v", o.Dropped)
	}
}

// An unchanged Claude config costs a stat per file: the second build
// reads no file (the hashes come from carry-hashes.json by size and
// mtime), and an edited file is read again and changes its item's hash.
func TestClaudeCarryHashesAreCached(t *testing.T) {
	withHome(t)
	home := claudeLaptopHome(t, false)
	first, err := buildClaudeCarry(home)
	if err != nil {
		t.Fatal(err)
	}
	before := claudeReads.Load()
	second, err := buildClaudeCarry(home)
	if err != nil {
		t.Fatal(err)
	}
	if n := claudeReads.Load() - before; n != 0 {
		t.Errorf("an unchanged config read %d files", n)
	}
	hashes := func(cc *claudeCarry) map[string]string {
		m := map[string]string{}
		for _, it := range cc.Items {
			m[it.Marker] = it.Hash
		}
		return m
	}
	h1, h2 := hashes(first), hashes(second)
	if len(h1) == 0 || fmt.Sprint(h1) != fmt.Sprint(h2) {
		t.Fatalf("hashes differ between builds:\n%v\n%v", h1, h2)
	}
	skill := filepath.Join(home, ".claude", "skills", "deploy", "SKILL.md")
	if err := os.WriteFile(skill, []byte("---\nname: deploy\n---\nchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(skill, later, later)
	before = claudeReads.Load()
	third, err := buildClaudeCarry(home)
	if err != nil {
		t.Fatal(err)
	}
	if n := claudeReads.Load() - before; n != 1 {
		t.Errorf("one edited file: %d reads, want 1", n)
	}
	h3 := hashes(third)
	if h3["claude-skills"] == h1["claude-skills"] || h3["claude-agents"] != h1["claude-agents"] {
		t.Errorf("hashes after editing a skill: %v, before %v", h3, h1)
	}
}

// carry-hashes.json is saved through a temp file of its own: concurrent
// carries (run and a session helper) never share one, and a leftover
// fixed-name temp (here squatted by a directory) cannot stop the save.
func TestClaudeHashCacheSavesConcurrently(t *testing.T) {
	dir, err := configDir()
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "carry-hashes.json")
	_ = os.Remove(cache)
	if err := os.MkdirAll(cache+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cache + ".tmp") })
	home := claudeLaptopHome(t, false)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := buildClaudeCarry(home); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	b, err := os.ReadFile(cache)
	if err != nil || !json.Valid(b) {
		t.Fatalf("cache after concurrent saves: %v %q", err, b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "carry-hashes-*.tmp")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestClaudePluginsFromALaptopDirectoryAreNamed(t *testing.T) {
	cc, err := buildClaudeCarry(claudeLaptopHome(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if len(cc.Plugins) != 1 || cc.Plugins[0] != "gopls-lsp@claude-plugins-official" {
		t.Errorf("plugins = %v", cc.Plugins)
	}
	if len(cc.Notes) != 2 || !strings.Contains(cc.Notes[0], "Left out 4 entries") || !strings.Contains(cc.Notes[1], "mine@laptop-dir") {
		t.Errorf("notes = %v", cc.Notes)
	}
}

// The background installer, with a stand-in claude on PATH that records
// its calls: it adds the official marketplace when the guest lacks it,
// installs what is missing, skips what is there, and writes its marker
// only when every install worked.
func TestClaudePluginsScript(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	stub := `#!/bin/sh
echo "$*" >> ` + calls + `
case "$*" in
  "plugin list --json") echo '[{"id":"have@claude-plugins-official"}]' ;;
  "plugin marketplace list --json") echo '[]' ;;
  "plugin install broken@claude-plugins-official") exit 1 ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(plugins ...string) {
		t.Helper()
		list, _ := json.Marshal(map[string]any{"plugins": plugins, "marketplaces": map[string]string{}})
		_ = os.MkdirAll(filepath.Join(home, ".repose"), 0o700)
		if err := os.WriteFile(filepath.Join(home, ".repose", "claude-plugins.json"), list, 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(calls)
		cmd := exec.Command("sh", "-c", claudePluginsScript("h1"))
		cmd.Env = append(filterTestEnv(os.Environ(), "HOME", "PATH", "TMUX"), "HOME="+home, "PATH="+bin+":"+os.Getenv("PATH"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("installer: %v\n%s", err, out)
		}
	}
	marker := filepath.Join(home, ".repose", "carry", "claude-plugins")

	run("have@claude-plugins-official", "new@claude-plugins-official", "broken@claude-plugins-official")
	got, _ := os.ReadFile(calls)
	for _, want := range []string{"plugin marketplace add anthropics/claude-plugins-official", "plugin install new@claude-plugins-official", "plugin install broken@claude-plugins-official"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("calls lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(string(got), "install have@") {
		t.Errorf("an installed plugin was installed again:\n%s", got)
	}
	if fileExists(marker) {
		t.Error("marker written although an install failed")
	}
	run("have@claude-plugins-official", "new@claude-plugins-official")
	if b, err := os.ReadFile(marker); err != nil || strings.TrimSpace(string(b)) != "h1" {
		t.Errorf("marker after a clean run: %q %v", b, err)
	}
}
