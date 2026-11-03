package testdb_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryTestdbPackageStopsItsCluster asserts that every package which
// reaches a throwaway Postgres cluster also ends it.
//
// testdb starts one cluster per test process and testdb.Run is what stops it
// when the package's tests end. A package that omits TestMain leaks its
// cluster on every ordinary, passing run -- not only on a crash. On
// 2026-11-03 nineteen of the twenty packages that touch testdb or apitest
// carried the line and internal/api/resting did not, so it had been leaking
// one cluster per run for as long as it existed, invisibly: a leaked cluster
// makes nothing fail. Twenty-five were alive on the dev box that day, 2.3 GB
// under TMPDIR, on a machine that had just killed two background tasks for
// want of memory (DECISIONS I-998).
//
// Scoped to packages with at least one _test.go: internal/api/apitest holds
// only non-test files and would otherwise read as a violation.
func TestEveryTestdbPackageStopsItsCluster(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	var checked int
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case ".git", "node_modules", "result", ".direnv":
			return filepath.SkipDir
		}
		ents, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		var tests []string
		var usesTestdb, hasMain bool
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(path, e.Name()))
			if err != nil {
				continue
			}
			src := string(b)
			if strings.Contains(src, "repose/internal/db/testdb") || strings.Contains(src, "repose/internal/api/apitest") {
				usesTestdb = true
			}
			if strings.HasSuffix(e.Name(), "_test.go") {
				tests = append(tests, e.Name())
				if strings.Contains(src, "func TestMain(") {
					hasMain = true
				}
			}
		}
		if !usesTestdb || len(tests) == 0 {
			return nil
		}
		checked++
		if !hasMain {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s reaches a throwaway Postgres cluster and has no TestMain, so it never stops it; add `func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }` (DECISIONS I-998)", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// An empty walk is a broken walk, not a clean tree.
	if checked == 0 {
		t.Fatal("no package was found to use testdb or apitest at all; the walk is wrong")
	}
	t.Logf("checked %d packages that reach a throwaway cluster", checked)
}
