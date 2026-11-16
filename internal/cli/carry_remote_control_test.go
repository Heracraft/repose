package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func boolp(b bool) *bool { return &b }

// I-1136: the switch is off unless config.toml says otherwise, and a
// project's own value beats the global one in both directions.
func TestRemoteControlFor(t *testing.T) {
	for name, tc := range map[string]struct {
		global  *bool
		project map[string]ProjectConfig
		want    bool
	}{
		"unset is off":            {nil, nil, false},
		"global on":               {boolp(true), nil, true},
		"global off":              {boolp(false), nil, false},
		"project on, global off":  {boolp(false), map[string]ProjectConfig{"todo-app": {RemoteControl: boolp(true)}}, true},
		"project off, global on":  {boolp(true), map[string]ProjectConfig{"todo-app": {RemoteControl: boolp(false)}}, false},
		"another project's value": {nil, map[string]ProjectConfig{"other": {RemoteControl: boolp(true)}}, false},
		"a table with no key":     {boolp(true), map[string]ProjectConfig{"todo-app": {}}, true},
	} {
		c := Config{RemoteControl: tc.global, Projects: tc.project}
		if got := c.remoteControlFor("todo-app"); got != tc.want {
			t.Errorf("%s: remoteControlFor = %v, want %v", name, got, tc.want)
		}
	}
}

// The key loads from a real file, global and per project.
func TestLoadConfigRemoteControl(t *testing.T) {
	dir := t.TempDir()
	body := "remote_control = true\n\n[projects.todo-app]\nremote_control = false\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.remoteControlFor("other") || cfg.remoteControlFor("todo-app") {
		t.Fatalf("remote_control: other=%v todo-app=%v", cfg.remoteControlFor("other"), cfg.remoteControlFor("todo-app"))
	}
}

// I-1136: the switch reaches the guest as one word, and the part runs
// repose-remote-control. A machine that never heard of it and a switch
// that is off is sent nothing at all (I-1134).
func TestRemoteControlCarry(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	home := t.TempDir()

	_, o, err := syncCredentialsAndCarry(ctx, f.target, home, f.local, credSyncOptions{}, carryOptions{Markers: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range o.Sent {
		if s == "remote-control" {
			t.Fatal("an off switch was sent to a machine with no marker")
		}
	}
	if err := runSSHOK(ctx, f.target, "test -e ~/.repose/remote-control"); err == nil {
		t.Fatal("~/.repose/remote-control was written for an off switch")
	}

	markers := map[string]string{}
	_, o, err = syncCredentialsAndCarry(ctx, f.target, home, f.local, credSyncOptions{}, carryOptions{RemoteControl: true, Markers: markers})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Failed) != 0 {
		t.Fatalf("outcome = %+v", o)
	}
	got, err := runSSH(ctx, f.target, "cat ~/.repose/remote-control", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "on" {
		t.Fatalf("~/.repose/remote-control = %q, want on", got)
	}

	// Off again, now that the machine holds a marker.
	out, err := runSSH(ctx, f.target, markerScript(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parseMarkers(string(out))["remote-control"]; !ok {
		t.Fatal("no remote-control marker after the switch went on")
	}
	if _, _, err := syncCredentialsAndCarry(ctx, f.target, home, f.local, credSyncOptions{}, carryOptions{Markers: parseMarkers(string(out))}); err != nil {
		t.Fatal(err)
	}
	got, err = runSSH(ctx, f.target, "cat ~/.repose/remote-control", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "off" {
		t.Fatalf("~/.repose/remote-control = %q, want off", got)
	}
}
