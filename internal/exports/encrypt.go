package exports

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// ErrDecrypt is returned when the export blob cannot be opened.
var ErrDecrypt = errors.New("exports: decrypt failed")

// sealAESGCM encrypts plaintext with a random nonce; layout: magic + nonce + ciphertext.
func sealAESGCM(key, plaintext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("exports: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(magic)+len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, magic...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plaintext, nil), nil
}

// openAESGCM decrypts a blob produced by sealAESGCM.
func openAESGCM(key, blob []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrDecrypt
	}
	if len(blob) < len(magic)+12 {
		return nil, ErrDecrypt
	}
	if string(blob[:len(magic)]) != magic {
		return nil, ErrDecrypt
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrDecrypt
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrDecrypt
	}
	nonceSize := gcm.NonceSize()
	rest := blob[len(magic):]
	if len(rest) < nonceSize {
		return nil, ErrDecrypt
	}
	nonce, ct := rest[:nonceSize], rest[nonceSize:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}
