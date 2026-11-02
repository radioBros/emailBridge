// Package passhash hashes and verifies protocol passwords (MSA submission, IMAP proxy) with argon2id.
//
// New hashes use the PHC string format
//
//	$argon2id$v=19$m=<KiB>,t=<iterations>,p=<lanes>$<salt b64>$<key b64>
//
// with the OWASP minimum parameters (19 MiB, 2 iterations, 1 lane). Legacy hashes written before the switch
// ("v1$<salt hex>$<sha256(salt||password) hex>") still verify, and NeedsRehash reports them so the caller
// re-hashes the password after the next successful login.
package passhash

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Current argon2id parameters.
const (
	memoryKiB  uint32 = 19 * 1024
	iterations uint32 = 2
	lanes      uint8  = 1
	saltLen           = 16
	keyLen     uint32 = 32

	// Upper bounds accepted when verifying a stored hash, so a tampered record cannot make one login
	// allocate unbounded memory or CPU.
	maxMemoryKiB  uint32 = 256 * 1024
	maxIterations uint32 = 10
	maxLanes      uint8  = 16
	maxKeyLen            = 64

	legacyPrefix = "v1"
	argonPrefix  = "$argon2id$"
)

var b64 = base64.RawStdEncoding

// dummyHash is verified when there is no stored credential, so a missing account or credential costs about
// as much as a wrong password (no user enumeration by timing).
var dummyHash = encode(make([]byte, saltLen), argon2.IDKey([]byte("emailbridge-dummy"), make([]byte, saltLen),
	iterations, memoryKiB, lanes, keyLen))

func encode(salt, key []byte) string {
	return fmt.Sprintf("%sv=%d$m=%d,t=%d,p=%d$%s$%s", argonPrefix, argon2.Version, memoryKiB, iterations, lanes,
		b64.EncodeToString(salt), b64.EncodeToString(key))
}

// Hash returns an argon2id PHC string for password with a fresh random salt.
func Hash(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return encode(salt, argon2.IDKey([]byte(password), salt, iterations, memoryKiB, lanes, keyLen)), nil
}

// Verify reports whether password matches hash (argon2id or legacy v1). The key compare is constant time.
func Verify(hash, password string) bool {
	switch {
	case strings.HasPrefix(hash, argonPrefix):
		return verifyArgon(hash, password)
	case strings.HasPrefix(hash, legacyPrefix+"$"):
		return verifyLegacy(hash, password)
	default:
		return false
	}
}

// Burn runs one verification against a dummy hash and discards the result. Call it on the "no such account /
// no credential" path so that path costs the same as a wrong password.
func Burn(password string) {
	_ = verifyArgon(dummyHash, password)
}

// NeedsRehash reports whether hash is not an argon2id hash with the current parameters.
func NeedsRehash(hash string) bool {
	p, ok := parseArgon(hash)
	if !ok {
		return true
	}
	return p.memory != memoryKiB || p.iterations != iterations || p.lanes != lanes || len(p.key) != int(keyLen)
}

type argonParams struct {
	memory     uint32
	iterations uint32
	lanes      uint8
	salt, key  []byte
}

func parseArgon(hash string) (argonParams, bool) {
	var p argonParams
	if !strings.HasPrefix(hash, argonPrefix) {
		return p, false
	}
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	parts := strings.Split(hash, "$")
	if len(parts) != 6 {
		return p, false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.iterations, &p.lanes); err != nil {
		return p, false
	}
	if p.memory == 0 || p.memory > maxMemoryKiB || p.iterations == 0 || p.iterations > maxIterations ||
		p.lanes == 0 || p.lanes > maxLanes {
		return p, false
	}
	var err error
	if p.salt, err = b64.DecodeString(parts[4]); err != nil || len(p.salt) < 8 {
		return p, false
	}
	if p.key, err = b64.DecodeString(parts[5]); err != nil || len(p.key) < 16 || len(p.key) > maxKeyLen {
		return p, false
	}
	return p, true
}

func verifyArgon(hash, password string) bool {
	p, ok := parseArgon(hash)
	if !ok {
		return false
	}
	n := uint32(len(p.key)) //nolint:gosec // len(p.key) is bounded to maxKeyLen by parseArgon
	got := argon2.IDKey([]byte(password), p.salt, p.iterations, p.memory, p.lanes, n)
	return subtle.ConstantTimeCompare(got, p.key) == 1
}

func verifyLegacy(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 3 || parts[0] != legacyPrefix {
		return false
	}
	salt, err := hex.DecodeString(parts[1])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := hex.DecodeString(parts[2])
	if err != nil || len(want) != sha256.Size {
		return false
	}
	sum := sha256.Sum256(append(salt, []byte(password)...))
	return subtle.ConstantTimeCompare(sum[:], want) == 1
}
