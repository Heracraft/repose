package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// Council findings 10 and 12 (I-1522): status names the base and whether
// the machine is Ubuntu or NixOS, and status and ls say when a newer base
// waits for a restart.
func TestStatusAndLsSayTheBase(t *testing.T) {
	pending := "2026.11.30"
	ubuntu := Project{ID: "p1", Slug: "app", State: "running", Class: "large", BaseVersion: "2026.11.22", GuestRuntime: "erofs", PendingBaseVersion: &pending, VolumeBytes: 40 << 30}
	legacy := Project{ID: "p2", Slug: "legacy", State: "running", Class: "large", BaseVersion: "2026.09.15", GuestRuntime: "nix", VolumeBytes: 40 << 30}

	var st bytes.Buffer
	writeStatus(&st, statusView{p: &ubuntu, now: time.Now()})
	got := st.String()
	for _, want := range []string{"  base 2026.11.30 is ready; app uses it at its next restart\n", "  base       2026.11.22, Ubuntu\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status lacks %q:\n%s", want, got)
		}
	}
	st.Reset()
	writeStatus(&st, statusView{p: &legacy, now: time.Now()})
	if !strings.Contains(st.String(), "2026.09.15, NixOS") || strings.Contains(st.String(), "is ready") {
		t.Fatalf("NixOS status:\n%s", st.String())
	}

	var ls bytes.Buffer
	writeProjectsTableHere(&ls, []Project{ubuntu, legacy}, "", nil)
	if !strings.Contains(ls.String(), "app: base 2026.11.30 is ready; app uses it at its next restart\n") || strings.Contains(ls.String(), "legacy: base") {
		t.Fatalf("ls:\n%s", ls.String())
	}
}
