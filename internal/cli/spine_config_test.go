package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/machine"
	"github.com/heracraft/repose/internal/menu"
)

// spineConfigEnv is an Env with one machine and this checkout recorded on
// it, which is what every `repose config` command on a machine the user
// owns resolves through. The working directory exists, because `config
// edit` writes the project's repose.nix in it.
func spineConfigEnv(t *testing.T) (*Env, *strings.Builder, *strings.Builder) {
	t.Helper()
	e, reg, _, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	recordPlace(t, e, "", e.Cwd, testPlace())
	if err := os.MkdirAll(e.Cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	out, errOut := &strings.Builder{}, &strings.Builder{}
	e.Out, e.ErrOut = out, errOut
	return e, out, errOut
}

func testPlace() machine.Project {
	return machine.Project{Machine: "homebox", Slug: "todo-app"}
}

// envApplied is `env apply`'s answer with one package installed, which is
// the shape the add and apply tests work from.
const envApplied = `{"profile":"/var/lib/repose/undo/projects/todo-app/.repose/profile","added":["air"],"present":[],"skipped":[],"ms":2100}`

func TestSpineConfigShowListsTheProfile(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer string
		want   string
	}{
		{
			name:   "what the profile holds",
			answer: `{"profile":"/p","packages":[{"name":"air-1.62.0","attr":"air"},{"name":"gcc-14.2.0","attr":"gcc"}]}`,
			want:   "air-1.62.0\ngcc-14.2.0\n",
		},
		{
			name:   "an environment that was never applied",
			answer: `{"profile":"/p","packages":[]}`,
			want:   "todo-app on homebox has no packages of its own.\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, out, _ := spineConfigEnv(t)
			fakeAgent(t, map[string]string{"env show": c.answer})
			handled, err := spineConfigShow(context.Background(), e, "", false)
			if !handled || err != nil {
				t.Fatalf("handled %v, err %v", handled, err)
			}
			if out.String() != c.want {
				t.Errorf("stdout %q, want %q", out.String(), c.want)
			}
		})
	}
}

// What `config add` sends is the nixpkgs attributes the names stand for,
// in one apply beside the project's own file.
func TestSpineConfigAddInstallsIntoTheProfile(t *testing.T) {
	e, out, _ := spineConfigEnv(t)
	sent := fakeAgent(t, map[string]string{"env apply": envApplied})
	handled, err := spineConfigAdd(context.Background(), e, "", []string{"air"})
	if !handled || err != nil {
		t.Fatalf("handled %v, err %v", handled, err)
	}
	if out.String() != "Added air to todo-app on homebox.\n" {
		t.Errorf("stdout %q", out.String())
	}
	want := reposeAgent + " 'env' 'apply' '--project' 'todo-app' '--add' 'air'"
	if len(*sent) != 1 || (*sent)[0] != want {
		t.Errorf("sent %q, want %q", *sent, want)
	}
}

// A menu entry is the packages it installs: its options at their
// defaults, and nothing of what only a host can do.
func TestCatalogAttrs(t *testing.T) {
	cat, err := menu.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id   string
		want []string
	}{
		{id: "bun", want: []string{"bun"}},
		{id: "nodejs", want: []string{"nodejs_22"}},
		{id: "go-tools", want: []string{"gopls", "golangci-lint", "delve"}},
		{id: "postgresql", want: []string{"postgresql_16"}},
		{id: "memcached", want: nil},
		{id: "vercel", want: nil},
	} {
		t.Run(c.id, func(t *testing.T) {
			entry, ok := cat.Entry(c.id)
			if !ok {
				t.Fatalf("the catalog has no %s", c.id)
			}
			got, err := catalogAttrs(cat, entry)
			if err != nil {
				t.Fatalf("catalogAttrs: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("catalogAttrs(%s) = %v, want %v", c.id, got, c.want)
			}
		})
	}
}

func TestSpineConfigAddResolvesTheNames(t *testing.T) {
	for _, c := range []struct {
		name     string
		args     []string
		answer   string
		wantAdd  string
		wantOut  string
		wantCode int
		wantErr  string
	}{
		{
			name:    "a menu entry's service is the box's own to run",
			args:    []string{"postgresql"},
			answer:  `{"profile":"/p","added":["postgresql_16"],"present":[],"skipped":[],"ms":9000}`,
			wantAdd: "'--add' 'postgresql_16'",
			wantOut: "postgresql's service is homebox's own to run; its package is what goes in.\n" +
				"Added postgresql_16 to todo-app on homebox.\n",
		},
		{
			name:    "a menu entry that stands for several packages",
			args:    []string{"go-tools"},
			answer:  `{"profile":"/p","added":["gopls","golangci-lint","delve"],"present":[],"skipped":[],"ms":9000}`,
			wantAdd: "'--add' 'gopls,golangci-lint,delve'",
			wantOut: "Added gopls, golangci-lint and delve to todo-app on homebox.\n",
		},
		{
			name:     "a menu entry a profile cannot install",
			args:     []string{"vercel"},
			wantCode: ExitUsage,
			wantErr:  "vercel is a menu entry a machine repose runs builds, and it installs no package; on homebox there is nothing to add for it.",
		},
		{
			name:     "a name nixpkgs cannot have",
			args:     []string{"gcc!"},
			wantCode: ExitUsage,
			wantErr:  `"gcc!" is not a nixpkgs attribute name (letters, digits, _ - + and dots, like gcc or python312Packages.black).`,
		},
		{
			name:     "no names at all, which is the menu's own listing",
			args:     nil,
			wantCode: 0,
			wantOut:  "",
			wantAdd:  "",
			wantErr:  "",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, out, _ := spineConfigEnv(t)
			sent := fakeAgent(t, map[string]string{"env apply": c.answer})
			handled, err := spineConfigAdd(context.Background(), e, "", c.args)
			if len(c.args) == 0 {
				if handled || err != nil {
					t.Fatalf("handled %v, err %v", handled, err)
				}
				return
			}
			if !handled {
				t.Fatal("a project on a machine the user owns was left to the api")
			}
			if exitCodeOf(err) != c.wantCode {
				t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), c.wantCode, err)
			}
			if c.wantErr != "" && err.Error() != c.wantErr {
				t.Errorf("refusal %q", err.Error())
			}
			if out.String() != c.wantOut {
				t.Errorf("stdout %q, want %q", out.String(), c.wantOut)
			}
			if c.wantAdd == "" {
				for _, cmd := range *sent {
					if strings.Contains(cmd, "'env' 'apply'") {
						t.Errorf("a refused add applied something: %s", cmd)
					}
				}
				return
			}
			if len(*sent) != 1 || !strings.Contains((*sent)[0], c.wantAdd) {
				t.Errorf("sent %q, want one apply with %s", *sent, c.wantAdd)
			}
		})
	}
}

// What the box says it did is what the user reads: the package that was
// there already, the one the file asked for beside it, and the one that
// would not go in.
func TestSpineConfigAddReportsWhatTheBoxDid(t *testing.T) {
	for _, c := range []struct {
		name     string
		answer   string
		wantOut  string
		wantCode int
	}{
		{
			name:    "already there",
			answer:  `{"profile":"/p","added":[],"present":["air"],"skipped":[],"ms":40}`,
			wantOut: "air is already in todo-app.\n",
		},
		{
			name:   "the project's own file went in with it",
			answer: `{"profile":"/p","added":["air","gcc"],"present":[],"skipped":[],"ms":9000}`,
			wantOut: "Added air to todo-app on homebox.\n" +
				"Installed gcc from repose.nix too.\n",
		},
		{
			name:   "what a profile cannot hold is named",
			answer: `{"profile":"/p","added":["air"],"present":[],"skipped":[{"name":"programs.*","reason":"a home-manager module, which a profile has not"}],"ms":9000}`,
			wantOut: "Added air to todo-app on homebox.\n" +
				"programs.* was not applied: a home-manager module, which a profile has not.\n",
		},
		{
			name:     "nothing of what was asked for went in",
			answer:   `{"profile":"/p","added":[],"present":[],"skipped":[{"name":"air","reason":"nixpkgs has no package \"air\""}],"ms":900}`,
			wantOut:  "air was not applied: nixpkgs has no package \"air\".\n",
			wantCode: ExitBuildFailed,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, out, _ := spineConfigEnv(t)
			fakeAgent(t, map[string]string{"env apply": c.answer})
			handled, err := spineConfigAdd(context.Background(), e, "", []string{"air"})
			if !handled {
				t.Fatal("a project on a machine the user owns was left to the api")
			}
			if exitCodeOf(err) != c.wantCode {
				t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), c.wantCode, err)
			}
			if out.String() != c.wantOut {
				t.Errorf("stdout %q, want %q", out.String(), c.wantOut)
			}
		})
	}
}

// `config apply` with no file applies the project's own repose.nix on the
// box again.
func TestSpineConfigApplyAppliesTheProjectsFile(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer string
		want   string
	}{
		{
			name:   "the box did the work",
			answer: `{"profile":"/p","added":["gcc"],"present":[],"skipped":[],"ms":12000}`,
			want:   "Installed gcc for todo-app on homebox.\n",
		},
		{
			name:   "nothing to do",
			answer: `{"profile":"/p","added":[],"present":["gcc"],"skipped":[],"ms":40}`,
			want:   "Nothing to install; todo-app on homebox has what it asks for.\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, out, _ := spineConfigEnv(t)
			sent := fakeAgent(t, map[string]string{"env apply": c.answer})
			handled, err := spineConfigApply(context.Background(), e, "", "")
			if !handled || err != nil {
				t.Fatalf("handled %v, err %v", handled, err)
			}
			if out.String() != c.want {
				t.Errorf("stdout %q, want %q", out.String(), c.want)
			}
			if len(*sent) != 1 || strings.Contains((*sent)[0], "--add") {
				t.Errorf("sent %q", *sent)
			}
		})
	}
}

// `config edit` opens the checkout's own file and says what sends it,
// because nothing else here would.
func TestSpineConfigEditEditsTheCheckoutsFile(t *testing.T) {
	for _, c := range []struct {
		name  string
		write string
		want  string
	}{
		{
			name:  "a file that changed",
			write: "{ pkgs, ... }:\n{ home.packages = [ pkgs.gcc ]; }\n",
			want:  "repose.nix is edited here; it reaches homebox with the next run.\n",
		},
		{
			name: "an editor that saved nothing",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, out, _ := spineConfigEnv(t)
			fakeAgent(t, nil)
			path := ""
			handled, err := spineConfigEdit(e, "", func(p string) error {
				path = p
				if c.write == "" {
					return nil
				}
				return os.WriteFile(p, []byte(c.write), 0o600)
			})
			if !handled || err != nil {
				t.Fatalf("handled %v, err %v", handled, err)
			}
			if path != filepath.Join(e.Cwd, repoConfigName) {
				t.Errorf("the editor was given %q", path)
			}
			if out.String() != c.want {
				t.Errorf("stdout %q, want %q", out.String(), c.want)
			}
		})
	}
}

// What a host or a server of ours answers for refuses in one sentence
// rather than asking the api about a project it has never heard of.
func TestSpineConfigRefusesWhatABoxCannotDo(t *testing.T) {
	for _, c := range []struct {
		name string
		call func(e *Env) (bool, error)
		code int
		want string
	}{
		{
			name: "config remove",
			call: func(e *Env) (bool, error) { return spineConfigRemove(e, "") },
			code: ExitGuestNotRunning,
			want: "A package is not taken out again on a machine you own; `repose rewind` takes todo-app on homebox back to a point before it went in.",
		},
		{
			name: "config revisions",
			call: func(e *Env) (bool, error) { return spineConfigRevisions(e, "") },
			code: ExitGuestNotRunning,
			want: "Revisions are a machine repose runs; todo-app is on homebox, where `repose rewind` takes its packages back with its files.",
		},
		{
			name: "config show --revisions",
			call: func(e *Env) (bool, error) { return spineConfigShow(context.Background(), e, "", true) },
			code: ExitGuestNotRunning,
			want: "Revisions are a machine repose runs; todo-app is on homebox, where `repose rewind` takes its packages back with its files.",
		},
		{
			name: "config --global on",
			call: func(e *Env) (bool, error) { return spinePersonalSwitch(e, "") },
			code: ExitGuestNotRunning,
			want: "machine.nix is for a machine repose runs; todo-app is on homebox, where the project's repose.nix is the whole of its configuration.",
		},
		{
			name: "config apply with a file",
			call: func(e *Env) (bool, error) { return spineConfigApply(context.Background(), e, "", "./other.nix") },
			code: ExitUsage,
			want: "A file is not sent to a machine you own; homebox builds todo-app from the repose.nix in its checkout, which `repose run` sends.",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, out, _ := spineConfigEnv(t)
			sent := fakeAgent(t, nil)
			handled, err := c.call(e)
			if !handled {
				t.Fatal("a project on a machine the user owns was left to the api")
			}
			if exitCodeOf(err) != c.code || err.Error() != c.want {
				t.Fatalf("exit %d: %q", exitCodeOf(err), err.Error())
			}
			if out.String() != "" || len(*sent) != 0 {
				t.Errorf("a refusal printed %q and sent %q", out.String(), *sent)
			}
		})
	}
}

// A box without Nix, and a project the box has not got, each read as what
// they are rather than as the agent's own wording.
func TestSpineConfigNamesWhatTheBoxHasNot(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer string
		want   string
	}{
		{
			name:   "no nix on the box",
			answer: `{"error":{"code":"not_found","message":"nix is not installed on this box"}}`,
			want:   "homebox has no Nix, so a project's packages cannot be installed there; `repose machine add homebox --ssh me@192.168.1.40` installs it.",
		},
		{
			name:   "a project the store has not got",
			answer: `{"error":{"code":"not_found","message":"the store has no project todo-app"}}`,
			want:   "todo-app is not on homebox yet; `repose run` prepares it.",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, _, _ := spineConfigEnv(t)
			fakeAgent(t, map[string]string{"env apply": c.answer})
			handled, err := spineConfigApply(context.Background(), e, "", "")
			if !handled {
				t.Fatal("a project on a machine the user owns was left to the api")
			}
			if exitCodeOf(err) != ExitGuestNotRunning || err.Error() != c.want {
				t.Fatalf("exit %d: %q", exitCodeOf(err), err.Error())
			}
		})
	}
}

// A checkout on no machine of the user's is the hosted path's, every
// config command of it.
func TestSpineConfigLeavesHostedProjectsAlone(t *testing.T) {
	for _, c := range []struct {
		name string
		call func(e *Env) (bool, error)
	}{
		{name: "show", call: func(e *Env) (bool, error) { return spineConfigShow(context.Background(), e, "", false) }},
		{name: "add", call: func(e *Env) (bool, error) { return spineConfigAdd(context.Background(), e, "", []string{"gcc"}) }},
		{name: "remove", call: func(e *Env) (bool, error) { return spineConfigRemove(e, "") }},
		{name: "edit", call: func(e *Env) (bool, error) {
			return spineConfigEdit(e, "", func(string) error { return nil })
		}},
		{name: "apply", call: func(e *Env) (bool, error) { return spineConfigApply(context.Background(), e, "", "") }},
		{name: "revisions", call: func(e *Env) (bool, error) { return spineConfigRevisions(e, "") }},
		{name: "--global on", call: func(e *Env) (bool, error) { return spinePersonalSwitch(e, "") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, reg, _, _ := spineEnv(t)
			addMachine(t, reg, testMachine())
			fakeAgent(t, nil)
			handled, err := c.call(e)
			if handled || err != nil {
				t.Fatalf("handled %v, err %v", handled, err)
			}
		})
	}
}

// The run applies the checkout's repose.nix before it takes the point,
// and the same file again costs the box nothing (repoconfig.go's cache).
func TestSpineApplyEnvSendsTheProjectsFileOnce(t *testing.T) {
	e, _, errOut := spineConfigEnv(t)
	sent := fakeAgent(t, map[string]string{
		"env apply": `{"profile":"/p","added":["gcc"],"present":[],"skipped":[],"ms":12000}`,
	})
	root := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(root, repoConfigName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("{ pkgs, ... }:\n{ home.packages = [ pkgs.gcc ]; }\n")
	var ticks []string
	tick := func(s string) { ticks = append(ticks, s) }

	e.spineApplyEnv(context.Background(), sshTarget{}, testMachine(), testPlace(), root, tick)
	if len(ticks) != 1 || ticks[0] != "Applied repose.nix" {
		t.Fatalf("ticks %q", ticks)
	}
	if len(*sent) != 1 {
		t.Fatalf("sent %q", *sent)
	}
	// The same file again: no command and no line.
	e.spineApplyEnv(context.Background(), sshTarget{}, testMachine(), testPlace(), root, tick)
	if len(*sent) != 1 || len(ticks) != 1 {
		t.Errorf("the unchanged file was applied again: %q, %q", *sent, ticks)
	}
	// A changed file is applied again.
	write("{ pkgs, ... }:\n{ home.packages = [ pkgs.gcc pkgs.ripgrep ]; }\n")
	e.spineApplyEnv(context.Background(), sshTarget{}, testMachine(), testPlace(), root, tick)
	if len(*sent) != 2 || len(ticks) != 2 {
		t.Errorf("the changed file was not applied: %q, %q", *sent, ticks)
	}
	if errOut.String() != "" {
		t.Errorf("stderr %q", errOut.String())
	}
}

// An environment that did not apply is a warning: the run goes on and
// starts the agent, because a box without a toolchain is still a box the
// agent runs on.
func TestSpineApplyEnvWarnsAndCarriesOn(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer string
		want   string
	}{
		{
			name:   "a package that would not build",
			answer: `{"error":{"code":"internal","message":"install the project's packages: nixpkgs has no package \"zigg\""}}`,
			want:   "Could not apply todo-app's packages: install the project's packages: nixpkgs has no package \"zigg\"\n",
		},
		{
			name:   "a box with no nix",
			answer: `{"error":{"code":"not_found","message":"nix is not installed on this box"}}`,
			want:   "homebox has no Nix, so a project's packages cannot be installed there; `repose machine add homebox --ssh me@192.168.1.40` installs it.\n",
		},
		{
			name:   "something the file asks for that a profile cannot hold",
			answer: `{"profile":"/p","added":[],"present":[],"skipped":[{"name":"repose.system","reason":"a system service is the box's own to run"}],"ms":40}`,
			want:   "repose.system was not applied: a system service is the box's own to run.\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, out, errOut := spineConfigEnv(t)
			fakeAgent(t, map[string]string{"env apply": c.answer})
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, repoConfigName), []byte("{ pkgs, ... }:\n{ home.packages = [ pkgs.gcc ]; }\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			var ticks []string
			e.spineApplyEnv(context.Background(), sshTarget{}, testMachine(), testPlace(), root, func(s string) { ticks = append(ticks, s) })
			if len(ticks) != 0 {
				t.Errorf("ticks %q", ticks)
			}
			if errOut.String() != c.want {
				t.Errorf("stderr %q, want %q", errOut.String(), c.want)
			}
			if out.String() != "" {
				t.Errorf("stdout %q", out.String())
			}
		})
	}
}

// A project that declares no environment asks the box for nothing, so a
// run of it costs no ssh and says nothing.
func TestSpineApplyEnvSaysNothingWithoutAFile(t *testing.T) {
	e, out, errOut := spineConfigEnv(t)
	sent := fakeAgent(t, nil)
	var ticks []string
	tick := func(s string) { ticks = append(ticks, s) }
	for _, root := range []string{"", t.TempDir()} {
		e.spineApplyEnv(context.Background(), sshTarget{}, testMachine(), testPlace(), root, tick)
	}
	if len(*sent) != 0 || len(ticks) != 0 || out.String() != "" || errOut.String() != "" {
		t.Errorf("sent %q, ticks %q, stdout %q, stderr %q", *sent, ticks, out.String(), errOut.String())
	}
}

func TestSpineInstallToolsInstallsWhatTheProjectNeeds(t *testing.T) {
	for _, c := range []struct {
		name      string
		answers   map[string]string
		wantTicks []string
		wantErr   string
		noRun     bool
	}{
		{
			name: "what is missing is installed and named",
			answers: map[string]string{
				"tools plan": `{"hash":"a1","missing":[{"name":"pnpm","bins":["pnpm"]}],"runtimes":[{"runtime":"node","want":"20","have":"24","package":"nodejs_20"}],"failed":[]}`,
				"tools run":  `{"hash":"a1","installed":["nodejs_20","pnpm"],"failed":[],"ms":48000}`,
			},
			wantTicks: []string{"Installed nodejs_20 and pnpm"},
		},
		{
			name:      "a box that has what the project needs says nothing",
			answers:   map[string]string{"tools plan": `{"hash":"a1","missing":[],"runtimes":[],"failed":[]}`},
			wantTicks: nil,
			noRun:     true,
		},
		{
			name:    "a box that could not answer",
			answers: map[string]string{"tools plan": `{"error":{"code":"not_found","message":"the store has no project todo-app"}}`},
			wantErr: "Could not read what todo-app needs on homebox: the store has no project todo-app\n",
			noRun:   true,
		},
		{
			name: "a tool that would not install",
			answers: map[string]string{
				"tools plan": `{"hash":"a1","missing":[{"name":"gh","bins":["gh"]}],"runtimes":[],"failed":[]}`,
				"tools run":  `{"hash":"a1","installed":[],"failed":[{"name":"gh","reason":"no nixpkgs package has bin/gh"}],"ms":900}`,
			},
			wantErr: "gh was not installed: no nixpkgs package has bin/gh.\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, out, errOut := spineConfigEnv(t)
			sent := fakeAgent(t, c.answers)
			var ticks []string
			e.spineInstallTools(context.Background(), sshTarget{}, testMachine(), testPlace(), func(s string) { ticks = append(ticks, s) })
			if strings.Join(ticks, "|") != strings.Join(c.wantTicks, "|") {
				t.Errorf("ticks %q, want %q", ticks, c.wantTicks)
			}
			if errOut.String() != c.wantErr {
				t.Errorf("stderr %q, want %q", errOut.String(), c.wantErr)
			}
			if out.String() != "" {
				t.Errorf("stdout %q", out.String())
			}
			if c.noRun {
				for _, cmd := range *sent {
					if strings.Contains(cmd, "'tools' 'run'") {
						t.Errorf("nothing was missing and it installed anyway: %s", cmd)
					}
				}
			}
		})
	}
}
