package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runScript runs a trust script with HOME set to home and CODEX_HOME
// unset, the way startAgentWindow's ssh runs it in the guest.
func runScript(t *testing.T, home, script string) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	cmd := exec.Command("bash", "-c", script+"echo started")
	env := []string{"HOME=" + home}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "CODEX_HOME=") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "started" {
		t.Fatalf("trust script: %v, output %q (want only the command after it)", err, out)
	}
}

func trustHome(t *testing.T) (home, resolved string) {
	t.Helper()
	home = t.TempDir()
	real := filepath.Join(home, "proj")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	resolved, _ = filepath.EvalSymlinks(real)
	return home, resolved
}

func readTrustedFolders(t *testing.T, home string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".gemini", "trustedFolders.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("trustedFolders.json is not JSON: %v: %s", err, b)
	}
	return m
}

// TestGeminiTrustScript (I-544): the folder a gemini window starts in is
// TRUST_FOLDER in ~/.gemini/trustedFolders.json, by its resolved path,
// keeping every other folder's rule.
func TestGeminiTrustScript(t *testing.T) {
	home, resolved := trustHome(t)
	file := filepath.Join(home, ".gemini", "trustedFolders.json")

	t.Run("no file yet", func(t *testing.T) {
		runScript(t, home, geminiTrustScript("~/proj"))
		if got := readTrustedFolders(t, home)[resolved]; got != "TRUST_FOLDER" {
			t.Fatalf("trust = %v, want TRUST_FOLDER", got)
		}
		if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, want 0600", info.Mode().Perm())
		}
	})

	t.Run("keeps other keys and replaces a refusal", func(t *testing.T) {
		other := `{"` + resolved + `":"DO_NOT_TRUST","/elsewhere":"DO_NOT_TRUST","/parent/x":"TRUST_PARENT"}`
		if err := os.WriteFile(file, []byte(other), 0o600); err != nil {
			t.Fatal(err)
		}
		runScript(t, home, geminiTrustScript(`"$HOME"/link`))
		m := readTrustedFolders(t, home)
		if m[resolved] != "TRUST_FOLDER" || m["/elsewhere"] != "DO_NOT_TRUST" || m["/parent/x"] != "TRUST_PARENT" {
			t.Fatalf("rules after: %v", m)
		}
		if _, ok := m[filepath.Join(home, "link")]; ok {
			t.Fatal("the symlink's own path was trusted")
		}
	})

	t.Run("already trusted is not rewritten", func(t *testing.T) {
		before, _ := os.Stat(file)
		time.Sleep(10 * time.Millisecond)
		runScript(t, home, geminiTrustScript("~/proj"))
		after, _ := os.Stat(file)
		if !after.ModTime().Equal(before.ModTime()) || !os.SameFile(before, after) {
			t.Fatal("an already trusted folder rewrote trustedFolders.json")
		}
	})

	t.Run("invalid JSON is left alone", func(t *testing.T) {
		for _, bad := range []string{"{not json", `["a list"]`} {
			if err := os.WriteFile(file, []byte(bad), 0o600); err != nil {
				t.Fatal(err)
			}
			runScript(t, home, geminiTrustScript("~/proj"))
			if b, _ := os.ReadFile(file); string(b) != bad {
				t.Fatalf("file %q changed to %q", bad, b)
			}
		}
	})

	t.Run("a symlinked file is left alone", func(t *testing.T) {
		target := filepath.Join(home, "dotfiles-trusted.json")
		if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, file); err != nil {
			t.Fatal(err)
		}
		runScript(t, home, geminiTrustScript("~/proj"))
		if fi, err := os.Lstat(file); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("the link was replaced: %v", err)
		}
		if b, _ := os.ReadFile(target); string(b) != "{}" {
			t.Fatalf("link target changed to %q", b)
		}
	})

	t.Run("a missing folder changes nothing and still starts", func(t *testing.T) {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		runScript(t, home, geminiTrustScript("~/gone"))
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("trustedFolders.json written for a missing folder: %v", err)
		}
	})

	entries, _ := os.ReadDir(filepath.Join(home, ".gemini"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "trustedFolders.json.") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

// TestCodexTrustScript (I-544): the folder a codex window starts in gets a
// trusted [projects."<dir>"] table in ~/.codex/config.toml, appended
// after what is there, once.
func TestCodexTrustScript(t *testing.T) {
	home, resolved := trustHome(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	table := `[projects."` + resolved + `"]` + "\ntrust_level = \"trusted\"\n"

	t.Run("no file yet", func(t *testing.T) {
		runScript(t, home, codexTrustScript("~/proj"))
		b, err := os.ReadFile(cfg)
		if err != nil || strings.TrimLeft(string(b), "\n") != table {
			t.Fatalf("config.toml = %q, %v; want %q", b, err, table)
		}
		if info, _ := os.Stat(cfg); info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, want 0600", info.Mode().Perm())
		}
	})

	t.Run("appends after the user's keys, without a final newline too", func(t *testing.T) {
		user := "notify = [\"repose-hook\"]\nmodel = \"o4\"\n\n[tui]\ntheme = \"dark\""
		if err := os.WriteFile(cfg, []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
		runScript(t, home, codexTrustScript(`"$HOME"/link`))
		b, _ := os.ReadFile(cfg)
		if !strings.HasPrefix(string(b), user+"\n") || !strings.HasSuffix(string(b), "\n"+table) {
			t.Fatalf("config.toml = %q", b)
		}
		if strings.Contains(string(b), filepath.Join(home, "link")) {
			t.Fatal("the symlink's own path was trusted")
		}
	})

	t.Run("a table for the folder is left as it is", func(t *testing.T) {
		for _, user := range []string{
			table,
			"[projects.'" + resolved + "']\ntrust_level = \"untrusted\"\n",
			"[projects.\"" + resolved + "\"]\ntrust_level = \"untrusted\"\n",
		} {
			if err := os.WriteFile(cfg, []byte(user), 0o600); err != nil {
				t.Fatal(err)
			}
			runScript(t, home, codexTrustScript("~/proj"))
			if b, _ := os.ReadFile(cfg); string(b) != user {
				t.Fatalf("%q changed to %q", user, b)
			}
		}
	})

	t.Run("a symlinked config.toml is left alone", func(t *testing.T) {
		target := filepath.Join(home, "dotfiles-config.toml")
		if err := os.WriteFile(target, []byte("model = \"o4\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(cfg); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, cfg); err != nil {
			t.Fatal(err)
		}
		runScript(t, home, codexTrustScript("~/proj"))
		if fi, err := os.Lstat(cfg); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("the link was replaced: %v", err)
		}
		if b, _ := os.ReadFile(target); string(b) != "model = \"o4\"\n" {
			t.Fatalf("link target changed to %q", b)
		}
	})

	t.Run("a path TOML would need escapes for is not written", func(t *testing.T) {
		if err := os.Remove(cfg); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(home, `we"ird`), 0o755); err != nil {
			t.Fatal(err)
		}
		runScript(t, home, codexTrustScript(`~/'we"ird'`))
		runScript(t, home, codexTrustScript("~/gone"))
		if _, err := os.Stat(cfg); !os.IsNotExist(err) {
			t.Fatalf("config.toml written: %v", err)
		}
	})

	entries, _ := os.ReadDir(filepath.Join(home, ".codex"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.toml.") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

// TestAgentWindowCommandTrustsPerAgent: each agent's trust write rides in
// front of its own new-window, in the folder the window gets, and no
// agent writes another's file.
func TestAgentWindowCommandTrustsPerAgent(t *testing.T) {
	files := map[string]string{"claude": ".claude.json", "gemini": "trustedFolders.json", "codex": "config.toml"}
	for _, a := range []string{"claude", "gemini", "codex", "opencode", "pi", "cat"} {
		c := agentWindowCommand("proj", "", a, "~/proj-worktree-1", a)
		if !strings.HasSuffix(c, "tmux new-window -t proj -n "+a+" -c ~/proj-worktree-1 -d '"+a+"'") {
			t.Fatalf("%s: %s", a, c)
		}
		for agent, f := range files {
			if got, want := strings.Contains(c, f), agent == a; got != want {
				t.Fatalf("%s: mentions %s = %v, want %v: %s", a, f, got, want, c)
			}
		}
		if _, ok := files[a]; ok && !strings.Contains(c, "cd ~/proj-worktree-1 ") {
			t.Fatalf("%s trusts another folder: %s", a, c)
		}
		c = agentWindowCommand("proj", "", a, "", a)
		if _, ok := files[a]; ok && (!strings.Contains(c, `cd "$repose_co" `) || strings.Index(c, "repose_co=") > strings.Index(c, files[a])) {
			t.Fatalf("%s in the guest's checkout: %s", a, c)
		}
	}
}

// TestAgentDialogErrorNamesTheAgent: the message names the agent that
// showed the dialog, not always Claude Code.
func TestAgentDialogErrorNamesTheAgent(t *testing.T) {
	for bin, want := range map[string]string{"claude": "Claude Code is", "gemini": "Gemini CLI is", "codex": "Codex is", "cat": "cat is"} {
		var err error = &agentDialogError{agent: bin}
		var d *agentDialogError
		if !errors.As(err, &d) || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: %q, want prefix %q", bin, err.Error(), want)
		}
	}
	for _, c := range []string{
		"│ Do you trust the files in this folder?",
		"  Trust this folder? Codex can read, edit, and run files here",
	} {
		if !paneShowsDialog(c) {
			t.Errorf("paneShowsDialog(%q) = false", c)
		}
	}
}

// TestPaneRuns: Gemini CLI's pane shows node, the others their binary.
func TestPaneRuns(t *testing.T) {
	for _, c := range []struct {
		binary, current string
		want            bool
	}{
		{"gemini", "node", true},
		{"gemini", "gemini", true},
		{"gemini", "bash", false},
		{"claude", "node", false},
		{"claude", "claude", true},
		{"codex", "codex", true},
	} {
		if got := paneRuns(c.binary, c.current); got != c.want {
			t.Errorf("paneRuns(%q, %q) = %v", c.binary, c.current, got)
		}
	}
}
