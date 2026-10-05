package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
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
