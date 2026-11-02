package sysmail

import "strings"

// DefaultAllowList is used in non-production when Options.AllowList (EB_SYSMAIL_ALLOW_LIST) is empty.
// It is empty: a non-production install sends no system mail until addresses are listed.
var DefaultAllowList = []string{}

// allowedRecipient reports whether to may receive system mail. Production always allows;
// otherwise the address must be in allow (case-insensitive).
func allowedRecipient(env, to string, allow []string) bool {
	if strings.EqualFold(strings.TrimSpace(env), "production") {
		return true
	}
	to = strings.ToLower(strings.TrimSpace(to))
	if to == "" {
		return false
	}
	if len(allow) == 0 {
		allow = DefaultAllowList
	}
	for _, a := range allow {
		if strings.ToLower(strings.TrimSpace(a)) == to {
			return true
		}
	}
	return false
}
