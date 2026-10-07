package mcpreg

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/render/*/out from a sync of in/")

// testPaths points a registry at dir/home, with in/platform.json and
// in/secrets when the case has them.
func testPaths(t *testing.T, caseDir, home string) Paths {
	t.Helper()
	p := Paths{
		Home:       home,
		Platform:   filepath.Join(caseDir, "in", "platform.json"),
		SecretsDir: filepath.Join(caseDir, "in", "secrets"),
		SocketDir:  filepath.Join(caseDir, "in", "sockets"),
		Etc:        filepath.Join(caseDir, "in", "root"),
	}
	return p
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// snapshot is every file under dir but sync's lock, by relative path.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == filepath.Join(".repose", "mcp", ".lock") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func keysOf(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// TestRenderGolden syncs every agent from each case's in/home and compares
// the whole home, plus stderr, with out/. A second sync must change no
// byte and print nothing: each warning comes once per change.
func TestRenderGolden(t *testing.T) {
	cases, err := os.ReadDir("testdata/render")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if !c.IsDir() {
			continue
		}
		t.Run(c.Name(), func(t *testing.T) {
			caseDir := filepath.Join("testdata", "render", c.Name())
			home := t.TempDir()
			copyTree(t, filepath.Join(caseDir, "in", "home"), home)
			p := testPaths(t, caseDir, home)

			var stderr bytes.Buffer
			Sync(p, Agents, &stderr)
			got := snapshot(t, home)
			got["stderr"] = stderr.String()

			outDir := filepath.Join(caseDir, "out")
			if *updateGolden {
				_ = os.RemoveAll(outDir)
				for rel, body := range got {
					f := filepath.Join(outDir, filepath.FromSlash(rel))
					if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			want := snapshot(t, outDir)
			if strings.Join(keysOf(got), "\n") != strings.Join(keysOf(want), "\n") {
				t.Fatalf("files after sync:\n%s\nwant:\n%s", strings.Join(keysOf(got), "\n"), strings.Join(keysOf(want), "\n"))
			}
			for rel, w := range want {
				if got[rel] != w {
					t.Errorf("%s:\n%s\nwant:\n%s", rel, got[rel], w)
				}
			}

			// Idempotent: the second run leaves every byte as it was.
			var stderr2 bytes.Buffer
			Sync(p, Agents, &stderr2)
			again := snapshot(t, home)
			if stderr2.Len() != 0 {
				t.Errorf("second sync printed:\n%s", stderr2.String())
			}
			again["stderr"] = got["stderr"]
			for rel, w := range got {
				if again[rel] != w {
					t.Errorf("second sync changed %s:\n%s\nfirst:\n%s", rel, again[rel], w)
				}
			}
			if len(again) != len(got) {
				t.Errorf("second sync left %d files, first %d", len(again), len(got))
			}
		})
	}
}

// TestUpgradeReplacesNativeEntries: on a machine an earlier base synced,
// the native entries it wrote for servers that now start through the
// launcher are replaced, with rendered.json or without it; the entry the
// user edited stays theirs. The Gemini CLI extension is repose's whole.
func TestUpgradeReplacesNativeEntries(t *testing.T) {
	for _, c := range []string{"upgrade-native", "upgrade-native-no-record"} {
		out := filepath.Join("testdata", "render", c, "out")
		for _, f := range []string{".claude.json", ".config/opencode/config.json", ".gemini/extensions/repose-mcp/gemini-extension.json"} {
			b, err := os.ReadFile(filepath.Join(out, f))
			if err != nil {
				t.Fatal(err)
			}
			s := string(b)
			if !strings.Contains(f, "gemini") && (!strings.Contains(s, `"EXTRA": "1"`) || !strings.Contains(s, `"edited-mcp"`)) {
				t.Errorf("%s %s lost the user's edited entry:\n%s", c, f, s)
			}
			if strings.Contains(s, "linear-mcp") || strings.Contains(s, "notion") || strings.Contains(s, "${LINEAR_TOKEN}") {
				t.Errorf("%s %s kept an old native entry:\n%s", c, f, s)
			}
		}
	}
}

// TestCodexHeldWarnsOncePerChange: a name Codex holds in dotted form warns
// at the first sync, not at the next, and again when the value repose
// would write changes.
func TestCodexHeldWarnsOncePerChange(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	p := Paths{Home: home, Platform: filepath.Join(root, "none.json"), SecretsDir: filepath.Join(root, "secrets"), SocketDir: filepath.Join(root, "sock"), Etc: root}
	writeFile(t, filepath.Join(home, ".codex/config.toml"), "mcp_servers.linear.url = \"https://old.linear.app/mcp\"\n")
	writeFile(t, filepath.Join(home, ".repose/mcp/rendered.json"), `{"version":1,"agents":{"codex":{"user":{"linear":{"url":"https://old.linear.app/mcp"}}}}}`)
	laptop := func(url string) {
		writeFile(t, filepath.Join(home, ".repose/mcp/laptop.json"), `{"version":1,"user":{"linear":{"type":"http","url":"`+url+`"}}}`)
	}
	sync := func() string {
		var b bytes.Buffer
		Sync(p, []string{"codex"}, &b)
		return b.String()
	}
	laptop("https://a.example/mcp")
	if got := sync(); !strings.Contains(got, "mcp_servers.linear") {
		t.Fatalf("first sync: %q", got)
	}
	if got := sync(); got != "" {
		t.Fatalf("second sync: %q", got)
	}
	laptop("https://b.example/mcp")
	if got := sync(); !strings.Contains(got, "mcp_servers.linear") {
		t.Fatalf("after the change: %q", got)
	}
	if got := sync(); got != "" {
		t.Fatalf("after the change, again: %q", got)
	}
}

// TestCodexLinkLeftAlone: a ~/.codex/config.toml that is a symlink stays a
// link with its target unchanged, quietly; agents/codex.json still holds
// what Codex would get.
func TestCodexLinkLeftAlone(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	p := Paths{Home: home, Platform: filepath.Join(root, "none.json"), SecretsDir: filepath.Join(root, "secrets"), SocketDir: filepath.Join(root, "sock"), Etc: root}
	target := filepath.Join(root, "real.toml")
	writeFile(t, target, "notify = [\"mine\"]\n")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".codex/config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".repose/mcp/laptop.json"), `{"version":1,"user":{"linear":{"type":"http","url":"https://mcp.linear.app/mcp"}}}`)
	var b bytes.Buffer
	Sync(p, []string{"codex"}, &b)
	if b.String() != "" {
		t.Fatalf("stderr: %q", b.String())
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.toml is no longer a link: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "notify = [\"mine\"]\n" {
		t.Fatalf("target changed: %q", got)
	}
	view, err := os.ReadFile(filepath.Join(home, ".repose/mcp/agents/codex.json"))
	if err != nil || !strings.Contains(string(view), "linear") {
		t.Fatalf("agents/codex.json: %s %v", view, err)
	}
	wantSkipped(t, p, "linear", "codex", "~/.codex/config.toml is a link, which repose does not write")
}

// wantSkipped checks the status row of laptop server name says why agent
// lacks it.
func wantSkipped(t *testing.T, p Paths, name, agent, reason string) {
	t.Helper()
	st, err := ReadStatus(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st.Servers {
		if s.Name == name && s.From == FromLaptop {
			if s.Skipped[agent] != reason || !strings.Contains(s.State, agent+": "+reason) {
				t.Errorf("%s: skipped %v, state %q; want %s: %s", name, s.Skipped, s.State, agent, reason)
			}
			return
		}
	}
	t.Errorf("no row for %s", name)
}

// TestCodexInlineServersLeftAlone: a root `mcp_servers = { ... }` takes
// no [mcp_servers.NAME] after it, which Codex would refuse to load; the
// file stays as it was and sync warns once.
func TestCodexInlineServersLeftAlone(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	p := Paths{Home: home, Platform: filepath.Join(root, "none.json"), SecretsDir: filepath.Join(root, "secrets"), SocketDir: filepath.Join(root, "sock"), Etc: root}
	const orig = "model = \"o3\"\nmcp_servers = { mine = { command = \"x\" } }\n"
	writeFile(t, filepath.Join(home, ".codex/config.toml"), orig)
	writeFile(t, filepath.Join(home, ".repose/mcp/laptop.json"), `{"version":1,"user":{"linear":{"type":"http","url":"https://mcp.linear.app/mcp"}}}`)
	var b bytes.Buffer
	Sync(p, []string{"codex"}, &b)
	if !strings.Contains(b.String(), "mcp_servers.linear") {
		t.Fatalf("first sync stderr: %q", b.String())
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".codex/config.toml")); string(got) != orig {
		t.Fatalf("config.toml changed:\n%s", got)
	}
	b.Reset()
	Sync(p, []string{"codex"}, &b)
	if b.String() != "" {
		t.Fatalf("second sync stderr: %q", b.String())
	}
	wantSkipped(t, p, "linear", "codex", "~/.codex/config.toml holds it in a form repose does not edit")
}

// TestOpencodeCommentedConfig: opencode reads comments and trailing
// commas in its .json files. A commented config.json is left as written
// with one warning, and a server the user's commented opencode.json
// defines is not rendered beside it.
func TestOpencodeCommentedConfig(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	p := Paths{Home: home, Platform: filepath.Join(root, "none.json"), SecretsDir: filepath.Join(root, "secrets"), SocketDir: filepath.Join(root, "sock"), Etc: root}
	const orig = "{\n  // mine\n  \"theme\": \"dark\",\n}\n"
	cfg := filepath.Join(home, ".config/opencode/config.json")
	writeFile(t, cfg, orig)
	writeFile(t, filepath.Join(home, ".repose/mcp/laptop.json"), `{"version":1,"user":{"linear":{"type":"http","url":"https://mcp.linear.app/mcp"}}}`)
	var b bytes.Buffer
	Sync(p, []string{"opencode"}, &b)
	if !strings.Contains(b.String(), "has comments") {
		t.Fatalf("first sync stderr: %q", b.String())
	}
	if got, _ := os.ReadFile(cfg); string(got) != orig {
		t.Fatalf("config.json changed:\n%s", got)
	}
	b.Reset()
	Sync(p, []string{"opencode"}, &b)
	if b.String() != "" {
		t.Fatalf("second sync stderr: %q", b.String())
	}
	wantSkipped(t, p, "linear", "opencode", "~/.config/opencode/config.json has comments, which a rewrite would lose")

	// A plain config.json, and the user's own linear in a commented
	// opencode.json: repose renders nothing under that name.
	writeFile(t, cfg, "{}\n")
	writeFile(t, filepath.Join(home, ".config/opencode/opencode.json"), "{\n  // mine\n  \"mcp\": {\"linear\": {\"type\": \"remote\", \"url\": \"https://x\"},},\n}\n")
	b.Reset()
	Sync(p, []string{"opencode"}, &b)
	if got, _ := os.ReadFile(cfg); strings.Contains(string(got), "linear") || b.String() != "" {
		t.Fatalf("config.json = %s, stderr %q", got, b.String())
	}
}
