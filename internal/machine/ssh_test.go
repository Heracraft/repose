package machine

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/machines and testdata/machines-home")

// goldenMachines covers what a block can carry: the default port left
// out, a port spelled out, a port that is not 22, an IPv6 address, a key
// path with a space in it, and a machine added without a key.
func goldenMachines() []Machine {
	return []Machine{
		{Name: "workshop", Address: "2001:db8::1", Port: 2222, Login: "ada", KeyPath: "/home/ada/.ssh/my key", UndoPath: "/var/lib/repose/undo"},
		{Name: "homebox", Address: "192.168.1.40", Login: "me", KeyPath: "~/.ssh/id_ed25519", UndoPath: "/var/lib/repose/undo"},
		{Name: "spare", Address: "spare.example", Port: 22, Login: "me"},
	}
}

func TestRenderSSHConfigGolden(t *testing.T) {
	cases := []struct {
		file   string
		prefix string
	}{
		{"testdata/machines", "~"},
		{"testdata/machines-home", "/home/ada"},
	}
	for _, c := range cases {
		want := RenderSSHConfig(goldenMachines(), c.prefix, true)
		if *updateGolden {
			if err := os.WriteFile(c.file, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s is stale; run this test with -update.\n--- on disk ---\n%s\n--- rendered ---\n%s", c.file, got, want)
		}
	}
}

// The multiplexing lines are the only difference the Windows build makes
// to a block; its OpenSSH has no ControlMaster.
func TestRenderSSHConfigWithoutMultiplexing(t *testing.T) {
	out := RenderSSHConfig(goldenMachines(), "~", false)
	for _, l := range []string{"ControlMaster", "ControlPath", "ControlPersist"} {
		if strings.Contains(out, l) {
			t.Errorf("%s is in a config rendered without multiplexing", l)
		}
	}
	if !strings.Contains(out, "Host homebox.repose\n  HostName 192.168.1.40\n  User me\n") {
		t.Error("the block itself changed")
	}
}

// A machine's blocks name no certificate and no gateway: that is the one
// thing that must stay true of this file, since the gateway's hosts file
// is the other half of ~/.ssh/repose and is written by internal/cli.
func TestRenderSSHConfigIsNotTheGateway(t *testing.T) {
	out := RenderSSHConfig(goldenMachines(), "~", true)
	for _, l := range []string{"CertificateFile", "repose.herakraft.co"} {
		if strings.Contains(out, l) {
			t.Errorf("%q is in a machine's block", l)
		}
	}
}

func TestIncludeLine(t *testing.T) {
	cases := []struct{ prefix, want string }{
		{"~", "Include ~/.ssh/repose/machines\n"},
		{"/home/ada", "Include /home/ada/.ssh/repose/machines\n"},
		{"/home/a b", "Include \"/home/a b/.ssh/repose/machines\"\n"},
	}
	for _, c := range cases {
		if got := IncludeLine(c.prefix); got != c.want {
			t.Errorf("IncludeLine(%q) = %q, want %q", c.prefix, got, c.want)
		}
	}
}

func TestSSHConfigAndWriteSSH(t *testing.T) {
	cfg, sd := t.TempDir(), t.TempDir()
	r := NewRegistry(cfg)
	for _, m := range goldenMachines() {
		if err := r.Add(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.WriteSSH(sd, "~"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sd, SSHName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.SSHConfig("~"); string(b) != got {
		t.Error("WriteSSH wrote something SSHConfig does not render")
	}
	if !strings.Contains(string(b), "Host homebox.repose\n") {
		t.Errorf("no block for homebox:\n%s", b)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}

	// A second write with nothing changed leaves the file alone.
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.WriteSSH(sd, "~"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("WriteSSH rewrote a file that had not changed")
	}

	// A registry it cannot read leaves the blocks on disk alone.
	if err := os.WriteFile(r.Path(), []byte("[machines.\"Home Box\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteSSH(sd, "~"); err == nil {
		t.Error("WriteSSH over an unreadable registry: want an error")
	}
	if now, err := os.ReadFile(path); err != nil || string(now) != string(b) {
		t.Error("WriteSSH emptied the file it could not render")
	}
}

// ssh itself resolves the alias to the machine's address, from the
// generated file and nothing else (SPINE.md checklist, "Contract 2").
func TestSSHResolvesTheAlias(t *testing.T) {
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("no ssh on this machine")
	}
	path := filepath.Join(t.TempDir(), "machines")
	if err := os.WriteFile(path, []byte(RenderSSHConfig(goldenMachines(), "~", true)), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ssh, "-G", "-F", path, "homebox.repose").Output()
	if err != nil {
		t.Fatalf("ssh -G: %v", err)
	}
	have := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), " "); ok {
			if _, seen := have[k]; !seen {
				have[k] = v
			}
		}
	}
	for k, want := range map[string]string{
		"hostname":      "192.168.1.40",
		"user":          "me",
		"port":          "22",
		"forwardagent":  "no",
		"controlmaster": "auto",
	} {
		if have[k] != want {
			t.Errorf("ssh -G %s = %q, want %q", k, have[k], want)
		}
	}
	// And the one with a port of its own keeps it.
	out, err = exec.Command(ssh, "-G", "-F", path, "workshop.repose").Output()
	if err != nil {
		t.Fatalf("ssh -G: %v", err)
	}
	if !strings.Contains(string(out), "\nport 2222\n") {
		t.Error("workshop.repose did not resolve to port 2222")
	}
}
