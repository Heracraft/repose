package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/machine"
)

// spineEnv is an Env with a home and a configuration directory of its
// own, so a spine test works on a real registry and project map without
// touching the laptop's.
func spineEnv(t *testing.T) (*Env, *machine.FileRegistry, *discardWriter, *discardWriter) {
	t.Helper()
	home := withHome(t)
	dir := filepath.Join(home, ".config", "repose")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	out, errOut := &discardWriter{}, &discardWriter{}
	e := &Env{
		Dir:     dir,
		Cwd:     filepath.Join(home, "code", "todo-app"),
		HomeDir: home,
		Out:     out,
		ErrOut:  errOut,
	}
	return e, machine.NewRegistry(dir), out, errOut
}

func testMachine() machine.Machine {
	return machine.Machine{Name: "homebox", Address: "192.168.1.40", Login: "me", UndoPath: defaultUndoPath}
}

// addMachine puts one machine in the registry.
func addMachine(t *testing.T, reg *machine.FileRegistry, m machine.Machine) {
	t.Helper()
	if err := reg.Add(m); err != nil {
		t.Fatalf("add %s: %v", m.Name, err)
	}
}

// recordPlace records a project under remote and dir, as a run against a
// machine does.
func recordPlace(t *testing.T, e *Env, remote, dir string, pr machine.Project) {
	t.Helper()
	err := machine.UpdateProjects(e.Dir, func(p *machine.Projects) error {
		p.Remember(remote, dir, pr)
		return nil
	})
	if err != nil {
		t.Fatalf("record %s: %v", pr.Slug, err)
	}
}

// fakeAgent replaces the ssh that carries repose-agent's commands: it
// records each remote command and answers with the JSON keyed by the
// subcommand words it starts with.
func fakeAgent(t *testing.T, answers map[string]string) *[]string {
	t.Helper()
	var mu sync.Mutex
	var sent []string
	old := runOnMachine
	runOnMachine = func(_ context.Context, _ sshTarget, remoteCmd string, _ io.Reader) ([]byte, error) {
		mu.Lock()
		sent = append(sent, remoteCmd)
		mu.Unlock()
		// The words as the caller meant them, without the quoting
		// agentRun adds.
		rest := strings.ReplaceAll(strings.TrimPrefix(remoteCmd, reposeAgent+" "), "'", "")
		keys := make([]string, 0, len(answers))
		for k := range answers {
			keys = append(keys, k)
		}
		// The longest matching key wins, so "point rewind" is not
		// answered by "point".
		sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
		for _, k := range keys {
			if strings.HasPrefix(rest, k) {
				return []byte(answers[k]), nil
			}
		}
		return nil, &sshError{ExitCode: 127, Stderr: "no such command"}
	}
	t.Cleanup(func() { runOnMachine = old })
	return &sent
}

func TestPlaceForResolvesACheckoutToItsMachine(t *testing.T) {
	for _, c := range []struct {
		name       string
		projectArg string
		remote     string // the keys the place is recorded under
		dir        string
		place      machine.Project
		deps       resolveDeps
		wantSlug   string
		wantCode   int
	}{
		{
			name:     "by the checkout's remote",
			remote:   "git@github.com:me/todo-app.git",
			place:    machine.Project{Machine: "homebox", Slug: "todo-app"},
			deps:     resolveDeps{RemoteFor: func(string) string { return "git@github.com:me/todo-app.git" }},
			wantSlug: "todo-app",
		},
		{
			name:     "by the repository root, from a subdirectory",
			dir:      "/home/me/code/todo-app",
			place:    machine.Project{Machine: "homebox", Slug: "todo-app"},
			deps:     resolveDeps{RootFor: func(string) string { return "/home/me/code/todo-app" }},
			wantSlug: "todo-app",
		},
		{
			name:       "by the project named",
			projectArg: "blog",
			dir:        "/elsewhere",
			place:      machine.Project{Machine: "homebox", Slug: "blog"},
			wantSlug:   "blog",
		},
		{
			name:     "a checkout on no machine",
			wantCode: ExitGuestNotRunning,
		},
		{
			name:       "a project on no machine",
			projectArg: "blog",
			wantCode:   ExitGuestNotRunning,
		},
		{
			name:     "a machine that was removed",
			dir:      "/home/me/code/todo-app",
			place:    machine.Project{Machine: "shed", Slug: "todo-app"},
			deps:     resolveDeps{RootFor: func(string) string { return "/home/me/code/todo-app" }},
			wantCode: ExitGuestNotRunning,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, reg, _, _ := spineEnv(t)
			addMachine(t, reg, testMachine())
			if c.place.Slug != "" {
				recordPlace(t, e, c.remote, c.dir, c.place)
			}
			m, place, err := placeFor(e, c.projectArg, c.deps)
			if c.wantCode != 0 {
				if exitCodeOf(err) != c.wantCode {
					t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), c.wantCode, err)
				}
				if !strings.HasSuffix(err.Error(), ".") || strings.Contains(err.Error(), "\n") {
					t.Errorf("refusal is not one sentence: %q", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("placeFor: %v", err)
			}
			if place.Slug != c.wantSlug || m.Name != "homebox" {
				t.Fatalf("resolved %s on %s, want %s on homebox", place.Slug, m.Name, c.wantSlug)
			}
		})
	}
}

func TestCheckoutKeysPreferTheRemote(t *testing.T) {
	e, _, _, _ := spineEnv(t)
	deps := resolveDeps{
		RemoteFor: func(string) string { return "git@github.com:me/todo-app.git" },
		RootFor:   func(string) string { return "/home/me/code/todo-app" },
	}
	remote, dir := checkoutKeys(e, deps)
	if remote != "git@github.com:me/todo-app.git" || dir != "/home/me/code/todo-app" {
		t.Errorf("checkoutKeys = %q, %q", remote, dir)
	}
	// With no git around, the working directory is the whole key.
	remote, dir = checkoutKeys(e, resolveDeps{})
	if remote != "" || dir != e.Cwd {
		t.Errorf("checkoutKeys with no git = %q, %q", remote, dir)
	}
}

func TestRememberPlaceRecordsBothKeys(t *testing.T) {
	e, _, _, _ := spineEnv(t)
	deps := resolveDeps{RemoteFor: func(string) string { return "git@github.com:me/todo-app.git" }}
	place := machine.Project{Machine: "homebox", Slug: "todo-app"}
	if err := rememberPlace(e, deps, place); err != nil {
		t.Fatal(err)
	}
	projects, err := machine.LoadProjects(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if pr, ok := projects.Resolve("git@github.com:me/todo-app.git", ""); !ok || pr != place {
		t.Errorf("by remote: %+v, %v", pr, ok)
	}
	if pr, ok := projects.Resolve("", e.Cwd); !ok || pr != place {
		t.Errorf("by directory: %+v, %v", pr, ok)
	}
}

func TestProjectAliasesReadsTheGatewayBlocks(t *testing.T) {
	spineEnv(t)
	sd, err := sshDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg := renderSSHConfig([]Project{{ID: "p1", Slug: "todo-app"}, {ID: "p2", Slug: "blog"}}, "heracraft", "~", true)
	if err := os.WriteFile(filepath.Join(sd, sshHostsName), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	got := projectAliases()
	sort.Strings(got)
	if strings.Join(got, ",") != "blog,todo-app" {
		t.Errorf("projectAliases = %v", got)
	}
}

func TestPointAge(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{20 * time.Second, "less than a minute ago"},
		{90 * time.Second, "1 minute ago"},
		{14 * time.Minute, "14 minutes ago"},
		{59*time.Minute + 59*time.Second, "59 minutes ago"},
		{90 * time.Minute, "1 hour ago"},
		{2 * time.Hour, "2 hours ago"},
		{25 * time.Hour, "1 day ago"},
		{72 * time.Hour, "3 days ago"},
	} {
		if got := pointAge(c.d); got != c.want {
			t.Errorf("pointAge(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestSizeWords(t *testing.T) {
	for _, c := range []struct {
		n    uint64
		want string
	}{
		{512, "512 B"},
		{40 << 30, "40 GB"},
		{2 << 20, "2 MB"},
	} {
		if got := sizeWords(c.n); got != c.want {
			t.Errorf("sizeWords(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestAgentRunQuotesItsArgumentsAndReadsTheResult(t *testing.T) {
	sent := fakeAgent(t, map[string]string{
		"point list": `{"points":[{"name":"r-0199a1c2","project":"todo-app","reason":"run","taken_at":"2026-10-19T10:00:00Z","bytes":109051904}]}`,
	})
	var points agentPoints
	if err := agentRun(context.Background(), sshTarget{}, &points, "point", "list", "--project", "todo-app"); err != nil {
		t.Fatalf("agentRun: %v", err)
	}
	if len(*sent) != 1 || (*sent)[0] != reposeAgent+" 'point' 'list' '--project' 'todo-app'" {
		t.Fatalf("sent %q", *sent)
	}
	if len(points.Points) != 1 || points.Points[0].Name != "r-0199a1c2" || points.Points[0].Bytes != 109051904 {
		t.Fatalf("points %+v", points.Points)
	}
	if !points.Points[0].TakenAt.Equal(time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("taken_at %s", points.Points[0].TakenAt)
	}
}

func TestAgentRunReadsTheErrorObject(t *testing.T) {
	old := runOnMachine
	runOnMachine = func(context.Context, sshTarget, string, io.Reader) ([]byte, error) {
		return []byte(`{"error":{"code":"busy","message":"the project is not quiesced"}}`), &sshError{ExitCode: 1}
	}
	t.Cleanup(func() { runOnMachine = old })
	err := agentRun(context.Background(), sshTarget{}, nil, "point", "rewind")
	var ae *agentError
	if !errors.As(err, &ae) {
		t.Fatalf("err is %T: %v", err, err)
	}
	if ae.Code != agentCodeBusy || !strings.Contains(ae.Error(), "not quiesced") {
		t.Fatalf("agentError %+v", ae)
	}
}

func TestMachineTargetNamesItsOwnConfig(t *testing.T) {
	spineEnv(t)
	configPath, err := machinesConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(configPath, filepath.Join(".ssh", "repose", machine.SSHName)) {
		t.Errorf("config path %q", configPath)
	}
	got := machineTarget(configPath, testMachine()).Args
	want := []string{"-F", configPath, "homebox.repose"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("ssh args %v, want %v", got, want)
	}
}
