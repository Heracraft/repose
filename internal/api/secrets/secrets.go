// Package secrets is the envelope encryption behind named secrets
// (docs/features/secrets.md, 05-control-plane-api.md §5.6): values are
// AES-256-GCM under a per-user data key, the data key is wrapped by a Key
// Vault key, and both ciphertext and wrapped key live on every secrets
// row. Values are decrypted in exactly one place, DecryptForGuest, whose
// only callers are the hostd command builders.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/db"
)

// KeyVault wraps and unwraps data keys. The real one is Azure Key Vault;
// tests use internal/fakes/kv.
type KeyVault interface {
	Wrap(ctx context.Context, dek []byte) (wrapped []byte, keyVersion string, err error)
	Unwrap(ctx context.Context, wrapped []byte, keyVersion string) (dek []byte, err error)
	CurrentVersion(ctx context.Context) (string, error)
}

// ErrKeyServiceUnavailable is surfaced as `internal: key service
// unavailable`; guest starts retry on it.
var ErrKeyServiceUnavailable = errors.New("key service unavailable")

// ErrInvalidName is returned for names outside the contract.
var ErrInvalidName = errors.New("invalid secret name")

// ErrTooLarge is returned for values over MaxValueBytes.
var ErrTooLarge = errors.New("secret value exceeds 64 KB")

// MaxValueBytes is the value cap from docs/interfaces/api.md.
const MaxValueBytes = 64 << 10

// PlatformProjectID is the pseudo-project that owns the CA material.
const PlatformProjectID = "00000000-0000-7000-8000-000000000000"

// PlatformUserID is the pseudo-user behind it.
const PlatformUserID = "00000000-0000-7000-8000-000000000000"

var nameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// reserved names are the guest sshd material delivered by CreateGuest
// (DECISIONS I-10); they are refused on PUT.
var reserved = map[string]bool{
	"ssh_host_ed25519_key":          true,
	"ssh_host_ed25519_key-cert.pub": true,
	"user_ca.pub":                   true,
}

// shellNames are names the guest uses to keep each command's secrets current
// (DECISIONS I-475): BASH_ENV and ENV name the loader, REPOSE_ENV_GEN is its
// marker. A secret by one of them would switch the refresh off, so PUT
// refuses them; one stored before stays listable and deletable, and guestd
// never exports it.
var shellNames = map[string]bool{
	"BASH_ENV":       true,
	"ENV":            true,
	"REPOSE_ENV_GEN": true,
}

// IsShellName reports whether name is one of the guest's shell names, which
// PUT refuses.
func IsShellName(name string) bool { return shellNames[name] }

// ValidName reports whether a user-facing name is acceptable.
func ValidName(name string) bool {
	return nameRe.MatchString(name) && !reserved[name]
}

// IsReserved reports whether the name is one of the sshd material names.
func IsReserved(name string) bool { return reserved[name] }

// Store is the secrets service.
type Store struct {
	pool *db.Pool
	kv   KeyVault

	mu    sync.Mutex
	cache map[string]cachedDEK
	ttl   time.Duration
}

type cachedDEK struct {
	dek     []byte
	expires time.Time
}

// New builds a store.
func New(pool *db.Pool, kv KeyVault) *Store {
	return &Store{pool: pool, kv: kv, cache: map[string]cachedDEK{}, ttl: 10 * time.Minute}
}

// Meta is what GET /secrets returns: never a value.
type Meta struct {
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NamedValue is a decrypted secret, for a hostd command only.
type NamedValue struct {
	Name  string
	Value []byte
}

func cacheKey(wrapped []byte, version string) string {
	h := sha256.Sum256(wrapped)
	return version + ":" + hex.EncodeToString(h[:8])
}

func kvErr(err error) error {
	return fmt.Errorf("%w: %v", ErrKeyServiceUnavailable, err)
}

func (s *Store) unwrap(ctx context.Context, wrapped []byte, version string) ([]byte, error) {
	k := cacheKey(wrapped, version)
	s.mu.Lock()
	if c, ok := s.cache[k]; ok && time.Now().Before(c.expires) {
		s.mu.Unlock()
		return c.dek, nil
	}
	s.mu.Unlock()
	dek, err := s.kv.Unwrap(ctx, wrapped, version)
	if err != nil {
		return nil, kvErr(err)
	}
	s.mu.Lock()
	s.cache[k] = cachedDEK{dek: dek, expires: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return dek, nil
}

// userDEK returns the user's wrapped DEK from any existing row, or wraps
// a fresh one. Two first-time PUTs racing can create two DEKs for a user;
// every row carries its own wrapped key, so both stay decryptable.
func (s *Store) userDEK(ctx context.Context, tx pgx.Tx, userID string) (dek, wrapped []byte, version string, err error) {
	err = tx.QueryRow(ctx, `select s.dek_wrapped, s.kv_key_version from secrets s join projects p on p.id = s.project_id where p.user_id = $1 order by s.created_at limit 1`, userID).Scan(&wrapped, &version)
	if err == nil {
		dek, err = s.unwrap(ctx, wrapped, version)
		return dek, wrapped, version, err
	}
	if !db.IsNoRows(err) {
		return nil, nil, "", err
	}
	dek = make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, nil, "", err
	}
	wrapped, version, err = s.kv.Wrap(ctx, dek)
	if err != nil {
		return nil, nil, "", kvErr(err)
	}
	s.mu.Lock()
	s.cache[cacheKey(wrapped, version)] = cachedDEK{dek: dek, expires: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return dek, wrapped, version, nil
}

// aad is the additional data that binds a ciphertext to its project and
// its name (DECISIONS I-433), so a row moved to another project or renamed
// does not decrypt. Rows sealed the older way are bound to the name alone;
// open accepts both.
func aad(projectID, name string) []byte {
	return []byte("repose-secret-v2\x00" + projectID + "\x00" + name)
}

// sealBound turns on writing the project-bound form. It is off for one
// release (I-433): an api image from before it cannot open that form, and
// a rolling deploy or a rollback runs one. The release after turns it on
// and runs Reseal.
const sealBound = false

func gcmFor(dek []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func seal(dek, additional, value []byte) ([]byte, error) {
	gcm, err := gcmFor(dek)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return append(nonce, gcm.Seal(nil, nonce, value, additional)...), nil
}

// Seal encrypts value under dek in the form this release writes: bound to
// the name, and to the project as well once sealBound is on.
func Seal(dek []byte, projectID, name string, value []byte) ([]byte, error) {
	if sealBound {
		return SealBound(dek, projectID, name, value)
	}
	return seal(dek, []byte(name), value)
}

// SealBound encrypts value bound to the project and the name, whatever
// sealBound says (Reseal, tests).
func SealBound(dek []byte, projectID, name string, value []byte) ([]byte, error) {
	return seal(dek, aad(projectID, name), value)
}

// Open reverses Seal.
func Open(dek []byte, projectID, name string, ciphertext []byte) ([]byte, error) {
	v, _, err := open(dek, projectID, name, ciphertext)
	return v, err
}

// open reverses Seal and SealBound, and reports legacy when the
// ciphertext is bound to the name alone.
func open(dek []byte, projectID, name string, ciphertext []byte) (value []byte, legacy bool, err error) {
	gcm, err := gcmFor(dek)
	if err != nil {
		return nil, false, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, false, errors.New("ciphertext too short")
	}
	nonce, sealed := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	if v, err := gcm.Open(nil, nonce, sealed, aad(projectID, name)); err == nil {
		return v, false, nil
	}
	v, err := gcm.Open(nil, nonce, sealed, []byte(name))
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

// Put stores or replaces a secret.
func (s *Store) Put(ctx context.Context, userID, projectID, name string, value []byte) error {
	if !nameRe.MatchString(name) && !s.isPlatform(projectID) {
		return ErrInvalidName
	}
	if reserved[name] || shellNames[name] {
		return ErrInvalidName
	}
	if len(value) > MaxValueBytes && !s.isPlatform(projectID) {
		return ErrTooLarge
	}
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		dek, wrapped, version, err := s.userDEK(ctx, tx, userID)
		if err != nil {
			return err
		}
		ct, err := Seal(dek, projectID, name, value)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `insert into secrets (id, project_id, name, ciphertext, dek_wrapped, kv_key_version) values ($1, $2, $3, $4, $5, $6)
			on conflict (project_id, name) do update set ciphertext = excluded.ciphertext, dek_wrapped = excluded.dek_wrapped, kv_key_version = excluded.kv_key_version`,
			uuid.Must(uuid.NewV7()), projectID, name, ct, wrapped, version)
		return err
	})
}

func (s *Store) isPlatform(projectID string) bool { return projectID == PlatformProjectID }

// PutReserved stores the guest sshd material (DECISIONS I-10, I-35) under
// one of the reserved names; user routes never reach it.
func (s *Store) PutReserved(ctx context.Context, userID, projectID, name string, value []byte) error {
	if !reserved[name] {
		return ErrInvalidName
	}
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		dek, wrapped, version, err := s.userDEK(ctx, tx, userID)
		if err != nil {
			return err
		}
		ct, err := Seal(dek, projectID, name, value)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `insert into secrets (id, project_id, name, ciphertext, dek_wrapped, kv_key_version) values ($1, $2, $3, $4, $5, $6)
			on conflict (project_id, name) do update set ciphertext = excluded.ciphertext, dek_wrapped = excluded.dek_wrapped, kv_key_version = excluded.kv_key_version`,
			uuid.Must(uuid.NewV7()), projectID, name, ct, wrapped, version)
		return err
	})
}

// Delete removes a secret; missing is not an error.
func (s *Store) Delete(ctx context.Context, projectID, name string) (bool, error) {
	tag, err := s.pool.Exec(ctx, "delete from secrets where project_id = $1 and name = $2", projectID, name)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// List returns names and timestamps of the user's secrets; the reserved
// sshd material is not listed.
func (s *Store) List(ctx context.Context, projectID string) ([]Meta, error) {
	rows, err := s.pool.Query(ctx, "select name, created_at, updated_at from secrets where project_id = $1 and name not in ('ssh_host_ed25519_key','ssh_host_ed25519_key-cert.pub','user_ca.pub') order by name", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Meta{}
	for rows.Next() {
		var m Meta
		if err := rows.Scan(&m.Name, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DecryptForGuest returns every secret of a project in plaintext. It
// exists for the hostd command builders (CreateGuest, StartGuest,
// Restore, UpdateSecrets) and for the platform's own CA material; nothing
// else may call it.
func (s *Store) DecryptForGuest(ctx context.Context, projectID string) ([]NamedValue, error) {
	rows, err := s.pool.Query(ctx, "select name, ciphertext, dek_wrapped, kv_key_version from secrets where project_id = $1 order by name", projectID)
	if err != nil {
		return nil, err
	}
	type row struct {
		name, version string
		ct, wrapped   []byte
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.ct, &r.wrapped, &r.version); err != nil {
			rows.Close()
			return nil, err
		}
		rs = append(rs, r)
	}
	rows.Close()
	out := []NamedValue{}
	for _, r := range rs {
		dek, err := s.unwrap(ctx, r.wrapped, r.version)
		if err != nil {
			return nil, err
		}
		v, legacy, err := open(dek, projectID, r.name, r.ct)
		if err == nil && legacy {
			err = s.refusePlatformDEK(ctx, projectID, dek)
		}
		if err != nil {
			return nil, fmt.Errorf("secret %s: %w", r.name, err)
		}
		out = append(out, NamedValue{Name: r.name, Value: v})
	}
	return out, nil
}

// ErrPlatformKey is a row outside the platform pseudo-project under the
// platform's data key: platform material copied into a project.
var ErrPlatformKey = errors.New("ciphertext is under the platform data key")

// refusePlatformDEK fails when dek is one of the platform's data keys and
// projectID is not the platform's. Only a row bound to its name alone
// needs it; a row bound to its project cannot open anywhere else.
func (s *Store) refusePlatformDEK(ctx context.Context, projectID string, dek []byte) error {
	if s.isPlatform(projectID) {
		return nil
	}
	rows, err := s.pool.Query(ctx, "select distinct dek_wrapped, kv_key_version from secrets where project_id = $1", PlatformProjectID)
	if err != nil {
		return err
	}
	type wrappedKey struct {
		wrapped []byte
		version string
	}
	var keys []wrappedKey
	for rows.Next() {
		var k wrappedKey
		if err := rows.Scan(&k.wrapped, &k.version); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range keys {
		pk, err := s.unwrap(ctx, k.wrapped, k.version)
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(pk, dek) == 1 {
			return ErrPlatformKey
		}
	}
	return nil
}

// Reseal rewrites every row still bound to its name alone so it is bound
// to its project as well, and returns how many it rewrote. A row under the
// platform's data key outside the platform project is left alone and
// counted in refused. Idempotent. Nothing calls it in this release: an
// older api image cannot open what it writes; the next release runs it at
// start with sealBound on (I-433).
func (s *Store) Reseal(ctx context.Context) (resealed, refused int, err error) {
	rows, err := s.pool.Query(ctx, "select id, project_id, name, ciphertext, dek_wrapped, kv_key_version from secrets")
	if err != nil {
		return 0, 0, err
	}
	type row struct {
		id            uuid.UUID
		project, name string
		ct, wrapped   []byte
		version       string
	}
	var rs []row
	for rows.Next() {
		var r row
		var pid uuid.UUID
		if err := rows.Scan(&r.id, &pid, &r.name, &r.ct, &r.wrapped, &r.version); err != nil {
			rows.Close()
			return 0, 0, err
		}
		r.project = pid.String()
		rs = append(rs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	for _, r := range rs {
		dek, err := s.unwrap(ctx, r.wrapped, r.version)
		if err != nil {
			return resealed, refused, err
		}
		v, legacy, err := open(dek, r.project, r.name, r.ct)
		if err != nil || !legacy {
			if err != nil {
				refused++
			}
			continue
		}
		if err := s.refusePlatformDEK(ctx, r.project, dek); err != nil {
			if errors.Is(err, ErrPlatformKey) {
				refused++
				continue
			}
			return resealed, refused, err
		}
		ct, err := SealBound(dek, r.project, r.name, v)
		if err != nil {
			return resealed, refused, err
		}
		tag, err := s.pool.Exec(ctx, "update secrets set ciphertext = $1 where id = $2 and ciphertext = $3", ct, r.id, r.ct)
		if err != nil {
			return resealed, refused, err
		}
		resealed += int(tag.RowsAffected())
	}
	return resealed, refused, nil
}

// CopyNamed copies a project's named secrets (not its sshd material) to
// another project of the same user inside tx: each value is opened and
// sealed again for the new project under the same data key, so a
// project-bound row stays bound to its own project (I-433).
func (s *Store) CopyNamed(ctx context.Context, tx pgx.Tx, fromProjectID, toProjectID string) error {
	rows, err := tx.Query(ctx, `select name, ciphertext, dek_wrapped, kv_key_version from secrets where project_id = $1 and name ~ '^[A-Z][A-Z0-9_]{0,63}$' order by name`, fromProjectID)
	if err != nil {
		return err
	}
	type row struct {
		name, version string
		ct, wrapped   []byte
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.ct, &r.wrapped, &r.version); err != nil {
			rows.Close()
			return err
		}
		rs = append(rs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range rs {
		dek, err := s.unwrap(ctx, r.wrapped, r.version)
		if err != nil {
			return err
		}
		v, legacy, err := open(dek, fromProjectID, r.name, r.ct)
		if err == nil && legacy {
			err = s.refusePlatformDEK(ctx, fromProjectID, dek)
		}
		if err != nil {
			return fmt.Errorf("secret %s: %w", r.name, err)
		}
		ct, err := Seal(dek, toProjectID, r.name, v)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into secrets (id, project_id, name, ciphertext, dek_wrapped, kv_key_version) values ($1, $2, $3, $4, $5, $6)`,
			uuid.Must(uuid.NewV7()), toProjectID, r.name, ct, r.wrapped, r.version); err != nil {
			return err
		}
	}
	return nil
}

// Rewrap re-wraps every row's DEK under the Key Vault key's current
// version without touching ciphertext, and returns how many rows changed.
func (s *Store) Rewrap(ctx context.Context) (int, error) {
	current, err := s.kv.CurrentVersion(ctx)
	if err != nil {
		return 0, kvErr(err)
	}
	rows, err := s.pool.Query(ctx, "select id, dek_wrapped, kv_key_version from secrets where kv_key_version <> $1", current)
	if err != nil {
		return 0, err
	}
	type row struct {
		id      uuid.UUID
		wrapped []byte
		version string
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.wrapped, &r.version); err != nil {
			rows.Close()
			return 0, err
		}
		rs = append(rs, r)
	}
	rows.Close()
	n := 0
	rewrapped := map[string][]byte{}
	for _, r := range rs {
		k := cacheKey(r.wrapped, r.version)
		nw, ok := rewrapped[k]
		if !ok {
			dek, err := s.unwrap(ctx, r.wrapped, r.version)
			if err != nil {
				return n, err
			}
			nw, _, err = s.kv.Wrap(ctx, dek)
			if err != nil {
				return n, kvErr(err)
			}
			rewrapped[k] = nw
			s.mu.Lock()
			s.cache[cacheKey(nw, current)] = cachedDEK{dek: dek, expires: time.Now().Add(s.ttl)}
			s.mu.Unlock()
		}
		if _, err := s.pool.Exec(ctx, "update secrets set dek_wrapped = $1, kv_key_version = $2 where id = $3 and kv_key_version = $4", nw, current, r.id, r.version); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// PutPlatform stores platform material (CA keys) under the pseudo-project.
func (s *Store) PutPlatform(ctx context.Context, name string, value []byte) error {
	return s.Put(ctx, PlatformUserID, PlatformProjectID, name, value)
}

// GetPlatform reads one platform value; db.ErrNotFound when absent.
func (s *Store) GetPlatform(ctx context.Context, name string) ([]byte, error) {
	vals, err := s.DecryptForGuest(ctx, PlatformProjectID)
	if err != nil {
		return nil, err
	}
	for _, v := range vals {
		if v.Name == name {
			return v.Value, nil
		}
	}
	return nil, db.ErrNotFound
}
