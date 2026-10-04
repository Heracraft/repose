package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runTrust runs claudeTrustScript for dir (a shell word) with HOME set to
// home, the way startAgentWindow's ssh runs it in the guest.
func runTrust(t *testing.T, home, dir string) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	cmd := exec.Command("bash", "-c", claudeTrustScript(dir)+"echo started")
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "started" {
		t.Fatalf("trust script: %v, output %q (want only the command after it)", err, out)
	}
}

func readClaudeJSON(t *testing.T, home string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("~/.claude.json is not JSON: %v: %s", err, b)
	}
	return m
}

func trusted(m map[string]any, p string) any {
	projects, _ := m["projects"].(map[string]any)
	proj, _ := projects[p].(map[string]any)
	return proj["hasTrustDialogAccepted"]
}

// TestClaudeTrustScript (I-486): the folder an agent window starts in is
// trusted in ~/.claude.json, by its resolved path, keeping everything else.
func TestClaudeTrustScript(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "proj")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(real)

	t.Run("no file yet", func(t *testing.T) {
		runTrust(t, home, "~/proj")
		if got := trusted(readClaudeJSON(t, home), resolved); got != true {
			t.Fatalf("trust = %v, want true", got)
		}
		if info, _ := os.Stat(filepath.Join(home, ".claude.json")); info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, want 0600", info.Mode().Perm())
		}
	})

	t.Run("keeps other keys and replaces a refusal", func(t *testing.T) {
		other := `{"mcpServers":{"x":{"command":"y"}},"hasCompletedOnboarding":true,"projects":{"` + resolved + `":{"hasTrustDialogAccepted":false,"allowedTools":["Bash"]},"/elsewhere":{"hasTrustDialogAccepted":false}}}`
		if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(other), 0o600); err != nil {
			t.Fatal(err)
		}
		runTrust(t, home, `"$HOME"/link`) // through the symlink
		m := readClaudeJSON(t, home)
		if trusted(m, resolved) != true {
			t.Fatalf("not trusted: %v", m)
		}
		if trusted(m, "/elsewhere") != false || m["hasCompletedOnboarding"] != true || m["mcpServers"] == nil {
			t.Fatalf("other keys changed: %v", m)
		}
		if tools := m["projects"].(map[string]any)[resolved].(map[string]any)["allowedTools"]; tools == nil {
			t.Fatalf("project keys dropped: %v", m)
		}
		if _, ok := m["projects"].(map[string]any)[filepath.Join(home, "link")]; ok {
			t.Fatal("the symlink's own path was trusted, not the folder Claude Code resolves")
		}
	})

	t.Run("already trusted is not rewritten", func(t *testing.T) {
		p := filepath.Join(home, ".claude.json")
		before, _ := os.Stat(p)
		time.Sleep(10 * time.Millisecond)
		runTrust(t, home, "~/proj")
		after, _ := os.Stat(p)
		if !after.ModTime().Equal(before.ModTime()) || !os.SameFile(before, after) {
			t.Fatal("an already trusted folder rewrote ~/.claude.json")
		}
	})

	t.Run("invalid JSON is left alone", func(t *testing.T) {
		bad := []byte("{not json")
		if err := os.WriteFile(filepath.Join(home, ".claude.json"), bad, 0o600); err != nil {
			t.Fatal(err)
		}
		runTrust(t, home, "~/proj")
		if b, _ := os.ReadFile(filepath.Join(home, ".claude.json")); string(b) != string(bad) {
			t.Fatalf("invalid file changed to %q", b)
		}
	})

	t.Run("a missing folder changes nothing and still starts", func(t *testing.T) {
		if err := os.Remove(filepath.Join(home, ".claude.json")); err != nil {
			t.Fatal(err)
		}
		runTrust(t, home, "~/gone")
		if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
			t.Fatalf("~/.claude.json written for a missing folder: %v", err)
		}
	})

	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".claude.json.") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

// TestAgentWindowCommandTrustsOnlyForClaude: the trust write rides in
// front of claude's new-window, in the folder the window gets, and in
// front of no other agent's.
func TestAgentWindowCommandTrustsOnlyForClaude(t *testing.T) {
	c := agentWindowCommand("proj", "claude-2", "~/proj-worktree-1", "claude")
	if !strings.Contains(c, "cd ~/proj-worktree-1 ") || !strings.Contains(c, "hasTrustDialogAccepted") || !strings.HasSuffix(c, "tmux new-window -t proj -n claude-2 -c ~/proj-worktree-1 -d 'claude'") {
		t.Fatalf("claude in a worktree: %s", c)
	}
	c = agentWindowCommand("proj", "claude", "", "claude")
	if !strings.Contains(c, `cd "$repose_co" `) || strings.Index(c, "repose_co=") > strings.Index(c, "hasTrustDialogAccepted") {
		t.Fatalf("claude in the guest's checkout: %s", c)
	}
	for _, a := range []string{"codex", "opencode", "gemini", "pi", "cat"} {
		if c := agentWindowCommand("proj", a, "~/proj", a); strings.Contains(c, ".claude.json") {
			t.Fatalf("%s touches ~/.claude.json: %s", a, c)
		}
	}
}

// TestPromptNotTypedIntoTrustDialog (I-486): an agent pane that settles on
// Claude Code's trust dialog gets no prompt; startAgentWindow returns an
// *agentDialogError and the stand-in agent's input stays empty.
func TestPromptNotTypedIntoTrustDialog(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if _, err := runSSH(ctx, f.target, "tmux new-session -d -s "+testSlug+" -c ~/"+testSlug, nil); err != nil {
		t.Fatalf("tmux new-session: %v", err)
	}
	dir := filepath.Join(f.guestHome, "bin")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got := filepath.Join(f.guestHome, "got")
	// Named cat, so pane_current_command settles on "cat" once it execs
	// the real one.
	script := filepath.Join(dir, "cat")
	body := "#!/bin/sh\n" +
		"echo ' Quick safety check: Is this a project you created or one you trust?'\n" +
		"echo ' > No, exit'\n" +
		"echo '   Yes, I trust this folder'\n" +
		"exec env cat > " + got + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	// The settled path: pane_current_command is "cat" and the capture
	// stops changing.
	if _, err := runSSH(ctx, f.target, "tmux new-window -t "+testSlug+" -n first -c ~/"+testSlug+" -d "+shQuote(script), nil); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := waitPaneIdle(ctx, f.target, testSlug, "first", "cat", nil); err == nil {
		t.Fatal("waitPaneIdle said the dialog was ready for a prompt")
	} else if _, ok := err.(*agentDialogError); !ok {
		t.Fatalf("waitPaneIdle err = %v, want *agentDialogError", err)
	}
	if waited := time.Since(start); waited > 10*time.Second {
		t.Fatalf("waitPaneIdle took %s on a settled dialog", waited)
	}
	// The timeout path, through startAgentWindow: the binary named here
	// (the script's path) never matches pane_current_command, as when
	// tmux shows a wrapper's name, and the deadline still checks.
	defer func(d time.Duration) { paneIdleTimeout = d }(paneIdleTimeout)
	paneIdleTimeout = 2 * time.Second
	err := startAgentWindow(ctx, f.target, testSlug, "cat", "~/"+testSlug, script, "the prompt", false, nil)
	if err == nil {
		t.Fatal("the prompt was sent into the trust dialog")
	}
	if _, ok := err.(*agentDialogError); !ok {
		t.Fatalf("err = %v, want *agentDialogError", err)
	}
	time.Sleep(300 * time.Millisecond)
	if b, _ := os.ReadFile(got); len(b) != 0 {
		t.Fatalf("the agent got input %q", b)
	}
}

func TestPaneShowsDialog(t *testing.T) {
	for capture, want := range map[string]bool{
		"Do you trust the files in this folder?\n> Yes, proceed": true,
		"   Yes, I trust this folder":                            true,
		"> \n  bypass permissions on":                            false,
		"":                                                       false,
	} {
		if got := paneShowsDialog(capture); got != want {
			t.Errorf("paneShowsDialog(%q) = %v, want %v", capture, got, want)
		}
	}
}
