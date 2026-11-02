package crypto

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"
)

// EncryptedString is the stored form of an encrypted field (S1 §1.3): {kid, dek, nonce, ct}.
// KID is the KEK that wrapped the DEK at encryption time (informational), DEK the data key ID.
type EncryptedString struct {
	KID   string `bson:"kid" json:"-"`
	DEK   string `bson:"dek" json:"-"`
	Nonce []byte `bson:"nonce" json:"-"`
	CT    []byte `bson:"ct" json:"-"`
}

// IsZero reports whether the field holds no value.
func (e EncryptedString) IsZero() bool { return e.DEK == "" && len(e.CT) == 0 }

// AAD binds a ciphertext to its place: a value copied to another document, field or collection does
// not decrypt (S10 §6).
type AAD struct {
	Collection string
	Field      string
	DocID      string
}

func (a AAD) bytes() []byte { return []byte("eb1|" + a.Collection + "|" + a.Field + "|" + a.DocID) }

// DataKey is a stored DEK record (collection dataKeys, S1 §2).
type DataKey struct {
	ID             string    `bson:"_id"`
	OrganizationID *string   `bson:"organizationId"`
	KEKID          string    `bson:"kekId"`
	Wrapped        []byte    `bson:"wrapped"`
	Status         string    `bson:"status"` // active | retired
	CreatedAt      time.Time `bson:"createdAt"`
}

// DEK statuses.
const (
	DEKActive  = "active"
	DEKRetired = "retired"
)

// DataKeyStore persists data keys. The MongoDB implementation is MongoKeyStore.
type DataKeyStore interface {
	Active(ctx context.Context, orgID string) (*DataKey, error) // nil, nil when none
	Get(ctx context.Context, id string) (*DataKey, error)
	// Insert stores a new active DEK; when another node inserted one concurrently it returns that one.
	Insert(ctx context.Context, k DataKey) (*DataKey, error)
	All(ctx context.Context) ([]DataKey, error)
	UpdateWrap(ctx context.Context, id, kekID string, wrapped []byte) error
	Retire(ctx context.Context, id string) error
}

// ErrNotFound is returned by stores for a missing data key.
var ErrNotFound = errors.New("crypto: data key not found")

// Cipher encrypts and decrypts fields with per-organisation DEKs. Unwrapped DEKs are cached for
// CacheTTL (default 10 min) so the KEK provider is not called per field.
type Cipher struct {
	KEK      KEKProvider
	Store    DataKeyStore
	NewID    func() string // DEK IDs ("dek_…"), injected so this package does not import store
	CacheTTL time.Duration
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]cachedKey // by DEK ID
}

type cachedKey struct {
	key     []byte
	expires time.Time
}

func (c *Cipher) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Cipher) ttl() time.Duration {
	if c.CacheTTL > 0 {
		return c.CacheTTL
	}
	return 10 * time.Minute
}

// Encrypt encrypts plaintext with the organisation's active DEK (orgID "" = instance-level secrets).
func (c *Cipher) Encrypt(ctx context.Context, orgID string, plaintext []byte, aad AAD) (EncryptedString, error) {
	dk, key, err := c.activeKey(ctx, orgID)
	if err != nil {
		return EncryptedString{}, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return EncryptedString{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return EncryptedString{}, err
	}
	return EncryptedString{KID: dk.KEKID, DEK: dk.ID, Nonce: nonce, CT: gcm.Seal(nil, nonce, plaintext, aad.bytes())}, nil
}

// EncryptString is Encrypt for strings.
func (c *Cipher) EncryptString(ctx context.Context, orgID, plaintext string, aad AAD) (EncryptedString, error) {
	return c.Encrypt(ctx, orgID, []byte(plaintext), aad)
}

// Decrypt decrypts a field. Any mismatch (key, data, AAD) yields ErrDecrypt.
func (c *Cipher) Decrypt(ctx context.Context, es EncryptedString, aad AAD) ([]byte, error) {
	if es.IsZero() {
		return nil, nil
	}
	key, err := c.key(ctx, es.DEK)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(es.Nonce) != gcm.NonceSize() {
		return nil, ErrDecrypt
	}
	pt, err := gcm.Open(nil, es.Nonce, es.CT, aad.bytes())
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// DecryptString is Decrypt for strings.
func (c *Cipher) DecryptString(ctx context.Context, es EncryptedString, aad AAD) (string, error) {
	b, err := c.Decrypt(ctx, es, aad)
	return string(b), err
}

// Reencrypt re-encrypts es with the organisation's active DEK if it uses another one (DEK rotation).
func (c *Cipher) Reencrypt(ctx context.Context, orgID string, es EncryptedString, aad AAD) (EncryptedString, bool, error) {
	dk, _, err := c.activeKey(ctx, orgID)
	if err != nil {
		return es, false, err
	}
	if es.DEK == dk.ID || es.IsZero() {
		return es, false, nil
	}
	pt, err := c.Decrypt(ctx, es, aad)
	if err != nil {
		return es, false, err
	}
	out, err := c.Encrypt(ctx, orgID, pt, aad)
	return out, err == nil, err
}

func (c *Cipher) activeKey(ctx context.Context, orgID string) (*DataKey, []byte, error) {
	dk, err := c.Store.Active(ctx, orgID)
	if err != nil {
		return nil, nil, err
	}
	if dk == nil {
		dk, err = c.createDEK(ctx, orgID)
		if err != nil {
			return nil, nil, err
		}
	}
	key, err := c.key(ctx, dk.ID)
	return dk, key, err
}

func (c *Cipher) createDEK(ctx context.Context, orgID string) (*DataKey, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	kekID, wrapped, err := c.KEK.Wrap(ctx, key)
	if err != nil {
		return nil, err
	}
	var org *string
	if orgID != "" {
		org = &orgID
	}
	return c.Store.Insert(ctx, DataKey{ID: c.NewID(), OrganizationID: org, KEKID: kekID, Wrapped: wrapped, Status: DEKActive, CreatedAt: c.clock().UTC()})
}

func (c *Cipher) key(ctx context.Context, dekID string) ([]byte, error) {
	now := c.clock()
	c.mu.Lock()
	if k, ok := c.cache[dekID]; ok && now.Before(k.expires) {
		c.mu.Unlock()
		return k.key, nil
	}
	c.mu.Unlock()
	dk, err := c.Store.Get(ctx, dekID)
	if err != nil {
		return nil, err
	}
	key, err := c.KEK.Unwrap(ctx, dk.KEKID, dk.Wrapped)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.cache == nil {
		c.cache = map[string]cachedKey{}
	}
	c.cache[dekID] = cachedKey{key: key, expires: now.Add(c.ttl())}
	c.mu.Unlock()
	return key, nil
}

// RotateKEK rewraps every DEK with the provider's active KEK (after a new key was added to the ring or
// rotated in Vault/KMS). It is safe to repeat; it returns the number of DEKs rewrapped.
func (c *Cipher) RotateKEK(ctx context.Context) (int, error) {
	keys, err := c.Store.All(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	active := c.KEK.ActiveKEK()
	for _, dk := range keys {
		if dk.KEKID == active && c.KEK.Name() != "vault" {
			continue
		}
		plain, err := c.KEK.Unwrap(ctx, dk.KEKID, dk.Wrapped)
		if err != nil {
			return n, fmt.Errorf("crypto: rotate %s: %w", dk.ID, err)
		}
		kekID, wrapped, err := c.KEK.Wrap(ctx, plain)
		if err != nil {
			return n, fmt.Errorf("crypto: rotate %s: %w", dk.ID, err)
		}
		if err := c.Store.UpdateWrap(ctx, dk.ID, kekID, wrapped); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// RotateDEK retires the organisation's active DEK; the next Encrypt creates a new one. Existing fields
// stay readable (retired DEKs are kept) and are moved with Reencrypt by each domain's rotation job.
func (c *Cipher) RotateDEK(ctx context.Context, orgID string) error {
	dk, err := c.Store.Active(ctx, orgID)
	if err != nil || dk == nil {
		return err
	}
	return c.Store.Retire(ctx, dk.ID)
}
