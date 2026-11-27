package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ENG-141, DECISIONS I-1440. `repose host rm --local` said "with every
// machine and snapshot on its disk" to a customer with three machines
// and to one with none, and said nothing at all about the account, where
// the removal leaves a machine whose destroy needs the host that just
// went. Twice on 2026-11-20 that wedged the owner's account and an
// operator had to run `repose-admin hosts mark-lost` to free it.
func TestHostRmLocalSaysWhatGoesWithIt(t *testing.T) {
	cases := []struct {
		name   string
		local  string
		host   *Host
		unread bool
		d2     bool
		want   []string
		not    []string
	}{
		{
			name:  "three machines are counted",
			local: "mac-park3",
			host:  &Host{Name: "mac-park3", Projects: 3},
			d2:    true,
			want:  []string{"Remove mac-park3 from this Mac?", "The 3 machines on it cannot be recovered", "their disks are on this Mac"},
			not:   []string{"every machine and snapshot"},
		},
		{
			name:  "one machine is not 1 machines",
			local: "mac-rig2",
			host:  &Host{Name: "mac-rig2", Projects: 1},
			want:  []string{"The machine on it cannot be recovered", "its disk is on this Mac"},
			not:   []string{"1 machines", "machines on it"},
		},
		{
			name:  "a host with none keeps the plain question",
			local: "mac-empty",
			host:  &Host{Name: "mac-empty", Projects: 0},
			want:  []string{"with every machine and snapshot on its disk?"},
			not:   []string{"cannot be recovered", "could not be read"},
		},
		{
			// The case that reached the wedge: an expired login refuses
			// the two account commands and `--local` still runs, which
			// is correct. What it must not do is imply it checked.
			name:   "an account that cannot be read is said, not implied",
			local:  "mac-park3",
			unread: true,
			want:   []string{"Your account could not be read, so this cannot say how many machines go with it"},
			not:    []string{"cannot be recovered"},
		},
		{
			name:  "repose added with --join has no name to look up",
			local: "",
			d2:    true,
			want:  []string{"Remove repose from this Mac, with every machine and snapshot on its disk?"},
		},
		{
			name:  "an appliance added with --join has no name to look up",
			local: "",
			want:  []string{"Remove the appliance on this Mac, with every machine and snapshot on its disk?"},
			not:   []string{"could not be read", "cannot be recovered"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := localRemovePrompt(c.local, c.host, c.unread, c.d2)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("prompt does not say %q:\n%s", w, got)
				}
			}
			for _, n := range c.not {
				if strings.Contains(got, n) {
					t.Errorf("prompt still says %q:\n%s", n, got)
				}
			}
			if !strings.HasSuffix(got, "[y/N] ") {
				t.Errorf("prompt does not end in the answer it wants:\n%s", got)
			}
		})
	}
}

// The line after the removal is the one that unblocks the account: the
// host is still there, its machines have nowhere to run, and I-1420 is
// what gets rid of it. Without this the customer has three documented
// commands that all refuse (ENG-119, ENG-120, ENG-141).
func TestHostRmLocalNamesTheWayOutOfTheWedge(t *testing.T) {
	e, _, m := newD2Mac(t)
	var errOut strings.Builder
	e.ErrOut = &errOut
	root := m.mapped(macHostPaths.Root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.mapped(macHostPaths.Record()), []byte(`{"host":"mac-park3","user":"dev","uid":501}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "volumes.raw"), []byte("a machine's disk"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/hosts" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"h1","name":"mac-park3","state":"up","projects":2,"mem_bytes":0,"vcpus":0,"cores":0,"last_heartbeat_at":null,"registered_at":null,"created_at":"2026-11-20T00:00:00Z"}]`))
	}))
	defer srv.Close()
	e.Client = newClient(srv.URL+"/v1", staticToken("tok"))
	// Before srv.Close, which otherwise waits five seconds on the
	// keep-alive this one request leaves open.
	defer e.Client.HTTP.CloseIdleConnections()

	var asked string
	confirm := func(prompt string) (bool, error) { asked = prompt; return true, nil }
	if err := HostRmLocalCmd(context.Background(), e, false, confirm); err != nil {
		t.Fatalf("rm --local: %v", err)
	}
	if !strings.Contains(asked, "The 2 machines on it cannot be recovered") {
		t.Errorf("the question did not count the machines:\n%s", asked)
	}
	said := errOut.String()
	for _, w := range []string{"mac-park3 is still on your account", "with 2 machines that no longer have a host.", "repose host rm mac-park3", "once it reports not answering"} {
		if !strings.Contains(said, w) {
			t.Errorf("after the removal, nothing says %q:\n%s", w, said)
		}
	}
	if _, err := os.Stat(root); err == nil {
		t.Error("repose's directory is still there")
	}
}

// A login that cannot answer makes the command say less, never refuse:
// `--local` is the one command that works when the api does not, and
// that is deliberate (ENG-141).
func TestHostRmLocalRunsWithNoSession(t *testing.T) {
	e, _, m := newD2Mac(t)
	var errOut strings.Builder
	e.ErrOut = &errOut
	root := m.mapped(macHostPaths.Root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.mapped(macHostPaths.Record()), []byte(`{"host":"mac-park3","user":"dev","uid":501}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthorized"}`))
	}))
	defer srv.Close()
	e.Client = newClient(srv.URL+"/v1", staticToken("tok"))
	defer e.Client.HTTP.CloseIdleConnections()

	var asked string
	confirm := func(prompt string) (bool, error) { asked = prompt; return true, nil }
	if err := HostRmLocalCmd(context.Background(), e, false, confirm); err != nil {
		t.Fatalf("rm --local with no session must still run: %v", err)
	}
	if !strings.Contains(asked, "could not be read") {
		t.Errorf("the question claimed to know what it could not read:\n%s", asked)
	}
	if errOut.String() != "" {
		t.Errorf("it told the user about an account it could not read: %q", errOut.String())
	}
	if _, err := os.Stat(root); err == nil {
		t.Error("repose's directory is still there")
	}
}
