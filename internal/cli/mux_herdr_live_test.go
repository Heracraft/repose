package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHerdrScriptsAgainstHerdr runs the guest-side herdr scripts with
// bash against a real herdr server (REPOSE_TEST_HERDR names the binary;
// the test is skipped without it), the way the guest runs them over ssh:
// the state listing, workspace create, tab create, agent start, agent
// prompt, pane send-text, worktree open, the focus step, the session
// check and the message. A stand-in `claude` on PATH plays the agent and
// records what is typed into it.
func TestHerdrScriptsAgainstHerdr(t *testing.T) {
	bin := os.Getenv("REPOSE_TEST_HERDR")
	if bin == "" {
		t.Skip("REPOSE_TEST_HERDR is not set")
	}
	// A short home: herdr's socket lives under it, and a unix socket path
	// has about 100 bytes.
	home, err := os.MkdirTemp("/tmp", "rh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	binDir := filepath.Join(home, "bin")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(binDir, 0o755))
	raw, err := os.ReadFile(bin)
	must(err)
	must(os.WriteFile(filepath.Join(binDir, "herdr"), raw, 0o755))
	typed := filepath.Join(home, "typed.log")
	must(os.WriteFile(filepath.Join(binDir, "claude"), []byte(`#!/usr/bin/env bash
printf '\033[?1049h'
echo "Claude Code (stand-in)"
echo "> "
while IFS= read -r l; do printf '%s\n' "$l" >> `+typed+`; echo "> "; done
`), 0o755))
	env := append(filterEnvKeys(os.Environ(), "HOME", "XDG_CONFIG_HOME", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_SESSION", "TMUX"),
		"HOME="+home, "XDG_CONFIG_HOME="+home+"/.config", "PATH="+binDir+":"+os.Getenv("PATH"))
	run := func(script string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "bash", "-c", script)
		cmd.Env, cmd.Dir = env, home
		out, err := cmd.CombinedOutput()
		t.Logf("$ %s\n%s", scriptHead(script), out)
		return string(out), err
	}
	srv := exec.Command(filepath.Join(binDir, "herdr"), "server")
	srv.Env, srv.Dir = env, home
	must(srv.Start())
	t.Cleanup(func() {
		_, _ = run("herdr server stop")
		_ = srv.Process.Kill()
		_ = srv.Wait()
	})
	sock := filepath.Join(home, ".config", "herdr", "herdr.sock")
	for i := 0; ; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if i > 100 {
			t.Fatal("herdr made no socket")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if out, err := run("herdr --version"); err != nil {
		t.Fatal(out)
	}

	// The checkout, as a sync leaves it.
	must(os.MkdirAll(filepath.Join(home, ".repose"), 0o755))
	must(os.WriteFile(filepath.Join(home, ".repose", "checkout"), []byte("todo-app\n"), 0o644))
	if out, err := run(`set -e; git init -q -b main todo-app; cd todo-app; git -c user.email=a@b -c user.name=a commit -q --allow-empty -m init`); err != nil {
		t.Fatal(out)
	}

	// The first boot came before the checkout: the server made `home`
	// there, and a base before I-597 then labelled the checkout's
	// workspace with the folder's name. The tidy step (the sync's, and the
	// prompt's below) renames that one to `checkout` and closes `home`
	// once it is an idle shell, never while it runs something (I-596,
	// I-597).
	labels := func() string {
		t.Helper()
		out, err := run(`herdr workspace list | jq -r '[.result.workspaces[].label] | join(",")'`)
		must(err)
		return strings.TrimSpace(out)
	}
	out, err := run(`herdr workspace create --cwd "$HOME" --label home --no-focus | jq -r '.result.root_pane.pane_id'`)
	must(err)
	homePane := strings.TrimSpace(out)
	_, err = run(`herdr workspace create --cwd "$HOME/todo-app" --label todo-app --no-focus`)
	must(err)
	_, err = run(`herdr pane send-text ` + homePane + ` 'sleep 300' && herdr pane send-keys ` + homePane + ` Enter`)
	must(err)
	herdrWaitFor(t, func() bool {
		out, _ := run(`herdr pane process-info --pane ` + homePane + ` | jq -r '.result.process_info | .foreground_process_group_id != .shell_pid'`)
		return strings.TrimSpace(out) == "true"
	})
	tidy := herdrMainVar("todo-app") + herdrRenameShell + herdrTidyShell
	_, err = run(tidy)
	must(err)
	if got := labels(); got != "home,checkout" {
		t.Fatalf("with a busy home: %q", got)
	}
	_, err = run(`herdr pane send-text ` + homePane + ` "$(printf '\003')"`)
	must(err)
	herdrWaitFor(t, func() bool {
		out, _ := run(`herdr pane process-info --pane ` + homePane + ` | jq -r '.result.process_info | .foreground_process_group_id == .shell_pid'`)
		return strings.TrimSpace(out) == "true"
	})
	// The old label again (a start on a base before I-597): the prompt's
	// script renames nothing (checkout is there) and closes the idle
	// duplicate and home.
	_, err = run(`herdr workspace create --cwd "$HOME/todo-app" --label todo-app --no-focus`)
	must(err)

	out, err = run(herdrStateScript("todo-app", ""))
	must(err)
	st, err := parseHerdrState(out)
	must(err)
	if st.Label != herdrCheckoutLabel || st.Alias != "todo-app" || len(st.Agents) != 0 {
		t.Fatalf("empty state %+v", st)
	}
	name, others := st.pick("claude")
	if name != "claude" || others {
		t.Fatalf("pick %q %v", name, others)
	}

	// Workspace create, tab create, agent start.
	out, err = run(herdrStartScript(agentStart{Slug: "todo-app", Agent: "claude", Name: "claude"}, ""))
	must(err)
	if marker(out, "pane") == "" || marker(out, "start") != "idle" {
		t.Fatalf("start: %q", out)
	}
	if got := labels(); got != "checkout" {
		t.Fatalf("after the first prompt: %q", got)
	}
	// Agent prompt: the stand-in shows no working state, so herdr says
	// stalled; the text is typed all the same.
	out, err = run(herdrPromptScript("claude", "say done"))
	must(err)
	if p := marker(out, "prompt"); p != "working" && p != "agent_prompt_stalled" && p != "idle" {
		t.Fatalf("prompt: %q", out)
	}
	herdrWaitFor(t, func() bool { b, _ := os.ReadFile(typed); return strings.Contains(string(b), "say done") })

	out, err = run(herdrStateScript("todo-app", ""))
	must(err)
	st, err = parseHerdrState(out)
	must(err)
	rows := herdrPsRows(st)
	if len(rows) != 1 || rows[0].Workspace != herdrCheckoutLabel || rows[0].Agent != "claude" || rows[0].Name != "claude" {
		t.Fatalf("ps rows %+v", rows)
	}
	if name, others := st.pick("claude"); name != "claude-2" || !others {
		t.Fatalf("second pick %q %v", name, others)
	}

	// The worktree probe lists herdr's agent names in its one ssh.
	out, err = run(worktreeProbeScript("todo-app", "", herdrMux{}.NamesScript("todo-app")))
	must(err)
	if !strings.Contains(out, "#window claude\n") || !strings.Contains(out, "#checkout todo-app\n") {
		t.Fatalf("worktree probe: %q", out)
	}

	// pane send-text into the agent's pane, and into the focused pane.
	for _, w := range []string{"claude", ""} {
		out, err = run(`f=/tmp/repose-paste/x.png` + "\n" + herdrMux{}.PasteScript("todo-app", w))
		must(err)
		if w == "claude" && strings.TrimSpace(out) != "claude" {
			t.Fatalf("paste into claude printed %q", out)
		}
	}
	if _, err := run(`f=/x` + "\n" + herdrMux{}.PasteScript("todo-app", "nobody")); err == nil || herdrExit(err) != pasteExitNoPane {
		t.Fatalf("paste to no agent: %v", err)
	}

	// worktree open groups the worktree under the repository.
	if out, err := run(`cd todo-app && git worktree add -q -b worktree-1 ../todo-app-worktree-1 HEAD`); err != nil {
		t.Fatal(out)
	}
	out, err = run(herdrStartScript(agentStart{Slug: "todo-app", Agent: "claude", Name: "claude-2", Dir: "~/todo-app-worktree-1", Worktree: true}, ""))
	must(err)
	if marker(out, "start") != "idle" {
		t.Fatalf("worktree start: %q", out)
	}
	out, err = run(`herdr workspace list | jq -c '[.result.workspaces[] | {label, repo: .worktree.repo_name, linked: .worktree.is_linked_worktree}]'`)
	must(err)
	if !strings.Contains(out, `{"label":"todo-app-worktree-1","repo":"todo-app","linked":true}`) {
		t.Fatalf("worktree workspace: %s", out)
	}

	// The Claude Code login (attach only): the tab is focused, since
	// `herdr agent focus` finds no agent name on a `pane run` pane.
	out, err = run(herdrStartScript(agentStart{Slug: "todo-app", Agent: "claude", Name: "claude-3", AttachOnly: true}, ""))
	must(err)
	loginPane := marker(out, "pane")
	if _, err := run(herdrFocusScript("todo-app", "", "claude-3")); err != nil {
		t.Fatal(err)
	}
	out, err = run(`w=$(herdr workspace list | jq -r 'first(.result.workspaces[] | select(.focused)) | .workspace_id')
herdr pane list | jq -r --arg w "$w" 'first(.result.panes[] | select(.focused and .workspace_id == $w)) | .pane_id'`)
	must(err)
	if loginPane == "" || strings.TrimSpace(out) != loginPane {
		t.Fatalf("focused pane %q, the login tab's %q", out, loginPane)
	}

	// Another checkout's workspace, made and focused by the attach.
	must(os.MkdirAll(filepath.Join(home, "notes"), 0o755))
	if out, err := run(herdrFocusScript("todo-app", "notes", "claude")); err != nil {
		t.Fatal(out)
	}
	out, err = run(`herdr workspace list | jq -r '.result.workspaces[] | select(.focused) | .label'`)
	must(err)
	if strings.TrimSpace(out) != "notes" && strings.TrimSpace(out) != herdrCheckoutLabel {
		t.Fatalf("focused %q", out)
	}
	if _, err := run(herdrFocusScript("todo-app", "missing", "")); herdrExit(err) != 2 {
		t.Fatalf("missing checkout: %v", err)
	}

	_, err = run(herdrMux{}.MessageScript("todo-app", "Time zone set"))
	must(err)

	// The session check's command: panes left, so not over.
	out, err = run(`herdr pane list | jq -r '.result.panes | length'`)
	must(err)
	if strings.TrimSpace(out) == "0" {
		t.Fatal("no panes")
	}
}

func filterEnvKeys(env []string, drop ...string) []string {
	var out []string
outer:
	for _, kv := range env {
		for _, d := range drop {
			if strings.HasPrefix(kv, d+"=") {
				continue outer
			}
		}
		out = append(out, kv)
	}
	return out
}

func scriptHead(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

func herdrExit(err error) int {
	if xe, ok := err.(*exec.ExitError); ok {
		return xe.ExitCode()
	}
	return -1
}

func herdrWaitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out")
}
