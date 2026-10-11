// Package freeze holds the host-side pieces of a guest freeze (DECISIONS
// I-652, I-653): the key that encrypts a frozen guest's memory image, the
// encrypted volume the image is written to, and the record that says what
// the image is and whether it is complete. The state machine that pauses,
// snapshots and restores a guest is internal/hostd/guest/freeze.go.
package freeze

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

// KeyBytes is the size of an image key: aes-xts-plain64 with two 256-bit
// halves.
const KeyBytes = 64

// Sealer wraps a fresh image key so it can sit beside the image on the
// host's disk without being usable there (I-653). Wrap must work with no
// network: a graceful shutdown may be the network's last minute, so it
// encrypts with the RSA public key fetched ahead of time (RSA-OAEP-256).
// Unwrap needs the private key, which never leaves its owner (Key Vault on
// a host): an image on a stolen disk or a disk snapshot cannot be read.
type Sealer interface {
	// Ready reports whether Wrap can work now (the public key is cached).
	Ready() error
	// Wrap encrypts key; keyID names the key version that unwraps it.
	Wrap(key []byte) (wrapped []byte, keyID string, err error)
	// Unwrap decrypts a key Wrap returned.
	Unwrap(ctx context.Context, wrapped []byte, keyID string) ([]byte, error)
	// Refresh fetches the current public key.
	Refresh(ctx context.Context) error
}

// NewKey returns a random image key.
func NewKey() ([]byte, error) {
	k := make([]byte, KeyBytes)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

// Wipe zeroes a key in place.
func Wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func wrapWith(pub *rsa.PublicKey, key []byte) ([]byte, error) {
	return rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, key, nil)
}

// KeyVault is the Sealer on a host: an RSA key in Azure Key Vault that the
// host's managed identity may Get (the public half) and UnwrapKey with,
// nothing else.
type KeyVault struct {
	client  *azkeys.Client
	keyName string

	mu      sync.Mutex
	pub     *rsa.PublicKey
	version string
}

// NewKeyVault connects to the vault at vaultURL as the managed identity
// clientID ("" for the default credential chain).
func NewKeyVault(vaultURL, keyName, clientID string) (*KeyVault, error) {
	var cred azcore.TokenCredential
	var err error
	if clientID != "" {
		cred, err = azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{ID: azidentity.ClientID(clientID)})
	} else {
		cred, err = azidentity.NewDefaultAzureCredential(nil)
	}
	if err != nil {
		return nil, fmt.Errorf("key vault credential: %w", err)
	}
	c, err := azkeys.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("key vault client: %w", err)
	}
	return &KeyVault{client: c, keyName: keyName}, nil
}

// Refresh implements Sealer.
func (k *KeyVault) Refresh(ctx context.Context) error {
	resp, err := k.client.GetKey(ctx, k.keyName, "", nil)
	if err != nil {
		return fmt.Errorf("key vault get %s: %w", k.keyName, err)
	}
	if resp.Key == nil || resp.Key.KID == nil || len(resp.Key.N) == 0 || len(resp.Key.E) == 0 {
		return fmt.Errorf("key vault: key %s is not an RSA key", k.keyName)
	}
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(resp.Key.N), E: int(new(big.Int).SetBytes(resp.Key.E).Int64())}
	k.mu.Lock()
	k.pub, k.version = pub, resp.Key.KID.Version()
	k.mu.Unlock()
	return nil
}

// Ready implements Sealer.
func (k *KeyVault) Ready() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.pub == nil {
		return errors.New("key vault public key not fetched yet")
	}
	return nil
}

// Wrap implements Sealer.
func (k *KeyVault) Wrap(key []byte) ([]byte, string, error) {
	k.mu.Lock()
	pub, version := k.pub, k.version
	k.mu.Unlock()
	if pub == nil {
		return nil, "", errors.New("key vault public key not fetched yet")
	}
	w, err := wrapWith(pub, key)
	return w, version, err
}

// Unwrap implements Sealer.
func (k *KeyVault) Unwrap(ctx context.Context, wrapped []byte, keyID string) ([]byte, error) {
	alg := azkeys.EncryptionAlgorithmRSAOAEP256
	resp, err := k.client.UnwrapKey(ctx, k.keyName, keyID, azkeys.KeyOperationParameters{Algorithm: &alg, Value: wrapped}, nil)
	if err != nil {
		return nil, fmt.Errorf("key vault unwrap: %w", err)
	}
	return resp.Result, nil
}

// FileKey is the Sealer for tests and for a host with no vault: an RSA
// private key in a PEM file. It keeps the image's key off the disk in the
// clear only when the file itself is somewhere the disk is not (a tmpfs
// filled at boot), which is why production uses KeyVault.
type FileKey struct {
	Path string

	mu   sync.Mutex
	priv *rsa.PrivateKey
}

func (f *FileKey) load() (*rsa.PrivateKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.priv != nil {
		return f.priv, nil
	}
	b, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("%s: no PEM block", f.Path)
	}
	var priv *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(blk.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s: not an RSA key", f.Path)
		}
		priv = rk
	} else if rk, err2 := x509.ParsePKCS1PrivateKey(blk.Bytes); err2 == nil {
		priv = rk
	} else {
		return nil, fmt.Errorf("%s: %w", f.Path, err)
	}
	f.priv = priv
	return priv, nil
}

// Refresh implements Sealer.
func (f *FileKey) Refresh(context.Context) error { _, err := f.load(); return err }

// Ready implements Sealer.
func (f *FileKey) Ready() error { _, err := f.load(); return err }

// Wrap implements Sealer.
func (f *FileKey) Wrap(key []byte) ([]byte, string, error) {
	p, err := f.load()
	if err != nil {
		return nil, "", err
	}
	w, err := wrapWith(&p.PublicKey, key)
	return w, "file", err
}

// Unwrap implements Sealer.
func (f *FileKey) Unwrap(_ context.Context, wrapped []byte, _ string) ([]byte, error) {
	p, err := f.load()
	if err != nil {
		return nil, err
	}
	return rsa.DecryptOAEP(sha256.New(), nil, p, wrapped, nil)
}

// GenerateFileKey writes a new 3072-bit RSA key to path (mode 0600), for
// tests and the freeze experiments.
func GenerateFileKey(path string) error {
	k, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return err
	}
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
}
