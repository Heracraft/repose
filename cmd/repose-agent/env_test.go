package main

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A project's repose.nix as a user writes one: the packages, and one
// thing a single-user profile cannot hold.
const projectFragment = `{ pkgs, lib, ... }: {
  # the project's own toolchain
  home.packages = with pkgs; [
    bun
    ripgrep
  ];
  programs.direnv.enable = true;
}
`

// The skipped entry projectFragment's programs.* earns. The CLI prints it
// as "programs.* was not applied: ..." (internal/cli spineSkipLine), so
// the reason is a clause, not a sentence.
const hmSkip = `{"name":"programs.*","reason":"a home-manager module, which a profile has not"}`

// TestEnvShapes is the golden shape of `env apply` and `env show`.
func TestEnvShapes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f *fixture)
		args  []string
		want  func(f *fixture) string
	}{
		{
			name: "env apply",
			setup: func(t *testing.T, f *fixture) {
				f.writeRepoConfig(t, "todo-app", projectFragment)
			},
			args: []string{"env", "apply", "--project", "todo-app"},
			want: func(f *fixture) string {
				return `{"profile":"` + profileDir(f.env.Store, "todo-app") +
					`","added":["bun","ripgrep"],"present":[],"skipped":[` + hmSkip + `],"ms":0}`
			},
		},
		{
			name: "env apply with a package already in the profile",
			setup: func(t *testing.T, f *fixture) {
				f.writeRepoConfig(t, "todo-app", projectFragment)
				mkdir(t, profileDir(f.env.Store, "todo-app"))
				f.nix.add("bun")
			},
			args: []string{"env", "apply", "--project", "todo-app"},
			want: func(f *fixture) string {
				return `{"profile":"` + profileDir(f.env.Store, "todo-app") +
					`","added":["ripgrep"],"present":["bun"],"skipped":[` + hmSkip + `],"ms":0}`
			},
		},
		{
			name: "env apply of a pkgs.NAME list with an expression in it",
			setup: func(t *testing.T, f *fixture) {
				f.writeRepoConfig(t, "todo-app",
					"{ home.packages = [ pkgs.bun (pkgs.hello) ]; }\n")
			},
			args: []string{"env", "apply", "--project", "todo-app"},
			want: func(f *fixture) string {
				return `{"profile":"` + profileDir(f.env.Store, "todo-app") +
					`","added":["bun"],"present":[],"skipped":[` +
					`{"name":"(pkgs.hello)","reason":"not a plain nixpkgs package name"}],"ms":0}`
			},
		},
		{
			name: "env apply of the packages the menu named, with no file",
			setup: func(t *testing.T, f *fixture) {
				mkdir(t, liveDir(f.env.Store, "todo-app"))
			},
			args: []string{"env", "apply", "--project", "todo-app", "--add", "bun,postgresql"},
			want: func(f *fixture) string {
				return `{"profile":"` + profileDir(f.env.Store, "todo-app") +
					`","added":["bun","postgresql"],"present":[],"skipped":[],"ms":0}`
			},
		},
		{
			name: "env apply of a package the menu named and the file asks for",
			setup: func(t *testing.T, f *fixture) {
				f.writeRepoConfig(t, "todo-app", "{ home.packages = [ pkgs.bun ]; }\n")
			},
			args: []string{"env", "apply", "--project", "todo-app", "--add", "bun"},
			want: func(f *fixture) string {
				return `{"profile":"` + profileDir(f.env.Store, "todo-app") +
					`","added":["bun"],"present":[],"skipped":[],"ms":0}`
			},
		},
		{
			name: "env show of a profile that was never made",
			setup: func(t *testing.T, f *fixture) {
				mkdir(t, liveDir(f.env.Store, "todo-app"))
			},
			args: []string{"env", "show", "--project", "todo-app"},
			want: func(f *fixture) string {
				return `{"profile":"` + profileDir(f.env.Store, "todo-app") + `","packages":[]}`
			},
		},
		{
			name: "env show",
			setup: func(t *testing.T, f *fixture) {
				mkdir(t, profileDir(f.env.Store, "todo-app"))
				f.nix.add("ripgrep")
				f.nix.add("bun")
			},
			args: []string{"env", "show", "--project", "todo-app"},
			want: func(f *fixture) string {
				return `{"profile":"` + profileDir(f.env.Store, "todo-app") +
					`","packages":[{"name":"bun","attr":"bun"},{"name":"ripgrep","attr":"ripgrep"}]}`
			},
		},
		{
			name: "env show of the listing nix printed as an array",
			setup: func(t *testing.T, f *fixture) {
				mkdir(t, profileDir(f.env.Store, "todo-app"))
				f.nix.stdout["profile list"] =
					`{"elements":[{"attrPath":"legacyPackages.x86_64-linux.python312Packages.black"}]}`
			},
			args: []string{"env", "show", "--project", "todo-app"},
			want: func(f *fixture) string {
				return `{"profile":"` + profileDir(f.env.Store, "todo-app") +
					`","packages":[{"name":"python312Packages.black","attr":"python312Packages.black"}]}`
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			if c.setup != nil {
				c.setup(t, f)
			}
			if code := f.run(c.args...); code != 0 {
				t.Fatalf("exit %d, stdout %s", code, f.stdout())
			}
			if got := normMS(f.stdout()); got != c.want(f) {
				t.Errorf("stdout\n got %s\nwant %s", got, c.want(f))
			}
			// One object and nothing else: the CLI parses this stream.
			if n := strings.Count(f.out.String(), "\n"); n != 1 {
				t.Errorf("stdout has %d lines, want 1: %q", n, f.out.String())
			}
		})
	}
}

// TestEnvErrorCodes is one case per code `env` can answer with. busy,
// exists and no_space are the undo store's, and no path here reaches
// them.
func TestEnvErrorCodes(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T, f *fixture)
		args     []string
		wantCode string
		wantExit int
	}{
		{
			name:     "invalid_argument without a project",
			args:     []string{"env", "apply"},
			wantCode: codeInvalidArgument,
			wantExit: 2,
		},
		{
			name:     "invalid_argument for a package that is not an attribute path",
			args:     []string{"env", "apply", "--project", "todo-app", "--add", "bun; rm -rf /"},
			wantCode: codeInvalidArgument,
			wantExit: 2,
		},
		{
			name:     "invalid_argument for an unknown env command",
			args:     []string{"env", "rebuild", "--project", "todo-app"},
			wantCode: codeInvalidArgument,
			wantExit: 2,
		},
		{
			name:     "not_found for a project the store has not",
			args:     []string{"env", "apply", "--project", "todo-app"},
			wantCode: codeNotFound,
			wantExit: 1,
		},
		{
			name: "not_found for a box with no nix",
			setup: func(t *testing.T, f *fixture) {
				mkdir(t, liveDir(f.env.Store, "todo-app"))
				f.nix.fail["nix --version"] = "bash: nix: command not found"
			},
			args:     []string{"env", "apply", "--project", "todo-app", "--add", "bun"},
			wantCode: codeNotFound,
			wantExit: 1,
		},
		{
			name: "internal when the profile cannot be read",
			setup: func(t *testing.T, f *fixture) {
				mkdir(t, profileDir(f.env.Store, "todo-app"))
				f.nix.fail["profile list"] = "error: profile is locked"
			},
			args:     []string{"env", "show", "--project", "todo-app"},
			wantCode: codeInternal,
			wantExit: 1,
		},
		{
			name: "internal when nothing would build",
			setup: func(t *testing.T, f *fixture) {
				mkdir(t, liveDir(f.env.Store, "todo-app"))
				f.nix.fail["nixpkgs#bun"] = `error: flake 'nixpkgs' does not provide attribute 'bun'`
			},
			args:     []string{"env", "apply", "--project", "todo-app", "--add", "bun"},
			wantCode: codeInternal,
			wantExit: 1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			if c.setup != nil {
				c.setup(t, f)
			}
			if code := f.run(c.args...); code != c.wantExit {
				t.Errorf("exit %d, want %d: %s", code, c.wantExit, f.stdout())
			}
			var ae errorEnvelope
			if err := json.Unmarshal([]byte(f.stdout()), &ae); err != nil {
				t.Fatalf("stdout is not the error envelope: %v: %s", err, f.stdout())
			}
			if ae.Error.Code != c.wantCode {
				t.Errorf("code %q, want %q", ae.Error.Code, c.wantCode)
			}
			if ae.Error.Message == "" {
				t.Error("the envelope carries no message")
			}
			if n := strings.Count(f.out.String(), "\n"); n != 1 {
				t.Errorf("stdout has %d lines, want 1: %q", n, f.out.String())
			}
		})
	}
}

// A box with no nix is refused by naming what is absent, and nothing is
// installed on it. not_found is also the project the store has not got,
// so the CLI tells the two apart by the first words of the message
// (internal/cli agentNoNix): they are part of the contract.
func TestEnvApplyNamesTheMissingNix(t *testing.T) {
	f := newFixture(t)
	f.writeRepoConfig(t, "todo-app", projectFragment)
	f.nix.fail["nix --version"] = "bash: nix: command not found"

	if code := f.run("env", "apply", "--project", "todo-app"); code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, f.stdout())
	}
	var ae errorEnvelope
	if err := json.Unmarshal([]byte(f.stdout()), &ae); err != nil {
		t.Fatalf("stdout is not the error envelope: %v", err)
	}
	if ae.Error.Code != codeNotFound {
		t.Errorf("code %q, want %q", ae.Error.Code, codeNotFound)
	}
	if !strings.HasPrefix(ae.Error.Message, "nix is not installed") {
		t.Errorf("the refusal does not open by naming what is absent: %q", ae.Error.Message)
	}
	if len(f.nix.installed) > 0 {
		t.Errorf("something was installed anyway: %v", f.nix.installed)
	}
}

// The profile is inside the project's live subvolume, which is what makes
// `point rewind` undo an environment change (DECISIONS I-832).
func TestEnvApplyInstallsIntoTheProjectsSubvolume(t *testing.T) {
	f := newFixture(t)
	f.writeRepoConfig(t, "todo-app", "{ home.packages = [ pkgs.bun ]; }\n")
	if code := f.run("env", "apply", "--project", "todo-app"); code != 0 {
		t.Fatalf("exit %d: %s", code, f.stdout())
	}
	profile := profileDir(f.env.Store, "todo-app")
	if want := filepath.Join(liveDir(f.env.Store, "todo-app"), ".repose", "profile"); profile != want {
		t.Fatalf("the profile is at %s, want %s", profile, want)
	}
	var found bool
	for _, argv := range f.nix.calls {
		if i := slices.Index(argv, "--profile"); i >= 0 && i+1 < len(argv) && argv[i+1] == profile {
			found = true
		}
	}
	if !found {
		t.Errorf("no nix call named the project's profile: %v", f.nix.calls)
	}
}

// A second apply installs nothing again: the packages are where it put
// them, and the answer says so.
func TestEnvApplyIsIdempotent(t *testing.T) {
	f := newFixture(t)
	f.writeRepoConfig(t, "todo-app", projectFragment)
	if code := f.run("env", "apply", "--project", "todo-app"); code != 0 {
		t.Fatalf("first apply: exit %d: %s", code, f.stdout())
	}
	mkdir(t, profileDir(f.env.Store, "todo-app")) // nix made it
	f.out.Reset()
	if code := f.run("env", "apply", "--project", "todo-app"); code != 0 {
		t.Fatalf("second apply: exit %d: %s", code, f.stdout())
	}
	var got wireEnvApply
	if err := json.Unmarshal([]byte(f.stdout()), &got); err != nil {
		t.Fatalf("stdout is not the answer: %v", err)
	}
	if len(got.Added) != 0 {
		t.Errorf("the second apply added %v", got.Added)
	}
	if want := []string{"bun", "ripgrep"}; !slices.Equal(got.Present, want) {
		t.Errorf("present %v, want %v", got.Present, want)
	}
	if want := []string{"bun", "ripgrep"}; !slices.Equal(f.nix.installed, want) {
		t.Errorf("nix installed %v over two applies, want %v", f.nix.installed, want)
	}
}

// The reader of repose.nix: what it takes for a package, and what it
// hands back named instead of half-applying.
func TestWantedPackages(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		attrs   []string
		skipped []string // the name of each skipped entry
	}{
		{
			name:  "with pkgs",
			body:  "{ home.packages = with pkgs; [ bun ripgrep ]; }",
			attrs: []string{"bun", "ripgrep"},
		},
		{
			name:  "pkgs.NAME",
			body:  "{ home.packages = [ pkgs.bun pkgs.python312Packages.black ]; }",
			attrs: []string{"bun", "python312Packages.black"},
		},
		{
			name: "one list per menu item, as the menu generates it",
			body: "{ imports = [\n{ home.packages = [ pkgs.bun ]; }\n" +
				"{ home.packages = [ pkgs.nodejs_22 ]; }\n]; }",
			attrs: []string{"bun", "nodejs_22"},
		},
		{
			name:  "a comment is not a package",
			body:  "{ # home.packages = with pkgs; [ cowsay ];\n home.packages = [ pkgs.bun ]; }",
			attrs: []string{"bun"},
		},
		{
			name:  "a string is not a package",
			body:  "{ home.packages = [ pkgs.bun ]; home.file.\".x\".text = \"home.packages = [ pkgs.cowsay ];\"; }",
			attrs: []string{"bun"},
			// home.file, named once, and nothing from inside the string.
			skipped: []string{"home.file"},
		},
		{
			name:    "a built package is not a name",
			body:    "{ home.packages = [ (pkgs.writeShellScriptBin \"hi\" \"echo hi\") ]; }",
			skipped: []string{"(pkgs.writeShellScriptBin )"},
		},
		{
			name:    "a service is the box's own",
			body:    "{ repose.system = [ { services.postgresql.enable = true; } ]; }",
			skipped: []string{"repose.system"},
		},
		{
			name:  "the same package twice is installed once",
			body:  "{ home.packages = with pkgs; [ bun bun ]; }",
			attrs: []string{"bun"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.writeRepoConfig(t, "todo-app", c.body)
			attrs, skipped, err := wantedPackages(liveDir(f.env.Store, "todo-app"))
			if err != nil {
				t.Fatalf("read the fragment: %v", err)
			}
			if !slices.Equal(attrs, c.attrs) {
				t.Errorf("packages %v, want %v", attrs, c.attrs)
			}
			var names []string
			for _, s := range skipped {
				names = append(names, s.Name)
				if s.Reason == "" {
					t.Errorf("%q is skipped with no reason", s.Name)
				}
			}
			if !slices.Equal(names, c.skipped) {
				t.Errorf("skipped %v, want %v", names, c.skipped)
			}
		})
	}
}

// A project with no repose.nix is not a failure: the carried tools and
// the packages the menu named are still an environment.
func TestWantedPackagesWithoutAFile(t *testing.T) {
	f := newFixture(t)
	mkdir(t, liveDir(f.env.Store, "todo-app"))
	attrs, skipped, err := wantedPackages(liveDir(f.env.Store, "todo-app"))
	if err != nil || len(attrs) != 0 || len(skipped) != 0 {
		t.Fatalf("packages %v, skipped %v, err %v", attrs, skipped, err)
	}
}

// msRe is the one field of an answer a golden shape cannot carry.
var msRe = regexp.MustCompile(`"ms":[0-9]+`)

func normMS(s string) string { return msRe.ReplaceAllString(s, `"ms":0`) }
