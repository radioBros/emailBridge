package crypto

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// ---- fakes ----

type memStore struct {
	mu   sync.Mutex
	keys map[string]DataKey
	fail error
}

func newMemStore() *memStore { return &memStore{keys: map[string]DataKey{}} }

func (m *memStore) Active(_ context.Context, org string) (*DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return nil, m.fail
	}
	for _, k := range m.keys {
		ko := ""
		if k.OrganizationID != nil {
			ko = *k.OrganizationID
		}
		if ko == org && k.Status == DEKActive {
			kk := k
			return &kk, nil
		}
	}
	return nil, nil
}
func (m *memStore) Get(_ context.Context, id string) (*DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &k, nil
}
func (m *memStore) Insert(_ context.Context, k DataKey) (*DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[k.ID] = k
	return &k, nil
}
func (m *memStore) All(context.Context) ([]DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DataKey
	for _, k := range m.keys {
		out = append(out, k)
	}
	return out, nil
}
func (m *memStore) UpdateWrap(_ context.Context, id, kek string, w []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.keys[id]
	k.KEKID, k.Wrapped = kek, w
	m.keys[id] = k
	return nil
}
func (m *memStore) Retire(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.keys[id]
	k.Status = DEKRetired
	m.keys[id] = k
	return nil
}

func key(b byte) []byte { return bytes.Repeat([]byte{b}, KeySize) }

var idSeq int

func nextID() string { idSeq++; return fmt.Sprintf("dek_%026d", idSeq) }

func newCipher(t *testing.T) (*Cipher, *memStore, *Local) {
	t.Helper()
	l, err := NewLocal([]LocalKey{{ID: "k1", Key: key(1)}, {ID: "k2", Key: key(2)}}, "")
	if err != nil {
		t.Fatal(err)
	}
	st := newMemStore()
	return &Cipher{KEK: l, Store: st, NewID: nextID}, st, l
}

var aad = AAD{Collection: "accounts", Field: "imap.auth.pass", DocID: "acc_1"}

// ---- tests ----

func TestLocalProvider(t *testing.T) {
	if _, err := NewLocal(nil, ""); err == nil {
		t.Error("empty ring")
	}
	for _, ring := range [][]LocalKey{
		{{ID: "", Key: key(1)}}, {{ID: "a:b", Key: key(1)}}, {{ID: "a", Key: []byte("short")}},
		{{ID: "a", Key: key(1)}, {ID: "a", Key: key(2)}},
	} {
		if _, err := NewLocal(ring, ""); err == nil {
			t.Errorf("ring %v accepted", ring)
		}
	}
	if _, err := NewLocal([]LocalKey{{ID: "a", Key: key(1)}}, "b"); err == nil {
		t.Error("unknown active")
	}
	l, _ := NewLocal([]LocalKey{{ID: "a", Key: key(1)}, {ID: "b", Key: key(2)}}, "b")
	if l.Name() != "local" || l.ActiveKEK() != "local:b" {
		t.Fatal("identity")
	}
	id, w, err := l.Wrap(context.Background(), key(9))
	if err != nil || id != "local:b" {
		t.Fatal(err)
	}
	got, err := l.Unwrap(context.Background(), id, w)
	if err != nil || !bytes.Equal(got, key(9)) {
		t.Fatal("unwrap")
	}
	for _, kid := range []string{"vault:x", "local:zzz"} {
		if _, err := l.Unwrap(context.Background(), kid, w); !errors.Is(err, ErrUnknownKEK) {
			t.Errorf("%s: %v", kid, err)
		}
	}
	if _, err := l.Unwrap(context.Background(), "local:a", w); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong kek must fail: %v", err)
	}
	if _, err := l.Unwrap(context.Background(), "local:b", []byte("x")); !errors.Is(err, ErrDecrypt) {
		t.Errorf("short ciphertext: %v", err)
	}
}

func TestGenerateLocalKey(t *testing.T) {
	s, err := GenerateLocalKey("2026a")
	if err != nil {
		t.Fatal(err)
	}
	kid, b64, _ := strings.Cut(s, ":")
	raw, _ := base64.StdEncoding.DecodeString(b64)
	if kid != "2026a" || len(raw) != KeySize {
		t.Fatalf("bad key %q", s)
	}
	if _, err := GenerateLocalKey("a,b"); err == nil {
		t.Fatal("bad kid accepted")
	}
}

func TestEncryptDecryptAndAADBinding(t *testing.T) {
	c, st, _ := newCipher(t)
	ctx := context.Background()
	es, err := c.EncryptString(ctx, "org_a", "s3cret", aad)
	if err != nil {
		t.Fatal(err)
	}
	if es.KID != "local:k1" || !strings.HasPrefix(es.DEK, "dek_") || len(es.Nonce) != 12 || bytes.Contains(es.CT, []byte("s3cret")) {
		t.Fatalf("bad envelope %+v", es)
	}
	got, err := c.DecryptString(ctx, es, aad)
	if err != nil || got != "s3cret" {
		t.Fatalf("decrypt = %q %v", got, err)
	}
	for _, other := range []AAD{
		{Collection: "accounts", Field: "smtp.auth.pass", DocID: "acc_1"},
		{Collection: "accounts", Field: "imap.auth.pass", DocID: "acc_2"},
		{Collection: "webhookEndpoints", Field: "imap.auth.pass", DocID: "acc_1"},
	} {
		if _, err := c.Decrypt(ctx, es, other); !errors.Is(err, ErrDecrypt) {
			t.Errorf("AAD %v must not decrypt: %v", other, err)
		}
	}
	tampered := es
	tampered.CT = append([]byte(nil), es.CT...)
	tampered.CT[0] ^= 1
	if _, err := c.Decrypt(ctx, tampered, aad); !errors.Is(err, ErrDecrypt) {
		t.Error("tampered ct")
	}
	badNonce := es
	badNonce.Nonce = []byte{1}
	if _, err := c.Decrypt(ctx, badNonce, aad); !errors.Is(err, ErrDecrypt) {
		t.Error("bad nonce")
	}
	if v, err := c.Decrypt(ctx, EncryptedString{}, aad); v != nil || err != nil || !(EncryptedString{}).IsZero() {
		t.Error("zero value decrypts to nil")
	}
	missing := es
	missing.DEK = "dek_missing"
	if _, err := c.Decrypt(ctx, missing, aad); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing dek: %v", err)
	}
	// one DEK per organisation, separate for another org and for instance-level secrets
	es2, _ := c.EncryptString(ctx, "org_a", "x", aad)
	es3, _ := c.EncryptString(ctx, "org_b", "x", aad)
	es4, _ := c.EncryptString(ctx, "", "x", aad)
	if es2.DEK != es.DEK || es3.DEK == es.DEK || es4.DEK == es.DEK || len(st.keys) != 3 {
		t.Fatalf("dek reuse wrong: %s %s %s %s (%d)", es.DEK, es2.DEK, es3.DEK, es4.DEK, len(st.keys))
	}
	if inst := st.keys[es4.DEK]; inst.OrganizationID != nil {
		t.Fatal("instance key must have nil organization")
	}
}

func TestCacheAndStoreErrors(t *testing.T) {
	c, st, _ := newCipher(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	c.CacheTTL = time.Minute
	es, _ := c.EncryptString(ctx, "org_a", "v", aad)
	// remove the stored key: cached key still works
	k := st.keys[es.DEK]
	delete(st.keys, es.DEK)
	if _, err := c.Decrypt(ctx, es, aad); err != nil {
		t.Fatalf("cached: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := c.Decrypt(ctx, es, aad); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired cache must reload: %v", err)
	}
	st.keys[es.DEK] = k
	st.fail = errors.New("db down")
	if _, err := c.EncryptString(ctx, "org_a", "v", aad); err == nil {
		t.Fatal("store error must surface")
	}
	if c.ttl() != time.Minute {
		t.Fatal("ttl")
	}
	c2 := &Cipher{}
	if c2.ttl() != 10*time.Minute || c2.clock().IsZero() {
		t.Fatal("defaults")
	}
}

func TestRotateKEKAndDEK(t *testing.T) {
	c, st, _ := newCipher(t)
	ctx := context.Background()
	es, _ := c.EncryptString(ctx, "org_a", "v", aad)
	// new ring with k2 active
	l2, _ := NewLocal([]LocalKey{{ID: "k1", Key: key(1)}, {ID: "k2", Key: key(2)}}, "k2")
	c.KEK = l2
	c.cache = nil
	n, err := c.RotateKEK(ctx)
	if err != nil || n != 1 || st.keys[es.DEK].KEKID != "local:k2" {
		t.Fatalf("rotate kek: %d %v %v", n, err, st.keys[es.DEK].KEKID)
	}
	if n, _ := c.RotateKEK(ctx); n != 0 {
		t.Fatal("second rotation must be a no-op")
	}
	// k1 can be removed now
	l3, _ := NewLocal([]LocalKey{{ID: "k2", Key: key(2)}}, "")
	c.KEK, c.cache = l3, nil
	if v, err := c.DecryptString(ctx, es, aad); err != nil || v != "v" {
		t.Fatalf("after kek removal: %v", err)
	}
	// DEK rotation
	if err := c.RotateDEK(ctx, "org_a"); err != nil {
		t.Fatal(err)
	}
	re, changed, err := c.Reencrypt(ctx, "org_a", es, aad)
	if err != nil || !changed || re.DEK == es.DEK {
		t.Fatalf("reencrypt: %v %v", changed, err)
	}
	if v, _ := c.DecryptString(ctx, re, aad); v != "v" {
		t.Fatal("reencrypted value")
	}
	if v, _ := c.DecryptString(ctx, es, aad); v != "v" {
		t.Fatal("old value must stay readable with the retired DEK")
	}
	if _, changed, _ := c.Reencrypt(ctx, "org_a", re, aad); changed {
		t.Fatal("already current")
	}
	if err := c.RotateDEK(ctx, "org_none"); err != nil {
		t.Fatal("rotating an org without a key is a no-op")
	}
	// unwrap failure during rotation surfaces
	st.keys["dek_bad"] = DataKey{ID: "dek_bad", KEKID: "local:gone", Status: DEKRetired}
	if _, err := c.RotateKEK(ctx); err == nil {
		t.Fatal("unknown kek during rotation must fail")
	}
}

func TestVaultProvider(t *testing.T) {
	var last string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "tok" || r.Header.Get("X-Vault-Namespace") != "ns" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch r.URL.Path {
		case "/v1/transit/encrypt/eb":
			last = in["plaintext"]
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"ciphertext": "vault:v1:" + in["plaintext"]}})
		case "/v1/transit/decrypt/eb":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"plaintext": strings.TrimPrefix(in["ciphertext"], "vault:v1:")}})
		case "/v1/transit/encrypt/empty":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	v := &Vault{Addr: srv.URL + "/", Token: "tok", Key: "eb", Namespace: "ns"}
	ctx := context.Background()
	if v.Name() != "vault" || v.ActiveKEK() != "vault:eb" {
		t.Fatal("identity")
	}
	id, w, err := v.Wrap(ctx, key(7))
	if err != nil || id != "vault:eb" || last == "" {
		t.Fatalf("wrap: %v", err)
	}
	got, err := v.Unwrap(ctx, id, w)
	if err != nil || !bytes.Equal(got, key(7)) {
		t.Fatalf("unwrap: %v", err)
	}
	if _, err := v.Unwrap(ctx, "vault:other", w); !errors.Is(err, ErrUnknownKEK) {
		t.Fatal("other key")
	}
	bad := &Vault{Addr: srv.URL, Token: "wrong", Key: "eb"}
	if _, _, err := bad.Wrap(ctx, key(1)); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("auth error: %v", err)
	}
	if _, _, err := (&Vault{Addr: srv.URL, Token: "tok", Namespace: "ns", Key: "empty"}).Wrap(ctx, key(1)); err == nil {
		t.Fatal("empty ciphertext")
	}
	if _, _, err := (&Vault{Addr: "http://127.0.0.1:1", Token: "tok", Key: "eb"}).Wrap(ctx, key(1)); err == nil {
		t.Fatal("unreachable vault")
	}
	// rotation with vault always rewraps (Vault versions keys internally)
	c := &Cipher{KEK: v, Store: newMemStore(), NewID: nextID}
	if _, err := c.EncryptString(ctx, "o", "x", aad); err != nil {
		t.Fatal(err)
	}
	if n, err := c.RotateKEK(ctx); err != nil || n != 1 {
		t.Fatalf("vault rotate: %d %v", n, err)
	}
}

type fakeKMS struct{ fail bool }

func (f fakeKMS) Encrypt(_ context.Context, in *kms.EncryptInput, _ ...func(*kms.Options)) (*kms.EncryptOutput, error) {
	if f.fail || in.EncryptionContext["purpose"] != "emailbridge-dek" {
		return nil, errors.New("kms denied")
	}
	return &kms.EncryptOutput{CiphertextBlob: append([]byte("k:"), in.Plaintext...)}, nil
}
func (f fakeKMS) Decrypt(_ context.Context, in *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	if f.fail {
		return nil, errors.New("kms denied")
	}
	return &kms.DecryptOutput{Plaintext: bytes.TrimPrefix(in.CiphertextBlob, []byte("k:"))}, nil
}

func TestKMSProvider(t *testing.T) {
	k := &KMS{Client: fakeKMS{}, KeyID: "arn:aws:kms:eu:1:key/x"}
	ctx := context.Background()
	if k.Name() != "awskms" || !strings.HasPrefix(k.ActiveKEK(), "awskms:arn") {
		t.Fatal("identity")
	}
	id, w, err := k.Wrap(ctx, key(3))
	if err != nil {
		t.Fatal(err)
	}
	got, err := k.Unwrap(ctx, id, w)
	if err != nil || !bytes.Equal(got, key(3)) {
		t.Fatal("unwrap")
	}
	if _, err := k.Unwrap(ctx, "local:x", w); !errors.Is(err, ErrUnknownKEK) {
		t.Fatal("foreign kek")
	}
	f := &KMS{Client: fakeKMS{fail: true}, KeyID: "x"}
	if _, _, err := f.Wrap(ctx, key(1)); err == nil {
		t.Fatal("wrap error")
	}
	if _, err := f.Unwrap(ctx, "awskms:x", w); err == nil {
		t.Fatal("unwrap error")
	}
}

func TestGCMKeySize(t *testing.T) {
	if _, err := seal([]byte("short"), []byte("x"), nil); err == nil {
		t.Fatal("short key must fail")
	}
	if _, err := open([]byte("short"), []byte("x"), nil); err == nil {
		t.Fatal("short key must fail")
	}
}

type failKEK struct {
	Local
	wrapErr, unwrapErr error
}

func (f *failKEK) Wrap(ctx context.Context, d []byte) (string, []byte, error) {
	if f.wrapErr != nil {
		return "", nil, f.wrapErr
	}
	return f.Local.Wrap(ctx, d)
}
func (f *failKEK) Unwrap(ctx context.Context, id string, w []byte) ([]byte, error) {
	if f.unwrapErr != nil {
		return nil, f.unwrapErr
	}
	return f.Local.Unwrap(ctx, id, w)
}

type failStore struct {
	*memStore
	insertErr, allErr, updateErr error
}

func (f *failStore) Insert(ctx context.Context, k DataKey) (*DataKey, error) {
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	return f.memStore.Insert(ctx, k)
}
func (f *failStore) All(ctx context.Context) ([]DataKey, error) {
	if f.allErr != nil {
		return nil, f.allErr
	}
	return f.memStore.All(ctx)
}
func (f *failStore) UpdateWrap(ctx context.Context, id, k string, w []byte) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	return f.memStore.UpdateWrap(ctx, id, k, w)
}

func TestErrorPaths(t *testing.T) {
	ctx := context.Background()
	base, _ := NewLocal([]LocalKey{{ID: "k1", Key: key(1)}}, "")
	boom := errors.New("boom")

	// KEK wrap failure while creating the first DEK
	fk := &failKEK{Local: *base, wrapErr: boom}
	c := &Cipher{KEK: fk, Store: newMemStore(), NewID: nextID}
	if _, err := c.EncryptString(ctx, "o", "x", aad); !errors.Is(err, boom) {
		t.Fatalf("wrap failure: %v", err)
	}
	// store insert failure
	fs := &failStore{memStore: newMemStore(), insertErr: boom}
	c = &Cipher{KEK: base, Store: fs, NewID: nextID}
	if _, err := c.EncryptString(ctx, "o", "x", aad); !errors.Is(err, boom) {
		t.Fatalf("insert failure: %v", err)
	}
	// unwrap failure on decrypt / reencrypt
	fs = &failStore{memStore: newMemStore()}
	fk = &failKEK{Local: *base}
	c = &Cipher{KEK: fk, Store: fs, NewID: nextID}
	es, err := c.EncryptString(ctx, "o", "x", aad)
	if err != nil {
		t.Fatal(err)
	}
	c.cache = nil
	fk.unwrapErr = boom
	if _, err := c.Decrypt(ctx, es, aad); !errors.Is(err, boom) {
		t.Fatalf("unwrap failure: %v", err)
	}
	if _, _, err := c.Reencrypt(ctx, "o", es, aad); !errors.Is(err, boom) {
		t.Fatalf("reencrypt active failure: %v", err)
	}
	fk.unwrapErr = nil
	_ = c.RotateDEK(ctx, "o")
	c.cache = nil
	bad := es
	bad.CT = []byte("garbage-garbage-garbage")
	if _, _, err := c.Reencrypt(ctx, "o", bad, aad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("reencrypt decrypt failure: %v", err)
	}
	// rotation failures
	fs.allErr = boom
	if _, err := c.RotateKEK(ctx); !errors.Is(err, boom) {
		t.Fatalf("all failure: %v", err)
	}
	fs.allErr = nil
	l2, _ := NewLocal([]LocalKey{{ID: "k1", Key: key(1)}, {ID: "k2", Key: key(2)}}, "k2")
	fk2 := &failKEK{Local: *l2, wrapErr: boom}
	c.KEK = fk2
	if _, err := c.RotateKEK(ctx); !errors.Is(err, boom) {
		t.Fatalf("rewrap failure: %v", err)
	}
	fk2.wrapErr = nil
	fs.updateErr = boom
	if _, err := c.RotateKEK(ctx); !errors.Is(err, boom) {
		t.Fatalf("update failure: %v", err)
	}
	if _, err := c.Encrypt(ctx, "o", nil, aad); err != nil {
		t.Fatal(err)
	}
	if err := (&Cipher{Store: &memStore{keys: map[string]DataKey{}, fail: boom}}).RotateDEK(ctx, "o"); !errors.Is(err, boom) {
		t.Fatalf("rotate dek failure: %v", err)
	}
}

func TestVaultUnwrapError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer srv.Close()
	v := &Vault{Addr: srv.URL, Token: "t", Key: "eb"}
	if _, err := v.Unwrap(context.Background(), "vault:eb", []byte("vault:v1:x")); err == nil {
		t.Fatal("expected error")
	}
	if _, _, err := (&Vault{Addr: "://bad", Key: "eb"}).Wrap(context.Background(), key(1)); err == nil {
		t.Fatal("bad URL")
	}
}

// Coverage note (S14 §1): the only statements not covered are the failure branches of crypto/rand.Read
// and of aes.NewCipher/cipher.NewGCM with a key whose length was already validated, plus json.Marshal of
// a map[string]string. None can be triggered without replacing the standard library.
