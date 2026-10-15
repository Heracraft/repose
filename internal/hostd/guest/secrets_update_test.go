package guest

import (
	"strings"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// lastWrite is the names of the last WriteSecrets a guest's guestd got,
// and whether it got one at all.
func lastWrite(h *harness, id string) (map[string]string, bool) {
	h.t.Helper()
	var out map[string]string
	for _, c := range h.guestd(id).Calls() {
		if ws := c.Req.GetWriteSecrets(); ws != nil {
			out = map[string]string{}
			for _, s := range ws.Secrets {
				out[s.Name] = string(s.Value)
			}
		}
	}
	return out, out != nil
}

// An UpdateSecrets with no secrets is the whole set, empty: the guest's
// last named secret goes, from the running guest and from the next start
// that brings no secrets of its own (found live 2026-10-14: rm --global of
// a project's only secret left it on the guest).
func TestUpdateSecretsWithNoneClearsTheGuest(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.UpdateSecrets{GuestId: gid1}))
	got, ok := lastWrite(h, gid1)
	if !ok {
		t.Fatal("no WriteSecrets after an empty UpdateSecrets")
	}
	if _, stale := got["API_KEY"]; stale {
		t.Fatalf("empty UpdateSecrets wrote the old set: %v", keys(got))
	}
	if _, ok := h.guestd(gid1).Secrets()["API_KEY"]; ok {
		t.Fatal("API_KEY still on the running guest")
	}

	// A start that carries nothing boots from the cache: no API_KEY, and
	// the sshd material create delivered.
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	sec := h.guestd(gid1).Secrets()
	if _, ok := sec["API_KEY"]; ok {
		t.Fatal("API_KEY back after a start")
	}
	if string(sec[SecretHostKey]) != "hostkey" || string(sec[SecretHostCert]) != "hostcert" || string(sec[SecretUserCA]) != "ssh-ed25519 AAAA ca" {
		t.Fatalf("sshd material after start: %v", keys(toStrings(sec)))
	}
}

// UpdateSecrets carries the named secrets only. It replaces those in the
// cache and keeps the sshd material, so a boot that brings no delivery of
// its own (a forced apply reboot, a start after hostd restarted) still has
// the guest's host key.
func TestUpdateSecretsKeepsSSHMaterialForTheNextBoot(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.UpdateSecrets{GuestId: gid1, Secrets: []*hostdv1.Secret{{Name: "TOKEN", Value: []byte("t")}}}))
	got, _ := lastWrite(h, gid1)
	if _, ok := got[SecretHostKey]; ok {
		t.Fatal("UpdateSecrets resent the host key; guestd would reload sshd on every secret change")
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	sec := h.guestd(gid1).Secrets()
	if string(sec["TOKEN"]) != "t" || string(sec[SecretHostKey]) != "hostkey" || string(sec[SecretHostCert]) != "hostcert" || string(sec[SecretUserCA]) != "ssh-ed25519 AAAA ca" {
		t.Fatalf("delivered at start: %v", keys(toStrings(sec)))
	}
	if _, ok := sec["API_KEY"]; ok {
		t.Fatal("API_KEY, replaced by the update, came back at start")
	}
}

// A start with no delivery fields leaves the cache alone: it is the
// "nothing in hand" case the cache exists for (I-26).
func TestStartWithoutDeliveryKeepsCachedSecrets(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if sec := h.guestd(gid1).Secrets(); string(sec["API_KEY"]) != "s3cret" || string(sec[SecretHostKey]) != "hostkey" {
		t.Fatalf("delivered at start: %v", keys(toStrings(sec)))
	}
}

// The start's no_secrets warning says the api has sent no delivery since
// hostd restarted. An UpdateSecrets in that time brings the named set but
// no host key, so the warning still fires; and the update's value, never
// one from before the restart, is what the guest gets.
func TestStartAfterHostdRestartWarnsDespiteAnUpdate(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	b := withBoot(t, h, "boot-1")
	hostRestart(t, h, b, "boot-1")
	h.mustOK(cmd(&hostdv1.UpdateSecrets{GuestId: gid1, Secrets: []*hostdv1.Secret{{Name: "TOKEN", Value: []byte("t")}}}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if !strings.Contains(h.logs.String(), `"reason":"no_secrets"`) {
		t.Fatal("start after a hostd restart and an UpdateSecrets logged no no_secrets warning")
	}
	sec := h.guestd(gid1).Secrets()
	if _, ok := sec["API_KEY"]; ok {
		t.Fatal("API_KEY from before the hostd restart came back")
	}
	if string(sec["TOKEN"]) != "t" {
		t.Fatalf("delivered at start: %v", keys(toStrings(sec)))
	}
}

// After create, an empty UpdateSecrets stores an empty named set; the host
// key is still cached, so a start without delivery does not warn.
func TestStartAfterEmptyUpdateDoesNotWarn(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.UpdateSecrets{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if strings.Contains(h.logs.String(), `"reason":"no_secrets"`) {
		t.Fatal("start with the sshd material cached logged no_secrets")
	}
}

func toStrings(m map[string][]byte) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = string(v)
	}
	return out
}

// keys lists names only, so a failure never prints a value.
func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
