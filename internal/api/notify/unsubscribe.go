package notify

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/db"
)

// UnsubKeyName is the platform secret holding the unsubscribe HMAC key,
// stored the same way as the CA material (internal/api/ca): ciphertext in
// the secrets table under the platform pseudo-project, never a fourth home.
const UnsubKeyName = "NOTIFY_UNSUB_KEY"

// PlatformSecrets is the slice of secrets.Store an Unsubscriber needs.
type PlatformSecrets interface {
	GetPlatform(ctx context.Context, name string) ([]byte, error)
	PutPlatform(ctx context.Context, name string, value []byte) error
}

// Unsubscriber signs and verifies the one-click unsubscribe link
// (13-notifications.md §5.6): the token names the user and is checked
// without a database round trip, so the link works even if the click
// arrives with no session and no bearer token.
type Unsubscriber struct{ key []byte }

// LoadOrCreateUnsubscriber reads the signing key, generating and storing
// one the first time the api starts. An operator step here (mirroring
// `repose-admin ca init`) would leave the very first account's unsubscribe
// link broken until someone remembered to run it, so this one key
// provisions itself.
func LoadOrCreateUnsubscriber(ctx context.Context, sec PlatformSecrets) (*Unsubscriber, error) {
	key, err := sec.GetPlatform(ctx, UnsubKeyName)
	if err == nil {
		return &Unsubscriber{key: key}, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, rerr := rand.Read(key); rerr != nil {
		return nil, rerr
	}
	if perr := sec.PutPlatform(ctx, UnsubKeyName, key); perr != nil {
		return nil, perr
	}
	return &Unsubscriber{key: key}, nil
}

// UnsubTTL is how long an unsubscribe link works after the email that
// carried it was sent. The dashboard's notification settings turn email
// off at any time; the link is a shortcut.
const UnsubTTL = 90 * 24 * time.Hour

// unsubDomain separates the expiring unsubscribe MAC from the reply MAC and
// from the first token shape, which MACed the bare user id.
const unsubDomain = "repose-unsub-v2\x00"

// ErrUnsubExpired is a well-signed unsubscribe token past its expiry.
var ErrUnsubExpired = errors.New("unsubscribe: the link has expired")

// Sign returns the token embedded in a `/notify/unsubscribe?token=` link:
// the user id and the expiry, signed.
func (u *Unsubscriber) Sign(userID uuid.UUID, expires time.Time) string {
	payload := make([]byte, 16+8)
	copy(payload, userID[:])
	binary.BigEndian.PutUint64(payload[16:], uint64(expires.Unix()))
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(u.unsubMAC(payload))
}

// Verify recovers the user id from a token, or an error if it is
// malformed, was not signed by this key, or is past its expiry
// (ErrUnsubExpired). A token of the first shape (the user id alone, no
// expiry), which emails sent before expiring links carry, is still
// accepted for one release (DECISIONS I-442).
func (u *Unsubscriber) Verify(token string, now time.Time) (uuid.UUID, error) {
	idPart, sigPart, ok := strings.Cut(token, ".")
	if !ok {
		return uuid.Nil, errors.New("unsubscribe: malformed token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(idPart)
	if err != nil {
		return uuid.Nil, errors.New("unsubscribe: malformed token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return uuid.Nil, errors.New("unsubscribe: malformed token")
	}
	switch len(payload) {
	case 16 + 8:
		if !hmac.Equal(sig, u.unsubMAC(payload)) {
			return uuid.Nil, errors.New("unsubscribe: invalid token")
		}
		id, _ := uuid.FromBytes(payload[:16])
		if now.Unix() > int64(binary.BigEndian.Uint64(payload[16:])) {
			return id, ErrUnsubExpired
		}
		return id, nil
	case 16:
		id, _ := uuid.FromBytes(payload)
		if !hmac.Equal(sig, u.legacyMAC(id)) {
			return uuid.Nil, errors.New("unsubscribe: invalid token")
		}
		return id, nil
	}
	return uuid.Nil, errors.New("unsubscribe: malformed token")
}

func (u *Unsubscriber) unsubMAC(payload []byte) []byte {
	h := hmac.New(sha256.New, u.key)
	h.Write([]byte(unsubDomain))
	h.Write(payload)
	return h.Sum(nil)
}

// legacyMAC is the first token shape's MAC, kept to verify links already
// sent.
func (u *Unsubscriber) legacyMAC(id uuid.UUID) []byte {
	h := hmac.New(sha256.New, u.key)
	h.Write(id[:])
	return h.Sum(nil)
}

// URL builds the unsubscribe link an email template embeds. base is the
// api's own public origin (`API_RESOURCE`), not the dashboard's: the route
// is served by the api, not the SPA, at the same /v1 prefix as every other
// route (docs/interfaces/api.md).
// The link opens a confirmation page (GET) whose button, or a mail
// client's one-click unsubscribe (RFC 8058), POSTs to the same URL; only
// the POST changes anything. It expires UnsubTTL after now.
func (u *Unsubscriber) URL(base string, userID uuid.UUID, now time.Time) string {
	return fmt.Sprintf("%s/v1/notify/unsubscribe?token=%s", strings.TrimRight(base, "/"), u.Sign(userID, now.Add(UnsubTTL)))
}
