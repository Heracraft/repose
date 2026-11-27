package cli

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
	"github.com/heracraft/repose/internal/machost"
)

// ENG-102, DECISIONS I-1614: --json is the flag an agent adds to parse
// the answer, so `repose host add --json` never prints the join token or
// the argument, whose bundle holds a WireGuard private key. Elsewhere
// than a Mac the argument goes into a 0600 file the output names; on a
// Mac the install runs here and the host follows.
func TestHostAddJSONPrintsNoSecret(t *testing.T) {
	t.Cleanup(func() { onLinuxX86 = func() bool { return runtime.GOOS == "linux" && runtime.GOARCH == "amd64" } })
	// Not the Mac path (TestHostAddJSONOnAMacInstallsAndPrintsNoSecret is),
	// on a Mac too: run on one, this test installed into the Mac it ran on.
	onAppleSilicon = func() bool { return false }
	for _, x86 := range []bool{false, true} {
		fake := fakeapi.New(fakeapi.Options{})
		e, _ := newRoundtripEnv(t, fake)
		out, errOut := &discardWriter{}, &discardWriter{}
		e.Out, e.ErrOut = out, errOut
		e.JSON = true
		onLinuxX86 = func() bool { return x86 }
		fake.SetHostInstall("repose host add --join %s")
		name := "box"
		if x86 {
			name = "box-x86"
		}
		if err := HostAddCmd(context.Background(), e, name); err != nil {
			t.Fatal(err)
		}
		fake.Close()
		var h struct {
			Name      string         `json:"name"`
			JoinToken *string        `json:"join_token"`
			Install   map[string]any `json:"install"`
		}
		if err := json.Unmarshal([]byte(out.buf.String()), &h); err != nil {
			t.Fatalf("--json: %v: %s", err, out.buf.String())
		}
		if h.JoinToken != nil {
			t.Fatalf("x86=%v: join_token printed: %s", x86, out.buf.String())
		}
		if _, ok := h.Install["argument"]; ok {
			t.Fatalf("x86=%v: install.argument printed: %s", x86, out.buf.String())
		}
		path, _ := h.Install["join_file"].(string)
		if path == "" {
			t.Fatalf("x86=%v: no join_file: %s", x86, out.buf.String())
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("join file mode %v", fi.Mode().Perm())
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		argument := strings.TrimSpace(string(b))
		if bundle, err := decodeJoinBundle(argument); err != nil || bundle.Token == "" {
			t.Fatalf("the file holds no join bundle: %v", err)
		}
		for _, s := range []string{out.buf.String(), errOut.buf.String()} {
			if strings.Contains(s, argument[:40]) {
				t.Fatalf("x86=%v: the argument reached the output: %s", x86, s)
			}
		}
		cmd, _ := h.Install["command"].(string)
		if x86 {
			if cmd != hostInstallX86FromFile(path) {
				t.Fatalf("x86 command: %q", cmd)
			}
		} else if cmd != "" {
			t.Fatalf("a command for another machine, with a file only this one has: %q", cmd)
		}
	}
}

func TestHostAddJSONOnAMacInstallsAndPrintsNoSecret(t *testing.T) {
	e, _, m := newD2Mac(t)
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e.Client = newLifecycleEnv(t, fake).Client
	out := &discardWriter{}
	e.Out = out
	e.JSON = true
	if err := HostAddCmd(context.Background(), e, "mac-mini"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.mapped(machost.HostdPlist())); err != nil {
		t.Fatalf("--json on a Mac did not install: %v", err)
	}
	var h map[string]any
	if err := json.Unmarshal([]byte(out.buf.String()), &h); err != nil {
		t.Fatalf("--json: %v: %s", err, out.buf.String())
	}
	if h["name"] != "mac-mini" {
		t.Fatalf("host: %s", out.buf.String())
	}
	if _, ok := h["join_token"]; ok {
		t.Fatalf("join_token printed: %s", out.buf.String())
	}
	if _, ok := h["install"]; ok || strings.Contains(out.buf.String(), "private_key") {
		t.Fatalf("install printed: %s", out.buf.String())
	}
}
