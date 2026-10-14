package gateway

import (
	"fmt"
	"testing"
	"time"
)

// TestRunningWithNoAddressIsNotAnUnreachableHost is the second half of
// the teksafari-org outage of 2026-10-13 (DECISIONS I-721). The project
// was running, its host was answering, and the api simply held no address
// for the guest; the gateway collapsed that with `host_unreachable` into
// one banner and told the user the machine was "on a host the control
// plane cannot reach right now; try again shortly" — false, and advice
// that would never have come true.
//
// The two states now read differently, and the no-address banner names
// the two commands that fix it.
func TestRunningWithNoAddressIsNotAnUnreachableHost(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)

	addr := h.projectRoute(t).GuestIP
	if addr == "" {
		t.Fatal("the harness project has no address to begin with")
	}

	// Running, host fine, no address: today's outage.
	h.api.SetGuestIP(h.project.ID, "")
	_, banner, err := h.dial(h.login, cert)
	if err == nil {
		t.Fatal("connected to a project with no address")
	}
	want := fmt.Sprintf(MsgNoAddressFmt, h.project.Slug, h.project.Slug, h.project.Slug)
	if banner != want {
		t.Fatalf("banner %q, want %q", banner, want)
	}
	if unreachable := fmt.Sprintf(MsgUnreachableHostFmt, h.project.Slug); banner == unreachable {
		t.Fatal("a missing address still blames the host")
	}
	t.Logf("no address: %q", banner)

	// The host really being away keeps its own banner.
	h.api.SetGuestIP(h.project.ID, addr)
	h.api.SetHostUnreachable(h.project.ID, true)
	h.offset.Add(6) // past the route cache
	_, banner, err = h.dial(h.login, cert)
	if err == nil {
		t.Fatal("connected to a project on an unreachable host")
	}
	if w := fmt.Sprintf(MsgUnreachableHostFmt, h.project.Slug); banner != w {
		t.Fatalf("banner %q, want %q", banner, w)
	}
	t.Logf("unreachable host: %q", banner)
}

// TestNoAddressCountsAsItsOwnAuthFailReason: the two states are also two
// `reason` values of repose_gateway_auth_fail_total, so the one that is
// the api's own record being wrong can be alerted on separately
// (I-721). route_error stays what it was for a route lookup that failed.
func TestNoAddressCountsAsItsOwnAuthFailReason(t *testing.T) {
	if ResultNoAddress == ResultRouteError {
		t.Fatal("no_address is not a separate reason")
	}
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	h.api.SetGuestIP(h.project.ID, "")
	if _, _, err := h.dial(h.login, cert); err == nil {
		t.Fatal("connected to a project with no address")
	}
	if n := h.authFails(ResultNoAddress); n == 0 {
		t.Fatalf("no_address auth failures %d, want at least 1", n)
	}
	if n := h.authFails(ResultRouteError); n != 0 {
		t.Fatalf("route_error auth failures %d, want 0", n)
	}
}
