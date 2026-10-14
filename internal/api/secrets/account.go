package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/db"
)

// Account secrets (DECISIONS I-697) are named secrets set once for every
// project of an account. They live in account_secrets under the same
// envelope as a project's (the user's data key, wrapped by Key Vault),
// bound to the user and the name, and reach a machine only through
// DecryptForGuest, merged under the project's own: a project secret of
// the same name wins.

// accountAAD binds an account ciphertext to its user and its name, in a
// form no project row uses, so an account row copied into secrets, or a
// project row into account_secrets, does not open.
func accountAAD(userID, name string) []byte {
	return []byte("repose-account-secret-v1\x00" + userID + "\x00" + name)
}

// SealAccount encrypts an account secret's value.
func SealAccount(dek []byte, userID, name string, value []byte) ([]byte, error) {
	return seal(dek, accountAAD(userID, name), value)
}

// OpenAccount reverses SealAccount.
func OpenAccount(dek []byte, userID, name string, ciphertext []byte) ([]byte, error) {
	gcm, err := gcmFor(dek)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	return gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], accountAAD(userID, name))
}

// PutAccount stores or replaces one of the user's account secrets. The
// names are a project secret's: the reserved sshd names and the guest's
// shell names are refused, and so is the platform pseudo-user.
func (s *Store) PutAccount(ctx context.Context, userID, name string, value []byte) error {
	if !nameRe.MatchString(name) || reserved[name] || shellNames[name] || userID == PlatformUserID {
		return ErrInvalidName
	}
	if len(value) > MaxValueBytes {
		return ErrTooLarge
	}
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		dek, wrapped, version, err := s.userDEK(ctx, tx, userID)
		if err != nil {
			return err
		}
		ct, err := SealAccount(dek, userID, name, value)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `insert into account_secrets (id, user_id, name, ciphertext, dek_wrapped, kv_key_version) values ($1, $2, $3, $4, $5, $6)
			on conflict (user_id, name) do update set ciphertext = excluded.ciphertext, dek_wrapped = excluded.dek_wrapped, kv_key_version = excluded.kv_key_version`,
			uuid.Must(uuid.NewV7()), userID, name, ct, wrapped, version)
		return err
	})
}

// DeleteAccount removes one of the user's account secrets and reports
// whether there was one.
func (s *Store) DeleteAccount(ctx context.Context, userID, name string) (bool, error) {
	tag, err := s.pool.Exec(ctx, "delete from account_secrets where user_id = $1 and name = $2", userID, name)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListAccount returns the names and timestamps of the user's account
// secrets, never a value.
func (s *Store) ListAccount(ctx context.Context, userID string) ([]Meta, error) {
	rows, err := s.pool.Query(ctx, "select name, created_at, updated_at from account_secrets where user_id = $1 order by name", userID)
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

// accountForGuest decrypts the account secrets of a project's user that
// the project has no secret of its own for.
func (s *Store) accountForGuest(ctx context.Context, projectID string) ([]NamedValue, error) {
	rows, err := s.pool.Query(ctx, `select a.user_id, a.name, a.ciphertext, a.dek_wrapped, a.kv_key_version
		from account_secrets a join projects p on p.user_id = a.user_id
		where p.id = $1 and not exists (select 1 from secrets s where s.project_id = p.id and s.name = a.name)
		order by a.name`, projectID)
	if err != nil {
		return nil, err
	}
	type row struct {
		user          uuid.UUID
		name, version string
		ct, wrapped   []byte
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.user, &r.name, &r.ct, &r.wrapped, &r.version); err != nil {
			rows.Close()
			return nil, err
		}
		rs = append(rs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]NamedValue, 0, len(rs))
	for _, r := range rs {
		dek, err := s.unwrap(ctx, r.wrapped, r.version)
		if err != nil {
			return nil, err
		}
		v, err := OpenAccount(dek, r.user.String(), r.name, r.ct)
		if err != nil {
			return nil, fmt.Errorf("account secret %s: %w", r.name, err)
		}
		out = append(out, NamedValue{Name: r.name, Value: v})
	}
	return out, nil
}
