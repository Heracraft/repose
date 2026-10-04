// Package kv is an in-memory stand-in for Azure Key Vault's wrap and
// unwrap operations: RSA-OAEP-256 keys by version, a version bump to
// exercise DEK rewrap, and a switch to make the service unavailable.
package kv

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
)

// ErrUnavailable is what every call returns while Down is set.
var ErrUnavailable = errors.New("fake key vault: unavailable")

// Fake is one wrapping key with versions.
type Fake struct {
	mu       sync.Mutex
	keys     map[string]*rsa.PrivateKey
	current  string
	Down     bool
	wraps    int
	unwraps  int
	versions int
}

// New creates a fake with one key version.
func New() *Fake {
	f := &Fake{keys: map[string]*rsa.PrivateKey{}}
	f.BumpVersion()
	return f
}

// BumpVersion rotates to a new key version, keeping the old ones for
// unwrap, as Key Vault does.
func (f *Fake) BumpVersion() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("fake kv: rsa keygen: " + err.Error()) // test fixture; entropy failure is unrecoverable
	}
	f.versions++
	v := fmt.Sprintf("v%d", f.versions)
	f.keys[v] = k
	f.current = v
	return v
}

// SetDown sets Down under the lock, for a test that flips it while another
// goroutine calls the vault.
func (f *Fake) SetDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Down = down
}

// Counts returns how many wrap and unwrap calls were made.
func (f *Fake) Counts() (wraps, unwraps int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wraps, f.unwraps
}

// CurrentVersion implements secrets.KeyVault.
func (f *Fake) CurrentVersion(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return "", ErrUnavailable
	}
	return f.current, nil
}

// Wrap implements secrets.KeyVault.
func (f *Fake) Wrap(ctx context.Context, dek []byte) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return nil, "", ErrUnavailable
	}
	f.wraps++
	out, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &f.keys[f.current].PublicKey, dek, nil)
	if err != nil {
		return nil, "", err
	}
	return out, f.current, nil
}

// Unwrap implements secrets.KeyVault.
func (f *Fake) Unwrap(ctx context.Context, wrapped []byte, version string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return nil, ErrUnavailable
	}
	f.unwraps++
	k, ok := f.keys[version]
	if !ok {
		return nil, fmt.Errorf("fake key vault: unknown key version %s", version)
	}
	return rsa.DecryptOAEP(sha256.New(), rand.Reader, k, wrapped, nil)
}
