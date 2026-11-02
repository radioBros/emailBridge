package passhash

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func legacyHash(salt []byte, password string) string {
	sum := sha256.Sum256(append(append([]byte(nil), salt...), []byte(password)...))
	return legacyPrefix + "$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(sum[:])
}

func TestHashVerifyArgon(t *testing.T) {
	h, err := Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("format: %s", h)
	}
	if !Verify(h, "correct horse") {
		t.Fatal("verify ok")
	}
	if Verify(h, "wrong") {
		t.Fatal("wrong password accepted")
	}
	if NeedsRehash(h) {
		t.Fatal("current hash needs no rehash")
	}
	h2, _ := Hash("correct horse")
	if h2 == h {
		t.Fatal("salt must differ")
	}
}

func TestLegacyVerifiesAndNeedsRehash(t *testing.T) {
	h := legacyHash([]byte("0123456789abcdef"), "old-pass")
	if !Verify(h, "old-pass") {
		t.Fatal("legacy verify")
	}
	if Verify(h, "other") {
		t.Fatal("legacy wrong accepted")
	}
	if !NeedsRehash(h) {
		t.Fatal("legacy must be rehashed")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	key := strings.Repeat("A", 43)
	cases := []string{
		"", "plain", "v1$zz$00", "v1$00$abcd", "v1$$" + strings.Repeat("0", 64), "v2$00$00",
		"v1$00$" + strings.Repeat("zz", 32), "v1$00",
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$",
		"$argon2id$v=18$m=19456,t=2,p=1$c2FsdHNhbHQ$" + key,
		"$argon2id$vv$m=19456,t=2,p=1$c2FsdHNhbHQ$" + key,
		"$argon2id$v=19$m=999999999,t=2,p=1$c2FsdHNhbHQ$" + key,
		"$argon2id$v=19$m=19456,t=99,p=1$c2FsdHNhbHQ$" + key,
		"$argon2id$v=19$m=19456,t=2,p=0$c2FsdHNhbHQ$" + key,
		"$argon2id$v=19$garbage$c2FsdHNhbHQ$" + key,
		"$argon2id$v=19$m=19456,t=2,p=1$!!$" + key,
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHQ$!!",
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHQ$QUFB",
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHQ",
	}
	for _, c := range cases {
		if Verify(c, "x") {
			t.Fatalf("accepted %q", c)
		}
		if !NeedsRehash(c) {
			t.Fatalf("malformed must need rehash: %q", c)
		}
	}
}

func TestNeedsRehashOnOldParams(t *testing.T) {
	h, _ := Hash("p")
	weaker := strings.Replace(h, "t=2", "t=1", 1)
	if !NeedsRehash(weaker) {
		t.Fatal("old params need rehash")
	}
	if Verify(weaker, "p") {
		t.Fatal("changing params changes the key")
	}
}

func TestBurnAndDummy(t *testing.T) {
	Burn("anything")
	if !Verify(dummyHash, "emailbridge-dummy") {
		t.Fatal("dummy hash is well formed")
	}
}
