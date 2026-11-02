package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
)

// secretsCmd implements `emailbridge secrets init [--dir DIR] [--force]`.
// It creates every secret file the compose stacks expect. Values are never printed.
func secretsCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "init" {
		_, _ = fmt.Fprintln(stderr, "usage: emailbridge secrets init [--dir DIR] [--force]")
		return 2
	}
	dir := "./secrets"
	force := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			rest := args[i+1:]
			if len(rest) == 0 {
				_, _ = fmt.Fprintln(stderr, "secrets init: --dir requires a path")
				return 2
			}
			dir = rest[0]
			i++
		case "--force":
			force = true
		default:
			_, _ = fmt.Fprintf(stderr, "secrets init: unknown flag %q\n", args[i])
			return 2
		}
	}
	absDir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	dir = absDir
	if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:gosec // G703: operator-chosen secrets directory for install
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	rootPW, err := randomBase64URL(32)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	keyfile, err := randomBase64(756)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	s3Access, err := randomBase64URL(32)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	s3Secret, err := randomBase64URL(32)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	authSecret, err := randomBase64(32)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	kid := time.Now().UTC().Format("20060102")
	keys, err := crypto.GenerateLocalKey(kid)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	mongoURI := fmt.Sprintf(
		"mongodb://root:%s@mongo:27017/emailbridge?authSource=admin&replicaSet=rs0",
		rootPW,
	)

	files := []struct {
		name string
		data string
	}{
		{"mongo_keyfile", keyfile},
		{"mongo_root_password", rootPW},
		{"mongo_uri", mongoURI},
		{"s3_access_key", s3Access},
		{"s3_secret_key", s3Secret},
		{"keys", keys},
		{"auth_secret", authSecret},
	}

	written := make([]string, 0, len(files))
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		if !force {
			if _, err := os.Stat(path); err == nil { //nolint:gosec // G703: path under Abs-cleaned secrets dir
				_, _ = fmt.Fprintf(stderr, "secrets init: %s exists (use --force to overwrite)\n", path)
				return 1
			} else if !os.IsNotExist(err) {
				_, _ = fmt.Fprintln(stderr, err)
				return 1
			}
		}
		if err := os.WriteFile(path, []byte(f.data), 0o600); err != nil { //nolint:gosec // G703: path under Abs-cleaned secrets dir
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		written = append(written, f.name)
	}
	for _, name := range written {
		_, _ = fmt.Fprintln(stdout, name)
	}
	return 0
}

func randomBase64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func randomBase64URL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
