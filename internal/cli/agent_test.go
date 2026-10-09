package cli

import (
	"context"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// --agent and config.toml's default_agent accept exactly the five agents
// every guest ships (features/run-and-attach.md); anything else is refused
// before a tmux window is opened for it.
func TestIsAgent(t *testing.T) {
	for _, a := range []string{"claude", "opencode", "codex", "gemini", "pi"} {
		if !isAgent(a) {
			t.Errorf("isAgent(%q) = false", a)
		}
	}
	for _, a := range []string{"", "Claude", "bash", "claude-2", "aider"} {
		if isAgent(a) {
			t.Errorf("isAgent(%q) = true", a)
		}
	}
}

// `repose run --agent aider -p "..."` is a usage error (exit 2) naming the
// five, before anything reaches the api or the guest.
func TestRunRejectsUnknownAgent(t *testing.T) {
	root := newRootCmd("test")
	root.SetArgs([]string{"run", "--agent", "aider", "-p", "fix the tests"})
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	err := root.ExecuteContext(context.Background())
	ue, ok := err.(cobraUsageError)
	if !ok {
		t.Fatalf("err = %T %v, want a usage error (exit 2)", err, err)
	}
	for _, w := range []string{"claude, opencode, codex, gemini, pi", `"aider"`} {
		if !strings.Contains(ue.Error(), w) {
			t.Errorf("message lacks %q: %s", w, ue.Error())
		}
	}
}

// config.toml's default_agent becomes a new project's agent_default; one
// that is not an agent is not sent and the api's default stands.
func TestCreateSendsDefaultAgent(t *testing.T) {
	for _, tc := range []struct{ cfg, want string }{
		{"codex", "codex"},
		{"claude", "claude"},
		{"aider", "claude"},
		{"", "claude"},
	} {
		fake := fakeapi.New(fakeapi.Options{})
		e := newLifecycleEnv(t, fake)
		e.Cfg.DefaultAgent = tc.cfg
		p, err := createProjectForRun(context.Background(), e, "", RunOptions{Name: "todo-app"}, newProgress(&discardWriter{}, false))
		fake.Close()
		if err != nil {
			t.Fatalf("default_agent %q: %v", tc.cfg, err)
		}
		if p.AgentDefault != tc.want {
			t.Errorf("default_agent %q: project agent_default = %q, want %q", tc.cfg, p.AgentDefault, tc.want)
		}
	}
}
