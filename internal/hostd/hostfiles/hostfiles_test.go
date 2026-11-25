package hostfiles

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// The Mac appliance's hostd.env as 2026.11.22.4 shipped it: no
// --owned-host (I-1626).
const env224 = `# hostd's flags on the Mac appliance (nix/hosts/mac.nix, DECISIONS I-1494).
HOSTD_API_ARGS=--api-addr 10.255.255.1:8443 --api-server-name api.repose.herakraft.co --api-ca /etc/repose/api-ca.pem
HOSTD_ARGS=--snapshot-dir /var/lib/repose/snapshots --ready-timeout 5m --park --free-page-reporting
`

// An x86 host's, as the installer wrote it before I-1626.
const envX86 = `# hostd's flags on an x86 host (ops/install-host-x86.sh, DECISIONS I-1498).
HOSTD_API_ARGS=--api-addr 10.255.255.1:8443 --api-server-name api.repose.herakraft.co --api-ca /etc/repose/api-ca.pem
# Snapshots on its own disk (DECISIONS I-1372); no park: an x86 host has
# no launcher and no Key Vault to wrap the image key with.
HOSTD_ARGS=--snapshot-dir /var/lib/repose/snapshots
`

type world struct {
	t    *testing.T
	root string
	r    *shell.Fake
	e    *Ensurer
	logs *bytes.Buffer
}

func newWorld(t *testing.T, files map[string]string) *world {
	t.Helper()
	w := &world{t: t, root: t.TempDir(), r: &shell.Fake{}, logs: &bytes.Buffer{}}
	for p, body := range files {
		full := filepath.Join(w.root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w.e = &Ensurer{Root: w.root, R: w.r, Log: slog.New(slog.NewJSONHandler(w.logs, nil))}
	return w
}

func (w *world) read(p string) string {
	w.t.Helper()
	b, err := os.ReadFile(filepath.Join(w.root, p))
	if err != nil {
		w.t.Fatalf("%s: %v", p, err)
	}
	return string(b)
}

// enabledAfter scripts `systemctl is-enabled` to say what the last
// `enable --now` made true.
func (w *world) enabledAfter() {
	w.r.Scripts = append(w.r.Scripts, shell.Script{Prefix: []string{"systemctl", "is-enabled"}, Handle: func(argv []string) (shell.Result, error) {
		for _, c := range w.r.Calls {
			if len(c) == 4 && c[1] == "enable" && c[3] == argv[2] {
				return shell.Result{Stdout: []byte("enabled\n")}, nil
			}
		}
		return shell.Result{Stdout: []byte("disabled\n"), ExitCode: 1}, &shell.ExitError{Argv: argv}
	}})
}

// A fleet host has neither the appliance's version file nor the
// installer's header, and nothing is read into, written or run there.
func TestFleetHostGetsNothing(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"nixos, no /etc/repose":       {},
		"a hostd.env of another hand": {"etc/repose/hostd.env": "HOSTD_ARGS=--snapshot-dir /x\n"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, files)
			res, err := w.e.Ensure(context.Background())
			if err != nil || res.Eligible || len(res.Written) != 0 || res.Restart {
				t.Fatalf("res %+v err %v", res, err)
			}
			if len(w.r.Calls) != 0 {
				t.Fatalf("ran %v", w.r.Calls)
			}
			if _, err := os.Stat(filepath.Join(w.root, "usr")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("wrote under /usr")
			}
			if b, err := os.ReadFile(filepath.Join(w.root, HostdEnv)); err == nil && strings.Contains(string(b), OwnedFlag) {
				t.Fatal("hostd.env changed")
			}
		})
	}
}

// A 2026.11.22.4 appliance and a pre-I-1626 x86 host get the files, the
// two units enabled and started, and --owned-host, once; a second start
// does nothing and asks for no restart.
func TestOldHostsGetTheFilesOnce(t *testing.T) {
	for name, setup := range map[string]map[string]string{
		"appliance 2026.11.22.4": {ApplianceVersion: "2026.11.22.4\n", HostdEnv: env224},
		"x86 installer":          {HostdEnv: envX86},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, setup)
			w.enabledAfter()
			before := setup[HostdEnv]
			res, err := w.e.Ensure(context.Background())
			if err != nil || !res.Eligible || !res.Restart {
				t.Fatalf("res %+v err %v", res, err)
			}
			for _, f := range files {
				if !slices.Contains(res.Written, f.dst) {
					t.Fatalf("%s not written: %v", f.dst, res.Written)
				}
				st, err := os.Stat(filepath.Join(w.root, f.dst))
				if err != nil || st.Mode().Perm() != f.mode {
					t.Fatalf("%s: %v %v", f.dst, st, err)
				}
			}
			if !strings.HasPrefix(w.read("usr/local/lib/repose/repose-snapshots-volume"), "#!/bin/bash") {
				t.Fatal("script body")
			}
			env := w.read(HostdEnv)
			want := strings.Replace(before, "HOSTD_ARGS=--snapshot-dir", "HOSTD_ARGS=--owned-host --snapshot-dir", 1)
			if env != want {
				t.Fatalf("hostd.env:\n%s\nwant:\n%s", env, want)
			}
			calls := w.r.CallsWithPrefix("systemctl")
			for _, u := range units {
				if !slices.ContainsFunc(calls, func(c []string) bool { return slices.Equal(c, []string{"systemctl", "enable", "--now", u}) }) {
					t.Fatalf("%s not enabled: %v", u, calls)
				}
			}
			if !slices.ContainsFunc(calls, func(c []string) bool { return slices.Equal(c, []string{"systemctl", "daemon-reload"}) }) {
				t.Fatalf("no daemon-reload: %v", calls)
			}
			// One event per file written.
			if n := strings.Count(w.logs.String(), `"event":"host_file"`); n != len(files)+len(units)+1 {
				t.Fatalf("%d host_file events:\n%s", n, w.logs.String())
			}
			left, _ := filepath.Glob(filepath.Join(w.root, "etc/repose/.*"))
			if len(left) != 0 {
				t.Fatalf("temporary files left: %v", left)
			}

			n := len(w.r.Calls) // systemd remembers what was enabled
			res, err = w.e.Ensure(context.Background())
			if err != nil || len(res.Written) != 0 || res.Restart {
				t.Fatalf("second start: %+v %v", res, err)
			}
			for _, c := range w.r.Calls[n:] {
				if len(c) > 1 && c[1] != "is-enabled" {
					t.Fatalf("second start ran %v", c)
				}
			}
		})
	}
}

// A hostd killed after the files and before the units (the units step
// failed here) has not touched hostd.env; the next start finishes the
// rest and only then adds the flag.
func TestAStartThatStoppedHalfwayIsFinishedByTheNext(t *testing.T) {
	w := newWorld(t, map[string]string{ApplianceVersion: "2026.11.22.4\n", HostdEnv: env224})
	w.r.Scripts = []shell.Script{{Prefix: []string{"systemctl", "enable"}, Err: errors.New("killed")}}
	if _, err := w.e.Ensure(context.Background()); err == nil {
		t.Fatal("the failed enable was not reported")
	}
	if w.read(HostdEnv) != env224 {
		t.Fatal("hostd.env changed before the units were in place")
	}
	w.r.Scripts = nil
	w.enabledAfter()
	res, err := w.e.Ensure(context.Background())
	if err != nil || !res.Restart || !strings.Contains(w.read(HostdEnv), "HOSTD_ARGS=--owned-host --snapshot-dir") {
		t.Fatalf("second start %+v %v\n%s", res, err, w.read(HostdEnv))
	}
	// The files were already right: only hostd.env was written this time.
	if !slices.Equal(res.Written, []string{HostdEnv}) {
		t.Fatalf("written %v", res.Written)
	}
}

// A host that already has the flag (an image built from main) keeps its
// hostd.env byte for byte.
func TestAHostWithTheFlagIsLeftAlone(t *testing.T) {
	env := strings.Replace(env224, "HOSTD_ARGS=--snapshot-dir", "HOSTD_ARGS=--owned-host --snapshot-dir", 1)
	w := newWorld(t, map[string]string{ApplianceVersion: "2026.11.26\n", HostdEnv: env})
	w.enabledAfter()
	res, err := w.e.Ensure(context.Background())
	if err != nil || res.Restart || slices.Contains(res.Written, HostdEnv) || w.read(HostdEnv) != env {
		t.Fatalf("res %+v err %v", res, err)
	}
}
