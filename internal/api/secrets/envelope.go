package secrets

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// SealValue encrypts value under the account's data key, bound to
// projectID and name as every secret is (DECISIONS I-433), and hands back
// the ciphertext with the wrapped key and its Key Vault version. It is
// for a caller that owns its own table and wants the same envelope: a
// webhook route's token (DECISIONS I-1110) is a tenant secret, so it is
// held the way `docs/features/secrets.md` says one is held, rather than
// in a column of its own shape.
//
// The data key is the account's existing one when it has any secret, and
// a fresh wrapped key otherwise, exactly as a `secrets set` would mint.
func (s *Store) SealValue(ctx context.Context, tx pgx.Tx, userID, projectID, name string, value []byte) (ciphertext, wrapped []byte, version string, err error) {
	dek, wrapped, version, err := s.userDEK(ctx, tx, userID)
	if err != nil {
		return nil, nil, "", err
	}
	ciphertext, err = Seal(dek, projectID, name, value)
	if err != nil {
		return nil, nil, "", err
	}
	return ciphertext, wrapped, version, nil
}

// OpenValue reverses SealValue for a row that carries its own wrapped key
// and version.
func (s *Store) OpenValue(ctx context.Context, wrapped []byte, version, projectID, name string, ciphertext []byte) ([]byte, error) {
	dek, err := s.unwrap(ctx, wrapped, version)
	if err != nil {
		return nil, err
	}
	return Open(dek, projectID, name, ciphertext)
}
