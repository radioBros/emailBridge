package imapproxy

import (
	"context"

	"github.com/radiobros/emailbridge/internal/crypto/passhash"
)

// hashPassword returns an argon2id hash (internal/crypto/passhash).
func hashPassword(password string) (string, error) { return passhash.Hash(password) }

// verifyPassword checks password against an argon2id or legacy v1 (salted SHA-256) hash.
func verifyPassword(hash, password string) bool { return passhash.Verify(hash, password) }

// credentialRehasher is implemented by credential stores that can upgrade a hash in place. The update only
// applies while the stored hash still equals oldHash, so it never overwrites a concurrent password change.
type credentialRehasher interface {
	Rehash(ctx context.Context, accountID, oldHash, newHash string) error
}

// upgradeHash re-hashes a legacy or outdated hash after a successful login. Failures are not fatal: the old
// hash keeps working and the upgrade is retried on the next login.
func upgradeHash(ctx context.Context, store any, accountID, oldHash, password string) bool {
	if !passhash.NeedsRehash(oldHash) {
		return false
	}
	rh, ok := store.(credentialRehasher)
	if !ok {
		return false
	}
	h, err := passhash.Hash(password)
	if err != nil {
		return false
	}
	return rh.Rehash(ctx, accountID, oldHash, h) == nil
}
