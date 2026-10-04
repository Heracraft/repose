package shell

import (
	"slices"
	"strings"
	"testing"
)

// A filesystem parser runs as a dynamic user with no capabilities and no
// network, sees only the one device, and its argv is passed through last.
func TestSandboxedConfinesTheParser(t *testing.T) {
	ro := Sandboxed("/dev/vg-guests/snap-x", false, "dumpe2fs", "/dev/vg-guests/snap-x")
	rw := Sandboxed("/dev/vg-guests/g-x", true, "e2fsck", "-fp", "/dev/vg-guests/g-x")
	for _, argv := range [][]string{ro, rw} {
		if !slices.Equal(argv[:5], []string{"systemd-run", "--quiet", "--pipe", "--wait", "--collect"}) {
			t.Fatalf("not a waited, piped transient unit: %v", argv)
		}
		joined := strings.Join(argv, " ")
		for _, want := range []string{"DynamicUser=yes", "DevicePolicy=closed", "PrivateNetwork=yes", "CapabilityBoundingSet=", "InaccessiblePaths=-/var/lib/repose", "InaccessiblePaths=-/run/repose", "NoNewPrivileges=yes"} {
			if !slices.Contains(argv, want) {
				t.Fatalf("sandbox lacks %s: %s", want, joined)
			}
		}
		if strings.Contains(joined, "User=root") || strings.Contains(joined, "AmbientCapabilities") {
			t.Fatalf("sandbox grants privilege: %s", joined)
		}
		if n := strings.Count(joined, "DeviceAllow="); n != 1 {
			t.Fatalf("%d DeviceAllow entries, want one: %s", n, joined)
		}
	}
	if !slices.Contains(ro, "DeviceAllow=/dev/vg-guests/snap-x r") || !slices.Contains(rw, "DeviceAllow=/dev/vg-guests/g-x rw") {
		t.Fatalf("device access: %v / %v", ro, rw)
	}
	if got := ro[len(ro)-3:]; !slices.Equal(got, []string{"--", "dumpe2fs", "/dev/vg-guests/snap-x"}) {
		t.Fatalf("argv tail %v", got)
	}
}
