package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// The carry's list as the laptop writes it (guest-conventions.md "Tools
// carry"): one tool the box lacks, one it has, and a node the project
// pins.
const wantedList = `{"v":1,"hash":"0199a1c21f3b7c4d9e2a5b6c7d8e9f01","items":[
  {"name":"ripgrep","bins":["rg"],"from":"project"},
  {"name":"jq","bins":["jq"],"from":"laptop"}
],"node":"22"}`

const wantedHash = "0199a1c21f3b7c4d9e2a5b6c7d8e9f01"

// TestToolsShapes is the golden shape of `tools plan` and `tools run`.
func TestToolsShapes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f *fixture)
		args  []string
		want  string
	}{
		{
			name:  "tools plan",
			setup: wantedAndProject,
			args:  []string{"tools", "plan", "--project", "todo-app"},
			want: `{"hash":"` + wantedHash + `","missing":[{"name":"ripgrep","bins":["rg"]}],` +
				`"runtimes":[{"runtime":"node","want":"22","have":"20","package":"nodejs_22"}],"failed":[]}`,
		},
		{
			name: "tools plan with nothing left to do",
			setup: func(t *testing.T, f *fixture) {
				wantedAndProject(t, f)
				writeMarker(liveDir(f.env.Store, "todo-app"), wantedHash)
			},
			args: []string{"tools", "plan", "--project", "todo-app"},
			want: `{"hash":"` + wantedHash + `","missing":[],"runtimes":[],"failed":[]}`,
		},
		{
			name: "tools plan with no list on the box",
			setup: func(t *testing.T, f *fixture) {
				mkdir(t, liveDir(f.env.Store, "todo-app"))
			},
			args: []string{"tools", "plan", "--project", "todo-app"},
			want: `{"hash":"","missing":[],"runtimes":[],"failed":[]}`,
		},
		{
			name: "tools plan of a tool that would not install before",
			setup: func(t *testing.T, f *fixture) {
				wantedAndProject(t, f)
				live := liveDir(f.env.Store, "todo-app")
				mkdir(t, live+"/.repose/tools")
				appendFailed(live, "ripgrep|||", "error: no nixpkgs package has bin/rg")
			},
			args: []string{"tools", "plan", "--project", "todo-app"},
			want: `{"hash":"` + wantedHash + `","missing":[],` +
				`"runtimes":[{"runtime":"node","want":"22","have":"20","package":"nodejs_22"}],` +
				`"failed":[{"name":"ripgrep","reason":"error: no nixpkgs package has bin/rg"}]}`,
		},
		{
			name: "tools run",
			setup: func(t *testing.T, f *fixture) {
				wantedAndProject(t, f)
				f.nix.provides["ripgrep"] = []string{"rg"}
			},
			args: []string{"tools", "run", "--project", "todo-app"},
			want: `{"hash":"` + wantedHash + `","installed":["nodejs_22","ripgrep"],"failed":[],"ms":0}`,
		},
		{
			name: "tools run with nothing left to do",
			setup: func(t *testing.T, f *fixture) {
				wantedAndProject(t, f)
				writeMarker(liveDir(f.env.Store, "todo-app"), wantedHash)
			},
			args: []string{"tools", "run", "--project", "todo-app"},
			want: `{"hash":"` + wantedHash + `","installed":[],"failed":[],"ms":0}`,
		},
		{
			name: "tools run of a tool nixpkgs has not",
			setup: func(t *testing.T, f *fixture) {
				wantedAndProject(t, f)
				f.nix.fail["nixpkgs#ripgrep"] = "error: flake 'nixpkgs' does not provide attribute 'ripgrep'"
			},
			args: []string{"tools", "run", "--project", "todo-app"},
			want: `{"hash":"` + wantedHash + `","installed":["nodejs_22"],"failed":[{"name":"ripgrep",` +
				`"reason":"error: flake 'nixpkgs' does not provide attribute 'ripgrep'"}],"ms":0}`,
		},
		{
			name: "tools run on a box with no nix, with the laptop's manager",
			setup: func(t *testing.T, f *fixture) {
				mkdir(t, liveDir(f.env.Store, "todo-app"))
				f.writeWanted(t, `{"v":1,"hash":"`+wantedHash+`","items":[
				  {"name":"typescript","bins":["tsc"],"manager":"npm","pkg":"typescript","from":"laptop"}
				]}`)
				f.nix.fail["nix --version"] = "bash: nix: command not found"
				f.nix.provides["typescript"] = []string{"tsc"}
			},
			args: []string{"tools", "run", "--project", "todo-app"},
			want: `{"hash":"` + wantedHash + `","installed":["typescript"],"failed":[],"ms":0}`,
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
			if got := normMS(f.stdout()); got != c.want {
				t.Errorf("stdout\n got %s\nwant %s", got, c.want)
			}
			if n := strings.Count(f.out.String(), "\n"); n != 1 {
				t.Errorf("stdout has %d lines, want 1: %q", n, f.out.String())
			}
		})
	}
}

// TestToolsErrorCodes is one case per code `tools` can answer with.
func TestToolsErrorCodes(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T, f *fixture)
		args     []string
		wantCode string
		wantExit int
	}{
		{
			name:     "invalid_argument without a project",
			args:     []string{"tools", "plan"},
			wantCode: codeInvalidArgument,
			wantExit: 2,
		},
		{
			name:     "invalid_argument for an unknown tools command",
			args:     []string{"tools", "install", "--project", "todo-app"},
			wantCode: codeInvalidArgument,
			wantExit: 2,
		},
		{
			name: "invalid_argument for a list that is not JSON",
			setup: func(t *testing.T, f *fixture) {
				mkdir(t, liveDir(f.env.Store, "todo-app"))
				f.writeWanted(t, "{not json")
			},
			args:     []string{"tools", "plan", "--project", "todo-app"},
			wantCode: codeInvalidArgument,
			wantExit: 2,
		},
		{
			name:     "not_found for a project the store has not",
			args:     []string{"tools", "run", "--project", "todo-app"},
			wantCode: codeNotFound,
			wantExit: 1,
		},
		{
			name: "internal without a home to read the list from",
			setup: func(t *testing.T, f *fixture) {
				mkdir(t, liveDir(f.env.Store, "todo-app"))
				f.env.Home = ""
			},
			args:     []string{"tools", "plan", "--project", "todo-app"},
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
		})
	}
}

// The marker goes inside the project's live subvolume, so a rewind takes
// it with the tools it describes and the laptop's next run installs them
// again (DECISIONS I-832).
func TestToolsRunMarksInsideTheSubvolume(t *testing.T) {
	f := newFixture(t)
	wantedAndProject(t, f)
	f.nix.provides["ripgrep"] = []string{"rg"}

	if code := f.run("tools", "run", "--project", "todo-app"); code != 0 {
		t.Fatalf("exit %d: %s", code, f.stdout())
	}
	live := liveDir(f.env.Store, "todo-app")
	b, err := os.ReadFile(markerPath(live))
	if err != nil {
		t.Fatalf("the carry marker is missing: %v", err)
	}
	if got := strings.TrimSpace(string(b)); got != wantedHash {
		t.Errorf("the marker holds %q, want %q", got, wantedHash)
	}
	if !strings.HasPrefix(markerPath(live), live) {
		t.Errorf("the marker is outside the live subvolume: %s", markerPath(live))
	}

	// A second run is quiet, and installs nothing twice.
	f.out.Reset()
	installed := slices.Clone(f.nix.installed)
	if code := f.run("tools", "run", "--project", "todo-app"); code != 0 {
		t.Fatalf("second run: exit %d: %s", code, f.stdout())
	}
	if !slices.Equal(f.nix.installed, installed) {
		t.Errorf("the second run installed %v, over %v", f.nix.installed, installed)
	}
	var got wireToolsRun
	if err := json.Unmarshal([]byte(f.stdout()), &got); err != nil {
		t.Fatalf("stdout is not the answer: %v", err)
	}
	if len(got.Installed) != 0 || len(got.Failed) != 0 {
		t.Errorf("the second run answered %+v", got)
	}
}

// A tool that would not install is recorded with the line that said why,
// so the next plan reports it instead of asking for it again.
func TestToolsRunRecordsAFailure(t *testing.T) {
	f := newFixture(t)
	wantedAndProject(t, f)
	f.nix.fail["nixpkgs#ripgrep"] = "error: flake 'nixpkgs' does not provide attribute 'ripgrep'"

	if code := f.run("tools", "run", "--project", "todo-app"); code != 0 {
		t.Fatalf("exit %d: %s", code, f.stdout())
	}
	f.out.Reset()
	if code := f.run("tools", "plan", "--project", "todo-app"); code != 0 {
		t.Fatalf("plan: exit %d: %s", code, f.stdout())
	}
	var plan wireToolsPlan
	if err := json.Unmarshal([]byte(f.stdout()), &plan); err != nil {
		t.Fatalf("stdout is not the plan: %v", err)
	}
	if len(plan.Missing) != 0 {
		t.Errorf("plan asks for %+v again", plan.Missing)
	}
	if len(plan.Failed) != 1 || plan.Failed[0].Name != "ripgrep" ||
		!strings.Contains(plan.Failed[0].Reason, "does not provide attribute") {
		t.Errorf("plan reports %+v", plan.Failed)
	}
}

// The runtime pin: the attribute goes into the project's profile, and the
// one it replaces comes out (DECISIONS I-265).
func TestToolsRunReplacesARuntimePin(t *testing.T) {
	f := newFixture(t)
	wantedAndProject(t, f)
	live := liveDir(f.env.Store, "todo-app")
	mkdir(t, live+"/.repose/tools")
	if err := os.WriteFile(live+"/.repose/tools/node", []byte("nodejs_20\n"), 0o644); err != nil {
		t.Fatalf("write the runtime record: %v", err)
	}

	if code := f.run("tools", "run", "--project", "todo-app"); code != 0 {
		t.Fatalf("exit %d: %s", code, f.stdout())
	}
	if !slices.Contains(f.nix.installed, "nodejs_22") {
		t.Errorf("nodejs_22 was not installed: %v", f.nix.installed)
	}
	if !slices.Contains(f.nix.removed, "nodejs_20") {
		t.Errorf("nodejs_20 was not removed: %v", f.nix.removed)
	}
	b, err := os.ReadFile(live + "/.repose/tools/node")
	if err != nil || strings.TrimSpace(string(b)) != "nodejs_22" {
		t.Errorf("the runtime record holds %q (%v)", b, err)
	}
}

// parseRuntimeVersion reads what each runtime prints
// (guest-conventions.md "Tools carry"): node's major, ruby's series, and
// the major of java's quoted version.
func TestParseRuntimeVersion(t *testing.T) {
	cases := []struct{ lang, out, want string }{
		{"node", "v22.11.0\n", "22"},
		{"ruby", "ruby 3.3.4 (2024-07-09 revision be1089c8ec) [x86_64-linux]\n", "3.3"},
		{"java", "openjdk version \"17.0.11\" 2024-04-16\n", "17"},
		{"java", "java version \"1.8.0_462\"\n", "8"},
		{"node", "", ""},
		{"java", "nothing a version", ""},
	}
	for _, c := range cases {
		if got := parseRuntimeVersion(c.lang, c.out); got != c.want {
			t.Errorf("%s of %q is %q, want %q", c.lang, c.out, got, c.want)
		}
	}
}

// runtimeAttr is the attribute each pin becomes.
func TestRuntimeAttr(t *testing.T) {
	cases := []struct{ lang, version, want string }{
		{"node", "22", "nodejs_22"},
		{"ruby", "3.3", "ruby_3_3"},
		{"java", "17", "jdk17_headless"},
	}
	for _, c := range cases {
		if got := runtimeAttr(c.lang, c.version); got != c.want {
			t.Errorf("%s %s is %q, want %q", c.lang, c.version, got, c.want)
		}
	}
}

// wantedAndProject is the box the carry has written its list onto: the
// project exists, jq is on PATH, the box's node is 20, and nix-locate's
// index says which package has bin/rg.
func wantedAndProject(t *testing.T, f *fixture) {
	t.Helper()
	mkdir(t, liveDir(f.env.Store, "todo-app"))
	f.writeWanted(t, wantedList)
	f.nix.have["jq"] = true
	f.nix.version["node"] = "v20.11.1\n"
	f.nix.stdout["nix-locate"] = "ripgrep.out\n"
}
