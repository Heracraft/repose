package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The probe finds a window's `-worktree-N` folder, its branch, tip and
// changes, and the other windows in it; the remove script removes it
// from its checkout; a window that is not there exits typeExitNoWindow
// (I-639). Run against a private tmux server and real git.
func TestStopWindowScripts(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)
	sock := t.TempDir()
	env := append(os.Environ(), "TMUX=", "TMUX_PANE=", "TMUX_TMPDIR="+sock, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
	sh := func(script string) (string, int) {
		t.Helper()
		c := exec.Command("bash", "-c", script)
		c.Dir, c.Env = base, env
		out, err := c.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	app := filepath.Join(base, "app")
	wt := filepath.Join(base, "app-worktree-1")
	if out, code := sh("git init -q -b main app && git -C app commit -q --allow-empty -m one && git -C app worktree add -q -b worktree-1 " + wt + " && git -C " + wt + " commit -q --allow-empty -m agent"); code != 0 {
		t.Fatal(out)
	}
	if out, code := sh("tmux new-session -d -s app -n shell -c " + app + " && tmux new-window -d -t =app: -n claude-2 -c " + wt + " && tmux new-window -d -t =app: -n codex -c " + app); code != 0 {
		t.Fatal(out)
	}
	defer sh("tmux kill-server")

	out, code := sh(stopWindowProbeScript("app", "claude-2"))
	w := parseStopWindowProbe(out)
	if code != 0 || w == nil || w.Dir != wt || w.Branch != "worktree-1" || w.Dirty != 0 || len(w.Others) != 0 || len(w.Tip) != 40 {
		t.Fatalf("probe: %d %q -> %+v", code, out, w)
	}
	if out, _ := sh(stopWindowProbeScript("app", "codex")); parseStopWindowProbe(out) != nil {
		t.Fatalf("the checkout is no worktree: %q", out)
	}
	if _, code := sh(stopWindowProbeScript("app", "claude-9")); code != typeExitNoWindow {
		t.Fatalf("missing window exit %d", code)
	}
	// Another window in the worktree, and an uncommitted file.
	sh("tmux new-window -d -t =app: -n claude-3 -c " + wt + " && echo x > " + wt + "/new.txt")
	out, _ = sh(stopWindowProbeScript("app", "claude-2"))
	if w := parseStopWindowProbe(out); w == nil || w.Dirty != 1 || strings.Join(w.Others, ",") != "claude-3" {
		t.Fatalf("probe with company: %q -> %+v", out, w)
	}
	if why := keptBecause(parseStopWindowProbe(out), true); why != "claude-3 is working in it" {
		t.Fatalf("kept because %q", why)
	}
	_ = os.Remove(filepath.Join(wt, "new.txt"))

	if _, code := sh(tmuxMux{}.CloseScript("app", "claude-2")); code != 0 {
		t.Fatal("close failed")
	}
	if _, code := sh(tmuxMux{}.CloseScript("app", "claude-2")); code != typeExitNoWindow {
		t.Fatalf("second close exit %d", code)
	}
	if out, code := sh(worktreeRemoveScript(wt)); code != 0 {
		t.Fatalf("remove: %s", out)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree still there")
	}
	if out, _ := sh("git -C app branch --list worktree-1"); !strings.Contains(out, "worktree-1") {
		t.Fatal("the branch went with the worktree")
	}

	// run -w types into the window.
	if _, code := sh(tmuxMux{}.TypeScript("app", "codex", "echo typed-here")); code != 0 {
		t.Fatal("type failed")
	}
	if _, code := sh(tmuxMux{}.TypeScript("app", "nobody", "x")); code != typeExitNoWindow {
		t.Fatalf("type into a missing window exit %d", code)
	}
}

func TestKeptBecause(t *testing.T) {
	w := &windowWorktree{Branch: "worktree-2", Tip: "0123456789abcdef"}
	if got := keptBecause(w, false); got != "worktree-2 is not fetched to this laptop" {
		t.Fatal(got)
	}
	if got := keptBecause(w, true); got != "" {
		t.Fatal(got)
	}
	w.Dirty = 3
	if got := keptBecause(w, true); got != "3 files not committed" {
		t.Fatal(got)
	}
}
