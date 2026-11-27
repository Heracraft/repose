package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubBin writes the commands the StartAgent script calls: a tmux that
// keeps its windows in a file, repose-checkout and repose-login-path.
func stubBin(t *testing.T, home string, session bool) (string, string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "tmux")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if session {
		if err := os.WriteFile(filepath.Join(state, "session"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tmux := `#!/bin/sh
s=` + state + `
case "$1" in
has-session) [ -e "$s/session" ] ;;
list-windows) cat "$s/windows" 2>/dev/null; exit 0 ;;
new-window)
  shift; n=; d=
  while [ $# -gt 0 ]; do case "$1" in -n) n=$2; shift ;; -c) d=$2; shift ;; esac; shift; done
  echo "$n" >> "$s/windows"; echo "$n $d $PATH" >> "$s/opened" ;;
esac
`
	files := map[string]string{
		"tmux":              tmux,
		"repose-checkout":   "#!/bin/sh\necho " + home + "/todo-app\n",
		"repose-login-path": "#!/bin/sh\necho " + bin + ":/login/path\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin, state
}

func runStartAgent(t *testing.T, bin, home, slug, agent string, rc bool) (string, int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", startAgentScript(slug, agent, rc))
	jq, _ := exec.LookPath("jq")
	path := bin + ":/usr/bin:/bin"
	if jq != "" {
		path = bin + ":" + filepath.Dir(jq) + ":/usr/bin:/bin"
	}
	cmd.Env = []string{"HOME=" + home, "PATH=" + path}
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

// TestStartAgentScript runs StartAgent's shell against stub commands
// (DECISIONS I-1702): it opens the agent's window in the checkout with
// the login PATH, answers existed the second time, writes the Remote
// Control switch only when asked, and refuses a herdr machine and a
// session that never comes.
func TestStartAgentScript(t *testing.T) {
	home := t.TempDir()
	bin, state := stubBin(t, home, true)
	out, code := runStartAgent(t, bin, home, "todo-app", "codex", false)
	if code != 0 || strings.TrimSpace(out) != "started" {
		t.Fatalf("first start: exit %d, %q", code, out)
	}
	opened, _ := os.ReadFile(filepath.Join(state, "opened"))
	if !strings.HasPrefix(string(opened), "codex "+home+"/todo-app "+bin+":/login/path") {
		t.Fatalf("opened %q, want codex in the checkout with the login PATH", opened)
	}
	if _, err := os.Stat(filepath.Join(home, ".repose", "remote-control")); !os.IsNotExist(err) {
		t.Fatalf("the Remote Control switch was written without being asked for: %v", err)
	}
	out, code = runStartAgent(t, bin, home, "todo-app", "codex", false)
	if code != 0 || strings.TrimSpace(out) != "existed" {
		t.Fatalf("second start: exit %d, %q, want existed", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "windows")); strings.Count(string(b), "codex") != 1 {
		t.Fatalf("windows %q, want one codex", b)
	}
	out, code = runStartAgent(t, bin, home, "todo-app", "claude", true)
	if code != 0 || strings.TrimSpace(out) != "started" {
		t.Fatalf("claude with remote control: exit %d, %q", code, out)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".repose", "remote-control")); err != nil || string(b) != "on\n" {
		t.Fatalf("the switch holds %q (%v), want on", b, err)
	}

	// No session within the wait: exit 3, which hostd answers no_terminal.
	// The wait is 60 s, so this case runs with a short one.
	short := strings.Replace(startAgentScript("todo-app", "pi", false), `-lt 120`, `-lt 2`, 1)
	nb, _ := stubBin(t, home, false)
	cmd := exec.Command("/bin/sh", "-c", short)
	cmd.Env = []string{"HOME=" + home, "PATH=" + nb + ":/usr/bin:/bin"}
	if err := cmd.Run(); err == nil || err.(*exec.ExitError).ExitCode() != startAgentExitNoSession {
		t.Fatalf("no session: %v, want exit %d", err, startAgentExitNoSession)
	}

	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("no jq here for the herdr case")
	}
	if err := os.WriteFile(filepath.Join(home, ".repose", "project.json"), []byte(`{"slug":"todo-app","multiplexer":"herdr"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := runStartAgent(t, bin, home, "todo-app", "gemini", false); code != startAgentExitHerdr {
		t.Fatalf("herdr: exit %d, want %d", code, startAgentExitHerdr)
	}
}
