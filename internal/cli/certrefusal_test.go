package cli

import (
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/gateway"
)

// TestRevokedCertificateIsReissuedOnce is the 2026-09-21 finding (I-175):
// the gateway's "certificate revoked" banner gets one forced re-issue and
// an immediate retry; a second certificate refusal ends the wait at once
// with the gateway's words; refusals a certificate cannot fix (a guest not
// accepting yet, the control plane, busy) never spend the re-issue. The
// banners are the gateway's own constants, so a reworded banner fails here.
func TestRevokedCertificateIsReissuedOnce(t *testing.T) {
	refused := func(banner string) *sshError {
		return &sshError{ExitCode: 255, Stderr: banner + "\nizma.me@ssh.repose.herakraft.co: Permission denied (publickey).\n"}
	}
	for _, m := range []string{gateway.MsgRevoked, gateway.MsgExpired, gateway.MsgNotYetValid, gateway.MsgBadCA, gateway.MsgCertRequired, gateway.MsgWrongPrincipal, ""} {
		if !isCertRefusal(refused(m)) {
			t.Errorf("%q is not taken for a certificate refusal", m)
		}
	}
	for _, m := range []string{gateway.MsgControlPlane, gateway.MsgBusy, gateway.MsgUserBusy, gateway.MsgRateLimited, "izma is starting; " + gateway.MsgNotReady, "izma is stopped; run `repose start`", "no such project: izma.me", gateway.BadLoginMessage} {
		if isCertRefusal(refused(m)) {
			t.Errorf("%q is taken for a certificate refusal", m)
		}
	}
	// A source over the gateway's pre-auth limits is closed before the key
	// exchange (I-435): no certificate can fix that.
	if isCertRefusal(&sshError{ExitCode: 255, Stderr: "kex_exchange_identification: Connection closed by remote host\n"}) {
		t.Error("a connection closed before the handshake is taken for a certificate refusal")
	}
	if isCertRefusal(&sshError{ExitCode: 1, Stderr: "Permission denied"}) {
		t.Error("a command's own exit status is taken for a refusal")
	}

	calls := 0
	h := certRefusalHandler(func() error { calls++; return nil })
	if retry, err := h(refused(gateway.MsgNotReady)); retry || err != nil || calls != 0 {
		t.Fatalf("not ready: retry %v err %v reissues %d", retry, err, calls)
	}
	if retry, err := h(refused(gateway.MsgRevoked)); !retry || err != nil || calls != 1 {
		t.Fatalf("revoked: retry %v err %v reissues %d", retry, err, calls)
	}
	retry, err := h(refused(gateway.MsgRevoked))
	if retry || err == nil || calls != 1 || !strings.Contains(err.Error(), "certificate revoked") || !strings.Contains(err.Error(), "repose login") {
		t.Fatalf("revoked again: retry %v err %v reissues %d", retry, err, calls)
	}
}
