package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

func TestParseCpSide(t *testing.T) {
	for arg, want := range map[string]cpSide{
		":logs/x.log":      {Remote: true, Path: "logs/x.log"},
		"izma:/tmp/t.json": {Remote: true, Project: "izma", Path: "/tmp/t.json"},
		"izma:":            {Remote: true, Project: "izma"},
		".":                {Path: "."},
		"./a:b":            {Path: "./a:b"},
		"/abs/a:b":         {Path: "/abs/a:b"},
		"dir/a:b":          {Path: "dir/a:b"},
		"x.log":            {Path: "x.log"},
		// Windows drives are local (I-621).
		`C:\a.txt`:   {Path: `C:\a.txt`},
		"C:/a.txt":   {Path: "C:/a.txt"},
		"d:notes.md": {Path: "d:notes.md"},
		`dir\a:b`:    {Path: `dir\a:b`},
		"ab:/tmp/x":  {Remote: true, Project: "ab", Path: "/tmp/x"},
	} {
		if got := parseCpSide(arg); got != want {
			t.Errorf("parseCpSide(%q) = %+v, want %+v", arg, got, want)
		}
	}
	for path, want := range map[string]string{"logs/x.log": "izma/logs/x.log", "": "izma", "/tmp/a": "/tmp/a", "~/.bashrc": "~/.bashrc"} {
		if got := (cpSide{Remote: true, Path: path}).guestPath("izma"); got != want {
			t.Errorf("guestPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// I-201 both ways against the local sshd harness: `repose cp :logs/x.log
// .` from the checkout, and the reverse into the project by name.
func TestScpRemoteQuote(t *testing.T) {
	for in, want := range map[string]string{
		"proj/logs/x.log": "proj/logs/x.log",
		"proj/a b $HOME":  `proj/a\ b\ \$HOME`,
		"~/it's":          `~/it\'s`,
		"~":               "~",
		"/tmp/*.log":      "/tmp/*.log",
	} {
		if got := scpRemoteQuote(in); got != want {
			t.Errorf("scpRemoteQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCpBothWays(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	scpExtraArgs = []string{"-O"}
	t.Cleanup(func() { scpExtraArgs = nil })
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(f.guestRepo(), "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "x.log"), []byte("line from the guest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := CpCmd(ctx, f.env, []string{":logs/x.log"}, out, false, ""); err != nil {
		t.Fatalf("cp from the guest: %v %s", err, f.env.ErrOut.(*discardWriter).buf.String())
	}
	if b, err := os.ReadFile(filepath.Join(out, "x.log")); err != nil || string(b) != "line from the guest\n" {
		t.Fatalf("copied = %q %v", b, err)
	}
	local := filepath.Join(out, "trace.json")
	if err := os.WriteFile(local, []byte(`{"from":"laptop"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CpCmd(ctx, f.env, []string{local}, testSlug+":tmp/trace.json", false, ""); err == nil {
		t.Fatal("copy into a directory that does not exist should fail like scp")
	}
	if err := CpCmd(ctx, f.env, []string{local}, testSlug+":logs/", false, ""); err != nil {
		t.Fatalf("cp to the guest: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(logs, "trace.json")); err != nil || string(b) != `{"from":"laptop"}` {
		t.Fatalf("in the guest = %q %v", b, err)
	}
	// The classic protocol (what -O and OpenSSH before 8.8 speak) hands the
	// remote path to the guest's shell: a space or a $ must survive it.
	odd := "a b $HOME.log"
	if err := os.WriteFile(filepath.Join(logs, odd), []byte("odd name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CpCmd(ctx, f.env, []string{":logs/" + odd}, out, false, ""); err != nil {
		t.Fatalf("cp of %q from the guest: %v", odd, err)
	}
	if b, err := os.ReadFile(filepath.Join(out, odd)); err != nil || string(b) != "odd name\n" {
		t.Fatalf("copied %q = %q %v", odd, b, err)
	}
	if err := CpCmd(ctx, f.env, []string{"a"}, "b", false, ""); err == nil || err.(*exitError).code != ExitUsage {
		t.Errorf("two local sides: %v", err)
	}

	// I-346: what a shell glob hands over, several sources into one
	// directory, both ways.
	var fwd []string
	for _, n := range []string{"Fwd_a.pdf", "Fwd_b c.pdf"} {
		p := filepath.Join(out, n)
		if err := os.WriteFile(p, []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
		fwd = append(fwd, p)
	}
	if err := CpCmd(ctx, f.env, fwd, testSlug+":logs/", false, ""); err != nil {
		t.Fatalf("cp of two files to the guest: %v", err)
	}
	back := t.TempDir()
	if err := CpCmd(ctx, f.env, []string{":logs/Fwd_a.pdf", ":logs/Fwd_b c.pdf"}, back, false, ""); err != nil {
		t.Fatalf("cp of two files from the guest: %v", err)
	}
	for _, n := range []string{"Fwd_a.pdf", "Fwd_b c.pdf"} {
		if b, err := os.ReadFile(filepath.Join(back, n)); err != nil || string(b) != n {
			t.Errorf("%s round trip = %q %v", n, b, err)
		}
	}
	for _, c := range []struct {
		srcs []string
		dst  string
		want string
	}{
		{[]string{"a", ":b"}, ".", "different sides"},
		{[]string{"izma:a", "other:b"}, ".", "two projects"},
		{[]string{"./Fwd_a.pdf", "./Fwd_b.pdf"}, "/tmp", "Every argument"},
		{[]string{"./Fwd_a.pdf"}, ":" + testSlug + ":/tmp/", "write " + testSlug + ":/tmp/"},
	} {
		err := CpCmd(ctx, f.env, c.srcs, c.dst, false, "")
		if ee, ok := err.(*exitError); !ok || ee.code != ExitUsage || !strings.Contains(ee.Error(), c.want) {
			t.Errorf("cp %v %s: %v, want a usage error with %q", c.srcs, c.dst, err, c.want)
		}
	}
}
