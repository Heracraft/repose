package undo

import (
	"context"
	"errors"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// dbBytes stands in for the dev database an agent drops, which is the
// thing a file-level checkpoint cannot bring back
// (docs/research/wrapper-undo.md).
const dbBytes = 32 << 20

// TestBtrfsStore is the measured half of the checklist: the mechanism
// against a real btrfs store, with the timings in the test output. It
// needs a store prepared by ops/dev/undo-testfs.sh — nothing it asserts
// is something a fake runner could tell the truth about.
func TestBtrfsStore(t *testing.T) {
	store := os.Getenv("REPOSE_UNDO_TESTFS")
	if store == "" {
		t.Skip("REPOSE_UNDO_TESTFS is unset: `sudo ops/dev/undo-testfs.sh --up` prepares a btrfs store and prints its path")
	}
	ctx := context.Background()
	b := New(store, Exec{})
	if err := b.Provision(ctx, 0); err != nil {
		t.Fatalf("provision: %v", err)
	}
	ready, err := b.Ready(ctx)
	if err != nil || !ready {
		t.Fatalf("the store is not ready: %v", err)
	}
	project := "undo-test-" + NewPointName()[2:14]
	live, err := b.Init(ctx, project)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { removeProject(t, b, project) })

	files := treeFiles(t)
	t0 := time.Now()
	buildTree(t, live, files)
	t.Logf("a tree of %d files and a %d MB database: built in %s", countFiles(t, live), dbBytes>>20, since(t0))

	// A point of the whole tree. The measured figure is 0.13 s; the bound
	// is loose because a slow disk is not a broken mechanism, and a copy
	// instead of a snapshot would be minutes.
	t0 = time.Now()
	first, err := b.Take(ctx, project, "r-first", ReasonRun)
	took := since(t0)
	t.Logf("point of %d files: %s", files, took)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if took > 5*time.Second {
		t.Errorf("a point took %s; a read-only subvolume snapshot is a tenth of a second", took)
	}
	if first.Reason != ReasonRun || first.TakenAt.IsZero() {
		t.Errorf("point %+v", first)
	}

	// A repeat of the same name is the same point, and takes nothing.
	again, err := b.Take(ctx, project, "r-first", ReasonRun)
	if err != nil {
		t.Fatalf("take again: %v", err)
	}
	if !again.TakenAt.Equal(first.TakenAt) {
		t.Errorf("a repeated take moved the point: %s then %s", first.TakenAt, again.TakenAt)
	}

	// A fork is a writable snapshot: no bytes copied.
	fork := project + "-fork"
	t0 = time.Now()
	err = b.Fork(ctx, project, "r-first", fork)
	forked := since(t0)
	t.Logf("fork of %d files: %s", files, forked)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	t.Cleanup(func() { removeProject(t, b, fork) })
	if forked > 5*time.Second {
		t.Errorf("a fork took %s; a writable snapshot copies nothing", forked)
	}
	if n := countFiles(t, b.livePath(fork)); n != countFiles(t, live) {
		t.Errorf("the fork has %d files, the project has %d", n, countFiles(t, live))
	}
	if err := b.Fork(ctx, project, "r-first", fork); !errors.Is(err, ErrExists) {
		t.Errorf("forking onto a project that is there: %v, want ErrExists", err)
	}

	// A rewind refuses a project something is still writing in.
	fd, err := os.Open(filepath.Join(live, "src", "f0000.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Rewind(ctx, project, "r-first"); !errors.Is(err, ErrBusy) {
		t.Errorf("rewind of a busy project: %v, want ErrBusy", err)
	}
	if err := fd.Close(); err != nil {
		t.Fatal(err)
	}

	// An agent wrecks the project: a directory gone, the database
	// overwritten, a file replaced.
	if err := os.RemoveAll(filepath.Join(live, "src")); err != nil {
		t.Fatal(err)
	}
	if err := overwrite(filepath.Join(live, "app.db"), 8<<20); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "package.json"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrecked := countFiles(t, live)
	t.Logf("wrecked: %d files left of %d", wrecked, files)

	t0 = time.Now()
	safety, err := b.Rewind(ctx, project, "r-first")
	rewound := since(t0)
	t.Logf("rewind of %d files: %s", files, rewound)
	if err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if rewound > 60*time.Second {
		t.Errorf("a rewind took %s; the measured figure for 37,000 files is 2.19 s", rewound)
	}
	if n := countFiles(t, live); n != files+2 {
		t.Errorf("%d files after the rewind, want %d", n, files+2)
	}
	if fi, err := os.Stat(filepath.Join(live, "app.db")); err != nil || fi.Size() != dbBytes {
		t.Errorf("the database is %v: %v", fi, err)
	}
	if buf, err := os.ReadFile(filepath.Join(live, "package.json")); err != nil || string(buf) != packageJSON {
		t.Errorf("package.json is %q: %v", buf, err)
	}
	// The pre-rewind state is a point of its own, so the rewind is undoable.
	if n := countFiles(t, b.pointPath(project, safety)); n != wrecked {
		t.Errorf("the safety point holds %d files, the wrecked project had %d", n, wrecked)
	}

	// Points are newest first, and the store's figures come from btrfs.
	ps, err := b.List(ctx, project)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ps) != 2 || ps[0].Name != safety || ps[1].Name != "r-first" {
		t.Fatalf("points %+v", ps)
	}
	sp, err := b.Space(ctx)
	if err != nil {
		t.Fatalf("space: %v", err)
	}
	t.Logf("store: %d bytes total, %d used, %d free, point bytes %d", sp.Total, sp.Used, sp.Free, ps[0].Bytes)
	if sp.Total == 0 || sp.Used == 0 || sp.Free == 0 {
		t.Errorf("space %+v", sp)
	}

	// GC under a mark nothing can be below takes the oldest point and
	// leaves the newest. It collects every project in the store, so a
	// store shared with another run loses that run's older points too.
	gone, err := b.GC(ctx, 1)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	t.Logf("gc deleted %d points", len(gone))
	left, err := b.List(ctx, project)
	if err != nil {
		t.Fatalf("list after gc: %v", err)
	}
	if len(left) != 1 || left[0].Name != safety {
		t.Fatalf("after gc the points are %+v, want only %s", left, safety)
	}
	if err := b.Delete(ctx, project, "r-first"); err != nil {
		t.Errorf("deleting a point that has gone: %v", err)
	}
}

func since(t0 time.Time) time.Duration { return time.Since(t0).Round(time.Millisecond) }

// treeFiles is how many files the tree holds: the checklist's 30,000,
// or REPOSE_UNDO_TESTFS_FILES for a quicker run.
func treeFiles(t *testing.T) int {
	t.Helper()
	if s := os.Getenv("REPOSE_UNDO_TESTFS_FILES"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 10 {
			t.Fatalf("REPOSE_UNDO_TESTFS_FILES %q", s)
		}
		return n
	}
	return 30000
}

const packageJSON = "{\n  \"name\": \"undo-test\"\n}\n"

// buildTree writes a project an agent would work in: many small files in
// package directories, some TypeScript, and a database that does not
// compress.
func buildTree(t *testing.T, live string, files int) {
	t.Helper()
	src := filepath.Join(live, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := files / 60
	if ts < 1 {
		ts = 1
	}
	for i := 0; i < ts; i++ {
		name := filepath.Join(src, "f"+pad(i)+".ts")
		if err := os.WriteFile(name, []byte("export const f"+strconv.Itoa(i)+" = "+strconv.Itoa(i)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rest := files - ts
	const perDir = 20
	for i := 0; i < rest; i++ {
		if i%perDir == 0 {
			if err := os.MkdirAll(filepath.Join(live, "node_modules", "pkg"+pad(i/perDir), "lib"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		name := filepath.Join(live, "node_modules", "pkg"+pad(i/perDir), "lib", "m"+pad(i%perDir)+".js")
		if err := os.WriteFile(name, []byte(strings.Repeat("x", 1024+i%4096)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(live, "package.json"), []byte(packageJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := os.Create(filepath.Join(live, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	buf := make([]byte, 1<<20)
	rnd := rand.New(rand.NewSource(1))
	for written := 0; written < dbBytes; written += len(buf) {
		_, _ = rnd.Read(buf)
		if _, err := db.Write(buf); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Sync(); err != nil {
		t.Fatal(err)
	}
}

func pad(i int) string {
	s := strconv.Itoa(i)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// overwrite replaces the first n bytes of a file, as an agent's migration
// does to a database.
func overwrite(path string, n int) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, n)
	_, _ = rand.New(rand.NewSource(2)).Read(buf)
	if _, err := f.Write(buf); err != nil {
		return err
	}
	return f.Sync()
}

func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	if err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// removeProject takes a test's project out of the store: its points, its
// live area, and anything a rewind left behind.
func removeProject(t *testing.T, b *Btrfs, project string) {
	t.Helper()
	ctx := context.Background()
	ps, err := b.List(ctx, project)
	if err != nil {
		t.Errorf("cleanup: list %s: %v", project, err)
	}
	for _, p := range ps {
		if err := b.Delete(ctx, project, p.Name); err != nil {
			t.Errorf("cleanup: delete %s: %v", p.Name, err)
		}
	}
	ents, err := os.ReadDir(b.projectsDir())
	if err != nil {
		t.Errorf("cleanup: %v", err)
		return
	}
	for _, e := range ents {
		n := e.Name()
		if n != project && !strings.HasPrefix(n, stagingPrefix+project) && !strings.HasPrefix(n, asidePrefix+project) {
			continue
		}
		if err := b.deleteSubvolume(ctx, filepath.Join(b.projectsDir(), n), false); err != nil {
			t.Errorf("cleanup: delete the live area of %s: %v", project, err)
		}
	}
	if err := os.RemoveAll(b.pointsDir(project)); err != nil {
		t.Errorf("cleanup: %v", err)
	}
}
