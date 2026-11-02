package crypto

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// Vault is the HashiCorp Vault Transit KEK provider (EB_VAULT_*, S9 §2.4). DEKs are encrypted and
// decrypted by Vault; the key material never leaves Vault.
type Vault struct {
	Addr      string // e.g. https://vault.example.com:8200
	Token     string
	Key       string // transit key name
	Namespace string
	Client    *http.Client
}

// Name implements KEKProvider.
func (v *Vault) Name() string { return "vault" }

// ActiveKEK implements KEKProvider. Vault versions keys internally ("vault:vN:" ciphertext prefix), so the
// KEK ID is the key name; rotation is done in Vault and rewrapping with `keys rotate-kek`.
func (v *Vault) ActiveKEK() string { return "vault:" + v.Key }

func (v *Vault) call(ctx context.Context, op string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := strings.TrimRight(v.Addr, "/") + "/v1/transit/" + op + "/" + v.Key
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", v.Token)
	req.Header.Set("Content-Type", "application/json")
	if v.Namespace != "" {
		req.Header.Set("X-Vault-Namespace", v.Namespace)
	}
	c := v.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("crypto: vault %s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("crypto: vault %s: status %d: %s", op, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Wrap implements KEKProvider.
func (v *Vault) Wrap(ctx context.Context, dek []byte) (string, []byte, error) {
	var out struct {
		Data struct {
			Ciphertext string `json:"ciphertext"`
		} `json:"data"`
	}
	if err := v.call(ctx, "encrypt", map[string]string{"plaintext": base64.StdEncoding.EncodeToString(dek)}, &out); err != nil {
		return "", nil, err
	}
	if out.Data.Ciphertext == "" {
		return "", nil, fmt.Errorf("crypto: vault encrypt returned no ciphertext")
	}
	return v.ActiveKEK(), []byte(out.Data.Ciphertext), nil
}

// Unwrap implements KEKProvider.
func (v *Vault) Unwrap(ctx context.Context, kekID string, wrapped []byte) ([]byte, error) {
	if kekID != v.ActiveKEK() {
		return nil, fmt.Errorf("%w %q", ErrUnknownKEK, kekID)
	}
	var out struct {
		Data struct {
			Plaintext string `json:"plaintext"`
		} `json:"data"`
	}
	if err := v.call(ctx, "decrypt", map[string]string{"ciphertext": string(wrapped)}, &out); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.Data.Plaintext)
}

// KMSAPI is the subset of the AWS KMS client the provider uses (mockable in tests).
type KMSAPI interface {
	Encrypt(ctx context.Context, in *kms.EncryptInput, optFns ...func(*kms.Options)) (*kms.EncryptOutput, error)
	Decrypt(ctx context.Context, in *kms.DecryptInput, optFns ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// KMS is the AWS KMS KEK provider (EB_AWS_KMS_KEY_ID, credentials from the default AWS chain).
type KMS struct {
	Client KMSAPI
	KeyID  string
}

// Name implements KEKProvider.
func (k *KMS) Name() string { return "awskms" }

// ActiveKEK implements KEKProvider.
func (k *KMS) ActiveKEK() string { return "awskms:" + k.KeyID }

var kmsContext = map[string]string{"purpose": "emailbridge-dek"}

// Wrap implements KEKProvider.
func (k *KMS) Wrap(ctx context.Context, dek []byte) (string, []byte, error) {
	out, err := k.Client.Encrypt(ctx, &kms.EncryptInput{KeyId: &k.KeyID, Plaintext: dek, EncryptionContext: kmsContext})
	if err != nil {
		return "", nil, fmt.Errorf("crypto: kms encrypt: %w", err)
	}
	return k.ActiveKEK(), out.CiphertextBlob, nil
}

// Unwrap implements KEKProvider.
func (k *KMS) Unwrap(ctx context.Context, kekID string, wrapped []byte) ([]byte, error) {
	if !strings.HasPrefix(kekID, "awskms:") {
		return nil, fmt.Errorf("%w %q", ErrUnknownKEK, kekID)
	}
	keyID := strings.TrimPrefix(kekID, "awskms:")
	out, err := k.Client.Decrypt(ctx, &kms.DecryptInput{KeyId: &keyID, CiphertextBlob: wrapped, EncryptionContext: kmsContext})
	if err != nil {
		return nil, fmt.Errorf("crypto: kms decrypt: %w", err)
	}
	return out.Plaintext, nil
}
