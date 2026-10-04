package storeview

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/heracraft/repose/internal/hostd/shell"
	"github.com/heracraft/repose/internal/hostd/systemd"
)

func TestMain(m *testing.M) {
	if dir := os.Getenv("STOREVIEW_CHILD"); dir != "" {
		child(dir)
		return
	}
	os.Exit(m.Run())
}

// child stands in for virtiofsd: in a mount namespace of its own it
// pivots into an empty tmpfs, says so, and waits.
func child(dir string) {
	must := func(err error) {
		if err != nil {
			fmt.Println("child:", err)
			os.Exit(1)
		}
	}
	must(unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""))
	must(unix.Mount("view", dir, "tmpfs", 0, "mode=0755"))
	must(unix.Chdir(dir))
	must(unix.Mkdir("old", 0o700))
	must(unix.PivotRoot(".", "old"))
	must(unix.Unmount("old", unix.MNT_DETACH))
	must(unix.Rmdir("old"))
	must(unix.Chdir("/"))
	fmt.Println("ready")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func TestStoreName(t *testing.T) {
	for p, ok := range map[string]bool{
		"/nix/store/2ndah67h0z5m31v2wkdmg2md4380ggr5-bash-interactive-5.3p15": true,
		"/nix/store/pyghkr26krhfj4hbr3xz2sh3xbip048s-fonts.conf":              true,
		"/nix/store/pyghkr26krhfj4hbr3xz2sh3xbip048s-fonts.conf/x":            false,
		"/nix/store/.links":                             false,
		"/nix/store/../etc":                             false,
		"/nix/store/eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee-x": false,
		"/etc/2ndah67h0z5m31v2wkdmg2md4380ggr5-bash":    false,
	} {
		if _, got := storeName(p); got != ok {
			t.Errorf("%s: %v, want %v", p, got, ok)
		}
	}
}

// A guest's view starts empty and holds exactly what hostd binds: the
// directory, file and symlink store paths of its closure, read-only and
// private, and nothing else of the host store. Binding again is a no-op.
// Needs root (open_tree, setns); run as `sudo storeview.test`.
func TestBindIntoAView(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	dir, file, link := sampleStorePaths(t)
	viewDir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "STOREVIEW_CHILD="+viewDir)
	cmd.SysProcAttr = &unix.SysProcAttr{Unshareflags: unix.CLONE_NEWNS}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	if line, _ := bufio.NewReader(stdout).ReadString('\n'); strings.TrimSpace(line) != "ready" {
		t.Fatalf("child: %q", line)
	}
	pid := cmd.Process.Pid

	sd := systemd.NewFake()
	sd.Units["virtiofsd@g"] = &systemd.FakeUnit{Active: true, MainPID: pid}
	v := &Real{SD: sd}
	paths := []string{dir, file}
	if link != "" {
		paths = append(paths, link)
	}
	if err := v.Populate(context.Background(), "virtiofsd@g", paths); err != nil {
		t.Fatal(err)
	}
	if err := v.Populate(context.Background(), "virtiofsd@g", paths); err != nil {
		t.Fatalf("second populate: %v", err)
	}
	root := fmt.Sprintf("/proc/%d/root", pid)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(paths) {
		t.Fatalf("view holds %d entries, want %d", len(entries), len(paths))
	}
	want, _ := os.ReadDir(dir)
	got, err := os.ReadDir(filepath.Join(root, filepath.Base(dir)))
	if err != nil || len(got) != len(want) {
		t.Fatalf("bound directory: %d entries, want %d (%v)", len(got), len(want), err)
	}
	wantFile, _ := os.ReadFile(file)
	if gotFile, err := os.ReadFile(filepath.Join(root, filepath.Base(file))); err != nil || string(gotFile) != string(wantFile) {
		t.Fatalf("bound file differs: %v", err)
	}
	if link != "" {
		wantLink, _ := os.Readlink(link)
		if got, err := os.Readlink(filepath.Join(root, filepath.Base(link))); err != nil || got != wantLink {
			t.Fatalf("link %q, want %q (%v)", got, wantLink, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, filepath.Base(dir), "x"), nil, 0o600); err == nil {
		t.Fatal("bound store path is writable")
	}
	mi, err := os.ReadFile(fmt.Sprintf("/proc/%d/mountinfo", pid))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(strings.TrimSpace(string(mi)), "\n") {
		if strings.Contains(l, "shared:") {
			t.Fatalf("a view mount propagates: %s", l)
		}
	}
}

// sampleStorePaths finds a directory, a regular file and (if any) a
// symlink among the top-level paths of the current system's closure.
func sampleStorePaths(t *testing.T) (dir, file, link string) {
	out, err := exec.Command("nix-store", "-qR", "/run/current-system").Output()
	if err != nil {
		t.Skipf("nix-store: %v", err)
	}
	for _, p := range strings.Fields(string(out)) {
		fi, err := os.Lstat(p)
		if err != nil {
			continue
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0 && link == "":
			link = p
		case fi.IsDir() && dir == "":
			dir = p
		case fi.Mode().IsRegular() && file == "":
			file = p
		}
	}
	if dir == "" || file == "" {
		t.Skip("no directory and file store paths to bind")
	}
	return dir, file, link
}

// A unit whose virtiofsd pivoted into something other than a tmpfs is a
// pre-I-463 store export: Populate leaves it alone.
func TestPopulateLeavesALegacyExportAlone(t *testing.T) {
	proc := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proc, "42"), 0o755); err != nil {
		t.Fatal(err)
	}
	// /proc/42/root -> a directory on the test's filesystem, not a tmpfs.
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(proc, "42", "root")); err != nil {
		t.Fatal(err)
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(target, &fs); err != nil || fs.Type == unix.TMPFS_MAGIC {
		t.Skip("the test's temp directory is a tmpfs")
	}
	sd := systemd.NewFake()
	sd.Units["virtiofsd@g"] = &systemd.FakeUnit{Active: true, MainPID: 42}
	if err := (&Real{SD: sd, Proc: proc}).Populate(context.Background(), "virtiofsd@g", []string{"/nix/store/2ndah67h0z5m31v2wkdmg2md4380ggr5-bash"}); err != nil {
		t.Fatal(err)
	}
}

// The real thing: virtiofsd@ started the way hostd starts it, with
// UnitProps and --sandbox namespace, then populated. Root only, and only
// when REPOSE_TEST_VIRTIOFSD names a virtiofsd binary.
func TestVirtiofsdServesTheView(t *testing.T) {
	bin := os.Getenv("REPOSE_TEST_VIRTIOFSD")
	if os.Geteuid() != 0 || bin == "" {
		t.Skip("needs root and REPOSE_TEST_VIRTIOFSD")
	}
	dir, file, _ := sampleStorePaths(t)
	sockDir, err := os.MkdirTemp("/run", "storeview-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	if err := os.Chmod(sockDir, 0o777); err != nil {
		t.Fatal(err)
	}
	unit := "repose-storeview-test"
	sd := systemd.NewReal(shell.Exec{})
	ctx := context.Background()
	props := append([]string{"User=nobody", "Group=nogroup"}, UnitProps()...)
	argv := []string{bin, "--socket-path", filepath.Join(sockDir, "v.sock"), "--shared-dir", Dir, "--sandbox", "namespace", "--cache", "auto", "--no-announce-submounts"}
	if err := sd.Run(ctx, unit, props, argv); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sd.Stop(ctx, unit) }()
	v := &Real{SD: sd}
	if err := v.Populate(ctx, unit, []string{dir}); err != nil {
		t.Fatal(err)
	}
	// A later switch adds a path while virtiofsd serves.
	if err := v.Populate(ctx, unit, []string{dir, file}); err != nil {
		t.Fatal(err)
	}
	m, err := sd.Show(ctx, unit, "MainPID")
	if err != nil {
		t.Fatal(err)
	}
	root := "/proc/" + m["MainPID"] + "/root"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != strings.Join(sortedBase(dir, file), " ") {
		t.Fatalf("virtiofsd serves %v", names)
	}
	// A whole system closure, as a guest boot binds it.
	out, err := exec.Command("nix-store", "-qR", "/run/current-system").Output()
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Fields(string(out))
	start := time.Now()
	if err := v.Populate(ctx, unit, all); err != nil {
		t.Fatal(err)
	}
	t.Logf("bound %d paths in %s", len(all), time.Since(start))
	if entries, _ := os.ReadDir(root); len(entries) != len(all) {
		t.Fatalf("view holds %d entries after the full closure, want %d", len(entries), len(all))
	}
	if _, err := os.Stat(Dir); err == nil {
		if ents, _ := os.ReadDir(Dir); len(ents) != 0 {
			t.Fatalf("the view leaked into the host namespace: %v", ents)
		}
	}
	mi, _ := os.ReadFile("/proc/self/mountinfo")
	if strings.Contains(string(mi), Dir) {
		t.Fatal("the view is mounted in the host namespace")
	}
}

func sortedBase(paths ...string) []string {
	var out []string
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	slices.Sort(out)
	return out
}
