package notify_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/db"
)

// fakePlatformSecrets is an in-memory stand-in for secrets.Store's
// GetPlatform/PutPlatform, enough to test key provisioning without a
// database.
type fakePlatformSecrets struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newFakePlatformSecrets() *fakePlatformSecrets {
	return &fakePlatformSecrets{values: map[string][]byte{}}
}

func (f *fakePlatformSecrets) GetPlatform(_ context.Context, name string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.values[name]
	if !ok {
		return nil, db.ErrNotFound
	}
	return v, nil
}

func (f *fakePlatformSecrets) PutPlatform(_ context.Context, name string, value []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[name] = value
	return nil
}

func TestLoadOrCreateUnsubscriberProvisionsOnce(t *testing.T) {
	sec := newFakePlatformSecrets()
	u1, err := notify.LoadOrCreateUnsubscriber(context.Background(), sec)
	if err != nil {
		t.Fatal(err)
	}
	u2, err := notify.LoadOrCreateUnsubscriber(context.Background(), sec)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	exp := time.Now().Add(time.Hour)
	if u1.Sign(id, exp) != u2.Sign(id, exp) {
		t.Fatal("second load minted a different key instead of reusing the stored one")
	}
	if len(sec.values) != 1 {
		t.Fatalf("expected exactly one stored secret, got %d", len(sec.values))
	}
}

func TestUnsubscriberVerifyRoundTrip(t *testing.T) {
	sec := newFakePlatformSecrets()
	u, err := notify.LoadOrCreateUnsubscriber(context.Background(), sec)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	token := u.Sign(id, time.Now().Add(notify.UnsubTTL))
	got, err := u.Verify(token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("verify returned %s, want %s", got, id)
	}
}

func TestUnsubscriberRejectsTamperedToken(t *testing.T) {
	sec := newFakePlatformSecrets()
	u, err := notify.LoadOrCreateUnsubscriber(context.Background(), sec)
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour)
	real := u.Sign(uuid.New(), exp)
	other := u.Sign(uuid.New(), exp)
	realID, _, _ := strings.Cut(real, ".")
	_, otherSig, _ := strings.Cut(other, ".")
	forged := realID + "." + otherSig
	if _, err := u.Verify(forged, time.Now()); err == nil {
		t.Fatal("a forged token verified")
	}
	if _, err := u.Verify("not-even-a-token", time.Now()); err == nil {
		t.Fatal("a malformed token verified")
	}
	if _, err := u.Verify("", time.Now()); err == nil {
		t.Fatal("an empty token verified")
	}
}

func TestUnsubscriberURL(t *testing.T) {
	sec := newFakePlatformSecrets()
	u, err := notify.LoadOrCreateUnsubscriber(context.Background(), sec)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	url := u.URL("https://api.repose.herakraft.co/", id, time.Now())
	if !strings.HasPrefix(url, "https://api.repose.herakraft.co/v1/notify/unsubscribe?token=") {
		t.Fatalf("url %q", url)
	}
}

// An unsubscribe link stops working UnsubTTL after it was sent; the
// expiry is inside the signature, so it cannot be pushed out.
func TestUnsubscribeTokenExpires(t *testing.T) {
	sec := newFakePlatformSecrets()
	u, err := notify.LoadOrCreateUnsubscriber(context.Background(), sec)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	now := time.Now()
	tok := u.Sign(id, now.Add(notify.UnsubTTL))
	if _, err := u.Verify(tok, now.Add(notify.UnsubTTL-time.Minute)); err != nil {
		t.Fatalf("inside the ttl: %v", err)
	}
	if got, err := u.Verify(tok, now.Add(notify.UnsubTTL+time.Minute)); !errors.Is(err, notify.ErrUnsubExpired) || got != id {
		t.Fatalf("past the ttl: %s %v, want ErrUnsubExpired", got, err)
	}
	// Re-signing the payload with a later expiry needs the key.
	p, sig, _ := strings.Cut(tok, ".")
	later, _, _ := strings.Cut(u.Sign(id, now.Add(10*notify.UnsubTTL)), ".")
	if _, err := u.Verify(later+"."+sig, now); err == nil {
		t.Fatal("a token with a moved expiry verified")
	}
	_ = p
	// A link from before expiring tokens (the user id alone, MACed bare)
	// still verifies for one release.
	mac := hmac.New(sha256.New, sec.values[notify.UnsubKeyName])
	mac.Write(id[:])
	legacy := base64.RawURLEncoding.EncodeToString(id[:]) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if got, err := u.Verify(legacy, now.Add(1000*time.Hour)); err != nil || got != id {
		t.Fatalf("legacy token: %s %v", got, err)
	}
}
