package mcpreg

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/render/*/out from a sync of in/")

// testPaths points a registry at dir/home, with in/platform.json and
// in/secrets when the case has them.
func testPaths(t *testing.T, caseDir, home string) Paths {
	t.Helper()
	p := Paths{
		Home:       home,
		Platform:   filepath.Join(caseDir, "in", "platform.json"),
		SecretsDir: filepath.Join(caseDir, "in", "secrets"),
		SocketDir:  filepath.Join(caseDir, "in", "sockets"),
		Etc:        filepath.Join(caseDir, "in", "root"),
	}
	return p
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// snapshot is every file under dir but sync's lock, by relative path.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == filepath.Join(".repose", "mcp", ".lock") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func keysOf(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// TestRenderGolden syncs every agent from each case's in/home and compares
// the whole home, plus stderr, with out/. A second sync must change no
// byte and print the same warnings.
func TestRenderGolden(t *testing.T) {
	cases, err := os.ReadDir("testdata/render")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if !c.IsDir() {
			continue
		}
		t.Run(c.Name(), func(t *testing.T) {
			caseDir := filepath.Join("testdata", "render", c.Name())
			home := t.TempDir()
			copyTree(t, filepath.Join(caseDir, "in", "home"), home)
			p := testPaths(t, caseDir, home)

			var stderr bytes.Buffer
			Sync(p, Agents, &stderr)
			got := snapshot(t, home)
			got["stderr"] = stderr.String()

			outDir := filepath.Join(caseDir, "out")
			if *updateGolden {
				os.RemoveAll(outDir)
				for rel, body := range got {
					f := filepath.Join(outDir, filepath.FromSlash(rel))
					if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			want := snapshot(t, outDir)
			if strings.Join(keysOf(got), "\n") != strings.Join(keysOf(want), "\n") {
				t.Fatalf("files after sync:\n%s\nwant:\n%s", strings.Join(keysOf(got), "\n"), strings.Join(keysOf(want), "\n"))
			}
			for rel, w := range want {
				if got[rel] != w {
					t.Errorf("%s:\n%s\nwant:\n%s", rel, got[rel], w)
				}
			}

			// Idempotent: the second run leaves every byte as it was.
			var stderr2 bytes.Buffer
			Sync(p, Agents, &stderr2)
			again := snapshot(t, home)
			again["stderr"] = stderr2.String()
			for rel, w := range got {
				if again[rel] != w {
					t.Errorf("second sync changed %s:\n%s\nfirst:\n%s", rel, again[rel], w)
				}
			}
			if len(again) != len(got) {
				t.Errorf("second sync left %d files, first %d", len(again), len(got))
			}
		})
	}
}
