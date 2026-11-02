package main

import (
	"fmt"
	"io"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
)

// keysCmd implements `emailbridge keys generate [kid]`: it prints a new EB_KEYS_LOCAL ring entry.
// `keys rotate-kek` and `keys rotate-dek` are wired once the config loader lands (they need the stores).
func keysCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "generate" {
		_, _ = fmt.Fprintln(stderr, "usage: emailbridge keys generate [key-id]")
		return 2
	}
	kid := time.Now().UTC().Format("20060102")
	if len(args) > 1 {
		kid = args[1]
	}
	entry, err := crypto.GenerateLocalKey(kid)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, entry)
	return 0
}
