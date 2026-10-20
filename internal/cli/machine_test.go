package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/machine"
	"github.com/heracraft/repose/ops/install"
)

// clockOf is a clock that returns base plus each offset in turn, and the
// last one from then on, so the ✓ lines' times are the same every run.
func clockOf(offsets ...time.Duration) func() time.Time {
	base := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	i := -1
	return func() time.Time {
		if i < len(offsets)-1 {
			i++
		}
		return base.Add(offsets[i])
	}
}

// installerCall is what the fake ssh was asked to run.
type installerCall struct {
	Remote string
	Stdin  string
	Target []string
}

// fakeInstaller replaces the ssh that pipes the installer to the box: it
// records what it was sent and prints lines on the installer's behalf.
func fakeInstaller(t *testing.T, lines []string, stdout string, fail error) *installerCall {
	t.Helper()
	got := &installerCall{}
	old := streamOnMachine
	streamOnMachine = func(_ context.Context, target sshTarget, remoteCmd string, stdin io.Reader, onLine func(string)) ([]byte, error) {
		got.Remote, got.Target = remoteCmd, target.Args
		if stdin != nil {
			b, err := io.ReadAll(stdin)
			if err != nil {
				t.Errorf("read the installer: %v", err)
			}
			got.Stdin = string(b)
		}
		for _, l := range lines {
			onLine(l)
		}
		if fail != nil {
			return nil, fail
		}
		return []byte(stdout), nil
	}
	t.Cleanup(func() { streamOnMachine = old })
	return got
}

// installerSteps is what machine-install.sh prints on stderr, one line
// per step, with the line it fails with and ssh's own chatter mixed in.
var installerSteps = []string{
	"Warning: Permanently added '192.168.1.40' (ED25519) to the list of known hosts.",
	"Checked the box (Ubuntu 24.04, x86_64, systemd, btrfs)",
	"Provisioned 40 GB of undo space (btrfs)",
	"",
	"Installed the agent and 5 coding agents",
}

func TestMachineAddInstallsAndRecordsTheMachine(t *testing.T) {
	e, reg, out, errOut := spineEnv(t)
	sent := fakeInstaller(t, installerSteps, `{"ok":true,"store":"/var/lib/repose/undo","fs":"btrfs"}`, nil)
	opts := machineAddOptions{
		Name:   "homebox",
		SSH:    "me@192.168.1.40",
		UndoGB: 40,
		Now:    clockOf(0, 0, 1200*time.Millisecond, 1600*time.Millisecond, 49600*time.Millisecond),
	}
	if err := MachineAddCmd(context.Background(), e, opts); err != nil {
		t.Fatalf("machine add: %v (stderr %s)", err, errOut.buf.String())
	}
	wantErr := "Installing on me@192.168.1.40 ...\n" +
		"✓ Checked the box (Ubuntu 24.04, x86_64, systemd, btrfs)  1.2s\n" +
		"✓ Provisioned 40 GB of undo space (btrfs)  0.4s\n" +
		"✓ Installed the agent and 5 coding agents  48s\n"
	if errOut.buf.String() != wantErr {
		t.Errorf("stderr:\n%q\nwant:\n%q", errOut.buf.String(), wantErr)
	}
	if out.buf.String() != "Added homebox.\n" {
		t.Errorf("stdout %q", out.buf.String())
	}
	// The installer is piped in, so nothing has to be on the box first.
	if install.Script == "" {
		t.Error("ops/install/machine-install.sh is empty; the embed carries no installer")
	}
	if sent.Stdin != install.Script {
		t.Errorf("stdin is %d bytes, want the installer's %d", len(sent.Stdin), len(install.Script))
	}
	if want := "bash -s -- --name 'homebox' --undo-gb '40'"; sent.Remote != want {
		t.Errorf("remote command %q, want %q", sent.Remote, want)
	}
	all, err := reg.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("registry %+v (%v)", all, err)
	}
	m := all[0]
	if m.Name != "homebox" || m.Address != "192.168.1.40" || m.Login != "me" || m.Port != 0 || m.UndoPath != defaultUndoPath {
		t.Errorf("entry %+v", m)
	}
	// The Host block is on disk before the installer runs, since that is
	// the ssh it runs over.
	sd, err := sshDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(filepath.Join(sd, machine.SSHName))
	if err != nil {
		t.Fatalf("read the machines config: %v", err)
	}
	if !strings.Contains(string(cfg), "Host homebox.repose") {
		t.Errorf("machines config:\n%s", cfg)
	}
	want := []string{"-F", filepath.Join(sd, machine.SSHName), "homebox.repose"}
	if strings.Join(sent.Target, " ") != strings.Join(want, " ") {
		t.Errorf("ssh args %v, want %v", sent.Target, want)
	}
}

func TestMachineAddRefusesWhatIsNoMachine(t *testing.T) {
	for _, c := range []struct {
		name string
		opts machineAddOptions
		says string
	}{
		{"no --ssh", machineAddOptions{Name: "homebox", UndoGB: 40}, "--ssh"},
		{"a name with a space", machineAddOptions{Name: "home box", SSH: "me@h", UndoGB: 40}, "dashes"},
		{"an upper-case name", machineAddOptions{Name: "HomeBox", SSH: "me@h", UndoGB: 40}, "dashes"},
		{"no undo space", machineAddOptions{Name: "homebox", SSH: "me@h", UndoGB: 0}, "--undo-gb"},
		{"a port that is no port", machineAddOptions{Name: "homebox", SSH: "me@h:nope", UndoGB: 40}, "port"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, reg, _, _ := spineEnv(t)
			fakeInstaller(t, nil, `{"ok":true}`, nil)
			c.opts.Now = clockOf(0)
			err := MachineAddCmd(context.Background(), e, c.opts)
			if exitCodeOf(err) != ExitUsage {
				t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), ExitUsage, err)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("%q does not say %q", err.Error(), c.says)
			}
			if all, _ := reg.List(); len(all) != 0 {
				t.Errorf("registry %+v", all)
			}
		})
	}
}

// A machine may not take a name the gateway's blocks already claim: ssh
// reads the first block it finds for an alias.
func TestMachineAddRefusesAProjectsAlias(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	fakeInstaller(t, nil, `{"ok":true}`, nil)
	sd, err := sshDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg := renderSSHConfig([]Project{{ID: "p1", Slug: "todo-app"}}, "heracraft", "~", true)
	if err := os.WriteFile(filepath.Join(sd, sshHostsName), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := machineAddOptions{Name: "todo-app", SSH: "me@192.168.1.40", UndoGB: 40, Now: clockOf(0)}
	err = MachineAddCmd(context.Background(), e, opts)
	if exitCodeOf(err) != ExitUsage || !strings.Contains(err.Error(), "todo-app.repose") {
		t.Fatalf("exit %d: %v", exitCodeOf(err), err)
	}
	if all, _ := reg.List(); len(all) != 0 {
		t.Errorf("registry %+v", all)
	}
}

func TestMachineAddForgetsTheEntryWhenTheInstallFails(t *testing.T) {
	e, reg, out, _ := spineEnv(t)
	fakeInstaller(t, []string{"Checked the box (Ubuntu 24.04, x86_64, systemd, btrfs)", "repose: btrfs is not in this kernel"}, "",
		&sshError{ExitCode: 1, Stderr: "repose: btrfs is not in this kernel"})
	opts := machineAddOptions{Name: "homebox", SSH: "me@192.168.1.40", UndoGB: 40, Now: clockOf(0)}
	err := MachineAddCmd(context.Background(), e, opts)
	if exitCodeOf(err) != ExitGeneric {
		t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), ExitGeneric, err)
	}
	if !strings.HasPrefix(err.Error(), "Could not install repose on homebox:") {
		t.Errorf("failure reads %q", err.Error())
	}
	if all, _ := reg.List(); len(all) != 0 {
		t.Errorf("a machine that did not install is still in the registry: %+v", all)
	}
	if out.buf.String() != "" {
		t.Errorf("stdout %q", out.buf.String())
	}
}

func TestMachineAddTwiceInstallsOverIt(t *testing.T) {
	e, reg, out, _ := spineEnv(t)
	fakeInstaller(t, nil, `{"ok":true}`, nil)
	opts := machineAddOptions{Name: "homebox", SSH: "me@192.168.1.40", UndoGB: 40, Now: clockOf(0)}
	if err := MachineAddCmd(context.Background(), e, opts); err != nil {
		t.Fatal(err)
	}
	if err := MachineAddCmd(context.Background(), e, opts); err != nil {
		t.Fatalf("a second add: %v", err)
	}
	if all, _ := reg.List(); len(all) != 1 {
		t.Errorf("registry %+v", all)
	}
	if out.buf.String() != "Added homebox.\nAdded homebox.\n" {
		t.Errorf("stdout %q", out.buf.String())
	}
	// The same name at another address is a mistake, not an install.
	other := machineAddOptions{Name: "homebox", SSH: "me@10.0.0.9", UndoGB: 40, Now: clockOf(0)}
	err := MachineAddCmd(context.Background(), e, other)
	if exitCodeOf(err) != ExitGeneric || !strings.Contains(err.Error(), "already a machine of yours") {
		t.Fatalf("exit %d: %v", exitCodeOf(err), err)
	}
}

func TestMachineAddRefusesAnInstallerThatDidNotFinish(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	fakeInstaller(t, nil, "", nil)
	opts := machineAddOptions{Name: "homebox", SSH: "me@192.168.1.40", UndoGB: 40, Now: clockOf(0)}
	if err := MachineAddCmd(context.Background(), e, opts); exitCodeOf(err) != ExitGeneric {
		t.Fatalf("exit %d: %v", exitCodeOf(err), err)
	}
	if all, _ := reg.List(); len(all) != 0 {
		t.Errorf("registry %+v", all)
	}
}

func TestMachineLsTable(t *testing.T) {
	e, reg, out, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	fakeAgent(t, map[string]string{"space": `{"total":42949672960,"used":13314398618,"free":29635274342}`})
	if err := MachineLsCmd(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	want := "NAME     ADDRESS          UNDO\n" +
		"homebox  me@192.168.1.40  12.4 GB used of 40 GB\n"
	if out.buf.String() != want {
		t.Errorf("table:\n%q\nwant:\n%q", out.buf.String(), want)
	}
}

func TestMachineLsSaysWhichMachineDidNotAnswer(t *testing.T) {
	e, reg, out, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	addMachine(t, reg, machine.Machine{Name: "shed", Address: "shed", Login: "me", Port: 2222})
	fakeAgent(t, nil)
	if err := MachineLsCmd(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	want := "NAME     ADDRESS          UNDO\n" +
		"homebox  me@192.168.1.40  unreachable\n" +
		"shed     me@shed:2222     unreachable\n"
	if out.buf.String() != want {
		t.Errorf("table:\n%q\nwant:\n%q", out.buf.String(), want)
	}
}

func TestMachineLsWithNoMachines(t *testing.T) {
	e, _, out, _ := spineEnv(t)
	if err := MachineLsCmd(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if out.buf.String() != "You have added no machines.\n" {
		t.Errorf("stdout %q", out.buf.String())
	}
}

func TestMachineRmForgetsTheMachineAndItsProjects(t *testing.T) {
	e, reg, out, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	addMachine(t, reg, machine.Machine{Name: "shed", Address: "shed", Login: "me"})
	if err := writeMachineSSH(reg); err != nil {
		t.Fatal(err)
	}
	recordPlace(t, e, "git@github.com:me/todo-app.git", "", machine.Project{Machine: "homebox", Slug: "todo-app"})
	recordPlace(t, e, "git@github.com:me/blog.git", "", machine.Project{Machine: "shed", Slug: "blog"})
	if err := MachineRmCmd(context.Background(), e, "homebox"); err != nil {
		t.Fatal(err)
	}
	if out.buf.String() != "Removed homebox.\n" {
		t.Errorf("stdout %q", out.buf.String())
	}
	all, err := reg.List()
	if err != nil || len(all) != 1 || all[0].Name != "shed" {
		t.Fatalf("registry %+v (%v)", all, err)
	}
	projects, err := machine.LoadProjects(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := projects.Resolve("git@github.com:me/todo-app.git", ""); ok {
		t.Error("the project on the machine that went is still recorded")
	}
	if _, ok := projects.Resolve("git@github.com:me/blog.git", ""); !ok {
		t.Error("another machine's project was forgotten too")
	}
	// The remaining machine's Host block is rewritten without it.
	sd, err := sshDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(filepath.Join(sd, machine.SSHName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), "homebox.repose") || !strings.Contains(string(cfg), "shed.repose") {
		t.Errorf("machines config:\n%s", cfg)
	}
}

func TestMachineRmOfNoMachineOfYours(t *testing.T) {
	e, _, _, _ := spineEnv(t)
	err := MachineRmCmd(context.Background(), e, "homebox")
	if exitCodeOf(err) != ExitGuestNotRunning {
		t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), ExitGuestNotRunning, err)
	}
	if err.Error() != "No machine of yours is called homebox." {
		t.Errorf("refusal %q", err.Error())
	}
}

func TestInstallerStepSkipsWhatIsNoStep(t *testing.T) {
	for _, c := range []struct {
		line string
		want string
	}{
		{"Checked the box (Ubuntu 24.04, x86_64, systemd, btrfs)", "Checked the box (Ubuntu 24.04, x86_64, systemd, btrfs)"},
		{"  Installed the agent  ", "Installed the agent"},
		{"", ""},
		{"repose: there is no sudo on this box", ""},
		{"usage: machine-install.sh --name NAME", ""},
		{"Warning: Permanently added 'shed' (ED25519) to the list of known hosts.", ""},
	} {
		got, ok := installerStep(c.line)
		if c.want == "" {
			if ok {
				t.Errorf("%q is no step, got %q", c.line, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("installerStep(%q) = %q, %v", c.line, got, ok)
		}
	}
}

func TestInstallerResult(t *testing.T) {
	if err := installerResult([]byte("{\"ok\":true,\"store\":\"/var/lib/repose/undo\"}\n")); err != nil {
		t.Errorf("ok: %v", err)
	}
	for _, out := range []string{"", "{}", `{"ok":false}`, "installing..."} {
		if err := installerResult([]byte(out)); err == nil {
			t.Errorf("%q is not a finished install", out)
		}
	}
}
