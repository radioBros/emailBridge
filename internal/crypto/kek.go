// Package crypto implements envelope encryption for secrets at rest (S10 §6): per-organisation data
// encryption keys (DEKs) wrapped by a key-encryption key (KEK) from a pluggable provider, and
// AES-256-GCM field encryption bound to its place by additional authenticated data.
package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// KEKProvider wraps and unwraps data keys. Implementations: Local, Vault (Transit), KMS (AWS).
type KEKProvider interface {
	// Name is the provider kind ("local", "vault", "awskms").
	Name() string
	// ActiveKEK is the KEK ID used for new wraps.
	ActiveKEK() string
	// Wrap encrypts a DEK with the active KEK.
	Wrap(ctx context.Context, dek []byte) (kekID string, wrapped []byte, err error)
	// Unwrap decrypts a DEK wrapped by kekID.
	Unwrap(ctx context.Context, kekID string, wrapped []byte) ([]byte, error)
}

// ErrUnknownKEK is returned when a wrapped key names a KEK the provider does not have.
var ErrUnknownKEK = errors.New("crypto: unknown key-encryption key")

// KeySize is the size of every symmetric key (AES-256).
const KeySize = 32

// LocalKey is one entry of the local key ring.
type LocalKey struct {
	ID  string
	Key []byte
}

// Local is the default KEK provider: a key ring from EB_KEYS_LOCAL (S9 §2.4). The active key wraps new
// DEKs; older keys stay in the ring to unwrap existing DEKs until `keys rotate-kek` rewrapped them.
type Local struct {
	keys   map[string][]byte
	active string
}

// NewLocal builds the local provider. active must be one of the ring's IDs ("" = the first key).
func NewLocal(ring []LocalKey, active string) (*Local, error) {
	if len(ring) == 0 {
		return nil, errors.New("crypto: local key ring is empty")
	}
	l := &Local{keys: map[string][]byte{}}
	for _, k := range ring {
		if k.ID == "" || strings.ContainsAny(k.ID, ":, ") {
			return nil, fmt.Errorf("crypto: invalid key ID %q", k.ID)
		}
		if len(k.Key) != KeySize {
			return nil, fmt.Errorf("crypto: key %q must be %d bytes, got %d", k.ID, KeySize, len(k.Key))
		}
		if _, dup := l.keys[k.ID]; dup {
			return nil, fmt.Errorf("crypto: key ID %q used twice", k.ID)
		}
		l.keys[k.ID] = append([]byte(nil), k.Key...)
	}
	if active == "" {
		active = ring[0].ID
	}
	if _, ok := l.keys[active]; !ok {
		return nil, fmt.Errorf("crypto: active key %q is not in the ring", active)
	}
	l.active = active
	return l, nil
}

// Name implements KEKProvider.
func (l *Local) Name() string { return "local" }

// ActiveKEK implements KEKProvider.
func (l *Local) ActiveKEK() string { return "local:" + l.active }

var dekAAD = []byte("emailbridge-dek-v1")

// Wrap implements KEKProvider.
func (l *Local) Wrap(_ context.Context, dek []byte) (string, []byte, error) {
	ct, err := seal(l.keys[l.active], dek, dekAAD)
	if err != nil {
		return "", nil, err
	}
	return l.ActiveKEK(), ct, nil
}

// Unwrap implements KEKProvider.
func (l *Local) Unwrap(_ context.Context, kekID string, wrapped []byte) ([]byte, error) {
	id, ok := strings.CutPrefix(kekID, "local:")
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownKEK, kekID)
	}
	key, ok := l.keys[id]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownKEK, kekID)
	}
	return open(key, wrapped, dekAAD)
}

// GenerateLocalKey returns a new key-ring entry "kid:base64" for EB_KEYS_LOCAL.
func GenerateLocalKey(kid string) (string, error) {
	if kid == "" || strings.ContainsAny(kid, ":, ") {
		return "", fmt.Errorf("crypto: invalid key ID %q", kid)
	}
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		return "", err
	}
	return kid + ":" + base64.StdEncoding.EncodeToString(k), nil
}

// seal encrypts with AES-256-GCM; the output is nonce || ciphertext.
func seal(key, plaintext, aad []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

func open(key, sealed, aad []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrDecrypt
	}
	pt, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("crypto: key must be %d bytes", KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// ErrDecrypt is returned for any authentication failure (wrong key, tampered data, wrong AAD). It never
// says which, so it cannot be used as an oracle.
var ErrDecrypt = errors.New("crypto: decryption failed")
