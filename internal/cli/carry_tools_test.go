package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeLaptop lays out one global tool per manager under a fake home, the
// way each manager leaves them on disk.
func fakeLaptop(t *testing.T) (toolEnv, string) {
	t.Helper()
	home := t.TempDir()
	// npm: prefix from ~/.npmrc, next to a token line that must not matter
	writeFile(t, filepath.Join(home, ".npmrc"), "//registry.example/:_authToken=secret-token\nprefix=~/.npm-global\n")
	nm := filepath.Join(home, ".npm-global", "lib", "node_modules")
	writeFile(t, filepath.Join(nm, "typescript", "package.json"), `{"name":"typescript","version":"5.6.2","bin":{"tsc":"./bin/tsc","tsserver":"./bin/tsserver"}}`)
	writeFile(t, filepath.Join(nm, "portless", "package.json"), `{"name":"portless","version":"0.15.6","bin":{"portless":"./dist/cli.js"}}`)
	writeFile(t, filepath.Join(nm, "@scope", "thing", "package.json"), `{"name":"@scope/thing","version":"1.0.0","bin":"./cli.js"}`)
	writeFile(t, filepath.Join(nm, "npm", "package.json"), `{"name":"npm","version":"11.0.0","bin":{"npm":"x","npx":"y"}}`)
	writeFile(t, filepath.Join(nm, "nobin", "package.json"), `{"name":"nobin","version":"1.0.0"}`)
	// a linked package is a laptop checkout, never carried
	if err := os.Symlink(t.TempDir(), filepath.Join(nm, "linked")); err != nil {
		t.Fatal(err)
	}
	// pnpm global
	pg := filepath.Join(home, ".local", "share", "pnpm", "global", "5")
	writeFile(t, filepath.Join(pg, "package.json"), `{"dependencies":{"vercel":"^39.0.0"}}`)
	writeFile(t, filepath.Join(pg, "node_modules", "vercel", "package.json"), `{"name":"vercel","version":"39.1.0","bin":{"vercel":"dist/index.js","vc":"dist/index.js"}}`)
	// bun global
	bg := filepath.Join(home, ".bun", "install", "global")
	writeFile(t, filepath.Join(bg, "package.json"), `{"dependencies":{"serve":"^14.0.0"}}`)
	writeFile(t, filepath.Join(bg, "node_modules", "serve", "package.json"), `{"name":"serve","version":"14.2.4","bin":{"serve":"build/main.js"}}`)
	// cargo: a registry crate and a path install
	writeFile(t, filepath.Join(home, ".cargo", ".crates2.json"), `{"installs":{"ripgrep 14.1.0 (registry+https://github.com/rust-lang/crates.io-index)":{"bins":["rg"]},"cargo-nextest 0.9.72 (registry+https://github.com/rust-lang/crates.io-index)":{"bins":["cargo-nextest"]},"mytool 0.1.0 (path+file:///home/me/src/mytool)":{"bins":["mytool"]}}}`)
	// uv tool
	uv := filepath.Join(home, ".local", "share", "uv", "tools", "ruff")
	writeFile(t, filepath.Join(uv, "uv-receipt.toml"), "[tool]\nrequirements = [{ name = \"ruff\" }]\nentrypoints = [\n    { name = \"ruff\", install-path = \"/home/me/.local/bin/ruff\" },\n]\n")
	writeFile(t, filepath.Join(uv, "lib", "python3.12", "site-packages", "ruff-0.6.9.dist-info", "METADATA"), "")
	// pipx
	writeFile(t, filepath.Join(home, ".local", "share", "pipx", "venvs", "black", "pipx_metadata.json"), `{"main_package":{"package":"black","package_version":"24.8.0","apps":["black","blackd"]}}`)
	env := map[string]string{}
	te := toolEnv{Home: home, GOOS: "linux", Getenv: func(k string) string { return env[k] }, LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
	return te, home
}

func TestReadGlobalTools(t *testing.T) {
	te, _ := fakeLaptop(t)
	got := readGlobalTools(te)
	type row struct{ Name, Manager, Pkg, Version, Bins string }
	var rows []row
	for _, it := range got {
		rows = append(rows, row{it.Name, it.Manager, it.Pkg, it.Version, strings.Join(it.Bins, ",")})
	}
	want := []row{
		{"@scope/thing", "npm", "@scope/thing", "1.0.0", "thing"},
		{"black", "pipx", "black", "24.8.0", "black,blackd"},
		{"cargo-nextest", "cargo", "cargo-nextest", "0.9.72", "cargo-nextest"},
		{"portless", "npm", "portless", "0.15.6", "portless"},
		// ripgrep's rg is in the base, so it does not travel
		{"ruff", "uv", "ruff", "0.6.9", "ruff"},
		{"serve", "bun", "serve", "14.2.4", "serve"},
		{"typescript", "npm", "typescript", "5.6.2", "tsc,tsserver"},
		{"vercel", "pnpm", "vercel", "39.1.0", "vercel,vc"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("readGlobalTools:\n got %+v\nwant %+v", rows, want)
	}
	// Nothing from the laptop but names, managers, versions: no path, no
	// token.
	tc := newToolsCarry(got, nil)
	for _, bad := range []string{te.Home, "secret-token", "/home/me", "mytool", "linked"} {
		if bytes.Contains(tc.JSON, []byte(bad)) {
			t.Errorf("the list carries %q: %s", bad, tc.JSON)
		}
	}
}

// NPM_CONFIG_PREFIX beats ~/.npmrc; with neither, the prefix is the
// directory above node's.
func TestNpmPrefix(t *testing.T) {
	te, home := fakeLaptop(t)
	te.Getenv = func(k string) string {
		if k == "NPM_CONFIG_PREFIX" {
			return "/opt/npm"
		}
		return ""
	}
	if got := npmPrefixes(te); !reflect.DeepEqual(got, []string{"/opt/npm"}) {
		t.Fatalf("env prefix: %v", got)
	}
	te.Getenv = func(string) string { return "" }
	if err := os.Remove(filepath.Join(home, ".npmrc")); err != nil {
		t.Fatal(err)
	}
	nodeDir := filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")
	writeFile(t, filepath.Join(nodeDir, "node"), "")
	te.LookPath = func(string) (string, error) { return filepath.Join(nodeDir, "node"), nil }
	if got := npmPrefixes(te); got[0] != filepath.Dir(nodeDir) {
		t.Fatalf("node prefix: %v", got)
	}
	te.GOOS = "windows"
	te.Getenv = func(k string) string {
		if k == "APPDATA" {
			return `C:\Users\me\AppData\Roaming`
		}
		return ""
	}
	if got := npmPrefixes(te); len(got) != 1 || !strings.HasSuffix(got[0], "npm") {
		t.Fatalf("windows prefix: %v", got)
	}
}

// A Go binary's build info gives `go install path@version`; one built
// from a checkout ("(devel)") is left out.
func TestReadGoBins(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	home := t.TempDir()
	gobin := filepath.Join(home, "go", "bin")
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		t.Fatal(err)
	}
	// The test binary itself has module build info: path
	// github.com/heracraft/repose/internal/cli.test, main version (devel).
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gobin, "devel-tool"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(gobin, "not-go"), "#!/bin/sh\n")
	te := toolEnv{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }}
	if got := readGoBins(te); len(got) != 0 {
		t.Fatalf("a (devel) binary and a script were carried: %+v", got)
	}
	// GOBIN wins over ~/go/bin.
	te.Getenv = func(k string) string {
		if k == "GOBIN" {
			return "/nonexistent"
		}
		return ""
	}
	if goBinDir(te) != "/nonexistent" {
		t.Fatal("GOBIN not honoured")
	}
}

// The base's own commands and the package managers never travel, and a
// name that could be read as a shell word or a path is dropped.
func TestDedupeTools(t *testing.T) {
	got := dedupeTools([]toolItem{
		{Name: "pnpm-dup", Bins: []string{"pnpm"}, Manager: "npm", Pkg: "pnpm-dup"},
		{Name: "jq-too", Bins: []string{"jq"}, Manager: "cargo", Pkg: "jq-too"},
		{Name: "air", Bins: []string{"air"}, Manager: "go", Pkg: "github.com/air-verse/air", Version: "v1.61.7"},
		{Name: "air2", Bins: []string{"air"}, Manager: "npm", Pkg: "air2"},
		{Name: "evil", Bins: []string{"$(id)"}, Manager: "npm", Pkg: "evil"},
		{Name: "--global", Bins: []string{"x"}, Manager: "npm", Pkg: "--global"},
		{Name: "v", Bins: []string{"vv"}, Manager: "npm", Pkg: "v", Version: "1.0; rm -rf ~"},
	})
	var names []string
	for _, it := range got {
		names = append(names, it.Name+"@"+it.Version)
	}
	if want := []string{"air@v1.61.7", "v@"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v, want %v", names, want)
	}
}

// The part travels when the list's hash is not the guest's marker, the
// notices part when the probe saw notices, and nothing otherwise.
func TestAddToolsPart(t *testing.T) {
	tc := newToolsCarry([]toolItem{{Name: "air", Bins: []string{"air"}, Manager: "go", Pkg: "github.com/air-verse/air", Version: "v1.61.7", From: "laptop"}}, nil)
	for _, c := range []struct {
		markers map[string]string
		want    []string
	}{
		{nil, []string{"tools"}},
		{map[string]string{"tools": "old"}, []string{"tools"}},
		{map[string]string{"tools": tc.Wanted.Hash}, nil},
		{map[string]string{"tools": tc.Wanted.Hash, "tools-notices": "waiting"}, []string{"tools-notices"}},
	} {
		p := newGuestPayload()
		got, err := addToolsPart(p, tc, carryOptions{Markers: c.markers})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("markers %v: sent %v, want %v", c.markers, got, c.want)
		}
	}
	if newToolsCarry(nil, &scanResult{}) != nil {
		t.Fatal("an empty list is still sent")
	}
	// The hash is the list's: the same list, the same hash.
	again := newToolsCarry(tc.Wanted.Items, nil)
	if again.Wanted.Hash != tc.Wanted.Hash {
		t.Fatal("hash is not deterministic")
	}
}

func TestCarryOutcomeInstallingLine(t *testing.T) {
	var o carryOutcome
	o.parse("#installing air portless typescript\n#warn Could not install tinygo: no nixpkgs package has bin/tinygo\n")
	lines := o.Lines()
	want := []string{
		"Installing 3 of your tools in the background: air, portless, typescript",
		"Could not install tinygo: no nixpkgs package has bin/tinygo",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines = %q", lines)
	}
}

// Through the local sshd: the list lands in ~/.repose, the installer's
// plan answer becomes the one line, the marker the installer writes stops
// the next carry from sending the list, and a notice is said once.
func TestToolsCarryOverSSH(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	bin := t.TempDir()
	// A stand-in for the base's installer: plan names what it would
	// install and, as the unit would when its pass ends, writes the marker.
	writeFile(t, filepath.Join(bin, "repose-tools-install"), `#!/bin/sh
[ "$1" = plan ] || exit 64
echo "#installing $(jq -r '[.items[].name] | join(" ")' ~/.repose/tools-wanted.json)"
mkdir -p ~/.repose/carry
jq -r .hash ~/.repose/tools-wanted.json > ~/.repose/carry/tools
`)
	if err := os.Chmod(filepath.Join(bin, "repose-tools-install"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	tc := newToolsCarry([]toolItem{
		{Name: "air", Bins: []string{"air"}, Manager: "go", Pkg: "github.com/air-verse/air", Version: "v1.61.7", From: "laptop"},
		{Name: "portless", Bins: []string{"portless"}, Manager: "npm", Pkg: "portless", From: "project"},
	}, nil)

	markers := func() map[string]string {
		out, err := runSSH(ctx, f.target, markerScript(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return parseMarkers(string(out))
	}
	_, o, err := syncCredentialsAndCarry(ctx, f.target, t.TempDir(), f.local, credSyncOptions{}, carryOptions{Tools: tc, Markers: markers()})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(o.Installing, []string{"air", "portless"}) {
		t.Fatalf("outcome = %+v", o)
	}
	var w toolsWanted
	b, err := os.ReadFile(filepath.Join(f.guestHome, ".repose", "tools-wanted.json"))
	if err != nil || json.Unmarshal(b, &w) != nil || w.Hash != tc.Wanted.Hash || len(w.Items) != 2 {
		t.Fatalf("tools-wanted.json = %s (%v)", b, err)
	}

	// Unchanged list: not sent, nothing said.
	_, o, err = syncCredentialsAndCarry(ctx, f.target, t.TempDir(), f.local, credSyncOptions{}, carryOptions{Tools: tc, Markers: markers()})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Sent) != 0 || len(o.Lines()) != 0 {
		t.Fatalf("an unchanged list was sent: %+v", o)
	}

	// A notice from the installer is said once.
	writeFile(t, filepath.Join(f.guestHome, ".repose", "tools-notices"), "Could not install air: cgo: C compiler \"gcc\" not found\n")
	m := markers()
	if m["tools-notices"] == "" {
		t.Fatalf("probe missed the notices: %v", m)
	}
	_, o, err = syncCredentialsAndCarry(ctx, f.target, t.TempDir(), f.local, credSyncOptions{}, carryOptions{Tools: tc, Markers: m})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`Could not install air: cgo: C compiler "gcc" not found`}; !reflect.DeepEqual(o.Warnings, want) {
		t.Fatalf("warnings = %q", o.Warnings)
	}
	if m := markers(); m["tools-notices"] != "" {
		t.Fatal("the notice would be said again")
	}
}

// The exact part scripts and a list for the guest NixOS test
// (nix/guest/tests/default.nix guest-tools-carry), which runs them as dev
// on a real base. -update rewrites them.
func TestToolsGuestPartsGolden(t *testing.T) {
	tc := newToolsCarry([]toolItem{
		{Name: "fake-tool", Bins: []string{"fake-tool"}, Manager: "npm", Pkg: "fake-tool", Version: "1.0.0", From: "laptop"},
		{Name: "greet", Bins: []string{"greet"}, Manager: "go", Pkg: "example.com/greet/cmd/greet", Version: "v1.0.0", From: "laptop"},
	}, &scanResult{
		Candidates: []toolItem{
			{Name: "hello", Bins: []string{"hello"}, From: "project"},
			{Name: "nonexistent-cmd", Bins: []string{"nonexistent-cmd"}, From: "project"},
		},
		Node: &scanVersion{Tool: "node", Major: "22"},
		Ruby: &scanVersion{Tool: "ruby", Major: "3.3"},
		Java: &scanVersion{Tool: "java", Major: "21"},
	})
	files := map[string][]byte{
		"tools.sh":          []byte(toolsPart),
		"tools-notices.sh":  []byte(toolsNoticesPart),
		"tools/wanted.json": tc.JSON,
	}
	for rel, want := range files {
		p := filepath.Join(guestPartsDir, rel)
		if *updateGolden {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is stale; run this test with -update (%v)", p, err)
		}
	}
}

// A base without the installer: the list lands, nothing is said, no
// marker, so the list travels again after a base upgrade.
func TestToolsCarryOldBase(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	t.Setenv("PATH", "/usr/bin:/bin")
	tc := newToolsCarry([]toolItem{{Name: "air", Bins: []string{"air"}, Manager: "go", Pkg: "github.com/air-verse/air", From: "laptop"}}, nil)
	_, o, err := syncCredentialsAndCarry(ctx, f.target, t.TempDir(), f.local, credSyncOptions{}, carryOptions{Tools: tc, Markers: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Failed) != 0 || len(o.Lines()) != 0 {
		t.Fatalf("outcome = %+v", o)
	}
	if _, err := os.Stat(filepath.Join(f.guestHome, ".repose", "carry", "tools")); err == nil {
		t.Fatal("a marker was written without an installer")
	}
}

// The laptop side's cost: the whole read (every manager, the project
// scan) on the owner's kind of laptop state. BenchmarkBuildToolsCarry
// prints the number the budget is checked against (~20 ms).
func BenchmarkBuildToolsCarry(b *testing.B) {
	home, _ := os.UserHomeDir()
	repo := os.Getenv("REPOSE_BENCH_REPO")
	if repo == "" {
		repo, _ = filepath.Abs("../..")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildToolsCarry(home, repo, toolPrecedence{})
	}
}

// A ceiling a regression would cross by far: 20 ms is the budget, and the
// fake laptop plus a fixture monorepo read in well under a millisecond.
func TestBuildToolsCarryIsFast(t *testing.T) {
	te, _ := fakeLaptop(t)
	start := time.Now()
	for i := 0; i < 20; i++ {
		_ = newToolsCarry(readGlobalTools(te), scanProject(filepath.Join("testdata", "scan", "monorepo")))
	}
	if per := time.Since(start) / 20; per > 20*time.Millisecond {
		t.Fatalf("one read took %s", per)
	}
}
