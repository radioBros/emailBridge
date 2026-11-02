package auth

import (
	_ "embed"
	"sort"
	"strings"
	"sync"
)

//go:embed commonpasswords.txt
var commonPasswordsFile string

var (
	commonPasswordsOnce sync.Once
	commonPasswords     []string
)

func loadCommonPasswords() {
	commonPasswordsOnce.Do(func() {
		for _, line := range strings.Split(commonPasswordsFile, "\n") {
			p := strings.TrimSpace(line)
			if p != "" {
				commonPasswords = append(commonPasswords, p)
			}
		}
	})
}

// isCommonPassword reports whether password appears in the bundled SecLists common-password list
// (case-insensitive). S10 §2.4.
func isCommonPassword(password string) bool {
	loadCommonPasswords()
	p := strings.ToLower(password)
	i := sort.SearchStrings(commonPasswords, p)
	return i < len(commonPasswords) && commonPasswords[i] == p
}
