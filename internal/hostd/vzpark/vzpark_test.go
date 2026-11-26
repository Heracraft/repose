package vzpark

import (
	"context"
	"testing"

	"github.com/heracraft/repose/internal/hostd/park"
)

// The record's key check must hold after a round trip: a sealer that gave
// back another key made every Mac unpark fail "image key does not match"
// (lab Mac, 2026-11-26 15:25Z).
func TestSealerRoundTripKeepsTheKeyCheck(t *testing.T) {
	k, err := park.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	w, id, err := Sealer{}.Wrap(k)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Sealer{}.Unwrap(context.Background(), w, id)
	if err != nil {
		t.Fatal(err)
	}
	if park.KeyCheck(got) != park.KeyCheck(k) {
		t.Fatal("unwrapped key fails the record's check")
	}
	if _, err := (Sealer{}).Unwrap(context.Background(), w, "kv-1"); err == nil {
		t.Fatal("a Key Vault record must not unwrap here")
	}
}

func TestParseSession(t *testing.T) {
	// The lab Mac's IOConsoleUsers, unlocked, then with the lock key set.
	unlocked := `[{"kCGSSessionOnConsoleKey":true,"kCGSessionLoginDoneKey":true,"kCGSSessionUserNameKey":"ec2-user","kCGSSessionUserIDKey":501}]`
	s, err := parseSession([]byte(unlocked), 501)
	if err != nil || !s.Unlocked() {
		t.Fatalf("%+v %v", s, err)
	}
	locked := `[{"kCGSSessionOnConsoleKey":true,"kCGSessionLoginDoneKey":true,"CGSSessionScreenIsLocked":true,"kCGSSessionUserIDKey":501}]`
	if s, _ := parseSession([]byte(locked), 501); s.Unlocked() || !s.Locked {
		t.Fatalf("locked read as %+v", s)
	}
	if s, _ := parseSession([]byte(unlocked), 502); s.LoggedIn || s.Unlocked() {
		t.Fatalf("another user's session read as %+v", s)
	}
	if s, _ := parseSession([]byte(`[]`), 501); s.Unlocked() {
		t.Fatal("no session read as unlocked")
	}
}
