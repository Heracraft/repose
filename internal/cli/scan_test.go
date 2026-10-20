package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The scan of each fixture, as `repose scan` prints it (the laptop part
// empty), against testdata/scan/<name>.golden. -update rewrites them.
func TestScanFixtures(t *testing.T) {
	for _, name := range []string{"monorepo", "goproj", "rustproj", "uvproj", "railsproj"} {
		t.Run(name, func(t *testing.T) {
			sc := scanProject(filepath.Join("testdata", "scan", name))
			var out bytes.Buffer
			printScan(&out, name, nil, sc, toolPrecedence{}, scanPersonal{})
			golden := filepath.Join("testdata", "scan", name+".golden")
			if *updateGolden {
				if err := os.WriteFile(golden, out.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out.Bytes(), want) {
				t.Fatalf("scan of %s:\n%s\nwant:\n%s", name, out.Bytes(), want)
			}
			// Deterministic: a second scan is byte for byte the same.
			var again bytes.Buffer
			printScan(&again, name, nil, scanProject(filepath.Join("testdata", "scan", name)), toolPrecedence{}, scanPersonal{})
			if !bytes.Equal(again.Bytes(), out.Bytes()) {
				t.Fatal("two scans differ")
			}
		})
	}
}

// The monorepo's list is exactly what the owner's session lacked: air and
// portless, plus the commands no dependency provides.
func TestScanMonorepoCandidates(t *testing.T) {
	sc := scanProject(filepath.Join("testdata", "scan", "monorepo"))
	var names []string
	for _, c := range sc.Candidates {
		names = append(names, c.Name)
	}
	want := []string{"air", "pagefind", "portless", "rimraf", "stripe", "tinygo"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("candidates = %v, want %v", names, want)
	}
	for _, c := range sc.Candidates {
		if c.Name == "portless" && (c.Manager != "npm" || c.Pkg != "portless") {
			t.Fatalf("portless = %+v", c)
		}
	}
	if sc.Node == nil || sc.Node.Major != "22" {
		t.Fatalf("node = %+v", sc.Node)
	}
}

// A command in node_modules/.bin of the workspace or the root is the
// project's.
func TestScanNodeModulesBin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"a":"sometool x","b":"othertool"}}`)
	writeFile(t, filepath.Join(dir, "node_modules", ".bin", "sometool"), "")
	sc := scanProject(dir)
	if len(sc.Candidates) != 1 || sc.Candidates[0].Name != "othertool" {
		t.Fatalf("candidates = %+v", sc.Candidates)
	}
}

// One checkout against the three machines the scan can be talking
// about. The base's tool list is a promise about a machine repose runs
// and about no other: on a machine the user owns, a command counts as
// there when the box itself said so, and when the box said nothing the
// listing falls back to the base's list, which the tail names.
func TestScanPerMachine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"dev":"node server.js && make build && air"}}`)
	for _, c := range []struct {
		name       string
		mach       scanMachine
		candidates []string
		skipped    []string
	}{
		{
			name:       "a machine repose runs",
			mach:       scanMachine{},
			candidates: []string{"air"},
			skipped:    []string{"make:" + baseSkipReason, "node:" + baseSkipReason},
		},
		{
			name:       "a machine the user owns, which answered",
			mach:       scanMachine{Owned: true, Name: "homebox", Asked: true, Have: map[string]bool{"make": true}},
			candidates: []string{"air", "node"},
			skipped:    []string{"make:already on homebox"},
		},
		{
			name:       "a machine the user owns, which did not",
			mach:       scanMachine{Owned: true, Name: "homebox", Why: scanNotAsked("homebox", "no answer")},
			candidates: []string{"air"},
			skipped:    []string{"make:" + baseSkipReason, "node:" + baseSkipReason},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			sc := scanProjectOn(dir, c.mach)
			var cands []string
			for _, x := range sc.Candidates {
				cands = append(cands, x.Name)
			}
			if !reflect.DeepEqual(cands, c.candidates) {
				t.Errorf("candidates = %v, want %v", cands, c.candidates)
			}
			var skips []string
			for _, x := range sc.Skipped {
				skips = append(skips, x.Name+":"+x.Why)
			}
			if !reflect.DeepEqual(skips, c.skipped) {
				t.Errorf("skipped = %v, want %v", skips, c.skipped)
			}
		})
	}
}

// The listing for a machine the user owns: every command says the
// machine has not got it, the tail names the machine and promises no
// install, and a machine that did not answer is named with the list that
// stood in for it.
func TestScanOwnedListing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"dev":"node server.js && air"}}`)
	answered := scanMachine{Owned: true, Name: "homebox", Asked: true}
	var out bytes.Buffer
	printScan(&out, dir, nil, scanProjectOn(dir, answered), toolPrecedence{}, scanPersonal{})
	for _, want := range []string{"air                  not on homebox", "node                 not on homebox", "homebox has not got 2 of them."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the listing has no %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "installed in the background") {
		t.Errorf("the listing promises a guest's installer:\n%s", out.String())
	}
	out.Reset()
	silent := scanMachine{Owned: true, Name: "homebox", Why: scanNotAsked("homebox", "no answer")}
	printScan(&out, dir, nil, scanProjectOn(dir, silent), toolPrecedence{}, scanPersonal{})
	if want := "1 to check on homebox. homebox did not answer (no answer), so the commands above are the ones a machine repose runs has."; !strings.Contains(out.String(), want) {
		t.Errorf("the listing has no %q:\n%s", want, out.String())
	}
}

// A node pin that names no one major is said, and on a machine the user
// owns the line says whether that box has a node at all: with none, the
// project has no runtime, which is the difference between its own
// scripts running and not.
func TestScanUnresolvableNodePin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"engines":{"node":"^20 || ^22"}}`)
	for _, c := range []struct {
		mach scanMachine
		note string
	}{
		{scanMachine{}, "pins no one major; the machine's node is kept"},
		{scanMachine{Owned: true, Name: "homebox"}, "pins no one major, so none is installed"},
		{scanMachine{Owned: true, Name: "homebox", Asked: true, Have: map[string]bool{"node": true}}, "pins no one major, so none is installed; homebox has node"},
		{scanMachine{Owned: true, Name: "homebox", Asked: true}, "pins no one major, so none is installed; homebox has no node"},
	} {
		sc := scanProjectOn(dir, c.mach)
		if sc.Node != nil {
			t.Fatalf("a pin naming no major became node %+v", sc.Node)
		}
		if len(sc.Versions) != 1 || sc.Versions[0].Note != c.note {
			t.Fatalf("versions = %+v, want the note %q", sc.Versions, c.note)
		}
	}
	// The scan prints it, which is the whole point of resolving nothing.
	var out bytes.Buffer
	sc := scanProjectOn(dir, scanMachine{Owned: true, Name: "homebox", Asked: true})
	printScan(&out, dir, nil, sc, toolPrecedence{}, scanPersonal{})
	if !strings.Contains(out.String(), "homebox has no node") {
		t.Fatalf("the listing does not say the pin resolved to nothing:\n%s", out.String())
	}
}

// What the machine is asked about: the laptop's commands, the project's,
// and node. A command no box lacks is not asked about, and a name that
// could be read as a shell word never travels.
func TestScanAskNames(t *testing.T) {
	laptop := []toolItem{
		{Name: "typescript", Bins: []string{"tsc", "tsserver"}},
		{Name: "evil", Bins: []string{"$(id)"}},
	}
	sc := &scanResult{Candidates: []toolItem{{Name: "air", Bins: []string{"air"}}, {Name: "ls", Bins: []string{"ls"}}}}
	want := []string{"air", "node", "tsc", "tsserver"}
	if got := scanAskNames(laptop, sc); !reflect.DeepEqual(got, want) {
		t.Fatalf("scanAskNames = %v, want %v", got, want)
	}
}

func TestNodeMajor(t *testing.T) {
	for in, want := range map[string]string{
		"22": "22", "v22.3.0": "22", "22.x": "22", "^22.1.0": "22", "~22": "22",
		">=22 <23": "22", ">=22.0.0 <23.0.0": "22", "22.1.0 - 22.9.0": "22",
		">=18": "", "lts/*": "", "^20 || ^22": "", ">=20 <23": "", "node": "", "": "",
	} {
		if got := nodeMajor(in); got != want {
			t.Errorf("nodeMajor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShellCommands(t *testing.T) {
	for in, want := range map[string][]string{
		"GOOS=js GOARCH=wasm tinygo build -o x":    {"tinygo"},
		"cd core && make build || echo no":         {"cd", "make", "echo"},
		"npx only-allow pnpm":                      nil,
		"pnpm exec biome check":                    {"pnpm", "biome"},
		`concurrently "air" "pnpm dev"`:            {"concurrently", "air", "pnpm"},
		"dotenv -e .env -- drizzle-kit push":       {"drizzle-kit"},
		"cross-env NODE_ENV=production next build": {"next"},
		"./scripts/x.sh && node x.js":              {"node"},
		"$(MAKE) lint; ${TOOL} run":                nil,
		"cargo run {{ARGS}}":                       {"cargo"},
		"echo hi > /dev/null 2>&1 | sort":          {"echo", "sort"},
		"FOO=1 BAR=2":                              nil,
		"# a comment":                              nil,
		"a 'b; c' d":                               {"a"},
	} {
		if got := shellCommands(in); !reflect.DeepEqual(got, want) {
			t.Errorf("shellCommands(%q) = %q, want %q", in, got, want)
		}
	}
}
