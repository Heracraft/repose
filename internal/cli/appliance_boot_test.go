package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// ENG-187 (the council's M6, 2026-11-22): `sudo shutdown -r now` on the
// AWS M4 with no GUI login, and the host went offline and stayed so.
// The launcher was a per-user launchd agent, which launchd loads only
// in a login session; a Mac mini after a power cut sits at the login
// window the same way. The appliance's job belongs in the system domain
// (DECISIONS I-1564): a LaunchDaemon that runs as the user.

// bootMac is fakeMac with sudo that works: `install` copies the file the
// way the real one would, and every call is recorded, as is every
// launchctl call through macExec.
type bootMac struct {
	sudo, exec []string
	refuse     bool
	loaded     bool // launchctl print system/run.repose.appliance answers
}

func newBootMac(t *testing.T) (*Env, *bootMac, appliancePaths, func() string) {
	t.Helper()
	e, errOut, p := fakeMac(t, 200)
	m := &bootMac{}
	inner := macExec
	macExec = func(name string, args ...string) ([]byte, error) {
		line := strings.Join(append([]string{name}, args...), " ")
		switch {
		case name == "id":
			return []byte("dev\n"), nil
		case name == "fdesetup":
			return []byte("FileVault is Off.\n"), nil
		case line == "launchctl print system/"+applianceLabel:
			if !m.loaded {
				return nil, errors.New("exit status 113")
			}
			return nil, nil
		case name == "launchctl":
			m.exec = append(m.exec, line)
		}
		return inner(name, args...)
	}
	macSudo = func(args ...string) error {
		m.sudo = append(m.sudo, strings.Join(args, " "))
		if m.refuse {
			return errors.New("exit status 1")
		}
		switch args[0] {
		case "install":
			b, err := os.ReadFile(args[len(args)-2])
			if err != nil {
				return err
			}
			return os.WriteFile(args[len(args)-1], b, 0o644)
		case "rm":
			return os.Remove(args[len(args)-1])
		case "launchctl":
			if args[1] == "bootstrap" {
				m.loaded = true
			}
		}
		return nil
	}
	return e, m, p, errOut.String
}

func TestApplianceStartsWhenTheMacBoots(t *testing.T) {
	e, m, p, errOut := newBootMac(t)
	if err := applianceInstall(context.Background(), e, encodeTokenBundle("rjt_test"), "mac-mini", applianceOptions{DiskGB: 60}); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(launchDaemonsDir, "run.repose.appliance.plist"))
	if err != nil {
		t.Fatalf("no LaunchDaemon after host add: %v (sudo calls %q, launchctl %q)", err, m.sudo, m.exec)
	}
	plist := string(b)
	for _, want := range []string{
		"<key>UserName</key><string>dev</string>",
		"<key>HOME</key><string>" + e.HomeDir + "</string>",
		"<string>host</string><string>run</string>",
		"<key>RunAtLoad</key><true/>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("the boot job lacks %s", want)
		}
	}
	if !contains(m.sudo, "launchctl bootstrap system "+bootPlistPath()) {
		t.Errorf("the boot job was not given to launchd's system domain: %q", m.sudo)
	}
	if _, err := os.Stat(p.Plist); err == nil {
		t.Error("a login agent was left beside the boot job: two launchers on one disk")
	}
	if strings.Contains(errOut(), "starts at your login") {
		t.Errorf("said the login fallback on a Mac where sudo worked: %s", errOut())
	}
}

func TestApplianceWithoutSudoSaysItStartsAtLogin(t *testing.T) {
	e, m, p, errOut := newBootMac(t)
	m.refuse = true
	if err := applianceInstall(context.Background(), e, encodeTokenBundle("rjt_test"), "mac-mini", applianceOptions{DiskGB: 60}); err != nil {
		t.Fatalf("install without sudo: %v", err)
	}
	if bootJobInstalled() {
		t.Fatal("a boot job without sudo")
	}
	if _, err := os.Stat(p.Plist); err != nil {
		t.Fatalf("no login agent when sudo refused: %v", err)
	}
	want := "The appliance starts at your login, not when this Mac boots"
	if !strings.Contains(errOut(), want) || !strings.Contains(errOut(), "`repose host boot`") {
		t.Fatalf("stderr does not say what a headless Mac needs:\n%s", errOut())
	}
}

func TestApplianceBootMovesTheLoginAgent(t *testing.T) {
	e, m, p, _ := newBootMac(t)
	m.refuse = true
	if err := applianceInstall(context.Background(), e, encodeTokenBundle("rjt_test"), "mac-mini", applianceOptions{DiskGB: 60}); err != nil {
		t.Fatal(err)
	}
	m.refuse = false
	m.exec = nil
	if err := ApplianceBootCmd(e); err != nil {
		t.Fatalf("host boot: %v", err)
	}
	if !bootJobInstalled() {
		t.Fatal("host boot installed no boot job")
	}
	if _, err := os.Stat(p.Plist); err == nil {
		t.Fatal("host boot left the login agent")
	}
	booted := false
	for _, l := range m.exec {
		booted = booted || strings.HasPrefix(l, "launchctl bootout ") && strings.HasSuffix(l, "/"+applianceLabel)
	}
	if !booted {
		t.Fatalf("the login agent was not booted out before the boot job started: %q", m.exec)
	}
}

func TestApplianceRemoveTakesTheBootJob(t *testing.T) {
	e, m, p, _ := newBootMac(t)
	if err := applianceInstall(context.Background(), e, encodeTokenBundle("rjt_test"), "mac-mini", applianceOptions{DiskGB: 60}); err != nil {
		t.Fatal(err)
	}
	// sudo refused at removal: nothing goes, because a boot job left
	// over a deleted disk would restart a VM on nothing at every boot.
	m.refuse = true
	if err := applianceRemove(e); err == nil {
		t.Fatal("removed with sudo refused")
	}
	if _, err := os.Stat(p.Disk); err != nil || !bootJobInstalled() {
		t.Fatalf("a refused removal took something: disk %v, job %v", err, bootJobInstalled())
	}
	m.refuse = false
	if err := applianceRemove(e); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !contains(m.sudo, "launchctl bootout system/"+applianceLabel) {
		t.Errorf("the boot job was not booted out: %q", m.sudo)
	}
	if bootJobInstalled() {
		t.Error("the boot job's plist outlived host rm")
	}
	if _, err := os.Stat(p.Dir); err == nil {
		t.Error("the appliance's directory outlived host rm")
	}
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// On the Mac, `repose host ls` says when the appliance there would stay
// off after a power cut, and says nothing once it starts at boot.
func TestHostLsSaysALoginOnlyAppliance(t *testing.T) {
	e, m, _, _ := newBootMac(t)
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e.Client = newLifecycleEnv(t, fake).Client
	m.refuse = true
	if err := HostAddCmd(context.Background(), e, "mac-mini", applianceOptions{DiskGB: 60}); err != nil {
		t.Fatal(err)
	}
	errOut := &discardWriter{}
	e.Out, e.ErrOut = &discardWriter{}, errOut
	if err := HostLsCmd(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.buf.String(), "starts at your login, not when the Mac boots") {
		t.Fatalf("host ls on a Mac with a login agent: %q", errOut.buf.String())
	}
	m.refuse = false
	if err := ApplianceBootCmd(e); err != nil {
		t.Fatal(err)
	}
	errOut.buf.Reset()
	if err := HostLsCmd(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if errOut.buf.Len() != 0 {
		t.Fatalf("host ls with the boot job still warned: %q", errOut.buf.String())
	}
}
