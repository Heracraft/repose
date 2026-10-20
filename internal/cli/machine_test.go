package cli

import (
	"context"
	"fmt"
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

// fakeResolve replaces the laptop's `ssh -G` with one answer per
// address: what the user's own ~/.ssh/config would have made of it. An
// address with no answer is one ssh could not resolve.
func fakeResolve(t *testing.T, answers map[string]machine.Resolved) {
	t.Helper()
	old := resolveTarget
	resolveTarget = func(_ context.Context, target machine.Target) (machine.Resolved, error) {
		if r, ok := answers[target.Address]; ok {
			return r, nil
		}
		return machine.Resolved{}, fmt.Errorf("%s does not resolve on this computer", target.Address)
	}
	t.Cleanup(func() { resolveTarget = old })
}

// resolvesAsSpelled is the laptop with no ~/.ssh/config to speak of:
// every address is the address, and the login is the one that was
// spelled.
func resolvesAsSpelled(t *testing.T) {
	t.Helper()
	old := resolveTarget
	resolveTarget = func(_ context.Context, target machine.Target) (machine.Resolved, error) {
		return machine.Resolved{Address: target.Address, Port: target.Port, Login: target.Login}, nil
	}
	t.Cleanup(func() { resolveTarget = old })
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
	resolvesAsSpelled(t)
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
			resolvesAsSpelled(t)
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
	resolvesAsSpelled(t)
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
	resolvesAsSpelled(t)
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
	e, reg, out, errOut := spineEnv(t)
	resolvesAsSpelled(t)
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
	// A box at the address it was added at has not moved, and nothing
	// says it did.
	if strings.Contains(errOut.buf.String(), "moved") {
		t.Errorf("stderr says it moved:\n%q", errOut.buf.String())
	}
}

// A machine's address changes: a box on DHCP, a laptop on another
// network, a `Host` entry edited. The entry follows it, and the projects
// recorded on the machine stay recorded, which rm-then-add would have
// taken with it.
func TestMachineAddFollowsAMachineThatMoved(t *testing.T) {
	e, reg, out, errOut := spineEnv(t)
	resolvesAsSpelled(t)
	fakeInstaller(t, nil, `{"ok":true}`, nil)
	opts := machineAddOptions{Name: "homebox", SSH: "me@192.168.1.40", UndoGB: 40, Now: clockOf(0)}
	if err := MachineAddCmd(context.Background(), e, opts); err != nil {
		t.Fatal(err)
	}
	recordPlace(t, e, "git@github.com:me/todo-app.git", "", machine.Project{Machine: "homebox", Slug: "todo-app"})
	was, err := reg.Get("homebox")
	if err != nil {
		t.Fatal(err)
	}
	out.buf.Reset()
	errOut.buf.Reset()

	moved := machineAddOptions{Name: "homebox", SSH: "me@10.0.0.9:2299", UndoGB: 40, Now: clockOf(0)}
	if err := MachineAddCmd(context.Background(), e, moved); err != nil {
		t.Fatalf("a machine that moved: %v", err)
	}
	if !strings.HasPrefix(errOut.buf.String(), "homebox moved from me@192.168.1.40 to me@10.0.0.9:2299.\n") {
		t.Errorf("stderr:\n%q", errOut.buf.String())
	}
	if out.buf.String() != "Added homebox.\n" {
		t.Errorf("stdout %q", out.buf.String())
	}
	all, err := reg.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("registry %+v (%v)", all, err)
	}
	if all[0].Address != "10.0.0.9" || all[0].Port != 2299 {
		t.Errorf("entry %+v", all[0])
	}
	if !all[0].AddedAt.Equal(was.AddedAt) {
		t.Errorf("AddedAt = %v, want the day it was added, %v", all[0].AddedAt, was.AddedAt)
	}
	projects, err := machine.LoadProjects(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := projects.Resolve("git@github.com:me/todo-app.git", ""); !ok {
		t.Error("the project on the machine that moved was forgotten")
	}
	// And the block ssh reads is the new address.
	sd, err := sshDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(filepath.Join(sd, machine.SSHName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "HostName 10.0.0.9") || strings.Contains(string(cfg), "192.168.1.40") {
		t.Errorf("machines config:\n%s", cfg)
	}
}

// A `Host` entry of the user's own, a Tailscale name and a jump host all
// reach a box. ssh resolves which when the machine is added, since the
// blocks repose writes are read with -F and the user's config is not.
func TestMachineAddResolvesTheAddressOnce(t *testing.T) {
	e, reg, out, errOut := spineEnv(t)
	fakeResolve(t, map[string]machine.Resolved{
		"shed": {
			Address: "10.0.0.9", Port: 2022, Login: "ada",
			KeyPath: "~/.ssh/work", ProxyJump: "jump@bast.example:2222",
		},
	})
	fakeInstaller(t, nil, `{"ok":true}`, nil)
	opts := machineAddOptions{Name: "shed", SSH: "ada@shed", UndoGB: 40, Now: clockOf(0)}
	if err := MachineAddCmd(context.Background(), e, opts); err != nil {
		t.Fatalf("machine add: %v (stderr %s)", err, errOut.buf.String())
	}
	if out.buf.String() != "Added shed.\n" {
		t.Errorf("stdout %q", out.buf.String())
	}
	m, err := reg.Get("shed")
	if err != nil {
		t.Fatal(err)
	}
	if m.Address != "10.0.0.9" || m.Port != 2022 || m.Login != "ada" ||
		m.KeyPath != "~/.ssh/work" || m.ProxyJump != "jump@bast.example:2222" {
		t.Errorf("entry %+v", m)
	}
	// The alias is kept for the user to see, since that is the name they
	// know the box by.
	if m.Spelled != "ada@shed" || m.Where() != "ada@shed" {
		t.Errorf("Spelled %q, Where %q", m.Spelled, m.Where())
	}
	// And the block carries what it resolved to, hops included: ssh reads
	// this file and no other.
	sd, err := sshDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(filepath.Join(sd, machine.SSHName))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"HostName 10.0.0.9", "Port 2022", "User ada", "ProxyJump jump@bast.example:2222"} {
		if !strings.Contains(string(cfg), line) {
			t.Errorf("machines config has no %q:\n%s", line, cfg)
		}
	}
}

// An address ssh makes nothing of is refused when the machine is added,
// naming it, rather than halfway through an install.
func TestMachineAddRefusesAnAddressThatDoesNotResolve(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	fakeResolve(t, nil)
	fakeInstaller(t, nil, `{"ok":true}`, nil)
	opts := machineAddOptions{Name: "shed", SSH: "me@typo.example", UndoGB: 40, Now: clockOf(0)}
	err := MachineAddCmd(context.Background(), e, opts)
	if exitCodeOf(err) != ExitUsage {
		t.Fatalf("exit %d, want %d (%v)", exitCodeOf(err), ExitUsage, err)
	}
	if !strings.Contains(err.Error(), "typo.example") {
		t.Errorf("%q does not name the address", err.Error())
	}
	if all, _ := reg.List(); len(all) != 0 {
		t.Errorf("registry %+v", all)
	}
}

func TestMachineAddRefusesAnInstallerThatDidNotFinish(t *testing.T) {
	e, reg, _, _ := spineEnv(t)
	resolvesAsSpelled(t)
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

// A store past the mark its machine keeps it under says so: the oldest
// points are what makes the room (ops/install/machine-install.sh runs
// `repose-agent gc --high-water 85`).
func TestMachineLsSaysWhenTheOldestPointsAreGoing(t *testing.T) {
	e, reg, out, _ := spineEnv(t)
	addMachine(t, reg, testMachine())
	fakeAgent(t, map[string]string{"space": `{"total":42949672960,"used":41231686041,"free":1717986919}`})
	if err := MachineLsCmd(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	want := "NAME     ADDRESS          UNDO\n" +
		"homebox  me@192.168.1.40  38.4 GB used of 40 GB, the oldest points are going\n"
	if out.buf.String() != want {
		t.Errorf("table:\n%q\nwant:\n%q", out.buf.String(), want)
	}
}

// The alias the user spelled is the address they are shown: it is the
// name they know the box by, and what their own config resolves it to is
// in the ssh config, not the table.
func TestMachineLsShowsTheAddressTheUserGave(t *testing.T) {
	e, reg, out, _ := spineEnv(t)
	addMachine(t, reg, machine.Machine{
		Name: "shed", Address: "10.0.0.9", Port: 2022, Login: "ada",
		Spelled: "shed", UndoPath: defaultUndoPath,
	})
	fakeAgent(t, map[string]string{"space": `{"total":4294967296,"used":360448,"free":4294606848}`})
	if err := MachineLsCmd(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	want := "NAME  ADDRESS  UNDO\n" +
		"shed  shed     352 KB used of 4 GB\n"
	if out.buf.String() != want {
		t.Errorf("table:\n%q\nwant:\n%q", out.buf.String(), want)
	}
}

// One word for every silence ("unreachable") covers a box that is off, a
// box that refused the laptop's key and a box whose host key changed,
// which are three different things to do about it.
func TestMachineLsSaysWhyAMachineIsSilent(t *testing.T) {
	for _, c := range []struct {
		name   string
		code   int
		stderr string
		want   string
	}{
		{"off", 255, "ssh: connect to host shed port 22: Connection refused", "no answer"},
		{"asleep", 255, "ssh: connect to host shed port 22: Connection timed out", "no answer"},
		{"a name that resolves to nothing", 255, "ssh: Could not resolve hostname shed: Name or service not known", "address does not resolve"},
		{"a key the box will not take", 255, "me@shed: Permission denied (publickey).", "refused the key"},
		{"a host key that changed", 255, "@@@@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @@@@\nHost key verification failed.", "host key changed"},
		{"a config ssh cannot read", 255, "Can't open user config file /home/me/.ssh/repose/machines: No such file or directory", "no ssh config"},
		{"a box with no repose on it", 127, "bash: line 1: /opt/repose/bin/repose-agent: No such file or directory", "no repose on the box"},
		{"something ssh did not explain", 255, "", "unreachable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, reg, out, _ := spineEnv(t)
			addMachine(t, reg, testMachine())
			fakeAgentFails(t, c.code, c.stderr)
			if err := MachineLsCmd(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.buf.String(), "  "+c.want+"\n") {
				t.Errorf("table:\n%q\nwant the UNDO cell %q", out.buf.String(), c.want)
			}
		})
	}
}

// The reading `machine ls` takes gives up on a box that is off, and asks
// for no password: there is nobody at the table to type one.
func TestProbeTargetGivesUp(t *testing.T) {
	got := probeTarget(sshTarget{Args: []string{"-F", "/tmp/machines", "homebox.repose"}, Owned: true})
	want := []string{"-F", "/tmp/machines", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "homebox.repose"}
	if strings.Join(got.Args, " ") != strings.Join(want, " ") {
		t.Errorf("probe args %v, want %v", got.Args, want)
	}
	if !got.Owned {
		t.Error("the probe is no longer a machine the user owns")
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

// A failure on a machine the user owns says what it was. There is no
// gateway and no certificate on this path, so the words the hosted path
// uses would name neither the box's refusal nor the way out of it.
func TestSSHErrorOnAMachineTheUserOwns(t *testing.T) {
	owned := sshTarget{Args: []string{"-F", "/home/me/.ssh/repose/machines", "homebox.repose"}, Owned: true}
	for _, c := range []struct {
		name   string
		code   int
		stderr string
		want   string
	}{
		{
			name: "the box refused the key", code: 255,
			stderr: "me@192.168.1.40: Permission denied (publickey).",
			want:   "the machine refused the key (me@192.168.1.40: Permission denied (publickey).)",
		},
		{
			name: "the box did not answer", code: 255,
			stderr: "ssh: connect to host 192.168.1.40 port 22: Connection refused",
			want:   "the machine did not answer (ssh: connect to host 192.168.1.40 port 22: Connection refused)",
		},
		{
			name: "the address resolves to nothing", code: 255,
			stderr: "ssh: Could not resolve hostname homebox: Name or service not known",
			want:   "the machine's address does not resolve on this computer (ssh: Could not resolve hostname homebox: Name or service not known)",
		},
		{
			name: "the command on the box failed", code: 1,
			stderr: "no such file",
			want:   "the command on the machine exited with status 1 (no such file)",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := &sshError{ExitCode: c.code, Stderr: c.stderr, Target: owned}
			if got := err.Error(); got != c.want {
				t.Errorf("Error() =\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

// The host key is the one failure the user cannot get past without being
// told which file remembers the old key, so the refusal names the file
// and the line that forgets it.
func TestSSHErrorNamesTheHostKeyFile(t *testing.T) {
	owned := sshTarget{Args: []string{"-F", "/home/me/.ssh/repose/machines", "homebox.repose"}, Owned: true}
	err := &sshError{
		ExitCode: 255,
		Stderr: "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\n" +
			"@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @\n" +
			"Host key verification failed.",
		Target: owned,
	}
	got := err.Error()
	for _, want := range []string{
		"the machine's host key is not the one it had when it was added",
		"ssh-keygen -R homebox.repose -f ",
		"/.ssh/repose/" + machine.KnownHostsName,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not say %q", got, want)
		}
	}
}

// The hosted path keeps its own words: there a refusal is the gateway's,
// and the certificate is what it refused.
func TestSSHErrorOnTheHostedPathIsUnchanged(t *testing.T) {
	err := &sshError{ExitCode: 255, Stderr: "todo-app.heracraft@ssh.repose.herakraft.co: Permission denied (publickey)."}
	if got := err.Error(); !strings.HasPrefix(got, "the gateway refused the SSH certificate") {
		t.Errorf("Error() = %q", got)
	}
}

func TestClassifySSH(t *testing.T) {
	for _, c := range []struct {
		stderr string
		want   sshFailure
	}{
		{"@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @", sshFailHostKey},
		{"Host key verification failed.", sshFailHostKey},
		{"Can't open user config file /home/me/.ssh/repose/machines: No such file or directory", sshFailNoConfig},
		{"/home/me/.ssh/config: line 3: Bad configuration option: nonsense", sshFailNoConfig},
		{"ssh: Could not resolve hostname shed: Name or service not known", sshFailNoName},
		{"me@shed: Permission denied (publickey).", sshFailKeyRefused},
		{"Received disconnect from 10.0.0.9 port 22:2: Too many authentication failures", sshFailKeyRefused},
		{"ssh: connect to host shed port 22: Connection refused", sshFailNoAnswer},
		{"ssh: connect to host shed port 22: No route to host", sshFailNoAnswer},
		{"", sshFailOther},
	} {
		if got := classifySSH(c.stderr); got != c.want {
			t.Errorf("classifySSH(%q) = %d, want %d", c.stderr, got, c.want)
		}
	}
}
